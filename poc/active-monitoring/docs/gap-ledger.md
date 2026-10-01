# The gap ledger

The plan's definition of done, applied literally:

> Done means every row is **built and tested**, or **blocked on a person, with the
> reason**.

So this file has two columns of truth and one of ownership, and no row is allowed to
be neither. A row that is neither is work nobody has claimed, which is how a
hackathon ends with a hole in it.

**As of 2026-10-01.** The gate that backs every "built and tested" claim below is
`poc/active-monitoring/scripts/verify.ps1`, run locally. There is no CI in this
repository; a green branch is a human claim, not an automated one.

## POC — built and tested

| gap | closed by | the test that would fail if it reopened |
|---|---|---|
| `vlm-probe` documented 3×, absent | `internal/vlm/probe.go`, `cmd/pocctl/vlmprobe.go` | `TestTheProbeRefusesToFollowARedirect`, `TestVLMProbeRefusesWithoutTheEnvironmentOnlyCredentials`, `TestWriteRawBodyLandsUnderRawAndNeverTouchesContractJSON` |
| `SP_POC_CAMERA_INTEGRITY` read outside the config schema | `internal/config/config.go` schema row; `integrityEnabled` deleted from `cmd/watch` | `TestTheCameraIntegrityTriggerIsRequiredByName`, `TestCameraIntegrityIsOnlyNamedByConfig` |
| no searchable corpus | `internal/store/search.go`, `cmd/pocctl/search.go` | `TestSearchRefusesToSayNoMatchesForAnUnaskedWindow`, `TestSearchReturnsOnlyAnsweredAlarmAsks`, `TestTheSearchPageSaysItDoesNotUseEmbeddings` |
| no shift brief | `internal/store/brief.go`, `cmd/pocctl/brief.go` | `TestTheFourHourStatesDoNotPrintTheSameSentence`, `TestTheBriefHasOneRowPerHeartbeatedHourAndNoOther`, `TestTheBriefScaffoldingIsByteStableAroundProse` |
| no CLI for ground truth | `cmd/pocctl/mark.go` | `TestMarkWritesThroughToTheStoredPrecision` |
| `probe` printed a table and dialed nothing | `internal/xprotect/probe.go`, `pocctl probe --live` | `TestTheProbeNeverWrites`, `TestTheFourStatesAreDistinguished`, `TestAFailedAuthenticationIsOneAnswerAndNotFourAbsent` |
| `internal/README.md` trap 6 factually wrong | `internal/README.md` — corrected in place, with what replaced it | none: a document. The next reader checks it |
| the novelty engine proposes regions and the tracker approves them, but **nothing crosses the line** | **an ADR and a human** — it is a detector-semantics decision, not a bug | **OPEN and fully measured.** 155 regions, 0 crossings, 234 tracks refused as `not seen long enough`. The novelty component is a leading-edge STRIP; consecutive strips touch without overlapping, so IoU is exactly 0 and a new track starts every frame. Fix A: associate on proximity — two adjacent objects then merge, which is a false negative, the worst error a security product has. Fix B: have the engine propose the object rather than the changed edge, which needs frame history it does not keep. Both are ADR-sized. Details and measurements in `docs/status.md` |
| the demo could not pass any criterion that needs a detection, evidence or a crossing | — | **CLOSED 2026-10-01.** Six stacked defects: the demo inherited the operator's environment; `SP_POC_CLASSES` could not approve the trigger's own class; the class cache was written into a value copy; the score threshold was on the wrong scale; the zone did not name the trigger's class; and every proposed region was anchored at the frame origin. Details and measurements in `docs/status.md` |
| regions never became a track, so nothing ever crossed | — | **CLOSED 2026-10-01.** The novelty component is the changed AREA — a strip — and consecutive strips touch without overlapping, so their IoU is exactly zero. Association is now overlap-or-touch, which is the tightest rule that fixes it and still refuses two separated objects |
| `AC3 evidence intact` could never pass, in any run, ever | — | **CLOSED 2026-10-01.** `cutAndStore` minted the evidence row's `detection_id` from the clip hash instead of using the detection's own id, so every write failed the foreign key and the run recorded a `clip_refused` fault that looks like ordinary behaviour |
| the gate's final red check misread a native command's stderr as a crash and reported a false red | — | **CLOSED 2026-10-01.** `*> $null` under `$ErrorActionPreference = Stop` terminated on stderr and corrupted `$LASTEXITCODE`. It now captures output and prints the failing package |
| red checks could silently `SKIP` on a reformat | `scripts/verify.ps1` prints a visible `SKIPPED`, and prints its own control count rather than a constant | reading the output |
| the gate could `SKIP` an **installed** tool | `Find-Tool` locates `mediamtx` in winget's archive folder, not only on `PATH` | a tool that IS installed printing a SKIP is a gate that has stopped describing its own coverage |
| `pocctl demo --rtsp` refused because of that same SKIP | `mediamtx` installed; the demo ran end to end on 2026-10-01 over the rtsp transport | the demo's own output, and `docs/status.md` |

## POC — blocked on a person, with the reason

| gap | blocked on | the reason, and what unblocks it |
|---|---|---|
| **Register for the hackathon** | a human, by **2026-10-08** | External, hard deadline. No code substitutes. It is the one task an AI may not do |
| **No live VLM call**; contract `asserted_not_verified`; contested EU host | a Hafnia **API key** | `pocctl vlm-probe` is built and tested and has never been run. One call, no clip, no quota. Then a **human** edits `contract.json` `status` to `verified` with the date — the command must not do it |
| **No live XProtect push** | VMS credentials and an **Analytics Event Type GUID** | `pocctl probe --live` and `pocctl demo --live` are built. Registering an event type, if none is suitable, is a human action on the VMS |
| **No Smart Client evidence** | a person with a mouse | A screen capture of the event and its bookmark on a real VMS. The push and read-back prove the event reached the **VMS**; nothing proves it reached an operator's screen |
| **No ground truth; no false-positive number** | a **person marking footage, with a named end date** | `pocctl mark` is built and tested. It is the only item on the critical path that does not parallelise, and it is the metric judges will ask about. Start the window the day the first real run is green |
| **No live camera** | a camera | `mediamtx` is **installed and the rtsp transport is measured**: `pocctl demo --rtsp` ran end to end on 2026-10-01 with the source line reading `live rtsp (relay of recorded material, NOT a camera)`. That is a relay of recorded material, not a camera, and the report says so on every page. What is still missing is an actual camera, and `ffplay` against a camera URL answers the one question no REST route can |
| **No real reference-mode detector run** | a person, a ladder, and a camera genuinely moved, covered and restored | The engine is built and RC-11 is enforced: an unpinned reference run reports `unpinned` and can never report `unchanged`. What is missing is the physical demonstration |
| **`testdata/labels.json` licence fields** | — **CLOSED for the committed material.** `car-crossing.mp4` is an `ffmpeg` `testsrc` pattern with a `drawbox` over it, so it has no rights holder to ask: the licence is this repository's own, the SHA-256 is recorded, and `attribution` is `NOT APPLICABLE`. See `docs/provenance.md` | the answer was not to invent a licence. It was to notice that a locally generated testsrc clip has no third-party rights, and to treat it differently from the YouTube clip **because the material is different**, not because one file was more convenient |
| **`youtube/*.mp4` licence** | the **camera operator**, a public body | not recoverable from this file and not recoverable by this repository. The file is gitignored, `cleared_for_external_use: false`, and may be read on this machine only |
| **No real-motion footage** | a person with a phone | `testdata/street.mp4`: 60–90 s of daylight, one real car or pedestrian crossing a line. Both committed clips are synthetic and a COCO model correctly sees nothing in either; **do not lower a threshold to fix that** |
| **No 24-hour / 7-day soak** | Phase 1 being green | The plan is explicit: do not start the soaks until the live push is proven. Starting them early spends time on an unproven path |

## Platform — already written, not this window

Recorded, not rebuilt. Re-recording them is unpaid work and each is somebody else's
decision.

| item | where it is recorded | why it is not hackathon work |
|---|---|---|
| three XProtect disagreements — wrong `/api/v1/` prefix, a bookmark body with two fields that do not exist, no event-push path at all | `poc/active-monitoring/contracts/xprotect/README.md:44`, with Milestone's published spec downloaded beside it | a **contract change plus a `contractcheck` re-baseline**, and a human signs it. The POC's own client is correct and independent |
| `ai_observations.model_hash NOT NULL` | `docs/proposals/hosted-model-attribution.md` — the SQL and the `CHECK` are both written | a **migration**, and a human signs it. This is the direct consequence of assumption 7: a hosted model has no weights to hash |
| `internal/policy` missing (P0), the P0.8 clock caller, P2.1 UDP, P4.4 dispatch-from-live-incident, P4.5 shadow run, P4.6 console, `slo.go`'s `Breaches` uncalled | the root `docs/status.md` | ordinary multi-month platform work. Listed as open, and `internal/README.md` trap 5 already says `internal/policy` does not exist so nobody goes looking |

## The two questions that would change this file

1. **Alone or a team?** Changes who runs the marking window, and that is the whole
   of the critical path's risk. A second pair of eyes is worth more here than any
   feature.
2. **Is a GPU realistically available?** NVIDIA is a partner and awards hardware.
   With one, `docs/vss-mapping.md` stops being a document and becomes a deployment,
   and the integration surface is already two methods: `soak.Observer`.

## What is deliberately not on this list

Anything not here is either done, cut, or out of scope. The plan cut: a
`scene_caption` prompt class, a `shift_summary` prompt class and its ledger kind, a
vector index, a review web page, a Dockerfile, a systemd unit, `internal/nim`, and
RC-10.

The cuts are recorded so they are not re-proposed. A new idea enters this file by
closing a row, or by a human adding one.