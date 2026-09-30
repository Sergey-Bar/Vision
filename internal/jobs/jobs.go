// Package jobs is the Postgres-backed work queue that replaces both Kafka and
// Redis in this product.
//
// The plan's reasoning for one table rather than two systems is worth keeping in
// view: the inbox gives exactly-once effect, the outbox gives atomic side
// effects, and this table gives retries and visibility timeouts. Four properties
// carry that:
//
//   - Claims use FOR UPDATE SKIP LOCKED, so many workers run without coordinating
//     and none blocks on another.
//   - Every job has an idempotency key unique per queue, so enqueueing the same
//     work twice is a no-op rather than a duplicate VLM inference.
//   - Every lease has an expiry, so a worker's crash is recovered rather than
//     waited out.
//   - dead_letter is a first-class state with a reason, surfaced in the console
//     and audited, never silently dropped.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// Queue names the work class. It is a discriminator, not a routing key: each
// queue has its own workers, its own lease duration and its own retry bound,
// because a VLM inference and a rule-bulk compile have nothing in common
// operationally.
type Queue string

// Work queues. Each has its own workers, lease duration and retry bound, because
// an inference and a rule-bulk compile have nothing in common operationally.
const (
	QueueVLM            Queue = "vlm"
	QueueEvidenceFetch  Queue = "evidence_fetch"
	QueueRuleBulk       Queue = "rule_bulk"
	QueueCheckpointSeal Queue = "checkpoint_seal"
)

// Status is a job's lifecycle.
type Status string

// Job states. done and dead_letter are terminal; dead_letter carries a reason and
// is surfaced in the console and audited.
const (
	StatusPending    Status = "pending"
	StatusLeased     Status = "leased"
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
	StatusDeadLetter Status = "dead_letter"
)

// ErrNotClaimed is returned when a lease is completed or renewed for a job the
// caller no longer holds. It is a distinct error because the correct response is
// to stop working on the job, not to retry.
var ErrNotClaimed = errors.New("jobs: job is not leased by this worker")

// Job is one unit of queued work.
type Job struct {
	ID             string
	TenantID       string
	SiteID         string
	Queue          Queue
	IdempotencyKey string
	Payload        json.RawMessage
	AttemptCount   int
	MaxAttempts    int
	RunAfter       time.Time
	LeaseOwner     string
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
}

// Enqueue describes one job to add.
type Enqueue struct {
	TenantID string
	SiteID   string
	Queue    Queue
	// IdempotencyKey is unique per queue. Required, because a duplicate enqueue
	// that silently creates a second job is a second VLM inference and a second
	// operator notification.
	IdempotencyKey string
	Payload        json.RawMessage
	MaxAttempts    int
	RunAfter       time.Time
}

// Enqueuer adds jobs inside a caller's transaction, so a job and the state
// change that caused it commit together.
type Enqueuer struct{ Pool *dbx.Pool }

// New builds an Enqueuer.
func New(pool *dbx.Pool) *Enqueuer { return &Enqueuer{Pool: pool} }

// Enqueue adds a job, or does nothing if the key already exists. It reports
// whether work was actually created.
func (e *Enqueuer) Enqueue(ctx context.Context, tx pgx.Tx, in Enqueue) (created bool, jobID string, err error) {
	switch {
	case in.TenantID == "":
		return false, "", errors.New("jobs: tenant id is required and is never read from a request body")
	case in.Queue == "":
		return false, "", errors.New("jobs: queue is required")
	case in.IdempotencyKey == "":
		return false, "", errors.New("jobs: idempotency key is required; a duplicate enqueue is a second inference")
	}
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 5
	}
	// The zero time is left alone so the column's own now() applies. See the
	// insert below for why the application clock is the wrong one here.
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO jobs (tenant_id, site_id, queue, idempotency_key, payload, max_attempts, run_after)
		VALUES ($1, $2, $3, $4, $5, $6,
		        -- The not-before time comes from the database, not from the
		        -- application. Every claim predicate compares against now() in
		        -- Postgres, so an unset run_after written from this host's clock
		        -- makes a job visible late or early by however far the two clocks
		        -- disagree, and on a site where they do a retry either never
		        -- becomes claimable or is claimable immediately. The column
		        -- already defaults to now(); this makes the intent explicit
		        -- rather than relying on a default three migrations away.
		        COALESCE($7::timestamptz, now()))
		ON CONFLICT (tenant_id, queue, idempotency_key) DO NOTHING
		RETURNING job_id::text`,
		in.TenantID, nullString(in.SiteID), string(in.Queue), in.IdempotencyKey,
		in.Payload, in.MaxAttempts, nullTime(in.RunAfter),
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("jobs: enqueue: %w", err)
	}
	return true, id, nil
}

// DefaultLeaseDuration is the visibility timeout for a general job.
const DefaultLeaseDuration = 60 * time.Second

// VLMLeaseDuration is longer, because an inference that has been running for
// thirty seconds is normal and a lease that expires under it would start a
// second inference of the same evidence.
const VLMLeaseDuration = 5 * time.Minute

const claimSQL = `
WITH candidate AS (
    SELECT job_id
      FROM jobs
     WHERE run_after <= now()
       AND ($4 = '' OR tenant_id = $4)
       AND (
             status IN ('pending', 'failed')
             -- A job whose worker died is still status=leased. Without this
             -- clause a killed worker would hold its jobs until a human
             -- intervened, and the lease would be decorative.
          OR (status = 'leased' AND lease_expires_at IS NOT NULL AND lease_expires_at < now())
           )
     ORDER BY run_after, job_id
     FOR UPDATE SKIP LOCKED
     LIMIT $3
)
UPDATE jobs j
   -- sqlguard:exempt lease - this is the claim. It is the statement that
   -- *grants* the lease rather than writing under one, it runs under the system
   -- worker because a worker serves every tenant, and it constrains the rows it
   -- takes with FOR UPDATE SKIP LOCKED inside the same statement.
   SET status = 'leased',
       lease_owner = $1,
       lease_expires_at = now() + make_interval(secs => $2),
       -- Advanced here, not in the handler. A job that fails repeatedly must
       -- reach max_attempts and become a dead letter; a counter that only moves
       -- on success never counts the failures that matter.
       attempt_count = j.attempt_count + 1
  FROM candidate c
 WHERE j.job_id = c.job_id
RETURNING j.job_id::text, j.tenant_id, j.site_id, j.queue, j.idempotency_key,
          j.payload, j.attempt_count, j.max_attempts, j.run_after,
          j.lease_owner, j.lease_expires_at, j.created_at`

// Worker claims and runs jobs of one queue.
type Worker struct {
	Enqueuer
	// Name selects which queue this worker serves.
	Name Queue
	// LeaseDuration is the visibility timeout for this queue's jobs.
	LeaseDuration time.Duration
	// Handler runs one job. It must be idempotent: the queue gives at-least-once
	// delivery, and a handler that is not idempotent will double its effect on
	// the first retry after a crash.
	Handler func(ctx context.Context, j Job) error
	// Logf receives progress. It never receives a payload.
	Logf func(format string, args ...any)
}

// Claim takes up to limit jobs for a single tenant.
//
// Claiming deliberately does not require a handler. A worker can legitimately
// claim, inspect and hand off, and a guard here would push that shape away from
// the natural one; the handler requirement lives in Run, where it is a real
// precondition for doing the work.
func (q *Worker) Claim(ctx context.Context, tenantID, owner string, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 50
	}
	seconds := int(q.lease().Seconds())
	if seconds < 1 {
		seconds = 1
	}

	var out []Job
	err := q.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, claimSQL, owner, seconds, limit, tenantID)
		if err != nil {
			return fmt.Errorf("jobs: claim: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var j Job
			var siteID *string
			if err := rows.Scan(&j.ID, &j.TenantID, &siteID, &j.Queue, &j.IdempotencyKey,
				&j.Payload, &j.AttemptCount, &j.MaxAttempts, &j.RunAfter,
				&j.LeaseOwner, &j.LeaseExpiresAt, &j.CreatedAt); err != nil {
				return fmt.Errorf("jobs: scan: %w", err)
			}
			if siteID != nil {
				j.SiteID = *siteID
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, err
}

// Run claims and executes up to limit jobs, returning how many completed.
//
// It is the shape a worker loop uses, and the place the handler precondition is
// enforced: a worker with no handler would claim jobs, do nothing, and leave
// them to expire - a loop that looks busy and achieves nothing.
func (q *Worker) Run(ctx context.Context, tenantID, owner string, limit int) (int, error) {
	if q.Handler == nil {
		return 0, errors.New("jobs: queue worker has no handler")
	}
	claimed, err := q.Claim(ctx, tenantID, owner, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, j := range claimed {
		if err := q.Handler(ctx, j); err != nil {
			status, ferr := q.Fail(ctx, j.TenantID, j.ID, owner, err)
			if ferr != nil {
				if errors.Is(ferr, ErrNotClaimed) {
					// The lease moved on while the handler ran. The work may have
					// been done by whoever holds it now, so this is reported
					// rather than retried.
					continue
				}
				return done, ferr
			}
			if status == StatusDeadLetter && q.Logf != nil {
				q.Logf("job %s (%s) dead-lettered: %v", j.ID, j.Queue, err)
			}
			continue
		}
		if err := q.Complete(ctx, j.TenantID, j.ID, owner); err != nil {
			if errors.Is(err, ErrNotClaimed) {
				continue
			}
			return done, err
		}
		done++
	}
	return done, nil
}

func (q *Worker) lease() time.Duration {
	if q.LeaseDuration > 0 {
		return q.LeaseDuration
	}
	if q.Name == QueueVLM {
		return VLMLeaseDuration
	}
	return DefaultLeaseDuration
}

// leasedWrite runs fn as the system worker, on the strength of a lease predicate.
//
// `site_isolation` has been RESTRICTIVE since migration 21, so a write against
// `jobs` naming only its tenant is refused by a policy the worker cannot see.
// Every terminal write here is guarded on `lease_owner`: only the worker that
// claimed this specific job may write it, which is a strictly stronger predicate
// than site scope — a worker does not hold every job of its site, it holds one
// job of one site.
//
// Running as the system worker is therefore not a bypass dressed up as a
// convenience. It is the "I am acting on one leased row, not browsing" case, and
// the lease predicate inside the statement is the proof. A caller gains nothing
// by it: the lease is what authorises the write, and a caller without the lease
// matches zero rows exactly as it did before.
func leasedWrite(ctx context.Context, pool *dbx.Pool, fn func(tx pgx.Tx) error) error {
	return pool.InSystemTenant(ctx, fn)
}

// Renew extends a lease a worker is still using. A long inference must not have
// its job stolen from under it and started a second time.
func (q *Worker) Renew(ctx context.Context, tenant, id, owner string) error {
	seconds := int(q.lease().Seconds())
	err := leasedWrite(ctx, q.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
		UPDATE jobs
		   SET lease_expires_at = now() + make_interval(secs => $3)
		 WHERE job_id = $1 AND tenant_id = $4 AND lease_owner = $2 AND status = 'leased'`,
			id, owner, seconds, tenant)
		if err != nil {
			return fmt.Errorf("jobs: renew: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: %s", ErrNotClaimed, id)
		}
		return nil
	})
	return err
}

// Complete marks a job done. Done is terminal and a job is never re-run.
func (q *Worker) Complete(ctx context.Context, tenant, id, owner string) error {
	return leasedWrite(ctx, q.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
		UPDATE jobs
		   SET status = 'done', completed_at = now(),
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL
		 WHERE job_id = $1 AND tenant_id = $3 AND lease_owner = $2`, id, owner, tenant)
		if err != nil {
			return fmt.Errorf("jobs: complete: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: %s", ErrNotClaimed, id)
		}
		return nil
	})
}

// Fail records a failed attempt and applies exponential backoff with a cap. Past
// the job's own retry bound it becomes a dead letter with a reason.
//
// The backoff exponent is capped *before* the cast, not after. It used to be
// `least(900, 5 * power(2, attempt_count)::int)`, which casts an unbounded
// exponent to int32 first: 2^29 - 5 is 2,684,354,560, so the cast raises at
// attempt_count 29. `max_attempts` is a column with only a `>= 0` check, so a
// job enqueued with a bound above 28 reaches that. The UPDATE then rolls back,
// `Fail` returns an error rather than a status, the row stays leased, the lease
// expires, the job is reclaimed, and the whole thing repeats - the worker
// aborts on every pass and the job never reaches `dead_letter` at all. A
// raising backoff on the failure path is the one thing that turns a bounded retry
// into a permanently stuck row.
func (q *Worker) Fail(ctx context.Context, tenant, id, owner string, cause error) (Status, error) {
	if cause == nil {
		// Not a defensive check about a caller that "should" pass an error: this
		// is the failure path, and `cause.Error()` below would panic while
		// building the arguments, before the query is even sent.
		cause = errors.New("jobs: the failure had no cause recorded")
	}
	var status Status
	return status, leasedWrite(ctx, q.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
		UPDATE jobs
		   SET status = CASE WHEN attempt_count >= max_attempts THEN 'dead_letter' ELSE 'failed' END,
		       last_error = $3,
		       dead_letter_reason = CASE WHEN attempt_count >= max_attempts THEN $3 ELSE NULL END,
		       -- completed_at is stamped on the transition to dead_letter, and
		       -- nowhere else. It is the instant the job entered the state an
		       -- operator has to act on, and DeadLetters orders by it.
		       --
		       -- Without it the listing ordered by created_at, which is when the
		       -- job was enqueued: a job retried for a month and dead-lettered
		       -- yesterday sorted above one that failed this morning, and the SLO
		       -- is "a dead letter must be visible within 60 seconds of entering the
		       -- state". The inbox and the outbox both carry the same column and
		       -- both were wrong in the same way before they were fixed.
		       completed_at = CASE WHEN attempt_count >= max_attempts THEN now() ELSE completed_at END,
		       run_after = now() + make_interval(secs => (5 * power(2, least(attempt_count, 6)))::int),
		       lease_owner = NULL,
		       lease_expires_at = NULL
		 WHERE job_id = $1 AND tenant_id = $4 AND lease_owner = $2
		RETURNING status`, id, owner, cause.Error(), tenant).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNotClaimed, id)
			}
			return fmt.Errorf("jobs: fail: %w", err)
		}
		return nil
	})
}

// DeadLetter is one job that reached a terminal failure.
type DeadLetter struct {
	ID             string
	TenantID       string
	SiteID         string
	Queue          Queue
	IdempotencyKey string
	Attempts       int
	MaxAttempts    int
	Reason         string
	CreatedAt      time.Time
}

// DeadLetters lists dead-lettered jobs, newest first. Surfaced in the console
// and audited: the plan is explicit that a dead letter is never silently dropped.
//
// The site is a parameter rather than a default. Since migration 21
// `site_isolation` is RESTRICTIVE, so a tenant-scoped read of a site-scoped table
// returns nothing rather than everything, and an operator shown an empty
// dead-letter list reads it as "the site is healthy".
func (q *Worker) DeadLetters(ctx context.Context, tenantID, siteID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := q.Pool.WithSites(tenantID, "jobs", []string{siteID}).Query(ctx, `
		SELECT job_id::text, tenant_id, site_id, queue, idempotency_key,
		       attempt_count, max_attempts, COALESCE(dead_letter_reason, last_error, ''),
		       -- completed_at, not created_at: the operator's question is "what
		       -- failed recently", and a job enqueued a month ago and dead-lettered
		       -- yesterday is recent.
		       COALESCE(completed_at, created_at)
		  FROM jobs
		 WHERE ($1 = '' OR tenant_id = $1) AND status = 'dead_letter'
		 ORDER BY COALESCE(completed_at, created_at) DESC
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("jobs: list dead letters: %w", err)
	}
	defer rows.Close()

	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		var siteID *string
		if err := rows.Scan(&d.ID, &d.TenantID, &siteID, &d.Queue, &d.IdempotencyKey,
			&d.Attempts, &d.MaxAttempts, &d.Reason, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("jobs: scan dead letter: %w", err)
		}
		if siteID != nil {
			d.SiteID = *siteID
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Replay moves a dead letter back to pending, resetting its retry budget.
//
// Operator-triggered, not automatic: a dead letter usually means the dependency
// is broken, and retrying in a loop turns a visible incident into invisible load.
func (q *Worker) Replay(ctx context.Context, tenant, id string) error {
	tag, err := q.Pool.For(tenant, "jobs").Exec(ctx, `
		UPDATE jobs
		   -- sqlguard:exempt lease - an operator replaying a dead letter. The row
		   -- is dead, so no worker holds its lease; the action is what makes it
		   -- claimable again.
		   SET status = 'pending', attempt_count = 0, last_error = NULL,
		       dead_letter_reason = NULL, completed_at = NULL, run_after = now(),
		       lease_owner = NULL, lease_expires_at = NULL
		 WHERE job_id = $1 AND tenant_id = $2 AND status = 'dead_letter'`, id, tenant)
	if err != nil {
		return fmt.Errorf("jobs: replay: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("jobs: %s is not a dead letter", id)
	}
	return nil
}

// Counts is the queue depth summary the SLO alerts read.
type Counts struct {
	Pending    int64
	Leased     int64
	Failed     int64
	DeadLetter int64
	Done       int64
}

// Counts reads the queue's depth.
//
// Scoped to a site, like the dead-letter listing: a depth that reads zero
// because the reader could not see the rows is a queue an operator would read as
// drained.
func (q *Worker) Counts(ctx context.Context, tenantID, siteID string) (Counts, error) {
	var c Counts
	err := q.Pool.WithSites(tenantID, "jobs", []string{siteID}).QueryRow(ctx, `
		SELECT
		    count(*) FILTER (WHERE status = 'pending'),
		    count(*) FILTER (WHERE status = 'leased'),
		    count(*) FILTER (WHERE status = 'failed'),
		    count(*) FILTER (WHERE status = 'dead_letter'),
		    count(*) FILTER (WHERE status = 'done')
		  FROM jobs
		 WHERE ($1 = '' OR tenant_id = $1) AND queue = $2`,
		tenantID, string(q.Name)).Scan(&c.Pending, &c.Leased, &c.Failed, &c.DeadLetter, &c.Done)
	if err != nil {
		return c, fmt.Errorf("jobs: counts: %w", err)
	}
	return c, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullTime renders the zero time as SQL NULL, so the column's own `now()`
// default applies instead of the year one.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
