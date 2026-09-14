# 002 — Export scheme constants for resolvers

**Status:** ready, if kept additive.

## Problem

`Open` dispatches on bare string literals (`"http"`, `"https"`, `"b2"`,
`"s3"`, `"gs"`), and `BucketResolver` hands a caller a bare `scheme string`.
Anyone writing a resolver has to guess or read the source for the exact
spellings, and a typo fails silently by falling through to the built-ins.

## Proposed fix

Export constants:

```go
const (
    SchemeFile  = ""
    SchemeHTTP  = "http"
    SchemeHTTPS = "https"
    SchemeB2    = "b2"
    SchemeS3    = "s3"
    SchemeGCS   = "gs"
)
```

## What not to do

Do **not** change `BucketResolver` to take a named `Scheme` type. That
breaks every existing resolver function signature. Adding constants
alongside the current `string` signature captures most of the value at no
compatibility cost.

Note also that a named string type would not catch a transposed *literal*
anyway — untyped constants convert implicitly. That was measured; see 007.

## Risk

None if additive.
