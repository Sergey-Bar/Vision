# Status

Milestone 1, as of the Track D rule-engine commit. This document is the source
of truth for what is built. The README is not.

Green means: written, building, and covered by a test that would fail if it broke.

## Track A — Contracts and substrate — complete

| # | Item | State |
|---|---|---|
| 1 | Repo layout, Go module, `make`, CI workflow, `.golangci.yml`, `.gitattributes` | green |
| 1 | Protobuf `security/v1` — 13 files, generated Go in `internal/contracts/securityv1` | green |
| 1 | `tools/contractcheck` — breaking-change gate with a committed baseline | green |
| 1 | OpenAPI `v1` — auth, incidents, evidence, disposition, audit, rules, health, ops | green, not served yet |
| 1 | JSON Schema — rule bundle, site config, release manifest, 3 AI observation profiles | green, enforced for rule bundles; the rest are published contracts not yet enforced |
| 2 | `security-migrate` — forward-only runner, advisory lock, per-step transaction, checksum drift detection, min/max schema window | green |
| 3 | M1 schema — 9 expand migrations, RLS, tenant context, monthly partitioning of `inbox_events` and `canonical_events` | green |
| 4 | `internal/audit` — writer, validation, per-tenant chain isolation | green |
| 4 | `internal/checkpoint` — `CheckpointSink`, signed offline file sealer, offline gap detection | green |
| 5 | `securityctl` command tree, `preflight` and `doctor` live | green |

## Track B — Adapter and fixtures — complete except real captures

Items 8 and 9 are green: 12 scenarios covering every behaviour the plan names,
byte-stable regeneration, a manifest whose hash drift is detected, and a
replay transport that is the same parsing path rather than a second one.

Item 6 is **not** done and cannot be on this machine: no Milestone server is
reachable, so nothing was captured. The fixtures are synthetic and say so. A
test fails if a scenario claims to be a capture without a capture time. Item 7's
endpoint surface is declared `Unverified` in `internal/milestone/routes.go` and
`securityctl adapter probe` does not exist yet.

## Track C — Durability — complete

`internal/inbox`, `internal/normalize`, `internal/outbox`, `internal/jobs`. The
lease-recovery bug the tests found — an expired lease was never reclaimable,
because the claim filtered on status and a dead worker's row is `processing` —
meant the whole recovery mechanism did not work. It does now, with a test.

## Track D — Graph, rules, verification, incidents, evidence — partial

| Item | State |
|---|---|
| 16 Rule engine: YAML → JSON Schema → semantic validation → AST → compiled rule | green |
| 17 Verification states, dedupe, corroboration, contradiction, drift exclusion | green |
| 18 Deterministic conflict precedence | green |
| 19 `rules lint` | green. `explain`, `simulate` and the activation gate are not built |
| 14 Security Graph service, traversal, snapshot export | not started |
| 15 Time subsystem: registry, drift measurement, skew policy | the policy and normalization are green in `internal/clock` and `internal/normalize`; the registry and measurer are not |
| 20 Incident lifecycle, evidence orchestration, decision replay | not started |
| Built-in bundle | green, 7 rules, lints clean in CI |

## Tracks E, F, G — not started

No identity, no sessions, no RBAC, no API server, no console. No AI runtime, no
model bundle format, no evaluation harness. No release builder, no installers, no
`verify-release`, no observability pipeline, and no threat model.

## Verified against a real database

`go test -p 1 -race ./...` passes against Postgres 16. That covers 25
database-backed cases in `internal/durability` plus the `internal/schemalaws`
and `internal/audit` suites: hash chaining and tamper localisation, append-only
ledger and observations, RLS on every large table, cross-tenant write refusal,
least-privilege grants, evidence path traversal, dedupe uniqueness, monthly
partitioning, concurrent intake, cross-partition dedupe, lease expiry and
recovery, atomicity of the normalize transaction, drift correction, and
terminal failure with a reason.

```sh
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
export SP_TEST_DATABASE_PASSWORD=postgres
make migrate-up && make test-race
```

Without `SP_TEST_DATABASE_URL` the database-backed tests skip rather than fail,
and say so.

## Deviations from the plan, and why

1. **Dedupe key moved off the partitioned tables.** The plan specifies
   `UNIQUE (tenant_id, integration_id, source_event_id)` on `inbox_events`, and
   also monthly range partitioning of that table. Postgres requires the
   partition key in every unique index on a partitioned table, so both cannot
   hold: the constraint would only have deduplicated within a month. The
   uniqueness now lives in `inbox_event_keys` and `canonical_event_keys`,
   written in the same transaction as the row. Exactly-once effect is preserved.
2. **The outbox idempotency key is scoped to the tenant** (migration 0009). It
   was globally unique, which is wrong for a multi-tenant product: two tenants
   naturally generate the same key for their own side effect, and the second
   one is silently swallowed as a duplicate.
3. **The system worker bypasses the tenant RLS policy, explicitly.** Background
   workers process rows across tenants. Rather than exempting them by role they
   set `app.system_worker`, and the predicate that honours it is bounded by
   grants.
4. **`security_core` does not have `BYPASSRLS`.** Only the migrator does. RLS is
   only defence in depth if the service layer is subject to it.
5. **NUL bytes are not constrained in the `storage_ref` check.** Postgres rejects
   NUL in `text` at the type level, so the constraint would be untestable. The
   null-byte negative test belongs to the evidence store, which is not written.
6. **`audit_ledger.prev_hash` and `hash` are computed by a trigger**, not
   supplied by the writer, over a length-prefixed canonical encoding.
7. **The rule schema's `eventType` vocabulary was corrected** to the adapter's
   normalized names (`alarm.created`, `device.camera_offline`, …) rather than the
   coarse vendor-ish set it originally carried. The contract follows what the
   adapter emits; a rule bundle must be portable between sites.

## What cannot be completed here, and why

Three Definition of Done items require things this environment does not have:

- **DoD 1** — integration mode and replay mode producing the same canonical
  event, needs a real XProtect installation to compare against.
- **DoD 6** — every SLO demonstrably met on a dev installation under load. The
  metrics and harness can be built; the demonstration cannot be faked.
- **DoD 10** — a production VLM profile passing its evaluation gates. There is no
  model, no GPU and no signed model bundle. The harness and the schemas can be
  built; the result cannot.

## Local toolchain notes

- Go 1.27 via winget.
- `protoc` via winget (`Google.Protobuf`); `protoc-gen-go` via `go install`.
  Both paths are in `Makefile` as overridable variables.
- `make test-race` needs cgo and a C compiler. On this machine that is MinGW-w64
  (`BrechtSanders.WinLibs.POSIX.UCRT` via winget). With `CGO_ENABLED=1` and gcc on
  `PATH`, the full suite including the race detector passes.


