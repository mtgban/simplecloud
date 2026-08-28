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
// cancellable. The returned writer implements Aborter.
func (f *FileBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &fileWriter{File: file}, nil
}

// fileWriter adds Abort to a local file.
type fileWriter struct {
	*os.File
}

// Abort closes the file and removes it. NewWriter created or truncated it, so
// the previous contents are already gone; removing the partial file makes the
// failure visible instead of leaving truncated data that reads as valid.
func (w *fileWriter) Abort() error {
	name := w.Name()
	w.Close()
	return os.Remove(name)
}
