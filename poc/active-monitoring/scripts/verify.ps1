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
    8. red-check    the same suite with three controls removed, each of which
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

$ffmpeg = Get-Command ffmpeg -ErrorAction SilentlyContinue
if ($ffmpeg) { Pass "ffmpeg at $($ffmpeg.Source)" }
else { Fail "ffmpeg is not on PATH. D2 makes it a hard runtime dependency: without it there is no source, no recording and no frames. The tests that need it FAIL rather than skip, on purpose." }

$ffprobe = Get-Command ffprobe -ErrorAction SilentlyContinue
if ($ffprobe) { Pass "ffprobe at $($ffprobe.Source)" }
else { Fail "ffprobe is not on PATH; the capability table cannot measure the source and preflight has no bit rate to compute a disk budget from." }

$py = Get-Command py -ErrorAction SilentlyContinue
if ($py) { Pass "py at $($py.Source)" }
else { $py = Get-Command python -ErrorAction SilentlyContinue }
if ($py) { Pass "python at $($py.Source)" }
else { Fail "no Python interpreter was found. The AI sidecar needs numpy and onnxruntime and is a hard dependency (D2)." }

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
  if ($py.Name -eq 'py.exe') { & $py -3 -m unittest discover -s pyworker -p 'test_*.py' }
  else { & $py -m unittest discover -s pyworker -p 'test_*.py' }
  if ($LASTEXITCODE -ne 0) { Fail "the sidecar's Python tests exited $LASTEXITCODE" } else { Pass "pyworker tests" }
} else { Skip "no Python interpreter, so the sidecar's tests did not run" }

# --- 8. red check -----------------------------------------------------------
Step "red check (three controls removed from the CODE, each must turn the suite red)"

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
        & $go test -count=1 $c.pkg *> $null
        if ($LASTEXITCODE -eq 0) {
          Fail "$($c.name): the suite stayed green with the control removed. $($c.why). A test that cannot fail when its subject is gone is not a test."
        } else {
          Pass "$($c.name) removed -> the suite went red, as it must"
        }
      } finally {
        [System.IO.File]::WriteAllText($full, $original)
      }
    }
    & $go test -count=1 ./... *> $null
    if ($LASTEXITCODE -ne 0) { Fail 'the suite is red after the controls were restored, so the restore did not work' }
    else { Pass 'the suite is green again with the controls restored' }
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
Write-Host "The gate is GREEN. It ran: toolchain, build, gofmt, vet, module boundary, tests, sidecar tests, red check." -ForegroundColor Green
exit 0
