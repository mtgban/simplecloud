# 013 — Drop `b2Writer` once blazer fixes #54

**Status:** blocked upstream.

## The situation

`b2.go` wraps blazer's writer in `b2Writer` because of
[Backblaze/blazer#54](https://github.com/Backblaze/blazer/issues/54)
(filed 2026-09-29, open). With `WithCancelOnError`, which AGENTS.md §5
requires, blazer's `setErr` cancels the large file on any error, through a
nil file when none was started, and so panics instead of returning the
error:

- under `ChunkSize` (100 MB), in `Close`, when the upload is refused, times
  out or is aborted;
- over it, in `Write`, when `b2_start_large_file` fails.

`setErr` records the error before it panics, and `b2Writer` recovers the
panic and returns that error. The fix proposed upstream is one guard in
`setErr`: `if w.ctxf == nil || w.file == nil { return }`. Both v0.7.2,
pinned here, and v0.8.0, the latest, have the bug.

## When a release carries the fix

Bump blazer, keep `WithCancelOnError`, and delete `b2Writer` whole:
`NewWriter` then hands blazer's writer straight to `cancelWriter`, and its
`cancelCtx` sets no flag. The B2 tests must keep passing without the
wrapper, since they test behaviour, not the wrapper. Measured with v0.7.2
patched as #54 proposes and `b2Writer` deleted:
`TestB2Bucket_FailedUploadReturnsError` and
`TestB2Bucket_UnrecordedPanicIsRaised` both pass.
