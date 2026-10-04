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
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is a connection pool with a tenant context bound to each transaction.
type Pool struct {
	*pgxpool.Pool
}

// Config is the connection policy. It holds no secret by default: the password is
// read from the encrypted secret store at connect time and handed to the driver
// directly, so it cannot land in a config file, a log line, or a process dump.
//
// The one exception is the unexported `password` field below, which is populated
// only when the DSN itself carried a password and which no rendering of a Config
// can print. That is a deliberate trade rather than a loosened policy: the
// alternative was a DSN whose password was silently discarded, and a connection
// that then authenticated as a role the operator never named.
type Config struct {
	Host             string
	Port             int
	Database         string
	User             string
	SSLMode          string
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	StatementTimeout time.Duration
	ApplicationName  string
	// SearchPath is the schema unqualified names resolve in.
	//
	// Empty means the server's default, which is what a single-deployment site
	// wants and what every migration in `migrations/` is written against: not one
	// of them qualifies a table name. It is here because a second deployment in
	// the same database, and the test harness, both need the same thing — and a
	// caller that had to set `search_path` by hand per connection would set it
	// on some connections and not others, which is the failure mode
	// `app.tenant_id` is documented about three functions above.
	SearchPath          string
	connectionMaxWait   time.Duration
	HealthCheckInterval time.Duration

	// password is set only by ApplyDSN, and only from a DSN that carried one. It is
	// unexported so that no caller can log it by naming the field, and String()
	// exists so that no caller can log it by printing the struct - fmt prints
	// unexported fields under %v and %+v, so without String() a diagnostic written
	// as "%+v" would carry it into whatever the diagnostic was pasted into.
	//
	// The dedicated password variable still wins over this one. See Open.
	password string
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

// ErrNoTarget is returned when no database was named and none was guessed.
var ErrNoTarget = errors.New("dbx: no database target")

// ConfigForTarget resolves which database to talk to, and refuses to guess.
//
// The compiled-in default names localhost:5432/security as user security_core.
// Returning it when nothing was specified means `security-migrate up`, typed in a
// shell that happens not to have SP_DATABASE_URL exported, migrates whatever
// happens to be listening on the local port. That is the worst thing a migration
// runner can do, and making it the default rather than an explicit choice is what
// makes it likely.
//
// So the refusal names both ways to name a database and prints the default it
// declined to use. `DSN` omits the password, so the message is safe to log.
func ConfigForTarget(dsnFlag, envVar string) (Config, error) {
	cfg := DefaultConfig()

	dsn := dsnFlag
	if dsn == "" {
		dsn = os.Getenv(envVar)
	}
	if dsn == "" {
		return cfg, fmt.Errorf("%w: pass -dsn, or set %s. Nothing named a database, and the "+
			"compiled-in default is %s — which is a guess about which database on this host you "+
			"meant, and applying migrations to it is not a guess this tool should make for you",
			ErrNoTarget, envVar, cfg.DSN())
	}
	// A DSN may carry a user. It must not carry a password in the environment: an
	// environment variable survives into child processes and diagnostic dumps, a
	// flag does not. The password has its own dedicated variable.
	if err := cfg.ApplyDSN(dsn); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// DSN renders the connection string with the password omitted. Safe to log.
//
// The user is included, because a DSN without one is not the same connection: it
// resolves the user from the environment, which for a libpq client means the OS
// user, and the resulting error is "password authentication failed for
// user <your-login>" — a message about a role nobody asked for. It was omitted
// until a test harness rewrote `SP_TEST_DATABASE_URL` with `DSN()` and every
// package that opens its own connection started authenticating as whatever the
// developer is logged in as.
func (c Config) DSN() string { return c.renderDSN("") }

// String is what every fmt verb prints for a Config, and it is DSN().
//
// This is a control, not a convenience. fmt prints unexported struct fields under
// %v and %+v, so once a Config could hold a password at all, the diagnostic
// written as "%+v" - which is how two test files report what a Config held -
// would have put the credential into the test output, and into whatever that
// output was pasted into. Routing every rendering through the password-free DSN
// is what keeps the field private in the only sense that counts.
func (c Config) String() string { return c.DSN() }

// connectionString is the string handed to the driver: the DSN with a role, and a
// password resolved out of band or out of the URL.
//
// The role is attached unconditionally, which is the same defect DSN() above
// documents from the logging side. Open used to attach a role only on the branch
// that had a password to attach, so a DSN naming a role and no password reached
// pgx naming no role at all: a migrator told to authenticate as `postgres`
// authenticated as whoever ran it. Open now refuses an empty role outright, so
// this only has to carry the roles it is handed.
//
// An out-of-band password wins, so the dedicated variable stays authoritative and a
// stale URL cannot override a deliberate one. A DSN password is used only when
// nothing was supplied, which is the case it exists for: it is the only credential
// available when SP_DATABASE_PASSWORD is unset.
func (c Config) connectionString(outOfBand string) string {
	if outOfBand == "" {
		outOfBand = c.password
	}
	return c.renderDSN(outOfBand)
}

// renderDSN is the one place a connection string is built, and the only place a
// password can enter one.
func (c Config) renderDSN(password string) string {
	u := url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Database,
	}
	if c.User != "" {
		// With no password this is exactly `url.User(c.User)`, which is what the
		// user-in-the-DSN fix needs. Branched rather than always url.UserPassword,
		// because that renders a bare trailing colon when the password is empty
		// ("postgres://core@db/" would become "postgres://core:@db/"). DSN() is a log
		// line and a preflight report; there is no reason for it to change shape.
		if password == "" {
			u.User = url.User(c.User)
		} else {
			u.User = url.UserPassword(c.User, password)
		}
	}
	q := u.Query()
	if c.SSLMode != "" {
		q.Set("sslmode", c.SSLMode)
	}
	q.Set("application_name", c.ApplicationName)
	u.RawQuery = q.Encode()
	return u.String()
}

// Open builds a pool. password is supplied out of band and takes precedence over
// one carried in the DSN, so the dedicated variable stays the authoritative source
// and a stale URL cannot override a deliberate one.
func Open(ctx context.Context, cfg Config, password string) (*Pool, error) {
	if cfg.User == "" {
		// Refused rather than attempted.
		//
		// A DSN with an empty user resolves the user from the environment, which
		// for a libpq client means the OS user — so the connection authenticates as
		// whoever happens to be logged in and the failure reads "password
		// authentication failed for user <your-login>", a message about a role
		// nobody asked for. `ApplyDSN` leaves `User` empty when the DSN carries no
		// `user=`, so this is reachable from a hand-written DSN.
		//
		// `DSN()` renders the user for the same reason, and the two were fixed
		// separately: the rendering path first, because a log line without a user is
		// unreadable, and this one second, because the rendering path is not the one
		// that authenticates.
		return nil, fmt.Errorf(
			"dbx: no user is configured, so the connection would authenticate as the OS user. " +
				"Set SP_DATABASE_URL with a user= component, or pass a Config with User set")
	}

	connStr := cfg.connectionString(password)

	poolCfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("dbx: parse config: %w", err)
	}
	poolCfg.MaxConns = clampConns(cfg.MaxOpenConns)
	poolCfg.MinConns = clampConns(cfg.MaxIdleConns)
	poolCfg.MaxConnLifetime = cfg.ConnMaxLifetime
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	poolCfg.HealthCheckPeriod = cfg.HealthCheckInterval
	poolCfg.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName

	// Applied as a runtime parameter rather than as a `SET` per connection, so it
	// cannot be missed on a pooled connection opened later. A connection that
	// resolved unqualified names against a different schema would read another
	// deployment's tables and, with row-level security on, silently see nothing.
	if cfg.SearchPath != "" {
		poolCfg.ConnConfig.RuntimeParams["search_path"] = cfg.SearchPath
	}

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
	// System marks the transaction as a SYSTEM WORKER, which every `site_isolation`
	// policy exempts via `app_is_system_worker()`.
	//
	// It exists because "this specific tenant" and "exempt from site scoping" are
	// independent: `InSystemTenant` binds the cross-tenant system tenant and gives no
	// way to name a tenant, so a caller that needs one had to reach past this type and
	// set the GUC by hand. `identity.BootstrapAdmin` is that caller.
	//
	// **It is an escalation and it belongs on this type so that it is visible.** Every
	// policy ORs it in, so a transaction with it set is unconstrained by site on every
	// site-scoped table at once. That is what a migration, a backup and a deliberate
	// cross-site operator query need, and it is exactly what an ingest loop must never
	// have, which is why nothing derives it from a request.
	System bool
}

// clampConns keeps a connection count inside the range the pool's own field can
// hold.
//
// The conversion is a narrowing one and the value comes from configuration, so a
// typo like a missing digit wraps to a negative number. A negative MaxConns is
// not a small pool; it is a pool that refuses to open a connection at all, and
// the site fails at startup with a message about the driver rather than about the
// setting.
func clampConns(n int) int32 {
	const ceiling = int64(1<<31 - 1)
	switch {
	case n < 0:
		return 0
	case int64(n) > ceiling:
		return int32(ceiling)
	default:
		return int32(n)
	}
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
		if tc.System {
			// `app.system_worker` is what `app_is_system_worker()` reads, and every
			// `site_isolation` policy ORs it in. It is set HERE rather than only by
			// `InSystemTenant` because a caller often needs a SPECIFIC tenant and the
			// worker exemption at the same time.
			//
			// `identity.BootstrapAdmin` is that caller, and migration 0023 is what made
			// it one: `sites` had no `site_isolation` until then, so a bootstrap could
			// read the site it was about to assign an administrator to. With the policy
			// in place and no `app.site_ids` bound, that read returns zero rows and the
			// bootstrap reports "site %q does not exist" for a site that plainly does.
			//
			// Setting the GUC directly would have worked too. It is a field because a
			// caller reaching past this type to set a GUC by hand is a caller that does
			// not know it needed to.
			if _, err := tx.Exec(ctx, `SELECT set_config('app.system_worker', 'true', true)`); err != nil {
				return fmt.Errorf("dbx: set app.system_worker: %w", err)
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
// operate across the rows they were given explicitly - the inbox normalizer and
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

// EnvOrDefault reads a non-secret setting from the environment. It exists for
// non-secret values only: a password never goes through it, because an
// environment variable survives into child processes and diagnostic dumps.
func EnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EncodeSiteIDs renders a site list as a PostgreSQL array literal, for
// `set_config('app.site_ids', ...)`.
//
// The braces and the quoting are not decoration. `app_current_site_ids` is
//
//	nullif(current_setting('app.site_ids', true), '')::text[]
//
// so the setting is CAST to text[], and a bare `a,b` is a malformed array
// literal: every query that consulted it failed with "malformed array literal".
// The function used to emit the comma-joined form, and nothing noticed, because
// `site_isolation` was PERMISSIVE and therefore never consulted it. The moment
// the policy became RESTRICTIVE the setting started being read on every row and
// the bug appeared on the first write.
//
// A site id containing a comma, a quote or a backslash is quoted and escaped,
// because an unquoted one would silently change the list's length.
func EncodeSiteIDs(ids []string) string {
	var out strings.Builder
	out.WriteByte('{')
	for i, id := range ids {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('"')
		for _, r := range id {
			if r == '"' || r == '\\' {
				out.WriteByte('\\')
			}
			out.WriteRune(r)
		}
		out.WriteByte('"')
	}
	out.WriteByte('}')
	return out.String()
}

func encodeSiteIDs(ids []string) string { return EncodeSiteIDs(ids) }
