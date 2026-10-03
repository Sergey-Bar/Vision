# `pocctl footage` - red-check record

Every control this branch still owns, and what it cost to have shipped. The raw
transcripts are in `footage_redcheck_transcripts.txt`, produced by
`run_footage_redcheck.ps1` on this machine. Each entry was run by hand: the control was
removed, the one test that guards it was run, the failure was recorded, the file was
restored, the restore was proved by SHA-256, and the test was re-run green.

Six controls, six reds, six restores. `problems: 0` at the foot of the transcript file.

## What moved upstream with the merge

This branch originally carried twelve. Six of them guarded code that `contract` now owns,
and they went with the code. Keeping either would have meant keeping a duplicate, which is
the defect the merge fixed.

| Was | Now guarded by |
|---|---|
| crop-to-frame geometry (`composeBox`, `cropPixels`, `cropBoundsOf`) | `internal/ortdetect/crop.go` + `crop_test.go`. Deleted from this branch; the crop pass is now one `Detect(..., rect)` call and this package holds no arithmetic of its own. `TestTheCropGeometryIsNotReimplementedHere` fails on a second spelling. |
| per-class partition | `internal/bench/perclass.go` + `perclass_test.go` (`ClassCounts`, `FoldClassCounts`). Deleted from this branch; both settings now go through `bench.Measure`. |
| gate: no provenance row | `footage.go` + upstream's `TestMaterialWithNoRowAtAllIsRefused` |
| gate: ambiguous base name | `footage.go` + upstream's `TestAnAmbiguousLookupIsRefusedRatherThanGuessed` |
| gate: absent clearance | `footage.go` + upstream's `TestAnAbsentClearedFlagIsRefused` |
| gate: over-wide interval | `footage.go` + upstream's `TestAWindowTooSparseToSpeakAboutTheCaseIsRefused` |

## The six that remain

| RC | Control removed | Guard | Cost had it shipped |
|---|---|---|---|
| RC-31 | the clamp in `padThenClamp` | `TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame` | A region at the frame edge records a pad that fell outside it. `Rect.Clamp` then disagrees with the rectangle we recorded, and every box the detector maps back onto the frame is placed against a crop it never saw. The red run printed `-32,270 192x108`, outside a 1280x720 frame. |
| RC-32 | the `coveredByAny` drop in `cropRegions` | `TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain` | Novelty returns several components per moving object. Measuring a component already inside an accepted one counts one object as several true positives, and the crop pass reports a recall it did not earn. The red run kept 2 regions where 1 belongs. |
| RC-35b | the PREFIX test on `NOT RECORDED` | `TestALicenceReadingNotRecordedIsRefusedOnItsOwnStopsFiring` | **The one place this branch deliberately changed an upstream line.** Upstream's gate tested `upper(licence) == "NOT RECORDED"`, which does not match a sentence, and the committed row reads "NOT RECORDED, and it is not recoverable from this file". It passes today only because that same row also records `cleared_for_external_use: false`, so a second check catches it by accident - and upstream's own test asserts the refusal mentions `cleared_for_external_use`, which is why it never noticed. A row whose licence says nothing was recorded while its clearance field says yes would be measured and quoted. The red run accepted exactly that row. The same file's `summariseLicence` already used a prefix test, which is what makes the equality an oversight rather than a decision. |
| RC-39 | `renderNotMeasured`'s refusal arm | `TestAMeasurementOverRefusalsRendersNotRunAndNeverZero` | The section goes quiet about the one thing it exists to say. On this export that arm IS the result: 20 of 20 inferences refuse, so a run of refusals would otherwise read like a run of zeros. |
| RC-40 | `dataRow`'s `notRun` default | `TestARateWithNoDenominatorPrintsNotRunAndNeverZero` | A recall of zero over no drawn boxes claims nothing was found when nothing was there to find. `0.000` is the most reassuring thing a table can print and the one most likely to be wrong. The red run turned the kite row's `NOT RUN` recall into `0.000`. |
| RC-41 | `refuseTrailingFrame`'s count test | `TestTrailingBytesAreRefusedRatherThanDropped` | The last sample is silently dropped and the sample count is a lie, or a half frame is measured and the number describes a picture nobody decoded. |

## Not red-checked, and why

**The crop pass's call site.** `measureTwoPasses` calls
`det.Detect(seq, rgb, w, h, r)` for a crop and `det.Detect(seq, rgb, w, h, ortdetect.Rect{})`
for the whole frame. Passing the zero Rect in both places would make the crop pass silently
BE the whole-frame pass, and the resulting report would look like a crop pass that found
nothing extra - which is one of the two honest results this command can produce, so the
defect would be invisible in the output.

It is not red-checked because it is not reachable: it needs a decoder that answers, and
every export this build decodes is fused while the shipped one is anchored and refused.
`TestTheCropPassProposesAndSlicesRealRegions` gets as close as is possible without one - it
runs the whole path over a real clip, asserts every proposed rectangle is non-empty, inside
the frame, and unchanged by `Rect.Clamp` - but it cannot observe the argument that reaches
`Detect`. A source-level assertion would be a grep, and a grep is not a control.

The honest fix, when a fused export exists: assert that a crop pass over material with a
known small object returns at least one box the whole-frame pass does not, or returns
nothing at all, and that the two are never silently identical. That is a test to write when
there is something to run, and writing it now would be writing a test that cannot fail.

**The crop POLICY's duplication with `internal/soak`.** `internal/soak/crop.go` carries the
same pad, area floor, dedupe threshold and containment test, and its entry point
`proposeCrops(f frameSource, ...)` takes an UNEXPORTED type, so a `main` package cannot
call it and there is no exported wrapper. This is a genuine second implementation and it is
labelled as one in `footage_crop.go`'s header rather than described as a copy. It is not a
near-duplicate - the soak policy also gates against the whole-frame detections, applies a
novelty rate gate and a camera-view arbitration, none of which belong in a measurement
comparing two passes over the same frames. The fix is to export `soak.CropPolicy` with a
`Propose` method over exported types and delete these four functions, which is a change to
`internal/soak` that this work may not make. Nothing to remove and watch go red; the claim
is an omission with a stated cause.

**The declared-sample-count half-open window.** `int(win.For/win.Every)` is asserted from
both sides against real ffmpeg output by `TestAWindowIsSampledFrameByFrameOverRealMaterial`,
and the mismatch row it feeds is asserted by the same test. A third assertion would restate
them.

## What is NOT red-checked because there is nothing to remove

The anchored-export refusal itself. `internal/ortdetect` owns it, it is not a file this
work may edit, and it is exercised end to end by
`TestTheAnchoredExportIsRefusedByNameAndTheRatesAreNotRun` against the real weights on this
machine. That test asserts five things in the order the output makes them - frames really
read, the session really opened, the decoder refused by name with its output count, BOTH
settings reported a refusal separately, and the rates are NOT RUN with no zero anywhere.
