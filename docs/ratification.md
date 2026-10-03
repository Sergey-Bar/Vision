# Ratification queue

Each entry is a **finished artefact**: the decision, the diff, the evidence, and
the alternative. No entry requires a build cycle. **None is merged by an agent.**

`AGENTS.md` reserves several things to a human — an invariant, a published
contract, a migration, auth, crypto, the rule language, a capability, a date. This
file is where the work that touched one of those is recorded before it lands, so
signing takes minutes rather than a re-derivation.

**The agent will not, at any point:** merge the `contract/` branch, sign a gate,
clear a vendor route's `Unverified` flag, promote a VLM profile to production,
contact anyone, or commit to a date.

---

## R13 — Per-package database isolation in the test harness

**Status: awaiting signature. Blocking for: P0 item 6, and every later phase that
adds a database-backed test.**

### What was asked, and what was built

The plan asked for `sp_test_<pkg>` **schema** isolation: each database-backed
package gets its own schema, so `go test ./...` runs concurrently and `-p 1` stops
being load-bearing.

**What was built is per-package *database* isolation**, which is a different change
to a different blast radius. It is recorded here as such rather than presented as
the thing that was asked for.

### Why it deviated

Migration `0008_identity_and_rls.expand.sql` hardcodes `public` twice:

- `WHERE n.nspname = 'public'` — the loop that finds the tables to put a policy on
- `format('%I.%I', 'public', t.relname)::regclass` — which reads the column list

No other migration qualifies a table name anywhere, so 0008 is the only place
`public` was ever written down — which is exactly why every other migration is
schema-agnostic.

Against a per-package **schema**, 0008's loop finds **no tables at all** and
applies **no row-level security policy to anything**. The database's only tenant
isolation is those policies. It appeared to work because the shared `public` still
held the previous run's tables, and it failed at the first partition migration 0015
had created:

```
apply 0008: ERROR: relation "inbox_events_2027_01" does not exist
```

A migration that silently applied no RLS on a database whose only tenant isolation
came from those policies is a far worse outcome than a slow test run, and it was
found by making the schemas honest about themselves rather than by reading 0008.

Making the harness use `current_schema()` instead would require a forward-only
migration 0022 that drops and recreates every policy — and until 0022 is applied,
0008 still applies none in a fresh schema. **A separate database needs no migration
at all**, because each database has its own `public`.

### The diff

| File | Change |
|---|---|
| `internal/testutil/testutil.go` | rewritten: per-package database derived from the caller's own directory; `Pool` creates it empty and repoints `SP_TEST_DATABASE_URL` at it; `Reset` drops the package's `public` in place; the five product roles are created once under a cluster-wide advisory lock on the maintenance database |
| `internal/testutil/isolation_test.go` | new: the isolation asserted directly, including that migration 0008 still finds its tables |
| `internal/dbx/dbx.go` | `SearchPath` field; `DSN()` now includes the user; `Open` refuses an empty user |
| `internal/migrate/migrate.go` | a bounded retry around step application for cluster-catalog contention |
| `AGENTS.md` | the `-p 1` rule replaced with the two consequences this has |

### The evidence

- `go test -p 8 ./...` is green against real Postgres 16. The `-p 1` rule is gone.
- `rls-canary` still passes, and `internal/testutil` asserts that a full migration
  produces 85 `tenant_isolation` policies — one per tenant-scoped table — rather
  than zero.
- **Isolation is what makes it safe, and disabling it reproduces the old symptom.**
  `sorry, too many clients already`, and a `DROP SCHEMA` landing inside another
  suite's test.
- Removing `-p 1` exposed four latent defects the serialised run could not see:
  1. **A cluster-wide `CREATE ROLE` race.** 0008's `ALTER ROLE security_migrate
     BYPASSRLS` is unconditional, and a PostgreSQL role is cluster-wide while a
     bigint `pg_advisory_lock` is **database**-scoped. Eight concurrent migrations
     produce `tuple concurrently updated` out of `pg_authid`.
  2. **Connection exhaustion.** Eight packages × a pool plus a second pool for a
     role exceeds the default `max_connections` of 100.
  3. **A migration-ledger leak** in `internal/migrate`, hidden by the shared schema.
  4. **A stack-depth bug in the harness's own `runtime.Caller(2)`**, which made
     `Reset` resolve the package from `testutil.go` rather than from the caller and
     drop the wrong database.

### What is actually being signed

Three things, and the third is the one to look at hardest.

1. **A test-infrastructure change touching every database suite.** Not a product
   decision, but it touches the tests that guard the invariants.
2. **A change to how migration 0008's role DDL behaves under concurrency**, via a
   bounded retry keyed on SQLSTATE `XX000` *and* the message `tuple concurrently
   updated`. A genuine internal error in a step is not retried, and a retried step
   rolls back before each attempt, so there is no partial application.
3. **The harness now issues cluster-scoped DDL.** It connects to the `postgres`
   maintenance database on whatever host `SP_TEST_DATABASE_URL` names and runs
   `DROP DATABASE`, `CREATE DATABASE` and `CREATE ROLE`. It previously only reset
   schemas *inside* the configured database. **A developer who exports a production
   DSN into that variable mutates that cluster's role catalogue.** The `sp_test_`
   prefix bounds the `DROP`; the `CREATE ROLE` is not bounded. A host or
   database-name assertion in `testutil.Pool` would close it, and is the obvious
   next change if you want one.

### The alternative, and what it costs

**Per-package schema plus forward-only migration 0022.** 0022 would drop and
recreate every RLS policy against `current_schema()`. Until it is applied, 0008
applies no policy in a fresh schema — so the two must land together, and a
half-applied pair is the exact failure this deviated to avoid. It also makes every
future migration's correctness depend on `search_path` being what it assumes, which
is a property no test in the tree asserts.

### Evidence this is safe to sign

`scripts/run-verify.ps1` — **all 21 gates pass**, `-race`, 57 packages, against a
real database. The record for the run is named in `docs/status.md`. The four
defects above were all found *by* this change, which is the argument for it: the
serialised harness had been hiding them.