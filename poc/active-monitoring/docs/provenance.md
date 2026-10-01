# Provenance of the committed video material

Which file came from where, and why the two of them are treated differently. The
machine-readable version of this is `testdata/labels.json`; this document is the
reasoning, and it exists because the reasoning is the part a reader cannot verify
from a JSON field.

## The rule

> A licence field is filled from what is known about the **material**, never from what
> would be convenient to claim.

That rule is what closed `car-crossing.mp4` and what keeps `youtube/sTF-6_xinUU.mp4`
shut, in one pass, with no special pleading for either.

## `car-crossing.mp4` — cleared, 2026-10-01

| field | value | why |
|---|---|---|
| provenance | `synthetic` | corrected from `real`: a measured run pointed a COCO model at this clip at three instants and it returned **zero boxes every time**. A flat coloured rectangle on a `testsrc` background is not a photograph. |
| licence | the same licence as this repository | there is **no rights holder to ask**. No third-party footage, no downloaded source, no copyrighted asset is inside the file. |
| attribution | **none required** | `NOT APPLICABLE`, recorded as a value rather than left as `REPLACE-ME` because nobody was certain. The licence imposes no attribution condition, so the honest entry is that there is nothing to attribute. |
| retrieved | 2026-09-30 | the date the file was written, which is what `retrieved` means for a locally generated clip. |
| contains_people | `false` | a `testsrc` pattern has no people in it. The only moving object is an `ffmpeg` fill. |
| sha256 | `E60759B6…88F2` | recorded so the label describes **this** file rather than a description of a file. |

The build is a `drawbox` over `testsrc`, recorded on this machine. Nothing was
downloaded, which is the entire fact that makes the licence answer possible.

### What closing the licence did NOT do

It did not make the material evidence about the real world. `car-crossing.mp4` is a
rectangle, a COCO model correctly sees nothing in it, and a run over it still
produces zero detections and `NOT RUN` on AC2–AC4.

**The licence hole and the real-motion hole are two different holes**, and this
change closed one of them. `docs/status.md` carries the other as its own row.

## `youtube/sTF-6_xinUU.mp4` — not cleared, and not recoverable from this file

| field | value |
|---|---|
| provenance | `real` |
| licence | **NOT RECORDED, and not recoverable from this file** |
| cleared_for_external_use | `false` |
| contains_people | `unknown` — no pedestrian observed in the frames examined; no claim that none is present |

The rights holder is the camera operator, a public body. The uploader is not the
rights holder. Neither has been asked, and this repository cannot ask on somebody's
behalf.

The file is gitignored, not committed, and **may be read on the machine that has it
and may not be shown to anyone else** — not in a demonstration, not in a screenshot,
not in a submission.

`cleared_for_external_use` is an explicit boolean, and it was added for one reason:
"the licence is unknown" and "the licence is known and permissive" are different
answers, and a reader should not have to infer which one this is from prose.

### Why it is here at all, given that it cannot be shown

It exposed two real bugs the committed synthetic material never could — a decoder rank
bug and a logit bug — and it remains the only **real** footage this project has ever
run against. That is worth a gitignored file nobody may show.

### What would clear it

Only the camera operator. Not the uploader, not this repository, and not an AI agent.

The substitute is `testdata/street.mp4`: 60–90 seconds of **daylight** footage, phone
fixed or on a tripod, one real car or pedestrian crossing a horizontal line, 1080p.
Recorded by whoever owns the phone, with no third-party rights at all. That is a
person with a phone, and it is the only real-motion material the submission will have.

## The file the loop actually plays

`scripts/make-loop.sh` assembles `car-crossing.mp4` and `bird-synthetic.mp4` into a
single self-contained `loop.mp4`, because a concat list `ffmpeg` re-reads every loop
is one more thing that can be wrong at hour three of hour one hundred and sixty-eight.

Both inputs are now licence-clean. Both are synthetic. **Neither is evidence that the
detector sees anything**, and the report prints `provenance: synthetic` beside every
number derived from either.