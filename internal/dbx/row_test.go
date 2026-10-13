package dbx_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// A QueryRow whose Scan is never reached used to hold its pooled connection for
// the life of the process. The row type had no Close, no finaliser and no
// timeout, and Scan was the only release path, so a caller that took a row and
// then returned early — on a validation branch, a switch case that did not
// match, any path that decided not to read the row — never gave the connection's
// semaphore slot back.
//
// Nothing reports that. pgxpool has no detector for a connection that was
// acquired and never released, and the caller that leaked it has already moved
// on. The consequence appears later and elsewhere: after MaxConns such events,
// every Acquire in the process blocks until its context is cancelled. That is a
// site-wide hang, not an error, and it looks like a slow database.
func TestAnUnscannedRowReleasesItsConnectionOnClose(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	row := pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`)
	// The row is deliberately never scanned.

	// Held while open. Without this the assertion below would also pass if
	// QueryRow took no connection at all, and the release would prove nothing.
	if got := pool.Stat().AcquiredConns(); got != 1 {
		t.Fatalf("an open unscanned row holds %d connection(s), want 1. The row takes its "+
			"connection at QueryRow, which is why releasing it has to be explicit", got)
	}

	row.Close()
	waitForNoAcquired(t, pool)
}

// The failure the leak produces, at a size small enough to reach in two rows.
// This is why the release is explicit rather than incidental: with MaxConns of 2,
// two unclosed rows are enough to stop the next statement in the process.
func TestUnclosedRowsStopTheNextStatementFromAcquiring(t *testing.T) {
	pool := openSmallPool(t, 2)
	ctx := context.Background()

	rows := []*dbx.Row{
		pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`),
		pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`),
	}
	if got := pool.Stat().AcquiredConns(); got != 2 {
		t.Fatalf("two unscanned rows hold %d connection(s), want 2", got)
	}

	// A context that gives up rather than one that blocks for ever: an acquire
	// with no deadline is exactly the failure mode being described, so a
	// regression here has to be a failure and not a hung test.
	blocked, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var n int
	if err := pool.For("tenant-a", "test").QueryRow(blocked, `SELECT 1`).Scan(&n); err == nil {
		t.Fatal("a third statement acquired a connection from a pool whose two are both held " +
			"by unscanned rows; the exhaustion is not reproducing")
	}

	for _, r := range rows {
		r.Close()
	}
	if err := pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatalf("the pool is still exhausted after both rows were closed: %v", err)
	}
}

// Closing twice must not release the same connection twice, which pgxpool treats
// as a bug rather than as a no-op: a double release hands the same backend to two
// callers, and both of their statements then run on one connection.
func TestClosingARowTwiceReleasesItOnce(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	row := pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`)
	row.Close()
	row.Close()

	waitForNoAcquired(t, pool)

	// A double release would show here as the pool not reusing the backend it
	// just got back.
	first := backendPID(t, pool)
	second := backendPID(t, pool)
	if first != second {
		t.Errorf("two readers got different backends (%d then %d) after a double close", first, second)
	}
}

// Scan is still a release path. Every existing call site reads the row in one
// expression and never calls Close, and the fix must not have turned that from
// safe into a leak — that would trade a rare leak for a certain one.
func TestScanningARowReleasesTheConnectionWithoutAClose(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	var n int
	if err := pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 1 {
		t.Errorf("SELECT 1 scanned as %d", n)
	}

	waitForNoAcquired(t, pool)
}

// ErrNoRows is the ordinary "nothing matched" answer, and it must release the
// connection too. A query that finds nothing is a result, not a reason to strand
// a connection.
func TestAnEmptyResultStillReleasesTheConnection(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	var id string
	err := pool.For("tenant-a", "test").QueryRow(ctx,
		`SELECT tenant_id FROM tenants WHERE tenant_id = 'no-such-tenant'`).Scan(&id)
	if err == nil {
		t.Fatal("a query for a tenant that does not exist returned a row")
	}

	waitForNoAcquired(t, pool)
}

// A connection released from a closed, unread row must not carry its tenant back
// to the pool. This is the reason the release path clears the session settings
// rather than merely calling Release, and closing without reading is a path that
// previously did not exist.
func TestAClosedUnreadRowDoesNotLeakItsTenantOntoTheConnection(t *testing.T) {
	pool := openPool(t)
	ctx := context.Background()

	pool.For("tenant-a", "test").QueryRow(ctx, `SELECT 1`).Close()
	waitForNoAcquired(t, pool)

	var leaked string
	if err := pool.For("tenant-b", "test").QueryRow(ctx,
		`SELECT current_setting('app.tenant_id', true)`).Scan(&leaked); err != nil {
		t.Fatalf("read the leftover tenant: %v", err)
	}
	if leaked != "tenant-b" {
		t.Errorf("the connection carried app.tenant_id=%q into the next reader, which asked for "+
			"tenant-b", leaked)
	}
}

// openSmallPool builds a product pool with a connection ceiling small enough to
// exhaust in a test. It is the same Pool the product uses; only the size differs.
func openSmallPool(t *testing.T, maxConns int32) *dbx.Pool {
	t.Helper()
	testutil.Pool(t)
	cfg := dbx.DefaultConfig()
	cfg.MaxOpenConns = int(maxConns)
	cfg.MaxIdleConns = int(maxConns)
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

// waitForNoAcquired blocks until the pool holds no acquired connections.
//
// pgxpool returns a backend on its own goroutine, so the count is not zero the
// instant Close returns. A timeout is a test failure rather than a value every
// caller has to remember to check.
func waitForNoAcquired(t *testing.T, pool *dbx.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := pool.Stat().AcquiredConns()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d connection(s) still acquired 5s after the row was released; a row that "+
				"is never scanned must not hold one for the life of the process", n)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
