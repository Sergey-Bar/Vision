# Roadmap — index and operating rules

This directory is the company's plan. It answers: what we build, in what order,
on what dates, gated by what, sold to whom, and what happens when it slips.

**Order of reading.** `00` then `01`, then the document for whatever you own.
Do not read a track document before `00` and `01`; they set the dates and the
constraints that the track documents obey.

## The documents

### Spine — read these first

| Doc | What it decides |
|---|---|
| [00 — Strategy and North Star](00-strategy-and-north-star.md) | Positioning, the five segments in order, the anti-segments, the three buyer questions, the North Star metric and its four mandatory companions, the moat sequence, the capacity law, the evidence discipline. **Overrides everything else on conflict.** |
| [01 — Master Product Roadmap](01-master-roadmap.md) | The calendar. M1–M8 with windows, missions, exit gates, commercial states, pre-declared reduced-scope cuts, moats, the two parallel tracks, the non-negotiable sequence dependencies, and what the first review pass got wrong. |

### Execution

| Doc | What it decides |
|---|---|
| [02 — M1 execution plan](02-m1-trust-spine-execution-plan.md) | Item-level plan for the milestone running now: W1–W9, 65 task IDs, a 26-week calendar with the 40%-tax weeks marked, the critical path with float, M1 risks, and the exit gate as a signed checklist with the artefact each line requires. |
| [03 — Team structure and operating model](03-team-structure-and-operating-model.md) | Every R&D role and who holds it, the AI-agent roster and its limits, the week, the RACI, the intake filter, customer communication, and the single-human on-call reality. |
| [05 — Product and backlog](05-product-and-backlog.md) | Product principles, the M1–M8 capability backlog, work-item anatomy with fill-in templates, the prioritisation model, discovery, and the product–engineering contract. |
| [19 — Decision log and process](19-decision-log-and-process.md) | Decision taxonomy, the ADR process, AI-agent rules of engagement reconciled with `AGENTS.md`, the settled-decisions index seeded from the code, and the open decisions with dates. |

### Engineering tracks

| Doc | What it decides |
|---|---|
| [06 — UX and design system](06-ux-and-design-system.md) | The operator's actual context, IA and routes, the three critical flows wireframed, design tokens and the CSP-constrained component table, AI-unavailable as a first-class state, accessibility targets, and visual identity. |
| [07 — Quality engineering](07-quality-engineering.md) | The quality thesis, the real test shape, the invariant→test matrix, golden discipline, the kill -9 programme, path-traversal testing, the release gate, domain severities, and the coverage-theatre refusals. |
| [08 — Metrics and analytics](08-metrics-and-analytics.md) | The metric tree, real definitions with real Postgres sources, what is measured when, the M2 exit-gate experiment design, AI quality measurement, guardrails that halt a release, and the metrics we refuse to track. |
| [09 — Integration and adapter track](09-integration-and-adapter-track.md) | The adapter contract as built, the canonical vocabulary, vendor-leak prevention, ONVIF, the second VMS adapter and the non-adapter diff test, the Adapter SDK, access control and sensors, the Security Graph, and the durability contract every adapter must pass. |
| [10 — AI and data track](10-ai-and-data-track.md) | The AI tiers as built, invariant 3 operationally, the VLM profile programme and its statistics, the local LLM and prompt injection, evidence intelligence, semantic search, site intelligence, autonomy A0–A4, the model abstraction, evaluation, and the AI risk register. |
| [11 — Hiring plan and org growth](11-hiring-plan-and-org-growth.md) | Why the first hire is not an engineer, a dated headcount plan tied to milestone gates, first-90-days for the three hires that matter, the fractional route, interview design including AI-usage assessment, and a 12-month forward view. |
| [12 — Release, SRE and support](12-release-sre-and-support.md) | The release train, deployment topology, offline and air-gapped operation, service units, observability, backup/DR including the honest restore-behind-seal finding, the support model, the 02:00 operations playbook, and disclosure intake. |

### Trust, compliance, risk

| Doc | What it decides |
|---|---|
| [13 — Security and trust program](13-security-and-trust-program.md) | The continuous program: supply chain, AI governance, privacy, hardening, evidence integrity and media provenance, vulnerability management, the invariant→control matrix, accepted risks, and when to re-threat-model. |
| [14 — Compliance and certifications](14-compliance-and-certifications.md) | Separates complying with a control set from independent confirmation from regulatory approval. SOC 2, ISO 27001, ISO 42001, pen test, EU AI Act readiness, privacy regimes, data residency, and the contract inventory. |
| [16 — Risk register](16-risk-register.md) | 54 scored risks with owners, early-warning signals and pre-agreed responses; a top ten by expected damage; pre-mortems for M1, M2 and M3 GA; the escalation ladder. |

### Company

| Doc | What it decides |
|---|---|
| [04 — GTM and sales](04-gtm-and-sales.md) | Segment playbooks, the design-partner motion, paid-pilot and GA motion, the 25-minute demo run sheet, sales collateral, the OEM channel, founder time budget, the competitive frame, and pipeline hygiene. |
| [15 — Launch plan](15-launch-plan.md) | The GA checklist T-90 to T+30, the four readiness gates, launch assets, rollout and rollback, the capability kill switch, hypercare, the 30-day scorecard, and the unglamorous risks. |
| [17 — Pricing and packaging](17-pricing-and-packaging.md) | Pricing principles, packaging options per milestone, the autonomy pricing question, the OEM model, metering, offline licensing, unit economics, and procurement readiness. |
| [18 — Financial plan](18-financial-plan.md) | Assumptions, an 18-month cash model with three scenarios, break-even, the capital plan and what each stage's diligence will hit hardest, cost structure by milestone, and the three financial risks that kill companies like this. |

## How this directory is used

- **`00` and `01` are the contract.** A track document that contradicts either is
  wrong, and the fix is to the track document.
- **Numbers carry their provenance.** `PROPOSED`, `ESTIMATE`, `ASSUMPTION` and
  `NOT YET KNOWN` are load-bearing. A bare number in this directory is either
  measured, in which case the document says where, or it is a defect.
- **Exit gates are signed by a human, always.** The AI agents in
  `03-team-structure-and-operating-model.md` write code and draft tests. They do
  not decide scope, sign a gate, contact anyone, or commit a date.
- **Review cadence.** `00`, `01`, `02` and `16` are reviewed monthly, immediately
  at any milestone boundary, and whenever an exit gate changes. Everything else
  is reviewed when its milestone enters its final quarter. Every document carries
  a last-revised date; a document without one is stale and should not be cited.
- **The build state lives elsewhere.** `docs/status.md` is the source of truth
  for what is built and what is not. This directory plans; that one reports.

## The five invariants, which are the law

Restated here because every document in this directory defers to them, and
because they are the only rules in the company that cannot be traded away.
Weakening any one requires an ADR and a failing test, in that order.

1. **Zero accepted event loss.** Anything reaching `inbox_events` reaches a
   terminal state unless explicitly dead-lettered. `kill -9` at any stage: no
   loss, no duplicate side effect.
2. **Decisions replay from stored facts and the stored `AIObservation`.** A
   `replayedFromStoredFacts` response that re-inferred anything is a bug.
3. **A missing AI observation is never a negative observation.** AI is never on
   the availability path. Every rule consuming a Tier C signal declares
   `if_ai_unavailable`.
4. **The audit ledger is append-only and hash-chained.** Editing any row fails
   checkpoint verification.
5. **Tenancy is enforced in the data layer.** `tenant_id` is `NOT NULL`
   everywhere and is never read from a request body. `site_id` is `NOT NULL`
   only on physically site-bound records.

Working agreement, treated with the same weight: **bundles are untrusted input.**
Rule bundles, release archives and model bundles are verified before execution.
No shell, no SQL, no network, no dynamic code, no unbounded regex.

## The 26-week question

A useful thing to be able to answer at any moment, and the reason this
directory is shaped the way it is: *what is the product, and what can it prove,
26 weeks from today?* The answer is in `02-m1-trust-spine-execution-plan.md`.
If that answer ever becomes vague, the roadmap has drifted from the plan.
