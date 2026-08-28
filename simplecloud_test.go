package simplecloud_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtgban/simplecloud"
)

var ctx = context.Background()

// ---- helpers ----------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, r io.ReadCloser) string {
	t.Helper()
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- Open -------------------------------------------------------------------

func TestOpen_LocalFileWithCompression(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json.gz")
	const want = `{"hello":"world"}`

	w, err := simplecloud.InitWriter(ctx, &simplecloud.FileBucket{}, path)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, want)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// No scheme -> local filesystem, and the .gz is decompressed transparently.
	r, err := simplecloud.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpen_LocalPathWithPercent(t *testing.T) {
	// Open must not run the path through url.Parse: a '%' is a hard parse error
	// there but a legal filename byte here.
	dir := t.TempDir()
	path := filepath.Join(dir, "50%off.txt")
	const want = "no url.Parse please"
	writeFile(t, path, want)

	r, err := simplecloud.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpen_HTTP(t *testing.T) {
	const want = "served over http"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dir/file.txt" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		io.WriteString(w, want)
	}))
	defer srv.Close()

	r, err := simplecloud.Open(ctx, srv.URL+"/dir/file.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpen_UnsupportedScheme(t *testing.T) {
	_, err := simplecloud.Open(ctx, "ftp://host/object.json")
	if err == nil {
		t.Fatal("expected error for unsupported scheme, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported scheme") {
		t.Fatalf("error should mention unsupported scheme, got %q", err.Error())
	}
}

// stringBucket is a trivial Reader that serves fixed content, used to exercise
// the resolver extension point without a live cloud backend.
type stringBucket struct {
	gotKey  string
	content string
}

func (b *stringBucket) NewReader(_ context.Context, path string) (io.ReadCloser, error) {
	b.gotKey = path
	return io.NopCloser(strings.NewReader(b.content)), nil
}

func TestOpen_ResolverHandlesCustomScheme(t *testing.T) {
	fake := &stringBucket{content: "from resolver"}
	var gotScheme, gotHost string
	resolver := func(_ context.Context, scheme, host string) (simplecloud.Reader, error) {
		gotScheme, gotHost = scheme, host
		if scheme == "mem" {
			return fake, nil
		}
		return nil, nil
	}

	// The '#' in the key also guards that Open takes only scheme/host from
	// url.Parse (which would treat '#v2' as a fragment) while the original path
	// still reaches the backend intact.
	r, err := simplecloud.Open(ctx, "mem://my-bucket/dir/obj#v2.txt", simplecloud.WithResolver(resolver))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAll(t, r); got != "from resolver" {
		t.Fatalf("got %q, want %q", got, "from resolver")
	}
	if gotScheme != "mem" || gotHost != "my-bucket" {
		t.Fatalf("resolver saw scheme=%q host=%q, want mem/my-bucket", gotScheme, gotHost)
	}
	if fake.gotKey != "/dir/obj#v2.txt" {
		t.Fatalf("bucket saw key %q, want /dir/obj#v2.txt", fake.gotKey)
	}
}

func TestOpen_ResolverFallsThroughToBuiltin(t *testing.T) {
	// A resolver that declines (nil, nil) must let the built-in schemes handle
	// the path.
	dir := t.TempDir()
	path := filepath.Join(dir, "local.txt")
	const want = "built-in wins"
	writeFile(t, path, want)

	resolver := func(_ context.Context, scheme, host string) (simplecloud.Reader, error) {
		return nil, nil // decline everything
	}

	r, err := simplecloud.Open(ctx, path, simplecloud.WithResolver(resolver))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---- FileBucket -------------------------------------------------------------

func TestFileBucket_ReadWrite(t *testing.T) {
	dir := t.TempDir()
	bucket := &simplecloud.FileBucket{}
	path := filepath.Join(dir, "sub", "hello.txt")
	const want = "hello, world"

	w, err := bucket.NewWriter(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, want); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := bucket.NewReader(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFileBucket_ReaderMissingFile(t *testing.T) {
	bucket := &simplecloud.FileBucket{}
	_, err := bucket.NewReader(ctx, "/nonexistent/path/file.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

// ---- HTTPBucket -------------------------------------------------------------

func TestHTTPBucket_NewReader(t *testing.T) {
	const want = "served content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, want)
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	r, err := bucket.NewReader(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHTTPBucket_NonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = bucket.NewReader(ctx, "/missing")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestHTTPBucket_BasePathPrefix(t *testing.T) {
	// A base URL with a path prefix should be preserved; the per-call path is
	// joined onto it rather than replacing it.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	r, err := bucket.NewReader(ctx, "/obj.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()

	if want := "/v1/obj.txt"; gotPath != want {
		t.Fatalf("server saw path %q, want %q", gotPath, want)
	}
}

func TestHTTPBucket_NilClient(t *testing.T) {
	// NewHTTPBucket(nil, ...) should not panic; NewReader should work.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	bucket, err := simplecloud.NewHTTPBucket(nil, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	r, err := bucket.NewReader(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

// ---- compression (InitReader / InitWriter) ----------------------------------

var compressionCases = []struct {
	ext  string
	name string
}{
	{".gz", "gzip"},
	{".bz2", "bzip2"},
	{".xz", "xz"},
}

func TestCompression_RoundTrip(t *testing.T) {
	const want = "the quick brown fox jumps over the lazy dog"

	for _, tc := range compressionCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bucket := &simplecloud.FileBucket{}
			path := filepath.Join(dir, "data"+tc.ext)

			w, err := simplecloud.InitWriter(ctx, bucket, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(w, want); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}

			r, err := simplecloud.InitReader(ctx, bucket, path)
			if err != nil {
				t.Fatal(err)
			}
			if got := readAll(t, r); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestCompression_FileIsActuallyCompressed(t *testing.T) {
	// Verify the file on disk isn't just plain text.
	const content = "compressible content"

	for _, tc := range compressionCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bucket := &simplecloud.FileBucket{}
			path := filepath.Join(dir, "data"+tc.ext)

			w, err := simplecloud.InitWriter(ctx, bucket, path)
			if err != nil {
				t.Fatal(err)
			}
			io.WriteString(w, content)
			w.Close()

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(raw, []byte(content)) {
				t.Fatal("file appears uncompressed")
			}
		})
	}
}

func TestInitReader_UnknownExtension(t *testing.T) {
	// .json should pass through without decompression.
	dir := t.TempDir()
	bucket := &simplecloud.FileBucket{}
	path := filepath.Join(dir, "data.json")
	const want = `{"key":"value"}`
	writeFile(t, path, want)

	r, err := simplecloud.InitReader(ctx, bucket, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInitReader_WrapsErrorPreservingIs(t *testing.T) {
	// A failure from the backend must be wrapped with package context while
	// remaining inspectable via errors.Is (i.e. wrapped with %w, not %s).
	bucket := &simplecloud.FileBucket{}
	_, err := simplecloud.InitReader(ctx, bucket, "/nonexistent/simplecloud/file.json.gz")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("wrapped error should preserve os.ErrNotExist, got %v", err)
	}
	if !strings.Contains(err.Error(), "simplecloud:") {
		t.Errorf("error should carry package context, got %q", err.Error())
	}
}

func TestInitReader_SpecialCharsInPath(t *testing.T) {
	// Filenames containing '%' or '#' are legal on disk and in object keys.
	// They must not trip up query stripping or extension detection: the file
	// should still be compressed (.gz applied) and round-trip cleanly.
	names := []string{
		"data50%off.json.gz",
		"report#3.json.gz",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bucket := &simplecloud.FileBucket{}
			path := filepath.Join(dir, name)
			const want = "special chars content"

			w, err := simplecloud.InitWriter(ctx, bucket, path)
			if err != nil {
				t.Fatalf("InitWriter: %v", err)
			}
			if _, err := io.WriteString(w, want); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}

			// The on-disk file must actually be gzip-compressed, proving the
			// ".gz" suffix was detected despite the '%'/'#' in the name.
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(raw, []byte(want)) {
				t.Fatal("file appears uncompressed: extension detection failed")
			}

			r, err := simplecloud.InitReader(ctx, bucket, path)
			if err != nil {
				t.Fatalf("InitReader: %v", err)
			}
			if got := readAll(t, r); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestInitReader_PresignedURL(t *testing.T) {
	// A query string on the path should not confuse extension detection.
	dir := t.TempDir()
	bucket := &simplecloud.FileBucket{}
	path := filepath.Join(dir, "data.gz")
	const want = "presigned content"

	w, err := simplecloud.InitWriter(ctx, bucket, path)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, want)
	w.Close()

	// Simulate a pre-signed URL by appending a fake query string.
	r, err := simplecloud.InitReader(ctx, bucket, path+"?X-Amz-Signature=abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---- Copy abort -------------------------------------------------------------

// commitBucket mimics cloud semantics, where Close is what publishes an object
// and Abort discards it.
type commitBucket struct {
	committed string
	aborted   bool
}

func (b *commitBucket) NewWriter(_ context.Context, _ string) (io.WriteCloser, error) {
	return &commitWriter{b: b}, nil
}

type commitWriter struct {
	b   *commitBucket
	buf []byte
}

func (w *commitWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *commitWriter) Close() error {
	w.b.committed = string(w.buf)
	return nil
}

func (w *commitWriter) Abort() error {
	w.b.aborted = true
	return nil
}

// failingBucket serves a reader that fails partway through the stream.
type failingBucket struct{}

func (failingBucket) NewReader(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(io.MultiReader(
		strings.NewReader("GOOD-DATA-"),
		errReader{},
	)), nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("network died mid-stream") }

func TestCopy_AbortsInsteadOfCommittingOnError(t *testing.T) {
	// A mid-transfer read failure must not publish a truncated object: Close
	// is what commits on the cloud backends, so Copy must Abort instead.
	dst := &commitBucket{}
	_, err := simplecloud.Copy(ctx, failingBucket{}, dst, "src.txt", "dst.txt")
	if err == nil {
		t.Fatal("expected error from failing source, got nil")
	}
	if !dst.aborted {
		t.Error("destination writer was not aborted")
	}
	if dst.committed != "" {
		t.Errorf("truncated object was committed: %q", dst.committed)
	}
}

func TestCopy_AbortWithCompression(t *testing.T) {
	// With a compressed destination the abort must reach the underlying storage
	// stream through the compression layer, rather than flushing the encoder
	// and committing a partial object.
	dst := &commitBucket{}
	_, err := simplecloud.Copy(ctx, failingBucket{}, dst, "src.txt", "dst.txt.gz")
	if err == nil {
		t.Fatal("expected error from failing source, got nil")
	}
	if !dst.aborted {
		t.Error("storage writer was not aborted through the compression layer")
	}
	if dst.committed != "" {
		t.Errorf("truncated compressed object was committed: %q", dst.committed)
	}
}

func TestCopy_AbortRemovesPartialLocalFile(t *testing.T) {
	// A failed transfer to the local filesystem must not leave a truncated file
	// behind: NewWriter already truncated any previous contents, so a partial
	// file would read as valid data.
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.txt")

	_, err := simplecloud.Copy(ctx, failingBucket{}, &simplecloud.FileBucket{}, "src.txt", dst)
	if err == nil {
		t.Fatal("expected error from failing source, got nil")
	}
	if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
		got, _ := os.ReadFile(dst)
		t.Fatalf("partial file left behind with contents %q", got)
	}
}

func TestCopy_CommitsOnSuccess(t *testing.T) {
	// The abort path must not disturb the success path.
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	const want = "complete payload"
	writeFile(t, src, want)

	dst := &commitBucket{}
	n, err := simplecloud.Copy(ctx, &simplecloud.FileBucket{}, dst, src, "dst.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(want)) {
		t.Errorf("copied %d bytes, want %d", n, len(want))
	}
	if dst.aborted {
		t.Error("writer was aborted on a successful copy")
	}
	if dst.committed != want {
		t.Errorf("committed %q, want %q", dst.committed, want)
	}
}

// ---- Copy -------------------------------------------------------------------

func TestCopy_SameFormat(t *testing.T) {
	dir := t.TempDir()
	bucket := &simplecloud.FileBucket{}
	src := filepath.Join(dir, "src.gz")
	dst := filepath.Join(dir, "dst.gz")
	const want = "copy me"

	w, err := simplecloud.InitWriter(ctx, bucket, src)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, want)
	w.Close()

	if _, err := simplecloud.Copy(ctx, bucket, bucket, src, dst); err != nil {
		t.Fatal(err)
	}

	r, err := simplecloud.InitReader(ctx, bucket, dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCopy_Transcode(t *testing.T) {
	// Copy from .gz to .bz2 — should decompress and recompress transparently.
	dir := t.TempDir()
	bucket := &simplecloud.FileBucket{}
	src := filepath.Join(dir, "src.gz")
	dst := filepath.Join(dir, "dst.bz2")
	const want = "transcode me"

	w, err := simplecloud.InitWriter(ctx, bucket, src)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, want)
	w.Close()

	if _, err := simplecloud.Copy(ctx, bucket, bucket, src, dst); err != nil {
		t.Fatal(err)
	}

	r, err := simplecloud.InitReader(ctx, bucket, dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, r); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
