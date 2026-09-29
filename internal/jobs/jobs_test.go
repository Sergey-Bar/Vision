package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func open(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t) // skips, rather than failing, with no database
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

func enqueue(t *testing.T, pool *dbx.Pool, tenant, key string) (created bool) {
	return enqueueAt(t, pool, tenant, key, 0)
}

// enqueueAt is enqueue with an explicit retry bound, for the test that drives
// attempt_count past the point where an unclamped backoff used to raise.
func enqueueAt(t *testing.T, pool *dbx.Pool, tenant, key string, maxAttempts int) (created bool) {
	t.Helper()
	err := pool.InTenant(context.Background(), dbx.TenantContext{TenantID: tenant, ActorID: "test"},
		func(tx pgx.Tx) error {
			var err error
			created, _, err = jobs.New(pool).Enqueue(context.Background(), tx, jobs.Enqueue{
				TenantID: tenant, Queue: jobs.QueueVLM, IdempotencyKey: key,
				Payload: []byte(`{"evidence":"x"}`), MaxAttempts: maxAttempts,
			})
			return err
		})
	if err != nil {
		t.Fatalf("enqueue %s: %v", key, err)
	}
	return created
}

// A job is delivered to exactly one worker.
//
// What this pins is exclusivity, which is the invariant that matters: two
// workers running the same inference is a duplicate side effect, and invariant 1
// forbids it. Nothing asserted it, and the property is the reason a Postgres
// queue with `FOR UPDATE` can replace Kafka here at all.
//
// It does not pin *why* the claim is exclusive. Removing `SKIP LOCKED` leaves
// this test green, because `FOR UPDATE` alone still makes the loser re-evaluate
// the predicate against the row the winner just updated and take nothing. What
// `SKIP LOCKED` buys is that the loser does not wait for the winner, and that is
// a throughput property - a test for it would be a timing assertion, and a
// timing assertion on a shared database is a test that goes red on a busy
// machine and teaches its readers to ignore it. Exclusivity is asserted; the
// cost of losing it is a note.
func TestAJobIsClaimedByExactlyOneWorker(t *testing.T) {
	pool := open(t)
	tenant, _, _ := testutil.NewTenant(t, testutil.Pool(t))
	firstCreated := enqueue(t, pool, tenant, "single-claim")
	if !firstCreated {
		t.Fatal("the first enqueue reported no work created")
	}

	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := map[string]int{}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}}
			lease, err := w.Claim(context.Background(), tenant, fmt.Sprintf("worker-%d", i), 10)
			if err != nil {
				t.Errorf("worker %d: %v", i, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, j := range lease {
				claimed[j.ID]++
			}
		}(i)
	}
	wg.Wait()

	if len(claimed) != 1 {
		t.Fatalf("%d distinct jobs were claimed, want 1: %v", len(claimed), claimed)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Errorf("job %s was claimed %d times. Two workers running the same inference "+
				"is a duplicate side effect, which is the one thing invariant 1 forbids", id, n)
		}
	}
}

// An idempotency key is a promise that a second enqueue is not a second piece of
// work. A retried poll, a restarted producer and a duplicated webhook all land
// here, and the key is the only thing standing between them and two inferences
// of the same evidence.
func TestAnIdempotencyKeyCreatesOneJob(t *testing.T) {
	pool := open(t)
	tenant, _, _ := testutil.NewTenant(t, testutil.Pool(t))
	ctx := context.Background()

	created := enqueue(t, pool, tenant, "same-key")
	if !created {
		t.Fatal("the first enqueue reported no work created")
	}
	created = enqueue(t, pool, tenant, "same-key")
	if created {
		t.Error("a repeated idempotency key created a second job. A retried poll, a restarted " +
			"producer and a duplicated webhook all land here, and the key is the only thing " +
			"standing between them and two inferences of the same evidence")
	}

	var n int
	if err := pool.For(tenant, "test").QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE tenant_id = $1 AND idempotency_key = 'same-key'`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d jobs carry the idempotency key, want 1", n)
	}
}

// The same key on a different queue is a different job: the queues are for
// different work, and collapsing them would drop one of the two.
func TestAnIdempotencyKeyIsScopedToItsQueue(t *testing.T) {
	pool := open(t)
	tenant, _, _ := testutil.NewTenant(t, testutil.Pool(t))
	ctx := context.Background()

	ids := map[string]string{}
	for _, q := range []jobs.Queue{jobs.QueueVLM, jobs.QueueRuleBulk} {
		var id string
		err := pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "test"},
			func(tx pgx.Tx) error {
				_, got, err := jobs.New(pool).Enqueue(ctx, tx, jobs.Enqueue{
					TenantID: tenant, Queue: q, IdempotencyKey: "shared", Payload: []byte(`{}`),
				})
				id = got
				return err
			})
		if err != nil {
			t.Fatalf("enqueue on %s: %v", q, err)
		}
		ids[string(q)] = id
	}
	if ids[string(jobs.QueueVLM)] == ids[string(jobs.QueueRuleBulk)] {
		t.Errorf("two queues with the same idempotency key produced one job (%v)", ids)
	}
}

// A retry bound with no reachable end.
//
// The backoff was `least(900, 5 * power(2, attempt_count)::int)`, which casts
// an unbounded exponent to int32 *before* clamping: 2^29 - 5 is 2,684,354,560,
// so the cast raises at attempt_count 29. The UPDATE rolled back, `Fail` returned
// an error instead of a status, the row stayed leased, the lease expired, the
// job was reclaimed - and the whole thing repeated. The worker aborted on every
// pass and the job never reached dead_letter at all.
//
// `max_attempts` had only a `>= 0` check, so a bound above 28 was accepted and
// reached. There is now a ceiling on the column, and the test drives the counter
// well past where the old expression died rather than trusting the arithmetic.
func TestARetryBoundAlwaysReachesATerminalState(t *testing.T) {
	pool := open(t)
	tenant, _, _ := testutil.NewTenant(t, testutil.Pool(t))
	ctx := context.Background()

	const maxAttempts = 100
	created := enqueueAt(t, pool, tenant, "unbounded", maxAttempts)
	if !created {
		t.Fatal("the job was not created")
	}

	w := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}}
	for i := 0; i < 40; i++ {
		lease, err := w.Claim(ctx, tenant, "worker", 10)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if len(lease) == 0 {
			// The row is waiting on its backoff. That is the bounded behaviour.
			break
		}
		status, err := w.Fail(ctx, tenant, lease[0].ID, "worker", errors.New("the consumer is down"))
		if err != nil {
			t.Fatalf("attempt %d: Fail returned %v. A raising backoff on the failure path "+
				"leaves the row leased for ever, which is the defect this is about", i, err)
		}
		if status == jobs.StatusDeadLetter {
			t.Fatalf("the job dead-lettered after %d attempts against a bound of %d",
				i+1, maxAttempts)
		}
		// Skip the backoff so the loop does not wait out 5, 10, 20 - seconds.
		// The retry still goes through the claim.
		testutil.Exec(t, pool.Pool,
			`UPDATE jobs SET run_after = now() - interval '1 second' WHERE tenant_id = $1`, tenant)
	}
}

// A worker's lease is a claim on the job, and only that worker may finish it. A
// worker whose lease expired and was reclaimed by somebody else must not be able
// to complete the row: two workers writing the same terminal state is exactly
// the duplicate side effect the lease exists to prevent.
func TestOnlyTheLeaseHolderMayCompleteAJob(t *testing.T) {
	pool := open(t)
	tenant, _, _ := testutil.NewTenant(t, testutil.Pool(t))
	_ = enqueue(t, pool, tenant, "lease-holder")
	ctx := context.Background()

	holder := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}, LeaseDuration: time.Second}
	thief := &jobs.Worker{Enqueuer: jobs.Enqueuer{Pool: pool}}

	lease, err := holder.Claim(ctx, tenant, "holder", 1)
	if err != nil || len(lease) != 1 {
		t.Fatalf("holder claimed %d jobs (err %v), want 1", len(lease), err)
	}
	id := lease[0].ID

	// Wait out the lease and let the thief take it.
	deadline := time.Now().Add(5 * time.Second)
	var stolen []jobs.Job
	for time.Now().Before(deadline) {
		stolen, err = thief.Claim(ctx, tenant, "thief", 1)
		if err != nil {
			t.Fatalf("thief claim: %v", err)
		}
		if len(stolen) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(stolen) != 1 {
		t.Fatal("the job was never reclaimed after its lease expired; the lease is decorative")
	}

	if err := holder.Complete(ctx, tenant, id, "holder"); err == nil {
		t.Error("a worker whose lease had been reclaimed still completed the job. Two workers " +
			"writing the same terminal state is a duplicate side effect")
	}
	if err := thief.Complete(ctx, tenant, id, "thief"); err != nil {
		t.Errorf("the lease holder could not complete its job: %v", err)
	}
}
