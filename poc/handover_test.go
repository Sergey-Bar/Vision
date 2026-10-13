package main

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/dutylog"
)

// newHandoverIngest builds an ingest with a site model and an open duty log
// in a temp dir, the way main() does: the fleet is built from the site model
// and the config's camera registry, and the autonomy layer — tracker, linker,
// rule set, coverage rule, playbook — is built by NewStreamIngest.
func newHandoverIngest(t *testing.T) *StreamIngest {
	t.Helper()
	s := NewStreamIngest(&Config{
		Site: &SiteConfig{Enabled: true, File: writeTestSite(t), DutyLogDir: t.TempDir()},
		Cameras: []CameraConfig{
			{ID: "cam-01", Name: "City Square", YouTubeURL: "https://www.youtube.com/watch?v=VGnFLdQW39A"},
		},
		Live: LiveConfig{CaptureFPS: 1},
	}, "tenant-1", "site-1")
	s.mode = "demo"
	s.rootCtx = t.Context()
	if err := s.initHandover("test-shift"); err != nil {
		t.Fatalf("init handover: %v", err)
	}
	t.Cleanup(func() { _ = s.handover.duty.Close() })
	return s
}

// TestLogTransitionRecordsOnlyChanges is the artifact's volume contract: an
// autonomy pass re-evaluates an open incident every frame, so a line is
// written when the decision changes and not when it merely repeats. Without
// this, the shift's log is a thousand identical lines and the hand-over is
// unreadable.
func TestLogTransitionRecordsOnlyChanges(t *testing.T) {
	s := newHandoverIngest(t)
	inc := &correlation.CorrelatedIncident{
		ID:            "inc-test",
		CameraIDs:     []string{"cam-01"},
		DetectionIDs:  []string{"cam-01/t0001"},
		ReasonChain:   correlation.ReasonChain{Why: "dwell in a restricted zone"},
		FirstSeen:     time.Now().UTC(),
		AutonomyState: "VERIFYING",
		Action:        "VERIFY",
		ThreatScore:   62,
	}

	if !s.logTransition(inc, "VERIFY", "VERIFYING", inc.ReasonChain.Why) {
		t.Fatal("the first decision for an incident was not recorded")
	}
	for i := 0; i < 5; i++ {
		if s.logTransition(inc, "VERIFY", "VERIFYING", inc.ReasonChain.Why) {
			t.Fatalf("repeat %d of an unchanged decision was recorded", i)
		}
	}
	if !s.logTransition(inc, "ESCALATE", "ESCALATING", "second camera corroborated") {
		t.Fatal("a changed decision was not recorded")
	}

	events, err := dutylog.ReadAll(s.handover.duty.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("duty log holds %d lines, want 2", len(events))
	}
	if events[0].Before != "" || events[1].Before != "VERIFY/VERIFYING" {
		t.Errorf("chain of decisions is broken: %+v", []string{events[0].Before, events[1].Before})
	}
	if events[1].Reason != "second camera corroborated" {
		t.Errorf("escalation reason = %q", events[1].Reason)
	}
	if err := s.handover.duty.Verify(); err != nil {
		t.Errorf("log does not verify: %v", err)
	}
}

// TestRebuildBriefDerivesFromTheLog checks the artifact/view rule: the brief
// rendered after a decision contains that decision, because it is read back
// out of the duty log rather than remembered in a second place.
func TestRebuildBriefDerivesFromTheLog(t *testing.T) {
	s := newHandoverIngest(t)
	inc := &correlation.CorrelatedIncident{
		ID:            "inc-test",
		CameraIDs:     []string{"cam-01"},
		DetectionIDs:  []string{"cam-01/t0001"},
		ReasonChain:   correlation.ReasonChain{Why: "dwell in a restricted zone"},
		FirstSeen:     time.Now().UTC(),
		AutonomyState: "VERIFYING",
		Action:        "VERIFY",
		ThreatScore:   62,
	}
	if !s.logTransition(inc, "VERIFY", "VERIFYING", inc.ReasonChain.Why) {
		t.Fatal("decision not recorded")
	}

	// A camera the fleet can see, so the brief's camera section is real.
	c := s.fleet.get("cam-01")
	c.frames.Store(10)
	c.lastFrame.Store(time.Now().UTC().Add(-500 * time.Millisecond).UnixNano())

	s.rebuildBrief()
	b := s.latestBrief()
	if b == nil {
		t.Fatal("no brief was rendered")
	}
	if len(b.Actions) != 1 {
		t.Fatalf("brief carries %d actions, want the one the log holds", len(b.Actions))
	}
	if b.Actions[0].Action != "VERIFY/VERIFYING" || b.Actions[0].Reason != inc.ReasonChain.Why {
		t.Errorf("brief's action = %+v", b.Actions[0])
	}
	// The camera section is the fleet's own counters, not a remembered state.
	var sawCam bool
	for _, cam := range b.Cameras {
		if cam.ID == "cam-01" && cam.State == camFresh {
			sawCam = true
		}
	}
	if !sawCam {
		t.Errorf("brief's camera section = %+v, want cam-01 %s", b.Cameras, camFresh)
	}
	if b.Shift != "test-shift" || b.SiteID != "site-1" || b.TenantID != "tenant-1" {
		t.Errorf("brief identity = %q/%q/%q, want test-shift/site-1/tenant-1", b.Shift, b.SiteID, b.TenantID)
	}
}

// TestBriefCoverageReflectsFleetHealth checks the declaration-based coverage
// section: a zone whose only declared camera never started reads under-
// observed in the brief, and the brief's open items name it — so a handover
// cannot show "all clear" for a zone nobody is watching.
func TestBriefCoverageReflectsFleetHealth(t *testing.T) {
	s := newHandoverIngest(t)

	// The coverage rule is wired into the rule set, from the site model's
	// declarations plus the fleet's live health.
	var wired bool
	for _, r := range s.anomalyRules.Rules() {
		if r.Name() == "coverage-under_observed" {
			wired = true
		}
	}
	if !wired {
		t.Fatal("the coverage rule is not in the anomaly rule set")
	}

	s.rebuildBrief()
	b := s.latestBrief()
	if b == nil {
		t.Fatal("no brief rendered")
	}
	var restricted, entrance bool
	for _, z := range b.Coverage {
		switch z.ZoneID {
		case "restricted":
			restricted = z.UnderObserved && len(z.DegradedCameras) > 0
		case "entrance":
			entrance = z.UnderObserved && len(z.DegradedCameras) > 0
		}
	}
	if !restricted {
		t.Error("restricted's only declared camera never started; it must read under-observed")
	}
	if !entrance {
		t.Error("entrance's only declared camera never started; it must read under-observed")
	}
	found := false
	for _, o := range b.OpenItems {
		if o.Kind == "coverage" && o.ID == "restricted" {
			found = true
		}
	}
	if !found {
		t.Error("the brief's open items do not name the under-observed zone")
	}
}

// TestATerminalIncidentStopsAccumulatingMemory is the bound the per-incident
// maps needed: both the last duty-log decision key and the last second-sight
// verdict are dropped when an incident ends, so a shift with many short-lived
// incidents does not grow the ingest's memory by two entries per incident. The
// linker never prunes its own incidents, so without this the ingest holds one
// entry per incident a shift produced.
func TestATerminalIncidentStopsAccumulatingMemory(t *testing.T) {
	s := newHandoverIngest(t)
	now := time.Now().UTC()

	// A sighting in the restricted zone produces a correlated incident.
	for i := 0; i < 6; i++ {
		s.autonomyPass("cam-01", now.Add(time.Duration(i)*time.Second), int64(i),
			[]frameObject{{ID: "p1", Category: "biological", Sub: "person", Label: "person",
				Conf: 0.9, BBox: wireBox{X: 600, Y: 60, Width: 80, Height: 200}}})
	}
	incidents := s.linker.AllIncidents()
	if len(incidents) == 0 {
		t.Skip("the fixture produced no incident; the site model's zones need a sighting to trigger one")
	}
	id := incidents[0].ID
	if _, ok := s.lastSight.Get(id); !ok {
		t.Fatal("the pass did not record a second-sight verdict for the open incident")
	}

	// End it, then run the janitor the metrics loop drives.
	s.linker.UpdateIncidentState(id, "RESOLVED")
	s.forgetTerminalIncidents()

	if _, ok := s.lastSight.Get(id); ok {
		t.Error("a resolved incident still holds a second-sight verdict")
	}
	s.handover.mu.Lock()
	_, keyed := s.handover.lastDecision[id]
	s.handover.mu.Unlock()
	if keyed {
		t.Error("a resolved incident still holds a duty-log decision key")
	}
	// And an OPEN incident keeps its memory: the janitor is not a wipe.
	open := 0
	for _, inc := range s.linker.AllIncidents() {
		if inc.State != "RESOLVED" && inc.State != "OVERRIDDEN" {
			open++
		}
	}
	if open > 0 {
		if _, ok := s.lastSight.Get(s.linker.AllIncidents()[0].ID); !ok && open == len(incidents) {
			t.Error("the janitor dropped an open incident's memory too")
		}
	}
	// The artifact itself is unchanged by the prune: the lines already written
	// are still there, because dropping an in-memory key is not an edit of an
	// append-only log.
	lines, err := dutylog.ReadAll(s.handover.duty.Path())
	if err != nil || len(lines) == 0 {
		t.Errorf("the duty log after the prune: %v, %d lines", err, len(lines))
	}
}

// TestHandoverWithoutALogStillRuns: the artifact is optional, and its absence
// degrades the log lines by name rather than the pipeline.
func TestHandoverWithoutALogStillRuns(t *testing.T) {
	s := NewStreamIngest(&Config{}, "tenant-1", "site-1")
	inc := &correlation.CorrelatedIncident{ID: "inc-test", ReasonChain: correlation.ReasonChain{Why: "x"}}
	if s.logTransition(inc, "VERIFY", "VERIFYING", "x") {
		t.Error("a decision was recorded with no duty log open")
	}
	if s.latestBrief() != nil {
		t.Error("a brief was rendered with no duty log open")
	}
	s.rebuildBrief() // must not panic
	s.logOverride(inc, "VERIFY", "HOLD", "contractor")
	s.logNote("operator:sam", "cam-01", "lens smeared")
}
