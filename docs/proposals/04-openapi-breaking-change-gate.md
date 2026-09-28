# Proposal 04 — A breaking-change gate for the OpenAPI contract

**Status:** proposal. Nothing is implemented. A human decides.

## The gap

`tools/contractcheck` makes a breaking **protobuf** change a build failure. It
runs as the first gate in `scripts/verify.ps1`, against
`contracts/compat/baseline.json`.

`contracts/openapi/v1/openapi.yaml` exists. **Nothing checks it.**

So of the three contracts this repository publishes, one is generated and
compatibility-checked, one is JSON Schema and checked for *internal divergence*
by `contract-sync` (it fails if `internal/rules/schema/rule-bundle.schema.json`
drifts from the published copy), and one is documentation. The OpenAPI contract
describes the operator-facing API — the surface that talks to the customer, the
one `internal/README.md` calls the most important unverified component — and a
breaking edit to it passes `verify.ps1` silently.

## Why we need it

The argument for gating protobuf is the argument for gating OpenAPI, and it is
stronger here, not weaker. Protobuf is consumed by generated Go inside this
repository. OpenAPI describes the wire to a **client this repository does not
own** — the console is server-rendered, so the real consumers are third-party
integrations and customer tooling whose breakage we would not see in any test.

A removal of a response field, a narrowing of an enum, or a required-parameter
added to a `GET` would be a silent breaking change to those consumers.

## Candidates

| Tool | Licence | Notes |
|---|---|---|
| **`oasdiff`** | Apache-2.0 (Go) | `oasdiff breaking base.yaml new.yaml --fail-on ERR`. Stable change IDs, severity levels, exit codes designed for CI. Compares two OpenAPI documents. **Go**, so it fits the existing toolchain with no new language. |
| `@stoplight/spectral-cli` | MIT (npm) | Lint, not compatibility. Style and correctness rules against a ruleset, SARIF output. Complementary, not a substitute. |
| **`bufbuild/buf`** | Apache-2.0 | Not an OpenAPI tool. Relevant to the protobuf side only; see proposal 06. |

## Why this over the alternatives

The alternative is the existing `contractcheck`, extended to parse OpenAPI.
`contractcheck` works on the protobuf descriptor set, which has a machine-readable
representation. OpenAPI has no equivalent canonical form, and the reference
tooling for it is already written, in Go, by people who do this full time.

## What we would build

1. Commit the current `openapi.yaml` as a baseline, the same shape as
   `contracts/compat/baseline.json`.
2. Add a `contract-openapi` gate to `scripts/verify.ps1`, after `contract-check`.
3. Fail on `ERR`, report on `WARN`. A first run will very likely produce
   findings, so the gate should be introduced non-blocking, findings triaged,
   then made blocking.

## What we would not do

- **Not hand-edit the OpenAPI to satisfy the linter.** The OpenAPI is the
  contract; the tool is the referee. If they disagree, the human decides which
  is wrong.
- **Not derive OpenAPI from the Go handlers.** Generated-from-code specs drift
  in a different way and lose the descriptions that make a contract usable.
  `internal/README.md`'s row for "a new API endpoint" requires the change in
  `contracts/openapi/v1/openapi.yaml` by hand; that discipline should stay.
- **Not bundle it with `contract-sync`.** One gate, one failure, named in the
  output. `verify.ps1` already exits non-zero naming whatever failed; that
  property is worth more than gate count.

## Risks

- **Tool version drift.** Change IDs and severities change between releases.
  Pin the version, and record the pin the way the Go toolchain version is
  recorded.
- **False positives on descriptions.** A reformatted description is not a
  breaking change. The gate must be configured to that effect, and a mis-
  configured gate trains people to ignore red — the failure mode `AGENTS.md`
  already names for a pipeline that never runs.
- **A gate nobody believes.** If the first blocking run is noisy, it will be
  disabled within a week and the repository will be worse off than having none.
  This is the main reason the non-blocking introduction is staged.

## Confidence

High. The gap is verified by reading `scripts/verify.ps1` and the `contracts/`
tree. The tool's behaviour is from its own documentation.

## Decision required

Is there a committed OpenAPI consumer — first-party console, partner
integration, or customer — whose breakage would cost something? If not, this
document is a comment in a YAML file and the gate is theatre.
