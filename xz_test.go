package simplecloud_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/mtgban/simplecloud"
	"github.com/ulikunitz/xz"
)

// blazerChunkSize is the download chunk size blazer's B2 reader defaults to.
// It is the interval at which that reader returns (0, nil) — see
// TestXZ_LiveB2BlazerReturnsZeroNilReads, which measures it against a real
// bucket rather than taking blazer's source for it.
const blazerChunkSize = 1e7

// compressiblePayload returns n bytes carrying four bits of entropy each. That
// matters: incompressible input makes LZMA2 emit *uncompressed* chunks, which
// are read in bulk and never reach the byte-at-a-time path where the
// ulikunitz/xz#79 bug lives. Real payloads here are JSON, which compresses.
func compressiblePayload(n int) []byte {
	r := rand.New(rand.NewSource(7))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + r.Intn(16))
	}
	return b
}

func xzCompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// zeroNilReader returns (0, nil) whenever the byte offset reaches a multiple of
// every. That is legal under the io.Reader contract — callers are told to treat
// it as "nothing happened" and retry — and it is exactly what blazer's B2
// reader does at each download-chunk boundary. ulikunitz/xz v0.5.16 and earlier
// turned it into a fatal "breader.ReadByte: no data" instead, because the LZMA
// range decoder pulls every compressed byte through a one-byte read that had no
// retry (ulikunitz/xz#79, fixed in v0.5.17).
type zeroNilReader struct {
	r     io.Reader
	every int64
	off   int64
	fired int
}

func (z *zeroNilReader) Read(p []byte) (int, error) {
	if z.off > 0 && z.off%z.every == 0 {
		z.off++ // only stall once per boundary, as blazer does
		z.fired++
		return 0, nil
	}
	if room := z.every - z.off%z.every; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := z.r.Read(p)
	z.off += int64(n)
	return n, err
}

// zeroNilBucket serves one object through a zeroNilReader.
type zeroNilBucket struct {
	data  []byte
	every int64
	last  *zeroNilReader
}

func (b *zeroNilBucket) NewReader(_ context.Context, _ string) (io.ReadCloser, error) {
	b.last = &zeroNilReader{r: bytes.NewReader(b.data), every: b.every}
	return io.NopCloser(b.last), nil
}

// TestInitReader_XZToleratesZeroByteReads pins the property the whole B2 read
// path depends on: the xz decoder must survive a source that returns (0, nil).
// Both subtests fail against ulikunitz/xz v0.5.16 with "breader.ReadByte: no
// data".
//
// The boundary interval is scaled down from blazer's 10 MB so the test stays
// fast; the mechanism is identical, and the real interval at the real object
// size is covered by TestXZ_LiveB2LargeObject.
func TestInitReader_XZToleratesZeroByteReads(t *testing.T) {
	payload := compressiblePayload(1 << 20)
	compressed := xzCompress(t, payload)
	if len(compressed) >= len(payload) {
		t.Fatalf("payload did not compress (%d -> %d); LZMA2 would emit "+
			"uncompressed chunks and skip the path under test",
			len(payload), len(compressed))
	}

	for _, every := range []int64{1 << 10, 1 << 14} {
		t.Run(fmt.Sprintf("stall every %d bytes", every), func(t *testing.T) {
			bucket := &zeroNilBucket{data: compressed, every: every}
			r, err := simplecloud.InitReader(ctx, bucket, "object.xz")
			if err != nil {
				t.Fatalf("InitReader: %v", err)
			}
			defer r.Close()

			got, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("reading through a stalling source: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
			}
			if bucket.last.fired == 0 {
				t.Fatal("the source never returned (0, nil); this test proved nothing")
			}
			t.Logf("decoded %d bytes across %d zero-byte reads", len(got), bucket.last.fired)
		})
	}
}

func liveB2(t *testing.T) (*simplecloud.B2Bucket, string) {
	t.Helper()
	id := os.Getenv("B2_APPLICATION_KEY_ID_DATASTORE")
	key := os.Getenv("B2_APPLICATION_KEY_DATASTORE")
	bucket := os.Getenv("SIMPLECLOUD_TEST_B2_BUCKET")
	if id == "" || key == "" || bucket == "" {
		t.Skip("B2 credentials or bucket not set")
	}
	b, err := simplecloud.NewB2Client(context.Background(), id, key, bucket)
	if err != nil {
		t.Fatal(err)
	}
	return b, bucket
}

// countingReader records how often the wrapped reader returns (0, nil).
type countingReader struct {
	r       io.Reader
	zeroNil int
	off     int64
	firstAt int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n == 0 && err == nil {
		c.zeroNil++
		if c.firstAt == 0 {
			c.firstAt = c.off
		}
	}
	c.off += int64(n)
	return n, err
}

// TestXZ_LiveB2BlazerReturnsZeroNilReads pins the premise the xz fix rests on:
// that blazer's B2 reader really does return (0, nil), at the chunk boundary
// and not merely in theory. If blazer ever stops doing this the offline test
// above becomes a test of nothing, and this is what will say so.
//
//	B2_APPLICATION_KEY_ID_DATASTORE=... B2_APPLICATION_KEY_DATASTORE=... \
//	  SIMPLECLOUD_TEST_B2_BUCKET=my-bucket go test -run TestXZ_LiveB2 -v
func TestXZ_LiveB2BlazerReturnsZeroNilReads(t *testing.T) {
	b, _ := liveB2(t)
	ctx := context.Background()

	payload := compressiblePayload(int(2.5 * blazerChunkSize))
	obj := fmt.Sprintf("simplecloud-selftest/zero-nil-probe-%d.bin", time.Now().UnixNano())
	w := b.Bucket.Object(obj).NewWriter(ctx)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.Bucket.Object(obj).Delete(context.Background()); err != nil {
			t.Errorf("cleanup of %q failed, remove it by hand: %v", obj, err)
		}
	})

	r := b.Bucket.Object(obj).NewReader(ctx)
	defer r.Close()
	cr := &countingReader{r: r}
	if _, err := io.Copy(io.Discard, cr); err != nil {
		t.Fatal(err)
	}

	if cr.zeroNil == 0 {
		t.Fatal("blazer no longer returns (0, nil); the offline xz test now models nothing")
	}
	if cr.firstAt != blazerChunkSize {
		t.Errorf("first (0, nil) at offset %d, expected the %g chunk boundary",
			cr.firstAt, blazerChunkSize)
	}
	t.Logf("blazer returned (0, nil) %d times over %d bytes, first at offset %d",
		cr.zeroNil, cr.off, cr.firstAt)
}

// TestXZ_LiveB2LargeObject is the test the offline one cannot be: a real B2
// object whose *compressed* size crosses blazer's real 10 MB chunk boundary,
// round-tripped through InitWriter/InitReader.
//
// Mocks were insufficient for this package before, and so were small live
// objects: the bug only appears once the compressed stream spans a boundary.
func TestXZ_LiveB2LargeObject(t *testing.T) {
	b, bucket := liveB2(t)
	ctx := context.Background()

	payload := compressiblePayload(24 << 20)

	obj := fmt.Sprintf("simplecloud-selftest/xz-chunk-boundary-%d.xz", time.Now().UnixNano())
	t.Cleanup(func() {
		if err := b.Bucket.Object(obj).Delete(context.Background()); err != nil {
			t.Errorf("cleanup of %q failed, remove it by hand: %v", obj, err)
		}
	})

	w, err := simplecloud.InitWriter(ctx, b, obj)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	attrs, err := b.Bucket.Object(obj).Attrs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("uploaded %s to %s: %d bytes compressed from %d (%.1f chunk boundaries)",
		obj, bucket, attrs.Size, len(payload), float64(attrs.Size)/blazerChunkSize)
	if attrs.Size < blazerChunkSize {
		t.Fatalf("compressed object is %d bytes, below the %g byte chunk size: "+
			"this test would pass even with the bug present", attrs.Size, blazerChunkSize)
	}

	r, err := simplecloud.InitReader(ctx, b, obj)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading back across a chunk boundary: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}
