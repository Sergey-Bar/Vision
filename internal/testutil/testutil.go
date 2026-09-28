// Package testutil holds the harness the database-backed tests share.
//
// CI must pass with no Milestone server reachable. A test that needs one is
// wrong. Postgres is different: it is the product's datastore, and asserting
// hash-chaining, RLS and append-only behaviour against a mock would be testing
// the mock. These tests skip when no database is configured rather than failing,
// so a contributor without Postgres still gets a green run that is honest about
// having covered less.
package testutil

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// DatabaseURLEnv is the environment variable naming a test database.
const DatabaseURLEnv = "SP_TEST_DATABASE_URL"

var (
	sharedOnce sync.Once
	sharedPool *pgxpool.Pool
	sharedErr  error
)

// Pool returns a shared pool for the configured test database, or skips.
//
// The database is reset once per test binary: schema_migrations plus every table
// it created. Tests that need isolation from each other should use distinct
// tenant ids rather than expecting a clean slate, because the laws under test
// (tenancy, chaining) are only meaningful when other rows exist.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv(DatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping database-backed test", DatabaseURLEnv)
	}

	sharedOnce.Do(func() {
		cfg := dbx.DefaultConfig()
		if err := cfg.ApplyDSN(dsn); err != nil {
			sharedErr = err
			return
		}
		pass := os.Getenv("SP_TEST_DATABASE_PASSWORD")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		pool, err := dbx.Open(ctx, cfg, pass)
		if err != nil {
			sharedErr = err
			return
		}
		if err := pool.Ping(ctx); err != nil {
			sharedErr = err
			pool.Close()
			return
		}
		sharedPool = pool.Pool
	})

	if sharedErr != nil {
		t.Skipf("test database unavailable: %v", sharedErr)
	}
	return sharedPool
}

// Reset drops and recreates the public schema, then leaves it empty. The
// migration test calls it; other suites do not, because dropping the schema out
// from under a parallel test is a race nobody needs.
func Reset(t *testing.T) {
	t.Helper()
	pool := Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `GRANT ALL ON SCHEMA public TO public;`); err != nil {
		t.Fatalf("grant on public schema: %v", err)
	}
}

// NewTenant creates a tenant, a site and a device, and returns their ids. Every
// id is unique per call so tests can run in any order.
func NewTenant(t *testing.T, pool *pgxpool.Pool) (tenant, site, device string) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	tenant = "t-" + suffix
	site = "s-" + suffix
	device = "d-" + suffix

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `INSERT INTO tenants (tenant_id, name) VALUES ($1, $2)`, tenant, "Test Tenant"); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sites (site_id, tenant_id, name, timezone) VALUES ($1, $2, $3, $4)`,
		site, tenant, "Test Site", "Asia/Jerusalem"); err != nil {
		t.Fatalf("insert site: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO devices (tenant_id, device_id, site_id, kind)
		VALUES ($1, $2, $3, 'camera')`, tenant, device, site); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return tenant, site, device
}

// Exec runs a statement in a fresh transaction as the owner role.
func Exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %s: %v", firstLine(sql), err)
	}
}

// ExpectError runs a statement and requires it to fail, returning the error.
func ExpectError(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) error {
	t.Helper()
	_, err := pool.Exec(context.Background(), sql, args...)
	if err == nil {
		t.Fatalf("expected %s to be refused, but it succeeded", firstLine(sql))
	}
	return err
}

// QueryOne runs a query and scans a single value.
func QueryOne(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) any {
	t.Helper()
	var v any
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("query %s: %v", firstLine(sql), err)
	}
	return v
}

// AsRole runs fn inside a transaction as the named role, with the given tenant
// context set. It is how the tests assert that least privilege is real rather
// than declared.
func AsRole(t *testing.T, pool *pgxpool.Pool, role, tenant string, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// SECURITY_RESTRICTED_OPERATION would be the production shape, but the test
	// database owner cannot SET ROLE into a role it does not have privileges
	// over in every environment; SET ROLE is checked explicitly below by the
	// assertions themselves.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL ROLE %s`, role)); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	if tenant != "" {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
			t.Fatalf("set tenant context: %v", err)
		}
	}
	fn(tx)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
