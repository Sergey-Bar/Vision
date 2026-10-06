# Proposal 06 — `buf` for protobuf lint and format

**Status:** proposal. Nothing is implemented. A human decides.

## The gap

`internal/README.md` documents the generation step precisely:

```sh
protoc -I contracts/protobuf \
  --go_out=. --go_opt=module=github.com/Sergey-Bar/Ai-Captain \
  contracts/protobuf/security/v1/*.proto
go run ./tools/contractcheck -mode check \
  -baseline contracts/compat/baseline.json -root contracts/protobuf
```

`protoc` is invoked directly, with a plugin discovered from `PATH`, at a pinned
file list. There is no protobuf **lint** and no **format** gate. There is a
**compatibility** gate, and it is the strongest of the three.

The risk this leaves: nothing enforces naming, enum-zero-value, or package
conventions on new messages, and nothing normalises formatting. `gofmt` runs
over the generated Go, so the *output* is formatted; the *schema* is not.

## The candidate

**`bufbuild/buf`** — Apache-2.0. It replaces day-to-day `protoc` with a
single CLI: build, lint (40+ built-in rules), format, breaking-change detection,
and generation from a checked-in template.

Two things make it interesting here and one makes it optional.

**Interesting: `buf breaking --against '.git#branch=main'`.** This is the same
job `tools/contractcheck` does, with a maintained rules catalogue and four
explicit severity levels — `FILE`, `PACKAGE`, `WIRE_JSON`, `WIRE` — because
protobuf breakage is not one thing. Renaming a field breaks generated source but
not the wire; changing a type breaks everything. Our own `contractcheck` is
silent about which of those it detects.

**Interesting: `buf lint`.** Catches API-shape mistakes while the author is
still editing, and `buf format -w` makes formatting non-negotiable.

**Optional: `buf generate`.** It would replace the `protoc` invocation above.
But that part is working, `protoc` is not on `PATH` on this machine and is
installed by winget, and `AGENTS.md` already documents that. The
install-the-toolchain problem is not the problem worth solving here.

## What we would build

If adopted at all, the minimum useful change is narrow:

1. `buf.yaml` with a `lint` ruleset.
2. Two new gates in `verify.ps1`: `buf-lint` and `buf-format`, both
   non-blocking on first run.
3. **No change to generation.** `protoc` and `make generate` stay exactly as
   they are.

Optionally, later and separately: evaluate `buf breaking` against
`contractcheck` and pick one, rather than running two compatibility gates with
different opinions.

## What we would not do

- **Not replace `contractcheck` without a written comparison.** It is ours, it
  is tested, and `internal/README.md` describes its scope precisely. Swapping it
  for a general-purpose tool is a change to the mechanism that enforces
  `AGENTS.md`'s "contracts are the source of truth" rule, and that deserves more
  than a dependency bump.
- **Not add a tool that requires network access to a registry.** Buf's CLI works
  without a BSR account, and the build must keep working at a site with no
  network. Local generation stays local.
- **Not run two format gates.** `gofmt` formats the Go; `buf format` formats
  the proto. Both are legitimate, they are not redundant, and neither should
  reformat the other's output.

## Risks

- **A second gate that can go red for the wrong reason.** `AGENTS.md` is blunt
  about this: a check that fails for a reason nobody can act on teaches people
  to ignore red. Introducing two new gates non-blocking first is not
  process timidity, it is the mitigation.
- **Toolchain growth on a machine with a small toolchain.** `go`, `gofmt`,
  `protoc`, `golangci-lint` and `make` are already not all on `PATH`. Adding a
  sixth binary has a real cost here, mitigated because `verify.ps1` already
  resolves its tools itself.
- **Duplicate responsibility.** A linter that disagrees with `contractcheck`
  about what a valid contract is produces a gate that is sometimes right.

## Confidence

High on what Buf does — read from its documentation. Medium on whether the
added value justifies a new tool, because the existing `contractcheck` already
covers the most important property (wire compatibility). The honest summary is:
**this proposal buys consistency, not safety.**

## Decision required

Is protobuf naming and formatting worth a permanent tool dependency when
compatibility is already covered? If the answer is yes, do `buf lint` and
`buf format` only, and leave generation alone.
