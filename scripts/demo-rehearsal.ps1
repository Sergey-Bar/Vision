# ============================================================================
#  VISION - the demo rehearsal.
#
#  WHAT THIS IS
#    The five-minute judge track, walked by a machine before a human walks it.
#    It fetches every page of the track, checks the page is really there, and
#    then checks the two things a judge would notice and a unit test would
#    not: that the page makes no request to any host but this one, and that
#    the browser's console says nothing.
#
#  WHY IT EXISTS
#    The demo used to 404 on every page. The showcase's own pages were in the
#    tree, complete, and unreachable, because a catch-all for a build output
#    that is gitignored was registered last and won. Nothing in the test suite
#    noticed: the routes were tested, just not the ones anybody visits.
#
#    So the rehearsal starts the server the way a person would, fetches the
#    pages a judge would fetch, and fails loudly on the first thing that is
#    wrong. It is the difference between "the gate is green" and "the demo
#    opens".
#
#  WHAT IT NEEDS
#    Node and npm, for the browser. Without them it says so and skips the
#    browser half - a skip is printed, never a quiet pass, because a silent
#    skip reads as a pass and this script exists to remove that reading.
#
#  USAGE
#    scripts/demo-rehearsal.ps1              # start a server, rehearse, stop it
#    scripts/demo-rehearsal.ps1 -BaseUrl ... # rehearse a server that is already up
#    scripts/demo-rehearsal.ps1 -NoBrowser   # fetch-only: no console check
# ============================================================================

param(
    # Rehearse a server that is already running, instead of starting one.
    [string]$BaseUrl = '',

    # Fetch the pages but do not open a browser, so the console-error check
    # and the external-request check are skipped. The fetch check still runs.
    [switch]$NoBrowser,

    # Port for the server this script starts.
    [int]$Port = 8099
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

# The track. The same nine paths the server's own route table holds, in the
# order the judge track visits them. The server answers /console too, and the
# rehearsal checks that separately: the console is the product, not the demo.
$acts = @(
    @{ path = '/';           name = 'the hub' },
    @{ path = '/live';       name = 'live' },
    @{ path = '/pipeline';   name = 'pipeline' },
    @{ path = '/trust';      name = 'trust' },
    @{ path = '/census';     name = 'census' },
    @{ path = '/operations'; name = 'operations' },
    @{ path = '/lab';        name = 'lab' },
    @{ path = '/ask';        name = 'ask' },
    @{ path = '/chief';      name = 'chief' }
)

$failures = New-Object System.Collections.Generic.List[string]
$skipped = $false

# ---------------------------------------------------------------------------
#  the toolchain
# ---------------------------------------------------------------------------

$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    $env:Path += ";C:\Program Files\Go\bin;$env:USERPROFILE\go\bin"
    $goCmd = Get-Command go -ErrorAction SilentlyContinue
}
if (-not $goCmd) {
    Fail 'go was not found (looked on PATH, in C:\Program Files\Go\bin and in $env:USERPROFILE\go\bin).'
    exit 1
}
$go = $goCmd.Source

$nodeCmd = Get-Command node -ErrorAction SilentlyContinue
$npmCmd  = Get-Command npm  -ErrorAction SilentlyContinue

# ---------------------------------------------------------------------------
#  start the server, unless one is already up
# ---------------------------------------------------------------------------

$proc = $null
$hub = $null

try {
    if (-not $BaseUrl) {
        $BaseUrl = "http://127.0.0.1:$Port"

        $outDir = Join-Path $repo 'dist\showcase'
        New-Item -ItemType Directory -Force -Path $outDir | Out-Null
        $hub = Join-Path $outDir 'rehearsal-hub.exe'

        Say 'building the hub (web/)...'
        $ErrorActionPreference = 'Continue'
        & $go build -o $hub ./web
        $code = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($code -ne 0) { Fail "go build ./web failed with exit $code"; exit 1 }

        Say "starting it on $BaseUrl"
	$proc = Start-Process -FilePath $hub -WorkingDirectory $repo -PassThru -NoNewWindow -ArgumentList @('-port', "$Port", '-dev')

        # Wait for the port to answer rather than sleeping a fixed second: a
        # fixed sleep is a race that passes on a fast machine and fails on a
        # slow one, and a rehearsal that fails for the wrong reason teaches
        # people to ignore it.
        $up = $false
        for ($i = 0; $i -lt 40; $i++) {
            Start-Sleep -Milliseconds 250
            try {
                Invoke-WebRequest -Uri "$BaseUrl/" -UseBasicParsing -TimeoutSec 2 | Out-Null
                $up = $true
                break
            } catch { }
        }
        if (-not $up) {
            Fail "the server never answered on $BaseUrl"
            exit 1
        }
    } else {
        Say "rehearsing the server already running at $BaseUrl"
    }

    # ---------------------------------------------------------------------------
    #  1. every page of the track answers, with HTML
    # ---------------------------------------------------------------------------

    Write-Host ''
    Say 'fetching every page of the track...'

    foreach ($act in $acts) {
        $url = "$BaseUrl$($act.path)"
        try {
            $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 15
            $ct = $r.Headers['Content-Type']
            if ($r.StatusCode -ne 200) {
                Fail ("{0,-12} {1} - status {2}" -f $act.path, $act.name, $r.StatusCode)
                $failures.Add("$($act.path) answered $($r.StatusCode)")
                continue
            }
            if ($ct -notlike 'text/html*') {
                Fail ("{0,-12} {1} - content type {2}" -f $act.path, $act.name, $ct)
                $failures.Add("$($act.path) answered content type $ct")
                continue
            }
            if ($r.RawContentLength -lt 200) {
                Fail ("{0,-12} {1} - {2} bytes, which is not a page" -f $act.path, $act.name, $r.RawContentLength)
                $failures.Add("$($act.path) answered a body of $($r.RawContentLength) bytes")
                continue
            }
            Say ("{0,-12} {1,-11} 200, {2} bytes" -f $act.path, $act.name, $r.RawContentLength)
        } catch {
            Fail ("{0,-12} {1} - {2}" -f $act.path, $act.name, $_.Exception.Message)
            $failures.Add("$($act.path) did not answer: $($_.Exception.Message)")
        }
    }

    # The console is a page too, and it is the one that is a build output.
    try {
        $r = Invoke-WebRequest -Uri "$BaseUrl/console" -UseBasicParsing -TimeoutSec 15
        if ($r.StatusCode -eq 200) {
            Say ("{0,-12} {1,-11} 200, {2} bytes" -f '/console', 'console', $r.RawContentLength)
        } else {
            # 503 is the honest answer when the bundle has not been built: the
            # placeholder names the build command instead of 404ing.
            Warn ("{0,-12} {1,-11} {2} - the bundle has not been built (npm run build in web/spa)" -f '/console', 'console', $r.StatusCode)
        }
    } catch {
        Warn "/console did not answer: $($_.Exception.Message)"
    }

    # The hub's three facts come from two endpoints. If those do not answer,
    # the front door has nothing to say, and a judge sees an empty page.
    Write-Host ''
    Say 'reading the hub''s three facts...'
    foreach ($ep in @('/api/showcase/status', '/api/showcase/incidents')) {
        try {
            $r = Invoke-WebRequest -Uri "$BaseUrl$ep" -UseBasicParsing -TimeoutSec 20
            $j = $r.Content | ConvertFrom-Json
            if ($ep -like '*status') {
                $gate = if ($j.gate) { "$($j.gate.outcome), $($j.gate.gates) gates" } else { 'NOT RUN (no record on this machine)' }
                $red  = if ($j.redCheck.pocTotal) { "$($j.redCheck.pocProved) of $($j.redCheck.pocTotal) proved" } else { 'NOT RUN (no transcript on this machine)' }
                Say "gate:       $gate"
                Say "red-check:  $red"
            } else {
                $n = if ($j.found) { "$(@($j.pairs).Count) incident documents" } else { "NOT RUN ($($j.reason))" }
                Say "incidents:  $n"
            }
        } catch {
            Fail "$ep did not answer: $($_.Exception.Message)"
            $failures.Add("$ep did not answer")
        }
    }

    # ---------------------------------------------------------------------------
    #  2. the browser half: no console error, no request off this machine
    # ---------------------------------------------------------------------------

    if ($NoBrowser) {
        Write-Host ''
        Warn 'SKIPPED: the browser check did not run (-NoBrowser). That is not a pass.'
    } elseif (-not $nodeCmd -or -not $npmCmd) {
        Write-Host ''
        Warn 'SKIPPED: node or npm is not on PATH, so the console-error and the' -ForegroundColor Yellow
        Warn 'external-request checks did not run. This is not a pass.'
    } else {
        Write-Host ''
        Say 'opening every page in a browser and watching the console...'

        $spaDir = Join-Path $repo 'web\spa'
        if (-not (Test-Path (Join-Path $spaDir 'node_modules'))) {
            Dim 'installing the console''s dependencies (once)...'
            $ErrorActionPreference = 'Continue'
            Push-Location $spaDir
            try {
                & npm ci --no-audit --no-fund 2>&1 | Out-Null
            } finally {
                Pop-Location
                $ErrorActionPreference = 'Stop'
            }
        }

        # The rehearsal drives the console's own Playwright, which is already a
        # dependency of the console: no new package, and the browser the
        # console's own tests use is the browser this checks.
        #
        # Two rules, and the difference between them matters:
        #
        #   - An UNCAUGHT EXCEPTION is always a failure, on every page.
        #   - A CONSOLE ERROR is a failure unless the browser is reporting a
        #     failed RESOURCE. "Failed to load resource: 400" is the browser
        #     saying that an endpoint the page deliberately probes - the
        #     database-backed clip catalog, on a machine with no database -
        #     answered with an error. The page handles it and says NOT RUN.
        #     Treating that as a defect would make the rehearsal fail on a
        #     correct page.
        #   - A REQUEST TO ANOTHER HOST is a failure, except on /live, whose
        #     subject IS a public stream: the page resolves a real public
        #     camera and analyses it. That is the one page that needs the
        #     internet, and the exemption is printed, never silent.
        $script = @"
const { chromium } = require('playwright');

const EXTERNAL_EXEMPT = new Set(['/live']);

(async () => {
  const base = process.argv[2];
  const paths = process.argv.slice(3);
  let browser;
  try {
    browser = await chromium.launch();
  } catch (e) {
    // The browser is not installed. That is a machine state, not a defect in
    // the demo, and it is reported as its own thing so the caller can print a
    // SKIP instead of a failure. A gate that cries wolf is a gate nobody runs.
    if (String(e).includes("Executable doesn't exist")) {
      console.log('NO_BROWSER');
      process.exit(2);
    }
    throw e;
  }
  const problems = [];
  for (const p of paths) {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    const errors = [];
    const external = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    page.on('pageerror', e => errors.push('UNCAUGHT: ' + String(e)));
    page.on('request', r => {
      const u = r.url();
      if (!u.startsWith(base) && !u.startsWith('data:') && !u.startsWith('blob:')) {
        external.push(u);
      }
    });
    try {
      // 'domcontentloaded', not 'networkidle'.
      //
      // The live page embeds a public stream, so its network is never idle and
      // 'networkidle' times out on a page that is working perfectly. A check
      // that fails on the page it is meant to check is a check that gets
      // ignored, so the wait is for the document, and the content is asserted
      // afterwards.
      const res = await page.goto(base + p, { waitUntil: 'domcontentloaded', timeout: 20000 });
      if (!res || res.status() !== 200) {
        problems.push(p + ': status ' + (res ? res.status() : 'none'));
      }
      // The hub's facts are written by script; give them a moment to land.
      await page.waitForTimeout(1500);
      const chars = await page.evaluate(() => (document.body.innerText || '').trim().length);
      if (chars < 40) {
        problems.push(p + ': the page rendered ' + chars + ' characters of text');
      }
      for (const e of errors) {
        if (e.includes('Failed to load resource')) continue; // a failed request, not a defect
        problems.push(p + ': console error: ' + e);
      }
      if (!EXTERNAL_EXEMPT.has(p)) {
        for (const u of external) problems.push(p + ': request off this machine: ' + u);
      } else if (external.length) {
        console.log('NOTE ' + p + ': ' + external.length + ' request(s) off this machine, which is what this page is for');
      }
    } catch (e) {
      problems.push(p + ': ' + String(e));
    }
    await ctx.close();
  }
  await browser.close();
  if (problems.length) {
    console.log('PROBLEMS');
    for (const p of problems) console.log(p);
    process.exit(1);
  }
  console.log('CLEAN');
})();
"@

        $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('vision-rehearsal-' + [System.Guid]::NewGuid().ToString('N') + '.js')
        [System.IO.File]::WriteAllText($tmp, $script, (New-Object System.Text.UTF8Encoding($false)))
        try {
            $paths = @($acts | ForEach-Object { $_.path }) + @('/console')
            # node resolves a module relative to the SCRIPT, and the script is
            # in the temp directory. NODE_PATH is how the console's own
            # node_modules is found from there: no copy of Playwright, no new
            # dependency, and the browser is the one the console's own tests
            # use.
            $env:NODE_PATH = Join-Path $spaDir 'node_modules'
            $ErrorActionPreference = 'Continue'
            $out = & node $tmp $BaseUrl @paths 2>&1
            $code = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            $out | ForEach-Object { Dim $_ }
            if ($code -eq 2) {
                Warn 'SKIPPED: Playwright''s browser is not installed, so the console-error and the' -ForegroundColor Yellow
                Warn 'external-request checks did not run. Run "npx playwright install chromium" in web/spa.'
                Warn 'This is not a pass.'
                $skipped = $true
            } elseif ($code -ne 0) {
                Fail 'the browser found problems on the pages:'
                $out | Where-Object { $_ -like 'PROBLEMS*' -or $_ -match '^\/' } | ForEach-Object { Fail "  $_" }
                $failures.Add('the browser check found problems')
            } else {
                Say 'the browser: no console error, no request off this machine.'
            }
        } finally {
            Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        }
    }
} finally {
    if ($proc -and -not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        Dim 'the rehearsal server stopped.'
    }
}

# ---------------------------------------------------------------------------
#  the verdict
# ---------------------------------------------------------------------------

Write-Host ''
if ($failures.Count -gt 0) {
    Fail "the rehearsal found $($failures.Count) problem(s):"
    foreach ($f in $failures) { Write-Host "    $f" -ForegroundColor Red }
    exit 1
}

if ($skipped) {
    Warn 'the rehearsal fetched every page and read the facts, but the browser half was SKIPPED.'
    Warn 'This is not a pass: the console-error and the external-request checks did not run.'
    exit 0
}

Say 'the rehearsal passed: every page of the track answered, the facts were readable, and the browser said nothing.'
exit 0
