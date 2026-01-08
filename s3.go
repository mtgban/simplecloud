package simplecloud

import (
	"context"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Bucket struct {
	Client   *s3.Client
	Uploader *manager.Uploader
	Bucket   string
}

func NewS3Client(ctx context.Context, bucketName, endpoint, region string) (*S3Bucket, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
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
		Client:   client,
		Uploader: uploader,
		Bucket:   bucketName,
	}, nil
}

func (s *S3Bucket) NewReader(ctx context.Context, path string) (io.ReadCloser, error) {
	key := strings.TrimLeft(path, "/")

	resp, err := s.Client.GetObject(ctx, &s3.GetObjectInput{
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
	err := <-w.done
	w.cancel()
	return err
}

func (s *S3Bucket) NewWriter(ctx context.Context, path string) (io.WriteCloser, error) {
	key := strings.TrimLeft(path, "/")

	pr, pw := io.Pipe()
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		_, err := s.Uploader.Upload(ctx, &s3.PutObjectInput{
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

