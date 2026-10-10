package anomaly

import (
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/correlation"
	"github.com/Sergey-Bar/Ai-Captain/internal/sitemodel"
)

func coverageSite(t *testing.T) *sitemodel.Site {
	t.Helper()
	site, err := sitemodel.LoadFromBytes([]byte(`
site:
  id: "cov-site"
  cameras:
    - id: "cam-01"
      zone_ids: ["restricted", "entrance"]
    - id: "cam-02"
      zone_ids: ["restricted", "parking"]
  zones:
    - id: "restricted"
      type: "restricted"
      polygon:
        - {x: 0, y: 0}
        - {x: 100, y: 0}
        - {x: 100, y: 100}
        - {x: 0, y: 100}
    - id: "entrance"
      type: "choke"
      polygon:
        - {x: 200, y: 0}
        - {x: 300, y: 0}
        - {x: 300, y: 100}
        - {x: 200, y: 100}
    - id: "parking"
      type: "monitor"
      polygon:
        - {x: 400, y: 0}
        - {x: 500, y: 0}
        - {x: 500, y: 100}
        - {x: 400, y: 100}
    - id: "far-side"
      type: "monitor"
      polygon:
        - {x: 600, y: 0}
        - {x: 700, y: 0}
        - {x: 700, y: 100}
        - {x: 600, y: 100}
`))
	if err != nil {
		t.Fatalf("load site: %v", err)
	}
	return site
}

// TestUnderObservedZonesIsDeclarationBased is the core contract: a zone whose
// every declared camera is degraded is under-observed, and a zone nobody
// declared is unanswerable rather than fine.
func TestUnderObservedZonesIsDeclarationBased(t *testing.T) {
	site := coverageSite(t)
	// cam-01 is the only camera for entrance, and it is degraded.
	// restricted has cam-01 (degraded) and cam-02 (live), so it is covered.
	degraded := map[string]bool{"cam-01": true}
	got := UnderObservedZones(site, func(id string) bool { return degraded[id] })

	byZone := map[string]ZoneCoverage{}
	for _, z := range got {
		byZone[z.ZoneID] = z
	}
	if !byZone["entrance"].UnderObserved {
		t.Error("entrance's only declared camera is degraded; it should be under-observed")
	}
	if len(byZone["entrance"].DegradedCameras) != 1 || byZone["entrance"].DegradedCameras[0] != "cam-01" {
		t.Errorf("entrance degraded cameras = %v", byZone["entrance"].DegradedCameras)
	}
	if byZone["restricted"].UnderObserved {
		t.Error("restricted still has a live camera; it is not under-observed")
	}
	if byZone["far-side"].UnderObserved || !byZone["far-side"].Unanswerable {
		t.Errorf("far-side has no declared camera; it should be unanswerable, not under-observed (%+v)", byZone["far-side"])
	}
	// Sorted by zone id so a brief's coverage section is stable.
	ids := make([]string, len(got))
	for i, z := range got {
		ids[i] = z.ZoneID
	}
	if strings.Join(ids, ",") != "entrance,far-side,parking,restricted" {
		t.Errorf("zone order = %v", ids)
	}
}

// TestUnderObservedZonesFailsSafeOnUnknownCameras: a camera the health source
// has no answer for counts as degraded, because a site would rather be told
// "nobody is watching this" than be told a zone is covered by a camera it
// cannot see.
func TestUnderObservedZonesFailsSafeOnUnknownCameras(t *testing.T) {
	site := coverageSite(t)
	known := map[string]bool{"cam-01": false, "cam-02": false}
	got := UnderObservedZones(site, func(id string) bool { return !known[id] })
	for _, z := range got {
		if z.ZoneID == "restricted" && !z.UnderObserved {
			t.Error("a camera with no health answer should read as degraded, not as covered")
		}
	}
}

// TestUnderObservedZonesWithoutHealthSource: with no health source at all the
// declarations are still reported, and nothing is claimed in either direction.
func TestUnderObservedZonesWithoutHealthSource(t *testing.T) {
	site := coverageSite(t)
	got := UnderObservedZones(site, nil)
	for _, z := range got {
		if z.UnderObserved || len(z.DegradedCameras) != 0 {
			t.Errorf("%s claims coverage with no health source: %+v", z.ZoneID, z)
		}
	}
	if UnderObservedZones(nil, nil) != nil {
		t.Error("a nil site produced coverage rows")
	}
}

// TestCoverageRuleAttachesUnderObserved is the seam: an incident inside a
// zone nobody is watching carries an under-observed anomaly naming the zone,
// and an incident in a covered zone carries none.
func TestCoverageRuleAttachesUnderObserved(t *testing.T) {
	site := coverageSite(t)
	degraded := map[string]bool{"cam-02": true}
	rule := NewCoverageRule(func(id string) bool { return degraded[id] })

	// An entity standing in the parking zone, whose only camera is degraded.
	inc := &correlation.CorrelatedIncident{
		ID: "inc-cov", CameraIDs: []string{"cam-02"},
		Entities: []correlation.EntityRef{{
			ID: "cam-02/t1", Class: "person", CameraID: "cam-02",
			BBoxHistory: []correlation.BBox{{X: 450, Y: 40, Width: 30, Height: 80}},
		}},
	}
	got := rule.Evaluate(inc, site, time.Now())
	if len(got) != 1 {
		t.Fatalf("coverage rule returned %d anomalies, want 1", len(got))
	}
	a := got[0]
	if a.Type != AnomalyUnderObserved || a.ZoneID != "parking" || a.RuleID != rule.Name() {
		t.Errorf("anomaly = %s/%s/%s", a.Type, a.ZoneID, a.RuleID)
	}
	if a.Details["zone_type"] != "monitor" {
		t.Errorf("zone type = %v, want monitor", a.Details["zone_type"])
	}
	if a.Details["degraded_cameras"] == nil {
		t.Error("the anomaly does not name the degraded cameras")
	}

	// The same entity in a covered zone gets no coverage anomaly.
	inc.Entities[0].BBoxHistory = []correlation.BBox{{X: 60, Y: 20, Width: 30, Height: 80}}
	if got := rule.Evaluate(inc, site, time.Now()); len(got) != 0 {
		t.Errorf("an incident in a covered zone carries %d coverage anomalies", len(got))
	}

	// No health source: the rule declines to guess.
	if got := NewCoverageRule(nil).Evaluate(inc, site, time.Now()); got != nil {
		t.Error("a coverage rule with no health source produced anomalies")
	}
}
