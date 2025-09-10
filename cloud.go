package simplecloud

import (
	"context"
	"io"
)

type Reader interface {
	NewReader(context.Context, string) (io.ReadCloser, error)
}

type Writer interface {
	NewWriter(context.Context, string) (io.WriteCloser, error)
}

type ReadWriter interface {
	Reader
	Writer
}
