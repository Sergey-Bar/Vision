# contracts/hafnia

**Status: ASSERTED AGAINST PUBLISHED DOCUMENTATION, NOT AGAINST A LIVE CALL.**
Retrieved 2026-09-30 from `hafnia.readme.io/docs/vlm-api`.

That sentence is the most important one in this file. `internal/vlm` is built
against what the provider *publishes*, and four of its assertions are marked
`ASSERTED` below rather than `VERIFIED`. The difference is the whole point of this
directory: a claim that has been read in a document and a claim that has been seen
on the wire look identical in a report and are not the same claim.

A live call settles the open questions. `pocctl vlm-probe` makes one and records
the raw response here. Until then, `pocctl preflight` prints this status, not a
green tick.

## What the client assumes

| # | Assumption | Status | If it is wrong |
|---|---|---|---|
| 1 | Asset upload is `POST /api/v1/assets`, `multipart/form-data`, a `file` part plus an optional `name`. | ASSERTED | 404 from a gateway whose body does not name the missing prefix |
| 2 | Completion is `POST /api/v1/chat/completions`. | ASSERTED | as above |
| 3 | Message content is a **typed array**, with `{"type":"text","text":…}` and `{"type":"asset_id","asset_id":…}`. | ASSERTED | a 400 whose body does not name the field |
| 4 | Auth is `Authorization: ApiKey <key>`. | ASSERTED | 401 on every call while the endpoint keeps answering |
| 5 | Assets are deleted with `DELETE /api/v1/assets/{id}`. | ASSERTED | clips are never removed from a third party's disk |
| 6 | The region host is `api.<region>.hafnia.milestonesys.com`, `eu` or `us`. | **CONTESTED** | footage leaves for the wrong jurisdiction |
| 7 | The API documents **no response schema** and **no region or bounding-box output** — it returns text. | ASSERTED | see below; this is the assumption the whole architecture rests on |
| 8 | Input is mp4, ≤ 100 MB, ≤ 30 s recommended. | ASSERTED | an upload refused at the far end |

Assumptions 1–5 are the four things the previous client got wrong silently. They
are listed as ASSUMED rather than as obvious because each of them fails in a way
that costs an afternoon: a wrong path, a wrong auth scheme and a wrong content
shape all produce "the provider is down".

### Assumption 6 is contested, and the contest is recorded

The published documentation carries a **historical disagreement about the EU host**:

- an earlier revision published `api.hafnia.milestonesys.com` — no `eu.` label
- the 2026-09-08 revision publishes `api.eu.hafnia.milestonesys.com`

Both spellings are recorded. `internal/vlm/regionHosts` carries only the newer one;
`supersededEUHost` carries the older one and a test asserts it is **absent** from
the map, so it cannot creep back in.

That is the right handling of a contradiction in somebody else's documentation:
name both, pick one, keep the loser as a named negative. Deleting the losing host
would leave no evidence that it was tried; keeping both in the map would leave two
destinations for a site's footage and no way to know which one a run used.

**One live call settles it.** Until then, a run against `SP_POC_VLM_REGION=eu`
prints the host it will send footage to on every banner and on every truth line,
so an operator can see which name this build dialled.

## Assumption 7 is the architecture

The API returns **text**. It documents:

- no structured output schema
- no bounding box
- no region

Everything else in this build follows from that:

| Consequence | Where it lives |
|---|---|
| The region on a pushed XProtect event is **ours and measured**, from pure-Go arithmetic on pixels the process already had | `internal/novelty` |
| The model is asked what is happening **in** the region and is **never asked for coordinates** | `internal/vlm/prompts/` |
| The label on the box is the model's and is **attributed as a claim** | `internal/store/vlm_observations` |
| There is **no synthetic model hash**, ever | `internal/store/observations.go` |

If assumption 7 is wrong — if the API does answer with a structured schema and a
region — then `internal/vlm/prompts/` is asking for less than it could and the
measured box is ours when it need not be. That is a *cheaper* failure than the
opposite: the product still works, it has simply left accuracy on the table. The
architecture is chosen for that asymmetry deliberately.

## Attribution: what there is and what there is not

A hosted model has no weights to hash. `internal/vlm` therefore reports:

- `model_identity` — what the API **said**, stored as a claim by the provider
- `contract_version` — a content hash **over everything this build controls**: the
  prompt, the prompt's revision, the output schema version, the API model name and
  the region
- `temperature` — what was asked for
- `output_hash` — the content hash of the validated four-field output

There is deliberately **no `model_hash` column** and there never will be one. A
synthetic hash would satisfy a column check while defeating the only thing the
column is for.

## Egress

| Rule | Where | Test |
|---|---|---|
| Only the pinned host is reachable; the host map is the allow-list | `internal/vlm/vlm.go` | `TestRegionPinningIsRefusedInAllThreeDirections` |
| No redirect is followed, from anywhere | `internal/vlm/http.go` | `TestTheClientRefusesToFollowARedirect` |
| TLS floor is 1.2, verified on | `internal/vlm/http.go` | `TestTheTLSFloorIsTwelve` |
| No failover between regions | there is no code path that tries the other host | RC-7 |
| The daily cap is consulted **before** the upload | `internal/vlm/observe.go` | `TestTheBudgetIsReservedBeforeAnythingLeavesTheSite` |
| The asset is deleted on every path after the upload, on a context that survives the caller | `internal/vlm/observe.go` | `TestTheAssetIsDeletedOnEveryPathAfterTheUpload` |
| An asset left behind by a killed run is swept by the next one | `internal/vlm/wire.go` | `TestSweepAssetsClearsWhatAnInterruptedRunLeftBehind` |

## Verdict vocabulary

Closed set of four, in `internal/vlm/parse.go`:

`confirmed` · `false_positive` · `needs_review` · `none`

`none` is a real member rather than an empty string, because a verdict with no
value and a verdict that is missing have to be distinguishable — the second is the
fabricated negative, which is the failure this whole package exists to prevent.

The parser requires **all four** fields, refuses unknown ones
(`DisallowUnknownFields`), requires `confidence` in `[0,1]`, refuses more than one
choice, and treats prose in the content slot as `cannot_tell`. A partial parse is
never returned.

## Prompts

Two classes, two files, `internal/vlm/prompts/`:

| id | file | asks |
|---|---|---|
| `alarm` | `alarm.v1.txt` | whether there is a real incident in the measured region |
| `camera_integrity` | `camera_integrity.v1.txt` | **which** of `moved` / `covered` / `obscured_scene` / `unchanged`, because the pixels can only say *that* the view changed |

A third class is a file plus one manifest entry. It is not a code change, and
`TestTwoIncidentClassesAreTwoPromptFilesAndNothingElse` is what says so.

## How to settle the open questions

```powershell
# One live call. Records the raw response into contracts/hafnia/raw/ and prints
# which host answered, which model the API claims, and what it actually returned.
pocctl vlm-probe

# Then set the status in this file by hand and record what changed:
#   ASSERTED -> VERIFIED, with the date and the raw response beside it.
```

Until `raw/` is non-empty, every row in the table above that says ASSERTED is a
claim about a document, and this build's preflight says so out loud.