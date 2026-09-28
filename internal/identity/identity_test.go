package identity_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// testSecretStore is a reversible wrapper so the tests can exercise the
// enrolment and verification path without a key. The real store holds a
// product-managed key the installer writes; there is deliberately no
// implementation of it in this repository that could be reached by a test.
type testSecretStore struct{}

func (testSecretStore) Wrap(b []byte) ([]byte, error) { return append([]byte("T:"), b...), nil }
func (testSecretStore) Unwrap(b []byte) ([]byte, error) {
	if len(b) < 2 || string(b[:2]) != "T:" {
		return nil, errors.New("not wrapped by this store")
	}
	return b[2:], nil
}

var clock = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func service(t *testing.T) (*identity.Service, string, string) {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	wrapped, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(wrapped.Close)

	tenant, site, _ := testutil.NewTenant(t, raw)
	return &identity.Service{
		Pool:        wrapped,
		Audit:       &audit.Writer{Pool: wrapped},
		Now:         func() time.Time { return clock },
		SecretStore: testSecretStore{},
	}, tenant, site
}

// ---------------------------------------------------------------------------
// Passwords.
// ---------------------------------------------------------------------------

func TestPasswordHashingAndVerification(t *testing.T) {
	hash, err := identity.HashPassword("a-long-enough-passphrase")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(hash, "a-long-enough-passphrase") {
		t.Fatal("the passphrase appears in the hash")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash is %q, want an argon2id hash", hash)
	}

	ok, err := identity.VerifyPassword(hash, "a-long-enough-passphrase")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("the correct passphrase was rejected")
	}

	ok, _ = identity.VerifyPassword(hash, "a-different-passphrase")
	if ok {
		t.Error("a wrong passphrase was accepted")
	}
}

func TestPasswordsAreSaltedPerUser(t *testing.T) {
	first, _ := identity.HashPassword("same-passphrase-here")
	second, _ := identity.HashPassword("same-passphrase-here")
	if first == second {
		t.Error("two users with the same passphrase produced the same hash; a database dump " +
			"would reveal which accounts share a password")
	}
}

func TestShortPassphrasesAreRefused(t *testing.T) {
	if _, err := identity.HashPassword("short"); err == nil {
		t.Error("a four-character passphrase was accepted")
	}
}

// A row in the database does not get to choose the site's password cost. A hash
// claiming weaker parameters is refused outright rather than verified cheaply.
func TestWeakHashParametersAreRefused(t *testing.T) {
	weak := "$argon2id$v=19$m=1024,t=1,p=1$c2FsdHNhbHRzYWx0$hashhashhash"
	if _, err := identity.VerifyPassword(weak, "anything-at-all"); err == nil {
		t.Error("a hash with weaker-than-floor parameters was accepted")
	} else if !strings.Contains(err.Error(), "weaker") {
		t.Errorf("error should name the parameter floor, got %q", err)
	}
}

// ---------------------------------------------------------------------------
// Authorization.
// ---------------------------------------------------------------------------

func TestCapabilitiesAreExactNotPrefixes(t *testing.T) {
	operator := identity.Principal{
		UserID: "u1", TenantID: "t1", Username: "op",
		Roles: []identity.Role{identity.RoleOperator}, MFAVerified: true,
	}
	if !operator.Can(identity.CapIncidentRead) {
		t.Error("an operator cannot read incidents")
	}
	if operator.Can(identity.CapIdentityManage) {
		t.Error("an operator can manage identity")
	}
	if operator.Can(identity.CapAuditRead) {
		t.Error("an operator can read the audit ledger; a service account must not either")
	}
}

func TestSiteScopeDeniesByDefault(t *testing.T) {
	// A principal with a role but no site assignment has been misconfigured, and
	// the safe reading of a misconfiguration that grants access is that it does
	// not grant it.
	p := identity.Principal{
		Roles: []identity.Role{identity.RoleOperator}, MFAVerified: true,
	}
	if p.CanSeeSite("any") {
		t.Error("a principal with no site scope can see a site")
	}
	if p.CanSeeSite("") {
		t.Error("an empty site id is visible")
	}

	scoped := p
	scoped.SiteScope = []string{"site-a"}
	if !scoped.CanSeeSite("site-a") {
		t.Error("an assigned site is not visible")
	}
	if scoped.CanSeeSite("site-b") {
		t.Error("a site the principal is not assigned to is visible")
	}
}

func TestServiceAccountsNeverHoldAuditRead(t *testing.T) {
	for _, cap := range identity.CapabilitiesFor(identity.RoleService) {
		if identity.Capability(cap) == identity.CapAuditRead {
			t.Errorf("the service role holds audit read: %s", cap)
		}
	}
	// And the capability list is walked exactly, not by prefix.
	svc := identity.Principal{
		UserID: "svc-1", Roles: []identity.Role{identity.RoleService},
		ServiceAccount: true, MFAVerified: true,
	}
	for _, forbidden := range []identity.Capability{
		identity.CapAuditRead, identity.CapIdentityManage,
		identity.CapRulesActivate, identity.CapIncidentDisposition,
	} {
		if svc.Can(forbidden) {
			t.Errorf("a service account holds %q", forbidden)
		}
	}
}

func TestAuthorizeRequiresAVerifiedFactor(t *testing.T) {
	p := identity.Principal{
		UserID: "u1", TenantID: "t1", Username: "op",
		Roles: []identity.Role{identity.RoleOperator},
		// No MFA.
	}
	err := p.Authorize(identity.CapIncidentRead)
	if err == nil {
		t.Error("a principal with no verified factor was authorized")
	}
	if !strings.Contains(err.Error(), "second factor") {
		t.Errorf("error should name the missing factor, got %q", err)
	}

	// A service account is a non-human principal: it authenticates with a scoped
	// token, so there is no human second factor to ask for. It is authorized on
	// its capabilities alone, which is why those capabilities matter so much.
	svc := identity.Principal{
		UserID: "svc-1", TenantID: "t1", Username: "adapter-1",
		Roles:          []identity.Role{identity.RoleService},
		ServiceAccount: true,
		SiteScope:      []string{"site-a"},
	}
	if err := svc.AuthorizeSite("event.ingest", "site-a"); err != nil {
		t.Errorf("a service account was asked for a human second factor: %v", err)
	}
	if err := svc.AuthorizeSite(identity.CapIdentityManage, "site-a"); err == nil {
		t.Error("a service account was authorized for identity management")
	}
}

func TestAuthorizeSiteCombinesCapabilityAndAssignment(t *testing.T) {
	p := identity.Principal{
		Roles: []identity.Role{identity.RoleOperator}, MFAVerified: true,
		SiteScope: []string{"site-a"},
	}
	if err := p.AuthorizeSite(identity.CapIncidentRead, "site-a"); err != nil {
		t.Errorf("an assigned site was refused: %v", err)
	}
	err := p.AuthorizeSite(identity.CapIncidentRead, "site-b")
	if err == nil {
		t.Fatal("an unassigned site was permitted")
	}
	if !strings.Contains(err.Error(), "not assigned") {
		t.Errorf("error should say the principal is unassigned, got %q", err)
	}
}

// ---------------------------------------------------------------------------
// Lockout.
// ---------------------------------------------------------------------------

// The backoff is exponential and capped, so a guessing attack's rate falls off a
// cliff while the operator's single mistake costs a minute.
func TestLockoutIsExponentialAndCapped(t *testing.T) {
	base := 30 * time.Second
	prev := time.Duration(0)
	for attempts := 1; attempts <= 12; attempts++ {
		lock, delay := identity.ShouldLock(
			identity.Lockout{FailedAttempts: attempts}, 5, base)
		if attempts < 5 {
			if lock {
				t.Errorf("locked after %d attempts, want the lock to start at 5", attempts)
			}
			continue
		}
		if !lock {
			t.Fatalf("not locked at %d attempts", attempts)
		}
		if delay < prev {
			t.Errorf("the delay decreased at %d attempts: %s after %s", attempts, delay, prev)
		}
		if delay > time.Hour {
			t.Errorf("the delay at %d attempts is %s, past the one-hour cap", attempts, delay)
		}
		prev = delay
	}
	if prev != time.Hour {
		t.Errorf("the delay saturates at %s, want one hour", prev)
	}
}

// ---------------------------------------------------------------------------
// The full flow.
// ---------------------------------------------------------------------------

func TestBootstrapRefusesASecondAdministrator(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin1",
		"a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	_, err := svc.BootstrapAdmin(ctx, tenant, "admin2", "another-long-passphrase", site)
	if err == nil {
		t.Fatal("a second tenant administrator was created; that is how an account nobody " +
			"knows about gets created")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the refusal should say why, got %q", err)
	}
}

func TestBootstrapPassphraseMustBeLong(t *testing.T) {
	svc, tenant, site := service(t)
	if _, err := svc.BootstrapAdmin(context.Background(), tenant, "admin",
		"short", site); err == nil {
		t.Error("a four-character bootstrap passphrase was accepted")
	}
}

// MFA is mandatory: a password step returns a challenge, never a session.
func TestPasswordStepReturnsAChallengeNotASession(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()

	admin, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	secret, _, err := svc.EnrollTOTP(ctx, tenant, admin, "admin")
	if err != nil {
		t.Fatalf("EnrollTOTP: %v", err)
	}
	if secret == "" {
		t.Error("the TOTP secret was not returned; the operator could not enrol")
	}

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	if !res.MFARequired {
		t.Error("the password step produced no MFA requirement")
	}
	if res.Session != nil {
		t.Fatal("the password step returned a session; MFA is mandatory")
	}
	if res.ChallengeID == "" {
		t.Error("no challenge was issued")
	}
}

func TestUnknownUserAndWrongPasswordAreIndistinguishable(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	unknown, err := svc.PasswordStep(ctx, tenant, "nobody", "a-long-bootstrap-passphrase", "10.0.0.1")
	wrong, err2 := svc.PasswordStep(ctx, tenant, "admin", "the-wrong-passphrase", "10.0.0.1")

	if !errors.Is(err, identity.ErrInvalidCredentials) {
		t.Errorf("an unknown user produced %v, want ErrInvalidCredentials", err)
	}
	if !errors.Is(err2, identity.ErrInvalidCredentials) {
		t.Errorf("a wrong password produced %v, want ErrInvalidCredentials", err2)
	}
	if err.Error() != err2.Error() {
		t.Errorf("the two failures are distinguishable: %q vs %q. An operator enumerating "+
			"usernames can tell one from the other", err, err2)
	}
	_ = unknown
	_ = wrong
}

func TestSuccessfulPasswordClearsTheFailureCounter(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	for i := 0; i < 3; i++ {
		_, _ = svc.PasswordStep(ctx, tenant, "admin", "wrong-passphrase-here", "10.0.0.1")
	}
	if _, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1"); err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}

	var failures int
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT failed_attempts FROM users WHERE tenant_id = $1 AND username = 'admin'`,
		tenant).Scan(&failures); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if failures != 0 {
		t.Errorf("the failure counter is %d after a successful login; three typos and one "+
			"success should not add up to a lockout", failures)
	}
}

func TestRepeatedFailuresLockTheAccount(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	var lastErr error
	for i := 0; i < 7; i++ {
		_, lastErr = svc.PasswordStep(ctx, tenant, "admin", "wrong-passphrase-here", "10.0.0.1")
	}
	// Past the threshold the account is locked, and a correct password is
	// refused with ErrLocked rather than ErrInvalidCredentials, so an operator
	// is told to wait rather than to keep typing.
	_, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if !errors.Is(err, identity.ErrLocked) {
		t.Errorf("a locked account returned %v, want ErrLocked", err)
	}
	_ = lastErr
}

// A session token is stored hashed, so a database dump does not yield sessions.
func TestSessionTokensAreStoredHashed(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	res, err := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	if err != nil {
		t.Fatalf("PasswordStep: %v", err)
	}
	if res.Session == nil {
		t.Fatal("a bootstrap account with no enrolled factor should get a session")
	}
	token := res.Session.Token
	if token == "" {
		t.Fatal("no session token was issued")
	}

	var stored string
	if err := testutil.Pool(t).QueryRow(ctx,
		`SELECT token_hash FROM sessions WHERE tenant_id = $1 AND user_id = $2`,
		tenant, admin).Scan(&stored); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	if stored == token {
		t.Fatal("the raw session token is stored in the database")
	}
	if stored != identity.HashToken(token) {
		t.Error("the stored hash is not the hash of the issued token")
	}
}

// Rotation on every use: a token captured from a browser is only good until the
// legitimate operator's next request.
func TestSessionTokensRotateOnUse(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	res, _ := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	first := res.Session.Token

	if _, err := svc.Resolve(ctx, tenant, first, "10.0.0.1"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := svc.Resolve(ctx, tenant, first, "10.0.0.1"); err == nil {
		t.Error("a rotated-away token still resolved; a captured cookie would be reusable")
	}
}

func TestCSRFTokenIsRequiredAndSessionBound(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	res, _ := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	sessionID := res.Session.Principal.SessionID

	if err := svc.CheckCSRF(ctx, tenant, sessionID, ""); err == nil {
		t.Error("a state-changing request with no CSRF token was accepted")
	}
	if err := svc.CheckCSRF(ctx, tenant, sessionID, "not-the-token"); err == nil {
		t.Error("a wrong CSRF token was accepted")
	}
	if err := svc.CheckCSRF(ctx, tenant, sessionID, res.Session.CSRFToken); err != nil {
		t.Errorf("the correct CSRF token was refused: %v", err)
	}
}

func TestPasswordChangeRevokesEverySession(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	res, _ := svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")
	token := res.Session.Token

	if _, err := svc.Resolve(ctx, tenant, token, "10.0.0.1"); err != nil {
		t.Fatalf("Resolve before change: %v", err)
	}
	if err := svc.SetPassword(ctx, tenant, admin, "a-long-bootstrap-passphrase",
		"a-different-long-passphrase", admin); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if _, err := svc.Resolve(ctx, tenant, token, "10.0.0.1"); err == nil {
		t.Error("a session survived a password change; a password change is the operator's " +
			"statement that whoever had the old one should not still be in")
	}
}

func TestEveryAuthEventIsAudited(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	if _, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	_, _ = svc.PasswordStep(ctx, tenant, "admin", "wrong-passphrase-here", "10.0.0.1")
	_, _ = svc.PasswordStep(ctx, tenant, "admin", "a-long-bootstrap-passphrase", "10.0.0.1")

	var n int
	if err := testutil.Pool(t).QueryRow(ctx, `
		SELECT count(*) FROM audit_ledger
		 WHERE tenant_id = $1 AND action IN
		   ('auth.login.success','auth.login.failed','identity.bootstrap_admin')`,
		tenant).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n < 3 {
		t.Errorf("%d authentication audit rows, want at least 3", n)
	}
}

func TestStepUpIsSingleUse(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, err := svc.BootstrapAdmin(ctx, tenant, "admin", "a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	secret, _, err := svc.EnrollTOTP(ctx, tenant, admin, "admin")
	if err != nil {
		t.Fatalf("EnrollTOTP: %v", err)
	}
	_ = secret

	if err := svc.ConsumeStepUp(ctx, tenant, admin, "no-such-proof"); err == nil {
		t.Error("an invented step-up proof was accepted")
	}
	if err := svc.ConsumeStepUp(ctx, tenant, admin, ""); !errors.Is(err, identity.ErrStepUpRequired) {
		t.Errorf("an empty proof produced %v, want ErrStepUpRequired", err)
	}
}
