// Package outbox gives transactional publish without a broker.
//
// The problem it solves is narrow and specific: a state change and its outbound
// side effect must commit together or not at all. Notify an operator about an
// incident, or create the incident - never one without the other, and never both
// when a crash lands between them.
//
// Writing the side effect and the row in one transaction, then letting a
// separate relay drain the row, is what makes that hold. The relay gives
// at-least-once delivery; consumers dedupe on idempotency_key, because
// at-least-once plus a consumer that assumes exactly-once is how a notification
// arrives three times and a dispatch happens three times.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/observability"
)

// Message is one outbound side effect.
type Message struct {
	TenantID string
	SiteID   string
	// Topic names the kind of side effect. A consumer subscribes by topic; an
	// unknown topic is dead-lettered rather than ignored, so a typo in a topic
	// does not look like a quiet system.
	Topic string
	// IdempotencyKey is unique per tenant and is how a consumer recognises a
	// redelivery. It is required, not optional: a consumer cannot dedupe what it
	// cannot identify. It is scoped to the tenant rather than global because two
	// tenants on one instance will naturally generate the same key for their own
	// side effect, and a global key would swallow the second one's.
	IdempotencyKey string
	Payload        json.RawMessage
	// RunAfter delays delivery, for a side effect that should not happen
	// immediately.
	RunAfter time.Time
}

// Enqueue writes a message inside the caller's transaction.
//
// It is a plain insert, not a function that opens its own transaction, because
// the whole point is that the caller has one open already.
func Enqueue(ctx context.Context, tx pgx.Tx, m Message) error {
	if m.TenantID == "" {
		return errors.New("outbox: tenant id is required and is never read from a request body")
	}
	if m.Topic == "" {
		return errors.New("outbox: topic is required")
	}
	if m.IdempotencyKey == "" {
		return errors.New("outbox: idempotency key is required; a consumer cannot dedupe what it cannot identify")
	}
	if len(m.Payload) == 0 {
		return errors.New("outbox: payload is required")
	}
	// run_after is left to the column default when the caller did not ask for a
	// delay, and the default is the DATABASE's now(), not the application's.
	//
	// Every claim predicate compares run_after against the database clock
	// (`run_after <= now()`), so a value written from the host's clock is being
	// compared against a clock it may not agree with. A host behind the database
	// writes a run_after in the past and the message fires immediately, skipping
	// the delay the caller asked for; a host ahead writes one in the future and
	// the message sits unclaimed until the skew drains. The inbox already requires
	// its caller to supply ReceivedAt for the same reason and takes it from the
	// platform's own clock; the outbox did not, and its default was
	// time.Now().UTC().
	//
	// Passing NULL and letting the column default apply is what makes the two
	// clocks the same clock. It needs no migration: the column already carries
	// DEFAULT now(), which is why this is a change to the INSERT rather than to
	// the schema.
	var runAfter any
	if !m.RunAfter.IsZero() {
		runAfter = m.RunAfter.UTC()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (tenant_id, site_id, topic, payload, idempotency_key, run_after)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6::timestamptz, now()))
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING`,
		m.TenantID, nullString(m.SiteID), m.Topic, m.Payload, m.IdempotencyKey, runAfter,
	); err != nil {
		return fmt.Errorf("outbox: enqueue: %w", err)
	}
	return nil
}

// Delivery is one message handed to a consumer.
type Delivery struct {
	OutboxID int64
	Message
	AttemptCount int
	// LeaseOwner is the relay that claimed this message. It is carried on the
	// delivery rather than taken as a parameter because it is the half of the
	// claim that the completion write has to match on, and a completion that
	// does not carry it cannot match on it.
	LeaseOwner string
}

// Handler consumes a delivery. It must be idempotent on IdempotencyKey: the relay
// gives at-least-once, and a handler that is not idempotent will double its
// effect on the first retry after a crash.
type Handler func(ctx context.Context, d Delivery) error

// Relay drains the outbox.
type Relay struct {
	Pool    *dbx.Pool
	Handler Handler
	// MaxAttempts is where a message becomes a dead letter. A message that
	// cannot be delivered is a visible, audited fact, never a silent drop.
	MaxAttempts int
	// LeaseDuration is the visibility timeout. A relay that dies mid-delivery
	// has its message redelivered when the lease expires.
	LeaseDuration time.Duration
	// Metrics is optional and nil-tolerant.
	//
	// `outbox_delivered_total` was declared and never incremented, while
	// `DrainOnce` computes the exact number as its return value. The counter was
	// free and nobody took it, which is why a delivery-rate dashboard built against
	// the name reads as "nothing has ever been delivered" on an install that has
	// delivered a million messages.
	//
	// Nil-tolerant for the reason it always is here: a relay is constructed in tests
	// and in single-message paths that have no registry, and a dashboard metric is
	// not a reason for any of them to acquire one.
	Metrics *observability.Registry
	// Logf receives progress. It never receives a payload.
	Logf func(format string, args ...any)
}

// DefaultLeaseDuration matches the inbox's, because the two are recovered by the
// same operator staring at the same dashboard.
const DefaultLeaseDuration = 60 * time.Second

const claimOutboxSQL = `
WITH candidate AS (
    SELECT outbox_id
      FROM outbox_events
     WHERE run_after <= now()
       AND ($4 = '' OR tenant_id = $4)
       AND (
             status IN ('pending', 'failed')
             -- A relay that died mid-delivery leaves the row status=delivering.
             -- Without this clause the lease would be decorative and a single
             -- crashed relay would strand every message it held.
          OR (status = 'delivering' AND lease_expires_at IS NOT NULL AND lease_expires_at < now())
           )
     ORDER BY run_after, outbox_id
		FOR UPDATE SKIP LOCKED
		LIMIT $1
)
UPDATE outbox_events o
   -- sqlguard:exempt lease - this is the claim. It grants the lease rather than
   -- writing under one, it runs under the system worker because a relay serves
   -- every tenant, and the rows it takes are pinned by FOR UPDATE SKIP LOCKED
   -- inside the same statement.
   SET status = 'delivering',
       lease_owner = $2,
       lease_expires_at = now() + make_interval(secs => $3),
       attempt_count = o.attempt_count + 1
  FROM candidate c
 WHERE o.outbox_id = c.outbox_id
-- lease_owner is returned as well as the message, because every write that
-- follows the claim has to be able to match the claim. A completion that cannot
-- name its holder is a completion that can be applied by anybody.
RETURNING o.outbox_id, o.tenant_id, o.site_id, o.topic, o.payload,
          o.idempotency_key, o.attempt_count, o.lease_owner`

// ErrLeaseLost is returned when a relay tries to finish a message it no longer
// holds.
//
// It is not a retry signal. The row is somebody else's now: either it was
// reclaimed after this relay's lease lapsed, or it already reached a terminal
// state. The correct response is to log it and move on, and treating it as
// "retry me" is how a dead letter gets overwritten by a worker that finished
// late.
var ErrLeaseLost = errors.New("outbox: the lease on this message is no longer held by this relay")

// DrainOnce delivers up to limit messages and returns how many it handled.
//
// Each message commits independently. Batching them into one transaction would
// mean one poison message rolls back the good messages with it, and the poison
// message would then be retried for ever.
func (r *Relay) DrainOnce(ctx context.Context, tenantID, owner string, limit int) (int, error) {
	if r.Handler == nil {
		return 0, errors.New("outbox: relay has no handler")
	}
	if limit <= 0 {
		limit = 100
	}
	lease := int(r.lease().Seconds())
	if lease < 1 {
		lease = 1
	}

	var deliveries []Delivery
	// The claim runs as a system worker: it takes the tenant from each message
	// it claims rather than from a session. tenantID narrows it when a caller
	// wants that; empty means drain across tenants.
	err := r.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, claimOutboxSQL, limit, owner, lease, tenantID)
		if err != nil {
			return fmt.Errorf("outbox: claim: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var d Delivery
			var siteID *string
			if err := rows.Scan(&d.OutboxID, &d.TenantID, &siteID, &d.Topic, &d.Payload,
				&d.IdempotencyKey, &d.AttemptCount, &d.LeaseOwner); err != nil {
				return fmt.Errorf("outbox: scan: %w", err)
			}
			if siteID != nil {
				d.SiteID = *siteID
			}
			deliveries = append(deliveries, d)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}

	delivered := 0
	for _, d := range deliveries {
		if err := r.deliver(ctx, d); err != nil {
			if r.Logf != nil {
				r.Logf("outbox delivery %d (%s) failed: %v", d.OutboxID, d.Topic, err)
			}
			continue
		}
		delivered++
	}
	// Counted in bulk from the number the function already computed, rather than
	// inside the loop, so the counter and the return value cannot disagree. A counter
	// incremented per delivery and a count returned from the same loop are two sources
	// of truth for one fact, and they diverge the first time something `continue`s
	// between them -- which this loop does, for every delivery whose handler failed.
	//
	// `delivered` is successes only, and that is what a `delivered_total` should
	// count. A message whose handler failed was attempted and is on its way to a dead
	// letter; counting it as delivered would make the delivery-rate dashboard read as
	// healthy on an install where every message is failing.
	//
	// Unconditional, including on an empty drain: `Add` with zero creates the series
	// at zero, and a dashboard for a name that does not exist yet is a dashboard that
	// has to be special-cased to draw a gap.
	r.addDelivered(int64(delivered))
	return delivered, nil
}

// addDelivered is the one place the relay touches metrics, so the nil check has one
// home rather than being repeated at every call site.
func (r *Relay) addDelivered(n int64) {
	if r.Metrics == nil {
		return
	}
	r.Metrics.Counters.Add(observability.CounterOutboxDelivered, n)
}

func (r *Relay) deliver(ctx context.Context, d Delivery) error {
	if err := r.Handler(ctx, d); err != nil {
		return r.markFailed(ctx, d, err)
	}
	// Delivered is terminal. A redelivery after a crash between the handler
	// succeeding and this write is why handlers must be idempotent; marking it
	// here is what stops the common case, not the rare one.
	//
	// The write matches the claim: status = 'delivering' AND lease_owner = this
	// relay. It used to match on outbox_id alone, which made the mark-delivered
	// statement applicable by anybody at any time. The interleaving that costs
	// something is reachable, because a handler is not bounded by the lease: relay
	// A claims, its handler is slow past the lease, relay B claims the expired
	// row, B's handler fails, and B drives the message to dead_letter. A then
	// returns from its handler successfully and overwrites status with
	// 'delivered' and clears last_error. The dead letter — the "visible, audited
	// fact, never a silent drop" this package's own doc promises — is erased by a
	// worker that finished late, and the operator never sees the message at all.
	// Matching the claim is the whole fix: A's write affects no rows, and A is
	// told its lease is gone rather than being allowed to assert a terminal state
	// it does not own.
	// `tenant_id` is in the WHERE and not only in the pool context.
	//
	// A `bigserial` primary key is global and predictable, so a statement whose
	// tenant-safety is a property of a policy three layers down is one refactor
	// away from being a cross-tenant write.
	//
	// The site is not in the WHERE, and deliberately so. This write is guarded on
	// `lease_owner`: only the relay that claimed this specific row may write it,
	// which is a strictly stronger predicate than site scope — a relay does not
	// hold every row of its site, it holds one row of one site. Since migration 21
	// `site_isolation` is RESTRICTIVE and a worker holding a lease across a
	// legitimate site boundary would be refused, so this runs as the system worker.
	// That bypass is the "I am acting on one leased row, not browsing" case, and
	// the lease predicate is the proof.
	err := r.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
		UPDATE outbox_events
		   SET status = 'delivered', delivered_at = now(),
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL,
		       -- The same reason last_error is cleared above, applied to the column
		       -- that replaced it for the dead-letter state. A row reaching this
		       -- write always has it NULL already — Replay clears it and markFailed
		       -- only writes it on the terminal transition — so this is the
		       -- assertion rather than the mechanism. It costs one column in a
		       -- statement that is already matching on the claim.
		       dead_letter_reason = NULL
		   WHERE outbox_id = $1 AND tenant_id = $3
		   AND status = 'delivering'
		   AND lease_owner IS NOT DISTINCT FROM $2`, d.OutboxID, d.LeaseOwner, d.TenantID)
		if err != nil {
			return fmt.Errorf("outbox: mark delivered: %w", err)
		}
		if tag.RowsAffected() == 0 {
			// Either the lease was reclaimed or the message already reached a
			// terminal state, most importantly dead_letter. Saying so beats writing
			// 'delivered' over another relay's terminal state.
			return fmt.Errorf("%w: message %d (%s)", ErrLeaseLost, d.OutboxID, d.IdempotencyKey)
		}
		return nil
	})
	return err
}

func (r *Relay) markFailed(ctx context.Context, d Delivery, cause error) error {
	if cause == nil {
		// The failure path, and `cause.Error()` below would panic while building
		// the arguments, before the query is sent.
		cause = errors.New("outbox: the delivery had no cause recorded")
	}
	maxAttempts := r.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	var status string
	// The backoff exponent is capped *before* the cast, not after. The original
	// expression was `least(300, 5 * power(2, attempt_count)::int)`, which casts
	// an unbounded exponent to int32 first: at attempt_count = 31 the cast raises
	// and the whole failure path errors out, so the row is left claimed by a
	// worker that has already moved on and is never marked at all. A raising
	// backoff on the failure path turns a bounded retry into a lost message,
	// which is the one thing this table exists to prevent.
	//
	// run_after is set to now(), not to a backoff, on the transition to
	// dead_letter. It used to be set to a future instant on that transition too,
	// which is a fact that reads as "this will be retried" and is not: no claim
	// ever selects a dead_letter row, so the value is a lie in the only column an
	// operator or a query has to judge from. now() is the honest "this stopped
	// here" and it is the same instant the dead letter entered the state, which is
	// what an operator ordering dead letters by time needs.
	//
	// Two columns, not one, and the difference is the whole point.
	//
	// last_error is the *current* failure text: it is rewritten on every attempt
	// that fails, so on a row still being retried it is exactly right, and on a
	// row that has stopped it is whatever the last attempt happened to say rather
	// than why the message died. A relay that was misconfigured, then fixed, then
	// hit a DNS failure leaves a dead letter explaining DNS.
	//
	// dead_letter_reason is written only on the transition to dead_letter, and is
	// never rewritten afterwards. That is the reason an operator needs, and it is
	// the column inbox_events and jobs already have — outbox_events was the only
	// one of the three without it, which meant the same question was answered
	// from two different columns depending on which table the message was in.
	//
	// The two are written from the same expression on purpose. There is no window
	// in which they can disagree, and a row that reaches dead_letter always has
	// both set, so an operator reading either column reads the same sentence.
	//
	// Needs migration 0019. The column is nullable with no default, so a relay
	// that has not been upgraded writes NULL and DeadLetters falls back to
	// last_error, which is the answer it gives today.
	//
	// As in `deliver`: this write is guarded on `lease_owner`, which is a
	// stronger predicate than site scope — a relay holds one message, not every
	// message of its site — so it runs as the system worker rather than
	// narrowing to a site it is not scoped to. Since migration 21
	// `site_isolation` is RESTRICTIVE, and a tenant-scoped write against this
	// table would be refused by a policy the relay cannot see.
	if err := r.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
		UPDATE outbox_events
		   SET status = CASE WHEN attempt_count >= $3 THEN 'dead_letter' ELSE 'failed' END,
		       last_error = $4,
		       dead_letter_reason = CASE WHEN attempt_count >= $3 THEN $4 ELSE NULL END,
		       run_after = CASE WHEN attempt_count >= $3
		                       THEN now()
		                       ELSE now() + make_interval(secs => (5 * power(2, least(attempt_count, 6)))::int)
		                   END,
		       lease_owner = NULL,
		       lease_expires_at = NULL
		 WHERE outbox_id = $1 AND tenant_id = $5
		   AND status = 'delivering'
		   AND lease_owner IS NOT DISTINCT FROM $2
		RETURNING status`, d.OutboxID, d.LeaseOwner, maxAttempts, cause.Error(), d.TenantID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The lease is gone, so this attempt's failure is not this relay's to
				// record. Recording it anyway is how a worker that finished late
				// resurrects a row that has since been delivered or dead-lettered by
				// somebody else.
				return fmt.Errorf("%w: message %d (%s): %w",
					ErrLeaseLost, d.OutboxID, d.IdempotencyKey, cause)
			}
			return fmt.Errorf("outbox: mark failed: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return cause
}

// DeadLetter is one message that reached a terminal failure and is waiting for
// an operator.
//
// The listing is scoped to a site, because `site_isolation` has been RESTRICTIVE
// since migration 21: a console that named only its tenant would show an
// operator an empty list, and an empty list reads as "the site is healthy".
type DeadLetter struct {
	OutboxID       int64
	TenantID       string
	SiteID         string
	Topic          string
	IdempotencyKey string
	Attempts       int
	// Reason is the dead letter's own reason, falling back to last_error.
	//
	// The fallback is not a courtesy. dead_letter_reason is nullable with no
	// default, so every row written before migration 0019 — and every row written
	// by a relay that has not been upgraded since — has it NULL, and reporting
	// "" for those would replace today's answer with silence. COALESCE is what
	// lets the two populations of rows be reported through one listing.
	Reason string
	// DeadLetteredAt is run_after. markFailed sets run_after to now() on the
	// transition to dead_letter, so on a dead-lettered row it is the instant the
	// message entered the state and nothing else. Ordering by it is what makes a
	// newly dead-lettered message appear at the top of the operator's list even
	// when it was enqueued weeks ago and has been retrying ever since.
	DeadLetteredAt time.Time
}

// DeadLetters lists dead-lettered messages, most recently dead-lettered first.
//
// It is the operator's reason surface for this table, and it is ordered by the
// instant the message reached the state rather than by when it was enqueued. The
// SLO says a dead letter must be visible within 60 seconds of entering the state;
// ordering by enqueue time puts a message that died a moment ago below one that
// died last month, which is the wrong answer to "what just broke".
func (r *Relay) DeadLetters(ctx context.Context, tenantID, siteID string, limit int) ([]DeadLetter, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Scoped, not bypassed: a dead letter is not a leased row any worker holds, so
	// the lease argument does not apply and the site is the honest constraint.
	// Since migration 21 `site_isolation` is RESTRICTIVE, and a listing an
	// operator reads must show the sites they are assigned to rather than every
	// site's messages.
	rows, err := r.Pool.WithSites(tenantID, "outbox", []string{siteID}).Query(ctx, `
		SELECT outbox_id, tenant_id, site_id, topic, idempotency_key,
		       attempt_count, COALESCE(dead_letter_reason, last_error, ''), run_after
		  FROM outbox_events
		 WHERE tenant_id = $1 AND status = 'dead_letter'
		 ORDER BY run_after DESC
		 LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: list dead letters: %w", err)
	}
	defer rows.Close()

	var out []DeadLetter
	for rows.Next() {
		var d DeadLetter
		var siteID *string
		if err := rows.Scan(&d.OutboxID, &d.TenantID, &siteID, &d.Topic, &d.IdempotencyKey,
			&d.Attempts, &d.Reason, &d.DeadLetteredAt); err != nil {
			return nil, fmt.Errorf("outbox: scan dead letter: %w", err)
		}
		if siteID != nil {
			d.SiteID = *siteID
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Replay moves a dead letter back to pending so it is retried.
//
// Replay is an operator action and never automatic: a message that could not be
// delivered usually means something on the other end is broken, and retrying it
// in a loop turns a visible incident into invisible load. Replaying also clears
// the attempt count, because the bound that produced the dead letter has already
// been spent and a replay that kept the count would dead-letter on the first
// attempt again.
func (r *Relay) Replay(ctx context.Context, tenantID string, outboxID int64) error {
	tag, err := r.Pool.For(tenantID, "outbox").Exec(ctx, `
		UPDATE outbox_events
		   -- sqlguard:exempt lease - an operator replaying a dead letter. The row
		   -- is dead, so no relay holds its lease; the action is what makes it
		   -- claimable again.
		   SET status = 'pending', attempt_count = 0, last_error = NULL,
		       -- A replayed row that then fails differently, or succeeds, must not
		       -- carry the reason it was dead-lettered for into a state where that
		       -- reason is no longer why. The replay is the operator saying "I have
		       -- fixed whatever this was", so the old reason is stale the moment it
		       -- runs.
		       dead_letter_reason = NULL,
		       run_after = now(), lease_owner = NULL, lease_expires_at = NULL
		 WHERE outbox_id = $1 AND tenant_id = $2 AND status = 'dead_letter'`, outboxID, tenantID)
	if err != nil {
		return fmt.Errorf("outbox: replay: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("outbox: message %d is not a dead letter", outboxID)
	}
	return nil
}

// ReplayByIdempotencyKey re-queues a dead letter by its idempotency key.
func (r *Relay) ReplayByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) error {
	// First, find the outbox_id for this idempotency key.
	var outboxID int64
	err := r.Pool.For(tenantID, "outbox").QueryRow(ctx, `
		SELECT outbox_id FROM outbox_events
		 WHERE tenant_id = $1 AND idempotency_key = $2 AND status = 'dead_letter'`,
		tenantID, idempotencyKey).Scan(&outboxID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("outbox: message %s is not a dead letter", idempotencyKey)
		}
		return fmt.Errorf("outbox: find message: %w", err)
	}
	return r.Replay(ctx, tenantID, outboxID)
}

func (r *Relay) lease() time.Duration {
	if r.LeaseDuration <= 0 {
		return DefaultLeaseDuration
	}
	return r.LeaseDuration
}

// Pending reports the backlog depth, which the observability contract names as
// outbox_pending.
//
// It runs as a system worker so an operator sees the whole backlog rather than
// their own session's slice of it.
func (r *Relay) Pending(ctx context.Context, tenantID string) (int64, error) {
	var n int64
	err := r.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM outbox_events
			 WHERE ($1 = '' OR tenant_id = $1) AND status IN ('pending', 'failed', 'delivering')`,
			tenantID).Scan(&n)
	})
	if err != nil {
		return 0, fmt.Errorf("outbox: pending count: %w", err)
	}
	return n, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
