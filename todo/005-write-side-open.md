# 005 — Write-side counterpart to `Open`

**Status:** ready. Additive API.

## Problem

`Open(ctx, path, opts...)` resolves a backend from a URL scheme for reads.
There is no equivalent for writes, so a caller that can say

```go
r, _ := simplecloud.Open(ctx, "b2://bucket/x.json.xz", opts...)
```

must still hand-construct a client to write the same object back. The API is
asymmetric for no reason other than that reads were needed first.

## Proposed fix

`Create(ctx, path, opts...) (io.WriteCloser, error)`, sharing
`openOptions.bucketFor` and delegating to `InitWriter`.

Two details:

- `bucketFor` returns a `Reader`. It would need to return something that can
  be asserted to `Writer`, or be split, since `HTTPBucket` is read-only and
  must produce a clear error rather than a nil-interface panic.
- The returned writer should keep the `Aborter` contract intact so `Copy`
  and callers can abort it.

## Risk

Low, but it widens the public API, and the `s3`/`gs` client-lifecycle
caveat in 010 applies to it equally — arguably more, since writes are
long-lived.
