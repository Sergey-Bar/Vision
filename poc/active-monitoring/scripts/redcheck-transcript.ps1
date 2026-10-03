<#
.SYNOPSIS
    Runs every entry in the POC module's red-check table and records what went red.

.DESCRIPTION
    `scripts\verify.ps1` runs the red-check table as one step among many, and its
    output is the console scrollback: gone by the time somebody asks which controls
    have been watched fail. This script does the one step on its own and writes a
    transcript to a file, which is what the `tree-state` gate's fourth assertion
    reads.

    Why it exists as a separate file rather than a flag on verify.ps1: verify.ps1
    also fetches a model, a runtime and a ground-truth set, so its red-check cannot
    be run without those. A control table whose only proof requires a 200 MB
    download is a table that goes unproven on the machine most likely to be
    introducing a control.

    Three rules, each of them a way this could otherwise lie:

      1. The baseline is recorded FIRST and must be green. Every later "exit != 0"
         is only evidence if the same command was green before the mutation.
      2. A control whose `from` line is not in the file is a SKIP with the line
         printed, never a pass. A mutation that matched nothing proves nothing.
      3. The restore is verified, per control, and the run ends with the whole set
         green. A red-check harness that can leave the tree mutated is worse than
         one that does not run, because the damage is found by the next person
         rather than by the run that caused it. The restore is retried, because a
         `go test` binary on Windows can still hold the source mapped for a moment
         after it exits.

.PARAMETER Out
    Where to write the transcript. Defaults to
    `..\redcheck-transcripts.txt` beside this script.

.PARAMETER Only
    Run only the entries whose name contains this substring. For re-running one
    control while iterating on it.

.EXAMPLE
    .\scripts\redcheck-transcript.ps1
#>
[CmdletBinding()]
param(
    [string]$Out,
    [string]$Only
)

$ErrorActionPreference = 'Continue'
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

if (-not $Out) { $Out = Join-Path $repo 'redcheck-transcripts.txt' }

# Go is not on PATH on a default Windows shell. Resolved the same way verify.ps1
# resolves it, because a script that only runs on a configured shell is a script
# that stops being run.
$goCmd = Get-Command go -ErrorAction SilentlyContinue
$go = if ($goCmd) { $goCmd.Source } else { $null }
if (-not $go) { $go = 'C:\Program Files\Go\bin\go.exe' }
if (-not (Test-Path -LiteralPath $go)) {
    Write-Error "go not found. Install it, or point -Go at it. See AGENTS.md."
    exit 127
}
$mingw = 'C:\msys64\mingw64\bin'
if (Test-Path -LiteralPath $mingw) { $env:Path = "$mingw;$env:Path" }
$env:CGO_ENABLED = '1'

# ---------------------------------------------------------------------------
# The table.
#
# Parsed out of verify.ps1 rather than duplicated here. A second copy of the
# control list is a list that drifts, and a transcript produced from a drifted list
# is a transcript of controls this repository no longer has.
# ---------------------------------------------------------------------------
function Read-RedCheckTable {
    param([string]$Path)

    # Only the `$controls = @( ... )` region is parsed. verify.ps1 carries at least
    # one other table of `@{...}` blocks above it -- a route table, whose entries
    # have a `pkg` field and no `name` -- and parsing the whole file picks those up
    # and then refuses the document because a `pkg` appeared before any `name`.
    #
    # The region is found by its opening line and the first line that closes it,
    # which is what the file's own indentation guarantees and what a hand-written
    # line count would not.
    $all = [System.IO.File]::ReadAllLines($Path)
    $open = -1
    for ($i = 0; $i -lt $all.Length; $i++) {
        if ($all[$i] -match '^\s*\$controls\s*=\s*@\(\s*$') { $open = $i; break }
    }
    if ($open -lt 0) { throw "no `$controls = @(` region found in $Path" }
    $close = -1
    for ($i = $open + 1; $i -lt $all.Length; $i++) {
        if ($all[$i] -match '^\s*\)\s*$') { $close = $i; break }
    }
    if ($close -lt 0) { throw "the `$controls region opened at line $($open + 1) never closes in $Path" }
    $body = ($all[$open..($close - 1)] -join "`n")

    $entries = [System.Collections.Generic.List[object]]::new()
    $cur = $null

    # A PowerShell single- or double-quoted literal, which may span lines, with
    # `''` and `""` as escapes and a backtick escape inside double quotes.
    $keyRe = [regex]'(?m)^[ \t]*(name|file|from|to|pkg|test)[ \t]*=[ \t]*(?:''|")'

    function Read-Literal {
        param([string]$S, [int]$Open)
        $q = $S[$Open]
        $b = [System.Text.StringBuilder]::new()
        for ($i = $Open + 1; $i -lt $S.Length; $i++) {
            if ($S[$i] -eq $q) {
                if ($i + 1 -lt $S.Length -and $S[$i + 1] -eq $q) { [void]$b.Append($q); $i++; continue }
                return $b.ToString()
            }
            if ($q -eq '"' -and $S[$i] -eq '`' -and $i + 1 -lt $S.Length) { [void]$b.Append($S[$i + 1]); $i++; continue }
            [void]$b.Append($S[$i])
        }
        throw "unterminated string starting at index $Open in the `$controls region of $Path"
    }

    foreach ($m in $keyRe.Matches($body)) {
        $key = $m.Groups[1].Value
        $value = Read-Literal -S $body -Open ($m.Index + $m.Length - 1)
        if ($key -eq 'name') {
            $cur = [ordered]@{ name = $value; file = ''; from = ''; to = ''; pkg = ''; test = '' }
            $entries.Add($cur)
            continue
        }
        if ($null -eq $cur) { throw "a '$key' appears before any 'name' in the `$controls region of $Path" }
        $cur[$key] = $value
    }
    if ($entries.Count -eq 0) { throw "no red-check entries found in the `$controls region of $Path" }
    return $entries
}

$controls = Read-RedCheckTable -Path (Join-Path $PSScriptRoot 'verify.ps1')
$lines = [System.Collections.Generic.List[string]]::new()

function Say { param([string]$s) $lines.Add($s); Write-Host $s }

Say "# Red-check transcript"
Say ""
Say ("Produced by scripts\redcheck-transcript.ps1 on {0}." -f (Get-Date).ToString('yyyy-MM-dd HH:mm'))
Say ("Go: {0}" -f (& $go version))
Say ""
Say ("Controls in the table: {0}" -f $controls.Count)
Say ""
Say "Every control below was applied to the file on disk, the named test was run,"
Say "the failure was recorded, the file was restored, and the restore was confirmed."
Say "The baseline was run FIRST and is required to be green: an exit code after a"
Say "mutation is only evidence if the same command was green before it."
Say ""
Say "---"

# The packages the table mutates, so the baseline and the final check cover all of
# them. Derived from the table rather than written out: a restore check that names
# packages by hand stops covering one the moment a control is added somewhere new.
$pkgs = @($controls | ForEach-Object { $_.pkg } | Sort-Object -Unique)

Say "## Baseline"
Say ""
Say ('    go test -count=1 ' + ($pkgs -join ' '))
Push-Location $repo
& $go test -count=1 @pkgs 2>&1 | ForEach-Object { $lines.Add(('    ' + $_.ToString())) }
$baseline = $LASTEXITCODE
Pop-Location
Say ""
Say ("    baseline exit=$baseline  (must be 0)")
Say ""
if ($baseline -ne 0) {
    Say "The baseline is red, so nothing below is evidence: a mutation that changes"
    Say "an already-failing package to a differently-failing one proves nothing."
    Set-Content -LiteralPath $Out -Value $lines -Encoding UTF8
    Write-Host "wrote $Out"
    exit 1
}

$proved = 0
$skipped = 0
$mutated = [System.Collections.Generic.List[string]]::new()

foreach ($c in $controls) {
    if ($Only -and $c.name -notlike "*$Only*") { continue }

    Say ("## " + $c.name)
    Say ""
    $full = Join-Path $repo $c.file
    if (-not (Test-Path -LiteralPath $full)) {
        Say ("    SKIPPED: " + $c.file + " does not exist. NOT a pass; this control was not run.")
        Say ""
        $skipped++
        continue
    }
    $original = [System.IO.File]::ReadAllText($full)
    if (-not $original.Contains($c.from)) {
        Say ("    SKIPPED: the control line is not in " + $c.file + " in the form the table")
        Say ("    expects, so no control was removed. NOT a pass. Look for: " + $c.from)
        Say ""
        $skipped++
        continue
    }
    $mutatedText = $original.Replace($c.from, $c.to)
    if ($mutatedText -eq $original) {
        Say "    SKIPPED: the mutation produced no change. NOT a pass."
        Say ""
        $skipped++
        continue
    }

    Say ("    file: " + $c.file)
    Say ("    from: " + $c.from.Trim())
    Say ("    to  : " + $c.to.Trim())
    $applied = $false
    try {
        [System.IO.File]::WriteAllText($full, $mutatedText)
        $applied = $true
        if ($c.test) {
            Say ("    go test -count=1 -run " + $c.test + " " + $c.pkg)
            $args = @('test', '-count=1', '-run', $c.test, $c.pkg)
        } else {
            Say ("    go test -count=1 " + $c.pkg)
            $args = @('test', '-count=1', $c.pkg)
        }
        Push-Location $repo
        $output = & $go @args 2>&1
        $code = $LASTEXITCODE
        Pop-Location
        foreach ($o in $output) {
            $t = $o.ToString()
            if ($t -match '^(--- FAIL|FAIL|ok|\s{4})') { $lines.Add(('    ' + $t)) }
        }
        Say ("    exit=$code")
        if ($code -eq 0) {
            Say "    *** THE SUITE STAYED GREEN WITH THE CONTROL REMOVED."
            Say "    *** A test that cannot fail when its subject is gone is not a test."
        } else {
            Say "    proved: the control is removed and the suite went red, as it must."
            $proved++
        }
    } catch {
        Say ("    ERROR: " + $_.Exception.Message)
    } finally {
        if ($applied) {
            $restored = $false
            for ($try = 1; $try -le 12; $try++) {
                try {
                    [System.IO.File]::WriteAllText($full, $original)
                    $restored = $true
                    break
                } catch {
                    Start-Sleep -Milliseconds (100 * $try)
                }
            }
            if (-not $restored) {
                Say ("    *** THE RESTORE FAILED. " + $c.file + " IS LEFT MUTATED.")
                Say "    *** Do not commit anything. `git checkout --` that file, then re-run."
                Set-Content -LiteralPath $Out -Value $lines -Encoding UTF8
                Write-Host "wrote $Out"
                exit 1
            }
        }
        $mutated.Add($c.file)
    }
    Say ""
}

Say "---"
Say ""
Say "## Restore check"
Say ""
Say ("    Every package the table mutated, after every mutation was reverted.")
Say ""
Push-Location $repo
& $go test -count=1 @pkgs 2>&1 | ForEach-Object { $lines.Add(('    ' + $_.ToString())) }
$restored = $LASTEXITCODE
Pop-Location
Say ""
Say ("    restore exit=$restored  (must be 0)")
Say ""
Say ("## Result")
Say ""
Say ("    proved:  $proved")
Say ("    skipped: $skipped")
Say ("    total:   $($controls.Count)")
Say ""
if ($skipped -gt 0) {
    Say "    A SKIP is not a pass. Each one above is a control this run did not prove,"
    Say "    and the reason is printed above it."
}
if ($restored -ne 0) {
    Say "    THE TREE IS RED AFTER THE RESTORE. Something did not go back."
}

Set-Content -LiteralPath $Out -Value $lines -Encoding UTF8
Write-Host ""
Write-Host "wrote $Out"

if ($restored -ne 0) { exit 1 }
exit 0