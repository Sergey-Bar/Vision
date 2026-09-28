# Product and Backlog

Owner: E1 (Product/Architecture). Status: living. Last revised 2026-09-28.

Read `00-strategy-and-north-star.md` and `01-master-roadmap.md` first. This
document is the mechanism by which a capability becomes a ticket, a release and
a validated outcome. It does not restate what is built; `docs/status.md` does
that.

The scope law is inherited: a new capability has to be justified as a platform
primitive, or it is not a capability. Section 1 states what that means in testable
terms, because a principle nobody can violate is a slogan.

---

## 1. Product principles

Seven. Each one is derived from a specific commitment in `00` or `01`, each one
has a named failure signal, and each one is enforced by something other than
good intentions.

### 1.1 Provability before usefulness

Every claim the product makes about a past decision must be reproducible from
stored facts, at the site, months later, with the model switched off. A decision
that can only be explained by re-running something is not provable, it is
reproducible-looking, and the difference is the whole product.

**How we know we're violating it.** A customer question of the form "why did it
do X?" cannot be answered by `securityctl incident explain <id>` and
`securityctl incident replay <id>` with the AI plane absent. Or: any
`replayedFromStoredFacts` path that opens a model client, reads a bundle path, or
reads a clock. Or: the roadmap adds a milestone whose gate cannot be stated as a
query against stored rows. The reference implementation is
`internal/incidents` — `TestReplayReproducesTheDecisionFromStoredFacts`,
`TestReplayReportsABundleMismatch`, `TestReplaySurfacesDegradation`.

### 1.2 No-replacement positioning

We sit above the stack the customer owns. We do not become a VMS, a camera
vendor, a generic SIEM, or a cloud-only product. The test is not "is this
feature nice" but "does this deepen the intelligence layer above the installed
security stack, or does it grow us into a different company" (`00` §7).

**How we know we're violating it.** A capability in §2 whose Segment column
reads "camera/OEM" or "generic SOC" is a red flag, not a proof. Concretely: we
ship a recording-management screen, a firmware-flash tool, a camera discovery
UI, or a per-user "create camera" form. Any of those means we own the thing the
customer replaced us for. `00` §3's anti-segments are permanent and a deal
requiring one is not a deal.

### 1.3 Site autonomy

A site must be operable when nothing else exists — no central plane, no
internet, no model runtime, no support engineer. This is a hard architectural
constraint, and from M5 it is a fault-injection test, not a design intention.

**How we know we're violating it.** A code path that is reachable only from the
central plane. A migration, upgrade, licence check, or feature flag that requires
outbound connectivity to evaluate. A customer who cannot describe, unaided, what
their site does when the WAN link is pulled. From M5 this is measured by the M5
exit gate: a three-site customer with the control plane pulled mid-incident and
no site degradation.

### 1.4 Evidence before assertion

Nothing is asserted that cannot be shown, and the showing is cheap. A claim
carries its citation; a citation resolves to stored bytes with a hash; the hash
is recomputable. A degraded, absent or insufficient-evidence signal is displayed
as an absence, never as a negative.

**How we know we're violating it.** Any string rendered to an operator that names
a conclusion with no adjacent reference. Concretely, in the console: an
explanation with no supporting event ids; a summary with no evidence link; a
`—` where an absence should be labelled; a "no" that was actually a timeout. The
engine-level form is `internal/evaluation.predictedRestricted` returning `nil`
for degraded, insufficient-evidence and missing-field cases, and
`internal/rules`' `DegradedObservationIsNotEvidence` /
`InsufficientEvidenceIsNotANegativeAnswer`.

### 1.5 Bounded autonomy

Anything with physical impact, legal consequence, or irreversible external
effect stays behind an explicit, audited, reversible policy gate. Autonomy is
granted per site, per class, at a stated level, and never by the AI itself.

**How we know we're violating it.** An action in `01`'s M3 action set, or any
later one, that can be reached without a policy decision recorded first. An
autonomy level that changes without a blast-radius preview, a named approver and
a rollback path. A rule the AI proposed that reached activation without a
simulated historical impact diff that a human read (`01` M6 item 6, which states
the AI never activates its own rule).

### 1.6 The operator-exception surface is the product

From M7 the operator's primary surface is an exception queue, not an alert feed.
The number of things a human is asked to look at is the primary design variable,
and reducing it without degrading a single companion metric is the primary
success condition. The four companions (`01` M7) are not a footnote; a
reduction in attention minutes reported without missed-incident rate, override
rate, false-escalation rate and evidence completeness is a false claim, and we
refuse to publish it.

**How we know we're violating it.** A release that lowers Human Attention Minutes
while missed-incident rate, operator override rate, false-escalation rate or
evidence completeness moves against it. An exception queue that an operator
routes around by going back to the VMS. A notification whose body is
"motion detected" instead of incident, severity, evidence, verification state
and deep link.

### 1.7 No silent activation

Nothing turns on by itself and nothing changes behaviour without a record. A rule
bundle is stored, simulated, diffed, reviewed and activated as separate acts. A
degraded device is excluded from corroboration visibly. A drift exclusion is
written on the verification result. A dead letter carries a reason. A restored
ledger is provably behind the seal and says so.

**How we know we're violating it.** Any behaviour change whose only record is in
operational logs rather than the audit ledger (`AGENTS.md`: the ledger is
separate from operational logs and is the authoritative one). Any UI that
applies a state and does not say it did. `internal/bundles`' `TestStoreDoesNotActivate`
and `TestActivationRequiresAReviewedDiffDigest` are the existing expression of
this; the UI equivalent does not exist yet and is in the backlog.

---

## 2. Capability backlog

**Legend.** *Effort* is engineer-weeks for one engineer including tests and
review: S = 0.5–1, M = 2–3, L = 4–6, XL = 7+. *Risk* is the risk of getting it
wrong, not the risk of it being hard: L means a defect here is a Sev-1 or
falsifies the product claim. *Status*: `green` = written, building, covered by a
test that fails if it breaks (the `docs/status.md` definition); `stub` =
registered and refuses with a named reason; `partial` = some of it is green;
`gate` = blocking a milestone exit gate; `blocked` = needs a capability that
does not exist yet; `external` = needs a customer installation, hardware or
third party.

M1 carries the most rows because it is the only milestone the team will actually
execute. The M5–M8 rows are a shape, not a plan, and are expected to change.

### 2.1 M1 — Trust Spine (now → 2027-03-31)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 1 | Canonical event pipeline (inbox → dedupe → normalizer → outbox) | 1 | Inv 1 | M | L | green |
| 2 | Scoped dedupe key off the partitioned tables | 1 | Inv 1 | S | L | green |
| 3 | Leases with owner, expiry, renewal, reclaim | 1 | Inv 1 | M | L | green |
| 4 | Dead-letter with a mandatory reason, operator-initiated replay | 1,4 | Inv 1 | S | M | green |
| 5 | Event id derived from the scoped key | 1 | Inv 1 | S | L | green |
| 6 | Clock drift measurement, normalisation, severe-band exclusion | 1,2 | Conv (`normalized_at` only) | M | M | green |
| 7 | Milestone/XProtect live adapter — event path | 1 | M1 gate 1 | L | L | stub (`Unverified`) |
| 8 | Milestone/XProtect live adapter — PTZ and bookmark actions | 1 | M1 gate 2 | M | M | blocked |
| 9 | Replay transport identical to the live parsing path | 1 | Inv 2 | M | M | green |
| 10 | Live-vs-fixture integration and replay parity proof | 1 | M1 gate 1 | M | L | external |
| 11 | Rule compiler: schema → semantic validator → AST | 1,2 | Inv 5 (no shell/SQL/net/regex) | L | L | green |
| 12 | Verification state machine and conflict precedence | 1,2 | Inv 3, M2 gate | M | L | green |
| 13 | `if_ai_unavailable` enforcement at compile time | 1,2 | Inv 3 | M | L | green |
| 14 | Provenance graph with revocation-by-hiding | 1,2 | Inv 2 | M | M | green |
| 15 | Evidence store: content addressing, path validation, retention sweep | 1,5 | Path-traversal rule | M | L | green |
| 16 | Incident lifecycle and unexplainable-decision refusal | 1 | Inv 2 | M | M | green |
| 17 | Disposition recording with step-up MFA on escalate/dismiss | 1,5 | Inv 4, AC | M | L | green |
| 18 | Replay path with no model client, bundle path or clock | 1 | Inv 2 | M | L | green |
| 19 | Hash-chained audit ledger, trigger-computed | 1,5 | Inv 4 | M | L | green |
| 20 | Signed offline checkpoint sink and offline seal verification | 1,5 | Inv 4 | M | L | green |
| 21 | Identity: Argon2id, mandatory MFA, rotating session tokens, exact capabilities, site scoping | 1,5 | Inv 5 | L | L | green |
| 22 | WebAuthn registration ceremony | 5 | Regulated parity | M | M | stub (`enroll-mfa --method webauthn` refuses) |
| 23 | JSON API, no content negotiation, body cap | 1,4 | AC | M | M | green |
| 24 | Server-rendered LTR console, strict CSP, no inline script | 1 | AC | M | M | green |
| 25 | Console disposition flow (the four-outcome form) | 1 | M1 gate 2 | M | M | planned |
| 26 | Console rule-bundle store → simulate → activate → rollback | 1,3 | Principle 1.7 | M | M | partial (CLI green, console planned) |
| 27 | Console dead-letter and clock-drift views | 1,4 | Inv 1 | S | M | green |
| 28 | AI observation persistence, attribution, supersession, shadow marking | 1,4 | Inv 2, Inv 3 | M | L | green |
| 29 | AI observation profile evaluation harness with named gates | 1,4 | Inv 3 | M | M | green |
| 30 | **Python VLM runtime** (inference service, schema-validated output) | 1 | M1 gate 4 | XL | L | not started — the one substantial outstanding piece of Track F |
| 31 | `securityctl install` — verified archive extraction | 1 | Principle 1.3 | L | L | stub |
| 32 | `securityctl upgrade` — transactional, schema-window checked | 1 | Principle 1.3 | L | L | stub |
| 33 | `securityctl rollback` | 1 | Principle 1.3 | M | M | stub |
| 34 | `securityctl backup` / `restore` | 1,5 | Principle 1.3 | M | L | stub |
| 35 | Release builder/verifier, SBOM, provenance, secret scanning | 1,3,4 | Inv 5 | M | L | green |
| 36 | Windows Service unit | 1 | Principle 1.3 | S | M | deferred (`deploy/` created and removed) |
| 37 | systemd unit | 1 | Principle 1.3 | S | M | deferred |
| 38 | SLO evaluation with remedy-per-objective | 1,4 | Principle 1.6 | S | M | green |
| 39 | SLO demonstration under recorded load | 1 | M1 gate 3 | M | M | external |
| 40 | Recovery drill producing the "restore is behind the seal" finding | 1,5 | Inv 4 | M | L | green |
| 41 | Threat model, 7 boundaries, 24 abuse cases, 35 control→test mappings | all | Inv 5 | L | L | green |
| 42 | Load generator for SLO demonstration | 1 | M1 gate 3 | M | M | external |
| 43 | Production VLM profile, 14-day shadow run at a design-partner site | 1 | M1 gate 4 | L | L | gate — blocked on 30 |

M1 totals ≈ 78 engineer-weeks of listed work at the top of the range. Against the
capacity law in `00` §6 — 2 engineers, 13 weeks/quarter, ~60% on new build, so
≈ 2 × 26 × 0.6 ≈ 31 engineer-weeks of new build in the window — the list as
written is roughly 2.5× the available capacity. This is the single most important
finding in this document and it is resolved by the M1 `Reduced scope` cut in
`01`, applied before the slip, not after. Items 31–34 and 36–37 are the named
deferrals; item 30 is not, because gate 4 cannot be met without it.

### 2.2 M2 — AI Investigator (2027-04-01 → 2027-09-30)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 44 | Evidence intelligence: camera relevance, time windows, snapshot/clip retrieval | 1,2 | Principle 1.4 | XL | M | planned |
| 45 | Cross-source correlation: neighbouring cameras, access events, sensors | 1,2 | M2 gate | L | M | blocked (44) |
| 46 | VLM profile library — ten profiles, each with schema, pinned prompt, pinned model, dataset, gate | 1,4 | Inv 3 | XL | L | planned |
| 47 | Local LLM investigation planner ("what happened around Door 18") | 1,2 | M2 gate | L | L | planned |
| 48 | Evidence-backed answer with resolvable references on every claim | 1,2 | Principle 1.4, M2 gate | M | L | blocked (47) |
| 49 | Plain-language search | 1,2 | M2 gate | L | M | planned |
| 50 | Semantic search via `pgvector`, derived data only | 2 | Postgres stays authoritative | M | M | planned |
| 51 | Incident summaries: what, when, where, supporting, contradicting, unresolved | 1,2 | M2 gate, Principle 1.4 | L | M | blocked (44,47) |
| 52 | Summary replay from stored facts with no model call, asserted by test | 1,2 | Inv 2, M2 gate | M | L | blocked (51) |
| 53 | MTTI measurement harness, per operator, against own VMS-only baseline | 1,2 | M2 gate | M | M | planned |
| 54 | Evidence export capability (`evidence.export`) surfaced to a supervisor | 2,5 | Regulated | M | M | planned |
| 55 | Legal hold, exposed as a first-class operator control | 5 | Regulated | S | M | partial (store-side only) |
| 56 | Retention policy configuration per site | 5 | Regulated | M | M | planned |
| 57 | Privacy zones and redaction | 5 | Regulated | L | M | planned |
| 58 | Notification integrations: Teams, Slack, email, webhook, SIEM | 1,4 | Principle 1.6 | M | M | planned |
| 59 | MTTV / MTTI / human attention minutes instrumentation | 1,2,4 | `08-metrics-and-analytics.md` | M | M | planned |

### 2.3 M3 — AI Operator Copilot (2027-10-01 → 2028-03-31) — first production GA

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 60 | Security playbooks: SOP → versioned, auditable, simulated before activation | 1,2 | Principle 1.7 | XL | L | planned |
| 61 | Recommendation engine: dismiss, monitor, escalate, request evidence, notify supervisor, PTZ preset, extend recording, open incident | 1,2 | M3 gate | L | L | planned |
| 62 | Policy engine as the gate; a rejected proposal never reaches an operator | 1,2,5 | Principle 1.5 | L | L | partial (policy table exists) |
| 63 | Safe action set: bookmark, extend recording, PTZ preset, snapshot, incident create/assign, notify | 1,2 | M3 gate | XL | L | planned |
| 64 | Action idempotency under replay, proven by fault injection at the action stage | 1,2 | Inv 1, M3 gate | M | L | planned |
| 65 | Action outcome verification: unverified action = failed action | 1,2 | Principle 1.5 | M | M | planned (M7 formalises) |
| 66 | Bounded auto-resolution on an enumerated class set | 1,2 | M3 gate, Principle 1.5 | L | L | planned — first thing cut if slipping |
| 67 | Operator Intervention Rate with the no-regression guardrails | 1,2,4 | M3 gate | M | M | blocked (66) |
| 68 | Console: exception surface before auto-resolution ships | 1,2 | Principle 1.6 | L | M | planned |
| 69 | Repeatable sales motion with three named production references | 1,2 | M3 commercial | M | M | external |

### 2.4 M4 — Universal Security Layer (2028-04-01 → 2028-12-22)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 70 | ONVIF Profile T: discovery, events | 3,5 | M4 moat | L | M | planned |
| 71 | ONVIF Profile M analytics metadata: people, vehicles, plates, object counters | 3,5 | M4 moat | XL | M | planned — the free signal density |
| 72 | ONVIF PTZ and streaming capability discovery | 3,5 | Principle 1.2 | M | M | planned |
| 73 | Second full VMS adapter, chosen by pipeline | 3 | M4 gate | XL | L | planned — not cuttable |
| 74 | Non-adapter diff of zero behavioural changes across two adapters | 3 | M4 gate | S | L | planned (a review artefact, cheap to state, expensive to lie about) |
| 75 | Adapter SDK v1: canonical event, device, evidence, action contracts | 3 | M4 gate | XL | L | planned |
| 76 | Adapter conformance suite, published | 3 | M4 gate | M | M | planned |
| 77 | Source simulator + fixture framework for third parties | 3 | M4 gate | L | M | planned |
| 78 | Certification suite run by someone outside the company | 3 | M4 gate | M | M | external |
| 79 | Access control: credentials, badges, identity refs, doors, granted/denied/forced-open/held-open | 2,3,5 | M4 moat | XL | M | planned |
| 80 | Sensors and IoT: intrusion, perimeter, environmental, intercom, barrier, LPR, BMS | 2,3,5 | M4 moat | XL | M | planned |
| 81 | Evidence media provenance: adopt ONVIF media-signing direction, do not build our own | 3,5 | Inv 4 | L | M | planned |

### 2.5 M5 — Enterprise Command Platform (2029-01-04 → 2029-09-28)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 82 | Optional central control plane, sites autonomous | 2,4,5 | Principle 1.3, M5 gate | XL | L | planned |
| 83 | Control-plane-loss fault injection, mid-incident | 2,5 | M5 gate | M | L | planned |
| 84 | Fleet management: site/adapter/rule/model versions, health, storage, licences, drift | 2,5 | M5 moat | XL | M | planned |
| 85 | OIDC, Microsoft Entra ID, AD, LDAP | 2,5 | M5 gate | L | M | planned |
| 86 | SCIM provisioning, tested against two real IdPs | 2,5 | M5 gate | M | M | planned |
| 87 | Local authentication retained permanently for isolated sites | 2,5 | Principle 1.3 | S | L | planned — a non-requirement, so it is a requirement |
| 88 | RBAC → RBAC + ABAC (view one site / export as supervisor / all regions as director / devices-not-evidence as integrator) | 2,4,5 | Inv 5 | L | L | planned |
| 89 | Core failover and database HA, measured RTO/RPO | 2,5 | M5 gate | XL | L | planned |
| 90 | Evidence-storage redundancy and site backup/DR | 2,5 | M5 moat | L | M | planned |
| 91 | Seal reconciliation procedure for a restored ledger behind its seal | 2,5 | Inv 4, `12-release-sre-and-support.md` §6 | M | L | planned |
| 92 | Organisation-wide Security Graph (same credential at Site A, B, C is one incident) | 2,4 | M5 moat | XL | M | planned — most differentiated, cut last |

### 2.6 M6 — Site Intelligence (2029-10-01 → 2030-06-28)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 93 | Site model: tenant → campus → building → floor → zone → door → camera → sensor, adjacency, FoV, allowed paths, restricted areas | 2,5 | M6 moat | XL | M | planned |
| 94 | Behavioural baselines by day/night, weekday/weekend, seasonal — "differs from this site's baseline", never "abnormal = dangerous" | 2,5 | M6 gate, Principle 1.4 | XL | L | planned |
| 95 | Site memory: repeated false alarms, common decisions, maintenance patterns, recurring incidents, site exceptions | 2,5 | M6 moat | L | M | planned |
| 96 | Operator feedback learning: AI said vs operator said vs final outcome, per class per site | 2,4 | M6 moat | M | M | planned |
| 97 | Drift detection: camera moved, activity changed, accuracy degraded, lighting changed, rule noisy, override rate up | 2,5 | M6 moat | L | M | planned |
| 98 | AI-proposed rules: proposal → simulation → historical replay → impact diff → human approval → activation | 2,5 | Principle 1.5 | L | L | planned — last in the milestone |
| 99 | Camera health intelligence: blur, obstruction, movement, low FPS, frozen image, exposure, night-mode failure — "operationally useful", not "online" | 2,5 | M6 moat | L | M | planned |
| 100 | Compounding-claim measurement: onboarding cost vs retention cost per site | 2 | `00` §5 falsifiable claim | M | M | planned — this is M6's real gate |

### 2.7 M7 — Autonomous Security Operations (2030-07-01 → 2031-06-30)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 101 | Autonomy levels A0–A4, per site, explicit audited reversible raise with blast-radius preview | 2,4,5 | Principle 1.5 | L | L | planned |
| 102 | Persistent incident agents, one per active incident, until close | 2,4 | M7 moat | XL | L | planned |
| 103 | Closed loop: detect → verify → act → observe outcome → verify success → audit | 2,4 | M7 gate | XL | L | planned |
| 104 | Autonomous routine operations: suppress validated nuisance alarms, collect evidence, start recording, track, notify, build package, close low-risk | 2,4 | M7 moat | XL | L | planned |
| 105 | Exception queue as the primary operator surface | 2,4 | Principle 1.6, M7 gate | XL | M | planned — before autonomy ships, not after |
| 106 | North Star metric live: Human Attention Minutes per 1,000 events with four companions | 2,4 | Principle 1.6 | M | M | blocked (59) |
| 107 | Failed-action rate as a first-class surface metric | 2,4 | Principle 1.5 | S | M | planned |

### 2.8 M8 — Physical Security OS (2031-07-01 → 2032-12-31)

| # | Capability | Segment | Protects | Effort | Risk | Status |
|---|---|---|---|---|---|---|
| 108 | Developer platform: Adapter, Event, Action, AI Observation and Rule SDKs; webhooks; graph APIs | 3,4 | M8 moat | XL | M | planned |
| 109 | Marketplace of signed, offline-installable adapters, rule packs, profiles, integration packs, compliance packs | 3,4 | M8 gate | XL | L | planned |
| 110 | Model marketplace: approved models as signed offline bundles with hardware profile, accuracy/speed trade-off, permitted use | 3,5 | Inv 3, M8 gate | L | L | planned |
| 111 | Vertical packs: logistics, retail, healthcare, manufacturing, campus — horizontal core, domain intelligence as rules/profiles/playbooks, never a fork | 2,5 | Principle 1.2 | L | M | planned |
| 112 | Certification suite public and run by a third party in production | 3 | M8 gate | M | M | external |

**Total: 112 rows, of which 27 are `green` today.** Everything from M2 onward is a
plan against a shape, and this table is expected to be wrong in detail.

---

## 3. Work-item anatomy

### 3.1 Fields every ticket carries

No ticket is started without all eleven. A ticket that cannot be filled in has
not been thought about, and the field that is usually missing — the invariant it
protects — is the one that would have caught the problem.

```
id:                M1-041                    # <milestone>-<seq>, never a tracker number
title:             one imperative sentence naming the outcome, not the mechanism
capability:        #NN from the §2 backlog, or "new" + a justification
milestone:         M1
segment:           1 | 2 | 3 | 4 | 5         # from 00 §3; a feature serving none is declined
owner:             E1 | E2
invariant:         Inv 1 | Inv 2 | Inv 3 | Inv 4 | Inv 5 | Conv | M<n> gate <n> | Principle 1.<n>
effort:            S | M | L | XL            # in engineer-weeks, not points
risk:              S | M | L
status:            proposed | specced | building | reviewing | green | stub | cut | dropped
depends_on:        ids, and whether they block start or block merge
test_that_fails:   the specific test function that does not exist yet
exit:              the observable state that makes this done, verifiable by someone else
reduced_scope:     what this becomes if the milestone slips — written now, not later
```

Two rules on the last two fields:

- `test_that_fails` is a **function name**, not a description. "Add tests for
  path traversal" is not an answer; `TestOpenRejectsTraversalAndReportsMissing` is.
  If the test already exists, the ticket names which one must be extended.
- `reduced_scope` is filled at creation. A scope reduction chosen in advance is a
  decision recorded in the roadmap; chosen during a slip it is a negotiation
  conducted under time pressure by the two people who also have to ship the
  result.

### 3.2 Template (a) — capability spec

Used for any row in §2 that is not `green`.

```
# Capability: <name>
Milestone: M<n>          Backlog row: #<n>
Segment: <n>              Why this segment, not another:

## What it is
<two sentences, present tense, no adjectives>

## What the operator does
<the observable action, step by step, in the third person>

## What it must never do
<the failure this design forecloses, named specifically>

## Invariant and gate
Protects: <Inv n / Conv / M<n> gate n>
The test that fails if the control is removed: <TestFunctionName>
The DoD item: <the fault-injection stage, if this touches durability>

## Contracts touched
contracts/protobuf/security/v1/<file>.proto: <added/changed/removed>
contracts/openapi/v1/<file>.yaml:            <...>
migrations/<NNNN>_<name>.<expand|contract>.sql: <...>
Generated code: regenerated by `make generate`, never hand-edited.

## Failure modes
- <mode> → <what the product does> → <what the operator sees>

## Rejected alternatives
- <alternative> — rejected because <reason>

## Evidence of completion
- [ ] `make ci` green with no Milestone server reachable
- [ ] <named test> fails when the control is removed (demonstrated, not asserted)
- [ ] console route renders with a real service behind it, not a stub
- [ ] <human acceptance step, with the name of the person who signs it>

Signed: E1 / E2        Date:
```

### 3.3 Template (b) — ADR

Required by `AGENTS.md` before any change that weakens an invariant, and by
`00` §6 before any new runtime, datastore or protocol family. Stored in
`docs/adr/NNNN-<slug>.md`, numbered, never edited after acceptance — superseded,
not amended.

```
# ADR-NNNN: <title>
Status: proposed | accepted | superseded by ADR-NNNN
Date: YYYY-MM-DD          Deciders: E1, E2
Invariants affected: <n, or "none">

## Context
<the forces. Two sentences of background are enough; a force nobody can state
in two sentences is not a force>

## Decision
<what we will do, in the active voice>

## Invariant analysis
Does this weaken one? If yes, stop: an ADR that weakens an invariant also
requires a test, in that order, and the test name goes here:
  - New test: <TestFunctionName>
  - Demonstrated: <how it was shown to fail when the control was removed>
If no, say why in one sentence. "It does not change how a decision is produced,
only how it is displayed" is an acceptable answer.

## Consequences
- Becomes easier:
- Becomes harder:
- New obligation: <what somebody must now do forever>
- Dependency added: <module, licence, version, offline-installability impact>

## Alternatives
| Option | Why not |
|---|---|
| <option> | <reason> |

## Reversal
<what it takes to undo this, and what data is unrecoverable if we do>
```

### 3.4 Template (c) — VLM profile spec

The gates are `internal/evaluation.DefaultGates()` and `ShadowGates()`. A profile
that has not passed its gate runs in shadow and cannot affect authoritative state
(`internal/observations`: `TestShadowProfilesCannotAffectAuthoritativeState`).

```
# Profile: <profile_id>
Schema version: <n>          Supersedes: <profile_id>@<n>, never edits>

## Purpose
One sentence. The question this profile answers and nothing else.

## Output schema
<the JSON Schema, committed under contracts/json-schema/ and embedded via the
same path contract `make contract-sync` enforces for the rule bundle>
Every field a rule may name appears here and only here
  (TestAIObservationFieldMustExistOnTheProfile)

## Pinning
Model:            <name and version — a moving target>
Model hash:       <sha256>
Prompt template:  <id>
Prompt version:   <n>            Input hash algorithm: <...>

## Degradation
if_ai_unavailable: <unverified | proceed_without>   (exactly one of two —
  TestAIDegradationMustBeOneOfTwoValues)
On unreachable:    degraded observation persisted, flagged, and counted as an
                   absence. Never a negative. (TestMissingAIObservationDegradesToUnverified)
On timeout:        <ms, and what the observation records>
On insufficient:   model's own statement recorded as insufficient, not as false
                   (TestInsufficientEvidenceIsNotANegativeAnswer)

## Evaluation
Dataset:                 <path, provenance, sanitiser version>
Labelled cases required: 30 for production, 10 for shadow
Gates (named individually, never one boolean — TestEveryFailingGateIsNamed):
  max false-positive rate    <x>
  min recall on true pos     <x>
  max median latency         <ms>
  max disagreement rate      <x>
  min labelled cases         <n>
Shadow duration: 14 consecutive days at a design-partner site
Report: <attached evaluation report, dated>

## Known limitations
<stated, in the profile, in the product, in the docs — the same three places>

Promotion decision: human, named, dated. `Report.Promotable` says who is
eligible; it never says who was promoted.
```

### 3.5 Template (d) — action spec

Every action is idempotent, authorised, and logged before and after. Anything
with physical impact on the world stays approval-gated (`01` M3 item 3).

```
# Action: <action_id>
Capability:            e.g. incident.disposition / ptz.preset / recording.extend
Version:               <n>              (an action's behaviour change is a new version)

## Effect
<what changes in the world, stated so a non-engineer can check it against reality>

## Preconditions
<deterministic, evaluated by the platform, not by a model>

## Idempotency key
<the exact expression. For an external side effect it must be derivable without
 reading the side effect's own result, or a crash between the call and the
 bookkeeping duplicates it.>

## Authorisation
Capability:            <identity.CapXxx>
Requires step-up MFA:  yes | no        (and why)
Site scope:            <required, denied by default>
Approval gate:         never | policy | named human (with role)

## Audit
Before: <what is written before the call>
After:  <what is written after>
Outcome verification: <how success is observed, or "not verifiable — treated as
                       failed" per the M7 closed loop>

## Failure and compensation
- Action fails:              <observable state>
- Action succeeds, write fails: <retry semantics; must be a retry, not a reconcile>
- Action succeeds, outcome unobservable: failed action, not completed
- Compensation: <the reversal, if any, and whether it is itself audited>

## Blast radius
<the worst credible outcome at one site, stated plainly. If this field is vague
 the action is not ready for A3 or above.>

## Fault injection
Stage: action execute.  Kill -9: <where, exactly>.  Assertion: <what must hold>.
This is a DoD item (AGENTS.md) and it is not optional.
```

### 3.6 Template (e) — rule-bundle change

A rule bundle is untrusted input (`AGENTS.md`, `00` §3). Storing one, simulating
it, diffing it and activating it are four separate acts.

```
# Bundle change: <version> (from <previous>)
File: rules/<builtin|customer>/bundle.yaml
Content hash: <sha256>        Schema: contracts/json-schema/rule-bundle.schema.json

## Diff summary
Rules added:     <n>   Rules removed: <n>   Rules changed: <n>
Event types added: <n>   Devices/zones introduced: <n>
Priorities changed: <n>   Clock bands changed: <n>

## Semantic validation
`securityctl rules lint` output:  <attached>
Language features used: none — no shell, SQL, network, dynamic code, regex.
  (TestNoUnboundedRegexOperatorExists, TestFieldValueListIsBounded)

## AI observation consumption
Rules consuming a Tier C signal: <n>
Each declares if_ai_unavailable: <list>   (TestConsumingAnAIObservationRequiresAnExplicitDegradation)
Each names a field that exists on the profile: <list>  (TestAIObservationFieldMustExistOnTheProfile)

## Simulate
`securityctl rules simulate` against <fixture set / replay window>:
  Incidents before: <n>      after: <n>
  Newly raised: <n>          newly suppressed: <n>
  State transitions: <n>
  False-positive delta: <n>  Missed-incident delta: <n>
Digest of the diff (computed over the diff, not the bundle): <sha256>

## Conflict check
Same-priority incompatible rules: <n>  (TestSamePriorityIncompatibleRulesAreAConflict)

## Review
Reviewed by: <human>        Date: <date>
Reviewed diff digest: <sha256>   (TestActivationRequiresAReviewedDiffDigest)

## Activation
Activated by: <human>       Date: <date>
Rollback target: <version>
Activation is audited (TestActivationIsAudited) and is a separate act from
storing the bundle (TestStoreDoesNotActivate).
```

### 3.7 Template (f) — customer commitment

Anything a customer relies on: a date, a number, an availability claim, a
retention promise, a regulatory statement. `00` §8 makes the evidence rule apply
to us as well as to our competitors.

```
# Commitment: <one sentence>
Customer: <name>            Site: <id>        Owner: E1 | E2
Given to: <person, role, date, channel>

## The commitment
<exact wording, as the customer received it — not the improved version>

## Basis
<what we measured or verified, with the artefact>
ESTIMATE: <if unmeasured, say so here and label it in the commitment itself>

## Qualifiers we said out loud
<what we told them that qualifies it — availability, exclusions, dependencies>

## Review date
<when we re-check it, and who>

## If we miss it
Notification: <within what time, to whom, by what channel>
Compensation: <what the contract says>
Internal action: <what changes in the product, and which milestone>
```

Two hard rules. A commitment containing an unmeasured number carries the word
ESTIMATE to the customer, not just to ourselves. And a commitment we cannot keep
is withdrawn in writing before the date, not explained after it.

---

## 4. Prioritisation

### 4.1 The model

Six axes, each 0–5, each anchored to something observable. The score is the sum
(0–30). It is a **ranking device, not a forecasting tool**: it orders the
backlog, and it never produces a date. Dates come from `01`'s windows and gates,
which are commitments, not estimates.

| Axis | 0 | 5 |
|---|---|---|
| **North Star impact** | No effect on Human Attention Minutes, missed-incident rate, override rate, false-escalation rate or evidence completeness | Directly moves one of them, and the move is measurable per `08-metrics-and-analytics.md` |
| **Invariant protection** | Touches nothing in `AGENTS.md` | Removing it would weaken an invariant, and the ADR + test order applies |
| **Segment pull** | No design partner asked | Three independent design partners asked, or the M<n> exit gate cannot be met without it |
| **Unblocking** | Unblocks nothing | Unblocks two or more `blocked` rows in §2 |
| **Cost** | XL (>7 ew) | S (≤1 ew) |
| **Reversibility** | Cannot be undone; data written under it cannot be re-read | Trivially reversible: flag, config, additive route behind a capability |

Cost and reversibility are additive, so a cheap irreversible thing is not
automatically good — a schema change with no reverse migration scores badly on
both, which is correct.

**Disqualifiers, applied before scoring.** A row is not scored at all if any of
these is true, and the disqualification is recorded in the ticket:

1. It fails `00` §7's feature test (deepens the intelligence layer vs grows us
   into a different company).
2. It serves an anti-segment in `00` §3.
3. It puts a new runtime, datastore or protocol family into the release without
   an ADR.
4. It makes the deterministic core depend on the AI plane. This is not a
   tradeoff; it is the law.
5. It is a `green` row being re-implemented because we did not read
   `docs/status.md`.

### 4.2 Worked example

Two candidate tickets at the M1 → M2 boundary.

**A — Console disposition form (backlog #25).**
North Star 4 (an operator's disposition is the ground truth every AI quality
number is computed from; without it `evaluation.Case.Outcome` has no source).
Invariant 3 (`escalated` and `dismissed` require step-up MFA, and the console is
where that prompt must appear or the operator routes around it). Segment pull 3
(three design partners dispositioned in `securityctl` during pilots and asked
for it in the console). Unblocking 3 (#59 metrics, #67 operator intervention
rate, and every M2 gate that measures MTTI). Cost 4 (M, ~2 ew). Reversibility 5
(additive route behind an existing capability). **Total 22.**

**B — Per-user camera management screen.**
North Star 0 (no effect on any of the five metrics). Invariant 0. Segment pull 1
(one integration partner asked; it is a VMS feature). Unblocking 0. Cost 2 (M,
but it will grow). Reversibility 3. **Total 6** — and before scoring, it is
disqualified by `00` §7 and `00` §3: it is a VMS feature. The score is recorded
anyway, because the number is what makes the conversation short the second time.

**A third case where the model is overridden deliberately.** `docs/status.md`
lists "the Milestone endpoint surface is still unverified" as the single most
important open item, and every M1 exit gate depends on it. The remaining work
scored 21 and cut. The override is recorded in the ticket with the reason — a
gate dependency is an invariant-protection argument in disguise, and the
disqualifier list does not contain "the exit gate needs it" only because the exit
gates in `01` are already written as constraints. If an override is used more
than twice in a milestone, the scoring model is wrong and gets fixed, not
worked around.

### 4.3 Story points

**We do not use story points, and we will not adopt them.** The reasons are
specific, not ideological:

1. The capacity law in `00` §6 is expressed in engineer-weeks against a
   percentage of a quarter, not in velocity. A forecast in points requires a
   historical velocity, and a two-person team that has shipped four milestones
   has four data points, none of them from a team that has since changed.
2. Points are a proxy for size that acquires a second meaning (difficulty,
   risk, uncertainty) the moment a stakeholder asks what they mean. The six
   axes above are explicit about which is which.
3. Our unit of risk is not size. A one-ew change to the rule compiler's
   degradation semantics is a larger risk than a six-ew change to the console,
   and points cannot express that. `effort` and `risk` are separate columns for
   exactly this reason.
4. The forecast that matters is "will this milestone's exit gate be met by
   2027-03-31", and the honest answer is never a sum. It is the gate list plus
   the named reduced-scope cut, and `01` already has both.

If a stakeholder asks for a number, the number given is the remaining backlog
count in engineer-weeks at the top of the range, the named cut that would be
applied, and the date at which the cut becomes necessary. That is the whole
forecast, and it is honest.

---

## 5. Discovery

The product's risk is building the wrong thing, not building it badly. Four
mechanisms, all of them cheap, none of them a research programme.

### 5.1 Design-partner interviews — 5 per month, from M1

Five structured interviews a month, each 45 minutes, each with a person who has
performed the job, not someone who manages people who perform it. From M2 onward
these are with paying or named-reference customers, and the interviewee is
always an operator, never only an IT or security manager.

The interview is recorded with consent and transcribed the same day. The
transcript is committed, anonymised, to `docs/research/YYYY-MM/<site-slug>.md`
with the sanitiser applied (§3.7 of `07-quality-engineering.md` §8 applies to
research artefacts too). A finding is only a finding if it appears in two
transcripts; a single mention is a lead, and leads are not written into the
backlog.

Each interview produces exactly one of three outputs, in writing: a problem
statement, a measurement (a number, or its absence), or a refusal. "We would use
this" is not one of them.

### 5.2 Shift shadowing — twice per quarter, from M2

A full shift, at the site, in person, twice a quarter per segment. Not a demo, not
a tour, not an hour. The observer takes notes on: what the operator looks at
first, what they stop looking at, what they do with both hands, what they do
when two incidents arrive at once, and every time they leave the console.

The deliverable is a timing: the median and the 90th percentile of
investigation time per incident class, measured on their console with their
mouse. That number is the M2 exit gate's baseline, and it is recorded in the
customer's own units before we ever propose a target. Shadowing also produces
the segment's vocabulary, which is what the natural-language search in M2 has to
parse; inventing our own words for their job is the most common way a search
product fails with competent engineers.

Shadowing requires a written consent that covers recording, transcription, and
retention, and the recording is deleted on a stated date.

### 5.3 Incident-review sampling

From M1, every disposition at a design-partner site is reviewed within five
working days by one engineer, against three questions:

1. Would this incident have been found by the customer without us? (If yes, the
   product is not earning its place and the rule set is wrong.)
2. Was the disposition the one a competent operator would reach from the
   evidence shown? (If no, the problem is the surface, not the operator.)
3. What did the operator have to look at that the product did not show? (Every
   answer is a candidate capability, and the answer is a screen, not a model.)

A sample of 10% of dispositions, minimum 20 per month, rotating across sites. The
output is a list, sorted by frequency, and the top item each month is either
built or explicitly declined with a reason. A review process that produces a
document nobody acts on is worse than none, because it consumes the time it
claims to save.

### 5.4 The "three similar requests" rule

A feature enters the backlog from a customer conversation only on the third
independent similar request, recorded with the date, the requester and the
segment, in `docs/requests/`. Exceptions, each of which must be written down as
an exception at the time:

- A request that maps to a safety or evidence gap in the current product.
  (One request is enough; safety is not a frequency problem.)
- A request from a segment we have not reached, that de-risks a milestone gate.
  (Adapter choice, IdP choice.)
- A request that would make us a different company. (Logged, declined, in
  writing, kept on file — because the third request for it will come.)

The rule exists because a single enthusiastic design partner is a sample size of
one with a budget, and because the alternative — building what the loudest person
asked for — is how a platform becomes a consultancy.

### 5.5 Interview guide — a security operator, 45 minutes

Eight questions. The first four are about their job and none of them mention us.
The last four are about the product and are asked last, after they have
described their shift in their own words.

**Warm-up (0–10 min), about the work, not the product**

1. *"Take me through last night. What came in, in what order, and what did you
   do with each one?"* — Listen for the order, not the story. The order is the
   real prioritisation rule and it is never the one in the SOP.
2. *"You got two of those at once. Which one do you deal with first, and what
   decides that?"* — Their answer is the exception queue's sort order.
3. *"What was the last one that turned out to be nothing, and how did you know
   it was nothing?"* — The word they use for "nothing" is our outcome vocabulary.
   Their answer is also the false-positive taxonomy.
4. *"What would have to be true for you to be sure you'd missed nothing last
   night?"* — This is the question the product is answering. If they have no
   answer, that is a finding, not a failure.

**About the system (10–40 min), once they have described the work unaided**

5. *"Right now, when you go to prove to somebody what happened at 02:14, what
   do you actually do? Walk me through it."* — Time it. This is the before/after
   number the M1 and M2 gates require, produced by them, not by us.
6. *"What would you refuse to let a system do on its own, and what would you let
   it do if it asked you first?"* — This is the A0–A4 boundary, in their words.
   Record the boundary they draw; it becomes the default policy.
7. *"Last time something went wrong with a system you rely on, what did you wish
   it had shown you?"* — The most useful question in the set. Almost every answer
   is a request for a provenance surface we have not built.
8. *"If this stopped working entirely, right now, what would you do in the first
   ten minutes?"* — This writes our operations playbook, from their perspective,
   and it is the §8 of `12-release-sre-and-support.md` before we have written it.

**Closing (40–45 min)**

*"Is there a question I should have asked you?"* — and then silence. The answers to
this are consistently better than the answers to the eight.

Ground rules: no demo before question 8; no product vocabulary in the questions;
record the session's date, the operator's role, and their years on the job, and
weight by it — an operator with two years and one with twenty are different
samples and we say which is which.

---

## 6. The product–engineering contract

### 6.1 What product promises

Product promises the operator an outcome, stated so it can be checked:

- The decision is reproducible from stored facts, offline, without the model.
- The system says when it does not know, and never turns not-knowing into a
  negative.
- The evidence behind any assertion is one click away and has a hash.
- Nothing changes behaviour without a record in the audit ledger.
- The site keeps working when everything else is gone.

Product does not promise a model output, a latency figure it has not measured, an
accuracy percentage it has not gated, or a date it has not put behind a gate.

### 6.2 What engineering commits

Engineering commits to:

- An exit gate is met, or the milestone has slipped and the named cut has been
  applied in writing. It is not met and quietly redefined.
- Every change that touches durability has a `kill -9` test at the stage it
  touches, before merge, not after (`AGENTS.md`).
- Every evidence path has the four negative tests on every change
  (`AGENTS.md`): `../`, absolute, NUL, symlink.
- Every invariant-weakening change has an ADR and a test, in that order.
- A capability declared `green` in `docs/status.md` has a test that would fail if
  it broke. That is the definition, and it is not negotiable per-item.
- CI passes with no Milestone server reachable (`AGENTS.md`).

### 6.3 What "committed" means

A work item is **committed** when, in the tracker, all of the following are true.
It is not committed when it is being built, however far along.

1. It has an id, an owner, a milestone, an invariant or gate, a `reduced_scope`,
   and a named `test_that_fails`.
2. Its exit condition is written as something a second person can check without
   asking the owner.
3. Any contract change is in `contracts/`, regenerated, and `make contract-check`
   is green. Nothing under `internal/contracts/` was hand-edited.
4. Any migration is a new forward-only file with an expand/contract phase, and
   a contract step is not bundled with the expand step that makes it safe.
5. The test exists, is in the suite, and has been **seen to fail** when the
   control is removed. Not "would fail" — seen.
6. `make ci` is green: `contract-check contract-sync vet lint test-race
   migration-phases rules-lint`.
7. If it is a console capability: it renders with a real service behind it, not a
   stub, and it has the five states from `06-ux-and-design-system.md` §3.
8. If it is a VLM profile: it has a spec, it has a dataset, and it has passed
   its named gates. A profile that has not passed runs in shadow and is marked
   as such wherever it appears.
9. E2 has reviewed the diff against the spec template, not against the intent in
   their head.
10. It is signed, dated, and `docs/status.md` is updated in the same change.

### 6.4 Scope reduction is chosen in advance

Each milestone in `01` carries a named `Reduced scope` cut. The rule, stated once
so it does not have to be re-argued at 2am in month five:

- The cut is decided when the milestone starts and written into `01`. Not
  decided in the week of the date.
- The trigger for applying the cut is written too: a named burndown checkpoint
  (M1: end of week 6, end of week 13, end of week 20) where remaining capacity is
  less than remaining work.
- Applying the cut is a same-week action that updates `01` with a date and a
  sentence, and tells any customer who has been given a date derived from the
  un-cut scope.
- What cannot be cut, ever, in any milestone: the audit ledger, the replay path,
  the `kill -9` fault-injection tests, the path-traversal negative tests, the
  `if_ai_unavailable` declarations, and the AI profile evaluation gates. These are
  in `01`'s M1 cut clause and they are restated here because the temptation
  appears under time pressure, which is exactly when a restatement helps.
- A cut that would weaken an invariant is not a cut; it is an ADR plus a test
  plus a different cut.

### 6.5 What happens when the two disagree

E2 holds the invariant. E1 holds the scope. Neither holds both, and that is the
point of a two-person company: the person who wants the feature cannot be the
person who decides the invariant is negotiable. A disagreement that survives one
weekend is written down in `docs/decisions/YYYY-MM-DD-<slug>.md` with both
positions stated in the writer's words, and it is escalated to a named advisor or
decided by whichever person the other has explicitly deferred to. It is never
resolved by whoever is angrier, and it is never resolved by shipping.

---

## 7. Roadmap governance

### 7.1 Who can move a milestone boundary

| Action | E1 | E2 | Both | Nobody |
|---|---|---|---|---|
| Move a date inside `01` | — | — | ✓ | |
| Apply a milestone's pre-declared `Reduced scope` cut | ✓ | ✓ | — | |
| Declare a new `Reduced scope` cut after the fact | — | — | — | permitted, but only with an ADR and a stated reason |
| Change a milestone's mission | — | — | ✓ (ADR) | |
| Change a milestone's exit gate | — | — | ✓ (ADR + test names named in the ADR) | |
| Insert a milestone | — | — | ✓ (ADR) | |
| Drop a milestone | — | — | ✓ (ADR, and `00` §5's compounding claim re-checked) | |
| Weaken an invariant | — | — | — | (ADR + test, in that order, and the test must fail when the control is removed) |
| Mark a capability `green` in `docs/status.md` | — | — | ✓ | |
| Mark an exit gate met | — | — | ✓, in writing, with the artefact | |
| Name a customer | ✓ | ✓ | — | |

One row is the important one: **no one can mark an exit gate met without the
artefact**, and the artefact is named in the gate itself. `01`'s M1 gate 3 says
"with the run and its numbers in the release notes" — that is the standard, and it
is why every gate in `01` is written as a measurable condition rather than an
outcome.

### 7.2 Evidence required to move a boundary

A boundary moves only with, attached and committed:

1. A written statement of which gate is not met, or which gate is now believed
   and not proven.
2. The artefact that would have proven it, and why it is absent or failing.
3. A revised effort estimate for the remainder, in engineer-weeks, with the
   burndown checkpoint that produces the number.
4. The reduced-scope cut that would be applied, and its consequences for any
   customer given a date.
5. A statement of which invariants are and are not affected.

Missing any of the five, the boundary does not move and the work continues at the
current scope. This is deliberately expensive: the cost of moving a boundary
should be higher than the cost of one uncomfortable week.

### 7.3 How decisions get logged

Four artefacts, each with a home and an owner:

| Artefact | Home | Owner | Retention |
|---|---|---|---|
| ADRs | `docs/adr/NNNN-<slug>.md` | whoever proposed it | permanent; superseded, never amended |
| Ticket history | the tracker | E1 | permanent |
| Roadmap changes | `docs/roadmap/01-master-roadmap.md`, dated edits with a one-line reason | E1 | permanent, versioned |
| Disagreements and deferrals | `docs/decisions/YYYY-MM-DD-<slug>.md` | whoever wrote it down | permanent |
| Research artefacts | `docs/research/YYYY-MM/<site-slug>.md`, sanitised | E1 | 24 months, then delete raw recordings |
| Customer requests | `docs/requests/` | E1 | permanent; a declined request stays on file |
| Design-partner commitments | §3.7 template, indexed per customer | E1 | contract life + 2 years |

The rule that makes this work: **a decision that exists only in a chat is not a
decision.** Anything that changes what we build gets written into one of the
above within 48 hours, by the person who made it, and links to the thing it
decides. At two people this is the only institutional memory that is guaranteed
to survive a holiday.

### 7.4 Review cadence

- **Weekly** (30 min, both): the burndown checkpoint. Remaining work in
  engineer-weeks against remaining capacity. One number, one risk, one decision.
  No status theatre; the tracker already has that.
- **Milestone boundary** (90 min, both, at each checkpoint): §7.2, in full,
  written up.
- **Quarterly**: the compounding claim in `00` §5 is re-examined honestly even
  before M6, because a claim checked for the first time at the moment it is a
  gate is a claim that will be found true.
- **Before every customer conversation**: E1 reads §3.7 for that customer, and the
  current `docs/status.md` "What is not built, and why" section aloud. A demo
  that implies the Milestone endpoint is verified, or that the VLM runtime exists,
  is a commitment made by accident and it is the fastest way to lose a reference.
