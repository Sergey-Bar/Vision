package normalize_test

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/normalize"
)

// `normalized_at` is, by AGENTS.md, the only field a time-window predicate may
// read. That makes the function that produces it load-bearing for every
// correlation decision in the product, and it had no test at all: a package that
// decides what "when it happened" means was shipping unwritten.

func TestNormalizedTimeSubtractsTheDrift(t *testing.T) {
	observed := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	got, fallback := normalize.NormalizedTime(observed, observed.Add(-time.Hour), 2000)
	if fallback {
		t.Error("a real observed time was reported as a fallback")
	}
	want := observed.Add(-2 * time.Second)
	if !got.Equal(want) {
		t.Errorf("normalized_at is %s, want %s. The device clock is 2000ms fast, so the "+
			"normalized time is 2000ms earlier than the one the device reported",
			got, want)
	}
}

// A device whose clock is behind is corrected the other way, which is the case
// a "subtract the drift" implementation gets wrong by construction.
func TestNormalizedTimeCorrectsABehindClockTheOtherWay(t *testing.T) {
	observed := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	got, _ := normalize.NormalizedTime(observed, observed, -2000)
	want := observed.Add(2 * time.Second)
	if !got.Equal(want) {
		t.Errorf("normalized_at is %s, want %s", got, want)
	}
}

// A missing AI observation is never a negative observation, and the same holds
// for a missing device time: the fallback is ingest time, it is reported as a
// fallback so a consumer can tell, and it is never silently invented.
func TestAMissingObservedTimeFallsBackToIngestAndSaysSo(t *testing.T) {
	received := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	got, fallback := normalize.NormalizedTime(time.Time{}, received, 500)
	if !fallback {
		t.Error("a missing observed time was not reported as a fallback")
	}
	if !got.Equal(received) {
		t.Errorf("the fallback is %s, want the ingest time %s", got, received)
	}

	// Neither one: there is nothing to fall back to, and the caller gets the
	// zero time plus the flag rather than a plausible-looking instant.
	got, fallback = normalize.NormalizedTime(time.Time{}, time.Time{}, 500)
	if !got.IsZero() || !fallback {
		t.Errorf("with neither time the result is (%s, %v), want the zero time and a fallback", got, fallback)
	}
}

// The result is UTC whatever zone the inputs arrived in, because a time-window
// predicate that has to ask which zone a value was in has already lost.
func TestNormalizedTimeIsAlwaysUTC(t *testing.T) {
	zone := time.FixedZone("UTC+5:30", 5*3600+1800)
	observed := time.Date(2026, 3, 1, 14, 30, 0, 0, zone)
	got, _ := normalize.NormalizedTime(observed, observed, 0)
	if got.Location() != time.UTC {
		t.Errorf("normalized_at is in %s, want UTC", got.Location())
	}
	if got.Hour() != 9 {
		t.Errorf("normalized_at is %02d:%02d, want 09:00 UTC", got.Hour(), got.Minute())
	}
}

// A derived event id, not a generated one. A retry after a crash, a replay of a
// fixture and a rebuild of a derived projection must all land on the same event;
// a generated id would make each attempt a different event and turn crash
// recovery into a reconciliation problem.
func TestDeriveEventIDIsStableAndScoped(t *testing.T) {
	key := inbox.Key{TenantID: "t-1", IntegrationID: "int-1", SourceEventID: "src-1"}
	first := normalize.DeriveEventID(key)
	if first != normalize.DeriveEventID(key) {
		t.Error("the same source key produced two event ids")
	}

	// The separator matters: without it these two keys would hash the same.
	a := inbox.Key{TenantID: "ab", IntegrationID: "c", SourceEventID: "d"}
	b := inbox.Key{TenantID: "a", IntegrationID: "bc", SourceEventID: "d"}
	if normalize.DeriveEventID(a) == normalize.DeriveEventID(b) {
		t.Error("two different source keys produced the same event id; the inputs are " +
			"concatenated without a separator and are therefore not distinguishable")
	}

	// And a different source event is a different event.
	other := key
	other.SourceEventID = "src-2"
	if normalize.DeriveEventID(other) == first {
		t.Error("two different source events produced the same event id")
	}
}
