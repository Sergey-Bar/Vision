package normalize_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/clocksub"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/inbox"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/normalize"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// The unit tests in normalize_test.go cover the pure functions with no database.
// The properties below are the ones that only exist because a row is written:
// the replay of an already-normalized key, the integrity hash as it is actually
// stored, and the clock measurement as it is actually recorded. All of them skip
// rather than fail with no database, so a green run with no
// SP_TEST_DATABASE_URL is a run that tested nothing.

const worker = "worker-1"

var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func dbPool(t *testing.T) *dbx.Pool {
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
	return pool
}

// accept writes an inbox event and takes the lease the normalizer matches on.
func accept(t *testing.T, pool *dbx.Pool, tenant, site, sourceID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := inbox.NewWriter(pool).Write(ctx, inbox.WriteOptions{
		TenantID: tenant, SiteID: site, IntegrationID: "int-1", SourceEventID: sourceID,
		Payload:    json.RawMessage(`{"Id":"` + sourceID + `","Type":"Motion"}`),
		ReceivedAt: base,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	leases, err := inbox.NewWriter(pool).ClaimFor(ctx, tenant, worker, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, l := range leases {
		if l.Key.SourceEventID == sourceID {
			return
		}
	}
	t.Fatalf("the lease for %s was not granted", sourceID)
}

func input(tenant, site, sourceID string) normalize.Input {
	return normalize.Input{
		Key:         inbox.Key{TenantID: tenant, IntegrationID: "int-1", SourceEventID: sourceID},
		SiteID:      site,
		EventType:   "motion",
		ObservedAt:  base,
		ReceivedAt:  base.Add(50 * time.Millisecond),
		ClockSource: "vms",
		Payload:     json.RawMessage(`{"Id":"` + sourceID + `","Type":"Motion"}`),
		LeaseOwner:  worker,
	}
}

func countRows(t *testing.T, pool *dbx.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// A replay of a key that already produced an event is a no-op, and this is the
// package's own claim in its doc comment: "Re-running normalization for a key
// that already produced an event is a no-op, not a second event, so crash
// recovery is a retry rather than a reconciliation."
//
// It was not. The already-normalized branch completed the inbox row, which
// requires status='processing' AND lease_owner = ours; a row a prior attempt
// already completed has lease_owner NULL, so the update matched nothing, the
// function returned an error and the transaction rolled back. pipeline.process
// checks err before result.AlreadyNormalized, so a replay of a successfully
// processed event was routed to inbox.Fail and eventually dead-lettered, with
// its canonical event, edges and incident all present.
func TestReplayingAnAlreadyNormalizedEventIsANoOpAndNotAnError(t *testing.T) {
	pool := dbPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	accept(t, pool, tenant, site, "replay-1")

	first, err := normalize.New(pool).Normalize(context.Background(), input(tenant, site, "replay-1"))
	if err != nil {
		t.Fatalf("first normalize: %v", err)
	}
	if first.AlreadyNormalized {
		t.Fatal("a first normalization reported itself as a repeat")
	}

	replay, err := normalize.New(pool).Normalize(context.Background(), input(tenant, site, "replay-1"))
	if err != nil {
		t.Fatalf("replaying a processed key returned an error: %v", err)
	}
	if !replay.AlreadyNormalized {
		t.Error("the replay did not report itself as a repeat")
	}
	if replay.EventID != first.EventID {
		t.Errorf("the replay names event %s, want the one already stored, %s",
			replay.EventID, first.EventID)
	}

	if n := countRows(t, pool, `SELECT count(*) FROM canonical_events WHERE tenant_id = $1`, tenant); n != 1 {
		t.Errorf("%d canonical events after a replay, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM canonical_event_keys WHERE tenant_id = $1`, tenant); n != 1 {
		t.Errorf("%d dedupe keys after a replay, want 1", n)
	}
	// The row must still be processed, with the event it produced attached. A
	// replay that moved it to dead_letter is exactly the defect: an event that
	// was fully processed, terminal in the wrong way.
	var status, eventID string
	if err := pool.QueryRow(context.Background(), `
		SELECT status, COALESCE(canonical_event_id, '')
		  FROM inbox_events
		 WHERE tenant_id = $1 AND integration_id = 'int-1' AND source_event_id = 'replay-1'`,
		tenant).Scan(&status, &eventID); err != nil {
		t.Fatalf("read the inbox row: %v", err)
	}
	if status != string(inbox.StatusProcessed) {
		t.Errorf("after a replay the inbox row is %q, want %q. A fully processed event must "+
			"not end up dead-lettered as collateral of a retry", status, inbox.StatusProcessed)
	}
	if eventID != first.EventID {
		t.Errorf("the inbox row points at %s, want %s", eventID, first.EventID)
	}
}

// The integrity hash must be a function of the event, not of when we happened to
// receive it. It claimed to be: "two runs over the same source event at
// different wall-clock instants must produce the same hash". It was not, because
// the covered field list was followed by h.Write(payload) over the whole
// canonical document, and that document's canonical map contains "receivedAt" -
// so the receipt time was in the hashed bytes by the back door, and the same
// source event normalized at two instants produced two hashes.
func TestTheIntegrityHashDoesNotDependOnTheReceiptTime(t *testing.T) {
	pool := dbPool(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	accept(t, pool, tenant, site, "hash-1")

	first, err := normalize.New(pool).Normalize(context.Background(), input(tenant, site, "hash-1"))
	if err != nil {
		t.Fatalf("first normalize: %v", err)
	}
	var stored string
	if err := pool.QueryRow(context.Background(),
		`SELECT integrity_hash FROM canonical_events WHERE tenant_id = $1`, tenant).Scan(&stored); err != nil {
		t.Fatalf("read the stored hash: %v", err)
	}
	if stored != first.IntegrityHash {
		t.Errorf("the stored hash is %s, want the one returned, %s", stored, first.IntegrityHash)
	}

	// The same source event, received three hours later. The receipt time
	// differs, the event does not.
	later := input(tenant, site, "hash-1")
	later.ReceivedAt = later.ReceivedAt.Add(3 * time.Hour)
	replay, err := normalize.New(pool).Normalize(context.Background(), later)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.IntegrityHash != stored {
		t.Errorf("the same source event received 3h later hashed to %s, want %s. The receipt "+
			"time is in the hashed bytes, so hash comparison cannot be used as a replay or "+
			"dedupe check and the golden fixtures can never be byte-stable",
			replay.IntegrityHash, stored)
	}

	// And a covered field still has to matter, or the fix is just "the hash is
	// constant". A different source event is a different event.
	accept(t, pool, tenant, site, "hash-2")
	other, err := normalize.New(pool).Normalize(context.Background(), input(tenant, site, "hash-2"))
	if err != nil {
		t.Fatalf("second normalize: %v", err)
	}
	if other.IntegrityHash == stored {
		t.Error("two different source events produced the same integrity hash")
	}

	// A field the hash covers but the key does not: the event type.
	accept(t, pool, tenant, site, "hash-3")
	typed := input(tenant, site, "hash-3")
	typed.EventType = "door.open"
	third, err := normalize.New(pool).Normalize(context.Background(), typed)
	if err != nil {
		t.Fatalf("third normalize: %v", err)
	}
	if third.IntegrityHash == stored {
		t.Error("changing the event type did not change the integrity hash; the hash is not " +
			"covering the fields it claims to cover")
	}
}

// "A device with no measurement yields zero with found=false ... That is
// deliberately different from a device measured at exactly zero drift: the first
// has never been checked and the second has been checked and is fine." That was
// a claim about a found flag loadDrift did not have: it returned (int32, error),
// and an empty device id, ErrNoRows and drift_ms=0 were all the same value.
func TestAMeasuredZeroDriftIsNotTheSameAsNoMeasurement(t *testing.T) {
	pool := dbPool(t)
	raw := testutil.Pool(t)
	tenant, site, measured := testutil.NewTenant(t, raw)
	_, _, unmeasured := testutil.NewTenant(t, raw)
	n := normalize.New(pool)

	// A camera that was sampled and found to be exactly right.
	testutil.Exec(t, raw, `
		INSERT INTO device_clocks (tenant_id, device_id, site_id, drift_ms, band, measured_at)
		VALUES ($1, $2, $3, 0, 'ok', now())`, tenant, measured, site)

	accept(t, pool, tenant, site, "clock-measured")
	in := input(tenant, site, "clock-measured")
	in.DeviceID = measured
	checked, err := n.Normalize(context.Background(), in)
	if err != nil {
		t.Fatalf("normalize the measured device: %v", err)
	}
	if !checked.ClockDriftMeasured {
		t.Error("a device measured at exactly zero drift reports no measurement")
	}
	if checked.ClockDriftFrom != normalize.DriftFromDevice {
		t.Errorf("the correction came from %q, want %q", checked.ClockDriftFrom, normalize.DriftFromDevice)
	}

	accept(t, pool, tenant, site, "clock-unmeasured")
	in = input(tenant, site, "clock-unmeasured")
	in.DeviceID = unmeasured
	unchecked, err := n.Normalize(context.Background(), in)
	if err != nil {
		t.Fatalf("normalize the unmeasured device: %v", err)
	}
	if unchecked.ClockDriftMeasured {
		t.Error("a device that has never been sampled reports a measurement")
	}
	if checked.NormalizedAt != unchecked.NormalizedAt {
		t.Errorf("normalized_at differs (%s against %s); a zero drift and an absent one "+
			"must agree on the corrected instant, because there is nothing to correct",
			checked.NormalizedAt, unchecked.NormalizedAt)
	}

	// The stored row is where the distinction has to survive, because the
	// correlator and a months-late audit read the row and not the return value.
	// clock_drift_ms alone cannot carry it: both are zero, which is the whole
	// point of the finding.
	rows := map[string]bool{"clock-measured": true, "clock-unmeasured": false}
	for sourceID, want := range rows {
		var drift int32
		var payload []byte
		if err := raw.QueryRow(context.Background(), `
			SELECT clock_drift_ms, payload FROM canonical_events
			 WHERE tenant_id = $1 AND source_event_id = $2`, tenant, sourceID).Scan(&drift, &payload); err != nil {
			t.Fatalf("read %s: %v", sourceID, err)
		}
		if drift != 0 {
			t.Errorf("%s stored drift %d, want 0", sourceID, drift)
		}
		var doc struct {
			Canonical struct {
				DriftMeasured bool   `json:"clockDriftMeasured"`
				DriftFrom     string `json:"clockDriftFrom"`
			} `json:"canonical"`
		}
		if err := json.Unmarshal(payload, &doc); err != nil {
			t.Fatalf("decode %s payload: %v", sourceID, err)
		}
		if doc.Canonical.DriftMeasured != want {
			t.Errorf("%s stored clockDriftMeasured=%v, want %v. With clock_drift_ms=0 in "+
				"both rows, this is the only thing that separates a clock that was checked from "+
				"one that was never looked at", sourceID, doc.Canonical.DriftMeasured, want)
		}
		if want && doc.Canonical.DriftFrom != normalize.DriftFromDevice {
			t.Errorf("%s stored clockDriftFrom=%q, want %q", sourceID, doc.Canonical.DriftFrom, normalize.DriftFromDevice)
		}
	}
}

// A VMS is the clock for every event it produced, and clocksub has measured and
// stored that skew since the first draft - under a reserved id, read by nobody.
// So a VMS 40 seconds fast produced 40 seconds of uncorrected normalized_at and
// no degradation anywhere, which is the failure the whole clocksub package
// exists to prevent.
func TestTheSourceSystemsSkewIsAppliedWhenTheDeviceHasNoMeasurement(t *testing.T) {
	pool := dbPool(t)
	raw := testutil.Pool(t)
	tenant, site, device := testutil.NewTenant(t, raw)
	ctx := context.Background()

	svc := &clocksub.Service{Pool: pool, Now: func() time.Time { return base }}
	if _, err := svc.MeasureFromSourceTimes(ctx, clock.DefaultPolicy(),
		tenant, site, "int-1", "site-reference", base.Add(40*time.Second), base); err != nil {
		t.Fatalf("MeasureFromSourceTimes: %v", err)
	}

	accept(t, pool, tenant, site, "vms-skew")
	in := input(tenant, site, "vms-skew")
	res, err := normalize.New(pool).Normalize(ctx, in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if res.ClockDriftFrom != normalize.DriftFromSourceSystem {
		t.Fatalf("the correction came from %q, want %q. The measured VMS skew was never read",
			res.ClockDriftFrom, normalize.DriftFromSourceSystem)
	}
	if want := base.Add(-40 * time.Second); !res.NormalizedAt.Equal(want) {
		t.Errorf("normalized_at is %s, want %s. A VMS 40s fast is 40s of uncorrected time "+
			"on every event it produced", res.NormalizedAt, want)
	}

	// The device's own measurement is the more specific one and wins. Applying
	// both would count the same skew twice and correct 80 seconds for a
	// 40-second error.
	testutil.Exec(t, raw, `
		INSERT INTO device_clocks (tenant_id, device_id, site_id, drift_ms, band, measured_at)
		VALUES ($1, $2, $3, 4000, 'warn', now())`, tenant, device, site)
	accept(t, pool, tenant, site, "device-wins")
	in = input(tenant, site, "device-wins")
	in.DeviceID = device
	res, err = normalize.New(pool).Normalize(ctx, in)
	if err != nil {
		t.Fatalf("normalize with a device measurement: %v", err)
	}
	if res.ClockDriftFrom != normalize.DriftFromDevice {
		t.Errorf("the correction came from %q, want %q", res.ClockDriftFrom, normalize.DriftFromDevice)
	}
	if want := base.Add(-4 * time.Second); !res.NormalizedAt.Equal(want) {
		t.Errorf("normalized_at is %s, want %s. The device's own 4s measurement is the "+
			"specific one; summing it with the VMS's would correct 44s for a 4s error",
			res.NormalizedAt, want)
	}
}
