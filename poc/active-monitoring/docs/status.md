# Status — poc/active-monitoring

Last revised 2026-09-30 (the RTSP relay, the real bounding box, the crossing
snapshot, the family taxonomy on the wire, the KPI strip, and the human
false-positive review).

This is the source of truth for **what is built** in this module. The README is
not, and neither is the commit history. Where something is not done it says why,
and where the reason is a person rather than a line of code it says that too.

## Verification

Command run:

```
poc/active-monitoring\scripts\verify.ps1
```

It runs, and it stops at the first failure: toolchain, `go build ./...`,
`gofmt -l`, `go vet ./...`, the module boundary check (including the
`internal/overlay` and `internal/classify` leaf rows), `go test -race -p 1
-count=1 ./...`, the sidecar's Python tests, and the red check.

**Last recorded result: see the table below.** A green gate here is a *local
claim* about one command on one machine. There is no pipeline on this repository
and there has not been one; do not write "CI passed" about anything in this
directory.

| | |
|---|---|
| result | **GREEN WITH SKIPS** — every step passed; one tool is absent |
| `--rtsp` used | **yes**, for a 60 s run, with a scratch copy of `mediamtx` v1.21.1 |
| `mediamtx` installed | **no** — downloaded to a temp directory for the run, not installed |
| the relay path exercised | **yes** — 167 frames read over `rtsp://127.0.0.1:8554/street`, 5 heartbeats, 0 decode errors, AC1 `PASS` |
| detections | **0** — the COCO model sees nothing in `car-crossing.mp4`, which `testdata/README.md` records as a measurement |

The relay is **opt-in**. `pocctl demo` without `--rtsp` loops a file and is a
complete gate on its own; that is D-4 and it is why a missing `mediamtx` is a
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
| a licence for the footage | three `REPLACE-ME` fields. **A clip with no licence may be read on this machine and may not be shown to anyone outside it.** No code fixes this. |
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