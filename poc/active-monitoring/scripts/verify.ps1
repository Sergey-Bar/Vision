<#
.SYNOPSIS
  The gate for poc/active-monitoring. This is the only gate this module has: the
  repository root's scripts/verify.ps1 does not build it, because the root's
  go build ./... skips a nested module entirely.

.DESCRIPTION
  It runs, in order, and stops at the first failure:

    1. toolchain    go, gofmt, and gcc (for -race). Each is located explicitly,
                    because none of them is on PATH on a default Windows shell.
                    A missing gcc prints SKIPPED with the reason and is not a pass.
    2. build        go build ./...
    3. format       gofmt -l, which must print nothing
    4. vet          go vet ./...
    5. boundary     the module does not import the platform, and the platform's
                    root does not build this module
    6. test         go test -race -p 1 -count=1 ./...
    7. python       py -3 -m unittest on the sidecar
8. red-check    the same suite with each control removed, each of which
                     must turn at least one test red

  Step 8 is the one that makes the other seven mean something. A suite that
  passes because the thing it was supposed to catch is not there any more is a
  green run that measured nothing, and this repository treats that as a defect
  rather than as a result.

.PARAMETER NoRace
  Skip the race detector. Prints SKIPPED for that step, visibly, and is not a
  pass for it.

.PARAMETER SkipRedCheck
  Skip the red-check. Prints SKIPPED. Only for a fast inner loop; a release check
  must run it.
#>
[CmdletBinding()]
param(
  [switch]$NoRace,
  [switch]$SkipRedCheck
)

$ErrorActionPreference = 'Stop'
$script:failed = @()
$script:skipped = @()

# The gate owns its own working directory, because nothing else does.
#
# This script sits two directories below the module it gates, and every path in
# it is relative to THAT module: `go build ./...`, the control table's
# `internal\detect\engine.go`, the `models\` lookups, `testdata\`. Until this
# line existed the gate only worked when the caller had already cd'd into
# `poc/active-monitoring`, which is how it was always invoked by hand — and the
# repository's own runbook invokes it from the repo root
# (`powershell -File poc\active-monitoring\scripts\verify.ps1`), where it did
# this:
#
#   * `go build ./...` and `go vet ./...` ran over the ROOT module and printed
#     PASS for it, which is a pass about a tree this gate has no opinion on;
#   * the platform-import scan walked from the repo root, so every file in the
#     root module that imports `github.com/Sergey-Bar/vision/` — which is
#     every one of them — was reported as "this module imports the platform";
#   * `Join-Path $PWD '..\..'` then resolved one level ABOVE the repository, and
#     `go list ./...` there is a terminating NativeCommandError that killed the
#     gate at step 5 of 9.
#
# So the documented invocation ran three steps, two of them about the wrong
# module, and aborted. The root gate has had this line since it was written
# (`$repo = Split-Path -Parent $PSScriptRoot; Set-Location $repo`); this one is
# the same idea one directory deeper.
$moduleRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $moduleRoot

function Step($name) { Write-Host ""; Write-Host "== $name" -ForegroundColor Cyan }

function Pass($msg) { Write-Host "   PASS  $msg" -ForegroundColor Green }
function Skip($msg) { Write-Host "   SKIPPED  $msg" -ForegroundColor Yellow; $script:skipped += $msg }
function Fail($msg) { Write-Host "   FAIL  $msg" -ForegroundColor Red; $script:failed += $msg }

# --- 1. toolchain -----------------------------------------------------------
Step "toolchain"

function Find-Tool([string]$name, [string[]]$candidates) {
  $onPath = Get-Command $name -ErrorAction SilentlyContinue
  if ($onPath) { return $onPath.Source }
  foreach ($c in $candidates) {
    if ($c -and (Test-Path -LiteralPath $c)) { return $c }
  }
  return $null
}

$go = Find-Tool 'go' @("$env:ProgramFiles\Go\bin\go.exe", "$env:LOCALAPPDATA\Programs\Go\bin\go.exe")
if (-not $go) {
  Fail "go is not installed or not found. This module cannot be built or tested without it. Install Go 1.26 or newer and re-run."
  Write-Host ""
  Write-Host "The gate stops here: everything below needs the toolchain." -ForegroundColor Red
  exit 1
}
$env:Path = (Split-Path $go) + ";" + $env:Path
Pass "go at $go"
& $go version

$goFmt = Find-Tool 'gofmt' @((Join-Path (Split-Path $go) 'gofmt.exe'))
if ($goFmt) { $env:Path = (Split-Path $goFmt) + ";" + $env:Path; Pass "gofmt at $goFmt" }
else { Fail "gofmt is not next to go; the format step cannot run and is not a pass" }

$gcc = Find-Tool 'gcc' @('C:\msys64\mingw64\bin\gcc.exe', 'C:\msys64\ucrt64\bin\gcc.exe')
if ($gcc) {
  $env:Path = (Split-Path $gcc) + ";" + $env:Path
  # -race needs cgo, and cgo is off by default when no C compiler was on PATH
  # at build time. Setting it here is what makes the race step a real run rather
  # than a skip that reads like a pass.
  $env:CGO_ENABLED = '1'
  Pass "gcc at $gcc and CGO_ENABLED=1 (the race detector needs both)"
} else { Skip "gcc was not found, so the race detector cannot run. Install MSYS2 (C:\msys64) or MinGW. The -race step below is SKIPPED, not passed." }

# mediamtx is an ARCHIVE package: `winget install` extracts the binary into its own
# Packages folder and adds a PATH entry that the shell that ran the install has not
# inherited. The candidate below is that folder, found by a glob rather than a
# version-pinned literal — a hardcoded version path would go stale on the next
# upgrade and make this gate print a SKIP about a tool that IS installed.
$mtCandidates = @()
$mtRoot = Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages'
if (Test-Path $mtRoot) {
  $mtCandidates = @(Get-ChildItem -Path $mtRoot -Filter 'mediamtx.exe' -Recurse -ErrorAction SilentlyContinue |
                    Select-Object -First 1 -ExpandProperty FullName)
}
$mediamtx = Find-Tool 'mediamtx' $mtCandidates
if ($mediamtx) {
  # Put it on PATH, or the `pocctl demo --rtsp` step below would refuse by name for
  # the same reason this one used to print a skip.
  $env:Path = (Split-Path $mediamtx) + ";" + $env:Path
  Pass "mediamtx at $mediamtx (`pocctl demo --rtsp` can publish an rtsp relay)"
}
else {
  # A SKIP and never a Fail, and the distinction is load-bearing. `pocctl demo`
  # without --rtsp loops a file and is a complete gate on its own; D-4 made the
  # relay opt-in for exactly this reason. A missing OPTIONAL tool that turned the
  # gate red would train everyone to ignore the gate, which is the exact failure
  # this module treats as a defect everywhere else.
  Skip "mediamtx is not installed, so `pocctl demo --rtsp` will refuse by name. Install it with `winget install --id Bluenviron.MediaMTX -e` (MIT) from https://github.com/bluenviron/mediamtx/releases. This is a SKIP, not a pass: the loop demo is still a full gate, and nothing below measured the rtsp path."
}

$ffmpeg = Get-Command ffmpeg -ErrorAction SilentlyContinue
if ($ffmpeg) { Pass "ffmpeg at $($ffmpeg.Source)" }
else { Fail "ffmpeg is not on PATH. D2 makes it a hard runtime dependency: without it there is no source, no recording and no frames. The tests that need it FAIL rather than skip, on purpose." }

$ffprobe = Get-Command ffprobe -ErrorAction SilentlyContinue
if ($ffprobe) { Pass "ffprobe at $($ffprobe.Source)" }
else { Fail "ffprobe is not on PATH; the capability table cannot measure the source and preflight has no bit rate to compute a disk budget from." }

$py = Get-Command py -ErrorAction SilentlyContinue
if ($py) { Pass "py at $($py.Source)" }
else {
  # `else if`, not two `if`s. This used to be two `if`s, so `Pass "python at"`
  # printed even when NEITHER `py` nor `python` existed and then `Fail` fired on
  # the next line: the gate claimed to have found an interpreter it had not
  # found. A PASS line followed by a FAIL for the same fact is the exact shape
  # of a gate nobody reads, and this gate is read.
  $py = Get-Command python -ErrorAction SilentlyContinue
  if ($py) { Pass "python at $($py.Source)" }
  else { Fail "no Python interpreter was found. The AI sidecar needs numpy and onnxruntime and is a hard dependency (D2)." }
}

# --- 2. build ---------------------------------------------------------------
Step "build"
& $go build ./...
if ($LASTEXITCODE -ne 0) { Fail "go build ./... exited $LASTEXITCODE" } else { Pass "go build ./... " }

# --- 3. format --------------------------------------------------------------
Step "gofmt"
$unformatted = & $goFmt -l . 2>&1
if ($unformatted) {
  $unformatted | ForEach-Object { Write-Host "        $_" -ForegroundColor Yellow }
  Fail "gofmt -l listed $($unformatted.Count) file(s). Formatting is not a style preference here: the file list is part of the diff a reviewer reads."
} else { Pass "gofmt -l is empty" }

# --- 4. vet -----------------------------------------------------------------
Step "vet"
& $go vet ./...
if ($LASTEXITCODE -ne 0) { Fail "go vet ./... exited $LASTEXITCODE" } else { Pass "go vet ./... " }

# --- 5. module boundary -----------------------------------------------------
Step "module boundary"

# D1: no import of the parent module, anywhere.
$platformImport = Get-ChildItem -Recurse -Filter *.go -Path . | Select-String -Pattern 'github\.com/Sergey-Bar/vision/'
if ($platformImport) {
  $platformImport | ForEach-Object { Write-Host "        $($_.Path):$($_.LineNumber)" -ForegroundColor Yellow }
  Fail "this module imports the platform. D1 forbids it: the POC mirrors the platform's ideas and shares none of its code."
} else { Pass "no import of the platform" }

# D1: the root does not build this module. This is the reason this script exists.
$root = Resolve-Path (Join-Path $PWD '..\..')
Push-Location $root
try {
  $rootPkgs = & $go list ./... 2>$null
  $rootPkgs | Where-Object { $_ -match 'active-monitoring' } | ForEach-Object {
    Write-Host "        the root build includes $_" -ForegroundColor Yellow
    Fail "the repository root's go list ./... includes this module. It must NOT: a nested module is skipped by the root, and a root that did build it would mean the boundary is not real."
  }
  if ($LASTEXITCODE -ne 0) { Skip "the root's go list ./... could not be run from here; the nested-module check was not performed" }
  else { Pass "the repository root does not build this module (as it must not)" }
} finally { Pop-Location }

# The structural boundaries inside the module. The check is on the IMPORT GRAPH,
# not on the text: a comment that happens to mention SQLite is not a dependency,
# and a text search would report one and hide a real one.
$boundaries = @(
  @{
    pkg = './internal/detect'
    forbid = @('modernc.org/sqlite', 'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/evidence',
               'github.com/Sergey-Bar/vision-poc/internal/xprotect',
               'github.com/Sergey-Bar/vision-poc/internal/ai')
    why = 'internal/detect must know nothing about storage, the wire or the sidecar'
  },
  @{
    pkg = './internal/ai'
    forbid = @('modernc.org/sqlite', 'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/evidence',
               'github.com/Sergey-Bar/vision-poc/internal/xprotect')
    why = 'internal/ai must know nothing about line-crossing, storage or the wire'
  },
  @{
    pkg = './internal/xprotect'
    forbid = @('github.com/Sergey-Bar/vision-poc/internal/xpsim',
               'modernc.org/sqlite', 'github.com/Sergey-Bar/vision-poc/internal/detect')
    why = 'internal/xprotect must not know the simulator exists, and must know nothing about storage'
  },
  @{
    pkg = './internal/xpsim'
    forbid = @('github.com/Sergey-Bar/vision-poc/internal/evidence',
               'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/xprotect')
    why = 'the simulator must know nothing about the pipeline: it is a server, not a participant'
  },
  @{
    # The leaf that both the screen and the runner draw with. internal/ui imports
    # internal/soak and internal/store, and T6.3 asks internal/soak to render a
    # crossing snapshot — so the drawing code cannot live in either one without an
    # import cycle. It lives here, below both, and this row is what stops it from
    # quietly growing a dependency on the packages that depend on it. That is the
    # cycle, and Go would refuse to build it: the point is to refuse it here with a
    # sentence about why, rather than as a compiler error a week later.
    pkg = './internal/overlay'
    forbid = @('github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/ai',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/ui',
               'modernc.org/sqlite')
    why = 'internal/overlay is the leaf the screen and the runner BOTH draw with, so it may import only the standard library and internal/classify. Anything else is the import cycle this package exists to avoid, and a package that has begun to be a dumping ground is a package whose boundary has stopped being enforced.'
  },
  @{
    # The shared box type, and the strictest leaf in the module: `ai.Box`,
    # `detect.Box` and `novelty.Box` are ALIASES of `proposal.Box`, so all three
    # of those packages import it and it may import none of them back.
    #
    # That is the whole reason this package exists. Three packages each declared
    # their own Box, and a fourth — the detector — could not be written without
    # importing one of them. A type that three packages alias can only stay one
    # type while it stays below all three; the moment it imports one of them, the
    # aliases stop being assignable and every existing call site becomes a
    # conversion, which is a place a coordinate gets rescaled by accident.
    #
    # It must also stay free of `math` at the edges: the predicates here decide
    # whether a box is real, and a predicate that needed the store to answer
    # would be a predicate about the database rather than about the picture.
    pkg = './internal/proposal'
    forbid = @('modernc.org/sqlite',
               'github.com/Sergey-Bar/vision-poc/internal/ai',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/novelty',
               'github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/overlay',
               'github.com/Sergey-Bar/vision-poc/internal/classify',
               'github.com/Sergey-Bar/vision-poc/internal/evidence',
               'github.com/Sergey-Bar/vision-poc/internal/config',
               'onnxruntime_go')
    why = 'internal/proposal is aliased BY internal/ai, internal/detect and internal/novelty, so it must import none of them and nothing at all. A shared vocabulary that depends on one of its consumers is not a shared vocabulary.'
  },
  @{
    # The detector's decoder, and a leaf for the same reason internal/vlm is one.
    #
    # It must not reach the database, and the reason is the parity claim: the
    # cross-decoder test asserts that this decoder and pyworker's decoder agree on
    # the same tensor. A decoder that could read a recorded answer would make that
    # comparison unfalsifiable, exactly as a verifier that can read the store would
    # make RC-4 unfalsifiable.
    #
    # `internal/novelty` is permitted and `internal/ai` is required: the absence
    # states are the four the contract fixes and they must not drift between the
    # sidecar path and the detector path, so both name them from one place.
    pkg = './internal/ortdetect'
    forbid = @('modernc.org/sqlite',
               'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/evidence',
               'github.com/Sergey-Bar/vision-poc/internal/xprotect',
               'github.com/Sergey-Bar/vision-poc/internal/ui',
               'github.com/Sergey-Bar/vision-poc/internal/classify')
    why = 'internal/ortdetect decodes a tensor and nothing else. It may import internal/proposal for the box type, internal/novelty for the candidate shape the Trigger interface hands back, and internal/ai for the absence states; anything that reaches storage or the pipeline would let a measurement be taken from a record instead of from a tensor.'
  },
  @{
    # The benchmark's arithmetic, and it is a leaf for the reason it matters.
    #
    # `metrics.go` is the only file here that the gate allows to import
    # `internal/ortdetect`, and it is allowed to import it for the BOX TYPE ALONE —
    # via `internal/proposal`, which it imports directly. So the effective rule is
    # what this checks: the arithmetic must not be able to reach a session, a store,
    # or the pipeline.
    #
    # The reason is falsifiability. The golden test works because `metrics.go`
    # imports nothing that could agree with it: a metric that read the database
    # could not be checked against hand-computed arithmetic, and a report whose
    # numbers come from the same code it is measuring is unfalsifiable.
    pkg = './internal/bench'
    forbid = @('modernc.org/sqlite',
               'github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/ortdetect',
               'github.com/Sergey-Bar/vision-poc/internal/evidence',
               'github.com/yalue/onnxruntime_go')
    why = 'internal/bench/metrics.go is the pure half of the benchmark and must stay pure, or its golden test is checking the implementation against itself. The half that holds a session is cmd/pocctl/bench.go, which is a command and not a package.'
  },
  @{
    # The marking tool's storage and page. It reads the store and draws boxes; it
    # must not reach a detector or a pipeline.
    #
    # A marking tool that could re-run the detector would be measuring the model
    # rather than the judgement, and the false-positive rate it produced would be
    # the model's opinion of itself.
    pkg = './internal/labelui'
    forbid = @('github.com/Sergey-Bar/vision-poc/internal/ortdetect',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/yalue/onnxruntime_go')
    why = 'internal/labelui marks what a run RECORDED. It may hold the store and serve a page; it must not hold a model, because a marking tool that re-runs the detector is asking the model to grade its own work.'
  },
  @{
    # The taxonomy is a leaf too, and for the opposite reason: it is the vocabulary
    # every other package agrees on, so a classify that reached for the store or
    # the wire would make the vocabulary depend on the thing it describes.
    pkg = './internal/classify'
    forbid = @('modernc.org/sqlite')
    why = 'internal/classify is the family vocabulary and must stay a leaf; a taxonomy that imports the store is a taxonomy that can be wrong in a way no table can show'
  },
  @{
    # The verifier is a leaf. It is the package that decides what a camera operator
    # is told, so the one thing it must not be able to do is read the database and
    # report what somebody else said last time.
    #
    # It also forbids internal/soak and internal/store, and the reason is the
    # replay: soak.Replay proves a decision by recomputing it from stored facts, and
    # that proof is structural ONLY because internal/vlm cannot reach the store. The
    # moment a client takes a *store.Store it can be handed a recorder's answer
    # instead of asking, and RC-4 becomes unfalsifiable.
    pkg = './internal/vlm'
    forbid = @('github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/novelty',
               'modernc.org/sqlite')
    why = 'internal/vlm decides what an operator is told. It must reach no database, no pipeline and no detector: a verifier that can read what somebody said last time is a recorder, and a recorder is exactly what RC-4 forbids.'
  },
  @{
    # The novelty engine is a leaf for the same reason as the overlay: it owns the
    # region and it owns nothing else. A detector that could reach the store could
    # report a decision it did not measure, and a detector that could reach soak
    # would be inside the pipeline it is supposed to be a measurement of.
    pkg = './internal/novelty'
    forbid = @('github.com/Sergey-Bar/vision-poc/internal/store',
               'github.com/Sergey-Bar/vision-poc/internal/soak',
               'github.com/Sergey-Bar/vision-poc/internal/vlm',
               'github.com/Sergey-Bar/vision-poc/internal/detect',
               'github.com/Sergey-Bar/vision-poc/internal/ui',
               'modernc.org/sqlite')
    why = 'internal/novelty measures a region from pixels and nothing else. A score that could read the database could report a decision nobody measured, and the box on a pushed event is supposed to be ours and measured.'
  }
)
foreach ($b in $boundaries) {
  $imports = & $go list -f '{{range .Imports}}{{.}}
{{end}}{{range .TestImports}}{{.}}
{{end}}' $b.pkg 2>$null
  $bad = @()
  foreach ($i in $imports) {
    foreach ($f in $b.forbid) {
      if ($i -eq $f) { $bad += $i }
    }
  }
  if ($bad) {
    $bad | ForEach-Object { Write-Host "        $($b.pkg) imports $_" -ForegroundColor Yellow }
    Fail $b.why
  } else { Pass "$($b.pkg) is isolated (import graph)" }
}

# --- 6. go tests ------------------------------------------------------------
Step "tests"
if ($NoRace) {
  Skip "the race detector was disabled with -NoRace. That is a skip, not a pass."
  & $go test -p 1 -count=1 ./...
} elseif ($env:CGO_ENABLED -ne '1') {
  Skip "CGO_ENABLED is not 1, so -race cannot run (it needs a C compiler). Install MSYS2 or MinGW. The race step is SKIPPED, not passed."
  & $go test -p 1 -count=1 ./...
} else {
  & $go test -race -p 1 -count=1 ./...
}
if ($LASTEXITCODE -ne 0) { Fail "go test exited $LASTEXITCODE" } else { Pass "go test" }

# --- 7. python --------------------------------------------------------------
Step "sidecar tests"
if ($py) {
  # $ErrorActionPreference is 'Stop' for this whole script, and PowerShell 5.1
  # turns ANY native command's stderr into a NativeCommandError record. So a
  # suite that PASSED aborted the gate with a red stack: `unittest` writes its
  # progress dots to stderr BY DESIGN, which means this step could never get
  # past a green run on this shell.
  #
  # The preference is relaxed for exactly this invocation and restored on the
  # line after, so nothing else in the gate is softened — and the verdict still
  # comes from $LASTEXITCODE, not from whether stderr said anything.
  $ErrorActionPreference = 'Continue'
  if ($py.Name -eq 'py.exe') { & $py -3 -m unittest discover -s pyworker -p 'test_*.py' }
  else { & $py -m unittest discover -s pyworker -p 'test_*.py' }
  $pyExit = $LASTEXITCODE
  $ErrorActionPreference = 'Stop'
  if ($pyExit -ne 0) { Fail "the sidecar's Python tests exited $pyExit" } else { Pass "pyworker tests" }
} else { Skip "no Python interpreter, so the sidecar's tests did not run" }

# --- 7b. cross-decoder parity ---------------------------------------------
# The detector exists to produce a precision number, and a precision number from a
# decoder nobody has checked is a number about the decoder. The Python sidecar is
# the reference: different language, different Numpy, and the implementation that
# produced every number this project has reported so far.
#
# This step runs the PARITY HARNESS, which is the part of the check that needs no
# model and no network: both axis layouts decoding the same records to the same
# boxes, and the fixture proving that it exercises both NMS passes and the
# clamp-then-gate ordering.
#
# The full cross-decoder comparison needs a running sidecar and a downloaded
# model, neither of which a gate may assume. It is SKIPPED with the command that
# runs it — never a quiet pass.
Step "cross-decoder parity"
& $go test -count=1 -run 'Parity|BothAxisLayouts|ExerciseBothNMS|ScoreParity' ./internal/ortdetect/
if ($LASTEXITCODE -ne 0) { Fail "the decoder's own parity harness failed: the two axis layouts disagree, or the fixture no longer exercises both suppression passes" }
else { Pass "the decoder's axis-layout and NMS-fixture parity harness" }

$pyRunner = Get-Command py -ErrorAction SilentlyContinue
if ($pyRunner) { $pyCmd = if ($pyRunner.Name -eq 'py.exe') { @('py','-3') } else { @('python') } }
else { $pyCmd = $null }

# The library name is resolved from `go env` rather than from `$IsWindows`.
# `$IsWindows` and `$IsMacOS` are PowerShell 7 variables and this script runs
# under Windows PowerShell 5.1, where they are UNSET — so a test on them is a
# test that silently takes the wrong branch on the platform this gate is written
# for. `go env GOOS` is the same question asked of the toolchain that will
# actually load the file.
$ortGOOS = (& $go env GOOS).Trim()
$ortLib = 'models/libonnxruntime.so'
if ($ortGOOS -eq 'windows') { $ortLib = 'models\onnxruntime.dll' }
elseif ($ortGOOS -eq 'darwin') { $ortLib = 'models/libonnxruntime.dylib' }
$ortModel = 'models\yolov4n.onnx'
if (-not (Test-Path -LiteralPath $ortModel)) { $ortModel = 'models/yolov4n.onnx' }

if ($pyCmd -and (Test-Path -LiteralPath $ortLib) -and (Test-Path -LiteralPath $ortModel)) {
  # The named test must EXIST before it is believed. This step ran
  # `go test -run 'TestCrossDecoderParity' -tags parity ./internal/ortdetect/`
  # for weeks against a file that did not exist: `go test -run` with a pattern
  # that matches nothing prints "no tests to run" and exits 0, so the step
  # printed "the Go decoder and pyworker agree on the same tensor" over a
  # comparison nobody had ever run — a green check about evidence that does not
  # exist, which is worse than no check.
  #
  # `-list` prints the matching test names and exits 0 either way, so the exit
  # code is not the evidence. The OUTPUT is: the command must name the test it is
  # about to run, or the step refuses rather than passes.
  $listed = & $go test -count=1 -list 'TestCrossDecoderParity' -tags parity ./internal/ortdetect/ 2>&1
  if (($listed | Where-Object { $_ -match '^(Test|Example)' }).Count -eq 0) {
    Fail "the cross-decoder parity test named above does not exist in ./internal/ortdetect with the 'parity' tag, and a `go test -run` over a name nothing matches is an exit 0. The step below would report agreement without comparing anything."
  } else {
    & $go test -count=1 -run 'TestCrossDecoderParity' -tags parity ./internal/ortdetect/
    if ($LASTEXITCODE -ne 0) {
      Fail "the Go decoder and pyworker's disagree on the same tensor. Every measurement taken with either is a measurement of their disagreement."
    } else { Pass "the Go decoder and pyworker agree on the same tensor" }
  }
} else {
  $missing = @()
  if (-not $pyCmd) { $missing += 'no Python interpreter' }
  if (-not (Test-Path -LiteralPath $ortLib)) { $missing += "no ONNX Runtime library at $ortLib" }
  if (-not (Test-Path -LiteralPath $ortModel)) { $missing += "no model at $ortModel" }
  Skip "the full cross-decoder comparison did not run: $($missing -join '; '). `pocctl fetch-runtime` and `pocctl fetch-model` obtain the two files. NOT a pass."
}

# --- 7c. benchmark arithmetic ----------------------------------------------
# The metrics are pure, so this needs no model and no network — and that is the
# point of them being pure. The golden test works because metrics.go imports
# nothing that could agree with it.
Step "benchmark arithmetic"
& $go test -count=1 -run 'TestTheMetricsAreTheHandComputed|TestARateWithNoDenominator|TestADroppedClass|TestOneToOneMatching|TestTheMatchThreshold' ./internal/bench/
if ($LASTEXITCODE -ne 0) { Fail "the benchmark's hand-computed arithmetic does not hold; every rate the report prints comes from these two functions" }
else { Pass "the benchmark's hand-computed arithmetic" }

# The STILL set's manifest, which is a DIFFERENT file from testdata/groundtruth.json.
# That one holds a person's marks about pushed events and it is not what
# `bench --images` reads; checking for it here would pass on a file that has nothing
# to do with a benchmark's inputs.
$gtManifest = 'testdata\still-groundtruth.json'
if (-not (Test-Path -LiteralPath $gtManifest)) { $gtManifest = 'testdata/still-groundtruth.json' }
if (Test-Path -LiteralPath $gtManifest) {
  Pass "a still-set ground-truth manifest exists, so a benchmark's material is attributable"
} else {
  Skip "no testdata/still-groundtruth.json, so `pocctl bench --images` refuses: a benchmark over material whose licence nobody wrote down produces numbers nobody may show. Run `pocctl fetch-groundtruth`. NOT a pass."
}

# The two must not have been collapsed into one path. `fetch-groundtruth` refuses to
# write over the event marks, and this checks the refusal's premise rather than the
# refusal itself — a guard against a file that does not exist protects nothing.
if (Test-Path -LiteralPath 'testdata\groundtruth.json') {
  Pass "the event-mark file is still separate from the still-set manifest"
} else {
  Pass "no event-mark file, so there is nothing for the still-set manifest to overwrite"
}

# --- 8. red check -----------------------------------------------------------
Step "red check (each control removed from the CODE, each must turn the suite red; the count is printed by the step below, not here)"

if ($SkipRedCheck) {
  Skip "the red check was skipped with -SkipRedCheck. A release check must run it."
} else {
  # The control is a line in the PRODUCTION code, not a test. Removing a test
  # proves nothing: a suite with one fewer test still passes and the thing that
  # test was watching is still un-watched. Removing the check itself proves the
  # test is watching it.
  $controls = @(
    @{
      name = 'evidence store path is derived from a hash'
      file = 'internal\evidence\store.go'
      from = 'if !isSHA256(sum) {'
      to   = 'if false { // REDCHECK: the hash-shape check was removed'
      pkg  = './internal/evidence'
      why  = 'remove it and a caller-chosen name is accepted, so two events could point at one stored object and a path could escape the store'
    },
    @{
      name = 'detection terminal-state guard'
      file = 'internal\store\detections.go'
      from = "WHERE id=? AND status NOT IN ('done')"
      to   = "WHERE id=? -- REDCHECK: the terminal-state guard was removed"
      pkg  = './internal/store'
      why  = 'remove the guard and a finished detection can be moved again, so one crossing ends up with two stories'
    },
    @{
      name = 'line-crossing tolerance band'
      file = 'internal\detect\engine.go'
      from = 'if dist < e.cfg.Tolerance {'
      to   = 'if dist < e.cfg.Tolerance*1000 { // REDCHECK: the band was made total'
      pkg  = './internal/detect'
      why  = 'widen it until every frame is "on the line" and no crossing is ever decided, so a run reports zero events and no error at all'
    },
    # --- RC-1..RC-7: the seven checks the plan calls "the whole difference
    # between a demo and a system". Each one is a control whose REMOVAL produces a
    # specific lie, and each has a test whose subject is that control.
    @{
      name = 'RC-1 a missing answer is never a negative one (parse time)'
      file = 'internal\vlm\parse.go'
      from = 'if o.State != aistate.Asked {'
      to   = 'if o.State != aistate.Asked && false { // REDCHECK RC-1: the fabricated-negative control was removed'
      pkg  = './internal/vlm'
      why  = 'a degraded or not_asked observation keeps its verdict, so a camera operator is told nothing happened because we could not ask'
    },
    @{
      name = 'RC-1 the same rule, at write time'
      file = 'internal\store\observations.go'
      from = 'if o.AIState != aistate.Asked && vlm.Verdict(o.Verdict) != vlm.VerdictNone {'
      to   = 'if false { // REDCHECK RC-1: the durable half was removed'
      pkg  = './internal/store'
      why  = 'a second writer that forgot the parser would put a fabricated negative into the one table a proof chain reads'
    },
    @{
      name = 'RC-2 an edited ledger row fails verification'
      file = 'internal\store\ledger.go'
      from = 'func hashMatches(a, b string) bool { return len(a) == 64 && a == b }'
      to   = 'func hashMatches(a, b string) bool { return len(a) == 64 } // REDCHECK RC-2: the comparison was reduced to a length test'
      pkg  = './internal/store'
      why  = 'the chain verifies for an edited row, so "the ledger is intact" becomes a comment and the proof chain proves nothing'
    },
    @{
      name = 'RC-3 the ledger is append-only'
      file = 'internal\store\ledger.go'
      from = 'var ledgerGuards = []string{ledgerGuardUpdate, ledgerGuardDelete}'
      to   = 'var ledgerGuards = []string{} // REDCHECK RC-3: the append-only triggers were removed'
      pkg  = './internal/store'
      why  = 'a person with a sqlite3 prompt can edit or delete a recorded decision row, and the guard that said they could not is gone'
    },
    @{
      name = 'RC-4 a replay makes zero provider calls'
      file = 'internal\soak\replay.go'
      from = 'if rep.ProviderCalls > 0 {'
      to   = 'if rep.ProviderCalls > 0 && false { // REDCHECK RC-4: the zero-call assertion was removed'
      pkg  = './internal/soak'
      why  = 'a replay that quietly re-inferred prints the same PASS, so "replayable from stored facts" stops being a claim about this code'
    },
    @{
      name = 'RC-5 a snapshot is the frame the decision was made on'
      file = 'internal\soak\snapshot.go'
      from = 'if !ok || len(raw) == 0 {'
      to   = 'if false { // REDCHECK RC-5: the frame-attribution refusal was removed'
      pkg  = './internal/soak'
      why  = 'the picture on file is a real frame of the same camera and a different instant, and every check that only asks "is there a picture?" still passes'
    },
    @{
      name = 'RC-6 the measured box reaches the pushed event'
      file = 'internal\xprotect\client.go'
      from = 'if in.Box.Empty() {'
      to   = 'if in.Box.Empty() && false { // REDCHECK RC-6: the all-zero-box refusal was removed'
      pkg  = './internal/xprotect'
      why  = 'the VMS draws nothing on the operator timeline while the database beside it says the region is real — the worst thing a demo can show'
    },
    @{
      name = 'RC-7 a call to the other region is refused by name'
      file = 'internal\vlm\vlm.go'
      from = 'if !c.pinned(host) {'
      to   = 'if !c.pinned(host) && false { // REDCHECK RC-7: the cross-region refusal was removed'
      pkg  = './internal/vlm'
      why  = "a site in the EU ships footage to the US host; there is deliberately no failover, and this is the line that refuses it"
    },
    @{
      name = 'RC-9 an unasked window is not searchable'
      file = 'cmd\pocctl\search.go'
      from = 'unsearchable := s.NotAsked + s.Degraded'
      to   = 'unsearchable := 0 // REDCHECK RC-9: the unsearchable-window refusal was removed'
      pkg  = './cmd/pocctl'
      why  = 'pocctl search prints "no matches" for a window whose verifier was never consulted, which is a negative answer nobody gave. R7.'
    },
    # --- RC-13..RC-20: the detector controls. ----------------------------------
    #
    # Each of these is a line whose REMOVAL produces a specific lie about a
    # detector's measurements, and each has a test whose subject is that line.
    #
    # They are grouped separately from RC-1..RC-9 because these are about a
    # component that did not exist when those were written: the previous build
    # reported 6,578 approved boxes and zero vehicles, and every one of these
    # controls exists because that number was possible.
    @{
      name = 'RC-13 the letterbox divides by the CONTENT, not the input'
      file = 'internal\ortdetect\letterbox.go'
      from = 'return (v - float64(pad)) / float64(content)'
      to   = 'return (v - float64(pad)) / float64(gInputSizeOf) // REDCHECK RC-13: the content divisor was replaced'
      pkg  = './internal/ortdetect'
      why  = 'every box is drawn 0.6x its true height on a 16:9 frame, every crossing happens early, and nothing anywhere reports an error — the boxes are in the right x and confidently the wrong size'
    },
    @{
      name = 'RC-14 the class-agnostic suppression pass runs'
      file = 'internal\ortdetect\decode.go'
      from = 'afterBoth := nmsGlobal(afterClass, cfg.NMSGlobal)'
      to   = 'afterBoth := afterClass // REDCHECK RC-14: the class-agnostic pass was removed'
      pkg  = './internal/ortdetect'
      why  = 'a car box and a truck box on ONE object survive as two boxes, two tracks and two crossings, because a class-aware pass structurally cannot compare across classes. The picture shows one car and the report counts two.'
    },
    @{
      name = 'RC-15 the score chain cannot be inverted'
      file = 'internal\config\config.go'
      from = 'if c.DetScore > c.GhostMinScore {'
      to   = 'if false { // REDCHECK RC-15: the chain validator was removed'
      pkg  = './internal/config'
      why  = 'with an inverted chain the picture shows objects the model scored below its own emit floor AND omits objects it did emit. Both halves individually pass every arithmetic in the module; only the ordering is checkable.'
    },
    @{
      name = 'RC-16 no degenerate box is ever emitted'
      file = 'internal\ortdetect\decode.go'
      from = 'if cx2-cx1 <= 1e-6 || cy2-cy1 <= 1e-6 {'
      to   = 'if false { // REDCHECK RC-16: the degenerate-box gate was removed'
      pkg  = './internal/ortdetect'
      why  = 'one box off the top of the frame clamps to a zero-area box, the consumer reads it as an uninterpretable answer, and a frame with nineteen good detections becomes a frame nobody can read'
    },
    @{
      name = 'RC-17 a benchmark over a finished run opens no model'
      file = 'cmd\pocctl\bench.go'
      from = 'return strings.TrimSpace(envOr("SP_POC_ORT_LIB", lib)) != "" ||'
      to   = 'if false { // REDCHECK RC-17: the refusal to re-decode a finished run was removed'
      pkg  = './cmd/pocctl'
      why  = 'a benchmark over a recorded run that opens a model is measuring the MODEL rather than the run, and the two disagree whenever the run used different settings — which is most of the time'
    },
    @{
      name = 'RC-18 every detector-mode decision names its model'
      file = 'internal\store\observations.go'
      from = 'if d.TriggerMode == TriggerModeDetector && !d.NamesModel() {'
      to   = 'if false { // REDCHECK RC-18: the provenance refusal on the write path was removed'
      pkg  = './internal/store'
      why  = 'a precision number that cannot name the file it came from describes a build nobody can reconstruct, and nothing downstream can detect it because every arithmetic still works perfectly well on the row'
    },
    @{
      name = 'RC-19 a ghost cannot create or age a track'
file = 'internal\detect\engine.go'
      from = 'if t.Seen >= e.trackMinFor(t) {'
      to   = 'if true { // REDCHECK RC-19: a track ages on its FIRST sighting'
      pkg  = './internal/detect'
      why  = 'a box the model approved once would be treated as an object that exists, so a refused box beside it could hold it alive and age it past the minimum. The gate is the AGED ASSIGNMENT and not the check: mutating the check to `if false` makes the recovery pass inert rather than permissive, which a test asserting "nothing was resumed" cannot see. The comparison is against e.trackMinFor(t) rather than e.cfg.TrackMinFrames because the minimum became SOURCE-AWARE; the control is the assignment, and this line was re-pointed when the expression changed rather than left to skip. Single-line controls only — PowerShell single quotes do not expand a newline, so a multi-line match string is a control that silently never fires.'
    },
    @{
      name = 'RC-20 the per-frame box log records whether a frame was MEASURED'
      file = 'internal\soak\boxlog.go'
      from = 'measured := tel.Candidates > 0 || tel.PreNms > 0 || len(boxes) > 0'
      to   = 'measured := true // REDCHECK RC-20: every frame is recorded as measured'
      pkg  = './internal/soak'
      why  = '"the model saw nothing" and "the pipeline never asked" become the same row, and a reader counting rows computes a recall of zero out of a run that never ran. This repository has shipped that exact confusion three times.'
    },
    @{
      # The two ledger readers have to agree about what (0, 0) means. VerifyLedger
      # has always read it as "to the end"; Ledger compared `seq <= to` literally, so
      # `pocctl audit ledger` printed "the ledger is empty. An empty chain is not an
      # intact one" and then "PASS — 57 row(s) recomputed and chained" on the next
      # line. Removing this line puts that contradiction back, and
      # TestAnOpenEndedLedgerRangeMeansTheWholeChain fails on the row count.
      name = 'RC-21 an open-ended ledger range means the whole chain'
      file = 'internal\store\ledger.go'
      from = 'to = s.lastSeq(ctx)'
      to   = 'to = to // REDCHECK RC-21: the open-ended range was removed'
      pkg  = './internal/store'
      why  = 'the audit command reports an empty ledger beside a verified chain, so a reader who sees "PASS" beside "the ledger is empty" has been taught to read past the tool that is supposed to be checking it'
    },
    @{
      # R8's first half: every pushed event names the state of its ask. Split from
      # the attribution half on purpose — an event with no state never reaches the
      # attribution checks, so folding them would make whichever came second
      # unreachable and its red check would pass vacuously. Two defects, two controls.
      name = 'RC-22 a pushed event names the state of its ask'
      file = 'internal\xprotect\client.go'
      from = '	if strings.TrimSpace(p.AIState) == "" {'
      to   = '	if false && strings.TrimSpace(p.AIState) == "" { // REDCHECK RC-22: the missing-state refusal was removed'
      pkg  = './internal/xprotect'
      why  = 'an event reads the same whether the model was asked and answered or whether nobody could reach it, so a reader cannot tell a finding from a gap — which is the entire product'
    },
    @{
      # R8's second half: an ANSWERED event carries the model, the contract version
      # and the output hash. Not no_objects and not the three blindness states:
      # nobody was asked, so there is nothing to attribute, and requiring it would
      # force the writer to invent all.
      name = 'RC-23 an answered event carries model, contract version and output hash'
      file = 'internal\xprotect\client.go'
      from = '	if strings.TrimSpace(p.ModelIdentity) == "" {'
      to   = '	if false && strings.TrimSpace(p.ModelIdentity) == "" { // REDCHECK RC-23: the attribution refusal was removed'
      pkg  = './internal/xprotect'
      why  = 'a caption arrives in Smart Client with nobody named as its author, and the person reading it has no way of knowing that this repository cannot check what it says'
    },
    @{
      # The aggregate's whole identity. Called once per WINDOW with the window's
      # first proposal's instant, the key is stable and every later proposal refreshes
      # the row under it. Recomputed per proposal it changes every time: the row still
      # collapses, but the VMS ends up holding the LAST proposal's tag, a retry of
      # the first proposal computes a key that matches nothing, and the retry becomes
      # a second remote event. The window was re-keyed this way once and the test that
      # caught it asserts both halves — the outbox row's tag column and the tag
      # inside its payload.
      name = 'RC-24 a blindness window is keyed once, by its first proposal'
      file = 'internal\soak\absence.go'
      from = '	w.tag = absenceWindowTag(r.opt.RunID, state, ev.At)'
      to   = '	w.tag = absenceWindowTag(r.opt.RunID, state, ev.At.Add(time.Duration(w.count)*time.Second)) // REDCHECK RC-24: the window was re-keyed per proposal'
      pkg  = './internal/soak'
      why  = 'the aggregated alarm loses its idempotency, so a retry of the window first proposal becomes a second remote event and AC4 duplicate-id count goes above zero'
    },
    @{
      # The outbox row and the payload must carry the SAME key. Rewriting only the
      # payload moves the count into the tag the VMS will store while the column the
      # push worker reads still holds the count-one key, so the read-back searches for
      # something that was never sent and reports `unknown` — "the push returned 202
      # but the event was not found" for an event that is sitting in the VMS.
      name = 'RC-25 an aggregated alarm key is rewritten with its payload'
      file = 'internal\store\outbox.go'
      from = '		UPDATE outbox SET payload=?, tag=?, detail=?, updated_at=?'
      to   = '		UPDATE outbox SET payload=?, detail=?, updated_at=? -- REDCHECK RC-25: the key was left behind'
      pkg  = './internal/store'
      why  = 'the module reports unknown for a push that succeeded, which AC2 reads as work still in flight and AC4 as an attempt with no result'
    },
    @{
      # `no_objects` is a negative ANSWER, not a blindness. Accepted as a key, it
      # becomes an Analytics Event Type that fires whenever a camera sees nothing —
      # the alarm fatigue this product exists to remove, reintroduced through the
      # blindness map. The refusal is in the PARSER and not at the push site on
      # purpose: a run that starts and then pushes nothing reports zero alarms and no
      # error, and "why did nothing arrive" is a much longer investigation than a
      # startup refusal naming the offending entry.
      name = 'RC-26 no_objects is refused its own Analytics Event Type'
      file = 'internal\config\eventtypes.go'
      from = '		if !IsBlindnessState(state) {'
      to   = '		if false { // REDCHECK RC-26: no_objects was accepted as an event-type state'
      pkg  = './internal/config'
      why  = 'an alarm that fires to say "nothing" arrives hundreds of times an hour, which is the fatigue the absence feature was built to remove and the reason no_objects drives the verdict on the detection own event instead'
    },
    @{
      # An unmapped (class, absence state) pair falls back to the DETECTION's event
      # type rather than being refused. Every absence then arrives as a line-crossing
      # alert, an Alarm Definition on "vehicle crossing the line" fires on it, and an
      # operator is paged about a car that may not exist because the module could not
      # reach its own model. The refusal names the pair and the remedy, which is the
      # house style and the only reason an operator can act on it at two in the
      # morning.
      name = 'RC-27 an unmapped absence pair is refused, not pushed as a crossing'
      file = 'internal\config\eventtypes.go'
      from = '	return c.refuseUnmapped(class, state)'
      to   = '	return c.EventTypeGUID, nil // REDCHECK RC-27: an unmapped pair was pushed as a crossing'
      pkg  = './internal/config'
      why  = 'the absence reaches the Smart Client wearing a crossing alarm type, and nothing in the output says the verdict was never produced'
    },
    @{
      # ONNX Runtime falls back to the CPU when the CUDA provider cannot be used and
      # reports it as a log line at session creation rather than an error. A CUDA
      # spelling that silently meant `cpu` would produce a correct, slower run whose
      # every recorded field says `cuda` — the exact shape of the defect this
      # repository exists to end: real numbers with the wrong label. The device
      # confirmation runs BEFORE the append, because the fallback happens after it,
      # where nothing in this package can see it.
      name = 'RC-28 a CUDA spelling is never silently the CPU'
      file = 'internal\ortdetect\execution_provider.go'
      from = 'func gpuMeansCUDA() string { return EPProviderCUDA }'
      to   = 'func gpuMeansCUDA() string { return EPProviderCPU } // REDCHECK RC-28: `gpu` became the CPU'
      pkg  = './internal/ortdetect'
      why  = 'a latency measured on the CPU is recorded as one measured on a GPU, and the number is real and the label is not'
    },
    @{
      # A model exported with dynamic axes publishes declared output dims of -1, and
      # a -1 cannot be sized. This module used to refuse such a model outright with
      # "an output tensor could not be allocated", which means the committed detector
      # could not be run at all, on either provider. That is the NORMAL shape for a
      # modern YOLO export, so the old behaviour was not a safety check — it was an
      # inability wearing a refusal's clothes, and it failed while claiming to protect
      # something. Removing this line makes the runtime allocate instead.
      name = 'RC-29 a dynamic-axis export is run with runtime-allocated outputs'
      file = 'internal\ortdetect\session_cgo.go'
      from = '		return make([]ort.Value, len(outputs)), true'
      to   = '		return preallocateConcrete(outputs), false // REDCHECK RC-29: the runtime never allocates'
      pkg  = './internal/ortdetect'
      why  = 'a detector exported with dynamic axes cannot be run at all, and the refusal says it is being protected from something'
    },
    @{
      # The session worked out the layout from the model's own declared dims and
      # recorded it — in Input().Layout, in the run report and in fixtureprobe's
      # output — and then fed the tensor in a different order from the one declared.
      # A recorded fact nobody acts on is the failure this module exists to end, and
      # here it was hiding inside the module that exists to catch it. The symptom on
      # an NHWC export is "index: 1 Got: 3 Expected: 416".
      name = 'RC-30 the input tensor is laid out the way the model declares'
      file = 'internal\ortdetect\session_cgo.go'
      from = '		shape = ort.NewShape(1, h, w, c)'
      to   = '		shape = ort.NewShape(1, c, h, w) // REDCHECK RC-30: NHWC is fed as NCHW'
      pkg  = './internal/ortdetect'
      why  = 'a tensor of exactly the right size fed in the wrong order decodes to boxes that are plausible and wrong, while every artefact describing the model says the layout was honoured'
    },
    # --- presence mode and the crop pass ---------------------------------------
    #
    # Sixteen controls added together, and the reason they are a block is that they are
    # ONE claim: "everything that passes, with nothing configured". Each control is a
    # line whose removal breaks a specific half of it, and each was run by hand against
    # this table before being added: the whole batch was green on the clean tree, then
    # green with each line mutated, and every one of the sixteen went RED.
    #
    # Three of them did NOT go red on the first attempt, and the fixes are in the tests
    # rather than in the claims. Those three are the reason this table exists:
    #
    #   * the presence latch passed because a ten-second cooldown was doing the latch's
    #     work. The headline test is still there and still worth having, and a second one
    #     now runs with the cooldown at zero so nothing but the latch can suppress
    #     anything.
    #   * the crop-stride mutation `src+c.W*3+3` was a NO-OP: copy() clamps to the
    #     destination's length, so the extra three bytes were dropped and the pixels came
    #     out identical. The control now drops the ROW offset, which shears.
    #   * the second-placeholder refusal was tested on the helper and not on the LOAD,
    #     so deleting the call site would have left it green. `loadPrompts` now takes a
    #     filesystem and the refusal is asserted on a prompt set built in memory.
    #
    # The claim being protected is that a smallest-object detection is a measurement
    # with a provenance, not a hope. A bird 40 px across at 30 m is delivered as eight
    # pixels by a whole-frame pass, below the floor at which any detector is reliable,
    # and roughly a hundred through a 200x150 crop. Every control below exists because a
    # number that is right with the wrong label, or right for the wrong reason, is the
    # failure this repository exists to end.
    @{
      name = 'presence: a line-less zone runs the presence machine'
      file = 'internal\detect\engine.go'
      from = 'if len(e.zone.Lines) == 0 {'
      to   = 'if false { // REDCHECK: presence mode became unreachable'
      pkg  = './internal/detect'
      why  = 'the primary configuration reports nothing at all. A zone with no line is the DEFAULT, so this is a run that looks healthy, records zero detections and raises no error, which is the most expensive silence in the module.'
    },
    @{
      name = 'presence: one track is one event'
      file = 'internal\detect\engine.go'
      from = 'if !t.allowed || t.announced {'
      to   = 'if !t.allowed { // REDCHECK: the per-track latch was removed'
      pkg  = './internal/detect'
      why  = 'a track that stays on screen re-announces itself every sample, so one bird becomes a hundred events and the event rate in the report becomes a function of the frame rate. A crossing can re-arm because a line has two sides; presence has no second side to wait for.'
    },
    @{
      name = 'source-aware minimum: a crop-sourced sighting uses the LOWER bar'
      file = 'internal\detect\engine.go'
      from = 'if cut[b] {'
      to   = 'if false { // REDCHECK: every sighting was held to the whole-frame bar'
      pkg  = './internal/detect'
      why  = 'the crop pass still runs and still costs an extra inference per region, and the smallest object the system exists to report is erased by the anti-jitter rule anyway. A bird at 30 m is on screen about a second and a half, which at the detection rate is two or three samples.'
    },
    @{
      name = 'source-aware minimum: a crop minimum ABOVE the whole-frame one is refused'
      file = 'internal\detect\engine.go'
      from = 'if cfg.CutMinFrames > cfg.TrackMinFrames {'
      to   = 'if false { // REDCHECK: the stricter bar on the better measurement was accepted'
      pkg  = './internal/detect'
      why  = 'the run reports a source-aware minimum while applying one stricter bar to both, so the crop pass costs a whole extra inference per sighting and buys nothing. A magnified sighting and a whole-frame one are not the same kind of evidence.'
    },
    @{
      name = 'crop pass: a region a detection covers is not inferred twice'
      file = 'internal\soak\crop.go'
      from = 'if coveredByAny(region, f, whole) {'
      to   = 'if false { // REDCHECK: overlapping crops are inferred again'
      pkg  = './internal/soak'
      why  = 'a crop is letterboxed to the same 416x416 as the whole-frame pass, so a second inference over pixels already answered costs a full inference to find the same car, and the union then has to deduplicate what the gate should have refused.'
    },
    @{
      name = 'crop pass: an ABUTTING region is still inferred'
      file = 'internal\ortdetect\crop.go'
      from = 'return a.X1 < b.X2 && b.X1 < a.X2 && a.Y1 < b.Y2 && b.Y1 < a.Y2'
      to   = 'return a.X1 <= b.X2 && b.X1 <= a.X2 && a.Y1 <= b.Y2 && b.Y1 <= a.Y2 // REDCHECK: touching counted as overlapping'
      pkg  = './internal/ortdetect'
      why  = 'regions sharing no pixels are treated as overlapping, so every crop beside a large detection goes unmeasured. A flock against a wall is a row of small regions along one big box and all of them vanish.'
    },
    @{
      name = 'crop pass: the same object from two passes is one box'
      file = 'internal\soak\crop.go'
      from = 'if overlapsAnyAbove(c, whole, cropDedupeIOU) {'
      to   = 'if false { // REDCHECK: both passes report one object'
      pkg  = './internal/soak'
      why  = 'one car appears twice in the overlay, twice in the class histogram and as two tracks with two histories, while the whole-frame box wins the union anyway, so the second box is a duplicate with no evidence behind it.'
    },
    @{
      name = 'region_source: a crop-sourced box records cut, not measured'
      file = 'internal\soak\trigger.go'
      from = 'if cut {'
      to   = 'if false { // REDCHECK: every box recorded `measured`'
      pkg  = './internal/soak'
      why  = 'the stored column claims the box was measured at the pipeline own resolution when it was found in a magnified crop. Per invariant 2 a fact that is displayed but not stored gets re-derived, and a re-derived region source is a re-inference on the one field a reader uses to tell the two passes apart.'
    },
    @{
      name = 'crop geometry: the crop OFFSET is applied on the way back'
      file = 'internal\ortdetect\crop.go'
      from = 'X1: (rx + b.X1*rw) / fx,'
      to   = 'X1: (b.X1*rw) / fx, // REDCHECK: the crop offset was dropped'
      pkg  = './internal/ortdetect'
      why  = 'every crop-sourced box lands at the origin of the crop with plausible coordinates and no error anywhere. This is the letterbox-divisor defect in a new place: the arithmetic is a couple of additions and forgetting one of them is invisible until somebody reports a bird in the wrong tree.'
    },
    @{
      name = 'crop geometry: every row of a crop reads its OWN row'
      file = 'internal\ortdetect\crop.go'
      from = 'src := ((c.Y+row)*frameW + c.X) * 3'
      to   = 'src := ((c.Y)*frameW + c.X) * 3 // REDCHECK: the row offset was dropped'
      pkg  = './internal/ortdetect'
      why  = 'every row of the crop is the first row, so the crop is a horizontally striped image of the object. A detector answers confidently about a sheared picture, and the box it returns is the box for the wrong pixels.'
    },
    @{
      name = 'prompt: the detected class is substituted into the prompt'
      file = 'internal\vlm\prompts.go'
      from = 'p.Text = strings.ReplaceAll(p.Text, classPlaceholder, detected)'
      to   = 'p.Text = strings.ReplaceAll(p.Text, classPlaceholder, "") // REDCHECK: the class was dropped'
      pkg  = './internal/vlm'
      why  = 'the model is told its own evidence is weaker than it is. A detector that placed a box on a bird it was confident about, and a novelty run that measured a brightness change and named nothing, send the SAME prompt, so the verifier decides on a premise about the clip that is false for one of them.'
    },
    @{
      name = 'prompt: a second placeholder is refused at load'
      file = 'internal\vlm\prompts.go'
      from = 'if extra := strayPlaceholder(string(body)); extra != "" {'
      to   = 'if extra := ""; extra != "" { // REDCHECK: an unsubstituted token would be sent'
      pkg  = './internal/vlm'
      why  = 'a literal placeholder token reaches the provider, the model reads it as text, and nothing in the attribution record accounts for it. An answer produced from a prompt nobody can quote is an answer nobody can reproduce.'
    },
    @{
      name = 'config: the track minimum may not exceed the samples a dwell produces'
      file = 'internal\config\config.go'
      from = 'if c.AIFPS > 0 && c.TrackMinFrames > 0 {'
      to   = 'if false { // REDCHECK: the two were validated independently again'
      pkg  = './internal/config'
      why  = 'a minimum of 9 samples at 2 inferences per second is unreachable for an object on screen about two seconds, so every object the run exists to report is silently dropped. The rule that erases them is working exactly as written, which is why nothing reports an error.'
    },
    @{
      name = 'config: a crop minimum above the global one is refused'
      file = 'internal\config\config.go'
      from = '} else if c.CropMinFrames > c.TrackMinFrames {'
      to   = '} else if false { // REDCHECK: the inverted pair loads'
      pkg  = './internal/config'
      why  = 'the same defect one layer earlier, at load rather than at New, so it is caught before a session is opened and before any frame is decoded'
    },
    @{
      name = 'config: an empty SP_POC_ZONES is presence mode, not a refusal'
      file = 'internal\config\config.go'
      from = '{Name: "SP_POC_ZONES", kind: kindString, structural: true, structuralVal: "",'
      to   = '{Name: "SP_POC_ZONES", kind: kindString,'
      pkg  = './internal/config'
      why  = 'the primary configuration is refused. This line exists because a guard written against an earlier engine outlived the engine: crossing was the only mode when SP_POC_ZONES was required, and the presence path made the refusal false without anything noticing.'
    },
    @{
      name = 'config: the crop on/off switch is required by name'
      file = 'internal\config\config.go'
      from = '{Name: "SP_POC_CROP_PASS", kind: kindBool, when: "SP_POC_TRIGGER_MODE=detector",'
      to   = '{Name: "SP_POC_CROP_PASS", kind: kindBool, structural: true, structuralVal: "false", when: "SP_POC_TRIGGER_MODE=detector",'
      pkg  = './internal/config'
      why  = 'a run that cannot see a bird at 30 m and a report claiming to report everything that passes are the same run, and nothing in the output says otherwise. Same reasoning as SP_POC_CAMERA_INTEGRITY: the default is the dangerous one, so it cannot be a default.'
    },
    # --- the novelty rate (0.6.5), the view-change rule (0.6.4), the search surface (4b) ---
    #
    # Twenty more controls, each run BY HAND before it entered this table: green on the
    # clean tree, green with the line mutated, red with the line mutated.
    #
    # SIX of these twenty-eight in total did not go red on the first attempt, and every
    # fix was in the TEST rather than in the claim. They are the reason this table exists
    # and they are the reason its entries are worth reading:
    #
    #   * the presence latch was masked by the class cooldown (a zero-cooldown test now).
    #   * the crop-stride mutation was a no-op, because copy() clamps to the destination.
    #   * the second-placeholder refusal was tested on the helper and not on the LOAD.
    #   * InvalidateAll's control let 19 seconds elapse between the last sighting and the
    #     re-sighting, so the tracks aged out on their own and a mutation that KEPT them
    #     passed a test that had already removed them by other means.
    #   * a "camera move is declared once" control stayed green because the SETTLING
    #     WINDOW already covers the frames after a change, so the agreement counter is
    #     never read again during it.
    #   * the search-scope control mutated `if len(scope) == 0` to `if false`, which
    #     reaches the same empty IN list by a different route and is a no-op.
    #
    # And ONE line is deliberately NOT in this table: the agreement counter is cleared at
    # BOTH the declaration site and the ordinary-frame branch, and neither clear is
    # individually red-checkable because the other one covers it. Redundant by design,
    # and a control that cannot be removed is not a control. The BEHAVIOUR is protected --
    # a second camera move an hour later still needs its two frames -- and the claim is
    # made about the behaviour rather than about a line.
    @{
      name = 'novelty gate: a nil gate means every frame'
      file = 'internal\novelty\gate.go'
      from = 'if g == nil {
		return true
	}'
      to   = 'if g == nil { // REDCHECK: a nil gate dereferences itself'
      pkg  = './internal/novelty'
      why  = 'a caller that does not watch view integrity or does not carry a crop pass passes nil, and nil reads as every frame. An earlier revision wrote the nil case as a branch condition that fell through to mark(), which dereferences it -- so the cost of reading nil as every frame rather than as a decision is a panic in the frame loop.'
    },
    @{
      name = 'novelty gate: the interval is rounded DOWN, so the gate is never slower than asked'
      file = 'internal\novelty\gate.go'
      from = 'if frames := g.intervalNS / frameNS; frames >= 1 {'
      to   = 'if frames := (g.intervalNS / frameNS) + 1; frames >= 1 { // REDCHECK: rounded UP'
      pkg  = './internal/novelty'
      why  = 'the rounding direction is the decision and it is not symmetric. At 25 fps a 10/s request rounds to 8.4/s rounding up and 12.5/s rounding down. Over-shooting costs arithmetic and finds more; under-shooting silently narrows the set of regions that get cropped, and the smallest object this system exists to report is exactly a region the gate did not propose. One of those two errors shows up on a cost projection and the other never shows up at all.'
    },
    @{
      name = 'novelty gate: a rate it could not meet is SAID, not absorbed'
      file = 'internal\novelty\gate.go'
      from = 'if g.Clamped() {'
      to   = 'if false { // REDCHECK: the shortfall is absorbed silently'
      pkg  = './internal/novelty'
      why  = 'a gate prints the requested rate next to counters produced by another rate. That is the shape of claim this repository exists to refuse, and the cheapest way to commit it is to quantise the interval and not mention it.'
    },
    @{
      name = 'novelty rate: a rate that cannot propose is refused BY NAME'
      file = 'internal\novelty\gate.go'
      from = 'return got, fmt.Errorf('
      to   = 'return got, nil && fmt.Errorf( // REDCHECK: a refusal that is not a refusal'
      pkg  = './internal/novelty'
      why  = 'a bird on screen 1.5 s proposed once is a bird whose detection depends on where in its dwell the gate happened to sample. The refusal has to name SP_POC_NOVELTY_FPS and say the sample shortfall, because "raise the rate" is advice and "you are one sample short of a bird" is a number.'
    },
    @{
      name = 'view change: one frame of movement is not a camera move'
      file = 'internal\novelty\viewchange.go'
      from = 'if v.agree < v.cfg.SustainFrames {'
      to   = 'if false { // REDCHECK: a single frame declares a camera move'
      pkg  = './internal/novelty'
      why  = 'one frame of a wide change is a door opening, a curtain or a compression artefact. It is exactly the case SP_POC_TRACK_MIN_FRAMES exists to reject, and applying a different standard to the same phenomenon is how a pipeline ends up with a jitter rule and a no-jitter rule.'
    },
    @{
      name = 'view change: brightness alone is not enough'
      file = 'internal\novelty\viewchange.go'
      from = 'moved := absF(sc.Illumination) >= v.cfg.MinIllumination && absF(sc.Spread) >= v.cfg.MinSpread'
      to   = 'moved := absF(sc.Illumination) >= v.cfg.MinIllumination // REDCHECK: the spread term was dropped'
      pkg  = './internal/novelty'
      why  = 'a cloud crossing a window moves the whole frame luma without moving the camera, and the illumination offset is large. A rule using one term suppresses every presence event for the duration of a cloud, which is worse than the flood it prevents.'
    },
    @{
      name = 'view change: the SIGNED offset is compared, so a tilt either way fires'
      file = 'internal\novelty\viewchange.go'
      from = 'moved := absF(sc.Illumination) >= v.cfg.MinIllumination && absF(sc.Spread) >= v.cfg.MinSpread'
      to   = 'moved := sc.Illumination >= v.cfg.MinIllumination && absF(sc.Spread) >= v.cfg.MinSpread // REDCHECK'
      pkg  = './internal/novelty'
      why  = 'a camera tilting toward the sky and one tilting toward the ground are the same event and the offset is negative as often as it is positive. A detector that only fires on one of them works in half the rooms.'
    },
    @{
      name = 'view change: a frame with no measurement decides NOTHING'
      file = 'internal\novelty\viewchange.go'
      from = 'if sc.Reason == ReasonCorrupt || sc.Reason == ReasonFirstFrame {'
      to   = 'if false { // REDCHECK: an unmeasurable frame is scored as a change'
      pkg  = './internal/novelty'
      why  = 'deciding on a frame the engine could not score is how a detector starts declaring camera faults on a run whose camera is fine. A corrupt frame is neither a change nor a confirmation, and it must not consume a sustain frame either, or a stream of them would hold the gate shut indefinitely.'
    },
    @{
      name = 'view change: opening the gate by hand does not CLAIM a detection'
      file = 'internal\novelty\viewchange.go'
      from = 'v.settle = v.cfg.SettleFrames
	v.agree = 0
}'
      to   = 'v.settle = v.cfg.SettleFrames
	v.agree = 0
	v.changes++ // REDCHECK: an operator opening the gate was counted as a detection'
      pkg  = './internal/novelty'
      why  = 'an operator who knows the camera moved has to be able to gate presence WITHOUT the system claiming it detected anything. Conflating the two puts a number on the report that nobody can audit, and it is the one number the demo would quote.'
    },
    @{
      name = 'view change: the presence gate withholds events'
      file = 'internal\detect\engine.go'
      from = 'if e.suppressed {
		e.suppressedEvents++
		return nil
	}'
      to   = 'if false { // REDCHECK: presence is emitted straight through a moved camera'
      pkg  = './internal/detect'
      why  = 'this is where the flood comes from. Every box in the panned view falls inside some old track neighbourhood, the association threshold matches them, and one physical camera move becomes fifty presence events beside a correct camera-integrity event.'
    },
    @{
      name = 'view change: InvalidateAll drops the tracks, not just the count'
      file = 'internal\detect\engine.go'
      from = 'e.live = make(map[int]*Track, len(e.live))
	e.next = 0'
      to   = '// REDCHECK: the tracks were counted and kept
	e.next = 0'
      pkg  = './internal/detect'
      why  = 'a wall at x=0.1 is still at x=0.1 in the new view noise and IoU does not know the camera moved, so keeping the map re-associates the old tracks to the new view. The count would be right and the behaviour wrong, which is the combination a count-only test cannot see.'
    },
    @{
      name = 'crop pass: the rate gate runs BEFORE the novelty engine'
      file = 'internal\soak\crop.go'
      from = 'if !cp.Gate.Due(f.Seq, f.AtNS) {'
      to   = 'if false { // REDCHECK: novelty is paid on every frame and the answer discarded'
      pkg  = './internal/soak'
      why  = 'the operator who set SP_POC_NOVELTY_FPS=2 is buying 12x on the novelty pass. Calling the engine and then throwing its answer away spends the whole cost the gate exists to save, while leaving the proposer at one call per frame.'
    },
    @{
      name = 'crop pass: a gated frame is COUNTED'
      file = 'internal\soak\crop.go'
      from = 'ct.GatedOut++'
      to   = '// REDCHECK: the gate dropped frames without saying so'
      pkg  = './internal/soak'
      why  = 'a gate that skipped everything and reported zero is indistinguishable from a detector that cannot see small objects, which is the fault the crop pass was built to remove. A cap that silently drops is a cap that lies.'
    },
    @{
      name = 'crop pass: a changed view discards the regions it proposed'
      file = 'internal\soak\crop.go'
      from = 'ct.ViewChanges++
		return ct, true'
      to   = 'ct.ViewChanges++
		return ct, false // REDCHECK: the regions are cropped from a frame mid-pan'
      pkg  = './internal/soak'
      why  = 'a region found on a frame whose view has changed describes a position the camera was pointing at for one frame. Cropping it measures a place that no longer exists, and deferring carries it into the next frame where it does not exist either.'
    },
    @{
      name = 'search: an empty scope matches NOTHING rather than everything'
      file = 'internal\store\search.go'
      from = 'return []string{r}
	}
	if len(q.RunIDs) == 0 {
		return nil
	}'
      to   = 'return []string{r}
	}
	if len(q.RunIDs) == 0 {
		return []string{q.RunID} // REDCHECK: an empty scope resolves to a run nobody named
	}'
      pkg  = './internal/store'
      why  = 'a camera filter is not a run. It names one level up in runs.source_arg, and answering a question about no particular run is not a narrower answer to this question. A filter that silently widens to every run looks like it worked, which is worse than no filter at all.'
    },
    @{
      name = 'search: every card carries the run id it came from'
      file = 'internal\store\searchcards.go'
      from = 'h.RunID, h.RunLabel, h.SourceArg = runID.String, runLabel.String, sourceArg.String'
      to   = 'h.RunLabel = runLabel.String // REDCHECK: the run id is blanked'
      pkg  = './internal/store'
      why  = 'a run has its own model identity, its own contract version and its own source, so answers from two runs are not comparable unless both are visible on the card itself. The same reasoning that refuses to compare a v1 answer with a v2 answer refuses to compare two runs without their labels.'
    },
    @{
      name = 'search: a reviewer own note is findable'
      file = 'internal\store\searchcards.go'
      from = 'OR lower(g.note) LIKE ?'
      to   = 'OR 1=0 -- REDCHECK: the judgement of the reviewer is unretrievable'
      pkg  = './internal/store'
      why  = 'the highest value per line in the whole search phase. An operator who wrote "that is the barrier shadow, not a stopped car" can find it by typing three words. Searching only the model sentence makes the reviewer judgement unretrievable, which is the whole reason the column exists.'
    },
    @{
      name = 'search: the confidence flag comes from the ANSWER, not the number'
      file = 'internal\store\searchcards.go'
      from = 'h.HasConfidence = h.AIState == aistate.Asked'
      to   = 'h.HasConfidence = h.Confidence > 0 -- REDCHECK: a model answering zero shows no number'
      pkg  = './internal/store'
      why  = 'confidence is NOT NULL DEFAULT 0, so a row nobody asked about carries 0 and a model may legitimately answer 0. Treating the number as the flag prints a fabricated measurement for one and hides a real answer for the other.'
    },
    @{
      name = 'search: a card with no clip does not lose the whole search'
      file = 'internal\store\search.go'
      from = 'var tb, te sql.NullString
		if err := rows.Scan(&h.DetectionID, &at, &h.Class, &h.Summary, &h.Verdict, &h.AIState,'
      to   = 'var tb, te string
		if err := rows.Scan(&h.DetectionID, &at, &h.Class, &h.Summary, &h.Verdict, &h.AIState,'
      pkg  = './internal/store'
      why  = 'evidence is a LEFT JOIN and a decision can be stored without its clip, because the clip may have been rotated away by the disk budget. Scanned into plain strings, one such row aborts the entire search with "converting NULL to string is unsupported" -- losing every card in the window because ONE of them had no evidence.'
    },
    @{
      name = 'config: the novelty rate must cover the smallest dwell'
      file = 'internal\config\config.go'
      from = 'if _, nerr := novelty.NoveltyRateCoversDwell(c.NoveltyFPS, novelty.SmallestDwellSeconds,'
      to   = 'if _, nerr := error(nil), error(nil); false && nerr != nil { // REDCHECK'
      pkg  = './internal/config'
      why  = 'the failure this refuses is a rate that cannot propose a region often enough for the smallest object, which looks exactly like a detector that cannot see small objects. Same symptom, different owner, and only one of them is fixable by a better model.'
    },
    # --- the per-class table (0.6.3) and the crop self-test (0.5.4) ---
    #
    # Eight more, each run by hand before entering the table.
    #
    # TWO of them are here because this session's own work produced a wrong number and a
    # test caught it, and both are the kind of defect this repository exists to end:
    #
    #   * the per-class PRECISION denominator omitted matched detections, so precision
    #     came out at 2.000 on a frame with two hits and one false positive. A rate
    #     above one is not a subtle error; it is a table nobody should read, and it got
    #     there by an omission that no compiler and no reviewer would have noticed.
    #   * the wrong-class match counted as a true positive for BOTH sides, so a bird
    #     found as a car reported a perfect run on a frame where the species was wrong.
    @{
      name = 'per class: a MATCHED detection still counts in the precision denominator'
      file = 'internal\bench\perclass.go'
      from = 'if det != nil {
			det.Detected++
		}
		if gt != nil && det != nil && gt == det {'
      to   = 'if det != nil && gt != nil && gt != det {
			det.Detected++
		}
		if gt != nil && det != nil && gt == det {'
      pkg  = './internal/bench'
      why  = 'precision came out at 2.000 on a frame with two hits and one false positive. A rate above one is not a subtle error: it is a table nobody should read, and it got there by an omission no compiler and no reviewer would have caught.'
    },
    @{
      name = 'per class: a wrong-class match is not a true positive for either side'
      file = 'internal\bench\perclass.go'
      from = 'if gt != nil && det != nil && gt == det {
			gt.TruePositives++
			continue
		}'
      to   = 'if gt != nil && det != nil {
			gt.TruePositives++
			det.TruePositives++
			continue
		}'
      pkg  = './internal/bench'
      why  = 'a bird found as a car would count as a hit for bird AND a hit for car, producing two correct-looking rates for one mistake. It is a miss for the class it is and a false positive for the class it was called, and counting it once in either column makes one of the two rates wrong.'
    },
    @{
      name = 'per class: a precision over ZERO detections is NOT RUN, not zero'
      file = 'internal\bench\perclass.go'
      from = 'if c.Detected == 0 {
		return 0, false
	}'
      to   = 'if c.Detected == 0 {
		return 0, true // REDCHECK: a printed 0.00 claims every detection was wrong, and there were none
	}'
      pkg  = './internal/bench'
      why  = 'a printed 0.00 over no detections is a claim that every detection was wrong when there were none -- and the class with no detections is usually exactly the one a reader most wants a number for, so the absent figure is the one that gets quoted.'
    },
    @{
      name = 'per class: an INEXPRESSIBLE class is dropped, not counted as a miss'
      file = 'internal\bench\perclass.go'
      from = 'if g.Ignored || !g.Expressible || usedGT[gi] {'
      to   = 'if g.Ignored || usedGT[gi] { // REDCHECK: a vocabulary penalty became a detector failure'
      pkg  = './internal/bench'
      why  = 'a class this build cannot name is a vocabulary penalty. Counting it as a false negative reports a recall figure that is really a statement about the labels, and it does so on exactly the classes nobody labelled.'
    },
    @{
      name = 'crop self-test: the whole-frame pass must come out BELOW the floor'
      file = 'cmd\pocctl\cropselftest.go'
      from = 'case wholePx >= floorPx:'
      to   = 'case false: // REDCHECK: a frame size that needs no crop is reported as proving one does'
      pkg  = './cmd/pocctl'
      why  = 'the row claims a whole-frame pass cannot see a bird at 30 m. If the measured delivery is at or above the floor then this frame size does NOT demonstrate that, and reporting it as a win is the failure the whole row exists to prevent.'
    },
    @{
      name = 'crop self-test: a crop that stays below the floor FAILS'
      file = 'cmd\pocctl\cropselftest.go'
      from = 'case cropPx < floorPx:'
      to   = 'case false: // REDCHECK: a crop that does not magnify is reported as a win'
      pkg  = './cmd/pocctl'
      why  = 'a crop that delivers less than the floor is a whole inference spent on nothing, and it is paid on every frame novelty proposes a region. Reporting it as a win is how a crop pass becomes unaffordable while looking healthy.'
    },
    @{
      name = 'crop self-test: a factor under fourfold FAILS'
      file = 'cmd\pocctl\cropselftest.go'
      from = 'case ratio < 4:'
      to   = 'case ratio < 0.0001: // REDCHECK: any margin at all is reported as a win'
      pkg  = './cmd/pocctl'
      why  = 'the cost argument rests on the order of magnitude, not on an improvement. A factor of two is destroyed by one number changing in the crop size, and a thin margin presented as a thick one is the arithmetic version of the false-positive-rate story this module was written to refuse.'
    },
    @{
      name = 'crop self-test: a crop box outside the crop rectangle FAILS'
      file = 'cmd\pocctl\cropselftest.go'
      from = 'return b.X1 >= loX-tol && b.X2 <= hiX+tol && b.Y1 >= loY-tol && b.Y2 <= hiY+tol'
      to   = 'return true // REDCHECK: the crop offset being dropped on the way back is invisible'
      pkg  = './cmd/pocctl'
      why  = 'dropping the crop offset when mapping a box back puts every box at the frame origin, with plausible coordinates and no error anywhere. That is the letterbox divisor defect in a new place, and the containment test is the only thing that notices it.'
    },
    # --- the caller side: a hand-built environment that fell behind the schema.
    #
    # Every control above removes a guard from inside the pipeline. These three
    # remove a NAME or a VALUE from `demoEnv`, which is a slice of `NAME=value`
    # strings in `cmd/pocctl` that `pocctl demo` hands to a child process. They are
    # here because the defect they guard is the one this table could not see at
    # all: a required variable added to `internal/config` does not reach a
    # hand-built slice, every test in `internal/config` stays green, and the demo
    # refuses to start at step 6 of 7 — after two builds, after the fault
    # rehearsal, and after the push target is listening. The schema does not know
    # the slice exists, so nothing in the schema could catch it. Only running the
    # command did.
    @{
      # The first entry of the eight `when: SP_POC_TRIGGER_MODE=detector`
      # variables. One name stands for all eight: they were added to the schema on
      # one commit and the demo's slice was not updated, and the mutation renames
      # rather than deletes so the slice still parses — a deleted line is a
      # compile error, which this gate reads as a red for the wrong reason.
      name = 'the demo environment names every variable the schema requires'
      file = 'cmd\pocctl\demo.go'
      from = '"SP_POC_DET_MODEL=" + model,'
      to   = '"SP_POC_DET_MODEL_RENAMED_BY_REDCHECK=" + model,'
      pkg  = './cmd/pocctl'
      why  = 'the detector opens no file, draws nothing, and reports a zero detection rate at full health with no error anywhere. Every number in the report is true and describes a run that never looked at a frame'
    },
    @{
      # The half of the score chain that is checked ONLY in detector mode, and the
      # one relation of the three that no downstream arithmetic can detect.
      name = 'the score chain in the demo environment ascends'
      file = 'cmd\pocctl\demo.go'
      from = '"SP_POC_DET_SCORE=0.005",'
      to   = '"SP_POC_DET_SCORE=0.5",'
      pkg  = './cmd/pocctl'
      why  = 'a box the model scored below its own emit floor is drawn as a ghost while a box it did emit is refused, so the screen shows objects that are not there and omits objects that are. The run completes and every number in the report is computed over that picture'
    },
    @{
      # A closed set of three trigger modes and a hand-written branch per mode,
      # where the mode that became the shipped default is the one nobody added a
      # branch for. This one printed, on the first screen of the headline
      # command, that the run was going to be refused — and the run completed.
      name = 'the banner describes the trigger the demo actually runs'
      file = 'cmd\pocctl\demo.go'
      from = 'case soak.TriggerDetector:'
      to   = 'case "not-a-mode":'
      pkg  = './cmd/pocctl'
      why  = 'the banner is derived from the configuration on purpose, and a sentence borrowed from another mode tells an operator reading a cost argument that this run has no ONNX in it while the model and its SHA-256 are printed two lines below'
    },
    @{
      # The plan's B3. The weights are not in git, so a download is the only way
      # a judge gets them, and until now the fetch checked nothing at all: it
      # wrote whatever the URL delivered and printed a size. This control is the
      # digest comparison, and it is the ONLY thing standing between "a model is
      # on disk" and "THE model the committed contract was measured against".
      name = 'the model download matches the digest the contract was measured against'
      file = 'cmd\pocctl\cap.go'
      from = 'case sum == expected:'
      to   = 'case false && sum == expected: // REDCHECK: the pinned model digest check was removed'
      pkg  = './cmd/pocctl'
      why  = 'the bytes a judge downloads from the pinned URL are never compared with the SHA-256 recorded in pyworker/fixtures/yolov4n.licence.json, so a run loads a model that may not be the one every number in this repository describes — and nothing complains, because a 245 MB ONNX file loads either way'
    },
    @{
      # The plan's B2. `pocctl demo` used to open with testdata\youtube\
      # sTF-6_xinUU.mp4 when --clip was absent: real footage, real moving
      # vehicles, and a labels.json row saying it may not be shown to anyone
      # outside this machine. The default is gone and this is its refusal. The
      # removal is worth watching go red precisely because of what replaces it:
      # without the refusal, `demo` falls through to the file-exists check and
      # fails with "demo:  is missing" — the same missing-file wall a judge met
      # on a clean clone, with a better-looking message.
      name = 'the demo refuses rather than choosing footage on the operator'
      file = 'cmd\pocctl\demo.go'
      from = 'if clip == "" {
			return noClipGiven(root)
		}'
      to   = 'if false { // REDCHECK: the no-default-clip refusal was removed
			return noClipGiven(root)
		}'
      pkg  = './cmd/pocctl'
      why  = 'the command falls through to the file-exists check and fails with "demo:  is missing" — the missing-file wall a judge met on a clean clone before B2, wearing a slightly better message. And a demo with no clip is a demo whose footage was picked by the program, which is exactly how the YouTube default survived for weeks'
    },
    @{
      # Linux and macOS release assets are .tgz, not .zip, so a fetch written
      # against archive/zip works on one of the three platforms its own error
      # message names — and it failed with a complaint about a FORMAT, which is
      # the same shape of defect as refusing by platform name. The dispatch is
      # the control; the selection rules and the depth rule are written once
      # against member names and this line is what decides which reader answers.
      name = 'a release asset in the other archive format is still read'
      file = 'cmd\pocctl\runtime.go'
      from = 'case strings.HasSuffix(name, ".tgz"), strings.HasSuffix(name, ".tar.gz"):'
      to   = 'case false && strings.HasSuffix(name, ".tgz"): // REDCHECK: the tarball release format was removed'
      pkg  = './cmd/pocctl'
      why  = 'every Linux and macOS ONNX Runtime asset is refused as "not a release archive this build can read", so bootstrap on those platforms cannot fetch a runtime at all and the refusal says a format rather than an asset'
    },
    @{
      # The demo prints an excerpt of the report, and the bound on that excerpt is
      # a control: it decides which of the two "why did the run stay quiet"
      # sections a reader sees. For weeks it was 90, and on a report whose
      # detector and verification sections carry real numbers the crop-gate
      # section starts at line 120 -- so the excerpt ended a quarter of the way
      # into "what the detector did" and the counter that says whether the camera
      # was looking at all was never printed. The test that was supposed to catch
      # that used a bare Report{} with one counter in it, where the section sits
      # at line 61 and any window above 61 passes: it could not fail when its
      # subject was wrong.
      #
      # The control is the WINDOW, not the section. Mutating it to 40 is the same
      # one-character change with the section at 120 rather than at 61, and this
      # time the test goes red naming both sections and the line numbers they
      # occupy. The bound is read out of this file by the test rather than
      # restated beside it, which is the part that makes the control real.
      name = 'the demo prints the two sections that answer why the run stayed quiet'
      file = 'cmd\pocctl\demo.go'
      from = 'for sc.Scan() && n < 160 {'
      to   = 'for sc.Scan() && n < 40 { // REDCHECK: the demo''s report window no longer reaches either quiet-run section'
      pkg  = './internal/soak'
      test = 'TestTheDemoReportWindow'
      why  = 'the excerpt a judge reads ends part-way through the detector section, so the per-site/per-class verification slice and the crop-gate section -- the two places a run can say that it asked and was not answered, or chose not to look -- are both invisible. The verdict is printed and the reason a run stayed quiet is not, which is the exact sentence the POC exists to refuse'
    },
    @{
      # The per-site, per-class slice's arithmetic, and the invariant it carries is
      # the platform's third: A MISSING AI OBSERVATION IS NEVER A NEGATIVE ONE.
      #
      # `no_objects` is a real answer -- the model ran, parsed, and the array was
      # empty, which is the only state in which "the model found nothing" is a
      # statement about the scene. `degraded`, `not_asked` and `cannot_tell` are
      # not. Folding any of those three into the answered half is how a run with a
      # dead sidecar reports a detection rate about frames nobody answered, which
      # is the most expensive sentence this module can print.
      #
      # The control is the predicate, and the mutation is `if true` -- every state
      # becomes an answer. The share collapses to 0.00, the section says the model
      # was served perfectly, and the two tests below go red naming the numbers
      # that moved. It is deliberately not a test about the rendered table: a
      # table test would pass while the arithmetic was folded, because the table
      # renders whatever arithmetic it is handed.
      name = 'verification slice: a state nobody answered for is not an answer'
      file = 'internal\soak\verification_slice.go'
      from = 'if aistate.IsAnswered(k) {'
      to   = 'if true { // REDCHECK: a state nobody answered for is counted as an answer'
      pkg  = './internal/soak'
      test = 'TestABlindState|TestAStateOutside|TestTheShareIsPrinted|TestAnUnmeasured'
      why  = 'the unanswered share collapses to 0% and the report states that the model was being served, on a run where a third of the detections were never answered at all. This is the platform''s invariant 3 seen from the module that is actually demonstrated, and the slice is the only place in the POC where the run-level arithmetic says so'
    },
    # --- the public relay, and the run's own honesty about whose camera it is
    #
    # `pocctl live` resolves the curated cameras in cameras.json to HLS playlists
    # with yt-dlp, and the run that reads one is labelled "public relay of a real
    # camera, NOT ours". That sentence is the whole honesty of the path, and it
    # exists because the two sentences available before it are both lies about the
    # same stream: `Relay`'s denies a camera that is demonstrably there, and the
    # plain live sentence says nothing about whose hardware it is.
    #
    # The five controls below were hand-run in order on 2026-10-15: each mutated,
    # each watched go RED, each restored and watched GREEN. They are only entered
    # here now because of that order.
    @{
      # The label is DERIVED from the flag, and it is asserted as a sentence rather
      # than as a substring: the test requires the ownership claim AND refuses the
      # recording sentence. A label that loses "NOT ours" over a public camera
      # claims hardware this installation does not own, which is the same defect
      # class D-5 exists for, one direction over.
      #
      # The anchor matches BOTH live kinds on purpose: rtsp and url derive the
      # same sentence from the same flag, and the mutation removes it from both.
      # That is one control — "the public-relay sentence is derived" — and not two,
      # and a table entry per kind would be one control counted twice.
      name = 'a public relay is labelled somebody else''s camera'
      file = 'internal\source\source.go'
      from = '		if s.PublicRelay {'
      to   = '		if false { // REDCHECK: the public-relay sentence was never derived'
      pkg  = './internal/source'
      test = 'TestAPublicRelayIsLabelledSomebodyElsesCamera'
      why  = 'a run over a real camera streamed by somebody else prints as this site''s own hardware, or as a recording that is not there. Both are claims the module cannot back, in opposite directions, which is why the flag is separate from Relay and the sentence is derived from it'
    },
    @{
      # A row in the curated table with no recorded licence basis is refused at
      # load. A public feed being PUBLIC is not the same as being free to record:
      # watching it live and storing clips of it are different acts, and the
      # second is the operator's to permit. The refusal is what stops an operator
      # adding a camera by pasting a URL, which is how the footage half of this
      # table did its work.
      name = 'a camera row with no recorded licence is refused'
      file = 'internal\cams\cams.go'
      from = '	if strings.HasPrefix(strings.ToUpper(lic), licenceUnrecorded) {'
      to   = '	if false { // REDCHECK: a camera with no recorded licence resolved'
      pkg  = './internal/cams'
      test = 'TestARowWithNoRecordedLicenceIsRefused'
      why  = 'the table stops being the place a basis is recorded and becomes a list of URLs. A run then records footage from a camera whose operator never said anyone may, and no artefact of the run says otherwise'
    },
    @{
      # `SP_POC_SOURCE_PUBLIC` and `SP_POC_SOURCE_RELAY` are different sentences
      # about one source — a recording is not a camera, and a public relay of a
      # real camera is. A run carrying both prints whichever wins the branch order
      # while having verbally claimed the opposite on the previous line, so the
      # pair is refused by name at startup.
      name = 'public-relay and recording-relay cannot both be claimed'
      file = 'internal\config\config.go'
      from = '	if c.SourcePublic && c.SourceRelay {'
      to   = '	if false { // REDCHECK: one source claimed to be a recording and a camera'
      pkg  = './internal/config'
      test = 'TestPublicAndRelayCannotBothBeClaimed'
      why  = 'the run''s source line asserts a camera and denies one in the same configuration, and the one that reaches the screen is whichever the label derived first'
    },
    # --- the calibration record: the only place a p99 may come from
    #
    # The detector's preflight row multiplies a MEASURED p99 by the declared frame
    # rate, so a number typed anywhere but in a measured record is a gate that
    # passes because somebody hoped. Two more controls belong to that record: one
    # is that it is refused when it is not measured, and one is that a record from
    # another build is refused rather than applied.
    @{
      # A calibration measured against a different model cannot describe this
      # run's latency. The comparison is on the file, not on the path string, so
      # the test covers both the mismatch and the "different spelling of the same
      # file" case: without that second half the control would refuse a machine
      # that benched the model it is about to run, from a different directory.
      name = 'a calibration is applied only to the build it measured'
      file = 'internal\soak\calibration.go'
      from = '	if !samePath(c.Model, model) {'
      to   = '	if false { // REDCHECK: another build''s p99 was applied to this one'
      pkg  = './internal/soak'
      test = 'TestACalibrationAppliesOnlyToTheBuildItMeasured'
      why  = 'the detector row multiplies a p99 from a build nobody is running, so a run is cleared against a machine that was never measured — or refused against the one it is on'
    },
    @{
      # The AI p99 row is about the run that HAS a per-frame model on the AI path.
      # On the shipped detector path there is no sidecar, and the row used to fail
      # every detector run forever: `pocctl preflight` in its shipped configuration
      # refused a row about a component the run does not start, which is the exact
      # defect W1-4 removed from the trigger row one row above. The not-applicable
      # branch names the row that holds the number instead, and the sidecar path is
      # still a refusal.
      name = 'the AI row is about the run that has an AI'
      file = 'internal\soak\preflight.go'
      from = '	if in.TriggerMode != config.TriggerModeSidecar {'
      to   = '	if false { // REDCHECK: a run with no sidecar failed the budget row'
      pkg  = './internal/soak'
      test = 'TestTheAIRowIsAboutTheRunThatHasAnAI'
      why  = 'the detector run refuses at startup over a measurement that cannot exist for it. A gate that cannot pass on the shipped path is a gate that gets deleted, and the row that said so genuinely was'
    },
    # --- the keystore boundary, found by a flake --------------------------------
    #
    # This control was not written because somebody looked for it. It was written
    # because the gate's own suite failed ONCE in fifteen runs on a test that had
    # been green for months: `TestTheTrustedKeyIsRefusedWhenItIsAPublicKey...`
    # drew a fresh random ed25519 key each run and parsed it as a raw trusted key,
    # and roughly one run in twenty draws a key whose first or last byte is a byte
    # `strings.TrimSpace` strips.
    #
    # The order of two branches decided whether a trusted key could be read at
    # all: the raw 32-byte case sat AFTER the bytes had been through `string()`
    # and `TrimSpace`, which is text handling applied to a binary key. About 5% of
    # all keys begin or end with one of six whitespace bytes, and every one of
    # those was trimmed to a different length and refused as "neither PEM, base64
    # nor a raw 32-byte key" -- the trust decision, failing on the shape of the
    # key's own randomness, intermittently.
    #
    # A defect that appears once in twenty runs is worse than a missing control,
    # because a green run means nothing about the next one. So the test is now
    # deterministic -- a key whose OWN first byte is a space -- and the branch
    # order is the control.
    @{
      name = 'a raw trusted key is read by its bytes, not by its trimmed text'
      file = 'internal\store\seal.go'
      from = '	if len(raw) == ed25519.PublicKeySize {'
      to   = '	if false { // REDCHECK: a raw trusted key was decided as text'
      pkg  = './internal/store'
      test = 'TestARawTrustedKeyIsReadByItsBytesAndNotByItsText'
      why  = 'one trusted key in twenty cannot be read at all, and which ones fail depends on the key''s own randomness. A seal verified against a key the module refused to load is a ledger nobody can check, and the operator is told their file is not a key'
    },
    # --- the benchmark's two kinds of zero ------------------------------------
    #
    # Found by the live demo on 2026-10-15, and worth the paragraph because the
    # symptom was the module's most honest output: `pocctl bench` printed
    # `negative frame, nothing drawn, nothing there` for the committed still set,
    # and it was FALSE. `Detect` returns its refusal INSIDE the Answer -- Boxes
    # is empty either way -- so the metrics loop, which looked at len(ans.Boxes),
    # read a frame the decoder refused to decode as a frame where it found
    # nothing.
    #
    # The model on this machine is why nobody noticed: the pinned Model Zoo
    # yolov4n.onnx is a multi-scale ANCHORED head and the in-process decoder
    # reads only fused outputs, so EVERY frame was refused, and the benchmark's
    # negative-frame line made a refusal look like a clean measurement. A
    # false-positive rate over frames nobody decoded is a claim about nothing,
    # phrased as a result.
    @{
      name = 'a refused frame is refused, not counted as a negative one'
      file = 'cmd\pocctl\bench_frames.go'
      from = '	if ans.Refusal != nil {'
      to   = '	if false { // REDCHECK: a refused frame was read as a negative one'
      pkg  = './cmd/pocctl'
      test = 'TestARefusedFrameIsNotANegativeOne'
      why  = 'the benchmark reports a clean measurement over frames the detector could not decode. Zero from a refusal and zero from a search are both the number 0, and only one of them is a measurement'
    }
  )
  Push-Location $PWD
  try {
    foreach ($c in $controls) {
      $full = (Resolve-Path -LiteralPath $c.file).Path
      $original = [System.IO.File]::ReadAllText($full)
      if (-not $original.Contains($c.from)) {
        Skip "$($c.name): the control line is not present in $($c.file) in the form this script expects, so the control could not be removed. NOT passing; not run. Look for: $($c.from)"
        continue
      }
      $mutated = $original.Replace($c.from, $c.to)
      if ($mutated -eq $original) {
        Skip "$($c.name): the mutation produced no change, so the control could not be removed. NOT passing; not run."
        continue
      }
      try {
        [System.IO.File]::WriteAllText($full, $mutated)
        # The same relaxation the sidecar step needs, for the same reason: a
        # control whose removal produces a COMPILE ERROR makes `go test` write to
        # stderr, and on this shell that becomes a terminating NativeCommandError
        # which aborts the whole gate mid-loop. A compile error is a legitimate red
        # result here — the control really is gone — so it must be read as one
        # rather than as a crash.
        $ErrorActionPreference = 'Continue'
        & $go test -count=1 $c.pkg *> $null
        $exit = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($exit -eq 0) {
          Fail "$($c.name): the suite stayed green with the control removed. $($c.why). A test that cannot fail when its subject is gone is not a test."
        } else {
          Pass "$($c.name) removed -> the suite went red, as it must"
        }
      } finally {
# The restore is RETRIED, and the reason is not theoretical.
        #
        # `go test` leaves a compiled test binary holding the source files it read.
        # On Windows that is an open mapping, and a single WriteAllText over a mapped
        # file fails with "the requested operation cannot be performed on a file
        # with a user-mapped section open". The failure then propagates out of the
        # `finally`, the gate aborts, and the file is left MUTATED -- so the next run
        # of the gate is measuring a tree that a control broke.
        #
        # That happened, twice and independently: this gate aborted with a control
        # still applied, and the symptom was a test failing for a reason nobody had
        # written. A red-check harness that can leave the tree broken is worse than
        # one that does not run, because the damage is discovered by the next person
        # rather than by the run that caused it.
        #
        # So: a handful of attempts with a growing gap between them, because the
        # window is the runtime tearing down its own mapping, and then a HARD failure
        # naming the file. It is deliberately NOT swallowed. A restore this gate
        # cannot complete means the remaining controls were never run and the table's
        # count is a lie, so the gate must stop rather than print a green summary
        # over a modified tree.
        $restored = $false
        for ($try = 1; $try -le 12; $try++) {
          try {
            [System.IO.File]::WriteAllText($full, $original)
            $restored = $true
            break
          } catch {
            if ($try -eq 12) {
              Fail "$($c.name): the control was NOT restored. $full is left MUTATED: $($_.Exception.Message)"
            }
            Start-Sleep -Milliseconds (100 * $try)
          }
        }
        if (-not $restored) {
          Fail 'the red check stopped with production code mutated. Do not commit anything from this worktree. `git checkout --` the file named above, then re-run.'
          throw "red check could not restore $($c.file); stopping rather than running further controls against a mutated tree"
        }
      }
    }
    # The output is printed on failure. A red that says only "the suite is red"
    # sends the reader to run the suite by hand to find out which package, and a
    # red with a name in it is the difference between a five-minute investigation
    # and a guess. The whole point of this gate is that its failures are legible.
    $restored = & $go test -count=1 ./... 2>&1
    if ($LASTEXITCODE -ne 0) {
      $restored | Select-String -Pattern '^FAIL|^---|^\s+---|_test\.go:' | ForEach-Object { Write-Host "    $_" }
      Fail 'the suite is red after the controls were restored, so the restore did not work'
    }
    else { Pass 'the suite is green again with the controls restored' }
    # The count is DERIVED from the table, never written into the step title. A
    # title that says "eleven" is a sentence that goes stale the moment someone
    # adds a twelfth control, and a stale count in a gate is worse than no count.
    $ran = @($controls | Where-Object { $_ }).Count
    Pass "$ran control(s) in the table above; the count is this line, not a constant in the step title"
  } finally { Pop-Location }
}

# --- summary ----------------------------------------------------------------
Write-Host ""
Write-Host "== summary" -ForegroundColor Cyan
if ($script:skipped) {
  Write-Host ""
  Write-Host "SKIPPED (not passed):" -ForegroundColor Yellow
  $script:skipped | ForEach-Object { Write-Host "  - $_" -ForegroundColor Yellow }
}
if ($script:failed) {
  Write-Host ""
  Write-Host "FAILED:" -ForegroundColor Red
  $script:failed | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
  Write-Host ""
  Write-Host "The gate is RED. Nothing above is a pass, including the steps that passed." -ForegroundColor Red
  exit 1
}
Write-Host ""
if ($script:skipped) {
  Write-Host "The gate is GREEN WITH SKIPS. Read the skips before treating this as a pass." -ForegroundColor Yellow
  exit 0
}
Write-Host "The gate is GREEN. It ran: toolchain, build, gofmt, vet, module boundary, tests, sidecar tests, decoder parity, benchmark arithmetic, red check." -ForegroundColor Green
Write-Host "If the list above printed SKIPPED lines, this is GREEN WITH SKIPS and the skips are part of the result." -ForegroundColor Green
exit 0
