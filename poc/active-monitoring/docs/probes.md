# The probes, and the four questions

`pocctl probe --live` and `pocctl vlm-probe` are how this module stops guessing. This
document is where their answers go, because an answer that lives only in a terminal
scrollback is an answer nobody finds when a judge asks.

**Status: nothing here has been run.** No API key, no XProtect instance, no camera.
Every row below is a question with a mechanism, not an answer with a result. The
moment a probe is run against a real system, this file gets a value and a date, and
that is a human writing down what the wire said.

Run them in this order. `vlm-probe` first: it is one call, it costs nothing against
the quota, and if the host is wrong everything else is wasted effort.

```powershell
$env:SP_POC_VLM_API_KEY='...'      # never a flag: a credential on a command line is
$env:SP_POC_VLM_MODEL='...'        # a credential in a shell history and a screenshot
.\bin\pocctl.exe vlm-probe --print-body
.\bin\pocctl.exe probe --live
```

## `vlm-probe` — the VMS's side

One call. No clip, no footage leaves the site, nothing is spent against the daily
cap. The raw body lands in `contracts/hafnia/raw/<UTC timestamp>-<region>-<status>.txt`
whatever happened, including a 401.

| # | question | how the probe answers it | current answer |
|---|---|---|---|
| 6 | Is the EU host `api.eu.hafnia.milestonesys.com`, or the superseded `api.hafnia.milestonesys.com`? | The host is resolved from the pinned region map and printed in the report header. A 200 from it settles the question; a DNS failure or a 404 from a gateway settles it the other way. | **NOT RUN.** The map pins `api.eu.hafnia.milestonesys.com` and keeps the loser as `supersededEUHost`, which a test asserts is absent from the map. |
| 2 | Is completion `POST /api/v1/chat/completions`? | A 200, or a 4xx whose body names the path it did not recognise. | **NOT RUN.** |
| 3 | Is message content a typed array with `{"type":"text"}` and `{"type":"asset_id"}`? | The probe sends a **text-only typed array** — no asset. A 400 naming `content` settles it. | **NOT RUN.** |
| 4 | Is auth `Authorization: ApiKey <key>`? | A 200 or a 401. There is no bearer fallback, deliberately: a fallback would make a wrong auth scheme look like a working one until the day it stopped. | **NOT RUN.** |
| 7 | Does the API report a model identity? | The report carries `model claimed:` and says `none reported` if the response had none. **No** is a real answer: it is what `model_hash` and the "hosted model, no weights" argument exist to handle. | **NOT RUN.** |
| 8 | Does an upload work, and does a full-length answer fit? | **This probe cannot answer it.** One call with no clip cannot tell you whether a 30-second upload is accepted or whether the answer is truncated at the token ceiling. That is the demo's job, and the plan is explicit that a probe must not ship a site's video to settle a question about spelling. | **NOT RUN, and deliberately out of scope for this command.** |

The eight rows and their `ASSERTED` status live in `contracts/hafnia/README.md`. That
file, not this one, is the contract; this one is the scoreboard.

## `probe --live` — the VMS's side

Read-only. There is no flag that makes it write, and a test asserts the only POST it
issues is the token exchange.

| # | question | how the probe answers it | current answer |
|---|---|---|---|
| 1 | **Live-Stream / RTSP available, or REST + archive only?** | **No REST route answers this.** The probe prints `NOT RUN` and says a person has to open the camera URL with `ffplay` and record what happened. | **NOT RUN — a person with `ffplay`.** |
| 2 | An Analytics Event Type we may push to, and its GUID? | `GET /API/rest/v1/eventTypes`, and the table prints the count and the `SP_POC_EVENT_TYPE_GUID` you would need to set. **The probe does not verify your GUID against that list** — this build pushes and reads back rather than cross-checking a configuration value, and a tool that validated its own config would be checking itself. | **NOT RUN.** |
| 3 | Camera GUIDs. Are the simulated streams 24/7? | **Not answerable by this build.** There is no published cameras route in the specification this client is written against, so the probe prints `NOT MEASURED` rather than inventing one. `SP_POC_CAMERA_GUID` is printed as whatever it is set to. | **NOT RUN — and no mechanism exists in this build.** |
| 4 | Does `GET /API/rest/v1/events?tag=equals:'…'` find our event on a real server? | Two halves. The probe queries a tag the run **never pushed**; a correct answer is an empty `data` array, and a 4xx means the filter syntax this client writes is not the syntax the server accepts. The other half is the read-back after a real push, and that is Phase 1.1. | **NOT RUN.** |

### What `reachable` does not mean

The strongest word any tool in this repository can print is `reachable`: the route
answered with the shape the specification declares. It is not `verified`.

`verified` is a row in `contracts/xprotect/contract.json` that a **human** flips,
with a date on it, after reading the evidence. A probe that could promote its own
homework would make the flag a claim the tool makes about itself, which is the thing
the flag exists to prevent. `TestAProbeNeverSaysVerified` asserts that no state in the
set contains the word.

## The five states, and why there are five

| state | meaning | next step |
|---|---|---|
| `reachable` | answered with the documented shape | read the output, then a human may promote the row |
| `contradicted` | answered, but not with that shape | **the document is wrong about this server** |
| `unauthenticated` | the route exists and refused the credentials | fix the credentials |
| `absent` | the route did not answer at all | the path is wrong, or the feature is not licensed |
| `unknown` | the host could not be reached | fix the network; every route is unknown for one reason and is printed as ONE row, not fourteen |

`unknown` was not in the plan's list of four. It was added because a failed
authentication printed as one `absent` row per route would have been fourteen wrong
claims, and `contradicted` was separated from `absent` because a 500 means something
answered — and "something answered, wrongly" and "nothing answered" have opposite
diagnoses.

## If question 1 turns out to be "archive only"

The plan makes one column conditional on it, and this is where that decision gets
made:

```
evidence_source TEXT   -- 'live_stream' | 'vms_clip'
```

added through `addedColumns` (`internal/store/store.go:546`, idempotent and
race-tolerant), printed on every truth line, and set per detection at the moment the
clip is cut.

It is **not** added yet, and deliberately: adding a column for a condition nobody has
established is a schema column no code reads, which is the same defect
`AGENTS.md` names when it drops `sites.default_locale`. The change is a schema
migration and a human's signature. Until question 1 is answered, every clip in this
module was cut by this process from a stream this process read — which is a fact
worth stating rather than a column worth having.