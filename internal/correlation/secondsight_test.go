package correlation

import (
	"strings"
	"testing"
	"time"
)

func sightIncident(cams ...string) *CorrelatedIncident {
	return &CorrelatedIncident{
		ID:        "inc-test",
		SiteID:    "site-1",
		CameraIDs: cams,
		FirstSeen: time.Now(),
		LastSeen:  time.Now(),
		Anomalies: []Anomaly{{
			Type:      "dwell",
			ZoneID:    "restricted",
			CameraID:  cams[0],
			EntityIDs: []string{"cam-01/p1"},
		}},
	}
}

func trackSet(cams ...string) []*Track {
	now := time.Now()
	out := make([]*Track, 0, len(cams))
	for i, c := range cams {
		out = append(out, &Track{
			ID:           "t" + string(rune('a'+i)),
			Class:        "person",
			CameraID:     c,
			FirstSeen:    now,
			LastSeen:     now,
			DetectionIDs: []string{c + "/t" + string(rune('a'+i))},
		})
	}
	return out
}

// TestSecondSightCorroborated: two witnesses is the only corroboration there
// is, and it comes from the incident's own cameras.
func TestSecondSightCorroborated(t *testing.T) {
	sight := SecondSight(sightIncident("cam-01", "cam-02"), trackSet("cam-01"), zoneMap())
	if !sight.Corroborated() || sight.Verdict != SightCorroborated {
		t.Fatalf("verdict = %q, want %q", sight.Verdict, SightCorroborated)
	}
	if len(sight.Cameras) != 2 {
		t.Errorf("witnesses = %v, want both cameras", sight.Cameras)
	}
	if !strings.Contains(sight.Reason, "corroborated by 2 cameras") {
		t.Errorf("reason = %q", sight.Reason)
	}
}

// TestSecondSightSingleSighting: one witness, with the other declared cameras
// demonstrably watching, is the case the 65.6% asked about — and the honest
// answer is that it is one witness.
func TestSecondSightSingleSighting(t *testing.T) {
	sight := SecondSight(sightIncident("cam-01"), trackSet("cam-01", "cam-02"), zoneMap())
	if sight.Verdict != SightSingle {
		t.Fatalf("verdict = %q, want %q", sight.Verdict, SightSingle)
	}
	if len(sight.Declared) != 1 || sight.Declared[0] != "cam-02" {
		t.Errorf("watching cameras = %v, want [cam-02]", sight.Declared)
	}
	if !strings.Contains(sight.Reason, "single sighting by cam-01") {
		t.Errorf("reason = %q", sight.Reason)
	}
}

// TestSecondSightCannotTellIsNeverANegative is the invariant that makes this
// function safe: a camera with no tracks has said nothing, and the verdict
// says so rather than converting silence into "nothing was there".
func TestSecondSightCannotTellIsNeverANegative(t *testing.T) {
	// No declaration loaded at all.
	sight := SecondSight(sightIncident("cam-01"), trackSet("cam-01"), nil)
	if sight.Verdict != SightCannotTell {
		t.Errorf("nil declaration map: verdict = %q, want %q", sight.Verdict, SightCannotTell)
	}
	if strings.Contains(strings.ToLower(sight.Reason), "corroborated") {
		t.Errorf("a cannot-tell reason must not claim corroboration: %q", sight.Reason)
	}

	// The other declared camera is not producing tracks: its silence proves
	// nothing, so the answer is cannot_tell, not single_sighting.
	sight = SecondSight(sightIncident("cam-01"), trackSet("cam-01"), zoneMap())
	if sight.Verdict != SightCannotTell {
		t.Errorf("silent second camera: verdict = %q, want %q", sight.Verdict, SightCannotTell)
	}
	if !strings.Contains(sight.Reason, "proves nothing") {
		t.Errorf("reason = %q, want the silence-is-not-evidence wording", sight.Reason)
	}

	// A zone covered by one declared camera cannot answer either.
	inc := sightIncident("cam-03")
	sight = SecondSight(inc, trackSet("cam-03"), zoneMap())
	if sight.Verdict != SightCannotTell {
		t.Errorf("one-camera zone: verdict = %q, want %q", sight.Verdict, SightCannotTell)
	}

	// No zone attribution on the incident: nothing to check against.
	inc = sightIncident("cam-01")
	inc.Anomalies = nil
	sight = SecondSight(inc, trackSet("cam-01", "cam-02"), zoneMap())
	if sight.Verdict != SightCannotTell {
		t.Errorf("zone-less incident: verdict = %q, want %q", sight.Verdict, SightCannotTell)
	}

	// A nil incident cannot be corroborated either.
	if SecondSight(nil, nil, zoneMap()).Verdict != SightCannotTell {
		t.Error("a nil incident was not answered cannot_tell")
	}
}

// TestSecondSightIsDeterministic: the same facts give the same verdict and
// the same lists, whatever order the cameras arrive in.
func TestSecondSightIsDeterministic(t *testing.T) {
	a := SecondSight(sightIncident("cam-01"), trackSet("cam-02", "cam-03", "cam-01"), zoneMap())
	b := SecondSight(sightIncident("cam-01"), trackSet("cam-01", "cam-03", "cam-02"), zoneMap())
	if a.Verdict != b.Verdict || strings.Join(a.Declared, ",") != strings.Join(b.Declared, ",") {
		t.Errorf("verdicts differ with track order: %+v vs %+v", a, b)
	}
}

func zoneMap() map[string][]string {
	return map[string][]string{
		"restricted": {"cam-01", "cam-02"},
		"entrance":   {"cam-01"},
	}
}
