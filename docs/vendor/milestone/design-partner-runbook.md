# Runbook — one afternoon at a real XProtect installation

Last revised: 2026-10-02. Author: an AI agent, unreviewed.

**What this is for.** Three questions about a real server that no document in this
repository answers and no test can answer for us. They are the input to the H1
decision in [`handover.md`](handover.md) and to the M2 sizing in
`docs/roadmap/master-roadmap.md`, and the whole of it is three commands.

**What this is not.** Not an installation guide, not a commissioning checklist,
and not a promotion procedure. Nothing here changes a route's `Unverified` flag
and nothing here writes to the customer's system.

---

## Before you go

| | |
|---|---|
| **A service token** | `/eventTypes` is readable with a service token (`events-rest-v1.reference.md:42`). A user token sees a filtered list, so the answer would be a subset that looks like the whole. Ask the customer for a **service token**, read-only if they will grant one |
| **The API root** | The scheme, host and port the customer's XProtect Management Server answers on. Often `https://<host>` and sometimes `https://<host>:8080` |
| **A known plate or object** | One licence plate or object class you can filter on. For question 2 |
| **An operator in the room** | Not for the commands — for the two questions the commands cannot answer: which cameras actually have analytics switched on, and who owns the rule configuration |

**Do not** put the token in a ticket, a file in this repository, or a screenshot.
Pass it through the environment:

```powershell
$env:SP_MILESTONE_TOKEN = "<the token>"
$MS = "https://milestone.example.internal"
```

---

## Question 1 — what are the event types actually called here?

This is the one that unblocks the alias table. `internal/milestone/eventtypes.go`
resolves each GUID against this site's answer, and the platform still does not know
what to call what it finds.

```powershell
securityctl adapter-probe --base-url $MS --token $env:SP_MILESTONE_TOKEN --json `
  > "$env:TEMP\probe-eventtypes.json"
```

Read the row for `/api/rest/v1/eventTypes` in that file. Two fields matter:

| Field | What it settles |
|---|---|
| `sample` | **The response schema**, which neither document shows. The identity field, the name field, and whether entries sit under `array` or stand alone. `eventtypes.go` guesses `id`/`name` today; this is how the guess is confirmed or killed |
| `sample[].name` | **The site's own names.** These are what a mapping table would be written from |

Also read the rows for `/api/rest/v1/events` and `/api/rest/v1/bookmarks`, and note
each one's `verdict`. A route reported `confirmed` means the *shape* matched and
nothing more — `NeedsHuman` says so on every `Unverified` row, and that sentence is
the tool refusing to grade its own homework.

**If `/eventTypes` comes back 404**, the answer is not a failure: this site predates
the Configuration API, its event types stay GUIDs, and the dead-letter reason will
say exactly that. Write it down and stop; questions 2 and 3 still matter.

---

## Question 2 — does `analyticData` arrive at all?

```powershell
securityctl adapter-probe --base-url $MS --token $env:SP_MILESTONE_TOKEN --json `
  --param "analyticData.object.value=contains:'<the plate>'" `
  > "$env:TEMP\probe-analyticdata.json"
```

Read the `sample` on the `/events` row.

| What you see | What it means |
|---|---|
| An event whose `analyticData.object.value` carries the plate | **Analytics are on and populated.** M2's 2ew item stands, and the `restricted-person` class becomes a geometric test on a typed event — deterministic, no GPU, no 8-second inference |
| An empty array, HTTP 400, or a filter error | Either the filter syntax is not honoured on this build, or analytics are off. **These are different facts and the two commands below tell them apart** |
| Events, none with `analyticData` | The filter was ignored rather than rejected. Run question 3 to find out whether analytics exist at all on this site |

Note whether the response was an **empty result** or an **error**. A server that
rejects an unknown filter is telling you the filter is wrong; a server that ignores
it is telling you the filter is unsupported. Neither is "no analytics".

**This is a documented filter with no machine-readable spec.** EVENTS has no OpenAPI
and no JSON download — both 404 (route-audit.md §4). Every citation to it is a
citation to prose. So "the filter is honoured" is a fact about a build of Milestone,
which is exactly why it is asked of a server and written down rather than believed
from a page.

---

## Question 3 — is `object.vendor` populated?

This one is the provenance field, and it is worth more than it looks.

```powershell
securityctl adapter-probe --base-url $MS --token $env:SP_MILESTONE_TOKEN --json `
  --param "typeGroup=equals:'d0ba5376-cba7-4a30-8f4b-b78d57b7a435'" `
  > "$env:TEMP\probe-typegroup.json"
```

`typeGroup` is the one GUID-shaped filter the documents actually show
(`events-rest-v1.reference.md:335`). Then read `sample[].analyticData.object`:

| Field | If populated | If empty or absent |
|---|---|---|
| `vendor` | The engine that produced the detection is named — *"Axis camera analytics asserted this"*. That is a categorically different statement from *"our model said so"*, and it is the difference between an audit line and an inference | The provenance claim cannot be made on this site, and M2's outputs would have to say *"the VMS reported an analytics event"* instead of who computed it. **Still shipable; lose the stronger wording** |
| `data` | Whatever the engine chose to attach | Nothing to carry |

If that type group returns nothing, ask the operator **which cameras have analytics
enabled in the VMS**. A silent zero and a site with no analytics look identical from
outside, and the difference is two days of M2 work.

---

## What to bring back

Three files, and nothing that identifies anybody:

```
dist/verification/  (do not commit these - they are gitignored by design)
```

| File | Feeds |
|---|---|
| `probe-eventtypes.json` | The H1 alias table. The input the decision has been waiting for |
| `probe-analyticdata.json` | Whether M2's `analyticData` item is 2ew or 12ew |
| `probe-typegroup.json` | Whether decisions can name their own provenance |

Paste the three `/eventTypes` and `/events` `sample` objects into
`docs/vendor/milestone/handover.md` and add a row to §1 of
[`route-audit.md`](route-audit.md). **That is where a probe result stops being a
measurement and starts being evidence** — and the row is what moves `NOT-ANSWERABLE-
FROM-DOCS` to something else.

---

## Three things not to do

1. **Do not clear an `Unverified` flag.** Promotion is a separate commit to
   `routes.go`, made by a human who has read the report. A published document
   refuted five of these routes; it confirmed none, and a probe confirms a *shape*,
   never a vocabulary.
2. **Do not edit `contracts/`.** If the names turn out to need a new normalized
   event type, that is a contract change and `AGENTS.md` reserves it. Write the
   proposal; do not make the change.
3. **Do not write to the customer's system.** `--write` places a real bookmark in
   their recording, and someone has to delete it afterwards. Leave it off.

---

## If everything 404s

Flip the case before anything else. EVENTS documents `/api/rest/v1` (lowercase) and
BOOKMARKS documents `/API/rest/v1` (uppercase) — in the same product, on the same
host (route-audit.md §2.1). This adapter uses the lowercase spelling for everything
and both spellings are recorded rather than one being preferred silently. If a
production build only answers the uppercase prefix, that is a finding worth a row
in §1 and a decision about which spelling this adapter sends.

The other two answers to an all-404 probe are the boring ones: the wrong host, or a
token from the wrong XProtect installation. Both surface as `unreachable` rather
than `contradicted`, deliberately — a route nobody reached has told us nothing about
its shape, and reporting that as a contradiction sends an operator to file a vendor
bug against a typo in a config file.
