package simplecloud

import (
	"context"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCSBucket implements Reader and Writer for a Google Cloud Storage bucket.
type GCSBucket struct {
	Bucket *storage.BucketHandle
}

// NewGCSClient creates a GCS client authenticated via the given service account
// JSON file and opens the named bucket. The underlying storage.Client is not
// exposed; callers that need to close it should construct one directly.
func NewGCSClient(ctx context.Context, serviceAccountFile, bucketName string) (*GCSBucket, error) {
	client, err := storage.NewClient(ctx, option.WithCredentialsFile(serviceAccountFile))
	if err != nil {
		return nil, err
	}

	bucket := client.Bucket(bucketName)

	return &GCSBucket{
		Bucket: bucket,
	}, nil
}

// NewReader opens the object at path in the bucket for reading.
func (g *GCSBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return g.Bucket.Object(path).NewReader(ctx)
}

// NewWriter opens the object at path in the bucket for writing. The caller
// must call Close when done; Close is what commits the object to GCS.
func (g *GCSBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	obj := g.Bucket.Object(path).NewWriter(ctx)
	return obj, nil
}
