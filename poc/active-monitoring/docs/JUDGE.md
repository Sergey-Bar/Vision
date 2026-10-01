# JUDGE.md — five minutes, and what each step is actually proving

This is the document to give somebody who has to decide whether this works, in
five minutes, without reading the source.

It replaces `docs/verify-live.md`, `docs/demo-live.md` and `docs/one-pager.md`.
Those three disagreed with each other in places and two of them made claims about
the machine that were false — one of them asserted `mediamtx` was not installed on
a machine where it had been installed for months. A document that is wrong about
its own prerequisites is worse than no document, because it is trusted.

Read the FAILURES. Every step below says what it proves **and** what it does not,
because the second half is where the interesting claims live.

---

## 0. Before you start

```powershell
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"
cd poc\active-monitoring
```

Three prerequisites, all hard: Go 1.26+, `ffmpeg`/`ffprobe` on PATH, and a Python
interpreter. `gcc` is needed only for the race detector — without it the gate
prints **SKIPPED** for that step and is otherwise green. A skip is not a pass, and
the gate says so in its summary.

---

## 1. The gate answers "is this thing internally honest?"

```powershell
.\scripts\verify.ps1
```

**What it proves.** It builds, formats, vets, checks the module boundary, runs
every test under the race detector, runs the sidecar's Python tests, checks the
decoder's own parity harness and the benchmark's hand-computed arithmetic, and then
**removes twenty controls from the production code one at a time and requires the
suite to go red each time**.

**What it does not prove.** That the system finds vehicles on real traffic. It
proves the arithmetic holds and that the claims the code makes about itself are
load-bearing. Those are different claims and the gate is only about the first.

**Read the SKIPPED list.** The full cross-decoder comparison needs a running
sidecar and a downloaded model, and the ground-truth benchmark needs a network
fetch. On a machine without them the gate is **GREEN WITH SKIPS**, and a green
with skips is not a green.

There is no CI in this repository — Actions cannot start a job for it, because the
repository is private, on the free plan, and the billing state blocks it. So a
green branch here is a human claim. What command produced it is printed at the
bottom of the gate's output.

---

## 2. The detector answers "does it find anything at all?"

This is the step that matters, and it is the one the previous build could not run:
there was no detector. The last two real runs approved 6,578 and 297 boxes and
recorded **zero vehicles, zero people and zero animals** — not because the detector
was wrong, but because the trigger was a whole-frame change-fraction, which can
say "the picture changed" and nothing else.

```powershell
.\bin\pocctl.exe fetch-runtime --url <onnxruntime-release-zip-url>
.\bin\pocctl.exe fetch-model   --url <your-onnx-url>
.\bin\fixtureprobe.exe -detector qa\fixtures\crossing.mp4
```

`fetch-runtime` computes the library's SHA-256 and prints it. **Read the number
against the release page**, then re-run with `--sha256=` to confirm it. A digest
nobody checked is not a provenance record, it is a number.

`fixtureprobe -detector` prints the five facts that make a zero diagnosable:

```
DECLARED OUTPUT SHAPE  [1,84,8400] or [1,8400,84]
RECORD WIDTH           84, and whether column 4 is an objectness
RAW CANDIDATE COUNT    records before any filter
PRE / POST NMS          what each suppression pass removed
PER-CLASS COUNTS       which class the boxes actually carry
```

**What this proves.** Whether a model, with this export, produces a box on this
material. **What it does not prove:** anything about your footage.

**If it prints ZERO BOXES**, it then says the four things to check in order: the
output shape (a fields-first tensor read as fields-last gives records of the wrong
width), the objectness column (reading an 84-field record as 85 shifts every class
by one, so a `truck` arrives as a `bench`), the coordinate space (a pixel-space
model read as fractions draws every box 640× too small), and the emit floor (it is
deliberately low, so a model emitting nothing means the weights are wrong, not the
threshold).

---

## 3. The demo answers "does the whole thing work end to end?"

```powershell
.\bin\pocctl.exe demo
```

Three minutes against a local simulator, on a synthetic clip. **What it proves:**
the pieces are wired together and a crossing is detected, snapshotted, recorded,
pushed and reported. **What it does not prove:** that any of it works on your
camera. The material is a white block on a black frame.

It also **refuses** rather than falling back if the model or the runtime library is
missing — and that refusal is the important part. A fallback to the novelty trigger
would produce a run reporting zero vehicles at full health, with every number in it
true.

---

## 4. The numbers answer "is it good enough?"

There is no honest number yet. Two of the three cannot be produced without work
that has not been done:

| number | needs | status |
|---|---|---|
| throughput (det_p99 vs frame interval) | `fixtureprobe -detector` over real frames | obtainable now |
| **precision** | a human marking each box: `pocctl label --run <id>` | needs a marking session |
| **recall** | the same session, plus drawn objects the detector missed | needs a marking session |
| false positives per hour | the marking session, over real footage | needs a marking session |

`pocctl bench --run <id>` reports the last two as **NOT RUN** and names what is
missing. It does not report them as zero. A false-positive rate is a claim about
what a human would have dismissed, and only a human can say.

`pocctl label` serves a marking page on **loopback only** — it refuses any other
interface including `0.0.0.0`, because it serves the footage and your judgements
about it and neither belongs on a network somebody else is on. It has a drag
gesture as well as accept/reject keys: without a way to draw an object the detector
MISSED, a frame with no detection carries no information, and recall cannot be
measured at all.

---

## 5. The five questions, answered honestly

**Does it detect vehicles?** Not yet demonstrated. The code path exists, the
decoder is tested, and no real model has been run over real footage on this
machine. `fixtureprobe -detector` is the next step and it is one command.

**Is the code internally honest?** Yes, to the extent twenty red checks and ~1,900
tests can show. Every claim the code makes about itself has a control whose removal
turns the suite red.

**Can I check its arithmetic?** Yes. `pocctl prove --detection <id>` recomputes a
decision from stored facts with zero provider calls, and since the crossing box
became Kalman-filtered it re-runs the whole track from the per-frame box log.

**Does it cost what it claims?** Not yet measured on this machine. The throughput
gate refuses before a run rather than during one: `det_p99 × ai_fps < 1000`, with
the arithmetic printed. Four cores and no GPU.

**Can I redistribute any of it?** The code, yes. The material — no, not until
`testdata/labels.json` records it, and the current model file's licence is **NOT
RECORDED**. `pocctl fetch-model` and `fetch-runtime` print what they wrote and say
so when they cannot.

---

## 6. Where the detail went

| was | is now |
|---|---|
| `docs/HOW-TO-RUN.md` | this file, §0–§1 |
| `docs/verify-live.md`, `docs/demo-live.md`, `docs/one-pager.md` | this file |
| `docs/architecture.md` | `README.md` |
| `docs/gap-ledger.md` | `docs/STATUS.md` |
| `docs/provenance.md`, `docs/vss-mapping.md`, `docs/probes.md`, `docs/findings.md` | `docs/STATUS.md` |
| `CONTRACT.md` | unchanged — it is the law, not a description of it |