# Measured, decided, and published

The numbers this repository quotes, and the decisions taken from them. Everything here is
a **measurement** or a **decision with its reasoning**; nothing is an estimate left
standing as if it were a number someone checked.

Reproduce every measurement with the command beside it.

---

## 0.5.1 Novelty pass cost at 1080p, pure Go

| | |
|---|---|
| **Measured p50** | **3.507 ms** |
| p95 | 4.510 ms |
| p99 | 5.513 ms |
| mean | 3.547 ms |
| frames | 2,000 |
| source | 1920x1080, noisy grey field with a 40x30 bright square |
| hardware | this machine, no GPU involved |
| **A1 assumed** | **3.0 ms** |

**The measurement is 1.17x A1's assumption, so A1's arithmetic stands.** That is the
whole point of measuring it: an assumption that survives being checked is worth
publishing, and one that does not would have invalidated the scale proposal.

```powershell
go test -run TestTheNoveltyPassCostAt1080pIsMeasured -v ./internal/novelty/
```

### What it re-derives (0.6.5)

At 25 decoded frames per second a camera spends **87.7 ms per second, or 8.8% of one
core**, on novelty alone.

| novelty rate | novelty cost, 2,000 cameras | detector cost, same | total |
|---|---|---|---|
| 2/s (the detection rate) | 12.0 cores | 10.2 cores | 22.2 cores |
| 25/s (the decoded frame rate) | **150.0 cores** | 127.5 cores | 277.5 cores |

**Both numbers belong in the proposal, or neither.** A rate that changes the answer by
12.5x is a rate that matters, and a scale claim that quotes the cheap row without the
expensive one is a choice rather than a fact.

Note the direction: at the decoded frame rate the **cheap pure-Go half is the expensive
half**. The crop pass inverts the cost model the scale proposal assumes, which prices
one detector inference per sampled frame and charges novelty nothing.

```powershell
go test -run TestTheCostProjectionNamesBothRates -v ./internal/novelty/
```

---

## 0.6.5 The novelty rate decision

**Decided: novelty runs at `SP_POC_NOVELTY_FPS`, which defaults to 0 = every decoded
frame. The default is stated, not silent, and the run prints what it used.**

### The reasoning

A bird at 30 m is on screen for about **1.5 s**. The crop pass is what finds it, and
cropping happens only where novelty **proposes**. So the rate is not a cost knob: it
decides whether the headline case works.

The smallest object needs **three proposals** in 1.5 s. That is **2 per second**, which
is the floor and not a margin, and it is reached only at the optimistic end of the dwell
estimate. At 2 fps a bird is proposed roughly twice, and "roughly twice" does not survive
a room with a CV reviewer in it.

So:

* **`SP_POC_NOVELTY_FPS=0` runs the pass on every decoded frame.** At the measured
  3.5 ms that is 8.8% of one core per camera, which for a one-camera POC is a price
  worth paying to remove the question.
* **A named rate is validated against the dwell.** `NoveltyRateCoversDwell` refuses a
  rate that cannot propose a region three times in 1.5 s, **by name**, with the sample
  shortfall in the message. `SP_POC_NOVELTY_FPS=1` is refused and the message says a
  1.5 s dwell would be proposed **once**.
* **The gate reports what it achieved.** A gate can only fire on a frame boundary, so a
  rate above half the decode rate cannot be met exactly. The interval is rounded
  **down** to a whole number of frames so the gate is never slower than asked, and the
  report prints both the requested rate and the achieved one.

### Why the rounding is downward, and not symmetric

At 25 fps a 10/s request rounds to 8.4/s if the interval is rounded up, and to 12.5/s if
it is rounded down. The two errors are not symmetric:

* **Over-shooting costs arithmetic and finds more.** Novelty is pure Go and its output is
  a proposal.
* **Under-shooting silently narrows** the set of regions that get cropped, and the
  smallest object the system exists to report is exactly a region the gate did not
  propose. That error shows up on a cost projection; this one does not show up at all.

```powershell
go test -run "TestTheNoveltyRateMustCover|TestAGateSays|TestTheGateRuns" -v ./internal/novelty/
```

---

## 0.6.1 The source-aware minimum

**Decided: the minimum is per SOURCE, not global. `SP_POC_CROP_MIN_FRAMES` defaults to
2; a whole-frame track still needs `SP_POC_TRACK_MIN_FRAMES`, which is 3.**

Two frames of a bird at 8 px are not two frames of a bird at 100 px. The second is a
structurally more confident observation, produced by a model looking at a magnified crop
rather than at a downscaled whole frame.

**Lowering the global minimum to 2 would have been wrong.** It re-admits exactly the
single-frame shadow and reflection noise the rule was added to stop, and that would be
visible in the false-positive rate. The whole-frame control test asserts this directly: a
two-sample bird found whole-frame produces **nothing**.

The cross-check that was missing is now a refusal. `SP_POC_TRACK_MIN_FRAMES` and
`SP_POC_AI_FPS` were validated independently and behaved as if no relationship had ever
been written down, so a combination in which the minimum exceeds the samples a dwell can
produce was accepted, and a run in that state reports a clean zero detections with no
error because the rule erasing the objects is working exactly as written.

```powershell
go test -run TestTheMinimumCanNeverExceed -v ./internal/config/
```

---

## 0.6.2 The verification budget

**Recorded, not scheduled.** The cap-exhaustion path was already explicit and is
unchanged: an event that was proposed, budget-refused and never asked is `not_asked`, not
`degraded`. A budget refusal is not a provider failure and the two have different owners.

**Still to do, and it needs footage:** measure the event rate of both modes on the same
clip and publish both. The number is the argument for the cap's existence, and a cap that
holds for a two-camera demo is not a cap that holds for 2,000.

---

## 0.5.4 Whole-frame against crop, measured on this machine

This is the plan's central claim, and it is now a **measurement** rather than a
derivation. It needs no camera, no footage and no licence — only the weights file — so
anybody can check it.

| path | a 40 px object arrives as | against the 16 px floor |
|---|---|---|
| whole frame, 1920x1080 → 416x416 | **8.7 px** | **below it** |
| 200x150 crop → 416x416 | **83.2 px** | well above it |

**Factor 9.6.** Model input 416x416 NHWC, content 416x234, pad 0/91 — the letterbox's
own geometry on this machine's build of it.

```powershell
$env:SP_POC_ORT_LIB='models\onnxruntime.dll'
$env:SP_POC_DET_MODEL='models\yolov4n.onnx'
$env:SP_POC_AI_ANCHORS='models\yolov4n.anchors.txt'
$env:SP_POC_DET_INPUT='416'
go run ./cmd/pocctl detector --selftest
```

### What this row does and does not claim

It claims the **arithmetic**: that a whole-frame pass delivers a bird at 30 m as 8.7 px
and that a 200x150 crop delivers the same bird as 83.2 px, and that only the second is
above the floor at which a detector is reliable. It is computed from the letterbox's own
scale, not from a second copy of the ratio, so it cannot drift from the code.

It does **not** claim the detector finds the object. The frame is synthetic, and a row
that said "bird detected" from a synthetic square would be the most dishonest line this
command could emit. `NOT RUN` is what it prints for the coordinate row here, with the
reason, because the in-process decoder reads only fused outputs and this export is an
anchored multi-scale head — a real limitation of the in-process path, named rather than
worked around.

### And the cost, which is a property of the region rate

Measured on this machine, **CPU only** (`SP_POC_ORT_EP=cpu`, 4 threads):

| | |
|---|---|
| whole-frame inference | **~198 ms median, 199 ms p95** |
| sustainable rate | about **5 inferences/second** |

**This is the CPU number and it is not the plan's number.** The cost model assumes a
**17 ms GPU inference**, and `docs/JUDGE.md` records that 17 ms as **measured on an
RTX 4070** (p95 19 ms, ~52/s), not as a target. This run asked for the CPU and
therefore measured CPU cost, roughly twelve times higher. Both are real; they answer
different questions, and every latency in this repository carries the provider it was
taken on rather than letting the cheap one stand in for the expensive one.

What follows from it is not "the idea needs a GPU" but the more useful shape: **the crop
pass doubles the inference cost of a frame that proposes a region**, so affordability is
a property of the region rate rather than of the mechanism. At one region per frame it is
two whole-frame passes; at a quarter of a region per frame it is a quarter again. That
is what `SP_POC_CROP_MAX_REGIONS` and `SP_POC_NOVELTY_FPS` exist for, and both print the
rate they achieved.

Every "a crop should cost at or below a whole-frame pass" statement in the original plan
is therefore **NOT RUN** on CPU rather than measured, and is not quoted anywhere.

---

## 0.6.3 Detection quality

**Not yet measured.** Per-class precision and recall on owned footage using
`testdata/labels.json` and `internal/evaluation`, with `bird` and `car` on separate rows
**even when one of them is a poor result**.

When it is published it must say plainly that this is **not mAP and cannot be**. One
minute of footage with a handful of labelled instances supports a precision and a recall;
it does not support a mean average precision. The harness's 200-case floor is about the
**false-positive** rate, which is a different measurement, and both numbers appear
together so neither is read as the other.

---

## 0.6.4 The view-change arbitration

**Decided and built: a view change invalidates every track and every baseline, declares
ONE camera-integrity event, and suppresses presence until the view settles, with the
suppression COUNTED.**

A camera being moved produces an enormous novelty spike, which produces tracks, which
produce events, which read as "everything passed at once". The camera-integrity event is
correct *and* the system is simultaneously reporting a storm of false presence. That is
worse than reacting to nothing, because the operator has been told the camera moved AND
that a hundred things happened, and the second of those is false.

### The signal, and why it costs nothing

The novelty engine **already measures** the two numbers that tell a moved camera from a
crossing:

* **`Score.Illumination`** is the frame-wide brightness offset `robust()` already removed
  before thresholding. A camera panning away from a wall changes every pixel at once. A
  car crossing does not.
* **`Score.Spread`** is how widely the residual is distributed afterwards. A moved camera
  leaves structure everywhere; a vehicle leaves it in one place.

Brightness alone is not enough — a cloud crossing a window does that without moving the
camera. Spread alone is not enough — a global compression change does that. Both
together, sustained over two frames, is a view change and nothing else.

**Two frames of sustain, because one is the false positive the anti-jitter rule exists to
reject.** Applying a different standard to the same phenomenon is how a pipeline ends up
with a jitter rule and a no-jitter rule.

### What it costs

Nothing per frame. It reads a score the crop gate already computed.

### The counters, because a gate with no counter is silence

| Counter | What it is for |
|---|---|
| `view_changes` | how many times the camera moved |
| `view_tracks_dropped` | **how many things the pipeline threw away because it moved** |
| `engine.SuppressedEvents` | how many presence events were withheld |
| `novelty.ViewChange.SuppressedFrames` | how many frames the gate was open |

A camera moved once and 14 tracks dropped is a different run from a camera moved once and
nothing dropped, and only the second figure tells an operator what their event stream
just did. Silence here would read as "nothing happened", which is the one sentence a
security pipeline must never be allowed to say.

```powershell
go test -run "TestOneCameraMove|TestACarCrossing|TestASuppressedEngine|TestInvalidateAll" -v ./internal/detect/ ./internal/novelty/
```

---

## 0.5.2 - 0.5.4 Still to measure

These need the GPU and the owned footage, and both are Phase 0 items that have not
happened yet:

* **0.5.2** crop pass cost against the 17 ms whole-frame pass. "Should be at or below" is
  not a measurement, and the whole plan rests on the crop pass being affordable.
* **0.5.3** end-to-end throughput with video in the loop, which is the table iCIT
  publishes.
* **0.5.4** detection rate on real footage at both settings, once whole-frame and once
  through a crop. **The whole-frame pass is expected to miss the bird.** Measuring that on
  the real clip before the demo is what turns the pixels-on-target arithmetic from a
  derivation into a demonstrated result.

**A measured number that turned out inconvenient is worth more than a promising one that
was never checked.** None of these three is quoted anywhere until it is measured.