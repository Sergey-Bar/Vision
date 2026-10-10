# Proposal 01 — `securityctl adapter probe`

**Status:** proposal. Nothing is implemented. A human decides.

## The gap

`internal/milestone/routes.go` declares eight routes. Every one carries
`Unverified: true`. That field is accurate: no Milestone server has ever been
reachable from this codebase. It is also the single most important open item in
`docs/status.md`.

The file repeatedly refers to a command that does not exist:

- `routes.go:16` — "the first thing to do on a real installation is to run
  `securityctl adapter probe`, which lists what the server actually returns next
  to what this adapter expects"
- `routes.go:42` — "`securityctl adapter probe` can print exactly which parts of
  the adapter are still a hypothesis"
- `routes.go:53`, `routes.go:119`, `adapter.go:366`

`UnverifiedRoutes()` (`routes.go:121`) is exported and has a caller-free comment
saying it exists for that command. The command tree in
`cmd/securityctl/main.go` registers fifteen commands — `preflight`, `doctor`,
`install`, `upgrade`, `rollback`, `backup`, `restore`, `verify-release`,
`build-release`, `migrate`, `rules`, `incident`, `audit`, `identity`,
`release-key` — and no `adapter`.

So the code contains a designed-for capability, a field carrying its state, an
exported accessor, and documentation in five places, and no way to exercise any
of it. The flag cannot be cleared, and it cannot be asserted to shrink.

## Why this is first

`AGENTS.md` treats the adapter as a hypothesis with tests, which is honest but
not sufficient. A test pins the *assumption*; only a live server settles it.
Everything downstream — the event-type map, the timestamp layouts, the paged
envelope shape, the clock-drift read — is currently unverified, and the
`KnownMilestoneEventTypes` table is known to be keyed on spellings
`normalizeEventType` never emits. Definition of Done item 1 ("integration and
replay parity") cannot be signed until this runs.

## The reference implementation we would use

`milestonesys/mipsdk-samples-protocol` — **MIT**, published by the vendor.

It is the authoritative public statement of the API surface this adapter
guesses at, and it covers all three transport families we would need:

| Sample | Relevance |
|---|---|
| `EventsAndStateWebSocketAPIPython` | The event feed as a push stream, via the API Gateway. Our adapter polls with a cursor; this is the alternative shape. |
| `RESTfulAlarmsAPI` | Confirms or refutes our `/api/v1/Alarms` assumption, including the "alarm is a state machine, not a type" reasoning at `routes.go:80`. |
| `RESTfulEventsAPI` | Field names for `Id, Timestamp, Type, Source, Priority, TimeSpan, Message, ImagePath, VideoRecordingStartTime` — the exact list we assumed at `routes.go:67`. |
| `TCP Video Viewer` | The ImageServer protocol, which we do not currently call at all. |
| `OAuth Login Flow` | Authentication, which the route table does not model. |

**Requirements this implies:** the RESTful APIs are served by the **XProtect API
Gateway**, and per Milestone's own documentation require XProtect VMS **2021 R1
or later** to install. This is a deployment prerequisite, not a code detail, and
it is unverified against whichever version a pilot site actually runs.

## What we would build

A command that, given a reachable base URL and credentials:

1. For each route in `Routes`, issues the request and records status, content
   type, and whether the body satisfies `Expect`.
2. Prints, per route, the field names observed versus the field names assumed in
   `Notes`, as a diff.
3. Emits a machine-readable report so a test can assert the `Unverified` count
   only ever decreases.
4. Never writes to the database and never mutates the flag. **Probing is
   read-only**; a human clears `Unverified` after reading the report.

## What we would not build

- Not a fixture generator. `tools/fixturegen` authors synthetic scenarios
  deliberately; a probe must not quietly turn a hypothesis into a golden file.
- Not a live-polling daemon. The `Transport` seam already gives replay and live
  one parsing path; a probe is a third read path and nothing more.
- Not a rewrite of `milestone.Routes`. The table stays the single declaration.

## Risks

- **Credential handling.** The probe authenticates. It must reuse the existing
  password-free DSN discipline in `internal/dbx` and must never log a token.
  This is threat-model territory, not a call-site decision.
- **PII in the report.** Event bodies carry camera identity and possibly image
  paths. The report must go through the same sanitizer choke point as
  `internal/fixture/sanitize.go`, which is the documented boundary between the
  network and the repository.
- **Version drift.** A probe result is only true for the version it ran against.
  The report must record the server version, or it becomes a stale claim of the
  kind `README.md` already warns about.

## Confidence

High. The gap is verified by reading the code; the reference repo is the
vendor's own. Unverified: whether the specific XProtect version in a pilot
exposes the API Gateway, and whether the assumed field names are current.

## Decision required

Does `adapter probe` belong in `securityctl` (operator surface, ships in the
release archive) or in a separate operator tool that does not ship? This is a
packaging question with a security consequence, and it is not ours to make.
