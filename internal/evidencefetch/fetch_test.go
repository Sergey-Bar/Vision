package evidencefetch

// This file holds the C1 lane the plan names: the bytes, and the kill -9 at the
// stage that takes them.
//
// Three tests, each for one property the plan asks for and the existing suite
// could not see:
//
//   - the fetch takes the bytes when the vendor holds them, and stores them like
//     any other evidence;
//   - a probe that found nothing is a fact about a recording, not a broken
//     worker, so the job completes with the copy skipped and the pointer kept;
//   - a vendor that will not answer is a retry, not a refusal.

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/milestone"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// stubMedia is the vendor's recording surface as a fixture sees it: a probe
// answer and a body. The fixtures are the only things that ever speak to it,
// which is the point — `adapter-probe` decided the routes and a fixture is how
// this package tests a route that has never been reached.
type stubMedia struct {
	available []mediaInterval
	source    string
	body      []byte
	failWith  error
}

type mediaInterval struct{ from, to time.Time }

func (m *stubMedia) ProbeRange(_ context.Context, _ string, _, _ time.Time) (milestone.MediaRange, error) {
	if m.failWith != nil {
		return milestone.MediaRange{}, m.failWith
	}
	out := milestone.MediaRange{Source: m.source}
	for _, iv := range m.available {
		out.Available = append(out.Available, milestone.Interval{From: iv.from, To: iv.to})
	}
	return out, nil
}

func (m *stubMedia) FetchClip(_ context.Context, _ string, _, _ time.Time) (milestone.MediaClip, error) {
	if m.failWith != nil {
		return milestone.MediaClip{}, m.failWith
	}
	return milestone.MediaClip{
		Kind: "clip", ContentType: "video/mp4", Size: int64(len(m.body)),
		Reader: io.NopCloser(bytes.NewReader(m.body)),
	}, nil
}

func (m *stubMedia) FetchSnapshot(_ context.Context, _ string, _ time.Time) (milestone.MediaClip, error) {
	return milestone.MediaClip{Kind: milestone.MediaSnapshotKind}, nil
}

// fetchWorker builds a worker over a migrated database with a real incident, so
// the fetch path runs against the same rows it writes in production.
func fetchWorker(t *testing.T, media milestone.MediaReader) (*Worker, string, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	ctx := context.Background()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, incident := testutil.NewTenant(t, raw)
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(ctx, cfg, "postgres")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	store, err := evidence.New(evidence.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	window := time.Date(2026, 6, 1, 14, 0, 0, 0, time.UTC)
	if _, err := raw.Exec(ctx, `
		INSERT INTO incidents (tenant_id, incident_id, primary_site_id, state, severity, title,
		                       rule_id, rule_bundle_version, bundle_content_hash, policy_version,
		                       first_event_at, last_event_at)
		VALUES ($1,$2,$3,'open','high','A test incident','r-1','builtin-v1',$4,'none',$5,$6)
		ON CONFLICT DO NOTHING`,
		tenant, incident, site, strings.Repeat("a", 64), window, window.Add(time.Hour)); err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	// The fact reader joins the incident to a canonical event to find its camera,
	// so the incident has to have one. A production incident always does — it was
	// raised from an event — and a fixture without one would be testing a shape
	// the product cannot produce.
	eventID := "evt-" + incident
	if _, err := raw.Exec(ctx, `
		INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
		VALUES ($1, 'int-1', $2, $3)
		ON CONFLICT (tenant_id, integration_id, source_event_id) DO UPDATE SET event_id = EXCLUDED.event_id`,
		tenant, eventID, eventID); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	if _, err := raw.Exec(ctx, `
		INSERT INTO canonical_events (tenant_id, event_id, site_id, source_system_id, source_event_id,
		                              integration_id, event_type, schema_version, observed_at, received_at,
		                              normalized_at, clock_source, clock_drift_ms, device_id, zone_id,
		                              correlation_id, causation_id, payload, integrity_hash)
		VALUES ($1,$2,$3,'sys',$4,'int-1','motion','1',$5,$5,$5,'ntp',0,'cam-door18','zone-door',
		        $4,$4,'{"vendor":{}}'::jsonb, $6)
		ON CONFLICT DO NOTHING`,
		tenant, eventID, site, eventID, window, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := raw.Exec(ctx, `
		INSERT INTO incident_events (tenant_id, incident_id, event_id, role)
		VALUES ($1,$2,$3,'trigger')`, tenant, incident, eventID); err != nil {
		t.Fatalf("seed incident event: %v", err)
	}
	// The device the event names, with the vendor's own reference the fact reader
	// reads. Without it the probe has nothing to ask about.
	if _, err := raw.Exec(ctx, `
		INSERT INTO devices (tenant_id, site_id, device_id, source_device_id, integration_id, kind)
		VALUES ($1,$2,'cam-door18','camera-ref-1','int-1','camera')
		ON CONFLICT DO NOTHING`, tenant, site); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	w := &Worker{
		Bookmarks: &stubWriter{},
		Media:     media,
		Store:     store,
		Facts:     NewPoolFacts(pool),
		Policy:    testPolicy(),
		PreRoll:   time.Minute, PostRoll: time.Minute,
		RetainFor: 24 * time.Hour,
	}
	return w, tenant, site, incident
}

// fetchJob is one evidence-fetch job for the incident the harness seeded.
func fetchJob(t *testing.T, tenant, site, incident string) jobs.Job {
	t.Helper()
	payload, err := dispatch.EncodeJobPayload(dispatch.JobPayload{
		ContractVersion: dispatch.ContractVersion,
		IncidentID:      incident,
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return jobs.Job{
		ID: "job-fetch-1", TenantID: tenant, SiteID: site,
		Queue: jobs.QueueEvidenceFetch, Payload: payload,
		IdempotencyKey: incident + ":evidence.fetch",
	}
}

// TestTheFetchTakesTheBytesWhenTheVendorHoldsThem.
func TestTheFetchTakesTheBytesWhenTheVendorHoldsThem(t *testing.T) {
	at := time.Date(2026, 6, 1, 14, 5, 0, 0, time.UTC)
	media := &stubMedia{
		available: []mediaInterval{
			{from: at.Add(-30 * time.Minute), to: at.Add(30 * time.Minute)},
		},
		source: "recording-index",
		body:   bytes.Repeat([]byte("v"), 8192),
	}
	w, tenant, site, incident := fetchWorker(t, media)
	j := fetchJob(t, tenant, site, incident)

	out, err := w.Handle(context.Background(), j)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if out.Fetched == "" {
		t.Fatal("the vendor held the window and the worker took no bytes. The fetch " +
			"path is C1's whole point: a pointer is not an answer to \"what did you keep\"")
	}
	if !out.Placed || out.EvidenceID == "" {
		t.Errorf("the bookmark and its receipt are still expected: %+v", out)
	}

	var count int
	if err := testutil.Pool(t).QueryRow(context.Background(),
		`SELECT count(*) FROM evidence WHERE evidence_id = $1`, out.Fetched).Scan(&count); err != nil {
		t.Fatalf("count the fetched row: %v", err)
	}
	if count != 1 {
		t.Errorf("the fetched evidence row count is %d, want 1", count)
	}
}

// TestTheFetchSkipsAGapRatherThanDeadletteringOnIt.
func TestTheFetchSkipsAGapRatherThanDeadletteringOnIt(t *testing.T) {
	media := &stubMedia{source: "recording-index"} // the probe holds nothing
	w, tenant, site, incident := fetchWorker(t, media)

	out, err := w.Handle(context.Background(), fetchJob(t, tenant, site, incident))
	if err != nil {
		t.Fatalf("a probe that found nothing is a fact about a recording, not a failure: %v", err)
	}
	if out.Fetched != "" {
		t.Errorf("no bytes were taken, and the outcome says %q", out.Fetched)
	}
	if !out.Placed || out.EvidenceID == "" {
		t.Error("the bookmark and its receipt still stand; only the copy was skipped")
	}
}

// TestAProbeFailureIsARetryNotARefusal.
func TestAProbeFailureIsARetryNotARefusal(t *testing.T) {
	media := &stubMedia{failWith: context.DeadlineExceeded}
	w, tenant, site, incident := fetchWorker(t, media)

	if _, err := w.Handle(context.Background(), fetchJob(t, tenant, site, incident)); err == nil {
		t.Error("a vendor that will not answer was handled as a completed judgement; a " +
			"dead letter here reports a broken worker for a link that was down")
	}
}
