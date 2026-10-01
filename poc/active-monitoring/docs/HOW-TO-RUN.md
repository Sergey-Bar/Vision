# How to run it

Three commands, on a clean checkout. If any of them fails, the failure says what
is missing and what breaks without it — that is the whole design of the
configuration layer, and it is why you do not have to read the source to get
started.

## 0. Prerequisites

```powershell
# Go. Not on PATH by default on Windows.
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"

go version            # 1.26 or newer
ffmpeg -version       # required (D2: a hard runtime dependency)
ffprobe -version      # required
py -3 --version       # required
```

`gcc` is optional and only for the race detector. Without it the gate prints
**SKIPPED** for that step and is otherwise green — a skip is not a pass.

## 1. Build

```powershell
cd poc\active-monitoring
go build -o bin\pocctl.exe ./cmd/pocctl
go build -o bin\watch.exe   ./cmd/watch
go build -o bin\poc.exe     ./cmd/poc
```

**This comes before everything, because the binaries are not in git.** `bin/` is
gitignored, so on a fresh clone `.\bin\pocctl.exe` does not exist yet. A document
that opens with a command from `bin\` is a document whose first line fails.

## 2. The gate

```powershell
.\scripts\verify.ps1
```

This builds, formats, vets, checks the module boundary, runs every test with the
race detector, runs the sidecar's Python tests, and then removes three controls
from the production code to prove the suite goes red without them.

It is the only gate. The repository root's `verify.ps1` does not build this
module — the root's `go build ./...` skips a nested module — which is why the
gate lives here and why it checks that the root really does skip it.

## 3. The model

Not in git. It is 250 MB and downloaded, and a model with no recorded
provenance is a model nobody checked.

```powershell
.\bin\pocctl.exe fetch-model --url <your-onnx-url> --out models\model.onnx
```

Any ONNX detector works. Two output layouts are supported and **detected from the
tensor's shape**, never from the file name — a YOLOv8 `(1, 4+nc, N)` and an ONNX
model-zoo `(1, 52, 52, 3, 85)` are both read correctly, and logits and
probabilities are told apart by their range.

## 3. Run it

```powershell
# Three minutes, end to end, against a local simulator.
.\bin\pocctl.exe demo
```

The demo runs, in this order, and refuses to continue if a step fails:

1. checks the material, the model and the zone file are present
2. builds `pocctl`, `watch` and `poc`
3. starts the local push-target simulator
4. **injects every modelled fault and checks each one behaved** — gate D, before
   the screen
5. runs the real pipeline with the screen on `http://127.0.0.1:8097`
6. reads back from the simulator what actually landed

## The individual commands

| command | what it does |
|---|---|
| `pocctl cap` | the measured capability table: codec, decoders, decode cost, disk budget, and the AI round-trip p99 on **this** machine |
| `pocctl probe` | the contract confidence table. `--live` dials the instance read-only and reports per route |
| `pocctl vlm-probe` | **one** call against the pinned VMS host. No clip. The raw body lands in `contracts/hafnia/raw/` whatever happened, including a failure |
| `pocctl preflight` | the full preflight table; refuses to start by name |
| `pocctl rehearse` | the fault rehearsal on its own |
| `pocctl soak start` | preflight, then gate D, then tells you the `watch` command |
| `pocctl soak status` | the live state of the newest run, from the store |
| `pocctl soak faults` | the six LOCAL fault classes, as against the seven provider ones |
| `pocctl report` | regenerates the report from the store |
| `pocctl search <text>` | what the model **said**, with the clip ref and its attribution, plus the four absence states of the window |
| `pocctl brief` | one row per hour for a run: frames read, proposals, and whether the model was asked |
| `pocctl mark <tag>` | one human verdict on one pushed event; this is what turns the false-positive rate from `NOT RUN` into a number |
| `pocctl zone --pick <clip>` | a still with a coordinate grid, to draw a line on |
| `pocctl sim` | the push-target simulator on its own |

### The two probes

Both are short, both are meant to be run **before** a demonstration rather than after
one, and neither changes `status: asserted_not_verified` in any contract file.

```powershell
# The host, the path and the auth scheme. ONE call, no footage, no quota.
$env:SP_POC_VLM_API_KEY='...'      # never a flag: a credential on a command line is
$env:SP_POC_VLM_MODEL='...'        # a credential in a shell history and a screenshot
.\bin\pocctl.exe vlm-probe --print-body

# The VMS: credentials, the event-type list, the tag filter, the bookmark read-back.
# READ-ONLY. There is no flag that makes it write.
.\bin\pocctl.exe probe --live
```

`vlm-probe` refuses to run without `SP_POC_VLM_API_KEY`, because a probe that echoed
the configuration back would be indistinguishable from one that reached the host. It
spends nothing against the daily cap, so running it three times costs nothing either.
A 401 is recorded with its body, in a file, under its own name: the failing probe is
usually the one that settles the question.

## A seven-day run

```powershell
$env:SP_POC_MODE='live'
$env:SP_POC_SOURCE_KIND='loop'
$env:SP_POC_SOURCE_ARG='testdata\loop.mp4'
$env:SP_POC_CAMERA_INTEGRITY='false'     # required: which trigger this run carries
$env:SP_POC_SOAK_TARGET='168h'
$env:SP_POC_LABEL='week-1'
.\bin\pocctl.exe soak start      # preflight + gate D
.\bin\watch.exe                  # the long one; ctrl-C to stop
.\bin\pocctl.exe brief --data-root <root>
.\bin\pocctl.exe report --data-root <root>
```

`SP_POC_SOAK_TARGET` is a promise made before the run. A run shorter than its
target reports `PROVISIONAL`, never `PASS`.

## Where the numbers come from

`pocctl report` rebuilds the report from the run's own database, so a report can
be regenerated without re-running anything, and the screen and the report read
the same rows. Every `SP_POC_*` variable is required, and a missing one fails by
name with the consequence of not having it:

```
SP_POC_SOURCE_ARG: not set; without it ffmpeg has no input, so preflight never
sees a first frame and the run cannot start; set it to a file path or a URL
```

`watch --config` prints the configuration the process will use, with the three
secrets — the VMS client secret, the VMS password and the camera password —
replaced by `<redacted: present>`.

`SP_POC_CAMERA_INTEGRITY` is in that table and is **required**, not defaulted. It
decides whether the run carries the `camera_integrity` trigger beside the alarm one,
and that trigger pins a reference view on disk which every later reference-mode
measurement is made against. It was read by `os.Getenv` in `cmd/watch` until
2026-10-01 and is now a schema row like the rest: refused by name, printed by
`watch --config`, and covered by a test.

## The screen

`http://127.0.0.1:8097`, loopback only. No JavaScript and no framework: the
live picture is a `multipart/x-mixed-replace` MJPEG stream inside a plain `<img>`,
which a browser has rendered since before this project existed.

`/` is the live view, `/board` is the seven-day board. Every number on either came
from a row, and a section the server could not read says **no data** in words and
never shows a zero.

## For a reviewer who wants the uncomfortable version

- [`CONTRACT.md`](CONTRACT.md) — the module's own rules, and what it refuses to claim
- [`docs/findings.md`](docs/findings.md) — the measurements, including the bugs
- [`docs/hackathon/demo-script.md`](docs/hackathon/demo-script.md) — the demo, and the
  questions a non-technical person will ask, with answers that do not overstate