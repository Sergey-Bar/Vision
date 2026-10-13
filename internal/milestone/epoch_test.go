package milestone_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/fixture"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// P0.6: `ReplayTransport.Get` computed the scenario's effective epoch and then
// threw it away on the line `_ = epoch`. Nothing failed, because nothing
// downstream read the result. The visible consequence was that every fixture
// replayed with the wall-clock instant it happened to be captured at, so
// `WithEpoch` moved only `received_at` and left `observed_at` behind.
//
// That is not cosmetic. `observed_at` is the field every time-window predicate
// reads, so every fixture test of a window was measuring the capture date rather
// than the window, and passing for the wrong reason. The two tests below are the
// ones that would have caught it.

// scenarioWithOffsets builds a scenario whose events are two minutes apart, and
// whose envelope timestamps are the capture-date values the rebase has to
// replace.
func scenarioWithOffsets() []fixture.Scenario {
	env := func(id, typ, at string) json.RawMessage {
		body, _ := json.Marshal(map[string]any{
			"Id": id, "Type": typ, "Timestamp": at,
		})
		return body
	}
	return []fixture.Scenario{{
		FormatVersion: 1,
		Name:          "epoch-probe",
		Source:        "synthetic",
		// The capture instant. Nothing in the assertions below may depend on it.
		Epoch:      time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		CapturedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		Events: []fixture.Event{
			{OffsetMS: 0, Kind: "/api/rest/v1/events", Method: "GET",
				Envelope: env("evt-1", "Motion", "2019-01-01T00:00:00Z")},
			{OffsetMS: 120_000, Kind: "/api/rest/v1/events", Method: "GET",
				Envelope: env("evt-2", "Motion", "2019-01-01T00:02:00Z")},
		},
	}}
}

func replayAt(t *testing.T, epoch time.Time) []milestone.RawRecord {
	t.Helper()
	tr := milestone.NewReplayTransport(scenarioWithOffsets()).WithEpoch(epoch)
	a := milestone.New(tr, "site-fixture", "int-1").
		WithClock(func() time.Time { return epoch })
	recs, err := a.Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return recs
}

func byID(recs []milestone.RawRecord, id string) milestone.RawRecord {
	for _, r := range recs {
		if r.SourceEventID == id {
			return r
		}
	}
	return milestone.RawRecord{}
}

// TestReplayPlacesObservedTimeAtTheEpochPlusTheOffset is the assertion P0.6 was
// missing. With the epoch discarded, `observed_at` stayed at the capture instant
// and this failed by thirty years on a fixture authored a month before it ran.
func TestReplayPlacesObservedTimeAtTheEpochPlusTheOffset(t *testing.T) {
	epoch := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	recs := replayAt(t, epoch)
	if len(recs) != 2 {
		t.Fatalf("replay produced %d records, want 2", len(recs))
	}

	first := byID(recs, "evt-1")
	second := byID(recs, "evt-2")

	if !first.ObservedAt.Equal(epoch) {
		t.Errorf("the first event observed at %v; the epoch is %v and its offset is zero",
			first.ObservedAt, epoch)
	}
	want := epoch.Add(2 * time.Minute)
	if !second.ObservedAt.Equal(want) {
		t.Errorf("the second event observed at %v, want %v (the epoch plus its 120000ms offset)",
			second.ObservedAt, want)
	}
}

// TestReplayIsDeterministicAcrossEpochs pins the two things that have to hold
// at the same time, and which the two bugs pulled in opposite directions.
//
// Relative timing must not move: a correlation window is a difference between
// two observed times, and if the difference moved with the epoch then every
// fixture test of a window would pass on the capture date and fail in review.
func TestReplayIsDeterministicAcrossEpochs(t *testing.T) {
	early := replayAt(t, testEpoch)
	late := replayAt(t, testEpoch.Add(5*365*24*time.Hour))

	if len(early) != len(late) {
		t.Fatalf("record counts differ: %d vs %d", len(early), len(late))
	}
	earlyFirst, earlySecond := byID(early, "evt-1"), byID(early, "evt-2")
	lateFirst, lateSecond := byID(late, "evt-1"), byID(late, "evt-2")

	// Identity is epoch-independent, including for an item whose only id is a
	// content hash.
	if earlyFirst.SourceEventID != lateFirst.SourceEventID {
		t.Errorf("the first event's identity moved with the epoch: %q vs %q",
			earlyFirst.SourceEventID, lateFirst.SourceEventID)
	}
	// Absolute placement follows the epoch...
	if lateFirst.ObservedAt.Equal(earlyFirst.ObservedAt) {
		t.Errorf("the first event stayed at %v across a five-year epoch change; the epoch is being discarded",
			earlyFirst.ObservedAt)
	}
	// ...and the interval between the two does not.
	want := earlySecond.ObservedAt.Sub(earlyFirst.ObservedAt)
	if got := lateSecond.ObservedAt.Sub(lateFirst.ObservedAt); got != want {
		t.Errorf("the interval between the two events moved with the epoch: %v then %v", want, got)
	}
	if want != 2*time.Minute {
		t.Errorf("the interval is %v, want the fixture's own 2m", want)
	}
}

// TestReplayPreservesTheOffsetTheSourceDeclared checks the part that is easy to
// get wrong in the other direction, and that a host-locale bug made wrong here.
//
// A timestamp saying "+02:00" must still say "+02:00" after a rebase. The
// obvious implementation - time.Time.In(parsed.Location()) - does not do that: Go's
// parser resolves a recognised abbreviation against the *host's* zone database,
// and In then re-resolves it for the new instant, so on a host at UTC+3 in
// July a source that declared +02:00 came out declaring +03:00. The same build on
// another host produced a different answer, which is the worst possible property
// for a timestamp: a fact about the capture that depends on the machine doing
// the replay.
func TestReplayPreservesTheOffsetTheSourceDeclared(t *testing.T) {
	env, _ := json.Marshal(map[string]any{
		"Id": "evt-tz", "Type": "Motion", "Timestamp": "2019-01-01T12:00:00+02:00",
	})
	sc := []fixture.Scenario{{
		FormatVersion: 1, Name: "tz", Source: "synthetic",
		Epoch:      time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		CapturedAt: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		Events: []fixture.Event{
			{OffsetMS: 60_000, Kind: "/api/rest/v1/events", Method: "GET", Envelope: env},
		},
	}}
	epoch := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	tr := milestone.NewReplayTransport(sc).WithEpoch(epoch)
	a := milestone.New(tr, "site-fixture", "int-1").
		WithClock(func() time.Time { return epoch })
	recs, err := a.Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	rec := byID(recs, "evt-tz")
	if rec.ObservedAt.IsZero() {
		t.Fatal("the event was not parsed")
	}
	// The instant is the epoch plus the offset. The *string* the source sent says
	// 17:01+02:00 and the stored UTC value is the same instant read as 15:01Z -
	// one fact, two renderings, and the offset on the string is still the
	// source's.
	want := epoch.Add(time.Minute)
	if !rec.ObservedAt.Equal(want) {
		t.Errorf("observed at %v, want %v (the epoch plus one minute)", rec.ObservedAt, want)
	}
	if rec.SourceTimezone != "+02:00" {
		t.Errorf("the source timezone is %q; a rebase must not let the host's zone database invent one",
			rec.SourceTimezone)
	}
}
