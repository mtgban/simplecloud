package simplecloud

import (
	"context"
	"io"
	"strings"

	"github.com/Backblaze/blazer/b2"
)

type B2Bucket struct {
	Bucket *b2.Bucket

	ConcurrentDownloads int
}

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

func (b *B2Bucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	src := strings.TrimLeft(path, "/")
	obj := b.Bucket.Object(src).NewReader(ctx)
	obj.ConcurrentDownloads = b.ConcurrentDownloads
	return obj, nil
}

func (b *B2Bucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	dst := strings.TrimLeft(path, "/")
	obj := b.Bucket.Object(dst).NewWriter(ctx)
	return obj, nil
}
