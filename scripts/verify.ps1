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
      lint             golangci-lint, including gosec. Skipped with a visible
                       warning where the binary is absent — never a quiet pass
      test-race        the full suite with the race detector, -p 1
      migration-phases every migration declares a phase and no expand ships
                       with its contract step
      generated-code  the checked-in .pb.go is what protoc produces from the
                       published .proto. Skipped with a visible warning where
                       protoc is absent
      rules-lint       the built-in bundle compiles as untrusted input
      recovery-drill   the whole-recovery drill: database gone, checkpoint
                       material gone, decision still replayable
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

# The race detector needs cgo, which needs a C compiler. Without one,
# `go test -race` fails to build and the whole gate stops — so the toolchain is
# located here and the gate says plainly which mode it ran in rather than
# reporting a pass it did not earn.
#
# MinGW-w64 is installed at C:\msys64 by the sweep that made this gate real. It
# is put on PATH for this process only; nothing outside it is changed.
$mingw = 'C:\msys64\mingw64\bin'
if (Test-Path -LiteralPath $mingw) { $env:Path = "$mingw;$env:Path" }
$haveCC = [bool](Get-Command gcc -ErrorAction SilentlyContinue)
$env:CGO_ENABLED = if ($haveCC) { '1' } else { '0' }

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

# The linter is a gate now, not a suggestion.
#
# `.golangci.yml` has enabled bodyclose, errorlint, gocritic, gosec, noctx,
# revive, unparam and seven others since before this repository had a second
# commit, and it had never been run. 526 findings came out of the first run, and
# among them were two real defects a reviewer would have missed: an environment
# file carrying SP_SECRET_KEY written world-readable, and a backoff expression
# that raised on its own failure path at the 31st attempt.
#
# A linter that is not in the gate is a linter nobody has decided to run, which
# is what this comment used to be able to say and no longer can. It is skipped —
# loudly, and as a skip rather than a pass — where the binary is absent, because
# a gate that quietly turns itself off is the thing this whole revision is about.
$golangciCmd = Get-Command golangci-lint -ErrorAction SilentlyContinue
$golangci = if ($golangciCmd) { $golangciCmd.Source } else { $null }
if (-not $golangci) {
    $candidate = Join-Path $env:USERPROFILE 'go\bin\golangci-lint.exe'
    if (Test-Path -LiteralPath $candidate) { $golangci = $candidate }
}
if (-not $golangci) {
    Write-Host "    SKIPPED: golangci-lint is not installed. The linter gate did not run; this is" -ForegroundColor Yellow
    Write-Host "    not a pass. Install it with 'go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest'." -ForegroundColor Yellow
} else {
    Invoke-Gate 'lint' { & $golangci run --timeout 15m }
}

# A single-element array from an `if` expression is unrolled to a scalar, so
# `@race` splatted the *string* '-race' into its characters and the test gate ran
# `go test -p 1 -count=1 - r a c e ./...`. It failed, loudly, which is how this was
# found — but the race detector this gate claims to run has never actually run.
# The comma keeps it an array.
#
# It runs now, and it runs for real: cgo is enabled above when a C compiler is
# present, which is what `-race` needs. Where there is no compiler the flag is
# dropped and the omission is stated, rather than the gate reporting a race-free
# suite it never built with the detector.
$race = @()
if (-not $NoRace) {
    if ($haveCC) {
        $race = @('-race')
    } else {
        Write-Host "    NOTE: no C compiler, so -race was not used. CGO_ENABLED=0 and the" -ForegroundColor Yellow
        Write-Host "    race detector is skipped. Install MinGW-w64 at C:\msys64 to run it." -ForegroundColor Yellow
    }
}
Invoke-Gate 'test' { & $go test -p 1 -count=1 @race ./... }

Invoke-Gate 'migration-phases' { & $go test -p 1 -count=1 -run TestAllLoadsAndValidates ./migrations/... }

# The generated code is checked against the contracts, not just assumed to match.
#
# `internal/contracts/securityv1` is generated by protoc from `contracts/protobuf`.
# Nothing asserted that the checked-in copy was the current output, so a change to
# a .proto could land with the generated Go left behind, and the wire format the
# product actually speaks would be the old one while the contract says otherwise.
#
# protoc is installed via winget but lands inside a versioned WinGet package
# directory rather than on PATH, so it is located the same way golangci-lint is.
# Where it is genuinely absent the gate says so rather than passing.
$protocCmd = Get-Command protoc -ErrorAction SilentlyContinue
$protoc = if ($protocCmd) { $protocCmd.Source } else { $null }
if (-not $protoc) {
    $pkg = Get-ChildItem (Join-Path $env:LOCALAPPDATA 'Microsoft\WinGet\Packages') `
        -Directory -Filter 'Google.Protobuf*' -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($pkg) {
        $candidate = Join-Path $pkg.FullName 'bin\protoc.exe'
        if (Test-Path -LiteralPath $candidate) { $protoc = $candidate }
    }
}
if (-not $protoc) {
    Write-Host "    SKIPPED: protoc is not installed, so the generated-code check did not" -ForegroundColor Yellow
    Write-Host "    run. This is not a pass. Install it with 'winget install Google.Protobuf'." -ForegroundColor Yellow
} else {
    Invoke-Gate 'generated-code' {
        $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("protoc-" + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $tmp -Force | Out-Null
        try {
            & $protoc -I contracts/protobuf --go_out=$tmp --go_opt=module=github.com/Sergey-Bar/Ai-Captain `
                contracts/protobuf/security/v1/*.proto
            if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
            $generated = Get-ChildItem $tmp -Recurse -Filter '*.pb.go'
            if (-not $generated) {
                Write-Host "    protoc produced no output; the invocation is wrong, not the tree."
                exit 1
            }
            # Compare the sorted, non-empty lines, not the raw bytes.
            #
            # The committed files are formatted with goimports — this repository's
            # .golangci.yml names it as a formatter — which regroups and reorders
            # the import block. protoc-gen-go emits one flat sorted block, and
            # gofmt does not regroup. A raw byte compare therefore calls every one
            # of these files stale for a difference that is only where an import
            # sits, and a gate that is always red is a gate nobody reads.
            #
            # Sorting the lines is the normalisation that matches what is actually
            # being asked: "is the code compiled from the published .proto the code
            # in the tree". A stale file differs in a declaration — a field name, a
            # type, a tag, a number — and every one of those is a whole line, so
            # line-order-insensitive comparison catches all of them and only those.
            # The contract-check gate is the one that reads the wire.
            function Normalise([string]$path) {
                $lines = Get-Content -LiteralPath $path |
                    ForEach-Object { $_.Trim() } |
                    Where-Object { $_ -ne '' }
                ($lines | Sort-Object) -join "`n"
            }
            foreach ($g in $generated) {
                # The generated tree is rooted at the module path, so the path
                # under the temp directory already includes internal/contracts.
                $rel = $g.FullName.Substring($tmp.Length).TrimStart('\', '/') -replace '\\', '/'
                $committed = Join-Path $repo ($rel -replace '/', '\')
                if (-not (Test-Path -LiteralPath $committed)) {
                    Write-Host "    generated file missing from the tree: $rel"
                    exit 1
                }
                if ((Normalise $g.FullName) -ne (Normalise $committed)) {
                    Write-Host "    $rel is stale; run 'make generate' and commit the result."
                    exit 1
                }
            }
        } finally {
            Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        }
    }
}

Invoke-Gate 'rules-lint' { & $go run ./cmd/securityctl rules lint --bundle rules/builtin }

# The whole-recovery drill, as its own step.
#
# It is a package test today, which means it runs as part of `go test ./...` and
# its failure is one line in a list of thirty packages. It is the only thing in
# this repository that proves a decision still replays after the database and
# the checkpoint material are both gone — the property the whole append-only,
# hash-chained design exists for — and burying it in a package count is how a
# check stops being looked at.
Invoke-Gate 'recovery-drill' { & $go test -p 1 -count=1 -timeout 900s -v -run TestRecoveryDrill ./internal/dr/... }

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
