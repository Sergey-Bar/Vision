<#
.SYNOPSIS
    The verification gate. Runs the same checks as `make verify`, natively.

.DESCRIPTION
    `make` is not installed on this machine - not on PATH, not in Git Bash, not
    in WSL - so `make verify` is a gate nobody can actually pull. This script is
    the same list of checks with no build tool required, so the thing the project
    tells you to trust is a thing that runs.

    The checks, in order, and why each is here:

      contract-check-* the wire has not changed incompatibly, across all three
                       published contract surfaces: protobuf messages and enums,
                       the JSON Schemas' properties and required sets, and the
                       OpenAPI path/method set
      contract-sync    every schema with a Go-side embedded copy is byte-identical
                       to the published one
      vet              Go's own static checks
      gofmt            formatting, without rewriting files
      test-race        the full suite with the race detector, -p 1
      migration-phases every migration declares a phase and no expand ships
                       with its contract step
      rules-lint       the built-in bundle compiles as untrusted input
      rls-canary       the row-level-security canary, which connects as the
                       production role rather than as the test database's
                       owner, because a superuser suite cannot see a missing
                       tenant context
      ai-runtime       the AI runtime's contract in Python

    Deliberately absent: golangci-lint (not installed, and `make ci` covers it
    before a release) and the protoc regeneration check.

    SonarQube is not here because it cannot be: the analysis has to be uploaded
    somewhere that holds the history, and GitHub Actions cannot start a job on
    this account. Run `scripts/sonar.ps1` when a server is reachable. See
    .github/dependabot.yml for the measured reason.

.NOTES
    Run with a database, or the database-backed suites will skip and a green
    result will mean nothing. See AGENTS.md.
#>
[CmdletBinding()]
param(
    # Skip the race detector. Faster, and not what the gate means.
    [switch]$NoRace
)

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

# Gates report through the exit code, and only through the exit code.
#
# With 'Stop' in effect, PowerShell turns anything a native command writes to
# stderr into a terminating error, so a tool that prints a diagnostic — a
# contract mismatch it wants to show you, a deprecation notice, a warning about
# a malformed document it worked around — aborts the whole script before the
# exit code is ever read. The first two gates of this script never wrote to
# stderr, so it went unnoticed for as long as it went unnoticed; adding the
# OpenAPI gate surfaced it on a warning, not on a failure.
#
# 'Continue' is the right setting here precisely because $LASTEXITCODE is the
# signal and stderr is not. A gate that fails still fails, and a diagnostic is
# still printed. What stops happening is a diagnostic being promoted to a
# failure because of where it was written. Anything that genuinely must abort —
# a missing interpreter, an unreadable file — calls exit itself.
$ErrorActionPreference = 'Continue'

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

# Three contract surfaces, because there are three published contract files. The
# protobuf gate ran alone while the JSON Schemas and the OpenAPI document were
# never diffed, so a breaking change to either published nothing. AGENTS.md
# requires `make verify` and this script to be the same list, and the Makefile
# now runs all three; if the two drift again, one of them is lying about what
# the gate checks.
Invoke-Gate 'contract-check-protobuf' { & $go run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/protobuf }
Invoke-Gate 'contract-check-schema' { & $go run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/json-schema }
Invoke-Gate 'contract-check-openapi' { & $go run ./tools/contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/openapi }

# Every published schema with a Go-side embedded copy, not just rule-bundle. The
# six schemas are embedded in internal/contracts/schemasync and byte-compared by
# its own tests; running that package here makes the copy step a gate rather than
# something the full test run happens to cover.
Invoke-Gate 'contract-sync' { & $go test -p 1 -count=1 ./internal/contracts/... }

Invoke-Gate 'vet' { & $go vet ./... }

Invoke-Gate 'gofmt' {
    $unformatted = & $gofmt -l .
    if ($unformatted) {
        Write-Host "    gofmt would reformat:"; $unformatted | ForEach-Object { Write-Host "      $_" }
        exit 1
    }
}

# A single-element array from an `if` expression is unrolled to a scalar, so
# `@race` splatted the *string* '-race' into its characters and the test gate ran
# `go test -p 1 -count=1 - r a c e ./...`. It failed, loudly, which is how this was
# found — but the race detector this gate claims to run has never actually run.
# The comma keeps it an array.
$race = if ($NoRace) { ,@() } else { ,@('-race') }
Invoke-Gate 'test' { & $go test -p 1 -count=1 @race ./... }

Invoke-Gate 'migration-phases' { & $go test -p 1 -count=1 -run TestAllLoadsAndValidates ./migrations/... }

Invoke-Gate 'rules-lint' { & $go run ./cmd/securityctl rules lint --bundle rules/builtin }

# The row-level-security canary is separated from the rest of the suite because
# it is the one that connects as the production role rather than as the test
# database's owner. A superuser suite cannot see a query issued without a tenant
# context: under RLS that returns no rows or is refused, while the superuser sees
# the right answer. It has found six defects that broke the product in
# production and that every other check passed straight through.
Invoke-Gate 'rls-canary' {
    & $go test -p 1 -count=1 -v ./internal/rlsrole/... 2>&1 |
        Select-String -Pattern '^(---|===) (PASS|FAIL|RUN)' |
        ForEach-Object { Write-Host "    $_" }
    if ($LASTEXITCODE -ne 0) { exit 1 }
}

# The AI runtime's contract. Standard library only, and 21 tests over the part of
# it that decides what a degraded observation looks like.
$py = Get-Command py -ErrorAction SilentlyContinue
if ($py) {
    Invoke-Gate 'ai-runtime' { & $py.Source -m unittest discover -s deploy/ai-runtime }
} elseif (Get-Command python -ErrorAction SilentlyContinue) {
    Invoke-Gate 'ai-runtime' { & python -m unittest discover -s deploy/ai-runtime }
} else {
    Write-Host "==> ai-runtime SKIPPED (no Python on PATH)"
    Write-Host "    The AI runtime's contract is unverified. Install Python, or accept that the"
    Write-Host "    degraded-observation path - the product's central invariant - has no test."
}

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
