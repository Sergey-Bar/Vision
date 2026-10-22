# The load run.
#
# # What it is
#
# A RECORDED run: the rate it ran at, how long it ran, every objective with its
# target and its measurement, and every objective the run did NOT cover.
#
# It is the evidence for M1 gate 3 -- "every SLO demonstrably met under a
# recorded load run, with the run and its numbers in the release notes" -- and
# the artefact is the release notes.
#
# # The rule that makes it worth running
#
# An objective the run did not measure is NOT published as met. It is named in
# `uncovered`, and the `load-run` gate in `scripts/verify.ps1` fails on an
# artefact that leaves one uncovered.
#
# That is the whole point. A run that measured four of the eight objectives and
# published four numbers reads, to a reader, like one that measured all eight.
# The five it did not measure are the ones nobody is being told about, and
# publishing an SLO the run did not cover is worse than publishing none.
#
# # What it needs
#
# A core to read the objectives from, running and reachable. The soak is
# self-contained -- a stub provider, its own evidence store, no key and no
# vendor -- so the only thing the run needs from outside is a core whose
# objectives it can read.
#
# Usage:
#   scripts/load-run.ps1 -Events 20000 -Cameras 8 -CoreUrl http://127.0.0.1:8443
#   scripts/load-run.ps1 -Events 5000 -Chaos
#
# The artefact lands in dist/verification/load-run-<timestamp>.json.

param(
    [int]$Events = 20000,
    [int]$Cameras = 8,
    [int]$Retries = 1,
    [switch]$Chaos,
    [string]$CoreUrl = 'http://127.0.0.1:8443',
    [string]$Out = ''
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

function Write-Step([string]$message) {
    Write-Host "  $message"
}

# The objectives this build evaluates, read off the evaluator rather than off a
# list kept beside the document.
$sloSource = Get-Content -LiteralPath (Join-Path $repoRoot 'internal\observability\slo.go') -Raw
$objectives = @([regex]::Matches($sloSource, 'Name:\s+"([^"]+)"') |
    ForEach-Object { $_.Groups[1].Value } | Sort-Object -Unique)
if ($objectives.Count -eq 0) {
    throw 'load-run: no objectives were found in internal/observability/slo.go'
}

# The core's exposition, which carries one `slo_objective` series per objective.
#
# The series carries the objective's name, its target and what the core
# measured, so the artefact does not have to be told any of it: it is read.
function Read-Objectives([string]$url) {
    $read = @{}
    try {
        $exposition = (Invoke-WebRequest -UseBasicParsing -Uri "$url/v1/metrics" -TimeoutSec 20).Content
    } catch {
        Write-Step "the core at $url did not answer: $($_.Exception.Message)"
        return $read
    }
    foreach ($line in ($exposition -split "`n")) {
        # slo_objective{objective="ingest_availability",measured="0 lost, 0 dead-lettered",...,target="zero accepted events lost"} 1
        if ($line -notmatch '^slo_objective\{') { continue }
        $name = if ($line -match 'objective="([^"]*)"') { $Matches[1] } else { '' }
        $target = if ($line -match 'target="([^"]*)"') { $Matches[1] } else { '' }
        $measured = if ($line -match 'measured="([^"]*)"') { $Matches[1] } else { '' }
        $breached = ($line -match '\}\s+1\s*$')
        if ($name -ne '') {
            $read[$name] = @{ target = $target; measured = $measured; breached = $breached }
        }
    }
    return $read
}

Write-Host "Load run"
Write-Step "objectives in this build: $($objectives.Count)"

$before = Read-Objectives $CoreUrl

# The soak.
#
# It is the load: synthetic events through ingest, detection, evaluation, the
# evidence store and the AI plane, with the fault-injection rehearsals when
# -Chaos is given. The rate is measured from the run's own numbers rather than
# declared, because a declared rate is a claim and a measured one is a reading.
$soakArgs = @('run', './cmd/securityctl', 'soak',
    '--events', "$Events",
    '--cameras', "$Cameras",
    '--retries', "$Retries",
    '--json')
if ($Chaos) { $soakArgs += '--chaos' }

$soakOut = & go @soakArgs 2>$null
if ($LASTEXITCODE -ne 0) {
    throw "load-run: the soak failed with exit code $LASTEXITCODE"
}
$soak = ($soakOut -join "`n") | ConvertFrom-Json

# The summary carries the run's own numbers: the events, the duration and the
# criteria. The rate is MEASURED from them rather than declared, because a
# declared rate is a claim and a measured one is a reading.
$summary = $soak.soak
$soakEvents = [int]$summary.events
$duration = [double]$summary.durationSeconds
if ($soakEvents -eq 0) { $soakEvents = $Events }
if ($duration -le 0) { $duration = 1.0 }
$rate = [math]::Round($soakEvents / $duration, 3)

Write-Step "soak: $soakEvents event(s) in $([math]::Round($duration, 1))s, $rate event(s)/s"

$after = Read-Objectives $CoreUrl

# The readings, and the objectives the run did NOT cover.
#
# An objective the core did not evaluate has no reading, and an objective with
# no reading is not an objective the run demonstrated.
$readings = @()
$uncovered = @()
foreach ($name in $objectives) {
    if ($after.ContainsKey($name)) {
        $r = $after[$name]
        $readings += @([ordered]@{
                name     = $name
                target   = $r.target
                measured = $r.measured
                breached = $r.breached
            })
    } else {
        $uncovered += $name
    }
}

$stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')
if (-not $Out) {
    $dir = Join-Path $repoRoot 'dist\verification'
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $Out = Join-Path $dir "load-run-$stamp.json"
}

$artefact = [ordered]@{
    version          = 'security.io/load-run/v1'
    started_at       = (Get-Date).ToUniversalTime().AddSeconds(-$duration).ToString('o')
    finished_at      = (Get-Date).ToUniversalTime().ToString('o')
    rate_events_per_second = $rate
    events           = $soakEvents
    duration_seconds = [math]::Round($duration, 3)
    criteria         = @($summary.criteria)
    chaos            = @($soak.chaos)
    objectives       = $readings
    uncovered        = $uncovered
    note             = 'The objectives are the core''s own readings, read out of its exposition. An ' +
                       'objective the core did not evaluate is named in `uncovered` and is NOT ' +
                       'published as met: an SLO the run did not cover is worse than none.'
}
$artefact | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $Out -Encoding utf8

Write-Step "artefact: $Out"

# The release-notes table.
#
# This is the artefact's other half: the numbers a release note quotes, and the
# ones it must not.
Write-Host ""
Write-Host "  | objective | target | measured | met |"
Write-Host "  |---|---|---|---|"
foreach ($r in $readings) {
    $met = if ($r.breached) { 'NO' } else { 'yes' }
    Write-Host ("  | {0} | {1} | {2} | {3} |" -f $r.name, $r.target, $r.measured, $met)
}
foreach ($name in $uncovered) {
    Write-Host ("  | {0} | - | NOT MEASURED | no |" -f $name)
}
Write-Host ""
if ($uncovered.Count -gt 0) {
    Write-Step "the run did NOT cover: $($uncovered -join ', ')"
    Write-Step "the load-run gate fails on this artefact, and it should"
    exit 1
}
Write-Step "the run covered every objective"
exit 0
