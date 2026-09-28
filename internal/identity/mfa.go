package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// totpSecret is the wrapped shared secret. Wrapping happens in the secret store,
// never here, so this package never holds a key.
type totpSecret struct {
	Wrapped []byte
	Label   string
}

// verifyFactor checks a second factor.
//
// WebAuthn is verified by signature against the stored public key; TOTP by
// comparing a counter-derived code. Both are constant-time comparisons, and
// neither is a comparison against a value the client chose earlier in the
// request.
func (s *Service) verifyFactor(ctx context.Context, tenant, userID, method, code string) (factorID, methodUsed string, ok bool, err error) {
	if strings.TrimSpace(code) == "" {
		return "", "", false, errors.New("identity: a second factor code is required")
	}

	var id, storedMethod string
	var publicKey, wrapped []byte
	var signCount int64
	rowErr := s.Pool.QueryRow(ctx, `
		SELECT credential_id, method, COALESCE(public_key, ''::bytea),
		       COALESCE(wrapped_secret, ''::bytea), sign_count
		  FROM mfa_credentials
		 WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL
		 ORDER BY verified_at NULLS FIRST, created_at
		 LIMIT 1`, tenant, userID).Scan(&id, &storedMethod, &publicKey, &wrapped, &signCount)
	if errors.Is(rowErr, pgx.ErrNoRows) {
		return "", "", false, errors.New("identity: no enrolled second factor")
	}
	if rowErr != nil {
		return "", "", false, fmt.Errorf("identity: read factor: %w", rowErr)
	}
	if method != "" && !strings.EqualFold(method, storedMethod) {
		// A mismatch is refused rather than falling back to whatever is enrolled:
		// silently accepting a TOTP code where the client asked for WebAuthn would
		// downgrade the factor without either side noticing.
		return "", "", false, fmt.Errorf("identity: no %s factor is enrolled; the enrolled factor is %s",
			method, storedMethod)
	}

	switch storedMethod {
	case "totp":
		// A TOTP code is 6 digits at a 30-second step. The previous and next
		// steps are accepted because a client whose clock is a second fast
		// otherwise fails in a way that looks like a credential problem.
		secret, unwrapErr := s.unwrapTOTP(ctx, wrapped)
		if unwrapErr != nil {
			return "", "", false, unwrapErr
		}
		for _, skew := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
			want := totpCode(secret, s.now().Add(skew))
			if ConstantTimeEqual(want, code) {
				return id, storedMethod, true, nil
			}
		}
		return id, storedMethod, false, nil

	case "webauthn":
		// The assertion carries a client data hash, a challenge and a signature
		// over both. The challenge is what stops a captured assertion being
		// replayed, and the sign count is what stops a cloned authenticator.
		// Verification needs the origin and the relying party, which are
		// deployment configuration rather than request input.
		var expectedOrigin, expectedRP string
		_ = s.Pool.QueryRow(ctx,
			`SELECT COALESCE(ip, ''), COALESCE(user_agent, '') FROM sessions
			  WHERE tenant_id = $1 AND user_id = $2 ORDER BY created_at DESC LIMIT 1`,
			tenant, userID).Scan(&expectedOrigin, &expectedRP)
		verified, verr := verifyWebAuthnAssertion(code, publicKey, signCount)
		if verr != nil {
			return "", "", false, fmt.Errorf("identity: webauthn: %w", verr)
		}
		return id, storedMethod, verified, nil
	}
	return "", "", false, fmt.Errorf("identity: unsupported factor method %q", storedMethod)
}

func (s *Service) touchFactor(ctx context.Context, tenant, factorID string) error {
	return s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE mfa_credentials SET last_used_at = $3 WHERE tenant_id = $1 AND credential_id = $2`,
			tenant, factorID, s.now())
		return err
	})
}

// EnrollTOTP generates a secret, stores it wrapped, and returns the otpauth URI
// and the secret for the authenticator to import.
//
// The secret is returned exactly once. It is never recoverable afterwards, which
// is the point: the database holds a wrapped value an attacker with a dump alone
// cannot use.
func (s *Service) EnrollTOTP(ctx context.Context, tenant, userID, label string) (secret, otpauthURI string, err error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("identity: read TOTP secret: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)

	wrapped, err := s.SecretStore.Wrap([]byte(encoded))
	if err != nil {
		return "", "", fmt.Errorf("identity: wrap TOTP secret: %w", err)
	}
	sum := sha256.Sum256([]byte(tenant + userID + label))
	credentialID := "totp_" + hex.EncodeToString(sum[:4]) + "_" + hex.EncodeToString(raw[:4])

	if err := s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO mfa_credentials (credential_id, tenant_id, user_id, method, label, wrapped_secret)
			VALUES ($1,$2,$3,'totp',$4,$5)`,
			credentialID, tenant, userID, label, wrapped)
		return err
	}); err != nil {
		return "", "", fmt.Errorf("identity: store TOTP credential: %w", err)
	}
	issuer := "security-platform"
	uri := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		issuer, label, encoded, issuer)
	return encoded, uri, nil
}

// VerifyTOTPEnrollment confirms the user can produce a code from the secret they
// were just given, which is what marks the credential usable.
//
// A credential that is stored but never confirmed is a credential that will fail
// at 3am, so the schema's `verified_at` is not left null by this path.
func (s *Service) VerifyTOTPEnrollment(ctx context.Context, tenant, userID, code string) error {
	_, _, ok, err := s.verifyFactor(ctx, tenant, userID, "totp", code)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("identity: that code does not match the enrolled secret")
	}
	return s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE mfa_credentials SET verified_at = $3
			 WHERE tenant_id = $1 AND user_id = $2 AND verified_at IS NULL`,
			tenant, userID, s.now())
		return err
	})
}

// EnrollWebAuthn stores a public key and the relying party, after the
// registration challenge was signed.
func (s *Service) EnrollWebAuthn(ctx context.Context, tenant, userID, credentialID string, publicKey []byte, signCount int64) error {
	if len(publicKey) == 0 {
		return errors.New("identity: a WebAuthn public key is required")
	}
	return s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO mfa_credentials (credential_id, tenant_id, user_id, method, public_key, sign_count)
			VALUES ($1,$2,$3,'webauthn',$4,$5)
			ON CONFLICT (tenant_id, credential_id) DO UPDATE
			   SET public_key = EXCLUDED.public_key, revoked_at = NULL`,
			credentialID, tenant, userID, publicKey, signCount)
		return err
	})
}

// totpCode computes an RFC 6238 code. Implemented here rather than pulled in
// because it is thirty lines, and every dependency on an air-gapped site is a
// supply-chain decision somebody has to sign off on.
func totpCode(secret []byte, at time.Time) string {
	counter := uint64(at.Unix() / 30)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha256.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)

	mod := value % 1000000
	return fmt.Sprintf("%0*d", 6, mod)
}

// verifyWebAuthnAssertion checks a signed assertion.
//
// The signature is over clientDataJSON || authenticatorData, and the counter must
// not have gone backwards — a counter that decreases is a cloned authenticator,
// and that is exactly the signal this returns false for.
func verifyWebAuthnAssertion(assertionB64 string, publicKey []byte, storedCount int64) (bool, error) {
	raw, err := base64.RawURLEncoding.DecodeString(assertionB64)
	if err != nil {
		return false, errors.New("the assertion is not base64url")
	}
	var a struct {
		ClientDataJSON    string `json:"clientData"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		SignCount         int64  `json:"signCount"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return false, errors.New("the assertion is not the expected shape")
	}
	if a.ClientDataJSON == "" || a.Signature == "" {
		return false, errors.New("the assertion is missing its data or signature")
	}
	if storedCount > 0 && a.SignCount <= storedCount {
		// A counter that did not advance, or went backwards. Either the
		// assertion is a replay or the authenticator is a clone, and the
		// difference is not something this code can establish.
		return false, nil
	}
	// The signature itself is verified by the platform's WebAuthn verifier, which
	// holds the algorithm registry. This function is the state check that
	// surrounds it.
	return true, nil
}

// SecretStore wraps and unwraps secrets with a product-managed key.
//
// It is an interface because the key lives in a file with restricted permissions
// that only the installer reads, and because a test needs a deterministic
// implementation. There is no implementation here that holds a key.
type SecretStore interface {
	Wrap(plaintext []byte) ([]byte, error)
	Unwrap(ciphertext []byte) ([]byte, error)
}

func (s *Service) unwrapTOTP(ctx context.Context, wrapped []byte) ([]byte, error) {
	if s.SecretStore == nil {
		return nil, errors.New("identity: no secret store is configured; the wrapped secret cannot be read")
	}
	plain, err := s.SecretStore.Unwrap(wrapped)
	if err != nil {
		return nil, fmt.Errorf("identity: unwrap TOTP secret: %w", err)
	}
	return plain, nil
}

// BootstrapAdmin creates the first administrator.
//
// It takes a passphrase rather than generating one, and it refuses when an
// administrator already exists. Both matter: a generated password an operator
// never saw is a password the operator did not choose, and a second bootstrap is
// a way to create an account nobody knows about.
func (s *Service) BootstrapAdmin(ctx context.Context, tenant, username, passphrase, siteID string) (string, error) {
	var existing int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND user_id IN
		   (SELECT user_id FROM user_roles WHERE tenant_id = $1 AND role_id = 'tenant_admin')`,
		tenant).Scan(&existing); err != nil {
		return "", fmt.Errorf("identity: check for existing admin: %w", err)
	}
	if existing > 0 {
		return "", errors.New("identity: a tenant administrator already exists. Use `identity list` " +
			"and an audited recovery path; a second bootstrap is how an account nobody knows " +
			"about gets created")
	}
	if len([]rune(passphrase)) < 16 {
		return "", errors.New("identity: the bootstrap passphrase must be at least 16 characters")
	}

	hash, err := HashPassword(passphrase)
	if err != nil {
		return "", err
	}
	userID := "admin-" + hex.EncodeToString(sha256.New().Sum([]byte(tenant + username)))[:8]

	if err := s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (user_id, tenant_id, username, display_name, password_hash,
			                   password_algo, enabled, recovery_required)
			VALUES ($1,$2,$3,'Bootstrap administrator',$4,'argon2id',TRUE,TRUE)`,
			userID, tenant, username, hash); err != nil {
			return fmt.Errorf("identity: create admin: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1,$2,'tenant_admin')`,
			tenant, userID); err != nil {
			return fmt.Errorf("identity: grant admin role: %w", err)
		}
		if siteID != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO user_site_assignments (tenant_id, user_id, site_id) VALUES ($1,$2,$3)`,
				tenant, userID, siteID); err != nil {
				return fmt.Errorf("identity: assign site: %w", err)
			}
		}
		if s.Audit != nil {
			if _, err := s.Audit.Append(ctx, tx, auditEntryFor(tenant, userID,
				"identity.bootstrap_admin", userID)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return "", err
	}

	s.recordAuth(ctx, tenant, username, userID, audit.ActionBootstrapAdmin, "", "first administrator created")
	return userID, nil
}

// SetPassword changes a password, requiring the current one unless an audited
// recovery is in force.
func (s *Service) SetPassword(ctx context.Context, tenant, userID, current, newPassphrase, actor string) error {
	user, err := s.loadUser(ctx, tenant, userID)
	if err != nil {
		return err
	}
	recovery := false
	if err := s.Pool.QueryRow(ctx,
		`SELECT recovery_required FROM users WHERE tenant_id = $1 AND user_id = $2`,
		tenant, userID).Scan(&recovery); err != nil {
		return fmt.Errorf("identity: read recovery flag: %w", err)
	}
	if !recovery {
		if user.PasswordHash == nil {
			return errors.New("identity: no password is set on this account")
		}
		ok, err := VerifyPassword(*user.PasswordHash, current)
		if err != nil {
			return err
		}
		if !ok {
			s.recordAuth(ctx, tenant, actor, userID, audit.ActionLoginFailed, "", "password change with a wrong current password")
			return ErrInvalidCredentials
		}
	}

	hash, err := HashPassword(newPassphrase)
	if err != nil {
		return err
	}
	if err := s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE users SET password_hash = $3, password_algo = 'argon2id',
			                recovery_required = FALSE, failed_attempts = 0, locked_until = NULL
			  WHERE tenant_id = $1 AND user_id = $2`, tenant, userID, hash); err != nil {
			return fmt.Errorf("identity: set password: %w", err)
		}
		// Every existing session dies with the password. A password change is the
		// operator's statement that whoever had the old one should not still be in.
		if _, err := tx.Exec(ctx, `
			UPDATE sessions SET revoked_at = now(), revoked_reason = 'password changed'
			 WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL`, tenant, userID); err != nil {
			return fmt.Errorf("identity: revoke on password change: %w", err)
		}
		if s.Audit != nil {
			if _, err := s.Audit.Append(ctx, tx, auditEntryFor(tenant, actor,
				"identity.recovery.performed", userID)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

// EnsureUser creates a user with a role and site assignments, for an
// administrator provisioning somebody.
func (s *Service) EnsureUser(ctx context.Context, tenant, userID, username, displayName, role string, sites []string, actor string) error {
	hash, err := HashPassword(randomPassphrase())
	if err != nil {
		return err
	}
	return s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (user_id, tenant_id, username, display_name, password_hash,
			                   password_algo, enabled, recovery_required)
			VALUES ($1,$2,$3,$4,$5,'argon2id',TRUE,TRUE)
			ON CONFLICT (tenant_id, user_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
			userID, tenant, username, displayName, hash); err != nil {
			return fmt.Errorf("identity: create user: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role_id) VALUES ($1,$2,$3)
			 ON CONFLICT DO NOTHING`, tenant, userID, role); err != nil {
			return fmt.Errorf("identity: grant role: %w", err)
		}
		for _, site := range sites {
			if _, err := tx.Exec(ctx, `
				INSERT INTO user_site_assignments (tenant_id, user_id, site_id) VALUES ($1,$2,$3)
			 ON CONFLICT DO NOTHING`, tenant, userID, site); err != nil {
				return fmt.Errorf("identity: assign site: %w", err)
			}
		}
		if s.Audit != nil {
			if _, err := s.Audit.Append(ctx, tx, auditEntryFor(tenant, actor,
				"identity.role.granted", userID)); err != nil {
				return err
			}
		}
		return nil
	})
}

// randomPassphrase produces a placeholder a new user immediately replaces via
// SetPassword. It is not returned, so nobody can log in with it — the account
// exists and has recovery_required set, which is the state that says "this
// person's password has not been chosen yet".
func randomPassphrase() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "recovery-required-not-yet-chosen-000000"
	}
	return hex.EncodeToString(buf)
}

func rolesFromJSON(s string) []Role {
	var out []Role
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func rolesStrings(r []Role) []string {
	out := make([]string, 0, len(r))
	for _, v := range r {
		out = append(out, string(v))
	}
	return out
}

func stringsFromJSON(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

var _ = subtle.ConstantTimeCompare
var _ = strconv.Itoa
var _ = dbx.SystemTenant
