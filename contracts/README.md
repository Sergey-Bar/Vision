# contracts

Contracts are the source of truth for the wire. Adapters, the AI runtime and
stored rows all depend on them, and a decision made months ago has to still
replay against rows written then. `tools/contractcheck` turns "do not break the
wire" into a build failure.

No language owns this directory. The Go types under `internal/contracts/` are an
output of `protoc`, not an input to anything.

## What lives here

| Path | What it is |
|---|---|
| `protobuf/security/v1/` | The canonical wire contract, package `security.v1`. 13 files. |
| `openapi/v1/openapi.yaml` | OpenAPI 3.1.0 description of the JSON API `security-core` serves. `info.version` is `1.0.0-m1`. |
| `json-schema/rule-bundle.schema.json` | The published rule-bundle schema. Byte-identical copy required at `internal/rules/schema/rule-bundle.schema.json`. |
| `json-schema/site-config.schema.json` | Static, non-secret site configuration. Structurally cannot carry a secret. |
| `json-schema/bundle-manifest.schema.json` | The signed release manifest: files, migrations, compatibility window, SBOM, signature. |
| `json-schema/ai-observations/*.schema.json` | Per-profile AI observation output schemas: `restricted-person` (production), `door-forced` and `vehicle-at-gate` (shadow). |
| `compat/baseline.json` | The frozen snapshot `contractcheck` compares against. 20 messages, 22 enums. |

### The thirteen `.proto` files

`event.proto`, `alarm.proto`, `device.proto`, `camera.proto`, `entity.proto`,
`evidence.proto`, `observation.proto`, `incident.proto`, `disposition.proto`,
`action.proto`, `edge.proto`, `policy.proto`, `job.proto`.

`internal/contracts/contractcompat_test.go` asserts all thirteen exist by name —
deleting a whole file makes every field look removed, but only if the baseline
still has it, and a baseline regenerated in the same commit hides that.

## The change workflow

Contract first, code second. Never the other way round.

1. **Edit the source of truth.** `contracts/protobuf/security/v1/*.proto`, or the
   JSON Schema, or the OpenAPI document. Not `internal/contracts/`.
2. **`make generate`.** Runs `protoc` over `contracts/protobuf/security/v1/*.proto`
   into `internal/contracts/securityv1/`, then runs `contractcheck -mode check`.
   If step 2's check fails, you have a breaking change — see below.
3. **`make contract-check`.** The gate, on its own: the same
   `contractcheck -mode check -baseline contracts/compat/baseline.json -root contracts/protobuf`.
4. **`go test -p 1 ./internal/contracts/...`** and **`go test -p 1 ./migrations/...`**.
   The first is the compatibility test that runs inside a plain `go test ./...`.
   The second is in the list because `migrations/compat.json` records
   `contractsVersion` (`"security/v1"`), both copies of it are embedded, and the
   release manifest is built from them. A contract version bump that does not
   reach `compat.json` ships a manifest that names a version the binaries do not
   speak.
5. **If you changed `rule-bundle.schema.json`, copy it** to
   `internal/rules/schema/rule-bundle.schema.json`, or run `make contract-sync`,
   which `cmp`s the two and fails with the copy command if they differ. A stale
   copy means bundles are validated against a contract nobody published.
6. **CI re-runs `make generate` and diffs `internal/contracts`.** If your working
   tree does not contain the regenerated output, the pipeline fails with
   `internal/contracts is out of date`.

### What `contractcheck` decides

It parses every `.proto` under `contracts/protobuf` into a map of
`package.Message` and `package.Enum`, and compares the current tree against
`compat/baseline.json`. It is a one-directional check: it only reports things
that would break a consumer holding the old contract. Adding a message, adding a
field on a fresh tag, adding an enum value on a fresh number — all silent,
because none of them break anybody.

The nine findings it can produce, verbatim from `tools/contractcheck/main.go`:

| Finding | What it means |
|---|---|
| `message removed` | Historical rows may no longer decode. |
| `field removed` | Historical rows lose data on re-read. |
| `field number reused` | Tag N now belongs to another field; stored rows will mis-decode. |
| `field type changed` | Tag N: `T1` -> `T2`. |
| `field cardinality changed` | Tag N `repeated` flipped. |
| `field moved out of oneof` | Tag N left a `oneof` (or joined one). |
| `enum removed` | Stored values lose their name. |
| `enum value removed` | Value N no longer decodes. |
| `enum value renamed` | Value N: `OLD` -> `NEW`. |

A narrowed type (say `int64` to `int32`) is caught as `field type changed`.

### When `-mode write` is legitimate, and when it is laundering

`-mode write` (the `make contract-baseline` target) rewrites
`contracts/compat/baseline.json` from the current tree. It is the mechanism by
which the check stops meaning what it said.

**Legitimate:** you are deliberately shipping a new contract version. The
tool's own failure message states the order — rewrite the `.proto` files, bump
the package or the `schema_version` field, *then* rewrite the baseline. So:

- The version is visible in the diff, in the package name or in `Event.schema_version`
  (`event.proto` tag 8) and in `migrations/compat.json`'s `contractsVersion`.
- The rewrite is its own commit, or at least its own reviewable hunk.
- Someone who did not write the change can say yes.

**Laundering, in descending order of how common it is:**

- Running `make contract-baseline` because `make contract-check` failed and you
  did not want to think about why. The baseline is not a lockfile to be
  refreshed; it is the record of what a deployed site already stored.
- Renaming a field and re-baselining in the same commit as an unrelated feature.
  The reviewer sees a feature diff and a green build.
- Removing an enum value because "nothing uses it" today. `IncidentState.CLOSED`
  being unreadable does not mean no stored row has it.

There is no automated test that catches a bad baseline rewrite. The gate is
review, which is why it is on this page.

## The hard rules

1. **Never hand-edit `internal/contracts/`.** It is `protoc` output. The
   `go_package` option in every `.proto` points at
   `github.com/Sergey-Bar/Ai-Captain/internal/contracts/securityv1`, and CI
   regenerates and diffs it.
2. **Contracts change before code, not after.** `AGENTS.md` and the master
   roadmap's non-negotiable sequence both say so. The order is: change
   `contracts/`, regenerate, let `contractcheck` rule, *then* change the code
   that reads it. Changing code first produces a tree that compiles against a
   contract nobody reviewed.
3. **A contract that no code reads and no adapter produces is dead weight.** Every
   message here should have a producer or a consumer in the tree today. If you
   add one, know which. The reverse test: a field nobody reads is a field that
   costs a tag number forever.

## The vocabulary

Package `security.v1`. Twenty messages, twenty-two enums. "Vendor concept
absorbed" means the message a source system's own record maps onto — the
`internal/milestone` adapter is the one implemented today
(`site-config.schema.json` lists `xprotect_milestone`, `xprotect_mip` and
`replay_fixture`; ONVIF is M4 and has no contract yet).

### Messages

| Message | What it is | Vendor concept absorbed |
|---|---|---|
| `Event` | The canonical event. Everything normalized before it reaches the graph. Uniqueness is `(tenant_id, integration_id, source_event_id)`. Three timestamps: `observed_at` (what the source claimed), `received_at` (our ingest), `normalized_at` (drift-corrected, and the only one a time-window predicate may read). | The XProtect event feed, the `Servlets.State` device feed, the alarm feed. `vendor_payload_ref` points at the immutable sanitized raw envelope. |
| `Alarm` | An alarm as a state machine, not a type. The same source id recurs with different states. | XProtect `Alarm` records. The adapter derives `alarm.created` / `.acknowledged` / `.closed` from `AlarmState`. |
| `Device` | A physical or logical thing in the Security Graph. M1 has no person model: an entity is only ever a concrete thing a device reports. | Cameras, doors, gates, sensors, alarm panels, access readers, from any source. |
| `DeviceClock` | Per-device clock state, drift and band. Migration 0011 allows a source system's clock in the same table, namespaced `source:<id>`. | A VMS that is 40 seconds fast is not one bad camera, it is every event that system produced. |
| `Camera` | The subset of `Device` that produces evidence, with resolution and low-light capability for profile scoping. | The XProtect camera record plus its online/offline transitions. |
| `Entity` | A concrete thing a device reported: a plate, a badge, a door, a gate, a vehicle. No cross-camera identity, no aliasing. | LPR plates, access-control badges, gate events. |
| `Evidence` | Addressing, hash, tier and retention law for stored bytes. `storage_ref` is storage-relative and never absolute. The bytes live behind the `EvidenceStore` interface. | XProtect bookmarks and clips, re-fetched snapshots, operator exports. |
| `AIObservation` | An immutable, fully attributed model output. `model`, `model_hash`, `prompt_template_id`, `prompt_version`, `model_bundle_signature` are mandatory; `input_evidence_ids` and `input_evidence_hashes` record exactly which bytes the model saw. | No vendor has this concept. It exists so a replay never has to re-run the model. |
| `Incident` | The operator-facing case. Carries `degraded` and `degraded_reasons` so a pilot can measure how often the AI path was live. | Not a vendor concept. The VMS has an alarm list, not a case. |
| `VerificationResult` | The deterministic outcome attached to an event or incident, with supporting and contradicting ids and the drift exclusions that refused a corroboration. | Not a vendor concept. No VMS corroborates. |
| `DriftExclusion` | One refused corroboration: device, drift, band, reason. | — |
| `Edge` | A Security Graph edge. An edge with no `asserted_by` is not allowed in. | Derived from co-occurrence across source systems. |
| `Provenance` | `asserted_by`, `confidence`, `method`. Deterministic asserted edges use confidence `1.0`; inferred edges lower it. | — |
| `EdgeEvidence` | Many-to-many: one edge justified by several independent artefacts. | — |
| `Disposition` | The operator's answer. Never overwrites the observation: "AI said X" and "operator said Y" are both stored and both replay. | The VMS acknowledgement, kept as evaluation data. |
| `Action` | A dispatched action, recorded whether or not a downstream system accepted it. `ACTION_STATUS_DEDUPED` is a first-class outcome. | PTZ moves, bookmark writes, notifications — M3's controlled action set. |
| `Policy` | A versioned, content-hashed, activatable policy. | — |
| `RuleBundle` | An immutable, versioned bundle with `content_hash`, core-version bounds and `supersedes_version`. | — |
| `Job` | A Postgres-backed queue row claimed with `FOR UPDATE SKIP LOCKED`. `dead_letter` is a terminal state, surfaced and audited, never silently dropped. | — |
| `JobResult` | A job's outcome, carrying the `AIDegradation` that produced it. | — |

### Enums

| Enum | Values | What it decides |
|---|---|---|
| `ClockSource` / `ClockSourceState` | NTP, VMS, HOST, UNKNOWN | Where a timestamp's authority came from. |
| `DriftBand` | OK, WARN, SEVERE | The bound a time-window predicate enforces. |
| `AlarmState` / `AlarmPriority` | CREATED, UPDATED, ACKNOWLEDGED, CLOSED / LOW, NORMAL, HIGH, CRITICAL | The source alarm's own lifecycle, separate from `IncidentState`. |
| `DeviceKind` | CAMERA, DOOR, SENSOR, GATE, ALARM_PANEL, ACCESS_READER, OTHER | Mirrors the `devices.kind` CHECK constraint. |
| `CameraState` / `RecordingState` | ONLINE, OFFLINE, MAINTENANCE, UNKNOWN / RECORDING, STOPPED, ARCHIVED, UNKNOWN | Operational truth about a camera. |
| `EntityKind` | LICENSE_PLATE, BADGE, DOOR, GATE, VEHICLE, DEVICE, OTHER | What a thing is. No `PERSON` on purpose. |
| `EvidenceKind` | SNAPSHOT, CLIP, BOOKMARK, EXPORT | What the bytes are. |
| `EvidenceTier` | A_AUTHORITATIVE, B_DERIVED, C_AI_OBSERVATION | The trust tier. Tier C "may be absent; never treated as negative evidence" — this is invariant 3 as an enum comment. |
| `IncidentState` | OPEN, IN_REVIEW, DISPOSITIONED, CLOSED | The operator's case lifecycle. |
| `Severity` | INFO, LOW, MEDIUM, HIGH, CRITICAL | Matches the rule DSL's `then.severity` enum. |
| `VerificationState` | CORROBORATED, UNVERIFIED, CONTRADICTED, DUPLICATE, SUPPRESSED | The deterministic outcome. `UNVERIFIED` is what a degraded AI path produces — never a negative. |
| `AIDegradation` | NONE, MODEL_UNAVAILABLE, NO_RUNTIME, TIMEOUT, ERROR, ABSENT | The closed reason set. The Python runtime's `ALLOWED_DEGRADATIONS` mirrors it exactly, and a reason outside it would be stored as a string nothing recognises. |
| `DispositionOutcome` | TRUE_POSITIVE, FALSE_POSITIVE, BENIGN, ESCALATED, ACKNOWLEDGED, DISMISSED | The operator's verdict; evaluation data. |
| `OperatorAgreement` | NO_AI_OBSERVATION, AGREED, DISAGREED, UNSURE | Agreement with the observation. Never fed back into it. |
| `ActionStatus` | REQUESTED, DISPATCHED, COMPLETED, FAILED, DEDUPED | M3's action lifecycle. |
| `JobQueue` | VLM, EVIDENCE_FETCH, RULE_BULK, CHECKPOINT_SEAL | The four queues. `JOB_QUEUE_VLM` currently has no producer or consumer in the Go tree (master-roadmap finding C1). |
| `JobStatus` | PENDING, LEASED, DONE, FAILED, DEAD_LETTER | `DEAD_LETTER` is terminal and audited. |
| `EdgeMethod` | OBSERVED, ASSERTED, INFERRED | How an edge came to exist. |
| `RuleEvaluationMode` | LINT, EXPLAIN, SIMULATE, PRODUCTION | Which surface is being run. |

## The known deviation: the `eventType` vocabulary

`docs/status.md` deviation 7 records this, and it matters when you touch the
rule schema.

The rule DSL's `when.eventType` vocabulary is **not** the vendor's. It is the
adapter's normalized lowercase set — `motion`, `analytics`, `alarm.created`,
`alarm.acknowledged`, `alarm.closed`, `device.camera_offline`,
`device.camera_online`, `device_state`, `recording.started`,
`recording.stopped`, `bookmark`, `metadata`, `access.granted`, `access.denied`,
`gate` — declared identically in three places:

- `contracts/json-schema/rule-bundle.schema.json` (`condition.eventType.items.enum`)
- `internal/rules/bundle.go` (`KnownEventTypes`)
- `internal/rules/schema/rule-bundle.schema.json` (the embed, byte-identical)

**The adapter is the source of truth for that set**, because
`normalizeEventType` in `internal/milestone/adapter.go` is the only place the
mapping lives. A vendor's PascalCase spelling is an adapter detail that must
never appear in a bundle: a bundle has to be portable between sites and between
replay mode and integration mode.

Two things to know before you edit any of the three:

- The vocabulary is **closed**, deliberately. A bundle naming a type outside it
  is a semantic error, not a forward-compatible extension: "a rule that can never
  match is a rule an operator believes is running" (`internal/rules/bundle.go:282`).
- A separate, open defect is recorded against the adapter's own
  `KnownMilestoneEventTypes` table — master-roadmap finding C3, where 12 of 18
  keys are spellings `normalizeEventType` never produces and 9 produced types
  are missing. It is bounded today only because `RawRecord.Malformed` has no
  consumer outside the adapter. Do not "fix" the schema to match that table; the
  schema is right and the table is the defect.

## Versioning and compatibility

**Adding without breaking.** Give a new field the next unused tag. Never reuse
a retired one, never renumber. Gaps are legal and already exist: `Evidence` runs
1-11 and then 13. A gap is a reserved number, not an error.

**What the baseline records.** For every message: each field's `number`, `name`,
`type`, `repeated` and `oneof`. For every enum: each value's `number` and `name`.
Nothing else — no comments, no options, no field descriptions.

**What a consumer may assume about a field it has never seen.**

- A protobuf consumer built against the older `.proto` decodes a stored row
  containing an unknown tag without error, ignores it, and (in protobuf-go)
  preserves it in the message's unknown-field set rather than dropping it.
  It is not required to understand the new field, and must not break because of it.
- An enum consumer must tolerate an unknown number. Proto3 enums are open:
  an unrecognized numeric value round-trips as itself. Renaming a value
  (`contractcheck` finding `enum value renamed`) is therefore a wire-visible
  change for every JSON and log consumer, and removing one is worse.
- A JSON consumer following `openapi.yaml` or a JSON Schema sees an absent
  property. Schemas here are `additionalProperties: false` in both directions,
  which is a *validation* rule, not a compatibility rule.

**Two limits of the gate, so you do not trust it more than it is:**

1. **It only reads protobuf.** `contractcheck` parses `.proto` files. The
   OpenAPI document and the five JSON Schemas have no compatibility diff. Their
   presence is asserted by `TestOtherContractSurfacesExist`, and that test's own
   comment says why: "nothing in the build currently diffs them."
2. **Its parser is regex-based and skips some fields.** `fieldRe` in
   `tools/contractcheck/main.go` does not match a `map<...>` declaration, so
   `Device.attributes` (tag 10) is absent from `baseline.json` and is invisible
   to the check. A map field can be renamed, renumbered or removed with no
   finding. Nested messages, services and extensions are likewise not read.
   Review those by hand.

## Agent checklist

Before you touch a contract:

1. **Change `contracts/` first, regenerate, and let `contractcheck` rule.** Code
   second. Never edit `internal/contracts/`.
2. **Use a fresh tag number and never reuse one.** A reused tag mis-decodes
   stored rows silently; there is no error, no test failure, just a months-old
   decision that replays wrong.
3. **If you changed `rule-bundle.schema.json`, copy it to
   `internal/rules/schema/` in the same commit** (`make contract-sync` proves
   it). If you changed the event vocabulary, change the schema and
   `internal/rules/bundle.go` together, and do not change
   `KnownMilestoneEventTypes`.
4. **Know which commit bumps the version.** If the change is breaking, it is a
   new contract version: bump the package or `Event.schema_version`, update
   `migrations/compat.json`'s `contractsVersion`, and rewrite the baseline in
   its own reviewable change.

**The mistake that costs the most:** reusing or renumbering an existing field
tag and then silencing `make contract-check` with `-mode write`. It is the only
mistake here whose failure is silent, permanent and undetectable after the fact:
rows already written to Postgres and already referenced by a sealed audit
checkpoint decode into a different field, an incident replays to a different
explanation, and the hash chain still verifies because the chain is over the
stored bytes, not over what they mean.
