# Runs scripts/verify.ps1 with the toolchain on PATH.
#
# It exists because none of go, gofmt, gcc, golangci-lint, protoc or skillspector
# is on PATH on a default Windows shell - AGENTS.md says so, and it has cost every
# run that started by discovering it. verify.ps1 resolves go, gofmt, protoc and
# golangci-lint itself; the three it does not resolve are added here so the list
# of what a run actually used is in one place rather than in a shell profile.
$ErrorActionPreference = 'Stop'

$env:Path += ';C:\Program Files\Go\bin'
$env:Path += ';C:\msys64\mingw64\bin'
$env:Path += ";$env:USERPROFILE\go\bin"
$env:Path += ";$env:USERPROFILE\.local\bin"

# The database-backed suites SKIP rather than fail when these are absent, so a green
# run without them is a run that tested almost nothing - the verification record
# says so in its `verified` field and this script prints the warning. Nothing is
# defaulted: the migrator refuses to guess a database, and so does this.
#
# Overridable, so a machine with its own database points here rather than editing
# the file.
if (-not $env:SP_TEST_DATABASE_URL) {
    $dbHost = if ($env:SP_TEST_DB_HOST) { $env:SP_TEST_DB_HOST } else { '127.0.0.1' }
    $dbPort = if ($env:SP_TEST_DB_PORT) { $env:SP_TEST_DB_PORT } else { '55432' }
    $env:SP_TEST_DATABASE_URL = "postgres://postgres@$dbHost`:$dbPort/security_test?sslmode=disable"
}
if (-not $env:SP_TEST_DATABASE_PASSWORD) { $env:SP_TEST_DATABASE_PASSWORD = 'postgres' }

Set-Location (Split-Path -Parent $PSScriptRoot)

& .\scripts\verify.ps1 @args
exit $LASTEXITCODE
