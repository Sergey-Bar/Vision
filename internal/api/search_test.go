package api_test

// This file holds the tests for the structured search surface: the plan's C5,
// the "Door 18, 14:00–15:00" question over the canonical event record. It lands
// in a file of its own, like the evidence-capability tests, so the search
// behaviour is asserted where it was built.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// TestTheSearchRouteAnswersTheNamedCase.
//
// The case the plan names, over HTTP: a zone and a window, and the hit comes
// back carrying the incident it belongs to when there is one and nothing at all
// when there is not.
func TestTheSearchRouteAnswersTheNamedCase(t *testing.T) {
	s, tenant, site := server(t)
	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)
	seedSearchEvent(t, tenant, site, "evt-door-18", "evt-door-18", "cam-door18", "zone-door", at.UTC())
	seedSearchEvent(t, tenant, site, "evt-elsewhere", "evt-elsewhere", "cam-back-1", "zone-rear", at.UTC())
	seedSearchIncidentLink(t, tenant, "inc-door", "evt-door-18")

	rec := call(t, s, http.MethodGet,
		"/v1/evidence?siteId="+site+"&zoneId=zone-door&from="+
			at.Add(-time.Hour).Format(time.RFC3339)+"&to="+
			at.Add(time.Hour).Format(time.RFC3339), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the search returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "evt-door-18") {
		t.Errorf("the zone window did not return its event: %s", body)
	}
	if !strings.Contains(body, "inc-door") {
		t.Errorf("the trigger event did not carry its incident: %s", body)
	}
	if strings.Count(body, `"eventId"`) != 1 {
		t.Errorf("the zone filter returned %d events, want exactly the one on "+
			"zone-door:\n%s", strings.Count(body, `"eventId"`), body)
	}
}

// TestTheSearchRouteRefusesASiteOutsideTheScope.
//
// The site outside the principal's assignments is refused, not returned empty:
// "no such site" and "not yours" are the same answer, and a different one is a
// disclosure.
func TestTheSearchRouteRefusesASiteOutsideTheScope(t *testing.T) {
	s, tenant, site := server(t)
	at := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	seedSearchEvent(t, tenant, site, "evt-mine", "evt-mine", "cam-1", "zone-door", at.UTC())

	rec := call(t, s, http.MethodGet, "/v1/evidence?siteId=somebody-elses-site", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a site outside the scope answered %d, want 403: %s", rec.Code, rec.Body)
	}
}

// TestAMalformedWindowIsRefusedRatherThanWidened.
//
// A window that will not parse is refused with the reason. Parsing it as the
// zero time would widen the search to the beginning of the table — the answer
// nobody asked for.
func TestAMalformedWindowIsRefusedRatherThanWidened(t *testing.T) {
	s, tenant, site := server(t)
	rec := call(t, s, http.MethodGet, "/v1/evidence?siteId="+site+"&from=not-a-time", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a malformed window answered %d, want 400: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "RFC3339") {
		t.Errorf("the refusal did not name the format the caller should use: %s", rec.Body)
	}
	_ = tenant
}

// TestTheSearchRouteIsCapabilityGated.
//
// `incident.read`, the same capability the incident pages carry, and the
// reasoning is the route comment's: the searched rows are the events incidents
// are built from. The assertion is made against the role table, which is where
// the claim actually lives, and it documents the consequence rather than hiding
// it — a viewer holds `incident.read`, so a viewer may search. That is the
// decision, not an oversight: a viewer reads incidents, and the events an
// incident is made of are the same record one row at a time.
func TestTheSearchRouteIsCapabilityGated(t *testing.T) {
	for _, tc := range []struct {
		role    identity.Role
		hasIt   bool
		whyText string
	}{
		{identity.RoleViewer, true, "a viewer reads incidents, and the searched rows are the incidents' own events"},
		{identity.RoleOperator, true, "an operator runs the incident queue"},
		{identity.RoleService, false, "a service account ingests and never reads; granting it a read of the record of its own behaviour is how an adapter audits itself"},
	} {
		got := false
		for _, c := range identity.CapabilitiesFor(tc.role) {
			if c == identity.CapIncidentRead {
				got = true
			}
		}
		if got != tc.hasIt {
			t.Errorf("role %s holds incident.read = %v, want %v (%s)", tc.role, got, tc.hasIt, tc.whyText)
		}
	}

	// And the route is reachable for a principal that holds it. A gate nobody
	// can pass is indistinguishable from a correct one until an operator needs
	// the answer.
	s, tenant, site := server(t)
	at := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	seedSearchEvent(t, tenant, site, "evt-gated", "evt-gated", "cam-door18", "zone-door", at.UTC())
	rec := call(t, s, http.MethodGet, "/v1/evidence?siteId="+site, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("a principal holding incident.read was refused %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "evt-gated") {
		t.Errorf("the search returned no hits: %s", rec.Body)
	}
}

// TestTheConsoleSearchPageRendersTheFormAndTheHits.
//
// The page renders the question and the answer in the same place, because a
// search page that only shows results offers an operator no way to change the
// window they are looking at.
func TestTheConsoleSearchPageRendersTheFormAndTheHits(t *testing.T) {
	s, tenant, site := server(t)
	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)
	seedSearchEvent(t, tenant, site, "evt-page", "evt-page", "cam-door18", "zone-door", at.UTC())

	rec := call(t, s, http.MethodGet, "/search?siteId="+site+"&from="+
		at.Add(-time.Hour).Format(time.RFC3339)+"&to="+
		at.Add(time.Hour).Format(time.RFC3339), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the search page returned %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="from"`) || !strings.Contains(body, `name="zoneId"`) {
		t.Error("the search page rendered no form, so the window cannot be changed")
	}
	if !strings.Contains(body, "evt-page") {
		t.Errorf("the search page rendered no hits: %s", body)
	}
}

// TestTheSearchPageWithoutAQuestionRendersOnlyTheForm.
//
// Landing on the page is not a query. A page that answered an empty query on
// arrival would render "no results" before the operator had asked anything —
// the empty that reads as a fact.
func TestTheSearchPageWithoutAQuestionRendersOnlyTheForm(t *testing.T) {
	s, _, _ := server(t)
	rec := call(t, s, http.MethodGet, "/search", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the search page returned %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "No events match") {
		t.Error("the search page answered before it was asked")
	}
	if !strings.Contains(rec.Body.String(), `name="from"`) {
		t.Error("the search page rendered no form")
	}
}

// seedSearchEvent writes one canonical event through the same tables the ingest
// path writes, so the search is asserting against the record rather than a
// fixture of its own invention.
func seedSearchEvent(t *testing.T, tenant, site, eventID, sourceID, deviceID, zoneID string, at time.Time) {
	t.Helper()
	pool := testutil.Pool(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', $2, $3)
		ON CONFLICT (tenant_id, integration_id, source_event_id) DO UPDATE SET event_id = EXCLUDED.event_id`,
		tenant, sourceID, eventID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	_, err = pool.Exec(context.Background(), `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, clock_source, clock_drift_ms, device_id, zone_id,
		                              correlation_id, causation_id, payload, integrity_hash)
		VALUES ($1,$2,$3,'sys',$4,'int-1','motion','1',$5,$5,$5,'ntp',0,$6,$7,
		        $4,$4,'{"vendor":{}}'::jsonb, $8)
		ON CONFLICT DO NOTHING`,
		tenant, eventID, site, sourceID, at, deviceID, zoneID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func seedSearchIncidentLink(t *testing.T, tenant, incidentID, eventID string) {
	t.Helper()
	pool := testutil.Pool(t)
	_, err := pool.Exec(context.Background(), `
		INSERT INTO incidents (tenant_id, incident_id, primary_site_id, state, severity, title,
		                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
		                       first_event_at, last_event_at)
		VALUES ($1,$2,(SELECT site_id FROM canonical_events WHERE tenant_id=$1 AND event_id=$3),
		        'open','high','Door 18','r-1','builtin-v1',$4,'none', now(), now())
		ON CONFLICT DO NOTHING`, tenant, incidentID, eventID, strings.Repeat("a", 64))
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
