package simplecloud

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// FileBucket implements Reader and Writer against the local filesystem.
type FileBucket struct{}

// NewReader opens the file at path for reading. The context is accepted to
// satisfy the Reader interface but is unused: local file reads are not
// cancellable.
func (f *FileBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// NewWriter creates or truncates the file at path for writing. Any missing
// parent directories are created automatically. The context is accepted to
// satisfy the Writer interface but is unused: local file writes are not
// cancellable.
func (f *FileBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return nil, err
	}
	return os.Create(path)
}
