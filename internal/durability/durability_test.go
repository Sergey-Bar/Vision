package durability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/normalize"
	"github.com/Sergey-Bar/vision/internal/outbox"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// defaultWorker is the lease holder the tests claim as. The normalizer matches
// the inbox transition on the lease owner, so this has to line up with the
// worker that took the lease.
const defaultWorker = "worker"

func pool(t *testing.T) *dbx.Pool {
	t.Helper()
	testutil.Pool(t)
	cfg := dbx.DefaultConfig()
	dsn := os.Getenv(testutil.DatabaseURLEnv)
	if err := cfg.ApplyDSN(dsn); err != nil {
		t.Fatalf("parse %s: %v", testutil.DatabaseURLEnv, err)
	}
	p, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func migrated(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t)
	// Always apply pending migrations rather than only on an empty schema: a new
	// migration added to the tree has to reach a database that already has the
	// older ones, or the suite would keep testing a schema that is no longer
	// what this build produces.
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool(t)
}

func siteOf(t *testing.T) (tenant, site string) {
	t.Helper()
	tenant, site, _ = testutil.NewTenant(t, testutil.Pool(t))
	return tenant, site
}

func writeOpts(tenant, site, sourceID string) inbox.WriteOptions {
	return inbox.WriteOptions{
		TenantID:      tenant,
		SiteID:        site,
		IntegrationID: "int-1",
		SourceEventID: sourceID,
		Payload:       json.RawMessage(`{"Id":"` + sourceID + `","Type":"Motion"}`),
		ReceivedAt:    base,
	}
}

// ---------------------------------------------------------------------------
// Invariant 1: zero accepted event loss, and exactly-once effect.
// ---------------------------------------------------------------------------

func TestWriteAcceptsOnce(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)

	first, err := w.Write(context.Background(), writeOpts(tenant, site, "src-1"))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if first.Duplicate {
		t.Fatal("the first write was reported as a duplicate")
	}

	// A duplicate insert is a no-op, never an error. A source that re-delivers
	// after a reconnect is the normal case, not a fault.
	second, err := w.Write(context.Background(), writeOpts(tenant, site, "src-1"))
	if err != nil {
		t.Fatalf("duplicate write returned an error rather than a no-op: %v", err)
	}
	if !second.Duplicate {
		t.Error("the second write was not recognised as a duplicate")
	}

	var n int
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_events WHERE tenant_id = $1 AND source_event_id = 'src-1'`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d inbox rows for one source event, want 1", n)
	}
}

// The dedupe key is global, not per partition. A duplicate arriving a month
// later is the case a per-partition constraint would miss.
func TestDedupeCrossesPartitions(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)

	jan := writeOpts(tenant, site, "src-jan")
	jan.ReceivedAt = base.AddDate(0, -2, 0)
	if _, err := w.Write(context.Background(), jan); err != nil {
		t.Fatalf("january write: %v", err)
	}

	mar := writeOpts(tenant, site, "src-jan")
	mar.ReceivedAt = base
	dup, err := w.Write(context.Background(), mar)
	if err != nil {
		t.Fatalf("march write: %v", err)
	}
	if !dup.Duplicate {
		t.Error("a source event delivered in a later month was accepted twice")
	}
}

// Two workers writing the same key concurrently must produce one row. The
// uniqueness is enforced by the database, not by the callers agreeing.
func TestConcurrentWritesProduceOneRow(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)

	const writers = 8
	var wg sync.WaitGroup
	dupes := make([]bool, writers)
	errs := make([]error, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			accepted, err := w.Write(context.Background(), writeOpts(tenant, site, "race-1"))
			dupes[n] = accepted.Duplicate
			errs[n] = err
		}(i)
	}
	wg.Wait()

	accepted := 0
	for i := 0; i < writers; i++ {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}
		if !dupes[i] {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("%d writers were told they accepted the event, want exactly 1", accepted)
	}
}

func TestWriteRejectsUnidentifiedEvents(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)

	// Without a source id there is no dedupe key, and an event that cannot be
	// deduplicated is re-ingested on every poll for ever.
	_, err := w.Write(context.Background(), writeOpts(tenant, site, ""))
	if err == nil {
		t.Error("an event with no source id was accepted")
	}
}

// ---------------------------------------------------------------------------
// Leases: a crashed worker's work is recovered, not lost and not duplicated.
// ---------------------------------------------------------------------------

func TestClaimLeasesAndIsExclusive(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	for i := 0; i < 5; i++ {
		if _, err := w.Write(context.Background(), writeOpts(tenant, site, fmt.Sprintf("lease-%d", i))); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	first, err := w.ClaimFor(context.Background(), tenant, "worker-a", 3, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("claimed %d events, want 3", len(first))
	}
	for _, l := range first {
		if l.Owner != "worker-a" {
			t.Errorf("lease owner is %q", l.Owner)
		}
		if l.LeaseExpiresAt.IsZero() {
			t.Error("a leased event has no lease expiry; a crashed worker would hold it for ever")
		}
		if l.Attempts != 1 {
			t.Errorf("attempt count is %d on first claim, want 1", l.Attempts)
		}
	}

	// A second worker must not see events already leased to the first.
	second, err := w.ClaimFor(context.Background(), tenant, "worker-b", 10, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	claimed := map[string]bool{}
	for _, l := range first {
		claimed[l.Key.SourceEventID] = true
	}
	for _, l := range second {
		if claimed[l.Key.SourceEventID] {
			t.Errorf("worker-b claimed %s, which worker-a already holds", l.Key.SourceEventID)
		}
	}
}

// A lease that expires is reclaimed. That is the whole recovery mechanism for a
// worker that was killed mid-batch.
func TestExpiredLeaseIsReclaimed(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "expired-1")); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := w.ClaimFor(context.Background(), tenant, "dead-worker", 1, time.Hour); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Simulate the crash: the lease is expired but the row is still leased.
	testutil.Exec(t, testutil.Pool(t),
		`UPDATE inbox_events SET lease_expires_at = now() - interval '1 second'
		  WHERE tenant_id = $1`, tenant)

	recovered, err := w.ClaimFor(context.Background(), tenant, "live-worker", 1, time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("reclaimed %d events, want 1", len(recovered))
	}
	if recovered[0].Attempts != 2 {
		t.Errorf("attempt count is %d after recovery, want 2", recovered[0].Attempts)
	}
}

func TestRenewFailsOnceTheLeaseIsLost(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "renew-1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	leases, err := w.ClaimFor(context.Background(), tenant, "worker-a", 1, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	key := leases[0].Key

	if err := w.Renew(context.Background(), key, "worker-a", time.Minute); err != nil {
		t.Fatalf("renew by the holder: %v", err)
	}
	// Renewing a lease somebody else now holds must fail, so the worker stops
	// rather than continuing on a row another worker may re-process.
	if err := w.Renew(context.Background(), key, "worker-b", time.Minute); err == nil {
		t.Error("a non-holder renewed a lease")
	}
}

func TestFailReachesDeadLetterWithAReason(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "doomed")); err != nil {
		t.Fatalf("write: %v", err)
	}

	cause := fmt.Errorf("source sent a payload this build cannot read")
	for i := 1; i <= 2; i++ {
		leases, err := w.ClaimFor(context.Background(), tenant, "worker", 1, time.Minute)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if len(leases) != 1 {
			t.Fatalf("claim %d returned %d leases", i, len(leases))
		}
		status, err := w.Fail(context.Background(), leases[0].Key, leases[0].Owner, cause, 2)
		if err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
		want := inbox.StatusFailed
		if i == 2 {
			want = inbox.StatusDeadLetter
		}
		if status != want {
			t.Errorf("attempt %d left status %q, want %q", i, status, want)
		}
		// A failed event must become immediately retryable in a test, or the
		// backoff would make this test take hours.
		testutil.Exec(t, testutil.Pool(t),
			`UPDATE inbox_events SET lease_expires_at = now() WHERE tenant_id = $1`, tenant)
	}

	letters, err := w.DeadLetters(context.Background(), tenant, site, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("%d dead letters, want 1", len(letters))
	}
	if letters[0].Reason == "" {
		t.Error("a dead letter has no reason; a dead letter with no reason is an incident nobody can act on")
	}
}

// ---------------------------------------------------------------------------
// Normalization: the crash window that produces either loss or duplication.
// ---------------------------------------------------------------------------

func normalizeInput(tenant, site, sourceID string) normalize.Input {
	return normalize.Input{
		Key:         inbox.Key{TenantID: tenant, IntegrationID: "int-1", SourceEventID: sourceID},
		SiteID:      site,
		EventType:   "motion",
		ObservedAt:  base,
		ReceivedAt:  base.Add(50 * time.Millisecond),
		ClockSource: "vms",
		Payload:     json.RawMessage(`{"Id":"` + sourceID + `","Type":"Motion"}`),
		LeaseOwner:  defaultWorker,
	}
}

func TestNormalizeProducesExactlyOneEvent(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	n := normalize.New(p)

	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "n-1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	leases, err := w.ClaimFor(context.Background(), tenant, "worker", 1, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	res, err := n.Normalize(context.Background(), normalizeInput(tenant, site, "n-1"))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if res.AlreadyNormalized {
		t.Fatal("a first normalization reported itself as a repeat")
	}
	if res.IntegrityHash == "" {
		t.Error("the canonical event has no integrity hash")
	}

	var events, processed int
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM canonical_events WHERE tenant_id = $1`, tenant).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Errorf("%d canonical events, want 1", events)
	}
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_events WHERE tenant_id = $1 AND status = 'processed'`,
		tenant).Scan(&processed); err != nil {
		t.Fatalf("count processed: %v", err)
	}
	if processed != 1 {
		t.Errorf("%d inbox events reached processed, want 1", processed)
	}
	_ = leases
}

// The central crash-safety property: re-normalizing a key that already produced
// an event must be a no-op, not a second event. This is what makes recovery a
// retry rather than a reconciliation.
func TestReNormalizeIsANoOp(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	n := normalize.New(p)

	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "dup-norm")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := w.ClaimFor(context.Background(), tenant, "worker", 1, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	first, err := n.Normalize(context.Background(), normalizeInput(tenant, site, "dup-norm"))
	if err != nil {
		t.Fatalf("first normalize: %v", err)
	}

	// The crash: the worker died before it recorded anything externally, so it
	// re-leases and redoes the work. The inbox event is already processed, so the
	// lease fails - which is the correct outcome and is what the second pass
	// below models by going through the key check directly.
	leases, err := w.ClaimFor(context.Background(), tenant, "worker-2", 10, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, l := range leases {
		if l.Key.SourceEventID == "dup-norm" {
			t.Fatal("a processed inbox event was re-leased; recovery would loop")
		}
	}

	var n1, n2 int
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM canonical_events WHERE tenant_id = $1`, tenant).Scan(&n1); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("%d canonical events after one normalization, want 1", n1)
	}

	// A retry of an already-normalized key is a no-op, and it is a no-op that
	// says so.
	//
	// This used to accept either outcome — it counted the events in both arms
	// and asserted nothing about the error. That is how the replay path went on
	// always failing: every call died at the key claim with "no rows in result
	// set", which this test's tolerant arm was shaped to survive. A test that
	// accepts the failure it is meant to exclude is not a test.
	second, err := n.Normalize(context.Background(), normalizeInput(tenant, site, "dup-norm"))
	if err != nil {
		t.Fatalf("re-normalizing an already-normalized key returned %v. A repeat is a no-op, "+
			"not a second event and not a failure: the package's own doc says crash recovery "+
			"is a retry rather than a reconciliation, and an error here routes the event to "+
			"inbox.Fail, which dead-letters a fully processed event", err)
	}
	if !second.AlreadyNormalized {
		t.Error("a second normalization did not report itself as a repeat")
	}
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM canonical_events WHERE tenant_id = $1`, tenant).Scan(&n2); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n2 != 1 {
		t.Fatalf("a repeat normalization produced %d events, want 1", n2)
	}
	if second.EventID != first.EventID {
		t.Errorf("the repeat produced event %q, the first produced %q. A repeat must name the "+
			"event the first one made", second.EventID, first.EventID)
	}
	if first.EventID == "" {
		t.Error("the first normalization produced no event id")
	}
}

// The canonical event and the inbox transition are one transaction, and the key
// claim is inside it. The failure used here is a real one rather than a contrived
// one: the lease is stolen mid-flight, so the inbox transition matches no row and
// the whole transaction must roll back - key included. A surviving key would make
// every future retry a silent no-op and lose the event for good.
func TestNormalizeIsAtomicWithTheInboxTransition(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	n := normalize.New(p)

	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "atomic-1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := w.ClaimFor(context.Background(), tenant, "worker-a", 1, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// The lease is taken away and given to somebody else, so worker-a's
	// transition will match no row.
	testutil.Exec(t, testutil.Pool(t),
		`UPDATE inbox_events SET lease_expires_at = now() - interval '1 second' WHERE tenant_id = $1`, tenant)
	other, err := w.ClaimFor(context.Background(), tenant, "worker-b", 1, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(other) != 1 || other[0].Owner != "worker-b" {
		t.Fatalf("the lease was not transferred: %+v", other)
	}

	if _, err := n.Normalize(context.Background(), normalizeInput(tenant, site, "atomic-1")); err == nil {
		t.Fatal("a normalization whose lease was stolen succeeded")
	}

	var keys, events int
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM canonical_event_keys WHERE tenant_id = $1`, tenant).Scan(&keys); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM canonical_events WHERE tenant_id = $1`, tenant).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if keys != 0 {
		t.Errorf("%d dedupe key(s) survived a rolled-back normalization; every future retry "+
			"would be swallowed as already-done and the event would be lost for good", keys)
	}
	if events != 0 {
		t.Errorf("%d canonical event(s) survived a rolled-back normalization", events)
	}
}

// A source whose clock runs fast must have its timestamps corrected, and the
// correction must be recorded rather than applied invisibly.
func TestNormalizeCorrectsClockDrift(t *testing.T) {
	p := migrated(t)
	tenant, site, device := testutil.NewTenant(t, testutil.Pool(t))
	raw := testutil.Pool(t)

	// The tenant's camera runs 40 seconds fast: far past any sane severe band.
	testutil.Exec(t, raw, `
		INSERT INTO device_clocks (tenant_id, device_id, site_id, drift_ms, band, measured_at)
		VALUES ($1, $2, $3, 40000, 'severe', now())`, tenant, device, site)

	w := inbox.NewWriter(p)
	n := normalize.New(p)
	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "drift-1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := w.ClaimFor(context.Background(), tenant, "worker", 1, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}

	in := normalizeInput(tenant, site, "drift-1")
	in.DeviceID = device
	res, err := n.Normalize(context.Background(), in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	// The camera claims 09:00:00 but is 40 seconds fast, so it normalizes to
	// 08:59:20.
	want := base.Add(-40 * time.Second)
	if !res.NormalizedAt.Equal(want) {
		t.Errorf("normalized_at is %s, want %s", res.NormalizedAt, want)
	}

	var drift int32
	var band string
	if err := raw.QueryRow(context.Background(),
		`SELECT clock_drift_ms, clock_source FROM canonical_events WHERE tenant_id = $1`,
		tenant).Scan(&drift, &band); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if drift != 40000 {
		t.Errorf("stored drift is %dms, want 40000", drift)
	}
	if band != "vms" {
		t.Errorf("stored clock source is %q, want vms", band)
	}
}

// An unreadable source timestamp must fall back visibly.
func TestNormalizeFallsBackVisibly(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	w := inbox.NewWriter(p)
	n := normalize.New(p)

	if _, err := w.Write(context.Background(), writeOpts(tenant, site, "notime-1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := w.ClaimFor(context.Background(), tenant, "worker", 1, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}

	in := normalizeInput(tenant, site, "notime-1")
	in.ObservedAt = time.Time{}
	res, err := n.Normalize(context.Background(), in)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !res.UsedReceivedFallback {
		t.Error("a missing source timestamp was not reported as a fallback")
	}
	if !res.NormalizedAt.Equal(in.ReceivedAt) {
		t.Errorf("normalized_at is %s, want the receipt time %s", res.NormalizedAt, in.ReceivedAt)
	}
}

// The event id is derived from the scoped key, not generated, so a retry after a
// crash lands on the same identity.
func TestEventIDIsDerivedFromTheScopedKey(t *testing.T) {
	key := inbox.Key{TenantID: "t", IntegrationID: "int-1", SourceEventID: "src-1"}

	// The derivation is checked directly rather than through a database round
	// trip: this is the property that makes a crash retry a retry rather than a
	// reconciliation, and it holds with no database at all.
	a := normalize.DeriveEventID(key)
	b := normalize.DeriveEventID(key)
	if a != b {
		t.Errorf("event id is not stable: %q then %q", a, b)
	}
	if a == "" {
		t.Fatal("event id is empty")
	}

	other := inbox.Key{TenantID: "t", IntegrationID: "int-1", SourceEventID: "src-2"}
	if normalize.DeriveEventID(other) == a {
		t.Error("two different source events produced the same event id")
	}
	// The tenant and integration are part of the key, so two tenants using the
	// same source id must not collide.
	otherTenant := inbox.Key{TenantID: "t2", IntegrationID: "int-1", SourceEventID: "src-1"}
	if normalize.DeriveEventID(otherTenant) == a {
		t.Error("the same source event id in two tenants produced the same event id")
	}
}

// The integrity hash must not depend on the receipt time, or two runs at
// different wall-clock instants would not agree.
func TestIntegrityHashIgnoresReceiptTime(t *testing.T) {
	in1 := normalizeInput("t", "s", "src-1")
	in2 := normalizeInput("t", "s", "src-1")
	in2.ReceivedAt = in1.ReceivedAt.Add(3 * time.Hour)

	payload, _ := json.Marshal([]byte(`{"a":1}`))
	h1 := normalize.IntegrityHash(in1, base, payload)
	h2 := normalize.IntegrityHash(in2, base, payload)
	if h1 != h2 {
		t.Error("the integrity hash changed with the receipt time; the golden tests would never be stable")
	}

	in3 := normalizeInput("t", "s", "src-2")
	if h3 := normalize.IntegrityHash(in3, base, payload); h3 == h1 {
		t.Error("two different source events produced the same integrity hash")
	}
}

// ---------------------------------------------------------------------------
// Jobs.
// ---------------------------------------------------------------------------

func TestJobEnqueueIsIdempotentPerQueue(t *testing.T) {
	p := migrated(t)
	tenant, _ := siteOf(t)
	e := jobs.New(p)

	created, id, err := enqueue(e, tenant, jobs.QueueVLM, "k1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !created {
		t.Fatal("the first enqueue reported no work created")
	}
	if id == "" {
		t.Error("the enqueue returned no job id")
	}

	created2, _, err := enqueue(e, tenant, jobs.QueueVLM, "k1")
	if err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	if created2 {
		t.Error("a duplicate enqueue created a second job; that is a second VLM inference")
	}

	// The same key on another queue is a different job, not a duplicate.
	created3, _, err := enqueue(e, tenant, jobs.QueueEvidenceFetch, "k1")
	if err != nil {
		t.Fatalf("cross-queue enqueue: %v", err)
	}
	if !created3 {
		t.Error("the same key on a different queue was treated as a duplicate")
	}
}

func TestJobEnqueueRequiresAnIdempotencyKey(t *testing.T) {
	p := migrated(t)
	tenant, _ := siteOf(t)
	e := jobs.New(p)

	err := inTx(t, p, func(tx pgx.Tx) error {
		_, _, err := e.Enqueue(context.Background(), tx, jobs.Enqueue{
			TenantID: tenant, Queue: jobs.QueueVLM, Payload: json.RawMessage(`{}`),
		})
		return err
	})
	if err == nil {
		t.Error("a job with no idempotency key was accepted; a duplicate enqueue is a second inference")
	}
}

func TestJobClaimIsExclusiveAndRecoverable(t *testing.T) {
	p := migrated(t)
	tenant, _ := siteOf(t)
	e := jobs.New(p)
	for i := 0; i < 4; i++ {
		if _, _, err := enqueue(e, tenant, jobs.QueueEvidenceFetch, fmt.Sprintf("ev-%d", i)); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	w := &jobs.Worker{Enqueuer: *e, Name: jobs.QueueEvidenceFetch}
	a, err := w.Claim(context.Background(), tenant, "worker-a", 2)
	if err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if len(a) != 2 {
		t.Fatalf("worker-a claimed %d, want 2", len(a))
	}
	if a[0].LeaseExpiresAt.IsZero() {
		t.Error("a claimed job has no lease expiry; a crashed worker would hold it for ever")
	}

	b, err := w.Claim(context.Background(), tenant, "worker-b", 10)
	if err != nil {
		t.Fatalf("claim b: %v", err)
	}
	held := map[string]bool{}
	for _, j := range a {
		held[j.ID] = true
	}
	for _, j := range b {
		if held[j.ID] {
			t.Errorf("worker-b claimed %s which worker-a holds; FOR UPDATE SKIP LOCKED is not doing its job", j.ID)
		}
	}

	// A crashed worker's jobs are recovered when the lease expires. This is the
	// whole recovery mechanism for a worker killed mid-batch.
	testutil.Exec(t, testutil.Pool(t),
		`UPDATE jobs SET lease_expires_at = now() - interval '1 second' WHERE lease_owner = 'worker-a'`)
	recovered, err := w.Claim(context.Background(), tenant, "worker-c", 10)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if len(recovered) != 2 {
		t.Errorf("recovered %d jobs, want 2", len(recovered))
	}
}

func TestJobDeadLetterIsVisibleWithAReason(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	e := jobs.New(p)

	if err := inTx(t, p, func(tx pgx.Tx) error {
		_, _, err := e.Enqueue(context.Background(), tx, jobs.Enqueue{
			TenantID: tenant, Queue: jobs.QueueRuleBulk, IdempotencyKey: "bad-1",
			MaxAttempts: 1,
		})
		return err
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w := &jobs.Worker{Enqueuer: *e, Name: jobs.QueueRuleBulk}
	claimed, err := w.Claim(context.Background(), tenant, "worker", 1)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d", len(claimed))
	}
	status, err := w.Fail(context.Background(), tenant, claimed[0].ID, "worker",
		fmt.Errorf("bundle references an unknown zone"))
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if status != jobs.StatusDeadLetter {
		t.Errorf("status is %q, want dead_letter", status)
	}

	letters, err := w.DeadLetters(context.Background(), tenant, site, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("%d dead letters, want 1", len(letters))
	}
	if letters[0].Reason == "" {
		t.Error("a dead letter has no reason; a dead letter with no reason is an incident nobody can act on")
	}

	// Replay is an operator action, not an automatic behaviour: a dead letter
	// usually means the dependency is broken, and retrying in a loop turns a
	// visible incident into invisible load.
	if err := w.Replay(context.Background(), tenant, letters[0].ID); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	after, err := w.DeadLetters(context.Background(), tenant, site, 10)
	if err != nil {
		t.Fatalf("DeadLetters after replay: %v", err)
	}
	if len(after) != 0 {
		t.Errorf("%d dead letters after an operator replay, want 0", len(after))
	}
}

func TestCompletingAJobSomebodyElseLeasesFails(t *testing.T) {
	p := migrated(t)
	tenant, _ := siteOf(t)
	e := jobs.New(p)
	if _, _, err := enqueue(e, tenant, jobs.QueueVLM, "steal-1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	w := &jobs.Worker{Enqueuer: *e, Name: jobs.QueueVLM}
	claimed, _ := w.Claim(context.Background(), tenant, "worker-a", 1)
	if len(claimed) != 1 {
		t.Fatalf("claimed %d", len(claimed))
	}
	if err := w.Complete(context.Background(), tenant, claimed[0].ID, "worker-b"); err == nil {
		t.Error("a worker completed a job it does not hold")
	}
	if err := w.Complete(context.Background(), tenant, claimed[0].ID, "worker-a"); err != nil {
		t.Errorf("the holder could not complete its own job: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Outbox.
// ---------------------------------------------------------------------------

func TestOutboxEnqueueIsTransactional(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)

	// A message enqueued in a transaction that rolls back must not survive.
	err := p.InTx(context.Background(), func(tx pgx.Tx) error {
		if err := outbox.Enqueue(context.Background(), tx, outbox.Message{
			TenantID: tenant, SiteID: site, Topic: "incident.created",
			IdempotencyKey: "rolled-back", Payload: json.RawMessage(`{"a":1}`),
		}); err != nil {
			return err
		}
		return fmt.Errorf("forced rollback")
	})
	if err == nil {
		t.Fatal("the forced rollback did not happen")
	}

	var n int64
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE idempotency_key = 'rolled-back'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d message(s) survived a rolled-back transaction; a side effect would fire "+
			"for a state change that never happened", n)
	}
}

func TestOutboxDeliversAtLeastOnceAndTerminally(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	// Keys are unique per test: the outbox key is scoped to the tenant, not to
	// the test run, so a fixed key would collide with a previous run's row and
	// quietly enqueue nothing.
	prefix := uniquePrefix()
	if err := p.InTx(context.Background(), func(tx pgx.Tx) error {
		for i := 0; i < 3; i++ {
			if err := outbox.Enqueue(context.Background(), tx, outbox.Message{
				TenantID: tenant, SiteID: site, Topic: "notify_operator",
				IdempotencyKey: fmt.Sprintf("%s-msg-%d", prefix, i), Payload: json.RawMessage(`{}`),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	seen := map[string]int{}
	relay := &outbox.Relay{
		Pool: p,
		Handler: func(_ context.Context, d outbox.Delivery) error {
			seen[d.IdempotencyKey]++
			return nil
		},
	}
	delivered, err := relay.DrainOnce(context.Background(), tenant, "relay", 10)
	if err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if delivered != 3 {
		t.Errorf("delivered %d, want 3", delivered)
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("%s was delivered %d time(s) on the first pass", k, n)
		}
	}

	// A second pass delivers nothing: a delivered message is terminal. The
	// at-least-once guarantee is about a crash between the handler and the
	// mark-delivered write, which is why handlers must be idempotent, not about
	// re-sending what already succeeded.
	delivered, err = relay.DrainOnce(context.Background(), tenant, "relay", 10)
	if err != nil {
		t.Fatalf("second DrainOnce: %v", err)
	}
	if delivered != 0 {
		t.Errorf("a second pass delivered %d already-delivered messages", delivered)
	}
}

func TestOutboxIsolatesAPoisonMessage(t *testing.T) {
	p := migrated(t)
	tenant, site := siteOf(t)
	prefix := uniquePrefix()
	if err := p.InTx(context.Background(), func(tx pgx.Tx) error {
		if err := outbox.Enqueue(context.Background(), tx, outbox.Message{
			TenantID: tenant, SiteID: site, Topic: "t",
			IdempotencyKey: prefix + "-poison", Payload: json.RawMessage(`{}`),
		}); err != nil {
			return err
		}
		return outbox.Enqueue(context.Background(), tx, outbox.Message{
			TenantID: tenant, SiteID: site, Topic: "t",
			IdempotencyKey: prefix + "-good", Payload: json.RawMessage(`{}`),
		})
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	delivered := 0
	relay := &outbox.Relay{
		Pool: p,
		Handler: func(_ context.Context, d outbox.Delivery) error {
			if strings.HasSuffix(d.IdempotencyKey, "-poison") {
				return fmt.Errorf("the operator directory is unreachable")
			}
			delivered++
			return nil
		},
	}
	if _, err := relay.DrainOnce(context.Background(), tenant, "relay", 10); err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	// One poison message must not roll back or block the good one. Batching them
	// into a single transaction would do exactly that, and the poison message
	// would then be retried for ever.
	if delivered != 1 {
		t.Errorf("%d good messages delivered, want 1", delivered)
	}
}

// Two tenants on one instance naturally generate the same idempotency key for
// their own side effect. A globally unique key would swallow the second one.
func TestOutboxIdempotencyKeyIsScopedToTheTenant(t *testing.T) {
	p := migrated(t)
	tenantA, siteA, _ := testutil.NewTenant(t, testutil.Pool(t))
	tenantB, siteB, _ := testutil.NewTenant(t, testutil.Pool(t))
	key := uniquePrefix() + "-incident-created:inc-1"

	for _, e := range []struct{ tenant, site string }{{tenantA, siteA}, {tenantB, siteB}} {
		if err := p.InTx(context.Background(), func(tx pgx.Tx) error {
			return outbox.Enqueue(context.Background(), tx, outbox.Message{
				TenantID: e.tenant, SiteID: e.site, Topic: "incident.created",
				IdempotencyKey: key, Payload: json.RawMessage(`{}`),
			})
		}); err != nil {
			t.Fatalf("enqueue for %s: %v", e.tenant, err)
		}
	}

	var n int64
	if err := p.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("%d messages for one key in two tenants, want 2", n)
	}
}

func TestOutboxEnqueueRequiresAnIdempotencyKey(t *testing.T) {
	p := migrated(t)
	tenant, _ := siteOf(t)
	err := p.InTx(context.Background(), func(tx pgx.Tx) error {
		return outbox.Enqueue(context.Background(), tx, outbox.Message{
			TenantID: tenant, Topic: "t", Payload: json.RawMessage(`{}`),
		})
	})
	if err == nil {
		t.Error("a message with no idempotency key was accepted; a consumer cannot dedupe what it cannot identify")
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// enqueue adds a job in a committed transaction, which is the honest shape:
// Enqueue takes a transaction so the job and the state change that caused it
// commit together, and a test using a bare connection would not be testing that.
func enqueue(e *jobs.Enqueuer, tenant string, queue jobs.Queue, key string) (bool, string, error) {
	p := e.Pool
	var created bool
	var id string
	err := p.InTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		created, id, err = e.Enqueue(context.Background(), tx, jobs.Enqueue{
			TenantID: tenant, Queue: queue, IdempotencyKey: key,
			Payload: json.RawMessage(`{"x":1}`),
		})
		return err
	})
	return created, id, err
}

// uniquePrefix keeps outbox keys from colliding with a previous run's rows. The
// key is scoped to the tenant rather than to a test, so a fixed key would make a
// second run enqueue nothing and then assert it delivered three.
func uniquePrefix() string {
	return fmt.Sprintf("t%d", time.Now().UnixNano())
}

func inTx(t *testing.T, p *dbx.Pool, fn func(tx pgx.Tx) error) error {
	t.Helper()
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(context.Background())
		return err
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return nil
}
