package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func str(s string) *string   { return &s }
func num(f float64) *float64 { return &f }
func inum(i int) *int        { return &i }

// seedDecision creates the detection a decision must reference. The decisions
// table has a foreign key onto detections, so a decision cannot be recorded
// without one — which is correct and is why every test here pays for it.
func seedDecision(t *testing.T, s *Store, runID, detectionID string) {
	t.Helper()
	d := Detection{ID: detectionID, RunID: runID, At: time.Now(), Class: "car", TrackID: 1,
		Score: 0.9, BoxTop: 100, BoxLeft: 200, BoxBottom: 300, BoxRight: 400,
		Direction: "left-to-right", AIState: "asked_answered", Provenance: ProvenanceSynthetic}
	if err := s.Accept(context.Background(), d); err != nil {
		t.Fatalf("cannot seed the detection %s: %v", detectionID, err)
	}
}

// TestADetectorDecisionMustNameItsModel is RC-18, and it is checked on the WRITE
// path because a check that only exists in a test proves the gate goes red while
// leaving the door open.
//
// The claim a precision number makes is "this model, at this hash, under this
// licence, produced these detections". A detector-mode row with no hash makes a
// number that describes a build nobody can reconstruct — and unlike a missing
// threshold, nothing downstream can detect it, because every arithmetic in the
// module still works perfectly well on the row.
func TestADetectorDecisionMustNameItsModel(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")
	seedDecision(t, s, "r1", "d1")

	good := Decision{
		DetectionID: "d1", RunID: "r1",
		TriggerMode: TriggerModeDetector, TriggerClass: "car",
		Score: 0.9, Threshold: 0.45, Release: 0.2,
		RegionTop: 100, RegionLeft: 200, RegionBottom: 300, RegionRight: 400,
		ModelIdentity: str("yolov4n"), ModelSHA256: str("abc123"), ModelLicence: str("AGPL-3.0"),
		InputSize: inum(640), NMSClassIOU: num(0.45), NMSGlobalIOU: num(0.75),
		InferMS: num(41.2), DetLatencyP50MS: num(38), DetLatencyP95MS: num(52), DetLatencyP99MS: num(61),
		TrackID: inum(4),
	}
	if err := s.PutDecision(ctx, good); err != nil {
		t.Fatalf("a fully attributed detector decision must be accepted: %v", err)
	}

	// Read it back and check the attribution SURVIVED. A writer that accepts the
	// fields and drops them would pass the write above and fail this, and that is
	// the same silent loss the plan's risk table names for the tensor axis.
	got, err := s.DecisionFor(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelSHA256 == nil || *got.ModelSHA256 != "abc123" {
		t.Fatalf("the model hash did not survive the round trip: %+v", got.ModelSHA256)
	}
	if got.ModelLicence == nil || *got.ModelLicence != "AGPL-3.0" {
		t.Fatalf("the model licence did not survive: %+v", got.ModelLicence)
	}
	if got.InputSize == nil || *got.InputSize != 640 {
		t.Fatalf("the input size did not survive: %+v", got.InputSize)
	}
	if got.DetLatencyP99MS == nil || *got.DetLatencyP99MS != 61 {
		t.Fatalf("the p99 did not survive: %+v", got.DetLatencyP99MS)
	}
	if got.TrackID == nil || *got.TrackID != 4 {
		t.Fatalf("the track id did not survive: %+v", got.TrackID)
	}
	if !got.NamesModel() {
		t.Fatal("a decision carrying a model hash must report that it names one")
	}

	// And the refusals. Each one names the defect, because a refusal a reader
	// cannot act on is a refusal they will work around.
	bad := map[string]func(d *Decision){
		"no hash at all":    func(d *Decision) { d.ModelSHA256 = nil },
		"an empty hash":     func(d *Decision) { d.ModelSHA256 = str("") },
		"no identity":       func(d *Decision) { d.ModelIdentity = nil },
		"an empty identity": func(d *Decision) { d.ModelIdentity = str("") },
	}
	for name, mut := range bad {
		t.Run(name, func(t *testing.T) {
			seedDecision(t, s, "r1", "d-"+strings.ReplaceAll(name, " ", "-"))
			d := good
			d.DetectionID = "d-" + strings.ReplaceAll(name, " ", "-")
			d.ModelIdentity = str("yolov4n")
			d.ModelSHA256 = str("abc123")
			mut(&d)
			err := s.PutDecision(ctx, d)
			if err == nil {
				t.Fatal("a detector-mode decision that names no model was recorded")
			}
			if !strings.Contains(err.Error(), "names no model") {
				t.Fatalf("the refusal does not say what is wrong: %v", err)
			}
			// And it must not be in the table. A refusal that writes the row and
			// then complains is not a refusal.
			if _, err := s.DecisionFor(ctx, d.DetectionID); err == nil {
				t.Fatal("the refused decision is in the table anyway")
			}
		})
	}
}

// TestAMotionDecisionNamesNoModelAndSaysSo is the other half, and it is why the
// attribution columns are NULLABLE rather than NOT NULL DEFAULT ”.
//
// A `novelty`-mode decision was made by comparing two frames and a threshold. No
// model produced it. With NOT NULL DEFAULT ”, every such row would carry an empty
// hash and an empty identity — which reads as "a model was involved and produced
// nothing", a claim nobody made and nobody can check.
//
// So the null is load-bearing, and this test is what stops someone "tidying up"
// the columns into NOT NULL.
func TestAMotionDecisionNamesNoModelAndSaysSo(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")
	seedDecision(t, s, "r1", "d1")

	d := Decision{
		DetectionID: "d1", RunID: "r1",
		// `temporal` is what the novelty path actually writes: the trigger mode is
		// the novelty MODE, not the string `novelty`. Naming that here is the point,
		// because a check written against "novelty" would silently never fire.
		TriggerMode: "temporal", TriggerClass: "bird",
		Score: 0.34, Threshold: 0.30, Release: 0.20,
		RegionSource: "measured",
		RegionTop:    100, RegionLeft: 200, RegionBottom: 300, RegionRight: 400,
	}
	if err := s.PutDecision(ctx, d); err != nil {
		t.Fatalf("a motion decision naming no model is a real thing and must be accepted: %v", err)
	}

	got, err := s.DecisionFor(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelSHA256 != nil {
		t.Fatalf("a motion decision recorded a model hash %q; no model produced it", *got.ModelSHA256)
	}
	if got.InferMS != nil {
		t.Fatalf("a motion decision recorded %v ms of inference; there was no inference", *got.InferMS)
	}
	if got.TrackID != nil {
		t.Fatalf("a motion decision recorded track %d; the novelty trigger does not track", *got.TrackID)
	}
	if got.NamesModel() {
		t.Fatal("a motion decision must not claim to name a model")
	}
}

// TestTheBoxLogDistinguishesNotAskedFromNothingThere is the column that a report
// would otherwise get wrong, and it is the single defect class this repository has
// shipped twice.
//
// Both of these frames produce ZERO rows of boxes, and the two mean opposite
// things:
//
//   - frame 1: the pipeline asked the detector and it found nothing;
//   - frame 2: the pipeline never asked, because SP_POC_AI_FPS said every third
//     frame.
//
// Counting rows computes a detection rate of zero from both. The `measured` column
// is what tells them apart, and FramesMeasured is what a report reads.
func TestTheBoxLogDistinguishesNotAskedFromNothingThere(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")
	fam := "vehicle"

	// Frame 1: asked, found nothing.
	// Frame 2: NOT asked. No box rows at all — there is no box to record.
	// Frame 3: asked, found two vehicles.
	if err := s.PutBoxObservations(ctx, []BoxObservation{
		{RunID: "r1", FrameSeq: 1, At: time.Now(), Source: "detector", Measured: true},
		{RunID: "r1", FrameSeq: 3, At: time.Now(), Source: "detector", Measured: true,
			TrackID: 4, Class: "car", Family: &fam, Score: 0.91, Top: 100, Left: 200, Bottom: 300, Right: 400},
		{RunID: "r1", FrameSeq: 3, At: time.Now(), Source: "detector", Measured: true,
			TrackID: 5, Class: "truck", Score: 0.77, Top: 120, Left: 600, Bottom: 340, Right: 900},
	}); err != nil {
		t.Fatal(err)
	}

	measured, err := s.FramesMeasured(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if measured != 2 {
		t.Fatalf("frames measured = %d, want 2 — frame 2 was never asked about and must not count", measured)
	}

	rows, err := s.BoxObservations(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Ordered by frame, so a replay re-runs the frames in the order the tracker
	// saw them. Two frames with the same timestamp must still have an order.
	if rows[0].FrameSeq != 1 || rows[1].FrameSeq != 3 || rows[2].FrameSeq != 3 {
		t.Fatalf("rows are not in frame order: %d %d %d", rows[0].FrameSeq, rows[1].FrameSeq, rows[2].FrameSeq)
	}
	// Within frame 3, ordered by track then class, so the order does not depend on
	// which goroutine wrote first.
	if rows[1].TrackID != 4 || rows[1].Class != "car" || rows[2].TrackID != 5 {
		t.Fatalf("frame 3's rows are not ordered deterministically: %+v %+v", rows[1], rows[2])
	}
	// Family is nullable and NULL must mean "not recorded", not "recorded as none".
	if rows[1].Family == nil || *rows[1].Family != "vehicle" {
		t.Fatalf("family did not survive: %+v", rows[1].Family)
	}
	if rows[2].Family != nil {
		t.Fatalf("a row with no family came back as %q; NULL and empty are different facts", *rows[2].Family)
	}
}

// TestADegenerateDrawnBoxIsRefused keeps the labelling tool from recording a box
// that names no place — the same refusal PutDecision makes, on the same reasoning.
func TestADegenerateDrawnBoxIsRefused(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")

	for _, bad := range []struct {
		name string
		obj  LabelObject
	}{
		{"zero width", LabelObject{Class: "car", Top: 10, Left: 20, Bottom: 30, Right: 20}},
		{"zero height", LabelObject{Class: "car", Top: 10, Left: 20, Bottom: 10, Right: 40}},
		{"negative extent", LabelObject{Class: "car", Top: 10, Left: 20, Bottom: 5, Right: 15}},
	} {
		t.Run(bad.name, func(t *testing.T) {
			bad.obj.RunID, bad.obj.By = "r1", "tester"
			if err := s.PutLabelObject(ctx, bad.obj); err == nil {
				t.Fatal("a drawn box with no area was recorded; evidence that names no place is not a measurement")
			}
		})
	}

	ok := LabelObject{RunID: "r1", FrameSeq: 7, Idx: 0, Class: "person", Top: 100, Left: 200,
		Bottom: 400, Right: 320, By: "tester"}
	if err := s.PutLabelObject(ctx, ok); err != nil {
		t.Fatalf("a real drawn box must be accepted: %v", err)
	}
	got, err := s.LabelObjects(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Class != "person" {
		t.Fatalf("the drawn object did not survive: %+v", got)
	}
}

// TestTheLabelTallyReportsBothHalves is the NOT RUN distinction at the point where
// it is decided.
//
// A run with 400 rejects and no accepts is a strong claim. A run with no labels at
// all is NO CLAIM, and a struct that omitted the empty half — or a report that
// printed only accepts — would make the two print alike. That is the defect the
// plan's `NOT RUN` rule exists to prevent, and it starts here.
func TestTheLabelTallyReportsBothHalves(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")

	none, err := s.LabelTally(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if none.Marked != 0 || none.Accepted != 0 || none.Rejected != 0 {
		t.Fatalf("an unlabelled run must tally as zero marked, not as anything else: %+v", none)
	}

	for _, l := range []Label{
		{RunID: "r1", FrameSeq: 1, BoxKey: "a", Verdict: LabelAccept, Class: "car", By: "ann"},
		{RunID: "r1", FrameSeq: 1, BoxKey: "b", Verdict: LabelAccept, Class: "person", By: "ann"},
		{RunID: "r1", FrameSeq: 2, BoxKey: "c", Verdict: LabelReject, Class: "car", By: "bo"},
	} {
		if err := s.PutLabel(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	tally, err := s.LabelTally(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if tally.Accepted != 2 || tally.Rejected != 1 || tally.Marked != 3 {
		t.Fatalf("tally = %+v, want 2 accepted, 1 rejected, 3 marked", tally)
	}
	// Both marks are attributed. A false-positive rate with no name against it is
	// a number from nowhere.
	if len(tally.By) != 2 {
		t.Fatalf("marks by = %v, want two names", tally.By)
	}

	// Re-marking the same box is an UPDATE, not a second row that disagrees.
	if err := s.PutLabel(ctx, Label{RunID: "r1", FrameSeq: 2, BoxKey: "c", Verdict: LabelAccept, By: "bo"}); err != nil {
		t.Fatal(err)
	}
	again, err := s.LabelTally(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Marked != 3 || again.Accepted != 3 || again.Rejected != 0 {
		t.Fatalf("after re-marking: %+v, want the judgement replaced, not added to", again)
	}
}

// TestALabelMustSayWhoAndWhichWay is the closed vocabulary. "mostly yes" is not a
// label anybody can count, and a false-positive rate needs a denominator.
func TestALabelMustSayWhoAndWhichWay(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")

	cases := map[string]Label{
		"no name":       {RunID: "r1", FrameSeq: 1, BoxKey: "a", Verdict: LabelAccept},
		"no box":        {RunID: "r1", FrameSeq: 1, Verdict: LabelAccept, By: "ann"},
		"no run":        {FrameSeq: 1, BoxKey: "a", Verdict: LabelAccept, By: "ann"},
		"not a verdict": {RunID: "r1", FrameSeq: 1, BoxKey: "a", Verdict: "maybe", By: "ann"},
	}
	for name, l := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.PutLabel(ctx, l); err == nil {
				t.Fatalf("a label with %s was accepted", name)
			}
		})
	}
}

// TestTheProposedBoxCountIgnoresFramesNobodyWasAskedAbout closes the loop on the
// marking session's denominator: it counts MEASURED, TRACKED boxes only. Offering
// a human the frames the pipeline skipped would be asking them to confirm a
// silence, and every silence they confirmed would become a false positive.
func TestTheProposedBoxCountIgnoresFramesNobodyWasAskedAbout(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	newRun(t, s, "r1")

	if err := s.PutBoxObservations(ctx, []BoxObservation{
		// A measured frame with a tracked box: offered for judgement.
		{RunID: "r1", FrameSeq: 1, Source: "detector", Measured: true, TrackID: 1, Class: "car",
			Top: 1, Left: 2, Bottom: 3, Right: 4},
		// A measured frame whose box no track claimed (-1): not offered, because
		// there is no track for a human to judge against a sequence.
		{RunID: "r1", FrameSeq: 2, Source: "detector", Measured: true, TrackID: -1, Class: "car",
			Top: 1, Left: 2, Bottom: 3, Right: 4},
		// Not measured at all: never offered.
		{RunID: "r1", FrameSeq: 3, Source: "detector", Measured: false, TrackID: 1, Class: "car",
			Top: 1, Left: 2, Bottom: 3, Right: 4},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.ProposedBoxCount(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("proposed boxes = %d, want 1 — only the measured, tracked box is offered for judgement", n)
	}
}

// TestTheNewTablesSurviveAReopen is the forward-only check. These three tables are
// additive migrations on a data root that may predate them, and the whole point of
// the addedColumns mechanism is that an OLD database keeps working under a NEW
// build. A schema version that only applies to a fresh database would satisfy
// every test above.
func TestTheNewTablesSurviveAReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	newRun(t, s, "r1")
	if err := s.PutBoxObservations(ctx, []BoxObservation{
		{RunID: "r1", FrameSeq: 1, Source: "detector", Measured: true, TrackID: 1, Class: "car",
			Top: 10, Left: 20, Bottom: 30, Right: 40},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatalf("a data root written by this build must reopen: %v", err)
	}
	defer func() { _ = again.Close() }()
	rows, err := again.BoxObservations(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Class != "car" {
		t.Fatalf("the box observation did not survive a reopen: %+v", rows)
	}
	if want, have, err := again.SchemaVersion(ctx); err != nil || want != have {
		t.Fatalf("schema version = %d, database says %d (err %v); a data root must be able to say which build wrote it", want, have, err)
	}
}
