# 001 — Single-source the compression table

**Status:** ready. Internal only, no API impact.

## Problem

`io.go` writes the suffix chain out twice — once in `InitReader`, once in
`InitWriter`:

```go
if strings.HasSuffix(key, ".xz") { … }
else if strings.HasSuffix(key, ".bz2") { … }
else if strings.HasSuffix(key, ".gz") { … }
```

Nothing keeps the two in sync. Adding `.zst` to the writer alone would
silently produce objects the reader treats as uncompressed — the same
silent-mismatch class as the bugs that produced two retracted releases.

## Proposed fix

An unexported enum plus one detection function:

```go
type compression int

const (
    compressNone compression = iota
    compressGzip
    compressBzip2
    compressXz
)

func compressionFor(key string) compression { … }
```

`InitReader` and `InitWriter` then `switch` over the result. The suffix
table exists once, and a `switch` gives some exhaustiveness pressure when a
codec is added.

## Why it is worth doing

This is the one type-hardening change in the repo that prevents a real bug
class rather than adding ceremony. Named string types for keys, buckets and
regions were assessed and rejected — see 007 for the measurement.

## Risk

Low. Internal refactor, fully covered by `TestCompression_RoundTrip` and
`TestCompression_FileIsActuallyCompressed` across all three codecs.
