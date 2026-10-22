package api_test

// The analytics routes, and the three things they have to be true about.
//
// # The capability
//
// `analytics.read`, and not `audit.read`. The metrics scrape reuses audit.read
// because it is a Prometheus exposition; these routes are a report about
// PEOPLE, and somebody who may read every operator's disposition row is not
// automatically somebody who may read the aggregate of them.
//
// # The site filter is a FILTER and the scope is the GRANT
//
// A request for a site the principal is not assigned to is refused, and a
// request that does not name a site sees only the principal's own. The scope
// is bound onto the connection, so a row outside it is not there to sum: the
// other site's number is ABSENT rather than zero.
//
// # Every response says what it is
//
// The window, the N, when it was computed and where it came from, and a
// drill-down reference that re-derives the number from the raw rows.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/api"

	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/operatormetrics"
)

// analyticsServer is a server with the analytics pool wired, its configured
// principal scoped to the tenant's own site.
func analyticsServer(t *testing.T) (*api.Server, string, string) {
	t.Helper()
	s, tenant, site := server(t)
	return s, tenant, site
}

// rollTheBucket writes the rollup rows the report reads.
//
// The rows are written DIRECTLY rather than derived, because this test is about
// the REPORT's shape and the writer's arithmetic is proved in the package that
// owns it. A test that derived them would be testing the same thing twice and
// would fail for the writer's reasons when it meant to fail for the report's.
//
// It returns nothing: the window it wrote into is the one the report asks for
// by its own query parameters, and a caller that took the window as a return
// value would be reading a different window from the one it asked about.
func rollTheBucket(t *testing.T, s *api.Server, tenant, site string) {
	t.Helper()
	end := time.Now().UTC().Truncate(time.Hour)
	start := end.Add(-time.Hour)

	rows := []struct {
		metric string
		value  float64
		n      int64
	}{
		{"canonical_events", 1000, 1000},
		{"attention_minutes_capped", 30, 5},
		{"attention_minutes_uncapped", 42, 5},
		{"attention_incidents", 5, 5},
		{"incidents_raised", 12, 12},
	}
	for _, r := range rows {
		if _, err := s.Pool.WithSites(tenant, "test", []string{site}).Exec(context.Background(),
			`INSERT INTO metric_rollups
				(tenant_id, metric, bucket_start, bucket_end, site_id, rule_id, device_id,
				 value, n, numerator, denominator, computed_at)
			 VALUES ($1, $2, $3, $4, $5, NULL, NULL, $6, $7, NULL, NULL, now())`,
			tenant, r.metric, start, end, site, r.value, r.n); err != nil {
			t.Fatalf("write the %s rollup row: %v", r.metric, err)
		}
	}
}

// get performs a GET against the server and returns the recorder.
func get(t *testing.T, s *api.Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// TestTheAnalyticsRoutesNeedTheAnalyticsCapability is the capability.
//
// A viewer may read incidents and evidence. They may not read the aggregated
// attention minutes, which is a number about how long a shift took to clear a
// site.
func TestTheAnalyticsRoutesNeedTheAnalyticsCapability(t *testing.T) {
	s, _, _ := analyticsServer(t)
	s.PrincipalRole = identity.RoleViewer

	for _, path := range []string{
		"/v1/analytics/summary",
		"/v1/analytics/series?metric=canonical_events",
		"/v1/analytics/trend?metric=canonical_events",
		"/v1/analytics/drilldown?metric=canonical_events&site=site-1",
	} {
		rec := get(t, s, path)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s answered %d for a principal without analytics.read, want 403",
				path, rec.Code)
		}
	}
}

// TestTheAnalyticsSummaryCarriesTheNorthStarAndItsFourCompanions is the
// report's shape.
//
// The North Star is never alone, and the report says which window, how many
// observations, when it was computed and where it came from.
func TestTheAnalyticsSummaryCarriesTheNorthStarAndItsFourCompanions(t *testing.T) {
	s, tenant, site := analyticsServer(t)
	rollTheBucket(t, s, tenant, site)

	rec := get(t, s, "/v1/analytics/summary?from="+
		time.Now().UTC().Truncate(time.Hour).Add(-2*time.Hour).Format(time.RFC3339)+
		"&to="+time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339))
	if rec.Code != http.StatusOK {
		t.Fatalf("the summary answered %d: %s", rec.Code, rec.Body.String())
	}

	var doc struct {
		ReportVersion string `json:"report_version"`
		Source        string `json:"source"`
		Window        struct {
			From time.Time `json:"from"`
			To   time.Time `json:"to"`
		} `json:"window"`
		Metrics []struct {
			Metric string  `json:"metric"`
			Value  float64 `json:"value"`
			N      int64   `json:"n"`
		} `json:"metrics"`
		NorthStar *struct {
			Metric      string                  `json:"metric"`
			Value       *float64                `json:"value"`
			Numerator   struct{ Metric string } `json:"numerator"`
			Denominator struct{ Metric string } `json:"denominator"`
			Companions  map[string]struct {
				Metric string   `json:"metric"`
				Value  *float64 `json:"value"`
				N      int64    `json:"n"`
			} `json:"companions"`
		} `json:"north_star"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parse the report: %v\n%s", err, rec.Body.String())
	}

	if doc.ReportVersion != operatormetrics.ReportVersion {
		t.Errorf("the report version is %q, want %q", doc.ReportVersion, operatormetrics.ReportVersion)
	}
	if doc.Source != "postgres" {
		t.Errorf("the source is %q, want postgres. A report that does not say where its numbers "+
			"came from is a report nobody can check", doc.Source)
	}
	if doc.Window.From.IsZero() || doc.Window.To.IsZero() {
		t.Errorf("the report carries no window: %+v", doc.Window)
	}
	if doc.NorthStar == nil {
		t.Fatalf("the report carries no North Star:\n%s", rec.Body.String())
	}
	if doc.NorthStar.Denominator.Metric != "canonical_events" {
		t.Errorf("the North Star's denominator is %q, want canonical_events. Incidents would "+
			"improve the ratio automatically as the rules got more conservative",
			doc.NorthStar.Denominator.Metric)
	}
	for _, name := range []string{
		"operator_override_rate", "ai_false_escalation_rate",
		"evidence_completeness", "missed_incident_rate",
	} {
		c, ok := doc.NorthStar.Companions[name]
		if !ok {
			t.Errorf("the companion %q is missing from the report. The North Star is never "+
				"reportable without all four", name)
			continue
		}
		if c.Metric != name {
			t.Errorf("the companion %q names itself %q", name, c.Metric)
		}
	}
}

// TestACrossSiteAnalyticsRequestIsRefused is the scope.
//
// A principal assigned to one site asks for another's numbers and is told it
// has no access to that site. It is NOT told "0", because a zero is a
// measurement and a site somebody else is assigned to is not the reader's
// measurement.
func TestACrossSiteAnalyticsRequestIsRefused(t *testing.T) {
	s, tenant, site := analyticsServer(t)
	rollTheBucket(t, s, tenant, site)

	rec := get(t, s, "/v1/analytics/summary?site=somebody-elses-site")
	if rec.Code != http.StatusForbidden {
		t.Errorf("a request for a site outside the principal's scope answered %d, want 403:\n%s",
			rec.Code, rec.Body.String())
	}
}

// TestTheAnalyticsRoutesRefuseAMetricOutsideTheVocabulary is the closed set.
//
// The refusal names the vocabulary, because a caller that guessed wrong
// deserves to know what the set is.
func TestTheAnalyticsRoutesRefuseAMetricOutsideTheVocabulary(t *testing.T) {
	s, _, _ := analyticsServer(t)

	rec := get(t, s, "/v1/analytics/series?metric=not_a_metric")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown metric answered %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "canonical_events") {
		t.Errorf("the refusal does not name the vocabulary: %s", body)
	}
}

// TestTheAnalyticsRoutesSayNotConfiguredWithoutAPool is the honest absence.
//
// An install with no analytics has a report that says so, not an empty report
// that reads as "the site had no activity".
func TestTheAnalyticsRoutesSayNotConfiguredWithoutAPool(t *testing.T) {
	s, _, _ := server(t)
	// The pool is removed by building a server with no analytics pool: the
	// routes answer from the Deps the harness built, so the field has to be
	// cleared rather than the server.
	s.Routes = func(mux *http.ServeMux) {
		s.RegisterRoutes(api.Deps{})(mux)
	}

	rec := get(t, s, "/v1/analytics/summary")
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("an install with no analytics pool answered %d, want 501:\n%s",
			rec.Code, rec.Body.String())
	}
}
