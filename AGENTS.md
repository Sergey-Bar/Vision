# Working agreements

## Before writing code

- The plan of record lives in the milestone plan. Scope is locked; a new
  capability has to be justified as a platform primitive, not added ad hoc.
- Contracts are the source of truth. Change `contracts/` first, regenerate, and
  let `contractcheck` tell you whether you broke the wire. Never hand-edit
  `internal/contracts/`.
- Rule bundles, release archives and model bundles are untrusted input. If you
  are adding a code path that executes anything derived from them, the threat
  model owns the answer, not the convenience of the call site.

## Invariants that tests must protect

These are the law. A change that weakens one needs an ADR and a test, in that
order.

1. Zero accepted event loss. Anything reaching `inbox_events` reaches a terminal
   state unless explicitly dead-lettered. `kill -9` at any stage: no loss, no
   duplicate side effect.
2. Decisions replay from stored facts and the stored `AIObservation`. A
   `replayedFromStoredFacts` response that re-inferred anything is a bug.
3. A missing AI observation is never a negative observation. Every rule that
   consumes a Tier C signal declares `if_ai_unavailable`.
4. The audit ledger is append-only and hash-chained. Editing any row must fail
   checkpoint verification.
5. `tenant_id` is `NOT NULL` everywhere and is never read from a request body.
   `site_id` is `NOT NULL` only on physically site-bound records.

## Conventions

- Time is UTC everywhere in storage. `observed_at` is what the source claimed,
  `received_at` is our ingest, `normalized_at` is drift-corrected and is the
  only field a time-window predicate may read.
- Go errors wrap with context; log lines carry `tenant_id`, `site_id` and
  `request_id`. No secrets, tokens, credentials or raw biometrics in logs. The
  audit ledger is separate from operational logs and is the authoritative one.
- SQL lives in `migrations/`, never inline in Go. Expand and contract ship
  separately; a contract step may never be bundled with the expand step that
  makes it safe.
- Migrations are forward-only. Never edit an applied migration; add a new one.

## Testing

- Unit tests for rule compilation, semantic validation, time-window and skew
  logic, conflict precedence, the verification state machine, clock drift
  normalization, tenant scoping, RLS, and job lease/claim races.
- Golden tests are deterministic-only: fixture to canonical to verification to
  incident must be byte-stable. Never golden-test VLM prose.
- Any durability change needs a `kill -9` fault-injection test at the stage it
  touches. It is a DoD item, not a nice-to-have.
- Path traversal (`../`, absolute paths, null bytes, symlink escapes) needs an
  explicit negative test on every evidence path, on every change.

## Hebrew and RTL

**Not a requirement.** The Hebrew-first RTL operator surface in the original plan
has been dropped by product decision. Console templates are LTR, `lang="en"`,
and no template may carry per-element direction overrides, because there is no
second direction to override to. The operator surface is English.

Two consequences that are easy to get wrong:

- Time rendering is unaffected. A site's timezone is still the site's timezone and
  never the host's, and every rendered time carries an explicit offset. Dropping
  RTL does not mean dropping the site's clock.
- `sites.default_locale` stays in the schema. It is a rendering input, not a
  security control, and a site that later needs another locale should not require
  a migration to express it.

## Verification

```sh
make contract-check
make build
make test-race   # -p 1: the database-backed suites share one database
make lint
```

CI must pass with no Milestone server reachable. If a test needs one, the test
is wrong.
