# 009 — The lint gate does not report test-file findings locally

**Status:** open question. Worked around, not understood.

## Symptom

`go run github.com/mgechev/revive@v1.13.0 -config .revive.toml ./...` exits
0 locally on a tree where CI reports `unhandled-error` findings in
`simplecloud_test.go`. Same pinned revive, same content — the PR merge
commit was confirmed byte-identical to the branch.

This produced a green local run and a red CI on an otherwise-clean PR.

## What is known

- Passing the same files explicitly (`revive … *.go`) **does** report them.
- Syntactic rules (`var-naming`) fire on the test file locally; only
  `unhandled-error`, which needs type information, is skipped.
- Ruled out: Go toolchain version (forced 1.25 to match
  `go-version-file: go.mod`), `GOFLAGS`, and any tree difference.
- The affected package is the *external* test package
  (`package simplecloud_test`).

Hypothesis, unconfirmed: revive fails to load type information for the
external test package in some environments and silently degrades to
syntax-only rather than erroring.

## Current workaround

`.revive.toml` exempts `io.WriteString`, which is what surfaced. AGENTS.md
tells contributors to run the explicit-file form before pushing and treats
CI as the authority.

## Why it is worth resolving

A gate that silently applies fewer rules in one environment than another
will hide a real finding eventually. Worth reporting upstream with a minimal
reproduction, or switching the lint step to a form that fails loudly when
type information is unavailable.
