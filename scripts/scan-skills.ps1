<#
.SYNOPSIS
Scans the installed agent-skill corpus and writes the record `skills-audit` checks.

.DESCRIPTION
The agent skills on this machine are untrusted third-party input: they are read as
instructions by the agent making the decisions, they arrive with no review, and
nothing in this repository can revoke one. AGENTS.md puts untrusted input under the
threat model, and this is the script that acts on that.

It is separate from scripts\verify.ps1 because a scan is expensive. Measured on this
machine: 6.3 minutes for 25 skills, 10.3 on a repeat - the scanner caches nothing, so
running it on every verification would add the best part of an hour to a gate whose
other half takes two. The gate instead checks that a record exists and postdates the
corpus, so the record cannot go stale while reading as current.

`--no-llm` is not optional here. Without it the scanner sends the contents of every
skill's files to whichever LLM endpoint SKILLSPECTOR_PROVIDER names. That would copy
the whole corpus to a third party as a side effect of a security check. With it,
analysis stays local and the only egress is SC4's dependency lookup against
api.osv.dev, which sends package names and versions and nothing else.

The verdict is advisory. It is written to disk for a human to adjudicate, and
docs/threat-model.md section 6b records the adjudications that have been made. A
finding with no adjudication there has not been judged - it has not been cleared.

.EXAMPLE
.\scripts\scan-skills.ps1

.EXAMPLE
.\scripts\scan-skills.ps1 -OutDir C:\temp\audit
#>
[CmdletBinding()]
param(
    [string]$OutDir
)

$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
if (-not $OutDir) { $OutDir = Join-Path $repo 'dist\skills-audit' }

$cmd = Get-Command skillspector -ErrorAction SilentlyContinue
if (-not $cmd) {
    Write-Error @"
skillspector is not on PATH.

Install it:
  uv tool install skillspector[mcp] @ git+https://github.com/NVIDIA/skillspector.git

It lands in %USERPROFILE%\.local\bin. That is on PATH after scripts\run-verify.ps1, and
not on a default Windows shell - the same problem AGENTS.md lists for go, gcc, protoc
and golangci-lint.
"@
    exit 127
}

New-Item -ItemType Directory -Path $OutDir -Force | Out-Null

$roots = @(
    @{ path = (Join-Path $env:USERPROFILE '.agents\skills');     report = 'agents.json'; label = 'agents' },
    @{ path = (Join-Path $env:USERPROFILE '.config\kilo\skills'); report = 'kilo.json';   label = 'kilo'   }
)

Write-Host "SkillSpector $((& $cmd.Source --version) -replace 'SkillSpector v','')" -ForegroundColor Cyan
$started = Get-Date
$failed = $false

foreach ($r in $roots) {
    if (-not (Test-Path -LiteralPath $r.path)) {
        Write-Host "  $($r.label): $($r.path) does not exist, nothing to scan" -ForegroundColor Yellow
        continue
    }
    $report = Join-Path $OutDir $r.report
    $count = @(Get-ChildItem -LiteralPath $r.path -Recurse -Filter 'SKILL.md' -File -ErrorAction SilentlyContinue).Count
    Write-Host ""
    Write-Host "==> $($r.label): $count skills at $($r.path)" -ForegroundColor Cyan
    Write-Host "    this takes minutes, not seconds" -ForegroundColor DarkGray

    & $cmd.Source scan $r.path --recursive --no-llm -f json -o $report | Out-Null
    $code = $LASTEXITCODE

    # 0 is clean, 1 is "findings present" which is the expected state for a corpus
    # of documentation, and above 1 is the scanner itself failing. Treating 1 as an
    # error would make this script unusable.
    if ($code -gt 1) {
        Write-Host "    the scanner itself failed (exit $code)" -ForegroundColor Red
        $failed = $true
        continue
    }
    if (-not (Test-Path -LiteralPath $report)) {
        Write-Host "    the scanner wrote no report" -ForegroundColor Red
        $failed = $true
        continue
    }

    $s = Get-Content -LiteralPath $report -Raw | ConvertFrom-Json
    # A --recursive scan returns a multi-skill aggregate; a single-skill scan returns
    # a different shape. Reading one off the other prints blanks.
    if ($s.PSObject.Properties.Name -contains 'max_risk_score') {
        Write-Host ("    {0} skills, max risk {1}/100 {2} ({3}), {4} finding(s)" -f `
            $s.skill_count, $s.max_risk_score, $s.risk_severity, $s.risk_recommendation, $s.public_finding_records)
        Write-Host ("    {0} omitted from the per-skill detail (output cap), {1} transitive findings" -f `
            $s.skills_omitted, $s.transitive_finding_count)
    } else {
        Write-Host ("    risk {0}/100 {1} ({2}), {3} finding(s)" -f `
            $s.risk_assessment.score, $s.risk_assessment.severity, $s.risk_assessment.recommendation, @($s.issues).Count)
    }
    Write-Host "    report: $report"
    Write-Host "    ADVISORY. Adjudicate each finding in docs/threat-model.md section 6b." -ForegroundColor DarkGray
}

Write-Host ""
Write-Host ("Finished in {0:N1} min. Records in {1}" -f ((Get-Date) - $started).TotalMinutes, $OutDir) -ForegroundColor Cyan
if ($failed) {
    Write-Host "At least one scan did not complete. The skills-audit gate will fail until they do." -ForegroundColor Red
    exit 1
}
exit 0
