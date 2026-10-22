# The milestone decision tickets — M2 … M8

Each ticket states the exit gate, the code deliverables, the external
dependency, and the decision needed **first**. The roadmap remains the
authority (`docs/roadmap/master-roadmap.md`); this is the work breakdown.

These are **tickets, not decisions**. Every "Decide first" below is a question
somebody has to answer and sign, and none of them has been answered here. A
ticket that records an agent's opinion as an answer is a ticket that stops
being a question.

The M1 decisions are in `docs/decisions.md`: D-16 is the auth posture, and the
rest of the document's entries are open.

---

## D-M2 · AI Investigator (2027-04 → 2027-09)

- **Gate:** MTTI reduced by a statistically meaningful margin on supported
  classes, per operator against their own VMS-only baseline; every search result
  and summary carries resolvable evidence references; a replayed incident
  reproduces its summary from stored facts with no model call.
- **Code:**
  1. The **trial log table** — the metrics document names it as "a new table,
     not in the current schema": the ABAB block assignment, arm, order and
     date, so the analysis is reproducible and not in somebody's notebook.
  2. The M2 trial design is already written
     (`docs/roadmap/engineering/metrics-and-analytics.md` §4) and needs the M1
     baseline **variance** to compute N — which the rollup and the report
     deliver.
  3. `internal/investigate` wired to a real product surface.
- **External:** a design-partner site and real operators.
- **Decide first:** the per-class minimum meaningful effect, **before the trial
  opens**, agreed by E1 and E2 and recorded. Moving it afterwards makes it a
  description of the result.
- **Depends on:** the M1 baseline and its variance, and the prior-window
  comparison. Without the rollup there is no baseline, and without a baseline N
  cannot be computed, and the trial cannot be sized.

---

## D-M3 · AI Operator Copilot (2027-10 → 2028-03) — first production GA

- **Gate:** Operator Intervention Rate below an agreed threshold on the bounded
  class set while false-negative and missed-incident rates do not regress;
  every automated action idempotent under replay, proven by fault injection at
  the action stage; an operator can explain from the product alone why the
  system did what it did.
- **Code:**
  1. `internal/threat`'s **playbook moves from the POC into the product
     pipeline** — cooldowns, the escalation budget, `ActionHold` and
     `RequireHumanForCritical` are POC-only today.
  2. `internal/policy` is a *binding*, not an engine — M3 needs the `policies`
     table and the recommendation engine the roadmap calls "the
     highest-consequence new package in the plan".
  3. The auto-resolution state machine, with the fault-injection test at the
     action stage the gate names.
- **External:** the compliance track's ruling on licence-plate recognition (an
  M3 item, blocked on compliance, not on build).
- **Decide first:** the OIR threshold, set in M2 against the M1/M2 baseline.
- **Depends on:** the `operator_override_rate` and `missed_incident_rate`
  companions of the M3 gate. **The first exists in the report today; the second
  does not**, and it is the one the "must not regress" clause leans on.

---

## D-M4 · Universal Security Layer (2028-04 → 2028-12)

- **Gate:** a second VMS adapter in production at one site with a zero
  behavioural non-adapter diff; the Adapter SDK v1 public with a certification
  suite run by someone outside the company.
- **Code:** a second adapter mirroring `internal/milestone` (its own
  `Transport`, its own `RawRecord`, its own route table with `Unverified`
  flags); the Adapter SDK published from the canonical contracts; a conformance
  suite; a source simulator (the emulator already provides a second wire shape
  to certify against); access-control events (credentials, badges, doors,
  granted, denied, forced open, held open — the roadmap calls forced-door and
  held-open "among the highest-value correlations in the whole product").
- **External:** a second VMS vendor's API and a real deployment.
- **Decide first:** which second VMS. The SDK cannot be written before the
  second adapter proves the abstraction holds.
- **Depends on:** nothing directly. It is the largest independent track.

---

## D-M5 · Enterprise Command Platform (2029-01 → 2029-09) — enterprise GA

- **Gate:** a three-site customer running production with the control plane
  pulled mid-incident and no site degradation; OIDC and SCIM tested against
  two real identity providers; an HA failover demonstrated under load with
  measured RTO and RPO.
- **Code:** the largest architectural change in the product. The runtime is
  **one tenant, one site, one process** today. M5 needs a resolved principal
  rather than a configured one, multi-site scope, fleet management,
  RBAC→RBAC+ABAC, HA, and the retention and archival design — the restored
  ledger is provably behind the seal, because a backup is a snapshot of an
  earlier moment, and that gap gets designed around rather than explained away.
- **External:** two real identity providers, a three-site customer, an HA
  database.
- **Decide first:** **this is where the identity package gets its login route.**
  D-16 scopes "no in-app login" to the appliance era (M1–M4). Enterprise
  identity (OIDC/Entra/AD/LDAP/SCIM) is M5, and the local password stack
  `internal/identity` already holds is the fallback for isolated sites.
- **Depends on:** D-16 signed, and the retention design. Nothing else in this
  list touches it.

---

## D-M6 · Site Intelligence (2029-10 → 2030-06)

- **Gate:** a site with baselines, memory and drift detection live; a proposed
  rule with a historical impact diff reviewed and approved by a customer
  operator; and the M6 review confirms the compounding claim.
- **Code:** site baselines; drift detection (camera moved, activity changed,
  lighting changed, a rule became noisy, **override rate rose** — the last one
  needs the override rate and a trend); operator-feedback learning ("every
  disposition becomes evaluation data"); AI-proposed rules with the flow
  proposal → simulation → historical replay → impact diff → human approval →
  activation, and the AI never activates its own rule.
- **External:** a site with enough labelled history for the baselines to mean
  something.
- **Decide first:** the **compounding claim has no metric name yet**. The
  metrics document flags this (§7.2) — a ratio of onboarding cost to marginal
  site cost. It must be named and defined before M6 opens, or the gate is
  unfalsifiable.
- **Depends on:** the rollup and the trends. Drift detection without a trend is
  a guess.

---

## D-M7 · Autonomous Security Operations (2030-07 → 2031-06)

- **Gate:** an A4 site running routine operations on policy for 60 consecutive
  days; the closed loop verified end to end including action-outcome
  verification; the North Star falling with no companion regressing.
- **Code:** the autonomy levels (A1–A4), each a customer decision with a
  documented rollback; the **exception-queue UX** — "not 813 alerts, but 12
  incidents of which 9 were handled automatically, 2 need review and 1 is
  urgent" — which is the evolution of the console's one screen;
  action-outcome verification, so `failed_action_rate` is real; the North Star
  becomes the headline.
- **External:** 60 consecutive days at one site.
- **Decide first:** what "the North Star is falling" means numerically — it
  must fall **continuously**, and the four companions must not regress. The
  threshold is a M2 decision; the proof is a 60-day series, which only the
  rollup can produce.
- **Depends on:** **the rollup is the whole milestone.** A 60-day trend in
  attention minutes per 1,000 events, with its four companions, is a rollup
  table. There is no other way to produce it, and M7 is four years away, which
  is exactly why the rollup has to be built in M1.

---

## D-M8 · Physical Security OS (2031-07 → 2032-12)

- **Gate:** a third-party adapter, built by someone outside the company, passes
  the public certification suite and runs in production at a customer site; a
  signed model bundle installs offline on customer-specified hardware; a
  vertical pack is installed at a customer who was not the design partner.
- **Code:** the public SDKs (Adapter, Event, Action, AI Observation, Rule) with
  webhooks and graph APIs; the marketplace, all signed, all installed offline;
  the model marketplace (approved models as signed offline bundles with a
  hardware profile, an accuracy/speed trade-off and a declared permitted use);
  vertical packs as rules, profiles and playbooks — **not as a fork**.
- **External:** a third party, a customer, and an ecosystem.
- **Decide first:** the SDK's stability promise and its versioning policy.
- **Depends on:** none. It inherits the contracts, the rule engine and the SDK
  surface that M1–M4 produce.

---

## The date that is not a ticket

The roadmap's Tier 2 reduced-scope ladder states that if a real design-partner
site is not available by **2026-12-01**, the M1 date moves and the milestone
re-plans.

That date is roughly thirteen weeks away and **no site is booked**. It is the
one item on this list with a clock on it, and it is not a decision anybody can
take by writing a document: it needs a site, and a site needs a signature.

Everything above M1 in this list — the trial, the second VMS, the three-site
customer, the 60 days — is downstream of that one. The M2 trial's N cannot be
computed without a variance from real operators, and a variance from a
simulator is a number about the simulator.
