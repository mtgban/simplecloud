# 012 — `multiCloser` embeds both `io.Reader` and `io.Writer`

**Status:** cosmetic. Low priority.

## Problem

```go
type multiCloser struct {
    io.Reader
    io.Writer
    closers []io.Closer
}
```

A reader-mode value has a nil `Writer`, and vice versa. So a value returned
by `InitReader` still satisfies `io.Writer`, and calling `Write` on it
panics with a nil-pointer dereference instead of failing cleanly.

## Why it has not been fixed

Nothing type-asserts these. The type is unexported (since v0.0.13), so the
only way to reach the broken half is from inside the package.

## Fix if touched

Split into `multiReadCloser` and `multiWriteCloser`, each embedding only the
direction it uses. `multiCloser.Abort` is write-only in practice and would
move to the writer type, which also removes its `io.WriteCloser` type
assertion.
