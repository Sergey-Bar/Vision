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
                                # -p 1, migration-phases, rules-lint, rls-canary,
                                # ai-runtime
.\scripts\verify.ps1 -NoRace    # same, without the race detector
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
