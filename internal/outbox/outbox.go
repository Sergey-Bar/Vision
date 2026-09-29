// Package outbox gives transactional publish without a broker.
//
// The problem it solves is narrow and specific: a state change and its outbound
// side effect must commit together or not at all. Notify an operator about an
// incident, or create the incident — never one without the other, and never both
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
	if m.RunAfter.IsZero() {
		m.RunAfter = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (tenant_id, site_id, topic, payload, idempotency_key, run_after)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, idempotency_key) DO NOTHING`,
		m.TenantID, nullString(m.SiteID), m.Topic, m.Payload, m.IdempotencyKey, m.RunAfter.UTC(),
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
   SET status = 'delivering',
       lease_owner = $2,
       lease_expires_at = now() + make_interval(secs => $3),
       attempt_count = o.attempt_count + 1
  FROM candidate c
 WHERE o.outbox_id = c.outbox_id
RETURNING o.outbox_id, o.tenant_id, o.site_id, o.topic, o.payload,
          o.idempotency_key, o.attempt_count`

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
				&d.IdempotencyKey, &d.AttemptCount); err != nil {
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
	return delivered, nil
}

func (r *Relay) deliver(ctx context.Context, d Delivery) error {
	if err := r.Handler(ctx, d); err != nil {
		return r.markFailed(ctx, d, err)
	}
	// Delivered is terminal. A redelivery after a crash between the handler
	// succeeding and this write is why handlers must be idempotent; marking it
	// here is what stops the common case, not the rare one.
	_, err := r.Pool.For(d.TenantID, "outbox").Exec(ctx, `
		UPDATE outbox_events
		   SET status = 'delivered', delivered_at = now(),
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL
		 WHERE outbox_id = $1`, d.OutboxID)
	if err != nil {
		return fmt.Errorf("outbox: mark delivered: %w", err)
	}
	return nil
}

func (r *Relay) markFailed(ctx context.Context, d Delivery, cause error) error {
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
	if err := r.Pool.For(d.TenantID, "outbox").QueryRow(ctx, `
		UPDATE outbox_events
		   SET status = CASE WHEN attempt_count >= $2 THEN 'dead_letter' ELSE 'failed' END,
		       last_error = $3,
		       run_after = now() + make_interval(secs => (5 * power(2, least(attempt_count, 6)))::int)
		 WHERE outbox_id = $1
		RETURNING status`, d.OutboxID, maxAttempts, cause.Error()).Scan(&status); err != nil {
		return fmt.Errorf("outbox: mark failed: %w", err)
	}
	return cause
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
