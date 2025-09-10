package simplecloud

import (
	"context"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

type GCSBucket struct {
	Bucket *storage.BucketHandle
}

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

func (g *GCSBucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return g.Bucket.Object(path).NewReader(ctx)
}

func (g *GCSBucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	obj := g.Bucket.Object(path).NewWriter(ctx)
	return obj, nil
}
