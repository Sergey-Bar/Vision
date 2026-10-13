package investigate_test

// This file holds the two gates the plan names for C6, stated as tests.
//
//   nl-hits-resolve      any hit that does not resolve to an incident or an
//                        evidence reference fails the build
//   nl-replays-without-model
//                        the investigation replays identically with the AI plane
//                        absent - invariant 3 on a whole surface
//
// They are here rather than in a separate gate package because the behaviour
// they assert is this package's behaviour, and a gate that tests a copy of the
// rule is a gate that passes when the rule changes.

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/eventsearch"
	"github.com/Sergey-Bar/vision/internal/investigate"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// testPool opens the package's pool ONCE.
//
// A pool per test is a pool per test: six tests in this package is six pools,
// and under `go test -p 8` with every other database-backed package doing the
// same it is how the harness's own teardown starts timing out at sixty seconds.
// The pool is built once and cached, every test gets a fresh tenant instead, and
// the isolation a fresh database was buying arrives at a four-hundredth of the
// connection count. The same pattern `internal/operatormetrics` uses, for the
// same reason its own comment gives.
var (
	testPoolOnce sync.Once
	testPoolDB   *dbx.Pool
	testPoolErr  error
)

func testPool(t *testing.T) *dbx.Pool {
	t.Helper()
	if os.Getenv(testutil.DatabaseURLEnv) == "" {
		t.Skip("no SP_TEST_DATABASE_URL: the database-backed tests skip rather than fail")
	}
	testPoolOnce.Do(func() {
		cfg := dbx.DefaultConfig()
		if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
			testPoolErr = err
			return
		}
		testPoolDB, testPoolErr = dbx.Open(context.Background(), cfg, "postgres")
	})
	if testPoolErr != nil {
		t.Fatalf("open the pool: %v", testPoolErr)
	}
	return testPoolDB
}

func service(t *testing.T, evidence investigate.EvidenceResolver) (*investigate.Service, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)
	pool := testPool(t)
	return &investigate.Service{
		Search:   &eventsearch.Service{Pool: pool},
		Evidence: evidence,
	}, tenant, site
}

func seedInvestigationEvent(t *testing.T, tenant, site, eventID string, at time.Time) {
	t.Helper()
	pool := testutil.Pool(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', $2, $3)
		ON CONFLICT (tenant_id, integration_id, source_event_id) DO UPDATE SET event_id = EXCLUDED.event_id`,
		tenant, eventID, eventID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, clock_source, clock_drift_ms, device_id, zone_id,
		                              correlation_id, causation_id, payload, integrity_hash)
		VALUES ($1,$2,$3,'sys',$4,'int-1','motion','1',$5,$5,$5,'ntp',0,'cam-door18','zone-door',
		        $4,$4,'{"vendor":{}}'::jsonb, $6)
		ON CONFLICT DO NOTHING`,
		tenant, eventID, site, eventID, at.UTC(), strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func seedInvestigationIncident(t *testing.T, tenant, site, incidentID, eventID string) {
	t.Helper()
	pool := testutil.Pool(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO incidents (tenant_id, incident_id, primary_site_id, state, severity, title,
		                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
		                       first_event_at, last_event_at)
		VALUES ($1,$2,$3,'open','high','The incident','r-1','builtin-v1',$4,'none', now(), now())
		ON CONFLICT DO NOTHING`, tenant, incidentID, site, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
		VALUES ($1,$2,$3,'trigger')`, tenant, incidentID, eventID)
	if err != nil {
		t.Fatalf("seed incident event: %v", err)
	}
}

// templateFor is the stored shape the schema validates, parsed the way the
// bundle path parses it.
func templateFor(site string, at time.Time) investigate.Template {
	t, err := investigate.FromCanonical(investigate.Canonical{
		APIVersion:    "security.io/v1",
		Kind:          "InvestigationTemplate",
		TemplateID:    "perimeter-breach",
		IncidentClass: "perimeter-breach",
		Version:       "1.0.0",
		Question:      "What happened at the perimeter around 14:00?",
		Steps: []investigate.CanonicalStep{{
			StepID: "zone-window", Name: "The door's own window", Reason: "The incident is about this door and this hour.",
			MaxHits: 50,
			Query: investigate.CanonicalQuery{
				SiteID: site, ZoneID: "zone-door",
				From: at.Add(-time.Hour).Format(time.RFC3339),
				To:   at.Add(time.Hour).Format(time.RFC3339),
			},
		}},
	})
	if err != nil {
		panic(err)
	}
	return t
}

// stubNarrator is the model-shaped boundary, and the point of the test is that
// its output cannot reach the hits.
type stubNarrator struct{ called bool }

func (s *stubNarrator) Narrate(_ context.Context, _ investigate.Run) (string, error) {
	s.called = true
	return "Two motion events at the perimeter door in the hour around 14:00.", nil
}

// nlResolves is the gate's own assertion, applied to a run.
//
// It is a free function rather than a method on the service because the gate is
// a claim about the shape of a run, and a reviewer reading the test should see
// the claim in one place.
func nlResolves(t *testing.T, run investigate.Run) {
	t.Helper()
	for _, h := range run.Hits {
		if !h.Resolved() {
			t.Fatalf("hit %s resolves to nothing: the gate is that any hit that "+
				"does not resolve to an incident or an evidence reference fails "+
				"the build. This one cites an event with no incident and no "+
				"evidence, so a narrative line about it cannot be checked", h.EventID)
		}
		if h.EventID == "" {
			t.Fatal("a hit carries no event id")
		}
	}
}

// TestEveryHitResolvesToAnOpenableRow is the `nl-hits-resolve` gate.
func TestEveryHitResolvesToAnOpenableRow(t *testing.T) {
	svc, tenant, site := service(t, nil)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)

	// One event is in an incident and one is not, and the one that is not has no
	// evidence either. Both are seeded, because a gate that only ever sees
	// resolvable events is a gate that has never been tried.
	seedInvestigationEvent(t, tenant, site, "evt-in-incident", at)
	seedInvestigationEvent(t, tenant, site, "evt-bare", at)
	seedInvestigationIncident(t, tenant, site, "inc-1", "evt-in-incident")

	run, err := svc.Run(ctx, tenant, templateFor(site, at), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	nlResolves(t, run)
	if len(run.Hits) != 1 || run.Hits[0].EventID != "evt-in-incident" {
		t.Fatalf("the run returned %d hits (%v), want exactly the resolvable one",
			len(run.Hits), hitsString(run))
	}
	if run.Hits[0].IncidentID != "inc-1" {
		t.Errorf("the resolvable event carries incident %q, want inc-1", run.Hits[0].IncidentID)
	}
	if run.Unresolved != 1 {
		t.Errorf("the run reported %d unresolved events, want 1. The count is "+
			"the honest part: an investigation that dropped an event and said "+
			"nothing is an investigation that hides its own gaps", run.Unresolved)
	}
}

// TestAnEvidenceReferenceAlsoResolves is the gate's second reference.
//
// An event with no incident resolves anyway when it has evidence behind it,
// because evidence is openable too. The evidence resolver is stubbed rather
// than seeded, and that is the point: the gate is about the resolution rule,
// not about one evidence table.
func TestAnEvidenceReferenceAlsoResolves(t *testing.T) {
	svc, tenant, site := service(t, evidenceStub{"ev-1"})
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)
	seedInvestigationEvent(t, tenant, site, "evt-with-evidence", at)

	run, err := svc.Run(ctx, tenant, templateFor(site, at), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	nlResolves(t, run)
	if len(run.Hits) != 1 || run.Hits[0].EvidenceID != "ev-1" {
		t.Fatalf("the run returned %+v, want one hit resolved by evidence", run.Hits)
	}
}

// TestTheRunReplaysIdenticallyWithoutTheModel is the `nl-replays-without-model`
// gate.
//
// The same template over the same record, once with a narrator attached and
// once without. The hit lists must be identical - not equivalent, identical -
// because invariant 3 on this surface means the answer is the answer whether or
// not the AI plane was there.
func TestTheRunReplaysIdenticallyWithoutTheModel(t *testing.T) {
	svc, tenant, site := service(t, nil)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)
	seedInvestigationEvent(t, tenant, site, "evt-a", at)
	seedInvestigationEvent(t, tenant, site, "evt-b", at.Add(5*time.Minute))
	seedInvestigationIncident(t, tenant, site, "inc-r", "evt-a")

	without, err := svc.Run(ctx, tenant, templateFor(site, at), nil)
	if err != nil {
		t.Fatalf("run without the model: %v", err)
	}
	narrator := &stubNarrator{}
	with, err := svc.Run(ctx, tenant, templateFor(site, at), narrator)
	if err != nil {
		t.Fatalf("run with the model: %v", err)
	}

	if !narrator.called {
		t.Error("the narrator was never consulted; the failure-under-test here is " +
			"a narrator that changes the answer, and this run cannot show it")
	}
	if !reflect.DeepEqual(without.Hits, with.Hits) {
		t.Fatalf("the hits differ with and without the model:\nwithout: %v\nwith:    %v",
			hitsString(without), hitsString(with))
	}
	if without.Narrated {
		t.Error("a run with no narrator claims to be narrated")
	}
	if !with.Narrated || with.Narrative == "" {
		t.Error("a run with a narrator produced no narrative")
	}
	if without.Narrative != "" {
		t.Error("a run with no narrator carries a narrative anyway")
	}
	if without.Unresolved != with.Unresolved {
		t.Errorf("the unresolved count differs with and without the model: %d vs %d",
			without.Unresolved, with.Unresolved)
	}
}

// TestAFailedNarratorCostsTheNarrativeNotTheInvestigation.
//
// The narrator fails like any other component. The hits survive, the error
// travels with the run rather than replacing it, and the caller decides what a
// missing narrative costs — which is a decision about the deployment, not about
// the evidence.
func TestAFailedNarratorCostsTheNarrativeNotTheInvestigation(t *testing.T) {
	svc, tenant, site := service(t, nil)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)
	seedInvestigationEvent(t, tenant, site, "evt-f", at)
	seedInvestigationIncident(t, tenant, site, "inc-f", "evt-f")

	run, err := svc.Run(ctx, tenant, templateFor(site, at), failingNarrator{})
	if err == nil {
		t.Fatal("a narrator that fails left the run silent")
	}
	nlResolves(t, run)
	if len(run.Hits) != 1 {
		t.Errorf("the narrator's failure cost the evidence: %+v", run.Hits)
	}
}

// TestATemplateWhoseTimesWillNotParseIsRefused.
//
// A stored template with a malformed window is refused at parse time rather
// than widened at run time, because a zero `from` is the beginning of the table.
func TestATemplateWhoseTimesWillNotParseIsRefused(t *testing.T) {
	_, err := investigate.FromCanonical(investigate.Canonical{
		APIVersion: "security.io/v1", Kind: "InvestigationTemplate",
		TemplateID: "t", IncidentClass: "c", Version: "1",
		Steps: []investigate.CanonicalStep{{
			StepID: "s", Name: "n", Reason: "r", MaxHits: 10,
			Query: investigate.CanonicalQuery{ZoneID: "z", From: "not-a-time"},
		}},
	})
	if err == nil {
		t.Fatal("a template with a malformed window parsed anyway; the step would " +
			"have widened to the beginning of the table at run time")
	}

}

type evidenceStub struct{ id string }

func (e evidenceStub) EvidenceForEvent(context.Context, string, string) (string, error) {
	return e.id, nil
}

type failingNarrator struct{}

func (failingNarrator) Narrate(context.Context, investigate.Run) (string, error) {
	return "", context.DeadlineExceeded
}

func hitsString(r investigate.Run) []string {
	out := make([]string, 0, len(r.Hits))
	for _, h := range r.Hits {
		out = append(out, h.EventID+"/"+h.IncidentID+"/"+h.EvidenceID)
	}
	return out
}
