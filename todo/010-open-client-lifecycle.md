# 010 — `Open` never closes the S3 and GCS clients it creates

**Status:** documented, with an escape hatch. A real fix is still open.

## Problem

`Open`'s built-in `s3` and `gs` schemes construct a client per call and
never close it. `*storage.Client` in particular holds connections that
should be released. For a CLI that opens one object and exits this is
harmless; in a long-running process that calls `Open` in a loop it leaks.

## Current mitigation

Two, both partial:

- `Open`'s doc comment states it outright.
- `WithResolver` lets a caller return a backend whose client they own and
  close, bypassing the built-in construction entirely.

## Why a proper fix is awkward

`Open` returns an `io.ReadCloser`. Closing it closes the *object* stream,
not the client, and there is nowhere to hang the client's lifetime without
either returning a second closer — changing the signature — or caching
clients inside the package, which introduces shared mutable state and a
cache-invalidation question the library has so far avoided.

## Options

1. Leave it; document harder. (Current.)
2. Return a value that carries both the stream and a `Close` covering the
   client. Breaking.
3. Cache clients per (scheme, host, credentials) inside `openOptions`,
   closed when the caller closes something. Adds state and lifetime
   questions.
4. Recommend `WithResolver` as the supported pattern for long-running
   processes and treat the built-ins as a short-lived-use convenience. This
   is effectively today's position, just stated as a decision.
