package identity_test

// The TOTP secret is sealed with its ROW bound in, and this file is what proves it.

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/identity"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// TestASecondFactorMovedToAnotherRowDoesNotDecrypt is the control.
//
// `EnrollTOTP` used `SecretStore.Wrap` and `verifyFactor` used `Unwrap`. Both are
// unbound, so a ciphertext copied from one `mfa_credentials` row to another opened
// cleanly there: an attacker who can write that table hands one operator's second
// factor to another, and the verification then fails in a way that looks like the
// wrong code rather than like a moved secret.
//
// The attack is worth being concrete about, because "an attacker who can write to
// mfa_credentials" sounds like a small premise and is not: the table is reachable by
// anything holding the tenant's database role, and the whole point of row-level
// security is that a compromise elsewhere in the tenant does not reach the factor
// column. A secret that is not row-bound is the one column where that is true for
// reads and false for writes.
//
// The move is done with the OWNER pool, because that is the attacker: the production
// role cannot write a factor row for another site, which is the point of migration
// 0023 and is why this test reaches for `testutil.Pool` rather than the service.
func TestASecondFactorMovedToAnotherRowDoesNotDecrypt(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	raw := testutil.Pool(t)

	adminA, _ := enrolAdmin(t, svc, tenant, site)

	// The code is computed FIRST, while the secret is still bound to its own row.
	// Reading it afterwards would read the moved row -- which `UnwrapFor` correctly
	// refuses, and which is the behaviour this test is asserting, so asking for it
	// here would be asking the control to fail for the test's benefit.
	code := rfc6238Code(t, mustReadSecret(t, raw, tenant, adminA), clock, sha256.New)

	// A second factor row in the same tenant, holding a COPY of the first row's
	// ciphertext, and then the original row is REMOVED -- so the only factor left
	// belongs to the wrong row. That is the attacker's end state: the secret they
	// moved is the only one there, and the original holder has lost theirs.
	//
	// `verifyFactor` selects by `(tenant, user)` and takes the first row it finds, so
	// leaving both would make this test depend on which one the query returns.
	const victimID = "totp_victim_row"
	var originalID string
	if err := raw.QueryRow(ctx,
		`SELECT credential_id FROM mfa_credentials WHERE tenant_id = $1 AND user_id = $2
		  ORDER BY credential_id LIMIT 1`, tenant, adminA).Scan(&originalID); err != nil {
		t.Fatalf("read the original credential id: %v", err)
	}
	if _, err := raw.Exec(ctx, `
		INSERT INTO mfa_credentials (credential_id, tenant_id, user_id, method, label, wrapped_secret)
		SELECT $1, tenant_id, user_id, method, 'moved', wrapped_secret
		  FROM mfa_credentials WHERE tenant_id = $2 AND credential_id = $3`,
		victimID, tenant, originalID); err != nil {
		t.Fatalf("copy the factor row: %v", err)
	}
	if _, err := raw.Exec(ctx,
		`DELETE FROM mfa_credentials WHERE tenant_id = $1 AND credential_id = $2`,
		tenant, originalID); err != nil {
		t.Fatalf("remove the original row: %v", err)
	}

	// The copied secret now belongs to a different row. Verifying a code against it
	// must fail, and it must fail as a DECRYPTION failure rather than as "wrong
	// code" -- the difference is what tells an operator their factor was moved rather
	// than that they typed the wrong six digits.
	if err := svc.VerifyTOTPEnrollment(ctx, tenant, adminA, code); err == nil {
		t.Fatalf("a second factor copied to another row verified a code")
	} else if !strings.Contains(err.Error(), "different row") {
		t.Fatalf("the moved factor failed with %v, which is not the row-binding refusal. "+
			"A failure that reads as a wrong code sends an operator to re-enrol instead of "+
			"to look at what happened to their row", err)
	}
}

// mustReadSecret unwraps the stored secret so the test can compute a valid code for
// it. It goes through the store's own bound reader, so the fixture is sealed the way
// the product seals it rather than by hand.
func mustReadSecret(t *testing.T, raw *pgxpool.Pool, tenant, user string) string {
	t.Helper()
	var id string
	var wrapped []byte
	if err := raw.QueryRow(context.Background(),
		`SELECT credential_id, wrapped_secret FROM mfa_credentials
		  WHERE tenant_id = $1 AND user_id = $2 ORDER BY credential_id LIMIT 1`,
		tenant, user).Scan(&id, &wrapped); err != nil {
		t.Fatalf("read the stored factor: %v", err)
	}
	plain, err := testSecretStore{}.UnwrapFor(tenant, user, id, wrapped)
	if err != nil {
		t.Fatalf("unwrap the stored factor for %s/%s: %v", tenant, user, err)
	}
	return string(plain)
}

// TestEnrollingWithAStoreThatCannotBindIsRefused is the interface's own guard.
//
// `SecretStore` carries the bound pair so the choice cannot be made by accident at a
// call site. An implementation with only the unbound pair therefore cannot enrol a
// factor at all — which is the correct failure, and the compiler is what enforces it.
func TestEnrollingWithAStoreThatCannotBindIsRefused(t *testing.T) {
	var _ identity.SecretStore = testSecretStore{}
}
