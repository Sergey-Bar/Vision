# Strategy and North Star

Owner: E1 (Product/Architecture). Status: living. Last revised 2026-10-21.

This is the document every other roadmap document is measured against. If a
plan somewhere else in `docs/roadmap/` contradicts this one, this one wins and
the other is wrong.

> **The 2026-10-21 revision is unsigned.** §1 and §7 changed, and they changed
> what the company is. `AGENTS.md` reserves a change of this kind to a human
> who reads the diff, and none has. The amended text below is the shape the
> code is being built to; until it is signed, §9 records what was decided and
> what was not.

## The North Star metric

**Human Attention Minutes per 1,000 security events.**

It becomes the headline metric at M7, but it is instrumented from M1 — the
components exist (operator actions per incident, incident durations, automated
resolution, human-intervention events) long before the autonomy they measure.

It must **always** be reported with its four companions, and a report showing
the North Star falling while any companion regresses is a failed quarter, not a
successful one:

| Companion | Why it is not optional |
|---|---|
| Missed-incident rate | The failure that costs a customer an intrusion. Asymmetric against a false alarm by orders of magnitude. |
| Operator override rate | Measures whether the operator trusts us. A falling North Star with a rising override rate means we are hiding work, not removing it. |
| False escalation rate | The noise that burns out a desk. If this rises, the customer will turn us off. |
| Evidence completeness | The audit story. An incident closed without evidence is a claim, not a record. |

**Automation alone is not success. Trusted automation is success**, and these
four numbers are how we prove the difference rather than assert it.

## 1. The one-sentence product

**A VMS that proves.**

VISION is the local-first video management system that records the cameras it
is given, reads the VMSes a customer already runs as a first-class secondary
source, turns raw signals into verified, evidenced incidents, and then handles
the routine work under policy so that humans receive exceptions instead of
alerts.

Every frame, every decision and every exported evidence item is verifiable
offline. The system states what it does **not** know. Recording continues when
the cloud, the network and the model are gone. Video never leaves the site.

**What changed on 2026-10-21, and the reason.** This section used to say the
product sat *above* the physical-security stack and did not replace the VMS.
That was an accurate description of a layer, and it is the wrong description of
a product a customer buys in order to record video. We now own the camera path
— RTSP ingest, our recording, our pre-roll, our per-device-day hash chain —
and we keep reading other vendors' VMSes as a first-class source. Both are the
product. Neither is a downgrade of the other.

The reason is not ambition. It is that a layer cannot make the one promise a
security buyer actually asks for, which is *prove what you recorded*. Recording
continuity is ours to own, and a gap in it is ours to publish rather than
merely to have inherited.

The intelligence above the stack is still the product's mind; what changed is
that the recording now sits under it, in the same product, on the same site,
under the same audit ledger. The three human shifts a day of alarm triage are
still what the customer is buying, and the model is still never asked to be
trusted opaquely — it is asked to be replayable.

## 2. Why now

Three things became true at once, and the window is measured in years, not
quarters.

1. **Vision-language models became good enough to be boring.** Asking a model
   "is there a person in this restricted zone, and what are they doing" returns
   a structured, useful answer on hardware a customer already owns in a back
   room. In 2023 this needed a cluster and produced prose. It now runs on a
   single GPU and can be schema-validated, which is the entire reason this
   company is possible.
2. **The VMS became an appliance and the customer got tired of it.** The market
   has converged on connected systems with flexible on-prem and hybrid
   deployment rather than rip-and-replace. That is a tailwind for a layer that
   makes the installed base smarter.
3. **Buyer scrutiny of AI in physical security arrived before the technology
   matured.** Every serious buyer now asks which decisions a model made, what
   evidence supported them, and what the system does when the model is
   unavailable. Almost no incumbent can answer those questions. Answering them
   is the product.

Point three is the wedge. A cheaper investigation product loses to free. A
provable one does not.

## 3. Who we sell to, in order

We do not sell to everyone. The order is a sequence, and each rung only becomes
reachable when the previous one has produced a reference the next rung can
quote.

| # | Segment | Entry milestone | What they buy | Why first |
|---|---|---|---|---|
| 1 | **Single-site commercial operators** (warehouses, clinics, retail chains with one guarded site) | M2 | Paid pilot, per site | Fastest decision, single operator, pain is immediate and daily. Buys with a credit card and a phone call. |
| 2 | **In-house security teams at 5–50 site organisations** | M3–M4 | Per-site platform + enterprise features | Same buyer, more sites. Expansion revenue with zero new acquisition cost. |
| 3 | **VMS OEMs and integrators** (Genetec, Milestone resellers, regional integrators) | M4–M5 | Adapter SDK, OEM/private-label programme | Highest leverage, slowest cycle. A reseller who ships our layer multiplies our distribution without headcount. |
| 4 | **MSSP / SOC operators serving physical security** | M5–M6 | Multi-tenant monitoring, exception queue | Already running a 24/7 desk. Our exception queue is their throughput problem, and they are a channel, not just a customer. |
| 5 | **Regulated verticals** (healthcare, critical infrastructure, government) | M5–M7 | Compliance-grade deployment | Highest ACV, longest sales, hardest evidence requirements. Entered last, on reference from segments 2–4. |

**Anti-segments, permanently.** Camera and hardware manufacturers we would
compete with. Generic SOC/SIEM platforms where physical security is a
line item. Cloud-only products an air-gapped site cannot buy. Any custom
single-customer product. If a deal requires us to become one of these, it is not
a deal.

## 4. The three questions every buyer asks, and our standing answer

These three answers are the product. Every demo, every doc, every sales call
maps back to one of them.

**"What did the AI decide, and why?"**
Every decision is a deterministic function of stored facts and the stored
`AIObservation`. Replay reproduces it months later. The model is never re-run to
explain a past outcome, and every AI output is an attributable record with its
model version, prompt version, profile version and input hash.

**"What happens when the AI is down?"**
Nothing breaks. The deterministic core runs with the AI plane entirely absent.
A missing AI observation is never a negative observation. Any rule that
consumes an AI signal must declare `if_ai_unavailable` explicitly, so its
degraded behaviour is written down before it ever runs.

**"Can I prove the system did what it says it did?"**
The audit ledger is append-only and hash-chained, sealed to signed offline
checkpoints. Editing any row fails verification. A `kill -9` at any stage of
the pipeline loses no accepted event and duplicates no side effect. And the
recording is part of that ledger: every segment is chained to the one before
it, per device, per day, and the chain head is sealed into the ledger at day
close. A gap in the recording is measured and published, not hidden.

## 5. The moat, and why it compounds

The moat is not the model. It is the accumulated, site-specific, evidence-backed
record of what happened at a customer's facility and what the operator decided
about it. The sequence in which that becomes hard to copy:

| Milestone | Moat | What the competitor cannot do |
|---|---|---|
| M1 | **Trust** | Reproduce a decision with full attribution. |
| M2 | **Investigation** | Retrieve and correlate evidence across cameras, access and sensors automatically. |
| M3 | **Workflow** | Know how a real security desk actually closes incidents, encoded as versioned playbooks. |
| M4 | **Integration** | Speak every vendor and protocol their installed base speaks. |
| M5 | **Enterprise** | Operate across a whole organisation, with identity, HA and central control. |
| M6 | **Context** | Know what normal looks like at *their* site, and detect when it changes. |
| M7 | **Autonomy** | Run routine operations under policy without a human in the loop. |
| M8 | **Ecosystem** | Be the layer other vendors build their features on. |

The compounding claim, stated precisely so it can be falsified: **by M6, a new
customer costs more to onboard than an existing customer costs to keep, because
the site's baselines, memory and rule pack are already built and the marginal
site is configuration.** If that is not true at the M6 review, the data moat did
not materialise and M7–M8 are an automation project on top of a search product.

**The recording is a second moat, and it is the one the incumbents cannot
copy without contradicting their own story.** They ship camera-to-cloud, and
their unit of evidence is a screenshot somebody exported. Ours is a
hash-chained segment set on the customer's own disk, with a measured
`recording_continuity_gap_seconds` published per device. The incumbents cannot
answer "were you recording at 03:14 on the 12th?" without a forensic
engineering exercise, and they cannot answer it at all about a site that was
air-gapped. Copying that means becoming local-first, and local-first contradicts
the recurring revenue that their cloud attachment exists to produce.

## 6. The capacity law

Two engineers plus heavy AI assistance. This is the constraint that shapes
every date in [the master roadmap](master-roadmap.md), so it is written down
rather than discovered later.

> **Overridden for the AI VMS programme on 2026-10-21, by the founder.** The law
> below still holds for everything that is not the media plane, the device
> registry, the AI provider registry or the command deck — which is, for now,
> most of the company. What changed is the *number of tracks*: the AI VMS
> programme runs four independent streams at once (P0/P1/P4/P6 in the execution
> plan) because each of them is a precondition for a different downstream
> phase, and serialising them would put the camera lab, the model providers and
> the ONVIF membership — three things with external lead times — behind each
> other. The law's other two clauses survive unchanged and are the ones that
> keep the plan honest: **scope reduction is the only allowed response to a
> slip**, and **no new runtime, no new datastore, no new protocol family
> without an ADR**. The override is recorded here rather than left implicit,
> because a capacity law that is quietly not being followed is a plan that
> cannot be dated.

- **2 engineers, 13 weeks per quarter, ~60% of that on new build.** The
  remaining 40% is the tax that quietly kills two-person companies: support,
  demos, sales calls, incident response, hiring, admin, and the AI review pass.
  No milestone in this roadmap plans above that line. A quarter of new build
  capacity is therefore **15.6 engineer-weeks**, and a 26-week milestone window
  is **~31 engineer-weeks**. Every document is sized against that number.
- **AI multiplies implementation, not judgement.** AI writes most of the code
  and most of the first-draft tests. It does not decide what to build, decide
  whether a decision is defensible, talk to a customer, or sign an exit gate.
  Every exit gate in this roadmap is signed by a human.
- **Sequential, not parallel, at small scale.** Two engineers cannot run three
  risky tracks at once. The roadmap therefore keeps exactly one milestone
  active plus one parallel track that is never itself on the critical path.
- **Scope reduction is the only allowed response to a slip.** Not more hours,
  not a third stream of work. Each milestone in the master roadmap has a named
  `Reduced scope` cut so a slip is a decision rather than a crisis.
- **No new runtime, no new datastore, no new protocol family without an ADR.**
  The value of the existing stack is that a six-month-old release is still
  installable on a customer's isolated network. A dependency that breaks that is
  a regression, not a feature.
- **The media plane does not break this law, and that was a decision rather
  than an accident.** The recorder is a **second Go binary** (`security-recorder`),
  not a second language, and it is a *process* rather than a *runtime*: it
  speaks the same Postgres, the same contracts and the same release archive as
  the core, and it is a separate process so that a recorder crash cannot stop
  event ingest. Video bytes live on the filesystem or the customer's own object
  store — **not in Postgres** — so the datastore did not change either. FFmpeg
  and ONNX stay inside the AI sub-process, where they already are. The only
  new protocol family is ONVIF, and it is §9's item for a human to sign.

## 7. What we are not building

Not a cloud video service — we host no video, ever. Not a camera
manufacturer: we consume cameras, we do not build them. Not a generic SIEM:
physical security is the whole product. Not a general LLM platform. Not
cloud-only. Not a proprietary hardware ecosystem. Not a GPU farm for playback.
Not a device-driver plugin marketplace in the first cut.

We do not train foundation models; we use the best available models behind our
own abstraction layer, and the abstraction is the asset.

**The test for any new feature, in order.**

1. **Does it make the record provable?** The recording, the decision, the
   evidence, or the statement of what the system does not know. A feature that
   moves none of those four is a feature nobody can audit, and it is declined.
2. **Does it grow us into a different company?** A feature that fails this is
   declined even if a customer asks for it in writing.

**What the old test said, and why it changed.** Until 2026-10-21 the test was a
single question: *does it deepen the intelligence layer above the installed
security stack?* That question assumed we never recorded video, and it is
exactly why the plan was a layer. It is now the second half of the first
question rather than the whole of it. A camera path is not a departure from the
product. And a layer that cannot prove its own recording has handed the
product's central promise to somebody else to keep.

## 8. Evidence discipline

Market claims, benchmark numbers, vendor comparisons and regulatory statements
are unverified until sourced. Until then they are marked unverified with a source
and a date, and are never asserted in the product, the docs or the code. This
applies to us selling to security buyers, whose entire profession is detecting
claims that turn out to be unsupported, and it applies to the roadmap itself:
every date here is a commitment subject to a named gate, not a promise.

## 9. The 2026-10-21 revision: what changed, and what is still unsigned

**One sentence.** The company stopped being a layer above the installed
security stack and became a video management system that proves, keeping the
layer above the stack as its mind and the other vendors' VMSes as a first-class
secondary source.

**The four decisions the revision makes.** They are recorded in full, with their
reopen triggers, in
[`docs/roadmap/milestones/m2-ai-vms-execution-plan.md`](milestones/m2-ai-vms-execution-plan.md).

| # | Decision | Reopen trigger |
|---|---|---|
| D1 | **Media: we own the camera path** (RTSP ingest, our recording, our pre-roll, our chain) **and** keep reading other VMSes as a secondary source | never |
| D2 | **AI: policy-gated multi-provider.** `local_only` is the default; cloud is an explicit, audited, per-site opt-in | never |
| D9 | **Video never leaves the site.** Only management may federate to cloud | never |
| D10 | **Postgres + RLS is the system of record. Media bytes live on the filesystem or the customer's own object store** | never |

**What the revision costs, stated plainly.** Three things this document used
to assert are now false, and every one of them was load-bearing:

1. **"We do not replace the VMS."** We now compete with one. The competitive
   ground is not recording — everyone records — but *provable* recording, and
   the ground we chose is narrow enough that it is defensible and wide enough
   that it is a real company.
2. **"Not per camera. Ever."** That was correct for a layer and wrong for a
   VMS, because a VMS's cost driver *is* the device count.
   `pricing-and-packaging.md` §1.1 carries the amendment and the reason.
3. **"Not a VMS."** The test in §7 is now two questions, and the first one
   covers the ground the old test excluded.

**What is still unsigned, and therefore not a decision.** `AGENTS.md` reserves
this to a human who reads the diff:

- the strategy change itself — this section, §1, §4, §5 and §7;
- the pricing model change — `pricing-and-packaging.md` §1.1 and §4;
- the **auth posture reversal** — the appliance era's "no in-app login" cannot
  stand for a VMS sold to an enterprise, which cannot have a configured
  principal standing in for a person. `docs/decisions.md` D-16 holds the
  current reading, and a new proposal supersedes it;
- ONVIF as a new protocol family, which §6's capacity law gates behind an ADR;
- every migration and every published contract change in the plan.

**What did not change.** The five invariants in `AGENTS.md` are untouched, and
so is the North Star: human attention minutes per 1,000 security events, always
reported with its four companions. A VMS that records more does not make the
operator's attention any less valuable, and a product that made the North Star
irrelevant by removing the operator would be a product that stopped being the
thing a customer bought.
