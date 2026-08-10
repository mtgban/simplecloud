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
	"fmt"
	"io"
	"os"
)

// Reader is implemented by any storage backend that supports object reads.
type Reader interface {
	// NewReader opens the object at path for reading. The caller must close
	// the returned ReadCloser when done.
	//
	// A missing object is reported as an error satisfying
	// errors.Is(err, os.ErrNotExist), whichever backend produced it. B2 only
	// discovers the absence once a range request lands, so there it surfaces
	// from the first Read rather than from NewReader.
	NewReader(context.Context, string) (io.ReadCloser, error)
}

// errNotExist marks err as a missing-object error, so that callers can test
// for one with errors.Is(err, os.ErrNotExist) without knowing which backend
// produced it or which SDK error type that backend speaks.
func errNotExist(err error) error {
	return fmt.Errorf("%w: %w", os.ErrNotExist, err)
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
