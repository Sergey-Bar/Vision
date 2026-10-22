# ============================================================================
#  VISION - the SHOWCASE: one command, every surface, the honest state of
#  this machine on screen before anything is clicked.
#
#  WHAT THIS STARTS
#    The showcase hub (web/) on http://localhost:8080 - the front door that
#    links every surface: /live (what the cameras see), /pipeline (how a
#    decision is made, from the demo's own files), /trust (the gate record,
#    the red-check, and two commands you can run from the page),
#    /operations, /console, /lab and /census.
#
#  WHAT IT BUILDS, AND WHEN
#    securityctl.exe    the console's binary. Built here only if it is missing;
#                        the hub refuses to start without it, so a missing one
#                        would otherwise fail as a path two levels away.
#    pocctl.exe          the POC module's binary, used by the two /trust
#                        actions. Built here only if it is missing, and a
#                        build failure is PRINTED and not hidden: /trust will
#                        then answer NOT RUN with the reason, which is the
#                        behaviour the page is designed for.
#    hub.exe             built fresh every run, because a stale server that
#                        serves last week's handlers is a story about a demo.
#
#  WHAT IT REFUSES TO PRETEND
#    Before starting, it reads what is on disk and says so: whether a
#    verification record exists, whether the POC red-check transcript exists,
#    whether dist/demo and dist/live-demo have been written, and how many
#    demo data roots exist. A page opened against a machine that has run
#    nothing says NOT RUN in its own voice - this script just makes sure the
#    visitor knows that before they look.
#
#  NO NETWORK, NO GPU, NO DOCKER IS NEEDED FOR THE HUB ITSELF. The live acts
#  (scripts/live-demo.ps1) and the conference demo (scripts/demo.ps1) are
#  separate scripts; the hub shows what they wrote.
# ============================================================================

param(
    # Do not open a browser; print the URL and serve. For a recording or a
    # machine where the default browser is not the wanted one.
    [switch]$NoBrowser,

    # Skip every build step. The script then refuses to start if
    # securityctl.exe is missing rather than building it quietly.
    [switch]$NoBuild
)

$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path (Join-Path $repo 'cmd\securityctl'))) {
    Write-Host "  this script must live in scripts\ inside the repository; $PSScriptRoot is not it" -ForegroundColor Red
    exit 1
}

function Say($s)  { Write-Host "  $s" -ForegroundColor Green }
function Dim($s)  { Write-Host "  $s" -ForegroundColor DarkGray }
function Warn($s) { Write-Host "  $s" -ForegroundColor Yellow }
function Fail($s) { Write-Host "  $s" -ForegroundColor Red }

# --- the toolchain is not on PATH on a default Windows shell ---------------
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    $env:Path += ";C:\Program Files\Go\bin;$env:USERPROFILE\go\bin"
    $goCmd = Get-Command go -ErrorAction SilentlyContinue
}
if (-not $goCmd) {
    Fail 'go was not found (looked on PATH, in C:\Program Files\Go\bin and in $env:USERPROFILE\go\bin).'
    Dim  'Install Go 1.26 or add it to PATH; nothing on the hub can be built without it.'
    exit 1
}
$go = $goCmd.Source

Push-Location $repo
try {
    # --- the console's binary, without which the hub refuses to start -------
    $securityctl = Join-Path $repo 'securityctl.exe'
    if (-not (Test-Path $securityctl)) {
        if ($NoBuild) {
            Fail 'securityctl.exe is missing and -NoBuild was passed. Build it with: go build -o securityctl.exe ./cmd/securityctl'
            exit 1
        }
        Say 'building securityctl.exe (missing; the console needs it)...'
        $ErrorActionPreference = 'Continue'
        & $go build -o $securityctl ./cmd/securityctl
        $code = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($code -ne 0) { Fail "go build ./cmd/securityctl failed with exit $code"; exit 1 }
    } else {
        Dim 'securityctl.exe is present.'
    }

    # --- the POC binary the /trust actions run, when it can be built --------
    $pocDir    = Join-Path $repo 'poc\active-monitoring'
    $pocctl    = Join-Path $pocDir 'bin\pocctl.exe'
    if (Test-Path $pocctl) {
        Dim 'pocctl.exe is present; the /trust actions will run.'
    } elseif ($NoBuild) {
        Warn 'pocctl.exe is missing and -NoBuild was passed; /trust will answer NOT RUN.'
    } else {
        Say 'building poc/active-monitoring/bin/pocctl.exe (missing; /trust runs it)...'
        Push-Location $pocDir
        try {
            $ErrorActionPreference = 'Continue'
            & $go build -o (Join-Path $pocDir 'bin\pocctl.exe') ./cmd/pocctl
            $code = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            if ($code -ne 0) {
                Warn "building pocctl failed with exit $code - the two /trust actions will answer NOT RUN with that reason, and nothing else is affected."
            } else {
                Dim 'pocctl.exe built.'
            }
        } finally {
            Pop-Location
        }
    }

    # --- the console's bundle, without which /console is a placeholder -----
    #
    # The console is a React application and its bundle is build output, so a
    # machine that has never run `npm run build` serves the committed
    # placeholder at /console: a page that says what to run, instead of a 404
    # with an empty body. The launcher builds it when it can and says so when
    # it cannot, because "the console showed a page telling me to run npm"
    # is a worse demo than "the console was not built on this machine".
    $spaDir  = Join-Path $repo 'web\spa'
    $spaOut  = Join-Path $repo 'web\consoleui\dist\index.html'
    if (Test-Path $spaOut) {
        Dim 'the console bundle is present; /console will serve it.'
    } elseif ($NoBuild) {
        Warn 'the console bundle is missing and -NoBuild was passed; /console will serve the placeholder that names the build command.'
    } else {
        $npmCmd = Get-Command npm -ErrorAction SilentlyContinue
        if (-not $npmCmd) {
            Warn 'npm is not on PATH, so the console bundle cannot be built; /console will serve the placeholder that names the build command.'
            Warn 'Install Node, then: cd web/spa; npm ci; npm run build'
        } else {
            Say 'building the console bundle (web/spa)...'
            if (-not (Test-Path (Join-Path $spaDir 'node_modules'))) {
                Dim 'installing the console''s dependencies (once)...'
                $ErrorActionPreference = 'Continue'
                Push-Location $spaDir
                try { & npm ci --no-audit --no-fund 2>&1 | Out-Null } finally { Pop-Location }
                $ErrorActionPreference = 'Stop'
            }
            $ErrorActionPreference = 'Continue'
            Push-Location $spaDir
            try { & npm run build 2>&1 | Out-Null } finally { Pop-Location }
            $code = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            if ($code -ne 0 -or -not (Test-Path $spaOut)) {
                Warn "the console bundle did not build (exit $code); /console will serve the placeholder that names the build command."
            } else {
                Dim 'the console bundle is built; /console will serve it.'
            }
        }
    }

    # --- what this machine actually has, said before the browser opens ------
    Write-Host ''
    Write-Host '  What the pages will show on this machine, read from disk right now:' -ForegroundColor White

    $record = Get-ChildItem (Join-Path $repo 'dist\verification') -Filter 'verify-*.json' -ErrorAction SilentlyContinue |
        Sort-Object Name | Select-Object -Last 1
    if ($record) {
        Say  "gate record:    $($record.Name) (written $($record.LastWriteTime.ToString('yyyy-MM-dd HH:mm')))"
    } else {
        Warn 'gate record:    NOT RUN - no dist/verification/verify-*.json on this machine; run scripts/verify.ps1.'
    }

    $transcript = Join-Path $pocDir 'redcheck-transcripts.txt'
    if (Test-Path $transcript) {
        $proved = (Select-String -Path $transcript -Pattern '^\s*proved:' | Select-Object -Last 1).Line
        Say  "POC red-check:  $($proved -replace '\s+', ' ' -replace '^ proved:', 'proved:') (from redcheck-transcripts.txt)"
    } else {
        Warn 'POC red-check:  NOT RUN - the transcript is not on this machine; poc/active-monitoring/scripts/verify.ps1 writes it.'
    }

    $incidents = @(Get-ChildItem (Join-Path $repo 'dist\demo') -Filter 'inc_*.incident.json' -ErrorAction SilentlyContinue)
    if ($incidents.Count -gt 0) {
        Say  "demo incidents: $($incidents.Count) incident documents in dist/demo - /pipeline renders them."
    } else {
        Warn 'demo incidents: NOT RUN - dist/demo has none; scripts/demo.ps1 writes them.'
    }

    $liveReport = Join-Path $repo 'dist\live-demo\report.html'
    if (Test-Path $liveReport) {
        $when = (Get-Item $liveReport).LastWriteTime.ToString('yyyy-MM-dd HH:mm')
        Say  "live demo:      dist/live-demo/report.html, written $when."
    } else {
        Warn 'live demo:      NOT RUN - dist/live-demo has no report; scripts/live-demo.ps1 writes it.'
    }

    $hasDemoData = Test-Path (Join-Path $pocDir '.demo-data\poc.db')
    $roots = @(Get-ChildItem (Join-Path $pocDir '.live-demo') -Directory -ErrorAction SilentlyContinue |
        Where-Object { Test-Path (Join-Path $_.FullName 'poc.db') })
    if ($hasDemoData -or $roots.Count -gt 0) {
        $parts = @()
        if ($hasDemoData) { $parts += '.demo-data' }
        if ($roots.Count -gt 0) { $parts += "$($roots.Count) root(s) under .live-demo" }
        Say  "demo data:      $(($parts -join ' and ')); /trust asks every root's ledger and acts on the newest one that verifies."
    } else {
        Warn 'demo data:      NOT RUN - no .demo-data and no .live-demo root; `pocctl demo` writes one in about three minutes, and scripts/live-demo.ps1 writes one for the live path.'
    }

    # --- build the hub fresh and serve it -----------------------------------
    $outDir = Join-Path $repo 'dist\showcase'
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
    $hub = Join-Path $outDir 'hub.exe'
    if (-not $NoBuild) {
        Write-Host ''
        Say 'building the hub (web/)...'
        $ErrorActionPreference = 'Continue'
        & $go build -o $hub ./web
        $code = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($code -ne 0) { Fail "go build ./web failed with exit $code"; exit 1 }
    } elseif (-not (Test-Path $hub)) {
        Fail 'hub.exe has not been built and -NoBuild was passed. Run without -NoBuild once.'
        exit 1
    }

    # --- is the port already taken? say so rather than die on the bind ------
    $listening = $null
    try {
        $listening = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
    } catch { }
    if ($listening) {
        Warn "port 8080 is already in use (PID $($listening[0].OwningProcess))."
        Dim  'If that is the hub, it is already up. If it is something else, stop it and re-run this script.'
        if (-not $NoBrowser) { Start-Process 'http://localhost:8080' }
        exit 0
    }

    Write-Host ''
    Say 'starting the showcase hub on http://localhost:8080'
    Dim 'pages: / (hub)  /live  /pipeline  /trust  /census  /operations  /lab  /ask  /chief  /console (the product)'
    Dim 'media pack: dist/showcase/site/ (static edition)  dist/showcase/media/ (video, screens, diagrams, PDF)'
    Dim 'Ctrl+C stops it.'

    $proc = $null
    try {
        $proc = Start-Process -FilePath $hub -WorkingDirectory $repo -PassThru -NoNewWindow

        # --- check every page before opening a browser -----------------------
        #
        # The pages were all there and all unreachable: the SPA's catch-all was
        # registered last and shadowed every one of them. A launcher that
        # prints a list of pages and then opens a browser to a 404 has taught
        # everyone that the list is a decoration.
        #
        # So the launcher checks the list it prints, and says which one failed.
        $pages = @('/', '/live', '/pipeline', '/trust', '/census', '/operations', '/lab', '/ask', '/chief', '/console')
        $up = $false
        for ($i = 0; $i -lt 40; $i++) {
            Start-Sleep -Milliseconds 250
            try {
                Invoke-WebRequest -Uri 'http://localhost:8080/' -UseBasicParsing -TimeoutSec 2 | Out-Null
                $up = $true
                break
            } catch { }
        }
        if (-not $up) {
            Fail 'the hub never answered on http://localhost:8080'
            exit 1
        }

        $bad = @()
        foreach ($p in $pages) {
            try {
                $r = Invoke-WebRequest -Uri ("http://localhost:8080" + $p) -UseBasicParsing -TimeoutSec 15
                $ok = ($r.StatusCode -eq 200 -and $r.Headers['Content-Type'] -like 'text/html*')
                if ($ok) {
                    Say ("{0,-12} 200, {1} bytes" -f $p, $r.RawContentLength)
                } else {
                    $bad += $p
                    Fail ("{0,-12} {1} {2}" -f $p, $r.StatusCode, $r.Headers['Content-Type'])
                }
            } catch {
                $status = $_.Exception.Response.StatusCode.value__
                $bad += $p
                Fail ("{0,-12} {1}" -f $p, $(if ($status) { $status } else { $_.Exception.Message }))
            }
        }
        if ($bad.Count -gt 0) {
            Fail ("{0} page(s) did not answer: {1}" -f $bad.Count, ($bad -join ' '))
            Fail 'the browser is not opened. A demo that 404s is worse than no demo.'
            exit 1
        }

        if (-not $NoBrowser) { Start-Process 'http://localhost:8080' }
        Wait-Process -Id $proc.Id -ErrorAction SilentlyContinue
    } finally {
        if ($proc -and -not $proc.HasExited) {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
            Dim 'hub stopped.'
        }
    }
} finally {
    Pop-Location
}
