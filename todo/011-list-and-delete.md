# 011 — No `List` or `Delete`

**Status:** out of scope by design. Recorded so it is a decision, not an
oversight.

## Situation

The library reads and writes whole objects. It cannot enumerate or remove
them. Every backend's SDK supports both, and callers needing them use the
SDK directly.

## Why it has not been added

The interfaces are deliberately two methods wide, which is what makes a fake
backend a dozen lines and lets `Copy` work across any pair of backends.
`List` in particular has no clean cross-backend shape: pagination,
prefix/delimiter semantics, and what a "directory" means differ between the
filesystem, B2, GCS and S3, and HTTP has no listing at all.

## When it would be worth revisiting

If a consumer needs cleanup of its own objects — for example removing
superseded dumps — a narrow `Delete(ctx, path) error` on an optional
interface (like `Aborter`) would be far easier to get right than `List`, and
would not touch the core two-method contract. Note that a `Delete` would
also make the test helpers in this repo able to clean up after themselves
against a live bucket, which currently requires dropping to blazer.
