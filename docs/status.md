# Status

Last revised 2026-10-04 (Phase 0: the product runs. `Dockerfile`,
`docker-compose.yml`, `cmd/security-seed`, a distroless runtime image with an
in-binary health probe, and `docker compose --profile verify run --rm verify`
running the SAME `scripts/verify.ps1` under PowerShell on Debian — which found
five defects that had never been found because the gate had never run on Linux.
See "The 2026-10-04 containerisation pass" below.)

Previously 2026-10-02 (a capture-path pass over `internal/rtsp`,
`internal/mp4`, `internal/detector` and `securityctl poc`: two defects with
red-checked tests, the per-frame copies on the hot path removed, and the dead
code in the poc command taken out — see "The 2026-10-02 capture-path pass"
below. The hackathon Phases 1, 3, 4 and 5 of 2026-09-29 — the second AI
provider behind a shared interface, the vendor write-back and clock seam, the
vendor-neutrality invariant, the general profile, the fail-closed rule for a
degradation with no reason, the soak and chaos harness, the adapter probe, the
acceptance report, and a repo-wide encoding repair — are described below where
they happened.)

Milestone 1. This document is the source of truth for what is built. The README
is not, and neither is the commit history.

Green means: written, building, and covered by a test that would fail if it broke.
Everything in the table below is that. Where a thing is not done it says why.

**One item in this record is awaiting a human signature.** `AGENTS.md` reserves
migrations, auth and anything touching an invariant to a human who reads the
diff. The 2026-10-02 work touched all three — the test harness now creates a
database per package and the product roles under a cluster-wide lock, and
migration 0008's role DDL is now retried under concurrency. That is recorded as
**R13** in [`docs/ratification.md`](ratification.md) with the decision, the diff,
the evidence, the alternative, and the one thing in it that deserves the most
scrutiny: the harness issues cluster-scoped DDL against whatever host
`SP_TEST_DATABASE_URL` names. It is written as per-package **database** isolation
rather than the per-package **schema** the plan asked for, because migration 0008
hardcodes `public` and a per-schema harness would apply **no row-level security at
all**; the reason is in R13.

**Verification:** `scripts/verify.ps1` green - twenty-five gates: contract-check
across all three published contract surfaces, contract-sync, vet, gofmt,
`golangci-lint` (including gosec), the generated-code check,
`go test -race -count=1 -json -coverprofile ./...`, skip-audit, no-silent-skips,
cover-floor, dead-export, predicate-cov,
red-check, migration-phases, sql-guard, skills-audit, rules-lint, the RLS canary,
the Python runtime suite, and three more named `tree-state`, `fuzz` and `flake` -
against a real Postgres 16, with `SP_TEST_DATABASE_URL` set so the database-backed
suites ran rather than skipped.
`contractcheck` reports 20 messages and 22 enums compatible with the baseline, plus
7 JSON Schemas (248 properties) and the OpenAPI document's 20 paths. No data
races. The most recent dated run record is
`dist/verification/verify-20261002-144151.json`, `verified: true`, with a gate list
of seventeen, zero failed, zero skipped, against a real database - including
`recovery-drill`, which is the gate that skips whenever the database variable is
absent, and which was the reason the previous run of that record was not evidence
even though it was green. The twenty-five gates above are a later tree than that
record: skip-audit, no-silent-skips, cover-floor, dead-export, predicate-cov,
tree-state, fuzz and flake were added
after it, so the record's own gate list is a statement about that run, not about
today's script.
`red-check` is 7 of 7 controls proved in 12 seconds; `skills-audit` is 1 second,
because it checks freshness rather than rescanning 67 skills.

### `no-silent-skips`: the run that tested nothing is now a red gate

**This is the highest-value item in the plan, and the reason it exists is in this
repository's own words.** `AGENTS.md`: *"The database-backed tests **skip** rather than
fail when the environment variable is absent, which means a green run can be a run that
tested nothing."* And: *"Always confirm they actually ran."*

Confirming is a thing a person has to remember to do. `skip-audit` did not cover it,
because it answers a different question — *is every skip DECLARED?* — and the two can
both pass over a run that tested nothing. A skip can carry a perfectly good reason and
still be forty-two suites declining to start because nothing set the variable.

The gate reads the run's own JSON stream, which is the same stream `skip-audit` reads,
and fails on either of two conditions:

| Condition | Why it exists |
|---|---|
| `SP_TEST_DATABASE_URL` unset and something skipped | the contributor has no database. Every database-backed suite declined, which is **correct behaviour**, and which means the run is not evidence. |
| something skipped with *"test database unavailable"* or *"is not set; skipping database-backed"* in its output | **the dangerous one.** The variable is set, so the configuration looks right; nothing connected because the password is unset, the container stopped, or the port moved. |

The second is not hypothetical and it is not rare. Docker Desktop was stopped mid-session
while this was being written, and the run that followed was **green on every gate**,
including `skip-audit`, because the configuration still said `SP_TEST_DATABASE_URL` was
set. The gate named the 287 tests that declined and printed:

```
287 database-backed test(s) declined to run even though a database URL is configured.
This run is GREEN and EMPTY. That is the failure mode this gate exists for.
```

It matches the harness's own **wording** rather than a list of test names, so it does
not need maintaining alongside `internal/testutil`. The alternative — naming the
database-backed tests — is a list that goes stale silently, and a stale list is a gate
that stops firing.

`-SkipNoDatabase` exists for a contributor who genuinely has no Postgres and is not
claiming a verified run. It does not pass quietly: it prints *"This run is NOT evidence
about the database-backed suites; they did not execute."* A gate that can never pass on
some machines is a gate that gets deleted; this one has an exit that says exactly what
it gave up.

**It has two conditions and only one transcript below**, because the second was caught
live and the first is the same code path with the variable absent. Both are recorded
here rather than only the one that happened.

**The four gates added with the 2026-10-02 pass, and what they caught.** All four
are about the run rather than the product, which is where this repository's
defects have clustered:

| Gate | What it is | What it found on its first run |
|---|---|---|
| `skip-audit` | reads the `go test -json` stream and fails on any skip not declared in `internal/skipaudit/declared-skips.json` with a reason | a run of this repository reported exit 0 with ten skips in it, including two installer tests whose skip message said "the rest of this test still runs" while `t.Skip` had prevented exactly that. Both tests now run on Windows, and the two platform-gated declarations carry the `when` field that keeps them honest on Linux |
| `dead-export` | every exported function, var, const and method must have a reader outside its own package's tests | 213 unread symbols and 23 uncalled methods. The plan named nine of them; the rest are recorded in the allow-list in `internal/deadexport/deadexport_test.go`, each entry saying whether it is a published vocabulary or genuinely unwired. **Six of the unwired ones are controls, not plumbing, and are listed below.** |
| `predicate-cov` | no SQL predicate may bound `observed_at`, `received_at` or `captured_at`; the canonical-event window query must bound `normalized_at`; and the window query may not mention a source-time column at all, even in a select list | `rules.State` carried an `ObservedAt` field that nothing read, and the correlator selected the column to fill it. The field is gone, the column is out of the select list, and the gate fails if either returns |
| `cover-floor` | a committed per-package statement-coverage floor, failing both downwards and upwards | nothing in the product — it is the first gate here whose first run was about itself. It then failed on `internal/covercheck` one turn later because the package had gained tests, which is the ratchet working on the gate's own author |

**Six unwired controls, named here because a security reader should not have to
find them in a test file.** `dead-export`'s allow-list is where they are tracked;
this is where they are *stated*, because `docs/status.md` is the only source of
truth for build state and "a backlog made visible" is not a statement an operator
can act on:

| Symbol | What it is | What is currently holding that role |
|---|---|---|
| `identity.AuthorizeSite` | the site-scope authorisation check at the application edge | migration 21's RESTRICTIVE `site_isolation` policy alone. A session can reach another site's rows if any query bypasses the policy |
| `identity.StepUp` | the step-up proof | nothing. Escalating a sensitive incident presents no fresh factor, so a session that was adequate fifteen minutes ago is the whole basis of the decision |
| `identity.RotateCSRF` | CSRF rotation on privilege change | nothing. A CSRF token minted before an escalation remains usable after one |
| `identity.WrapFor` / `UnwrapFor` | row-bound secret sealing | nothing. `SecretStore` — one of the nine dead exports that started this work — is unwired, so a ciphertext copied to another row would still decrypt |
| `clock.AssessGap` | the clock-gap assessor an exclusion's reason depends on | nothing. The corrector still runs, so `normalized_at` is still written, but nothing *assesses* it and an unverifiable exclusion carries no reason |
| `pipeline.IngestScoped` | the tenant-scoped ingest entry point | the unscoped path. Adapters do not state which tenant they are ingesting for at the call site |

Four of the six are closed by P5 (the disposition form spends a step-up, rotates
CSRF, and authorises per site). The other two are harness-and-ingest changes and
belong with the work that makes the adapters state their tenant.

Each of the four gates also found a defect in itself, which is the only honest way
to introduce one: `dead-export` reported an unread symbol in its own package,
`predicatecov` reported the field its own documentation explained the removal of,
`covercheck` measured generated protobuf as if it were hand-written, and
`skip-audit` decoded nothing at all on the first run because Windows PowerShell's
`>` writes UTF-16.

**What this round added, and what it cost.** Three things, all of them about this
gate rather than the product:

| Found | Cost had it shipped |
|---|---|
| `docs/status.md` claimed 6 JSON Schemas (232 properties) when the tree had 7 (248) | the document every reader is told to trust understated the published contract surface |
| `docs/status.md` named fifteen gates while the script ran seventeen | a reader checking the gate list found two gates the document did not mention |
| `Invoke-Gate` read `$LASTEXITCODE` without resetting it, so a gate whose last operation was a cmdlet inherited the previous gate's verdict | measured: a gate that printed a string was reported FAILED after a gate that had exited 1. Every gate after a failing one was mislabelled |
| `gofmt` and `generated-code` called `exit` inside a gate, which ends the whole run | measured: a gofmt failure meant every later gate never ran and `dist/verification/verify-<ts>.json` was never written — the one artefact that names the gate that stopped the build |

Two of those four were found by machinery rather than by reading: the first two by
`internal/statuscheck`, which now derives every countable claim in this paragraph
and fails when the document disagrees; the last two while adding a gate and
finding that the gate framework would not report its own failure.

**The counts in that paragraph are now checked.** `internal/statuscheck` derives
every one of them from the file that defines it - the contract counts from
`contracts/compat/baseline.json`, the gate count from the distinct gate names in
`scripts/verify.ps1`, the Go minor from `go.mod` - and fails when this document
prints a different number.

It was written because this paragraph had already been wrong three times. It
claimed a package count of twenty-four against a tree of thirty. It named a Go
minor two releases ahead of the `go.mod` directive. And it undercounted both the
published schemas and their properties, because a fourth profile was added and
nobody recounted. `internal/README.md` trap 14 records the first two. Correcting
a number by hand does not keep it correct, so the numbers derive now and the
document is checked against them.

One scope restriction, stated because it is the kind of thing a reader would
otherwise read as a bug: **the claims are checked only in the `**Verification:**`
paragraph.** The rest of this document records what was true on a given day — a
table of what a round fixed, a dated run record — and editing one of those to
match today's tree would be falsifying a record rather than correcting a claim.
The scope was not free: it was added after the checker correctly reported twice
that sentences *describing* the stale numbers ("this paragraph had claimed six
JSON Schemas") were defects. The checker was right about the pattern and wrong
about the sentence, which is the harder failure — it teaches an author to write
less about why something changed. `TestNumbersOutsideTheVerificationBlockAreNotChecked`
holds the scope with a synthetic document, so it cannot rot into an assertion
about whatever prose happens to be in the file today.

**`internal/statuscheck` is red-checked.** Restoring the three stale numbers by
hand and running the check produced three findings, each citing its line in this
document:

```
docs/status.md:27: published JSON Schemas is 6, the tree says 7.
docs/status.md:27: schema properties is 232, the tree says 248.
docs/status.md:20: gate count is 15, the tree says 17.
```

Without that run it would be a comparison nobody has watched fail, which is the
comment this repository keeps refusing to accept.

**`red-check` is now seven controls, and the third one found a missing test.**

| Found | Cost had it shipped |
|---|---|
| `unsafeSegment` in `internal/evidence/store.go` rejects a path segment beginning with a colon, and **nothing tested it** | the only check that rejects `:foo` — no backslash, not absolute, no volume name, `strings.Trim(segment, ".")` not empty. Mutating the regex to match nothing left `TestValidateRefRejectsEveryTraversalForm` fully green. A control with no test is a comment |
| a segment with a colon **not** at the start is accepted: `a/b:stream` returns no error | on Windows that is alternate-data-stream syntax, so a reference reaches a stream rather than a file. **Not fixed here — it is a behaviour change and it is a human's call.** See below |

The first row is fixed: `:foo` and `a/:foo` are now in that test's bad list, the
regex is guarded, and mutating it turns the suite red. The second row is not
fixed, deliberately — refusing `a/b:stream` changes which references the store
will hold, which is a change to the control rather than to its test, and
`AGENTS.md` reserves those for a human who reads the diff. It is written here so
it is not lost.

**`red-check`'s two controls are red-checked.** All seven were run by hand with
the mutation applied and watched turn the suite red before being put in the table:
removing the degraded-observation reason requirement fails
`TestADegradedObservationWithNoReasonIsRefused` on all five degradation values, and
its mirror fails `TestANonDegradedObservationWithAReasonIsAlsoRefused`.
The gate runs the same table on every verification, and afterwards re-runs every
package the table mutates and requires green — a restore check that named packages
by hand would stop covering one the moment a control was added somewhere new, and
the gate meant to prove the table left the tree alone would be the gate not
watching the table's own footprint.

### The site gate: `AuthorizeSite` wired, and a control that refused to be a table entry

`identity.Principal.AuthorizeSite` carried `TEST-ONLY` in the dead-export ledger:
holding the capability is not being assigned to the place, the function existed and
was correct, and the console's per-site routes did not call it. They called
`p.CanSeeSite(site)` by hand, inside each handler that knew a site, after that
handler had already fetched the thing the site belonged to.

Two things were wrong with that. The site check was a **second thing to remember**,
so a handler added tomorrow would be written with `requireCapability` because that is
what every sibling looks like, and nothing in the router could notice. And it ran
**after** the handler's work, so a caller who was not assigned could make the server
read a row and only then learn no.

`internal/api.requireCapabilityAtSite` is now the wrapper the five incident-addressed
routes go through: it resolves the incident's site and calls `AuthorizeSite` before
the handler runs, and an id that resolves to no site is refused with the same 404 an
unassigned site gets. `GET /v1/incidents` stays on `requireCapability` and filters
per row, because "every site I may see" is the correct answer for a scope-wide read
rather than an exemption from the gate.

Nine subtests were added to `TestHoldingACapabilityIsNotBeingAssignedToThePlace`,
which previously had exactly one cross-site case and it covered the list route:

| Subtest | What it asserts |
|---|---|
| refused on the incident detail / explanation / evidence list / disposition / console page | each of the five routes answers **404** for an incident at a site the principal is not assigned to |
| an id that resolves to no site is refused, not served | the `site == ""` arm, and that the answer is the same 404 |
| an incident at the principal's own site is served on the same routes | **200**, because a filter that refuses everything is not a filter |

Baseline green, mutation applied, restore green:

```
ok  	github.com/Sergey-Bar/Ai-Captain/internal/api	4.641s
baseline exit=0   (must be 0)
--- FAIL: .../an_incident_at_an_unassigned_site_is_refused_on_the_incident_detail
    handlers_test.go:951: an incident at unassigned site "site-not-mine" returned 200, want 404:
        {"incidentId":"inc_site-not-mine_1791060224211697600","siteId":"site-not-mine","state":"open",...
--- FAIL: .../an_incident_at_an_unassigned_site_is_refused_on_the_explanation
    handlers_test.go:951: returned 409, want 404: {"code":"no_active_bundle",...
--- FAIL: .../an_incident_at_an_unassigned_site_is_refused_on_the_evidence_list
    handlers_test.go:951: returned 200, want 404: {"items":null}
--- FAIL: .../an_incident_at_an_unassigned_site_is_refused_on_the_disposition
    handlers_test.go:951: returned 201, want 404: {"incidentId":"inc_site-not-mine_...","state":"dispositioned"}
--- FAIL: .../an_incident_at_an_unassigned_site_is_refused_on_the_console_page
    handlers_test.go:951: returned 200, want 404
ok  	github.com/Sergey-Bar/Ai-Captain/internal/api	5.250s
restore exit=0   (must be 0)
```

The **201** is the one to read twice: with both gates gone, a principal assigned to
one site could record a disposition on another site's incident. The 200s carry the
full incident including its rule id, bundle version, verification state and
supporting event ids.

**This control is NOT in `scripts/verify.ps1`'s table, and the reason is the
finding rather than an omission.** It is three substitutions across three files, and
**each one alone leaves the suite green**:

| Mutation | Result |
|---|---|
| the router's `AuthorizeSite` call only | **green.** Every handler still has its own `CanSeeSite` |
| `handlers.go`'s `!p.CanSeeSite(detail.SiteID)` only | **green.** The router gate catches it |
| `console.go`'s `!p.CanSeeSite(detail.SiteID)` only | **green**, and the console-page subtest above is not exercised at all, because the substitution never reached the file |

A table entry mutates one line in one file and reports the red it got. Three
entries that each report green would be three entries claiming a control nobody can
prove, which is the failure `AGENTS.md` names when it says entries are added only
after being run by hand and watched go red. The table's format cannot express a
control that lives in three files, so the transcript is here and the gap is stated.

Two earlier attempts at the mutation produced **compile errors**, not failures, and
both are recorded here because a compile error is not evidence that a control was
removed:

  - replacing the handler check with `false` or `true` leaves `detail` unused in
    `handleIncidentExplain` and `handleDisposition`, which use it for nothing else.
    The replacement is a comparison against a site id no row can have.
  - replacing it with `true /* ... */` commented out the `{` on the next line.

**One of the new subtests was passing for the wrong reason.** The disposition case
sent `"agreement": true`, and `dispositionRequest.Agreement` is a **string** in a
closed set that migration 0005's `dispositions_agreement_check` enforces. It passed
because the gate refuses before the body is decoded, so the malformed body was never
reached. The fixture was only read when the gate was removed to red-check it, and
the test failed on a 400 about JSON rather than on the 404 that would have said the
incident was served. A test whose fixture is never parsed is a test whose fixture is
not a test, and this one would have kept passing for as long as the gate held.

**The dead-export ledger's own rule caught the wiring.** `AuthorizeSite`'s entry said
`TEST-ONLY`, and the gate's unused-declaration check failed the moment the console
called it: `the allow-list excuses internal/identity.AuthorizeSite and nothing
matches it any more. Delete the entry rather than leaving it to look like a decision
that was made`. The entry is deleted. Four of the five other unwired controls remain
and are listed in Part I.6's backlog below.

### The ingest path was the one path that wrote rows without a site binding

`pipeline.Deps.IngestScoped` was in the dead-export ledger as `UNWIRED`: the
tenant-scoped ingest entry point nothing called. It existed for one reason, stated
in its own doc comment: the ingest path should get **one** call site, so a missing
site id is refused at the start of every pass rather than at the first query that
happens to need a connection.

The connection was instead built inside `loadCursor`, and `loadCursor`'s error was
read as *"no cursor is not fatal: it means a full re-read"*. Which is true of a
cursor, and was applied to a site binding. So with `SP_SITE_ID` unset, a pass
logged that line, **polled the adapter anyway, and wrote every record it got into
`inbox_events`** through a connection with no tenant context bound.

That is the whole severity of it. Under RLS a write with no tenant context is
refused by the database — but the write is done by the pool's owner role, and an
owner bypasses the policies. So `Scoped`'s refusal was the only thing between a
misconfigured daemon and tenant-scoped rows written with nothing scoping them, and
the one path that writes rows was the one path that skipped it.

`PollOnce` now resolves the scope once, before anything is polled, and refuses the
whole pass. The cursor is untouched, so the next pass re-reads the same window once
an operator has set `SP_SITE_ID`.

| Control | Mutation | Result |
|---|---|---|
| an ingest pass with no site id writes nothing | the `if _, err := l.Deps.IngestScoped(); err != nil {` call deleted, which still compiles | `TestAnIngestPassWithNoSiteIngestsNothing` FAIL: `a pass with no site id wrote 1 inbox row(s)` and `the accepted counter moved from 0 to 1 on a refused pass` |

```
ok  	github.com/Sergey-Bar/Ai-Captain/internal/pipeline	3.724s   (baseline)
ok  	github.com/Sergey-Bar/Ai-Captain/internal/pipeline	2.842s   (after restore)
```

Two tests, both directions: `TestAnIngestPassWithNoSiteIngestsNothing` asserts zero
rows **and** an unmoved counter — the counter is the sharper half, because a row
count of zero is also what a broken adapter produces, while a counter that moved on
a refused pass means the refusal happened *after* the work. And
`TestAnIngestPassWithASiteIngests` asserts the pass still works, because a guard that
refuses everything is not a guard.

This is in the table, unlike the site gate above, because it is **one line in one
file** and removing it leaves a function that compiles. The table's format can
express that; it cannot express the site gate's three files.

The dead-export ledger's unused-declaration check caught the wiring on its own:
`the allow-list excuses internal/pipeline.IngestScoped and nothing matches it any
more. Delete the entry rather than leaving it to look like a decision that was
made.` Deleted.

And `tree-state`'s fourth assertion caught the omission this paragraph fixes: the
moment RC-12 was added to `scripts/verify.ps1`, the gate reported it as
`an ingest pass with no site id writes nothing -- is in scripts/verify.ps1 and
appears nowhere in its transcript document`. A control in the table with no record
of being watched fail is a control that is a comment, and the gate says so without
being asked which is the only way anyone finds out.

### `dead-export` was reporting three dead exports as live, and its own looseness was why

Wiring `GaugeEvidenceDiskFree` put `runtime.KeepAlive` into `internal/pipeline`, and
`dead-export` immediately reported its own allow-list as stale:

```
the allow-list excuses internal/rtsp.KeepAlive and nothing matches it any more.
Delete the entry rather than leaving it to look like a decision that was made
```

`internal/rtsp.KeepAlive` had a reader. The reader was `runtime.KeepAlive`.

`addSelectors` records every selector name in the tree **unqualified**, because a
method is only ever reached through a receiver and a per-package rule would report
every interface method as dead. The one qualification it applied was "the qualifier
names one of **this repository's** packages" — so a qualifier naming an **imported**
package still counted, and:

| Masking reader | Dead export it was hiding |
|---|---|
| `filepath.Walk` in `internal/deadexport`, `internal/predicatecov` and `cmd/securityctl/pack.go` | `internal/graph.(*Service).Walk` |
| `ed25519.PrivateKey(key)` in three `cmd` files — a **type conversion**, whose selector read counted | `internal/checkpoint.(*Signer).PrivateKey` |
| `embed.FS` / `fs.Sub` in `internal/contracts/schemasync` | `internal/contracts/schemasync.FS` |

The direction is the one `deadexport`'s own package comment calls the bad one:
**reporting a dead export as live**. Its words are *"Missing a dead symbol is the safe
direction; reporting a live one is the direction that gets the gate switched off."*
Three exports had been switched off without anybody noticing.

Import names are now collected per file and excluded exactly as local package names
are, because for this purpose they are the same thing — an identifier that cannot be
a receiver. Blank and dot imports are excluded, or recording `_` as a package name
would suppress every selector written after it in that file.

Red-checked, with a mutation that still compiles — the first attempt left `imports`
unused and the package failed to **build**, which is not evidence that a test caught
anything:

```
--- with the import exclusion removed:
    --- FAIL: TestAnImportedPackageQualifiedSelectorIsNotAMethodCall
        deadexport_test.go:633: filepath.Walk excused an uncalled method of the same
        name; findings were [walker.Visiting]
--- restored ---
    ok
```

The three are now in the allow-list with what closing each one actually takes, and the
two that are genuinely dead product code are named as such: `checkpoint.PrivateKey`
should probably be **deleted** rather than wired, because key material should flow one
way into a `Signer`, and `schemasync.FS` needs a validator that takes a whole
directory rather than a single file, which is a real requirement for rule-bundle
schemas that `$ref` each other and not a one-line wire-up.

### Nine unwritten metrics: eight closed, and one reason was wrong

`internal/observability/metric_producers_test.go` is a ledger of every declared metric
nothing writes, with a required reason per entry, and a gate that fails on a new one
and fails on an entry that has gone stale. **All eight entries are now closed and the
ledger is deleted**, along with the floor test that said to delete it in the same
commit.

Wired, because the number was already computed somewhere nobody looked:

| Metric | Where the number already was |
|---|---|
| `CounterAuditAppends` | `audit.Writer.Append` returns the row's hash on the only path a row reaches the ledger |
| `CounterOutboxDelivered` | `outbox.Relay.DrainOnce` returns the delivered count as its result |
| `CounterRuleBundleActivated` | `bundles.Service.Activate`, one event on an append-only path |
| `GaugePostgresConns` | `pgxpool.Stat()` has counted this in memory since pgxpool existed |
| `GaugeEvidenceDiskFree` | **the one that changed a gate** — see below |
| `GaugeClockDriftMS` | `drift_ms` has been a column on `device_clocks` since the table was created |

**`GaugeEvidenceDiskFree` is the one that mattered.** `evidence_disk` in `slo.go`
breaches below ten percent free, and `DiskFreeMeasured` is false while nothing writes
the gauge — so the objective printed *"not measured"* on every install and never once
had an opinion. It needed a platform split, because **the standard library has no
disk-space call that works on both platforms**: `syscall.Statfs` does not exist on
Windows, and Windows' `syscall` exports no `GetDiskFreeSpaceEx` wrapper at all.
`disk_unix.go` and `disk_windows.go` split it, the Windows half through the loader the
standard library already provides rather than adding `golang.org/x/sys` for one
function. `Bavail` on Unix and `freeBytesAvailableToCaller` on Windows, because
`Bfree` counts space reserved for root — a gauge counting it reports room right up to
the point where every write fails with ENOSPC.

**Two entries had reasons that were wrong**, which is the more interesting half:

```
CounterEvidenceFetched: "evidence retrieval is M2's blocker. There is no fetch worker
                        to increment it; inventing one now would be inventing the thing
                        that calls it."
```

Retrieval has no *worker*, and `GET /v1/evidence/{id}/content` is a perfectly good
fetch event: it opens the object, verifies its SHA-256, and writes an
`ActionEvidenceFetched` row to the **hash-chained audit ledger**, which is a more
authoritative record of a fetch than a counter is. Both counters are now wired there.
The excuse read like a decision, which is exactly why it survived — and it is the
second time in this session that a plausible reason has been the thing hiding a
defect.

**One entry was a duplicate asking to be guessed at.** `CounterDeadLettersVisible` sat
beside `CounterInboxDeadLettered` with no stated difference and is **deleted**. It
would have had to be incremented by whoever renders the dead-letter list, so it counts
operator attention rather than a system state — and every value such a series can take
is a function of how often somebody looked. The operator-attention number this
repository cares about is `GaugeIncidentOpen`, and it is defined as states an operator
watches rather than events they accumulate.

### The write canary: every canary in this repository was a read

`internal/rlsrole` had five canaries and every one of them asserted that a statement
issued without a tenant context returns **no rows**. That is the shape of a SELECT
under a PERMISSIVE policy with no matching context, and it is a real and valuable
thing to prove. It is also, entirely, the shape of a READ.

**A read that crosses a boundary returns nothing, so the defect is invisible.** A read
that *should* return rows and correctly does is indistinguishable from a read that is
not filtered at all. Nothing in the suite tried a write, and a write is where the cost
is: a cross-tenant write puts a row in somebody else's ledger and the audit trail then
records an action attributed to an actor who could not have performed it.

Five tests, all as `security_core` — the production role, RLS in force, not
superuser:

| Test | What it attempts |
|---|---|
| `TestAWriteWithNoTenantContextIsRefused` | INSERT with no tenant context |
| `TestAWriteWithTheWrongSiteIsRefused` | INSERT with the right tenant and a site the principal is **not** assigned to |
| `TestAWriteWithTheRightScopeIsAccepted` | the same INSERT with the right tenant **and** the right site |
| `TestAnUpdateAndADeleteAreScopedToo` | UPDATE and DELETE at an unassigned site |
| `TestTheUnprotectedLedgerIsConsulted` | keeps the ledger below honest in both directions |

**Case 3 is what gives the other two their meaning.** A canary that only asserts
refusals passes on a table that is simply unwritable, and an unwritable table is what a
migration with a missing GRANT produces — which fails closed, loudly, in production, on
the first write. So the control asserts both directions, and asserts the ROW afterwards
rather than the absence of an error, because a policy that silently drops a write is
worse than one that refuses it: the caller is told it succeeded.

`isPolicyRefusal` distinguishes `42501` from a syntax error, because a canary that
passes for the wrong reason is worse than no canary — it is reported on the same line of
output as one that is working. The first version of this file failed on
`INSERT has more target columns than expressions` and reported it as "not a policy
refusal", which is the check working: it is what caught the wrong column list.

#### It found a live defect on its first run

```
CROSS-SITE WRITE REACHED jobs, and the ledger says so: NO site_isolation policy at all.
CROSS-SITE WRITE REACHED outbox_events, and the ledger says so: NO site_isolation
    policy at all -- the same shape as jobs
```

`pg_policy` confirms it: `incidents` carries `tenant_isolation` **and**
`site_isolation`; `jobs` and `outbox_events` carry only `tenant_isolation`.

The cause is in migration 0008, which applied the site policy by enumerating the
tenant-bearing relations that existed at the time. `jobs` and `outbox_events` are
site-bearing and were not in that list, so they were never given one — and migration 21
made policies RESTRICTIVE *where they existed*, which did nothing for a table that has
none.

**Two writes the pipeline makes on every event cross a site boundary with no check at
all.**

**The migration is NOT written here.** `AGENTS.md`: *"Migrations, `contracts/`,
anything touching an invariant, dependencies, auth, crypto and the rule language all
require a human to read the diff before it lands."* A cross-tenant RLS policy is
exactly that, and the reason it matters is on both tables: `site_id` is **NULLABLE** on
each, so the policy has to allow NULL — the worker that enqueues a site-less job is
legitimate, and a check that refused NULL would break it in a way that only shows up
under load.

So the finding is in `unprotectedTables` with the migration it needs, and the shape is
the third instance of a pattern this repository already uses twice — `deadexport`'s
allow-list and the unproduced-metric ledger. A **known** gap is a line somebody wrote
down and is reported on every run; an **unknown** one is a failure. Closing an entry
requires both the migration and deleting the entry, because
`TestTheUnprotectedLedgerIsConsulted` fails the moment a table in the ledger grows a
`site_isolation` policy — which is the failure direction that tells somebody to go and
delete a stale exemption rather than leave it looking like work that remains.

#### What this file does NOT claim

`writeCanaries` covers **3 of 15** site-scoped tables, and
`TestTheSiteScopedTablesAreAllCovered` logs the uncovered twelve on every run:

```
write canaries cover 3 of 15 site-scoped tables: [device_clocks evidence
user_site_assignments zones cameras inbox_events devices integrations source_events
canonical_events entities sites ai_observations]
```

A sample is a choice somebody made rather than a property anybody checked, so the
number is in the log rather than in a comment. `evidence` is the one that matters most
and is **not** covered yet: it is the only site-scoped table whose rows are raw video
bytes.

### The fuzzer found a real defect in the RTSP URL parser in three seconds

Six `Fuzz*` targets already existed and their seeds already ran in an ordinary
`go test`. Nothing had ever **fuzzed** them, and there was no `testdata/fuzz/`
corpus anywhere -- so a crash found in the field would have had nowhere to be
committed and nothing would have replayed it. Both halves of Q2 were missing: not
the targets, the *time*.

Run for 20 seconds each, all six targets. `internal/mp4`'s three found nothing.
`FuzzURLNeverCarriesCredentialsIntoItsRendering` failed in seven seconds, and then
found a second and third thing, and one of the three is a **real defect**:

```
string("rtsp://:0@::")
    the rendering "rtsp://[:]:554/" does not parse back:
    parse "rtsp://[:]:554/": invalid host: ParseAddr(":"): each colon-separated
    field must have at least one digit (at ":")
```

`url.Parse("rtsp://:0@::")` reports `Hostname() == ":"` — a single colon, which is
neither empty nor a host. `ParseURL`'s guard was `if u.Hostname() == ""`, so the URL
was **accepted**. `u.Port()` is then `""` (Go cannot read `::` as a port), so the port
fell back to 554, and `net.JoinHostPort(":", "554")` brackets a colon-bearing host:

```
net.JoinHostPort(":", "554")            ->  "[::554]"
(&url.URL{Host: "[::554]"}).String()   ->  "rtsp://[:]:554/"
```

So the product accepted an RTSP URL that it then rendered into something
`net/url` **cannot read back**. `ParseURL` now refuses a hostname containing a colon,
with the reasoning written at the check.

Red-checked, and the corpus file is the mutation's evidence:

```
--- with the host check removed:
    --- FAIL: FuzzURLNeverCarriesCredentialsIntoItsRendering
        the rendering "rtsp://[:]:554/" does not parse back: ...
--- restored ---
    ok
```

## The 2026-10-04 containerisation pass

Until this pass the product could be started and could pass its gate, and neither of
those said anything about it. There was no `Dockerfile`, no compose file and no seed
path, so the strongest available evidence that the thing worked was a sentence in
`README.md`. This is that pass, and the defects are listed first because they are the
part of it that matters.

### What was built

| Thing | What it is |
|---|---|
| `Dockerfile`, target `runtime` | `CGO_ENABLED=0`, `gcr.io/distroless/static-debian12`, four binaries: `security-core`, `securityctl`, `security-migrate`, `security-seed` |
| `Dockerfile`, target `verify` | PowerShell 7, Go, gcc, `protoc` 36.0 and `protoc-gen-go` v1.36.12, `golangci-lint` v2.14.0, `ffmpeg`, `postgresql-client` — the gate's toolchain and **no source**, so the tree is bind-mounted |
| `docker-compose.yml` | `db`, `migrate`, `seed`, `core`, `console`, and `verify` behind a profile |
| `cmd/security-seed` | The deterministic seed. Drives the **real** adapter, inbox, normalizer, graph, correlator, rule engine and incident service; only the Milestone transport is stubbed, with the fixtures the adapter's own tests use |
| `securityctl healthcheck` | The in-binary probe a shell-less image needs. `--ca`, and deliberately no `--insecure` |
| `securityctl auth code` | RFC 6238 over a base32 secret, checked against the Appendix B vectors, so a judge can sign in without installing an authenticator |

The product image is 4 binaries on a base with no shell, no package manager and no
`/etc/passwd` entry. Its healthcheck is a binary, because a container healthcheck is
a command run *inside* the container and this base has no shell to run `curl` from —
verified rather than assumed, by running `/busybox/sh` in the image and getting
`no such file or directory`.

### Five defects the Linux gate found

None of these were visible from Windows. All five are recorded here with what
triggered them, because a gate finding a defect in the check before the code is the
rule this repository keeps re-learning, and the first four are that rule being
applied to itself.

**1. A Linux-only test whose assertion had been wrong since it was written.**
`internal/lifecycle`'s `TestTheEnvironmentFileIsNotWorldReadable` asserted
`Perm() & 0o060 == 0o060` on a file `WriteUnits` writes at `0o640`. `0o640 & 0o060`
is `0o040`, so it failed on a file that was exactly the mode it wanted — and the
error said `is mode 640, want 0640`. `requirePOSIXModes` skips it on Windows, so the
only platform the product installs on is the only platform where it had never run.
Fixed to assert the mode exactly, with the mask arithmetic written down so the next
reader does not re-derive it.

**2. The coverage floors were measured on one platform and compared against another.**
`internal/covercheck/floors.json` held Windows numbers; a Linux run produced **42
findings on an unchanged tree**, each of them the tool reporting a deleted test or an
untested branch when neither had happened. The cause is real:
`internal/pipeline/disk_windows.go` has a unix sibling selected by build tag, so the
two platforms compile different statement sets out of identical source. Fixed with
`floors-linux.json`, measured by `covercheck -write-baseline` from a real Linux run
of the whole tree, selected by `$IsLinux` in `scripts/verify.ps1`. The two files can
drift and nothing enforces that both are current; that cost is stated in the file
rather than hidden.

**3. Two skips that had never been declared, because they only exist on Linux.**
`internal/evidence`'s two junction tests skip on Linux for the correct reason —
there is no junction to plant — and were undeclared, so `internal/skipaudit` failed.
Both declared with `"when": "linux"`, which the file's own rules require: a
platform-gated skip is conditional in **both** directions.

**4. `protoc` had never been asked to find a well-known type.**
`contracts/protobuf/security/v1/action.proto` imports
`google/protobuf/timestamp.proto`. The Windows protoc carries its own copy inside
its distribution directory. Debian's `protobuf-compiler` does not — those live in
`libprotobuf-dev` — so `generated-code` failed with `File not found` and reported a
missing dependency as a tree that did not match its generated output. The image now
installs the upstream release zip, pinned by SHA-256, which carries its own
`include/` and is the same `protoc` the workstation uses. Pinning the *version* also
matters: `protoc` writes its version into the generated header, so Debian's 3.21.12
would have reported a clean tree as stale.

**5. A red-check control with no test behind it.** Not a Linux defect: the seeded
demo found it. See the thirteenth row in the red-check table above — migration
`0022` widened `ai_observations` for a hosted provider, `Validate` accepted that
branch, and `insertSQL` never mentioned either column, so the second disjunct of the
attribution check was unsatisfiable and a deployment with a hosted model could not
record an honest observation without fabricating a `model_hash`.

### Three portability defects in the gate itself

`$home` is a read-only automatic variable in PowerShell and assigning to it is a
terminating error — the first version of the skills-audit home lookup used it, and
the gate did not run at all in the container. `Join-Path $repo 'dist\verification'`
produces `/repo/dist\verification` on Linux, a *filename* containing a backslash, so
every red-check control whose file was written `internal\observations\observations.go`
would have silently reported SKIPPED rather than run. And compose interpolates
`${PATH}` against the **host** before the container exists, so
`export PATH="...:${PATH}"` shipped a Linux container a PATH made of
`C:\Program Files\...`; the gate died with `cgo: C compiler "gcc" not found`, which
names neither compose nor interpolation. All three are fixed in the script rather
than worked around in a Linux fork.

### What the seed does and does not claim

It raises **8 incidents** through the product's own pipeline from the committed
fixtures replayed onto `2026-03-01T22:30:00Z` — the same instant
`securityctl rules simulate` uses, so the activation digest a human acknowledged
describes the run the seed then reproduces. Incident ids are stable because
`incidents.Service` has an injectable `IDGenerator` and the default is
`time.Now().UnixNano()`.

One observation is healthy and attributed to `seeded/absent`. Seven are **explicitly
degraded**, rotating through `no_runtime`, `timeout` and `model_unavailable`. That
asymmetry is the point: a missing AI observation is never a negative observation, and
a site where every observation is healthy would prove nothing about the invariant
while looking better. One reviewer's disposition says `agreement: disagreed` with the
observation, which is what makes `search_documents.unindexed_vector` non-empty for a
row and what gives text search something to find in a column that is matched and not
indexed.

`docker compose up` is idempotent. The seed will not reset a site it has already
reviewed, because `dispositions` is append-only and a reviewer's answer is not the
seed's to delete; a second `up` prints the same report and writes nothing, and
`docker compose down -v` is the rebuild.

### Not fixed, recorded instead

`ai_observations.temperature` was added by migration `0022`,
`internal/airuntime` fills `Observation.Temperature`, and `insertSQL` drops it — the
same defect as the fifth item above, in the third column of the same migration. It is
not a one-liner: `Observation.Temperature` is a `float64`, a provider asked for
greedy decoding legitimately reports `0`, and the column cannot distinguish
"temperature zero" from "the provider did not say". Writing `0` for the second case
is a false claim, so the fix needs a nullable representation in the Go struct, which
is a change to a published shape and therefore a human decision under `AGENTS.md`.

`incidents.first_event_at` is written as `now()` rather than the earliest supporting
event's time, so a seeded site shows all eight incidents at one instant. The column
has no `COMMENT ON` and `created_at` exists beside it, so the two statements are
close to indistinguishable. Not changed: the incident write path is invariant-2
territory and the console sorts by `created_at`, so the feed order is right.

## Two of the three were false positives, and both were in the ASSERTION

The first counterexample was `rtsp://:0@0`:

```
the rendering "rtsp://0:554/" carries the password
```

Not a defect, and the parser was right. `URL.String()` builds a fresh `url.URL`
with **no `User` field at all**, so a password cannot reach the output structurally.
The password here is the string `0` and so is the host, so the correct rendering
contains it — **as a host**. The assertion was `contains(rendering, u.Password)`, and
a password that happens to be a substring of its own host, port or path reports.
That fails in the direction that matters: every coincidence reports, the fuzzer spends
its budget on them, and a real leak among them is one more failure in a wall.

The second was `rtsp://:0@0/0@`, which failed the replacement
`contains(rendering, password+"@")` because the **path** is `/0@` — a path segment
may legitimately contain `@`.

The version that is right asks the question the property actually is: parse the
rendering back and assert it carries **no userinfo**. `net/url` distinguishes
userinfo-`@` from every other `@`, and no substring search does.

Both counterexamples are deleted from the corpus rather than left in it, because
`testdata/fuzz/` reads as "these inputs crash" and two of the three never did. The
one that stays is the regression test for the real fix, and it replays on every
`go test` in this repository with no flag.

## The `fuzz` gate, and why it is bounded

Twenty-five gates now. `fuzz` runs all six targets for `-FuzzSeconds` each — 20 by
default, raise it for a release check — and a failing input is committed under
`testdata/fuzz/` where it replays forever after.

Bounded on purpose. A gate that runs for hours is a gate that gets skipped, and this
repository has already paid for that twice in the tools it named and did not run. The
**committed corpus** is the part that does not expire with the clock; the timed run
is the part that finds new inputs. Both halves are recorded in the gate's comment
rather than left as a guess about which one matters.

### Seven of twenty-five gates have ever been recorded red

`scripts/verify.ps1` runs twenty-five gates. Every run leaves a record in
`dist/verification/` saying, per gate, whether it passed or failed. There are **40**
records. `TestAGateThatHasNeverFailedIsNamed` reads all of them and answers the
question the count of gates cannot:

```
7 of 25 declared gates have been recorded red across 40 run record(s)
NEVER RED: ai-runtime, contract-check-openapi, contract-check-protobuf,
  contract-check-schema, contract-sync, fuzz, generated-code, migration-phases,
  no-silent-skips, predicate-cov, recovery-drill, red-check, rls-canary,
  rules-lint, sql-guard, tree-state, vet
```

**Proven red: `cover-floor`, `dead-export`, `gofmt`, `lint`, `skills-audit`,
`skip-audit`, `test`.**

A gate that has never gone red is either working or is not checking anything, and the
two are indistinguishable from its output. `golangci-lint` named eleven linters for
this repository's whole life without running once, and the first run found **526
findings** — nothing about it was visible, because it printed a line and exited 0. So
"twenty-four gates, all passed" claims much less than it appears to, and this makes
the seventeen visible **by name**.

The assertion is not the log line. It is:

```
a gate that has been recorded red and is no longer declared
```

which is the direction that matters. A gate that is renamed or replaced is fine and
the record is history. A gate that is **removed because it was failing** is the
`golangci-lint` failure, and "which gates has this suite ever seen fail" is the only
place it would show up.

#### What this does not claim

That a gate on the list is broken, or that one off it is sound. This is a statement
about the **record**, and the record only covers runs somebody kept.
`red-check`'s twelve controls are each individually proved in this document, which is
a stronger claim than "the gate has gone red once" — it goes red whenever any control
stops being guarded, which is the property the gate exists for.

#### The records were partly unreadable, and that was the same defect

The reader refused eight of the forty records:

```
verify-20261002-065752.json does not parse: invalid character '\ufeff'
```

`Set-Content -Encoding UTF8` in Windows PowerShell 5.1 emits a BOM, and
`encoding/json` refuses a document that starts with one. **Eight records — including
four that caught `lint`, `test` and `dead-export` failures — were unreadable to any
strict parser**, and a reader that treated them as absent would have reported every
gate those runs saw fail as *never* having failed.

The wrong answer, in the direction that makes the check useless. The reader now
strips a BOM, and the writer no longer emits one:

```powershell
$json = $record | ConvertTo-Json -Depth 6
[System.IO.File]::WriteAllText($path, $json, (New-Object System.Text.UTF8Encoding $false))
```

### Four packages, three runs, shuffled order

A test that passes once is a sample. The `flake` gate re-runs the four packages with
concurrency, leases and timers in them **three times in one process, with the test
order shuffled every repetition**:

```
./internal/jobs      ok x3 (shuffled) -- lease-and-claim: two workers racing for one job
./internal/outbox    ok x3 (shuffled) -- lease-and-claim on the outbox relay, with a visibility timeout
./internal/pipeline  ok x3 (shuffled) -- loops against an injected clock, plus the gauge sampler
./internal/store     ok x3 (shuffled) -- the POC reference store: the same lease-and-claim shape, in a different module
```

`-count=3` re-runs each test in the **same** process, so state a package forgets
between runs is caught too. `-shuffle=on` varies the order each repetition, so a test
that depends on another having run first fails rather than passing on the repetition
where the order happened to suit it. The shuffle seed is printed on success, which is
what makes a re-run reproduce rather than re-roll.

`-race` is already on the main `test` gate. This is deliberately **not** `+2` runs of
everything: four packages, three counts, and a written reason per package is a gate
somebody runs. Twenty-five gates that take an hour are twenty-four gates.

#### The cross-module package, and the gate that caught me naming it wrongly

The POC is a **separate module** with its own `go.mod`, so
`poc/active-monitoring/internal/store` is not addressable from this module's root.
`TestEveryPackageTheGateNamesExists` — which exists to catch "a gate that names a
package that does not exist is a gate that fails for a reason unrelated to the code" —
caught exactly that:

```
the gate names "./internal/store/", which is not a directory in this repository
```

**The flake gate's own comment had named the hazard one paragraph earlier**: *"a
cross-module package in a single-module gate is a gate that silently tests less than
it says, and the failure is a green line."* The comment was right, the gate then did
the thing, and the check caught it. That is the system working, and it is also the
fourth time in this work that a comment stating a rule did not stop the code doing the
thing.

Each entry now carries its own `dir` and runs with `go test -C`, and the check
understands the qualification: a path that is not in this module is looked for under
`poc/active-monitoring/` **and only there**. One named module rather than a suffix
search anywhere, because a suffix match would make the check pass for a genuinely
wrong path — the exact defect it was written for.

#### What this did NOT do, and the run proved it

The `red-check` gate's restore failed on `internal/rules/match.go` during one run:

```
Exception calling "WriteAllText": The requested operation cannot be performed on a
file with a user-mapped section open.
```

That is the Windows mapping race the retry loop in `red-check` exists for, and after
twelve attempts the gate **stopped and said so** rather than running eleven more
controls against a mutated tree. The mutation was visible in `git status` as
`!(s.AIObservation.Degraded && false)`, restored by hand, and the next run exited 0
with the tree unmodified.

That is the designed behaviour working, and it is worth stating plainly because the
alternative — a gate that carries on after failing to restore — is the defect that
produced two un-restored controls sitting in this repository for weeks.

### The sixth unwired control: a second factor that could be moved between rows

`identity.WrapFor` and `identity.UnwrapFor` were the last two of the six controls the
dead-export ledger carried as `UNWIRED`, and the ledger said they must move **together**
or not at all — because a seal with no row binding is a capability nobody should hold.

Both are now wired, and the seal is **unbound-capable no more**.

`EnrollTOTP` used `SecretStore.Wrap`, and `verifyFactor` used `Unwrap`. Neither binds
anything, so a ciphertext copied from one `mfa_credentials` row to another opened
cleanly there: **an attacker holding the tenant's database role hands one operator's
second factor to another**, and the verification then fails in a way that looks like
the wrong six digits rather than like a moved secret.

The premise is not small. Row-level security exists so that a compromise elsewhere in
the tenant does not reach the factor column; a secret that is not row-bound is the one
column where that is true for reads and false for writes.

`credentialID` is derived from the tenant, the user, the label and four bytes of the
secret — **all of which are in hand before the row exists** — so `EnrollTOTP` can seal
with `(tenant, user, credentialID)` bound as AEAD additional data, and
`verifyFactor` opens with the same triple. A moved row now fails at the AEAD rather
than at the code comparison.

`TestASecondFactorMovedToAnotherRowDoesNotDecrypt` does the move with the **owner**
pool, because that is the attacker: the production role cannot write a factor row for
another site, which is what migration 0023 just closed.

#### Red-checked both ways, and the failures are different

```
--- EnrollTOTP seals UNBOUND again:
    --- FAIL: TestASecondFactorMovedToAnotherRowDoesNotDecrypt
        confirm the enrolment: ... not wrapped by this store, or bound to a different row

--- verifyFactor opens UNBOUND again:
    --- FAIL: TestASecondFactorMovedToAnotherRowDoesNotDecrypt
```

The first fails at **enrolment**, because an unbound seal cannot be reopened by a
bound reader at all. The second fails at **verification**, which is the actual attack.
Two mutations, two different failure points, and only one of them is the threat — a
test that only had the first would look like it covered the second.

#### Two things the change broke, both the tests telling the truth

`TestACodeUsedInOneTenantIsNotRefusedInAnother` pasted a raw `T:`-prefixed secret
into both tenants' rows so both factors would produce the same code. With the binding
in place that is refused, and the failure read `not wrapped by this store, or bound to
a different row` — **the new control working on a fixture that bypassed it.** A test
that pokes a ciphertext into a table by hand has to seal it the way the product seals
it, or it is asserting on a shape nothing produces.

`internal/command.UsageError.Unwrap` was reported dead by the same change, and it was
**correct**: it implements the standard library's `Unwrap() error` convention, which
`errors.Is` and `errors.As` call from *inside* the `errors` package, through an
interface assertion and never through a selector in this tree. The only reason it read
as live was the unrelated `s.SecretStore.Unwrap` call elsewhere in the tree — a bare
selector name satisfying any method of that name.

`dead-export` now recognises a convention method **by shape**: `Unwrap() error` on a
type that also has `Error() string`. Judged on structure rather than on a name list,
so a `Unwrap() error` on a type with no `Error()` is still gated.

**`SecretStore` no longer has an unbound half at all**, and neither does
`FileSecretStore`. An implementation that cannot bind cannot enrol a factor, which is
the correct failure, and the compiler enforces it rather than a review.

### The sixth of six, and what the ledger says now

All six controls the ledger named as `UNWIRED` are wired: `IngestScoped`,
`AuthorizeSite`, `StepUp`, `RotateCSRF`, `WrapFor`, `UnwrapFor`. Three ledgers have
now emptied the same way — `dead-export`'s allow-list for these six, the
unproduced-metric ledger's eight, and `write_canary_test.go`'s two.

### Eight controls had no transcript at all, and the gate said so

The new `tree-state` gate asserts that every red-check entry's name appears in this
document. It failed on **eight of eleven** — the three DSN controls above had
transcripts, and the other eight did not, so `scripts/verify.ps1` has been running
them mechanically while this document claimed only that they had "been run by hand".

They have now been, all eight, with the baseline recorded first because a
transcript without a baseline is a list of exit codes:

```
ok  github.com/Sergey-Bar/Ai-Captain/internal/observations  0.295s
ok  github.com/Sergey-Bar/Ai-Captain/internal/evidence      2.590s
ok  github.com/Sergey-Bar/Ai-Captain/internal/rules        1.984s
ok  github.com/Sergey-Bar/Ai-Captain/internal/dbx          0.236s
baseline exit=0   (must be 0)
```

| Control | Mutation | What went red |
|---|---|---|
| a degraded observation without a reason is refused | `case o.Degraded != DegradationNone && ...` becomes `case false && o.Degraded != ...` | `TestADegradedObservationWithNoReasonIsRefused` FAIL on **all five** degradation values: `no_runtime`, `model_unavailable`, `timeout`, `absent`, `error` — each `a "<value>" degradation with no reason validated; the reason is the only thing that says the model was never asked` |
| a non-degraded observation carrying a reason is also refused | `if o.Degraded == DegradationNone && ...` becomes `if false && o.Degraded == ...` | `TestANonDegradedObservationWithAReasonIsAlsoRefused` FAIL: `an observation that claims no degradation but carries a reason validated` |
| a traversal-shaped path segment is refused | the segment regex `^(\.\.?|:|\\|//)` becomes `^(ZZZNOTHING)` | `TestValidateRefRejectsEveryTraversalForm` FAIL: `reference ":foo" was accepted` and `reference "a/:foo" was accepted` |
| a NUL byte in a reference is refused | `strings.ContainsRune(ref, 0) {` becomes `false && strings.ContainsRune(ref, 0) {` | `TestValidateRefRejectsEveryTraversalForm` FAIL: `reference "a\x00b" was accepted` |
| a degraded observation is not evidence | `!s.AIObservation.Degraded &&` becomes `!(s.AIObservation.Degraded && false) &&` | `TestDegradedObservationIsNotEvidence` FAIL: `a degraded observation corroborated an incident; degradation is not evidence`, and `degraded reason is "none", want timeout` |
| insufficient evidence is not a negative answer | `!s.AIObservation.InsufficientEvidence` becomes `!(s.AIObservation.InsufficientEvidence && false)` | `TestInsufficientEvidenceIsNotANegativeAnswer` FAIL: `insufficient evidence was treated as corroboration` |
| a missing observation reports the absent reason, not a default | `return DegradedAbsent` becomes `return DegradedTimeout` | `TestMissingAIObservationDegradesToUnverified` FAIL: `degraded reason is "timeout", want absent` |
| a Config with no role is refused rather than attempted | `if cfg.User == "" {` becomes `if false && cfg.User == "" {` | `TestAConfigWithNoRoleIsRefusedRatherThanAttempted` FAIL: `Open returned a pool for a Config with no role. The driver resolves a missing role from the environment, which is the OS user, so this is the wrong identity` |

Restore check afterwards, over every package the batch mutated:

```
ok  github.com/Sergey-Bar/Ai-Captain/internal/observations  0.107s
ok  github.com/Sergey-Bar/Ai-Captain/internal/evidence      0.879s
ok  github.com/Sergey-Bar/Ai-Captain/internal/rules        0.604s
ok  github.com/Sergey-Bar/Ai-Captain/internal/dbx          0.108s
restore exit=0   (must be 0)
```

**A DSN that carried its own password connected as somebody else. Four new
controls, so the table holds eleven.** `cmd/security-migrate` was given
`postgres://postgres:postgres@127.0.0.1:55432/security_test` and reached the
server as the OS account, so the operator's error named a user they had never
written down:

```
security-migrate: migrate: acquire connection: failed to connect to
  `user=Sergey database=security_test`:
  127.0.0.1:55432 (127.0.0.1): failed SASL auth:
  FATAL: password authentication failed for user "Sergey" (SQLSTATE 28P01)
```

The parser was not dropping a field. Two halves failed together, and neither is
visible in the other. `Config.DSN()` never rendered `Config.User`, so
`ApplyDSN`'s carefully guarded `c.User = p.User` wrote to a field that every
rendered connection string then dropped - including the one handed to pgx, the
one in the startup log, and the one inside the `ErrNoTarget` refusal message. And
`dbx.Open` attached a role to the connection string only on the branch that had a
password to attach. A DSN carrying both, which is the only form that carries a
password when `SP_DATABASE_PASSWORD` is unset, therefore reached pgx naming no
role at all, and pgx fills a missing role from the OS account. The pre-fix
connection string was exactly:

```
postgres://db.internal:55432/security?application_name=security-core&sslmode=require
```

A missing role is not a missing credential. It is a different identity.

The fix attaches the role unconditionally, and keeps a DSN's password in an
unexported `Config` field that no rendering can print: `DSN()` omits it, and
`String()` routes every fmt verb through `DSN()`. `fmt` prints unexported struct
fields under `%v` and `%+v`, which is why that method is a control rather than a
formality - without it, a diagnostic written as `"%+v"` carries the credential
into whatever it was pasted into. `SP_DATABASE_PASSWORD` still overrides a
password embedded in the URL, so a stale URL cannot override a deliberate
variable. The refusals are intact and tested: `TestRedactedCarriesNoPassword` and
`TestTheRenderedDSNCarriesTheRoleAndNeverThePassword` between them assert that no
verb, `%+v` included, prints it.

Each of the three was run by hand with the mutation applied and watched turn the
suite red, before being put in the table:

| Control | Mutation | Result |
|---|---|---|
| a password carried in the DSN reaches the connection string | `if p.Password != ""` becomes `if false && p.Password != ""` in `ApplyDSN` | `TestADSNPasswordReachesTheConnectionString` FAIL: `the connection string carries password "" (present: false), want the one the DSN carried` |
| the configured role reaches the connection string | `if c.User != ""` becomes `if c.User != "" && false` in `renderDSN` | `TestTheRoleReachesTheConnectionStringWithNoPasswordAtAll` FAIL, and the failure printed the whole pre-fix connection string with no role in it |
| a password held by Config cannot be printed out of it | delete `func (c Config) String() string` | `TestTheRenderedDSNCarriesTheRoleAndNeverThePassword` FAIL: `%+v printed the database password` and `%v printed the database password` |
| a hosted observation is attributed without inventing a hash | `o.ModelIdentity, o.ModelContractVersion` in the `Record` argument list becomes `"", ""` | `TestAHostedObservationIsAttributedByIdentityAndContractVersion` FAIL: `new row for relation "ai_observations" violates check constraint "ai_observations_attribution_ck" (SQLSTATE 23514)` |

The fourth row is the thirteenth control, and it is the only one in the table that
was found by the seeded demo rather than by reading the code. Migration `0022`
widened `ai_observations` for a hosted provider — no weights to hash, so attribution
by provider name plus this build's contract version — and its own header states the
Go layer "is already verified against this shape". `Validate` was: the field
comments describe the branch in detail and the check was written for it. `Record`
was not: `insertSQL` never mentioned either column, so the second disjunct of
`ai_observations_attribution_ck` was unsatisfiable and every row had to carry a
`model_hash`. For a hosted provider the only available hash is one nobody can
compute, so the configuration the column was added for could not record a single
honest observation.

The mutation is the two bound **values**, not the two column names. Deleting the
columns from the statement also turns the test red, but on `INSERT has more
expressions than target columns` — which proves the statement and its argument list
are tied together and says nothing about attribution. Blanking the values keeps the
SQL valid and the row unattributed, so the red is the check constraint firing on a
row the Go layer had already blessed. The first attempt used the column-deletion
mutation and was replaced for exactly that reason; a red that does not name the
defect teaches the reader nothing about the defect.

One defect in the same area is **not** fixed and **not** red-checked, and is recorded
here rather than left for somebody to find:
`ai_observations.temperature` was added by migration `0022` as well,
`internal/airuntime` fills `Observation.Temperature`, and `insertSQL` drops it. So
every observation in every deployment has a NULL temperature and the column's own
comment — "a replay that does not record it cannot reproduce which one it is
comparing against" — is not true of this build. Fixing it is not a one-liner:
`Observation.Temperature` is a `float64`, and a provider asked for greedy decoding
legitimately reports `0`, so the column cannot distinguish "temperature zero" from
"the provider did not say", and writing `0` for the second case is a false claim.
It needs a nullable representation in the Go struct, which is a change to a
published shape and therefore a human decision under `AGENTS.md`'s rules.

### The caller side: three more, in the POC module's own table

The eleven controls above all remove a guard from inside the pipeline. These three
remove a NAME or a VALUE from `demoEnv` — a slice of `NAME=value` strings in
`cmd/pocctl` that `pocctl demo` hands to a child process. They are in
`poc/active-monitoring/scripts/verify.ps1`, which is the POC module's table, and
they guard the one defect class neither table could see: a required variable added
to `internal/config` does not reach a hand-built slice, **every test in
`internal/config` stays green**, and the demo refuses to start at step 6 of 7 —
after two builds, after the fault rehearsal, and after the push target is
listening. The schema does not know the slice exists, so nothing in the schema
could catch it. Only running the command did.

`config.RequiredUnder` is what makes the slice checkable: the enumeration `Load`
already performs, exposed so a caller assembling its OWN environment can be held
against the schema rather than against a comment.

Baseline before the batch, and after the restore: `go test -count=1 ./cmd/pocctl/`
**exit 0** in both. Each control was then applied by hand, in the file, and watched
go red:

| Control | Mutation | Result |
|---|---|---|
| the demo environment names every variable the schema requires | `"SP_POC_DET_MODEL=" + model,` becomes `"SP_POC_DET_MODEL_RENAMED_BY_REDCHECK=" + model,` | exit 1 — `TestTheDemoEnvironmentIsOneTheDemoOwnChildWouldAccept` and `TestTheScoreChainInTheDemoAscends` FAIL |
| the score chain in the demo environment ascends | `"SP_POC_DET_SCORE=0.005",` becomes `"SP_POC_DET_SCORE=0.5",` | exit 1 — the same two FAIL. `config.Validate` refuses an inverted chain by name, so the demo dies in `watch` |
| the banner describes the trigger the demo actually runs | `case soak.TriggerDetector:` becomes `case "not-a-mode":` | exit 1 — `TestTheBannerNamesTheShippedTrigger` FAIL |

The first two mutations rename and re-value rather than delete, deliberately. A
deleted line is a compile error, which this gate reads as a red for the wrong
reason — the control is not what went red, the compiler is.

**A first attempt at this batch was recorded as proved and was void.** The
transcripts were taken with `go test -C <dir> -count=1`, and `-C` is only
recognised as the *first* argument; every run in the batch — baseline included —
exited 2 on the same flag-parsing error. A control table whose entries are all
proved by a run that never started is precisely the failure `AGENTS.md` names, so
the batch was re-run with a real `exit 0` baseline recorded above it. The table is
only worth what its baseline is worth.

The gate used to be a subset of what it claimed. Three tools it named were not
installed and their absence was described rather than fixed, so `lint` and
`-race` were decorative: the config had enabled eleven linters since the
repository's second commit and none of them had ever run. The first run of
golangci-lint found **526 findings**, and among them three real defects:

- **`security-platform.env` was written world-readable (0644)** while carrying
  `SP_DATABASE_URL` and `SP_SECRET_KEY` - and the template the same function
  emits says in its own text "keep this file mode 0640". A preserved env file is
  chmod'ed on re-install too, so an upgrade fixes a file an older installer left
  open.
- **The outbox backoff raised on its own failure path.** `least(300, 5 *
  power(2, attempt_count)::int)` casts an unbounded exponent to int32 *before*
  the clamp; at attempt_count 31 the cast raises, the failure path errors, and the
  row is left claimed by a worker that has already moved on. A raising backoff
  turns a bounded retry into a lost message.
- **A seal that failed was logged nowhere.** `Scheduler.Run` discarded the error
  from every pass, so a sealer that failed every time - a key that would not
  load, a database that went away - produced no log line, no counter and no
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
| A - Contracts and substrate | 1-5 | green |
| B - Adapter and fixtures | 8, 9 | green |
| C - Durability | 10-13 | green |
| D - Graph, rules, verification, incidents | 14-20 | green |
| E - Identity, API, console | 21-24 | green |
| F - AI observation plane | 25-29 | green except the inference engine (see below) |
| G - Threat model, release, lifecycle, observability, DR | 30-35 | green |
| H - The runnable core service | 36-39 | green |
| I - Camera capture and proof | POC 1-8 | green, with a named detector and a named gap (see below) |

No command in the tree refuses as unimplemented. The one remaining guard in the
code is the audit endpoint's answer when no audit writer is configured, which is a
defence rather than a stub.

### The camera capture path, and `securityctl poc`

Four packages and one command, all pure Go with no cgo, no system dependency and
no new module requirement. The whole thing builds and runs on a machine with no
CUDA and no GStreamer, which is the point: it installs into air-gapped sites.

| Package | What it is | State |
|---|---|---|
| `internal/rtsp` | RTSP 1.0 receiver, TCP interleaved, RFC 6184 depacketisation | green |
| `internal/mp4` | Annex-B reader, avcC builder, fragmented-MP4 muxer, GOP pre-roll ring | green |
| `internal/detector` | always-on trigger: a score and a reason, never a verdict | green |
| `securityctl poc` | stream to stored hashed clip to raised incident, on one machine | green |

What one command does, and what it prints:

```
securityctl poc --camera rtsp://CAMERA/stream --record capture.h264
```

It prints a banner naming the camera, the transport, the resolution, the frame
rate, the clock, the detector and its threshold, and the clip caps - with no
blanks, because every field is either a value or a labelled absence. It then
prints a live novelty score per picture. On the first trigger it assembles a
bounded clip with lead-in, stores it by content hash, reads it back to prove the
bytes match, and raises an incident.

**The clip contains the cause.** A trigger does not begin recording; it arms the
recorder, and recording begins at the next IDR, by which point the ring holds
every complete group since the last eviction - including the group the trigger
fired inside. `TestPreRollContainsTheEventThatTriggeredIt` and
`TestPocTakesAStreamToAStoredHashedClipAndARaisedIncident` are the assertions, and
the second one red-checks the first.

**There is no AI plane in this build.** Every incident `poc` raises is therefore
decided *without* an observation, and says so on the banner, in the output, and in
the artefact it writes. The three absences - no observation, degraded observation,
insufficient evidence - are three named booleans with a reason each, not one
nullable field, and `TestPocNeverPrintsAMissingObservationAsNothingHappened`
red-checks that the command's output contains none of the phrases a reader scans
for ("nothing happened", "all clear", "no activity"). It is the behaviour under
the three-absences rule, exercised on every run.

#### What is built and what is not, stated plainly

| Plan item | State |
|---|---|
| RTSP handshake, TCP interleaved, Annex-B out | done |
| NVDEC decode behind a build tag | **not done** - out of Stage 1, and the AI plane is not needed to prove the capture path |
| CPU frame differencing as the reference detector | done, tested, and the better of the two detectors |
| The detector the live path actually runs | `coded-size`, not frame differencing - see below |
| GOP pre-roll ring, =5 s | done |
| Annex-B to AVCC, avcC, fragmented MP4 | done, validated with `ffprobe` |
| `internal/evidence` store, `storage_ref`, SHA-256 | done, with a read-back proof |
| A degraded observation, `no_runtime` | done, shaped by the same call the writer makes |
| `securityctl poc`, no database | done |
| `poc --record`, committed fixture corpus | done; `testdata/poc/testsrc.h264` is a synthetic lavfi pattern, so it carries no licence and nobody's likeness |

**The detector is named, and it is not the one the plan preferred.** The plan
calls for frame differencing over decoded frames. Nothing here decodes H.264, and
the live path therefore runs the compressed-domain reference: it measures each
picture's size against a running baseline of its own recent sizes. It is a real
motion signal, it costs no GPU, and it is not the same thing - it cannot tell a
person from a shadow, and its threshold will not suit a camera with analogue
noise in the dark. `internal/detector` has both implementations behind one
interface so that a site which can decode swaps them without touching anything
upstream, and both carry the name of the implementation that produced a score on
every reading. Nothing in the product presents them as equivalent.

**What the public camera is remains open.** The plan's question - which camera,
reachable from this network, genuinely RTSP, and under what licence - is still
unanswered here, and `poc --camera` is written against it. What is not open is
whether the client works: `TestCaptureReproducesTheStreamByteForByte` drives the
real client over a real socket against a server that repacketises the committed
fixture into RTP, and recovers every NAL unit byte for byte. A build that only
works against a live camera cannot have its failures told apart from the
camera's, so the fixture is the development path and the camera is the
demonstration.

**Verified here:** `scripts/verify.ps1` exit 0 with `-race`, `golangci-lint` at
0 issues, and the muxer tests additionally run `ffprobe` against every clip they
produce - reading each one back with a decoder that shares no code with the
writer. Six fuzz targets cover the Annex-B reader, the picture builder, the SPS
parser, the RTSP response parser, the RFC 6184 depacketiser and the URL parser.
The SPS fuzzer found a real defect during development: H.264 sets no maximum
frame size, and a six-byte parameter set can legitimately describe a 32x16400
picture that no decoder will size. The muxer now refuses one, by name, where the
number is still a number.

### The second AI provider, and the seam around it

`airuntime.Provider` is a two-method interface over the `Request`/`Result`
vocabulary that already existed, and `internal/hafnia` implements it. The plan's
own criterion for the refactor - "the existing tests must pass untouched, and
that is the evidence the refactor was honest" - is met: `internal/airuntime`'s
suite passes with no change to it.

The client is fully built and fully tested **without a key**, which is the plan's
�0b point rather than a workaround:

| Item | State |
|---|---|
| `POST /assets`, `POST /chat/completions`, `DELETE /assets/{id}` | built, fixture-tested |
| Region pinning, EU and US, closed set, no cross-region failover | built, red-checked |
| Egress hardening: redirects refused, TLS 1.2 floor, closed host allow-list | built |
| Per-site daily query cap, exhaustion a recorded first-class state | built, red-checked |
| Asset deletion on every path after upload, plus a sweep for the residue | built, red-checked |
| Strict output parser: parse, never guess; a malformed answer degrades | built |
| Fault matrix: 429, 500, 401, malformed body, two choices, prose, empty content | built |
| Region and cap as *distinct* Degradations | **not done - see below** |

#### The attribution fix, and the one part of it that is blocked

`observations.Validate` accepted an observation without a `ModelHash`, and its own
comment said *"a default here is a silent downgrade."* A hosted VLM gives a model
identifier, a response and no weights to hash, so filling that column would mean
either a lie about the schema or a fabricated hash.

It now carries three honest fields - `model_identity` (what the API reported, as
a claim), `model_contract_version` (this build's content hash over prompt, prompt
version, output schema version and API model name) and `temperature` - and
`Validate` accepts an empty `ModelHash` **only** when the first two are present.
`TestTheAttributionNamesTheModelWithoutInventingAHash` red-checks the
fabricated-hash variant.

**The blocked half:** `ai_observations.model_hash` is `NOT NULL` in the schema and
in migration `0004`. Storing a Hafnia observation honestly therefore needs a
migration making the column nullable and adding the three new columns, plus the
protobuf and JSON Schema surfaces. That is a contract and migration change, which
`AGENTS.md` reserves for a human to read before it lands, and it is not made here.
Until it is, the Go layer is correct and the storage cannot yet hold the honest
record - which is a stated gap, not a working around one.

#### The Degradation vocabulary is closed, and that cost something

`observations.Degradations` is a **closed set pinned by a schema CHECK**. The
plan's P3.2 (the pinned region was refused) and P3.4 (the cap was reached) are two
first-class states the plan wants to be *distinguishable*, and neither is in the
vocabulary. Extending it is the same contract change as above.

So the *category* is shared and the *cause* is not: every degraded result carries
a `reason=<code>` field in a fixed key=value shape, with the vocabulary the
repository's own rule demands. The codes are `cap_reached`, `region_refused`,
`region_unavailable`, `answer_malformed`, `asset_missing`, `redirect_refused`,
`timeout`, `error`. The matrix test asserts the categories differ, and it is the
assertion that goes red the day someone extends the vocabulary.

#### The neutrality invariant, in three tests

- **Only the composition root imports the vendor package.** The plan says "no
  package outside internal/milestone imports it", and taken literally that is
  wrong in a way that would force the wiring to be impossible: the binaries
  construct the adapter and the pipeline is handed one. The exemption is a named
  list, so adding a fourth caller is a deliberate edit to a test.
- **The four new packages name no vendor.** Matched against source text with
  comments stripped, so a package may explain *why* it is vendor-neutral and may
  not put a vendor's name in a string literal or an import.
- **The vendor surface is at most five methods.** "Three small methods" is an
  adjective until something counts them.

All three are red-checked. The second is red-checked against a string literal
specifically, not a comment, because that is the case that would actually be a
dependency.

### Defects this work found, and what each one would have cost

Named because a defect with a name gets looked for again, and one without gets
rediscovered as a mystery.

| Found | Cost had it shipped |
|---|---|
| `rules simulate` declared its flags twice — the tree parsed them and the Run got an empty list | `--bundle` was silently ignored, so the **activation digest was computed for the default bundle** while the operator reviewed a candidate. The gate agreed, because the report was internally consistent about the wrong file. |
| The evidence store's containment check read `ModeSymlink`, which Windows does not set for a directory junction | `Open` **read files outside the evidence root** on Windows. The one test that would have caught it skipped on Windows. |
| `ReplayTransport` computed the scenario epoch and discarded it on `_ = epoch` | `observed_at` stayed at the capture instant, so **every fixture test of a time window was measuring the capture date** and passing for the wrong reason. |
| The rebase's `at.In(parsed.Location())` resolved through the host's zone database | A vendor clock declaring `+02:00` was recorded as `+03:00` — a fact about the capture that depended on the machine doing the replay. |
| FU-A reassembly read the indicator's *type* instead of `nal_ref_idc` | Every keyframe stopped being a keyframe. Nothing failed; the muxer wrote the file, ffprobe read it, and it was a clip that could not be seeked or pre-rolled. |
| `AlreadyQueried` compared an empty `Degraded` against `"none"` | Every observation looked like a failure, so nothing ever counted as asked and the product **paid for every window on every event**, silently. |
| The detector's baseline absorbed keyframes | The change that started a motion sequence was normalised away and the trigger stayed quiet through the event it exists to catch. |
| The soak's stub deadlocked calling a locked helper | The harness hung; a soak that hangs proves nothing and is not re-run. |
| A path check said the verdict was unreachable when nothing was reachable, a contradiction, and a 404, all the same | An operator is sent to look at a base URL that is fine, or to file a vendor bug that is a typo in a config file. |

**And a repo-wide encoding repair.** Thirty-two files carried double-encoded
UTF-8 in their comments and user-facing strings, which is how this tree had
`a`-accented mojibake standing in for an em dash. It is repaired, and the gate
runs after the repair to prove 32 files still build, vet, lint and pass. The
cause was a build script using the platform's default encoding rather than
stating UTF-8, and the fix is that the files are now clean rather than that the
symptom was hidden.

### The 2026-10-02 capture-path pass

One subsystem was taken through hygiene, typing and hot-path work: `internal/rtsp`,
`internal/mp4`, `internal/detector` and `securityctl poc`. Every existing test in
those four trees passes **unchanged** — the refactor criterion the plan sets for
this kind of work — and two behaviour changes came with a new test each, both run
once with the control removed and watched go red.

| Found | Cost had it shipped |
|---|---|
| A recorder refusal ended `poc` with `nil`, and the `strings.HasPrefix(stopReason, "the recorder")` branch meant to catch it was unreachable behind an early `return` | A mid-stream parameter-set change exited **"refused: the run produced no report"** — a statement about the reporting, on a capture that had already stopped. `TestPocReportsAStreamTheRecorderRefused` asserts `ExitError` and the absence of that sentence. |
| `parseSDP` used `PayloadType == 0` as the "no video media section" sentinel | A camera offering payload type 0 — a legal RTP value — was refused for a media section that was present. `TestAPayloadTypeOfZeroIsNotMistakenForNoVideo` covers both halves: type 0 is accepted, and a description with no video section is still refused. |
| `Recorder.write` measured a sample with `len(AVCC(p.NALs))`, then `appendSample` built it again | Every frame in every clip was assembled and discarded once before being assembled for real. `AVCCLen` is the measurement; `AVCC` is the construction. |
| `firstMBInSlice` called `rbsp`, which copies the whole NAL unit to read one Exp-Golomb value | An allocation per slice, on a path that runs for the life of the process. The builder now hands the same scratch buffer back. |
| `fragmenter.appendSample` routed the sample through `boxWriter.box`, which takes an already-built payload | Each frame was copied into a scratch buffer and then copied again into the file. The `mdat` header is now written in place. |
| `parseSPS` ran four times per clip — on every `Geometry()` call, in `newFragmenter`, and in `Finish` | The banner's `Geometry` re-parsed the parameter set on every poll, and a clip's own width and height came from a parse separate from the one that built the avcC. It is parsed once when the SPS is stored; `Finish` refuses loudly rather than reporting zeroes. |

Measured over the committed fixture, 20 iterations, before and after on the same
machine: the read-and-group path went from 248,320 B and 981 allocations per pass
to 203,026 B and 866, and the full capture path (read, build pictures, record,
mux) from 1,069,930 B and 5,745 to 859,417 B and 5,238. Wall time moved less than
the noise on this fixture and is not claimed.

Smaller items, each with its reason rather than a diff: `Options.MaxNALBytes`
carried a copy of `Transport`'s doc comment; `AnnexBReader.Next`'s contract said
the returned slice was reader-owned and allocation-free, which was never true and
is now what the code does; `ReadPacket` allocated a buffer per RTP packet and now
reuses one, with the aliasing contract written down; the session `state` is a
`State` with three constants rather than three string literals; `KeepAlive`'s
`nilerr` suppression described a branch that returned `nil` either way; the poc
carried a `pocRun` type, a `finish` receiver and a `rec` parameter that were all
read by nobody, and the report's detector name now comes from the detector that
ran rather than from the frame source. `detector.Decimate`'s saturation clamp was
provably unreachable — every term is at most 255, so the quotient is — and is
replaced by the argument that makes the narrowing safe.

`Observed.Luma` now documents that a decoder reusing one output buffer must hand
over a copy, because a reference frame that aliases the current frame compares
equal to itself and a detector that can never fire looks exactly like a quiet
scene. Nothing decodes yet, so it is a contract written down rather than a bug
fixed.

**The two control characters named below are fixed, and the class is now gated.**
`internal/inbox/inbox.go:160` read `pp.site_ids` where it means `app.site_ids` —
a `0x07` byte where the `a` of `app` belonged — and `internal/milestone/epoch_test.go:136`
had the same byte before `t.In(`, so the sentence read `t.In(parsed.Location())`
rather than `time.Time.In(...)`. Both were comments; neither was a functional bug,
and both are invisible in a diff because the byte is not printable.
`internal/testutil/encoding_test.go` now fails the build on any C0 control
character in a `.go`, `.md` or `.sql` file outside a declared allowlist, so the next
one is a test failure rather than a line somebody notices while grepping.

**Evidence for this round:** `scripts/verify.ps1` exit 0 on this machine —
contract-check on all three surfaces, contract-sync, vet, gofmt, `golangci-lint`
at 0 issues, the generated-code check, `go test -race -p 1 -count=1 ./...`,
migration-phases, sql-guard, rules-lint and the 61-test Python runtime. It
printed its own warning that `SP_TEST_DATABASE_URL` was not set, because no
Postgres is reachable here, so the database-backed suites **skipped**: the
recovery drill and the five RLS canary queries. That is missing evidence, not a
pass, and it is the same gap the 2026-09-29 run above does not have. The muxer
tests did run: `ffprobe` is on `PATH` and read every clip back.

**Evidence for the 2026-10-02 route-audit round:** `scripts/verify.ps1` exit 0 on
this machine, **with a database**. All fifteen gates, race detector on, cgo on,
`golangci-lint` at 0 issues, the generated-code check against `protoc`, and the
61-test Python runtime. Unlike the round above, `SP_TEST_DATABASE_URL` and
`SP_TEST_DATABASE_PASSWORD` were set and `sp-pg` was running, so the database-backed
suites executed rather than skipped: the recovery drill, the RLS canary, and the
pipeline's poll / consume / dead-letter suites all ran.

The script also wrote itself a record this time, to
`dist/verification/verify-<timestamp>.json` — the gate list with a per-gate outcome
and duration, tool versions, the commit, whether the database variables were set, and
a `verified` boolean that is false whenever anything was skipped or failed. Two
things it says out loud rather than leaving to be inferred:

- `provenance.kind` is `local-run` and `provenance.automated` is `false`. **There is
  no CI in this repository**: Actions cannot start a job on this account, and a green
  branch here is a human claim. The record's own `provenance.ci` field says so in
  words, so it cannot be quoted as automation.
- `excluded` names `poc/`, Sonar and GitHub Actions. No gate in that script reaches
  the POC, because it is a second Go module.

The record also preserved what the console scrolled past: the local toolchain is
**go1.27.0** while `go.mod` declares `go 1.26.0` and the removed CI pinned
`GO_VERSION: "1.26"`. The build passes — a newer toolchain than the directive is not
the same thing as a pin below it — but the drift is recorded rather than noticed.

The POC's own gate was run separately and is green
(`poc/active-monitoring`, every package, no database needed).

A second pass followed on the same day, from the same audit: the documented
`source` form is now parsed, so a real event is attributable (above), and the two
incident gauges the AI-plane SLO reads are now written (above). Both red-checked.
`scripts/verify.ps1` was re-run afterwards: exit 0, fifteen gates, no gate skipped,
`verified: true`.

A third pass, from the same audit's H1: the event-type GUID is now resolved against
the site's own `/eventTypes` answer (above). `scripts/verify.ps1` exit 0, **fifteen
gates**, race detector on, database present, record at
`dist/verification/verify-20261002-115034.json`, `verified: true`,
`provenance.kind: local-run`. Two changes to existing code came with it and both are
recorded where they happened rather than only here: `pagination_test.go`'s stub now
pages **per route** instead of on one process-wide counter, because the adapter now
makes one read ahead of the feeds and a shared counter meant the order of requests
decided which page each collection got — the test was asserting pagination through
a fixture that had accidentally coupled it to request ordering. And `ProbeResult`
gained a `sample` field, so a probe of `/eventTypes` reports the vendor's actual
response body instead of an item count; without it the one route whose purpose is
to be learned from a live site reported the only thing the documents already say.

A fourth pass recorded the two E1 signatures in
[`docs/roadmap/master-roadmap.md`](../roadmap/master-roadmap.md): M1's exit gate
gains **gate 5**, the event→incident funnel per camera with the ratio published in
the release notes, and **M2 is re-baselined from ~46 to 12 engineer-weeks** with a
named 19 ew float and four named cuts. Gate 5's arithmetic is the reason it exists:
the designed load is 1.73M events a day, the North Star is Human Attention Minutes
per 1,000 security events, and no document stated what fraction of events becomes an
incident — a number that is load-bearing for pricing, packaging and fit. Gate 4
already produced it as a byproduct nobody had to write down.

The same pass moved four other things the roadmap had wrong: ONVIF Profile M back
into M4 (in XProtect it comes from a third-party appliance, so a Milestone site has
none of it), M4's second adapter selected for analytics-metadata richness rather
than pipeline count, M5's multi-site correlation from last to second-built and
last-cut, and licence-plate recognition out of M4 into an M3 item blocked on the
compliance track. `scripts/verify.ps1` was re-run afterwards: exit 0, fifteen gates,
no gate skipped, `verified: true`,
`dist/verification/verify-20261002-120501.json`.

A fifth pass made the site trip executable.
[`docs/vendor/milestone/design-partner-runbook.md`](../vendor/milestone/design-partner-runbook.md)
is the whole of H1's remaining evidence as three commands, and `Query.Params` plus
`securityctl adapter-probe --param` exist so the questions it asks can be asked
*of a server*. This API has no machine-readable specification — EVENTS' OpenAPI and
JSON downloads both 404 — so "the documented filter is honoured" is a fact about a
build of Milestone and not about a page. Hand-typing a curl at a customer's box is
what `adapter-probe` exists to replace.

That pass also found and corrected a false claim that had been sitting in nine
documents: the command is `securityctl adapter-probe`, and every one of those
references said `adapter probe` — including four that asserted the command **does
not exist**. It has existed since it was written. Q-4 and F-2 in the roadmap's risk
registers are marked resolved rather than deleted, with the finding they missed
written down: the references were wrong in two ways at once and nothing noticed,
because nothing checked. That is the defect `release-sre-and-support.md` §8 already
predicted — a hand-maintained list of commands cannot catch a wrong command name.

`scripts/verify.ps1`: exit 0, **seventeen gates**, nothing skipped, `verified: true`,
`dist/verification/verify-20261002-152932.json`, `database.urlSet: true`.

> **This and every other count above is a dated record, not a claim about today.** The
> paragraphs from here to the end of the run log describe the 2026-10-02 round and are
> accurate about it. The gate count has moved a long way since — it is **twenty-three**
> now, and the Verification block at the top of this file is the one that tracks it,
> because `internal/statuscheck` derives it from `scripts/verify.ps1` and fails when
> the two disagree. Rewriting the historical numbers would make the log lie about the
> runs it records, which is the same defect as the nine documents that said
> `adapter probe` does not exist.

**And one thing about that count that belongs here rather than in a commit message.**
The gate list was fifteen when this work started and is seventeen now. The two that
appeared in between — `red-check` and `skills-audit` — were added to
`scripts/verify.ps1` by another session working in this same directory between
12:05 and 13:57 UTC, and were swept into the previous commit by a single
`git add -A` that was told to commit the whole tree. They are good gates and both
pass, and the commit says nothing about them. The risk was named before that commit
was made and it happened anyway: **a whole-tree commit cannot tell whose work it
carries**, and a commit message that describes only the author's own changes is
then incomplete in a way no reviewer can see.

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
installed, upgraded to a second version, and rolled back - with the pre-upgrade
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

**What a model adds:** a `run_inference` body. Everything around it - verification,
attribution, validation, degradation, the reason vocabulary - is written and
tested.

## What is not done, and why

### The hackathon plan, and where it stands

Stated plainly, because a plan that reads as complete when it is not costs the
entry more than one that does not.

| Phase | State |
|---|---|
| **Stage 1 - the POC** | **green.** RTSP, detector, pre-roll, muxer, evidence, degraded observation, `poc`, `--record` |
| **Phase 0 - unblock** | P0.1, P0.3, P0.5 (the probe), P0.6, P0.7 (already fixed), P0.9 done. P0.2, P0.4 need a key and an instance. P0.8 (the clock *caller*) and P0.10 (labelled footage) not done |
| **Phase 1 - the seams** | **green.** P1.1 bookmark + clock + capabilities, P1.2 the provider interface, P1.3 the neutrality invariant |
| **Phase 2 - capture** | P2.1 (TCP interleaved; **UDP not done**), P2.2, P2.3 (CPU reference), P2.4, P2.5 (gaps named, drift widened or refused), P2.6, P2.7 (a real hard-killed child), P2.8 (idempotency, no column) done |
| **Phase 3 - the VLM** | Client **green** and key-free. P3.2 and P3.4 degrade to a shared category - see above. P3.1 unverified against a live endpoint |
| **Phase 4 - spine and consoles** | P4.1, P4.2, P4.3 done. P4.4 the bookmark decision, pre-roll, drift correction and idempotency **built and tested; not yet dispatched from a live incident**. P4.5 shadow run, P4.6 console, P4.7 systemd not done |
| **Phase 5 - soak and acceptance** | P5.1 the harness and its eight criteria **done**, P5.4 the fault-injection cases **done**, P5.3 the acceptance report **done** and runnable. No 7-day run, no 24-hour rehearsal, no measured false-positive rate |

### The vendor seam, and the three additions

P1.1 is **optional interfaces**, not three more methods on `Transport`, and that
is the whole of the plan's "optional so `ReplayTransport` does not break": a
fixture transport has no clock to sample and no bookmark to place, and making
those methods mandatory would force every fixture to grow a fake implementation
of a system it stands in for.

| Surface | What it does | State |
|---|---|---|
| `BookmarkWriter` | places a pointer into the vendor's own recording | built, tested |
| `ClockProbe` | samples the vendor's clock with its round trip | built, tested |
| `CapabilityReporter` / `Probe` | reports what a deployment can do | built, tested |

Three properties the tests pin rather than the code assuming:

- **A bookmark is a pointer, not a copy of the footage.** The test asserts the
  body carries a time, a label and a device reference and nothing that looks like
  video, because "we do not duplicate an archive" is a claim about the wire and
  this is where it is made.
- **A 200 with no bookmark id is not a success.** "The call returned 200" and "a
  bookmark exists" are different claims, and a bookmark that silently did not
  land is an operator who follows the incident and finds nothing.
- **The drift correction's sign.** Half the round trip is added to the reading
  and subtracted from our own later instant. Getting the sign wrong reports a
  healthy link as drifting by half its own latency; the test is red-checked in
  all three directions.

The clock and bookmark routes stay `Unverified`. A passing unit test is not the
plan's P0.5 probe, and a test asserts they have not been quietly promoted.

### The general profile, and the rule that must fail closed

P4.1 is one general profile, `general-monitoring`, v1, **shadow** - one profile
whose only structured claims are "something departed from normal" and "a hazard is
visible", with the concept itself carried by the operator's question in the
prompt. A profile with a field per concept is a schema per concept, which is the
rule engineering the plan set out to eliminate, so the test asserts the field set
is small *and* that none of its fields names a detection concept.

It is shadow because it has never been measured against the 200 labelled cases
the plan requires, and a profile allowed to create incidents before that has a
false-positive rate that is a guess.

**P4.3 is now enforced in the schema, and it is the plan's red-check.**
`observations.Validate` accepts a degraded observation **only** when it carries
its reason:

```go
case o.Degraded != DegradationNone && strings.TrimSpace(o.DegradedDetail) == "":
    return fmt.Errorf("%w: a degraded observation must carry its reason; without it a reader "+
        "cannot tell 'the model was never asked' from 'the model answered and the answer was unusable', "+
        "which is the difference between an absence and a negative", ErrInvalid)
```

The mirror case is enforced too: a non-degraded observation carrying a reason is
refused, because a reason attached to a row that claims to be fine is either a
field that was not cleared or a reason attached to the wrong row.

Remove the check and `TestADegradedObservationWithNoReasonIsRefused` is the only
assertion in the tree that fails. That is the definition of a control that works.

### Video frames are untrusted input, and what bounds an injection

`docs/threat-model.md` has a new section, because the capture path made the
boundary exist. The control is **not a filter** - nothing strips what an injection
asked for, because a client that quietly dropped the undeclared fields would
validate a *different* answer from the one the model gave, and the writer's
refusal would never see what it exists to refuse. The bounds are:

| | |
|---|---|
| The model holds no tools | Red-checked: adding `CallTool` to `airuntime.Provider` is a **compile error**, not a silent capability |
| Output is validated at the storage boundary | `observations.Writer` refuses any field the profile's schema does not declare |
| Nothing matches prose | The profile schema documents this on `summary` itself; a test rejects a reason that names a conclusion |
| The profile cannot interrupt an operator | Shadow until measured, asserted in the registry |

The residual is stated plainly rather than left in the gap list: a determined
attacker holding a printed page can still cause false positives. The blast
radius is bounded at "a human gets interrupted", and that is a decision with an
owner rather than an unrecorded one.

**The two things that block the most, and neither is ordinary work.** The
observation schema's `model_hash NOT NULL` and the closed `Degradation` CHECK both
need a contract and migration change. `AGENTS.md` reserves those for a human, so
they are **written up and not applied**: `docs/proposals/hosted-model-attribution.md`
carries the exact SQL, the exact `CHECK` that makes an invented hash impossible
rather than discouraged, and the one naming question worth deciding before the
SQL. The Go layer is written and tested against the shape they propose; only the
storage cannot yet hold it.

**The rest of the list is ordinary work that has not been done yet**, and nothing
on it is blocked by a decision: P0.8 (the clock *caller* — the probe samples the
clock, but nothing invokes it from a running service), P0.10 (labelled footage),
P2.1's UDP half, P4.4's dispatch from a live incident, P4.5's shadow run, P4.6's
console, and the 7-day and 24-hour runs — which are time, not engineering.

### `securityctl acceptance` — the criteria, runnable

The thirteen criteria are in the plan as a list, and a list is checked by hand or
not at all. `securityctl acceptance` checks what this build can check and reports
the rest as **NOT RUN** with what each one needs:

```
criteria: 7 pass, 0 fail, 6 not runnable in this build
```

Three states, never two. A criterion that cannot be evidenced here is reported
as not-run rather than omitted, because an omitted criterion reads as a passed
one, and the six that need a key, a live VMS or a camera are exactly the six a
judge would otherwise have to take on trust. A test asserts the count is
thirteen, that no number appears twice, and that every not-run says both what
would be needed and what *is* built.

### The soak, the chaos rehearsal, and the probe

`securityctl soak` runs the eight published criteria against a stub provider and
reports them in order, failures first. The harness **records** and `Criteria`
**reads**: nothing in it decides whether it passed, and every criterion is proven
capable of failing by a test that constructs a run violating it. The cost
criterion is the one that matters most, and it is exact — 200 events at three
attempts each make 200 provider calls, because a retry must not pay.

`securityctl soak --chaos` runs four fault-injection cases plus a fifth for the
evidence volume. The kill case is the one worth reading: seven events completed,
thirteen are visibly pending, and none of them is claimed handled.

`securityctl adapter-probe` reaches every route and reports
confirmed / contradicted / unreachable / skipped, with the field names the vendor
actually returned. It **never** promotes a route's `Unverified` flag — a probe
that marked its own homework would make the flag a claim the tool makes about
itself — and it prints how many routes a human could promote and why that is
their decision.

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

**The Milestone endpoint surface is unverified, and was also wrong.** Every route in
`internal/milestone/routes.go` is marked `Unverified` and the fixtures are
synthetic. No Milestone server has ever been reachable from this codebase. This
is the single most important open item: everything else is proven against a
fixture, and the one component that has not touched the real thing is the one
that talks to the customer.

It was also wrong in a way that was checkable, because **Milestone's own published
documents were committed in this repository the whole time** and nobody had read
them against the route table. They are now in [`docs/vendor/milestone/`](vendor/milestone/README.md),
and [`docs/vendor/milestone/route-audit.md`](vendor/milestone/route-audit.md)
gives every route a verdict with a line citation. The result:

| Finding | Status |
|---|---|
| The path prefix was `/api/v1`. The documented root is `/api/rest/v1`. **Every call would have 404'd**, and a feed that 404s is a site that looks quiet | Corrected, in one constant (`milestone.APIPrefix`) |
| Bookmarks were read with `GET /Bookmarks`. The vendor says in its own words that bookmarks *"cannot be retrieved by use a GET"*, and the documented read is `POST /bookmarks?task=searchTime` with a search body | Corrected, over a new optional `BookmarkSearcher` |
| The bookmark write-back sent `bookmarks`/`idempotencyKey`/`deviceRef`/`time`/`label`. **None of those five field names is in the published schema**, and the two the vendor requires — `header`, `devicePath` — were missing | Corrected, and the required field set is asserted by a test |
| A successful bookmark write was parsed for an `id` the published answer does not contain; it answers `{"array":[{…,"reference":…}]}` | Corrected |
| The feed cursor was `?since=`/`?limit=`/`?cursor=` and a `Next-Cursor` header. **None of the four is a parameter this API has**; the documented spellings are `time=gt:'…'`, `size` and `_links.next`. Every poll asked the same unfiltered question and got the oldest stored events first, from the start of the retention window | Corrected. `Next-Cursor` is still honoured, and both mechanisms are recorded rather than one chosen silently |
| Both documented answers wrap their entries in `array`, which the item decoder did not know. A corrected path would have answered with a body the adapter refused to read | Corrected |
| `Priority`, `TimeSpan`, `ImagePath` and `VideoRecordingStartTime` were assumed event fields. **They do not exist in this API** — they are the older XProtect Event Manager shape | Removed from the assumption; recorded in audit §3 |
| `type` on an event is a **GUID**, not a name, so no event type normalises yet | **Half open.** The Configuration API route (`GET /eventTypes`) is declared and the site's own name for a GUID is resolved per site and carried on the record, the envelope and the dead-letter reason. The alias table from a vendor name to a canonical type is **still reserved to a human** — see below |
| Four routes (`System/UserGroups`, `Alarms`, `ResourceGroups`, `System/Time`) and one name (`Servlets`) appear in **neither** document | **Open.** `NOT-ANSWERABLE-FROM-DOCS`: absence from a document is not evidence of absence from the product |
| A live clock endpoint does not exist in either document, so the clock-drift gate has no working input | **Open.** Recorded in audit §2.7 with a candidate (the HTTP `Date` header) |
| A tenth route, `GET /eventTypes`, was added. The documents name the resource twice (`events-rest-v1.reference.md:40`, `:42`) and **never show a body**, so the route declares no response `Expect` at all | **Declared, `Unverified`.** Audit §2.8. The route-audit gate caught it the moment it was declared — that is the gate working, not a gap in it |

`internal/milestone/route_audit_test.go` is the gate. It fails if a route has no
verdict in the audit, if a verdict names a route the code no longer calls, if the
code and the audit's documented form disagree, and if the vendor documents stop
containing what the adapter sends. Red-checked: reverting one route path fails
five of them.

**What this did not do, deliberately.** A published document can refute an
assumption; it cannot confirm one. **No `Unverified` flag was cleared.** Nothing
here is a probe, and `securityctl adapter-probe` against a real installation plus
a human reading the artifact is still the only thing that can promote a route.

### The event-type GUID now has a name attached, resolved from the site itself

The half of H1 that did not need a decision has landed.

**What was wrong.** `type` on a documented event is a GUID
(`events-rest-v1.reference.md:289-297`, `:380-388`), so every event from a real
server normalised to `unknown.<guid>`, was flagged `Malformed`, and was
dead-lettered with a string no operator can look up. Nothing was lost — the event
is stored and the reason is visible — but the feed could not work.

**What landed.** `GET /api/rest/v1/eventTypes` is declared
(`routes.go`, audit §1 row 10, `Unverified` like everything else) and read once per
poll. The site's **own** name for each GUID is resolved per `(tenant, site)`,
carried on `RawRecord.SourceTypeName`, quoted in the dead-letter reason, and
written into the stored envelope as `_resolvedEventTypeName` so the consume loop
re-derives the same type weeks later (invariant 2). A GUID the site did not answer
for is **unresolved, never defaulted**.

**The trap this had to avoid.** The GUIDs are **per-site configuration**, not
global constants — `481d4d6c-…` is an instance in one customer's XProtect and the
same concept carries a different GUID at the next site. A compiled-in `guid ->
motion` table would work perfectly at the design partner and silently dead-letter
every event at customer two, invisible until there was a customer two, and
unreachable by any test using the design partner's bytes. The mapping is therefore
name-based and resolved from each site's own answer;
`TestTwoSitesWithDifferentGUIDsResolveToTheirOwnNames` is the assertion that fails
if anyone replaces it with a table. Red-checked: a process-wide vocabulary, which
is what a compiled-in table degenerates into, fails it and four other tests.

**What is still a human's, and is not writable yet.** Mapping a vendor's name onto
a canonical type changes `rules.KnownEventTypes` and the published rule-bundle
vocabulary, which `AGENTS.md` reserves. It is also **blocked on evidence the code
cannot produce**: the names have to be a real site's, and no Milestone server has
ever answered a request from this codebase. The sequence is in audit §5 and
[`handover.md`](vendor/milestone/handover.md) — probe the site, read the
`/eventTypes` row's `sample` (the probe now reports the first item's body, not
only an item count), then decide on **names** rather than GUIDs.

`normalizeEventType` and `rules.KnownMilestoneEventTypes` are **unchanged**. The
resolved name is an input to the existing normalizer, so a site whose names happen
to be spellings the platform already knows starts working with no table at all, and
one whose are not dead-letters exactly as before — with the vendor's own string in
the reason instead of an opaque GUID.

### A documented event is now attributable, and the one that failed silently is the one that mattered

The audit found three things hanging off the GUID problem. Two needed a vocabulary
decision. **The third did not, and it was the worst of the three**, so it is now
closed:

`SourceDeviceRef` was empty for every documented event. `sourceDeviceRef` looked for
a bracketed id in `Source` — the shape `"Entrance Camera 01 [CAM-001]"`, which
appears in no Milestone document — while the documented value is
`cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c` (EVENTS:291, bare `{id}` also
allowed at :409). So every event from a real server arrived with **no device
attached**. Nothing crashed, nothing was flagged, nothing was logged: zone rules and
device-filtered rules simply stopped matching. That is indistinguishable from a site
where nothing is happening, which is the worst way for a defect to present.

It is now parsed in both documented forms, returning the identifier without the
collection prefix — the thing `devices.source_device_id` holds and
`resolveDevice` matches on. Red-checked: disabling the parse fails three tests.
`adapter_test.go` now feeds the vendor's own sample body
(`events-rest-v1.reference.md:380-388`) through the parser, which is the only test
in that package that can fail if the parser is wrong about the real thing; every
other test there feeds a body in the assumed shape, and a test whose fixture agrees
with the code under test about a value the vendor contradicts is a test of nothing.

A second test pins the blast radius: the *only* thing a documented event cannot do
is name its type. When H1 is decided, that is the test to change, and it fails
loudly if a second gap appears beside it.

### Ten of twenty-seven metrics had no producer

`metrics.go` says its names are the plan's contract, "because a dashboard somebody
hand-writes against a name this package invents is a dashboard that quietly stops
updating." Ten of the twenty-seven were written by nothing anywhere in the tree. A
dashboard built against one of them shows a flat line, which reads as *this has
never happened* rather than *nothing is measuring this*.

One of them was load-bearing and I had just made it so: **`ai_plane_availability`
reads `GaugeIncidentDegraded`**, so with no producer the objective reported "not
measured" on every install and never once had an opinion. A pilot could not measure
how often the AI path was actually live, which is the single number the
`incidents.degraded` column exists to provide.

Two were free and are now written — `SampleGauges` reads both in one query,
`count(*) FILTER (WHERE state IN ('open','in_review'))` and
`count(*) FILTER (WHERE degraded)`. "Open" counts an incident in review as well as
one nobody has touched, because both consume operator attention and a gauge that
falls when somebody picks a case up reads as the backlog shrinking. The remaining
eight are in `internal/observability/metric_producers_test.go` with a reason each,
and every declared metric must now have a producer or a line in that ledger. Three
of the eight are one line away: `CounterAuditAppends` (the writer counts none of
the rows it appends), `CounterOutboxDelivered` (`DrainOnce` already returns that
count) and `CounterRuleBundleActivated`.

That test found two I had missed by hand — which is the argument for it, and it is
the same argument as `sqlguard` and `rlsrole`: a class of defect that is invisible
in a diff needs a gate, and a list is better than nothing but a list that is checked
is better than a list.

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
5. **NUL bytes are not in the `storage_ref` CHECK** - Postgres rejects NUL in
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
    all, which it did not have: every graph read the product performs - `Snapshot`,
    `Walk`, `CheckRebuild` - was failing with "permission denied for view
    edges_active" under the role the product runs as.
13. **The correlator's candidate window is bounded by a constant, not by the
    bundle.** A bundle is untrusted input; one asking for an hour-long window
    would otherwise turn every event into a scan of every other event. The clamp
    is reported rather than silent, because the alternative is a site whose
    incidents are quietly under-corroborated with nothing to explain it.
14. **`ai_runtime` is never bound to a non-loopback address without
    `SP_AI_ALLOW_REMOTE`.** `runtime.py` has no authentication of any kind, so
    anything that can reach the socket can read the site's evidence.

### The decision path, which was the worst of it

- **One bundle warning stopped every incident.** The compiler warns about things a
  rule author has *decided* - a `proceed_without` fallback, an `ifAIAvailable`
  with no AI to apply to - and both are `SeverityWarning` and both are endorsed by
  the semantic validator. `activeBundle` refused any bundle with any problem, so
  one deliberate warning made `decide` fail for every event. The whole
  AI-degradation path was a bundle-killer, and the bundle had already passed
  `Store` and `Activate`.
- **A match implied an incident.** The gate was `RuleID != ""`, so a rule with
  `then: {suppress: true}` - whose entire purpose is to *prevent* an incident -
  created one, at the default severity `medium`. `internal/simulate` already
  modelled it correctly (`IncidentRaised: d.Action.CreateIncident`), so the digest
  an operator acknowledged said "no incident" for a bundle that raised one in
  production.
- **`graph.Walk` scanned `node_id` and `edge_id` into one destination.** Every
  returned node id was an edge id, and traversing from a returned id found
  nothing. `EdgeIDs` was a list of empty strings and the dedupe never fired.
- **An incident with no `corroborate` clause had no linked events.** The trigger
  is carried in a different field from the supporting list, and only the
  supporting list was walked. Replay then ran the rule against an empty event
  type - a structural mismatch, not a bundle property.
- **Replay lent the incident's observation to every event.** An event with no
  observation of its own was handed one belonging to a different event, so the
  replay cited evidence the rule never matched on, and a trigger with no real
  observation looked corroborated.
- **A degradation reason was concatenated with prose.** `degraded_reason` is
  CHECK-constrained to a closed set and the engine switches on the exact string,
  so `model_unavailable: the VLM worker exited 137` fell through to `default` and
  was recorded as `absent`. An operator triaging an outage was told the model was
  never asked. The schema already had a `degraded_detail` column; only the
  in-memory struct was collapsing them.
- **Schedule conditions were dead in production.** Three callers read a schedule
  and each answered differently; the pipeline supplied no resolver, so every
  schedule rule in the active bundle never fired. The evaluation context also
  hardcoded UTC, so a site in UTC+3 had its business hours evaluated three hours
  early. One resolver now, in `rules`, with the site's location.
- **A lifecycle transition was audited as a creation**, and the state was read
  outside the transaction that wrote it.

### The gate that catches the class, not the instance

Three adversarial reviews were run against this repository in one day. Between
them they found, independently, five statements that were the same defect:

> **a mutation whose WHERE clause did not constrain the thing that grants
> permission to make it.**

`outbox.deliver` and `markFailed` wrote with `WHERE outbox_id = $1` and no
lease owner. `inbox.Fail` and `Complete` matched on the key alone. `SetState`
read the state on a separate connection and wrote with no guard on what it read.
Three separate agents found them by reading carefully. `internal/sqlguard` finds
them by looking, and it is a gate:

- every mutation of a tenant-scoped table names its `tenant_id`;
- every write to a leased table names its `lease_owner`;
- an append-only table is only ever inserted into.

**The table facts are derived from the migrations**, not from a hand-maintained
list, so a table added tomorrow is covered today. That matters more than it
sounds: a guard with a hand-written list quietly stops covering the newest table
and keeps reporting green, which is the exact condition it was written to remove.

**A legitimate exception needs a reason on the statement**, in the form
`sqlguard:exempt <rule> - why`, and every exemption is printed on every run. A
rule that cannot be excepted gets switched off; a rule whose exceptions are
invisible gets trusted. Seven exist today and each is one of three shapes: a
claim, which grants the lease rather than writing under one; a reclaim of a row
whose worker died; and an operator replaying a dead letter.

Building it found four real cross-tenant write surfaces - `outbox.deliver`,
`outbox.markFailed`, `outbox.Replay`, and four statements in `jobs` - that set a
tenant context in the pool and then did not reference the tenant in the
statement. They were safe only because an RLS policy three layers down happened
to filter them, which is a property of a different file and not something a
reader of the statement can see.

The gate is red-checked: with the lease constraint removed from `inbox.Fail`, it
reports the statement.

## Defects the 2026-09-28 and 2026-09-29 sweeps found

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
  applies a backoff, and the claim itself retires a row whose budget is spent -
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
  deleted - only a new session row was inserted - so one intercepted challenge id
  minted sessions for its whole two-minute TTL. It is now consumed in the same
  transaction as the session insert.
- **The second factor had no brute-force control.** The password step counts
  failures and locks the account; the second-factor step did neither, and the
  route behind it is CSRF-exempt. Migration 0016 adds a per-challenge attempt
  counter. It is deliberately *not* an account counter: the password already
  succeeded, so counting the second factor against the account would lock an
  operator out for mistyping six digits.
- **A TOTP code could be used twice.** A code is valid for a 30-second step plus
  the -1 window, so it stays acceptable for about 90 seconds, and nothing
  recorded that it had been used. RFC 6238 -5.2 says a verifier must not accept
  the same OTP code twice. Migration 0017 adds a per-factor consumed-step cache
  and the record is the arbiter - an insert that loses the race refuses, so two
  concurrent presentations of one code produce one session, not two. The key is
  `(tenant, factor, step, code digest)` rather than the step alone, because this
  codebase accepts both SHA-1 and SHA-256 authenticators and the two produce
  *different* digits for the same step; keying on the step would break MFA for
  every authenticator that ignores `algorithm=` in the enrolment URI. A replay
  returns the same answer as a wrong code, so it is not an oracle. This was not
  in the sweep's item list; it is a real defect the TOTP work surfaced.
- **Anyone holding `identity.manage` could re-key an account.** `SetPassword`
  skipped the current-password check whenever `recovery_required` was set - and
  `EnsureUser` set that flag on every account it ever created, so the bypass was
  the normal state of every provisioned user rather than an edge case. The flag
  now means what its name says: this account has never had a password chosen by
  its owner. A provisioned account is created with a NULL `password_hash` rather
  than a random placeholder nobody keeps, which is the case the flag was
  standing in for. The audit action distinguishes an ordinary password change
  from a recovery, which it used to write as `identity.recovery.performed`
  either way - one row in a hash-chained ledger for two different events.
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
  `SP_BEHIND_PROXY` is set - empty list plus behind-proxy refuses to start, the
  same rule as the cookie and TLS refusals - and the address is the rightmost
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
  entirely and printed a warning - on the two commands whose entire purpose is to
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
  `os.WriteFile`, in both `internal/release` and `internal/lifecycle` - and
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
  is now real Ed25519 verification, pure-Python RFC 8032, standard library only -
  the air-gap supply-chain decision forbids a dependency. The tests include the
  RFC vectors and were mutation-tested six ways, including a revert to the old
  code.

### Correctness

- **The gate had no existence test.** `AGENTS.md` and this file both name
  `scripts/verify.ps1` as the thing to trust, and nothing asserted it exists, that
  every package it names resolves, or that it checks all three contract surfaces.
  Delete it and the suite stayed green. There is now a test that fails if it goes.
- **A diagnostic on stderr aborted the whole gate.** `verify.ps1` ran with
  `$ErrorActionPreference = 'Stop'`, so a tool that printed a warning - which
  PowerShell turns into a terminating error - killed the script before its exit
  code was read. The first two gates never wrote to stderr, so it went unnoticed
  until the OpenAPI gate had a warning to give.
- **A metric named a class of thing that does not exist.** `actions_dispatched_total`
  counted the relay acknowledging an outbox message it then logged and discarded;
  in M1 no action has physical impact, so a dashboard built on it would report a
  site taking physical action. It is now `outbox_messages_delivered_total`, and a
  test names the old one so it cannot come back by accident.

- **Recall could not fail.** `DetectedRestricted` counted every case the profile
  flagged, including benign ones, so one true positive and twenty false ones gave
  a recall of 21.0 - and the gate that decides M1 exit gate 4 is "at least 0.70".
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
  It said "no incident raised" and then raised one, correctly - `unverified` is a
  first-class verification state, indexed as one. The log line was wrong, not the
  pipeline.

### Two counters that were not counters

- **The password lockout bounded nothing under parallelism.**
  `PasswordStep` takes `FOR UPDATE` in a transaction that commits - the argon2
  verification has to happen outside it or a deliberately slow hash serialises
  every login on the site - and `registerFailure` then computed
  `attempts := user.FailedAttempts + 1` in Go from that already-stale value and
  wrote the result. Red-checked: eight concurrent wrong passwords left the
  counter at **1** and the account unlocked. It is the named control for the
  CSRF-exempt login route. The increment is now `failed_attempts + 1` in SQL with
  the value read back.
- **The MFA budget bounded nothing under parallelism either.** It was read in the
  committed transaction, compared, and incremented separately on failure.
  Red-checked: **8 of 8** concurrent wrong codes reached the factor check against
  a budget of 5. A challenge id is documented in that package as "a two-minute
  bearer of 'the password was right'", so this was the only thing bounding a
  six-digit code. The budget is now *claimed* - one statement where the increment
  and the ceiling are the same predicate.

### Trust, one more time

- **`build-release` generated a signing key when the path did not exist**, with no
  message, and then printed the matching `--trusted-key` - actively telling the
  operator to adopt it. A release signed by a key nobody holds the offline half
  of is verifiable by anybody who can read the archive, every archive signed by
  the real key stops verifying, and a typo in a path on a build host does all of
  that silently and successfully. It refuses now.
- **The audit feed's `chainValid` did not verify the chain.** It compared
  `items[i].PrevHash` to `items[i-1].Hash`: two stored columns side by side, so
  an edited row verified fine, and the first row was never checked, so a
  single-row page - which is what `?fromSequence=<n>&limit=1` produces - always
  reported true. The comment said it was recomputed. It now calls
  `audit_verify_chain`, the same function a checkpoint seal makes.
- **Visiting the console root logged the operator out.** `Resolve` rotates the
  session token on every call and `handleConsoleRoot` discarded the new value; the
  browser kept the old one and the next request bounced to `/login`.
  `s.principal` sends the rotated cookie back, and this one handler was the
  exception.

### Encoding

335 comment lines carried em-dashes and typographic quotes that had been through
several lossy encoding round-trips, and one string literal had become a Unicode
control character that `staticcheck` caught. The unambiguous sequences were
decoded; the rest, which had been through more than one pass and could not be
recovered reliably, are now plain hyphens in comments. No operator-facing string
literal was touched.

### Migrations

Schema version is now 24 — the compatibility window in `migrations/compat.json`.

The newest migration is `0024_search_documents.expand.sql`: a
`GENERATED ALWAYS AS ... STORED` `tsvector` over `verification_results.explanation`,
a GIN index over it, and a `search_documents` view unioning the deterministic
explanation with a reviewer's note and the model's output. **It indexes the
deterministic half only**, and that is structural rather than a shortcut: a generated
column is computed from the row it is on, so a vector over `verification_results`
cannot see a `dispositions` row. The other two are matched and not indexed — a
**partial index**, stated rather than hidden — and denormalising them is a contract
step because it adds a table somebody has to keep correct.

The deterministic half is the right one to index first anyway: it is the text an
operator can reproduce from stored facts, so a hit on it is citable.

Indexing over the `english` configuration is a decision with a named cost: it stems
and drops stop words, and it assumes English at sites whose operators do not all read
it. `simple` would be language-neutral and would stem nothing, so a plural query would
not find its singular, which an operator describes as the search being broken. **That
decision is not taken here**; it belongs beside D-01 in `docs/decisions.md`.

### The migration before it

Schema version was 23 — the compatibility window in `migrations/compat.json`,
and `0023_site_isolation_for_every_site_bearing_table.expand.sql`,
which creates a `site_isolation` policy on **every** site-bearing relation that
lacks one, DERIVED from the catalogue rather than enumerated: migration 0008 listed
nine table names, and every site-bearing table created afterwards had no policy at
all. The write canary found the first two (`jobs` and `outbox_events`, both written
on every event); the catalogue said eight, plus twenty-four monthly partitions whose
direct access bypasses the parent's policy.

`user_site_assignments` is the one deliberate exclusion, and it is the interesting
one: **scoping the table that DEFINES the site scope by the site scope is
circular.** Reading a principal's assignments needs `app.site_ids`; `app.site_ids`
comes from reading the assignments. The policy is therefore useless as a control —
it answers "can this principal see this assignment?", which is true for every row a
login would ask about — while making the login itself unresolvable.
`TestRolesAndScopeAreReadableUnderRLS` caught it, and it caught it the only way it
could: the assignment read came back empty and the test said *"the site assignment
was not read"* rather than "a policy refused".

Adding the policy to `sites` did break one path, which is the "the Go side has to be
right first" risk this migration's header names. `identity.BootstrapAdmin` bound a
tenant context and read the site it was about to assign an administrator to; with
`sites` site-scoped and no principal yet, that read returns zero rows and bootstrap
reports *"site %q does not exist"* for a site that plainly does.
`dbx.TenantContext` now carries an explicit `System` flag for a transaction that
needs a named tenant AND the worker exemption, and bootstrap sets it. Setting the GUC
by hand would also have worked; it is a field because a caller reaching past the type
to set it is a caller that does not know it needed to.

### The migration before it

Schema version was 22 — the compatibility window in `migrations/compat.json`,
and the newest migration in `migrations/` was
`0022_observation_hosted_attribution.expand.sql`, which adds `model_identity`,
`model_contract_version` and `temperature`, drops `model_hash`'s `NOT NULL`, and
substitutes `ai_observations_attribution_ck` so that an observation must still say
which model answered — either by the hash of the weights, or by naming the provider
and the contract version this build pinned. **It is a substitution, not a
relaxation**: half an attribution (a provider name with no pinned contract
version) is still refused, and the red-check in
`internal/observations/attribution_test.go` removes the constraint and asserts the
refusal stops. The migration is expand-only, with no contract step bundled, so the
window may widen immediately — a binary predating 0022 reads a database with an
extra nullable column and ignores it.
**That sentence is a test.** `migrations/proposal_migration_test.go` fails the build
if `compat.json` and the migrations on disk disagree, and if this number and
`compat.json` disagree, because a stale version number in the repository's source
of truth is a defect that costs a deploy to discover. This paragraph is where it
went stale before: it read "Schema version is now 17" for four migrations after it
stopped being true, and nothing could have told anyone.

- **`audit_verify_chain` reported every sub-range as broken.** It seeded the
  running previous-hash with the genesis hash regardless of `p_from`, so the
  first row it examined was compared against a genesis it does not have. Every
  call with `p_from > 1` returned `valid = false, broken_at = p_from`, pointing
  at the start of the range rather than at a real break. The tests only ever
  verified from 1.

  That is invariant 4's detector, and `audit_checkpoints` seals ranges, so this
  is the function that verifies a seal. A verifier that fails closed on an
  untouched ledger cannot distinguish tampering from its own bug; an operator told
  their ledger is broken learns to ignore the output, which is the same as having
  no verifier. Migration 0018 seeds from the row before `p_from` and refuses
  outright when that row is missing, because a missing link and a broken chain
  call for different responses from the operator.
- **`Worker.Fail`'s backoff raised on its own failure path**, the defect already
  fixed in the inbox and the outbox and missed in jobs: the exponent was cast to
  int32 before the clamp, so it raised at attempt 29, the row stayed leased, and
  the worker aborted on every pass. Red-checked at attempt 28.
- **`Migration.IsValid` never looked at the SQL.** It checked that the phase
  string was one of three known values, so a `DROP COLUMN` in a file named
  `.expand.sql` passed validation and was applied. It now rejects contract DDL
  outside a `.contract.sql` file.
- **`jobs.max_attempts` had no upper bound**, so a bound above 28 was accepted
  and reached the raising arithmetic. 0018 adds a ceiling.
- **`inbox.Writer.Fail` and `Complete` matched on the key alone**, so a worker
  whose lease had lapsed could flip a row another worker was actively processing
  to `dead_letter`. Both match the lease owner now. Red-checked: without the
  guard the stale worker dead-letters the in-flight row.

- **`VerifyChecksums` compared only the checksum.** A renamed migration or a
  rewritten phase passed. It now compares the applied row's name and phase as
  well, and the duplicate pre-DDL call in `Up` is gone. The checksum *format* is
  unchanged: changing it would make every deployed site refuse to upgrade, and
  nothing in this repository can `UPDATE schema_migrations`, so the re-record
  could not be delivered.

  This paragraph used to end "Schema version is now 17", which was true when
  written and had been read by nobody since. The number now lives at the top of
  the Migrations section, where it is checked rather than remembered.

  The gate is real: it caught an edit to an already-applied migration during
  this sweep, and the correct response - reset the test database - is what was
  done. That is the control working, not the control being inconvenient.

- **`internal/clocksub` had no tests, and the drift measurement it owns is the
  input to every time-windowed corroboration decision.** A device 400 days out
  wrapped the int32 conversion and was recorded as 200,261,632ms - a positive
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

### Site isolation exists in the database

For this product's whole life the two RLS policies on every tenant table were
both PERMISSIVE. PostgreSQL ORs multiple permissive policies for the same
command, so

    tenant_isolation OR site_isolation  ==  tenant_isolation

There was no site isolation at the row level. Migration 0008's comment says "so
the two concerns compose: tenant first, then site", which is AND; the DDL
implemented OR. Confirmed directly against the catalogue rather than inferred
from the migration text:

```sql
SELECT polname, polpermissive FROM pg_policy WHERE polrelid = 'evidence'::regclass;
--  site_isolation      permissive = true
--  tenant_isolation    permissive = true
```

Every site check in the product was a handler doing it by hand, and a handler
that forgot one was a cross-site read with no error anywhere. Migration 0021
recreates every `site_isolation` policy as RESTRICTIVE, so the effective
predicate is tenant AND site. `ALTER POLICY` only supports `RENAME TO`, so each
is dropped and recreated carrying its original USING expression and role list
over verbatim, and a policy whose role list is anything but PUBLIC is refused
rather than guessed — recreating it with different roles would be a privilege
change disguised as a scope change.

The change is fail-closed, and that shaped how it landed. A statement that
reaches a site-scoped table without naming a site now sees nothing: right
direction for a security control, dangerous for availability, because a consume
loop that forgot its site would ingest nothing and say nothing. The Go side was
therefore changed first, and the whole suite was run against the flipped policy
before the migration was considered done.

**The RLS canary found four real gaps that nothing else could.** The inbox write,
the normalizer's insert, the backlog count, and the lease key not carrying its
site. Every other test in the tree connects as the superuser, which bypasses RLS
entirely, so the whole ingest path ran green for as long as the policy did not
consult the site. That is the argument for a canary: without one, a fail-closed
policy has no failure to find.

It also surfaced a latent bug that had been invisible and would have broken
every site-scoped read the moment the policy started consulting `app.site_ids`:
`encodeSiteIDs` emitted a bare `a,b`, and `app_current_site_ids` casts that to
`text[]`, so every read would have failed with "malformed array literal". It
emits a quoted array literal now, and elements containing a comma, a quote or a
backslash are escaped rather than silently changing the list's length.

The terminal writes in `jobs` and `outbox` run as the system worker rather than
narrowing to a site, and the reasoning is in the code: they are guarded on
`lease_owner`, and holding one row's lease is a strictly stronger predicate than
holding a whole site. A caller without the lease matches zero rows exactly as it
did before.

`internal/rlsrole/site_isolation_test.go` asserts the policy is RESTRICTIVE, that
the two compose as AND under the production role (own site sees the row, another
site in the same tenant sees nothing, no site sees nothing), that every
request-path read of a site-scoped table binds a scope, and that every service
that touches one can narrow itself. The last two are static checks over the tree,
so a path added tomorrow is covered by construction.

### The Ed25519 verifier had a forgery, and it was not the cofactor

`runtime.py` verified `a` using the un-cofactored equation `[S]B == R + [k]A`,
justified by a comment that misattributed the choice to the RFC's reference
implementation. It is now cofactored, and it requires BOTH equations — which is
strictly smaller than either, because multiplication by the cofactor is a group
homomorphism, so the plain equation implies the cofactored one and never the
reverse, and checking only the cofactored form accepts a superset of what it
should.

The cofactor was not the hole, and the red-check the plan asked for is
therefore impossible for the equation change alone: no input exists that the
cofactored form rejects and the plain one accepts, and the agent building it
proved the direction rather than asserting it.

The hole was a small-order public key. For an order-8 key, `[k]A` is the
identity for every `k`, so both equations are satisfied and any signature
verifies. Two cases are now refused outright — the identity element as a public
key, and an order-8 key with a zero signature — and both were confirmed to
return `True` against the pre-change code. The suite is 61 tests, from 49.
### Still open

- **A site with a stale `migrations/compat.json` can lie about its own core
  version.** `lifecycle.Verify` compares the manifest's core window against the
  build's *declared* window, because nothing in the tree carries the running
  binary's version. A site whose compat file claims a core version it does not
  run is not caught by that comparison, and the honest fix is for the build to
  stamp its own version into the compat file at package time.

- **A rollback onto a version whose window excludes this core is refused by the
  code and asserted, but is not yet driven end to end through the installer
  against a real archive.** Install and upgrade themselves are now covered by
  `cmd/securityctl/install_upgrade_test.go`.

- **Most of the suite connects as the superuser, which bypasses RLS.** Two
  packages (`rlsrole`, `schemalaws`) run as the production role, and they are
  what found the four site-isolation gaps. Every security-relevant statement
  should eventually have a canary in that form, and the order matters: the ones
  that write, first, because a write that cannot see its site is an ingest
  outage and a read that cannot is a blank console.

- **The verifier search is not finished.** Three adversarial reviews in one day
  found ninety defects between them. The two gates added afterwards —
  `internal/sqlguard` for the mutation class, `internal/rlsrole` for the scope
  class — exist because the classes recurred rather than because the instances
  did. The classes they do not cover (a counter computed in Go from a stale
  read, a test that accepts the failure it should exclude, an exported symbol
  with no reader) are still found by reading. A fourth review is not a plan; it
  is a habit.

- **Migration 0010 drops a column inside a file named `.expand.sql`.** The
  validator rejects that shape going forward and the file is grandfathered by
  name with a written reason, because 0010 is applied on deployed databases and
  the rule is forward-only. Correcting it means an ADR and a new migration.

## Defects the runnable-core work found

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
- **The AI runtime endpoint could not be called with the version a caller
  actually sends.** `apiVersion` is unmarshalled as an integer and asserted
  against `runtime.py`'s own `!=` comparison, so a client sending the string
  `"1"` gets a 400 that reads like the runtime is broken.
- **The client address was the caller-asserted one.** `X-Forwarded-For` was read
  leftmost when `SP_BEHIND_PROXY=true`, and every header was believed from any
  peer. That value is the audit `remote` field and the key a rate limiter
  buckets on, so both were keyed on whatever the caller wrote. The effective
  client is now the rightmost entry that is not a configured trusted proxy, and a
  peer outside the trusted list has its header ignored entirely.
- **`SP_TRUSTED_PROXY_CIDRS` is now required whenever `SP_BEHIND_PROXY=true`**
  - a comma-separated CIDR list, no default - and the process refuses to start
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

`security-migrate` used to default to a silent local DSN when none was given. It
refuses now, and names both ways to name a database rather than guessing one.

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
