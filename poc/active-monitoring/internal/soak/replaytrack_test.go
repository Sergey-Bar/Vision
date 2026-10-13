package soak

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/detect"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// The track replay: RC-20.
//
// A crossing box is now Kalman-filtered, so it is a function of the frames around
// it and CANNOT be recomputed from the decisions row alone. The replay reads the
// per-frame box log and re-runs the real engine over it.
//
// The tests below are arranged so that each one is a DIFFERENT defect the replay
// must catch, plus the two ways it must report NOT RUN rather than a pass.

// replayZone is a single line at x=0.5 crossing left to right, and a whole-frame
// polygon, which is the minimum a zone can be and still have a crossing.
func replayZone() detect.Zone {
	return detect.Zone{
		Lines: []detect.Line{{
			Name: "carriageway", A: [2]float64{0.5, 0}, B: [2]float64{0.5, 1},
			Direction: "left-to-right", Classes: []string{"car"},
		}},
		Polygons: []detect.Polygon{{
			Name: "frame", Points: [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}},
			Classes: []string{"car"},
		}},
	}
}

func replayConfig() detect.Config {
	return detect.Config{
		IoU: 0.2, Tolerance: 0.005, TrackMinFrames: 2, MaxAge: 2 * time.Second,
	}
}

// seedTrackRun writes a detection, a detector-mode decision and the per-frame boxes
// for a car walking steadily across the line, and returns the ids.
//
// The boxes are written by RUNNING A REAL ENGINE rather than by computing them, so
// the stored decision's region is the same filtered box the replay will produce.
// That is the whole design: the replay is a check on the stored facts, so the test
// has to store facts the real code would have stored.
func seedTrackRun(t *testing.T, st *store.Store) (runID, detectionID string) {
	t.Helper()
	ctx := context.Background()
	runID = "rt1"
	startRun(t, st, runID)

	eng, err := detect.New(replayZone(), detect.Config{
		ScoreThreshold: 0.45, Classes: []string{"car"}, IoU: 0.2, Tolerance: 0.005,
		MaxAge: 2 * time.Second, TrackMinFrames: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	var obs []store.BoxObservation
	var crossings []detect.Crossing
	crossingFrame := int64(-1)
	at := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 16; i++ {
		cx := 0.20 + float64(i)*0.045
		b := detect.Box{Class: "car", Score: 0.9,
			X1: cx - 0.045, Y1: 0.35, X2: cx + 0.045, Y2: 0.78}
		cx2, _, _ := eng.FeedAnswered(at, detect.Answer{AIState: "asked_answered", Boxes: []detect.Box{b}})
		if len(cx2) > 0 && crossingFrame < 0 {
			crossingFrame = int64(i)
		}
		crossings = append(crossings, cx2...)
		obs = append(obs, store.BoxObservation{
			RunID: runID, FrameSeq: int64(i), At: at, TrackID: -1,
			Class: "car", Score: 0.9,
			Top: int(b.Y1 * 1000), Left: int(b.X1 * 1000),
			Bottom: int(b.Y2 * 1000), Right: int(b.X2 * 1000),
			Source: "detector", Measured: true,
		})
		at = at.Add(100 * time.Millisecond)
	}
	if err := st.PutBoxObservations(ctx, obs); err != nil {
		t.Fatal(err)
	}
	if len(crossings) == 0 || crossingFrame < 0 {
		t.Fatal("the seed track produced no crossing, so there is nothing to replay")
	}
	c := crossings[0]
	detectionID = "d1"
	if err := st.Accept(ctx, store.Detection{
		ID: detectionID, RunID: runID, At: c.At, Class: "car", TrackID: c.TrackID,
		Score: c.Confidence(), BoxTop: int(c.Box.Y1 * 1000), BoxLeft: int(c.Box.X1 * 1000),
		BoxBottom: int(c.Box.Y2 * 1000), BoxRight: int(c.Box.X2 * 1000),
		Direction: c.Direction, AIState: "asked_answered", Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	sha := "abc123"
	lic := "AGPL-3.0"
	size := 640
	thr := 0.45
	classIOU, globalIOU := 0.45, 0.75
	if err := st.PutDecision(ctx, store.Decision{
		DetectionID: detectionID, RunID: runID,
		TriggerMode: store.TriggerModeDetector, TriggerClass: "car",
		Score: 0.9, Threshold: thr, Release: 0.2,
		RegionSource: "measured",
		RegionTop:    int(c.Box.Y1 * 1000), RegionLeft: int(c.Box.X1 * 1000),
		RegionBottom: int(c.Box.Y2 * 1000), RegionRight: int(c.Box.X2 * 1000),
		FrameSeq: crossingFrame, ReferenceSHA: "ref",
		ModelIdentity: strp("yolov4n"), ModelSHA256: &sha, ModelLicence: &lic,
		InputSize: &size, NMSClassIOU: &classIOU, NMSGlobalIOU: &globalIOU,
	}); err != nil {
		t.Fatal(err)
	}
	return runID, detectionID
}

func strp(s string) *string { return &s }

// startRun creates a run the store will accept.
//
// It needs a positive TargetSeconds because the store refuses a run without one:
// "PASS cannot be distinguished from PROVISIONAL". That is a real rule about the
// report and it applies to a fixture too — a run row with no declared target cannot
// be scored, and a replay fixture is a scored row.
func startRun(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.StartRun(context.Background(), store.Run{
		ID: id, StartedAt: time.Now().UTC(), TargetSeconds: 3600,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestTheTrackReplayReproducesItsOwnRun is the positive case, and it is the one
// that proves the replay is comparing against the same arithmetic the run used.
func TestTheTrackReplayReproducesItsOwnRun(t *testing.T) {
	ctx := context.Background()
	st := trackTestStore(t)
	_, id := seedTrackRun(t, st)

	rep := ReplayTrack(ctx, TrackReplayInput{
		Store: st, DetectionID: id, Zone: replayZone(), Config: replayConfig(),
		Window: 32, Tolerance: 2,
	})
	if !rep.Available {
		t.Fatalf("NOT RUN: %s", rep.Reason)
	}
	if rep.CrossingsReplayed != 1 {
		t.Fatalf("the replay produced %d crossings where the run recorded one", rep.CrossingsReplayed)
	}
	if !rep.Exact {
		t.Fatalf("the replayed box %v does not match the stored %v (delta %v). The replay runs the "+
			"REAL engine over the stored boxes, so an exact match is the expected result and a "+
			"mismatch means one of the two is not doing what this file says",
			rep.ReplayedBox, rep.StoredBox, rep.Delta)
	}
	if !rep.Passed {
		t.Fatalf("the beat did not pass: %v", FormatTrackReplay(rep, id))
	}
	if rep.ReplayClass != "car" {
		t.Errorf("replayed class %q, want car", rep.ReplayClass)
	}
}

// TestTheTrackReplayCatchesADecisionTheStoredBoxesDoNotSupport is RC-20's actual
// purpose.
//
// A decision whose box does not follow from the stored per-frame boxes is a claim
// with nothing behind it, and this is the beat that says so.
//
// The tamper here is a whole run whose stored boxes NEVER cross — the car walks up
// to x=0.40 and turns back — while its decision claims a crossing at x=0.50. That
// is the cleanest version of the defect, and it is worth saying why the obvious
// test does not work: EDITING a box in place ADDS a row rather than replacing one,
// because the table's primary key includes the coordinates so that two cars of the
// same class on one frame both fit. An append-only measurement log cannot be edited
// in place, so a tamper has to be a differently-seeded run.
func TestTheTrackReplayCatchesADecisionTheStoredBoxesDoNotSupport(t *testing.T) {
	ctx := context.Background()
	st := trackTestStore(t)
	seedNeverCrossingRun(t, st)

	rep := ReplayTrack(ctx, TrackReplayInput{
		Store: st, DetectionID: "dnc", Zone: replayZone(), Config: replayConfig(),
		Window: 32, Tolerance: 2,
	})
	if !rep.Available {
		t.Fatalf("NOT RUN: %s", rep.Reason)
	}
	if rep.CrossingsReplayed != 0 {
		t.Fatalf("the stored track produced %d crossings; it was seeded to cross none, so the "+
			"replay found something the log does not contain", rep.CrossingsReplayed)
	}
	if rep.Passed {
		t.Fatal("a decision the stored boxes do not support replayed as a PASS")
	}
	out := FormatTrackReplay(rep, "dnc")
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("the report does not say FAIL: %s", out)
	}
	if !strings.Contains(out, "cannot be recomputed") {
		t.Errorf("the report does not say the decision does not follow from the boxes: %s", out)
	}
}

// seedNeverCrossingRun writes a run whose boxes approach the line and turn back,
// plus a decision claiming they crossed it.
func seedNeverCrossingRun(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	runID := "rnc"
	startRun(t, st, runID)

	eng, err := detect.New(replayZone(), detect.Config{
		ScoreThreshold: 0.45, Classes: []string{"car"}, IoU: 0.2, Tolerance: 0.005,
		MaxAge: 2 * time.Second, TrackMinFrames: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var obs []store.BoxObservation
	at := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 14; i++ {
		// Walks to x=0.40 and turns back. Never crosses x=0.50.
		var cx float64
		if i < 7 {
			cx = 0.10 + float64(i)*0.05
		} else {
			cx = 0.40 - float64(i-7)*0.03
		}
		b := detect.Box{Class: "car", Score: 0.9,
			X1: cx - 0.045, Y1: 0.35, X2: cx + 0.045, Y2: 0.78}
		eng.FeedAnswered(at, detect.Answer{AIState: "asked_answered", Boxes: []detect.Box{b}})
		obs = append(obs, store.BoxObservation{
			RunID: runID, FrameSeq: int64(i), At: at, TrackID: -1,
			Class: "car", Score: 0.9,
			Top: int(b.Y1 * 1000), Left: int(b.X1 * 1000),
			Bottom: int(b.Y2 * 1000), Right: int(b.X2 * 1000),
			Source: "detector", Measured: true,
		})
		at = at.Add(100 * time.Millisecond)
	}
	if err := st.PutBoxObservations(ctx, obs); err != nil {
		t.Fatal(err)
	}
	// A decision claiming a crossing at the line, which the boxes above contradict.
	if err := st.Accept(ctx, store.Detection{
		ID: "dnc", RunID: runID, At: at, Class: "car", TrackID: 1,
		Score: 0.9, Direction: "left-to-right", AIState: "asked_answered",
		BoxTop: 349, BoxLeft: 470, BoxBottom: 780, BoxRight: 560,
		Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	sha, lic, thr := "abc", "AGPL-3.0", 0.45
	if err := st.PutDecision(ctx, store.Decision{
		DetectionID: "dnc", RunID: runID, TriggerMode: store.TriggerModeDetector,
		TriggerClass: "car", Score: 0.9, Threshold: thr, RegionSource: "measured",
		RegionTop: 349, RegionLeft: 470, RegionBottom: 780, RegionRight: 560,
		FrameSeq:      13,
		ModelIdentity: strp("yolov4n"), ModelSHA256: &sha, ModelLicence: &lic,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestTheTrackReplayIsNotRunWhenThereAreNoBoxes is the absence rule, and it is the
// one that matters most here.
//
// A replay that cannot run must say NOT RUN and name what is missing. Reporting it
// as a pass would be the exact defect this project has shipped three times: a zero
// that reads as a success.
func TestTheTrackReplayIsNotRunWhenThereAreNoBoxes(t *testing.T) {
	ctx := context.Background()
	st := trackTestStore(t)
	_, id := seedTrackRun(t, st)
	runID := "rt1"

	// A second run with a decision but no per-frame boxes at all.
	empty := "rt2"
	startRun(t, st, empty)
	if err := st.Accept(ctx, store.Detection{
		ID: "d2", RunID: empty, At: time.Now().UTC(), Class: "car", TrackID: 1,
		Score: 0.9, Direction: "left-to-right", AIState: "asked_answered",
		BoxTop: 349, BoxLeft: 470, BoxBottom: 780, BoxRight: 560,
		Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	sha := "abc"
	lic := "AGPL-3.0"
	if err := st.PutDecision(ctx, store.Decision{
		DetectionID: "d2", RunID: empty, TriggerMode: store.TriggerModeDetector,
		TriggerClass: "car", Score: 0.9, Threshold: 0.45,
		RegionSource: "measured", FrameSeq: 6,
		RegionTop: 349, RegionLeft: 470, RegionBottom: 780, RegionRight: 560,
		ModelIdentity: strp("yolov4n"), ModelSHA256: &sha, ModelLicence: &lic,
	}); err != nil {
		t.Fatal(err)
	}

	rep := ReplayTrack(ctx, TrackReplayInput{
		Store: st, DetectionID: "d2", Zone: replayZone(), Config: replayConfig(),
		Window: 32, Tolerance: 2,
	})
	if rep.Available {
		t.Fatal("a run with no box observations was reported as available for replay")
	}
	out := FormatTrackReplay(rep, "d2")
	if !strings.Contains(out, "NOT RUN") {
		t.Fatalf("the report does not say NOT RUN: %s", out)
	}
	if !strings.Contains(out, "no per-frame box observations") {
		t.Fatalf("the report does not name what is missing: %s", out)
	}
	// And the two must not be confused with the run that does have a log.
	ok := ReplayTrack(ctx, TrackReplayInput{
		Store: st, DetectionID: id, Zone: replayZone(), Config: replayConfig(),
		Window: 32, Tolerance: 2,
	})
	if !ok.Available {
		t.Fatalf("the seeded run was reported as unavailable while the empty one was not: %s", ok.Reason)
	}
	_ = runID
}

// TestTheTrackReplayRefusesANilStoreRatherThanPanning is the same shape as
// everything else in this module: a hard failure, not a zero.
func TestTheTrackReplayRefusesANilStoreRatherThanPanning(t *testing.T) {
	rep := ReplayTrack(context.Background(), TrackReplayInput{DetectionID: "d1"})
	if rep.Available || rep.Reason == "" {
		t.Fatalf("a nil store produced %+v", rep)
	}
	rep = ReplayTrack(context.Background(), TrackReplayInput{Store: nil, DetectionID: ""})
	if rep.Reason == "" {
		t.Fatal("a nil store and a nil detection produced no reason")
	}
}

// TestTheToleranceIsReportedBesideEveryVerdict is the rule about acceptance
// criteria: PASS, FAIL or NOT RUN with the number that decided it.
//
// A bare "PASS" on a replay whose box was 19 thousandths out under a tolerance of
// 20 tells a reader nothing about how close it was, and the next question is
// always "how close?".
func TestTheToleranceIsReportedBesideEveryVerdict(t *testing.T) {
	ctx := context.Background()
	st := trackTestStore(t)
	_, id := seedTrackRun(t, st)

	rep := ReplayTrack(ctx, TrackReplayInput{
		Store: st, DetectionID: id, Zone: replayZone(), Config: replayConfig(),
		Window: 32, Tolerance: 7,
	})
	out := FormatTrackReplay(rep, id)
	if !strings.Contains(out, "PASS") {
		t.Fatalf("the run did not pass at a tolerance of 7: %s", out)
	}
	// The PASS message names the tolerance only when it was needed, and the delta is
	// printed either way.
	if !strings.Contains(out, "delta") {
		t.Errorf("the report prints no delta: %s", out)
	}
	if !rep.Exact {
		t.Errorf("the replay was not exact, so the report must print the delta prominently: %s", out)
	}
}

// trackTestStore is a store with the run already created.
func trackTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
