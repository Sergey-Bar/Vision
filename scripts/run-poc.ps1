<#
.SYNOPSIS
    The POC's script runner: backend and frontend, no binary in the checkout.

.DESCRIPTION
    Starts the backend with `go run ./poc` and prints the frontend's URL.
    Nothing is compiled into the repository or into an output directory:
    `go run` builds into the toolchain's own cache and runs the result, so what
    an operator keeps is source. This is the "backend and frontend as scripts"
    shape - the backend is this runner, the frontend is the dashboard it points at.

    Why the cleanup is spelled out: `go run`'s own child is the compiled server,
    and killing the `go run` wrapper alone leaves that server holding the port.
    The stop path below kills both, and reports when it cannot.

.PARAMETER Mode
    demo (the simulator), auto (the configured cameras) or live.

.PARAMETER Config
    The config file. Defaults to poc/config.yaml.

.PARAMETER Open
    Open the dashboard in the default browser once it is up.

.PARAMETER Smoke
    Ask the investigator one question and stop the backend, for a check that
    the pipeline is alive without leaving a server on the port. The shell
    runner's -smoke does the same thing.

.EXAMPLE
    .\scripts\run-poc.ps1
    .\scripts\run-poc.ps1 -Mode auto -Open
#>
[CmdletBinding()]
param(
    [ValidateSet('demo', 'auto', 'live')]
    [string]$Mode = 'demo',
    [string]$Config = 'poc/config.yaml',
    [switch]$Open,
    [switch]$Smoke
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    # The port the POC binds, read from its own `web.port` so the busy check,
    # the readiness loop and the printed URLs all come from the value the
    # server binds. `websocket.port` sits above it in the same file, so the
    # match is anchored to the web block rather than taking the first port in
    # the file.
    $port = 8082
    $cfgPath = Join-Path $repoRoot "poc\config.yaml"
    if (Test-Path -LiteralPath $cfgPath) {
        $inWeb = $false
        foreach ($line in [System.IO.File]::ReadAllLines($cfgPath)) {
            if ($line -match '^[a-zA-Z]') { $inWeb = ($line -match '^web:') }
            if ($inWeb -and $line -match '^\s*port:\s*(\d+)') { [int]$port = $Matches[1]; break }
        }
    }
    $base = "http://127.0.0.1:$port"

    # The toolchain is not on PATH on a default Windows shell, which is the first
    # thing that wastes an operator's time. Resolve it the way scripts/verify.ps1
    # does, and name the install command when it is genuinely absent.
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) {
        $candidates = @(
            'C:\Program Files\Go\bin\go.exe',
            (Join-Path $env:USERPROFILE 'go\bin\go.exe')
        )
        foreach ($c in $candidates) {
            if (Test-Path -LiteralPath $c) { $go = Get-Command $c; break }
        }
    }
    if (-not $go) {
        throw 'The Go toolchain was not found. Install Go 1.26+ (https://go.dev/dl) or put it on PATH.'
    }
    & $go.Source version

    # The port is checked before anything starts: a leftover server means this
    # run's output would be that process's, not this build's.
    $inUse = $false
    try {
        Invoke-WebRequest -Uri "$base/api/healthz" -TimeoutSec 2 -UseBasicParsing | Out-Null
        $inUse = $true
    } catch {
        $inUse = $false
    }
    if ($inUse) {
        throw "$base is already serving. Stop that process first, or this run will read it instead of this build."
    }

    Write-Host "==> POC backend (mode=$Mode, config=$Config)"
    Write-Host "    starting with: $($go.Source) run ./poc -config $Config -mode $Mode"

    $proc = Start-Process -FilePath $go.Source `
        -ArgumentList 'run', './poc', '-config', $Config, '-mode', $Mode `
        -NoNewWindow -PassThru -RedirectStandardOutput "$env:TEMP\poc-backend.log" `
        -RedirectStandardError "$env:TEMP\poc-backend.err"

    # `go run`'s own child is the compiled server. Killing the wrapper alone
    # leaves the server on the port, which is the leak this stop path exists for.
    $stopping = $false
    $stop = {
        if ($script:stopping) { return }
        $script:stopping = $true
        try {
            $children = Get-CimInstance Win32_Process -Filter "ParentProcessId = $($proc.Id)" -ErrorAction SilentlyContinue
            foreach ($child in $children) { Stop-Process -Id $child.ProcessId -Force -ErrorAction SilentlyContinue }
        } catch {
        }
        if (-not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
        $still = $false
        try {
            Invoke-WebRequest -Uri "$base/api/healthz" -TimeoutSec 2 -UseBasicParsing | Out-Null
            $still = $true
        } catch {
            $still = $false
        }
        if ($still) {
            Write-Warning "$base is still serving after the stop; the process leaked."
        }
    }
    $null = Register-EngineEvent -SourceIdentifier PowerShell.Exiting -Action $stop
    try {
        # Readiness is waited for, never slept through: a cold build of ./poc
        # takes tens of seconds, and a fixed sleep is how a demo measures a dead
        # port and records it as a pass.
        $ready = $false
        foreach ($i in 1..90) {
            if ($proc.HasExited) { throw 'The backend exited before it answered /api/healthz. See the log below.' }
            try {
                Invoke-WebRequest -Uri "$base/api/healthz" -TimeoutSec 2 -UseBasicParsing | Out-Null
                $ready = $true
                break
            } catch {
                Start-Sleep -Seconds 1
            }
        }
        if (-not $ready) {
            Write-Host '--- backend log ---'
            if (Test-Path "$env:TEMP\poc-backend.err") { Get-Content "$env:TEMP\poc-backend.err" -Tail 20 }
            throw "The backend never answered $base/api/healthz."
        }

        Write-Host ""
        Write-Host "==> POC backend up (mode=$Mode)."
        Write-Host ""
        # The endpoint list is one file both runners print, so the two
        # platforms cannot print different URLs for the same backend. The
        # port is substituted because the shared list hardcodes 8082.
        $endpointFile = Join-Path $PSScriptRoot "poc-endpoints.txt"
        if (Test-Path -LiteralPath $endpointFile) {
            Get-Content -LiteralPath $endpointFile | Where-Object { $_.Trim() -ne "" } | ForEach-Object {
                Write-Host ("    " + ($_ -replace '127\.0\.0\.1:8082', "127.0.0.1:$port"))
            }
        }
        Write-Host ""
        if ($Open) { Start-Process $base }

        if ($Smoke) {
            # One question, then the stop path takes the backend down. A
            # smoke run that left the server on the port would be the same
            # trap the shell runner's old -once fell into.
            Write-Host "==> smoke: one question, then the backend stops."
            try {
                $body = @{ question = 'which cameras are degraded and why?' } | ConvertTo-Json
                $ans = Invoke-RestMethod -Uri "$base/api/investigate" -Method Post `
                    -ContentType 'application/json' -Body $body -TimeoutSec 60
                if ($ans -and $ans.answer) {
                    Write-Host ("    answer: " + $ans.answer)
                    if ($ans.citations) { Write-Host ("    cited: " + ($ans.citations -join ', ')) }
                } else {
                    $reason = if ($ans -and $ans.degraded_reason) { $ans.degraded_reason } else { "no answer and no reason" }
                    Write-Host ("    degraded: " + $reason)
                }
            } catch {
                Write-Host ("    the investigator could not be reached: " + $_.Exception.Message)
            }
            Write-Host ""
            return
        }

        Write-Host "    Ctrl-C stops the backend and the compiled server behind it."

        # Wait on the wrapper; the child is stopped by the stop path above.
        Wait-Process -Id $proc.Id
    } finally {
        & $stop
        $null = Unregister-Event -SourceIdentifier PowerShell.Exiting -ErrorAction SilentlyContinue
    }
} finally {
    Pop-Location
}
