# todo

Known improvements that are deliberately not done yet. Each file states the
problem, the evidence for it, a proposed fix, and whatever is blocking it.
An item being listed here is not a commitment to do it — several are
recorded specifically so they are not "fixed" by someone who has not seen
the reason they were left alone.

Ordered roughly by value, not by number.

| # | Item | Status |
|---|---|---|
| [001](001-compression-enum.md) | Single-source the compression table | Ready; no API impact |
| [002](002-scheme-constants.md) | Export scheme constants for resolvers | Ready; non-breaking if additive |
| [005](005-write-side-open.md) | Write-side counterpart to `Open` | Ready; API addition |
| [006](006-compression-levels.md) | Configurable compression levels | Ready; API addition |
| [008](008-verify-s3-gcs-abort.md) | Verify S3/GCS abort against live buckets | Blocked: no credentials |
| [003](003-s3-transfermanager.md) | Migrate off deprecated `manager.Uploader` | Blocked: replacement is pre-1.0 |
| [004](004-consolidate-xz-libraries.md) | Drop one of the two xz libraries | Blocked: upstream bug |
| [007](007-s3-config-struct.md) | Replace `NewS3Client`'s six string params | Breaking; needs a decision |
| [009](009-revive-test-file-gap.md) | Lint gate misses test files locally | Open question |
| [010](010-open-client-lifecycle.md) | `Open` never closes S3/GCS clients | Documented; needs a real fix |
| [011](011-list-and-delete.md) | No `List` or `Delete` | Out of scope by design |
| [012](012-multicloser-embedding.md) | `multiCloser` embeds both directions | Cosmetic |
