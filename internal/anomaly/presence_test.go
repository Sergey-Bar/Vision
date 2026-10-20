package anomaly_test

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
)

// The presence rule fires on a class that has been in a zone for long enough.
//
// The rule is the PRODUCT's anomaly, and the test is about what it claims: a
// class, in a zone, seen by a device, over a window. Not an entity -- the
// product's record has no entities to track, and a rule that claimed one would
// be claiming something the record threw away.
func TestThePresenceRuleFiresOnAClassThatStayed(t *testing.T) {
	site := &sitemodel.Site{
		ID: "site",
		Zones: []sitemodel.Zone{
			{ID: "zone-restricted", Name: "Restricted", Type: "restricted",
				Polygon: []sitemodel.Point{{X: 0, Y: 0}, {X: 100, Y: 0}, {X: 100, Y: 100}, {X: 0, Y: 100}}},
		},
	}
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	inc := &correlation.CorrelatedIncident{
		ID:        "inc-1",
		SiteID:    "site",
		FirstSeen: now.Add(-30 * time.Second),
		LastSeen:  now,
		CameraIDs: []string{"cam-1"},
		Provenance: correlation.ProvenanceExt{
			SiteID: "site", CameraID: "cam-1", ZoneID: "zone-restricted",
		},
	}
	rules := anomaly.PresenceRuleSet(30 * time.Second)
	anomalies := rules.Evaluate(inc, site, now)
	if len(anomalies) != 1 {
		t.Fatalf("a class present for 30 seconds in a restricted zone produced %d anomalies, want one", len(anomalies))
	}
	a := anomalies[0]
	if a.Type != "presence" {
		t.Errorf("the anomaly is a %q, want presence", a.Type)
	}
	if a.ZoneID != "zone-restricted" {
		t.Errorf("the anomaly is about %q, want zone-restricted", a.ZoneID)
	}
	if a.Severity != "high" {
		t.Errorf("a presence in a restricted zone is %q, want high: the zone's type is where the severity comes from", a.Severity)
	}
	if a.Details["zone_type"] != "restricted" {
		t.Errorf("the anomaly does not carry the zone's type, and the score is a multiple of it: %v", a.Details)
	}
	if a.Details["present_time_seconds"] != float64(30) {
		t.Errorf("the anomaly says the class was present for %v seconds, want 30", a.Details["present_time_seconds"])
	}
}

// A class that was there for a moment is not a presence.
//
// The threshold is half the window, and the test is that the rule does not fire
// below it -- a rule that fires on everything is a rule that ranks nothing.
func TestThePresenceRuleDoesNotFireOnAMoment(t *testing.T) {
	site := &sitemodel.Site{
		ID:    "site",
		Zones: []sitemodel.Zone{{ID: "zone-restricted", Type: "restricted"}},
	}
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	inc := &correlation.CorrelatedIncident{
		ID:         "inc-1",
		SiteID:     "site",
		FirstSeen:  now.Add(-2 * time.Second),
		LastSeen:   now,
		CameraIDs:  []string{"cam-1"},
		Provenance: correlation.ProvenanceExt{ZoneID: "zone-restricted"},
	}
	anomalies := anomaly.PresenceRuleSet(30*time.Second).Evaluate(inc, site, now)
	if len(anomalies) != 0 {
		t.Errorf("a class present for two seconds produced %d anomalies, want none", len(anomalies))
	}
}

// An incident with no zone cannot be a presence.
//
// "We do not know where this happened" and "it happened in the restricted zone"
// are different facts, and only the second one is a finding.
func TestThePresenceRuleNeedsAZone(t *testing.T) {
	site := &sitemodel.Site{ID: "site", Zones: []sitemodel.Zone{{ID: "zone-restricted", Type: "restricted"}}}
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	inc := &correlation.CorrelatedIncident{
		ID: "inc-1", SiteID: "site",
		FirstSeen: now.Add(-30 * time.Second), LastSeen: now,
		CameraIDs: []string{"cam-1"},
	}
	if got := anomaly.PresenceRuleSet(30*time.Second).Evaluate(inc, site, now); len(got) != 0 {
		t.Errorf("an incident with no zone produced %d anomalies, want none", len(got))
	}
}

// A nil site is a legitimate construction.
//
// The product's no-site path builds the rule set before a model is loaded, and it
// must yield an empty answer rather than a nil dereference.
func TestThePresenceRuleSurvivesANilSite(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	inc := &correlation.CorrelatedIncident{
		ID: "inc-1", SiteID: "site",
		FirstSeen: now.Add(-30 * time.Second), LastSeen: now,
		Provenance: correlation.ProvenanceExt{ZoneID: "zone-restricted"},
	}
	anomalies := anomaly.PresenceRuleSet(30*time.Second).Evaluate(inc, nil, now)
	if len(anomalies) != 1 {
		t.Fatalf("a presence with no site model produced %d anomalies, want one with a neutral severity", len(anomalies))
	}
	if anomalies[0].Details["zone_type"] != "" {
		t.Errorf("the anomaly claims a zone type without a site model: %v", anomalies[0].Details)
	}
}
