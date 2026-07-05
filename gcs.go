package simplecloud

import (
	"context"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCSBucket implements Reader and Writer for a Google Cloud Storage bucket.
type GCSBucket struct {
	Bucket *storage.BucketHandle
}

// NewGCSClient creates a GCS client and opens the named bucket. If
// serviceAccountFile is non-empty it is used for authentication; otherwise
// Application Default Credentials are used, which works automatically in GKE,
// Cloud Run, and locally via `gcloud auth application-default login`. The
// underlying storage.Client is not exposed; callers that need to close it
// should construct one directly.
func NewGCSClient(ctx context.Context, serviceAccountFile, bucketName string) (*GCSBucket, error) {
	var opts []option.ClientOption
	if serviceAccountFile != "" {
		opts = append(opts, option.WithCredentialsFile(serviceAccountFile))
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
// what commits the object to GCS.
func (g *GCSBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	key := strings.TrimLeft(path, "/")
	obj := g.Bucket.Object(key).NewWriter(ctx)
	return obj, nil
}
