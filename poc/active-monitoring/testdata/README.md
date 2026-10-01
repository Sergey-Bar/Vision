# `testdata/` — the labelled material, and what its labels mean

Everything here carries a `provenance` in [`labels.json`](labels.json), and the
report prints that provenance next to every number derived from the material.
This directory is not decoration: an unlabelled measurement is a claim.

## What is here

| File | Provenance | What it is |
|---|---|---|
| `car-crossing.mp4` | **`synthetic`** — licence **cleared 2026-10-01** | a coloured rectangle crossing a `testsrc` frame. It exercises the line-cross machine; a COCO model sees nothing in it (measured, below) |
| `bird-synthetic.mp4` | **`synthetic`** | a bright rectangle travelling left to right, built by `scripts/make-bird-clip.sh` |
| `groundtruth.json` | — | the human review: which pushed events were false. Empty `marks` means **NOT RUN**, not zero |
| `labels.json` | — | the provenance record: source, licence, retention, expected windows |
| `car-crossing.zones.json` | — | **PROPOSED** line for the `testsrc` case |
| `bird-synthetic.zones.json` | — | **known-correct** line for the synthetic case |

## The two honest statements

**The bird material is a rectangle.** It has bird-like motion and bird-like
proportions and nothing else. It exists so the pipeline can be exercised end to
end on material whose every property is known and reproducible. It is **not**
evidence that the system detects birds, and `CONTRACT.md` §12 and the report both
say so. If this material is ever quoted as a detection claim, that is a defect.

**The real-motion slot is empty. The licence half of it is now closed.**

`labels.json`'s three `REPLACE-ME` fields were on `car-crossing.mp4`, and they are
filled: that clip is an `ffmpeg` `testsrc` pattern with a `drawbox` rectangle over it,
so it has **no rights holder to ask** and the licence is this repository's own. The
YouTube clip is a different animal and stays shut — the camera belongs to a public
body, the footage is theirs, and watching it live and storing clips of it are
different acts. It may be read on the machine that has it and **may not be shown to
anyone else**, and `cleared_for_external_use: false` says so in a field rather than
in prose.

These were two different problems and closing one did not close the other:

| | what is missing | what fixes it | state |
|---|---|---|---|
| **licence** on the committed material | three fields | a licence recorded verbatim from what is known about the material | **CLOSED 2026-10-01** — `docs/provenance.md` |
| **licence** on the YouTube clip | permission from a public body | the camera operator. Not the uploader, not us, not an agent | **open**, and not recoverable from this file |
| **real motion** | there is no real footage at all | record `testdata/street.mp4`: 60–90 s, **daylight**, phone fixed or on a tripod, one real car or pedestrian crossing a horizontal line, 1080p | **open** — a person with a phone |

**Do not lower the threshold to make this footage detect.** If a daytime street
recording produces nothing, the recording is wrong — too dark, nothing on the line,
the camera too far up — and the remedy is a new recording. A threshold lowered far
enough to fire on synthetic material fires on noise, and the false-positive number
becomes the first thing a reviewer reads.

## Why a line has to be drawn, not guessed

`pocctl zone --pick <source>` renders a still with a coordinate grid on it and
proposes a `zones.json`. A hand-drawn line that crosses a car's bounding box
centre at some arbitrary height produces a run with **zero events and no error at
all** — the most expensive kind of silence there is, because a reader sees a
report with no failures and concludes nothing was there.

So the line is placed by a human reading a grid, and then **verified with a short
run**: one expected event is the proof the line is in the right place. A run that
produces none is not evidence that nothing happened.

## Retention

`labels.json` records a retention period per clip. The evidence store's sweep is
age-based against it. The sweep never deletes a clip to satisfy a byte target,
and the rolling disk ceiling never deletes stored evidence — a run that fills its
data root stops with `disk_budget_exceeded` and says why, because "continue and
delete" is how the only copy of an incident disappears.

## Reproducing the synthetic material

```bash
scripts/make-bird-clip.sh          # writes testdata/bird-synthetic.mp4
scripts/make-loop.sh               # assembles the seven-day loop and prints its event rate
```

`make-loop.sh` prints `expected_events_per_day` computed from the loop's own
measured duration **before** you start a run, and refuses if the rate is below the
target. A run with almost no events cannot exercise AC2 or AC4 at all: both are
about what happened to events, and there are almost none.

## What a real run measured, and what it means for this material

Three findings from actually running the pipeline, recorded here because they
change what a run of this material is worth:

**1. A COCO model does not see the committed material.** A real run with
`yolov4.onnx` was pointed at `car-crossing.mp4` at three instants and the model
returned **zero boxes at every one**. A flat coloured rectangle on a `testsrc`
background is not a photograph, and a detector trained on photographs is right to
see nothing in it. This is not a bug and it is not a tuning failure: it is the
reason the `real` slot above is empty — and it is why `labels.json` now records
this clip's provenance as **`synthetic`** rather than `real`. It used to say
`real`, and that was a false label on a measurement.

**What that means for the POC:** a run over this material produces **zero
detections**, and the report says so — AC2, AC3 and AC4 come out `NOT RUN` with
the reason "no detection was recorded". That is the correct and intended
behaviour, and it is the single most important thing to understand before showing
anyone a run over synthetic footage.

**The detection path is proven a different way.** `internal/soak`'s integration
test drives the whole pipeline with real ffmpeg, a real recorder, the real
detector, a real store, the real content store and the real Milestone client
against a real simulator, and stubs **only** the model's accuracy. It shows a
frame becoming a crossing, a committed row, a stored-and-hashed clip, a pushed
event, and a read-back finding it under its content-derived tag. The clip path's
own integrity — re-encode, byte-for-byte read-back, hash, dedup, sweep — is proven
separately in `internal/recorder` and `internal/evidence` against real ffmpeg.

**2. Throughput is the real constraint, and it is measured.** `yolov4.onnx` at
416×416 on this machine's CPU takes **~0.9 s per frame**, against a 0.8 s AI
deadline. At `--ai-fps 5` the analysis pipe fills within seconds and the run ends
with `ai_pipe_backpressure` — which is the designed behaviour: a recorded fault,
ffmpeg killed, no silent frame drops. At `--ai-fps 1` the pipe stays at depth
0–5 and the run reaches its target with AC1, AC5 and AC6 passing on measured
numbers. Run `pocctl cap` to measure this on the machine in front of you before
choosing a rate.

**3. A 71-second heartbeat gap was an artefact, not a dropout.** The first
version of the run loop shared one goroutine between frames and heartbeats, so
the first inference into a freshly loaded ONNX session silenced the liveness
signal and AC1 failed for a reason that had nothing to do with the pipeline. The
heartbeat now runs on its own goroutine, and `internal/soak/loop_test.go` has a
test for exactly that.

