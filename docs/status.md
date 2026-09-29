# Status

Last revised 2026-09-29 (Phase 0 closure and trust sweep).

Milestone 1. This document is the source of truth for what is built. The README
is not, and neither is the commit history.

Green means: written, building, and covered by a test that would fail if it broke.
Everything in the table below is that. Where a thing is not done it says why.

**Verification:** `scripts/verify.ps1` green — contract-check across
all three published contract surfaces, contract-sync, vet, gofmt,
`golangci-lint` (including gosec), the generated-code check,
`go test -race -p 1 -count=1 ./...`, migration-phases, rules-lint, the RLS canary
and the Python runtime suite — against a real Postgres 16, with
`SP_TEST_DATABASE_URL` set so the database-backed suites ran rather than
skipped. `contractcheck` reports 20 messages and 22 enums compatible with the
baseline, plus 6 JSON Schemas (232 properties) and the OpenAPI document's 20
paths. No data races.

The gate used to be a subset of what it claimed. Three tools it named were not
installed and their absence was described rather than fixed, so `lint` and
`-race` were decorative: the config had enabled eleven linters since the
repository's second commit and none of them had ever run. The first run of
golangci-lint found **526 findings**, and among them three real defects:

- **`security-platform.env` was written world-readable (0644)** while carrying
  `SP_DATABASE_URL` and `SP_SECRET_KEY` — and the template the same function
  emits says in its own text "keep this file mode 0640". A preserved env file is
  chmod'ed on re-install too, so an upgrade fixes a file an older installer left
  open.
- **The outbox backoff raised on its own failure path.** `least(300, 5 *
  power(2, attempt_count)::int)` casts an unbounded exponent to int32 *before*
  the clamp; at attempt_count 31 the cast raises, the failure path errors, and the
  row is left claimed by a worker that has already moved on. A raising backoff
  turns a bounded retry into a lost message.
- **A seal that failed was logged nowhere.** `Scheduler.Run` discarded the error
  from every pass, so a sealer that failed every time — a key that would not
  load, a database that went away — produced no log line, no counter and no
  error, and the ledger simply stopped being sealed.

All three tools are now installed (MinGW-w64 for cgo, golangci-lint, protoc)
and all three are gates. Where a tool is genuinely absent the gate prints a
visible **SKIPPED** and says "this is not a pass", rather than turning itself off
quietly. `make verify` and `verify.ps1` are the same list again.

Every control changed in this round was run once with the control removed and
the corresponding test watched go red, and the results are recorded in the plan
file. A control with no such run is not a control; it is a comment.

> **The race detector did not run.** `-race` needs cgo, and this machine has no
> C compiler installed, so `go test -race` fails at build with
> `cgo: C compiler "gcc" not found`. The race step of `verify.ps1` is therefore
> unverified here. It is not a code failure and it is not a pass; it is missing
> evidence, and it is recorded here as missing rather than quietly dropped.
> MinGW-w64 is what supplies the compiler on Windows, and the toolchain section
> below lists it.

> **Verification is local. There is no pipeline.** This repository is private and
> on GitHub's free plan, and the account's billing state prevents any Actions job
> from starting. The workflow was removed rather than left as decoration that
> would never run; `scripts/verify.ps1` is the gate.
>
> The consequence to be honest about: every verification claim in this document
> is a **local** run on the machine that produced it, not pipeline evidence.
> There is no check on a pull request, and nothing stops a force-push to `main`.
> `scripts/enable-branch-protection.sh` fixes the second the day the plan is
> upgraded.

The linter configuration in `.golangci.yml` excludes four categories, each
with a written reason rather than a blanket suppression: writes to stdout from a
CLI, helper failures a test asserts for itself, `httptest.NewRequest` in tests
whose handlers read no context, and parameters kept for interface conformance.

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
| H — The runnable core service | 36–39 | green |

No command in the tree refuses as unimplemented. The one remaining guard in the
code is the audit endpoint's answer when no audit writer is configured, which is a
defence rather than a stub.

### The core service

`cmd/security-core` exists and runs the pipeline. It is the first binary here
that owns a lifecycle rather than describing one.

- `internal/config` reads the environment once and validates it. Every refusal
  names the variable and the consequence; nothing gets a silent default whose
  absence would change behaviour, and the database password is deliberately not
  a field on the config struct so it cannot be logged with the startup line.
- `internal/pipeline` runs the ingest, consume, outbox and gauge loops and owns
  the shutdown order: stop accepting, stop the loops with the pool still open,
  drain the outbox, close the pool.
- `internal/correlator` assembles a rule's signals from stored canonical events,
  device clocks and stored observations, and nothing else. It cannot re-infer,
  so a decision made from its output replays from stored facts.
- `internal/airuntime` is the Go client for `runtime.py`, and `QueueVLM` has a
  producer (the consume loop, after the incident) and a consumer (the VLM
  worker). Every failure path records a degraded observation; none fabricates an
  answer.
- `securityctl install` writes the systemd units and the environment file the
  shipped unit already referenced and nothing read. The env file is preserved on
  a re-install, because that is where the tenant id and the database password
  live.

The end-to-end test in `internal/pipeline` drives the real adapter, inbox,
normalizer, correlator, rule engine and incident service against a real database
and asserts the one thing that matters: an event that reaches `inbox_events`
reaches a terminal state.

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

**The systemd unit still says `Type=notify`.** Nothing in the tree implements an
`sd_notify` handshake, so a unit written by `securityctl install` waits out
systemd's start timeout before being killed as failed. This is a one-line
`Type=simple` and it is not changed here because a unit's service type is a
deployment decision a human signs. The same unit also sets
`MemoryDenyWriteExecute`, `SystemCallFilter=@system-service` and
`PrivateDevices`, each of which is incompatible with binding a GPU runtime: a
site that binds one needs those three relaxed, and doing so is an operator's
choice rather than a default.

**`systemd` is not exercised here.** The units are written and their content is
asserted, and this repository has no systemd to start them under. An install has
not been driven end to end by `systemctl`.

**The inference engine.** There is no model, no weights, no GPU and no signed
bundle. The runtime refuses rather than pretending, and `docs/status.md` says so
because a pilot's first question is whether the AI plane is doing anything.

**The Milestone endpoint surface is unverified.** Every route in
`internal/milestone/routes.go` is marked `Unverified` and the fixtures are
synthetic. No Milestone server has ever been reachable from this codebase. This
is the single most important open item: everything else is proven against a
fixture, and the one component that has not touched the real thing is the one
that talks to the customer.

**Policy version on an incident.** `internal/pipeline` records the bundle version
in `incidents.policy_version` because that is the identity it actually read.
There is no reader for the policies table, so there is nothing else to record,
and a policy version invented here would point an auditor at a policy that was
never consulted.

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
12. **`edges_active` is `security_invoker`** (migration 0014). A view is
    security-definer by default in Postgres, so RLS on the underlying tables was
    evaluated for the view's owner and the tenant policies never applied to a
    traversal. The migration also grants `security_core` SELECT on the view at
    all, which it did not have: every graph read the product performs — `Snapshot`,
    `Walk`, `CheckRebuild` — was failing with "permission denied for view
    edges_active" under the role the product runs as.
13. **The correlator's candidate window is bounded by a constant, not by the
    bundle.** A bundle is untrusted input; one asking for an hour-long window
    would otherwise turn every event into a scan of every other event. The clamp
    is reported rather than silent, because the alternative is a site whose
    incidents are quietly under-corroborated with nothing to explain it.
14. **`ai_runtime` is never bound to a non-loopback address without
    `SP_AI_ALLOW_REMOTE`.** `runtime.py` has no authentication of any kind, so
    anything that can reach the socket can read the site's evidence.

## Defects found and fixed while building the core

These are the Phase 0 and Phase A/B items of the 2026-09-28 sweep. Every one
shipped with a test that was run once with the control removed and watched go
red; the red runs are in the plan file.

### Intake and durability

- **Intake died on 2027-01-01.** `inbox_events` and `canonical_events` had
  monthly partitions ending at that date and no DEFAULT, so the first write of
  the new year raised "no partition of relation found for row". Migration 0015
  adds the 2027 partitions *and* a DEFAULT partition for both, so it does not
  recur in 2028. It also carries the two indexes the correlator and the inbox
  claim were missing, and a BRIN index on `canonical_events(normalized_at)`.
- **Nothing ever dead-lettered.** Every failure path in the consume loop logged
  and returned, so `inbox.Writer.Fail` had no caller: no attempt ever reached the
  retry bound, and an event that could never normalize was reclaimed every lease
  period for the life of the process. The loop now records the failure, `Fail`
  applies a backoff, and the claim itself retires a row whose budget is spent —
  which is what covers a worker killed mid-handle, where no cooperating code is
  left to call `Fail`.
- **Every partition created by 0015 carries the tenant and site policies.**
  PostgreSQL applies a partition's own policies and not the parent's, so a new
  partition created without them is an unfiltered relation.

### Identity and the API

- **Every MFA login was a 500.** The handler read the principal off the session,
  which is nil by construction on the MFA branch. The principal is now part of
  the result.
- **An MFA challenge could be used repeatedly.** The challenge row was never
  deleted — only a new session row was inserted — so one intercepted challenge id
  minted sessions for its whole two-minute TTL. It is now consumed in the same
  transaction as the session insert.
- **The second factor had no brute-force control.** The password step counts
  failures and locks the account; the second-factor step did neither, and the
  route behind it is CSRF-exempt. Migration 0016 adds a per-challenge attempt
  counter. It is deliberately *not* an account counter: the password already
  succeeded, so counting the second factor against the account would lock an
  operator out for mistyping six digits.
- **A TOTP code could be used twice.** A code is valid for a 30-second step plus
  the ±1 window, so it stays acceptable for about 90 seconds, and nothing
  recorded that it had been used. RFC 6238 §5.2 says a verifier must not accept
  the same OTP code twice. Migration 0017 adds a per-factor consumed-step cache
  and the record is the arbiter — an insert that loses the race refuses, so two
  concurrent presentations of one code produce one session, not two. The key is
  `(tenant, factor, step, code digest)` rather than the step alone, because this
  codebase accepts both SHA-1 and SHA-256 authenticators and the two produce
  *different* digits for the same step; keying on the step would break MFA for
  every authenticator that ignores `algorithm=` in the enrolment URI. A replay
  returns the same answer as a wrong code, so it is not an oracle. This was not
  in the sweep's item list; it is a real defect the TOTP work surfaced.
- **Anyone holding `identity.manage` could re-key an account.** `SetPassword`
  skipped the current-password check whenever `recovery_required` was set — and
  `EnsureUser` set that flag on every account it ever created, so the bypass was
  the normal state of every provisioned user rather than an edge case. The flag
  now means what its name says: this account has never had a password chosen by
  its owner. A provisioned account is created with a NULL `password_hash` rather
  than a random placeholder nobody keeps, which is the case the flag was
  standing in for. The audit action distinguishes an ordinary password change
  from a recovery, which it used to write as `identity.recovery.performed`
  either way — one row in a hash-chained ledger for two different events.
- **Every bootstrap administrator in every deployment was `admin-e3b0c442`.**
  `sha256.New().Sum(x)` returns `x` plus the digest of nothing, and `e3b0c442` is
  the first eight hex characters of the SHA-256 of no input at all. The inputs are
  also NUL-separated now, so `("ab","c")` and `("a","bc")` cannot collide.
- **A CSRF rejection was the only response with no security headers.**
  `requireCSRF` was the outermost middleware, so the four rejections it emits
  carried no CSP, no `nosniff`, no frame-deny, no HSTS, no request id, and
  nothing in the access log. The one response an attacker could read the shape of
  for free.
- **The client address was client-asserted.** `SP_BEHIND_PROXY` made the server
  believe the *leftmost* `X-Forwarded-For` entry, which is a header the caller
  writes. Every audit row, and any rate limiter keyed on it, was keyed on
  something the caller chose. `SP_TRUSTED_PROXY_CIDRS` is now required whenever
  `SP_BEHIND_PROXY` is set — empty list plus behind-proxy refuses to start, the
  same rule as the cookie and TLS refusals — and the address is the rightmost
  *untrusted* hop rather than the leftmost, with `X-Forwarded-For` ignored
  entirely when the direct peer is not a trusted proxy.
- **`SP_SESSION_COOKIE_SECURE` existed but was hardcoded to `true`.** The setting
  was not read, so there was no way to say what it was for, and an install on a
  non-loopback listener could not run without the secure flag. It is read now,
  and `false` on a non-loopback listener refuses to start rather than quietly
  emitting a session cookie over plaintext.
- The `csrfExempt` comment claimed the login routes were rate limited. No
  source-keyed limiter existed anywhere in the tree; what actually bounds guessing
  is the per-account lockout and the new per-challenge counter, and the comment
  now says so.

### Trust and evidence

- **The offline verifiers accepted self-signed forgeries.** With no trusted key
  supplied, `release.Verify` and `checkpoint.VerifySeal` skipped the comparison
  entirely and printed a warning — on the two commands whose entire purpose is to
  detect exactly that. Both now refuse, and both CLI entry points require
  `--trusted-key`.
- **The manifest signature did not cover the SBOM block.** The pointer to the
  SBOM was outside the signed bytes, so restating it was a one-line edit nobody
  detected. It is inside now, and the declared hash is compared to the archive's
  bytes rather than merely being present. This invalidates every signature signed
  before it.
- **A seal's public key was not in its signed payload.** A seal could be
  re-pointed at another key and re-signed by whoever held it. It is bound in now.
- **A checkpoint seal verified with a nil trusted key passed `internal/dr`'s
  recovery drill.** The existing test asserted that behaviour; the test now
  asserts the refusal.
- **`evidence.Store.Sweep` joined its tenant argument onto the root with no
  validation** and then deleted what the walk found. `Put` validates the same
  argument. A tenant of `../..` walked out of the evidence root.
- **The fixture sanitizer had no notion of a person.** Names, faces, image URLs
  and coordinates are not credentials, and a reviewer hunting for a leaked
  password does not flag any of them. Device identifiers that arrive as *numbers*
  were not covered at all, because the value-level rules never saw a number.
- **Non-regular tar entries were indexed and extracted as ordinary files.** A
  symlink, hard link or device node was hashed by its own bytes and written with
  `os.WriteFile`, in both `internal/release` and `internal/lifecycle` — and
  lifecycle is the one that lands bytes during an install. The empty body on a
  link entry meant the hash check would usually catch it, and "usually" is not a
  control.
- **A vendor session header was on the fixture allow-list.**
  `x-milestone-session` is a bearer of the vendor's authenticated session, in a
  list whose whole justification is deny-by-default. Nothing replays on it.
- **A bare `name` in a capture was committed.** It is the most ambiguous key in
  a vendor payload, and a person's name is one of its meanings.
- **The Milestone client followed redirects,** replaying the `Authorization`
  bearer to whatever host `Location` named. A 3xx is now refused by name, so an
  operator whose vendor moved gets a message that says so.
- **`runtime.py` compared a SHA-256 digest against a text file and called it a
  signature.** Anyone who could edit the bundle could regenerate the `.sig`. It
  is now real Ed25519 verification, pure-Python RFC 8032, standard library only —
  the air-gap supply-chain decision forbids a dependency. The tests include the
  RFC vectors and were mutation-tested six ways, including a revert to the old
  code.

### Correctness

- **The gate had no existence test.** `AGENTS.md` and this file both name
  `scripts/verify.ps1` as the thing to trust, and nothing asserted it exists, that
  every package it names resolves, or that it checks all three contract surfaces.
  Delete it and the suite stayed green. There is now a test that fails if it goes.
- **A diagnostic on stderr aborted the whole gate.** `verify.ps1` ran with
  `$ErrorActionPreference = 'Stop'`, so a tool that printed a warning — which
  PowerShell turns into a terminating error — killed the script before its exit
  code was read. The first two gates never wrote to stderr, so it went unnoticed
  until the OpenAPI gate had a warning to give.
- **A metric named a class of thing that does not exist.** `actions_dispatched_total`
  counted the relay acknowledging an outbox message it then logged and discarded;
  in M1 no action has physical impact, so a dashboard built on it would report a
  site taking physical action. It is now `outbox_messages_delivered_total`, and a
  test names the old one so it cannot come back by accident.

- **Recall could not fail.** `DetectedRestricted` counted every case the profile
  flagged, including benign ones, so one true positive and twenty false ones gave
  a recall of 21.0 — and the gate that decides M1 exit gate 4 is "at least 0.70".
  Every rate now divides by `Answered`, so a profile cannot improve its numbers by
  declining to answer, and a rate whose denominator is zero is reported as not
  measurable rather than left as an un-normalised count.
- **The promotion case floor was 30.** At thirty cases a profile sitting exactly
  on the 0.20 false-positive gate is still consistent with a true rate near 37%,
  and a true recall of 52% clears the 0.70 gate. It is 200.
- **Replay fabricated drift exclusions.** One incident with one 200ms-excluded
  camera marked every unrelated event with 200ms of drift as excluded too, by
  applying the first exclusion's measured drift as a threshold to every event. A
  drift exclusion is a fact about a device, not a number to compare against.
  The observation a replay cites is now chosen in one place, and `observation_id`
  is the tiebreak on `created_at` so "oldest" is a total order.
- **`graph.Walk` read through the bare pool,** so under the production role RLS
  emptied it and the traversal reported an empty graph with no error anywhere.
  The RLS canary now covers it; the existing graph tests connect as a superuser
  and could not have seen it.
- **A stale tenant could survive on a pooled connection.** The clear that runs
  before release discarded its error and released unconditionally, so a failed
  clear was indistinguishable from a successful one. It now destroys the
  connection.
- **An uncorroborated decision's log line said the opposite of what happened.**
  It said "no incident raised" and then raised one, correctly — `unverified` is a
  first-class verification state, indexed as one. The log line was wrong, not the
  pipeline.

### Migrations

- **`VerifyChecksums` compared only the checksum.** A renamed migration or a
  rewritten phase passed. It now compares the applied row's name and phase as
  well, and the duplicate pre-DDL call in `Up` is gone. The checksum *format* is
  unchanged: changing it would make every deployed site refuse to upgrade, and
  nothing in this repository can `UPDATE schema_migrations`, so the re-record
  could not be delivered. Schema version is now 17.

  The gate is real: it caught an edit to an already-applied migration during
  this sweep, and the correct response — reset the test database — is what was
  done. That is the control working, not the control being inconvenient.

- **`internal/clocksub` had no tests, and the drift measurement it owns is the
  input to every time-windowed corroboration decision.** A device 400 days out
  wrapped the int32 conversion and was recorded as 200,261,632ms — a positive
  number, in the severe band, therefore looking entirely plausible while being
  wrong by a factor of 172. Saturating has to work in both directions or a clock
  that is behind is recorded as one that is ahead.
- **`normalized_at` had no test at all.** It is, by AGENTS.md, the only field a
  time-window predicate may read, and the function producing it was unwritten:
  drift correction, the missing-time fallback, the UTC normalisation and the
  derived (not generated) event id all shipped untested.
- **The outbox relay had no tests**, and its backoff raised on its own failure
  path (above). The idempotency key, the tenant scoping and the dead-letter
  transition are all asserted now.
- **The activation digest had no tests.** It is what the bundle-activation gate
  compares against a reviewed value, and it must be stable across a re-run and
  sensitive to every real input; both directions are now asserted, and the empty
  diff is asserted to be a digest rather than the empty string an activation
  would compare equal to anything that failed to produce one.

### Still open, and needing a human

- **The DR drill is not part of the gate.** `internal/dr` runs the whole-recovery
  drill and passes, but as a package test rather than as a step a release is
  gated on.
- **`internal/schemalaws` has eight unhandled errors in its tests** — a
  `testutil.ExpectError` whose return is discarded. They are excluded rather than
  fixed, on the grounds that the helper asserts by panicking; if that changes, the
  exclusion changes with it.

## Defects found and fixed while building the core

Each of these was live, invisible to the suite, and total in production. They are
listed because the pattern matters more than the list: every one of them is a
documented field, interface or unit that nothing assigned, granted or read.

- **No event could ever be accepted from Milestone.** `RawRecord.Envelope` was
  declared, documented, and never assigned, so every record reached the inbox with
  an empty payload and was refused with "payload is required". The entire ingest
  path was dead while every unit test passed, because the tests asserted on
  `RawRecord` fields and never on the write.
- **`security_core` could not read the graph at all.** Migration 0003 created the
  `edges_active` view and migration 0008 granted privileges on the tables under it
  but not on the view. Every graph read failed under the production role.
- **`KnownMilestoneEventTypes` was keyed on the wrong vocabulary.** Twelve of its
  eighteen keys could never be reached because the check runs against the
  *normalized* type, and five types the adapter really emits were not keys at all.
  Every motion, analytics and access event on a live site arrived flagged
  "unrecognised source event type".
- **The graph write path issued its INSERTs against the bare pool.** With no
  `app.tenant_id` set, RLS refuses the write outright under the production role.
- **`observability.NewCounters` seeded every gauge into the counter set.** Render
  then emitted each gauge twice, once as each kind; a metric name carrying two
  type declarations is a scrape the parser rejects.
- **`api.Server.start` was never assigned**, so `/v1/health` reported an uptime of
  about one second for the life of the process.
- **`ListenAndServe` never called `Shutdown`.** It used the context only as a
  `BaseContext`, so a signal stopped nothing and in-flight requests ran against a
  pool the process was about to close.
- **The request log carried no `tenant_id`, no `site_id` and no `request_id`,**
  which AGENTS.md requires and which made the log useless for the question it
  exists to answer.
- **`evidence.Options.Fsync` was a plain `bool`,** so every caller writing
  `Options{Root: dir}` got a store that acknowledged evidence it had not fsynced,
  against a field comment that said the default was true. `VerifyHashOnRead` was
  read by nobody at all.
- **The request log recorded a request id that could be absent**, and the panic
  path read it from the header directly. One id is now assigned at the outermost
  middleware, echoed to the client, and carried in the context.
- **`identity.SecretStore` had no implementation.** MFA enrolment could not have
  worked in production; the tests used a reversible prefix.
- **`checkpoint.SealRange` had no caller.** The ledger was append-only and
  hash-chained, and nothing ever sealed it.
- **The AI runtime endpoint was unwired** — `apiVersion` as an integer is
  asserted against `runtime.py`'s own `!=` comparison, because a client sending
  the string `"1"` gets a 400 that reads like the runtime is broken.
- **The client address was the caller-asserted one.** `X-Forwarded-For` was read
  leftmost when `SP_BEHIND_PROXY=true`, and every header was believed from any
  peer. That value is the audit `remote` field and the key a rate limiter
  buckets on, so both were keyed on whatever the caller wrote. The effective
  client is now the rightmost entry that is not a configured trusted proxy, and a
  peer outside the trusted list has its header ignored entirely.
- **`SP_TRUSTED_PROXY_CIDRS` is now required whenever `SP_BEHIND_PROXY=true`**
  — a comma-separated CIDR list, no default — and the process refuses to start
  without it. The same refusal applies to `SP_SESSION_COOKIE_SECURE=false` on a
  non-loopback listener, and to a configuration with neither TLS nor a proxy,
  which is a plaintext listener with nothing terminating it. A site that
  genuinely sits behind a proxy has one to name.

## Reproducing the verification

```sh
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
export SP_TEST_DATABASE_PASSWORD=postgres
go run ./cmd/security-migrate up --dsn "$SP_TEST_DATABASE_URL"
.\scripts\verify.ps1          # contract-check (three surfaces), contract-sync,
                              # vet, gofmt, test -race -p 1, migration-phases,
                              # rules-lint, rls-canary, ai-runtime
.\scripts\verify.ps1 -NoRace  # the same, without the race detector

cd deploy/ai-runtime && py -m unittest discover
```

`security-migrate` defaults to a silent local DSN when none is given. That is
the same class of defect as a CSRF exemption that is really an absence, and it
is on the list to fix.

Without `SP_TEST_DATABASE_URL` the database-backed tests skip rather than fail,
and say so. A green run with the variable unset is a run that tested almost
nothing, so confirm the suite you are reading actually ran. `-p 1` is required:
the database-backed suites share one database and rebuild the schema, so package
test binaries must not run concurrently.
`verify.ps1` without `-NoRace` needs cgo. On Windows that means MinGW-w64's `gcc`
on `PATH`; without it the race step fails at build rather than at a data race, and
the failure message names the missing compiler.

## Local toolchain

- Go 1.26 and `protoc`, both via winget.
- MinGW-w64, for the cgo the race detector needs on Windows. **Not installed on
  the machine this revision was written on**, which is why `-NoRace` was used and
  why the race step is called out above as unverified rather than passed.
- Python 3.14 via the `py` launcher. The runtime targets 3.11+ syntax and uses
  the standard library only, which CI asserts.
