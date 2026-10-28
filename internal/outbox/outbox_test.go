package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/outbox"
	"github.com/Sergey-Bar/vision/internal/testutil"
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

// statusOf reads one message's row: its status, its attempt count and the reason
// recorded on it, if any.
//
// The key is fixed because every test in this file enqueues exactly one message
// under a tenant of its own. What is under test is always "what state did the
// relay leave this one message in", and a parameter that never varies is one more
// place for a test to be reading a different row than it thinks it is.
func statusOf(t *testing.T, pool *dbx.Pool, tenant string) (string, int, string) {
	t.Helper()
	var status, lastErr string
	var attempts int
	row := pool.For(tenant, "test").QueryRow(context.Background(), `
		SELECT status, attempt_count, COALESCE(last_error, '')
		  FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant)
	defer row.Close()
	if err := row.Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatalf("read the outbox row: %v", err)
	}
	return status, attempts, lastErr
}

func runAfterOf(t *testing.T, pool *dbx.Pool, tenant string) time.Time {
	t.Helper()
	var at time.Time
	row := pool.For(tenant, "test").QueryRow(context.Background(),
		`SELECT run_after FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant)
	defer row.Close()
	if err := row.Scan(&at); err != nil {
		t.Fatalf("read run_after: %v", err)
	}
	return at
}

// uniqueTenant gives a test a tenant nobody else has used.
//
// The suite does not reset the schema between runs, and the outbox key is
// (tenant_id, idempotency_key) with ON CONFLICT DO NOTHING — so a fixed tenant
// means the second run of the suite enqueues nothing and asserts against the
// previous run's terminal row. The test passes or fails for reasons that have
// nothing to do with the code under test, which is the worst kind of flake.
// siteFor gives a site id for a synthetic tenant. The dead-letter listing is
// scoped to a site, so every caller needs one; these tests connect as the
// superuser, which bypasses RLS, so the value has to be consistent rather than
// real.
func siteFor(tenant string) string { return "site-of-" + tenant }

func uniqueTenant(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
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
	tenantA, tenantB := uniqueTenant("t-a"), uniqueTenant("t-b")
	put(t, pool, outbox.Message{TenantID: tenantA, IdempotencyKey: "shared", Payload: []byte(`{"n":1}`)})
	put(t, pool, outbox.Message{TenantID: tenantB, IdempotencyKey: "shared", Payload: []byte(`{"n":2}`)})

	for _, tenant := range []string{tenantA, tenantB} {
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
	tenant := uniqueTenant("t-dup")
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":2}`)})

	var n int
	if err := pool.For(tenant, "test").QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
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
	tenant := uniqueTenant("t-fail")
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   3,
		LeaseDuration: 200 * time.Millisecond,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the consumer is down")
		},
	}
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	deadline := time.Now().Add(30 * time.Second)
	for pass := 1; ; pass++ {
		if _, err := relay.DrainOnce(context.Background(), tenant, "relay-1", 10); err != nil {
			t.Fatalf("drain: %v", err)
		}
		status, attempts, lastErr := statusOf(t, pool, tenant)
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
			WHERE tenant_id = $1`, tenant)
		time.Sleep(10 * time.Millisecond)
	}
}

// The happy path: delivered is terminal, and the backlog an operator is shown
// shrinks.
func TestASuccessfulDeliveryIsTerminal(t *testing.T) {
	pool := open(t)
	tenant := uniqueTenant("t-ok")
	var got []outbox.Delivery
	relay := &outbox.Relay{
		Pool: pool,
		Handler: func(_ context.Context, d outbox.Delivery) error {
			got = append(got, d)
			return nil
		},
	}
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	n, err := relay.DrainOnce(context.Background(), tenant, "relay-1", 10)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	if len(got) != 1 || got[0].IdempotencyKey != "k" || got[0].TenantID != tenant {
		t.Fatalf("the handler saw %+v", got)
	}

	status, _, _ := statusOf(t, pool, tenant)
	if status != "delivered" {
		t.Errorf("the message is %q after a successful delivery, want delivered", status)
	}
	if pending, err := relay.Pending(context.Background(), tenant); err != nil {
		t.Fatalf("pending: %v", err)
	} else if pending != 0 {
		t.Errorf("the backlog is %d after the only message was delivered", pending)
	}
}

// ---------------------------------------------------------------------------
// O1: a slow relay must not resurrect a dead letter.
// ---------------------------------------------------------------------------

// A dead letter is the "visible, audited fact, never a silent drop" this package
// promises. It used to be erasable by a worker that finished late.
//
// The mark-delivered write matched on outbox_id alone, so it applied to whoever
// held the row at the time it ran. A handler is not bounded by the lease: relay A
// claims, its handler is slow, the lease lapses, relay B claims the expired row,
// B's handler fails and B drives the message to dead_letter. A then returns
// successfully and writes status = 'delivered', clearing last_error. The dead
// letter is gone and the operator is never told the message could not be
// delivered — which is not a lost event, since the event is still in the outbox,
// but it is a lost incident, and the promise the package doc makes is the thing
// that broke.
func TestASlowRelayCannotOverwriteAnotherRelaysDeadLetter(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-slow")
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	entered := make(chan struct{})
	release := make(chan struct{})
	var slowLoggedLeaseLost, deliveredByA bool

	// Relay A: claims, then blocks inside its handler past its own lease.
	slow := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   8,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			close(entered)
			<-release
			return nil
		},
		Logf: func(format string, args ...any) {
			if strings.Contains(fmt.Sprintf(format, args...), "no longer held") {
				slowLoggedLeaseLost = true
			}
		},
	}
	slowDone := make(chan int, 1)
	go func() {
		n, err := slow.DrainOnce(ctx, tenant, "relay-a", 10)
		if err != nil {
			t.Errorf("relay A drain: %v", err)
		}
		slowDone <- n
	}()
	<-entered

	// Wait past A's lease, so the row is claimable again.
	time.Sleep(1500 * time.Millisecond)

	// Relay B claims the expired row and its handler fails. MaxAttempts 1 against
	// an attempt_count of 2 puts it straight into dead_letter.
	quick := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   1,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the consumer is down")
		},
	}
	if _, err := quick.DrainOnce(ctx, tenant, "relay-b", 10); err != nil {
		t.Fatalf("relay B drain: %v", err)
	}
	status, attempts, lastErr := statusOf(t, pool, tenant)
	if status != "dead_letter" {
		t.Fatalf("relay B left the message %q after %d attempts, want dead_letter. The "+
			"interleaving this test sets up has not happened, so it proves nothing",
			status, attempts)
	}
	if lastErr == "" {
		t.Fatal("relay B dead-lettered the message with no recorded reason")
	}

	// Now A finishes. It believes it delivered, and its write used to say so.
	close(release)
	deliveredByA = <-slowDone != 0

	after, attempts, afterErr := statusOf(t, pool, tenant)
	if after != "dead_letter" {
		t.Errorf("the message is %q after the slow relay returned successfully. A worker that "+
			"no longer holds the lease overwrote another relay's dead letter, so the one fact "+
			"the operator has to act on was erased by a stale worker", after)
	}
	if afterErr == "" {
		t.Error("the slow relay cleared last_error on a dead-lettered row; the reason an " +
			"operator needs to act was erased")
	}
	if attempts != 2 {
		t.Errorf("attempt_count is %d, want 2. The slow relay's success must not be recorded "+
			"as an attempt outcome either", attempts)
	}

	// The refusal has to be reported, not silently swallowed. A relay that
	// believes it delivered a message and was not has a backlog number and a
	// delivery count that are both wrong, and nothing in the process can tell.
	if !slowLoggedLeaseLost {
		t.Error("the slow relay logged nothing about losing its lease. A refused completion " +
			"that is not reported is indistinguishable from a delivered message")
	}
	if deliveredByA {
		t.Error("the slow relay's DrainOnce counted the message as delivered. It must not: the " +
			"row is a dead letter and counting it as delivered is the same lie, one layer up")
	}
}

// The other half of the same claim, and the reason the fix is on the failure path
// too and not only on the success path. A relay whose lease lapses and whose
// handler then fails must not be able to write its failure over a row that has
// since been delivered by somebody else.
func TestASlowRelayCannotOverwriteADeliveredRowOnFailure(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-slowfail")
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	entered := make(chan struct{})
	release := make(chan struct{})

	slow := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   8,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			close(entered)
			<-release
			return errors.New("the consumer is still down")
		},
	}
	slowDone := make(chan error, 1)
	go func() {
		_, err := slow.DrainOnce(ctx, tenant, "relay-a", 10)
		slowDone <- err
	}()
	<-entered
	time.Sleep(1500 * time.Millisecond)

	quick := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   8,
		LeaseDuration: time.Second,
		Handler:       func(context.Context, outbox.Delivery) error { return nil },
	}
	if _, err := quick.DrainOnce(ctx, tenant, "relay-b", 10); err != nil {
		t.Fatalf("relay B drain: %v", err)
	}
	if status, _, _ := statusOf(t, pool, tenant); status != "delivered" {
		t.Fatalf("relay B left the message %q, want delivered", status)
	}

	close(release)
	if err := <-slowDone; err != nil {
		t.Fatalf("slow relay failed: %v", err)
	}

	after, _, afterErr := statusOf(t, pool, tenant)
	if after != "delivered" {
		t.Errorf("the message is %q after a late failure from a relay that no longer held "+
			"the lease. A failure recorded against somebody else's delivery is how a "+
			"delivered side effect gets retried for ever", after)
	}
	if afterErr != "" {
		t.Errorf("last_error is %q on a delivered row; the stale failure was recorded", afterErr)
	}
}

// ---------------------------------------------------------------------------
// O2: a dead letter must carry a reason the operator can read, and must not
// carry a not-before time nothing will read.
// ---------------------------------------------------------------------------

// The reason has to reach the operator, not merely exist in the column. The test
// goes through the listing an operator's console would call, rather than reading
// the row back with the test's own SQL, because "the value is in the table" and
// "the value is on the operator's screen" are different claims and only the
// second one is the promise this package makes.
func TestAnOutboxDeadLetterReachesTheOperatorWithItsReason(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-reason")
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   1,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the operator directory is unreachable")
		},
	}
	put(t, pool, outbox.Message{
		TenantID: tenant, IdempotencyKey: "k",
		Topic: "notify_operator", Payload: []byte(`{"n":1}`),
	})

	if _, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if status, _, _ := statusOf(t, pool, tenant); status != "dead_letter" {
		t.Fatalf("the message is %q after one failed attempt against a bound of 1", status)
	}

	letters, err := relay.DeadLetters(ctx, tenant, siteFor(tenant), 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d outbox dead letters, want 1", len(letters))
	}
	got := letters[0]
	if got.Reason == "" {
		t.Error("the dead letter is listed with no reason. A dead letter with no reason is an " +
			"incident nobody can act on")
	}
	if !strings.Contains(got.Reason, "operator directory is unreachable") {
		t.Errorf("the listed reason is %q, which does not contain the failure the handler "+
			"reported", got.Reason)
	}
	if got.Topic != "notify_operator" || got.IdempotencyKey != "k" {
		t.Errorf("the listed dead letter is %+v, which does not identify the message", got)
	}
	if got.Attempts < 1 {
		t.Errorf("the listed dead letter reports %d attempts", got.Attempts)
	}
}

// A dead-lettered row used to carry run_after set to a backoff — a future
// instant. No claim ever selects a dead_letter row, so nothing reads it, and
// everything that reads the row reads it as "this will be retried", which is the
// opposite of what a dead letter means.
func TestADeadLetterDoesNotCarryAFutureNotBeforeTime(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-runafter")
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   1,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the consumer is down")
		},
	}
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	if _, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if status, _, _ := statusOf(t, pool, tenant); status != "dead_letter" {
		t.Fatalf("the message is %q, want dead_letter", status)
	}

	at := runAfterOf(t, pool, tenant)
	if at.After(time.Now().Add(2 * time.Second)) {
		t.Errorf("the dead letter carries run_after = %s, which is in the future. Nothing will "+
			"ever read it - no claim selects a dead_letter row - and every reader of the row "+
			"reads it as a pending retry", at)
	}
	// The listing is what an operator orders by, so it has to carry a real time.
	letters, err := relay.DeadLetters(ctx, tenant, siteFor(tenant), 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d outbox dead letters, want 1", len(letters))
	}
	if letters[0].DeadLetteredAt.IsZero() {
		t.Error("the listed dead letter has no dead-lettered-at time, so the list cannot be " +
			"ordered by when the message died")
	}
}

// A failed message below the bound still gets a real backoff. The dead-letter
// change must not have taken the retry schedule with it: run_after is the
// not-before time for a failed row, and a failed row with no backoff is retried
// on every pass for ever.
func TestAFailedMessageStillCarriesABackoff(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-backoff")
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   5,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the consumer is down")
		},
	}
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})

	if _, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if status, _, _ := statusOf(t, pool, tenant); status != "failed" {
		t.Fatalf("the message is %q after one failure against a bound of 5", status)
	}
	at := runAfterOf(t, pool, tenant)
	if !at.After(time.Now()) {
		t.Errorf("a failed message carries run_after = %s, which is not in the future, so it is "+
			"retried on the next pass with no backoff at all", at)
	}
}

// Replay is how an operator retries a dead letter after fixing whatever broke it,
// so the retry has to actually happen. It is also the only path that clears the
// attempt count, because the bound that produced the dead letter is spent.
func TestReplayingADeadLetterRetriesIt(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-replay")
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   1,
		LeaseDuration: time.Second,
		Handler: func(context.Context, outbox.Delivery) error {
			return errors.New("the consumer is down")
		},
	}
	put(t, pool, outbox.Message{TenantID: tenant, IdempotencyKey: "k", Payload: []byte(`{"n":1}`)})
	if _, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	letters, err := relay.DeadLetters(ctx, tenant, siteFor(tenant), 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d dead letters, want 1", len(letters))
	}

	// The consumer is fixed.
	delivered := 0
	relay.Handler = func(context.Context, outbox.Delivery) error { delivered++; return nil }
	if err := relay.Replay(ctx, tenant, letters[0].OutboxID); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if letters, err = relay.DeadLetters(ctx, tenant, siteFor(tenant), 10); err != nil {
		t.Fatalf("DeadLetters after replay: %v", err)
	} else if len(letters) != 0 {
		t.Fatalf("%d dead letter(s) remain after an operator replay", len(letters))
	}
	if n, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain after replay: %v", err)
	} else if n != 1 {
		t.Errorf("a replayed message delivered %d time(s), want 1. Replay moved the row to "+
			"pending with its attempt count spent, so the retry dead-letters on its first "+
			"attempt without resetting the count", n)
	}
	if delivered != 1 {
		t.Errorf("the handler saw %d deliveries after the replay, want 1", delivered)
	}

	// Replaying something that is not a dead letter is refused rather than
	// silently resetting a message that is mid-flight.
	if err := relay.Replay(ctx, tenant, outboxID(t, pool, tenant)); err == nil {
		t.Error("replaying a delivered message succeeded; an operator action must not reset a " +
			"row that has already reached a terminal state")
	}
}

// outboxID reads the id of the one message the test enqueued, so the replay
// assertion can name it rather than reusing the id the listing already gave.
func outboxID(t *testing.T, pool *dbx.Pool, tenant string) int64 {
	t.Helper()
	var id int64
	row := pool.For(tenant, "test").QueryRow(context.Background(),
		`SELECT outbox_id FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant)
	defer row.Close()
	if err := row.Scan(&id); err != nil {
		t.Fatalf("read the outbox id: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// O6: the not-before time is the database's clock, not the host's.
// ---------------------------------------------------------------------------

// The claim predicate is `run_after <= now()` — the database's clock. The default
// on insert used to be time.Now().UTC() — the host's. Those are two clocks, and a
// host behind the database writes a not-before time in the past (a delayed
// message fires immediately, skipping its delay) while a host ahead writes one in
// the future (the message sits unclaimed until the skew drains).
//
// The value is compared against the database's clock, so it has to be written
// from that same clock.
//
// The test cannot arrange for the two clocks to disagree — it has no way to move
// the host's — so it proves the property that distinguishes them instead. now() in
// Postgres is the transaction's start timestamp, fixed for the life of the
// transaction, whereas time.Now() is the instant of the call. The enqueue
// therefore happens three seconds into a transaction, and the stored run_after
// says which of the two wrote it:
//
//   - the database's now() gives the transaction's start instant, three seconds
//     before the enqueue;
//   - the host's time.Now() gives the enqueue moment, which is now.
//
// This is the only way to observe the difference on a host whose clock is
// correct, and it is exactly the difference that matters on a host whose clock is
// not.
func TestTheNotBeforeTimeComesFromTheDatabaseClock(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-clock")

	// One transaction spanning the delay, so both candidate values are observable.
	var txStart, stored time.Time
	err := pool.InTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&txStart); err != nil {
			return fmt.Errorf("read the database clock: %w", err)
		}
		time.Sleep(3 * time.Second)
		if err := outbox.Enqueue(ctx, tx, outbox.Message{
			TenantID: tenant, Topic: "incident.raised",
			IdempotencyKey: "k", Payload: []byte(`{"n":1}`),
		}); err != nil {
			return fmt.Errorf("enqueue: %w", err)
		}
		return tx.QueryRow(ctx,
			`SELECT run_after FROM outbox_events WHERE tenant_id = $1 AND idempotency_key = 'k'`,
			tenant).Scan(&stored)
	})
	if err != nil {
		t.Fatalf("enqueue in a transaction: %v", err)
	}

	// Half a second of slack for the round trip, against the three-second gap
	// between the two candidate values.
	const slack = 500 * time.Millisecond
	if drift := stored.Sub(txStart.UTC()); drift > slack || drift < -slack {
		t.Errorf("run_after is %s, which is %s after the transaction started. The value was "+
			"written from a clock other than the database's: now() in Postgres is the "+
			"transaction's start instant, so a value the database wrote lands there and a "+
			"value the host wrote lands at the moment of the call. The claim predicate "+
			"compares run_after against the database's clock, so the two have to be one clock",
			stored, drift)
	}
}

// The delay the caller asked for is still the delay the caller gets. Handing the
// default to the database must not have started ignoring RunAfter.
func TestAnExplicitDelayIsStillHonoured(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-delay")
	// A delay far enough ahead that a message with it cannot be claimed now, and
	// far enough behind that the test does not have to wait for it.
	delayed := time.Now().UTC().Add(12 * time.Hour)
	put(t, pool, outbox.Message{
		TenantID: tenant, IdempotencyKey: "k",
		Payload: []byte(`{"n":1}`), RunAfter: delayed,
	})
	got := runAfterOf(t, pool, tenant)
	if got.Before(delayed.Add(-time.Minute)) || got.After(delayed.Add(time.Minute)) {
		t.Errorf("run_after is %s, want about %s. The caller's delay was not honoured", got, delayed)
	}

	relay := &outbox.Relay{
		Pool:    pool,
		Handler: func(context.Context, outbox.Delivery) error { return nil },
	}
	if n, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	} else if n != 0 {
		t.Errorf("a message delayed by 12h was delivered on the first pass")
	}
}
