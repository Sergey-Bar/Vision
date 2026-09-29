package identity_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// enrolAdmin bootstraps an administrator with a verified TOTP factor and returns
// the user id and the shared secret.
//
// The enrolment itself consumes a TOTP step, so a test that goes on to verify a
// login has to use a *later* step. The verifier keeps a cache of consumed steps
// (RFC 6238 says a code must not be accepted twice), so reusing the frozen
// clock's code here would be refused — correctly, and for a reason that has
// nothing to do with what those tests are about. `enrolAdmin` therefore returns
// the secret and the test asks for the step it wants.
func enrolAdmin(t *testing.T, svc *identity.Service, tenant, site string) (string, string) {
	t.Helper()
	ctx := context.Background()
	admin, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	secret, _, err := svc.EnrollTOTP(ctx, tenant, admin, "admin")
	if err != nil {
		t.Fatalf("EnrollTOTP: %v", err)
	}
	if err := svc.VerifyTOTPEnrollment(ctx, tenant, admin,
		rfc6238Code(t, secret, clock, sha256.New)); err != nil {
		t.Fatalf("confirm the enrolment: %v", err)
	}
	return admin, secret
}

// nextStep is the code for the step after enrolment. Each call returns a
// different, later step, so a test that verifies more than once does not
// accidentally assert against the replay cache instead of what it means to
// test.
func nextStep(secret string, n int) string {
	return rfc6238Code(&testing.T{}, secret, clock.Add(time.Duration(n)*30*time.Second), sha256.New)
}

// A challenge is a two-minute bearer of "the password was right". Consuming it
// has to be the transaction that issues the session, not something the purge
// sweep does an hour later — one intercepted challenge id used to mint a session
// per request for its whole TTL.
func TestAMFAChallengeIsSingleUse(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	_, secret := enrolAdmin(t, svc, tenant, site)

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	code := nextStep(secret, 1)
	if _, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", code, "10.0.0.1"); err != nil {
		t.Fatalf("the first completion failed: %v", err)
	}
	if _, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", code, "10.0.0.1"); err == nil {
		t.Fatal("the same challenge id completed a second time. A challenge that can be used " +
			"twice is a password equivalent")
	}

	// And the row is gone, rather than merely rejected because the code was stale.
	var rows int
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE tenant_id = $1 AND session_id = $2`,
		tenant, "chal_"+res.ChallengeID).Scan(&rows); err != nil {
		t.Fatalf("count the challenge row: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d challenge row(s) survived a successful login. The row is deleted in the same "+
			"transaction as the session insert, so an unconsumed challenge cannot be replayed "+
			"even inside its own TTL", rows)
	}
}

// A six-digit code inside a plus-or-minus-thirty-second window is guessable in a
// couple of minutes of wall clock, and the challenge lives for two of them. The
// password step has a lockout; this one had nothing, and the route it sits behind
// is CSRF-exempt.
func TestMFAVerifyIsBoundedByAnAttemptCounter(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	svc.MaxFailedAttempts = 3
	_, secret := enrolAdmin(t, svc, tenant, site)

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	for i := 0; i < svc.MaxFailedAttempts; i++ {
		if _, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", "000000", "10.0.0.1"); err == nil {
			t.Fatalf("guess %d was accepted", i)
		}
	}

	// The correct code is now refused, because the challenge has spent its budget.
	_, err = svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp",
		nextStep(secret, 1), "10.0.0.1")
	if !errors.Is(err, identity.ErrLocked) {
		t.Errorf("after %d wrong codes the correct one produced %v, want ErrLocked. Without a "+
			"counter the second factor is brute-forceable unbounded inside its own TTL",
			svc.MaxFailedAttempts, err)
	}
}

// The counter is per challenge, not per account. The password already succeeded
// on this path, so folding the second factor into the account counter would lock
// an operator out for mistyping six digits, and the account counter is cleared by
// the next successful password anyway.
func TestTheChallengeCounterDoesNotLockTheAccount(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	svc.MaxFailedAttempts = 3
	_, secret := enrolAdmin(t, svc, tenant, site)

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	for i := 0; i < svc.MaxFailedAttempts-1; i++ {
		_, _ = svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp", "000000", "10.0.0.1")
	}
	if _, err := svc.CompleteChallenge(ctx, tenant, res.ChallengeID, "totp",
		nextStep(secret, 1), "10.0.0.1"); err != nil {
		t.Errorf("a legitimate operator who mistyped %d times was refused: %v",
			svc.MaxFailedAttempts-1, err)
	}
}

// The login response has to name who is being asked for a second factor. It used
// to read the principal off the session, which is nil by construction whenever
// MFA is required — so every MFA login for every enrolled operator dereferenced
// nil and the primary login path was dead.
func TestMFARequiredLoginNamesThePrincipal(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, _ := enrolAdmin(t, svc, tenant, site)

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	if res.Session != nil {
		t.Fatal("the password step returned a session; MFA is mandatory")
	}
	if res.Principal.UserID != admin {
		t.Errorf("the MFA-required result names principal %q, want %q. A caller that reads "+
			"the principal off the (nil) session dereferences nil and every enrolled operator "+
			"gets a 500", res.Principal.UserID, admin)
	}
	if res.Principal.TenantID != tenant || res.Principal.Username == "" {
		t.Errorf("the principal is incomplete: %+v", res.Principal)
	}
	if len(res.Principal.Roles) == 0 {
		t.Error("the principal carries no roles; the response has to say what is being asked of " +
			"them, and a role-less principal renders as an operator with no capabilities")
	}
	if res.Principal.MFAVerified {
		t.Error("the principal is marked MFA-verified before the second factor was presented")
	}
}

// The bootstrap administrator's id used to be the SHA-256 of nothing, appended to
// the tenant and username: `sha256.New().Sum(x)` returns x plus the digest of
// zero bytes, so every administrator in every deployment was "admin-e3b0c442".
// The current password is required whatever the account's recovery flag says.
//
// `EnsureUser` inserts every new user with `recovery_required = TRUE`, so the old
// bypass was not an edge case — it was the normal state of every account an
// administrator ever provisioned. Anybody holding `identity.manage` could
// re-key one, including one that was already compromised.
func TestSetPasswordRequiresTheCurrentPasswordInBothModes(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	admin, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	// A provisioned user: recovery_required is TRUE from the moment it exists.
	if err := svc.EnsureUser(ctx, tenant, "u-recovery", "recovering", "R. Cover",
		"tenant_admin", []string{site}, "admin"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	// Give it a real password, which clears the flag.
	// No password exists yet, so there is nothing to verify: this is the one legitimate
	// recovery path, and it is reachable only because the account has no hash at all.
	if err := svc.SetPassword(ctx, tenant, "u-recovery", "", "a-real-passphrase", "admin"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	var flag bool
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT recovery_required FROM users WHERE tenant_id = $1 AND user_id = 'u-recovery'`,
		tenant).Scan(&flag); err != nil {
		t.Fatalf("read the flag: %v", err)
	}
	if flag {
		t.Fatal("setting a password did not clear recovery_required; the test would not " +
			"exercise the flag being true")
	}

	// The flag is now false, and a wrong current password is refused.
	if err := svc.SetPassword(ctx, tenant, "u-recovery", "not-the-password",
		"another-passphrase", "admin"); err == nil {
		t.Error("a password change with a wrong current password succeeded")
	}

	// The flag is back to false after the successful change; the real question is
	// what a *provisioned* account does, so set it true directly and go again.
	testutil.Exec(t, testutil.Pool(t),
		`UPDATE users SET recovery_required = TRUE WHERE tenant_id = $1 AND user_id = 'u-recovery'`,
		tenant)
	if err := svc.SetPassword(ctx, tenant, "u-recovery", "not-the-password",
		"yet-another-passphrase", "admin"); err == nil {
		t.Error("a password change with a wrong current password succeeded while " +
			"recovery_required was true. The recovery flag is meant to say the account has " +
			"never had a password chosen, not that the current one need not be known")
	}
	var stored string
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT COALESCE(password_hash, '') FROM users WHERE tenant_id = $1 AND user_id = 'u-recovery'`,
		tenant).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if ok, err := identity.VerifyPassword(stored, "a-real-passphrase"); err != nil || !ok {
		t.Errorf("the two refused attempts changed the stored password. A refusal has to mean "+
			"nothing happened, not that the wrong value was written anyway: verify=%v err=%v",
			ok, err)
	}

	// And the right one still works.
	if err := svc.SetPassword(ctx, tenant, "u-recovery", "a-real-passphrase",
		"the-third-passphrase", "admin"); err != nil {
		t.Errorf("a correct current password was refused: %v", err)
	}

	// The administrator's own account is untouched by the above.
	if err := svc.SetPassword(ctx, tenant, admin, "a-long-bootstrap-passphrase",
		"a-new-admin-passphrase", admin); err != nil {
		t.Errorf("an administrator could not change their own password: %v", err)
	}
}

func TestBootstrapAdminIdsAreDistinctPerTenant(t *testing.T) {
	svc, _, _ := service(t)
	ctx := context.Background()
	raw := testutil.Pool(t)

	seen := map[string]string{}
	for i := 0; i < 2; i++ {
		tenant, site, _ := testutil.NewTenant(t, raw)
		id, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
		if err != nil {
			t.Fatalf("BootstrapAdmin for tenant %d: %v", i, err)
		}
		if prev, dup := seen[id]; dup {
			t.Fatalf("tenants %s and %s both produced the administrator id %q. The id is "+
				"derived from the tenant and the username, and it was the digest of nothing",
				prev, tenant, id)
		}
		seen[id] = tenant
		if strings.Contains(id, "e3b0c442") {
			t.Errorf("the administrator id %q contains e3b0c442, which is the first eight hex "+
				"characters of the SHA-256 of no input at all", id)
		}
	}
}

// The separator and the digest are asserted against the derivation itself, in
// adminid_internal_test.go, rather than here. Driving them through BootstrapAdmin
// needed two fixed tenant ids in the shared database, which made the test
// impossible to run twice: the bootstrap refuses to create a second
// administrator, so the second run failed before it could assert anything.
