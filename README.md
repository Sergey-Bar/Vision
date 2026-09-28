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
| `contracts/` | Protobuf `security/v1`, OpenAPI `v1`, JSON Schema. No language owns the schema. |
| `cmd/securityctl` | Operator and installer CLI. |
| `cmd/security-migrate` | Expand/contract migration runner. |
| `internal/` | Core: adapters, durability, graph, rules, verification, policy, audit, identity, API, console. |
| `migrations/` | Ordered SQL. `expand` steps ship separately from `contract` steps. |
| `rules/builtin/` | The built-in bundle, shipped in the release archive. |
| `docs/` | Threat model, roadmap, operational runbook. |
| `docs/roadmap/` | The plan: strategy, the M1–M8 calendar, and every R&D, GTM, trust and company track. Start at `docs/roadmap/README.md`. |
| `deploy/` | Windows Service, systemd, optional OCI profile, and the AI runtime. |

## Build

```sh
make bootstrap     # fetch tools
make generate      # contracts -> generated code + compatibility check
make build
make test
make lint
```

Windows uses `make` through Git Bash, or the equivalent `go` invocations in
`Makefile` directly from PowerShell.

## Status

Milestone 1 is in progress. See `docs/status.md` for what is built and what is
not — the doc is the source of truth, not this file.

## Plan

`docs/roadmap/` holds the roadmap from today to product: the M1–M8 calendar with
dated exit gates, and the track documents behind it — product, UX, QA, metrics,
adapters, AI, security, compliance, GTM, pricing, launch, risk, hiring, finance.
Its index is `docs/roadmap/README.md`, and it is deliberately readable by someone
who is not an engineer.

## Evidence rule

Market claims, benchmark numbers and vendor comparisons in the source material
were never verified. Until they are, they are marked as unverified with a
source and a date, and are not asserted in the product, the docs or the code.
