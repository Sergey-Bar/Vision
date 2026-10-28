# Shared Interface — Milestone / Hafnia Integration

This document is the minimal shared interface both adapters implement for the
Milestone XProtect + Hafnia VLM hackathon scenario. It is a contract, not code:
the Go side of it lives in `internal/integration`.

## Scenario

XProtect motion/analytics event → clip evidence → Hafnia VLM answer → Milestone
bookmark on the camera timeline.

## Direction of connection

1. **Poll from Milestone** — the Milestone adapter reads events from XProtect
   feeds (`Adapter.Poll`) into `RawRecord`s, deterministic by source event id then
   observed time.
2. **Call out to Hafnia** — the Hafnia adapter uploads the clip as an asset, asks
   the prompt, and deletes the asset. Quota is consulted before any bytes leave
   the site.
3. **Write back to Milestone** — the bridge places a bookmark on the camera
   timeline via `milestone.PlaceBookmark` / `BookmarkWriter`, pointing the
   operator at the moment the event happened.

## Shared event shape

Both adapters produce/consum the same `Event` interface:

| Field | Meaning |
|---|---|
| Event ID | The source's own id for the event (Milestone `SourceEventID`). |
| Source | The source system that produced it. |
| Logical camera | The camera the event is about, in the source's vocabulary. |
| Occurred at | UTC instant the source claimed (`observed_at`). |
| Description | Human-readable description of what happened. |
| Event type | One of the closed event-type vocabulary (normalized names, not vendor spellings). |
| Simulated | true when the input is synthetic/manual test data. |

Every simulated input carries `simulated: true` and is labelled `SIMULATED` in
the demo output; it is never presented as automatic detection.

## Event type vocabulary (closed)

Mirrors `milestone.NormalizedEventTypes()` — normalized lowercase names, not
vendor spellings:

- `motion`
- `analytics`
- `analytics.restricted_person`
- `analytics.person_with_bag`
- `analytics.vehicle`
- `analytics.animal`
- `alarm.created`, `alarm.acknowledged`, `alarm.closed`
- `device.camera_offline`, `device.camera_online`
- `recording.started`, `recording.stopped`
- `bookmark`, `metadata`
- `access.granted`, `access.denied`, `gate`

## Confidentiality

No proprietary sources, customer data, model weights, or internal documents are
part of this interface. All demo data is synthetic and marked.
