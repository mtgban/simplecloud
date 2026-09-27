package simplecloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
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

// cleanListPrefix resolves prefix to the directory List's walk should start
// from and the string every candidate path must have as a prefix, both
// derived from filepath.Clean(prefix) so they agree with the clean paths
// filepath.WalkDir itself produces. Matching the walk's output against an
// uncleaned prefix - one containing "./", a doubled separator or a ".."
// segment - would never succeed even though NewReader accepts that same
// path directly, since WalkDir never yields a path in that form.
func cleanListPrefix(prefix string) (root, match string) {
	clean := filepath.Clean(prefix)
	switch {
	case clean == ".":
		// "", ".", "./" and the like all mean the whole tree: WalkDir's
		// paths from "." carry no "./" for a literal "." to match against.
		return ".", ""
	case strings.HasSuffix(prefix, "/") && clean != "/":
		// The trailing slash names clean itself as the directory to walk,
		// rather than a partial name inside its parent.
		return clean, clean + "/"
	default:
		return filepath.Dir(clean), clean
	}
}

// List iterates over every regular file on the local filesystem whose path
// begins with prefix. Unlike the cloud backends, FileBucket has no bucket
// root: NewReader and NewWriter take path exactly as given, so here prefix is
// a filesystem path, a leading slash is significant and is never stripped,
// and every yielded Key can be opened directly with NewReader. prefix is
// cleaned with filepath.Clean before matching, keeping a trailing slash's
// meaning, so "./dumps/lorcana/" and "dumps//lorcana/" behave like
// "dumps/lorcana/".
//
// prefix may name a directory (trailing slash) or a partial file or
// directory name, e.g. "dumps/lorcana/cool" matches
// "dumps/lorcana/coolstuffinc/retail/CSI.json.xz". The walk starts at the
// deepest directory prefix names or implies, rather than at "/" or the
// working directory, so an empty prefix walks the working directory tree and
// a prefix whose directory does not exist yields no objects and no error,
// matching a cloud prefix with no matches. A symlink to a regular file is
// yielded, using the target's size and modification time, but - like
// filepath.WalkDir itself - the walk does not recurse into a symlinked
// directory.
func (f *FileBucket) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		root, match := cleanListPrefix(prefix)
		walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if path == root && errors.Is(err, os.ErrNotExist) {
					return filepath.SkipAll
				}
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() || !strings.HasPrefix(path, match) {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				// d.Info() is an lstat of the link itself; resolve the target
				// to decide whether it is a regular file. This does not widen
				// into recursing through a symlinked directory - WalkDir
				// already does not follow those.
				info, err = os.Stat(path)
				if errors.Is(err, os.ErrNotExist) {
					return nil // dangling symlink: nothing to list
				}
				if err != nil {
					return err
				}
			}
			if !info.Mode().IsRegular() {
				return nil
			}

			if !yield(ObjectInfo{Key: path, Size: info.Size(), LastModified: info.ModTime()}, nil) {
				return filepath.SkipAll
			}
			return nil
		})
		if walkErr != nil {
			yield(ObjectInfo{}, fmt.Errorf("simplecloud: list %q: %w", prefix, walkErr))
		}
	}
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
