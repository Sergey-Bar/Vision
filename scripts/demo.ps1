# The demo, start to finish.
#
# One command: Postgres, the product daemon, the VMS emulator and the console.
# The emulator plays the operator's own footage, detects objects on the GPU, and
# publishes what it finds the way a VMS publishes its own analytics. The product
# ingests it through the adapter it already had, applies its rules, opens
# incidents, and fetches the clip for each incident from the emulator's media
# surface. The console shows the cockpit.
#
# There is no Milestone in this and no cloud and no login, and nothing here
# reaches the network: the footage is local, the model is fetched once by a
# digest-pinned command, and the database is a container on loopback.
#
# The five minutes:
#
#   1. The cockpit.         What needs the operator now, and why.
#   2. The live grid.       The VMS's own picture with the VMS's own boxes.
#   3. The search.          "Who was carrying a bag near the entrance?"
#   4. The instant replay.  The clip for the moment the incident was raised.
#   5. The explanation.     Why this, why now, from the product's own rules.
#
# Every step is a URL and a question. The script prints them; the operator
# drives.

#Requires -Version 5.1
[CmdletBinding()]
param(
    # Port the VMS emulator listens on.
    [int]$VmsPort = 8010,
    # Port the product daemon listens on.
    [int]$ApiPort = 8443,
    # Port the console listens on.
    [int]$ConsolePort = 8080,
    # Port the demo's Postgres listens on.
    [int]$DatabasePort = 55432,
    # The footage the entrance camera plays.
    [string]$EntranceVideo = "Videos/Shoppers.mp4",
    # The footage the street camera plays.
    [string]$StreetVideo = "Videos/road1.mp4",
    # Frames per second measured per camera.
    [double]$Fps = 5,
    # Skip the checks and start anyway. The checks are the point; skipping them
    # is for the person who has already run them.
    [switch]$SkipChecks
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot

# The demo's transcript.
#
# Every line the demo prints also goes to this file, as it is printed. A demo
# that leaves a transcript behind can be read after the fact, and a demo whose
# output lives only in a terminal that scrolls away says nothing to the person
# who comes back to ask what happened.
$transcript = Join-Path $PSScriptRoot "..\dist\demo\demo.log"
New-Item -ItemType Directory -Path (Split-Path -Parent $transcript) -Force | Out-Null
function Write-Line($message, $colour) {
    Write-Host $message -ForegroundColor $colour
    Add-Content -LiteralPath $transcript -Value $message
}

function Say($message) { Write-Line "  $message" Cyan }
function Warn($message) { Write-Line "  $message" Yellow }
function Die($message) { Write-Line "  $message" Red; exit 1 }

# Invoke-Native runs a native command and returns its exit code.
#
# The error preference is relaxed for the call and restored after it, because a
# native command that writes to stderr under `Stop` is a terminating error: the
# migration's own output, Vite's chunk-size advice and a program's diagnostics
# all go to stderr, and every one of them would stop the demo before it started
# and hide the reason. The exit code is the answer, and the demo checks it.
#
# The command's OUTPUT is captured rather than returned, because a function that
# returns everything its scriptblock wrote returns the output AND the exit code
# -- and an array compared with `ne 0` is never equal to it, so the demo would
# report a failure that had not happened.
function Invoke-Native {
    param([scriptblock]$Command)
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $Command 2>&1
        $code = $LASTEXITCODE
        foreach ($line in @($output)) {
            Add-Content -LiteralPath $transcript -Value "    $line"
        }
        $output | Out-Host
        return $code
    } finally {
        $ErrorActionPreference = 'Stop'
    }
}

Write-Host ""
Write-Line "The product: a VMS you can play video into" White
Write-Line "============================================" White
Write-Host ""

# ---------------------------------------------------------------- the checks
#
# The demo is five minutes long and the checks are twenty seconds, and the
# checks are the part that makes the five minutes repeatable. Every one of them
# is a thing whose absence produces a confusing failure three minutes in.
if (-not $SkipChecks) {
    Write-Line "Checking what the demo needs" White
    Say "ffmpeg"
    if (-not (Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
        Die "ffmpeg is not on PATH. The emulator decodes and cuts video with it, and there is no second demuxer here."
    }
    Say "docker"
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        Die "docker is not on PATH. The demo's database is a container on loopback."
    }
    Say "the footage"
    foreach ($video in @($EntranceVideo, $StreetVideo)) {
        if (-not (Test-Path -LiteralPath $video)) {
            Die "$video is not here. The demo runs on the operator's own footage, and the footage is not in the repository: put it in Videos/ and point --entrance and --street at it."
        }
    }
    Say "the model"
    if (-not (Test-Path -LiteralPath "models/yolo11n.onnx")) {
        Say "fetching the detector model (once)"
        & go run ./cmd/vms-emulator fetch-model --name yolo11n.onnx --out models
        if ($LASTEXITCODE -ne 0) { Die "the model could not be fetched. It is digest-pinned; the download is a network fetch and the demo itself is not." }
    }
    Say "the ONNX Runtime"
    $ortLib = $null
    if ($env:SP_ORT_LIB -and (Test-Path -LiteralPath $env:SP_ORT_LIB)) {
        $ortLib = $env:SP_ORT_LIB
    } elseif (Test-Path -LiteralPath "poc/active-monitoring/models/onnxruntime.dll") {
        $ortLib = "poc/active-monitoring/models/onnxruntime.dll"
    }
    if (-not $ortLib) {
        Warn "no ONNX Runtime library found. Run `vms-emulator fetch-runtime --url <release asset>` and set SP_ORT_LIB."
        Warn "The demo will start without a detector: the emulator plays the video and publishes nothing, and the console says so."
    }
    Write-Host ""
}

# ----------------------------------------------------------------- the database
Write-Line "Starting the database" White
$container = "vision-demo-pg"
# A FRESH database every time, and the container is the demo's own --
# `vision-demo-pg` -- so nothing outside the demo is touched.
#
# The seed refuses to delete a disposition, and it is right to: a reviewer's
# answer is a record, and a record that can be removed is not one. A demo that
# inherited yesterday's dispositions would be a demo about yesterday, so the
# database starts empty every time. Anything the operator wants to keep lives
# in a database with a different name.
docker rm -f $container 2>$null | Out-Null
docker run -d --name $container `
    -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=vision_demo `
    -p "${DatabasePort}:5432" pgvector/pgvector:pg16 -c max_connections=300 | Out-Null
Say "the container is up on port $DatabasePort"

$env:SP_DATABASE_URL = "postgres://postgres:postgres@127.0.0.1:${DatabasePort}/vision_demo?sslmode=disable"
$env:SP_TEST_DATABASE_URL = $env:SP_DATABASE_URL
$env:SP_TEST_DATABASE_PASSWORD = "postgres"

# Wait for the container to be RUNNING.
#
# Not for its log, and not with `pg_isready`.
#
# `docker logs` on a container that has not started yet BLOCKS: the log endpoint
# waits for the container, so the demo waited for the log while the log waited
# for the container, and neither of them was ever going to hear from the other.
#
# `pg_isready` is not enough either: during the image's INIT phase a temporary
# server answers on the container's socket and `pg_isready` reports it ready --
# and then that server shuts down and the real one starts, so a client that
# connects in that window gets "unexpected EOF".
#
# So the wait is on the container's STATE, and the readiness of the server is
# the migration's own retry.
$running = $false
for ($i = 0; $i -lt 60; $i++) {
    $state = docker inspect -f '{{.State.Running}}' $container 2>$null
    if ("$state".Trim() -eq "true") { $running = $true; break }
    Start-Sleep -Milliseconds 500
}
if (-not $running) { Die "the database container did not start; docker says why: $(docker logs --tail 20 $container 2>&1 | Out-String)" }
Say "the container is running"

Write-Line "Migrating" White
# The migration, retried.
#
# The container's state says it is RUNNING and the server inside it is still
# finishing its init phase, and the first connection in that window is refused
# with "unexpected EOF". A retry is the right answer: the database is coming, and
# the migration is idempotent.
$migrateExit = 1
for ($attempt = 1; $attempt -le 10; $attempt++) {
    $migrateExit = Invoke-Native { & go run ./cmd/security-migrate up | Out-Null }
    if ($migrateExit -eq 0) { break }
    Say "the database is not answering yet (attempt $attempt)"
    Start-Sleep -Seconds 2
}
if ($migrateExit -ne 0) { Die "the migrations did not apply." }
Say "the schema is up to date"

# ------------------------------------------------------------------ the VMS
Write-Line "Building the three programs" White
#
# Built, not `go run`. A `go run` compiles on the way in, and the operator is
# watching a blank screen while it does; the first build of the day is a
# minute, and the demo's first minute is the one the operator is judging.
$bin = Join-Path $repoRoot "dist/demo"
New-Item -ItemType Directory -Path $bin -Force | Out-Null
& go build -o (Join-Path $bin "vms-emulator") ./cmd/vms-emulator
if ($LASTEXITCODE -ne 0) { Die "the VMS emulator did not build." }
& go build -o (Join-Path $bin "security-core") ./cmd/security-core
if ($LASTEXITCODE -ne 0) { Die "the product did not build." }
& go build -o (Join-Path $bin "console") ./web
if ($LASTEXITCODE -ne 0) { Die "the console did not build." }Push-Location "web/spa"
if (Test-Path -LiteralPath "node_modules") {
    # The build's own WARNINGS go to stderr, and a native command that writes to
    # stderr under `Stop` is a terminating error. So Vite's advice about chunk
    # sizes killed the demo half way through its own build.
    $ErrorActionPreference = 'Continue'
    & npm run build | Out-Null
    $buildExit = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
    if ($buildExit -ne 0) { Die "the console's front end did not build." }
} else {
    Say "the SPA is not built (no node_modules in web/spa). The console will serve the last build."
}
Pop-Location
Say "the three programs are built"

Write-Line "Starting the VMS emulator" White
$env:SP_VMS_LISTEN = "127.0.0.1:$VmsPort"
# The VMS's credential, and the product's copy of it.
#
# They are the same string and they are NOT empty: the adapter REFUSES to poll a
# vendor with no credential -- a request with an empty bearer is a request with no
# identity, and the answer would be a 401 that reads like a wrong URL. The
# console's proxy adds the same credential to the browser's VMS requests, so the
# browser never holds it.
$env:SP_VMS_TOKEN = "demo-vms-token"
$env:SP_MILESTONE_TOKEN = $env:SP_VMS_TOKEN
$emulatorArgs = @(
    "--listen", "127.0.0.1:$VmsPort",
    "--source", "CAM-001=file:$EntranceVideo",
    "--source", "CAM-014=file:$StreetVideo",
    "--fps", "$Fps"
)
if (Test-Path -LiteralPath "models/yolo11n.onnx") {
    $emulatorArgs += @("--model", "models/yolo11n.onnx")
}
if ($ortLib) {
    $emulatorArgs += @("--ort-lib", $ortLib)
}
$emulator = Start-Process -FilePath (Join-Path $bin "vms-emulator") -ArgumentList $emulatorArgs -PassThru -NoNewWindow
Say "the emulator is starting on port $VmsPort (pid $($emulator.Id))"

# Wait for the vendor surface, because the daemon polls it and the first poll
# against a closed port is a connection error in a log nobody is watching.
$up = $false
for ($i = 0; $i -lt 60; $i++) {
    try {
        Invoke-RestMethod -Uri "http://127.0.0.1:$VmsPort/api/rest/v1/Servlets" -TimeoutSec 2 | Out-Null
        $up = $true
        break
    } catch { Start-Sleep -Milliseconds 500 }
}
if ($up) { Say "the vendor surface is answering" } else { Warn "the vendor surface has not answered yet; the daemon will retry" }

# ---------------------------------------------------------------- the product
Write-Line "Starting the product" White
$env:SP_TENANT_ID = "demo"
$env:SP_SITE_ID = "demo-site"
$env:SP_INTEGRATION_ID = "milestone"
$env:SP_EVIDENCE_ROOT = Join-Path $repoRoot "dist/demo-evidence"
$env:SP_LISTEN_ADDR = "127.0.0.1:$ApiPort"
# The site root, NOT a path that ends in the API prefix. The adapter appends
# `/api/rest/v1` itself, and a base that already carries it asks for
# `/api/api/rest/v1/events` -- which is a working system that sees nothing.
$env:SP_MILESTONE_BASE_URL = "http://127.0.0.1:$VmsPort"
# The operator's own drawing of the site: the zones, the cameras and the
# routes. The console draws it, and the live state comes from the VMS.
#
# It is the demo's own drawing and not the POC's, because its camera ids are the
# SEEDED devices -- an event that names a device the platform does not know is an
# event with no zone, and a rule that filters on a zone is then unreachable.
if (Test-Path -LiteralPath "deploy/demo-site.yaml") {
    $env:SP_SITE_MODEL = "deploy/demo-site.yaml"
}
$env:SP_MILESTONE_TOKEN = $env:SP_VMS_TOKEN
# The product refuses to serve plaintext with nothing in front of it, and it is
# right to: the API's bearer credentials and the site's evidence would be on
# the wire in the clear.
#
# The CONSOLE is in front of it -- the console proxies /v1 to the product so the
# browser never holds a credential -- and the console is on loopback, so the
# forwarded-for headers come from a trusted peer and the pair is safe to
# configure.
$env:SP_BEHIND_PROXY = "true"
$env:SP_TRUSTED_PROXY_CIDRS = "127.0.0.1/32,::1/128"
# The dispatch destinations.
#
# The bundle's rules ask for a notification, and a notification with nowhere to
# go is a DEAD LETTER: the product refuses to start rather than accept a rule
# whose dispatch it cannot deliver, because a dead letter discovered at 03:00 by
# whoever is on call is worse than a refusal at 09:00 by whoever is deploying.
#
# The file is the one in the repository, so the demo dispatches where the
# repository says it dispatches.
if (Test-Path -LiteralPath "deploy/dispatch-destinations.yaml") {
    $env:SP_DISPATCH_DESTINATIONS = "deploy/dispatch-destinations.yaml"
}
New-Item -ItemType Directory -Path $env:SP_EVIDENCE_ROOT -Force | Out-Null

# The rule bundle, activated.
#
# The product makes NO DECISION without an active bundle -- every event it
# ingests is normalised and then dropped, with a line in the log saying so. So
# the demo activates the bundle, and it activates it the way an operator does:
# the simulation produces a report, and the report is what gets activated. The
# digest is not typed on the command line and the report is not edited: a
# bundle that was activated without being simulated is a bundle nobody checked.
# The rule bundle, activated.
#
# The product makes NO DECISION without an active bundle -- every event it
# ingests is normalised and then dropped, with a line in the log saying so. So
# the demo seeds the site, and the seed is what stores AND activates the bundle
# through the same simulation report the operator would review.
Write-Line "Seeding the site and activating the rule bundle" White
$report = Join-Path $repoRoot "dist/demo/simulation-report.json"
# The report is written as UTF-8 with NO BOM.
#
# PowerShell's `>` writes UTF-16, and a UTF-16 report is not a JSON report: the
# activation re-reads it and refuses it, with a message about an invalid
# character that says nothing about the encoding.
$simulateOutput = & go run ./cmd/securityctl rules simulate --bundle rules/builtin/bundle.yaml --fixtures testdata/fixtures --json
if ($LASTEXITCODE -ne 0) { Die "the simulation did not run; the bundle cannot be activated without it." }
[System.IO.File]::WriteAllText($report, ($simulateOutput -join "`n"), (New-Object System.Text.UTF8Encoding $false))
$simulated = Get-Content -Raw -LiteralPath $report | ConvertFrom-Json
# The seed creates the tenant, the site, the devices and the zones, and it stores
# and activates the bundle. The devices and zones are the FIXTURES' topology --
# the same table the simulation replays over -- so the zones a rule filters on
# are the zones the seeded devices are in.
$secretKey = Join-Path $repoRoot "dist/demo/secret.key"
# The demo's own scratch key, cleared before the seed. The seed REFUSES to
# overwrite an existing key, and it is right to: a key that is replaced is a
# factor that cannot be unwrapped any more. The key is the demo's, in the
# demo's own directory, and the demo recreates it every time.
Remove-Item -LiteralPath $secretKey -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path (Split-Path -Parent $secretKey) -Force | Out-Null
# The MFA wrapping key, which the seed writes and the product reads. They are
# the same file: an install that enrolled a factor with one key and serves with
# another cannot verify the factor it enrolled.
$env:SP_SECRET_KEY = $secretKey
# The dispatch token, so a notification is attempted rather than refused for
# want of a credential. There is no operator behind the example URL, so the
# delivery fails and the ledger says so -- which is the honest outcome for a
# demo with nobody to notify.
$env:SP_DISPATCH_OPERATOR_TOKEN = "demo-dispatch-token"
$seedExit = Invoke-Native { & go run ./cmd/security-seed `
    --tenant $env:SP_TENANT_ID `
    --site $env:SP_SITE_ID `
    --integration $env:SP_INTEGRATION_ID `
    --evidence-root $env:SP_EVIDENCE_ROOT `
    --secret-key $secretKey `
    --console-tls (Join-Path $repoRoot "dist/demo/console-tls") `
    --bundle rules/builtin `
    --fixtures testdata/fixtures `
    --passphrase "demo-operator-passphrase" `
    --reset }
if ($seedExit -ne 0) { Die "the seed did not run; there is no site, no devices and no active bundle." }
Say "the site is seeded and bundle $($simulated.bundleVersion) is active"

$daemon = Start-Process -FilePath (Join-Path $bin "security-core") -PassThru -NoNewWindow
Say "the product is starting on port $ApiPort (pid $($daemon.Id))"

# ---------------------------------------------------------------- the console
Write-Line "Starting the console" White
$env:SP_API_URL = "http://127.0.0.1:$ApiPort"
$env:SP_VMS_URL = "http://127.0.0.1:$VmsPort"
# The console's proxy adds the VMS's credential to the browser's requests, so
# the browser never holds it. The product's copy of the same credential is
# SP_MILESTONE_TOKEN above.
$env:SP_VMS_TOKEN = "demo-vms-token"
$console = Start-Process -FilePath (Join-Path $bin "console") -PassThru -NoNewWindow -WorkingDirectory $repoRoot
Say "the console is starting on port $ConsolePort (pid $($console.Id))"

# ------------------------------------------------------------------ the walk
Write-Host ""
Write-Host "The demo" -ForegroundColor White
Write-Host "----------" -ForegroundColor White
Write-Host ""
Write-Host "  Cockpit          http://127.0.0.1:$ConsolePort/" -ForegroundColor Green
Write-Host "  Live grid        http://127.0.0.1:$ConsolePort/live" -ForegroundColor Green
Write-Host "  Incidents        http://127.0.0.1:$ConsolePort/incidents" -ForegroundColor Green
Write-Host "  Identity         http://127.0.0.1:$ConsolePort/users" -ForegroundColor Green
Write-Host "  The ledger       http://127.0.0.1:$ConsolePort/audit" -ForegroundColor Green
Write-Host ""
Write-Host "  The five minutes:"
Write-Host "    1. The cockpit. What needs the operator now, and why."
Write-Host "       The attention queue is the PRODUCT's ranking, with the reason chain open."
Write-Host "    2. The live grid. The VMS's own picture with the VMS's own boxes."
Write-Host "       A person with a bag is a class the VMS asserted, not a shape the browser drew."
Write-Host "    3. The search. Type: who was carrying a bag near the entrance?"
Write-Host "       The deterministic path searches the canonical record. It returns moments."
Write-Host "    4. The instant replay. Open the incident, then its evidence."
Write-Host "       The product asked the VMS for the clip around the moment and stored it."
Write-Host "    5. The explanation. Why this, why now, from the product's own rules."
Write-Host ""
Write-Host "  To check the seam from the outside:"
Write-Host "    go run ./cmd/securityctl adapter-probe --base-url http://127.0.0.1:$VmsPort" -ForegroundColor Green
Write-Host ""
Write-Host "  Ctrl-C stops the script. The three processes are children of this shell," -ForegroundColor DarkGray
Write-Host "  so they stop with it." -ForegroundColor DarkGray
Write-Host ""

try {
    while ($true) { Start-Sleep -Seconds 5 }
} finally {
    Write-Host ""
    Write-Host "Stopping" -ForegroundColor White
    foreach ($proc in @($console, $daemon, $emulator)) {
        if ($proc -and -not $proc.HasExited) {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        }
    }
    Say "the console, the product and the emulator are stopped"
    Say "the database container is still up: docker rm -f $container"
}
Pop-Location
