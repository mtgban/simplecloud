package simplecloud

import (
	"context"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Bucket implements Reader and Writer for an Amazon S3 bucket (or any
// S3-compatible object store).
type S3Bucket struct {
	client   *s3.Client
	uploader *manager.Uploader
	Bucket   string
}

// NewS3Client creates an S3 client for the named bucket. accessKey and
// secretKey are optional; if both are empty, the default AWS credential chain
// is used. endpoint may be set to target S3-compatible stores (e.g.
// Cloudflare R2, MinIO); path-style addressing is enabled automatically when
// an endpoint is provided. region defaults to "auto" if empty.
func NewS3Client(ctx context.Context, accessKey, secretKey, bucketName, endpoint, region string) (*S3Bucket, error) {
	if region == "" {
		region = "auto"
	}
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})

	uploader := manager.NewUploader(client)

	return &S3Bucket{
		client:   client,
		uploader: uploader,
		Bucket:   bucketName,
	}, nil
}

// NewReader opens the object at path in the bucket for reading. A leading
// slash in path is stripped before the request is made.
func (s *S3Bucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	key := strings.TrimLeft(path, "/")

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.Bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

type s3PipeWriter struct {
	pw     *io.PipeWriter
	done   chan error
	ctx    context.Context
	cancel context.CancelFunc
}

func (w *s3PipeWriter) Write(p []byte) (int, error) {
	return w.pw.Write(p)
}

func (w *s3PipeWriter) Close() error {
	w.pw.Close()
	err, ok := <-w.done
	if ok {
		close(w.done)
	}
	w.cancel()
	return err
}

// NewWriter opens the object at path in the bucket for writing using a
// background goroutine and an io.Pipe so that data is streamed to S3 without
// buffering the entire payload in memory. A leading slash in path is stripped.
// The caller must call Close when done; Close blocks until the upload completes
// and returns any upload error.
func (s *S3Bucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	key := strings.TrimLeft(path, "/")

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		_, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket: &s.Bucket,
			Key:    &key,
			Body:   pr,
		})
		pr.CloseWithError(err)
		done <- err
	}()

	return &s3PipeWriter{
		pw:     pw,
		done:   done,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}
