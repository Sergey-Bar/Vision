# The absence panel on the operator screen

`pocctl search` refuses to say "no matches" for a window the model was never asked
about. This records the same refusal on the surface an operator actually watches
during a run, the controls behind it, and the transcripts of each control being
removed and watched go red.

## Which surface, and why

`internal/ui`'s live page at `/`, in the right-hand column, first panel. It is the
only surface that renders the run's live status while the run is going:

- `internal/overlay` is drawn onto the picture, into a 22-pixel header and a
  22-pixel footer on a 640x384 frame. Four counts with four causes do not fit there,
  and rule 2 says a panel, not a footer. The picture's own note line already
  carries the suppression count; adding a second block of text to the pixels is the
  thing this document argues against.
- `internal/labelui` is the box-marking tool a reviewer drives AFTER a run. It
  renders `Proposed`, `Marked`, `Accepted`, `Rejected` and `Unmarked` and nothing
  about what the model was asked. It is a keyboard-and-mouse judgement surface for
  a false-positive rate, not a live status surface.
- The live page had exactly one sentence about the model, `ai state`, and it is
  about the LAST FRAME. Nothing on it said whether the run's other proposals were
  ever sent. A run whose verifier never got a key showed a green `asked_answered`
  beside a picture nobody asked about.

## What it says

Both renderings below are the panel's body with the markup removed. The totals line
is byte-identical to `printAbsence` in `cmd/pocctl/search.go`, which is the point:
`TestTheScreenAndTheCommandSayTheSameThingsAboutTheSameWindow` runs both surfaces
over one store and fails if the words drift apart.

### A window with everything asked

```
6 proposal(s), asked and answered 6 (4 with no objects, 0 degraded, 0 not asked, 2 cannot tell)

no_objects        4   asked and answered, with no object present in the frame. This IS a negative answer
degraded          0   asked, and failed, late or malformed. A missing answer here is NOT a negative answer
not_asked         0   the detector proposed them and the verifier never saw them
cannot_tell       2   answered with insufficient evidence or needs_review, which is a positive claim that the frame did not settle it

pocctl search --data-root "<the folder holding poc.db>" --run <run id> --query <text> - the answers themselves,
each one carrying the recorded window it is about: the clip reference, its exact bounds and its sha256.
There is no continuous video file to pull instead, because none is written.
```

No notice. A run whose every proposal was answered can carry the sentence "the model
found nothing about X", so there is nothing to qualify, and a notice that fires
always is a notice nobody reads.

### A window with unasked proposals

```
12 proposal(s), asked and answered 9 (4 with no objects, 1 degraded, 2 not asked, 5 cannot tell)

no_objects        4   asked and answered, with no object present in the frame. This IS a negative answer
degraded          1   asked, and failed, late or malformed. A missing answer here is NOT a negative answer
not_asked         2   the detector proposed them and the verifier never saw them
cannot_tell       5   answered with insufficient evidence or needs_review, which is a positive claim that the frame did not settle it

THIS RUN IS NOT FULLY SEARCHABLE. 3 of the 12 proposals in it were never answered (2 not asked, 1 degraded).
An answer that is absent here is not evidence the model saw nothing: the run did not ask, and that is not the
same as the model having answered 'no'.

pocctl search --data-root "<the folder holding poc.db>" --run <run id> --query <text> - the answers themselves,
each one carrying the recorded window it is about: the clip reference, its exact bounds and its sha256.
There is no continuous video file to pull instead, because none is written.
```

### Three proposals and nobody asked

The two causes that ARE counts, and only those two. `no_objects` and `cannot_tell`
are counts over a set with no members, and a zero beside either of them reads as a
claim about a provider nobody called.

```
the model was asked about NOTHING: 3 proposal(s), 0 answers (not_asked 2, degraded 1)
the run proposed detections and consulted nobody about them

degraded          1   asked, and failed, late or malformed. A missing answer here is NOT a negative answer
not_asked         2   the detector proposed them and the verifier never saw them

THIS RUN IS NOT FULLY SEARCHABLE. 3 of the 3 proposals in it were never answered (2 not asked, 1 degraded).
...
```

### Nothing proposed at all

A sentence, and no table. Zero proposals is a real measurement, but it is a
measurement about the detector and there is no row for any cause to be counted
against.

```
there were NO detections at all in this run so far, so there was nothing to ask about (not_asked).
This is a statement about the detector, not about the model
```

## The controls, each removed and watched go red

Nine mutations, run by hand in this worktree. `REDCHECK` marks the mutated line.

| # | Control | Mutated line | Test | Observed |
|---|---|---|---|---|
| 1 | An unread breakdown is no data, not four zeros | `if !d.AbsenceKnown {` -> `if false {` in `absencePanel` | `TestAnUnreadableBreakdownSaysNoDataAndNotFourZeros` | `an unread breakdown did not say so` |
| 2 | An empty window is a sentence, not a table of zeros | `case a.Proposed == 0:` -> `case false:` | `TestAnEmptyWindowShowsTheDetectorSentenceAndNotFourZeros` | `a window with no proposals does not say so`, and the panel rendered `degraded 0` / `not_asked 0` under a window with no detections |
| 3 | Only the asked causes are counted | `if a.Answered > 0 {` -> `if true {` in `askableCauses` | `TestAWindowNobodyWasAskedAboutPrintsNoCountForTheAskedCauses` | `the panel counted "no_objects" against a window with no asked rows` |
| 4 | A run that was never fully asked says so | `if !a.Unsearchable() {` -> `if true { return "" }` in `absenceNotice` | `TestAWindowThatWasNeverFullyAnsweredSaysSoOnTheScreen` | `a run with two unasked proposals printed no qualification` |
| 5 | The panel reads the window through `SearchObservations` | `d.Absence, d.AbsenceKnown = sum, true` -> `= sum, false` in `Gather` | `TestTheScreenReadsTheWindowThroughTheSearchCall` | `the screen gathered no absence breakdown at all` |
| 6 | `Answered` counts answers, not rows that have an observation | `SUM(CASE WHEN v.aistate=? ...)` -> `SUM(CASE WHEN v.aistate<>? ...)` in `searchSummary` | `TestThePartitionIsExhaustive` | `sum = {Proposed:4 Answered:1 ...}` |
| 7 | A detection with no stored clip does not take the search down | `var tb, te string` -> `var tbeg, tend sql.NullString` | `TestADetectionWithNoStoredClipDoesNotTakeTheWholeSearchDown` | `an answer with no stored clip took the whole search down: sql: Scan error on column index 14, name "time_begin": converting NULL to string is unsupported` |
| 8 | A nil read-back cannot take the screen down | `if !st.ReadBack { continue }` removed from `VerifyStats` | `TestARunWithAStoredClipStillRendersAndSaysItDidNotVerify` | `panic: runtime error: invalid memory address or nil pointer dereference` |
| 9 | No percentage and no ranking on this surface | a percentage ADDED to `absencePointer` (this control is the absence of a thing, so it is falsified by committing the violation) | `TestThePanelNeverPrintsAPercentageOrARanking` | `the panel printed a percentage for {Proposed:0 ...}` |

## Three defects found on the way, all fixed here

They are recorded because each was a live-surface defect that a panel depending on
the store would have inherited silently.

1. **`SearchObservations` could not survive a detection with no evidence row.** The
   join is a LEFT JOIN and two of the four evidence columns were scanned as
   nullable while two were not, so one NULL `time_begin` failed the whole query --
   including the window summary beside it. `pocctl search` and the screen both
   reported nothing at all for a run that contained an answer without a stored clip.
   Control 7.

2. **`Answered` counted every observation row, including a `degraded` one.** A
   window of three proposals with one answer, one lost ask and one never sent
   reported "asked and answered 2", and `cannot_tell` -- the residue of `answered` --
   reported the lost ask as a positive claim that the frame did not settle it. That
   is exactly the collapse rule 2 exists to prevent, in the shared partition both
   surfaces read. The partition is now exhaustive and checkable:
   `Proposed = Answered + Degraded + NotAsked`. Control 6.

3. **The live page panicked the first time a run stored a clip.** `Gather` calls
   `VerifyStats(ctx, runID, nil)` -- the screen does not re-hash a run's clips on
   every page render -- and `VerifyStats` called that nil unconditionally. So the
   first crossing of any real run killed the page. `EvidenceStats.ReadBack` now
   says whether a verification was measured at all, and the KPI renders `NOT RUN`
   rather than a `0` that reads as "every clip failed to verify". Control 8.

## Left out of scope, and why

- **`pocctl unsearchable` is not named on the panel.** It does not exist in this
  tree: `cmd/pocctl/main.go` dispatches `search`, `brief`, `report` and the rest,
  and there is no `unsearchable` case and no `store.UnsearchableWindows`. The panel
  names `pocctl search`, which does exist and which `TestThePanelPointsAtThe
  RecordedWindowAndNotAVideoFile` pins. Naming a command that does not exist is
  the exact class of defect this panel exists to refuse, so it was not named. When
  that command lands, the pointer is one string in `absencePointer`.
- **The burned-in picture carries no absence panel.** A 22-pixel footer on a 640x384
  frame is the wrong place for four counts with four causes, and the operator's
  question is answered on the page above it. Nothing was removed from the picture.
- **No per-window list of unsearchable stretches on the screen.** A run is one
  window here, so one notice is the whole of it. A per-hour or per-stretch list is
  `pocctl brief`'s hour table, and duplicating it on the page would be a second
  answer to one question.
- **No relevance or similarity ordering anywhere.** There are no embeddings in this
  project and rule 4 is that this is a decision. The panel orders nothing; the
  four causes are in CONTRACT.md 2.2's order because that is the vocabulary, not a
  ranking.
- **`scripts/verify.ps1` was not edited.** Its red-check table is another agent's
  file. The nine mutations above are the transcripts; when the table is next
  extended, controls 1-5 and 9 belong in it as single-line mutations and 6-8 belong
  in `internal/store`.