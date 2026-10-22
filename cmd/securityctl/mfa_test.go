package main

import (
	"bytes"
	"context"
	"encoding/base32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/command"
	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

// The MFA enrolment, on the terminal.
//
// It is a command and not a route, and this test is the evidence that the
// command works: a secret comes back exactly once, the factor it created can
// be confirmed with a code from an authenticator, and the secret cannot be
// read back out of the database afterwards.

// mfaTestSite is a tenant with one principal and a wrapping key, and the
// environment the CLI reads.
func mfaTestSite(t *testing.T) (tenant, keyPath string) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: pool}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, _, _ = testutil.NewTenant(t, pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (tenant_id, user_id, username, display_name, enabled, recovery_required)
		VALUES ($1, 'usr-operator', 'operator', 'Test operator', TRUE, FALSE)`, tenant); err != nil {
		t.Fatalf("insert the operator: %v", err)
	}

	keyPath = filepath.Join(t.TempDir(), "secret.key")
	if err := identity.GenerateSecretKey(keyPath); err != nil {
		t.Fatalf("generate the wrapping key: %v", err)
	}

	// The CLI reads the database and the tenant from the environment, the way
	// an install does.
	t.Setenv("SP_DATABASE_URL", os.Getenv(testutil.DatabaseURLEnv))
	t.Setenv("SP_DATABASE_PASSWORD", os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	t.Setenv("SP_TENANT_ID", tenant)
	return tenant, keyPath
}

// runMFA runs the mfa command tree and returns what it printed.
func runMFA(t *testing.T, args ...string) (stdout, stderr string, code command.ExitCode) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := &command.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}
	code = mfaCommand().Execute(context.Background(), env, args)
	return out.String(), errOut.String(), code
}

// The enrolment prints the secret and the URI, and says the secret is not
// recoverable.
//
// The URI is what an authenticator imports, and printing it is the whole
// reason the command exists: an operator who has to type a base32 secret by
// hand into a phone is an operator who will get it wrong.
func TestMFAEnrolPrintsTheSecretAndTheURIOnce(t *testing.T) {
	_, keyPath := mfaTestSite(t)

	stdout, stderr, code := runMFA(t, "enrol", "--user", "operator", "--secret-key", keyPath)
	if code != command.ExitOK {
		t.Fatalf("mfa enrol exited %d: %s", code, stderr)
	}

	if !strings.Contains(stdout, "otpauth://totp/") {
		t.Errorf("the enrolment printed no otpauth URI:\n%s", stdout)
	}
	if !strings.Contains(stdout, "secret:") {
		t.Errorf("the enrolment printed no secret:\n%s", stdout)
	}
	if !strings.Contains(stdout, "not recoverable") {
		t.Errorf("the enrolment did not say the secret is unrecoverable:\n%s", stdout)
	}
	// The command that confirms the enrolment has to be named, or the operator
	// is left with a factor nobody has checked.
	if !strings.Contains(stdout, "mfa confirm") {
		t.Errorf("the enrolment did not say how to confirm it:\n%s", stdout)
	}
}

// The secret is not recoverable afterwards.
//
// The second enrolment issues a different secret, and the first one is not
// printed again by anything: the database holds a wrapped copy that only the
// install's key opens. A copy of the database alone is not a working factor.
func TestTheEnrolledSecretIsNotRecoverable(t *testing.T) {
	tenant, keyPath := mfaTestSite(t)

	first, _, code := runMFA(t, "enrol", "--user", "operator", "--secret-key", keyPath)
	if code != command.ExitOK {
		t.Fatalf("the first enrolment did not succeed")
	}
	second, _, code := runMFA(t, "enrol", "--user", "operator", "--secret-key", keyPath)
	if code != command.ExitOK {
		t.Fatalf("the second enrolment did not succeed")
	}

	secretOf := func(out string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "secret:") {
				return strings.TrimSpace(strings.SplitN(line, "secret:", 2)[1])
			}
		}
		return ""
	}
	if secretOf(first) == "" || secretOf(second) == "" {
		t.Fatalf("the enrolments printed no secret to compare:\n%s\n%s", first, second)
	}
	if secretOf(first) == secretOf(second) {
		t.Error("two enrolments issued the same secret; the secret is not random")
	}

	// And the stored credential is the wrapped form, not the secret.
	pool := testutil.Pool(t)
	var wrapped []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT wrapped_secret FROM mfa_credentials WHERE tenant_id = $1`, tenant).Scan(&wrapped); err != nil {
		t.Fatalf("read the credential: %v", err)
	}
	if strings.Contains(string(wrapped), secretOf(first)) {
		t.Error("the stored credential contains the secret in the clear")
	}
}

// The enrolment is bound to the principal that was named, and to that
// principal's tenant.
func TestMFAEnrolBindsTheFactorToThePrincipal(t *testing.T) {
	tenant, keyPath := mfaTestSite(t)

	if _, stderr, code := runMFA(t, "enrol", "--user", "operator", "--secret-key", keyPath); code != command.ExitOK {
		t.Fatalf("mfa enrol exited %d: %s", code, stderr)
	}

	pool := testutil.Pool(t)
	var userID, method string
	var verified pgtype.Timestamptz
	if err := pool.QueryRow(context.Background(),
		`SELECT user_id, method, verified_at FROM mfa_credentials WHERE tenant_id = $1`, tenant).
		Scan(&userID, &method, &verified); err != nil {
		t.Fatalf("read the credential: %v", err)
	}
	if userID != "usr-operator" {
		t.Errorf("the factor is bound to %q, want usr-operator", userID)
	}
	if method != "totp" {
		t.Errorf("the factor is a %q, want totp", method)
	}
}

// A principal that does not exist is refused, and the refusal names what was
// asked for.
//
// An enrolment against a typo would otherwise create a factor for a user
// nobody has, which is an account that cannot sign in and a credential row
// nobody will ever read.
func TestMFAEnrolRefusesAPrincipalThatDoesNotExist(t *testing.T) {
	_, keyPath := mfaTestSite(t)

	_, stderr, code := runMFA(t, "enrol", "--user", "nobody", "--secret-key", keyPath)
	if code == command.ExitOK {
		t.Fatal("the enrolment succeeded against a principal that does not exist")
	}
	if !strings.Contains(stderr, "nobody") {
		t.Errorf("the refusal does not name the principal:\n%s", stderr)
	}
}

// The confirmation is what proves the authenticator holds the secret.
//
// Without it the credential is stored and unverified: the phone may have
// imported the wrong secret and nothing has said so. A wrong code is refused,
// so the command cannot mark a factor as confirmed that nobody proved.
func TestMFAConfirmMarksTheFactorVerifiedAndRefusesAWrongCode(t *testing.T) {
	tenant, keyPath := mfaTestSite(t)

	stdout, _, code := runMFA(t, "enrol", "--user", "operator", "--secret-key", keyPath)
	if code != command.ExitOK {
		t.Fatal("the enrolment did not succeed")
	}
	secret := ""
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "secret:") {
			secret = strings.TrimSpace(strings.SplitN(line, "secret:", 2)[1])
		}
	}
	if secret == "" {
		t.Fatalf("the enrolment printed no secret:\n%s", stdout)
	}

	// A code from a different secret, so the refusal is about the code rather
	// than about the command line.
	other := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("a-different-secret-entirely"))
	wrong, err := totpCode(other, time.Now())
	if err != nil {
		t.Fatalf("compute the wrong code: %v", err)
	}
	if _, stderr, code := runMFA(t, "confirm", "--user", "operator", "--code", wrong,
		"--secret-key", keyPath); code == command.ExitOK {
		t.Error("the confirmation accepted a code from a different secret")
	} else if !strings.Contains(strings.ToLower(stderr), "refus") &&
		!strings.Contains(strings.ToLower(stderr), "invalid") &&
		!strings.Contains(strings.ToLower(stderr), "identity") {
		t.Errorf("the refusal said nothing about the code:\n%s", stderr)
	}

	pool := testutil.Pool(t)
	var verified pgtype.Timestamptz
	if err := pool.QueryRow(context.Background(),
		`SELECT verified_at FROM mfa_credentials WHERE tenant_id = $1`, tenant).Scan(&verified); err != nil {
		t.Fatalf("read the credential: %v", err)
	}
	if verified.Valid {
		t.Error("the factor is verified after a confirmation that was refused")
	}

	right, err := totpCode(secret, time.Now())
	if err != nil {
		t.Fatalf("compute the right code: %v", err)
	}
	if _, stderr, code := runMFA(t, "confirm", "--user", "operator", "--code", right,
		"--secret-key", keyPath); code != command.ExitOK {
		t.Fatalf("the confirmation with the right code was refused: %s", stderr)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT verified_at FROM mfa_credentials WHERE tenant_id = $1`, tenant).Scan(&verified); err != nil {
		t.Fatalf("read the credential: %v", err)
	}
	if !verified.Valid {
		t.Error("the factor is still unverified after a confirmation with the right code")
	}
}

// totpSecret is gone: the package already has the RFC 6238 construction in
// auth_code.go, and a second copy of it is a second thing to get wrong.
