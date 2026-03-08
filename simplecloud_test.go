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
