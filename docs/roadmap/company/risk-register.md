# Risk Register

Owner: E1 (accountable for the register itself). Status: **live instrument**.
Last revised 2026-10-12.

This is the whole plan's risk register, in one table, scored, owned, and dated.
It is a working instrument: if a row is not reviewed, that row is the bug, not
the register. [`strategy-and-north-star.md`](../strategy-and-north-star.md) §6's capacity law and
[`master-roadmap.md`](../master-roadmap.md)'s dates are the thing it protects.

**Two rules about what belongs here.**

1. **A risk is a thing that has not happened yet.** Once it has happened it is
   an incident ([`quality-engineering.md`](../engineering/quality-engineering.md) §10) and it lives in the incident
   tracker. The register holds the possibility, the signal, and the pre-agreed
   response.
2. **Every row names a person, a signal, a response and a date.** A risk with
   none of those four is a worry, and a worry is not something a two-person
   company can carry — it just shows up as a bad week. §1.4 is the rule and
   §1.3 is the enforcement.

---

## 1. How this register works

### 1.1 Scoring

**Likelihood × Impact, 1–5 each, score = L × I (1–25).** The two are scored
separately on purpose, because conflating them produces a number that moves
when nothing changed.

**Likelihood (L)** — how likely within the review window, which is the row's own
review date, not "ever":

| L | Meaning | Calibration |
|---|---|---|
| 1 | Remote | Would require an unusual conjunction. No precedent in this product's short history |
| 2 | Unlikely | Plausible; has happened in adjacent products or in this team's careers |
| 3 | **Possible** | Could reasonably happen within a year without anyone being surprised |
| 4 | **Likely** | Expected at least once before the next review date unless something changes |
| 5 | Near-certain | Already happening, or has a hard deadline attached |

**Impact (I)** — deliberately the same scale `07` §10.1 uses for defects, and
therefore the same scale we already have practice with:

| I | Meaning | In this product it means |
|---|---|---|
| 1 | Annoying | Costs a day. Nobody's week is ruined |
| 2 | Costly | Costs a week, or a customer's patience |
| 3 | **Serious** | A milestone slips; a quarter of capacity is lost; a customer is disappointed but recovers |
| 4 | **Severe** | A quarter slips; a reference is lost; a Sev-1 is raised; a founder's month is consumed |
| 5 | **Existential** | A real customer's security is compromised, the product's central claim is falsified, a person's safety is affected, or the company does not continue |

**The interpretation bands**, and the rule attached to each:

| Score | Band | What it means operationally |
|---|---|---|
| 20–25 | **Critical** | Named response, pre-agreed, tested. Reviewed **monthly** with both people present. Any change to it is a decision ([`decisions-and-process.md`](../engineering/decisions-and-process.md)) |
| 12–19 | **High** | Named response. Reviewed quarterly at minimum, or monthly if it moves |
| 6–11 | Medium | Named response. Reviewed at its own date, which is at most two quarters out |
| 1–5 | Low | Reviewed at its date. No pre-agreed response required beyond "watch the signal" — but a signal is still required |

**Two corrections to the arithmetic, and they are not optional:**

- **A risk involving a real person's safety or a customer's data loss is
  floored at Critical** (score ≥ 20) regardless of the arithmetic, because the
  response to it is never "monitor it" (§6.3). RISK-F-series and RISK-A-series
  rows reflect this.
- **A risk whose response is an ADR is floored at High**, because an ADR is
  two people having a conversation with a date on it and the cost of being
  wrong compounds.

### 1.2 The fields, and why each one is mandatory

| Field | Why it exists | The failure it prevents |
|---|---|---|
| **ID** | Stable reference in ADRs, post-mortems, and the weekly digest | "the retention thing" |
| **Risk** | Stated as a *condition*, not as a worry: "X happens", not "X is hard" | A worry cannot be responded to |
| **Category** | Drives which review it appears in | Ten critical risks in ten different places |
| **L / I / Score** | Forces an argument about magnitude, and the argument is written down | The biggest risks get the least attention because they are the least urgent |
| **Early-warning signal** | The observable, ideally already instrumented, that tells us it is starting. **A signal nobody can observe is not a signal** | The response starts too late to matter |
| **Owner** | One named person. Not "the team", not "engineering" | Two people, both assuming the other is watching it |
| **Pre-agreed response** | Written **before** it happens, while nobody is emotional. This is the single most valuable field in the register | The 03:00 decision that nobody made in advance |
| **Review date** | A date, not a quarter. "Next quarter" is the same as never | The register becomes an archive |
| **State** | Open / Mitigating / Accepted / Transferred / Avoided / Closed / Realised (§1.5) | A risk that has been handled staying on the list looking live forever |

### 1.3 How a row gets added, and how it leaves

**Who may add a risk: anyone, including an agent.** An agent may add a row and
may not remove one, may not change an owner's response, and may not set a state
other than Open. That asymmetry is deliberate: adding is cheap and the downside
of a spurious row is fifteen seconds; removing is where institutional memory is
lost, and [`decisions-and-process.md`](../engineering/decisions-and-process.md) §4 says the same thing about ADRs.

**The monthly review, thirty minutes, both people, standing agenda item 3**
(the agenda is [`team-and-operating-model.md`](../engineering/team-and-operating-model.md)'s):

```
1  any Sev-1 or Sev-2 security finding open, and any new row it creates
2  the three highest-score rows, one sentence each: has the signal moved?
3  EVERY row: overdue reviews, and rows whose review date has passed
4  new risks proposed since last time, each with its signal and its owner
5  risks that have been Accepted for more than two review cycles
   without movement → re-argue, or close, or convert to a decision
6  the pre-mortems in §5 — is any of their signals already visible?
```

**The movement rule.** A row's score may change in either direction, but a
**decrease requires a stated reason**, and an increase requires a stated
signal. A score that goes down because nobody looked is not a decrease; that is
why item 3 asks about overdue reviews rather than about the scores.

### 1.4 The four-part rule, and how it is enforced

> **Every risk has a named owner, an early-warning signal, a pre-agreed
> response, and a review date.** A row missing any of the four may not be
> discussed, only repaired. A meeting spent debating a risk with no pre-agreed
> response is a meeting where the response gets invented by the person with the
> least context, at the worst possible moment.

Enforcement is blunt: a row without all four is marked **[INCOMPLETE]** in the
state column and is repaired before anything else. As of this revision there
are **two** such rows, RISK-F05 and RISK-O06, and they are repaired in the
November review.

### 1.5 The states, and the four words people confuse

| State | Meaning | What it commits us to | What it costs us |
|---|---|---|---|
| **Open** | We believe it might happen and have not decided what to do | The owner watches the signal and brings it to the review | Attention |
| **Mitigating** | We are spending money or time reducing likelihood or impact | A named action with a date, reviewed at the same cadence | The action's cost |
| **Accepted** | We have decided to live with it, having said so out loud, with the residual stated | The decision is in `docs/threat-model.md` §6 or here, with a named owner and a review date. **Acceptance is re-argued every two review cycles** | The loss, when it comes |
| **Transferred** | Somebody else is carrying it | A named party has accepted it — an insurer, a customer under contract, a subcontractor. **A transferred risk with a party who has not said yes is still Open** | Premium, contract, dependency |
| **Avoided** | We changed the plan so it cannot happen | The thing that would have made it possible is gone, and a test or a review rule now prevents it coming back. "Avoided" is the strongest claim in this table and needs evidence | Design cost |
| **Closed** | It happened | The incident record replaces it. The post-mortem either closes it or replaces it with the *next* risk in the chain, which is the normal outcome | — |
| **Realised** | It happened and was handled | Closed with the incident number attached | — |

**The four distinctions, precisely, because they are used constantly:**

- **Accepted** = we knew, we decided, we will not act. *Mitigated* = we are
  acting. *Transferred* = somebody else is acting. *Avoided* = the possibility
  was removed.
- **Accepted ≠ zero effort.** A critical risk can be Accepted *and* carry a
  monitoring signal and a partial mitigation. "We are not going to prevent it;
  we are going to notice it in time" is a legitimate position and it is
  **Transferred** to our own detection, not Accepted bare.
- **Transferred is a real answer and a common evasion.** Insurance is
  transferred. A customer accepting a risk under a contract is transferred. A
  risk transferred to a party we have not yet signed is **not transferred** —
  RISK-C04 is exactly that shape, and its state is Open for that reason.
- **Avoided is the only state that needs a test.** We cannot prove we mitigated
  something by intending to. We prove it by a control with a test that fails when
  the control is removed (`13` §9.1's discipline, applied to risks). Where a row
  claims Avoided, the register names the test.

### 1.6 Where this sits against the other instruments

| Instrument | Holds | Does not hold |
|---|---|---|
| `13` §9.3's accepted risks | Security risks we have decided to live with, stated to a customer | The business, the runway, the channel, the model provider |
| This register | Everything, including the security rows by reference | The control-to-test map (that is `docs/threat-model.md` §5) |
| `16` §5's pre-mortems | Failure causes per milestone, each with an earliest signal | The live scoring |
| `07` §10 | Things that already happened | Things that might |
| `19` §5's settled decisions | What we already decided and why | What might go wrong |

---

## 2. The register

64 rows. Categories: **T**echnical · **P**roduct/Market · **C**ommercial ·
**S**ecurity · **L**egal/Compliance · **H**uman/People · **F**inancial ·
**O**perational · **A**I-specific · **D**ependency.

> **The count was stale and was corrected on 2026-10-21.** The header said 54
> while the tables held 56: two rows had been added without the count
> following them. A register that cannot say how many rows it has cannot be
> reviewed, because the review asks about *every* row. The eight rows the AI
> VMS strategy added are T-09, T-10, P-07, P-08, C-07, O-07, A-07 and D-05.

State key: `O` Open · `M` Mitigating · `A` Accepted · `T` Transferred ·
`V` Avoided.

### 2.1 TECHNICAL

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **T-01** | The live Milestone endpoint surface does not match the adapter's assumed routes, and it is discovered **late** — after six months of building against fixtures | 4 | 4 | **16** | `securityctl adapter probe` against any real server returns any endpoint or field the adapter does not expect; or a design partner's event feed is empty while the fixtures are green | E2 | **Write `adapter probe` before the first site visit (Q-4, referenced 5× and absent).** Put it in the first design-partner install. If a route contradicts us, the fix is in `routes.go` and the flag comes off — `TestMilestoneRoutesAreDeclaredUnverified` asserts the flag exists so it cannot rot. **If two or more routes are wrong, the reduced scope applies: ship with synthetic fixtures and a demo site, defer live parity to M2, and say so in the release notes rather than pretending** | 2026-11-30 | O |
| **T-02** | CI cannot build the module, so no CI control is demonstrated. `go.mod` says 1.26.0, `ci.yml` pins 1.24 with `GOTOOLCHAIN=local` | 5 | 3 | **15** | Any CI run goes red at `setup-go`; "CI is green" cannot be verified | E2 | **Week 1.** Pin one version in three places and add a CI step asserting `go.mod` against the runner's. Until fixed, **release notes say "CI status unverified" rather than "CI passed"** (`07` §9.3) | 2026-10-31 | O |
| **T-03** | A durability change ships without a `kill -9` test, and a real crash loses or duplicates an accepted event | 3 | 5 | **15** | Any PR touching `internal/durability`, `outbox`, `jobs`, `inbox`, `normalize` or the evidence write path with no fault-injection test in its own diff | E2 | The `07` §5 stage table is the DoD. Stage 8 (action execute) is the highest priority in the whole quality programme and is a **gate on M3's first physical action**, not a follow-up. A change that reaches a customer's site without its stage test is a Sev-1 by `07` §10.1 | 2026-12-31 | O |
| **T-04** | M1 is oversubscribed ~2.5×: the backlog is ~78 engineer-weeks against ~31 of capacity | 5 | 3 | **15** | Any week where the milestone's remaining work exceeds remaining capacity at 60% efficiency | E1 | **`01`'s reduced-scope cut, applied deliberately and early, not on the day.** The cut is items 31–34 and 36–37 (installer, service units) plus the fault-injection harness last. `00` §6: "Scope reduction is the only allowed response to a slip. Not more hours, not a third stream" | 2026-10-31 | O |
| **T-05** | The `obs.Ready`/metrics path is never wired, so no SLO is ever observed and M1's exit gate 3 cannot be signed | 5 | 3 | **15** | `internal/observability` is still imported only by its own test; `/v1/metrics` returns 404 | E2 | Q-3, and it is on the critical path of a milestone gate, which makes it the highest-priority non-feature in M1. Seven call sites plus a handler, and a test asserting each metric is **reachable**, not merely named | 2026-10-31 | O |
| **T-06** | The Python VLM runtime is not written, so M1 exit gate 4 is unsignable and the milestone cannot complete | 5 | 4 | **20** | Any M1 review where the runtime has fewer than 50% of its stories started, with 60 days to the gate | E2 | **Gate 4 is not negotiable and `01`'s reduced scope does not make it skippable.** The real mitigations are: (a) start it now, (b) accept a *smaller* model on *smaller* hardware so the integration is the only variable, (c) if the 14-day shadow run cannot start by 2027-02-01, **say publicly that M1 will not complete on date and move the date** — a slipped date with an honest reason is survivable; a gate signed on a synthetic demo is the end of the company | 2026-10-31 | O |
| **T-07** | A path-handling defect in the evidence store or the archive extractor reaches a customer | 3 | 5 | **15** | Any diff touching a path without the four negatives in its own diff; `TestSymlinkEscapeIsRefused` still skipping on Windows | E2 | `AGENTS.md`'s review rule and `07` §6.4's diff checklist, enforced at review. Q-6 (the Windows skip) is a **M1 deliverable** — a site on a supported platform must not have an unverified escape protection | 2026-11-30 | O |
| **T-08** | A capability leak in the console: a surface renders data its caller is not authorised to read (Q-5, `requirePrincipal` where the API uses `requireCapability`) | 4 | 4 | **16** | Any new console route registered with `requirePrincipal`; a capability test missing for a new screen | E2 | Fix Q-5. Then: **every new surface gets a capability test, not a principal test**, and the rule is written into the ticket template so it is not a memory | 2026-10-31 | O |
| **T-09** | **Camera compatibility is a swamp.** A device claims a capability it was never probed for, or a probe result is trusted for a device it was not measured on | 4 | 4 | **16** | A capability-manifest entry with no probe result behind it; the same model and firmware reported with two different capability sets; a device added to a site with an empty capability set and the console still drawing a live tile for it | E2 | **The manifest is measured per device, and a claim without a probe result is not a capability** — the same discipline as `internal/milestone`'s `Unverified` flag. `device-capability-manifest` is a gate: it fails when a capability is asserted with no probe result behind it. **The bottleneck is the probe, not the adapter code**: with unlimited AI writing device code, the thing that runs out is somebody holding a camera in a lab, so the probe infrastructure and the camera-lab procurement are P0 items and the probe is built in P1 rather than after | 2027-03-31 | O |
| **T-10** | **Recording continuity is the product's promise and its hardest property.** A device shows a gap in a 24-hour run, and the product's central claim is falsified by its own data | 4 | 5 | **20** | `recording_continuity_gap_seconds` above zero on any device; a segment recorded as `state='partial'`; a chain head that does not verify at day close | E2 | **The gap is measured and published, not hidden**, and that is the design rather than a mitigation: the device moves to a visible degraded state, the chain records the boundary, and the gap is *provable* rather than invisible. A partially written segment is recorded as `state='partial'` with its true boundary and is never a silent hole. `media-continuity` is a gate, and a `kill -9` at the segment-write stage is a fault-injection test rather than a nice-to-have | 2027-03-31 | O |

### 2.2 PRODUCT / MARKET

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **P-01** | The three buyer questions in `00` §4 are answered *well* and *believed* by nobody, because the product's value is invisible in a demo | 4 | 4 | **16** | A design partner says "interesting" and cannot articulate the value to their own boss; a demo ends with a question about AI accuracy rather than about provability | E1 | **The demo's climax is the replay, not the detection.** Show a 4-month-old incident reproducing with the model switched off, and show `securityctl audit verify-seals` running with the network unplugged. `00` §2: "A cheaper investigation product loses to free. A provable one does not" | 2026-11-30 | O |
| **P-02** | A VMS vendor ships a competing AI feature — bundled, free with the licence, good enough | 3 | 5 | **15** | A VMS release note announcing AI-assisted event triage; a customer asking "why wouldn't we just use the one we have" | E1 | **The product's answer is a bundle that cannot do this**: the VMS AI does not corroborate across cameras, does not replay a decision from stored facts, does not produce a hash-chained record a compliance officer can verify offline, and is not model-agnostic across a customer's hardware. **The response is not a feature race — it is a comparison document that a reseller can hand to a customer, plus the OEM channel (RISK-C05) so we are inside the stack rather than beside it.** Note the honest asymmetry: they have distribution, we have provability | 2027-01-31 | O |
| **P-03** | The product becomes a "VMS integration" in buyers' heads rather than a layer, and the second adapter (M4) is never funded | 3 | 4 | **12** | Sales conversations that ask "does it replace Milestone?"; a roadmap where M4 slips twice | E1 | `00` §1: "sits **above** the physical-security stack a customer already owns". The demo never shows a VMS feature. **Every VMS announcement is answered with what it still cannot do**, because the answer is the roadmap | 2026-12-31 | O |
| **P-04** | The evidence-intelligence feature (M2) is six months of work that customers describe as "nice" rather than as the thing they needed | 3 | 4 | **12** | M2's MTTI study shows a statistically significant but commercially unimpressive improvement | E1 | M2's exit gate sets "meaningful" **before** the trial, per class, and records it (`01` M2). If the improvement is unimpressive, the finding is that the *class* was wrong, not that the feature was — re-pick the incident classes, do not re-pick the metric | 2027-06-30 | O |
| **P-05** | The exception-queue UX (M7) is worse than the alert list it replaced, and operators route around the product | 3 | 4 | **12** | Operator override rate rising while automation rate rises; "can we go back to the list" in a support request | E1 | `01` M7's reduced scope already has the right answer: "**the exception queue UX before autonomous resolution** — if we ship autonomy with a worse surface than the alert list we replaced, operators will route around us." Autonomy is cut before the surface is | 2030-06-30 | O |
| **P-06** | M6's compounding claim fails — a new customer costs less to onboard than an existing one costs to keep | 3 | 4 | **12** | M3 review data shows onboarding converging to configuration cost with no site-specific asset built | E1 | This is `01` M6's exit gate and it is **falsifiable by design**: "If it does not, the data moat did not materialise and M7–M8 are re-planned as automation on top of search." The pre-agreed response is to **re-plan, not to re-market.** Doing M7 as automation on a search product is a smaller, honest company | 2030-03-31 | O |
| **P-07** | **The sales motion changes from a pilot to an appliance sale.** A VMS is bought like a VMS — off a price list, with an installer, a hardware list and a renewal cycle — and the company has never sold one | 3 | 4 | **12** | A design-partner conversation that turns on the price list rather than on the pilot; a "who does the install" question we cannot answer; a lost deal attributed to procurement rather than to the product | E1 | **The exit gate is unchanged and it is a product gate rather than a sales gate**: the recording continues when the cloud, the network and the model are gone, and every exported evidence item verifies offline. The commercial answer is the *appliance* shape (the control mode in P7, the edge profile in P8) and the pricing model in `pricing-and-packaging.md` §1.1, which is unsigned until a human signs it. **A VMS two people cannot install is not a product, and C-03 already says so** | 2027-06-30 | O |
| **P-08** | **"The product is a VMS" is heard as "the product is a replacement", and the installed base stops buying** | 3 | 4 | **12** | A prospect asking whether we rip and replace; a reseller declining to introduce us because we are a competitor to the VMS they sell; the second adapter slipping because the first customer's own vendor felt replaced | E1 | **Both sources are first-class and the price says so**: a customer who keeps their VMS and reads it through the adapter pays the site fee and no device tier, and the adapter is the cheapest way to buy the product without migrating their recording. The demo shows the adapter working against a live VMS rather than a mock-up of one. D1's reopen trigger is *never*, so this is a response to be executed rather than a question to be re-opened | 2027-06-30 | O |

### 2.3 COMMERCIAL

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **C-01** | **The OEM / reseller channel takes 18 months** and produces nothing, and M4–M5 depend on it for distribution | 4 | 3 | **12** | No signed pilot with a VMS OEM or regional integrator by 2028-06-30 | E1 | **`01` M4 already: "An OEM conversation is not a sale and is not forecast as one until a signed pilot exists."** So the pre-agreed response is that the *forecast* never counted on it: the revenue plan carries the OEM channel at zero until a pilot is signed, and the M5 enterprise plan does not depend on channel distribution. If the channel is not producing by 2029-01, the direct motion is the only motion and the capacity plan says so | 2027-03-31 | O |
| **C-02** | **Every design partner is somebody we know personally**, and none of them is representative | 5 | 4 | **20** | A design-partner reference fails to transfer: a prospect from the same segment says "yes, but ours is different" | E1 | **`01` M2's commercial state already names the answer: "First external customer who is not personally known. The pricing experiment runs here, not before."** The pre-agreed response: the *first* paid pilot at a site we have no relationship with is a **milestone item, not an outcome**, and M2 is not complete without it. And the reference pack must be written so it does not depend on knowing us: a measured before/after number, an audit sample, and a product-artefact demonstration | 2027-06-30 | O |
| **C-03** | The first external customer arrives and the product cannot be installed by two people on a road trip | 4 | 4 | **16** | Any install that takes more than a day, needs a second visit, or requires us to do something by hand | E2 | `docs/status.md` is honest: `install`/`upgrade`/`rollback`/`backup`/`restore` are skeletons. The response is the M1 work, and the measure is a **timed install by a person who is not us** at a site we then never visit | 2026-12-31 | O |
| **C-04** | A risk is treated as Transferred to a customer or an insurer who has not agreed to carry it | 3 | 4 | **12** | Any register row moving to T with a counterparty not named in a signed document | E1 | **The §1.5 rule: a transferred risk with a party who has not said yes is still Open.** The only rows permitted to be T are those naming a signed document. In practice today **no row is T** | 2026-11-30 | V |
| **C-05** | A reseller or OEM requires a private-label build, and it forks the product | 2 | 4 | **8** | A reseller asks for a modified console, a rebrand, or a custom bundle | E1 | Vertical packs carry domain intelligence **as rules, profiles and playbooks, not as a fork** (`01` M8). A private label is configuration and a signed bundle, never a branch. If a reseller will only move with a fork, the answer is no, and the reason is the fork's maintenance cost with two people | 2028-06-30 | O |
| **C-06** | Pricing is wrong and we discover it at the M2 pricing experiment, with three customers already on the wrong price | 3 | 3 | **9** | The M2 pricing experiment produces a conversion rate that does not match the model | E1 | `01` M2: "the pricing experiment runs here, not before." Design-partner deals are free and explicitly marked as not being the price. If the experiment says we are 2× out, we re-price for the next five and honour the first three in writing — retroactively re-pricing a design partner is a reference-destroying move | 2027-06-30 | O |
| **C-07** | **The OEM / reseller channel acquires a reason to block us.** An adapter that reads a vendor's VMS and a recorder that competes with it are now a threat to a channel that used to see us as a complement | 3 | 4 | **12** | A reseller asking for exclusivity terms we did not offer in 2026; a vendor's roadmap naming "third-party recorder" as a competitive category; a certification request that arrives with a deadline attached | E1 | **The certification suite is the answer, and it is built before the channel needs it.** A certified adapter is a thing a vendor's own customer can rely on, and a certification requirement is a moat a vendor cannot ship in a release note. The no-fork clause and the attribution clause are unchanged, and they are what make the SDK safe to publish at all. C-01's pre-agreed response stands unchanged: the forecast never counted on the channel, so a channel that blocks us costs a forecast that was already zero | 2028-06-30 | O |

### 2.4 SECURITY

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **S-01** | **A security researcher finds the tenant-scoping path** — a handler that decodes `tenant_id` from a body, or an RLS escape | 3 | 5 | **15** | The static route walk (the missing test in `13` §9.2) is still unwritten; a code review comment about tenant context; a new handler taking a tenant from a query or body | E2 | **Write the static route walk this month** — a static analysis of the route table asserting no handler decodes a tenant from a request body. It is the highest value-per-hour security test in the M1 backlog. Then: any new handler gets a tenant-scoping test, and the answer is one of the release gate's 20 lines | 2026-10-31 | O |
| **S-02** | **A control is documented in three places and absent from the code** — and a customer reads the document | 5 | 4 | **20** | Any control in `docs/threat-model.md` §5 whose named test does not exist or does not fail when the control is removed | E1 | **Quarterly audit, both people, 90 minutes: take the 35 controls, find the test for each, and delete or fix any row that does not have one.** Three are already known — per-source login rate limiting, WebAuthn signature verification, TOTP algorithm — and each is fixed or the document is amended in the same month. **A control with no failing test is a claim, and a claim in a document we hand a customer is a misrepresentation** | 2026-11-30 | O |
| **S-03** | The release signing key is lost or misused, and no site can verify any release | 2 | 5 | **10** | The key store is on a machine that is also the daily build machine; no rotation has been rehearsed | E2 | `12` §1.3: two keys from the start, rotation before M3 GA as a minor release, the previous key's signature remaining verifiable for a stated period. **And SC-5: `build-release` must refuse to generate a key**, because a careless build today produces a release signed by a key whose public half exists on one laptop | 2026-12-31 | O |
| **S-04** | A malicious or buggy adapter at a customer site gets our service account's reach (M4 and later) | 4 | 5 | **20** | The Adapter SDK's public release without a declared permission set per adapter; a conformance suite that checks output shape only | E1 | **`13` §10.3's B8: "what is the blast radius of a malicious adapter?" If the answer is 'the service account', the SDK needs a sandbox before it is public, not after.** The M4 exit gate should include that question as a signed item | 2028-06-30 | O |
| **S-05** | The release archive, the model bundle, or a customer-supplied rule bundle is a supply-chain attack vector we do not detect because we have not produced a real SBOM | 3 | 5 | **15** | `sbom.spdx.json` is a declared path with no generator; `Provenance.SourceDigest` ships empty | E2 | CI-8 and SC-1..SC-3 in `13` §3.2. **The SBOM is the mechanism that makes "we checked the licences" checkable rather than assumed**, so it is not a paperwork item | 2026-11-30 | O |
| **S-06** | A security researcher reports a vulnerability and we mishandle it, damaging a reputation we cannot spend | 3 | 4 | **12** | Acknowledgement takes more than 2 business days; any argument with a reporter; any public post before the customer is told | E1 | `12` §10.4 in full: acknowledge in 2 business days, never threaten a researcher, no name sharing without written permission, updates at least every two weeks even with no news, the fix before the advisory, and 90 days with no threat of litigation. **And build the intake** (`13` §8.1) — an unmade promise to a researcher is worse than no promise | 2026-11-30 | O |

### 2.5 LEGAL / COMPLIANCE

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **L-01** | **The legal exposure of a missed real incident** — a customer suffers a loss and our product did not raise it | 2 | 5 | **10** | Any Sev-1 that involves a missed event rather than a broken record; a support request phrased as "we missed something" | E1 | **A real missed incident is a Sev-1 the moment anybody says so, until disproved** (`12` §7.1), and the response is the `07` §10.3 protocol: tell the customer at T+0.5h before we understand it. The **legal** mitigations: the product never claims to detect everything (the release notes name the limitations), the customer's own VMS keeps working when we are down, and the contract allocates liability honestly rather than promising a detection rate we have not measured. **We are also two people and cannot carry professional indemnity insurance for a product in a customer's critical path without pricing it, and pricing it belongs in the M3 model** | 2027-03-31 | O |
| **L-02** | We drift into biometric processing without deciding to — a face-blur feature, "find the same person", an LPR feature promoted from shadow | 4 | 5 | **20** | Any feature request using the words face, person, plate, "find the same", "reduce privacy"; any proposal to promote `vehicle-at-gate` out of shadow | E1 | `14` §3.1's list. **DPIA-lite questions 7 and 8 must be answered from the schema, before the migration.** `schemalaws.TestNoEntityAliasTableInM1` is the structural version of this. And the one-sentence position we give a customer: *"We analyse images. We do not perform biometric identification."* | 2026-11-30 | O |
| **L-03** | EU AI Act scope is wrong, and we told a customer we are out of scope | 3 | 4 | **12** | An EU customer asks; an OEM in Europe asks; counsel has not been engaged | E1 | **REQUIRES LEGAL ADVICE.** `14` §3.3's four questions, in order, from a named firm, target date 2027-06-30. In the meantime we build the artefacts, because they are needed under every answer — the AI disclosure package is most of a provider's technical-documentation obligation | 2026-11-30 | O |
| **L-04** | A customer cannot answer a data-subject request with our product, and we have no DSAR workflow | 4 | 3 | **12** | The first regulated customer asks "how do I find everything you hold about this person?" | E1 | `14` §8.3: a query surface, a consent-aware redacted export, an audit record of the search, and a documented statement of what we do *not* have to find — stated in advance rather than discovered under a deadline. M3 work, before a regulated customer, not M5 | 2027-06-30 | O |
| **L-05** | Our certifications make claims we cannot support — a proposal converts an audit into an approval | 2 | 4 | **8** | Any customer-facing document that uses "certified" to mean approved, or any statement about a jurisdiction we have not asked about | E1 | `14` §1.2's rule: build (1) completely and (2) honestly, and **never describe (2) as (3)**. The one sentence to have ready is in §1.2 and it is a **release-gate line** — E1 reviews the notes and an unsupported claim in them is a security finding, not a typo | 2027-01-31 | V |

### 2.6 HUMAN / PEOPLE

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **H-01** | **Single-engineer bus factor on every critical path** — one of the two is unavailable and the work stops | 4 | 4 | **16** | Any work item where only one person could do it, is doing it, and has been for more than a week | E1 | `00` §6's constraint is permanent; the response is structural. **Every work item names a second person or names why there is none.** Every hard-won fact is in `docs/` (`docs/status.md` is the source of truth, not the commit history). A Sev-1 uses the two-person protocol so the incident commander does not write code. And the runway risk (F-01) is the real mitigation — a company that cannot afford both founders is a company with a bus factor of one regardless of documentation | 2026-10-31 | O |
| **H-02** | A 24/7 support expectation is created before a rota is staffed | 3 | 4 | **12** | A customer asks for 24/7; a contract draft contains an SLI we cannot staff; a founder is woken by a non-Sev-1 | E1 | `12` §7.3: "Until that rota exists, we do not sell 24/7. We would rather decline a deal than sign one." Business hours, written down, in the design-partner agreement, in those words | 2027-06-30 | O |
| **H-03** | Burnout in a two-person company during a hard milestone | 3 | 4 | **12** | Two consecutive weeks of 70+ hours; a slipped gate that is accepted without a reduction; a founder taking no holiday for two months | E1 | [`team-and-operating-model.md`](../engineering/team-and-operating-model.md)'s rota, and the honest constraint that the 40% tax is not optional. **The pre-agreed response to burnout is a scope cut, not a sprint.** And the 07 §10.3 rule that the incident commander does not write code applies to ordinary weeks too, not only to incidents | 2026-11-30 | O |
| **H-04** | The AI agent introduces defects faster than two people can review them | 4 | 3 | **12** | Review queue depth rising; a merge nobody can explain; a test that exists but asserts nothing; a test that has never been seen to fail | E2 | `07` §11.2's hard line, which is `19` §4's rules of engagement: every generated test is read by a human who can answer "what does this fail on"; mutants are the tool, not the scoreboard; an agent may not touch a `green` marker. The measurable signal: **the rate at which we re-read a diff and find something wrong rises** | 2026-11-30 | O |

### 2.7 FINANCIAL

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **F-01** | **The founders' runway expires before GA** — 24 months of funding against a plan whose first paid cohort is M2 and whose first GA is M3 (2028-03-31) | 3 | 5 | **15** | Runway below 9 months; a month where revenue does not cover any part of cost; a founder discussing "a smaller version of this" | E1 | **Decide the trigger now, in writing: at 9 months of runway, the milestone plan is re-cut to what the remaining money buys, and the reduced scope is applied immediately rather than negotiated.** The alternatives, in order of preference: (a) a bridge or an angel round earlier than feels comfortable — at 12 months, not 6; (b) revenue earlier via the design-partner-to-paid conversion `01` M2 already names; (c) reduce the plan. **What we do not do is discover this in month one of being broke** | 2026-11-30 | O |
| **F-02** | **Evidence storage cost growth exceeds what a site budgets for**, and a site fills its disk in month three | 4 | 3 | **12** | `evidence_disk_free_percent` breaching; a customer asking for help with disk; storage cost per site exceeding the licence | E2 | `12` §9.1: "**The evidence number is a policy decision, not a calculation**" and "the rule pack, not the camera count, is the variable that moves this by an order of magnitude." The response is sizing honesty (a formula with a measured constant, not a number), the `RUNBOOK-disk-full.md` dry-run-first procedure, and a commercial conversation about storage at contract time rather than as a support incident | 2027-03-31 | O |
| **F-03** | Compliance cost arrives faster than revenue and consumes the capacity the product needs | 4 | 3 | **12** | A quarter where security/compliance exceeds the 0.5 engineer-day/week estimate | E1 | `14` §1.4's arithmetic, and the order in `14` §1.3: **the customer's own questionnaire first, because it is the cheapest work and the most likely to pay.** If the estimate is exceeded, the SOC 2 observation window lengthens rather than the evidence thinning | 2027-01-31 | O |
| **F-04** | An external pen test finds something that requires a redesign rather than a fix | 2 | 4 | **8** | The scoped architecture review (`14` §2 item 3) surfacing a structural concern rather than a bug | E2 | **Do the cheap scoped review in M1, a year before the full pen test, precisely so that a structural finding arrives while M2's design is still movable.** A finding that needs a redesign is worth 10× more in M1 than in M3, and the cost of acting on it is a quarter instead of a replan | 2027-03-31 | O |
| **F-05** | **[INCOMPLETE — no pre-agreed response written]** SOC 2 audit fees or scope grow materially beyond the estimate, or the observation window must extend | 4 | 3 | **12** | The readiness gap assessment returning more than 15 exceptions; a firm quoting a scope with a second audit period | E1 | **To be written at the November review.** The provisional position, recorded now so the November review has something to argue about: the §2 chain is dated backwards from the report, so the response to growth is **a later report, not a thinner window.** What is not negotiable is that the report says what the evidence supports | 2026-11-30 | O |

### 2.8 OPERATIONAL

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **O-01** | A restore leaves the audit ledger provably behind the seal and nobody notices for more than one shift | 3 | 4 | **12** | `TestRecoveryDrill` stops finding the gap; a site's `SEQ_LEDGER < SEQ_SEALED` with no reconciliation record; a support request about a restore | E2 | `12` §6.2's nine-step reconciliation, which is a procedure because it will be done at 04:00. And three commitments: **never report a restored site as healthy; never silently re-seal over a gap; never tell a customer their ledger is fine because the chain verifies** | 2026-12-31 | O |
| **O-02** | The DR story's RTO/RPO are quoted to a customer before they are measured | 3 | 4 | **12** | Any proposal, contract or deck containing an RTO or RPO; the words "up to 1 hour" in a slide | E2 | `12` §6.3: they are **ESTIMATE**s from a sizing calculation and are not stated to a customer until measured on a build of that size. `README.md`'s evidence rule applies to us first. The response when asked before then: "we publish measurements, not targets" | 2027-03-31 | V |
| **O-03** | The Windows Service and systemd units never land, so a site has no supported way to start the product at boot | 4 | 3 | **12** | The first customer install requiring a manual start; `deploy/` still absent at 2027-01-31 | E2 | Q-8. `12` §3.6: "nothing else depends on it" stops being true the moment there is a customer, and that is now. Genuinely small; it is M1 work with a date on it | 2026-12-31 | O |
| **O-04** | An upgrade reaches a site half-applied and cannot be rolled back | 2 | 5 | **10** | Any release bundling an expand and a contract migration; a rollback path that requires a restore; `securityctl upgrade` refusing without a verified backup | E2 | `12` §1.1 and §2.3.2: a patch never contains a contract migration; step 3 refuses to proceed without a verified backup taken since the last release; rollback is a pointer flip, and a release that cannot roll back is a major. `TestContractStepAlongsideExpandIsRefused` is the automated half | 2026-12-31 | O |
| **O-05** | A dead-letter storm or a poison message blocks the queue and the site stops seeing incidents | 3 | 4 | **12** | `dead_letter_count` rising; `job_queue_depth` growing with no progress; an operator reporting "nothing is coming through" | E2 | `RUNBOOK-dead-letters.md`, the refusal-not-drop design (a dead letter is an accepted event the platform could not process, and the reason is on it), `TestOutboxIsolatesAPoisonMessage`, and Q-2's honesty fix: the SLO must measure what it claims to measure or be restated to what it does | 2027-01-31 | O |
| **O-06** | **[INCOMPLETE — no early-warning signal]** A runbook step names a command that does not exist, and an operator loses time at the worst moment | 4 | 3 | **12** | — | E2 | **To be written at the November review.** The provisional position: `TestDrillRunbookNamesRealCommands` exists because this happens, and it must be **generated from the command tree rather than hand-maintained**, or it cannot catch the case it exists for — which is exactly how Q-4 (`securityctl adapter probe`, referenced five times, does not exist) survived | 2026-11-30 | O |
| **O-07** | **The recorder stops recording and nobody notices for a shift.** A crashed recorder, a full disk, a lost lease or a poisoned device degrades the recording path quietly, and the product keeps serving the index of a recording that stopped | 3 | 5 | **15** | `recording_continuity_gap_seconds` non-zero for a whole shift; the recorder's own health signal not scraped by anything; a device's segment sequence that stops advancing | E2 | **The recorder is a separate process with its own failure domain, and it is monitored as one.** A recorder crash must not stop event ingest, and an ingest crash must not stop recording, so the two health signals are separate and both are published. `disk-full` is a gate: the recorder degrades *visibly* rather than corrupting the index. The recorder cannot write the schema — `recorder-schema-isolated` is a gate — so a corrupt index is a query failure rather than a migration | 2027-03-31 | O |

### 2.9 AI-SPECIFIC

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **A-01** | **The model's false-negative rate in the field is materially worse than the evaluation**, and a real intrusion is not raised | 3 | 5 | **15** | Disagreement rate rising at a site after promotion; operator override rate on AI-influenced incidents rising; a site where AI-driven incidents are consistently dismissed | E2 | **The North Star's four companions are the control** (`01` M7): missed-incident rate, operator override rate, false escalation rate, evidence completeness, and **the North Star is never reported without them.** Concretely: per-class and per-site disagreement tracking, `TestAbsentObservationIsNeverFoldedIntoARate` so the measurement cannot flatter itself, and a **demotion path** — a profile whose site-specific disagreement exceeds the gate is rolled back to the prior bundle, which is a pointer move, not a release. The uncomfortable truth we must say to a customer: our own false-negative rate is a number we measure and report, and the reason a corroborated incident needs two independent signals is that we do not trust one | 2027-06-30 | O |
| **A-02** | **The model provider changes its price, its availability, or its licence terms** mid-term | 3 | 4 | **12** | A provider announcement; a rate-limit or deprecation notice; a licence change in the model card; a model going from an open weight to a hosted-only API | E1 | **Three defences, in order: (a) model-agnostic by design** — `01` M7's "the core stays model-agnostic; the customer's hardware and risk tolerance choose the model", which is the whole reason the abstraction layer is the asset; (b) **the model lives on the customer's hardware**, so a provider change is a decision we decline to adopt, not an outage we absorb; (c) a second evaluated model per profile, so a profile's evaluation is not a single point of dependency. And a contractual one: `14` §8.4's **AI-8** — notice with a right to remain on the prior version. **If a provider moves to a hosted-only API, that is a decision point, not a migration, because it would take inference off the customer's hardware and break the residency posture** | 2027-01-31 | O |
| **A-03** | A model or prompt change ships without re-evaluation, and a decision months later cannot be explained | 3 | 5 | **15** | A diff touching a model, a prompt template, a prompt version, a profile schema or a dataset; a stored `output_hash` that no longer recomputes | E2 | `07` §7.5's contract, verbatim: **any change to those five things forces step 2 (evaluation) and step 5 (14-day shadow). There is no diff size below which this is skipped, and no emergency release that bypasses it.** The hard test: `TestOutputHashIsStableAndRecomputable` re-run against already-stored observations — if a change makes a stored hash unverifiable, invariant 2 is falsified and the change does not ship | 2026-11-30 | O |
| **A-04** | A model bundle on a site disk is swapped or tampered with | 3 | 4 | **12** | A bundle whose signature does not verify at install; an observation whose `ModelBundleSignature` does not match the installed bundle | E2 | A tampered bundle is rejected before any inference (`docs/threat-model.md` A4.3). What is **not yet built** is the installer, and it is a gate on the Python runtime's delivery date, not a follow-up (`13` §3.7). In the meantime the compensating control is the **grant**: the AI runtime's role can insert observations and read evidence and *nothing else*, so a lying model degrades verification rather than forging it | 2026-12-31 | O |
| **A-05** | The AI plane takes a site off the availability path — a readiness probe, a dependency, or a resource grab | 2 | 5 | **10** | Any readiness check that fails because of the AI runtime; a `security-core` restart caused by GPU-host pressure; a degraded incident count that is a *dropped* incident rather than an unverified one | E2 | The grant-level and code-level controls already exist: `/v1/health` excludes the AI plane by name, `/v1/health/ready` exempts a check called `aiRuntime`, and `TestBuiltInBundleRunsWithNoAIObservationAtAll` is the test. **The residual is a manual run with the model stopped at every milestone gate**, because a test suite cannot prove that no other code path consults the model on the availability path | 2026-11-30 | O |
| **A-06** | The AI governance record stays incomplete — no approved use cases, no known limitations, no deployment history — and the trust story is unverifiable at exactly the moment it is needed | 5 | 4 | **20** | The model/profile registry table does not exist by 2027-03-31; an observation with attribution but no evaluation report reference; a customer asking "what are this model's known limitations?" and the answer requiring an email | E1 | `13` §4.1's table: the first five attribution rows are implemented and enforced; **the last four are the gap and they are the four a regulator, a customer's risk committee and the M8 marketplace all require.** Approved use cases must be a **closed vocabulary of incident classes**, not free text, because a free-text use-case field becomes a marketing sentence. This is the trust story's load-bearing wall and it is currently unbuilt | 2027-03-31 | O |
| **A-07** | **Cloud egress becomes the default by accident.** A site configured `local_only` sends a frame, a credential or a plate to a hosted provider, and the posture the whole product is sold on is gone in one request | 3 | 5 | **15** | Any row in `ai_egress_log` on a `local_only` site; a provider adapter reachable from a site whose policy names no provider; a new outbound destination in the AI runtime that no policy names | E2 | **`local_only` forbids all outbound traffic at the runtime, not only in policy**, and `ai-egress-policy` is a gate that fails the build on a single offending row. The egress table is append-only and audited, and every egress records the digest of exactly what left the site. The same shape as the embedding prohibition: `internal/embedaudit` is extended to the egress record, so person, plate, face-crop and credential regions cannot leave even if a policy says they may | 2027-03-31 | O |

### 2.10 DEPENDENCY

| ID | Risk | L | I | Score | Early-warning signal | Owner | Pre-agreed response | Review | St |
|---|---|---|---|---|---|---|---|---|---|
| **D-01** | A transitive dependency in the Argon2id or TLS path is compromised upstream | 2 | 5 | **10** | `govulncheck` reporting a reachable finding; a `golang.org/x/crypto` advisory; a Go security release | E2 | Nine modules today, and a small number is the policy (`13` §3.4). `govulncheck` reads the call graph rather than the module list, so it is the control that matters rather than a version diff. **A `x/crypto` bump is an ADR, not a patch**, and `identity.VerifyPassword` refusing a weaker stored hash means a change that altered encoding is an availability event for every account | 2026-11-30 | O |
| **D-02** | A dependency breaks a six-month-old isolated install — the exact regression `00` §6 names | 3 | 5 | **15** | Any proposed dependency that needs a registry, a proxy, a runtime download, or a licence check; an install rehearsal that needs the internet | E2 | The rule, and it is the whole test: **does a six-month-old release still install on an isolated network?** The measurable control is a **quarterly install rehearsal from a release artefact on a machine with no network**, which is also the DR drill we already owe (`12` §6.4) | 2027-01-31 | O |
| **D-03** | The floating toolchain makes the build non-reproducible, so a rebuilt release is not the release we shipped | 5 | 3 | **15** | `protoc-gen-go@latest`, golangci-lint `version: latest`, a floating `postgres:16` tag, or the three-way Go version conflict (T-02) | E2 | CI-4, CI-5, CI-6 and T-02, all week 1–2. And `13` §3.5: define "reproducible" as the same inputs producing the same **file set and hashes**, and publish a content digest over the signed payload excluding timestamps, so two sites can compare "did you get the same product I did" | 2026-10-31 | O |
| **D-04** | The Python VLM runtime's dependency tree is a supply-chain hole at every customer site | 4 | 4 | **16** | The runtime design does not state how its dependencies are vendored and pinned; any `pip install` at start time | E2 | `13` §4.5: dependencies pinned, **vendored into a signed model bundle**, and covered by the SBOM. **A runtime that pip-installs at start is a runtime an air-gapped site cannot run, and a hole at every site at once.** It is a design requirement on the runtime's first day, not a hardening item afterwards | 2026-12-31 | O |
| **D-05** | **ONVIF is the first new protocol family since the stack was fixed**, the camera lab is not procured, and the conformance tool is not in the gate | 4 | 4 | **16** | A device adapter written against a published document rather than against a device; no camera in the building; ONVIF membership not applied for; a conformance run that is manual and unrepeatable | E2 | **ONVIF membership with their conformance tool in CI is the answer, and it is procurement rather than engineering** — it has the longest lead time in the plan and it starts in P0. In the meantime the discipline is the `Unverified` one: a route is `Unverified` until a probe says otherwise, and the probe infrastructure is built in P1 rather than after. "Certified, not compatible" is a P9 claim and it is not claimed before the tool runs | 2027-03-31 | O |

---

## 3. Top risks by expected damage

Ordered by **expected damage** — score × how much of the company it takes with it
— not by score alone. A score-15 risk that costs a fortnight is below a
score-12 risk that costs the company its reference in a regulated market. This
ordering is a judgement and it is labelled as one.

> The list held ten entries until 2026-10-21 and holds eleven now, because the
> AI VMS strategy added a row that belongs in it rather than beside it. A
> "top ten" with a forced eleventh is a top eleven with a misleading heading,
> so the heading says what the list is.

---

**1 · A-06 — The AI governance record stays incomplete.** *L5 × I4 = 20, Critical*

Every other thing in this register is survivable because the product's central
claim is provable. This one attacks the claim's *content* rather than its
mechanism: we can prove that a decision was made and replay it, and if we
cannot say which model made it, under what approved use, with what known
limitations, and where it has been deployed, then "provable" means "we stored a
structured field called `restrictedZoneBreach`". A customer in a regulated
vertical asks the risk committee's two questions — "what is this allowed to do"
and "what happens when it is wrong" — and the honest answer is a manual email.
The mitigation is unglamorous and cheap: a registry table, a closed vocabulary
of approved use cases, a known-limitations list with a mitigation or "none
known" per entry, and a deployment history. It is five M1/M2 stories. The reason
it ranks first is that it is the only row on this list where **the longer we
wait, the more expensive it gets**, because every profile deployed without a
registry entry is a migration later, and every month of M2 pilot data collected
without an approved-use-case declaration is data we cannot honestly say was
collected for a permitted purpose.

---

**2 · L-02 — We drift into biometric processing without deciding to.** *L4 × I5 = 20, Critical*

Illinois BIPA carries a per-violation private right of action and a $1,000,000
penalty for wilfulness or negligence as against a minor; Washington's has no
cap. The exposure is not that we would do biometric identification — we
explicitly do not and have a test for it — it is that a reasonable, individually
sensible feature ("blur faces on export", "do not show this camera to
contractors", "find the same person in the other footage") turns a
non-biometric product into a biometric-processing one, and by the time anybody
realised, the product had been sold three times into the EU and once into
Illinois. The response is a **single rule with teeth**: any feature request
containing face, person, plate, "find the same" or "reduce privacy" stops and
clears DPIA-lite questions 7 and 8 *answered from the schema before the
migration*, plus legal advice, plus the shadow-then-production gate. The
`vehicle-at-gate` profile with its `plateLegible` and `plateText` fields sitting
in the M1 shadow set is the concrete test case: it is a licence-plate reader
today, and promoting it is a decision, not a config change.

---

**3 · C-02 — Every design partner is somebody we know personally.** *L5 × I4 = 20, Critical*

Three M1 design partners, all reachable on a phone, all willing to give time
instead of money, all impressed by the founder who built it. That is the correct
way to build a trust spine and it produces **zero information about whether a
stranger buys this**. The failure mode is quiet and expensive: M1 and M2
complete on time, the references are glowing, the pricing experiment produces
exciting conversion numbers from people who like us, and then the first cold
prospect says "we'd need to see SOC 2, a pen test, and a reference from someone
we also don't know" — and we discover that the entire commercial foundation was
built on relationships that will not transfer. `01` M2 already names the
mitigation and it must be treated as a **milestone item rather than an
outcome**: the first paid pilot at a site we have no relationship with, with a
reference pack that does not depend on knowing us — a measured before/after
number, an audit sample a customer can verify themselves, and a product
demonstration they can run with our servers switched off.

---

**4 · S-02 — A control is documented in three places and absent from the code.** *L5 × I4 = 20, Critical*

Three already exist and were found by reading the tree rather than the docs:
per-source login rate limiting is claimed in `docs/threat-model.md` A3.5, in the
OpenAPI description, and as a config field — and there is no implementation; the
WebAuthn assertion signature is checked by a "platform verifier" that does not
exist; the TOTP algorithm does not match its own enrolment URI. The damage is
disproportionate to the size of the bug, because **a control documented in a
document we hand a customer is a claim we have made, and a claim we have made
and cannot support is a misrepresentation by a company whose entire product is
not making unsupported claims.** A security researcher who finds one of these
does not report a bug; they report that our documentation is not true, and that
is a different conversation with a much worse outcome. The response is a
**quarterly 90-minute audit of all 35 controls — find the test, and delete or
fix any row that does not have one** — plus the rule that a control without a
test that fails when it is removed is not a control. It is the cheapest
high-value discipline in this document.

---

**5 · T-06 — The Python VLM runtime is not written, so M1's gate 4 is unsignable.** *L5 × I4 = 20, Critical*

The Go side is complete: profiles declared, outputs schema-validated,
observations persisted with mandatory attribution, shadow marking, promotion
gates. The runtime is a Python service that does not exist. So M1's fourth exit
gate — a production profile passing its gates on a real GPU, in shadow, for 14
consecutive days at a design-partner site — **cannot be signed**, and `01`'s
reduced scope explicitly does not make it skippable: "if it cannot be met, gate
4 is not met and M1 has not finished. That is uncomfortable and it is correct."
Three mitigations, in order: start now; reduce the model and the hardware so the
integration is the only variable; and if the shadow run cannot start by
2027-02-01, **say publicly that M1 will not complete on date and move the date.**
A slipped date with an honest reason is survivable. A gate signed on a synthetic
demo is the moment the company's central claim stops meaning anything, and no
later amount of engineering recovers it.

---

**6 · S-04 — A malicious or buggy adapter gets our service account's reach.** *L4 × I5 = 20, Critical*

M4's exit gate requires "the Adapter SDK v1 is public with a certification suite
that has been run by someone outside the company". The moment the SDK is public,
third-party code runs inside our process, with our service account, on a
customer's site, on a LAN that `docs/threat-model.md` §2 B1 correctly calls
untrusted. Everything the threat model built — the scoped database role, the
RLS, the audit ledger — protects against an attacker who has our credentials. A
hostile adapter **is** our credential holder, by design. The question the M4
review must answer in writing is *"what is the blast radius of a malicious
adapter?"* and if the answer is "the service account", the SDK needs a process
boundary, a declared permission set per adapter, a conformance suite that
checks security properties rather than output shape, and a revocation story
**before it is public**. M8's marketplace multiplies this by the number of
publishers, and M8's own reduced-scope instinct — "one real third party beats
fifty unreviewed uploads" — is the correct medicine.

---

**7 · T-10 — Recording continuity is the product's promise and its hardest property.** *L4 × I5 = 20, Critical*

This row exists because of the 2026-10-21 strategy change, and it is the
sharpest new risk in the register: **the thing that differentiates the product
is the thing most likely to be visibly wrong.** Every other risk on this list
is about the intelligence being wrong; this one is about the *record* being
wrong, and the record is what the customer buys when they buy a VMS. A buyer
who finds a gap in our recording on their own site does not ask for a root
cause, they ask why they bought this instead of the one their installer
recommended — and unlike a missed alarm, a gap in the recording is not
arguable, because we published it ourselves in
`recording_continuity_gap_seconds`. The design answer is not "never have a
gap", which is a promise nobody can keep across a power cut and a disk; it is
that **the gap is measured, published, chained and bounded, so the question a
buyer asks is answered by a number we produced rather than by an excuse we
invent.** That only works if the measurement is honest, which is why
`media-continuity` is a gate rather than a metric, why a partially written
segment is recorded as `state='partial'` with its true boundary rather than
discarded, and why a `kill -9` at the segment-write stage is a test rather
than a hope. A VMS that hides a gap is a VMS nobody trusts with the one thing
they bought it for.

---

**8 · P-02 — A VMS vendor ships a competing AI feature.** *L3 × I5 = 15*

They have distribution we will not have, they have the camera relationship, and
the feature lands free in a licence the customer already pays for. The strategic
answer is not a feature race — we lose that race — it is that **the four things
they cannot do are the four things this product is for**: their AI does not
corroborate across cameras, does not replay a decision from stored facts, does
not produce a hash-chained record a compliance officer can verify with the
network unplugged, and is not model-agnostic across the customer's own hardware.
That is a defensible position today and it needs a concrete artefact: a
comparison document a reseller can hand to a customer without embarrassment, and
the OEM channel (C-01) so that we are inside the stack rather than beside it.
The honest asymmetry, which should be said out loud in the room: they can ship
faster, they can bundle, and we can be right for longer.

---

**9 · T-01 — The live Milestone endpoint surface does not match our assumptions, discovered late.** *L4 × I4 = 16*

Every route in `internal/milestone/routes.go` is marked `Unverified`, no
Milestone server has ever been reachable, and the fixtures are synthetic. The
adapter is the only component that talks to the customer, and it is the one that
has never touched the real thing. The failure mode is not a crash; it is an
**empty event feed on a real site** while every test is green, discovered in week
three of a pilot, which looks exactly like a product that does not work. The
mitigation is small and already designed: `securityctl adapter probe` lists what
the server actually returns next to what the adapter expects, and the `Unverified`
flag is a field rather than a comment precisely so that it can come off, with
`TestMilestoneRoutesAreDeclaredUnverified` asserting it exists so it cannot rot.
That command does not exist. It is referenced in five places including "the
first thing to run on a real install". Writing it is a week.

---

**10 · A-01 — The model's false-negative rate in the field is worse than the evaluation.** *L3 × I5 = 15*

The evaluation measures a dataset; the field is a different dataset, and the
difference is where a missed intrusion lives. A profile evaluated at 0.70 recall
on a curated set can be materially worse on a site's actual lighting, camera
angles, weather, and a person who knows they are being watched. We are
deliberately conservative here — corroboration requires two independent signals
and a single source cannot corroborate itself — but the honest position is that
**we have a false-negative rate, it is a number we measure and report, and the
product's design assumes the model is wrong sometimes.** The control is the North
Star's four companion metrics, which is why they exist and why the North Star is
never reported without them. The operational piece is a **demotion path**: a
profile whose site-specific disagreement exceeds the gate is rolled back to the
prior bundle, which is a pointer move and a ledger row, not a release. If the
company cannot say out loud, in a customer meeting, "our AI's recall on your
site is currently 0.7 and here is the disagreement by class", we have built the
wrong company.

---

**11 · F-01 — The founders' runway expires before GA.** *L3 × I5 = 15, Critical by §1.1's safety-and-data rule*

The plan runs to M3 GA on 2028-03-31 and the first paying cohort is M2. Funding
runs out on a date that is somebody's private knowledge. The failure mode is
that this becomes a surprise in the worst month, and the response under pressure
is to make exactly the wrong promises — an enterprise SLA, a 24/7 commitment, a
migration commitment. The pre-agreed response must be written **now, while it is
cheap**: at 9 months of runway the milestone plan is re-cut to what the
remaining money buys and the reduced scope is applied immediately rather than
negotiated. At 12 months, raise a bridge or an angel round, earlier than feels
comfortable. And note the interaction with everything else on this list: a
two-person company that cannot afford both founders has a bus factor of one
(H-01) regardless of how good the documentation is, which makes runway the
mitigation for the most systemic risk here.

---

## 4. Risk-review log

**Every monthly review appends a row. This is the register's memory, and it is
what stops a risk from being re-opened, re-argued, or quietly dropped.**

```
RISK REVIEW LOG

date        risk ID  movement      why it moved                        decision
─────────────────────────────────────────────────────────────────────────────────
2026-09-28  (init)  register      54 rows created from the 13/14       adopt as v1
                         v1        documents and a direct read of the   E1 accountable
                                   code. Two rows incomplete (F-05,
                                   O-06); both repaired at the Nov
                                   review.

2026-10-21  T-09    new           The AI VMS strategy puts a camera      the capability
              T-10                 behind every claim the product       manifest is
              P-07                 makes, and no row covered either      measured, never
              P-08                 the compatibility swamp or the        assumed
              C-07                 recording's continuity. Eight rows     → T-09, and
              O-07                 added, not edits: §1.3 gives an        media-continuity
              A-07                 agent the right to add and not the    is a gate
              D-05                 right to change an owner's            → T-10, and
                                   response, so the three rows the       camera-lab
                                   strategy touches (P-02, P-03, C-01)   procurement
                                   keep the responses their owners       starts in P0
                                   wrote, and the new risk each of       → D-05
                                   creates gets its own row.             the pricing
                                                                       model change
                                                                       is unsigned
                                                                       → P-07
                                                                       the adapter
                                                                       and the camera
                                                                       path are both
                                                                       first-class
                                                                       → P-08
                                                                       a vendor is
                                                                       also a channel
                                                                       → C-07
                                                                       the recorder
                                                                       is monitored as
                                                                       its own failure
                                                                       domain
                                                                       → O-07
                                                                       local_only is
                                                                       enforced at the
                                                                       runtime
                                                                       → A-07

2026-10-21  P-02    (no change)   The AI VMS strategy makes this risk     nothing to do
              P-03                 larger, not different, and the
              C-01                 register's rule forbids an agent
                                   from rewriting an owner's response.
                                   **The three rows were reviewed and
                                   their responses stand**: P-02's
                                   "bundle that cannot do this" now
                                   has a second answer ("and neither
                                   can it prove the recording"), P-03's
                                   "the demo never shows a VMS feature"
                                   is now the wrong instruction
                                   because the product IS one, and
                                   C-01's "the forecast never counted
                                   on the channel" is unchanged and is
                                   why C-07 costs a forecast that was
                                   already zero. → **action: E1 to
                                   re-word P-02, P-03 and C-01 at the
                                   November review, by the owner and
                                   not by an agent, date 2026-11-30.**

  template   RISK-##  new / ↑ / ↓  the *signal* that moved it, not     the action, the
             / →       / accepted   a re-derivation of the score. A      owner, and the
                       / closed     score that goes down because          date it is done
                                   nobody looked is not a decrease.
```

**Worked example of the format, so the standard is visible:**

```
2026-11-30  S-02    ↓ 16→12       S-01's static route walk merged and
                                  its test fails when a handler decodes
                                  a tenant from a body. One of three
                                  known false controls fixed; two
                                  remaining (rate limiting, WebAuthn).
                                  Score falls because the *likelihood*
                                  of an undocumented control reaching a
                                  customer fell — not because the
                                  control class is smaller.

            S-02    (no change)   The quarterly control audit found
                                  control C22's named test
                                  (TestSiteConfigSchemaCannotCarryASecret)
                                  passes and C27's is named but was not
                                  demonstrated failing. → **action: C27
                                  gets a demonstration before the next
                                  review, owner E2, date 2026-12-31.**
                                  Score unchanged because the control is
                                  probably fine and the problem is our
                                  inability to prove it.
```

**The rule about this log:** an entry that only changes a number, with no
signal and no decision, is not a review. The log exists so that a decision made
in a specific month can be found in that month rather than re-litigated in an
arbitrary one — which is the same purpose
[`decisions-and-process.md`](../engineering/decisions-and-process.md) §3
serves for ADRs, and the same failure (`amnesia and re-arguing`) that both exist
to prevent.

---

## 5. Pre-mortems

The rule: **write the failure as though it has already happened, in the past
tense, with a date.** Not "T-06 might slip" — "*it is 2027-07-01 and M1 did not
complete; here is why*". A forward-looking risk list is optimistic by
construction, because a pre-mortem cannot be written in the future tense without
quietly becoming a plan.

Each cause below has an **earliest detectable signal** — the thing that, if we
watched it, would have told us — and a **countermeasure** that would have caught
it. A cause with no detectable signal is a cause we cannot act on, and it is
marked as such rather than given an invented one.

---

### 5.1 M1 — Trust Spine · milestone date 2027-03-31 · *"It is 2027-07-01 and M1 did not complete"*

| # | Why it failed | Earliest detectable signal | Countermeasure that would have caught it |
|---|---|---|---|
| **M1-a** | **The Milestone mapping was wrong and we found out in a pilot, not in a test.** The live surface differed from `internal/milestone/routes.go` in ways the synthetic fixtures could never surface — a field name, a pagination shape, an endpoint that returns 200 with an empty array on a permission the customer's group does not have | Any `adapter probe` run against a real server returning an unexpected shape. Today: nothing, because the command does not exist | **Write `adapter probe` and run it on the first site visit, as the first thing.** Add it to the install checklist. Do not leave a site without running it | 2026-11-30 |
| **M1-b** | **The VLM gate was never met and we shipped anyway.** The Python runtime arrived too late, there was no real GPU, and gate 4 got signed on a demo because the date was already public and moving it felt like admitting failure | The runtime's story points at <50% complete 60 days out; no 14-day shadow run started by 2027-02-01 | **A gate that is not met is a date that moves.** Put the 2027-02-01 shadow-run start date in the milestone plan as a hard checkpoint with a pre-agreed response, so the decision is made in January by two people with clear heads rather than in March by one person with a customer waiting | 2026-12-31 |
| **M1-c** | **Nobody ever ran the system with the model stopped.** Invariant 3 held in tests because the tests supply a fixture observation, and somewhere a code path consulted the model on the availability path | Any readiness check or restart traceable to the AI runtime; a dropped rather than unverified incident | **The manual run with the model stopped is a signed gate artefact, not a test.** It has to be a transcript because, as `07` §3 says, a test suite cannot prove that no other code path consults the model | at the gate |
| **M1-d** | **M1 was oversubscribed 2.5× and we discovered it in January.** ~78 engineer-weeks of backlog against ~31 of capacity, and the cut was not applied until the slip was a fact | Any week where remaining work exceeds remaining capacity at 60% | **The reduced-scope cut is applied on a date, in advance.** `00` §6: "Scope reduction is the only allowed response to a slip. Not more hours, not a third stream of work." Put the cut date in the plan | 2026-10-31 |
| **M1-e** | **The fault-injection work was cut and a real crash lost an event.** The last-thing-cut rule survived pressure, and a durability change shipped with only crash-equivalent coverage | Any diff touching a durability path with no fault-injection test; a `TestRecoveryDrill` that stops finding the gap | **Stage 3 (normalise) and stage 6 (job claim) are the M1 minimum** — the four stages with a partial substitute. The harness itself is 4–5 engineer-weeks and it is the single largest unbudgeted liability in the plan; budgeting it is the countermeasure | 2026-12-31 |
| **M1-f** | **Nobody could install it.** `install`/`upgrade`/`backup` were still skeletons, the service units were still absent, and a real install on a real site took three days of both founders' time | The first external install taking more than a day; `deploy/` still absent at 2027-01-31 | **Time an install by somebody who is not us, at a site we then never visit.** That is the only measurement that counts, and it has to be attempted before the milestone, not after | 2026-12-31 |
| **M1-g** | **A documented control reached a customer with no code behind it** — most likely the login rate limit, which is in the threat model, in the OpenAPI description, and in a config field, and nowhere in the implementation | The quarterly control audit; any customer security questionnaire answered from the document rather than the code | **The quarterly 35-control audit, and answering every customer questionnaire from the code.** A control with no failing test is a claim, and a claim in a document we hand out is a misrepresentation | 2026-11-30 |
| **M1-h** | **We had no SLO numbers, so gate 3 was signed on a load run that measured nothing.** `internal/observability` was still imported only by its own test and `/v1/metrics` returned 404 | The metric registry being unreachable from any code path; a load run whose numbers are all zero | **A new test asserting each metric is *reachable* from a code path, not merely named** (`TestEveryPlannedMetricExists` passes for a registry nobody writes to). Q-3 is on the critical path of a milestone gate | 2026-10-31 |

---

### 5.2 M2 — AI Investigator · milestone date 2027-09-30 · *"It is 2026-12-31 and M2 did not deliver the value we sold"*

| # | Why it failed | Earliest detectable signal | Countermeasure that would have caught it |
|---|---|---|---|
| **M2-a** | **The first paying customer was somebody we know.** M2's commercial state named "first external customer who is not personally known" and the pricing experiment produced numbers from friendly sites | Any pricing experiment whose respondents are design partners; any deal closed without a cold prospect in the funnel | **Make the cold pilot a milestone item with a date, not an outcome.** A reference pack that does not depend on knowing us: a measured before/after, an audit sample the customer verifies themselves, a demo they run with our servers off | 2027-06-30 |
| **M2-b** | **The ten VLM profiles were built and none of them passed its gate**, because ten profiles need ten datasets and ten evaluation runs and one person cannot do ten of those while building the evidence engine | Fewer than three profiles with a committed labelled dataset at the end of Q1 2027; any profile whose dataset is the same cases as another | **`01` M2's reduced scope: four profiles instead of ten. Take it early.** And gate each profile on a committed dataset with a recorded provenance file (`07` §8.4 rule 2) before building the profile | 2027-03-31 |
| **M2-c** | **The investigation engine made no measurable difference**, and we discovered it in the MTTI study rather than in a design review | Operator time-to-investigate flat against their own VMS-only baseline after four weeks; a support request framed as "we can find that ourselves" | **Set "meaningful" before the trial, per class, and record it** (`01` M2). If the study shows no difference, the finding is that the *class* was wrong — re-pick the classes, do not re-pick the metric or quietly report a favourable one | 2027-06-30 |
| **M2-d** | **The LLM orchestrator made things worse** — it plans, and a plan that searches the wrong time window produces a confident, evidence-backed wrong answer | Orchestration failures concentrated on incidents with clock drift; a summary that contradicts the incident's own timeline | **The orchestrator asserts nothing** (`01` M2: "the LLM orchestrates; it never asserts"), and a summary is derived, clearly marked, and never authoritative. The test is that a replayed incident reproduces its summary from stored facts with **no model call** — which `01` M2 puts in the exit gate and which does not exist yet | 2027-03-31 |
| **M2-e** | **`pgvector` for semantic search broke the offline install or the six-month-old-release rule**, and we found out from a customer | Any install or upgrade rehearsal needing the internet; a vector extension not present in the target Postgres image | **The dependency rule applies to Postgres extensions exactly as to Go modules** (`13` §3.4). Derived data only, PostgreSQL stays authoritative, and a quarterly install rehearsal from a release artefact on a machine with no network is the measurable control | 2027-01-31 |
| **M2-f** | **SOC 2 was not ready when the first enterprise conversation happened**, so a deal that was otherwise won was lost on procurement | No gap assessment delivered by 2026-11-30; the audit firm not engaged | **`14` §2's chain, which is arithmetic and not ambition, and whose first step is a purchase decision three months away.** Nine months from the report, and the report has to be in hand before enterprise deals, not after | 2026-11-30 |
| **M2-g** | **The operator's day got worse before it got better** — the new surfaces did not reduce attention, they added a second workflow to learn | Time-to-disposition per incident flat while the incident volume rose; "where do I do the old thing now" in a support request | **One unmoderated usability round per milestone, signed by E1**, and a hard rule that a new surface ships only when the old one it replaces is gone. A second workflow is a regression with a nicer screenshot | 2027-06-30 |
| **M2-h** | **A support incident consumed both founders for two weeks** and the roadmap did not move, and the loss was invisible in the plan | Any two-week period where roadmap work is zero; a Sev-1 in the first week of a milestone | **A named support budget inside the 40% tax, and a published support-hours paragraph in every agreement** so the expectation is set before the incident rather than during it. The second week is a scope conversation, not a work harder conversation | 2026-11-30 |

---

### 5.3 M3 — First production GA · milestone date 2028-03-31 · *"It is 2028-06-30 and GA is over"*

| # | Why it failed | Earliest detectable signal | Countermeasure that would have caught it |
|---|---|---|---|
| **M3-a** | **We auto-resolved a real incident.** Bounded auto-resolution shipped, the class was wider than the evaluation supported, and nobody was watching at 03:00 | False-negative rate or missed-incident rate regressing while the intervention rate improves — **both halves of the gate, read together** | **`01` M3's gate is the control: intervention rate below threshold *while* false-negative and missed-incident rates do not regress.** It is stated as a conjunction on purpose. And a demotion path: bounded auto-resolution per class, switchable at the site, audited, reversible | 2027-12-31 |
| **M3-b** | **A physical action was not idempotent.** PTZ to a person, a recording extension on the wrong window, a notification to the wrong recipient, and no way to argue about it afterwards | Any action path with no fault-injection test at the action stage; an action's idempotency key derived from reading the side effect's own result | **`07` §5's stage 8 is the highest-priority item in the whole quality programme and it is a gate on the first physical action, not a follow-up.** A kill after the external call succeeded and before the "after" audit row commits must produce one effect, not two. Build the harness before the PTZ preset ships | 2027-06-30 |
| **M3-c** | **The 24/7 support promise was signed before the rota was staffed**, and the first overnight Sev-1 was answered in eleven hours | A contract with a contractual SLI; a rota with fewer than four people; a founder woken by a non-Sev-1 | **`12` §7.3: we would rather decline a deal than sign one.** And the pricing line: a customer paying for 24/7 must see 24/7 on the invoice, because a 24/7 promise funded by a five-hour-a-day plan ends badly | 2027-09-30 |
| **M3-d** | **The first serious product incident was a wrong record**, and a customer found it in their own data before we did | Any Sev-1 discovered by a customer rather than by a detector; a metric that is named but never emitted masking a real condition | **The detection commitment is the control** (`12` §7.3): Sev-1 detection must be automatic and must not depend on a human noticing — the dead-letter, AI-unavailable, disk-free and seal-verification detectors, plus a nightly `doctor` whose failure is an alert. A Sev-1 a customer discovered is a detection failure as well as a product failure, and it is logged as both | 2027-12-31 |
| **M3-e** | **A customer ran a build past its support window and we had no answer**, and a security question about a two-year-old version exposed a control we had changed | Any build older than 12 months in a customer site; a question about a behaviour that changed without a migration | **`14` §6 and the support matrix in `12` §3.6**: a platform is supported when we have a release artefact, a service unit, a path-traversal negative test on that platform, and a customer — and the release notes state the outstanding gates. A site more than one major behind is upgraded by us or declined | 2027-12-31 |
| **M3-f** | **The operator learned to dismiss the feed.** False positives went up as automation increased, and the harm compounded silently per operator | Dismissal rate rising on a specific class; an operator saying "this is the same as the VMS" | **`internal/evaluation`'s 0.20 false-positive gate is the noise floor** ("an operator who is shown noise twice a week learns to dismiss the feed"), and the product-side answer is the per-class disagreement trend with a demotion path. And `12` §8.6: fix the cause, not the symptom — suppressing the rule without fixing the cause means the same incidents arrive from a different rule next week | 2027-12-31 |
| **M3-g** | **The evidence store filled a customer's disk and incidents silently lost their pictures.** Events kept arriving; evidence stopped | `evidence_disk_free_percent` breaching; a site at month three with a full volume | **`12` §9.1's sizing honesty** — the honest number is a formula with a measured constant, not a number — and a **conversations about storage at contract time rather than as a support incident.** The playbook page for this is the most serious routine situation in the document: "An incident without its evidence is not defensible" | 2027-09-30 |
| **M3-h** | **We said something we could not support in a release note or a contract**, and a customer held us to it | Any unsupported claim in a note reviewed under time pressure; a slide saying "certified" | **`07` §9.1 gate 19 and `14` §1.2.** E1 reviews the release notes and an unsupported claim is a security finding, not a typo. The evidence rule in `README.md` applies to us before it applies to anyone we write about | every release |

---

## 6. The escalation ladder

### 6.1 The three tiers, and what goes in each

**Tier 0 — raised immediately, interrupts whatever we are doing.** The bar is
low on purpose, because the cost of a false escalation is a ten-minute
conversation and the cost of a delayed one is a month.

```
  · Anything in this register with I = 5. No permission needed, no
    "let me check" first, no "is it worth bothering you".
  · Any safety-of-a-person signal, however uncertain.
  · Any customer-data-loss signal, however small.
  · Any indication that the five invariants are broken or might be.
  · Any external communication we did not author — a researcher, a
    journalist, a regulator, a competitor's customer.
  · Any incident that would be Sev-1 if we confirmed it.
  · Anything a customer could reasonably describe as "you told us this was
    fine" and we now think it might not be.
```

Who: **whichever of us learns it, to the other, immediately.** Not at the end
of the day, not in the weekly digest. The mechanism is the same as `12` §7.4's
Tier 1: a named person, by a channel that reaches them, and the right to say
"this is Tier 0" without justifying it.

**Tier 1 — the weekly agenda, or sooner.** Everything that is a problem rather
than an emergency.

```
  · Any new Critical (20–25) or High (12–19) row
  · Any row whose early-warning signal has moved
  · Any register row that could not be reviewed this month
  · Any decision blocked on the other person
  · Any pre-mortem signal that has become visible (§5 is a standing detector)
  · Any row whose state is Accepted and has been for two review cycles
  · Evidence from an agent's review of the diff, the dependencies, or the
    documents that contradicts something in here
```

**Tier 2 — the monthly register review.** The full pass: all Critical rows, all
overdue reviews, all new proposals, the risk-review log, and the pre-mortem
signals.

### 6.2 What is never escalated and never discussed

Named so the ladder is not the place where everything goes:

- **Risks below 12** with no signal movement. They live in the table and get
  their date.
- **Worries with no signal.** If it cannot be observed, it goes in `docs/requests/`
  or nowhere. "A risk nobody can observe is a signal" is the same discipline as
  `07` §7.2's "a metric that is not read by anyone" and `13` §2.2's review
  requirement.
- **Anything already decided.** [`decisions-and-process.md`](../engineering/decisions-and-process.md) §5's settled
  index exists so that a settled question is answered by pointing at the ADR,
  not by re-arguing. Re-opening a settled decision requires new evidence, and
  "I have thought about it more" is not evidence.

### 6.3 The one rule that overrides the ladder

> **No risk involving a real person's safety or a customer's data loss is ever
> carried silently for a week.**

Not "not for a week without both of us knowing". Not "not for a week unless it
is on Friday". The rule has one exception, and the exception is written down
here in advance so it cannot be used after the fact:

**The exception** is a Tier 0 raised to the other person on a channel that
reaches them, and then a weekly written summary in the decision digest until it
closes. What the exception is *not* is: waiting for a convenient time, waiting
for the next scheduled review, deciding to "confirm it first", or deciding the
risk is too small to interrupt somebody.

**Why this rule exists rather than a general escalation policy.** Everything
else in this register has a clock measured in months. A missed intrusion and a
lost evidence store have a clock measured in hours, and the two of us are the
only people in the company. A rule that says "escalate anything serious" is
unactionable; a rule that names the two categories which are *never* silent
gives both of us a thing to hold without requiring a judgement call at 03:00.
