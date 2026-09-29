// Package migrate applies the ordered SQL that builds the Postgres schema.
//
// Three rules, in order of how much damage breaking them does:
//
//  1. Migrations are forward-only. An applied migration is never edited. A
//     correction is a new, higher-numbered migration, because a site that has
//     already run version N must keep agreeing with the binary that ran it.
//  2. A contract step never ships in the same release as the expand step that
//     makes it safe. The window between them is the window in which the old and
//     new binary both have to work, and collapsing it is how an upgrade takes a
//     site down at 3am.
//  3. The runner refuses to touch a database whose schema version is outside
//     this build's declared window, rather than guessing.
package migrate

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/Ai-Captain/migrations"
)

// lockID is the advisory lock every runner takes. Two `security-migrate up`
// processes, or one racing the installer, must serialize rather than interleave
// DDL.
const lockID int64 = 0x1F0CA11_5EC0011

// Applied is one row of the migration ledger.
type Applied struct {
	Version   int           `json:"version"`
	Name      string        `json:"name"`
	Phase     string        `json:"phase"`
	Checksum  string        `json:"checksum"`
	AppliedAt time.Time     `json:"appliedAt"`
	Duration  time.Duration `json:"-"`
	DurationS string        `json:"duration"`
}

// Runner applies migrations against a pool.
type Runner struct {
	Pool *pgxpool.Pool
	// DryRun makes Up compute the plan and stop. Used by `preflight` and by CI.
	DryRun bool
	// Logf receives human-readable progress. It never receives SQL or a DSN.
	Logf func(format string, args ...any)
}

const bootstrapDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version      INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    phase        TEXT NOT NULL CHECK (phase IN ('expand', 'backfill', 'contract')),
    checksum     TEXT NOT NULL,
    applied_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_by   TEXT NOT NULL DEFAULT current_user,
    duration_ms  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS schema_migrations_applied_idx ON schema_migrations (applied_at);
`

// EnsureLedger creates the migration ledger if it is absent.
func (r *Runner) EnsureLedger(ctx context.Context) error {
	if _, err := r.Pool.Exec(ctx, bootstrapDDL); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	return nil
}

// CurrentVersion reports the highest applied version, or 0 on a fresh database.
func (r *Runner) CurrentVersion(ctx context.Context) (int, error) {
	var v int
	err := r.Pool.QueryRow(ctx,
		`SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("migrate: read current version: %w", err)
	}
	return v, nil
}

// Applied returns the migration ledger in version order.
func (r *Runner) Applied(ctx context.Context) ([]Applied, error) {
	rows, err := r.Pool.Query(ctx,
		`SELECT version, name, phase, checksum, applied_at, duration_ms
		   FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read ledger: %w", err)
	}
	defer rows.Close()

	var out []Applied
	for rows.Next() {
		var a Applied
		// duration_ms is milliseconds and time.Duration is nanoseconds. Scanning
		// the column straight into the Duration read a 1.5s migration back as
		// 1500ns, which DurationS then rendered as "1.5µs" on the operator-facing
		// migration status. The conversion belongs here, in the only place that
		// knows which unit the column is in.
		var durationMS int64
		if err := rows.Scan(&a.Version, &a.Name, &a.Phase, &a.Checksum, &a.AppliedAt, &durationMS); err != nil {
			return nil, fmt.Errorf("migrate: scan ledger: %w", err)
		}
		a.Duration = time.Duration(durationMS) * time.Millisecond
		a.DurationS = a.Duration.String()
		out = append(out, a)
	}
	return out, rows.Err()
}

// verifyIdentity compares one applied ledger row against the embedded file with
// the same version, on the three things that together identify what a site ran.
//
// The checksum covers the migration body only, deliberately: changing the digest
// format would make every already-deployed site refuse to upgrade, and nothing
// in this repository can UPDATE schema_migrations to re-record it, because the
// migration is the very thing being blocked. The identity the digest omits - the
// file's own name and phase — is already recorded in the ledger, so it is
// compared here rather than folded into the hash. A rename, a rewritten phase,
// or a file swapped in under an existing version now fails verification with no
// format change, no re-record, and no weakened window.
//
// Each failure names which of the three diverged, so a human reading the error
// knows whether the bytes, the name, or the phase moved.
func verifyIdentity(byVersion map[int]migrations.Migration, version int, name, phase, sum string) error {
	have, ok := byVersion[version]
	if !ok {
		return fmt.Errorf("migrate: version %d (%s) is applied but absent from this build; "+
			"this binary is older than the database", version, name)
	}
	if have.Checksum != sum {
		return fmt.Errorf("migrate: checksum mismatch for %s: applied %s, build has %s. "+
			"Applied migrations are never edited; add a new migration instead",
			have.FileName(), sum[:16], have.Checksum[:16])
	}
	if have.Name != name {
		return fmt.Errorf("migrate: name mismatch for version %d: applied %q, build has %q. "+
			"A migration is never renamed once applied; add a new migration instead",
			version, name, have.Name)
	}
	if string(have.Phase) != phase {
		return fmt.Errorf("migrate: phase mismatch for %s: applied %q, build has %q. "+
			"A migration's phase is never rewritten once applied; add a new migration instead",
			have.FileName(), phase, string(have.Phase))
	}
	return nil
}

// verifyLedgerRows runs verifyIdentity over the whole ledger.
func verifyLedgerRows(byVersion map[int]migrations.Migration, rows []Applied) error {
	for _, a := range rows {
		if err := verifyIdentity(byVersion, a.Version, a.Name, a.Phase, a.Checksum); err != nil {
			return err
		}
	}
	return nil
}

func embeddedByVersion() (map[int]migrations.Migration, error) {
	all, err := migrations.All()
	if err != nil {
		return nil, fmt.Errorf("migrate: load embedded migrations: %w", err)
	}
	byVersion := make(map[int]migrations.Migration, len(all))
	for _, m := range all {
		byVersion[m.Version] = m
	}
	return byVersion, nil
}

// VerifyChecksums compares every applied migration against the embedded copy.
//
// A mismatch means the SQL on disk - or in the binary - no longer matches what
// this site executed. That is a release integrity failure, and continuing would
// mean the recorded history is a lie.
func (r *Runner) VerifyChecksums(ctx context.Context) error {
	byVersion, err := embeddedByVersion()
	if err != nil {
		return err
	}
	applied, err := r.Applied(ctx)
	if err != nil {
		return err
	}
	return verifyLedgerRows(byVersion, applied)
}

// Plan is the ordered work Up would perform.
type Plan struct {
	CurrentVersion int
	TargetVersion  int
	Pending        []migrations.Migration
	Compat         migrations.Compatibility
	Compatible     bool
}

// compatible reports whether a database at `current` may be migrated by this
// build.
//
// A fresh database counts as compatible: version 0 has no schema to be
// incompatible with, and refusing to install onto an empty cluster would make
// the first install of every site fail. The window's job is to stop a binary
// from reshaping a database whose shape it does not understand, not to stop a
// build from creating a database it defines itself.
func compatible(current int, c migrations.Compatibility) bool {
	if current == 0 {
		return true
	}
	return current >= c.MinSchemaVersion && current <= c.MaxSchemaVersion
}

// BuildPlan computes the pending set and checks it against the release's
// declared schema window.
func (r *Runner) BuildPlan(ctx context.Context) (Plan, error) {
	all, err := migrations.All()
	if err != nil {
		return Plan{}, fmt.Errorf("migrate: load embedded migrations: %w", err)
	}
	compat, err := migrations.ReleaseCompatibility()
	if err != nil {
		return Plan{}, fmt.Errorf("migrate: read compatibility manifest: %w", err)
	}
	current, err := r.CurrentVersion(ctx)
	if err != nil {
		return Plan{}, err
	}

	p := Plan{CurrentVersion: current, TargetVersion: 0, Compat: compat}
	if n := len(all); n > 0 {
		p.TargetVersion = all[n-1].Version
	}
	for _, m := range all {
		if m.Version > current {
			p.Pending = append(p.Pending, m)
		}
	}
	p.Compatible = compatible(current, compat)
	return p, nil
}

// Up applies every pending migration, one transaction per step, under a
// session-level advisory lock.
func (r *Runner) Up(ctx context.Context) (Plan, error) {
	conn, err := r.Pool.Acquire(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("migrate: acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return Plan{}, fmt.Errorf("migrate: take advisory lock: %w", err)
	}
	defer func() {
		// Best effort: the lock is also released when the connection dies, so a
		// failure here must not mask the original error.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockID)
	}()

	if _, err := conn.Exec(ctx, bootstrapDDL); err != nil {
		return Plan{}, fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	if err := r.verifyChecksumsOn(ctx, conn.Conn()); err != nil {
		return Plan{}, err
	}

	plan, err := r.planOn(ctx, conn.Conn())
	if err != nil {
		return Plan{}, err
	}
	if !plan.Compatible {
		return plan, &migrations.IncompatibleSchemaError{
			Current: plan.CurrentVersion,
			Min:     plan.Compat.MinSchemaVersion,
			Max:     plan.Compat.MaxSchemaVersion,
			Reason:  "the database sits outside this build's supported window",
		}
	}
	if r.DryRun {
		return plan, nil
	}

	for _, m := range plan.Pending {
		start := time.Now()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return plan, fmt.Errorf("migrate: begin %s: %w", m.FileName(), err)
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return plan, fmt.Errorf("migrate: apply %s: %w", m.FileName(), err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, phase, checksum, duration_ms)
			 VALUES ($1, $2, $3, $4, $5)`,
			m.Version, m.Name, string(m.Phase), m.Checksum, time.Since(start).Milliseconds()); err != nil {
			_ = tx.Rollback(ctx)
			return plan, fmt.Errorf("migrate: record %s: %w", m.FileName(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return plan, fmt.Errorf("migrate: commit %s: %w", m.FileName(), err)
		}
		if r.Logf != nil {
			r.Logf("applied %s (%s) in %s", m.FileName(), m.Phase, time.Since(start).Round(time.Millisecond))
		}
	}
	return plan, nil
}

func (r *Runner) planOn(ctx context.Context, conn *pgx.Conn) (Plan, error) {
	all, err := migrations.All()
	if err != nil {
		return Plan{}, fmt.Errorf("migrate: load embedded migrations: %w", err)
	}
	compat, err := migrations.ReleaseCompatibility()
	if err != nil {
		return Plan{}, fmt.Errorf("migrate: read compatibility manifest: %w", err)
	}
	var current int
	if err := conn.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return Plan{}, fmt.Errorf("migrate: read current version: %w", err)
	}
	p := Plan{CurrentVersion: current, Compat: compat}
	if n := len(all); n > 0 {
		p.TargetVersion = all[n-1].Version
	}
	for _, m := range all {
		if m.Version > current {
			p.Pending = append(p.Pending, m)
		}
	}
	p.Compatible = compatible(current, compat)
	return p, nil
}

func (r *Runner) verifyChecksumsOn(ctx context.Context, conn *pgx.Conn) error {
	byVersion, err := embeddedByVersion()
	if err != nil {
		return err
	}
	rows, err := conn.Query(ctx, `SELECT version, name, phase, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("migrate: read ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		var name, phase, sum string
		if err := rows.Scan(&v, &name, &phase, &sum); err != nil {
			return fmt.Errorf("migrate: scan ledger: %w", err)
		}
		if err := verifyIdentity(byVersion, v, name, phase, sum); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Status is the machine-readable state `securityctl` prints.
type Status struct {
	CurrentVersion int                      `json:"currentVersion"`
	TargetVersion  int                      `json:"targetVersion"`
	Pending        []PendingStatus          `json:"pending"`
	Applied        []Applied                `json:"applied"`
	Compat         migrations.Compatibility `json:"compatibility"`
	Compatible     bool                     `json:"compatible"`
}

// PendingStatus is the preflight view of a database-s schema: what it is at
// now, what this build would do, and whether the two are compatible.
type PendingStatus struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	File    string `json:"file"`
}

// StatusReport builds the full report.
func (r *Runner) StatusReport(ctx context.Context) (Status, error) {
	if err := r.EnsureLedger(ctx); err != nil {
		return Status{}, err
	}
	plan, err := r.BuildPlan(ctx)
	if err != nil {
		return Status{}, err
	}
	applied, err := r.Applied(ctx)
	if err != nil {
		return Status{}, err
	}
	s := Status{
		CurrentVersion: plan.CurrentVersion,
		TargetVersion:  plan.TargetVersion,
		Applied:        applied,
		Compat:         plan.Compat,
		Compatible:     plan.Compatible,
	}
	for _, m := range plan.Pending {
		s.Pending = append(s.Pending, PendingStatus{
			Version: m.Version, Name: m.Name, Phase: string(m.Phase), File: m.FileName(),
		})
	}
	return s, nil
}
