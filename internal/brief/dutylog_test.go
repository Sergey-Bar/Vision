package brief

import (
	"fmt"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dutylog"
)

// TestFactsFromEventsRoutesCoverageToCoverage is the shared mapping's whole
// point: the POC and the console both call it, so a coverage finding must land
// in the same section whichever reader renders it. The console's own copy filed
// coverage lines under actions, so one artifact rendered two different briefs
// and the console one under-reported the site's blind spots.
func TestFactsFromEventsRoutesCoverageToCoverage(t *testing.T) {
	base := time.Date(2026, 10, 10, 23, 0, 0, 0, time.UTC)
	events := []dutylog.Event{
		{Seq: 1, TS: base, Kind: dutylog.KindTransition, Actor: "autonomy", Subject: "inc-a",
			Action: "VERIFY/VERIFYING", Reason: "dwell in a restricted zone"},
		{Seq: 2, TS: base.Add(time.Minute), Kind: dutylog.KindCoverage, Actor: "autonomy",
			CameraIDs: []string{"cam-01", "cam-02"}, EvidenceRefs: []string{"cam-02"}, Subject: "restricted-1",
			Action: "under-observed", Before: "covered", After: "under-observed",
			Reason: "every declared camera of restricted-1 (cam-02) is degraded"},
		{Seq: 3, TS: base.Add(2 * time.Minute), Kind: dutylog.KindCoverage, Actor: "autonomy",
			CameraIDs: nil, Subject: "restricted-1",
			Action: "covered", Before: "under-observed", After: "covered",
			Reason: "every declared camera of restricted-1 is watching"},
		{Seq: 4, TS: base.Add(3 * time.Minute), Kind: dutylog.KindNote, Actor: "operator:sam",
			CameraIDs: []string{"cam-02"}, Subject: "route-01", Reason: "lens smeared"},
	}

	facts := FactsFromEvents(events)
	if len(facts.Actions) != 1 {
		t.Errorf("actions = %d, want the one transition (a coverage finding is not an action)", len(facts.Actions))
	}
	if len(facts.Notes) != 1 || facts.Notes[0].Text != "lens smeared" || facts.Notes[0].Camera != "cam-02" {
		t.Errorf("notes = %+v, want the patrol note against cam-02", facts.Notes)
	}
	// One row per zone, and the LAST line wins: the zone is covered now.
	if len(facts.Coverage) != 1 {
		t.Fatalf("coverage rows = %d, want one per zone", len(facts.Coverage))
	}
	cov := facts.Coverage[0]
	if cov.ZoneID != "restricted-1" {
		t.Errorf("zone = %q, want restricted-1", cov.ZoneID)
	}
	if cov.UnderObserved {
		t.Error("the zone reads under-observed, but the last coverage line says covered")
	}
	if len(cov.DegradedCameras) != 0 {
		t.Errorf("degraded cameras = %v, want none for a covered zone", cov.DegradedCameras)
	}
	// And the dark case keeps the two lists distinct: the declared set is the
	// event's camera list and the degraded set is its evidence refs. Assigning
	// both from one field, as the first version did, printed the degraded
	// cameras as the declared ones in the console's coverage table.
	facts = FactsFromEvents(events[:2])
	if len(facts.Coverage) != 1 || !facts.Coverage[0].UnderObserved {
		t.Fatalf("the zone should read under-observed from its own line: %+v", facts.Coverage)
	}
	if got := fmt.Sprint(facts.Coverage[0].DeclaredCameras); got != "[cam-01 cam-02]" {
		t.Errorf("declared = %s, want [cam-01 cam-02]", got)
	}
	if got := fmt.Sprint(facts.Coverage[0].DegradedCameras); got != "[cam-02]" {
		t.Errorf("degraded = %s, want [cam-02]", got)
	}
	if got := Render(facts).Headline; got == "" {
		t.Error("a brief with an under-observed zone rendered no headline")
	}
}
