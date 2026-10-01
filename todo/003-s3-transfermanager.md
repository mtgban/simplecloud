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

1. **The replacement is pre-1.0** — still `v0.4.12` as of 2026-10-01. Pointing a
   published library's dependency from a stable `v1.22.x` module at a `v0.x`
   one with no API-stability guarantee is a real cost for consumers.
2. **Its abort must be re-verified.** `Abort` currently depends on
   `manager.Uploader` calling `AbortMultipartUpload` with
   `LeavePartsOnError` defaulting false. The offline route in 008 can now
   check that without credentials; a live bucket is still the stronger
   check, since B2 is precisely where reading the source alone gave the
   wrong answer.

## Recommendation

Revisit when `transfermanager` reaches v1. Land 008's offline abort test
first, so the migration has something to fail against.
