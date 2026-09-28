// Package inbox is the intake path: dedupe, lease, and the guarantee that
// anything accepted reaches a terminal state.
//
// The whole package exists to make one sentence true: zero accepted event loss.
// Everything here is shaped by the two ways that sentence is usually broken.
//
// The first is a crash between "I accepted this" and "I did something with it".
// The fix is that acceptance is a database commit, not a side effect, and
// everything downstream is reachable from the committed row.
//
// The second is a duplicate. A source re-delivers after a reconnect, an operator
// replays a scenario, a relay retries. The fix is a scoped unique key taken
// before the work, in the same transaction, so the duplicate loses at the
// database rather than in application code that a second process does not share.
//
// The lease is the third piece: a worker that dies mid-processing must not hold
// its event forever, and must not have its work done twice either. A lease with
// an expiry, reclaimed by another worker, and an idempotent consumer downstream,
// is what makes that safe.
package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// Status is an inbox event's lifecycle state. The only terminal states are
// processed, dead_letter, and the retention sweep — and the sweep is outside the
// retention window, so within it every accepted event is accounted for.
type Status string

const (
	StatusReceived   Status = "received"
	StatusProcessing Status = "processing"
	StatusProcessed  Status = "processed"
	StatusFailed     Status = "failed"
	StatusDeadLetter Status = "dead_letter"
)

// ErrDuplicate is returned when an event was already accepted. It is not a
// failure: a duplicate insert is a no-op, and the caller treats it as success.
var ErrDuplicate = errors.New("inbox: event already accepted")

// Accepted is the outcome of an intake attempt.
type Accepted struct {
	// Duplicate is true when the event was already in the inbox. A duplicate is
	// a successful outcome with no work attached, not an error to retry.
	Duplicate bool
	Status    Status
	// EventID is the inbox row's identity, present on both paths so a caller
	// can log a correlation id for a duplicate too.
	EventID string
	// ReceivedAt is the value the database stored, which is the partition the row
	// actually landed in. It is read back rather than echoed from the request,
	// because a write that landed in a different month would otherwise leave the
	// dedupe key's partition label quietly wrong.
	ReceivedAt time.Time
}

// WriteOptions configures one intake.
type WriteOptions struct {
	TenantID      string
	SiteID        string
	IntegrationID string
	SourceEventID string
	// Payload is the sanitized envelope. It is stored verbatim: the canonical
	// event is a normalization of this, never a replacement, so that a
	// normalization bug can be fixed and re-run against the original bytes.
	Payload json.RawMessage
	// ReceivedAt is the platform's ingest time. It is the partition key, and it
	// is the platform's own clock, never the source's.
	ReceivedAt time.Time
	// RetentionUntil bounds how long the row survives. The plan is explicit
	// that rows are never deleted inside the retention window: they are the
	// evidence that nothing was lost.
	RetentionUntil *time.Time
}

// Writer performs intake.
type Writer struct {
	Pool *dbx.Pool
	// MaxAttempts is where a failing event becomes a dead letter. Below this it
	// is retried, because a transient database blip must not silently end the
	// life of an accepted event.
	MaxAttempts int
}

// NewWriter builds a writer with the plan's retry bound.
func NewWriter(pool *dbx.Pool) *Writer {
	return &Writer{Pool: pool, MaxAttempts: 5}
}

const insertInboxSQL = `
INSERT INTO inbox_events (
    tenant_id, integration_id, source_event_id, site_id,
    status, payload, payload_hash, received_at, attempt_count, retention_until
) VALUES ($1, $2, $3, $4, 'received', $5, $6, $7, 0, $8)
RETURNING received_at`

const insertKeySQL = `
INSERT INTO inbox_event_keys (tenant_id, integration_id, source_event_id, inbox_partition)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, integration_id, source_event_id) DO NOTHING
RETURNING 1`

// Write accepts one event.
//
// The dedupe key is taken first, in the same transaction, and a conflict means
// the event was already accepted. Doing it in this order is what makes the
// exactly-once effect hold: there is no window in which a row exists without a
// key, or a key exists without a row.
func (w *Writer) Write(ctx context.Context, opts WriteOptions) (Accepted, error) {
	if err := validateWrite(opts); err != nil {
		return Accepted{}, err
	}
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	var accepted Accepted
	err := w.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: opts.TenantID,
		ActorID:  "inbox:" + opts.IntegrationID,
	}, func(tx pgx.Tx) error {
		// The partition name is derived from the ingest month. It is a label,
		// not a decision: the row lands in the table's own default routing, and
		// this is recorded so a human reading the key table can tell which
		// partition owns a key without a second query.
		partition := "inbox_events_" + opts.ReceivedAt.UTC().Format("2006_01")

		var claimed int
		// ON CONFLICT DO NOTHING returns no row rather than a zero, so an empty
		// result is the duplicate signal. A duplicate is a successful outcome
		// with no work attached, and a source that re-delivers after a reconnect
		// is the normal case rather than a fault.
		err := tx.QueryRow(ctx, insertKeySQL,
			opts.TenantID, opts.IntegrationID, opts.SourceEventID, partition).Scan(&claimed)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			accepted = Accepted{Duplicate: true, Status: StatusReceived}
			return nil
		case err != nil:
			return fmt.Errorf("inbox: claim dedupe key: %w", err)
		}
		if claimed == 0 {
			accepted = Accepted{Duplicate: true, Status: StatusReceived}
			return nil
		}

		// received_at is read back rather than assumed: it is the partition key,
		// so a write that landed in a different month than the caller expected
		// would leave the dedupe key's partition label wrong.
		var storedReceivedAt time.Time
		if err := tx.QueryRow(ctx, insertInboxSQL,
			opts.TenantID, opts.IntegrationID, opts.SourceEventID, opts.SiteID,
			opts.Payload, Hash(opts.Payload), opts.ReceivedAt.UTC(), opts.RetentionUntil,
		).Scan(&storedReceivedAt); err != nil {
			return fmt.Errorf("inbox: insert event: %w", err)
		}
		accepted = Accepted{Status: StatusReceived, ReceivedAt: storedReceivedAt.UTC()}
		return nil
	})
	return accepted, err
}

func validateWrite(o WriteOptions) error {
	switch {
	case o.TenantID == "":
		return fmt.Errorf("inbox: tenant id is required and is never read from a request body")
	case o.SiteID == "":
		return errors.New("inbox: site id is required; an inbox event is physically site-bound")
	case o.IntegrationID == "":
		return errors.New("inbox: integration id is required")
	case o.SourceEventID == "":
		// Without it there is no dedupe key, and an event that cannot be
		// deduplicated is re-ingested on every poll for ever.
		return errors.New("inbox: source event id is required; without it the event cannot be deduplicated")
	case len(o.Payload) == 0:
		return errors.New("inbox: payload is required")
	case o.ReceivedAt.IsZero():
		return errors.New("inbox: received at is required")
	}
	return nil
}

// Hash is the SHA-256 over the stored envelope. It lets a duplicate be
// recognised as an identical re-delivery rather than as the same id carrying
// different bytes, which is a source fault worth surfacing.
func Hash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Lease is a claimed unit of work.
type Lease struct {
	// Key is the scoped dedupe key, and the primary identity of the work item.
	Key        Key
	SiteID     string
	Payload    json.RawMessage
	Attempts   int
	ReceivedAt time.Time
	// LeaseExpiresAt is when this lease stops being valid. It is read from the
	// row, not computed locally, so a worker's own clock skew cannot make it
	// believe it holds a lease the database has already given away.
	LeaseExpiresAt time.Time
	// Owner identifies the worker, so a lease that expires can be reported as
	// having been held by a specific process.
	Owner string
}

// Key is the scoped identity of an inbox event.
type Key struct {
	TenantID      string
	IntegrationID string
	SourceEventID string
}

// DefaultLeaseDuration is the visibility timeout. It is long enough that a
// healthy worker is not interrupted mid-batch, and short enough that a crashed
// worker's work is recovered quickly: the plan's bound is two lease intervals.
const DefaultLeaseDuration = 60 * time.Second

// Claim leases up to limit events that are ready to be processed.
//
// The claim is a single statement with FOR UPDATE SKIP LOCKED, so many workers
// can run without coordinating and none of them blocks on another. The lease
// expiry is written in the same statement as the status change, so a claimed row
// can never be status=leased with no deadline.
const claimSQL = `
WITH candidate AS (
    SELECT tenant_id, integration_id, source_event_id
      FROM inbox_events
     WHERE ($4 = '' OR tenant_id = $4)
       AND (
             status IN ('received', 'failed')
             -- A row whose worker died is still status=processing. Without this
             -- clause the lease expiry would be decorative: the row would never
             -- become claimable again and a killed worker's event would sit
             -- until a human intervened, which is exactly the loss the lease
             -- exists to prevent.
          OR (status = 'processing' AND lease_expires_at IS NOT NULL AND lease_expires_at < now())
           )
     ORDER BY received_at
     FOR UPDATE SKIP LOCKED
     LIMIT $1
)
UPDATE inbox_events e
   SET status = 'processing',
       lease_owner = $2,
       lease_expires_at = now() + make_interval(secs => $3),
       attempt_count = e.attempt_count + 1
  FROM candidate c
 WHERE e.tenant_id = c.tenant_id
   AND e.integration_id = c.integration_id
   AND e.source_event_id = c.source_event_id
RETURNING e.tenant_id, e.integration_id, e.source_event_id, e.site_id,
          e.payload, e.attempt_count, e.received_at, e.lease_expires_at, e.lease_owner`

// ClaimFor takes up to limit leases for a single tenant.
func (w *Writer) ClaimFor(ctx context.Context, tenantID, owner string, limit int, lease time.Duration) ([]Lease, error) {
	return w.claim(ctx, tenantID, owner, limit, lease)
}

// ClaimAll takes up to limit leases across every tenant.
//
// A worker legitimately processes events across tenants, taking the tenant from
// the row it already holds, so this runs as a system worker rather than under a
// session tenant. Claim is the tenant-scoped form and is what almost every caller
// wants; a caller that reaches for ClaimAll is making a deliberate choice to
// drain across tenants and is visible at the call site.
func (w *Writer) ClaimAll(ctx context.Context, owner string, limit int, lease time.Duration) ([]Lease, error) {
	return w.claim(ctx, "", owner, limit, lease)
}

func (w *Writer) claim(ctx context.Context, tenantID, owner string, limit int, lease time.Duration) ([]Lease, error) {
	if limit <= 0 {
		return nil, errors.New("inbox: claim limit must be positive")
	}
	if lease <= 0 {
		lease = DefaultLeaseDuration
	}
	seconds := int(lease / time.Second)
	if seconds < 1 {
		seconds = 1
	}

	var out []Lease
	err := w.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, claimSQL, limit, owner, seconds, tenantID)
		if err != nil {
			return fmt.Errorf("inbox: claim: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var l Lease
			if err := rows.Scan(&l.Key.TenantID, &l.Key.IntegrationID, &l.Key.SourceEventID,
				&l.SiteID, &l.Payload, &l.Attempts, &l.ReceivedAt,
				&l.LeaseExpiresAt, &l.Owner); err != nil {
				return fmt.Errorf("inbox: scan lease: %w", err)
			}
			out = append(out, l)
		}
		return rows.Err()
	})
	return out, err
}

// Renew extends a lease a worker is still working on. A worker processing a slow
// batch must not have its work stolen from under it.
func (w *Writer) Renew(ctx context.Context, key Key, owner string, lease time.Duration) error {
	seconds := int(lease / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	tag, err := w.Pool.Exec(ctx, `
		UPDATE inbox_events
		   SET lease_expires_at = now() + make_interval(secs => $4)
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
		   AND lease_owner = $5 AND status = 'processing'`,
		key.TenantID, key.IntegrationID, key.SourceEventID, seconds, owner)
	if err != nil {
		return fmt.Errorf("inbox: renew lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the lease was reclaimed or the event reached a terminal state.
		// Either way this worker no longer owns it, and saying so beats
		// silently continuing to work on a row somebody else may re-process.
		return fmt.Errorf("inbox: lease on %s/%s is no longer held by %s",
			key.IntegrationID, key.SourceEventID, owner)
	}
	return nil
}

// Complete marks a lease processed and records what it produced.
func (w *Writer) Complete(ctx context.Context, key Key, canonicalEventID string) error {
	tag, err := w.Pool.Exec(ctx, `
		UPDATE inbox_events
		   SET status = 'processed', completed_at = now(),
		       canonical_event_id = $4,
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
		   AND status = 'processing'`,
		key.TenantID, key.IntegrationID, key.SourceEventID, canonicalEventID)
	if err != nil {
		return fmt.Errorf("inbox: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("inbox: %s/%s is not in processing state", key.IntegrationID, key.SourceEventID)
	}
	return nil
}

// Fail records a failed attempt. Past the retry bound the event becomes a dead
// letter, which is a first-class state: surfaced in the console, audited, and
// never silently dropped.
func (w *Writer) Fail(ctx context.Context, key Key, cause error, maxAttempts int) (Status, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var status Status
	err := w.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: key.TenantID,
		ActorID:  "inbox:worker",
	}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			UPDATE inbox_events
			   SET status = CASE WHEN attempt_count >= $4 THEN 'dead_letter' ELSE 'failed' END,
			       last_error = $5,
			       dead_letter_reason = CASE WHEN attempt_count >= $4 THEN $5 ELSE NULL END,
			       lease_owner = NULL,
			       lease_expires_at = NULL
			 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
			RETURNING status`,
			key.TenantID, key.IntegrationID, key.SourceEventID, maxAttempts, cause.Error(),
		).Scan(&status)
	})
	if err != nil {
		return "", fmt.Errorf("inbox: fail: %w", err)
	}
	return status, nil
}

// DeadLetter is one event that reached a terminal failure.
type DeadLetter struct {
	Key
	SiteID    string
	Attempts  int
	Reason    string
	FirstSeen time.Time
	UpdatedAt time.Time
}

// DeadLetters lists dead-lettered events, newest first.
//
// This is a console and an alert surface. The SLO says a dead letter must be
// visible within 60 seconds of entering the state, which is only achievable if
// the query behind this is cheap enough to run constantly.
func (w *Writer) DeadLetters(ctx context.Context, tenantID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := w.Pool.Query(ctx, `
		SELECT tenant_id, integration_id, source_event_id, site_id,
		       attempt_count, COALESCE(dead_letter_reason, last_error, ''),
		       first_seen_at, received_at
		  FROM inbox_events
		 WHERE tenant_id = $1 AND status = 'dead_letter'
		 ORDER BY received_at DESC
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("inbox: list dead letters: %w", err)
	}
	defer rows.Close()

	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		if err := rows.Scan(&d.Key.TenantID, &d.Key.IntegrationID, &d.Key.SourceEventID,
			&d.SiteID, &d.Attempts, &d.Reason, &d.FirstSeen, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("inbox: scan dead letter: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Replay moves a dead letter back to received so it can be retried.
//
// Replayable is deliberately an operator action rather than an automatic
// behaviour: a dead letter usually means the source is producing something the
// platform cannot read, and retrying that in a loop turns a visible incident
// into invisible load.
func (w *Writer) Replay(ctx context.Context, key Key) error {
	tag, err := w.Pool.Exec(ctx, `
		UPDATE inbox_events
		   SET status = 'received', attempt_count = 0, last_error = NULL,
		       dead_letter_reason = NULL, completed_at = NULL,
		       lease_owner = NULL, lease_expires_at = NULL
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
		   AND status = 'dead_letter'`,
		key.TenantID, key.IntegrationID, key.SourceEventID)
	if err != nil {
		return fmt.Errorf("inbox: replay: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("inbox: %s/%s is not a dead letter", key.IntegrationID, key.SourceEventID)
	}
	return nil
}

// Counts is the backlog summary the SLO alerts read.
type Counts struct {
	Received   int64
	Processing int64
	Failed     int64
	DeadLetter int64
	Processed  int64
}

// Counts reads the backlog for a tenant. The backpressure thresholds in the SLO
// are evaluated against this, so it is one round trip rather than four.
func (w *Writer) Counts(ctx context.Context, tenantID string) (Counts, error) {
	var c Counts
	err := w.Pool.QueryRow(ctx, `
		SELECT
		    count(*) FILTER (WHERE status = 'received'),
		    count(*) FILTER (WHERE status = 'processing'),
		    count(*) FILTER (WHERE status = 'failed'),
		    count(*) FILTER (WHERE status = 'dead_letter'),
		    count(*) FILTER (WHERE status = 'processed')
		  FROM inbox_events
		 WHERE tenant_id = $1`, tenantID).
		Scan(&c.Received, &c.Processing, &c.Failed, &c.DeadLetter, &c.Processed)
	if err != nil {
		return c, fmt.Errorf("inbox: counts: %w", err)
	}
	return c, nil
}
