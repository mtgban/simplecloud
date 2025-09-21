package simplecloud

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

type FileBucket struct{}

func (f *FileBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func (f *FileBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return nil, err
	}
	return os.Create(path)
}
