# Milestone route audit — every route in `internal/milestone` against Milestone's published documents

Last revised: 2026-10-02. Author: an AI agent, unreviewed. Status: **A1 of the
vendor-route contradiction closure.** Evidence only — the code changes it
justifies are in `internal/milestone`, and each one is asserted by a test.

## The one rule this document exists to enforce

**A published OpenAPI document is not a running server.**

Reading these documents can correct an obvious wire assumption. It cannot
legitimately clear `Unverified` on any route, because `Unverified` means "no live
Milestone server has ever answered a request from this codebase" and a document
does not answer requests. Promotion stays a human editing `routes.go` after
reading a dated probe artifact, which is the rule `probe.go:24-31` already
follows. Nothing in this audit, and nothing in the code written from it, changes a
single `Unverified` flag.

## Evidence

Two documents, both committed in this repository, both retrieved from Milestone's
public developer site on 2026-09-30. Provenance and licences:
[`README.md`](README.md) in this directory.

| Short name | File | What it is |
|---|---|---|
| **EVENTS** | [`events-rest-v1.reference.md`](events-rest-v1.reference.md) | Milestone's Events REST API reference page, tag-stripped from the HTML they serve |
| **BOOKMARKS** | [`bookmarks-rest-v1.openapi.yaml`](bookmarks-rest-v1.openapi.yaml) | The OpenAPI 3.0.1 document Milestone serves for the Bookmarks API |

EVENTS is prose plus embedded response samples, not a machine-readable
specification: the events OpenAPI file 404s (see `README.md`). Where EVENTS
describes a shape it does so in its own words, and those words are quoted here
rather than paraphrased.

## Verdict vocabulary

| Verdict | Meaning |
|---|---|
| `CONTRADICTED` | A committed document states something incompatible with the route as declared. The route will not work as written, or will work into a body the adapter cannot read. |
| `CONFIRMED` | A committed document declares this method and path, with the required parameters, exactly as declared. |
| `NOT-ANSWERABLE-FROM-DOCS` | Neither document speaks to this route or this field. Absence from a document is not evidence of absence from the product, so this verdict **never** promotes and **never** condemns. It stays `Unverified` and it needs the probe. |

---

## 1. Verdicts

Ten distinct routes. Nine are the entries of `Routes` in
`internal/milestone/routes.go`; the tenth is `bookmarkRoute`, the write-back route
in `internal/milestone/writeback.go`.

The "was" column is not history for its own sake: it is the reason this audit
exists, and it is what a reader compares against a deployment that still runs the
old adapter. The "now" column is what `internal/milestone/route_audit_test.go`
matches against the code, so a route that changes in `routes.go` without a verdict
here fails the build.

| # | Route as declared before | Route as declared now | Verdict | Citation |
|---|---|---|---|---|
| 1 | `GET /api/v1/System/UserGroups` | `GET /api/rest/v1/System/UserGroups` | `CONTRADICTED` + `NOT-ANSWERABLE-FROM-DOCS` | EVENTS:34, :40, :72; BOOKMARKS:36-37 |
| 2 | `GET /api/v1/Events` | `GET /api/rest/v1/events` | `CONTRADICTED` | EVENTS:72, :220-263, :300, :311-313, :377-391 |
| 3 | `GET /api/v1/Events/{eventId}` | `GET /api/rest/v1/events/{id}` | `CONTRADICTED` | EVENTS:266-283 |
| 4 | `GET /api/v1/Alarms` | `GET /api/rest/v1/Alarms` | `NOT-ANSWERABLE-FROM-DOCS` (+ prefix contradicted) | EVENTS:49-50 |
| 5 | `GET /api/v1/Bookmarks` | `POST /api/rest/v1/bookmarks?task=searchTime` | `CONTRADICTED` | BOOKMARKS:45-46, :140, :167-171, :305-374 |
| 6 | `GET /api/v1/ResourceGroups` | `GET /api/rest/v1/ResourceGroups` | `NOT-ANSWERABLE-FROM-DOCS` (+ prefix contradicted) | none exists: neither document contains the words "resource group" or "zone" — that is the finding, not a missing citation |
| 7 | `GET /api/v1/Servlets` | `GET /api/rest/v1/Servlets` | `CONTRADICTED` | BOOKMARKS:257, :232; EVENTS:291, :409 |
| 8 | `GET /api/v1/System/Time` | `GET /api/rest/v1/System/Time` | `NOT-ANSWERABLE-FROM-DOCS` (+ prefix contradicted) | EVENTS:63, :93, :137, :172, :210 |
| 9 | `POST /api/v1/Bookmarks` (write-back) | `POST /api/rest/v1/bookmarks` (no `task`) | `CONTRADICTED` | BOOKMARKS:140-161, :424-457, :380-386 |
| 10 | — (not declared before this round) | `GET /api/rest/v1/eventTypes` | `NOT-ANSWERABLE-FROM-DOCS` **for the response schema**; the resource itself is documented | EVENTS:40, :42; :335 (`typeGroup` filter), :42 (`generatorType` / `generatorSubtype`) — none of which shows a body, and that absence *is* the verdict |

**Nothing in the table is `CONFIRMED`.** Before this audit, nine of nine routes
were declared and none of the declarations matched a published document. After
it, five are corrected to a documented form and four are marked as needing the
probe. Row 10 was added after it, under the same rule and by the same gate: a new
route with no verdict fails the build.

**Row 10 is not the same verdict as rows 1, 4, 6 and 8.** Those four are routes the
documents do not mention at all. Row 10 is a route the documents *name twice* and
never show. The resource and the method are quoted from EVENTS; the response
schema is the part nothing answers, and the route therefore declares **no**
`Expect` at all. Declaring a shape the evidence does not carry would make the
probe report a shape mismatch as a contradiction, which is a report that sends an
operator to file a vendor bug against a correct server.

### A tenth declaration, and it is a duplicate

`writeback.go:232-235` declares `systemTimeRoute` as a **second, independent**
copy of route 8's path. Nothing tied them together: the two could disagree and
every test would still pass, because each one only ever looked at itself. It is
now one declaration, looked up by path, with a test that says so. This is the
same class of defect as the rest of this document — a thing the code states that
nothing checks.

---

## 2. Route detail

### 2.1 The prefix, and the case discrepancy

| | |
|---|---|
| Declared | `/api/v1` — every route, all nine |
| Documented | `/api/rest/v1` |

`/api/v1` is not a Milestone prefix. It appears in neither document. The evidence
inside the two documents also disagrees with itself on case, and this audit does
**not** paper over it:

- EVENTS writes `/api/rest/v1/events` in all five of its quickstarts — `:72`,
  `:107`, `:149`, `:187`, `:215`.
- BOOKMARKS writes `https://localhost/API/rest/v1` in its `servers` block —
  `:36-37` — described as the *"API Gateway entry point"*.
- EVENTS itself mixes them: the same page uses `/API/IDP/connect/token` at `:63`,
  `:93`, `:137`, `:172` and `:210` against `/api/rest/v1/events` at `:72`.

**Chosen:** `/api/rest/v1`, because that is the form EVENTS uses for the resource
family this adapter calls, five times, and because it is the lower-risk of the two
in the single place where both are written (`servers:`). **Recorded:** the
bookmarks document's own spelling is `/API/rest/v1`. If a probe answers 404 on
every route at once, the case is the first thing to flip — and it is a one-line
change to a single constant. Two independent implementations of this adapter in
this repository (the POC at `poc/active-monitoring/internal/xprotect/client.go:36-41`
and this one) must not be allowed to drift into disagreeing about it silently.

**Also documented, and not addressed here:** the base URL is the site root.
`https://milestone.example.com`. Both documents put their paths after the host,
so a base URL that already ends in a path segment produces a doubled prefix. No
document states this as a constraint, so it is a `NOT-ANSWERABLE-FROM-DOCS`
observation, and the probe is where it gets settled.

### 2.2 Route 2 — the event feed

| Aspect | Declared | Documented | Verdict |
|---|---|---|---|
| Method and path | `GET /api/v1/Events` | `GET /api/rest/v1/events` | `CONTRADICTED` |
| Time cursor | `?since=<RFC3339>` (EVENTS has no `since`) | `?time=gt:'<ISO8601>'` — filter syntax `property=operator:value`, string and DateTime values in single quotes, `gt`/`lt` only, 10 ms accuracy | `CONTRADICTED` |
| Page size | `?limit=<n>` | `?size=<n>`, 1–2000 | `CONTRADICTED` |
| Paging | `?cursor=` plus a `Next-Cursor` **response header** | `?page=<n>`, page ≥ 0; continuation in the **body** as `_links.next` | `CONTRADICTED` |
| Ordering | none sent | `?orderBy=asc:'time'` / `desc:'time'`; allowed fields `time`, `tag`, `source.name`. Default list order is *"starting from the oldest"* | deliberately **not** sent — see below |
| Response shape | a bare JSON array (`Expect: "array"`) | `{"_links":{"prev","next"},"array":[…],"eventCount":N}` | `CONTRADICTED` |

The declared cursor was never a cursor. `since`, `limit` and `cursor` are not
parameters this API has, so every poll asked the same question — *"give me events"* —
and EVENTS:300 answers that by returning the oldest stored events first, from the
beginning of the retention window, every time. The inbox's per-(tenant,
integration, source_event_id) uniqueness turns the repeats into no-ops, so nothing
is *lost*; but the site is re-read end to end on every poll, and the recorded
reason for the adapter carrying a `since` cursor at all — *"an adapter that
re-reads the whole feed on reconnect is a load problem on a 2,000 camera site"* —
is not met.

The response shape is the more dangerous half, because it survives the path being
fixed: `decodeItems` accepted a bare array, or `Items`, `Data`, `values`, and this
document's answer is `array`. A body under a name it did not know is
`MalformedError`, and a feed that raises `MalformedError` is the exact failure
`routes.go:14-18` warns about — *"a wrong endpoint or a wrong field name shows up
as an empty event feed on a real site, not as a failing test"*.

**On `orderBy`, the one thing here left alone deliberately.** The documented
default order is oldest-first, and a poll filtered by `time=gt:'<cursor>'` reading
oldest-to-newest within that window is correct: the window is bounded on both ends
by the cursor, so the oldest-first order does not skip anything. Sending
`orderBy` would be a second guess at a server default that happens to be right, and
the document says the parameter applies to *"some resource collections"* (:252) —
sending it on a feed that does not support it invites a 400 for no gain. The probe
confirms the default; if a build orders differently, the cursor filter still makes
the poll complete, only slower.

**`Next-Cursor` is still honoured.** It appears nowhere in either document and the
documented continuation is `_links.next` in the body. Both are followed, because a
header the documents do not mention may still be what a given build sends, and one
branch is cheaper than being wrong. Which one a real server uses is a probe
question, and it is recorded rather than assumed.

### 2.3 Route 3 — one event

`GET /events/{id}`. The placeholder is `id`, documented `string <guid>`; the
adapter declared `{eventId}`. The route also takes `include`, and 404s for an event
that is *"not stored because of the event type retention policy"* (EVENTS:281) —
which the adapter already treats as a normal race (`routes.go:206-212`). Nothing
else about this route is contradicted; the field names it returns are in §3.

### 2.4 Route 5 — reading bookmarks

**The single clearest contradiction in the audit.** BOOKMARKS:45-46, in the
document's own words:

> Bookmarks cannot be retrieved by use a GET .../bookmarks request. Please use one
> of the search tasks under the POST requests.

The adapter reads bookmarks with `GET /api/v1/Bookmarks`. The documented read is
`POST /bookmarks` with `task` as a **query** parameter (`:167-171`) and a body
that is one of four shapes (`:174-183`). The one that matches a time-windowed
poll is `searchTime`, whose body requires `time` and `countLimit` (`:372-374`) and
requires either `resourceTypes` or `devicePaths` (`:257-258`).

Two documented details that constrain the implementation and are therefore
recorded rather than discovered later:

- With `time` and no `endTime` and no `timeLimit`, the search window is the
  documented default of 60 minutes (`:342`). A poll window wider than that has to
  say `endTime`.
- `resourceTypes` is one or more of `cameras`, `microphones`, `speakers`
  (`:319-321`). There is no "all" value, so the adapter names all three.

The answer is `{"array":[…]}` (`:380-386`) — the same unrecognised envelope as
route 2.

### 2.5 Route 9 — writing a bookmark

This is the Phase 1 seam: *"a pointer into the vendor's own recording"* so that
*"the operator opens the vendor's player, at the right second, on the vendor's own
archive"* (`writeback.go:33-38`). Three separate things about it were wrong.

**The task parameter.** `task` is a query parameter (`:167-171`) selecting one of
four behaviours (`:142-161`):

| `task` | What BOOKMARKS says it does |
|---|---|
| absent | *"a new bookmark is created from the body"*; *"the bookmark is created, but the rule system is not triggered"* (`:145`, `:160-161`) |
| `newReference` | *"create a new reference and trigger rules"*; *"will be used to execute configured rules, to ensure actions like Start-Recording is performed in real time. The newReference task does not create the bookmark in the database"* (`:147`, `:156-158`) |
| `searchTime` | search |
| `searchFromBookmark` | search |

**The choice, made deliberately: no `task`.** The plan asked for a decision here
and flagged that `writeback.go` implied one answer while the feature's purpose
read like the other. The document settles it, and it settles it the other way
round from the phrasing:

- `newReference` **does not create the bookmark at all** (`:158`). It fires the
  site's own rules so that actions like Start-Recording happen in real time. An
  incident write-back that made the customer's VMS start recording is an
  operational side effect on their archive, which is a capability activation, and
  `AGENTS.md` reserves that to a human.
- No `task` creates a bookmark that exists, is listed in the operator's bookmark
  list, and points at the right second — which is the entire declared purpose.

So the answer the code already implied is also the documented answer, and it is
now cited rather than assumed. The two tasks are not two spellings of one
operation and must not be conflated in either direction.

**The body.** Declared `{"bookmarks":[{idempotencyKey, deviceRef, time, label,
description}]}`. Documented `newBookmarkBody` (`:424-457`):

| Declared field | Documented field | Status |
|---|---|---|
| `bookmarks` (wrapper) | — | the documented body is a single bookmark, not an array |
| `label` | `header` (**required**, `:456`) | renamed |
| `deviceRef` | `devicePath{type,id}`, **required** (`:457`), `type` e.g. `cameras`, `id` a uuid (`:224-235`) | restructured |
| `time` | `timeTriggered`, with `timeBegin`/`timeEnd` (`:446-449`) | renamed |
| `description` | `description` (`:435`) | unchanged |
| `idempotencyKey` | — | **not in the document at all** |

`devicePath` is the sharp edge. It is `{type, id}`, and the events document shows
the id in its REST-resource form: `"source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c"`
(EVENTS:291), and states that a camera's may also be written as a bare `{id}`
(EVENTS:409). The adapter's `SiteRef` today is a display string such as
`"Perimeter North [CAM-014]"` (`writeback_test.go:57`), which is not a device
reference in this API. Sending it unchanged would put a vendor-unknowable value
in a required field.

**What the code does about it, and why it refuses rather than passes it through.**
`parseDevicePath` accepts `cameras/<uuid>`, `microphones/<uuid>`,
`speakers/<uuid>` and a bare uuid, and refuses anything else by name before a
byte leaves. The uuid check is an *assumption* — the schema says `format: uuid`
(:233-235) and this build enforces it — so a site whose device ids are not uuids
has bookmarks refused rather than sent and refused. That is the trade being made
deliberately: a refusal at the call site names the cause, and the alternative is
a 400 whose body says nothing about which field was wrong. The probe is where the
assumption is settled.

**The answer.** Declared `{"id":…}` / `{"bookmark":{"id":…}}`. Documented, for a
task-less POST, `bookmarkArrayBody` = `{"array":[{bookmarkBody}]}` (`:380-386`),
where the identity field is `reference`, described as *"Bookmark ID"* (`:408-410`).
So the existing parse could not have succeeded against the documented answer, and
its failure mode is the one `parseBookmarkRef` was written to avoid — *"a bookmark
that silently did not land is an operator who finds nothing"*.

**Idempotency, honestly.** The documents describe no idempotency mechanism: no
`Idempotency-Key` header, and no statement that a client-supplied `reference` is
honoured as a caller-chosen key. The key therefore continues to travel as a
header (free if honoured, ignored if not) and **no code claims it was honoured**:
`BookmarkRef.Replayed` stays `false` unless the vendor echoes the key back. A
second bookmark on a retry is a real, unmitigated risk, and it is recorded here as
an open item for the probe rather than papered over. What *is* provable is the
duplicate-avoidance mechanism the platform already owns — `POST /bookmarks` with no
`task` never triggers the vendor's rules, so a duplicate bookmark cannot start a
duplicate recording.

### 2.6 Routes 1, 4, 6, 7 — what the documents do not say

| Route | Why `NOT-ANSWERABLE-FROM-DOCS` |
|---|---|
| 1 `/System/UserGroups` | EVENTS:34 says cameras are listed with the **Configuration REST API**, not with a user-group route. So the *kind* of resource is wrong as well as the prefix, but the documents never enumerate what the Configuration API exposes under this gateway. |
| 4 `/Alarms` | Alarms are Configuration API objects (EVENTS:49-50) and, per EVENTS:50, are *triggered by* events rather than read as a feed. Nothing here declares an alarms feed. The adapter already treats alarms as a state machine (`adapter.go:563-590`), which is consistent with the document, and `CapabilityAlarms` is already documented as best-effort (`writeback.go:116-122`). |
| 6 `/ResourceGroups` | Neither document contains the words "resource group" or "zone". Nothing to correct it against. |
| 7 `/Servlets` | "Servlet" is XProtect's older name for a recording server and appears nowhere in either document. The documented device vocabulary is `cameras` / `microphones` / `speakers` (BOOKMARKS:257) and a device reference is `cameras/<guid>` (EVENTS:291). Recorded as `CONTRADICTED` because the resource name is contradicted, with the caveat that a later MIP release may still expose it under the Configuration API — which is a probe question, not a document question. |

### 2.7 Route 8 — the clock

`GET /api/v1/System/Time` is not in either document. There is no documented clock
endpoint, and there is no documented field name for a clock answer, so
`writeback.go:364-383`'s four-shape tolerance has nothing to be tolerant of yet.

Recorded for the probe, not decided here: every HTTP/1.1 response carries a `Date`
header (RFC 9110 §6.6.1), so a clock reading is obtainable from *any* 2xx this
adapter already makes — with the same round-trip uncertainty `SampleDrift` already
models. That is a design proposal against a standard, not a vendor fact, and it is
recorded here so the probe can be asked about it explicitly rather than discovered
late. The adapter's clock-drift gate — `ClockDriftSevere` excludes a device from
time-windowed corroboration (`writeback.go:189-221`) — therefore has no working
input today, which is a finding in its own right.

### 2.8 Route 10 — the event-type vocabulary, and the per-site trap

`GET /api/rest/v1/eventTypes`. In the vendor's own words, twice:

> Motion detection, License plate recognition or Fall detection are examples of
> event types. You can list available event types or create new event types with
> the Configuration REST API: Event types. — EVENTS:40
>
> An API request with a service token can trigger events of any event type in
> `GET /eventTypes`. In contrast, a request with a user token can only trigger
> events with `generatorType=External` (User Defined Events) and
> `generatorType=MIPDevice` (Analytics Events and other event types defined by
> plugins), excluding those with `generatorSubtype=Generic` (Generic Events).
> — EVENTS:42

**Documented:** the resource name, the method, and that a service token may read
it. **Not documented, anywhere:** the response body. Neither committed document
shows one. That is the whole of row 10's verdict, and it is why the route carries
no `Expect`.

What the surrounding prose does constrain is worth writing down, because it is
what the two field-name guesses were made from and a reader deserves to know they
are guesses. `:42` names `generatorType` and `generatorSubtype` on an event type;
`:335` documents a `typeGroup` filter over a GUID (`typeGroup=equals:'d0ba5376-
…'`), which is the only other GUID-shaped vendor identifier in either document
and is a reasonable model for the identity field. The identity and name
spellings this adapter reads are in `eventtypes.go` as `guidKeyFields` and
`nameKeyFields`, and the probe's `sample` field is how they get settled.

#### The trap, which is the reason this route is per-site and not a table

**The GUIDs are per-site configuration.** `481d4d6c-9156-4698-af7e-74b32f9e4359`
is an *instance* in one customer's XProtect; the GUID is generated when that
customer created that event type, and the next installation has a different one
for the same concept. `:42` says as much in its own words — event types are
"defined by plugins" and by user-defined events, which is a statement about one
site's configuration rather than about a product-wide constant.

So a compiled-in `guid -> motion` table would work perfectly at the design
partner and dead-letter every event at customer two. It would be invisible until
there was a customer two, and no test using the design partner's bytes could ever
find it, because those bytes *are* the table.

The mapping is therefore resolved at read time from the site's own answer, per
`(tenant, site)`, cached for the poll, and **a GUID the site did not answer for is
unresolved — never a default**. `TestTwoSitesWithDifferentGUIDsResolveToTheirOwnNames`
is the assertion that catches the table version, and it is the test to read before
changing anything here.

#### What landed, and what did not

| | Change | |
|---|---|---|
| 1 | The route is declared, `Unverified`, read once per poll before any feed. | Landed. |
| 2 | The site's own name is carried on `RawRecord.SourceTypeName`, quoted in the dead-letter reason, and written into the stored envelope under `_resolvedEventTypeName`. | Landed. |
| 3 | The stored name is an **input** to the existing `normalizeEventType`, so a site whose names are spellings the platform already knows works with no table at all. | Landed. `normalizeEventType` and `KnownMilestoneEventTypes` are unchanged. |
| 4 | An alias table from a vendor's name to a canonical type. | **Refused.** Published vocabulary; `AGENTS.md` reserves it; and it is not writable until a probe has shown a real site's real names. |
| 5 | Any `Unverified` flag. | **Refused**, and unchanged. |

Item 2's envelope key deserves a word, because it is the least obvious decision
here. The resolved name is fetched at ingest time, and `RecordFromPayload`
re-derives the record from the **stored bytes** weeks later — which is what makes
a claim retried after a crash produce exactly the same canonical event (invariant
2). A name that lived only in memory would give the same event two different
types depending on when it was read, and no replay would notice, because both
answers are internally consistent. So the name is stored with the envelope and the
replay path reads it from there and never calls the site.

---

## 3. The event object, field by field

This is §2's payload, and it is the part with the longest reach, because the
adapter's whole mapping table hangs off these names.

`routes.go:76-78` declares the event field names it expects as *"Id, Timestamp,
Type, Source, Priority, TimeSpan, Message, ImagePath, VideoRecordingStartTime"*.
EVENTS:289-297 and :380-388 show the object it actually returns:

```json
{ "specversion": "1.0", "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
  "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
  "time": "2023-02-28T18:10:12.4786334Z", "id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
  "localId": 4, "datatype": "none", "tag": "My custom tag" }
```

| Declared name | Present? | What EVENTS calls it | Effect on the adapter as written |
|---|---|---|---|
| `Id` | yes | `id` (a guid) | none — `stringField(item, "Id", "id", …)` already accepts `id` |
| `Timestamp` | yes | `time`, ISO 8601 UTC, 7 decimal places | none — `"Time", "time"` is already in `milestoneTimestampKeys` and `RFC3339Nano` parses it |
| `Type` | yes, **but not a name** | `type`, **a GUID** | **every event** normalises to `unknown.<guid>` and is flagged `Malformed` |
| `Source` | yes | `source`, `cameras/<guid>` | device resolution reads `Source` first and expects a bracketed id in it; there is none, so `SourceDeviceRef` is empty |
| `Priority` | **no** | — | nothing reads it |
| `TimeSpan` | **no** | — | nothing reads it |
| `Message` | **no** | `message` is a *filter* name (EVENTS:318-321), not a returned field. The nearest returned field is `tag` | nothing reads it |
| `ImagePath` | **no** | — | nothing reads it |
| `VideoRecordingStartTime` | **no** | — | nothing reads it |

`Priority`, `TimeSpan`, `ImagePath` and `VideoRecordingStartTime` are not
Milestone Events REST fields at all — they are the shape of the older XProtect
Event Manager API. Whatever produced that list, it was not this document, and
four of the nine names it contributed have no counterpart to map onto.

The `type`-is-a-GUID row is the one that needs a human — but only the second half of
it does any longer. Getting from a GUID to a name needs the event-types resource,
which EVENTS:40 and :42 place in the **Configuration REST API** and reference as
`GET /eventTypes`. **That route now exists** (§2.8): it is declared, probed, and
the site's own name for a GUID now reaches the record, the envelope and the
dead-letter reason.

What still needs a human is what comes after: deciding that a given vendor name
means a canonical type, and therefore which names become which members of
`rules.KnownEventTypes` and of the published
`contracts/json-schema/rule-bundle.schema.json` vocabulary. That is a contract
change, and `AGENTS.md` reserves those. It is also **not writable yet**, for a
reason the code cannot supply: the names in the table have to be a real site's,
and no Milestone server has ever answered a request from this codebase. It is
handed over in §5, unapplied.

Until it is decided, the honest behaviour is the one already implemented: the
event arrives, is stored, and is flagged `Malformed` with the reason quoted
(`adapter.go` `unrecognisedTypeReason`). Nothing is dropped and nothing is silently
mapped. That is invariant 1 and invariant 3 behaving correctly under an unknown
vocabulary. Since §2.8 the reason also names what the site calls the type, so the
dead letter is an input to the decision instead of a question mark.

### Three consequences of the `type` finding, and what happened to each

The `type`-is-a-GUID row above is the one that needs a human. These three hang off
it, and they do not all have to wait for it — that was the mistake in the first
draft of this document, which listed them as consequences and left them all sitting.

| | Finding | Outcome |
|---|---|---|
| 1 | A documented bookmark item carries **no `Type` field**, so every bookmark item currently arrives as an `adapter_unparsed_item` whose reason is *"item … has no type field"*. The documented bookmark identity field is `reference` (`:408-410`) and its timestamp is `timeTriggered` (`:446-448`); neither is read as either. | **Open — H1.** Deciding what a bookmark *is* to the platform is a vocabulary decision, and it is the same decision as the GUID mapping. The events still land and are visible in the dead-letter table, which is correct under an undecided mapping. |
| 2 | The device reference a bookmark needs is the one the event already carries. `source` on a documented event is `cameras/<guid>` (EVENTS:291), so the `{type, id}` pair `devicePath` wants is derivable with no new route and no new capability. | **Partly closed.** `parseDevicePath` now takes exactly those two documented forms and refuses anything else by name, before a byte leaves. Which source a correlation window's bookmark points at is still a product decision and is named in `handover.md`. |
| 3 | `SourceDeviceRef` stayed empty for every documented event. The parser read the device from `Source` and expected a bracketed id in it; `source` is `cameras/<guid>`. So events were unattributable and **every device-filtered rule matched nothing**. | **Closed.** `sourceDeviceRef` parses the documented REST resource string and the documented bare-uuid form, and returns the identifier without the collection prefix — which is what `devices.source_device_id` holds and what `resolveDevice` matches on. Red-checked: disabling the parse fails three tests. |

The one that was closed is the one that mattered most, and it is worth being precise
about why it was wrong rather than merely wrong. Every other field in that table
fails *loudly*: an event with no type is flagged `Malformed` and dead-lettered with
the reason quoted, and someone reads it. An event with no device fails **silently**
— zone rules and device-filtered rules simply stop matching, which is
indistinguishable from a site where nothing is happening. A defect that looks like
the absence of events is the worst kind there is, and this one had been in the tree
for the entire life of the adapter.

A test now feeds the vendor's own sample body
(`events-rest-v1.reference.md:380-388`) through the parser, and a second test pins
the blast radius: the *only* thing a documented event cannot do is name its type.
When H1 is decided that test is the one to change, and it fails loudly if a second
gap appears beside it. **It is still true after §2.8**, and deliberately so: the
route resolves what the site calls the type, and the platform still does not know
what to call that.

---

## 4. What the documents themselves get wrong

Two defects in the evidence, recorded because a reader will otherwise trust them
more than they deserve.

1. **BOOKMARKS:31-33** links its own download to `openapi.yaml` in the same
   directory. The committed file is named `bookmarks-rest-v1.openapi.yaml`. The
   document therefore does not attest to its own filename, which is a small
   reminder that its contents are a snapshot of one retrieval, not a live contract.
2. **EVENTS has no machine-readable form.** Its OpenAPI and JSON downloads both
   404; the page is JavaScript-rendered (`README.md`). Every citation to EVENTS in
   this audit is therefore a citation to prose and to embedded response samples.
   Where those disagree with BOOKMARKS — the prefix case — both readings are kept
   rather than one being preferred silently.

---

## 5. What this audit does NOT change

Two items are outside what an agent may change, per `AGENTS.md`: published
contracts, and anything that changes a capability.

Both are written up for a human in [`handover.md`](handover.md), with the sequence
to read them in. In short:

**H1 — the event-type vocabulary.** §3's GUID row, and the three consequences
listed with it. **Phase A has landed** (§2.8): the Configuration API route is
declared, the site's own name for a GUID is resolved per site and carried on the
record, the envelope and the dead-letter reason. **Phase B is still yours**, and it
is the part that cannot be written without a live site:

1. Run `securityctl adapter probe` against a real installation and read
   `sample` on the `/eventTypes` row. That is the response schema, which neither
   document shows, and the identity/name field names this adapter guesses at
   (`eventtypes.go`) either match it or do not.
2. Read the GUID→name pairs off that row. **They are per-site**, so the decision
   is about how to map *names*, not GUIDs — a compiled-in GUID table would break
   at customer two, which is why none was written.
3. Decide which vendor names become which members of `rules.KnownEventTypes` and
   of the rule-bundle schema. Sequence: `contracts/` first, regenerate,
   `contract-check`, then the code, then this document's verdict column.
4. In the same review, answer consequence (1) below: whether a bookmark feed is
   read at all.

**H2 — the hosted-model attribution proposal.** `docs/proposals/hosted-model-attribution.md`
is complete and blocked on a human reading the diff. Its §2 asks whether
`region_refused` is a degradation at all or a correct refusal — a sovereignty
decision that would otherwise be frozen into a schema `CHECK`. Its migration number
was colliding with a shipped one and is now 0022, checked by a test; the
sovereignty question is untouched. Unrelated to the route contradiction; named here
because both are waiting on the same signature.

**H3 — the one deliberate half-fix.** The bookmark feed reads the vendor correctly
and still produces no records, because a documented bookmark item has no `Type`
field. That is consequence (1) of H1 and it is on the route's own `Notes` so a reader
of §1 row 5 is not misled. The bookmark *write* side — the Phase 1 seam — is fully
corrected.

---

## 6. How this document is kept honest

A verdict column that nothing reads is a comment with a table on it.
`internal/milestone/route_audit_test.go` makes it a gate, in both directions:

- **Every route has a verdict.** Every entry of `Routes`, plus `bookmarkRoute`, has
  a row in §1's table, the verdict is one of the three words above, and the
  citation column is populated. A route added to `routes.go` without a verdict
  fails the build.
- **Every verdict names a real route.** A row for a route the code no longer calls
  is a stale claim, and this repository's own rule is that a stale claim is a
  defect — so it fails too.
- **The code and §1 agree.** Each route's method, path and task are asserted against
  a literal copy of the "now" column, so the table cannot drift away from
  `routes.go`.
- **The evidence has not changed.** The two vendor documents are re-read, and the
  prefix, the bookmark body field names, the bookmark `task` values and the
  documented filter and paging spellings this adapter now sends are asserted to be
  in them. If the specs are ever re-downloaded and a name has moved, that is a
  finding, not a silent divergence.

Row 10 was added under this gate and the gate caught it, which is the point of
having one: `GET /api/rest/v1/eventTypes` failed `TestEveryRouteHasAVerdictInTheAudit`
the moment it was declared, and this row is why it passes now. It was not waived
and the test was not loosened.

Two properties of `/eventTypes` that are gates rather than comments, because both
are ways to get this quietly wrong: `TestTheEventTypeVocabularyIsNeverPolledAsAFeed`
fails if the route is marked `Paginated`, since `Adapter.Poll` reads every route
marked that way and would store an event-type list as though a camera had produced
it; and `TestTheResolvedNameIsStoredSoTheTypeReplays` fails if the site-resolved
name is not written into the stored envelope, because the derive-from-stored-bytes
property (invariant 2) is what stops one event having two types.

This is the third class-level gate after `sqlguard` and `rlsrole`, and it exists
because the class has already recurred — a documented field nothing reads, in
`RawRecord.Envelope`, `checkpoint.SealRange` and `api.Server.start`.

None of these can promote a route, and none is a probe. `securityctl adapter
probe` remains the only thing that can produce evidence a human may act on, and a
human remains the only thing that may act on it.