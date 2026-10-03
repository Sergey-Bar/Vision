// Package testutil holds the harness the database-backed tests share.
//
// CI must pass with no Milestone server reachable. A test that needs one is
// wrong. Postgres is different: it is the product's datastore, and asserting
// hash-chaining, RLS and append-only behaviour against a mock would be testing
// the mock. These tests skip when no database is configured rather than failing,
// so a contributor without Postgres still gets a green run that is honest about
// having covered less — and `internal/skipaudit` fails that run, because a green
// line per package is exactly what a skipped suite looks like from the console.
//
// # One database per package
//
// Every package's tests get their own database, named after the package's
// directory, created empty on first use. That is what lets `go test -p 8` replace
// `go test -p 1`.
//
// `-p 1` was load-bearing because `Reset` dropped and recreated `public`, and
// `public` is shared: one package's `DROP SCHEMA ... CASCADE` landed inside
// another's test, and the failures looked like product bugs rather than like a
// harness that cannot run two suites at once. AGENTS.md records the same thing as
// a rule rather than as a cause.
//
// # Why a database and not a schema
//
// A schema per package was tried first and does not work, and the reason is worth
// keeping. Migration 0008 hardcodes `n.nspname = 'public'` twice — once to find
// the tables to put a policy on, and once in a `format('%I.%I', 'public',
// ...)::regclass` that reads the column list. No migration qualifies a table name
// anywhere else, so 0008 was the only place `public` was written down, and it is
// the reason every other migration can be schema-agnostic.
//
// Against a per-package schema, 0008's loop then finds *no tables at all* and
// applies no policy to anything. It appeared to work only because the shared
// `public` still held the previous run's tables, and the run failed at the first
// partition 0015 had created: `apply 0008: relation "inbox_events_2027_01" does
// not exist`. A migration that silently applied no row-level security, on a
// database whose only tenant isolation came from those policies, is a far worse
// outcome than a slow test run — and it was found by making the schemas honest,
// not by reading the migration.
//
// Making the harness use `current_schema()` instead would mean a forward-only
// migration 0022 that drops and recreates every policy, and until 0022 is
// applied 0008 still applies none in a fresh schema. A separate database needs no
// migration at all, because each database has its own `public`.
//
// The name is derived from the caller's own file, so a package cannot end up in
// another's database by forgetting to configure anything. It is stable rather
// than random, so a database is reused between runs and the count is bounded by
// the number of database-backed packages rather than by the number of times anyone
// has run the suite.
package testutil

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// DatabaseURLEnv is the environment variable naming a test database.
const DatabaseURLEnv = "SP_TEST_DATABASE_URL"

// dbPrefix marks a database as belonging to the test harness, so a stale one is
// recognisable rather than looking like somebody's data.
const dbPrefix = "sp_test_"

// maintenanceDatabase is where the harness connects to create and drop the
// per-package databases. Postgres has no `CREATE DATABASE` from inside a
// transaction and none from inside another database, so there is one place to go.
const maintenanceDatabase = "postgres"

// harnessMaxOpenConns is small on purpose. A test does not need a hundred
// connections, and a suite that does is a suite that is about connection limits
// rather than about the product.
const (
	harnessMaxOpenConns = 6
	harnessMaxIdleConns = 3
)

// productRoles are the roles migration 0008 creates, with the attributes it gives
// them. The harness creates them before any package migrates; see ensureRoles.
var productRoles = []string{
	"security_migrate", "security_core", "security_adapter", "security_ai", "security_readonly",
}

// roleLockKey is the advisory lock the harness holds on the maintenance database
// while it creates the product roles.
//
// The two-argument form, on purpose. A one-argument `pg_advisory_lock(bigint)` is
// scoped to the *current database*, which is why the migrator's own lock stopped
// serialising anything the moment each package got a database of its own — and
// that turned out to matter, because a PostgreSQL role is cluster-wide. Eight
// packages racing `CREATE ROLE security_core` in eight separate databases produce
// `tuple concurrently updated` out of `pg_authid`: a catalog the databases do not
// share and the per-database migration lock therefore no longer covers.
//
// Holding it on the maintenance database makes the lock as wide as the thing it
// guards, because every binary contends for that one database.
const (
	roleLockClass int32 = 0x1F0CA11
	roleLockObj   int32 = 0x5EC0DE5
)

var (
	// poolMu guards the pool's lifecycle rather than just its creation, because
	// Reset closes it and reopens it while other tests hold a reference to the
	// previous one. Tests in a package run sequentially unless they ask for
	// `t.Parallel`, and none of the callers do, so this is a mutex against a
	// future caller rather than against today's.
	poolMu sync.Mutex
	// poolFor is the per-package pool, keyed by database name because one binary
	// has one package and one package has one database.
	poolFor = map[string]*pgxpool.Pool{}
	// sharedErr is a connection failure rather than a harness failure: a
	// contributor without Postgres gets a skip, a broken harness gets a failure,
	// and the two are not the same claim.
	sharedErr error
	// harnessErr is the harness being wrong, which must fail the run rather than
	// shrink it. See FailHarness.
	harnessErr error
)

// Database returns the name of the database this test binary owns.
//
// Exported because a package's own test asserts on it — the isolation is worth a
// test of its own rather than being a property of a helper.
func Database(t *testing.T) string {
	t.Helper()
	name, err := databaseName()
	if err != nil {
		t.Fatalf("derive the test database name: %v", err)
	}
	return name
}

// databaseName derives a database name from the caller's own file: the directory
// holding it, relative to the module root, flattened into an identifier.
//
// Derived rather than configured because a configurable name is a name two
// packages can be given the same value for by accident, and the failure of that
// is the exact `-p 1` race this replaces: two packages dropping one another's
// database mid-test.
//
// # Why it walks the stack rather than counting frames
//
// The first version used `runtime.Caller(2)`, which is correct only while every
// path into this file reaches `databaseName` from the same depth. `Reset` calls
// `Pool`, so a `Reset` from `internal/migrate` landed two frames short of the test
// and resolved to `internal/testutil` — and `Reset` then dropped and recreated
// *this* package's database instead of the caller's, leaving the caller's schema
// untouched. It read as "Reset does nothing", it was invisible in every test that
// called `Pool` first, and it meant `internal/testutil`'s own database was
// recreated by eight unrelated packages on every run.
//
// So: the name comes from the outermost frame that is not this file. That is the
// actual intent — "the package whose test called into the harness" — and it cannot
// be broken by adding a helper here, which is exactly how the depth arithmetic
// broke.
func databaseName() (string, error) {
	file, err := firstCallerOutsideThisFile()
	if err != nil {
		return "", err
	}
	root, err := moduleRoot(filepath.Dir(file))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, filepath.Dir(file))
	if err != nil {
		return "", fmt.Errorf("the calling file %s is not under the module root %s", file, root)
	}
	name := dbPrefix + strings.NewReplacer(string(filepath.Separator), "_", "-", "_").Replace(rel)
	// Postgres truncates an identifier at 63 bytes and a truncated name is a
	// *different* name, which is the worst kind of collision: two packages with
	// long paths landing in one database silently. Refused rather than truncated.
	if len(name) > 63 {
		return "", fmt.Errorf(
			"the database name %q for package directory %q is %d bytes, over Postgres's 63-byte "+
				"identifier limit. It is refused rather than truncated, because a truncated name "+
				"would collide with another package's", name, rel, len(name))
	}
	return name, nil
}

// firstCallerOutsideThisFile is the first stack frame in a file other than this
// one, innermost first.
//
// Compared by base name rather than by full path because the harness is compiled
// into every test binary in the tree, so the full path is wherever the compiler
// put it and cannot be predicted here. A second copy of `testutil.go` in another
// directory would defeat that, and the module has one.
func firstCallerOutsideThisFile() (string, error) {
	const depth = 32
	var pcs [depth]uintptr

	// Skip 2: runtime.Callers and this function.
	n := runtime.Callers(2, pcs[:])
	if n == 0 {
		return "", fmt.Errorf("no stack frames, so the calling package is unknown")
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if filepath.Base(frame.File) != filepath.Base(thisFile) {
			return frame.File, nil
		}
		if !more {
			return "", fmt.Errorf("every frame in the stack is inside %s, so the calling package "+
				"is unknown. That means the harness was called from itself, which is not a thing "+
				"a test does", thisFile)
		}
	}
}

// thisFile is this source file's base name, resolved once at init from the
// compiler's own record of where it is.
var thisFile = func() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "testutil.go"
	}
	return filepath.Base(file)
}()

// moduleRoot walks up from dir to the nearest go.mod.
func moduleRoot(dir string) (string, error) {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod above %s, so the package path is unknown", dir)
		}
		d = parent
	}
}

// Pool returns a shared pool on this package's own database, creating it empty on
// first use, or skips.
//
// It also repoints `SP_TEST_DATABASE_URL` at that database, so a test which opens
// its own connection lands in the same place. Tests that need isolation from each
// other should use distinct tenant ids rather than expecting a clean slate,
// because the laws under test (tenancy, chaining) are only meaningful when other
// rows exist. Isolation *between packages* is what the per-package database buys,
// and it is unconditional.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv(DatabaseURLEnv)
	if dsn == "" {
		t.Skipf("%s is not set; skipping database-backed test", DatabaseURLEnv)
	}
	name, err := databaseName()
	if err != nil {
		t.Fatalf("derive the test database name: %v", err)
	}

	poolMu.Lock()
	defer poolMu.Unlock()

	if p, ok := poolFor[name]; ok && p != nil {
		return p
	}
	if harnessErr != nil {
		failHarness(t, "%v", harnessErr)
	}

	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(dsn); err != nil {
		recordUnavailable(err)
		t.Skipf("test database unavailable: %v", sharedErr)
	}
	pass := os.Getenv("SP_TEST_DATABASE_PASSWORD")
	// Capped, and capped low.
	//
	// Postgres defaults `max_connections` to 100 for the whole cluster, and every
	// database-backed package now has its own pool — plus a handful that open a
	// second one to be a different role. At `go test -p 8` that is comfortably over
	// a hundred, and the failure is `sorry, too many clients already` on whichever
	// connection happened to arrive last, which reads as a product bug rather than
	// as a test environment at its limit.
	//
	// Capping the harness pool is the half this file controls; the other half is
	// `max_connections` on the test instance, which AGENTS.md's `docker run` line
	// now sets. Neither is a product default: a site's pool sizing is its own
	// decision and `dbx.DefaultConfig` is left alone.
	cfg.MaxOpenConns = harnessMaxOpenConns
	cfg.MaxIdleConns = harnessMaxIdleConns

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := ensureDatabase(ctx, cfg, pass, name); err != nil {
		if isHarness(err) {
			failHarness(t, "%v", err)
		}
		recordUnavailable(err)
		t.Skipf("test database unavailable: %v", sharedErr)
	}

	// Point the environment variable at this package's database, before anything
	// else can read it.
	//
	// This is the step that makes the isolation total rather than partial. About a
	// dozen packages do not use `testutil.Pool`: they build their own `dbx.Open`
	// from the environment, because they are testing the connection path or need a
	// different role. Those tests wrote to the configured database and read from it
	// while `testutil.Pool` handed them a per-package one, and the symptom was a
	// tenant that `NewTenant` had just created and `bootstrap-admin` could not see.
	//
	// Rewriting the variable is blunt, and blunt is the right tool: a test that
	// opens its own connection should land in the same database as one that uses
	// the shared pool, and the only way to guarantee that without editing every
	// call site is to make the name itself say where this package is.
	dsnTarget, err := retargetDatabase(dsn, name)
	if err != nil {
		failHarness(t, "point %s at the package database: %v", DatabaseURLEnv, err)
	}
	if err := os.Setenv(DatabaseURLEnv, dsnTarget); err != nil {
		failHarness(t, "point %s at the package database: %v", DatabaseURLEnv, err)
	}

	target := cfg
	target.Database = name
	pool, err := dbx.Open(ctx, target, pass)
	if err != nil {
		recordUnavailable(err)
		t.Skipf("test database unavailable: %v", sharedErr)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		recordUnavailable(err)
		t.Skipf("test database unavailable: %v", sharedErr)
	}
	poolFor[name] = pool.Pool
	return pool.Pool
}

// retargetDatabase returns dsn with only its database name replaced.
//
// URL surgery on the original rather than re-rendering from `dbx.Config`, because
// the original is what the developer wrote and it may carry a user, a port and a
// query string the harness has no field for. Rebuilding it from the struct is how
// this lost `user=postgres` the first time, and every package that opens its own
// connection then authenticated as the developer's OS user.
func retargetDatabase(dsn, database string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", dsn, err)
	}
	if u.Scheme == "" {
		return "", fmt.Errorf("%q is not a URL, so its database name cannot be replaced", dsn)
	}
	u.Path = "/" + database
	return u.String(), nil
}

// recordUnavailable keeps the first cause. Twenty database-backed tests in a
// package each skip with the same underlying error, and the first one is the
// informative one; the rest would each print a different connection failure that
// happened to arrive last, which reads like twenty problems.
func recordUnavailable(err error) {
	if sharedErr == nil {
		sharedErr = err
	}
}

// ensureDatabase creates the package's database, dropping one left by an earlier
// run first so every run starts from nothing.
//
// Dropping before creating rather than reusing is deliberate. A reused database
// carries the previous run's tables, and migration 0008's RLS loop iterates over
// whatever is already there — so on a reused database it applies policies to
// *both* runs' tables, and on a clean one it applies them to none. The suite's
// green runs were partly resting on that inconsistency, and it only shows up the
// day a fresh database is the normal case rather than the exception.
func ensureDatabase(ctx context.Context, cfg dbx.Config, password, name string) error {
	admin := cfg
	admin.Database = maintenanceDatabase
	// No statement timeout on the maintenance connection: `CREATE DATABASE` and
	// `DROP DATABASE` are not affected by it, but a hung one would be, and a
	// harness that times out while creating a database reports itself as an
	// unavailable database rather than as a harness problem.
	admin.StatementTimeout = 0

	maint, err := dbx.Open(ctx, admin, password)
	if err != nil {
		return fmt.Errorf("connect to %s to create the test database: %w", maintenanceDatabase, err)
	}
	defer maint.Close()

	if _, err := maint.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(name)+` WITH (FORCE)`); err != nil {
		return harnessError("drop the previous test database %s: %v", name, err)
	}
	if _, err := maint.Exec(ctx, `CREATE DATABASE `+quoteIdent(name)+` TEMPLATE template0`); err != nil {
		return harnessError("create the test database %s: %v", name, err)
	}
	if err := ensureRoles(ctx, maint); err != nil {
		return err
	}
	return nil
}

// ensureRoles creates the five product roles once for the whole cluster, under a
// lock, before any package applies migration 0008, and pre-grants the membership
// 0008 grants unconditionally.
//
// 0008 guards its `CREATE ROLE` with `IF NOT EXISTS (SELECT 1 FROM pg_roles ...)`,
// which is correct and is not enough once packages run concurrently: the check and
// the create are not atomic against another session, and a PostgreSQL **role is
// cluster-wide** while a bigint `pg_advisory_lock` is scoped to the current
// database. Eight packages racing produce `tuple concurrently updated` out of
// `pg_authid`.
//
// The membership is the same defect with a wider blast radius, and this harness's
// first attempt at fixing only the roles missed it: 0008's
// `GRANT security_migrate TO CURRENT_USER` is **unconditional and unguarded**, and
// a role grant is a row in `pg_auth_members`, which is cluster-wide too. Retrying
// the whole step does not fix that, because the step's own duration is the
// contention window: eight packages each retry a four-second step and the last one
// waits minutes. It surfaced as `panic: test timed out after 10m0s` in three
// packages, not as an error anybody could read.
//
// Postgres's `GRANT` returns without writing when the membership already exists,
// so doing it here makes 0008's a no-op.
//
// The attributes are 0008's, copied rather than imported: `NOLOGIN` plus
// `BYPASSRLS` on the migrate role. A test harness that created these roles with a
// password would make an RLS test pass by authenticating as the role it is
// asserting cannot see anything, which is the failure mode `internal/rlsrole`
// exists to catch.
func ensureRoles(ctx context.Context, maint *dbx.Pool) error {
	if _, err := maint.Exec(ctx, `SELECT pg_advisory_lock($1, $2)`, roleLockClass, roleLockObj); err != nil {
		return harnessError("take the role-creation lock: %v", err)
	}
	defer func() {
		// Best effort, like the migrator's own unlock: the lock is released when
		// the connection dies, and a failure here must not mask the real error.
		_, _ = maint.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1, $2)`,
			roleLockClass, roleLockObj)
	}()

	for _, role := range productRoles {
		bypass := ""
		if role == "security_migrate" {
			bypass = " BYPASSRLS"
		}
		stmt := `DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = ` + quoteLiteral(role) + `) THEN
				CREATE ROLE ` + quoteIdent(role) + ` NOLOGIN` + bypass + `;
			END IF;
		END $$;`
		if _, err := maint.Exec(ctx, stmt); err != nil {
			return harnessError("create the product role %s: %v", role, err)
		}
	}

	// 0008's unguarded grant, done once under the same lock.
	if _, err := maint.Exec(ctx,
		`GRANT `+quoteIdent("security_migrate")+` TO CURRENT_USER`); err != nil {
		return harnessError("grant security_migrate to the migration role: %v", err)
	}
	return nil
}

// quoteLiteral renders a string literal for interpolation, doubling an embedded
// apostrophe. Used for the role names above, which come from this file, so the
// rule being applied is defence in depth rather than a defence.
func quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, "'", "''") + `'`
}

// Reset empties this package's database and leaves it as a fresh install.
//
// It drops and recreates `public` *inside this package's own database*. The old
// version did the same thing in the one database every package shared, and that
// is why `-p 1` is not optional: one suite's `DROP SCHEMA ... CASCADE` landed
// inside another's test and the failures looked like product bugs.
//
// It used to drop the whole database, which does not work here: a caller holds a
// `*pgxpool.Pool` from an earlier `Pool(t)`, and closing it out from under them
// turns every subsequent statement into `acquire connection: closed pool`. Every
// caller in the tree takes a pool first and resets afterwards — that is the shape
// of a test that wants a clean schema and its own fixtures — so the reset has to
// leave the connection alone.
//
// Dropping the database is still how a *run* starts clean, which is what
// `ensureDatabase` does the first time this binary asks for a pool.
func Reset(t *testing.T) {
	t.Helper()
	pool := Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The same statement the original used, now in a database of this package's
	// own. `GRANT ALL ON SCHEMA public TO public` is not decoration: without it a
	// fresh `public` has no privileges and the roles migration 0008 creates cannot
	// be granted anything in it.
	const reset = `
		DROP SCHEMA IF EXISTS public CASCADE;
		CREATE SCHEMA public;
		GRANT ALL ON SCHEMA public TO public;
	`
	if _, err := pool.Exec(ctx, reset); err != nil {
		t.Fatalf("reset the public schema in the test database %s: %v", Database(t), err)
	}
}

// harnessError marks an error as the harness's fault rather than the database's
// absence. A named type rather than a marker inside the message: the whole
// distinction is worth one type, and a `%w` chain through pgx would lose a
// sentinel half the time.
//
// The cause is rendered into the message rather than wrapped, because `harnessFault`
// is a string and a string cannot be unwrapped. The alternative — a struct with an
// `Unwrap` — is more machinery than one error path in a test harness is worth.
type harnessFault struct{ msg string }

func (e harnessFault) Error() string { return e.msg }

func harnessError(format string, args ...any) error {
	return harnessFault{msg: fmt.Sprintf(format, args...)}
}

func isHarness(err error) bool {
	// errors.As rather than a type assertion: every caller wraps with %v or
	// %w on the way out, and an assertion only sees the outermost type. This
	// function exists to tell the harness's own failure from the database's
	// absence, so a wrapped one that reads as absent turns a broken harness back
	// into a skip — which is the failure this harness already made once.
	var fault harnessFault
	return errors.As(err, &fault)
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

// ExpectError runs a statement and requires it to fail.
//
// It returns nothing, deliberately. The assertion is "this statement must be
// refused", and it is made by failing the test if the statement succeeds — which
// happens inside the helper. Returning the error as well gave every call site
// two ways to do nothing with it, and eight of them took the second: the call
// read as if the error were being checked when it was being dropped, and a
// reviewer had to look at the assignment to know which it was. A call site that
// wants the error asks for it by name; see Refusal.
func ExpectError(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	// Discarded on purpose and visibly. This call site exists to make the
	// statement fail; a caller that needs the reason calls Refusal, which returns
	// it. A bare `Refusal(...)` here would be the same discard with less
	// ceremony, and the linter is right that it looks like a dropped error.
	_ = Refusal(t, pool, sql, args...)
}

// Refusal is ExpectError for the callers that need to say *why* the statement was
// refused - a unique violation, an append-only trigger, a check constraint.
//
// This matters more than it looks: "the statement failed" and "the statement
// failed for the reason the product intends" are different claims, and a
// constraint test that only checks the first will pass the day the statement
// starts failing for a different reason.
func Refusal(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) error {
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
// and site context set. It is how the tests assert that least privilege is real
// rather than declared.
//
// `sites` may be empty, and an empty scope is meaningful: since migration 21
// `site_isolation` is RESTRICTIVE, so the effective predicate is
// `tenant_isolation AND site_isolation` and a context with no `app.site_ids`
// sees no site-scoped rows at all. That is fail-closed and it is the point: a
// statement that reaches a site-scoped table without saying which site it wants
// is not allowed to guess.
func AsRole(t *testing.T, pool *pgxpool.Pool, role, tenant string, sites []string, fn func(tx pgx.Tx)) {
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
	//
	// The role name is quoted. Every caller passes a literal today, so the quoting
	// buys nothing measurable — and it is here because this is the one function in
	// the harness that establishes the authorisation context, fifty lines below
	// `quoteIdent`, and a role name threaded from a test-case variable or a config
	// row would turn an unquoted `%s` into injection in the place where a bad role
	// assumption would be least likely to be noticed.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL ROLE %s`, quoteIdent(role))); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	if tenant != "" {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenant); err != nil {
			t.Fatalf("set tenant context: %v", err)
		}
	}
	if len(sites) > 0 {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.site_ids', $1, true)`,
			dbx.EncodeSiteIDs(sites)); err != nil {
			t.Fatalf("set site context: %v", err)
		}
	}
	fn(tx)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

// failHarness records that the harness itself is broken and fails the run.
//
// It exists because "no database" and "the harness is broken" are different claims
// with opposite correct responses. A contributor without Postgres should get a
// green run that skips; a developer whose DDL has a syntax error should get a red
// one. Routing both through `t.Skipf` made the second look like the first — found
// in practice, when a `CREATE SCHEMA IF NOT EXISTS quote_ident('x')` typo silently
// skipped the entire database-backed suite and every package reported `ok`.
//
// The failure is recorded as well as reported, so a package with twenty
// database-backed tests reports one cause rather than twenty identical skips that
// read as twenty independent problems.
func failHarness(t *testing.T, format string, args ...any) {
	t.Helper()
	poolMu.Lock()
	if harnessErr == nil {
		harnessErr = fmt.Errorf(format, args...)
	}
	recorded := harnessErr
	poolMu.Unlock()
	t.Fatalf("the test harness is broken, which is not the same as having no database: %v", recorded)
}

// quoteIdent renders an identifier for interpolation.
//
// Double quotes, with an embedded one doubled. The database name is derived from a
// directory path this package controls, so it is not attacker-supplied — but it is
// still a value built at runtime and pasted into SQL, and `CREATE DATABASE ` + a
// name is exactly the shape that turns a future rename into an injection.
//
// Postgres has no `quote_ident()` usable as a bare identifier, which this version
// found by writing `CREATE SCHEMA IF NOT EXISTS quote_ident('x')` and reading a
// syntax error.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
