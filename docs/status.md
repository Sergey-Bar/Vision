# Status

Milestone 1. This document is the source of truth for what is built. The README
is not, and neither is the commit history.

Green means: written, building, and covered by a test that would fail if it broke.
Everything in the table below is that. Where a thing is not done it says why.

**Verification:** `go test -p 1 -race ./...` green across 24 packages against a
real Postgres 16, with the race detector on. `go vet` and `gofmt` clean.
`contractcheck` reports 20 messages and 22 enums compatible with the baseline.
The Python AI runtime's 21 contract tests pass under `py -m unittest`.

## What is built

| Track | Items | State |
|---|---|---|
| A — Contracts and substrate | 1–5 | green |
| B — Adapter and fixtures | 8, 9 | green |
| C — Durability | 10–13 | green |
| D — Graph, rules, verification, incidents | 14–20 | green |
| E — Identity, API, console | 21–24 | green |
| F — AI observation plane | 25–29 | green except the inference engine (see below) |
| G — Threat model, release, lifecycle, observability, DR | 30–35 | green |

No command in the tree refuses as unimplemented. The one remaining guard in the
code is the audit endpoint's answer when no audit writer is configured, which is a
defence rather than a stub.

### The operator CLI

```
securityctl incident list | show <id> | explain <id> | replay <id> | disposition <id>
securityctl audit verify | seal | checkpoints | verify-seals
securityctl identity bootstrap-admin | enroll-mfa | list | revoke-sessions
securityctl rules lint | explain | simulate | store | show | activate | rollback
securityctl install | upgrade | rollback | backup | restore
securityctl build-release | verify-release | release-key
securityctl preflight | doctor | migrate up | status
```

Every one of these was exercised by hand against a running database, not only by
unit test. A full release cycle was built, signed, verified against a trusted key,
installed, upgraded to a second version, and rolled back — with the pre-upgrade
backup verified offline and the wrong signing key refused at every step.

### The lifecycle commands

- `install` verifies the signature, the manifest, every file hash, the schema
  window and the expand/contract discipline before extracting a byte, and
  extraction re-checks every archive entry independently.
- `upgrade` is backup, then stage, then swap, and the previous program directory
  is kept rather than deleted, so a rollback does not first have to rebuild the
  version it is rolling back to.
- `rollback` reads the *previous build's own* declared schema window and refuses
  when the database has moved past it, because a half-downgraded site is the state
  this product must never be in.
- `backup` is self-consistent or it is recorded as partial, with the reason. A
  database dump without the evidence directory is an archive whose incidents cite
  bytes that are not in it.

### The AI runtime

`deploy/ai-runtime/runtime.py` is a separate process with no third-party
dependencies, and it has no model. That is the honest shape of the work: the
bundle is signed and verified, the request and response contract is enforced, the
output is validated against the profile's schema, and every failure path returns
a *reason*. With no model present the plane starts, reports itself unavailable,
and every observation is recorded as degraded.

There is no placeholder that returns a plausible answer, and there deliberately
is not: a stub returning `{"restrictedZoneBreach": false}` would produce a
corroborated incident on a fabricated negative, which is the single worst failure
this product can have. The tests assert the degraded path instead, and
`insufficientEvidence` is true on every degraded response so a consumer that
ignores the degradation field still fails closed.

**What a model adds:** a `run_inference` body. Everything around it — verification,
attribution, validation, degradation, the reason vocabulary — is written and
tested.

## What is not done, and why

**The inference engine.** There is no model, no weights, no GPU and no signed
bundle. The runtime refuses rather than pretending, and `docs/status.md` says so
because a pilot's first question is whether the AI plane is doing anything.

**The Milestone endpoint surface is unverified.** Every route in
`internal/milestone/routes.go` is marked `Unverified` and the fixtures are
synthetic. No Milestone server has been reachable from this codebase. This is the
single most important open item: everything else is proven against a fixture, and
the one component that has not touched the real thing is the one that talks to the
customer.

**Automated database restore.** `restore --verify` checks the backup's hashes and
reports what a restore would do, then refuses to perform it, because a restore that
guesses at a connection string, a role and a password fails silently on a site
nobody is watching. Restoring the dump is a deliberate act with the operator's own
tooling.

**Attestation trust chain for WebAuthn.** Registration, challenge binding, COSE
key validation and ES256/EdDSA signature verification are implemented and
tested. There is no attestation certificate chain check: the replay defence here
is the authenticator's sign counter, not the certificate, and the enrolment
command says so rather than leaving an operator to assume it.

## Three Definition of Done items that cannot be signed here

1. **Integration and replay parity.** Needs a real XProtect installation.
2. **Every SLO demonstrably met under load.** The metrics and the evaluation
   exist; the demonstration needs a dev installation and a load generator.
3. **A production VLM profile passing its gates.** The harness is written and
   decides; there is no model to run through it.

## Deviations from the plan

1. **Dedupe key moved off the partitioned tables.** A unique index on a partitioned
   table must include the partition key, so the planned constraint would only have
   deduplicated within a month.
2. **The outbox idempotency key is scoped to the tenant** (migration 0009).
3. **Background workers reach other tenants through an explicit
   `app.system_worker` setting**, bounded by grants, not a role exemption.
4. **`security_core` has no `BYPASSRLS`.** Only the migrator does.
5. **NUL bytes are not in the `storage_ref` CHECK** — Postgres rejects NUL in
   `text` at the type level, so the constraint would be untestable.
6. **The audit hash chain is trigger-computed** over a length-prefixed encoding.
7. **The rule schema's `eventType` vocabulary** follows the adapter's normalized
   names.
8. **`canonical_events` gained a primary key** (migration 0012). It had none.
9. **Hebrew and RTL were dropped**; `sites.default_locale` is removed by migration
   0010, not by editing 0001.
10. **The console is English and LTR**, with `dir="ltr"` and `lang="en"` asserted.
11. **The AI runtime's input hash sorts by (id, hash), not id alone.** Evidence
    ids are unique in practice, but a sort that is not a total order makes the hash
    depend on the order the caller passed.

## Reproducing the verification

```sh
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
export SP_TEST_DATABASE_PASSWORD=postgres
go run ./cmd/security-migrate up
go test -p 1 -race ./...

cd deploy/ai-runtime && py -m unittest discover
```

Without `SP_TEST_DATABASE_URL` the database-backed tests skip rather than fail,
and say so. `-p 1` is required: the database-backed suites share one database and
rebuild the schema, so package test binaries must not run concurrently.

## Local toolchain

- Go 1.27, `protoc` and MinGW-w64 (for `-race` on Windows), all via winget.
- Python 3.14 via the `py` launcher. The runtime targets 3.11+ syntax and uses
  the standard library only, which CI asserts.
