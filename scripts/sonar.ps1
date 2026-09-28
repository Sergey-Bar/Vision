<#
.SYNOPSIS
    Runs the SonarQube scan locally, against a SonarQube this machine can reach.

.DESCRIPTION
    There is no workflow for this. GitHub Actions cannot start a job on this
    account — GitHub's own message on a probe run was "The job was not started
    because recent account payments have failed or your spending limit needs to be
    increased", and every job failed in a few seconds having executed no steps.

    A workflow file would therefore be decoration that never runs, which is the
    thing this repository's previous review correctly removed. The scan is real
    work and it runs here instead.

    Two things are required and neither is in the repository:
      - a SonarQube server this machine can reach
      - a scanner, either `sonar-scanner` on PATH or a jar this script is told about

.PARAMETries Server
    The SonarQube base URL, e.g. http://sonarqube.internal:9000

.PARAMETries Token
    A token with execute-analysis permission. Read from SONAR_TOKEN when omitted,
    so it is not in the shell history.

.PARAMETries Scanner
    Path to a sonar-scanner CLI, or to a sonar-scanner jar. Uses `sonar-scanner`
    from PATH when omitted.

.PARAMETries SkipTests
    Analyse without regenerating the coverage report. The coverage metrics will
    then be empty and any gate built on them is meaningless — offered only for a
    fast loop while looking at duplication.

.EXAMPLE
    scripts/sonar.ps1 -Server http://localhost:9000
#>
[CmdletBinding()]
param(
    [string]$Server = $env:SONAR_HOST_URL,
    [string]$Token = $env:SONAR_TOKEN,
    [string]$Scanner = $env:SONAR_SCANNER,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Push-Location $repo

if (-not $Server) {
    Write-Error @'
No SonarQube server. Pass -Server, or set SONAR_HOST_URL.

This is the one gate that cannot run entirely inside the repository: the analysis
has to be uploaded somewhere that holds the history. A local SonarQube, an
internal one, or SonarCloud — any of them. What cannot happen is a run that
reports success without having analysed anything.
'@
}

if (-not $Scanner) { $Scanner = 'sonar-scanner' }

Write-Host '== static analysis inputs'
foreach ($required in @('sonar-project.properties', 'internal', 'cmd')) {
    if (-not (Test-Path $required)) {
        Write-Error "The scanner would measure nothing: $required is missing."
    }
    Write-Host "   $required ok"
}

if (-not $SkipTests) {
    $go = 'C:\Program Files\Go\bin\go.exe'
    if (-not (Test-Path $go)) { $go = 'go' }

    Write-Host '== coverage'
    # -coverpkg across the repository, because a test in one package that exercises
    # another still covers it. Measured per package, several packages report as
    # untested that are not.
    $strict = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & $go test -p 1 -count=1 -coverpkg=./... -coverprofile=coverage.out ./... 2>&1 |
        Select-Object -Last 3 | ForEach-Object { Write-Host "   $_" }
    $code = $LASTEXITCODE
    $ErrorActionPreference = $strict
    if ($code -ne 0) {
        Write-Error 'The tests failed, so the coverage report is not trustworthy. Fix that first.'
    }
    $coverage = (& $go tool cover -func=coverage.out 2>&1 | Select-Object -Last 1)
    Write-Host "   $coverage"
}

Write-Host ''
Write-Host "Scanning. This is the step that needs the network and the server."
Write-Host ''

$env:SONAR_SCANNER_OPTS = '-Dsonar.go.coverageReportPaths=coverage.out'
if ($Token) { $env:SONAR_TOKEN = $Token }

$strict = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& $Scanner "-Dsonar.host.url=$Server" 2>&1 | Select-Object -Last 25 | ForEach-Object { Write-Host $_ }
$code = $LASTEXITCODE
$ErrorActionPreference = $strict

Pop-Location

if ($code -ne 0) {
    Write-Error "The scan failed (exit $code). No quality gate is being asserted."
}
Write-Host ''
Write-Host 'Scan complete. Read the gate on the server before believing it.'
exit 0
