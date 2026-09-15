# 011 — No `Delete`

**Status:** `List` is implemented; `Delete` remains out of scope.

## What changed

This item originally covered both `List` and `Delete`, and argued both were
out of scope. `List` has since been implemented for the three cloud backends
as an optional `Lister` interface — see SPECIFICATIONS §10.

The original objection to `List` was that it has no clean cross-backend
shape: pagination, prefix/delimiter semantics and the meaning of a
"directory" differ between the filesystem, B2, GCS and S3, and HTTP has no
listing at all. That was resolved rather than ignored:

- **Pagination** is hidden behind `iter.Seq2`, so no backend's page-token
  model leaks into the API.
- **Delimiters** are simply not offered. Listing is flat, which is the one
  behaviour every backend agrees on; "common prefixes" would have required
  picking a winner between three different models.
- **HTTP and the filesystem** do not implement the interface at all, rather
  than implementing it badly. `Lister` is optional, like `Aborter`.
- **`LastModified`** could not be fully normalised, so the difference is
  documented rather than papered over: B2 may report an upload time where
  S3 and GCS report a modification time.

## `Delete`

Still not provided, and still the easier of the two to add if wanted: a
narrow `Delete(ctx, key) error` on an optional interface, alongside
`Aborter` and `Lister`, would not disturb the core contract.

Two things make it worth pausing on rather than adding reflexively:

1. It is the first destructive operation in the library. Everything else
   either creates or reads; a mistaken key in `Delete` is unrecoverable on a
   bucket without versioning.
2. Backend semantics differ more than for `List`. B2 hides rather than
   removes by default and keeps versions; GCS and S3 differ on whether a
   delete of a missing object is an error.

If it is added, the live-test helpers in this repo currently drop to blazer
directly to clean up after themselves, and would be able to use it instead.
