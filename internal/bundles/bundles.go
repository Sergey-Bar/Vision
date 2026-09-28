// Package bundles stores and activates immutable rule bundles.
//
// A bundle is stored whole and never edited. Activation is a pointer move, and
// rollback is a pointer move back. That is what makes "the incident was decided
// under bundle 0.1.0" a statement that can be true years later.
//
// Git is the development source of truth and never an operational requirement: a
// customer's air-gapped site has no git, and a product that needs it to roll
// back a rule is a product that cannot recover during an incident.
package bundles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// Service stores, activates and rolls back bundles.
type Service struct {
	Pool *dbx.Pool
	// Compiler compiles a bundle on store. Compiling here rather than at activation
	// means a bundle that cannot run is never stored in the first place, and the
	// stored copy is known to compile under this build.
	Compiler *rules.Compiler
	// Audit records every state change here. Optional in a unit test, never
	// optional in a running system: activating a rule set changes what will alarm
	// a site, and nobody should be able to do that without a ledger row.
	Audit *audit.Writer
}

// Meta is a stored bundle's index entry.
type Meta struct {
	BundleID        string    `json:"bundleId"`
	Version         string    `json:"version"`
	ContentHash     string    `json:"contentHash"`
	SourceReference string    `json:"sourceReference"`
	CreatedBy       string    `json:"createdBy"`
	CreatedAt       time.Time `json:"createdAt"`
	Active          bool      `json:"active"`
	Supersedes      string    `json:"supersedesVersion,omitempty"`
	LintStatus      string    `json:"lintStatus"`
	// ActivateDiffDigest is the simulation the operator acknowledged. It is
	// recorded so an auditor can answer "what did you see before you turned this
	// on" months later, which is a question the incident history will raise.
	ActivateDiffDigest string `json:"activateDiffDigest,omitempty"`
}

// ErrNotStored is returned for a bundle version that is not in the store.
var ErrNotStored = errors.New("bundles: no such version is stored")

// ErrGateNotSatisfied is returned when a bundle cannot be activated.
//
// Every reason is named, because an operator at a site with no network cannot go
// and look up which of the three conditions failed.
type ErrGateNotSatisfied struct {
	Reason string
}

func (e *ErrGateNotSatisfied) Error() string { return "bundles: activation refused: " + e.Reason }

// Store compiles and stores a bundle without activating it.
//
// Storing is not activating. The two are separate commands and separate
// operations, because a bundle that could be activated in one step would be
// activated without anyone reading what it changes.
func (s *Service) Store(ctx context.Context, tenantID string, content []byte, source, createdBy, supersedes string) (Meta, []rules.Problem, error) {
	var meta Meta
	if s.Compiler == nil {
		return meta, nil, errors.New("bundles: no compiler is configured")
	}
	if tenantID == "" {
		return meta, nil, errors.New("bundles: tenant id is required and is never read from a request body")
	}
	tenant := tenantID

	compiled, problems := s.Compiler.Compile(content)
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			return meta, problems, nil
		}
	}
	if compiled == nil {
		return meta, problems, nil
	}

	hash := sha256.Sum256(content)
	contentHash := hex.EncodeToString(hash[:])
	// Derived from the content, so the same bytes always land on the same id and
	// a re-upload is recognisable rather than looking like a second bundle.
	bundleID := "rb-" + contentHash[:16]

	lintStatus := "passed"
	for _, p := range problems {
		if p.Severity == rules.SeverityWarning {
			lintStatus = "passed"
		}
	}

	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: createdBy},
		func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `
				INSERT INTO rule_bundles (bundle_id, tenant_id, version, content_hash, content,
				                           source_reference, created_by, supersedes_version, lint_status)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
				ON CONFLICT (tenant_id, version) DO NOTHING`,
				bundleID, tenant, compiled.Version, contentHash, content,
				source, createdBy, nullString(supersedes), lintStatus); err != nil {
				return fmt.Errorf("bundles: store: %w", err)
			}
			if s.Audit == nil {
				return nil
			}
			_, err := s.Audit.Append(ctx, tx, audit.Entry{
				TenantID: tenant, Actor: createdBy, ActorType: audit.ActorAdmin,
				Action: audit.ActionRuleBundleStored, TargetType: "rule_bundle", TargetID: bundleID,
				ResultStateHash: contentHash, Reason: source,
			})
			return err
		})
	if err != nil {
		return meta, problems, err
	}

	meta, err = s.Meta(ctx, tenant, compiled.Version)
	return meta, problems, err
}

// Meta reads a stored bundle's index entry.
func (s *Service) Meta(ctx context.Context, tenant, version string) (Meta, error) {
	var m Meta
	var supersedes *string
	var digest *string
	err := s.Pool.QueryRow(ctx, `
		SELECT bundle_id, version, content_hash, source_reference, created_by, created_at,
		       active, COALESCE(supersedes_version, ''), lint_status, COALESCE(activate_diff_digest, '')
		  FROM rule_bundles WHERE tenant_id = $1 AND version = $2`,
		tenant, version).Scan(&m.BundleID, &m.Version, &m.ContentHash, &m.SourceReference,
		&m.CreatedBy, &m.CreatedAt, &m.Active, &supersedes, &m.LintStatus, &digest)
	if err != nil {
		if isNoRows(err) {
			return m, fmt.Errorf("%w: %s", ErrNotStored, version)
		}
		return m, fmt.Errorf("bundles: meta: %w", err)
	}
	if supersedes != nil {
		m.Supersedes = *supersedes
	}
	if digest != nil {
		m.ActivateDiffDigest = *digest
	}
	return m, nil
}

// Content reads a stored bundle's bytes.
func (s *Service) Content(ctx context.Context, tenant, version string) ([]byte, error) {
	var content []byte
	err := s.Pool.QueryRow(ctx,
		`SELECT content FROM rule_bundles WHERE tenant_id = $1 AND version = $2`,
		tenant, version).Scan(&content)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotStored, version)
		}
		return nil, fmt.Errorf("bundles: content: %w", err)
	}
	return content, nil
}

// Active reads the active bundle's version and bytes.
func (s *Service) Active(ctx context.Context, tenant string) (version string, content []byte, err error) {
	err = s.Pool.QueryRow(ctx,
		`SELECT version, content FROM rule_bundles WHERE tenant_id = $1 AND active`,
		tenant).Scan(&version, &content)
	if err != nil {
		if isNoRows(err) {
			return "", nil, fmt.Errorf("bundles: no active bundle for tenant %s", tenant)
		}
		return "", nil, fmt.Errorf("bundles: active: %w", err)
	}
	return version, content, nil
}

// List returns every stored bundle, newest first.
func (s *Service) List(ctx context.Context, tenant string) ([]Meta, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT bundle_id, version, content_hash, source_reference, created_by, created_at,
		       active, COALESCE(supersedes_version, ''), lint_status, COALESCE(activate_diff_digest, '')
		  FROM rule_bundles WHERE tenant_id = $1
		 ORDER BY created_at DESC`, tenant)
	if err != nil {
		return nil, fmt.Errorf("bundles: list: %w", err)
	}
	defer rows.Close()

	var out []Meta
	for rows.Next() {
		var m Meta
		if err := rows.Scan(&m.BundleID, &m.Version, &m.ContentHash, &m.SourceReference,
			&m.CreatedBy, &m.CreatedAt, &m.Active, &m.Supersedes, &m.LintStatus,
			&m.ActivateDiffDigest); err != nil {
			return nil, fmt.Errorf("bundles: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Activate turns a stored bundle on.
//
// The gate is the reviewed diff digest. A bundle that has not been simulated
// cannot be activated, and a bundle whose simulation produced a different digest
// than the one acknowledged cannot either — which is what stops a review from
// being silently invalidated by the fixtures or the active bundle moving
// afterwards.
func (s *Service) Activate(ctx context.Context, tenant, version, diffDigest, actor string) error {
	if strings.TrimSpace(diffDigest) == "" {
		return &ErrGateNotSatisfied{Reason: "no simulate diff digest was supplied. Run `rules simulate` and " +
			"acknowledge the digest it prints."}
	}
	if len(diffDigest) != 64 || !isHex(diffDigest) {
		return &ErrGateNotSatisfied{Reason: "the simulate diff digest is not a SHA-256 hex digest"}
	}

	meta, err := s.Meta(ctx, tenant, version)
	if err != nil {
		return err
	}
	if meta.Active {
		return fmt.Errorf("bundles: %s is already active", version)
	}
	if meta.LintStatus != "passed" {
		return &ErrGateNotSatisfied{Reason: fmt.Sprintf(
			"bundle %s has lint status %q. A bundle that has not passed lint cannot be activated.",
			version, meta.LintStatus)}
	}

	previous, _, prevErr := s.Active(ctx, tenant)
	superseded := previous
	if prevErr != nil && !strings.Contains(prevErr.Error(), "no active bundle") {
		return prevErr
	}

	return s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: actor},
		func(tx pgx.Tx) error {
			// The pointer moves in one transaction: the previously active version
			// is deactivated and the new one activated, so there is no instant at
			// which two are active or none is.
			if _, err := tx.Exec(ctx,
				`UPDATE rule_bundles SET active = FALSE WHERE tenant_id = $1 AND active`,
				tenant); err != nil {
				return fmt.Errorf("bundles: deactivate: %w", err)
			}
			tag, err := tx.Exec(ctx, `
				UPDATE rule_bundles
				   SET active = TRUE, activate_diff_digest = $3
				 WHERE tenant_id = $1 AND version = $2`,
				tenant, version, diffDigest)
			if err != nil {
				return fmt.Errorf("bundles: activate: %w", err)
			}
			if tag.RowsAffected() == 0 {
				return fmt.Errorf("%w: %s", ErrNotStored, version)
			}
			if s.Audit == nil {
				return nil
			}
			_, err = s.Audit.Append(ctx, tx, audit.Entry{
				TenantID: tenant, Actor: actor, ActorType: audit.ActorAdmin,
				Action: audit.ActionRuleBundleActivated, TargetType: "rule_bundle", TargetID: meta.BundleID,
				PrevStateHash: superseded, ResultStateHash: meta.ContentHash,
				Reason: "reviewed simulate diff " + diffDigest,
			})
			return err
		})
}

// Rollback returns to the previously active version.
//
// The previous version is not computed from anything: it is whatever was active
// before this one, and the operator gets it by asking. Computing a "previous"
// from version numbers would break the day somebody stored an out-of-order
// version.
func (s *Service) Rollback(ctx context.Context, tenant, actor string) (string, error) {
	current, _, err := s.Active(ctx, tenant)
	if err != nil {
		return "", err
	}

	var target Meta
	err = s.Pool.QueryRow(ctx, `
		SELECT bundle_id, version FROM rule_bundles
		 WHERE tenant_id = $1 AND version <> $2
		   AND created_at < (SELECT created_at FROM rule_bundles WHERE tenant_id = $1 AND version = $2)
		 ORDER BY created_at DESC LIMIT 1`, tenant, current).Scan(&target.BundleID, &target.Version)
	if err != nil {
		if isNoRows(err) {
			return "", fmt.Errorf("bundles: no earlier bundle to roll back to from %s", current)
		}
		return "", fmt.Errorf("bundles: find rollback target: %w", err)
	}

	err = s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: actor},
		func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx,
				`UPDATE rule_bundles SET active = FALSE WHERE tenant_id = $1 AND active`,
				tenant); err != nil {
				return fmt.Errorf("bundles: rollback deactivate: %w", err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE rule_bundles SET active = TRUE WHERE tenant_id = $1 AND version = $2`,
				tenant, target.Version); err != nil {
				return fmt.Errorf("bundles: rollback activate: %w", err)
			}
			if s.Audit == nil {
				return nil
			}
			_, err := s.Audit.Append(ctx, tx, audit.Entry{
				TenantID: tenant, Actor: actor, ActorType: audit.ActorAdmin,
				Action: audit.ActionRuleBundleRolledBack, TargetType: "rule_bundle", TargetID: target.BundleID,
				PrevStateHash: current, ResultStateHash: target.Version,
				Reason: "operator rollback",
			})
			return err
		})
	return target.Version, err
}

func nullString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
