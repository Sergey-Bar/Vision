# Architecture

The whole system on one page, drawn so that the **AI plane is visibly optional** and
its **absence is a first-class state** rather than a gap in a picture.

Read the dashed boxes. Everything inside a dashed box can be absent, and when it is,
the pipeline still completes and still tells the truth — it says `not_asked`, not
`nothing happened`. That is the property the diagram exists to show, and it is why
the AI plane is drawn as a *plane* and not as a step in a line.

```
                         ONE READER, ONE CLOCK
  ┌────────────────────────────────────────────────────────────────────────────────┐
  │                                                                                │
  │   camera ──rtsp/http──┐                                                      │
  │                       ├──► ffmpeg ──(one process, one clock)──┐                │
  │   file ───────────────┘                                        │                │
  │                                                                ├──► rolling  │
  │                                                                │    recording │
  │                                                                │                │
  │                                        ┌───────────────────────┴───┐            │
  │                                        │  MJPEG pipe             │            │
  │                                        └───┬───────────────────┬───┘            │
  │                                            │ RGB frame        │ no second    │
  │                                            ▼                  │ reader       │
  │                                  ┌──────────────────┐         │              │
  │                                  │ internal/novelty │         │              │
  │                                  │ pure Go, no model│         │              │
  │                                  │ MEASURES a region│         │              │
  │                                  └────────┬─────────┘         │              │
  │                                           │                   │              │
  └───────────────────────────────────────────┼───────────────────┼──────────────┘
                                              │                   │
                        ═══════════════════════╪═══════════════════╪═══════════════
                        ║   THE DETERMINISTIC CORE — runs with the    ║
                        ║   entire AI plane absent. No code path in   ║
                        ║   these boxes can invoke a model.           ║
                        ═══════════════════════╪═══════════════════╪═══════════════
                                              │                   │
                                              ▼                   ▼
                                    ┌──────────────┐    ┌──────────────────┐
                                    │  evidence    │    │  detections      │
                                    │ content-addr.│    │  + decisions     │
                                    │ path FROM    │    │  score, threshold│
                                    │ the sha256   │    │  region=measured │
                                    └──────┬───────┘    └────────┬─────────┘
                                           │                     │
                                           └──────────┬──────────┘
                                                      │
                        ┌─────────────────────────────┼──────────────────────────┐
                        │                             │                          │
   ═════════════════════╪═════════════════════════════╪══════════════════════════╪══════
   ║   THE AI PLANE — OPTIONAL. Every box below may be absent, and every one of   ║
   ║   them has a recorded absence that is NOT a negative answer.                 ║
   ═════════════════════╪═════════════════════════════╪══════════════════════════╪══════
                        │                             │                          │
             ┌──────────▼──────────┐        ┌─────────▼─────────┐                │
             │ internal/vlm        │        │  clip → clip      │                │
             │ ONE call per event  │        │  upload → DELETE  │                │
             └──────────┬──────────┘        └─────────┬─────────┘                │
                        │                             │                          │
             ┌──────────▼──────────┐                  │                          │
             │ vlm_observations    │                  │                          │
             │ summary, verdict,   │                  │                          │
             │ aistate, output_    │                  │                          │
             │ hash, model_id,     │                  │                          │
             │ contract_version    │                  │                          │
             └──────────┬──────────┘                  │                          │
                        │                             │                          │
   ═════════════════════╪═════════════════════════════╪══════════════════════════╪══════
   ║   BACK TO THE CORE — what comes back is a STORED VALUE, not a live handle    ║
   ═════════════════════╪═════════════════════════════╪══════════════════════════╪══════
                        │                             │                          │
                        └─────────────┬───────────────┘                          │
                                      ▼                                          │
                        ┌──────────────────────────┐                            │
                        │ outbox → XProtect        │                            │
                        │ event + bookmark,        │                            │
                        │ verified by READ-BACK    │                            │
                        └────────────┬─────────────┘                            │
                                     ▼                                          │
                        ┌──────────────────────────┐                            │
                        │ ledger (hash-chained)    │                            │
                        │ + ed25519 seal           │                            │
                        └──────────────────────────┘                            │
                        └────────────────────────────────────────────────────────┘
                                                     │
                                                     ▼
                        ┌────────────────────────────────────────────────────────┐
                        │  READ-ONLY SURFACES — no observer, no client,          │
                        │  so no handle by which they could reach a provider     │
                        ├────────────────────────────────────────────────────────┤
                        │  pocctl prove    recompute from stored facts           │
                        │  pocctl search   what the model SAID + its attribution │
                        │  pocctl brief    one row per hour, including empty     │
                        │  pocctl report   regenerated from the store            │
                        │  pocctl audit    recompute the chain                  │
                        │  pocctl mark     one human verdict                     │
                        └────────────────────────────────────────────────────────┘
```

## The four absence states, and where each is produced

This is the table the whole diagram exists to make legible. A missing answer is never
a negative answer, and the four absences have four different causes with four
different owners.

```
no_objects    the model ran and the frame was empty     → a NEGATIVE answer.
              THE ONLY state in which "the model found nothing" is a statement
              about the scene.                                       internal/ai

degraded      asked, and it failed / was late / malformed → NOT a negative answer.
              A retry is owed.                                   internal/ai, internal/vlm

not_asked     never sent — backpressure, a busy worker, a gap → NOT a negative
              answer. The detector proposed it and nobody       internal/detect,
              looked at it.                                      internal/soak

cannot_tell   structurally valid and not interpretable      → NOT a negative answer.
              A POSITIVE claim that the frame did not settle it.  internal/ai
```

Two refusals keep them from collapsing, and both are in `scripts/verify.ps1` as red
checks — the code is changed and the suite must go red:

| refusal | the lie it removes |
|---|---|
| `internal/vlm/parse.go` clears a verdict on an unasked ask | "nothing happened", printed because *we* could not ask |
| `internal/store/observations.go` refuses to write one | the same lie, by a second writer |
| `cmd/pocctl/search.go` refuses to call a window searchable when any proposal was unasked or degraded | "no matches", printed for footage the model never saw |
| `internal/novelty` refuses `unchanged` while no reference is pinned | "the camera is fine", asserted about a camera that was never compared with anything |

## Why the AI plane is a plane and not a step

Three reasons, in order of how much they cost to learn the hard way:

1. **A hung inference must not hang ingest.** The region measurer, the clip cut, the
   push and the ledger are all inside the core box, and none of them can reach a
   model. A provider that stops answering costs you the `degraded` state and nothing
   else — the recording keeps rolling, the crossing keeps being detected, the
   bookmark is still written with "the model was not asked and why".
2. **Replay has to mean something.** `pocctl prove` recomputes a decision from stored
   facts with **provider calls 0**, and that number is *measured*, not asserted. It
   is possible because the replay function is handed no observer and no client, which
   is a structural property of its signature rather than a discipline its author
   maintained.
3. **A missing answer is not a negative answer.** If the AI plane were a step, its
   absence would be a crash or a default, and a default here is a fabricated negative
   about a camera. Every row of the table above exists because that sentence is the
   most expensive one this product can print.

## What is NOT in this diagram, deliberately

| absent | why |
|---|---|
| a second model provider | `soak.Observer` is two methods. When a GPU appears, that is the whole integration surface. An unrun provider earns no credit and costs a boundary row. |
| embeddings / a vector index | a similarity-ranked answer is **not replayable from stored facts**. Search is a substring over a stored column, and `pocctl search` says so on every run. |
| a shift-summary model call | `MaxTokens` is 256. A brief is a rendered view of stored columns, so it cannot be fabricated and needs no tokens. |
| a person model | VSS `Person` implies identity and a track. This module has `track_id` within one run and nothing else. `docs/vss-mapping.md` is where that is written out. |
| CI | this repository has no pipeline; `poc/active-monitoring/scripts/verify.ps1` is the gate and it runs locally. A green branch is a human claim. |