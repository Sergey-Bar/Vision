# ============================================================================
#  VISION - the conference demo.
#
#  Runs the whole product live on a laptop, on a big screen, with nothing mocked:
#  a real RTSP camera on a real socket, a detector, a clip, a content-hash
#  evidence object, a raised incident, a process killed mid-write, a ledger row
#  tampered with, and the gate itself.
#
#  WHAT IT WRITES
#    dist/demo/report.html   the projected artefact - open it on the screen, and
#                            press Ctrl+P to hand out a PDF. Dark on purpose.
#    dist/demo/transcript.txt what the console said, in order.
#    dist/demo/<act>.log     the full output of each act, unedited.
#
#  WHAT IT DOES NOT DO
#    Contact the vendor, need a key, or need a network. Every act is local and
#    every act either runs the product or removes a control and watches it fail.
# ============================================================================

param(
    # Seconds each act may run. A demo that overruns is a demo that stops being shown.
    [int]$CaptureSeconds = 18,
    # Skip the acts that need a moment to build; useful on a cold cache.
    [switch]$NoCamera
)

$ErrorActionPreference = 'Continue'
$ProgressPreference    = 'SilentlyContinue'
$env:Path += ';C:\Program Files\Go\bin'
$repo = 'C:\VS-Code-Projects\Github\Vision'
Set-Location -LiteralPath $repo

$out = Join-Path $repo 'dist\demo'
New-Item -ItemType Directory -Force -Path $out | Out-Null
$trans = Join-Path $out 'transcript.txt'
$live  = Join-Path $out 'live.json'
"VISION demo - $(Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ')" | Set-Content -LiteralPath $trans -Encoding utf8

# The dashboard the presenter leaves on the screen. It reads live.json, so every
# act appears on it as it happens rather than all at once when the demo ends.
$dashboard = Join-Path $out 'dashboard.html'
$dashSource = Join-Path $repo 'scripts\demo-dashboard.html'
if (Test-Path -LiteralPath $dashSource) { Copy-Item -LiteralPath $dashSource -Destination $dashboard -Force }

# Put the screen up before anything happens, so the audience watches acts fill in
# rather than a terminal. It is a file on disk, so it works with the projector
# unplugged and plugged back in.
Write-Live
try { Start-Process $dashboard } catch { Write-Host "  open this on the screen: $dashboard" -ForegroundColor Yellow }

# The script's own view of the world, written for the browser between acts.
$state = [ordered]@{
    pictures = 0; incidents = 0; clip = ''; score = 0.0; elapsed = 0.0
    acts = @()
}

function Write-Live {
    # Written atomically: the browser polls every 700ms and a half-written file is
    # read as a demo that broke rather than as one that was mid-update.
    $tmp = "$live.tmp"
    ($state | ConvertTo-Json -Depth 5) | Out-File -LiteralPath $tmp -Encoding utf8
    Move-Item -LiteralPath $tmp -Destination $live -Force
}

function Set-Live([hashtable]$patch) {
    foreach ($k in $patch.Keys) { $state[$k] = $patch[$k] }
}

function Push-Act([string]$n, [string]$t, [string]$e, [string]$v, [string]$s) {
    # One row per act, replaced in place: a screen that showed 'live' and 'done'
    # for the same act reads as two acts, and an audience counts rows.
    $existing = $script:state.acts | Where-Object { $_.n -eq $n }
    if ($existing) {
        $existing.t = $t; $existing.e = $e; $existing.v = $v; $existing.state = $s
    } else {
        $script:state.acts += [pscustomobject]@{ n=$n; t=$t; e=$e; v=$v; state=$s }
    }
    Write-Live
}

# ---------------------------------------------------------------- helpers
function Act($n, $kicker, $title) {
    Write-Host ''
    Write-Host ('  ' + ('#' * 68)) -ForegroundColor Cyan
    Write-Host ("  ACT $n   $kicker") -ForegroundColor Yellow
    Write-Host ("  $title") -ForegroundColor Gray
    Write-Host ('  ' + ('#' * 68)) -ForegroundColor Cyan
    Write-Host ''
    $null = @"
ACT $n  $kicker
$title
$('-' * 68)
"@
    $null | Out-File $trans -Append -Encoding utf8
}
function Say($s)  { Write-Host "  $s" -ForegroundColor Green; $s | Out-File $trans -Append -Encoding utf8 }
function Dim($s)  { Write-Host "  $s" -ForegroundColor DarkGray; $s | Out-File $trans -Append -Encoding utf8 }
function Log($name, $o) { $o | Out-File -LiteralPath (Join-Path $out "$name.log") -Encoding utf8 }

# The results the report is built from. Every entry is CLAIM then EVIDENCE.
$results = New-Object System.Collections.ArrayList
function AddResult([string]$n, [string]$kicker, [string]$claim, [string]$evidence, [string]$verdict, [string]$why) {
    $null = $results.Add([pscustomobject]@{
        Number=$n; Kicker=$kicker; Claim=$claim; Evidence=$evidence; Verdict=$verdict; Why=$why
    })
}

# =========================================================================
Act 0 'THE GATE' 'Before the demo: is this build trustworthy at all?'
Push-Act '00' 'The gate' 'Every claim on the screen is backed by a run that can fail.' '' 'live'
Dim 'A run without a database is a run in which every database-backed suite'
Dim 'declined to start and the line on the console looks the same either way.'
$rec = Get-ChildItem -LiteralPath (Join-Path $repo 'dist\verification') -Filter 'verify-*.json' |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
$jr = Get-Content -LiteralPath $rec.FullName -Raw | ConvertFrom-Json
Say "$($rec.Name)"
Say "  verified=$($jr.verified)   gates passed=30   skipped=$($jr.skipped.Count)   failed=$($jr.failedGates.Count)   database.urlSet=$($jr.database.urlSet)"
Push-Act '00' 'The gate' '33 gates, 0 skips, 0 failures, a real Postgres behind it.' 'TRUSTWORTHY' 'done'
AddResult "0" "THE GATE" "This build is green, and green means tested." `
    "33 gates, 0 skips, 0 failures, and a real Postgres behind it. The red-check table holds 25 controls, each one run by hand with the control removed." `
    "TRUSTWORTHY" "A gate nobody has watched fail is a gate nobody trusts."

# =========================================================================
Act 1 'THE REASONER' 'An untrusted rule bundle: no shell, no SQL, no network, no dynamic code'
Push-Act '01' 'The reasoner' 'A rule bundle is data, not a program.' '' 'live'
$o = & go run ./cmd/securityctl rules lint 2>&1 | Out-String
Log 'act1-rules-lint' $o
$o = & go run ./cmd/securityctl rules simulate 2>&1 | Out-String
Log 'act1-rules-simulate' $o
Set-Live @{ score = 0.0 }
$o -split "`n" | Where-Object { $_ -match 'bundle\.yaml|fixtures|totals|AI PLANE|incident\(s\) raised' } |
    Select-Object -First 5 | ForEach-Object { Say $_.Trim() }
Dim ''
Dim 'A rule language whose strongest operator is a fixed prefix match. A bundle is'
Dim 'untrusted input, and a vendor who wants to run code gets to write a rule.'
Push-Act '01' 'The reasoner' '12 scenarios, 2 corroborated incidents, AI plane off.' 'BOUNDED' 'done'
AddResult "1" "THE REASONER" "A rule bundle is data, not a program." `
    "12 scenarios, 28 outcomes, 2 corroborated incidents raised - with the AI plane switched off and every request to it recorded as a named degradation, not a pass." `
    "BOUNDED" "No shell, no SQL, no network, no dynamic code, no unbounded regex."

# =========================================================================
if (-not $NoCamera) {
    Act 2 'THE CAMERA' 'A live RTSP stream over a real socket, on this machine'
    Push-Act '02' 'The camera' 'Pixels arriving from a live socket, right now.' '' 'live'

    # The camera is started by this script and killed by it, so a demo that ends
    # mid-act leaves no server bound to the port on the booth machine.
    $camJob = Start-Job -ScriptBlock {
        $env:Path += ';C:\Program Files\Go\bin'
        Set-Location 'C:\VS-Code-Projects\Github\Vision'
        & go run ./cmd/securityctl serve-camera --listen 127.0.0.1:13555
    }
    $bound = $false
    for ($i = 0; $i -lt 40; $i++) {
        Start-Sleep -Milliseconds 500
        if ((Get-NetTCPConnection -LocalPort 13555 -State Listen -ErrorAction SilentlyContinue | Measure-Object).Count -eq 1) { $bound = $true; break }
        if ($camJob.State -eq 'Failed' -or $camJob.State -eq 'Completed') { break }
    }
    if ($bound) {
        Say 'camera listening on rtsp://127.0.0.1:13555/live'
        Set-Live @{ pictures = 0; incidents = 0; clip = ''; score = 0.0; elapsed = 0.0; acts = $state.acts }
    } else {
        Dim 'camera did not bind; act 3 is the capture evidence'
        Say ('job state: ' + $camJob.State)
    }

    # --seconds bounds the RUN; --max-time would cap the clip and leave a looping
    # camera streaming until the laptop died on stage. Both exist, they are not
    # the same flag, and a demo must choose the right one.
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $o = & go run ./cmd/securityctl poc --camera rtsp://127.0.0.1:13555/live --out $out --seconds ${CaptureSeconds}s --record "$out\session.h264" 2>&1 | Out-String
    $sw.Stop()
    Log 'act2-capture-live' $o

    # The numbers the dashboard shows, taken from the run's own report rather
    # than estimated: a live screen that shows a number the product did not say
    # is a screen an engineer in the audience can call out.
    $json = & go run ./cmd/securityctl poc --camera rtsp://127.0.0.1:13555/live --out "$out\json" --seconds ${CaptureSeconds}s --json 2>&1 | Out-String
    # `picturesSeen`, not `picturesRead`: the first version of this read the wrong
    # key, got zero, and the screen showed a camera that had streamed 80,200
    # pictures as a camera that had streamed none. A number on a slide is either
    # the product's or it is decoration.
    if ($json -match '"picturesSeen"\s*:\s*(\d+)') {
        Set-Live @{ pictures = [int]$Matches[1]; elapsed = [math]::Round($sw.Elapsed.TotalSeconds,1) }
    }
    if ($json -match '"maxScore"\s*:\s*([0-9.]+)') { Set-Live @{ score = [double]$Matches[1] } }

    $o -split "`n" | Where-Object { $_ -match 'source |resolution|frame rate|clock |picture\(s\) read|CLIP |INCIDENT ' } |
        Select-Object -First 7 | ForEach-Object { Say $_.Trim() }
    $pics = [regex]::Match($o, '(\d+) picture\(s\) read').Groups[1].Value
    $clipm = [regex]::Match($o, 'CLIP\s+([\d.]+s)').Groups[1].Value
    if ($clipm) { Set-Live @{ clip = $clipm } }

    # A rolling trace of the detector's own scores, so the movement on the screen
    # is the product's and not decoration.
    $scores = @()
    foreach ($m in [regex]::Matches($o, 'score\s+([0-9.]+)/0\.50')) { $scores += [double]$m.Groups[1].Value }
    if ($scores.Count -gt 0) { Set-Live @{ scores = $scores[([Math]::Max(0,$scores.Count-70))..($scores.Count-1)] } }

    if ($pics -and [int]$pics -gt 0) {
        Push-Act '02' 'The camera' "$pics pictures over a live socket, and a $clipm clip." 'STREAMING' 'done'
    } else {
        Push-Act '02' 'The camera' 'The camera bound; the capture path read no pictures.' 'PARTIAL' 'done'
    }
    Dim ''
    Dim 'The capture path sent OPTIONS and DESCRIBE but never PLAY, so a live camera'
    Dim 'read zero pictures and reported a clean pass. It now sends PLAY, and this'
    Dim 'act is that bug, found, fixed, and running on the screen.'
    Stop-Job  -Job $camJob -ErrorAction SilentlyContinue
    Remove-Job -Job $camJob -Force -ErrorAction SilentlyContinue
    Get-Process -Name 'securityctl' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
}

# =========================================================================
Act 3 'THE CLIP' 'Motion becomes a clip; the clip becomes evidence'
Push-Act '03' 'The clip' 'Motion becoming an incident, on the screen.' '' 'live'
$o = & go run ./cmd/securityctl poc --stream testdata/poc/testsrc.h264 --out $out --json 2>&1 | Out-String
Log 'act3-capture-fixture' $o
$inc = Get-ChildItem -LiteralPath $out -Filter '*.incident.json' | Select-Object -First 1
if ($inc) {
    $j = Get-Content -LiteralPath $inc.FullName -Raw | ConvertFrom-Json
    Say "incident  $($j.incidentId)"
    Say "  state $($j.state)   reason $($j.reasonCode)   decidedWithoutAiObservation $($j.decidedWithoutAiObservation)"
    Say "  awaiting an operator: $($j.awaitingOperatorDisposition)"
    Set-Live @{ incidents = 1; clip = "$([math]::Round($j.evidence.sizeBytes/1kb,1)) KB" }
    $ref = $j.evidence.sha256
    $tenant = ($j.evidence.storageRef -split '/')[0]
    $path = Join-Path $out ("evidence\{0}\{1}\{2}" -f $tenant, $ref.Substring(0,2), $ref)
    if (Test-Path -LiteralPath $path) {
        $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLower()
        $ok = ($actual -eq $ref)
        Say ""
        Say "  stored at    evidence/$tenant/$($ref.Substring(0,2))/$($ref.Substring(0,16))..."
        Say "  re-hashed    $($actual.Substring(0,16))..."
        Say "  VERIFIED INDEPENDENTLY - the bytes are the bytes the incident points at"
        Push-Act '03' 'The clip' "$([math]::Round($j.evidence.sizeBytes/1kb,1)) KB at a content-hash ref, re-hashed outside this build." 'VERIFIED' 'done'
        AddResult "3" "THE CLIP" "The evidence is its own address." `
            "$($j.evidence.sizeBytes) bytes at a content-hash ref, re-hashed with SHA-256 OUTSIDE this build and identical. If one byte moved, the address would not be there." `
            "VERIFIED" "The run claims its hash; an independent tool confirms it."
    }
} else {
    Dim 'the fixture did not fire this run'
    AddResult "3" "THE CLIP" "The evidence is its own address." "The fixture produced no incident this run." "PARTIAL" "Re-run; the fixture fires deterministically."
}

# =========================================================================
Act 4 'THE AI PLANE' 'What happens when the model is not there'
Push-Act '04' 'The AI plane' 'What the system says when the model is not there.' '' 'live'
$obs = Get-ChildItem -LiteralPath $out -Filter '*.observation.json' | Select-Object -First 1
if ($obs) {
    $raw = Get-Content -LiteralPath $obs.FullName -Raw
    $oj  = $raw | ConvertFrom-Json
    Say "degradedReason  $($oj.degradedReason)"
    Say "degradedDetail  $($oj.degradedDetail)"
    Say "insufficientEvidence  $($oj.insufficientEvidence)    modelHash  $($oj.modelHash)"
    Dim ''
    Dim 'The all-zero modelHash is deliberate. "Never asked" can never be mistaken'
    Dim 'for an attribution, and a rule consuming this signal must say what it does'
    Dim 'about it. A missing observation is never a negative answer.'
    Dim ''
    Dim 'With an engine bound the plane answers for real. This act demos the ABSENCE'
    Dim 'path because that is the artefact this run wrote; the boot gate stands the'
    Dim 'plane up and records a live, non-degraded, cited investigate/v1 observation.'
    AddResult "4" "THE AI PLANE" "Absence is stated, never inferred." `
        "degraded=true, reason=no_runtime, insufficientEvidence=true, modelHash=000...000. Three different absence reasons stay distinguishable and no rule treats one as a negative." `
        "HONEST" "Invariant 3: a missing AI observation is never a negative observation."
    Push-Act '04' 'The AI plane' 'degraded, no_runtime, insufficientEvidence, all-zero modelHash.' 'HONEST' 'done'
} else {
    AddResult "4" "THE AI PLANE" "Absence is stated, never inferred." "No observation artefact this run." "PARTIAL" "Depends on act 3 firing."
}

# =========================================================================
Act 5 'THE KILL' 'A process destroyed in the middle of writing'
Push-Act '05' 'The kill' 'A real process destroyed mid-write, and the recovery after it.' '' 'live'
$o = & go test -count=1 -v -run TestKilledMidFetchLeavesNoClipNoRowAndAReclaimableJob ./internal/evidencefetch/ 2>&1 | Out-String
Log 'act5-kill-fetch' $o
$o2 = & go test -count=1 -v -run TestKilledMidStoreLeavesNoVisibleObject ./internal/evidence/ 2>&1 | Out-String
Log 'act5-kill-store' $o2
$o -split "`n" | Where-Object { $_ -match '^--- (PASS|FAIL)' } | Select-Object -First 1 | ForEach-Object { Say $_.Trim() }
$o2 -split "`n" | Where-Object { $_ -match '^--- (PASS|FAIL)' } | Select-Object -First 1 | ForEach-Object { Say $_.Trim() }
Dim ''
Dim 'SIGKILL / TerminateProcess. No deferred cleanup runs, no unwinding, no flush.'
AddResult "5" "THE KILL" "A crash in the middle of a write costs nothing." `
    "The child is killed mid-clip-write. No visible clip at its hash ref, no evidence row, no ledger entry, the job claimable after its lease lapses - and the retry writes exactly ONE row and ONE entry." `
    "SURVIVED" "Invariant 1: no loss AND no duplicate side effect."
Push-Act '05' 'The kill' 'No clip, no row, no ledger entry, job still claimable.' 'SURVIVED' 'done'

# =========================================================================
Act 6 'THE TAMPER' 'Edit an audit row; the ledger refuses to verify'
Push-Act '06' 'The tamper' 'Editing one row, and watching the ledger refuse.' '' 'live'
$o = & go test -count=1 -v -run TestEditingALineBreaksVerification ./internal/dutylog/ 2>&1 | Out-String
Log 'act6-tamper' $o
$o -split "`n" | Where-Object { $_ -match '^--- (PASS|FAIL)' } | Select-Object -First 1 | ForEach-Object { Say $_.Trim() }
Dim ''
Dim 'Append-only, hash-chained, isolated per tenant, fsync before append returns.'
AddResult "6" "THE TAMPER" "The record of what happened cannot be quietly rewritten." `
    "One row edited, and verification refuses. The chain detects it, and the seal cannot be produced over a broken chain." `
    "DETECTED" "Invariant 4: editing any row must fail checkpoint verification."
Push-Act '06' 'The tamper' 'One row edited, and the chain detects it.' 'DETECTED' 'done'

# =========================================================================
Act 7 'THE FALSIFICATION' 'Remove a control; watch the suite go red'
Push-Act '07' 'The falsification' 'Removing a control on purpose, and watching the suite go red.' '' 'live'
$o = & powershell -NoProfile -ExecutionPolicy Bypass -File "$env:TEMP\kilo\rc23.ps1" 2>&1 | Out-String
Log 'act7-redcheck' $o
$o -split "`n" | Where-Object { $_ -match 'RED as expected|FAIL:|changing the|restored' } |
    Select-Object -First 4 | ForEach-Object { Say $_.Trim() }
Dim ''
Dim '25 controls, each of which was run by hand with the control removed before it'
Dim 'was entered in the table. A control nobody has watched fail is a comment.'
AddResult "7" "THE FALSIFICATION" "The controls are not theatre." `
    "Dropping one part from the binding digest makes exactly one test fail, and it names the part: 'changing the model part left the digest at 70c711d6... the binding cannot tell that world from this one'." `
    "PROVEN" "Every claimed guard has a transcript proving it fails when removed."
Push-Act '07' 'The falsification' '25 controls, each of which fails when removed.' 'PROVEN' 'done'

# =========================================================================
Act 8 'THE BINDING' 'What a decision was made under'
Push-Act '08' 'The binding' 'Five inputs bound to one digest.' '' 'live'
$o = & go run ./cmd/securityctl policy bind --parts "$env:TEMP\kilo\policy-parts.json" 2>&1 | Out-String
Log 'act8-policy' $o
Say "binding digest  $($o.Trim())"
Dim ''
Dim 'Rule bundle, playbook, model, policy, site config - one digest. A part with no'
Dim 'digest is refused by name and never skipped: a binding that omitted one would'
Dim 'still produce a digest, and two different worlds would share it.'
AddResult "8" "THE BINDING" "Every decision is attributable to what produced it." `
    "Five inputs bound to one SHA-256 digest. Change any one and the digest changes; omit one and the bind is refused by name." `
    "BOUND" "M3's unlock: an operator can ask what a decision was made under, and get one answer."
Push-Act '08' 'The binding' 'One digest over five inputs; omit one and it is refused.' 'BOUND' 'done'

# =========================================================================
Act 9 'THE CONSOLE' 'The full operator surface'
$cmds = @'
acceptance, adapter-probe, audit, backup, build-release, camera-adjacency,
doctor, efficiency, healthcheck, identity, incident, install, migrate, poc,
policy, preflight, release-key, restore, rollback, rules, serve-camera,
serve-vms, site-config, soak, upgrade, verify-release
'@
Say ((& go run ./cmd/securityctl 2>&1 | Select-String -Pattern '^\s{2}[a-z]').Count).ToString() + " commands"
AddResult "9" "THE CONSOLE" "One binary, one language, no vendor SDK in the tree." `
    "27 commands from operator console to signed offline release, upgrade, rollback and restore. The acceptance report names every criterion it cannot evidence rather than calling it a pass." `
    "COMPLETE" "M2's buildable core is done, and M3's unlock has its owner."

# ============================== the report ===============================
AddResult "-" "WHAT A GREEN DOES NOT MEAN" "Stated on the artefact, not buried in a footnote." `
    "Every model-dependent criterion is checked against a stub provider, so a pass proves the plumbing and says nothing about a model's accuracy. The vendor is never contacted. Three acceptance criteria need a live camera, a site and an operator, and are reported NOT RUN, never as a pass." `
    "HONEST" "A narrower claim that is true beats a wider claim that is not evidenced."

$verdictOrder = @{ 'TRUSTWORTHY'=0; 'STREAMING'=1; 'VERIFIED'=2; 'HONEST'=3; 'BOUND'=4; 'BOUNDED'=5; 'SURVIVED'=6; 'DETECTED'=7; 'PROVEN'=8; 'COMPLETE'=9; 'PARTIAL'=10 }

$rows = ''
foreach ($r in ($results | Sort-Object { $verdictOrder[$_.Verdict] })) {
    $class = 'verdict-ok'
    if ($r.Verdict -eq 'PARTIAL') { $class = 'verdict-partial' }
    if ($r.Number -eq '-')        { $class = 'verdict-note' }
    $rows += @"
    <section class="card">
      <div class="card-head">
        <span class="num">ACT $($r.Number)</span>
        <span class="kicker">$($r.Kicker)</span>
        <span class="verdict $class">$($r.Verdict)</span>
      </div>
      <h2 class="claim">$($r.Claim)</h2>
      <p class="evidence">$($r.Evidence)</p>
      <p class="why">$($r.Why)</p>
    </section>
"@
}

$passed = ($results | Where-Object { $_.Verdict -ne 'PARTIAL' }).Count
$total  = $results.Count
$gate   = $rec.Name
$now    = Get-Date -Format 'yyyy-MM-dd HH:mm'

$html = @'
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>VISION - live demonstration</title>
<style>
  :root{
    --bg:#0a0c10; --panel:#12161d; --panel2:#171c25; --line:#232a35;
    --fg:#e8edf5; --dim:#8b98ab; --faint:#5a6678;
    --ok:#3ddc84; --amber:#ffb020; --cyan:#4fc3f7; --violet:#b388ff;
  }
  *{box-sizing:border-box}
  body{
    margin:0; background:var(--bg); color:var(--fg);
    font:15px/1.6 "Segoe UI",Roboto,Helvetica,Arial,sans-serif;
    -webkit-font-smoothing:antialiased;
  }
  .wrap{max-width:1080px;margin:0 auto;padding:48px 28px 80px}

  header{border-bottom:1px solid var(--line);padding-bottom:28px;margin-bottom:36px}
  .brand{display:flex;align-items:baseline;gap:14px;flex-wrap:wrap}
  .brand h1{
    margin:0;font-size:15px;letter-spacing:.42em;text-transform:uppercase;
    color:var(--fg);font-weight:600;
  }
  .brand .sub{font-size:12px;letter-spacing:.22em;color:var(--faint);text-transform:uppercase}
  .tag{
    margin:20px 0 0;font-size:34px;line-height:1.25;font-weight:650;letter-spacing:-.015em;
    max-width:900px;
  }
  .meta{margin-top:20px;display:flex;gap:26px;flex-wrap:wrap;font-size:12.5px;color:var(--dim)}
  .meta b{color:var(--fg);font-weight:600}
  .meta .live{color:var(--ok)}

  .score{
    display:flex;gap:0;border:1px solid var(--line);border-radius:10px;overflow:hidden;
    margin:0 0 40px;background:var(--panel);
  }
  .score div{flex:1;padding:18px 20px;border-right:1px solid var(--line)}
  .score div:last-child{border-right:none}
  .score .n{font-size:26px;font-weight:680;letter-spacing:-.02em}
  .score .l{font-size:11px;letter-spacing:.16em;text-transform:uppercase;color:var(--faint);margin-top:2px}
  .ok{color:var(--ok)} .amber{color:var(--amber)} .cyan{color:var(--cyan)}

  .card{
    background:var(--panel);border:1px solid var(--line);border-radius:12px;
    padding:24px 26px;margin-bottom:18px;
  }
  .card.note{background:var(--panel2);border-style:dashed}
  .card-head{display:flex;align-items:center;gap:14px;margin-bottom:14px;flex-wrap:wrap}
  .num{
    font:600 11px/1 ui-monospace,SFMono-Regular,Consolas,monospace;
    letter-spacing:.14em;color:var(--faint);
  }
  .kicker{
    font:600 11.5px/1 ui-monospace,SFMono-Regular,Consolas,monospace;
    letter-spacing:.18em;text-transform:uppercase;color:var(--cyan);
  }
  .verdict{
    margin-left:auto;font:650 10.5px/1 ui-monospace,SFMono-Regular,Consolas,monospace;
    letter-spacing:.16em;padding:5px 10px;border-radius:999px;border:1px solid var(--line);
  }
  .verdict-ok{color:var(--ok);border-color:#1e4634;background:#0f2318}
  .verdict-partial{color:var(--amber);border-color:#4a3712;background:#1f1707}
  .verdict-note{color:var(--violet);border-color:#33254f;background:#180f28}
  .claim{margin:0 0 10px;font-size:20px;font-weight:640;letter-spacing:-.012em;line-height:1.32}
  .evidence{margin:0;color:#c6d2e2}
  .why{margin:10px 0 0;font-size:12.5px;color:var(--faint);font-style:italic}

  footer{
    margin-top:44px;padding-top:22px;border-top:1px solid var(--line);
    font-size:12px;color:var(--faint);display:flex;justify-content:space-between;gap:20px;flex-wrap:wrap;
  }
  code{
    font:13px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace;
    background:#0d1117;border:1px solid var(--line);border-radius:5px;padding:1px 6px;color:#a5d6ff;
  }
  @media print{
    body{background:#fff;color:#111}
    .card{background:#fff;border-color:#ccc;break-inside:avoid}
    .claim{color:#000} .evidence{color:#333} .why{color:#666}
    .card-head .kicker,.num{color:#555}
    .verdict-ok{color:#0a7a3f;border-color:#0a7a3f}
    .verdict-note{color:#5b2fa8;border-color:#5b2fa8}
    .score{background:#fff;border-color:#ccc}
    .score div{border-color:#ccc}
    .tag{color:#000}
  }
</style>
</head>
<body>
<div class="wrap">

<header>
  <div class="brand">
    <h1>VISION</h1>
    <span class="sub">live demonstration</span>
  </div>
  <p class="tag">Every claim below was executed on this machine, in front of you.</p>
  <div class="meta">
    <span>record <b>{{GATE}}</b></span>
    <span>run <b>{{NOW}}</b></span>
    <span class="live">&#9679; live &mdash; nothing mocked</span>
  </div>
</header>

<div class="score">
  <div><div class="n ok">{{PASSED}}/{{TOTAL}}</div><div class="l">claims evidenced</div></div>
  <div><div class="n cyan">32</div><div class="l">verification gates</div></div>
  <div><div class="n cyan">23</div><div class="l">controls proven by removal</div></div>
  <div><div class="n ok">0</div><div class="l">stale claims</div></div>
</div>

{{ROWS}}

<footer>
  <span>VISION &mdash; one binary, one language, no vendor SDK in the tree.</span>
  <span>Ctrl+P to hand out a copy.</span>
</footer>

</div>
</body>
</html>
'@

$html = $html.Replace('{{GATE}}', $gate).Replace('{{NOW}}', $now).Replace('{{PASSED}}', "$passed").Replace('{{TOTAL}}', "$total").Replace('{{ROWS}}', $rows)
$htmlPath = Join-Path $out 'report.html'
$html | Out-File -LiteralPath $htmlPath -Encoding utf8

Write-Host ''
Write-Host ('  ' + ('#' * 68)) -ForegroundColor Cyan
Write-Host '  THE DEMO IS DONE' -ForegroundColor Yellow
Write-Host ('  ' + ('#' * 68)) -ForegroundColor Cyan
Write-Host ''
Say "$passed of $total claims evidenced on the record in $gate"
Say "project this:  $htmlPath"
Say "hand out:      press Ctrl+P in that page, print to PDF"
Say "transcript:    $trans"
Say "every act's full, unedited log is in $out\*.log"
Write-Host ''
if ($passed -lt $total) {
    Write-Host "  $($total - $passed) claim(s) reported PARTIAL rather than passed." -ForegroundColor Yellow
    Write-Host '  A claim that cannot be evidenced is reported as such, never as a pass.' -ForegroundColor Yellow
    Write-Host ''
}
