# simplecloud

A tiny Go package for reading and writing objects across different storage backends with a unified interface.

## Installation

```sh
go get github.com/mtgban/simplecloud
```

## Supported Backends

| Backend | Read | Write | Constructor |
|---------|------|-------|-------------|
| Local filesystem | ✓ | ✓ | `&FileBucket{}` |
| HTTP/HTTPS | ✓ | — | `NewHTTPBucket(client, baseURL)` |
| Backblaze B2 | ✓ | ✓ | `NewB2Client(ctx, accessKey, secretKey, bucket)` |
| Google Cloud Storage | ✓ | ✓ | `NewGCSClient(ctx, serviceAccountFile, bucket)` |
| Amazon S3 | ✓ | ✓ | `NewS3Client(ctx, accessKey, secretKey, bucket, endpoint, region)` |

## Usage

All backends implement the same interface:

```go
type Reader interface {
    NewReader(context.Context, string) (io.ReadCloser, error)
}

type Writer interface {
    NewWriter(context.Context, string) (io.WriteCloser, error)
}
```

### Reading from GCS

```go
bucket, err := simplecloud.NewGCSClient(ctx, "service-account.json", "my-bucket")
if err != nil {
    log.Fatal(err)
}

reader, err := bucket.NewReader(ctx, "path/to/file.txt")
if err != nil {
    log.Fatal(err)
}
defer reader.Close()

data, err := io.ReadAll(reader)
```

### Writing to B2

```go
bucket, err := simplecloud.NewB2Client(ctx, accessKey, secretKey, "my-bucket")
if err != nil {
    log.Fatal(err)
}

writer, err := bucket.NewWriter(ctx, "path/to/file.txt")
if err != nil {
    log.Fatal(err)
}

_, err = writer.Write([]byte("hello world"))
if err != nil {
    writer.Close()
    log.Fatal(err)
}

if err := writer.Close(); err != nil {
    log.Fatal(err)  // important: Close() flushes to cloud storage
}
```

## Transparent Compression

Use `InitReader` and `InitWriter` to automatically handle compressed files based on extension:

| Extension | Compression |
|-----------|-------------|
| `.gz` | gzip |
| `.bz2` | bzip2 |
| `.xz` | xz/lzma |

```go
// Automatically decompresses .gz file
reader, err := simplecloud.InitReader(ctx, bucket, "data.json.gz")
if err != nil {
    log.Fatal(err)
}
defer reader.Close()
// reader yields decompressed data

// Automatically compresses to .xz
writer, err := simplecloud.InitWriter(ctx, bucket, "output.json.xz")
if err != nil {
    log.Fatal(err)
}
// writes are compressed before storage
```

## Copying Between Backends

Copy files between any backends, with automatic compression/decompression:

```go
src, _ := simplecloud.NewGCSClient(ctx, "sa.json", "source-bucket")
dst, _ := simplecloud.NewB2Client(ctx, key, secret, "dest-bucket")

// Copy and transcode: decompress gzip, recompress as xz
n, err := simplecloud.Copy(ctx, src, dst, "input.json.gz", "output.json.xz")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("copied %d bytes\n", n)
```

## Limitations

This is a lightweight helper, and some operations are not covered:

- No `List` or `Delete` API
- No retry logic or exponential backoff
- No ACL or permission management
- No multipart upload configuration
- Context cancellation doesn't interrupt local file operations
- Cloud clients aren't exposed for cleanup (create short-lived or manage externally)

For advanced use cases, use the underlying SDKs directly:
- [cloud.google.com/go/storage](https://pkg.go.dev/cloud.google.com/go/storage)
- [github.com/Backblaze/blazer/b2](https://pkg.go.dev/github.com/Backblaze/blazer/b2)
- [github.com/aws/aws-sdk-go-v2](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/welcome.html)

## License

MIT
