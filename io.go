package simplecloud

import (
	"compress/bzip2"
	"compress/gzip"
	"context"
	"io"
	"strings"

	bzip2Writer "github.com/dsnet/compress/bzip2"
	"github.com/ulikunitz/xz"
	xzReader "github.com/xi2/xz"
)

// cleanPath reduces a path to the object key used for storage access and
// extension-based compression detection. A trailing query string (e.g. a
// presigned-URL signature) is always dropped. When the path is a full URL
// (b2://, gs://, s3://, http(s)://, …) the scheme and authority are stripped
// and only the object key is kept, matching the documented contract.
//
// It deliberately avoids running arbitrary paths through url.Parse, which
// rejects '%' as an invalid escape and swallows '#...' as a URL fragment —
// both legal characters in local file paths and object keys.
func cleanPath(path string) string {
	before, _, _ := strings.Cut(path, "?")

	// Strip a leading scheme://authority for any URL, keeping everything from
	// the first slash of the path onward (leading slash included; the cloud
	// backends strip it as needed).
	if i := strings.Index(before, "://"); i >= 0 {
		rest := before[i+len("://"):]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return rest[slash:]
		}
		return ""
	}
	return before
}

// MultiCloser composes an io.Reader or io.Writer with multiple Closers that
// must all be closed in order. It is used internally by InitReader and
// InitWriter to close both the compression layer and the underlying storage
// stream in the correct sequence.
type MultiCloser struct {
	io.Reader
	io.Writer
	closers []io.Closer
}

func (m *MultiCloser) Close() error {
	var first error
	for _, closer := range m.closers {
		err := closer.Close()
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// InitReader opens path from bucket for reading, wrapping the stream in a
// decompressor when the path extension is recognised:
//
//   - .gz  — gzip
//   - .bz2 — bzip2
//   - .xz  — xz/lzma
//
// The path may be a full URL; only the path component is passed to the bucket.
// The caller must close the returned ReadCloser when done.
func InitReader(ctx context.Context, bucket Reader, path string) (io.ReadCloser, error) {
	key := cleanPath(path)

	reader, err := bucket.NewReader(ctx, key)
	if err != nil {
		return nil, err
	}

	var decoder io.ReadCloser
	if strings.HasSuffix(key, ".xz") {
		xzReader, err := xzReader.NewReader(reader, 0)
		if err != nil {
			reader.Close()
			return nil, err
		}
		decoder = io.NopCloser(xzReader)
	} else if strings.HasSuffix(key, ".bz2") {
		bz2Reader := bzip2.NewReader(reader)
		decoder = io.NopCloser(bz2Reader)
	} else if strings.HasSuffix(key, ".gz") {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			reader.Close()
			return nil, err
		}
		decoder = gzipReader
	}

	if decoder == nil {
		return reader, nil
	}

	return &MultiCloser{
		Reader:  decoder,
		closers: []io.Closer{decoder, reader},
	}, nil
}

// InitWriter opens path on bucket for writing, wrapping the stream in a
// compressor when the path extension is recognised:
//
//   - .gz  — gzip
//   - .bz2 — bzip2
//   - .xz  — xz/lzma
//
// The path may be a full URL; only the path component is passed to the bucket.
// The caller must call Close on the returned WriteCloser when done; for cloud
// backends this is what commits the upload.
func InitWriter(ctx context.Context, bucket Writer, path string) (io.WriteCloser, error) {
	key := cleanPath(path)

	writer, err := bucket.NewWriter(ctx, key)
	if err != nil {
		return nil, err
	}

	var encoder io.WriteCloser
	if strings.HasSuffix(key, ".xz") {
		xzWriter, err := xz.NewWriter(writer)
		if err != nil {
			writer.Close()
			return nil, err
		}
		encoder = xzWriter
	} else if strings.HasSuffix(key, ".bz2") {
		bz2Writer, err := bzip2Writer.NewWriter(writer, nil)
		if err != nil {
			writer.Close()
			return nil, err
		}
		encoder = bz2Writer
	} else if strings.HasSuffix(key, ".gz") {
		gzipWriter := gzip.NewWriter(writer)
		encoder = gzipWriter
	}

	if encoder == nil {
		return writer, nil
	}

	return &MultiCloser{
		Writer:  encoder,
		closers: []io.Closer{encoder, writer},
	}, nil
}

// Copy reads from srcPath on src and writes to dstPath on dst, using
// InitReader and InitWriter so that compression and decompression are applied
// automatically based on the path extensions. This means formats can be
// transcoded in a single call — e.g. copying a .gz source to a .xz
// destination will decompress and recompress on the fly.
//
// The returned count is the number of uncompressed bytes transferred between
// the reader and writer, not the number of bytes read from or written to
// storage.
func Copy(ctx context.Context, src Reader, dst Writer, srcPath, dstPath string) (int64, error) {
	r, err := InitReader(ctx, src, srcPath)
	if err != nil {
		return 0, err
	}
	defer r.Close()

	w, err := InitWriter(ctx, dst, dstPath)
	if err != nil {
		return 0, err
	}

	n, err := io.Copy(w, r)
	closeErr := w.Close()
	if closeErr != nil && err == nil {
		err = closeErr
	}
	return n, err
}
