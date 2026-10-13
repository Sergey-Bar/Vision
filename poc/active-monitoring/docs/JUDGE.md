# Judge script — five minutes

Everything a judge needs to know is here in the order they need it. The build is
`poc/active-monitoring/`, it is a nested Go module, and this file is the only
document you have to read before asking a question the source cannot answer.

The one-line version: **a security camera asks a vision model whether an alert is
real, refuses to push the model's answer without the model naming itself, keeps a
signed record of what it pushed, and proves that record catches an edit.**

---

## 0. Before you press anything (30 seconds)

```powershell
cd poc\active-monitoring
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"
```

The path line is in every document in this repository, and it is there because all
five tools it names are installed and none of them is on `PATH` on a default Windows
shell. A script that reports "go: not found" is telling you about the shell, not about
the build.

Three commands, in this order:

| # | command | what it does | if it takes too long |
|---|---|---|---|
| 1 | `go build ./...` | compiles everything | — |
| 2 | `pocctl demo --clip testdata\car-crossing.mp4` | runs the whole story on a labelled clip, against a local VMS simulator, in about 90 seconds | stop it, read §1 |
| 3 | `pocctl audit ledger --tamper` | changes one byte of one ledger row, watches the chain fail, puts it back | — |

Number 2 is the demo. Number 3 is the proof the demo is not theatre. Everything
after this is for the questions.

---

## 1. The demo, narrated (90 seconds)

`pocctl demo` prints seven steps. Here is what each one is actually showing, because
a judge watching output nobody explains is a judge watching output.

| step | what happens | why it matters |
|---|---|---|
| 1 | a synthetic clip is played and faked as a live camera | the demo says so on every line. It is not a camera. |
| 2 | the local ONNX detector finds boxes **with class names** | the detector reports `car`, `person`, `bird` and a score for each. The class is the model's own output, not a per-camera list: `SP_POC_DET_MODEL` names the weights and the stored decision carries the class. |
| 3 | the object is tracked, and **presence mode reports it once** | `SP_POC_ZONES` is unset, so there is no line to cross and nothing to configure. A tracked object that holds still long enough is reported once, with its class and its measured box. |
| 4 | a clip is cut, stored, and **read back** | the clip's bytes are hashed on the way in and hashed again when read out. A clip that did not survive the round trip is refused. |
| 5 | Hafnia's VLM is asked, and **told what the detector found** | **the only network call in the run**. The prompt states whether the clip came from a change measurement or from a detection, and substitutes the detected class: `alarm.v3.txt`, `{{class}}`. |
| 6 | the verdict is stored with the model's own name | the whole product is in this step |
| 7 | the event is pushed to Milestone XProtect | and read back |

Two numbers matter at step 7 and both are labelled on screen:

- **`NOT RUN` / `PROVISIONAL`** if the run was shorter than its target. This module's
  vocabulary is `PASS` / `FAIL` / `NOT RUN` and nothing else. A run short of its
  target is `PROVISIONAL`, which is not a soft pass — it is an admission that the
  acceptance arithmetic was not evaluated.
- **`provenance: synthetic`** beside every number derived from the clip. The demo clip
  is colour bars and a rectangle. It is not evidence that this system works on a real
  camera; it is evidence that the pipeline is honest about what it measured.

---

## 2. What the VLM call actually sends (the sentence in the plan)

Hafnia's `chat/completions` endpoint takes text plus images. This module sends the
**prompt and the measured region**, and the prompt pins a closed four-field answer:

```
verdict, summary, confidence, insufficient_evidence
verdict ∈ { confirmed, false_positive, needs_review, none }
```

The decoder **refuses** anything else. A fifth verdict does not become a new word on
a screen an operator acts on — it becomes `cannot_tell` with a reason. That is the
first thing to ask about, and the answer is `internal/vlm/parse.go`.

You will be asked: **"isn't trusting a VLM's verdict the whole problem?"**

No, because the verdict is a *claim*, and a claim that cannot be checked is not a
finding. Everything downstream of this point exists to make the claim checkable:

- the answer is stored with the model identity **as the provider stated it**, the
  contract version it was produced under, and the hash of the validated output
- the pushed event carries all of it, so the operator reading Smart Client sees who
  said what
- the whole tuple is hash-chained and signed, so it cannot be quietly revised later

There is **no model hash** and there never will be: this is a hosted model, it has no
weights to hash, and inventing a field would be exactly the failure R8 exists to
prevent.

---

## 3. The third incident class: the camera was moved (45 seconds)

This is the one to lead with if you only show one thing, because it is the one a
security buyer has not seen from anyone.

XProtect has a `camera_integrity` state: the camera view moved. Somebody carrying a
steering wheel, someone covering the lens. **The camera reports nothing when this
happens** — it is a silent hole in coverage.

```powershell
$env:SP_POC_CAMERA_INTEGRITY = 'true'
pocctl demo --clip testdata\car-crossing.mp4
```

The `--clip` is not optional. `pocctl demo` has no default clip on purpose: the
clip it used to default to is a real night intersection whose `labels.json` row
says it may not be shown outside this machine, and a demo is shown to people.
Name the material you are showing; the command refuses rather than choosing for
you, and prints the provenance table so the choice is informed. The committed
synthetic clip is what to use here — see §1 for what a run over it proves and
what it honestly cannot.

What it does that nothing else does:

- pins a **reference view** of the camera at setup and scores every frame against it
- fires only when the whole view has stayed different for a *sustained* period, which
  is what separates a moved camera from a car driving through the shot
- the prompt asks **how**, not whether: `moved` / `covered` / `obscured_scene` /
  `unchanged`
- and the pushed event carries the measured box of what changed

The test that makes it more than a claim:
`TestAReferenceScoreDoesNotRiseOnAMovingObject` — a car driving through the frame
does **not** raise the score. A moved camera does.

### And the part nobody else does: it does not flood you with the aftermath

A moved camera is the one event where a naive pipeline produces the worst possible
output. The pan is a huge novelty spike, so it makes dozens of tracks, so it makes
dozens of presence events, so the operator is told **the camera moved** *and* **a
hundred things happened**, and the second of those is false. A system that does that is
worse than one that reacts to nothing.

This one says **one thing** and then counts its own silence:

| what it does | why |
|---|---|
| **invalidates every track** | every track describes a position the camera is no longer looking at, and IoU does not know the camera moved — a wall at x=0.1 is still at x=0.1 in the new view's noise |
| **discards the frame's proposed regions** | a crop taken mid-pan describes a place that no longer exists |
| **suppresses presence until the view settles** | and the suppression is **counted**, because a gate with no counter is silence |
| **needs two consecutive agreeing frames** | one frame of a wide change is a door opening, and the single-frame rule is exactly what the anti-jitter rule elsewhere already rejects |
| **needs BOTH a brightness shift and a wide residual** | a cloud crossing a window shifts the whole frame's brightness without moving the camera |
| **works on numbers the novelty engine already computes** | `Score.Illumination` is the offset `robust()` already removed, `Score.Spread` is how wide the residual is. Zero extra cost per frame. |

The counters are the part to ask about: **how many tracks did it drop**, and **how many
frames did it suppress**. A camera moved once and 14 tracks dropped is a different run
from a camera moved once and nothing dropped, and only the second number tells you what
your event stream just did.

```powershell
go test -run "TestOneCameraMove|TestACarCrossing|TestASuppressedEngine|TestInvalidateAll" -v ./internal/detect/ ./internal/novelty/
```

---

## 3b. The smallest object, and the arithmetic behind it (60 seconds)

The claim: **a bird at 30 m is invisible to a whole-frame pass and visible through a
crop.** It is not a tuning claim. It is arithmetic, and you can check it yourself on any
machine with the weights file — no camera, no footage, no licence.

```powershell
$env:SP_POC_ORT_LIB      = 'models\onnxruntime.dll'
$env:SP_POC_DET_MODEL    = 'models\yolov4n.onnx'
$env:SP_POC_AI_ANCHORS    = 'models\yolov4n.anchors.txt'
$env:SP_POC_DET_INPUT    = '416'
pocctl detector --selftest
```

```
PASS  crop pass: whole-frame vs crop, delivered size
       a 40 px object arrives at 8.7 px through the whole-frame pass and 83.2 px
       through the 200x150 crop, a factor of 9.6.
```

**8.7 px is below the floor at which any detector is reliable. 83.2 px is not.** That is
the whole of it, and the row is computed from the letterbox's own scale rather than from
a second copy of the ratio, so it cannot drift from the code.

A whole-frame pass squeezes 1920x1080 into 416x416 — a 4.6x downscale — and throws the
extra resolution away **before the model sees it**. That is why a 4K camera is not a
small-object camera through this path: the same bird at the same distance arrives at the
same 8.7 px. Only the crop converts pixels into detection capability.

What this row does **not** claim: that the detector found the bird. The frame is
synthetic, and `NOT RUN` is what it prints for the coordinate row here, with the reason.
A row saying "bird detected" from a synthetic square would be the most dishonest line
this command could emit.

**And here is the number from THIS run, which was configured for the CPU:**

```
median 198 ms, p95 199 ms  on provider "cpu"
```

The cost model assumes a **17 ms GPU inference**, and the table further down records that
17 ms as **measured on an RTX 4070**, not as a target. This particular run asked for the
CPU — `SP_POC_ORT_EP=cpu` — and therefore measured CPU cost, twelve times higher. Both
numbers are real; they are different questions, and the run says which provider each was
taken on rather than letting the cheap one stand in for the expensive one.

The point to take from it: **the crop pass doubles the inference cost of a frame that
proposes a region**, so its affordability is a property of the *region rate*, not of the
idea. At one region per frame it is twice a whole-frame pass; at a quarter of a region
per frame it is a quarter again. That is what `SP_POC_CROP_MAX_REGIONS` and
`SP_POC_NOVELTY_FPS` are for, and both print what they achieved.

We would rather show you the 198 ms than hide it behind the 17 ms.

### And the counter that says whether the gate was looking at all

The region rate is only half the affordability question. The other half is **how often
the gate ran at all**, and until this pass that number existed nowhere: the crop
gate's own comment says its gated-out counter "is the counter that says whether the
rate is too low", and the counter was accumulated on every frame and read by nothing.

The report now has a section for it — **`## When the run decided not to look`** — and
it reports a share and not a count, because 40 gated-out frames means nothing on its
own and 40 out of 42 is a camera that is not looking most of the time. On this
machine's demo clip the number is at the top of the report; on a run whose crop pass
never ran it says `NOT RUN` rather than printing a zero, because a zero there would
read as "the gate looked at every frame".

The reason this matters more than it looks: a gate that skips most frames cannot
propose regions often enough for the 1.5-second dwell the crop policy needs, and the
only other symptom is that **the smallest objects quietly go missing**. Nothing else
in the report shows that. It is the arithmetic in §3 above, turned around: the cost
model tells you what a region costs, and this tells you whether you were offered any.

---

## 4. The tamper proof (30 seconds)

```powershell
pocctl audit ledger --tamper
```

It changes **one byte** of **one row** of the decision ledger, on your own data root:

```
BEFORE   PASS   57 rows from sequence 0 to 57 recomputed and chained
TARGET   seq=57  kind=push_enqueued  subject=d-...
         payload: "...score=0.700000"    ->   "...score=0.700001"
DURING   FAIL   row 57 is the first one that does not chain
AFTER    PASS   57 rows from sequence 0 to 57 recomputed and chained
```

Watch what it does *around* the edit, because that is the design:

- it **refuses** to run against a chain that is already broken. Breaking a second one
  hides the first, and the one already there is the one to look at.
- it drops the two append-only triggers first, because that is what a person with a
  `sqlite3` prompt does. The triggers exist to stop the accidental edit during an
  incident review, not the deliberate one.
- it **restores** the byte and the triggers, and then verifies a third time. A
  demonstration that leaves the evidence broken is not a demonstration.
- it prints `FAIL` for the broken state and nothing softer.

If you ask *"why can you edit the database at all?"* — that is the honest answer: this
is not a claim that the database is tamper-proof. It is a claim that an edit is
**detected, located by row number, and survives a restart**. `onnxruntime` is not
involved; this is SQLite.

---

## 5. The absence path: what happens when the model cannot be reached

Every one of the four defect modes the plan lists, and the behaviour for each:

| defect | what a naive system does | what this one does |
|---|---|---|
| provider 503 | pushes "no incident" | records `degraded`. **A missing answer is never a negative one.** |
| latency spike | pushes "no incident" | records `degraded` |
| provider down | pushes "no incident" | records `not_asked` |
| clip missing | pushes with no evidence | records the clip as absent |
| response unparseable | pushes a default | records `cannot_tell` |

There are exactly **five** words for "we could not decide" and this module will not
invent a sixth. Three of them — `not_asked`, `degraded`, `cannot_tell` — get their own
Milestone Analytics Event Type, so *camera not assessed* is a **filterable XProtect
alarm** rather than a word in a description field.

And the arithmetic, which is the part worth asking about:

> If the model is unreachable for an hour and 500 crossings happen, that is **one**
> `not_asked` alarm carrying the number 500. Never 500 alarms.

That is `internal/soak/absence.go`, and the test names the arithmetic:
`TestFiveHundredUnaskedProposalsProduceOneEvent`. Each crossing still gets its own
XProtect event with its own measured box — **the crossing happened; only the verdict
is missing** — and the blindness is the thing that gets aggregated.

What this does **not** claim: the analytics event types are not registered on any real
Milestone instance. `SP_POC_EVENT_TYPE_GUIDS` maps them, and an account without the
admin rights to register them gets a refusal naming the pair and a window that stays
visible in `pocctl brief` as unsearchable. Nothing is pushed as if it were a crossing.

---

## 6. Prompt injection

Worth raising yourself, because you will be asked and because nobody in this category
does.

```powershell
go run ./cmd/injectionfixture     # regenerates testdata/injection-hostile.mp4
go test ./internal/vlm/ ./internal/injectionfixture/
```

The fixture has four different attacks **rendered into the picture** — not pasted into
a prompt, which would not be an attack at all. They differ in register on purpose: an
imperative, a fake verdict policy, an attempt to set the negative case, and an
impersonation of the operator. A banned-word list catches `ignore` and misses the rest.

Both prompts were revised (**v1 → v2**) to say that text in the frame is *evidence*,
never an instruction, and to quote the attacks by their own words. The revision
reason is in `internal/vlm/prompts/manifest.json` and it names the fixture, so the
evidence for the change is findable from the change. Because a prompt revision is part
of `contract_version`, an answer produced under v1 cannot be compared silently with one
produced under v2.

**What this proves, exactly:** the output shape is closed, so an injected instruction
cannot introduce a fifth verdict; an injected sentence that *does* come back reaches
the operator as the model's own words beside its output hash, which is the opposite of
an instruction.

**What it does not prove:** that any particular model refuses to comply with that text.
That needs a live call with the frame attached. **A green test here is a statement about
the shape of the answer, not about the model's judgement**, and this repository does not
claim otherwise. If you ask "have you actually run this against Hafnia?" the answer is
no, and the report will say `NOT RUN`.

---

## 7. The gate (30 seconds, run it if you want to be unimpressed)

```powershell
.\scripts\verify.ps1            # or -NoRace
```

`verify.ps1` is the gate. It builds, vets, checks formatting, checks the module
boundary, runs `go test -race -p 1 -count=1 ./...`, and then — this is the part worth
watching — **it deletes a line of production code, one control at a time, and checks
that the suite goes red.** Every time. Then it puts the line back.

| removed | the suite must then fail, because |
|---|---|
| the SHA-256 shape check | a reader-chosen name can be any object, and two bugs can point at one |
| the terminal-state guard on a detection | a hidden detection can be moved again and one crossing gets two stories |
| the novelty tolerance band | every frame is "on the line" — a run reports **zero events and no error** |
| the VLM's "we did not ask" | the operator sees "nothing happened" because *we* could not reach it |
| the ledger's hash comparison | "the ledger is intact" becomes an opinion |
| the ledger's append-only triggers | a person with `sqlite3` edits a decision already recorded |
| the reference frame | the "photo" is a real frame from the same camera at a *different* moment |
| the box on the wire | every pushed event draws nothing on the operator's timeline |
| a frame's MEASURED marker | "the model saw nothing" and "the pipeline never asked" become one row |
| an open-ended ledger range | the audit prints "the ledger is empty" beside a verified chain |
| the missing-state refusal | an event cannot be read as a finding or a gap |
| the attribution refusal | a caption arrives with nobody named as its author |
| a blindness window's key | the aggregated alarm loses its idempotency; a retry becomes a second event |
| an aggregated alarm's key | the module reports `unknown` for a push that succeeded |
| the `no_objects` refusal | an alarm that fires to say "nothing" arrives hundreds of times an hour |
| an unmapped absence pair | an absence arrives wearing a crossing alarm type |
| a CUDA spelling | a CPU latency is recorded as a GPU one |

The gate prints `SKIPPED` for anything it could not measure on the machine it ran on —
it never passes silently.

---

## 8. The questions you should ask, and the honest answers

**"What is your accuracy?"** Not measured against real footage. The committed
material is synthetic; `testdata/labels.json` records that on every clip, and the
report prints `provenance: synthetic` beside every number. The `false-positive` rate is
`NOT RUN` until a person marks which detections are real.

**"Does it work with a real camera?"** The source is RTSP-first and the relay/simulator
path is exercised end to end. A real camera has not been run in this build. The
architecture does not care — the source is a camera GUID and the frames — but claiming
it has been demonstrated on real footage would be false.

**"What if Hafnia changes its model?"** Then the model identity changes, the
`contract_version` changes, and every stored decision says which version produced it.
Old answers stay readable and are not silently comparable with new ones. This is the
provider's contract and this module's response to it; it is not a claim that a hosted
model is stable.

**"Why not just search the footage later?"** `pocctl search` refuses to rank and prints
why on every run: a similarity-ranked answer is not replayable from stored facts, so
there are no embeddings here. What it gives you is an interval of instants and a count
of what was actually decided about them — and an honest `cannot_tell` where there was
no answer.

It searches **across runs and across cameras** (`--runs a,b,c`, `--camera cam-north`),
because every card names the run it came from, the run's label and its source. A run has
its own model identity and its own contract version, so answers from two runs are not
comparable unless both are on the card — the same reasoning that refuses to compare a v1
answer with a v2 answer.

**"What is the number on the card?"** The model's **own** confidence in the verdict it
gave, printed beside the verdict and never instead of it: `87% - confirmed (bird)`. It is
`vlm_observations.confidence`, a stored column, so it recomputes from stored facts
because it IS a stored fact. It is **not** a similarity and never renders as one, because
this surface has no embeddings to produce and would be lying if it printed one.

A card with **no answer shows no number**. `not_asked`, `degraded` and `cannot_tell`
render as their absence word and a reason. A percentage against a row nobody asked about
is a fabricated measurement, and it is the easiest way for this surface to become the
thing this repository exists not to be.

**"What if the search finds nothing?"** Then the screen says **which** kind of nothing.
`pocctl unsearchable` (or `pocctl search --unsearchable`) lists the stretches of the run
that **could not be answered**, with the reason and how many frames were involved:

```
windows run r7 could not answer, longest first
  2026-10-01T15:04:10Z .. 2026-10-01T15:05:02Z  (52.0 s)
    12 proposal(s): 12 not asked, 0 asked and degraded, most common reason "quota"
```

That is the question a reviewer actually asks about a system running without a cloud: not
"how accurate were you" but **"what did you not know, and when"**. A count of zero with
no second is a negative answer that was never given.

**"Can I find what my reviewer concluded?"** Yes, and that is the highest-value line in
the surface. The text search covers the model's stored sentence **and** the reviewer's
ground-truth note, so somebody who wrote *"that is the barrier shadow, not a stopped
car"* can find it by typing three words. The card then carries the verdict, **who**
concluded it, and the note, because "this is wrong" from a named person is evidence and
the same sentence from nobody is an opinion.

**"What does it cost to run?"** The numbers are on screen during the demo and they name
the thread count and the execution provider. A latency without its thread count and its
device describes no build.

**"Why did you build it for CPU then?"** Because that is what was available, and because
the module refused to pretend otherwise. Every latency in this system is recorded with
the execution provider it was measured on, and `SP_POC_ORT_EP=cuda` is **refused before
the session opens** unless `nvidia-smi` reports the configured device — ONNX Runtime
falls back to the CPU, logs it and returns success, so a run that asked for a GPU and got
the CPU would produce real numbers under a label that says otherwise. That is the exact
shape of defect this repository exists to end.

A 4070 was then available, and it is **measured, not `NOT RUN`**:

| provider | median | p95 | sustains |
|---|---|---|---|
| `cpu`, 4 threads | 241 ms | 265 ms | ~3 / s |
| `cuda`, RTX 4070 | **17 ms** | **19 ms** | ~52 / s |

Same model, same machine, same thread count, first frame excluded. You can run it:

```powershell
$env:SP_POC_ORT_LIB='models\onnxruntime.dll'; $env:SP_POC_DET_MODEL='models\yolov4n.onnx'
$env:Path += ";$((Resolve-Path models).Path)"
$env:SP_POC_ORT_EP='cuda'; pocctl detector --selftest --frames 30
```

The interesting part is not the 14x. It is that **the GPU made two silent defects
fatal**, and both were the same shape as everything else in this repository is about: a
fact the module recorded in every artefact and did not act on.

- **The committed detector could not be run at all.** It declares its output dimensions
  as `-1`, and the detector pre-allocated every declared output and refused the model
  when it could not — with a message phrased as protection. It was declining every modern
  YOLO export.
- **The input tensor was laid out backwards.** The session worked out the layout from the
  model's declared dimensions, recorded it in the report and in every screen this
  repository shows, and then fed the tensor in a fixed order regardless. The visible
  symptom was an error. The dangerous one is a tensor of exactly the right size fed in
  the wrong order, which decodes to boxes that are plausible and wrong and errors on
  nothing.

Neither had a test, and that is the finding: the path from "the model declares X" to "the
module does X" was untested in both directions. Opening one session found both.

---

## 9. Where the detail went

| was | is now |
|---|---|
| `docs/HOW-TO-RUN.md` | this file, §0𗜧1 |
| `docs/verify-live.md`, `docs/demo-live.md`, `docs/one-pager.md` | this file |
| `docs/architecture.md` | `README.md` |
| `docs/gap-ledger.md`, `docs/STATUS.md` | nothing. A hand-maintained status file goes stale the day somebody forgets it, and the honest substitute is generated: `CONTRACT.md` for the rules, `README.md` for the real-run findings, and the SKIPPED block the gate prints for what it could not measure |
| `docs/vss-mapping.md` | nothing. There was no mapping behind it — the four families are `internal/classify`'s closed set over COCO-80, and calling that VSS 4.2 without a per-class table would be a claim this module cannot make. `internal/classify/classify.go` is the vocabulary, and it is in the source a judge can read |
| `docs/provenance.md`, `docs/probes.md`, `docs/findings.md` | `README.md` (real-run findings), `testdata/labels.json` and `pyworker/fixtures/*.licence.json` (provenance), `pocctl probe` (contract confidence, live only) |
| `CONTRACT.md` | unchanged — it is the law, not a description of it |

Two of these rows say "nothing" and that is the finding, not an omission. Both files
were linked from the submission documents and neither had ever been written.
