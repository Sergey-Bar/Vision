package dbx

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tenant is a Pool bound to one tenant.
//
// It exists because the bare Pool.Query, Pool.QueryRow and Pool.Exec are the
// wrong default for a database where most tables carry tenant_id and row-level
// security. A statement issued without a tenant context does not fail loudly: a
// SELECT returns no rows, and an INSERT is refused by the policy. The first is a
// silent wrong answer; the second is a function that simply does not work. Both
// were present in this codebase and neither was visible in the test suite,
// because the suite connects as a superuser, which bypasses the policy.
//
// A Tenant-bound pool makes the context impossible to forget rather than merely
// discouraged: there is no way to call Exec on it without the tenant being set,
// and a static check in internal/rlsrole fails the build if a bare call against a
// tenant-scoped table reappears.
//
// Use For for anything scoped to one tenant. Use InSystemTenant for work that
// legitimately spans tenants - a background worker, a migration, the installer
// creating a tenant's first principal.
type Tenant struct {
	pool *Pool
	tc   TenantContext
}

// For binds a pool to one tenant.
//
// The actor is recorded on the connection so a statement can attribute itself, and
// it is separate from the tenant: a background worker acting for one tenant
// passes the worker's name and the tenant's id, which is the pair an auditor
// needs.
func (p *Pool) For(tenant, actor string) *Tenant {
	return &Tenant{pool: p, tc: TenantContext{TenantID: tenant, ActorID: actor}}
}

// WithSites binds a pool to one tenant and a set of sites, so a site-scoped
// policy sees the same scope the service layer checked.
func (p *Pool) WithSites(tenant, actor string, sites []string) *Tenant {
	return &Tenant{pool: p, tc: TenantContext{TenantID: tenant, ActorID: actor, SiteIDs: sites}}
}

// TenantID is the bound tenant.
func (t *Tenant) TenantID() string { return t.tc.TenantID }

// Pool returns the underlying pool, for the rare caller that genuinely needs a
// tenant-independent statement.
func (t *Tenant) Pool() *Pool { return t.pool }

// Exec runs one statement with the tenant context applied.
func (t *Tenant) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := t.require(); err != nil {
		return pgconn.CommandTag{}, err
	}
	// An Exec has no result to hold, so the whole statement runs inside one
	// transaction and the context is scoped to it. Nothing to release, nothing to
	// leak onto the next statement that connection runs.
	var tag pgconn.CommandTag
	err := t.pool.InTenant(ctx, t.tc, func(tx pgx.Tx) error {
		var execErr error
		tag, execErr = tx.Exec(ctx, sql, args...)
		return execErr
	})
	return tag, err
}

// QueryRow runs one query with the tenant context applied.
//
// The connection is held from here until the caller scans, and released by the
// scan. That is the same contract pgx's own QueryRow has — a Row is a promise
// that something will be read - so a call site reads exactly as it did against
// the bare pool.
//
// It scans at most once. A second Scan is refused rather than returning the first
// result again, because a caller that does that has a bug and a stale value is a
// worse answer than an error.
func (t *Tenant) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := t.require(); err != nil {
		return errorRow{err: err}
	}
	conn, err := t.pool.Acquire(ctx)
	if err != nil {
		return errorRow{err: fmt.Errorf("dbx: acquire for a tenant query: %w", err)}
	}
	if err := setTenantOnConn(ctx, conn, t.tc); err != nil {
		conn.Release()
		return errorRow{err: err}
	}
	return &tenantRow{
		ctx: ctx, conn: conn, sql: sql, args: args,
	}
}

type tenantRow struct {
	ctx     context.Context
	conn    *pgxpool.Conn
	sql     string
	args    []any
	scanned bool
}

func (r *tenantRow) Scan(dest ...any) error {
	if r.scanned {
		return fmt.Errorf("dbx: this row has already been scanned; a pgx.Row yields its result once")
	}
	r.scanned = true
	defer clearTenantOnConn(r.ctx, r.conn)

	row := r.conn.QueryRow(r.ctx, r.sql, r.args...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return fmt.Errorf("dbx: tenant query: %w", err)
	}
	return nil
}

type errorRow struct{ err error }

func (e errorRow) Scan(...any) error { return e.err }

// Query runs a multi-row query with the tenant context applied.
//
// The connection is released when the rows are closed, and the context is
// cleared first, so a returned-to-pool connection can never carry one tenant's
// context into the next statement.
func (t *Tenant) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := t.require(); err != nil {
		return nil, err
	}
	conn, err := t.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("dbx: acquire for a tenant query: %w", err)
	}
	if err := setTenantOnConn(ctx, conn, t.tc); err != nil {
		conn.Release()
		return nil, err
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		clearTenantOnConn(ctx, conn)
		conn.Release()
		return nil, err
	}
	return &tenantRows{Rows: rows, conn: conn}, nil
}

func (t *Tenant) require() error {
	if t.tc.TenantID == "" {
		return fmt.Errorf("%w: For() was called with an empty tenant, so every statement it "+
			"runs would see no rows", ErrNoTenant)
	}
	return nil
}

func setTenantOnConn(ctx context.Context, conn *pgxpool.Conn, tc TenantContext) error {
	if tc.TenantID == "" {
		return fmt.Errorf("%w: a tenant is required for a tenant-scoped statement", ErrNoTenant)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('app.tenant_id', $1, false)`, tc.TenantID); err != nil {
		return fmt.Errorf("dbx: set app.tenant_id: %w", err)
	}
	if tc.ActorID != "" {
		if _, err := conn.Exec(ctx, `SELECT set_config('app.actor_id', $1, false)`, tc.ActorID); err != nil {
			return fmt.Errorf("dbx: set app.actor_id: %w", err)
		}
	}
	if len(tc.SiteIDs) > 0 {
		if _, err := conn.Exec(ctx, `SELECT set_config('app.site_ids', $1, false)`,
			encodeSiteIDs(tc.SiteIDs)); err != nil {
			return fmt.Errorf("dbx: set app.site_ids: %w", err)
		}
	}
	return nil
}

// clearTenantOnConn wipes the context before the connection goes back, and
// destroys the connection if it cannot.
//
// This is the line between "tenants are isolated" and "tenants are isolated
// until the next statement". A connection returned to the pool with
// app.tenant_id still set runs the next caller's statement as the previous
// tenant, and the caller has no way to know: every query succeeds and every one
// of them is wrong. This is invariant 5.
//
// The previous version discarded the error and released unconditionally, which
// made a failed clear indistinguishable from a successful one. The setting is
// session-level (set_config(..., false)), not SET LOCAL, precisely so it
// survives the statement and applies to statements issued outside a
// transaction; that is what makes this clear load-bearing rather than tidiness.
func clearTenantOnConn(ctx context.Context, conn *pgxpool.Conn) {
	_, err := conn.Exec(context.WithoutCancel(ctx),
		`SELECT set_config('app.tenant_id', '', false),
		        set_config('app.actor_id', '', false),
		        set_config('app.site_ids', '', false)`)
	releaseConn(ctx, conn, err)
}

// releaseConn returns the connection to the pool, or destroys it first when the
// tenant clear failed.
//
// Destroying is the right failure. A connection that may carry a tenant we can
// no longer prove it has dropped is not a connection worth reusing, and the pool
// opens another one. The cost is one connection; the cost of getting it wrong is
// a tenant reading another tenant's data with no error anywhere.
//
// Release happens in both cases because pgxpool requires it: a Conn that is
// never released keeps the pool open for ever. The pool is what actually
// discards it - by the time it looks, the connection is closed, so it is
// replaced rather than reissued.
func releaseConn(ctx context.Context, conn *pgxpool.Conn, clearErr error) {
	if clearErr != nil {
		_ = conn.Conn().Close(context.WithoutCancel(ctx))
	}
	conn.Release()
}

type tenantRows struct {
	pgx.Rows
	conn    *pgxpool.Conn
	cleared bool
}

func (r *tenantRows) Close() {
	r.Rows.Close()
	if !r.cleared {
		r.cleared = true
		clearTenantOnConn(context.Background(), r.conn)
	}
}
