# Strategy and North Star

Owner: E1 (Product/Architecture). Status: living. Last revised 2026-09-28.

This is the document every other roadmap document is measured against. If a
plan somewhere else in `docs/roadmap/` contradicts this one, this one wins and
the other is wrong.

## 1. The one-sentence product

Captain is the local-first intelligence and autonomy layer that sits **above**
the physical-security stack a customer already owns, turns raw signals into
verified, evidenced incidents, and then handles the routine work under policy so
that humans receive exceptions instead of alerts.

The product does not replace the VMS, the panel, or the camera. It replaces the
three human shifts a day of alarm triage, and it does that without ever asking
the customer to trust an opaque model.

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
the pipeline loses no accepted event and duplicates no side effect.

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

## 6. The capacity law

Two engineers plus heavy AI assistance. This is the constraint that shapes
every date in ``, so it is written down rather than
discovered later.

- **2 engineers, 13 weeks per quarter, ~60% of that on new build.** The
  remaining 40% is the tax that quietly kills two-person companies: support,
  demos, sales calls, incident response, hiring, admin, and the AI review pass.
  No milestone in this roadmap plans above that line.
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

## 7. What we are not building

Not a VMS. Not a camera maker. Not a generic SIEM. Not a general LLM platform.
Not cloud-only. Not a proprietary hardware ecosystem. We do not train foundation
models; we use the best available models behind our own abstraction layer, and
the abstraction is the asset.

The test for any new feature: does it deepen the intelligence layer above the
installed security stack, or does it grow us into a different company? A
feature that fails that test is declined even if a customer asks for it in
writing.

## 8. Evidence discipline

Market claims, benchmark numbers, vendor comparisons and regulatory statements
are unverified until sourced. Until then they are marked unverified with a source
and a date, and are never asserted in the product, the docs or the code. This
applies to us selling to security buyers, whose entire profession is detecting
claims that turn out to be unsupported, and it applies to the roadmap itself:
every date here is a commitment subject to a named gate, not a promise.
