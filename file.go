package simplecloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"
)

// FileBucket implements Reader, Writer and Lister against the local
// filesystem.
//
// The zero value takes every path exactly as given, relative to the working
// directory or absolute. With Root set, it addresses the tree under Root the
// way a cloud backend addresses its bucket.
type FileBucket struct {
	// Root, when set, is the directory every path is resolved inside, so a
	// directory laid out like a bucket can stand in for one. A leading slash
	// on a path is ignored, as the cloud backends ignore it, and a path that
	// would leave Root, through ".." or a symbolic link, is refused with an
	// error. List yields keys relative to Root, slash-separated.
	//
	// Root is opened afresh on every call, through os.Root, which follows a
	// symbolic link only when its target is relative and stays inside Root.
	Root string
}

// NewReader opens the file at path for reading: the path as given, or inside
// Root when one is set. The context is accepted to satisfy the Reader
// interface but is unused: local file reads are not cancellable.
func (f *FileBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	if f.Root == "" {
		return os.Open(path)
	}
	// A leading slash is ignored, as on the cloud backends; os.Root would
	// otherwise refuse the name as absolute.
	return os.OpenInRoot(f.Root, strings.TrimLeft(path, "/"))
}

// NewWriter creates or truncates the file at path for writing: the path as
// given, or inside Root when one is set. Any missing parent directories are
// created automatically, Root included. The context is accepted to satisfy
// the Writer interface but is unused: local file writes are not cancellable.
// The returned writer implements Aborter.
func (f *FileBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	mkdirAll, create := os.MkdirAll, os.Create
	if f.Root != "" {
		err := os.MkdirAll(f.Root, 0755)
		if err != nil {
			return nil, err
		}
		root, err := os.OpenRoot(f.Root)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		mkdirAll, create = root.MkdirAll, root.Create
		path = strings.TrimLeft(path, "/")
	}

	err := mkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		return nil, err
	}
	file, err := create(path)
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
func cleanListPrefix(prefix string) (start, match string) {
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

// List iterates over every regular file whose path begins with prefix.
//
// With Root set, prefix and every yielded Key are keys relative to Root,
// slash-separated, as a cloud backend's are: a leading slash on prefix is
// ignored, and every Key can be opened with NewReader. A prefix that would
// leave Root is refused with an error, and a missing Root lists nothing.
//
// Without a Root, prefix and every yielded Key are filesystem paths exactly
// as NewReader takes them: a leading slash is significant and is never
// stripped, and every Key can be opened directly with NewReader.
//
// Either way, prefix is cleaned with filepath.Clean before matching, keeping
// a trailing slash's meaning, so "./dumps/lorcana/" and "dumps//lorcana/"
// behave like "dumps/lorcana/". It may name a directory (trailing slash) or a
// partial file or directory name, e.g. "dumps/lorcana/cool" matches
// "dumps/lorcana/coolstuffinc/retail/CSI.json.xz". The walk starts at the
// deepest directory prefix names or implies, so an empty prefix walks the
// whole tree (Root's, or else the working directory's) and a prefix whose
// directory does not exist yields no objects and no error, matching a cloud
// prefix with no matches.
//
// A symlink to a regular file is yielded, using the target's size and
// modification time, and a dangling one is skipped. Like filepath.WalkDir
// itself, the walk does not recurse into a symlinked directory.
//
// With Root set, every symlink resolves inside Root: one that is absolute, or
// whose target leaves Root, ends the listing with an error, as NewReader
// would refuse it. The directory the walk starts from is walked even when it
// is itself a symlink to a directory inside Root.
func (f *FileBucket) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		err := f.walk(ctx, prefix, yield)
		if err != nil {
			yield(ObjectInfo{}, fmt.Errorf("simplecloud: list %q: %w", prefix, err))
		}
	}
}

// walk runs List's walk over the working directory's tree, or over Root's
// when one is set, and returns the error that ended it.
func (f *FileBucket) walk(ctx context.Context, prefix string, yield func(ObjectInfo, error) bool) error {
	if f.Root == "" {
		start, match := cleanListPrefix(prefix)
		return walkFiles(ctx, start, match, filepath.WalkDir, os.Stat, yield)
	}

	root, err := os.OpenRoot(f.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()

	// Inside Root the walk yields io/fs paths, relative and slash-separated,
	// which are the keys List returns.
	start, match := cleanListPrefix(strings.TrimLeft(prefix, "/"))
	start, match = filepath.ToSlash(start), filepath.ToSlash(match)
	fsys := root.FS()
	walkDir := func(start string, fn fs.WalkDirFunc) error {
		return fs.WalkDir(fsys, start, fn)
	}
	return walkFiles(ctx, start, match, walkDir, root.Stat, yield)
}

// walkFiles walks the tree from start with walkDir, yielding every regular
// file whose path begins with match. stat resolves a symlink to its target.
func walkFiles(ctx context.Context, start, match string, walkDir func(string, fs.WalkDirFunc) error, stat func(string) (fs.FileInfo, error), yield func(ObjectInfo, error) bool) error {
	return walkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == start && errors.Is(err, os.ErrNotExist) {
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
			info, err = stat(path)
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
