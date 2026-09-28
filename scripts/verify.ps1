<#
.SYNOPSIS
    The verification gate. Runs the same checks as `make verify`, natively.

.DESCRIPTION
    `make` is not installed on this machine — not on PATH, not in Git Bash, not
    in WSL — so `make verify` is a gate nobody can actually pull. This script is
    the same list of checks with no build tool required, so the thing the project
    tells you to trust is a thing that runs.

    The checks, in order, and why each is here:

      contract-check   the wire has not changed incompatibly
      contract-sync    the embedded rule schema is the published one
      vet              Go's own static checks
      gofmt            formatting, without rewriting files
      test-race        the full suite with the race detector, -p 1
      migration-phases every migration declares a phase and no expand ships
                       with its contract step
      rules-lint       the built-in bundle compiles as untrusted input

    Deliberately absent: golangci-lint (not installed, and `make ci` covers it
    before a release) and the protoc regeneration check.

.NOTES
    Run with a database, or the database-backed suites will skip and a green
    result will mean nothing. See AGENTS.md.
#>
[CmdletBinding()]
param(
    # Skip the race detector. Faster, and not what the gate means.
    [switch]$NoRace
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

# Go is installed but not on PATH. Resolve it rather than assuming.
# Written for Windows PowerShell 5.1: no ?. operator, no ?? coalescing.
$goCmd = Get-Command go -ErrorAction SilentlyContinue
$go = if ($goCmd) { $goCmd.Source } else { $null }
if (-not $go) { $go = 'C:\Program Files\Go\bin\go.exe' }
if (-not (Test-Path -LiteralPath $go)) {
    Write-Error "go not found. Install it, or point -Go at it. See AGENTS.md."
    exit 127
}

# gofmt is a separate binary from the go command, and it is not on PATH either.
$gofmtCmd = Get-Command gofmt -ErrorAction SilentlyContinue
$gofmt = if ($gofmtCmd) { $gofmtCmd.Source } else { $null }
if (-not $gofmt) { $gofmt = Join-Path (Split-Path -Parent $go) 'gofmt.exe' }
if (-not (Test-Path -LiteralPath $gofmt)) {
    Write-Error "gofmt not found next to $go. See AGENTS.md."
    exit 127
}

$failures = [System.Collections.Generic.List[string]]::new()

function Invoke-Gate {
    param([string]$Name, [scriptblock]$Command)
    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan
    & $Command
    if ($LASTEXITCODE -ne 0) {
        $script:failures.Add($Name)
        Write-Host "    FAILED ($Name)" -ForegroundColor Red
    } else {
        Write-Host "    ok" -ForegroundColor DarkGray
    }
}

Invoke-Gate 'contract-check' { & $go run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/protobuf }

Invoke-Gate 'contract-sync' {
    $a = 'contracts/json-schema/rule-bundle.schema.json'
    $b = 'internal/rules/schema/rule-bundle.schema.json'
    if (-not ((Get-Content -LiteralPath $a -Raw) -eq (Get-Content -LiteralPath $b -Raw))) {
        Write-Host "    internal/rules/schema is stale; copy the published contract:"
        Write-Host "      Copy-Item $a $b"
        exit 1
    }
}

Invoke-Gate 'vet' { & $go vet ./... }

Invoke-Gate 'gofmt' {
    $unformatted = & $gofmt -l .
    if ($unformatted) {
        Write-Host "    gofmt would reformat:"; $unformatted | ForEach-Object { Write-Host "      $_" }
        exit 1
    }
}

$race = if ($NoRace) { @() } else { @('-race') }
Invoke-Gate 'test' { & $go test -p 1 -count=1 @race ./... }

Invoke-Gate 'migration-phases' { & $go test -p 1 -count=1 -run TestAllLoadsAndValidates ./migrations/... }

Invoke-Gate 'rules-lint' { & $go run ./cmd/securityctl rules lint --bundle rules/builtin }

Write-Host ""
if ($failures.Count -gt 0) {
    Write-Host "FAILED: $($failures -join ', ')" -ForegroundColor Red
    exit 1
}

Write-Host "All gates passed." -ForegroundColor Green
if (-not $env:SP_TEST_DATABASE_URL) {
    Write-Host ""
    Write-Warning 'SP_TEST_DATABASE_URL was not set, so the database-backed suites skipped.'
    Write-Warning 'This run is not evidence. See AGENTS.md for the setup.'
}
exit 0
