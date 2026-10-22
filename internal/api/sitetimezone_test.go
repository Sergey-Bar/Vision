package api_test

// The site's clock, served.
//
// # What is being tested
//
// The `sites` row records the zone, and migration 0001 says what it is for:
// "Rendering law: dates cross the site timezone, never the host's." The column
// existed from the first migration and no surface read it, so the site's clock
// was unreachable by every surface at once.
//
// The tests below cover the two halves of that: the zone reaches the surfaces
// that render times, and it does NOT reach them as a guess.
//
// # Why a stub and a database
//
// The stub proves the handlers put the zone in the response and that they say
// nothing when the zone is unknown -- the part that is about the API. The
// database test proves the reader reads the SITE'S ROW, because a stub cannot
// prove a query and a test that only used a stub would pass if the reader
// returned the wrong site's zone.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Sergey-Bar/vision/internal/api"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/sitemodel"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// stubTimezone is a reader that answers with a fixed zone.
type stubTimezone struct {
	zone string
	err  error
	// asked is the site the reader was last asked about, so a test can prove
	// the handler asked for the SITE's clock and not for a constant.
	asked string
}

func (s *stubTimezone) Timezone(_ context.Context, _, siteID string) (string, error) {
	s.asked = siteID
	return s.zone, s.err
}

// clockServer is a server whose principal is the tenant administrator and
// whose site is named.
//
// The administrator is the role that may read both the identity route and the
// site view, which are the two surfaces the clock reaches.
func clockServer(reader api.SiteTimezoneReader) *api.Server {
	s := &api.Server{
		Tenant:        "clock-tenant",
		PrincipalRole: identity.RoleTenantAdmin,
		PrincipalSite: "clock-site",
	}
	deps := api.Deps{
		Site:         &sitemodel.Site{ID: "clock-site"},
		SiteTimezone: reader,
	}
	s.Routes = func(mux *http.ServeMux) { s.RegisterRoutes(deps)(mux) }
	return s
}

// clockBody reads a JSON body into a map.
func clockBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return out
}

// TestTheSiteAndThePrincipalBothCarryTheSitesClock is the positive case.
//
// The site's own model and the identity route are the two surfaces that answer
// "what time is it at this site", and they have to agree: a console that draws
// the site in the site's zone and stamps its clock in another one is the
// "one console, two clocks" defect the rendering law is about.
func TestTheSiteAndThePrincipalBothCarryTheSitesClock(t *testing.T) {
	reader := &stubTimezone{zone: "Asia/Jerusalem"}
	s := clockServer(reader)
	h := s.Handler()

	for _, path := range []string{"/v1/site", "/v1/principal"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200: %s", path, rec.Code, rec.Body)
		}
		zone, ok := clockBody(t, rec)["timezone"].(string)
		if !ok {
			t.Errorf("GET %s answered without a timezone: %s", path, rec.Body)
			continue
		}
		if zone != "Asia/Jerusalem" {
			t.Errorf("GET %s answered timezone %q, want %q", path, zone, "Asia/Jerusalem")
		}
	}
	if reader.asked != "clock-site" {
		t.Errorf("the reader was asked about %q, want the site's own id %q", reader.asked, "clock-site")
	}
}

// TestASiteWithNoClockIsNotGivenOne is the negative case, and it is the one
// that matters.
//
// A site whose row cannot be read has no clock, and the answer says so by
// saying nothing. It does not answer UTC: UTC is a zone, and a zone nobody
// chose is a clock that is wrong in a way nobody can see.
func TestASiteWithNoClockIsNotGivenOne(t *testing.T) {
	reader := &stubTimezone{zone: ""}
	s := clockServer(reader)
	h := s.Handler()

	for _, path := range []string{"/v1/site", "/v1/principal"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got %d, want 200: %s", path, rec.Code, rec.Body)
		}
		body := clockBody(t, rec)
		if zone, present := body["timezone"]; present {
			t.Errorf("GET %s answered a timezone of %v, and this site has no clock to "+
				"report. A guessed zone is a wrong clock.", path, zone)
		}
	}
}

// TestTheTimezoneComesFromTheSitesRow is the database half.
//
// The stub above proves the handlers pass the zone through. This proves the
// reader reads the zone out of the SITE'S OWN ROW -- a reader that answered a
// constant, or another site's row, would pass every test above.
//
// It skips without a database, like every other database-backed test here.
func TestTheTimezoneComesFromTheSitesRow(t *testing.T) {
	raw := testutil.Pool(t)

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
	reader := api.NewSiteTimezoneReader(pool, "clock-test")

	zone, err := reader.Timezone(context.Background(), tenant, site)
	if err != nil {
		t.Fatalf("read the site's timezone: %v", err)
	}
	// The harness seeds every site in Asia/Jerusalem, and the reader must
	// report what the row says rather than what it assumes.
	if zone != "Asia/Jerusalem" {
		t.Errorf("the site's timezone read as %q, want the row's own %q", zone, "Asia/Jerusalem")
	}

	// A site with no row has no clock, and that is not a failure: it is the
	// difference between "this site has no clock" and "the clock could not be
	// read", and only one of them is a fault.
	zone, err = reader.Timezone(context.Background(), tenant, "no-such-site")
	if err != nil {
		t.Errorf("reading a site with no row failed with %v; it should answer with no "+
			"clock and no error", err)
	}
	if zone != "" {
		t.Errorf("a site with no row read a timezone of %q; it has none", zone)
	}
}
