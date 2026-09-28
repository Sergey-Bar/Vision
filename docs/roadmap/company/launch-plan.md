# Launch Plan

Owner: E1 (commercial), E2 (technical). Status: living. Last revised 2026-09-28.

Read `` §M1/§M2/§M3, `` §4, and
`` §3 and §8 first. This document is the calendar for
telling anybody anything, which is the smallest and most dangerous part of
building a security product.

**Marker convention:** *(real)* = a file, command or gate in this repository.
*(PROPOSED)* = proposed here, never measured, not a customer commitment.
*(ESTIMATE)* = calculated from stated assumptions. *(NOT YET KNOWN)* = does not
exist yet.

---

## 0. The three dates, and one problem with them

| Event | Target | Milestone | Size |
|---|---|---|---|
| **Design-partner announce** | **2027-03-31** | M1 exit boundary | Small. A named, closed list. Not a launch |
| **Paid-pilot announce** | **2027-09-30** | M2 exit boundary | Medium. The first external customer is named |
| **First Production GA** | **2028-01-31** | M3 | The real one |

### 0.1 The inconsistency, stated rather than resolved silently

`01` §M3 runs **2027-10-01 → 2028-03-31** and its exit gate is Operator
Intervention Rate below an agreed threshold **while** false-negative and
missed-incident rates do not regress, action idempotency under replay proven by
fault injection, and an operator able to explain any incident in the window from
the product alone. A GA on **2028-01-31** is two months *before* the M3 window
closes.

Two readings, and this document picks one:

| Reading | What ships at GA | Consequence |
|---|---|---|
| **A — GA is scoped to M3's pre-declared `Reduced scope`** | Playbooks, recommendations, notification integrations, and the safe action set. **No bounded auto-resolution, no PTZ.** `01` §M3's reduced scope says auto-resolution is "the milestone's most valuable and most dangerous feature; it is the correct thing to cut first" | GA on 2028-01-31 is honest, and the M3 exit gate is signed 2028-03-31 with auto-resolution still outstanding. The commercial promise at GA is the workflow surface, not the autonomy claim |
| **B — GA waits for the full M3 gate** | Everything in `01` §M3 | GA is **2028-03-31** and the 2028-01-31 target is dropped |

**Default: B.** `01` §M3's exit gate is signed by both founders with artefacts,
and a GA that precedes its own exit gate means the commercial date and the
engineering date are two different numbers, which is how a company ends up
supporting a promise its own roadmap does not contain. `01`'s reduced-scope
clause exists for a slip (`05` §6.4), and applying it in advance to hit a
marketing date inverts its purpose.

**Decision gate `G3-0`, 2027-09-30, at the M2 exit review, both founders, in
writing.** The input is the M2 burndown at the `05` §7.4 checkpoints and whether
M3's four exit lines are tracking. If at `G3-0` auto-resolution is projected to
pass its gate by 2028-01-31, GA is 2028-01-31 (reading A, with the reduced scope
declared in the release notes and the pricing sheet). If it is not, GA is
2028-03-31 (reading B) and 2028-01-31 is withdrawn **in writing to anyone who
has been told it**, per `05` §3.7's "a commitment we cannot keep is withdrawn in
writing before the date, not explained after it."

**2028-01-31 is used as the working date throughout this document because it is the
stated target, and every calendar item below is recalculated from a new T-0 if
`G3-0` selects reading B.** The offsets are the plan; the dates are derived.

---

## 1. What a launch is, and the four things we launch

### 1.1 The design-partner announce — 2027-03-31

**For.** To tell three named design partners, and the people who referred them,
what has been built and what has been measured, and to ask them for the
reference in writing. It is the moment `01` §M1's commercial state ("up to three,
at no charge, in exchange for a named reference, a measured before/after
investigation time, and access to anonymised incident data") becomes real.

**It is not a launch.** `01` §M1: "No public launch, no pricing, no self-serve."

**It must never:**
- Be published, indexed, or sent to a list we do not own. The test in `04` §4.4
  applies: *if this were read aloud by a design partner about to decide whether to
  be a reference, would they object?*
- Contain a capability that has not passed its named gates (`04` §1.3). At M1 that
  includes the whole AI plane, because M1's AI profile is in shadow by
  construction and gate 4 is the thing being announced.
- Contain a number that is not in `08`'s definitions, with its N. There is one
  real number in it: the measured investigation time, by class, with N, against
  their own baseline, and the four companions (`08` §4.5).
- Be a milestone release note *plus a press framing*. The release note is a
  separate artefact (§1.4) and it does not get marketing language.
- Name a customer who has not read the text and agreed to it word for word.

**Artefact.** `docs/roadmap/releases/M1-2027-03-31.md` (the release note) and
`docs/decisions/2027-03-31-design-partner-announce.md` (the agreed announcement
text, the recipient list, and who approved it).

### 1.2 The paid-pilot announce — 2027-09-30

**For.** To state that a paying external customer exists who did not know us, and
to make the first commercial claim: a named customer, a named price level
internal-only (`17` §1.4 — no published price until three referenceable GA
customers, `04` §4.2), and the M2 measurement result with all four companions.

**It must never:**
- Publish a price (`04` §4.2's rule; `17` §1.4). The pilot rate is a founding rate
  for named accounts and quoting it publicly converts a negotiation into a
  published list price we cannot retract.
- Report an MTTI improvement **without** `missed_incident_rate`,
  `evidence_completeness`, the degraded-decision share and the operator's own
  assessment. `08` §4.5 and §4.1's second rule: the direction of the hypothesis
  is not announced to the operators running the trial, and a result published
  without companions is the same failure as the experiment being faked.
- State a target MTTI that was not declared before the trial opened
  (`08` §4.4 step 2).
- Be described as a launch in any material. It is a milestone report.

**Gate.** `01` §M2's exit gate, signed by E1 and E2 with artefacts: a
statistically meaningful MTTI reduction on supported classes, per operator,
against their own VMS-only baseline, with N computed from the M1 variance; every
search result and summary carrying resolvable evidence references; and a replayed
incident reproducing its summary from stored facts with no model call, asserted
by a test.

**Artefact.** `docs/roadmap/releases/M2-2027-09-30.md` plus the signed M2
measurement report (including the N computation, the effect threshold declared in
advance, and the per-class distribution for both arms).

### 1.3 First Production GA — 2028-01-31 (subject to `G3-0`)

**For.** Three things, and only these three, per `01` §M3's commercial state: "a
repeatable sales motion with three named production references is [the goal].
Self-serve is not the goal; this is the first milestone at which a customer other
than a design partner can be declined for capacity rather than for trust."

1. A production release that a customer can install from a signed archive and run
   unattended.
2. A price that exists and can be quoted, and a contractual support level.
3. Three named production references, willing, in writing.

**It must never:**
- Be a big bang. §2.1 states the availability posture: the GA release goes to
  reference sites in a declared order over declared days. There is no date on
  which every site gets it.
- Announce a capability that has not passed its gates. At GA this is the whole
  AI plane: every profile in the build has a dated evaluation report, and a
  profile in shadow is labelled shadow in the release notes, the pricing sheet and
  the console (`04` §1.3).
- Claim an OIR, a missed-incident rate, or an accuracy figure that was not
  measured at a customer site with a stated N. `08` §3.2 lists the numbers this
  roadmap refuses to state; GA is the day the pressure to state one is highest.
- Carry a 24/7 support promise until the rota exists. `12` §7.3: "we would rather
  decline a deal than sign one." **This is the single most likely GA blocker and
  it is a hiring decision, not a launch decision** (`18` §5).
- Publish a self-serve signup, a free tier, or a trial. `01` §M3 is explicit that
  self-serve is not the goal, and `08` §8.1's list of things not tracked includes
  everything a free tier optimises.
- Say "AI autonomous", "AI agent", or "hands-free" anywhere. The words imply a
  capability M3 does not have. `01` §M3's mission is "AI performs the routine
  operator workflow", with a policy gate, an action audit, and `01` §M7 two years
  away for autonomy levels.

### 1.4 The per-milestone release notes

**For.** `07` §9.1 lines 19 and 20: known limitations and unverified claims stated
in the release notes, and `docs/status.md` updated in the same change. The release
note is the authoritative record of what a given build does, and it is the
document `05` §7.4's pre-customer-conversation discipline is built on.

**They are the opposite of marketing.** Their audience is a customer whose site
is running the build.

Every release note contains, in this order:

1. Version, build date, the commit, and the sign-off state of the release quality
   gate (`07` §9.1's twenty checks with their signers).
2. What changed, and **which of those changes a customer could depend on** —
   `07` §9.1 line 10's "an undocumented change a customer could depend on" is the
   failure.
3. **What is still not built**, quoted from `docs/status.md`, with the scope of
   each limitation's effect. The standing list until they stop being true
   (`07` §9.3): the Milestone endpoint surface is unverified; the Python VLM
   runtime is absent from a build that has it enabled; `deploy/` is absent;
   `install`/`upgrade`/`rollback`/`backup`/`restore` are stubs that refuse with a
   named reason. **A release that ships implying a gate is met is the failure
   `07` §9.3's last bullet exists for.**
4. Which AI profiles are in the build, each with model version and hash, prompt
   version, gate results individually, and **shadow or production**. A shadow
   profile presented as production is a release-gate failure (line 12).
5. Migrations shipped, with their phase, and the expand/contract separation
   stated. A contract step bundled with an expand step is a failure (line 3).
6. The contract-surface diff: the API diff, the rule-language diff, the migration
   diff (line 10).
7. Every commitment given to a customer that this release affects, with the
   `05` §3.7 record it comes from.
8. Known open defects of Sev-3 and above with owners and dates, and the count of
   Sev-4s.

**They must never** be written for an audience of investors, contain a roadmap
date, or be used as a source for a marketing claim. A release note is a
statement about a build.

### 1.5 The rule that covers all four

> `05` §3.7's template governs anything a customer relies on, and a release note,
> a launch post and a deck slide are all "anything a customer relies on" when
> they are shown to one. A number without a measurement carries the word
> ESTIMATE to the customer, not just to us.

---

## 2. GA checklist

### 2.1 Availability posture, decided first, before any date is fixed

**No big bang. No stage. There is one GA release and it goes to sites in a
declared order on declared days.**

| Cohort | Sites | Day | Who installs | Note |
|---|---|---|---|---|
| **0 — Design partners** | up to 3 | **T-0** | We, on site, from the signed archive | They are first *because* they are the only sites we can safely fix in person, not because they get anything new |
| **1 — Production references** | 2–3 | **T+2** | We, on site | Their "willing to be a reference" is one of the three commercial gate items, and they are being given the release *before* the announcement, not after |
| **2 — Pilot conversions and paid sites** | remainder | **T+7** | We, on site or with a written runbook | The upgrade path is rehearsed on a disposable site image first (`07` §9.1 line 14) |
| **3 — New prospects** | — | **T+14 at the earliest** | Self-serve from the signed archive, with the technical integration guide | Not a stage; a date. No prospect is offered a GA install before T+14 |

The reason for the on-site installs is not service levels, it is arithmetic: the
install path is `securityctl install` from a verified archive onto a service unit
with no shell access (`03` §1.2 R-08's single metric), and that path is new at
M3. **Every GA install in the first two weeks is supervised, and the third
cohort only exists because the first two produced supervised installs with no
Sev-1.**

### 2.2 The checklist

Each item has one owner, one gate, and one artefact. An item without all three
is a wish. Owners are E1, E2, or **both** — there is no fourth person, and items
marked "both" are the ones that cannot be signed by the person who did the work
(`07` §9.1's opening line).

#### T-90 — 2027-11-02 — the go/no-go frame, 8 weeks before the date is real

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T90-1 | Reference queue depth check: how many production sites are *willing* to be a named reference, in writing | E1 | **≥ 3 written agreements to be a named production reference.** Fewer than 3 → the GA date is not confirmed and `G3-0`'s decision is revisited immediately | The three signed reference consents, filed by name |
| T90-2 | `01` §M3 exit-gate burndown: the four exit lines, remaining engineer-weeks against remaining capacity, at the `05` §7.4 checkpoint | Both | Remaining capacity < remaining work → `01`'s M3 `Reduced scope` is applied **this week** and GA is rescheduled under `05` §7.2 with all five evidence items | The burndown table and the scope decision, dated |
| T90-3 | Support model decided and hired or explicitly not | Both | **A decision, recorded.** Either the 24/7 rota of ≥4 exists or GA does not sell 24/7 (`12` §7.3). "We'll do it on best efforts" is not a decision | `docs/decisions/YYYY-MM-DD-ga-support-model.md` |
| T90-4 | Technical gate dry run, full 20 checks (`07` §9.1), on a release candidate | E2 | All 20 pass or the failing ones are named with owners and dates | The signed gate sheet, with every line's evidence attached |
| T90-5 | The pricing sheet drafted, in `docs/collateral/`, reviewed by both | E1 | Reviewed and sourced; **not published** (`04` §6.2 item 5) | The draft and the review note |
| T90-6 | Collateral completeness review: all ten documents in `04` §6.2 either complete with a date, or on a dated plan with a reason | E1 | No document is missing without a dated reason | The monthly collateral defect list, closed for this round |
| T90-7 | Insurance bound: cyber liability and technology E&O, with certificates issued | E1 | Certificates in hand, limits stated, named-insured list correct | The certificates, filed |
| T90-8 | The GA scope frozen: exactly which capabilities are in the GA build | Both | A written list. Every AI profile named with shadow/production status | The scope list, signed, referenced by the release note |

#### T-60 — 2027-12-02 — the trust gate and the dry run

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T60-1 | Threat model current: every control still mapped to a test that exists, every accepted risk re-stated, "what is not modelled" re-read | E1 (E2 supplies) | Reviewed in the last 60 days. The eight open DoD-blocking defects in `07` §11.4 are each either fixed or re-declared as a known limitation **in the release notes** | The review record, dated; the revised `docs/threat-model.md` |
| T60-2 | `security.txt` live, at a real address, with a monitored mailbox and a named human | E2 | A test email from outside the company receives a response within one business day | The live file; the test exchange |
| T60-3 | Full DR drill against the GA candidate, including the manual run with the runbook (`07` §9.1 line 15) | E2 | The "restored ledger is provably behind the seal" finding is still the finding, and the seal-reconciliation procedure is written | The drill record with timings; the procedure in `docs/` |
| T60-4 | Migration rehearsal against a restored copy of production-shaped data, timings recorded (`07` §9.1 line 13) | E2 | Migrations complete within the maintenance window with a stated margin | The rehearsal record |
| T60-5 | Upgrade and rollback rehearsed, **including a deliberate failure** (`07` §9.1 line 14) | E2 | A half-upgrade is demonstrated to be unreachable | The rehearsal record and the deliberate-failure output |
| T60-6 | Rollback plan for a *capability* (not just a release) written and rehearsed: §5.3's kill switch | E2 | A capability disabled and re-enabled on a test site, with the audit rows visible | The rehearsal record; the two audit row ids |
| T60-7 | Contracts: MSA, DPA, sub-processor list, the AI clauses from `17` §9, and the data-handling one-pager | E1 | Counsel-reviewed. Every clause a security director will ask for is answered in the document rather than in a call | The signed template set |
| T60-8 | First case study drafted, with a placeholder for the number that does not exist yet | E1 | **Blocked, not started** if the numbers are missing — `04` §4.3: a case study with a missing number is not written. Draftable structure only at T-60 | The draft skeleton with every number field marked NOT YET KNOWN |
| T60-9 | Security questionnaire response pack assembled; unanswered questions answered with **when and why**, never blank | E1 | Zero blanks. `04` §6.2 item 9 | The pack, with a completeness note |

#### T-30 — 2027-12-31 / 2028-01-03 — the commercial and technical gate

*(2028-01-01 is a Saturday and 2028-01-02 a Sunday; the checkpoint runs on
2028-01-03. This is the same unlabelled holiday gap that `01` has between
2028-12-22 and 2029-01-04, and it is a reminder that a plan without holiday
labels is a plan that will be "met" in a week nobody worked.)*

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T30-1 | **Technical gate**: the full 20-check release quality gate on the exact GA build, every line signed by a person who did not do the work | Both | All 20 green. `07` §9.2: **a skipped test is a failure**, and a test never seen to fail is not a test | The signed gate sheet with per-line evidence |
| T30-2 | SLOs demonstrated under a recorded load run, with the run and its numbers in the release notes (`01` §M1 gate 3's standard, applied to GA) | E2 | Every SLO in `internal/observability/slo.go` met. **Q-1 and Q-2 are resolved first** (`07` §11.4): `correlation_under_drift` currently breaches at `> 0` severe device rather than at the 1% it states, and `dead_letter_visibility` measures the existence of a dead letter rather than its visibility. At GA these are customer-visible page conditions | The load-run record; the two SLO fixes and their tests |
| T30-3 | **Operational gate**: support hours and SLAs staffed, runbooks written, escalation path named, `security.txt` live | Both | §2.4 below, all four sub-gates green | The gate sheet |
| T30-4 | **Commercial gate**: pricing signed off, three references willing, collateral complete, contracts reviewed, insurance in place | E1 | §2.5 below, all five green. **Any one red moves the date** | The gate sheet |
| T30-5 | **Trust gate**: threat model current, no unverified claim in any of the ten collateral documents, the three buyer questions answerable in the demo, verified by running the 25-minute run sheet end to end with a non-founder watching | Both | `04` §5.3's run sheet completes in 25 minutes with all three questions answered and the foreign key refused | The run-sheet record and the observer's note |
| T30-6 | Evidence completeness measured at each GA cohort 0 and 1 site, on real incidents, before GA | Both | 1.0 for `high` and `critical` incidents (`08` §3.1, PROPOSED). Below that, the release notes say so with the number | The measurement, per site, with N |
| T30-7 | The release note for GA drafted, in full, including the "what is not built" section | E2 | `07` §9.1 lines 19 and 20 satisfied. Reviewed by E1 for every customer-facing claim | The draft |
| T30-8 | Rollback rehearsed on a disposable site image against the GA build, and the *capability* kill switch rehearsed (`T60-6` again, on the final build) | E2 | Both pass | The rehearsal record |
| T30-9 | Hypercare rota written, and the founder time budget for T-1 to T+7 agreed with `04` §8.4's rules | Both | A written week-by-week allocation. **E2 is not in a customer call during the release week** | The allocation, in the shared calendar |
| T30-10 | The GA announcement drafted and word-counted against `01`'s "no unverified claim" rule | E1 | Every sentence traceable to a measured artefact or a repository file. Reviewed by E2 for technical claims and by E1 for commercial claims; neither signs the other's | The draft and the two review notes |

#### T-14 — 2028-01-17 — freeze

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T14-1 | Cohort 0 (design partners) GA install rehearsed on a site image, not a site | E2 | Installs, runs, and survives one restart with no loss (`securityctl audit verify` after a `kill -9` at each pipeline stage) | The rehearsal record |
| T14-2 | Cohort 1 references confirm their T+2 install window in writing | E1 | Three confirmations with a named human and a date | The confirmations |
| T14-3 | Code freeze for anything in the GA build. Only a Sev-1 fix enters, and each one re-runs the full 20-check gate | Both | The freeze is announced in the shared calendar and honoured | The freeze note; any exception logged with its re-gate |
| T14-4 | **Release week declared** (`04` §8.4: T-7 to T-2). E2's customer-facing slot is reassigned for that week | E1 | Confirmed in the calendar | The calendar entry |
| T14-5 | Status page live and monitored; a way for a customer to report a problem reaches a human | E2 | A test report from a customer contact routes to a named engineer and is acknowledged inside the SLI | The test ticket |
| T14-6 | Backup demo recording captured on the exact GA build, on local media | E2 | It plays, with audio, end to end | The dated recording |
| T14-7 | Announcement final read-through against the three buyer questions, aloud, by the person who will send it | E1 | It answers all three, contains no number without a method, and names no customer without permission | The final text and the read-through note |

#### T-7 — 2028-01-24 — the last week

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T7-1 | All four readiness gates re-signed against the frozen build, with the build hash recorded | Both | Green, and the build hash in the announcement matches the build hash on each customer's site | Four signed gate sheets |
| T7-2 | Cohort 0 install plan per site, hour by hour, with a named engineer and a rollback trigger | E2 | Written per site | The per-site plan |
| T7-3 | Support readiness: the named engineer and named backup per customer published to the customer, by name (`12` §7.4) | E1 | Every GA customer has received a named person and a named backup before the first production install | The sent message |
| T7-4 | Runbooks for the eight operations-playbook scenarios present in the release archive (`12` §8) | E2 | Present and current in the build | The archive listing |
| T7-5 | The "what if the launch goes wrong" decision tree (§5.5) read by both, with the decision owners named | Both | Agreed, and the triggers have numbers, not adjectives | The signed tree |
| T7-6 | Go/no-go recorded, in writing, with the evidence for each gate attached | Both | **The GA date moves if any gate is red. There is no "soft go".** | `docs/decisions/2028-01-24-ga-go-no-go.md` |

#### T-0 — 2028-01-31

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T0-1 | GA build, manifest hash, and signature published with the release note | E2 | `securityctl verify-release` accepts on a machine with no network and no database | The verified manifest |
| T0-2 | Cohort 0 installed at all three design-partner sites, on site, from the signed archive, one site at a time, with a verification step between each | E2, with E1 on the call | Each site: `securityctl doctor` clean, `audit verify` clean, no accepted event loss, evidence completeness at 1.0 for high and critical | The per-site install record |
| T0-3 | Cohort 0 held for **4 hours** after install before cohort 1 moves. Not a monitoring window — a deliberate pause in which nothing is changed | E2 | No Sev-1, no Sev-2, no guardrail breach (`08` §6.1) | The four-hour record |
| T0-4 | The announcement goes out, after cohort 0 is confirmed | E1 | Sent to the list in `docs/decisions/`, and the pricing page and status page are live at the same moment, not before | The sent announcement |
| T0-5 | The day-1 hypercare schedule is running (§5.4) | Both | A named human is awake and watching for the whole window | The schedule |

#### T+7 — 2028-02-07

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T7-1 | Cohort 1 (production references) installed and confirmed | E2, E1 | Three sites running GA, each with a named support contact and a signed reference consent | The install records |
| T7-2 | Cohort 2 (paid sites) install begins, on the runbook, without us on site | E2 | The first three unsupervised installs succeed. A failure here re-opens supervised-only | The install records |
| T7-3 | Activation scorecard read (§5.6) against its PROPOSED targets | E1 | The scorecard, with each number and its N | The scorecard |
| T7-4 | Every Sev-1 and Sev-2 in the window reviewed with `12` §10.3's protocol | Both | Each one has a notification, a timeline, and either a fix or a written statement that it was not a product defect | The incident log |
| T7-5 | Collateral correction: anything a customer learned in week one that made a document wrong is corrected this week | E1 | The defect list is acted on or explicitly declined with a reason | The updated documents |
| T7-6 | First announcement-response review: what did we get asked, and does the FAQ answer it | E1 | Every question not already in `04` §6.2 item 6 is added | The FAQ diff |

#### T+30 — 2028-03-02

| # | Item | Owner | Gate | Artefact |
|---|---|---|---|---|
| T30-1 | Cohort 3 opens: new prospects may be offered a GA install | E1 | No Sev-1 in the first 30 days attributable to the GA build | The record |
| T30-2 | The 30-day scorecard reviewed in full, including the four guardrail metrics and the vanity exclusions | Both | Signed, with the numbers, and a written statement of what we do not know yet | The signed scorecard |
| T30-3 | The case study written — or explicitly declared not yet, with the missing measurement named (`04` §4.3) | E1 | Either a complete case study with every required metric, or a written reason | The case study or the declaration |
| T30-4 | The 30/60/90 review scheduled for T+60 and T+90, with the agenda from §7.1 | E1 | In the calendar, both people, with the input list pre-written | The calendar entries |
| T30-5 | Hypercare stands down, or extends, in writing, with the reason and the end date | Both | A date, not a milestone | The decision note |
| T30-6 | Pipeline reviewed against `04` §10's stage definitions; the first closed-won deals entered | E1 | Every record has `LAST TOUCHED` and a `LOSS REASON` where applicable | The pipeline files |

### 2.3 The gate summary

A launch is four gates. Each is signed by both founders, and each names its
artefact. This is the whole launch-readiness system; there is no fifth gate and
there is no committee.

| Gate | Signed | Fails if |
|---|---|---|
| **Technical** | E2 proposes, **both** sign | Any of `07` §9.1's 20 checks fails; a skipped test; a Sev-1 or Sev-2 open; an SLO not demonstrated under a recorded load run; a guardrail breach (`08` §6.1: a guardrail breach halts a release regardless of how good the feature looks); an AI profile in the build without a dated passing report |
| **Operational** | Both | Support not staffed for the SLAs being offered; a runbook scenario missing from the archive; no named human per customer; `security.txt` not answering; no status page |
| **Commercial** | E1 proposes, **both** sign | Fewer than three references willing in writing; the price not signed off; a collateral document incomplete; contracts unreviewed; insurance not bound |
| **Trust** | Both | The threat model not reviewed in 60 days; any unverified claim in any asset; the demo does not answer all three buyer questions in 25 minutes; a known limitation is not in the release notes |

### 2.4 The operational gate in detail

| Sub-gate | Condition | Source |
|---|---|---|
| **Support staffed for what is being sold** | The SLA offered is the SLA staffed. Business hours at M2 and earlier; 24/7 only with the ≥4-person rota priced into the licence | `12` §7.3 |
| **Runbooks** | All eight operations-playbook scenarios present in the release archive: product down; console not; model unavailable; evidence storage full; clock drift; camera moved; rule noisy; under attack | `12` §8.1–8.7 |
| **Escalation path** | Tier 0 (operator, `docs/OPERATIONS.md`, `securityctl doctor`), Tier 1 (one named engineer, inside the SLI), Tier 2 (the other engineer, automatically, on a Sev-1 or on "I need help"). "Beyond there is nobody", and the customer is told that in writing before they sign | `12` §7.4 |
| **`security.txt`** | Live, at a monitored address, with a named human, response tested from outside | §T60-2 |
| **Status page** | Live before the announcement, and never used to communicate a Sev-1 to a named customer — the direct notification comes first | `12` §10.1 |
| **The named-human rule** | Every GA customer is given one named person and one named backup, by name, **before the first production install**. "The support team" transfers responsibility to nobody | `12` §7.4 |

### 2.5 The commercial gate in detail

| Sub-gate | Condition | Failure response |
|---|---|---|
| **Three references willing** | Three written, name-specific consents to be a named production reference, reviewed *with* the customer in the exit-gate session before it goes to a third party (`03` §6.3) | **The date moves.** Fewer than three and this is not a GA — `01` §M3's commercial state names three. Not two, not "three in progress" |
| **Pricing signed off** | `17`'s price points adopted, `04` §6.2 item 5 complete, and the condition met: three referenceable GA customers have each seen a price and said yes at approximately that level, or the price is quoted per-opportunity and not published | Publish the price with fewer than three and the number becomes a list price we are held to |
| **Collateral complete** | All ten documents in `04` §6.2 complete, or on a dated plan with a reason, and the monthly review closed | An incomplete pack is a customer who is told no in month four |
| **Contracts reviewed** | MSA, DPA, sub-processor list, insurance certificates, export-control/sanctions statement, and the four AI clauses (`17` §9) | Unreviewed templates are the most common cause of a security review that fails in month three |
| **Insurance in place** | Cyber liability and technology E&O bound, limits stated, named-insured correct, and — the detail integrators ask for first — **the OEM/partner indemnity structure agreed in principle** if any partner conversation is live | A partner asking about our limits is a good sign; no answer is a disqualifier |

---

## 3. Launch assets checklist

Each asset: an owner, a completeness test, and the milestone by which it must
exist. A launch asset is not "done" because it exists.

| # | Asset | Owner | Complete when | By |
|---|---|---|---|---|
| 1 | **Release notes** for the GA build | E2 | All eight sections of §1.4 present, `docs/status.md` updated in the same change, both signed | T-14 |
| 2 | **Demo environment + fallback recording** | E2 | Environment built from the GA build, backup recording captured on local media, both dated; the run sheet rehearsed in full | T-14 |
| 3 | **Pricing page** | E1 | The price, what is included and not, the term, the licence, renewal, support level, and the note that the GPU is optional. **Not published before the three-reference condition** | T-0 |
| 4 | **Status page** | E2 | Live, monitored, with a named human on the alert route, and tested | T-14 |
| 5 | **KB / documentation index** | E1 | Every `docs/` document reachable, with the runbooks first, and a "what is not built yet" page linked from the index | T-14 |
| 6 | **The case study** | E1 | Complete with every required metric in `04` §4.3, or explicitly declared not ready with the missing measurement named | T+30 |
| 7 | **Migration guide** | E2 | Expand/contract phases stated, the schema window explained, a rehearsal reference, and what a customer does if a migration refuses | T-14 |
| 8 | **Upgrade and rollback guide** | E2 | Both directions, the deliberate-failure case, the schema-window check, and the point at which a rollback is the right answer rather than a fix | T-14 |
| 9 | **Data-handling one-pager** | E2 | What is stored, where, for how long, what never leaves, what we never do with it, the export path | T-60 |
| 10 | **Security questionnaire response pack** | E1 | **Zero blanks**; every unanswered question has a "when" and a "why" | T-60 |
| 11 | **On-site demo kit** | E1 | A laptop with the environment and the recording, a local network path to the demo site, a printed run sheet, a second copy of the run sheet on paper, a spare foreign public key for the seal demonstration, and a power and network fallback | T-7 |
| 12 | **Security and architecture whitepaper** | E2 | Referencing `docs/threat-model.md` as its source, not restating or improving on it | T-60 (`04` §6.2 item 2) |
| 13 | **Evidence and compliance pack** | E1 | Complete, including the published open-defect list (`07` §11.4) and the AI governance record with every profile's known limitations | T-60 |
| 14 | **Technical integration guide** | E2 | Install → service unit → configure → rule bundle lifecycle → API → evidence and retention → upgrade/rollback/backup/restore, and the support matrix | T-14 |
| 15 | **Buyer FAQ** | E1 | All three questions with long answers, the four standing objections, "what we do not do", "what is not built yet", support hours verbatim, how to leave | T-14 |
| 16 | **Competitive brief** | E1 | Categorical only, five categories plus the do-nothing baseline, **no vendor names** (`04` §9) | T-14 |

**The rule for all sixteen:** no asset makes an unverified claim. Every number
carries a measurement with its method and N, a file reference, or a marker with
the measurement that would replace it (`04` §6.1).

---

## 4. Rollout

### 4.1 Availability posture

Restated from §2.1 because it is the single most consequential decision in this
document: **one GA release, installed cohort by cohort, in person for the first
two cohorts, unsupervised only after the first three supervised installs have
been clean.** The reasoning is that at M3 the install path is new
(`securityctl install`, the service units, the transactional upgrade), and a
security product's first production release is not the place to discover an
install problem at three sites simultaneously.

**No stage, no blue/green, no canary percentage.** Those are for a team with
someone to watch a dashboard. The equivalent here is a person standing next to
the first install.

### 4.2 The rollback plan

Three distinct things get rolled back, and conflating them is how a launch goes
wrong:

| Rollback | What it is | Mechanism | Authority | Time |
|---|---|---|---|---|
| **A single site** | The GA build is bad on that site | `securityctl rollback` to the previous verified release, from the previous signed archive already on the site's media | E2, immediately, no meeting. **A rollback is not a decision, it is a procedure** | ≤ 30 min |
| **The whole cohort** | The build is bad in a way one site did not show | Rollback on cohort 0 within the T-0 4-hour hold; after that, cohort-by-cohort with each rollback treated as a site rollback plus an investigation | E2, immediately; E1 tells the affected customers | ≤ 2 h per site |
| **The whole launch** | The build is systematically wrong | GA is withdrawn. Every site rolled back. The announcement is corrected in public within one business day, and the release note carries the correction | **Both**, in writing, per `05` §7.2's five evidence items | Decision within 1 h; announcement correction ≤ 1 business day |

**Preconditions to a rollback, stated in advance so they are not argued about
later:**

- Accepted event loss > 0 on any site (invariant 1). Immediate, no
  investigation first.
- `audit verify` failing on any site (invariant 4). Immediate, and seal a
  checkpoint first if the chain is at risk (`03` §7.1: the first action is always
  *seal and stop the bleeding*, never a fix attempt on a live site).
- A guardrail breach on a shipped metric: `evidence_completeness` < 1.0 for high
  or critical, `missed_incident_rate` regressing, or `operator_override_rate`
  moving against us (`08` §6).
- Two Sev-1s attributable to the GA build within seven days.
- An action taken by the platform that it was not authorised to take. Sev-1
  always (`12` §7.1).

**What is never a rollback trigger:** a customer being unhappy, a metric moving
in a direction we did not predict, or an investor's opinion. Only the five above.

### 4.3 The kill switch — a capability-level disable, not a deploy

> **Any AI capability can be disabled by a single, audited configuration change
> that takes effect without a release, without an internet connection, and
> without a decision by anyone other than a named human — and the change itself
> appears in the audit ledger.**

This is a direct consequence of `00` §4 question 2 and `01` §M3's
"no silent activation" principle, and it is the most operationally important
mechanism in this document.

| Property | Requirement |
|---|---|
| **Granularity** | Per capability *and* per site. A single site disables a capability; a global disable exists; nothing coarser than the capability |
| **Mechanism** | A configuration change applied through the normal activation path: stored, simulated, diffed, reviewed, activated — the same four separate acts as a rule bundle (`05` §3.6; `internal/bundles`: `TestStoreDoesNotActivate`, `TestActivationRequiresAReviewedDiffDigest`). **The kill switch is not an exception to the change-control path. It is that path, used in a hurry** |
| **Audit** | The actor, the capability, the site, the time, the blast-radius preview, and the reason, all in the hash-chained ledger before the change takes effect. `05` §1.7: any behaviour change whose only record is in operational logs rather than the ledger is a violation |
| **No deploy** | No release, no restart of the platform, no network. The capability is off at the next evaluation and the audit rows are written regardless |
| **What happens to the work in flight** | Incidents whose decisions depended on the capability complete as `unverified` with the degradation recorded. **They do not fail, and they do not silently resolve the other way** — a missing AI observation is never a negative observation (invariant 3) |
| **What the operator sees** | A visible, labelled state on every affected incident: *"AI capability disabled by <name> at <time>"*, never a silent absence. `01` §R3: a degraded incident must never read as a quiet one |
| **Re-enable** | The same four acts in reverse, with a named approver. Never an implicit re-enable at the next release |
| **Tested** | Toggled off and on on a test site with the audit rows visible, at T-60 and again at T-30 (`T60-6`, `T30-8`). **A kill switch nobody has exercised is a comment** |

**Which capabilities have a kill switch at GA:** every AI observation profile;
the investigation planner; semantic search; incident summaries; auto-resolution
classes individually; safe actions individually; notification integrations
individually. **Not:** ingest, verification, evidence storage, dispositions, the
audit ledger, the seal path. Those are the deterministic core, they are the
product's claim, and disabling them is a site's decision to stop using us, not a
switch we provide.

### 4.4 Day-1 hypercare

| Window | Coverage | Who | What is watched | What triggers the escalation |
|---|---|---|---|---|
| **T-0 to T+1 day** | Business hours plus a declared on-call phone for Sev-1, per `12` §7.3's honest paragraph | E2 technical, E1 customer | Install verification, `accepted_event_loss`, `audit verify`, `backlog_depth`, `evidence_disk_free_percent`, `dead_letter_count`, `clock_severe_device_count`, `ai_unavailable`, seal verification result | Any Sev-1; any guardrail breach; any Sev-2 not triaged within the working day |
| **T+1 to T+3** | Business hours, both reachable, E1 owns the customer line | Both | The same, plus the activation scorecard's leading indicators, plus the FAQ's unanswered questions | Two Sev-2s from the same cause |
| **T+4 to T+7** | Business hours, on-call by phone for Sev-1 | E2 primary, E1 secondary | The scorecard, the defect list, and the collateral corrections | The scorecard missing a target with no explanation |
| **T+8 to T+30** | Normal support (`12` §7.3) | Both | The monthly cadence (`03` §6.3) plus the scorecard | As normal |

**The hypercare rule that matters:** during T-1 to T+7, **no engineer is in a
customer call during the release week** (`04` §8.4), and no roadmap work is
started. The hypercare week is capacity, planned in `04` §8.3's milestone
variation, not absorbed. `03` §7.1's rule that the on-call human's week is half a
normal week applies to the GA week with no exceptions.

### 4.5 If the launch goes wrong

Pre-agreed, in order, so nobody is deciding a response under pressure:

| Situation | First 60 minutes | By 1 business day | By 5 business days |
|---|---|---|---|
| **A reference customer has an incident we got wrong** | `12` §10's protocol: notify the **named person** with what we observed, what we believe, what we are doing, and when we will next update. **Before they find out and before we understand it.** Severity first, then diagnose. If the wrongness could plausibly have been a missed real security event, it is a Sev-1 and the clock is `07` §10.3's | The internal incident review; the cohort decision — is this site-specific or systemic; a written determination | Root cause within `12` §7.2's SLI (Sev-1: 5 working days) and the `12` §10.3 correction protocol, including whether other customers are affected and whether they are told |
| **The build is bad** | Roll back per §4.2. A rollback is a procedure, not a decision | The announcement is corrected in public. The correction states what was wrong, what it affected, and what we did — `12` §10.3's format | Root cause, a regression test that fails when the control is removed, and the `07` §9.2 demonstration that it fails |
| **We are the story — a researcher, an incident, a disclosure** | §5.7's security-intake protocol in `12` §10.4. The product is not a security incident with a customer until it is confirmed; the intake is a channel with a named human and a response time | The assessment, with a named owner | Coordinated disclosure per `12` §10.4. `03` §7.3: a disclosure arriving on a Friday afternoon does not get a rushed release that skips a control test |
| **The demo failed in front of a prospect** | Nothing, in front of the customer. The recording goes up and we say the environment broke | The defect is logged with `05` §3.6's template and fixed or scheduled. A demo that fails twice for the same reason is a DoD-blocking defect | Rehearsal schedule corrected |
| **We missed the reference queue** | GA does not happen. `01` §M3's commercial state names three, and a GA with two is a marketing event | The date moves under `05` §7.2 with all five evidence items | — |
| **We are out of capacity** | The customer is told within the day, in writing, by a named human | Deliverables are re-sequenced against the milestone, in writing, and the customer is told which of their dates moved | The `05` §6.4 scope conversation, held this week rather than at the milestone boundary |

---

## 5. Metrics for launch health

### 5.1 The first-30-day activation scorecard

Four activation events, then the guardrails. All targets are **PROPOSED** and none
of them is a customer commitment; `08` §3.2 is the list of numbers this roadmap
refuses to state, and these are on it until measured.

| # | Metric | Definition | Target *(PROPOSED)* | Denominator | Why this one |
|---|---|---|---|---|---|
| **A1** | **First incident closed in the product** | The site's first incident reaches `dispositioned` or `closed` through the product, with a recorded disposition, within the first 7 days of the GA build being live | **≥ 3 of ≥ 3** cohort-0 sites, and **≥ 80%** of cohort 1 and 2 sites within 14 days | Sites with the GA build live ≥ 7 days | It is the only metric that says a human did the job in our system. Everything else is a claim about the software |
| **A2** | **First evidence bundle viewed** | An operator opens an incident's evidence — snapshot or clip — and the bytes resolve and match their hash | **≥ 1 per operator, and ≥ 3 per site** in week 1; **100%** of high and critical incidents | Operators who dispositioned in the period | An incident without viewed evidence is an incident nobody could defend. `08` §2.2's `evidence_completeness` is the guardrail version of this; A2 is whether a human actually looked |
| **A3** | **First disposition** | The site's first disposition recorded through the product, with the step-up MFA path where the outcome requires it | **≤ 1 day** from the first incident raised | — | The shortest path to the claim. If an operator cannot dispose an incident in a day, the surface is wrong and the whole GA thesis is wrong |
| **A4** | **First audit checkpoint verified** | A signed seal written at the site and then verified — offline, with a trusted key — by someone who is not the person who wrote it | **≥ 1 per site in week 1**, and **≥ 1 per month thereafter** by the customer, unprompted | Sites live | Q3 answered by the customer's own hands. An audit capability the customer never invokes is a capability we have not delivered |
| **G1** | `accepted_event_loss` | Accepted events not in a terminal state | **0.** Not a percentage | Live count | Invariant 1. `08` §2.1: "0.0000% loss" should make an operator suspicious |
| **G2** | `audit_chain_valid` | `securityctl audit verify` | **true**, everywhere, always | — | Invariant 4 |
| **G3** | `evidence_completeness` for `high` and `critical` | Closed incidents with retrievable, hash-matching evidence | **1.0** | Closed high/critical incidents | `slo.go`'s own statement: an incident without evidence is not defensible |
| **G4** | `missed_incident_rate` | From the replay audit, per site, with N and the Wilson interval | **No regression** versus the pre-GA baseline at the same site | Labelled cases from the replay audit | A faster product that misses more is not an improvement (`08` §4.5) |
| **G5** | `operator_override_rate` | `agreement = 'disagreed'` ÷ rows with a related observation; `no_ai_observation` rows excluded | **No regression**; a rising override rate is a drift signal, not a quality signal | Dispositions with `related_observation_id` set | North Star companion; also the earliest warning that a profile is decaying at a site |

### 5.2 What we do not put on the launch scorecard

Stated as a prohibition, not a preference, because `08` §8.1's argument is that a
number nobody forbade gets optimised.

| Excluded | Why it would optimise the wrong thing |
|---|---|
| Logins, DAU, sessions, console time | Measures the UI. A launch scorecard that rewards logins produces a UI that gets opened more and a security desk that is not improved |
| Alerts reduced, alarms suppressed | **Suppression improves this number by hiding things.** Reportable only per class, only for dispositioned classes, and only beside `missed_incident_rate` and `evidence_completeness` |
| Incidents prevented | Undefined, unmeasurable, no counterfactual. If ever computed it is a per-site claim with a stated denominator and method, never a launch claim |
| "Accuracy" as one number | It is at minimum four numbers over a denominator that excludes three buckets, on a profile grouped by profile, with an N that a Wilson interval will show is insufficient. `08` §8.2 |
| Time to first install | Optimises the installer, and the installer's job is to be slow enough to be verified |
| Number of announcements, press mentions, signups | GA is not a campaign. `01` §M3: self-serve is not the goal |
| MRR, ARR, pipeline | `08` §8.1: a finance report, not a product metric, and not on a dashboard an engineer reads. It is in `04` §10, on a different cadence, with different rules |
| Count of integrations or adapters shipped | `08` §8.1 calls it a trap. The metric that matters is the non-adapter diff, and it is a milestone gate, not a launch metric |
| A single "launch went well" judgement | If it cannot be computed and it cannot be a measurement, it is a mood. The review agenda in §7.1 exists so the mood is decomposed into things that can be checked |

### 5.3 The reporting rule

The scorecard is issued weekly for the first 30 days, then monthly, and it is
issued **with** the guardrail rows. A scorecard showing A1–A4 improving and G1–G5
breached is a defective report, and the response is to withdraw it, not to explain
it. `08` §1.2: a reduction in attention minutes reported without the companions
is a false claim, and we refuse to publish it.

---

## 6. The unglamorous risk list

Four things that will actually happen, each with a response agreed before it
happens. This list is the reason §4.5 exists.

### 6.1 A reference customer has an incident we got wrong, during the launch window

**Why it is likely.** The launch window is when the volume of real decisions is
highest, the operators are least practised, and three sites are running a new
build. A wrong decision affecting a customer's understanding of a security event
is the definition of a product incident in `07` §1.2, and the product incident
count at GA will not be zero.

**The pre-agreed response:**

1. `12` §10.1's disclosure policy, unmodified: **before they find out, and before
   we understand it.** A notification to a named person with what we observed,
   what we believe, what we are doing, and when we will next update. Not a
   changelog entry. Not "this was fixed in 1.4.1".
2. Severity is assigned from the customer's language, and a claim that a real
   security event was missed is a **Sev-1 until disproved** (`12` §7.1: the
   asymmetry is not close).
3. **The cohort decision, made within 60 minutes, in writing:** is this
   site-specific, or is the build wrong? If there is any doubt, it is the build.
   A rollback is a procedure, not a decision (§4.2).
4. The internal review is never performed by the person who built the thing that
   was wrong. At two people this means E1 leads it when the defect is E2's, and
   the review is written down.
5. `12` §10.3's correction protocol, which covers telling *other* customers we
   know are affected. A defect in a class that runs at three sites is a defect
   at the three sites whether or not the other two have noticed yet.
6. **The reference is not withdrawn, and the customer is not asked to keep
   quiet.** Their decision, in writing, and the case study waits for it.

**The thing we will be tempted to do and will not:** minimise it. "The system
corrected itself", "the operator caught it", "it was a degraded incident" — every
one of these is true in our own system's terms and none of them is an answer to
a customer whose understanding of a security event was wrong.

### 6.2 A competitor announces the same thing

**Why it is likely.** The wedge is legible: local-first, evidence-backed, AI
that can be explained and can be switched off. A VMS vendor with an installed base
has every incentive to describe that, and they will do it louder and sooner.

**The pre-agreed response, decided now so nobody decides it in a hurry:**

1. **Nothing, publicly, on the day.** No response post, no comparison, no
   benchmark, no tweet quoting theirs. The competitive brief is categorical and
   un-named (`04` §9) precisely so that there is nothing to update.
2. **Within 5 business days**, a written internal assessment: what they claim, what
   we can verify, what we cannot, and what it changes. Attached to the launch
   retro, not published.
3. **What actually changes our position:** nothing structural, and the honest
   analysis is that an incumbent announcing this is *validation of the wedge*, not
   a threat to it — unless they can do the three things, and the first two are
   architectural decisions they would have to have already made. Our response is
   to keep demonstrating the first two in a four-minute check, because the buyer
   who runs the check is the buyer who knows the difference.
4. **What we do not do:** claim they cannot do it. We have not verified their
   product, `00` §8 forbids the assertion, and it is the claim that would follow
   us if it is ever disproven by a customer reading their release notes.
5. **One exception, and it is the only one:** if they claim a capability our
   gated product does not have, we say so about **ourselves** — in our release
   notes and to anyone who asks — rather than about them. `docs/status.md`'s "What
   is not built, and why" is the document, and it is public.

### 6.3 A security researcher finds something

**Why it is likely.** A product whose entire claim is that it is auditable, that
its bundles are untrusted input, and that its evidence paths cannot escape their
root, is a target. We publish a threat model with 35 controls. We also publish
eight known defects (`07` §11.4), which means we have already told them where to
look.

**The pre-agreed response:**

1. `12` §10.4's coordinated-disclosure protocol and security intake, live, with
   `security.txt` answering, a named human, and a published response time. A
   researcher who cannot reach a human concludes the vendor does not care, and that
   conclusion becomes the finding.
2. **A14, `evidence of degradation`, is an asset in the threat model** — the
   ability to measure whether the AI plane was actually running. An attacker who
   suppresses AI observations silently attacks the product's central claim. This
   is the class of finding we should expect and should instrument for first
   (`08` §2.1's `ai_unavailable`).
3. Triage against the threat model's own boundary and abuse-case numbering, so the
   finding is placed in a structure that already exists rather than in a new one.
4. **Disclosure is on our schedule, after a fix, and it is proactive.** `12`
   §10.1: before they find out. A vendor that waits for a researcher to publish
   has a second problem.
5. **No rushed release.** `03` §7.3 names the Friday-afternoon temptation and
   the answer: `verify-release` → `contract-check` → `make ci` → the control
   tests. A rushed release that skips a control test is the exact supply-chain
   event B6 exists to prevent, delivered by us to ourselves.
6. **If it is in an invariant:** ADR, then a test, in that order, and the test
   demonstrated to fail when the control is removed (`AGENTS.md`).
7. Credit the researcher by name, in the advisory, with their permission.

### 6.4 The demo fails in front of a prospect

**Why it is likely.** It is 25 minutes of live software on a laptop, on someone
 else's site, on a network we do not control, during a period when both founders
 are the most loaded people in the company.

**The pre-agreed response:**

1. **Never debug in front of the buyer.** The fallback recording, played openly
   with the date and the build it was taken on. A buyer watching a competent
   failure is a better outcome than a buyer watching a flail, and we say so in one
   sentence rather than apologising for a minute.
2. **The model being down is not a failure.** It is the centrepiece (`04` §5.5),
   and the unplanned version is the same demo. Say: *"that is unplanned, and I am
   going to do exactly what the demo does when the model is on, which is continue."*
3. The environment is E2's job, prepared the day before and re-checked an hour
   before, every time, with a checklist of ten items (`04` §5.4). **The demo is
   rehearsed weekly on the same script**, so the failure is discovered on a Tuesday.
4. The demo runs on a build **that is not a release-week build** (`04` §8.4).
5. The defect is logged with `05` §3.6's template and reviewed at the next
   burndown. **A demo that fails twice for the same reason is a DoD-blocking
   defect** and goes on `07` §11.4's list.
6. If the demo has to be rescheduled, it is rescheduled promptly and we do not
   let a prospect's calendar drive a rushed environment. The alternative is a
   demo that works once and is never trusted again.

### 6.5 The two we did not expect, added because they are the real risks

| Risk | Response |
|---|---|
| **We are too busy shipping to sell** | The launch dates are decided by the commercial gate, not the engineering gate, and the three-reference condition is a hard blocker (`T90-1`). `01` §M3: this is the first milestone at which a customer can be declined for capacity rather than for trust — which means capacity has to be visible. If E1's customer hours are being consumed by the build for two consecutive weeks (`03` §7.4's early-warning list), the date moves rather than the reference count |
| **A capability ships enabled that should have shipped shadowed** | It is a release-gate failure (`07` §9.1 line 12) and a trust-gate failure (§2.3). The check is mechanical: the release note's profile list, cross-referenced against the evaluation report's promotion decision, with `Promotable` and "who was promoted" as separate fields. `05` §3.4: the report says who is *eligible*; it never says who was promoted. That separation is the check |

---

## 7. Post-launch

### 7.1 The 30 / 60 / 90 review agenda

The same agenda each time, 90 minutes, both founders, written up. A review whose
agenda is chosen on the day is a status meeting.

| # | Item | Output |
|---|---|---|
| 1 | **The scorecard, in full** — A1–A4 and G1–G5, with N, and the vanity exclusions restated so nobody adds one | The signed scorecard, or a signed statement of what we do not know |
| 2 | **Every Sev-1 and Sev-2** in the window, with `12` §10.3's protocol followed or not, per incident | The incident list and the determination per incident |
| 3 | **Every commitment made in the window** (`05` §3.7 records). Did we keep it? If not, was it withdrawn in writing before the date, and is the internal action recorded? | A kept/withdrawn/missed list. A "missed" without a withdrawal is a process failure, not a delivery one |
| 4 | **Guardrails** (`08` §6.1) with the evidence attached, not a summary. `08` §7.4: a per-release guardrail check with the evidence, not optional and not a summary | The check |
| 5 | **The three buyer questions, from customer evidence** — not our opinion. What did customers actually ask, what did they stop asking, and did any of the three stop being asked? A question that stops being asked is either answered or irrelevant, and we cannot tell which | A paragraph per question |
| 6 | **Collateral defects**: everything a customer learned that made a document wrong | The corrections, done that week |
| 7 | **Pipeline against `04` §10's stage definitions**, with `LOSS REASON` codes counted. The two most valuable codes are `security review failed` and `evidence not convincing`, because both mean we were wrong rather than the market | The pipeline review, and the count of each loss reason |
| 8 | **The capacity question**: are we above or below `00` §6's line, and for how long? Is E1's customer time still inside `04` §8.2's budget, and is E2 still out of customer calls in release weeks? | The number, honestly, and the scope conversation if it is over |
| 9 | **Scope: what goes into the next milestone** (§7.4) | A written list, before the next milestone starts, not during it |
| 10 | **What we would tell the next company doing this.** One paragraph. It is the most reused artefact in the repo and the only one that gets read by anyone who is not us | The paragraph |

### 7.2 What triggers a rollback of a shipped capability

Not a release rollback. A capability that is live, correct at launch, and wrong
later.

| Trigger | Source | Action |
|---|---|---|
| A guardrail breach attributable to the capability: `evidence_completeness` < 1.0 for high or critical, `missed_incident_rate` regressing, `operator_override_rate` climbing, `ai_false_negative_rate` above the profile's gate | `08` §6 | The **kill switch** for that capability, per §4.3, per site, in an audited config change. Not a release |
| An action taken that was not authorised to take it | Invariant 1, 4, 5; `01` §M3 item 3 | Kill switch immediately, then the incident protocol. **An unauthorised action is always Sev-1** (`12` §7.1) |
| A profile's evaluation gates fail on a re-run, or its known limitations turn out to be wrong | `05` §3.4; `01`'s sequence dependencies | The profile returns to shadow. It is a Tier C signal and it can be withdrawn without a release |
| Drift: a rule becomes noisy, a camera moves, the site's activity changes | `01` M6 item 5 — and long before M6, in the operations playbook | A drift exclusion, recorded on the verification result. **Visible, not silent** (`01` §R3; `05` §1.7) |
| Two Sev-1s from the same cause within 30 days | `12` §7.1 | Kill switch, then the capability does not come back until the regression test exists and has been seen to fail when the control is removed |

**The rule that governs all five:** the kill switch is a configuration change, so
using it is not a deployment, does not need a release, and does not need anyone
else's agreement. **What needs agreement is turning it back on.** And a
capability that has been killed twice for the same reason does not come back
without an ADR.

### 7.3 How a capability is deprecated and removed

A capability can be killed. It must not simply stop existing without a record,
because the product's entire claim is that nothing changes behaviour without a
record (`05` §1.7).

| Stage | Duration | What happens | What the customer sees |
|---|---|---|---|
| **Announce** | ≥ 90 days for a GA capability, ≥ 30 for a pilot-era one | In the release notes and directly to every customer using it, with: what is being deprecated, the replacement or the migration, and the date | A notice in the release notes, naming the capability exactly |
| **Warn** | 30 days | Per-site usage report: which sites use it, how much, and what breaks when it goes | A report they can act on |
| **Disable by default** | — | A configuration change through the activation path. Existing sites keep it; new sites do not get it | A visible state: "deprecated, disabled by default, still active at this site because you have not migrated" |
| **Remove** | On the announced date | The capability and its audit rows' *behaviour* are removed. **Audit rows are never removed** — the ledger is append-only (invariant 4) and a deprecation is a new row, not a deletion | The capability is gone; the history of its use, and of its activation and deprecation, is fully visible |
| **Migrate** | If a replacement exists | The migration guide, with a rehearsal, and the data either migrated or explicitly exported | A guide, not a surprise |

**Hard rules.** A deprecated capability is never silently removed. The audit
record of its deactivation is as permanent as its activation. And a capability
that produced incidents in the customer's understanding of a security event is
never removed before the incidents it produced have been reviewed with the
customer (`12` §10).

### 7.4 How the launch retro feeds the next milestone's scope

The mechanism, so the retro is not a document nobody acts on — the same argument
`05` §5.3 makes about incident review:

1. **At T+90, the retro produces one list: candidate scope changes for the next
   milestone.** Each candidate states the evidence that put it there (an incident,
   a guardrail breach, a repeated `LOSS REASON`, a scorecard miss, a collateral
   defect), and the milestone it belongs to.
2. **Each candidate goes through `03` §5's two-person filter**, like any other
   idea: does it serve a named segment in `00` §3, and is it the general form of a
   real problem or one customer's configuration?
3. **The top item each cycle is either built or explicitly declined with a
   reason.** A review that produces a document nobody acts on is worse than none,
   because it consumes the time it claims to save.
4. **Nothing is added mid-milestone.** `05` §6.4: the cut is decided when the
   milestone starts. A launch retro that injects scope into a milestone already in
   flight is how the capacity law in `00` §6 breaks, and the early-warning sign
   is already in `03` §7.4: more than three items on the "at risk" list.
5. **What is explicitly *not* fed back:** anything that would have been caught by
   a test we did not write. That is a `07` DoD failure, it goes into the milestone
   that owns the test, and it is not a scope item.

### 7.5 The one thing the launch cannot fix

**It cannot make three happy customers into a market.** What GA proves is that a
two-person company can ship a security product, install it on a real site,
support it, and produce evidence a customer can hand to an auditor. What it does
not prove is demand at scale, and the only honest measure of that between now and
M5 is the pipeline in `04` §10 and the count of closed-won deals with a named
champion and a documented decision process.

If GA lands and the pipeline does not grow, the correct reading is not "we need
more marketing". It is that one of the five segments in `00` §3 is not the
segment that buys, and the next four months are spent finding the one that is —
with `05` §5.1's interview method, which produces a problem statement, a
measurement, or a refusal, and never "we would use this".
