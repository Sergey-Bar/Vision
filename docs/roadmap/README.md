# Roadmap

The plan, from today to product. What we build, in what order, on what dates,
gated by what, sold to whom, and what happens when it slips.

Start at **[`strategy-and-north-star.md`](strategy-and-north-star.md)**. It
overrides everything else in this directory on conflict.

> **A note on continuous integration.** There is no CI workflow committed under
> `.github/workflows/` yet, so the gate that runs is `scripts/verify.ps1`,
> locally, run by a person. When CI is added, `make verify` should delegate to
> the same script from a workflow, and this note must be updated in the same
> change.
>
> **Any reference to `ci.yml`, to a CI job, or to a pipeline claiming it does not
> exist is historical.** It records what was true when the document was written
> while the repository was private, and it stays true until a workflow file is
> committed.

## Read this much and stop

| If you have | Read |
|---|---|
| Five minutes | This page, then the two spine documents below |
| One hour | Both spine documents, then the track you own |
| A milestone to run | The spine, the milestone plan, and the two tracks it touches |

The full set is about 17,000 lines. It is not a set to read end to end; it is a
set to navigate. Every document states what it decides and what it defers to.

## The spine — the two documents everything obeys

| Document | What it decides |
|---|---|
| [`strategy-and-north-star.md`](strategy-and-north-star.md) | Positioning; the five customer segments in order; the anti-segments; the three questions every buyer asks; the North Star metric and its four mandatory companions; the moat sequence; the capacity law; the evidence discipline. |
| [`master-roadmap.md`](master-roadmap.md) | The calendar. M1–M8 from 2026-09-28 to 2032-12-31, each with a mission, a signed exit gate, a commercial state, a pre-declared reduced-scope cut, and a moat. Plus the two parallel tracks, the non-negotiable sequence dependencies, and a frank list of what this plan got wrong and what it found in the code. |

## Milestones

| Document | What it decides |
|---|---|
| [`milestones/m1-trust-spine-execution-plan.md`](milestones/m1-trust-spine-execution-plan.md) | The plan running right now: workstreams, 65 task IDs, a 26-week calendar with the 40% tax weeks marked, the critical path with float, M1 risks, and the exit gate as a signed checklist with the artefact each line requires. |
| [`milestones/m2-ai-vms-execution-plan.md`](milestones/m2-ai-vms-execution-plan.md) | M2 after the 2026-10-21 strategy change: the ten locked decisions, the fifteen root fixes and the gate that proves each, the target architecture and the record path specified line by line, the ten phases with their exit gates, the sixteen new gates, the commercial spine, and the eight risks the programme adds. |

## Engineering tracks

| Document | What it decides |
|---|---|
| [`engineering/product-and-backlog.md`](engineering/product-and-backlog.md) | Product principles, the M1–M8 capability backlog, work-item anatomy with fill-in templates, the prioritisation model, discovery, and the product–engineering contract. |
| [`engineering/ux-and-design-system.md`](engineering/ux-and-design-system.md) | The operator's actual context, IA and routes, the three critical flows wireframed, design tokens and the CSP-constrained component table, AI-unavailable as a first-class state, accessibility targets, visual identity. |
| [`engineering/quality-engineering.md`](engineering/quality-engineering.md) | The quality thesis, the real test shape, the invariant→test matrix, golden discipline, the kill -9 programme, path-traversal testing, the release gate, domain severities, and the coverage-theatre refusals. |
| [`engineering/metrics-and-analytics.md`](engineering/metrics-and-analytics.md) | The metric tree, real definitions with real Postgres sources, what is measured when, the M2 exit-gate experiment design, AI quality measurement, guardrails that halt a release, and the metrics we refuse to track. |
| [`engineering/integrations-and-adapters.md`](engineering/integrations-and-adapters.md) | The adapter contract as built, the canonical vocabulary, vendor-leak prevention, ONVIF, the second VMS adapter and the non-adapter diff test, the Adapter SDK, access control and sensors, the Security Graph. |
| [`engineering/ai-and-data.md`](engineering/ai-and-data.md) | The AI tiers as built, invariant 3 operationally, the VLM profile programme and its statistics, the local LLM and prompt injection, evidence intelligence, semantic search, site intelligence, autonomy A0–A4, evaluation, the AI risk register. |
| [`engineering/release-sre-and-support.md`](engineering/release-sre-and-support.md) | The release train, deployment topology, offline and air-gapped operation, service units, observability, backup and DR including the honest restore-behind-seal finding, the support model, the 02:00 operations playbook, disclosure intake. |
| [`engineering/team-and-operating-model.md`](engineering/team-and-operating-model.md) | Every R&D role and who holds it, the AI-agent roster and its limits, the week, the RACI, the intake filter, customer communication, and the single-human on-call reality. |
| [`engineering/decisions-and-process.md`](engineering/decisions-and-process.md) | Decision taxonomy, the ADR process, AI-agent rules of engagement reconciled with `AGENTS.md`, the settled-decisions index seeded from the code, and the open decisions with dates. |

## Trust and compliance

| Document | What it decides |
|---|---|
| [`security/trust-and-security-program.md`](security/trust-and-security-program.md) | The continuous program: supply chain, AI governance, privacy, hardening, evidence integrity and media provenance, vulnerability management, the invariant→control matrix, accepted risks, when to re-threat-model. |
| [`security/compliance-and-certifications.md`](security/compliance-and-certifications.md) | Separates complying with a control set from independent confirmation from regulatory approval. SOC 2, ISO 27001, ISO 42001, pen test, EU AI Act readiness, privacy regimes, data residency, the contract inventory. |

## Company

| Document | What it decides |
|---|---|
| [`company/gtm-and-sales.md`](company/gtm-and-sales.md) | Segment playbooks, the design-partner motion, paid-pilot and GA motion, the 25-minute demo run sheet, sales collateral, the OEM channel, founder time budget, the competitive frame, pipeline hygiene. |
| [`company/pricing-and-packaging.md`](company/pricing-and-packaging.md) | Pricing principles, packaging per milestone, the autonomy pricing question, the OEM model, metering, offline licensing, unit economics, procurement readiness. |
| [`company/launch-plan.md`](company/launch-plan.md) | The GA checklist T-90 to T+30, the four readiness gates, launch assets, rollout and rollback, the capability kill switch, hypercare, the 30-day scorecard. |
| [`company/risk-register.md`](company/risk-register.md) | 54 scored risks with owners, early-warning signals and pre-agreed responses; a top ten by expected damage; pre-mortems for M1, M2 and M3 GA; the escalation ladder. |
| [`company/financial-plan.md`](company/financial-plan.md) | Assumptions, an 18-month cash model with three scenarios, break-even, the capital plan and what each stage's diligence will hit hardest, cost structure by milestone, the three financial risks that kill companies like this. |
| [`company/hiring-and-org-growth.md`](company/hiring-and-org-growth.md) | Why the first hire is not an engineer, a dated headcount plan tied to milestone gates, first-90-days for the three hires that matter, the fractional route, interview design including AI-usage assessment. |

## How this directory is used

- **The spine is the contract.** A track document that contradicts
  `strategy-and-north-star.md` or `master-roadmap.md` is wrong, and the fix is
  to the track document.
- **Numbers carry their provenance.** `PROPOSED`, `ESTIMATE`, `ASSUMPTION` and
  `NOT YET KNOWN` are load-bearing. A bare number here is either measured — and
  the document says where — or it is a defect.
- **Exit gates are signed by a human, always.** The AI agents in
  `engineering/team-and-operating-model.md` write code and draft tests. They do
  not decide scope, sign a gate, contact anyone, or commit a date.
- **Review cadence.** The two spine documents, the M1 plan and the risk register
  are reviewed monthly, immediately at any milestone boundary, and whenever an
  exit gate changes. Everything else is reviewed when its milestone enters its
  final quarter. Every document carries a last-revised date; one without it is
  stale and should not be cited.
- **The build state lives elsewhere.** `docs/status.md` is the source of truth
  for what is built. This directory plans; that one reports.
- **Cross-references use relative paths**, so a document can be moved without
  breaking its links, and every link in this directory is verified to resolve.

## The five invariants, which are the law

Restated here because every document above defers to them, and because they are
the only rules in the company that cannot be traded away. Weakening any one
requires an ADR and a failing test, in that order.

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

Treated with the same weight: **bundles are untrusted input.** Rule bundles,
release archives and model bundles are verified before execution. No shell, no
SQL, no network, no dynamic code, no unbounded regex.

## The 26-week question

A useful thing to be able to answer at any moment, and the reason this
directory is shaped the way it is: *what is the product, and what can it prove,
26 weeks from today?* The answer is in
[`milestones/m1-trust-spine-execution-plan.md`](milestones/m1-trust-spine-execution-plan.md).
If that answer ever becomes vague, the roadmap has drifted from the plan.
