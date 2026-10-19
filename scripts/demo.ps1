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

function Say($message) { Write-Host "  $message" -ForegroundColor Cyan }
function Warn($message) { Write-Host "  $message" -ForegroundColor Yellow }
function Die($message) { Write-Host "  $message" -ForegroundColor Red; exit 1 }

Write-Host ""
Write-Host "The product: a VMS you can play video into" -ForegroundColor White
Write-Host "============================================" -ForegroundColor White
Write-Host ""

# ---------------------------------------------------------------- the checks
#
# The demo is five minutes long and the checks are twenty seconds, and the
# checks are the part that makes the five minutes repeatable. Every one of them
# is a thing whose absence produces a confusing failure three minutes in.
if (-not $SkipChecks) {
    Write-Host "Checking what the demo needs" -ForegroundColor White
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
Write-Host "Starting the database" -ForegroundColor White
$container = "vision-demo-pg"
$running = docker ps --filter "name=$container" --format '{{.Names}}' 2>$null
if ($running -eq $container) {
    Say "the container is already running"
} else {
    docker rm -f $container 2>$null | Out-Null
    docker run -d --name $container `
        -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=vision_demo `
        -p "${DatabasePort}:5432" pgvector/pgvector:pg16 -c max_connections=300 | Out-Null
    Say "the container is up on port $DatabasePort"
}

$env:SP_DATABASE_URL = "postgres://postgres:postgres@127.0.0.1:${DatabasePort}/vision_demo?sslmode=disable"
$env:SP_TEST_DATABASE_URL = $env:SP_DATABASE_URL
$env:SP_TEST_DATABASE_PASSWORD = "postgres"

# Wait for Postgres to answer, because the migration runs against it and a
# connection refused is a confusing first error.
$ready = $false
for ($i = 0; $i -lt 40; $i++) {
    docker exec $container pg_isready -U postgres -d vision_demo 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Milliseconds 500
}
if (-not $ready) { Die "Postgres did not become ready." }
Say "Postgres is ready"

Write-Host "Migrating" -ForegroundColor White
& go run ./cmd/security-migrate up | Out-Null
if ($LASTEXITCODE -ne 0) { Die "the migrations did not apply." }
Say "the schema is up to date"

# ------------------------------------------------------------------ the VMS
Write-Host "Building the three programs" -ForegroundColor White
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
if ($LASTEXITCODE -ne 0) { Die "the console did not build." }
Push-Location "web/spa"
if (Test-Path -LiteralPath "node_modules") {
    & npm run build | Out-Null
} else {
    Say "the SPA is not built (no node_modules in web/spa). The console will serve the last build."
}
Pop-Location
Say "the three programs are built"

Write-Host "Starting the VMS emulator" -ForegroundColor White
$env:SP_VMS_LISTEN = "127.0.0.1:$VmsPort"
$env:SP_VMS_TOKEN = ""
$emulatorArgs = @(
    "--listen", "127.0.0.1:$VmsPort",
    "--source", "entrance=file:$EntranceVideo",
    "--source", "street=file:$StreetVideo",
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
Write-Host "Starting the product" -ForegroundColor White
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
if (Test-Path -LiteralPath "poc/sitemap.yaml") {
    $env:SP_SITE_MODEL = "poc/sitemap.yaml"
}
$env:SP_MILESTONE_TOKEN = ""
New-Item -ItemType Directory -Path $env:SP_EVIDENCE_ROOT -Force | Out-Null
$daemon = Start-Process -FilePath (Join-Path $bin "security-core") -PassThru -NoNewWindow
Say "the product is starting on port $ApiPort (pid $($daemon.Id))"

# ---------------------------------------------------------------- the console
Write-Host "Starting the console" -ForegroundColor White
$env:SP_API_URL = "http://127.0.0.1:$ApiPort"
$env:SP_VMS_URL = "http://127.0.0.1:$VmsPort"
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
