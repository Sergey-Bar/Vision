package migrate_test

// The partition sweep, and the two things it must not do.
//
// # It must not drop a partition the install is keeping
//
// The sweep drops a partition whose WHOLE period ended before the horizon. A
// partition that straddles the horizon is not dropped, and neither is the
// current one, whatever the horizon says.
//
// # It must not drop anything at all on a non-positive horizon
//
// A retention window of zero would drop every partition, and a function that
// guessed a window would be a function that deletes data nobody agreed to
// delete. The horizon is an argument and a non-positive one is a refusal.
//
// The test uses the partitions the MIGRATION created rather than making its
// own, because the migration's are the ones a real install has, and their
// boundaries are the ones the sweep has to read.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// TestThePartitionSweepDropsOnlyWhatTheHorizonCovers is the sweep.
//
// The oldest partition is dropped by a horizon that covers it, and the newest
// one survives the same sweep.
func TestThePartitionSweepDropsOnlyWhatTheHorizonCovers(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	oldest, newest := partitionSpan(t, pool)
	if oldest == "" || newest == "" {
		t.Skip("the migration created no partitions to sweep")
	}

	// A horizon that covers the oldest partition's whole period and not the
	// newest one's.
	horizon := 90 * 24 * time.Hour
	dropped, err := runner.DropExpiredPartitions(ctx, horizon)
	if err != nil {
		t.Fatalf("DropExpiredPartitions: %v", err)
	}
	if dropped < 1 {
		t.Fatalf("the sweep dropped %d partitions, want at least the oldest one (%s). The "+
			"handover named the gap: the partitions were created and nothing ever dropped one",
			dropped, oldest)
	}

	if tableExists(t, pool, oldest) {
		t.Errorf("the partition %s survived a sweep with a 90-day horizon", oldest)
	}
	if !tableExists(t, pool, newest) {
		t.Errorf("the partition %s was dropped by a sweep with a 90-day horizon. A partition "+
			"that straddles the horizon still holds data the install is keeping", newest)
	}

	// Put it back.
	//
	// The test database is shared with the other tests in this package, and a
	// test that leaves it smaller than it found it is a test that decides
	// whether the next one passes. The sweep is destructive by design; the
	// TEST is not allowed to be.
	restorePartition(t, pool, oldest)
}

// restorePartition recreates a dropped partition.
func restorePartition(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	at, err := time.Parse("2006_01", name[len(name)-7:])
	if err != nil {
		t.Fatalf("parse the partition name %q: %v", name, err)
	}
	from := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	if _, err := pool.Exec(context.Background(), fmt.Sprintf(
		"CREATE TABLE %s PARTITION OF canonical_events FOR VALUES FROM ('%s') TO ('%s')",
		name, from.Format("2006-01-02"), to.Format("2006-01-02"))); err != nil {
		t.Fatalf("restore the partition %s: %v", name, err)
	}
}

// TestThePartitionSweepRefusesANonPositiveHorizon is the refusal.
//
// A zero horizon would drop every partition, and that is a decision nobody
// made.
func TestThePartitionSweepRefusesANonPositiveHorizon(t *testing.T) {
	pool := testutil.Pool(t)
	runner := &migrate.Runner{Pool: pool}
	for _, horizon := range []time.Duration{0, -time.Hour} {
		if _, err := runner.DropExpiredPartitions(context.Background(), horizon); err == nil {
			t.Errorf("the sweep accepted a horizon of %s. A non-positive horizon would drop every "+
				"partition", horizon)
		}
	}
}

// partitionSpan is the oldest and the newest partition of `canonical_events`.
func partitionSpan(t *testing.T, pool *pgxpool.Pool) (oldest, newest string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public'
		   AND c.relkind = 'r'
		   AND c.relname LIKE 'canonical_events_%'
		   AND c.relname NOT LIKE '%_default'
		 ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("list the partitions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if oldest == "" {
			oldest = name
		}
		newest = name
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read: %v", err)
	}
	return oldest, newest
}

// tableExists reports whether a relation is there.
func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		   WHERE n.nspname = 'public' AND c.relname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check whether %s exists: %v", name, err)
	}
	return exists
}
