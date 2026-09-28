# Hiring and Org Growth

Owner: E1 (accountable), E2 (technical interview). Status: living. Last revised
2026-09-28.

This is the hiring plan for a company whose two founders are also product,
architect, security engineer and salesperson. Read `../strategy-and-north-star.md`
§6 (the capacity law — the constraint this document exists to obey),
`../master-roadmap.md` (the gates hires must unblock), `../engineering/team-and-operating-model.md`
(the roles, the agent roster and the weekly rhythm this plan must not break),
`financial-plan.md` (the cash this spends), and `../engineering/quality-engineering.md` §11
(QA capacity reality) first.

Every figure is **ESTIMATE** unless it is a salary band or a date that another
roadmap document already fixes. Where this document disagrees with a sibling,
it says so rather than quietly taking the other view.

---

## 1. The first constraint, and the proof that the first hire is not an engineer

### 1.1 The capacity law

`../strategy-and-north-star.md` §6, quoted because the rest of this document is
downstream of it:

> 2 engineers, 13 weeks per quarter, ~60% of that on new build. The remaining
> 40% is the tax that quietly kills two-person companies: support, demos, sales
> calls, incident response, hiring, admin, and the AI review pass. No milestone in
> this roadmap plans above that line.

Two consequences that are easy to get wrong:

- **The 40% is not slack.** It is a named, itemised tax. A plan that adds a
  third stream of work is not "using the capacity", it is spending money that
  does not exist.
- **Sequential, not parallel, at small scale.** "The roadmap therefore keeps
  exactly one milestone active plus one parallel track that is never itself on
  the critical path."

### 1.2 The binding constraint today is E1's calendar, not E2's throughput

`../engineering/team-and-operating-model.md` §1.1 states the split (E1: Product, Architecture,
Security, Sales; E2: Backend, AI, Data, Release) and, in the same table, names
the bottleneck: E1 is the bottleneck for **site access — "the only critical-path
item with no internal lever."**

That is the whole proof, and it is worth unpacking because it is the opposite of
the intuitive answer. M1's exit gate has four lines
(`../master-roadmap.md` §M1):

| Gate line | Whose calendar does it need? |
|---|---|
| 1 — live XProtect integration and replay parity | E1 (a real site, credentials, a working installation) **and** E2 (the parity differ) |
| 2 — one incident class end to end, a real operator, timed | E1 (the operator, the site, the call) **and** E2 |
| 3 — every SLO met under a recorded load run | E2 |
| 4 — a production VLM profile in shadow for 14 days | E2, plus a design partner who will host a GPU |

**Three of four need a customer, and a customer relationship is a person.** A
third engineer does not shorten any of them. E2 adding a third engineer
accelerates gates 3 and 4 by a fraction and does not touch 1 or 2 at all.

### 1.3 The four candidates, argued

| | **QA / SDET** | **Solutions Engineer** | **Product Designer** | **Senior Backend / AI** |
|---|---|---|---|---|
| **The bottleneck it removes** | None today. `../engineering/quality-engineering.md` §11 says two people can test what exists, and `internal/observability` — the actual DoD gap for gate 3 — is a *wiring* task E2 can do in days | **E1's calendar.** Converts "we have XProtect" into a mapped site configuration, a rule bundle, and a measurement, and runs the install | None today. `../engineering/ux-and-design-system.md` §10 is a findings list against an M1 console E1 can iterate on with real operators | **E2's throughput.** The most code per hire, and the least scarce resource |
| **The signal that would justify it** | Two consecutive milestone gates signed without a failing test that should have caught a defect. `../engineering/quality-engineering.md` §11.4's open DoD-blocking defects growing rather than shrinking | A design partner asking for a second site, or E1 spending > 8 h/week on deployment questions (the figure `gtm-and-sales.md` §8.2 uses) | An operator completing the M1 workflow unaided in under N minutes *and* the console being the reason they could not | E2's queue > 3 weeks deep for two consecutive months, with the backlog item being genuinely parallelisable work |
| **Whether the signal exists today** | **No.** `docs/status.md` records 24 packages green with 30 test files and 21 Python contract tests. The gap is instrumentation, not test authorship | **Partly.** Three design partners are the plan; each install is E1's calendar for a day or more, and every one lands in a release week | **No, but it will.** The exception queue UX (`../engineering/ux-and-design-system.md` §3.3) is M7's primary surface and cannot be authored by the engineer writing the backend | **No.** E2 is not behind. `../milestones/m1-trust-spine-execution-plan.md` §4.1's critical path is gated on customer access and on F1/F2/F4 in `../engineering/ai-and-data.md` §1.5 — none of which a third engineer shortens |
| **Cost (ESTIMATE, fully loaded)** | $95k–115k/yr | $95k–125k/yr | $95k–130k/yr | $130k–165k/yr |
| **When it becomes urgent** | M2's exit gate (2027-09-30) — a second and third incident class multiply the matrix | **M1's last quarter, 2027-01** | M3 (2027-10 → 2028-03), when the copilot surface ships | M4 (2028-04), when the second adapter and the SDK start |
| **The counter-argument for not hiring it yet** | Hiring a tester before the pipeline is wired means testing a system that emits no metrics. `../engineering/release-sre-and-support.md` §5.2: every metric is currently *named but never emitted* because `internal/observability` has no production call site. The tester would be measuring nothing | The road to an SE hire passes through an M1 gate that a third engineer does not unblock, and a hire that lands mid-milestone is a slip, not a rescue (`../engineering/team-and-operating-model.md` R-13) | M1's console has one workflow. Designing a second surface before the first has been used by a real operator is designing a guess | It is the most expensive hire on the list and the one whose output is furthest from the customer. A senior backend hire in a two-person company also raises the *floor*: you cannot let them go, which is §8's constraint |
| **Verdict** | **H3, 2027-11-01** | **H1, 2027-07-01** | **H4, 2028-01-15** | **H5, 2028-04-01** |

### 1.4 The conclusion, and the reconciliation this document forces

**The first hire is a Solutions Engineer, and the reason is that the M1 gate
blocker is a customer relationship.** `../engineering/team-and-operating-model.md` §8's last
row already reaches this conclusion for R-12: "A hire, and the first hire."

**A divergence this document must name.** `financial-plan.md` §1.2 currently
plans **Hire 1 = Deployment & Support, 2027-07-01, $95,000/yr** and **Hire 2 =
Solutions Engineer, 2028-01-01, $110,000/yr**. That is a different sequence
from this one, for the same date.

The two are reconcilable and the reconciliation is better than either: at 2027-07
there is one site configuration being deployed, three design partners, and a
support inbox with one customer in it. **That is one person who deploys,
configures, measures and answers — a Solutions Engineer with deployment as the
first half of the job.** Separating "Deployment & Support" from "Solutions
Engineer" as two sequential hires creates a person who can install the product
and cannot design the rule bundle, followed six months later by a person who can
design the rule bundle and has never installed the product.

**Recommendation: H1 = Solutions Engineer (deployment, configuration,
measurement, support), 2027-07-01, at the $95k–125k band.** This is a change to
`financial-plan.md` §1.2 and §2.1's payroll line, and its owner should make it.
Until they do, two documents disagree and the cash model is the one that has to
change.

---

## 2. The staged headcount plan

**Rule from `../engineering/team-and-operating-model.md` R-13: hiring is planned as capacity,
never as rescue.** A hire that arrives during a milestone is a slip, not a
rescue. So every date below is placed at a **milestone boundary or just after
one**, never in the middle of a milestone's most active quarter. The exception
is H1, which lands in M2's first quarter and is justified in §1.4.

### 2.1 H1 — Solutions Engineer · 2027-07-01 · M2

| | |
|---|---|
| **Why now** | M2 opens 2027-04-01. Its first deliverable is the evidence engine, whose adapter half is blocked (see `../engineering/integrations-and-adapters.md` §4.3). M2's exit gate is a **measured** MTTI reduction "per operator against their own VMS-only baseline", and `../engineering/metrics-and-analytics.md` §4 owns that study's design — a study nobody runs is not a study. M2's commercial state is a paid pilot: "First external customer who is not personally known" |
| **The bottleneck** | E1's calendar. A founder cannot be in a customer call during a release week, and `gtm-and-sales.md` §8.1 already itemises the 40% tax as including 8 h/week of E1's time on the M1 design-partner motion — rising in M2 |
| **Cost band (ESTIMATE)** | $95,000–125,000 fully loaded. **$9,500/month at the midpoint of $114k.** Loaded multiplier ~1.25–1.30 on base (`financial-plan.md` §1.2) |
| **Seniority** | Mid. Wants to carry a deployment *and* sit in a design conversation. A senior SE will not do installs; a junior SE will not design a rule bundle |
| **What they are judged by (90 days)** | `../engineering/team-and-operating-model.md` R-12's metric: "Number of sites where the customer produced their own before/after number. If we have to compute it, they did not feel the value" |
| **What they must not do** | Decide scope. `../engineering/team-and-operating-model.md` §2.1 rule 1: "AI never decides scope" — the same discipline applies to a new hire who is good at customer work and will start saying yes |

### 2.2 H2 — Applied AI / ML Engineer · 2027-10-01 · M2/M3 boundary

| | |
|---|---|
| **Why now** | The AI plane is **not connected to the core** (`../engineering/ai-and-data.md` §1.5, F1: no Go client, no `vlm` worker, no binary enqueues it). That is four concrete build items and it is a hard dependency of M1 gate 4 — which means it is arguably an M1 item, and its slip date is why the hire cannot come earlier than the M1 boundary. At 2027-10 it also arrives before M2's ten-profile programme and before M3's playbook runner, which is the last point at which one person can own the whole AI plane without inheriting someone else's design |
| **The bottleneck** | E2's calendar is not the bottleneck; **E2's lack of a second pair of hands on a domain that is 80% of the remaining risk** is |
| **Cost band (ESTIMATE)** | $120,000–160,000 fully loaded. **$11,700/month at the midpoint of $140k** |
| **Seniority** | Mid-to-senior. This is not an ML-research hire — the hard part is attribution, evaluation discipline and the promotion decision (`../engineering/team-and-operating-model.md` R-05), and someone who wants to tune architectures will be frustrated |
| **What they are judged by (90 days)** | The F1 client shipped, a degraded observation recorded when the runtime is stopped, and a profile gate report that prints its denominators. **Not** "a model runs" |
| **The trap** | Hiring an ML person to make a model good is the wrong hire. `../engineering/ai-and-data.md` §12's asymmetry is the whole argument: false negatives are the severe failure, and the product's answer is governance, not accuracy |

### 2.3 H3 — QA / SDET · 2027-11-01 · M3

| | |
|---|---|
| **Why now** | M3's exit gate requires "Every automated action is idempotent under replay, **proven by a fault-injection test at the action stage**" and M3 is the first milestone with an action surface at all. The M2–M3 boundary is also where the number of test surfaces goes from one workflow to several, and `../engineering/quality-engineering.md` §11.1 says what two people can and cannot do |
| **The bottleneck** | Two people cannot author a fault-injection matrix for a system that has an action dispatcher, a playbook engine and an AI plane. The matrix is the deliverable and it is finite |
| **Cost band (ESTIMATE)** | $100,000–130,000 fully loaded. **$9,600/month at the midpoint of $115k** |
| **Seniority** | Mid. A senior SDET at this stage designs a test strategy for a system whose third milestone has not shipped, which is a plan nobody asked for |
| **What they are judged by (90 days)** | `../engineering/quality-engineering.md`'s invariant→test matrix has no row that is claimed but untested, and the `kill -9` stage table is complete for every stage the system actually has |
| **The trap** | Hiring a tester before `internal/observability` is wired. **Sequencing rule: observability wiring lands in M1, before this hire.** Otherwise the SDET spends their first quarter reading code |

### 2.4 H4 — Product Designer · 2028-01-15 · M3

| | |
|---|---|
| **Why now** | M3 is the copilot milestone and the first one where the console is not a single disposition workflow. The specific reason this is a hire and not a founder task is in the brief and it is correct: **the exception queue UX cannot be shipped by the engineer who authors the backend.** `../engineering/ux-and-design-system.md` §3.3's flow C (exception queue triage) is the product's destination UX and it is a design problem — density, triage order, the degraded state, the evidence affordance — not a schema problem |
| **The bottleneck** | E1 already owns the console (`../engineering/team-and-operating-model.md` R-03) and E1 is also product, architecture, security and sales. In M3, E1 is running GA |
| **Cost band (ESTIMATE)** | $100,000–135,000 fully loaded. **$9,800/month at the midpoint of $118k** |
| **Seniority** | Mid. Needs to sit in shift shadowing (`../engineering/product-and-backlog.md` §5.2, twice a quarter from M2) and be trusted by operators, not present a design system to a board |
| **What they are judged by (90 days)** | Time for a real operator to complete the M3 copilot workflow unaided, measured once, from `../engineering/ux-and-design-system.md`'s Flow B/C. One number, one person |
| **The trap** | Hiring a designer who wants to rebuild the console. The constraint at `../engineering/ux-and-design-system.md` §4.2 decides what components are possible, and the operator's context is a 24/7 desk, not a browser |

### 2.5 H5 — Senior Backend / Durability Engineer · 2028-04-01 · M4

| | |
|---|---|
| **Why now** | M4 is the second-adapter milestone. The adapter template in `../engineering/integrations-and-adapters.md` §6.2 is **8–12 weeks of one person's uninterrupted work** and it is the milestone's gate. Running it in parallel with the ONVIF programme, the SDK and the M3 residual is not a plan |
| **The bottleneck** | E2 alone cannot be the Milestone owner, the second-adapter owner, the SDK owner and the ONVIF owner |
| **Cost band (ESTIMATE)** | $135,000–170,000 fully loaded. **$12,700/month at the midpoint of $152k** |
| **Seniority** | Senior. The second adapter is the test of whether the abstraction holds, and it needs someone who will push back on the seam rather than route around it |
| **What they are judged by (90 days)** | `make non-adapter-diff` runs and produces a report. A diff that reveals a core default that was wrong is the single most valuable output this hire can produce |
| **The trap** | This is the most expensive hire so far and the one whose loss would hurt most (§8.1). It is also the first hire where the "we cannot afford to lose them" constraint becomes real |

### 2.6 H6 — Enterprise Solutions Engineer (OEM channel) · 2028-06-01 · M4

| | |
|---|---|
| **Why now** | `gtm-and-sales.md` §7.3 puts the realistic OEM cycle at 15–30 months, and §7.4 argues the SDK must exist *before* the conversation. So the first OEM conversation is 2028-06 at the earliest, and it needs a person who can demo a conformance suite to an integrator |
| **The bottleneck** | E1's calendar, permanently. `financial-plan.md` §5.2: "Channel, continuously ... consuming E1's calendar the whole time, with no revenue until the end. This is a *person*, not a project, because a relationship is a person" |
| **Cost band (ESTIMATE)** | $105,000–140,000 fully loaded. **$10,200/month at the midpoint of $122k** |
| **Seniority** | Mid-to-senior with integrator or VMS background. A channel person who has never installed a VMS will be selling a demo, not a platform |
| **What they are judged by (90 days)** | One integrator workshop delivered and one written follow-up they asked for |
| **Divergence to record** | `financial-plan.md` §5.3 recommends cutting M4 to the second adapter + SDK and using the partner's engineers for Profile M, access control and sensors as paid channel work. **This plan adopts that** (H5 + H6 only, no ONVIF or access-control hires in M4) and says so explicitly rather than quietly hiring for a scope that will be cut |

### 2.7 H7 — AppSec · 2028-07-01 · M4

| | |
|---|---|
| **Why now** | M4 is when a customer's security reviewer first reads the control-to-test map. `../security/trust-and-security-program.md` §2.3 lists the CI gaps (no `permissions:` block, floating action tags, no SBOM generation, no govulncheck, `.golangci.yml` disabling gosec) and §2.4 is the spend. At 2 people E1 is accountable and E2 implements, and both have day jobs |
| **The bottleneck** | Neither founder can do it well, and it is the one function where "good enough" is a liability in a regulated sale |
| **Cost band (ESTIMATE)** | $130,000–170,000 fully loaded. **$12,500/month at the midpoint of $150k** |
| **Alternative, and it should be considered first** | `financial-plan.md` §5.4: "Security and compliance — Fractional consultant at M4, 0.5 FTE at M5. Episodic at first; standing by M5." **Recommendation: a fractional security consultant for 2028 H1, and the hire only if the enterprise pipeline justifies it.** See §4 |
| **What they are judged by (90 days)** | `../security/trust-and-security-program.md` §9.1's matrix has a named test and a cadence for every control, and the CI gaps are closed with pinned SHAs and a generated SBOM |

### 2.8 H8/H9/H10 — SRE, Support/on-call, and the M5 shape · 2029-01-04

M5 is the discontinuity. `../master-roadmap.md` §M5's exit gate needs a three-site
customer with the control plane pulled mid-incident, HA failover under load with
measured RTO/RPO, and OIDC/SCIM against two real identity providers. None of that
is a two-person activity.

| Hire | Date | Band (ESTIMATE) | Why this date |
|---|---|---|---|
| **H8 — SRE** | 2029-01-04 | $120,000–160,000 fully loaded; **$11,700/mo** | M5's HA and DR are the gate. `../engineering/release-sre-and-support.md` §6 is a whole document and nobody owns it |
| **H9 — Support engineer #1** | 2029-01-04 | $70,000–95,000 fully loaded; **$6,900/mo** | The 24/7 rota needs **four** people (`financial-plan.md` §5.4). #1 is not a rota; it is the start of one, and the honest statement is that **we do not sell 24/7 until a rota of ≥4 exists** (`financial-plan.md` §1.3) |
| **H10 — Support engineer #2** | 2029-04-01 | $70,000–95,000; **$6,900/mo** | Rota #2 |
| **H11 — Solutions/CS #2** | 2029-04-01 | $95,000–125,000; **$9,200/mo** | Third site and beyond; `financial-plan.md` §5.2's 50-hour acquisition cost per site has to come down |
| **H12 — Security/compliance, 0.5 → 1.0 FTE** | 2029-01-04 | $110,000–150,000 pro-rated; **$5,400/mo** at 0.5 | `financial-plan.md` §5.4: the 0.5 FTE becomes an employee at M5 |
| **H13/H14 — Support #3 and #4** | 2029-07-01, 2029-10-01 | $70,000–95,000 each; **$6,900/mo** each | **This is the date 24/7 becomes sellable**, and it is the only honest one |
| **H15 — Second backend** | 2029-04-01 | $135,000–170,000; **$12,700/mo** | `financial-plan.md` §5.3's M5 engineering count is 6 including the founders |

### 2.9 M6–M8 shape and the honest totals

`../master-roadmap.md`'s M6 (2029-10 → 2030-06) is Site Intelligence, M7
(2030-07 → 2031-06) is Autonomous Security Operations, M8 (2031-07 → 2032-12) is
the platform and marketplace.

| Milestone | Engineering | Support/SRE | Solutions/CS | Channel | Security | Product | **Total** |
|---|---|---|---|---|---|---|---|
| **Today (M1)** | 2 | — | — | — | — | folded into E1 | **2** |
| **M3 (2028-03)** | 4 (E1, E2, H1, H2) | 0.5 (H1) | 1 (H1) | — | 0.25 (contract) | 1 (H4) | **7** |
| **M4 (2028-12)** | 5 | 1 (H1) | 1 (H1) | 1 (H6) | 0.25→1 (H7) | 1 | **10** |
| **M5 (2029-09)** | 7 | 5 (H8 + 4 support) | 2 | 1 | 1 (H12) | 1 | **17** |
| **M6 (2030-06)** | 9 | 5 | 3 | 1 | 1 | 2 | **21** |
| **M7 (2031-06)** | 11 | 6 | 3 | 2 | 1 | 2 | **25** |
| **M8 (2032-12)** | 13 | 6 | 4 | 3 | 2 | 2 | **30** |

**Two things this table is honest about.**

First, it disagrees with `financial-plan.md` §5.3, which reaches 11 at M4 and
23 at M8. This plan is 10 at M4 and 30 at M8. The M4 gap is the same
recommendation taken up — M4 is the second adapter plus the SDK, not the full
scope — and the M8 gap is that a marketplace is a supply-and-demand business and
`../master-roadmap.md` §M8's own gate ("one real third party beats fifty
unreviewed uploads") is a curation function, not a headcount function. **Where
the two documents disagree, the cash model wins** and `financial-plan.md` §5.3
should be reconciled to this table or the other way round, by its owner.

Second, **the 24/7 support promise is not a headcount line, it is a gate.** The
table shows 5 support/SRE at M5 and that is *four* support engineers plus one
SRE, arriving between 2029-01 and 2029-10. Until H13 and H14 land, 24/7 is not
sold. That is not a budget decision, it is a contract decision, and it belongs
in `pricing-and-packaging.md` and in the sales collateral.

### 2.10 The M6–M8 shapes, named

| Role | Milestone | Why it arrives then | Earliest date |
|---|---|---|---|
| **Applied scientist** (baselines, drift detection, seasonality) | M6 | Site Intelligence's statistical core is the hardest *research* in the plan and it is not ML — it is time-series with a false-anomaly problem. `../engineering/ai-and-data.md` §8.3 | 2029-10-01 |
| **Product manager** | M6/M7 | The exception queue has one surface and one primary persona until M7, at which point the site is choosing an autonomy level and the product is selling a posture. E1 cannot hold product *and* architecture *and* security *and* sales at that point | 2029-10-01 |
| **Customer success** | M6 | Site baselines and memory are per-site assets; onboarding them is a service, and `../strategy-and-north-star.md` §5's compounding claim is checked at M6's gate | 2029-10-01 |
| **Legal/compliance counsel** (fractional → 0.5) | M5/M6 | NIS2, HIPAA, the EU AI Act's high-risk classification, and the data-residency questions `../security/compliance-and-certifications.md` §4.4 puts four open decision dates on. `financial-plan.md` §5.4: at under $5M revenue outside counsel, not a full-time hire | 2029-01-04, fractional |
| **Support/on-call lead** | M7 | At A4 an unattended site is an unattended building. The escalation path must have a name (`../engineering/release-sre-and-support.md` §7.4) | 2030-07-01 |
| **Marketplace/ecosystem manager** | M8 | Third-party adapters, rule packs, model bundles and vertical packs, all signed and all installed offline. The work is curation and trust, and it is the same work as the channel | 2031-07-01 |

---

## 3. First-90-day plans for the three hires that matter most

### 3.1 H1 — Solutions Engineer

**Week 1 — read, in this order, and do not skip ahead.**

| # | File | What they are looking for |
|---|---|---|
| 1 | `AGENTS.md` | The five invariants. This is the document that decides every argument they will have in their first month |
| 2 | `README.md` | The one-line workflow: "XProtect event → canonical event → Security Graph → deterministic verification → VLM observation → incident → operator disposition → audit". If they cannot recite it by Friday, week 1 failed |
| 3 | `docs/status.md` | What is built and what is not. Specifically: **"The Milestone endpoint surface is unverified"** and "There is no command in the tree refuses as unimplemented" |
| 4 | `docs/roadmap/README.md`, then `../strategy-and-north-star.md` | The segment order and the three buyer questions. They will be in customer meetings; this is what they are selling |
| 5 | `../master-roadmap.md` §M1 and §M2 only | The two milestones they will work inside. Do not read further; it is not load-bearing for them and it is 420 lines |
| 6 | `contracts/protobuf/security/v1/event.proto`, `incident.proto`, `observation.proto` | The wire. Three files, and they explain most of the vocabulary |
| 7 | `internal/milestone/routes.go` | **The route table and its `Unverified` flags.** They should be able to say, in week 1, that every endpoint in the product is an assumption and that `securityctl adapter probe` does not exist yet |
| 8 | `internal/milestone/adapter.go` | `RawRecord` is the adapter's only output type. They need to know what a source event becomes |
| 9 | `rules/builtin/bundle.yaml` | The shipped rules. A customer's rule bundle is what they will write, and this is the reference |
| 10 | `internal/api/handlers.go` + `contracts/openapi/v1/openapi.yaml` | The surface the operator actually sees |
| 11 | `docs/threat-model.md`, plus the operator runbook (not yet written) | What exists to hand to a customer |

**Deliverable, week 1:** a written one-page site-deployment checklist that does
not exist today, listing every command they would run on a new site in order.
They will find the gaps by writing it. That is the point.

**Week 2 — shadow E1 on a live install.** Run `securityctl preflight`, `doctor`,
`migrate up`, `status` against a real (or staging) database. Read
`cmd/securityctl/operator.go` and `lifecycle.go` while doing it. Then read
`../engineering/release-sre-and-support.md` §2 (deployment topology) and §7 (support) in full —
it is the document their job is.

**Deliverable, week 2:** deploy the product to one site end to end, timed, and
write down every point where you needed a human who was not you.

**Week 4 — own one site.** Take primary for one design partner: their site
configuration, their rule bundle, their before/after measurement. Pair with E1 on
the customer call; E1 talks, they configure.

**Deliverable, week 4:** one site running with a site-specific bundle that is
not `rules/builtin`, and a written note on what had to be changed and why —
which is the first real input to M2's reduced-scope decision.

**Week 8 — the MTTI baseline.** `../engineering/metrics-and-analytics.md` §4 owns the study
design. Their deliverable is the *baseline arm*: the customer's own VMS-only
investigation time, collected before the product is in the loop, per class, per
operator. Without it M2's exit gate cannot be signed and it cannot be signed
retroactively.

**Week 12 — the first repeatable deployment.** Same steps, a second site, no E1
hands-on. The measurement in §2.1 is whether the *customer* produced their own
before/after number. If E1 computed it, the hire has not landed.

### 3.2 H3 — QA / SDET

**Week 1.** `AGENTS.md` §"Invariants" and §"Testing" (the whole thing is 82
lines and both of them are about their job), then `../engineering/quality-engineering.md` in
full — it is their operating manual, and §11.4's open DoD-blocking defects are
their inbox. Then `Makefile` (94 lines) and `.github/workflows/ci.yml`, and then
run the suite:

```sh
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
export SP_TEST_DATABASE_PASSWORD=postgres
go run ./cmd/security-migrate up
go test -p 1 -race -count=1 ./...
cd deploy/ai-runtime && py -m unittest discover
```

**They must be able to explain `-p 1` by the end of week 1** — the Makefile says
it: "the database-backed suites share one database and rebuild the schema, so
package test binaries must not run concurrently. Without it, one suite's DROP
SCHEMA lands in the middle of another's test and produces failures that look like
product bugs." That sentence is a test of whether they read carefully.

**Deliverable, week 1:** reproduce the verification in `docs/status.md` §
"Reproducing the verification" from a clean machine, and report anything that
does not reproduce. On Windows, note that `TestSymlinkEscapeIsRefused` skips —
one of the four path-traversal negatives does not run on the platform the team
uses. That is their first ticket.

**Week 2 — the invariant matrix.** `../engineering/quality-engineering.md` §3 is the
invariant→test matrix. For each of the five invariants, find the test that fails
if the invariant is broken, and write the test if it does not exist. Invariant 3
has two missing integration-level tests (see `../engineering/ai-and-data.md` §2.4).

**Week 4 — the fault-injection stage table.** `../engineering/quality-engineering.md` §5.1
lists the stages. For every stage the system *currently has*, there is a `kill -9`
test or there is a ticket. `docs/status.md`'s honest statement is that a
durability change needs one; the question is whether the existing stages all
have them.

**Week 8 — the M3 action-stage test.** M3's gate is "every automated action is
idempotent under replay, proven by a fault-injection test at the action stage".
The action surface does not exist yet, so week 8's deliverable is the *test
harness* plus the specification of what the action stage must assert, reviewed
with whoever builds it.

**Week 12 — the release quality gate.** `../engineering/quality-engineering.md` §9.1's ordered
checklist, run against a real release candidate, end to end, with the evidence
attached. A gate signed with evidence only the founders can interpret will be
re-litigated at M2 when it becomes commercial (`../engineering/team-and-operating-model.md`
R-10).

**The trap to name for them:** the most valuable thing they can do in their
first quarter is close the *instrumentation* gap, not add tests.
`../engineering/release-sre-and-support.md` §5.2 — `internal/observability` is imported by
exactly one file, its own test, so every metric name is currently named but never
emitted. A test suite over a system that emits nothing cannot verify an SLO, and
M1 gate 3 is an SLO gate.

### 3.3 H4 — Product Designer

**Week 1.** `AGENTS.md` (82 lines; the "Hebrew and RTL" section is relevant to
them because it is a product decision they must not undo), then
`../engineering/ux-and-design-system.md` in full — it is their design system, its constraints
and its known findings, and §1 "The operator's actual context" is the brief.
Then read the console: `internal/api/console.go` and the templates, running
against a live database.

**The three constraints they must internalise in week 1:**
- The operator is on a 24/7 desk with a 3-second attention budget, not a browser
  (`../engineering/ux-and-design-system.md` §1).
- Colour is never the only channel (§4.3), because the desk runs on bad
  monitors in bad light.
- "AI unavailable" is a first-class design state with a copy pattern already
  written (§5), not an error case.

**Week 2 — shift shadowing.** `../engineering/product-and-backlog.md` §5.2: twice a quarter
from M2. Their first one is the most valuable week they will have. They will
learn that the operator's actual triage order is not the one in the IA.

**Week 4 — one flow, end to end, redrawn.** Take M3's copilot flow
(`../engineering/ux-and-design-system.md` §3.2, M2 investigation) and produce a design that a
real operator would accept, tested with 5–8 unmoderated operators per §8.1. Their
first deliverable is *not* a component library.

**Week 8 — the degraded and exception states.** The M3 reduced-scope line is
"the exception queue UX before autonomous resolution — if we ship autonomy with a
worse surface than the alert list we replaced, operators will route around us"
(`../master-roadmap.md` §M3). Design both the degraded state and the exception queue
and show them side by side, with the numbers, to E1.

**Week 12 — Flow C.** The M7 exception queue is the product's destination UX and
it is the design artefact M7's exit gate will be judged on. It is a design
problem eight months before M7 and that is deliberate.

**How they are evaluated:** one number from one real person — time to complete
the M3 workflow unaided — plus the unmoderated round results, plus whether a
designer who has never met an operator can read the console and predict the
operator's next action.

---

## 4. The fractional and contract route

### 4.1 The test

From `financial-plan.md` §5.4, and the rule behind it is the one worth
restating: **anything a customer is given a name for is an employee.** A
contractor roster is not a name.

### 4.2 What is contracted, and when

| Work | Route | Earliest date | Cost (ESTIMATE) | Why contract rather than hire |
|---|---|---|---|---|
| **SOC 2 readiness consulting** | **Fractional, 0.1–0.2 FTE** | 2027-04 | $3k–6k/quarter | `financial-plan.md` §5.4: episodic, deliverable-shaped, cheap per project. And the honest position, which is the important part: **we do not claim SOC 2 and we do not intend to; we publish the control-to-test map instead** — which is what a security reviewer actually wants and costs a fraction of an audit |
| **Penetration test** | **Contract, annually** | 2027-06, then annually | $12k–25k per engagement | "It must be able to tell us things we do not want to hear, and the report is worthless if the tester is our colleague." Independence is the property being bought |
| **Threat-model review** | **Contract, annually** | 2027-06 | $4k–8k | A second pair of eyes on the document that says our controls are adequate |
| **Accessibility audit** | Contract, per release cycle | 2027-10 | $3k–6k | Episodic; `../engineering/ux-and-design-system.md` §7's 24/7 operator accessibility is real |
| **Legal templates** (design-partner agreement, DPA, MSA) | **Outside counsel** | 2026-12, 2027-04, 2027-09 | $4,000 / $1,500 / $5,000 (from `financial-plan.md` §1.2) | A one-off review per document. Hiring a lawyer at this revenue is a mistake |
| **Financial / fractional CFO** | Fractional | 2027-07 | $2k–4k/month | `financial-plan.md` §5.4: "At under $5M of revenue the full-time versions do not pay for themselves" |
| **Brand and visual identity** | **Contract** | 2027-10 | $8k–20k | `../engineering/ux-and-design-system.md` §9.5's rule — no scraped imagery, licensing and provenance documented. Episodic and deliverable-shaped |
| **ONVIF device interop testing** | **Contract** | 2028 | $10k–30k per campaign | "Access to hardware is the thing being bought." We do not own a device lab |
| **Load testing** | Contract, then in-house from M3 | 2028-01 | $8k–15k per campaign | Same |
| **The Adapter SDK certification run** | **Contract — and it MUST be external** | 2028-12 | $15k–30k | `../master-roadmap.md` §M4's gate requires "someone outside the company". A contractor is the cheapest way to satisfy that honestly |
| **The model runtime** | **See §4.4 — this is the interesting one** | | | |

### 4.3 The management cost at two people, stated honestly

Every contract has a cost that is not the invoice, and at two people it is the
binding one.

| Hidden cost | How much | Who pays |
|---|---|---|
| **Briefing** | 2–6 h per engagement to explain a system with five invariants, no onboarded users and a codebase the contractor has never seen | E1 or E2, out of the 40% tax |
| **Review** | The output must be read carefully enough to know whether it is right. A threat-model review that is accepted unread is worse than none, because it creates the appearance of a second opinion | E1 |
| **Context re-acquisition** | Every engagement starts from zero. There is no institutional memory on the vendor's side | Both |
| **Availability** | A contractor with a full calendar is not a same-day responder. For a Sev-1 that matters; for a pen test it does not | — |
| **The one that bites** | **A contractor who learns something nobody internal knows and leaves.** At two people the founder who ran the design-partner integration *is* the documentation | E1, irreversibly |

**Rule: a contractor's output is only worth its cost if we can absorb it into an
artefact we keep.** Every engagement above must produce a file in the repo or a
document in `docs/`, not a PDF in an inbox. If it cannot, contract it for the
*knowledge* and record the knowledge ourselves within a week.

### 4.4 The model runtime — the one that should NOT simply be contracted

The natural instinct is to contract "the VLM runtime" to an ML engineer. **Do
not, and the reason is the product's own thesis.**

The M2/M3/M7 local LLM is not an implementation detail. `../engineering/ai-and-data.md` §4
describes it as the component that orchestrates and never asserts, whose every
claim binds to an evidence reference, and which must be structurally incapable of
being talked into an action by untrusted input (a rule bundle, a model bundle, an
operator note — `AGENTS.md`). **The safety property of this product lives in the
shape of the orchestrator's interface, and an interface is a thing we own.**

What *can* be contracted:

| Contractable | Not contractable |
|---|---|
| Building a `run_inference` body for one model and one profile class — the piece `docs/status.md` says is "the one remaining guard" | The `InferenceRequest`/`InferenceResult` contract and its version negotiation |
| A profile prompt and its first evaluation dataset | The promotion gate and the human promotion decision (`evaluation.Report.Promotable` is explicit that promotion is human) |
| GPU rental and a measured cost-per-evaluation | Any decision about what the model may be asked |
| Performance tuning of a specific model on specific hardware | The evidence-reference binding |

**The gate on this:** the contractor delivers a `run_inference` body and nothing
else, it is reviewed by a human against the contract, and the attribution,
validation, degradation and reason vocabulary — everything `docs/status.md` says
"is written and tested" — stays in code we own. If a contractor delivers a
runtime rather than a body, the engagement has failed even if the model works.

---

## 5. Interviewing

### 5.1 The loop

Five stages, 3.5 hours of candidate time total, and a written scorecard per
stage completed **before** the interview, not after. The reason: a scorecard
written afterwards is a justification.

| Stage | Who | Length | What it decides |
|---|---|---|---|
| **1 — Context** | E1 | 30 min | Whether they want this. The product is pre-revenue, two people, five invariants, and the interview is the honest version of that. Anyone who wants a mature team self-selects out here, which is the cheapest possible outcome |
| **2 — Work sample** | E1 + E2 | 90 min (take-home) | See §5.2. This is the stage that carries the signal |
| **3 — Deep dive on the sample** | E2 | 45 min | Whether they understand *why* their own code is shaped the way it is |
| **4 — Systems and judgment** | E1 | 45 min | See §5.3's per-role table |
| **5 — Reverse interview** | Candidate | 30 min | They ask us about the invariants, the capacity law, the reduced-scope clauses and the runway. A candidate who asks "what happens if M2 slips?" has understood the company. A candidate who asks "what's the tech stack?" has not |

**Feedback is written independently and then discussed.** A consensus vote across
five people in a room is how a mediocre hire gets in.

### 5.2 The work sample — a real small task, not a whiteboard

**Every role gets the same *shape*: a real artefact from this repo, scoped to a
few hours, with a real constraint and a real review.** The work sample is done
against the actual repository, with the actual `AGENTS.md`.

| Role | The task | The trap in it | What a good answer looks like |
|---|---|---|---|
| **Solutions Engineer** | Given `rules/builtin/bundle.yaml` and a fictional customer site description (60 cameras, 12 zones, one `restricted` zone with 2 cameras in it, after-hours schedule, one badge reader on the loading door), write the site's rule bundle and the site configuration | The natural answer copies `bundle.yaml` and changes `siteId`. The good answer notices that `site.default_locale` no longer exists (`docs/status.md` deviation 9), that `zone.pathPrefix: /restricted` must match the site's actual zone paths, and that `settings.clock.skewTolerance` is a site parameter | A bundle that lints (`go run ./cmd/securityctl rules lint --bundle <dir>`), plus a written note on which decisions are site parameters and which are product decisions |
| **QA / SDET** | Given `internal/normalize/normalize.go`'s `completeInbox`, write the test that fails if the lease-owner match is removed | The natural answer tests the happy path. The good answer notices that the whole point of the function is a race, and writes a test with two concurrent workers | A test that is deterministic, fails against a mutation, and does not need `-race` to be correct. **And** a note on why `-p 1` is required, which tests whether they read the Makefile |
| **Product Designer** | Given the M1 console running against a fixture, redesign the incident detail page for an operator who has 12 exceptions and 3 minutes | The natural answer makes it prettier. The good answer notices the degraded state, the evidence affordance and the "why am I seeing this" requirement, and does not add a filter to a screen an operator reads once per shift | A design that renders correctly with **zero** observations, one degraded observation and five normal ones — i.e. that treats the three absences as three states |
| **Senior Backend** | Given `internal/milestone/adapter.go`, find the bugs | There are at least three real ones, and a candidate who finds even one has demonstrated the right instinct. Candidates are given the file, not the answer, and are told it is real code | Naming any of: `KnownMilestoneEventTypes` keyed on vendor spellings that `normalizeEventType` can never return; `ReplayTransport`'s discarded epoch; `Poll` skipping non-paginated routes. **The scoring question is what they do next** — do they fix it, or ask whether the behaviour is intended? Both are good answers; writing a patch without asking is not |
| **Applied AI** | Given `evaluation.DefaultGates()` and 30 labelled cases with 6 false positives, explain whether the profile should be promoted | The natural answer is "6/30 = 0.20, which meets the gate, so yes" | "The gate is met on the point estimate and the data are consistent with a true rate of 37%. The gate is not demonstrated. I would report the interval and not promote." Full arithmetic in `../engineering/ai-and-data.md` §3.3 |

**A note on the Senior Backend task.** Pointing a candidate at a real bug in
unreleased code is a legitimate and common technique, and the bug must be one
that exists. It does. The scorecard should record which of the three they found
and, separately, how they handled finding it.

### 5.3 What is actually tested, per role

| Role | Tested | Not tested |
|---|---|---|
| **Solutions Engineer** | Can they turn a customer description into a configuration? Do they ask what the site's timezone is before writing a schedule? Do they treat "the customer wants X" as a discovery question? | Whether they have used Milestone specifically |
| **QA / SDET** | Do they write a test that fails when the thing breaks? Do they understand a race? Do they read the Makefile? | Coverage numbers. `../engineering/quality-engineering.md` §11.3 names a list of coverage metrics we refuse |
| **Product Designer** | Can they design for the degraded state? Do they treat the operator's time as the budget? Do they show work rather than describe it? | Visual polish. `../engineering/ux-and-design-system.md` §4.2's constraint decides what components are possible; taste without the constraint is not what we need |
| **Senior Backend** | Do they look for the invariant a piece of code protects before they change it? Do they ask "was this intentional" before "this is wrong"? | Whether they have written Go before — the language is learnable in a week; the instinct is not |
| **Applied AI** | Do they reason about an interval instead of a point estimate? Do they distinguish "the model was wrong" from "the label was wrong"? Do they ask what the denominator is? | Whether they have shipped a model |
| **AppSec** | Do they read a threat model looking for the assumption that is wrong, rather than the control that is missing? | Whether they hold a certification |

### 5.4 The AI-usage policy and its rubric

The team is AI-assisted (`../engineering/team-and-operating-model.md` §2). Two questions must be
answered in every interview and both have to be answered honestly, because the
alternative is discovering it in month three.

**Policy, stated once:**

1. **Disclosure is mandatory.** If AI produced or materially shaped any part of
   the work sample, say so, and say which part and how much.
2. **Verification is the candidate's, not ours.** We do not check whether you
   used AI. We check whether you can defend every line. "The model wrote it" is
   an answer to a different question and it is not accepted.
3. **The invariant applies to you.** `AGENTS.md` says an agent never decides
   scope, never signs a gate, never talks to a customer, never ships an
   unverified bundle. A human who delegates those to an assistant has violated the
   working agreement the same way an agent would.
4. **Prompt injection is a job skill, not a taboo.** The work samples include
   untrusted input (a rule bundle, a vendor envelope). A candidate who pipes an
   untrusted file into a model and trusts the output has demonstrated the exact
   failure this product spends its whole budget preventing.
5. **No AI-generated candidates' CVs, no AI-written references, no AI-generated
   test answers during a take-home.** This one is not negotiable and the reason
   is in `AGENTS.md`: the five invariants are protected by tests, and a hiring
   process that generates its own evidence is a process we would have to
   distrust.

**Rubric, scored 0–3 per dimension, with 0 meaning "would not pass an
interview loop on this dimension alone":**

| # | Dimension | 0 | 1 | 2 | 3 |
|---|---|---|---|---|---|
| 1 | **Disclosure quality** | Did not mention AI | Mentioned it vaguely, when asked directly | Volunteered it unprompted, named what it did and did not do | Volunteered, and named a place where they deliberately *didn't* use it and why |
| 2 | **Defensibility** | Cannot explain a third of the work sample | Explains the happy path | Explains every line, including the boring ones | Explains what would break it and how the test would catch it |
| 3 | **Verification instinct** | Assumed the output was right | Spot-checked one thing | Verified the load-bearing claims and ignored the rest | Verified the load-bearing claims *and* said which ones they deliberately left unverified and why that was safe |
| 4 | **Untrusted-input handling** | Piped untrusted content into a model and trusted the output | Aware of the risk, no practice | Treats untrusted input as data, with a concrete example | Has a story about catching a real injection, and can describe the mitigation they now use |
| 5 | **Honest uncertainty** | Confident and wrong | Hedges vaguely | Says "I don't know, here is how I'd find out" | Says "I don't know" early enough that it saved time rather than costing it |

**A score of 0 on dimension 1 or dimension 5 is disqualifying regardless of
total.** Dimension 5 is the one that matters most for a company whose product
claim is provability: a person who is confidently wrong about a number will put
a number in front of a security buyer.

---

## 6. Onboarding an engineer into this codebase

This section assumes the hire is a **generalist or backend engineer** arriving
at day 1 with no context.

### 6.1 Day 1

| | |
|---|---|
| **Read, in this order** | `AGENTS.md` (82 lines, read twice) → `README.md` → `docs/status.md` |
| **Set up** | Go 1.26 toolchain (see below — there is a version conflict, §6.6), `protoc`, MinGW-w64 for `-race` on Windows. On Windows, `make` runs through Git Bash |
| **Run the build** | `make bootstrap`, `make generate`, `make build` |
| **The three commands that matter** | `make contract-check`, `make build`, `make test-race`, `make lint` — the four in `AGENTS.md` §Verification. Everything else is convenience |
| **The local database** | From `docs/status.md`: `docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=security_test -p 55432:5432 postgres:16`, then `export SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'`, `export SP_TEST_DATABASE_PASSWORD=postgres`, `go run ./cmd/security-migrate up` |
| **The Python runtime** | Python 3.11+ (3.14 via the `py` launcher). `cd deploy/ai-runtime && py -m unittest discover`. It is standard library only and CI asserts that by grepping the imports — a new dependency in `runtime.py` fails the build |
| **Do not** | Change `contracts/`, `migrations/` or `internal/contracts/`. `AGENTS.md`: "Contracts are the source of truth. Change `contracts/` first, regenerate, and let `contractcheck` tell you whether you broke the wire. Never hand-edit `internal/contracts/`." `../engineering/team-and-operating-model.md` §2.2 makes this an agent rule too |

### 6.2 Day 7

- **Read the spine.** `docs/roadmap/README.md` → `../strategy-and-north-star.md` →
  `../master-roadmap.md` → the milestone's own plan. The question to answer by Friday
  is: *what is this company refusing to build, and why?* (`../strategy-and-north-star.md`
  §7's anti-segments.)
- **Read the contracts.** All thirteen protos in `contracts/protobuf/security/v1/`,
  the OpenAPI, and the four JSON Schemas. They are the source of truth and they
  are readable in an afternoon.
- **Read the pipeline.** `internal/milestone` → `internal/inbox` → `internal/normalize`
  → `internal/graph` → `internal/rules` → `internal/incidents` →
  `internal/observations` → `internal/audit`. In that order, because that is the
  order the data flows.
- **Ship one small thing** with the invariants intact. The measure of day 7 is not
  what shipped; it is that `make ci` was green before and after.
- **Write down every question you had.** That list is the best onboarding
  artefact the company will ever produce, and it should be filed in
  `../engineering/team-and-operating-model.md`'s intake file.

### 6.3 Day 30

- **Own one stage of the pipeline** end to end, including its `kill -9` test.
- **Read `docs/threat-model.md` and the trust-boundary sections** of
  `../security/trust-and-security-program.md` §10.3.
- **Make one decision and write it up** as an ADR using
  `../engineering/decisions-and-process.md` §3.1's template, including "What would reverse this".
- **Sit in on a customer interaction** if there is one. Nobody who has not seen an
  operator's face should own the exception queue.
- **The measure of day 30**: they can explain, without notes, why
  `normalize.Normalize` commits the canonical event and the inbox transition in
  one transaction, and what happens if it did not.

### 6.4 Why `-p 1` is required — the thing every new engineer gets wrong

`Makefile:43-48`:

> `-p 1`: the database-backed suites share one database and rebuild the schema, so
> package test binaries must not run concurrently. Without it, one suite's DROP
> SCHEMA lands in the middle of another's test and produces failures that look
> like product bugs.

It is not a flake to be retried. It is a correct-by-construction requirement, and
the failure mode it prevents — *product-looking failures from test interference*
— is exactly the failure mode that trains a team to ignore a red build. A new
engineer who "fixes" this by retrying has introduced a worse problem.

`the suite must pass with no Milestone server reachable. If a test needs one, the test
is wrong` (`AGENTS.md`). Every scenario comes from `internal/fixture`.

### 6.5 Why the Python runtime exists in a different language, and how to run it

Because a hung inference must not hang ingest, and because a library call inside
the core cannot honour that (`runtime.py:1-19`). It is a separate process with its
own service account and its own database grants.

```sh
cd deploy/ai-runtime && py -m unittest discover
```

CI runs it as `py -m unittest discover -s deploy/ai-runtime -v` and, before the
tests, greps the import lines and fails on any third-party module
(`.github/workflows/ci.yml:130-139`). The reason is stated there: "a service that
runs on a customer's site and touches camera footage is not the place to take a
supply-chain dependency it does not need."

**As of this writing the runtime has no model.** `Runtime.run` raises
`DEGRADED_NO_RUNTIME` unconditionally. A new engineer will ask whether that is a
bug. It is not: `docs/status.md` explains it, and the tests assert the degraded
path rather than a placeholder — "a stub returning `{"restrictedZoneBreach":
false}` would produce a corroborated incident on a fabricated negative, which is
the single worst failure this product can have."

### 6.6 A version conflict a new engineer will hit in hour one

`go.mod` says `go 1.26.0`; `.github/workflows/ci.yml` pins `GO_VERSION: "1.26"`
with `GOTOOLCHAIN: local`; `docs/status.md` says **Go 1.27**. The go.mod and CI
agree, so CI builds — but `docs/status.md` will send the new engineer to install
a toolchain one minor version ahead of what the module declares, and `GOTOOLCHAIN:
local` means it will not silently upgrade. **Whoever installs the toolchain
should read `go.mod`, not `docs/status.md`.** This is a documentation defect and
its owner should fix the status line.

---

## 7. Org design

### 7.1 How the team is organised

**One team, no sub-teams, no layers, until there is a reason that is not
headcount.** The organising principle is the milestone: exactly one milestone is
active plus one parallel track that is never itself on the critical path
(`../strategy-and-north-star.md` §6). Teams are cut along the milestone, not along
the function, because a functional cut at six people produces two teams that each
have half of every competence.

The unit of ownership is a **stage of the pipeline and its invariant**, not a
component. The stage table in `../engineering/quality-engineering.md` §5.1 is the real org chart
at M1: intake, normalize, graph, verification, observation, disposition, audit,
action. Each stage has an owner and a fault-injection test.

### 7.2 Flat, no management, and when it is revisited

**The decision: no management layer until there are 12 engineers, or until a
person has 5+ direct reports, whichever comes first.** It is revisited
explicitly at 8 engineers, not at 12, because the first manager hire is a
calendar decision and the calendar should be looked at before it is forced.

| Headcount | Shape |
|---|---|
| 2–4 | Everyone is a reviewer. Decisions by conversation. `../engineering/team-and-operating-model.md` §3's weekly rhythm |
| 5–7 | The weekly rhythm's Monday planning meeting starts to strain. Still flat. Explicit decision rights in §4's RACI become the tool |
| 8 | **The manager-hire trigger is evaluated** (§7.4) |
| 9–12 | One engineering manager, or a team lead who does not manage. Test whether the manager actually manages; if the answer is no, keep it flat and hire a second senior instead |
| 13+ | Two teams, cut along the M-series boundary: Trust & Adapter (M4 shape) and Intelligence & Autonomy (M6/M7 shape). A real split, because the two have opposite risk profiles — one is integration risk, the other is judgment risk |

**Why flat survives longer than people expect here:** the product's core claim is
provability, and provability requires that the person who changed a thing and the
person who signs the gate can be the same person. A management layer inserts a
reviewer who did not make the change and cannot answer "why is this here" without
asking. That is a real cost, and it is paid back only when review is the
bottleneck rather than context.

### 7.3 How E1 and E2 split responsibilities as headcount arrives

| | E1 | E2 |
|---|---|---|
| **Permanent** | Product, Architecture, Security, Sales, design-partner relationships, every customer-facing claim | Backend, AI/ML, Data, Release, the build, CI, the installer |
| **Powers down as** | H1 (Solutions) takes deployment and support-first customer contact | H2 (Applied AI) takes the model runtime; H5 (Senior Backend) takes the adapter and the SDK |
| **Never gives up** | Signing every exit gate jointly with E2 | Signing every exit gate jointly with E1 |
| **Powers up into** | Channel (H6), which is a sales motion; and a product manager at M6, which frees E1 from product | Support/on-call technical escalation, which `../engineering/team-and-operating-model.md` R-11 assigns to E2 today |

**The thing that must not happen:** E1 and E2 both powering down in the same
quarter. The capacity law's 40% tax is a *joint* burden — the support inbox, the
demo, the customer call and the release week all land on both. Two departures
from the plan in one quarter is a schedule event, not a hiring event.

### 7.4 The first manager hire's trigger

**Four triggers, any one of which fires it. They are checked at 8 engineers.**

| # | Trigger | Why it is a trigger rather than a preference |
|---|---|---|
| 1 | **Someone has 5+ direct reports and is spending more than 20% of their time on coordination** | This is the only trigger that is a *measurement*. The other three are structural |
| 2 | **The weekly rhythm has become the bottleneck** | `../engineering/team-and-operating-model.md` §3.4's rule protects the week. A team that needs a meeting to coordinate is a team that has outgrown talking |
| 3 | **The engineering half of a milestone cannot be staffed** | M4 and M5 are both larger than the people available. At that point someone must decide *what* is staffed, all day, and that is a manager's job |
| 4 | **On-call becomes a rota with real consequences** | H9–H14 arrive in 2029. `../engineering/release-sre-and-support.md` §7.3's honest version of support hours assumes a person whose job is that |

**The first manager is an engineering manager, not a head of engineering, and not
a "VP Engineering" at 12 people.** The company will not have a VP at this scale
and pretending otherwise is how a two-person company becomes a middle-management
company. When it is hired, the founding engineers report to them and the manager
reports to the board — and E1 and E2 keep the unfailing veto on the gate
signature, because the gate is a personal attestation and an attestation cannot
be delegated to a role.

---

## 8. People risks

### 8.1 Bus factor on every critical path

| Path | Bus factor | The mitigation | The honest verdict |
|---|---|---|---|
| **The Milestone adapter** | **1** (E2) | H5 at 2028-04 | Until then, if E2 is unavailable the M1 gate is unreachable. **This is the single largest key-person risk in the company** |
| **The contract / wire** | **1** (E1) | ADRs; `../engineering/decisions-and-process.md` §3.3 ("an ADR is never edited after acceptance") | The contracts are files and the baseline is a file, so the *knowledge* is partly externalised. The judgement is not |
| **Migrations** | **1** (E2) | Forward-only, expand/contract separated, phase in the filename, `migration-phases` in the Makefile | A schema is a file. A schema *decision* is not |
| **The AI plane** | **1** (E2) | H2 at 2027-10 | And as of this writing the plane is not connected at all (`../engineering/ai-and-data.md` §1.5), so the risk is a design risk more than a key-person risk today |
| **The design-partner relationship** | **1** (E1) | H1 at 2027-07 | `gtm-and-sales.md`'s whole design-partner section is E1's calendar |
| **Security** | **1** (E1, accountable) | Fractional consultant from 2027-04; H7 at 2028-07 | Accountable and implementable are two people, which is the correct split and leaves no slack |
| **The release pipeline and installer** | **1** (E2) | H5; `securityctl`'s lifecycle commands are already exercised by hand | — |
| **Signing the gate** | **2** (jointly) | None possible, and none wanted | `AGENTS.md`: "Every exit gate in this roadmap is signed by a human." Joint signature means one absence blocks a gate. **That is a feature**: a gate that one person can sign alone is a gate one person can be talked into |

### 8.2 The two-person point of failure

It is not a cliché here, it is arithmetic. `../strategy-and-north-star.md` §6 sets
2 engineers; `../engineering/team-and-operating-model.md` §1.1 names the two bottlenecks. **Both
founders being unavailable for two consecutive weeks is a milestone slip, not an
incident.** There is no redundancy to draw on, no on-call partner, and no
handover document because there is nobody to hand over to.

**What mitigates it, honestly:** `../engineering/team-and-operating-model.md` §7.2's vacation
rule and §7.4's early-warning signs. And the AI agents, which are a real
mitigation for *throughput* and none at all for *judgement* — an agent can write
the migration, and `AGENTS.md` is explicit that it cannot decide whether the
schema is right.

**What the company should actually do about it:** buy the founders' health
insurance and take the vacation. §7.3's rule that survives everything is that at
two people, rest is a reliability control. A company that skips it will not
discover that in M1; it will discover it in M5 with eleven people and no
founder who has worked a full quarter in two years.

### 8.3 Knowledge concentration

Three specific concentrations, and one general rule.

1. **The Milestone API surface is one person's memory** plus a file of
   `Unverified: true` routes. The file is the mitigation and it is not
   sufficient: the file records *what is assumed*, not *why it was assumed*.
   **Action:** every route's `Notes` should record the reason, and
   `securityctl adapter probe` (which does not exist and is referenced five
   times) should write its output back into the `Notes`.
2. **The customer sites are one person's relationships.** Not transferable at any
   price. H1 at 2027-07 is the mitigation and it takes four months.
3. **The invariants are enforced by tests, and that is the good news.** The tests
   are in the repo. The *reasoning* behind them is in `AGENTS.md` and in the ADR
   set — and `../engineering/decisions-and-process.md` §3.4 notes the first five ADRs are
   **overdue**. Writing them is the cheapest knowledge-transfer action available
   and it is not a hire.

### 8.4 Burnout

The 40% tax is the mechanism, and the mechanism is well-documented:
`gtm-and-sales.md` §8 itemises it (8 h/week of E1's time on design partners in
M1, rising), `../engineering/team-and-operating-model.md` §7 covers sustainability at two
people, and `../engineering/release-sre-and-support.md` §10's "when our product is wrong" is
about the emotional cost of disclosure.

**The specific risk this plan creates:** H1 arrives at 2027-07, which is *inside*
M2, and the design-partner motion does not pause. E1's calendar gets a colleague
and E1's *load* does not. The predictable result is that the founder stays in the
customer call because they are good at it, and the hire becomes a second person
carrying the work while the founder carries the relationship. **Mitigation, and it
is a decision not a hope:** the SE takes the customer call from week 4, E1 sits
in as an observer, and `gtm-and-sales.md` §8.4's rules about the founder's time
are written into the SE's scorecard. Otherwise H1's 90-day review will say
"great hire" and the calendar will be unchanged.

### 8.5 Security clearing for regulated customers

Several segments in `../strategy-and-north-star.md` §3 — healthcare, critical
infrastructure, government — will ask about personnel screening, and some will
require it contractually.

| Constraint | Reality | What to do |
|---|---|---|
| **Clearance requirements** | Healthcare and government buyers may ask for background checks on staff with site access | Do not compete on this. A two-person company cannot provide a facility clearance and should say so in the first call, not the fifth |
| **The people with site access** | At M1–M2, it is the founders and possibly H1. At M5, it is a support rota of four | This is a real hiring constraint at H9–H14: **the support rota hires need whatever screening the customers require, and that changes the cost and the location of those hires** |
| **The alternative posture, which is better** | `financial-plan.md` §5.4: "we do not claim SOC 2 and we do not intend to; we publish the control-to-test map instead" | Extend that honesty: we publish our people policy, our access procedure and our training record, and we do not claim certifications we do not hold. A buyer who needs a cleared vendor is not a buyer for a company this size |
| **The one that does bite** | If a customer requires screening, and we cannot provide it, the deal is declined. That is a *commercial* limit and it belongs in `gtm-and-sales.md`'s qualification, not in a hiring plan | Name it now, not in M6 |

### 8.6 "We cannot hire a senior person we cannot afford to lose"

This is the constraint that shapes the whole comp philosophy in §9, and it
deserves its own statement.

A senior backend engineer is the most valuable hire on the plan and also the one
whose departure is worst. `../engineering/team-and-operating-model.md` §1.3's on-call reasoning
applies to hiring: **a hire we cannot afford to lose is a hire who has leverage,
and a pre-seed company that creates leverage accidentally ends up making every
decision around one person's comfort.** The specific failure mode is a company
that cannot cut a scope because the person who built it objects, which is
precisely the failure `../strategy-and-north-star.md` §6's "scope reduction is the
only allowed response to a slip" depends on not happening.

**Three rules, and they are not negotiable at any headcount:**

1. **Every role's scope is written down before the hire starts**, in the
   milestone plan, and changing it is a plan change (../engineering/product-and-backlog.md §7.1),
   not a personnel matter.
2. **No role is a single point of failure past M2.** The bus-factor table in §8.1
   is a review item, not a document.
3. **Documentation is a condition of employment for the two most senior
   engineering roles**, specifically the ADR set (`../engineering/decisions-and-process.md`
   §3.4's first five are overdue) and the runbooks
   (`../engineering/release-sre-and-support.md` §7.6). This is not bureaucracy: it is the
   company's only defence against the concentration in §8.3.

---

## 9. Compensation philosophy for the first ten hires

### 9.1 Bands come from the market, not from the founders' salary

**The rule: every band is set from market data for the role, and the founders'
compensation is never an input to it.** The reason is specific rather than
principled-sounding: `financial-plan.md` §1.1 records that both founders are
unpaid, and §1.2 records that founder salary starts at $7,500/month combined
when tranche 1 closes. **An unpaid founder and a paid employee at the same rate
is a statement about the person's worth, made by accident, by two people who
cannot afford the alternative.** It also makes every future raise conversation
start from a number that was never a salary.

**Bands are ESTIMATE and must be replaced with real data before an offer is
made.** A band that is a guess is a promise the company cannot keep.

| # | Role | Base band (ESTIMATE) | Fully loaded (×1.28) | Monthly loaded | Band width |
|---|---|---|---|---|---|
| H1 | Solutions Engineer | $80k–100k | $102k–128k | $8,500–10,700 | 25% |
| H2 | Applied AI/ML Engineer | $95k–125k | $122k–160k | $10,100–13,300 | 32% |
| H3 | QA / SDET | $80k–102k | $102k–131k | $8,500–10,900 | 28% |
| H4 | Product Designer | $82k–108k | $105k–138k | $8,800–11,500 | 32% |
| H5 | Senior Backend | $105k–132k | $134k–169k | $11,200–14,100 | 26% |
| H6 | Enterprise Solutions (OEM) | $82k–108k | $105k–138k | $8,800–11,500 | 32% |
| H7 | AppSec | $100k–132k | $128k–169k | $10,700–14,100 | 32% |
| H8 | SRE | $95k–125k | $122k–160k | $10,100–13,300 | 32% |
| H9–H14 | Support engineer | $55k–72k | $70k–92k | $5,900–7,700 | 31% |
| H12 | Security/compliance | $88k–115k | $113k–147k | $9,400–12,300 | 31% |

**Placement inside the band is not negotiable in the founder's favour and not
negotiable against the candidate either.** Three inputs, weighted: the role's
market band, the scarcity of the specific skill (an integrator with a VMS
background is scarce and should be at the top), and *equity* (§9.2) — a
candidate who will take less cash for more equity at pre-seed has made a bet on
us, and it should be honoured, not extracted.

**Two things this table does not do:** it does not have a "seniority inflation"
row, and it does not allow a band to be widened to fit a candidate. A band
exists to make an offer defensible to the next employee, and widening one for
one person makes the table a fiction.

### 9.2 Equity before seed

| Stage | Instrument | Range (ESTIMATE) | Notes |
|---|---|---|---|
| **H1–H3 (pre-seed, 2027)** | Options over a common pool | 0.30%–1.20% for H1, 0.40%–1.50% for H2/H3 | H1 takes more than H3 because the early hires carry the most risk and the least optionality. **4-year vest, 1-year cliff** — the cliff is not a punishment, it is the reason the equity means something |
| **H4–H7 (2028)** | Options | 0.35%–1.00% | Stable; the *value* of the same percentage grows with the company and that is the point |
| **H8+ (2029)** | Options | 0.20%–0.70% | As the pool grows and the option price rises, later hires get more absolute value from the same percentage. This is correct and should not be "corrected" |
| **Pool** | 10%–12% post-seed, from a 15% pre-seed pool | — | A pool that is not topped up at the seed is the most common way a pre-seed company loses its first engineer at the moment it matters most |

**The philosophy, stated plainly:**

1. **Equity is a bet, not compensation.** Nobody should be asked to take a lower
   cash band because of equity at pre-seed, and nobody should be *offered* equity
   as a substitute for a fair cash band. Equity is what is left after the band is
   set.
2. **The option price is the honest one.** The first price is the pre-money at the
   pre-seed. It is not adjusted upward to make the grant look larger, and it is
   not adjusted downward to make an early hire feel better.
3. **Vesting is uniform.** 4 years, 1-year cliff, no exceptions for anyone,
   including the founders. An exception here is the one thing an employee finds
   out about in the worst possible week.
4. **The founders' equity is discussed when there is a round to discuss it in,
   and not before.** Two unpaid founders holding 100% of a company with no
   funding is a fact, not a plan, and pretending otherwise in an offer letter is
   a way to lose a hire in month six.

### 9.3 The honest statement about competing with a funded competitor

**We cannot compete on cash with a funded competitor for the first three hires,
and the plan should stop pretending otherwise.**

The arithmetic: `financial-plan.md` §1.1 records **no funding, no revenue, no
grant** — the company is funded by two people who are not being paid. A funded
competitor in physical security can offer 30–50% more cash at the same band, or
the same cash for a role that is two levels more senior. Against that, three
things are true and one is not.

| | Truth | Verdict |
|---|---|---|
| **Scope** | H1 owns deployments *and* site configuration *and* the measurement design *and* first-line support. At a funded competitor that is three roles, or one role at twice the level | **Real advantage.** Not a discount to be proud of — a structural consequence of being small, and it is worth naming honestly as "you will do a wider job sooner" |
| **Ownership** | At M3 the equity in §9.2 is a real position in a company whose first customers are named. At a funded competitor, an SE at the same level owns a slice of a roadmap | **Real advantage, and the only one that compounds** |
| **The invariant work** | `AGENTS.md`'s five invariants, the ADR discipline, the `make ci` gate, the replay path. Most companies do not have this and most would not recognise it as an advantage until they tried to remove it | **Real advantage, and it is invisible to a candidate who has not worked somewhere it matters.** It has to be shown, not claimed — a candidate who reads `AGENTS.md` in the take-home is the right hire |
| **Cash** | No funding, no revenue, founders unpaid | **Not an advantage. It is a real disadvantage and it should be said in the first interview, not the fourth** |

**The rule that follows: we do not hire out of desperation, and we do not sell a
vision to cover a cash gap.** A hire made because the alternative is worse than
the role is a hire who leaves at the first raise cycle, which is at tranche 2 —
and `financial-plan.md` §4.2's seed is at the M2 gate, so a desperate H1 has a
six-to-nine-month window before the market sets their price.

**The specific consequence for H1, who is the first hire and therefore the one
most likely to be made out of need:** if the search runs past 2027-09-01 without
an accepted offer, the correct action is to **delay the hire and extend the
founders' coverage**, not to lower the band or accept a candidate who will leave.
`../engineering/team-and-operating-model.md` R-13's rule is exact: "A hire that arrives during
a milestone is a slip, not a rescue." A late hire at the right band is a
schedule change. A hire at the wrong band is a second departure to plan for.

---

## 10. A 12-month forward view

Month-by-month from 2026-10. **No founder salaries** (they are in
`financial-plan.md` §2.1 and unchanged). New payroll is fully loaded, ESTIMATE.
Gate column names the milestone gate the hire unblocks.

| Month | Hire | New monthly loaded (ESTIMATE) | Cost band (ESTIMATE) | The gate it unblocks |
|---|---|---|---|---|
| **2026-10** | — | $0 | — | M1 window opens. `../strategy-and-north-star.md` §6's 13-week quarter begins |
| **2026-11** | — | $0 | — | M1: the `securityctl adapter probe` command that `routes.go:16` references five times and that does not exist |
| **2026-12** | — | $0 | — | M1: first design-partner agreement reviewed by counsel ($4,000 one-off, `financial-plan.md` §1.2) |
| **2027-01** | — | $0 + founder salary begins ($7,500 combined) | — | M1: the live-Milestone parity proof. **This is the month `docs/status.md` says is the single most important open item** |
| **2027-02** | — | $0 | — | M1: kill -9 at every stage, evidence-path negatives, the SLO load run |
| **2027-03** | — | $0 | — | **M1 exit gate, 2027-03-31.** All four lines, signed jointly by E1 and E2 |
| **2027-04** | — | $0 | — | M2 opens. Evidence engine begins. **Blocker: no adapter evidence retrieval exists** (`../engineering/integrations-and-adapters.md` §4.3) |
| **2027-05** | — | $0 | — | First paid revenue modelled (`financial-plan.md` §2.1) |
| **2027-06** | — | $0 | — | M2: the AI-plane client that does not exist today (`../engineering/ai-and-data.md` §1.5, F1). H1 search peaks |
| **2027-07** | **H1 — Solutions Engineer** | **+$9,600** | $102k–128k loaded | M2: repeatable deployment, and the **MTTI baseline arm** without which M2's gate cannot be signed. Also: pen test ($12k–25k) and threat-model review ($4k–8k) in this quarter |
| **2027-08** | — | $9,600 | — | M2: first site deployed by the SE with E1 observing, not leading |
| **2027-09** | — | $9,600 | — | **M2 exit gate, 2027-09-30.** A "statistically meaningful margin" per `../engineering/metrics-and-analytics.md` §4. **Deadline for H1: if no accepted offer by 2027-09-01, delay the hire** (§9.3) |
| **2027-10** | **H2 — Applied AI/ML** | **+$21,700** | $122k–160k loaded | M3 opens 2027-10-01. The VLM client, the `vlm` worker, the ten-profile programme, and the playbook runner. Also: brand engagement ($8k–20k) |
| **2027-11** | **H3 — QA / SDET** | **+$31,300** | $102k–131k loaded | M3: "Every automated action is idempotent under replay, proven by a fault-injection test at the action stage" |
| **2027-12** | — | $31,300 | — | M3: playbooks, recommendations, PTZ. MSA review ($5,000, `financial-plan.md` §1.2) |
| **2028-01** | — | $31,300 | — | M3: notification integrations; first GA install |
| **2028-02** | — | $31,300 | — | M3: bounded auto-resolution. Cyber liability and tech E&O from $650/month (`financial-plan.md` §1.2) |
| **2028-03** | — | $31,300 | — | **M3 exit gate, 2028-03-31 — first production GA** |
| **2028-04** | **H5 — Senior Backend/Durability** | **+$44,000** | $134k–169k loaded | **M4 opens 2028-04-01.** The second VMS adapter, 8–12 weeks, one person's uninterrupted work. The non-adapter diff is the gate |
| **2028-05** | — | $44,000 | — | M4: ONVIF Profile T discovery and events |
| **2028-06** | **H6 — Enterprise Solutions (OEM)** | **+$54,200** | $105k–138k loaded | M4: the first OEM conversation, 15–30 months before revenue (`gtm-and-sales.md` §7.3) |
| **2028-07** | **H7 — AppSec** | **+$66,700** | $128k–169k loaded | M4: the security review pack a customer's reviewer first reads. **Consider the fractional consultant for 2028 H1 instead** (§4.2) |
| **2028-08** | — | $66,700 | — | M4: access control — credentials, badges, forced open, held open |
| **2028-09** | — | $66,700 | — | M4: the Adapter SDK, the fixture framework, the source simulator |
| **2028-10** | — | $66,700 | — | M4: sensors and IoT; camera capability declaration |
| **2028-11** | — | $66,700 | — | M4: ONVIF interop campaign ($10k–30k, contracted) |
| **2028-12** | — | $66,700 | — | **M4 exit gate, 2028-12-22.** Second adapter in production with a zero behavioural diff; SDK v1 public; **conformance suite run by someone outside the company** ($15k–30k, contracted) |
| **2028-13 → 2029-01** | **H8 SRE, H9 Support #1, H12 Security 0.5** | **≈ +$27,200 → $95,900** | see §2.8 | **M5 opens 2029-01-04** |

**What an investor reads in this table, stated as five sentences:**

1. **The company hires its first person seven months after its first milestone
   gate, and the reason is that the M1 gate blocker is a customer relationship,
   which is not a thing a third engineer shortens.**
2. **The first three hires are solutions, AI and QA — in that order — and none of
   them is a generalist.** The plan's most expensive hire (H5, $134k–169k) is the
   fourth person and arrives at M4, when the second adapter is the milestone's
   gate and needs one person for 8–12 weeks uninterrupted.
3. **M3 GA is reached with seven people, and the two founders still sign every
   gate jointly.** No hire changes who attests.
4. **M4 to M5 is where the headcount steps, and the step is support and channel,
   not product.** Twenty-four-hour support is a gate on a four-person rota that
   does not exist until 2029-10, so 24/7 is not sold before then.
5. **The 12-month payroll is $364,300** (H1 6 months at ~$9,600 + H2 3 at ~$11,700
   + H3 3 at ~$10,900, plus two months of H5/H6/H7 at the M4 boundary), against
   `financial-plan.md`'s modelled base scenario, which should be reconciled by
   its owner — this plan's H1 role and `financial-plan.md` §1.2's "Deployment &
   Support" are the same person and the same number.

---

## 11. What this document does not claim

1. **The compensation bands are ESTIMATE and unverified.** `financial-plan.md`
   §1.2's "Base tooling — replaced by: the actual invoice, which is a
   five-minute exercise and has not been done" applies here with more force. A
   band that has not been checked against three real postings is a guess.
2. **No headcount in this document is funded.** `financial-plan.md` §1.1: no
   investment, no revenue, no grant, corporate cash on hand **unknown**.
3. **The M1 → M2 → M3 headcount disagrees with `financial-plan.md` §5.3** in
   both directions: lower at M4 (10 vs 11, because this plan adopts §5.3's own
   recommendation to cut M4's scope) and higher at M8 (30 vs 23, because a
   marketplace needs curation). One of the two tables should be corrected by its
   owner and the cash model should win.
4. **The first-hire *role* differs from `financial-plan.md` §1.2**, and §1.4
   argues the reconciliation rather than silently taking one side.
5. **This document does not claim the AI track has a person-shaped dependency
   resolved.** It does not: as of this writing the Go core has no client for the
   AI runtime, `internal/policy` does not exist, and the evidence engine's
   adapter half is a set of route assumptions (`../engineering/ai-and-data.md` §1.5,
   `../engineering/integrations-and-adapters.md` §4.3). Those are the first three things the
   hires in this plan are for, and they are the reason the plan starts with a
   solutions engineer rather than a builder.
