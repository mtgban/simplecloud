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

func InitReader(ctx context.Context, bucket Reader, path string) (io.ReadCloser, error) {
	reader, err := bucket.NewReader(ctx, path)
	if err != nil {
		return nil, err
	}

	var decoder io.ReadCloser
	if strings.HasSuffix(path, "xz") {
		xzReader, err := xzReader.NewReader(reader, 0)
		if err != nil {
			reader.Close()
			return nil, err
		}
		decoder = io.NopCloser(xzReader)
	} else if strings.HasSuffix(path, "bz2") {
		bz2Reader := bzip2.NewReader(reader)
		decoder = io.NopCloser(bz2Reader)
	} else if strings.HasSuffix(path, "gz") {
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

func InitWriter(ctx context.Context, bucket Writer, path string) (io.WriteCloser, error) {
	writer, err := bucket.NewWriter(ctx, path)
	if err != nil {
		return nil, err
	}

	var encoder io.WriteCloser
	if strings.HasSuffix(path, ".xz") {
		xzWriter, err := xz.NewWriter(writer)
		if err != nil {
			writer.Close()
			return nil, err
		}
		encoder = xzWriter
	} else if strings.HasSuffix(path, ".bz2") {
		bz2Writer, err := bzip2Writer.NewWriter(writer, nil)
		if err != nil {
			writer.Close()
			return nil, err
		}
		encoder = bz2Writer
	} else if strings.HasSuffix(path, ".gz") {
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
