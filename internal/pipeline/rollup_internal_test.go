package pipeline

// The rollup loop, and the two things it has to get right.
//
// # The bucket it derives
//
// The PREVIOUS hour, and the hour is TRUNCATED. A bucket is a half-open
// interval and its boundaries are the hour, so a window that started at 10:17
// would be a bucket nothing else agrees with, and the reader's sum and the
// writer's key would disagree about which rows belong to it.
//
// # What it is a producer of
//
// A DERIVATION, not a recording. The loop derives the hour that ended most
// recently, and the same bucket derived twice is the same row.
//
// The test is in-package because the derivation is an unexported method, and a
// test that could only reach it through the public surface would have to start
// the whole pipeline to observe one bucket.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestTheRollupDerivesTheHourThatJustEnded is the derivation itself.
//
// A canonical event in the hour before this one is counted, and a canonical
// event in the hour before THAT is not, because the loop derives one bucket and
// not the history.
func TestTheRollupDerivesTheHourThatJustEnded(t *testing.T) {
	ctx := context.Background()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(ctx, cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer pool.Close()

	tenant, site, device := testutil.NewTenant(t, raw)

	// The event belongs to the hour that just ended, so it is inside the
	// bucket the loop derives.
	inside := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Minute)
	// And one that belongs to the hour before that, which the loop does not
	// derive.
	outside := inside.Add(-time.Hour)
	insertCanonical(t, raw, tenant, site, device, "evt-rollup-inside", inside)
	insertCanonical(t, raw, tenant, site, device, "evt-rollup-outside", outside)

	loops := New(Deps{Pool: pool, Tenant: tenant, SiteID: site})
	loops.rollupOnce(ctx)

	var n int64
	var bucketStart time.Time
	if err := pool.For(tenant, "test").QueryRow(ctx,
		`SELECT n, bucket_start
		   FROM metric_rollups
		  WHERE tenant_id = $1 AND metric = 'canonical_events' AND site_id = $2`,
		tenant, site).Scan(&n, &bucketStart); err != nil {
		t.Fatalf("read the rollup: %v", err)
	}
	if n != 1 {
		t.Errorf("the rollup counted %d canonical event(s), want 1. The event in the hour that "+
			"just ended is inside the bucket and the event two hours ago is not", n)
	}
	wantStart := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	if !bucketStart.Equal(wantStart) {
		t.Errorf("the bucket starts at %s, want %s. A bucket that does not start on the hour is a "+
			"bucket nothing else agrees with", bucketStart.UTC().Format(time.RFC3339),
			wantStart.UTC().Format(time.RFC3339))
	}
}

// TestTheRollupIsOffWithoutAPool is the shape check.
//
// A pipeline with no database has no rollup, and the derivation says so rather
// than writing to a nil pool.
func TestTheRollupIsOffWithoutAPool(t *testing.T) {
	loops := &Loops{Deps: Deps{Tenant: "t-nothing"}}
	loops.rollupOnce(context.Background())
}

// insertCanonical writes one canonical event, which is the North Star's
// denominator.
func insertCanonical(t *testing.T, raw *pgxpool.Pool, tenant, site, device, eventID string, at time.Time) {
	t.Helper()
	if _, err := raw.Exec(context.Background(), `
		INSERT INTO canonical_events
			(tenant_id, event_id, site_id, source_system_id, source_event_id, integration_id,
			 event_type, schema_version, observed_at, received_at, normalized_at, device_id,
			 integrity_hash)
		VALUES ($1, $2, $3, 'sys-canary', $2, 'int-canary', 'motion', '1', $4, $4, $4, $5,
		        repeat('a', 64))`,
		tenant, eventID, site, at, device); err != nil {
		t.Fatalf("insert the canonical event: %v", err)
	}
}

// TestTheRollupLoopRunsOnItsInterval is the loop, not the derivation.
//
// The loop is what makes the rollup a PRODUCT rather than a command somebody
// remembers to run, and the thing that can go wrong with a loop is that it
// never ticks. So the loop is started, waited for, and stopped.
func TestTheRollupLoopRunsOnItsInterval(t *testing.T) {
	ctx := context.Background()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(ctx, cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer pool.Close()

	tenant, site, device := testutil.NewTenant(t, raw)
	insertCanonical(t, raw, tenant, site, device, "evt-rollup-loop",
		time.Now().UTC().Truncate(time.Hour).Add(-30*time.Minute))

	loops := New(Deps{
		Pool:           pool,
		Tenant:         tenant,
		SiteID:         site,
		RollupInterval: 50 * time.Millisecond,
	})

	stop, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		loops.runRollup(stop)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	var n int64
	for time.Now().Before(deadline) {
		if err := pool.For(tenant, "test").QueryRow(ctx,
			`SELECT count(*) FROM metric_rollups
			  WHERE tenant_id = $1 AND metric = 'canonical_events' AND site_id = $2`,
			tenant, site).Scan(&n); err == nil && n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	if n == 0 {
		t.Error("the rollup loop ran and derived nothing. A loop that never ticks is a loop " +
			"that looks exactly like one that works")
	}
}
