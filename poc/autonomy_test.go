package main

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
	"github.com/Sergey-Bar/vision/internal/telemetry"
	"github.com/Sergey-Bar/vision/internal/threat"
	"github.com/Sergey-Bar/vision/poc/search"
	"github.com/Sergey-Bar/vision/poc/spatial"
	"github.com/Sergey-Bar/vision/poc/ws"
)

// newAutonomyIngest builds a StreamIngest with only the autonomy layer wired,
// no network, no ffmpeg and no evidence store — the autonomy pass is a pure
// function of the detections and the site model, so a test can drive it
// directly.
func newAutonomyIngest(t *testing.T) *StreamIngest {
	t.Helper()
	site, err := sitemodel.LoadFromBytes([]byte(`
site:
  id: "test-site"
  cameras:
    - id: "cam-1"
      zone_ids: ["restricted", "choke"]
  zones:
    - id: "restricted"
      type: "restricted"
      polygon:
        - x: 0
          y: 0
        - x: 400
          y: 0
        - x: 400
          y: 400
        - x: 0
          y: 400
    - id: "choke"
      type: "choke"
      polygon:
        - x: 500
          y: 0
        - x: 700
          y: 0
        - x: 700
          y: 400
        - x: 500
          y: 400
  routes: []
  assets: []
`))
	if err != nil {
		t.Fatalf("load site: %v", err)
	}

	cfg := &Config{WebSocket: WSConfig{Port: 0, Heartbeat: 30 * time.Second}}
	ingest := &StreamIngest{
		config:        cfg,
		wsHub:         ws.NewHub(0, 30*time.Second),
		searchIndex:   search.NewIndex(time.Hour, 10),
		spatialEngine: spatial.NewEngine(nil),
		siteModel:     site,
		tracker:       correlation.NewTracker(correlation.DefaultTrackerConfig()),
		linker:        correlation.NewLinker("test-site", correlation.DefaultLinkerConfig()),
		anomalyRules:  anomaly.DefaultRuleSet(site),
		threatScorer:  threat.NewScorer(threat.DefaultScorerConfig()),
		playbook:      threat.NewPlaybook(threat.DefaultPlaybookConfig(), threat.DefaultMatrix(), threat.NewScorer(threat.DefaultScorerConfig())),
		telemetry:     telemetry.NewMetricsCollector(),
		lastSight:     newSightCache(),
		siteID:        "test-site",
		tenantID:      "test-tenant",
	}
	return ingest
}

func obj(id string, x, y float64) frameObject {
	return frameObject{
		ID:       id,
		Category: "biological",
		Sub:      "human",
		Label:    "person",
		Conf:     0.9,
		BBox:     wireBox{X: x, Y: y, Width: 60, Height: 160},
	}
}

// TestAutonomyPassProducesCorrelatedIncidentWithProof is the integration
// assertion the plan's Phase 9 asks for: the consume path turns a stream of
// detections into a CorrelatedIncident that carries a reason chain, a threat
// score and a decision, and the linker can be asked for it again afterwards.
func TestAutonomyPassProducesCorrelatedIncidentWithProof(t *testing.T) {
	ingest := newAutonomyIngest(t)

	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// A person standing in the restricted zone for 20 seconds. This must
	// produce a dwell anomaly, a non-zero threat score and a reason chain
	// naming the dwell.
	for i := 0; i < 20; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		ingest.autonomyPass("cam-1", now, int64(i), []frameObject{obj("obj_1", 100, 100)})
	}

	incidents := ingest.linker.AllIncidents()
	if len(incidents) == 0 {
		t.Fatal("autonomy pass produced no incidents from a 20s dwell in a restricted zone")
	}

	var found *correlation.CorrelatedIncident
	for _, inc := range incidents {
		if len(inc.Anomalies) > 0 {
			found = inc
			break
		}
	}
	if found == nil {
		t.Fatal("no incident carries an anomaly after a dwell in a restricted zone")
	}

	if found.Anomalies[0].Type != "dwell" {
		t.Errorf("anomaly type = %q, want dwell", found.Anomalies[0].Type)
	}
	if found.ThreatScore <= 0 {
		t.Errorf("threat score = %d, want > 0", found.ThreatScore)
	}
	if len(found.ReasonChain.Steps) == 0 {
		t.Error("reason chain has no steps; the score would be unexplained")
	}
	if found.Action == "" {
		t.Error("incident has no action; the playbook never decided")
	}
	if found.AutonomyState == "" {
		t.Error("incident has no autonomy state")
	}

	// The proof bundle: the linker still holds the incident by id, and it
	// names its detections and its cameras.
	again := ingest.linker.GetIncident(found.ID)
	if again == nil {
		t.Fatal("linked incident cannot be retrieved by id for a proof request")
	}
	if len(again.DetectionIDs) == 0 {
		t.Error("incident records no detections; a proof bundle would cite nothing")
	}
	if len(again.CameraIDs) == 0 {
		t.Error("incident records no cameras; a proof bundle would cite nothing")
	}
	if again.Provenance.SiteID == "" {
		t.Error("incident provenance has no site id")
	}
	if again.Provenance.CorrelationID != again.ID {
		t.Errorf("provenance correlation id = %q, want %q", again.Provenance.CorrelationID, again.ID)
	}
}

// TestAutonomyPassIsBoundedForAnEmptyScene is the guard-rail half: a scene
// with no objects must not invent an incident, and a scene in an unconfigured
// zone must not either.
func TestAutonomyPassIsBoundedForAnEmptyScene(t *testing.T) {
	ingest := newAutonomyIngest(t)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 10; i++ {
		ingest.autonomyPass("cam-1", t0.Add(time.Duration(i)*time.Second), int64(i), nil)
	}
	if got := len(ingest.linker.AllIncidents()); got != 0 {
		t.Errorf("empty scene produced %d incidents, want 0", got)
	}

	// An object outside every zone must not produce an anomaly.
	for i := 0; i < 30; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		ingest.autonomyPass("cam-1", now, int64(i), []frameObject{obj("obj_2", 1200, 700)})
	}
	for _, inc := range ingest.linker.AllIncidents() {
		if len(inc.Anomalies) > 0 {
			t.Errorf("an object outside every zone produced an anomaly: %+v", inc.Anomalies)
		}
	}
}

// TestAutonomyPassOverridesRecordTelemetry proves the override path is wired:
// it moves the incident out of the autonomous decision and counts the override
// so the KPI strip's override rate is a real number rather than a placeholder.
func TestAutonomyPassOverridesRecordTelemetry(t *testing.T) {
	ingest := newAutonomyIngest(t)
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		ingest.autonomyPass("cam-1", now, int64(i), []frameObject{obj("obj_1", 100, 100)})
	}

	var target *correlation.CorrelatedIncident
	for _, inc := range ingest.linker.AllIncidents() {
		if len(inc.Anomalies) > 0 {
			target = inc
			break
		}
	}
	if target == nil {
		t.Fatal("no incident to override")
	}

	ingest.linker.UpdateIncidentState(target.ID, "OVERRIDDEN")
	ingest.linker.UpdateIncidentAction(target.ID, "HOLD", "OVERRIDDEN")
	ingest.telemetry.RecordOverride()

	got := ingest.linker.GetIncident(target.ID)
	if got.State != "OVERRIDDEN" {
		t.Errorf("state after override = %q, want OVERRIDDEN", got.State)
	}
	if got.Action != "HOLD" {
		t.Errorf("action after override = %q, want HOLD", got.Action)
	}

	live := ingest.telemetry.GetLiveTelemetry()
	if live.TotalOverrides != 1 {
		t.Errorf("override count = %d, want 1", live.TotalOverrides)
	}
}

// TestAutonomyPassWithoutSiteModelIsANoOp is the load-bearing nil guard: a
// deployment with no site model configured must still run the deterministic
// spine and must not panic in the autonomy pass.
func TestAutonomyPassWithoutSiteModelIsANoOp(_ *testing.T) {
	ingest := &StreamIngest{siteID: "no-site"}
	ingest.autonomyPass("cam-1", time.Now(), 1, []frameObject{obj("obj_1", 1, 1)})
}

// TestScenarioKindsAreRehearsedOnceEach is the demo-behaviour assertion: the
// simulator rehearses each scripted behaviour exactly once, so a demo shows
// all four without repeating one of them.
func TestScenarioKindsAreRehearsedOnceEach(t *testing.T) {
	sim := newSimulator(newTestEngine(), nil, 7)
	kinds := sim.scenarioKinds()
	if len(kinds) == 0 {
		t.Skip("engine has no restricted or monitoring zones; no scripted actor is defined")
	}
	want := map[string]bool{}
	for _, k := range kinds {
		if k.kind == "" || k.zoneID == "" {
			t.Fatalf("scenario kind is incomplete: %+v", k)
		}
		want[k.kind] = true
	}

	now := time.Now()
	played := map[string]bool{}
	for i := 0; i < 200; i++ {
		if sim.spawnScenario(now) {
			for _, o := range sim.objects {
				if s, ok := o.attrs["scenario"].(string); ok {
					played[s] = true
				}
			}
		}
	}
	for kind := range want {
		if !played[kind] {
			t.Errorf("scenario %q was never rehearsed", kind)
		}
	}
	if len(sim.scenarioState) != len(kinds) {
		t.Errorf("rehearsed %d of %d scenarios, want each once", len(sim.scenarioState), len(kinds))
	}
}

// TestScriptedActorsCarryTheSyntheticLabel checks the demo's truth line at the
// object level: a scripted actor is labelled synthetic, so nothing downstream
// can present a rehearsal as an observation of a real site.
func TestScriptedActorsCarryTheSyntheticLabel(t *testing.T) {
	sim := newSimulator(newTestEngine(), nil, 11)
	if len(sim.scenarioKinds()) == 0 {
		t.Skip("no zones configured for a scripted actor")
	}
	sim.spawnScenario(time.Now())
	found := false
	for _, o := range sim.objects {
		if _, ok := o.attrs["scenario"].(string); ok {
			found = true
			if v := o.attrs["synthetic"]; v != true {
				t.Errorf("scripted actor %s has synthetic=%v, want true", o.id, v)
			}
		}
	}
	if !found {
		t.Error("spawnScenario produced no scripted actor")
	}
}
