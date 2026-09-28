# M1 — Trust Spine Execution Plan

Owner: E1. Status: living. Last revised 2026-09-28. Window: **2026-09-28 → 2027-03-31**.

Read `` first (the capacity law in §6 constrains every
number here) and `` §"M1" (the exit gate this plan executes).
`docs/status.md` is the authoritative build state and is not restated here.

This is the plan the team executes this quarter. Every date in it traces to a gate
in ``; every claim about what is built traces to
`docs/status.md` or to the code.

---

## 1. Where M1 actually stands

Derived from `docs/status.md` §"What is not built, and why" plus a direct read of
the tree. Nothing in the "Open" column is a guess; the evidence column names the
file.

| # | Open item | Roadmap location | State today | Evidence | Why it is open |
|---|---|---|---|---|---|
| (a) | **Python VLM runtime** — Track F, items 25–27 | 01 §M1 gate 4 | Go side **complete and green**; the Python service is **not written** | `internal/observations/observations.go`, `internal/evaluation/evaluation.go` | The Go plane declares profiles (`M1Profiles`: `restricted-person` production, `door-forced` and `vehicle-at-gate` shadow), validates output against the profile field set, persists with full attribution, marks shadow profiles, and ships the gate harness. Nothing calls a model. `vlm_invocations_total` has no emitter. |
| (b) | **Windows Service + systemd units** — Track G, item 33 | 01 §M1 (implied by any real install) | **Absent.** `deploy/` does not exist | repo root; `docs/status.md` | Written against a real service manager, not guessed. Nothing in the tree depends on it, which is why it is cheap — and also why it has no natural forcing function until W3. |
| (c) | **`securityctl install / upgrade / rollback / backup / restore`** | 01 §M1 (any real install), 01 §M5 §5 (drill) | **Skeletons.** Each registers, has real usage text, refuses with `not implemented in this build` | `cmd/securityctl/commands.go:20-82`; `cmd/securityctl/main.go:105-109` | The release builder and verifier they consume exist and work (`internal/release`). The installer that consumes them does not. |
| (d) | **Milestone endpoint surface is entirely `Unverified`** | 01 §M1 gate 1, 01 §"Sequence dependencies" (M4 note) | **8 of 8 routes `Unverified: true`.** No Milestone server has ever been reachable from this codebase | `internal/milestone/routes.go:57-106`; `docs/threat-model.md` §6 R4 | **This is the single most important open item in the project.** Everything else in the pipeline is proven against synthetic fixtures; the one component that talks to the customer is a hypothesis. It is also the only open item whose failure mode is *silence* — a wrong field name produces an empty feed that looks like a quiet site, not a failing test. |
| (e) | **3 Definition-of-Done items that cannot be signed here** | 01 §M1 gates 1, 3, 4 | Open | `docs/status.md` §"Three Definition of Done items" | 1. Integration and replay parity — needs a real XProtect install. 2. Every SLO met under load — needs a dev install and a load generator. 3. A production VLM profile passing its gates — needs a model, a GPU, and a signed model bundle. All three are environmental, not code. |

### 1.1 Three blocking findings not in `docs/status.md`

These are code facts, verified, and they change the sequencing. Each has an owner
and a task ID in §2.

| # | Finding | Evidence | Consequence for M1 |
|---|---|---|---|
| F1 | **`securityctl adapter probe` does not exist.** `internal/milestone/routes.go:16` instructs the reader that "the first thing to do on a real installation is to run `securityctl adapter probe`", and `:121` exposes `UnverifiedRoutes()` explicitly so the command can print them. The command is absent from the tree at `cmd/securityctl/main.go:45-61`. | direct read | **This is the first task of M1.** It is the instrument that turns item (d) from a hypothesis into a list of confirmed and contradicted routes, and nothing in W1 can start without it. |
| F2 | **`internal/observability` has no production caller.** The only importer in the repository is `internal/observability/observability_test.go`. There is no scrape endpoint. | grep for `internal/observability` across all `*.go` returns 1 hit | Every metric in `metrics.go` is correctly *named* and correctly *evaluated by `Breaches`*, but nothing emits it. Gate 3 ("every SLO demonstrably met under a recorded load run") is not reachable from the current code. See `` §7. |
| F3 | **CI cannot build the module as pinned.** `go.mod` declares `go 1.26.0`; `.github/workflows/ci.yml:11-12` pins `GO_VERSION: "1.24"` with `GOTOOLCHAIN: local`; `docs/status.md` §"Local toolchain" says Go 1.27. | direct read of all three | Three asserted versions, none reconcilable. `GOTOOLCHAIN: local` with a 1.24 toolchain and a 1.26 module is a hard build failure. Every "green" claim in `docs/status.md` is a local claim until this is fixed. **One line, day 1.** |

### 1.2 What is *not* open, and must not be re-litigated

| Item | State | Do not |
|---|---|---|
| Deterministic spine (graph, rules, verification, incidents, replay) | green, 21 packages, `-p 1 -race`, real Postgres 16 | Re-open during M1. The only permitted change is a bug fix with a failing test first. |
| Audit ledger (trigger-stamped, hash-chained, sealed) | green; 7 controls with named failing tests | Weaken. Any weakening needs an ADR and a test, in that order (`AGENTS.md`). |
| Rule engine (schema → semantic validation → AST, no shell/SQL/network/regex) | green | Add a capability. A new operator is a new ADR. |
| Threat model (7 boundaries, 6 profiles, 24 abuse cases, 35 controls, 4 accepted risks) | green, blocking for the pilot | Add a boundary without a control that has a test. |
| Release builder + verifier (signed archive, SBOM, provenance, secret scan) | green | Trust it for the install path without the installer consuming it. |
| Recovery drill (demonstrates the restored-ledger-behind-the-seal finding) | green | Re-run without keeping the finding. It is designed to fail informatively. |

---

## 2. Workstreams

Nine workstreams. Capacity is 2 engineers × 26 weeks = **52 engineer-weeks**, of
which **20 engineer-weeks (38.5%) is the standing tax** defined in
`` §6 — support, demos, sales calls, incident
response, hiring, admin, and the AI review pass. The build allocation below totals
**32.0 engineer-weeks**, which is 61.5% and therefore inside the 60% line's
intent (the tax is spent on W9 and on the customer-facing half of W1/W5, and is
not double-counted in the build column).

Owner convention: **E1** = product, architecture, security, sales. **E2** = backend,
AI, data, release. This split is stated plainly and defended in
`` §1.

### W1 — Live-Milestone verification

| Field | Value |
|---|---|
| Owner | **E2** (adapter code), **E1** (site relationship, credentials, NDA) |
| Outcome | Every route in `internal/milestone/routes.go` is confirmed or corrected against a real server. `UnverifiedRoutes()` is empty or every remaining entry has a written reason. Live-vs-replay parity is demonstrated on the same scenarios. |
| Estimate | **4.0 ew** |
| Likely drafting agent | `adapter-verifier` (see `` §2), for `probe` scaffolding, the parity differ, and the route table. E2 owns every conclusion. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T01 | Write `securityctl adapter probe`: for each `Route`, call it, print HTTP status, returned content type, top-level JSON shape and key set **next to** what `Expect` declares; print `UnverifiedRoutes()` verbatim; exit non-zero if any route contradicts its `Expect` | — | Closes **F1**. E2 must read the diff; this is adapter code on the customer-facing boundary (B1 in the threat model) |
| M1-T02 | Fix CI Go version: `ci.yml` `GO_VERSION` → the version `go.mod` declares, or drop `GOTOOLCHAIN: local` and pin the toolchain. Reconcile with `docs/status.md` | — | Closes **F3**. Half a day. F1–T02 ship together on day 1 |
| M1-T03 | Secure a real XProtect site: named design partner, written agreement, network path from our host to the VMS API, a read-only integration credential, a named customer-side contact who can approve an event-type change | M1-T01 | **Longest lead time in the plan and the only item with an external dependency we do not control.** Start on day 1 regardless of code readiness. Gates W1, W5, W6 |
| M1-T04 | Run `probe` against the real server; record every route as confirmed / corrected / unreachable. Correct `routes.go` — `Expect`, `Notes`, pagination assumptions, `Query` parameter names | M1-T01, M1-T03 | The artifact is a dated `docs/verification/milestone-probe-<date>.md` with raw responses redacted by the existing fixture sanitizer |
| M1-T05 | Turn confirmed routes' `Unverified: true` into `false`, **one route at a time**, each in its own commit with the probe output attached | M1-T04 | `routes.go:118-129` says a test should assert the list shrinks. Add that test: `len(UnverifiedRoutes()) <= N` with a decreasing `N` and a named reason for each remaining entry |
| M1-T06 | Build the parity differ: run scenario S through the live path and through the fixture path in the same process, assert the canonical events are byte-identical after normalization | M1-T04 | `internal/milestone/adapter.go` already has one parsing path with a `Transport` seam. The differ is a test, not a second parser |
| M1-T07 | Parity run over all 12 committed scenarios (`testdata/fixtures/*.json` — 12 files plus `manifest.json`), then over a live capture sanitized by `internal/fixture/sanitize.go` | M1-T06 | **Gate 1 evidence.** The 12 scenario names are the plan of record for what parity means |
| M1-T08 | Write the parity report: per scenario, live-vs-fixture event counts, field-level diffs, and the canonical hash comparison | M1-T07 | Attach to `docs/status.md`; this is the artifact E1 and E2 sign |

**Definition of done (W1).** `UnverifiedRoutes()` returns an empty list, or every
entry names a specific reason it could not be reached. The parity report exists and
shows zero unexplained diffs across the 12 scenarios. `M1-T02` is merged. No
`internal/milestone` change was made without a probe output attached.

**Owner split, stated honestly.** E1 owns T03 entirely — nobody else can get a
customer to open their VMS to us — and therefore owns the schedule of this whole
workstream. E2 owns T01, T04–T08. If T03 slips, W1 slips, and W1 is on the
critical path. This is a single point of failure with one human behind it.

---

### W2 — Python VLM runtime

| Field | Value |
|---|---|
| Owner | **E2** |
| Outcome | A signed, offline-installable Python service that runs one profile on a real GPU, emits a schema-valid structured observation with full attribution, records a degraded row when it cannot, and can be stopped without affecting the core. |
| Estimate | **6.0 ew** (largest single workstream; see note on scope) |
| Likely drafting agent | `runtime-implementer` for the service skeleton, the schema-validation client, and the tests. E2 owns the model choice, the prompt, and the hardware profile. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T09 | Specify the core↔runtime contract as a **versioned interface**: request `{observation_id, tenant, site, incident, event, profile_id, prompt_template_id, prompt_version, input_evidence[]:{id, sha256, storage_ref}}`; response `{structured_output, insufficient_evidence, latency_ms, model, model_hash, runtime_version, hardware}` | — | This is a `contracts/` change (T14, W9, in-flight). It must go through `contracts/` first, not be hand-written into Go. The Python side never writes to `incidents`, `dispositions`, `rule_bundles`, `users` or `audit_ledger` — the `security_ai` grants in `migrations/0008` are the enforcement (threat model B4, control C5) |
| M1-T10 | Build the runtime service skeleton: health, readiness, one inference endpoint, graceful shutdown, structured logs carrying `tenant_id`/`site_id`/`request_id` | M1-T09 | **Startup must not block the core.** The core's readiness must be independent of this process |
| M1-T11 | Model bundle loader: verify the signed bundle before any inference; refuse a tampered bundle (threat model A4.3). Pin `model` + `model_hash` into every observation | M1-T10 | `ai_observations.model_hash` is `NOT NULL`; `observations.Validate()` at `internal/observations/observations.go:163-166` refuses an observation without it |
| M1-T12 | Prompt template with a pinned `prompt_template_id` + `prompt_version`, and a `output_schema_version` that matches `contracts/json-schema/restricted-person.schema.json` | M1-T11 | `structured_output` is validated against the profile's declared field set at `observations.go:363-381`. An undeclared field is refused. The JSON Schema in `contracts/` is the contract; the runtime must not carry a second copy |
| M1-T13 | Degradation paths: model unavailable, no runtime, timeout, error, absent. Each writes a **row** via the core (`observations.RecordDegraded`), never a log line | M1-T12 | `RecordDegraded` forces `insufficient_evidence = true` so a consumer that forgets to check fails closed. This is invariant 3 and it is tested (`rules.TestMissingAIObservationDegradesToUnverified`) |
| M1-T14 | GPU driver/hardware profile check at startup; record `hardware` on every observation; fail readiness, not the process | M1-T11 | `ai_observations.hardware` exists for exactly this |
| M1-T15 | Offline install: wheel bundle inside the release archive, no network at install or run, pinned hashes, `pip --no-index` semantics | M1-T10 | Threat model B6. The runtime is a supply-chain surface like everything else in the archive |
| M1-T16 | Runtime unit tests: schema validation, attribution completeness, degradation matrix, bundle tamper refusal, a deterministic replay that produces the same `output_hash` for the same inputs | M1-T15 | Golden tests are deterministic-only per `AGENTS.md`. **Never golden-test VLM prose** — golden-test the hash of the validated structured output |
| M1-T17 | Service unit for the runtime on Windows and systemd | M1-T10 | Shares W4's unit template |

**Definition of done (W2).** One production profile (`restricted-person`) runs on
a real GPU against real evidence and writes attributed, schema-valid rows. The
matrix in `ai_observations.degraded_reason` is exercised by a test. The core boots,
serves incidents, and disposes with the runtime process killed — demonstrated, not
asserted. Installation needs no network.

**Scope note.** 6.0 ew for a full runtime is tight and assumes a single profile, a
single model family, and no fine-tuning. It does **not** include a model
evaluation UI, a prompt-optimisation loop, or a second runtime language. If it
overruns, the reduced cut is: `restricted-person` only, no shadow profiles served
by the runtime (shadow evaluation runs from the harness against recorded evidence
instead).

---

### W3 — Installer lifecycle

| Field | Value |
|---|---|
| Owner | **E2** |
| Outcome | `install`, `upgrade`, `rollback`, `backup`, `restore` are real, offline, transactional, and refuse with a named reason. The expand/contract rule is enforced in code, not in a review checklist. |
| Estimate | **5.0 ew** |
| Likely drafting agent | `installer-implementer` for the state machine, the file layout, and the tests. E2 owns the failure semantics. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T18 | Define the on-disk install layout and the install state file, and write the state machine: `absent → installed → upgrading → installed`, with `rollback → installed(previous)`, and `failed` as a terminal-per-attempt state that leaves the previous install runnable | — | "Leaves the previous install runnable on any failure" is the requirement already written in `commands.go:38-47` |
| M1-T19 | `install`: verify-release → SBOM check → migration compatibility window → evidence dir exclusivity → secret-shaped material refusal → extract → migrate up → create service accounts and bind credentials from the secret store | M1-T18 | Every precondition is already a named check in `securityctl preflight` (`cmd/securityctl/ops.go:111-165`). `install` should *call* preflight, not re-implement it |
| M1-T20 | `upgrade`: backup first, apply **expand steps only**, refuse an archive containing a contract step, refuse an unknown migration phase, leave the previous binary runnable on any failure | M1-T19, M1-T21 | `migrations.TestM1ContainsNoContractStep` (C31) already asserts M1 contains no contract step. The installer must refuse one on the same grounds |
| M1-T21 | Migration phase enforcement in the installer: filenames carry a phase, the runner refuses an unknown one, and an archive that bundles expand+contract is rejected | M1-T19 | Already true of the runner; the installer must not become a second path that applies raw SQL |
| M1-T22 | `rollback`: restore the previous install, never downgrade the schema by itself, refuse when the previous binary cannot read the current schema version | M1-T20 | The refusal is the point. A half-downgraded site is the state this product must never be in |
| M1-T23 | `backup`: database + evidence directory + checkpoint material, self-consistent, refuses a partial backup unless told explicitly | M1-T18 | `commands.go:64-70` already states this refusal. Backing up the DB without evidence produces an archive whose incidents reference bytes that do not exist |
| M1-T24 | `restore`: restore what `backup` produced; re-run the chain verification; report the restored-ledger-behind-the-seal gap rather than hiding it | M1-T23 | Reuse the finding `internal/dr/drill_test.go:196-220` already demonstrates. The restore path must **surface** the gap; a restore that quietly reports success is a lie |
| M1-T25 | Fault-injection tests for install/upgrade/rollback at the stage each touches: kill mid-extract, kill mid-migration, kill mid-outbox-drain, kill mid-seal | M1-T19, M1-T20, M1-T22 | `AGENTS.md`: "Any durability change needs a `kill -9` fault-injection test at the stage it touches. It is a DoD item." These are the newest durability code in the project |
| M1-T26 | Installer runbook with the exact commands, asserted by a test the way `TestDrillRunbookNamesRealCommands` asserts the DR runbook | M1-T25 | Reuse the existing pattern at `internal/dr/drill_test.go:306-323` |

**Definition of done (W3).** All five commands perform their stated action or
refuse with a named reason; none reports success without having done the thing. The
expand/contract refusal is tested. Upgrade is transactional and its failure paths
have `kill -9` coverage.

---

### W4 — Service units and packaging

| Field | Value |
|---|---|
| Owner | **E2** |
| Outcome | Windows Service and systemd unit definitions, written against a real service manager, with restart policy, ordered start/stop, log routing, and an offline install path. |
| Estimate | **1.5 ew** |
| Likely drafting agent | `packaging-implementer`. E2 installs and restarts it on both platforms before it is done. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T27 | `deploy/windows-service` — service definition, restart on failure, delayed auto-start, dependencies, service account, log directory | M1-T19 | Written against a real service manager, per `docs/status.md`. Not guessed from documentation |
| M1-T28 | `deploy/systemd` — unit, `Restart=on-failure`, `After=`/`Requires=` ordering, `RuntimeDirectory`, `StateDirectory`, `ProtectSystem`/`PrivateTmp` as far as the evidence directory permits | M1-T19 | The evidence directory is the one path that must be writable; hardening that would break evidence is a bug, and the test should catch it |
| M1-T29 | Unit for the Python runtime, and a unit or timer for the checkpoint sealer | M1-T17, M1-T27 | One template, two consumers. Do not write two unit systems |
| M1-T30 | Package the units into the signed release archive with correct manifest entries | M1-T27, M1-T28, M1-T31 | A file in the archive that is not in the manifest fails `verify-release` |
| M1-T31 | Release manifest/SBOM updated to cover runtime wheels, unit files, and the model bundle slot | M1-T30 | `internal/release` already refuses a path escaping the archive root and secret-shaped material. Extend, do not bypass |
| M1-T32 | Restart-under-load and reboot-recovery test on both platforms: a restart mid-incident loses no accepted event and duplicates no side effect | M1-T27, M1-T28, M1-T25 | This is invariant 1 tested at the service boundary, which is the boundary a real site actually exercises |

**Definition of done (W4).** The platform starts on boot on both platforms, survives
a `kill -9` of the core, the runtime, and the database in any order, and is
installed entirely from a signed offline archive. The M1 exit-gate run happens on a
machine brought up by these units, not by a developer shell.

---

### W5 — Design-partner integration, load and SLO demonstration

| Field | Value |
|---|---|
| Owner | **E1** (relationship, operator, demonstration), **E2** (load generator, instrumentation) |
| Outcome | One real site running the platform from a signed archive; one incident class completed end to end by a real operator and timed; every SLO in `internal/observability/slo.go` demonstrated under a recorded load run with the run and its numbers in the release notes. |
| Estimate | **5.0 ew** |
| Likely drafting agent | `load-harness` for the generator and the recording; `dashboard-builder` for the operator view. E1 owns the operator and the timer; E2 owns the numbers. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T33 | **Wire `internal/observability` into the running system** (closes **F2**): construct the `Registry` in the core, emit at every named call site in `metrics.go`, expose a scrape endpoint, and wire `Breaches()` to a surface an operator reads | — | **Precondition for gate 3 and for the whole metrics program.** Nothing in the tree emits a metric today |
| M1-T34 | Load generator: replay a recorded/synthetic stream at a declared rate against a dev install; the rate is declared in the release notes, not chosen after the fact | M1-T33, M1-T31 | SLO 5 (`ingest_backlog`) breaches above 50,000 in inbox/outbox/jobs. The run must be recorded with the rate, the duration, and the machine |
| M1-T35 | Instrumented SLO run: capture every SLO in `slo.go` — `ingest_availability`, `ingest_latency` (p95 < 5s), `incident_visibility` (p95 < 15s), `ai_plane_availability`, `ingest_backlog` (soft 5,000 / hard 50,000), `correlation_under_drift` (<1% severe), `dead_letter_visibility` (≤60s), `evidence_disk` (10% free, warn 25%) — under that recorded load | M1-T34 | **Gate 3 evidence.** Every SLO already carries a remedy in code; the run must quote the remedy, not just the number |
| M1-T36 | Install the platform at the design-partner site from the signed archive, via the installer, onto the service units | M1-T19, M1-T27, M1-T31, M1-T03 | The first real install is also the first real test of the installer. Budget for it to find something |
| M1-T37 | Pick the one M1 incident class with the customer. The built-in bundle's production rule requires two independent signals (accepted risk R2), so the chosen class must be corroborable at that site | M1-T03, M1-T36 | **Do this before W6, not after.** The profile is chosen to match the class the site can actually produce |
| M1-T38 | End-to-end timed run with a real operator: event → graph → deterministic verification → VLM observation → incident → operator disposition → sealed audit checkpoint. One session, one stopwatch, recorded | M1-T37 | **Gate 2 evidence.** The timer is the customer's operator, not us. Record the elapsed time, the number of operator actions, and whether the operator preferred it to the VMS workflow — that last one is the mission of M1, not a gate |
| M1-T39 | Seal a checkpoint at the site and verify it against a trusted public key fetched out of band | M1-T36 | B7. A seal stored where the DB owner can rewrite it is a claim, not evidence |
| M1-T40 | Operator walkthrough: an operator can explain, from the product alone, why the system did what it did | M1-T38 | This is the M3 gate restated early. Doing it in M1 is how we find out it is not true |

**Definition of done (W5).** A real site, real events, a real operator, a real
timing, a recorded load run, and a signed checkpoint verified off-box. The release
notes for the M1 build carry the load rate and every SLO reading.

---

### W6 — Production VLM profile promotion

| Field | Value |
|---|---|
| Owner | **E2** (evaluation, gates), **E1** (go/no-go, customer communication) |
| Outcome | `restricted-person` in shadow mode at the design-partner site for ≥14 consecutive days, evaluated against `evaluation.DefaultGates()` with the report attached, and a human decision on promotion. |
| Estimate | **2.5 ew** + **14 calendar days of clock** |
| Likely drafting agent | `evaluation-runner` for the shadow harness and the report. E2 signs the numbers; only a human signs the promotion. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T41 | Shadow-mode wiring: a shadow profile writes observations and creates no incident and suppresses nothing | M1-T12, M1-T36 | `observations.IsProductionProfile` already gates `production_profile`; the runtime must be told which mode it is in |
| M1-T42 | Label pipeline: harvest cases from the site, map operator dispositions to labels, and persist them so `evaluation.Evaluate` can read them | M1-T41, M1-T38 | The labels come from `dispositions.outcome`. A case nobody judged is not evidence and is counted as `undispositioned`, never folded into a rate (`evaluation.go:29-35`) |
| M1-T43 | Run the harness continuously; emit a per-profile report: `Labeled`, `Undispositioned`, `TruePositives`, `FalsePositives`, `Benign`, `Degraded`, `Insufficient`, `DetectedRestricted`, `MissedRestricted`, FPR, recall, disagreement rate, median latency, hours-to-disposition | M1-T42 | Every failing gate is **named**, not averaged (`evaluation.go:259-289`) |
| M1-T44 | Meet `DefaultGates()`: FPR ≤ 0.20, recall ≥ 0.70 on true positives, median latency ≤ 8000 ms, disagreement ≤ 0.25, ≥30 labelled cases | M1-T43, **≥14 days** | These are the numbers in `evaluation.DefaultGates()`. They are not invented here |
| M1-T45 | Attach the evaluation report; record the 14-day start and end dates and the continuous-day count | M1-T44 | **Gate 4 evidence.** "14 consecutive days" means 14 consecutive calendar days with the profile running and the site live. A gap resets the clock |
| M1-T46 | Human promotion decision, recorded in the ledger with `actor_type='admin'` | M1-T45 | The harness says which profiles are *eligible*. Promotion is a human act (`evaluation.go:139-141`) |

**Definition of done (W6).** 14 consecutive days, shadow mode, no authoritative
effect, a report that names each gate it met or failed, and a signed human decision
either way. **A profile that fails its gates in M1 is a successful M1**: the gate
that does not fire is not evidence.

---

### W7 — Console and UX hardening

| Field | Value |
|---|---|
| Owner | **E1** |
| Outcome | The operator surface is usable by a real operator on a real site without us in the room: every rendered time carries an explicit offset in the site's timezone, the incident shows its state rather than implying completeness, degraded incidents are visibly degraded, and the CSP stays strict. |
| Estimate | **2.0 ew** |
| Likely drafting agent | `console-drafter` for templates and tests. E1 owns every visual and wording decision. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T47 | Time rendering audit: every timestamp renders in `sites.timezone` with an explicit offset; `dir="ltr"` and `lang="en"` assertions stay; no per-element direction overrides | — | `AGENTS.md` §"Hebrew and RTL": dropping RTL does not drop the site's clock. `0001` keeps `sites.timezone NOT NULL`; `0010` removed `default_locale` |
| M1-T48 | Incident state is stated, never implied: the operator sees `open / in_review / dispositioned / closed`, the verification state, and whether the incident is still assembling (accepted risk R3) | M1-T33 | R3 is the risk that bites first in a demo: an operator acting on the last 15 seconds is acting on an incomplete picture |
| M1-T49 | Degradation is visible on the incident: a degraded incident is visibly degraded, with the reason, and never reads as "nothing found" | M1-T13 | Invariant 3, made legible. `incidents.degraded` and `incidents.degraded_reasons` already exist |
| M1-T50 | Evidence completeness: every incident shows what it has, what it is missing, and the retrieval status of each item | M1-T36 | An incident without its evidence is not defensible (`slo.go` evidence_disk remedy) |
| M1-T51 | Dead-letter list reachable and replayable from the console within 60 seconds of the dead letter | M1-T36 | SLO 7. Replay is an operator action, gated by `CapDeadLetterRead` which is deliberately separate from incident triage |
| M1-T52 | CSP stays strict; no inline script; re-run the XSS negative test on every template change | M1-T47 | Threat model A3.4. The server-rendered console exists partly to make this achievable |

**Definition of done (W7).** A real operator completed the M1 workflow in M1-T38
using only the console, with no help and no console access to a shell. E1 signed
the walkthrough.

---

### W8 — QA and fault-injection completion

| Field | Value |
|---|---|
| Owner | **E2** |
| Outcome | Every durability stage that M1 touches has a `kill -9` test; every evidence path change has its traversal negative test; golden tests stay deterministic-only; the full `make ci` target is green. |
| Estimate | **2.0 ew** |
| Likely drafting agent | `fault-injection-writer` for the test scaffolding. E2 owns the DoD claim. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T53 | `kill -9` coverage at every stage M1 adds or touches: intake, normalize, outbox relay, job lease, VLM runtime, evidence fetch, checkpoint seal, install, upgrade | M1-T25, M1-T32 | Per stage: no accepted event lost, no duplicate side effect. Invariant 1 |
| M1-T54 | Path-traversal negative test on every evidence path touched, on every change: `../`, absolute, backslash, drive letter, NUL, symlink escape | M1-T23 | The database CHECK is half; the store is the other half; both are tested. A NUL test belongs to the evidence store because Postgres rejects it at the type level |
| M1-T55 | Re-run the full threat-model control suite: 35 controls, each mapped to its failing test | — | A control with no failing test is a claim |
| M1-T56 | Golden suite stays deterministic-only: fixture → canonical → verification → incident, byte-stable. **No VLM prose in any golden file** | M1-T16 | `AGENTS.md` §Testing |
| M1-T57 | `scripts/verify.ps1` green with no Milestone server reachable | M1-T02 | `contract-check contract-sync vet lint test-race migration-phases rules-lint`. The suite must pass with no Milestone server reachable; if a test needs one, the test is wrong |
| M1-T58 | M1 branch: one signed commit per closed DoD item, each referencing the test that proves it | M1-T55 | `docs/status.md` is updated in the same commit, or it goes stale within a week |

**Definition of done (W8).** `make ci` green; the fault-injection matrix complete
for M1's stages; `docs/status.md` says what is true on the day it is read.

---

### W9 — Docs, compliance and sales enablement

| Field | Value |
|---|---|
| Owner | **E1** (customer-facing, compliance posture), **E2** (technical docs, runbooks) |
| Outcome | The three standing-answer documents exist and match the code; the pilot paperwork exists; a non-founder can run a demo. |
| Estimate | **4.0 ew** |
| Likely drafting agent | `docs-writer` for first drafts. E1 owns every claim that reaches a customer. |

| ID | Task | Deps | Notes |
|---|---|---|---|
| M1-T59 | The three buyer answers, as documents that match the code: what the AI decided and why; what happens when the AI is down; can you prove it did what it says | M1-T13, M1-T39, M1-T46 | `` §4. Every claim in them is traceable to a test or a table |
| M1-T60 | Trust documentation for a security buyer: the threat model, the five invariants, the accepted risks stated plainly, the control-to-test map | M1-T55 | Security buyers detect unsupported claims as a profession. Accepted risk R4 (synthetic fixtures are not captures) is closed by W1 and must be updated when it is |
| M1-T61 | Architecture doc (`README.md` §Layout promises one and it does not exist) and the operational runbook (`README.md` §Layout promises one; only the DR drill's inline runbook exists) | M1-T26, M1-T58 | **Inconsistency found while writing this plan:** `README.md` lists `docs/` as "Threat model, architecture, operational runbook" and neither of the latter two exists |
| M1-T62 | Pilot agreement template: named reference, measured before/after investigation time, anonymised incident data for evaluation, exit terms, no SLA we cannot keep | M1-T03 | The M1 commercial state is design partners only, up to three, at no charge (`01` §M1). Getting the data rights in writing before the data exists is the whole point |
| M1-T63 | Data-handling note: what leaves the site for evaluation, what does not, retention, anonymisation, regional policy | M1-T62 | The Trust and Security parallel track (`01` §"The two parallel tracks") requires this from M1 |
| M1-T64 | Demo script and a 20-minute walkthrough a non-founder can run without notes | M1-T40, M1-T48 | If only a founder can demo it, it is not a product, it is a presentation |
| M1-T65 | The contract/AI-runtime contract change (M1-T09) lands in `contracts/` first, regenerates, and passes `contractcheck` | M1-T09 | Never hand-edit `internal/contracts/`. A breaking change fails the build (`C29`) |

**Definition of done (W9).** Every doc a buyer or an operator is handed exists,
matches the code, and every claim in it names its evidence. The pilot agreement is
signed before the first byte of customer data is collected.

---

## 3. The 26-week calendar

Window 2026-09-28 (Monday, ISO **2026-W40**) → 2027-03-31 (Wednesday, ISO
**2027-W13**). That is 13 ISO weeks in 2026 plus 13 in 2027, the last of which
contributes Monday–Wednesday. This is exactly two quarters of the 13-week unit
that `` §6 defines, which is the check that the
calendar respects the capacity law.

**Legend.** `W#` workstream active · `■` demo/pilot/customer week (the visible
40%) · `◆` gate or checkpoint · `▲` the 14-day shadow clock is running.

| ISO week | Dates | Mon | Tue | Wed | Thu | Fri | Active | Note |
|---|---|---|---|---|---|---|---|---|
| 2026-W40 | 09-28 → 10-02 | W1 | W1 | W1 | W1 | W1 | W1, W9 | M1 starts. T01 probe, T02 CI fix, T03 site approach. Also: ADR for the M1 architecture decisions |
| 2026-W41 | 10-05 → 10-09 | W1 | W1 | W2 | W2 | W2 | W1, W2, W9 | T03 must be closed or the schedule is already in trouble. T09 contract spec |
| 2026-W42 | 10-12 → 10-16 | W1 | W1 | W2 | W2 | W2 | W1, W2 | T04 probe run begins. T10 runtime skeleton |
| 2026-W43 | 10-19 → 10-23 | W1 | W1 | W2 | W2 | W2 | W1, W2, W8 | T05 route confirmation. T13 degradation paths. T55 control suite |
| **2026-W44** | **10-26 → 10-30** | **■** | **■** | **■** | **■** | **■** | W1, W5, W9 | **Demo/pilot week 1.** First end-to-end on a live server if T04–T06 are done. Four calls, one design-partner workshop, 40% of capacity to the customer |
| 2026-W45 | 11-02 → 11-06 | W2 | W2 | W2 | W3 | W3 | W2, W3 | T11 model bundle, T12 prompt, T18 install state machine |
| 2026-W46 | 11-09 → 11-13 | W3 | W3 | W3 | W2 | W2 | W2, W3, W8 | T19 install, T53 fault-injection at intake/normalize |
| 2026-W47 | 11-16 → 11-20 | W3 | W3 | W3 | W3 | W4 | W3, W4 | T20 upgrade, T21 expand/contract refusal, T27/T28 units |
| 2026-W48 | 11-23 → 11-27 | W4 | W4 | W3 | W8 | W8 | W3, W4, W8 | T22 rollback, T30/T31 packaging, T57 `make ci` green |
| 2026-W49 | 11-30 → 12-04 | W5 | W5 | W3 | W3 | W8 | W3, W5, W8 | **T33 observability wiring** — the gate to everything measurable. T23/T24 backup/restore |
| 2026-W50 | 12-07 → 12-11 | W5 | W5 | W5 | W5 | W5 | W5, W7 | T34/T35 load run + SLO demonstration. **Gate 3 candidate** |
| **2026-W51** | **12-14 → 12-18** | **■** | **■** | **■** | **■** | **■** | W5, W7, W9 | **Demo/pilot week 2.** Site install (T36) from a signed archive onto the units. Incident class chosen (T37). 40% to the customer |
| 2026-W52 | 12-21 → 12-25 | W7 | W7 | W8 | — | — | W7, W8 | T47–T52 console hardening. Year-end freeze on scope, not on work |
| 2027-W01 | 01-04 → 01-08 | W6 | W6 | W7 | W7 | W8 | W6, W7, W8 | T41 shadow wiring, T42 label pipeline, T25/T26 install fault injection + runbook |
| 2027-W02 | 01-11 → 01-15 | W6 | W6 | W5 | W5 | W5 | W5, W6 | T43 harness. **T45 shadow clock must be starting** |
| 2027-W03 | 01-18 → 01-22 | W6▲ | W6▲ | W6▲ | W6▲ | W6▲ | W6 | **Shadow clock week 1.** ▲ Target start 2027-02-19, giving 5 weeks of float |
| **2027-W04** | **01-25 → 01-29** | **■** | **■** | **■** | **■** | **■** | W6, W5, W9 | **Demo/pilot week 3.** Timed end-to-end run with a real operator (T38) while the shadow clock runs. Checkpoint seal + verify (T39). 40% to the customer |
| 2027-W05 | 02-01 → 02-05 | W7▲ | W7▲ | W7▲ | W7▲ | W7▲ | W6▲, W7 | T40 operator walkthrough. T59–T64 docs and pilot paperwork |
| 2027-W06 | 02-08 → 02-12 | W7▲ | W8▲ | W8▲ | W8▲ | W8▲ | W6▲, W8 | T53–T58 final QA pass. Docs through E2's review |
| 2027-W07 | 02-15 → 02-19 | W6▲ | W6▲ | W6▲ | W6▲ | W6▲ | W6 | **Day 14 of the shadow clock at the latest.** T44 gate check |
| 2027-W08 | 02-22 → 02-26 | W6▲ | W6▲ | W6▲ | W6▲ | W6▲ | W6 | **Last date the shadow clock may start and still end by 2027-03-31.** Float is now negative |
| 2027-W09 | 03-01 → 03-05 | W9 | W9 | W9 | W9 | W9 | W9 | Gate artefacts assembled. E1 + E2 pre-read of the exit gate. No new work |
| 2027-W10 | 03-08 → 03-12 | W9 | W9 | W8 | W8 | W8 | W8, W9 | Final fault-injection pass. `docs/status.md` rewritten to the truth |
| 2027-W11 | 03-15 → 03-19 | ◆ | ◆ | ◆ | ◆ | ◆ | — | **Exit gate week.** Hard stop on new work. Every gate line signed or the reduced-scope cut is invoked |
| 2027-W12 | 03-22 → 03-26 | ◆ | ◆ | ◆ | ◆ | ◆ | — | Evidence attached, signatures collected. M2 planning opens |
| 2027-W13 | 03-29 → 03-31 | ◆ | — | — | — | — | — | 2027-03-31. M1 closes. Absolute last date a 14-day shadow clock may start is **2027-03-18** |

### 3.1 The three demo/pilot/customer weeks

**2026-W44 (10-26 → 10-30), 2026-W51 (12-14 → 12-18), 2027-W04 (01-25 → 01-29).**

In each of these, roughly **40% of both engineers' capacity goes to the customer**:
the demo, the workshop, the calls, and the follow-up written up afterwards. This is
stated in two places because it is easy to misread: 40% is the *standing quarterly
average* from `00` §6, and these three weeks are where it is **concentrated and
visible**. The other 13 weeks are not 100% build — they carry roughly 35% of the
same tax, spread thin across support, incident response, hiring, admin, and the AI
review pass. Nobody gets a week that is 100% build, which is the failure mode that
kills two-person companies.

If a demo week is at risk, **the demo is cut, not the engineering** — unless the
demo is the gate, in which case the engineering is cut per §4.

---

## 4. Critical path and float

### 4.1 The chain

```
M1-T03  real XProtect site, credential, network path
   │
   ├─► M1-T01 securityctl adapter probe            [0.5 ew]
   │      └─► M1-T04 run probe, correct routes      [0.5 ew]
   │            └─► M1-T06 parity differ            [0.5 ew]
   │                  └─► M1-T07 parity run         [0.5 ew]
   │                        └─► M1-T08 report  ══► GATE 1
   │
   └─► M1-T18 install state machine                [0.5 ew]
          └─► M1-T19 install                       [1.0 ew]
                └─► M1-T20 upgrade + T21 phase    [1.0 ew]
                      └─► M1-T22/T23/T24 rollback, backup, restore [1.5 ew]
                            └─► M1-T27/28 units    [0.75 ew]
                                  └─► M1-T31 packaged, signed  [0.5 ew]
                                        └─► M1-T36 install at the site  [0.5 ew]
                                              │
   M1-T09 contract spec ─► M1-T10..T12 runtime ──┤
                                              ├─► M1-T41 shadow wiring
                                              │      └─► ▲▲▲ 14 CONSECUTIVE DAYS ▲▲▲
                                              │            └─► M1-T44 gates ══► GATE 4
                                              │
                                              └─► M1-T37 incident class chosen
                                                    └─► M1-T38 timed run ══► GATE 2

   M1-T33 observability wiring ─► M1-T34 load generator ─► M1-T35 SLO run ══► GATE 3
   (runs in parallel from 2026-W49; not on the longest chain, but it is the
    longest chain to GATE 3 and it starts late)
```

### 4.2 The single longest chain

**T03 → T19 → T27/31 → T36 → T41 → 14 days → T44.** Six sequential steps, four of
which are calendar time rather than effort, and the last of which is a 14-day clock
that **cannot be compressed by adding people, money, or hours**. The engineering in
that chain totals roughly 4.5 engineer-weeks spread over 12 calendar weeks. The
14 days is the part that actually decides the date.

Consequences, stated so nobody is surprised in February:

- The **14-day shadow clock is the binding constraint on the M1 date**, not the
  Python runtime and not the installer. If the clock starts late, the date moves.
- The clock's **last legal start is 2027-03-18**. The *target* start in §3 is
  **2027-02-19** (2027-W07), which leaves 5 weeks of float for a restart.
- A "consecutive day" means the profile was running and the site was live. **A gap
  resets the clock.** This is why W6 has 6 calendar weeks of calendar for 2.5
  engineer-weeks of work.
- Anything that makes the profile stop — a model bundle rejection, a GPU driver
  event, a site power event, a runtime crash loop — costs 14 days. The runtime
  must be boring. This is an argument for M1-T14 failing readiness rather than
  crashing, and for M1-T53 covering the runtime in the fault-injection matrix.

### 4.3 Float per item, and what a slip does to the exit gate

| Chain step | Planned | Float | If it slips by 1 week | If it slips by 2+ weeks |
|---|---|---|---|---|
| **M1-T03** site access | 2026-W40 | **3 weeks** | GATE 1 and the whole shadow clock shift. Everything in W5/W6 shifts with it | Reduced scope. `01` §M1: "Ship with ONVIF-agnostic synthetic fixtures and a demo site, and defer the live-Milestone parity proof to M2." **Gate 4 is not cuttable by this clause** — it needs a real site. If no real site exists by 2026-W44, the M1 date moves and that is the decision |
| M1-T01 probe | 2026-W40 | 0 | T04 cannot start. Gate 1 slips one-for-one | It is half a day of work. If it is late in W40, something else is wrong and should be looked at |
| M1-T19 install | 2026-W46 | 1 week | Gate 3 needs an installed system; load run slips | T36 at the site slips; shadow clock slips. **This is the second most dangerous item after T03** |
| M1-T27/28 units | 2026-W47 | 1 week | M1-T32 restart test slips; the site install happens on a developer shell instead, which is a weaker gate | Accept a developer-shell install for the pilot, but **not** for gate 3 or gate 4 evidence. Record the substitution |
| M1-T31 packaging | 2026-W48 | 0.5 week | T36 slips | T36 slips. A site running from a shell is not a demonstration of an offline install |
| M1-T36 site install | 2026-W51 | **1 week** | Shadow clock start slips one-for-one. Still inside the 5-week float | Clock start slips into 2027-W08, which is the last legal week. Beyond that, gate 4 fails on the date |
| **Shadow clock start** | 2027-W03 target / **2027-W08 last legal** | **5 weeks** | Consumed. Nothing else absorbs it | **Gate 4 fails.** The response is not more hours: it is a date change, agreed in writing by E1 and E2, or the reduced-scope cut invoked explicitly |
| M1-T33 observability | 2026-W49 | 0 | T34/T35 slip. **Gate 3 is unreachable until this is merged** | Gate 3 fails. Every other gate is unaffected, so the fallback is to sign gates 1, 2 and 4 and re-sign gate 3 in M2 with a stated date |
| M1-T38 timed run | 2027-W04 | 1 week | Gate 2 slips; re-run the following week. Cheap to redo | Gate 2 fails. It is one operator session; do not lose it to scheduling |
| M1-T44 gate check | 2027-W08 | 0 | If the profile fails a gate, there is no more time to fix it | The profile stays in shadow and does not promote. **This is an acceptable M1 outcome** — a profile that fails honestly is a working gate |

### 4.4 What is explicitly *not* on the critical path, and why that is a decision

W7 (console hardening, 2.0 ew) and W9 (docs, 4.0 ew) are both off the longest
chain. They are also the two most likely to be quietly expanded, because they are
the two where "a bit more polish" feels free. W7's only critical item is M1-T48
(incident state is stated, never implied), which is what makes gate 2 believable in
front of an operator. The rest of W7 is quality, and quality is where a slip gets
absorbed.

W9 is off the chain but is **not** optional: an exit gate signed with evidence that
a buyer cannot read is a gate that will not be re-signed at M2, when the question
becomes commercial.

---

## 5. M1 risks

Likelihood and impact are **PROPOSED** judgements, made here and not measured —
nothing in the repository supports a probability. They are ordered by expected
damage, and the early-warning signal is written so that it can be observed, not
felt.

| # | Risk | Likelihood (PROPOSED) | Impact | Early-warning signal | Owner | Pre-agreed response |
|---|---|---|---|---|---|---|
| R1 | **The Milestone API surface is materially different from the assumption.** A different auth model, a REST-vs-MIL-module split, different paging, or event types that do not map onto the rule schema's vocabulary | High | **Critical.** Threat model R4, gates 1 and 4, and the entire M1 thesis | `probe` returns 401/404 on more than 2 of 8 routes, or returns 200 with an unexpected top-level shape | E1 (relationship), E2 (fix) | The adapter is wrong, not the plan. Add a Milestone-specific transport behind the existing `Transport` seam — the parsing path above it does not change. If the *event vocabulary* cannot map, that is a rule-bundle change and goes through `rules lint` and `rules simulate`, not a code patch. If it cannot be made to work in 3 weeks, invoke the `01` §M1 reduced-scope cut and move gate 1 to M2, in writing |
| R2 | **No design partner site is available on the timeline.** Gating on a person outside the company who has to provision credentials and network access | **High** — it is the only item with no internal lever | **Critical.** Gates 1, 2, 3 and 4 all need it | No signed pilot agreement by 2026-W41; no credential by 2026-W42 | E1 | Two fallback sites, in order: (1) a demo site — a real XProtect install we control, which satisfies gates 1 and 3 but not 4; (2) a reseller's lab install. If neither exists by 2026-W44, **move the M1 date.** Do not sign a gate against a fixture. The reduced-scope cut explicitly permits a demo site and it is the honest use of it |
| R3 | **The 14-day shadow clock does not complete.** A restart, a bundle rejection, a GPU fault, or a profile that fails its gates | Medium-High | **Critical.** Gate 4 only | Fewer than 14 consecutive days logged by 2027-W08; `ai_observations.degraded_reason <> 'none'` on any day in the window | E2 | (a) Make the runtime boring before 2027-03-01: readiness-not-crash, bundle verified once at startup, no auto-restart storm. (b) Run the clock on a second, quieter site in parallel so there is a second source of consecutive days. (c) If it does not complete, gate 4 is **not signed** and the milestone closes with gates 1–3 signed and gate 4 stated as open with a date. That is a legitimate outcome; a signed gate 4 without 14 days is not |
| R4 | **The product is unobservable.** `internal/observability` has no caller, so gate 3 cannot be produced and no metric in the program exists | **Certain today** — this is F2, not a prediction | High | `grep -r "internal/observability" --include=*.go` returns one hit | E2 | M1-T33 is scheduled in 2026-W49 with zero float. If it is not started by 2026-W48, it is escalated to a W1-priority item and W3 is deprioritised, because an unmeasured system cannot pass gate 3 no matter what else is true |
| R5 | **The installer's first real install finds something.** An installer written against a dev machine meeting a real Windows Service, a real Postgres service account, a real evidence directory on a real disk | High | Medium-High. Schedule, not correctness | T36 slips twice, or T36 requires a code change to W1–W4 | E2 | The first real install is scheduled as a **test**, not a milestone. Do a dry install at a second real machine in W47. Budget one week of slip; that is in the 1 week of float on T36. If it overruns further, ship to the pilot site from a documented manual install and record the substitution in the release notes — but never sign gate 3 or 4 on a manual install |
| R6 | **Capacity slip from the customer.** The 40% tax grows because a customer needs something, or because a person is ill | Medium | High. Two people, no slack | More than 2 consecutive weeks where either engineer is below 60% on the workstream tasks | E1 | Escalate to the `01` §M1 reduced-scope cut, decided the same week it is observed, not at the end of the milestone. The specific cuts are already named in `01`; this plan adds no new ones. Do not respond with hours |
| R7 | **The green in `docs/status.md` is local, not CI.** `ci.yml` pins Go 1.24 with `GOTOOLCHAIN: local` against a `go 1.26.0` module (F3) | **Certain** | Medium-High. Every verification claim in the repo is currently unverified against CI | Any `go` build on the CI image | E2 | M1-T02, half a day, W40. If it turns out CI has been red for the whole history of the project, say so in `docs/status.md` rather than quietly fixing it — the number of claims that rest on it is a planning input |
| R8 | **The invariant is weakened by a deadline.** A guardrail gets relaxed, a fault-injection test gets skipped, a dead letter is auto-retried into silence, a contract step is bundled with its expand | Low-Medium | **Highest of all.** Invariant 4 and the entire M1 moat | Any review comment containing "temporarily", "for the pilot", or "just this release" | E1 (accountable), both (accountable) | The rule in `AGENTS.md` is the response and it needs no elaboration: a weakening needs an ADR and a test, **in that order**. No exit gate is signed over an unweakened invariant, and the exit-gate checklist in §6 has a line for it that either passes or fails |

### 5.1 Risks deliberately not listed

| Not listed | Why |
|---|---|
| "The team runs out of money" | Not measurable from this repository and not the engineers' decision. It is a founder-level constraint and belongs in the operating model, not the M1 risk table |
| "The model gets worse" | A model change is a new signed bundle, a new evaluation, and a new shadow run. It is an expected, handled event, not a risk. The controls already exist |
| "A customer asks for something we do not build" | That is the anti-segment rule in `00` §3, enforced by the intake filter in `` §5. It is a process control, not an uncertainty |
| "A competitor ships the same thing" | The moat is the site-specific record, not the feature. Out of scope for a 26-week plan |

---

## 6. M1 exit gate — signed checklist

Restated from `` §"M1 — Trust Spine", with the evidence artifact
each line requires. `01` is the source; this table adds only the artifact and the
signature. **All four are signed by E1 and E2.** A line is signed only with its
artifact attached, and an artifact is only accepted if it is reproducible by the
other person without being told how.

### Gate 1 — Live XProtect integration and replay parity

> A real XProtect installation has pushed events through the live adapter path,
> with **integration and replay parity demonstrated** — the same scenarios produce
> the same canonical events whether they arrive live or from a fixture.

| # | Line | Evidence artifact | Source | E1 | E2 |
|---|---|---|---|---|---|
| 1.1 | A real XProtect installation pushed events through the live adapter path | Dated `docs/verification/milestone-probe-<date>.md`: the 8 routes from `internal/milestone/routes.go`, each with HTTP status, returned shape and the probe output. `UnverifiedRoutes()` empty, or each remaining entry with a named reason | M1-T04, T05 | ☐ | ☐ |
| 1.2 | The same scenarios produce the same canonical events live and from a fixture | `docs/verification/parity-<date>.md`: all 12 committed scenarios in `testdata/fixtures/`, per-scenario live-vs-fixture event counts, field-level diffs (expected: none), and the canonical `integrity_hash` comparison | M1-T06, T07, T08 | ☐ | ☐ |
| 1.3 | The mapping is no longer a hypothesis | `routes.go` diff where `Unverified: true` became `false`, one route per commit, each commit message naming the probe output that justified it | M1-T05 | ☐ | ☐ |
| 1.4 | Every route change is covered by a test that fails when the assumption breaks | The test that asserts `len(UnverifiedRoutes()) <= N`, and a negative test for a malformed body per route | M1-T05, M1-T53 | ☐ | ☐ |
| 1.5 | **No invariant was weakened to achieve parity** | `make ci` green; the 35 threat-model controls each still mapped to their failing test | M1-T55, T57 | ☐ | ☐ |

### Gate 2 — One incident class, end to end, on a real site, timed

> One incident class runs end to end on a real site: event → graph → deterministic
> verification → VLM observation → incident → operator disposition → sealed audit
> checkpoint. A real operator completed it, timed.

| # | Line | Evidence artifact | Source | E1 | E2 |
|---|---|---|---|---|---|
| 2.1 | The named incident class, and the site it ran at | Class name, `rule_id`, `rule_bundle_version`, site id, and a note on how the class is corroborated by two independent signals (accepted risk R2) | M1-T37 | ☐ | ☐ |
| 2.2 | The full chain ran, event to sealed checkpoint | One incident id, with the linked rows: `canonical_events` (via `incident_events`), `edges` (via `edges_active`), `verification_results`, `ai_observations`, `dispositions`, and the `audit_checkpoints` row | M1-T38 | ☐ | ☐ |
| 2.3 | A **real operator** completed it, and it was timed | Start and end timestamps in the site's timezone with explicit offset, elapsed wall time, the number of operator actions from `audit_ledger`, and the operator's identity as recorded in `dispositions.actor_id` | M1-T38 | ☐ | ☐ |
| 2.4 | The checkpoint is signed and verifies against an out-of-band public key | The seal file from `audit_checkpoints.sink_ref`, plus `securityctl audit verify-seals` output using a public key obtained outside this machine | M1-T39 | ☐ | ☐ |
| 2.5 | The decision replays from stored facts, with no model call | `GET /v1/incidents/{id}/explanation` output showing `replayedFromStoredFacts: true`, plus the test that asserts a replay issues zero model calls | M1-T40, T53 | ☐ | ☐ |
| 2.6 | The operator could explain, from the product alone, why the system did what it did | The operator's own written answer, unattributed to us, describing why the incident was raised and why that verdict | M1-T40 | ☐ | ☐ |
| 2.7 | The operator's view of the surface was recorded, including whether they preferred it to the VMS workflow | The post-session note from the customer operator. The M1 mission is that a real operator *prefers* this workflow; if the answer is no, that is written down, not edited out | M1-T38, M1-T64 | ☐ | ☐ |

### Gate 3 — Every SLO demonstrably met under a recorded load run

> Every SLO in `internal/observability/slo.go` is demonstrably met under a
> recorded load run, with the run and its numbers in the release notes.

| # | Line | Evidence artifact | Source | E1 | E2 |
|---|---|---|---|---|---|
| 3.1 | The metric registry is actually emitted by the running system | The scrape output from a live process, showing every name in `internal/observability/metrics.go` with a value. Today no production code imports the package; this line is the proof that it now does | M1-T33 | ☐ | ☐ |
| 3.2 | `ingest_availability` — **zero accepted events lost** | The load-run count, the dead-letter count, and the reconciliation: accepted rows in `inbox_events` = processed + dead-lettered, with no third category | M1-T35 | ☐ | ☐ |
| 3.3 | `ingest_latency` — p95 under 5s | p95 from the run, with the reservoir size stated | M1-T35 | ☐ | ☐ |
| 3.4 | `incident_visibility` — p95 under 15s | p95 from the run, measured on the AI path, with the observation count it is computed over | M1-T35 | ☐ | ☐ |
| 3.5 | `ai_plane_availability` — reported, never required | The degraded-decision count and the `ai_observations.degraded_reason` breakdown. **A run with a non-zero count is a pass**; the requirement is that it is reported, not that it is zero | M1-T35 | ☐ | ☐ |
| 3.6 | `ingest_backlog` — soft 5,000, hard 50,000 | Inbox, outbox and job depths at the end of the run, and the maximum observed during it | M1-T35 | ☐ | ☐ |
| 3.7 | `correlation_under_drift` — fewer than 1% of devices in the severe band | Device count and severe count from `device_clocks` at the end of the run, and the exclusions recorded on `verification_results.drift_exclusions` | M1-T35 | ☐ | ☐ |
| 3.8 | `dead_letter_visibility` — visible within 60s | A timed observation: a deliberately induced dead letter, its appearance in the console, and the elapsed time. Measured by the operator's interval, per the SLO's own comment | M1-T51 | ☐ | ☐ |
| 3.9 | `evidence_disk` — 10% free, warning at 25% | Free-space reading at the end of the run and the retention-sweep dry run | M1-T35 | ☐ | ☐ |
| 3.10 | The run itself is recorded, and the numbers are in the release notes | The run record: date, machine, release id, load generator rate, duration, event count, and every SLO reading with its remedy. The declared rate appears in the release notes **before** the run, not after it | M1-T34, T35 | ☐ | ☐ |
| 3.11 | CI is green with no Milestone server reachable | The CI run for the M1 commit, showing `contract-check contract-sync vet lint test-race migration-phases rules-lint` | M1-T02, T57 | ☐ | ☐ |

### Gate 4 — A production VLM profile, shadow mode, 14 consecutive days, real GPU

> A production VLM profile has passed its promotion gates on a real GPU, in shadow
> mode, for at least 14 consecutive days at a design-partner site, with the
> evaluation report attached.

| # | Line | Evidence artifact | Source | E1 | E2 |
|---|---|---|---|---|---|
| 4.1 | The profile ran in **shadow mode** for ≥14 consecutive calendar days | A per-day table: date, observations written, degraded count, and a continuity mark. A day with a gap resets the count and the table shows where | M1-T41, T45 | ☐ | ☐ |
| 4.2 | It was at a **design-partner site**, on a **real GPU** | Site id, and `ai_observations.hardware` from the run, showing the GPU the inference actually happened on | M1-T41 | ☐ | ☐ |
| 4.3 | Shadow mode affected no authoritative state | Evidence: no incident created, nothing suppressed, and no rule decision citing a shadow observation. The `production_profile` flag on the rows, and a test asserting a shadow profile cannot affect authoritative state | M1-T41 | ☐ | ☐ |
| 4.4 | It passed `evaluation.DefaultGates()` | The evaluation report: FPR ≤ 0.20, recall ≥ 0.70 on true positives, median latency ≤ 8000 ms, disagreement ≤ 0.25, ≥30 labelled cases — each stated with its measured value and PASS/FAIL, and `Undispositioned` counted separately and never folded into a rate | M1-T43, T44 | ☐ | ☐ |
| 4.5 | The model bundle was signed and verified | The bundle signature as recorded in `ai_observations.model_bundle_signature`, and a negative test showing a tampered bundle is refused before any inference | M1-T11 | ☐ | ☐ |
| 4.6 | Every observation carries full attribution | One sample row showing `model`, `model_hash`, `runtime_version`, `prompt_template_id`, `prompt_version`, `input_evidence_hashes` and `output_hash` all populated, plus the test that refuses an incomplete one | M1-T12, T16 | ☐ | ☐ |
| 4.7 | Promotion was a human decision | The ledger row: `actor_type='admin'`, action naming the profile and version, with the evaluation report digest in `reason` | M1-T46 | ☐ | ☐ |
| 4.8 | The AI plane's absence is **measurable**, not assumed | `incidents.degraded` counts, `verification_results.ai_degradation` breakdown across `none / model_unavailable / no_runtime / timeout / error / absent`, and the declared `if_ai_unavailable` behaviour of the class's rule | M1-T13, T38 | ☐ | ☐ |

### Gate-level preconditions

These are not the four gates. They are conditions under which a signature on any
gate above is worth anything, and they are restated from `AGENTS.md` and
`docs/threat-model.md` so nobody signs around them.

| # | Precondition | Evidence | E1 | E2 |
|---|---|---|---|---|
| P1 | Invariant 1 held: zero accepted event loss, no duplicate side effect under `kill -9` at any stage | The M1 fault-injection matrix: one run per stage, each showing no loss and no duplicate | ☐ | ☐ |
| P2 | Invariant 2 held: decisions replay from stored facts and the stored `AIObservation`, and nothing re-inferred | The replay test and the explanation output in 2.5 | ☐ | ☐ |
| P3 | Invariant 3 held: a missing AI observation is never a negative observation, and every rule consuming a Tier C signal declares `if_ai_unavailable` | `rules.TestConsumingAnAIObservationRequiresAnExplicitDegradation`, `TestMissingAIObservationDegradesToUnverified`, `TestDegradedObservationIsNotEvidence` green, plus a `securityctl rules explain` dump for the class's rule | ☐ | ☐ |
| P4 | Invariant 4 held: the audit ledger is append-only and hash-chained, and editing any row fails verification | The chain verification output, plus `schemalaws.TestTamperedRowBreaksChainVerification` green | ☐ | ☐ |
| P5 | Invariant 5 held: bundles are untrusted input — no shell, SQL, network, dynamic code or unbounded regex | `securityctl verify-release` on the archive that was installed, and the rule-engine control tests | ☐ | ☐ |
| P6 | Any invariant that was weakened has an ADR **and** a test, in that order | The ADR index, or "none weakened" | ☐ | ☐ |
| P7 | The threat model's 4 accepted risks are still accurate, and R4 is closed | The updated `docs/threat-model.md` §6, with R4 rewritten to reflect the live verification | ☐ | ☐ |
| P8 | `docs/status.md` describes the build on the day it is read, not on the day it was written | A `docs/status.md` diff that closes exactly the items this plan closed, and no others | ☐ | ☐ |

### Reduced scope, restated

From `` §"M1", verbatim in substance. It is here so nobody
invents a different cut under pressure:

> Ship with ONVIF-agnostic synthetic fixtures and a demo site, and defer the
> live-Milestone parity proof to M2. Do not cut the audit, the replay path, or the
> `kill -9` fault-injection tests to make a date.

Two things this plan adds, and they are scheduling rules rather than scope changes:

1. **Gate 4 is not covered by that clause.** A 14-day shadow run at a real design
   partner site requires a real site. If there is no real site, gate 4 is not
   signed and the milestone closes with it explicitly open and dated. The clause
   was written for a code slip, not for a customer-availability slip.
2. **The cut is decided the week it is observed**, by E1 and E2 together, in
   writing, and recorded in this file. A cut decided at the end of the milestone is
   a deadline, not a decision.
