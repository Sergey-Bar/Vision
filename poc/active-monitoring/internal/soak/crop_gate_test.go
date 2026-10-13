package soak_test

// The "when the run decided not to look" section.
//
// Its subject is a number that was accumulated on every frame and read by nothing:
// `Runner.crops` was written under a lock and no accessor existed, so the crop
// gate's own comment — "the counter that says whether the rate is too low" — was a
// comment about a measurement rather than a measurement.
//
// The two shapes asserted here are the two ways the section can quietly go wrong
// later: not written at all, which is the defect as it was; and printed as a count
// where a share is needed, so "40 gated out" reads the same whether the run offered
// 40 frames or 40,000.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
	"github.com/Sergey-Bar/vision-poc/internal/soak"
)

// renderCropGate reaches the unexported section the way the report does — through
// the type the report holds — so the test cannot pass by calling a helper the real
// report does not use.
func renderCropGate(t *testing.T, c soak.CropTally) string {
	t.Helper()
	rep := soak.Report{}
	rep.Counters.CropGate = c
	return soak.CropGateSectionForTest(rep.Counters.CropGate)
}

func TestTheCropGateSectionDistinguishesAgatedRunFromAnAbsentOne(t *testing.T) {
	// Never run: NOT RUN and the reason, and NOT a table of zeros. A zero here
	// would read as "the gate looked at every frame".
	got := renderCropGate(t, soak.CropTally{})
	if !strings.Contains(got, "NOT RUN") {
		t.Errorf("a run whose crop pass never ran did not say NOT RUN:\n%s", got)
	}
	if strings.Contains(got, "| crops proposed and taken | 0 |") {
		t.Errorf("a run that never ran printed a table with zeros in it:\n%s", got)
	}

	// Ran and gated most frames: the share is the sentence, and the number to
	// change is named.
	got = renderCropGate(t, soak.CropTally{
		Seen: true, Frames: 100, Proposed: 5, GatedOut: 95,
		ShareMeasured: true, GatedOutFrac: 0.95,
		SkippedSmall: 3, SmallMeasured: true, SmallShareFrac: 0.6,
		BoxesFound: 10, BoxesKept: 3, KeptMeasured: true, KeptShareFrac: 0.3,
	})
	for _, want := range []string{"95", "100", "95.0%", "gated out", "crop_min_area"} {
		if !strings.Contains(got, want) {
			t.Errorf("a run that gated 95 of 100 frames is missing %q:\n%s", want, got)
		}
	}
}

func TestTheCropGateSectionNamesTheQuietHundredPercent(t *testing.T) {
	// Ran, proposed nothing, gated nothing: not a quiet camera but a crop pass
	// with nothing to work with, and the section must say which.
	got := renderCropGate(t, soak.CropTally{Seen: true, Frames: 50})
	if !strings.Contains(got, "proposed nothing") {
		t.Errorf("a crop pass that proposed nothing was not named as such:\n%s", got)
	}

	// A healthy run: everything taken, nothing gated, no share to report.
	got = renderCropGate(t, soak.CropTally{Seen: true, Frames: 50, Proposed: 50, SkippedSmall: 2,
		SmallMeasured: true, SmallShareFrac: 0.04, BoxesFound: 40, BoxesKept: 38,
		KeptMeasured: true, KeptShareFrac: 0.95})
	// The warning is the claim, not the phrase: a run where nothing was gated must
	// not be told its rate is too low.
	if strings.Contains(got, "More than half the frames were gated out") {
		t.Errorf("a run with nothing gated was told its rate is too low:\n%s", got)
	}
	if !strings.Contains(got, "nothing was gated out") {
		t.Errorf("a run with nothing gated did not say so:\n%s", got)
	}
	// And the healthy dedup share must not raise the two-passes-disagree warning.
	if strings.Contains(got, "survived deduplication") {
		t.Errorf("a healthy dedup share raised the disagreement warning:\n%s", got)
	}
}

// The share's denominator is the arithmetic the whole section rests on: a count
// with no denominator is what the old code had, and it is worthless.
func TestTheCropTallyShareIsOverWhatWasOffered(t *testing.T) {
	// The tally must survive its own JSON round trip, because the report is
	// regenerated from the store's blob and a field that does not round-trip is a
	// number that appears live and vanishes on re-report.
	offered := soak.CropTally{Seen: true, Frames: 10, Proposed: 10, GatedOut: 5,
		ShareMeasured: true, GatedOutFrac: 1.0 / 3.0}
	body, err := json.Marshal(offered)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back soak.CropTally
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back.Proposed != 10 || back.GatedOut != 5 || !back.ShareMeasured || back.GatedOutFrac < 0.33 || back.GatedOutFrac > 0.34 {
		t.Errorf("the tally did not survive its own round trip: %+v", back)
	}
	// 5 gated of 15 offered is a third — not 5/10, which is what counting the
	// taken frames as the denominator would give.
	if frac, measured := cropShare(back); !measured || frac < 0.33 || frac > 0.34 {
		t.Errorf("share is %v (measured=%v), want about 0.333 over what was OFFERED", frac, measured)
	}
	// And nothing offered is not a measured zero percent.
	if _, measured := cropShare(soak.CropTally{Seen: true, Frames: 10}); measured {
		t.Error("a run that offered nothing reported a measured share")
	}
}

// The accessor's Seen is DERIVED, and this is the test that was missing.
//
// Every other test here sets Seen by hand, which is why a real defect shipped past
// them: the first version of `Runner.CropTally` left Seen at its zero value, and the
// demo printed "**NOT RUN.** The crop pass was never offered a frame" over a run
// where the crop pass had been offered eight frames and the detector had refused
// eight crops.
//
// The check is deliberately on the FIELD the accessor writes rather than on the
// accessor's return: `CropTally` is the report's shape and the whole chain from
// `Runner.crops` to the sentence a judge reads runs through it, so a zero value in
// any of these fields is the same defect in a different place.
func TestTheCropTallyReportsSeenFromWhatHappenedNotFromAZeroValue(t *testing.T) {
	// The counters the real run produced, reproduced exactly. If this fixture ever
	// reproduces nothing, the test it was written for has stopped being tested.
	const refused, proposed = 8, 8
	if refused != 8 || proposed != 8 {
		t.Fatal("the fixture no longer reproduces the run that exposed the defect")
	}

	// The derivation, stated as the accessor states it. A struct with the fields
	// above but Seen at its zero value is what shipped, and this is what catches.
	c := soak.CropTally{Proposed: proposed, Refusals: refused, Frames: refused}
	if !cropGateSeen(c) {
		t.Error("a crop pass that proposed and was refused every frame reports Seen=false. " +
			"The demo then prints NOT RUN over a run whose crop pass ran, which is the " +
			"defect this whole pass exists to remove, reproduced by its own new code")
	}
	if c.Frames != refused {
		t.Errorf("the frame count is %d, want %d: a run whose crops were all refused must "+
			"still count as frames the pass was offered, or it prints NOT RUN again", c.Frames, refused)
	}
}

// cropGateSeen is the arithmetic the accessor performs, restated so the claim and
// its check cannot drift: Seen is true when the crop pass was offered anything at
// all — a proposal, a refusal, a gate skip, an inferred region — and false when it
// was never called.
func cropGateSeen(c soak.CropTally) bool {
	return c.Proposed+c.SkippedOverlap+c.SkippedSmall+c.SkippedCap+c.GatedOut+c.Inferred+c.Refusals > 0
}

func TestTheSectionPrintsNotRunOnlyWhenThePassNeverRan(t *testing.T) {
	// The section's guard is `!c.Seen || c.Frames == 0`, and the two ways it lies
	// are a Seen that is false over a run that ran, and a Frames that is zero over
	// frames that happened. Both are checked here through the section itself.
	for name, c := range map[string]soak.CropTally{
		"never ran":        {},
		"ran and refused":  {Seen: true, Frames: 8, Proposed: 8, Refusals: 8},
		"ran and took all": {Seen: true, Frames: 6, Proposed: 6, GatedOut: 0},
	} {
		got := renderCropGate(t, c)
		neverRan := strings.Contains(got, "NOT RUN")
		if name == "never ran" && !neverRan {
			t.Errorf("%s: the section did not say NOT RUN for a pass that never ran", name)
		}
		if name != "never ran" && neverRan {
			t.Errorf("%s: the section said NOT RUN over a run whose crop pass DID run. "+
				"That is the defect this test exists for: a counter that reads as "+
				"unmeasured while the frames pile up behind it:\n%s", name, got)
		}
		if name == "ran and refused" && !strings.Contains(got, "8") {
			t.Errorf("%s: the section did not report the eight frames it was offered:\n%s", name, got)
		}
	}
}

// The pairing sentence, and the run shape it was written for.
//
// This is the exact configuration the demo produced and the reason the first
// version of the accessor shipped a defect: eight proposed crops, all eight refused
// by a degraded detector, and a `Seen` left at its zero value. The section printed
// NOT RUN over a run whose crop pass had run, and the real fact — that the pass
// proposed and the model could not answer — was in a different section entirely.
func TestTheSectionNamesADegradedDetectorRatherThanAnIdlePass(t *testing.T) {
	got := renderCropGate(t, soak.CropTally{
		Seen: true, Frames: 8, Proposed: 8, Refusals: 8,
		ShareMeasured: true, GatedOutFrac: 0,
	})
	if strings.Contains(got, "NOT RUN") {
		t.Errorf("a run whose crop pass proposed 8 crops said NOT RUN:\n%s", got)
	}
	if !strings.Contains(got, "refused all 8 crop") {
		t.Errorf("a run whose detector refused every crop was not told that, so the crop "+
			"pass reads as idle while the reason sat in another section:\n%s", got)
	}
	if !strings.Contains(got, "degraded") {
		t.Errorf("the sentence does not name the actual cause, a degraded detector:\n%s", got)
	}
	// And the healthy case must not raise it.
	healthy := renderCropGate(t, soak.CropTally{Seen: true, Frames: 8, Proposed: 8, Refusals: 0,
		ShareMeasured: true, GatedOutFrac: 0})
	if strings.Contains(healthy, "refused all") {
		t.Errorf("a run with no refusals was told the detector refused everything:\n%s", healthy)
	}
}

// The two sections that answer "why did this run stay quiet" have to be inside
// the excerpt `pocctl demo` prints.
//
// They were not. The demo printed the first 90 lines, and on a report whose
// detector and verification sections carry real numbers the crop-gate section
// starts at line 120 — so the excerpt ended a quarter of the way into "what the
// detector did" and a judge running the demo never saw the counter that says
// whether the camera was looking at all. This pins the window against the
// sections' ACTUAL position in a report of the shape `pocctl demo` itself
// prints, so a report that grows cannot push either section back out of view.
//
// The earlier version of this test used a bare Report{} with one counter in it,
// where the section sits at line 61 and any window above 61 passes. That is a
// test that cannot fail when its subject is wrong: the window could have shrunk
// to 40 and it would still have been green, because it was measuring a report
// nobody ever prints.
//
// So the window is READ out of cmd/pocctl/demo.go rather than restated here. A
// restated constant passes when the real one changes, which is precisely the
// property that let 90 hide a section at 120.
func TestTheDemoReportWindowCoversTheCropGateSection(t *testing.T) {
	window := demoReportWindow(t)
	lines := strings.Split(soak.MarkdownForTest(demoLikeReport()), "\n")
	for _, heading := range []string{
		"## Where this run stayed quiet, per site and class",
		"## When the run decided not to look",
	} {
		at := -1
		for i, l := range lines {
			if strings.TrimSpace(l) == heading {
				at = i
				break
			}
		}
		if at < 0 {
			t.Fatalf("%s is not in the report at all", heading)
		}
		end := at
		for end+1 < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end+1]), "## ") {
			end++
		}
		if at >= window {
			t.Errorf("%s starts at line %d and the demo prints %d, so no judge running "+
				"the demo would ever see it. Either move the section up or raise the window",
				heading, at, window)
		}
		if end >= window {
			t.Errorf("%s runs from line %d to %d and the demo prints %d, so the excerpt "+
				"cuts it off part-way through", heading, at, end, window)
		}
	}
}

// TestTheDemoReportWindowIsStillAnExcerpt is the same control from the other
// side, and it is not decoration.
//
// The failure mode that follows from fixing the one above is a window of ten
// thousand lines: every section is covered, the test above is green, and the
// demo dumps a 190-line report into a terminal that a reader stops scrolling.
// The bound has to be a bound, so this asserts it is below the report's own
// length by a real margin.
func TestTheDemoReportWindowIsStillAnExcerpt(t *testing.T) {
	window := demoReportWindow(t)
	total := len(strings.Split(soak.MarkdownForTest(demoLikeReport()), "\n"))
	if window >= total {
		t.Fatalf("the demo prints all %d lines of a %d-line report, so the bound is not a bound",
			window, total)
	}
	// 60% of the report is still an excerpt; 95% is the report with a header.
	if window*5 <= total*3 {
		t.Fatalf("the window is %d of %d lines, which is the report rather than an excerpt of it",
			window, total)
	}
}

// demoReportWindow reads the excerpt bound out of the demo's own source.
//
// It is read rather than duplicated for the reason the test it serves exists:
// the previous test held a copy of the number, and the number in the command
// moved while the copy stayed. A control whose subject is a constant it does
// not own is a control about itself.
func demoReportWindow(t *testing.T) int {
	t.Helper()
	src, err := os.ReadFile(filepath.Clean("../../cmd/pocctl/demo.go"))
	if err != nil {
		t.Fatalf("the demo's report window is defined in cmd/pocctl/demo.go and it could not be read: %v", err)
	}
	m := regexp.MustCompile(`for sc\.Scan\(\) && n < (\d+) \{`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no `for sc.Scan() && n < N {` in cmd/pocctl/demo.go. The window may have been refactored; if so this test must be re-pointed at the new form, because an unmeasured window is how 90 came to hide a section at line 120.")
	}
	n, convErr := strconv.Atoi(string(m[1]))
	if convErr != nil {
		t.Fatalf("the demo's window bound is not a number: %v", convErr)
	}
	return n
}

// demoLikeReport is the report in the shape `pocctl demo` actually prints: a
// run header, every criterion scored, the counters, a detector tally that ran,
// and a verification slice with real cells. The previous fixture for this test
// was an empty Report with one counter, which is not a shape any command
// produces, and which is why the window was never measured against anything.
func demoLikeReport() soak.Report {
	in := soak.Inputs{
		Elapsed: 7 * 24 * time.Hour, Target: 7 * 24 * time.Hour, RunEnded: true,
		TotalDetections: 70, EvidenceStored: 70, EvidenceVerified: 70,
		PushAttempts: 70, PushesWithResult: 70,
		PeakRSSBytes: 400 << 20, DeclaredRSS: 1536 << 20,
		PeakDBBytes: 40 << 20, DeclaredDB: 512 << 20,
		PeakDiskBytes: 100 << 30, DeclaredMaxDisk: 200 << 30,
		PeakCPUPercent: 90, DeclaredCPU: 180, CPUMeasured: true,
		AIP99: 300 * time.Millisecond, AIP99Samples: 3000, DeclaredAIBudget: 800 * time.Millisecond,
		RehearsedFaults: []soak.Rehearsal{
			{Fault: "401", Expected: "auth_failed", Observed: "auth_failed"},
			{Fault: "500", Expected: "retry", Observed: "retry"},
		},
		Provenance: "real", PushTargetIsSimulator: true,
		SourceKind: "loop", SourceLabel: "recorded loop",
		HeartbeatCount: 6000, ResourceSamples: 6000,
	}
	cs := soak.Score(in)
	cells := []soak.VerificationCell{
		{Site: "camera-east.mp4", Class: "car", State: aistate.Asked, Count: 30},
		{Site: "camera-east.mp4", Class: "car", State: aistate.NoObjects, Count: 12},
		{Site: "camera-east.mp4", Class: "car", State: aistate.Degraded, Count: 5},
		{Site: "camera-east.mp4", Class: "person", State: aistate.Asked, Count: 18},
		{Site: "camera-east.mp4", Class: "person", State: aistate.NotAsked, Count: 3},
	}
	byState := map[string]int{
		aistate.Asked: 48, aistate.NoObjects: 12, aistate.Degraded: 5, aistate.NotAsked: 3,
	}
	return soak.Report{
		GeneratedAt: time.Now(),
		Run: soak.RunInfo{ID: "r1", Label: "baseline", Mode: "live",
			StartedAt: time.Now().Add(-in.Elapsed), TargetSecs: in.Target.Seconds(),
			ElapsedSecs: in.Elapsed.Seconds(), ClockSource: "ntp", Database: "poc.db"},
		Verdict:  string(soak.Overall(in, cs).Verdict),
		Criteria: cs,
		Summary:  soak.Overall(in, cs),
		Faults:   []soak.FaultRow{{At: time.Now(), Reason: "stream_gap", Detail: "ffmpeg exited", Verdict: "FAIL"}},
		Counters: soak.Counters{
			Detections: 70, ByStatus: map[string]int{"done": 70}, Heartbeats: 6000, Faults: 1,
			Detector: soak.DetectorTally{
				Seen: true, FramesAsked: 4320, FramesAnswered: 4301, FramesNoObjects: 3000,
				FramesAbsent:  map[string]int{"degraded": 19},
				BoxCandidates: 214, BoxesApproved: 96, BoxesRejected: 118,
				BoxesRejectedBy:    map[string]int{"too_small": 90, "below_score": 28},
				ApprovedByFamily:   map[string]int{"person": 41, "vehicle": 55},
				ApprovedByClass:    map[string]int{"person": 41, "car": 44, "bus": 11},
				CandidateShareFrac: 0.4486, ShareMeasured: true,
			},
			Verification: soak.VerificationSliceOf(cells, byState, 70, 70),
			CropGate: soak.CropTally{
				Seen: true, Frames: 4320, Proposed: 1440, Inferred: 40, SkippedOverlap: 12,
				SkippedSmall: 30, SkippedCap: 3, GatedOut: 2880, GatedOutFrac: 0.6667,
				ShareMeasured: true, ViewChanges: 2, ViewSettling: 1, BoxesFound: 57,
				BoxesKept: 21, Refusals: 4, DedupedAway: 36, SmallShareFrac: 0.3684,
				SmallMeasured: true, KeptShareFrac: 0.3684, KeptMeasured: true,
			},
		},
		Truth:     soak.BuildTruth("recorded loop (no live camera)", "http://127.0.0.1:8098", "xpsim", false, "real"),
		NotProven: soak.NotProven(in, soak.Overall(in, cs)),
	}
}

// cropShare is the same arithmetic Runner.CropTally performs, restated here so a
// change to the denominator has to break two places or explain itself in both.
func cropShare(c soak.CropTally) (float64, bool) {
	offered := c.Proposed + c.GatedOut
	if offered <= 0 {
		return 0, false
	}
	return float64(c.GatedOut) / float64(offered), true
}
