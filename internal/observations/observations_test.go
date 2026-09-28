package observations_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/observations"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

var clock = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func writer(t *testing.T) (*observations.Writer, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	tenant, site, _ := testutil.NewTenant(t, raw)
	_, err = raw.Exec(context.Background(), `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ('inc-1', $1, $2, 'r.test', '1.0.0', $3, 'p1', now(), now())`,
		tenant, site, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	return &observations.Writer{Pool: pool, Now: func() time.Time { return clock }}, tenant, site
}

func base(tenant, site string) observations.Observation {
	return observations.Observation{
		TenantID: tenant, SiteID: site, IncidentID: "inc-1", EventID: "evt-1",
		ProfileID: "restricted-person",
		Model:     "vision-1", ModelHash: strings.Repeat("b", 64), RuntimeVersion: "rt-1",
		PromptTemplateID: "pt-1", PromptVersion: "1", ModelBundleSignature: "sig-1",
		InputEvidenceIDs:    []string{"ev-1"},
		InputEvidenceHashes: []string{strings.Repeat("c", 64)},
		OutputSchemaVersion: 1,
		StructuredOutput:    map[string]any{"restrictedZoneBreach": true, "confidence": 0.93},
		LatencyMS:           1200, Hardware: "gpu-0",
	}
}

// Attribution is not decoration. An observation without a model hash or a
// prompt version cannot be explained, and an unexplainable observation is one an
// operator cannot act on.
func TestAttributionIsMandatory(t *testing.T) {
	cases := map[string]func(*observations.Observation){
		"no tenant":         func(o *observations.Observation) { o.TenantID = "" },
		"no site":           func(o *observations.Observation) { o.SiteID = "" },
		"no incident":       func(o *observations.Observation) { o.IncidentID = "" },
		"no event":          func(o *observations.Observation) { o.EventID = "" },
		"no profile":        func(o *observations.Observation) { o.ProfileID = "" },
		"no model":          func(o *observations.Observation) { o.Model = "" },
		"no model hash":     func(o *observations.Observation) { o.ModelHash = "" },
		"no runtime":        func(o *observations.Observation) { o.RuntimeVersion = "" },
		"no prompt id":      func(o *observations.Observation) { o.PromptTemplateID = "" },
		"no prompt version": func(o *observations.Observation) { o.PromptVersion = "" },
		"no schema version": func(o *observations.Observation) { o.OutputSchemaVersion = 0 },
		"unknown degraded":  func(o *observations.Observation) { o.Degraded = "because" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := base("t", "s")
			mutate(&o)
			if err := o.Validate(); err == nil {
				t.Fatalf("an observation missing %s was accepted", name)
			}
		})
	}
}

func TestUnknownProfileAndFieldAreRefused(t *testing.T) {
	w, tenant, site := writer(t)

	o := base(tenant, site)
	o.ProfileID = "invented-profile"
	if _, err := w.Record(context.Background(), o); err == nil {
		t.Error("an observation for a profile this build does not have was stored")
	}

	o = base(tenant, site)
	o.StructuredOutput = map[string]any{"inventedField": true}
	_, err := w.Record(context.Background(), o)
	if err == nil {
		t.Fatal("an observation with an undeclared field was stored")
	}
	if !strings.Contains(err.Error(), "undeclared") {
		t.Errorf("the refusal should name the problem, got %q", err)
	}
}

func TestShadowProfilesAreMarkedAndProductionIsNot(t *testing.T) {
	w, tenant, site := writer(t)

	o := base(tenant, site)
	if _, err := w.Record(context.Background(), o); err != nil {
		t.Fatalf("record production: %v", err)
	}
	var production bool
	if err := testutil.Pool(t).QueryRow(context.Background(), `
		SELECT production_profile FROM ai_observations
		 WHERE tenant_id = $1 AND incident_id = 'inc-1' AND profile_id = 'restricted-person'`,
		tenant).Scan(&production); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !production {
		t.Error("the production profile was not marked as production")
	}

	shadow := base(tenant, site)
	shadow.EventID = "evt-2"
	shadow.ProfileID = "door-forced"
	shadow.StructuredOutput = map[string]any{"doorState": "forced"}
	if _, err := w.Record(context.Background(), shadow); err != nil {
		t.Fatalf("record shadow: %v", err)
	}
	if err := testutil.Pool(t).QueryRow(context.Background(), `
		SELECT production_profile FROM ai_observations
		 WHERE tenant_id = $1 AND profile_id = 'door-forced'`, tenant).Scan(&production); err != nil {
		t.Fatalf("read: %v", err)
	}
	if production {
		t.Error("a shadow profile was marked production; that is how a failing profile " +
			"ends up affecting authoritative state")
	}
}

func TestShadowProfilesCannotAffectAuthoritativeState(t *testing.T) {
	for _, id := range []string{"door-forced", "vehicle-at-gate"} {
		if observations.IsProductionProfile(id) {
			t.Errorf("shadow profile %q reports itself production", id)
		}
	}
	if !observations.IsProductionProfile("restricted-person") {
		t.Error("the production profile does not report itself production")
	}
}

// A degraded observation is a real row, not a log line. "The model was down at
// 03:14" is a fact a pilot needs six weeks later.
func TestDegradedObservationIsPersistedAndFlagged(t *testing.T) {
	w, tenant, site := writer(t)

	o, err := w.RecordDegraded(context.Background(), base(tenant, site),
		observations.DegradationTimeout, "the runtime did not answer in 10s")
	if err != nil {
		t.Fatalf("RecordDegraded: %v", err)
	}
	if o.Degraded != observations.DegradationTimeout {
		t.Errorf("degradation is %q, want timeout", o.Degraded)
	}
	// A degraded observation must fail closed for any consumer that forgets to
	// check the degradation.
	if !o.InsufficientEvidence {
		t.Error("a degraded observation does not carry insufficient_evidence; a consumer " +
			"that ignores the degradation would read an empty output as a negative answer")
	}

	read, err := w.Get(context.Background(), tenant, o.ObservationID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if read.Degraded != observations.DegradationTimeout {
		t.Errorf("the stored degradation is %q", read.Degraded)
	}
	if read.DegradedDetail == "" {
		t.Error("the degradation has no detail; \"timeout\" without a reason is not actionable")
	}
}

// A re-evaluation is a new row naming the one it replaces. Both replay.
func TestReEvaluationSupersedesRatherThanOverwrites(t *testing.T) {
	w, tenant, site := writer(t)
	ctx := context.Background()

	first, err := w.Record(ctx, base(tenant, site))
	if err != nil {
		t.Fatalf("first record: %v", err)
	}

	second := base(tenant, site)
	second.StructuredOutput = map[string]any{"restrictedZoneBreach": false, "confidence": 0.2}
	second.SupersedesObservationID = first.ObservationID
	recorded, err := w.Record(ctx, second)
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	if recorded.ObservationID == first.ObservationID {
		t.Fatal("the re-evaluation reused the first observation's id")
	}

	// The original is untouched.
	original, err := w.Get(ctx, tenant, first.ObservationID)
	if err != nil {
		t.Fatalf("the first observation was removed by the re-evaluation: %v", err)
	}
	if original.StructuredOutput["restrictedZoneBreach"] != true {
		t.Errorf("the first observation changed: %v", original.StructuredOutput)
	}

	// And the supersession chain is walkable.
	superseded, err := w.SupersededBy(ctx, tenant, first.ObservationID)
	if err != nil {
		t.Fatalf("SupersededBy: %v", err)
	}
	if len(superseded) != 1 || superseded[0].ObservationID != recorded.ObservationID {
		t.Errorf("the supersession chain does not lead to the replacement: %+v", superseded)
	}
}

func TestOutputHashIsStableAndRecomputable(t *testing.T) {
	a := base("t", "s")
	b := base("t", "s")

	ha, err := a.OutputHash()
	if err != nil {
		t.Fatalf("OutputHash: %v", err)
	}
	hb, _ := b.OutputHash()
	if ha != hb {
		t.Error("the output hash is not stable for the same output")
	}

	b.StructuredOutput = map[string]any{"restrictedZoneBreach": false}
	hc, _ := b.OutputHash()
	if ha == hc {
		t.Error("different outputs produced the same hash; \"the model said this\" is not checkable")
	}
}

func TestObservationsAreAppendOnly(t *testing.T) {
	w, tenant, site := writer(t)
	ctx := context.Background()

	o, err := w.Record(ctx, base(tenant, site))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	// The database refuses an update outright; the package does not even offer a
	// method for one.
	err = testutil.Pool(t).QueryRow(ctx, `
		UPDATE ai_observations SET structured_output = '{"restrictedZoneBreach":false}'::jsonb
		 WHERE tenant_id = $1 AND observation_id = $2`, tenant, o.ObservationID).Scan(new(string))
	if err == nil {
		t.Fatal("a stored observation was mutable; every decision that cited it is now a lie")
	}
}

func TestStatsReportAvailability(t *testing.T) {
	w, tenant, site := writer(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		o := base(tenant, site)
		o.EventID = "evt-ok-" + string(rune('a'+i))
		if _, err := w.Record(ctx, o); err != nil {
			t.Fatalf("record ok: %v", err)
		}
	}
	for i := 0; i < 1; i++ {
		o := base(tenant, site)
		o.EventID = "evt-degraded"
		if _, err := w.RecordDegraded(ctx, o, observations.DegradationNoRuntime, "no runtime on this host"); err != nil {
			t.Fatalf("record degraded: %v", err)
		}
	}

	stats, err := w.Stats(ctx, tenant, clock.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Total != 4 {
		t.Errorf("total is %d, want 4", stats.Total)
	}
	if stats.Degraded != 1 {
		t.Errorf("degraded is %d, want 1", stats.Degraded)
	}
	if got := stats.Availability(); got < 0.74 || got > 0.76 {
		t.Errorf("availability is %.3f, want 0.75; a pilot claiming the AI plane reduced "+
			"effort has to be able to say how often it was there", got)
	}
	if stats.DegradedByReason["no_runtime"] != 1 {
		t.Errorf("the degradation breakdown is %v", stats.DegradedByReason)
	}
}

func TestLatestForIncident(t *testing.T) {
	w, tenant, site := writer(t)
	ctx := context.Background()

	none, err := w.LatestForIncident(ctx, tenant, "inc-1")
	if err != nil {
		t.Fatalf("LatestForIncident: %v", err)
	}
	if none != nil {
		t.Error("an incident with no observations reported one")
	}

	first, _ := w.Record(ctx, base(tenant, site))
	latest, err := w.LatestForIncident(ctx, tenant, "inc-1")
	if err != nil || latest == nil {
		t.Fatalf("LatestForIncident after a record: %v", err)
	}
	if latest.ObservationID != first.ObservationID {
		t.Errorf("latest is %s, want %s", latest.ObservationID, first.ObservationID)
	}
}
