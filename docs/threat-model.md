# M1 threat model

Blocking for the pilot. Its findings drive implementation, so it is written
before the controls it names are built, and every control it lists has a test
that fails when the control is removed.

Scope: Milestone 1 of the Physical Security Intelligence Layer. One site, one
Postgres instance, one filesystem evidence directory, a Milestone VMS on the site
LAN, an operator console, an optional Python AI runtime, and an offline release
process. What follows is deliberately about this system and not about Milestone
itself: the VMS is a dependency, not a target.

## 1. Assets

Ranked by what the product exists to protect, not by what is easiest to lose.

| # | Asset | Why it matters | Loss is |
|---|---|---|---|
| A1 | Incident history | The record of what the system saw and what it decided | Permanent, and the product's entire justification |
| A2 | Audit ledger | The independent record of who did what | Permanent, and the only defence against A1 being rewritten |
| A3 | Checkpoint material | The signed seals that make A2 falsifiable | Permanent loss means tamper evidence ends |
| A4 | Evidence bytes | Snapshots and clips incidents cite | Permanent; an incident without them is not defensible |
| A5 | Canonical events | The facts decisions replay from | Permanent loss breaks replay, which is the DoD |
| A6 | Operator identity and sessions | Who may see and act | Account takeover |
| A7 | VMS and DB credentials | Reach to the source and the datastore | Lateral movement |
| A8 | Rule bundles | What the site treats as an incident | Silent policy change |
| A9 | Model bundle | What the AI plane asserts | Fabricated observations |
| A10 | Site topology and device registry | What the graph is made of | Corrupt correlation |
| A11 | The release bundle | The thing every site installs | Arbitrary code on every site |
| A12 | Evidence of degradation | How often the AI path was actually live | An AI availability claim nobody can check |

A12 is listed as an asset rather than a metric because a pilot that cannot
measure whether the AI plane was running is making a security claim it cannot
support.

## 2. Trust boundaries

Seven. Each is enumerated, and each has abuse cases below.

```
                    ┌───────────────────────────────────────────────┐
   B1               │  SITE LAN (untrusted)                          │
   site LAN   ──────┤   Milestone VMS, cameras, door controllers      │
   ↔ adapter        └───────────────────────────────────────────────┘
        │
   B2   │  adapter ↔ Postgres
        ▼
   ┌───────────────────────────────────────────────┐
   │  B3   core ↔ console browser  (TLS, cookies,    │
   │       CSRF, CSP)                               │
   └───────────────────────────────────────────────┘
   B4   core ↔ AI runtime
   B5   core ↔ evidence filesystem
   B6   installer ↔ release bundle  (offline media)
   B7   checkpoint sink  (must be outside DB's reach)
```

**B1 — site LAN to adapter.** The LAN is untrusted: cameras and door
controllers are often unpatched, the VMS is reachable by anything on the
segment, and a compromised camera can forge events. The adapter assumes every
envelope is hostile input and the sanitizer is the choke point.

**B2 — adapter to Postgres.** The adapter's database account can write to the
inbox and read device and integration state. Nothing else. It has no grant on
`audit_ledger`, `incidents`, `users` or `rule_bundles`, so a compromised adapter
cannot alter authoritative state or erase its own tracks.

**B3 — core to console browser.** TLS with no plaintext fallback, `HttpOnly`
+ `Secure` + `SameSite` cookies, a CSRF token on every state-changing method, a
strict CSP with no inline script. `tenant_id` is never read from a request body:
a `X-Tenant-Id` header is honoured only when it matches the session tenant, and a
mismatch is 403 and audited.

**B4 — core to AI runtime.** The AI plane is off the availability and boot
critical path. Its database account can insert `ai_observations` and read
`evidence`, and nothing else — it cannot write to incidents, dispositions, the
rule store, identity, or the ledger. A compromised model runtime therefore
cannot alter authoritative state, only lie about evidence it was shown, and a lie
is a Tier C observation an operator can see.

**B5 — core to evidence filesystem.** The evidence directory is not
world-readable and not writable by the service account beyond need. Every path
is validated: absolute paths, `..` segments, backslashes, drive letters and NUL
bytes are refused, and the resolved path must be under the root. The database
enforces half of this with a CHECK; the store enforces the other half on every
read, and both are tested.

**B6 — installer to release bundle.** The archive is signed. `verify-release`
refuses a tampered archive, an unsigned one, a manifest whose file hashes do not
match the contents, a path escaping the archive root, and any manifest that would
install a secret. This is the boundary where a supply-chain attacker gets code
execution on every site at once.

**B7 — checkpoint sink.** The seal directory must not be writable by anyone who
can reach the database. That is the whole reason the sink exists: a seal stored
where the database owner can rewrite it is a claim rather than evidence. The seal
is signed, and verification takes a trusted public key, so a substituted key
cannot launder a forged chain.

## 3. Attacker profiles

**P1 — credential thief.** Has a VMS or database password, from a backup, a
reused password, or a phishing mail to a contractor. Goal: read everything.
Bounded by least privilege and RLS. This is the most likely profile by a wide
margin.

**P2 — malicious insider with console access.** An operator or administrator
account. Goal: read one site beyond their assignment, or alter history after the
fact. Bounded by the audit ledger, hash chaining, RLS site scoping, and the fact
that a disposition never overwrites an observation.

**P3 — network attacker on the site LAN.** Can observe and inject. Goal: forge
events. Bounded by the scoped dedupe key, by the fact that an injected envelope
must pass schema validation, and by the fact that it needs corroboration from a
second independent signal before an incident is corroborated. A forged event can
raise an unverified incident; it cannot forge a corroborated one without also
compromising a second device.

**P4 — supply-chain attacker.** Reaches the build, the release archive, or the
model bundle. Goal: code on every site. Bounded by signature verification, the
SBOM, provenance, and the rule that a tampered archive fails `verify-release` and
a tampered model bundle is rejected.

**P5 — exfiltrating operator.** Legitimate access, exfiltrating evidence. This
profile is the reason evidence is hashed, retention is explicit, every access is
audited, and the AI service account cannot read the ledger.

**P6 — compromised camera or VMS endpoint.** Firmware-level, on the LAN. Same
reach as P3 but persistent. This is why an event's provenance records
`asserted_by` and why a single source cannot corroborate itself.

## 4. Abuse cases, per boundary

### B1
- **A1.1** Forged motion event raises a false critical incident to exhaust the
  operator's attention. *Mitigation*: corroboration requires independent signals;
  provenance names the asserting device; the incident is auditable back to it.
- **A1.2** Clock manipulation: a compromised device reports times 40 seconds
  fast, joining two unrelated events into one false corroboration. *Mitigation*:
  drift measurement, the severe band, exclusion from time-windowed
  corroboration, and the exclusion recorded on the verification result.
- **A1.3** A device is deleted and re-registered with a new id, splitting one
  camera's history. *Mitigation*: the graph keeps revoked edges visible through
  `edges_active`; history is not silently rewritten.

### B2
- **A2.1** The adapter writes directly to `canonical_events` to plant a
  corroborated incident. *Mitigation*: `security_adapter` has no grant on
  `canonical_events`; the adapter writes only to the inbox and the normalizer —
  which runs as `security_core` — is the only writer.
- **A2.2** The adapter floods the inbox past 50,000 rows to exhaust disk, hoping
  for a silent drop. *Mitigation*: backpressure is a metric and a refusal, never
  a drop; dead letters are surfaced within 60 seconds.

### B3
- **A3.1** A stolen session cookie is replayed. *Mitigation*: `HttpOnly` +
  `Secure` + `SameSite`, session rotation on authentication and privilege
  change, absolute expiry, and revocation.
- **A3.2** CSRF from a page an operator is visiting, disposing an incident they
  never saw. *Mitigation*: a CSRF token on every state-changing method; the
  cookie alone is not authorization.
- **A3.3** A crafted request sets `X-Tenant-Id` to another tenant. *Mitigation*:
  the header is honoured only when it matches the session tenant, a mismatch is
  403 and audited, and RLS independently refuses the row.
- **A3.4** XSS through an event payload rendered in the console. *Mitigation*:
  strict CSP with no inline script, and the server-rendered console exists partly
  to make that achievable.
- **A3.5** Credential stuffing against the console. *Mitigation*: rate limiting
  per source, exponential lockout, mandatory MFA, and a break-glass login that
  raises a high-priority audit event.

### B4
- **A4.1** The model runtime is compromised and writes a fabricated observation
  with `restrictedZoneBreach: true`. *Mitigation*: the service account can only
  insert; it cannot alter incidents, rules, identity or the ledger. The
  observation is attributable — model, model hash, prompt template and version,
  runtime version, evidence hashes — and the operator sees it as an AI
  observation rather than as a system fact.
- **A4.2** The model is simply down and the site treats the absence as "no
  person". *Mitigation*: the strongest and most-tested invariant in the product.
  A missing observation is never a negative observation; a rule consuming a Tier C
  signal must declare `ifAIAvailable`, and the outcome is `UNVERIFIED` with the
  degradation recorded and flagged.
- **A4.3** A model bundle is swapped on disk. *Mitigation*: the bundle is
  signed, and a tampered bundle is rejected before any inference.

### B5
- **A5.1** A crafted `storage_ref` of `../../etc/passwd` reads outside the
  evidence root. *Mitigation*: the database CHECK refuses it, the store refuses
  it on read, and both are tested with absolute paths, `..` segments,
  backslashes, drive letters and NUL bytes.
- **A5.2** A symlink planted inside the evidence root points outside it.
  *Mitigation*: the resolved path must remain under the root after symlink
  resolution.
- **A5.3** Evidence is overwritten in place after being cited. *Mitigation*:
  content-addressed storage, a SHA-256 on every row, and legal hold that the
  retention sweeper respects.

### B6
- **A6.1** A tampered release archive is installed. *Mitigation*:
  `verify-release` fails on any hash, signature or coverage mismatch.
- **A6.2** A manifest smuggles a secret into the archive. *Mitigation*: the
  manifest schema has no field that can carry one — enforced by a test that
  fails if a secret-shaped property is ever added to the site config schema, and
  by the builder refusing to package credential-shaped material.
- **A6.3** A migration in the archive is a contract step bundled with its
  expand. *Mitigation*: filenames carry a phase, the runner refuses an unknown
  one, and a test asserts M1 contains no contract step at all.

### B7
- **A7.1** A ledger row is edited by an operator with database write access.
  *Mitigation*: the ledger is append-only at the storage layer, and the hash
  chain catches the edit even when the append-only trigger is dropped by a
  superuser. The test does exactly that, because that is the realistic attacker.
- **A7.2** Seals are deleted to hide a gap. *Mitigation*: seals are signed, and
  the gap detector reports an unsealed range to anyone holding the remaining
  seals — which is why they are kept off the database host.
- **A7.3** The whole ledger is deleted. *Mitigation*: the last seal names a
  sequence the database can no longer produce, and the chain resumes from the
  seal rather than from nothing.

## 5. Controls, and the test that proves each

The third column is the point. A control with no failing test is a claim.

| # | Control | Test |
|---|---|---|
| C1 | Tenant isolation at the service layer and at RLS | `schemalaws.TestRLSIsoTenants`, `TestRLSIsoTenantsForLargeTables` |
| C2 | Cross-tenant writes refused, including a missing context | `schemalaws.TestRLSBlocksCrossTenantWrite`, `TestTenantContextIsRequiredForWrites` |
| C3 | `security_core` is subject to RLS; only the migrator bypasses it | `schemalaws.TestMigratorIsTheOnlyRoleThatBypassesRLS` |
| C4 | Adapters cannot read the ledger, incidents or identity | `schemalaws.TestAdapterCannotReadAuditLedger` |
| C5 | The AI runtime cannot alter authoritative state | `schemalaws.TestAIRuntimeCannotAlterAuthoritativeState` |
| C6 | The audit ledger is append-only at the storage layer | `schemalaws.TestAuditLedgerIsAppendOnly` |
| C7 | Tamper is caught by the chain even with the guard defeated | `schemalaws.TestTamperedRowBreaksChainVerification` |
| C8 | Seals verify against a trusted key, and a foreign key is rejected | `checkpoint.TestVerifyRejectsForeignKey` |
| C9 | A removed seal is detectable as a gap | `checkpoint.TestGapDetectsRemovedSeal` |
| C10 | A seal over a chain that does not verify is refused | `audit.TestSealerRefusesToSealABrokenChain` |
| C11 | Evidence paths cannot escape the root | `schemalaws.TestEvidenceStorageRefIsAlwaysRelative` |
| C12 | Stored hashes are validated by the schema | `schemalaws.TestEvidenceHashIsValidatedByTheSchema` |
| C13 | A bundle is not loadable without its schema | `rules` — schema missing yields `schema_unavailable` and refuses |
| C14 | No unbounded pattern in a rule bundle | `rules.TestNoUnboundedRegexOperatorExists`, `TestFieldValueListIsBounded` |
| C15 | A rule consuming a Tier C signal must declare its degradation | `rules.TestConsumingAnAIObservationRequiresAnExplicitDegradation` |
| C16 | A missing AI observation is never a negative observation | `rules.TestMissingAIObservationDegradesToUnverified`, `TestInsufficientEvidenceIsNotANegativeAnswer` |
| C17 | A degraded observation is not evidence | `rules.TestDegradedObservationIsNotEvidence` |
| C18 | An AI observation is append-only | `schemalaws.TestAIObservationIsAppendOnly` |
| C19 | Superseding an observation is a new row, never an edit | `schemalaws.TestAIObservationSupersession` |
| C20 | Fixtures are sanitized: secrets, identifiers, hosts and opaque strings | `fixture.TestRedactsSecretsByKeyName`, `TestRedactsIdentifiers`, `TestRedactsURLs`, `TestSanitizeResponse` |
| C21 | The embedded bundle schema matches the published contract | `rules.TestEmbeddedSchemaMatchesThePublishedContract`, `make contract-sync` |
| C22 | The site config schema has no field that can hold a secret | `contracts.TestSiteConfigSchemaCannotCarryASecret` |
| C23 | Exactly-once effect at intake, across partitions | `durability.TestDedupeCrossesPartitions`, `TestConcurrentWritesProduceOneRow` |
| C24 | A killed worker's work is recovered, not lost and not duplicated | `durability.TestExpiredLeaseIsReclaimed` |
| C25 | A worker whose lease was stolen cannot complete the event | `durability.TestNormalizeIsAtomicWithTheInboxTransition` |
| C26 | Dead letters are visible with a reason, and replay is operator-driven | `durability.TestFailReachesDeadLetterWithAReason` |
| C27 | A poison outbox message cannot block the good ones | `durability.TestOutboxIsolatesAPoisonMessage` |
| C28 | Outbox side effects commit with the state change or not at all | `durability.TestOutboxEnqueueIsTransactional` |
| C29 | Contract compatibility is a build failure | `make contract-check`, `contractcheck` |
| C30 | An applied migration is never edited | `migrate.TestVerifyChecksumsCatchesDrift` |
| C31 | M1 ships expand steps only | `migrations.TestM1ContainsNoContractStep` |
| C32 | Graph edges require provenance, and revocation does not delete | `schemalaws.TestEdgesRequireProvenance`, `TestRevokedEdgesAreHiddenFromTheActiveView` |
| C33 | A break-glass account cannot exist without a password | `schemalaws.TestBreakGlassRequiresAPassword` |
| C34 | The AI plane's absence is measurable, not assumed | `rules` — every degraded decision carries a reason and a flag |
| C35 | M1 has no person model | `schemalaws.TestNoEntityAliasTableInM1` |

## 6. Accepted risks

Stated plainly, because a threat model that reports no accepted risk is not
being honest about anything.

**R1 — a single Postgres instance is a single point of compromise.** RLS,
grants and the hash chain limit what an attacker with database write access can
do, and tamper is detectable. But an attacker with the ability to truncate the
ledger *and* the checkpoint material can destroy history without leaving evidence
that survives elsewhere. Mitigated by B7 requiring the sink to be outside the
database's reach. Not eliminated: a control-plane host that can reach both is a
host that can lose both.

**R2 — a site with a single camera type cannot corroborate itself.** The built-in
bundle's production rule requires two independent signals, so a site with one
camera and no door controller cannot produce a corroborated incident. This is
correct: a rule that corroborates a camera with itself is theatre. Such a site
gets `UNVERIFIED` incidents and should be told so.

**R3 — the incident feed is eventually consistent.** An incident appears after
its events, the VLM observation, and the rule evaluation. The SLO bounds this at
p95 15 seconds on the AI path. An operator acting on the last 15 seconds is acting
on an incomplete picture, and the console states the incident's state rather than
implying completeness.

**R4 — synthetic fixtures are not captures.** The fixture set proves the pipeline
is deterministic; it does not prove the Milestone endpoint mapping is correct,
because that mapping is `Unverified` in `internal/milestone/routes.go` until a
real server confirms it. DoD item 1 stays open until it is.

## 7. What is not yet modelled

Named so the next person does not mistake silence for a decision.

- Physical access to the evidence host. Out of scope: the product assumes the
  host is administered by the customer.
- The Milestone VMS itself. A dependency, not a target. Its own security posture
  is the customer's procurement problem.
- Denial of service against the site by a party who can saturate the LAN. The
  backpressure metrics will show it; no mitigation is claimed here.
- Insider collusion across two accounts. The ledger records both, and the chain
  holds, but collusion is only detectable by a human reading it.
