# GTM and Sales

Owner: E1 (Sales is a permanent E1 role — `03-team-structure-and-operating-model.md`
§1.2 R-01, §1.3). Status: living. Last revised 2026-09-28.

Read `00-strategy-and-north-star.md` §3 and §4, `01-master-roadmap.md` §M1/§M2/§M3,
and `05-product-and-backlog.md` §3.7 and §5 first. This document turns those into
a motion. It does not restate what is built; `docs/status.md` does that.

**Marker convention, inherited from `08-metrics-and-analytics.md`:**

| Marker | Meaning |
|---|---|
| *(real)* | Read from code, a migration or a document in this repository. The file is named. |
| *(PROPOSED)* | A threshold, price, cycle length or target this document proposes. Never measured. **Does not appear in a customer conversation, in a contract, or in collateral until it has been measured** |
| *(ESTIMATE)* | A calculated figure resting on stated assumptions. Stated with the measurement that replaces it |
| *(NOT YET KNOWN)* | A number that does not exist yet. M1 or M2 measures it. Inventing it here makes every document that cites this one wrong in a way nobody would notice |

The rule from `00` §8 applies to us selling harder than it applies to our
competitors: **no collateral, no slide and no sentence in a customer call makes a
claim we have not measured or verified.** The buyers in this market are
professionally trained to detect exactly that, and they check.

---

## 1. The sales thesis

### 1.1 Why "provable, not clever" is the wedge

Three of the five segments we sell to are in the business of *not* believing
software vendors. A security director's professional reputation depends on being
able to say, months later, why a decision was made and to be able to prove it.
An AI feature that cannot do that is a liability in their building, regardless of
its accuracy.

So the wedge is not accuracy, and it is not price. It is:

> Every decision this system made is reproducible from stored facts, with the
> model switched off, at the site, months later — and the record of what happened
> is hash-chained and sealed so it cannot have been rewritten.

That claim is **checkable in four minutes by a sceptic who has never heard of us**
(`securityctl incident replay <id>`, then `securityctl audit verify-seals` with
no database, then a foreign key refused). It is also the claim a cheap
investigation product cannot make, because making it costs the whole engineering
effort: the rule engine with no shell, SQL, network, dynamic code or regex
(*real* — `docs/threat-model.md` C14); the append-only observation store
(C18/C19); the replay path that opens no model client, no bundle path and no
clock (*real* — `05` §1.1); the trigger-computed hash chain (C6/C7).

A cheaper investigation product loses to free, because the VMS already does free
investigation. A provable one does not, because the VMS cannot do provable at any
price. That is the whole thesis and it is one sentence long on purpose.

### 1.2 The three buyer questions are the spine of every call

`00` §4 names the three questions. Structurally:

```
   call opens  →  Q1  "what did the AI decide, and why?"
   middle      →  Q2  "what happens when the AI is down?"
   close       →  Q3  "can I prove the system did what it says it did?"
```

Every discovery call, demo, written answer, case study, objection response and
collateral section maps to at least one of the three. **A customer conversation
that produces a decision without all three answered in writing is a conversation
that has not happened.** `05` §7.4 already requires E1 to read that customer's
`docs/status.md` "What is not built" section aloud before every customer
conversation, because a demo that implies the Milestone endpoint is verified, or
that the Python VLM runtime exists, is *a commitment made by accident* and it is
the fastest way to lose a reference.

### 1.3 The rule that binds the whole motion: we never demo a capability that has not passed its gates

> **A capability that has not passed its named gates does not appear in a demo,
> a trial, a trial environment, a proposal, a case study or a sentence of
> marketing collateral. If a gate has not been passed, the capability is not in
> the build.**

Three real mechanisms already enforce this, and a fourth is a backlog item:

| Gate | Where it is enforced *(real)* | What a failure does |
|---|---|---|
| A VLM profile in shadow cannot affect authoritative state | `internal/observations`: `TestShadowProfilesCannotAffectAuthoritativeState` | The profile is marked shadow everywhere it appears, including the console. It cannot be shown as producing an outcome |
| Promotion requires each named gate individually, never a single boolean | `internal/evaluation`: `TestEveryFailingGateIsNamed`; gates in `evaluation.DefaultGates()` — FPR ≤ 0.20, recall ≥ 0.70 on true positives, disagreement ≤ 0.25, median latency ≤ 8000 ms, ≥ 30 labelled cases for production, ≥ 10 for shadow | A profile without a passing, dated report is not in the release, per `07-quality-engineering.md` §9.1 line 12 |
| Storing a bundle does not activate it; activation requires a reviewed diff digest | `internal/bundles`: `TestStoreDoesNotActivate`, `TestActivationRequiresAReviewedDiffDigest` | Nothing a customer sees can change behaviour without a human reviewing a simulate diff |
| **The demo itself refuses an ungated capability** | **Not yet built. M1 backlog: a demo/preview mode that renders shadow and degraded state explicitly, and a console affordance that marks a profile as shadow wherever it is shown** (`05` §1.7 names the UI equivalent of the store test as outstanding) | Until it exists, the discipline is manual, and a manual discipline is a discipline that fails under pressure |

**The commercial consequence, stated plainly:** in M1 we have no AI capability
that is allowed to influence anything, and the only thing we can honestly sell is
the deterministic spine plus a shadow-mode AI plane that is measured and
gated. That is not a weakness in the pitch, it is the pitch: *"I am showing you a
system that currently refuses to let the model do anything, and every claim it
makes about what the model did is a stored fact you can re-verify. Now imagine
what happens when we let it act."* A buyer who does not buy that is a buyer who
wants a black box, and we lose to a cheaper black box, which is a fine outcome.

### 1.4 What we refuse to sell

- A **custom single-customer product** (`00` §3 anti-segments). The one exception
  path is `03` §6.2 and it requires E1's written decision, a timebox, and a
  statement to the customer that it is not in the product.
- **Anything that weakens an invariant**, whatever the customer signs
  (`03` §7.3: no customer escalation, no security incident, no investor request
  and no deadline bypasses the invariant list in `AGENTS.md`).
- **A 24/7 response commitment** until the rota exists (`12-release-sre-and-support.md`
  §7.3: "we would rather decline a deal than sign one").
- **A camera-count-priced product** (`17-pricing-and-packaging.md` §1).
- **Cloud video.** Permanently, for every segment, without exception.

---

## 2. Segment playbooks

Segments and order are `00` §3's. **Each rung becomes reachable only when the
previous rung has produced a reference the next one can quote.** A deal that skips
a rung is a deal we cannot close, and the reason is in the playbook.

### 2.1 Segment 1 — Single-site commercial operators (warehouses, clinics, guarded retail)

Entry milestone: **M2** (paid pilot). This is the rung we recruit in M1, as
design partners, at no charge.

| | |
|---|---|
| **Buyer persona** | The person who is on site at 03:00 when something beeps. Not the person who owns the budget |
| **Titles that buy** | Security manager, facilities manager, operations manager, clinic administrator, warehouse shift supervisor with a budget line. At the smallest end, the owner-operator who signs their own invoices |
| **Trigger events** | A missed incident, not a productivity problem — a theft they did not see, a break-in their response arrived for 20 minutes after the fact, or a claim their insurer or their customer refused because they could not evidence what happened. Second trigger: alarm fatigue complaint, or a night shift they can no longer staff. Third trigger: a growth or a site opening that makes the current arrangement visibly unsustainable |
| **Discovery questions** (ask before showing anything) | 1. "Take me through last night. What came in, in what order, and what did you do with each one?" 2. "The last time you had to prove what happened at a particular time, what did you actually do?" 3. "What would you refuse to let a system do on its own?" 4. "If this stopped working entirely right now, what would you do in the first ten minutes?" 5. "Who else has to say yes, and what do they need to see?" (`05` §5.5's guide, abridged; questions 5–8 of that guide are asked after, never before) |
| **Proof asset required** | The M1 exit-gate pack in their language: a real incident, a real operator, a timed investigation, the replay with the model off, the sealed checkpoint verified offline, and a foreign key refused. `03` §6.3 makes this a **per-milestone, 60-minute, reviewed *with* them before it goes to a third party**. This is the entire M1 sales motion |
| **Cycle length** *(PROPOSED)* | First call → decision: **3–6 weeks.** Paid pilot → production use: **2–4 weeks.** The reason it is short is that the decision unit is one person and the money is small enough that there is no committee |
| **Decision unit** | One named person who can say yes with a purchase order. There is no committee. If we have found a committee, we have found the wrong entry point |
| **Disqualifier** | The site has a single camera type and no door controller or second signal source. `docs/threat-model.md` R2: the built-in bundle's production rule requires two independent signals, so such a site gets `UNVERIFIED` incidents and no corroborated ones — and the honest thing is to tell them so in the first call, not the eighth. Second disqualifier: the buyer wants video in a cloud we do not have. Third: they are a hardware or VMS vendor (`00` §3) |

**Top 6 objections, segment 1**

| # | Objection | Response |
|---|---|---|
| 1 | **"Our VMS already does this."** | It does, and it does it in the way that produced the missed incident they are asking me about. Ask one question: *"show me what your VMS does when I ask it why it raised that alarm."* If they can show it, we have a fast conversation about something else; if they cannot — and they usually cannot, because the answer lives in a vendor's logs — that is the product. Second, honest: the VMS is a *better* recorder than we are and we do not compete with it (`00` §7). We compete with the three shifts a day of triage |
| 2 | **"We cannot send video to the cloud."** | Correct, and we never ask. Everything runs on hardware you already own. There is a capability named for exactly this: with the AI plane entirely absent, the deterministic core keeps ingesting, verifying, raising incidents, storing evidence and sealing checkpoints (`01` M1 mission, `00` §4 question 2). We will demo it by stopping the model mid-call |
| 3 | **"Who is liable if the AI misses something?"** | Today: the answer is in the contract, not the product, and it is deliberately a *capability* answer rather than a guarantee. We do not sell detection guarantees. We sell (a) an append-only hash-chained ledger of every decision, (b) a replay that reproduces any past decision from stored facts, and (c) the product's own failure record: the fraction of decisions that ran on a degraded or absent AI signal is a *measured, reported* number (A12 in the threat model is an asset, not a metric). A liability clause that promises detection accuracy is a promise about a number we have refused to publish. See `17` §9 for the clause set |
| 4 | **"Our security director won't allow it."** | At this size there may not be one, and the person who is refusing may be describing a policy he has not read. Ask to see the policy. If there is a real one, the answer is the same as for segment 5: the site keeps operating with the AI plane absent, nothing is trained on their data, the data never leaves, and every action is in the ledger. Offer to run the read-only evaluation before anything is installed |
| 5 | **"We'd rather hire a person than pay for software."** | Sometimes correct. The comparison is not person-versus-software, it is *hours a night you have to staff* versus a licence. The measurable version: I will instrument your existing incident history and show you what fraction of it a person looked at. If that fraction is small enough that a person is not the constraint, I will tell you not to buy |
| 6 | **"Is this on a roadmap with a date? When can it do X?"** | `05` §3.7 governs this. I will not give you a date for something that is not in the backlog, and if I do give you a date it is a commitment with a named gate behind it, in writing, with what happens if we miss it. What I will tell you is what is cut first if we slip — and the answer is auto-resolution, which is also the part I would worry about buying early |

### 2.2 Segment 2 — In-house security teams at 5–50 sites

Entry milestone: **M3–M4**. Reached by expansion of segment 1, not by cold
outreach.

| | |
|---|---|
| **Buyer persona** | A corporate security manager or regional facilities director responsible for alarm handling across sites, with a security manager at each site doing the triage and a head-office IT or security function that owns the network |
| **Titles that buy** | Head of Security, Regional Security Manager, Facilities Director, Director of Operations, Head of IT (as the technical gatekeeper, rarely as the buyer) |
| **Trigger events** | A missed incident *with a corporate consequence* — a claim, a regulator, a customer audit, or an internal incident review. Second: a new site opening, or an acquisition adding sites, which is when "the process is different at every site" becomes a stated problem. Third: an insurance or audit questionnaire asking how alarm handling is evidenced. Fourth: a regional or corporate security operations centre being considered |
| **Discovery questions** | 1. "When you have to prove what happened at one site, who do you call and what do they send you?" 2. "How many sites can you onboard without a person per site going with them?" 3. "Show me the last three alarm-handling policy reviews you have had to pass, and what they asked." 4. "What happens to a site when the WAN link goes down?" 5. "Who signs off a new site going live, and how long does that take?" 6. "What does a site tell you when it has had a bad night?" |
| **Proof asset required** | Two things the single-site pack cannot give: (a) **the same VMS adapter at a second site with a zero non-adapter diff**, which is `01` M4's exit gate and the only honest proof that we are a layer and not a product for one VMS; (b) **a site that keeps operating with the central plane pulled mid-incident**, which is `01` M5's exit gate. A multi-site buyer who has only seen single-site evidence is buying a tool, not a layer |
| **Cycle length** *(PROPOSED)* | First call → production at 2+ sites: **3–6 months.** A 50-site rollout is 6–12 months and is sold as a programme, not a deal |
| **Decision unit** | A buying group: economic buyer (Head of Security or Facilities), technical evaluator (IT/security engineering — usually the person who says no), and a procurement or legal reviewer who appears late. The technical evaluator has veto. **We sell to the evaluator first, in a way that lets them say yes on the merits** |
| **Disqualifier** | The organisation wants us to become the system of record for video. A request for retention management, firmware, camera discovery or a recording UI is a request to become a VMS (`00` §3, `00` §7, `05` §1.2's disqualifier 2). Second: they require video in *our* cloud, which is a permanent no. Third: a single-VMS-only requirement with a "we will never buy a second one" written policy — that is a segment-1 customer with a procurement department, and we sell it the segment-1 way |

**Top 6 objections, segment 2**

| # | Objection | Response |
|---|---|---|
| 1 | **"Our security director won't allow it."** | The most common blocking objection and it is legitimate, so we do not argue with it. Three things make the conversation tractable: (a) the product is *designed* so the site keeps working with the AI plane absent, so the worst case for the director is a site that behaves like today, not a site that goes dark; (b) every rule that consumes an AI signal declares its degraded behaviour in advance, in the rule itself, before it ever runs — so the director can read what happens when the model is unreachable rather than finding out; (c) we will run a **read-only evaluation on their own incident history with nothing installed at their site**, which is a four-week motion with no footprint and no risk, and it produces the number their director needs |
| 2 | **"Who is liable if the AI misses something?"** | See segment 1 objection 3, plus the enterprise version: audit rights, a disclosure obligation, and a contractual severity schedule with credits (`12` §7.2, `17` §9). The sentence I use: *"We are not in the business of guaranteeing detection. We are in the business of guaranteeing that when we did or did not raise something, there is a record you can produce to a regulator, and it cannot have been edited."* Every serious buyer relaxes at that sentence, because it is the guarantee they can actually get |
| 3 | **"This is another AI vendor."** | We are the layer above the stack you already own, and we are explicit that the model is not the asset — the abstraction is. `00` §7: we do not train foundation models, and a model bundle is untrusted input verified before execution, same as a release archive. Ask them what their current AI vendor's answer is to *"can I reproduce a decision from six months ago with your system offline?"* If it has one, they are a competitor conversation we are glad to have |
| 4 | **"We would need to change how our sites work."** | We do not want that, and the pilot is designed to prove we do not need it. The measurement design is within-subject and alternating — blocks of your existing workflow and blocks of ours, per operator, per incident class, counterbalanced (`08` §4.1) — so the trial produces a comparison rather than a migration. If after the trial the site's operator prefers their own workflow for a class, that class goes back to their workflow and we say so in the report |
| 5 | **"What is the total cost of ownership, including the hardware?"** | Honest answer, from `12` §9: application host, Postgres, evidence storage 1–8 TB depending on incident volume, and — optionally — a GPU box. **The GPU is optional and its absence degrades verification quality, it does not disable the product.** The variable that surprises people is *evidence storage*, and it scales with **incidents per day and the rule pack**, not with cameras (`12` §9.1: ~2.7 MB/incident, ~73 GB at 30 incidents/day with 30-day retention, ~730 GB at 300). We do a sizing session before the contract, and we bring the rule pack, not the camera count, to that session |
| 6 | **"How do we exit?"** | The full export path: canonical events, evidence bytes with their hashes, the audit ledger, and the seal file. Your site is running software on hardware you own with a signed offline installer; there is no data held by us that you cannot take, and `17` §7's anti-dark-pattern rules make "export everything" a contractual obligation rather than a support request. Ask for the export as part of evaluation and we will do it, on a fresh install, with no special access |

### 2.3 Segment 3 — VMS OEMs and integrators (channel)

Entry milestone: **M4** (first conversations, 2028 Q2), realistically first
revenue **M5–M6**.

| | |
|---|---|
| **Buyer persona** | A product manager or solutions architect at a VMS OEM who owns the roadmap for "what do our customers ask for next", and a director of partner/channel operations who owns whether a third party is allowed inside their product |
| **Titles that buy** | Product Manager (AI/Analytics), Solutions Architect, Head of Technology Partnerships, Alliances Manager, and — for regional integrators — the owner or a services director |
| **Trigger events** | Their customers asking for AI and them not having an answer. Losing deals to a competitor that has an answer. A strategic push into a vertical. An integrator losing a bid because the customer asked for something they could not supply |
| **Discovery questions** | 1. "What are your top three things customers ask for that you cannot currently say yes to?" 2. "Who owns the roadmap item if a third party built it, and who owns the support call at 02:00?" 3. "What would it take for your field engineers to learn a new integration, and who pays for that training?" 4. "What do you need to be able to say to a customer about where their video goes?" 5. "What would make you say no to a partnership like this in one line?" |
| **Proof asset required** | The published **Adapter SDK v1 with a certification suite that has been run by someone outside the company** (`01` M4 exit gate). Not a slide, not a partnership promise: a public artefact a third party has already passed. An OEM conversation that starts before this exists starts with us asking for patience, and an OEM does not have a category for that |
| **Cycle length** *(PROPOSED)* | First conversation → signed pilot: **9–15 months.** Signed pilot → shipping product: **6–12 months.** This is longer than every other segment combined and it is the reason `17` §4 and §7 of this document are written now rather than in 2028 |
| **Decision unit** | A three-person committee: product owner (sponsor), partner operations (contract and legal), and engineering (can we support this). Any one of them can stop it. The partner-operations person is the one who stops it by asking a question about who owns the 02:00 call |
| **Disqualifier** | They want exclusivity (it forecloses segments 1, 2 and 4, which is most of the company). They want us to fork into their platform (permanent anti-segment, `00` §7 — a fork ends with us maintaining N incompatible cores). They want a white-label arrangement that removes attribution entirely *and* they will carry the audit liability. They want us to hire an integration team for their platform specifically |

**Top 6 objections, segment 3**

| # | Objection | Response |
|---|---|---|
| 1 | **"We already ship AI features."** | Possibly, and respectfully: most of what incumbent VMS AI features *are* is a rule engine and a heat map, and some of them are genuinely good at their job. The gap is not the feature, it is the three questions in `00` §4 — explainability of a past decision with attribution, defined behaviour when the model is unavailable, and a tamper-evident record. The test we offer is to run our replay and audit verification against *their* system's last AI-generated decision. If they can do it, they do not need us and I will say so |
| 2 | **"Who is liable if the AI misses something?"** | The question that kills a partnership and the one to answer in the first meeting. Our answer: liability for detection sits with the party operating the security system — the customer — and it does not move because a model was involved. What a partner cares about is *not being the party that cannot answer an audit*, and that is exactly what we provide. This is also why our contract structure for OEMs caps our exposure and requires the OEM to preserve the ledger (`17` §4) |
| 3 | **"We cannot send video to the cloud."** | We never ask them to. For an OEM this is stronger than it sounds: the partner's own customers include air-gapped sites, and a cloud-required product is disqualified in exactly the accounts an OEM most wants. Local-first is a channel asset, not a constraint |
| 4 | **"Our customers won't install a second thing."** | Agreed, and that is why the integration is designed to be inside their deployment rather than beside it — a service on their hardware, a plugin, a container on the appliance. It is also why the Adapter SDK ships before the first partner conversation (§7.4): the answer to "will our engineers be able to support this" is a public SDK and a certification suite, not a promise |
| 5 | **"What is your roadmap and where does this leave us?"** | Here is our roadmap, including the parts where you are not in it, and here are the dates with the gates behind them. What we will not do is take a date from you that we cannot keep. If your annual planning cycle requires a commitment 14 months out, say so now, and we will decide together whether to wait — because a partner who is promised a date we miss is a partner who files a claim against us in front of their customers |
| 6 | **"Can you be white-label with no trace of you?"** | Partial. White-label in deployment, documentation and console: yes, and I will write that into the agreement. White-label in provenance — the audit record showing which vendor produced which decision: no, and this is not negotiable, because an unattributable AI decision in someone else's building is a liability for them. A partner who requires that is a partner asking us to help them build something their own customers will not be able to audit |

### 2.4 Segment 4 — MSSP / SOC operators serving physical security

Entry milestone: **M5–M6**. They are a channel as much as a customer, and the
multi-tenant story is the reason.

| | |
|---|---|
| **Buyer persona** | A monitoring provider's operations manager who owns throughput and a service manager who owns the SLA. The person who loses the night is the escalation analyst at 04:00 |
| **Titles that buy** | Head of SOC Operations, Service Delivery Manager, Director of Operations, Head of Physical Security Services |
| **Trigger events** | Capacity: a new contract with more sites than analysts. Utilisation: analysts spending the night on nuisance alarms. Margin pressure on a flat-rate per-site contract. A customer asking them for evidence-grade audit trails, which their current tooling does not produce |
| **Discovery questions** | 1. "How many sites can one analyst carry, and what happens when you add ten?" 2. "What is your current false-positive rate and what does a shift look like when it doubles?" 3. "How do you evidence to your customer that a response happened?" 4. "How many customers' sites are air-gapped?" 5. "What does your analyst's screen look like at 03:00, and what is on it besides the alert?" |
| **Proof asset required** | The exception-queue surface with a **before/after on their own incident history**, and `missed_incident_rate` and `evidence_completeness` beside the reduction (`08` §4.5: the M2 result always carries four companions unprompted). For an MSSP the second proof asset is a *tenancy* artefact: two customers' data, isolated, with RLS, and the cross-tenant refusal demonstrated live. `01` M5's organisation-wide Security Graph — the same credential denied at Site A, denied at Site B, attempted at Site C is one incident — is the single feature MSSPs ask about first, and it is the one they cannot build themselves |
| **Cycle length** *(PROPOSED)* | First call → paid pilot at one client site: **4–8 months.** To a multi-client rollout: **6–12 months** and it is a partner-shaped procurement process with their own security review of us as a sub-processor |
| **Decision unit** | The operations owner (economic) plus a security/compliance reviewer (veto, because they become a sub-processor to their clients) plus commercial/legal. Treat the compliance reviewer as an ally early: give them the threat model and the sub-processor pack before they ask |
| **Disqualifier** | They want a managed service from us (we sell software; `12` §7.3's rota is our own support, not their customers'). They want us to run the SOC for their sites, which makes us staff we are not. They want raw video streamed to their NOC — this is their *own* infrastructure question, and it is answerable, but it is a different product conversation and it is where this segment goes wrong |

**Top 6 objections, segment 4**

| # | Objection | Response |
|---|---|---|
| 1 | **"Our VMS partners already offer AI."** | Your VMS partner sells to you, not to your clients, and their AI runs inside their platform. What you need is the layer that reads across every VMS your clients own — the same credential denied at three of their sites is one incident, and no single VMS can see that. The honest test: pick the two clients with different VMS vendors and ask whether their tooling can correlate across both. It cannot |
| 2 | **"We cannot send video to the cloud."** | Some of your clients cannot either, and that is the strongest argument for us: a product that requires our cloud disqualifies you in the accounts you most want. Evidence lives at the site; what reaches you is the incident, the verification state, the evidence references and the disposition — and each of those resolves at the client's site without us |
| 3 | **"Who is liable if the AI misses something?"** | For an MSSP this is the sharpest version of the question, and the answer is contractual and boring: the sub-processor relationship is defined, the security review pack is complete, and the data we process is the incident record, not the video. The thing they are really asking is whether you can produce records to *your* clients and to their auditors. That is the sealed checkpoint and the offline `verify-seals`, run on their side, with no database |
| 4 | **"Our security lead won't allow a sub-processor we can't audit."** | Then audit us. The threat model is a public document with 35 controls each mapped to the test that fails when the control is removed, four accepted risks stated plainly, and a list of what is *not* modelled. `07` §11.4's eight open defects are published rather than hidden, because a security reviewer who finds them themselves has learned we are not managing their expectations. Send the pack before the call. Never wait to be asked |
| 5 | **"We would need to resell this to our clients."** | That is the right shape, and it is why the OEM terms in `17` §4 exist. What we will not do is let you resell a claim we cannot support: your clients' audit rights are your contractual relationship with them, and what we owe you is the artefact. If that division is uncomfortable, it is the same discomfort every platform vendor has, and it is better to find out at pilot than at renewal |
| 6 | **"What does this do to our analyst headcount?"** | It should reduce nuisance load, not remove analysts, and I will not sell headcount reduction. The product's destination is an exception queue, not an empty desk, and if a customer routes back to their old tooling because we made their surface worse, we have failed (`01` M7's reduced-scope note). Sell them *hours per incident* on supported classes, measured against their own baseline, and let the staffing decision be theirs |

### 2.5 Segment 5 — Regulated verticals (healthcare, critical infrastructure, government)

Entry milestone: **M5–M7**, entered last, on references from segments 1–4.

| | |
|---|---|
| **Buyer persona** | A compliance or patient-safety officer who owns the evidence obligation, plus a security architect or CISO who owns whether the thing is allowed on the network. In healthcare these are frequently two people who do not report to each other, and the compliance officer is usually the one who wins |
| **Titles that buy** | Compliance Officer, Patient Safety Officer, Head of Clinical/Physical Security, CISO, Security Architect, Government Facilities or Public Safety programme manager |
| **Trigger events** | A survey, inspection, accreditation visit, or an internal audit finding that requires alarm-handling and response to be evidenced. A mandated safety or safeguarding standard with a named evidence requirement. A post-incident review, litigation discovery, or an ombudsman case. A public-sector framework requirement |
| **Discovery questions** | 1. "Who signs off that your alarm handling is adequate, and what do they ask for?" 2. "What evidence must you be able to produce about a specific event, and how far back?" 3. "Where does that data physically sit today, and who has ever asked you to move it?" 4. "What is your change-control process for anything that touches the security system?" 5. "What is your incident reporting obligation, and who owns the clock on it?" 6. "Have you ever been asked to prove a decision your own vendor made?" |
| **Proof asset required** | `17` §9's procurement pack in full: security questionnaire responses, DPA, insurance certificates, sub-processor list, export-control/sanctions statement, and the AI-specific clauses — liability for a missed detection, data residency, no-training-on-customer-data, and audit rights. Plus the **evidence completeness measurement** on their own incident history, and a signed evidence-pack walkthrough with their own compliance officer in the room. This is the only segment where the demo is a supporting artefact and the document review is the event |
| **Cycle length** *(PROPOSED)* | First call → production: **9–18 months.** Composition: 1–3 months procurement and security review, 3–6 months pilot, 2–6 months change control and validation, and a budget cycle that is not ours to accelerate |
| **Decision unit** | A committee of four or five: budget holder, compliance officer (veto on evidence), security architect (veto on network), procurement, and legal. The compliance officer's veto is the one to win first, and they are won with documents, not with a demo |
| **Disqualifier** | They require a named accuracy or detection guarantee (a promise about a number we refuse to publish — see `08` §3.2's list of numbers this roadmap refuses to state). They require us to store data in a region we do not operate in *and* they will not accept data residency at the site. They require a certification we do not hold with a date we cannot control. They require video in a cloud |

**Top 6 objections, segment 5**

| # | Objection | Response |
|---|---|---|
| 1 | **"Who is liable if the AI misses something?"** | In this segment this is a legal question before it is a technical one, and I will not answer it in a sales conversation. Here is our position in writing (`17` §9): we do not warrant detection; we warrant that the system does what its documentation says it does, that the record of what it decided is complete and tamper-evident, that we disclose our own errors under a named policy and timeframe (`12` §10), and that we give you audit rights over both. Your counsel and ours will negotiate that, and it should take weeks |
| 2 | **"Our security director won't allow it."** | Send the security architect the threat model and the sub-processor pack before the second call. The specific blockers in regulated environments are almost always: (a) an AI system on the security network with no defined degraded behaviour — answered by `if_ai_unavailable` being declared in the rule, in advance, and by the site working with the plane absent; (b) an unmodeled data flow — answered by an architecture that has no outbound data flow to us; (c) an untested claim — answered by 35 controls each mapped to a failing test. Invite them to read the test names. Security architects respect a supplier who shows them the tests |
| 3 | **"We cannot send video to the cloud."** | Usually stated as an assumption rather than a requirement, which means there is room in the conversation. Evidence stays at the site, on hardware you own; we do not operate a video cloud; the AI runtime runs on a GPU on your site; and if the GPU is absent the product still runs, with reduced verification quality and every degradation recorded. The only thing that ever leaves the site in a connected deployment is telemetry counters, and the product works with those disabled |
| 4 | **"We need this for our accreditation / inspection."** | Then the acceptance criteria are written before the pilot and agreed with the inspector's expectations, and we build the trial around producing that evidence. Be careful here and be honest: **we do not know your regulator's position on AI in security monitoring and we will not guess at one.** What we can do is make every decision attributable, replayable, offline-verifiable and sealed, which is the substrate any such position is built on. We will also work with your counsel on how you describe our role in your own documentation |
| 5 | **"How long until you have the certifications we need?"** | We do not have them yet and we will not forecast a certification date. What we do have, today, is a threat model, a control-to-test map, a signed offline release path, a privacy and retention design, and a customer data-handling one-pager. If your framework requires a specific certification to permit deployment, that is a legitimate disqualifier today and I would rather know it in the first call than in the fourth month |
| 6 | **"Show me a regulated customer running this."** | At M5, segment 2 and segment 4 references. Until then: "we do not have one yet, here is what we have instead — a production reference, a full threat model, our own published defect list, and a site-health record at each reference that we will show you." Never imply a regulated customer exists because a prospect in a regulated vertical expressed interest. That is the exact failure `00` §8 was written to prevent |

---

## 3. The M1 design-partner motion

### 3.1 What we are recruiting, and what the deal actually is

`01` §M1 commercial state: **up to three, at no charge, in exchange for a named
reference, a measured before/after investigation time, and access to anonymised
incident data for evaluation. No public launch, no pricing, no self-serve.**

So the deal is not free work. It is an exchange with four consideration items, and
each one has to be *delivered*, because the exchange is worthless without it.

| What they give | What it is worth to us | What they get | What they must understand |
|---|---|---|---|
| **A named reference** — a written, quotable statement, agreed in wording, plus willingness to take a prospect call | Everything after M1. `00` §3: each rung becomes reachable only when the previous one has produced a reference the next can quote | A production system, an engineer who answers the phone, and influence on the roadmap (`03` §6.2's exception path is the *only* route to that, and it is timeboxed) | That the reference is not a marketing approval right. The wording is agreed, the fact is not negotiable, and we do not let them veto a fact because it is unflattering — we do not let ourselves hide one either |
| **A measured before/after investigation time** | The M1 and M2 exit gates. `01` §M2: "measured per operator against their own VMS-only baseline" | The number itself, produced by their operator, in their units, on their history | That we will report a result that shows no improvement, or a mixed one, in the same document |
| **Anonymised incident data for evaluation** | The labelled dataset every VLM profile's gates are computed against | Faster fixes, and the profile's own accuracy improving at their site | Precisely what "anonymised" means, in writing, before any data moves: the sanitiser at `internal/fixture` is the single choke point and it is tested (`docs/threat-model.md` C20) — secrets, identifiers, hosts and opaque strings |
| **Site access, an operator, and patience** | Nothing. This is the cost side | — | That this is a build-phase product and `docs/status.md`'s "What is not built, and why" applies to them as much as to us |

### 3.2 Recruiting three

| Step | Method | Why this way |
|---|---|---|
| **Source** | 5 structured interviews a month from M1 (`05` §5.1), each with a person who has performed the job. Design partners are recruited *from the interviews*, not from a cold list. `05` §5.4's three-similar-requests rule applies: a feature enters the backlog from a conversation only on the third independent similar request, and a **partner is recruited only if they are the third site saying the same thing** | A single enthusiastic contact is a sample size of one with a budget. Three independent sites with the same problem is a segment, and the whole product is built on the difference |
| **Qualify on the incident history, not the enthusiasm** | Can they produce 90 days of incident records with dispositions? Do they have an operator willing to be measured for two quarters? | If the answer to either is no, the before/after number cannot exist and the exchange collapses. Better to find out in the first call |
| **Recruit the operator, not the buyer** | The design partner must name a person who does the work and agrees to be in the `05` §5.2 shift shadowing (twice a quarter, a full shift, at the site, in person) | The operator is the reference, the measurement subject, and the person whose testimony in the case study is the whole point. A partner who sends only a manager is a logo, not a design partner |
| **Be honest at recruitment about what is not built** | Read `docs/status.md` §"What is not built" aloud: the Milestone endpoint surface is unverified; the Python VLM runtime is not written; `deploy/` is absent; `install`/`upgrade`/`rollback`/`backup`/`restore` are skeletons that refuse with a named reason | A partner who discovers these in month three is a churned reference and a bad case study. A partner who hears them in call one becomes the kind of customer who tells other people the truth about us, which is worth more than a logo |
| **Say no to the fourth** | There is a hard cap of three | Three is the number the exchange can be delivered to (`03` §6.3's cadence is a real-time commitment: weekly update, bi-weekly incident review, monthly measurement). Four would mean one partner gets less than the three need, and the ones already in get it first |

### 3.3 Pilot agreement contents

A short document, in plain language, eleven sections. The template lives at
`docs/roadmap/templates/design-partner-agreement.md` and is created before the
first signature.

1. **What this is.** A no-charge evaluation of pre-GA software at one site, with
   named consideration items. Not a sale, not a purchase, no obligation either way.
2. **What is in the build and what is not**, by version, quoted from
   `docs/status.md` at signature, with the date. **The version is pinned in the
   agreement.** A partner running a build from a moving target cannot be a
   reference, because the thing referenced changes.
3. **The reference obligation.** What "a named reference" means: a written
   statement, wording agreed by both, and availability for prospect calls. What
   it does not mean: approval rights, a veto on facts, or anything about pricing.
4. **The measurement obligation and its limits.** The before/after investigation
   time will be measured per operator, per incident class, on the design in
   `08` §4, with the minimum meaningful effect agreed in writing **before** the
   trial opens, and the sample size computed from the M1 baseline variance.
   *The trial extends if the site's event rate cannot supply the sample inside
   the window. The effect threshold does not move* (`08` §4.4 step 4).
5. **The 14-day shadow requirement.** A production VLM profile runs in shadow
   mode — marked, unable to affect authoritative state, persisted with full
   attribution — for **14 consecutive days** at the design-partner site, with the
   evaluation report attached. This is `01` M1 exit gate 4 and it is not
   shortened. If the clock is behind, **the clock restarts** (`03` §7.3).
6. **Data handling.** What data moves (incident records, sanitised), what does
   not (video, credentials, personal identifiers), what the sanitiser does
   (`internal/fixture`, C20), retention of raw evaluation data, the right to
   request deletion, and the legal-hold interaction. The data-handling one-pager
   is an attachment and is not modified without telling them.
7. **The AI question, in writing.** The model is not required for the site to
   operate. Rules consuming an AI signal declare `if_ai_unavailable`. Every AI
   output is an attributable record with model version, prompt version, profile
   version and input hash. **No customer data trains any model**, and that is a
   contractual sentence, not a documentation note.
8. **Support, honestly.** `12` §7.3's paragraph verbatim: business hours 09:00–18:00
   weekdays in [timezone], out of hours a Severity 1 issue reaches one named
   engineer by phone at any hour, anything else waits for the next working
   morning, and we will say so when the report is filed rather than after we have
   looked at it. Named engineer and named backup, by name.
9. **Licence during the pilot.** Perpetual, per site, and the product will not be
   disabled for non-payment — stated before `12` §3.3's ADR exists, because that
   sentence is worth more to a design partner than whatever enforcement we might
   ship (`12` §3.3).
10. **What ends it.** Termination on 30 days' notice by either party, immediate on
    breach of the data-handling section, and immediate by us if continuing would
    put the partner's security at risk. On exit: full data export, uninstall
    instructions, a written statement of what the platform did and did not do at
    their site over the term, and **no claim on the reference**. The partner may
    stop being a reference at any time and say why, and we do not contest it.
11. **No liability, no publicity, no exclusivity.** Asymmetric liability: we cap
    our liability, they have none to give. No publicity without written consent
    per artefact. No exclusivity in either direction.

### 3.4 The 14-day shadow requirement, stated as a gate and not a phase

`01` M1 gate 4: *"A production VLM profile has passed its promotion gates on a
real GPU, in shadow mode, for at least 14 consecutive days at a design-partner
site, with the evaluation report attached."*

Four things this means operationally, because "14 days" is the part people
misread as "14 days of effort":

- **Consecutive calendar days at one site**, on the site's own hardware, with the
  site's real event mix and its real night-time conditions. Not a harness run.
  Not 14 days of batches.
- **A gap restarts it.** A site restart, a bundle change, a profile change, or
  two consecutive days below the minimum labelled case count resets the clock.
- **The evidence is the report**, dated, with N, the per-class distribution, the
  gates each named individually, and the Wilson interval on the rates
  (`08` §5.5: a rate quoted without its N is the exact error the section exists to
  prevent). The report says `Promotable`, and never says who was promoted —
  promotion is a human, named, dated decision (`05` §3.4).
- **It cannot be assembled from other runs.** `03` §7.3 names the temptation
  explicitly — logging harness observations as if they were live — and the
  answer is that the clock restarts, because faking this makes gate 4 a claim,
  and a claim about provability is the one failure this company cannot survive.

### 3.5 Data handling during M1

| Question | Answer | Where it is enforced |
|---|---|---|
| What leaves the site? | Nothing but optional telemetry counters, and the product works with those disabled. For a design partner, default is **no telemetry at all** | `12` §3.1; `08` §2.1's evidence-disk and backlog readings are read from the site's own `/v1/ops/*` |
| What comes to us for evaluation? | Sanitised incident records: the canonical event, the verification result, the AI observation, the disposition, and evidence **hashes** | `internal/fixture`'s sanitiser is the single choke point between a live source and a committed file, tested by C20 |
| What never comes to us? | Video bytes, credentials, personal identifiers, badge data, and anything that could re-identify a person. `01` M5's `schemalaws.TestNoEntityAliasTableInM1` (C35) is why there is no person table to leak in M1 | C35 |
| Who may see raw evaluation data? | Both founders, named in the agreement. Not an agent, not a third party, not a future employee without a written amendment to the agreement | Agreement §6 |
| Retention | Raw evaluation data deleted 24 months after the pilot ends, on a stated date, and the customer may accelerate it (`05` §7.3's retention table: research artefacts 24 months) | `05` §7.3 |
| Legal hold | If the partner's legal function holds the data, the hold wins and the deletion date moves. This is the same precedence the evidence store's retention sweeper implements, and the same test — `TestSweepRespectsRetentionAndLegalHold` | `05` §2.1 row 15 |
| Transcript and shadowing recordings | Consent in writing covering recording, transcription and retention, at `05` §5.2; deleted on a stated date | `05` §5.2 |

### 3.6 Exit terms

- **Either side, 30 days' notice, no penalty.** We will not create a hostage
  situation and we will not accept being one.
- **The partner keeps everything they produced.** Full export of canonical events,
  evidence bytes with hashes, the audit ledger, and the seal file. This is a
  contract term (`17` §7), not a courtesy.
- **We keep nothing we are not permitted to keep**, and on exit the evaluation
  data is deleted or handed over, at their instruction.
- **The platform leaves**: uninstall instructions that work, a written record of
  what ran at their site, and — if they ask — the `kill -9` and recovery evidence
  from the M1 gates, because a partner who spent six months watching us work
  should get the artefacts.
- **The reference is not hostage to the exit** and the exit does not require
  them to keep being a reference. A partner may end the pilot and still speak for
  us, or decline to. We do not ask for a positive quote on the way out and we do
  not withhold anything on the way in.
- **The one thing that survives**: nothing contractual binds them. The only
  obligations that outlive the agreement are theirs, and they are: keep using the
  thing in the way you agreed, and tell us before you use our data for something
  else.

### 3.7 What we build for them that we do not build for anyone

This is the anti-scopedrift mechanism `00` §7 and `03` §6.2 need, expressed
concretely. A design partner gets nothing that is not a configuration, a service
act, or a general capability justified by a segment.

| Built for a design partner | Why it is not a fork | Where it is logged | Expires |
|---|---|---|---|
| A rule bundle for their topology — their doors, their camera ids, their zones | A site configuration and a rule bundle are the product being *used*; a new *schema field* set by one customer is a custom feature in disguise and goes through the `03` §6.2 two-question filter like any other | `docs/requests/` plus the bundle change record with its simulate diff digest (`05` §3.6) | Never — but it is versioned, diffed, reviewed and activated as four separate acts |
| Their thresholds, their quiet hours, their escalation ladder | Configuration | Site config, committed | On request |
| A schedule, a suppression window, a retention policy for their site | Configuration | Site config | On request |
| A bespoke one-time analysis of their incident history, delivered as a report | A service act, done with them in the room | Written up as a research artefact (`docs/research/YYYY-MM/`) | The engagement |
| On-site install, upgrade, a data migration, a camera mapping session | Professional services delivered by the two people who wrote the product | A timebox on the milestone plan, never its own track | The engagement |
| **A new capability, if three sites need it** | Then it is a product | `docs/requests/` with three dated independent requests, and a backlog row | Becomes a feature with the second site; with one site it is a service and should have been priced as one |

**What is never built for a partner, at any price, in writing:** anything that
weakens an invariant, adds a dependency, changes a contract, adds an action type
with physical impact, or forks the core for their platform (`03` §6.2 "What is
never an exception"). If a partner asks for one of those, the answer is the
`03` §6.2 exception path, which does not exist for these five, and the customer
is told what they are getting instead.

---

## 4. M2 paid pilot and M3 GA motion

### 4.1 Design partner → paid pilot conversion

| Step | When | What |
|---|---|---|
| **Qualify** | 2027-03, at the M1 exit review | The exit-gate pack has been reviewed *with* the partner for 60 minutes, the reference wording is agreed, the before/after baseline exists, and the partner says they would pay. A partner who says "this is great" and would not sign anything is a case study, not a pipeline |
| **Terms** | 2027-04 | Founding paid-pilot rate, PROPOSED **$600/site/month**, 6-month minimum, cancellable on 60 days' notice, then month-to-month on 60 days' notice. Perpetual licence, no expiry, no enforcement on site (`12` §3.3). 24/7 response is **not** offered at this rate or any rate until the rota exists — and at the base case it would require 58 sites to carry, which is the arithmetic in `17` §2.4 |
| **Contract** | 2027-04 | MSA, DPA, sub-processor list, the AI clauses from `17` §9, and a data-handling one-pager. Reviewed by counsel once, then templated. The drafting cost is an `18-financial-plan.md` line item, not an afterthought |
| **Conversion rate assumption** | — | **PROPOSED: 2 of 3 design partners convert.** One does not convert and we name the reason in the M2 retro. A 3-of-3 conversion is a sign we recruited friends |
| **The external one** | 2027-07 onward | `01` §M2: "First external customer who is not personally known." This is the conversion event that matters, because it is the first evidence that the motion works without a relationship. PROPOSED: first external pilot signed 2027-08, installed 2027-09, at the founding rate rather than the list rate, explicitly labelled a reference-acquisition price |

### 4.2 The pricing experiment design

`01` §M2: "The pricing experiment runs here, not before." `17-pricing-and-packaging.md`
holds the price points; this section holds the *experiment*, because an
experiment without a design is a price change with a survey attached.

**What we vary (one axis at a time, never two)**

| # | Variable | Values | Set at |
|---|---|---|---|
| 1 | **Price level** (M2) | PROPOSED three levels: **$450, $600, $850** per site per month, assigned to the first 6–9 paid sites. Roughly 3 sites per level, **not** randomised — the first sites come from the design partners and the pipeline, and refusing to randomise real deals is the correct trade. `17` §2.3 carries the assumptions each level rests on | 2027-04 |
| 2 | **Packaging shape** (M2–M3) | PROPOSED per-site-per-month versus per-site base + per-1,000-events, held at the same realised revenue per site by construction at this volume, so the *shape* is tested by which one the customer can explain to their own finance team rather than by the invoice | 2027-06 |
| 3 | **Contract length** | PROPOSED 6 months versus 12 months, with a stated discount on 12. A discount that exists is a discount that will be demanded by everyone, so the experiment measures the demand, not the discount | 2027-07 |

**What we hold fixed, and never vary**

| Held | Why |
|---|---|
| The product, the build, and the incident classes supported | A price experiment that also varies the product measures nothing |
| The onboarding effort (deployment time, configuration, training) | If we vary the effort with the price we are measuring our own diligence |
| The support commitment — `12` §7.3's business hours, stated identically to every pilot | Support is the thing most likely to be varied by accident and least likely to be noticed |
| The measurement design (`08` §4) | The before/after number must be produced the same way at $450 and at $850 or the pricing experiment contaminates the M2 gate |
| The reference ask | Every paid site is asked. If willingness to be a reference turns out to correlate with price, that is a finding about references, not about price |
| The discount policy within a level | One founding rate, quoted, no exceptions inside a level, or the levels stop being levels |

**How many sites before we can conclude — and what "conclude" means**

- **Minimum 6 paid sites across 3 price levels** (2 per level) before any
  comparison is even drawn, and that comparison is directional only.
- **PROPOSED 12 paid sites** before a price is adopted as the founding rate for
  M3. Below 12 the honest statement is "we have observed no resistance at these
  levels", which is a weaker and different claim.
- **What we will look at:** acceptance rate by level, time from first call to
  signature by level, objection themes by level, and — most useful — whether the
  customer's own procurement threshold ("we can approve under $X without a
  committee") falls between two of our levels. That is a direct measurement of
  the `17` §1 principle that a price must let a site say yes without a procurement
  committee.
- **What we will not conclude:** anything about willingness to pay at a price we
  have not quoted. A price nobody has been asked about is not a data point.
- **What invalidates the experiment:** a design partner converting at a founding
  rate is *not* in the experiment. Relationship-converted deals cannot price-test
  anything, and mixing them in is the most likely way to get a wrong answer.

**The rule about publishing price**

> **No price is published — on a website, in a deck, in a search listing, in a
> calculator, or in a standard "talk to sales" page with numbers on it — until
> there are three referenceable GA customers who have each seen a price and said
> yes at approximately that level.**

Three, not one, because one customer at one price is a data point. Not published
earlier, because a published price is a promise to a market we have not measured,
and in security a wrong public price is a procurement conversation we lose
before we start. Until then the answer to "how much is it" is: *"I will not
guess at your number, and I will not quote you one I have not validated. Here is
what I can tell you is true about the cost: it is per site, it does not scale
with your cameras, and it does not require you to move video anywhere."* Then
come back with a quote after the design-partner conversation.

### 4.3 The first case-study template

One template, and the rule is that **a case study with a missing number is not
written.** If the number is not measured, the case study is not ready, and the
backlog does not care.

```
TITLE      <Site type> — <the operator's own words for the outcome>
CLIENT     Named, with written consent for this exact text. Logo use is a
           separate, written permission and is not implied by the reference.
SECTOR / SITES / DURATION     named, dated, specific
THE PROBLEM, IN THE OPERATOR'S WORDS
           2–4 sentences, transcribed, lightly edited, with the edit noted.
           Not our paraphrase. The value of this section is entirely in it not
           being ours.
BASELINE — WHAT THEY MEASURED BEFORE
           MTTI per class, median AND mean, with N
           attention minutes (capped and uncapped), with the §2.2.1 caveat
           incidents/day; evidence per incident; operator actions per incident
           Their numbers, from their console, produced by their operator
           (`03` §1.2 R-12's single metric is this)
AFTER     — THE SAME MEASUREMENTS, THE SAME DESIGN
           The M2 measurement design stated in one line: within-subject,
           alternating ABAB blocks of one shift, counterbalanced, per class, with
           the declared minimum meaningful effect and the computed N
           — AND THE FOUR COMPANIONS, UNPROMPTED (`08` §4.5):
             missed_incident_rate on the same classes and window
             evidence_completeness
             the degraded-decision share
             the operator's own assessment, in their words
THE PROOF THEY ASKED FOR
           The replay that reproduced a specific decision from stored facts with
           the model switched off. Named incident class, not a slogan.
           The sealed checkpoint verified offline against a trusted key, and the
           same seal refused against a foreign key.
WHAT IT DOES NOT DO
           The limitations, stated by us, in the customer's hearing. A case study
           that has no limitations section is an advertisement.
WHAT THEY WOULD TELL THE NEXT SITE
           One paragraph, theirs, on the record, unedited, about the thing they
           would want to warn someone about. If they will not write it, the case
           study is not finished.
```

Metrics the case study **must** contain, or it is not published: per-class MTTI
(median, mean, N, before and after); `missed_incident_rate`; `evidence_completeness`;
the degraded-decision share; the AI plane's actual availability over the period;
`attention_minutes` per 1,000 events with the four companions; and the
investigation-time number in **the operator's own units**.

Metrics it **must not** contain, per `08` §8.1: alerts reduced, alarms
suppressed, "incidents prevented", a single "accuracy" number, and any benchmark
score. Each of those is a number that improves by hiding something.

### 4.4 "No public launch before M3" — the discipline, and its exceptions

`01` §M1: "No public launch, no pricing, no self-serve."

**What this forbids, concretely**

- No website with a sign-up, a trial, a price, or a capability list that implies
  the unbuilt (`00` §7, `05` §1.2). The pre-M3 public surface is: a written
  description of the three answers, a threat-model link, a way to send an email,
  and nothing that can be clicked to start using.
- No case study, logo wall, conference talk with a claim, podcast, or "we're
  excited to announce" anything.
- No VMS vendor marketplace listing, no app-store listing, no "integrations"
  count (`08` §8.1 calls that one a trap).
- No benchmark, no leaderboard position, no "we compared to X" claim (`00` §8).
- No free tier. A free tier before pricing is validated is a pricing decision
  taken by accident, at zero revenue, to a self-selecting population.

**The three exceptions, each pre-approved, each a written record**

| Exception | Condition | Record |
|---|---|---|
| The **M1 design-partner announce** (§3) | Announced to a named, closed list — the three partners, the people who referred them, and the interview participants who asked. Not published, not indexed, not in a mailing list we do not own | The agreed announcement text, before it is sent, in `docs/decisions/` |
| A **supplier or integrator conversation** where a customer already exists | No public content. A one-to-one briefing, with the same evidence discipline | `05` §3.7 commitment record for the specific customer |
| **Recruitment** | Always allowed. Engineering and go-to-market candidates are told the truth, including that the company is two people with a date and no funding, because the alternative is hiring someone into a surprise | — |

**The test for anything borderline:** *if this were read aloud by a design
partner who is about to decide whether to be a reference, would they object?* If
yes, it does not ship. This test is cheap, it takes one person, and it is the
entire compliance mechanism for a two-person company with no marketing
department.

---

## 5. The demo

### 5.1 Shape

**25 minutes, scripted, one incident, three questions answered.** The demo is not
a feature tour. It is a single real incident followed from the alarm to the
sealed checkpoint, with the AI plane deliberately stopped in the middle of it.

The three buyer questions map onto the run sheet and the demo is incomplete
without all three:

| Buyer question (`00` §4) | Where it is answered | Minutes |
|---|---|---|
| "What did the AI decide, and why?" | Steps 3–4 | 8 |
| "What happens when the AI is down?" | Step 5 — the centrepiece | 4 |
| "Can I prove the system did what it says it did?" | Steps 4, 7 | 7 |

### 5.2 The one demo incident

A single **after-hours restricted-zone entry**, one incident, used for the entire
demo. It is a recorded build from a design-partner site, sanitised, and it is the
*same incident object* every time, so the run sheet is a fixed script.

| Property | Value | Why this one |
|---|---|---|
| Class | After-hours entry to a restricted zone, corroborated | Needs two independent signals or it cannot be corroborated at all (`docs/threat-model.md` R2). Chosen because it is the class that makes the "two independent sources" architecture visible |
| Sources | Door/contact signal from one device; camera motion from a second, different device | Independence is the whole claim. A camera corroborating itself is theatre, and the demo should show the two devices named separately |
| Clock | One source is ~30 seconds fast, and is in the severe drift band | The drift exclusion is recorded on the verification result, visibly, and the corroboration still holds on the other pair. This is the most impressive thing in the demo and almost nobody shows it |
| AI observation | **Shadow-marked, and deliberately insufficient-evidence** on one field, `restrictedZoneBreach: unverified` | It cannot affect authoritative state (`TestShadowProfilesCannotAffectAuthoritativeState`), and it is displayed as an absence rather than as a negative. Demonstrates invariant 3 in a screenshot |
| Disposition | Dismissed by the operator, with step-up MFA, with agreement recorded | `dispositions.agreement` = `disagreed` on a field, which makes the override path real rather than theoretical |
| Retention | 3 days, so the evidence is present and the storage numbers are not scary | — |

### 5.3 The 25-minute run sheet

Times are cumulative. The demo operator is E1. E2 is not in the customer call
during a release week (§8.4).

| T | Screen / action | What is said (the line that matters) | Buyer question |
|---|---|---|---|
| **0:00–1:30** | Console, incident list. The site's local time with an explicit offset, `lang="en"`, `dir="ltr"`. The demo incident is the third row | *"This is Monday, 02:14, site local time, which is not my local time and not UTC. Every time in this product is the site's time and carries an offset, because the only time that matters in an investigation is the time the person remembers."* | framing |
| **1:30–3:30** | Incident detail. The event timeline: each canonical event, its `observed_at` / `received_at` / `normalized_at`, and the device that asserted it | *"Every one of these is a fact, and every fact names the device that asserted it. If you ask me later which camera raised this, that answer was written when the event arrived, not reconstructed now."* | Q1 |
| **3:30–5:30** | The verification result: corroborated, with the two independent sources named, and the drift exclusion shown on the third | *"Corroboration required two independent signals. This third device's clock is 30 seconds fast, which is in our severe band, so it is excluded from time-windowed corroboration — and you can see that it was excluded, here, rather than us quietly ignoring it. The incident still corroborates, on the other two."* | Q1 |
| **5:30–7:30** | The AI observation panel: model, model hash, prompt template and version, profile version, input evidence hashes, and the **shadow** badge; and one field reading *unverified — insufficient evidence* | *"This is an AI observation, labelled as one, with everything needed to reproduce it. It is running in shadow, so it has changed nothing. And that field says 'I cannot tell', not 'no' — because a model that cannot tell and a model that saw nothing are not the same event, and treating them as the same is how a security system starts lying."* | Q1 |
| **7:30–9:30** | `securityctl incident explain <id>`, then `securityctl incident replay <id>` in a terminal | *"This is the same decision, reproduced from stored facts, with no model running. Not 'here is a similar situation' — this incident, this decision, this evidence."* | Q1 |
| **9:30–13:30** | **The centrepiece.** Stop the AI runtime (a service stop, visible). Re-run `replay`. The output is byte-identical. Then start it again | *"I have just taken the model offline. Everything in the pipeline still ran. Every rule that depends on the AI has its degraded behaviour written in the rule itself, in advance — `if_ai_unavailable` — so nobody had to decide what to do at 02:14 when it stopped. Watch what happens to the decision."* *(the replay output does not change)* *"That is the second question answered. A system that cannot run without the model is a system that will fail in the one window when you most need it — which is a power cut, because that is when the model server is on the same UPS as everything else."* | **Q2** |
| **13:30–15:30** | The disposition form: four outcomes; escalate and dismiss require step-up MFA; agreement recorded | *"The operator's verdict is recorded separately and never overwrites what the model said. Both are stored. That is why 'the AI was wrong' is answerable a year later instead of being an argument."* | Q1 |
| **15:30–18:30** | `securityctl audit verify`, then `securityctl audit seal`, then `securityctl audit verify-seals` with **no database** and **no network** | *"This is a signature over the audit ledger, written offline to media you control. Now I verify it with no database at all — this works on a laptop on a plane. It accepts."* Then, with a foreign public key: *"And with the wrong key, it refuses. The chain is not something you can rewrite quietly, because rewriting a row breaks every seal after it."* | **Q3** |
| **18:30–21:30** | `securityctl rules show` / `simulate` on the active bundle, showing the reviewed diff digest and the activation record | *"This is what the site treats as an incident. Storing a bundle does not activate it. Simulating, diffing, reviewing and activating are four separate acts, and activation required a human to read a digest of the diff — not the bundle — and sign."* | Q3 |
| **21:30–23:00** | The site health surface: backlog, dead letters with reasons, clock drift, evidence disk, degraded decisions, AI availability | *"This is what I look at on Monday morning. Every number on it has a remedy written next to it, because an alert with no remedy is an alert everyone learns to ignore."* | trust |
| **23:00–25:00** | Q&A, seeded with the four questions we are always asked | *"Ask me what happens at 03:00 if my VMS goes down. Ask me what I do to prove a decision in six months. Ask me who is liable. Those three are the whole product and everything else is detail."* | all three |

**Hard rule: the demo stops at 25 minutes.** Overrunning it is itself a signal to
a security buyer — it means the product needs narration, and a product that needs
narration does not survive contact with a 03:00 shift.

### 5.4 Pre-demo checklist

Run in order the day before and again one hour before. Two people; the checklist
is short because a long checklist is a checklist that gets skipped.

**Environment (E2's, the morning of)**

1. `securityctl build-release` then `securityctl verify-release` on the exact
   build that will be demoed. Version number read out loud and recorded, and it
   is the same version named in the customer's pilot agreement.
2. Demo database restored from the sanitised fixture, not from a live design
   partner. **Never demo against a customer's site.**
3. `securityctl doctor`, `securityctl preflight`, `make test-race` green.
4. The AI runtime deliberately started **stopped**, so the centrepiece is
   genuinely taking something down rather than performing a "simulated outage".
5. A separate release-channel check: no build in the demo path is from a release
   week or a release train (§8.4).

**Content (E1's, the evening before)**

6. `docs/status.md` §"What is not built, and why" read aloud, and the three
   known limitations stated in the opening minute if the room is senior
   (Milestone endpoint surface unverified; the Python VLM runtime's build
   configuration; the stub commands that refuse with a named reason).
7. The profile shown at step 3.5 is confirmed **shadow**, with a passing dated
   evaluation report. If it is not passing, step 3.5 is cut and the demo says
   the AI plane is not enabled.
8. The `05` §3.7 commitment record for this customer read, and every number about
   *this* customer cross-checked. No number appears in the demo that is not in
   that record.
9. The foreign public key for step 15.5–18.5 is on a separate medium and known to
   be a *different* key, tested the previous day.
10. The backup recording exists: the demo environment captured end to end, with
    the audio, dated, on local media. If the demo fails, we play the recording
    and say so.

**Before every call**

11. E1 reads the profile in `05` §5.5: the operator's years on the job, and whether
    the person in the room does that job.
12. Confirm no real customer name, site, or incident appears in the environment.
    The fixture is sanitised; verify rather than assume.

### 5.5 Failure modes, and what we say

**The model being unavailable is not a failure of the demo. It is the demo.**

| Failure | What happens | What is said |
|---|---|---|
| **The model is unavailable — on purpose** | Step 9.5 happens, which is the centrepiece | *"This is the question I most want you to ask. I have just taken the model offline and nothing broke. Watch what happens to the decision — it is the same decision. That is not a limitation of the product; that is the product. Any system that cannot run without the model will fail you in a power cut."* **No apology. No workaround. No "let me fix it"** |
| **The model is unavailable — genuinely, unexpectedly** | The demo simply continues; the deterministic path is what we planned to show | *"That is unplanned, and I am going to do exactly what the demo does when the model is on, which is continue. Everything you are about to see runs without it. If anything on this screen depends on the model, I will point at it, and it should be nothing."* Then continue. If a *specific* number is missing, the rule is: **say what is missing and why, never substitute a plausible value** |
| The model is up but the observation is insufficient-evidence | It renders as an absence | *"It said it cannot tell. That is a real answer, and we are not going to launder it into a 'no'. A system that turns 'I don't know' into 'nothing happened' is the single most dangerous thing in security software."* |
| The profile is not promoted / has not passed its gates | Step 3.5 is cut | *"The profile running at this site has not passed its promotion gate, so it is in shadow and it cannot affect anything. I am not going to show you a capability that has not cleared its own gate, and the fact that I can say that in a demo is the same property you would want in the product."* |
| The demo environment is down | The recording, up front | *"My environment is broken, and rather than watch me fix it we are going to watch the recording from Tuesday. It is the same build, and I will tell you where the recording was taken and when."* Then play it. **Never debug a demo in front of a security buyer** — a buyer watching you fail gracefully learns something good, and a buyer watching you flail learns something they will repeat to their peers |
| The database is slow / a query hangs | Stop, do not wait | *"That is a real performance problem and it is on my list, here is the ticket number, and I would rather you saw it than not."* `07` §1.1: in this product a defect that affects an operator's ability to reach evidence is a **product** incident, not a quality incident. Saying so out loud is correct |
| The customer asks something the build cannot do | — | *"It does not do that. Here is what it does not do, and here is what is on the list."* Never speculate live. `05` §3.7: a commitment given in a demo is a commitment, and the artefact for it is the commitment record |
| The customer asks about a competitor by name | — | *"I will not make claims about their product that I cannot verify — including ours. Here is the question to ask any vendor, including me: can you reproduce a decision from six months ago with your system offline, and can you show me the record that it has not been edited?"* |
| The customer asks a security question we have not answered | — | *"I do not know, and I will not guess at a number. It is written down: here is the threat model, here are the accepted risks, here is the published defect list. I will answer in writing by Thursday."* `12` §10's disclosure policy makes that a commitment with a clock on it |

---

## 6. Sales collateral inventory

### 6.1 The rule

> **No collateral makes an unverified claim.** Every number in every document
> carries one of: a measurement with its method and N, a reference to a file in
> this repository, or a marker (`PROPOSED`, `ESTIMATE`, `NOT YET KNOWN`) with the
> measurement that would replace it. A document with an unmarked number in it is
> not published, and the review that catches it is a named person's job.

`00` §8 makes this the law for the roadmap and for our competitors. It is
applied to ourselves with the same severity, because the buyer is trained to
detect it and because a collateral pack with one unsupported number destroys the
credibility of the other nine documents in the same folder.

Review rule: **E2 reviews every technical claim, E1 reviews every commercial
claim, and neither signs the other's.** A document signed only by the person who
wrote it is not reviewed, per `07` §9.1's principle applied to prose.

### 6.2 What must exist, and by when

Dates are the milestone boundary they gate. "Exists" means complete, reviewed by
both, with every claim sourced, and stored under `docs/collateral/`.

| # | Document | Length | Owner | By | Contents | Must never contain |
|---|---|---|---|---|---|---|
| 1 | **One-pager** | 1 page | E1 | **2027-01-15** (M1, for design-partner recruitment) | The three buyer answers, in the buyer's words. What it is not: not a VMS, not a camera maker, not a cloud product. The 14-day shadow and the AI-offline demo as the two facts. A named person and an email | Any claim, any number, any logo, any adjective |
| 2 | **Security and architecture whitepaper** | 12–20 pages | E2 (E1 edits) | **2027-06-30** (before the M2 paid-pilot launch) | The pipeline end to end with the trust boundaries `docs/threat-model.md` names. Why video never leaves. The seven boundaries, the four accepted risks, the data flow, the deployment topology including the optional GPU, what happens when each component dies. **References `docs/threat-model.md` as the source and does not restate or improve on it** | A customer name without written permission; a boundary not in the threat model; an unverified market claim |
| 3 | **Evidence and compliance pack** | Folder, 6+ documents | E1 (E2 supplies evidence) | **2027-09-30** (M2) | The threat model; the 35 control→test mappings with the test names; the SBOM and provenance story; the AI governance record (every profile: version, hash, evaluation, approved uses, **known limitations**, deployment history); retention and privacy zones; the disclosure policy (`12` §10); the eight open defects from `07` §11.4 | An omission. A pack that lists what works and not what does not is the single fastest way to lose a security reviewer's trust |
| 4 | **Technical integration guide** | 15–25 pages | E2 | **2028-01-31** (M3 GA) | Installing from a signed archive, onto a service unit, with no shell access; the site config; the rule bundle lifecycle (store/simulate/diff/review/activate); the adapter surface and the fixture framework; the API; the evidence store and its retention; upgrade, rollback, backup, restore; the exact support matrix (`12` §3.6) | An instruction that requires a developer's laptop to be right (`03` §1.2 R-08's single metric) |
| 5 | **Pricing sheet** | 2 pages | E1 | **2028-01-31** at the earliest, and **not before 3 referenceable GA customers** (§4.2) | The price, what is included, what is not, the term, the licence model, the renewal, the support level, and the honest note that the GPU is optional | A published number before the three references exist; a per-camera price (`17` §1); a 24/7 commitment (`12` §7.3) |
| 6 | **Buyer FAQ** | 10–15 pages | E1 | **2027-12-31** (M2 close) | The three questions and long answers. The four objections that always appear: "our VMS already does this", "we cannot send video to the cloud", "who is liable if the AI misses something", "our security director won't allow it". What we do not do. What is not built yet. Support hours verbatim. How to leave | An answer to a question we have not verified. A hedge that reads as an evasion — a security buyer prefers "not yet, and here is when" |
| 7 | **Competitive brief** | 4–6 pages | E1 | **2027-12-31** (M2 close) | **Categorical only** (§9). The five categories, what each optimises for, what each structurally cannot do, and the do-nothing baseline | **A single vendor name.** No feature comparison, no pricing claim, no performance claim, and no sentence of the form "X cannot do Y" unless X's own documentation says so with a link and a date. A competitive brief that names a vendor is an unverified claim with a competitor's logo on it |
| 8 | **Data-handling one-pager** | 1 page | E2 | **2027-09-30** (M2) | What is stored, where, for how long, what never leaves, what we never do with it, and the export path | Anything that would need a lawyer. If it needs a lawyer, it is a contract term and it goes there |
| 9 | **Security questionnaire response pack** | Folder | E1 (E2 technical) | **2027-12-31** | The standard 100–200 question set, answered. Unanswered questions answered with **when, and why**, not with a blank | A blank. A blank is read as a "no" and costs the deal; an honest "not yet, here is the date and the reason" is read as competence |
| 10 | **Case study** | 4 pages | E1 | **First one 2028-02-28** (post-GA) | §4.3's template, in full, with every required metric | Any metric from `08` §8.1's prohibited list; any missing number (§4.3) |

### 6.3 The collateral review

Once a month, 45 minutes, both founders, and it is a real review with a defect
list:

1. Read every collateral document in `docs/collateral/` end to end, out loud.
2. Every number: is it sourced, with a method and N, or marked? A number with
   neither is a defect, not a nit.
3. Every claim about the product: is it in `docs/status.md` as built? A capability
   described in collateral that is not in the status doc is the fastest way to
   make a false commitment (`05` §7.4).
4. Every name, logo, customer, or case-study sentence: is there written
   permission for this exact text?
5. Every date: is there still a gate behind it, and is the gate still
   projected? A date whose gate moved is a commitment to update the collateral.
6. Output: a defect list, and a document updated in the same week. **Collateral
   that is reviewed and not corrected is worse than none**, because it looks
   maintained.

---

## 7. The channel plan

### 7.1 Why resellers multiply distribution without headcount

The argument is arithmetic, not enthusiasm. Direct motion costs us a qualified
conversation per site: a call, a demo, a security review, a pilot, a deployment,
a support relationship. At the PROPOSED cycle lengths in §2, that is roughly
**3–6 months and roughly 60–90 hours of E1's calendar per site** for a single-site
deal, and it does not scale because E1 is one person.

A reseller or integrator already has the relationship, the site access, the
installer relationships, the local-language support, and a customer who has
already decided to trust them. One enablement conversation with a firm that has
40 sites converts 40 sites that we would each have cost a quarter of E1's year.

**The honest counter-argument, which belongs in this document and not only in a
risk register:** resellers are also the reason a channel deal takes 12–18 months,
the reason 30% of the value leaves the business, and the reason the OEM's product
roadmap can deprioritise us in a quarter we do not control. The channel is a
leverage multiplier with a 12–18 month delay and a partner who can end it. It is
still correct, because the alternative is that E1 personally sells every site
forever, and `00` §6's capacity law makes that plan unreachable by 2029.

### 7.2 What an OEM actually needs

This is the list we should be able to answer *before the first conversation*, and
we cannot answer most of it today. That is the reason for §7.4.

| What they need | Status | Our answer today | Build item |
|---|---|---|---|
| **An SDK a third party can write an adapter against without reading our source** | `01` M4 gate 3; backlog #75 | The canonical event, device, evidence and action contracts exist *(real — `contracts/`)*. There is no public SDK, no conformance suite, no fixture framework, no simulator | Backlog #75–#77, M4 |
| **A certification suite they can put in their own CI** | `01` M4 gate | Does not exist | #76, #78 — and #78 requires it to be run by **someone outside the company**, which is the whole point |
| **Documentation that assumes the reader is competent** | No | The internal architecture and runbook docs | §6.2 item 4, extended with an SDK surface |
| **Margin** | `17` §4 | Not decided beyond a shape. **PROPOSED: 20–30% discount from list for a certified partner, with volume tiers, and a floor we will not go below** | Decision before the first partner conversation, not during one |
| **Support SLAs for their customers** | `12` §7.3 | We have an honest version (business hours, one named engineer, Sev-1 by phone) and a 24/7 version we cannot staff | The 24/7 rota. **A partner will ask for 24/7 and we must be able to say when, not improvise it** |
| **White-label** | `17` §4 | Deploy, document and console: yes. Provenance and attribution: no | `17` §4's clause set |
| **A no-fork agreement** | `17` §4 | Not drafted. The commitment is: one core, their adapter lives outside it, the non-adapter diff is a published artefact | `17` §4 |
| **A model of what happens when they stop selling it** | `17` §4 | Customers' data is theirs, at their site, exportable in full. There is nothing to take away, and that is both the honest answer and the commercial argument | `17` §7 |
| **A certification they can put in front of their compliance department** | Does not exist | We do not have certifications. Say so early (`12` §10's disclosure policy is the template) | Not a roadmap item; a fact |
| **A business case for their sales team** | No | The before/after investigation-time number, from a customer of theirs, is the entire business case | Depends on references existing |

### 7.3 The realistic OEM cycle

*(PROPOSED throughout; there is no precedent in this company's history and the
numbers are a shape, not a forecast.)*

| Stage | Duration | What actually has to happen | What kills it |
|---|---|---|---|
| Introduction through a mutual contact | 2–4 months | Someone who has seen the product recommend it. There is no other realistic first meeting for an OEM | Nobody in their organisation has a problem this solves |
| Technical evaluation | 3–6 months | Their architects read the SDK, their engineers prototype, and someone builds something | The SDK does not exist, so the evaluation is a demo, and a demo does not get past architecture review |
| Commercial and legal | 4–8 months | Partner agreement, margin, white-label, support, liability, exclusivity (which we refuse) | Exclusivity, or a liability position that transfers our obligations to them with no cap |
| Pilot | 6–12 months | One customer, one site, their engineers supporting it, our certification suite green | Their support team cannot carry it, which is the most common real failure |
| **Total to first revenue** | **15–30 months** | | |
| Total to a shipping product in their portfolio | 21–42 months | | |

**The implication, and it is the most important sentence in this section:** an OEM
signed in 2028 Q2 produces its first meaningful revenue in 2029–2030, and consumes
E1's calendar continuously from the first call. `18-financial-plan.md` §7 names
this as one of three risks that can kill the plan, and the decision date is
**2028-06-30**: if no OEM pilot agreement is signed by then, the M4 commercial
track does not get a budget line and the second VMS adapter is chosen purely on
the direct pipeline.

### 7.4 Why we build the Adapter SDK before we have OEM conversations

Four reasons, and the fourth is the one that decides it.

1. **The OEM's architecture review will ask whether their engineers can build an
   adapter without ours in the room.** The honest answer in 2026 is "not yet, and
   here is the roadmap". An answer in 2029 is a link.
2. **A certification suite is the only artefact that transfers capability.** A
   partner's engineering manager is not persuaded by documentation; they are
   persuaded by a test suite their own CI runs and passes. `01` M4's exit gate
   requires that suite to have been run by **someone outside the company**,
   which is precisely the sentence that makes it transferable.
3. **The SDK is the same artefact the second VMS adapter needs.** Building it for
   the OEM conversation builds it for `01` M4's architecture test at the same
   time, and `01`'s M4 non-adapter diff of zero behavioural changes is only
   meaningful if there is a defined adapter boundary. There is nothing wasted if
   no OEM ever signs.
4. **A partnership conversation that starts before the SDK exists costs us the
   only asset we have with an OEM, which is credibility.** We are asking a
   company with a hundred engineers and an installed base to build an integration
   for a two-person company. The only thing that makes that reasonable is having
   finished the part that is hard. Starting the conversation early means
   spending the relationship on a "soon".

**Concretely:** the first OEM conversation is scheduled in **M4** (2028 Q2),
which is after the Adapter SDK v1 and its externally-run certification suite are
existing artefacts. Before M4 the only OEM-adjacent work is: a partner one-pager
listing what an integrator can do today, and listening to the twenty integrators
in the design-partner sites' own supply chains, who are the actual distribution
channel for segment 1 and 2 and are available in M2 at a tenth of the cost.

---

## 8. The founder's time budget

### 8.1 The 40% tax, made concrete

`00` §6: two engineers, 13 weeks per quarter, **~60% on new build**, the other
40% being "the tax that quietly kills two-person companies: support, demos, sales
calls, incident response, hiring, admin, and the AI review pass."

In hours: 2 people × 40 h/week = **80 h/week**. 60% new build = **48 h/week**,
which is 624 h/quarter of new build across both — the number `05` §2.1's M1
capacity arithmetic is built on. **The tax is 32 h/week.** It is not slack. It is
the largest single line in the plan after engineering, and it is the line that is
skipped first, which is why it is written down here in hours.

### 8.2 The weekly split

| | E1 (Product / Architecture / Security / Sales) | E2 (Backend / AI / Data / Release) |
|---|---|---|
| **New build** | **24 h** (60%) | **24 h** (60%) |
| Sales and customer calls | **8 h** — discovery calls, demos, pilot deployments, the weekly written update, the bi-weekly incident review, the monthly measurement, interviews (`05` §5.1) | **2 h** — technical deep-dives, integration guides, evidence packs, review of E1's technical claims |
| Support | **3 h** — triage, written first responses, customer communication | **4 h** — diagnosis, the runbook entry the same session must write (`12` §7.5) |
| Release, CI, infra | 1 h | **5 h** |
| Admin, compliance, recruiting, legal | **3 h** | 3 h |
| AI review pass | **2 h** | **2 h** |
| **Total** | **41 h** — the 1 h over is the honest overflow and it is the first thing that goes | **40 h** |

**Where the 8 hours of sales go, in a normal M1 week** *(PROPOSED allocation)*:

| Activity | Hours/week | Note |
|---|---|---|
| Discovery calls and first meetings (3–5) | 2.0 | From `05` §5.1's interview flow. Most are with people who will not buy, and that ratio is normal |
| Design-partner calls: 3 weekly updates, 1.5 bi-weekly reviews, 1 monthly measurement, on-site time | 2.5 | `03` §6.3's cadence. Three partners × real attention is 2.5 h/week and it is not compressible |
| Demo preparation and collateral | 1.0 | The run sheet is scripted; the preparation is the environment, the backup recording, and the commitment record |
| Sales calls, proposals, follow-up written answers | 1.5 | `05` §3.7's commitment record is the expensive part: every number about a customer has to be written and dated |
| Pipeline and CRM | 1.0 | §10 |

**This is 40% of E1's week on customer-facing work, and it is not sustainable as
the customer count grows.** `03` §1.3 folds Sales into E1 permanently and
explicitly says a fractional seller costs a percentage of a revenue line that
does not exist yet. The first trigger to reconsider is the **third paid site**,
not a revenue number. At three sites, 24 h/week of E1 is on customers, and the
capacity law says 24 h is the whole new-build budget.

### 8.3 Variation by milestone

| Milestone | Sales hours/week (E1) | What changes |
|---|---|---|
| M1 (to 2027-03-31) | **4–6 h** | Recruitment and three design partners. Low volume, high touch. The demo is not yet ready, so the time is interviews and site work |
| M2 (2027-04 → 2027-09) | **8–10 h** | Paid-pilot conversion, the pricing experiment, first external prospect, collateral. This is the peak and it collides with the M2 measurement design in `08` §4, which is also in M2 |
| M3 (2027-10 → 2028-03) | **10–14 h** | GA, first repeatability of the motion, collateral completion, the case study. The highest load of the plan |
| M4 (2028 Q2 → Q4) | **12–16 h** | Channel conversations on top of direct, plus the second VMS adapter's pipeline choosing the adapter |
| M5+ | — | Not a 2-person company's problem. The hiring plan in `18` §5 is what makes it answerable, and it is a business decision, not an engineering one |

**The M2 collision, named now because it will not be visible until it is
expensive:** M2's exit gate is a *measurement*, and `08` §4 requires the design
to be written and signed before the trial opens, the baseline to be measured, the
minimum meaningful effect declared by both founders, and N computed from the M1
variance. That is E1 and E2's time in the same weeks as the pricing experiment,
the paid-pilot conversion, and the first external prospect. **The measurement
design wins.** It is the exit gate; the pricing experiment is not. If the month
is short, the pricing experiment loses a level, not the measurement.

### 8.4 The rules that protect it

| Rule | Detail |
|---|---|
| **E2 is never the one in the customer call during a release week** | A **release week** is the five working days from T-7 to T-2 relative to a tagged release in `12` §1.1's train. In that window E2's 2 h customer-facing slot goes to the release, and E1 covers the call or the call moves. A person in a demo while their change set is being frozen is a person who will either miss the demo or ship badly, and both are the same failure |
| **E2 presents a demo at most twice a quarter, and never the first one to a new segment** | E2's credibility is the technical depth. Spent on routine demos it is a slow, careful person doing a sales job, which is worse than E1 doing it quickly |
| **One customer call per week, maximum, in any week that contains a milestone checkpoint** | `05` §7.4's weekly burndown is 30 minutes and it is the thing that prevents a slip from being discovered at the boundary |
| **The demo is scripted and rehearsed, and E2 owns the environment** | E1 owns the run, E2 owns the build and the backup recording. Rehearsal happens weekly, 25 minutes, on the same script, so the failure is discovered on a Tuesday rather than in front of a prospect |
| **No customer call is booked inside a `kill -9` fault-injection run** | Those runs take a process down repeatedly on purpose. Being unavailable for two hours during one is not a service failure, and it is the cheapest possible scheduling rule |
| **A demo failure is a defect, not an incident** | Logged with `05` §3.6's template, reviewed at the next burndown, fixed if the cause is in the product. A demo that fails for the same reason twice is a DoD-blocking defect |
| **Every customer conversation is preceded by E1 reading that customer's `05` §3.7 record and `docs/status.md`'s "What is not built" section aloud** | `05` §7.4's last bullet, and it is the single most effective thing either of us does for reference quality |

---

## 9. Competitive landscape

### 9.1 The discipline

`00` §8: market claims, benchmark numbers, vendor comparisons and regulatory
statements are unverified until sourced, and are never asserted in the product,
the docs or the code. This section therefore contains **no vendor names, no
feature comparisons, no performance comparisons and no pricing claims about
anyone.**

What it does contain is a description of five **categories**, each characterised
by *what it optimises for* and *what that structurally prevents it from doing*.
That is a statement about incentives and architecture, not about a product, and
it is falsifiable by anyone with the source product's documentation. The test for
any sentence in this section: **could a competitor's own engineer read this and
say "that is a fair characterisation of our architecture"?** If no, delete it.

### 9.2 The five categories

| # | Category | What it optimises for | What that structurally prevents | How we compete |
|---|---|---|---|---|
| 1 | **Incumbent VMS AI features** | Retention and differentiation of the platform the customer already owns. The AI is an argument for not migrating | The VMS's own architecture is the constraint: a decision must be explainable in terms of the VMS's object model, the VMS's retention, and the VMS's support commitments. A general evidence record and an offline-verifiable audit chain are not in its path, because they are not the product | Not on features. On the three questions. A VMS is a better recorder than we are and we say so. The test a buyer can run in four minutes against any system, including a VMS: reproduce a decision from six months ago with the model off, and verify the record has not been edited |
| 2 | **Physical-security SOC and monitoring tools** | Operator throughput: fewer alerts, faster acknowledgement, a defensible service record. Multi-site visibility and ticketing discipline | Alert-centric UX shapes the product. A tool that starts from the alarm is built to make alarms cheap, and the interesting question — what actually happened — is a second tab. Multi-VMS correlation is hard because each vendor's data model is a competitor's territory | We are not a ticketing system. We start from the incident, and the incident is assembled from the whole installed base. The MSSP gets the exception queue and the evidence-grade record; it keeps its desk |
| 3 | **Generic computer-vision and people-analytics platforms** | Accuracy on published benchmarks, throughput per camera, and a model-agnostic platform story | Benchmark-shaped development produces profiles that score well in public and fail on a customer's night-time footage at a specific camera angle — the exact problem `internal/evaluation` exists to catch. And a general platform's economics are per-camera or per-stream, which punishes the customer for having more cameras and rewards the vendor for the wrong thing | We do not compete on model capability and we do not publish benchmark scores (`08` §8.2). We compete on attribution, gating and the degraded path. A model is a replaceable signed bundle behind our abstraction (`00` §7); a decision's provenance is the asset |
| 4 | **Human-manned monitoring and guarding providers** | Labour hours, coverage, and a contractual response time. The product is a roster and a radio | Cost scales with sites and hours. Every hour of attention is a paid hour, so the incentive is to make attention cheap and to define the response rather than to produce the record. They are excellent at the thing they do and they are not a technology comparison | The only honest answer is a split: we do not replace the guard. We reduce the hours of attention per incident, which is a different line in a different budget, and the arithmetic in the operator's own units (`05` §5.2) is the whole pitch. `00` §3's segment 1 objection 5 is the version of this we say out loud |
| 5 | **The do-nothing baseline** | Nothing. Its cost is the incumbent's: operator hours, missed incidents, unprovable claims, and the insurance and audit exposure of all three | Nothing. It is free, it needs no approval, and it is the default that every deal is actually compared against | **This is the competitor that matters most, and §9.3 is the section that wins or loses the company** |

### 9.3 The do-nothing baseline

Every deal is decided against "we keep doing what we do", not against a vendor.
The do-nothing baseline is not stupid. It has four genuine advantages and every
one of them is something we must respect:

1. **It costs nothing to approve.** No security review, no procurement, no
   change control, no new vendor to manage. Our entire motion is asking someone
   to take all of that on, for an uncertain benefit, before they have seen the
   number.
2. **It is what the operator already knows.** The operator's competence is
   expressed in the current workflow. A tool that asks them to change it is asking
   them to be learners at 03:00.
3. **It never has an outage.** We do. The moment we have a bad week, "do nothing"
   looks like the low-risk option, and the customer will be right about the risk
   and wrong about the outcome.
4. **Its failures are familiar.** A missed incident that everyone expected is less
   damaging to a security manager's credibility than a missed incident produced by
   a system that promised to catch it. This is a real effect and it is the reason
   we refuse detection guarantees.

**How we beat it, specifically:**

| The baseline's weakness | Our answer | The artefact that proves it |
|---|---|---|
| The operator's time is spent on a triage queue | Measured attention minutes, per 1,000 events, with the four companions | `05` §5.2's shift shadowing produces the number in *their* units, before we propose a target |
| A missed incident is usually discovered by somebody walking past | A recorded, sealed, attributable record of what was seen and decided | `securityctl audit verify-seals` offline, and the foreign key refused |
| "What happened at 02:14" takes three people and a day | Four minutes and one CLI command | The recorded demo |
| Nobody can say why an alert fired | Provenance per event, verification state per verdict, attribution per AI output | The run sheet, steps 3–5 |
| The alarm fatigue is structural and never measured | The false-positive taxonomy from `05` §5.1's interview 3 | The before/after, per class, with N |
| The value cannot be shown to a board | A number the operator produced, in writing, that they can take to a board | The `05` §3.7 commitment record |

**What we do not claim against the baseline:** that we prevent incidents, that
we reduce loss, that we make a site safer in a measurable way. `08` §3.2 lists
the numbers this roadmap refuses to state, and a "we prevent X%" figure is first
on that list because it is undefined, unmeasurable and the most tempting thing to
launder. Against the do-nothing baseline we sell **a measured reduction in hours
spent per incident, and a record that survives a question**.

---

## 10. CRM and pipeline hygiene

### 10.1 The minimum viable pipeline record

A two-person company does not need a CRM; it needs a list that a tired person
cannot get wrong. **The record is a file, not a system**, and the file is the
source of truth. `08` §8.1 is explicit that pipeline is not a product metric and
does not belong on a dashboard an engineer reads — it is a finance artefact.

Stored in `docs/pipeline/<org-slug>.md`, one file per organisation, committed to
the repository so it survives a lost laptop and is reviewable by the other founder.

```
ORG              <name, and which segment from 00 §3>
SITE             <name and count; air-gapped: yes/no>
CONTACT          <name, title, role: champion / evaluator / economic / blocker>
                  "champion" means they can give us the requirement, not the money
STAGE            <from §10.2>
TRIGGER          <the event that created the deal, in the customer's words>
THREE QUESTIONS  Q1 answered: <artefact>   Q2: <artefact>   Q3: <artefact>
                  A record with any of the three unanswered is not a stage-3 deal
PROOF SHOWN      <which demo run sheet step, on what date, to whom>
COMMITMENTS      <links to 05 §3.7 records. Every number said out loud appears here>
DECISION UNIT    <names and roles, and the one with the veto>
PROCUREMENT      <threshold below which they can approve without a committee>
DECISION DATE    <the date the customer has, and ours, and the gap between them>
COMPETITOR       <categorical only, from §9.2. No names in this repository>
NEXT STEP        <one sentence, with a date and a name>
LOSS REASON      <one of a fixed list — required on a closed-lost, and it is the
                  most valuable field in the file>
LAST TOUCHED     <date. A record untouched for 30 days is closed-lost with reason
                  "gone quiet", not a live deal>
```

**Why the record is this small and this strict.** Two people, and E1 is the only
one who writes it. The three questions and the commitments are mandatory because
those are the two fields whose absence produces a lost deal we did not learn from.
`LOSS REASON` is mandatory because `05` §5.4's three-similar-requests rule and
this pipeline share the same input: the third occurrence of a pattern is the
only thing that is allowed to become a decision.

### 10.2 Stage definitions

Each stage has an **entry condition that is a fact, not a feeling.** A deal moves
because a condition became true, and the date it moved is the date the condition
became true, not the date we noticed.

| Stage | Entry condition (all must be true) | Typical weight *(PROPOSED)* | What it is not |
|---|---|---|---|
| **0 — Target** | Named organisation, named contact, trigger event recorded. Nothing more | 0 | Not a deal. It is a list. It has no value and it is never counted |
| **1 — Discovery** | At least one conversation has happened and we have their vocabulary, not ours | 0 | Not qualified. "They asked for a demo" is not qualification |
| **2 — Problem confirmed** | They have described the problem in their own words, we have recorded it verbatim, and it maps to a segment in `00` §3 and to a supported incident class | 5% | Not "they said it was interesting" |
| **3 — Evaluating** | All three buyer questions answered in writing for this customer, a demo has been run, **and they have asked us a technical question we could not answer from collateral** | 15% | The 15% is not a probability of closing. It is a weighting so the weighted number cannot be inflated by count |
| **4 — Pilot agreed** | Written agreement, named start date, named success criteria, and a measured baseline exists | 40% | Not "they said yes" |
| **5 — Pilot running** | The product is installed at their site and the measurement is running | 60% | Not a pilot in name |
| **6 — Closed won** | Signed order, first invoice, production use | 100% | — |
| **7 — Closed lost** | A named reason code | 0 | Never "no decision yet" — that is stage 3 with a stale `LAST TOUCHED` |

**Loss reason codes, the fixed list.** Every one is a real decision we can act
on, and none of them is a judgement about the customer: `no budget`, `no
authority`, `incumbent contract`, `security review failed`, `evidence not
convincing`, `cycle longer than their budget cycle`, `wrong segment per 00 §3`,
`timing`, `build risk`, `other (written sentence)`.

The two most valuable codes are **`security review failed`** and **`evidence not
convincing`**, because both mean the product or the collateral is wrong rather
than the market being wrong, and both are counted in the monthly collateral
review (§6.3).

### 10.3 The forecast commitment rule

> **A number is only ever called committed if all four are true, and all four are
> facts on the record, not judgements:**
>
> 1. A signed commercial agreement, with a start date.
> 2. A named person inside the customer's organisation who has told us in
>    writing that they have the budget and the authority.
> 3. A procurement path that has a date on it — a purchase order number, an
>    approval workflow step, or a stated committee date.
> 4. No unresolved security, legal or technical review on our side.

Everything else is a forecast, and a forecast is labelled as one **every time it
is spoken or written**, including to ourselves. Three specific prohibitions:

- **Never** report a stage-4-or-later count as revenue. A stage-4 deal is a
  signed pilot agreement and no revenue.
- **Never** cite a weighted number to anyone other than the two of us and a named
  advisor. It is a planning device, and it loses all meaning the moment it is
  said out loud, because it is the number most easily inflated and the hardest to
  audit.
- **Never** put a number in an investor update that we could not reconstruct from
  the pipeline files, with the stage and the entry condition for each. If it
  cannot be reconstructed, it is not reported. This is `00` §8 applied to our own
  fundraising, and it is the first thing a good investor will test.

### 10.4 What a PROPOSED pipeline number is allowed to be called

| It may be called | It may **not** be called |
|---|---|
| "Weighted opportunities" | "Revenue" |
| "Pipeline" with the stage distribution shown | "Forecast" without the weights and the stage definitions |
| "Prospects" | "Customers" |
| "Sites in evaluation" | "Deployments" |
| "Assumed, not measured" | "Expected" or "projected" |
| Anything, provided the number is dated and the source is the pipeline files | Anything, provided it is later described as "roughly where we are" — because that sentence converts a weighed opportunity list into a claim in the reader's head, and it will be quoted back to us in a raise |

And the standing caveat, printed at the top of every pipeline summary:

> *These figures are assumptions derived from stage weights, not measurements.
> They are as unreliable as the weights, and the weights were chosen by the same
> two people who wrote this. Until there are three closed-won deals, every number
> in this file is a hypothesis about how a market behaves that has not yet been
> observed to behave that way.*

---

## 11. What this document does not claim

- Any market size, growth rate, incident volume, or false-positive rate for any
  industry. Not sourced, not asserted (`00` §8).
- Any vendor's capability, by name.
- Any customer outcome before `01` M1 exit gate 2 is signed with its artefact.
- Any cycle length as a forecast. Every one in §2 is *(PROPOSED)* and describes
  a shape.
- Any statement that a particular product behaviour will produce a particular
  business result. The measurable product claim is in `08` §4, the measurement
  design is in `08` §4.1–4.4, and the gate is in `01` §M2.
