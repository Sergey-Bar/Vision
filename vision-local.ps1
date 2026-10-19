# vision-local.ps1
# Runs Vision on localhost:8080 with no login and no MFA.
# Run from PowerShell in the project root folder.

$ErrorActionPreference = "Stop"

$env:SP_DATABASE_URL = 'postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
$env:SP_DATABASE_PASSWORD = 'postgres'
$env:SP_LISTEN_ADDR = '127.0.0.1:8080'
$env:SP_LOG_LEVEL = 'info'
$env:SP_TENANT_ID = 'tenant-demo'
$env:SP_SITE_ID = 'site-demo'
$env:SP_INTEGRATION_ID = 'demo-integration'
$env:SP_EVIDENCE_ROOT = "$env:USERPROFILE\vision-evidence"
$env:SP_BEHIND_PROXY = 'true'
$env:SP_TRUSTED_PROXY_CIDRS = '127.0.0.1/32'

$GO = "C:\Program Files\Go\bin\go.exe"
if (-not (Test-Path -LiteralPath $GO)) {
    Write-Error "Go not found at $GO"
    exit 1
}

New-Item -ItemType Directory -Path $env:SP_EVIDENCE_ROOT -Force | Out-Null

Write-Host "Checking Postgres container sp-pg..." -ForegroundColor Cyan
$existing = docker ps --format '{{.Names}}' | Where-Object { $_ -eq 'sp-pg' }
if (-not $existing) {
    Write-Host "Starting Postgres container on port 55432..." -ForegroundColor Cyan
    docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=security_test -p 55432:5432 postgres:16 -c max_connections=300 | Out-Null
    Write-Host "Waiting for Postgres to be ready..." -ForegroundColor Cyan
    for ($i = 0; $i -lt 30; $i++) {
        $ready = docker exec sp-pg pg_isready -U postgres 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Postgres is ready." -ForegroundColor Green
            break
        }
        Start-Sleep -Seconds 1
    }
} else {
    Write-Host "Container sp-pg is already running." -ForegroundColor Green
}

Write-Host "Running database migrations..." -ForegroundColor Cyan
& $GO run ./cmd/security-migrate up

Write-Host "Starting security-core server on $env:SP_LISTEN_ADDR (no login, no MFA)..." -ForegroundColor Cyan
& $GO run ./cmd/security-core
