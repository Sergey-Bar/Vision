# Track C — ten minutes, for a customer or an investor

Same engine as Track J: the same hub, the same commands, the same files. The difference
is pacing and what gets emphasised — this track spends its time on the problem, on what
quiet is worth, and on the parts of the product a buyer can check without trusting us.

Run `scripts/showcase.ps1` first; it prints what this machine has actually produced
before the browser opens.

Total: **10:00**. Nothing in this script is a market number or a projection, because
this repository does not invent numbers. Every figure below is a measurement with a
file behind it, and the two things that are not measured are said to be not measured.

---

## 0:00 — The problem, in two sentences (60 s)

> "A security operation fails in two directions at once. It drowns: every naive pipeline
> turns a hundred little events into a hundred alarms and the operator stops reading
> them. And it goes blind silently: a model call fails, a camera moves, and the system
> reports 'nothing happened' — which is the one report an operator must never get for
> free."

> "VISION is built around the second failure. It is a camera-pipeline that can tell you
> *how it knows* — and, when it cannot decide, says so with a word and a number instead
> of silence."

---

## 1:00 — The hub (60 s)

Open `http://localhost:8080`. Read the one line, then the three chips: gate,
red-check, incidents — each read from this machine at page load.

> "This is not a deck. Every badge, every number you are about to see, is read out of a
> file on this machine as the page loads. The finance of this product is the same fact:
> evidence you can audit is what makes an automated decision insurable."

---

## 2:00 — The live surface: `/live` (90 s)

Click **Live**. Two surfaces on one page, and the page labels which is which: the
security POC's own live gate — a terminal run (`scripts/live-demo.ps1`) whose **report**
and **transcript** open from this page — and, below it, the lab-grade live analytics
surface (Python detector in `tools/`, CUDA), marked lab and not on the gate path.

Read the run's own report:

- a real public camera resolved to a live stream; **11 preflight checks** passed
  against it — 1920x1080, 30.000 fps, decode at 6.42x real time;
- the watched window produced **no detections**, so every check that needs one reads
  **NOT RUN** — and `prove` **refused**: "a proof of nothing is not a proof."

> "Two things to notice. One: the two detector paths this module ships are both on the
> page with their own numbers. The in-process path measures p99 16.4 ms — it fits
> `ai_fps=5` — and it still refuses every frame of the pinned export, because that
> export is a multi-scale anchored head and the in-process decoder reads only fused
> outputs. The Python sidecar decodes that family; its loopback p99 is 471.5 ms
> recorded, 809.3 ms re-measured on this box — too slow for the declared rate, and the
> gate refuses to start rather than pretend. Two: when the relay is unreachable, the
> run swaps in a synthetic clip and *labels it synthetic*. There is no fake camera
> anywhere in this product."

---

## 3:30 — The decision, and the arithmetic of silence: `/pipeline` (120 s)

Click **Pipeline**. One incident, replayed from this machine's own files.

Walk it once: frame → detection → decision from stored facts → clip cut, hashed, read
back → model verdict stored with the model's own name → event chained into the ledger.

Then the part this product exists for — open the honesty fields and read them:

- `reasonCode: ai_plane_absent`, `decidedWithoutAiObservation: true`,
  `observationDegraded: true`, `insufficientEvidence: true`, model `"none"`.

> "Five words cover the whole of 'what did the model say' — `asked_answered`,
> `degraded`, `not_asked`, `cannot_tell`, `no_objects` — and there will never be a
> sixth invented in a description field. Three of them get their own Milestone
> Analytics Event Type, so 'we could not assess this camera' is a filterable alarm."

Then the arithmetic, which is the slide no competitor has:

> "If the model is unreachable for an hour and 500 crossings happen, the operator gets
> **one** alarm carrying the number 500 — never 500 alarms, and never 'no incidents'.
> The crossing happened; only the verdict is missing, and the blindness is what gets
> aggregated. That is a test in this repository, not a promise:
> `TestFiveHundredUnaskedProposalsProduceOneEvent`."

---

## 5:30 — Evidence you can poke: `/trust` (150 s)

Click **Trust**. Start with the two panels you did not have to trust us for: the
verification record (newest gate run, with its commit and honesty flags) and the
red-check (a control is a control only if it was **watched failing** — the page counts
the controls and reads the transcripts).

Then press the three buttons, in this order, and let the output panels speak:

1. **Recompute the ledger chain** — every row re-hashed and re-chained:
   `PASS — n row(s) … recomputed and chained`.
2. **Tamper one row, then restore it** — one byte of one row changes, the chain fails
   **and names the row**, the byte and the triggers are restored, and the third pass
   verifies again. It refuses to tamper a chain that is already broken, because a second
   break hides the first.
3. **Replay the newest decision — zero provider calls** — the decision is recomputed
   from the stored row; the model is not called.

> "This is the sales argument. Not 'trust our model' — *check our record*. An edit is
> detected, located by sequence number, and survives a restart. A replay is
> bit-for-bit, and it costs nothing because nothing is asked again."

If you have 30 more seconds: open the invariants table at the bottom — five laws, each
with the test that would fail if it were violated.

---

## 8:00 — Quiet as a product feature: `/operations` (60 s)

Click **Operations**. This is where the system stays quiet, and why. The moved-camera
class is the one to name here:

> "Somebody knocks the camera, or tapes the lens, and the camera reports nothing — a
> silent hole in coverage. This pipeline keeps a reference view, scores every frame
> against it, and fires only when the whole view has stayed different for a sustained
> period. A car driving through the frame does not raise the score; a moved camera
> does. And when it fires, it fires **once** and then counts its own silence — a naive
> pipeline would report the camera moved *and* a hundred things happened, and the second
> of those is false."

---

## 9:00 — Lab tools and the census (60 s)

Click **Lab** — fleet management, an autonomous operator, conversational search over
stored observations, behavioural learning. These are Python tools from `tools/`, shown
because they are real, labelled **lab demo** because they are not on the gate path.

Click **Census** last, and press `NOT RUN` first:

> "Thirty capabilities, one row each, every row carrying the command that proves it or
> the file it was measured into. The `NOT RUN` rows are the pitch: accuracy on real
> footage is not measured, nothing was pushed to a real Milestone server, relay footage
> is watched and never stored, and the CPU sidecar's p99 does not fit the declared
> frame rate. A vendor that shows you this filter is telling you where to push. Push
> there."

Closing, then stop:

> "What is next is a person's decision and not a date — the plan of record is in the
> repository, and the same pages will show what has been proven and what has not, on
> the day it is."
