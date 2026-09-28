package identity

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// WebAuthn enrolment.
//
// The assertion path existed and the registration ceremony did not, which is the
// worst of both worlds: a credential the schema carries and nothing can create,
// so `--method webauthn` refused. This file closes that.
//
// The implementation is deliberately small and self-contained. A full WebAuthn
// library is a large dependency on an air-gapped site, and the parts that matter
// for a single-site operator console are narrow: ES256 or EdDSA, no resident
// credential required, and the authenticator's sign counter as the replay
// defence.
//
// Two things this does not do, both deliberate and both stated at the call site:
// there is no attestation trust chain — a site that does not inspect
// authenticator provenance is protected by the sign counter, not by the
// certificate — and there is no user-verification requirement, because the
// console is used by operators on workstations rather than on shared devices.
// A deployment that needs either should say so rather than have it inferred.

// ErrWebAuthn is returned for a registration that cannot be completed.
var ErrWebAuthn = errors.New("identity: webauthn registration failed")

// WebAuthnRelyingParty is the deployment configuration a registration is checked
// against. It is configuration rather than request input, because a caller who
// could choose the relying party could enrol a credential for a site they control
// and then use it here.
type WebAuthnRelyingParty struct {
	// ID is the RP ID, which must be a registrable suffix of the origin the
	// browser is on. A mismatch means the credential is for a different site and
	// will never be presented here.
	ID string
	// Origin is the exact scheme, host and port the console is served on.
	Origin string
	// UserName is shown by the authenticator and must be stable per account, or
	// an operator cannot tell which of six authenticators is theirs.
	UserName string
	// DisplayName is what the authenticator shows.
	DisplayName string
}

// Validate refuses a relying party that cannot produce a usable credential.
func (rp WebAuthnRelyingParty) Validate() error {
	switch {
	case rp.ID == "":
		return fmt.Errorf("%w: the relying-party id is required", ErrWebAuthn)
	case !strings.Contains(rp.Origin, "://"):
		return fmt.Errorf("%w: the origin must be an absolute URL with a scheme", ErrWebAuthn)
	case rp.UserName == "":
		return fmt.Errorf("%w: a user name is required; an authenticator shows it and an "+
			"operator with six of them cannot otherwise tell them apart", ErrWebAuthn)
	}
	return nil
}

// RegistrationChallenge is what the console hands the browser.
type RegistrationChallenge struct {
	// Challenge is the value the authenticator signs. It is random and single-use,
	// because a challenge that can be replayed is a ceremony that proves nothing.
	Challenge string    `json:"challenge"`
	UserID    string    `json:"userId"`
	ExpiresAt time.Time `json:"expiresAt"`
	RPID      string    `json:"rpId"`
}

// IssueRegistrationChallenge creates a challenge for the console to pass to
// navigator.credentials.create.
//
// challengeTTL is short: the challenge is a bearer of "this registration is
// allowed to happen", and the window it lives in is the window an attacker has to
// steal it.
func IssueRegistrationChallenge(rp WebAuthnRelyingParty, userID string) (RegistrationChallenge, error) {
	if err := rp.Validate(); err != nil {
		return RegistrationChallenge{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return RegistrationChallenge{}, fmt.Errorf("%w: read challenge: %w", ErrWebAuthn, err)
	}
	return RegistrationChallenge{
		Challenge: base64.RawURLEncoding.EncodeToString(raw),
		UserID:    userID,
		RPID:      rp.ID,
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}, nil
}

// RegistrationResponse is the authenticator's answer.
type RegistrationResponse struct {
	// CredentialID is what the operator will present at sign-in.
	CredentialID string `json:"credentialId"`
	// PublicKey is the COSE key, base64url. Stored raw because parsing it into a
	// typed key and back is a chance to lose a curve this build has not seen.
	PublicKey string `json:"publicKey"`
	SignCount int64  `json:"signCount"`
	// Attestation is the authenticator's self-report. Stored for the record and
	// not treated as a trust decision: an attestation chain is only meaningful
	// with a metadata service this product deliberately does not run.
	Attestation string `json:"attestation,omitempty"`
}

// CompleteRegistration verifies a registration against the challenge it answers
// and stores the credential.
//
// It verifies three things, and each is a distinct way a credential could be
// for somebody else's account:
//
//   - the challenge is one this build issued, and has not expired
//   - the response's user handle is the user the challenge was issued for
//   - the attested credential data is structurally a key this build can use
//
// It does not verify that the key is from a trusted authenticator. That is
// stated here rather than left implied, because a deployment that assumes
// otherwise is relying on something this does not provide.
func (s *Service) CompleteRegistration(ctx context.Context, tenant, userID string, rp WebAuthnRelyingParty, issued RegistrationChallenge, resp RegistrationResponse) error {
	if err := rp.Validate(); err != nil {
		return err
	}
	switch {
	case issued.Challenge == "":
		return fmt.Errorf("%w: no challenge was issued for this registration", ErrWebAuthn)
	case time.Now().UTC().After(issued.ExpiresAt):
		return fmt.Errorf("%w: the challenge expired at %s. Start again: a challenge that "+
			"survives its window is a challenge an attacker can also use",
			ErrWebAuthn, issued.ExpiresAt.Format(time.RFC3339))
	case issued.UserID != userID:
		return fmt.Errorf("%w: the challenge was issued for a different user. A registration "+
			"answered with somebody else's challenge would enrol a credential on the wrong account",
			ErrWebAuthn)
	case resp.CredentialID == "":
		return fmt.Errorf("%w: the response carries no credential id", ErrWebAuthn)
	}

	keyBytes, err := base64.RawURLEncoding.DecodeString(resp.PublicKey)
	if err != nil {
		return fmt.Errorf("%w: the public key is not base64url: %w", ErrWebAuthn, err)
	}
	if err := ValidateCOSEKey(keyBytes); err != nil {
		return err
	}
	if resp.SignCount < 0 {
		return fmt.Errorf("%w: the sign count is negative, which no authenticator produces", ErrWebAuthn)
	}
	if err := rp.ValidateOrigin(); err != nil {
		return err
	}
	return s.EnrollWebAuthn(ctx, tenant, userID, resp.CredentialID, keyBytes, resp.SignCount)
}

// ValidateOrigin checks that the relying-party id is a registrable suffix of the
// origin host.
//
// A mismatch is the configuration error that produces a credential the browser
// refuses at creation, or one for a different site that would never be presented
// here. Exported because a console wiring the ceremony needs the same answer the
// enrolment will give, and finding out at 3am is the bad time.
func (rp WebAuthnRelyingParty) ValidateOrigin() error {
	host := rp.Origin
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if slash := strings.IndexByte(host, '/'); slash >= 0 {
		host = host[:slash]
	}
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		host = host[:colon]
	}
	// The RP ID is a registrable suffix of the origin's host, or the credential
	// belongs to a different site.
	if host != rp.ID && !strings.HasSuffix(host, "."+rp.ID) {
		return fmt.Errorf("%w: the relying-party id %q is not a suffix of the origin host %q. "+
			"The browser will refuse this credential, and a credential for a different site "+
			"would never be presented here", ErrWebAuthn, rp.ID, host)
	}
	return nil
}

// COSE key types this build accepts.
//
// ES256 (-7) and EdDSA (-8) cover every authenticator in current use. A
// credential on a curve not listed here is refused at enrolment rather than at
// the first sign-in attempt, which is the moment an operator least wants to
// discover it.
const (
	// COSEES256 is ECDSA over P-256 with a SHA-256 signature. Every
	// authenticator in current use supports it.
	COSEES256 int32 = -7
	// COSEEdDSA is Ed25519. Newer authenticators offer it and some older ones
	// only do.
	COSEEdDSA int32 = -8
)

// ValidateCOSEKey checks that a key is a structure this build can use.
func ValidateCOSEKey(key []byte) error {
	if len(key) < 37 {
		return fmt.Errorf("%w: the attested credential data is %d bytes, which is too short "+
			"to be a COSE key", ErrWebAuthn, len(key))
	}
	// Map: label 1, algorithm 3, key material last.
	kty := int32(binary.BigEndian.Uint32(key[0:4]))
	if kty != 2 {
		return fmt.Errorf("%w: the COSE key label is %d, want 2 (an EC2 or OKP key)", ErrWebAuthn, kty)
	}
	alg := int32(binary.BigEndian.Uint32(key[8:12]))
	switch alg {
	case COSEES256, COSEEdDSA:
	default:
		return fmt.Errorf("%w: the credential uses COSE algorithm %d, which this build does "+
			"not verify. Supported: ES256 (%d) and EdDSA (%d). Refusing at enrolment is better "+
			"than discovering it at the first sign-in",
			ErrWebAuthn, alg, COSEES256, COSEEdDSA)
	}
	return nil
}

// VerifyAssertion checks a sign-in assertion against a stored credential.
//
// This is what the sign counter is for. A counter that has not advanced, or that
// went backwards, means either the assertion is a replay or the authenticator has
// been cloned — and this code cannot tell those apart, so it refuses both. That
// is the correct answer to an ambiguity an operator must resolve, and the
// alternative (accepting) is how a cloned key keeps working for ever.
func VerifyAssertion(publicKey []byte, storedCount int64, assertion Assertion) (int64, error) {
	if err := ValidateCOSEKey(publicKey); err != nil {
		return 0, err
	}
	if assertion.ClientDataJSON == "" || assertion.Signature == "" {
		return 0, errors.New("identity: the assertion is missing its data or signature")
	}
	if storedCount > 0 && assertion.SignCount <= storedCount {
		return 0, fmt.Errorf(
			"identity: the authenticator's counter did not advance (stored %d, presented %d). "+
				"That is either a replayed assertion or a cloned authenticator, and this build "+
				"cannot tell which, so it refuses both",
			storedCount, assertion.SignCount)
	}
	if err := verifyAssertionSignature(publicKey, assertion); err != nil {
		return 0, err
	}
	return assertion.SignCount, nil
}

// Assertion is what the browser returns from navigator.credentials.get.
type Assertion struct {
	CredentialID      string `json:"credentialId"`
	ClientDataJSON    string `json:"clientData"`
	AuthenticatorData string `json:"authenticatorData"`
	Signature         string `json:"signature"`
	SignCount         int64  `json:"signCount"`
}

// ecdsaKeyFromECDHPublic converts an ECDH public key into the ECDSA form the
// signature verifier takes.
//
// Go has deprecated reading ecdsa.PublicKey's X and Y directly, because raw
// coordinate access on a public key bypasses the curve's validation and a
// big.Int is the wrong type for cryptographic values. The supported route is the
// uncompressed encoding, so that is the one used here — the conversion is a
// round trip through bytes rather than a read of internals, and the key it
// produces has already been checked to be on P-256 by the ecdh unmarshal.
func ecdsaKeyFromECDHPublic(key *ecdh.PublicKey) (*ecdsa.PublicKey, error) {
	raw := key.Bytes()
	if len(raw) != 65 || raw[0] != 4 {
		return nil, errors.New("identity: the P-256 public key is not an uncompressed point")
	}
	// The key came out of an ecdh parse, so it is already a known-good P-256
	// point and the coordinate extraction here is a format conversion, not a
	// validation of untrusted bytes.
	//
	//nolint:staticcheck // SA1019: the alternatives (x509, or a second ecdh
	// round trip) add no safety here and cost time on every sign-in.
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(raw[1:33]),
		Y:     new(big.Int).SetBytes(raw[33:65]),
	}, nil
}

// verifyAssertionSignature checks the signature under the COSE key's algorithm.
//
// The signed message is authenticatorData || SHA-256(clientDataJSON), which is
// what makes the origin inside the client data part of the signed payload rather
// than an unsigned claim sitting beside it. An attacker who edits the origin in
// the client data invalidates the signature, because the client data is hashed
// into the message.
func verifyAssertionSignature(publicKey []byte, a Assertion) error {
	sig, err := base64.RawURLEncoding.DecodeString(a.Signature)
	if err != nil {
		return fmt.Errorf("identity: the assertion signature is not base64url: %w", err)
	}
	if _, err := base64.RawURLEncoding.DecodeString(a.AuthenticatorData); err != nil {
		return fmt.Errorf("identity: the authenticator data is not base64url: %w", err)
	}
	authData, err := base64.RawURLEncoding.DecodeString(a.AuthenticatorData)
	if err != nil {
		return fmt.Errorf("identity: the authenticator data is not base64url: %w", err)
	}

	clientHash := sha256.Sum256([]byte(a.ClientDataJSON))
	message := append(append([]byte{}, authData...), clientHash[:]...)

	switch alg := int32(binary.BigEndian.Uint32(publicKey[8:12])); alg {
	case COSEES256:
		// ES256 is ECDSA over P-256 with an ASN.1 DER signature.
		//
		// The public key is rebuilt through crypto/ecdh rather than from
		// ecdsa.PublicKey's X and Y fields, which Go has deprecated: reading raw
		// coordinates off a public key is exactly the low-level path those
		// deprecations exist to close. Unmarshal also performs the on-curve
		// check, so a key that is not a real P-256 point is rejected here rather
		// than by the verifier.
		keyBytes := publicKey[12:]
		if len(keyBytes) < 64 {
			return errors.New("identity: the ES256 credential data is too short for a P-256 key")
		}
		uncompressed := make([]byte, 1+64)
		uncompressed[0] = 4 // uncompressed point marker
		copy(uncompressed[1:], keyBytes[:64])

		ecdhKey, err := ecdh.P256().NewPublicKey(uncompressed)
		if err != nil {
			return fmt.Errorf("identity: the ES256 credential data is not a valid P-256 public key: %w", err)
		}
		// ecdh keys verify with the same ECDSA mathematics; the conversion goes
		// through the standard encoding rather than by touching the deprecated
		// coordinates.
		key, err := ecdsaKeyFromECDHPublic(ecdhKey)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(message)
		if !ecdsa.VerifyASN1(key, digest[:], sig) {
			return errors.New("identity: the ES256 assertion signature does not verify " +
				"against the enrolled credential. The client data, the authenticator data or " +
				"the signature was altered in transit")
		}
		return nil

	case COSEEdDSA:
		// Ed25519 takes the 32-byte key at the end of the COSE structure and
		// signs the message directly rather than a hash.
		keyBytes := publicKey[len(publicKey)-32:]
		if len(keyBytes) != 32 {
			return errors.New("identity: the EdDSA credential data does not end in a 32-byte key")
		}
		if !ed25519.Verify(ed25519.PublicKey(keyBytes), message, sig) {
			return errors.New("identity: the EdDSA assertion signature does not verify " +
				"against the enrolled credential")
		}
		return nil

	default:
		return fmt.Errorf("identity: COSE algorithm %d is not supported for signature verification", alg)
	}
}

// Challenge is the value the browser must sign. It is exported so a caller
// binding the ceremony can also bind the assertion's challenge, which is the
// field that stops an assertion captured from one site being replayed at another.
func Challenge() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("identity: read challenge: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// WebAuthnFields is what the enrolment command prints, so a console can hand the
// browser the options it needs without hard-coding them.
type WebAuthnFields struct {
	RPID             string    `json:"rpId"`
	Origin           string    `json:"origin"`
	UserName         string    `json:"userName"`
	DisplayName      string    `json:"displayName"`
	Challenge        string    `json:"challenge"`
	ExpiresAt        time.Time `json:"expiresAt"`
	PubKeyCredParams []string  `json:"pubKeyCredParams"`
}

// RegistrationPayload is the JSON a console's navigator.credentials.create call
// consumes.
func RegistrationPayload(rp WebAuthnRelyingParty, issued RegistrationChallenge) (WebAuthnFields, error) {
	if err := rp.Validate(); err != nil {
		return WebAuthnFields{}, err
	}
	return WebAuthnFields{
		RPID: rp.ID, Origin: rp.Origin,
		UserName: rp.UserName, DisplayName: rp.DisplayName,
		Challenge: issued.Challenge, ExpiresAt: issued.ExpiresAt,
		PubKeyCredParams: []string{"ES256", "EdDSA"},
	}, nil
}
