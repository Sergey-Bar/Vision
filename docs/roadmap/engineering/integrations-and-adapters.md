# Integrations and Adapters

Owner: E1 (Product/Architecture). Status: living. Last revised 2026-09-28.

Scope: the M1–M4 integration architecture track. This is the most important
architectural track in the company, because the second VMS adapter is the real
test of whether the abstraction holds, and because `master-roadmap.md` §M4 sets
one gate that is not negotiable: **nothing outside the adapter may change to
support the second adapter.** That statement is a claim about code we have not
written yet, so this document's job is to make it cheap to falsify.

Read `strategy-and-north-star.md` §6 (the capacity law), `master-roadmap.md` §M4,
`quality-engineering.md` §2.4 and §2.5, `trust-and-security-program.md` §3.7,
`decisions-and-process.md` §2.2, and `status.md` first. `docs/status.md` is the
authoritative build state; this document does not restate it.

Everything asserted below was read in the tree. Where something does not exist,
this document says so and says what it must be.

---

## 1. The adapter contract as it exists today

### 1.1 Where the seam actually is

The seam is `internal/milestone`. The package doc is at `routes.go:1-19` and it
is unusually honest about its own confidence, which is the first thing an
integrations engineer should read:

> Milestone's public API surface is assumed, not verified... a wrong endpoint or a
> wrong field name shows up as an empty event feed on a real site, not as a
> failing test.

The seam is one interface, `Transport` (`routes.go:137-143`):

```go
type Transport interface {
    Get(ctx context.Context, route Route, query Query) (*Response, error)
    Close() error
}
```

Three properties of that interface are the architecture, and each was chosen
deliberately:

1. **It is at the byte level, not the object level.** `Response.Body` is
   `json.RawMessage` (`routes.go:161-168`). The comment at `routes.go:131-136`
   gives the reason: if the transport returned parsed objects, the fixture path
   would have to parse the JSON itself, producing a second parser that could
   drift from the first — which is precisely the bug replay-equality exists to
   catch rather than create. There is exactly one `json.Unmarshal` of a source
   body in the adapter: `Adapter.Parse` at `adapter.go:188`.
2. **The route is a value, not a URL string.** `Route` (`routes.go:30-48`)
   carries `Method`, `Path`, `Expect`, `Paginated`, `Unverified` and `Notes`.
   `Unverified` is a *field* rather than a comment specifically so that a probe
   command and a test can both read it (`routes.go:41-45`).
3. **A transport failure is typed, not a status code.** `UnauthenticatedError`,
   `MalformedError`, `NotFoundError` (`routes.go:172-212`) and `IsNotFound`
   (`routes.go:200-203`) exist because "credentials rejected" is an operational
   incident with an operator-visible remedy and "404" is a normal race between a
   listing and a detail fetch. Collapsing them into one error type is how a
   partial Milestone installation becomes a stream of alerts.

### 1.2 What a source must provide

A source adapter, in the shape the M1 code already implies, must provide:

| Obligation | Where it is expressed today | Enforced by |
|---|---|---|
| A `Route` list, every entry with an `Unverified` flag and a `Notes` saying what is assumed | `Routes` var, `routes.go:57-106` | `UnverifiedRoutes()` is exported so a test can assert the list shrinks; today nothing asserts it is non-empty |
| A `Transport` that returns status + allow-listed headers + body bytes | `routes.go:137-168` | Interface only |
| A per-page decode that accepts the real envelope shape | `decodeItems`, `adapter.go:358-385` | `adapter_test.go` |
| A per-item field extraction for identity and type | `toRecord`, `adapter.go:239-284` | `adapter_test.go` |
| A mapping from vendor vocabulary to the canonical lowercase set | `normalizeEventType`, `adapter.go:321-356`; `deriveEventType`, `adapter.go:288-313` | `rules.KnownEventTypes` (downstream) |
| A device-reference resolution rule | `sourceDeviceRef`, `adapter.go:95-110` | `adapter_test.go` |
| A timestamp parse list, and a rule for what a naive timestamp means | `milestoneTimeLayouts`, `adapter.go:406-414`; `parseMilestoneTime`, `427-445`; `declaredZone`, `448-471` | `TestTimezoneProvenanceIsPreserved` |

### 1.3 What the adapter is responsible for

Precisely three things, stated at `adapter.go:13-17`: **read, sanitize, hand
over.** Its output type is `RawRecord` (`adapter.go:49-83`) and there is no
other. That is not stylistic; it is what makes `kill -9` at this stage
testable, because the adapter's whole responsibility ends at the moment the
record is written.

`Adapter.Poll` (`adapter.go:131-164`) is where three policies live that every
future adapter must copy verbatim:

- **Only paginated routes are polled** (`adapter.go:132-137`). The filter is by
  `Route.Paginated`, so a new adapter's single-object routes (device detail,
  server time) are skipped by construction.
- **A feed failure is isolated and recorded** (`adapter.go:145-153`): the error
  becomes one `RawRecord{EventType: "adapter_feed_error", Malformed: true,
  MalformedReason: err.Error()}` and the poll continues. The stated reason is
  that a partial outage must not become a total ingest outage, because the SLO
  counts accepted events, not whole feeds.
- **Order is by `(SourceEventID, ObservedAt)`, never arrival** (`adapter.go:157-163`).
  The comment names the reason: an adapter that returns socket order is one whose
  replay mode and integration mode can disagree, and `master-roadmap.md` §M1 gate 1
  depends on them not.

`Adapter.Parse` adds two more policies:

- **404 is a normal state, checked twice** — once as a typed error
  (`readFeed`, `adapter.go:169-172`) and once as a `Response.Status == 404`
  (`adapter.go:179-181`). The comment explains the second check: a transport that
  returns a 404 response rather than a 404 error must not turn a partially
  deployed Milestone installation into a stream of alerts.
- **One unreadable item must not lose the page** (`adapter.go:216-231`). The item
  becomes an `adapter_unparsed_item` record with a synthetic, stable
  `SourceEventID` of `<integration>:<routePath>:<index>`, precisely so it is not
  re-ingested on every poll (`adapter_test.go:166-168` asserts that identity).

### 1.4 Where vendor-specific knowledge is allowed to live

**Only in three places in the whole tree**, and the second and third are the
`Transport` implementations that do not exist yet:

1. `internal/milestone/routes.go` — endpoint paths and the assumptions about them.
2. `internal/milestone/adapter.go` — the field-name and vocabulary mapping
   (`stringField` calls, `deviceRefPattern`, `milestoneTimeLayouts`,
   `normalizeEventType`, `deriveEventType`).
3. `internal/fixture/sanitize.go` and the `Transport` implementation itself — the
   bytes and the allow-list.

`internal/milestone/errors.go` is 17 lines and holds only `asNotFound` and
`describe`; the error types themselves live in `routes.go` next to the transport
that produces them, which is the right adjacency and is worth preserving.

### 1.5 Which invariants are enforced at the adapter boundary, and which above it

| Invariant (`AGENTS.md`) | Enforced at the adapter boundary? | Where it is actually enforced |
|---|---|---|
| **1 — zero accepted event loss** | **Partly.** The adapter never drops: unparseable items, unknown types and feed errors all become records. But the adapter writes nothing, so *acceptance* is not its concern. | `inbox.Writer.Write` (`inbox.go:124-172`) takes the scoped key before the row in one transaction; `Writer.Fail` (`inbox.go:365-390`) is the only route to `dead_letter`; `Writer.Replay` (`inbox.go:442-458`) is operator-initiated. |
| **2 — decisions replay from stored facts** | **No, and correctly so.** The adapter has no decision to replay. What it provides is the *replay transport*: `ReplayTransport` (`replay.go:18-137`) is the same code path with different bytes. | `incidents.Replay` (`incidents/replay.go:79-110`) and `ReplayInputs` (`replay.go:55-70`), which has no field for a model client, a bundle path or a clock — "the struct cannot be given the means to re-run anything". |
| **3 — a missing AI observation is never a negative observation** | **No.** The adapter knows nothing about AI. | `rules/semantic.go:419-440` — `consumesAI` requires `ifAIAvailable` or the bundle fails to compile. |
| **4 — audit ledger append-only and hash-chained** | **No.** | `audit.Entry` validation plus the trigger-computed chain in `migrations/0007_audit.expand.sql`; `checkpoint` seals. |
| **5 — `tenant_id` `NOT NULL`, never from a request body** | **Yes, and it is the adapter's most interesting boundary decision.** `Adapter` has **no tenant field at all** (`adapter.go:23-27`): the comment says a transport-supplied tenant "would be a tenant read from a file". | `Adapter.tenant()` returns `""` (`adapter.go:237`) and the caller supplies the tenant. The `NOT NULL` is enforced by the schema and by `inbox.validateWrite` (`inbox.go:174-192`). |

The adapter contributes most strongly to invariants 1 and 2, and contributes
nothing at all to 3, 4 or 5 beyond the tenant decision. That is worth stating
plainly because the M4 gate is usually described as an integration gate, and it
is mostly a *replay-parity and no-drop* gate.

### 1.6 Four real gaps in the current seam, named

These are stated here because every one of them is a thing the second adapter
will trip over, and finding them during the second adapter means the abstraction
did not hold.

**(a) `Adapter.Poll` never touches the two non-paginated routes.**
`/api/v1/Events/{eventId}` and `/api/v1/System/Time` are `Paginated: false`
(`routes.go:72-76`, `100-105`). The filter at `adapter.go:132-137` skips them.
Consequence: the `/api/v1/System/Time` route exists to "measure clock drift
between the VMS and this host", and there is **no code path in the tree that
calls it**, so `device_clocks` (`migrations/0003_events_and_graph.expand.sql`) is
only ever populated by whatever a future clock worker writes. `normalize.loadDrift`
(`normalize.go:246-268`) reads it and treats `ErrNoRows` as "not measured yet" —
correct, and permanently true in M1. **What must exist:** a clock-sampling
routine in the adapter interface (`SampleClock(ctx) (driftMS int32, ok bool,
err error)`) or an equivalent, plus a worker, or the `DriftBand` severe path in
`observability/slo.go` can never fire from a real VMS.

**(b) `RawRecord.Malformed` has no consumer.** `Malformed` and `MalformedReason`
(`adapter.go:73-78`) are set at `adapter.go:218-229`, `280-282` and `148-152`.
`rg '\.Malformed'` across the tree returns hits **only** in
`internal/milestone/adapter.go` and `internal/milestone/adapter_test.go`. Neither
`normalize.Input` nor `inbox.WriteOptions` has a `Malformed` field. So the
"carry, don't drop" guarantee is currently an *adapter-local* property with no
downstream expression, and the reason string travels nowhere.

**(c) `KnownMilestoneEventTypes` is a vendor-shaped map checked against a
normalized name, and 13 of its 19 keys are unreachable.**
`toRecord` calls `deriveEventType` first (`adapter.go:255`) and then does
`if !KnownMilestoneEventTypes[eventType]` (`adapter.go:277`). But
`deriveEventType` always returns one of the outputs of `normalizeEventType`
(`adapter.go:288-356`), which are lowercase. The map
(`adapter.go:116-123`) is keyed `"Motion"`, `"Analytics"`, `"MotionDetected"`,
`"alarm"`, `"RecordingStarted"`, `"RecordingStopped"`, `"Bookmark"`,
`"Metadata"`, `"AccessGranted"`, `"AccessDenied"`, `"GateEvent"`,
`"DeviceState"` — none of which `normalizeEventType` can ever return. The
reachable keys are exactly six: `alarm.created`, `alarm.acknowledged`,
`alarm.closed`, `device.camera_offline`, `device.camera_online`, `device_state`.

The consequence is not theoretical: a source event `{"Type":"Motion"}` yields
`EventType: "motion"` **and** `Malformed: true,
MalformedReason: unrecognised source event type "Motion"`. The same is true for
`Analytics`, `RecordingStarted`, `AccessGranted`, `AccessDenied`, `GateEvent`,
`Bookmark`, `Metadata` — i.e. nine of the canonical types the production bundle
in `rules/builtin/bundle.yaml` matches on (`eventType: [motion, analytics]`).
`adapter_test.go:208-219` counts `case "motion": good++` and never asserts
`Malformed == false`, so the suite is green.

Two things are true at once and both belong in the M1 backlog: the *current*
blast radius is bounded, because (b) means `Malformed` is read by nobody; and
the day someone wires `Malformed` to a dead-letter or a quarantine, the entire
site's motion feed dead-letters at once. The fix is a rename to
`KnownCanonicalEventTypes` keyed on the *output* vocabulary, ideally generated
from the same source as `rules.KnownEventTypes` (`rules/bundle.go:285-292`) so
the two cannot drift. See also `status.md` deviation 7.

**(d) `ReplayTransport`'s epoch override is dead code.**
`replay.go:92-104` computes `epoch` from `t.epoch.At` or `sc.Epoch` and then
discards it with `_ = epoch` (`replay.go:104`). `fixture.Event.ObservedAt(epoch)`
(`fixture/fixture.go:98-100`) exists and `rg 'ObservedAt\('` shows **no caller
anywhere in the tree**. The offset machinery is therefore real — `OffsetMS` is
stored, validated, sorted on and hashed into `Scenario.ContentHash()`
(`fixture.go:105-115`) — and then never applied to anything.

The consequence for the M1 gate is precise and must be understood before the
gate is run: **replay parity is a byte-parity property today, not a
timing-parity property.** `ReplayTransport.Get` serves `ev.Envelope` verbatim
(`replay.go:118-125`), so the only timing that exists in replay is whatever
absolute timestamp is baked into the stored envelope. A fixture that stores
absolute timestamps is timing-deterministic but stops being a "relative offsets"
fixture; a fixture that stores relative offsets replays with no timing at all.
The package doc at `fixture.go:19-22` promises the second and the transport
implements the neither. **What must exist:** `ReplayTransport.Get` must rewrite
envelope timestamps to `epoch + OffsetMS` through a declared, tested
substitution — or, preferably, the normalizer must be given an epoch override so
the stored bytes stay verbatim (which is the property the package doc actually
argues for: "The envelope is stored verbatim, in the same bytes the source
produced").

---

## 2. The canonical event model

### 2.1 The vocabulary, and where each vendor concept lands

`contracts/protobuf/security/v1/` holds thirteen protos. The one that matters
most for adapters is `event.proto` — the canonical envelope, uniqueness scoped to
`(tenant_id, integration_id, source_event_id)` (the file's own header comment).

The canonical vocabulary that a rule bundle may name is defined twice, and the
two definitions are currently identical by hand:

- `rules.KnownEventTypes` — `rules/bundle.go:285-292`
- `contracts/json-schema/rule-bundle.schema.json` `eventType.items.enum` — lines 117-125

Fifteen names: `motion`, `analytics`, `alarm.created`, `alarm.acknowledged`,
`alarm.closed`, `device.camera_offline`, `device.camera_online`, `device_state`,
`recording.started`, `recording.stopped`, `bookmark`, `metadata`,
`access.granted`, `access.denied`, `gate`.

**The adapter is the source of truth for this vocabulary, and `status.md`
deviation 7 says so explicitly**: "The rule schema's `eventType` vocabulary
follows the adapter's normalized names." That is the correct direction of
dependency and the M4 second adapter must not change it. A second adapter that
produces a name outside the set does not get a schema amendment first; it maps
onto the existing set, or the platform declines to call it an event type.

| Canonical name | Vendor concept absorbed (Milestone) | Vendor concept absorbed (ONVIF, M4) | Vendor concept absorbed (access control, M4) |
|---|---|---|---|
| `motion` | `Motion`, `MotionDetected` | `tt:Motion` in Profile T | — |
| `analytics` | `Analytics`, and — by assumption — `ObjectDetected`, `LineCrossed`, `Loitering` (`adapter.go:326`) | `tt:Analytics` Profile M: people, vehicles, licence plates, object counters | — |
| `alarm.created` | `Alarm` with `AlarmState` default; also `/api/v1/Alarms` | `tt:StateEvent` entering an alarm state | — |
| `alarm.acknowledged` | `Alarm` + `AlarmState ∈ {acknowledged, acked}` | same | — |
| `alarm.closed` | `Alarm` + `AlarmState ∈ {closed, resolved}` | same | — |
| `device.camera_offline` | `Servlets.State` / `ServiceState` with state ∈ {offline, unavailable, disconnected} | Profile T `tt:Source` / device heartbeat loss | — |
| `device.camera_online` | same, {online, available, connected} | same | — |
| `device_state` | same, anything else | same | reader, panel, environmental, BMS health |
| `recording.started` | `RecordingStarted` | `tt:Recording` (not a standard ONVIF event; a configuration state) | — |
| `recording.stopped` | `RecordingStopped` | as above | — |
| `bookmark` | `Bookmark`, `/api/v1/Bookmarks` | — (no ONVIF equivalent; a platform concept) | — |
| `metadata` | `Metadata` | `tt:Analytics` metadata frames | — |
| `access.granted` | `AccessGranted` | — | reader grant, credential presented and accepted |
| `access.denied` | `AccessDenied` | — | reader denial, bad credential, anti-passback |
| `gate` | `GateEvent` | — | barrier open/close/loop-detector |

### 2.2 The `Alarm` special case, which is the model for every state machine

`deriveEventType` (`adapter.go:288-313`) exists because "the transition is the
event" for a state machine. An alarm arrives as one `Type: "alarm"` with a
changing `AlarmState`; the same source id appears three times with three states;
a rule correlates on the transition, not on the object. The adapter therefore
synthesises the transition, and `alarm.proto`'s `AlarmState` enum (created,
updated, acknowledged, closed) is what it derives from.

Every future adapter that has a state machine — a door, a barrier, a camera's
maintenance flag — must do the same thing, and the generalisation is worth
extracting into the SDK (§7) as a `StateTransition` helper. Otherwise each
adapter invents its own spelling and the vocabulary fragments.

### 2.3 The other twelve protos, and what an adapter may and may not populate

| Proto | What it is | Adapter's role |
|---|---|---|
| `device.proto` | `Device`, `DeviceKind` (camera, door, sensor, gate, alarm_panel, access_reader, other), `DeviceClock`, `ClockSourceState`, `DriftBand` | Adapter *proposes* devices; registration is an operator act. `source_device_id` (`device.proto:6`) is the only vendor identifier that ever enters the canonical model, and it does so as an opaque string. |
| `camera.proto` | `Camera` + `CameraState` (online/offline/**maintenance**/unknown) + `RecordingState`, plus `width`, `height`, `low_light_capable` | Adapter proposes; `CameraState` is the natural target of `device.camera_offline` and of the M6 camera-health work. `width`/`height` exist "used by the AI plane to scope a model profile" — a vendor *capability* crosses as a number, never as a profile name. |
| `alarm.proto` | `Alarm` with `AlarmState`, `AlarmPriority`, `acknowledged_by`, `attributes` | See §2.2. `AlarmPriority` is a **vendor-shaped enum and must not become the canonical severity** — see §3.4. |
| `entity.proto` | `Entity` + `EntityKind` (license_plate, badge, door, gate, vehicle, device, other) | The M1 scope decision, stated in the proto's own comment: "An entity is a concrete thing a device reported: a plate, a badge, a door. M1 deliberately has no person model, no cross-camera identity and no aliasing." |
| `evidence.proto` | `Evidence` + `EvidenceKind` (snapshot, clip, bookmark, export) + **`EvidenceTier` A/B/C** | The tier is the spine of invariant 3. A is "authoritative system state reported by the source of truth", B is "deterministic derived fact", C is "AI observation. May be absent; never treated as negative evidence." |
| `edge.proto` | `Edge` + `Provenance` + `EdgeMethod` (observed / asserted / inferred) + `EdgeEvidence` | Adapter-produced topology edges are `observed`; §9. |
| `observation.proto` | `AIObservation` | Adapter does not touch it. See `ai-and-data.md`. |
| `incident.proto` | `Incident`, `IncidentState`, `Severity`, `VerificationResult`, `VerificationState`, `DriftExclusion`, `AIDegradation` | Adapter does not touch it. |
| `action.proto` | `Action` + `ActionStatus` (requested, dispatched, completed, failed, **deduped**) | M3. `idempotency_key` and `DEDUPED` are the replay-safety fields. |
| `policy.proto` | `Policy`, `RuleBundle`, `RuleEvaluationMode` | `internal/policy` does not exist; see `ai-and-data.md` §9. |
| `job.proto` | `Job`, `JobQueue` (vlm, evidence_fetch, rule_bulk, checkpoint_seal), `JobStatus`, `JobResult` | Adapter enqueues `evidence_fetch`; it must not enqueue `vlm` directly (§7.3). |
| `disposition.proto` | `Disposition`, `DispositionOutcome`, `OperatorAgreement` | Adapter does not touch it. |

### 2.4 Known vocabulary gaps

Named now, because each one is a decision the M4 adapters have to make and each
has a default that is wrong if left implicit.

1. **No `access.forced` and no `access.held_open`.** The master roadmap calls
   forced-door and held-open "among the highest-value correlations in the whole
   product" and they are the reason access control is in M4, but the canonical
   vocabulary has only `access.granted` and `access.denied`. A door that reports
   "forced" today can only reach us as a `vendor.Message` string — and
   `rules/builtin/bundle.yaml:46-49` already shows a shipped suppression rule
   reading exactly that path (`path: vendor.Message`,
   `op: contains_prefix, value: "maintenance"`). See §8.2.
2. **No `sensor.*` family at all.** Intrusion, perimeter, environmental,
   intercom, gate/barrier, LPR and BMS all have to land somewhere. The worst
   available answer today is `device_state`, which is lossy and
   non-differentiating. See §8.3.
3. **No notion of a *person*.** Deliberate (`graph.go:16-22`), and it is a scope
   decision, not a gap — but it means a `restricted-person` VLM profile produces
   `persons[]` in an observation payload that the graph cannot represent. The
   observation is Tier C prose-adjacent structure; the graph has nowhere to put
   it. That is a real seam to design at M4/M6, not a bug.
4. **`unknown.<lower>` is a legal `EventType`.** `normalizeEventType`
   (`adapter.go:355`) returns it for anything unrecognised, and it is *not* in
   `rules.KnownEventTypes` — so a rule cannot name it, which is correct, but it
   does mean unrecognised source types reach `canonical_events.event_type` as a
   namespace nothing else understands. The backpressure metric is supposed to
   surface it; `metrics-and-analytics.md` §7.1 is the right place for that number.
5. **No `priority` in the canonical event.** `AlarmPriority` exists in
   `alarm.proto`; the canonical `Event` has no severity or priority column, so a
   vendor's priority is currently only reachable through `payload.vendor`. See
   §3.4.
6. **No canonical zone concept with a vendor identity.** `routes.go:91-95` says
   `/api/v1/ResourceGroups` is "zone-equivalent topology. Mapped onto the ZONE
   graph nodes" — but `normalize.Input.ZoneID` is resolved by the *caller*, and
   the adapter's `toRecord` never produces one. Zone resolution does not exist
   yet at all, which blocks every rule in the production bundle that filters on
   `zone.pathPrefix: /restricted`.

---

## 3. The port/adapter layering

### 3.1 The rule

**A vendor concept may exist in exactly one place — inside the adapter package —
and crosses into the platform only as a canonical value the platform already
defines.** The test of an adapter is not "does it work" but "can the entire rest
of the tree be diffed against a second adapter with no behavioural change."

### 3.2 The leak table

| # | Where a vendor concept could leak | The concrete forbidden pattern | The rule that keeps it out | The test — and whether it exists today |
|---|---|---|---|---|
| 1 | **Vendor enum reaching the graph** | A `from_type`/`to_type` of `MILESTONE_CAMERA`, or a `device.kind` of `"Servlets.State"`, or an `EntityKind` addition per vendor | Graph node types are the closed set in `graph.Relations`/`graph.Methods` (`graph.go:46-63`) plus the node types named in the package doc (`graph.go:5-15`): SITE, ZONE, DEVICE, EVENT, INCIDENT, EVIDENCE, OBSERVATION, ENTITY | **MISSING.** No test asserts the set of node types written to `edges`/`edges_active`. A CI check over `migrations/0003_events_and_graph.expand.sql` for a CHECK constraint on `from_type`/`to_type` is the cheap version. |
| 2 | **Vendor field names in the API** | `GET /v1/incidents/{id}` returning `milestoneAlarmId`, `servletsState`, or `TimeSpan`; a console template printing `vendor.Message` | The API's JSON is the protobuf's JSON — `normalize.ToProto` exists "so the contract is not decorative" (`normalize.go:402-407`). `openapi.yaml` is the published surface; the console renders it | **PARTIAL.** `openapi.yaml` describes no vendor field, and `api/handlers.go` returns `map[string]any` built by hand rather than by protojson, so nothing mechanically prevents a drift. **Recommended:** a test that renders every read handler and asserts the key set is a subset of the proto message's field names, or `api/handlers.go` switches to `protojson`. |
| 3 | **Vendor time semantics** | A rule reading `observed_at`; a `normalized_at` computed without drift; a naive timestamp silently assumed to be the site's zone; a host-local `time.Now()` reaching a stored value | A time-window predicate may read `normalized_at` and nothing else — enforced by `semantic.go:302-313`, which errors with code `time_window_field` on anything but `normalized_at`. Naive timestamps keep an empty `SourceTimezone` (`adapter.go:435-439`, asserted by `TestTimezoneProvenanceIsPreserved`). Drift is `device_clocks` via `normalize.loadDrift` | **EXISTS and is good.** `semantic.go:302-313` plus `adapter_test.go:115-144` plus `normalize.NormalizedTime` (`normalize.go:275-286`), whose zero-observed-time branch is a real case, not an error. Add at M4: a fixture per vendor with a DST-spanning window. |
| 4 | **Vendor severity mapping** | `Milestone Priority: 2` becoming `Severity.HIGH`, or a rule bundle matching on a vendor's own priority scale | The adapter must not map vendor severity into the canonical `Severity` at ingest. The rule bundle's `then.severity` is the only place a severity is chosen, and it is an operator's declaration in a reviewed, activated, versioned artifact | **MISSING and actively at risk.** `alarm.proto` has `AlarmPriority` (low/normal/high/critical) and `incident.proto` has `Severity` (info/low/medium/high/critical) — two near-identical enums one hop apart. **Recommended:** a compile-time or CI assertion that `internal/milestone` does not reference `securityv1.Severity` or `securityv1.AlarmPriority`, and a rule that the two are never translated in the adapter. |
| 5 | **Vendor string reaching a rule as identity** | `field.path: vendor.Message` (which the shipped bundle does, twice) | A rule's `field.path` must resolve into the *canonical* half of `canonicalPayload` (`normalize.go:314-326`), which is five keys plus `unregisteredDevice`. Anything under `vendor` is untrusted vendor text | **PARTIAL.** `validFieldPath` (`semantic.go:337-343`) checks the *shape* of the path, not which half it addresses. **Recommended:** the semantic validator refuses any `field.path` whose first segment is `vendor`, with a diagnostic naming the canonical replacement. See §3.5. |
| 6 | **Vendor device identity as a primary key** | `SourceDeviceRef` matching "Entrance Camera 01 [CAM-001]" being stored as `device_id` | The adapter emits an *opaque* `source_device_ref`; the caller resolves it to a registered `device_id`; an unresolvable reference stays empty and the event is marked `unregisteredDevice: true` (`normalize.go:321-325`) — "unattributable rather than attributed to something guessed" | **EXISTS in the adapter** (`sourceDeviceRef`, `adapter.go:95-110`, with the explicit refusal to treat a bare human-readable label as an id). **MISSING above it:** the resolver. No code maps `source_device_ref` → `device_id`; `normalize.Input.DeviceID` is set by a caller that does not exist yet. |
| 7 | **Vendor event-type spelling in a bundle** | `eventType: [MotionDetected]` | `KnownEventTypes` + `knownEventType` (`rules/bundle.go:294`), error code `unknown_event_type` (`semantic.go:248-256`): "a rule that can never match is a rule an operator believes is running" | **EXISTS.** Plus the JSON Schema enum, so a bundle is refused at parse time too. `TestUnknownEventTypeIsRejected` (`compile_test.go:224`). |
| 8 | **Vendor credential or host material** | A committed fixture containing a MAC address, a person image, an absolute path, or an unexpected header | Sanitize at capture time by `internal/fixture/sanitize.go`; keep only allow-listed headers (`fixture.go:154-159` rejects a non-allow-listed one); `/api/v1/System/UserGroups` is used for "camera identity only" (`routes.go:61-62`) | **EXISTS** and is enforced in the fixture format itself. `trust-and-security-program.md` §3.7 is the owning policy. |
| 9 | **Vendor error string as an alert** | "Milestone returned 500" paging an operator at 03:00 | Typed errors, per-feed isolation, and an explicit `adapter_feed_error` record type the console can group rather than surface | **EXISTS** (`adapter.go:145-153`, `Parse`'s status switch at `adapter.go:192-200`). `TestFeedFailureIsIsolated` (`adapter_test.go:208`). |
| 10 | **Vendor route list in the core** | A `switch route.Path` in `internal/api` or `internal/incidents` | Routes are adapter data; the core never names a path. The core knows `integration_id` and nothing else about the source | **EXISTS structurally.** `rg 'milestone\.' --glob '*.go'` outside the package returns only `tools/fixturegen`, `cmd/securityctl/rules_simulate_support.go`, `internal/fixture/sanitize.go` and tests. Note the last two are fixture-side, not core-side. |
| 11 | **Vendor concepts in the schema migrations** | A `milestone_alarm_id` column, a `genetec_device` enum | SQL lives in `migrations/`, forward-only, and a schema change is a contract decision (`decisions-and-process.md` §2.3) | **EXISTS.** No vendor-named table, column or enum in `0001`–`0012`. Verified by reading the `CREATE TABLE`/`CREATE INDEX` inventory. |
| 12 | **Vendor *absence* becoming a fact** | A 404 from `/api/v1/ResourceGroups` recorded as "this site has no zones" | A not-deployed feed returns no records at all (`adapter.go:169-172`, `179-181`); absence of a feed is not evidence about the site | **EXISTS.** Worth a test at M4 for every new route. |

### 3.3 The three highest-value tests that do not exist

1. **Node-type closure.** Assert the set of `(from_type, to_type)` values ever
   written to `edges` is a subset of the documented node types, and add a CHECK
   constraint in a new migration so a rogue writer cannot. Cost: one migration,
   one test.
2. **Read-model key closure.** Render every read handler in `internal/api` and
   assert no response key matches a vendor identifier pattern
   (`(?i)(milestone|genetec|avigilon|axis|flir|onvif)[A-Za-z]*`), and that the
   key set is a subset of the corresponding proto message's fields. Cost: one
   table-driven test. This is the mechanical form of "vendor field names in the
   API" and it is what makes the M4 gate checkable rather than aspirational.
3. **Severity laundering.** Assert `internal/milestone` (and every future
   adapter) does not import or construct `securityv1.Severity` or
   `securityv1.AlarmPriority`. Cost: a lint rule or a `go/ast` test, ~30 lines.
   `release-sre-and-support.md` §4 and `trust-and-security-program.md` §6.2 both
   want a "definition of done for a hardening item", and this is a one-line
   version of it.

### 3.4 The severity problem, stated properly

`alarm.proto` `AlarmPriority` and `incident.proto` `Severity` are two four-or-
five-value ordered scales, one hop apart, with overlapping names. The plan's
answer is already in the bundle schema: the rule's `then.severity` is the only
severity an incident may have, and it is chosen by an operator in a reviewed
artifact. The gap is that nothing stops an adapter from inventing a third path —
a background job that reads `payload.vendor.Priority` and writes
`incidents.severity`. **The rule is: severity is a decision, and decisions live
in activated rule bundles.** Everything upstream of a rule is a fact, and facts
have no severity. That is a cleaner statement than "map the vendor's priority
carefully", and it is the one to enforce.

### 3.5 The `vendor.Message` suppression rule, named as a finding

`rules/builtin/bundle.yaml:39-52`:

```yaml
- id: core.suppress.camera-offline-during-maintenance
  when:
    eventType: [device.camera_offline]
    field:
      path: vendor.Message
      op: contains_prefix
      value: "maintenance"
  then:
    createIncident: false
    suppress: true
```

This is the only place in the shipped bundle where an attacker-influenceable
string can suppress a security incident. `AGENTS.md` classifies rule bundles as
untrusted input, and here the bundle is *trusting* untrusted input: a camera
whose name or status text a local attacker can influence decides whether a
camera-offline incident exists. It is `enabled: false` today, which is why it is
a finding and not an incident.

The correct shape is a canonical boolean the adapter derives — e.g.
`CameraState_MAINTENANCE` already exists in `camera.proto:30` — matched with
`field.path: canonical.cameraState`. This is the M4 adapter work item that
replaces it, and it is a good test case for the second adapter's conformance
suite: *"an adapter must not require a rule to read `vendor.*` to express a
source-native state."*

---

## 4. M1 → M2 → M3 → M4: how the adapter surface grows

### 4.1 The compatibility discipline, which does not change

- Change `contracts/` first. `make generate` runs `protoc` then
  `tools/contractcheck -mode check -baseline contracts/compat/baseline.json`.
- `contractcheck` decides. CI's `contract` job runs `make contract-check` and
  then `make generate` and fails if `git diff -- internal/contracts` is
  non-empty (`.github/workflows/ci.yml:36-46`) — so hand-editing
  `internal/contracts/securityv1/*.pb.go` fails the build.
- `make contract-baseline` is the only way to move the baseline, and it is a
  new-contract-version act, not a convenience.
- **JSON Schema has no such gate for the AI profiles.** `make contract-sync`
  (Makefile:86-89) `cmp`s only `contracts/json-schema/rule-bundle.schema.json`
  against `internal/rules/schema/rule-bundle.schema.json`. The three
  `contracts/json-schema/ai-observations/*.schema.json` files have **no sync
  check and no CI job**. That is a gap the AI track needs and
  `ai-and-data.md` §3.7 records it independently; the integration track needs it
  too because a second adapter that adds a canonical event type changes the rule
  schema's enum.
- Migrations: forward-only, expand and contract separately, never an applied file
  edited (`AGENTS.md`; `Makefile:migration-phases` asserts every filename carries
  a phase).

### 4.2 The surface, milestone by milestone

| Capability | M1 (now → 2027-03) | M2 (2027 Q2–Q3) | M3 (2027 Q4 → 2028 Q1) | M4 (2028 Q2–Q4) |
|---|---|---|---|---|
| **Route coverage** | 8 assumed routes, all `Unverified:true`. 6 paginated feeds polled. 2 non-paginated routes unpolled. `securityctl adapter probe` — **referenced at `routes.go:16` and does not exist**; `newRootCommand` (`cmd/securityctl/main.go:53-69`) registers no `adapter` command | Probe becomes a real command; each confirmed route loses its `Unverified` flag; a test asserts the unverified list shrinks | Same; plus the `Servlets` route extended to cover maintenance state | 8 vendors × their event surfaces. `Unverified` per route per vendor, which is where the flag earns its keep |
| **Evidence retrieval** | **Does not exist.** `routes.go:67-69` names `ImagePath` and `VideoRecordingStartTime` as assumed fields; nothing reads them. `fetch_evidence` is a legal dispatch type (`rules/bundle.go:307`) and a legal queue (`jobs.QueueEvidenceFetch`) and no producer or worker exists | **The M2 blocker.** Retrieval design in `ai-and-data.md` §5; adapter gains `FetchSnapshot`, `FetchClip`, `ProbeClipRange`. Clips need a start *and* end, and a VMS that offers only "clip covering the alarm" makes the end time a vendor concept that must be normalised | Evidence-completeness measurement; recording-extension as a fetched artifact | Per-vendor clip semantics, and ONVIF `Recording` search |
| **Actions** | None. `Action` proto and `ActionStatus` exist; no dispatcher | None | First controlled set: bookmark, snapshot, recording extension, incident create/assign, notification. Idempotent, authorised, logged before and after, with a `kill -9` test at the action stage | PTZ presets, in the same shape. Every action idempotency key derived from `(incident, action_type, target, window)` |
| **Streaming** | None. Poll only | None | None | Optional. `Query.Cursor` exists (`routes.go:156`) and nothing uses it; `Query.Limit` is hard-coded to 200 (`adapter.go:167`) |
| **Clock** | `/api/v1/System/Time` declared, never called (§1.6a) | Clock sampling implemented; `device_clocks` populated from real sources; `DriftBand` becomes real | — | Per-vendor clock semantics are the hard part, not the protocol |
| **Device/zone resolution** | `sourceDeviceRef` exists; the resolver does not | Resolver, so `zone.pathPrefix` rules work on a real site | — | Per-vendor topology mapping (`ResourceGroups`, ONVIF `tt:Network`/entity trees) |

### 4.3 The M2 evidence-retrieval blocker, stated as a dependency

M2's entire thesis — "the operator stops searching cameras by hand" — depends on
an adapter capability that does not exist. `Adapter` is a *poller of event
feeds*. It has no method that returns bytes. `evidence.Store`
(`evidence/store.go:46-72`) exists and is carefully built — relative
forward-slashed refs, symlink re-resolution, hash-on-read, `DefaultMaxBytes` of
512 MiB, and the package doc insists "this package is the only thing that touches
the evidence root" — but nothing in the tree ever calls `Store.Put` from a
source.

This is a first-class M2 backlog item and its blocker is stated nowhere. It
should be in `product-and-backlog.md` §2.2 as a named dependency of the evidence
engine, because the evidence engine is the milestone's first deliverable
(`master-roadmap.md` §M2 scope 1) and cannot start.

---

## 5. The ONVIF programme (M4)

### 5.1 Scope

`master-roadmap.md` §M4 item 1: Profile T and Profile M — discovery, events,
analytics metadata (people, vehicles, licence plates, object counters), PTZ, and
streaming capability discovery. "Profile M is where the free signal density is."

That last sentence is the strategic content of the programme and it should drive
sequencing: **Profile T alone is an event feed; Profile M is a metadata feed that
carries the analytics the VLM profiles need.** A camera doing line-crossing that
publishes `tt:Analytics` frames is producing, for free and deterministically, the
people count, the vehicle count and the licence plate that `restricted-person` and
`vehicle-at-gate` would otherwise have to infer from pixels.

### 5.2 Work breakdown (ESTIMATE on effort)

| # | Work item | Depends on | Effort (ESTIMATE, engineer-weeks) |
|---|---|---|---|
| W1 | WS-Discovery (`ONVIF Device Discovery` multicast + unicast fallback), device description XML → `Device` + `Camera` | — | 3 |
| W2 | Device service enumeration; per-service `GetXxx` capability probing | W1 | 2 |
| W3 | `CreateSubscription` / `PullPoint` event handling → canonical `analytics` and alarm transitions | W1 | 4 |
| W4 | `tt:Analytics` metadata frame parsing → `people`, `vehicles`, `licence plates`, object counters | W1 | 5 |
| W5 | PTZ: `ContinuousMove` / `AbsoluteMove` / `GotoPreset` / `Stop`, with preset existence check | W2 | 3 |
| W6 | `GetStreamUri` + `GetServiceCapabilities` → streaming capability discovery, and the RTSP back-channel question | W2 | 3 |
| W7 | Device **capability declaration** persistence and diffing across polls (§5.4) | W1, W2 | 2 |
| W8 | Interop lab: a device matrix, and the conformance report | all | 4 |
| | **Total** | | **26** |

W8 is not optional overhead. `financial-plan.md` §5.4 already classifies
"ONVIF device interop testing against other vendors' hardware" as a
**contractor** engagement because "access to hardware is the thing being bought".
That classification is correct and should not change.

### 5.3 The hard parts, honestly

**(a) Partial and non-conformant implementations are the normal case.** A large
share of deployed ONVIF cameras implement a subset, misreport their own
capabilities, or implement the specification's letter and not its intent. The
platform cannot assume a capability because a spec-conformant device should have
it. It must *ask*, and record the answer.

**(b) Conformance class differs per device and per service.** There is no single
"this device is Profile T" answer. A device may be Profile T for events and
Profile S for media, and may support `Analytics` on one channel and not another.
A boolean per device is the wrong shape. See §5.4.

**(c) Profile S/T/M confusion is endemic in the field.** Cameras are labelled
"Profile T" and ship no analytics; "Profile M" devices frequently expose
analytics only as a *licence-gated* add-on; "Profile S" devices are media-only
and will never emit an event. Mislabeling is common enough that a vendor
marketing page must never be an input to a capability declaration. **Every
capability must be observed from the device.**

**(d) WS-Discovery is unreliable in exactly the environments we sell into.**
Multicast is commonly blocked on enterprise VLANs; sites that isolate cameras on
a dedicated network will not answer unicast either. Discovery therefore needs
seeded configuration as a first-class path, not a fallback.

**(e) Event ordering and replay.** ONVIF subscriptions are pull-based and
lossy. A dropped `PullMessages` response is a gap, and the platform's answer has
to be the same as the inbox's: a visible gap, not a silent one. A device
capability of "supports `AbsoluteTimeFilter`" determines whether a late
subscriber can backfill at all, and that is a capability declaration item.

### 5.4 Device capability declaration — the design

The principle: **the platform declares what a device actually supports, because
it probed it, and stores that declaration as a dated, diffable record.** A
capability is a `(device, capability, value, observed_at, source)` row. It is not
a boolean column on `devices`, because (i) capabilities are per-service and
per-channel, (ii) they change, and (iii) a boolean cannot be dated.

Proposed shape (new table, forward-only migration, `migrations/`):

```sql
CREATE TABLE device_capabilities (
    tenant_id            TEXT NOT NULL,
    device_id            TEXT NOT NULL,
    capability           TEXT NOT NULL,   -- e.g. 'event.analytics.people_count'
    value                JSONB,           -- a value, not a bool: 'ok'|count|token
    conformance_class    TEXT,            -- the service the answer came from
    observed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    source               TEXT NOT NULL,   -- 'probe' | 'config' | 'operator'
    PRIMARY KEY (tenant_id, device_id, capability)
);
```

Design rules, each of which exists because of the failure it prevents:

1. **`source` is mandatory and may be `operator`.** An operator asserting a
   capability is legitimate ("this camera is behind the desk and its events
   arrive via the panel") and must be distinguishable from a probe result, the
   same way `graph.Method` distinguishes `observed` from `asserted`
   (`graph.go:58-62`).
2. **A capability row is replaced, never silently.** On a re-probe that
   contradicts a prior value, write a new row into a
   `device_capability_history` (or reuse the `revises` relation the graph
   already has) and set `source='probe'`. Capability *drift* is operationally
   meaningful: a camera that stops publishing analytics is a camera-health
   finding (`master-roadmap.md` §M6 item 7), not a silent downgrade.
3. **A missing capability row is a first-class answer.** It is not `false`. It
   means "never probed", which behaves like the AI plane's absence case
   (`observations.Degradation` — no observation vs degraded vs
   insufficient). Reuse that three-way shape rather than inventing a fourth.
4. **The capability set is a closed vocabulary**, checked like
   `rules.KnownEventTypes`: a probe that returns an unknown key is a source
   observation, not a new capability. Vocabulary lives in `contracts/`.
5. **No capability declaration ever grants an action.** PTZ is permitted because
   policy says so, not because the device answers. A device that claims PTZ and
   the policy that permits PTZ are two independent facts, and conflating them is
   how an "integrations" feature becomes a safety incident.

### 5.5 What the platform refuses to do

- **No default camera behaviour on assumption.** If a capability is unprobed, the
  platform behaves as it behaves for a camera it has never heard of: the event
  arrives, is attributed, and any rule that needs an undeclared capability does
  not match.
- **No silent feature enablement from a vendor's marketing profile.**
- **No per-vendor code path above the adapter.** Every one of these is a row in a
  table, not an `if vendor ==`.

---

## 6. The second VMS adapter

### 6.1 Vendor selection criteria

Selection is by **customer pipeline, not preference** (`master-roadmap.md` §M4
item 2). `gtm-and-sales.md` §2.3 and §7 own the pipeline; the criteria below are
how the engineering side scores a candidate once the commercial side has named
one. Candidates named in the roadmap: Genetec, FLIR, Avigilon, the Axis
ecosystem.

| # | Criterion | What "good" looks like | How it is scored before we start |
|---|---|---|---|
| C1 | **Pipeline, not preference** | A named design partner or paid pilot is blocked without this adapter | Count of committed sites naming it. If zero, the adapter is not M4's second adapter, regardless of how good the API is |
| C2 | **Documented, reachable API** | A published REST/SDK surface, not a reverse-engineered one | Counting: a documented API is a two-week integration; an undocumented one is a six-week one and carries a legal question |
| C3 | **Read-only tolerance** | A read-only integration account is *supported and normal*, not a licence-upgrade conversation | Ask the vendor. A vendor that requires write access for a read integration is disqualified: `strategy-and-north-star.md` §3's anti-segments forbid a dependency on a physical-action control path we do not own |
| C4 | **Event model that maps without loss** | Vendor events cover ≥ 80% of our canonical vocabulary, or the gap is explainable | Map the vendor's event list onto the 15 canonical names before writing code. A vendor whose model is 40% analytics and 60% proprietary is a permanent vocabulary-debt cost |
| C5 | **Evidence retrieval available** | Snapshots and clips are reachable through a documented interface | If not, M2's evidence intelligence cannot run on that vendor's sites, and M4's adapter is a partial product |
| C6 | **Local-first fit** | Works with the VMS on the customer's LAN; no cloud dependency | A cloud-only VMS in a security product is a deployment objection, not an integration detail |
| C7 | **Device heterogeneity it represents** | Broad enough that the adapter generalises | The abstraction is proven by breadth of devices as much as by breadth of vendors. A vendor with one appliance model proves nothing |
| C8 | **What it teaches the abstraction** | Written down *before* the build | The real deliverable of the second adapter is a list of seam changes. A second adapter that requires zero seam changes was probably built to be easy |

### 6.2 The 8–12 week build template

| Week | Work | Gate at the end of the week |
|---|---|---|
| 1 | Vendor interface discovery. Map the vendor's event list onto the 15 canonical names. Write the `Routes` var with every entry `Unverified: true` and a `Notes` line per entry | The map has no unclassified vendor event, or the unclassified ones are listed with a reason |
| 2 | `Transport` implementation: auth, pagination, cursor, retry, rate limiting, 401/403/404 typing | `Adapter.Poll` runs against a recording proxy and produces records |
| 3 | `toRecord` / identity / type / timestamp extraction, including the naive-timezone rule and the device-ref rule | Every field the M1 tests assert, re-asserted for this vendor |
| 4 | Device-reference resolution **and the zone resolver** (M2's missing piece), so `zone.pathPrefix` rules work | A fixture set where a device is resolved to a registered `device_id` and a zone |
| 5 | Evidence retrieval: snapshot + clip, with `OpenDuration`/end-time normalisation, size caps, and the evidence store's path rules wired | A clip lands in `evidence.Store` with a correct `storage_ref` and `sha256`, and a `../` reference is refused |
| 6 | Actions: bookmark, snapshot, PTZ preset — each idempotent, authorised, logged before and after | The `kill -9` fault-injection test at the action stage, per `quality-engineering.md` §5 |
| 7 | **Non-adapter diff run, pass 1** (§6.3) | The first non-empty diff, which is the point of doing it this early |
| 8 | Clock sampling and drift; `device_clocks` populated for real | `DriftBand` is computed from a real measurement, not a fixture |
| 9 | Capability declaration persistence and re-probe diffing | A camera whose analytics capability disappears is detected and reported |
| 10 | Conformance suite for this adapter; fixture set; fault injection at every stage; path-traversal negatives | The bring-up checklist (§11) complete except for the parity report |
| 11 | Replay parity: the same scenarios live and from fixtures, canonical events compared | The parity report exists, with a byte-level diff or a named exception |
| 12 | Documentation: route list with `Unverified` flags cleared where probed, known limitations, operator runbook | A reviewer can run the adapter from the docs alone |

**The template's structural claim: 8–12 weeks assumes the `Transport` seam, the
`RawRecord` shape, the inbox, the normalizer, the graph and the rule engine do
not change.** That is not optimism. It is the thing §6.3 exists to test, and
week 7 is placed early on purpose.

### 6.3 The non-adapter diff test

**The name: `make non-adapter-diff <vendorA> <vendorB>`.**

The claim it tests, verbatim from `master-roadmap.md` §M4: "A second full VMS
adapter works with zero changes outside the adapter."

**What "behavioural" means.** The test must compare *behaviour*, not text. A
comment, a docstring or an import reordering is not a behavioural change. A
changed default, a changed ordering, a changed error classification, a changed
metric emission or a changed field name **is**. So the diff is over a
**canonical, normalised rendering**, not over raw `git diff`.

**The procedure.**

```
1. Branch A: the tag/commit at which adapter A (Milestone) went to production.
   Branch B: the tag/commit at which adapter B (vendor N) is enabled in
             integration mode at its first site.

2. Define the non-adapter tree, once, in a file `nonadapter.txt` at the repo root:
     cmd/securityctl/**
     internal/            minus the two adapter packages
     contracts/
     migrations/
     rules/
     deploy/ai-runtime/
     tools/
   The adapter packages themselves are named in `adapters.txt` and excluded. The
   exclusion list is explicit and reviewed, not a glob, because a glob that
   accidentally includes an adapter would make the test vacuous.

3. For the SAME fixture set — the vendor's scenarios, translated into the
   canonical vocabulary, plus the shared scenarios both adapters must satisfy —
   capture, on each branch:
     a. the canonical events produced (id, event_type, observed_at,
        normalized_at, device_id, zone_id, correlation_id, integrity_hash)
     b. the verification results and reason codes
     c. the incidents raised, their severities and their states
     d. the audit ledger row count and the checkpoint seal
     e. every metric name emitted (not its value)

4. Compare (a)-(d) byte-wise after canonical serialisation. Compare (e) as a set
   of names.

5. Emit a report: `artifacts/non-adapter-diff/<A>-vs-<B>/report.md` plus the
   raw diffs.
```

**Two passes, and both are acceptable outcomes:**

- **Zero diff.** The abstraction held. The M4 gate's condition is met and the
  report is the evidence.
- **Non-zero diff, with every difference named and reviewed.** This is not a
  failure of the test; it is the test doing its job. A diff that is one
  documented default changed in `internal/clock` because vendor N's clock
  semantics are different is a *finding about the clock abstraction*, and the
  correct response is an ADR plus a fix in the core, then a re-run. A diff that
  is a vendor branch in `internal/incidents` is a **failure**, full stop, and it
  is the one outcome that means the abstraction did not hold.

**How it is run.** Locally, on both branches, in CI as a nightly job (not a
per-PR job — it needs both branches to exist), and **manually, once, immediately
before the M4 gate is signed**, with the report attached to the gate. The
manual run is the one that counts. A nightly green is evidence; a signed report
is a gate.

**How it is reviewed.** The report is reviewed by a human who did not build
either adapter, against a fixed question list: *is every difference a change of
default, a change of ordering, a change of classification, or a change of
surface?* The first three are core bugs and get an ADR. The fourth is a contract
change and goes through `contracts/` and `contractcheck`. Anything that is not
in one of those four categories did not get reviewed properly.

**What the test cannot prove**, and must say in its own report: it compares two
adapters on two fixture sets. It does not prove the *set* of adapters is
closed. That is what the Adapter SDK's conformance suite is for (§7), and M8's
exit gate ("a third-party adapter, built by someone outside the company") is the
only thing that actually proves it.

---

## 7. The Adapter SDK (M4)

### 7.1 The requirement, quoted from the roadmap

`master-roadmap.md` §M4 item 3: "A third party must be able to write a working
adapter without reading our source." The M4 gate requires that the certification
suite "has been run by someone outside the company". `gtm-and-sales.md` §7.4
argues the SDK must exist *before* the OEM conversation for exactly this reason,
and `financial-plan.md` §5.3's recommendation (cut M4 to the second adapter and
the SDK, then use the partner's engineers) is only possible because the SDK
exists. This is the highest-leverage item in M4 and it should be built as a
library, not as documentation.

### 7.2 Package layout

```
sdk/
  go.mod                        # module: aicaptain.dev/sdk  (a separate module)
  adapter/
    adapter.go       # Adapter, RawRecord-equivalent, Poll, Parse — the M1 seam,
                     #   generalised. Today this is internal/milestone/adapter.go
                     #   generalised; it moves, it is not reimplemented.
    route.go         # Route, Query, Response, Transport
    errors.go        # UnauthenticatedError, NotFoundError, MalformedError, IsNotFound
    poll.go          # the poll ordering, per-feed isolation, 404-is-normal rules
  record/
    record.go        # the vendor-neutral output type
    vocabulary.go    # the canonical event-type vocabulary, GENERATED
    time.go          # the timestamp layout list and the naive-timezone rule
    deviceref.go     # the source-device-reference resolution ladder
  fixture/
    scenario.go      # fixture.Scenario, FormatVersion, Validate, ContentHash
    sanitize.go      # capture-time redaction
    generator.go     # the capture tool: point it at a vendor, get a fixture set
  conformance/
    suite.go         # the tests every adapter must pass
    cases/           # the shared scenarios: duplicate, out-of-order, 404,
                     #   401, malformed item, unknown type, naive time,
                     #   unparseable time, page-exhaustion, empty feed
    report.go        # the certification report format
  version/
    version.go       # API version negotiation, deprecation windows
  doc.go             # godoc that is the documentation, not a comment on it
```

**The single most important structural decision: the vocabulary is generated,
not hand-written.** Today the canonical event set exists in two hand-maintained
places — `rules.KnownEventTypes` (`rules/bundle.go:285-292`) and the JSON Schema
enum (`rule-bundle.schema.json:117-125`) — and they agree only by discipline. A
third-party adapter that adds a mapping needs a *typed* statement of what it
maps onto, and that statement must be the same one the rule compiler checks. One
generated source, one embed, `make contract-sync` extended to cover it.

### 7.3 The public interface

```go
// Package adapter is the seam a source integration implements.
package adapter

// Transport supplies raw responses. It is deliberately at the byte level.
type Transport interface {
    Get(ctx context.Context, route Route, query Query) (*Response, error)
    Close() error
}

// Source is what an adapter implementation provides: the vendor's knowledge.
type Source interface {
    // Routes is the complete assumed API surface, every entry marked
    // Unverified until a probe confirms it. Unverified is a field, not a
    // comment, so a probe and a test can both read it.
    Routes() []Route

    // Transport returns the byte source. A live server and a fixture directory
    // differ only here, which is what makes integration and replay comparable
    // as a structural property rather than a thing kept in sync by hand.
    Transport(cfg Config) (Transport, error)

    // NormalizeType maps a vendor event type onto the canonical vocabulary.
    // Returning the input unchanged is a bug: the vocabulary is closed and an
    // unrecognised value is a source observation, not a new type.
    NormalizeType(vendorType string, item map[string]any) string

    // DeviceRef resolves the source's device identifier. Returning "" is
    // correct and expected: an unresolvable reference leaves the event
    // unattributable rather than attributed to a guess.
    DeviceRef(item map[string]any) string

    // TimeLayouts is the ordered list of timestamp formats this source uses.
    // A naive timestamp yields an empty source timezone; the adapter never
    // assumes one.
    TimeLayouts() []string

    // Capabilities declares what this source can do, probed rather than
    // assumed. M4; before that, the interface is smaller.
    // Capabilities(ctx context.Context, t Transport) (Capabilities, error)
}

// Adapter is the shared implementation. An adapter author implements Source and
// gets Poll, Parse, ordering, per-feed isolation, the 404-is-normal rule and
// the malformed-item recovery for free.
func New(src Source, cfg Config) *Adapter
func (a *Adapter) Poll(ctx context.Context, since *time.Time) ([]Record, error)
```

**What the SDK deliberately does not include**, because each is a place an
adapter author would be tempted to reach around the platform and break an
invariant:

- **No database access.** An adapter does not write. The inbox writer is
  platform-side, always.
- **No tenant field.** The tenant comes from the runtime session. An adapter that
  could set it would be a tenant read from a file (`adapter.go:23-27`).
- **No action dispatch.** An adapter *requests* an action; the platform's policy
  decides, the outbox delivers, the audit ledger records. An adapter that can
  book-mark without authorisation is a remote control, not an integration.
- **No AI calls.** An adapter enqueues `evidence_fetch`; it never enqueues `vlm`
  directly (`jobs.QueueVLM` exists; nothing consumes it today — see
  `ai-and-data.md` §1.4). The profile is chosen by a rule, the model by policy,
  the observation by the platform.
- **No shell, no SQL, no network egress, no dynamic code, no unbounded regex.**
  `AGENTS.md` states this for rule bundles; the same posture applies to a
  third-party adapter binary, and `trust-and-security-program.md` §3.7 owns the
  untrusted-input policy for all three kinds.

### 7.4 The fixture framework and the source simulator

The fixture framework exists (`internal/fixture`) and is good: verbatim
envelopes, sanitization at capture time, relative timing, a content hash, a
manifest with a `Covers []string` per scenario ("a scenario whose purpose is
unstated is a scenario nobody will notice has stopped testing anything"), and
`FormatVersion` that a reader refuses if it does not recognise. **Two changes are
required before it can be an SDK:**

1. Fix the epoch defect in §1.6d. The `OffsetMS` machinery is stored, hashed and
   ignored. Until it is applied, "relative timing" is a promise in a comment.
2. Extract the capture tool. `tools/fixturegen/main.go` exists; promoting it to
   `sdk/fixture/generator` is what makes capture something a third party can run.

**The source simulator** is the piece that makes the SDK testable without
hardware: a `Transport` that serves a fixture set, which is
`internal/milestone/replay.go` generalised. It must additionally support:

- **Fault injection**: per-route status codes, malformed bodies, a body that is
  valid JSON but not the expected shape, a truncated page, a transport that
  returns `[]` forever, a transport that hangs until a context deadline.
  This is what makes the conformance suite able to run the `kill -9` and
  partial-failure cases without a real device.
- **Determinism**: byte-identical responses for identical requests, so a
  certification report is reproducible.

### 7.5 The conformance and certification suite

**Conformance (self-run, mandatory, in the adapter's own CI).** Every adapter
must pass:

| Suite | Cases | What a failure means |
|---|---|---|
| `SharedScenarios` | duplicate, out-of-order, negative offset, 404-not-deployed, 401, 500, malformed item, unknown type, naive timestamp, unparseable timestamp, page exhaustion, empty feed | The adapter does not behave like the seam |
| `VocabularyClosure` | every event type the adapter can emit is in the canonical set or is explicitly `unknown.*` | Vocabulary debt admitted knowingly |
| `Determinism` | same fixture, two runs, byte-identical records and identical `integrity_hash` | Replay parity is impossible |
| `TimeProvenance` | naive time yields empty `SourceTimezone`; unparseable time yields zero observed and a recorded fallback | A DST-spanning window cannot be reasoned about |
| `DeviceRefLadder` | dedicated field wins over bracketed convention wins over bare name; bare name returns "" | Device rows per human label |
| `NoVendorTextInRules` | no shipped rule for this vendor reads `vendor.*` to express a source-native state | The `vendor.Message` finding, generalised |
| `Ordering` | output ordered by `(source_event_id, observed_at)`, independent of arrival | Integration and replay disagree |
| `FailureIsolation` | one failing feed does not abort the poll and produces exactly one `adapter_feed_error` | Partial outage becomes total outage |

**Certification (third-party run, M4 gate).** The third party runs the same
suite against the built adapter, against a device or the simulator, and returns
a **signed report** in a published format. The report includes: the SDK API
version, the adapter's declared source version, the device/fixture set, the
result of every suite, and any **waivers** — named, justified, and dated.
Waivers are allowed (some vendors cannot satisfy a case); a waiver that is not
named is a failure.

**Publishing the conformance suite as an artefact** is an M4 gate line
(`master-roadmap.md` §M4). It is also the cheapest possible way to make the
"third party can build an adapter" claim falsifiable: a published suite is a
contract a stranger can run.

### 7.6 Versioning and deprecation

- **Two versions, not one.** An SDK **API version** (the Go interface) and a
  **contract version** (`security.v1`, owned by `contracts/compat/baseline.json`
  and enforced by `contractcheck`). They move independently and an adapter
  declares both. The Python runtime already models this correctly: `API_VERSION`
  in `deploy/ai-runtime/runtime.py:37`, refused on mismatch with a 400 and a
  named reason ("a silent field rename is how a version skew becomes a wrong
  observation"). The SDK should use the same pattern.
- **Deprecation window: two milestone releases minimum, published in a
  `deprecated-versions.md`, with the adapter still building and still passing its
  suite.** A site upgrading from M4 to M5 must not be told its adapter no longer
  compiles.
- **Adding a capability to `Source` is a major version.** Adding a `Route` field
  with a default is a minor version. Anything that changes what an existing
  adapter *emits* is a major version, because the canonical event changed.
- **Removing a canonical event type is forbidden** while any activated rule bundle
  on any site names it. `contractcheck` refuses the removal; that is what the
  baseline is for.

### 7.7 A sample adapter skeleton

```go
// Package acmeadapter is a third-party integration with the ACME VMS.
//
// It is the smallest thing that satisfies sdk/adapter.Source. Everything
// else — poll ordering, per-feed isolation, the 404-is-normal rule, malformed
// item recovery, timezone provenance — comes from the SDK.
package acmeadapter

import (
	"context"
	"time"

	"aicaptain.dev/sdk/adapter"
	"aicaptain.dev/sdk/record"
)

// New returns an adapter over the SDK's Poll implementation.
func New(cfg Config) *adapter.Adapter {
	return adapter.New(Source{Routes: routes, cfg: cfg}, adapter.Config{
		SiteID:        cfg.SiteID,
		IntegrationID: cfg.IntegrationID,
	})
}

// Source carries the vendor's knowledge and nothing else.
type Source struct {
	routes []adapter.Route
	cfg    Config
}

// Routes is the assumed ACME API surface.
//
// Unverified is set on every entry until a probe confirms it against a real
// server. It is a field rather than a comment so that `acmeadapter probe` can
// print exactly which parts are still a hypothesis, and so a test can assert
// the list shrinks over time rather than rotting silently.
func (s Source) Routes() []adapter.Route {
	return s.routes
}

var routes = []adapter.Route{
	{
		Method: "GET", Path: "/rest/events", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "The event feed. Field names assumed: eventId, eventType, " +
			"deviceId, timeGenerated, severity.",
	},
	{
		Method: "GET", Path: "/rest/events/{eventId}", Expect: "object",
		Unverified: true,
		Notes: "Full event detail including the analytics payload.",
	},
	{
		Method: "GET", Path: "/rest/devices", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Device list. Camera identity only; no operator credentials are read.",
	},
	{
		Method: "GET", Path: "/rest/system/clock", Expect: "object",
		Unverified: true,
		Notes: "Server time, read to measure drift between the VMS and this host.",
	},
}

// NormalizeType maps ACME's vocabulary onto the canonical set.
//
// The mapping lives here and nowhere else. A rule bundle must be portable
// between sites and between replay and integration mode, so it refers to the
// normalized names, and the vendor's spelling is an adapter detail.
//
// An unrecognised value is returned as "unknown.<lowercased>". It is carried,
// never dropped: a vendor adding an event type is information, and the ingest
// backpressure metric is where it should show up.
func (Source) NormalizeType(vendorType string, item map[string]any) string {
	switch normalizeKey(vendorType) {
	case "motion", "videomotion":
		return "motion"
	case "analytics", "linecrossing", "loitering", "objectdetection":
		return "analytics"
	case "alarm", "alarmcreated":
		return "alarm.created"
	case "alarmacknowledged":
		return "alarm.acknowledged"
	case "alarmclosed", "alarmresolved":
		return "alarm.closed"
	case "cameraoffline", "devicedisconnected":
		return "device.camera_offline"
	case "cameraonline", "deviceconnected":
		return "device.camera_online"
	case "devicestate":
		return "device_state"
	case "recordingstarted":
		return "recording.started"
	case "recordingstopped":
		return "recording.stopped"
	case "bookmark":
		return "bookmark"
	case "metadata":
		return "metadata"
	case "accessgranted":
		return "access.granted"
	case "accessdenied":
		return "access.denied"
	case "gateevent", "barrierevent":
		return "gate"
	}
	return "unknown." + normalizeKey(vendorType)
}

// DeviceRef resolves ACME's device identifier.
//
// The ladder is: a dedicated field, then a bracketed convention, then nothing.
// A bare human-readable name is not a device reference — treating a label as an
// id creates a device row per camera label and attaches events to the wrong
// thing when the label is reworded.
func (Source) DeviceRef(item map[string]any) string {
	if d := record.StringField(item, "deviceId", "DeviceId", "cameraId"); d != "" {
		return d
	}
	if src := record.StringField(item, "source", "Source"); src != "" {
		if m := record.BracketedID.FindStringSubmatch(src); len(m) == 2 {
			return m[1]
		}
	}
	return ""
}

// TimeLayouts is ACME's timestamp formats, most specific first. ACME sends
// naive local timestamps on older firmware and offset-bearing ones on newer;
// the naive case is why SourceTimezone comes back empty rather than assumed.
func (Source) TimeLayouts() []string {
	return []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999",
	}
}

// Transport returns the byte source. A live server and a fixture directory
// differ only here, which is the whole reason integration mode and replay mode
// are comparable.
func (s Source) Transport(cfg adapter.Config) (adapter.Transport, error) {
	if cfg.FixtureDir != "" {
		return adapter.NewFixtureTransport(cfg.FixtureDir), nil
	}
	return newHTTPTransport(s.cfg)
}
```

What this skeleton deliberately does **not** contain, and the reviewer should
check for each: a database import, a `tenant` field, a `http.Client` to anything
other than the configured vendor base URL, an action dispatcher, a model client,
a `regexp.MustCompile` with an unbounded pattern, and any branch on a vendor
name outside `normalizeKey` and `Transport`.

---

## 8. Access control and sensors (M4)

### 8.1 The canonical vocabulary that must be added

None of these exist today. `rules.KnownEventTypes` has `access.granted` and
`access.denied` and nothing else; the proto layer has `EntityKind_BADGE` and
`EntityKind_DOOR` and no access vocabulary at all.

| Canonical name | Meaning | Absorbs |
|---|---|---|
| `access.granted` | A credential was presented and accepted | Reader grant, badge read, PIN accepted, mobile credential |
| `access.denied` | A credential was presented and refused | Bad credential, expired badge, out-of-schedule, anti-passback |
| `access.forced` | A door reached open without a valid grant — a REX, a propped door, a tailgate at a controlled door | Door contact + REX + a second reader's silence |
| `access.held_open` | A door was granted and remained open past a site-declared dwell | Door-open duration beyond policy |
| `access.tailgated` | A grant for one followed immediately by an unauthorised passage | Present in the `restricted-person` schema's `accessState` enum (`["unknown","authorized","unauthorized","forced","tailgated"]`) but **with no canonical event to carry it** |

And the entities: a **credential** is a thing that can be presented (a badge, a
PIN, a mobile token, a plate); an **identity reference** is the stable handle to
a person or vehicle that the customer's access system assigns, which we store and
never resolve to a person ourselves. `entity.proto`'s comment already draws this
line — "An entity is a concrete thing a device reported" — and it is the same
line `graph.go:16-22` draws by refusing a person model. The access vocabulary
must respect it.

`door.proto` **does not exist** and would be the M4 addition. `device.proto`'s
`DeviceKind` already has `DEVICE_KIND_DOOR` and `DEVICE_KIND_ACCESS_READER`, so
the device side is ready and only the event vocabulary is missing.

### 8.2 Why forced-open and held-open correlation is the highest-value correlation in the product

Four reasons, and the first is a commercial one.

1. **It is the one access event that is unambiguous and time-critical.** A granted
   badge is routine. A denied badge is often a misread card at 6am. A door
   forced, or held open past policy, is *almost never* routine, and it is
   reported by a contact on a door the customer already owns — no analytics, no
   tuning, no licence. `master-roadmap.md` §M4 item 4 calls them "among the
   highest-value correlations in the whole product". The honest reading: they are
   the *cheapest* high-value correlations in the whole product, and the roadmap's
   placement of access control before sensors (§M4's reduced scope) is correct for
   exactly this reason.
2. **It is the correlation that decides the trust question at a pilot.** A buyer
   asks "what happens when the AI is down?" (`strategy-and-north-star.md` §4). The
   strongest possible answer is: *"A door forced at 02:14 on a badge reader is
   Tier A — the reader's own state, reported by the customer's system. It needs no
   model. The model's contribution is corroborating who it was, and that
   corroboration is optional and labelled."* The bundle already contains the rule
   shape: `core.door-forced-after-hours` at `rules/builtin/bundle.yaml:117-120`,
   described as "A door forced or left open outside business hours. **Needs no
   AI.**" The comment two rules above it is the product thesis in one line: "so a
   site can run the whole operator workflow with the AI plane completely absent —
   which is a supported production configuration, not a degraded mode."
3. **It is where access, video and the graph genuinely meet.** A forced door is a
   Tier A event from a reader; the valuable part is correlating it with the
   nearest cameras' `analytics` metadata inside a `normalized_at` window
   (`semantic.go:302-313` enforces that only `normalized_at` may be read), with
   the badge's `Entity` for the same window, and with the zone's exit/ingress
   structure. That is M2's evidence intelligence exercised on the one class
   where the operator will always look.
4. **It is the correlation with the clearest escalation semantics.** Forced door
   → notify. Held open → escalate. Neither is a page, and both are auditable
   with a name attached. That is the `master-roadmap.md` §M3 bounded auto-
   resolution's first candidate class, and the one where "auto-resolve after N
   seconds if no motion" is *correct* rather than reckless.

**The design risk, named.** Forced and held-open are *derived* facts, not source
facts. A reader reports "door opened" and "door closed"; "forced" is an inference
from a REX contact, and "held open" is an inference from a duration. The
distinction matters enormously: a derived Tier B fact must be reproducible from
stored facts (invariant 2) and must be attributable to the derivation. So the
adapter emits `access.granted`/`access.denied`/a door-open/door-close pair as
Tier A, and the **rule** derives forced and held-open as Tier B with a named rule
id. If the adapter synthesises `access.forced` from a heuristic, the derivation
is unauditable and a replay cannot reproduce it. This is a design decision that
should be an ADR before any code (`decisions-and-process.md` §2.2/§2.3).

### 8.3 The sensor taxonomy

| Family | What "signal" means in a canonical event for this family | Canonical name (proposed) | Tier | Notable hard case |
|---|---|---|---|---|
| **Intrusion** (motion, glass-break, door contact, PIR) | A zone transition. The signal is a *zone state change*, not a device state | `sensor.motion`, `sensor.contact_open`, `sensor.contact_closed` | A | Multi-zone intrusion panels report a whole panel's state; per-zone granularity varies |
| **Perimeter** (fence, gate contact, REX, beam, radar) | A boundary transition, and **the association of that transition to a graph zone is the product** | `sensor.perimeter_breach`, `sensor.perimeter_restored` | A | Perimeter is the canonical case for a `ZONE` node type the adapter must create, and it is where `zone.pathPrefix` rules earn their keep |
| **Environmental** (temperature, humidity, water, smoke, CO) | A **threshold crossing**, and the crossing's direction. "Temperature 21.4°C" is not a signal; "temperature rose above 30°C" is | `sensor.threshold_crossed` + `canonical.value`, `canonical.threshold`, `canonical.direction` | A | Many environmental sensors are not event sources at all — they are polled. A polled reading is an `event_type` of `device_state` with a payload, which is a real modelling compromise to decide consciously |
| **Intercom** (call, door release, request for entry) | A **request**, and the grant or refusal of that request | `access.intercom_request`, `access.intercom_granted`, `access.intercom_denied` | A | Intercom is a *conversational* medium: the "signal" is often a video-and-audio event, and the canonical model has no way to represent a conversation |
| **Gate / barrier** | A barrier state transition plus the **loop detector** as a separate signal | `gate` (exists today, at its lowest resolution) + `gate.loop_detected` | A | `gate` is currently a single undifferentiated canonical name. It is the weakest name in the vocabulary and should be split |
| **LPR** | A **plate observation** with a legibility flag. Not an identity | `lpr.read` with `canonical.plateText`, `canonical.confidence` | A (device) / the plate is an `Entity` | Plate normalisation (case, separators, country) is a *canonicalisation* decision with privacy consequences, and it is exactly the kind of thing a vendor will get wrong in a different way from every other vendor. `vehicle-at-gate`'s schema says `plateText` is "Not normalized, not resolved to a vehicle identity in M1" — that decision should be re-examined at M4 rather than inherited |
| **BMS** (HVAC, power, access-controlled building systems) | A **point change** in a building system, and it is usually not a security event at all | `bms.point_changed` | A | The temptation is to make BMS a general SCADA ingest. The anti-segment rule (`strategy-and-north-star.md` §3: not a generic SIEM) applies. BMS is in scope only where a point change bears on a security outcome — occupancy feeding a zone's baseline, a power loss feeding a door's held-open state |

**The taxonomy rule:** *a signal is a transition or a threshold crossing, not a
level.* A device that reports "closed" every second produces no events; a device
that reports "open" once produces exactly one. This is the same rule that makes
`deriveEventType` synthesise `alarm.created` / `alarm.acknowledged` /
`alarm.closed` from a state machine, and it is the rule every sensor adapter
must implement.

---

## 9. The Security Graph and multi-tenancy

### 9.1 What exists

`internal/graph/graph.go` is 536 lines and its package doc (lines 1-27) states
the scope precisely: topology and provenance only.

**The M1 edge vocabulary is a closed set of nine relations** — `contains`,
`observed_by`, `occurred_in`, `supported_by`, `has_evidence`, `derived_from`,
`about`, `kind`, `revises` (`graph.go:46-57`) — and three methods: `observed`,
`asserted`, `inferred` (`graph.go:58-62`).

**Provenance is mandatory and is enforced in two places.** `graph.Edge.Validate`
(`graph.go:101-125`) refuses an edge with no `AssertedBy` (`ErrNoProvenance`,
`graph.go:67`) and refuses an `inferred` edge with no `RuleOrModelID`: "an
inferred edge without the rule or model that inferred it is a claim with no
reasoning, which is the thing the method exists to distinguish." The database
agrees: `edges.asserted_by TEXT NOT NULL` and
`method TEXT NOT NULL CHECK (method IN ('observed','asserted','inferred'))` in
`migrations/0003_events_and_graph.expand.sql`, plus
`confidence ... CHECK (confidence >= 0.0 AND confidence <= 1.0)`.

**The observability model, which is the most defensible part of the design:**

- `AddEdge` returns `(bool, error)` where `false` means "already existed"
  (`graph.go:144-160`). A replayed event re-asserting edges is silent and
  correct, not an error a caller retries for ever.
- `AddEdges` writes a whole topology set in one transaction (`graph.go:167-193`),
  with the stated reason: 2,000 cameras is 2,000 round trips for a thing that
  either all happened or did not.
- **`Walk` is bounded twice** — `MaxDepth` (default 6) and `MaxNodes` (default
  2,000), plus an optional `FollowedRelations` allow-list
  (`graph.go:219-231`, `259-277`). The comment gives the threat model directly:
  "A graph with an unbounded walk is a graph that can be made to consume the whole
  database by an event naming one node, and **an untrusted adapter supplies event
  names.**" Every future adapter inherits that threat.
- `Snapshot` is bounded at `MaxSnapshotEdges = 250_000` with `Truncated` reported
  (`graph.go:328`, `331-367`), and `Snapshot.MarshalJSON` is a stable
  serialisation so "two exports of the same graph are byte-identical and a diff
  means something" (`graph.go:512-520`). That is the property the non-adapter
  diff test (§6.3) depends on.
- `CheckRebuild` (`graph.go:382-422`) reports `OrphanSourceEvents` and
  `UnparentedZones` and explicitly declines to call either an error: "a retained
  incident may outlive its event's retention window — so this needs review rather
  than automatic repair."

### 9.2 Revocation

`Service.Revoke` (`graph.go:200-210`) inserts into `edge_revocations` and does
**not** delete from `edges`. The reasoning is the right one and the migration
comment restates it: "Revocation is a historical fact — 'this was claimed, and
then withdrawn' — and deleting the row would make a rebuilt projection disagree
with the live one. `edges_active` is what traversal reads."

`edge_revocations` has `reason` and `replacement_edge_id`, so a supersession
chain is walkable. The `revises` relation in the vocabulary is the graph-level
expression of the same idea.

**Gap:** `Revoke` takes `replacement string` but nothing enforces that the
replacement edge exists, and there is no `Unrevoke`. Both are correct: revocation
should be a one-way audited act. But the ADR should say so, because "revoke then
re-assert" is currently the only recovery and it leaves a gap in the active view
that no reader can distinguish from a topology fact.

### 9.3 Tenant scoping

Enforced in four places, and the layering is the point:

1. **Schema.** Every table has `tenant_id TEXT NOT NULL`. `PRIMARY KEY
   (tenant_id, edge_id)` on `edges` — the tenant is part of every key.
2. **RLS.** `migrations/0008_identity_and_rls.expand.sql` enables *and forces*
   row-level security on every table (lines 193, 197) and creates
   `tenant_isolation` policies of the form `tenant_id = app_current_tenant() OR
   app_is_system_worker()`, with a separate `site_isolation` policy for
   site-bound tables.
3. **The system worker boundary.** `app_is_system_worker()` (line 163) is
   `coalesce(current_setting('app.system_worker', true), '') = 'true'`, set by
   `dbx.InSystemTenant`. `status.md` deviation 3 is the reason this is worth
   stating: "Background workers reach other tenants through an explicit
   `app.system_worker` setting, bounded by grants, not a role exemption." And
   deviation 4: "`security_core` has no `BYPASSRLS`. Only the migrator does."
   `ClaimAll` (`inbox.go:279-281`) is the deliberate, visible call site.
4. **Code.** Every `graph.Service` method takes `tenant` as a parameter and every
   query filters on it. `Edge.Validate` refuses an empty tenant with the
   invariant's own wording.

### 9.4 What must change at M5

| Change | Why | Risk if done late |
|---|---|---|
| **A person model — and only then** | M5's requirement is "the same credential denied at Site A, denied at Site B, attempted at Site C is one incident, not three" (`master-roadmap.md` §M5 item 6). That is a *credential* correlation, not a person correlation, and it is achievable without a person model: the same `Entity` value across sites. **`entity.proto`'s `ENTITY_KIND_BADGE` and the `entities` table's `entities_identity_idx` are the hook.** A person model is *not* required for M5's stated gate | The temptation at M5 is exactly the temptation `graph.go:16-22` was written to resist, and the M6 exit gate adds a behavioural layer on top. See the finding below |
| **Cross-site `Entity` identity** | `entities` is currently tenant-keyed; a multi-site tenant needs the same badge to resolve to one entity | Duplicated entity rows, and M5's headline correlation silently does not work |
| **A `SITE`-scoped traversal default** | `Walk` is tenant-scoped; M5 needs site-scoped as the common case with tenant-wide as the deliberate choice | Every M5 query writes a site filter by hand, and one of them will forget |
| **Central-plane consistency rules** | M5's central plane is optional and sites stay autonomous. That means the graph is *eventually* consistent across sites, and every cross-site correlation must be able to say "Site B's data is 4 minutes stale" | A cross-site incident that quietly omits a site is worse than no cross-site incident |
| **Federated provenance** | An edge asserted by a site and consumed centrally needs a provenance that names the *site* as well as the actor | `asserted_by` is currently a string; at M5 it needs to be resolvable to `(site, actor, method)` |
| **Retention interaction** | `CheckRebuild.OrphanSourceEvents` is already a real category. Multi-site makes it worse: Site A's events may be gone while Site C's incident still cites the shared credential | Silent incompleteness in the evidence chain |

**A finding to resolve before M5, not at M5.** `graph.go:20-22` states: "the test
asserting the absence of `entity_aliases` exists to stop it becoming one by
accident." **No such test exists.** `internal/graph/graph_test.go` does not
contain it. So M1's most consequential scope decision — no person model, no
cross-camera identity, no re-identification, no entity aliasing — is protected
by a comment and nothing else, and M5 multi-site correlation plus M6 behavioural
baselines are exactly the two milestones that make an alias table look
attractive. The correct fix is cheap and belongs in M1: a test that asserts
`information_schema.tables` has no table matching `%alias%` and no column
matching `%person%`, run against the migrated schema. See
`quality-engineering.md` §3 for where it should sit in the invariant matrix.

---

## 10. The durability contract every adapter must uphold

### 10.1 The four properties

**(1) The dedupe key.** `inbox.Write` (`inbox.go:124-172`) takes
`(tenant_id, integration_id, source_event_id)` in `inbox_event_keys` **before**
inserting the row, in one transaction, with `ON CONFLICT DO NOTHING`. The stated
reason (lines 118-123): "Doing it in this order is what makes the exactly-once
effect hold: there is no window in which a row exists without a key, or a key
exists without a row." A conflict is `Accepted{Duplicate: true}` — a success with
no work, not an error.

**The adapter's obligation is the first half of that**: it must supply a
`SourceEventID` that is **stable across polls and unique per event**.
`adapter.go:239-244` enforces it at the item level: an item with no id becomes
`adapter_unparsed_item` with a synthetic, position-derived id, precisely so it is
not re-ingested forever. `status.md` deviation 1 explains why the key lives on a
non-partitioned table: "A unique index on a partitioned table must include the
partition key, so the planned constraint would only have deduplicated within a
month."

**(2) Lease and claim semantics.** `claimSQL` (`inbox.go:237-265`) is one
statement with `FOR UPDATE SKIP LOCKED`, so workers neither coordinate nor block
each other. The critical clause is
`OR (status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at < now())`,
and its comment is the whole reason the lease exists: "Without this clause the
lease expiry would be decorative: the row would never become claimable again and
a killed worker's event would sit until a human intervened, which is exactly the
loss the lease exists to prevent." `DefaultLeaseDuration` is 60s
(`inbox.go:229`).

The lease **expiry is read from the row, not computed locally**
(`inbox.go:212-214`): "so a worker's own clock skew cannot make it believe it
holds a lease the database has already given away."

`Renew` (`inbox.go:319-341`) and `Complete` (`344-359`) both match on
`status = 'processing'`, and `Complete` reports "not in processing state" when
`RowsAffected() == 0` — a wrong answer beats a silent one.

**(3) Idempotency of the consumer.** `normalize.Normalize` (`normalize.go:106-186`)
is where idempotency is actually won, and three mechanisms combine:

- The canonical event and the inbox transition commit in **one transaction**
  (lines 127-184). "There is no state in which a canonical event exists and the
  inbox still says received."
- The scoped key is claimed inside that same transaction
  (`canonical_event_keys ... ON CONFLICT DO NOTHING`, lines 137-150). A
  conflict sets `AlreadyNormalized` and completes the inbox row — a no-op.
- **The event id is derived, not generated** (`DeriveEventID`, lines 302-305):
  `sha256(tenant \0 integration \0 source_event_id)`, first 12 bytes.
  "A generated id would make each attempt a different event, and crash recovery a
  reconciliation problem rather than a retry."

And `completeInbox` (lines 191-218) matches on `lease_owner IS NOT DISTINCT FROM
$4` **as well as** status, rolling back when the lease moved, with the comment:
"Status alone is not enough: a reclaimed row is still status=processing."

Note the asymmetry, and it is deliberate: **canonical events get a derived id,
observations get a random one** (`observations.newID`, `observations.go:333-359`).
A canonical event is a fact recorded once; an observation is a *run* of a model,
and two runs are two rows — that is what supersession is for.

**(4) Terminal states.** `Writer.Fail` (`inbox.go:365-390`) is the only route to
`dead_letter`, and it records `dead_letter_reason`. `Replay` (`442-458`) is
operator-initiated, and the comment explains why it is not automatic: "a dead
letter usually means the source is producing something the platform cannot read,
and retrying that in a loop turns a visible incident into invisible load." The
ledger row is `audit.ActionInboxDeadLettered` = `durability.inbox.dead_lettered`.

The adapter's own contribution: `MaxAttempts` is 5 (`NewWriter`, `inbox.go:100-103`)
and a feed that fails for a reason the adapter could not parse becomes a
*record*, not an error — so adapter-level faults surface as a high
`adapter_feed_error` count rather than as a stuck queue.

### 10.2 The exact tests every adapter must pass

| Test | What it asserts | Method | Exists for Milestone? |
|---|---|---|---|
| **T1 — integration/replay parity** | The same scenario, live and from a fixture, produces byte-identical canonical events: id, type, observed_at, normalized_at, device_id, zone_id, correlation_id, integrity_hash | Capture → run live → run fixture → compare | **No.** This is M1 DoD item 1, blocked on a real server. It is the single most important missing test in the repo. |
| **T2 — duplicate re-read** | Re-reading the same feed after a reconnect produces `Accepted{Duplicate: true}` and no second canonical event | `inbox.Writer.Write` twice with the same key | `internal/durability` (some) |
| **T3 — crash at intake** | `kill -9` between key claim and row insert leaves no state that loses or duplicates | Fault injection | `quality-engineering.md` §5 owns the programme |
| **T4 — crash at normalize** | `kill -9` between canonical insert and inbox completion; recovery is a retry, not a reconciliation | Fault injection, asserting `DeriveEventID` stability | `quality-engineering.md` §5 |
| **T5 — lease expiry and reclaim** | A killed worker's event is reclaimed by another worker within two lease intervals, and the first worker's later `Complete` is refused | Fault injection + concurrent claim | `internal/durability` |
| **T6 — lease theft** | Two workers cannot both write; the loser rolls back and finds the key claimed | Two claims on one key | `normalize.completeInbox` logic |
| **T7 — dead letter is terminal and visible** | After `MaxAttempts`, status is `dead_letter` with a reason, it appears in `DeadLetters`, it is audited, and it is not retried automatically | `Writer.Fail` → `DeadLetters` | Yes |
| **T8 — partial feed outage** | One failing route does not abort the poll and produces exactly one `adapter_feed_error` | `TestFeedFailureIsIsolated` | **Yes** |
| **T9 — 404 is not a fault** | A route not deployed on this site produces no records and no error | `readFeed` both paths | Yes (implicitly) |
| **T10 — path traversal, four negatives** | `../`, absolute path, NUL byte, symlink escape are all refused on **every** evidence path this adapter can produce | `evidence.ValidateRef` + `Store` | `evidence_test.go`; **note** the symlink case skips on Windows, so on the platform the first hires will use it is not running |
| **T11 — determinism** | Two runs over one fixture produce identical records and identical `integrity_hash` | `IntegrityHash` excludes `received_at` on purpose (`normalize.go:335-340`) | Yes |
| **T12 — tenant scoping** | Every row the adapter's path produces is tenant-scoped and RLS-enforced; a cross-tenant read returns nothing | RLS suite | Yes (`quality-engineering.md` §3 Inv 5) |

**T1 is the gate and the blocker.** `master-roadmap.md` §M1 gate 1 requires it
and `status.md` names it as one of three DoD items that "cannot be signed here".

---

## 11. Adapter bring-up checklist

No adapter ships without all six. Each is a file, not a feeling.

### 11.1 A fixture set

- Captured from the real source, not authored. `fixture.Scenario.Source` records
  `"synthetic"` for an authored scenario, and `ManifestEntry.Covers []string`
  states what each scenario proves.
- Sanitized at capture time by `internal/fixture/sanitize.go`; no credentials, no
  MAC addresses, no person images, no absolute paths; only allow-listed headers
  (`fixture.go:154-159`).
- **Covering, at minimum:** duplicate, out-of-order (negative `OffsetMS`),
  page exhaustion, 404-not-deployed, 401, 500, malformed item, unknown event
  type, naive timestamp, unparseable timestamp, empty feed.
- `Validate()` clean, `ContentHash()` recorded in the manifest.
- **The epoch defect (§1.6d) fixed**, or the set is explicitly documented as
  byte-parity-only.

### 11.2 A conformance run

- Every case in §7.5's `SharedScenarios` and the seven other suites green.
- `VocabularyClosure` green: every emittable type is canonical or explicitly
  `unknown.*`.
- Result attached to the adapter's own CI run, and re-run on every change.

### 11.3 A replay-parity report

- T1 above. Same fixture set, live and replayed, canonical events compared
  byte-wise.
- **Either** a zero diff **or** a named, reviewed exception per line.
- Attaches the fixture manifest, the two runs and the comparison.
- Produced on a real installation. A parity report produced against a simulator
  proves nothing and must say so.

### 11.4 A fault-injection run

- `kill -9` at every stage the adapter's path touches: intake, normalize, graph
  write, rule evaluation, evidence write, action dispatch.
- Zero accepted event loss. Zero duplicate side effects.
- Per `AGENTS.md`: "Any durability change needs a `kill -9` fault-injection test
  at the stage it touches. It is a DoD item, not a nice-to-have."
- The four path-traversal negatives on every evidence path the adapter can
  produce, explicitly, as negative tests — not as an assertion that a valid path
  works.

### 11.5 A documented capability declaration

- The `device_capabilities` rows (§5.4) written from a real probe, with
  `source` set honestly per row.
- A statement of what is **unprobed**, listed as explicitly as what is probed. A
  capability declaration that lists only what works is marketing.
- A re-probe diff, showing what changed and when.
- The operator-facing consequence of each gap: which rules cannot match on this
  site, and why an incident will be attributed rather than guessed.

### 11.6 The route list, with its confidence

- Every `Route` entry's `Unverified` flag is either cleared by a probe or
  retained with an updated `Notes` line saying what the probe found.
- `UnverifiedRoutes()` is now empty or its non-emptiness is justified in writing.
- The `securityctl adapter probe` command — **which does not exist today** and is
  referenced five times in `routes.go` and its neighbours — is what produces this
  list, and building it is M1 work, not M4 work.

### 11.7 What the checklist does not include, deliberately

- A demo. A demo is not a conformance run.
- Vendor certification. Vendors certify conformance to *their* specification, not
  to ours, and their certificate says nothing about whether the adapter is right.
- "It worked at the design partner site." That is a smoke test. It goes in the
  smoke-test list, which is a different list, and it does not substitute for any
  of the six above.

---

## 12. Open questions this document cannot close

1. **Which vendor is the second adapter?** C1 makes it a commercial decision
   (`gtm-and-sales.md` §2.3, §7). Engineering's job is to score it, and the score
   depends on answers we do not have: whether a read-only integration account is
   supported, and what fraction of the vendor's event model maps cleanly.
2. **Does the epoch defect block M1 gate 1?** If the parity comparison is
   byte-level and both sides read the same stored envelopes, it does not. If
   parity is meant to include timing — "the same scenarios produce the same
   canonical events whether they arrive live or from a fixture" — then §1.6d must
   be fixed first. **This needs a decision from E1 before M1's gate is
   attempted**, because it changes what the gate means.
3. **`RawRecord.Malformed` needs a destination.** Options: (i) drop the field and
   keep the malformed-ness only as an `adapter_*` event type; (ii) carry it into
   `inbox_events` and make it visible in `DeadLetters` and the console; (iii)
   leave it. Option (iii) is not neutral — combined with the §1.6c defect it means
   a future consumer inherits a field that is true for every motion event.
4. **Zone resolution is not designed.** `normalize.Input.ZoneID` exists; nothing
   sets it. M1's production bundle filters on `zone.pathPrefix: /restricted`, so
   the one production rule in the shipped bundle cannot fire on a real site until
   the resolver exists. This is M1 work, not M4, and it is not in any backlog I
   could find.
5. **Is `entity_aliases` really out of scope, and who enforces it?** §9.4.
6. **The `vendor.*` field-path prohibition.** Should `semantic.go` refuse
   `field.path` beginning with `vendor`? It would break one shipped rule
   (`core.suppress.camera-offline-during-maintenance`) and one
   (`core.suppress.analytics-below-threshold`, which reads
   `vendor.Analytics.Confidence`). Both need a canonical replacement first. This
   is a small, high-value ADR candidate.

See `risk-register.md` §2.1 for the technical risks this document's open items
map onto, and `decisions-and-process.md` §2.2 for the contract-side decisions
items 2, 3, 4 and 6 above would need.
