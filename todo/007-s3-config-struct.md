# 007 — Replace `NewS3Client`'s six string parameters

**Status:** breaking. Needs a product decision.

## Problem

```go
func NewS3Client(ctx context.Context, accessKey, secretKey, bucketName, endpoint, region string) (*S3Bucket, error)
```

Five adjacent strings. Transposing `bucketName` and `endpoint` compiles
cleanly and fails at runtime, possibly by writing to the wrong place.

## What does *not* fix it

Named string types (`type Bucket string`, `type Endpoint string`) were
measured against this exact shape:

| Call site | Caught? |
|---|---|
| Transposed **literals** — `newClient("my-endpoint", "my-bucket")` | **No** — compiles; untyped constants convert implicitly |
| Transposed named-type **variables** | Yes |
| Plain `string` variables | Rejected — forces a conversion at every call site |

Callers here pass literals and values read from env vars, which is exactly
the case named types do not protect. They would add friction everywhere and
catch almost nothing. This is why the wider "use real types everywhere"
idea was narrowed to 001 and 002.

## What would fix it

A config struct with named fields:

```go
type S3Config struct {
    AccessKey, SecretKey string
    Bucket               string
    Endpoint, Region     string
}
func NewS3Client(ctx context.Context, cfg S3Config) (*S3Bucket, error)
```

Field names make transposition impossible regardless of literals, and new
knobs stop extending the parameter list.

## Cost

Breaking for every caller of `NewS3Client`. Pre-1.0 permits it, and this
repo has taken breaking changes before, but it should ride with a release
that is already breaking rather than on its own.
