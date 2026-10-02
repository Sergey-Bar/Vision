package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The aggregate's bookkeeping, tested at the layer that owns the SQL. The runner
// tests prove the arithmetic; these prove the two columns move together and that the
// concurrency guard is a guard and not a comment.

// seedAbsence accepts one detection and puts a single pending aggregate on it.
func seedAbsence(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.StartRun(ctx, Run{
		ID: "run-abs", Label: "absence", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), TargetSeconds: 3600,
	}); err != nil {
		t.Fatal(err)
	}
	acceptOne(t, s, Detection{
		ID: "det-1", RunID: "run-abs", At: time.Date(2026, 9, 30, 10, 0, 1, 0, time.UTC),
		Class: "car", Family: "vehicle", BoxTop: 300, BoxLeft: 290, BoxBottom: 410, BoxRight: 420,
		AIState: "not_asked", Provenance: ProvenanceSynthetic,
	})
	id, err := s.Enqueue(ctx, OutboxEntry{
		DetectionID: "det-1", Kind: KindAbsenceEvent,
		Payload: `{"count":1}`, Tag: "aicap-blind-not-asked-abc|state=not_asked|n=1",
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestARefreshMovesTheKeyWithThePayload is RC-25.
//
// Only rewriting the payload puts the new count into the tag the VMS will store while
// the COLUMN the push worker reads still holds the count-one key. The read-back then
// searches for something that was never sent and reports `unknown` — "the push
// returned 202 but the event was not found" — for an event that is sitting in the VMS.
// AC2 reads that as work still in flight and AC4 as an attempt with no result.
func TestARefreshMovesTheKeyWithThePayload(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id := seedAbsence(t, s)
	before, err := s.Entry(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.RefreshAbsence(ctx, id, `{"count":500}`,
		"aicap-blind-not-asked-abc|state=not_asked|n=500", 500)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the refresh reported the row as spent; it has no result")
	}
	after, err := s.Entry(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Payload != `{"count":500}` {
		t.Fatalf("payload = %q", after.Payload)
	}
	if after.Tag == before.Tag {
		t.Fatalf("the key did not move with the payload: %q. The read-back matches the column and the push sends "+
			"the body, so a key that stayed behind makes them disagree permanently", after.Tag)
	}
	if !strings.HasSuffix(after.Tag, "|n=500") {
		t.Fatalf("the key does not carry the new count: %q", after.Tag)
	}
	if !strings.Contains(after.Detail, "500") {
		t.Fatalf("the detail does not say how many proposals joined: %q", after.Detail)
	}
}

// TestARefreshOnASpentRowIsRefused: the guard is the whole point. The push worker runs
// on its own goroutine, and a blind UPDATE would overwrite a payload that has already
// been published — so the count in the VMS would disagree with the count here with
// nothing to say which is right.
func TestARefreshOnASpentRowIsRefused(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id := seedAbsence(t, s)
	if err := s.RecordResult(ctx, id, "push_sent", "202 and the read-back found it", "evt-1"); err != nil {
		t.Fatal(err)
	}
	before, err := s.Entry(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.RefreshAbsence(ctx, id, `{"count":999}`,
		"aicap-blind-not-asked-abc|state=not_asked|n=999", 999)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a refresh succeeded on a row that already has a result")
	}
	after, err := s.Entry(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Payload != before.Payload || after.Tag != before.Tag {
		t.Fatalf("a refused refresh still changed the row: payload %q -> %q, tag %q -> %q",
			before.Payload, after.Payload, before.Tag, after.Tag)
	}
}

// TestARefreshCannotTouchADetectionEvent: the guard includes the kind, so a refresh
// aimed at an id by mistake cannot rewrite the bytes of a crossing. The ids are
// per-database and the caller has them; nothing about the API prevents passing the
// wrong one.
func TestARefreshCannotTouchADetectionEvent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	seedAbsence(t, s)
	evID, err := s.Enqueue(ctx, OutboxEntry{
		DetectionID: "det-1", Kind: KindEvent, Payload: `{"event":true}`, Tag: "aicap-car-abc",
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.RefreshAbsence(ctx, evID, `{"count":1}`, "aicap-blind-xyz", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("an absence refresh rewrote a detection's own event payload")
	}
	got, err := s.Entry(ctx, evID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Payload != `{"event":true}` || got.Tag != "aicap-car-abc" {
		t.Fatalf("the detection's event was changed: %+v", got)
	}
}

// TestPendingAbsenceFindsTheOpenWindowAndNotASpentOne: `PendingAbsence` is the
// aggregate's only bookkeeping read, and it has to agree with `RefreshAbsence` about
// what "spent" means — two functions with two definitions of a word is how a window
// joins one whose count has already been published.
func TestPendingAbsenceFindsTheOpenWindowAndNotASpentOne(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id := seedAbsence(t, s)

	got, err := s.PendingAbsence(ctx, "aicap-blind-not-asked-")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("PendingAbsence did not find the open window: %+v", got)
	}
	// A tag that is not the prefix finds nothing, which is what keeps one state from
	// joining another's window.
	other, err := s.PendingAbsence(ctx, "aicap-blind-degraded-")
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		t.Fatalf("PendingAbsence matched another state's window: %+v", other)
	}
	if err := s.RecordResult(ctx, id, "push_sent", "202", "evt-1"); err != nil {
		t.Fatal(err)
	}
	spent, err := s.PendingAbsence(ctx, "aicap-blind-not-asked-")
	if err != nil {
		t.Fatal(err)
	}
	if spent != nil {
		t.Fatalf("PendingAbsence returned a SPENT window: %+v", spent)
	}
}

// TestAnUndeclaredKindIsRefusedRatherThanQueued: `kind` is unconstrained TEXT, so
// nothing in the schema stops a typo. A row whose kind nobody recognises would sit at
// attempts=0 forever, and AC4 counts an attempt with no result as a push nobody
// recorded.
func TestAnUndeclaredKindIsRefusedRatherThanQueued(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	// The same run the aggregate test seeds, so the detection's foreign key holds: a
	// detection cannot be accepted before its run exists, and this test is about the
	// KIND and not about the run lifecycle.
	if err := s.StartRun(ctx, Run{
		ID: "run-kind", Label: "k", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC), TargetSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}
	acceptOne(t, s, Detection{
		ID: "det-k", RunID: "run-kind", At: time.Now().UTC(), Class: "car", Family: "vehicle",
		BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2, AIState: "not_asked",
		Provenance: ProvenanceSynthetic,
	})
	_, err := s.Enqueue(ctx, OutboxEntry{
		DetectionID: "det-k", Kind: "absence", Payload: `{}`, Tag: "t",
	}, 4)
	if err == nil {
		t.Fatal("an undeclared outbox kind was queued")
	}
	for _, want := range []string{"absence_event", "nothing drains", "attempts=0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}
