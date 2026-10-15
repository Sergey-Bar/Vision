package soak

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestPreflightAndTheCapabilityTableAgreeOnTheDecoder is the bug this test
// exists for.
//
// preflight had its own copy of "is this decoder available", written as a
// start-of-line comparison against the symbol. ffmpeg puts a capability column in
// FRONT of the symbol and that column's length varies with the build — `V.....`
// on one machine, `VFS..D` on another — so the gate's copy found nothing and
// refused to start on a machine whose decoder `pocctl cap` had just reported as
// present.
//
// Two copies of one rule will disagree. This test runs the SAME fixture through
// the shared matcher preflight now uses, and through the capability table's
// own, and requires the same answer.
func TestPreflightAndTheCapabilityTableAgreeOnTheDecoder(t *testing.T) {
	// The first two lines are verbatim ffmpeg 8.1.1 output on Windows: the
	// second one has a SEVEN-character capability column because that build
	// reports frame-level multithreading.
	const real = `Decoders:
 V..... = Video
 VFS..D h264                 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10
 V....D h264_qsv             H264 video (Intel Quick Sync Video acceleration) (codec h264)
 V..... h264_cuvid           Nvidia CUVID H264 decoder (codec h264)
`
	for _, sym := range []string{"h264", "h264_qsv", "h264_cuvid"} {
		ok, _ := hasDecoder(context.Background(), PreflightInput{FFmpeg: "ffmpeg"}, sym)
		if !ok {
			t.Errorf("preflight says %q is not decodable, from real ffmpeg output that lists it. "+
				"The gate and the capability table must share ONE matcher; two copies will disagree", sym)
		}
	}
	// And a decoder this build does not have is still refused.
	ok, why := hasDecoder(context.Background(), PreflightInput{FFmpeg: "ffmpeg"}, "h264_videotoolbox")
	if ok {
		t.Fatal("preflight says a decoder this build does not list is available")
	}
	if why == "" {
		t.Fatal("the refusal has no reason; a failing row with no reason is a shrug")
	}
}

// TestPreflightSaysWhatEachFailingRowCosts: every row exists because something
// specific breaks without it, and that something has to be written down or the
// row is a rule nobody can act on.
func TestPreflightSaysWhatEachFailingRowCosts(t *testing.T) {
	res, err := Preflight(context.Background(), PreflightInput{
		FFmpeg: "", FFprobe: "", SourceReport: StreamFacts{}, FreeBytes: 0,
		Headroom: 1.5, BudgetDays: 7, DataRoot: "", Client: nil, SimURL: "",
	})
	if err != nil {
		t.Fatalf("preflight returned an error instead of a table: %v", err)
	}
	if res.Pass {
		t.Fatal("preflight passed on an empty world")
	}
	if len(res.Checks) < 8 {
		t.Fatalf("the table has %d rows, want the whole set: %v", len(res.Checks), res.Summary)
	}
	for _, c := range res.Checks {
		if c.Why == "" {
			t.Errorf("the row %q has no stated consequence", c.Name)
		}
		if c.Name == "" {
			t.Error("a row has no name")
		}
	}
	// The failure rows must be specific enough to act on, not "something failed".
	names := map[string]bool{}
	for _, c := range res.Checks {
		names[c.Name] = !c.Pass
	}
	for _, want := range []string{
		"ffmpeg on PATH",
		"h264 decoder listed by ffmpeg",
		// W1-4: the row is named for the TRIGGER, not for the sidecar. This
		// world declares none, so the sidecar is not required and asking for it
		// would be asking about a component the run does not have.
		"source visible (at least one frame)",
		"free space >= 1.50x the measured 7-day budget",
		"clock stated (ntp, host, or explicitly not verified)",
		"push target answers who it is",
		"fault rehearsal green on this build",
	} {
		if _, ok := names[want]; !ok {
			t.Errorf("the preflight table is missing the row %q", want)
		}
	}
}

// TestAClockThatIsDeclaredAndUnverifiedPassesWithACaveat: a run on the host's
// own clock is a legitimate choice, and the gate must not refuse it. Only a
// clock that is NOT declared is a refusal.
func TestAClockThatIsDeclaredAndUnverifiedPassesWithACaveat(t *testing.T) {
	for _, declared := range []string{"ntp", "host", "synchronised", "not verified"} {
		note := clockSourceNote(declared)
		if note == "" {
			t.Errorf("%q is a declared clock and produced no note for the report", declared)
		}
	}
	if !strings.Contains(clockSourceNote("not verified"), "unproven") {
		t.Error("an unverified clock does not say the timestamps are unproven; a reader would take them as checked")
	}
	if !strings.Contains(clockSourceNote(""), "no clock source was declared") {
		t.Error("an absent clock does not say so plainly")
	}
}

// --- the detector row's calibration -----------------------------------------

// detectorInput is a detector run's preflight world, with the calibration the
// caller names. Everything else is left empty on purpose: this file is about the
// detector row, and the other rows failing is not its business.
func detectorInput(cal *DetectorCalibration, p99 float64, note string) PreflightInput {
	in := PreflightInput{
		TriggerMode: "detector",
		// Named, on disk, and with threads, so the row's own arithmetic is the
		// only thing left to decide. The model is a file that exists on the
		// machine running the test: the test's own binary.
		DetectorModel:   os.Args[0],
		DetectorLibrary: os.Args[0],
		DetectorThreads: 4,
		DetectorP99MS:   p99,
		AIFPS:           5,
		Calibration:     cal,
		CalibrationNote: note,
	}
	return in
}

// TestTheDetectorRowPassesOnAMeasuredCalibration is the green half, asserted
// here because the red half is the one that used to be permanent: the row was
// fed no model, no library and no p99 by any caller, so EVERY detector run
// failed a gate its own configuration had already satisfied.
func TestTheDetectorRowPassesOnAMeasuredCalibration(t *testing.T) {
	cal := testCalibration()
	in := detectorInput(&cal, cal.P99MS, "")
	if !detectorPreflightOK(in) {
		t.Fatalf("a detector run with a measured calibration was refused: %s", detectorDetail(in))
	}
	if !strings.Contains(detectorMeasured(in), "det_p99=18.4ms") {
		t.Fatalf("the measured line does not carry the calibration's number: %q", detectorMeasured(in))
	}
	if !strings.Contains(detectorMeasured(in), "calibrated") {
		t.Fatalf("the measured line does not say the number came from a calibration: %q", detectorMeasured(in))
	}
}

// TestTheDetectorRowRefusesAnAbsentCalibrationByName: a p99 of zero must not be
// multiplied out into a pass, and the refusal must name the command that
// produces one.
func TestTheDetectorRowRefusesAnAbsentCalibrationByName(t *testing.T) {
	in := detectorInput(nil, 0, "")
	if detectorPreflightOK(in) {
		t.Fatal("a detector run with no measured calibration passed; that is the multiplication by zero this gate exists to refuse")
	}
	detail := detectorDetail(in)
	for _, want := range []string{"bench", "--data-root"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not name %q: %s", want, detail)
		}
	}
}

// TestTheDetectorRowRefusesACalibrationForAnotherBuild: the record exists and
// says so, and the refusal names WHICH part differs — "run the bench again" is
// wrong advice for a machine that ran it yesterday on a different library.
func TestTheDetectorRowRefusesACalibrationForAnotherBuild(t *testing.T) {
	cal := testCalibration()
	in := detectorInput(&cal, 0, "it was measured at 16 thread(s) and this run declares 4")
	if detectorPreflightOK(in) {
		t.Fatal("a calibration for another build was applied")
	}
	detail := detectorDetail(in)
	if !strings.Contains(detail, "16") || !strings.Contains(detail, "does not describe this run") {
		t.Fatalf("the refusal does not name the part that differs: %s", detail)
	}
}

// TestTheDetectorRowRefusesADetectorThatCannotHoldTheRate: the arithmetic is the
// gate, and a slow detector must refuse rather than backpressure at minute three.
func TestTheDetectorRowRefusesADetectorThatCannotHoldTheRate(t *testing.T) {
	cal := testCalibration()
	in := detectorInput(&cal, 400, "")
	if detectorPreflightOK(in) {
		t.Fatal("a 400ms detector at 5 fps passed: 2000ms of a 1000ms second is a run that blocks its own queue")
	}
	if !strings.Contains(detectorDetail(in), "Lower SP_POC_AI_FPS") {
		t.Fatalf("the refusal does not name the lever: %s", detectorDetail(in))
	}
}

// --- the AI row --------------------------------------------------------------

// TestTheAIRowIsAboutTheRunThatHasAnAI: the shipped detector path has no
// sidecar, and a row that fails a configuration it does not describe is a gate
// that trains everyone to ignore it. `pocctl preflight` in detector mode used to
// fail exactly that row forever.
func TestTheAIRowIsAboutTheRunThatHasAnAI(t *testing.T) {
	for _, mode := range []string{"detector", "novelty", ""} {
		ok, detail, measured := aiBudget(context.Background(), PreflightInput{TriggerMode: mode})
		if !ok {
			t.Errorf("trigger %q has no sidecar and the AI row failed: %s", mode, detail)
		}
		if !strings.Contains(measured, "not applicable") {
			t.Errorf("trigger %q reports %q; a row about a component the run does not start must say it does not apply", mode, measured)
		}
		if !strings.Contains(detail, "detector row above") {
			t.Errorf("the not-applicable answer does not point at the row that holds the number: %s", detail)
		}
	}
	// A declared sidecar that cannot be reached is still a refusal: somebody
	// asked for the sidecar, so the budget was never measured.
	ok, detail, _ := aiBudget(context.Background(), PreflightInput{TriggerMode: "sidecar"})
	if ok {
		t.Fatal("a declared sidecar with no client passed the budget row")
	}
	if !strings.Contains(detail, "SP_POC_TRIGGER_MODE=sidecar") {
		t.Fatalf("the refusal does not name the variable: %s", detail)
	}
}
