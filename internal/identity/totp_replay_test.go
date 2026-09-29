package identity_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// A TOTP code is valid for its 30-second step and this platform accepts one step
// either side, so it stays acceptable for roughly ninety seconds. Nothing used to
// record that a code had been spent, which meant a code shoulder-surfed off a
// screen, photographed, or replayed out of a captured request could be presented
// a second time inside the window and would be accepted. RFC 6238 -5.2 forbids
// exactly that.
func TestTheSameTOTPCodeIsAcceptedOnce(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, secret := enrolAdmin(t, svc, tenant, site)

	code := rfc6238Code(t, secret, clock, sha256.New)
	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", code); err != nil || !ok {
		t.Fatalf("the first presentation was refused (ok=%v err=%v)", ok, err)
	}

	_, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", code)
	if err != nil {
		t.Fatalf("the second presentation returned an error rather than a refusal: %v", err)
	}
	if ok {
		t.Fatal("the same code verified twice. A code is good for roughly ninety seconds, " +
			"and without a record of what has been spent that whole window is a second " +
			"authentication for whoever captured it")
	}
}

// The refusal has to be indistinguishable from a wrong code. A caller that could
// tell "you have already used this" from "that is not the code" can tell an
// attacker whether a guess landed, which turns a six-digit search into a
// confirmation oracle.
func TestAReplayedCodeIsRefusedExactlyLikeAWrongOne(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, secret := enrolAdmin(t, svc, tenant, site)

	code := rfc6238Code(t, secret, clock, sha256.New)
	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", code); err != nil || !ok {
		t.Fatalf("the first presentation was refused (ok=%v err=%v)", ok, err)
	}

	_, methodReplay, replayOK, replayErr := svc.VerifyFactor(ctx, tenant, admin, "totp", code)
	_, methodWrong, wrongOK, wrongErr := svc.VerifyFactor(ctx, tenant, admin, "totp", "000000")

	// errors.Is, not ==: the two answers must be indistinguishable to a caller,
	// and comparing errors by identity is a different statement from comparing the
	// value a caller would see. One of the two returned errors is very likely nil,
	// and a bare != between a nil and a non-nil wrapped error is not the
	// comparison being described.
	sameError := (replayErr == nil && wrongErr == nil) ||
		(replayErr != nil && wrongErr != nil && replayErr.Error() == wrongErr.Error())
	if replayOK != wrongOK || !sameError || methodReplay != methodWrong {
		t.Errorf("a replay is distinguishable from a wrong code: "+
			"replay=(%q,%v,%v) wrong=(%q,%v,%v). The difference tells an attacker that a "+
			"guess was correct",
			methodReplay, replayOK, replayErr, methodReplay, wrongOK, wrongErr)
	}
}

// The non-obvious decision. An operator's device clock runs a little behind the
// server's, so the code in their hand is routinely from the previous step, and
// RFC 6238 asks no monotonicity of the verifier. Refusing every step at or below
// the highest one already spent would look stricter and would refuse a correct
// operator - to defend against an attacker who already holds a code they can
// present once. An unspent older code is not a replay.
func TestAnOlderStepIsStillAcceptedAfterANewerOneWasUsed(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, secret := enrolAdmin(t, svc, tenant, site)

	// Now sits on the newer step; the code below is the previous step's.
	now := clock
	svc.Now = func() time.Time { return now }

	older := rfc6238Code(t, secret, clock.Add(-30*time.Second), sha256.New)
	newer := rfc6238Code(t, secret, clock, sha256.New)

	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", newer); err != nil || !ok {
		t.Fatalf("the newer step was refused: ok=%v err=%v", ok, err)
	}

	// The same request, a step behind: still inside the -one-step window, so it
	// verifies cryptographically, and it has never been spent.
	now = clock.Add(-30 * time.Second)
	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", older); err != nil || !ok {
		t.Errorf("a step that has never been used was refused after a newer one was "+
			"spent (ok=%v err=%v). The operator's device clock is behind the server's and "+
			"RFC 6238 requires no monotonicity; demanding it refuses a correct operator",
			ok, err)
	}

	// And the older one, once spent, is refused like any other.
	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", older); err != nil || ok {
		t.Errorf("a spent older step verified again (ok=%v err=%v)", ok, err)
	}
}

// The cache keys on the code, not on the step, and this is why. VerifyFactor
// accepts both SHA-256 and SHA-1 for one factor, because an authenticator that
// ignores the `algorithm` parameter in the enrolment URI falls back to SHA-1.
// Those produce genuinely different six digits for the same instant, so a
// one-use-per-step rule would break MFA for whichever client the operator happens
// to have - the fallback would work right up until the step was spent and then
// stop. RFC 6238 §5.2 says "the same code", and this is that.
func TestTheOtherAlgorithmsCodeForTheSameStepIsNotTreatedAsAReplay(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, secret := enrolAdmin(t, svc, tenant, site)

	sha256Code := rfc6238Code(t, secret, clock, sha256.New)
	sha1Code := rfc6238Code(t, secret, clock, sha1.New)

	if sha256Code == sha1Code {
		t.Skip("this secret produced the same digits under both algorithms at this instant, " +
			"so the two paths cannot be told apart here")
	}

	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", sha256Code); err != nil || !ok {
		t.Fatalf("the SHA-256 code was refused (ok=%v err=%v)", ok, err)
	}
	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", sha1Code); err != nil || !ok {
		t.Errorf("the SHA-1 code for the already-spent step was refused (ok=%v err=%v). It is "+
			"a different set of digits; refusing it would break MFA for every authenticator "+
			"that falls back to SHA-1", ok, err)
	}
	// Both are now spent, and each is still refused on a second presentation.
	if _, _, ok, _ := svc.VerifyFactor(ctx, tenant, admin, "totp", sha256Code); ok {
		t.Error("the SHA-256 code verified a second time")
	}
	if _, _, ok, _ := svc.VerifyFactor(ctx, tenant, admin, "totp", sha1Code); ok {
		t.Error("the SHA-1 code verified a second time")
	}
}

// The cache is per tenant. Two tenants can hold the same six digits - an
// attacker who watched one operator types a code for another tenant's account —
// and neither one's use may refuse the other's.
func TestACodeUsedInOneTenantIsNotRefusedInAnother(t *testing.T) {
	svc, tenantA, siteA := service(t)
	ctx := context.Background()

	raw := testutil.Pool(t)
	tenantB, _, _ := testutil.NewTenant(t, raw)

	adminA, secret := enrolAdmin(t, svc, tenantA, siteA)
	adminB, err := svc.BootstrapAdmin(ctx, tenantB, "admin", "a-long-bootstrap-passphrase", "")
	if err != nil {
		t.Fatalf("BootstrapAdmin in the second tenant: %v", err)
	}
	if _, _, err := svc.EnrollTOTP(ctx, tenantB, adminB, "admin"); err != nil {
		t.Fatalf("EnrollTOTP in the second tenant: %v", err)
	}
	// Same shared secret in both tenants, so both factors produce the same code
	// for the same instant. Only the tenant differs.
	testutil.Exec(t, raw,
		`UPDATE mfa_credentials SET wrapped_secret = $3
		  WHERE tenant_id = $1 AND user_id = $2`,
		tenantA, adminA, "T:"+secret)
	testutil.Exec(t, raw,
		`UPDATE mfa_credentials SET wrapped_secret = $3
		  WHERE tenant_id = $1 AND user_id = $2`,
		tenantB, adminB, "T:"+secret)
	if err := svc.VerifyTOTPEnrollment(ctx, tenantB, adminB,
		rfc6238Code(t, secret, clock, sha256.New)); err != nil {
		t.Fatalf("confirm the second tenant's enrolment: %v", err)
	}

	code := rfc6238Code(t, secret, clock, sha256.New)
	if _, _, ok, err := svc.VerifyFactor(ctx, tenantA, adminA, "totp", code); err != nil || !ok {
		t.Fatalf("the first tenant's use was refused (ok=%v err=%v)", ok, err)
	}
	if _, _, ok, err := svc.VerifyFactor(ctx, tenantB, adminB, "totp", code); err != nil || !ok {
		t.Errorf("tenant B's use of the same code was refused by tenant A's (ok=%v err=%v). "+
			"The cache is keyed per tenant and a replay in one tenant is not a replay in "+
			"another", ok, err)
	}
}

// This is the one that matters. The two requests carry the same code, so a
// read-then-write cache lets both of them read "not used", both decide they are
// first, and both authenticate. Only a single atomic statement can decide.
//
// The identity suite runs with -p 1, because the database-backed suites share one
// database - but that is package-level. Goroutines inside one test still run
// concurrently, and this test is concurrent on purpose.
func TestTwoConcurrentUsesOfTheSameCodeProduceExactlyOneSuccess(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, secret := enrolAdmin(t, svc, tenant, site)
	code := rfc6238Code(t, secret, clock, sha256.New)

	const racers = 8
	var (
		start     = make(chan struct{})
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		failures  []error
	)

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", code)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failures = append(failures, err)
			case ok:
				succeeded++
			}
		}()
	}
	close(start)
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("%d of %d concurrent presentations of the same code succeeded, want exactly "+
			"one. Read-then-write is a race: every caller reads the same unspent state and "+
			"every caller decides it is first", succeeded, racers)
	}
	for _, err := range failures {
		t.Errorf("a concurrent verification returned an error rather than a refusal: %v", err)
	}
}

// One row per spent (tenant, factor, step), and it survives the process that
// wrote it: the step the operator spent is still recorded when a different
// Service - a different pool, a different process - checks it.
func TestTheSpentStepIsRecordedInTheDatabase(t *testing.T) {
	svc, tenant, site := service(t)
	ctx := context.Background()
	admin, secret := enrolAdmin(t, svc, tenant, site)

	code := rfc6238Code(t, secret, clock, sha256.New)
	if _, _, ok, err := svc.VerifyFactor(ctx, tenant, admin, "totp", code); err != nil || !ok {
		t.Fatalf("the first presentation was refused (ok=%v err=%v)", ok, err)
	}

	var rows int
	if err := testutil.Pool(t).QueryRow(ctx, `
		SELECT count(*) FROM mfa_used_codes
		 WHERE tenant_id = $1 AND user_id = $2 AND step_counter = $3`,
		tenant, admin, clock.Unix()/30).Scan(&rows); err != nil {
		t.Fatalf("read the spent-step row: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d rows recorded for the spent step, want 1. The record lives in the "+
			"database; a cache in process memory is emptied by a restart, which is "+
			"something an attacker with partial access to a site can arrange", rows)
	}

	// A second Service over the same database - the shape of a restarted process.
	restarted := &identity.Service{Pool: svc.Pool, Now: svc.Now, SecretStore: svc.SecretStore}
	if _, _, ok, err := restarted.VerifyFactor(ctx, tenant, admin, "totp", code); err != nil || ok {
		t.Errorf("a fresh Service accepted the code (ok=%v err=%v). The cache has to outlive "+
			"the process that wrote it", ok, err)
	}
}
