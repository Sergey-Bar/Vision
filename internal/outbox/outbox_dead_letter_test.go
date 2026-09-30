package outbox_test

// This file holds the control for outbox_events' dead-letter reason: the column
// exists, the terminal transition writes it, the listing reads it, and the
// listing's own query is supported by an index shaped like the query.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/outbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// deadLetter drives one message to the dead-letter state and returns the relay
// and the listing an operator would see.
// The site is a parameter because the listing is scoped to one since migration 21:
// site_isolation is RESTRICTIVE, so a dead-letter console that named only its
// tenant would show an operator an empty list.
func deadLetter(t *testing.T, pool *dbx.Pool, tenant, site, cause string) (*outbox.Relay, outbox.DeadLetter) {
	t.Helper()
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   1,
		LeaseDuration: time.Second,
		Handler:       func(context.Context, outbox.Delivery) error { return errors.New(cause) },
	}
	put(t, pool, outbox.Message{
		TenantID: tenant, IdempotencyKey: "k",
		Topic: "notify_operator", Payload: []byte(`{"n":1}`),
	})
	if _, err := relay.DrainOnce(context.Background(), tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if status, _, _ := statusOf(t, pool, tenant); status != "dead_letter" {
		t.Fatalf("the message is %q after one failed attempt against a bound of 1", status)
	}
	letters, err := relay.DeadLetters(context.Background(), tenant, site, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d outbox dead letters, want 1", len(letters))
	}
	return relay, letters[0]
}

// The reason has its own column, and the terminal transition is what writes it.
//
// It did not. `inbox_events` and `jobs` both carry a `dead_letter_reason`
// column; `outbox_events` carried none, so a dead letter's reason was read out
// of `last_error` — the column that is rewritten on every failed attempt and
// cleared on delivery. The one question an operator has about a dead letter was
// therefore answered by whatever the *last* attempt happened to say, which is
// the one attempt that failed for the reason the retry exists to work around.
//
// This asserts the column by name rather than through the listing, because the
// listing is what the change also touched and a test that only reads the listing
// cannot tell the two halves apart: with the column present and unread, or
// absent and read, the listing can look the same.
func TestADeadLetterCarriesItsReasonInItsOwnColumn(t *testing.T) {
	pool := open(t)
	// A real tenant and site rather than a synthetic name: the listing is scoped to
	// a site, and a scoped read of a site that does not exist is a read of nothing.
	tenant, site, _ := testutil.NewTenant(t, pool.Pool)
	_, listed := deadLetter(t, pool, tenant, site, "the operator directory is unreachable")

	var reason *string
	if err := pool.For(tenant, "test").QueryRow(context.Background(), `
		SELECT dead_letter_reason FROM outbox_events
		 WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant).Scan(&reason); err != nil {
		t.Fatalf("read dead_letter_reason: %v", err)
	}
	if reason == nil {
		t.Fatal("the dead letter's own reason column is NULL. The reason is being carried by " +
			"last_error, which is rewritten on every attempt and cleared on delivery, so the " +
			"row's answer to \"why did this stop\" is whatever the last attempt happened to say")
	}
	if !strings.Contains(*reason, "the operator directory is unreachable") {
		t.Errorf("the dead-letter reason is %q, which does not contain the failure the handler "+
			"reported", *reason)
	}
	if !strings.Contains(listed.Reason, "the operator directory is unreachable") {
		t.Errorf("the listing reports %q, so the column is written and never read", listed.Reason)
	}
}

// A row that is still being retried has no dead-letter reason. The reason belongs
// to the terminal state; giving a live retry a reason makes the row say it died
// when it has not, which is the same class of lie as a future not-before time on
// a dead letter.
func TestAMessageBelowTheAttemptBoundHasNoDeadLetterReason(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant := uniqueTenant("t-live-reason")
	relay := &outbox.Relay{
		Pool:          pool,
		MaxAttempts:   8,
		LeaseDuration: time.Second,
		Handler:       func(context.Context, outbox.Delivery) error { return errors.New("transient") },
	}
	put(t, pool, outbox.Message{
		TenantID: tenant, IdempotencyKey: "k",
		Topic: "notify_operator", Payload: []byte(`{"n":1}`),
	})
	if _, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if status, _, _ := statusOf(t, pool, tenant); status != "failed" {
		t.Fatalf("the message is %q after one failure against a bound of 8", status)
	}

	var reason *string
	if err := pool.For(tenant, "test").QueryRow(ctx, `
		SELECT dead_letter_reason FROM outbox_events
		 WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant).Scan(&reason); err != nil {
		t.Fatalf("read dead_letter_reason: %v", err)
	}
	if reason != nil {
		t.Errorf("a message that is still being retried carries dead_letter_reason %q. It has "+
			"not died; the next attempt may yet succeed", *reason)
	}
}

// The listing reads the dead-letter column, and only falls back to last_error.
//
// The two are set from the same expression, so a real row never disagrees about
// its reason. This puts a disagreement in by hand to prove which one the
// operator is shown: if the listing read last_error, the row would report the
// last transient failure rather than the terminal one, which is the whole reason
// the second column exists.
func TestTheListingReportsTheDeadLetterReasonRatherThanTheLastTransientFailure(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant, site, _ := testutil.NewTenant(t, pool.Pool)
	relay, _ := deadLetter(t, pool, tenant, site, "the terminal cause")

	// `last_error` is rewritten on every attempt and cleared on delivery, so by the time
	// a message is dead its content is the *current* failure text, not the reason it
	// stopped. `dead_letter_reason` is written once by the terminal transition and
	// holds the cause. The test below rewrites `last_error` to a plausible but
	// misleading transient failure and checks the listing still names the real one.
	// dead_letter_reason is the reason the message stopped.
	if _, err := pool.For(tenant, "test").Exec(ctx,
		`UPDATE outbox_events SET last_error = 'a later, misleading transient failure'
		  WHERE tenant_id = $1`, tenant); err != nil {
		t.Fatalf("rewrite last_error: %v", err)
	}

	letters, err := relay.DeadLetters(ctx, tenant, site, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d dead letters, want 1", len(letters))
	}
	if !strings.Contains(letters[0].Reason, "the terminal cause") {
		t.Errorf("the listing reports %q. It is reading last_error, so a dead letter explains "+
			"the last transient failure rather than why the message stopped", letters[0].Reason)
	}
}

// A replayed message that then succeeds must not carry the reason it died for
// into a delivered state. The operator said "I have fixed whatever this was" by
// replaying it, and the stale reason is a statement about a state the row is no
// longer in.
func TestAReplayedMessageDoesNotCarryAStaleDeadLetterReason(t *testing.T) {
	pool := open(t)
	ctx := context.Background()
	tenant, site, _ := testutil.NewTenant(t, pool.Pool)
	relay, letter := deadLetter(t, pool, tenant, site, "the original cause")

	if err := relay.Replay(ctx, tenant, letter.OutboxID); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	var afterReplay *string
	if err := pool.For(tenant, "test").QueryRow(ctx, `
		SELECT dead_letter_reason FROM outbox_events
		 WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant).Scan(&afterReplay); err != nil {
		t.Fatalf("read dead_letter_reason after the replay: %v", err)
	}
	if afterReplay != nil && *afterReplay != "" {
		t.Errorf("the replayed message still carries dead_letter_reason %q. The message is "+
			"pending again and the reason is stale the moment the operator replays it", *afterReplay)
	}

	// And after it is delivered for real.
	relay.Handler = func(context.Context, outbox.Delivery) error { return nil }
	if _, err := relay.DrainOnce(ctx, tenant, "relay-1", 10); err != nil {
		t.Fatalf("drain after the replay: %v", err)
	}
	var afterDelivery *string
	if err := pool.For(tenant, "test").QueryRow(ctx, `
		SELECT dead_letter_reason FROM outbox_events
		 WHERE tenant_id = $1 AND idempotency_key = 'k'`, tenant).Scan(&afterDelivery); err != nil {
		t.Fatalf("read dead_letter_reason after delivery: %v", err)
	}
	if afterDelivery != nil && *afterDelivery != "" {
		t.Errorf("a delivered message carries dead_letter_reason %q. A delivered message has "+
			"no reason to have died", *afterDelivery)
	}
}

// The listing is `WHERE tenant_id = $1 AND status = 'dead_letter' ORDER BY
// run_after DESC`, and before the index it was served by neither of the two the
// table had: outbox_pending_idx is partial on (pending, failed) and so contains
// no dead letter at all, and outbox_tenant_idx is ordered by created_at, which
// is the one column this query does not sort on. So answering "what just broke"
// meant reading the tenant's whole outbox history and sorting it in memory.
//
// The predicate and the column order are asserted, not just the existence,
// because an index on outbox_events that is merely present is a second thing to
// keep in step with the query rather than a control.
func TestTheDeadLetterListingIsSupportedByAnIndexShapedLikeTheListing(t *testing.T) {
	pool := open(t)
	var def string
	if err := pool.For(uniqueTenant("t-index"), "test").QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes
		  WHERE schemaname = 'public' AND tablename = 'outbox_events'
		    AND indexname = 'outbox_dead_letter_idx'`).Scan(&def); err != nil {
		t.Fatalf("outbox_dead_letter_idx is not on outbox_events: %v. The dead-letter listing "+
			"is a sequential scan of the tenant's whole outbox history", err)
	}
	norm := strings.Join(strings.Fields(def), " ")
	for _, want := range []string{"(tenant_id, run_after DESC)", "dead_letter"} {
		if !strings.Contains(norm, want) {
			t.Errorf("the index definition is\n\t%s\nwhich does not contain %q. The listing "+
				"filters on (tenant_id, status) and orders by run_after DESC, and an index "+
				"that does not match that predicate and sort is a second thing to keep in "+
				"step with the query", norm, want)
		}
	}
	if !strings.Contains(norm, "WHERE") {
		t.Errorf("the index is not partial.\n\t%s\nA partial index on the dead-letter state "+
			"keeps it small: the rows that are actively being retried are the ones the "+
			"existing indexes already cover", norm)
	}
}
