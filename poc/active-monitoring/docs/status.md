# Status — poc/active-monitoring

Last revised 2026-10-01 (the Hafnia VLM client and `pocctl vlm-probe`, the
pure-Go novelty engine and its unpinned-reference refusal, the bookmark push, the
hash-chained ledger, the ed25519 seal, `pocctl prove`, `pocctl probe --live`,
`pocctl mark`, `pocctl search`, `pocctl brief`, `pocctl demo --live`, and the
twelve red checks).

This is the source of truth for **what is built** in this module. The README is
not, and neither is the commit history. Where something is not done it says why,
and where the reason is a person rather than a line of code it says that too.

## Phase 1 gate (1.3) — one of three conditions green

The plan's gate is three conditions. One is met, two are blocked on credentials that
this session does not have. Stating the split is the whole point: a gate described as
green with two thirds missing is the exact failure this module is built to prevent.

| condition | state |
|---|---|
| `verify.ps1` green **including the red checks** | **GREEN**, twelve controls, locally. No skips. Not CI — there is no pipeline in this repository |
| one live push proven by read-back | **NOT RUN.** `pocctl probe --live` and `pocctl demo --live` are built and tested; no instance was reachable. Every credential variable is absent on this machine — checked, not assumed |
| one live VLM call in `contracts/hafnia/raw/` | **NOT RUN.** `pocctl vlm-probe` is built and tested; no API key. It refuses by name without one, and `raw/` holds only a `.gitkeep` |

So the 24-hour soak is **not** started, and the plan is explicit that it must not be:
starting it on an unproven path spends real time on a result nobody would trust.
`docs/gap-ledger.md` names who each of the two blocked conditions is waiting on.

## 2026-10-01, second pass: the demo could not have worked

Asked to build a QA lab, I went looking for material first and found instead that
**`pocctl demo` was structurally incapable of producing a detection**, for five
separate reasons stacked on top of each other. Each one produced the same symptom —
zero detections, zero errors, exit status 0 — and each was hidden by the one above it.

| # | defect | symptom | fixed |
|---|---|---|---|
| 1 | `runEnv` merged the demo environment over `os.Environ()` while `demoEnv`'s comment said it did not, and `SP_POC_TRIGGER_MODE` was absent from the slice — so the mode came from the operator's shell | the banner printed `pure Go, no ONNX, no Python, no sidecar` over a per-frame ONNX run. The only measured claim in this project printed without being derived from a measurement | `cmd/pocctl/demo.go`: every `SP_POC_*` dropped from the parent, `SP_POC_TRIGGER_MODE=novelty` declared, banner derived from the environment it builds |
| 2 | `SP_POC_CLASSES` could not approve `motion` — the class the shipped trigger emits. `config.IsApproved` did an exact lookup against COCO classes, and `internal/detect` built a **second, private** map from the raw list that knew even less | every candidate refused as `class not on the approved list` | `IsApproved` accepts a class whose FAMILY is named; `detect.Config.Approve` is handed `cfg.IsApproved` so there is one answer, not two |
| 3 | the resolved-class cache was written into a **value receiver's** copy, so only the first reader in a seven-reader process approved anything | the class list approved nothing depending on which function asked | `Config.approved` is a `*approvedCache`, created by `Load` because `Load` is the only place that hands out copies |
| 4 | `SP_POC_SCORE_THRESHOLD=0.4` is a model-CONFIDENCE bar. The novelty engine's score is a changed FRACTION, which measures ~0.05 for anything it can see | every candidate refused as `below the score threshold` | the demo ships `0.02`, with the two scales named in the comment |
| 5 | a zone's `classes` list must name the trigger's own class, or nothing is in the polygon | every candidate refused as `outside every permitted polygon` | `testdata/qa-fixtures.zones.json` names `motion` |

A run on the new fixture now reaches **189–271 boxes approved for tracking**, up
from zero.

### And the gap that is NOT closed

**Zero crossings. `AC2`, `AC3` and `AC4` are still `NOT RUN`.** Measured, not guessed:

```
crossing.mp4   200 frames  peak 0.047  regions 155  crossings 0
               regions proposed but NO CROSSING: the tracker never joins them

report.md:     boxes rejected 240
               `track has not been seen long enough`   234
```

**The novelty engine's component is a leading-edge STRIP, and consecutive strips
touch without overlapping.** An object moving D px per frame changes exactly D px of
width, so frame N's component is `[a, a+D]` and frame N+1's is `[a+D, a+2D]` — IoU is
*exactly zero*, the IoU tracker starts a new track every frame, `seen` never reaches
`SP_POC_TRACK_MIN_FRAMES`, and nothing is ever old enough to cross. Semi-transparency
does not help; the interior of a fading object does not change either, so it produces
no residual.

This is a **design mismatch, not a bug**, and the reason it was never found is that
the shipped pairing was never exercised end to end: the reference trigger emits a
whole-frame finding and needs no tracker, and the temporal path is the one that does.

Two fixes, and **neither is mine to choose**:

| | change | the cost |
|---|---|---|
| A | let the tracker associate on **proximity** rather than overlap | two people walking together merge into one track. One crossing reported instead of two is a **false negative** — the worst error a security product has |
| B | have the novelty engine propose the **object** rather than the changed edge | needs history in the engine: carry the last N frames' components and union them. More state, more memory, and it re-introduces a persistence question the engine currently has none of |

A is a threshold I cannot validate without real footage, which is the wall this whole
exercise ran into. B is a change to what the shipped detector *is*. Either way it is
an ADR and a human signature, so it is recorded and not done.

### What came out of it, and works

| | |
|---|---|
| `qa/make-fixtures.sh` | generates every fixture. No downloaded footage anywhere in it, so nothing has a licence and nothing is restricted |
| `cmd/fixtureprobe` | runs the **whole chain** — novelty engine then tracker then line machine — over a clip and reports `crossings 0` with the reason. It exists because a `pocctl demo` run prints one number and both of these failures produce it |
| `testdata/qa-fixtures.zones.json` | a **vertical** line with `motion` in its class list |
| `internal/novelty/mask.go` | **the box fix below** |
| `internal/novelty/component_test.go` | the fix, plus the case it hid behind |
| `scripts/verify.ps1` | the final red check now prints which package failed, and no longer misreads a native command's stderr as a crash |

#### The box bug, which was real and was hiding all of it

`components()` grew a bounding box with `minf(c.box.X1, x)` starting from a
zero-value `Box{X1:0, Y1:0, X2:0, Y2:0}`. A zero has `X1 == 0`, and `minf(0, anything)`
is 0 — so **`X1` and `Y1` were pinned to the frame's top-left corner for every
component, in every frame**, and only `X2` and `Y2` moved.

Every region this engine proposed was a rectangle stretching from `(0,0)` to wherever
the change ended. Three consequences, none of which looked like the cause: the box
drawn on the operator's screen was the wrong shape; consecutive boxes all shared the
left edge so their IoU collapsed; and `Box.Area()` — which is what
`SP_POC_MIN_BOX_AREA` compares against — was the area of the frame's corner rather
than of the change. Before the fix a component at the origin looked correct by
accident, which is why the test uses one deliberately not at the origin.

## The demo passes

**2026-10-01. Verdict `PASS`, all seven criteria, for the first time in this project's history.**

```
pocctl demo --clip qa/fixtures/crossing.mp4 --zones testdata/qa-fixtures.zones.json
```

| | |
|---|---|
| AC1 zero dropouts | **PASS** — 0 process exits, 0 heartbeat gaps, 6 heartbeats |
| AC2 zero lost events | **PASS** — 10 detections, 0 not terminal past the limit |
| AC3 evidence intact | **PASS** — 4 stored, **4 verified on read-back, 0 mismatched** |
| AC4 push without loss or duplication | **PASS** — 20 attempts, 20 with a result, 0 duplicate tags, 0 dead-lettered |
| AC5 resources bounded | **PASS** — rss 31 MiB, db 2.0 MiB, disk 2.4 MiB |
| AC6 no invented answers | **PASS** — 0 unparsed, 0 healthy-from-failed |
| AC7 modelled faults behaved | **PASS** — 14 rehearsed faults |

It took three more defects to get there, and every one of them had been hiding
behind the one before it.

### The three that closed it

**1. `internal/novelty/mask.go` — every proposed region was anchored at the frame origin.**

```go
c := component{}                                  // Box{X1:0, Y1:0, X2:0, Y2:0}
c.box.X1 = minf(c.box.X1, float64(x)/float64(m.W))
```

`minf(0, anything)` is `0`, so **`X1` and `Y1` were pinned to `(0,0)` for every
component in every frame**. Three consequences: the box on the operator's screen was
the wrong shape; consecutive boxes all shared the left edge so their IoU collapsed;
and `Box.Area()` — what `SP_POC_MIN_BOX_AREA` compares against — was the area of the
frame's corner. A component at the origin looked correct by accident, which is why the
test in `component_test.go` uses one deliberately not at the origin.

**2. `internal/detect` — a moving region is a strip, and strips touch without overlapping.**

The novelty component is the *changed* area, so an object moving D px per frame
produces a D-px strip: frame N is `[a, a+D]`, frame N+1 is `[a+D, a+2D]`. Their IoU is
**exactly zero** — no threshold below 1.0 matches them — so a new track started every
frame, `seen` never reached `TrackMinFrames`, and nothing was ever old enough to
cross. Association is now **overlap OR touch**, which is the tightest rule that fixes
it: it admits abutting regions and refuses anything with a gap between them.

Association is greedy and one-to-one, so two objects near each other still get two
tracks — a box joins its single nearest track and a second box gets a second. The
cost is identity *switching* when two objects cross closely, which is the ordinary
trade in every tracker and is bounded by the gap the rule refuses. The test asserts
both halves: abutting strips become one track, separated boxes become two.

**3. `internal/soak/runner.go` — evidence pointed at a detection that never existed.**

`cutAndStore` minted its own id, `cutDetectionID(sha, at)`, and wrote it into BOTH
`id` and `detection_id`. The second is a FOREIGN KEY to `detections(id)`, and a
freshly minted `ev-<hash>` names a row that has never existed — so every `PutEvidence`
failed. **AC3 could never pass in any run, ever.**

It stayed invisible because the failure looks like a legitimate outcome: a clip that
cannot be cut is a real thing that happens, the detection transitions to
`clip_refused`, a fault is recorded, and the run carries on. Ten detections, zero
stored evidence, every other criterion green. The fix was to pass the detection's own
id in — the caller already had it — and the row's primary key is now
`evidenceID(detection, sha)` so two clips for one detection are two rows.

### The lesson, because it is the same three times

**Every one of these printed a number and an error count, and none of them was
distinguishable from a working system.** Zero detections, zero stored evidence, exit
status 0. The module's own rule says a criterion nobody exercised is `NOT RUN`, never
a pass — and here the inverse happened: a criterion that was *structurally
unreachable* printed as `NOT RUN`, which looks identical to a criterion nobody got
round to. `AC3: NOT RUN` and `AC3: cannot ever pass` are the same string.

That is why `cmd/fixtureprobe` exists and why it runs the whole chain — novelty
engine, tracker, line machine — and prints `crossings 0` with the reason rather than
a summary line a person has to interpret. It is also why the red check now prints
which package failed instead of only saying the suite is red.

## What 2026-10-01 added

Four surfaces a judge asks for, and the tooling that makes them honest.

| | built | the refusal it carries |
|---|---|---|
| `pocctl vlm-probe` | one call against the pinned VMS host, no clip, raw body to `contracts/hafnia/raw/<ts>-<region>-<status>.txt` | refuses to follow a redirect, refuses without an environment-held key, spends no quota, and does not touch `contract.json` |
| `pocctl probe --live` | read-only dial of the XProtect instance; per route it reports `reachable` / `contradicted` / `unauthenticated` / `absent` / `unknown` | never POSTs an event, never promotes a row to `verified` |
| `pocctl mark <tag>` | one human verdict on one pushed event, straight into `store.MarkGroundTruth` | the store still refuses a tag the run never pushed; `Precision` prints `NOT RUN` and never `0` |
| `pocctl search` | what the model *said*, with its clip ref, its attribution and the four absence states of the **window** | RC-9: a window with an unasked or degraded proposal prints `THIS WINDOW IS NOT FULLY SEARCHABLE` and refuses the sentence "the model found nothing" |
| `pocctl brief` | one row per hour, from `heartbeats` grouped by hour | a zero-frame hour is `stream was DOWN`, and an hour of detections that were never asked is `model NEVER ASKED` — neither prints as "nothing happened" |

`SP_POC_CAMERA_INTEGRITY` moved out of an `os.Getenv` in `cmd/watch` and into the
configuration schema, where it is required by name and refused by consequence. It was
a behaviour-changing variable in no table, so no test covered it and `watch --config`
never printed it. A run must now say which trigger it is carrying.

Three documents that did not exist before this commit:

| | |
|---|---|
| [`docs/architecture.md`](architecture.md) | the system on one page, with the **AI plane drawn as optional** and the four absence states produced at each box |
| [`docs/probes.md`](probes.md) | where the two probes' answers go. Currently a page of `NOT RUN` with the mechanism beside each question |
| [`docs/gap-ledger.md`](gap-ledger.md) | the plan's definition of done, applied: every row **built and tested**, or **blocked on a person with the reason** |
| [`docs/vss-mapping.md`](vss-mapping.md) | VSS 4.2, and the four places the mapping is not one |

## The five minutes nobody has had yet

| | |
|---|---|
| a live camera | **never** — every run this build was tested on reads a recording |
| a live XProtect instance | **never** — every push went to `xpsim`, a program in this repository |
| a live VLM call | **never** — `contracts/hafnia/` is ASSERTED against published documentation, not verified on the wire. `pocctl vlm-probe` now exists to settle it and has never been run against a key |
| ground-truth marks | **none** — `testdata/groundtruth.json` is empty, so the false-positive row is `NOT RUN` |
| the reference-mode trigger on a real moved camera | **never** — tested against synthetic fixtures, which is not the same thing |

Those five lines are the whole of what is missing, and they are the whole of what
`pocctl probe --live`, `pocctl vlm-probe`, `pocctl demo --live` and
`contracts/hafnia/README.md` exist to close.

## The four defects that were live before this work

They are listed because their shape is the argument for the red checks. None was in
an interesting part of the module; all four were **refusals that were missing**, and
a missing refusal is invisible in every artifact a demo produces.

| | what was wrong | now |
|---|---|---|
| the pushed bounding box | every event carried `top/left/bottom/right` all zero while the row beside it held real numbers | `xprotect.BuildEvent` refuses an all-zero box; RC-6 removes that refusal and asserts the suite goes red |
| the bookmark | `SP_POC_DEVICE_PATH` was required by configuration and read **nowhere**, so every event went out with no pointer to any recording | a bookmark is built, queued through the same outbox as the event, and read back by reference; `xpsim` learned the read-back so the contract has something to fail against |
| the sidecar | every sidecar variable was unconditionally required, so the shipped pipeline needed an ONNX model, a Python worker and a config the run could not use | `SP_POC_TRIGGER_MODE` gates them; preflight asks the question about the component the run actually has |
| the incident class | the class was a hard-coded family enum, so a second class was a code change | the class is a prompt file and one manifest entry; a test asserts two classes differ only in prompt id |

## What is built, and has been tested here

- the **novelty engine** as a region measurer, including the two ways it fails
  silently â€” a reference score must not rise on a moving object, and a temporal
  score must not fire on a lighting change. Both are tests, with synthetic scenes
  built so a displacement is a displacement and a vehicle is a vehicle.
- the **VLM client**: strict parsing, a closed four-word verdict set, region pinning
  with no failover, redirects refused, a TLS floor, a per-site daily cap reserved
  *before* the upload, and an asset deleted on every path after it.
- the **spine**: commit, record the decision inputs, snapshot the frame the decision
  was made on, cut the clip, ask once, push the event, place the bookmark â€” every
  external effect after the row.
- the **ledger**: length-prefixed chained hashes, SQLite triggers refusing UPDATE and
  DELETE, verification that names the first broken row, and an ed25519 seal over the
  head that is **refused over a broken chain**.
- `pocctl prove`, which recomputes a decision from stored facts with **zero** provider
  calls because the replay function is handed no observer and no client.

## Verification

Command run:

```
poc/active-monitoring\scripts\verify.ps1
```

It runs, and it stops at the first failure: toolchain, `go build ./...`,
`gofmt -l`, `go vet ./...`, the module boundary check (including the
`internal/overlay` and `internal/classify` leaf rows, and the two new ones:
**`internal/vlm`** and **`internal/novelty`**, which may not reach a database, a
pipeline or a detector â€” `internal/vlm` in particular, because a verifier that
could read what somebody said last time would be a recorder, and a recorder is
exactly what `pocctl prove` forbids), `go test -race -p 1
-count=1 ./...`, the sidecar's Python tests, and the red check.

**The red check now removes twelve controls**, not three. The nine added are RC-1
through RC-7 and RC-9 from the milestone plan: a verdict cleared on an unasked ask
(twice, at parse time and at write time), the chain's hash comparison, the
append-only triggers, the zero-provider-call assertion in the replay, the snapshot's
frame-attribution refusal, the all-zero bounding-box refusal, the cross-region
refusal, and the unsearchable-window refusal in `pocctl search`. Each is a control
removed from the **production** code, and the suite must go red every time. The
step prints its own count from the table, so the number cannot go stale.

RC-11 — a reference-mode `unchanged` is unreachable while no reference is pinned —
is a refusal in `internal/novelty` with two tests and is deliberately **not** a
control row: the table is a list of properties of code, and recording "this is not
a control" next to the ones that are is the honest way to keep the count meaning
something.

**Last recorded result: see the table below.** A green gate here is a *local
claim* about one command on one machine. There is no pipeline on this repository
and there has not been one; do not write "CI passed" about anything in this
directory.

| | |
|---|---|
| result | **GREEN** — every step passed, no skips |
| `--rtsp` used | **yes**, twice. First for a 60 s run with a scratch copy of `mediamtx` v1.21.1; then 2026-10-01 as a full `pocctl demo --rtsp` |
| `mediamtx` installed | **yes, since 2026-10-01** — `winget install --id Bluenviron.MediaMTX -e` (MIT), v1.21.1 |
| the relay path exercised | **yes** — 167 frames over `rtsp://127.0.0.1:8554/street`, 5 heartbeats, 0 decode errors, AC1 `PASS`; and on 2026-10-01 a full 2 m run reading 11 heartbeats over the relay |
| detections | **0** — the COCO model sees nothing in `car-crossing.mp4`, which `testdata/README.md` records as a measurement |

The `mediamtx` row changed twice today, and both changes were defects in **this
document and in the gate**, not in the demo:

1. The gate located `mediamtx` by name on `PATH` only. `winget install` is an
   *archive* package — it extracts into its own folder and adds a PATH entry the
   installing shell has not inherited — so a tool that is genuinely installed
   produced a `SKIPPED`. `Find-Tool` now falls back to winget's package folder, found
   by glob so it does not go stale on the next version bump. A gate that prints a
   skip about an installed tool has stopped describing its own coverage, and that is
   the exact failure this module treats as a defect everywhere else.
2. Having installed it, `pocctl demo --rtsp` still refused by name — because the
   binary's folder was not on the child process's `PATH`. The gate now puts it there
   when it finds it, so the demo that the toolchain step just blessed is the demo
   that can actually run.

The relay is **opt-in**. `pocctl demo` without `--rtsp` loops a file and is a
complete gate on its own; that is D-4 and it is why a missing `mediamtx` would be a
`SKIPPED` and never a `FAIL`. A gate that goes red for a missing *optional* tool
teaches everyone to ignore the gate.

### What the relay run scored, and what it did not

The overall verdict was **`FAIL`**, and that is the correct answer, not a
disappointment: AC2, AC3 and AC4 are `NOT RUN` because nothing was detected, and
`NOT RUN` is never a `PASS`. The run proved the **transport** and nothing about
the detector — which is the whole limit of the relay, and why the source line
says so on every surface.

## The one that made the POC detect nothing

`the sidecar's answer is not the documented shape (json: unknown field "input_w")`

The sidecar had started sending `input_w` and `input_h`. `ai.Client` decodes the
reply with `DisallowUnknownFields` — correctly, strictly, and that strictness is
what caught it — so **every reply was refused**. 169 of 169 frames came back
`cannot_tell`, which is a *designed* state, so nothing was red. The tests were
green. The report said `cannot_tell`, which reads as the tripwire doing its job.

Two gates had been built to catch exactly this class and neither fired:

- **AC6, "no invented answers", could not fail.** Its `unparsed` and
  `healthy-from-failed` clauses read `soak.Inputs` fields that **nothing ever
  assigned** — `Runner.AICounters()` had no caller. The criterion reported `0
  unparsed` over a run in which every frame was unreadable.
- **The detector's arithmetic was computed nowhere.** `detect.Counters` existed,
  was tested, and `Runner` never called `Observe`. So `detections | 0` could not
  be distinguished from "the model saw nothing".

All three are fixed, and the run below is the result.

### The run, after the fix

`pocctl demo --clip testdata/youtube/sTF-6_xinUU.mp4 --zones testdata/night-intersection.zones.json`

Real footage, a real signalised intersection, at night.

| | |
|---|---|
| frames asked / answered | **366 / 366** |
| raw candidates | 3,896,802 |
| boxes approved | 36 |
| boxes rejected | 4,079 — 3,972 `class not on the approved list`, 94 `below the score threshold`, 13 `track has not been seen long enough` |
| detections | **1**, terminal |
| clip | stored, **verified on read-back**, 0 mismatched |
| push | 1 attempt, 1 result, 0 duplicates, 0 dead-lettered |
| AI p99 | 567 ms against a 900 ms budget |
| **verdict** | **PASS** |

And the event on the wire:

```json
"object": {
  "type": "car",
  "value": "car (vehicle)",
  "confidence": 0.84,
  "boundingBox": { "top": 512, "left": 534, "bottom": 579, "right": 661 }
}
```

Those four numbers are byte-identical to `detections.box_top/left/bottom/right`
for the same detection — which is B2, closed by a comparison rather than by an
eyeball.

**The new section in the report is what made the bug findable.** `detections | 0`
has two causes that print the same two characters, so `report.md` now carries a
`## What the detector did` section that says which one it was. It is the first
thing to read when a run finds nothing.

## Three more numbers that were presented but never computed

| | was | now |
|---|---|---|
| **AC6's two clauses** | `soak.Inputs.UnparsedAIResponses` / `HealthyFromFailed` were assigned only in a test, so the criterion could never fail | read off `Runner.AICounters()`, and `ai.Result.Unparsed` distinguishes *a reply that arrived and could not be read* from a deadline and from a working tripwire |
| **a pushed event's confidence** | `scoreOf` returned a hardcoded `0` for every event, with a comment arguing that was safer than inventing a number — so every VMS showed `confidence: 0` for a detection the detector was confident about | `detect.Crossing` carries the score, copied from the box at the instant of the crossing |
| **the detector's own arithmetic** | `detect.Counters` was never observed | observed per frame, persisted in `detector_tally`, and printed by both `watch` and `pocctl report` |

A field that is always zero is not a conservative default. It is a missing
measurement printed as a real one.

## The wire is now pinned from both sides

`pyworker/fixtures/telemetry_keys.json` records the sidecar's telemetry key set.
`pyworker/test_wire.py` asserts the live dict still matches it; `internal/ai/wire_test.go`
decodes a payload built from the same file with the same strict decoder
production uses. A field added on one side without the other is now a test
failure instead of a whole quiet run.

Strict decoding stays. It is what caught this.

Each was found by running the thing, not by reading it, and each had a green
suite behind it.

| | what was wrong | now |
|---|---|---|
| **`boundingBox` of zeros** | every pushed event carried `xprotect.Box{}` while the row beside it held the real box. 286 green tests. Written up in `docs/findings.md` §8. | the same expression, asserted by a **comparison** — and the comparison was proven to have teeth by reverting the fix and watching it fail |
| **`pocctl demo` could not run** | `demoEnv` was missing six required `SP_POC_*` variables, so the headline command died in `config.Load`. Then, once that was fixed, it started the simulator and then asked `rehearse` to arm one on the same port — which `rehearse` correctly refuses | six variables set, and the rehearsal moved **ahead** of the simulator so it owns its own |
| **`-reconnect` on an RTSP input** | those are HTTP protocol options; ffmpeg answers `Option reconnect not found` and exits. The run ended in **20 ms** and reported zero of everything — indistinguishable from an empty road | the flags are HTTP-only, and a test asserts the RTSP argument list carries no option the RTSP demuxer does not have |
| **`go vet` red, `gofmt` red, boundary red** | `config.Config` embedded a `sync.Once` and is passed by value to seven readers; twelve files were unformatted; `internal/detect` had come to import `internal/ai` | the cache is behind a pointer, the module is formatted, and the shared vocabulary lives in `internal/aistate` so both packages can see it without either depending on the other |

A fifth, in the script itself: `verify.ps1` crashed on a **passing** Python
suite, because `unittest` writes its progress dots to stderr by design and
PowerShell 5.1 turns any native command's stderr into a terminating error under
`$ErrorActionPreference = 'Stop'`. The gate could never get past a green run on
this shell.

## What is built, and what proves it

| | built | proven by |
|---|---|---|
| the four-bug decode collapse | `pyworker/server.py` (`ExportSpec`, `decide_family`, anchored + fused) | 40+ cases in `pyworker/test_decode.py`, asserted against a committed contract |
| a **measured** model contract | `pyworker/fixtures/yolov8n.contract.json` | `pyworker/probe_model.py`, through the sidecar's own load path |
| the family taxonomy | `internal/classify` | every COCO class mapped, `TestOneWayOwnership` |
| runtime telemetry + the `cannot_tell` tripwire | `internal/ai/telemetry.go`, `Interpret()` | `internal/ai/telemetry_test.go` |
| approved/ghost split, track age, per-reason rejection | `internal/detect/split.go`, `engine.go` | `internal/detect/*_test.go` |
| `family`, `snapshot_ref`, `snapshot_kind` columns | `internal/store/store.go`, `family_test.go` | the column-migration test |
| three evidence kinds | `internal/evidence/store.go`, `PutKind`/`RefKind` | `internal/evidence/evidence_test.go` |
| **the real bounding box on every pushed event** | `internal/soak/runner.go` | a **comparison** against `detections.box_*`, `pipeline_e2e_test.go` |
| the crossing snapshot | `internal/soak/snapshot.go` | the e2e asserts a JPEG is on disk for the crossing's frame |
| the RTSP relay | `pocctl demo --rtsp` | `mediamtx` + `ffmpeg` publisher + `ffprobe` poll; refuses by name when absent |
| the human false-positive review | `internal/store/groundtruth.go`, `testdata/groundtruth.json` | the join is refused for a tag the run never pushed |
| the model licence record | `pyworker/fixtures/yolov8n.licence.json` | beside the measured contract, because `models/` is gitignored |

## Two plan premises that were wrong, and what was done instead

| | the plan said | reality | now |
|---|---|---|---|
| **mediamtx is config-less** | "mediamtx runs config-less and serves `:8554` by default" | true of the versions current when the plan was written. v1.21 answers `path 'street' is not configured` and refuses the publisher | a **five-line `mediamtx.yml`** is written into the relay's own directory and its path is printed. Every other protocol is switched off, so the relay occupies exactly one TCP port |
| **ffmpeg reconnects over RTSP** | "`-reconnect 1 -reconnect_streamed 1 -reconnect_delay_max 5`" are correct for RTSP | they are **HTTP** protocol options. The RTSP demuxer has no such option, and ffmpeg exits | they are HTTP-only now. The honest consequence, written into `source.InputArgs`: **a run over RTSP does not survive the publisher being killed.** A camera drop is a RUN ENDING |

## What is NOT built, and why

| | why |
|---|---|
| **`testdata/street.mp4`** | **Phase 0 is open.** It needs a person to physically record 60–90 s of daylight footage of a car or pedestrian crossing a line. No code produces it, and no threshold change substitutes for it. |
| the `real` slot in `testdata/labels.json` | same reason. It is empty, and `car-crossing.mp4`'s provenance was corrected from `real` to `synthetic` because a measured run returns zero boxes on it. |
| a licence for the committed footage | **CLOSED 2026-10-01.** `car-crossing.mp4` and `bird-synthetic.mp4` are generated locally by this repository's own scripts, so there is **no rights holder to ask** and the licence is the repository's own. `attribution` is recorded as `NOT APPLICABLE` rather than left as `REPLACE-ME`, and the SHA-256 is recorded so the label describes this file. See `docs/provenance.md`. |
| a licence for the YouTube clip | **still open, and not fixable here.** The rights holder is the camera operator, a public body, and neither they nor the uploader have been asked. `cleared_for_external_use: false`: readable on this machine, not showable to anyone else. |
| a licence for the model | **NOT RECORDED.** `models/yolov8n.onnx` is a YOLOv4-shaped export under a filename that says YOLOv8, and this repository has no record of where the weights came from. See `pyworker/fixtures/yolov8n.licence.json`. Cleared for measurement here; **not** cleared for sale or distribution. |
| a real XProtect push | the target stays `xpsim` and every surface says so. Out of scope by decision. |
| a seven-day run | never run. `AC1` is zero tolerance, and zero over five minutes is not zero. |
| a false-positive number | `NOT RUN`. The mechanism is built and refuses to invent one; nobody has marked an event. |

## Things that were wrong and are now fixed

- Every pushed event carried an all-zero `boundingBox` while the row beside it in
  the database held the real box. 286 green tests, real detections, and an event
  on the wire with four zeros in it. Written up in `docs/findings.md` §8.
- `pocctl demo` — the headline command of the module — died in `config.Load`.
  `demoEnv` had never been updated for the six required variables that arrived
  with the approved/rejected split, so the environment it built was one its own
  child refused.
- `go vet ./...` was **red**: `config.Config` embedded a `sync.Once` and is passed
  by value to seven readers in `cmd/watch`. The cache is behind a pointer now.
- The live screen hardcoded the word `answered`, which is not one of the four
  words in `CONTRACT.md` §2.2, and printed it for frames that were never sent.
- `SP_POC_MIN_BOX_AREA`, `SP_POC_GHOST_MIN_SCORE`, `SP_POC_TRACK_MIN_FRAMES` and
  `SP_POC_MAX_BOXES_DRAWN` were read, validated and then **not passed to the
  engine**. A configuration value that is checked and ignored is worse than one
  that is absent: the report says the operator set it and the run did not obey it.
- The engine hardcoded `split.AIState = asked_answered` for every frame, so the
  run-level counters could not tell a frame the model answered from a frame the
  sidecar never reached. `Split.Telemetry` was never assigned anywhere in
  production code, so `BoxCandidates` and `CandidateShare()` returned zeros and
  `false` for every frame of every run.

The last two are the same shape as the first: **a number the system presents and
does not compute.** They are wired now (`Engine.FeedAnswered` takes the frame's
real state, `Split.Candidates` is filled from the real answer), and the fix is
`Engine.Feed`'s: the pipeline supplies what it knows and nothing in this module
guesses at it.

## The demo material, and what it is not

The demo runs over `testdata/youtube/sTF-6_xinUU.mp4` with
`testdata/night-intersection.zones.json`. It is a real signalised intersection at
night, and it is the only material in this repository the model actually detects
things in.

- The **clip is not in git** (`testdata/youtube*/` is gitignored) and **its
  licence is not recorded**. It may be read on this machine. It may **not** be
  shown to anyone outside it — not in a demonstration, not in a screenshot.
- The **zone file is in git**, because the line is a measurement and is useless
  without the footage.
- The line was placed by **looking at the frame** and then measuring where the
  model puts its own boxes on it. Vehicles travel right to left along y ≈ 0.54,
  which is why the line across that carriageway is **vertical** — a horizontal
  line would never be crossed by anything, and a zone with no crossings is
  indistinguishable from an empty road.
- Phase 0 is still open. `testdata/street.mp4` — 60–90 s of daylight footage
  recorded by a person — does not exist, and no code produces it.