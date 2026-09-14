# 008 — Verify the S3 and GCS abort paths against live buckets

**Status:** blocked on credentials. Highest-value open verification.

## Why this matters more than it sounds

The abort contract (SPECIFICATIONS §6) is verified unevenly:

| Path | Verified how |
|---|---|
| Local filesystem | Offline test |
| Compressed (`multiCloser`) | Offline test |
| B2 | **Live bucket**, including a 150 MiB transfer past the 100 MB threshold |
| S3 | Source reading only |
| GCS | Source reading only |

The B2 row is the reason this item exists. Source reading said the abort was
correct. Mock tests passed. A live test with a small object passed. Only a
>100 MB transfer revealed that blazer silently left an unfinished large file
behind — billable, and invisible in a normal object listing — because it
only issues `b2_cancel_large_file` when given `WithCancelOnError`.

S3 and GCS are currently in exactly the state B2 was in before that test.

## What to run, given credentials

For S3 (real, or R2/MinIO via `WithS3Endpoint`):

1. A `Copy` whose source fails mid-stream, with a payload above the
   multipart threshold — S3's default part size is 5 MB, so this triggers
   far more easily than B2's 100 MB.
2. Assert no object was committed.
3. Assert `ListMultipartUploads` reports nothing left for that key. This is
   the check that matters; a committed-object check alone would have passed
   on B2 too.

For GCS the residue question is different and was already answered from the
documentation: an incomplete resumable upload never appears in the bucket,
does not count toward storage, and expires after one week. So the GCS risk
is a wrong error or a hang, not a billable leak.

## Note

`manager.Uploader` issues `AbortMultipartUpload` on the same context passed
to `Upload`, which is why `s3PipeWriter.Abort`'s ordering is load-bearing.
That ordering is exactly what a live test would confirm or refute.
