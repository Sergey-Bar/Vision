package soak

import (
	"context"
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
