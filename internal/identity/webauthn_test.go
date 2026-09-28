package identity_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/identity"
)

func ctx() context.Context { return context.Background() }

// coseKey builds a COSE_Key with the given algorithm and key material: the shape
// a browser's getPublicKey returns.
func coseKey(alg int32, material []byte) []byte {
	out := make([]byte, 12, 12+len(material))
	binary.BigEndian.PutUint32(out[0:4], 2) // kty: EC2 (2) for both curves
	binary.BigEndian.PutUint32(out[4:8], 1) // alg label
	binary.BigEndian.PutUint32(out[8:12], uint32(alg))
	return append(out, material...)
}

func es256Key(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	material := make([]byte, 64)
	priv.PublicKey.X.FillBytes(material[:32])
	priv.PublicKey.Y.FillBytes(material[32:])
	return priv, coseKey(identity.COSEES256, material)
}

func eddsaKey(t *testing.T) (ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return priv, coseKey(identity.COSEEdDSA, pub)
}

func relyingParty() identity.WebAuthnRelyingParty {
	return identity.WebAuthnRelyingParty{
		ID: "console.example", Origin: "https://console.example:8443",
		UserName: "admin", DisplayName: "Administrator",
	}
}

func TestWebAuthnRelyingPartyValidation(t *testing.T) {
	invalid := map[string]identity.WebAuthnRelyingParty{
		"no rp id":        {Origin: "https://console.example", UserName: "admin"},
		"no origin":       {ID: "console.example", UserName: "admin"},
		"relative origin": {ID: "console.example", Origin: "console.example", UserName: "admin"},
		"no user name":    {ID: "console.example", Origin: "https://console.example"},
	}
	for name, r := range invalid {
		if err := r.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := relyingParty().Validate(); err != nil {
		t.Errorf("a valid relying party was refused: %v", err)
	}
}

// The relying-party id must be a suffix of the origin's host. A credential for a
// different site would never be presented here, and the browser refuses it at
// creation — so catching it at enrolment is the difference between a clear
// message and a silent dead end.
func TestOriginAndRelyingPartyMustAgree(t *testing.T) {
	good := []identity.WebAuthnRelyingParty{
		{ID: "console.example", Origin: "https://console.example:8443", UserName: "admin"},
		{ID: "example", Origin: "https://console.example", UserName: "admin"},
		{ID: "example", Origin: "https://console.example:8443/incidents", UserName: "admin"},
	}
	for _, r := range good {
		if err := r.ValidateOrigin(); err != nil {
			t.Errorf("a matching origin/id pair was refused: %v (%s / %s)", err, r.Origin, r.ID)
		}
	}
	bad := identity.WebAuthnRelyingParty{
		ID: "other.example", Origin: "https://console.example", UserName: "admin",
	}
	if err := bad.ValidateOrigin(); err == nil {
		t.Error("a relying-party id from a different site was accepted")
	}
}

func TestRegistrationChallengeIsShortLivedAndUnique(t *testing.T) {
	c, err := identity.IssueRegistrationChallenge(relyingParty(), "user-1")
	if err != nil {
		t.Fatalf("IssueRegistrationChallenge: %v", err)
	}
	if c.Challenge == "" {
		t.Fatal("no challenge was issued")
	}
	if c.RPID != "console.example" {
		t.Errorf("the challenge carries rp id %q", c.RPID)
	}
	// A challenge is a bearer of "this registration is allowed", so the window it
	// lives in is the window an attacker has to steal it.
	if ttl := time.Until(c.ExpiresAt); ttl > 10*time.Minute || ttl <= 0 {
		t.Errorf("the challenge lives for %s, want a short positive window", ttl)
	}

	other, _ := identity.IssueRegistrationChallenge(relyingParty(), "user-1")
	if other.Challenge == c.Challenge {
		t.Error("two challenges for the same user are identical")
	}
}

func TestCOSEKeyValidation(t *testing.T) {
	_, es := es256Key(t)
	_, ed := eddsaKey(t)
	if err := identity.ValidateCOSEKey(es); err != nil {
		t.Errorf("a valid ES256 COSE key was refused: %v", err)
	}
	if err := identity.ValidateCOSEKey(ed); err != nil {
		t.Errorf("a valid EdDSA COSE key was refused: %v", err)
	}

	bad := map[string][]byte{
		"too short":       {1, 2, 3},
		"wrong label":     coseKey(99, make([]byte, 64)),
		"unsupported alg": coseKey(-257, make([]byte, 64)),
	}
	for name, key := range bad {
		if err := identity.ValidateCOSEKey(key); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// Refusing an unsupported curve at enrolment beats discovering it at the first
	// sign-in, which is when an operator least wants a surprise.
	err := identity.ValidateCOSEKey(coseKey(-257, make([]byte, 64)))
	if err == nil || !strings.Contains(err.Error(), "ES256") {
		t.Errorf("the refusal should name the supported algorithms, got %v", err)
	}
}

// A registration answered with somebody else's challenge would enrol a
// credential on the wrong account, so the user binding is checked.
func TestRegistrationRefusesAnotherUsersChallenge(t *testing.T) {
	svc, tenant, _ := service(t)
	issued, err := identity.IssueRegistrationChallenge(relyingParty(), "user-1")
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	_, cose := es256Key(t)

	err = svc.CompleteRegistration(ctx(), tenant, "user-2", relyingParty(), issued,
		identity.RegistrationResponse{
			CredentialID: "cred-1", PublicKey: base64.RawURLEncoding.EncodeToString(cose),
		})
	if err == nil {
		t.Fatal("a registration was completed with another user's challenge")
	}
	if !strings.Contains(err.Error(), "different user") {
		t.Errorf("the refusal should name the mismatch, got %q", err)
	}
}

func TestRegistrationRefusesAnExpiredChallenge(t *testing.T) {
	svc, tenant, _ := service(t)
	issued, _ := identity.IssueRegistrationChallenge(relyingParty(), "user-1")
	issued.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	_, cose := es256Key(t)

	err := svc.CompleteRegistration(ctx(), tenant, "user-1", relyingParty(), issued,
		identity.RegistrationResponse{
			CredentialID: "cred-1", PublicKey: base64.RawURLEncoding.EncodeToString(cose),
		})
	if err == nil {
		t.Fatal("an expired challenge was accepted")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("the refusal should say the challenge expired, got %q", err)
	}
}

func TestRegistrationStoresTheCredential(t *testing.T) {
	svc, tenant, site := service(t)
	// The credential belongs to a real account, because that is the case: an
	// enrolment path that stores a key against nobody would be a finding, not a
	// test.
	userID, err := svc.BootstrapAdmin(ctx(), tenant, "webauthn-admin",
		"a-long-bootstrap-passphrase", site)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	issued, _ := identity.IssueRegistrationChallenge(relyingParty(), userID)
	_, cose := es256Key(t)

	err = svc.CompleteRegistration(ctx(), tenant, userID, relyingParty(), issued,
		identity.RegistrationResponse{
			CredentialID: "cred-1", PublicKey: base64.RawURLEncoding.EncodeToString(cose),
		})
	if err != nil {
		t.Fatalf("CompleteRegistration: %v", err)
	}

	// The credential is stored, so the sign-in path can find it.
	var method string
	if err := testutilQuery(t,
		`SELECT method FROM mfa_credentials WHERE tenant_id = $1 AND user_id = $2`,
		tenant, userID).Scan(&method); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if method != "webauthn" {
		t.Errorf("stored method is %q", method)
	}
}

// The sign counter is the replay defence. A counter that has not advanced, or
// that went backwards, is either a replay or a cloned authenticator, and the code
// cannot tell which — so it refuses both rather than passing one.
func TestSignCounterRefusesReplayAndClone(t *testing.T) {
	priv, es := es256Key(t)
	a := assertionSignedBy(t, priv)

	if _, err := identity.VerifyAssertion(es, 0, a); err != nil {
		t.Fatalf("a first assertion at counter 0 was refused: %v", err)
	}
	if _, err := identity.VerifyAssertion(es, 1, a); err != nil {
		t.Fatalf("an assertion whose counter advanced was refused: %v", err)
	}

	// Replayed: the same assertion, with the counter the database already has.
	_, err := identity.VerifyAssertion(es, a.SignCount, a)
	if err == nil {
		t.Fatal("a replayed assertion was accepted")
	}
	if !strings.Contains(err.Error(), "counter did not advance") {
		t.Errorf("the refusal should name the counter, got %q", err)
	}
	if !strings.Contains(err.Error(), "clone") {
		t.Errorf("the refusal should say why the ambiguity is refused, got %q", err)
	}

	// And a counter that went backwards — the other way the same ambiguity
	// shows up — is refused for the same reason.
	if _, err := identity.VerifyAssertion(es, a.SignCount+1, a); err == nil {
		t.Error("an assertion whose counter went backwards was accepted")
	}
}

// A signature is checked, not merely present. An edited client data or
// authenticator data must fail.
func TestAssertionSignatureIsVerified(t *testing.T) {
	priv, es := es256Key(t)
	a := assertionSignedBy(t, priv)

	if _, err := identity.VerifyAssertion(es, 0, a); err != nil {
		t.Fatalf("a genuine assertion was refused: %v", err)
	}

	// The client data carries the origin and the ceremony type; editing either
	// breaks the signature, because the client data is hashed into the signed
	// payload rather than sitting beside it unsigned.
	tampered := a
	tampered.ClientDataJSON = strings.Replace(a.ClientDataJSON, "console.example", "evil.example", 1)
	if _, err := identity.VerifyAssertion(es, 0, tampered); err == nil {
		t.Error("an assertion with edited client data was accepted")
	}

	noSig := a
	noSig.Signature = ""
	if _, err := identity.VerifyAssertion(es, 0, noSig); err == nil {
		t.Error("an assertion with no signature was accepted")
	}
}

func TestEdDSAAssertionIsVerified(t *testing.T) {
	priv, cose := eddsaKey(t)
	a := assertionForEdDSA(t, priv)

	if _, err := identity.VerifyAssertion(cose, 0, a); err != nil {
		t.Fatalf("a genuine EdDSA assertion was refused: %v", err)
	}
	tampered := a
	tampered.ClientDataJSON = strings.Replace(a.ClientDataJSON, "webauthn.get", "webauthn.create", 1)
	if _, err := identity.VerifyAssertion(cose, 0, tampered); err == nil {
		t.Error("an assertion with edited client data was accepted under EdDSA")
	}
}

// A signature made by a different key must not verify against the enrolled one.
func TestSignatureFromAnotherKeyIsRefused(t *testing.T) {
	_, es := es256Key(t)
	otherPriv, _ := es256Key(t)
	a := assertionSignedBy(t, otherPriv)
	if _, err := identity.VerifyAssertion(es, 0, a); err == nil {
		t.Error("an assertion signed by a different key verified against the enrolled credential")
	}
}

// assertionSignedBy builds a genuine ES256 assertion.
func assertionSignedBy(t *testing.T, priv *ecdsa.PrivateKey) identity.Assertion {
	t.Helper()
	clientData := `{"type":"webauthn.get","origin":"https://console.example:8443","challenge":"abc"}`
	authData := authenticatorData()
	sum := sha256.Sum256(append(append([]byte{}, authData...), clientHash(clientData)...))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return identity.Assertion{
		CredentialID:      "cred-1",
		ClientDataJSON:    clientData,
		AuthenticatorData: base64.RawURLEncoding.EncodeToString(authData),
		Signature:         base64.RawURLEncoding.EncodeToString(sig),
		SignCount:         7,
	}
}

func assertionForEdDSA(t *testing.T, priv ed25519.PrivateKey) identity.Assertion {
	t.Helper()
	clientData := `{"type":"webauthn.get","origin":"https://console.example:8443","challenge":"abc"}`
	message := append(append([]byte{}, authenticatorData()...), clientHash(clientData)...)
	return identity.Assertion{
		CredentialID:      "cred-1",
		ClientDataJSON:    clientData,
		AuthenticatorData: base64.RawURLEncoding.EncodeToString(authenticatorData()),
		Signature:         base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, message)),
		SignCount:         7,
	}
}

// authenticatorData is a minimal 37-byte structure: the 32-byte RP ID hash, a
// counter, then the flags byte. Only the flags matter to this build's checks.
func authenticatorData() []byte {
	ad := make([]byte, 37)
	ad[32] = 0x05 // user present, user verified
	return ad
}

func clientHash(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
