package simplecloud

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	xzReader "github.com/xi2/xz"
)

// testdata/blocks-*.xz hold 4 KiB of JSON lines in four blocks each, one
// file per check type, written by `xz -T2 --block-size=1KiB`, so every
// block header records its sizes. testdata/xz-utils is xz-utils' own test
// suite, as xi2 ships it: blocks without sizes, broken and unsupported files.

func decodeXZ(data []byte) ([]byte, error) {
	z, err := newXZReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	return io.ReadAll(z)
}

func decodeXi2(data []byte) ([]byte, error) {
	z, err := xzReader.NewReader(bytes.NewReader(data), 0)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(z)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Every file decodes to what xi2 alone makes of it, or fails where xi2 does.
func TestXZReader_MatchesXi2(t *testing.T) {
	inputs := map[string][]byte{}
	for _, pattern := range []string{"testdata/*.xz", "testdata/xz-utils/*.xz"} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			inputs[path] = data
		}
	}
	if len(inputs) < 60 {
		t.Fatalf("found %d fixtures, want the xz-utils suite and the block files", len(inputs))
	}

	// Streams one after another: parallel then serial, serial then
	// parallel, and two parallel ones with stream padding between them.
	parallel := readFixture(t, "blocks-crc64.xz")
	serial := readFixture(t, "xz-utils/good-1-check-crc32.xz")
	padding := []byte{0, 0, 0, 0}
	inputs["parallel+pad+serial"] = bytes.Join([][]byte{parallel, padding, serial}, nil)
	inputs["serial+parallel"] = bytes.Join([][]byte{serial, parallel}, nil)
	inputs["parallel+pad+parallel"] = bytes.Join([][]byte{parallel, padding, readFixture(t, "blocks-sha256.xz")}, nil)
	inputs["parallel+short pad"] = bytes.Join([][]byte{parallel, {0, 0}}, nil)
	inputs["parallel+garbage"] = bytes.Join([][]byte{parallel, []byte("trailing")}, nil)

	for name, data := range inputs {
		want, wantErr := decodeXi2(data)
		got, gotErr := decodeXZ(data)
		if (gotErr != nil) != (wantErr != nil) {
			t.Errorf("%s: error %v, xi2 gives %v", name, gotErr, wantErr)
			continue
		}
		if gotErr == nil && !bytes.Equal(got, want) {
			t.Errorf("%s: decoded %d bytes that differ from xi2's %d", name, len(got), len(want))
		}
	}
}

// A file whose blocks record their sizes takes the parallel path: none of
// it falls back to xi2 reading the stream serially.
func TestXZReader_DecodesBlocksInParallel(t *testing.T) {
	src := bufio.NewReader(bytes.NewReader(readFixture(t, "blocks-crc64.xz")))
	header := make([]byte, 12)
	_, err := io.ReadFull(src, header)
	if err != nil {
		t.Fatal(err)
	}
	z := &xzParallelReader{pieces: make(chan chan xzPiece, 16), done: make(chan struct{})}
	serial, err := z.stream(src, header)
	if err != nil {
		t.Fatal(err)
	}
	if serial != nil {
		t.Fatal("stream fell back to serial decoding")
	}
	if len(z.pieces) != 4 {
		t.Fatalf("queued %d blocks, want 4", len(z.pieces))
	}
}

// Cutting the file short anywhere fails, and so does changing any byte
// xi2 notices. The one it does not is the first byte of each block's range
// coder, which LZMA decoders ignore; the output is the same without it.
func TestXZReader_TruncatedOrCorruptFails(t *testing.T) {
	data := readFixture(t, "blocks-crc64.xz")
	for n := range len(data) {
		_, err := decodeXZ(data[:n])
		if err == nil {
			t.Fatalf("decoding the first %d of %d bytes succeeded", n, len(data))
		}
	}
	want, err := decodeXi2(data)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := make([]byte, len(data))
	accepted := 0
	for i := range data {
		copy(corrupt, data)
		corrupt[i] ^= 0x01
		_, xi2Err := decodeXi2(corrupt)
		got, err := decodeXZ(corrupt)
		if xi2Err != nil && err == nil {
			t.Fatalf("decoding with byte %d flipped succeeded, xi2 gives %v", i, xi2Err)
		}
		if err == nil {
			accepted++
			if !bytes.Equal(got, want) {
				t.Fatalf("byte %d flipped decodes to different output", i)
			}
		}
	}
	if accepted > 4 {
		t.Fatalf("%d single-byte changes decode, want at most one per block", accepted)
	}
}

// Closing partway through returns, and stops the reader.
func TestXZReader_CloseMidStream(t *testing.T) {
	block := readFixture(t, "blocks-crc64.xz")
	data := bytes.Repeat(block, 50)
	z, err := newXZReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadFull(z, make([]byte, 10))
	if err != nil {
		t.Fatal(err)
	}
	err = z.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, err = z.Read(make([]byte, 10))
	if err == nil {
		t.Fatal("Read after Close succeeded")
	}
}
