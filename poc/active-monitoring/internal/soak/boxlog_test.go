package soak

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/evidence"
	"github.com/Sergey-Bar/vision-poc/internal/recorder"
	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision/shared/classify"
	"github.com/Sergey-Bar/vision/shared/sidecar"
)

// TestEveryCrossingFrameSurvivesTheCap is the rule that makes the bound usable.
//
// At the cap the log drops frames, and the first thing a reader would assume it
// dropped is the interesting one. So a frame that produced a crossing is NEVER
// dropped, whatever the cap - it is the claim, and the frames around it are only
// context. Dropping a claim to keep context is exactly backwards.
func TestEveryCrossingFrameSurvivesTheCap(t *testing.T) {
	log := NewBoxLog(10)
	for i := 0; i < 10; i++ {
		if keep, why := log.ShouldKeep(0); !keep {
			t.Fatalf("frame %d inside the cap was dropped: %s", i, why)
		}
	}
	if log.Stats().SampledOut != 0 {
		t.Fatalf("sampled %d frames while still inside the cap", log.Stats().SampledOut)
	}
	for i := 0; i < 200; i++ {
		keep, why := log.ShouldKeep(1)
		if !keep {
			t.Fatalf("a crossing frame was dropped at the cap: %s", why)
		}
		if !strings.Contains(why, "crossing") {
			t.Fatalf("a crossing frame was kept for the wrong reason: %q", why)
		}
	}
	st := log.Stats()
	// A frame that was KEPT is never also counted as sampled out. That is what makes
	// `kept + sampledOut == seen` close, and it is why a run whose every frame
	// produced a crossing reports SampledOut of 0 despite being far past the cap:
	// nothing was dropped.
	if st.Kept+st.SampledOut != st.Seen {
		t.Fatalf("kept(%d) + sampled(%d) != seen(%d): every frame must be accounted for",
			st.Kept, st.SampledOut, st.Seen)
	}
	if st.SampledOut != 0 {
		t.Fatalf("%d frames were counted as sampled out, but every frame past the cap produced a "+
			"crossing and crossings are never dropped", st.SampledOut)
	}
	// And the flag is FALSE, which is the correct answer and the one a reader needs:
	// nothing was dropped, so this run's replay is not working from a sampled log.
	// Setting it because the cap was exceeded would tell a reader their replay is
	// weaker than it is.
	if log.Sampled() {
		t.Fatal("Sampled() is true although nothing was dropped. The flag means 'this replay reads " +
			"an incomplete log', and reporting it true when the log is complete sends a reader to " +
			"distrust a replay that has no reason to be doubted")
	}

	// The other half: a run of ordinary frames past the cap DOES drop them, and says so.
	quiet := NewBoxLog(4)
	for i := 0; i < 200; i++ {
		quiet.ShouldKeep(0)
	}
	if !quiet.Sampled() {
		t.Fatal("200 ordinary frames through a cap of 4 reported as a complete log")
	}
	qs := quiet.Stats()
	if qs.SampledOut == 0 {
		t.Fatal("nothing was dropped from a log 50 times over its cap")
	}
	if qs.Kept+qs.SampledOut != qs.Seen {
		t.Fatalf("kept(%d) + sampled(%d) != seen(%d)", qs.Kept, qs.SampledOut, qs.Seen)
	}
}

// TestTheSamplerKeepsSpreadingToTheEndOfTheRun is the reason everyNth GROWS.
//
// A fixed interval past the cap fills the log with the first `Cap` frames and then
// goes silent - and a log that goes silent is a replay that cannot see the second
// half of an incident. Growing the interval keeps the retained fraction uniform
// across the whole clip instead.
func TestTheSamplerKeepsSpreadingToTheEndOfTheRun(t *testing.T) {
	log := NewBoxLog(4)
	for i := 0; i < 4; i++ {
		log.ShouldKeep(0)
	}
	quarters := [4]int{}
	for i := 0; i < 400; i++ {
		if keep, _ := log.ShouldKeep(0); keep {
			quarters[i/100]++
		}
	}
	for q, n := range quarters {
		if n == 0 {
			t.Fatalf("quarter %d of the post-cap run kept nothing: the sampler front-loaded and "+
				"then went silent, so a replay could not see the end of an incident", q)
		}
	}
}

// TestAZeroCapDisablesTheLogAndSaysSo, because SP_POC_BOX_LOG_FRAMES=0 is a real
// setting and the refusal has to explain the consequence.
//
// A run with no per-frame box log cannot be replayed from stored boxes. That is
// acceptable when somebody chose it and unacceptable when nobody finds out, so the
// reason string says so.
func TestAZeroCapDisablesTheLogAndSaysSo(t *testing.T) {
	log := NewBoxLog(0)
	for i := 0; i < 10; i++ {
		keep, why := log.ShouldKeep(1)
		if keep {
			t.Fatalf("frame %d was kept with a cap of zero", i)
		}
		if !strings.Contains(why, "cannot be replayed") {
			t.Fatalf("the refusal does not say what disabling the log costs: %q", why)
		}
	}
	if log.Sampled() {
		t.Fatal("a disabled log reports itself as sampled")
	}
	// A nil log must be safe, because a runner built without one must not crash on
	// the first frame.
	var nilLog *BoxLog
	if keep, why := nilLog.ShouldKeep(0); keep || why == "" {
		t.Fatal("a nil BoxLog did not refuse with a reason")
	}
	if nilLog.Stats().Seen != 0 {
		t.Fatal("a nil BoxLog counted a frame")
	}
}

// TestTheMeasuredFlagIsTheDifferenceTheTableExistsFor is the whole reason the
// `measured` column is there.
//
// Two frames both produce zero boxes and mean opposite things: the model was asked
// and found nothing, and the pipeline never asked. A reader counting rows cannot
// tell them apart and computes a recall of zero out of a run that never ran - which
// is the defect class this repository has shipped twice.
func TestTheMeasuredFlagIsTheDifferenceTheTableExistsFor(t *testing.T) {
	src := SourceFor(TriggerDetector)

	// Asked, and the model published candidates but none cleared the floor.
	asked := boxObservationsFor("r1", 11, zeroTime(), nil, sidecar.Telemetry{Candidates: 8400}, nil, src)
	if len(asked) != 1 {
		t.Fatalf("a measured frame with no boxes produced %d rows, want 1", len(asked))
	}
	if !asked[0].Measured {
		t.Fatal("a frame the model was asked about is recorded as NOT measured")
	}

	// Never asked: no candidates, no boxes, no pre-NMS count.
	notAsked := boxObservationsFor("r1", 11, zeroTime(), nil, sidecar.Telemetry{}, nil, src)
	if len(notAsked) != 1 {
		t.Fatalf("an unasked frame produced %d rows, want 1", len(notAsked))
	}
	if notAsked[0].Measured {
		t.Fatal("a frame the pipeline never asked about is recorded as MEASURED. This is the " +
			"defect the column exists to prevent: a recall computed from it would be a recall over " +
			"frames nobody looked at")
	}
	if len(asked) != len(notAsked) {
		t.Fatal("the two cases must be the same shape with a different flag, or a reader counting " +
			"rows cannot tell them apart at all")
	}
}

// TestTheBoxScaleIsTheOneTheRestOfTheStoreUses - thousandths against the frame.
//
// `detections.box_*`, the decisions region columns and this table all use 0..1000.
// A second scale in this store is a reader that disagrees with every other one, and
// the disagreement would be invisible in the data and visible only as a box in the
// wrong place.
func TestTheBoxScaleIsTheOneTheRestOfTheStoreUses(t *testing.T) {
	obs := boxObservationsFor("r1", 1, zeroTime(), []sidecar.Box{
		{Class: "car", Score: 0.91, X1: 0.1, Y1: 0.2, X2: 0.3, Y2: 0.4},
		{Class: "person", Score: 0.5, X1: 0, Y1: 0, X2: 1, Y2: 1},
	}, sidecar.Telemetry{Candidates: 100}, nil, SourceFor(TriggerDetector))
	if len(obs) != 2 {
		t.Fatalf("rows = %d, want 2", len(obs))
	}
	if obs[0].Top != 200 || obs[0].Left != 100 || obs[0].Bottom != 400 || obs[0].Right != 300 {
		t.Errorf("box (0.1,0.2)-(0.3,0.4) stored as (%d,%d)-(%d,%d), want (100,200)-(300,300) in "+
			"thousandths: %+v", obs[0].Left, obs[0].Top, obs[0].Right, obs[0].Bottom, obs[0])
	}
	if obs[1].Top != 0 || obs[1].Left != 0 || obs[1].Bottom != 1000 || obs[1].Right != 1000 {
		t.Errorf("the whole frame stored as %+v, want (0,0)-(1000,1000)", obs[1])
	}
}

// TestFamilyIsAlwaysRecordedAndNeverInvented - nil is "not recorded", an
// empty string would read as a recorded absence.
func TestFamilyIsAlwaysRecordedAndNeverInvented(t *testing.T) {
	obs := boxObservationsFor("r1", 1, zeroTime(), []sidecar.Box{
		{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.1, X2: 0.3, Y2: 0.3},
		{Class: "zzzz-not-a-coco-class", Score: 0.9, X1: 0.1, Y1: 0.1, X2: 0.3, Y2: 0.3},
	}, sidecar.Telemetry{Candidates: 2}, nil, SourceFor(TriggerDetector))
	if len(obs) != 2 {
		t.Fatalf("rows = %d, want 2 - an unplaceable class is still a measured box", len(obs))
	}
	if obs[0].Class != "car" || obs[0].Family == nil {
		t.Fatalf("a car must carry its family: %+v", obs[0])
	}
	if got := *obs[0].Family; got != string(classify.FamilyOf("car")) {
		t.Errorf("family = %q, want %q", got, classify.FamilyOf("car"))
	}
	if obs[1].Family == nil {
		t.Fatal("an unplaceable class recorded a NULL family, while a placed one records a name. " +
			"A reader could not then tell 'placed in the catch-all' from 'never looked'")
	}
	if *obs[0].Family == *obs[1].Family {
		t.Fatalf("a car and a nonsense word were both placed in family %q; the taxonomy is not "+
			"doing the work the report depends on", *obs[0].Family)
	}
}

// TestTrackIDIsMinusOneAndNotZero is the claim each row makes until the Kalman
// association supplies a real one.
//
// -1 says "this box is not attached to a track yet". 0 says "this is track 0",
// which is a track number nobody has. The difference matters the moment a reader
// groups by track_id: a zero silently becomes a track with a thousand boxes on it.
func TestTrackIDIsMinusOneAndNotZero(t *testing.T) {
	obs := boxObservationsFor("r1", 1, zeroTime(),
		[]sidecar.Box{{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.1, X2: 0.3, Y2: 0.3}},
		sidecar.Telemetry{Candidates: 1}, nil, SourceFor(TriggerDetector))
	if obs[0].TrackID != -1 {
		t.Fatalf("track_id = %d, want -1: 0 would be a track number nobody has, and a reader "+
			"grouping by it would find a thousand-box track that does not exist", obs[0].TrackID)
	}
}

// TestTheSourceColumnSaysWhichDecoderRan is what stops a sidecar measurement being
// reported as a shipped-path one.
func TestTheSourceColumnSaysWhichDecoderRan(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{TriggerDetector, "detector"},
		{TriggerSidecar, "sidecar"},
		{TriggerNovelty, "novelty"},
		{"", "novelty"},
	} {
		if got := SourceFor(tc.mode); got != tc.want {
			t.Errorf("SourceFor(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
	// An unrecognised mode passes through rather than being rewritten to one of the
	// three. Mislabeling it would be the exact failure the column prevents.
	if got := SourceFor("something-new"); got != "something-new" {
		t.Errorf("an unrecognised mode was rewritten to %q; a row that names a decoder nobody "+
			"recognises is checkable and one that names the wrong decoder is not", got)
	}
}

// TestDetectorModeWithNoDetectorIsRefusedAtConstruction is the last cheap place to
// catch it.
//
// Left unrefused, the frame loop falls through to `measureTrigger`, which returns
// nothing because there is no trigger either, and the run reports an empty road at
// full health. That is the defect this project exists to end.
func TestDetectorModeWithNoDetectorIsRefusedAtConstruction(t *testing.T) {
	st, ev, rec := boxLogTestDeps(t)

	_, err := NewRunner(RunnerOptions{
		RunID: "r1", Store: st, Evidence: ev, Recorder: rec,
		TriggerMode: TriggerDetector,
	})
	if err == nil {
		t.Fatal("a detector-mode runner with no detector was built")
	}
	if !strings.Contains(err.Error(), "no detector") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
	// And it must name the consequence, because "no detector" alone sends a reader to
	// look at the wrong thing.
	if !strings.Contains(err.Error(), "zero") {
		t.Fatalf("the refusal does not say what the run would report: %v", err)
	}

	// The same for the sidecar, which has always been implied rather than stated.
	_, err = NewRunner(RunnerOptions{
		RunID: "r1", Store: st, Evidence: ev, Recorder: rec,
		TriggerMode: TriggerSidecar,
	})
	if err == nil {
		t.Fatal("a sidecar-mode runner with no client was built")
	}
	if !strings.Contains(err.Error(), "no sidecar client") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
}

// boxLogTestDeps builds the three things NewRunner refuses to proceed without.
//
// A real store and a real evidence store rather than doubles, because the refusals
// under test are about wiring rather than about storage and a double would let a
// test pass for the wrong reason.
func boxLogTestDeps(t *testing.T) (*store.Store, *evidence.Store, *recorder.Recorder) {
	t.Helper()
	work := t.TempDir()
	st, err := store.Open(work)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ev, err := evidence.Open(filepath.Join(work, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := recorder.New(recorder.Options{
		FFmpeg: "ffmpeg", RollingDir: filepath.Join(work, "rolling"), SegmentSeconds: 5, FPS: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, ev, rec
}

// zeroTime is the timestamp every box observation in this file is written with.
//
// The column is NOT NULL and these tests are about the sampled/measured flags and
// the scale, so a fixed instant keeps them comparable. It is a real time rather than
// Go's zero value because the column says NOT NULL and a zero year is a worse lie
// than 1970.
func zeroTime() time.Time { return time.Unix(0, 0).UTC() }
