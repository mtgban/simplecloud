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

### HTTP base paths

The base URL passed to `NewHTTPBucket` may include a path prefix, which is
preserved on every request; the per-call path is joined onto it. For example, a
base of `https://host/v1` reading `/data.json` requests
`https://host/v1/data.json`. Any credentials in the base URL are reused and
redacted from error messages.

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

## Opening by URL

`Open` picks a backend from the path's scheme so callers don't have to
construct one, applying transparent decompression via `InitReader`:

| Scheme | Backend |
|--------|---------|
| (none) | Local filesystem |
| `http`, `https` | HTTP(S) |
| `b2` | Backblaze B2 (host is the bucket name) |
| `s3` | Amazon S3 / S3-compatible (host is the bucket name) |
| `gs` | Google Cloud Storage (host is the bucket name) |

Backend-specific configuration — credentials, the HTTP client, endpoints,
concurrency — is passed with functional options, so the package never sources
credentials on its own:

```go
r, err := simplecloud.Open(ctx, "b2://my-bucket/data/report.json.xz",
    simplecloud.WithB2Credentials(keyID, appKey),
    simplecloud.WithConcurrentDownloads(8),
)
if err != nil {
    log.Fatal(err)
}
defer r.Close()
// r yields decompressed data

// S3-compatible (e.g. Cloudflare R2):
r, err = simplecloud.Open(ctx, "s3://my-bucket/data/report.json.gz",
    simplecloud.WithS3Credentials(accessKey, secretKey),
    simplecloud.WithS3Endpoint("https://<account>.r2.cloudflarestorage.com"),
)

// A local path needs no options and no scheme:
r, err = simplecloud.Open(ctx, "/data/report.json.gz")
```

For any other scheme — or to control client lifecycle instead of the per-call
client the `s3` and `gs` schemes create — supply a resolver. It is consulted
before the built-in schemes (so it can also override them); returning
`(nil, nil)` falls through:

```go
gcs, _ := simplecloud.NewGCSClient(ctx, "sa.json", "my-bucket") // closed by you
r, err := simplecloud.Open(ctx, "gs://my-bucket/data/report.json.gz",
    simplecloud.WithResolver(func(_ context.Context, scheme, host string) (simplecloud.Reader, error) {
        if scheme == "gs" {
            return gcs, nil
        }
        return nil, nil
    }),
)
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
