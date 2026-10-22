package incidents_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/incidents"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

func service(t *testing.T) (*incidents.Service, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	wrapped, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(wrapped.Close)
	tenant, site, _ := testutil.NewTenant(t, raw)
	return &incidents.Service{Pool: wrapped}, tenant, site
}

func builtinBundle(t *testing.T) *rules.CompiledBundle {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the repository root")
		}
		dir = parent
	}
	raw, err := os.ReadFile(filepath.Join(dir, "rules", "builtin", "bundle.yaml"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	compiled, problems := rules.NewCompiler().Compile(raw)
	if compiled == nil {
		for _, p := range problems {
			t.Errorf("%s", p)
		}
		t.Fatal("built-in bundle did not compile")
	}
	return compiled
}

func seedEvent(t *testing.T, tenant, site, eventID, sourceID, eventType string, at time.Time) {
	t.Helper()
	raw := testutil.Pool(t)
	_, err := raw.Exec(context.Background(), `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', $2, $3)
		ON CONFLICT (tenant_id, integration_id, source_event_id) DO UPDATE SET event_id = EXCLUDED.event_id`,
		tenant, sourceID, eventID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	_, err = raw.Exec(context.Background(), `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, clock_source, clock_drift_ms, device_id, zone_id,
		                              correlation_id, causation_id, payload, integrity_hash)
		VALUES ($1,$2,$3,'sys',$4,'int-1',$5,'1',$6,$6,$6,'ntp',0,'cam-1','zone-entrance',
		        $4,$4,'{"vendor":{"Type":"Motion"}}'::jsonb, $7)
		ON CONFLICT DO NOTHING`,
		tenant, eventID, site, sourceID, eventType, at.UTC(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func seedObservation(t *testing.T, tenant, site, incidentID, eventID, obsID string, degraded string, structured map[string]any) {
	t.Helper()
	raw := testutil.Pool(t)
	body, _ := json.Marshal(structured)
	insufficient := false
	if v, ok := structured["insufficientEvidence"].(bool); ok {
		insufficient = v
	}
	_, err := raw.Exec(context.Background(), `
		INSERT INTO ai_observations (observation_id, tenant_id, site_id, incident_id, event_id, profile_id,
		                            production_profile, model, model_hash, runtime_version, prompt_template_id,
		                            prompt_version, input_evidence_hashes, output_schema_version,
		                            structured_output, insufficient_evidence, degraded_reason, latency_ms,
		                            hardware, output_hash)
		VALUES ($1,$2,$3,$4,$5,'restricted-person',TRUE,'vision-1',$6,'runtime-1','pt-1','1',
		        $7,1,$8,$9,$10,4200,'gpu-0',$11)`,
		obsID, tenant, site, incidentID, eventID,
		strings.Repeat("b", 64), "[]", body, insufficient, degraded,
		strings.Repeat("c", 64))
	if err != nil {
		t.Fatalf("seed observation: %v", err)
	}
}

func aDecision() rules.Decision {
	return rules.Decision{
		State:                  rules.StateCorroborated,
		RuleID:                 "core.camera-offline",
		Severity:               "low",
		Reason:                 "corroborated",
		Action:                 rules.Action{CreateIncident: true, Severity: "low"},
		SupportingEvents:       []string{"evt-1", "evt-2"},
		SupportingObservations: nil,
		Degraded:               false,
		DegradedReason:         rules.DegradedNone,
	}
}

// TestTheAttentionQueueIsOrderedAndDecays
//
// The queue is the product's order, and the calibrated engine's queue decays
// with recency.
//
// The decay is a property of the QUEUE and not of the incident: the stored
// score does not change, and the queue's order does. An incident that is
// older than the half-life carries half its score in the queue, and one that
// is older than four half-lives carries an eighth.
func TestTheAttentionQueueIsOrderedAndDecays(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	raw := testutil.Pool(t)

	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", time.Now().UTC())
	seedEvent(t, tenant, site, "evt-2", "src-2", "motion", time.Now().UTC())

	// The higher-scoring incident, and the lower-scoring one.
	high, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site,
		Decision: aDecision(), RuleBundleVersion: "0.1.0",
		BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
		Title: "Camera offline", ThreatScore: 90,
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	low, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site,
		Decision: aDecision(), RuleBundleVersion: "0.1.0",
		BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
		Title: "Camera offline", ThreatScore: 20,
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	items, err := svc.ListAttention(ctx, tenant, site, 50)
	if err != nil {
		t.Fatalf("ListAttention: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("the queue holds %d item(s), want 2", len(items))
	}
	if items[0].IncidentID != high {
		t.Errorf("the queue's first item is %s and the incident that scored 90 is %s",
			items[0].IncidentID, high)
	}
	if items[0].QueueScore != 90 {
		t.Errorf("the queue score of a fresh incident is %d, want 90: the legacy engine "+
			"does not decay", items[0].QueueScore)
	}

	// Now the calibrated engine, and an incident that is four half-lives old.
	// Its queue score is an eighth of its stored score, and the lower-scoring
	// incident, which is recent, overtakes it.
	if _, err := raw.Exec(ctx, `UPDATE incidents SET created_at = now() - interval '8 hours'
		WHERE tenant_id = $1 AND incident_id = $2`, tenant, high); err != nil {
		t.Fatalf("age the incident: %v", err)
	}
	calibrated := &incidents.Service{Pool: svc.Pool, ThreatEngine: "calibrated", QueueHalfLife: 2 * time.Hour}
	items, err = calibrated.ListAttention(ctx, tenant, site, 50)
	if err != nil {
		t.Fatalf("ListAttention (calibrated): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("the queue holds %d item(s), want 2", len(items))
	}
	if items[0].IncidentID != low {
		t.Errorf("the queue's first item is %s and the recent incident is %s: the decay "+
			"did not move the old incident down the queue", items[0].IncidentID, low)
	}
	for _, item := range items {
		if item.IncidentID != high {
			continue
		}
		if item.ThreatScore != 90 {
			t.Errorf("the STORED score of the aged incident is %d, want 90: the decay is a "+
				"property of the queue and not of the incident", item.ThreatScore)
		}
		// Eight hours is four half-lives, so the queue score is a sixteenth.
		if item.QueueScore < 4 || item.QueueScore > 7 {
			t.Errorf("the queue score of an incident four half-lives old is %d, want about "+
				"5: the decay is not a half-life", item.QueueScore)
		}
	}
}

// TestTwoIncidentsRaisedInTheSameTickAreDistinct
//
// The wall clock does not tick between every pair of calls. Its resolution is
// about a millisecond on Windows and about a microsecond on Linux, so two
// incidents of the same rule at the same site raised inside one tick used to
// derive the SAME id — and the second INSERT failed on the primary key.
//
// The incident was then LOST. That is invariant 1, "zero accepted event loss",
// broken by a clock's resolution, and it is the kind of defect that only
// shows on a machine whose clock is coarse: a test on a fast machine with a
// fine clock passes by luck.
func TestTwoIncidentsRaisedInTheSameTickAreDistinct(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	const raised = 25
	seen := map[string]bool{}
	for i := 0; i < raised; i++ {
		id, err := svc.CreateIncident(ctx, incidents.Create{
			TenantID: tenant, SiteID: site,
			Decision: aDecision(), RuleBundleVersion: "0.1.0",
			BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
			Title: "Camera offline",
		})
		if err != nil {
			t.Fatalf("raise incident %d of %d: %v", i+1, raised, err)
		}
		if seen[id] {
			t.Fatalf("two incidents raised in the same tick share the id %q, so the second one was LOST", id)
		}
		seen[id] = true
	}
	if len(seen) != raised {
		t.Errorf("%d distinct id(s) for %d incident(s)", len(seen), raised)
	}
}

func TestCreateIncidentStoresItsJustification(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", time.Now().UTC())

	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site,
		Decision: aDecision(), RuleBundleVersion: "0.1.0",
		BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
		Title: "Camera offline",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	detail, err := svc.Get(ctx, tenant, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if detail.State != "open" {
		t.Errorf("state is %q, want open", detail.State)
	}
	if detail.Verification.State != string(rules.StateCorroborated) {
		t.Errorf("verification state is %q", detail.Verification.State)
	}
	if len(detail.Events) == 0 {
		t.Error("the incident has no events; it cannot be explained")
	}
	if detail.Verification.Explanation == "" {
		t.Error("the verification has no explanation")
	}

	// The supporting events and the verification result exist or they do not,
	// together. A half-written incident is worse than none.
	var linked, verified int
	raw := testutil.Pool(t)
	if err := raw.QueryRow(ctx,
		`SELECT count(*) FROM incident_events WHERE tenant_id = $1 AND incident_id = $2`,
		tenant, id).Scan(&linked); err != nil {
		t.Fatalf("count linked: %v", err)
	}
	if err := raw.QueryRow(ctx,
		`SELECT count(*) FROM verification_results WHERE tenant_id = $1 AND incident_id = $2`,
		tenant, id).Scan(&verified); err != nil {
		t.Fatalf("count verified: %v", err)
	}
	if linked != 2 || verified != 1 {
		t.Errorf("%d linked event(s) and %d verification row(s), want 2 and 1", linked, verified)
	}
}

func TestCreateIncidentRefusesAnUnexplainableDecision(t *testing.T) {
	svc, tenant, site := service(t)
	d := aDecision()
	d.RuleID = ""
	if _, err := svc.CreateIncident(context.Background(), incidents.Create{
		TenantID: tenant, SiteID: site, Decision: d,
	}); err == nil {
		t.Error("an incident with no rule id was raised; nothing could explain it later")
	}
}

// The disposition never overwrites the observation: "AI said X, operator said Y"
// is a fact, and both replay.
func TestDispositionDoesNotOverwriteTheObservation(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", time.Now().UTC())

	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site, Decision: aDecision(),
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64),
		PolicyVersion: "p1", Title: "Camera offline",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	seedObservation(t, tenant, site, id, "evt-1", "obs-1", "none",
		map[string]any{"restrictedZoneBreach": true, "confidence": 0.9})

	if _, err := svc.Record(ctx, incidents.Disposition{
		TenantID: tenant, IncidentID: id, Outcome: "false_positive",
		Agreement: "disagreed", ActorID: "op1",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	detail, err := svc.Get(ctx, tenant, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(detail.Observations) != 1 {
		t.Fatalf("%d observations after a disposition, want 1", len(detail.Observations))
	}
	if len(detail.Dispositions) != 1 {
		t.Fatalf("%d dispositions, want 1", len(detail.Dispositions))
	}
	obs := detail.Observations[0]
	structured, _ := obs.StructuredOutput.(map[string]any)
	if structured["restrictedZoneBreach"] != true {
		t.Errorf("the observation changed after a disposition: %v", obs.StructuredOutput)
	}
	if detail.Dispositions[0].Agreement != "disagreed" {
		t.Errorf("the operator's disagreement was lost: %+v", detail.Dispositions[0])
	}
	if detail.Dispositions[0].RelatedObservationID != "obs-1" {
		t.Error("the disposition is not linked to the observation it answers")
	}
}

// Escalating or dismissing on someone else's live session launders a decision
// past a second pair of eyes, so those outcomes need a fresh factor.
func TestSensitiveDispositionsRequireStepUp(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", time.Now().UTC())
	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site, Decision: aDecision(),
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	for _, outcome := range []string{"escalated", "dismissed"} {
		if _, err := svc.Record(ctx, incidents.Disposition{
			TenantID: tenant, IncidentID: id, Outcome: outcome, ActorID: "op1",
		}); err == nil {
			t.Errorf("%q was accepted without a step-up verification", outcome)
		}
		if _, err := svc.Record(ctx, incidents.Disposition{
			TenantID: tenant, IncidentID: id, Outcome: outcome, ActorID: "op1", StepUpVerified: true,
		}); err != nil {
			t.Errorf("%q with a step-up verification was refused: %v", outcome, err)
		}
	}
	if _, err := svc.Record(ctx, incidents.Disposition{
		TenantID: tenant, IncidentID: id, Outcome: "acknowledged", ActorID: "op1",
	}); err != nil {
		t.Errorf("a routine disposition needed a step-up: %v", err)
	}
	if _, err := svc.Record(ctx, incidents.Disposition{
		TenantID: tenant, IncidentID: id, Outcome: "invented", ActorID: "op1",
	}); err == nil {
		t.Error("an unrecognised outcome was accepted")
	}
}

func TestLifecycleTransitionsAreChecked(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", time.Now().UTC())
	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site, Decision: aDecision(),
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	if err := svc.SetState(ctx, tenant, id, incidents.StateInReview, "op1", "looking"); err != nil {
		t.Fatalf("open -> in_review: %v", err)
	}
	if err := svc.SetState(ctx, tenant, id, incidents.StateClosed, "op1", "done"); err != nil {
		t.Fatalf("in_review -> closed: %v", err)
	}
	// Closing is what an operator signs, so it does not reopen silently.
	if err := svc.SetState(ctx, tenant, id, incidents.StateOpen, "op1", "reopen"); err == nil {
		t.Error("a closed incident was silently reopened")
	}
}

// The central DoD property: a decision made months ago reproduces from stored
// facts and the stored observation, with nothing re-run.
func TestReplayReproducesTheDecisionFromStoredFacts(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)

	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", at)
	seedEvent(t, tenant, site, "evt-2", "src-2", "analytics", at.Add(time.Second))

	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site, Decision: aDecision(),
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64),
		PolicyVersion: "p1", Title: "Camera offline",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	seedObservation(t, tenant, site, id, "evt-1", "obs-1", "none",
		map[string]any{"restrictedZoneBreach": true, "confidence": 0.93})

	ex, err := svc.Replay(ctx, incidents.ReplayInputs{
		TenantID: tenant, IncidentID: id, Bundle: builtinBundle(t),
		Policy: clock.DefaultPolicy(), SkewToleranceMS: 5000,
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if !ex.ReplayedFromStoredFacts {
		t.Error("the explanation does not assert that it replayed from stored facts")
	}
	if ex.RuleID != "core.camera-offline" {
		t.Errorf("rule is %q, want core.camera-offline", ex.RuleID)
	}
	if ex.ObservationID != "obs-1" {
		t.Errorf("observation is %q, want obs-1", ex.ObservationID)
	}
	if ex.Model == "" || ex.ModelHash == "" || ex.PromptTemplateID == "" || ex.PromptVersion == "" {
		t.Errorf("the model attribution is incomplete: %+v", ex)
	}
	if len(ex.Steps) < 3 {
		t.Errorf("the explanation has %d steps; an operator could not follow it", len(ex.Steps))
	}

	// The explanation must name the model and say it was not re-run.
	var sawObservationStep bool
	for _, s := range ex.Steps {
		if s.Kind != "observation" {
			continue
		}
		sawObservationStep = true
		if !strings.Contains(s.Description, "not re-run") {
			t.Errorf("the observation step does not say the model was not re-run: %q", s.Description)
		}
		if len(s.ObservationIDs) == 0 {
			t.Error("the observation step cites no observation")
		}
	}
	if !sawObservationStep {
		t.Error("the explanation has no observation step")
	}
}

// Replaying under a bundle that no longer contains the rule must report the
// mismatch rather than quietly answer a different question.
func TestReplayReportsABundleMismatch(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)
	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", at)

	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site, Decision: aDecision(),
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}

	ex, err := svc.Replay(ctx, incidents.ReplayInputs{
		TenantID: tenant, IncidentID: id, Bundle: emptyBundle(t),
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if ex.Matches {
		t.Error("replaying under a bundle without the rule reported a match")
	}
	if ex.Mismatch == "" {
		t.Error("the mismatch was not explained; a caller cannot tell what went wrong")
	}
}

func emptyBundle(t *testing.T) *rules.CompiledBundle {
	t.Helper()
	src := "apiVersion: security.io/v1\nkind: RuleBundle\nversion: 9.9.9\n" +
		"site: {siteId: s1, timezones: [UTC]}\n" +
		"settings:\n  clock: {skewTolerance: 5000, driftWarnMs: 2000, driftSevereMs: 10000, referenceSource: ntp}\n" +
		"rules: []\n"
	compiled, problems := rules.NewCompiler().Compile([]byte(src))
	if compiled == nil {
		for _, p := range problems {
			t.Errorf("%s", p)
		}
		t.Fatal("test bundle did not compile")
	}
	return compiled
}

// A degraded incident must say so in the explanation, because that is how a
// pilot measures how often the AI path was actually live.
func TestReplaySurfacesDegradation(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)
	seedEvent(t, tenant, site, "evt-1", "src-1", "motion", at)

	decision := aDecision()
	decision.State = rules.StateUnverified
	decision.Reason = "degraded_per_rule_timeout"
	decision.Degraded = true
	decision.DegradedReason = rules.DegradedTimeout

	id, err := svc.CreateIncident(ctx, incidents.Create{
		TenantID: tenant, SiteID: site, Decision: decision,
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64), PolicyVersion: "p1",
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	seedObservation(t, tenant, site, id, "evt-1", "obs-1", "timeout",
		map[string]any{"insufficientEvidence": false})

	ex, err := svc.Replay(ctx, incidents.ReplayInputs{
		TenantID: tenant, IncidentID: id, Bundle: builtinBundle(t),
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}

	var sawDegradation bool
	for _, s := range ex.Steps {
		if s.Kind == "degradation" && strings.Contains(s.Description, "timeout") {
			sawDegradation = true
		}
	}
	if !sawDegradation {
		t.Errorf("a degraded incident's explanation does not say the AI observation was a timeout: %+v", ex.Steps)
	}

	// And the feed row carries the flag.
	list, err := svc.List(ctx, tenant, site, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 || !list[0].Degraded {
		t.Error("the incident feed does not flag the degraded incident; a pilot could not " +
			"measure how often the AI path was live")
	}
}

// An incident outside the caller's assignment must be indistinguishable from one
// that does not exist. Saying otherwise is a disclosure.
func TestGetRefusesAnUnknownIncident(t *testing.T) {
	svc, tenant, _ := service(t)
	_, err := svc.Get(context.Background(), tenant, "inc_does-not-exist")
	if err == nil {
		t.Fatal("an unknown incident was returned")
	}
	if !errors.Is(err, incidents.ErrNotFound) {
		t.Errorf("error is %v, want ErrNotFound so absence and non-visibility are indistinguishable", err)
	}
}

var _ = pgx.ErrNoRows
