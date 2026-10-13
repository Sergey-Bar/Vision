# Team Structure and Operating Model

Owner: E1. Status: living. Last revised 2026-10-12.

How two engineers and a pile of AI agents actually run an R&D organisation, and
what each role means when the roster is two people long. Read
[`strategy-and-north-star.md`](../strategy-and-north-star.md) §6 (the capacity law) and
[`master-roadmap.md`](../master-roadmap.md) first; this document is the operating consequence of both.

This is not an org chart. There is no third engineer to promote anyone into, and
writing a role catalogue that pretends otherwise would be the first dishonest
document in `docs/roadmap/`.

---

## 1. Role catalogue

### 1.1 The honest two-line split

**E1 holds Product, Architecture, Security and Sales. E2 holds Backend, AI, Data
and Release.**

That is not a preference; it is a consequence of what each person is good at and
what each one is already carrying, and it is a real risk, discussed in §7.

| | E1 | E2 |
|---|---|---|
| Owns | Product, Architecture, Security, Sales, design-partner relationships, customer-facing claims | Backend, AI/ML, Data, Release, the build, CI, the installer |
| Signed gate lines | All of them, jointly with E2 | All of them, jointly with E2 |
| Is the bottleneck for | T03 (site access) — the only critical-path item with no internal lever | T33 (observability wiring) and T19 (install) — the two items that gate three of the four M1 exit lines |
| Failure mode if overloaded | The plan slips on the customer side and nobody notices until W44 | The platform ships and cannot be measured or installed, and gate 3 fails |

### 1.2 The catalogue

Eleven roles the founders asked for, plus two that arrive with the first paying
customer. "Who holds it" is a real answer, not a placeholder.

| # | Role | Why it exists | Who holds it now | Artefacts this role owns | The single metric it is judged by |
|---|---|---|---|---|---|
| R-01 | **Product** | Decides what is built and, more often, what is refused. At 2 people this is the job that keeps scope from expanding to fill the calendar | **E1**, permanently | [`strategy-and-north-star.md`](../strategy-and-north-star.md) §3 segment order and anti-segments; the intake decisions in §5; the demo script; the pilot agreement | **Share of shipped work that maps to a named segment in `00` §3.** A quarter at zero means product drift has started, whatever the velocity looks like |
| R-02 | **Architect** | Owns the things that are expensive to change: contracts, migrations, the trust boundaries, the adapter seam. Everything else can be refactored; the wire cannot | **E1**, permanently | `docs/threat-model.md`; the ADR set; the contract change path; the decision that M4's second adapter is the real architecture test | **Number of invariants weakened without an ADR and a test, in that order.** Target: zero. It is the cleanest possible metric for "is the architecture still ours" |
| R-03 | **UI/UX** | The console is the product for the operator. Nobody else will notice that a degraded incident looks like a quiet one | **E1**, permanently | The server-rendered console templates; the CSP; the time-rendering law; the dead-letter list; M1-T47–T52 in `02` §2 | **Time for a real operator to complete the M1 workflow unaided, measured once in `02` M1-T38.** Not a usability score. One number, from one real person |
| R-04 | **QA** | Owns the DoD claim, the fault-injection matrix, the golden suite, and the path-traversal negatives. At 2 people this is the role most likely to be quietly dropped, so it is written down | **E2**, permanently, with the `fault-injection-writer` agent drafting | M1-T53–T58 in `02` §2; the control-to-test map; the statement in `docs/status.md` that green means green | **`make ci` green with no Milestone server reachable, and a `kill -9` test at every stage M1 touches.** A gate with no failing test is a claim (`docs/threat-model.md` §5) |
| R-05 | **AI/ML** | The VLM runtime, the profiles, the prompts, the evaluation, the promotion decision. The hard part is not inference; it is attribution and the promotion discipline | **E2**, permanently | M1-T09–T17, T41–T46 in `02` §2; the profile JSON Schemas in `contracts/json-schema/`; the evaluation report | **The named gates a profile passes, reported individually — never a single boolean.** Plus, for M1: whether the 14-day shadow run completed |
| R-06 | **Backend** | Durability, API, identity, console server, jobs, outbox, the normalizer. The part that is supposed to be boring for years | **E2**, permanently | `internal/`; the API routes in `internal/api/handlers.go`; the durability substrate | **Invariants 1 and 2 holding under `kill -9` at every stage.** Not throughput. Nothing is optimised for throughput this year |
| R-07 | **Adapter / Integrations** | The one component that has never touched a real server, and therefore the one that carries the whole M1 risk | **E2** (code), **E1** (site relationship) | `internal/milestone/`; the route table; the parity differ; the fixture framework | **`UnverifiedRoutes()` empty, and a real site in `integrations` with a successful `last_success_at`.** Second milestone: a non-adapter diff of zero behavioural changes |
| R-08 | **DevOps / SRE** | Offline install, service units, backup, restore, the release pipeline, the load runs, the SLOs. On a site with no network this is not optional | **E2**, permanently | M1-T18–T35 in `02` §2; `internal/release`; `internal/checkpoint`; the DR drill | **A site brought up from a signed archive, onto service units, with no shell access.** If an install needs a developer's laptop to be right, SRE has not done its job |
| R-09 | **Security** | Threat model, controls, dependency review, disclosure handling, AI governance, privacy. The buyer asks about this first and checks it last | **E1** (accountable), **E2** (implementation) | `docs/threat-model.md`; the control-to-test map; the accepted-risk list; the SBOM; the model-bundle signature policy | **Accepted risk count and severity, restated honestly every month.** A threat model that reports no accepted risk is not being honest about anything |
| R-10 | **Docs / DX** | The operator runbook, the architecture doc (not yet written), the three buyer answers, the demo script, the internal API notes. The thing that makes a 2-person company legible to its own future self and to a customer | **E1** (customer-facing), **E2** (technical), with the `docs-writer` agent drafting | M1-T59–T65 in `02` §2; the architecture doc and operator runbook (neither written yet), and the contract and AI-runtime contract docs | **A gate signed with evidence a customer can read.** A gate signed on evidence only the two founders can interpret will be re-litigated at M2 when it becomes commercial |
| R-11 | **Support** | At two engineers, support is not a department; it is the tax in `00` §6. It arrives the moment there is a site running | **E1** (triage and customer communication), **E2** (diagnosis) | The support inbox; the incident log; `securityctl doctor` and `securityctl preflight` as the first answers | **Time to first written response, and whether the answer came from a doc that already existed.** A support question that needs a code change is a docs defect, not a support ticket |
| R-12 | **Solutions Engineer** | Converts "we have XProtect" into a mapped rule bundle, a site configuration, and a scheduled measurement. This is the M2 and M3 job of deploying the product at a customer | **AI-agent drafted, E1 signed, from M2.** At M1 this is a part-time E1 role and pretending otherwise is how M2 slips | The site configuration; the rule bundle for the customer; the baseline measurement | **Number of sites where the customer produced their own before/after number.** If we have to compute it, they did not feel the value |
| R-13 | **Hiring / People** | Hiring is planned as capacity, never as rescue. The roadmap keeps one parallel track off the critical path so a hire does not arrive mid-milestone | **E1** (accountable), **E2** (technical interview) | The role scorecard; the interview loop; the on-call and vacation rules in §7 | **The first hire lands in the quarter the capacity law says we can afford, and not before.** A hire that arrives during a milestone is a slip, not a rescue |

### 1.3 Roles that are deliberately *not* separate people

| Role people expect | Why it is folded in | Where it would come from if it needed separating |
|---|---|---|
| Data engineering | Postgres is the datastore and there is no second one. `00` §6: no new datastore without an ADR | M5, when multi-site and HA make retention and reporting a real problem |
| Sales | E1 does it directly. A fractional seller at this stage costs a percentage of a revenue line that does not exist yet | First paid pilot closes (`01` §M2 commercial state) |
| Marketing | Nothing is public until M3 GA. Marketing a product whose one distinguishing claim is provability would mean proving it first | M3 |
| Customer success | It is E1's calendar, which is why §6 and §7 of this document cap it | M2, at the third design-partner site |
| SRE on call | One human. §7 says what that actually means, and the answer is "the site degrades, and we say so" | M5, when a site going down is a contract issue rather than a phone call |

---

## 2. AI agent roster

### 2.1 The four rules, stated first

These are not guidance. They are the contract, and a human reversing any of them
is a legitimate act that must be recorded.

1. **AI writes code and first-draft tests. AI never decides scope.** Scope is a
   product decision and it is E1's. An agent that proposes scope proposes it in a
   ticket, and the ticket is decided by a human like any other.
2. **AI never signs an exit gate.** Every line in
   [`m1-trust-spine-execution-plan.md`](../milestones/m1-trust-spine-execution-plan.md) §6 is signed by E1 and E2, by hand. Not
   delegated, not summarised, not "the report says it passed".
3. **AI never talks to a customer.** No agent sends an email, joins a call, posts
   in a channel, or drafts something a human sends without editing it. The design-partner
   relationship is E1's, and the operator's time is the scarcest resource in the
   company.
4. **AI never ships an unverified bundle.** A rule bundle, a release archive, a
   model bundle, a migration set or a contract change lands only after
   `securityctl verify-release`, `contractcheck`, `rules lint`, and the tests,
   all of which are code, not judgement.

### 2.2 The file rule

> **No agent writes to `migrations/`, `contracts/` or `internal/contracts/` without
> a named human diff review before it lands.**

Not "review it if you remember". Not "review it if it looks unusual". Every diff in
those three trees gets:

- a named reviewer (E1 or E2, not "the team"),
- a read of the actual diff, not the PR description,
- an explicit confirmation that the change is *additive* and forward-only.

The reasons, so this is not bureaucracy:

| Path | Why an agent is especially dangerous here |
|---|---|
| `migrations/` | Migrations are **forward-only; an applied migration is never edited** (`AGENTS.md`). An agent "fixing" a typo in `0007_audit.expand.sql` silently invalidates every applied checksum and `migrate.TestVerifyChecksumsCatchesDrift` fires weeks later, on a site, with no obvious cause. An agent can also easily bundle an expand with a contract step, which `01` calls out as the thing that takes a site down |
| `contracts/` | "No language owns the schema" and a compatibility gate fails the build on a removed field, a reused tag, a changed type or cardinality, a moved `oneof` member, a renamed enum value. These are the changes the gate exists to catch — which means an agent that *tries* to make one of them and updates the baseline is a gate bypass, and it is invisible in a diff of the generated code |
| `internal/contracts/` | **Never hand-edited, by anyone** (`AGENTS.md`). It is generated output. A human writing here is committing an error that regeneration overwrites; an agent writing here is committing one that survives until the next `make generate` on someone's machine |

Concretely, the rule for those trees is: an agent may *draft* a change and may
*explain* one, but the human runs the command that produces the generated code.
The agent never runs `make generate` and commits the result, because that is the
step where the baseline and the intent can diverge without anybody reading the
input.

### 2.3 The roster

Eight agents. Each has a name, a scope, an autonomy boundary, a review SLA, and a
named human owner. The SLA is a working-hours clock, because a 2-person company
does not have an on-call rota for a build agent.

| Agent | Scope | Autonomous up to | Requires human review before it lands | Review SLA | Human owner |
|---|---|---|---|---|---|
| **`adapter-verifier`** | `internal/milestone/`, `securityctl adapter-probe`, the parity differ and its tests | New probe and diff tooling. Failing tests. Fixtures sanitised by the existing `internal/fixture/sanitize.go` choke point | **Any change to `Routes`** — method, path, `Expect`, `Notes`, `Paginated`, `Unverified`. Any change to the canonical-event mapping. Any fixture content | **Same day, blocking.** A wrong route is a silent empty feed on a customer's site, and nothing in the codebase will fail | E2 |
| **`runtime-implementer`** | The Python VLM runtime, its schema-validation client, its tests | Service skeleton, health/readiness, degradation paths, offline packaging | The prompt template and `prompt_version`. The model bundle loader and its verification. Anything that decides a `degraded_reason` value. The core↔runtime contract (that one goes through `contracts/`, so §2.2 applies) | **Same day, blocking.** The prompt is the product; the degradation path is invariant 3 | E2 |
| **`installer-implementer`** | `cmd/securityctl` install/upgrade/rollback/backup/restore, the install state machine, its fault-injection tests | Implementation against the written semantics, the layout, the tests | Every refusal path. The backup/restore completeness rule. Any file written outside the documented layout | **Same day, blocking.** An installer that reports success without having done the thing is a site that believes it is patched | E2 |
| **`packaging-implementer`** | `deploy/`, unit files, the release manifest and SBOM entries, packaging CI | Unit files, manifest entries, packaging scripts | Anything inside the signed archive. The SBOM. The trusted-key path. `.github/workflows/*` | **Same day, blocking.** B6 is the boundary where a supply-chain attacker gets code execution on every site at once | E2 |
| **`fault-injection-writer`** | `internal/*/`*`_test.go` durability tests, the traversal negatives, the golden suite | New tests only. Never production code. Never an assertion loosened | Any change to an existing assertion. Any "expected" value that moves. Any skipped or `t.Skip`ed test | **Same day, blocking.** Loosening an assertion is how an invariant is weakened without an ADR | E2 |
| **`console-drafter`** | `internal/api/templates/`, `internal/api/console.go`, console tests | Markup, copy variants, accessibility, template tests | Every template change, for CSP compliance (no inline script). Every wording that reaches an operator. Any styling that could imply completeness when the incident is still assembling | **Same day, non-blocking for markup, blocking for copy and CSP** | E1 |
| **`docs-writer`** | `docs/`, `*.md`, CLI usage text | Drafts, first-pass edits, cross-reference fixes | **Every claim that reaches a customer.** Every number. Every date. Every statement about what the system does — because docs drift silently and a stale doc is a false claim | **Same day for customer-facing, 48h for internal** | E1 (customer-facing), E2 (technical) |
| **`eval-runner`** | The evaluation harness driver, the label pipeline, the profile reports | Running the harness, emitting the report, computing FPR/recall/disagreement/latency from stored rows | The **promotion decision** (always, always human). The gate thresholds. Any manual relabelling. Any exclusion of a case from a denominator | **Same day, blocking.** A relabelled case is a moved number, and moved numbers are how a profile ships | E2 |
| **`load-harness`** | The load generator, the run recorder, the SLO run capture | Generator implementation, run recording, report formatting | The declared load rate, which must be **fixed before the run, not after it**. Any exclusion of outliers from a quantile | **Same day, blocking** | E2 |

### 2.4 The review rule that makes the roster work

| Situation | Rule |
|---|---|
| An agent's diff is small, in a non-protected path, and obviously correct | The human reads the diff and lands it. Review is *reading*, not *approving a summary* |
| An agent's diff touches `migrations/`, `contracts/`, `internal/contracts/`, a threat-model control, a golden fixture, a refusal path, or a gate threshold | Named-human diff review, explicitly, per §2.2. Not delegable to another agent, and not delegable to "the CI passed" |
| An agent disagrees with a human | The human wins and the disagreement is written down. An agent's architectural opinion is evidence for a decision, not a decision |
| An agent's change is wrong in a way the tests did not catch | The human writes the failing test **first**, then the fix. This is the `tdd`-shaped rule and it is not negotiable for the durability and security paths |
| An agent proposes new scope | It becomes a ticket and goes through §5. It does not become a branch |
| An agent cannot be reviewed today | The work does not land today. There is no "merge and fix in review" for a `migrations/` or `contracts/` diff |

---

## 3. Weekly operating rhythm

Two people can afford about **2.5 hours of synchronous time per week** and still
do the work. Everything else is async, written, and short. The default is async;
a meeting is what happens when a decision needs two people at once.

### 3.1 The week

| Day | Block | What happens | Sync? |
|---|---|---|---|
| **Monday** | 09:30–10:00 | **Planning, 30 min, both.** Read the week's board. Three questions: what is the single most important thing that must be true by Friday; what is blocked; what will we cut. Written into the board before the meeting starts, not during | **Yes** — the only required meeting of the week |
| Monday | 10:00–13:00 | Deep work, no meetings, no chat. Longest uninterrupted block of the week | No |
| Monday | 13:00–14:00 | Overhead: inbox, calendar, customer replies, support | No |
| Monday | 14:00–18:00 | Deep work | No |
| **Tuesday** | 09:30–13:00 | Deep work | No |
| Tuesday | 13:00–14:00 | **Design-partner block** (E1) / **review queue** (E2) — E1 answers the week's customer items, E2 clears the agent review backlog | No, but visible |
| Tuesday | 14:00–18:00 | Deep work | No |
| **Wednesday** | 09:30–12:30 | Deep work | No |
| Wednesday | 12:30–13:30 | **Mid-week sync, 30 min, both.** Not a status report. Three items maximum, each with a decision attached. If there is nothing to decide, it is cancelled | **Yes, optional** |
| Wednesday | 13:30–18:00 | Deep work | No |
| **Thursday** | 09:30–13:00 | Deep work | No |
| Thursday | 13:00–14:00 | **Customer and sales block** (E1) / **integration block** (E2 — the customer site, the adapter, the load runs) | No |
| Thursday | 14:00–16:00 | Deep work | No |
| Thursday | 16:00–17:00 | **Release and risk review, 45 min, both.** What is the state of the week's critical-path items; what is the earliest date a gate line can realistically be signed; what has changed in the risk table. Written down, one paragraph each | **Yes** |
| **Friday** | 09:30–12:00 | **Close-out, 2.5h, both.** `make ci`. Review and land what is reviewed. Write the week's entry: what shipped, what was cut, what the board looks like next Monday. Anything unreviewed is left, not landed on a Friday afternoon | **Yes** — the second required meeting |
| Friday | 12:00–13:00 | **Weekly written note to the board** (async, both, 20 minutes each) | No — it is written, then read |
| Friday | 13:00 onwards | **Off.** Hard. Not "if there's nothing urgent". Two people who work every weekend have 4 months of runway, not 6 | No |

### 3.2 What is async, always

| Thing | Medium | Rule |
|---|---|---|
| A proposal, a design, a decision | Written, in the repo or the board | A decision reached in a meeting without a written record is a decision that gets re-litigated. Write it down the same day |
| Status | The board, one line per workstream, updated Monday and Friday | Never a status meeting. `02` §3 is the calendar; the board is the state |
| Code review | A diff and a named reviewer | Same-day SLA for protected paths (§2.3) |
| A customer question | Email, answered in writing | Written, always, because it is the only record of what was promised |
| A decision that changes scope | A ticket, decided by a human through §5 | Never in a DM, never by an agent, never implied by code that already landed |
| The weekly note | Written, Friday | Two paragraphs. What is true, what is at risk, what we are not doing |

### 3.3 Meetings we do not have

Stated explicitly, because at two people the default failure is a calendar that
fills itself:

- **No daily standup.** Monday's 30 minutes covers the week; the board covers the
  state.
- **No sprint ceremony.** No sprint planning, no review, no retrospective, no
  backlog grooming. "Sprint" is a process for coordinating more people than
  coordination costs; two people coordinate by talking.
- **No 1:1s with a manager.** There is no manager.
- **No architecture review meeting.** Architecture is an ADR, written, reviewed
  asynchronously. If an ADR genuinely needs a conversation, it is 30 minutes on a
  Wednesday and it is scheduled for that purpose.
- **No all-hands.** There is no third person.
- **No status meeting with a customer.** Customers get a written update, and a
  demo when a demo is what they asked for.

### 3.4 The rule that protects the week

**No customer meeting is scheduled into Monday morning, Wednesday morning, Friday
morning, or the Thursday release review.** Those blocks are the plan. A customer
who needs a Tuesday is a Tuesday customer; a customer who needs a Friday morning
call is a customer we have decided is worth more than the plan, and that is a
decision E1 makes explicitly, knowing the cost.

---

## 4. Decision rights and RACI

**R** = Responsible (does the work; exactly one per row). **A** = Accountable (owns
the outcome; exactly one per row). **C** = Consulted (input required before the
decision). **I** = Informed (told after). A = AI, H = human. "AI-A" means an agent
drafts and a human is accountable.

The rule beneath the table: **Accountable is always a human.** Every row.

| # | Activity | R | A | C | I | Notes on the decision right |
|---|---|---|---|---|---|---|
| 1 | **Contract change** (`contracts/`) | AI-A (`docs-writer`, mechanical only) | **E2** | E1 | Both, after | `contracts/` first, regenerate, then `contractcheck` decides. A breaking change is a new contract *version* and needs both signatures, not one. Nobody hand-edits `internal/contracts/` |
| 2 | **Migration** | AI-A (`installer-implementer` drafts; **never** applied) | **E2** | E1 | Both | **§2.2 applies in full.** Forward-only; an applied migration is never edited. Expand and contract ship separately; a contract step is never bundled with its expand. A migration touching `audit_ledger`, RLS or identity additionally requires E1's signature, because those are invariant 4 and 5 |
| 3 | **New rule bundle** | AI-A (`docs-writer` drafts YAML) | **E2** (activate) | **E1** (accept the operational risk) | Design partner | `rules lint` → `rules simulate` → reviewed diff digest → `rules activate --version --diff-digest`. Activation without a digest is refused by design. A bundle that changes what counts as an incident at a design-partner site is a **customer-visible change** and needs the customer's informed consent, not ours alone |
| 4 | **New VLM profile** (or a profile version bump) | AI-A (`runtime-implementer` + `eval-runner`) | **E2** | **E1** | Both | New profile → shadow first, `ShadowGates()` (`MinLabelledCases: 10`), never authoritative. Promotion to production → **E1 or E2, individually, in the ledger, with the report digest in `reason`**. The harness says *eligible*; a human says *promoted*. An agent never says either |
| 5 | **New action type** (something the platform does to the world) | **E2** | **E1** | Security review (`docs/threat-model.md`) | Design partner | Anything with physical impact stays approval-gated — permanently, per `01` §M3. A new action type is a new capability check, which is a schema change, which is the point (`migrations/0008`) |
| 6 | **Dependency addition** | AI-A (proposal) | **E1** | E2 | — | `00` §6: no new runtime, no new datastore, no new protocol family without an ADR. A dependency that breaks installing a six-month-old release on an isolated network is a regression, not a feature. The test: *can `01` §M1's offline install still work if this package is unavailable?* If the answer is no, the answer to the ticket is no |
| 7 | **Release** | **E2** (build) | **E2** (sign) | **E1** (release notes and customer-facing claims) | Design partner | `verify-release` → SBOM → `contract-check` → `make ci`. A release that ships with a failing control test is not a release. E1's veto on release notes is real: an unsupported claim in release notes is a security finding, not a typo |
| 8 | **Security disclosure** (received, or our own) | — | **E1** | E2 | Customer, if affected | Received disclosure: acknowledge in writing within **one business day**, do not discuss publicly, no timeline promised until one is met. Own vulnerability: fix, then disclose in the release notes, no embargo theatre for a product with no installed base. A disclosure never bypasses the invariant list — see §7.3 |
| 9 | **Pricing change** | — | **E1** | E2 | — | Pricing does not exist before M2 (`01` §M1: "no pricing, no self-serve"). When it does, it is E1's, and the *pricing experiment* is part of the M2 milestone scope, not a side activity |
| 10 | **Customer commitment** (a date, a feature, a number in a contract) | — | **E1** | E2 (feasibility) | Both, after | Only E1 makes one, in writing, with a scope reference. E2's input is feasibility, and it is given before the commitment, not after. "We do not build custom for one customer" (§6.2) applies with full force here |
| 11 | **Hiring** (role scorecard, loop, offer) | E2 (technical loop) | **E1** | — | — | The roadmap keeps one parallel track off the critical path *so that* a hire can arrive without stalling a milestone. A hire that lands mid-milestone is a slip and is resourced as one, not as a rescue. Hired-for: **Solutions Engineer at M2** (R-12) is the first realistic hire; **Backend at M3** is the second |
| 12 | **Incident response** (product or site) | **E2** (diagnosis) | Whichever engineer is on call (§7.1) | The other | Customer, if affected | Declared in advance so nobody is deciding the escalation path while it is happening. The hard rule is §7.3: no escalation bypasses the invariant list. Severity is declared by the on-call human in the first 15 minutes and never downgraded without a written reason |

### 4.1 Three decision boundaries worth stating

| Boundary | Rule |
|---|---|
| **Judgement vs. execution** | Execution is delegated freely. Judgement — scope, promotion, a release, a signature, a customer commitment, a disclosure — is not delegated at all. This is the whole `00` §6 "AI multiplies implementation, not judgement" claim, given teeth |
| **Who may stop a release** | Anyone, for a guardrail. E1 and E2 each alone, for a feature. An agent may *flag* a guardrail concern; only a human may act on it. See [`metrics-and-analytics.md`](metrics-and-analytics.md) §6 |
| **Who may weaken an invariant** | Neither, unilaterally. It needs an ADR **and** a test, in that order, and both signatures. There is no path in this document or any other where a date produces a weakened invariant |

---

## 5. Intake: how an idea becomes a ticket, or does not

An idea arrives from four places: founder instinct, a customer, a competitor, or
support. All four go through the same two questions, and the two questions are
the *only* filter. There is no scoring rubric, because a rubric at 2 people is
just a way of rationalising the answer you already had.

### 5.1 The two-person filter

> **Q1. Does this serve a named segment in [`strategy-and-north-star.md`](../strategy-and-north-star.md) §3, at
> the milestone that segment is reachable?**
> **Q2. Does it deepen the intelligence layer above the installed security
> stack?**

**Both must be yes.** Q1 rejects work aimed at a segment we have not reached, or
at an anti-segment (camera and hardware manufacturers, generic SOC/SIEM,
cloud-only, custom single-customer). Q2 rejects anything that grows us into a
different company — which is the test `00` §7 states and which is the one that
actually bites, because Q1 is easy and Q2 is the argument you have to win.

Note the asymmetry: an idea that is good but premature fails Q1 on *timing*, not
on merit. Those are written to a `parking lot` file with the milestone they become
eligible in, and read at the start of each milestone's planning. The parking lot is
not a graveyard; it is a list of things we chose not to be distracted by.

### 5.2 The process

| Step | Who | Timebox | Output |
|---|---|---|---|
| 1. Capture | Whoever had it | Immediate | One line, in the `intake` file, with the source and the date. Not a ticket, not a document |
| 2. Screen | E1 | **Same day for customer-sourced items, 48h otherwise** | The two questions, answered in one sentence each. Three possible outcomes below |
| 3. Decide | E1 (scope), E2 (feasibility) | 24h | Accept / defer / decline, written with the reason |
| 4. Write up | E1, or `docs-writer` drafting | 1h | A ticket: the problem in one paragraph, the segment, the milestone, the definition of done, the thing that would make us stop |
| 5. Queue | — | — | Into `02` §3's calendar, or into a milestone's workstream |

### 5.3 The three outcomes

| Outcome | When | What is written | What is *not* written |
|---|---|---|---|
| **Accept** | Both questions yes, and it fits the current milestone's scope | A ticket with a definition of done, added to the milestone plan | — |
| **Defer** | Both questions yes, but the wrong milestone | A line in the parking lot: the idea, the segment, the milestone it becomes eligible in | A promise. A deferral with a date attached to the customer is a commitment, and that is row 10 of the RACI |
| **Decline** | Either question is no | The reason, in one sentence, in the `intake` file | Nothing further. A declined idea is not re-litigated in a meeting |

### 5.4 The two things that make this work

**It is fast.** A customer idea gets an answer the same day, even when the answer
is no. The cost of a slow "no" at a design partner is the relationship, and the
relationship is the only distribution we have before M4.

**It is written down and it says why.** "No" with a reason is a decision. "No"
without one gets re-asked in six weeks, and re-asking is what turns a filter into
ceremony. When a decision is reversed, the original entry is annotated with the
reversal and the reason — never deleted, the same law the audit ledger obeys.

### 5.5 The intake file

| Field | Purpose |
|---|---|
| Date, source, who | So a pattern is visible. Three of the same idea from three customers is a segment; once is an anecdote |
| One-line problem | The problem, never the solution. "The operator cannot tell a forced door from a propped one" is a problem; "add a forced-door rule" is a solution that may be the wrong one |
| Q1 answer, with the segment named | Forces the segment to be real rather than plausible |
| Q2 answer | Forces the "different company" test to actually be run |
| Outcome and reason | The written record |
| Milestone, if deferred | Turns deferral into a plan |

---

## 6. Communication

### 6.1 Channels

| Channel | Who is on it | Response commitment | Notes |
|---|---|---|---|
| **Email** | Design partner's named contact, their operations lead | **One business day**, in writing, always | The only channel where a commitment is made. Everything else is not a promise |
| **Shared inbox** (`support@`) | Customer ops and whoever is on call | **One business day** for first response; a same-day answer when the site is down | Triage is E1's; diagnosis is E2's |
| **Board** | E1, E2 | Read Monday and Friday, written every Friday | Internal state. Not a customer channel, ever |
| **Repo issues and ADRs** | E1, E2, agents drafting | 24h for a decision; 48h for a non-blocking item | A decision reached in conversation and not written down did not happen |
| **Phone / a call** | Only with a customer, only when they asked for one | — | E1 only. A call with a customer that E2 is not on is an undocumented commitment; E2 joins or it is not a commitment |
| **Demo** | Whoever can run it, ideally not the founder | — | M1-T64 exists so the demo is not founder-dependent |

### 6.2 "We do not build custom for one customer"

`00` §3 names any custom single-customer product as a **permanent anti-segment**.
This is the rule, and the exception path exists because a rule with no exception
path gets broken quietly.

| The rule | In practice |
|---|---|
| No feature exists for exactly one customer | A request from one customer goes through §5 like any other idea: two-question filter, same as a founder's instinct |
| The question is never "is this custom?" — it is "is this the general form of a real problem?" | If three customers would want it, it is a product. If one customer wants it and the rest of the segment would not, it is a configuration or a service, not a feature |
| Configuration is not a custom feature | A site configuration, a rule bundle for that site's topology, a schedule, a policy — these are the product being *used*. A new field in a schema that only one customer sets is a custom feature wearing a disguise, and it goes through §2 row 1 |
| **The exception path** | Exactly one, and it is expensive on purpose: **(a) E1 decides, in writing, with the segment and the reusable form named; (b) the work is scoped and timeboxed like any other work in the current milestone — it does not get its own track; (c) the decision is recorded in the milestone plan with an expiry: if the same need appears at a second site before M5, it is promoted to a feature; if not, it was a service and should have been priced as one; (d) the customer is told it is not in the product, and what they are getting instead.** |
| What is never an exception | Anything that weakens an invariant, adds a dependency, changes a contract, or adds an action type with physical impact. Those are governed by §4 rows 1, 2, 5, 6 and are not available to a customer conversation |

### 6.3 Design-partner cadence

Up to three partners, at no charge, in exchange for a named reference, a measured
before/after investigation time, and access to anonymised incident data
(`01` §M1). That exchange sets the cadence, because the before/after measurement is
the deliverable we actually need.

| Cadence | What | Who | Length |
|---|---|---|---|
| **Weekly** | A short written update: what shipped, what is running at their site, what we are asking for, what we are not doing | E1 | 15 min to write, 5 min to read |
| **Weekly** | Site health read from `/v1/ops/*` and the metrics scrape — backlog, dead letters, drift, evidence disk, degraded decisions | E2 | Automated, reported, not explained |
| **Bi-weekly** | 30-minute working session: one real incident reviewed end to end, from the operator's point of view. Not a demo — a review of something that actually happened | E1, with E2 when the answer is technical | 30 min |
| **Monthly** | Measured number: incidents, degraded rate, evidence completeness, and the investigation-time comparison so far | E1 | Written, attached to the pilot agreement |
| **Per milestone** | Exit-gate evidence pack, in the customer's language, reviewed *with* them before it goes to a third party | E1 | 60 min |
| **On change** | Anything that changes what the site treats as an incident, or any new action the platform can take | E1, before the change | Written, with the simulate diff digest |

### 6.4 What always gets a written response

| Gets a written answer, within one business day | Gets an immediate answer |
|---|---|
| Any feature request | **The site is down** |
| Any "can you also..." | **The site is degraded but up** — backpressure, backlog above the soft limit, dead letters appearing |
| Any question about how the system decided something | **A security disclosure**, received or ours |
| Any pricing, timeline or commitment question | **Data loss or an audit chain break** — stop, seal, and write before doing anything else |
| Any "is this safe / compliant / auditable" | |
| Any request to use their data for anything not in the pilot agreement | |
| Any support question | |

The asymmetry is deliberate. Nothing that is routine waits, and nothing that
threatens the invariants is handled without a written record.

---

## 7. Sustainability at two people

This section exists because the most likely way this company fails is not running
out of runway. It is one person noticing they are the only person who can do
something, in month seven, while also being the only person who can sell.

### 7.1 On-call: what it means with one human

There is no rota because a rota needs two people. The honest version:

| Rule | What it actually means |
|---|---|
| **Primary and secondary are the same person, rotating weekly** | E1 takes week A, E2 takes week B. The on-call human is reachable during business hours, always. Outside them, the site is not down, it is unattended — which is the normal state of a customer site |
| **No 24/7 commitment is made to anyone** | Not to a design partner, not in writing, not "informally". A two-person company that promises 24/7 is promising something it cannot deliver, and the first time it fails it fails with a customer watching |
| **Sev-1 is "the site is not ingesting, or the audit chain is broken"** | Response: acknowledge within business hours, seal a checkpoint if the chain is at risk, and diagnose. The first action is always *seal and stop the bleeding*, never a fix attempt on a live site |
| **Sev-2 is degraded: backlog, dead letters, drift, evidence disk** | Response within one business day, with a written status. These are the SLO remedies in `slo.go`, and the remedy is written down for exactly this reason — an alert with no remedy is an alert everyone learns to ignore |
| **A page out of hours is not a thing that exists** | If we tell a customer "page us", we have created a 24/7 commitment by another name. The words "page", "on call" and "urgent response" are removed from anything we send a customer until there are three engineers |
| **The on-call human's week is half a normal week for deep work** | This is the real cost and the one most likely to be skipped. It is the first thing to be cut under pressure and the first thing that ends the company |
| **Escalation is a conversation, not a page** | The secondary's input is a scheduled 15 minutes during business hours. Slower, and honest about the size of the team |

### 7.2 Vacation and absence

| Rule | Detail |
|---|---|
| **Notice** | Two weeks for planned leave, both people, in the shared calendar, visible to nobody outside the company. Not negotiable in the other direction either — a founder who never takes leave is a single point of failure with a pulse |
| **Coverage is a written handover, not a person** | A customer site running without a human for two weeks is fine if nothing needs a human. The handover is: current state, known issues, who to call if it is genuinely down, and what we told the customer before we went |
| **No milestone gate is signed during a handover week** | Signatures are E1 *and* E2. If one of them is away, the gate waits. This is the correct behaviour, not a bureaucracy |
| **Illness is not leave** | The rule is "if you are ill, you are not working, and nothing is scheduled that requires you". It does not require a handover and it does not require notice |
| **The critical path has an owner and a backup** | Every critical-path item in `02` §4 names one human. If that human is unavailable for a week, the item is re-planned that week, not silently absorbed |
| **Annual, unplanned, uncounted** | Every calendar year, at least one week where neither person is on a milestone. The roadmap is not allowed to plan through it, because that is how a plan becomes a schedule nobody believes |

### 7.3 The rule that survives everything

> **No customer escalation, no security incident, no investor request, and no
> deadline bypasses the invariant list in `AGENTS.md`.**

This is written down because the situations where it is tested are exactly the
situations where it is most tempting to break:

| Situation | What the pressure says | What actually happens |
|---|---|---|
| A design partner is on site in 20 minutes and the adapter is wrong for one event type | "Just map it, we will fix it later" | The mapping is corrected and re-probed, or the event type is not supported. A wrong mapping produces a *silently wrong* incident, which is the failure mode `01` §4 is built to prevent. And it is an `internal/milestone` change, so §2.3's same-day blocking review applies |
| A site's evidence disk is at 4% free during a customer visit | "Delete something" | Run the retention sweep. The dry run is safe. A manual deletion that skips `legal_hold` destroys the only copy of evidence for an incident that may be in dispute. Incident history is asset A1 |
| A customer wants an action taken now, while we are debugging | "It's their site, they authorised it" | The action is not new. An approval-gated action, authorised, logged before and after, through the normal path. Urgency changes the timing, not the authorisation |
| A security disclosure arrives on a Friday afternoon | "Patch it and ship it, no time for the full flow" | `verify-release` → `contract-check` → `make ci` → the control tests. A rushed release that skips a control test is the exact supply-chain event B6 exists to prevent, delivered by us to ourselves |
| An investor or a candidate asks whether we have zero event loss | "It's basically fine" | It is either true, with the fault-injection matrix as the evidence, or it is not. "Basically" is not a value this project reports |
| The 14-day shadow clock is behind schedule | "Log the observations from the harness runs as if they were live" | The clock restarts. The shadow run is 14 **consecutive** days at a site. Faking it makes gate 4 a claim, and a claim about provability is the one failure this company cannot survive |

### 7.4 The early warning signs, for ourselves

Not for a board. For two people, in a repo, in plain sight. Any one of these, for
two consecutive weeks, triggers a scope conversation — not a productivity one.

| Sign | What it actually means | The response |
|---|---|---|
| The same person is the only one who can fix a class of problem | A bus factor of 1 on a critical-path item | Write it down. The next instance of that class goes into §2.3 as an agent's scope |
| `docs/status.md` is more than two weeks stale | The build state and the plan have diverged; the plan is now fiction | Update it. It is the source of truth (`README.md`: "the doc is the source of truth, not this file") |
| The board's "at risk" list has more than three items | Scope exceeds the capacity law | Invoke `01`'s named reduced-scope cut. This week, not at the end of the milestone |
| Nobody has taken leave in three months | §7.2 is being ignored | Take the week. The roadmap is not allowed to plan through it |
| Every conversation is about a customer commitment and none is about the invariants | The company has started selling the claim instead of proving it | Stop selling. Re-read `00` §8. A provable claim is the entire product; an unprovable one loses to free |
| E1 is the only one who can demo | A single point of failure on the commercial side | M1-T64. It is already on the critical path for a reason |

---

## 8. What changes as the team grows

Not a hiring plan. A note on which of these rules were load-bearing for *two* and
which were load-bearing for *one*, so that hiring one person changes the right
things.

| Rule | Why it exists at 2 | What happens at 3+ |
|---|---|---|
| §7.1 on-call rotation | A rotation needs two | A real rota appears, and the "no 24/7 commitment" rule can be revisited deliberately — with a written decision, not by drift |
| §4 "accountable is always a human" | A human is a scarce and reliable reviewer | Unchanged. A third person does not get more authority over a signature, and an agent still does not get any |
| §2.2 no agent writes to `migrations/`, `contracts/`, `internal/contracts/` | Review capacity is one person deep | Unchanged. More engineers means more diffs, not less need for the rule |
| §5 two-question intake | The filter has to be cheap enough to run daily | Unchanged. If the filter needs a committee it is not being used |
| §7.2 vacation | No handover partner | Handover becomes a team norm, and the critical-path owner/backup requirement becomes an on-call rota |
| §3 weekly rhythm | Two people can coordinate by talking | This is the first thing to revisit. A 30-minute Monday planning meeting works for two and does not scale past four |
| R-12 Solutions Engineer | E1 doing it part-time is the M1 shape | A hire, and the first hire (`01` §M2 needs repeatable deployment) |
