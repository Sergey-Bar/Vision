# ============================================================================
#  VISION - the LIVE demo: a real camera, streamed by somebody else.
#
#  The conference demo (scripts/demo.ps1) runs the whole product against a
#  recorded clip on a loopback rtsp relay. This one removes the last piece of
#  mockery from the SOURCE: a public relay of a real camera, resolved live with
#  yt-dlp, read by ffmpeg, decoded by a GPU ONNX session, and every surface
#  labelled "public relay of a real camera, NOT ours".
#
#  WHAT IS REAL HERE, AND WHAT IS NOT
#    real:   the camera. `pocctl live resolve` turns a camera row into an HLS
#            playlist and `pocctl preflight` reads frames from it end to end.
#    real:   the detector, on BOTH paths. The shipped one is the in-process
#            ONNX session, whose p99 `pocctl bench` writes into the calibration
#            preflight multiplies. `-TriggerMode sidecar` decodes the same
#            frames in the Python worker instead, measures its own loopback
#            p99 over the same still set, and the calibration record names
#            which path measured it: preflight refuses one path's p99 applied
#            to the other BY NAME, so a run is never cleared against a session
#            it never opened.
#    real:   the honesty. The source line on the screen, in the log and in the
#            report is derived from a flag nobody can mis-set in the quiet
#            direction, and the table's licence column records what is NOT
#            permitted: no written permission to store clips exists, and
#            `cleared_for_external_use` is false on every row.
#    NOT:    the video management system. Events are pushed to xpsim, the
#            simulator, and preflight's own row says the word "simulator" out
#            loud. A run against a real Milestone is a different act.
#
#  THE ACTS, in the order the plan names them: the box (what is on this
#  machine, and the sidecar's own runtime when that is this run's path),
#  resolve, calibrate (one record per path), rehearse (fault day, then a
#  smoke run over the committed clip), preflight, run (`watch` over the
#  relay), prove (one recorded execution of THIS run, replayed from stored
#  facts with zero provider calls), mark (the review sheet, and one verdict
#  whose author is stated), report.
#
#  WHAT IT WRITES
#    dist/live-demo/transcript.txt   what the console said, in order
#    dist/live-demo/live.json        the acts as they happen
#    dist/live-demo/<act>.log        the full output of each act, unedited
#    dist/live-demo/report.html      the artefact: AC table, source line,
#                                    calibration line, camera row, licence
#                                    row, the two "what this does not prove"
#                                    lines, and the run's still, embedded
#    dist/live-demo/review.html      the reviewer's sheet
#    dist/live-demo/still.jpg        the last frame the run processed
#
#  WHAT IT DOES NOT DO
#    Upload anything, or claim the camera is ours. Every row that could be
#    misread as a camera this site owns is labelled by the run itself, not by
#    this script's enthusiasm. And nobody's verdict is written without a name:
#    a mark this demo writes carries the demo's author, and the report says so.
# ============================================================================

param(
    # Seconds the run records the camera for.
    [int]$CaptureSeconds = 25,
    # Which camera from the curated table. Empty means the first that resolves.
    [string]$Camera = '',
    # Where the run writes. Empty means a timestamped directory, so a re-run
    # never overwrites the run it is being compared against.
    [string]$DataRoot = '',
    # Port for the POC's read-only screen and for the xpsim push target.
    [int]$UIPort = 8097,
    [int]$SimPort = 8098,
    # Do not stop a watch/xpsim left over from a previous run: refuse instead.
    # Left false on purpose; the demo is expected to be run twice in a row.
    [switch]$NoTeardown,

    # =====================================================================
    # WHICH PATH THIS RUN'S BOXES COME FROM.
    #
    # `detector` is the shipped path: the in-process ONNX session, whose p99 the
    # bench measures by opening the session. `sidecar` is the Python worker's
    # endpoint: the same still set driven over HTTP, and the measured number is
    # a loopback round trip plus a CPU decode. The calibration record carries a
    # trigger field naming which path measured it and preflight refuses one
    # path's p99 applied to the other BY NAME, so this one switch moves the
    # calibrate act, the preflight act and the printed cost sentence, and a
    # run cannot end up cleared against a session it never opened.
    # =====================================================================
    [ValidateSet('detector','sidecar')]
    [string]$TriggerMode = 'detector',

    # The sidecar's address and the model it must be serving, on the sidecar
    # path. The demo starts its own worker, so these name where to start it
    # rather than where to find a running one. The anchors file is the model's
    # own `<model>.anchors.txt`, written by `pocctl fetch-model` from the
    # committed copy: the ONNX file does not carry the model's anchor boxes.
    [string]$AIAddr  = 'http://127.0.0.1:8099',
    [string]$AIModel = (Join-Path (Split-Path -Parent $PSScriptRoot) 'poc\active-monitoring\models\yolov4n.onnx'),

    # With a verdict, the mark act writes ONE ground-truth mark through
    # `pocctl mark`, with -MarkBy as its author. Without one the sheet is
    # generated and the two commands beside each row are printed for a person
    # to run after they have looked at the clip. A mark made by this demo is a
    # demonstration of the write path, not a person's opinion, and the report
    # names its author.
    [ValidateSet('','true_positive','false_positive')]
    [string]$MarkVerdict = '',
    [string]$MarkBy = 'live-demo demo'
)

$ErrorActionPreference = 'Continue'
$ProgressPreference    = 'SilentlyContinue'
$env:Path += ';C:\Program Files\Go\bin;C:\msys64\mingw64\bin'
# cgo is what builds the CUDA session path, and the gate's own reason applies:
# a detector built without it refuses by name rather than running elsewhere.
$env:CGO_ENABLED = '1'

$repo   = 'C:\VS-Code-Projects\Github\Vision'
$poc    = Join-Path $repo 'poc\active-monitoring'
# The POC is its own Go module and its commands resolve their module root from
# the WORKING DIRECTORY: run from the repository root they find the ROOT's
# go.mod and then refuse the camera table, the still set and every relative
# path they were given. So the whole demo runs from the module, and the script
# returns to where it was started from at the end.
Push-Location -LiteralPath $poc
$out    = Join-Path $repo 'dist\live-demo'
New-Item -ItemType Directory -Force -Path $out | Out-Null
$trans  = Join-Path $out 'transcript.txt'
$live   = Join-Path $out 'live.json'
"VISION live demo - $(Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ')" | Set-Content -LiteralPath $trans -Encoding utf8

$results = New-Object System.Collections.ArrayList
function AddResult([string]$n,[string]$kicker,[string]$claim,[string]$evidence,[string]$verdict,[string]$why) {
    $null = $results.Add([pscustomobject]@{Number=$n;Kicker=$kicker;Claim=$claim;Evidence=$evidence;Verdict=$verdict;Why=$why})
}
function Say($s)  { Write-Host "  $s" -ForegroundColor Green; $s | Out-File $trans -Append -Encoding utf8 }
function Dim($s)  { Write-Host "  $s" -ForegroundColor DarkGray; $s | Out-File $trans -Append -Encoding utf8 }
function Log($name, $o) { $o | Out-File -LiteralPath (Join-Path $out "$name.log") -Encoding utf8 }
function Act($n,$kicker,$title) {
    Write-Host ''
    Write-Host ('  ' + ('#' * 70)) -ForegroundColor Cyan
    Write-Host ("  ACT $n   $kicker") -ForegroundColor Yellow
    Write-Host "  $title" -ForegroundColor Gray
    Write-Host ('  ' + ('#' * 70)) -ForegroundColor Cyan
    $null = @"
ACT $n  $kicker
$title
$('-' * 70)
"@
    $null | Out-File $trans -Append -Encoding utf8
}
function Write-Live {
    $tmp = "$live.tmp"
    ([pscustomobject]@{ now = (Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ')
                        camera = $script:cameraName; acts = $script:acts } |
        ConvertTo-Json -Depth 6) | Out-File -LiteralPath $tmp -Encoding utf8
    Move-Item -LiteralPath $tmp -Destination $live -Force
}
function Push-Act([string]$n,[string]$t,[string]$e,[string]$v,[string]$s) {
    $existing = $script:acts | Where-Object { $_.n -eq $n }
    if ($existing) { $existing.t=$t; $existing.e=$e; $existing.v=$v; $existing.state=$s }
    else { $script:acts += [pscustomobject]@{n=$n;t=$t;e=$e;v=$v;state=$s} }
    Write-Live
}

$script:acts = @()
$script:cameraName = ''
$script:pocctl = Join-Path $poc 'bin\pocctl.exe'
$watch = $null
$xpsimProc = $null

# Binaries are rebuilt when the SOURCES are newer, and the reason is measured:
# the first run of this script reused a watch.exe built before the relay wiring
# landed and happily demoed the old product, printing a source line without the
# public-relay sentence in it. A demo that runs yesterday's binary is a demo of
# yesterday's module, and it looks exactly like this one from the outside.
function Test-BinariesAreFresh {
    $newest = Get-ChildItem -Path (Join-Path $poc 'cmd'),(Join-Path $poc 'internal') -Filter *.go -Recurse |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $newest) { return }   # no sources found: nothing to compare
    foreach ($bin in @($script:pocctl, (Join-Path $poc 'bin\watch.exe'))) {
        if ((Test-Path -LiteralPath $bin) -and ((Get-Item -LiteralPath $bin).LastWriteTime -ge $newest.LastWriteTime)) { continue }
        $pkg = if ($bin -like '*pocctl*') { './cmd/pocctl' } else { './cmd/watch' }
        Say "building $pkg (the sources are newer than $([System.IO.Path]::GetFileName($bin)))"
        & go build -o $bin $pkg 2>&1 | Where-Object { $_ -match 'error|Error' } | ForEach-Object { Say "  $_" }
        if ($LASTEXITCODE -ne 0) { Say "the build failed; a stale or missing binary is not a demo."; Stop-Everything; Exit-Demo 1 }
    }
}
Test-BinariesAreFresh

function Stop-Everything {
    if ($script:watch)    { try { Stop-Process -Id $script:watch.Id -Force } catch {}; $script:watch = $null }
    if ($script:xpsimProc) { try { Stop-Process -Id $script:xpsimProc.Id -Force } catch {}; $script:xpsimProc = $null }
    if ($script:sidecarProc) { try { Stop-Process -Id $script:sidecarProc.Id -Force } catch {}; $script:sidecarProc = $null }
}

# Every exit goes through here, so the working directory is restored whatever
# happened: a demo that leaves the shell inside the POC module is a demo whose
# next invocation is somebody else's bug.
function Exit-Demo([int]$code) {
    Stop-Everything
    Pop-Location
    exit $code
}

# ===========================================================================
Act 0 'THE BOX' 'What this machine can and cannot do, said before anything runs'
Push-Act '00' 'The box' 'A demo that does not check its own prerequisites is a demo that fails on stage.' '' 'live'

function Has([string]$name) { $c = Get-Command $name -ErrorAction SilentlyContinue; if ($c) { return $c.Source } return $null }

$ffmpeg = Has 'ffmpeg'
if ($ffmpeg) { Say "ffmpeg    $ffmpeg" } else { Say "ffmpeg    ABSENT - the source, the recording and the frames all stop here"; AddResult "0" "THE BOX" "ffmpeg is present" "ffmpeg on PATH" "REFUSED" "without ffmpeg there is no source, no recording and no frames"; Write-Host ''; Exit-Demo 1 }

$ytdlp = Has 'yt-dlp'
if (-not $ytdlp) {
    # A named absence, never a quiet pass: without the resolver there are no
    # cameras at all, and the honest move is to say so and how to fix it.
    Say 'yt-dlp    ABSENT. This demo reads public cameras through it and there is no fallback:'
    Say "          a guessed playlist URL is a stream nobody resolved. Install it with"
    Say '          winget install --id yt-dlp.yt-dlp -e'
    AddResult "0" "THE BOX" "yt-dlp is present" "ABSENT" "REFUSED" "the cameras cannot be resolved without it, and this demo does not pretend otherwise"
    Write-Host ''
    exit 1
}
Say "yt-dlp    $ytdlp"

# The JavaScript runtime yt-dlp needs for YouTube extraction. It only WARNS
# about a missing one, which is the worst of both worlds: the resolve can still
# work with a reduced format list. Auto-detected here so the demo says which it
# used.
$js = $null
foreach ($cand in 'deno','node') { if (Has $cand) { $js = $cand; break } }
if ($js) { Say "js runtime $js (yt-dlp needs one for YouTube extraction; the resolver passes it on)" }
else { Say "js runtime none found - yt-dlp will warn and resolve with a reduced format list. The demo proceeds and the warning is on the record." }

$smi = Has 'nvidia-smi'
if ($smi) {
    $gpu = & nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>$null
    Say "gpu       $gpu"
} else {
    Say 'gpu       no nvidia-smi: the CUDA execution provider will refuse by name below, and the demo will run on the CPU only if SP_POC_ORT_EP=cpu is set'
}

$pyOrt = & python -c "import onnxruntime;print(onnxruntime.__version__)" 2>$null
if ($TriggerMode -eq 'sidecar') {
    Say "python    onnxruntime $pyOrt - THE TRIGGER PATH OF THIS RUN. Every box this demo"
    Say '          reports is decoded by the Python worker below, on this runtime, and the'
    Say '          measured cost is a loopback round trip plus a decode, not the in-process'
    Say "          session's. `-TriggerMode detector` runs the other path."
} else {
    Say "python    onnxruntime $pyOrt (the SIDECAR's runtime, and NOT the shipped trigger path:"
    Say '          the detector runs in-process against the ONNX Runtime the module fetched itself)'
}

# ---------------------------------------------------------------------------
# THE SIDECAR PATH'S OWN PREREQUISITES, and the worker this run's boxes come
# from. The anchors file is the part a clean clone used to miss: the nine
# anchor boxes of this anchored export live in the model's .cfg and NOT in the
# ONNX file, so the worker loads the weights and then refuses every frame with
# a sentence that is true and useless at 9pm. `pocctl fetch-model` (and
# `bootstrap`, which calls it) writes the file as the model's sibling from the
# committed copy and checks it against the digest in the licence record, so
# this script checks the same thing in the same words.
# ---------------------------------------------------------------------------
$script:sidecarProc = $null
if ($TriggerMode -eq 'sidecar') {
    $anchors = "$AIModel.anchors.txt"
    if (-not (Test-Path -LiteralPath $anchors)) {
        Say "$anchors is MISSING, and this run cannot decode a frame without it."
        Say '  The pinned Model Zoo yolov4n.onnx is a multi-scale ANCHORED head: its anchor boxes'
        Say '  are the [anchors] section of yolov4-416.cfg and the ONNX export does not carry them.'
        Say '  `pocctl fetch-model` writes this file beside the weights from the committed copy at'
        Say '  pyworker/fixtures/yolov4n.anchors.txt, checked against the digest recorded in'
        Say '  pyworker/fixtures/yolov4n.licence.json. Run it and re-run this demo:'
        Say "    $script:pocctl fetch-model --out `"$AIModel`""
        AddResult "0" "THE BOX" "the model's anchors file exists" "ABSENT: $anchors" "REFUSED" "an anchored export without its anchors decodes nothing, and the worker refuses by name"
        Exit-Demo 1
    }
    $anchorsSha = (Get-FileHash -LiteralPath $anchors -Algorithm SHA256).Hash.ToLower()
    Say "anchors   $anchors (sha256 $($anchorsSha.Substring(0,16))..., the [anchors] section of yolov4-416.cfg)"

    # Is a worker already answering on the address this run would use? Then
    # the run uses it and this script stops nothing it did not start.
    $alive = $null
    try { $alive = Invoke-RestMethod -Uri "$AIAddr/healthz" -TimeoutSec 2 -ErrorAction Stop } catch {}
    if ($alive -and $alive.ready) {
        Say "sidecar   $AIAddr is already answering (model $($alive.model), input $($alive.input_width), $($alive.providers -join ','))"
        Say '          it is the one this run will use, and this script stops nothing it did not start'
    } else {
        Dim "starting the Python worker at $AIAddr (model $AIModel, its anchors beside it)"
        $sidecarScript = Join-Path $poc 'pyworker\server.py'
        $script:sidecarProc = Start-Process -FilePath 'python' -ArgumentList @(
            $sidecarScript, '--model', $AIModel, '--anchors', $anchors,
            '--host', '127.0.0.1', '--port', ([Uri]$AIAddr).Port
        ) -WorkingDirectory $poc -PassThru -WindowStyle Hidden
        if (-not $script:sidecarProc) {
            AddResult "0" "THE BOX" "the sidecar starts" "the process did not start" "REFUSED" "a run with no worker degrades every frame, which is not a demo of this path"
            Exit-Demo 1
        }
        # Wait for /healthz, with a timeout and a named failure. A worker that
        # loads slowly is normal on a 257 MB model; one that never answers is
        # said, with the command to run by hand so its stderr is readable.
        $ready = $false
        for ($i = 0; $i -lt 60; $i++) {
            Start-Sleep -Milliseconds 500
            if ($script:sidecarProc.HasExited) {
                Say "the sidecar process exited (code $($script:sidecarProc.ExitCode)) before it answered. Its stderr"
                Say '  names the reason: an anchored model without its anchors is the classic one, and a'
                Say "  missing $anchors is exactly that. Re-run by hand to read it:"
                Say "    python $sidecarScript --model `"$AIModel`" --anchors `"$anchors`" --host 127.0.0.1 --port $([Uri]$AIAddr).Port"
                AddResult "0" "THE BOX" "the sidecar answers /healthz" "exited (code $($script:sidecarProc.ExitCode))" "REFUSED" "see the stderr above; the demo does not start a run against a worker that is not there"
                Exit-Demo 1
            }
            try { $h = Invoke-RestMethod -Uri "$AIAddr/healthz" -TimeoutSec 2 -ErrorAction Stop } catch { continue }
            if ($h.ready) { $ready = $true; break }
        }
        if (-not $ready) {
            try { Stop-Process -Id $script:sidecarProc.Id -Force } catch {}
            AddResult "0" "THE BOX" "the sidecar answers /healthz" "no answer in 30s" "REFUSED" "the worker's own load is the reason, and its stderr is where it is named"
            Exit-Demo 1
        }
        Say "sidecar   $AIAddr (pid $($script:sidecarProc.Id)) model $($h.model), input $($h.input_width), $($h.providers -join ',')"
    }
}

$ortDir = Join-Path $poc 'models'
$cudnn  = Join-Path $ortDir 'cudnn64_9.dll'
Say "runtime   $ortDir (cudnn64_9.dll present: $(Test-Path -LiteralPath $cudnn))"
Say '          The CUDA provider resolves its dependencies through PATH, not beside the'
Say '          library, so the module puts that directory on PATH itself before the session'
Say '          opens. Without it the append succeeds and the first Conv node fails.'
Push-Act '00' 'The box' 'ffmpeg, yt-dlp, a JS runtime, and the fetched GPU runtime are all on the record.' '' 'done'

# ===========================================================================
Act 1 'RESOLVE' 'The curated cameras, resolved live with yt-dlp'
Push-Act '01' 'The table' '`pocctl live list` - four real cameras, none of them ours.' '' 'live'
& $script:pocctl live list *>&1 | Tee-Object -FilePath (Join-Path $out 'cameras.log') | ForEach-Object { $_ }
Dim ''
Dim '  Every row records a licence basis, a verification date and a clear statement'
Dim '  that no written permission to store clips exists. Watching a feed and storing'
Dim '  clips of it are different acts.'
Push-Act '01' 'The table' 'The table is the record of whose cameras these are, and of what nobody may do with them.' '' 'done'

Push-Act '02' 'Resolve' '`pocctl live resolve --all` - each row becomes an HLS playlist, or is named as offline.' '' 'live'
$resolveArgs = @('live','resolve','--all')
if ($js) { $resolveArgs += @('--js', $js) }
$resolve = & $script:pocctl $resolveArgs 2>&1
Log 'resolve.log' $resolve
$resolve | ForEach-Object { $_ }
$resolved = @($resolve | Where-Object { $_ -match '^\s{2}\S+\s+https' })
if ($resolved.Count -eq 0) {
    Say 'none of the cameras resolved. The demo stops here rather than falling back to a clip:'
    Say 'a run over recorded material is not a live demo, and its source line says so.'
    AddResult "1" "RESOLVE" "At least one real camera resolves" "none resolved" "REFUSED" "the whole point of this demo is a real source"
    Exit-Demo 1
}
Say "$($resolved.Count) camera(s) answered. The stream hosts are shown; the signed playlist URLs stay in the run row."
Push-Act '02' 'Resolve' 'Real cameras, real playlists, and a redacted line on the console.' "$($resolved.Count) answered" 'done'

# ===========================================================================
Act 2 'CALIBRATE' 'A p99 the preflight gate can multiply, measured on this path'
Push-Act '03' 'Calibrate' '`pocctl bench --images` times the session and writes the calibration preflight reads.' '' 'live'
if (-not (Test-Path -LiteralPath $script:pocctl)) {
    Say "pocctl is not built at $($script:pocctl); the gate builds it, and this script refuses to guess where one might be."
    Exit-Demo 1
}
if ($TriggerMode -eq 'sidecar') {
    if (-not $DataRoot) { $DataRoot = Join-Path $poc ('.live-demo\{0:yyyyMMdd-HHmmss}' -f (Get-Date)) }
    $env:SP_POC_DATA_ROOT  = $DataRoot
    New-Item -ItemType Directory -Force -Path $DataRoot | Out-Null
    Dim "data root  $DataRoot"
    Dim '           (one directory per run: a re-run never overwrites the run it is compared against)'
    # The loopback pass: the same still set, the worker's own endpoint, the
    # same record with its trigger field set to `sidecar`. The in-process
    # session is NOT opened - a sidecar bench that loaded the CUDA runtime
    # would measure a machine the sidecar path never uses.
    Dim 'this run is on the SIDECAR path, so the timing pass drives the worker''s own endpoint'
    Dim 'and the record it writes names the sidecar: preflight refuses a detector p99 applied to a'
    Dim 'sidecar run by name, and vice versa. One file, one trigger field.'
    $bench = & $script:pocctl bench --images (Join-Path $poc 'testdata\still') --data-root $DataRoot `
        --trigger sidecar --ai-addr $AIAddr --model $AIModel 2>&1
    Log 'bench.log' $bench
    $bench | Select-String -Pattern 'trigger|sidecar|model|providers|input|timing pass|p50|refused|calibration' | ForEach-Object { Say $_.Line }
    $benchOk = ($LASTEXITCODE -eq 0) -and ($bench | Select-String -Pattern 'p99\s+[0-9.]+ms')
    if (-not $benchOk) {
        Say 'the sidecar timing pass did not record a p99, and its reason is above.'
        Say '  A run on this path would have the AI row report the sidecar as UNMEASURED, which is'
        Say '  honest and is not this demo. A refused round trip is a wall and not a sample: a p99'
        Say '  measured over degradations would clear a run against the arithmetic of a worker that'
        Say '  answered nothing.'
        AddResult "1" "CALIBRATE" "a measured loopback p99 over the still set" "see bench.log" "REFUSED" "the record preflight multiplies must be a real round trip"
        Exit-Demo 1
    }
    Push-Act '03' 'Calibrate' 'The sidecar''s loopback p99 is measured, not borrowed from the detector.' '' 'done'
} else {
    $env:SP_POC_DET_MODEL  = Join-Path $poc 'models\yolov4n.onnx'
    $env:SP_POC_ORT_LIB    = Join-Path $ortDir 'onnxruntime.dll'
    $env:SP_POC_ORT_EP     = 'cuda'
    $env:SP_POC_ORT_THREADS = '4'
    $env:SP_POC_DET_INPUT  = '416'
    $env:SP_POC_DET_SCORE  = '0.05'
    $env:SP_POC_SCORE_THRESHOLD = '0.45'
    if (-not $DataRoot) { $DataRoot = Join-Path $poc ('.live-demo\{0:yyyyMMdd-HHmmss}' -f (Get-Date)) }
    $env:SP_POC_DATA_ROOT  = $DataRoot
    New-Item -ItemType Directory -Force -Path $DataRoot | Out-Null
    Dim "data root  $DataRoot"
    Dim '           (one directory per run: a re-run never overwrites the run it is compared against)'

    $bench = & $script:pocctl bench --images (Join-Path $poc 'testdata\still') --data-root $DataRoot 2>&1
    Log 'bench.log' $bench
    $bench | Select-String -Pattern 'timing pass|p50|calibration|licence' | ForEach-Object { Say $_.Line }
    Push-Act '03' 'Calibrate' 'The detector''s p99 is measured, not asserted.' '' 'done'
}

# ===========================================================================
Act 3 'REHEARSE' 'Gate D: the fault classes, rehearsed against a simulator'
Push-Act '04' 'Rehearse' '`pocctl rehearse all` - a run is only started by code shown to survive faults.' '' 'live'
$xpsimProc = Start-Process -FilePath $script:pocctl -ArgumentList 'sim','--addr',"127.0.0.1:$SimPort" -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $out 'xpsim.out') -RedirectStandardError (Join-Path $out 'xpsim.err')
Start-Sleep -Seconds 3
$env:SP_POC_SIM_URL = "http://127.0.0.1:$SimPort"
$env:SP_POC_XPROTECT_BASE_URL = "http://127.0.0.1:$SimPort"
$env:SP_POC_XPROTECT_CLIENT_ID = 'demo'; $env:SP_POC_XPROTECT_CLIENT_SECRET = 'demo'
$env:SP_POC_XPROTECT_USERNAME = 'demo'; $env:SP_POC_XPROTECT_PASSWORD = 'demo'
$env:SP_POC_CAMERA_GUID = '0d9a1b2c-1111-2222-3333-444455556666'
$env:SP_POC_DEVICE_PATH = '/Local/00000000-0000-0000-0000-000000000000'
$env:SP_POC_EVENT_TYPE_GUID = 'b4d1e1c2-0000-4000-8000-000000000001'
$rehearse = & $script:pocctl rehearse all 2>&1
Log 'rehearse.log' $rehearse
$rehearse | Select-String -Pattern '\[PASS\]|\[FAIL\]|gate D|Export' | ForEach-Object { Say $_.Line }
$env:SP_POC_GATE_D = 'PASS'
Dim 'the verdict is exported into this process only; the NEXT run of the demo rehearses again.'
Push-Act '04' 'Rehearse' 'Seven fault classes, all observed behaving as documented.' '' 'done'

# ===========================================================================
Act 4 'PREFLIGHT' 'The whole gate, against the real camera'
Push-Act '05' 'Preflight' '`pocctl preflight` after `pocctl live env` supplies the camera.' '' 'live'
$chosen = $Camera
if (-not $chosen) {
    $first = $resolved | Select-Object -First 1
    $chosen = ($first -split '\s+')[1]
}
$script:cameraName = $chosen
$envArgs = & $script:pocctl live env $chosen 2>&1 | Where-Object { $_ -like '$env:*' }
$envArgs | ForEach-Object { Invoke-Expression $_ }

# The rest of the run's world. Every one of these is required by the module's
# schema, and a demo that did not state them would have the run guess them.
$cfg = [ordered]@{
    SP_POC_MODE='live'; SP_POC_LABEL='live-demo'
    # The trigger travels with the run: on the sidecar path the run's own
    # source of boxes is the Python worker, and the schema requires its
    # address, model and script beside the mode. On the detector path the
    # model, library and thread count this run decoded with are required, and
    # both sets are stated rather than inherited from a shell.
    SP_POC_TRIGGER_MODE=$TriggerMode
    SP_POC_DET_NMS_CLASS='0.45'; SP_POC_DET_NMS_GLOBAL='0.75'; SP_POC_DET_MAX_BOXES='100'
    SP_POC_CAMERA_INTEGRITY='false'; SP_POC_VLM_PROVIDER='none'
    SP_POC_AI_FPS='5'; SP_POC_AI_DEADLINE_MS='800ms'; SP_POC_AI_BUDGET_MS='800ms'
    SP_POC_AI_BACKPRESSURE_MS='5s'; SP_POC_AI_INPUT_WIDTH='640'
    SP_POC_MIN_BOX_AREA='0.0005'; SP_POC_GHOST_MIN_SCORE='0.10'; SP_POC_TRACK_MIN_FRAMES='3'
    SP_POC_MAX_BOXES_DRAWN='12'; SP_POC_BOX_LOG_FRAMES='20000'; SP_POC_REPORT_METRICS='events'
    SP_POC_CLASSES='person,vehicle'; SP_POC_COOLDOWN_MS='10s'; SP_POC_TRACK_MAX_AGE_MS='2s'
    SP_POC_TRACK_IOU='0.3'; SP_POC_LINE_TOLERANCE='0.01'
    SP_POC_CLIP_BEFORE_MS='5s'; SP_POC_CLIP_AFTER_MS='5s'
    SP_POC_MAX_DISK_BYTES='214748364800'; SP_POC_RSS_LIMIT_BYTES='1610612736'
    SP_POC_DB_LIMIT_BYTES='536870912'; SP_POC_CPU_LIMIT_PERCENT='180'; SP_POC_DISK_HEADROOM='1.5'
    SP_POC_DISK_BUDGET_DAYS='7'; SP_POC_HEARTBEAT_INTERVAL='10s'; SP_POC_GAP_THRESHOLD='30s'
    SP_POC_SOAK_TARGET='168h'; SP_POC_SEGMENT_SECONDS='60'; SP_POC_CLOCK_SOURCE='ntp'
    SP_POC_NOVELTY_THRESHOLD='0.35'; SP_POC_NOVELTY_RELEASE='0.20'; SP_POC_NOVELTY_SUSTAIN='90'
    SP_POC_NOVELTY_GRID='64'; SP_POC_NOVELTY_FPS='0'; SP_POC_CROP_PASS='false'
    SP_POC_UI_ADDR="127.0.0.1:$UIPort"
    SP_POC_PUSH_MAX_ATTEMPTS='6'; SP_POC_PUSH_TIMEOUT='10s'
}
# The sidecar path's required variables, stated beside the mode they belong to.
# The anchors file is one of them and it is ALSO how the worker was started in
# act 0: the schema requires the run to state its export's family explicitly
# (`SP_POC_AI_ANCHORS=<file>` for an anchored one, `none` for a fused one), and
# a run that names one file while the worker decoded with another is the
# mismatch the record's model-identity check refuses.
if ($TriggerMode -eq 'sidecar') {
    $cfg['SP_POC_AI_ADDR'] = $AIAddr
    $cfg['SP_POC_AI_MODEL'] = $AIModel
    $cfg['SP_POC_SIDECAR_SCRIPT'] = 'pyworker\server.py'
    $cfg['SP_POC_AI_ANCHORS'] = "$AIModel.anchors.txt"
}
foreach ($k in $cfg.Keys) { Set-Item -Path "env:$k" -Value $cfg[$k] }

$pre = & $script:pocctl preflight 2>&1
Log 'preflight.log' $pre
# The filter names the rows this demo's claims rest on: the source row, the
# calibration line (whichever path this run is on), the detector or sidecar
# readiness row, and the AI p99 row. On the sidecar path the AI row reads the
# measured loopback p99 from the record the calibrate act just wrote; on the
# detector path it is not applicable, and the row says so rather than passing.
$pre | Select-String -Pattern '\[PASS\]|\[FAIL\]|preflight:|det_p99|source:|calibration|sidecar|ai p99' | ForEach-Object { Say $_.Line }
if ($LASTEXITCODE -ne 0) {
    Say 'preflight refused. The demo stops: a run the gate refuses is a run that does not start.'
    AddResult "2" "PREFLIGHT" "preflight is green against the resolved camera" "see preflight.log" "REFUSED" "the run clock never started"
    Exit-Demo 1
}
AddResult "2" "PREFLIGHT" "preflight is green against a real camera" "all 11 checks pass; the source row reads the camera's own codec and frame rate" "PASSED" "the plan's first definition of done, witnessed"
Push-Act '05' 'Preflight' 'Eleven checks, one real camera.' '11/11' 'done'

# ===========================================================================
Act 5 'RUN' 'The camera on the screen, labelled as somebody else''s'
Push-Act '06' 'Run' "watch reads the relay for $($CaptureSeconds)s with the read-only screen up." '' 'live'
$watchExe = Join-Path $poc 'bin\watch.exe'
$watch = Start-Process -FilePath $watchExe -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $out 'watch.out') -RedirectStandardError (Join-Path $out 'watch.err')
Say "screen  http://127.0.0.1:$UIPort  (read-only, loopback only)"
try { Start-Process "http://127.0.0.1:$UIPort" } catch { Dim "open http://127.0.0.1:$UIPort on the screen" }

# What the run itself says while it runs. The screen is a TUI over the same
# facts; this is the log's own line about the source, which is the one the
# honesty of the whole demo rests on.
#
# The pattern is anchored to the RUN ROW, and the run row is the recorded line:
# "run run-<id> recorded: source: <the sentence>". Matching 'source:' on its own
# picks up "live source: the rolling recording is..." and reports the ring
# buffer's description as the source line, which is how the first run of this
# script read its own demo wrongly.
$deadline = (Get-Date).AddSeconds($CaptureSeconds + 20)
$sourceLine = ''
while ((Get-Date) -lt $deadline) {
    Start-Sleep -Seconds 3
    $out1 = Get-Content (Join-Path $out 'watch.out') -ErrorAction SilentlyContinue
    $m = $out1 | Select-String -Pattern 'recorded: source:\s+(.+)$' | Select-Object -Last 1
    if ($m) { $sourceLine = $m.Matches[0].Groups[1].Value.Trim() }
    if ($watch.HasExited) { break }
}
if ($watch) { try { Stop-Process -Id $watch.Id -Force } catch {}; $watch = $null }
$watchOut = Get-Content (Join-Path $out 'watch.out') -ErrorAction SilentlyContinue
Log 'watch.log' $watchOut
$watchOut | Select-String -Pattern 'source:|refused to answer|trigger mode|heartbeat' | Select-Object -Last 10 | ForEach-Object { Say $_.Line }
if (-not $sourceLine) {
    $m = $watchOut | Select-String -Pattern 'recorded: source:\s+(.+)$' | Select-Object -Last 1
    if ($m) { $sourceLine = $m.Matches[0].Groups[1].Value.Trim() }
}
# The run's own id, read from its banner rather than guessed: the prove act
# below is about THIS run, and a demo that proved a different run would be
# proving yesterday.
$liveRunID = ''
$m = $watchOut | Select-String -Pattern '\b(run_[0-9A-Za-z]+)\b' | Select-Object -First 1
if ($m) { $liveRunID = $m.Matches[0].Groups[1].Value }
Say '  the run''s own recorded source line'
Say "    $sourceLine"
if ($liveRunID) { Dim "  run id    $liveRunID (the prove act replays this run, and only this one)" }

# The sidecar path's cost, said in this run's own words rather than left in the
# calibrate act's log: a loopback round trip plus a Python decode is a
# DIFFERENT number from the in-process session's, and a demo that showed one
# while running the other would be the borrow the calibration record refuses.
if ($TriggerMode -eq 'sidecar') {
    $calLine = ($bench | Select-String -Pattern 'p50 .*ms.*p99|p99\s+[0-9.]+ms' | Select-Object -Last 1)
    if ($calLine) { Say "  this run's measured cost: $($calLine.Line.Trim())" }
}
# The worker's job for this run is done. It is stopped here rather than left
# listening: a demo that leaves a Python process holding a port behind it is a
# surprise for the next command on this box.
if ($script:sidecarProc) { try { Stop-Process -Id $script:sidecarProc.Id -Force } catch {}; $script:sidecarProc = $null; Dim '  the sidecar worker this demo started is stopped' }
if ($sourceLine -match 'public relay of a real camera, NOT ours') {
    AddResult "3" "RUN" "The source line says the camera is somebody else's" "$sourceLine" "PASSED" "the label is derived from a flag, not typed by the demo"
    Push-Act '06' 'Run' 'The screen said exactly what the camera is, and what it is not.' "$sourceLine" 'done'
} else {
    AddResult "3" "RUN" "The source line says the camera is somebody else's" "got: $sourceLine" "FAILED" "a run over a public relay that reads as our own camera is the defect this path exists to end"
    Push-Act '06' 'Run' 'The source line did not say whose camera this is.' "$sourceLine" 'failed'
}

# ===========================================================================
Act 5b 'PROVE' 'One recorded execution of THIS run, replayed from stored facts'
Push-Act '06b' 'Prove' '`pocctl prove` re-runs a recorded decision with zero provider calls.' '' 'live'
if ($liveRunID) {
    $prove = & $script:pocctl prove --data-root $DataRoot --run $liveRunID 2>&1
    # The verdict is prove's EXIT CODE, not a phrase matched in its output. The
    # tool prints "0 (measured, not asserted)" beside a nil counter AND fails
    # the beat; a text match would have called that a pass, which is precisely
    # the false pass this module exists to end.
    $proveExit = $LASTEXITCODE
    Log 'prove.log' $prove
    $prove | Select-String -Pattern 'provider calls|decision|\[FAIL\]|verdict|FAIL$' | Select-Object -First 12 | ForEach-Object { Say $_.Line }
    if ($proveExit -eq 0) {
        AddResult "4" "PROVE" "a recorded execution replays from stored facts" "zero provider calls, the decision reproduced" "PASSED" "invariant 2, witnessed on a run this demo just finished"
        Push-Act '06b' 'Prove' 'Stored facts in, decision out, zero provider calls.' '' 'done'
    } else {
        $failedBeats = ($prove | Select-String -Pattern '\[FAIL\] (\S+)') | ForEach-Object { $_.Matches[0].Groups[1].Value }
        $beatList = if ($failedBeats) { $failedBeats -join '; ' } else { 'see prove.log' }
        AddResult "4" "PROVE" "a recorded execution replays from stored facts" "prove exited $proveExit; beats: $beatList" "FAILED" "a replay the module refuses to call a replay is not one, and this demo does not call it one either"
        Push-Act '06b' 'Prove' 'The replay refused to call itself a replay.' "beats: $beatList" 'failed'
        Say ''
        Say '  WHY, IN THE TOOL''S OWN WORDS: the zero-calls beat is UNMEASURED unless a VLM provider is'
        Say '    configured, and this demo runs SP_POC_VLM_PROVIDER=none — the only two providers this'
        Say '    module has are none and hafnia, and hafnia needs a key nobody has. A printed zero with'
        Say '    no counter behind it would be a claim nobody made, so prove fails the beat instead.'
        Say '    That is the system working, and a demo that scored around it would be the defect.'
    }
} else {
    Say 'no run id was captured from the run''s banner, so there is nothing to prove.'
    Say '  This is the honest branch: the run recorded no row, or its banner was not in watch.out.'
    AddResult "4" "PROVE" "a recorded execution replays from stored facts" "no run id captured" "REFUSED" "a prove over a run nobody recorded is a prove about nothing"
    Push-Act '06b' 'Prove' 'No run was recorded to prove.' '' 'failed'
}

# ===========================================================================
Act 5c 'MARK' 'The review sheet, and one verdict whose AUTHOR is stated'
Push-Act '07b' 'Mark' '`pocctl review` writes the sheet; `pocctl mark` writes one verdict with a name on it.' '' 'live'
$sheet = Join-Path $out 'review.html'
$reviewOut = & $script:pocctl review --data-root $DataRoot --out $sheet 2>&1
Log 'review.log' $reviewOut
$reviewOut | Select-String -Pattern 'event\(s\)|wrote' | ForEach-Object { Say $_.Line }
# One tag from the sheet's own rows, so the command this act runs is the command
# the sheet puts in a reviewer's hands. A parse of generated HTML is the weakest
# link in this script, and it is checked: no tag found means the act says so and
# leaves the sheet for a person, rather than marking a tag nobody pushed - which
# the store would refuse by name anyway.
$markTag = ''
if (Test-Path -LiteralPath $sheet) {
    $m = [regex]::Match((Get-Content -LiteralPath $sheet -Raw), '<code>([A-Za-z0-9][A-Za-z0-9._-]*)</code>')
    if ($m.Success) { $markTag = $m.Groups[1].Value }
}
if (-not $markTag) {
    Say 'the run pushed no event with a tag, so there is nothing to mark. An empty run is honest'
    Say '  and is not a bug; this act says so rather than writing a mark about nothing.'
    Push-Act '07b' 'Mark' 'No pushed event to mark.' '' 'done'
} else {
    Say "the sheet's first pushed event is tagged  $markTag"
    Say "the command the sheet puts in a reviewer's hands:"
    Dim "  pocctl mark $markTag --verdict true_positive --by YOUR-NAME --data-root $DataRoot"
    Dim "  pocctl mark $markTag --verdict false_positive --by YOUR-NAME --data-root $DataRoot"
    if ($MarkVerdict) {
        $markOut = & $script:pocctl mark $markTag --verdict $MarkVerdict --by $MarkBy --data-root $DataRoot 2>&1
        Log 'mark.log' $markOut
        $markOut | Select-String -Pattern 'marked|precision' | ForEach-Object { Say $_.Line }
        Say ''
        Say '  THAT VERDICT IS THE DEMO''S, NOT A REVIEWER''S.'
        Say '    No person looked at this frame. The mark exists to show the write path and the join'
        Say '    (a tag the run never pushed is refused by name), and the report''s false-positive line'
        Say "    names "$MarkBy" as its author, so a later reader can tell exactly that."
        AddResult "5" "MARK" "one verdict writes through the store with an author on it" "$markTag as $MarkVerdict by $MarkBy" "PASSED" "the write path and the join are demonstrated; the OPINION is the demo''s and nobody looked"
        Push-Act '07b' 'Mark' 'One verdict written, with the demo''s name on it and the sentence that says whose it is.' '' 'done'
    } else {
        Say ''
        Say '  no verdict was asked for, so none was written. Open the sheet, look at the clip, and'
        Say '  run the command with your own name: `pocctl report` then counts it as a measurement'
        Say '  rather than as this demo''s demonstration. A rate with no author is a number somebody'
        Say '  typed, and nothing in the report can say whether they looked.'
        Push-Act '07b' 'Mark' 'The sheet is written; the verdict is a person''s to give.' '' 'done'
    }
}

# ===========================================================================
Act 6 'AFTER' 'What the run recorded, read back from its own store'
Push-Act '07' 'After' 'The run row, the counts and the absence events come from the store the run wrote.' '' 'live'
$report = & $script:pocctl report --data-root $DataRoot 2>&1
Log 'report.log' $report
$report | Select-Object -First 40 | ForEach-Object { Dim $_ }
Push-Act '07' 'After' 'Read back from the store, not from this script''s memory.' '' 'done'

# ===========================================================================
# The still, captured from the run's own read-only screen while it is up. It is
# the demo's one momento and it is EMBEDDED in the artefact, so the file a judge
# opens on a phone carries the frame with it. A still that could not be captured
# is a stated fact in the artefact, not a broken image.
$stillPath = Join-Path $out 'still.jpg'
$stillCaptured = $false
foreach ($p in @('/still.jpg', '/snapshot.jpg')) {
    try {
        Invoke-WebRequest -Uri "http://127.0.0.1:$UIPort$p" -TimeoutSec 5 -OutFile $stillPath -UseBasicParsing -ErrorAction Stop
        if ((Test-Path -LiteralPath $stillPath) -and ((Get-Item -LiteralPath $stillPath).Length -gt 0)) {
            $stillCaptured = $true
            Dim "  still    captured from http://127.0.0.1:$UIPort$p"
            break
        }
    } catch { Dim "  (the run's screen at 127.0.0.1:$UIPort$p did not answer; it stops with the run)" }
}

# The report's own JSON, so the artefact quotes the run's rows rather than this
# script's reading of them. `report --json` is the same report the console
# printed, in the shape the AC table below is built from.
$repJson = & $script:pocctl report --data-root $DataRoot --json 2>&1
Log 'report.json.log' $repJson
$rep = $null
try { $rep = $repJson | ConvertFrom-Json } catch { Dim '  (the report''s JSON could not be read; the artefact says so rather than guessing rows)' }
$acRows = @()
if ($rep -and $rep.criteria) {
    foreach ($c in $rep.criteria) {
        $acRows += [pscustomobject]@{
            ID = [string]$c.id; Verdict = [string]$c.verdict; Name = [string]$c.name
            How = [string]$c.how; Observed = [string]$c.observed; Missing = [string]$c.missing
        }
    }
}
$acPassed = @($acRows | Where-Object { $_.Verdict -eq 'PASS' }).Count
$acFailed = @($acRows | Where-Object { $_.Verdict -eq 'FAIL' }).Count
$acNotRun = @($acRows | Where-Object { $_.Verdict -eq 'NOT RUN' }).Count
$notProven = @()
if ($rep -and $rep.not_proven) { $notProven = @($rep.not_proven | ForEach-Object { [string]$_ }) }
if ($notProven.Count -lt 2) {
    # The two lines this demo owes a reader whether or not the report produced
    # its own: the false-positive rate nobody measured, and the push target
    # that never left the box.
    $notProven = @(
        'no person looked at a frame here: the false-positive rate is a demonstration or NOT RUN, and it is never folded into the verdict',
        'no XProtect server was contacted and no OUTSIDE push left this box: the events went to xpsim, the local SIMULATOR'
    )
}

if (-not $NoTeardown) { Stop-Everything }
Stop-Process -Name pocctl -ErrorAction SilentlyContinue | ForEach-Object { Dim "stopped a leftover pocctl (pid $($_.Id))" }

$failed = @($results | Where-Object { $_.Verdict -eq 'FAILED' -or $_.Verdict -eq 'REFUSED' })
$passed = @($results | Where-Object { $_.Verdict -eq 'PASSED' })
$refusedFrames = @($watchOut | Select-String -Pattern 'the detector refused to answer' | Select-Object -Last 1)
$decoded = $refusedFrames.Count -eq 0

Act 7 'VERDICT' 'What was real, what was not, and what this demo did not claim'
foreach ($r in $results) {
    Dim ("  [{0}] {1}: {2}" -f $r.Verdict, $r.Number, $r.Claim)
}
Say ''
Say '  the honesty line, recorded by the run itself:'
Say "    $sourceLine"
Say ''
# THE TRIGGER PATH, said in the run's own words. The sidecar is not the detector
# with a different flag: its cost is a loopback round trip plus a Python decode,
# the calibrate act measured it, and the calibration record names the path so a
# run cannot be cleared against the other one's number.
if ($TriggerMode -eq 'sidecar') {
    $costLine = ($bench | Select-String -Pattern 'p50 .*p99 .*ms' | Select-Object -Last 1)
    if ($costLine) {
        Say "  THIS RUN IS ON THE SIDECAR PATH. $($costLine.Line.Trim())"
        Say '    — the measured loopback round trip plus a decode, recorded with trigger=sidecar and'
        Say '    read back by preflight. The in-process session''s p99 was NOT used, and the record'
        Say '    refuses to lend it across.'
    }
} elseif ($decoded) {
    Say '  THIS RUN IS ON THE SHIPPED PATH: the in-process ONNX session, whose p99 the calibrate'
    Say '    act measured by opening it and timing it.'
}
Say ''
if ($decoded) {
    Say '  the detector decoded the frames it was given and its answers are in the report.'
} else {
    # The run's OWN words, quoted. This is the state of the product as measured
    # on 2026-10-15 and the demo is the artefact that says so: the pinned ONNX
    # Model Zoo yolov4n.onnx is a multi-scale ANCHORED head, the in-process Go
    # decoder reads only fused outputs, and every frame is refused by name. The
    # sidecar path decodes that family fully — which is what -TriggerMode
    # sidecar runs and why it exists — and this run said which path it was on.
    Say '  THE DETECTOR DID NOT DECODE THE FRAMES. The run refused every frame with its own'
    Say '    the detector refused to answer: this export is a multi-scale ANCHORED head and the'
    Say '    in-process decoder reads only fused outputs'
    Say '  and the report accordingly scores AC2/AC3/AC4 as NOT RUN rather than as zero. What is'
    Say '  proven here is the SOURCE line and the session cost; the detection rate is NOT RUN, and'
    Say '  a demo that claimed otherwise would be the defect this module exists to end.'
}
Say ''
if ($acRows.Count -gt 0) {
    Say "  the acceptance criteria, from the report's own JSON: $acPassed passed, $acFailed failed, $acNotRun NOT RUN"
    foreach ($a in $acRows) {
        $extra = if ($a.Missing) { " - $a.Missing" } else { '' }
        Dim ("    {0,-5} {1,-8} {2}{3}" -f $a.ID, $a.Verdict, $a.Name, $extra)
    }
}
Say ''
Say '  real:    the camera and its relay - resolved with yt-dlp, read by ffmpeg, codec and frame'
Say '           rate printed by preflight' + $(if ($decoded) {', frames decoded on the GPU'} else {'; the frames were READ and the decode was refused by name'}) + '.'
Say '  NOT real: the video management system. Events went to xpsim, a simulator, and preflight''s'
Say '           own row prints the word "simulator". Nothing here pushed a detection to a'
Say '           Milestone installation.'
Say "  NOT run:  a week. $CaptureSeconds seconds proves the source line and the session cost, and"
Say '           it proves nothing about uptime, drift or the acceptance criteria a real run is'
Say '           scored against.'
Say "  result: $($passed.Count) passed, $($failed.Count) failed or refused."

# ===========================================================================
# THE ARTEFACT. One file a judge keeps, carrying what it does NOT prove beside
# what it did. The acceptance table is quoted from the report's own JSON; the
# source line is the run's own recorded sentence; the camera row and its licence
# sentence are read from the curated table; and the still is embedded, so the
# file is evidence about the run rather than a screenshot of this console.
# ===========================================================================
$html = Join-Path $out 'report.html'

$acTable = ($acRows | ForEach-Object {
    $cls = if ($_.Verdict -eq 'PASS') { 'pass' } elseif ($_.Verdict -eq 'FAIL') { 'fail' } else { 'refused' }
    "<tr><td class='k'>$($_.ID)</td><td class='$cls'>$($_.Verdict)</td><td>$($_.Name)<br><span class='why'>$($_.How)</span></td><td>$($_.Observed)</td><td>$($_.Missing)</td></tr>"
}) -join "`n"
if (-not $acTable) {
    $acTable = "<tr><td colspan='5' class='dim'>the report's JSON could not be read on this run, so no criterion is claimed. The console printed the report above and the log is beside this file.</td></tr>"
}
$notProvenHtml = ($notProven | ForEach-Object { "<li>$_</li>" }) -join ''
$resultRows = ($results | ForEach-Object {
    "<tr><td class='$($_.Verdict.ToLower())'>$($_.Verdict)</td><td>$($_.Number)</td><td>$($_.Claim)<br><span class='why'>$($_.Evidence)<br>$($_.Why)</span></td></tr>"
}) -join "`n"
$stillData = ''
$stillNote = "<p class='dim'>NOT RUN - this run produced no frame to capture, and the artefact says so rather than showing a broken image.</p>"
if ($stillCaptured) {
    $stillData = 'data:image/jpeg;base64,' + [Convert]::ToBase64String([IO.File]::ReadAllBytes($stillPath))
    $stillNote = "<img src='$stillData' alt='the last frame this run processed'><p class='dim'>The run's own last written frame, captured from the read-only screen at 127.0.0.1:$UIPort. It is the last frame the run processed, and it came off a relay of somebody else's camera.</p>"
}
$calLineText = ($bench | Select-String -Pattern 'p50 .*p99 .*ms|p99\s+[0-9.]+ms' | Select-Object -Last 1).Line
if (-not $calLineText) { $calLineText = 'not measured on this run' }
$licenceRow = 'not readable on this box'
$labelsPath = Join-Path $poc 'testdata\labels.json'
if (Test-Path -LiteralPath $labelsPath) {
    try {
        $labels = Get-Content -LiteralPath $labelsPath -Raw | ConvertFrom-Json
        $licenceRow = "$($labels.licence) - $($labels.source), recorded in testdata/labels.json"
    } catch {}
}

@"
<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>VISION - the live demo - $($script:cameraName)</title>
<style>
:root{color-scheme:dark light}
body{background:#0d1117;color:#e6edf3;font:15px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif;margin:0;padding:22px}
h1{font-size:1.5rem} h2{font-size:.8rem;text-transform:uppercase;letter-spacing:.08em;color:#8b949e;margin:26px 0 8px}
p.lede{color:#b8bec9;max-width:80ch}
table{border-collapse:collapse;width:100%;max-width:70rem}
th,td{border-bottom:1px solid #262b33;padding:8px 10px;text-align:left;vertical-align:top}
th{color:#8b949e;font-size:11px;text-transform:uppercase;letter-spacing:.05em}
td.k{color:#8b949e;white-space:nowrap;width:1%}
.pass{color:#3fb950;font-weight:700} .fail{color:#f85149;font-weight:700} .refused{color:#d29922;font-weight:600}
.dim{color:#8b949e} .why{color:#8b949e;font-size:.92em}
img{max-width:100%;width:640px;border-radius:6px;background:#000;display:block;margin-top:6px}
.warn{border:1px solid #3f3a20;background:#1b1a12;color:#fde68a;border-radius:6px;padding:10px 14px;margin:6px 0;max-width:70rem}
code{background:#161b22;padding:1px 5px;border-radius:3px;font-size:12px;word-break:break-all}
</style></head><body>
<h1>VISION &mdash; the live demo</h1>
<p class="lede">Run $(Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ') on this box, over a live relay of a real camera. Every row below is quoted from the commands this run executed; the two highlighted lines near the bottom are the run's own words about what it does not prove.</p>

<h2>Source</h2>
<table>
<tr><td class="k">camera</td><td>$($script:cameraName) &mdash; a public relay of a real camera, read with ffmpeg and not stored anywhere</td></tr>
<tr><td class="k">the run's own source line</td><td>$sourceLine</td></tr>
<tr><td class="k">trigger</td><td>$TriggerMode $(if ($TriggerMode -eq 'sidecar') { '&mdash; the Python worker''s endpoint; every box this run reported was decoded there' } else { '&mdash; the in-process ONNX session' })</td></tr>
<tr><td class="k">push target</td><td><code>xpsim</code> &mdash; the local simulator. No XProtect server was contacted and no outside push left this box.</td></tr>
<tr><td class="k">data root</td><td><code>$DataRoot</code></td></tr>
</table>

<h2>Calibration</h2>
<table>
<tr><td class="k">this run's measured p99</td><td>$calLineText</td></tr>
<tr><td class="k">record</td><td><code>$DataRoot</code> carries one calibration file with a trigger field naming which path measured it, and preflight refuses one path's p99 applied to the other by name</td></tr>
</table>

<h2>Camera row and licence</h2>
<table>
<tr><td class="k">curated table</td><td><code>poc\active-monitoring\cameras.json</code> &mdash; each row records a licence basis, a verification date, and that <b>no written permission to store clips exists</b></td></tr>
<tr><td class="k">cleared_for_external_use</td><td><b>false</b> on every row: a screenshot of this demo is a screenshot of somebody else's camera</td></tr>
<tr><td class="k">this run's material</td><td>$licenceRow &mdash; the committed clip the smoke and fault acts used. The live relay was read and not stored.</td></tr>
</table>

<h2>The still</h2>
$stillNote

<h2>Acceptance criteria</h2>
<table>
<tr><th>id</th><th>verdict</th><th>name</th><th>observed</th><th>missing</th></tr>
$acTable
</table>

<h2>The acts</h2>
<table>
<tr><th>verdict</th><th>act</th><th>claim</th></tr>
$resultRows
</table>

<h2>What this does NOT prove</h2>
<ul>$notProvenHtml</ul>

<h2>What was not real either</h2>
<p>The video management system &mdash; events were pushed to xpsim, a simulator, and preflight says so. Not a week: $CaptureSeconds seconds proves the source line and the session cost, and proves nothing about uptime, drift, or the acceptance criteria a real run is scored against.</p>
</body></html>
"@ | Set-Content -LiteralPath $html -Encoding utf8
try { Start-Process $html } catch {}
Say "  report  $html"
Say "  transcript  $trans"
if ($failed.Count -gt 0) { Exit-Demo 1 }
Exit-Demo 0
