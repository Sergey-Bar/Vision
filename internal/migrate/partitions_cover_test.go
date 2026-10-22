package migrate_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// EnsurePartitions was added with migration 0025 and grew no test, so the runner
// that keeps the time-partitioned tables writable in future years reached 0%
// coverage — and the reason nobody noticed is that it could not create a
// partition at all. PostgreSQL does not accept bind parameters inside DDL, so
// `FOR VALUES FROM ($1) TO ($2)` failed on every call with `mismatched param and
// argument count`. The fix is in migrate.go; these tests are what keep it fixed.

// TestEnsurePartitionsCreatesOnATableWithNoPartitions is the direct assertion
// that the DDL executes: give it a partitioned table with no future partitions,
// and it must create them.
//
// It drops the future partitions first. Without that, a database that any earlier
// run has already populated through the ten-year ceiling reports zero created for
// a perfectly working runner — and that is precisely how the DDL bug survived:
// `FOR VALUES FROM ($1) TO ($2)` cannot work, because PostgreSQL rejects bind
// parameters inside DDL, so every call failed with `mismatched param and argument
// count` and every call looked like "nothing to do".
func TestEnsurePartitionsCreatesOnATableWithNoPartitions(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Drop every existing partition so the runner starts from nothing. The
	// parent tables survive, and so does the migration ledger.
	//
	// The two EVENT tables are dropped and `recording_segments` is not, and
	// that is deliberate rather than an oversight. The test's claim is that
	// the DDL executes, and two tables prove it; dropping the recording
	// index's sixty partitions as well costs the cluster a minute of DDL
	// churn while every other package is trying to drop its own database,
	// and a harness that spends its whole budget on one package's setup is a
	// harness that fails at random.
	//
	// The recording index's partitions are covered by the two tests below,
	// which assert that they exist and that they carry their policies.
	for _, table := range []string{"inbox_events", "canonical_events"} {
		testutil.Exec(t, pool, fmt.Sprintf(`
			DO $$
			DECLARE r record;
			BEGIN
			  FOR r IN
			    SELECT c.relname FROM pg_class c
			    JOIN pg_namespace n ON n.oid = c.relnamespace
			    WHERE n.nspname = 'public' AND c.relname LIKE '%s'
			      AND c.relkind = 'r' AND c.relispartition
			  LOOP
			    EXECUTE format('DROP TABLE %%I', r.relname);
			  END LOOP;
			END $$`, table+"_%"))
	}

	var before int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname LIKE 'inbox_events_%'
		  AND c.relkind = 'r' AND c.relispartition`).Scan(&before); err != nil {
		t.Fatalf("count partitions before: %v", err)
	}
	if before != 0 {
		t.Fatalf("could not clear partitions to set up the test; %d remain", before)
	}

	created, err := runner.EnsurePartitions(ctx, 1)
	if err != nil {
		t.Fatalf("EnsurePartitions(ctx, 1): %v", err)
	}
	if created == 0 {
		t.Fatal("EnsurePartitions created no partitions on a partitioned table that had none. " +
			"PostgreSQL rejects bind parameters inside CREATE TABLE, so zero created is the " +
			"signature of that bug.")
	}

	for _, table := range []string{"inbox_events", "canonical_events", "recording_segments"} {
		var after int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relname LIKE $1
			  AND c.relkind = 'r' AND c.relispartition`, table+"_%").Scan(&after); err != nil {
			t.Fatalf("count partitions of %s after: %v", table, err)
		}
		if after < 12 {
			t.Errorf("%s has %d partitions, want at least 12 for one year ahead", table, after)
		}
	}
}

// TestEnsurePartitionsCoversTheDeclaredTables is the failure this runner exists
// to prevent: a time-partitioned table that stops having future partitions stops
// accepting rows, and the table is the ingest path.
func TestEnsurePartitionsCoversTheDeclaredTables(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if _, err := runner.EnsurePartitions(ctx, 3); err != nil {
		t.Fatalf("EnsurePartitions(ctx, 3): %v", err)
	}

	for _, table := range []string{"inbox_events", "canonical_events", "recording_segments"} {
		var partitions int
		err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public'
			  AND c.relname LIKE $1
			  AND c.relkind = 'r'
			  AND c.relispartition`, table+"_%").Scan(&partitions)
		if err != nil {
			t.Fatalf("count partitions of %s: %v", table, err)
		}
		if partitions == 0 {
			t.Errorf("%s has no partitions", table)
		}

		// Every partition must carry a policy. A partition that bypasses RLS is
		// a hole in the isolation the schema guarantees.
		//
		// `relkind = 'r'` matters: an index on a partition also reports
		// `relispartition`, and a query without it counts indexes as unpoliced
		// tables — a false alarm that would look exactly like this bug.
		var unpoliced int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public'
			  AND c.relname LIKE $1
			  AND c.relkind = 'r'
			  AND c.relispartition
			  AND NOT EXISTS (
			    SELECT 1 FROM pg_policy p WHERE p.polrelid = c.oid
			  )`, table+"_%").Scan(&unpoliced); err != nil {
			t.Fatalf("count unpoliced partitions of %s: %v", table, err)
		}
		if unpoliced != 0 {
			t.Errorf("%s has %d partitions with no RLS policy, want 0", table, unpoliced)
		}
	}
}

// TestEnsurePartitionsIsIdempotent is the half that matters in operation: this
// runs on a schedule, so a second call must not fail and must not re-create what
// already exists.
func TestEnsurePartitionsIsIdempotent(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if _, err := runner.EnsurePartitions(ctx, 2); err != nil {
		t.Fatalf("first EnsurePartitions: %v", err)
	}
	created, err := runner.EnsurePartitions(ctx, 2)
	if err != nil {
		t.Fatalf("second EnsurePartitions: %v", err)
	}
	if created != 0 {
		t.Errorf("second call created %d partitions, want 0 — partitions are not idempotent", created)
	}
}

// TestEnsurePartitionsClampsTheYearsAheadArgument covers the argument handling
// around the loop: 0 means the default of three years, and an absurd value is
// clamped to the ten-year ceiling rather than honoured or refused.
//
// It does not assert how many were created. On a database where earlier runs
// already built partitions through the ceiling, the honest answer is zero — and a
// test that demanded otherwise would fail on a perfectly good runner.
func TestEnsurePartitionsClampsTheYearsAheadArgument(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	for _, arg := range []int{0, 3, 200} {
		if _, err := runner.EnsurePartitions(ctx, arg); err != nil {
			t.Errorf("EnsurePartitions(ctx, %d): %v", arg, err)
		}
	}

	// Whatever the argument, the window it promised must be covered: a table
	// with no future partition is a table that starts refusing writes.
	for _, table := range []string{"inbox_events", "canonical_events", "recording_segments"} {
		var partitions int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'public' AND c.relname LIKE $1 AND c.relispartition`,
			table+"_%").Scan(&partitions); err != nil {
			t.Fatalf("count partitions of %s: %v", table, err)
		}
		if partitions == 0 {
			t.Errorf("%s has no partitions after EnsurePartitions", table)
		}
	}
}
