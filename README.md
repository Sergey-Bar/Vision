# security-platform

Physical Security Intelligence Layer — Milestone 1.

One end-to-end operator workflow on a real XProtect installation:

```
XProtect event → canonical event → Security Graph → deterministic verification
  → VLM observation → incident → operator disposition → audit
```

with a complete offline install / upgrade / rollback story, and a deterministic
core that is fully functional with the AI plane completely absent.

## The law

Five invariants that the rest of the code exists to protect.

1. **Zero accepted event loss.** Anything that reaches `inbox_events` and is not
   explicitly dead-lettered reaches a terminal state. `kill -9` at any pipeline
   stage loses nothing and duplicates no side effect.
2. **Decisions replay from stored facts.** A decision made months ago is
   reproduced from stored facts and the stored `AIObservation`. The model is
   never re-run to explain a past outcome.
3. **A missing AI observation is never a negative observation.** AI is never on
   the availability and boot critical path. Any rule that needs a Tier C signal
   must declare `if_ai_unavailable` explicitly.
4. **The audit ledger is hash-chained and append-only.** Modifying any row fails
   checkpoint verification.
5. **Bundles are untrusted input.** Rule bundles, release archives and model
   bundles are all verified before execution. No shell, SQL, network, dynamic
   code or unbounded regex.

## Layout

| Path | Contents |
|---|---|
| `contracts/` | Protobuf `security/v1`, OpenAPI `v1`, JSON Schema. No language owns the schema. See [`contracts/README.md`](contracts/README.md). |
| `cmd/securityctl` | Operator and installer CLI. |
| `cmd/security-migrate` | Expand/contract migration runner. |
| `internal/` | Core: adapters, durability, graph, rules, verification, audit, identity, API, console. Start at [`internal/README.md`](internal/README.md). |
| `migrations/` | Ordered SQL. `expand` steps ship separately from `contract` steps. See [`migrations/README.md`](migrations/README.md). |
| `rules/builtin/` | The built-in bundle, shipped in the release archive. |
| `docs/` | Build status and threat model. Index: [`docs/README.md`](docs/README.md). |
| `docs/roadmap/` | The full plan. Start at [`docs/roadmap/README.md`](docs/roadmap/README.md). |
| `deploy/` | Windows Service, systemd, and the AI runtime. See [`deploy/README.md`](deploy/README.md). |
| `poc/active-monitoring/` | **A second Go module.** See below. Not part of this build. |

## Two trees, one repository

`poc/active-monitoring/` declares its own module path,
`github.com/Sergey-Bar/ai-captain-poc`, with its own `go.mod`, `go.sum` and
`scripts/verify.ps1`. It imports nothing from the module above and the module above
imports nothing from it.

This matters more than it looks. `go build ./...`, `go vet ./...`, `golangci-lint`,
`sonar-project.properties` and `scripts/verify.ps1` at the root **cannot** reach a
line of it: `./...` does not cross a module boundary. So a change under `poc/` is
not covered by this repository's gate and a change above is not covered by the POC's,
and each of those facts is silent unless somebody says it out loud.

The POC is a reference implementation with its own gate, kept for the reader who
wants to see a Milestone client that was written from Milestone's published
documents rather than from the parent platform's assumptions. It reached three of
the same conclusions about the parent platform's Milestone code independently —
including that `/api/v1` is not a Milestone prefix — which is recorded in
[`poc/active-monitoring/contracts/xprotect/README.md`](poc/active-monitoring/contracts/xprotect/README.md)
and in [`docs/vendor/milestone/route-audit.md`](docs/vendor/milestone/route-audit.md).

Run its gate separately:

```powershell
cd poc/active-monitoring; .\scripts\verify.ps1
```

Nothing merges between them. The POC does not import the platform's contracts, and
the platform does not read the POC's.

## Run

```sh
docker compose up
```

That is the whole demo. It brings up Postgres, applies the migrations, seeds a
populated site from the committed fixtures, and starts two cores — one doing the
pipeline work, one serving the console on **<https://localhost:8443/>**.

The certificate is self-signed, because an install has to mint one and this one did
it in the seed; the seed prints its SHA-256 fingerprint so you can check what the
browser warned you about. Sign in as `admin`, with the passphrase and the current
TOTP code the seed printed.

The seed's report is the thing to read first. It lists the incident ids it raised,
the SHA-256 of every clip it stored, which AI observations are degraded and why, and
which reviewer disagreed with which observation. `docs/status.md` is the source of
truth for what is built; the seed report is the source of truth for what is *in that
particular database*.

Three facts about that site, stated up front because a demo that hides them is not a
demo:

- **There is no camera, no VMS and no model behind it.** Every AI observation is
  either explicitly degraded with a named reason or labelled `seeded/absent`. An
  all-healthy site would look better and prove nothing, because a degraded
  observation not being a negative one is the invariant worth showing.
- **The console is published to loopback only.** A published `8443:8443` binds every
  interface, which on conference-hotel wifi puts the console in the DHCP range.
- **`docker compose down -v` is the rebuild.** The seed will not reset a site it has
  already reviewed, because `dispositions` is append-only and a reviewer's answer is
  not the seed's to delete. A second `docker compose up` finds the site populated,
  writes nothing, prints the same report and exits 0.

There is no seed flag that bypasses either. `securityctl acceptance` reports which
of the thirteen criteria this leaves unevidenced, and it reports `not-run` as a
distinct verdict from `pass`.

## Build

The toolchain is installed but **not on `PATH`** in a default Windows shell.
`go`, `protoc`, `golangci-lint` and `make` are all missing from it.

```powershell
$env:Path += ";C:\Program Files\Go\bin;$env:USERPROFILE\go\bin"
& "C:\Program Files\Go\bin\go.exe" build ./...   # must exit 0
```

```sh
make bootstrap     # fetch tools
make generate      # contracts -> generated code + compatibility check
make build
make lint
```

The product image builds with `CGO_ENABLED=0` and runs on
`gcr.io/distroless/static-debian12`, which has no shell — so its healthcheck is
`securityctl healthcheck`, a binary that is already in the image. There is no
`--insecure` on it; `--ca` names the certificate to trust and keeps verification on.

## Verify

```powershell
.\scripts\verify.ps1    # the gate: contract, vet, gofmt, test -race, migrations, rules
```

```sh
docker compose --profile verify run --rm verify     # the same gate, in a container
```

Both run `scripts/verify.ps1`. The container is not a second gate and not a Linux
fork of one: it is the same PowerShell under Debian, with the toolchain the image
carries and the working tree bind-mounted so it checks the tree you have rather than
a snapshot of it. That matters, because this gate has now found four defects that
only exist on Linux or only exist because nobody ran it — see
`docs/status.md`.

`make verify` is the same list of checks, but **`make` is not installed on this
machine** — not on `PATH`, not in Git Bash, not in WSL — so the PowerShell script
is the one that runs. It resolves `go` and `gofmt` itself, and it warns you when
`SP_TEST_DATABASE_URL` is unset, because the database-backed tests **skip** rather
than fail without it, and a green run that tested nothing is worse than a red one.

Windows uses `make` through Git Bash, or the equivalent `go` invocations in
`Makefile` directly from PowerShell. The database-backed tests **skip** rather
than fail when `SP_TEST_DATABASE_URL` is unset, so a green run can be a run that
tested nothing — `AGENTS.md` has the setup.

Coverage floors are per-platform. `internal/covercheck/floors.json` holds the
Windows numbers and `floors-linux.json` the Linux ones, because this repository has
a Windows-only source file with a unix sibling selected by build tag and so the two
platforms compile different statement sets out of identical source. Comparing a
Linux run against Windows floors produced 42 findings on an unchanged tree.

**One gate cannot pass in a container that is a view of a machine with local
worktrees.** `internal/treestate` resolves every registered git worktree with
`git rev-parse --git-dir`, and a worktree created by Agent Manager on Windows is
registered with a host-absolute `C:\...` path that does not resolve inside the
container. `git worktree list` calls it *prunable* there, and the gate reports it.
The compose entrypoint prints the worktree list before the gate runs so the cause is
next to the failure, and the fix is `git worktree prune` **on the host** — running it
in the container would delete the registration out from under Agent Manager. Every
other gate passes in the container.

## Status

Milestone 1 is in progress. `docs/status.md` is the source of truth for what is
built and what is not — not this file.

## Plan

[`ROADMAP.md`](ROADMAP.md) is the one-page entry point. The full set of
documents lives in `docs/roadmap/`: the M1–M8 calendar with dated exit gates, and
the track documents behind it — product, UX, QA, metrics, adapters, AI, release,
security, compliance, GTM, pricing, launch, risk, hiring and finance. It is
organised so that a non-engineer can navigate it, and it is readable by one.

The master roadmap's final section lists the fourteen code defects found while
writing the plan, two of which — a bundle check that verifies a digest rather
than a signature, and an adapter event-type table keyed on spellings
normalisation never emits — are worth fixing before anything ships.

## Evidence rule

Market claims, benchmark numbers and vendor comparisons in the source material
were never verified. Until they are, they are marked as unverified with a
source and a date, and are not asserted in the product, the docs or the code.
