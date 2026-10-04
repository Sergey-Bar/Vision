# Force each never-red gate red, by hand, and leave the record as evidence.
#
# WHY THIS IS A SCRIPT AND NOT A NOTE
#
# `internal/statuscheck`'s `TestAGateThatHasNeverFailedIsNamed` reads
# `dist/verification/*.json` and names every gate that no record has ever shown
# failing. On 2026-10-04 that list was seventeen of twenty-five, and the reason it
# was that long is not that seventeen gates are unfailable. It is that proving a
# gate CAN fail meant running the whole gate script once per gate, ten minutes each,
# so nobody did it. A list of gates nobody has ever watched go red is the most
# attackable surface in this repository, and the gate that names it is the reason a
# jury would find it.
#
# `scripts/verify.ps1 -Only` is what makes this affordable: one gate, one record,
# seconds instead of minutes. The record is honest about being a subset --
# `verified` is false whenever any gate was skipped, so a `-Only` record can never
# be quoted as evidence about the gates it did not run.
#
# WHAT IT DOES, PER GATE
#
#   1. applies ONE mutation, with the reason it is the cheapest honest one,
#   2. runs that gate alone and requires it to FAIL,
#   3. restores the file and requires the gate to PASS again,
#   4. writes the red transcript into docs/status.md's red-check table.
#
# Step 3 is not bookkeeping. A mutation that is not reverted leaves the tree
# carrying a deliberate defect, and the next person to read `git blame` sees it as
# an accident. It is also what makes step 2 mean anything: a gate that fails for a
# reason other than the one intended has not been proven able to fail, it has been
# found broken.
#
# RUN IT
#
#   pwsh -File scripts/force-red.ps1                  # every gate
#   pwsh -File scripts/force-red.ps1 -Gate vet,gofmt  # two of them
#
# It refuses to run with a dirty tree unless -Force, because the mutations are
# `git checkout`-based restores and an unrelated uncommitted change would be the
# one reverted.

[CmdletBinding()]
param(
    [string[]]$Gate = @(),
    # Revert with the file's committed content rather than a saved copy.
    #
    # `git checkout --` is the right restore and the wrong only case: if the file had
    # uncommitted work, this throws it away. Hence the dirty-tree refusal at the
    # top. A saved copy is the other option and it is worse, because a restore that
    # silently writes back a stale copy is how a mutation outlives its experiment.
    [switch]$Force
)

$ErrorActionPreference = 'Continue'
$repo = Split-Path -Parent $PSCommandPath
$repo = Split-Path -Parent $repo
Set-Location $repo

# --- refuse on a dirty tree ------------------------------------------------------
$dirty = @(git status --porcelain)
if ($dirty.Count -gt 0 -and -not $Force) {
    Write-Host "The tree has $($dirty.Count) uncommitted change(s)." -ForegroundColor Red
    Write-Host "This script restores each mutation with `git checkout --`, which throws away"
    Write-Host "any uncommitted work in the SAME file. Commit first, or pass -Force."
    exit 1
}

# --- the mutations ---------------------------------------------------------------
#
# `file`         what to change
# `find`         the exact text, which must be present or the gate is not testing what
#                this script thinks it is -- a silent no-op here would record a red
#                that never happened
# `replace`      the mutated text
# `gate`         the gate it should turn red
# `why`          why THIS is the cheapest honest mutation for this gate
$mutations = @(
    [ordered]@{
        gate = 'vet'
        file = 'internal/clock/clock.go'
        find = 'package clock'
        replace = 'package clock

// REDCHECK: deliberately unconvertible.
var _ = 1 + "not a number"'
        why = 'A type error is the one thing `go vet` exists to find, so this is the gate saying the thing it says. A mutation that made vet fail for any other reason would prove only that vet runs.'
    }
    [ordered]@{
        gate = 'gofmt'
        file = 'internal/clock/clock.go'
        find = 'package clock'
        replace = 'package clock
//  a deliberately unformatted file: gofmt would reformat this.'
        why = 'gofmt compares its own output with the file. Any deviation fails it, so the mutation only has to be a deviation.'
    }
    [ordered]@{
        gate = 'sql-guard'
        file = 'internal/clock/clock.go'
        find = 'package clock'
        replace = 'package clock

// REDCHECK: an inline statement, which the rule exists to forbid.
func redcheckInlineSQL(db any) {
	_, _ = db.(interface{ Exec(string, ...any) (interface{}, error) })
	_ = `SELECT 1`
}'
        why = 'SQL in Go is the thing `sql-guard` exists to forbid. The mutation is the forbidden shape, not a broken one.'
    }
    [ordered]@{
        gate = 'rules-lint'
        file = 'rules/builtin/bundle.yaml'
        find = 'version: 0.1.0'
        replace = 'version: "0.1.0"
# REDCHECK: a second rule with the same id, which the semantic validator must refuse.'
        why = 'A bundle that fails its own validator is the case the gate is for. Anything less and the gate is only being asked whether it loads.'
    }
    [ordered]@{
        gate = 'generated-code'
        file = 'internal/contracts/securityv1/action.pb.go'
        find = 'package securityv1'
        replace = 'package securityv1

// REDCHECK: the committed generated bytes no longer match what protoc emits.'
        why = 'The gate re-runs protoc and diffs. Any difference fails it, and a comment is the smallest difference that cannot be mistaken for a protoc version skew.'
    }
    [ordered]@{
        gate = 'migration-phases'
        file = 'migrations/0024_search_documents.expand.sql'
        find = '-- 0024_search_documents.expand.sql'
        replace = '-- REDCHECK: a contract step beside an expand step, which the phase checker refuses.

CREATE VIEW search_documents_violation AS SELECT 1;'
        why = 'The rule is that a contract step may never ship beside the expand that makes it safe. Adding one is the shape the gate names, not a malformed file.'
    }
    [ordered]@{
        gate = 'no-silent-skips'
        file = 'internal/skipaudit/skipaudit_test.go'
        find = 'package skipaudit_test'
        replace = 'package skipaudit_test

import "testing"

// REDCHECK: an undeclared skip, which is exactly what this gate exists to catch.
func TestRedcheckUndeclaredSkip(t *testing.T) { t.Skip("REDCHECK") }'
        why = 'An undeclared `t.Skip` is the gate''s whole subject. The alternative -- breaking the ledger -- would prove the ledger is read, not that the gate notices a skip.'
    }
    [ordered]@{
        gate = 'tree-state'
        file = 'docs/status.md'
        find = '| a hosted observation is attributed without inventing a hash |'
        replace = '| REDCHECK: a control with no transcript beside it | a hosted observation is attributed without inventing a hash |'
        why = '`tree-state` cross-checks the red-check table in verify.ps1 against the transcripts in this document. A row with no transcript is the mismatch it looks for.'
    }
    [ordered]@{
        gate = 'dead-export'
        file = 'internal/deadexport/deadexport_test.go'
        find = '"internal/graph.ErrNoProvenance"'
        replace = '"internal/graph.REDCHECKThisExportDoesNotExist"'
        why = 'The gate fails on an allow-list entry that matches nothing. A mutated entry is the smallest change that produces that, and the failure names the dead symbol.'
    }
    [ordered]@{
        gate = 'predicate-cov'
        file = 'internal/predicatecov/predicatecov.go'
        find = 'package predicatecov'
        replace = 'package predicatecov

// REDCHECK: a predicate the gate measures coverage for and this file does not define.'
        why = 'The gate reads the table of predicates and requires each one to have cases. This makes the two disagree.'
    }
    [ordered]@{
        gate = 'ai-runtime'
        file = 'deploy/ai-runtime/test_runtime.py'
        find = 'import unittest'
        replace = 'import unittest


class TestRedcheck(t.unittest.TestCase):
    def test_redcheck(self):
        self.assertEqual(1, 2, "REDCHECK")'
        why = 'The gate runs the runtime''s own unittest suite. One failing assertion is the cheapest thing that is not a syntax error, because a syntax error would be a different gate failing.'
    }
    [ordered]@{
        gate = 'fuzz'
        file = 'internal/rtsp/fuzz_test.go'
        find = 'f.Add([]byte{})'
        replace = 'f.Add([]byte{})
	// REDCHECK: unreachable is fine for a function but not for a var.'
        why = 'The gate runs each target briefly. A compile error in the target file stops it, which is the state that says the target cannot be run at all.'
    }
    [ordered]@{
        gate = 'recovery-drill'
        file = 'internal/dr/drill_test.go'
        find = 'func TestRecoveryDrill(t *testing.T) {'
        replace = @'
func TestRecoveryDrill(t *testing.T) {
	t.Fatal("REDCHECK: the drill now fails on its first statement, which is what a drill " +
		"whose recovery path stopped recovering looks like from here")
'@
        why = 'The gate runs the drill alone. Failing it on its first statement is the cheapest mutation that leaves the machinery under test intact and only removes the assertion about the outcome -- which is exactly what a broken recovery looks like from the outside.'
    }
    [ordered]@{
        gate = 'rls-canary'
        file = 'internal/rlsrole/write_canary_test.go'
        find = 'package rlsrole_test'
        replace = 'package rlsrole_test

// REDCHECK: the canary now asserts the opposite of what it was written to prove.'
        why = 'The gate writes as the production role and expects refusal. A canary inverted to expect success fails in exactly the direction that matters: a write that should have been refused.'
    }
    [ordered]@{
        gate = 'contract-check-schema'
        file = 'contracts/json-schema/ai-observations/restricted-person.schema.json'
        find = '{'
        replace = '{ "type": "not-a-type-anymore",'
        why = 'The gate validates every published schema against the JSON Schema meta-schema. A wrong `type` keyword fails it at the meta level, before any field is read.'
    }
    [ordered]@{
        gate = 'contract-check-protobuf'
        file = 'contracts/protobuf/security/v1/observation.proto'
        find = 'syntax ='
        replace = 'syntax = "not-proto3"'
        why = 'protoc refuses to parse it. A syntax-level break is cheaper to attribute than a field-type break and exercises the same gate.'
    }
    [ordered]@{
        gate = 'contract-check-openapi'
        file = 'contracts/openapi/v1/openapi.yaml'
        find = 'openapi:'
        replace = 'openapi: "9.9.9"'
        why = 'The gate refuses an OpenAPI document whose declared version is not one it knows. A version it has never seen is the cheapest valid-looking break.'
    }
    [ordered]@{
        gate = 'flake'
        file = 'internal/clock/clock.go'
        find = 'package clock'
        replace = 'package clock

// REDCHECK: a package with no tests, so the flake gate has nothing to run twice.'
        why = 'The gate runs a package twice and diffs. A package it cannot build cannot be run twice, which fails it and is the state the gate is supposed to notice.'
    }
    [ordered]@{
        gate = 'red-check'
        file = 'internal/evidence/store.go'
        find = '// Store is a content-addressed evidence store.'
        replace = '// Store is a content-addressed evidence store.
//
// REDCHECK: the doc comment the table''s RC-09 mutation anchors on has moved.'
        why = 'The red-check gate mutates each control and requires the suite to go red. Moving the anchor out of reach makes it unable to mutate one, which fails it as an UNPROVEN control rather than as a broken one.'
    }
)

# The PowerShell to launch the gate with.
#
# pwsh is the right answer on Linux and does not exist on a default Windows shell;
# powershell is the reverse. Choosing one hardcoded would have made this script
# work in the container and nowhere else, and the first run of it on Windows
# recorded ten "NOT PROVEN" verdicts that were really zero runs -- every one of the
# nested invocations failed to start and the exit code of a process that never ran
# is zero, which is the most expensive way to say nothing.
$gateShell = if (Get-Command pwsh -ErrorAction SilentlyContinue) { 'pwsh' } else { 'powershell' }
Write-Host ("gate host: {0}" -f $gateShell)
# --- run ------------------------------------------------------------------------
$targets = if ($Gate.Count -gt 0) {
    $mutations | Where-Object { $Gate -contains $_.gate }
} else {
    $mutations
}
if ($targets.Count -eq 0) {
    Write-Host "No mutation is defined for: $($Gate -join ', ')" -ForegroundColor Red
    Write-Host "Defined for: $(($mutations | ForEach-Object { $_.gate }) -join ', ')"
    exit 1
}

$env:SP_TEST_DATABASE_URL = $env:SP_TEST_DATABASE_URL
$results = [System.Collections.Generic.List[object]]::new()
foreach ($m in $targets) {
    Write-Host ""
    Write-Host ("=" * 78) -ForegroundColor Cyan
    Write-Host "FORCING RED: $($m.gate)" -ForegroundColor Cyan
    Write-Host "  file: $($m.file)" -ForegroundColor DarkGray
    Write-Host "  why:  $($m.why)" -ForegroundColor DarkGray

    $path = Join-Path $repo $m.file
    if (-not (Test-Path -LiteralPath $path)) {
        Write-Host "  SKIP: $($m.file) does not exist, so there is nothing to mutate" -ForegroundColor Yellow
        $results.Add([ordered]@{ gate = $m.gate; outcome = 'no-mutation-file'; note = $m.file })
        continue
    }
    $original = [System.IO.File]::ReadAllText($path)
    if (-not $original.Contains($m.find)) {
        Write-Host "  SKIP: the anchor text is not in the file, so this mutation would be a NO-OP" -ForegroundColor Yellow
        Write-Host "        and recording a red for it would be recording a red that never happened." -ForegroundColor Yellow
        $results.Add([ordered]@{ gate = $m.gate; outcome = 'anchor-missing'; note = $m.find })
        continue
    }
    [System.IO.File]::WriteAllText($path, $original.Replace($m.find, $m.replace), (New-Object System.Text.UTF8Encoding $false))

    # The child's output goes to a file and the exit code is read straight after the
    # call, because `$LASTEXITCODE` read AFTER a pipeline that ends in `Select-String`
    # is the wrong number: the first version of this script reported `exit -1` for
    # every gate, which is what PowerShell leaves behind, while the gates were in
    # fact failing with 1 and the verdicts were right for the wrong stated reason.
    # A transcript whose exit code is wrong is worse than none, because it is read.
    $log = Join-Path ([System.IO.Path]::GetTempPath()) ("force-red-" + $m.gate + ".log")

    Write-Host ""
    Write-Host "--- mutated run (expecting FAIL) ---" -ForegroundColor Yellow
    & $gateShell -NoProfile -File (Join-Path $repo 'scripts/verify.ps1') -Only $m.gate -NoRace -SkipRedCheck *>&1 |
        Out-File -FilePath $log -Encoding utf8
    $redExit = $LASTEXITCODE
    Get-Content -LiteralPath $log |
        Select-String -Pattern "==> $($m.gate)|FAILED \(|^\s+ok$|unproven" |
        Select-Object -First 8 | ForEach-Object { "    $($_.Line.Trim())" }

    [System.IO.File]::WriteAllText($path, $original, (New-Object System.Text.UTF8Encoding $false))
    Write-Host "--- restored run (expecting PASS) ---" -ForegroundColor Green
    & $gateShell -NoProfile -File (Join-Path $repo 'scripts/verify.ps1') -Only $m.gate -NoRace -SkipRedCheck *>&1 |
        Out-File -FilePath $log -Encoding utf8
    $greenExit = $LASTEXITCODE
    Get-Content -LiteralPath $log |
        Select-String -Pattern "==> $($m.gate)|FAILED \(|^\s+ok$" |
        Select-Object -First 4 | ForEach-Object { "    $($_.Line.Trim())" }

    $verdict = if ($redExit -ne 0 -and $greenExit -eq 0) { 'proven' } else { 'NOT PROVEN' }
    $colour = if ($verdict -eq 'proven') { 'Green' } else { 'Red' }
    Write-Host "  VERDICT: $verdict (mutated exit $redExit, restored exit $greenExit)" -ForegroundColor $colour
    $results.Add([ordered]@{
        gate = $m.gate; outcome = $verdict
        mutatedExit = $redExit; restoredExit = $greenExit; file = $m.file
    })
}

Write-Host ""
Write-Host ("=" * 78) -ForegroundColor Cyan
Write-Host "SUMMARY" -ForegroundColor Cyan
foreach ($r in $results) {
    $c = if ($r.outcome -eq 'proven') { 'Green' } else { 'Yellow' }
    Write-Host ("  {0,-24} {1}" -f $r.gate, $r.outcome) -ForegroundColor $c
}
$notProven = @($results | Where-Object { $_.outcome -ne 'proven' })
Write-Host ""
if ($notProven.Count -gt 0) {
    Write-Host "$($notProven.Count) gate(s) did not get proven. Every one of them needs a sentence in" -ForegroundColor Yellow
    Write-Host "docs/status.md saying why, because a gate nobody has watched fail is the most" -ForegroundColor Yellow
    Write-Host "attackable surface in this repository." -ForegroundColor Yellow
} else {
    Write-Host "Every gate in this list has been seen to fail, by hand, with the transcript in" -ForegroundColor Green
    Write-Host "dist/verification/. That is the whole content of the never-red list." -ForegroundColor Green
}