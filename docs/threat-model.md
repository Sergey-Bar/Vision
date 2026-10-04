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

**R5 — agent skills are untrusted input, and the corpus is prose.** Sixty-seven
skills from outside this repository were installed on the machine this product is
developed on, and a skill is read as instructions by the thing making the
decisions. That is the same category as a rule bundle, with a wider blast radius:
a rule bundle is data this product validates, and a skill is text an agent may act
on. The published research on skill supply chains puts vulnerability rates at
26.1% and likely-malicious at 5.2%, with executable scripts 2.12x more likely to
be affected, so "they are just markdown" is not a defence.

The scan was run and the adjudication is recorded in §6b. What survives it as an
accepted risk: the corpus is overwhelmingly documentation, the findings are
overwhelmingly false positives, and a false-positive-heavy scanner is a scanner
whose *next* report gets skimmed. The control that matters is that the gate runs
on every verification and writes its findings to disk, not that any one run came
back clean.

## 6b. The skill supply chain, and the adjudication of its scan

**The boundary.** Agent skills installed on the development machine are untrusted
third-party input. They are not part of this product, they are not version-
controlled here, and nothing in this repository can revoke one — only the person
who installed it can.

**The control, and the test that proves it.**

| | |
|---|---|
| `skills-audit` in `scripts/verify.ps1` asserts that a scan record exists for each skill directory **and that it postdates the newest skill in that directory** | the gate runs every verification; add a skill and it fails until `scripts\scan-skills.ps1` is re-run |
| The scan itself is `scripts\scan-skills.ps1` (or `.\scripts\verify.ps1 -ScanSkills`), not every run | measured: 6.3 min cold and 10.3 min warm for 25 skills, because the scanner caches nothing. A gate that added forty minutes per run is a gate that gets stopped being run — the same defect that made `lint` and `-race` decorative |
| `--no-llm` keeps file contents local | SC4 still queries `api.osv.dev` with dependency coordinates, which is the only egress and is documented in the script comment |
| The verdict is **advisory** and never sets the gate's exit code | a legitimate skill scoring 100 CRITICAL on a filtered `os.environ` passthrough would otherwise block the build, which trains its readers to pass `--fail-on-findings` nothing and then stop reading the scanner |
| Where the tool is absent, the gate prints `SKIPPED` and sets `verified: false` in the record | the same rule as every other gate: a check that turns itself off is not a check |

**Why freshness rather than a scan on every run.** A scan-on-every-run gate was
built first and measured, and it was wrong: 42 skills in one directory did not
finish inside ten minutes. Replacing it with a freshness check keeps the property
that actually matters — the record cannot go stale while reading as current — and
keeps the gate's cost at milliseconds. The failure mode this trades away is a
change *inside* an already-scanned skill, which the mtime comparison does catch,
against the failure mode it buys back: a gate nobody runs.

**The scan, 2026-10-02.** 67 skills, 375 findings, 17.9 minutes for both
directories.

| Directory | Skills | Max risk | Findings |
|---|---|---|---|
| `~/.agents/skills` | 42 | 100 CRITICAL | 57 |
| `~/.config/kilo/skills` | 25 | 100 CRITICAL | 317 |

**Every serious category is a false positive on a documentation corpus**, and the
reason is the same in every case: the finding is a pattern match on the *shape* of
text whose subject is that pattern.

| Rule | Count | Where | Adjudication |
|---|---|---|---|
| `RP1` artifact not fully inspected | 142 | `sentry-*` SDK reference docs | **Coverage limit, not a vulnerability.** These skills ship 40+ SDK documentation files that exceed the per-file analysis cap. The scanner is saying it read less than it wanted, which is worth knowing and is not an attack. |
| `E1`, `AE1`, `PE3` data-exfiltration shapes | 110 | `sentry-*`, `graphify`, `archify` | Documentation about shipping telemetry, plus a code-graph tool that necessarily reads the repository. No destination, no encoding, no beacon. |
| `P2` hidden instructions | 30 | `sentry-*/references/sdks/**`, `archify/assets/template.html` | Comments and `<script>` blocks inside documentation *about* instrumentation and session replay. |
| `YR2` webshell match | 3 | `security-review/references/{language-patterns,vuln-categories}.md` | **Exactly right, and exactly backwards.** That file is a catalogue of malicious-code patterns, supplied to teach an agent to recognise them. A webshell signature firing on a webshell example is a true negative. |
| `SSRF1` | 1 | same file | Same reasoning. |
| `YR4` hack tool | 6 | `archify/examples/*.html` | Inline JavaScript in HTML diagrams, matched by hack-tool heuristics. |
| `SC2` remote script fetch | 6 | `sentry-create-alert`, `sentry-snapshots-cocoa` | Install snippets in prose, not lines this repository runs. |
| `TM1` shell invocation, `AST4` subprocess | 9 | `graphify`, `security-review`, `acquire-codebase-knowledge`, `test-gap-audit` | Shell and subprocess calls are what a code-navigation tool *is*. |
| `E2` env harvesting | 5 | `skill-creator/scripts/{run_eval,improve_description}.py` | `{k: v for k, v in os.environ.items() if k != "CLAUDECODE"}` — a filter that *removes* one variable and passes the child environment through. It does not read the values. |

**Two mitigations that are checkable rather than a judgement call.** The one
finding that needed more than reading was `skill-creator`'s, and it is closed by
absence rather than by argument: every script in that skill shells out to
`claude -p`, and `claude` is not on this machine, so the code cannot run here at
all. `Get-Command claude` returning nothing is the proof. The same applies to
`webapp-testing`, whose one genuine capability is `subprocess.Popen(...,
shell=True)` on a `--server` string — real, and constrained by the same rule that
bounds every other untrusted-input path in this repository: the threat model owns
the answer, not the convenience of the call site. Never interpolate repository or
web content into that flag.

**What is not modelled.** SkillSpector is defense-in-depth, not a sandbox: it
never executes the skill, does not isolate the host, reports that it may miss
non-English content and image-borne payloads, and its `--no-llm` mode is static
plus regex plus YARA with no semantic analysis. A skill that achieves its effect by
being *persuasive prose* — the one category this corpus is made of — is the worst
case for it and the best case for it at the same time, because the prose is right
there in `SKILL.md` for a human to read. **The control that actually holds is that
a human reads `AGENTS.md` before the agent acts, and `AGENTS.md` reserves changing
an invariant, a published contract, a capability or a date to that human.** No
scanner substitutes for it.

## 6a. Video frames are untrusted input

Added when the capture path went from "no video" to "video from a camera", which
is the change that made this boundary exist. Everything above it treats the
platform as a system of record; this section is about the one input that is
attacker-influenced by design.

### The boundary

```
camera ─RTSP─▶ mux ─▶ (transmit) ─▶ VLM ─▶ schema-validated output ─▶ rule ─▶ incident
   untrusted        untrusted        UNTRUSTED, third-party      trusted        trusted
```

A frame is untrusted input for the same reason a rule bundle is: **somebody
outside the system can write words into it.** A sign by a loading bay, a licence
plate, graffiti, a banner on a fence, a caption burned into a stream, a person
holding a printed page. The model reads pixels and turns them into text, and
there is no seam between the two.

The provider states it has no general guardrails by design. That is a reasonable
provider position and it is exactly why the control cannot be delegated to it.

### What an injected frame can and cannot do

**It can** make the model say something. It cannot do anything else, and the
reason is structural rather than a filter:

| | |
|---|---|
| Tools the model holds | **none.** It is given a prompt and an asset and returns text. There is no function to call, no URL to fetch, no credential to read. |
| What reaches the spine | schema-validated fields only. `additionalProperties: false` on every profile, and a field a rule may reference is a field the registry lists — a test asserts the two agree. |
| What is ever matched on | **structure, never prose.** `summary`, `sceneDescription` and `recommendedAction` are stored and displayed. No rule compares a string. |
| What the model can influence | whether an incident is raised, and its severity — i.e. whether a human is interrupted. |

So the blast radius of a successful injection is: **a human gets interrupted.**
That is a real cost and the plan's answer is the gate board — a measured
false-positive rate and a case count, before the profile is allowed to decide
anything. `general-monitoring` is shadow for that reason and for no other.

### The controls, and the test that proves each

| Control | Test |
|---|---|
| The model holds no tools | Structural: there is no tool interface in `internal/hafnia` or `internal/airuntime` to call. A test asserts the client exposes exactly the four documented endpoints. |
| Output is schema-validated before it is stored | Every profile's `additionalProperties` is `false`; the validator refuses an undeclared field rather than dropping it. |
| A rule may only name a field the registry lists | `TestTheGeneralProfileFieldSetMatchesItsPublishedSchema`, and the bundle compiler's own check at compile time. |
| Prose is never parsed | `TestReasonsNameAMeasurementAndNotAConclusion` and the absence of any rule comparing a string. The `general-monitoring` schema documents this on `summary` itself. |
| An in-frame instruction produces an observation and no action | `TestAnInjectedFrameProducesAnObservationAndNoAction` — the plan's red-check. It asserts the injected text is *stored* (it is data) and that nothing was dispatched, and it is red-checked by removing the "no tools" property. |
| The profile is not allowed to decide | `TestTheGeneralProfileCarriesNoPerConceptFields` and the shadow flag. |

### The residual, stated plainly

The control above bounds the blast radius; it does not remove the attack. A
frame can still cause a false positive, and a determined attacker can cause them
cheaply by holding a printed page in frame. Two things limit that, and neither is
a technical control:

- **The trigger is local and the model is only asked on novelty.** An attacker
  who can hold a page in frame can also walk in frame, so they have already spent
  the effort the attack is trying to avoid.
- **The gate board counts cases before promotion.** Until the false-positive rate
  is measured over the required case floor, the profile creates no incident and
  an injected frame costs one observation row.

That is a weaker position than "prompt injection is solved" and a stronger one
than silence. It is recorded here rather than left in the gap list because it is
a decision, and an unrecorded decision gets re-litigated by whoever reads this
next.

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

### 7a. A camera substitute, and why it is a threat-model entry at all

`securityctl serve-camera` streams a committed clip over RTSP and
`securityctl serve-vms` serves the vendor route table. Both exist so the acceptance
report can measure two criteria without hardware, and both print on startup that
they are substitutes.

The reason they belong in a threat model rather than in a README is the opposite of
the reason one would expect. **They are not attack surface — they are a claim about
what has been tested, and a substitute that reads like a device is a way to
overstate that.** Three specific risks, and what is done about each:

| Risk | What is done |
|---|---|
| A reader believes criterion 02 involved a camera | The acceptance evidence names a CAMERA SUBSTITUTE in every direction the word camera could have meant, and the demo transcript repeats it in its own footer |
| A reader believes the fixture is a stored recording of a real place | The evidence names the committed fixture by path and byte count, and says what a camera decides that this server does not |
| The substitute is mistaken for the real thing at runtime | Neither is reachable by default. Both bind `127.0.0.1`, neither is in the product image, and `serve-vms` prints that all nine routes remain `Unverified` and that nothing it does can promote one |

What is **not** claimed: that the RTSP client behaves against a camera's own
misbehaviour. It is tested against four ways a device lies — a port that is open
and is not RTSP, headers that never end, a raw non-RTSP response, and a server that
answers the handshake correctly and then sends something that is not video — because
those four are what `ServerFaults` injects. It is not tested against a device that
transcodes on the fly, renegotiates mid-session, or has a firmware bug nobody has
reproduced yet.

### 7b. The demo, and what running it costs

`make demo` writes `dist/demo/transcript.md` into the working tree and binds the
repository into a container. Three things a reader should know before running it in
a place that matters:

- **It publishes nothing.** Every port is loopback. The console is bound to
  `127.0.0.1:8443` and the database is not published at all.
- **It writes real material.** A generated MFA wrapping key and a self-signed
  console certificate, both on a named volume that `docker compose down -v` throws
  away. Neither is a credential for anything that existed before this run.
- **It is not the gate.** Five of twenty-five checks, and the transcript says so in
  its own footer. `make verify-container` is the full run.

And the one that is easy to get wrong: **a transcript is evidence only if it was not
written by hand.** `scripts/demo.sh` runs each command and captures its output and
its exit code. It has no facility for adding a number, which is the point — the four
bugs it shipped in its first run were all ways of producing a document that read
well and was quietly false.
