# Compliance and Certifications

Owner: E1 (accountable), with external counsel as required. Status: living.
Last revised 2026-09-28.

This is the second half of the security programme in ``.
Where that document says *what is controlled*, this one says *what is provable to
a third party* — and, just as importantly, what a third party's certificate
does not mean.

**A rule about the numbers in this document.** Every cost and every date is an
**ESTIMATE**. Per `README.md`'s evidence rule, an unverified number is labelled
with its source and a date or it is not used. We have no quotes. Before any of
this is planned against, three written quotes are obtained per line, and the
quotes are filed in `docs/compliance/quotes/`. A cost estimate written by the
people who would like the project to happen is a sales document.

---

## 1. The compliance thesis

### 1.1 What a certificate buys, stated exactly

This is the section to read before quoting a certification to a customer,
because the gap between the two halves of the sentence is where a two-person
company accidentally makes a promise it cannot keep.

> **A SOC 2 report says: an independent auditor examined a system description
> and a set of controls, tested them over a stated period, and found them
> operating effectively as described.** It does not say the product is secure,
> the code is correct, the AI is accurate, or that no customer will ever be
> breached. It says a set of *assertions* were tested and a *description* was
> accurate at a time.

| A certificate **is** | A certificate **is not** |
|---|---|
| Evidence for a procurement questionnaire, which is where 80% of the value is | A regulator approving the product |
| A forcing function that makes security controls real and dated | A substitute for the controls themselves |
| An external opinion on a *subset* of controls (security, and in a Type I or II report also availability and/or confidentiality) | An opinion on the product's correctness, its AI accuracy, or its roadmap |
| Reusable across customers who ask the same question | A reason for a regulated customer to skip their own assessment |
| A marketing asset that is *legible to a buyer's procurement team* | A technical artefact an operator reads at 03:00 |

**The one sentence to have ready for the customer who asks "so you're
certified?":** *"We are independently tested against a published control set
over a stated period, and here is the report and the exceptions. We are not
claiming a regulator has approved us, because no regulator has and we would not
represent otherwise."*

### 1.2 The gap between "we comply with a control set" and "a regulator approved us"

This gap is the reason the compliance track exists as a separate document and
not as a section of the security programme. Three distinct things, in increasing
order of cost and decreasing order of our control:

```
  1  WE COMPLY WITH A CONTROL SET
     We implement and document controls. We can show them.
     Cost: engineering time.  Testable: yes.  In our control: yes.
     Evidence: the control matrix in 13 §9.1 and docs/threat-model.md §5,
               each row naming the test that fails without the control.

  2  AN INDEPENDENT PARTY CONFIRMS IT
     An auditor or assessor tests the controls and issues a report.
     Cost: fees + months of evidence collection.  Testable: no.
     In our control: the *facts*, not the *opinion*.  We cannot influence
     what the auditor concludes, and we must not try — a company that shops
     for a clean opinion is a company whose report says nothing.
     Evidence: the report, the system description, the exception list, the
               bridge letter, the complementary user-entity controls.

  3  A REGULATOR APPROVES US
     A supervisory authority reaches a conclusion about our product in a
     regulated context — a notified body, a market surveillance authority, a
     data protection authority, a sector regulator.
     Cost: months to years, and mostly legal rather than engineering.
     Testable: no.  **In our control: almost nothing.**
     Evidence: an approval, a registration, a conformity assessment.
```

**The rule that follows, and it is the most important line in this document:**

> **We will build (1) completely and (2) honestly, and we will never describe
> (2) as (3).** A sentence in a proposal that converts an audit into an
> approval is a misrepresentation, and in a security product a
> misrepresentation is the one thing that ends the company.

**Where (3) is actually reachable for us.** Not by applying for anything. Three
specific routes, each of which is a *customer's* choice rather than ours:

- **EU AI Act conformity.** If we fall in scope as a high-risk system, we do not
  "apply for approval" — we self-assess against the applicable requirements
  (or use a notified body for the highest-risk class), produce technical
  documentation, register the system in the EU database, and the obligation
  falls on the *deployer* (the customer) with us as provider. See §2's row and
  §3.3's questions. **REQUIRES LEGAL ADVICE.**
- **NIS2 / sectoral rules.** These bind the *operator* of critical
  infrastructure, not the software vendor. Our obligation is contractual and
  evidential: we must be able to show how our product helps the customer meet
  their logging, incident-reporting and supply-chain-security duties. See §3.4.
- **A customer's own internal supplier qualification.** Very often the real ask
  is a customer's security questionnaire, a site-level risk assessment, and a
  contract clause — no certificate at all. **This is the cheapest compliance
  work available and it is the most likely to pay.**

### 1.3 The order buyers ask for, in the order we will do them

`00` §3 puts regulated verticals last among our segments (M5–M7, entered on
reference from segments 2–4). The order of the *certificates* is different from
the order of the *buyers*, because one of them gates several:

| Rank | Item | Why this rank |
|---|---|---|
| 1 | **A customer's own security questionnaire, answered completely and honestly** | It is the thing actually blocking a deal, it costs engineering hours rather than fees, and it is the raw material for every later certificate. Doing this first also tells us which controls the buyers actually care about |
| 2 | **SOC 2 Type II** | The most-requested single artefact in enterprise procurement, in every vertical we sell into. It is also the *evidence engine* for everything after it: the same controls, the same evidence, the same period |
| 3 | **A penetration test** | Cheaper than a certificate, more persuasive to a technical buyer than a certificate, and it is the artefact that tells us something we do not already know. `07` §11.1 commits to one before M3 GA |
| 4 | **ISO 27001** | Asks the same question as SOC 2 in a different vocabulary, plus an ISMS. Buyers who ask for it are usually non-US or non-financial. **It is largely a repackaging of SOC 2 evidence, which is why it is fourth and not first** |
| 5 | **ISO 42001 (AI management)** | The one that most directly serves the trust story — it is a *certified management system for the AI we ship*, which is exactly the claim `00` §2 point 3 says almost no incumbent can make. It is rarely done, so it is a differentiator, and it is fifth because it depends on 27001 and on a mature AI-governance record we do not have yet (§4 of `13`) |
| 6 | **EU AI Act readiness** | Not a certificate at all — a conformity posture plus documentation plus registration. Sequenced after ISO 42001 because ISO 42001's management-system evidence is most of the work, and gated on legal advice (see §2, §3.3) |

**The sequencing constraint that decides all of it:** SOC 2 Type II requires
**a minimum of about three months of operating evidence** (most firms will not
do less, many prefer six) and an **audit firm engaged in advance** — engagement,
scoping, a system description, and a fieldwork date are all lead-time items. The
honest arithmetic is in §2's table and it is the single most urgent line in this
document.

### 1.4 The honest statement about effort

At two people, with the capacity law of `00` §6 (2 engineers, 13 weeks a
quarter, ~60% on new build), compliance is not a parallel project. It is a
**reallocation of the 40% tax**, and the reallocation is:

| Activity | First year | Steady state |
|---|---|---|
| Security questionnaire responses (per customer) | 2–4 days each, more at first | 0.5 day each |
| Evidence collection for SOC 2 (continuous, not a phase) | ~0.5 day/week | ~0.5 day/week, forever |
| Audit fieldwork support | 3–5 days | 3–5 days per year |
| Policy documents, kept current | 6–8 days/yr | 2 days/yr |
| Vulnerability response | see `13` §8.3 | as needed |
| AI governance record upkeep (`13` §4) | 4 days | 1 day/month |
| **External pen test** | 0 before M3 | 3–5 days + fixes per year |

**The conclusion is uncomfortable and it is the correct one: a SOC 2 Type II
costs roughly 0.5 engineer-days a week for a year plus fees, before any
engineering work to close gaps.** At `00` §6's capacity that is about 5% of the
company's total capacity, every year, for ever. That is a real, quantified,
permanent price, and the decision to pay it is a business decision with a date —
which is why the SOC 2 question cannot wait for M3.

---

## 2. Certification timeline

**Every cost is an ESTIMATE with no quote behind it, obtained from a general
sense of the market rather than from three vendors.** Every target date is
computed backwards from the milestone it is meant to serve, and every one is
subject to a gate.

| # | Item | Target date | Why this date | Prerequisite evidence | Cost ESTIMATE | Owner | Who does the work |
|---|---|---|---|---|---|---|---|
| 1 | **GDPR / UK GDPR: documented posture** | **2026-12-31** | Every design partner asks, and the answer is a document, not a project. It is also the input to the DPA (§8) which is a **prerequisite for signing any customer** | A data inventory from the DPIA-lite (`13` §5.5); the retention and redaction mechanisms that already exist (`AGENTS.md`; the sanitiser; the evidence store) | 0 (internal) | E1 | E1 writes, E2 supplies the schema facts |
| 2 | **Customer security questionnaire pack** | **2026-11-30** (first pack) | It is the cheapest compliance work available and it is the raw material for everything later | The control matrix (`13` §9.1); `docs/threat-model.md`; the release notes; the known-limitations list | 0 (internal) | E1 | E1, from the documents we already maintain |
| 3 | **A scoped external architecture + identity review** | **2027-03-31** | Cheaper than a pen test, earlier than M3, and aimed at the three areas a penetration test would examine first: the evidence path, the release verifier, the identity model | `docs/threat-model.md` as the scope document; a test environment; a design partner willing to host | EST. low five figures, or a day-rate engagement | E1 | An independent consultant, 3–5 days |
| 4 | **SOC 2 readiness gap assessment** | **2026-11-30** | **Critical path.** An audit firm will not scope an engagement without one, and an engagement without one produces a report full of exceptions. The deliverable is a gap list and a remediation plan, and it is the first thing that has to be bought | The §2 item-2 questionnaire pack; the control matrix; a candid statement of the gaps in `13` §9.3 and `07` §11.4 | EST. five figures | E1 | An audit firm or a SOC 2 consultancy |
| 5 | **SOC 2 Type II: controls operating** | **2026-12-01 → 2027-03-31** | A **minimum-viable four-month observation window.** This is the earliest credible Type II and it is only credible if item 4 lands on time and the control set is already mostly real. The control set is mostly real: `docs/threat-model.md` §5 is 35 controls each mapped to a test, and `07` §3 maps every invariant to a test that fails when the control is removed. **We are not starting from zero; we are starting from an undocumented control set** | Items 1, 2, 4; a written information-security policy set; access control evidence; change management evidence (git history + PR reviews); incident response evidence (the `07` §10.3 protocol, written, and at least one real drill) | EST. five figures in fees, plus ~0.5 engineer-day/week | E1 | E1 owns; E2 produces evidence |
| 6 | **SOC 2 Type II: fieldwork and report** | **Fieldwork 2027-05; report issued 2027-06-30** | Six months after this company was founded is early for a Type II and *perfectly normal* for a first one. The report is issued before M2's paid pilot converts, so the first paying non-design-partner customer sees one | Item 5 complete; a system description; the auditor's sampling; the complementary user-entity controls the customer must implement (and which we document for them) | included in item 5 | E1 | The audit firm; E1 on the management response |
| 7 | **Penetration test** | **2028-01-31**, before M3 GA | `07` §11.1's commitment: "**We buy one before M3 GA from someone outside the company** — the first outside pair of eyes on the identity, the release and the evidence path is worth more than a year of our own tests, and the threat model is the scope document we hand them." M3 GA is 2028-03-31, so January leaves time to fix what it finds | Item 3's review, done and remediated; a stable GA candidate; a written scope and a written authorisation to test (`12` §10.4's safe harbour applies to *them* as testers of us, and our own out-of-scope statement should be explicit) | EST. low-to-mid five figures | E1 | An external firm, 5–10 days |
| 8 | **ISO 27001: ISMS operating** | **2027-07-01 → 2028-01-31** | Starts the clock the moment the SOC 2 evidence set is stable, because ISO 27001 is mostly the same controls plus an ISMS: scope, statement of applicability, risk treatment, internal audit, management review. Six months of operation is the usual minimum before Stage 1 | Items 1, 2, 5; a risk register (`` — it exists and is the ISMS risk register, which is a nice property); a statement of applicability over Annex A | EST. five figures in certification fees; consultant support extra | E1 | E1 is the management representative; E2 is the ISMS operator |
| 9 | **ISO 27001: certification** | **2028-06-30** | Before M4's platform expansion, because the OEM and integrator channel (§8.6 of this document) will not sell into a large customer without one, and M4 is where the channel starts | Item 8; Stage 1 and Stage 2 audits; internal audit findings closed; a management review with minutes | EST. five-to-low-six figures total | E1 | A certification body |
| 10 | **EU AI Act readiness** | **2027-06-30** for the legal classification; **2028-09-30** for the documentation | Classification first, because everything else depends on it and it is a legal question, not an engineering one. Documentation second, after ISO 27001 gives us the management-system evidence to point at | Item 4's counsel engagement; the AI governance record from `13` §4.4; the disclosure package from §5 of this document; the human-oversight model from `01` M7 | EST. five-to-mid figures for the legal classification; documentation is mostly internal once the classification is known | E1 | **External counsel**, plus E1 internally |
| 11 | **ISO 42001 (AI management system)** | **2029-06-30**, after 27001 | It is built *on* ISO 27001, so it is impossible before item 9, and it needs the AI governance record (`13` §4.1's last four rows) to be real rather than designed. **It is also the cheapest differentiator in this table and the one a competitor is least likely to have** | Item 9; a populated model and profile registry; a documented promotion process with ≥1 real shadow run and ≥1 real promotion decision; the incident-notification commitment (§5.8) in a contract | EST. five-to-mid figures | E1 | A certification body; E1 as management representative |
| 12 | **NIS2 / sector supply-chain evidence pack** | **2029-03-31**, before M5 Enterprise GA | M5 is where we sell to critical infrastructure (`01` M3's segment 5). NIS2 binds the operator, not us; our obligation is to *be usable as evidence* | Items 1, 5; the logging, incident-reporting and supply-chain artefacts the operator must show their own regulator — mapping those to our product is the deliverable | 0–low internal | E1 | E1 |
| 13 | **A bug bounty** | **Not before M8** | `12` §10.4: "We do not run a bug bounty programme yet. We will say so plainly rather than imply otherwise." A bounty we cannot pay promptly is a reputational liability, and a security product that fails to pay a researcher is a security product nobody reports to | M8 revenue, a triage process that does not interrupt the roadmap, and a published scope | EST. per-report plus platform | E1 | deferred |

### 2.1 The critical path, stated as arithmetic rather than as ambition

The SOC 2 date is not chosen. It is derived, and the derivation is what makes it
urgent:

```
  2026-11-30   gap assessment delivered          ← we are 3 months from this
  2026-12-01   controls begin operating
  2027-03-31   4 months of evidence              ← the minimum credible window
  2027-05      fieldwork
  2027-06-30   report issued
                          =  9 months from today
```

**Nine months, and the first step is a purchase decision that has not been
made.** If item 4 slips by a month, the whole chain slips a month. If it slips by
three, the observation window is too thin for most firms and the report moves to
Q1 2028 — which is M3 GA week, which is the worst possible moment to have
"our SOC 2 is in progress" in a sales conversation.

**And the thing that makes it uncomfortable:** a first SOC 2 Type II for a
two-person company has a structural problem an auditor will find. The trust
services criteria include separation of duties and the requirement that
"the person who performs an activity is different from the person who reviews
it". We are two people. **The control that answers this already exists and is
the right one: the `07` §10.3 Sev-1 protocol, where "a Sev-1 means the IC does
not write code, does not run the deploy, and does not talk to the customer."**
That is a genuine two-person segregation-of-duties control and it should be
documented as one rather than improvised at audit time. Similarly, the release
gate's rule that a line is signed by someone other than the person who did the
work (`07` §9.1: "A line signed by the person who did the work is not a gate")
is the change-management control.

**One more thing the audit will ask and we have not answered:** the AI agent
writes most of the code. An auditor will ask how access to a code-generating
system is controlled, what it may do, and who reviews its output. The answer
exists — `` §4 is the rules of engagement, and
`13` §11.3 says an agent may not accept a risk — and it needs to be a written
policy, not a conversation.

---

## 3. Regional and vertical obligations

Format for each: **what we do now / what we must not do / when to revisit.**
The middle column is a list of things that would be easy to drift into
accidentally, which is exactly how a company ends up processing biometric data
without deciding to.

### 3.1 GDPR / UK GDPR and video surveillance

**What we do now.**
Captain processes video-derived data on the customer's own infrastructure, on
the customer's network, under the customer's instruction, and does not
transmit it anywhere. The data is a bounded set of snapshots and clips cited by
incidents, stored content-addressed on the customer's own disk, with a
site-configurable retention and a legal hold the sweeper respects. We are, in
data-protection terms, **a processor** for a customer's operator who is the
controller, and the product architecture is what makes that a small processing
relationship rather than a large one.

**What we must not do.**
1. **No facial recognition, no biometric identification, no 1:1 or 1:N matching,
   no face templates, no gait analysis, no voice identification.** Not in M1,
   not in M2, not in M3. M1's position is structural: there is no person model,
   and `schemalaws.TestNoEntityAliasTableInM1` is a test asserting the *absence*
   of that capability.
2. **No drift into it silently.** This is the actual risk, not the capability.
   The features that make biometric processing feel unavoidable are all
   individually reasonable: face-blur on export, a "do not show faces to
   contractors" mode, licence-plate OCR (already a *shadow* profile,
   `vehicle-at-gate`, with `plateLegible` and `plateText` fields), person
   re-identification across cameras, "find this person in other footage".
   **Each of those is a biometric-processing decision in disguise, and each must
   clear the DPIA-lite (`13` §5.5, questions 7 and 8) and legal advice before
   it is designed, not after it ships.**
3. **No re-identification of a tracked person over time** — a persistent
   identifier that follows a face or a plate is a biometric template whatever it
   is called.
4. **No use of customer data to train anything.** (§8.4's first AI clause.)
5. **No real biometric data in the repository or in CI** (`07` §8.4 rule 1).

**On lawful basis.** A customer processing CCTV of identifiable people in a
workplace will usually be relying on **legitimate interests** (Art. 6(1)(f)
GDPR; the UK equivalent), with a balancing test, signage, and a retention
policy. We are not the controller and we do not make that call. What we must do:
(1) document our processing accurately enough for the customer's record of
processing activities; (2) never process beyond the customer's documented
instruction; (3) support the customer's ability to answer a data-subject request
— and here there is a real gap, because a person asking "what did you record of
me" is a request to the **customer**, who must then find it in our store, and
the product today has no DSAR workflow. That is M3 work and it should be on the
backlog before a regulated customer asks.

**When to revisit.** At every milestone review, via the DPIA-lite. And
immediately — not at a milestone — the moment any feature request mentions a
face, a plate, a person, "find the same", or "reduce privacy".

### 3.2 Illinois BIPA and the state-law family

**The concrete risk, named.** The Illinois Biometric Information Privacy Act
applies to "biometric identifiers" including **face geometry** and **retina and
iris scans**, and it has private rights of action with statutory damages per
violation and per person, and a **$1,000,000 per violation** penalty for a
violation that is wilful or negligent as against a minor. Texas, Washington and
Colorado have similar or broader statutes; Washington is notable for a private
right of action with no cap.

**The answer is the same answer as §3.1: we do not process biometric
identifiers, we do not create a template, and we do not identify anyone.** The
distinction that matters, and that we should write down once so it is not
re-litigated by a well-meaning feature request:

> **We analyse images. We do not perform biometric identification.** A camera
> frame showing a person is personal data. It becomes *biometric data* when
> something extracts a template from it or uses it to *identify* a person. We
> do neither. Our AI profiles answer structured questions about a scene
> (`restrictedZoneBreach`, `personCount`, `doorState`, `afterHours`) and do not
> produce an identity, a template, or a match against any gallery.

**What we must not do.** Store face crops; run a face-matching feature; export
a face crop even for "the customer's records" without the customer having
decided that lawful basis; accept a customer request to identify a person; or
ship a capability we cannot describe in one paragraph of the above.

**When to revisit.** Whenever §3.1's list is at risk — and before any US
government, school, healthcare or biometrics-adjacent customer, where a
statutory-damages class action is a procurement blocker regardless of whether
we are liable.

**A useful distinction for the sales conversation.** A customer's own
*existing* VMS may already be a BIPA controller, and a customer asking us to add
analysis to that estate is asking us to sit inside a legal position they already
have. **We will not give legal advice on whether that position is sound, and we
will not provide a feature whose legality depends on the answer.** That sentence
goes in the sales deck.

### 3.3 The EU AI Act — **REQUIRES LEGAL ADVICE**

> **This section is written by engineers to be a good brief for a lawyer, and it
> is not legal advice. Marked as such deliberately. Nothing here may be stated to
> a customer as a conclusion about their obligations.**

**Why it might apply to us.** The EU AI Act's high-risk obligations attach by
Annex III use case, and one of the Annex III categories covers AI systems
intended as **safety components in the management or operation of critical
digital infrastructure, road traffic, or the supply of water, gas, heating or
electricity**. A physical-security intelligence layer that verifies events and
(from M7) escalates to physical response sits uncomfortably close to that line.
Separately, Annex III covers AI systems performing **biometric identification or
categorisation**, which we do not do (§3.1), and systems used in
**employment and worker management**, which is relevant when a customer's
security team evaluates staff. The Act also carries a specific carve-out for
certain security functions performed by or on behalf of law-enforcement
authorities, which is **not** our customer's typical case but is a data point
the statute's own drafters considered relevant to security AI.

**The four questions to put to counsel, in this order, because each one's answer
narrows the next:**

1. **Is Captain a "high-risk AI system" under Annex III at all?** Specifically:
   is a system that *corroborates* and *raises an incident* for a human to
   action a "safety component" in the management or operation of critical
   infrastructure, or is it a monitoring-and-informing tool outside the scope of
   Annex III? The answer changes the obligations by an order of magnitude.
2. **Which of these are we, and which are the customer?** We are the **provider**
   of the AI system. The customer is the **deployer** and holds most of the
   operational obligations (human oversight in operation, use per instructions,
   monitoring, informing workers, keeping logs). **Our contractual job is to be
   the provider a deployer can actually comply with** — which is §5 of this
   document, the disclosure package, and nothing in it is optional.
3. **What are the provider obligations, and when do they bite?**
   Technical documentation, a record of the training/evaluation process, a
   logging system, transparency and instructions for use, human-oversight
   measures, accuracy/robustness/cybersecurity requirements, conformity
   assessment, CE marking, registration in the EU database, and a post-market
   monitoring plan. **Timing matters and changes over the regulation's
   implementation period — which is precisely why this is a question for counsel
   and not a reading of a web page.** We should not state a date we have not had
   confirmed.
4. **Is there a "safety component / substantial modification" trap for a
   product that installs a customer's own rule bundle and a customer's own model
   bundle?** If a customer activates a high-autonomy profile under their own
   policy, are they modifying the system such that some obligations transfer to
   them, and is our documentation good enough to *show* that they did? **This
   question is about M7 and it is the reason `13` §4.4's "approved use cases"
   field must be a closed vocabulary rather than free text: a closed vocabulary
   is a set of intended purposes we can point at, and free text is a set of
   claims nobody can bound.**

**What we do in the meantime, without waiting for the classification.** Build
the artefacts, because they are the same artefacts every other answer needs:
the technical documentation is the AI disclosure package (§5); the logging
requirement is `internal/audit`; the transparency requirement is the structured
observation schema with its attribution; the human-oversight requirement is
`01` M7's autonomy levels with a blast-radius preview and a documented
rollback; the accuracy requirement is `internal/evaluation`'s named gates and
the "known failure modes" section of §5. **None of this work is wasted under any
of the four answers**, which is why it is worth doing before the classification
arrives rather than after.

**When to revisit.** M2 (the investigation engine adds an LLM that *plans*,
which changes the system's description), and M7 (autonomy with physical
consequence). Plus any EU customer at any time — the answer "we are not in
scope" is an answer a lawyer gave, not one an engineer guessed.

### 3.4 NIS2 and critical-infrastructure customers

**The structure to understand before writing a clause.** NIS2 (and the
transposed national laws) places duties on **entities in covered sectors** —
energy, transport, water, health, digital infrastructure, public administration,
and the entities supplying them. It does **not** place duties on a software
vendor merely because the vendor's customer is covered. What a covered customer
is actually looking for from us is **evidential**: can they show a regulator
that their security operations are logged, monitored, and reported?

**What we do now.** The artefacts that answer most of the question already
exist: a hash-chained, append-only ledger with a signed offline seal, verifiable
with no network and no database; per-actor, per-action attribution; a
break-glass account whose every use raises a high-priority audit event; explicit
degradation states rather than silent absence; and a release that is a signed
offline artefact with an SBOM and a reproducible file set. **That is a
substantially better answer to "show me your audit trail" than the incumbent
VMS's, and it is worth saying out loud in a NIS2-adjacent sale.**

**What we must not do.** Accept an obligation to *report* to a regulator on the
customer's behalf — that is the operator's duty and ours is a contractual
information duty. Accept liability for the customer's own security posture. Ship
an incident-notification commitment we cannot staff (see `12` §7.3: we do not
sell 24/7 before M3).

**When to revisit.** M5, when critical infrastructure is a named segment in
`01` §3. And note: the transposition and the sector definitions differ by member
state, so the answer is per-jurisdiction, which is a lawyer's product and ours
to fund.

### 3.5 HIPAA and healthcare deployments

**The likely answer, stated precisely.** A software vendor is a **covered
entity** only if it operates a covered business (a health plan, a health care
clearinghouse, or a health care provider). We do not. Therefore **we are almost
certainly not a covered entity.** What we probably are, if a covered customer
uses us and we have access to PHI, is a **business associate** under
45 CFR 164.504(e) — which triggers a Business Associate Agreement.

**The nuance that makes this a real question rather than a rhetorical one.** We
install on the customer's infrastructure and the customer's own data stays
there. If the platform's operator, our support engineer, or our diagnostic bundle
ever touches a system containing PHI, we have a BA relationship and we need a
BAA. So the design question is: **can our product be operated, supported and
diagnosed without our accessing PHI at all?** On-prem-by-default, evidence
staying on-site, support bundles that are redacted and customer-reviewed, and
"the site can answer 90% of 'it is broken' with the product and the runbooks on
their machine" (`12` §5.6) — that design is a HIPAA control. **It is also the
strongest data-residency argument in §4.** These are the same decision.

**The concrete risk, named.** A support engineer on a laptop with a database
dump of a hospital's incident data is a HIPAA incident and a headline. So:
support access is the customer's to grant, per-incident, and is audited;
diagnostic bundles are generated by the customer's own operator, are covered by
the sanitiser (`internal/fixture.SanitizeResponse`) and are customer-reviewed
before they leave the site; and no customer's data is ever copied to a
developer machine for debugging. **The last one is a hard rule with no
exception, including "it's just a dev site"** (`07` §8.3: capture → commit →
sanitise has no supported reversal).

**What we must not do.** Store PHI in a support ticket, a chat message, a log
line, a test fixture, or a golden file. Log a raw event payload at a site we are
diagnosing. Accept a customer request to send us their database.

**When to revisit.** The first healthcare customer, which per `00` §3 is M5+.
Before that, get a BAA template reviewed by counsel — it is a standard document
and the cost is a legal review, not a project.

### 3.6 Summary table

| Regime | Are we in scope? | What we must not do | Revisit |
|---|---|---|---|
| GDPR / UK GDPR | **Processor** for a customer's controller | biometric identification of any kind; process beyond instruction; hold a DSAR-less system and call it compliant; send customer data to a developer machine | every milestone (DPIA-lite); immediately on any face/plate/"find the same" request |
| Illinois BIPA and the state family | **No**, because we identify nobody | store or export face crops; face matching; accept an identity request | before any biometrics-adjacent US customer; with §3.1 |
| EU AI Act | **Unknown — legal advice required** | state a scope conclusion to a customer; drift into high-autonomy physical action without a human-oversight story | M2, M7, and any EU customer |
| NIS2 | **Not directly**; evidential duty only | accept a regulatory-reporting obligation; accept liability for the customer's posture | M5 |
| HIPAA | **Probably not a covered entity; possibly a business associate** | touch PHI in support, logs, tickets or fixtures; copy a customer DB to a dev machine | first healthcare customer (M5+) |

---

## 4. Data residency and sovereignty

### 4.1 The posture, which is our strongest compliance answer and it is a real one

```
  Everything that matters stays on the customer's network, on the customer's
  hardware, under the customer's key, and can be verified by the customer's
  compliance officer with our servers switched off.
```

Concretely, and each of these is a real property rather than a promise:

| Property | Where it is true today |
|---|---|
| Video never traverses our infrastructure | We are not in the video path at all (`12` §8.7) |
| Evidence bytes live on a content-addressed filesystem the customer controls | `internal/evidence`; the database holds addressing, hashes and retention law, not bytes |
| The audit ledger and its signed seals are verifiable **offline, with no database and no network** | `securityctl audit verify-seals`; demonstrated in `docs/status.md` |
| No runtime component fetches anything | `12` §3.1: no CDN, no licence check, no telemetry, no crash reporter, no update check, no online revocation check |
| A customer's exported audit record is theirs | the ledger, the seals, the evidence and the incident package are all on their disk and verifiable there |
| We can be disconnected and the site keeps working | the architectural constraint from M1, and the M5 exit gate's fault-injection test |

**This is the answer we lead with in every regulated deal, and it is worth more
than any certificate** — because a certificate describes a management system
and this describes the deployment. A customer whose legal department cannot
approve a cloud vendor for a certain jurisdiction can approve this, and often
faster.

### 4.2 The four open questions, each with a decision date

The posture above is real but it is **currently undermined by four unresolved
decisions**, and none of them has an owner or a date. This is the most
actionable section in the document: each of these is a small decision with a
large commercial consequence, and each of them is a way that a product whose
entire pitch is "it does not leave your network" turns out to have a phone-home
in it.

| # | Open question | The problem in one sentence | Options | **Decision date** | Owner |
|---|---|---|---|---|---|
| **DR-1** | **Is telemetry ever allowed, and in what form?** | `12` §3.1 says no runtime fetch of any kind. But a two-person company learning nothing from production is a company that will be surprised by its own product, and the usual answer (phone home) contradicts the pitch | (a) **Never.** Nothing leaves. (b) A **customer-initiated, one-way, explicitly-toggled** export of a metric bundle, scrubbed by the same sanitiser, never automatic, disabled by default, and visible in the ledger. (c) A separate, opt-in **customer-run** collector the customer owns | **2027-01-15**, before M2's paid pilots and before the first enterprise conversation, because retrofitting a phone-home is far worse than never having one | E1, with E2 |
| **DR-2** | **Support bundles: what may leave a site, and under what procedure?** | The operator needs to send us something to diagnose. That something sits next to a database of incidents and next to a video evidence store | (a) A `securityctl support-bundle` command that emits a **redacted, hashed, size-capped** bundle, generated by the customer's operator, **reviewed by them**, and the bundle's manifest hashed into the ledger so we can prove what we were given. (b) Ad-hoc. (c) Screen-share. **Never: a database copy, and never evidence bytes** | **2026-12-15** — this blocks the first real support incident, and an ad-hoc first answer becomes the practice | E2 implements, E1 owns the procedure |
| **DR-3** | **Licence validation** | `12` §3.3 recommends option E: perpetual per-site licence, enforcement in the fleet view, **never a site-side gate on operation**. But the fleet view is M5, and until then there is no enforcement at all | (a) Ship E now: perpetual, contractual renewal, no technical enforcement until the fleet exists. (b) Ship B: expiring, enforced by the site clock, defeatable by clock manipulation and we would say so. (c) D: online activation — **rejected outright**, it makes us a liability when our server is down | **ADR before M3 GA**, and the interim answer is (a) plus a written statement in every design-partner agreement. `12` §3.3 recommends exactly this | E1 |
| **DR-4** | **Model downloads and licence terms for model weights** | A model bundle's **weights** are a separate thing from its **outputs**. Where a customer's legal team reads "model", they will read "is the vendor's IP being sent to a third party" and then read the model provider's licence terms, which may forbid commercial redistribution in a bundle we ship to a customer | (a) Ship only weights whose licence permits offline redistribution, checked in the bundle's SPDX record. (b) Let the customer obtain the weights from the provider themselves and install them from their own media. (c) A curated bundle repository with per-model licence terms in the manifest. **Note: the SBOM is the mechanism that makes (a) checkable rather than assumed** (`13` §3.4 rule 4) | **2027-06-30**, before M2's VLM profile library (ten profiles is ten licences) | E1, with counsel |
| **DR-5** | **Where does the central plane's index live, and whose jurisdiction is it in?** | M5's central plane holds "a fleet index, not the truth". An index of sites, versions, health and storage is still personal data about a customer's estate, in *our* jurisdiction, which may be the wrong one | (a) Central plane is **region-pinned**, deployed inside the customer's jurisdiction, or into ours in a jurisdiction they nominate. (b) Central plane holds a **pseudonymous** fleet index with no site names, no topology, and no evidence references | **ADR by M4's exit gate (2028-12-22)** — it is a topology decision, and topology decisions are expensive to change | E1 + ADR |

### 4.3 The one residency claim to be careful about

It would be easy to say "we are GDPR-compliant because we host nothing". That
is not a defensible sentence, and it is wrong in three ways:

1. **We do access data** — as a processor, in the sense of §3.1, and once
   support bundles exist (DR-2) we demonstrably hold customer-derived data
   temporarily. So a DPA is required (§8.2), not optional.
2. **The customer's data is in the database on their host, and the host may be
   operated by a third party.** The customer is the controller and we are a
   processor, but the *site administrator* may be a third party with their own
   obligations. Our contract has to say which of us is instructing whom.
3. **The model weights are ours or the provider's, and a licence is a legal
   relationship that can be terminated.** DR-4.

**The sentence we may say:** *"Video, evidence, the audit ledger and the
checkpoints remain on your infrastructure. Our product makes no network request
at run time. Where we handle data for you at all — support bundles — it is at
your instruction, on your machine, redacted, reviewed by you, and recorded."*
Every clause of that sentence is checkable by the customer, which is the whole
point.

---

## 5. The AI disclosure package — a product artefact, not a legal one

> **This is a product artefact.** It is a document the product emits, in a form
> an operator or a customer's risk officer can read at their site, without
> contacting us. It is built with the release and shipped in the archive,
> because "it is in our documentation" is not a thing a customer at an isolated
> site can check.

**Why it is the highest-leverage compliance artefact in this document.** It is
what makes `00` §2 point 3 ("every serious buyer now asks which decisions a
model made, what evidence supported them, and what the system does when the
model is unavailable. Almost no incumbent can answer those questions. Answering
them is the product") into a document a procurement file can point at. It is
also, item for item, most of what an EU AI Act provider's transparency and
technical-documentation obligations ask for — so it does double duty without
being written twice.

**Shape: `securityctl ai-disclosure --profile <id>` and a static
`docs/AI-DISCLOSURE.md` in the release archive, generated from the registry so
it cannot drift from the code.**

### 5.1–5.8 The eight sections

**1 · What the AI does.** In one paragraph per profile, in the operator's
vocabulary. Example shape for `restricted-person`: *"Given frames from the
cameras mapped to a restricted zone, and the time and the site's timezone, it
answers structured questions: is a person present in the restricted zone, how
many, what the door state was, is this after the site's defined hours, and is
there enough in the frame to say at all. It answers in a fixed schema. It does
not identify anyone, produce a name, or produce a biometric template."*

**2 · What the AI does not do.** The negative list is the more important half
and it is a **closed list enforced in code**, not prose:
no biometric identification or categorisation; no face, plate, gait or voice
identification; no 1:1 or 1:N matching against any gallery or watchlist; no
persistent identifier that follows a person; no inference about a person that is
not visible in the frames shown; no training, fine-tuning or improvement of any
model on customer data; no network request at run time; no access to the audit
ledger, to identity, to rule bundles, or to dispositions — **enforced by database
grants**, `schemalaws.TestAIRuntimeCannotAlterAuthoritativeState`.

**3 · The data it processes.** The exact input list with its source: frames and
clips from named cameras at named times, the site's timezone and the device's
clock-drift measurement, the zone path from the Security Graph, and the site's
configuration. **With the classification of each item** (from `13` §5.1) and the
statement that the input may contain personal data including, in some frames,
faces. Stated in a customer's language, not ours.

**4 · The model and version.** From the attribution that is already mandatory
(`internal/observations`): model identity, model hash, prompt template id and
version, runtime version, model-bundle signature, output schema version, the
hardware it ran on, and the input evidence hashes for any specific observation.
**Every one of those is already a required field, so this section is generated
from data we already refuse to store an observation without.**

**5 · The evaluation results and their limits.** The `internal/evaluation`
`Report`: labelled cases, false-positive rate, recall on true positives, median
latency, disagreement rate, and **every failing gate named**. Plus the four
things the number does not tell you, stated in the artefact:
* the dataset is not the site's traffic; * a rate from N cases is a number and
not an estimate until N is large enough (`MinLabelledCases`); * a case nobody
judged is counted as an absence and never folded into a rate
(`TestUndispositionedCasesAreNeverFoldedIntoARate`); * a profile that stays
silent gets recall 0 and fails, while a profile that is *unavailable* gets a
degraded count and no recall penalty, "because it did not fail to answer; it was
not asked in a way that produced an answer."

**6 · The known failure modes.** A per-profile list, and this is the section
customers will read most carefully. For a VLM reading CCTV, the honest list is:
low light and glare; heavy compression artefacts; occlusion and partial views;
a camera aimed at a ceiling or a wall; a person wearing a mask, helmet or
uniform; unusual clothing; a scene the camera never saw during evaluation
because it is a new site; **a scene crafted to be misread**; and a site's
lighting changed after evaluation (§6 of `01` introduces exactly this as drift
detection, so we will detect it before we fix it). Each entry gets a
**mitigation or "none known"** — an entry with neither is not allowed in the
registry, and that is a schema constraint, not a review convention.

**7 · The human-oversight model.** Who can stop it, what they see, and what
happens when they do not. Today: the deterministic core decides; the model is
Tier C; every rule consuming a Tier C signal declares `if_ai_unavailable`; a
shadow profile cannot affect authoritative state; a missing observation is never
a negative. From M3: playbooks and recommendations with a policy gate, and
anything with physical impact stays approval-gated. From M7: autonomy levels
A0–A4 chosen by the customer per site, where a level change is an explicit,
audited, reversible act with a blast-radius preview. **The one sentence that
matters, and it is already true and testable:** *"The system will never tell
you that a model outage means an event did not occur."*

**8 · The incident-notification commitment.** Exact text, not a paraphrase,
because a commitment that exists in three versions is not a commitment. See §5.8
below for the text itself.

### 5.8 The incident-notification text

This is the wording a customer's counsel reads first, and it is deliberately
narrower than what we would like to promise, because a promise we cannot keep
is the one failure mode this whole product is against. It is derived from
`12` §10.1 and §10.3 and adds nothing we have not already committed to.

```
INCIDENT NOTIFICATION — what we commit to, and what we do not

WE NOTIFY YOU when the product made a decision that was wrong and that
decision affected your understanding of a security event. That includes, and
is not limited to: a real security event the platform did not raise; an
incident that was auto-dismissed and was real; an accepted event that was
lost; an action the platform took that it was not authorised to take; a
record that cannot be verified; evidence that was lost or altered.

  ·  We tell you BEFORE you find out, and before we understand it.
  ·  We tell a named person, by a channel that authenticates itself, within
     the severity's clock: 1 hour for an actively exploited vulnerability,
     4 hours to contain a severity 1, 24 hours after a confirmed fix for
     anything requiring you to act.
  ·  What we send is: what happened, what it means for your records, what we
     are doing, what we do not yet know, and the time and name of the next
     update. Facts and a clock. No cause we have not established, and no
     "we are investigating thoroughly".
  ·  We tell affected customers before we publish anything publicly, and we
     tell every customer who ran the affected version whether or not they
     were affected, because a defect that only bit one site is a defect that
     APPEARS to have bitten one site.
  ·  We correct by changing the product, not by explaining. A correction in
     the record is a new record that says the old one was wrong and why. We
     do not rewrite your history: a system that can be edited after the fact
     is a system you have to believe rather than one you can prove.

WE DO NOT NOTIFY YOU about, because these are not defects:
  · a limitation we disclosed in the release notes before you installed it;
  · the incident feed being eventually consistent — the console states the
    incident's state rather than implying completeness;
  · a site's own camera or VMS failing. That is the site's to detect, and
    our product does not sit in the video path.
  · a restore that is behind a sealed checkpoint. We will always tell you,
    and we will tell you the exact size of the gap — but it is a property of
    backups, not a defect of ours, and you can size it before you need it.

WE CANNOT COMMIT TO:
  · 24/7 response, before we sell it. Until we staff it, we tell you our
    support hours in writing, and a severity 1 outside them reaches one named
    engineer by phone at any hour.
  · an RTO or RPO we have not measured on a build of your size. We publish
    measurements, not targets.
```

**The last block is the part that costs us sales and is therefore the part that
matters.** Every competitor's contractual surface promises 24/7 and an RTO.
Ours says what we can actually do, with dates. That is a harder sell and the only
one that survives a customer's incident.

---

## 6. Export controls and cryptography policy

### 6.1 Jurisdictions

**Position, and it is a simple one because the product is local-first.** We
ship software. We do not export hardware, we do not provide a hosted service,
and we do not operate in a customer's country. That means:

- **The primary sanction regimes (EAR, the Wassenaar arrangement) are very
  unlikely to bite on a self-hosted software product.** The control-relevant
  question is not "is the algorithm controlled" but "is there US-person or
  end-use restricted content involved".
- **Sanctions screening applies to who we sell to, not to what we ship.** A
  distributor, a reseller or an OEM in a sanctioned jurisdiction is a
  screening question, and it is a screening question for the resellers we will
  onboard from M4 onward.
- **The real question is a mapping one, and it is specific to us:** does the
  customer use the product at a site that is itself subject to an embargo, a
  sanctions programme, or an export restriction on the underlying camera or
  VMS technology? **We are downstream of Milestone/Genetec and of the model
  providers, and their licence terms may be stricter than any export control.**
  A model whose licence forbids use in a sanctioned jurisdiction is a licence
  question we answer at bundle-install time by refusing to install it — which is
  a control we can implement, and DR-4's SPDX record is how we know.

**Action:** a written export-control and sanctions-screening note, one page,
reviewed by counsel, before the first OEM/reseller conversation at M4. **It
should cover sanctions screening of channel partners and the licence-terms
question for model weights, and it should say plainly that we are not a defence
export.** Cost: one legal review. Owner: E1. By: 2028-03-31.

### 6.2 Cryptography in our own products — a usage policy, not a practice

| Where | Algorithm | Why | Where the key lives |
|---|---|---|---|
| Release archive signature | **Ed25519** | a site verifies with a small dependency-free implementation and a key that is cheap to rotate; an air-gapped site cannot ship a crypto library to check a signature | Offline store, never in the repository, never in CI (`12` §1.3); two keys with rotation before M3 GA |
| Audit checkpoint seal | **Ed25519** over a length-prefixed payload | same reason, plus "a delimited join is ambiguous, and an ambiguous signature is a signature that can be moved from one seal to another" (`internal/checkpoint`) | Installer-managed key file; the private half is never in a seal, a config file, a log, or the ledger |
| Evidence content addressing | **SHA-256** | content addressing, deduplication, tamper *detection* on read | Not a key. The concern is tenant-collision, and `TestIdenticalBytesDeduplicateAndCannotCollide` covers it |
| Audit hash chain | **SHA-256**, computed by a database trigger | the chain must be computable by the database so the application cannot lie about it | — |
| Session tokens | 32 random bytes, stored as **SHA-256** | "a database dump does not yield sessions" | — |
| Passwords | **Argon2id**, parameters in the build not the row, with a floor enforced on verify | "an attacker who can raise the cost factor cannot"; a database row does not get to choose the site's password cost | — |
| CSRF tokens | 32 random bytes, hashed at rest, session-bound, rotated with the session | "a CSRF token cannot be forged by reading one out of a browser that already had it" | — |
| TOTP | **HMAC** — and see §6.4 | — | wrapped by `SecretStore`; "there is no implementation here that holds a key" |
| Transport | **TLS 1.2 minimum**, no plaintext fallback unless a proxy is declared; `sslmode=require` even on loopback "so a misconfiguration cannot silently downgrade" | — | customer's certificate; private key from the secret store, not from config |
| TOTP implementation note | RFC 6238 with a 30 s step, ±1 step of skew | "a client whose clock is a second fast otherwise fails in a way that looks like a credential problem" | — |

**The policy, five rules:**

1. **No custom cryptography, ever.** Every primitive above is a published
   standard implemented by a standard library. A hand-rolled cipher, a hand-rolled
   MAC, or a bespoke signature scheme is a security finding on sight.
2. **No algorithm in the rule language.** `AGENTS.md`, and enforced by
   `rules.TestNoUnboundedRegexOperatorExists` and the shape of the language: no
   regex, no shell, no SQL, no network, no dynamic code. **This is a
   cryptography control as well as a safety control** — an unbounded pattern is
   a denial-of-service primitive in a bundle a customer uploads.
3. **Keys are not in the product.** Not in `config.toml`, not in the environment,
   not in a log, not in a release archive, not in a database column that a
   migration can print. `identity.SecretStore` is an interface precisely so that
   the package that needs a wrapped secret does not hold a key.
4. **Every key has a rotation story before the first customer.** The release key
   and the checkpoint key both do (`12` §1.3: two keys from the start, rotation
   a minor release, the previous key's signature remains verifiable for a stated
   period). A customer on one key who loses it loses the product, which is why.
5. **A cryptographic change is an ADR, not a PR.** Including a parameter change.
   `identity.VerifyPassword` refuses a stored hash weaker than the build's floor,
   so a dependency bump in `golang.org/x/crypto` is an availability event for
   every account, not a security improvement.

### 6.3 Encryption and key management for an air-gapped install

**The answer: customer-held keys, and our product must work entirely without
ours.** The design already supports this; the policy states it:

| Key | Whose | How it reaches an isolated site | What happens if it is lost |
|---|---|---|---|
| Release signing key (private) | **Ours, offline, never on a site** | Never transferred. Only the **public** key reaches a site, on different media from a different person | Nothing at the site. We lose the ability to ship; that is our problem, and `12` §1.3's two-key scheme is the mitigation |
| Checkpoint signing key (private) | **The customer's**, written by the installer at install time | Generated on the site | **The site can still run and still read, but cannot seal new checkpoints.** Existing seals stay verifiable. The console says so. This is the correct failure mode and it must be stated in `RUNBOOK-break-glass.md`'s sibling: a lost checkpoint key is a *future* integrity gap, not data loss |
| TLS private key | **The customer's** | Their certificate, their key, from the secret store | The console is unreachable; the CLI still works. `12` §8.1a |
| Database password, TOTP wrapping key | **The customer's** | Secret store, never config | Authentication unavailable; break-glass exists and is audited on every use |
| Evidence at rest | **The customer's decision** | The store is a filesystem; if the customer wants encryption at rest, that is the volume's job | — |

**What we must not do:** require a key we hold in order for the product to
operate; phone home for a key; store a customer's key in anything of ours; or
offer a "managed key" option that puts our key in an air-gapped site (a
contradiction we should name if a customer asks for it — some will, and it is
worth having the sentence ready).

### 6.4 The cryptography defects found in this tree

Named here because §6.2's rule 5 says they are ADRs and because a cryptography
policy with two open defects in it is a policy nobody has read.

| Finding | Where | Consequence | Action |
|---|---|---|---|
| **The WebAuthn assertion signature is not verified on the authenticating path.** The counter is checked and the function returns `true`; the comment attributes the signature check to "the platform's WebAuthn verifier, which holds the algorithm registry". A real verifier — `VerifyAssertion` in `internal/identity/webauthn.go`, with COSE key validation and ECDSA/EdDSA signature checking — **now exists but is not called by `verifyFactor`**, so the path that authenticates an operator still reduces to a counter check | `internal/identity/mfa.go:212-239`, reached from `verifyFactor` at `:93`; the unwired verifier is `internal/identity/webauthn.go`'s `VerifyAssertion` | Until the wiring lands, a WebAuthn factor is not a second factor. **This is a cryptography defect and an authentication defect and it is the single most consequential thing on this list.** The remaining work is small and specific: call the real verifier, bind `clientDataJSON` and the origin, and remove the stub so no future caller can reach it. **The gate: either the signature is verified on the authenticating path, or no build can enrol a WebAuthn credential** |
| **The TOTP algorithm does not match the enrolment URI.** The code computes `HMAC-SHA256`; the `otpauth://` URI declares `algorithm=SHA1` | `internal/identity/mfa.go:141` and `:188-205` | A standard authenticator produces SHA-1 codes that will not verify, so MFA silently fails at every site | Match the standard, and add a test that verifies a code against a known RFC 6238 vector |

**Neither is a roadmap decision and neither belongs in a "future work" list.
Both are M1 work with a Sev-2 severity under `07` §10.1** ("the product works
but is materially wrong, and a customer would be misled or slowed" — an operator
who cannot log in is worse than one who sees an error).

---

## 7. Records, retention and legal hold

### 7.1 The tension, stated as a tension rather than a solution

```
  EVIDENCE RETENTION (a legal matter)
    A site must be able to prove what happened. In many jurisdictions the
    retention period for security records is set by the customer, by a
    regulator, or by litigation. Deleting too early destroys a defence;
    deleting too late violates a deletion obligation.

  PRIVACY DELETION (a legal matter)
    Data protection law gives individuals rights that, in some circumstances,
    override a customer's own retention policy — erasure, and the right not
    to be subject to a decision made with automated means.
                                       │
                                       ▼
    The two are in direct tension and **no product can resolve it.** The
    correct posture is the one this product already has: the customer is the
    controller, the customer makes the call, and the product's job is to make
    the consequences of either choice explicit and auditable rather than to
    take the decision away.
```

**The three properties that make that posture real, and all three exist:**

1. **Retention is customer-configurable and a sweep is an operator action with a
   dry run, not a background timer.** `internal/evidence.Store.Sweep` takes
   `dryRun`; the comment is the reason: "deleting evidence on a schedule nobody
   is watching is how a site discovers its retention policy was wrong after the
   fact."
2. **Legal hold wins, and an unevaluable hold deletes nothing.** `Sweep` calls
   `SkipLegalHold`; an error from it records the error and **skips that object**
   rather than deleting it. `TestSweepFailsClosedWhenLegalHoldCannotBeChecked`.
   The general rule this encodes is worth restating because it is the right
   default for every destructive operation in this codebase: **the default for a
   destructive action whose precondition cannot be evaluated is to do nothing.**
3. **The audit ledger's retention is a legal matter, not a log policy.** `12`
   §4.3: operational logs are 30 days and rotated; the ledger "is governed by
   the site's retention configuration and legal hold, and it is never swept by a
   log rotation. That distinction is why they are two systems."

### 7.2 The legal-hold path for an incident under investigation

This is a **procedure**, because a legal hold that is a principle is not a hold.
Derived from `12` §4.4's "DO preserve" and from the Sev-1 protocol's T+1 step
("Stop any retention sweep. Copy the evidence store, the database, the checkpoint
sink, the rule bundles and the release manifests, before anything is cleaned up"):

```
LEGAL HOLD — who does it, and what it does

  WHO      An operator with evidence.export, or anyone the customer
           designates. A hold is not a privileged object: a site must be able
           to place one without us.

  WHEN     Before, not after. The Sev-1 protocol makes "stop any retention
           sweep" step T+1 — an investigation that runs a sweep is an
           investigation that destroyed the evidence. A hold placed after the
           sweep has already run is a hold that documents a failure.

  SCOPE    By incident, by evidence object, by time range, by device. A hold
           that holds everything is a hold nobody can lift.

  EFFECTS  (a) The evidence sweeper skips held objects — structurally, not by
               configuration.
           (b) An operator runs `evidence sweep --dry-run` and reads the output
               before anything is deleted. A dry run proposes and deletes
               nothing, and it names anything under hold so a human sees it.
           (c) **The hold itself is in the ledger**, with who, when, what and
               why, because a hold is a record about a record.

  LIFT     By a named person, with a reason, also in the ledger. "We will
           remember to lift it in six months" is not a lift.

  THE FAILURE MODE WE DESIGN FOR  The customer cannot delete something they
           needed, and the customer cannot accidentally delete something they
           were told to keep. Both are expensive; only one of them is
           irreversible.
```

**The gap to build (M3, before a regulated customer):** today legal hold is a
*query the sweeper makes*. It is not yet a first-class object with a scope, a
lifecycle, and an audit trail. That is real work: a `legal_hold` table, a scope
expression, an API and console surface, and the ledger entries. It should be a
**milestone gate for the first regulated customer**, not a nice-to-have, and it
is on the backlog in `` as a customer requirement.

### 7.3 Retention as an auditable, customer-configurable setting

**Every retention change is a ledger row.** Not a config edit — a row in the
hash-chained ledger with: who, when, which class of record (operational log,
evidence object, incident, audit ledger, AI observation, rule bundle, model
bundle), the previous value, the new value, the reason, and whether a hold
existed. Reasons an auditor asks and the row answers:

- *When was evidence retention last shortened?* → a ledger row.
- *Who shortened it?* → a ledger row.
- *Was anything under legal hold when they did?* → a ledger row naming the hold
  state at that moment.
- *What was deleted, and how much?* → the sweep's result, recorded: `Scanned`,
  `Removed`, `Freed`, `Errors`. The dry run's identical output is what makes a
  deletion defensible after the fact.

**And the rule about models:** retention covers **model bundles with a live
citation**, exactly as `TestSweepRespectsRetentionAndLegalHold` covers evidence
(`12` §3.4). "Deleting a model bundle whose observations are still cited breaks
a replay — and that is an invariant-2 problem before it is a disk-space problem."

### 7.4 Exportability — the test of whether it is really the customer's record

A record the customer cannot extract is a record the customer does not hold. The
four exports, and their status:

| Export | Status | Note |
|---|---|---|
| **The incident package** — incident, its events, its evidence, its observations, its dispositions, its decision's justification, and the audit rows covering it, as a signed, hashed bundle | **Partly built.** `securityctl incident show / explain / replay` exist; the *package* is a packaging job, not a capability gap | M2 |
| **The audit ledger + all seals** | **✅ available and verifiable offline** | `securityctl audit verify-seals`. This is the strongest export story in the product and it is already true |
| **Evidence bytes** | ✅ `Store.Open` with hash verification; a bulk copy is a filesystem copy because the store is content-addressed | — |
| **Everything, as a database dump plus the store plus the config** | ✅ `pg_dump` plus the directory, with secrets excluded and listed by name only (`12` §2.3.4) | — |

**The requirement to write into the roadmap:** an export must be *complete
enough to be a legal hold export* — meaning it must include the audit rows and
the seals, not just the incident. A customer who exports an incident without its
seals has exported a claim rather than a record. That is M2 work and it belongs
in the same commit as the incident package.

---

## 8. Contract templates inventory

`` owns nothing here; the commercial motion is in the
GTM documents. This section is the **inventory and the clause list**, because
the clause list is where the compliance risk actually lives.

### 8.1 The inventory, with a date for each

| # | Template | Purpose | Earliest need | Owner | Status |
|---|---|---|---|---|---|
| C1 | **Pilot / design-partner agreement** | M1–M2. No-charge, named reference, measured before/after investigation time, access to anonymised incident data for evaluation, the support-hours paragraph in `12` §7.3 **verbatim**, and the sentence that the licence is perpetual and the product will not be disabled for non-payment (`12` §3.3) | **now** — it gates the first external site | E1 | **blocks the first external customer** |
| C2 | **DPA** (data processing agreement, GDPR Art. 28) | Controller/processor roles, processing instructions, the categories of data and the classification from `13` §5.1, sub-processors (us, the customer's site administrator, the model provider for *weights only*), security measures mapped to our controls, breach notification, deletion/return at end of term, audit rights | with the first regulated or non-UK customer; arguably from C1 | E1 + counsel | **not started** |
| C3 | **MSA** (master services agreement) | Order forms, fees, term, renewal, the licence (DR-3's answer), service levels, warranties (deliberately narrow), limitation of liability, the governing law, and the AI-specific clauses of §8.4 | M3 GA (2028-03-31) | E1 + counsel | not started |
| C4 | **SLA / support schedule** | Response SLIs from `12` §7.2, support hours, credits, the on-call commitment **only from M3 and only if staffed**, RTO/RPO **only as measured**, and the list of what is not covered | M3 GA, and the 24/7 clause is gated on the rota existing | E1 | not started; the honesty clauses of §5.8 are already written |
| C5 | **OEM / SDK licence** (M4) | Per-seat or per-site licensing for a VMS OEM or integrator embedding the Adapter SDK; the reseller's own indemnity to their end customer; our support obligations to them; **who is liable to the end customer for a miss** — see §8.4's liability allocation; a private-label clause; the certification suite's terms | M4 first OEM conversations (2028) | E1 + counsel | not started; and §13 §10.3's B8 shows the security model is not designed yet |
| C6 | **Support contract** (stand-alone) | Hours, channels, response SLIs, a named human and a named backup by name, the runbook-as-the-answer rule, and what the customer may do without us (`12` §5.6) | M3 | E1 | mostly written; the content is in `12` |
| C7 | **BAA** (HIPAA business associate agreement) | Standard document, legal review only | first healthcare customer (M5) | E1 + counsel | not started; a standard document, so a review not a project |
| C8 | **AI addendum** | The AI-specific clauses of §8.4 — **extractable from the MSA so it can be attached to a pilot too** | with C1 | E1 | not started; §8.4 is the list |

**The one to get right first is C1, and it is not a legal document so much as a
commitment document.** `12` §7.3's support-hours paragraph and `12` §3.3's
perpetual-licence sentence both go in it in those words, because both are
sentences we would be embarrassed to be asked to change later, under pressure,
with a customer in the room.

### 8.2 The DPA's substance, so the review is not a blank sheet

The clauses that will be negotiated and the positions we take on each:

| Clause | Our position | Why |
|---|---|---|
| **Roles** | Processor for a customer's controller. The site administrator may be a separate controller for site operations; we follow the customer's documented instructions | The data-protection answer to "who decided to film this person" is the customer's, not ours |
| **Subject matter and duration** | The term of the agreement | — |
| **Categories of data subject** | Staff, contractors, visitors, and the general public in camera coverage. **We do not hold a person model** (`schemalaws.TestNoEntityAliasTableInM1`) | Stated as a product fact, and it is a strong one to state |
| **Categories of personal data** | From `13` §5.1's table, verbatim, with the classification. **Including the statement that evidence frames may contain faces and are therefore biometric data in the customer's hands** | Hiding that from a customer's counsel is the fastest way to lose the deal |
| **Special categories** | The product as shipped does not perform biometric identification and creates no biometric template (§3.1). We state our processing does not produce Art. 9 data, **and** we state the customer's own processing of video may | The honest version is "we do not add any; your footage already contains it" |
| **Sub-processors** | Us. The site's administrator, if any. The model provider **for weights, not for inference** — inference is on the customer's hardware. A general authorisation with a change-notification commitment, which is the norm | — |
| **Security measures** | The control matrix from `13` §9.1, described in the DPA's own words, referencing the threat model and the certifications as evidence rather than reproducing them | A DPA that reproduces the control matrix goes stale in a quarter |
| **Breach notification** | To the customer without undue delay and within a stated window; the content is the `12` §10.2 five sections | — |
| **Sub-processor changes** | Notification with a right to object. **A new sub-processor is a change to the disclosure package, and therefore an ADR** | Consistency with §5 |
| **Deletion / return at end of term** | The customer keeps everything — it is on their disk. Our obligation is to stop processing and, where anything of ours remains (nothing should), delete it and say so in writing | This clause is unusually short and that is the point |
| **Substantiation / audit** | SOC 2 report on request under NDA; a pen-test summary letter; the AI disclosure package; a site visit by mutual agreement | The commercial reality is a SOC 2 report and a summary letter, not a full audit |
| **Data localisation** | §4's table, with the four open questions named in the annexe and their decision dates | A DPA that promises a residency the roadmap has not yet decided is a promise we will break |
| **Breach of the agreement by a sub-processor** | Our liability, not a pass-through | A two-person company cannot pass a customer's compliance failure to a subcontractor |

### 8.3 The data-subject-rights position, and the gap

A data subject asks the customer. The customer must find it. Today there is no
DSAR workflow, which means a customer with a DSAR obligation cannot use the
product to answer one.

**The minimum viable answer, and it is a good one for this architecture:** the
platform already has almost everything a DSAR needs — content-addressed evidence
by hash, an indexed event store, an attribution chain on every row, a
time-indexed observation table. A search by a time range and a camera is most of
it. The work is (a) a query surface an authorised operator can run, (b) a
**consent-aware export with a redaction option** for the face-crop case, (c) an
audit record of the search itself (a search for a person's data is itself a
disclosure event and must be in the ledger), and (d) a documented position on
what we do **not** have to find, stated to the customer in advance rather than
discovered under a deadline. **M3 work, before a regulated customer. Not M5.**

### 8.4 The AI-specific clauses

These are the clauses that are not in a standard MSA, and they are where a
customer's counsel will spend the most time. Each with our position and the
reason.

| # | Clause | Our position | Why |
|---|---|---|---|
| **AI-1** | **No training on customer data.** We will not use customer data, customer video, customer evidence, operator dispositions, or derived data to train, fine-tune, improve or evaluate any model for any purpose, including for another customer | **Unconditional. Not negotiable. Ever.** | It is the clause a customer's most cautious lawyer will ask for, and the one that costs us nothing, because we do not train models at all (`00` §7: "We do not train foundation models; we use the best available models behind our own abstraction layer, and the abstraction is the asset"). **Refusing to give this clause is a signal that a company is not worth the conversation, and the reason to refuse is always the same: they want the data to improve their product, which is a different company** |
| **AI-2** | **Liability allocation for a decision the AI informed.** | Our liability is capped at fees paid, and the cap does not exclude our liability for our own negligence, wilful misconduct, or a breach of the invariants. The customer's decision, its staffing, its camera placement, its rule configuration and its operating policy are theirs | The honest version of this clause: **we are liable for the record being true and the decision being reproducible; we are not liable for whether the decision was correct for their site.** That is not a legal position we are conceding, it is the boundary of the product — and stating it in advance is better than litigating it |
| **AI-3** | **Human oversight.** The system is an assistant, not an autonomous agent, unless the customer raises the site to an autonomy level; a level change is an explicit, audited, reversible customer act with a blast-radius preview | "Anything with physical impact on the world stays approval-gated" (M3) and "sensitive and high-impact actions stay behind explicit policy and authorisation, permanently" (M7) | This is a product promise with tests behind it, and the contract clause should say so, with the tests named as the specification |
| **AI-4** | **The degradation contract.** A missing AI observation is never a negative observation; a rule consuming a Tier C signal must declare its degradation in advance; the system will never tell the operator that a model outage means an event did not occur | **Unconditional.** It is invariant 3, it is the strongest-tested thing in the product, and it is the promise that makes a customer able to sleep at night | If a customer's counsel reads §5.8's block on the degradation contract and the product does what it says, that is the whole sale |
| **AI-5** | **Audit rights.** The customer may verify the audit chain, every seal, and the attribution of any observation, **offline, without us and without our servers** | Unconditional, and it is the reason `securityctl audit verify-seals` needs no database | An audit right we have to service is a support ticket. An audit right the customer can exercise alone is a feature |
| **AI-6** | **Model, version and evaluation disclosure.** The customer is entitled to the AI disclosure package (§5), on the version they are running, and to the evaluation report for every profile in their build | Unconditional; the package ships in the archive for exactly this reason | A vendor who will not say which model it ran is a vendor the customer cannot risk |
| **AI-7** | **Model-bundle supply chain.** Bundles are signed, verified before installation, and never activated on install | The customer may pin a model version, and a site keeps its old bundle when the model changes, so a site's observations stay attributable to a version that still exists on disk | — |
| **AI-8** | **Sub-processor and model-provider change notice.** A change to the model a profile runs is notice plus a re-evaluation, and a change to a model's licence terms is notice with the right to remain on the prior version | This is the AI-2 risk with a clock on it: a provider changing terms or prices mid-term. See ``'s RISK on the model provider | — |

### 8.5 The clauses we will not concede

Named in advance, because a clause that is refused without a pre-agreeed reason
becomes a negotiation every time, and a negotiation every time becomes a
concession eventually. The reason column is the thing a founder can say in the
room without hesitating.

| We will not | Why |
|---|---|
| **Train on customer data, in any form, including for "improving the product"** | We are a local-first product whose entire thesis is provability. A clause that lets us train on a customer's video is a clause that says our provability claims apply to everyone except the one who bought the product. It is also the fastest route to losing a regulated customer and to an enterprise deal simultaneously |
| **Guarantee zero false negatives, or zero false positives** | Impossible, and a guarantee we could not keep. What we give is the evaluation report with its named gates, the known-failure-mode list, the disagreement rate, and the rule that a missing observation is never a negative. **A vendor who guarantees a model's accuracy has not evaluated it** — and a customer who accepts such a guarantee has bought a liability they cannot price, which is why they should not want it from us either |
| **Operate a hosted service, or phone home, or require a licence check at run time** | `12` §3.1. It makes us unusable at the site we are designed for, and it converts a security asset into a security liability |
| **Accept liability for the customer's physical security outcomes** | We are a layer above a stack the customer owns. We produce evidenced, replayable, auditable decisions; we do not hold the cameras, the locks, or the alarm path, and if we are down the site's own security works exactly as it did before we arrived (`12` §8.7) |
| **Indemnify a customer's regulatory penalty** | We can indemnify for our own breach of law and our own negligence. We cannot indemnify for a penalty levied on them for a deployment decision they made |
| **Accept a 24/7 response commitment before we staff one** | `12` §7.3: "We would rather decline a deal than sign one." A contractual SLI we have not staffed is a liability we have sold |
| **Accept a right to unilaterally change a model's behaviour** | Any model, prompt or profile change re-opens the evaluation (`13` §4.3). If a customer can be surprised by a behavioural change, AI-8's notice obligation is meaningless |
| **Accept an exclusive licence or a most-favoured-customer clause** | Two people cannot price exclusivity, and an MFC clause is a promise to match a price we do not have a mechanism to hold |
| **Accept indemnity for a third-party adapter written against our SDK** (before M4) | Until B8 is designed — §13 §10.3 — we do not know the blast radius of a malicious adapter, and indemnifying an unknown blast radius is not a commercial decision, it is a guess with a customer's money |

---

## 9. What this track does not do

- **It does not promise a customer that a regulator has approved us.** §1.2.
- **It does not start SOC 2 later than §2 says because we are busy.** The
  observation window does not compress. If the chain slips, the report slips,
  and the report is dated honestly rather than issued with a thin window.
- **It does not use a certificate as a substitute for a control.** Every item in
  `13` §9.1's matrix stands whether or not any certificate is ever issued. The
  certificates are evidence; the controls are the product.
- **It does not buy a certificate before the evidence, in the hope that the
  evidence follows.** That is the most common way a two-person company spends
  £30,000 and gets a report with nine exceptions and no remediation.
- **It does not make claims about a jurisdiction we have not asked about.** DR-5
  and §3.6's per-jurisdiction note.
- **It does not let an agent draft a clause.** The AI disclosure package is a
  *product* artefact and a machine may generate it from the registry, because a
  generated document that is generated from the code cannot drift from the code.
  A *legal* artefact is not a product artefact, and ``
  §4 puts legal and contractual decisions on the human-review list.
