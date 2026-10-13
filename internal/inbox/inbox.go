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

	"github.com/Sergey-Bar/vision/internal/dbx"
)

// Status is an inbox event's lifecycle state. The only terminal states are
// processed, dead_letter, and the retention sweep - and the sweep is outside the
// retention window, so within it every accepted event is accounted for.
type Status string

// Inbox states. The only terminal states are processed and dead_letter; a row
// is never deleted inside the retention window, because it is the evidence
// that nothing was lost.
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
	// can log a correlation id for a duplicate too. It is the scoped natural key
	// rendered as one value — tenant, integration and source event id — because
	// that triple is what identifies the row and no single column names it. It
	// was documented as present on both paths and was assigned on neither, so it
	// was always ""; a caller correlating a duplicate, which is the case where a
	// correlation id matters most, had nothing to correlate with.
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
-- The natural key is returned, not just the timestamp. Accepted.EventID promises
-- the row's identity on both paths, and the only honest way to fill it is to read
-- it back from the row the database just wrote rather than to echo the request:
-- the two are the same values today, and the day they are not, an echo is a lie
-- dressed as an identity.
RETURNING received_at, tenant_id, integration_id, source_event_id`

const insertKeySQL = `
INSERT INTO inbox_event_keys (tenant_id, integration_id, source_event_id, inbox_partition)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, integration_id, source_event_id) DO NOTHING
RETURNING 1`

// readExistingEventSQL reads the identity of a row that is already in the inbox.
//
// This exists because of a promise Accepted makes and did not keep. EventID is
// documented as "present on both paths so a caller can log a correlation id for a
// duplicate too", and it was assigned on neither: both duplicate returns built
// Accepted{Duplicate: true, Status: StatusReceived} and left it "". A caller
// correlating a duplicate — which is the case where a correlation id matters most,
// because the duplicate is the confusing one — had nothing to correlate with and
// no error telling it so.
//
// The lookup is on the scoped key, which is the table's primary key, so it is an
// index probe rather than a scan. Its received_at is returned as well, and for
// the same reason the insert reads it back: it is the partition the row landed in,
// and a duplicate is precisely the case where the caller cannot know it.
const readExistingEventSQL = `
SELECT received_at, tenant_id, integration_id, source_event_id
  FROM inbox_events
 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3`

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
	var accepted Accepted
	err := w.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: opts.TenantID,
		// app.site_ids is set, not just the tenant. site_isolation has been
		// RESTRICTIVE since migration 21, so the effective policy is tenant AND
		// site and a transaction that named only its tenant would be refused by the
		// WITH CHECK on the very insert it is here to make.
		SiteIDs: []string{opts.SiteID},
		ActorID: "inbox:" + opts.IntegrationID,
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
			return w.acceptedExisting(ctx, tx, opts, &accepted)
		case err != nil:
			return fmt.Errorf("inbox: claim dedupe key: %w", err)
		}
		if claimed == 0 {
			return w.acceptedExisting(ctx, tx, opts, &accepted)
		}

		// received_at and the natural key are read back rather than assumed:
		// received_at is the partition key, so a write that landed in a different
		// month than the caller expected would leave the dedupe key's partition
		// label wrong, and the key is the identity the caller was promised.
		var storedReceivedAt time.Time
		var id Key
		if err := tx.QueryRow(ctx, insertInboxSQL,
			opts.TenantID, opts.IntegrationID, opts.SourceEventID, opts.SiteID,
			opts.Payload, Hash(opts.Payload), opts.ReceivedAt.UTC(), opts.RetentionUntil,
		).Scan(&storedReceivedAt, &id.TenantID, &id.IntegrationID, &id.SourceEventID); err != nil {
			return fmt.Errorf("inbox: insert event: %w", err)
		}
		accepted = Accepted{
			Status:     StatusReceived,
			EventID:    id.String(),
			ReceivedAt: storedReceivedAt.UTC(),
		}
		return nil
	})
	return accepted, err
}

// acceptedExisting fills Accepted for an event that is already in the inbox.
//
// The row is read rather than reconstructed from the request. Reading it means
// the duplicate carries the identity and the ingest instant of the row that
// actually exists, which is the only version of those two facts an operator can
// use. A key row with no inbox row behind it would return no rows here, and that
// is a real state — the two writes are in one transaction, so it can only be a
// row that some other process is mid-transaction on, and the isolation level is
// what decides whether this sees it. It is reported as a fresh accept rather than
// as a duplicate with a blank identity, because the alternative is a duplicate
// carrying no identity at all, which is the defect being fixed.
func (w *Writer) acceptedExisting(ctx context.Context, tx pgx.Tx, opts WriteOptions, accepted *Accepted) error {
	var storedReceivedAt time.Time
	var id Key
	err := tx.QueryRow(ctx, readExistingEventSQL,
		opts.TenantID, opts.IntegrationID, opts.SourceEventID,
	).Scan(&storedReceivedAt, &id.TenantID, &id.IntegrationID, &id.SourceEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		*accepted = Accepted{Status: StatusReceived}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inbox: read the existing event: %w", err)
	}
	*accepted = Accepted{
		Duplicate:  true,
		Status:     StatusReceived,
		EventID:    id.String(),
		ReceivedAt: storedReceivedAt.UTC(),
	}
	return nil
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
	// SiteID is the site the event belongs to.
	//
	// It is part of the key's *scope* rather than its identity: the three fields
	// above name the row, and this says which site that row is. It is here because
	// every statement this package issues touches `inbox_events`, which is a
	// site-scoped table, and the RLS policy meant to enforce that is PERMISSIVE —
	// it is ORed away by `tenant_isolation`, so the site constraint does not exist
	// at the database layer at all. A writer with no site would rely entirely on
	// a policy that is not doing what its comment says.
	SiteID string
}

// String renders the key as one value, for a log line or a correlation id.
//
// The three fields are the row's identity and there is no single column that
// names it: inbox_events cannot carry a unique constraint on the triple, because
// it is range-partitioned on received_at and Postgres requires the partition key
// in every unique index on a partitioned table. That is why uniqueness lives on
// inbox_event_keys instead, and it is why the identity an operator or a log line
// needs is a composite one. The separator is a slash because none of the three
// components may contain one: an integration id is a configured slug and a source
// event id is whatever the vendor chose, and a rendered key that cannot be
// unambiguously split back into its parts is not an identity.
func (k Key) String() string {
	return k.TenantID + "/" + k.IntegrationID + "/" + k.SourceEventID
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
//
// A row that has already used its whole retry budget is not claimable. That
// guard is the second half of the terminal-state guarantee: Fail reaches
// dead_letter on the ordinary path, but a worker that is killed mid-handle never
// calls Fail, and without the guard such a row is reclaimed every lease period
// for the life of the process with no backoff and no terminal state. The
// preceding expireExhausted statement turns those into dead letters so they stop
// being claimable work rather than sitting in 'processing' for ever.
const claimSQL = `
WITH candidate AS (
    SELECT tenant_id, integration_id, source_event_id
      FROM inbox_events
     WHERE ($4 = '' OR tenant_id = $4)
       AND attempt_count < $5
       AND (
             -- A row that failed keeps lease_expires_at as its not-before time.
             -- The backoff is why a poisoned event costs a bounded number of
             -- attempts rather than a busy retry forever: 5, 10, 20 … 320
             -- seconds, then a dead letter.
             (status IN ('received', 'failed')
              AND (status = 'received' OR lease_expires_at IS NULL OR lease_expires_at <= now()))
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
   -- sqlguard:exempt lease - this is the claim. It grants the lease rather than
   -- writing under one, it runs under the system worker because a consume loop
   -- serves every tenant, and the rows it takes are pinned by FOR UPDATE SKIP
   -- LOCKED inside the same statement.
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

// expireExhaustedSQL turns rows that have burned their whole retry budget into
// dead letters. It runs inside the claim transaction, before the claim, so a
// worker that was killed mid-handle reaches a terminal state without any
// cooperating code ever calling Fail.
//
// A row that is currently being processed by a live worker is excluded: its
// lease has not expired, and dead-lettering under a live worker would duplicate
// or drop work that is in progress.
//
// The tenant predicate is not an optimisation, it is a requirement. This runs on
// every claim, and the consume loop claims on a timer, so an unfiltered version
// is a statement over every tenant's inbox on every tick. The status IN is
// carried by inbox_events_pending_idx, which is partial on exactly these three
// statuses, so the scan is bounded to unclaimed work and nothing else - but only
// once the tenant is pinned, because a site's consume loop has no business
// touching another site's rows and no business paying for their scan either.
const expireExhaustedSQL = `
-- sqlguard:exempt lease - this is the reclaim. A row whose worker died is still
-- status=processing with an expired lease and no live owner, and retiring it is
-- the entire purpose of this statement: the budget is spent and nothing is left
-- to hold the lease. It runs under the system worker because a claim must reach
-- every tenant, and it constrains itself to rows whose lease has actually
-- lapsed.
UPDATE inbox_events
   SET status = 'dead_letter',
       last_error = COALESCE(last_error, 'retries exhausted'),
       dead_letter_reason = COALESCE(dead_letter_reason, last_error,
                                     'retries exhausted without a recorded error'),
       lease_owner = NULL,
       lease_expires_at = NULL,
       completed_at = now()
 WHERE status IN ('received', 'failed', 'processing')
   AND attempt_count >= $1
   AND ($2 = '' OR tenant_id = $2)
   AND (status <> 'processing' OR lease_expires_at IS NULL OR lease_expires_at < now())`

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
	maxAttempts := w.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	var out []Lease
	err := w.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, expireExhaustedSQL, maxAttempts, tenantID); err != nil {
			return fmt.Errorf("inbox: expire exhausted events: %w", err)
		}
		rows, err := tx.Query(ctx, claimSQL, limit, owner, seconds, tenantID, maxAttempts)
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
			// The key carries the site as well as the lease.
			//
			// `site_isolation` is RESTRICTIVE as of migration 21, so a worker
			// acting on this lease and reading through `Key` alone would be
			// refused by a policy it cannot see. Every statement that mutates a
			// leased row scopes itself with `Key.SiteID`, and a key without one
			// scopes it with nothing. Populating both, rather than making every
			// consumer remember which of the two it holds, is the version where
			// the two cannot disagree.
			l.Key.SiteID = l.SiteID
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
	tag, err := w.Pool.WithSites(key.TenantID, "inbox", []string{key.SiteID}).Exec(ctx, `
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
//
// It matches on the lease owner, not only on the status, and that is the whole
// control. A worker whose lease expired can still be running - it has no way to
// know the lease lapsed until it tries to renew - and by the time it finishes,
// the row may have been reclaimed and be halfway through another worker's
// normalize. Matching on `status = 'processing'` alone lets the stale worker
// overwrite `canonical_event_id` with its own, stamp `completed_at`, and clear
// the `last_error` of a row that is still in flight. `normalize.completeInbox`
// gets this right and says why; this is the same statement on the other path.
func (w *Writer) Complete(ctx context.Context, key Key, owner, canonicalEventID string) error {
	tag, err := w.Pool.WithSites(key.TenantID, "inbox", []string{key.SiteID}).Exec(ctx, `
		UPDATE inbox_events
		   SET status = 'processed', completed_at = now(),
		       canonical_event_id = $5,
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
		   AND status = 'processing'
		   AND lease_owner IS NOT DISTINCT FROM $4`,
		key.TenantID, key.IntegrationID, key.SourceEventID, owner, canonicalEventID)
	if err != nil {
		return fmt.Errorf("inbox: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("inbox: %s/%s is not being processed by %s: the lease was reclaimed "+
			"or the event already reached a terminal state",
			key.IntegrationID, key.SourceEventID, owner)
	}
	return nil
}

// Fail records a failed attempt. Past the retry bound the event becomes a dead
// letter, which is a first-class state: surfaced in the console, audited, and
// never silently dropped.
//
// Below the bound it becomes 'failed' with a backoff. The backoff lives in
// lease_expires_at, which the claim reads as a not-before time for a failed row
// and as a lease deadline for a processing one — the column already means "when
// this row becomes claimable again", and the two readings do not conflict
// because a row is only ever in one status. The exponent is capped before the
// cast, so the expression cannot overflow at a large attempt_count; a raising
// backoff on the failure path is the one thing that turns a dead letter into a
// permanently leased row.
//
// The lease owner is matched, for the reason given on Complete and with the same
// consequence. The interleaving this closes is reachable: worker A holds the
// lease and is inside Normalize, the lease lapses, worker B claims the row and
// commits a successful normalize, and A's normalize then fails on something
// downstream. A's `Fail` matches unconditionally and flips a fully processed
// event to `failed` — or to `dead_letter`, where the operator's dead-letter
// console now lists an event whose canonical event, graph edges and incident all
// exist. Fail takes the owner for the same reason Complete does.
func (w *Writer) Fail(ctx context.Context, key Key, owner string, cause error, maxAttempts int) (Status, error) {
	if cause == nil {
		// Not a check on a caller that "should" pass an error: this is the failure
		// path, and `cause.Error()` below would panic while building the
		// arguments, before the query is sent.
		cause = errors.New("inbox: the failure had no cause recorded")
	}
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
			       -- completed_at is written on the transition to dead_letter and
			       -- only there. It used to not be written at all on this path, which
			       -- left a dead letter with no completion instant: the console's
			       -- dead-letter list orders by completed_at, and a NULL sorts first
			       -- under DESC, so every dead letter that came through Fail would
			       -- have headed the list regardless of when it died. The instant the
			       -- row entered its terminal state is what the list is ordered by and
			       -- what the 60-second visibility SLO is measured against, so it is
			       -- recorded here.
			       completed_at = CASE WHEN attempt_count >= $4 THEN now() ELSE completed_at END,
			       lease_owner = NULL,
			       lease_expires_at = CASE
			           WHEN attempt_count >= $4 THEN NULL
			           ELSE now() + make_interval(secs => (5 * power(2, least(attempt_count, 6)))::int)
			       END
			 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
			   AND status = 'processing'
			   AND lease_owner IS NOT DISTINCT FROM $6
			RETURNING status`,
			key.TenantID, key.IntegrationID, key.SourceEventID, maxAttempts, cause.Error(), owner,
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
	// UpdatedAt is when the event entered dead_letter — completed_at. It was
	// scanned from received_at, which is not an update time in any sense: it is
	// when the event arrived, and for an event that spent a month being retried
	// the two are a month apart. A console column labelled "updated" showing the
	// ingest instant tells an operator the age of the event rather than the age
	// of the incident.
	UpdatedAt time.Time
}

// DeadLetters lists dead-lettered events, newest first.
//
// This is a console and an alert surface. The SLO says a dead letter must be
// visible within 60 seconds of entering the state, which is only achievable if
// the query behind this is cheap enough to run constantly and orders by the
// thing the SLO is about.
//
// It orders by completed_at, the instant the row entered dead_letter. It used to
// order by received_at, which is ingest order: an event ingested today and
// dead-lettered after a month of retries sorted below one ingested yesterday and
// dead-lettered immediately, so the message that just died — the one the operator
// opened the page for — was not on it. That is not a presentation nit, it is the
// ordering that decides whether the 60-second visibility SLO is met.
// DeadLetters lists the terminal failures an operator has to act on.
//
// The site is a parameter rather than a default. Since migration 21
// site_isolation is RESTRICTIVE, so a tenant-scoped listing over a site-scoped
// table returns nothing -- and a dead-letter console that shows an operator an
// empty list because the reader could not see the rows is worse than one that is
// wrong, because it says the site is healthy.
func (w *Writer) DeadLetters(ctx context.Context, tenantID, siteID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := w.Pool.WithSites(tenantID, "inbox", []string{siteID}).Query(ctx, `
		SELECT tenant_id, integration_id, source_event_id, site_id,
		       attempt_count, COALESCE(dead_letter_reason, last_error, ''),
		       first_seen_at, completed_at
		  FROM inbox_events
		 WHERE tenant_id = $1 AND status = 'dead_letter'
		 -- NULLS LAST explicitly. Postgres puts NULLs first under DESC, so without
		 -- it a dead letter with no completion instant would sort above every real
		 -- one. Every path into the state now writes completed_at, which is why
		 -- there should be none; the clause is what makes a row that somehow has
		 -- one sort as unknown rather than as newest, which is the honest answer.
		 ORDER BY completed_at DESC NULLS LAST
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("inbox: list dead letters: %w", err)
	}
	defer rows.Close()

	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		// completed_at is nullable in the schema, and a dead-lettered row always
		// has it: Fail and the expireExhausted statement both write it, and they
		// are the only two ways into the state. COALESCE rather than a scan error
		// anyway, because a dead letter with no completion instant would take the
		// whole operator list down with it, and one unreadable row is a worse
		// reason to show nothing than showing the rest.
		var completedAt *time.Time
		if err := rows.Scan(&d.TenantID, &d.IntegrationID, &d.SourceEventID,
			&d.SiteID, &d.Attempts, &d.Reason, &d.FirstSeen, &completedAt); err != nil {
			return nil, fmt.Errorf("inbox: scan dead letter: %w", err)
		}
		if completedAt != nil {
			d.UpdatedAt = completedAt.UTC()
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
	tag, err := w.Pool.WithSites(key.TenantID, "inbox", []string{key.SiteID}).Exec(ctx, `
		UPDATE inbox_events
		   -- sqlguard:exempt lease - an operator replaying a dead letter. The row
		   -- is dead, so by definition no worker holds its lease; the point of the
		   -- action is to make it claimable again.
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
// Counts reads the backlog for one tenant.
//
// The query runs inside the tenant context, because inbox_events carries
// tenant_id and therefore row-level security. Without the context the database
// returns zero rows rather than an error, so the backlog would read as empty on
// every site and the backpressure alert would never fire — the SLO's own
// monitoring silently blind exactly when it is needed.
// Counts reports the inbox backlog for one site.
//
// The site is a parameter rather than an implicit "every site in the tenant":
// since migration 21 `site_isolation` is RESTRICTIVE, so a tenant-scoped count
// over a site-scoped table returns nothing. A backlog that reads zero because
// the reader could not see the rows is a backlog an operator would read as
// "the site is caught up".
func (w *Writer) Counts(ctx context.Context, tenantID, siteID string) (Counts, error) {
	var c Counts
	err := w.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: tenantID,
		SiteIDs:  []string{siteID},
		ActorID:  "inbox",
	},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT
				    count(*) FILTER (WHERE status = 'received'),
				    count(*) FILTER (WHERE status = 'processing'),
				    count(*) FILTER (WHERE status = 'failed'),
				    count(*) FILTER (WHERE status = 'dead_letter'),
				    count(*) FILTER (WHERE status = 'processed')
				  FROM inbox_events
				 WHERE tenant_id = $1`, tenantID).
				Scan(&c.Received, &c.Processing, &c.Failed, &c.DeadLetter, &c.Processed)
		})
	if err != nil {
		return c, fmt.Errorf("inbox: counts: %w", err)
	}
	return c, nil
}
