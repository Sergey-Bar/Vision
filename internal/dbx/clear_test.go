package dbx

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests live in package dbx rather than dbx_test because they exercise an
// unexported release decision. They therefore cannot use internal/testutil, which
// imports this package; the connection string is read from the same environment
// variable by name.
const testDatabaseURLEnv = "SP_TEST_DATABASE_URL"

// The tenant context is session-level (set_config(..., false)), so the clear that
// runs before a connection goes back to the pool is the only thing standing
// between one request's tenant and the next request's statement. A connection
// handed back with app.tenant_id still set runs the next caller's statement as
// the previous tenant, and every one of those statements succeeds.
//
// The failure is injected directly rather than through a faulted database,
// because the failure that matters is a statement-level one — a timeout, a
// cancel, a serialisation failure — where the connection is still perfectly
// usable and the error is the easiest thing in the function to discard. Losing
// the backend does not reach that branch: pgx closes a connection whose socket
// died, with or without our help.
func TestAFailedTenantClearDestroysTheConnection(t *testing.T) {
	pool := openRawPool(t)
	ctx := context.Background()

	conn, dirty := acquireDirty(t, pool)
	releaseConn(ctx, conn, errors.New("clear refused: statement cancelled"))
	waitForNoAcquiredConns(t, pool)

	next, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire after the failed clear: %v", err)
	}
	defer next.Release()
	if got := pidOf(t, ctx, next); got == dirty {
		t.Errorf("backend %d, whose tenant clear failed, was handed to the next caller. It may "+
			"still be carrying app.tenant_id, so that caller's statement runs as the previous "+
			"tenant and reports success while doing so", dirty)
	}
}

// The other half, and the reason the previous behaviour was not merely wasteful:
// a clean clear must return the connection, not replace it. A pool that opened a
// fresh connection on every release exhausts itself under load.
func TestACleanTenantClearKeepsTheConnection(t *testing.T) {
	pool := openRawPool(t)
	ctx := context.Background()

	conn, dirty := acquireDirty(t, pool)
	releaseConn(ctx, conn, nil)
	waitForNoAcquiredConns(t, pool)

	next, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire after the clean clear: %v", err)
	}
	defer next.Release()
	if got := pidOf(t, ctx, next); got != dirty {
		t.Errorf("a cleanly cleared connection (backend %d) was replaced by %d. The pool opens "+
			"a fresh connection per statement, and a site under load runs out of connections",
			dirty, got)
	}
}

// clearTenantOnConn is the caller, and it has to pass its error down. The
// previous version discarded it here, which is why a failed clear was
// indistinguishable from a successful one.
func TestClearTenantOnConnWipesTheTenantOnASuccessfulRelease(t *testing.T) {
	pool := openRawPool(t)
	ctx := context.Background()

	conn, _ := acquireDirty(t, pool)
	clearTenantOnConn(ctx, conn)
	waitForNoAcquiredConns(t, pool)

	next, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer next.Release()

	var leaked string
	if err := next.QueryRow(ctx, `SELECT current_setting('app.tenant_id', true)`).Scan(&leaked); err != nil {
		t.Fatalf("read the leftover tenant: %v", err)
	}
	if leaked != "" {
		t.Errorf("the connection went back to the pool still carrying app.tenant_id=%q. Every "+
			"later tenant on that connection would read this tenant's rows", leaked)
	}
}

func acquireDirty(t *testing.T, pool *pgxpool.Pool) (*pgxpool.Conn, int) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('app.tenant_id', 'tenant-a', false)`); err != nil {
		t.Fatalf("set the tenant: %v", err)
	}
	return conn, pidOf(t, ctx, conn)
}

func pidOf(t *testing.T, ctx context.Context, conn *pgxpool.Conn) int {
	t.Helper()
	var pid int
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	return pid
}

func openRawPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(testDatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping database-backed test", testDatabaseURLEnv)
	}
	// pgx has no separate password argument, so it goes in the URL when the URL
	// does not already carry one.
	if pass := os.Getenv("SP_TEST_DATABASE_PASSWORD"); pass != "" {
		if u, err := url.Parse(dsn); err == nil {
			if pw, _ := u.User.Password(); pw == "" {
				name := ""
				if u.User != nil {
					name = u.User.Username()
				}
				u.User = url.UserPassword(name, pass)
				dsn = u.String()
			}
		}
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func waitForNoAcquiredConns(t *testing.T, pool *pgxpool.Pool) int32 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := pool.Stat().AcquiredConns()
		if n == 0 || time.Now().After(deadline) {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
}
