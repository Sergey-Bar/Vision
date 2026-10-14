<#
.SYNOPSIS
    The boot gate: migrate, seed, start security-core, assert it serves, tear it
    down, and assert a second seed refuses cleanly.

.DESCRIPTION
    This is the gate that turns "the binary builds" into "the service runs". It
    does the smallest honest boot: run migrations, seed a fresh tenant, start the
    core against a real Postgres, and require it to answer /v1/health and
    /v1/health/ready and to SERVE (not 5xx) /v1/incidents and /v1/evidence -- which
    it refuses only for want of a session, which is the correct answer for an
    unauthenticated probe. Then it seeds a SECOND time and requires the refusal the
    append-only ledger promises, and tears every process down in a finally block.

    The AI plane is booted alongside when Python is present, because the core is
    pointed at a real engine; when it is not, the core still boots and still serves
    health, because a site with no model is a supported configuration rather than a
    crash. This gate needs Python no more than the core does.

    Every assertion here is one the product can fail. Written after a session that
    booted the core by hand and found it could not: the dispatch-check read the
    active bundle with an EMPTY tenant, tripping dbx's no-tenant guard, so a fresh
    deployment failed to start. That defect is fixed; this gate is what keeps it
    fixed, because reverting the fix makes the core refuse to reach a listening
    socket and this gate goes red. A service that cannot boot is the defect a build
    green everywhere else hides.
#>
param(
    [string]$DBUrl = $env:SP_TEST_DATABASE_URL,
    [string]$DBPassword = $env:SP_TEST_DATABASE_PASSWORD
)

# Continue, not Stop. `go run security-seed` legitimately writes a warning to
# stderr -- the keystore's world-readable advisory, which is exactly the Wave 0
# control working -- and under Stop that warning is a terminating
# NativeCommandError that aborts the gate before it can read the exit code. This
# gate validates every native call by its exit code and its output, which is the
# robust pattern; Stop buys nothing here and loses the seed.
$ErrorActionPreference = 'Continue'
$repo = Split-Path -Parent $PSScriptRoot
$procs = [System.Collections.Generic.List[System.Diagnostics.Process]]::new()

function Fail([string]$why) {
    Write-Host "    BOOT FAILED: $why" -ForegroundColor Red
    exit 1
}
function Done([string]$what) { Write-Host "    ok: $what" -ForegroundColor Green }

try {
    $go = (Get-Command go -ErrorAction SilentlyContinue).Source
    if (-not $go) { $go = 'C:\Program Files\Go\bin\go.exe' }
    if (-not (Test-Path -LiteralPath $go)) { Fail "go toolchain not found; the boot gate cannot run." }
    if (-not $DBUrl) { Fail "no SP_TEST_DATABASE_URL: the boot gate needs a real Postgres, and a green run without one would be a run that booted nothing." }

    $run = [Guid]::NewGuid().ToString('N').Substring(0, 8)
    $root = Join-Path $repo ("dist\boot-check\$run")
    New-Item -ItemType Directory -Force -Path "$root\artifacts", "$root\evidence", "$root\console" | Out-Null
    $tenant = "bt$run"; $site = "bs$run"

    $env:SP_DATABASE_URL = $DBUrl; $env:SP_DATABASE_PASSWORD = $DBPassword
    $env:SP_TEST_DATABASE_URL = $DBUrl

    # 1. Migrations.
    & $go run ./cmd/security-migrate up *> $null
    if ($LASTEXITCODE -ne 0) { Fail "go run security-migrate up (exit $LASTEXITCODE); the schema was not ready" }
    Done "migrations up"

    # 2. Build the core.
    $coreExe = Join-Path $root 'security-core.exe'
    & $go build -o $coreExe ./cmd/security-core *> $null
    if ($LASTEXITCODE -ne 0) { Fail "go build security-core (exit $LASTEXITCODE)" }
    Done "security-core built"

    # 3. Seed a fresh tenant. Exit 0 is the assertion: this is the step that used
    #    to die at TOTP enrolment on Windows, and it must now complete.
    $seedArgs = @(
        '--tenant', $tenant, '--site', $site,
        '--evidence-root', "$root\evidence",
        '--secret-key', "$root\secret.key",
        '--console-tls', "$root\console",
        '--reset', '--passphrase', 'boot-gate-passphrase-000'
    )
    $seedOut = & $go run ./cmd/security-seed @seedArgs 2>&1
    if ($LASTEXITCODE -ne 0) { Fail "seed (exit $LASTEXITCODE):`n$seedOut" }
    Done "seeded tenant $tenant / site $site (seed completed, enumerating)"

    # 4. Boot the AI plane when Python is available, so the core is pointed at a
    #    real engine. Absent Python the core still boots; this is not a hard
    #    requirement, only a bonus evidence path.
    $aiPort = 8899
    $py = $null
    foreach ($n in 'py','python','python3') { $c = Get-Command $n -ErrorAction SilentlyContinue; if ($c) { $py = $c.Source; break } }
    if ($py) {
        $bundleDir = "$root\bundle"
        & $py (Join-Path $repo 'deploy\ai-runtime\build_bundle.py') (Join-Path $repo 'deploy\ai-runtime\engines\fact_investigator.py') -o $bundleDir --secret '9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60' *> $null
        if ($LASTEXITCODE -ne 0) { Fail "build_bundle (exit $LASTEXITCODE); the signed plane could not be built" }
        # The plane is configured through the environment it inherits from this
        # shell; the core reads none of these, so sharing them is harmless and
        # keeps per-process env out of Start-Process, which cannot set it.
        $env:SP_MODEL_BUNDLE = $bundleDir
        $env:SP_MODEL_BUNDLE_KEY = 'd75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a'
        $env:SP_ENGINE_PATH = Join-Path $repo 'deploy\ai-runtime\engines\fact_investigator.py'
        $env:SP_AI_LISTEN = "127.0.0.1:$aiPort"
        $plane = Start-Process -FilePath $py -PassThru -WindowStyle Hidden -RedirectStandardOutput "$root\plane.log" -RedirectStandardError "$root\plane.err" -ArgumentList @((Join-Path $repo 'deploy\ai-runtime\runtime.py')) -WorkingDirectory $repo
        $procs.Add($plane) | Out-Null
        Done "the AI plane is booted against a signed bundle with the investigate engine"
    }

    # 5. Dispatch destinations the seed's active bundle names. Written here because
    #    the gate carries its own: https and non-loopback, both of which the loader
    #    enforces.
    $destFile = "$root\dispatch-destinations.yaml"
    @"
version: 1
destinations:
  - name: on-call
    url: https://on-call.invalid/on-call
    secret_ref: SP_DISPATCH_ON_CALL_TOKEN
  - name: security-manager
    url: https://security-manager.invalid/security-manager
    secret_ref: SP_DISPATCH_SECURITY_MANAGER_TOKEN
"@ | Set-Content -LiteralPath $destFile -Encoding utf8

    # 6. Start the core. This is the step the empty-tenant dispatch bug blocked;
    #    reaching a listening socket is the gate's central assertion.
    $crt = "$root\console\console.crt"; $key = "$root\console\console.key"
    if (-not (Test-Path -LiteralPath $crt)) { Fail "the seed produced no console.crt at $crt; TLS cannot be configured" }
    $env:SP_TENANT_ID = $tenant; $env:SP_SITE_ID = $site; $env:SP_INTEGRATION_ID = 'milestone'
    $env:SP_EVIDENCE_ROOT = "$root\evidence"; $env:SP_SECRET_KEY = "$root\secret.key"
    $env:SP_AI_ENDPOINT = "http://127.0.0.1:$aiPort"
    $env:SP_LISTEN_ADDR = '127.0.0.1:8898'
    $env:SP_TLS_CERT = $crt; $env:SP_TLS_KEY = $key
    $env:SP_DISPATCH_DESTINATIONS = $destFile; $env:SP_LOG_LEVEL = 'info'
    $core = Start-Process -FilePath $coreExe -PassThru -WindowStyle Hidden -RedirectStandardOutput "$root\core.log" -RedirectStandardError "$root\core.err" -WorkingDirectory $repo
    $procs.Add($core) | Out-Null

    # 7. Wait for health. curl -k trusts the self-signed cert the seed minted.
    $ok = $false
    for ($i = 0; $i -lt 60; $i++) {
        Start-Sleep -Milliseconds 500
        $code = (& curl.exe -sk "https://127.0.0.1:8898/v1/health" -o "$root\health.json" -w '%{http_code}') 2>$null
        if ($code -eq '200') { $ok = $true; break }
    }
    if (-not $ok) { Fail "the core never answered /v1/health with 200. Its log:`n$(Get-Content "$root\core.err" -Raw -ErrorAction SilentlyContinue)" }

    # 8. The endpoints the plan names. Both must be SERVED, not 5xx. An
    #    unauthenticated probe is correctly refused with 401, which is the proof
    #    the route exists and the CSRF/auth guard is in front of it.
    $ready = & curl.exe -sk "https://127.0.0.1:8898/v1/health/ready" -o NUL -w '%{http_code}'
    if ($ready -ne '200') { Fail "/v1/health/ready returned $ready, want 200" }
    foreach ($path in '/v1/incidents', '/v1/evidence') {
        $code = & curl.exe -sk "https://127.0.0.1:8898$path" -o NUL -w '%{http_code}'
        if ($code -ne '401') { Fail "$path returned $code, want 401 (served, auth-gated); a 5xx would be the route broken" }
    }
    Done "core served /v1/health, /v1/health/ready, /v1/incidents, /v1/evidence on a seeded tenant"

    # 8b. The end the whole AI plane was built for: ai_plane_absent becomes a real
    #     answer. Store a structured fact-object as evidence, enqueue one VLM job, and
    #     let the RUNNING worker investigate it against the real bound engine. The
    #     probe reports the stored observation and exits nonzero unless it is
    #     non-degraded, cited, and insufficient=false -- the first live answer the
    #     product produces, not a placeholder. Skipped without Python: the plane is
    #     Python, and this is the bonus evidence path, not the boot requirement.
    if ($py) {
        $env:PROBE_TENANT = $tenant
        $env:PROBE_SITE = $site
        $probeOut = & $go run ./scripts/probe_vlm.go 2>&1
        $probeOut | ForEach-Object { Write-Host "    probe: $_" }
        if ($LASTEXITCODE -ne 0) { Fail "no real (non-degraded, cited) observation was produced end to end; see the probe output above" }
        Done "a real cited observation was produced by the running worker against the bound engine"
    }

    # 9. Idempotency: a second seed refuses, because the append-only disposition
    #    ledger is exactly what --reset may not delete. The refusal is the promise,
    #    so its clean, named exit is the assertion.
    $reseed = & $go run ./cmd/security-seed @seedArgs 2>&1
    if ($LASTEXITCODE -eq 0) { Fail "the second seed succeeded; --reset is meant to refuse when the append-only ledger already holds a disposition, and it did not" }
    if ("$reseed" -notmatch 'append-only|will not delete|disposition') { Fail "the second seed refused, but not for the reason the plan names:`n$reseed" }
    Done "the second seed refused cleanly and by name (append-only ledger)"

    Write-Host "    ok: BOOT gate passed" -ForegroundColor Green
    exit 0
}
finally {
    foreach ($p in $procs) { try { if (-not $p.HasExited) { $p.Kill() } } catch {} }
    Write-Host "    (tore down $($procs.Count) process(es))"
}
