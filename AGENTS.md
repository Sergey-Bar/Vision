# Working agreements

## Start here

| You want to | Read |
|---|---|
| The plan for the next six years | [`ROADMAP.md`](ROADMAP.md) |
| What is actually built | [`docs/status.md`](docs/status.md) |
| Where each package is and what owns what | [`internal/README.md`](internal/README.md) |
| The rules below | this file |

`docs/status.md` is the only source of truth for build state. The README and the
commit history are not. A stale claim in any other document is a defect.

## Environment — read this before running anything

The toolchain is installed but **not on `PATH`** on a default Windows shell.
This is the first thing that will waste your time.

| Tool | Where | On `PATH`? |
|---|---|---|
| `go` | `C:\Program Files\Go\bin\go.exe` | **No** |
| `gcc` (cgo, for `-race`) | `C:\msys64\mingw64\bin\gcc.exe` | **No** |
| `protoc` | `...\WinGet\Packages\Google.Protobuf_...\bin\protoc.exe` | **No** |
| `protoc-gen-go` | `$(go env GOPATH)\bin`, installed by `make bootstrap` | **No** |
| `golangci-lint` | `$(go env GOPATH)\bin` | **No** |
| `py`, `git`, `docker`, `winget` | — | Yes |

All five of the first were missing at some point, and the gate said so by
describing the absence rather than by fixing it. `scripts/verify.ps1` now
locates each one itself and, where a tool is genuinely absent, prints a visible
**SKIPPED** with the install command — never a quiet pass.

```powershell
# PowerShell, for this session
$env:Path += ";C:\Program Files\Go\bin;C:\msys64\mingw64\bin;$env:USERPROFILE\go\bin"
```

Verify before you trust anything:

```powershell
& "C:\Program Files\Go\bin\go.exe" build ./...   # must exit 0
& "C:\Program Files\Go\bin\go.exe" version        # 1.26.x; go.mod says 1.26.0
```

The module declares `go 1.26.0` and CI pins `GO_VERSION: "1.26"` with
`GOTOOLCHAIN: local`. A pin below the `go` directive is a hard build failure, so
if you change one you must change the other in the same commit.

### The database

The database-backed tests **skip** rather than fail when the environment
variable is absent, which means a green run can be a run that tested nothing.
Always confirm they actually ran.

```powershell
docker run -d --name sp-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres `
  -e POSTGRES_DB=security_test -p 55432:5432 postgres:16
$env:SP_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55432/security_test?sslmode=disable'
$env:SP_TEST_DATABASE_PASSWORD='postgres'
go run ./cmd/security-migrate up
go test -p 1 -count=1 ./...
```

`-p 1` is not optional. The database-backed suites share one database and
rebuild the schema, so package test binaries must not run concurrently; without
it one suite's `DROP SCHEMA` lands inside another's test and the failures look
like product bugs.

`go test -coverprofile=coverage` writes a plain file at the repo root. It is
ignored; do not stage it. Same for `__pycache__/`.

## Before writing code

- The plan of record lives in the milestone plan. Scope is locked; a new
  capability has to be justified as a platform primitive, not added ad hoc.
- Contracts are the source of truth. Change `contracts/` first, regenerate, and
  let `contractcheck` tell you whether you broke the wire. Never hand-edit
  `internal/contracts/`.
- Rule bundles, release archives and model bundles are untrusted input. If you
  are adding a code path that executes anything derived from them, the threat
  model owns the answer, not the convenience of the call site.
- An AI agent may not change an invariant, change a published contract, activate
  a capability, contact anyone, or commit to a date. It may write code, tests
  and documentation, and it may propose. A human signs every exit gate.
- Migrations, `contracts/`, anything touching an invariant, dependencies, auth,
  crypto and the rule language all require a human to read the diff before it
  lands.

## Invariants that tests must protect

These are the law. A change that weakens one needs an ADR and a test, in that
order.

1. Zero accepted event loss. Anything reaching `inbox_events` reaches a terminal
   state unless explicitly dead-lettered. `kill -9` at any stage: no loss, no
   duplicate side effect.
2. Decisions replay from stored facts and the stored `AIObservation`. A
   `replayedFromStoredFacts` response that re-inferred anything is a bug.
3. A missing AI observation is never a negative observation. Every rule that
   consumes a Tier C signal declares `if_ai_unavailable`.
4. The audit ledger is append-only and hash-chained. Editing any row must fail
   checkpoint verification.
5. `tenant_id` is `NOT NULL` everywhere and is never read from a request body.
   `site_id` is `NOT NULL` only on physically site-bound records.

## Conventions

- Time is UTC everywhere in storage. `observed_at` is what the source claimed,
  `received_at` is our ingest, `normalized_at` is drift-corrected and is the
  only field a time-window predicate may read.
- Go errors wrap with context; log lines carry `tenant_id`, `site_id` and
  `request_id`. No secrets, tokens, credentials or raw biometrics in logs. The
  audit ledger is separate from operational logs and is the authoritative one.
- SQL lives in `migrations/`, never inline in Go. Expand and contract ship
  separately; a contract step may never be bundled with the expand step that
  makes it safe.
- Migrations are forward-only. Never edit an applied migration; add a new one.
- A number in `docs/status.md` is derived from the file that defines it, not
  maintained by hand. `internal/statuscheck` fails when the document and the tree
  disagree, which it has done three times.

## The agent's own rules

`.kilo/skills/` holds skills that exist **only** in this repository, because each
one encodes a rule below that no general-purpose skill knows. They are committed
rather than installed, because `AGENTS.md` reserves changing an invariant to a
human who reads the diff, and a skill nobody can see is a skill nobody signs.

| Skill | What it is for |
|---|---|
| `red-check` | Proves a control actually fails when it is removed. This is the falsification step for every claim in `docs/status.md` and every row in §5 of the threat model — "a control with no such run is not a control; it is a comment". It is **not** TDD: `tdd` shows a test tracks new behaviour, this shows a test guards an existing invariant. **The `red-check` gate runs it mechanically** over a table of controls, and the mechanism is ported from the POC module's own gate, which has thirty of these. |
| `contract-first` | Enforces contract → owner → test. Carries the "where to add X" table, and states that the table in `internal/README.md` wins when they disagree. |
| `invariant-check` | Checks a change against the five invariants above before it is called done, including the ones a superuser test suite cannot see. |
| `milestone-probe` | How a `securityctl adapter-probe` report becomes a reviewed route-table change. DoD item 1 is the only open product item a person with a real installation can move today, and its whole content is that **the probe never clears its own `Unverified` flag**. |

A fourth thing that began as a fifth skill did not stay one: the drift check on
`docs/status.md` is a **test**, `internal/statuscheck`, because the repo's own
precedent is that this class of defect becomes "a test failure rather than a line
somebody notices while grepping".

**Red-check new controls.** Any change that adds an invariant, a schema `CHECK`,
a validation guard or a claimed protection carries a red-check, and the record
goes in `docs/status.md` with both transcripts. "Not red-checked, and here is what
would ship" is an acceptable answer; an unlabelled control is not. Where a control
is red-checkable by removing one line, it belongs in the `red-check` table in
`scripts/verify.ps1`, which is the part that keeps running. That table holds seven
entries — two observations, two evidence, three rules — against thirty in the POC
module. Entries are added only after being run by hand and watched go red, because
a table seeded with plausible mutations nobody has run reports thirty guarded
controls and proves none of them. **That rule has teeth: the sixth control tried
was the path-traversal segment regex, it turned out nothing tested it, and the
missing test case fell out of the attempt.**

**Third-party skills are untrusted input.** Sixty-five skills are installed on the
development machine from outside this repository, and a skill is read as
instructions by the thing making the decisions. `scripts/scan-skills.ps1` scans
them with NVIDIA SkillSpector and writes `dist/skills-audit/*.json`; the
`skills-audit` gate runs every verification and asserts that each record exists
and **postdates the newest skill in its directory**, so the record cannot go stale
while reading as current. It deliberately does not run the scan itself — a scan of
one 25-skill directory measured 6.3 minutes cold and 10.3 warm, and a forty-minute
gate is a gate that stops being run, which is how `lint` and `-race` became
decorative. The verdict is **advisory** and never fails the build; the
adjudication of each finding is recorded in §6b of `docs/threat-model.md`. A
finding with no adjudication there has not been judged, not been cleared. No skill
overrides this file.

## Testing

- Unit tests for rule compilation, semantic validation, time-window and skew
  logic, conflict precedence, the verification state machine, clock drift
  normalization, tenant scoping, RLS, and job lease/claim races.
- Golden tests are deterministic-only: fixture to canonical to verification to
  incident must be byte-stable. Never golden-test VLM prose.
- Any durability change needs a `kill -9` fault-injection test at the stage it
  touches. It is a DoD item, not a nice-to-have.
- Path traversal (`../`, absolute paths, null bytes, symlink escapes) needs an
  explicit negative test on every evidence path, on every change.

## Hebrew and RTL

**Not a requirement.** The Hebrew-first RTL operator surface in the original plan
has been dropped by product decision. Console templates are LTR, `lang="en"`,
and no template may carry per-element direction overrides, because there is no
second direction to override to. The operator surface is English.

Two consequences that are easy to get wrong:

- Time rendering is unaffected. A site's timezone is still the site's timezone and
  never the host's, and every rendered time carries an explicit offset. Dropping
  RTL does not mean dropping the site's clock.
- `sites.default_locale` **does not exist**. It was dropped by migration
  `0010_drop_default_locale.expand.sql`, which is the correct call: a locale is a
  rendering input, not a security control, and a schema column that no code
  reads is one more thing to keep in sync. A site that needs another locale gets
  it from its profile at render time, not from a column. If you are reading an
  older revision of this file that says the column "stays in the schema", that
  sentence was wrong.

## Verification

**`scripts/verify.ps1` is the gate. It runs locally, and it is the thing to
trust.**

```powershell
.\scripts\verify.ps1            # contract-check (three surfaces), contract-sync,
                                # vet, gofmt, lint, generated-code, test -race
                                # -p 1, red-check, migration-phases, sql-guard,
                                # skills-audit, rules-lint, rls-canary, ai-runtime
.\scripts\verify.ps1 -NoRace    # same, without the race detector
.\scripts\run-verify.ps1        # same, with the toolchain on PATH - none of go,
                                # gofmt, gcc, golangci-lint, protoc or
                                # skillspector is on PATH on a default shell
.\scripts\scan-skills.ps1       # the skill-supply-chain scan, which is slow
                                # (6-10 min) and so is not in the loop above
```

It exits non-zero naming whatever failed, and it resolves `go`, `gofmt`, `gcc`,
`golangci-lint` and `protoc` itself, because none of them is on `PATH` on a
default Windows shell.

`lint` and `generated-code` used to be named in this file and absent from the
script, because the tools they need were not installed and the absence was
described rather than fixed. All three tools are installed now and all three are
gates. golangci-lint's first run found 526 findings, among them a secrets file
written world-readable and a backoff expression that raised on its own failure
path. A gate listed in a document and not run is worse than one that is not
listed, because the document says the code was checked.

**`make verify` is the same list** and is what the `Makefile` and the roadmap
documents name, but **`make` is not installed here** — not on `PATH`, not in Git
Bash, not in WSL. The PowerShell script is the one that runs; the Makefile is
the portable description. If you install `make` they are equivalent.

Not covered here, and still required before a release: `golangci-lint`, and
regenerating the contracts with `protoc` and committing the result.

**GitHub Actions does not exist in this repository.** The pipeline was removed.
The reason is not that it was unhelpful: Actions cannot start a job for this
account, because the repository is private, it is on the free plan, and the
account's billing state blocks it. A pipeline that never runs is worse than no
pipeline, because a red X trains you to ignore red.

Consequences to be honest about:

- **No check runs on a pull request.** A green branch here is a human claim, not
  an automated one. Do not write "CI passed" anywhere; write which command ran.
- **No push protection.** Nothing stops a force-push to `main`, and nothing
  blocks a merge. `scripts/enable-branch-protection.sh` applies both the day the
  plan is upgraded — with the check list emptied, because there are no checks to
  require.
- **`docs/status.md`'s verification block describes a local run.** That is what
  it is; it is not pipeline evidence.

If CI is ever wanted back, it should be rebuilt deliberately with the
`permissions`, `concurrency` and SHA-pinning that the deleted pipeline had, plus
a Windows test job. Do not restore it by copying an old file back.

The suite must pass with **no Milestone server reachable**. If a test needs one,
the test is wrong. That rule is unaffected by any of the above.
