# 004 — Drop one of the two xz libraries

**Status:** blocked upstream. Recommend leaving as-is.

## The situation

`io.go` imports two xz implementations: `ulikunitz/xz` for writing and
`xi2/xz` for reading. This looks like a redundant dependency. It is not.

## Root cause

`ulikunitz/xz`'s `lzma/breader.go` does a single one-byte `Read` with no
retry and converts a `(0, nil)` return into a fatal
`breader.ReadByte: no data`:

```go
n, err := r.Reader.Read(r.p)
if n < 1 {
    if err == nil {
        err = errors.New("breader.ReadByte: no data")
    }
    return 0, err
}
```

`io.Reader`'s contract explicitly tells callers to treat `(0, nil)` as
"nothing happened" and retry. blazer's B2 reader returns exactly that at
every download-chunk boundary (`ChunkSize` defaults to `1e7`), so reads fail
once the **compressed** object exceeds ~10 MB:

```
 9.9 MB compressed → 0 chunk boundaries → OK
11.5 MB compressed → 1 chunk boundary   → breader.ReadByte: no data
```

The size dependence is why it presents as intermittent — small test objects
pass. `xi2/xz` reads into its own 8 KiB buffer and loops on `rn == 0`, so it
is unaffected. Still present in `v0.5.16`.

Reported as [ulikunitz/xz#79](https://github.com/ulikunitz/xz/issues/79)
(filed 2026-08-30). A patch exists that fixes the reproducer and keeps the
full upstream suite green, but opening that PR was deliberately parked.
Upstream cadence is sparse, so nothing here should wait on it.

## If it is ever consolidated anyway

Two things are required, not one:

1. Wrap the source in a reader that retries `(0, nil)`, bounded, returning
   `io.ErrNoProgress` on a genuinely stalled source. `bufio` does **not**
   work — it passes `(0, nil)` straight through.
2. Set an explicit `xz.ReaderConfig{DictCap: N}`. `xi2` caps the LZMA2
   dictionary at 64 MiB and returns `ErrMemlimit`; `ulikunitz` grows to
   whatever the stream declares. Dropping `xi2` also drops that memory
   guard against a hostile `.xz`.

Net: one fewer dependency, in exchange for a shim plus a `DictCap` someone
has to remember. That trade is why this is not done.
