// Package audit writes the append-only, hash-chained ledger.
//
// The hash chain is computed by the database, in a trigger, over a
// length-prefixed canonical encoding. That choice is the whole design:
//
//   - If the application supplied prev_hash and hash, the chain would only ever
//     mean "whatever the last writer believed", and one buggy writer would
//     break every row it touched.
//   - Length prefixing removes field-boundary ambiguity. A '|'-joined encoding
//     lets actor "a|b" collide with actor "a" and action "b", so two legitimate
//     rows could be made to share one digest. With a length in front of every
//     field there is exactly one byte string per row.
//
// This package therefore owns the Entry shape, the semantic validation that must
// happen before a row is written, and the verification and sealing APIs. It
// never computes a chain hash itself.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/observability"
)

// ActorType enumerates who acted. A principal is either a human with a role, or
// a machine with a scope, and the distinction is recorded on every row because
// "the operator did it" and "the adapter did it" are different facts.
type ActorType string

// Actor types. A human with a role and a machine with a scope are different
// facts, and every ledger row records which one acted.
const (
	ActorOperator  ActorType = "operator"
	ActorAdmin     ActorType = "admin"
	ActorService   ActorType = "service"
	ActorAdapter   ActorType = "adapter"
	ActorAIRuntime ActorType = "ai_runtime"
	ActorSystem    ActorType = "system"
)

// Action names a state transition. The vocabulary is open; what is not open is
// that every auth event, every disposition, every bundle activation and every
// dead letter has one.
const (
	ActionLoginAttempt        = "auth.login.attempt"
	ActionLoginSuccess        = "auth.login.success"
	ActionLoginFailed         = "auth.login.failed"
	ActionLoginLocked         = "auth.login.locked"
	ActionBreakGlassLogin     = "auth.login.break_glass"
	ActionLogout              = "auth.logout"
	ActionStepUpMFASucceeded  = "auth.stepup.succeeded"
	ActionSessionRevoked      = "auth.session.revoked"
	ActionRecoveryPerformed   = "identity.recovery.performed"
	ActionBootstrapAdmin      = "identity.bootstrap_admin"
	ActionRoleGranted         = "identity.role.granted"
	ActionRoleRevoked         = "identity.role.revoked"
	ActionDispositionRecorded = "incident.disposition.recorded"
	ActionIncidentCreated     = "incident.created"
	ActionEvidenceFetched     = "evidence.fetched"
	// ActionEvidenceExported used to be declared here as "evidence.exported" and
	// was appended to by nothing, ever. Its capability (evidence.export) was
	// granted to two roles and enforced by no route, so there was no code path
	// that could reach this row — and a ledger action nobody can write is a
	// second thing that claims a capability exists. It was removed with the
	// capability rather than left as a placeholder for a feature nobody specified.
	// ActionEvidenceFetched is the row that records evidence bytes leaving.
	ActionRuleBundleStored     = "rules.bundle.stored"
	ActionRuleBundleActivated  = "rules.bundle.activated"
	ActionRuleBundleRolledBack = "rules.bundle.rolled_back"
	ActionCheckpointSealed     = "audit.checkpoint.sealed"
	ActionCheckpointFailed     = "audit.checkpoint.failed"
	ActionInboxDeadLettered    = "durability.inbox.dead_lettered"
	ActionJobDeadLettered      = "durability.job.dead_lettered"
	ActionAIDegraded           = "ai.observation.degraded"
	ActionAIObserved           = "ai.observation.recorded"
)

// ErrIncompleteEntry is returned when an entry is missing a field that must
// never be absent. Rejecting the write is better than writing a ledger row that
// a month of auditing cannot interpret.
var ErrIncompleteEntry = errors.New("audit: entry is incomplete")

// Entry is one row of the ledger. tenant_id is set from the session context,
// never from a caller-supplied body.
type Entry struct {
	TenantID        string
	Sequence        int64
	TS              time.Time
	Actor           string
	ActorType       ActorType
	Action          string
	TargetType      string
	TargetID        string
	PrevStateHash   string
	ResultStateHash string
	PolicyVersion   string
	Reason          string
	EvidenceRefs    []string
	RequestID       string
	SessionID       string
	SiteScope       []string
}

// Validate enforces the minimum an auditor needs in order to read the row six
// months later, with no other table to consult.
func (e *Entry) Validate() error {
	switch {
	case e.TenantID == "":
		return fmt.Errorf("%w: tenant_id is required and is never taken from a request body", ErrIncompleteEntry)
	case e.Actor == "":
		return fmt.Errorf("%w: actor is required", ErrIncompleteEntry)
	case e.ActorType == "":
		return fmt.Errorf("%w: actor_type is required", ErrIncompleteEntry)
	case e.Action == "":
		return fmt.Errorf("%w: action is required", ErrIncompleteEntry)
	case e.TS.IsZero():
		return fmt.Errorf("%w: ts is required", ErrIncompleteEntry)
	}
	switch e.ActorType {
	case ActorOperator, ActorAdmin, ActorService, ActorAdapter, ActorAIRuntime, ActorSystem:
	default:
		return fmt.Errorf("%w: unknown actor_type %q", ErrIncompleteEntry, e.ActorType)
	}
	return nil
}

// Writer appends to the ledger.
type Writer struct {
	Pool *dbx.Pool
	// Now is injectable so a test can pin the timestamp that the chain covers.
	// The chain includes ts, so a clock that jumps backwards mid-run is
	// visible to anyone verifying the ledger afterwards.
	Now func() time.Time
	// Metrics is optional and nil-tolerant.
	//
	// It exists because `audit_appends_total` was declared, the SLO dashboard was
	// built against it, and no code path anywhere in the tree incremented it. The
	// number was free: `Append` is the only way a row reaches the ledger, and it
	// already returns the row's hash. One field and one line.
	//
	// Nil-tolerant because an audit writer is constructed in tests, in migrations
	// and in a rollback path, and a metrics counter is not a reason for any of them
	// to have to know about a registry. A writer with no registry counts nothing
	// rather than refusing to append, because refusing to append would turn a
	// cosmetic dependency into an availability one.
	Metrics *observability.Registry
}

func (w *Writer) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now().UTC()
}

const insertSQL = `
INSERT INTO audit_ledger (
    tenant_id, sequence, ts, actor, actor_type, action, target_type, target_id,
    prev_state_hash, result_state_hash, policy_version, reason, evidence_refs,
    request_id, session_id, site_scope
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING prev_hash, hash`

// Append writes one entry inside the caller's transaction, so that the fact
// being audited and the ledger row that records it commit or fail together. An
// audited action that is not recorded because the transaction rolled back did
// not happen; a recorded action whose state change rolled back would be a lie.
func (w *Writer) Append(ctx context.Context, tx pgx.Tx, e Entry) (string, error) {
	// Default the timestamp before validating, not after. Validation is what
	// catches an entry nobody can attribute later, and a caller that did not set
	// a timestamp has not made the entry unattributable - it has simply left the
	// field for the writer that is about to record it.
	if e.TS.IsZero() {
		e.TS = w.now()
	}
	if err := e.Validate(); err != nil {
		return "", err
	}
	evidence, err := jsonArray(e.EvidenceRefs)
	if err != nil {
		return "", fmt.Errorf("audit: evidence_refs: %w", err)
	}
	sites, err := jsonArray(e.SiteScope)
	if err != nil {
		return "", fmt.Errorf("audit: site_scope: %w", err)
	}

	seq, err := auditNextSequence(ctx, tx, e.TenantID)
	if err != nil {
		return "", err
	}

	var prevHash, hash string
	err = tx.QueryRow(ctx, insertSQL,
		e.TenantID, seq, e.TS.UTC(), e.Actor, string(e.ActorType), e.Action,
		orEmpty(e.TargetType), orEmpty(e.TargetID),
		// These two are genuinely nullable: an action that changed nothing has
		// neither a prior state nor a result state, and inventing one would put a
		// hash in the ledger that refers to nothing.
		nullable(e.PrevStateHash), nullable(e.ResultStateHash),
		// The rest are NOT NULL with a default. An empty string is the honest
		// value for "this field does not apply", and sending an explicit NULL
		// would defeat the default rather than use it.
		orEmpty(e.PolicyVersion), orEmpty(e.Reason),
		evidence, orEmpty(e.RequestID), orEmpty(e.SessionID), sites,
	).Scan(&prevHash, &hash)
	if err != nil {
		return "", fmt.Errorf("audit: append entry: %w", err)
	}
	// Counted here, after the row is in the CALLER's transaction and before that
	// transaction is committed -- which is the only place this can be counted
	// correctly and not counted on a rollback.
	//
	// The alternative, counting on commit, would need a transaction hook this
	// package has no business knowing about. The cost of counting here is that the
	// counter can read one high for a transaction that later rolls back, which is
	// the smaller error: an append counter that over-reports by one on a failed
	// transaction, versus an audit ledger whose row count disagrees with the
	// counter that is supposed to describe it.
	if w.Metrics != nil {
		w.Metrics.Counters.Inc(observability.CounterAuditAppends)
	}
	return hash, nil
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

func auditNextSequence(ctx context.Context, tx pgx.Tx, tenant string) (int64, error) {
	var seq int64
	// The advisory lock the trigger takes is per-tenant and transaction-scoped;
	// taking it here too means two concurrent appends cannot both compute the
	// same next sequence and race for the primary key.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenant); err != nil {
		return 0, fmt.Errorf("audit: lock tenant chain: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT audit_next_sequence($1)`, tenant).Scan(&seq); err != nil {
		return 0, fmt.Errorf("audit: next sequence: %w", err)
	}
	return seq, nil
}

func jsonArray(v []string) (string, error) {
	if v == nil {
		v = []string{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Verification is the result of recomputing a chain.
type Verification struct {
	Valid        bool
	FromSequence int64
	ToSequence   int64
	// BrokenAt is zero when the chain holds.
	BrokenAt int64
	Checked  int64
	Digest   string
}

// Verify recomputes the hash chain for a tenant from the genesis row.
//
// This is the check that makes the ledger worth something. It is run on every
// checkpoint seal, and by `securityctl audit verify` on demand. A single edited
// row anywhere inside a sealed range changes the digest at that row, and every
// row after it stops chaining, so tampering is both detectable and localizable.
func (w *Writer) Verify(ctx context.Context, tx pgx.Tx, tenant string, from int64) (Verification, error) {
	var v Verification
	// broken_at is NULL when the chain holds, and PL/pgSQL reports a
	// RETURNS TABLE column as not-null in the row description.
	var brokenAt *int64
	err := tx.QueryRow(ctx,
		`SELECT valid, from_sequence, to_sequence, broken_at, checked
		   FROM audit_verify_chain($1, $2)`, tenant, from).
		Scan(&v.Valid, &v.FromSequence, &v.ToSequence, &brokenAt, &v.Checked)
	if err != nil {
		return v, fmt.Errorf("audit: verify chain: %w", err)
	}
	if brokenAt != nil {
		v.BrokenAt = *brokenAt
	}
	return v, nil
}
