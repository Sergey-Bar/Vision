# docs/

An index, not an essay. Every document below is listed once, with what it
answers and when to read it. If a document is not here, it does not exist.

The architecture of the Go codebase is documented in
[`../internal/README.md`](../internal/README.md) — a package map with the
dependency rules, the three seams and a reading order. There is no separate
architecture document, by design.

---

## The index

"Day one" means before you write code. "Writing code" means while you are
changing something. "Shipping" means before a release leaves the building.
"Selling" means you are talking to a customer, an investor or a candidate.
"Never unless you own it" means it is not yours to act on.

### Top level

| Path | What it answers | When to read it |
|---|---|---|
| [`../AGENTS.md`](../AGENTS.md) | The five invariants and the working conventions | Day one, and every day after |
| [`../README.md`](../README.md) | What the product is, in one screen | Day one. It defers to `status.md` for build state |
| [`../internal/README.md`](../internal/README.md) | Where every Go package is and what it is for | Day one, before you touch Go |
| [`status.md`](status.md) | What is built, what is not, and why | Day one, and any time someone says "we already do that" |
| [`threat-model.md`](threat-model.md) | The security decision record: assets, boundaries, abuse cases, and the test that proves each control | Writing code that touches a boundary; before a release |
| This file | Which document to trust for what | Day one |

### Roadmap spine

| Path | What it answers | When to read it |
|---|---|---|
| [`roadmap/README.md`](roadmap/README.md) | The map of `docs/roadmap/`, and the rules that directory obeys | Day one. Five minutes, then stop |
| [`roadmap/strategy-and-north-star.md`](roadmap/strategy-and-north-star.md) | Who we sell to, the North Star metric and its four mandatory companions, the anti-segments, the capacity law | Selling; and once, to know what "better" means |
| [`roadmap/master-roadmap.md`](roadmap/master-roadmap.md) | The M1–M8 calendar with dated exit gates, reduced-scope cuts, and a frank list of what the plan got wrong | Writing code (which milestone, which gate); selling |

### Roadmap — milestones

| Path | What it answers | When to read it |
|---|---|---|
| [`roadmap/milestones/m1-trust-spine-execution-plan.md`](roadmap/milestones/m1-trust-spine-execution-plan.md) | The plan running right now: workstreams, 65 task IDs, a 26-week calendar, the critical path, and the exit gate as a signed checklist | Writing code. This is the item-level plan |

### Roadmap — engineering tracks

| Path | What it answers | When to read it |
|---|---|---|
| [`roadmap/engineering/product-and-backlog.md`](roadmap/engineering/product-and-backlog.md) | Product principles, the M1–M8 capability backlog, prioritisation, the product–engineering contract | Writing code (why this is being built); selling |
| [`roadmap/engineering/quality-engineering.md`](roadmap/engineering/quality-engineering.md) | The invariant→test matrix, golden discipline, the kill -9 programme, path-traversal testing, coverage-theatre refusals | Writing code. Read before adding a test |
| [`roadmap/engineering/integrations-and-adapters.md`](roadmap/engineering/integrations-and-adapters.md) | The adapter contract as built, vendor-leak prevention, the Security Graph, the second VMS adapter, the Adapter SDK | Writing code in `internal/milestone`, `internal/graph` or `internal/normalize` |
| [`roadmap/engineering/ai-and-data.md`](roadmap/engineering/ai-and-data.md) | The AI tiers, invariant 3 operationally, the VLM profile programme, evaluation, the AI risk register | Writing code in `internal/observations` or `internal/evaluation` |
| [`roadmap/engineering/metrics-and-analytics.md`](roadmap/engineering/metrics-and-analytics.md) | The metric tree with real Postgres sources, the M2 experiment design, guardrails that halt a release | Writing code that emits a metric; before a release |
| [`roadmap/engineering/release-sre-and-support.md`](roadmap/engineering/release-sre-and-support.md) | The release train, offline and air-gapped operation, service units, observability, backup and DR, the support model | Shipping |
| [`roadmap/engineering/ux-and-design-system.md`](roadmap/engineering/ux-and-design-system.md) | The operator's context, routes, the three critical flows, design tokens under CSP, AI-unavailable as a first-class state | Writing code in `internal/api` templates |
| [`roadmap/engineering/team-and-operating-model.md`](roadmap/engineering/team-and-operating-model.md) | Every R&D role and who holds it, the week, the RACI, the intake filter | Day one, as a new engineer; selling, as a candidate |
| [`roadmap/engineering/decisions-and-process.md`](roadmap/engineering/decisions-and-process.md) | The ADR process, AI-agent rules of engagement reconciled with `AGENTS.md`, the settled-decisions index | Before proposing a change to an invariant; never to act on a process decision without the owner's agreement |

### Roadmap — trust and compliance

| Path | What it answers | When to read it |
|---|---|---|
| [`roadmap/security/trust-and-security-program.md`](roadmap/security/trust-and-security-program.md) | The continuous program: supply chain, AI governance, hardening, evidence integrity, vulnerability management, when to re-threat-model | Shipping; never unless you own security |
| [`roadmap/security/compliance-and-certifications.md`](roadmap/security/compliance-and-certifications.md) | The difference between complying with a control set, independent confirmation, and regulatory approval; SOC 2, ISO 27001, ISO 42001, EU AI Act readiness | Selling, to a regulated customer. Never a substitute for engineering work |

### Roadmap — company

| Path | What it answers | When to read it |
|---|---|---|
| [`roadmap/company/gtm-and-sales.md`](roadmap/company/gtm-and-sales.md) | Segment playbooks, the design-partner and paid-pilot motions, the demo run sheet, the competitive frame | Selling |
| [`roadmap/company/pricing-and-packaging.md`](roadmap/company/pricing-and-packaging.md) | Pricing principles, packaging per milestone, metering, offline licensing, unit economics | Selling; never to quote a number without reading it first |
| [`roadmap/company/launch-plan.md`](roadmap/company/launch-plan.md) | The GA checklist T-90 to T+30, four readiness gates, rollout and rollback, the capability kill switch | Shipping at a milestone boundary |
| [`roadmap/company/risk-register.md`](roadmap/company/risk-register.md) | Scored risks with owners, early-warning signals and pre-agreed responses; pre-mortems for M1, M2 and M3 GA | Never unless you own it. But read the top ten before sizing a milestone |
| [`roadmap/company/financial-plan.md`](roadmap/company/financial-plan.md) | An 18-month cash model with three scenarios, break-even, the capital plan, the financial risks that kill companies like this | Selling, to an investor. Never to act on |
| [`roadmap/company/hiring-and-org-growth.md`](roadmap/company/hiring-and-org-growth.md) | Why the first hire is not an engineer, a dated headcount plan, first-90-days, interview design | Selling, to a candidate; day one, as a new hire |

---

## Which document do I trust for what

There are three overlapping sources of truth in this repository and confusing
them is expensive, because they are written in the same confident voice and they
disagree.

| Question | Trust this | And not this |
|---|---|---|
| Is it built? Does it work? | [`status.md`](status.md) | Everything else |
| Is it planned? When? Under what gate? | [`roadmap/master-roadmap.md`](roadmap/master-roadmap.md) | `status.md`, which reports rather than plans |
| Why is it built this way? What is the risk? | [`threat-model.md`](threat-model.md) | The plan, which schedules the work but does not decide the security posture |
| What are the rules? | [`../AGENTS.md`](../AGENTS.md) | Any document that restates them |
| Where does the code live? | [`../internal/README.md`](../internal/README.md) | — |

Stated plainly, because both are commonly cited as if they were:

- **The root `README.md` is not a source of truth for build state.** It says so
  itself at line 63: "`docs/status.md` is the source of truth for what is built
  and what is not — not this file."
- **The commit history is not a source of truth for build state.** A commit says
  what someone did on a date, not what a reviewer has verified. A green `make
  test-race` says the third thing.
- **A stale claim in a non-authoritative document is a defect worth
  reporting.** It is not a reason to distrust the document's whole subject area,
  and it is not something to fix silently in a file you do not own. Say it,
  cite the file and line, and let the owner decide.

`docs/status.md` is authoritative *and* currently stale in two places — it
claims the suite is green across 24 packages (the tree has 30 leaf packages) and
that the toolchain is Go 1.27 (`go.mod` says 1.26.0, CI pins 1.26). That is the
rule above applied to the rule's own document, and it is the reason to check a
number before repeating it rather than the reason to stop reading the file.

---

## Documentation rules

These are the rules `docs/roadmap/README.md` already states and that the rest of
the directory obeys. They are restated here because this index is where people
arrive.

1. **Every document carries a last-revised date.** A document without one is
   stale and should not be cited. All twenty-one roadmap documents carry one as
   of 2026-09-28. *Known exception:* `status.md` and `threat-model.md` do not
   carry one today, and they are the two most-cited documents in the repository.
   Treat them as undated rather than as fresh, and prefer a claim corroborated
   by the tree.
2. **Numbers carry their provenance.** `PROPOSED`, `ESTIMATE`, `ASSUMPTION` and
   `NOT YET KNOWN` are load-bearing. A bare number in a roadmap document is
   either measured — and the document says where — or it is a defect. Measured
   numbers belong to the code or to a recorded run, not to prose.
3. **Cross-references are relative paths.** A document can be moved without
   breaking its links, and every link in `docs/roadmap/` is verified to resolve.
4. **A claim that cannot be sourced is marked unverified rather than asserted.**
   Market claims, benchmark numbers and vendor comparisons in the source
   material were never verified; they carry a source and a date, and they are
   not asserted in the product, the docs or the code.
5. **Scope lives in the milestone plan.** A new capability has to be justified
   as a platform primitive, not added ad hoc.
6. **One spine.** A track document that contradicts
   `strategy-and-north-star.md` or `master-roadmap.md` is wrong, and the fix is
   to the track document.

---

## Documents that do not exist yet

Listed so nobody hunts for them.

| Missing | Where it actually is |
|---|---|
| An architecture document | [`../internal/README.md`](../internal/README.md) — the package map, dependency rules, three seams, reading order. There is no separate architecture document and none is planned |
| An operator runbook | Nothing. The 02:00 operations playbook is specified inside [`roadmap/engineering/release-sre-and-support.md`](roadmap/engineering/release-sre-and-support.md) and has not been written as a standalone document |
| An ADR directory | No `docs/adr/`. The ADR *process* is in [`roadmap/engineering/decisions-and-process.md`](roadmap/engineering/decisions-and-process.md), and it seeds a settled-decisions index from the code. Record a decision by following that process, not by inventing a file |
| A fixtures document | `.gitignore` references `docs/fixtures.md` and the file does not exist. The fixture format and the sanitization rules are documented in the package doc comments of `internal/fixture` |
| A CI or infrastructure document | No such document. `.github/workflows/ci.yml` is the artifact |
| An `internal/policy` package | Not a document and not a package. `policy.proto` and a `0005_ai_and_policy` migration exist; the Go package does not, and is an M3 dependency with no owner in the tree |
