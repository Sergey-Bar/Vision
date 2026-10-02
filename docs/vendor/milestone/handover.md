# Handover — three things that need a human, and why an agent did not do them

Last revised: 2026-10-02. Author: an AI agent, unreviewed.

This file is a list of what the 2026-10-02 route-audit pass deliberately did **not**
do, and what a human has to decide. It is not a backlog of nice-to-haves. Each item
is something the repository's own rules put out of an agent's reach, and each is
written so that the decision can be made without re-reading the code.

The evidence behind all three is in [`route-audit.md`](route-audit.md).

---

## H1 — the event-type vocabulary: phase A landed, phase B is yours

**The finding, unchanged.** Milestone's Events REST API returns `type` as a **GUID**
(`events-rest-v1.reference.md:289-297`, `:380-388`). The adapter's
`normalizeEventType` expects a human-readable name and maps it into
`rules.KnownEventTypes` and, through it, the published rule-bundle vocabulary. So
every event from a real server normalises to `unknown.<guid>`, is flagged
`Malformed`, and is dead-lettered with the reason quoted.

Nothing is lost — the event is stored and the reason is visible — so invariant 1 and
invariant 3 are both satisfied. But the feed cannot work, and the audit had to leave
it that way, because fixing it means deciding what the vocabulary *is*.

**What changed on 2026-10-02 (phase A, no decision required).** Item 1 of the
original list is done, and the finding above is now smaller than it was:

| | Was | Is |
|---|---|---|
| The event-types route | "Adding that route is easy" — not added | **Declared**, `Unverified`, probed by `securityctl adapter probe` with the response body in the report (`route-audit.md` §2.8, §1 row 10) |
| The GUID | An opaque string in a dead letter | **Resolved per site.** `RawRecord.SourceTypeName` carries the name *this site* uses, and the dead-letter reason quotes it: *"this site calls event type \"Motion Started\" a \"481d4d6c-…\""* |
| The mapping | none | Still none. `normalizeEventType` and `rules.KnownMilestoneEventTypes` are untouched. |

Two things that are worth knowing before you read the code:

- **The GUIDs are per-site.** A compiled-in `guid -> name` table would work at the
  design partner and dead-letter every event at customer two, invisibly. That is why
  the mapping is resolved from each site's own answer and why a GUID the site did
  not answer for is *unresolved* rather than defaulted.
- **The site-resolved name is stored in the envelope**, under
  `_resolvedEventTypeName`. It has to be: the consume loop re-derives the record
  from the stored bytes weeks later, so a name that lived only in memory would give
  one event two different types depending on when it was read.

**What still needs you (phase B), and it is the part that cannot be written blind.**

1. **Probe a real site and read the `/eventTypes` row's `sample`.** That is the
   response body. Neither published document shows one, so it is the only evidence
   there will be until someone stands in front of a server. Read off it: the
   identity field, the name field, and the GUID→name pairs. The field names
   `eventtypes.go` currently guesses at are either right or wrong, and this is how
   you find out.
2. **Decide the mapping on *names*, not GUIDs.** Which of the vendor's own names
   become which members of `rules.KnownEventTypes` and of
   `contracts/json-schema/rule-bundle.schema.json`. Sequence: `contracts/` first,
   regenerate, `contract-check`, then the code, then `route-audit.md`'s verdict
   column. A name-based table is what makes this portable; a GUID-based one is
   what makes it work at exactly one customer.
3. **Answer the bookmark question in the same review** (consequence 1 below).

**What a human can do cheaply instead of deciding anything:** run
`securityctl adapter probe` against a real installation and read the artifact. It
reports, per route, the fields the server actually returned next to the fields this
adapter expects, and now the first item's body. That turns "the documents say GUID"
into "this build returned `481d4d6c-…` for an event it calls Motion Started", which
is the input H1 actually needs.

**Do not** clear any `Unverified` flag as a side effect of this work. The audit
refuted five routes against published documents; it confirmed none, and cannot.
Phase A confirmed nothing either — it declared a route the documents *name* and
refused to declare a schema they do not show.

**Three consequences that must be decided in the same review**, because fixing `type`
alone leaves them broken and they are all mapping questions rather than protocol
ones:

| | Finding | Where it stands |
|---|---|---|
| 1 | A documented bookmark item carries **no `Type` field**, so every bookmark arrives as `adapter_unparsed_item` — *"item … has no type field"*. Its identity field is `reference` (`bookmarks-rest-v1.openapi.yaml:408-410`) and its timestamp is `timeTriggered` (`:446-448`); neither is read as either. | **Open. Yours.** The bookmark feed is "corrected" in the sense that its method, task and envelope now match the document, and it still produces no records. Deciding what a bookmark *is* to the platform is the same decision as the name mapping. |
| 2 | The device reference a bookmark needs is the one the event already carries: `source` is `cameras/<guid>` (EVENTS:291), which splits directly into the `devicePath{type,id}` the bookmark body requires (BOOKMARKS:224-235). | **Protocol closed, decision open.** `parseDevicePath` now takes both documented forms and refuses anything else by name. What is still yours is *which* source a correlation window's bookmark points at — the first event, the alerting event, or the device the rule matched. That is a product question, not a protocol one. |
| 3 | `SourceDeviceRef` was empty for every documented event. The parser read the device out of `Source` and expected a bracketed id; `source` is `cameras/<guid>`. So events were unattributable and **every device-filtered rule matched nothing**. | **Closed, 2026-10-02.** The documented REST resource string and the documented bare-uuid form both parse, returning the identifier without the collection prefix — which is what `devices.source_device_id` holds. Red-checked. It needed no decision: the value is documented and unambiguous, and it was the one defect in this table that failed *silently*, which is the worst way to fail. |

**What a human can do cheaply instead of deciding anything:** run
`securityctl adapter probe` against a real installation and read the artifact. It
reports, per route, the fields the server actually returned next to the fields this
adapter expects. That turns "the documents say GUID" into "this build returned
`481d4d6c-…` for a motion event", which is the input H1 actually needs.

**Do not** clear any `Unverified` flag as a side effect of this work. The audit
refuted five routes against published documents; it confirmed none, and cannot.

---

## H2 — `region_refused`, in `docs/proposals/hosted-model-attribution.md`

**Unrelated to the route contradiction.** Named here because both items are waiting
on the same signature.

**The finding.** The proposal's SQL adds
`CHECK (... OR (model_identity <> '' AND model_contract_version <> ''))` and its
degradation vocabulary includes `region_refused`. Its §2 asks the question directly:
*is a refusal the same kind of thing as a degradation?*

The document answers its own question — *"A sovereignty decision, not a degradation
of service"* — and then puts the value in a `CHECK` beside the degradations anyway.
So the schema currently says "refused because of a sovereignty boundary" and "the
model was slow" are the same class of event, in the same column, aggregated together
by whatever reads them.

**Why it was not settled here.** `region_refused` being a degradation is a
sovereignty decision: it changes what a customer's regulator can conclude from an
audit of a pilot, and `AGENTS.md` reserves that. Guessing "correct refusal" and
freezing it into a `CHECK` is the failure this repository has already paid for once.

**What changed in the proposal, and what did not.** Its migration number was
`0015_observation_hosted_attribution.expand.sql`, which collides with
`0015_partitions_and_correlation_indexes.expand.sql`, shipped long ago; the tree is at
21. That is now `0022`, and `migrations/proposal_migration_test.go` fails the build
if any proposal names a number that has already shipped or skips ahead. The
`region_refused` question is untouched and still needs an answer before the diff is
read.

---

## H3 — one deliberate half-fix, named so it is not discovered later

**The bookmark feed reads the vendor correctly and produces no records.** Route 5's
method, `task`, search body and response envelope all match
`bookmarks-rest-v1.openapi.yaml` now, and the adapter's item decoder reads the
documented `array` envelope. What it cannot do is turn a documented bookmark item
into a platform event, because that item has no `Type` field and the platform's event
type is a closed vocabulary — consequence (1) of H1.

This is stated here rather than left to be found, because a reader of
`route-audit.md` §1 row 5 is entitled to conclude the bookmark feed works and it does
not. `Routes[4].Notes` says so on the route itself, and the audit says it in §3.

The bookmark **write** side — the Phase 1 seam, a pointer an operator can follow — is
fully corrected and is a separate matter from reading bookmarks back.

---

## What is *not* on this list

For the avoidance of doubt, the following were done and need no decision:

- The path prefix, the placeholders, the bookmark method and task, the documented
  request body and answer shape, the documented filter and paging spellings, the
  documented `array` envelope, the single-sourced clock route, the device-reference
  refusal, the `task`-less write decision and its rationale.
- The event-type route, the per-site GUID→name resolution, and the site-resolved
  name on the record, the envelope and the dead-letter reason. No mapping was added
  and no vocabulary was invented — the existing normalizer is offered the site's own
  name and decides what it already knew how to decide.
- Every `Unverified` flag is still set. No route was promoted.
- No published contract changed. `contracts/` was not touched, so no regeneration or
  wire change is pending review.
- No rule-bundle vocabulary changed, and no new normalized event type was invented.

The verification evidence for all of it is `scripts/verify.ps1`'s own record — a
local run, `verified: true`, **fifteen** gates, race detector on, database present —
written to `dist/verification/`. It is a local run and is labelled as one. **There is
no CI in this repository** and the record says so in its own `provenance.ci` field.