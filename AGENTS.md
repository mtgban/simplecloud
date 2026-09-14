# AGENTS.md

Working notes for automated agents (and humans) changing this repository.
Read this before editing; several of the rules below exist because breaking
them produced silent data corruption in production, not hypothetically.

## What this is

`github.com/mtgban/simplecloud` is a small Go library (~900 lines of
non-test code) giving one interface over five storage backends: the local
filesystem, HTTP(S), Backblaze B2, Google Cloud Storage and Amazon S3
(including S3-compatible stores). It is consumed by `go-mtgban`, which uses
it to publish and read compressed datastore dumps.

`SPECIFICATIONS.md` describes the behaviour contract. `todo/` holds known
improvements that are deliberately not done yet, each with the reasoning.

## The gate

Every change must pass all five, and CI enforces them:

```sh
gofmt -s -l .                 # must print nothing
go vet ./...
go run github.com/mgechev/revive@v1.13.0 -set_exit_status -config .revive.toml ./...
go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
go test -race ./...
```

Both linters are pinned on purpose: an unpinned release finding new things
would fail a build that changed nothing.

**Run them under the `go.mod` toolchain, not a newer local Go.** staticcheck
understands the compiler's export data only up to the Go release it was
built against, so a newer local toolchain makes it fail with

```
internal error in importing "container/list" (cannot decode …,
export data version 4 is greater than maximum supported version 2)
```

which looks like a broken tree and is not. CI is unaffected because
`setup-go` reads `go-version-file: go.mod`. Locally, match it:

```sh
GOTOOLCHAIN=go1.25.0 go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
```

Observed with local Go 1.27.1 against staticcheck 2025.1.1; an older
staticcheck (v0.4.6) panics outright on a Go it does not know, so the
symptom varies with the pair. When bumping the pin, bump it to a release
built against a Go at least as new as `go.mod`'s.

**A local `revive ./...` pass is not sufficient.** It does not report
`unhandled-error` findings in the external test package (`package
simplecloud_test`) locally, though it does on CI — the rule needs type
information, and the test package does not always get it. Syntactic rules
(`var-naming`) do fire locally, which makes the gap easy to miss. Before
pushing, also run revive with an explicit file list:

```sh
go run github.com/mgechev/revive@v1.13.0 -config .revive.toml *.go
```

and ignore the `should have a package comment` lines that mode produces —
revive cannot see the package doc comment when handed loose files.

## Invariants that must not be broken

### 1. `Close()` publishes. Never close a stream whose operation failed.

On **every** cloud backend, `Close()` is what commits the object: the S3
uploader completes, B2 and GCS finalise. Closing a writer after a failed
transfer therefore publishes a **truncated or empty object** that later
readers cannot distinguish from a good one, while the caller sees only an
error.

Use `abortWrite(w)` instead, and do not discard its result — a failed abort
can strand an unfinished multipart upload whose parts keep incurring
storage charges. `Copy` and `InitWriter` both do this; any new failure path
that has already opened a storage writer must too.

### 2. Object keys are not URLs. Do not run them through `url.Parse`.

`cleanPath` uses string operations, not `url.Parse`, and must keep doing so.
`url.Parse` rejects a bare `%` as an invalid escape and swallows `#...` as a
fragment — both are legal in filesystem paths and object keys. A previous
version parsed keys as URLs and silently wrote objects under truncated
names.

`Open` does use `url.Parse`, but only to read the scheme and host; the
original path still goes to `InitReader`, so the key is untouched. Keep that
split.

### 3. A scheme-qualified URL is not an object key.

`b2://bucket/dir/file.xz` must resolve to key `dir/file.xz` in `bucket`.
Release v0.0.10 wrote the entire URL as the object key — a real dump landed
at the literal key `b2://mtgban-dumps/magic/.../HASealed.json.xz`. That is
what `retract v0.0.10` in `go.mod` refers to, and what `TestCleanPath`
guards.

### 4. Leading slashes are stripped on every cloud backend.

Not cosmetic. `aws-sdk-go-v2`'s `Upload`/`PutObject` **silently succeeds
without storing anything** when the key begins with `/`
(aws/aws-sdk-go-v2#1701), and reads with leading or double slashes 404
(aws/aws-sdk-go#2559). B2 and GCS would create objects literally named
`/foo`. Do not "clean up" the `strings.TrimLeft(path, "/")` calls.

### 5. B2 needs `WithCancelOnError` to cancel a large-file upload.

Cancelling the write context alone is **not** enough. blazer only issues
`b2_cancel_large_file` when the writer was created with
`b2.WithCancelOnError`; otherwise `setErr` returns early at
`if w.ctxf == nil` and the unfinished large file is left behind, billable
and invisible in a normal object listing. The cancel request also needs a
context that outlives the cancelled one, hence `context.WithoutCancel`.

Verified against a live bucket: before the fix a 150 MiB failing transfer
left an entry in `b2_list_unfinished_large_files`; after it, three
consecutive runs left nothing.

### 6. `s3PipeWriter.Abort`'s ordering is load-bearing.

`manager.Uploader` issues `AbortMultipartUpload` on the *same* context
passed to `Upload`. `Abort` must fail the pipe read, wait on `done`, and
only then `cancel()`. Cancelling first kills the abort request too and
strands the parts. There is a comment saying so at the call site; keep it.

### 7. The two xz libraries are deliberate.

`io.go` imports `ulikunitz/xz` for writing and `xi2/xz` for reading. This is
not a redundant dependency. `ulikunitz/xz`'s `lzma/breader.go` turns a legal
`(0, nil)` read into a fatal `breader.ReadByte: no data`, and blazer's B2
reader returns exactly that at every 10 MB download-chunk boundary — so
reads fail once the *compressed* object exceeds ~10 MB. Reported upstream as
ulikunitz/xz#79. See `todo/004-consolidate-xz-libraries.md` before touching
it.

## House style

- **No wrapper function whose body is a single stdlib call.** Inline it and
  put the rationale in a comment. A helper earns its place when it has real
  logic — `cleanPath` does; a `normalizeKey` that only called
  `strings.TrimLeft` did not, and was rejected in review.
- **Resolve a linter finding by fixing the code or exempting the rule
  family in `.revive.toml`** — not by annotating the call site with
  `_, _ =`. The exemption list carries the reasoning.
- Comments explain *why*, especially for anything that looks removable.
  Most of the invariants above are one-line changes away from being
  "simplified" back into bugs.
- Doc comments on all exported symbols, starting with the symbol name.
- Errors are wrapped with `%w` and a `simplecloud:` prefix in the
  `Init*`/`Copy`/`Open` layer; the thin backend methods return SDK errors
  as-is.

## Testing expectations

26 tests, all offline — no credentials or network beyond `httptest`. New
behaviour needs a test that **fails without the change**; several fixes in
this repo were verified that way and it caught a wrong first attempt.

Cloud behaviour is faked with `commitBucket`, which models the real
semantics: `Close` records `committed`, `Abort` records `aborted`, and
`failWrites` makes a compressor fail to initialise. Prefer extending it over
writing a new fake.

Be aware that mocks were **not** sufficient historically: the B2 large-file
leak passed every mock test and the small-file live test, and only appeared
in a >100 MB transfer against a real bucket.

## Git and release conventions

- Work in a git worktree, never a shared checkout.
- Every PR branches off `master`. **Never stack PRs.**
- Never push to `master` without being asked for that specific push.
- Tags are `v0.0.N`. The Go module proxy is immutable: a bad release can
  only be **retracted**, and the `retract` directive must live in a *later*
  version's `go.mod`, released at the same time, or consumers resolve
  backwards to an older, worse version.
- Not every change deserves a release. CI, lint config and verified
  behaviour-preserving changes ride along with the next real one.
