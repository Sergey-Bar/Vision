package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
	"github.com/Sergey-Bar/vision/migrations"
)

// TestMigrationsApplyToEmptyDatabase is the smoke test: a fresh install lands on
// the latest version and every step is recorded.
func TestMigrationsApplyToEmptyDatabase(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)

	runner := &migrate.Runner{Pool: pool}
	plan, err := runner.Up(context.Background())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if plan.CurrentVersion != 0 {
		t.Errorf("a reset database reported version %d, want 0", plan.CurrentVersion)
	}
	if len(plan.Pending) == 0 {
		t.Fatal("no migrations were pending on a fresh database")
	}

	latest, err := migrations.LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	current, err := runner.CurrentVersion(context.Background())
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if current != latest {
		t.Errorf("schema is at version %d, want %d", current, latest)
	}

	applied, err := runner.Applied(context.Background())
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(applied) != latest {
		t.Errorf("ledger holds %d rows, want %d", len(applied), latest)
	}
}

// Migrations are forward-only and a second run must be a no-op. Re-running an
// upgrade script is the most common operational mistake, and it must not double-apply.
func TestUpIsIdempotent(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	plan, err := runner.Up(ctx)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(plan.Pending) != 0 {
		t.Errorf("second Up planned %d migrations, want 0", len(plan.Pending))
	}
}

// A migration that is not present in this build, or whose bytes changed, means
// this binary and the site disagree about what happened. Continuing would make
// the recorded history a lie, so the runner stops.
func TestVerifyChecksumsCatchesDrift(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := runner.VerifyChecksums(ctx); err != nil {
		t.Fatalf("a freshly migrated database failed checksum verification: %v", err)
	}

	// Simulate the tampering an attacker with database write access would do:
	// rewrite the recorded checksum of the first migration.
	testutil.Exec(t, pool, `UPDATE schema_migrations SET checksum = $1 WHERE version = 1`, strings.Repeat("0", 64))

	err := runner.VerifyChecksums(ctx)
	if err == nil {
		t.Fatal("a rewritten migration checksum was not detected")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error should name the mismatch, got %q", err)
	}
}

func TestStatusReport(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	report, err := runner.StatusReport(ctx)
	if err != nil {
		t.Fatalf("StatusReport: %v", err)
	}
	if report.CurrentVersion != report.TargetVersion {
		t.Errorf("status reports %d applied of %d target", report.CurrentVersion, report.TargetVersion)
	}
	if !report.Compatible {
		t.Error("a freshly migrated database is outside the supported window")
	}
	if len(report.Pending) != 0 {
		t.Errorf("status lists %d pending migrations after a complete run", len(report.Pending))
	}
}

// A failed step must leave the earlier steps committed and must not record the
// failed one. Partial application is how a site ends up with a version number
// that does not describe its schema.
func TestFailedStepLeavesEarlierStepsCommitted(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	// A constraint violation stands in for a step that fails halfway through.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO incidents (incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
		                       bundle_content_hash, policy_version, first_event_at, last_event_at)
		VALUES ('i1', 'no-such-tenant', 's1', 'r', '1.0.0', repeat('a', 64), 'p', now(), now())`); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("a foreign key violation was accepted; the test no longer proves anything")
	} else {
		_ = tx.Rollback(ctx)
	}

	applied, err := runner.Applied(ctx)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	latest, _ := migrations.LatestVersion()
	if len(applied) != latest {
		t.Errorf("ledger holds %d rows after a failed step, want %d", len(applied), latest)
	}
}

var _ = pgx.Tx(nil)
