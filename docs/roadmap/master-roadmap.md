# Master Product Roadmap — Today to Product

Owner: E1. Status: living. Last revised 2026-09-28.

Read `` first. It defines who we sell to, what the
moat is, and the capacity law that constrains every date below.

This document is the calendar: what ships, when, what gates it, and what is cut
if it slips. The per-milestone engineering detail lives in the documents listed
in `README.md`.

## Shape of the plan

Eight milestones, roughly two to seven quarters each, running from today
(2026-09-28) to the point where the product is a platform other companies build
on. Each milestone has:

- a **mission** in one sentence,
- an **exit gate** that is a measurable, signed-off condition,
- a **commercial state**, because the product and the sales motion change together,
- a **reduced scope** cut, decided in advance, because the only legal response to
  a slip is less product,
- and a **moat**, because a milestone that adds no defensibility is a feature.

| # | Milestone | Window | Exit gate summary | Commercial state | Moat |
|---|---|---|---|---|---|
| M1 | Trust Spine | 2026-09-28 → 2027-03-31 | One real XProtect site, one real AI-assisted incident class, end to end, replayable, audited | Design partner, technical pilot | Trust |
| M2 | AI Investigator | 2027-04-01 → 2027-09-30 | Operators investigate supported incident classes measurably faster than with the VMS alone | Paid pilot | Investigation |
| M3 | AI Operator Copilot | 2027-10-01 → 2028-03-31 | Incident classes resolved under playbook without operator action, at a quality that does not regress | **First production GA on 2028-03-31** | Workflow |
| M4 | Universal Security Layer | 2028-04-01 → 2028-12-22 | A second full VMS adapter works with zero changes outside the adapter; Adapter SDK v1 is public | Platform expansion; first OEM talks | Integration |
| M5 | Enterprise Command Platform | 2029-01-04 → 2029-09-28 | Multi-site, HA, OIDC/SCIM, and a site that keeps operating with the control plane disconnected | **Enterprise GA** | Enterprise |
| M6 | Site Intelligence | 2029-10-01 → 2030-06-28 | Baselines, site memory and drift detection live; a proposed rule shows a historical impact diff before approval | Intelligence premium tier | Context |
| M7 | Autonomous Security Operations | 2030-07-01 → 2031-06-30 | A4 sites run routine operations on policy; the exception queue is the operator's primary surface | Autonomous operations | Autonomy |
| M8 | Physical Security OS | 2031-07-01 → 2032-12-31 | Third-party adapters, rule packs and model bundles install from a signed marketplace | Platform / marketplace | Ecosystem |

Windows are given in full dates because the capacity law in
[the strategy document](strategy-and-north-star.md) §6 makes a quarter a real
unit of capacity, and a milestone boundary that ignores a quarter is a date
nobody defends. The **M4 → M5 gap of 13 days** (2028-12-22 → 2029-01-04) is a
deliberate holiday buffer and carries no work.

## The calendar in detail

### M1 — Trust Spine · now → 2027-03-31

**Mission.** Prove the fundamental architecture works on a real customer
installation, with one AI-assisted operator workflow that a real operator
actually prefers to the VMS workflow.

State today: the deterministic spine is built and green — 24 Go packages,
twelve forward-only migrations, thirteen contracts, a hash-chained audit
ledger, a fixture-driven replay path, a content-addressed evidence store, a
rule engine with no shell, SQL, network, dynamic code or regex in the language,
an evaluation harness for AI observation profiles, a working
install/upgrade/rollback/backup lifecycle, Windows Service and systemd units,
and a dependency-free Python AI runtime that validates and attributes
observations but holds no model. `docs/status.md` is the authoritative build
state and this roadmap does not restate it.

What that leaves for M1 is narrow and hard, which is the right shape for a
milestone whose purpose is proof rather than surface area:

1. **The Milestone endpoint surface has never touched a real server.** Every
   route is still `Unverified` and the fixtures are synthetic.
2. **There is no model, no GPU, no signed model bundle, and no Go client for
   the runtime.** Nothing in the Go tree calls the inference endpoint, so gate 4
   is blocked by a missing *connection* as much as by a missing model.
3. **No SLO has been demonstrated under load**, because the observability
   package has no production caller yet.
4. **No design partner has run this for 14 consecutive days.**

Note for a future reader: M1's first cut assumed the installer, the service
units and the Python runtime were outstanding. They were built, which is why
the remaining M1 scope is roughly a quarter of what the first plan assumed. The
reduced-scope tier below has been narrowed accordingly, and
[the M1 execution plan](milestones/m1-trust-spine-execution-plan.md) is the
authoritative item-level plan.

**Exit gate.** All four, signed by E1 and E2:

1. A real XProtect installation has pushed events through the live adapter path,
   with **integration and replay parity demonstrated** — the same scenarios
   produce the same canonical events whether they arrive live or from a fixture.
2. One incident class runs end to end on a real site: event → graph →
   deterministic verification → VLM observation → incident → operator
   disposition → sealed audit checkpoint. A real operator completed it, timed.
3. Every SLO in `internal/observability/slo.go` is demonstrably met under a
   recorded load run, with the run and its numbers in the release notes.
4. A production VLM profile has passed its promotion gates on a real GPU, in
   shadow mode, for at least 14 consecutive days at a design-partner site, with
   the evaluation report attached.

**Reduced scope if slipping, in two tiers.** The first tier is a real M1. The
second is an M1 that has slipped its date, and it is named here so the decision
is made now rather than in week 20.

*Tier 1 — cut, M1 still signs:*
- Ship **one** VLM profile, not three. Gates 2 and 4 say "a" profile, not three.
- Run gate 3 against one incident class and a bounded event rate rather than the
  full designed load, provided the published SLO numbers are restated to match
  what was actually demonstrated. Publishing an SLO the run did not cover is the
  one thing that is not cuttable.
- Do not cut the audit, the replay path, the tenant-scoping tests, or the
  fault-injection tests. These are the invariants, not features.
- The installer, the service units and the Python runtime are already built, so
  they are not available as cuts. Do not re-open them.

*Tier 2 — the cut does not reach, M1 slips instead:* gates 1, 2 and 4 all
require a real customer installation with live data, and gate 4 requires 14
consecutive days of shadow-mode running at that site. A synthetic demo site
cannot produce a signed M1, so if a real site is not available by **2026-12-01**
the date moves and the milestone re-plans. That decision is made on 2026-12-01
by E1 and E2, not deferred into the slip.

**Capacity reconciliation.** M1's committed scope is sized against the capacity
law (26 weeks × 2 engineers × 60% ≈ 31 engineer-weeks), and the arithmetic is
in [the M1 execution plan](milestones/m1-trust-spine-execution-plan.md) rather
than hidden. The full M1 inventory in
[the product backlog](engineering/product-and-backlog.md) is larger
than that and is therefore **not** a commitment: it is the ordered pool the
committed set was selected from. Moving a row into M1 requires removing
another, in writing, with both recorded in the milestone plan.

**Commercial state.** Design partners only. Up to three, at no charge, in
exchange for a named reference, a measured before/after investigation time, and
access to anonymised incident data for evaluation. No public launch, no pricing,
no self-serve.

**Moat.** Trust. Every decision is attributable, replayable and auditable. This
is the asset that makes every later milestone sellable, and it is the reason a
cheaper but unprovable competitor loses.

### M2 — AI Investigator · 2027-04-01 → 2027-09-30

**Mission.** Move from "AI helps verify one alarm" to "AI investigates security
incidents."

Scope, in this order:

1. **Evidence intelligence.** Given an incident, the platform identifies relevant
   cameras, retrieves the right time windows across them, pulls snapshots and
   clips, gathers metadata, and correlates neighbouring cameras, access events
   and sensor events. The operator stops searching cameras by hand.
2. **VLM profile library.** Ten profiles, each with a structured observation
   schema, a pinned prompt version, a pinned model version, an evaluation
   dataset, a promotion gate, and shadow-then-production stages. A profile that
   has not passed its gate runs in shadow and cannot affect authoritative state.
3. **Investigation orchestration.** A local LLM plans an investigation —
   "what happened around Door 18" becomes a plan over related alarms, cameras,
   time, access events, observations and history — and returns an
   evidence-backed answer. The LLM orchestrates; it never asserts.
4. **Natural-language search and semantic search.** Operators ask in plain
   language; every result carries an evidence reference. `pgvector` for derived
   data only. PostgreSQL stays authoritative.
5. **Incident summaries.** What happened, when, where, supporting evidence,
   contradicting evidence, and unresolved questions. Derived, clearly marked,
   never authoritative.

**Exit gate.** The M1 incident class plus two more, with:

- Mean Time To Investigate reduced by a **statistically meaningful margin** on
  supported classes, measured per operator against their own VMS-only baseline.
  "Meaningful" is set before the trial, per class, and recorded.
- Every search result and every summary carries resolvable evidence references.
- A replayed incident reproduces its summary from stored facts and the stored
  observation, with no model call. A test asserts this.

**Reduced scope if slipping.** Four VLM profiles instead of ten; keyword search
before semantic search; orchestration over cameras and access only, deferring
sensors. Ship the evidence engine before the summaries.

**Commercial state.** Paid pilot. Design-partner sites convert to a paid pilot
at a founding rate. First external customer who is not personally known. The
pricing experiment runs here, not before.

**Moat.** Investigation. We retrieve and correlate evidence across the whole
installed base automatically.

### M3 — AI Operator Copilot · 2027-10-01 → 2028-03-31

**Mission.** Move from "AI investigates" to "AI performs the routine operator
workflow."

1. **Security playbooks.** SOPs compiled into versioned, auditable, simulated
   before activation. Every step references the evidence it consumed.
2. **Recommendation engine.** Structured proposals — dismiss, monitor,
   escalate, request evidence, notify supervisor, move PTZ to an approved
   preset, extend recording, open an incident. The policy engine decides what is
   permitted, and a proposal the policy rejects never reaches an operator.
3. **Safe actions.** The first controlled action set: bookmarks, recording
   extension, PTZ presets, snapshots, incident create/assign, notification.
   Every action is idempotent, authorised, and logged before and after. Anything
   with physical impact on the world stays approval-gated.
4. **Notification integrations.** Teams, Slack, email, webhooks, SIEM. Each
   notification carries incident, severity, evidence, verification state and a
   deep link. Never "Motion detected."
5. **Bounded auto-resolution.** A strictly enumerated set of incident classes
   close without an operator, where the playbook's preconditions are
   deterministic. Every automated decision is audited with the same rigour as a
   human one.

**Exit gate.**

- Operator Intervention Rate — incidents needing human input over total
  incidents — is below an agreed threshold on the bounded class set **while**
  false-negative rate and missed-incident rate do not regress versus M2.
- Every automated action is idempotent under replay, proven by a fault-injection
  test at the action stage.
- An operator can explain, from the product alone, why the system did what it
  did for any incident in the window.

**Reduced scope if slipping.** Playbooks and recommendations, no auto-resolution,
no PTZ. Auto-resolution is the milestone's most valuable and most dangerous
feature; it is the correct thing to cut first.

**Commercial state.** First production GA on **2028-03-31**, the last day of the milestone. GA cannot precede its own exit gate: an earlier date is reachable only through this milestone's pre-declared reduced scope, and if that scope is taken the GA slips with the gate rather than shipping ahead of it. Self-serve is not the goal; a
repeatable sales motion with three named production references is. This is the
first milestone at which a customer other than a design partner can be declined
for capacity rather than for trust.

**Moat.** Workflow. We encode how a real security desk closes incidents.

### M4 — Universal Security Layer · 2028-04-01 → 2028-12-22

**Mission.** Stop being "AI for XProtect" and become "AI for physical security
infrastructure."

1. **ONVIF.** Profile T and Profile M: discovery, events, analytics metadata
   (people, vehicles, licence plates, object counters), PTZ, and streaming
   capability discovery. Profile M is where the free signal density is.
2. **Second full VMS adapter.** Chosen by customer pipeline, not by preference.
   Genetec, FLIR, Avigilon and the Axis ecosystem are the candidates. This is
   the real architecture test: **nothing outside the adapter may require
   rewriting.** The test that proves it is a diff of the non-adapter trees
   across the two adapters, reviewed.
3. **Adapter SDK.** Canonical event, device, evidence and action contracts;
   conformance tests; a fixture framework; a source simulator; a certification
   suite. A third party must be able to write a working adapter without reading
   our source.
4. **Access control.** Credentials, badges, identity references, doors, granted,
   denied, forced open, held open. Forced-door and held-open events are among
   the highest-value correlations in the whole product.
5. **Sensors and IoT.** Intrusion, perimeter, environmental, intercom,
   gate/barrier, LPR, BMS signals. Multimodal.

**Exit gate.** The second VMS adapter is in production at one site, with a
non-adapter diff of zero behavioural changes; the Adapter SDK v1 is public with
a certification suite that has been run by someone outside the company; the
conformance suite is a published artifact.

**Reduced scope if slipping.** ONVIF events and PTZ without Profile M
analytics. Access control before sensors. A second VMS adapter is not
cuttable — without it, M4 is a claim, not a layer.

**Commercial state.** Platform expansion. First partner conversations with VMS
OEMs and regional integrators. An OEM conversation is not a sale and is not
forecast as one until a signed pilot exists.

**Moat.** Integration. We speak everything the installed base speaks.

### M5 — Enterprise Command Platform · 2029-01-04 → 2029-09-28

**Mission.** Move from single sites to whole organisations, without ever making
a site depend on our central plane.

1. **Optional central control plane.** Sites stay autonomous. Central
   connectivity disappearing changes nothing about site operation. This is a
   hard architectural constraint with a fault-injection test, not a feature.
2. **Fleet management.** Site versions, adapter versions, rule bundles, model
   versions, health, storage, licences, configuration drift.
3. **Enterprise identity.** OIDC, Microsoft Entra ID, Active Directory, LDAP,
   SCIM. Local authentication remains available for isolated sites, always.
4. **RBAC graduating to RBAC + ABAC.** View one site; export evidence as
   supervisor; see all regions as director; manage devices but not evidence as
   integrator.
5. **High availability.** Core failover, database HA, adapter restart and
   recovery, evidence-storage redundancy, site backup, DR. The DR drill must
   still surface the real finding from M1: a restored ledger is provably behind
   the seal, because a backup is a snapshot of an earlier moment. That gap gets
   designed around, not explained away.
6. **Multi-site correlation.** The same credential denied at Site A, denied at
   Site B, attempted at Site C is one incident, not three. The Security Graph
   becomes organisation-wide.

**Exit gate.** A three-site customer running production with the control plane
pulled mid-incident and no site degradation; OIDC and SCIM provisioning tested
against two real identity providers; an HA failover demonstrated under load with
measured RTO and RPO that match what we published.

**Reduced scope if slipping.** OIDC and SCIM before local-federation breadth.
ABAC before HA. Multi-site correlation last within the milestone, though it is
the most differentiated piece and the one enterprise buyers ask about first.

**Commercial state.** Enterprise GA. Credible for corporate campuses, logistics,
healthcare, manufacturing, critical infrastructure, large retail, government and
multi-site organisations.

**Moat.** Enterprise. We operate across an organisation.

### M6 — Site Intelligence · 2029-10-01 → 2030-06-28

**Mission.** The platform learns the site. This is where the product becomes
genuinely different from anything a VMS vendor ships.

1. **Site model.** Tenant → campus → building → floor → zone → door → camera →
   sensor, with camera adjacency, field-of-view relationships, allowed movement
   paths, entrances and exits, and restricted areas.
2. **Behavioural baselines.** Normal occupancy, movement, vehicle arrival, door
   activity, day/night, weekday/weekend, seasonal. The output is
   *"differs significantly from this site's baseline"*, never *"abnormal =
   dangerous."* The distinction is a product requirement, not a nuance.
3. **Site memory.** Long-term derived knowledge: repeated false alarms, common
   operator decisions, maintenance patterns, recurring incidents, site
   exceptions, historical response outcomes.
4. **Operator feedback learning.** Every disposition becomes evaluation data.
   AI said vs operator said vs final outcome, tracked per class and per site.
5. **Drift detection.** Camera moved, site activity changed, accuracy degraded,
   lighting changed, a rule became noisy, override rate rose. Models and
   policies do not silently decay.
6. **AI-proposed rules.** The AI proposes "suppress when X + Y + Z", and the
   flow is always: proposal → simulation → historical replay → impact diff →
   human approval → activation. The AI never activates its own rule.
7. **Camera health intelligence.** Blur, obstruction, movement, low FPS, frozen
   image, exposure, night-mode failure. The target state is not "camera online"
   but "camera operationally useful".

**Exit gate.** A site where baselines, memory and drift detection are live; a
proposed rule with a historical impact diff reviewed and approved by a customer
operator; and the M6 review confirms the compounding claim in
`` §5 — a new site costs more to onboard than an
existing site costs to keep. If it does not, the data moat did not materialise
and M7–M8 are re-planned as automation on top of search.

**Reduced scope if slipping.** Baselines and drift detection before the richer
site model. Site memory before feedback learning. No AI-proposed rules — that
feature is the last thing in the milestone, not the first.

**Commercial state.** Intelligence premium. A paid tier on top of the platform,
justified by measured reduction in operator minutes, sold on a before/after
number the customer produced.

**Moat.** Context. We know what normal looks like at each site, and we notice
when it changes.

### M7 — Autonomous Security Operations · 2030-07-01 → 2031-06-30

**Mission.** From copilot to operator, for approved workflows only.

1. **Autonomy levels A0–A4**, per site, chosen by the customer: observe, assist,
   recommend, execute-with-approval, policy-controlled autonomy. A site raising
   its level is an explicit, audited, reversible act with a blast-radius
   preview.
2. **Persistent incident agents.** One per active incident, continuously
   gathering evidence, watching related cameras and events, updating the
   timeline, checking new access events, updating verification, evaluating
   playbook state, until the incident closes.
3. **Closed loop.** detect → verify → act → observe outcome → verify the action
   succeeded → audit. An action that is not verified as having succeeded is a
   failed action, not a completed one.
4. **Autonomous routine operations.** Suppress validated nuisance alarms, collect
   evidence, start recording, track progression, notify staff, build the
   incident package, maintain the timeline, close resolved low-risk events.
   Sensitive and high-impact actions stay behind explicit policy and
   authorisation, permanently.
5. **Exception-driven SOC.** The operator surface becomes an exception queue:
   not 813 alerts, but 12 incidents of which 9 were handled automatically, 2
   need review and 1 is urgent. This is the product's destination UX.

**North Star metric, live from here.** Human Attention Minutes per 1,000
security events, which must fall continuously, and is never reported without
its four companions: missed-incident rate, operator override rate, false
escalation rate, evidence completeness. Automation alone is not success.
Trusted automation is success, and the four companions are how we prove the
difference rather than assert it.

**Exit gate.** An A4 site running routine operations on policy for 60
consecutive days; the closed loop verified end to end including action-outcome
verification; and the North Star metric falling with no companion metric
regressing.

**Reduced scope if slipping.** A2 and A3 before A4. Persistent agents for
low-severity incidents only. The exception queue UX before autonomous
resolution — if we ship autonomy with a worse surface than the alert list we
replaced, operators will route around us.

**Commercial state.** Autonomous operations. Sold per site per month, priced on
the exception queue's value. Every autonomy level increase is a customer
decision with a documented rollback, and the contract says so.

**Moat.** Autonomy. We perform security operations.

### M8 — Physical Security OS · 2031-07-01 → 2032-12-31

**Mission.** Become the platform other companies build on.

1. **Developer platform.** Adapter SDK, Event SDK, Action SDK, AI Observation
   SDK, Rule SDK, webhooks, graph APIs. Public, versioned, documented, with
   conformance suites.
2. **Marketplace.** Installable VMS adapters, access-control adapters, sensor
   adapters, rule packs, incident profiles, VLM profiles, integration packs,
   regional compliance packs. All signed, all installed offline.
3. **Model marketplace.** Approved models as signed offline bundles with
   hardware profile, accuracy/speed trade-off and permitted-use declared. The
   core stays model-agnostic; the customer's hardware and risk tolerance choose
   the model.
4. **Vertical packs.** Logistics, retail, healthcare, manufacturing, campus. The
   core stays horizontal; the packs carry domain intelligence as rules, profiles
   and playbooks, not as a fork.

**Exit gate.** A third-party adapter, built by someone outside the company,
passes the public certification suite and runs in production at a customer site;
a signed model bundle installs offline on customer-specified hardware; a
vertical pack is installed at a customer who was not the design partner for it.

**Reduced scope if slipping.** Rule packs before adapters. One vertical pack
done properly before five done thinly. The marketplace's value is that
third-party supply exists, and one real third party beats fifty unreviewed
uploads.

**Commercial state.** Platform and marketplace. Revenue is SDK support, OEM
licensing, marketplace take rate, and vertical packs.

**Moat.** Ecosystem. Others build on us.

## The two parallel tracks

Neither waits for M8. Each is a continuous, staffed obligation, not a project.

### Trust and Security

Supply-chain integrity — signed builds, SBOM, provenance, reproducible release
pipeline, offline verification. AI governance — every model carries version,
hash, evaluation, approved use cases, known limitations and deployment history.
Privacy — retention, redaction, privacy zones, anonymisation, selective
persistence, regional policy. Continuous cybersecurity hardening across
adapters, auth, secrets, certificates, APIs, evidence, installers and update
packages. Evidence integrity — adopt stronger media provenance as the ecosystem
matures, including ONVIF's media-signing direction and its tightening
cybersecurity requirements, rather than building our own.

The full program, with owners and cadence, is in
``. It is written against the five invariants in
`AGENTS.md`, which are the law and which no milestone may weaken without an ADR
and a test, in that order.

### Product analytics

From M1 onward: operator efficiency (Mean Time To Verify, MTTI = Mean Time To Investigate, human attention minutes,
operator actions per incident); AI effectiveness (agreement, false-positive,
false-negative, insufficient-evidence, override rate); automation (auto-triaged,
auto-resolved, human-intervention, failed-action rates); platform health
(accepted event loss, event latency, evidence latency, VLM latency, adapter
availability, backlog, storage growth).

Definitions, sources and reporting cadence are in
[the metrics program](engineering/metrics-and-analytics.md). A metric without a
definition, an owner and a report is not a metric, and adding one is a change
with a cost.

## Sequence dependencies that are not negotiable

- Contracts before code. `contracts/` changes first, regeneration follows, and
  `contractcheck` decides whether the wire is broken. Nothing hand-edits
  `internal/contracts/`.
- Migrations ship expand and contract separately, and a contract step never
  bundles with the expand step that makes it safe. Forward-only; an applied
  migration is never edited.
- AI profiles gate on evaluation. No profile reaches production because a demo
  looked convincing. Shadow first, gates named individually, never a single
  boolean.
- The deterministic core stays functional with the AI plane entirely absent. If
  a change makes the product unusable when the model is unreachable, that change
  is wrong regardless of its value when the model is present.
- M4's second adapter is the architecture test. The M1 adapter's existence does
  not prove the abstraction holds.
- M6's compounding claim is checked, not assumed. See the M6 exit gate.
- **There is no `internal/policy` package yet**, and M3's recommendation engine,
  M6's rule-approval flow and all of M7's autonomy levels depend on it. It is a
  first-class M3 deliverable, not an incidental component, and it is the
  component that decides what the AI is permitted to do. Treat it as the
  highest-consequence new package in the plan.

## What this review got wrong, and what it found

A plan that pretends its own review found nothing is not a reviewed plan. These
were found while writing this document set and are carried as open items with
owners in [the risk register](company/risk-register.md).

**Defects in the plan itself, now fixed in the text above:**

1. **M1 was oversubscribed by roughly 2.5×** — a ~78 engineer-week backlog
   against ~31 weeks of capacity. The committed set is now stated separately
   from the pool it was selected from.
2. **The original M1 reduced-scope cut could not produce a signed milestone**,
   because it permitted a synthetic demo site while the exit gate requires a
   real one. Now split into two tiers with a decision date of 2026-12-01.
3. **The North Star metric was absent from the document named for it**, first
   appearing five years out in the M7 section. Now stated up front with its four
   mandatory companions.
4. **GA was dated before its own exit gate.** GA and the M3 window are now both
   2028-03-31, and the coupling is explicit.
5. **An M4 → M5 gap of 13 days was unlabelled.** Now a declared holiday buffer.

**Defects in the code, found while grounding the plan, and not yet fixed.** These
are more serious than anything above and are listed here because the plan is the
document a reader is most likely to trust:

| # | Finding | Consequence |
|---|---|---|
| C1 | **The Go core has no client for the AI runtime.** Nothing in any `.go` file calls the inference endpoint; `jobs.QueueVLM` is a queue name with no producer or consumer. | M1 gate 4 is blocked by a missing *connection*, not only a missing model. `docs/status.md` names three unsigned DoD items and this is a fourth. |
| C2 | **The runtime's bundle check verifies a digest, not a signature.** `ModelBundle.verify()` compares `sha256(manifest)` against the *contents* of `manifest.sig` and never uses `public_key`, while the docstring and error message both say "signature". | A bundle whose `.sig` file contains a hash is accepted as if it were signed. This is the one finding that undermines a supply-chain claim in a document that is otherwise careful about them. |
| C3 | **The adapter's known-event-type table is keyed on spellings normalisation never produces.** 12 of 18 keys are unreachable and 9 types that are produced are missing. | Real motion, analytics and access events are marked malformed. Bounded today only because `RawRecord.Malformed` has no consumer outside the adapter. |
| C4 | **`evaluation.Evaluate` hard-codes the `restricted-person` field**, so every other profile scores recall 0 and can never pass a gate. | Blocks M2's "the M1 incident class plus two more". |
| C5 | **The labelled-case minimum is statistically insufficient.** At n=30 an observed 0.20 false-positive rate has a Wilson upper bound of 0.373, and a 0.70 recall has a lower bound of 0.521. ~200 cases are needed for the stated gates. | A profile could pass its gates on evidence that cannot support them. Full working in [the AI track](engineering/ai-and-data.md) §3. |
| C6 | **`internal/observability` has no production caller**, so no metric is emitted. | M1 gate 3 is unreachable, not merely unmet. |
| C7 | **A rule can legally name `aiObservation.field: summary`** although the schema says it is never evaluated by a rule, and nothing enforces either. | A weak, invariable constraint on the AI trust boundary. |
| C8 | **`internal/graph`'s package doc claims a test asserts the absence of `entity_aliases`; no such test exists.** | M1's no-person-model decision is protected by a comment. |
| C9 | **`fixture.Event.ObservedAt` is never called and `ReplayTransport.Get` discards the epoch it computes.** | Replay parity today is byte-parity, not timing-parity, which changes what M1 gate 1 means. Needs an E1 decision before the gate is attempted. |
| C10 | **CI pinned Go 1.24 with `GOTOOLCHAIN: local` against a `go 1.26.0` module.** | CI could not build the project at all. Fixed to `1.26`; `docs/status.md`'s "Go 1.27" was the remaining stale claim, now corrected. |
| C11 | **There is no `cmd/security-core`, but both service units invoke one.** `make build` produces only `securityctl` and `security-migrate`; `internal/api.Server.ListenAndServe` has no `main` caller. | The service units cannot start as written, so the lifecycle has been exercised against a manually run process rather than a supervised service. The unit also declares `Type=notify` with no `sd_notify` handshake, and its `MemoryDenyWriteExecute`, `SystemCallFilter=@system-service` and `PrivateDevices` hardening each preclude a GPU runtime. |
| C12 | **`contractcheck` silently ignores `map<...>` fields and never diffs OpenAPI or the JSON Schemas.** `Device.attributes` is absent from the baseline. | The compatibility gate is narrower than "the contracts are the source of truth" implies. A breaking change to a map field, or to any HTTP or JSON-Schema surface, passes silently. |
| C13 | **`VerifyChecksums` hashes the migration file body, not its name.** | Renaming an applied migration passes verification while the ledger's `name` column silently diverges from the file. |
| C14 | **The API access log carries no `tenant_id`, `site_id` or `request_id`**, which the conventions require on every line. | An operator cannot correlate an HTTP request to a ledger row or a log line. |

C2 and C3 are the two I would fix this week. Both are small, both are
contained, and both are the kind of defect that a security buyer's engineer
finds in an afternoon. C11 is close behind, because a customer whose service
will not start is a customer we cannot pilot with.
