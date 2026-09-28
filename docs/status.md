# Status

Milestone 1, as of the Track A hand-off. This document is the source of truth for
what is built. The README is not.

Green means: written, building, and covered by a test that would fail if it broke.

## Track A — Contracts and substrate

| # | Item | State |
|---|---|---|
| 1 | Repo layout, Go module, `make`, CI workflow, `.golangci.yml` | green |
| 1 | Protobuf `security/v1` — 13 files, generated Go in `internal/contracts/securityv1` | green |
| 1 | `tools/contractcheck` — breaking-change gate with a committed baseline | green |
| 1 | OpenAPI `v1` — auth, incidents, evidence, disposition, audit, rules, health, ops | green, not served yet |
| 1 | JSON Schema — rule bundle, site config, release manifest, 3 AI observation profiles | green, not enforced yet |
| 2 | `security-migrate` — forward-only runner, advisory lock, per-step transaction, checksum drift detection, min/max schema window | green |
| 3 | M1 schema — 8 expand migrations, RLS, tenant context, monthly partitioning of `inbox_events` and `canonical_events` | green |
| 4 | `internal/audit` — writer, validation, per-tenant chain isolation | green |
| 4 | `internal/checkpoint` — `CheckpointSink`, signed offline file sealer, offline gap detection | green |
| 5 | `securityctl` command tree, `preflight` and `doctor` live | green |
| 5 | Lifecycle, rules, incident, audit and identity subcommands | skeleton — registered, refuse with a named reason |

## Verified against a real database

The schema laws are asserted in `internal/schemalaws`, not merely written down:
hash chaining and tamper localisation, append-only ledger and observations, RLS
tenancy on every large table, cross-tenant write refusal, least-privilege grants
for the adapter and AI roles, evidence path-traversal rejection, dedupe
uniqueness, and monthly partitioning. `internal/audit` adds concurrent-append
chain integrity and the sealer refusing to seal a broken chain.

Run against Postgres 16:

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

1. **Dedupe key moved off the partitioned table.** The plan specifies
   `UNIQUE (tenant_id, integration_id, source_event_id)` on `inbox_events`, and
   also specifies monthly range partitioning of that table. Postgres requires the
   partition key to be part of every unique index on a partitioned table, so both
   cannot hold: the constraint would only have deduplicated within a month. The
   uniqueness now lives in `inbox_event_keys` (and `canonical_event_keys`),
   written in the same transaction as the row it protects. Exactly-once effect is
   preserved; the guarantee just lives in a table that can carry it.

2. **The system worker bypasses the tenant RLS policy, explicitly.** Background
   workers legitimately process rows across tenants. Rather than exempt them by
   role, they set `app.system_worker`, and the predicate that honours it is
   bounded by grants: a worker account holds no grants on identity or the rule
   store, so cross-tenant visibility never becomes cross-tenant authority.

3. **`security_core` does not have `BYPASSRLS`.** Only the migrator does. RLS is
   described as defence in depth under service-layer authorisation, which is only
   true if the service layer is subject to it.

4. **NUL bytes are not constrained in the `storage_ref` check.** Postgres rejects
   NUL in `text` at the type level, so the constraint would be untestable. The
   null-byte negative test belongs to the evidence store, which the plan already
   requires and which is not written yet.

5. **`audit_ledger.prev_hash` and `audit_ledger.hash` are computed by a trigger,
   not supplied by the writer.** If the application supplied them, the chain would
   only mean "whatever the last writer believed". The Go writer validates entries
   and records the sequence; the database owns the chain.

## Not built yet

Everything in Tracks B through G. The specific gaps a reader should know about
before trusting this:

- No adapter, no XProtect client, no fixtures, no replay harness. The canonical
  event path is defined in contracts and schema but nothing produces one.
- No inbox writer, normalizer, outbox relay or job runner. The tables and their
  constraints exist; the durability logic that Track C will implement does not.
- No rule engine. `securityctl rules lint` refuses. The bundle schema exists so
  the engine has a contract to compile against, and `rules/builtin/` is empty.
- No graph traversal, no verification state machine, no incident lifecycle.
- No identity: `IdentityProvider`, `LocalIdentityProvider`, Argon2id, MFA, sessions
  and RBAC exist as tables and constraints only.
- No API server, no console, no templates.
- No AI runtime, no model bundle format, no evaluation harness.
- No release builder, no installers, no `verify-release`. The manifest schema
  exists; nothing produces or checks an archive.
- No observability pipeline or metrics registry. The metric names are specified in
  the plan and not yet exported.
- No threat model. Track G item 30, blocking for the pilot.

## Local toolchain notes

- Go 1.27 via winget.
- `protoc` via winget (`Google.Protobuf`); `protoc-gen-go` via `go install`.
  Both paths are in `Makefile` as overridable variables.
- `make test-race` needs cgo and a C compiler. On this machine that is MinGW-w64
  (`BrechtSanders.WinLibs.POSIX.UCRT` via winget). With `CGO_ENABLED=1` and gcc on
  `PATH`, the full suite including the race detector passes.

## Not started

The working tree is not a git repository. `.gitignore` and the CI workflow are in
place but inert until someone runs `git init` and pushes. That is a decision for
the team, not a step to take unasked.

