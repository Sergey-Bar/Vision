package anomaly

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

func TestDwellRule(t *testing.T) {
	site := &sitemodel.Site{
		ID: "test-site",
		Zones: []sitemodel.Zone{
			{ID: "restricted", Type: "restricted", Name: "Restricted", Polygon: []sitemodel.Point{
				{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
			}},
		},
	}

	rule := newDwellRule("restricted", 10*time.Second)
	now := time.Now()

	inc := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent1",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(-15 * time.Second),
				LastSeen:  now,
				BBoxHistory: []correlation.BBox{
					{X: 50, Y: 50, Width: 20, Height: 40},
				},
			},
		},
	}

	anomalies := rule.Evaluate(inc, site, now)
	if len(anomalies) != 1 {
		t.Errorf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Type != "dwell" {
		t.Errorf("anomaly type = %s, want dwell", anomalies[0].Type)
	}
	if anomalies[0].RuleID != "dwell-restricted" {
		t.Errorf("anomaly rule = %s, want dwell-restricted", anomalies[0].RuleID)
	}

	// Test with insufficient dwell time
	inc2 := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent2",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(-5 * time.Second),
				LastSeen:  now,
				BBoxHistory: []correlation.BBox{
					{X: 50, Y: 50, Width: 20, Height: 40},
				},
			},
		},
	}
	anomalies2 := rule.Evaluate(inc2, site, now)
	if len(anomalies2) != 0 {
		t.Errorf("expected 0 anomalies for short dwell, got %d", len(anomalies2))
	}

	// Test with entity not in zone
	inc3 := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent3",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(-15 * time.Second),
				LastSeen:  now,
				BBoxHistory: []correlation.BBox{
					{X: 200, Y: 200, Width: 20, Height: 40},
				},
			},
		},
	}
	anomalies3 := rule.Evaluate(inc3, site, now)
	if len(anomalies3) != 0 {
		t.Errorf("expected 0 anomalies for entity outside zone, got %d", len(anomalies3))
	}
}

func TestLoiterRule(t *testing.T) {
	site := &sitemodel.Site{
		ID: "test-site",
		Zones: []sitemodel.Zone{
			{ID: "monitor", Type: "monitor", Name: "Monitor", Polygon: []sitemodel.Point{
				{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
			}},
		},
	}

	rule := newLoiterRule("monitor", 3, 60*time.Second)
	now := time.Now()

	// Entity that revisits the zone 3 times
	inc := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent1",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(-50 * time.Second),
				LastSeen:  now,
				BBoxHistory: []correlation.BBox{
					{X: 10, Y: 10, Width: 20, Height: 40},   // in zone
					{X: 200, Y: 200, Width: 20, Height: 40}, // out
					{X: 15, Y: 15, Width: 20, Height: 40},   // in zone
					{X: 210, Y: 210, Width: 20, Height: 40}, // out
					{X: 20, Y: 20, Width: 20, Height: 40},   // in zone
				},
			},
		},
	}

	anomalies := rule.Evaluate(inc, site, now)
	if len(anomalies) != 1 {
		t.Errorf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Type != "loiter" {
		t.Errorf("anomaly type = %s, want loiter", anomalies[0].Type)
	}
	if anomalies[0].Details["revisits"] != 3 {
		t.Errorf("revisits = %v, want 3", anomalies[0].Details["revisits"])
	}
}

func TestFlowRule(t *testing.T) {
	site := &sitemodel.Site{
		ID: "test-site",
		Routes: []sitemodel.Route{
			{ID: "route-1", Points: []sitemodel.Point{
				{X: 0, Y: 100}, {X: 100, Y: 100}, {X: 200, Y: 100},
			}, AllowedDirection: "forward"},
		},
	}

	rule := newFlowRule("route-1")
	now := time.Now()

	// Entity moving opposite to allowed direction (right to left)
	inc := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent1",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(-10 * time.Second),
				LastSeen:  now,
				BBoxHistory: []correlation.BBox{
					{X: 190, Y: 90, Width: 20, Height: 40}, // right side
					{X: 10, Y: 90, Width: 20, Height: 40},  // left side (wrong way)
				},
			},
		},
	}

	anomalies := rule.Evaluate(inc, site, now)
	if len(anomalies) != 1 {
		t.Errorf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Type != "flow" {
		t.Errorf("anomaly type = %s, want flow", anomalies[0].Type)
	}

	// Entity moving in correct direction
	inc2 := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent2",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(-10 * time.Second),
				LastSeen:  now,
				BBoxHistory: []correlation.BBox{
					{X: 10, Y: 90, Width: 20, Height: 40},  // left side
					{X: 190, Y: 90, Width: 20, Height: 40}, // right side (correct)
				},
			},
		},
	}
	anomalies2 := rule.Evaluate(inc2, site, now)
	if len(anomalies2) != 0 {
		t.Errorf("expected 0 anomalies for correct direction, got %d", len(anomalies2))
	}
}

func TestTailgateRule(t *testing.T) {
	site := &sitemodel.Site{
		ID: "test-site",
		Zones: []sitemodel.Zone{
			{ID: "choke", Type: "choke", Name: "Choke", Polygon: []sitemodel.Point{
				{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
			}},
		},
	}

	rule := newTailgateRule("choke", 50.0, 5*time.Second)
	now := time.Now()

	// Two entities entering close in space and time
	inc := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent1",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now,
				LastSeen:  now.Add(5 * time.Second),
				BBoxHistory: []correlation.BBox{
					{X: 20, Y: 20, Width: 20, Height: 40},
				},
			},
			{
				ID:        "ent2",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(2 * time.Second),
				LastSeen:  now.Add(7 * time.Second),
				BBoxHistory: []correlation.BBox{
					{X: 30, Y: 30, Width: 20, Height: 40},
				},
			},
		},
	}

	anomalies := rule.Evaluate(inc, site, now.Add(10*time.Second))
	if len(anomalies) != 1 {
		t.Errorf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Type != "tailgate" {
		t.Errorf("anomaly type = %s, want tailgate", anomalies[0].Type)
	}
	if len(anomalies[0].EntityIDs) != 2 {
		t.Errorf("expected 2 entity IDs, got %d", len(anomalies[0].EntityIDs))
	}

	// Test with entities too far apart in time
	inc2 := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{
			{
				ID:        "ent3",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now,
				LastSeen:  now.Add(5 * time.Second),
				BBoxHistory: []correlation.BBox{
					{X: 20, Y: 20, Width: 20, Height: 40},
				},
			},
			{
				ID:        "ent4",
				Class:     "person",
				CameraID:  "cam1",
				FirstSeen: now.Add(10 * time.Second), // too late
				LastSeen:  now.Add(15 * time.Second),
				BBoxHistory: []correlation.BBox{
					{X: 30, Y: 30, Width: 20, Height: 40},
				},
			},
		},
	}
	anomalies2 := rule.Evaluate(inc2, site, now.Add(20*time.Second))
	if len(anomalies2) != 0 {
		t.Errorf("expected 0 anomalies for large time gap, got %d", len(anomalies2))
	}
}

func TestDefaultRuleSet(t *testing.T) {
	site := &sitemodel.Site{
		ID: "test-site",
		Zones: []sitemodel.Zone{
			{ID: "restricted", Type: "restricted", Name: "Restricted", Polygon: []sitemodel.Point{
				{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
			}},
			{ID: "choke", Type: "choke", Name: "Choke", Polygon: []sitemodel.Point{
				{X: 200, Y: 200}, {X: 300, Y: 200}, {X: 300, Y: 300}, {X: 200, Y: 300},
			}},
			{ID: "monitor", Type: "monitor", Name: "Monitor", Polygon: []sitemodel.Point{
				{X: 400, Y: 400}, {X: 500, Y: 400}, {X: 500, Y: 500}, {X: 400, Y: 500},
			}},
		},
		Routes: []sitemodel.Route{
			{ID: "route-1", Points: []sitemodel.Point{{X: 0, Y: 0}, {X: 100, Y: 100}}},
		},
	}

	rs := DefaultRuleSet(site)
	if len(rs.rules) == 0 {
		t.Fatal("default rule set should have rules for a site with zones and a route")
	}

	// Every behaviour the site declared a zone or route for gets a rule, or
	// that behaviour is silently unchecked.
	kinds := map[string]int{}
	for _, r := range rs.rules {
		name := r.Name()
		if i := indexByte(name, 0x2d /* - */); i > 0 {
			kinds[name[:i]]++
		}
	}
	for _, want := range []string{"dwell", "loiter", "flow", "tailgate"} {
		if kinds[want] == 0 {
			t.Errorf("default rule set has no %s rule", want)
		}
	}

	// A restricted-zone dwell is scored high enough for the playbook to verify
	// it: the severity is the zone''s, not a shareable default, and the
	// alternative is an autonomy badge that never leaves AUTO.
	for _, r := range rs.rules {
		if d, ok := r.(*DwellRule); ok && d.ZoneID == "restricted" && d.MinSeverity != "high" {
			t.Errorf("restricted-zone dwell severity = %q, want high", d.MinSeverity)
		}
	}
}

// TestRuleSetEvaluatesEveryRuleSequentially checks the set is a fold over its
// rules rather than a single rule: every rule gets the same incident, so a
// second behaviour in the same window is found rather than shadowing the first.
func TestRuleSetEvaluatesEveryRuleSequentially(t *testing.T) {
	site := &sitemodel.Site{
		ID: "test-site",
		Zones: []sitemodel.Zone{
			{ID: "restricted", Type: "restricted", Name: "Restricted", Polygon: []sitemodel.Point{
				{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100},
			}},
		},
	}

	rs := newRuleSet(
		newDwellRule("restricted", 10*time.Second),
		newDwellRule("restricted", 20*time.Second),
		newLoiterRule("restricted", 3, 60*time.Second),
	)
	now := time.Now()
	inc := &correlation.CorrelatedIncident{
		Entities: []correlation.EntityRef{{
			ID: "e1", Class: "person", CameraID: "cam1",
			FirstSeen: now.Add(-25 * time.Second), LastSeen: now,
			BBoxHistory: []correlation.BBox{
				{X: 10, Y: 10, Width: 20, Height: 40},
				{X: 15, Y: 15, Width: 20, Height: 40},
				{X: 20, Y: 20, Width: 20, Height: 40},
			},
		}},
	}

	var dwells, loiters int
	for _, a := range rs.Evaluate(inc, site, now) {
		switch a.Type {
		case "dwell":
			dwells++
		case "loiter":
			loiters++
		}
	}
	if dwells != 2 {
		t.Errorf("got %d dwell anomalies, want 2 (both thresholds crossed)", dwells)
	}
	if loiters != 1 {
		t.Errorf("got %d loiter anomalies, want 1", loiters)
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
