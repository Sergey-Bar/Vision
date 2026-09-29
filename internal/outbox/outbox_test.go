package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/outbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func open(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t) // skips, rather than fails, with no database
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func put(t *testing.T, pool *dbx.Pool, m outbox.Message) {
	t.Helper()
	if m.Topic == "" {
		m.Topic = "incident.raised"
	}
	if m.IdempotencyKey == "" {
		m.IdempotencyKey = "key-" + m.TenantID
	}
	if m.Payload == nil {
		m.Payload = json.RawMessage(`{"incidentId":"inc-1"}`)
	}
	err := pool.InTenant(context.Background(), dbx.TenantContext{TenantID: m.TenantID, ActorID: "test"},
		func(tx pgx.Tx) error { return outbox.Enqueue(context.Background(), tx, m) })
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
}

func statusOf(t *testing.T, pool *dbx.Pool, tenant, key string) (string, int, string) {
	t.Helper()
	var status, runAfter string
	var attempts int
	if err := pool.For(tenant, "test").QueryRow(context.Background(), `
		SELECT status, attempt_count, COALESCE(last_error, '')
		  FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = $2`,
		tenant, key).Scan(&status, &attempts, &runAfter); err != nil {
		t.Fatalf("read the outbox row: %v", err)
	}
	return status, attempts, runAfter
}

// The three things a consumer cannot work without are refused up front. An
// idempotency key is the one that matters: the relay gives at-least-once
// delivery, so a message with no key cannot be deduplicated and its handler
// doubles its effect on the first retry.
func TestAMessageWithoutAnIdempotencyKeyIsRefused(t *testing.T) {
	pool := open(t)
	err := pool.InTenant(context.Background(),
		dbx.TenantContext{TenantID: "t-1", ActorID: "test"},
		func(tx pgx.Tx) error {
			return outbox.Enqueue(context.Background(), tx, outbox.Message{
				TenantID: "t-1", Topic: "incident.raised", Payload: []byte(`{}`),
			})
		})
	if err == nil {
		t.Fatal("a message with no idempotency key was accepted. The relay gives " +
			"at-least-once delivery and this is the field a consumer dedupes on")
	}
}

// The key is scoped to the tenant, not global. Two tenants on one instance
// naturally generate the same key for their own side effect, and a global key
// would swallow the second one's.
func TestAnIdempotencyKeyIsScopedToItsTenant(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	put(t, pool, outbox.Message{TenantID: "t-a", IdempotencyKey: "shared", Payload: []byte(`{"n":1}`)})
	put(t, pool, outbox.Message{TenantID: "t-b", IdempotencyKey: "shared", Payload: []byte(`{"n":2}`)})

	for _, tenant := range []string{"t-a", "t-b"} {
		var n int
		if err := pool.For(tenant, "test").QueryRow(ctx,
			`SELECT count(*) FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = 'shared'`,
			tenant).Scan(&n); err != nil {
			t.Fatalf("count for %s: %v", tenant, err)
		}
		if n != 1 {
			t.Errorf("tenant %s has %d messages with the shared key, want 1", tenant, n)
		}
	}
}

// A redelivery is recognised by the key, so the second enqueue of the same side
// effect is a no-op rather than a second message.
func TestARepeatedIdempotencyKeyDoesNotEnqueueTwice(t *testing.T) {
	pool := open(t)
	put(t, pool, outbox.Message{TenantID: "t-dup", IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})
	put(t, pool, outbox.Message{TenantID: "t-dup", IdempotencyKey: "k", Payload: []byte(`{"n":2}`)})

	var n int
	if err := pool.For("t-dup", "test").QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE tenant_id = 't-dup'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d messages carry the key, want 1", n)
	}
}

// A failing handler has to reach a terminal state.
//
// The backoff used to be `least(300, 5 * power(2, attempt_count)::int)`, which
// casts an unbounded exponent to int32 before the clamp: at attempt_count 31 the
// cast raises, the failure path errors, and the row is left claimed by a worker
// that has already moved on and is never marked at all. A raising backoff on the
// failure path turns a bounded retry into a lost message, which is the one thing
// this table exists to prevent.
func TestAFailingHandlerReachesADeadLetter(t *testing.T) {
	pool := open(t)
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   3,
		LeaseDuration: 200 * time.Millisecond,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the consumer is down")
		},
	}
	put(t, pool, outbox.Message{TenantID: "t-fail", IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	deadline := time.Now().Add(30 * time.Second)
	for pass := 1; ; pass++ {
		if _, err := relay.DrainOnce(context.Background(), "t-fail", "relay-1", 10); err != nil {
			t.Fatalf("drain: %v", err)
		}
		status, attempts, lastErr := statusOf(t, pool, "t-fail", "k")
		if status == "dead_letter" {
			if lastErr == "" {
				t.Error("the dead letter carries no error; an operator looking at the " +
					"dead-letter table sees a row and no explanation")
			}
			if attempts < 3 {
				t.Errorf("the message died after %d attempts against a bound of 3", attempts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %d drains the message is %q with %d attempts and no terminal "+
				"state. A side effect that cannot be delivered must be dead-lettered, not "+
				"retried for the life of the process", pass, status, attempts)
		}
		// Clear the backoff so the test does not wait out a real retry delay. The
		// retry still goes through the claim, so the backoff's absence here is not
		// what makes the message terminal.
		testutil.Exec(t, pool.Pool, `UPDATE outbox_events SET run_after = now() - interval '1 second'
			WHERE tenant_id = 't-fail'`)
		time.Sleep(10 * time.Millisecond)
	}
}

// The happy path: delivered is terminal, and the backlog an operator is shown
// shrinks.
func TestASuccessfulDeliveryIsTerminal(t *testing.T) {
	pool := open(t)
	var got []outbox.Delivery
	relay := &outbox.Relay{
		Pool: pool,
		Handler: func(_ context.Context, d outbox.Delivery) error {
			got = append(got, d)
			return nil
		},
	}
	put(t, pool, outbox.Message{TenantID: "t-ok", IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	n, err := relay.DrainOnce(context.Background(), "t-ok", "relay-1", 10)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	if len(got) != 1 || got[0].IdempotencyKey != "k" || got[0].TenantID != "t-ok" {
		t.Fatalf("the handler saw %+v", got)
	}

	status, _, _ := statusOf(t, pool, "t-ok", "k")
	if status != "delivered" {
		t.Errorf("the message is %q after a successful delivery, want delivered", status)
	}
	if pending, err := relay.Pending(context.Background(), "t-ok"); err != nil {
		t.Fatalf("pending: %v", err)
	} else if pending != 0 {
		t.Errorf("the backlog is %d after the only message was delivered", pending)
	}
}
