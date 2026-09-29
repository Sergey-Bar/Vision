package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/api"
)

// The primary login path for every enrolled operator.
//
// The handler read the principal off res.Session, and res.Session is nil by
// construction on the MFA branch — the early return above it is the non-MFA
// case. So a nil dereference turned every MFA login for every operator with a
// second factor into a 500, and the only logins that worked were bootstrap
// accounts with nothing enrolled. The test drives the route, not the service,
// because the defect was in the route.
func TestLoginWithAnEnrolledFactorReturnsTheChallengeNotAFiveHundred(t *testing.T) {
	s, tenant, _ := server(t)
	ctx := context.Background()
	admin := bootstrapAdminID(t, s, tenant)

	if _, _, err := s.Identity.EnrollTOTP(ctx, tenant, admin, "admin"); err != nil {
		t.Fatalf("enrol: %v", err)
	}

	rec := callAnon(t, s, http.MethodPost, "/v1/auth/login", map[string]any{
		"username": "admin", "password": "a-long-bootstrap-passphrase",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("an MFA-required login returned %d: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		MFARequired bool   `json:"mfaRequired"`
		ChallengeID string `json:"challengeId"`
		Session     struct {
			PrincipalID string   `json:"principalId"`
			Roles       []string `json:"roles"`
		} `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.MFARequired {
		t.Error("the response does not say a second factor is required")
	}
	if body.ChallengeID == "" {
		t.Error("the response carries no challenge id; the operator cannot proceed")
	}
	if body.Session.PrincipalID != admin {
		t.Errorf("the response names principal %q, want %q. The principal is read from the "+
			"LoginResult, not from the nil session", body.Session.PrincipalID, admin)
	}
	if len(body.Session.Roles) == 0 {
		t.Error("the response names no roles for the operator being asked to authenticate")
	}
}

// The second step, end to end, and then a replay of the same challenge. The
// challenge is a bearer of "the password was right"; two calls with the same id
// used to both return a session.
func TestMFAVerifyConsumesTheChallengeItRedeems(t *testing.T) {
	s, tenant, _ := server(t)
	ctx := context.Background()
	admin := bootstrapAdminID(t, s, tenant)

	secret, _, err := s.Identity.EnrollTOTP(ctx, tenant, admin, "admin")
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	// The enrolment consumes a TOTP step, and RFC 6238 forbids accepting a code twice, so
	// the login below uses a later step rather than this one.
	if err := s.Identity.VerifyTOTPEnrollment(ctx, tenant, admin, codeAt(t, secret, clock)); err != nil {
		t.Fatalf("confirm the enrolment: %v", err)
	}

	rec := callAnon(t, s, http.MethodPost, "/v1/auth/login", map[string]any{
		"username": "admin", "password": "a-long-bootstrap-passphrase",
	})
	var login struct {
		ChallengeID string `json:"challengeId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode: %v", err)
	}

	body := map[string]any{
		"challengeId": login.ChallengeID, "method": "totp", "code": codeAt(t, secret, clock.Add(30*time.Second)),
	}
	first := callAnon(t, s, http.MethodPost, "/v1/auth/mfa/verify", body)
	if first.Code != http.StatusOK {
		t.Fatalf("the first verification returned %d: %s", first.Code, first.Body.String())
	}
	if len(first.Result().Cookies()) == 0 {
		t.Error("a successful verification set no session cookie")
	}

	second := callAnon(t, s, http.MethodPost, "/v1/auth/mfa/verify", body)
	if second.Code == http.StatusOK {
		t.Errorf("the same challenge id verified a second time and returned %d. A challenge "+
			"that can be used twice is a password equivalent", second.Code)
	}
	if !strings.Contains(second.Body.String(), "challenge") {
		t.Errorf("the replay was refused with %q, which does not say the challenge was the "+
			"problem. An operator who mistyped a code needs to be told to restart with the "+
			"password, not sent round again", second.Body.String())
	}
}

// bootstrapAdminID reads the administrator server() created. Its id is derived
// from the tenant and the username rather than generated, so the test reads it
// instead of recomputing a hash it would only be testing against itself.
func bootstrapAdminID(t *testing.T, s *api.Server, tenant string) string {
	t.Helper()
	var id string
	if err := s.Pool.For(tenant, "test").QueryRow(context.Background(),
		`SELECT user_id FROM users WHERE tenant_id = $1 AND lower(username) = 'admin'`,
		tenant).Scan(&id); err != nil {
		t.Fatalf("read the bootstrap administrator: %v", err)
	}
	return id
}

// codeAt is the RFC 6238 code for a SHA-256 secret, computed here rather than
// borrowed from the verifier, so the route is tested against something that is
// not the code under test.
func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("the enrolled secret is not base32: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha256.New, raw)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[off])&0x7f)<<24 |
		(uint32(sum[off+1])&0xff)<<16 |
		(uint32(sum[off+2])&0xff)<<8 |
		(uint32(sum[off+3]) & 0xff)
	return fmt.Sprintf("%06d", value%1000000)
}
