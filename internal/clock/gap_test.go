package clock

import (
	"strings"
	"testing"
	"time"
)

// The plan's §9, "the things that are easy to forget": a camera restarting, a
// network blip, a storage pause. Each leaves the same visible trace - the
// evidence the window needs does not exist - and each is a completely different
// fact.
//
// The failure is treating the shared trace as a finding. An evaluation that sees
// no events and concludes "nothing happened" has fabricated a negative from an
// absence, which is the one thing this product must never do. These tests pin
// that the absence is *named*, and named differently per cause.

func aWindow() Window {
	return Window{From: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), To: time.Date(2026, 7, 1, 12, 0, 8, 0, time.UTC)}
}

func aMeasuredDevice() *DeviceState {
	return &DeviceState{
		DeviceID:   "cam-014",
		Band:       BandOK,
		DriftMs:    120,
		MeasuredAt: time.Date(2026, 7, 1, 11, 55, 0, 0, time.UTC),
	}
}

func TestAFullWindowIsNotAGap(t *testing.T) {
	g := AssessGap(GapState{EventCount: 6, Window: aWindow(), State: aMeasuredDevice(), EvidenceFetched: true, EvidenceNeeded: true})
	if !g.Present() {
		t.Errorf("a window with events, a measured clock and fetched evidence was reported as %q", g.Kind)
	}
	if g.Kind != GapNone {
		t.Errorf("Kind is %q; a caller that only checks the kind would misread this window", g.Kind)
	}
}

func TestAnEmptyWindowIsNamedRatherThanReadAsQuiet(t *testing.T) {
	g := AssessGap(GapState{EventCount: 0, Window: aWindow(), State: aMeasuredDevice(), EvidenceFetched: true, EvidenceNeeded: true})
	if g.Kind != GapNoEvents {
		t.Fatalf("an empty window is %q, want %q", g.Kind, GapNoEvents)
	}
	if g.Present() {
		t.Error("an empty window reports itself as present")
	}
	// And it says, in words, that it is not a finding. That sentence is the
	// entire point; a gap that only said "no_events" is one UI label away from
	// being read as a negative.
	if !strings.Contains(g.String(), "not a finding that nothing happened") {
		t.Errorf("the gap does not say what it is not:\n%s", g.String())
	}
}

// TestEachCauseIsNamedDistinctly is the substance: a device that reported itself
// offline and a device that reported nothing look identical to a caller that
// only counts events, and they are not the same incident.
func TestEachCauseIsNamedDistinctly(t *testing.T) {
	measured := aMeasuredDevice()
	severe := aMeasuredDevice()
	severe.Band = BandSevere
	severe.DriftMs = 45000

	stale := aMeasuredDevice()
	stale.MeasuredAt = time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		in   GapState
		want GapKind
	}{
		{"no events", GapState{EventCount: 0, Window: aWindow(), State: measured}, GapNoEvents},
		{"device offline", GapState{EventCount: 0, OfflineFor: 4 * time.Second, Window: aWindow(), State: measured}, GapDeviceOffline},
		{"no clock state", GapState{EventCount: 6, Window: aWindow(), EvidenceFetched: true, EvidenceNeeded: true}, GapClockUnknown},
		{"clock measured too long ago", GapState{EventCount: 6, Window: aWindow(), State: stale, EvidenceFetched: true, EvidenceNeeded: true}, GapClockUnknown},
		{"excluded by drift", GapState{EventCount: 6, Window: aWindow(), State: severe, EvidenceFetched: true, EvidenceNeeded: true}, GapExcludedByDrift},
		{"events but no recording", GapState{EventCount: 6, Window: aWindow(), State: measured, EvidenceNeeded: true}, GapEvidenceUnavailable},
	}
	seen := map[GapKind]string{}
	for _, tc := range cases {
		g := AssessGap(tc.in)
		if g.Kind != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, g.Kind, tc.want)
			continue
		}
		if prev, dup := seen[g.Kind]; dup && g.Kind != tc.want {
			t.Errorf("%s and %s share the kind %q", prev, tc.name, g.Kind)
		}
		seen[g.Kind] = tc.name
	}
	if len(seen) < 5 {
		t.Errorf("only %d distinct causes across the catalogue; each must be tellable from the others", len(seen))
	}
}

func TestTheMostSpecificCauseWins(t *testing.T) {
	// A device that reported itself offline is not also "a device that reported
	// nothing", and telling an operator the second thing sends them to check a
	// camera that was switched off.
	severe := aMeasuredDevice()
	severe.Band = BandSevere
	severe.DriftMs = 45000

	g := AssessGap(GapState{
		EventCount: 0, OfflineFor: 4 * time.Second,
		Window: aWindow(), State: severe,
	})
	if g.Kind != GapDeviceOffline {
		t.Errorf("an offline device with severe drift is %q; the vendor's own report is the more "+
			"specific fact and an operator should be sent to the device, not the clock", g.Kind)
	}
}

func TestAPartialGapSaysHowMuchIsMissing(t *testing.T) {
	// "Partly" is the case a boolean throws away, and it is the case an operator
	// most needs: a four-minute window with ninety seconds missing is a
	// different question from a window with none of it.
	g := AssessGap(GapState{OfflineFor: 2 * time.Second, Window: aWindow(), State: aMeasuredDevice()})
	if !g.Partial() {
		t.Errorf("a window with two of eight seconds missing is not reported as partial: %s", g)
	}
	if g.Present() {
		t.Error("a partial window reports itself as present")
	}
	if g.Covered <= 0 || g.Covered >= 1 {
		t.Errorf("coverage is %v, want a fraction strictly between 0 and 1", g.Covered)
	}
	if !strings.Contains(g.String(), "not a finding that nothing happened") {
		t.Errorf("a partial gap does not say what it is not:\n%s", g)
	}
}

func TestEventsWithoutARecordingAreNotAQuietScene(t *testing.T) {
	// The subtlest of the six, and the one a naive "did we get any events?"
	// check gets exactly backwards. The events are real. What is missing is the
	// thing an operator would look at to judge them, and that is a retrieval
	// failure, not an absence of activity.
	g := AssessGap(GapState{
		EventCount: 4, Window: aWindow(), State: aMeasuredDevice(),
		EvidenceNeeded: true, EvidenceFetched: false,
	})
	if g.Kind != GapEvidenceUnavailable {
		t.Fatalf("events with no recording is %q, want %q", g.Kind, GapEvidenceUnavailable)
	}
	if !strings.Contains(g.String(), "retrieval failure") {
		t.Errorf("the gap does not distinguish a retrieval failure from a gap in the recording:\n%s", g)
	}
}

func TestAWindowThatNeedsNoEvidenceIsNotGappedByIt(t *testing.T) {
	// A pure clock correlation does not need the recording, and marking its
	// window as gapped would declare most of a healthy system broken.
	g := AssessGap(GapState{
		EventCount: 4, Window: aWindow(), State: aMeasuredDevice(),
		EvidenceNeeded: false, EvidenceFetched: false,
	})
	if !g.Present() {
		t.Errorf("a window that needs no recording was reported as %q", g.Kind)
	}
}
