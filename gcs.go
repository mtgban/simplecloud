package simplecloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// GCSBucket implements Reader and Writer for a Google Cloud Storage bucket.
type GCSBucket struct {
	// Bucket is the underlying GCS bucket handle that reads and writes go
	// through.
	Bucket *storage.BucketHandle
}

// NewGCSClient creates a GCS client and opens the named bucket. If
// serviceAccountFile is non-empty it is used for authentication; otherwise
// Application Default Credentials are used, which works automatically in GKE,
// Cloud Run, and locally via `gcloud auth application-default login`. The
// underlying storage.Client is not exposed; callers that need to close it
// should construct one directly.
//
// The file is declared to hold a service account rather than passed as an
// untyped credentials file, which is the non-deprecated form of the option:
// the untyped variant accepts any credential type, including externally
// sourced ones that name an executable to run. Note that the storage client
// resolves credentials through a path that does not currently act on the
// declared type, so this states the expectation rather than enforcing it.
func NewGCSClient(ctx context.Context, serviceAccountFile, bucketName string) (*GCSBucket, error) {
	var opts []option.ClientOption
	if serviceAccountFile != "" {
		opts = append(opts, option.WithAuthCredentialsFile(option.ServiceAccount, serviceAccountFile))
	}

	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	bucket := client.Bucket(bucketName)

	return &GCSBucket{
		Bucket: bucket,
	}, nil
}

// NewReader opens the object at path in the bucket for reading. A leading slash
// is stripped so keys match the S3 and B2 backends; the GCS client would
// otherwise treat "/foo" as an object literally named "/foo".
func (g *GCSBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	key := strings.TrimLeft(path, "/")
	return g.Bucket.Object(key).NewReader(ctx)
}

// NewWriter opens the object at path in the bucket for writing. A leading slash
// is stripped (see NewReader). The caller must call Close when done; Close is
// what commits the object to GCS. The returned writer implements Aborter:
// aborting cancels the write's context, which is how the GCS client is told to
// discard a partial object instead of committing it.
//
// Unlike S3 and B2, an abandoned GCS upload needs no server-side cleanup: only
// a completed resumable upload appears in the bucket, so partial data is never
// stored or billed, and the session expires on its own after a week.
func (g *GCSBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	key := strings.TrimLeft(path, "/")
	ctx, cancel := context.WithCancel(ctx)
	obj := g.Bucket.Object(key).NewWriter(ctx)
	return &cancelWriter{WriteCloser: obj, cancel: cancel}, nil
}

// List iterates over objects in the bucket whose key begins with prefix. The
// underlying iterator pages lazily, so abandoning it stops the requests.
func (g *GCSBucket) List(ctx context.Context, prefix string) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		p := strings.TrimLeft(prefix, "/")
		it := g.Bucket.Objects(ctx, &storage.Query{Prefix: p})
		for {
			attrs, err := it.Next()
			if errors.Is(err, iterator.Done) {
				return
			}
			if err != nil {
				yield(ObjectInfo{}, fmt.Errorf("simplecloud: list %q: %w", p, err))
				return
			}
			ok := yield(ObjectInfo{
				Key:          attrs.Name,
				Size:         attrs.Size,
				LastModified: attrs.Updated,
			}, nil)
			if !ok {
				return
			}
		}
	}
}
