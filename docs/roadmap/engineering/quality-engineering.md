# Quality Engineering

Owner: E2 (Engineering). Status: living. Last revised 2026-09-28.

The gate commands are in `AGENTS.md` and `Makefile`. Everything in this document
is written against the tests that exist in the tree, named by function, and
against the suite as `make ci` actually runs it.

---

## 1. The quality thesis

### 1.1 What a defect in this product is

The product's own claim is that every decision it made is provable: reproducible
from stored facts, attributable to a model version and an input hash, hash-chained
into a ledger, and sealed to a signed offline checkpoint. That is not a feature
list, it is a claim about the truth of records, and a buyer in this market is
being paid to check exactly that.

So:

> **A defect in verification, replay, evidence handling, audit or the identity
> model is not a bug. It is a falsification of the product claim, and it is
> graded as if the company's central assertion had been shown false.**

That grading rule has consequences that are unusual for a QA document, and they
are the point:

- A falsification is a Sev-1 by default, regardless of how few customers are
  affected, and regardless of whether anybody noticed. Severity here is not
  measured in users; it is measured in whether the claim still holds.
- A falsification discovered before a customer discovers it is a success, and it
  is bought with the same money as a feature: it is budgeted in the milestone and
  named in the work item's `test_that_fails` field.
- The evidence that a control works is not "we wrote it" and not "we reviewed
  it". It is **a test that has been seen to fail when the control is removed.**
  This is the standard `docs/status.md` already uses for the word "green", and it
  is the only standard in this document.

### 1.2 Quality incident vs product incident

Two different things, and conflating them is how a security product ends up with
a bug tracker full of noise and a history of one thing that mattered.

| | **Product incident** | **Quality incident** |
|---|---|---|
| What happened | The product behaved in a way that affected a customer's security operation or their trust in a record | The build, the test suite, the release process or the development practice is defective |
| Examples | A missed real intrusion. A false alarm campaign. A wrong PTZ move. An unverifiable audit row. A lost accepted event. A degraded path presented as a working one | A `kill -9` stage with no test. A golden test that is not byte-stable. An invariant with no test that fails when the control is removed. A test that skips in CI and reads as a pass. A migration that would be applied to a live site with no rehearsal. A CI toolchain that cannot build the module |
| Lives in | the incident tracker, keyed to a site and a time | the engineering tracker, keyed to a commit and a milestone |
| Severity scale | §10 | §10, but the mapping is different: a quality incident that would have caught a Sev-1 is itself a Sev-1 |
| Who owns the response | E1, with the customer | E2, with the milestone |
| Closes when | the customer is told, the correction is made, the record is complete | the test exists, fails when the control is removed, and is in `make ci` |

The rule that keeps them from blurring: **a product incident is a missing test
plus a customer; a quality incident is the missing test alone.** When we find a
missed intrusion at a design-partner site, that is one product incident and one
quality incident, and closing only the first is how it happens again.

### 1.3 The three properties every test in this repo must have

1. **It fails for one reason.** A test that fails for three reasons is a test
   nobody debugs under pressure. The existing tests model this: the message says
   what happened and why it matters, not just "got X want Y" — `TestExpiredLeaseIsReclaimed`
   says "a crashed worker would hold it for ever"; `TestNormalizeIsAtomicWithTheInboxTransition`
   says "every future retry would be swallowed as already-done and the event would
   be lost for good".
2. **It is honest about the environment.** Database-backed suites read
   `SP_TEST_DATABASE_URL` and **skip with a message** when it is absent
   (`internal/testutil`), rather than passing vacuously. A test that skips must
   say so in its output; CI must treat a skip count as a failure, or the skip
   becomes a hole (§11.2).
3. **It wires real services, not stubs, where the real service is available.**
   `internal/api/api_test.go` builds `incidents.Service`, `bundles.Service`,
   `identity.Service` and `audit.Writer` over a real pool and registers real
   routes, with the comment "a nil service behind a route is exactly the shape of
   a defect the console would hit in production". `internal/durability`'s `enqueue`
   helper opens a transaction for the same reason. Stubs are for the model client
   and the clock, and nowhere else.

---

## 2. The test shape

It is not a pyramid. A pyramid assumes tests get more expensive and less numerous
as they go up, and it assumes the top is the only thing that matters. Neither
holds here: the expensive tests are in the *middle* (database-backed, ~20s each
with `-race` and `-p 1`), the top is small and mostly manual, and there is a
whole tier (fault injection) that has no analogue in the classical model because
it is the only tier that tests the *absence* of a failure rather than the presence
of a behaviour.

The shape, and what each tier is for:

```
                 ┌───────────────────────────────────────────┐
                 │  human acceptance            per-milestone │  ~4 gates × 1.5h
                 │  E1 + E2, signed, with the artefact       │     2 people
                 └───────────────────────────────────────────┘
        ┌──────────────────────────────────────────────────────────────┐
        │ fault injection        every stage, every durability change  │  the tier
        │   kill -9 at a named point; assert the two invariants         │  nobody
        └──────────────────────────────────────────────────────────────┘
  ┌───────────────────────────────────────────────────────────────────┐
  │ security tests       the 35 threat-model controls, path traversal │  per-commit
  │ golden / replay      fixture → canonical → verification → incident │  nightly
  └───────────────────────────────────────────────────────────────────┘
     ┌───────────────────────────────────────────────────────────────────┐
     │ database-backed integration   real Postgres 16, real pool, -race  │  per-commit
     └───────────────────────────────────────────────────────────────────┘
        ┌─────────────────────────────────────────────────────────────────┐
        │ contract conformance     contractcheck, migration phases, RLS   │  per-commit
        └─────────────────────────────────────────────────────────────────┘
  ┌───────────────────────────────────────────────────────────────────────┐
  │ unit + property        no database, no network, milliseconds         │  per-commit
  └───────────────────────────────────────────────────────────────────────┘
     ┌───────────────────────────────────────────────────────────────────┐
     │ UI / E2E               server-rendered HTML assertions, 12 tests  │  per-commit
     └───────────────────────────────────────────────────────────────────┘
```

UI/E2E is *low* in this diagram deliberately. It is a small tier and it is low in
volume, not low in value: `internal/api/api_test.go` is the only automated
executor of the console, and those twelve tests are load-bearing (§2.7).

### 2.1 Unit and property tests

**Covers.** Rule compilation and the semantic validator; the verification state
machine; conflict precedence; time-window and skew logic; clock-drift
normalisation; evidence path validation; the observation schema check; the
evaluation gates; the metric registry's arithmetic; the command dispatcher; the
release path validator; the fixture sanitiser. Everything that is a pure function
of its arguments, with the database replaced by a struct.

**Command.** `go test ./internal/rules/... ./internal/evaluation/...
./internal/evidence/... ./internal/release/... ./internal/command/...` — in
practice `make test` (`go test -p 1 ./...`) and `make test-race`
(`go test -p 1 -race -count=1 ./...`).

**Frequency.** Per commit. Wall clock: these are the fast packages and they are
the ones that should stay under a second each; a unit test that needs a database
is a misfiled test.

**Representative tests, by what they actually pin down.** From
`internal/rules/compile_test.go`: `TestBuiltinBundleCompiles`,
`TestEmbeddedSchemaMatchesThePublishedContract`, `TestNoUnboundedRegexOperatorExists`
(the language has no regex — `AGENTS.md` and `00` §5 of `01`),
`TestFieldValueListIsBounded` (a bundle cannot allocate without bound),
`TestTimeWindowMayOnlyReadNormalizedAt` (the only field a time predicate may read,
`AGENTS.md`), `TestUnknownEventTypeIsRejected`, `TestDuplicateRuleIDIsRejected`,
`TestSamePriorityIncompatibleRulesAreAConflict`,
`TestSamePriorityCompatibleRulesAreNotAConflict`,
`TestDifferentPrioritiesAreResolvedNotConflicted`, `TestEvaluationOrderIsStable`,
`TestImpossibleClockBandsAreRejected`, `TestMissingTimezoneIsRejected`,
`TestConsumingAnAIObservationRequiresAnExplicitDegradation`,
`TestAIDegradationMustBeOneOfTwoValues`, `TestProceedWithoutWarns`,
`TestAIObservationFieldMustExistOnTheProfile`, `TestEmptyConditionAndActionAreRejected`,
`TestContradictoryActionIsRejected`. From `internal/release/release_test.go`:
`TestValidateFilePathRejectsEveryEscape`, `TestSecretMaterialIsRefused`,
`TestBinaryFilesAreNotScanned`, `TestContractStepAlongsideExpandIsRefused`.
From `internal/command/command_test.go`: `TestFindRejectsUnknownPath`,
`TestExitCodesDistinguishRefusalFromFailure` — a refusal and a failure must have
different exit codes, because the first is correct behaviour and the second is an
outage.

**Owner.** E2, with the author of the code under test.

**The bar.** A new function with a branch in it does not merge without a test
that exercises the branch. A new operator in the rule language does not merge
without a test that refuses it (`TestNoUnboundedRegexOperatorExists` is the model:
it is a test about the *absence* of a capability).

### 2.2 Contract conformance

**Covers.** The wire. Whether a change to `contracts/` breaks a client, whether
the embedded copy of a schema matches the published one, whether every migration
is a legal forward-only step in a legal phase, whether the RLS laws hold, and
whether the schema window in the release manifest covers the migrations.

**Commands.** Four, all in `make ci`:

| Command | What it does | Test behind it |
|---|---|---|
| `make contract-check` | `go run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json` — fails on a removed field, a reused tag, a changed type or cardinality, an enum value removed or renamed, a field moved out of a `oneof` | `internal/contracts/contractcompat_test.go`: `TestContractBaselineIsPresentAndNonEmpty`, `TestAllPlannedContractFilesExist`, `TestOtherContractSurfacesExist`, `TestSiteConfigSchemaCannotCarryASecret` |
| `make contract-sync` | `cmp contracts/json-schema/rule-bundle.schema.json internal/rules/schema/rule-bundle.schema.json` | `TestEmbeddedSchemaMatchesThePublishedContract` |
| `make migration-phases` | `go test -p 1 ./migrations/... -run TestAllLoadsAndValidates` | `migrations/migrations_test.go`: `TestAllLoadsAndValidates`, `TestM1ContainsNoContractStep`, `TestParseFileName`, `TestIsValidRejectsUnknownPhase`, `TestReleaseCompatibilityWindowCoversMigrations`, `TestEmbeddedAndOnDiskManifestsAgree`, `TestChecksumDetectsAnyEdit` |
| `make rules-lint` | `securityctl rules lint --bundle rules/builtin`, no Milestone server | `TestBuiltinBundleCompiles` |

**The RLS and schema-law tier** is `internal/schemalaws/schemalaws_test.go`, 24
tests, and it is the most important test file in the repository that is not about
durability: `TestRLSIsoTenants`, `TestRLSIsoTenantsForLargeTables`,
`TestRLSBlocksCrossTenantWrite`, `TestTenantContextIsRequiredForWrites`,
`TestMigratorIsTheOnlyRoleThatBypassesRLS`, `TestAdapterCannotReadAuditLedger`,
`TestReadonlyHasNoGrantOnAuditLedger`,
`TestAIRuntimeCannotAlterAuthoritativeState`, `TestAuditLedgerIsAppendOnly`,
`TestAuditChainChainsFromGenesis`, `TestAuditChainRejectsDuplicateSequence`,
`TestAuditChainRejectsGap`, `TestTamperedRowBreaksChainVerification`,
`TestAIObservationIsAppendOnly`, `TestAIObservationSupersession`,
`TestAIObservationRecordsDegradation`, `TestEvidenceStorageRefIsAlwaysRelative`,
`TestInboxDedupeIsGlobalNotPerPartition`, `TestJobsAreIdempotentPerQueue`,
`TestInboxAndCanonicalEventsArePartitionedByMonth`, `TestSessionsCarryMFAAndSiteScope`,
`TestBreakGlassRequiresAPassword`, `TestNoEntityAliasTableInM1`,
`TestEdgesRequireProvenance`, `TestRevokedEdgesAreHiddenFromTheActiveView`,
`TestAuditLedgerRequiresTenant`.

**Frequency.** Per commit, and they are the *first* thing CI runs because they are
the cheapest and they fail the build before anything is compiled.

**Owner.** Whoever changes the contract, with E2 as the reviewer. **Nothing
under `internal/contracts/` is hand-edited** — `AGENTS.md`, and it is a
`contractcheck` failure waiting to happen if it is.

**Known gap.** `make contract-check` compares against `contracts/compat/baseline.json`.
The baseline is regenerated with `make contract-baseline` (`-mode write`), and
**nothing in CI prevents someone running `-mode write` and committing the
result.** The gate only detects an *unintentional* break. The control that
actually protects the wire is a review rule: a diff to
`contracts/compat/baseline.json` is an ADR, every time, with the removed fields
listed. Add a check that the baseline's git history contains exactly one commit
per intentional version bump, keyed to an ADR filename. This is a named quality
incident under §1.2 — the control is a convention, and conventions are not
controls.

### 2.3 Database-backed integration

**Covers.** Anything where the database is the thing under test: dedupe, leases,
normalisation atomicity, job claiming, outbox delivery, RLS under a real policy,
identity under real Argon2id, the audit trigger, the dead-letter transitions.

**Command.** `make test-race` — `go test -p 1 -race -count=1 ./...`.

**`-p 1` is not a politeness setting.** The database-backed suites share one
database and rebuild the schema, so package test binaries must not run
concurrently; without it one suite's `DROP SCHEMA` lands in the middle of
another's test and produces failures that look like product bugs. This is
recorded in the `Makefile` and in `docs/status.md` and it is the first thing a new
engineer breaks.

**Setup**, from `docs/status.md`:

```sh
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
export SP_TEST_DATABASE_PASSWORD=postgres
go run ./cmd/security-migrate up
go test -p 1 -race ./...
```

**Frequency.** Per commit, and the whole suite is a pre-release gate.

**Owner.** E2. The `migrated(t)` helper in `internal/durability` always applies
*pending* migrations rather than only on an empty schema, with the reason in a
comment: "a new migration added to the tree has to reach a database that already
has the older ones, or the suite would keep testing a schema that is no longer
what this build produces." That is the correct pattern and it is copied.

**Known gap.** There is no test that a *partial* migration failure leaves the
database in the state the runner claims. `TestFailedStepLeavesEarlierStepsCommitted`
exists in `internal/migrate` and asserts the positive half; the negative half — a
step that fails *after* a non-transactional statement has already changed
something — is not covered, and Postgres runs each statement in a file
independently. **Every future migration must be individually reviewed for whether
its statements are transactional**, and a table of "is this step one transaction"
belongs in the migration's own header comment. This is a named quality incident.

### 2.4 Replay and golden

**Covers.** That a decision reproduces. That a fixture set is intact. That the
adapter's live path and its replay path are the same parser. That the sanitiser
is the only way data enters the repository.

**The golden chain** is `fixture → canonical → verification → incident`, and the
requirement is **byte-stability**, not similarity. A golden test that passes on a
changed byte and calls it "close enough" is not a golden test.

**Commands.** `go test ./internal/milestone/... ./internal/fixture/...` and the
schema-law replay assertions in `./internal/schemalaws/...`; all of them inside
`make test-race`. `securityctl incident replay <id>` is the manual form.

**Frequency.** Per commit for the integrity tests; the full fixture set
regeneration and byte-comparison is nightly, because it is the one thing that
should never change without a human reading the diff.

**Representative tests.** `internal/milestone/adapter_test.go`:
`TestPollParsesAFeed`, `TestTimezoneProvenanceIsPreserved`,
`TestOneUnreadableItemDoesNotLoseThePage`, `TestUnknownEventTypeIsCarriedNotDropped`,
`TestFeedFailureIsIsolated`, `TestCredentialFailureIsNamedAsSuch`,
`TestPollOrderIsDeterministic`, `TestReplayTransportIsDeterministic`,
`TestReplayEpochDoesNotChangeRelativeTiming`,
`TestReplayProducesEveryRequiredBehaviour`, `TestReplayDeliversDuplicatesVerbatim`,
`TestMilestoneRoutesAreDeclaredUnverified`.
`internal/fixture/fixture_test.go`: `TestStoreRoundTripAndManifestVerification`,
`TestVerifyManifestDetectsPostReviewEdit`,
`TestVerifyManifestDetectsMissingAndExtraScenarios`,
`TestCommittedFixtureSetVerifies`, `TestFixtureSetCoversEveryRequiredCase` (which
asserts coverage of `motion`, `analytics`, `alarm.created`, `alarm.acknowledged`,
`device.camera_offline`, `device.camera_online`, `bookmark`, `recording.started`,
`metadata`, `malformed`, `duplicate`, `delayed`, `out-of-order`, `missing-camera`,
`reconnect`), `TestRedactsSecretsByKeyName`,
`TestSecretKeyMatchingIgnoresCaseAndSeparators`, `TestRedactsIdentifiers`,
`TestRedactsURLs`, `TestRedactsOpaqueLongStrings`, `TestSanitizeResponse`.
`internal/incidents/incidents_test.go`: `TestReplayReproducesTheDecisionFromStoredFacts`,
`TestReplayReportsABundleMismatch`, `TestReplaySurfacesDegradation`.
`internal/observations/observations_test.go`: `TestOutputHashIsStableAndRecomputable`,
`TestObservationsAreAppendOnly`, `TestReEvaluationSupersedesRatherThanOverwrites`.

**Owner.** E2, and the adapter's author for the Milestone-specific half.

**`TestMilestoneRoutesAreDeclaredUnverified` deserves its own paragraph.** Every
route in `internal/milestone/routes.go` is marked `Unverified`, and this test
*asserts that they still are*. It is a test whose job is to fail when someone
removes a warning. That is the right shape for an honest gap, and it is rare
enough to name as a model.

### 2.5 Fault injection

**Covers.** The two halves of invariant 1 at a named stage: no loss, and no
duplicate side effect.

**This is the tier that does not yet exist in the form `AGENTS.md` requires**, and
saying so is the honest position. `AGENTS.md`: "Any durability change needs a
`kill -9` fault-injection test at the stage it touches. It is a DoD item, not a
nice-to-have." What the tree has today is *crash-equivalent* tests: they put the
database into the state a crash would leave it in and assert the recovery
behaviour. `TestReNormalizeIsANoOp` steals a lease mid-flight;
`TestNormalizeIsAtomicWithTheInboxTransition` does the same and asserts the
`canonical_event_keys` row rolled back with it; `TestExpiredLeaseIsReclaimed`
moves `lease_expires_at` into the past; `TestOutboxEnqueueIsTransactional` forces
a rollback inside the transaction. These are good tests and they are not the same
thing.

**The difference, precisely.** A crash-equivalent test sets up the state a crash
leaves. A fault-injection test *causes* the crash in a real process, with a real
socket, a real WAL and a real operating system, and observes what the surviving
process — restarted against the same database — finds. The gap between the two is
exactly where the bugs live: what is in the WAL but not committed, what a client
had already been told, what a half-written file on disk looks like, what two
processes do when the lease has not yet expired on the dead one.

**The programme to close it is §5.** It is the highest-value quality work
available and it is 8 stages, of which 4 have a partial substitute today.

**Frequency.** Per commit for the stage a change touches. Full sweep nightly.

**Owner.** E2, and the author of the stage.

### 2.6 Security tests

**Covers.** The 35 controls in `docs/threat-model.md`, each mapped to the test
that fails when the control is removed — the mapping is already written there and
this document does not restate it. Plus the two tiers below, which get their own
headings because they are the ones with an explicit rule in `AGENTS.md`.

**Release and bundle verification** — `internal/release/release_test.go`:
`TestABuiltArchiveVerifies`, `TestTamperedArchiveIsRefused`,
`TestUnsignedManifestIsRefused`, `TestForeignSigningKeyIsRefused`,
`TestManifestEditBreaksTheSignature`, `TestUnlistedFileIsRefused`,
`TestMissingFileIsRefused`, `TestPathTraversalEntryIsRefused`,
`TestValidateFilePathRejectsEveryEscape`, `TestSecretMaterialIsRefused`,
`TestBinaryFilesAreNotScanned`, `TestContractStepAlongsideExpandIsRefused`,
`TestEmptyManifestIsRefused`.

**Identity** — `internal/identity/identity_test.go`, 21 tests, and several of
them exist specifically because the naive implementation would pass a code review
and fail a penetration test: `TestPasswordHashingAndVerification`,
`TestPasswordsAreSaltedPerUser`, `TestShortPassphrasesAreRefused`,
`TestWeakHashParametersAreRefused` (the parameters live in the build, not in the
hash row, so they can be raised without a migration),
`TestCapabilitiesAreExactNotPrefixes`, `TestSiteScopeDeniesByDefault`,
`TestServiceAccountsNeverHoldAuditRead`, `TestAuthorizeRequiresAVerifiedFactor`,
`TestAuthorizeSiteCombinesCapabilityAndAssignment`,
`TestLockoutIsExponentialAndCapped`, `TestBootstrapRefusesASecondAdministrator`,
`TestPasswordStepReturnsAChallengeNotASession`,
`TestUnknownUserAndWrongPasswordAreIndistinguishable`,
`TestSuccessfulPasswordClearsTheFailureCounter`,
`TestRepeatedFailuresLockTheAccount`, `TestSessionTokensAreStoredHashed`,
`TestSessionTokensRotateOnUse`, `TestCSRFTokenIsRequiredAndSessionBound`,
`TestPasswordChangeRevokesEverySession`, `TestEveryAuthEventIsAudited`,
`TestStepUpIsSingleUse`.

**API and console** — `internal/api/api_test.go`, 12 tests, §2.7.

**Frequency.** Per commit. **Owner.** E2. A security test failure is never
"flaky, retry it"; a flaky security test is a finding about the control.

### 2.7 UI / E2E

**Covers.** The rendered console and the HTTP contract around it. There is no
browser automation and there will not be any — the console is server-rendered Go
templates with no client-side JavaScript (§4.2 of
``), so "E2E" here means asserting on the HTML a real
request produces, against real services over a real database.

**The twelve tests, and what each one is really asserting:**

| Test | What it protects |
|---|---|
| `TestSecurityHeadersArePresent` | `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `COOP: same-origin`, `X-Robots-Tag: noindex, nofollow, noarchive`, HSTS `max-age=31536000; includeSubDomains` |
| `TestContentSecurityPolicyForbidsInlineScript` | `default-src 'none'`, `script-src 'self'`, `style-src 'self'`, `frame-ancestors 'none'`, `object-src 'none'`, `base-uri 'none'`, **and** that neither `unsafe-inline` nor `unsafe-eval` appears anywhere in the header |
| `TestHealthExcludesTheAIPlane` | `/v1/health` returns `status: ok` **and** says something about the AI plane — "an operator reading it should be able to tell the AI plane was not consulted" |
| `TestUnauthenticatedRequestsAreRefused` | 401 on `/v1/incidents`, `/v1/auth/session`, `/v1/audit/ledger`, `/v1/ops/dead-letters`, `/incidents` |
| `TestStateChangingRequestWithoutCSRFIsRefused` | a disposition needs the token; and a *correct* token gets past CSRF and fails on the incident, not the token |
| `TestCSRFTokenIsBoundToTheSession` | a foreign token is 403 |
| `TestTenantHeaderMismatchIsRefused` | `X-Tenant-Id` is honoured only when it matches the session tenant |
| `TestConsoleIsLeftToRight` | `dir="ltr"`, `lang="en"`, **no `<script` element anywhere**, **no `style=` attribute anywhere** — `AGENTS.md` |
| `TestJSONIsNeverNegotiatedIntoHTML` | `Accept: text/html` on `/v1/incidents` still returns `application/json` |
| `TestSessionCookieCarriesEveryAttribute` | `HttpOnly`, `Secure`, `SameSite=Strict` |
| `TestLoginWithBadCredentialsIsRefused` | 401 **and zero cookies set** |
| `TestBodySizeIsCapped` | a 2 MB body is refused |

**Command.** `go test ./internal/api/...`, inside `make test-race`.
**Frequency.** Per commit. **Owner.** E2.

**The gap, and it is a real one.** These tests assert properties of responses.
They do not assert that the console *renders the thing it is supposed to render*:
no test asserts that the incident page contains the incident's title, that the
evidence table has one row per item, that the disposition form's radio group
carries the four outcomes, or that the AI-unavailable notice appears when
`DegradedReasons` is non-empty. A template that renders an empty table for every
incident passes all twelve. Adding content assertions is M1 work, and the
`06` document's §10 finding list is where the missing assertions get named.

### 2.8 Human acceptance

**Covers.** The four things a machine cannot judge: is the explanation actually
comprehensible, is the disposition reachable without a mouse, does the evidence
bundle let a competent operator reach the right answer, and would *this
operator* rather than this engineer prefer it to the VMS.

**The four M1 gates, and their test.** From `` M1, each
signed by E1 and E2:

1. Live Milestone integration and replay parity → the parity report, both paths,
   same canonical events, side by side. **Unsignable today**: no Milestone
   server has been reachable, `docs/status.md`.
2. One incident class end to end, real operator, timed → a transcript and a
   stopwatch, not a demo.
3. Every SLO in `internal/observability/slo.go` met under a recorded load run,
   with the run and its numbers in the release notes → a load-run artefact. **The
   evaluation exists; the demonstration needs a dev installation and a load
   generator.**
4. A production VLM profile passing its gates on a real GPU, in shadow, 14
   consecutive days, evaluation report attached → §7.

**Frequency.** Per milestone, at the exit gate, and per release candidate for
anything touching the console.

**Owner.** Both people, in the same room, with the artefact open. A gate signed by
one person is not a gate.

---

## 3. Invariant → test matrix

The rule from `AGENTS.md`: **a change that weakens an invariant needs an ADR and a
test, in that order.** The order is the point. The ADR states what is being given
up and why; the test proves the thing still holds. Writing the test first is a
test of the new behaviour, which is not the same thing. So every row below has
an ADR column, and a row without an ADR path is a row that cannot be touched
without one.

**The test named in each row is the test that fails when the control is removed.**
Not a test that covers the area. If you delete the control and the test is still
green, that is a gap and it is reported, not worked around.

### Invariant 1 — zero accepted event loss, no duplicate side effect

| Control | Test that fails when removed | ADR required | Today |
|---|---|---|---|
| Scoped dedupe key taken before the work | `TestWriteAcceptsOnce` (durability) | yes | ✅ |
| Dedupe is global, not per-partition | `TestDedupeCrossesPartitions`; `TestInboxDedupeIsGlobalNotPerPartition` (schemalaws) | yes | ✅ |
| Uniqueness enforced by the database, not by callers agreeing | `TestConcurrentWritesProduceOneRow` (8 goroutines, exactly 1 accepted) | yes | ✅ |
| An event with no source id is refused | `TestWriteRejectsUnidentifiedEvents` | no | ✅ |
| Lease expiry with owner and attempt count | `TestClaimLeasesAndIsExclusive`, `TestExpiredLeaseIsReclaimed` | no | ✅ |
| A non-holder cannot renew | `TestRenewFailsOnceTheLeaseIsLost` | no | ✅ |
| Terminal failure reaches a dead letter *with a reason* | `TestFailReachesDeadLetterWithAReason`, `TestJobDeadLetterIsVisibleWithAReason` | no | ✅ |
| Event id derived from the scoped key, so a retry is a retry | `TestEventIDIsDerivedFromTheScopedKey` (no database needed) | yes | ✅ |
| Integrity hash independent of receipt time | `TestIntegrityHashIgnoresReceiptTime` | no | ✅ |
| Canonical event + inbox transition + key claim in one transaction | `TestNormalizeIsAtomicWithTheInboxTransition` — asserts **zero** surviving `canonical_event_keys` rows after rollback | yes | ✅ |
| Repeat normalisation is a no-op | `TestNormalizeProducesExactlyOneEvent`, `TestReNormalizeIsANoOp` | yes | ✅ |
| Job enqueue requires an idempotency key, scoped per queue | `TestJobEnqueueRequiresAnIdempotencyKey`, `TestJobEnqueueIsIdempotentPerQueue`, `TestJobsAreIdempotentPerQueue` | yes | ✅ |
| `FOR UPDATE SKIP LOCKED` claim is exclusive and recoverable | `TestJobClaimIsExclusiveAndRecoverable` | no | ✅ |
| A worker cannot complete a job it does not hold | `TestCompletingAJobSomebodyElseLeasesFails` | no | ✅ |
| Outbox is transactional with the state change | `TestOutboxEnqueueIsTransactional` | yes | ✅ |
| Outbox delivers at least once and terminally, idempotently | `TestOutboxDeliversAtLeastOnceAndTerminally` | no | ✅ |
| One poison message does not block a good one | `TestOutboxIsolatesAPoisonMessage` | no | ✅ |
| Outbox idempotency key is tenant-scoped | `TestOutboxIdempotencyKeyIsScopedToTheTenant` | yes | ✅ |
| Any loss is a breach, stated as a count | `TestAnyLossIsABreach` (observability) | no | ✅ |
| Every SLO carries a remedy | `TestEverySLOBriefCarriesARemedy` | no | ✅ |

**Fault-injection coverage for this invariant: none in the `kill -9` sense.** All
of the above is crash-*equivalent*. §5.

### Invariant 2 — decisions replay from stored facts and the stored AIObservation

| Control | Test that fails when removed | ADR required | Today |
|---|---|---|---|
| The replay path has no model client, no bundle path, no clock | `TestReplayReproducesTheDecisionFromStoredFacts` | **yes, always** | ✅ |
| A bundle mismatch is reported, not silently replayed | `TestReplayReportsABundleMismatch` | yes | ✅ |
| A degraded decision replays as degraded | `TestReplaySurfacesDegradation` | yes | ✅ |
| Evaluation is a pure function of stored facts | `TestEvaluationIsDeterministic` | yes | ✅ |
| Out-of-order arrival does not change the outcome | `TestOutOfOrderArrivalDoesNotChangeTheOutcome` | yes | ✅ |
| Every decision carries its provenance | `TestDecisionCarriesItsProvenance` | yes | ✅ |
| Graph snapshot is stable and refuses an unknown relation | `TestSnapshotIsStable`, `TestSnapshotRefusesAnUnknownRelation` | no | ✅ |
| A replayed adapter feed is byte-identical to the live parse | `TestReplayTransportIsDeterministic`, `TestPollOrderIsDeterministic`, `TestReplayEpochDoesNotChangeRelativeTiming` | no | ✅ |
| An observation's output hash is recomputable from stored bytes | `TestOutputHashIsStableAndRecomputable` | yes | ✅ |
| Observations are append-only; re-evaluation supersedes | `TestObservationsAreAppendOnly`, `TestReEvaluationSupersedesRatherThanOverwrites`, `TestAIObservationIsAppendOnly`, `TestAIObservationSupersession` | yes | ✅ |
| An unexplainable decision cannot become an incident | `TestCreateIncidentRefusesAnUnexplainableDecision`, `TestCreateIncidentStoresItsJustification` | yes | ✅ |
| A disposition never overwrites an observation | `TestDispositionDoesNotOverwriteTheObservation` | **yes, always** | ✅ |
| A replayed summary makes no model call | M2 — does not exist yet | yes | M2 gate |
| The whole system reproduces after a database loss | `TestRecoveryDrill` (dr) | yes | ✅ |

**This invariant carries the most ADRs in the product**, because it is the one a
feature request attacks most naturally. Every new capability that produces a
decision must arrive with an answer to "what is stored, and does this still
reproduce with the model switched off?" A capability that cannot answer that is
declined.

### Invariant 3 — a missing AI observation is never a negative observation

| Control | Test that fails when removed | ADR required | Today |
|---|---|---|---|
| A rule consuming a Tier C signal **must** declare `if_ai_unavailable` | `TestConsumingAnAIObservationRequiresAnExplicitDegradation` | **yes, always** | ✅ |
| The declaration is exactly one of two values | `TestAIDegradationMustBeOneOfTwoValues` | yes | ✅ |
| `proceed_without` warns, because it is a weaker guarantee | `TestProceedWithoutWarns` | yes | ✅ |
| A field a rule names exists on the profile | `TestAIObservationFieldMustExistOnTheProfile` | no | ✅ |
| A missing observation degrades to UNVERIFIED, never to a false | `TestMissingAIObservationDegradesToUnverified` | **yes, always** | ✅ |
| A degraded observation is not evidence | `TestDegradedObservationIsNotEvidence` | **yes, always** | ✅ |
| Insufficient evidence is not a negative answer | `TestInsufficientEvidenceIsNotANegativeAnswer` | **yes, always** | ✅ |
| The built-in bundle runs with no AI observation at all | `TestBuiltInBundleRunsWithNoAIObservationAtAll` | **yes, always** | ✅ |
| A shadow profile has no authoritative effect | `TestShadowProfilesHaveNoAuthoritativeEffect`, `TestShadowProfilesCannotAffectAuthoritativeState`, `TestAIRuntimeCannotAlterAuthoritativeState` | yes | ✅ |
| Unknown profile or undeclared field is refused | `TestUnknownProfileAndFieldAreRefused` | no | ✅ |
| Attribution is mandatory on every observation | `TestAttributionIsMandatory` | **yes, always** | ✅ |
| A degraded observation is persisted *and flagged*, not dropped | `TestDegradedObservationIsPersistedAndFlagged`, `TestAIObservationRecordsDegradation` | yes | ✅ |
| Absence is not a prediction, in the harness too | `TestAbsenceIsNotAPrediction`, `TestInsufficientEvidenceIsNotAPrediction` | **yes, always** | ✅ |
| An undispositioned case is never folded into a rate | `TestUndispositionedCasesAreNeverFoldedIntoARate` | **yes, always** | ✅ |
| The AI plane is reported but never required | `TestAIPlaneIsReportedButNotRequired`, `TestHealthExcludesTheAIPlane` | no | ✅ |
| Pilot reporting can measure how often the AI path was live | `TestAvailabilityIsExposedForPilotReporting` | no | ✅ |
| A drift exclusion is recorded, not applied silently | `TestDriftedDeviceIsExcludedAndRecorded`, `TestDriftIsSurfacedEvenWhenNoIncidentIsRaised` | yes | ✅ |
| **The deterministic core is functional with the AI plane entirely absent** | `TestBuiltInBundleRunsWithNoAIObservationAtAll` + `TestHealthExcludesTheAIPlane` | **yes, always** | ✅ |

The last row is the load-bearing one and it is a *product* claim, not just a code
property. A test suite can prove the bundle compiles and evaluates without
observations; it cannot prove that no other code path consults the model on the
availability path. That is checked at the milestone gate by a run with the model
runtime stopped, and the run's transcript is the artefact.

### Invariant 4 — the audit ledger is append-only and hash-chained

| Control | Test that fails when removed | ADR required | Today |
|---|---|---|---|
| The chain starts at genesis | `TestAuditChainChainsFromGenesis` | no | ✅ |
| A duplicate sequence is rejected | `TestAuditChainRejectsDuplicateSequence` | no | ✅ |
| A gap is rejected | `TestAuditChainRejectsGap` | no | ✅ |
| Editing any row breaks verification | `TestTamperedRowBreaksChainVerification` | **yes, always** | ✅ |
| The ledger cannot be updated or deleted | `TestAuditLedgerIsAppendOnly` | **yes, always** | ✅ |
| Concurrent appends produce one chain | `TestConcurrentAppendsProduceOneChain` | no | ✅ |
| Chains are isolated per tenant | `TestChainsAreIsolatedPerTenant` | yes | ✅ |
| An entry is incomplete or it is not written | `TestValidateRejectsIncompleteEntries` | no | ✅ |
| A broken chain cannot be sealed | `TestSealerRefusesToSealABrokenChain` | no | ✅ |
| A seal verifies, and any row change changes the digest | `TestSealerProducesAVerifiableSeal`, `TestDigestChangesWithAnyRow` | no | ✅ |
| A seal will not verify under a foreign key | `TestVerifyRejectsForeignKey`, `TestForeignSigningKeyIsRefused` | yes | ✅ |
| Tampering with a sealed field is detected | `TestVerifyDetectsFieldTampering` | no | ✅ |
| A malformed or unsigned seal file is refused | `TestVerifyRejectsMalformedSeals`, `TestFileSinkRefusesUnsignedAndUnsafe` | no | ✅ |
| A removed seal is detected as a gap | `TestGapDetectsRemovedSeal`, `TestVerifyAgainstSeals` | yes | ✅ |
| The signing payload is unambiguous | `TestSigningPayloadIsUnambiguous` | no | ✅ |
| The adapter role cannot read the ledger | `TestAdapterCannotReadAuditLedger`, `TestReadonlyHasNoGrantOnAuditLedger` | yes | ✅ |
| Only the migrator bypasses RLS | `TestMigratorIsTheOnlyRoleThatBypassesRLS` | yes | ✅ |
| **Editing any row must fail checkpoint verification** | the last row, run against a *sealed* range in the recovery drill | **yes, always** | ✅ (`TestRecoveryDrill`) |

### Invariant 5 — `tenant_id` is `NOT NULL` everywhere and is never read from a request body

| Control | Test that fails when removed | ADR required | Today |
|---|---|---|---|
| The audit ledger requires a tenant | `TestAuditLedgerRequiresTenant` | no | ✅ |
| RLS isolates tenants on the small tables | `TestRLSIsoTenants` | yes | ✅ |
| RLS isolates tenants on the **large** partitioned tables | `TestRLSIsoTenantsForLargeTables` | yes | ✅ |
| A cross-tenant write is blocked | `TestRLSBlocksCrossTenantWrite` | yes | ✅ |
| A write without tenant context is refused | `TestTenantContextIsRequiredForWrites` | yes | ✅ |
| `site_id` is `NOT NULL` only on physically site-bound records | schema review per migration + `TestEvidenceStorageRefIsAlwaysRelative` | yes | ✅ |
| Site scope denies by default | `TestSiteScopeDeniesByDefault` | **yes, always** | ✅ |
| Capability **and** assignment are both required | `TestAuthorizeSiteCombinesCapabilityAndAssignment` | yes | ✅ |
| Capabilities are exact, never prefixes | `TestCapabilitiesAreExactNotPrefixes` | yes | ✅ |
| A service account can never read the audit ledger | `TestServiceAccountsNeverHoldAuditRead` | yes | ✅ |
| A tenant header cannot select a tenant | `TestTenantHeaderMismatchIsRefused` (api) | **yes, always** | ✅ |
| Unauthenticated access is refused on every surface, HTML included | `TestUnauthenticatedRequestsAreRefused` | no | ✅ |
| Break-glass requires a password and is audited | `TestBreakGlassRequiresAPassword`, `TestEveryAuthEventIsAudited` | yes | ✅ |
| A sensitive disposition needs a fresh factor | `TestSensitiveDispositionsRequireStepUp`, `TestAuthorizeRequiresAVerifiedFactor`, `TestStepUpIsSingleUse` | yes | ✅ |
| The site config schema cannot carry a secret | `TestSiteConfigSchemaCannotCarryASecret` | no | ✅ |

**The gap that the tests do not close.** Every one of these is a test in the
repository. None of them is a test that walks every handler and asserts the
tenant came from the session rather than the body. `TestTenantHeaderMismatchIsRefused`
covers the header; the body is covered by nothing structural, because the pattern
is enforced by discipline plus `dbx.TenantContext` being required at the pool
level. **The control is real — the pool requires a tenant context for a write —
and the test that proves no handler bypasses it is missing.** That is a M1 quality
incident and the test is a static walk of the route table asserting that no
handler decodes a `tenantId` from a request body. Recorded, not worked around.

### Invariant 5b (README's fifth) — bundles are untrusted input

`AGENTS.md` states this as a working agreement rather than a numbered invariant,
and `README.md` states it as invariant 5 in place of the `tenant_id` one. Both
statements are the law; §"Consistency findings" in the summary records the
discrepancy.

| Control | Test that fails when removed | Today |
|---|---|---|
| The rule language has no regex, no shell, no SQL, no network, no dynamic code | `TestNoUnboundedRegexOperatorExists`, `TestEmptyConditionAndActionAreRejected`, `TestContradictoryActionIsRejected` | ✅ |
| A list-valued field is bounded, so a bundle cannot allocate without bound | `TestFieldValueListIsBounded` | ✅ |
| Unknown keys, multiple YAML documents, schema violations, unknown event types, duplicate rule ids are all refused | `TestUnknownKeyIsRejected`, `TestMultipleYAMLDocumentsAreRejected`, `TestSchemaViolationIsCaught`, `TestUnknownEventTypeIsRejected`, `TestDuplicateRuleIDIsRejected` | ✅ |
| The embedded schema is the published schema | `TestEmbeddedSchemaMatchesThePublishedContract` + `make contract-sync` | ✅ |
| Storing a bundle does not activate it | `TestStoreDoesNotActivate` (bundles) | ✅ |
| Activation needs a reviewed diff digest computed over the diff | `TestActivationRequiresAReviewedDiffDigest` | ✅ |
| Activation is audited; stored content is immutable; rollback works | `TestActivationIsAudited`, `TestStoredContentIsImmutable`, `TestRollbackReturnsToThePreviousVersion`, `TestStoreIsIdempotentForTheSameVersion`, `TestStoreRefusesAnUncompilableBundle` | ✅ |
| A release archive: every file listed, every path safe, manifest signed, no secrets | `TestABuiltArchiveVerifies`, `TestUnlistedFileIsRefused`, `TestMissingFileIsRefused`, `TestValidateFilePathRejectsEveryEscape`, `TestPathTraversalEntryIsRefused`, `TestSecretMaterialIsRefused`, `TestBinaryFilesAreNotScanned` | ✅ |
| Zone paths feed the compiler from the graph, not from the bundle | `TestZonePathsFeedTheRuleCompiler` | ✅ |
| A graph edge requires provenance and the vocabulary is closed | `TestEdgeRequiresProvenance`, `TestEdgeVocabularyIsClosed` | ✅ |

---

## 4. Golden-test discipline

### 4.1 The rule

> **Golden tests are deterministic-only.** `fixture → canonical → verification →
> incident` must be **byte-stable**. **VLM prose is never golden-tested.**

Both halves are in `AGENTS.md`. The first is a stability requirement; the second
is a correctness requirement, and it is the more interesting one.

### 4.2 Why prose cannot be golden-tested

A golden test asserts "the output equals this stored value". Apply that to a
model's natural-language field and three things break at once:

1. **It fails for a change that is not a regression.** A prompt rephrased for
   clarity changes every prose output. The test goes red, the author has to decide
   whether the change was an improvement, and there is no principled way to
   answer — which means every such change gets a re-recorded golden, and the
   golden file stops being evidence after the fourth regeneration.
2. **It rewards the wrong thing.** Once the golden is regenerated on every
   change, the test detects nothing. It becomes a change-detector, and a
   change-detector that always passes when regenerated teaches the team that the
   suite is green, which is the exact opposite of what a golden test is for.
3. **It freezes prose into a contract.** A stored string in `testdata/` is a
   promise that the product will keep saying that thing. Nobody wants that
   promise. Nobody has ever wanted it. And when a customer reads a
   `testdata/golden/incident.txt` they read a model sentence with a customer's
   site in it, which is also a §8 data problem.

### 4.3 The mechanism, and why it is an invariant-2 requirement

The product answers the question with **replay from the stored `AIObservation`**.
`internal/rules.Signal.AIObservation` carries only the *validated structured
fields* — the package comment says it directly:

> "There is deliberately no free-text field: a rule that could match model prose
> would be matching on wording that changes between model versions, and an
> incident that appears or disappears because a prompt was rephrased is not an
> incident anyone can defend."

So the chain is:

```
fixture (committed, sanitised, manifest-hashed)
  │  same parsing path as live — TestReplayTransportIsDeterministic
  ▼
canonical event (id derived from the scoped key, integrity hash independent of
  receipt time — TestEventIDIsDerivedFromTheScopedKey,
  TestIntegrityHashIgnoresReceiptTime)
  ▼
verification (pure function of stored facts + stored structured observation —
  TestEvaluationIsDeterministic, TestOutOfOrderArrivalDoesNotChangeTheOutcome)
  ▼
incident (justification stored at creation —
  TestCreateIncidentStoresItsJustification)
```

Everything downstream of the fixture is deterministic **because the AI output is
an input, not a step.** A golden test supplies the `AIObservation` as a fixture
field — the same bytes the production system stored, with the same
`output_hash` — and asserts the rest. The model is never called. Prose in the
observation is stored and replayed for a human to read, and it is never compared.

**This is why it is an invariant-2 requirement and not a style preference.**
Invariant 2 says a decision replays from stored facts and the stored
`AIObservation`, and a `replayedFromStoredFacts` response that re-inferred
anything is a bug. A golden test that called the model would be *a test that
re-infers*, and it would pass or fail on model variance rather than on our
logic. The mechanism that makes the product replayable is the same mechanism that
makes it testable, and the test would be measuring the wrong system if it did
anything else.

### 4.4 What a golden test looks like, concretely

```
testdata/golden/
  scenarios/            12 scenarios, one directory each
    analytics/          request.json, responses/*.json, expected.json
    out-of-order/       manifest entry declaring covers: ["out-of-order"]
  manifest.json         {version, scenarios[{name, epoch, covers, hashes}]}
  expected/
    analytics.canonical.json
    analytics.verification.json
    analytics.incident.json
```

`expected/*` is the byte-stable artefact. `manifest.json` is hashed and
`TestStoreRoundTripAndManifestVerification` / `TestVerifyManifestDetectsPostReviewEdit`
/ `TestVerifyManifestDetectsMissingAndExtraScenarios` /
`TestCommittedFixtureSetVerifies` guard it, so an edit to a fixture after review
is detected. That is what makes a golden set reviewable: reviewing a diff of
`expected/` means reviewing the behaviour change, because the input that produced
it is hashed.

`expected/*.incident.json` contains the incident's fields **including the stored
observation's structured output and its `output_hash`**, and contains the
observation's prose field only as a stored, hashed artefact — never as a compared
assertion. If a fixture's observation carries prose, the golden asserts the
`output_hash`, which is stable, and ignores the prose, which is not. The
explanation text the rule engine composes *is* golden-tested, because it is
composed from the structured fields and the reason code and is therefore
deterministic by construction.

### 4.5 Regeneration

`securityctl` has a fixture-regeneration path and it is **founder-only**: it
requires the release or an explicit `--rewrite` that prints the diff to stdout
before writing. Regeneration without a review step is how a golden set becomes a
change-detector, and the prevention is that regenerating is itself an auditable
action with a diff a human must read. A golden regeneration that lands in the same
commit as a behaviour change is rejected at review, always, without discussion.

---

## 5. The `kill -9` fault-injection programme

`AGENTS.md`: "Any durability change needs a `kill -9` fault-injection test at the
stage it touches. It is a DoD item, not a nice-to-have."

**Current state, stated honestly.** There is no test in the tree that sends
`SIGKILL` to a process. What exists is crash-*equivalent* coverage: tests that
place the database in the state a crash would leave it in. That is a real
contribution and it is not the same thing, and the difference is where the
remaining bugs are — WAL contents at the moment of death, half-written evidence
files, bytes already written to a socket, two processes with a lease that has not
yet expired on the dead one.

**The mechanism.** A test binary that runs the pipeline as a *child process*
against the real database, with a kill point:

```
go test ./internal/faultinjection/... -run TestKill -stage <stage>
  1. start a child process running the stage, with a kill-point env var set
  2. the child signals "I am at <kill point>" on a pipe and then blocks
  3. the parent sends SIGKILL (process.Kill on Windows — same guarantee:
     no cleanup, no flush, no defer)
  4. the parent restarts a fresh process against the same database
  5. assert the two invariants
```

A pipe, not a file, so the parent's knowledge of "the child is at the kill point"
is not itself dependent on a flush. `process.Kill` on Windows is
`TerminateProcess`, which is the correct analogue of `SIGKILL`: no `defer`, no
buffer flush, no orderly close. The test is meaningful on both platforms and the
`AGENTS.md` word "`kill -9`" means "the process dies without cooperating", not
"a specific signal number".

**The two assertions, at every stage.** From invariant 1, verbatim:

- **A1 — no loss.** Every event that reached `inbox_events` and was not
  explicitly dead-lettered reaches a terminal state (`processed` or
  `dead_letter`). Asserted as a count, never a percentage: the SLO's own comment
  says a percentage of zero is still a non-zero number of lost events and an
  operator reading "0.0000% loss" should be suspicious of the measurement.
- **A2 — no duplicate side effect.** After recovery, each of: canonical events,
  audit rows (by hash-chain sequence, not by count), outbox deliveries per
  idempotency key, and job completions is exactly what it would have been with no
  crash at all. **The reference is a clean run of the same input, in the same
  test, compared row by row.** A test that asserts "no more than N" is a
  coverage test, not a fault-injection test.

### 5.1 The stage table

| # | Stage | Failure injected | Assertion | DoD item | Today |
|---|---|---|---|---|---|
| 1 | **Ingest — accept** | SIGKILL between the `inbox_event_keys` insert and the `inbox_events` insert, in a child process, real WAL | A1: the event is present exactly once after restart, or absent from both tables. A surviving key with no event is the failure: every future retry would be swallowed as already-done | Ingest DoD | ❌ **not built** (crash-equivalent: `TestNormalizeIsAtomicWithTheInboxTransition` covers the *normalise* transaction, not the ingest one) |
| 2 | **Ingest — dedupe** | SIGKILL immediately after a duplicate is detected and before the response is written | A2: still exactly one row; the client retry is still a no-op and still *not* an error. A duplicate insert is a no-op, never a fault | Ingest DoD | ❌ not built (`TestWriteAcceptsOnce`, `TestDedupeCrossesPartitions` cover the non-crash behaviour) |
| 3 | **Normalize** | SIGKILL after the canonical event insert, before the inbox transition commits | A1: on restart, the key is reclaimed, normalisation is a **retry**, `AlreadyNormalized` is true, and the total is **one** canonical event | Normalize DoD, invariant 1 | ⚠️ **crash-equivalent only** — `TestReNormalizeIsANoOp`, `TestNormalizeIsAtomicWithTheInboxTransition` set up the states; neither kills a process |
| 4 | **Outbox — enqueue** | SIGKILL after `outbox.Enqueue` inside the incident transaction, before commit | A2: a rolled-back transaction fires no side effect; a committed one fires exactly one delivery per idempotency key | Outbox DoD | ⚠️ crash-equivalent only — `TestOutboxEnqueueIsTransactional` forces a rollback with a returned error, which is a *graceful* rollback, not a power loss |
| 5 | **Outbox — relay** | SIGKILL after the handler succeeded and before `mark_delivered` commits | A2: the message is delivered **again** (at-least-once is the guarantee) and the handler is idempotent, so the external effect happens **once**. This is the test that proves idempotency rather than assuming it | Outbox DoD | ❌ not built. `TestOutboxDeliversAtLeastOnceAndTerminally` proves no *duplicate on a second clean pass*, which is a different claim |
| 6 | **Job claim** | SIGKILL after a batch is claimed, with the lease still valid | A2: nothing is lost, nothing is double-processed; once the lease expires the work is reclaimed with `Attempts` incremented, and the recovering worker is not the dead one | Job DoD, invariant 1 | ⚠️ crash-equivalent only — `TestJobClaimIsExclusiveAndRecoverable` expires the lease by `UPDATE` |
| 7 | **Evidence write** | SIGKILL after the content hash is computed and before the bytes are fully written | A2: a partial file is **not evidence**. `TestStagingFilesAreNotEvidence` already encodes the design (staging is not the root); the fault-injection test proves that a kill leaves only staging, and that a later `Open` of a truncated object reports rather than serving garbage. A2 on the DB side: no `evidence` row without a verified object | Evidence DoD | ❌ not built. `TestAlteredFileIsReportedNotServed` and `TestPutIsBounded` cover the non-crash behaviour |
| 8 | **Action execute** | SIGKILL after the external call succeeded and before the "after" audit row commits | A2: the action's idempotency key is derived **without reading the side effect's own result**, so the retry does not repeat the physical effect. And per `01` M7's closed loop: an action whose success is not verified is a **failed action**, recorded as one, not as a completed one | Action DoD, invariant 1, M3 gate | ❌ not built. M3 work; the idempotency design must be proven before the first physical action ships, not after |
| 9 | **Audit seal** | SIGKILL mid-`SealRange`, and separately: the seal is written and the database is then lost | Invariant 4: a partially-written seal file is refused by `TestFileSinkRefusesUnsignedAndUnsafe` and by `TestVerifyRejectsMalformedSeals`; a seal that survives is verifiable offline with the trusted key; **a restored ledger is provably behind the seal and says so** | Audit DoD, invariant 4 | ✅ **`TestRecoveryDrill` in `internal/dr`** — the closest thing to the real thing in the tree. It is a *test*, not a runbook, "so that it cannot quietly stop being true", and it produces the finding that a backup is a snapshot of an earlier moment |

### 5.2 Order and cost

The order is by value per engineer-week, not by pipeline order:

1. **Stage 8 (action execute)** — before M3's first physical action exists. If
   idempotency is not proven before the PTZ preset ships, the first customer who
   pulls the network cable during a PTZ move gets a camera pointed at the wrong
   place and there is no way to argue about it afterwards. **Highest priority in
   the whole quality programme.**
2. **Stages 3, 4, 6 (normalize, outbox enqueue, job claim)** — the harness is
   built once and the three tests are variations on a stage name. Roughly 2
   engineer-weeks for the harness and 0.5 each after.
3. **Stage 5 (outbox relay)** — this is where "at least once" becomes real, and it
   is the stage most likely to have a live bug, because a relay that crashes
   after a webhook is the classic duplicate-notification defect.
4. **Stage 7 (evidence write)** — needs the staging design to be final.
5. **Stages 1, 2 (ingest)** — lowest value, because the ingest transaction is
   short and the crash-equivalent test covers the interesting failure.

**Total: about 4–5 engineer-weeks to build the harness and all eight tests.**
That is roughly one seventh of the M1 new-build budget and it is the single
largest unbudgeted quality liability in the plan. It belongs in M1, and if the M1
cut is applied, it is the **last** thing cut and never below the four stages that
already have a partial substitute.

### 5.3 Rules for the programme

- **A stage without a fault-injection test is not covered.** `docs/status.md` may
  not list it, and the threat model's control-to-test mapping may not point at a
  crash-equivalent test as if it were a fault-injection test. The two are
  different tests and a mapping that conflates them is a false claim.
- **Every fault-injection test runs in `make test-race`**, not in a separate
  target nobody runs. They are slower; that is the cost of the guarantee.
- **The child process is a real process, not a goroutine.** A goroutine cannot be
  killed without cooperation, and a cooperative kill is the one thing this tier
  is not testing.
- **The reference run is in the same test.** A test that asserts a hand-written
  expectation has encoded the bug it was written next to. Run the stage clean,
  capture, kill, recover, capture, compare.
- **The test must fail when the control is removed.** Delete the
  `canonical_event_keys` insert from the normalise transaction and stage 3 must
  go red. A fault-injection test that passes on a broken control is worse than no
  test, because it consumes the time and produces a green checkmark.

---

## 6. Path traversal and evidence-path security testing

### 6.1 The rule

`AGENTS.md`: "Path traversal (`../`, absolute paths, null bytes, symlink
escapes) needs an explicit negative test on every evidence path, **on every
change**."

Two parts, and the second is the one people drop. A path handler is tested once
when it is written, and the handler is edited eleven times; the eleventh edit is
where someone adds a `filepath.Clean` and a Windows drive-letter check falls out.
**"On every change" means the negative test is re-read and re-run as part of any
diff that touches a path, a ref, a root, or a join.** Practically: any diff
touching `internal/evidence`, `internal/release`'s path validation, the
`storage_ref` schema, or the fixture scenario name must include the four negative
tests in its own diff, or the review rejects it.

### 6.2 The checklist

Every evidence path — that is, every function that turns untrusted input into a
filesystem path — must have all four, and the answer to "which function is this"
is "any function whose parameter is a ref, a path, a filename, a scenario name or
a manifest entry".

| # | Attack | Refused by | Test | Today |
|---|---|---|---|---|
| 1 | `../` traversal, at every depth | string check before any filesystem call, and again on the cleaned name | `evidence`: `TestValidateRefRejectsEveryTraversalForm` (asserts `../etc/passwd`, `../../etc/passwd`, `a/../../b`, `a/b/../../../c`, `..`, `.`); `release`: `TestValidateFilePathRejectsEveryEscape`, `TestPathTraversalEntryIsRefused`; `fixture`: `TestScenarioNameRejectsPathTraversal` | ✅ |
| 2 | Absolute path — POSIX and Windows | same, plus drive letters and UNC prefixes | `TestValidateRefRejectsEveryTraversalForm` (`/etc/passwd`, `/`, `C:\Windows\System32\config`, `\\server\share\evidence`); `release`'s `unsafePath` regexp `(^/)|(\.\.)|(^~)|([A-Za-z]:)|(\\)` | ✅ |
| 3 | NUL byte | explicit, because a NUL truncates the path in every syscall taking a C string | `TestValidateRefRejectsEveryTraversalForm` (`a\x00b`) against `evidence.ValidateRef`; `schemalaws`: `TestEvidenceStorageRefIsAlwaysRelative` | ✅ |
| 4 | Symlink escape — a link *inside* the root pointing out of it | the one string validation cannot catch, and the one that matters most, because planting a symlink is a legitimate filesystem operation for whoever has write access to the directory | `TestSymlinkEscapeIsRefused` | ✅ **⚠️ skips on Windows** |

**The Windows skip is a real hole and it is a Sev-1-shaped hole.** A test that
skips on the platform that runs half our target installs is a test that is not
protecting half our customers. `internal/evidence/evidence_test.go` skips with
"symlink creation on Windows needs a privilege this test does not assume".

Three options, in order of preference:

1. **Request the privilege in CI.** A Windows CI runner can be granted
   `SeCreateSymbolicLinkPrivilege` for the account. The test then runs
   everywhere. Cheapest, correct, and the only option that does not leave a
   hole.
2. **Use a junction or a directory reparse point** where a symlink cannot be
   created, which needs no privilege. Covers the escape; does not cover a file
   symlink.
3. **Test `Open`'s post-open `Lstat` check directly** by constructing the state in
   Go and asserting the refusal, so the *logic* is covered on Windows even if
   the *filesystem setup* is not.

At least one of the three ships in M1. A Windows site with a symlink escape in the
evidence store is a site whose evidence can be read by anyone who can create a
file in the store, and evidence is the thing the whole product exists to keep
intact.

### 6.3 The rest of the evidence-store test surface, and what each protects

| Test | Protects |
|---|---|
| `TestPutAndOpenRoundTrip` | the basic path |
| `TestIdenticalBytesDeduplicateAndCannotCollide` | content addressing: two tenants' identical bytes must not collide, and deduplication must not become a disclosure channel |
| `TestAlteredFileIsReportedNotServed` | a changed file is **detected**, not served. This is the evidence-integrity property and it is separate from path safety |
| `TestOpenRejectsTraversalAndReportsMissing` | `Open` is safe, not just `ValidateRef` — a caller that bypasses the validator is still caught |
| `TestPutIsBounded` | a size cap, so a bundle or a bundle-author cannot fill the disk |
| `TestPutRefusesAnUnusableTenant` | a tenant id that would produce a path outside the root is refused |
| `TestSweepRespectsRetentionAndLegalHold` | retention never deletes held evidence |
| `TestSweepFailsClosedWhenLegalHoldCannotBeChecked` | **a sweep that cannot determine legal hold deletes nothing.** Fail-closed on the one operation that is irreversible |
| `TestStagingFilesAreNotEvidence` | a partially-written file is not in the root, so a crash cannot leave a truncated object that looks like evidence |

`TestSweepFailsClosedWhenLegalHoldCannotBeChecked` is the one to remember when
reviewing a new destructive operation in this codebase: **the default for a
destructive action whose precondition cannot be evaluated is to do nothing.**

### 6.4 The review rule

```
RULE: a diff touching any of
      internal/evidence/**
      internal/release/  (ValidateFilePath, unsafePath, archive extraction)
      internal/fixture/  (Scenario name, store paths)
      contracts/json-schema/**storage_ref**
      migrations/*.sql   (any storage_ref or evidence column)
      the evidence root configuration
must include, in the same diff:
      □ the four negative tests, re-read and passing
      □ a line in the ticket naming which function is the evidence path
      □ a note if a new filesystem call was added
A review that cannot find these rejects the diff.
```

---

## 7. AI-output quality testing

The harness is `internal/evaluation`, and its doc comment states the reason it
exists: "the alternative is shipping a model because it felt right."

### 7.1 The gates, named individually

`DefaultGates()` — the bar for a production profile in M1:

| Gate | Value | Why this value |
|---|---|---|
| `MaxFalsePositiveRate` | 0.20 | "an operator who is shown noise twice a week learns to dismiss the feed, and a feed nobody reads is worth nothing whatever its recall" |
| `MinRecallOnTruePositives` | 0.70 | "catches a profile that is precise by being quiet" |
| `MaxMedianLatencyMS` | 8000 | "a profile that adds thirty seconds to every incident is a profile operators learn to work around" |
| `MaxDisagreementRate` | 0.25 | "either the profile is wrong or its output is not legible, and both are reasons not to promote it" |
| `MinLabelledCases` | 30 | "a rate computed from four cases is a number, not an estimate" |

`ShadowGates()` is the same with `MinLabelledCases = 10`, because the point of
shadow evaluation is to collect cases, not to promote.

**Every gate is checked independently and every failure is named.** A profile
that is precise and slow has *three* failures listed, not a score of "half
passed" — `evaluateGates` returns `[]string` and `TestEveryFailingGateIsNamed`
asserts it. `Report.Passed` is `len(pr.Failures) == 0`; there is no averaging,
and averaging gates is exactly how a profile nobody should ship gets promoted.

### 7.2 The eleven harness tests, and the three that are load-bearing

`internal/evaluation/evaluation_test.go`: `TestAPassingProfileIsEligible`,
`TestNoisyProfileIsNotEligible`, `TestSilentProfileFailsRecall`,
`TestTooFewCasesFailsTheGate`, `TestSlowProfileFailsLatency`,
`TestDisagreementFails`, `TestEveryFailingGateIsNamed`,
`TestAvailabilityIsExposedForPilotReporting`, and three that encode invariant 3
inside the measurement layer:

- **`TestUndispositionedCasesAreNeverFoldedIntoARate`** — a case nobody judged is
  counted as `Undispositioned`, separately, and is never folded into a
  denominator. "A case nobody judged is not evidence, and counting it as anything
  is how a harness makes a profile look better than it is."
- **`TestAbsenceIsNotAPrediction`** and **`TestInsufficientEvidenceIsNotAPrediction`**
  — `predictedRestricted` returns `nil` for a missing field, a degraded
  observation and an insufficient-evidence observation, and `nil` is counted as an
  absence rather than as a false. The comment: "That is the invariant, expressed
  once."

**The boundary, stated precisely.** A missing prediction is not a false negative
and not a true positive. It is counted in `Undispositioned`. This is why
`pr.Recall = rate(pr.DetectedRestricted, pr.TruePositives)` has a numerator that
can only come from a case where an observation actually made a claim — a profile
that stays silent gets `Recall: 0` and fails `MinRecallOnTruePositives`
(`TestSilentProfileFailsRecall`), and a profile that is unavailable gets a
degraded count and no recall penalty, because it did not fail to answer; it was
not asked in a way that produced an answer.

### 7.3 The "no golden on prose" boundary, in testing terms

| Assertion type | Golden-tested? | Tested by |
|---|---|---|
| Structured field values (`restrictedZoneBreach`, `personCount`) | **Yes**, when the observation is a *fixture* | `TestEvaluationIsDeterministic` |
| `output_hash` of an observation | **Yes** | `TestOutputHashIsStableAndRecomputable` |
| Degraded / insufficient flags | **Yes** | `TestDegradedObservationIsPersistedAndFlagged`, `TestInsufficientEvidenceIsNotANegativeAnswer` |
| The rule engine's `Explanation` (composed from structured fields and the reason code) | **Yes** — deterministic by construction | golden chain, §4 |
| Model prose (`observation.structured.narrative` or any free-text field) | **No. Never.** | by review; the `AIObservation` type has no free-text field at all |
| Operator's "did you agree with the AI" (`Disposition.Agreement`) | **No** | it is an input to the evaluation, not an output of the system |
| Latency | **Never golden.** Measured, thresholded. | `TestSlowProfileFailsLatency` |

The structural reason the last row of the table matters: `internal/rules`'s
`AIObservation` type has **no free-text field at all**. A rule *cannot* match model
prose because prose is not in the type the rule sees. That is a better control
than a review rule, and it is why the boundary has held so far.

### 7.4 Shadow mode

`internal/observations`: `TestShadowProfilesAreMarkedAndProductionIsNot`,
`TestShadowProfilesCannotAffectAuthoritativeState`, and at the database level
`TestAIRuntimeCannotAlterAuthoritativeState` and
`TestShadowProfilesHaveNoAuthoritativeEffect`. The AI plane's role cannot write
authoritative state at all — a grant-level control, not a code-path convention.

**Shadow is how a profile earns the right to be authoritative.** The sequence is
fixed and none of it is skippable:

```
1. declared         profile spec exists, output schema committed, model and
                    prompt pinned, degradation behaviour declared
2. evaluated offline against a labelled dataset, gates run
3. shadow           runs on live traffic, produces observations, affects nothing,
                    marked shadow everywhere it is displayed
4. 14 consecutive days at a design-partner site, evaluation report attached
5. human promotion decision, named and dated
6. production
```

Step 5 is a human decision. `Report.Promotable` says who is **eligible**; it
never says who was **promoted**, and no code path promotes.

### 7.5 The regression suite for a model or prompt change

When the model, the prompt template, the prompt version or the profile schema
changes:

| Step | What runs | What it decides |
|---|---|---|
| 1 | `make ci` in full | we did not break the deterministic core. **A model change must not change a single golden byte outside the observation fixture.** If it does, something is inferring at decision time and invariant 2 is broken |
| 2 | The profile's dataset evaluation, gates run individually | does the new version still pass. Every failing gate is named |
| 3 | **Side-by-side on the same dataset**: old version vs new version, per case | where they disagree, case by case. A new version that wins on aggregate and loses on a class of case the old one handled is a regression with a better average |
| 4 | Historical site data replay for the affected profiles | the disagreement rate on *this* site's traffic, not a generic dataset |
| 5 | Shadow for 14 days at the site, as for a new profile | the same bar. A prompt change is a new profile version, and it is gated like one |
| 6 | `TestOutputHashIsStableAndRecomputable` re-run against the stored observations | the observations already stored remain recomputable under the new version. **If a model change makes a stored `output_hash` unverifiable, invariant 2 is falsified and the change does not ship** |

**The contract, stated once:** *any change to a model, a prompt template, a prompt
version, a profile schema or a profile's dataset forces step 2 and step 5. There
is no diff size below which this is skipped, and no emergency release that
bypasses it. A change that touches the model without a re-evaluation is a Sev-1
quality incident against the product's claim, not a small omission.*

The one place this bites hardest: `01` M1's reduced scope says "ship with
ONVIF-agnostic synthetic fixtures and a demo site". Synthetic fixtures do not
make the VLM gate skippable. The gate is on a real profile on real GPU hardware
in shadow for 14 days, and if it cannot be met, gate 4 is not met and M1 has not
finished. That is uncomfortable and it is correct.

---

## 8. Test data strategy

### 8.1 The choke point

**The sanitiser is the single point through which any data from a live source
becomes a committed file.** `internal/fixture/sanitize.go`, `SanitizeResponse`
(`TestSanitizeResponse`), is that point, and there is exactly one of it. This is
stated in `docs/status.md` as a property of the build and it is the reason the
design holds: a second path into the repository is a second place to forget, and
"we were careful" is not a control.

Its tests are the specification of what it removes:

| Test | Removes |
|---|---|
| `TestRedactsSecretsByKeyName` | values whose key names a secret |
| `TestSecretKeyMatchingIgnoresCaseAndSeparators` | `api_key`, `apiKey`, `API-KEY` — "the same secret under a different spelling" |
| `TestRedactsIdentifiers` | tenant, site, device, camera and person identifiers |
| `TestRedactsURLs` | hostnames, internal addresses, and query-string tokens — "a URL whose key is innocuous still carries the site hostname and, often, a token in the query string" |
| `TestRedactsOpaqueLongStrings` | anything long and high-entropy that no key names, which is the residue a key-name approach misses |

`TestRedactsOpaqueLongStrings` is the one that stops the sanitiser from being a
key-name list, which is what most sanitiser implementations are.

### 8.2 The fixture format

```
testdata/fixtures/<set-name>/
  manifest.json        { formatVersion, generatedAt, sanitiserVersion, source,
                         scenarios: [{ name, epoch, covers: [...], files: [...],
                                       sha256: ... }] }
  <scenario>/
    request.json       the request that was sent, sanitised
    responses/*.json   the responses, sanitised, in order
    expected.json      what the adapter should produce
```

Guard tests: `TestStoreRoundTripAndManifestVerification`,
`TestVerifyManifestDetectsPostReviewEdit`,
`TestVerifyManifestDetectsMissingAndExtraScenarios`,
`TestCommittedFixtureSetVerifies`, `TestValidateRejectsMalformedFixtures`
(wrong format version, no name, no epoch), `TestScenarioNameRejectsPathTraversal`,
and `TestFixtureSetCoversEveryRequiredCase` — the last being the one that makes
the set's coverage an assertion rather than a claim: a required case with no
fixture "is a gap that is invisible until a pilot".

`manifest.json` carries `sanitizerVersion`, so a sanitiser change is visible in
the manifest diff and every fixture can be re-reviewed for the new rule.

### 8.3 Customer data, before anything else

**Order of operations for anything from a live system, and the order is not
optional:**

```
1  capture        from the site, on the customer's machine, by their operator
                  or by us under their supervision
2  sanitise       through internal/fixture.SanitizeResponse — the only path
3  human review   a person reads the output. The sanitiser is a filter, not a
                  reviewer, and its comment says "the alternative is shipping a
                  model because it felt right" about a different subject; the
                  same honesty applies here
4  record         sanitiserVersion, source, date, and the site reference, in
                  the manifest and in docs/requests/
5  commit         only after 1–4
```

**Never in this order: capture → commit → sanitise.** A commit of unsanitised
data is a customer-data disclosure, it is in the git history for ever, and there
is no supported way to remove it from a repo that has been pushed. There is no
exception, including "it's just a dev site" and "we'll delete it in the next
commit".

### 8.4 The absolute rules

1. **No real biometric data is ever in the repository or in CI.** Not a face
   crop, not a licence plate, not a gait sequence, not a voice sample, not a
   depth frame from a 3D camera, not a body silhouette. "Biometric" here means
   the data, not the intent: a blurred, deliberately degraded frame of a loading
   bay at 04:00 with no face, no plate and no person is site telemetry; a frame
   with a face in it is biometric data under every regulation this customer base
   operates in. The test is not "is it identifying" — it is "would a privacy
   officer need to know it exists", and for a face crop the answer is always yes.
2. **The evaluation datasets for M2's ten profiles contain no real faces.** The
   datasets are built from synthetic renders, from stock footage we licence with
   a recorded licence, or from real frames that have passed through an
   irreversible anonymisation step (pixelation, synthetic re-render, or
   replacement of the person entirely) — and the anonymisation is recorded in the
   dataset's provenance file, because an unrecorded anonymisation is an assertion
   rather than a control. Recorded licence and provenance per §9.5 of
   ``.
3. **The suite has no access to a customer's data.** Tests run on synthetic
   fixtures and committed datasets only. A test that needs a live site is a
   manual test with a person, and `AGENTS.md`'s rule generalises: **the suite
   must pass with no Milestone server reachable; if a test needs one, the test is
   wrong.** The same applies to a customer database, a customer VMS and a
   customer evidence store. This is now the only place the rule is enforced,
   since the pipeline does not run for this repository — which makes it more
   important, not less.
4. **`SP_TEST_DATABASE_PASSWORD` is a local test password** and is only ever
   present in a local shell or a CI secret store. It never appears in a committed
   file, a log line or a test failure message. A failing test that prints a DSN
   with a password in it is a Sev-2 quality incident.
5. **Research artefacts go through the same sanitiser.** Shift-shadowing notes,
   interview transcripts and incident-review samples are customer data. They
   live in `docs/research/YYYY-MM/` sanitised, and the raw recordings are
   deleted on the stated date (`` §5.2).
6. **Retention of test data is bounded.** Golden sets are permanent because they
   are inputs to a reproducibility claim. Evaluation datasets are versioned and
   superseding is a new directory, never an edit. Everything else — captured
   responses, load-run artefacts, shadow-run logs — has a stated deletion date at
   creation.

---

## 9. The release quality gate

### 9.1 The ordered checklist

Run in order. Each line has exactly one signer. A line signed by the person who
did the work is not a gate; the value of a gate is that a second person is looking
at the evidence.

| # | Check | Command or artefact | Signer | Fails if |
|---|---|---|---|---|
| 1 | Contracts unchanged or intentionally versioned | `make contract-check`; a diff to `contracts/compat/baseline.json` must cite an ADR | E2 | the wire moved without a decision |
| 2 | Embedded schemas match published ones | `make contract-sync` | E2 | a bundle is validated against a contract nobody published |
| 3 | Migrations legal and forward-only | `make migration-phases`; `internal/migrate`'s five tests | E2 | a contract step is bundled with an expand step, or a phase is unknown |
| 4 | Built-in bundle compiles with no server | `make rules-lint` | E2 | `AGENTS.md`: the suite must pass with no Milestone server reachable |
| 5 | Vet and lint clean | `make vet && make lint` | E2 | — |
| 6 | Full suite green with `-race` | `make test-race` | E2 | any test fails **or any test skips** (see 9.2) |
| 7 | Fault injection green for every stage a change touches | `go test ./internal/faultinjection/... -stage <stages>` | E2 | a stage has no test, or a test passes on a control that was removed |
| 8 | Path-traversal negatives re-run and read | the six tests of §6 | E2 | any diff touched a path and did not re-run them |
| 9 | Release archive builds and verifies | `securityctl build-release` then `securityctl verify-release` | E2 | any file unlisted, any path unsafe, any secret-shaped material, foreign key |
| 10 | Contract surfaces documented and diffed | the API diff, the rule-language diff, the migration diff | E1 | an undocumented change a customer could depend on |
| 11 | Golden set byte-stable, or the diff reviewed line by line | `git diff` on `testdata/golden/expected/` | E2 | a golden changed without a behaviour change behind it |
| 12 | Every AI profile in the build has a spec and current gates | the evaluation report | E1 | a profile is in the binary without a passing report, or a shadow profile is presented as production |
| 13 | Migrations rehearsed against a copy of production data | a rehearsal on a restored copy, timings recorded | E2 | a migration is untested at production volume |
| 14 | Upgrade and rollback rehearsed, including a deliberate failure | on a disposable site image | E2 | a half-upgrade is reachable |
| 15 | DR drill run against this build | `TestRecoveryDrill` plus a manual run with the runbook | E2 | the finding changed shape and nobody noticed |
| 16 | Console renders with real services, keyboard-only, all five states | a human pass over the milestone's screens | E1 | a screen has no empty state, or is not keyboard-reachable |
| 17 | Accessibility: automated pass plus the manual checks | axe over every template, plus §7 of `06` | E1 | a contrast target, a focus ring, a target size or a label is missing |
| 18 | No Sev-1 or Sev-2 open; every Sev-3 has an owner and a date | the tracker | E1 | see 9.3 |
| 19 | Known limitations and unverified claims are stated in the release notes | the notes | E1 | anything unstated that a pilot could discover |
| 20 | `docs/status.md` updated in the same change | the diff | E1 | the status doc and the code disagree — `docs/status.md` is the source of truth, and a stale one is a false claim |

### 9.2 Two rules about "green"

**A skipped test is a failure.** Database-backed suites skip when
`SP_TEST_DATABASE_URL` is absent, with a message — that is the correct behaviour
for a developer's first five minutes and it is unacceptable in CI. **CI fails if
any test skips**, with a `go test -json` check on the skip event. A green CI run
where the database suites skipped is the exact failure mode this rule exists to
prevent: it looks like a pass, it has asserted nothing, and it is the shape of the
"0.0000% event loss" mistake the SLO's own comment warns about.

**A test that has never been seen to fail is not a test.** For every new test
added in a release cycle, the author demonstrates the failure once — revert the
control locally, run the test, show the red, restore. Not in the PR description as
a claim. Run. This is the only defence against a test that asserts nothing and
passes.

### 9.3 The definition

> **No known Sev-1 or Sev-2, and every Sev-3 is triaged with a named owner and a
> date.**

Precisely, so it cannot be satisfied by assertion:

- **Sev-1 / Sev-2**: zero open, anywhere, including "open on a customer's site,
  mitigated, not fixed". A mitigation that has not been merged is not a mitigation
  a customer is running. A workaround documented in a support ticket is not
  fixed.
- **Sev-3**: each has a named human and a date. Not a milestone. A date. A Sev-3
  with "next quarter" has no date and is reclassified as an unowned bug.
- **Sev-4**: counted, not triaged. Logged, fixed opportunistically, never
  blocking.
- **A known limitation is not a bug and does not block a release**, but it must be
  written in the release notes with the scope of its effect, because a pilot that
  discovers it is a customer who now has a question about the claims. Examples
  that must be stated until they stop being true: the Milestone endpoint surface
  is unverified; the Python VLM runtime is absent from a build that has it
  enabled; `deploy/` is absent; `install`/`upgrade`/`rollback`/`backup`/`restore`
  are stubs.
- **An exit gate is not a release gate.** M1's four gates are milestone gates
  signed with artefacts. A release may ship with gates outstanding — that is what
  a design-partner build is — provided the release notes say which gates are
  outstanding. A release that ships *implying* a gate is met is the failure this
  line exists for.

---

## 10. Bug triage

### 10.1 Severity, specific to this domain

The generic scale does not survive contact with a security product. A missed
"nice to have" in a SaaS dashboard is a bug report; a missed real intrusion in
this product is somebody's building. These are the definitions, and the severity
is assigned by **what it does to the truth of a record or to a customer's
security**, not by how many users are affected or how visibly it fails.

| Severity | Definition | Examples |
|---|---|---|
| **Sev-1** | The product asserts something false about a security event, loses an accepted event, takes an action it was not authorised to take, or a customer cannot prove what happened | · A real intrusion that was not raised, was suppressed, or was auto-dismissed<br>· A real incident an operator dismissed on our evidence and that turns out to have been real<br>· A lost accepted event: anything reaching `inbox_events` that did not reach a terminal state<br>· **A wrong action on a camera**: PTZ to a person, a lock opened, a notification to the wrong recipient<br>· An unverifiable audit row, a broken chain, a seal that will not verify under the trusted key, an edited record<br>· Evidence loss, corruption, or an altered object served instead of reported<br>· An authentication or authorisation bypass: a cross-tenant read, an RLS escape, a session that survives a password change, a step-up that was not required<br>· A degraded path presented as a working one — a missing AI observation rendered as a negative (invariant 3)<br>· A release archive that verifies against a key it should not |
| **Sev-2** | The product works but is materially wrong, and a customer would be misled or slowed | · A false-alarm campaign: the false-positive rate above a gate, sustained<br>· A wrong disposition recorded with correct attribution — the audit is right and the outcome is wrong<br>· The AI plane taking down a surface it is not permitted to take down (invariant 3)<br>· A dead-letter storm, or a poison message blocking a queue<br>· A restore that is behind the seal and nobody noticed for more than one shift<br>· The evidence store full and fetches failing silently<br>· `install`/`upgrade`/`rollback` failing on a supported platform<br>· A path-handling defect that a negative test would have caught (§6) |
| **Sev-3** | Degraded, bounded, with a workaround that does not mislead | · A dead console link, a wrong breadcrumb, a 404<br>· A missing keyboard shortcut<br>· A contrast or target-size failure below AA<br>· A screen missing an empty or partial state in a case that does not occur in practice<br>· A metric that is named but not emitted<br>· A runbook step naming a command that does not exist (`TestDrillRunbookNamesRealCommands` exists because this happens) |
| **Sev-4** | Cosmetic, no operator consequence | Wording, spacing, a glyph |

**Two assignments worth naming.** A missed real incident is Sev-1 even if exactly
one customer at one site and even if we found it ourselves a week later. A false
alarm is Sev-2, not Sev-1, **unless** it is sustained past a promotion gate,
because the harm is the operator learning to dismiss the feed, and that harm is
compounding and permanent per operator.

### 10.2 Response SLIs

| Severity | Detect | Triage | Mitigate | Root cause |
|---|---|---|---|---|
| **Sev-1** | ≤ 1 hour, or on discovery by any means | ≤ 2 hours, one named human on the incident | ≤ 4 hours | ≤ 5 working days |
| **Sev-2** | ≤ 1 working day | ≤ 1 working day | ≤ 5 working days | ≤ 3 weeks |
| **Sev-3** | next review | 1 week | scheduled, with a date | scheduled, with a date |
| **Sev-4** | backlog | backlog | opportunistic | backlog |

**These are commitments about our own attention, and they are only honest if
someone is awake.** With two people, a Sev-1 at 03:00 is answered by whichever of
the two is on the call rotation, and "≤ 1 hour to detect" is a statement about
monitoring, not about staffing. Two consequences, both accepted:

1. **Detection of a Sev-1 must be automatic and must not depend on a human
   noticing.** The `dead_letter_count` gauge, the `ai_unavailable` gauge, the
   `evidence_disk_free_percent` gauge and the seal-verification result are the
   detectors. A Sev-1 that is discovered by a customer is a detection failure as
   well as a product failure, and it is logged as both.
2. **From M3 GA, the on-call rotation is a contracted commitment and it is
   priced.** Before M3, support is business hours on the founder's own timezone
   and the customer is told so in writing (§7 of
   ``). Pretending otherwise in a contract is the
   single worst thing we could do to a company that is buying us for
   provability.

### 10.3 The Sev-1 protocol, for a team of two

The general protocol does not scale to two people, so here is the one that does.

```
T+0    STOP THE LINE. No release ships. A named person is the incident
       commander (IC) and the other is the operator of the record. The IC does
       not write code.

T+0.5  TELL THE CUSTOMER. Before we understand it. This is not optional and it
       is not negotiable, because the alternative is a customer discovering a
       gap in their evidence chain and no longer trusting a single row we
       produce. Say: what we observed, what we believe, what we are doing, when
       we will next update. No cause, no confidence, no "we are investigating
       thoroughly" — facts and a clock.

T+1    PRESERVE. Stop any retention sweep. Copy the evidence store, the
       database, the checkpoint sink, the rule bundles and the release
       manifests, before anything is cleaned up. An investigation that runs a
       sweep is an investigation that destroyed the evidence. If the
       configuration allows it, stop the site services too: a live system
       keeps mutating what you are reading.

T+2    SCOPE IT AGAINST THE INVARIANTS. Which of the five is broken? Write it
       down. A Sev-1 that has not been named against an invariant is not
       triaged, and the ADR in step T+5d will be impossible to write.

T+4    CONTAIN. The smallest reversible action that stops the harm. Roll back
       the release; deactivate the bundle; drop the site to A2; stop the action
       queue. Prefer the blast radius of one site over a clever fix.

T+24   FIX + TEST. The code fix, AND the test that fails when the control is
       removed, in the same change. If the test is not there, the fix is not
       finished. Run the fault-injection test at the stage the defect was in,
       even if the defect was not obviously a durability problem — it usually
       is.

T+72   COMMUNICATE. Full write-up to the customer: what happened, what it means
       for their records, what we changed, what we will never do again, and the
       invariant it broke. For a missed intrusion, they may have a
       notification obligation. They must have ours first.

T+5d    ADR. What we believed, why it was wrong, what we changed, what test now
       holds it. In that order, per AGENTS.md. Then the retrospective: was the
       defect in the design, in the review, in the test suite, or in the
       product's honesty about what was verified? Write down which, because the
       fix is different in each case.

T+30   30-day review. Read the ADR and ask whether the same class of defect is
       still reachable. Sev-1 fixes have a habit of being symptoms.
```

**The rule about the two of us.** A Sev-1 means the IC does not write code, does
not run the deploy, and does not talk to the customer. That is the only
division of labour in the whole document, and it is the one that matters: the
person who understands what just happened is the person who should be
interrogating it, and at 03:00 with one other human, the temptation to do both is
what turns a four-hour Sev-1 into a four-week one.

---

## 11. QA capacity reality

### 11.1 What two people can and cannot test

`00` §6: 2 engineers, 13 weeks a quarter, ~60% on new build, 40% on the tax
(support, demos, sales calls, incident response, hiring, admin, the AI review
pass). Testing is not a separate line item — it is inside the 60%, and every hour
of test written is an hour of feature not built. The realistic budget:

| We can afford | We cannot afford |
|---|---|
| A fast, wide unit suite that runs per commit | A browser-driven UI suite. The console has no JavaScript, so a real E2E harness would be testing HTTP, and `internal/api/api_test.go` already is that |
| The full database-backed suite per commit at ~20 minutes with `-race -p 1` | Per-commit fault injection for all eight stages. The harness is per-commit; the full sweep is nightly |
| Golden tests that are byte-stable and never regenerated casually | Golden tests that re-record. A regenerated golden is a deleted test |
| The four path-traversal negatives on every evidence-path change, without exception | An exhaustive path-fuzzing campaign. Four forms plus a review rule is the right spend; a fuzzer over `ValidateRef` is a nice-to-have, not a control, because the four forms are the four attacks |
| Security tests mirroring the 35 threat-model controls, mapped and reviewed | A penetration test. **We buy one before M3 GA from someone outside the company** — the first outside pair of eyes on the identity, the release and the evidence path is worth more than a year of our own tests, and the threat model is the scope document we hand them |
| One unmoderated usability round per milestone | Continuous exploratory testing |
| AI-output evaluation on a labelled dataset per profile | Per-observation human review of model output outside the audit trail |

### 11.2 Where the AI agent fits, and where it does not

The strategy is explicit: "AI multiplies implementation, not judgement. AI
writes most of the code and most of the first-draft tests. It does not decide what
to build, decide whether a decision is defensible, talk to a customer, or sign an
exit gate."

**Good AI work in QA, and it is a lot of it:**

- Generating the first draft of a test table from an invariant, then the human
  reading the test and deleting the ones that assert nothing. Most tables in §3
  started as a prompt and became something a person edited.
- Generating the negative cases for a path validator, a JSON schema, a command
  dispatcher or an HTTP handler. Enumerating `a/../../b`, `a/b/../../../c`,
  `a\x00b` and `\\server\share\evidence` is exactly the mechanical work an LLM is
  good at, and exactly the work a human forgets the fourth form of.
- Writing the fault-injection harness's boilerplate: child process, pipe, kill,
  restart, teardown. ~200 lines of scaffolding per stage that nobody should hand-write.
- Mutants. A test that survives a mutation of the code it covers is not testing
  that code. An agent generating plausible mutations — a flipped condition, a
  dropped error check, a changed comparison — is the cheapest way to find
  vacuous tests, and vacuous tests are the failure mode of an AI-written suite.
- Generating the migration rehearsal steps, the load-run script, the matrix
  assembly.
- Reviewing a diff against the ticket's `test_that_fails` field and saying which
  of the eleven required fields is not evidenced.

**Work an AI agent may not do here, and this is a hard line:**

- Deciding whether a control is adequate. It cannot know which control a rule is
  supposed to enforce.
- Writing a test that will be merged unseen. Every generated test is read by a
  human before it lands, and the human's job is to be able to answer "what does
  this fail on" for each assertion.
- Anything that touches a `green` marker in `docs/status.md`. Only a human
  promotes a capability to green, because green means "covered by a test that
  would fail if it broke" and that is a claim about adequacy, not coverage.
- Signing an exit gate. `00` §6: every exit gate is signed by a human.
- Writing the test for a durability change on its own. The kill -9 harness's
  *assertions* are where the knowledge of what the invariants mean at that stage
  lives, and that knowledge is E2's.

### 11.3 Coverage theatre — the list we refuse

Named, because the temptation arrives with a well-meant suggestion and a
dashboard, and because a metric that looks like rigour is the most effective way
to stop looking at the things that matter.

| Refused | Why |
|---|---|
| **A line-coverage percentage, and any threshold on one** | Coverage measures which lines ran, not whether the assertion was meaningful. The existing `TestThresholdsAreDefinedAgainstRealNumbers`-style threshold is the thing we would be tempted to build, and the AI-generated tests that satisfy it are the reason the rule-differentiation is hard. We already have a coverage gate: `TestFixtureSetCoversEveryRequiredCase` asserts the *cases* exist, and every capability in `docs/status.md` marked `green` names a test that fails if it breaks. That is coverage. |
| **A coverage dashboard nobody reads** | A dashboard is a thing that goes stale quietly. `TestEveryPlannedMetricExists` and the closed `knownMetrics` set in `internal/observability/metrics.go` are the right shape for observability: a typo fails a test. A coverage chart has no such property. |
| **Exhaustive UI automation** | The console has no client JavaScript. A Playwright suite would be testing `httptest`. Twelve targeted tests on rendered HTML — CSP, security headers, `dir="ltr"`, no `<script>`, no `style=`, cookies, CSRF, body cap — cover the whole attack surface of a server-rendered page. A hundred more would cover a hundred things we do not have. |
| **Mock-everything end-to-end** | The opposite of what this repository does. `internal/api/api_test.go` wires real services over a real pool and the comment says why: "a nil service behind a route is exactly the shape of a defect the console would hit in production." A mocked E2E passes while the migration runner, the RLS policy and the trigger are all fiction. |
| **Mutation testing as a gate** | Mutants are the tool (§11.2), not the scoreboard. A mutation score on a codebase with a database is noisy and slow, and a person generating the ten mutations that matter beats a number over ten thousand. |
| **A test for every branch** | The rule in `AGENTS.md` is a list of *areas*: rule compilation, semantic validation, time-window and skew logic, conflict precedence, the verification state machine, clock drift normalisation, tenant scoping, RLS, job lease/claim races. Covering those areas thoroughly beats covering every branch of every function, and the areas are the things that actually fail. |
| **Fuzzing in CI on every commit** | `ValidateRef` and the JSON decoders are worth fuzzing, as a nightly, forever. Fuzzing at commit time on a two-person machine costs more wall clock than it finds. |
| **A performance benchmark suite with recorded numbers in the docs** | `README.md`'s evidence rule: "Market claims, benchmark numbers and vendor comparisons in the source material were never verified. Until they are, they are marked unverified with a source and a date, and are not asserted in the product, the docs or the code." M1 gate 3 wants every SLO met under a **recorded** load run with the run and its numbers in the release notes. A synthetic benchmark in CI is exactly the unverified number the rule forbids, and it is worse than nothing because it is precise. |
| **Automated accessibility at every commit on every template** | Cheap enough to keep, but it is a completeness argument, not a sufficiency one, and it is a false assurance if it is the only thing. The automated pass is per-commit; the manual keyboard and screen-reader pass is per-milestone and is signed by E1. |

### 11.4 Open DoD-blocking defects

Defects found while writing this document that block a green milestone. Each is
a §1.2 **quality incident**, not a product incident, and each is listed here so
that "green" in `docs/status.md` is not read as "nothing is wrong".

| # | Defect | Where | Why it blocks | Fix |
|---|---|---|---|---|
| Q-1 | `correlation_under_drift` states its target as "fewer than 1% of devices in the severe band" and implements `Breached: GaugeClockSevereDevices > 0`. A 2,000-camera site with one clock that drifted 45 seconds breaches an SLO that claims a 1% budget, i.e. 20 devices | `internal/observability/slo.go:115-122` | An alert that fires on one bad camera in a large site trains the operator to ignore it, and then it fires on the twentieth camera too. Every SLO breach is a page to a human at 03:00, and our SLO budget at two people is small | The code to match the stated target: the gauge is a *count* of severe-band devices, so the check needs the total device count as a denominator, or the target is restated as "fewer than 1 device in the severe band per 1,000", which a count can express. **The target is the better statement**, so the code changes: `GaugeClockSevereDevices / total_devices > 0.01`. Needs a new total-device gauge, which means a new metric in the closed `knownMetrics` set, which means a change to `TestEveryPlannedMetricExists` |
| Q-2 | `dead_letter_visibility` targets "visible within 60s" and implements `Breached: GaugeDeadLetterCount > 0`. It is measuring the *existence* of a dead letter, not whether one became visible within 60 seconds | `internal/observability/slo.go:127-134` | The comment above it says the requirement "is about the operator being able to see it, and only an operator can say so" — and then implements something an operator does not have to say. A dead letter that appeared at 03:00 and was read at 09:00 is invisible as a breach | Either (a) the SLO measures operator acknowledgement, which means a new field and a new read path, or (b) the target is restated to what the code actually checks — "a dead letter is visible in the console immediately" — which is a weaker and entirely achievable promise. **(b) is the honest option for M1** and (a) is M3, when the exception queue makes acknowledgement an event |
| Q-3 | The AI plane has no production call site. `internal/observability` is imported by exactly one file: its own test. Every metric name in `metrics.go` is therefore *named but never emitted*, and there is no `/metrics` endpoint | `internal/observability/`, `internal/api/server.go` | `01` M1 gate 3 requires every SLO in `slo.go` demonstrably met under a recorded load run. An SLO whose inputs are never populated evaluates against zeros forever, and a green run would be a measurement of nothing | Wire the registry at ingest, normalise, verify, evidence, action, seal and adapter boundaries, and add a `/v1/metrics` handler on the Prometheus text exposition `Registry.Render()` already produces. `TestEveryPlannedMetricExists` already asserts the names; a new test must assert each is *reachable* from a code path, or the closed set is a wish list |
| Q-4 | `securityctl adapter probe` is referenced in five places in `internal/milestone/routes.go` — including "the first thing to run on a real install" — and the command does not exist in `cmd/securityctl`'s tree | `internal/milestone/routes.go:16,42,53,119` | The M1 exit gate's first item is live-Milestone parity, and the command written to start that conversation is not there. Whoever goes to a real installation runs a command that does not exist | Write it before the first site visit. It should print what the server actually exposes, what the adapter can reach, and what is unverified — which is precisely `TestMilestoneRoutesAreDeclaredUnverified` made operable |
| Q-5 | The console's dead-letter and clock-drift tables render from `consoleData` with `requirePrincipal`, not `requireCapability`, so a principal without `dead_letter.read` is shown the dead-letter list | `internal/api/handlers.go:158-163`, `internal/api/console.go:186-195` | A capability leak. The API routes get this right (`requireCapability(identity.CapDeadLetterRead, ...)`); the console routes do not, because `requirePrincipal` was used for the HTML surface. Invariant 5 is about tenant scoping, and this is the same family of defect: a surface reads data its caller is not authorised to read | Console routes use `requireCapability` like the API ones, or the data is omitted by capability. A test to match, in `internal/api/api_test.go` — and it is a §7-content-assertion gap as well (§2.7) |
| Q-6 | `internal/evidence`'s `TestSymlinkEscapeIsRefused` skips on Windows | `internal/evidence/evidence_test.go:74-77` | §6.2. A test that skips on the platform running half our target installs is not protecting half our customers | Grant `SeCreateSymbolicLinkPrivilege` on the Windows CI runner, or test the post-open `Lstat` check directly, or use a junction. At least one ships in M1 |
| Q-7 | `go.mod` declares `go 1.26.0`; `.github/workflows/ci.yml` pins `GO_VERSION: "1.24"` with `GOTOOLCHAIN: local`; `docs/status.md` says Go 1.27 | `go.mod:3`, `.github/workflows/ci.yml:11-12` | With `GOTOOLCHAIN=local` a Go 1.24 toolchain cannot build a module declaring 1.26. **CI cannot build this module as pinned.** A green local run and a red CI run is the most expensive way to discover a version mismatch, and it will be discovered by a release, not by a test | Pin one version in three places and add a CI step asserting `go.mod`'s version against the runner's. Until then, treat "CI is green" as unverified, and say so in the release notes rather than asserting it |
| Q-8 | The Windows Service and systemd units are absent; `deploy/` was created and removed | `docs/status.md` | A site on an isolated network has no supported way to start the product at boot. Every other deployment concern in `` is downstream of this | M1. Genuinely small; deferred rather than absent, and nothing else depends on it — but "nothing depends on it" stops being true the moment there is a customer |

### 11.5 The one summary line for a QA review

> For every row of `docs/status.md` marked green: a named test exists, that test
> has been **seen to fail** when its control was removed, and the row says which
> one. For every invariant in `AGENTS.md`: a named test exists, and changing an
> invariant requires an ADR before a test is written. Anything else is a
> preference.

If that sentence is true, the quality posture is defensible, and it is checkable
in one afternoon. If it is not true, no amount of coverage tooling makes it so.
