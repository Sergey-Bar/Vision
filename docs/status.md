# Status

Milestone 1. This document is the source of truth for what is built. The README
is not, and neither is the commit history.

Green means: written, building, and covered by a test that would fail if it broke.
Everything in the table below is that. Where a thing is not done it says why.

**Verification:** `go test -p 1 -race ./...` green across 21 packages, against a
real Postgres 16, with the race detector on. `gofmt` and `go vet` clean.
`contractcheck` reports 20 messages and 22 enums compatible with the baseline.

## What is built

| Track | Items | State |
|---|---|---|
| A — Contracts and substrate | 1–5 | green |
| B — Adapter and fixtures | 8, 9 | green |
| C — Durability | 10–13 | green |
| D — Graph, rules, verification, incidents | 14–20 | green |
| E — Identity, API, console | 21–24 | green |
| F — AI observation plane | 25–29 | green except the Python runtime (see below) |
| G — Threat model, release, observability, DR | 30, 31, 32, 34, 35 | green |

### The operator CLI

`securityctl` reaches every capability above. Verified by hand against a running
database, not only by unit test:

```
securityctl identity bootstrap-admin --username admin   # refuses a second one
securityctl identity list                              # roles, sites, MFA, state
securityctl identity revoke-sessions --user <id>
securityctl incident list | show <id> | explain <id> | replay <id> | disposition <id>
securityctl audit verify | seal | checkpoints | verify-seals
securityctl rules lint | explain | simulate | store | show | activate | rollback
securityctl build-release | verify-release | release-key
securityctl preflight | doctor | migrate up | status
```

`audit seal` produced a signed seal, and `audit verify-seals` — the air-gapped
path, no database — accepted it offline and refused the same seal against a
different key.

### A — Contracts and substrate

Thirteen protobuf files for `security/v1` with generated Go, an OpenAPI v1
surface, JSON Schema for the rule bundle, site config, release manifest and three
AI observation profiles, and a compatibility gate that fails the build on a
removed field, a reused tag, a changed type or cardinality, an enum value removed
or renamed, or a field moved out of a `oneof`.

Nine forward-only expand migrations, a migration runner that refuses a database
outside its declared schema window, a hash-chained audit ledger computed by a
database trigger, and a signed offline-file checkpoint sink.

### B — Adapter and fixtures

Twelve scenarios covering every behaviour the plan names, byte-stable
regeneration, a manifest whose hash drift is detected, a sanitizer that is the
single choke point between a live source and a committed file, and a replay
transport that is the same parsing path as a live server rather than a second
parser.

### C — Durability

Inbox with a scoped dedupe key taken before the work, leases with owners and
expiry, the normalizer that writes a canonical event and completes the inbox
transition in one transaction, an outbox relay, and a Postgres job queue. Event
ids are derived from the scoped key so a crash retry is a retry rather than a
reconciliation.

### D — Graph, rules, verification, incidents

A provenance graph with revocation that hides rather than deletes, a clock
subsystem that measures drift for a device and for a source system, an evidence
store with content addressing and symlink-aware path validation, an incident
lifecycle, and a replay path whose inputs contain no model client, no bundle
path and no clock.

The rule engine compiles YAML through the JSON Schema, a semantic validator, and
an AST, with no shell, SQL, network, dynamic code or regular expression anywhere
in the language. Activation is gated on a reviewed simulate diff digest computed
over the diff rather than the bundle.

### E — Identity, API, console

Argon2id with the parameters in the build rather than in the hash row, mandatory
MFA, sessions that store a hashed token and rotate on every use, exact capability
checks, site scoping that denies by default, a JSON API that does not
content-negotiate, and a server-rendered LTR console with no inline script and a
strict Content-Security-Policy that enforces it.

### F — AI observation plane

Observation persistence with full attribution, append-only storage, supersession
as a new row, an undeclared-field check against the profile's schema, and a
harness that scores a profile on false-positive rate, recall, latency,
disagreement and case count — with every failing gate named rather than a single
boolean.

### G — Threat model, release, observability, DR

`docs/threat-model.md`: seven trust boundaries, five attacker profiles, 24 abuse
cases, 35 controls each mapped to the test that fails when the control is
removed, and four accepted risks stated plainly.

A signed offline release builder and verifier, a metric registry using the plan's
exact names, SLO evaluation where every objective carries a remedy, and a recovery
drill that demonstrates the real finding: after a database loss the restored
ledger is provably behind the seal, because a backup is a snapshot of some
earlier moment.

## What is not built, and why

**The Python VLM runtime (Track F, items 25–27).** The Go side is complete:
profiles are declared, outputs are validated against their schema, observations
are persisted with attribution, shadow profiles are marked and cannot affect
authoritative state, and the evaluation harness has promotion gates. The runtime
itself is a Python service and is not written. It is the one substantial piece of
Track F outstanding.

**Windows Service and systemd units (Track G, item 33).** `deploy/` was created
and removed again. The service definition is genuinely small, and it is
deferred rather than absent — nothing else depends on it and it should be written
against a real service manager.

**`securityctl install`, `upgrade`, `rollback`, `backup` and `restore` remain
skeletons.** They register and refuse with a named reason. The release builder
and verifier they depend on now exist and work, and the schema window check that
stops a half-upgrade is implemented; what is missing is the installer that
extracts the verified archive, the transactional upgrade, and the backup command.
Those are real work, not a stub, and they are the next thing to write.

**WebAuthn enrolment is not implemented.** TOTP enrolment, verification and
the WebAuthn assertion's counter check are, and the schema carries both
methods. The registration ceremony is missing, so `enroll-mfa --method webauthn`
refuses rather than storing an unusable credential.

**The Milestone endpoint surface is still unverified.** Every route in
`internal/milestone/routes.go` is marked `Unverified`, and the fixtures are
synthetic. No Milestone server has been reachable from this codebase. This is the
single most important open item: every other part of the pipeline is proven
against a fixture, and the one component that has not touched the real thing is
the one that talks to the customer.

## Three Definition of Done items that cannot be signed here

1. **Integration and replay parity.** Needs a real XProtect installation to
   compare against.
2. **Every SLO demonstrably met under load.** The metrics and the evaluation
   exist; the demonstration needs a dev installation and a load generator.
3. **A production VLM profile passing its gates.** Needs a model, a GPU and a
   signed model bundle. The harness that would judge it is written.

## Deviations from the plan

1. **Dedupe key moved off the partitioned tables.** A unique index on a
   partitioned table must include the partition key, so the planned constraint
   would only have deduplicated within a month. The key lives in
   `inbox_event_keys` and `canonical_event_keys`, written in the same transaction.
2. **The outbox idempotency key is scoped to the tenant** (migration 0009). A
   globally unique key swallows the second tenant's side effect.
3. **Background workers reach other tenants through an explicit
   `app.system_worker` setting**, bounded by grants, rather than a role exemption.
4. **`security_core` has no `BYPASSRLS`.** Only the migrator does.
5. **NUL bytes are not in the `storage_ref` CHECK.** Postgres rejects NUL in
   `text` at the type level, so the constraint would be untestable; the test
   belongs to the evidence store, which has it.
6. **The audit hash chain is trigger-computed** over a length-prefixed encoding.
7. **The rule schema's `eventType` vocabulary** was corrected to the adapter's
   normalized names. The contract follows what the adapter emits.
8. **`canonical_events` gained a primary key** (migration 0012). It had none.
9. **Hebrew and RTL were dropped** by product decision; `sites.default_locale` is
   removed by migration 0010, not by editing 0001.
10. **The console is English and LTR**, with `dir="ltr"` and `lang="en"` asserted.

## Reproducing the verification

```sh
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
export SP_TEST_DATABASE_PASSWORD=postgres
go run ./cmd/security-migrate up
go test -p 1 -race ./...
```

Without `SP_TEST_DATABASE_URL` the database-backed tests skip rather than fail,
and say so.

`-p 1` is required: the database-backed suites share one database and rebuild the
schema, so package test binaries must not run concurrently.

## Local toolchain

- Go 1.27, `protoc`, and MinGW-w64 — all via winget. `make test-race` needs cgo
  and a C compiler; on this machine that is MinGW.
