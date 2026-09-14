# 006 — Configurable compression levels

**Status:** ready. Additive API.

## Problem

All three codecs use library defaults, with no way to trade CPU for size.
For a datastore dump republished on a schedule that is a real knob:
`gzip.BestCompression` or a tuned xz preset can change both the stored size
and the publish time materially.

## Proposed fix

Options threaded into `InitWriter`, e.g. a `WriterOption` carrying a level
per codec, defaulting to today's behaviour. `gzip.NewWriterLevel` and
`xz.WriterConfig` both support it; `dsnet/compress/bzip2` takes a
`*bzip2.WriterConfig` that is currently passed as `nil`.

## Open question

`InitWriter`'s signature takes no options today. Adding a variadic
parameter is source-compatible, but it also means `Copy` needs a way to pass
them through, which widens `Copy`'s signature too. Worth designing the
whole path before starting.

## Risk

Low. Defaults preserve current behaviour; round-trip tests cover all three
codecs.
