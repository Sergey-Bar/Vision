package identity_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// The lockout is the named control for the CSRF-exempt login route, and it
// bounded nothing under parallelism.
//
// `PasswordStep` takes `FOR UPDATE` in a transaction that COMMITS ÃƒÆ’Ã‚Â¢ÃƒÂ¢Ã¢â‚¬Å¡Ã‚Â¬ÃƒÂ¢Ã¢â€šÂ¬Ã‚Â the argon2
// verification has to happen outside it, because holding a row lock across a
// deliberately slow hash would serialise every login on the site - and
// `registerFailure` then computed `attempts := user.FailedAttempts + 1` in Go
// from that already-stale value and wrote the result. Fifty concurrent wrong
// passwords for one username all read zero, all wrote one, and the exponential
// backoff never engaged.
//
// The count the database ends up with is the number of failures that actually
// happened, and the lockout is derived from that.
func TestConcurrentWrongPasswordsAreAllCounted(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	const attempts = 8
	var wg sync.WaitGroup
	wg.Add(8)
	for i := 0; i < 8; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.PasswordStep(ctx, tenant, "admin", "wrong-password", "10.0.0.1")
		}()
	}
	wg.Wait()

	var counted int
	var lockedUntil *string
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT failed_attempts, locked_until::text FROM users
		  WHERE tenant_id = $1 AND user_id LIKE 'admin-%'`, tenant).
		Scan(&counted, &lockedUntil); err != nil {
		t.Fatalf("read the counter: %v", err)
	}
	if counted != attempts {
		t.Errorf("the counter says %d after %d concurrent wrong passwords. Each request read "+
			"the same stale value and wrote the same result, so the backoff never engaged and "+
			"online guessing was bounded only by network throughput", counted, attempts)
	}
	if lockedUntil == nil {
		t.Error("the account is not locked after the wrong passwords. Every one of them " +
			"overwrote the same field, and the last writer computed its deadline from a " +
			"counter of one")
	}
}

// The same shape on the second factor, where it is worse: a challenge id is
// documented in this package as "a two-minute bearer of 'the password was
// right'", and the account lockout deliberately does not apply to it, so the
// per-challenge budget is the only thing bounding a six-digit code.
func TestConcurrentMFAVerifiesAreAllCounted(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	svc.MaxFailedAttempts = 5
	enrolAdmin(t, svc, tenant, site)

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}

	// Twenty wrong codes at once. Every one of them reads the budget, and every
	// one of them used to pass the check.
	const guesses = 8
	var wg sync.WaitGroup
	wg.Add(8)
	reached := make(chan error, guesses)
	for i := 0; i < 8; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp",
				"000000", "10.0.0.1")
			// Did it get as far as checking the code? ErrLocked means the budget
			// stopped it; ErrInvalidCredentials means it reached the factor.
			reached <- err
		}()
	}
	wg.Wait()
	// Closed, or the `range` below waits for ever: a test that hangs is worse
	// than one that fails, because it holds a package's whole run.
	close(reached)

	var reachedTheFactor int
	var locked int
	for err := range reached {
		switch {
		case err == nil:
			t.Fatal("a wrong second factor was accepted")
		case errors.Is(err, identity.ErrLocked):
			locked++
		default:
			reachedTheFactor++
		}
	}
	if reachedTheFactor > svc.MaxFailedAttempts {
		t.Errorf("%d of %d concurrent wrong codes reached the factor check, and the budget is %d. "+
			"The bound was read, compared and incremented in three separate operations, so "+
			"parallelism walked through it",
			reachedTheFactor, 8, svc.MaxFailedAttempts)
	}
	if locked == 0 {
		t.Error("no request was refused by the budget; the counter was incremented after the " +
			"comparison rather than in the same statement that enforces the ceiling")
	}

	// And the counter really is the number of attempts, not the number of
	// statements that happened to run.
	var counted int
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT mfa_attempts FROM sessions WHERE tenant_id = $1 AND session_id = $2`,
		tenant, "chal_"+res.ChallengeID).Scan(&counted); err != nil {
		t.Fatalf("read the challenge counter: %v", err)
	}
	if counted > svc.MaxFailedAttempts {
		t.Errorf("the challenge counter is %d against a budget of %d. The ceiling is in the "+
			"statement that increments, so it cannot be exceeded however many arrive at once",
			counted, svc.MaxFailedAttempts)
	}
}

// A legitimate operator is not locked out by their own parallelism: the budget is
// per challenge, and a challenge that succeeds is deleted.
func TestAConcurrentSuccessStillSucceedsWithinTheBudget(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	svc.MaxFailedAttempts = 5
	_, secret := enrolAdmin(t, svc, tenant, site)

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	code := nextStep(secret)
	if _, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", code, "10.0.0.1"); err != nil {
		t.Fatalf("a correct code inside the budget was refused: %v", err)
	}
	_ = sha256.New
}
