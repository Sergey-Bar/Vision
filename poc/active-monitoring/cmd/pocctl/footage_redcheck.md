# `pocctl footage` - red-check record

Every control the footage command adds, and what it cost to have shipped. The raw
transcripts are in `footage_redcheck_transcripts.txt`, produced by
`run_footage_redcheck.ps1` on this machine. Each entry was run by hand: the control
was removed, the one test that guards it was run, the failure was recorded, the
file was restored, the restore was proved by SHA-256, and the test was re-run green.

Twelve controls, twelve reds, twelve restores. `problems: 0` at the foot of the
transcript file.

| RC | Control removed | Guard | Cost had it shipped |
|---|---|---|---|
| RC-31 | `padThenClamp`'s `return clampRegion(padded)` | `TestPadThenClampKeepsAnEdgeRegionsPadInsideTheFrame` | A region at the frame edge records a pad that fell outside it. The recorded geometry is wider than the pixels measured, and every box composed onto the frame is placed against a crop the model never saw. The red run printed `{X1:-0.025 ...}`. |
| RC-32 | the `coveredByAny` drop in `cropRegions` | `TestARegionAlreadyCoveredIsDroppedRatherThanMeasuredAgain` | Novelty returns several components per moving object. Measuring a component already inside an accepted region counts one object as several true positives, and the crop pass reports a recall it did not earn. The red run kept 2 regions where 1 belongs. |
| RC-33 | the `+ float64(b.X0)` origin in `composeBox` | `TestComposingACropBoxIsTheInverseOfSlicingTheRegion` | Every crop box lands at the crop's own origin, scaled against the frame: the right size in the wrong place. On a per-class table that reads as a MISS, and a miss is a thing an operator believes. The red run composed a full-crop box to `X1:0` where the crop began at `0.5`. |
| RC-34 | the per-class partition in `partitionByClass` | `TestAMisnamedDetectionIsAFalsePositiveNotATruePositive` | `bench.Measure` pairs by geometry alone, so a `car` detection on a `person` box becomes one true positive. The per-class table then reports a detector that calls everything a car as its BEST row. The red run produced an all-zero row instead. |
| RC-35 | `resolveLabel`'s zero-match refusal | `TestTheGateRefusesMaterialWithNoProvenanceRow` | Material with nothing written about it is measured. The refusal that would have caught it then names the wrong field, so an operator fills in a licence for a file nobody has written anything about. |
| RC-35b | the PREFIX test on `NOT RECORDED` | `TestTheGateRefusesALicenceThatBeginsNotRecorded` | The one row in `testdata/labels.json` that matters reads `NOT RECORDED, and it is not recoverable from this file`. An equality test waves the single most important row in the table through a gate whose entire purpose is to stop it, and its numbers may be quoted. |
| RC-36 | the `case c.Cleared == nil:` arm | `TestTheGateRefusesAnAbsentClearanceField` | `car-crossing.mp4` and `bird-synthetic.mp4` carry a recorded licence and no clearance field at all. A `bool` cannot tell "the row says no" from "the row never said", so both are measured and reported beside material that was cleared. The red run CRASHED on the nil deref rather than refusing, which is the same defect with less information. |
| RC-37 | the `every > maxSamplingInterval` refusal | `TestASamplingIntervalOverOneAndAHalfSecondsIsRefused` | A road sampled every three seconds returns four frames of nothing and the command reports a clean result over material a pedestrian was standing on. The most convincing kind of wrong number this module can print. |
| RC-38 | `resolveLabel`'s ambiguity refusal | `TestTheGateRefusesAnAmbiguousBaseName` | Two provenance rows share a base name and carry different licences. Picking the first measures material under terms that were never granted, and nothing in the output says which row was used. |
| RC-39 | `renderNotMeasured`'s refusal arm | `TestAMeasurementOverRefusalsRendersNotRunAndNeverZero` | The section goes quiet about the one thing it exists to say. A run in which every inference was refused reads like a run in which nothing was found, which is the defect class this command was written to catch. |
| RC-40 | `rate`'s `if !ok` arm | `TestARateWithNoDenominatorPrintsNotRunAndNeverZero` | A precision of `0.000` with no detections, which claims every detection was wrong when there were none. `0.000` is the most reassuring thing a table can print and the one most likely to be wrong. The red run printed exactly `0.000`. |
| RC-41 | `refuseTrailingFrame`'s count test | `TestTrailingBytesAreRefusedRatherThanDropped` | The last sample is silently dropped and the sample count is a lie, or a half frame is measured and the number describes a picture that was never decoded. |

## Not red-checked, and why

**The crop-policy location decision** (footage_crop.go's header comment): the claim
is that this is ONE implementation rather than a second copy of one in
`internal/soak`. The mechanical half is checked by
`TestTheCropPolicyIsOneImplementation`, which counts definition sites across the
package and fails on a second spelling. The rest - that the policy should eventually
live in `internal/soak` - is a decision, not a control, and there is nothing to
remove and watch go red.

**The gate's own refusal ordering** (`checkLicenceGate` collects every reason rather
than returning the first): no test removes it. It is a convenience, not a control,
and a convenience with no test behind it should be described as one. What IS tested
is that all four refusals fire independently.

**`--truth` half-interval tolerance**: `TestAGroundTruthBoxTooFarFromEveryFrameIsCountedNotAttached`
covers both sides of the boundary, so removing the tolerance would move a box onto a
frame the annotator never described. Not run as a mutation: the two sides are already
asserted from both directions and a third assertion would restate them.

## What is NOT red-checked because there is nothing to remove

The anchored-export refusal itself. `internal/ortdetect` owns it, it is not a file
this work may edit, and it is exercised end to end by
`TestTheAnchoredExportIsRefusedByNameAndTheRatesAreNotRun` against the real weights
on this machine. Its guard is that test's four assertions on the output, and the
transcript of the actual run is in the report this work returned.
