package dbx_test

import (
	"context"
	"os"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// openPool builds a dbx pool against the test database.
//
// This is the product's own Pool rather than the raw pgxpool the rest of the
// suite shares, because these tests are about what dbx does with a connection
// once it has one.
func openPool(t *testing.T) *dbx.Pool {
	t.Helper()
	// The suite skips rather than fails when there is no database, and it does
	// that by going through testutil.Pool first. Without that call an absent
	// database surfaced as a confusing failure out of ApplyDSN rather than as a
	// skip, which is the difference between "this machine has no Postgres" and
	// "this code is broken" in a log somebody reads at 2am.
	testutil.Pool(t)

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

// An empty tenant is refused rather than defaulting. "No tenant" is not a state
// the policies know: app_current_tenant() returns NULL, a SELECT returns nothing
// and an INSERT is refused, so a default would read as an answer.
func TestForRefusesAnEmptyTenant(t *testing.T) {
	pool := openPool(t)
	if _, err := pool.For("", "actor").Exec(context.Background(), `SELECT 1`); err == nil {
		t.Fatal("For(\"\") was accepted. Every statement it ran would have seen no rows, " +
			"which reads as an empty table rather than as a refusal")
	}
	if err := pool.For("", "actor").QueryRow(context.Background(), `SELECT 1`).Scan(new(int)); err == nil {
		t.Fatal("For(\"\").QueryRow scanned successfully")
	}
	if _, err := pool.For("", "actor").Query(context.Background(), `SELECT 1`); err == nil {
		t.Fatal("For(\"\").Query succeeded")
	}
}

// A cleared connection is reused, not replaced. Every release re-opening a
// connection would exhaust a pool of 32 under the load the product is specified
// for, and the connection-reuse release decision itself is asserted in
// clear_test.go, which can reach the unexported path.
func TestAClearedConnectionIsReused(t *testing.T) {
	pool := openPool(t)

	first := backendPID(t, pool)
	for i := 0; i < 3; i++ {
		if got := backendPID(t, pool); got != first {
			t.Fatalf("iteration %d got backend %d, want the pooled %d", i, got, first)
		}
	}
}

// backendPID reads the backend of a connection the pool hands to a tenant-scoped
// query. The tenant is fixed: what is under test is that the connection is reused
// after a clear, and varying the tenant would only test that two different
// settings land on one connection, which is a different claim.
func backendPID(t *testing.T, pool *dbx.Pool) int {
	t.Helper()
	var pid int
	if err := pool.For("tenant-a", "test").QueryRow(context.Background(),
		`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	return pid
}
