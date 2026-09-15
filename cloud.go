// Package simplecloud provides a unified interface for reading and writing
// objects across different storage backends, including the local filesystem,
// HTTP, Backblaze B2, Google Cloud Storage, and Amazon S3.
//
// All backends implement the Reader and/or Writer interfaces, which wrap the
// underlying SDK into a simple NewReader/NewWriter model. Transparent
// compression and decompression based on file extension is available via
// InitReader and InitWriter.
package simplecloud

import (
	"context"
	"io"
	"iter"
	"time"
)

// Reader is implemented by any storage backend that supports object reads.
type Reader interface {
	// NewReader opens the object at path for reading. The caller must close
	// the returned ReadCloser when done.
	NewReader(context.Context, string) (io.ReadCloser, error)
}

// Writer is implemented by any storage backend that supports object writes.
type Writer interface {
	// NewWriter opens the object at path for writing. The caller must call
	// Close when done; for cloud backends, Close is what commits the upload.
	NewWriter(context.Context, string) (io.WriteCloser, error)
}

// ReadWriter is implemented by backends that support both reads and writes.
type ReadWriter interface {
	Reader
	Writer
}

// ObjectInfo describes a single object returned by a List.
type ObjectInfo struct {
	// Key is the object's full key, not relative to the listed prefix, and
	// carries no leading slash.
	Key string

	// Size is the stored size in bytes. For a compressed object this is the
	// compressed size, not the size InitReader will yield.
	Size int64

	// LastModified is the object's modification time. The backends do not all
	// mean the same thing by it: S3 and GCS always report a server-side
	// timestamp, while B2 only records one when the uploader supplied it and
	// otherwise falls back to the upload timestamp. Treat it as "roughly when
	// this object appeared", not as a value to compare across backends.
	LastModified time.Time
}

// Lister is implemented by backends that can enumerate objects.
//
// It is optional, in the same way as Aborter: the local filesystem and HTTP
// backends do not implement it. Type-assert to reach it.
type Lister interface {
	// List iterates over every object whose key begins with prefix, in
	// whatever order the backend returns them. A leading slash on prefix is
	// stripped, and an empty prefix lists the whole bucket.
	//
	// Pagination is handled internally; the iterator fetches further pages as
	// it is consumed, so stopping early stops the requests. On failure the
	// iterator yields one final pair with a non-nil error and then ends, so a
	// caller must check the error on every iteration:
	//
	//	for obj, err := range bucket.List(ctx, "magic/") {
	//		if err != nil {
	//			return err
	//		}
	//		...
	//	}
	//
	// Listing is flat: there is no delimiter, so keys containing "/" are
	// returned in full rather than collapsed into common prefixes.
	List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error]
}

// The cloud backends implement Lister; the filesystem and HTTP backends do
// not. Asserted here so a signature drift fails the build rather than silently
// dropping a backend out of the interface.
var (
	_ Lister = (*S3Bucket)(nil)
	_ Lister = (*GCSBucket)(nil)
	_ Lister = (*B2Bucket)(nil)
)

// Aborter is implemented by writers that can discard an in-progress write
// instead of committing it.
//
// On the cloud backends Close is what publishes an object, so closing a stream
// whose transfer failed would commit a truncated object. Copy calls Abort in
// that case. Writers that cannot abort are simply closed, which does commit
// whatever was written.
type Aborter interface {
	// Abort discards the write. It is called instead of, not in addition to,
	// Close.
	Abort() error
}

// abortWrite discards an in-progress write, falling back to Close for writers
// that do not implement Aborter.
func abortWrite(w io.WriteCloser) error {
	if a, ok := w.(Aborter); ok {
		return a.Abort()
	}
	return w.Close()
}

// cancelWriter aborts an upload by cancelling the context its underlying writer
// was created with. This is how the GCS and B2 clients are told to discard a
// partial object rather than commit it.
type cancelWriter struct {
	io.WriteCloser
	cancel context.CancelFunc
}

// Close commits the object, then releases the context.
func (w *cancelWriter) Close() error {
	err := w.WriteCloser.Close()
	w.cancel()
	return err
}

// Abort cancels the context so the client discards the upload, then releases
// the underlying writer. The close error is expected after cancellation and is
// not reported.
func (w *cancelWriter) Abort() error {
	w.cancel()
	w.WriteCloser.Close()
	return nil
}
