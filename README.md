# security-platform

Physical Security Intelligence Layer — Milestone 1.

One end-to-end operator workflow on a real XProtect installation:

```
XProtect event → canonical event → Security Graph → deterministic verification
  → VLM observation → incident → operator disposition → audit
```

with a complete offline install / upgrade / rollback story, and a deterministic
core that is fully functional with the AI plane completely absent.

## The law

Five invariants that the rest of the code exists to protect.

1. **Zero accepted event loss.** Anything that reaches `inbox_events` and is not
   explicitly dead-lettered reaches a terminal state. `kill -9` at any pipeline
   stage loses nothing and duplicates no side effect.
2. **Decisions replay from stored facts.** A decision made months ago is
   reproduced from stored facts and the stored `AIObservation`. The model is
   never re-run to explain a past outcome.
3. **A missing AI observation is never a negative observation.** AI is never on
   the availability and boot critical path. Any rule that needs a Tier C signal
   must declare `if_ai_unavailable` explicitly.
4. **The audit ledger is hash-chained and append-only.** Modifying any row fails
   checkpoint verification.
5. **Bundles are untrusted input.** Rule bundles, release archives and model
   bundles are all verified before execution. No shell, SQL, network, dynamic
   code or unbounded regex.

## Layout

| Path | Contents |
|---|---|
| `contracts/` | Protobuf `security/v1`, OpenAPI `v1`, JSON Schema. No language owns the schema. See [`contracts/README.md`](contracts/README.md). |
| `cmd/securityctl` | Operator and installer CLI. |
| `cmd/security-migrate` | Expand/contract migration runner. |
| `internal/` | Core: adapters, durability, graph, rules, verification, audit, identity, API, console. Start at [`internal/README.md`](internal/README.md). |
| `migrations/` | Ordered SQL. `expand` steps ship separately from `contract` steps. See [`migrations/README.md`](migrations/README.md). |
| `rules/builtin/` | The built-in bundle, shipped in the release archive. |
| `docs/` | Build status and threat model. Index: [`docs/README.md`](docs/README.md). |
| `docs/roadmap/` | The full plan. Start at [`docs/roadmap/README.md`](docs/roadmap/README.md). |
| `deploy/` | Windows Service, systemd, and the AI runtime. See [`deploy/README.md`](deploy/README.md). |
| `poc/active-monitoring/` | **A second Go module.** See below. Not part of this build. |

## Two trees, one repository

`poc/active-monitoring/` declares its own module path,
`github.com/Sergey-Bar/ai-captain-poc`, with its own `go.mod`, `go.sum` and
`scripts/verify.ps1`. It imports nothing from the module above and the module above
imports nothing from it.

This matters more than it looks. `go build ./...`, `go vet ./...`, `golangci-lint`,
`sonar-project.properties` and `scripts/verify.ps1` at the root **cannot** reach a
line of it: `./...` does not cross a module boundary. So a change under `poc/` is
not covered by this repository's gate and a change above is not covered by the POC's,
and each of those facts is silent unless somebody says it out loud.

The POC is a reference implementation with its own gate, kept for the reader who
wants to see a Milestone client that was written from Milestone's published
documents rather than from the parent platform's assumptions. It reached three of
the same conclusions about the parent platform's Milestone code independently —
including that `/api/v1` is not a Milestone prefix — which is recorded in
[`poc/active-monitoring/contracts/xprotect/README.md`](poc/active-monitoring/contracts/xprotect/README.md)
and in [`docs/vendor/milestone/route-audit.md`](docs/vendor/milestone/route-audit.md).

Run its gate separately:

```powershell
cd poc/active-monitoring; .\scripts\verify.ps1
```

Nothing merges between them. The POC does not import the platform's contracts, and
the platform does not read the POC's.

## Build

The toolchain is installed but **not on `PATH`** in a default Windows shell.
`go`, `protoc`, `golangci-lint` and `make` are all missing from it.

```powershell
$env:Path += ";C:\Program Files\Go\bin;$env:USERPROFILE\go\bin"
& "C:\Program Files\Go\bin\go.exe" build ./...   # must exit 0
```

```sh
make bootstrap     # fetch tools
make generate      # contracts -> generated code + compatibility check
make build
make lint
```

## Verify

```powershell
.\scripts\verify.ps1    # the gate: contract, vet, gofmt, test -race, migrations, rules
```

`make verify` is the same list of checks, but **`make` is not installed on this
machine** — not on `PATH`, not in Git Bash, not in WSL — so the PowerShell script
is the one that runs. It resolves `go` and `gofmt` itself, and it warns you when
`SP_TEST_DATABASE_URL` is unset, because the database-backed tests **skip** rather
than fail without it, and a green run that tested nothing is worse than a red one.

Windows uses `make` through Git Bash, or the equivalent `go` invocations in
`Makefile` directly from PowerShell. The database-backed tests **skip** rather
than fail when `SP_TEST_DATABASE_URL` is unset, so a green run can be a run that
tested nothing — `AGENTS.md` has the setup.

## Status

Milestone 1 is in progress. `docs/status.md` is the source of truth for what is
built and what is not — not this file.

## Plan

[`ROADMAP.md`](ROADMAP.md) is the one-page entry point. The full set of
documents lives in `docs/roadmap/`: the M1–M8 calendar with dated exit gates, and
the track documents behind it — product, UX, QA, metrics, adapters, AI, release,
security, compliance, GTM, pricing, launch, risk, hiring and finance. It is
organised so that a non-engineer can navigate it, and it is readable by one.

The master roadmap's final section lists the fourteen code defects found while
writing the plan, two of which — a bundle check that verifies a digest rather
than a signature, and an adapter event-type table keyed on spellings
normalisation never emits — are worth fixing before anything ships.

## Evidence rule

Market claims, benchmark numbers and vendor comparisons in the source material
were never verified. Until they are, they are marked as unverified with a
source and a date, and are not asserted in the product, the docs or the code.
