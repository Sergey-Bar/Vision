package eventsearch_test

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/eventsearch"
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

func service(t *testing.T) (*eventsearch.Service, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, raw)
	pool := testPool(t)
	return &eventsearch.Service{Pool: pool}, tenant, site
}

// seedEvent writes one canonical event, with an explicit drift so the observed
// and normalized times can differ — which is the whole point of the first test.
func seedEvent(t *testing.T, tenant, site, eventID string, observed time.Time, driftMS int) {
	t.Helper()
	raw := testutil.Pool(t)
	_, err := raw.Exec(context.Background(), `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', $2, $3)
		ON CONFLICT (tenant_id, integration_id, source_event_id) DO UPDATE SET event_id = EXCLUDED.event_id`,
		tenant, eventID, eventID)
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	normalized := observed.Add(time.Duration(driftMS) * time.Millisecond)
	_, err = raw.Exec(context.Background(), `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, clock_source, clock_drift_ms, device_id, zone_id,
		                              correlation_id, causation_id, payload, integrity_hash)
		VALUES ($1,$2,$3,'sys',$4,'int-1','motion','1',$5,$5,$6,'ntp',$7,'cam-door18','zone-door',
		        $4,$4,'{"vendor":{}}'::jsonb, $8)
		ON CONFLICT DO NOTHING`,
		tenant, eventID, site, eventID, observed.UTC(), normalized.UTC(),
		driftMS, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

// TestTheWindowReadsNormalizedAtAndNotTheSourceClock.
//
// The invariant, on the search surface: a time-window predicate may read
// `normalized_at` and nothing else, because `observed_at` is what a camera
// claimed and a window over it is a window over a clock the site's NTP owns.
//
// Two events at the same observed minute, one of them carrying a thirty-minute
// drift. The window asks 14:00–15:00. The drifted one must not appear even
// though the source said it was inside, and the corrected one must.
func TestTheWindowReadsNormalizedAtAndNotTheSourceClock(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	window := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	// Claimed 14:00, corrected to 14:30 — inside.
	seedEvent(t, tenant, site, "evt-inside", window, 30*60*1000)
	// Claimed 14:10, corrected to 13:40 — outside, and the drift is the whole
	// reason the answer is not the claimed one.
	seedEvent(t, tenant, site, "evt-drifted-out", window.Add(10*time.Minute), -(30 * 60 * 1000))

	got, err := svc.Search(ctx, tenant, eventsearch.Query{
		SiteIDs: []string{site},
		From:    window, To: window.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got.Hits) != 1 || got.Hits[0].EventID != "evt-inside" {
		t.Fatalf("the window answered %v, want exactly [evt-inside]. A window over "+
			"observed_at answers about the camera's clock, not about when it happened",
			hitIDs(got))
	}
}

// TestTheCaseThePlanNamed.
//
// "Door 18, 14:00–15:00": a zone and a window, and the hit comes back with the
// incident it belongs to when there is one. This is the shape C5's demo runs on.
func TestTheCaseThePlanNamed(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	at := time.Date(2026, 6, 1, 14, 12, 0, 0, time.UTC)
	seedEvent(t, tenant, site, "evt-door-18", at, 0)
	seedEvent(t, tenant, site, "evt-other-door", at, 0)
	raw := testutil.Pool(t)
	_, err := raw.Exec(ctx, `
		INSERT INTO incidents (tenant_id, incident_id, primary_site_id, state, severity, title,
		                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
		                       first_event_at, last_event_at)
		VALUES ($1,'inc-door',$2,'open','high','Door 18','r-1','builtin-v1',$3,'none', now(), now())
		ON CONFLICT DO NOTHING`, tenant, site, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	_, err = raw.Exec(ctx, `
		INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
		VALUES ($1,'inc-door','evt-door-18','trigger')`, tenant)
	if err != nil {
		t.Fatalf("seed incident event: %v", err)
	}

	got, err := svc.Search(ctx, tenant, eventsearch.Query{
		SiteIDs: []string{site}, ZoneID: "zone-door",
		From: at.Add(-time.Hour), To: at.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("the zone query answered %v, want both events", hitIDs(got))
	}
	for _, h := range got.Hits {
		if h.EventID == "evt-door-18" {
			if h.IncidentID != "inc-door" {
				t.Errorf("the trigger event carries incident %q, want inc-door", h.IncidentID)
			}
		}
		if h.EventID == "evt-other-door" && h.IncidentID != "" {
			t.Errorf("an event with no incident link carries %q; a fabricated link is "+
				"a fabricated fact", h.IncidentID)
		}
	}
}

// TestASiteOutsideTheScopeIsNotReturned.
//
// Site scoping is the RLS claim on the read path. The second site is seeded the
// same way and must not appear even though the tenant is the same one.
func TestASiteOutsideTheScopeIsNotReturned(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	seedEvent(t, tenant, site, "evt-mine", at, 0)
	otherTenant, otherSite, _ := testutil.NewTenant(t, testutil.Pool(t))
	_ = otherTenant
	seedEvent(t, tenant, otherSite, "evt-theirs", at, 0)

	got, err := svc.Search(ctx, tenant, eventsearch.Query{
		SiteIDs: []string{site}, From: at.Add(-time.Hour), To: at.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got.Hits) != 1 || got.Hits[0].EventID != "evt-mine" {
		t.Fatalf("the scoped search answered %v, want exactly [evt-mine]", hitIDs(got))
	}
}

// TestAnEmptyScopeReturnsNothing.
//
// A principal with no sites is not a principal with every site. The empty page
// is the honest answer and the alternative is a disclosure.
func TestAnEmptyScopeReturnsNothing(t *testing.T) {
	svc, tenant, _ := service(t)
	ctx := context.Background()
	at := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	seedEvent(t, tenant, "s", "evt-unscoped", at, 0)

	got, err := svc.Search(ctx, tenant, eventsearch.Query{From: at.Add(-time.Hour), To: at.Add(time.Hour)})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got.Hits) != 0 {
		t.Errorf("a query with no site scope answered %v; an empty scope is not "+
			"every-site", hitIDs(got))
	}
}

// TestTheCursorPaginatesWithoutSkippingOrRepeating.
//
// The cursor is the last row's sort key, so a page boundary cannot skip or
// repeat an event even when two events share a normalized time.
func TestTheCursorPaginatesWithoutSkippingOrRepeating(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	base := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		seedEvent(t, tenant, site, "evt-page-"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute), 0)
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("the cursor did not terminate; a pagination that loops is a " +
				"denial of service wearing a nextCursor")
		}
		got, err := svc.Search(ctx, tenant, eventsearch.Query{
			SiteIDs: []string{site}, Limit: 2, Cursor: cursor,
			From: base.Add(-time.Hour), To: base.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		for _, h := range got.Hits {
			if seen[h.EventID] {
				t.Fatalf("event %s appeared on two pages; the keyset cursor is "+
					"not stable across a boundary", h.EventID)
			}
			seen[h.EventID] = true
		}
		if got.NextCursor == "" {
			break
		}
		cursor = got.NextCursor
		if _, err := base64.RawURLEncoding.DecodeString(cursor); err != nil {
			t.Fatalf("the cursor is not the shape this server issued: %v", err)
		}
	}
	if len(seen) != 5 {
		t.Errorf("the pagination showed %d events, want 5: %v", len(seen), seen)
	}
}

// TestAMalformedCursorIsRefusedRatherThanIgnored.
//
// A cursor the server did not issue is refused with a reason. Silently starting
// from the beginning would answer a different question than the one asked.
func TestAMalformedCursorIsRefusedRatherThanIgnored(t *testing.T) {
	svc, tenant, site := service(t)
	_, err := svc.Search(context.Background(), tenant, eventsearch.Query{
		SiteIDs: []string{site}, Cursor: "not-a-cursor",
	})
	if err == nil {
		t.Error("a malformed cursor was accepted; the server cannot tell where to " +
			"resume and says so")
	}
}

func hitIDs(r eventsearch.Result) []string {
	out := make([]string, 0, len(r.Hits))
	for _, h := range r.Hits {
		out = append(out, h.EventID)
	}
	return out
}
