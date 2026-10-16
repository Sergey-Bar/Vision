# The showcase rehearsal: what running it found

Started 2026-10-15, on `showcase` (base `d8e008c7`). This is the work log for the
showcase branch: defects the build found by *running* the demo scripts and the hub,
and the machine facts that shaped what the showcase can honestly show. Claims about the
product belong in `docs/status.md`; this file is the list a commit message and a reader
can start from.

The rule the whole pass runs under: a page may not restate a number its artefact does
not carry, and a refusal is kept rather than tuned away.

## Defects found in the scripts (all fixed)

`scripts/live-demo.ps1`, found by running it on both trigger paths:

1. **The sidecar anchors path was a second spelling.** Written as
   `"$AIModel.anchors.txt"`, which for `yolov4n.onnx` names
   `yolov4n.onnx.anchors.txt` - a file that does not exist - while the Go side's
   `anchorsBeside` strips `.onnx` and appends `.anchors.txt`. Fixed to derive the path
   by the same rule the Go side uses, so the two cannot disagree again.
2. **`SP_POC_SCORE_THRESHOLD` was set on the detector branch only.** The sidecar
   preflight refused by name: `not set; without it there is no approved class
   threshold`. Hoisted above the branch.
3. **The detector bench call was missing `--trigger detector`.** The calibration
   record must say which path measured it and `WriteCalibration` refuses one that does
   not, so `pocctl bench` ran every inference and wrote nothing; preflight then refused
   two acts later on a record that had never been written. Fixed, and the act now fails
   loudly when no p99 was recorded instead of leaving the gate to discover it.
4. **Added `-AiFps` (default 5).** The throughput gate names the declared rate as the
   legitimate lever; a person on a faster box can now lower it without editing the
   script. Raising the budget would be the demo congratulating itself.
5. **The acceptance table printed its own object dump.** `"... - $a.Missing"` in
   Windows PowerShell 5.1 renders the object's `ToString()` followed by literal text;
   the console showed `@{ID=AC4; ...}.Missing` while the HTML artefact (which used
   `$()`) was correct. Fixed, and every `.ps1` in the tree was swept for the pattern.

## Defects found in the product (fixed, with a red-check)

`poc/active-monitoring`, found by the same runs:

6. **`cmd/pocctl/bench.go`: the detector timing pass never stamped the calibration's
   trigger.** The field is required on write and the sidecar pass stamps it; the
   detector pass did not, so the only command that can produce a detector calibration
   could not write one. Fixed by building the record in `detectorCalibration`, which
   stamps `soak.TriggerDetector`. New control (the 94th in the POC table,
   `the detector timing pass says which path measured it`) asserts the trigger field
   and that `WriteCalibration` accepts the record; hand-run red and green, transcripts
   in `docs/status.md`.

## Defects found in the hub's own pages (fixed)

The hub is a reading surface, so a number or a cell it prints that is not what it
claims is the same class of defect as a stale one:

7. **The root-control count read the gate's skip records as controls.** The status
   endpoint said thirty-four root controls where the table holds twenty-eight: the
   counter matched `name = '` anywhere on a line, and `$script:skips.Add([ordered]@{
   name = 'red-check'; ... })` carries the same text mid-line. The counter now
   requires the line to begin with the assignment. RC-29.
8. **Three cells printed `[object HTMLElement]`.** The gate column on `/trust` and
   the proof and artefact columns on `/census` passed an element into `el()`'s text
   argument, and `el()` sets `textContent`, so `String(element)` rendered into the
   cell. Each page now builds those cells with a `codeCell` helper, and a scan over
   every page script (`TestNoScriptPassesAnElementAsText`) keeps the shape out -
   found by looking at a screenshot, not by a test, which is why the scan covers all
   the scripts and not just the two that were fixed. RC-30.

8. **Three cells printed `[object HTMLElement]`.** The gate column on `/trust` and
   the proof and artefact columns on `/census` passed an element into `el()`'s text
   argument, and `el()` sets `textContent`, so `String(element)` rendered into the
   cell. Each page now builds those cells with a `codeCell` helper, and a scan over
   every page script (`TestNoScriptPassesAnElementAsText`) keeps the shape out -
   found by looking at a screenshot, not by a test, which is why the scan covers all
   the scripts and not just the two that were fixed. RC-30.
9. **Two panels printed a raw 404 as their content.** `/live`'s clip catalog, critical
   ranking and grounded chat are endpoints of the lab tool server under `tools/`,
   which the showcase hub does not attach, so every visit rendered
   `Clip catalog unavailable: 404 page not found` - a page showing a stack-trace
   sentence where the project's own vocabulary belongs. The three catch branches now
   say `NOT RUN` with the reason. Presentation only: no control changed, and the
   panels were never on the gate path.

## The media pack (built this pass, all from the repository's own files)

- **Video** - `dist/showcase/vision-showcase/`, rendered with `hyperframes` from a
  single seek-safe GSAP timeline: ten scenes, eighty seconds, English captions. Its
  moving pictures are real screen recordings of the hub (`record_trust.py` drives the
  tamper command in Chrome and records it) plus the module's own synthetic fixture
  (`testdata/car-crossing.mp4`, labelled synthetic on screen). No relay footage, and
  no invented frame: the two detector paths' numbers in the video are the numbers in
  the census.
- **Screens** - thirteen pages captured with Playwright (`capture_shots.py`),
  including the three states of the tamper run.
- **Diagrams** - `docs/showcase/diagrams/`: pipeline and trust chain (archify
  editions, interactive), the arithmetic of refusal and the surface map (built in the
  pack's own light style, exported with `export_c1d1.py`). Every one carries NOT RUN,
  DEGRADED and RECORDED as part of the drawing.
- **Static edition and PDF** - `build_site.py` writes `dist/showcase/site/`: the same
  content without a server, with the census parsed from `web/static/census.json` and
  the counters read from the files at build time. `build_pdf.py` writes the same
  material as a printable pack.
- The tools live in `docs/showcase/tools/` (committed, unlike the artefacts they
  write) so a fresh clone can rebuild the pack; every one of them fails loudly on a
  missing input rather than shipping a hole.

## Findings about this machine (not defects)

- **Both live paths refuse here today, and both refusals are by name.** The in-process
  decoder refuses the pinned anchored export per frame (`this export is a multi-scale
  ANCHORED head and the in-process decoder reads only fused outputs`), and the sidecar
  path cannot hold the rate the tracking arithmetic demands: at `ai_fps=1` the
  tracking gate refuses (a crossing-sized object yields 2 samples where
  `SP_POC_TRACK_MIN_FRAMES` requires 3), and at `ai_fps=2` the throughput gate refuses
  (measured p99 809.3 ms; `809.3 x 2 > 1000`). Refusals kept in
  `dist/showcase/sidecar-refusals/`.
- **The detector-mode live run after the fixes is coherent and dated 2026-10-15 21:09:**
  preflight 11/11, 25 s watch on the resolved relay, report and transcript written.
  AC1 PASS; AC2/AC3/AC4 NOT RUN (no detections); the prove act REFUSED by name because
  the run has no detections. That run's artefacts are what the hub serves through
  `/rundoc/`.
- **Two stray `pyworker\server.py` processes** (PIDs 10788, 29672) from earlier failed
  runs were stopped before re-measuring. They were idle.
- **Untracked debris at the repository root** from an earlier screenshot attempt
  (`poc_dashboard.png`, `screenshot_poc.py`, `screenshot_poc2.py`) is not part of the
  showcase and must not be committed; it is dealt with in the media pass.

## Numbers this pass moves

- Root red-check table: 27 -> 30 entries (RC-28 the served-document name, RC-29 the
  control count, RC-30 the element-as-text scan). `AGENTS.md` corrected to thirty.
- POC red-check table: 93 -> 94 controls. `AGENTS.md` corrected from its stale
  eighty-one; the POC transcript regenerates to 94 when the POC gate runs in this pass.
- `web/static/census.json`: the gate row's POC count is updated with the regenerated
  transcript, in the same pass, so the document and the artefact never disagree.
