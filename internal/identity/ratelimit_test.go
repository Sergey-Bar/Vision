package identity_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/identity"
)

// The per-source login rate limit.
//
// The threat model records credential stuffing as mitigated by a per-source
// limit (docs/threat-model.md A3.5) and the site configuration publishes the
// number it uses (`console.loginRateLimitPerMinute` in
// contracts/json-schema/site-config.schema.json). Before this, neither
// existed in code: there was an ACCOUNT lockout and nothing about a SOURCE,
// so one address could try every account in the tenant, each of them once,
// and never touch a lockout at all.
//
// These tests are about the control. The account lockout has its own.

// TestALoginBurstFromOneSourceIsRefusedAfterTheLimit is the control itself.
//
// The credentials are CORRECT. That is deliberate: the point of a per-source
// limit is that it bounds a source's throughput whatever the source knows, and
// a test that used wrong passwords would be measuring the account lockout
// instead - which is a different control, on a different subject, and is
// already tested.
func TestALoginBurstFromOneSourceIsRefusedAfterTheLimit(t *testing.T) {
	now := clock
	svc, tenant, site, _ := serviceWith(t, func() time.Time { return now }, 0)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	limit := identity.DefaultLoginRateLimitPerMinute
	for i := 0; i < limit; i++ {
		if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d of %d from one source was refused: %v", i+1, limit, err)
		}
	}

	_, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("attempt %d from one source returned %v, want ErrRateLimited", limit+1, err)
	}
}

// The limit is per SOURCE, and a limit that were per install would be a
// denial of service with a lever on it: one attacker would lock every
// operator on the host out of their console at once.
func TestTheRateLimitIsPerSourceAndNotPerInstall(t *testing.T) {
	now := clock
	svc, tenant, site, _ := serviceWith(t, func() time.Time { return now }, 0)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	limit := identity.DefaultLoginRateLimitPerMinute
	for i := 0; i < limit; i++ {
		if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d of %d from one source was refused: %v", i+1, limit, err)
		}
	}
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("the source's budget was not exhausted: %v", err)
	}

	// A different address is a different source, with its own budget.
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.2"); err != nil {
		t.Fatalf("a different source was refused because another source was limited: %v", err)
	}
}

// The budget is per minute, not for ever. A throttle that never reopens is a
// ban with no appeal, and one source having a bad minute must not cost an
// operator their console for the rest of the shift.
func TestTheRateLimitWindowResets(t *testing.T) {
	now := clock
	svc, tenant, site, _ := serviceWith(t, func() time.Time { return now }, 0)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	limit := identity.DefaultLoginRateLimitPerMinute
	for i := 0; i < limit; i++ {
		if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d of %d from one source was refused: %v", i+1, limit, err)
		}
	}
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("the source's budget was not exhausted: %v", err)
	}

	// A minute and a second later the window has rolled over the attempts
	// that spent it.
	now = now.Add(time.Minute + time.Second)
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
		t.Fatalf("the source was still refused after its window had passed: %v", err)
	}
}

// The limit is a deployment decision and it is published as one, so it has to
// be settable. A site with six operators behind one address raises it; the
// number is named in the configuration for exactly that reason.
func TestTheRateLimitIsConfigurable(t *testing.T) {
	now := clock
	svc, tenant, site, _ := serviceWith(t, func() time.Time { return now }, 2)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
			t.Fatalf("attempt %d was refused: %v", i+1, err)
		}
	}
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("a configured limit of two was not applied: %v", err)
	}
}

// The second-factor route has its own budget.
//
// It is a different route with a different expensive operation - guessing a
// six-digit code rather than checking a passphrase - and the published API
// gives it its own 429. Sharing one budget between the two would mean a
// legitimate sign-in, which is a password step AND a code, spent two of the
// five and was refused on the third.
func TestTheSecondFactorRouteHasItsOwnBudget(t *testing.T) {
	now := clock
	svc, tenant, site, _ := serviceWith(t, func() time.Time { return now }, 2)
	ctx := context.Background()

	admin, secret := enrolAdmin(t, svc, tenant, site)
	_ = admin

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	if res.ChallengeID == "" {
		t.Fatal("the password step produced no challenge to verify")
	}

	// A code that is deterministically wrong: the verifier accepts the step
	// it is at and the half-steps either side of it, so a code from ten
	// minutes away matches none of the three.
	wrong := rfc6238Code(t, secret, now.Add(10*time.Minute), sha256.New)

	// Two verifications are inside the configured budget. The codes are wrong
	// on purpose: this is about how many attempts the route accepts, not
	// about whether a code is right.
	for i := 0; i < 2; i++ {
		if _, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", wrong, "10.0.0.1"); err == nil {
			t.Fatalf("verification %d of a wrong code was accepted", i+1)
		}
	}

	_, err = svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", wrong, "10.0.0.1")
	if !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("the third verification from one source returned %v, want ErrRateLimited", err)
	}
}

// A refused attempt is a fact about the source, and the ledger is where an
// investigator looks for it. A rate limiter that refuses silently is a
// control whose refusals cannot be counted, which is how a broken limiter
// looks like a quiet afternoon.
func TestARateLimitedAttemptIsAudited(t *testing.T) {
	now := clock
	svc, tenant, site, raw := serviceWith(t, func() time.Time { return now }, 1)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
		t.Fatalf("the first attempt was refused: %v", err)
	}
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); !errors.Is(err, identity.ErrRateLimited) {
		t.Fatalf("the second attempt returned %v, want ErrRateLimited", err)
	}

	var reason string
	err := raw.QueryRow(ctx,
		`SELECT reason FROM audit_ledger
		  WHERE tenant_id = $1 AND action = 'auth.login.failed' AND request_id = '10.0.0.1'
		  ORDER BY sequence DESC LIMIT 1`, tenant).Scan(&reason)
	if err != nil {
		t.Fatalf("read the audit row: %v", err)
	}
	if reason == "" {
		t.Error("the refused attempt was audited with no reason; an investigator would see a " +
			"failure with nothing to explain it")
	}
}
