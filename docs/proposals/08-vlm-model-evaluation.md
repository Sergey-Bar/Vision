# Proposal 08 — Evaluating an off-the-shelf VLM against the existing gates

**Status:** proposal. Nothing is implemented. No dependency is proposed.
A human decides.

## The gap

`docs/status.md` is precise about what is missing and why it is honest rather
than faked:

> There is no model, no weights, no GPU and no signed bundle. The runtime
> refuses rather than pretending.

And:

> **What a model adds:** a `run_inference` body. Everything around it —
> verification, attribution, validation, degradation, the reason vocabulary — is
> written and tested.

`internal/evaluation` implements the promotion gates as code: `DefaultGates`,
`ShadowGates`, `Evaluate`, with a `Runner` and labelled `Case`s. DoD item 3 is
"a production VLM profile passing its gates".

So the missing thing is a candidate to run **through the gates that already
exist**. That is a well-posed problem with an existing measuring instrument,
which is rare and worth taking advantage of.

## Candidates found

No production-ready, licence-clear model for this task was found. Four research
implementations with runnable code were, all in video anomaly detection for
security video:

| Project | Approach | Claimed result |
|---|---|---|
| **`Memories-ai-labs/memories-s0`** | 3B VLM, edge-oriented, Qwen2.5-VL base, token compression and structured pruning | F1 **79.21** zero-shot on SmartHomeBench, reported as exceeding larger models including GPT-4o and Claude 3.5 Sonnet |
| **`Muk-00/CoReVAD`** (ICPR 2026) | Training-free, one frozen VLM, emits an anomaly score **and** a temporal description; Local Response Cleaning to suppress generative noise | Competitive among training-free methods on UCF-Crime and XD-Violence |
| **`Obada-Badee/GridVAD` | VLM as a *proposer* only, with Grounding DINO and SAM2; Self-Consistency Consolidation keeps only proposals recurring across independent samplings | Reports zero-shot pixel localisation and a controllable precision/recall knob |
| `qdrant/video-anomaly-edge` | Detection as nearest-neighbour search against a "normal" baseline; ~10% of clips escalated to a VLM | 94% recall, 0.97 AUC-ROC on UCF-Crime |

**Licences and weight terms are unverified for all of them, and at least one
carries a non-standard licence marker.** This is a hard blocker on any commercial
use and must be settled before any of this is downloaded.

## The one idea worth more than the models

`GridVAD`'s Self-Consistency Consolidation is a direct answer to the failure
this product cares most about.

`AGENTS.md` invariant 3: a missing AI observation is never a negative
observation, and `internal/rules` requires a rule consuming a Tier C signal to
declare `if_ai_unavailable`. `observations.Degradation` distinguishes *not
asked* from *asked and degraded* from *asked and it could not tell*.

The GridVAD technique is: run the same question M times over independent views,
and treat only a **recurring** answer as evidence. That converts a
single-sample VLM assertion into something that can be checked for stability —
and a `observations` record could carry `independent_samples` and
`recurrence_count` rather than a single opaque boolean. A proposal that recurs
in 5 of 7 independent samples is categorically different from one asserted once,
and the difference is measurable, replayable, and storable.

That is a **contract change**, which means `AGENTS.md` applies in full: contract
first, regenerate, let `contractcheck` decide, and a human reads the diff.

## What we would build

Nothing. The first step is a written comparison, not a dependency:

1. Establish whether a third-party model may be used at all — licence and
   weights, per candidate, with sources and dates, per the evidence rule.
2. Write the labelled `evaluation.Case` set. This is the real work and the real
   deliverable: without cases matching the actual security scenarios and the
   actual `Degradations` vocabulary, any model's number is uninterpretable.
3. Run candidates through `internal/evaluation` **in shadow mode** — the mode
   that already exists for exactly this.
4. Let the gates decide. If none passes, that is a finding worth having, and it
   is the same finding a stub would have produced more expensively.

## What we would not do

- **Not import research code into `deploy/ai-runtime`.** That process is
  dependency-free, standard-library-only by design and by test. A research
  repository with `thirdparty/`, model checkpoints and a torch pin is the
  opposite of that shape, and `AGENTS.md` says the bundle is untrusted input
  that must be verified before execution.
- **Not invent a benchmark to justify a model choice.** `README.md` states that
  market claims and benchmark numbers in the source material were never
  verified. That applies to these projects' numbers exactly as it applies to
  vendor marketing.
- **Not let a model's score justify weakening a gate.** `DefaultGates` and
  `ShadowGates` are the law. A model that scores well does not get a relaxed
  threshold; a threshold is changed by a human, in a diff, with a reason.
- **Not confuse a benchmark win with a product fit.** UCF-Crime and
  SmartHomeBench are not a corporate site. A gate is only as good as its cases.

## Risks

- **The numbers are not measurements.** Every figure above is a third party's
  claim, read from a README or abstract. `Memories-S0`'s F1 was not reproduced
  here and cannot be reproduced without the data.
- **Failure mode coupling.** A model chosen partly for a benchmark is partly
  chosen for that benchmark's distribution. Sites differ; cameras differ; the
  degraded path must stay the default when the distribution does not match,
  which invariant 3 already requires.
- **Silent shape change.** Adding a field to `observations` to carry recurrence
  is a published-contract change to protobuf and JSON Schema. It touches the
  wire, the AI runtime's `ALLOWED_DEGRADATIONS` agreement across three places,
  and the golden tests. This is a milestone-sized decision, not an add-on.

## Confidence

Low on the models, high on the process. The correct next action is writing
labelled cases, and that is valuable **regardless of which model is chosen** —
including choosing none.

## Decision required

Is a third-party model acceptable in a product whose evidence base is an audit
ledger? And separately: is the Self-Consistency idea worth a contract change,
which is the only durable thing in this document?
