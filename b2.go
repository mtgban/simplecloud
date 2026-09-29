package simplecloud

import (
	"context"
	"fmt"
	"io"
	"iter"
	"strings"
	"sync/atomic"

	"github.com/Backblaze/blazer/b2"
)

// B2Bucket implements Reader and Writer for a Backblaze B2 bucket.
type B2Bucket struct {
	// Bucket is the underlying blazer bucket handle that reads and writes go
	// through.
	Bucket *b2.Bucket

	// ConcurrentDownloads controls how many parallel range requests are used
	// when downloading large objects. Zero uses the blazer library default.
	ConcurrentDownloads int
}

// NewB2Client authenticates with Backblaze B2 using accessKey and secretKey,
// then opens the named bucket.
func NewB2Client(ctx context.Context, accessKey, secretKey, bucketName string) (*B2Bucket, error) {
	client, err := b2.NewClient(ctx, accessKey, secretKey)
	if err != nil {
		return nil, err
	}

	bucket, err := client.Bucket(ctx, bucketName)
	if err != nil {
		return nil, err
	}

	return &B2Bucket{
		Bucket: bucket,
	}, nil
}

// NewReader opens the object at path in the bucket for reading. A leading slash
// is stripped: blazer interpolates the name straight into the object URL path,
// so "/foo" would create an object literally named "/foo".
func (b *B2Bucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	src := strings.TrimLeft(path, "/")
	obj := b.Bucket.Object(src).NewReader(ctx)
	obj.ConcurrentDownloads = b.ConcurrentDownloads
	return obj, nil
}

// NewWriter opens the object at path in the bucket for writing. A leading slash
// is stripped (see NewReader). The caller must call Close when done; Close
// finalises the upload to B2. The returned writer implements Aborter: aborting
// cancels the write's context, and for uploads large enough to have switched to
// the large-file API it also issues b2_cancel_large_file so the uploaded parts
// are discarded rather than left billable.
func (b *B2Bucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	dst := strings.TrimLeft(path, "/")
	writeCtx, cancel := context.WithCancel(ctx)
	w := &b2Writer{}

	// blazer only calls b2_cancel_large_file when given WithCancelOnError;
	// without it a failed multipart upload silently leaves an unfinished large
	// file behind, whose parts keep consuming storage. The cancel request needs
	// a context that outlives the one being cancelled, hence WithoutCancel.
	cancelCtx := func() context.Context {
		w.failed.Store(true)
		return context.WithoutCancel(ctx)
	}
	w.obj = b.Bucket.Object(dst).NewWriter(writeCtx, b2.WithCancelOnError(cancelCtx, nil))
	return &cancelWriter{WriteCloser: w, cancel: cancel}, nil
}

// b2Writer returns the errors blazer panics on. WithCancelOnError has setErr
// cancel the large file on any error, calling a method on a nil file when none
// was started (Backblaze/blazer#54): under ChunkSize (100 MB) a refused,
// timed-out or aborted upload panics in Close, and over it a failed
// b2_start_large_file panics in Write. setErr records the error first, so a
// second call returns it: Close's sync.Once counts the panicking body as done,
// and Write checks for a recorded error before buffering.
//
// blazer's writer is a named field, not embedded, so its ReadFrom, which
// io.Copy would prefer, is not promoted around Write.
type b2Writer struct {
	obj *b2.Writer

	// failed is set by the ctxf that setErr calls after recording the error and
	// before its call on a nil file panics, so it means an error was recorded.
	// Were that order to change, failed would stay false and Write would
	// re-panic, which TestB2Bucket_FailedUploadReturnsError catches.
	failed atomic.Bool
}

// Write passes p to blazer and returns the error blazer panics on. A panic with
// no error recorded is raised again rather than retried, since a second Write
// would then block on channels blazer never made.
func (w *b2Writer) Write(p []byte) (n int, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if !w.failed.Load() {
			panic(r)
		}
		n, err = w.obj.Write(p)
		if err == nil {
			panic(r)
		}
	}()
	return w.obj.Write(p)
}

// Close finalises the upload. A panic that blazer recorded no error for is
// raised again, since it could otherwise read as a successful upload. Unlike a
// second Write, which would write, a second Close sends nothing and returns nil
// when no error was recorded, re-raising the panic, so it needs no flag.
func (w *b2Writer) Close() (err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		err = w.obj.Close()
		if err == nil {
			panic(r)
		}
	}()
	return w.obj.Close()
}

// List iterates over objects in the bucket whose key begins with prefix. The
// blazer iterator pages lazily, so abandoning it stops the requests.
//
// Attrs is free here: the objects come back from the listing with their file
// info already populated, so reading them costs no extra request.
func (b *B2Bucket) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		p := strings.TrimLeft(prefix, "/")
		it := b.Bucket.List(ctx, b2.ListPrefix(p))
		for it.Next() {
			obj := it.Object()
			attrs, err := obj.Attrs(ctx)
			if err != nil {
				yield(ObjectInfo{}, fmt.Errorf("simplecloud: list %q: %w", p, err))
				return
			}

			// B2 only records LastModified when the uploader supplied
			// src_last_modified_millis; UploadTimestamp is always set, so it
			// stands in rather than reporting a zero time.
			mtime := attrs.LastModified
			if mtime.IsZero() {
				mtime = attrs.UploadTimestamp
			}

			if !yield(ObjectInfo{Key: attrs.Name, Size: attrs.Size, LastModified: mtime}, nil) {
				return
			}
		}
		if err := it.Err(); err != nil {
			yield(ObjectInfo{}, fmt.Errorf("simplecloud: list %q: %w", p, err))
		}
	}
}
