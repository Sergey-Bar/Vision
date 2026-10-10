package correlation

import (
	"testing"
	"time"
)

func TestTracker(t *testing.T) {
	cfg := DefaultTrackerConfig()
	tracker := NewTracker(cfg)
	now := time.Now()

	detections := []Detection{
		{ID: "det1", Class: "person", CameraID: "cam1", BBox: BBox{X: 100, Y: 100, Width: 50, Height: 100}, Confidence: 0.9, Timestamp: now},
	}

	tracks := tracker.Update(detections, now)
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if tracks[0].Class != "person" {
		t.Errorf("track class = %s, want person", tracks[0].Class)
	}
	if tracks[0].CameraID != "cam1" {
		t.Errorf("track camera = %s, want cam1", tracks[0].CameraID)
	}

	// Same object, moved a little: the same track, extended.
	detections2 := []Detection{
		{ID: "det2", Class: "person", CameraID: "cam1", BBox: BBox{X: 105, Y: 102, Width: 50, Height: 100}, Confidence: 0.85, Timestamp: now.Add(1 * time.Second)},
	}
	tracks2 := tracker.Update(detections2, now.Add(1*time.Second))
	if len(tracks2) != 1 {
		t.Fatalf("expected the moved object to stay one track, got %d", len(tracks2))
	}
	if len(tracks2[0].DetectionIDs) != 2 {
		t.Errorf("expected 2 detection IDs, got %d", len(tracks2[0].DetectionIDs))
	}

	// A different class is a different entity, on the same camera.
	detections3 := []Detection{
		{ID: "det3", Class: "vehicle", CameraID: "cam1", BBox: BBox{X: 200, Y: 200, Width: 100, Height: 50}, Confidence: 0.9, Timestamp: now.Add(2 * time.Second)},
	}
	tracks3 := tracker.Update(detections3, now.Add(2*time.Second))
	if len(tracks3) != 2 {
		t.Errorf("expected 2 tracks for two classes, got %d", len(tracks3))
	}

	// A far-away detection of the same class is NOT the same entity: a
	// tracker that merges two distant people would correlate incidents that
	// never happened.
	detections4 := []Detection{
		{ID: "det4", Class: "person", CameraID: "cam1", BBox: BBox{X: 1100, Y: 600, Width: 50, Height: 100}, Confidence: 0.9, Timestamp: now.Add(3 * time.Second)},
	}
	tracks4 := tracker.Update(detections4, now.Add(3*time.Second))
	if len(tracks4) != 3 {
		t.Errorf("expected the distant person to be a new track, got %d tracks", len(tracks4))
	}
}

func TestTrackerExpiresIdleTracks(t *testing.T) {
	cfg := DefaultTrackerConfig()
	cfg.MaxAge = 5 * time.Second
	tracker := NewTracker(cfg)
	now := time.Now()

	tracker.Update([]Detection{
		{ID: "d1", Class: "person", CameraID: "cam1", BBox: BBox{X: 10, Y: 10, Width: 20, Height: 40}, Timestamp: now},
	}, now)

	// Nothing seen for longer than maxAge: the track is gone, so an incident
	// cannot grow from an entity that left the scene minutes ago.
	tracks := tracker.Update(nil, now.Add(10*time.Second))
	if len(tracks) != 0 {
		t.Errorf("expected idle tracks to expire, got %d", len(tracks))
	}
}

func TestLinker(t *testing.T) {
	cfg := DefaultLinkerConfig()
	linker := NewLinker("test-site", cfg)
	now := time.Now()

	tracks := []*Track{
		{ID: "track1", Class: "person", CameraID: "cam1", FirstSeen: now, LastSeen: now, Position: Point{X: 100, Y: 100}, BBoxHistory: []BBox{{X: 100, Y: 100, Width: 50, Height: 100}}, DetectionIDs: []string{"det1", "det2"}},
	}

	incidents := linker.Link(tracks, now)
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}

	inc := incidents[0]
	if inc.State != "ACTIVE" {
		t.Errorf("incident state = %s, want ACTIVE", inc.State)
	}
	if len(inc.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(inc.Entities))
	}
	if len(inc.CameraIDs) != 1 {
		t.Errorf("expected 1 camera, got %d", len(inc.CameraIDs))
	}
	if inc.SiteID != "test-site" {
		t.Errorf("incident site = %s, want test-site", inc.SiteID)
	}
	if inc.Provenance.CorrelationID != inc.ID {
		t.Errorf("provenance correlation id = %q, want %q", inc.Provenance.CorrelationID, inc.ID)
	}
	if inc.Action != "OBSERVE" {
		t.Errorf("a fresh incident starts at %s, want OBSERVE", inc.Action)
	}

	// The behaviours the operator surface calls on a linked incident.
	linker.AddAnomaly(inc.ID, Anomaly{Type: "dwell", RuleID: "dwell-x", LastSeen: now})
	if len(inc.Anomalies) != 1 {
		t.Errorf("expected 1 anomaly, got %d", len(inc.Anomalies))
	}
	linker.UpdateIncidentState(inc.ID, "VERIFYING")
	if inc.State != "VERIFYING" {
		t.Errorf("state = %s, want VERIFYING", inc.State)
	}
	linker.UpdateIncidentAction(inc.ID, "VERIFY", "VERIFYING")
	if inc.Action != "VERIFY" || inc.AutonomyState != "VERIFYING" {
		t.Errorf("action/autonomy = %s/%s, want VERIFY/VERIFYING", inc.Action, inc.AutonomyState)
	}
	linker.UpdateIncidentThreatScore(inc.ID, 42, ReasonChain{Why: "dwell>15s"})
	if inc.ThreatScore != 42 || inc.ReasonChain.Why != "dwell>15s" {
		t.Errorf("score/chain = %d/%q, want 42/dwell>15s", inc.ThreatScore, inc.ReasonChain.Why)
	}

	// An unknown id is refused rather than panicking: the operator surface
	// takes ids from a URL.
	linker.AddAnomaly("no-such-incident", Anomaly{Type: "dwell"})
	linker.UpdateIncidentState("no-such-incident", "RESOLVED")
	linker.UpdateIncidentAction("no-such-incident", "HOLD", "HOLD")
	linker.UpdateIncidentThreatScore("no-such-incident", 1, ReasonChain{})
}

func TestLinkerSingleDetectionIsNotAnIncident(t *testing.T) {
	linker := NewLinker("test-site", DefaultLinkerConfig())
	now := time.Now()

	// One frame of one object: one track, one detection. That is a sighting,
	// not an incident, and a linker that raised an incident on it would raise
	// one per frame for the rest of the run.
	tracks := []*Track{
		{ID: "t1", Class: "person", CameraID: "cam1", FirstSeen: now, LastSeen: now, Position: Point{X: 1, Y: 1}, BBoxHistory: []BBox{{X: 1, Y: 1, Width: 10, Height: 20}}, DetectionIDs: []string{"d1"}},
	}
	if got := linker.Link(tracks, now); len(got) != 0 {
		t.Errorf("a single detection produced %d incidents, want 0", len(got))
	}
}

func TestLinkerCrossCamera(t *testing.T) {
	cfg := DefaultLinkerConfig()
	linker := NewLinker("test-site", cfg)
	now := time.Now()

	track1 := &Track{
		ID: "track1", Class: "person", CameraID: "cam1",
		FirstSeen: now, LastSeen: now.Add(10 * time.Second),
		Position:     Point{X: 100, Y: 100},
		BBoxHistory:  []BBox{{X: 100, Y: 100, Width: 50, Height: 100}},
		DetectionIDs: []string{"det1", "det2", "det3"},
	}
	track2 := &Track{
		ID: "track2", Class: "person", CameraID: "cam2",
		FirstSeen: now.Add(15 * time.Second), LastSeen: now.Add(20 * time.Second),
		Position:     Point{X: 200, Y: 200},
		BBoxHistory:  []BBox{{X: 200, Y: 200, Width: 50, Height: 100}},
		DetectionIDs: []string{"det4", "det5"},
	}

	incidents := linker.Link([]*Track{track1, track2}, now.Add(20*time.Second))
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident for cross-camera tracks, got %d", len(incidents))
	}

	inc := incidents[0]
	if len(inc.CameraIDs) != 2 {
		t.Errorf("expected 2 cameras in incident, got %d", len(inc.CameraIDs))
	}
	if len(inc.Entities) != 2 {
		t.Errorf("expected 2 entities in incident, got %d", len(inc.Entities))
	}
}

// TestLinkerResolvesWhenAnEntityLeaves is the lifecycle half: an incident that
// stops receiving detections is resolved, so the operator surface shows what
// is happening now rather than everything that ever happened.
func TestLinkerResolvesWhenAnEntityLeaves(t *testing.T) {
	cfg := DefaultLinkerConfig()
	linker := NewLinker("test-site", cfg)
	now := time.Now()

	linker.Link([]*Track{{
		ID: "t1", Class: "person", CameraID: "cam1",
		FirstSeen: now, LastSeen: now, Position: Point{X: 5, Y: 5},
		BBoxHistory:  []BBox{{X: 5, Y: 5, Width: 10, Height: 20}},
		DetectionIDs: []string{"d1", "d2"},
	}}, now)

	linker.Link(nil, now.Add(3*cfg.MaxTimeGap))
	for _, inc := range linker.AllIncidents() {
		if inc.State != "RESOLVED" {
			t.Errorf("incident %s is %s long after its last detection, want RESOLVED", inc.ID, inc.State)
		}
	}
}

func TestIncidentIDsAreDeterministic(t *testing.T) {
	// Two linkers over the same site, same class, same window and same order
	// must produce the same incident id, because a decision replayed from
	// stored facts has to land on the same identity.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func() string {
		l := NewLinker("site-a", DefaultLinkerConfig())
		out := l.Link([]*Track{{
			ID: "t1", Class: "person", CameraID: "cam1",
			FirstSeen: now, LastSeen: now, Position: Point{X: 1, Y: 1},
			BBoxHistory:  []BBox{{X: 1, Y: 1, Width: 5, Height: 10}},
			DetectionIDs: []string{"d1", "d2"},
		}}, now)
		if len(out) != 1 {
			t.Fatalf("expected one incident, got %d", len(out))
		}
		return out[0].ID
	}
	if a, b := mk(), mk(); a != b {
		t.Errorf("incident ids differ across identical runs: %s vs %s", a, b)
	}
}

func TestDistanceAndBBoxCenter(t *testing.T) {
	if d := distance(Point{}, Point{X: 3, Y: 4}); d != 5 {
		t.Errorf("Distance = %v, want 5", d)
	}
	c := BBoxCenter(BBox{X: 10, Y: 20, Width: 10, Height: 10})
	if c.X != 15 || c.Y != 25 {
		t.Errorf("BBoxCenter = %v, want (15,25)", c)
	}
}
