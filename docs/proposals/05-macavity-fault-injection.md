# Proposal 05 — Deterministic fault injection for the durability DoD

**Status:** proposal. Nothing is implemented. A human decides.

## The gap

`AGENTS.md`:

> Any durability change needs a `kill -9` fault-injection test at the stage it
> touches. It is a DoD item, not a nice-to-have.

`internal/durability` implements the current answer: dedupe across partitions,
lease reclaim, normalize atomicity, outbox poison isolation. A process-level
kill proves that the process dies mid-operation and the database does not care,
because the database is the thing that survived.

What it cannot prove is the other half: **what happens when the database fails,
not the process.** A crash between a `COMMIT` and the socket acknowledgement, a
backend that dies at exactly the point the audit trigger computes its chain
hash, a commit that returns an error after it may already have landed. Postgres
does not lie about this — it crashes the cluster, disconnects every session, and
runs recovery — and that is precisely the scenario the invariant exists for.

## The candidate

**`macavity`** — a PostgreSQL extension in the PGXN network, v0.1.0.
**Licence not verified; PostgreSQL-ecosystem extensions are typically
PostgreSQL Licence, which must be confirmed before use.**

It exists to break Postgres on cue, and it does so in the shape this repository
needs:

- **Session-local fault state.** One fault armed per session, by one session.
  Faults do not cross sessions.
- **Named execution points** — a session arms a fault at a point, then runs
  normally until that point is reached and the fault fires.
- **Three actions:** `error` (raise, normal transaction abort semantics),
  `crash` (`SIGKILL` to the calling backend's own PID), `delay`.
- **`occurrence`** — fire on the Nth hit, not the first. "Crash on the third
  commit" is expressible; "crash at some point" is not a test.
- **Counters updated before the fault action runs**, so the hit that kills the
  backend is still recorded.
- **Ships a `pg_regress` suite**, with the crash case correctly split into a
  shell script because `pg_regress` cannot survive a cluster-wide restart
  mid-run.

That last point is the mark of a tool built by someone who hit the same
problem: the tests are structured around the fact that a backend kill takes the
cluster with it.

## What it buys us, concretely

- `crash` at `before_commit` on a write to `audit_ledger` — does the chain
  still verify, and does checkpoint sealing see a gap or a clean sequence?
- `crash` on the backend holding an inbox lease — does the reclaim path
  recover, and does the stale worker get refused by the `lease_owner` match in
  `completeInbox`?
- `error` on a statement inside the single transaction that spans the inbox
  transition and the canonical event — the invariant 1 case, at the exact stage.
- `delay` to stretch a window and reproduce a timing-dependent race
  deterministically, which is what `internal/durability`'s lease-reclaim test
  currently has to provoke by other means.

## What we would not do

- **Not on a shared or long-lived database.** The extension's own documentation
  carries a destructive warning: a `crash` terminates the backend, the postmaster
  disconnects every other session and runs crash recovery. This belongs on a
  disposable container, started and destroyed per run.
- **Not inside `verify.ps1` as it stands.** `verify.ps1` currently assumes a
  database the developer keeps alive, and `-p 1` exists because the
  database-backed suites share one schema. A gate that kills the cluster would
  break every other gate after it. It needs a separate script and its own
  ephemeral container, and it should say so loudly.
- **Not a replacement for the existing `kill -9` tests.** Those test the process.
  This tests the database. Both are DoD items.

## Alternatives considered

- **`alexei-led/pumba`** — **Apache-2.0**, 3,076 stars, 88 releases. Container
  level: `kill`, `netem` delay and loss, `stress-ng`. Good for the systemd unit
  and for network conditions, useless for a point inside a transaction.
- **`lesovsky/noisia`** — **BSD-3-Clause**, 763 stars. Destructive workload
  generator for Postgres: `terminate backends`, `failconns`, `slotbloat` (WAL
  grows until the instance PANICs), `waitxacts` (blocks until
  `max_connections` is exhausted). These are sustained pressure, not
  deterministic points. Complementary: noisia proves the system degrades, macavity
  proves it survives a specific event.
- **`pgguru/pg_kaboom`** — includes `rm -Rf $PGDATA` as a callable function.
  Noted here so nobody installs it by accident.

## Risks

- **Licence unverified.** Must be checked before anything runs.
- **PostgreSQL version.** An extension of this kind depends on server internals.
  This repository tests on Postgres 16; the extension's supported version range
  must be confirmed, and a new production version may need a new extension
  version before it can be used.
- **Windows.** The dev environment is Windows. The crash case needs a real
  Unix-style signal path; if the extension does not behave under the local
  Docker Postgres, this runs in CI or in a Linux container only, and the local
  `verify.ps1` must not depend on it.
- **Scope creep into a chaos suite.** The valuable version is four or five named
  points. A general chaos harness is a different project with a different owner.

## Confidence

High on the mechanism, read from the extension's own documentation and test
layout. **Unverified: the licence, the PostgreSQL version range, and Windows
behaviour.** All three must be settled before the first run.

## Decision required

Does durability testing against a database fault belong in the DoD at all, and
if so does it run locally, in CI, or both? Note that this repository currently
has no pipeline, so "CI" is not currently a place a test can run.
