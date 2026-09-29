# internal/ — package map

A local-first physical-security intelligence platform. It ingests events from a
Milestone XProtect VMS, normalizes them into canonical events, builds a security
graph, evaluates a deterministic rule engine, records VLM observations, creates
incidents, and records everything in a hash-chained audit ledger.

This file is for two readers: a new engineer on day one, and an AI agent with no
prior context. It answers *where things are* and *what each package is for* so
neither of you has to open thirty packages to find out.

It is a map, not an architecture document. It does not restate build state —
`docs/status.md` is the only source of truth for that — and it does not restate
the plan, which lives in `docs/roadmap/`.

---

## Read this first

**The pipeline, as one line:**

```
Milestone → inbox → normalize → graph → rules → verify → observations
          → incidents → evidence → audit
```

**The rule that governs the whole design:** the deterministic core — `milestone`,
`inbox`, `normalize`, `graph`, `clock`, `rules`, `incidents` — runs with the AI
plane entirely absent. There is no code path in those packages that could invoke
a model. The AI plane (`observations`, `evaluation`,
`deploy/ai-runtime/runtime.py`) is an optional second opinion whose *absence* is
a recorded, first-class state rather than a negative answer.

The five invariants in `AGENTS.md` are the law. Nothing in this file overrides
them, and a change that weakens one needs an ADR and a failing test, in that
order.

---

## The package table

Roles: **ingest** (source-facing) · **normalize** (source shape → canonical
shape) · **store** (durable state) · **evaluate** (decide) · **observe** (the AI
plane) · **present** (operator-facing) · **support** (substrate, tooling or a
test suite with no runtime role).

"Audit ledger" means: does the package hold an `*audit.Writer` and append rows?
Only five do. Everything else reaches the ledger through its caller.

| Package | Responsibility (one line) | Role | Writes audit ledger | Key exported type / entry point |
|---|---|---|---|---|
| 
| `airuntime` | The Go client for `runtime.py` and the `QueueVLM` worker. Imports nothing from the deterministic core | observe | no (the caller audits `ai.observation.*`) | `airuntime.Client`, `Client.Do`, `airuntime.Result`, `VLMWorker` |
| `api` | Strict-JSON API and the server-rendered console, with CSRF, CSP and per-route capabilities | present | yes (denials and refusals) | `api.Server`, `Server.RegisterRoutes(Deps)`, `api.Deps` |
| `audit` | Append-only, hash-chained ledger; owns the `Entry` shape, validation, verification and sealing | store | yes — it *is* the writer | `audit.Writer`, `Writer.Append`, `Writer.Verify`, `audit.Entry` |
| `bundles` | Stores, activates and rolls back immutable rule bundles; activation is a pointer move | evaluate | yes (`rules.bundle.*`) | `bundles.Service`, `Service.Store`, `bundles.Meta` |
| `checkpoint` | Signs the ledger to a sink outside the database, so tamper is provable after the fact | store | no (the caller audits `audit.checkpoint.sealed`) | `checkpoint.Seal`, `checkpoint.Sealer`, `Sink`, `Signer`, `VerifySeal` |
| `clock` | The time subsystem: sources, drift bands, per-site policy, normalized time, skew, lag. Pure, no DB | support | no | `clock.Policy`, `clock.Band`, `clock.Normalized`, `clock.Lag` |
| `command` | A ~270-line subcommand tree for the CLIs. Deliberately not a dependency | present | no | `command.Command`, `Execute`, `command.ExitCode` |
| `config` | Reads and validates the daemon's environment once. No field is a secret, and nothing whose absence would change behaviour gets a silent default | support | no | `config.Config`, `config.Load`, `config.LoadFrom` |
| `contracts` | Generated protobuf (`securityv1`) plus a compatibility test. **No hand-written source** | support | no | `securityv1.Event`, `securityv1.Incident`, `securityv1.Observation` |
| `correlator` | Assembles a rule's signals from stored canonical events, device clocks and stored observations. Imports rules but not the rule engine's inputs, so evaluation cannot acquire a database handle | evaluate | no | `correlator.Correlator`, `SignalsFor`, `correlator.Result` |
| `dbx` | The Postgres pool, the tenant-context discipline, and the password-free DSN policy | support | no | `dbx.Pool`, `Pool.InTenant`, `Pool.InSystemTenant`, `dbx.Config` |
| `dr` | The whole-recovery drill as a test: database gone, checkpoint material gone, decision still replayable | support | test only | `dr` (DoD item 9) |
| `durability` | The durability test suite: dedupe across partitions, lease reclaim, normalize atomicity, outbox poison isolation | support | test only | `durability` (threat-model controls C23–C28) |
| `evaluation` | Scores a VLM profile against labelled cases and decides promotion, with the gates as code | observe | no | `evaluation.Evaluate`, `evaluation.Gates`, `Runner`, `Case` |
| `evidence` | Content-addressed filesystem store. The only code that touches the evidence root | store | no (the API audits `evidence.fetched`) | `evidence.Store`, `New`, `ValidateRef`, `Store.Put`, `Store.Open` |
| `fixture` | The on-disk format for captured/replayed envelopes, plus the sanitizer that is the single network-to-repo choke point | ingest (test double) | no | `fixture.Scenario`, `SanitizeResponse`, `fixture.Store` |
| `graph` | The M1 Security Graph: topology and provenance edges, bounded traversal, snapshot. No person model | store | no | `graph.Service`, `graph.Edge`, `graph.Provenance`, `Service.Walk` |
| `identity` | Authentication, sessions, MFA (TOTP + WebAuthn), roles, capabilities, step-up | present | yes (every auth event) | `identity.Service`, `identity.Principal`, `identity.Capability` |
| `inbox` | Intake: scoped dedupe key taken before the row, lease with expiry, terminal states, dead letters | store | no | `inbox.Writer`, `WriteOptions`, `inbox.Lease`, `inbox.Key` |
| `incidents` | The case lifecycle, dispositions, and the decision-replay path | present | yes (`incident.created`, `incident.disposition.recorded`) | `incidents.Service`, `incidents.Create`, `incidents.Explanation`, `ReplayInputs` |
| `jobs` | The Postgres work queue that replaces Kafka and Redis: `SKIP LOCKED` claims, per-queue idempotency keys, leases, dead letters | support | no | `jobs.Enqueuer`, `jobs.Queue` (`QueueVLM`, `QueueRuleBulk`, …), `jobs.Job`, `jobs.Worker` |
| `lifecycle` | install / upgrade / rollback / backup / restore, plus archive extraction that re-validates every path, plus the systemd unit writer | support | no | `lifecycle.State`, `lifecycle.Extract`, `lifecycle.Refuse`, `lifecycle.WriteUnits` |
| `migrate` | The forward-only migration runner: ordered SQL, checksums, phase window, advisory lock | store | no | `migrate.Runner`, `Runner.Up`, `Runner.Verify`, `Runner.CurrentVersion` |
| `milestone` | The XProtect protocol adapter. The *only* place vendor concepts may exist | ingest | no | `milestone.Adapter`, `milestone.Transport`, `RawRecord`, `Routes`, `ReplayTransport` |
| `normalize` | Inbox row → canonical event, committing with the inbox transition in one transaction | normalize | no | `normalize.Normalizer`, `Input`, `Result`, `DeriveEventID`, `ToProto` |
| `observability` | The metric registry (closed name set) and SLO evaluation | support | no | `observability.Registry`, `observability.Breaches`, `observability.SLO` |
| `observations` | Persists and validates AI observations: mandatory attribution, append-only, supersession, profiles | observe | no (the caller audits `ai.observation.*`) | `observations.Writer`, `Observation`, `M1Profiles`, `Degradation` |
| `outbox` | Transactional publish without a broker: the state change and its outbound effect commit together | store | no | `outbox.Enqueue`, `outbox.Message`, `outbox.Relay` |
| `pipeline` | The loops: ingest, consume, outbox and gauge sampling, plus the shutdown order. Nothing else owns a lifecycle | present | no | `pipeline.Start`, `pipeline.New`, `Loops.Stop`, `pipeline.Deps` |
| clocksub | Measures and stores per-device clock drift against the site reference | store | no | `clocksub.Service`, `Service.Measure`, `Service.State` |
| dr | The whole-recovery drill as a test: database gone, checkpoint material gone, decision still replayable | support | test only | `dr` (DoD item 9) |
elease | Builds and verifies signed offline release archives. Verification is deliberately stricter than construction | support | no | `release.Build`, `release.Verify`, `release.Manifest`, `ScanForSecrets` |
| `rules` | The rule DSL: bundle, JSON Schema, semantic validation, compiled AST, deterministic engine. Imports nothing internal | evaluate | no | `rules.Compiler`, `Compile`, `CompiledBundle`, `Engine`, `Decision`, `Tier`, `DegradeTo` |
| `schemalaws` | The schema-level invariant suite: RLS isolation, append-only, provenance, `storage_ref` CHECKs, no person model | support | test only | `schemalaws` (threat-model controls C1–C6, C11–C12, C18–C19, C32–C33, C35) |
| `simulate` | Runs a bundle over the fixture set and diffs the outcome; the diff *digest* is the activation gate | evaluate | no | `simulate.Runner.Run`, `simulate.Diff`, `simulate.DigestFor` |
| `testutil` | The shared database-backed test harness. Skips rather than fails when no database is configured | support | no | `testutil.Pool`, `testutil.Reset`, `testutil.NewTenant` |

### Binaries and tools

| Path | What it is |
|---|---|
| `cmd/securityctl` | The operator and installer CLI. One binary, two audiences (a developer running `rules lint`, an operator running `upgrade` at a site with no network). Registers `preflight`, `doctor`, `install`, `upgrade`, `rollback`, `backup`, `restore`, `verify-release`, `build-release`, `migrate`, `rules`, `incident`, `audit`, `identity`, `release-key`. |
| `cmd/security-migrate` | A separate binary on purpose: the migrator owns the schema, so the long-running core cannot reshape its own tables underneath itself. |
| `tools/contractcheck` | Turns "a breaking protobuf change is a build failure" into a build failure. `-mode check` gates, `-mode write` re-baselines. |
| `tools/fixturegen` | Authors the synthetic fixture set. The scenarios say `Source: synthetic` and a test asserts a scenario claiming to be a capture names a real one. |
| `deploy/ai-runtime/runtime.py` | The AI plane, as a separate dependency-free Python process. No model in it. |
| `deploy/systemd`, `deploy/windows` | Service units. |

---

## Dependency rules

Read the current import graph before you trust this section; it was derived from
every import statement in `internal/`, `cmd/` and `tools/`.

### The layering that holds today

```
cmd/securityctl, cmd/security-migrate, tools/*
        │
        ▼
     api ─────────────── identity ──┐
        │                            │
        ├── incidents ── rules       │
        ├── bundles  ── rules        │
        ├── inbox, graph,            │
        │   clocksub, observations   │
        │        │                   │
        │        ▼                   ▼
        │      dbx ────────────── audit
        │        │
        │        ├── inbox ── normalize ── contracts/securityv1 (generated)
        │        ├── graph, clocksub, jobs, outbox, observations
        │        └── migrate ── migrations/ (embedded SQL)
        │
        ├── milestone ── fixture
        ├── lifecycle ── release
        ├── simulate ── rules
        ├── evaluate ── (nothing internal)
        └── clock, evidence, checkpoint, command, testutil ── (nothing internal)
```

### Rules, and where each one is grounded

1. **Nothing in the deterministic core may import the AI plane.** The core is
   `milestone`, `inbox`, `normalize`, `graph`, `clock`, `rules`, `incidents`.
   None of them imports `observations` or `evaluation`, and none of them can
   reach `deploy/ai-runtime/`. `incidents` reads AI evidence only as a *stored
   value* — `rules.Signal.AIObservation`, a validated struct — which is what
   makes `internal/incidents/replay.go` (`ReplayInputs` has no model client, no
   bundle path and no clock field) able to satisfy invariant 2 structurally
   rather than by discipline.

2. **`internal/rules` may not import `dbx`, and today does not.** The engine
   receives `[]Signal` and a `Context` and returns a `Decision`. A rule engine
   that could query the database would produce decisions that depend on data the
   explanation does not carry, and replay would stop being reproducible. The
   engine's only non-stdlib dependency is nothing at all.

3. **`internal/contracts/` is generated and is never hand-edited.** The
   generated code is `internal/contracts/securityv1/*.pb.go`; the directory
   `internal/contracts` itself contains only `contractcompat_test.go`. Change
   `contracts/protobuf/security/v1/*.proto`, run `make generate`, and let
   `contractcheck` decide. Only `normalize` imports `securityv1` today (for
   `ToProto`); keep it that way, because a second hand-written Go struct for the
   same wire type is how a contract quietly stops matching the thing it
   describes.

4. **`internal/milestone` is the only place vendor concepts may exist.** The
   vendor-to-canonical event-type map (`normalizeEventType`,
   `deriveEventType`), the vendor spellings in `KnownMilestoneEventTypes`, the
   assumed timestamp layouts (`milestoneTimeLayouts`), the device-reference
   regex, and the whole assumed API surface (`Routes`) all live there and
   nowhere else. A rule bundle names normalized types because
   `rules.KnownEventTypes` is the stable cross-site vocabulary; if the mapping
   leaks inward, bundles stop being portable between sites and between replay
   and integration mode.

5. **`internal/milestone` does not touch the database.** It imports `dbx`
   nowhere; its output type is `RawRecord` and its job is "read, sanitize, hand
   over". That separation is what makes `kill -9` mid-pipeline testable.

6. **`internal/dbx` imports nothing internal.** It is the only place allowed to
   set `app.tenant_id` on a transaction. Every read of tenant-scoped data goes
   through a `Tx` because a session-level `SET` would leak the previous
   request's tenant into a pooled connection.

7. **`internal/testutil` is imported from `_test.go` files only.** It is a test
   harness, and a production package importing it would make the test database
   reachable from the shipped binary.

8. **Store packages depend on `dbx` and on nothing else in the domain.**
   `inbox`, `outbox`, `jobs`, `observations`, `graph` and `clocksub` import
   `dbx` and (in tests) `migrate`/`testutil`. They do not import each other.
   If two of them ever need to talk, the coordination belongs in `normalize` or
   in a caller, not in a new edge between two store packages.

9. **Presentation may reach the domain; the domain may never reach
   presentation.** `api` imports `incidents`, `bundles`, `evidence`,
   `identity`, `rules`, `inbox`, `jobs`, `clocksub`, `clock`, `migrate`, `dbx`
   and `audit`. None of them import `api`.

10. **`internal/identity` may not import `internal/api`.** It does not, and
    `api` depends on it. Authorization decisions are made in `identity`
    (`CapabilitiesFor`, `Capability`) and merely enforced by routes.

### Forbidden directions, stated plainly

- core → AI plane: forbidden (rule 1).
- any package → `deploy/ai-runtime`: forbidden; the AI plane is a separate
  process reached over HTTP, not a library call, precisely so a hung inference
  cannot hang ingest.
- `internal/contracts` hand-edit: forbidden.
- `internal/milestone` → `dbx`, or vendor vocabulary outside `internal/milestone`:
  forbidden.
- `internal/rules` → `dbx`, `net/http`, `os/exec`, `database/sql`, or
  `regexp`-backed user patterns: forbidden. The strongest operator in the rule
  language is a fixed prefix match (`rules.FieldOperators`).
- SQL in Go: forbidden. Schema lives in `migrations/`. The few inline statements
  in store packages are row operations against that schema, not DDL.
- editing an applied migration: forbidden. Add a higher-numbered one.

---

## The three seams worth understanding first

Each seam is where a class of bug that no unit test would catch actually lives.
Read them in the order given.

### (a) The vendor adapter seam — `internal/milestone`

The one thing to internalise: **every route in this package is an assumption.**
`Unverified: true` is a field, not a comment, so it can be asserted on and
printed.

1. `internal/milestone/routes.go` — the complete assumed Milestone API surface:
   eight routes, every one `Unverified`, with a `Notes` field saying what is
   being assumed. Also `Transport`, `Query`, `Response` and the three typed
   errors.
2. `internal/milestone/adapter.go` — `Poll` (deterministic order by source event
   id, then observed time, never arrival order), `Parse` (the single parsing
   path, shared by every transport), `toRecord`, `deriveEventType` (an alarm is
   a state machine, not a type), `normalizeEventType` (the vendor-to-canonical
   map), `parseMilestoneTime` (a naive timestamp gets an *empty* source timezone,
   not an assumed UTC).
3. `internal/fixture/fixture.go` and `internal/fixture/sanitize.go` — what a
   capture stores, and the allow-list choke point between the network and the
   repository.
4. `internal/milestone/replay.go` — the same `Transport` interface with bytes
   from a directory. Not a stub; a second byte source for the same code.
5. `internal/milestone/adapter_test.go` — twelve tests that pin the assumptions.
6. `tools/fixturegen/main.go` — the synthetic scenarios, and why they are
   authored rather than captured.

### (b) The durability seam — `inbox`, `outbox`, `jobs`, `migrate`, `dbx`

The one thing to internalise: **acceptance is a database commit, not a side
effect.** Everything downstream is reachable from the committed row.

1. `internal/dbx/dbx.go` — `Pool.InTenant` and `Pool.InSystemTenant`. The only
   place `app.tenant_id` is set, and the reason a background worker reaching
   across tenants is bounded by grants rather than by a role exemption.
2. `internal/migrate/migrate.go` — forward-only, checksums, the
   expand/backfill/contract phase rule, and the refusal to touch a database
   outside the build's declared window.
3. `internal/inbox/inbox.go` — the dedupe key is claimed *before* the row, in the
   same transaction, so there is no window with a row and no key or a key and no
   row. `ClaimFor`/`ClaimAll` reclaim an expired lease; `Complete`/`Fail` are
   the only terminal transitions; `DeadLetters`/`Replay` are the operator
   surface.
4. `internal/normalize/normalize.go` — the canonical event, its dedupe key, and
   the inbox transition all in one transaction. `DeriveEventID` is derived from
   the scoped key, not generated, so a retry after a crash lands on the same
   identity. `completeInbox` matches on `lease_owner` as well as status, so a
   reclaimed row cannot be completed by the worker that lost it.
5. `internal/outbox/outbox.go` — `Enqueue` takes the caller's `pgx.Tx`; it
   never opens its own. Idempotency key is scoped to the tenant.
6. `internal/jobs/jobs.go` — four queues, each with its own workers, lease
   duration and retry bound; `ErrNotClaimed` is distinct so the correct response
   is "stop", not "retry".
7. `internal/durability/durability_test.go` — the kill -9 and lease-race tests.
   Read this before changing anything in (1)–(6).
8. `internal/schemalaws/schemalaws_test.go` — the same laws asserted at the
   schema layer, where an RLS policy or a `CHECK` is the real control.

### (c) The AI observation seam — `observations`, `evaluation`, `runtime.py`

The one thing to internalise: **the three absences are kept apart, and none of
them is a negative answer.**

```
no observation         the AI plane was never asked
degraded observation   it was asked and could not answer
insufficient evidence  the model answered "I cannot tell"
```

1. `internal/observations/observations.go` — the `Observation` record and why
   every attribution field is mandatory; append-only supersession; `M1Profiles`
   (one production, two shadow) and `IsProductionProfile`; the closed
   `Degradations` set that matches the schema's `CHECK`.
2. `internal/rules/bundle.go` — `Corroboration.IfAIAvailable` is a pointer with
   no default, and the comment says why: "what happens when the model is down"
   is a decision an operator makes once and reads many times.
3. `internal/rules/engine.go` — `Signal.AIObservation`, and `stateFor`, which
   checks degradation *before* counting corroborating signals. Counting
   non-AI signals and reporting CORROBORATED would tell an operator the
   evidence was sufficient when the rule itself said it was not.
4. `internal/rules/match.go` — `matchAIObservation` and `degradeReasonOf`: the
   only two places a Tier C signal can influence a decision.
5. `internal/evaluation/evaluation.go` — the promotion gates as code, not as
   prose. `DefaultGates`, `ShadowGates`, `Evaluate`.
6. `deploy/ai-runtime/runtime.py` — the process boundary. `ALLOWED_DEGRADATIONS`
   is written out in Go, in two protos and here; the file says the three must
   agree. There is no model, and its absence produces `model_unavailable`
   rather than a fabricated negative.
7. `internal/incidents/replay.go` — where the *stored* observation replaces the
   model call, and `ReplayedFromStoredFacts` is a constant `true` rather than a
   computed field.

---

## Where to add X

Every row follows the same order: **contract first, then owner, then test.**
`AGENTS.md`: "Contracts are the source of truth. Change `contracts/` first,
regenerate, and let `contractcheck` tell you whether you broke the wire."

| Change | Package that owns it | Contract that must change first | Test that must exist |
|---|---|---|---|
| A new canonical event type | `internal/milestone` (`normalizeEventType`) *and* `internal/rules` (`KnownEventTypes`) — both, or the rule silently never matches | `contracts/json-schema/rule-bundle.schema.json` (the `eventType` vocabulary) plus its embedded copy; `contracts/protobuf/security/v1/event.proto` if a field is added | `internal/milestone/adapter_test.go` (the mapping) and `internal/rules/compile_test.go` (the vocabulary). Run `make contract-sync` after the schema edit |
| A new VMS adapter | A new package mirroring `internal/milestone`: implement `Transport`, produce `RawRecord`, own its route table with `Unverified` flags | `contracts/json-schema/` only if it adds a wire type; the adapter itself is internal | Adapter package tests plus the non-adapter diff (nothing outside the adapter package changes). See `docs/roadmap/engineering/integrations-and-adapters.md` |
| A new rule construct | `internal/rules` in five places: the Go type in `bundle.go`, the JSON Schema, the compiler check in `compile.go`, the semantic rule in `semantic.go`, the evaluator in `match.go`/`engine.go` | `contracts/json-schema/rule-bundle.schema.json`, then `cp` to `internal/rules/schema/` (`make contract-sync` fails otherwise) | `internal/rules/compile_test.go` and `engine_test.go`. A new operator must have a closed-set entry in `FieldOperators`/`DispatchTypes`/`Severities` and a test that an unbounded pattern cannot be expressed |
| A new VLM profile | `internal/observations` (`M1Profiles`, `Fields`) and `internal/evaluation` (its gates) | `contracts/json-schema/ai-observations/<profile>.schema.json`; `observation.proto` only if a new top-level field is added | `internal/observations/observations_test.go` (shadow vs production, attribution mandatory) and `internal/evaluation/evaluation_test.go`. Start it `Shadow: true` |
| A new safe action (dispatch) | `internal/rules` for the vocabulary (`DispatchTypes`); `internal/outbox` or `internal/jobs` for the effect; a `securityctl` command for the operator-facing half | `contracts/protobuf/security/v1/action.proto` | A red test that the action cannot reach a shell, SQL, the network or dynamic code, and a test that it is idempotent on a retry. An action is untrusted-input-driven code: `AGENTS.md` puts it under the threat model, not the call site |
| A new audit field | `internal/audit` (`Entry`, `Validate`) **and** a new migration, because the chain hash is trigger-computed over a length-prefixed encoding that must cover the new column | The migration. Never edit `0007_audit.expand.sql`; add `NNNN_*.<phase>.sql` | `internal/audit/audit_test.go` and `internal/schemalaws/schemalaws_test.go` (a chain still verifies; an edited row still breaks it) |
| A new API endpoint | `internal/api/handlers.go` (`RegisterRoutes`) and, if it needs a new read model, `readmodels.go` | `contracts/openapi/v1/openapi.yaml` | `internal/api/api_test.go`. The route must declare a capability via `requireCapability`, take a CSRF token if it changes state, and never take `tenant_id` from a body |
| A new migration | `migrations/NNNN_name.<phase>.sql`, applied by `internal/migrate` | n/a — the migration *is* the change | `make migration-phases` (`migrations.TestAllLoadsAndValidates`) and `internal/migrate/migrate_test.go`. `TestVerifyChecksumsCatchesDrift` must still pass, and expand and contract ship in separate releases |

---

## Generated code

`internal/contracts/securityv1/*.pb.go` is generated. **Hand-editing it is the
one thing that silently breaks the wire**: `protoc` will overwrite your change
on the next `make generate`, and until then the Go type and the `.proto` file
disagree with no compile error anywhere.

The exact command:

```sh
make generate
```

which runs:

```sh
protoc -I contracts/protobuf \
  --go_out=. --go_opt=module=github.com/Sergey-Bar/Ai-Captain \
  contracts/protobuf/security/v1/*.proto
go run ./tools/contractcheck -mode check \
  -baseline contracts/compat/baseline.json -root contracts/protobuf
```

`make contract-check` runs the compatibility half on its own. Use
`make contract-baseline` **only** when a new contract version is intended.

There is a second, non-obvious copy: `internal/rules/schema/rule-bundle.schema.json`
is the published `contracts/json-schema/rule-bundle.schema.json` embedded for
the compiler. `make contract-sync` fails if they diverge, because a bundle
validated against a contract nobody published is worse than no validation.

---

## Reading order for a new engineer

Fifteen files, in the order that makes each one easier to read than it would be
out of order.

| # | File | Why this one, and why here |
|---|---|---|
| 1 | `AGENTS.md` | The five invariants and the conventions. Nothing below may contradict it |
| 2 | `docs/status.md` | The only source of truth for what is built and what is not |
| 3 | `internal/README.md` | This file: the map you are now using |
| 4 | `docs/threat-model.md` | Every control names the test that proves it. Read the control table, not the prose |
| 5 | `internal/milestone/routes.go` | The vendor seam and the eight `Unverified` assumptions. Start here, because everything downstream is downstream of a hypothesis |
| 6 | `internal/milestone/adapter.go` | How a vendor envelope becomes a `RawRecord`, and where the vocabulary mapping lives |
| 7 | `internal/dbx/dbx.go` | The tenant-context discipline. Everything tenant-scoped depends on reading this first |
| 8 | `internal/inbox/inbox.go` | Invariant 1: dedupe key before row, lease with expiry, terminal states |
| 9 | `internal/normalize/normalize.go` | The one-transaction step, drift correction, and a derived event id |
| 10 | `internal/clock/clock.go` | Why `observed_at` / `received_at` / `normalized_at` are three columns and not one |
| 11 | `internal/graph/graph.go` | Topology and provenance, and the deliberate absence of a person model |
| 12 | `internal/rules/bundle.go` | The rule DSL, evidence tiers, and `IfAIAvailable` having no default |
| 13 | `internal/rules/engine.go` | Deterministic evaluation, `Decision`, and degradation checked before corroboration |
| 14 | `internal/observations/observations.go` | The AI record and the three absences. The AI plane's whole contract in one file |
| 15 | `internal/incidents/replay.go` | Invariant 2, expressed as a struct that cannot be given a model |

Then run the four verification commands from `AGENTS.md`:
`make contract-check`, `make build`, `make test-race`, `make lint`.

---

## Traps

Every item here was verified in the working tree. If one stops being true, fix
this section rather than trusting it.

1. **`-p 1` is not optional.** `Makefile:43-51` and
   `internal/schemalaws/schemalaws_test.go:20-23`: the database-backed suites
   share one database and rebuild the schema, so two package test binaries
   running concurrently will drop the schema out from under each other. The
   resulting failures look exactly like product bugs. Use `make test` /
   `make test-race`, not bare `go test ./...`.

2. **A green run may have covered less than it looks.** Without
   `SP_TEST_DATABASE_URL`, `testutil.Pool` *skips* rather than fails
   (`internal/testutil/testutil.go:43-46`). Eight packages have no `_test.go` of
   their own — `clocksub`, `dbx`, `inbox`, `jobs`, `normalize`, `outbox`,
   `simulate`, `testutil` — and are covered only indirectly, through
   `internal/durability`, `internal/api`, `internal/incidents` and
   `internal/evaluation`.

3. **`RawRecord.Malformed` currently has no consumer.** A grep for `.Malformed`
   outside `internal/milestone/adapter.go` and its own test returns nothing.
   A malformed envelope is therefore carried, flagged, and stored — and nothing
   downstream branches on the flag. If you find yourself reading `Malformed` as
   if it already suppresses an incident, it does not.

4. **`KnownMilestoneEventTypes` is keyed on spellings `normalizeEventType` never
   emits.** Nine canonical names the function produces — `motion`, `analytics`,
   `recording.started`, `recording.stopped`, `bookmark`, `metadata`,
   `access.granted`, `access.denied`, `gate` — are absent from the map, and
   twelve map keys are unreachable. So most real events are marked
   `Malformed`, and by trap 3 nothing happens because of it. This is the
   adapter defect the root `README.md` already names. The repo's own
   `README.md:82-84` is the citation.

5. **`internal/policy` does not exist.** There is a `policy.proto` and a
   `0005_ai_and_policy.expand.sql` migration, and there is no Go package. M3's
   recommendation engine, and everything downstream of it, has no owner in the
   tree. Do not go looking for it, and do not assume a `policy` table means a
   policy engine.

6. **`securityctl adapter probe` does not exist.** `internal/milestone/routes.go:16`
   names it as the first thing to run on a real installation, and it is
   referenced repeatedly across `docs/roadmap/`. The command tree in
   `cmd/securityctl/main.go:53-69` registers no `adapter` command. The eight
   `Unverified` route flags therefore have no way to be cleared.

7. **`TestSymlinkEscapeIsRefused` skips on Windows**
   (`internal/evidence/evidence_test.go:74-77`), because symlink creation needs
   a privilege the test does not assume. `AGENTS.md` requires an explicit
   negative test on every evidence path; on a Windows dev machine one of the
   four traversal negatives is not running.

8. **The HTTP access log now carries the fields the conventions require.**
   `internal/api/server.go` logs `tenant_id`, `site_id` and `request_id` on every
   line, with the request id assigned once at the outermost middleware so the log
   line, the panic path and the audit row all carry the same one. The audit ledger
   is separate and authoritative; the access log is for operators, and an
   operator reading it now has the correlation id the audit row was written with.

9. **`internal/observability` has a production caller**, and `slo.go`'s
   `Breaches` still does not. The pipeline's gauge loop samples every gauge it
   can observe on its own timer — deliberately not at the end of a pass, because
   a gauge that only updates when work arrives reports zero on a healthy idle site
   by never having been written — and `/v1/metrics` renders the registry under
   `audit.read`. The SLO thresholds are still evaluated by nothing in-process, so
   gate 3 is closer but not closed.

10. **`internal/contracts` has no hand-written Go file.** If you are looking for
    the `Event` type, it is `internal/contracts/securityv1/event.pb.go`, which
    is generated. The only file directly in `internal/contracts` is
    `contractcompat_test.go`.

11. **Replay parity today is byte-parity, not timing-parity.**
    `internal/milestone/replay.go:104` computes the scenario epoch and discards
    it (`_ = epoch`), so `ReplayTransport.WithEpoch` does not shift the
    timestamps a fixture replays with. A scenario replays with the same
    relative timing it was authored with, which is a weaker property than
    Definition of Done item 1 requires.

12. **`.gitignore` covers `/coverage` and `__pycache__/`.** `go test
    -coverprofile=coverage` writes a plain file at the repo root that is easy to
    stage by accident, and `deploy/ai-runtime/__pycache__/*.pyc` is present on
    disk right now. Both are build output. Note also that `.gitignore`
    references `docs/fixtures.md`, which does not exist.

13. **`internal/graph`'s package doc points at a test that lives elsewhere.** The
    doc says a test asserts the absence of `entity_aliases`; that test is
    `schemalaws.TestNoEntityAliasTableInM1`
    (`internal/schemalaws/schemalaws_test.go:675`), not anything in
    `graph_test.go`. The M1 no-person-model decision is enforced. Do not add
    identity resolution without reading that test.

14. **`docs/status.md` is the source of truth and it is stale in two places.**
    It says the suite is green "across 24 packages" (the tree has 30 leaf
    packages under `internal/` plus `internal/contracts/securityv1`) and that the
    toolchain is "Go 1.27" (`go.mod` says `1.26.0`; `.github/workflows/ci.yml`
    pins `GO_VERSION: "1.26"`). Everything else in it — the 20 contract
    messages and 22 enums, which I counted in `contracts/compat/baseline.json` —
    matches the tree. Two stale numbers in an authoritative document is a defect
    worth reporting, not a reason to stop trusting the document.

15. **`AGENTS.md` and `docs/status.md` disagree about `sites.default_locale`.**
    `AGENTS.md` says it "stays in the schema … a site that later needs another
    locale should not require a migration to express it".
    `docs/status.md` deviation 9 says it "is removed by migration 0010", and
    `migrations/0010_drop_default_locale.expand.sql` does exactly that. One of
    the two is wrong. `AGENTS.md` is the working agreement, so the migration is
    the thing to revisit — but this is a schema decision for a human, not a
    documentation fix.
