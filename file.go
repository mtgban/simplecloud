package simplecloud

import (
	"context"
	"io"
	"os"
)

type FileBucket struct{}

func (f *FileBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}
