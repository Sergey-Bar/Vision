# Pricing and Packaging

Owner: E1, with E2 on metering and licensing. Status: living. Last revised
2026-10-12.

Read [`strategy-and-north-star.md`](../strategy-and-north-star.md) §3, §6 and §8,
[`master-roadmap.md`](../master-roadmap.md)
§M2/§M3/§M7, and [`compliance-and-certifications.md`](../security/compliance-and-certifications.md) §8.1 first. This document is
decided **early enough to instrument** and **not published before the evidence
exists**, and those two facts are in tension on purpose: an unmeasured price that
was not anticipated is a pricing model we cannot implement, and a measured price
that was published before it was measured is a promise we cannot keep.

**Marker convention:** *(real)* = a file, constant or gate in this repository.
*(PROPOSED)* = proposed here, never measured, **not a customer commitment until
three referenceable GA customers exist** (`04` §4.2). *(ESTIMATE)* = calculated
from stated assumptions, with the measurement that replaces it named.
*(NOT YET KNOWN)* = does not exist yet.

> **Every price in this document is a hypothesis.** The words "our pricing is"
> do not appear anywhere in it, and if they appear in a customer conversation
> before the three-referenceable-customer condition is met, that conversation
> has gone wrong. What we may say before then: the *shape* (per site, not per
> camera; outcome-based, not seat-based; no video to a cloud), the *components*,
> and the fact that a quote follows a design-partner conversation rather than
> preceding one.

---

## 1. Pricing principles

Six. Each one is a decision we can be shown to have made, and each one has the
reason it was made, because a pricing principle with no stated reason is a
preference.

### 1.1 Priced on the outcome, not on the camera count

**Never per camera. Ever.** Three reasons, and the first is arithmetic:

1. **A camera price punishes the buyer for their own success and rewards us for
   nothing.** A customer who installs four more cameras to close a coverage gap
   is doing exactly what we want them to do, and a per-camera price makes that
   decision expensive. We would be charging them for evidence that the system is
   working.
2. **The cost driver is not the camera count either.** `12` §9.1: at M1's incident
   volume, evidence retention is dominated by *how many incidents a site has*,
   not how many cameras it has — the formula is
   `evidence/day = incidents/day × items_per_incident × mean_item_size ×
   retention_days`, and a site with 30 incidents/day on 30-day retention needs
   ~73 GB against ~730 GB for 300 incidents/day (ESTIMATE). A per-camera price
   charges for the wrong variable and a per-incident price charges for the right
   one in a way that penalises a site with a genuine security problem.
3. **It is the wrong unit for a layer.** We sell above a stack the customer owns.
   A metric that counts their stack invites the question "why are you charging me
   for a camera you already own and I already pay for".

**What we price on instead, in priority order:** the site (the unit of
deployment and the unit of autonomy), and above a declared threshold the volume
of *verified incidents handled* (the unit of the outcome we actually produce). The
second is metered, capped in the contract, and reconciled monthly — §5 explains
why it needs a cap to exist at all.

### 1.2 Local-first means the value proof lives on the customer's site

Every number we would ever use in a renewal conversation — MTTI per class,
`attention_minutes` per 1,000 events, `missed_incident_rate`,
`evidence_completeness`, the AI plane's actual availability — is computed at their
site from their data, by their rules, with a CLI command they run themselves.
`08` §2.0: a number in a gate artefact, a release note, a customer report or a
board document is always the Postgres computation at the site, not a counter
reading we ship.

The commercial consequence is not a feature decision, it is a **renewal
strategy**: we do not hold the value evidence, so we cannot be switched off from
it, and so the renewal conversation is about whether the number is still true on
their site. That is a much stronger position than a dashboard they cannot
reproduce, and it is a direct consequence of refusing cloud video.

### 1.3 No per-seat pricing for the operator

**The operator is the thing we are reducing.** A per-seat price is a price on the
labour we are trying to make unnecessary: it grows as they hire, it falls as they
succeed, and it tells them the wrong thing about what the product does. It is also
the pricing model of the incumbents whose desks we are meant to make smaller, and
`08` §8.1's North Star is *human attention minutes*, which is by construction a
number that must go down.

What we charge for instead is deployment, capability and, eventually, autonomy
level — the things that scale with what we do, not with who is looking.

**The one place seats appear** is as a *discount trigger*, not a charge: PROPOSED
a 5% discount when a site has more than N console operators, on the reasoning that
a shared surface used by a whole shift is a sign of adoption, not a billing unit.
This is reversible and costs nothing if it is wrong.

### 1.4 A price that lets a site say yes without a procurement committee

This is a **hard product constraint on the commercial model**, not a marketing
goal. From `00` §3, segment 1 is entered at M2 and is "the fastest decision,
single operator, pain is immediate and daily"; `04` §2.1 names its disqualifier
as "a site with a single camera type", not as "a site above a certain budget". If
the price requires a committee, we do not reach segment 1, we skip to segment 2
and we inherit segment 2's 3–6 month cycle (PROPOSED) with none of the density.

**What it constrains, concretely:**

| | Rule | Why |
|---|---|---|
| Single-site price | At or below the threshold where a manager can approve from a budget line, PROPOSED **≤ $1,200/month (≤ $14,400/year)** | Above this, at most organisations need a purchase order and a second approver, and the cycle is measured in months |
| Number of components on a quote | **One line item plus a support line.** Three lines and the buyer has to allocate cost between them, which becomes a negotiation about allocation rather than about value | A quote is an argument, and an argument with three numbers has three places to lose |
| Signature | E1 can sign a single-site deal on a purchase order under the threshold. No legal review for deals inside it | Legal review is `18`'s legal line item, and it exists for the segment-5 deal, not this one |
| Pilot | Starts as a single site at the founding rate, cancellable on 60 days' notice | The purchase is a measurement, and if the measurement fails they leave cheaply and tell the truth about why |
| First-year total, all in | The quote states the year-one cost including the optional GPU and including evidence storage, because the two surprises are never the licence | `12` §9.3: the number a customer is surprised by is evidence storage |

### 1.5 Price what we can prove, and refuse to price what we cannot

We do not price, discount against, or warrant: detection accuracy, incident
prevention, loss reduction, or false-positive reduction. `08` §3.2 lists these
among the numbers this roadmap refuses to state, and the first reason is that
they are the most tempting to launder and the least likely to survive a customer
who measures them.

**What we do price is:** a site, a capability, an autonomy level, and — above a
declared volume — the number of verified incidents handled. Every one of those is
a fact we can count at the customer's site and reconcile in a statement they can
audit.

**What the customer prices, which is the thing we do not control:** their own
operator minutes. Our job is to make that number legible and to make it go down,
and the M2 measurement design (`08` §4) is the instrument. Pricing follows the
measurement, and the measurement is set up before the first quote exists.

### 1.6 The grandfathering rule

**A customer's price never goes up for a capability they already use, within the
term they signed.** New sites at an existing customer get the current list. New
capabilities at an existing site are either included in what they pay or
explicitly priced as a module, and the second is a conversation, not an invoice.

The reason is structural rather than sentimental: `00` §5's compounding claim is
that by M6 a new customer costs more to onboard than an existing customer costs to
keep. A company that raises prices on its own installed base is optimising the
opposite of that, and the claim is checked at the M6 review (`01` §M6's exit
gate). Making this rule now is making the M6 gate reachable.

---

## 2. Packaging: M2 → M5

### 2.1 The four shapes, with the trade-offs

| # | Option | Shape | Pros | Cons | Verdict |
|---|---|---|---|---|---|
| **A** | **Per site, per month** | One price, one site, one month. Modules as opt-in add-ons | Simplest possible quote, which is §1.4's requirement. Predictable for both sides. Pairs with autonomy being granted per site (`01` §M7). **Air-gap-safe: the only billable quantity is a count, and a count does not need telemetry** | Does not scale with the customer's success. A site with 5 incidents a day and a site with 500 pay the same, and the second one is getting far more value. `08` §2.1's own note: cost per inference is "optimised by never inferring", so a price with no volume component has the same disease one level up | **Recommended as the base, M2 → M4** |
| **B** | **Per incident tier** | Bands of verified incidents handled per month, per site, priced per band | Directly on the outcome (§1.1's second unit). The price rises with demonstrated value rather than with headcount. Reads well to a finance team as a cost per outcome | **The customer's control, not ours**: an incident volume falls when the product works. Pricing on a number the vendor reduces is a structural argument for the vendor to under-report, and we would be relying on ourselves not to. Requires telemetry to meter, and §5 shows why that is unavailable on an air-gapped site. Also prices a *bad* quarter (a break-in month) as a good one | **Rejected as a primary; retained as a declared-threshold allowance** |
| **C** | **Per site base + per-1,000-events** | A small base plus a metered variable above a threshold | The variable only bites a genuinely high-volume site, and the base covers the small ones. Smoother than tiers. Directly measurable | Metering dependency (air-gapped sites cannot be metered), and "events" is a *pipeline* number the customer cannot independently verify and would have to take on trust — the opposite of §1.2. `08` §1.2's fabrication trap is relevant here too: an event count is trivially moved by deduplication, filtering and suppression, all of which the product does legitimately from M3 | **Rejected as a primary; the variable component requires a contract cap** |
| **D** | **Module add-ons** | Base plus named capability modules: evidence intelligence, investigation orchestration, site intelligence, autonomy tier, SDK/OEM | Lets a customer buy the thing that solves their problem and defer the rest. The M2 pricing experiment can test packaging shape at constant revenue (PROPOSED, `04` §4.2 variable 2) | **More than one line item on the quote**, which §1.4 forbids. And modules invite the reading that the base is inadequate, which is a worse conversation than a higher base price | **Recommended from M5, and from M2 only as a *capability* list in the quote rather than as separate priced lines** |

### 2.2 The recommended structure

```
M2–M4   Base:  per site, per month.  One number.  One line item.
        Optional: a support level priced as a separate line, because it is a
                  genuinely different promise with a genuinely different cost
                  (12 §7.3) and burying it inside the licence is how a company
                  ends up promising 24/7 from a five-hour-a-day plan.
M5      Base:  per site, per month, with volume tiers on the site count
              (the tiers are on OUR rate, not on their consumption).
        Plus:  a platform fee covering the control plane, fleet management,
               OIDC/SCIM, HA, and the 24/7 rota — because those are the
               capabilities M5 adds and they are not per-site costs.
        Modules become separately priced from M6, at the point there is a
        second thing to sell.
M6      Adds:  the intelligence premium (§2.5).
M7      Adds:  the autonomy tier (§3).
M8      Adds:  SDK, OEM, marketplace take rate (revenue is not per-site at all).
```

### 2.3 Price points

**All *(PROPOSED)*. All hypotheses. All resting on assumptions that do not exist
yet, listed against each one.**

| Milestone | Item | Price (USD) | Term | Assumptions it rests on |
|---|---|---|---|---|
| **M2** | Founding paid-pilot rate, single site | **$600 / site / month** | 6 months minimum, cancellable on 60 days' notice, then month-to-month with 60 days' notice | (1) A single-site operator's investigation time is reducible by ~50% on supported classes — **(NOT YET KNOWN)**, and it is the M2 exit gate's number. (2) A loaded operator cost of ~$30/hour in segment 1 — ESTIMATE, unverified, and the customer's own number, not ours. (3) ~60 operator-hours/month of incident investigation at a site with ~20 incidents/night — **(NOT YET KNOWN)**, from the M1 shadowing in `05` §5.2. At 50% reduction that is ~$900/month of saving, so $600 is ~67% of a conservative estimate. **The price is below the estimated saving, deliberately, because the finding is unmeasured and the saving is the customer's labour** |
| **M2** | Pricing-experiment levels | **$450 / $600 / $850** | As above | 6–9 paid sites across 3 levels, 2+ per level, not randomised. **A price we have never quoted to anyone is not a data point** (`04` §4.2) |
| **M3** | First production GA list | **$1,200 / site / month** | 12 months, or monthly with 90 days' notice after month 3 | GA list ≈ 2× the founding rate. The gap exists to make conversion from the founding rate a real decision rather than an automatic yes. **Not published** until three referenceable GA customers have each seen a price and said yes at approximately this level |
| **M5** | Enterprise, 5–19 sites | **$2,200 / site / month** + **$5,000 / month platform fee** | 12–36 months | Requires the M5 control plane, fleet management, OIDC/SCIM, HA, and a site that keeps operating with the plane pulled. The platform fee is the cost centre, not a margin centre. **Assumes segment 2's 3–6 month cycle holds with a named technical evaluator** (`04` §2.2) |
| **M5** | Enterprise, 20+ sites | **$1,800 / site / month** + **$12,000 / month platform fee** | 12–36 months | Volume tier reduces our per-site price because our cost per site at 20 sites is lower than at 5 (§8). **The floor is not set**; there is no number below which a site is served below cost. Setting it is a M5 decision with the support data from M3 and M4 |
| **M5** | Support 24/7 | **$1,200 / site / month uplift** (PROPOSED) | Bundled annually | `12` §7.3: 24/7 is priced visibly into the licence, because "a 24/7 promise funded by a five-hour-a-day plan ends badly for the customer". This is only sellable once the ≥4-person rota exists, and §2.3 of `15` `T90-3` is the decision record for that |
| **M6** | Intelligence premium | **+40% on the site price** | Bundled | `01` §M6's commercial state: "a paid tier on top of the platform, justified by measured reduction in operator minutes, sold on a before/after number the customer produced". **The premium is 40% because the increment is 40% more capability and the measured value is unmeasured; if the measured value is lower than 40%, the premium is wrong and gets corrected, not defended** |
| **M7** | Autonomy tier | See §3 | — | The hardest number in this document, and it is 4 years away |
| **M8** | SDK / OEM / marketplace | See §4 | — | The only revenue that does not scale with our own sales capacity |

**What must not be charged for, ever:** the audit ledger, the seal path, replay,
`securityctl doctor`, `securityctl preflight`, the rule-bundle lifecycle, the
offline installer, the local identity, and the audit checkpoint verification. These
are the three buyer questions in `00` §4, and pricing the answer to "can I prove
the system did what it says it did" is a way of telling the buyer the answer
costs money.

### 2.4 The break-even arithmetic, with every number exposed

**This is the part of pricing that is arithmetic rather than hypothesis, and it
should be read before any number in §2.3 is quoted to anyone.**

| Input | Value | Source / status |
|---|---|---|
| List price, single site | $1,200.00 / month | §2.3 *(PROPOSED)* |
| Support cost per site | 1.5 h/month × $95/h loaded = **$142.50** | *(ESTIMATE)* — the measurement that replaces it: logged support minutes per site per month across ≥ 20 production sites for a quarter, from the support system, not from a survey |
| Verification compute borne by us | **$25.00 / site / month** | *(ESTIMATE)* — a shared evaluation and analytics footprint. The customer's GPU is their hardware (`12` §9.3); measurement: actual cost at the measured AI plane availability across N sites |
| Evidence storage borne by us | **$0** at M2–M4 | Real, by architecture: evidence lives on the customer's disk. It becomes a real cost only if we ever replicate it, and §6.4 forbids that for video/evidence bytes |
| **Variable cost per site** | **$167.50 / month** = support $142.50 + compute $25.00 + storage $0.00 | arithmetic |
| **Contribution per site, list** | **$1,032.50 / month** = **86% of list** | arithmetic |
| **Contribution per site, founding rate** | **$432.50 / month** = 72% of $600 | arithmetic. The founding rate is a *deep* discount — it is 42% of list — and §2.1's "a site costs a deliberate loss against fixed cost and recovers support, not overhead" is the reason |
| Company fixed cost, GA without the 24/7 rota, **founders unpaid** | **$20,050 / month** = 2 founders at $0 + 2 hires at $17,050 + infra $550 + insurance/legal $650 + travel $1,800 | *(ESTIMATE)* — the same number, line by line, in `18` §2. **This is today's state and it is not a state a company stays in** |
| Company fixed cost, GA without the 24/7 rota, **founders paid at $7,500** | **$27,550 / month** | *(ASSUMPTION)* — `18` §1.2. The founder salary is a commercial lever and it is shown as one |
| Company fixed cost, GA **with** the 24/7 rota | **$50,050** (founders unpaid) / **$57,550** (founders paid) | *(ESTIMATE)*; `12` §7.3 requires ≥4 people for 24/7, and `17` §2.3 prices the uplift at $1,200/site/month |

**Break-even, eight ways, from those inputs.** The fleet of the first 12 sites is
4 at the founding rate and the rest at list (`17` §2.5), so the second column of
each pair is the break-even for *that* actual mix, not a hypothetical one:

```
  all at list:   1,200·N − 167.50·N ≥ fixed        →  N ≥ fixed / 1,032.50
  4 founding:    2,400 + 1,200·(N−4) − 167.50·N ≥ fixed  →  N ≥ (fixed + 2,400) / 1,032.50
```

| Configuration | Fixed | **All at list** | **With 4 at the founding rate** |
|---|---|---|---|
| GA, no 24/7, **founders unpaid** | $20,050 | **20 sites** | **22 sites** |
| GA, no 24/7, **founders paid** | $27,550 | **27 sites** | **29 sites** |
| GA, **with** 24/7, founders unpaid | $50,050 | **49 sites** | **51 sites** |
| GA, **with** 24/7, **founders paid** | $57,550 | **56 sites** | **58 sites** |

Check: 20,050 ÷ 1,032.50 = 19.4 → **20**; (20,050 + 2,400) ÷ 1,032.50 = 21.7 →
**22**. 27,550 ÷ 1,032.50 = 26.7 → **27**; 29,950 ÷ 1,032.50 = 29.0 → **29**.
50,050 ÷ 1,032.50 = 48.5 → **49**; 52,450 ÷ 1,032.50 = 50.8 → **51**.
57,550 ÷ 1,032.50 = 55.7 → **56**; 59,950 ÷ 1,032.50 = 58.0 → **58**.

**The finding, stated plainly, because it is the single most important commercial
fact in this document:**

> **The 24/7 support promise costs 27 additional customers. The founders taking a
> salary costs 7 more.** Together they move break-even from 20 sites to 58 — from
> a fleet a two-person company can conceivably sell to a fleet that is only
> reachable through a channel.
>
> The reason is structural and not a pricing failure: a 4-person rota is
> $30,000/month whether we have 3 sites or 300, and a salary is $7,500/month
> whether we have one customer or fifty. **The biggest cost in this company's
> future is a support commitment and a payroll, not the product.**
>
> We therefore do not sell 24/7 before the rota exists (`12` §7.3), we price the
> uplift visibly rather than absorbing it ($1,200/site/month — which nearly
> halves the site count required, since it is the only change that raises
> contribution as fast as it raises fixed cost), and every site count, runway
> number and capital ask in [`financial-plan.md`](financial-plan.md) is computed with 24/7
> **excluded** and then shown with it **included**. A plan that quietly assumes
> 24/7 is a plan built on a promise we cannot staff.

### 2.5 The first 12 sites

The actual sequence, which is what the pipeline in `04` §10 tracks:

| Sites | Milestone | Price | Monthly revenue | Monthly contribution | Against a $20,050 fixed base |
|---|---|---|---|---|---|
| 0 | M1 | none | $0 | $0 | −$20,050. Three design partners, no charge (`01` §M1), and this is the entire cost of the M1 commercial motion |
| 1–3 | M2 | $600 founding | $1,800 | $1,297.50 | −$18,752. **Every site at the founding rate is priced to recover support, not overhead.** At the founders-paid base of $27,550 the gap is −$26,252 |
| 4–12 | M2–M3 | 4 founding + rest at $1,200 list | $2,400 → $12,000 | $1,730 → $9,990 | −$18,320 → −$10,060. Break-even at 20–22 sites is not reached inside the first 12 and the plan does not assume it is |
| 13–20 | M3–M4 | same mix | $13,200 → $21,600 | $11,022.50 → $18,250 | −$9,027.50 → −$1,800. Still short: **the fixed half is covered by capital, not by revenue, until roughly 20 sites** |

---

## 3. The autonomy pricing question (M7)

Unresolved until M7, and recorded here as a question with the three candidate
answers and their failure modes, because a pricing model for a product that does
not exist is a guess, and the guesses worth recording are the *shapes*, not the
numbers.

### 3.1 What is being priced

`01` §M7: autonomy levels **A0 observe, A1 assist, A2 recommend, A3
execute-with-approval, A4 policy-controlled autonomy**, granted per site, raised
by an explicit, audited, reversible act with a blast-radius preview. The
commercial state: "Sold per site per month, priced on the exception queue's
value."

Three candidate shapes:

| Shape | What the customer buys | PROPOSED price | Pros | Failure mode |
|---|---|---|---|---|
| **Premium** — a flat uplift on the site price per level | A capability | A2 +25%, A3 +50%, A4 +80% on the site price | Simple to quote, one line item, matches `01` §M7's "sold per site per month". Predictable for the customer | **The customer is paying for a promise we cannot yet measure.** We would be selling a percentage uplift for a reduction in operator minutes we have not shown at that level. And the percentage would be set in 2026 for a product in 2030 |
| **Shared savings** — a share of the measured reduction in operator minutes | An outcome, verified by the customer's own telemetry | e.g. 20% of the measured reduction in `attention_minutes` per 1,000 events, quarterly, reconciled against the M1 baseline the customer produced | It is the most honest possible structure: it is priced on the North Star, it is measured by the customer at their site (§1.2), and it cannot be gamed by us because the customer computes it. **It is the only option where the price falls when the product gets better** | It needs a baseline that survives staffing changes, a shift pattern change, and a site event. `08` §2.2.1: `attention_minutes` is currently **elapsed time, not active time**, and the cap is *(PROPOSED: 4 hours)*. Quoting a percentage of a number with that caveat is quoting a percentage of an approximation. It also creates a perverse conversation where the customer is incentivised to keep incidents open to reduce our revenue share, or to distrust us at renewal |
| **Per-site tier** — the site's autonomy level *is* the price band | A governance posture | A0–A1 = base; A2 = +$600/site/month; A3 = +$1,200; A4 = +$2,000 | The price is tied to something the customer's *risk* committee already has a level for. It maps onto how regulated buyers think, which is where the ACV is (`00` §3 segment 5) | It prices a governance posture rather than a result, and a customer can raise their level and see no difference — which is exactly the wrong incentive in a product whose headline metric is attention minutes going down |

### 3.2 The recommendation, and the reason

**Recommendation: the premium, at a percentage, with a *floor* and a *review*, and
a shared-savings component available as a negotiated alternative for a customer
whose finance function prefers it.** The premium shape is chosen because
`01` §M7 specifies the commercial basis (per site per month, priced on the
exception queue's value) and because a per-invoice reconciliation of a metric
with a known methodological caveat is a contract we would be defending at
renewal.

**But the percentage is not decided until M6 has the measured reduction.** The
rule: the A2 uplift is set to the value of the measured reduction in attention
minutes for a customer running at A2, computed from at least three customers'
own numbers, and rounded down. A number we cannot derive from three customers'
measurements is a number we invent, and `00` §8 is the reason we do not.

### 3.3 The hard rule, and what it does to the commercial model

> **A customer may always lower a site's autonomy level, with no notice period,
> no fee, no penalty, no minimum term, no price increase on the remaining
> capability, and no effect on renewal. It is an audited configuration change
> through the activation path, like every other behaviour change, and it is
> available at every level including from A4 to A0 in one action.**

This is not customer service. It is forced by three things in the codebase and the
roadmap:

1. **`01` §M7: "Sensitive and high-impact actions stay behind explicit policy and
   authorisation, permanently."** A penalty for reducing safety is a pressure to
   keep running autonomous operations, which is a pressure against the permanent
   authorisation gate. The rule and the safety property are the same thing.
2. **The kill switch exists and is a configuration change** (`15` §4.3), and it
   is the same mechanism. If lowering a level had a commercial consequence, the
   kill switch would have one too, and a kill switch with a commercial consequence
   is not a kill switch. It would be a suggestion.
3. **`01` §M7: "an action that is not verified as having succeeded is a failed
   action, not a completed one."** The same reasoning at the level of the posture:
   if the posture stops being appropriate, the cost of discovering that must be
   zero.

**What it costs commercially, stated honestly:**

- We cannot price the *option* to go autonomous. Only the state.
- A customer can reach the revenue ceiling and then stay there, indefinitely, and
  we have no contractual lever.
- Every renewal conversation has a section in which the customer asks to be
  *charged less for being safer*, and the answer is yes, immediately, in writing,
  in the same conversation. That is a real cost and it is a cost we would pay
  anyway in a disclosure or an incident, at a much higher price.
- It means the pricing model cannot rely on escalation revenue. It has to rely on
  **more sites** and on **modules**, because those are the only two things a
  customer voluntarily increases.

**What it buys, which is the only reason to accept it:**

- The autonomy claim survives a security review. A product that punishes a
  customer for reducing its blast radius has a blast-radius story, and it is a
  bad one.
- Our largest segment 5 buyers (healthcare, critical infrastructure, government)
  have internal policy that *requires* the ability to reduce autonomy on demand.
  A product without it does not clear their change control, and `04` §2.5's
  disqualifier list is full of deals lost to exactly this.
- The North Star (`01` §M7) requires trust as the measure. Trusted automation is
  success. A pricing model that charges for the absence of trust contradicts the
  company's own stated definition of success, and a contradiction there is a
  defect in the product, not in the contract.

---

## 4. The OEM and channel model

`00` §3 segment 3. The SDK, the per-deployment royalty, the white-label fee, the
support and certification fees, and the no-fork and attribution clauses. All
*(PROPOSED)*; the OEM cycle that consumes all of this is 15–30 months to first
revenue *(PROPOSED)*, `04` §7.3.

### 4.1 The components

| Component | PROPOSED | What it is | Why it is separate |
|---|---|---|---|
| **Certified-partner platform licence** | $25,000/yr (T1) to $60,000/yr (T3), **or** 20–30% off list for the partner's own customers, whichever the partner prefers | The right to ship our layer inside their product, with the certification suite in their CI, the SDK, and the support tier below | The partner's own engineering and support cost is not our cost, and pricing it as if it were makes us look expensive and them look cheap |
| **Per-deployment royalty** | **15% of list, per site per year, paid by the partner on our behalf** | The per-site economics of the product, so that the partner's incentive is to sell sites rather than to sell a contract once | Without a royalty, a partner's incentive is to close one big deal and let the renewal sit. With it, our revenue and theirs are the same thing |
| **White-label fee** | One-time **$15,000** setup + **5% uplift** on the white-label variant | Deployment, documentation and console under their brand | It is real work and it is a one-off cost we carry |
| **Certification fee** | **$2,500 per adapter**, waived for the first two adapters from a partner who also commits to a marketing plan | Running our public certification suite against their adapter, and the review of the result | It is the same service we run internally (`01` M4's gate requires it be run by someone outside the company) and it is a real cost |
| **Support** | Tiers: L1 (partner's own desk, we supply runbooks and a second-line escalation) to L3 (we are the named human, at the `12` §7.3 SLIs) | Whichever the partner needs and can staff | The partner's customers' SLAs are the partner's contractual problem, and the only question is who actually answers. Getting this wrong is the most common real failure in the pilot stage (`04` §7.3) |
| **Marketplace take rate (M8)** | PROPOSED 15–25% | Their adapter or pack, sold to a third party through our marketplace | Later, and the number is a market-structure question, not a product question |

### 4.2 The no-fork and attribution clauses

**No-fork.** One core. The partner's adapter, rule packs and profiles live
*outside* the core, against published contracts, and are installed the same way any
third-party install is. The commitment is structural, not contractual: `01` M4's
exit gate is a **non-adapter diff of zero behavioural changes** across two
adapters, and a fork ends that property permanently, which means the partner's
version and ours stop being the same product within one release cycle.

A fork is grounds for termination. This is the one clause I expect to be tested
hardest and the one I will not trade.

**Attribution.** Every decision our layer produces names us — layer version,
model version, prompt version, profile version, input hash, in the audit record
the partner's customer holds. **White-label in deployment, documentation and
console: yes. White-label in provenance: no, and it is not negotiable.**

The reason is not pride. An unattributable AI decision inside someone else's
building is a liability for *them*, and a partner who requires it is asking us to
help them ship something their own customers will be unable to audit — which is
also the thing that ends their cycle. If that is not persuasive, the threat model
and the control-to-test map are, and we send them.

### 4.3 What an OEM will refuse to sign, and what we can concede

| They will refuse | Position | What we can concede |
|---|---|---|
| **Exclusivity** — "our customers only" | **Hard no.** It forecloses segments 1, 2 and 4, which is most of the company (`00` §3) | Non-exclusivity plus a first-look right: we do not sign a direct deal with an account in their named territory without 30 days' notice, and we take their objection seriously |
| **The attribution clause** | **Hard no.** See §4.2 | Everything else in the white-label conversation. Console, docs, installer, the update channel, the pricing page, the release notes' brand layer |
| **A fork** | **Hard no.** See §4.2 | A partner-specific *rule pack* and *adapter*, which is not a fork, and is a product (`01` M8 vertical packs) |
| **Per-deployment royalties** | Real resistance. Their finance function will not accept a recurring per-site charge in a hardware-margin business | The royalty can be **capped per partner per year**, or converted to a step function (a fixed annual minimum once they cross a site count). The economics we must keep: their incentive has to grow with sites |
| **Our 24/7 SLAs, for their customers** | They will ask, and we cannot yet staff them | `12` §7.3's business-hours commitment in writing, and a named date for the rota. **Selling an SLA we cannot staff to make a partner's quarter is the exact thing `00` §7 is written to prevent** |
| **A certification they did not ask for** | They will ask to skip it | Nothing to concede. The certification suite is the artefact that makes *them* able to support it. Skipping it is how a partnership fails at the pilot stage, and it fails them, not us |
| **A liability cap they want raised** | They will want our cap to be higher than the deal size | The cap is negotiable; the *structure* is not — it is a cap, and the partner preserves the ledger (`01` §M4's whole property) |
| **Roadmap commitments a year out** | They will ask for a feature by date | `05` §3.7: no date for something not in the backlog, and a commitment with a named gate and a written answer to "if we miss it". What we will offer instead is the reduced-scope list, so they can see what we cut first |
| **Source access** | They will ask to see the core | The public SDK, the public contracts, the conformance suite, and the threat model. The core source is not available and this will not change |
| **A revenue share on the *first* year rather than a royalty** | Occasionally | Yes, on the first year, at a lower percentage. It costs us a year of the recurring number in exchange for a signed deal, and at a channel stage with a 15–30 month payback, that is a good trade |
| **Perpetual licence terms for their customers** | They will ask | A contractual commitment to the customer that the licence is perpetual and that the product will not be disabled for non-payment (`12` §3.3) — **is** available, and it is a *better* thing to give them than enforcement |

---

## 5. Metering

### 5.1 What we count

| Counted | Unit | Where the number comes from | Why this one |
|---|---|---|---|
| **Sites** | count | A signed activation, one row per site, in the ledger | The only quantity available on an air-gapped site. §5.3 |
| **Accepted events** | count, in aggregate, per period | `inbox_events` in a terminal state in the window. `08` §2.1's durable form | The platform-health number that already exists; used for the volume allowance and for capacity, not for a headline price |
| **Incidents** | count, by class, per period | `incidents` with `rule_id`, per `08` §2.2 | The outcome unit. Class is included because a class is what a customer recognises |
| **Evidence bundles** | count, by kind, per period | `evidence` by `kind` (`snapshot`, `clip`, `bookmark`, `export`) | Metering what we produce, not what the customer's cameras produce. A clip is 8 s of a 30-day recording, not a month of video |
| **Verification compute** | count of observations + a compute-minute figure | `ai_observations` count, `latency_ms` sum, `degraded_reason` breakdown | Both directions matter: the count is what we cost the customer's GPU, and the degraded breakdown is the AI plane's actual availability, which is an asset in the threat model (A12) and a thing a customer will ask about first |
| **Capability activations** | count, per site, per capability | The ledger, via the activation path | The module-billing quantity, and it is free: it is a count of audited rows, and it cannot be inflated by hiding behaviour because the behaviour change is the thing being audited |

### 5.2 What we must never count, or persist for billing, that the customer considers sensitive

| Never | Why |
|---|---|
| **Evidence bytes, or any content derived from them** | Video of people, in a building, at a time. The absence of a person model in M1 is enforced by `schemalaws.TestNoEntityAliasTableInM1` (C35) precisely so that there is nothing to bill on. A billing system that reads evidence is a billing system with a data-protection assessment attached, and we do not need one |
| **Any per-event payload**: camera id, device id, zone, door, or event content | A count that requires the customer to send us their device inventory to compute is a privacy review, a site-configuration export, and an objection we will lose |
| **The identity of any person** — badge, credential, face, plate, name, shift pattern | `01` M5 is where a person model exists, and even there it is not a billable quantity. **If our pricing ever requires knowing who was on site, the pricing is wrong** |
| **AI model outputs or their content** | Billing on model output means a billing dispute about what the model said, which is unresolvable, and it is a copy of the customer's data leaving the site |
| **Free-text incident notes, dispositions' narratives, or anything an operator wrote** | Same reason, and it is the most sensitive thing on the platform |
| **Anything about a specific incident, in aggregate or otherwise** | An incident is a security event at a site. Aggregating them is still building a profile of that site's incidents, and a security director will ask why their incident count is in a vendor's invoice |
| **Anything that would let a customer, or us, infer an incident occurred** | Hard rule. If the meter can be used to ask "how many break-ins did we have", the meter is surveillance, and a security product that is also a surveillance system cannot pass `docs/threat-model.md`'s P5 profile review |

**The rule, general:** *a billing counter may count the work; it may not know the
thing it counted.* Every counted quantity is a number, never a payload, and every
number is derived from a table the customer already owns and can query.

### 5.3 The offline constraint, and the design problem it creates

**The constraint.** `00` §3, `01` §M1 and `12` §3.1: the product is local-first,
an air-gapped site is a first-class customer, and the suite must pass with no server
reachable. Metering therefore has to work with **no outbound connection, ever**.

**The design problem, stated as a problem and not solved by waving:**

| Metered quantity | Air-gapped metering | Consequence |
|---|---|---|
| **Sites** | Trivially available: a local count, and a signed activation per site in the ledger | None. **This is why sites is the base unit** (§1.1, option A) |
| **Evidence bundles, incidents, accepted events** | Available *locally* as counters, but not reconcilable with us without a connection | See below |
| **Verification compute** | Available locally | Same |
| **Capability activations** | Available locally, and in the ledger | None — the ledger is at the site, and a usage statement can be produced by an offline CLI command |

**The problem.** Three of the four billable quantities cannot reach us from an
air-gapped site. Options that look reasonable and are not:

| Tempting approach | Why it fails |
|---|---|
| Phone home periodically | Contradicts `12` §3.1 outright. A site that cannot start because our server is down has a security product that is a liability, and the same reasoning applies to billing |
| Bill on a locally-computed estimate | The customer would be disputing our arithmetic on a machine we cannot inspect, at renewal, forever. Unresolvable and corrosive |
| Make telemetry a condition of the licence | The metering then controls whether the site runs. A meter that can switch off a security system is a liability with a revenue motive |
| Drop metering entirely and price per site only | Loses the outcome component, and loses the ability to grow with a customer's volume |

**The resolution, and it constrains packaging rather than engineering:**

> **Every usage-based component of the price carries a declared contractual
> ceiling and a reconciliation clause. Air-gapped sites are billed on the
> contractual ceiling, not on measured usage. Connected sites are reconciled
> monthly from a signed usage statement the customer can produce themselves.**

Concretely:

- **The usage statement is a local artefact.** `securityctl usage-statement` (new,
  backlog) emits a signed, dated statement: the counters of §5.1, the period, the
  site, the build, and a digest. It runs offline, with no network, and it is the
  same kind of offline-verifiable artefact as a seal — so a customer can produce
  it, keep it, and we can verify it later with no connectivity. *(The design
  mirrors the checkpoint sink: B7's whole reason is that a seal stored where the
  database can rewrite it is a claim rather than evidence, and the same reasoning
  applies to a usage statement.)*
- **The customer is never charged above the contractual ceiling without a
  conversation.** The ceiling is a number in the contract, visible at signature.
  A bill that surprises a customer is a support incident and a churn event, and
  §7's anti-dark-pattern rules forbid it.
- **We do not require the statement in order for the site to run.** It is a
  billing artefact, not a licence check, and the difference is the difference
  between a supplier and a hostage situation (§7).
- **The engineering consequence, which is a real backlog item:** the counters must
  be monotonic, restart-safe, tamper-evident enough to be signed, and computed
  from Postgres rather than from in-process counters. `08` §2.0 is the rule: a
  counter reading is only ever a live health signal, and a number that appears in
  a customer's invoice is always the Postgres computation. **This is the same
  requirement as the M1 gate 3 evidence, for a different reason, which is the
  cheapest possible way to get it right: do it once.**
- **ADR candidate:** "usage metering and signed offline usage statements" is a
  new contract surface and a new trust boundary, so under `00` §6 it requires an
  ADR before the M2 paid-pilot launch, not at M3.

---

## 6. Offline and air-gapped licensing

> Cross-reference: [`release-sre-and-support.md`](../engineering/release-sre-and-support.md) §3.3, which states the same
> problem and records that **the decision has not been made**. §6.1–6.3 below do
> not change `12`; they propose the options `12` §3.3 asks for and mark the
> decision as an ADR required before M3 GA. [`launch-plan.md`](launch-plan.md) §2.3's technical
> gate is where the ADR is verified as present.

### 6.1 The honest problem statement

Three facts, and any solution that ignores one of them is wrong.

1. **Activation requires a licence.** A paid product must be able to tell a paying
   customer from a non-paying one. That is the entire reason licensing exists.
2. **An air-gapped site cannot phone home.** It is a supported, first-class
   deployment (`12` §3.2, no-internet install from signed media). It may not have
   DNS, egress, a proxy we control, or a vendor portal. **Anything that requires
   egress is a licence check that will fail on exactly the deployments the product
   is strongest in.**
3. **Clock rollback is an attack vector, and we already measure it.** The entire
   clock-drift subsystem exists — `device_clocks.drift_ms` by device and by source
   system, the severe band, the exclusion from time-windowed corroboration
   recorded on the verification result (`AGENTS.md`'s conventions; `08` §2.1;
   `docs/threat-model.md` A1.2). **Any licence that expires on a date enforced by
   the site's own clock is defeated by setting the clock back, and the customer
   who does it may be an administrator, not an attacker — which makes it a
   support incident before it is a security one.**

And the framing that decides everything:

> **A security product that goes dark because a licence lapsed is the one failure
> mode a customer cannot tolerate and we cannot excuse.** `12` §3.3: the product
> never refuses to operate because a licence lapsed. Ever.

### 6.2 The options, and the tradeoff for each

| # | Option | Offline | Enforceable | Customer experience | What it costs us | The tradeoff, stated |
|---|---|---|---|---|---|---|
| **A** | **Signed licence file + hardware fingerprint + bounded offline grace period** | Yes, fully | Partially — it survives image re-imaging, machine replacement and a rolled-back clock, up to the grace period | Never blocked, within the grace period. Degrades **visibly** at the end of it, and the degradation is labelled | A support problem when a machine is replaced; the grace period is a real cliff; the fingerprint raises its own questions | **The best availability story and the most support load.** The fingerprint has to be defined carefully — see §6.4 — because a *hardware serial* creates a re-imaging burden and, in some jurisdictions, a device-identification question. A fingerprint of the **site configuration digest** (device roles, rule bundle version, host identity) is stable, is a thing the customer already has, and says nothing about the hardware |
| **B** | **On-prem licence server** | Yes, entirely | Yes, within the customer's own environment | Never blocked, if they run it. A new component to install, secure, patch, and document, and one more thing that can be down | We build and support a server. It is a new runtime in a product whose value is `00` §6's "a six-month-old release is still installable on a customer's isolated network" | **A licence server is a server.** It is an availability dependency we created, it is an attack surface, and the customer now has a component they must keep alive to keep the product running — which is the exact inversion of the design goal. The ADR should record this in one sentence and the sentence should be uncomfortable |
| **C** | **Periodic re-validation** | No, not on an air-gapped site | Yes, on a schedule | **Blocked when the re-validation window expires** if we enforce it. "Blocked, and we will not help you" is the failure mode | Little | **Non-viable for our customer base.** It requires a server (`00` §3's anti-segment: no cloud-only), it is the one thing `12` §3.3 calls "not viable", and it would disqualify every air-gapped deal in `04` §2's segment 2 and 5. It is listed because it is what most software does and it should be visibly rejected rather than silently absent from the options |
| **D** | **Perpetual, non-expiring licence + contractual renewal tracked in the fleet view** (`12` §3.3's option E) | Yes | No — on site | Never blocked. Ever | A customer who stops paying is a customer we keep supporting until we notice from our own records | **The most honest option and the weakest enforcement**, and the weakness is a commercial problem rather than a technical one. It is also the only option where a security product's behaviour does not depend on a licence, which is a product property, not a commercial one |
| **E** | **Perpetual on site, with a *bounded* offline grace period on the support entitlement only** (a hybrid: D for operation, A for support) | Yes | Partially, and only where it matters | **The site never stops operating.** Support and update entitlement lapse visibly and are re-established by re-validation when a connection exists, or by a phone call | Little, and the support cliff is the part the customer can reason about | **This is the recommendation.** It splits the two things that are currently conflated: *whether the product runs* (never depends on a licence) and *whether we are obliged to support it* (a commercial relationship, which is where a commercial lever belongs) |

### 6.3 The choice, and the ADR

**Chosen: E — a perpetual, non-expiring, signed licence that never gates
operation, with a bounded offline grace period on the support and update
entitlement, re-established by re-validation when a connection exists.**

**ADR required before M3 GA (2028-01-31), covering:**

1. What the licence file is, what signs it, and what the trusted key is at the
   site. (Same trust discipline as the release archive: B6, `verify-release`,
   C8.)
2. The fingerprint definition — **the site-configuration digest, not a hardware
   serial number** — and the enumerated changes that re-issue it without support
   (a re-imaged host, a new application server, a moved evidence volume, a
   changed rule bundle, a changed site id).
3. The grace period's length, and **what happens at its end**: a *labelled,
   visible* degradation of support and update entitlement, recorded in the ledger,
   **and nothing at all to the pipeline**. The site keeps ingesting, verifying,
   raising incidents, storing evidence, sealing checkpoints and replaying.
4. The re-validation path for a connected site, and the phone path for an
   air-gapped one.
5. The **kill switch and the licence are different systems and both must be
   audited** — a licence change and an AI capability disable both appear in the
   hash-chained ledger, and neither can be confused with the other in an audit.
6. **What this explicitly does not do:** it does not phone home, it does not
   require a server of ours, it does not gate operation, and it does not create a
   path by which a lapsed licence degrades a customer's security operations. If
   anyone proposes that last one, it is an ADR that weakens the product's
   central claim, and the argument in `12` §3.3 applies in full.

**Until the ADR lands, every design-partner and pilot agreement already says in
writing that the licence is perpetual and that the product will not be disabled
for non-payment** (`12` §3.3, `04` §3.3 item 9). That sentence is worth more
than whatever enforcement we might have shipped, and it is the sentence a
customer's counsel will point at when the ADR is eventually negotiated.

**The OEM exception, and it is not an exception to the above.** An OEM reseller
*will* ask for enforcement, and it is a different conversation with a different
answer (`12` §3.3): enforcement at the **fleet level, on updates**, not at the
**site level, on operation**. A partner who has not paid receives no new
versions; a partner's running sites never stop. This is written into the OEM
agreement from the start so it is not litigated at M4 with a reseller in the room.

---

## 7. Trial and friction reduction, and the anti-dark-pattern rules

### 7.1 What a prospect gets for free

Three things, and all three are *before* a purchase decision, which is the point:
the friction in this market is not price, it is risk, and risk is removed by
evidence rather than by discount.

| # | What | What it is | What it must not become |
|---|---|---|---|
| **1** | **A read-only evidence evaluation** | Four weeks, no installation at their site, no footprint, no network path. They give us a sanitised export of incident history (`internal/fixture`'s sanitiser is the single choke point, C20); we return a per-class picture: investigation-time distribution, `evidence_completeness`, the class mix, and what fraction of their history a person actually looked at | A trial that runs something on their site and quietly becomes a pilot |
| **2** | **A shadow-mode profile report** | Their own sanitised incident history run through a candidate VLM profile in shadow, with the named gates reported **individually**, the per-class case counts, and the Wilson intervals on every rate (`08` §5.5). They see the profile's limits measured on their footage before it can affect anything — which is exactly what `01` M1 gate 4 did at our own design partners | A benchmark score, a single "accuracy" number, or a claim that is not in the three buyer answers. `08` §8.2 |
| **3** | **A measured before/after on their own incident history** | The `08` §4 design, run per operator, per class, against their own VMS-only baseline, with the effect threshold declared **before** the trial opens and N computed from the M1 baseline variance. Delivered as a report, with all four companions, whether or not it flatters us | A "we reduced your incident time by X%" headline. §7.2's rule 4 |

**The one number that closes most single-site deals** is not a discount, it is
this: the fraction of their own incident history that a human actually
dispositioned. It is produced from their data, by their operator, and it is
frequently a surprise to them in the direction that supports the deal — because
the queue is deeper than anyone believes.

### 7.2 The anti-dark-pattern rules

These are **contract terms**, not policies, and `04` §6.2 item 5 puts them in the
pricing sheet so a prospect reads them before they sign rather than after.

1. **No auto-renewal surprise.** The renewal date, the notice period, and the
   new price at renewal are all on the quote and in the contract, in the same
   document. A renewal that changes a price without a conversation first is a
   breach of this rule, not a commercial decision.
2. **No price increase for a capability the customer already uses**, within the
   term they signed (§1.6). New sites get the current list; existing capability
   does not get repriced.
3. **Export everything, on demand, at any point, at no cost.** Canonical events,
   evidence bytes with their hashes, the audit ledger, the seal file. Delivered in
   a documented format. It is a contractual obligation with a response time, not
   a support ticket. **A customer that believes leaving will be painful is not a
   customer, it is a hostage situation, and the two are indistinguishable from the
   outside and completely distinguishable from the inside.**
4. **No data hostage.** No metric, counter, or feature that is disabled by
   non-payment. The product keeps running (§6.3). Payment is a commercial
   relationship; it is never an availability lever.
5. **No bill above the contractual ceiling without a conversation.** §5.3's
   ceiling is a number in the contract, visible at signature.
6. **Nothing requires a data connection.** No telemetry is a condition of anything.
   The product works with telemetry disabled, offline, air-gapped, and the bill
   reconciles from an artefact the customer produces themselves.
7. **No gated exit.** Uninstall instructions that work, and an export path that
   works, are deliverables in the release (`12` §2.3.1's install unit is the
   precedent: "a site brought up from a signed archive, onto service units, with
   no shell access" is R-08's single metric, and the inverse — removal with no
   vendor involvement — is the same property).
8. **The reference is never hostage to the relationship**, and the customer's
   data is never used for anything not in the written agreement. Both are in
   `04` §3.6's exit terms and both are in the customer contract, because a
   relationship that can be damaged by an exit is an exit that will be negotiated
   badly.
9. **Every number in every invoice is one the customer can compute themselves**,
   from their site, with a command. If we send a number they cannot reproduce,
   it is an error, and the response is to correct the invoice rather than to
   defend it.
10. **A commitment we cannot keep is withdrawn in writing before the date**
    (`05` §3.7). This is a pricing rule in the sense that matters: a price
    promise that lapses quietly is a billing relationship that ends badly.

---

## 8. Unit economics per site

Everything in this section is **ESTIMATE**. §8.4 is the table of what replaces
each estimate, and it is the more important half.

### 8.1 The per-site P&L at list price

| Line | Monthly | Status | Note |
|---|---|---|---|
| Revenue, single site | **$1,200.00** | *(PROPOSED)* | §2.3 |
| Support labour | −$142.50 | *(ESTIMATE)* | 1.5 h/site/month at $95/h fully loaded. **The largest single cost and the least measured.** `12` §7.3's honest support structure at two people is a *lot* of this |
| Verification compute we bear | −$25.00 | *(ESTIMATE)* | A shared evaluation and analytics footprint. **The customer's GPU is their hardware** (`12` §9.3) |
| Evidence storage we bear | **$0.00** | *(real, by architecture)* | Evidence lives on the customer's disk. It becomes a real cost only if we replicate it, and §6.4 forbids replicating evidence bytes |
| **Contribution** | **$1,032.50** | arithmetic | **86% of list** |
| Acquisition cost, direct | −$500.00 amortised | *(ESTIMATE)* | ~50 h of E1+E2 time per single-site close at ~$120/h = $6,000, amortised over 12 months. **Partner-sourced: a 20% royalty on year-one revenue (PROPOSED, §4.1) = $2,880 = −$240/month** |
| **Contribution after acquisition** | **$532.50** direct, **$792.50** partner-sourced | arithmetic | The design-partner / channel motion is worth **1.49×** a direct close in the first year, before counting the reference value that the next three deals inherit |
| **Payback period** | **11.3 months** direct; **3.6 months** partner-sourced | arithmetic | $6,000 ÷ $532.50 = 11.27; $2,880 ÷ $792.50 = 3.63 |

Payback of 11.3 months on a direct close is long, and it is the number that
decides the channel strategy: the channel is not a distribution preference, it is
the difference between **11-month and 4-month payback**, on a motion whose direct
form consumes the scarcest resource in the company (`04` §8.2's 8 hours of E1 per
week, against `00` §6's 60% new-build cap). It is also the number `18` §4.4
promises a growth investor as "a partner-sourced site costs roughly one third as
much to acquire", which is why the target is written here and measured in
`17` §8.4 item 6.

### 8.2 Why evidence storage, not inference, is the cost driver

Inference is the intuition and it is wrong here, for a structural reason: **the
model runs on the customer's GPU.** `12` §9.3 states it as a commercial shape and
it is deliberate — a customer can run the entire product, with no GPU, and get
every claim the product makes; the GPU buys corroboration quality. Our inference
cost is a shared evaluation footprint, roughly flat in sites, and it is the
*smaller* of the two by a wide margin.

Evidence storage is the cost driver, in **three different currencies**:

| Currency | Mechanism | Why it dominates |
|---|---|---|
| **The customer's** | `evidence/day = incidents/day × items_per_incident × mean_item_size × retention_days`. With ~180 KB per snapshot, ~1.2 MB per 8-second clip and ~4 items per incident (~2.7 MB), a site at 30 incidents/day on 30-day retention needs ~73 GB; at 300 incidents/day, ~730 GB *(all ESTIMATE — `12` §9.1)* | **The variable that moves it by an order of magnitude is the rule pack, not the camera count.** A sizing conversation that starts from cameras and never reaches rules produces a disk that fills in month three (`12` §9.1's own conclusion, and operations-playbook §8.3) |
| **Ours, in support and testing** | Every restore, every migration rehearsal, every `doctor` run, every `kill -9` fault-injection run at volume, and every support session that has to look at an incident, touches the evidence store. **Our test and support cost therefore scales with evidence volume, even though our storage bill is zero** | The DR drill, the load runs, the migration rehearsals, and the restore-time figures are all bounded by it, and every one of them is on a milestone exit gate |
| **Commercial** | It is the number a customer discovers in month three, having signed for a licence. It is the number that makes a `12` §9 sizing session necessary before signature rather than after | `slo.go` calls a full evidence disk "the one that ends an incident badly" *(real — `08` §2.1)*: a full disk stops evidence fetches, and an incident without its evidence is not defensible |

### 8.3 The retention and pricing policy that keeps it bounded

| Policy | Rule | Why |
|---|---|---|
| **Default retention** | *(PROPOSED)* 30 days hot, 90 days archived, on the customer's hardware, configurable per site. Indefinite only under legal hold | Bounded by default and explicit by exception. A product that retains indefinitely by default in a customer's building is a product that fills their disk |
| **A hard disk floor, already an SLO** | 10% free, warning at 25% *(real — `internal/observability/slo.go`, SLO 8)* | The SLO is the product, not the price list. And it is an *operational* boundary, not a commercial one |
| **Dry-run-first retention sweep** | The sweeper is run as a dry run and reviewed before it deletes anything, and it respects `legal_hold` — `TestSweepRespectsRetentionAndLegalHold` | `03` §7.3: a manual deletion that skips `legal_hold` destroys the only copy of evidence for an incident that may be in dispute. Incident history is asset A1 |
| **Never bill by evidence volume** | Evidence bundles are counted as a *unit of work we did*, not as bytes stored by the customer | Billing by bytes punishes a customer for having incidents, which is the same mistake as per-camera pricing (§1.1), and it rewards us for a disk filling up |
| **Evidence on the customer's disk, never replicated** | No evidence-byte replication to us, ever, including in the M5 central plane. **A fleet feature that copies evidence between sites is refused, and an ADR that allows it requires an ADR that does not weaken the data-residency claim** | Once a copy exists, the data has left the site, and the entire first call — "we cannot send video to the cloud" — becomes a qualification of a promise rather than a promise |
| **Sizing is a sales artefact** | A sizing session with the rule pack, not the camera count, before the contract, with the formula in writing and the per-site measured constants substituted | §8.2. The number that surprises people must be the number we put in front of them first |

### 8.4 What must be measured, and when — the table that replaces every estimate

| # | Estimate | The measurement that replaces it | When | Method | Consequence of being wrong |
|---|---|---|---|---|---|
| 1 | **1.5 support hours per site per month** | Logged support minutes per site per month | M3 + 1 quarter | The support system, per site, per severity — **not a survey**, because a survey of three design partners is the number `08` §8.1 refuses to publish | The entire contribution margin, and therefore every price and every break-even in this document |
| 2 | **The evidence-per-incident constant (~2.7 MB)** | 30 days of real data per site, per incident class | M2 (`12` §9.4 item 4) | Three design-partner sites, measured | The customer's storage bill and therefore the retention default and the credibility of the sizing session |
| 3 | **AI plane availability in production** | `observations.Stats.Availability()` per site, per month | M1 baseline, then rolling | *(real code — `internal/observations`)*. What is NOT YET KNOWN is its production value | Whether the customer's GPU and our compute allowance cover the promised verification quality, and whether the degraded share makes the product's headline number unreadable |
| 4 | **Verification compute cost** | Actual cost at measured availability across N sites | M3 | A cost-per-resolved-incident computation — **not cost per inference**, which `08` §8.2 says is "optimised by never inferring" | The $25/site/month allowance, and whether local-first is genuinely cheaper for us than an API would be |
| 5 | **Support headcount to coverage ratio** | Analyst-hours per site per month at each rota size | M3, and every quarter after | Rota arithmetic against logged support minutes (measurement 1) | Whether a 4-person rota covers 49 sites or 200, which moves break-even by an order of magnitude |
| 6 | **Acquisition cost per site** | E1+E2 hours per close, by source | M2 onward | The pipeline files in `04` §10 | The 11.8-month payback, and the entire argument for the channel |
| 7 | **Gross margin, honestly** | Contribution ÷ revenue, per cohort | M3 + 30 days | The signed 30-day scorecard (`15` §5.1) | Whether this is a software business or a services business wearing software's clothes, which is the question an M3 investor will ask first |
| 8 | **Churn** | Does not exist below three cohorts | M4 at the earliest | — | Nothing yet, and the number is written here so that when it first appears it is reported rather than quietly modelled |

---

## 9. Procurement readiness

Everything a security director's procurement function will ask for before a
signature. `04` §6.2 item 9 requires **zero blanks** — an unanswered question gets
a "when" and a "why", because a blank is read as a no.

### 9.1 The document set

| # | Document | What it must contain | Owner | By |
|---|---|---|---|---|
| 1 | **Security questionnaire response pack** | The standard 100–200 question set, answered. Every question sourced to `docs/threat-model.md`, a control, a test name, or a repository file. **Unanswered questions carry a date and a reason** | E1, E2 technical | 2027-12-31 |
| 2 | **DPA** | Roles (we are processor, customer is controller, except where the customer is itself a processor for an enterprise — which is the MSSP case in `04` §2.4, and the sub-processor chain has to be modelled). Categories of data (incident records, evidence references, telemetry counters — **and explicitly not video, not biometrics, not person identity**), processing locations (**at the customer's site**), retention and deletion, sub-processors, international transfers (none, in the base product), audit rights, and breach notification timing | E1, counsel | 2027-09-30 |
| 3 | **Insurance** | Cyber liability and technology E&O: limits, retentions, the named-insured list, and — the detail integrators ask for first — the **indemnity structure**, so the partner knows what we will and will not cover. Certificates issued and bound before GA, not after a deal is live (`15` §2.5) | E1 | 2028-01-31 |
| 4 | **Subcontractor list** | Every third party with access to customer data, in a published, dated list: the release-signing key holder if external, the model bundle source, the support tooling, the status page host, and **the two founders, by name**, because in a two-person company the founders *are* the subprocessors. A list of corporate entities that omits the two people who can read a customer's incident history is a list that is wrong | E1 | 2027-09-30, and updated on any change |
| 5 | **Export-control and sanctions screening statement** | A written statement of: the countries in which we will not do business and why; the screening we apply; whether any component we ship is subject to export control; the jurisdiction of incorporation and of every dependency we ship in the release archive, from the **SBOM** (which is a real artefact — `internal/release`, `07` §9.1 line 9); and our position on a customer request to restrict a component. **No claim here is made without the SBOM behind it** | E2 (technical), E1 (statement) | 2027-12-31 |
| 6 | **Data-handling one-pager** | §7.2's nine anti-dark-pattern rules, plus what is stored, where, for how long, and what never leaves | E2 | 2027-09-30 |
| 7 | **AI governance record** | Per profile: model name and version, model hash, prompt template and version, the named gates with their results, the labelled case count, the known limitations **stated in three places — the profile, the product, and the docs** (`05` §3.4), and the deployment history at each site | E2 | With every release, and in the evidence pack at 2027-09-30 |
| 8 | **Acceptable-use and permitted-use statement** | What a model bundle may be used for, how that is enforced (`securityctl model-bundle install` refuses a bundle whose declared permitted uses do not include the profiles we would run — `12` §3.4), and who may change a permitted use | E2 | 2028-01-31 |

### 9.2 The clauses a security director will require on AI

Four, in the order they come up. Each gets the position we will sign and the
reason, because a clause we cannot justify is a clause we should not sign.

#### 9.2.1 Liability for a missed detection

**Our position, and it is not negotiable on the merits, only on the drafting:**

> We do not warrant that the platform will detect any particular security event.
> We warrant that the platform performs the functions described in its
> documentation; that every decision it makes is recorded with its inputs and
> provenance; that any decision it made in the past can be reproduced at your
> site from stored facts with the AI plane absent; that the record of what
> happened is append-only and tamper-evident, and we will disclose to you if it
> is not; and that the fraction of decisions taken on a degraded or absent AI
> signal is a number we measure, report, and make available to you.

**Why we refuse the alternative.** A detection warranty is a promise about a
number this roadmap has decided not to publish (`08` §3.2: a missed-incident rate
requires a labelled replay audit at a known sample size, and quoting a rate
without its N is the exact error the section exists to prevent). Warranting a
number we cannot measure produces two failure modes: we silently over-promise and
then defend a figure we invented, or we under-measure and discover the warranty
was breached by an event we would never have detected. **A liability clause is
the wrong instrument for a capability that is probabilistic, and the right
instrument for a platform that is deterministic about its own behaviour.** Every
warranty above is checkable by the customer, today, in four minutes.

**What we do sign instead:** a severity schedule with response SLIs
(`12` §7.2), a mitigation time, a root-cause time, credits at GA once the
contractual SLIs are staffed, and the disclosure policy's clock (`12` §10.1:
notification before they find out and before we understand it).

#### 9.2.2 Data residency

> All processing of your data occurs on hardware you own, at your site. Video and
> evidence do not leave. The AI runtime runs on a GPU at your site. The audit
> ledger and the evidence store are at your site. We operate no cloud video
> service and this is a permanent product commitment, not a configuration.

**The one thing that has to be honest:** the software licence keys, the release
keys, and the SBOM are ours; the *data* is yours and stays with you. And where a
connected deployment sends optional telemetry counters, the contract says which
counters, that they are aggregate counts and not payloads (§5.2), and that the
product is fully functional with them disabled. A customer who needs zero egress
gets zero egress, and that is a supported configuration rather than a project.

#### 9.2.3 No training on customer data

> We do not train any model on your data. We do not use your data to train,
> fine-tune, evaluate, or improve any model for any customer, with or without
> your consent being sought, and this obligation survives the termination of this
> agreement.

**Three things make this credible rather than a sentence:** the model bundle is
**untrusted input verified before execution** and content-addressed, so replacing
it does not touch your observations (`AGENTS.md`; `12` §3.4); the abstract layer
is ours and the model is a replaceable signed artefact (`00` §7); and
**anonymised evaluation data from a design partner is a separate, written,
specific grant** (`04` §3.1) that is not implied by the licence and is revocable
(`04` §3.6). If the anonymisation is ever in doubt, the customer can run the
sanitiser themselves and hand us a fixture — the sanitiser is a single choke
point in the product, not a service we perform.

#### 9.2.4 Audit rights

> You may audit us. You may verify your own system's integrity at any time
> without us and without a database, using the signed checkpoint and the
> verification command. You may obtain the threat model, the control-to-test
> map, the SBOM, the release provenance, the AI governance record, and our
> accepted-risk list. You may send a security disclosure to a monitored address
> with a published response time, and you may name us in your own audit
> documentation.

**The clause that distinguishes this product from a claim:** *you do not need us
to verify the system, and you do not need a connection to do it.* `securityctl
audit verify-seals` runs with no database and no network, and refuses a seal
verified against a foreign key. A security director who has been told by a vendor
that they can audit the system *by phoning the vendor* has been told they cannot
audit the system. This one is checkable in four minutes and it is the whole
commercial argument, which is why it is the first thing in the pricing sheet
rather than the last thing in the DPA.

### 9.3 The three questions a procurement reviewer will actually ask

Not the questionnaire. The three, in the room, after the questionnaire:

1. **"Show me a decision your system made and prove I can reproduce it."** — The
   four-minute check: `securityctl incident replay <id>` with the AI plane
   stopped, then `securityctl audit verify-seals` offline, then the same seal
   against a foreign key. `04` §5.3's run sheet, steps 7.5–18.5.
2. **"What happens at 03:00 when your company is unreachable?"** — The site keeps
   ingesting, verifying, raising incidents, storing evidence, sealing checkpoints
   and replaying, on hardware the customer owns, with the AI plane entirely
   absent. The honest addition: *and if both of us are unreachable, the answer is
   the same, which is why the runbooks exist* (`12` §7.4's escalation, whose
   third line is "there is nobody beyond this").
3. **"Who at your company can read my incident data?"** — The sub-processor list,
   which names the two founders, plus the architecture, in which the answer is
   "the people who wrote the product, on your site, or in the sanitised form you
   approved" — and a customer who wants to test it can hand us a fixture they
   sanitised themselves.
