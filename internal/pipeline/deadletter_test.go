package pipeline_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// poison replaces the stored envelope with one the adapter cannot derive a
// record from, so normalization is impossible from then on. The inbox row stays:
// the event was accepted, and an accepted event is never deleted to make a test
// tidy.
func poison(t *testing.T, tenant string) {
	t.Helper()
	testutil.Exec(t, testutil.Pool(t),
		`UPDATE inbox_events SET payload = '[]'::jsonb WHERE tenant_id = $1`, tenant)
}

func inboxState(t *testing.T, tenant, sourceEventID string) (string, int, string) {
	t.Helper()
	var status, reason string
	var attempts int
	if err := testutil.Pool(t).QueryRow(context.Background(), `
		SELECT status, attempt_count, COALESCE(dead_letter_reason, last_error, '')
		  FROM inbox_events WHERE tenant_id = $1 AND source_event_id = $2`,
		tenant, sourceEventID).Scan(&status, &attempts, &reason); err != nil {
		t.Fatalf("read the inbox row for %q in %q: %v", sourceEventID, tenant, err)
	}
	return status, attempts, reason
}

// An event the platform can never normalize has to reach a terminal state.
//
// Every failure path in the consume loop used to log and return, which meant
// inbox.Fail was never called. Nothing incremented to the retry bound, so the
// row was reclaimed every lease period for the life of the process: no backoff,
// no terminal state, and a dead-letter table that stayed empty while the backlog
// grew. That is invariant 1 broken, and the dead-letter surface the operator is
// told to watch was the thing that never filled.
// A worker that has lost its lease must not write a terminal state.
//
// The interleaving: worker A claims and is inside Normalize. Its lease lapses.
// Worker B claims the row and commits a successful normalize. A's normalize then
// fails on something downstream and calls Fail. `Fail` used to match on the key
// alone, so it flipped a fully processed event to `failed` — or to `dead_letter`,
// where the operator's dead-letter console lists an event whose canonical event,
// graph edges and incident all exist.
//
// The unit under test is the ownership match, so the test stages it directly
// rather than reproducing the race, which needs a hook in the middle of
// Normalize that the product does not have and should not grow for a test.
func TestAWorkerThatLostItsLeaseCannotWriteATerminalState(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()
	h.loops.PollOnce(ctx)

	w := h.loops.Deps.Inbox

	// Worker A holds the lease.
	first, err := w.ClaimFor(ctx, h.tenant, "worker-a", 10, 50*time.Millisecond)
	if err != nil || len(first) != 1 {
		t.Fatalf("worker A claimed %d rows (err %v), want 1", len(first), err)
	}
	// The key comes from the lease rather than being written out here: the
	// integration id is whatever the harness registered, and a test that
	// reconstructs it is a test with a second, silent failure mode.
	key := first[0].Key

	// Worker B takes it after the lease lapses.
	deadline := time.Now().Add(5 * time.Second)
	var second []inbox.Lease
	for time.Now().Before(deadline) {
		second, err = w.ClaimFor(ctx, h.tenant, "worker-b", 10, 30*time.Second)
		if err != nil {
			t.Fatalf("worker B: %v", err)
		}
		if len(second) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(second) != 1 {
		t.Fatal("worker B never reclaimed the row; the lease is decorative")
	}

	// The interleaving that matters is A reporting *while B is still working*,
	// not after B has finished. Once B completes, the status is `processed` and
	// the status check alone excludes A — so a test that lets B finish first
	// passes even with no ownership check at all, and proves nothing. Here the
	// row is still `processing` and is owned by B, which is exactly the window
	// in which A's late write lands.
	statusWhileBWorks, ownerWhileBWorks, err := inboxRow(t, h.tenant, key.SourceEventID)
	if err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if statusWhileBWorks != string(inbox.StatusProcessing) || ownerWhileBWorks != "worker-b" {
		t.Fatalf("the row is status=%q owner=%q, want processing/worker-b; the test is not "+
			"staging the window it claims to", statusWhileBWorks, ownerWhileBWorks)
	}

	// Worker A, whose lease lapsed, now reports a failure. It must be refused.
	if status, err := w.Fail(ctx, key, "worker-a",
		errors.New("A's normalize failed after its lease lapsed"), 2); err == nil {
		t.Errorf("a worker that had lost its lease recorded status=%q against a row another "+
			"worker is still processing. The dead-letter console would list an event whose "+
			"canonical event, edges and incident all exist", status)
	}
	// And it must not be able to complete it either.
	if err := w.Complete(ctx, key, "worker-a", "evt-from-a"); err == nil {
		t.Error("a worker that had lost its lease overwrote the canonical event id of a row " +
			"another worker is still processing")
	}

	// The row is untouched, and worker B can still finish its own work.
	if status, _, err := inboxRow(t, h.tenant, key.SourceEventID); err != nil {
		t.Fatalf("read the row: %v", err)
	} else if status != string(inbox.StatusProcessing) {
		t.Fatalf("the row is %q after the late writes, want processing", status)
	}
	if err := w.Complete(ctx, key, "worker-b", "evt-from-b"); err != nil {
		t.Fatalf("worker B could not complete its own row: %v", err)
	}

	status, _, err := inboxRow(t, h.tenant, key.SourceEventID)
	if err != nil {
		t.Fatalf("read the inbox row: %v", err)
	}
	if status != string(inbox.StatusProcessed) {
		t.Errorf("the row is %q after the late writes, want processed", status)
	}

	var canonical string
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx, `
		SELECT coalesce(canonical_event_id, '') FROM inbox_events
		 WHERE tenant_id = $1 AND source_event_id = $2`,
		h.tenant, key.SourceEventID).Scan(&canonical); err != nil {
		t.Fatalf("read the canonical id: %v", err)
	}
	if canonical != "evt-from-b" {
		t.Errorf("the row's canonical event is %q, want the one worker B wrote", canonical)
	}
}

// inboxRow reads one inbox row's status and lease owner.
func inboxRow(t *testing.T, tenant, sourceEventID string) (string, string, error) {
	t.Helper()
	pool := testutil.Pool(t)
	var status, owner string
	err := pool.QueryRow(context.Background(),
		`SELECT status, coalesce(lease_owner, '') FROM inbox_events
		 WHERE tenant_id = $1 AND source_event_id = $2`, tenant, sourceEventID).
		Scan(&status, &owner)
	return status, owner, err
}

func TestAPermanentlyUnnormalizableEventReachesADeadLetter(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()
	h.loops.PollOnce(ctx)
	poison(t, h.tenant)

	deadline := time.Now().Add(30 * time.Second)
	for pass := 1; ; pass++ {
		h.loops.ConsumeOnce(ctx)

		status, attempts, reason := inboxState(t, h.tenant, "e1")
		if pass == 1 {
			// The first failure has to be recorded, not merely logged. A row left
			// in 'processing' is one whose retry budget nobody is counting, which
			// is the whole defect: the lease lapses, the claim takes it again, and
			// the count only ever reaches the ceiling by accident.
			if status != string(inbox.StatusFailed) {
				t.Fatalf("after the first failed consume the row is %q, want failed. Every "+
					"failure path used to log and return, so nothing recorded the attempt and "+
					"the event was reclaimed for the life of the process", status)
			}
		}
		if status == string(inbox.StatusDeadLetter) {
			if reason == "" {
				t.Error("the event is a dead letter with no reason; an operator looking at the " +
					"dead-letter table sees a row and no explanation")
			}
			if attempts < 2 {
				t.Errorf("the event died after %d attempt(s); the writer's bound is 5", attempts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %d consume passes the event is %q with %d attempt(s) and no "+
				"terminal state. An event that can never normalize must be dead-lettered, not "+
				"retried for the life of the process", pass, status, attempts)
		}
		// Clear the backoff so the loop does not have to wait out a real retry
		// delay. The retry still goes through the claim, so the backoff's absence
		// here is not what makes the event terminal.
		testutil.Exec(t, testutil.Pool(t),
			`UPDATE inbox_events SET lease_expires_at = now() - interval '1 second' WHERE tenant_id = $1`,
			h.tenant)
		time.Sleep(10 * time.Millisecond)
	}
}

// The crash path. A worker killed mid-handle never calls Fail — there is no
// cooperating code left to call it — so the reclaim path is what has to notice
// that the retry budget is spent. Without the ceiling the row is reclaimed for
// ever and never dead-letters, which is the same defect reached by a different
// route, and the more common one on a site that loses power.
func TestAWorkerKilledMidHandleStillReachesADeadLetter(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()
	h.loops.PollOnce(ctx)
	poison(t, h.tenant)

	const passes = 12
	for i := 0; i < passes; i++ {
		// Each round is a claim followed by a simulated death: the row is left
		// status=processing with an expired lease and nothing able to process it.
		testutil.Exec(t, testutil.Pool(t), `
			UPDATE inbox_events
			   SET status = 'processing', lease_owner = 'dead-worker',
			       lease_expires_at = now() - interval '1 minute'
			 WHERE tenant_id = $1`, h.tenant)
		h.loops.ConsumeOnce(ctx)
	}

	status, attempts, _ := inboxState(t, h.tenant, "e1")
	if status != string(inbox.StatusDeadLetter) {
		t.Errorf("after %d crashed attempts the event is %q, want dead_letter. A worker that "+
			"dies mid-handle never calls Fail, so the claim itself has to retire the row once "+
			"the budget is spent", attempts, status)
	}
	if attempts > passes {
		t.Errorf("the event was attempted %d times across %d passes; the ceiling is not "+
			"enforced on the reclaim path", attempts, passes)
	}
}

// The backoff is the other half of "bounded". A counted failure that is
// immediately re-claimable is still a busy retry, and on a source that produces
// a million poison envelopes it is the difference between a bounded cost and an
// unbounded one.
func TestAFailedEventIsHeldOffUntilItsBackoffExpires(t *testing.T) {
	h := setup(t, bundle, `[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z","Source":"Entrance Camera 01 [cam-1]"}
	]`)
	ctx := context.Background()
	h.loops.PollOnce(ctx)
	poison(t, h.tenant)

	h.loops.ConsumeOnce(ctx)

	status, _, _ := inboxState(t, h.tenant, "e1")
	if status != string(inbox.StatusFailed) {
		t.Fatalf("after one failed consume the row is %q, want failed", status)
	}

	var until *time.Time
	if err := h.loops.Deps.Pool.For(h.tenant, "test").QueryRow(ctx, `
		SELECT lease_expires_at FROM inbox_events
		 WHERE tenant_id = $1 AND source_event_id = 'e1'`, h.tenant).Scan(&until); err != nil {
		t.Fatalf("read the backoff: %v", err)
	}
	if until == nil {
		t.Fatal("a failed row has no retry time; it is re-claimable on the next tick")
	}
	if d := time.Until(*until); d <= 0 {
		t.Errorf("the retry time is %s away, so the row is already re-claimable", d)
	}

	// And a claim before the retry time does not take it.
	leases, err := h.loops.Deps.Inbox.ClaimFor(ctx, h.tenant, "greedy", 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim during the backoff: %v", err)
	}
	if len(leases) != 0 {
		t.Errorf("a claim during the backoff took %d row(s); the backoff is decorative", len(leases))
	}
}
