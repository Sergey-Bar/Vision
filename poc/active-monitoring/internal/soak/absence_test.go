package soak

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
	"github.com/Sergey-Bar/vision-poc/internal/config"
	"github.com/Sergey-Bar/vision-poc/internal/novelty"
	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/vlm"
	"github.com/Sergey-Bar/vision-poc/internal/xprotect"
)

// The absence aggregate's tests, and they are the plan's headline arithmetic: 500
// un-asked proposals produce ONE remote event and 500 recorded results.
//
// The number is not decorative. "One event" without "carrying the count 500" is the
// same as one event carrying the count 1 with better manners, and an operator cannot
// tell a model that was unreachable for one question from one that was unreachable
// for an hour.

// absenceRunner builds a runner with a real store and the blindness map wired, and
// nothing else: no ffmpeg, no client, no sidecar. The aggregate is a store
// interaction, so a real store is the right fixture and a fake would test the fake.
func absenceRunner(t *testing.T, guids string) (*Runner, *store.Store, context.Context) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	const run = "run-absence-1"
	if err := st.StartRun(ctx, store.Run{
		ID: run, Label: "absence", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: atTime(10, 0), TargetSeconds: 3600,
	}); err != nil {
		t.Fatal(err)
	}
	// A real Config, so the resolution under test is the shipped one and not a
	// hand-written lambda that happens to agree with it.
	cfg := config.Config{EventTypeGUID: "type-guid", CameraGUID: "camera-guid"}
	if guids != "" {
		m, perr := config.ParseEventTypeMap(guids)
		if perr != nil {
			t.Fatalf("the test's own map is malformed: %v", perr)
		}
		cfg.EventTypeGUIDs = m
	}
	return &Runner{
		opt: RunnerOptions{
			RunID: run, Store: st, Logf: func(string, ...any) {}, PushMax: 4,
			EventTypeGUID: cfg.EventTypeGUID, EventTypeGUIDs: cfg.EventTypeGUIDs,
			EventTypeGUIDFor: cfg.EventTypeGUIDFor, CameraGUID: cfg.CameraGUID,
			Provenance: store.ProvenanceSynthetic,
		},
		absence: newAbsenceLedger(),
	}, st, ctx
}

func atTime(hour, minute int) time.Time {
	return time.Date(2026, 9, 30, hour, minute, 0, 0, time.UTC)
}

// blindProposal is one crossing that could not be assessed. `At` is what separates
// two windows, so the tests move it when they want a new one.
func blindProposal(i int, at time.Time) TriggerEvent {
	return TriggerEvent{
		DetectionClass: "car", PromptID: "alarm", Line: "l", Direction: "left_to_right",
		At: at.Add(time.Duration(i) * time.Second),
		// A real measured box: an aggregate is not an excuse to push a zero region,
		// and RC-6 would refuse it anyway.
		Box: [4]float64{0.30, 0.28, 0.42, 0.40}, Score: 0.8,
		RegionSource: novelty.RegionMeasured,
	}
}

// commitProposal does the part of handleIncident the aggregate depends on: accept the
// detection, enqueue ITS OWN event, then hand the blindness to the aggregate.
func commitProposal(t *testing.T, r *Runner, st *store.Store, ctx context.Context, ev TriggerEvent) {
	t.Helper()
	id := ev.DetectionIDFor(r.opt.Provenance)
	if err := st.Accept(ctx, store.Detection{
		ID: id, RunID: r.opt.RunID, At: ev.At, Class: ev.DetectionClass, Family: "vehicle",
		BoxTop: 300, BoxLeft: 290, BoxBottom: 410, BoxRight: 420,
		Direction: ev.Direction, AIState: aistate.NotAsked, Provenance: store.ProvenanceSynthetic,
	}); err != nil {
		t.Fatal(err)
	}
	// The detection's own event, which the aggregate must NOT suppress. The crossing
	// happened; only the verdict is missing.
	if err := r.enqueueEvent(ctx, ev, EvidenceResult{
		StorageRef: "ab/cd/x.mp4", SHA256: strings.Repeat("a", 64),
		TimeBegin: ev.At.Add(-2 * time.Second), TimeEnd: ev.At.Add(2 * time.Second),
	}, vlm.Observation{DetectionID: id, State: aistate.NotAsked, Message: "no observer was wired"},
		ev.EventTagFor(r.opt.Provenance)); err != nil {
		t.Fatalf("the detection's own event was refused: %v", err)
	}
	r.enqueueAbsence(ctx, ev, vlm.Observation{DetectionID: id, State: aistate.NotAsked, Message: "no observer was wired"})
}

func absenceRows(t *testing.T, st *store.Store, ctx context.Context) []store.OutboxEntry {
	t.Helper()
	var out []store.OutboxEntry
	due, err := st.Due(ctx, time.Now().Add(time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range due {
		if e.Kind == store.KindAbsenceEvent {
			out = append(out, e)
		}
	}
	return out
}

func eventRows(t *testing.T, st *store.Store, ctx context.Context) []store.OutboxEntry {
	t.Helper()
	var out []store.OutboxEntry
	due, err := st.Due(ctx, time.Now().Add(time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range due {
		if e.Kind == store.KindEvent {
			out = append(out, e)
		}
	}
	return out
}

// TestFiveHundredUnaskedProposalsProduceOneEvent is the plan's arithmetic, stated as
// a test.
func TestFiveHundredUnaskedProposalsProduceOneEvent(t *testing.T) {
	const proposals = 500
	r, st, ctx := absenceRunner(t, "*@not_asked=blind-guid")
	for i := 0; i < proposals; i++ {
		commitProposal(t, r, st, ctx, blindProposal(i, atTime(10, 0)))
	}

	blind := absenceRows(t, st, ctx)
	if len(blind) != 1 {
		t.Fatalf("%d un-asked proposals produced %d aggregated alarms; this is the alarm fatigue the "+
			"aggregation exists to remove", proposals, len(blind))
	}
	// And the count on it is the truth, not the count at the first proposal.
	var body xprotect.EventRequest
	if err := json.Unmarshal([]byte(blind[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Data.Evidence.Note, fmt.Sprintf("window_count=%d", proposals)) {
		t.Fatalf("the aggregated event does not carry the count:\n%s", body.Data.Evidence.Note)
	}
	if !strings.Contains(body.Tag, fmt.Sprintf("|n=%d", proposals)) {
		t.Fatalf("the tag does not carry the count: %q", body.Tag)
	}
	// AC4: every detection still has its OWN outbox row. The aggregate does not
	// consume them, because the crossing happened and the VMS can draw it.
	own := eventRows(t, st, ctx)
	if len(own) != proposals {
		t.Fatalf("%d detection(s) have no event row; AC4 counts a detection with no outbox row as a push "+
			"nobody recorded, and the aggregate must not consume one", proposals-len(own))
	}
	// And no detection has TWO of one kind: the unique index is doing its job.
	seen := map[string]int{}
	for _, e := range own {
		seen[e.DetectionID]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("detection %s has %d event rows", id, n)
		}
	}
}

// TestTheAggregateEventIsAboutTheCameraAndNotAboutOneCar: the box is the whole frame
// and the value carries the blindness word. A box drawn around one car would tell the
// operator that one car was unassessed when what happened is that nothing could be
// assessed at all.
func TestTheAggregateEventIsAboutTheCameraAndNotAboutOneCar(t *testing.T) {
	r, st, ctx := absenceRunner(t, "*@not_asked=blind-guid")
	commitProposal(t, r, st, ctx, blindProposal(0, atTime(10, 0)))
	blind := absenceRows(t, st, ctx)
	if len(blind) != 1 {
		t.Fatalf("one proposal produced %d alarms", len(blind))
	}
	var body xprotect.EventRequest
	if err := json.Unmarshal([]byte(blind[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Object.BoundingBox != fullFrameBox() {
		t.Fatalf("the aggregate's box is %+v, want the whole frame: this alarm is about the camera", body.Data.Object.BoundingBox)
	}
	if !strings.HasSuffix(body.Data.Object.Value, "/"+aistate.NotAsked) {
		t.Fatalf("value = %q; the blindness word has to be in the field the Smart Client shows, not only in the tag",
			body.Data.Object.Value)
	}
	if body.Data.Evidence.SHA256 != "" {
		t.Fatal("the aggregate carries an evidence pointer; it is about the ABSENCE of an assessment, and pointing " +
			"at one proposal's clip would attach a picture to a claim about five hundred")
	}
}

// TestASecondWindowOpensAfterTheFirstWasSent: the window closes when the push worker
// sends the row, not on a timer. Two absences merged across a push would produce one
// remote event whose count understates how long the model was unreachable, and the
// count is the number an operator reads to decide how urgently to look.
func TestASecondWindowOpensAfterTheFirstWasSent(t *testing.T) {
	r, st, ctx := absenceRunner(t, "*@not_asked=blind-guid")
	commitProposal(t, r, st, ctx, blindProposal(0, atTime(10, 0)))
	first := absenceRows(t, st, ctx)
	if len(first) != 1 {
		t.Fatalf("one proposal produced %d alarms", len(first))
	}
	// The push worker sends it and records a result.
	if err := st.RecordResult(ctx, first[0].ID, "push_sent", "202 and the read-back found it", "evt-1"); err != nil {
		t.Fatal(err)
	}
	commitProposal(t, r, st, ctx, blindProposal(1, atTime(10, 5)))
	pending := absenceRows(t, st, ctx)
	if len(pending) != 1 {
		t.Fatalf("after the first window was sent there are %d pending alarms, want 1", len(pending))
	}
	if pending[0].ID == first[0].ID {
		t.Fatal("the second proposal joined the SPENT window instead of opening a new one; the count would " +
			"then understate how long the model was unreachable")
	}
	var body xprotect.EventRequest
	if err := json.Unmarshal([]byte(pending[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Data.Evidence.Note, "window_count=1") {
		t.Fatalf("the new window's count is not 1:\n%s", body.Data.Evidence.Note)
	}
}

// TestARowSpentBetweenTheReadAndTheWriteStartsANewWindow is the race. The push worker
// runs on its own goroutine; if it sends the row between the aggregate's read and its
// refresh, a blind UPDATE would overwrite a payload that has already been published
// and the count in the VMS would disagree with the count here with nothing to say
// which is right.
func TestARowSpentBetweenTheReadAndTheWriteStartsANewWindow(t *testing.T) {
	r, st, ctx := absenceRunner(t, "*@not_asked=blind-guid")
	commitProposal(t, r, st, ctx, blindProposal(0, atTime(10, 0)))
	first := absenceRows(t, st, ctx)
	if len(first) != 1 {
		t.Fatalf("one proposal produced %d alarms", len(first))
	}
	// The push worker wins the race: the row gets a result, and only then does the
	// aggregate try to refresh it.
	if err := st.RecordResult(ctx, first[0].ID, "push_sent", "202 and the read-back found it", "evt-1"); err != nil {
		t.Fatal(err)
	}
	ok, err := st.RefreshAbsence(ctx, first[0].ID, `{"already":"sent"}`, first[0].Tag, 9)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a refresh succeeded on a row that already has a result; the published payload can now be " +
			"overwritten by a count the VMS never received")
	}
	// And the aggregate notices, by opening a new window rather than joining a spent
	// one.
	commitProposal(t, r, st, ctx, blindProposal(1, atTime(10, 5)))
	pending := absenceRows(t, st, ctx)
	if len(pending) != 1 || pending[0].ID == first[0].ID {
		t.Fatalf("the proposal after a spent row did not open a new window: %+v", pending)
	}
}

// TestAnUntypedAbsenceIsRecordedAndNamedAndNotPushed is the declared limitation for
// an account without the rights to register Analytics Event Types.
//
// It is the whole failure-mode row in the plan, and the thing that must NOT happen is
// the absence being pushed with the DETECTION's event type — which would make an
// Alarm Definition on "vehicle crossing the line" fire on it and page an operator
// about a car that may not exist because the module could not reach its own model.
func TestAnUntypedAbsenceIsRecordedAndNamedAndNotPushed(t *testing.T) {
	r, st, ctx := absenceRunner(t, "") // no map at all
	for i := 0; i < 3; i++ {
		commitProposal(t, r, st, ctx, blindProposal(i, atTime(10, 0)))
	}
	if n := len(absenceRows(t, st, ctx)); n != 0 {
		t.Fatalf("%d absence alarm(s) were queued with no Analytics Event Type configured", n)
	}
	// The detection's own event is untouched: the crossing is still reported.
	if n := len(eventRows(t, st, ctx)); n != 3 {
		t.Fatalf("%d detection event(s) were queued, want 3; an untyped blindness must not retract the crossing", n)
	}
	faults, err := st.Faults(ctx, r.opt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range faults {
		if f.Reason == store.FaultAbsenceNotTyped {
			found = true
			for _, want := range []string{"(car, not_asked)", "SP_POC_EVENT_TYPE_GUIDS", "admin rights"} {
				if !strings.Contains(f.Detail, want) {
					t.Fatalf("the fault does not say %q: %s", want, f.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatalf("no %s fault was recorded; the limitation has to be declared, not silent", store.FaultAbsenceNotTyped)
	}
}

// TestTheRefusalIsLoggedOnceAndNotOncePerProposal: three proposals, one line. An
// account without the types produces this on every proposal, and five hundred
// identical lines an hour is how a real fault beside it goes unread.
func TestTheRefusalIsLoggedOnceAndNotOncePerProposal(t *testing.T) {
	var lines []string
	r, _, ctx := absenceRunner(t, "")
	r.log = func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	for i := 0; i < 5; i++ {
		r.enqueueAbsence(ctx, blindProposal(i, atTime(10, 0)), vlm.Observation{State: aistate.NotAsked})
	}
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "not pushable") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the untyped-absence refusal was logged %d time(s), want 1: %v", n, lines)
	}
}

// TestEachBlindnessStateGetsItsOwnWindow: three states, three alarms, and they do not
// merge. `not_asked` and `degraded` have different owners and different fixes, and an
// aggregate that mixed them would tell an operator the model was unreachable when it
// was reachable and slow.
func TestEachBlindnessStateGetsItsOwnWindow(t *testing.T) {
	r, st, ctx := absenceRunner(t,
		"*@not_asked=g1;*@degraded=g2;*@cannot_tell=g3")
	for i, state := range aistate.BlindnessStates {
		ev := blindProposal(i, atTime(10, 0))
		id := ev.DetectionIDFor(r.opt.Provenance)
		if err := st.Accept(ctx, store.Detection{
			ID: id, RunID: r.opt.RunID, At: ev.At, Class: ev.DetectionClass, Family: "vehicle",
			BoxTop: 300, BoxLeft: 290, BoxBottom: 410, BoxRight: 420,
			AIState: state, Provenance: store.ProvenanceSynthetic,
		}); err != nil {
			t.Fatal(err)
		}
		r.enqueueAbsence(ctx, ev, vlm.Observation{DetectionID: id, State: state})
	}
	blind := absenceRows(t, st, ctx)
	if len(blind) != len(aistate.BlindnessStates) {
		t.Fatalf("%d window(s) for %d blindness states; they must not merge", len(blind), len(aistate.BlindnessStates))
	}
	guids := map[string]bool{}
	for _, e := range blind {
		var body xprotect.EventRequest
		if err := json.Unmarshal([]byte(e.Payload), &body); err != nil {
			t.Fatal(err)
		}
		guids[body.Type] = true
	}
	if len(guids) != len(aistate.BlindnessStates) {
		t.Fatalf("%d distinct event type(s) across the three states, want %d: %v", len(guids), len(aistate.BlindnessStates), guids)
	}
}

// TestAnAnsweredProposalNeverOpensAnAbsenceWindow: the aggregate is for blindness
// only. `no_objects` is a negative ANSWER and it already drives the `false_positive`
// verdict on the detection's own event — a window that fired on it would be an alarm
// that says "nothing" hundreds of times an hour.
func TestAnAnsweredProposalNeverOpensAnAbsenceWindow(t *testing.T) {
	r, st, ctx := absenceRunner(t, "*@not_asked=blind-guid")
	for i, state := range []string{aistate.Asked, aistate.NoObjects} {
		ev := blindProposal(i, atTime(10, 0))
		id := ev.DetectionIDFor(r.opt.Provenance)
		if err := st.Accept(ctx, store.Detection{
			ID: id, RunID: r.opt.RunID, At: ev.At, Class: ev.DetectionClass, Family: "vehicle",
			BoxTop: 300, BoxLeft: 290, BoxBottom: 410, BoxRight: 420,
			AIState: state, Provenance: store.ProvenanceSynthetic,
		}); err != nil {
			t.Fatal(err)
		}
		r.enqueueAbsence(ctx, ev, vlm.Observation{DetectionID: id, State: state, Verdict: vlm.VerdictFalsePositive})
	}
	if n := len(absenceRows(t, st, ctx)); n != 0 {
		t.Fatalf("an ANSWERED state opened %d absence window(s); only not_asked, degraded and cannot_tell are blindness", n)
	}
}

// TestTheWindowTagIsMintedOnceAndReused is the mechanism, not a detail.
//
// `absenceWindowTag` takes an instant, so it MUST be called once per window and not
// once per proposal. Called per proposal it returns a different key every time: the
// aggregate still collapses to one row, but the tag the VMS ends up holding is the
// last proposal's — and a retry of the first proposal then computes a key that
// matches nothing, so the read-back finds no event, the idempotency it exists to
// provide is gone, and the retry becomes a second remote event.
func TestTheWindowTagIsMintedOnceAndReused(t *testing.T) {
	r, st, ctx := absenceRunner(t, "*@not_asked=blind-guid")
	for i := 0; i < 5; i++ {
		commitProposal(t, r, st, ctx, blindProposal(i, atTime(10, 0)))
	}
	blind := absenceRows(t, st, ctx)
	if len(blind) != 1 {
		t.Fatalf("five proposals produced %d alarms", len(blind))
	}
	var body xprotect.EventRequest
	if err := json.Unmarshal([]byte(blind[0].Payload), &body); err != nil {
		t.Fatal(err)
	}
	// The row's own tag column is the key the push worker will match on, so it and
	// the body's tag are the two things that have to agree.
	if blind[0].Tag != body.Tag {
		t.Fatalf("the outbox tag is %q and the payload's is %q; the read-back matches the first and the push "+
			"sends the second, so the two can never agree", blind[0].Tag, body.Tag)
	}
	// And it is the key the FIRST proposal minted, with the count the last one made
	// it — the prefix frozen at the window's first proposal, the count current.
	wantPrefix := absenceWindowTag(r.opt.RunID, aistate.NotAsked, blindProposal(0, atTime(10, 0)).At) + "|state=" + aistate.NotAsked + "|n=5"
	if blind[0].Tag != wantPrefix {
		t.Fatalf("the window's tag is %q, want %q. The window's key must be the FIRST proposal's, so a retry of "+
			"that proposal finds the event that is already there", blind[0].Tag, wantPrefix)
	}
	// A different run, state or instant is a different window.
	for _, other := range []string{
		absenceWindowTag("run-2", aistate.NotAsked, blindProposal(0, atTime(10, 0)).At),
		absenceWindowTag("run-1", aistate.Degraded, blindProposal(0, atTime(10, 0)).At),
		absenceWindowTag("run-1", aistate.NotAsked, atTime(11, 0)),
	} {
		if other == blind[0].Tag {
			t.Fatalf("a different window computed the same tag: %s", other)
		}
	}
}

// TestTheAbsenceKindIsDeclaredAndACountStaysSerializable: `kind` is unconstrained
// TEXT, so a third kind needs no migration — but it also means nothing enforces it,
// and a typo would leave a row nothing drains.
func TestTheAbsenceKindIsDeclaredAndACountStaysSerializable(t *testing.T) {
	for _, k := range []string{store.KindEvent, store.KindBookmark, store.KindAbsenceEvent} {
		if !store.ValidOutboxKind(k) {
			t.Fatalf("%q is not a declared outbox kind", k)
		}
	}
	if store.ValidOutboxKind("absence") {
		t.Fatal(`"absence" is accepted; it is not the declared spelling, and two spellings for one effect is ` +
			"two rows nothing joins")
	}
	if !strings.HasPrefix(absencePrefix, "aicap-") {
		t.Fatal("the aggregate's tag does not carry this module's prefix, so it cannot be recognised in a VMS search")
	}
}
