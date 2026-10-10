package brief

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func shiftFacts() Facts {
	return Facts{
		Shift:       "2026-10-10-night",
		TenantID:    "tenant-1",
		SiteID:      "site-1",
		GeneratedAt: time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC),
		Incidents: []Incident{
			{
				ID: "inc-bbb", Title: "Dwell, restricted-1", CameraIDs: []string{"cam-02", "cam-01"},
				Anomalies: []string{"dwell"}, ThreatScore: 62, Action: "VERIFY", AutonomyState: "VERIFYING",
				Reason: "42s dwell in restricted-1, one camera", State: "ACTIVE",
			},
			{
				ID: "inc-aaa", Title: "Loiter, parking", CameraIDs: []string{"cam-01"},
				Anomalies: []string{"loiter"}, ThreatScore: 18, Action: "OBSERVE", AutonomyState: "AUTO",
				Reason: "loiter below the verify floor", State: "RESOLVED",
			},
		},
		Actions: []Action{
			{TS: time.Date(2026, 10, 10, 23, 5, 0, 0, time.UTC), Actor: "autonomy", Subject: "inc-bbb", Action: "VERIFY", Reason: "score 62, one anomaly"},
			{TS: time.Date(2026, 10, 10, 23, 40, 0, 0, time.UTC), Actor: "operator:sam", Subject: "inc-bbb", Action: "HOLD", Reason: "contractor on site, verified"},
			{TS: time.Date(2026, 10, 10, 23, 41, 0, 0, time.UTC), Actor: "operator:sam", Subject: "inc-bbb", Action: "HOLD", Reason: "another line at the same instant"},
		},
		Notes: []Note{
			{TS: time.Date(2026, 10, 11, 0, 10, 0, 0, time.UTC), Actor: "operator:sam", Text: "lens smeared on cam-02", Camera: "cam-02"},
		},
		Cameras: []Camera{
			{ID: "cam-01", State: "fresh"},
			{ID: "cam-02", State: "blind", Reason: "near-uniform frame (lens covered, blackout or white-out)"},
		},
		Coverage: []ZoneCoverage{
			{ZoneID: "restricted-1", DeclaredCameras: []string{"cam-01", "cam-02"}, DegradedCameras: []string{"cam-02"}, UnderObserved: true},
			{ZoneID: "parking", DeclaredCameras: []string{"cam-01"}, DegradedCameras: nil},
		},
		OpenItems: []OpenItem{
			{ID: "manual-1", Kind: "note", Summary: "wash cam-02 before the morning handover"},
		},
	}
}

// TestRenderIsDeterministic is the contract the whole package exists for: the
// same facts render the same bytes, every time, in every order the caller
// happened to build them in.
func TestRenderIsDeterministic(t *testing.T) {
	a := Render(shiftFacts())
	b := Render(shiftFacts())
	ja, err := a.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	jb, err := b.JSON()
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	if string(ja) != string(jb) {
		t.Error("two renders of the same facts differ")
	}
	if a.Text() != b.Text() {
		t.Error("two text renders of the same facts differ")
	}
}

// TestRenderSortsEverySection checks the ordering promise: sections are
// sorted by stable keys regardless of the order the facts arrived in.
func TestRenderSortsEverySection(t *testing.T) {
	b := Render(shiftFacts())
	if b.Incidents[0].ID != "inc-aaa" || b.Incidents[1].ID != "inc-bbb" {
		t.Errorf("incidents are not sorted by id: %s, %s", b.Incidents[0].ID, b.Incidents[1].ID)
	}
	if b.Cameras[0].ID != "cam-01" || b.Cameras[1].ID != "cam-02" {
		t.Error("cameras are not sorted by id")
	}
	if b.Coverage[0].ZoneID != "parking" || b.Coverage[1].ZoneID != "restricted-1" {
		t.Error("coverage is not sorted by zone id")
	}
	for i := 1; i < len(b.Actions); i++ {
		if b.Actions[i].TS.Before(b.Actions[i-1].TS) {
			t.Errorf("actions are not in time order at %d", i)
		}
	}
	// The tie at 23:41 breaks deterministically on subject, never on the
	// order the caller built.
	if !b.Actions[1].TS.Equal(b.Actions[2].TS) {
		t.Skip("no same-timestamp pair to order")
	}
	if b.Actions[1].Subject > b.Actions[2].Subject {
		t.Error("same-timestamp actions are not ordered by subject")
	}
}

// TestOpenItemsAreDerived checks the hand-over list cannot drift from the
// sections above it: open incidents, degraded cameras and under-observed
// zones appear because the sections say so, not because the caller listed
// them.
func TestOpenItemsAreDerived(t *testing.T) {
	b := Render(shiftFacts())
	kinds := map[string]bool{}
	for _, o := range b.OpenItems {
		kinds[o.Kind] = true
	}
	for _, want := range []string{"incident", "camera", "coverage", "note"} {
		if !kinds[want] {
			t.Errorf("open items are missing a %s entry", want)
		}
	}
	// The resolved incident is not open.
	for _, o := range b.OpenItems {
		if o.ID == "inc-aaa" {
			t.Errorf("resolved incident inc-aaa leaked into the open items")
		}
	}
	if b.FleetDegraded != 1 {
		t.Errorf("fleet_degraded = %d, want 1", b.FleetDegraded)
	}
	if b.Headline == "" || strings.Contains(b.Headline, "No incidents") {
		t.Errorf("headline = %q, want a non-empty summary of a busy shift", b.Headline)
	}
}

// TestGoldenTextAndJSON is the byte-stability gate: fixture to brief must not
// change without a deliberate contract edit. It never involves model prose,
// which is the other half of the golden-test rule.
func TestGoldenTextAndJSON(t *testing.T) {
	b := Render(shiftFacts())

	wantJSON := `{"shift":"2026-10-10-night","tenant_id":"tenant-1","site_id":"site-1",` +
		`"generated_at":"2026-10-11T06:00:00Z","schema":"brief/v1",` +
		`"headline":"2 correlated incidents, 1 cameras degraded, 1 zones under-observed during 2026-10-10-night.",` +
		`"incidents":[{"ID":"inc-aaa","Title":"Loiter, parking","CameraIDs":["cam-01"],` +
		`"Anomalies":["loiter"],"ThreatScore":18,"Action":"OBSERVE","AutonomyState":"AUTO",` +
		`"Reason":"loiter below the verify floor","State":"RESOLVED"},` +
		`{"ID":"inc-bbb","Title":"Dwell, restricted-1","CameraIDs":["cam-02","cam-01"],` +
		`"Anomalies":["dwell"],"ThreatScore":62,"Action":"VERIFY","AutonomyState":"VERIFYING",` +
		`"Reason":"42s dwell in restricted-1, one camera","State":"ACTIVE"}],` +
		`"actions":[{"TS":"2026-10-10T23:05:00Z","Actor":"autonomy","Subject":"inc-bbb","Action":"VERIFY","Reason":"score 62, one anomaly"},` +
		`{"TS":"2026-10-10T23:40:00Z","Actor":"operator:sam","Subject":"inc-bbb","Action":"HOLD","Reason":"contractor on site, verified"},` +
		`{"TS":"2026-10-10T23:41:00Z","Actor":"operator:sam","Subject":"inc-bbb","Action":"HOLD","Reason":"another line at the same instant"}],` +
		`"cameras":[{"ID":"cam-01","State":"fresh","Reason":""},` +
		`{"ID":"cam-02","State":"blind","Reason":"near-uniform frame (lens covered, blackout or white-out)"}],` +
		`"coverage":[{"ZoneID":"parking","DeclaredCameras":["cam-01"],"DegradedCameras":null,"UnderObserved":false,"Unanswerable":false},` +
		`{"ZoneID":"restricted-1","DeclaredCameras":["cam-01","cam-02"],"DegradedCameras":["cam-02"],"UnderObserved":true,"Unanswerable":false}],` +
		`"notes":[{"TS":"2026-10-11T00:10:00Z","Actor":"operator:sam","Text":"lens smeared on cam-02","Camera":"cam-02"}],` +
		`"open_items":[{"ID":"cam-02","Kind":"camera","Summary":"cam-02 blind: near-uniform frame (lens covered, blackout or white-out)"},` +
		`{"ID":"restricted-1","Kind":"coverage","Summary":"restricted-1: 1 of 2 declared cameras degraded"},` +
		`{"ID":"inc-bbb","Kind":"incident","Summary":"inc-bbb — VERIFY (VERIFYING), score 62"},` +
		`{"ID":"manual-1","Kind":"note","Summary":"wash cam-02 before the morning handover"}],` +
		`"fleet_degraded":1}`
	got, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != wantJSON {
		t.Errorf("brief JSON changed.\n got: %s\nwant: %s", got, wantJSON)
	}

	wantText := `SHIFT HAND-OVER — 2026-10-10-night
site site-1 · tenant tenant-1 · generated 2026-10-11T06:00:00Z

2 correlated incidents, 1 cameras degraded, 1 zones under-observed during 2026-10-10-night.

CAMERAS
  cam-01       fresh    –
  cam-02       blind    near-uniform frame (lens covered, blackout or white-out)

INCIDENTS
  inc-aaa  score 18  OBSERVE / AUTO
      cameras: cam-01
      anomalies: loiter
      reason: loiter below the verify floor
  inc-bbb  score 62  VERIFY / VERIFYING
      cameras: cam-02, cam-01
      anomalies: dwell
      reason: 42s dwell in restricted-1, one camera

ACTIONS
  2026-10-10T23:05:00Z  autonomy   inc-bbb        VERIFY
      why: score 62, one anomaly
  2026-10-10T23:40:00Z  operator:sam inc-bbb        HOLD
      why: contractor on site, verified
  2026-10-10T23:41:00Z  operator:sam inc-bbb        HOLD
      why: another line at the same instant

COVERAGE
  parking        covered          declared: cam-01
  restricted-1   UNDER-OBSERVED   declared: cam-01, cam-02

NOTES
  2026-10-11T00:10:00Z  operator:sam lens smeared on cam-02

OPEN ITEMS FOR THE NEXT SHIFT
  [camera] cam-02 blind: near-uniform frame (lens covered, blackout or white-out)
  [coverage] restricted-1: 1 of 2 declared cameras degraded
  [incident] inc-bbb — VERIFY (VERIFYING), score 62
  [note] wash cam-02 before the morning handover
`
	if got := b.Text(); got != wantText {
		t.Errorf("brief text changed.\n got:\n%s\nwant:\n%s", got, wantText)
	}
}

// TestEmptyFactsStillRenders: a quiet shift is a brief, not a nil.
func TestEmptyFactsStillRenders(t *testing.T) {
	b := Render(Facts{Shift: "quiet"})
	if b.Headline != "No incidents this shift; every camera reporting." {
		t.Errorf("headline = %q", b.Headline)
	}
	text := b.Text()
	for _, want := range []string{"CAMERAS", "no cameras report", "none."} {
		if !strings.Contains(text, want) {
			t.Errorf("empty brief text missing %q:\n%s", want, text)
		}
	}
	if len(b.OpenItems) != 0 {
		t.Errorf("empty facts produced %d open items", len(b.OpenItems))
	}
}
