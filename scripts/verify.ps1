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
                       warning where the binary is absent â€” never a quiet pass
      test-race        the full suite with the race detector, -p 1
      migration-phases every migration declares a phase and no expand ships
                       with its contract step
      generated-code  the checked-in .pb.go is what protoc produces from the
                       published .proto. Skipped with a visible warning where
                       protoc is absent
      rules-lint       the built-in bundle compiles as untrusted input
      recovery-drill   the whole-recovery drill: database gone, checkpoint
                       material gone, decision still replayable
      sql-guard        every SQL statement this repository writes, checked
                       against the schema: a mutation of a tenant-scoped table
                       names its tenant, a write to a leased table names its
                       lease owner, and an append-only table is only inserted
                       into. Exemptions are allowed but must carry a reason on
                       the statement and are printed on every run
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
    [switch]$NoRace,
    # Skip the red-check. Only for a fast inner loop. A release check must run it,
    # and it prints SKIPPED rather than passing quietly - the same rule as every
    # other omission in this script.
    [switch]$SkipRedCheck,
    # Re-run the agent-skill supply-chain scan. The `skills-audit` gate only
    # checks that a scan record exists and postdates the corpus, because a
    # measured scan of 25 skills takes 6-10 minutes and this script has sixteen
    # other gates. Use this when the corpus changed, or on a release check.
    [switch]$ScanSkills
)

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

# The race detector needs cgo, which needs a C compiler. Without one,
# `go test -race` fails to build and the whole gate stops â€” so the toolchain is
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
# stderr into a terminating error, so a tool that prints a diagnostic â€” a
# contract mismatch it wants to show you, a deprecation notice, a warning about
# a malformed document it worked around â€” aborts the whole script before the
# exit code is ever read. The first two gates of this script never wrote to
# stderr, so it went unnoticed for as long as it went unnoticed; adding the
# OpenAPI gate surfaced it on a warning, not on a failure.
#
# 'Continue' is the right setting here precisely because $LASTEXITCODE is the
# signal and stderr is not. A gate that fails still fails, and a diagnostic is
# still printed. What stops happening is a diagnostic being promoted to a
# failure because of where it was written. Anything that genuinely must abort â€”
# a missing interpreter, an unreadable file â€” calls exit itself.
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

# The controls the red-check removed that would not turn the suite red. Collected
# rather than printed, because Invoke-Gate reads one exit code and this gate needs
# to say which control failed, not only that one did.
$script:redCheckFailures = [System.Collections.Generic.List[string]]::new()

# What each gate did, in order. The console output is a transcript; this is the
# record, and it is written to disk at the end of the run whether the run passed or
# failed. A failed run's record is evidence too - it is the one that says which
# gate stopped the build.
$results = [System.Collections.Generic.List[object]]::new()
$skips = [System.Collections.Generic.List[object]]::new()

function Invoke-Gate {
    param([string]$Name, [scriptblock]$Command)
    Write-Host ""
    Write-Host "==> $Name" -ForegroundColor Cyan
    $started = Get-Date
    # Zeroed before the gate runs, not after.
    #
    # $LASTEXITCODE is a PowerShell *global*, and a gate whose last operation is a
    # cmdlet rather than a native command never touches it - so it arrives here
    # still holding whatever the previous gate left behind. Measured on this
    # script: with gate A setting it to 1 and gate B printing a string, gate B was
    # reported FAILED while doing nothing wrong. Every gate after a failing one was
    # mislabelled, in both directions: a real pass could read as a failure and a
    # failure could read as a pass depending on which gate ran before it.
    $global:LASTEXITCODE = 0
    & $Command
    $elapsed = [int]((Get-Date) - $started).TotalSeconds
    if ($LASTEXITCODE -ne 0) {
        $script:failures.Add($Name)
        Write-Host "    FAILED ($Name)" -ForegroundColor Red
        $script:results.Add([ordered]@{ name = $Name; outcome = 'failed'; seconds = $elapsed })
    } else {
        Write-Host "    ok" -ForegroundColor DarkGray
        $script:results.Add([ordered]@{ name = $Name; outcome = 'passed'; seconds = $elapsed })
    }
}

# Fail-Gate marks the running gate failed WITHOUT ending the script.
#
# `exit 1` inside a gate scriptblock terminates the whole run, not just the gate.
# Measured on this script: a gate that called `exit 1` meant the gate after it
# never ran, the summary never printed, and the verification record was never
# written - which is the one artifact that names the gate that stopped the build,
# and the header of this function says it is written whether the run passed or
# failed. Two gates (`gofmt`, `generated-code`) used `exit`, so a gofmt failure
# produced no record at all.
#
# A failing gate has to be recorded and the run has to continue, because "which
# gate failed" and "what else did it break" are different questions and a script
# that answers the first by never reaching the second cannot answer the second.
function Fail-Gate {
    param([string]$Why)
    Write-Host "    $Why" -ForegroundColor Red
    $global:LASTEXITCODE = 1
}

# Invoke-Skip records a gate that did not run.
#
# It exists because the two skip sites below used to print "SKIPPED" and nothing
# else, which is visible to whoever is watching the console and invisible in the
# one artefact this script now leaves behind. A verification record that cannot
# distinguish "passed" from "did not run" is the same quiet-pass defect as a gate
# that turns itself off, in a place it would be read by somebody who was not there.
function Invoke-Skip {
    param([string]$Name, [string]$Reason)
    Write-Host ""
    Write-Host "==> $Name SKIPPED" -ForegroundColor Yellow
    $script:skips.Add([ordered]@{ name = $Name; reason = $Reason })
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
        Fail-Gate 'gofmt would reformat the files above'
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
# is what this comment used to be able to say and no longer can. It is skipped â€”
# loudly, and as a skip rather than a pass â€” where the binary is absent, because
# a gate that quietly turns itself off is the thing this whole revision is about.
$golangciCmd = Get-Command golangci-lint -ErrorAction SilentlyContinue
$golangci = if ($golangciCmd) { $golangciCmd.Source } else { $null }
if (-not $golangci) {
    $candidate = Join-Path $env:USERPROFILE 'go\bin\golangci-lint.exe'
    if (Test-Path -LiteralPath $candidate) { $golangci = $candidate }
}
if (-not $golangci) {
    Invoke-Skip 'lint' 'golangci-lint is not installed; the linter gate did not run'
    Write-Host "    SKIPPED: golangci-lint is not installed. The linter gate did not run; this is" -ForegroundColor Yellow
    Write-Host "    not a pass. Install it with 'go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest'." -ForegroundColor Yellow
} else {
    Invoke-Gate 'lint' { & $golangci run --timeout 15m }
}

# A single-element array from an `if` expression is unrolled to a scalar, so
# `@race` splatted the *string* '-race' into its characters and the test gate ran
# `go test -p 1 -count=1 - r a c e ./...`. It failed, loudly, which is how this was
# found â€” but the race detector this gate claims to run has never actually run.
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

# ---------------------------------------------------------------------------
# The red-check: the controls, removed, each of which must turn the suite red.
# ---------------------------------------------------------------------------
#
# Everything above this line checks that the suite passes. That is necessary and
# it is not sufficient. A suite passes because the thing it was supposed to catch
# is no longer there, and nothing in a green run can tell those two apart - which
# is why docs/status.md says a control with no such run is not a control but a
# comment.
#
# The mechanism is ported from the POC module's own gate
# (poc/active-monitoring/scripts/verify.ps1), which has thirty of these. The root
# module had none, which is the same defect in the opposite direction: the POC
# proved its controls and the product did not.
#
# Three rules, and each of them is a way this gate would otherwise lie:
#
#   1. A control whose line is not present in the file is a SKIP, never a pass.
#      A mutation that silently matched nothing reports the control as guarded,
#      because a suite that was never mutated is green for the uninteresting
#      reason.
#   2. A mutation that produces no textual change is a SKIP, for the same reason.
#   3. After the table has run, the suite is run again and must be green. A
#      restore that failed leaves a tree that fails every later gate for an
#      unrelated reason, and the reader has no way to tell which.
#
# Every entry here was run by hand with its mutation applied and watched go red.
# A control that has not been through that is not in this table, which is why it
# holds two entries and not thirty - seeding it with plausible-looking mutations
# nobody has run would produce a gate that reports thirty guarded controls and
# proves none of them.
if ($SkipRedCheck) {
    Invoke-Skip 'red-check' '-SkipRedCheck was passed; no control was removed and nothing was proven'
    Write-Host "    No control was removed, so no control was proven. This is not a pass, and" -ForegroundColor Yellow
    Write-Host "    a release check must run it without the switch." -ForegroundColor Yellow
} else {
Invoke-Gate 'red-check' {
    $controls = @(
        @{
            # Invariant 3, first half. The fail-closed rule: a degraded observation
            # must carry its reason, because without one a reader cannot tell "the
            # model was never asked" from "the model answered and the answer was
            # unusable", which is the difference between an absence and a negative.
            name = 'a degraded observation without a reason is refused'
            file = 'internal\observations\observations.go'
            from = '	case o.Degraded != DegradationNone && strings.TrimSpace(o.DegradedDetail) == "":'
            to   = '	case false && o.Degraded != DegradationNone && strings.TrimSpace(o.DegradedDetail) == "": // REDCHECK RC-1'
            test = 'TestADegradedObservationWithNoReasonIsRefused'
            pkg  = './internal/observations'
            why  = 'a degraded observation with no reason is a row that reads as a negative answer, so every window paid for and never asked is indistinguishable from a window that was asked and found nothing - the one failure this product must never commit'
        }
@{
            # A reason attached to a row that claims to be fine is either a field
            # that was not cleared or a reason on the wrong row. Split from RC-1 on
            # purpose: folding the two would make whichever came second
            # unreachable, and its red-check would pass vacuously.
            name = 'a non-degraded observation carrying a reason is also refused'
            file = 'internal\observations\observations.go'
            from = '	if o.Degraded == DegradationNone && strings.TrimSpace(o.DegradedDetail) != "" {'
            to   = '	if false && o.Degraded == DegradationNone && strings.TrimSpace(o.DegradedDetail) != "" { // REDCHECK RC-2'
            test = 'TestANonDegradedObservationWithAReasonIsAlsoRefused'
            pkg  = './internal/observations'
            why  = 'a stale reason reads as this run''s explanation of a failure it did not have, so an operator debugging a degradation chases a cause that belongs to an earlier row'
        }
        @{
            # A path segment that is a traversal, a drive, a UNC prefix or a
            # leading colon. Of the three traversal checks this is the only one that
            # rejects `:foo` - no backslash, not absolute, no volume name, and
            # strings.Trim(segment, ".") is not empty - so before `:foo` was added
            # to the test's bad list this mutation left the suite green. Measured,
            # not assumed: it is how the missing test case was found.
            name = 'a traversal-shaped path segment is refused'
            file = 'internal\evidence\store.go'
            from = '^(\.\.?|:|\\\\|//)'
            to   = '^(ZZZNOTHING)'
            test = 'TestValidateRefRejectsEveryTraversalForm'
            pkg  = './internal/evidence'
            why  = 'a reference is joined under the evidence root, so a segment that resolves outside it reads or writes outside the root. On Windows a leading colon is alternate-data-stream syntax'
        }
        @{
            # A NUL truncates the path in every syscall that takes a C string, and a
            # reference reaches a database and an export tool as well as os calls.
            name = 'a NUL byte in a reference is refused'
            file = 'internal\evidence\store.go'
            from = 'strings.ContainsRune(ref, 0) {'
            to   = 'false && strings.ContainsRune(ref, 0) {'
            test = 'TestValidateRefRejectsEveryTraversalForm'
            pkg  = './internal/evidence'
            why  = 'a reference that is validated, stored, and only later concatenated into a path is truncated at the NUL, so the file actually opened is not the file that was validated'
        }
        @{
            # Invariant 3, read at the point of use rather than at intake.
            name = 'a degraded observation is not evidence'
            file = 'internal\rules\match.go'
            from = '!s.AIObservation.Degraded &&'
            to   = '!(s.AIObservation.Degraded && false) &&'
            test = 'TestDegradedObservationIsNotEvidence'
            pkg  = './internal/rules'
            why  = 'a degraded observation and an absent one are the same to a rule, and both differ from a negative answer. Treating a degradation as a usable signal is how "the model could not tell" becomes "the model said no", and the incident is filed on a fact nobody observed'
        }
        @{
            # Invariant 3's third absence, which is the one most easily collapsed
            # into the second. The model answering "I cannot tell" is not the model
            # answering "no", and it neither satisfies nor contradicts a condition.
            name = 'insufficient evidence is not a negative answer'
            file = 'internal\rules\match.go'
            from = '!s.AIObservation.InsufficientEvidence'
            to   = '!(s.AIObservation.InsufficientEvidence && false)'
            test = 'TestInsufficientEvidenceIsNotANegativeAnswer'
            pkg  = './internal/rules'
            why  = 'collapsing the third absence into the second is a quiet downgrade: the decision still looks right, and the only symptom is that a rule which could never corroborate appears to corroborate'
        }
        @{
            # Which absence is reported. A missing observation is `absent`, and a
            # defaulted reason would file an operator's triage under somebody else's
            # failure mode.
            name = 'a missing observation reports the absent reason, not a default'
            file = 'internal\rules\match.go'
            from = 'return DegradedAbsent'
            to   = 'return DegradedTimeout'
            test = 'TestMissingAIObservationDegradesToUnverified'
            pkg  = './internal/rules'
            why  = 'the degraded reason is the only thing that says the AI plane was never asked. Defaulting it to a failure mode sends the operator to debug a timeout that never happened, and hides the fact that the AI path was not live'
        }
    )

    $proved = 0
    foreach ($c in $controls) {
        $full = Join-Path $repo $c.file
        if (-not (Test-Path -LiteralPath $full)) {
            Write-Host "    SKIPPED: $($c.name) - $($c.file) does not exist, so no control was removed. Not a pass."
            $script:skips.Add([ordered]@{ name = 'red-check'; reason = "$($c.name): $($c.file) does not exist" })
            continue
        }
        $original = [System.IO.File]::ReadAllText($full)
        if (-not $original.Contains($c.from)) {
            Write-Host "    SKIPPED: $($c.name) - the control line is not in the file in the form this gate expects, so the control could not be removed. Not a pass. Look for: $($c.from)"
            $script:skips.Add([ordered]@{ name = 'red-check'; reason = "$($c.name): control line not found in $($c.file)" })
            continue
        }
        $mutated = $original.Replace($c.from, $c.to)
        if ($mutated -eq $original) {
            Write-Host "    SKIPPED: $($c.name) - the mutation produced no change. Not a pass."
            $script:skips.Add([ordered]@{ name = 'red-check'; reason = "$($c.name): mutation produced no change" })
            continue
        }
        try {
            [System.IO.File]::WriteAllText($full, $mutated)
            # Relaxed for the same reason the POC's gate relaxes it: a control whose
            # removal produces a compile error makes go test write to stderr, which
            # on this shell is a terminating NativeCommandError that would abort the
            # whole gate mid-table. A compile error is a legitimate red result - the
            # control really is gone - so it is read as one rather than as a crash.
            $ErrorActionPreference = 'Continue'
            & $go test -p 1 -count=1 -run $c.test $c.pkg *> $null
            $exit = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            if ($exit -eq 0) {
                Write-Host "    FAILED: $($c.name) - the suite stayed green with the control removed. $($c.why)"
                Write-Host "      A test that cannot fail when its subject is gone is not a test."
                $script:redCheckFailures.Add($c.name)
            } else {
                Write-Host "    ok: $($c.name) removed -> the suite went red, as it must"
                $proved++
            }
        } finally {
            [System.IO.File]::WriteAllText($full, $original)
        }
    }

    # The restore is a control on the controls. Without it a failed restore leaves
    # a mutated tree that fails every later gate for a reason nobody is looking at.
    #
    # It runs over every package the table mutates, derived from the table rather
    # than written out, because a restore check that names packages by hand quietly
    # stops covering one the moment a control is added somewhere new - and the gate
    # that is supposed to prove the table left the tree alone would then be the one
    # gate not checking the table's own footprint.
    $mutatedPkgs = @($controls | ForEach-Object { $_.pkg } | Sort-Object -Unique)
    $ErrorActionPreference = 'Continue'
    $restoreFailed = $false
    foreach ($p in $mutatedPkgs) {
        & $go test -p 1 -count=1 $p *> $null
        if ($LASTEXITCODE -ne 0) {
            Write-Host "    FAILED: $p is red after the controls were restored, so a restore did not work"
            $restoreFailed = $true
        }
    }
    $ErrorActionPreference = 'Stop'
    if ($restoreFailed) {
        $script:redCheckFailures.Add('restore')
    } else {
        Write-Host "    ok: the suite is green again in all $($mutatedPkgs.Count) package(s) the table mutated"
    }

    Write-Host "    $proved of $($controls.Count) control(s) proved; the count is derived from the table, never written into the gate's name"
    if ($script:redCheckFailures.Count -gt 0) {
        Fail-Gate "$($script:redCheckFailures.Count) control(s) could not be proven guarded: $($script:redCheckFailures -join '; ')"
    }
}
}

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
    Invoke-Skip 'generated-code' 'protoc is not installed; the generated-code check did not run'
    Write-Host "    SKIPPED: protoc is not installed, so the generated-code check did not" -ForegroundColor Yellow
    Write-Host "    run. This is not a pass. Install it with 'winget install Google.Protobuf'." -ForegroundColor Yellow
} else {
    Invoke-Gate 'generated-code' {
        $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("protoc-" + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $tmp -Force | Out-Null
        try {
            & $protoc -I contracts/protobuf --go_out=$tmp --go_opt=module=github.com/Sergey-Bar/Ai-Captain `
                contracts/protobuf/security/v1/*.proto
            if ($LASTEXITCODE -ne 0) { Fail-Gate "protoc exited $LASTEXITCODE"; return }
            $generated = Get-ChildItem $tmp -Recurse -Filter '*.pb.go'
            if (-not $generated) {
                Fail-Gate 'protoc produced no output; the invocation is wrong, not the tree.'
                return
            }
            # Compare the sorted, non-empty lines, not the raw bytes.
            #
            # The committed files are formatted with goimports â€” this repository's
            # .golangci.yml names it as a formatter â€” which regroups and reorders
            # the import block. protoc-gen-go emits one flat sorted block, and
            # gofmt does not regroup. A raw byte compare therefore calls every one
            # of these files stale for a difference that is only where an import
            # sits, and a gate that is always red is a gate nobody reads.
            #
            # Sorting the lines is the normalisation that matches what is actually
            # being asked: "is the code compiled from the published .proto the code
            # in the tree". A stale file differs in a declaration â€” a field name, a
            # type, a tag, a number â€” and every one of those is a whole line, so
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
                    Fail-Gate "generated file missing from the tree: $rel"
                    return
                }
                if ((Normalise $g.FullName) -ne (Normalise $committed)) {
                    Fail-Gate "$rel is stale; run 'make generate' and commit the result."
                    return
                }
            }
        } finally {
            Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
        }
    }
}

Invoke-Gate 'sql-guard' { & $go test -p 1 -count=1 -timeout 300s ./internal/sqlguard/... }

# The skill supply chain.
#
# Every other gate in this script checks code that lives in this repository. This
# one checks the code that checks this repository: the agent skills installed on
# the machine, which arrive as untrusted third-party input and run with implicit
# trust and no review.
#
# AGENTS.md already owns the rule this gate applies - "rule bundles, release
# archives and model bundles are untrusted input... the threat model owns the
# answer, not the convenience of the call site" - and a skill is the same category
# with a worse surface, because it is read as instructions by the thing making the
# decisions. Sixty-four of them were installed from outside this repository and
# none had been scanned.
#
# WHAT THIS GATE DOES NOT DO, because it was measured: it does not run the scan.
# A recursive scan of one 25-skill directory took 6.3 minutes cold and 10.3 warm -
# the scanner caches nothing, so a repeat run is not cheaper - and the corpus is
# 64 skills across two directories. A gate that added forty minutes to every
# verification is a gate that gets stopped being run, and this repository's own
# record is that a gate named in a document and not run is worse than one not
# named at all. `lint` and `-race` were decorative for exactly this reason.
#
# So the gate is fast and the scan is not. What the gate asserts is the thing that
# would otherwise rot silently: that a scan record exists, and that it was taken
# after the newest skill in the corpus. Add a skill and this gate fails until the
# scan is re-run, so the record cannot be stale while reading as current. The scan
# itself is `scripts\scan-skills.ps1`, or `.\scripts\verify.ps1 -ScanSkills`.
#
# `--no-llm` is not a caution, it is the reason the scan can run here at all:
# without it the scanner sends the contents of every skill's files to whichever LLM
# endpoint SKILLSPECTOR_PROVIDER names. A gate that copies the whole skill corpus
# to a third party as a side effect of verifying the build is not a gate this
# repository wants. With it, analysis stays local and the only egress left is
# SC4's dependency lookup against api.osv.dev, which sends package names and
# versions and nothing else.
#
# The verdict is advisory. A scanner that scores a legitimate Anthropic skill at
# 100 CRITICAL on a filtered `os.environ` passthrough is describing the shape of
# the code, not its intent, and a gate that blocked on that would train its
# readers to pass `--fail-on-findings` nothing and then to ignore the scanner.
# Findings are written to disk for a human to adjudicate, and
# `docs/threat-model.md` section 6b records the adjudications that have been
# made. A finding with no adjudication there has not been judged, not been cleared.
$auditDir = Join-Path $repo 'dist\skills-audit'
$skillRoots = @(
    @{ path = (Join-Path $env:USERPROFILE '.agents\skills');     report = 'agents.json'; label = 'agents' },
    @{ path = (Join-Path $env:USERPROFILE '.config\kilo\skills'); report = 'kilo.json';   label = 'kilo'   }
)

if ($ScanSkills) {
    $skillspectorCmd = Get-Command skillspector -ErrorAction SilentlyContinue
    if (-not $skillspectorCmd) {
        Write-Error @"
skillspector is not on PATH, so -ScanSkills cannot run. Install it with:
  uv tool install skillspector[mcp] @ git+https://github.com/NVIDIA/skillspector.git
It lands in %USERPROFILE%\.local\bin, which is what scripts\run-verify.ps1 puts on PATH.
"@
        exit 127
    }
    New-Item -ItemType Directory -Path $auditDir -Force | Out-Null
    Write-Host ""
    Write-Host "==> scan-skills (requested with -ScanSkills; this is the slow one)" -ForegroundColor Cyan
    $scanStarted = Get-Date
    foreach ($r in $skillRoots) {
        if (-not (Test-Path -LiteralPath $r.path)) {
            Write-Host "    $($r.label): $($r.path) does not exist, nothing to scan"
            continue
        }
        $report = Join-Path $auditDir $r.report
        Write-Host "    scanning $($r.path) - expect minutes, not seconds"
        # An advisory scan. Its exit code is not the gate's exit code: a finding is
        # a question for a human, and 1 means "findings present", which is the
        # expected state for a corpus of documentation. Only an exit above 1 is the
        # scanner itself failing.
        & $skillspectorCmd.Source scan $r.path --recursive --no-llm -f json -o $report | Out-Null
        if ($LASTEXITCODE -gt 1) {
            Write-Error "the scanner itself failed on $($r.path) (exit $LASTEXITCODE)"
            exit $LASTEXITCODE
        }
        if (-not (Test-Path -LiteralPath $report)) {
            Write-Error "the scanner wrote no report for $($r.path)"
            exit 1
        }
        # A multi-skill aggregate - --recursive is a different command shape from a
        # single-skill scan - carries max_risk_score and skill_count; a single-skill
        # report carries risk_assessment. Reading one shape off the other prints
        # blanks, and a gate whose own summary is blank is a gate whose reader learns
        # to skip it. Both are read and the one found says which.
        $summary = Get-Content -LiteralPath $report -Raw | ConvertFrom-Json
        if ($summary.PSObject.Properties.Name -contains 'max_risk_score') {
            Write-Host ("    {0}: {1} skills, max risk {2}/100 {3} ({4}), {5} finding(s) -> {6}" -f `
                $r.label, $summary.skill_count, $summary.max_risk_score, $summary.risk_severity, `
                $summary.risk_recommendation, $summary.public_finding_records, $report)
        } else {
            Write-Host ("    {0}: risk {1}/100 {2} ({3}), {4} finding(s) -> {5}" -f `
                $r.label, $summary.risk_assessment.score, $summary.risk_assessment.severity, `
                $summary.risk_assessment.recommendation, @($summary.issues).Count, $report)
        }
        Write-Host "    advisory only. Adjudicate in docs/threat-model.md section 6b." -ForegroundColor DarkGray
    }
    Write-Host ("    scan finished in {0:N1} min" -f ((Get-Date) - $scanStarted).TotalMinutes) -ForegroundColor DarkGray
}

# The gate proper: is there a record, and does it postdate the corpus?
Invoke-Gate 'skills-audit' {
    New-Item -ItemType Directory -Path $auditDir -Force | Out-Null
    foreach ($r in $skillRoots) {
        if (-not (Test-Path -LiteralPath $r.path)) { continue }
        $report = Join-Path $auditDir $r.report
        $newest = Get-ChildItem -LiteralPath $r.path -Recurse -Filter 'SKILL.md' -File -ErrorAction SilentlyContinue |
            Sort-Object LastWriteTime -Descending |
            Select-Object -First 1
        $count = @(Get-ChildItem -LiteralPath $r.path -Recurse -Filter 'SKILL.md' -File -ErrorAction SilentlyContinue).Count

        if (-not (Test-Path -LiteralPath $report)) {
            Fail-Gate ("no scan record for {0} ({1} skills). This is not a pass: the skill corpus is untrusted " +
                       "input and nothing has looked at it. Run .\scripts\scan-skills.ps1" -f $r.label, $count)
            continue
        }
        $scannedAt = (Get-Item -LiteralPath $report).LastWriteTime
        if ($newest -and $scannedAt -lt $newest.LastWriteTime) {
            $staleBy = [int]((New-TimeSpan -Start $scannedAt -End $newest.LastWriteTime).TotalHours)
            Fail-Gate (("the scan record for {0} is older than its own corpus: scanned {1}, and a skill changed {2} " +
                       "({3}h later). A skill was added or edited after the last scan, so the record no longer " +
                       "describes what is installed. Run .\scripts\scan-skills.ps1") -f `
                $r.label, $scannedAt.ToString('yyyy-MM-dd HH:mm'), $newest.LastWriteTime.ToString('yyyy-MM-dd HH:mm'), $staleBy)
            continue
        }
        Write-Host ("    ok: {0} - {1} skills, scan record current (taken {2})" -f `
            $r.label, $count, $scannedAt.ToString('yyyy-MM-dd HH:mm')) -ForegroundColor DarkGray
    }
}

Invoke-Gate 'rules-lint' { & $go run ./cmd/securityctl rules lint --bundle rules/builtin }

# The whole-recovery drill, as its own step.
#
# It is a package test today, which means it runs as part of `go test ./...` and
# its failure is one line in a list of thirty packages. It is the only thing in
# this repository that proves a decision still replays after the database and
# the checkpoint material are both gone â€” the property the whole append-only,
# hash-chained design exists for â€” and burying it in a package count is how a
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
    Invoke-Skip 'ai-runtime' 'no Python on PATH'
    Write-Host "==> ai-runtime SKIPPED (no Python on PATH)"
    Write-Host "    The AI runtime's contract is unverified. Install Python, or accept that the"
    Write-Host "    degraded-observation path - the product's central invariant - has no test."
}

# ---------------------------------------------------------------------------
# The verification record.
# ---------------------------------------------------------------------------
#
# `docs/status.md` reports a local run and is careful not to call it CI. That
# caution has nowhere to put the evidence: the console scrollback is gone by the
# time somebody asks whether the AI runtime's tests actually ran, whether the
# database was there, or whether the linter was present. So this writes a record.
#
# What it deliberately does NOT say, because it cannot:
#
#   - "CI passed". **There is no CI.** GitHub Actions cannot start a job on this
#     account; AGENTS.md says so and the pipeline was removed rather than left
#     red. This record is one local run by one person on one machine, and the
#     `provenance` field says exactly that so nobody can quote it as automation.
#   - That a skipped gate passed. `skipped` is a third outcome beside passed and
#     failed, and `verified` below is false whenever the list is non-empty.
#   - That the gate covered the whole repository. `poc/` is a second Go module and
#     no gate here reaches it; `excluded` says so in the record itself rather than
#     leaving the next reader to discover it.
function Write-VerificationRecord {
    param([bool]$Succeeded, [string]$ExitReason)

    $commit = $null
    $dirty = $null
    $git = Get-Command git -ErrorAction SilentlyContinue
    if ($git) {
        $commit = (& $git.Source -C $repo rev-parse HEAD 2>$null)
        if ($LASTEXITCODE -ne 0) { $commit = $null }
        $dirty = (& $git.Source -C $repo status --porcelain 2>$null).Count -gt 0
    }

    $goVersion = (& $go version) 2>$null
    $passed = @($results | Where-Object { $_.outcome -eq 'passed' }).Count
    $failed = @($results | Where-Object { $_.outcome -eq 'failed' }).Count

    $record = [ordered]@{
        schema         = 'security-platform/verification-record/v1'
        startedFrom    = 'scripts/verify.ps1'
        finishedAtUtc  = (Get-Date).ToUniversalTime().ToString('o')
        outcome        = if ($Succeeded) { 'passed' } else { 'failed' }
        exitReason     = $ExitReason

        provenance = [ordered]@{
            kind        = 'local-run'
            automated   = $false
            ci          = 'none. GitHub Actions cannot start a job on this account; see AGENTS.md. A green branch here is a human claim, not an automated one.'
            machine     = $env:COMPUTERNAME
            user        = $env:USERNAME
            powershell  = "$($PSVersionTable.PSVersion) ($($PSVersionTable.PSEdition))"
            go          = $goVersion
            commit      = $commit
            worktreeDirty = $dirty
        }

        mode = [ordered]@{
            raceDetector = (-not $NoRace)
            cgoEnabled   = ($env:CGO_ENABLED -eq '1')
            gccPresent   = $haveCC
        }

        # False whenever anything did not run, so a record cannot be read as a
        # clean bill of health by someone looking at one field.
        verified = ($Succeeded -and $failed -eq 0 -and $skips.Count -eq 0)

        database = [ordered]@{
            urlSet          = [bool]$env:SP_TEST_DATABASE_URL
            passwordSet     = [bool]$env:SP_TEST_DATABASE_PASSWORD
            # The database-backed suites skip rather than fail when the URL is
            # absent, so a green run with this false is a run that tested almost
            # nothing. Stated in the record because it is the single most likely
            # way for this file to be misread.
            note = 'Without SP_TEST_DATABASE_URL the database-backed suites skip rather than fail. urlSet=false means this run is not evidence.'
        }

        excluded = @(
            'poc/ - a second Go module (github.com/Sergey-Bar/ai-captain-poc). No gate in this script reaches it; run poc/active-monitoring/scripts/verify.ps1 separately.',
            'SonarQube duplication, complexity and coverage analysis - scripts/sonar.ps1, run by hand.',
            'GitHub Actions - it does not exist here.',
            "SkillSpector's verdict on the installed skills is advisory and does not set outcome for the skills-audit gate. A finding is a question for a human, recorded in docs/threat-model.md; the gate fails only when the scanner itself errors or writes no report. A record read as 'the skills are clean' would be a misreading of it - a clean gate means the scan ran, not that nothing was found."
        )

        gates     = @($results)
        skipped   = @($skips)
        failedGates = @($failures)
    }

    $dir = Join-Path $repo 'dist\verification'
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')
    $path = Join-Path $dir "verify-$stamp.json"
    try {
        ($record | ConvertTo-Json -Depth 6) | Set-Content -LiteralPath $path -Encoding UTF8
        Write-Host ""
        Write-Host "verification record: $path"
        if (-not $record.verified) {
            Write-Warning "It is marked verified=false. Read the skipped list before quoting it."
        }
    } catch {
        Write-Warning "the verification record could not be written: $($_.Exception.Message)"
    }
}

Write-VerificationRecord -Succeeded:($failures.Count -eq 0) -ExitReason $(if ($failures.Count -eq 0) { 'all gates passed' } else { 'one or more gates failed' })

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
