package simplecloud

import (
	"context"
	"io"
	"strings"

	"github.com/Backblaze/blazer/b2"
)

// B2Bucket implements Reader and Writer for a Backblaze B2 bucket.
type B2Bucket struct {
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

// NewReader opens the object at path in the bucket for reading. A leading
// slash in path is stripped before the request is made.
func (b *B2Bucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	src := strings.TrimLeft(path, "/")
	obj := b.Bucket.Object(src).NewReader(ctx)
	obj.ConcurrentDownloads = b.ConcurrentDownloads
	return obj, nil
}

// NewWriter opens the object at path in the bucket for writing. A leading
// slash in path is stripped. The caller must call Close when done; Close
// finalises the upload to B2.
func (b *B2Bucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	dst := strings.TrimLeft(path, "/")
	obj := b.Bucket.Object(dst).NewWriter(ctx)
	return obj, nil
}
