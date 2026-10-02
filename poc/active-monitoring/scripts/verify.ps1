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
$platformImport = Get-ChildItem -Recurse -Filter *.go -Path . | Select-String -Pattern 'github\.com/Sergey-Bar/Ai-Captain/'
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
    forbid = @('modernc.org/sqlite', 'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/evidence',
               'github.com/Sergey-Bar/ai-captain-poc/internal/xprotect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/ai')
    why = 'internal/detect must know nothing about storage, the wire or the sidecar'
  },
  @{
    pkg = './internal/ai'
    forbid = @('modernc.org/sqlite', 'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/evidence',
               'github.com/Sergey-Bar/ai-captain-poc/internal/xprotect')
    why = 'internal/ai must know nothing about line-crossing, storage or the wire'
  },
  @{
    pkg = './internal/xprotect'
    forbid = @('github.com/Sergey-Bar/ai-captain-poc/internal/xpsim',
               'modernc.org/sqlite', 'github.com/Sergey-Bar/ai-captain-poc/internal/detect')
    why = 'internal/xprotect must not know the simulator exists, and must know nothing about storage'
  },
  @{
    pkg = './internal/xpsim'
    forbid = @('github.com/Sergey-Bar/ai-captain-poc/internal/evidence',
               'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/xprotect')
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
    forbid = @('github.com/Sergey-Bar/ai-captain-poc/internal/soak',
               'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/ai',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/ui',
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
               'github.com/Sergey-Bar/ai-captain-poc/internal/ai',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/novelty',
               'github.com/Sergey-Bar/ai-captain-poc/internal/soak',
               'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/overlay',
               'github.com/Sergey-Bar/ai-captain-poc/internal/classify',
               'github.com/Sergey-Bar/ai-captain-poc/internal/evidence',
               'github.com/Sergey-Bar/ai-captain-poc/internal/config',
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
               'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/soak',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/evidence',
               'github.com/Sergey-Bar/ai-captain-poc/internal/xprotect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/ui',
               'github.com/Sergey-Bar/ai-captain-poc/internal/classify')
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
               'github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/soak',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/evidence',
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
    forbid = @('github.com/Sergey-Bar/ai-captain-poc/internal/ortdetect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/soak',
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
    forbid = @('github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/soak',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/novelty',
               'modernc.org/sqlite')
    why = 'internal/vlm decides what an operator is told. It must reach no database, no pipeline and no detector: a verifier that can read what somebody said last time is a recorder, and a recorder is exactly what RC-4 forbids.'
  },
  @{
    # The novelty engine is a leaf for the same reason as the overlay: it owns the
    # region and it owns nothing else. A detector that could reach the store could
    # report a decision it did not measure, and a detector that could reach soak
    # would be inside the pipeline it is supposed to be a measurement of.
    pkg = './internal/novelty'
    forbid = @('github.com/Sergey-Bar/ai-captain-poc/internal/store',
               'github.com/Sergey-Bar/ai-captain-poc/internal/soak',
               'github.com/Sergey-Bar/ai-captain-poc/internal/vlm',
               'github.com/Sergey-Bar/ai-captain-poc/internal/detect',
               'github.com/Sergey-Bar/ai-captain-poc/internal/ui',
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
$ortModel = 'models\yolov8n.onnx'
if (-not (Test-Path -LiteralPath $ortModel)) { $ortModel = 'models/yolov8n.onnx' }

if ($pyCmd -and (Test-Path -LiteralPath $ortLib) -and (Test-Path -LiteralPath $ortModel)) {
  & $go test -count=1 -run 'TestCrossDecoderParity' -tags parity ./internal/ortdetect/
  if ($LASTEXITCODE -ne 0) {
    Fail "the Go decoder and pyworker's disagree on the same tensor. Every measurement taken with either is a measurement of their disagreement."
  } else { Pass "the Go decoder and pyworker agree on the same tensor" }
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
      from = 'if t.Seen >= e.cfg.TrackMinFrames {'
      to   = 'if true { // REDCHECK RC-19: a track ages on its FIRST sighting'
      pkg  = './internal/detect'
      why  = 'a box the model approved once would be treated as an object that exists, so a refused box beside it could hold it alive and age it past the minimum. The gate is the AGED ASSIGNMENT and not the check: mutating the check to `if false` makes the recovery pass inert rather than permissive, which a test asserting "nothing was resumed" cannot see. Single-line controls only — PowerShell single quotes do not expand a newline, so a multi-line match string is a control that silently never fires.'
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
        [System.IO.File]::WriteAllText($full, $original)
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
