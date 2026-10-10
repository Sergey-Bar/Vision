<#
.SYNOPSIS
    The Security Core's script runner: backend, no binary in the checkout.

.DESCRIPTION
    Starts the core with `go run ./cmd/security-core` and waits for its health
    endpoint. Nothing is compiled into the repository or into an output
    directory: `go run` builds into the toolchain's own cache and runs the
    result, so what an operator keeps is source. This is the script
    replacement for `go build -o security-core.exe ./cmd/security-core` —
    which on a Windows development machine is also the difference between a
    runnable command and a file Windows may block or quarantine.

    The core's own configuration comes from the environment, exactly as it
    does under the installed service: `SP_TENANT_ID`, `SP_SITE_ID`,
    `SP_INTEGRATION_ID` and `SP_EVIDENCE_ROOT` are required by its own
    validation, and this script refuses by name when one is missing rather
    than letting the process die part-way through a boot.

    The console then lives at the URL this script prints, and
    GET /v1/health is what answers.

.PARAMETER Listen
    The address the core binds, matching SP_LISTEN_ADDR. Defaults to
    127.0.0.1:8443.

.PARAMETER Tenant, Site, Integration, Evidence
    Required configuration, passed through as the SP_ variables the core
    reads. Defaults are the ones a lone development checkout can use.

.PARAMETER Open
    Open the console in the default browser once it is up.

.PARAMETER Smoke
    Ask /v1/health one question and stop, for a liveness check that leaves
    neither a server on the port nor a binary on disk.

.PARAMETER Cert
.PARAMETER Key
    A certificate and key for TLS. The core refuses plaintext without a
    declared posture, so one of this pair or -BehindProxy is required.

.PARAMETER BehindProxy
    Declare that a TLS-terminating proxy fronts this process
    (SP_BEHIND_PROXY=true). This is a statement about the deployment, not a
    dev-mode toggle, which is why the script asks for it rather than assuming
    it: asserting a proxy that does not exist is a false claim about the
    world. Requires -TrustedProxy.

.PARAMETER TrustedProxy
    The CIDR blocks the proxy connects from, comma-separated (e.g.
    127.0.0.1/32 for a loopback proxy on this machine). The core refuses
    -BehindProxy without it, because without the list the only client address
    left is the leftmost X-Forwarded-For entry, which the caller wrote.

.EXAMPLE
    .\scripts\run-core.ps1 -Tenant dev -Site site-001 -Integration dev`
        -Evidence .\data\evidence -Smoke

.EXAMPLE
    .\scripts\run-core.ps1            # defaults, console at https://127.0.0.1:8443/
#>
[CmdletBinding()]
param(
    [string]$Listen = '127.0.0.1:8443',
    [string]$Tenant = 'dev-tenant',
    [string]$Site = 'dev-site',
    [string]$Integration = 'dev',
    [string]$Evidence = '',
    [switch]$Open,
    [switch]$Smoke,
    [string]$Cert = '',
    [string]$Key = '',
    [switch]$BehindProxy,
    [string]$TrustedProxy = ''
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    if (-not $Evidence) { $Evidence = Join-Path $repoRoot "data\evidence" }

    # The toolchain is not on PATH on a default Windows shell, which is the
    # first thing that wastes an operator's time.
    $go = Get-Command go -ErrorAction SilentlyContinue
    if (-not $go) {
        foreach ($c in @('C:\Program Files\Go\bin\go.exe', (Join-Path $env:USERPROFILE 'go\bin\go.exe'))) {
            if (Test-Path -LiteralPath $c) { $go = Get-Command $c; break }
        }
    }
    if (-not $go) {
        throw 'The Go toolchain was not found. Install Go 1.26+ (https://go.dev/dl) or put it on PATH.'
    }
    & $go.Source version

    # The core's own required set, refused by name before the process starts.
    $missing = @()
    if (-not $Tenant)        { $missing += 'Tenant (-> SP_TENANT_ID)' }
    if (-not $Site)          { $missing += 'Site (-> SP_SITE_ID)' }
    if (-not $Integration)   { $missing += 'Integration (-> SP_INTEGRATION_ID)' }
    if (-not $Evidence)      { $missing += 'Evidence (-> SP_EVIDENCE_ROOT)' }
    if ($missing.Count -gt 0) {
        throw ('The core will not start without: ' + ($missing -join ', ') + '. tenant_id is never read from a request body, so there is nowhere else for it to learn these.')
    }

    $tlsCert = if ($Cert) { $Cert } elseif ($env:SP_TLS_CERT) { $env:SP_TLS_CERT } else { '' }
    $tlsKey  = if ($Key)  { $Key }  elseif ($env:SP_TLS_KEY)  { $env:SP_TLS_KEY }  else { '' }
    if (($tlsCert -eq '') -ne ($tlsKey -eq '')) {
        throw 'SP_TLS_CERT and SP_TLS_KEY must be set together; one without the other is a certificate with no key.'
    }
    # The operator's string is opaque and is passed through: `config.go`'s
    # boolEnv (1/true/yes/on, case-insensitive) is the single judge, and a
    # runner that re-implements the token set answers differently from the
    # process it starts. The script only needs to know whether to poll http
    # or https, which follows from the certificate, not from the flag.
    $behindProxyRaw = $env:SP_BEHIND_PROXY
    $behindProxy = [bool]$behindProxyRaw
    if (-not $TrustedProxy -and $env:SP_TRUSTED_PROXY_CIDRS) { $TrustedProxy = $env:SP_TRUSTED_PROXY_CIDRS }
    if (-not $tlsCert -and -not $behindProxy) {
        # The core refuses this itself, and it is right to: serving plaintext
        # with nothing upstream declaring TLS is a posture, and the script
        # does not get to choose one on the operator's behalf. The two real
        # choices are named instead.
        throw @'
The core refuses plaintext with no declared TLS posture. Pick one:

  -Cert <file> -Key <file>       serve HTTPS with a certificate
  -BehindProxy -TrustedProxy ..  declare that a TLS-terminating proxy fronts
                                 this process, and the CIDRs it connects from

The second is a statement about the deployment, not a dev-mode toggle.
'@
    }
    if ($behindProxy -and -not $TrustedProxy) {
        throw @'
-BehindProxy needs -TrustedProxy, and the core refuses without it: with no
trusted-proxy list the only client address left is the leftmost X-Forwarded-For
entry, which the caller wrote — so an audit row or a rate limiter keyed on it
would be keyed on something the caller chose. For a loopback proxy on this
machine: -TrustedProxy 127.0.0.1/32
'@
    }
    $scheme = if ($tlsCert) { 'https' } else { 'http' }
    $base = "${scheme}://$Listen"

    # The port is checked before anything starts, so this run's output cannot
    # be another process's.
    $inUse = $false
    try {
        Invoke-WebRequest -Uri "$base/v1/health" -TimeoutSec 2 -UseBasicParsing `
            -SkipCertificateCheck | Out-Null
        $inUse = $true
    } catch {
        $inUse = $false
    }
    if ($inUse) {
        throw "$base is already serving. Stop that process first, or this run will read it instead of its own."
    }

    # The environment is set for the child rather than inherited ad hoc, so a
    # run and the installed service read the same variables the same way.
    $env:SP_LISTEN_ADDR = $Listen
    $env:SP_TENANT_ID = $Tenant
    $env:SP_SITE_ID = $Site
    $env:SP_INTEGRATION_ID = $Integration
    $env:SP_EVIDENCE_ROOT = $Evidence
    if ($behindProxyRaw) { $env:SP_BEHIND_PROXY = $behindProxyRaw }
    elseif ($BehindProxy) { $env:SP_BEHIND_PROXY = 'true' }
    if ($TrustedProxy) { $env:SP_TRUSTED_PROXY_CIDRS = $TrustedProxy }
    if ($tlsCert) { $env:SP_TLS_CERT = $tlsCert }
    if ($tlsKey)  { $env:SP_TLS_KEY  = $tlsKey }
    if (-not $env:SP_HANDOVER_DIR) { $env:SP_HANDOVER_DIR = Join-Path $repoRoot "data\dutylog" }

    Write-Host "==> Security Core backend"
    Write-Host "    starting with: $($go.Source) run ./cmd/security-core"
    Write-Host "    health: $base/v1/health"

    $proc = Start-Process -FilePath $go.Source `
        -ArgumentList 'run', './cmd/security-core' `
        -NoNewWindow -PassThru -RedirectStandardOutput "$env:TEMP\core-backend.log" `
        -RedirectStandardError "$env:TEMP\core-backend.err"

    # `go run`'s own child is the compiled server. Killing the wrapper alone
    # leaves the server on the port, which is the leak this stop path exists
    # for.
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
            Invoke-WebRequest -Uri "$base/v1/health" -TimeoutSec 2 -UseBasicParsing `
                -SkipCertificateCheck | Out-Null
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
        # Readiness is waited for, never slept through: a cold build of the
        # core takes tens of seconds, and a fixed sleep is how a demo ends up
        # measuring a dead port.
        $ready = $false
        foreach ($i in 1..90) {
            if ($proc.HasExited) { throw 'The core exited before it answered /v1/health. Check SP_TENANT_ID, SP_SITE_ID, SP_INTEGRATION_ID and SP_EVIDENCE_ROOT.' }
            try {
                Invoke-WebRequest -Uri "$base/v1/health" -TimeoutSec 2 -UseBasicParsing `
                    -SkipCertificateCheck | Out-Null
                $ready = $true
                break
            } catch {
                Start-Sleep -Seconds 1
            }
        }
        if (-not $ready) {
            Write-Host '--- backend log ---'
            if (Test-Path "$env:TEMP\core-backend.err") { Get-Content "$env:TEMP\core-backend.err" -Tail 20 }
            throw "The core never answered $base/v1/health."
        }

        Write-Host ""
        Write-Host "==> Security Core up."
        Write-Host ""
        # One endpoint file both runners print, so the two platforms cannot
        # print different URLs for the same backend -- the same reason
        # poc-endpoints.txt exists for the POC runner. The port is substituted
        # because the shared list hardcodes 8443.
        $endpointFile = Join-Path $PSScriptRoot "core-endpoints.txt"
        if (Test-Path -LiteralPath $endpointFile) {
            Get-Content -LiteralPath $endpointFile | Where-Object { $_.Trim() -ne "" } | ForEach-Object {
                Write-Host ("    " + ($_ -replace '127\.0\.0\.1:8443', "127.0.0.1:$port"))
            }
        }
        Write-Host ""
        if ($Open) { Start-Process $base }

        if ($Smoke) {
            # A smoke run answers one question and stops. Leaving the server on
            # the port would be the trap the POC runner's old -once fell into.
            Write-Host "==> smoke: one health probe, then the backend stops."
            try {
                $h = Invoke-RestMethod -Uri "$base/v1/health" -TimeoutSec 10 `
                    -UseBasicParsing -SkipCertificateCheck
                Write-Host ("    health: " + ($h | ConvertTo-Json -Compress))
            } catch {
                Write-Host ("    health could not be read: " + $_.Exception.Message)
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
