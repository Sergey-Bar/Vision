package main

// Database-backed tests for the new free-text search, chat, clips, and critical
// handlers. They run against a real Postgres when SP_TEST_DATABASE_URL is set
// and skip otherwise — a contributor without Postgres gets an honest green run.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

var (
	dbPoolOnce sync.Once
	dbPoolDB   *dbx.Pool
	dbPoolErr  error
)

func dbPool(t *testing.T) *dbx.Pool {
	t.Helper()
	if os.Getenv(testutil.DatabaseURLEnv) == "" {
		t.Skip("no SP_TEST_DATABASE_URL: the database-backed tests skip rather than fail")
	}
	// The harness points `SP_TEST_DATABASE_URL` at THIS PACKAGE's database when
	// its own pool is first built, and the variable is the only thing that says
	// where this package is. So the harness pool is built FIRST, and the pool
	// below is opened against the retargeted variable.
	//
	// It used to be the other way round, and the symptom was `relation
	// "tenants" does not exist`: the migrations ran against the per-package
	// database and the rows went into the BASE one, which has no schema at
	// all. Two databases, one test, and a failure that read as a product
	// defect rather than as a harness pointing at the wrong one.
	testutil.Pool(t)
	dbPoolOnce.Do(func() {
		cfg := dbx.DefaultConfig()
		if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
			dbPoolErr = err
			return
		}
		dbPoolDB, dbPoolErr = dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	})
	if dbPoolErr != nil {
		t.Fatalf("open the pool: %v", dbPoolErr)
	}
	return dbPoolDB
}

func seedChatData(t *testing.T, pool *dbx.Pool) (string, string) {
	t.Helper()
	ctx := context.Background()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Create tenant and site with a COMMITTED transaction (testutil.NewTenant
	// rolls back, which would break FK constraints on incidents/evidence).
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant := "t-" + suffix
	site := "s-" + suffix
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenants (tenant_id, name) VALUES ($1, $2)`, tenant, "Test Tenant"); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, $3, $4)`, site, tenant, "Test Site", "UTC"); err != nil {
		t.Fatalf("insert site: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	at := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	_, err = pool.Exec(ctx, `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', $2, $3)
		ON CONFLICT (tenant_id, integration_id, source_event_id) DO UPDATE SET event_id = EXCLUDED.event_id`,
		tenant, "evt-incident", "evt-incident")
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, clock_source, clock_drift_ms, device_id, zone_id,
		                              correlation_id, causation_id, payload, integrity_hash)
		VALUES ($1,$2,$3,'sys','evt-incident','int-1','motion','1',$4,$4,$4,'ntp',0,'cam-door18','zone-door',
		        'evt-incident','evt-incident','{"vendor":{"type":"motion"}}'::jsonb, $5),
		       ($1,'evt-bare',$3,'sys','evt-bare','int-1','motion','1',$4,$4,$4,'ntp',0,'cam-door18','zone-door',
		        'evt-bare','evt-bare','{"vendor":{"type":"motion"}}'::jsonb, $5)
		ON CONFLICT DO NOTHING`,
		tenant, "evt-incident", site, at.UTC(), hash)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO event_embeddings (tenant_id, event_id, embedding, document, model)
		VALUES ($1,'evt-incident',$2::vector,'red car at the gate','test'),
		       ($1,'evt-bare',$2::vector,'blue truck in the yard','test')
		ON CONFLICT (tenant_id, event_id) DO UPDATE SET embedding = EXCLUDED.embedding, document = EXCLUDED.document`,
		tenant, formatVectorForTest(zeroVector384()))
	if err != nil {
		t.Fatalf("seed embedding: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (tenant_id, incident_id, primary_site_id, state, severity, title,
		                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
		                       first_event_at, last_event_at)
		VALUES ($1,'inc-1',$2,'open','high','The perimeter breach','r-1','builtin-v1',$3,'none', now(), now())
		ON CONFLICT DO NOTHING`, tenant, site, hash)
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
		VALUES ($1,'inc-1','evt-incident','trigger')`, tenant)
	if err != nil {
		t.Fatalf("seed incident event: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO evidence (tenant_id, evidence_id, site_id, camera_id, device_id, kind, tier,
		                      observed_at, captured_at, storage_ref, media_type, size_bytes, sha256)
		VALUES ($1,'ev-1',$2,'cam-door18','cam-door18','clip','A',$3,$3,'clips/ev-1.mp4','video/mp4',100000,
		        $4)
		ON CONFLICT DO NOTHING`, tenant, site, at.UTC(), hash)
	if err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return tenant, site
}

// seededScope is the console's scope for a seeded tenant.
//
// The console server does not read the tenant out of a request any more: the
// scope is configured, and a header is an instruction rather than an
// authorisation. So a test that seeds a tenant must SCOPE the handler to that
// tenant, and the `X-Tenant-ID` header the tests still set is there to prove
// it is not what decides: the handler is given the scope and the header
// points at the same tenant, so a handler that read the header would pass
// this test too, and the one that proves the header is not read is
// `TestTheConsoleNeverReadsATenantFromAHeader`.
func seededScope(tenant, site string) scope {
	return scope{tenant: tenant, sites: []string{site}, sitesDeclared: true}
}

func TestDBSearchFreeRanksAndResolves(t *testing.T) {
	pool := dbPool(t)
	tenant, site := seedChatData(t, pool)

	h := handleSearchFree(pool, seededScope(tenant, site))
	body := `{"q":"red car","limit":10}`
	req := httptest.NewRequest(http.MethodPost, "/api/search/free", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/search/free -> %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Hits []struct {
			EventID    string `json:"eventId"`
			IncidentID string `json:"incidentId"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Hits) == 0 {
		t.Fatal("search returned no hits; seeded events should match 'red car'")
	}
	foundIncident := false
	for _, h := range resp.Hits {
		if h.IncidentID != "" {
			foundIncident = true
		}
	}
	if !foundIncident {
		t.Error("no hit carries an incident id; events linked to incidents should resolve")
	}
}

func TestDBSearchFreeRespectsSiteScope(t *testing.T) {
	pool := dbPool(t)
	tenant, site := seedChatData(t, pool)

	// A second site in the same tenant, committed so the FK holds.
	otherSite := "s-other"
	if _, err := pool.Exec(context.Background(), `INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, $3, 'UTC')`, otherSite, tenant, "Other Site"); err != nil {
		t.Fatalf("insert other site: %v", err)
	}

	h := handleSearchFree(pool, seededScope(tenant, site))
	body := `{"q":"red car","limit":10,"siteId":"` + otherSite + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/search/free", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/search/free -> %d, want 200", rec.Code)
	}
	var resp struct {
		Hits []struct {
			EventID string `json:"eventId"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Hits) != 0 {
		t.Errorf("site-scoped search returned %d hits, want 0: a query must not escape the caller's site", len(resp.Hits))
	}
}

func TestDBChatReturnsGroundedAnswer(t *testing.T) {
	pool := dbPool(t)
	tenant, site := seedChatData(t, pool)

	h := handleChat(pool, seededScope(tenant, site))
	body := `{"question":"red car","videoId":"vid-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/chat -> %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Answer    string   `json:"answer"`
		State     string   `json:"state"`
		Citations []string `json:"citations"`
		Narrated  bool     `json:"narrated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if resp.State == "unavailable" {
		t.Error("chat returned unavailable despite stored incidents; the answer should be grounded")
	}
	if len(resp.Citations) == 0 {
		t.Error("chat returned no citations; every answer must cite stored incident or evidence rows")
	}
	if resp.Narrated {
		t.Error("chat claims to be narrated with no narrator attached; the narrator is optional")
	}
}

func TestDBChatUnavailableWhenNothingStored(t *testing.T) {
	pool := dbPool(t)
	tenant, site := seedChatData(t, pool)

	h := handleChat(pool, seededScope(tenant, site))
	body := `{"question":"nothing at all matches this impossible phrase xyzzy"}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/chat -> %d, want 200", rec.Code)
	}
	var resp struct {
		Answer string `json:"answer"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if resp.State != "unavailable" {
		t.Errorf("chat state = %q, want unavailable for an unsupported question", resp.State)
	}
}

func TestDBClipsListsStoredClips(t *testing.T) {
	pool := dbPool(t)
	tenant, site := seedChatData(t, pool)

	h := handleClips(pool, seededScope(tenant, site))
	req := httptest.NewRequest(http.MethodGet, "/api/clips", nil)
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/clips -> %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Clips []struct {
			EvidenceID string `json:"evidenceId"`
		} `json:"clips"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Clips) == 0 {
		t.Error("clips returned nothing despite a seeded clip")
	}
}

func TestDBCriticalListsOpenIncidents(t *testing.T) {
	pool := dbPool(t)
	tenant, site := seedChatData(t, pool)

	h := handleCritical(pool, seededScope(tenant, site))
	req := httptest.NewRequest(http.MethodGet, "/api/critical?limit=5", nil)
	req.Header.Set("X-Tenant-ID", tenant)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/critical -> %d, want 200. Body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Streams []struct {
			IncidentID string `json:"incidentId"`
			Severity   string `json:"severity"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(resp.Streams) == 0 {
		t.Error("critical returned nothing despite a seeded open incident")
	}
	for _, s := range resp.Streams {
		if s.IncidentID == "" {
			t.Error("a critical stream carries no incident id; every citation must resolve")
		}
	}
}

// Ensure the packages we use are referenced (avoid unused import when tests skip).
var _ = eventsearch.Query{}
var _ = investigate.Run{}
var _ = dbx.Pool{}

// formatVectorForTest renders a float32 slice as a pgvector literal for SQL.
func formatVectorForTest(v []float32) string {
	b := strings.Builder{}
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// zeroVector384 returns a 384-dimensional zero vector for pgvector column tests.
func zeroVector384() []float32 {
	v := make([]float32, 384)
	return v
}
