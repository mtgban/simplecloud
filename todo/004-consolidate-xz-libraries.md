# 004 — Drop one of the two xz libraries

**Status:** done, with one consequence left open — see "What it cost" below.

## What the problem was

`io.go` imported two xz implementations: `ulikunitz/xz` for writing and
`xi2/xz` for reading. `ulikunitz`'s `lzma/breader.go` did a single one-byte
`Read` with no retry and converted a `(0, nil)` return into a fatal
`breader.ReadByte: no data`. `io.Reader`'s contract explicitly tells callers to
treat `(0, nil)` as "nothing happened" and retry.

blazer's B2 reader returns `(0, nil)` at every download-chunk boundary. Measured
against a live bucket, on a 25,000,000-byte object:

```
conc=0 bufSize=1      (0,nil) returns=3  firstAt=10000000
conc=0 bufSize=4096   (0,nil) returns=3  firstAt=10000000
conc=0 bufSize=32768  (0,nil) returns=3  firstAt=10000000
conc=4 bufSize=32768  (0,nil) returns=3  firstAt=10000000
```

Once per 10 MB `ChunkSize`, regardless of `ConcurrentDownloads` or the caller's
buffer size. The LZMA range decoder pulls *every* compressed byte through
`breader`, so a compressed object spanning a boundary failed deterministically.

Reported as [ulikunitz/xz#79](https://github.com/ulikunitz/xz/issues/79) and
**fixed in v0.5.17** (2026-09-19): `ReadByte` now retries up to 100 times and
returns `io.ErrNoProgress` rather than inventing a fatal error.

## What an earlier draft of this file got wrong

Two things, both of which would have misled the next person:

1. It said the bug reproduced whenever "the compressed object exceeds ~10 MB".
   Necessary, not sufficient. **Incompressible payloads do not reproduce it**:
   LZMA2 falls back to *uncompressed* chunks, which are read in bulk and never
   reach `breader`. A first attempt at a live test used random data, crossed
   2.5 chunk boundaries, and passed against the broken version. The payload has
   to actually compress — real payloads here are JSON, which is why this
   presented as "always fails one direction".

2. It recommended setting `xz.ReaderConfig{DictCap: N}` to restore `xi2`'s
   memory guard. **That does not work.** See below.

## What it cost

`xi2` capped the LZMA2 dictionary at 64 MiB and returned `ErrMemlimit`.
`ulikunitz` allocates whatever the stream declares, and `DictCap` is a floor,
not a ceiling (`lzmafilter.go`: `if dc > config.DictCap { config.DictCap = dc }`).

Measured, with `DictCap` explicitly set to 1 MiB in the third column:

| input | xi2 | ulikunitz default | ulikunitz DictCap=1MiB |
|---|---|---|---|
| 4164-byte .xz, dict=1MiB | 1 MiB | 1 MiB | 1 MiB |
| 4164-byte .xz, dict=1536MiB | `ErrMemlimit`, 0 MiB | 1536 MiB | 1536 MiB |
| 4164-byte .xz, dict=4GiB | `ErrMemlimit`, 0 MiB | 4096 MiB | 4096 MiB |

So a four-kilobyte response can cost four gigabytes of allocation. That matters
most for `HTTPBucket`, which reads whatever URL it is given.

Capping this needs upstream support — a `MaxDictCap` that `lzmafilter.go`
honours as a ceiling. Sniffing it locally is not a substitute: the dictionary
size lives in each *block* header, so a guard that inspects only the first
block is bypassed by adding a second one.

Reported as [ulikunitz/xz#84](https://github.com/ulikunitz/xz/issues/84)
(filed 2026-09-23).

**v0.6 already fixes the allocation side.** Measured on `v0.6.0-alpha.3`:

| declared dictionary | v0.5.17 (DictCap=1MiB) | v0.6.0-alpha.3 |
|---|---|---|
| as written (8 MiB) | 8.04 MiB | 0.03 MiB |
| 1 GiB | 1024.04 MiB | 0.03 MiB |
| 1536 MiB | 1536 MiB | 0.03 MiB |
| 2 GiB and above | allocates it | `panic: lz: buffer is full` |

Allocation there tracks the data, not the declaration, so the amplification is
gone. The panic that replaces it is its own problem — the streams are valid
(v0.5.17 decodes them, `xz` 5.8.4 reads them), and under the default
`Workers = GOMAXPROCS` it fires inside `mtrWork`'s goroutine, where the caller
cannot recover it. Both halves are in #84.

The practical consequence: this trade is temporary. Re-measure when v0.6
ships rather than carrying the table above forward.

## Tests

`xz_test.go` covers all three properties, and each fails against v0.5.16:

- `TestInitReader_XZToleratesZeroByteReads` — offline, compressible payload,
  source stalls at a scaled-down boundary. Asserts the stall actually fired.
- `TestXZ_LiveB2BlazerReturnsZeroNilReads` — pins the premise: blazer really
  does return `(0, nil)`, at offset 10,000,000.
- `TestXZ_LiveB2LargeObject` — 24 MiB compressible payload, 13,870,452 bytes
  stored, round-tripped through `InitWriter`/`InitReader`. Fails with
  `breader.ReadByte: no data` on v0.5.16, passes on v0.5.17.
