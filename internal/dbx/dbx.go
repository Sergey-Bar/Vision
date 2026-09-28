// Package dbx owns the Postgres connection policy.
//
// One pool, one tenant-context discipline, one place where a transaction is
// allowed to set `app.tenant_id`. Every read of tenant-scoped data goes through
// a Tx, because a session-level `SET` would leak the previous request's tenant
// into a pooled connection.
package dbx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is a connection pool with a tenant context bound to each transaction.
type Pool struct {
	*pgxpool.Pool
}

// Config is the non-secret connection policy. The password is never here: it is
// read from the encrypted secret store at connect time and passed to the driver
// directly, so it cannot land in a config file, a log line, or a process dump.
type Config struct {
	Host                string
	Port                int
	Database            string
	User                string
	SSLMode             string
	MaxOpenConns        int
	MaxIdleConns        int
	ConnMaxLifetime     time.Duration
	StatementTimeout    time.Duration
	ApplicationName     string
	connectionMaxWait   time.Duration
	HealthCheckInterval time.Duration
}

// DefaultConfig is the single-instance, single-site profile from the capacity
// target: roughly 2,000 cameras, 50 alarm-generating devices, 20 events/sec.
func DefaultConfig() Config {
	return Config{
		Host:                "127.0.0.1",
		Port:                5432,
		Database:            "security",
		User:                "security_core",
		SSLMode:             "require",
		MaxOpenConns:        32,
		MaxIdleConns:        8,
		ConnMaxLifetime:     30 * time.Minute,
		StatementTimeout:    15 * time.Second,
		ApplicationName:     "security-core",
		connectionMaxWait:   5 * time.Second,
		HealthCheckInterval: 30 * time.Second,
	}
}

// DSN renders the connection string with the password omitted. Safe to log.
func (c Config) DSN() string {
	u := url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Database,
	}
	q := u.Query()
	if c.SSLMode != "" {
		q.Set("sslmode", c.SSLMode)
	}
	q.Set("application_name", c.ApplicationName)
	u.RawQuery = q.Encode()
	return u.String()
}

// Open builds a pool. password is supplied out of band, never read from a
// config file or an environment variable that ends up in a diagnostic dump.
func Open(ctx context.Context, cfg Config, password string) (*Pool, error) {
	connStr := cfg.DSN()
	if password != "" {
		u, err := url.Parse(connStr)
		if err != nil {
			return nil, fmt.Errorf("dbx: parse dsn: %w", err)
		}
		u.User = url.UserPassword(cfg.User, password)
		connStr = u.String()
	}

	poolCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("dbx: parse config: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.MaxOpenConns)
	poolCfg.MinConns = int32(cfg.MaxIdleConns)
	poolCfg.MaxConnLifetime = cfg.ConnMaxLifetime
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = cfg.HealthCheckInterval
	poolCfg.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName

	if cfg.StatementTimeout > 0 {
		// A statement that runs forever on a site holding a live incident feed
		// is a site that stops ingesting. The SLO owns this number, not taste.
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] =
			fmt.Sprintf("%d", cfg.StatementTimeout.Milliseconds())
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("dbx: open pool: %w", err)
	}
	return &Pool{Pool: pool}, nil
}

// TenantContext is the per-transaction identity every tenant-scoped statement
// runs under. It comes from the authenticated session, never from a request
// body.
type TenantContext struct {
	TenantID string
	SiteIDs  []string
	ActorID  string
	Session  string
}

// ErrNoTenant is returned when a tenant-scoped operation is attempted without
// a tenant context. Failing closed here is the whole point: a missing context
// must surface as an error, not as an unfiltered table scan.
var ErrNoTenant = errors.New("dbx: tenant context is required for tenant-scoped work")

// InTenant runs fn inside a transaction with the tenant context applied.
//
// SET LOCAL is deliberate. A pooled connection carries session state between
// uses, and a session-level SET would let one request's tenant leak into the
// next request that happens to get the same connection.
func (p *Pool) InTenant(ctx context.Context, tc TenantContext, fn func(pgx.Tx) error) error {
	if tc.TenantID == "" {
		return ErrNoTenant
	}
	return p.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tc.TenantID); err != nil {
			return fmt.Errorf("dbx: set app.tenant_id: %w", err)
		}
		if tc.ActorID != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id', $1, true)`, tc.ActorID); err != nil {
				return fmt.Errorf("dbx: set app.actor_id: %w", err)
			}
		}
		if tc.Session != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.session_id', $1, true)`, tc.Session); err != nil {
				return fmt.Errorf("dbx: set app.session_id: %w", err)
			}
		}
		if len(tc.SiteIDs) > 0 {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.site_ids', $1, true)`, encodeSiteIDs(tc.SiteIDs)); err != nil {
				return fmt.Errorf("dbx: set app.site_ids: %w", err)
			}
		}
		return fn(tx)
	})
}

// InTx runs fn in a transaction with no tenant context. Used by the migrator
// and by work that is genuinely tenant-independent, such as schema inspection.
func (p *Pool) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dbx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dbx: commit: %w", err)
	}
	return nil
}

// SystemTenant is the tenant context used by background workers that legitimately
// operate across the rows they were given explicitly — the inbox normalizer and
// the job runner. It is a named constant rather than an empty string, so that
// "no context" and "worker context" can never be confused for one another.
const SystemTenant = "__system__"

// InSystemTenant runs fn as a background worker: full tenant visibility, with
// the actor recorded as the system. Background workers take the tenant from the
// row they are processing, never from a request.
func (p *Pool) InSystemTenant(ctx context.Context, fn func(pgx.Tx) error) error {
	return p.InTenant(ctx, TenantContext{TenantID: SystemTenant, ActorID: "system"}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT set_config('app.system_worker', 'true', true)`)
		if err != nil {
			return fmt.Errorf("dbx: set app.system_worker: %w", err)
		}
		return fn(tx)
	})
}

func encodeSiteIDs(ids []string) string {
	out := make([]byte, 0, len(ids)*8)
	for i, id := range ids {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, id...)
	}
	return string(out)
}
