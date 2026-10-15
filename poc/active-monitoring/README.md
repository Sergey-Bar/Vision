# Active Monitoring POC — פרויקט בדיקת האפקטיביות החי

> ⚠️ **קריאה חובה לפני הרצה של שבוע.**
> קיום ה־POC הוא הוכחה להנדסה: **המערכת יכולה להפיק אירוע, לשמור קליפ
> מאומת, ולדחוף ל־API של Milestone, בלי לאבד אירוע ובלי להמציא תשובה.**
> זה לא ראיות שהמערכת מזהה עופות בעולם. זה לא ראיות שהמערכת מסובכת
> גבוהה. זה לא אומר שהאירוע הגיע ל־Smart Client של מילסטון. בסעיף
> [מה שה־POC לא מוכיח](#מה-שהpoc-לא-מוכיח) כתוב מה בדיוק **אין** כאן.
> אם מישהו ישאל אתכם "האם זה עובד?" — התשובה הנכונה היא שם הקטע הזה.

מסמך זה מיועד לאדם. הקוד, הבדיקות, הדוח והחוזה (`CONTRACT.md`) באנגלית,
מפני שהפורמטים על החוט, קריטריות הקבלה ושורות הלוג הן אנגלית. אין כאן
שכבת תרגום, כי שכבת תרגום במסלול אמינות היא מקום שבו משפט אחד נעלם.

---

## Read these, in English, before anything else

Four documents survive in this module. There were eleven; the rest are folded into
these and the fold is recorded at the bottom of each. Every link in this table
resolves to a file in the tree — there used to be two here that did not, and a
document that sends a reader to a file that was never written is a document whose
other claims are not worth checking.

| | |
|---|---|
| [`docs/JUDGE.md`](docs/JUDGE.md) | **the five minutes a judge watches**, and what each step does *not* prove |
| [`docs/hackathon/demo-script.md`](docs/hackathon/demo-script.md) | the three-minute narration, with the questions it is expected to draw |
| [`CONTRACT.md`](CONTRACT.md) | the law. Rules R1–R8, the acceptance arithmetic, the four absence words |
| [`contracts/hafnia/README.md`](contracts/hafnia/README.md) | what the VLM client assumes, which assumption the provider's own documentation contests, and what settles it |

**What is built, what is not, and who each missing thing is waiting on** is not a
separate document here. It is `CONTRACT.md` for the rules, `README.md` for the
real-run findings, and the SKIPPED block that `scripts/verify.ps1` prints for
anything the gate could not measure on the machine it ran on. Those three are
generated or measured; a hand-maintained status file goes stale on the day
somebody forgets it, and a stale status file is worse than none.

### Three commands, on a clean checkout

```powershell
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"
cd poc\active-monitoring

go build -o bin\pocctl.exe ./cmd/pocctl
go build -o bin\watch.exe   ./cmd/watch
go build -o bin\fixtureprobe.exe ./cmd/fixtureprobe

.\scripts\verify.ps1          # the gate: build, vet, boundary, tests, then every red check
```

**Build comes first because the binaries are not in git.** `bin/` is gitignored,
so on a fresh clone `.\bin\pocctl.exe` does not exist yet, and a document that
opens with a command from `bin\` is a document whose first line fails.

Then one command that makes everything else runnable:

```powershell
.\bin\pocctl.exe bootstrap     # toolchain, model, runtime, the material table, the truth line
.\bin\pocctl.exe live list     # the curated public cameras, if a LIVE source is wanted
```

The live cameras are separate from `bootstrap` on purpose. `bootstrap` is what a run
needs on a clean checkout and is deterministic; `live` resolves a public YouTube page
over the network with `yt-dlp` and therefore cannot be. `pocctl live env <id>` prints
the environment one camera produces, and `scripts/../../scripts/live-demo.ps1` from the
repo root runs the whole path — see *The live relay: real cameras that are not ours*
below for what that proved and what it did not.

**`bootstrap` is four fetches and a statement of what it verified.** The model
comes from a pinned URL and is checked against a digest recorded in
`pyworker/fixtures/yolov4n.licence.json` — the URL earned the pin because its
bytes were *measured* against the weights this repository's contract was recorded
from, two byte streams and one SHA-256, which is provenance rather than a guess.
The runtime comes from the release asset for the platform, with no digest claimed
on a clean clone, and says so: it is **RECORDED, NOT VERIFIED** until you pass
`--sha256`. A machine that already has `models/runtime.json` keeps what that
manifest records instead of being defaulted over.

`fetch-runtime` prints the library's SHA-256 and ships no hardcoded digest on
purpose: one baked into a source file is a digest somebody verified once, on a
build that may not be the one being downloaded. When you supply `--sha256`, a
mismatch is a refused wall; when you do not, it is a labelled skip.

```powershell
.\bin\fixtureprobe.exe -detector qa\fixtures\crossing.mp4
```

`fixtureprobe -detector` prints the declared output shape, the record width, the
raw candidate count, what each suppression pass removed, and the per-class
histogram. **Those five facts are what make a zero diagnosable** rather than merely
observable, and this repository shipped three silent zeros before this pass.

### Three things that were added for the 2026 submission

Everything below is a change of behaviour, not a change of wording, and each one
carries its own red check in `scripts/verify.ps1`.

**1. Every pushed event names the model that answered.** `xprotect.Provenance` is
the R8 bundle — the ask's state, the model's own sentence verbatim, the model
identity *as the provider stated it*, the contract version, and the hash of the
validated output. It rides in two places on purpose: `data.evidence.note`, which is
this repository's own extension and which Milestone's specification never
enumerates, and the event's `tag`, which the specification documents as assignable
"for filtering and sorting purposes". If a real VMS drops the evidence object, the
tag survives and names the absence state and the output hash. `BuildEvent` **refuses**
an event with no state, and refuses one that was answered but carries no attribution.

**2. "Camera not assessed" is a filterable XProtect alarm, aggregated.** Three
absence states — `not_asked`, `degraded`, `cannot_tell` — resolve through
`SP_POC_EVENT_TYPE_GUIDS` to three Analytics Event Types; an unmapped pair is
refused **by name**, because the alternative is an absence arriving wearing the
detection's own event type, which fires a line-crossing Alarm Definition on a verdict
that was never produced. `no_objects` is refused its own type by name too: it is a
negative *answer*, it already drives the `false_positive` verdict, and an alarm that
fires to say "nothing" is the fatigue this product exists to remove.

A window of blindness produces **one** alarm carrying a count, not one per proposal:
`TestFiveHundredUnaskedProposalsProduceOneEvent` states the arithmetic. Each crossing
still gets its own XProtect event with its own measured box, because the crossing
happened and only the *verdict* is missing.

**3. The tamper proof is a command, not a claim.**

```powershell
pocctl audit ledger --tamper
```

It changes one byte of one row, watches verification fail and name the row, restores
the byte **and the two append-only triggers**, and verifies a third time. It refuses
to run against an already-broken chain, because breaking a second one hides the
first.

`SP_POC_ORT_EP=cuda` exists and **it is measured, on an RTX 4070**. It is refused
before the session opens unless `nvidia-smi` reports the configured device — ONNX
Runtime falls back to the CPU when the CUDA provider cannot be used and reports it as a
log line rather than an error, so a run that asked for a GPU and got the CPU would
produce real numbers under a label that says otherwise.

### Measured, 02.10.2026, on this machine

`pocctl detector --selftest --frames 30`, RTX 4070 (driver 610.88), ONNX Runtime
1.29.0 `win-x64-gpu_cuda12`, cuDNN 9.27, `models/yolov4n.onnx` at 416x416 NHWC,
4 CPU threads, the first frame excluded:

| provider | median | p95 | max | sustains |
|---|---|---|---|---|
| `cpu` | 241 ms | 265 ms | 293 ms | ~3 / s |
| `cuda`, device 0 | **17 ms** | **19 ms** | 19 ms | ~52 / s |

The first frame is excluded and so is the mean, because the first pays for CUDA context
and cuDNN kernel compilation — it was 7.9 s here, and including it would report this GPU
as the slow one. The row prints the sustained rate rather than a conclusion written next
to it, because a hard-coded sentence beside a measurement gets read as part of it.

Reproduce it with:

```powershell
$env:SP_POC_ORT_LIB='models\onnxruntime.dll'; $env:SP_POC_DET_MODEL='models\yolov4n.onnx'
$env:Path += ";$((Resolve-Path models).Path)"   # cuDNN is loaded by BARE NAME
$env:SP_POC_ORT_EP='cuda'; pocctl detector --selftest --frames 30
$env:SP_POC_ORT_EP='cpu';  pocctl detector --selftest --frames 30
```

**The CPU-only claim in this file's earlier revisions was wrong for this model.** The
documented 0.9 s/frame bottleneck did not reproduce: on this machine the CPU detector
runs the committed model in 241 ms, comfortably inside the 0.8 s frame interval. The
0.9 s figure came from a `yolov4.onnx` that is **not in this repository**, so it cannot
be re-measured here and is no longer claimed.

### The live relay: real cameras that are not ours

`cameras.json` is the curated table of public live cameras, `pocctl live` is the
command that reads it, and `SP_POC_SOURCE_PUBLIC=true` is the flag the run's own
source sentence is derived from. All three exist because of one honesty problem:
a YouTube live cam is a REAL camera streamed by SOMEBODY ELSE, and the two
sentences available before this were both lies about it — `Relay`'s ("relay of
recorded material, NOT a camera") denies a camera that is demonstrably there, and
the plain live sentence says nothing about whose hardware it is.

```powershell
pocctl live list                       # the table: place, operator, licence, verified date
pocctl live resolve --all              # yt-dlp -g -f bestvideo per row; a failure is named
pocctl live env abbey-road             # the SP_POC_SOURCE_* lines one camera produces
.\scripts\..\..\scripts\live-demo.ps1  # the whole path, one button (repo root)
```

The demo's acts, in the order the plan names them: **the box** (the tools, the
weights, the runtime, and the sidecar's own runtime when that is this run's
path), **resolve** (a curated row becomes a live playlist), **calibrate** (the
timing pass for the path this run uses), **rehearse** (fault day, then a smoke
run over the committed clip), **preflight** (the eleven rows, against the
calibration this run will use), **run** (`watch` over the relay until Ctrl-C),
**prove** (`pocctl prove` on a recorded execution of *this* run: stored facts
in, decision out, zero provider calls), **mark** (the review sheet, and one
verdict whose author is stated), **report** (AC1–AC6 scored from the run's own
JSON, and a one-file artefact in `dist/live-demo/` that carries the AC table,
the source line, the calibration line, the camera row, the licence row, the two
"what this does not prove" lines and the run's still, embedded).

`-TriggerMode sidecar` runs the same path with the Python worker as the trigger:
it starts the worker beside the model's anchors file, calibrates through the
worker's own endpoint, preflights against the sidecar's calibration, and prints
the measured loopback cost in the verdict rather than in a footnote.

Four EarthCam cameras, verified live **14.10.2026**: Times Square North 4K, the Abbey
Road crossing, Dublin, and New Orleans Street. What was verified is exactly what a
human did that day — resolved with `yt-dlp --js-runtimes node -g -f bestvideo` to an
HLS playlist on `manifest.googlevideo.com`, and read with ffprobe (h264, 1920x1080,
30 fps, live). A camera that answered on its verification date is a camera that may be
offline today, and the resolver's error names which one.
Two things the table refuses to do. A row with no recorded licence basis is refused at
load: a public feed being PUBLIC is not the same as being free to record, so every row
states the basis and states in words that no written permission to store clips exists,
and `cleared_for_external_use` is false on every row — a screenshot of a run over these
cameras is a screenshot of these cameras. And the two relay flags cannot both be true,
because one source cannot be a recording and a camera at once.

`-f bestvideo`, not `-f best`: `best` asks for a pre-merged file, and a segmented live
stream has no such format, so it answers "Requested format is not available" against
every YouTube live cam on earth. The pipeline reads frames and drops audio, so video-only
is the correct selection anyway.

**Preflight, green against a real camera, 15.10.2026.** All eleven rows, with the
camera row reading the stream's own facts (h264 1920x1080 @ 30 fps, decode 9.32x real
time) and the detector row carrying a measured p99. Reproduce it in this order:

```powershell
pocctl bench --images testdata\still --data-root <root>   # times the session, writes the calibration
pocctl rehearse all                                       # gate D: seven fault classes
pocctl live env abbey-road | Out-String                   # the camera's environment
pocctl preflight
```

Two defects the calibration closed, both of the same shape — a gate that cannot pass is
a gate nobody reads. The detector row was fed no model, no library and no p99 by ANY
caller, so every detector run failed a row its own required configuration had already
satisfied; and the AI p99 row required a sidecar client from a run that never starts
one. The record itself is `<data-root>/detector-calibration.json`, written by the bench
and refused by preflight when it is absent or when it describes a different build — the
same model file, the same runtime library, the same thread count and the same input
size, compared as files rather than as strings. The first sample of the timing pass is
discarded as the CUDA warm-up: measured here at 600.9 ms for a kernel that costs
13.7 ms once cuDNN has loaded, so a p99 over cold starts would refuse a detector that
demonstrably runs.

**The CUDA provider now puts its own directory on PATH.** On Windows the provider
resolves `cudnn64_9.dll` through the process DLL search order, which covers the
executable's directory, the system directories and PATH — not the directory beside
`onnxruntime.dll`, which is where `fetch-runtime` put it. The consequence was a run
that appended the CUDA provider successfully and then failed its first Conv node, which
is exactly the silent-CPU-fallback shape the provider path claims to refuse.
`ortdetect.PrependLibraryDirToPATH` closes it before the environment is initialised.

### What the live path proved, and what it did not

**What it proved.** The camera is real: `pocctl live resolve` turns a YouTube URL into an
HLS playlist, ffmpeg reads it, and preflight's own row prints the stream's codec and
frame rate. The run records the source line `live http stream (public relay of a real
camera, NOT ours)` — the row in the store, which is the one the report and the log read.

**What it did not.** The detection rate is NOT RUN, and the run says so in its own
words:

	frame 1: the detector refused to answer: this export is a multi-scale ANCHORED head
	and the in-process decoder reads only fused outputs

The pinned Model Zoo `yolov4n.onnx` (`defaultModelSHA256`, 257 MB) publishes three
outputs of shape `[1 52 52 3 85]` — one tensor per stride with coordinates that are
offsets from anchor boxes living in `yolov4-416.cfg`, which is not in the ONNX graph. The
in-process decoder deliberately refuses that family, because guessing an anchor set
produces boxes in the right place with the wrong sizes and every artefact would still
say `fused`. The Python sidecar implements the anchored family.

**2026-10-15: the sidecar path is now runnable end to end, and the two things a
clean clone could not do are both closed.** The anchors file did not exist on this
box and nothing in the repository wrote it: `models/` is gitignored, `demo`'s
`anchorsPathFor` derives `<model>.anchors.txt`, and `config.Load` refuses a missing
one — so a clean clone reached the worker's refusal with nine numbers nobody had
written. The committed copy is now `pyworker/fixtures/yolov4n.anchors.txt` (the
`[anchors]` section of `yolov4-416.cfg`, transcribed, with its provenance and
SHA-256 recorded in that fixture's `anchors` object in `yolov4n.licence.json`), and
`pocctl fetch-model` — and `pocctl bootstrap`, which delegates to it — writes it
beside the weights and checks what it wrote against the pin. The same discipline
the weights are fetched with, applied to the model's own configuration: the file is
identified by content, not by a name.

The path's own cost is measured rather than asserted, because it is a different
number by a factor nobody should guess. `pocctl bench --images <dir> --data-root
<root> --trigger sidecar --ai-addr http://127.0.0.1:8099` drives the worker's own
HTTP endpoint over the still set, discards the first round trip as the warm-up,
refuses to record a p99 over refused round trips, and writes the SAME calibration
record with the trigger field naming the path that measured it. `Applies` compares
the axis and refuses a detector record applied to a sidecar run by name; preflight's
AI p99 row — three-state, so a run with no sidecar cannot fail it — multiplies the
sidecar's measured number when there is one. And `scripts/live-demo.ps1` takes
`-TriggerMode sidecar`, which starts the worker, calibrates through it, preflights
against the sidecar's calibration, runs the relay through it, and prints the
loopback cost in the verdict rather than in a footnote. The framedeg decision the
sentence above named is therefore a measured choice, made by a person, on this box.

Two things the demo caught while it was at it. `pocctl bench` printed
`negative frame, nothing drawn, nothing there` over frames the decoder had refused,
because `Detect` returns its refusal INSIDE the Answer and the loop looked at
`len(ans.Boxes)` — two kinds of zero, printed as one. And `pocctl report` refused the
committed `testdata/groundtruth.json` with `unknown field "$comment"`: the loader was
strict and the struct was missing the key the file uses. Both are fixed, both are tests,
and the second one had been printing NOT RUN at every report for as long as the header
had been in the file.

### What this path does NOT prove

Not a week, and not a VMS. `$CaptureSeconds` seconds proves the source line and the
detector and nothing about uptime, drift or the acceptance criteria. The events are
pushed to xpsim, a simulator, and preflight's own row prints the word "simulator"
rather than letting a reader assume otherwise.

### The two defects the GPU exposed

Both were invisible until a session actually opened, and both are the same shape: a fact
this module **records** in every artefact and does not **act on**.

**RC-29 — a dynamic-axis export could not be run at all.** The committed detector
declares its output dims as `-1`, because the anchor count depends on the input. The
detector pre-allocated every declared output and *refused* the model when it could not,
with a message phrased as protection. What it was actually doing was declining every
modern YOLO export. It now hands the runtime a slice of nils and lets it allocate.

**RC-30 — the input tensor was laid out the wrong way round.** The session worked the
layout out from the model's own declared dims and recorded it — in `Input().Layout`, in
the run report, in `fixtureprobe -detector` — and then built the tensor in a fixed order
regardless. On this NHWC export that is a legible runtime error. The reason it is worth
a test anyway is the quiet version: a tensor of exactly the right number of floats, fed
in the wrong order, decodes to boxes that are **plausible and wrong**, nothing errors,
and every artefact describing the model says the layout was honoured.

The shapes the fix produces — `[1 52 52 3 85]`, `[1 26 26 3 85]`, `[1 13 13 3 85]` —
are byte-for-byte what `pyworker/fixtures/yolov4n.contract.json` already recorded from
a CPU run of this same model, which is an independent check that the layout is now right.

### The system, on one page

```
  camera ──► recorder ──► frame loop ──► TRIGGER ──► line test ──► detection
             (ffmpeg)      (soak.Runner)    │            (detect)      │
                                             │                            │
                        ┌────────────────────┴───────┐                    │
                        │  detector  (internal/     │                    │
                        │  ortdetect, ONNX, cgo)    │                    │
                        │  detector  (pyworker,     │                    │
                        │  Python)                 │                    │
                        │  novelty  (pure Go)      │                    │
                        └───────────────────────────┘                    │
                                                                            ▼
                                          store ──► evidence ──► ledger ──► seal
                                            │                        │
                                            ├──► outbox ──► XProtect ──┘
                                            └──► box_observations ──► replay
```

Three trigger modes, all still selectable:

- **`detector`** — the shipped path. In-process ONNX Runtime, real classes and
  scores per frame, Kalman-filtered tracks, two-stage ghost recovery.
- **`sidecar`** — the per-frame Python classifier. Kept as the cross-decoder
  parity reference: the implementation that produced every number this project has
  reported so far, in a different language against a different Numpy.
- **`novelty`** — pure-Go whole-frame change measurement. Licence-free, no
  weights, and **no class vocabulary at all**, which is why the previous build
  reported zero vehicles.

The AI plane is optional and its absence is a first-class state, not an error.
`not_asked` and `no_objects` are different words and the report prints them
differently; a run that asked nothing and a run that asked and found nothing are
not the same claim.

### Module boundary

`scripts/verify.ps1` holds one import-graph row per package below `internal/soak`.
The load-bearing ones:

- `internal/vlm` may not reach the store or the pipeline. RC-4's replay proof is
  structural **only** because of this: a verifier that can read what somebody said
  last time is a recorder.
- `internal/bench/metrics.go` may not reach anything. Its golden test works
  because it can be checked against hand-computed arithmetic, and a metric that
  read the database could not be.
- `internal/ortdetect` may not reach storage, because the cross-decoder parity
  claim is only meaningful if neither decoder can be handed the other's answer.
- `internal/proposal` is the strictest leaf: `ai.Box`, `detect.Box` and
  `novelty.Box` are ALIASES of it, so all three import it and it may import none
  of them back.

### What changed, and what it cost

The trigger path is no longer a per-frame ONNX sidecar. It is a **pure-Go novelty
engine** (`internal/novelty`) that MEASURES which part of the frame changed, and a
**hosted model** (`internal/vlm`) that is asked **once per event** what is happening
in that region. The split is the honest one:

> Cheap perception proposes; the expensive model verifies.

The Hafnia API documents **no region output and no bounding box** — it returns text.
So the box on a pushed event is ours and measured, and the label on it is the model's
and is attributed as a claim. There is no synthetic model hash anywhere.

The `SP_POC_AI_MODEL`, `SP_POC_AI_ANCHORS` and `SP_POC_SIDECAR_SCRIPT` variables are
still read — `SP_POC_TRIGGER_MODE=sidecar` is a real mode and a useful one, it is how
a threshold gets chosen — but the **shipped pipeline does not require them**, and
`pyworker/` is a measurement tool rather than a dependency.

New commands:

```
pocctl bootstrap               one command from a clean clone: toolchain, model, runtime, material table
pocctl demo --live             the same demo against a live camera and a live VMS
pocctl prove <detection>       recompute one decision from stored facts, zero provider calls
pocctl audit ledger            recompute the hash-chained decision ledger
pocctl audit seal              sign the chain head, or verify a seal
pocctl audit seals             verify every seal against a trusted public key
pocctl audit decisions         the stored decision inputs: score, threshold, measured region
pocctl audit observations      every model's answer, with its attribution and reason code
pocctl probe --live           read-only dial of the XProtect instance; per route: reachable / contradicted / unauthenticated / absent
pocctl vlm-probe              ONE call to the pinned VMS host, no clip; the raw body lands in contracts/hafnia/raw/
pocctl mark <tag>             one human verdict on one pushed event; this is what makes the false-positive rate measurable
pocctl search <text>          what the model SAID, with its clip ref and its attribution
pocctl brief                  one line per hour: frames read, proposals, and whether the model was asked at all
```

### Where this module's red-check evidence lives

Every control in `scripts/verify.ps1`'s table has a transcript — the baseline, the
mutation's failure and the restore, one per control — in
**`poc/active-monitoring/redcheck-transcripts.txt`**, regenerated by
`scripts/redcheck-transcript.ps1`. It is not decoration and it is not the gate: the
ROOT module's own gate (`internal/treestate`) reads both tables and this file and
fails when a control appears in one and not the other. That is how this repository
found out, on 2026-10-15, that the seven controls added for the live relay had been
proved by the POC gate and recorded nowhere: the POC gate says green, the root gate
said the evidence does not exist, and the root gate was right.

---

## מה זה

מערכת אחת ש:

1. קוראת זרם וידאו **פעם אחת**, בתהליך `ffmpeg` אחד, ושני פלטים ממנו:
   הקלטה מתגלגלת (`-c copy`) **ו** צינור MJPEG לניתוח.
2. שולחת פריים RGB גולמי לתהליך Python צד־שרת (sidecar) שמריץ מודל ONNX.
3. עושה זיהוי קו־חצייה עם מעקב IoU ומכונת מצבים.
4. שומר קליפ לכל אירוע, **מתמלל ומאמת שהבייטים תואמים** את ה־SHA-256.
5. דוחק אירוע ל־Milestone VMS דרך ה־REST API הרשמי, **ומאמת בקריאת חזרה** שהוא
   באמת נמצא שם.
6. כותב הכול ל־SQLite במצב WAL, ומדפיס דוח שכל מספר בו נקרא משורה.

## שלוש עקרונות שלא נגעים בהם

| | |
|---|---|
| **קורא אחד, שעון אחד** | תהליך `ffmpeg` אחד הוא הקורא היחיד. Python לא קורא וידאו, לא עוקב אחרי אובייקטים, לא מחזיק מצב. חותכות `ffmpeg` חוזרות לפי **שעון הקיר של Go** — כך הקליט תמיד מראה את הדבר שהפעיל אותו. |
| **רישום לפני אפקט** | שורת הזיהוי נכתבת ונשמרת **לפני** חיתוך הקליט ולפני הדחיפה. `kill -9` בכל רגע משאיר מצב שניתן להמשיך ממנו, לא אירוע שאבד. |
| **תשובה חסרה אינה תשובה שלילית** | ארבע תשובות נפרדות: `no_objects` (המודל רץ ולא מצא), `degraded` (נשלח, נכשל/איחר), `not_asked` (לא נשלח), `cannot_tell` (הגיע ולא מפורש). **אין** מצב שבו כישלון הופך ל־"אין כלום". |

---

## מה צריך להיות מותקן

```powershell
# כל הכלים נמצאים ב־PATH אצלכם; ה־Go וה־gcc צריכים התקנה מפורשת.
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"

go version          # 1.26 ומעלה
ffmpeg -version     # חובה (D2)
ffprobe -version    # חובה
py -3 --version     # חובה

# תלויות Python של הצד־שרת (D2)
py -3 -m pip install -r pyworker/requirements.txt
```

> **שים לב:** `go`, `gofmt` ו־`gcc` **אינם** ב־PATH ב־Windows רגיל. זה לא באג —
> זה המצב שה־`scripts/verify.ps1` נועד לו. הוא מאתר כל אחד בעצמו ומדפיס **SKIPPED**
> עם הסיבה כשמשהו חסר, ולא "עבר בשקט".

---

## התחלה מהירה (בדיקה, שלוש דקות)

```powershell
cd poc\active-monitoring
.\scripts\verify.ps1                                   # השער היחיד של המודול
.\bin\pocctl.exe probe                                  # טבלת ביטחון החוזה מול Milestone
.\bin\pocctl.exe rehearse --data-root C:\poc-rehearsal  # שער D: התנהגות בכשל
.\bin\pocctl.exe demo                                   # הדמו במלואו, כולל המסך
```

`pocctl demo` מריץ את הצינור האמיתי, מראה את המסך האמיתי, **ומציג אירוע שהוכח שנמצא
בקריאת חזרה**. הוא גם מסמן `mode=demo-replay` בכל מקום, כי צופה לא אמור לחפש
את זה.

---

## שבוע ימים

```powershell
# 1. חומר מסומן
scripts\make-bird-clip.sh        # קליפ סינתטי, ניתן לשחזר ביט־לביט
scripts\make-loop.sh             # מדפיס expected_events_per_day *לפני* הרצה

# 2. מדדים — מבנה ההרצה המלא
$env:SP_POC_MODE='live'
$env:SP_POC_SOURCE_KIND='loop'
$env:SP_POC_SOURCE_ARG='testdata\loop.mp4'
$env:SP_POC_SOAK_TARGET='168h'
$env:SP_POC_LABEL='week-1'
.\bin\watch.exe                  # יוצא עם Ctrl-C

# 3. הדוח — ניתן לייצר מחדש מהמסד
.\bin\pocctl.exe report --data-root C:\poc-data
```

`SP_POC_SOAK_TARGET` הוא **הכרזה שנדרשת מראש**. הרצה קצרה יותר מהיעד מדווחת
`PROVISIONAL`, לא `PASS`, כי משהו שלא הסתיים אינו יכול לסיים טענה על משהו שארך
שבוע.

---

## כל משתנה `SP_POC_*` חייב להיות מוגדר

המודול **מסרב לעלות** אם משתנה חסר. אין ברירת מחדל שמשנה התנהגות, ואין
"ברירת מחדל שסבירה". כל הסרטוב שאתם רואים כאן נכתב כדי לומר למפעיל **מה ייפגע
ולמה**, לא רק שמשהו לא בסדר:

```
SP_POC_DATA_ROOT: not set; without it there is nowhere to put the SQLite WAL,
the rolling segments or the evidence store, and the run cannot record anything;
set it with a path on a LOCAL disk
```

כל המשתנים במקום אחד: `internal/config/config.go`. כל אחד מהם מוגדר בטסט
(`TestEveryVariableIsRequiredByName`) שמוחק את המשתנה **אחד** ודורש שהסירוב יציין
אותו בשמו.

---

## קריטריות הקבלה

שלוש גריטות בלבד: `PASS`, `FAIL`, `NOT RUN`. `NOT RUN` תמיד כולל "מה חסר", ואף פעם
אינו נכתב כ־`PASS`.

| # | קריטריון | החישוב בדיוק כפי שהוא מחושב |
|---|---|---|
| `AC1` | אפס נפילות | `process_exits == 0` **and** `count(heartbeat_gap > 30s) == 0`. אפס מוחלט. לא "99.9%". |
| `AC2` | אפס אירועים אבודים | `count(detections where status != 'done' and age > 5m) == 0` |
| `AC3` | עדות שלמה | `count(clip_stored where readback_sha256 != sha256) == 0` |
| `AC4` | דחיפה בלי אובדן ובלי כפילות | `count(detections with no outbox row) == 0` **and** `count(attempts with no result) == 0` **and** `count(duplicate ids per tag) == 0` |
| `AC5` | משאבים תקופים | `rss <= declared` **and** `db <= declared` **and** `disk <= declared` **and** `cpu <= declared` |
| `AC6` | אין תשובות מומצאות | `count(unparsed responses) == 0` **and** `count(healthy from a failed read) == 0` **and** `ai_p99 <= declared` |
| `AC7` | תקלות מדומות התנהגו כמצופה | כל תקלה שהוזרקה חייבת להכיל תוצאה צפויה ונצפית |

שלוש הערות שחשובות יותר מרוב המספרים:

- **`AC5`'s CPU** הוא `NOT RUN` אם לא הוגדר תקרה. לא `PASS`.
- **`AC7`** הוא `NOT RUN` אם לא הוזרקה תקלה. הוא **לא** מקבל `PASS` אוטומטית
  מהעובדה שמערכת לא נפלה. הפרה קודמת לשעון ההרצה.
- **`AC3` ו־`AC4`** הן `NOT RUN` אם לא נמצא אירוע אחד, כי קריטרייה שלא נוצה עליה
  לא נמדדה. הרצה שלא מצאה כלום אינה מדווחת כ־"הכול עבר" — היא אומרת `NOT RUN`.

---

## מה שה־POC **לא** מוכיח

זה הקטע החשוב ביותר במסמך הזה, והוא נכתב כרשימה כדי שאף עובד לא יעלה אותו
לכותרת בעתיד:

- ❌ **"עבר את כל מחלקות התקלה"** — זו רשימה של תקלות שאדם הזריק.
- ❌ **"אין אזעקות שווא"** — איש לא סימן אף אירוע כשווא, ולכן **אין מספר**.
- ❌ **"אומת מול Milestone"** — שום שרת VMS היה נגיש מעולם.
- ❌ **"100% זיהוי"** — אין אמת מוחלט; יש שיעור זיהוי.
- ❌ **"המסך מראה שהכול בסדר"** — המסך מראה מה שיש במסד הנתונים.
- ❌ **"מזהה עופות"** — קליפ העוף הוא `synthetic`: מלבן נע.
- ❌ **"פלט ל־XProtect"** — מה שהוכח הוא שה־`POST` תואם לחוזה ושקריאת החזרה
  מצאה אותו.

כל מסמך שהמודול מייצר — הדוח, המסך, תדפיס ההדגמה — מדפיס רשימה זו במלואה.

---

## ה־gate: `scripts/verify.ps1`

זה **ה־gate היחיד** של המודול. שורש המאגר **אינו** בונה מודול מקונן — `go build ./...`
שם פשוט מדלג את `poc/active-monitoring/`, ולכן אין לו זכות לומר דבר עליו. הסקריפט
גם בודק שהשורש **אכן** לא בונה אותו, כי גבול שקיים רק בכיתוב אינו גבול.

הוא רץ, בסדר זה, ועוצר בכשל הראשון:

| שלב | מה הוא בודק |
|---|---|
| toolchain | `go`, `gofmt`, `gcc` (ל־`-race`), `ffmpeg`, `ffprobe`, Python. כל אחד מאותר **במפורש**. חסר ⇒ `SKIPPED` עם סיבה, לא "עבר". |
| build | `go build ./...` |
| format | `gofmt -l` חייב להדפיס **כלום** |
| vet | `go vet ./...` |
| boundary | אין import של הפלטפורמה; השורש לא בונה את המודול; `internal/xprotect` לא מכיר את `xpsim`; `internal/detect` ו־`internal/ai` לא מכירים אחסון או את התול |
| tests | `go test -race -p 1 -count=1 ./...` |
| sidecar | `py -3 -m unittest` על `pyworker/` |
| **red check** | **כל בקרות ה־red check של הסקריפט מוסרות מהקוד**, וכל אחת חייבת להפוך את הסביבה ל־אדום. המספר מודפס ע"י השלב עצמו, לא כתוב כאן — מספר שנכתב במסמך הופך לשקר ביום שמוסיפים בקורה אחת־עשרה |

### ה־red check

זה השלב שהופך את כל הששה האחרים משמעותיים. **הוא מוחק קו מהקוד בייצור**, לא
טסט. מחיקת טסט לא מוכיחה דבר: סביבה עם טסט אחד פחות עדיין ירוקה, והדבר שהטסט
שמר עליו עדיין לא מוגן. מחיקת הבדיקה עצמה מוכיחה שהטסט **שומר** עליה.

שלוש הראשונות היו קיימות לפני עבודת ה־VLM, ואחריהן `RC-1` ועד `RC-20` — כל אחת
מסירה הכחשה אחת מסוימת. **הרשימה המלאה והעדכנית היא טבלת ה־`$controls` בתוך
`scripts/verify.ps1`**, לא הטבלה שלהלן: רשימה שמתוחרת עם הקוד היא רשימה שתתיישן,
והשלב עצמו מדפיס כמה בקרות הוא באמת הריץ.

| נמחק | למה הסביבה חייבת להאדים |
|---|---|
| `internal/evidence/store.go` — בדיקת צורת ה־SHA-256 | בלי היא שם שנבחר על ידי קורא מתקבל, ושתי תקלות יכולות להצביע על אותו אובייקט |
| `internal/store/detections.go` — ה־`WHERE … status NOT IN ('done')` | בלי היא זיהוי שהסתיים אפשר להזיז שוב, וחצייה אחת מקבלת שתי סיפורים |
| `internal/detect/engine.go` — רצועת הסבילות | בליה כל פריים "על הקו", ולא חוצה דבר: ריצה מדווחת אפס אירועים **ואין שום שגיאה** |
| `internal/vlm/parse.go` — ניקוי גזירה כשלא נשאלו | מפעיל רואה "לא קרה דבר" כי **אנחנו** לא הצלחנו לשאול |
| `internal/store/observations.go` — אותו כלל בכתיבה | כותב שני שכח את הפרסר מכניס את אותה שקר לטבלה שההוכחה קוראת |
| `internal/store/ledger.go` — השוואת הגיבוב | "הלדגר שלם" הופך להערה |
| `internal/store/ledger.go` — הטריגרים של append-only | אדם עם `sqlite3` עורך החלטה שכבר נרשמה |
| `internal/soak/replay.go` — אפס קריאות ספק בהשרדה | "ניתן להשמע מעובדות שמורות" מפסיק להיות טענה על הקוד הזה |
| `internal/soak/snapshot.go` — השיוך הפריים | התמונה שבקובץ היא פריים אמיתי מאותה מצלמה ב**רגע אחר** |
| `internal/xprotect/client.go` — סירוב תיבת אפס | Milestone מצייר כלום בזמן שמסד הנתונים אומר שהאזור אמיתי |
| `internal/vlm/vlm.go` — סירוב חוצה־אזור | אתר ב־EU שולח וידאו למארח ה־US |

---

## מה נבדק מול החוזה של Milestone

`contracts/xprotect/` מכיל את המפרטים של Milestone, **כפי שהורדו מהאתר שלהם בעצמם**,
עם תאריך השליפה והרישיון (MIT, מצוין בתוך הקובץ). `pocctl probe` מדפיס את טבלת הביטחון.

כל נתיב וכל שדה שהלקוח שולח נבדקים בטסט
(`internal/xprotect/contract_test.go`) מול הקובץ שהורד. **שינוי נתיב או שדה גורם
לטסט להיפול, ואין צורך בשרת כלל כדי שהוא ייפול.**

שלושה מקומות שבהם הקוד הקיים של הפלטפורמה שגויים, ולכן הקוד שלו **לא** שוכפל כאן:

1. `internal/milestone/routes.go` משתמש ב־`/api/v1/…`. ה־API האמיתי הוא תחת
   `/API/rest/v1/`.
2. `internal/milestone/writeback.go` שולח
   `{"bookmarks":[{idempotencyKey, deviceRef, time, label, description}]}`. הגוף
   שפורסם הוא `newBookmarkBody{header, devicePath{type,id}, timeTriggered, …}`.
   `idempotencyKey` ו־`deviceRef` לא קיימים.
3. **אין לפלטפורמה כלל נתיב "הפעלת אירוע".** היא קוראת אירועים ומוציאה סימניות;
   ה־POC מוסיף את זה, וזה בדיוק מה ש־MIP plug-in קיים כדי לעשות.

---

## מפת המודול

| חבילה | אחראית ל | יודע על |
|---|---|---|
| `internal/config` | כל הקריאה היחידה לסביבה | כלום |
| `internal/source` | בניית שורת הפקודה של `ffmpeg`, `ffprobe`, טבלת יכולות | כלום |
| `internal/recorder` | תהליך `ffmpeg` היחיד, צינור MJPEG, קטעים, גלילת תקציב דיסק, חיתוך קליפים | `internal/source` |
| `internal/ai` | לקוח הצד־שרת, אוצר האחווה, p50/p95/p99 | כלום |
| `internal/novelty` | **מנוע החדשנות המקומי** — מודד את האזור מפיקסלים, בלי ONNX ובלי Python | כלום |
| `internal/vlm` | **לקוח Hafnia** — העלאה, קריאה, פריסה, סימון אזור, תקציב יומי | `contracts/hafnia` |
| `internal/detect` | מעקב IoU, מכונת מצבי חציית קו, קירור | כלום |
| `internal/store` | SQLite ב־WAL, outbox, **לדגר משורש גיבוב**, חתימת ed25519, הפקה חוזרת מהמסד | כלום |
| `internal/evidence` | מאגר מבוסס תוכן, הגנות נתיׁ, ניקוי לפי גיל | כלום |
| `internal/xprotect` | תבניות חוט Milestone, דחיפת **אירוע ו־bookmark** עם קריאת־חזרה | `contracts/xprotect` |
| `internal/xpsim` | מדמה VMS מקומי, כולל קריאת־חזרה ל־bookmarks | כלום — הוא **שרת**, לא משתתף |
| `internal/supervisor` | תהליכי הילדים, backpressure, `stream_gap` | `internal/recorder` |
| `internal/soak` | preflight, קריטריות, מנוע ההרצה, `prove`, הדוח | הכול |
| `internal/ui` | המסך, **הגזירה שרופה בפיקסלים**, כותרות אבטחה | `internal/store` |
| `pyworker/` | צד־שרת Python: פריים → תיבות. **כלי מדידה**, לא במסלול ההפעלה | `numpy`, `onnxruntime`. **לא** OpenCV, **לא** Pillow |

`internal/vlm` ו־`internal/novelty` הם **עלים**: הגרף־הייבוא ב־gate אוסר עליהם לייבא
מסד, צינור, זיהוי או ממשק. לוקח ה־VLM אינו יכול לקרוא מהמסד ולהחזיר מה שמישהו אמר
בפעם האחרונה — וזה בדיוק מה ש־`pocctl prove` טוען שאין בו.

`internal/xpsim` **לא** מיובא על ידי `internal/xprotect`. זו לא בדיקת סגנון: הלקוח
אינו יודע שיש מדמה, והמדמה אינה יודעת שיש צינור. הגבול נבדק ב־**גרף הייבוא** על
ידי ה־gate, לא בחיפוט טקסט.

---

## שאלות שיגיעו, והתשובות

**"זה מזהה עופות?"**
לא. קליפ העוף הוא מלבן נע שנבנה מפריימים של `ffmpeg`. הוא מסומן `synthetic` בכל
מקום שיש בו מספר.

**"האירוע הגיע ל־מילסטון?"**
לא. יעד הדחיפה הוא מדמה מקומי. המסך והדוח אומרים זאת במילים בכל עמוד. מה שכן
הוכח: שה־`POST` תואם למפרט שהורד מהאתר של Milestone, ושקריאת חזרה מצאה את
האירוע.

**"הדטקטור היה ירוק?"**
אם `verify.ps1` הדפיס GREEN, הוא הריץ גם את ה־red check — כלומר כל בקרה שהשלב
הדפיס הוסרה מהקוד והסביבה הפכה לאדומה כל פעם. אם הוא הדפיס GREEN **עם SKIPS**, קרא את הדללת
לפני שאתה מתייחס אליו כ־עבר.

**"למה לא שתי הרצות במקביל?"**
כי זו לא סיבה. התוכנית אומרת חד־מקור, והשתנות של סביבת הריצה היא רעיון שרוצה
להיכנס לתוך מדידה. אם יש סיבה אמיתית, היא נכתבת ב־ADR לפני השינוי, לא אחריו.

**"אפשר להתחיל קיצור את התקציב?"**
אפשר, והדוח יסמן `PROVISIONAL` — לא `PASS`. `PROVISIONAL` הוא אמרה שהריצה
טרם הסתיימה, לא `PASS` רך.

---

## מה נמדד בהרצה אמיתית (30.09.2026)

שלוש הממצאים מהרצה אמיתית, רשומים כאן כי הם משנים את מה שהרצה על חומר
סינתטי שווה:

**1. מודל COCO לא רואה את החומר המחויב.** הרצה אמיתית עם `yolov4.onnx` מול
`car-crossing.mp4` החזירה **אפס תיבות** בשלושה רגעים. מלבן צבעוני שטוח על רקע
`testsrc` אינו תמונה, ומנבא שמופה על תמונות צודק לא לראות בו כלום. זו לא תקלה
וזו לא כיוון יתר על כיוון — לכן שדת ה־`real` ב־`labels.json` ריקה.

**מה זה אומר בפועל:** הרצה על חומר סינתטי מפיקה **אפס אירועים**, והדוח אומר
את זה: `AC2`, `AC3` ו־`AC4` יוצאים `NOT RUN` עם הסיבה "אין אירוע שנרשם". זו
**התנהגות מכוונת ונכונה**, וזה הדבר החשוב ביותר להבין לפני שמראים למישהו הרצה
על חומר סינתטי.

**נתיב הזיהוי מוכח בדרך אחרת.** בדיקת אינטגרציה ב־`internal/soak` מפעילה את כל
הצינור האמיתי — `ffmpeg` אמיתי, מקליט אמיתי, מזהה אמיתי, מסד אמיתי, מאגר
תוכן אמיתי, לקוח Milestone אמיתי, סימולטור אמיתי — ומתחלף רק את **דיוק המודל**.
היא מראה פריים שהופך לחציית קו, לשורה מחויבת, לקליפ מאומת, לאירוע נדחף, ולקריאת
חזרה שמוצאת אותו תחת התג שמופק מתוכן. שלמות הקליט עצמה — קידוד מחדש, קריאה
בייט־לבייט, hash, ביטול כפילויות, ניקוי — מוכחת בנפרד ב־`internal/recorder` וב־
`internal/evidence` מול `ffmpeg` אמיתי.

**2. תפוקה היא האילוץ האמיתי, והיא נמדדת.** `yolov4.onnx` ב־416×416 על המעבד
של המכונה הזו לוקח **כ־0.9 שניות לפריים**, מול מו־תקן 0.8 שניות. ב־`--ai-fps 5`
הצינור מתמלא תוך שניות והריצה מסתיימת ב־`ai_pipe_backpressure` — שהוא בדיוק
ההתנהגות המתוכננת: תקלה שנרשמה, `ffmpeg` נהרג, ואין שום drop שקט. ב־
`--ai-fps 1` הצינור נשאר בעומק 0–5 והריצה מגיעה ליעד עם `AC1`, `AC5` ו־`AC6` עוברים
על מספרים מדודים. הריצו `pocctl cap` כדי למדוד על המכונה שלפניכם.

**3. פער heartbeat בן 71 שניות היה ארטיפקט, לא נפילה.** גרסה ראשונה של לול הריצה
השתמשה ב־goroutine אחד גם לפריימים וגם ל־heartbeat, ולכן הסטת מודל ראשונה
שתקה את אות החיות ו־`AC1` נפל מסיבה שאין לה קשר לאמינות הצינור. ה־heartbeat
רץ עכשיו ב־goroutine נפרד, ול־`internal/soak/loop_test.go` יש בדיקה בדיוק לזה.

**4. כל אירוע שנדחף נשאר `boundingBox` של אפסים, בעוד השורה במסד הנתונים החזיקה
מספרים אמיתיים.** `internal/soak/runner.go` קבע `Box: xprotect.Box{}` בדיוק כשהוא
בונה את האירוע. כל 286 הבדיקות היו ירוקות, המזהה מצא אובייקטים אמיתיים, והדחיפה
הצליחה — ועדיין כל מה שיצא לחוט היה ארבעה אפסים. זהו הדבר שהיה הכי גרוע בדמו:
ה־bounding box הוא מה שמצייר את האובייקט ל־Smart Client, והצגת אפסים שם היא
הצגה שהמוצר לא עובד. עכשיו התיקון הוא אותו חישוב בדיוק ש־`detections.box_*` משתמש
בו (אלפיות), והבדיקה ב־`internal/soak/pipeline_e2e_test.go` משווה את האובייקט
שנדחף לשורה שלו — **השוואה, לא מבט על המסך**. ההשוואה נשברת אם מחזירים את
האפסים.

**5. שדה אחד שהסיידקר שלח, וה־POC לא זיהה דבר בכלל.** הסיידקר החל לשלוח
`input_w`; בצד ה־Go הפענח מפענח **קשוח** (`DisallowUnknownFields`) — וזה נכון,
זה מדויק, וזה בדיוק מה שהכיס את התקלה. כל תשובה נדחתה, כל הזמן. הריצה דיווחה
`cannot_tell` על כל פריים, וזה **סטטוס מתוכנן** — הוא נראה כמו ה־tripwire עושה
את שלו. הבדיקות היו ירוקות. ושתי הרשעות שהיו אמורות לתפוס את זה לא ירצו:
`AC6` "אין תשובות מומצאות" **לא יכול היה להיכשל** (`AICounters()` לא היה לו
קורא), והחשבון של המזהה לא נחשב בשום מקום (`detect.Counters` נבדק אבל `Observe`
לעולם לא נקרא). היום החוט מועמד משני הצדדים, `report.md` מספר **למה** אין
תקרות, והריצה הבאה על חומר אמיתי החזירה: 366 פריימים נשאלו ו־366 נענו, תקרה
אחת, קליפ שנאומת בקריאה חזרה, דחיפה עם תוצאה, **PASS**.

---

## מה נמדד: צורת המודל, הרישיון, והגבלות החומר

שלושה דברים שלא נמדדים בתוך ריצה — הם תכונות של **הבנייה** ושל **החומר**, זהים
לכל הרצה, והם שאלות שישאלו בחדר. כל אחד מהם רשום בקובץ שמחויב לצד הדבר שהוא
מתארים, כדי שדף אחד לא יוכל לטעון צורת מודל או רישיון שהרשומה שלידו לא תומכת
בו.

### המודל שנמפק מכנה `yolov4n.onnx` הוא **לא** YOLOv8

| | |
|---|---|
| **שדות לזיהוי** | 85 — כלומר יש `objectness` בעמודה 4. ל־YOLOv8 יש 84 ואין לו עמודת objectness |
| **פלטים** | שלושה, במדרגים 52 / 26 / 13 על קלט 416×416 ב־`NHWC` — כלומר **anchored**. YOLOv8 ממוזג, פלט אחד, ללא anchors |
| **שמות מחלקות** | **ריקים**. לייצוא אין מטא־נתונים של מחלקות, ולכן המפענח נופל חזרה למילון COCO־80 לפי אינדקס |
| **ספק ביצוע** | `CPUExecutionProvider` בלבד |

הכתובת המלאה: [`pyworker/fixtures/yolov4n.contract.json`](pyworker/fixtures/yolov4n.contract.json),
הופקה ע"י `pyworker/probe_model.py`.

### הרישיון של המודל: **המקור נמדד**, והוא מ־ONNX Model Zoo

**מה נמדד (11.10.2026), ולא מה שהוערך.** המשקלות שעל המכונה הזו חושו ל־SHA-256
(257,470,589 בתים), וקובץ `yolov4.onnx` של ONNX Model Zoo הורד וחושב: **אותו
hash, אותו גודל**. שני זרמי בתים שונים, digest אחד. אין כאן דבר שדורש להאמין לו
כדי שהוא יהיה נכון — גם ה־URL וגם ה‑SHA‑256 הם בתים שאפשר להוריד ולבדוק, וכל
אדם יכול לעשות את הבדיקה בעצמו.

בדיקה שנייה, עצמאית מהראשונה: העמוד שמפרסם את הקובץ מפרסם את הצורה שלו —
`(1, 416, 416, 3)` ב־`NHWC` פנימה, `(1, 52, 52, 3, 85)` החוצה, שלושה מדרגים —
וזו בדיוק הצורה שרשומה ב־[`pyworker/fixtures/yolov4n.contract.json`](pyworker/fixtures/yolov4n.contract.json)
מן הבתים האלה, עוד לפני שהיה ידוע דבר על המקור. זיהוי שנמדד פעמיים, בשתי
דרכים עצמאיות.

**מה נסגר ומה לא.** הרישיון **רשום**, כמו שהעמוד עצמו מצהיר: `SPDX-License-Identifier:
MIT` וסעיף License שקורא `MIT License`. מה שזה מחייב הוא הודעת MIT, וארבע שורות
הייחוס שהעמוד מפרסם נשמעות כפי שהן והועברו לכל שימוש מחוץ למכונה. מה **לא** נסגר:
העמוד הוא בעל הייצוא, והוא עצמו ממקם את מה שהייצוא הומר ממנו
(`darknet`, `tensorflow-yolov4-tflite`). ההחלטה להסתמך על הודעת MIT היא החלטה
של אדם, והיא לא החלטה שקובץ הזה יכול לקחת — היא רק דורשת שיכתוב אותה מישהו.

הרשומה: [`pyworker/fixtures/yolov4n.licence.json`](pyworker/fixtures/yolov4n.licence.json),
עם `origin.url`, `sha256`, `licence_source` ו‑`licence_recorded_on`.

### השם של הקובץ הוא עדיין שגוי — וזה רשום במקום שינוי

השם הוא `yolov4n.onnx`. אין `yolov4n` בסט המאושר של ה‑zoo ולא היה מעולם: ה־`n`
היה המצאה של המאגר הזה, נותר מהכתיב הקודם שלו (רשום בקובץ הרשומה, יחד עם הסיבה
לשינוי). ה‑asset המקורי נקרא
`yolov4.onnx` והוא ה‑YOLOv4 השלם ב‑416; קובץ ה‑anchors שלידו הוא סעיף
`[anchors]` של `yolov4-416.cfg`. הקובץ **לא** שינה את שמו, כי שינוי נתוב ש־
`SP_POC_DET_MODEL`, `fetch-model --out`, ה‑fixtures של הסיידקר וסקריפט ה‑gate
כולם מכנים הוא שינוי עם שטח השפעה משל עצמו — ולכן הוא רשום כאן במקום שייעשה
בשקט. מה שכן חדש: כל מקום שמזכיר את הקובץ יכול להצביע על הרשומה וללמוד בדיוק
אילו בתים הוא מדבר עליהם, לפי digest.

### החומר בדמו **אינו נבחר ברירת מחדל**

אין ברירת מחדל. הדמו **היה** נפתח עם `testdata\youtube\sTF-6_xinUU.mp4` — צומת לילה
אמיתי, כלי רכב אמיתיים, ושורה ב־[`testdata/labels.json`](testdata/labels.json)
שאומרת בשדות ולא בפרוזה: `licence` **לא רשום**, `cleared_for_external_use`
**false** — קריא על המכונה הזו, לא ניתן להצגה לאף אחד מחוצה לה. ההדגמה היא דבר
שמראים לאנשים, ולכן ברירת מחדל שמוסרת כך היא בדיוק הבעיה ש־B2 פותר. הפתרון
אינו ברירת מחדל אחרת: `--clip`.

הדמו מסרב **בשם** ומדפיס את הטבלה: מה מחויב, מה הרישיון שלו, ואם הוא מורשה
להיות מוצג.

| | |
|---|---|
| `car-crossing.mp4` | `synthetic`, רישיון **רשום** (ffmpeg `testsrc` + `drawbox` — אין בעל זכויות לשאול), נאחזר 2026-10-01 |
| `bird-synthetic.mp4` | `synthetic`, רישיון רשום |
| `injection-hostile.mp4` | `synthetic`, רישיון רשום, **מקור התקפה מכוונת** — לא קליפ דמו |
| `youtube/sTF-6_xinUU.mp4` | `real`, רישיון **לא רשום** — השורה נשמרת כ**הרשומה של למה אין ברירת מחדל** |

**והמגבלה שנשארת:** מודל COCO מחזיר **אפס תיבות** על מלבן סינתטי. לכן הרצה על
החומר המחויב היא הרצה ריקה **אמיתית**: `AC2`, `AC3` ו־`AC4` יוצאים `NOT RUN`
עם הסיבה, לא `PASS`. עד שייכנס חומר בעל תנועה אמיתית ורישיון שניתן לציטוט
(`B1`), זו התשובה הכנה ביותר שיש.

### נתיב ה־RTSP הוכח, אך **אף מצלמה לא היתה מחוברת**

`pocctl demo --rtsp` מפרסם את אותו קליפ דרך `mediamtx` וקורא אותו דרך סוקט
חי. זה מוכיח את **התעבורה**. מקור ההזנה על המסך, ביומן ובדוח הוא ביטוי
נגזר: `live rtsp (relay of recorded material, NOT a camera)`. שום מצלמה לא
חוברה לעולם, ושום מסמך כאן לא טוען אחרת.

---

## מה נמצא בבדיקת איכות (QA)

עשרה באגים נמצאו ע"י הרצת כל נתיבי הפקודה שלא נבדקו קודם — אף אחד לא היה נראה מהטסטים. כולם תוקנו, ולכל אחת מהם נוספה בדיקה שנשברת בלי התיקון.

| מה שבר | ההשלכה |
|---|---|
| `flagSet` קרא `os.Args[2:]` בעצמו | `poc soak status` ו־`poc soak report` נכשלו ב**כל** הרצה: `"status" is not a flag` |
| `pocctl soak start` העביר את כל הדגלים לשני תהליכים | `--data-root` נדחה ע"י preflight, והפקודה לא יכלה לעבוד בכלל |
| `soak start` היה סביבתי-עגולה לא יכולה | preflight דורש "תחזורת תקלות ירוקה", והתחזורת רצה אחריו — שער שאי אפשר לפתוח |
| `zone --pick` דרס קובץ zones ידני | **הרס את `testdata/car-crossing.zones.json`**. עכשיו הוא מסרב, ועם `--force` שומר את הישן כ־`.replaced` |
| `zone --pick` רנדר still כ־MJPEG | ffmpeg 8 מסרב לקודד YUV שאינו full-range — **הפיקר מעולם לא עבד**. עכשיו PNG |
| `watch --config` נכשל | הסריקה התעצבה *לפני* ש־os.Args נכתב מחדש, והבדיקה על סודות עברה **ריקה** כי הפקודה מתה לפני שהדפיס דבר |
| preflight היה לו **העתאה שנייה** של בדיקת המפענח | עמודת היכולת סיפרה `h264: present`, והשער אמר `ABSENT` — כי תו היכולת משתנה לפי build |
| מדידת שטח פנוי על תיקייה שעדיין לא נוצרה | SQLite מחזיר `out of memory (14)` — והשער דחה מכונה עם 900 GB פנויים בגלל "אין מספיק מקום" |
| מצב השעון דרוש דגל שני לאותו עובדה | מי שהגדיר `SP_POC_CLOCK_SOURCE` נדחה ע"י `--clock-state` |
| `xpsim` כבר היה על הפורט | התחזורת **של מישהו אחר** על הפורט, ולא בדקה כלל. שער שאפשר להכריע לפי מי שאקרא — אינו שער |
| עמודת `Why` הייתה ריקה בדיוק כשהבדיקה עברה | כלל שמפסיק להסביר את עצמו בדיוק כשאיש פחות ערנין לכך |

`pocctl soak start` עכשיו ירוק לגמרי: **תחזורת תקלות 7/7**, **preflight 11/11**, והוא מדפיס את פקודת ה־`watch` המדויקת להרצה.