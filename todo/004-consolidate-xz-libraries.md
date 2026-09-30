# 004 — Drop one of the two xz libraries

**Status:** no longer blocked upstream. It is now a trade-off, not a blocker,
and the trade is currently against consolidating.

## What forced the split

`io.go` imports `ulikunitz/xz` for writing and `xi2/xz` for reading.
`ulikunitz`'s `lzma/breader.go` converted a legal `(0, nil)` read into a fatal
`breader.ReadByte: no data`, and blazer's B2 reader returns `(0, nil)` once per
10 MB `ChunkSize` — measured at offsets 10000000, 20000000 and EOF on a
25,000,000-byte object, unchanged by `ConcurrentDownloads` or the caller's
buffer size. The LZMA range decoder pulls every compressed byte through
`breader`, so a compressed object spanning a boundary failed deterministically.

**Fixed upstream in v0.5.17** (2026-09-19, ulikunitz/xz#79).

One subtlety worth keeping, because it invalidated a first attempt at a live
test: **incompressible payloads do not reproduce it.** LZMA2 falls back to
uncompressed chunks, which are read in bulk and never reach `breader`. A test
using random data crossed 2.5 chunk boundaries and passed against the broken
version. Any reproducer here needs compressible data — real payloads are JSON —
and should assert the stall actually fired.

## What consolidating would now cost

`xi2` caps the LZMA2 dictionary at 64 MiB and returns `ErrMemlimit`.
`ulikunitz` allocates whatever the stream's block header declares. Measured on
an 84-byte `.xz`, with `DictCap` explicitly set to 1 MiB in the third column:

| declared dictionary | xi2 | ulikunitz default | ulikunitz DictCap=1MiB |
|---|---|---|---|
| as written (8 MiB) | 1 MiB | 8 MiB | 8 MiB |
| 1 GiB | `ErrMemlimit`, 0 | 1024 MiB | 1024 MiB |
| 4 GiB - 1 | `ErrMemlimit`, 0 | 4096 MiB | 4096 MiB |

`xz.ReaderConfig{DictCap: N}` is a floor, not a ceiling — `lzmafilter.go` does
`if dc > config.DictCap { config.DictCap = dc }`. So there is no configuration
that restores the guard, and a caller cannot add one from outside the library:
the dictionary size lives in *each* block header, so checking only the first is
bypassed by a stream with a second block.

This matters most for `HTTPBucket`, which reads whatever URL it is given.

Reported as [ulikunitz/xz#84](https://github.com/ulikunitz/xz/issues/84).

## v0.6 removes the cost, and adds a different one

Measured on `v0.6.0-alpha.3`: allocation tracks the data rather than the
declaration, so the same 84-byte streams allocate 0.03 MiB whatever they
declare. The amplification is gone.

In its place, a declared size of 2 GiB or more panics with
`lz: buffer is full`. The streams are valid — v0.5.17 decodes them and `xz`
5.8.4 reads them — and under the default `Workers = GOMAXPROCS` the panic
fires inside `mtrWork`'s goroutine, where the caller cannot recover it. Both
halves are in #84.

## Recommendation

Keep both libraries while v0.5.x is current. Consolidating buys one fewer
dependency and costs an uncappable allocation on any `.xz` this library did not
write. Re-measure when v0.6 ships — the numbers above are version-specific and
the trade flips if the panic is turned into an error.
