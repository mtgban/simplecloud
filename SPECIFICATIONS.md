# SPECIFICATIONS

The behaviour contract of `github.com/mtgban/simplecloud`. Where a statement
here was established by experiment rather than by reading a vendor's
documentation, it says so.

## 1. Scope

One interface over five storage backends for whole-object reads and writes,
with transparent compression driven by the path extension.

Deliberately **not** provided: listing, deleting, copying server-side, ACL or
permission management, metadata or content-type control, multipart tuning,
retries or backoff, and resumable transfers. Callers needing those use the
underlying SDKs directly.

## 2. Interfaces

```go
type Reader interface {
    NewReader(context.Context, string) (io.ReadCloser, error)
}

type Writer interface {
    NewWriter(context.Context, string) (io.WriteCloser, error)
}

type ReadWriter interface { Reader; Writer }

type Aborter interface { Abort() error }
```

A backend implements `Reader`, `Writer`, or both. `Aborter` is optional and
is described in §6.

### 2.1 Lifecycle

- The caller closes every `io.ReadCloser` returned.
- The caller closes every `io.WriteCloser` returned. **On the cloud
  backends `Close` is what commits the object**; a writer that is never
  closed publishes nothing.
- A `Close` error is meaningful on writers and must be checked: for S3 it
  carries the upload result, because the upload runs in a background
  goroutine.

## 3. Backends

| Backend | Read | Write | Constructor |
|---|---|---|---|
| Local filesystem | ✓ | ✓ | `&FileBucket{}` |
| HTTP / HTTPS | ✓ | — | `NewHTTPBucket(client, baseURL)` |
| Backblaze B2 | ✓ | ✓ | `NewB2Client(ctx, keyID, appKey, bucket)` |
| Google Cloud Storage | ✓ | ✓ | `NewGCSClient(ctx, serviceAccountFile, bucket)` |
| Amazon S3 / compatible | ✓ | ✓ | `NewS3Client(ctx, accessKey, secretKey, bucket, endpoint, region)` |

### 3.1 Local filesystem

`NewWriter` creates missing parent directories with mode `0755` and
truncates any existing file. The `ctx` parameter is accepted to satisfy the
interface and is unused — local file operations are not cancellable. The
returned writer implements `Aborter`.

### 3.2 HTTP

Read-only. The per-call path is **joined onto** the base URL, so a base of
`https://host/v1` reading `/obj.gz` requests `https://host/v1/obj.gz`.
Scheme, host and any userinfo credentials are reused for every request; the
bucket's base URL is never mutated. Non-2xx responses become an error with
the URL redacted, after the body is drained so the connection can be reused.
A nil client at construction becomes `http.DefaultClient`.

### 3.3 Backblaze B2

`ConcurrentDownloads` sets the number of parallel range requests; zero uses
blazer's default. Writers are created with `b2.WithCancelOnError` — see §6.3.

### 3.4 Google Cloud Storage

An empty `serviceAccountFile` uses Application Default Credentials. A
non-empty one is declared as a service account
(`option.WithAuthCredentialsFile(option.ServiceAccount, …)`).

**Measured:** declaring the type is presently a no-op. The storage client
resolves credentials through `DetectDefault`, which reads the file but
discards the declared type (`internal/creds.go` passes `credsFile, _`); only
the older `baseCreds` path forwards it. Feeding an `authorized_user` file
and an `external_account` file naming an executable through both the
deprecated and current option produces byte-identical errors. The statement
is therefore an expectation, not an enforced guarantee.

The underlying `*storage.Client` is not exposed and is never closed.

### 3.5 Amazon S3 and S3-compatible

`region` defaults to `"auto"`. A non-empty `endpoint` also enables
path-style addressing, which is what R2 and MinIO need. Both keys empty
falls back to the default AWS credential chain.

Writes stream through an `io.Pipe` into a background `manager.Uploader`, so
payloads are not buffered whole in memory. `Close` blocks until the upload
finishes and returns its error.

## 4. Path and key handling

`InitReader`, `InitWriter`, `Copy` and `Open` all reduce their path argument
to a storage key with `cleanPath`:

1. Everything from the first `?` onward is dropped (a presigned-URL
   signature, typically).
2. If the remainder contains `://`, the scheme and authority are removed and
   the key is everything from the first `/` of the path onward, leading
   slash included. A URL with an authority but no path yields `""`.
3. Otherwise the path is used unchanged.

`cleanPath` deliberately does **not** use `url.Parse`, which rejects a bare
`%` and treats `#...` as a fragment. Both characters are legal in keys and
must survive.

Each cloud backend then strips leading slashes from the key. This is
required, not cosmetic — see AGENTS.md §4.

### 4.1 Worked examples

| Input | Key passed to the backend |
|---|---|
| `magic/x.json.xz` | `magic/x.json.xz` |
| `/magic/x.json.xz` | `/magic/x.json.xz`, then `magic/x.json.xz` on B2/S3/GCS |
| `b2://bucket/magic/x.json.xz` | `/magic/x.json.xz` → `magic/x.json.xz` |
| `https://host/v1/obj.gz?sig=abc` | `/v1/obj.gz` |
| `/data/50%off.json.gz` | unchanged — `%` preserved |
| `report#3.json.gz` | unchanged — `#` preserved |
| `2024:report.gz` | unchanged — not treated as a scheme |
| `b2://bucket` | `""` |

## 5. Compression

Selected by suffix on the cleaned key, for both directions:

| Suffix | Codec | Read | Write |
|---|---|---|---|
| `.gz` | gzip | `compress/gzip` | `compress/gzip` |
| `.bz2` | bzip2 | `compress/bzip2` | `dsnet/compress/bzip2` |
| `.xz` | xz / LZMA2 | `xi2/xz` | `ulikunitz/xz` |

Any other suffix passes through uncompressed. Compression levels are not
configurable; codec defaults apply.

The two different xz libraries are deliberate — see AGENTS.md §7 and
`todo/004`.

Because the suffix drives both directions independently, `Copy` transcodes:
a `.gz` source to a `.xz` destination decompresses and recompresses in one
streaming pass.

## 6. Abort semantics

### 6.1 The problem

`Close` publishes. If a transfer fails partway and the destination is
closed, a truncated object is committed and the caller cannot tell a later
reader that it is bad.

### 6.2 The contract

`Copy` calls `abortWrite(w)` instead of `Close` when the transfer fails, and
joins any abort error onto the transfer error rather than dropping it — a
failed abort can strand billable multipart parts. `InitWriter` does the same
when a compressor fails to construct over an already-opened storage stream.

A destination that does not implement `Aborter` is closed instead and
retains whatever was written. This is stated, not silently assumed.

For a compressed destination, `multiCloser.Abort` deliberately skips the
compression layer — flushing it would only finish framing a partial
object — and aborts the underlying storage stream, which is always last in
the closer list.

### 6.3 Per-backend mechanism

| Backend | Mechanism |
|---|---|
| Local | close the file and `os.Remove` it — `NewWriter` already truncated any previous contents, so leaving it would be truncated data that reads as valid |
| S3 | `pw.CloseWithError` fails the uploader's read so it aborts instead of completing from a clean EOF, then the context is cancelled — **in that order** |
| B2 | cancel the write context; the writer is built with `b2.WithCancelOnError` so `b2_cancel_large_file` is actually issued, using a `context.WithoutCancel` context that outlives the cancelled one |
| GCS | cancel the write context, which is what Google's docs prescribe (`CloseWithError` is deprecated) |

**Verification status.** The local and compressed paths are covered by
offline tests. The B2 path is verified against a live bucket, including a
150 MiB transfer that crosses blazer's 100 MB large-file threshold. The
S3 and GCS paths are verified by source reading only — no credentials were
available. See `todo/008`.

**GCS residue:** incomplete resumable uploads never appear in the bucket and
do not count toward storage; the session expires after one week. So an
abandoned GCS upload leaves nothing billable, unlike S3 and B2 parts.

## 7. Errors

`InitReader`, `InitWriter`, `Copy` and `Open` wrap failures with `%w` and a
`simplecloud:` prefix naming the operation and key, so `errors.Is` and
`errors.As` keep working — `os.ErrNotExist` survives, and a test asserts it.
The thin backend methods return SDK errors unwrapped.

Cloud `NewReader`/`NewWriter` return a nil error eagerly; the handles are
lazy, so failures surface on first `Read`, `Write` or `Close`.

## 8. `Open` — scheme dispatch

`Open(ctx, path, opts...)` picks a backend from the URL scheme and delegates
to `InitReader`.

| Scheme | Backend | Required options |
|---|---|---|
| *(none)* | local filesystem | — |
| `http`, `https` | HTTP(S), base = scheme + host | `WithHTTPClient` (optional) |
| `b2` | Backblaze B2, host = bucket | `WithB2Credentials` |
| `s3` | S3 / compatible, host = bucket | `WithS3Credentials`, `WithS3Endpoint`, `WithS3Region` |
| `gs` | GCS, host = bucket | `WithGCSServiceAccount` |
| anything else | error, unless a resolver handles it | `WithResolver` |

Scheme and host come from `url.Parse`. A path with no host — a local file,
or a `scheme:opaque` form such as `report:v2/file.gz` — or one `url.Parse`
rejects, such as a local path containing a bare `%`, is treated as a local
filesystem path. The **original** path is passed to `InitReader`, so keys
keep their `#`.

Consequence: a *remote* key containing a bare `%` is not addressable through
`Open`, because `url.Parse` fails and the path falls back to local.
Percent-encode it, or construct the backend directly.

### 8.1 Resolver

```go
type BucketResolver func(ctx context.Context, scheme, host string) (Reader, error)
```

Consulted **before** the built-in schemes, so it can add new schemes or
override built-in ones. Returning `(nil, nil)` falls through.

It is also the supported way to control client lifecycle: the built-in `s3`
and `gs` schemes construct a client per call and never close it, which is
fine for short-lived processes but leaks in a long-running one. A resolver
can return a shared backend whose client the caller closes.

### 8.2 Options

`OpenOption` values apply in order; a later option of the same kind
overrides an earlier one. The library never reads credentials from the
environment on its own — except through each SDK's own default chain when
the corresponding option is left empty.

## 9. Guarantees and non-guarantees

**Guaranteed**

- A given path addresses the same object on every backend.
- A failed `Copy` publishes no object on a backend implementing `Aborter`.
- Compression round-trips for all three codecs.
- `errors.Is`/`As` see through the wrapping.
- `%` and `#` survive in keys (`#` through `Open` too).

**Not guaranteed**

- Context cancellation does not interrupt local filesystem operations.
- No retries, backoff or resumption.
- Cloud clients created by `Open` are not closed.
- Concurrent use of one writer from multiple goroutines is not supported.
- Error strings are not stable; match with `errors.Is`, never on text.

## 10. Versioning

Tags are `v0.0.N`; the API is pre-1.0 and has taken breaking changes
(`MultiCloser` was unexported in v0.0.13). The Go module proxy is immutable,
so a defective release can only be retracted, and the `retract` directive
must ship in a *later* version's `go.mod` — otherwise consumers resolve
backwards. `v0.0.10` and `v0.0.12` are retracted; both entries in `go.mod`
record why.
