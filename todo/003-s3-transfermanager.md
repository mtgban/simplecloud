# 003 — Migrate off the deprecated `manager.Uploader`

**Status:** blocked. Recommend waiting.

## Problem

`staticcheck` reports SA1019: `feature/s3/manager`'s `Uploader` is
deprecated in favour of `feature/s3/transfermanager`. Currently suppressed
per-site in `s3.go` with reasons.

## Why the replacement is genuinely better

`transfermanager` aborts with a **fresh context**
(`freshCtx, cancel := u.freshContext(ctx)`) rather than the upload context,
and it *reports* abort failures instead of swallowing them the way
`manager.fail()` does (`_ = err`, with a TODO). Migrating would remove the
load-bearing ordering dependency in `s3PipeWriter.Abort` documented in
AGENTS.md §6.

## Why it is blocked

1. **The replacement is pre-1.0** — `v0.4.3` at time of writing. Pointing a
   published library's dependency from a stable `v1.22.x` module at a `v0.x`
   one with no API-stability guarantee is a real cost for consumers.
2. **It cannot be verified here.** `Abort` currently depends on
   `manager.Uploader` calling `AbortMultipartUpload` with
   `LeavePartsOnError` defaulting false. Any migration must re-verify the
   abort against a live bucket — and B2 is precisely where reading the
   source alone gave the wrong answer (see 008).

## Recommendation

Revisit when `transfermanager` reaches v1, or sooner if S3-compatible
credentials (R2, MinIO) become available to test against.
