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
