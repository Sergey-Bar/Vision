# CONTRACT — `poc/active-monitoring`

This file is the whole law of this module. It is short on purpose. An agent
editing code here must not invent a metric that is not written down here, and
must not weaken a rule that is written down here without changing this file
first.

Everything below is in English. Hebrew is the human-facing `README.md` and
`docs/` material only; the code, the contract and the report vocabulary are
English because the wire formats, the acceptance criteria and the log lines are
English.

---

## 1. Reliability rules

These five are not negotiable. Each one has a test. A change that weakens one
requires a new test in the same commit that changes the rule.

**R1 — Zero accepted event loss.** A detection row reaches a terminal state or
it is a defect. `pending`, `clipping`, `pushing` and `queued` are *not* terminal.
A row in a non-terminal state older than 5 minutes fails `AC2` and is reported
as a lost event, whether or not the process is alive.

**R2 — Record before effect.** The detection row is committed in a transaction
*before* any external effect: before the clip is cut, before the push is
attempted. There is no "we saw it but forgot to write it down". Every push
attempt is an `outbox` row with attempt count, next attempt time and a
dead-letter decision. `kill -9` at any point must leave a resumable state, not a
lost one.

**R3 — One reader, one clock.** Exactly one process reads the video source: a
single `ffmpeg`. The Go sidecar drains its MJPEG stdout pipe in a dedicated
goroutine and stamps each frame with `time.Now()` at the moment the frame is
read. No second reader of the source exists, anywhere. Python reads no video,
tracks nothing, holds no state.

**R4 — A missing answer is never a negative answer.** The sidecar has three
distinct absences, and they stay distinct: *not asked*, *asked and failed*,
*cannot tell*. A malformed response, a timeout, or a non-200 is never
`no_objects`. A response that arrives after its deadline is `degraded`.

**R5 — Evidence is content-addressed and read back.** A clip is not "stored"
because a process exited zero. It is stored when its bytes hash to the value in
the `evidence` row, verified by reading it back from disk. Content is keyed by
SHA-256; the path is derived, never supplied.

**R6 — `ai_pipe_backpressure` is a fault, never a silent drop.** If the MJPEG
pipe is blocked longer than the configured deadline, `ffmpeg` is killed and a
`fault` is recorded with reason `ai_pipe_backpressure`. Frames are never dropped
quietly; quiet dropping is exactly what the seven-day run is supposed to find.

**R7 — A retrieval is not a verdict.** Anything that reads what was already
stored — `pocctl search`, `pocctl brief`, the report, the audit commands —
holds no observer and no `*vlm.Client`, and calls no provider. It reads columns
and prints them. Two consequences follow and both are the rule:

- It must report the four absence states **of the window, not of the hits**. A
  query that returns eleven rows and one that returns none are the same query when
  eleven proposals in the window were never asked, and a surface that prints only
  the hits cannot tell those apart. So `SearchObservations` returns a
  `SearchSummary` beside the rows and `Unsearchable()` refuses the sentence "the
  model found nothing" whenever any proposal was `not_asked` or `degraded`
  (RC-9).
- A reference-mode run with no pinned view reports `unpinned` and never
  `unchanged` (RC-11). `IntegrityUnchanged` is unreachable from an unpinned
  engine; `novelty.Engine.IntegrityState` is the only way to get one.

**R8 — A caption is a claim, and a claim carries its attribution.** Every
sentence the model produced that a surface renders must be renderable *with* the
clip it is about and the identity of who produced it, or the surface must refuse
to render it. Concretely, a rendered line carries: the stored `summary` verbatim
(no paraphrase — a paraphrase of a model's sentence is a sentence this repository
invented), the `evidence.storage_ref` and `sha256` a person can open, the
`model_identity` the provider *claimed*, the `contract_version`, the `temperature`
and the `output_hash`.

There is no `model_hash`. A hosted model has no weights to hash, and inventing one
is the failure R8 exists to prevent. What is stored is the provider's claim, the
contract version — a hash of prompt text plus output schema plus model name plus
region — and the hash of the four validated output fields. Those three together
are what makes a caption checkable; a weight hash would have been a fourth thing
that looked stronger and was not.

---

## 2. Vocabulary

These are the only words allowed in status columns, reason codes and report
tables. A new word needs a definition here first.

### 2.1 Detection lifecycle (the states `AC2` depends on)

```
                  accepted  (committed, before any external effect)
                     |
        +------------+--------------------+
        |            |                    |
   clip_ok    clip_refused      push_sent | push_refused | push_dead
        |            |                    |
        +------------+------> done <------+
```

| State | Meaning | Terminal |
|---|---|---|
| `accepted` | Received and recorded. Never un-accepted after commit. | no |
| `clip_ok` | Clip cut, read back, SHA-256 matches. | no (leads to `done`) |
| `clip_refused` | Clip refused, with a stored reason. | no (leads to `done`) |
| `push_sent` | Provider accepted (202/200) **and** the read-back call found its id. | no (leads to `done`) |
| `push_refused` | Provider said no (403/400) and the reason was stored. Terminal, not "waiting". | no (leads to `done`) |
| `push_dead` | The backoff budget was exhausted. The `outbox` row stays for human review. | no (leads to `done`) |
| `done` | Terminal. Anything not `done` after 5 minutes is a lost event. | **yes** |

`pending`, `clipping`, `pushing`, `queued` are explicitly **not** states of this
machine. They are worker labels and are never written to `detections.status`.

### 2.2 AI absence vocabulary

| Term | Meaning | Not to be confused with |
|---|---|---|
| `no_objects` | The model ran, parsed, and the array was empty. | — |
| `degraded` | Asked, and the answer was late, malformed, or came from a failing read. | a negative answer |
| `not_asked` | The frame was never sent (backpressure, worker busy, gap). | a negative answer |
| `cannot_tell` | The response was structurally valid but not interpretable. | a negative answer |

### 2.3 Report verdicts — exactly three

`PASS`, `FAIL`, `NOT RUN`. Nothing else. `NOT RUN` always carries "what was
missing". `NOT RUN` is never written as `PASS`, on any screen, in any file.
A run shorter than its target is `PROVISIONAL`; see §3.3.

### 2.4 Provenance

Every piece of video material carries `provenance: real | synthetic` in its
`labels.json`, and the report prints it next to every number derived from it.
The bird clip in `testdata/` is `synthetic` and is labelled as such everywhere —
in the screen, the log, the report and the one-pager. See D7.

### 2.5 Modes

| Mode | Meaning |
|---|---|
| `live` | A real source, real run. |
| `demo-replay` | A labelled `testdata` clip driven through the real pipeline. Shown in the header, the log and the report. |
| `xpsim` | Push target is the local simulator. **Not** an XProtect server. |
| `live-vms` | Push target is a real XProtect, only ever selected by an explicit `--xprotect-base-url`. |

---

## 3. Acceptance criteria — the closed definition

`AC1`–`AC7` are listed in §7 of the plan and are not restated with different
wording here. What is restated is the arithmetic, because the arithmetic is what
an agent is most likely to invent.

| # | Criterion | Arithmetic |
|---|---|---|
| `AC1` | Zero dropouts | `process_exits == 0` **and** `count(heartbeat_gap > 30s) == 0`. Absolute zero. Not "99.9%". |
| `AC2` | Zero lost events | `count(detections where status != 'done' and age > 5m) == 0` |
| `AC3` | Evidence intact | `count(clip_stored where readback_sha256 != sha256) == 0` |
| `AC4` | Push without loss or duplication | `count(detections with no outbox row) == 0` **and** `count(push attempts with no result) == 0` **and** `count(duplicate ids per tag) == 0` |
| `AC5` | Bounded resources | `rss <= 1.5 GiB` **and** `db <= 512 MiB` **and** `disk <= declared --max-disk` **and** `cpu(ffmpeg+python) <= declared` |
| `AC6` | No invented answers | `count(unparsed AI responses) == 0` **and** `count(healthy derived from a failed read) == 0` **and** `ai_p99 <= declared budget` |
| `AC7` | Modelled faults behaved | every rehearsed fault has a recorded, expected outcome. A rehearsed fault with no recorded outcome fails `AC7`. |

### 3.1 `NOT RUN`

`AC7` is `NOT RUN` when no fault rehearsal ran. `AC5`'s CPU clause is `NOT RUN`
when no CPU ceiling was declared. The false-positive rate is `NOT RUN` unless a
human marked false positives — it is never invented. (§7, T27.)

### 3.2 The truth line

Every report, screen and log line carries, verbatim, which source and which push
target were used. Against the simulator the line is:

```
push target: local simulator (xpsim). No XProtect server was reachable during this run.
source: recorded loop. No live camera was used.
```

There is no column headed "reached Milestone" in a run that went to `xpsim`.

### 3.3 `PROVISIONAL`

`PASS` requires `elapsed >= target` **and** all criteria holding. Below the
target the verdict is `PROVISIONAL`, with the number of criteria that do hold
and the failures already found. `PROVISIONAL` is not a soft `PASS`; it is a
statement that the run has not concluded, and a screen showing it must display
`PROVISIONAL` and not `PASS`.

---

## 4. What this module does not claim

Sourced from §12 of the plan. The list exists so that a future agent cannot
quietly promote one of these into a headline.

- "Crashed through every fault class" — it is a list of faults a human injected.
- "No false alarms" — no human marked them, so there is no number.
- "Verified against Milestone" — no VMS was reachable.
- "100% detection" — there is no ground truth; there is a detection rate.
- "The screen shows everything is fine" — the screen shows what the DB says.
- "Detects birds" — the bird clip is `synthetic`.
- "Output to XProtect" — what was proven is that the `POST` is contract-correct
  and that the read-back found it.

---

## 5. Module boundary

This module is a nested Go module. It must not import
`github.com/Sergey-Bar/Ai-Captain/...`. The repository root's `go build ./...`
and `go test ./...` skip this directory, which is exactly why
`scripts/verify.ps1` exists here, and why one of its checks proves the root does
not build this module. The root `go.mod`, root `scripts/verify.ps1` and
`contracts/` are not ours to edit.
