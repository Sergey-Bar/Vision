# Financial Plan

Owner: E1, with E2 on cost. Status: living. Last revised 2026-09-28.

Read `00-strategy-and-north-star.md` §6 and §8, `01-master-roadmap.md`,
`04-gtm-and-sales.md` §8, `12-release-sre-and-support.md` §7.3 and §9, and
`17-pricing-and-packaging.md` first. This document is the one in the roadmap
where being wrong is cheapest to discover today and most expensive to discover in
month fourteen, so it is mostly assumptions with the arithmetic exposed.

**Marker convention:** *(ASSUMPTION)* = chosen by us, could be otherwise.
*(ESTIMATE)* = calculated from stated assumptions. *(PROPOSED)* = a target or
price from `17`. *(real)* = read from this repository. *(NOT YET KNOWN)* = does
not exist yet.

> **Every number in this document is an ASSUMPTION, an ESTIMATE, or a
> PROPOSED target. None is a measurement.** The only measured numbers available
> today are in `docs/status.md` (build state) and `12` §9.1 (sizing inputs, whose
> derived costs are explicitly ESTIMATE). `08` §3.2 lists the numbers this
> roadmap refuses to state; a financial model is a set of assumptions, and the
> only discipline available is to say which ones.

---

## 1. The assumptions block

Everything the model rests on, in one place, with the reasoning.

### 1.1 The state today

| Assumption | Value | Status | Note |
|---|---|---|---|
| **Founder salary** | **$0** | *(ASSUMPTION)* | Both founders are unpaid. This is the single most load-bearing assumption in the document and it is not sustainable; §1.3 says when it stops |
| **Funding** | **None** | *(ASSUMPTION — real)* | No investment, no revenue, no grant. The company is funded by two people who are not being paid |
| **Corporate cash on hand** | **Unknown** | *(NOT YET KNOWN)* | Not asked, and it is the first number a founder must produce before this model is used for anything |
| **Incorporation** | Not stated in this repo | *(NOT YET KNOWN)* | Affects the export-control statement (`17` §9.1 item 5), the insurance, and the sub-processor list |
| **Model window** | 2026-10-01 → 2028-03-31 (18 months) | *(ASSUMPTION)* | Chosen to span M1's exit (2027-03-31), M2's exit (2027-09-30), the M2 paid-pilot announce, and the M3 GA target (2028-01-31) plus two months of GA operation |
| **Milestone dates** | As in `01` | *(real)* | A slip is modelled in §2.3; it is not re-derived here |
| **Price points** | Founding $600/site/month; list $1,200/site/month | *(PROPOSED)* — `17` §2.3 | Hypotheses. `17` §1's opening rule applies: not quotable until three referenceable GA customers |

### 1.2 Cost assumptions, with the reasoning

| Line | Value | Status | Reasoning and the measurement that replaces it |
|---|---|---|---|
| **Base tooling** | $400/month | *(ESTIMATE)* | Hosting, domains, email, source control, artefact storage for release builds, error tracking, password manager, and LLM API tokens for the AI-assisted development that `03` §2 is built on. `03` §2's roster is the reason this line exists at all. **Replaced by:** the actual invoice, which is a five-minute exercise and has not been done |
| **GPU rental for profile evaluation** | $900/month, 2026-11 → 2027-09 | *(ESTIMATE)* | M1 exit gate 4 needs a production VLM profile on a real GPU, and M2's ten-profile library needs more. `12` §9.2: a 16–24 GB VRAM GPU is the AI plane, and it is the customer's in production. **Replaced by:** measured cost per profile evaluation, from `12` §9.4 item 5 |
| **Demo/staging environment** | $150/month, from 2027-10 | *(ESTIMATE)* | Exists because GA prospects get a self-serve install at T+14 (`15` §2.1 cohort 3) and `04` §5.4's environment must not be a customer's site. **Replaced by:** the same |
| **Design-partner agreement, first legal review** | $4,000 one-off, 2026-12 | *(ESTIMATE)* | A template, reviewed once, that the design-partner agreement is derived from. `04` §3.3's eleven sections. **Replaced by:** the invoice |
| **DPA + privacy review** | $1,500 one-off, 2027-04 | *(ESTIMATE)* | `17` §9.1 item 2. **Replaced by:** the invoice |
| **MSA + sales terms review** | $5,000 one-off, 2027-09 | *(ESTIMATE)* | `17` §9.1. The single largest legal line and the one that most directly gates revenue. **Replaced by:** the invoice |
| **Cyber liability + technology E&O** | $650/month from 2027-10 | *(ESTIMATE)* | ~$7,800/year. Premiums for a pre-revenue security software company are small in absolute terms and hard to get without a threat model, a security review pack and two named founders. **Replaced by:** a broker quote, which should be obtained in M1 and not in the GA quarter |
| **Travel and on-site work** | $400 (2026-10/11), $600 (2026-12), $1,200 (2027-01→09), $1,800 (2027-10→2028-03) | *(ESTIMATE)* | Three design-partner sites with weekly written updates, bi-weekly incident reviews, monthly measurements and on-site installs, plus a pilot deployment and then GA installs. `04` §8.2's 8 hours/week of E1's time is *not* travel — this line is the other half of the same cost and it rises with customer count. **Replaced by:** the count of site visits per month, which is a calendar, not an estimate |
| **Hire 1 — Deployment & Support** | 2027-07-01, $95,000/yr fully loaded = **$7,900/month** | *(ASSUMPTION)* | The first hire is a deployment/support engineer, not a product engineer, and the reason is in `03` §1.2: R-08 (SRE) and R-11 (support) are the roles most likely to be quietly dropped at two people, and the install path (`securityctl install`, service units, transactional upgrade) is the M3 GA critical path. **Replaced by:** a signed offer |
| **Hire 2 — Solutions Engineer** | 2028-01-01, $110,000/yr fully loaded = **$9,150/month** | *(ASSUMPTION)* | `03` §1.2 R-12 exists as an agent-drafted, E1-signed role from M2 and becomes a person at the third site. A solutions engineer who can carry a deployment and a measurement is the difference between E1 selling and E1 engineering. **Replaced by:** a signed offer |
| **Founder salary, when it starts** | $7,500/month combined ($3,750 each), from 2027-01 | *(ASSUMPTION)* | Deliberately below market and stated as such. It starts the month after tranche 1 closes (§4). **If tranche 1 slips, this does not slip** — which is the whole problem, and §7.2 |
| **Fully-loaded cost multiplier** | The hire figures are all-in (salary + employer taxes + benefits + equipment) | *(ASSUMPTION)* | Finance calls this "total cost of employment". A model that adds salary and forgets the rest understates payroll by 25–30% |
| **Loaded hour rate for cost attribution** | $95/hour (engineering) and $120/hour (E1/E2, valued at the opportunity cost of the scarcest resource) | *(ESTIMATE)* | Used only in `17` §8.1's acquisition cost, never in the cash model |

### 1.3 What the model deliberately does not include

| Excluded | Why, and when it enters |
|---|---|
| **Any M4+ work** | M4 begins 2028-04-01, one day after this window closes. §5 and §4 cover it; putting it in an 18-month cash model would be a forecast wearing a table's clothing |
| **The 24/7 support rota** | `12` §7.3: we do not sell 24/7 until a rota of ≥4 exists. The base scenario does not have one, so the base scenario does not sell 24/7. §3.2 shows what happens when we do |
| **Hardware resale** | `00` §3 anti-segments: not a hardware ecosystem. The customer buys the GPU; `12` §9.3 says the GPU is optional and its absence degrades verification quality rather than disabling the product |
| **Cloud video or evidence storage** | The product is local-first by architecture. Evidence storage is $0 to us and §6.2 of `17` forbids replicating evidence bytes ever |
| **Currency, tax, VAT** | Assumed nil or absorbed. A single-country, two-person, pre-revenue company; if this is wrong it is a §9.1 correction |
| **The cost of a security incident** | Not modelled, because it is not estimable. §7.1 is the answer to that, and the answer is a policy rather than a number |

---

## 2. The monthly cash model

### 2.1 Base scenario

Window 2026-10-01 → 2028-03-31. All figures USD. Design-partner revenue is $0
throughout (`01` §M1: no charge, in exchange for a reference, a measured
before/after, and evaluation data). First paid revenue is modelled in
**2027-05**: M2 opens 2027-04-01, but a paid pilot requires the design-partner
review of the M1 exit-gate pack (`04` §4.1) which happens at the 2027-03-31
boundary, so 2027-04 is the contractual floor and 2027-05 is the first realistic
month.

| Month | Payroll | Infra | Legal / insurance | Travel | **Revenue** | **Net** | Cumulative |
|---|---|---|---|---|---|---|---|
| 2026-10 | 0 | 400 | 0 | 400 | 0 | **−800** | −800 |
| 2026-11 | 0 | 1,300 | 0 | 400 | 0 | **−2,100** | −2,500 |
| 2026-12 | 0 | 1,300 | 4,000 | 600 | 0 | **−5,900** | −8,400 |
| 2027-01 | 7,500 | 1,300 | 0 | 1,200 | 0 | **−10,000** | −18,400 |
| 2027-02 | 7,500 | 1,300 | 0 | 1,200 | 0 | **−10,000** | −28,400 |
| 2027-03 | 7,500 | 1,300 | 0 | 1,200 | 0 | **−10,000** | −38,400 |
| 2027-04 | 7,500 | 1,300 | 1,500 | 1,200 | 0 | **−11,500** | −49,900 |
| 2027-05 | 7,500 | 1,300 | 0 | 1,200 | 1,200 | **−8,800** | −58,700 |
| 2027-06 | 7,500 | 1,300 | 0 | 1,200 | 1,200 | **−8,800** | −67,500 |
| 2027-07 | 15,400 | 1,300 | 0 | 1,200 | 1,200 | **−16,700** | −84,200 |
| 2027-08 | 15,400 | 1,300 | 0 | 1,200 | 1,800 | **−16,100** | −100,300 |
| 2027-09 | 15,400 | 1,300 | 5,000 | 1,200 | 1,800 | **−21,100** | −121,400 |
| 2027-10 | 15,400 | 550 | 650 | 1,800 | 1,800 | **−16,600** | −138,000 |
| 2027-11 | 15,400 | 550 | 650 | 1,800 | 1,800 | **−16,600** | −154,600 |
| 2027-12 | 15,400 | 550 | 650 | 1,800 | 2,400 | **−16,000** | −170,600 |
| 2028-01 | 24,550 | 550 | 650 | 1,800 | 2,400 | **−25,150** | −195,750 |
| 2028-02 | 24,550 | 550 | 650 | 1,800 | 3,600 | **−23,950** | −219,700 |
| 2028-03 | 24,550 | 550 | 650 | 1,800 | 4,800 | **−22,750** | **−242,450** |

**Column totals:** payroll **$211,050** · infra **$18,000** · legal/insurance
**$14,400** · travel **$23,000** · revenue **$24,000** · **net −$242,450**.

Arithmetic check: 211,050 + 18,000 + 14,400 + 23,000 = 266,450 of cost, less
24,000 of revenue = 242,450. ✓

**The three things this table says, none of which is about the product:**

1. **Revenue is 8% of cost.** Six sites at the end of 18 months, against two
   hires and a founder salary. The company is funded, and the funding is not
   revenue. Anyone reading this as a commercial trajectory is reading it wrong.
2. **Payroll is 87% of the cost**, and 36% of the total is the two founders
   taking $3,750 each. A company whose largest cost line is its founders'
   partially-restored salaries is a company that is one bad quarter from
   renegotiating them, which is a §7.2 risk.
3. **The cash model does not break even inside the window, and does not break
   even after it either at this ramp.** §3.

### 2.2 Scenario A — the base case, restated as a line item

For clarity, because every other scenario is a delta against it:

| | 2026-10 → 2027-06 | 2027-07 → 2027-12 | 2028-01 → 2028-03 |
|---|---|---|---|
| Founder salary | 0, then $7,500 from 2027-01 | $7,500 | $7,500 |
| Hires | 0 | H1 at $7,900 from 2027-07 | H1 + H2 = $17,050 from 2028-01 |
| Sites (revenue) | 0 → 0 | 2 → 4 (founding $600) | 4 → 6 (2 at list $1,200) |

### 2.3 Scenario B — the paid pilots delay six months

**What slips and why.** `01` §M2's exit gate is a *measurement* whose design must
be written and signed before the trial opens, whose N is computed from the M1
baseline variance, and whose trial extends rather than moving its effect
threshold (`08` §4.4). A six-month delay is therefore not a sales problem, it is
`05` §7.2's evidence-to-move-a-boundary: the gate is not met, the artefact is
absent, and the date moves.

**What follows from it:** paid pilots start 2027-10 instead of 2027-05; **H1
slips three months to 2027-10** because `03` §1.2 R-13 says hiring is planned as
capacity and never as rescue, and hiring a deployment engineer before there is a
deployment to support is a rescue; H2 slips two months to 2028-03.

| Month | Net (Scenario B) | Cumulative (B) | Δ vs base |
|---|---|---|---|
| 2026-10 → 2027-06 | identical to base | −67,500 | 0 |
| 2027-07 | −7,500 (founder salary only) | −75,000 | +9,200 |
| 2027-08 | −7,500 | −82,500 | +17,800 |
| 2027-09 | −12,500 (no H1, + legal $5,000) | −95,000 | +26,400 |
| 2027-10 | −12,500 (H1 starts) + 1,200 rev = −11,300 | −106,300 | +31,700 |
| 2027-11 | −11,300 | −117,600 | +37,000 |
| 2027-12 | −11,300 + 600 = −10,700 | −128,300 | +42,300 |
| 2028-01 | −10,700 | −139,000 | +56,750 |
| 2028-02 | −10,700 | −149,700 | +70,050 |
| 2028-03 | −18,850 (H2 starts, + 600 rev) | −168,550 | +73,900 |

**Scenario B total: −$168,550. Cash preserved versus the base case: $73,900.**

**This is the finding, and it is counter-intuitive enough to be worth
underlining:** *a six-month delay in the M2 gate makes the 18-month cash position
$73,900 better.* The reason is that at 2–6 sites the revenue at stake is
$24,000, while the payroll the delay avoids is $98,000. A delay looks like bad
news and is arithmetically good, because the company is too small for a delay to
cost much revenue and the delay is expensive in time, not in cash.

**The cost of Scenario B is entirely personal and temporal, not financial:**

| What gets worse | By how much |
|---|---|
| Time to break-even | 6 months later (§3.3) |
| Founder salary months before revenue covers any of it | 6 more months, at $7,500/month = **$45,000** of personal draw |
| Founder personal reserve exhaustion | 6 months earlier (§7.2) |
| M4's start | Unchanged on paper, and that is the lie: M4 is a 2028-04 milestone and a 6-month-later company enters it 6 months into a window it cannot staff (§5) |
| Competitive position | Six months of a competitor being visibly in market (`15` §6.2), against a company with no reference beyond its own design partners |

**And the reason it is nevertheless a bad scenario:** the $73,900 is not a
saving, it is deferred payroll. H1 and H2 have to be hired anyway; Scenario B
moves three and two months of cost past a cash window, and the cash is smaller
than the pipeline the delay costs. At tranche-2 scale, where payroll is $2.3M a
year, a six-month delay costs six months of a 2.3M/yr cost base — approximately
$1.15M — against revenue that by M4 is still small. **The arithmetic inverts as
the company grows, which is why "a delay saves cash" is true only while you are
small, and why the delay must be managed as a date rather than as a budget
variance.**

### 2.4 Scenario C — one hire earlier (H1 at 2027-04 instead of 2027-07)

Everything in the base case, with H1 starting three months early. The only
difference is three extra months of $7,900 = **$23,700**.

| | Base | Scenario C | Δ |
|---|---|---|---|
| Payroll | $211,050 | $234,750 | +$23,700 |
| All other costs | $55,400 | $55,400 | 0 |
| Revenue | $24,000 | $24,000 | 0 |
| **Net, 18 months** | **−$242,450** | **−$266,150** | **−$23,700** |

**When this is the right decision, and it is not obviously the wrong one:** H1 is
a deployment and support engineer, and `12` §7.3's honest support structure says
a Sev-1 outside business hours *wakes one person*. If M2 is on track to sign its
first external customer while the only person who can install it is E2, and E2
is also the person who cannot be in a customer call during a release week
(`04` §8.4), then the release train and the deployment collide every time. Moving
H1 forward by one quarter is paying $23,700 to remove a structural conflict
between two commitments that are both in the plan.

**The counter, which is also true:** in a scenario where the M2 gate is at risk,
hiring a person who will have nothing to deploy for a quarter is a cost with no
return, and `03` §1.2 R-13's rule exists for exactly that. **The decision rule:
hire H1 forward if and only if at least one paid pilot has a signed start date.
No signed start date, no early hire.** The decision date is 2027-03-31, at the M1
exit review, and it is a one-line check.

### 2.5 Runway

| Measure | Base | Scenario B | Scenario C |
|---|---|---|---|
| Corporate cash consumed, 18 months | $242,450 | $168,550 | $266,150 |
| Peak single-month burn | $25,150 (2028-01) | $18,850 (2028-03) | $25,150 |
| Months of corporate cash at the final run rate | 9.6 | 8.9 | 10.6 |
| Cash break-even | **see §3.3** | 6 months later | ~1 month earlier |
| **Founders' personal runway (the real constraint)** | **see §7.2** | 6 months less | unchanged |
| Funding required to reach cash break-even | **$750,000** (§4.1) | **$650,000** | **$800,000** |

**The "months of runway" row is nearly meaningless and is included because
people ask for it.** Corporate cash is a small number against a small burn rate,
so the ratio is large and reassuring, and it is not the constraint. The
constraint is §7.2.

---

## 3. Break-even analysis

### 3.1 The inputs, and where they come from

| Input | Value | Source |
|---|---|---|
| Revenue per site, list | $1,200.00/month | *(PROPOSED)* `17` §2.3 |
| Revenue per site, founding rate | $600.00/month | *(PROPOSED)* `17` §2.3 |
| Variable cost per site | **$167.50/month** = support $142.50 + compute $25.00 + storage $0.00 | *(ESTIMATE)* `17` §8.1. **The measurement that replaces it is measurement 1 in `17` §8.4: logged support minutes per site per month. Everything below is only as good as that number, which does not exist** |
| Contribution per site, list | **$1,032.50/month** (86%) | arithmetic |
| Contribution per site, founding | **$432.50/month** (72%) | arithmetic: $600 − $167.50. Identical to `17` §2.4, which is where the table lives |
| Fixed cost, GA without 24/7, founders unpaid | $20,050/month | payroll $17,050 + infra $550 + insurance/legal $650 + travel $1,800 |
| Fixed cost, GA without 24/7, founders paid | **$27,550/month** | the above + $7,500 |
| Fixed cost, GA **with** 24/7 rota, founders paid | **$57,550/month** | the above + a 4-person rota at $7,500/month each |

### 3.2 The four break-evens

Break-even condition, for a fleet of *N* sites of which 4 are grandfathered at the
founding rate (the actual state of the first 12 sites, `17` §2.5):

```
  revenue(N) − 167.50·N ≥ fixed
  2,400 + 1,200·(N − 4) − 167.50·N ≥ fixed
  1,032.50·N ≥ fixed + 2,400
```

| Fixed cost basis | Fixed | Break-even, all sites at list | Break-even, with 4 founding |
|---|---|---|---|
| GA, no 24/7, **founders unpaid** (today's state) | $20,050 | **20 sites** | **22 sites** |
| GA, no 24/7, **founders paid at $7,500** (post-raise) | $27,550 | **27 sites** | **29 sites** |
| GA, **with** 24/7 rota, founders unpaid | $50,050 | **49 sites** | **51 sites** |
| GA, **with** 24/7 rota, **founders paid** | $57,550 | **56 sites** | **58 sites** |

Arithmetic, all four rows, from the formula in `17` §2.4:
`N ≥ fixed / 1,032.50` for an all-at-list fleet, and
`N ≥ (fixed + 2,400) / 1,032.50` for the actual mix of 4 founding-rate sites.
20,050 ÷ 1,032.50 = 19.4 → 20; 22,450 ÷ 1,032.50 = 21.7 → 22.
27,550 ÷ 1,032.50 = 26.7 → 27; 29,950 ÷ 1,032.50 = 29.0 → 29.
50,050 ÷ 1,032.50 = 48.5 → 49; 52,450 ÷ 1,032.50 = 50.8 → 51.
57,550 ÷ 1,032.50 = 55.7 → 56; 59,950 ÷ 1,032.50 = 58.0 → 58.

**The finding, which is the most important commercial number in this document:**

> **The 24/7 support promise costs 29 additional customers, and the founders
> taking a salary costs 7 more.** Together they move break-even from 20 sites to
> 58 — from a number a two-person company can conceivably reach by selling, to a
> number that is only reachable through a channel.
>
> This is why `12` §7.3 says we do not sell 24/7 until the rota exists, and why
> `17` §2.4 prices it visibly. A support promise is a fixed cost with no ceiling
> on the customers required to carry it, and in this business it is larger than
> the product's entire engineering cost.

### 3.3 When, in calendar terms

At **+1 net new site per month** from 2028-04 (the base ramp, which is what
`04` §10's stage definitions imply for two people with 8 hours a week), starting
from 6 sites at 2028-03:

| Target | Sites | Additional | Reached |
|---|---|---|---|
| Break-even, all at list, no 24/7, founders unpaid | 20 | 14 | **2029-05** |
| Break-even, 4 founding, no 24/7, founders paid | 29 | 23 | **2029-12** |
| Break-even, 4 founding, with 24/7, founders paid | 58 | 52 | **2032-04** |

**Three findings, and the third is the strategic one:**

1. **No 24/7, founders paid: break-even is late 2029** — inside M5, in the middle
   of the enterprise milestone, with capital required throughout. This is
   survivable and it is not comfortable.
2. **With 24/7, founders paid: break-even is 2032** — the year *after* M8. The
   24/7 model at the current price and the current ramp is not a business, it is
   a plan that is funded forever. It only works at a higher price, a lower cost
   base, or a much larger channel.
3. **The channel is not a distribution preference; it is the only route to
   break-even at scale.** At +1 site/month we sell what one person can sell. A
   channel that adds 5 sites/partner/month changes the slope of the entire
   model. This is the financial form of `04` §7.1's argument and the two
   documents have to agree, which is why `04` §7.1 states the reseller's
   12–18 month delay as plainly as its leverage.

### 3.4 Sensitivity

| Variable | Change | Effect on break-even | Effect on cash before break-even |
|---|---|---|---|
| **Price** | −$300/site/month → contribution $732.50 | 20 → **28** sites (all at list, founders unpaid); the founders-paid case 27 → **38** | Worse. The model is more sensitive to price than to cost because the fixed base is small relative to the growth rate |
| | +$300 → contribution $1,332.50 | 20 → **16** sites | **≈ $57/month of price is worth one fewer site to carry; ≈ $30/month is worth one more.** Check: 20,050 ÷ 1,332.50 = 15.0 and 20,050 ÷ 1,002.50 = 20.0. That is the arithmetic behind `17` §2.4's conclusion that the premium tiers are where the margin is |
| | Price → $0 | Never. Contribution is negative: −$167.50/site/month | We pay $167.50 per site per month to serve it |
| **Sales cycle length** | GA cycle 3 → 6 months | Unchanged in *count*, +3 months in *time*. Break-even moves 2029-12 → 2030-03 | **$82,650** of additional burn at the founders-paid fixed base of $27,550 × 3 months. At the 2028-03 run rate of $22,750/month it is $68,250, and the relevant window is the base scenario's 2028-04 to 2028-12 stretch, so ≈ $70,000 |
| **M2 gate duration** | Each month of delay | +1 month on every date downstream | **$17,900 per month** once H1 is hired ($15,400 payroll + $1,300 infra + $1,200 travel), and **$10,000 per month** before it ($7,500 + $1,300 + $1,200). The delay is cheaper before the hire and more expensive after, which is why `04` §8.3's M2 collision resolves in favour of the measurement design rather than the pricing experiment |
| **Support hours per site** | 1.5 → 4.0 h/month (the realistic figure for a 24/7 rota covering a small customer base) | Variable cost $167.50 → $405.00, contribution $795.00. Break-even 20 → **26** sites | **−$237.50/site/month** of contribution. At 6 sites that is $1,425/month of additional loss; at 30 sites, $7,125/month. **This is the variable with the least data and the largest effect, and it is measurement 1 in `17` §8.4** |
| **24/7 sold** | +$1,200/site/month uplift, +$30,000/month fixed | Revenue/site $2,400, contribution $2,232.50, fixed $57,550. Break-even 56 → **26 sites** | A 24/7 price of $1,200/site/month more than halves the site count required, because it is the only change that raises contribution as fast as it raises fixed cost. **The uplift is the whole business case for the rota, which is why `17` §2.3 prices it and `15` §2.3 makes "the price is not signed off" a commercial-gate blocker** |

**The sensitivity table's own conclusion, which is a strategy and not a number:**
break-even is far more sensitive to **price** and to **whether 24/7 is sold at a
price that covers it** than to anything the engineers do. The engineering risk in
this plan is a slipped milestone. The commercial risk is that we reach GA with a
price that does not carry the support promise, which is why `17` §2.3's list
price and `15` §2.3's commercial gate are one decision taken in two documents.

---

## 4. The capital plan

### 4.1 Tranche 1 — pre-seed, now

**Ask: $750,000.** *(PROPOSED — a number with the arithmetic below, and the
arithmetic is only as good as `17` §8.4's unmeasured constants.)*

| Use | Amount | When |
|---|---|---|
| Corporate burn, 2026-10 → 2028-03 (the base scenario) | $242,450 | 18 months |
| Corporate burn, 2028-04 → break-even (2029-12, 21 months at an average net of ~−$13,000) | ~$273,000 | 21 months |
| **Sub-total, to cash break-even** | **~$515,000** | |
| Contingency at 50% | ~$258,000 | A security company. A six-month slip costs ~$74,000 (Scenario B's *timing*, not its cash), an incident costs a month of both founders, and the three references are a single point of failure (§7.1) |
| **Total requirement** | **~$773,000** | |
| **Ask** | **$750,000** | |

**The ask is slightly below the computed requirement and that is deliberate:** a
raise that is fully allocated to the bottom line arrives with no room to respond
to the first surprise, and the first surprise in a pre-seed is not usually a cost,
it is an opportunity that needs three months. Stating the gap openly
("this takes us to break-even with about 3% headroom, and here is the month we
find out whether that is enough") is a better posture with an investor than a
number that is round and undefended.

**What it explicitly does not buy:** one dollar of M4. §4.3.

**When the money is needed, precisely:**

| Date | Event | Cash position if tranche 1 has closed |
|---|---|---|
| **2026-12-31** | Tranche 1 targeted close | −$8,400. Founder salary starts 2027-01 |
| **2027-06-30** | **The founders' runway decision date** (§7.2) | −$67,500 |
| 2027-09-30 | M2 exit gate; Scenario B decision point | −$121,400 |
| 2028-01-31 | M3 GA | −$195,750 |
| 2028-03-31 | End of the model window | −$242,450 |

### 4.2 Tranche 2 — seed, at the M2 gate

**Ask: $1,500,000–$2,500,000, taken at the M2 exit gate in late 2027, not
earlier.** *(PROPOSED.)*

The argument for waiting for M2 rather than raising earlier: everything an
investor can diligence at M2 — the M2 measurement report with its N, the
pre-declared effect threshold, the per-class distribution, the four companions,
and `17` §8.4's estimate-to-measurement table partially filled — is **the same
evidence that sets our price**. Raising before it means selling the plan twice
and proving it once, and the second time is cheaper.

**The argument for taking a first close earlier**: the founders' salary is a
$7,500/month obligation that tranche 1 is funding, and if tranche 1 is late then
tranche 2 being late as well is a compounding failure. The mitigation is not to
raise earlier; it is §7.2's decision rule.

### 4.3 Tranche 3 — the M4/M5 build

**Ask: $3,000,000–$3,500,000, in 2028.** *(PROPOSED.)*

Sized from §5's headcount, at the point M4 begins:

| Line | Annual | 18 months (M4 + half of M5) |
|---|---|---|
| 4 more engineers at $115k | $460,000 | $690,000 |
| The 24/7 rota, 4 people at $90k | $360,000 | $540,000 |
| 1 solutions engineer, 1 channel manager at $110k | $220,000 | $330,000 |
| 0.5 sales, 0.5 compliance, 0.25 finance | $160,000 | $240,000 |
| Infra, travel, legal, insurance | $600,000 | $900,000 |
| **Total** | **$1,800,000** | **$2,700,000** |
| Revenue offset (M4 partial, M5 early) | | −$400,000 |
| **Tranche 3 ask** | | **~$3,100,000** |

**What tranche 3 buys, in the order it matters:** the second VMS adapter and the
Adapter SDK v1 (the moat — `01` M4, the highest-leverage engineering in the plan);
the 24/7 rota that makes the GA support promise sellable; the 4-person channel
attempt; and the two years of runway needed to reach `01` M6's compounding-claim
review, which is the first gate in this roadmap that tests whether the business
has a moat or just a good product.

### 4.4 Who fits at each stage, and what they will diligence hardest

| Stage | Investor types that fit this profile | What they will diligence hardest | The evidence that answers it |
|---|---|---|---|
| **Tranche 1 / pre-seed, $750k, now** | (a) **Angels who have run physical security or a SOC** — a former monitoring-desk manager, a physical-security product leader, a VMS OEM's former channel director. They are worth more than the money because they are the `04` §2 pipeline. (b) **Tiny pre-seed funds with a security-infrastructure thesis** that can write a $400k cheque. (c) **Accelerators**, for the $1,200 of cloud and dev spend and the mentorship rather than the capital — accepting 6–10% dilution for that is a rational trade at this stage | **The architecture, and the two people.** Specifically: can this be built by two engineers, and does the claim of provability rest on something other than a design intention? The first question is answered by `05` §2.1's honest arithmetic (78 engineer-weeks of M1 work against 31 of capacity, with the cuts named in advance). The second is answered by the five invariants, `docs/threat-model.md`'s 35 controls each mapped to a test that fails when the control is removed, and the `kill -9` fault-injection matrix. **An investor who asks to see the fault-injection matrix is the right investor** | `docs/status.md` (21 packages green, 27 of 112 backlog rows green, the "what is not built" section stated plainly); the threat model; the fault-injection stage table; `05`'s backlog with per-row effort, risk and the test that fails; `00` §6's capacity law with the 40% tax made concrete in `04` §8.2 |
| **Tranche 2 / seed, $1.5–2.5M, at M2 (late 2027)** | Seed funds that have backed a security or industrial product and can follow a hardware-adjacent deployment motion; a **corporate/strategic** investor from the VMS or the access-control side who wants a layer rather than a competitor — the hardest to raise from and the most aligned if it works, because they arrive with a customer pipeline we cannot build (`04` §7.1) | **Unit economics and the measurement.** The question is not "is the demo impressive" — it is "is the MTTI reduction real, and did you design the study to produce it". The answer is `08` §4 written and signed **before** the trial opened, with the effect threshold declared in advance, N computed from the M1 baseline variance, counterbalancing, the Hawthorne countermeasures, and the named way the study gets faked (moving the threshold after seeing the data, pooling classes to reach significance). **The written-before design, not the result, is the diligence artefact.** Second: `17` §8.4's table, with the first two rows measured | The signed M2 measurement report with N, the per-class distribution for both arms, the four companions unprompted, the operator's own assessment in their words, and `17` §8.4 items 1 and 2 (support hours, evidence-per-incident) measured at three sites |
| **Tranche 3 / M4–M5, $3.1M, 2028** | Growth-stage funds that have taken a seed-stage security infrastructure company to an enterprise motion; **the OEM's corporate venture arm**, which is the strategic asset (distribution, credibility, a pipeline) and the hardest to sign | **The enterprise sales motion and the channel.** Is the motion repeatable or is it E1? The evidence is `04` §10's pipeline files with the stage definitions and entry conditions, the closed-won count, the `LOSS REASON` distribution, and CAC and payback from `17` §8.1. And: does one partner relationship distribute a segment — sites added per partner-month, the partner's own support cost per site, and whether a partner-sourced site costs one third as much to acquire | Three named production references who have each seen a price and said yes at approximately that level (`04` §4.2's publication condition); the closed-won count with the decision units; the partner pilot agreement; and the M4 exit gate's non-adapter diff, which is the artefact that proves we are a layer and not a product for one VMS |

**What no stage is asked to believe, because it is not measurable today:** a
market size, an incident volume, a false-positive rate for any industry, a churn
number, or a "we prevent X%" figure. `00` §8 and `08` §3.2. **If an investor
presses for one, the correct answer is the measured version — one site, one
denominator, one method, dated — and a refusal to generalise from it.**

---

## 5. Cost structure by milestone: the honest part

### 5.1 What the engineering actually costs

`05` §2's effort estimates, converted to headcount. One engineer's new-build
capacity per 13-week quarter is **7.8 engineer-weeks** (`00` §6: 13 weeks at 60%
on new build). Both the low and the top of each effort range are shown, because
`05` §2.1's own argument is that the plan is built against the top of the range
and the low end is a plan nobody should bet on.

| Milestone | Window (weeks) | New-build demand, low end (ew) | Top end (ew) | Engineers needed, low | Engineers needed, top |
|---|---|---|---|---|---|
| M4 — Universal Security Layer | 2028-04-01 → 2028-12-22 (38) | 53.5 | 78 | **2.3** | **3.4** |
| M5 — Enterprise Command | 2028-01-04 → 2029-09-28 (39) | 46.5 | 68 | **2.0** | **3.0** |
| M6 — Site Intelligence | 2029-10-01 → 2030-06-28 (39) | 27 | 44 | **1.2** | **1.9** |
| M7 — Autonomous Operations | 2030-07-01 → 2031-06-30 (52) | 25 | 50 | **0.8** | **1.6** |
| M8 — Physical Security OS | 2031-07-01 → 2032-12-31 (78) | 24 | 42 | **0.5** | **0.9** |

Computation, M4 top end: rows 70–81 at L=6, XL=10, M=3, S=1 →
6+10+3+10+1+10+3+6+3+10+10+6 = 78 ew. 78 ÷ (38 × 0.6 = 22.8 ew per engineer) =
**3.4 engineers**. ✓

**This is not the headline, and the headline is not in the table.** M4's
*engineering* need is 3.4 people. Two of them are the founders. So the engineering
case for M4 is a modest hire, and it is worth saying so plainly, because the
prose version of "M4 onwards is not a two-person company" is usually about
engineering and the engineering is the easy part.

### 5.2 What M4 actually costs, which is not engineering

| Obligation | Starts | Why it cannot be done by an engineer who is also the support rota |
|---|---|---|
| **24/7 support rota, 4 people** | M3 GA, 2028-01 | `12` §7.3: 24/7 is a contractual promise with credits, and `15` `T90-3` makes the decision to have the rota a go/no-go item. Four people, because a rota of one is a phone and a rota of two means neither person ever sleeps. **This is the single largest headcount item in the plan and it is caused by a support commitment, not by the roadmap** |
| **ONVIF Profile M interop** | M4 | The engineering estimate counts the code. The real cost is debugging against other vendors' actual devices, which is unbounded and is not in any estimate. **Rule of thumb, not a claim: interop work runs 2–3× the coding** |
| **The second VMS adapter deployed to a real customer** | M4 gate | `01` M4's gate requires it in production at one site with a zero non-adapter diff. A production deployment is `12` §2.3.1–2.3.4's install, upgrade, backup and restore work against a site we do not control |
| **Channel, continuously** | M4, from the first conversation | 15–30 months to first revenue (*PROPOSED*, `04` §7.3), consuming E1's calendar the whole time, with no revenue until the end. This is a *person*, not a project, because a relationship is a person |
| **Solutions and deployment** | M3 | `03` §1.2 R-12 exists as an agent-drafted, E1-signed role from M2 and becomes two people by M5. Each new site is a deployment, a configuration, a rule bundle and a measured before/after, and `17` §8.1's acquisition cost of 50 hours is a large part of that |
| **Security and compliance as a function** | M4 | `17` §9.1's document set is a standing obligation, not an annual audit: DPA updates, sub-processor changes, the SBOM per release, the AI governance record per profile, and the export-control statement whenever a dependency changes |

### 5.3 The headcount the plan implies

| Milestone | Engineering | Support / SRE | Solutions / CS | Channel | Compliance | Finance | Sales | **Total** |
|---|---|---|---|---|---|---|---|---|
| **Today (M1)** | 2 | — | — | — | — | — | E1, folded | **2** |
| **M3 (2027-10)** | 3 (E1, E2, H1) | 1 (H1, folded) | 1 (H2 from 2028-01) | — | 0.25 (consultant) | — | E1, folded | **5** |
| **M4 (2028-04)** | 4 | 4 (rota) | 1 | 1 | 0.25 | 0.25 | 0.5 | **11** |
| **M5 (2029-01)** | 6 | 5 | 2 | 1 | 0.5 | 0.25 | 0.5 | **16** |
| **M6 (2029-10)** | 8 | 5 | 2 | 1 | 1 | 0.5 | 1 | **19** |
| **M7 (2030-07)** | 9 | 6 | 2 | 1 | 1 | 0.5 | 1 | **21** |
| **M8 (2031-07)** | 10 | 6 | 2 | 1 | 1 | 1 | 1 | **23** |

*(Engineering column is the new-build headcount available; M4's 4 includes the
founder pair, and 2 more than the 3.4 requirement because the founder pair is
also the gate signers and cannot be 100% on new build.)*

**The discontinuity is M3 → M4: 5 people to 11, and the driver is the support
rota and the channel, not the product.** A company cannot hire six people in one
quarter boundary. The three options, stated so one is chosen in advance:

| Option | What it means | Cost |
|---|---|---|
| **A — Cut M4's scope** | Deliver the second VMS adapter and the Adapter SDK v1 (rows 73, 75, 76, 77, 78 = 32 ew at top end) and defer ONVIF Profile M, access control and sensors to M4.5. 32 ew ÷ 22.8 = 1.4 engineers, so a team of 6 does M4 without a step change | The moat arrives later and narrower. `00` §3's segment 3 is delayed by a quarter. **Cheapest and most likely** |
| **B — Extend M4** | Keep the scope, run the window to 2029-Q2 instead of 2028-Q4 | 6 extra months of M4 payroll (~$700,000) and a 6-month delay to every segment-3 and segment-5 conversation. `04` §7.3's cycle is already 15–30 months, so 6 months here compounds badly |
| **C — Use the partner's engineers** | The Adapter SDK's entire purpose is that a third party can write an adapter. An OEM partner's engineering capacity is a **substitute** for ours, and it is the only option that does not cost a hire | It means the channel conversation has to start before the SDK exists, which `04` §7.4's fourth reason explicitly rejects, and it means depending on a partner whose roadmap we do not control. **It becomes available at M4's end, not its start** |

**Recommendation: A, with C as the mechanism for what A defers.** Cut M4 to the
second adapter and the SDK, get the certification suite run by a third party
(which is the M4 gate anyway), and then use the partner's engineers for the
Profile M, access-control and sensor adapters as paid channel work. This is the
only option in which the headcount step is avoided without cutting a moat, and it
is only available *because* of the SDK — which is the strongest financial
argument for `04` §7.4's sequencing and the strongest strategic argument for
building the SDK before the partnership exists.

### 5.4 Contractor or employee

| Work | Contractors | Employees | The test |
|---|---|---|---|
| Penetration test, annual | **Contractor.** It must be able to tell us things we do not want to hear, and the report is worthless if the tester is our colleague | — | Independence is the property being bought |
| Threat-model review, annual | **Contractor** | — | A second pair of eyes on the document that says our controls are adequate |
| Accessibility audit, SOC 2 readiness consulting, technical writing of the evidence pack | **Contractor** | — | Episodic, deliverable-shaped, and cheap to buy per project. **Note: we do not claim SOC 2 and we do not intend to; we publish the control-to-test map instead, which is what a security reviewer actually wants** |
| Load testing, ONVIF device interop testing against other vendors' hardware | **Contractor** for the interop (it needs a device lab we do not own); load testing from M3 | — | Access to hardware is the thing being bought |
| The certification-suite run (`05` row 78) | **Must be external** | — | `01` M4's gate requires "someone outside the company". A contractor is the cheapest possible way to satisfy it honestly |
| The 24/7 rota | — | **Employees** | Four people who are a phone, on a schedule, for years. A contractor agency in this loop is a data-access question and a response-time question, and both are fatal in this market |
| Solutions and deployment | — | **Employees** | `17` §8.1's acquisition cost is 50 hours of E1's time. It has to be a person with product knowledge, and a contractor is a person with product knowledge who leaves |
| Security and compliance | Fractional consultant at M4, 0.5 FTE at M5 | The 0.5 FTE becomes an employee at M5 | Episodic at first; standing by M5, and it is the role holding the invariants |
| Channel manager | — | **Employee** | A relationship is a person. `04` §7.3's 15–30 month cycle cannot be held by a contractor who is also doing something else |
| Finance, legal | Fractional CFO; outside counsel | — | At under $5M of revenue the full-time versions do not pay for themselves |

**The rule behind the table:** *anything a customer is given a name for is an
employee.* `12` §7.4: a customer who cannot name the person who answers their
phone cannot escalate to anyone. A contractor roster is not a name.

---

## 6. Pricing and gross-margin reality

### 6.1 The model that produces this margin is unusual, and the unusualness is the point

There is no cloud video, no per-camera price, and no hosted model. So:

| Cost | Who bears it | Our cost |
|---|---|---|
| **Inference** | The customer's GPU, in their building | **$0 per site**, plus a shared evaluation footprint (ESTIMATE $25/site/month) |
| **Evidence storage and growth** | The customer's disk | **$0** |
| **Ingest, verification, the audit ledger, the seal path, replay** | The customer's application host and Postgres | **$0** — Postgres is free and open, and `12` §9.3 says so plainly |
| **The console** | A workstation the operator already has | **$0** — we do not sell hardware (`00` §3) |
| **Our cost** | Support, sales, engineering, the release pipeline, insurance, legal | The whole of §2 |

`12` §9.3 states the commercial shape and calls it deliberate:

> A customer can run the entire product — ingest, verification, evidence,
> incidents, operator disposition, audit ledger, sealed checkpoints, replay — on
> hardware they own, with no GPU, and get every claim the product makes. The GPU
> buys corroboration quality. **If the GPU were required, the trust claim would
> be a claim about a purchase decision, and it would be worth less.**

**So the gross margin is not a margin story. It is close to 100% in cash terms
through M4, and that number is meaningless at this scale.** A company with $24,000
of revenue and $242,450 of costs does not have an 86% gross margin; it has a
small number. The metric that actually matters is **contribution per site after
support** (`17` §8.1) and the target is *(PROPOSED)* ≥ 8× — a site costing ≤ 12.5%
of its annual revenue in support. At $1,200/month that is ≤ $2,700/year = ≤ 18
hours/year = **≤ 1.5 hours per site per month.**

**And 1.5 h/site/month is the number in `17` §8.1, and it is the least measured
number in this document.** §3.4's sensitivity table shows what happens if it is
really 4 hours: contribution falls from $1,032.50 to $795.00 and break-even moves
from 20 to 26 sites. The support rota's own arithmetic (`12` §7.3, measurement 5
in `17` §8.4) says four people at $90k = $30,000/month could carry a lot of
sites — *if* support is 1.5 hours. At 4 hours per site, four people cover roughly
a quarter as many. **The unit economics of this company is a support-number
hypothesis wearing a pricing document, and the first real measurement of it is
the thing an M3 investor will diligence hardest.**

### 6.2 Why evidence storage, not inference, is the cost driver

`17` §8.2 states this in full. In financial terms, from this document's vantage:

| Currency | Inference | Evidence storage |
|---|---|---|
| **Our cash** | ~$25/site/month shared footprint, roughly flat in sites | **$0** |
| **Our support and test cost** | Low. A profile that runs is a profile that runs | **Scales directly with volume.** Every restore, migration rehearsal, `doctor` run, `kill -9` run at volume, and support session that opens an incident touches the evidence store. Every one of those is on a milestone exit gate |
| **The customer's cost** | A GPU they may decline entirely, with no loss of function | **The number they discover in month three.** `12` §9.1: ~73 GB at 30 incidents/day on 30-day retention; ~730 GB at 300. **The variable that moves it by an order of magnitude is the rule pack, not the camera count** |
| **Commercial** | None. A GPU is a line item they can decline | `slo.go` calls a full evidence disk "the one that ends an incident badly" *(real)*, and it is the number most likely to make a signed deal into a support escalation |

**The asymmetry is the point.** Inference is the vendor's cost in a cloud product
and ours is not one here. Evidence storage is the customer's cost, ours is
indirect, and it is the number that decides whether they can afford us at all. So
the honest statement about cost structure is not "our gross margin is high" — it
is **"our product's cost falls on the customer in the form of disk, and our
engineering job is to make that number predictable at signature instead of
discovered in month three."** That is what `17` §8.3's sizing session is for, and
it is why the formula goes in the quote:

```
  evidence/day = incidents/day × items_per_incident × mean_item_size × retention_days
```

with the first two measured per site (M2, `12` §9.4 item 4) and the last two
configured.

### 6.3 The retention and pricing policy that bounds it

`17` §8.3 in full; the financial consequence is three rules:

1. **Retention is bounded by default** (PROPOSED: 30 days hot, 90 archived,
   indefinite only under legal hold) and a customer's unbounded default retention
   in their own building is a defect in our defaults, not in their configuration.
2. **We never bill by evidence volume or by camera count.** Both punish the
   customer for having incidents, and both reward us for the wrong thing.
3. **The 10%-free SLO is an operational boundary, not a commercial one.** It is
   `slo.go` SLO 8, it is real, and the dry-run-first retention sweep respects
   `legal_hold` because a manual deletion that skips it destroys the only copy of
   evidence for an incident that may be in dispute. **Incident history is asset
   A1 and the retention policy is the product's behaviour, not a pricing lever.**

---

## 7. The three financial risks

### 7.1 The one that kills a pre-seed security company: a single reference customer's incident going wrong

**Why it is the pre-seed's specific killer.** Everything the pre-seed is raised on
sits on three design partners and, at M3, three production references. They are
not a sample; they are the entire evidence base, the entire pricing evidence, and
the entire reference queue. And a security product's failure mode is not a bug
report — it is a customer concluding that a decision the system made was wrong,
in a way that affected their understanding of a real security event, in a
building where they are the ones who answer for it.

**It does not have to be a bad incident.** It has to be an incident we got wrong
in a way the customer cannot explain away. The reference is lost, and the loss is
not proportionate: one lost reference out of three is not a 33% hit, it is the
collapse of the `01` M3 commercial state and the pre-seed's second act.

**Specific mitigation, in four parts:**

1. **Pre-drafted disclosure, not a reaction.** `12` §10.1's policy is written
   before it is needed: notify the named person with what we observed, what we
   believe, what we are doing, and when we will next update — **before they find
   out and before we understand it.** A pre-drafted template with the timeline and
   the named contacts in it is a five-hour job that removes two days from the
   worst day the company will have.
2. **Severity assigned from the customer's language, not ours.** `12` §7.1: a
   report that a real security event was missed is a **Sev-1 until disproved**.
   The asymmetry is explicit: the cost of treating a false alarm as a Sev-1 is one
   person's worst week; the cost of treating a real missed intrusion as a Sev-3 is
   a customer who never trusts the product again and tells everyone they know.
3. **The kill switch, exercised.** `15` §4.3: any AI capability is disabled by a
   single audited configuration change, with no deploy, no restart and no network,
   and the incidents in flight complete as `unverified` rather than failing or
   silently reversing. Rehearsed at T-60 and T-30, because a kill switch nobody
   has exercised is a comment.
4. **Insurance, bound before GA.** Cyber liability and technology E&O, with the
   indemnity structure agreed in principle, so the cost of an incident is a
   negotiation with an insurer rather than with a balance sheet.

**Decision date: 2027-12-01, the go/no-go on the GA scope.** Before that date:
if any of the three production references has had a product incident affecting its
understanding of a security event in the preceding 90 days, GA does not ship
with that customer, and the scope decision reverts to `G3-0`'s reduced-scope
option. **The check is a date and a question, and it is written here so it cannot
be forgotten in January.**

### 7.2 The one that kills a two-person company: the founders' runway expiring before the GA date

**What we know, and what we do not.** The consumption rate is an assumption and
the reserve is a number only the two founders hold. **This is the most important
input this document does not have, and it is the reason the risk below is a risk
rather than a calculation.**

| | Value | Status |
|---|---|---|
| Personal draw, both founders | **$4,400/month** ($2,200 each) | *(ASSUMPTION)* — a minimum sustainable living cost, drawn from personal savings |
| Personal reserve available | **Unknown** | *(NOT YET KNOWN)* — and deliberately not invented here. A fabricated reserve would make this section look answered and it is not |
| Date GA falls on the founders' own clock | **Month 16** of the draw (2026-10 → 2028-01-31) | arithmetic on `01`'s GA date |
| Date the plan needs no personal draw at all | **2027-01**, the month tranche 1 is modelled to close and salary begins | §2.1 |

**The calendar, stated as consumption rather than as a balance:**

| Date | Months of draw elapsed | Event | The question |
|---|---|---|---|
| 2026-10-01 | 0 | Model window opens, salary $0 | — |
| **2026-12-31** | 3 | **Tranche 1 targeted close** | *Is the reserve at least 12 months deep?* If not, this is the risk live on day one |
| **2027-06-30** | 9 | **The decision date** — the M1 exit review | *Has tranche 1 closed?* No → mitigation 2 or 3 below, this week, in writing |
| 2027-09-30 | 12 | M2 exit gate | *Is the reserve at least 9 months deep from here?* 18 months to GA |
| **2028-01-31** | **16** | **M3 GA — the date the plan is built around** | *Can the founders reach month 16?* |

**The precise formulation of the risk.** The corporate cash plan in §2 does not
depend on the founders' personal situation at all, because founder salary is
$0 in three of the first four months of the model and $7,500/month thereafter —
and that $7,500 is a company expense, funded by tranche 1. **The runway risk
lives entirely in the gap between "the plan starts" and "tranche 1 closes", and
that gap is three months by design and open-ended if the raise slips.** Every
month tranche 1 is late adds $4,400 of personal draw and $7,500 of unfunded
company payroll, and the second is the one that matters, because §7.2's whole
purpose is to prevent a plan that spends the founders' savings to protect a date.

**What the plan is actually exposed to, stated without flinching: at month 16,
GA arrives and the reserve is either comfortable or it is gone.** There is no
middle outcome, and this document cannot tell you which, because the number that
decides it is not in the repository. **The first action item arising from this
document is for one of the two founders to write down the number** — and to treat
it as a board-level number rather than a private one, because a plan whose
schedule depends on a private figure is not a plan.

**Mitigation, in order:**

1. **Tranche 1 closes before 2026-12-31.** Everything else follows from this and
   is downstream of it. Three months of exposure is a manageable exposure.
2. **If it has not closed by 2027-06-30: apply `01`'s M1 or M2 `Reduced scope`
   cut the same week, and defer H1 rather than the founder salary.** `05` §6.4:
   the cut is decided when the milestone starts, and the cost of moving a
   boundary should be higher than the cost of one uncomfortable week (`05` §7.2).
   Paying ourselves from the runway to hold a date is the exact behaviour the
   reduced-scope mechanism exists to prevent.
3. **A third option, and it should be written down in advance because nobody
   decides it calmly in month nine:** one founder takes a role elsewhere
   part-time while the other continues. This halves the personal draw to
   $2,200/month, and it halves the capacity law (`00` §6), which means a
   milestone's scope halves with it. **It is the correct response to a runway
   crisis and it is also a scope decision, so it is written here as a scope
   decision** — and it requires the `05` §6.4 named cut to be applied at the same
   time, not later.
4. **No fourth option is permitted.** Not a bridge, not a credit card, not a
   customer prepayment used as a payroll source, not an invoice to a design
   partner. A security company that borrows to make payroll discovers what its
   customers are, and the first person to hear about it will be the first person
   to ask whether a missed detection happened because the company was distracted.

**Decision date: 2027-06-30.** A one-line check at the M1 exit review: *has
tranche 1 closed?* Yes → continue. No → apply item 2 or 3 this week, in writing,
and tell any customer who has been given a date derived from the un-cut scope
(`05` §6.4's third rule).

### 7.3 The one that kills the plan: a long OEM cycle consuming all cash

**Why it is the one that kills the plan rather than the runway.** The runway risks
are bounded, visible and have a date attached. This one has the following shape:

| Phase | Cash consumed | Revenue produced |
|---|---|---|
| Introduction (2–4 months) | E1's calendar: high | $0 |
| Technical evaluation (3–6 months) | E1 + a solutions engineer: high, because we build things for the evaluation | $0 |
| Commercial and legal (4–8 months) | Counsel, E1's calendar: high | $0 |
| Pilot (6–12 months) | Deployment, support, a partner's certifications: highest | $0 |
| **Total** | **15–30 months of continuous senior attention** | **$0** |

`04` §7.3. Against a company whose entire capacity is two people and whose
capacity law already books 40% for everything-that-is-not-building. The channel
is *the* strategic answer to the E1 bottleneck (`17` §3.3) and it is also a 2-year
commitment of the scarcest resource to a counterparty whose roadmap we do not
control.

**The failure mode, stated precisely:** the company spends 2028's entire senior
capacity on a channel that has not shipped, tranche 2's money goes on the
resulting payroll rather than on the product, the M6 compounding-claim review in
Q2 2030 is reached with no compounding claim, and `00` §5 says what happens then:

> If that is not true at the M6 review, the data moat did not materialise and
> M7–M8 are an automation project on top of a search product.

**Mitigation, three parts:**

1. **A hard date, and a budget line that does not exist without it.**
   **Decision date: 2028-06-30** — `04` §7.3's date. If no OEM **pilot agreement**
   is signed by then, the M4 commercial track gets no budget line, no headcount
   line, and no tranche-3 money, and the second VMS adapter is chosen purely on the
   direct pipeline (`01` M4: "Chosen by customer pipeline, not by preference").
   Conversations are not pilots. `01` §M4 is explicit that an OEM conversation is
   not a sale and is not forecast as one.
2. **Sequential, not parallel, at small scale** (`00` §6). The channel cannot be
   run *in addition to* the direct motion with two people. It runs *instead of* part
   of it, and the part it replaces is named in advance so the replacement is a
   decision.
3. **A 25% margin floor, and the option to concede everything except the two hard
   clauses.** `17` §4.3: exclusivity and the no-fork/attribution pair are not
   tradeable, and `04` §7.4's rule is that the first conversation happens *after*
   the SDK exists precisely so the conversation does not have to be spent on
   patience. **A partner who requires exclusivity or a fork is a partner who would
   have foreclosed segments 1, 2 and 4, and declining them costs us a delay rather
   than a company.**

### 7.4 The three risks in one line each

| Risk | It is fatal because | Mitigation | Decision date |
|---|---|---|---|
| A reference customer's incident goes wrong | Three references are the entire pre-seed evidence base; the loss is not proportionate to the count | `12` §10.1's pre-drafted disclosure; Sev-1-until-disproved; the rehearsed kill switch; insurance bound before GA | **2027-12-01** — the GA scope go/no-go |
| The founders' runway expires | The reserve ends 2028-07, six months after the GA date, on a date no milestone contains | Tranche 1 closes by 2026-12-31; at 2027-06-30 apply the reduced-scope cut or the one-founder-part-time option, in writing, this week | **2027-06-30** — the M1 exit review |
| A long OEM cycle consumes the cash | 15–30 months of senior attention for $0, against a capacity law that already books 40% for everything else | No pilot agreement by 2028-06-30 → no budget line, no headcount, adapter chosen on the direct pipeline; the channel runs instead of part of the direct motion, not in addition to it | **2028-06-30** — the M4 mid-point |

---

## 8. What this document does not claim

- A market size, a total addressable market, a growth rate, an incident volume, or
  a false-positive rate for any industry. Unsourced, therefore not stated
  (`00` §8, `08` §3.2).
- A churn number. Below three cohorts it does not exist, and §17 §8.4 item 8
  records it as a number that will be *reported* when it first appears rather than
  modelled in advance.
- A gross margin that means anything. At $24,000 of revenue against $242,450 of
  cost, a margin percentage is a rounding of noise.
- That the support-hours figure is right. It is the largest lever in §3.4 and it
  is an estimate with a named measurement that does not exist yet.
- Any founder's personal financial situation beyond the $2,200/month assumption
  in §7.2, which is the founders' number to correct, not this document's.
