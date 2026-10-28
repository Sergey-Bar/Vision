<#
.SYNOPSIS
    Runs the console frontend locally and opens the Hafnia demo page in the default browser.

.DESCRIPTION
    Builds the SPA (web/spa) and starts `go run ./web`, which serves the
    embedded console under /console on http://127.0.0.1:8080. Once the
    demo page answers, the script opens the default browser at:

        http://127.0.0.1:8080/console/demo-hafnia

    The whole page runs the integration bridge in-process against synthetic
    fixtures and a local Hafnia test server, so nothing here needs a VMS, a
    database, or any network egress. Every byte is marked SIMULATED.

    Press Ctrl-C to stop. The server is a child of this shell and stops with it.

.PARAMETER Port
    The port the console listens on. Defaults to 8080.

.EXAMPLE
    .\scripts\open-demo.ps1

.EXAMPLE
    .\scripts\open-demo.ps1 -Port 8090
#>
[CmdletBinding()]
param(
    [int]$Port = 8080
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot

# Locate the Go toolchain (not on PATH on a default Windows shell).
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    foreach ($c in @('C:\Program Files\Go\bin\go.exe', (Join-Path $env:USERPROFILE 'go\bin\go.exe'))) {
        if (Test-Path -LiteralPath $c) { $go = Get-Command $c; break }
    }
}
if (-not $go) {
    throw 'The Go toolchain was not found. Install Go 1.26+ (https://go.dev/dl) or put it on PATH.'
}

# Build the SPA so the console embeds the real bundle (not the placeholder).
if (Get-Command npm -ErrorAction SilentlyContinue) {
    Push-Location 'web/spa'
    try {
        Write-Host "==> Building the SPA" -ForegroundColor White
        $ErrorActionPreference = 'Continue'
        & npm run build | Out-Null
        $buildExit = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($buildExit -ne 0) {
            throw 'The SPA did not build.'
        }
    } finally {
        Pop-Location
    }
} else {
    Write-Warning "npm is not on PATH. The console will serve the last SPA build (or the placeholder if none exists)."
}

$base = "http://127.0.0.1:$Port"
$demoUrl = "$base/console/demo-hafnia"

# Make sure nothing is already serving on this port.
try {
    Invoke-WebRequest -Uri "$base/v1/health" -TimeoutSec 2 -UseBasicParsing | Out-Null
    throw "$base is already serving. Stop that process first."
} catch {
    # Not serving yet - that is what we want.
}

# Start the console server. `go run` compiles into the toolchain cache and runs
# the result; nothing is written into the repository.
Write-Host "==> Starting the console on $base" -ForegroundColor White
Write-Host "    $($go.Source) run ./web" -ForegroundColor DarkGray

$proc = Start-Process -FilePath $go.Source -ArgumentList 'run', './web' -NoNewWindow -PassThru `
    -RedirectStandardOutput "$env:TEMP\vision-console.out" `
    -RedirectStandardError "$env:TEMP\vision-console.err"

$stopping = $false
$stop = {
    if ($script:stopping) { return }
    $script:stopping = $true
    try {
        $children = Get-CimInstance Win32_Process -Filter "ParentProcessId = $($proc.Id)" -ErrorAction SilentlyContinue
        foreach ($child in $children) { Stop-Process -Id $child.ProcessId -Force -ErrorAction SilentlyContinue }
    } catch {}
    if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    Write-Host ""
    Write-Host "==> Console stopped." -ForegroundColor White
}
$null = Register-EngineEvent -SourceIdentifier PowerShell.Exiting -Action $stop

try {
    # Wait for readiness by polling the demo page itself (not a fixed sleep).
    $ready = $false
    foreach ($i in 1..90) {
        if ($proc.HasExited) {
            Write-Host '--- console stderr ---'
            if (Test-Path "$env:TEMP\vision-console.err") { Get-Content "$env:TEMP\vision-console.err" -Tail 30 }
            throw 'The console exited before it answered.'
        }
        try {
            Invoke-WebRequest -Uri $demoUrl -TimeoutSec 2 -UseBasicParsing | Out-Null
            $ready = $true
            break
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    if (-not $ready) {
        throw "The console never answered $demoUrl."
    }

    Write-Host ""
    Write-Host "==> Console up." -ForegroundColor White
    Write-Host "    Open the Hafnia demo: $demoUrl" -ForegroundColor Green
    Write-Host ""

    # Open the frontend in the default browser.
    Start-Process $demoUrl

    Write-Host "    Ctrl-C stops the console." -ForegroundColor DarkGray
    Wait-Process -Id $proc.Id
} finally {
    & $stop
    $null = Unregister-Event -SourceIdentifier PowerShell.Exiting -ErrorAction SilentlyContinue
} finally {
    Pop-Location
}
