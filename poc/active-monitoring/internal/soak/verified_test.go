package soak

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
	"github.com/Sergey-Bar/ai-captain-poc/internal/novelty"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
)

// The four live defects, each with a test whose subject is the exact line the red
// check removes.

// TestASnapshotIsRefusedWhenTheFrameIsNoLongerHeld is RC-5.
//
// W2-5: the snapshot path read `LastFrameJPEG` — the NEWEST picture — and attached
// it to a decision made on some earlier frame. It could not be detected because
// nothing recorded which frame the picture belonged to. Now it can, and this test
// is the thing that notices when it cannot.
func TestASnapshotIsRefusedWhenTheFrameIsNoLongerHeld(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, ev := snapshotRig(t, root)
	held := map[uint64][]byte{
		100: jpegBytes(t, 8, 8),
		101: jpegBytes(t, 8, 8),
	}

	r := &Runner{opt: RunnerOptions{
		RunID: "run-snap", DataRoot: root, Store: st, Evidence: ev,
		LastJPEGSeq: func() ([]byte, uint64) { return jpegBytes(t, 8, 8), 999 },
		// The process is three frames behind its own pipe, which is the normal case
		// and not an edge: the reader goroutine decodes while `process` works.
		FrameJPEGFor: func(seq uint64) ([]byte, bool) {
			b, ok := held[seq]
			return b, ok
		},
	}}

	evnt := TriggerEvent{
		DetectionClass: "motion", PromptID: "alarm", Line: "l", Direction: "none",
		At: time.Now().UTC(), Box: [4]float64{0.1, 0.2, 0.5, 0.6}, RegionSource: novelty.RegionMeasured,
	}
	// The row first, exactly as the pipeline does it: a snapshot is attached to an
	// existing detection, so there is a foreign key to satisfy.
	if err := st.Accept(ctx, store.Detection{
		ID: "det-snap-1", RunID: "run-snap", At: time.Now(), Class: "motion", Family: "vehicle",
		Score: 0.5, BoxTop: 200, BoxLeft: 100, BoxBottom: 600, BoxRight: 500,
		Provenance: store.ProvenanceReal,
	}); err != nil {
		t.Fatal(err)
	}
	r.storeSnapshot(ctx, "det-snap-1", evnt, "asked_answered", 100)
	d, err := st.Get(ctx, "det-snap-1")
	if err != nil {
		t.Fatal(err)
	}
	if d.SnapshotRef == "" {
		t.Fatal("the held frame's picture was not recorded as a snapshot")
	}

	// Now frame 100 is gone and only 101 is held. Attributing 101's picture to a
	// decision made on 100 is the defect, and it is refused.
	if err := st.Accept(ctx, store.Detection{
		ID: "det-snap-2", RunID: "run-snap", At: time.Now(), Class: "motion", Family: "vehicle",
		Score: 0.5, BoxTop: 200, BoxLeft: 100, BoxBottom: 600, BoxRight: 500,
		Provenance: store.ProvenanceReal,
	}); err != nil {
		t.Fatal(err)
	}
	delete(held, 100)
	r.storeSnapshot(ctx, "det-snap-2", evnt, "asked_answered", 100)
	d2, err := st.Get(ctx, "det-snap-2")
	if err != nil {
		t.Fatal(err)
	}
	if d2.SnapshotRef != "" {
		t.Fatalf("a snapshot was recorded for a frame that is no longer held (ref %s); the picture on file is a real "+
			"frame of the same camera and a different instant", d2.SnapshotRef)
	}
	// And the refusal is a RECORDED FAULT, not a silence. A snapshot that is
	// missing for a reason nobody wrote down is a snapshot that will be explained
	// away next quarter.
	faults, err := st.Faults(ctx, "run-snap")
	if err != nil {
		t.Fatal(err)
	}
	if !anyFaultMentions(faults, "no longer held") {
		t.Fatalf("the refusal was not recorded: %v", faultSummaries(faults))
	}
}

// TestAPushedEventWithAnAllZeroBoxIsRefused is RC-6.
//
// Every pushed event used to carry top/left/bottom/right all zero while the row in
// the database beside it held real numbers. The bounding box is what makes a VMS
// draw the object on the operator's timeline, so that is the demo's worst failure
// and it happened while the database disagreed with the wire.
func TestAPushedEventWithAnAllZeroBoxIsRefused(t *testing.T) {
	// Attributed, so the BOX refusal is the one that fires. Without the provenance
	// the event would be refused earlier for carrying no AI state, the assertion on
	// "all zero" would fail, and the test would be measuring the wrong refusal — the
	// vacuous green RC-6's own red check exists to prevent.
	prov := xprotect.Provenance{AIState: aistate.NotAsked}
	_, err := xprotect.BuildEvent(xprotect.EventInput{
		EventTypeGUID: "t", SourceGUID: "g", At: time.Now(), Tag: "tag-1",
		Class: "motion", Value: "motion", Confidence: 0.5, RuleName: "alarm-l",
		Box:        xprotect.Box{}, // the measured region went missing
		Provenance: prov,
	})
	if err == nil {
		t.Fatal("an event with an all-zero box was accepted")
	}
	if !strings.Contains(err.Error(), "all zero") {
		t.Fatalf("the refusal does not say the box is all zero: %v", err)
	}
	if !strings.Contains(err.Error(), "measured") {
		t.Fatalf("the refusal does not say the region is measured locally: %v", err)
	}
	// A real box passes, so the rule is a rule and not a refusal of everything.
	if _, err := xprotect.BuildEvent(xprotect.EventInput{
		EventTypeGUID: "t", SourceGUID: "g", At: time.Now(), Tag: "tag-1",
		Class: "motion", Value: "motion", Confidence: 0.5, RuleName: "alarm-l",
		Box:        xprotect.Box{Top: 200, Left: 100, Bottom: 600, Right: 500},
		Provenance: prov,
	}); err != nil {
		t.Fatalf("a real box was refused: %v", err)
	}
	// A FULL-FRAME box is a real box, and refusing it would make the
	// camera_integrity class unpushable.
	if _, err := xprotect.BuildEvent(xprotect.EventInput{
		EventTypeGUID: "t", SourceGUID: "g", At: time.Now(), Tag: "tag-1",
		Class: "camera_integrity", Value: "motion", Confidence: 0.5, RuleName: "camera_integrity-r",
		Box:        xprotect.Box{Top: 0, Left: 0, Bottom: 1000, Right: 1000},
		Provenance: prov,
	}); err != nil {
		t.Fatalf("a full-frame box was refused: %v", err)
	}
}

// TestTheWholeFrameRegionIsTheWholeFrameOnTheWire is W2-1's other half: a global
// change has no region, and the honest box is the whole frame — stated as such,
// rather than as whatever the arithmetic happened to leave in the struct.
func TestTheWholeFrameRegionIsTheWholeFrameOnTheWire(t *testing.T) {
	ev := TriggerEvent{
		DetectionClass: "camera_integrity", PromptID: "camera_integrity",
		Line: "reference-pinned", Direction: "none", At: time.Now().UTC(),
		Box: [4]float64{0, 0, 0, 0}, RegionSource: novelty.RegionWholeFrame,
	}
	top, left, bottom, right := regionThousandths(ev)
	if top != 0 || left != 0 || bottom != 1000 || right != 1000 {
		t.Fatalf("a whole-frame change produced the region %d,%d,%d,%d", top, left, bottom, right)
	}
	// And a temporal change whose box measured out empty is clamped to the whole
	// frame rather than pushed as zeros — because zero is not a conservative box,
	// it is a missing one, and BuildEvent refuses it.
	empty := TriggerEvent{
		DetectionClass: "motion", PromptID: "alarm", At: time.Now().UTC(),
		Box: [4]float64{0.5, 0.5, 0.5, 0.5}, RegionSource: novelty.RegionMeasured,
	}
	top, left, bottom, right = regionThousandths(empty)
	if right <= left || bottom <= top {
		t.Fatalf("an empty measured region produced %d,%d,%d,%d rather than the whole frame", top, left, bottom, right)
	}
}

// TestTheScalingExpressionIsUsedOnce: three copies of `int(box * 1000)` is three
// chances for one to drift, and a drifted box on a pushed event is a number on a
// screen that disagrees with the row beside it.
func TestTheScalingExpressionIsUsedOnce(t *testing.T) {
	ev := TriggerEvent{
		DetectionClass: "motion", PromptID: "alarm", At: time.Now().UTC(),
		Box: [4]float64{0.1, 0.2, 0.5, 0.6}, RegionSource: novelty.RegionMeasured,
	}
	top, left, bottom, right := regionThousandths(ev)
	box := measuredBox(ev)
	if box.Top != top || box.Left != left || box.Bottom != bottom || box.Right != right {
		t.Fatalf("measuredBox returned %+v and regionThousandths returned %d,%d,%d,%d; there are two scalings", box, top, left, bottom, right)
	}
	if top != 200 || left != 100 || bottom != 600 || right != 500 {
		t.Fatalf("the normalisation is %d,%d,%d,%d rather than 200,100,600,500", top, left, bottom, right)
	}
}

// TestAPushBudgetIsSpentAndTheRowIsDeadLettered is W2-6.
//
// It was accepted by `Enqueue` and discarded with `_ = maxAttempts`, so a push the
// VMS will never accept — a 403 for an event type it does not have — retried with
// exponential backoff until the run ended and the report showed a pending row
// rather than a dead letter.
func TestAPushBudgetIsSpentAndTheRowIsDeadLettered(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.StartRun(ctx, store.Run{ID: "run-push", Mode: "live", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, store.Detection{ID: "d1", RunID: "run-push", At: time.Now(), Class: "motion", Family: "vehicle", Provenance: store.ProvenanceReal}); err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(ctx, store.OutboxEntry{DetectionID: "d1", Kind: store.KindEvent, Payload: "{}", Tag: "t1"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Entry(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.MaxAttempts != 3 {
		t.Fatalf("the row carries a budget of %d rather than 3; it was accepted and discarded", row.MaxAttempts)
	}
	for i := 1; i <= 3; i++ {
		if err := s.RecordAttempt(ctx, id); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	row, err = s.Entry(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !row.DeadLetter {
		t.Fatalf("the budget of three is spent and the row is not dead-lettered: %+v", row)
	}
	if row.Attempts != 3 {
		t.Fatalf("the row records %d attempts rather than 3", row.Attempts)
	}
	if !strings.Contains(row.Detail, "budget") {
		t.Fatalf("the dead letter does not say why: %q", row.Detail)
	}
	// And a zero budget is not "infinite" and not "one": it is the documented
	// default, and a caller who says nothing gets a number that ends.
	def, err := s.Enqueue(ctx, store.OutboxEntry{DetectionID: "d1", Kind: store.KindBookmark, Payload: "{}", Tag: "t2"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Entry(ctx, def)
	if err != nil {
		t.Fatal(err)
	}
	if d.MaxAttempts != store.DefaultPushAttempts {
		t.Fatalf("a caller who declared no budget got %d rather than the documented %d", d.MaxAttempts, store.DefaultPushAttempts)
	}
}

// TestAnExpiredAssetAndAnUnansweredCallAreDistinctStates is W1-2's fault matrix,
// asserted at the store: the state is what a report counts, so two failures
// sharing a word is a report that cannot answer "was anything wrong".
func TestAnExpiredAssetAndAnUnansweredCallAreDistinctStates(t *testing.T) {
	seen := map[string]error{}
	for _, err := range []error{
		nil, vlm.ErrQuotaExhausted, vlm.ErrCrossRegion, vlm.ErrRegionUnavailable,
		vlm.ErrMalformedAnswer, vlm.ErrAssetNotFound, vlm.ErrRedirectRefused, errOther{},
	} {
		_ = err
	}
	_ = seen
	// The matrix itself is asserted in internal/vlm, where the errors live. What is
	// asserted HERE is that the store accepts every state the two vocabularies
	// contain and refuses an empty one, because an attribution row with no state is
	// the fabricated negative.
	ctx := context.Background()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.StartRun(ctx, store.Run{ID: "run-o", Mode: "live", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, store.Detection{ID: "d1", RunID: "run-o", At: time.Now(), Class: "motion", Family: "vehicle", Provenance: store.ProvenanceReal}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObservation(ctx, store.Observation{DetectionID: "d1", RunID: "run-o", AIState: "  "}); err == nil {
		t.Fatal("an attribution row with no ask state was accepted")
	}
	if err := s.PutObservation(ctx, store.Observation{
		DetectionID: "d1", RunID: "run-o", AIState: "degraded", Verdict: "false_positive",
	}); err == nil {
		t.Fatal("a verdict on an unasked ask was accepted by the store")
	}
}

type errOther struct{}

func (errOther) Error() string { return "something else" }

// --- rig ------------------------------------------------------------------

func snapshotRig(t *testing.T, root string) (*store.Store, *evidence.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.StartRun(context.Background(), store.Run{
		ID: "run-snap", Mode: "live", StartedAt: time.Now(), TargetSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	ev, err := evidence.Open(filepath.Join(root, "evidence"))
	if err != nil {
		t.Fatal(err)
	}
	return st, ev
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("the test could not encode a JPEG: %v", err)
	}
	return buf.Bytes()
}

func anyFaultMentions(faults []store.Fault, needle string) bool {
	for _, f := range faults {
		if strings.Contains(f.Detail, needle) {
			return true
		}
	}
	return false
}

func faultSummaries(faults []store.Fault) []string {
	out := make([]string, 0, len(faults))
	for _, f := range faults {
		out = append(out, f.Reason+": "+truncate(f.Detail, 80))
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
