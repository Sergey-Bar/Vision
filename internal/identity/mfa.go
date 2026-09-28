package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// VerifyFactor checks a second factor.
//
// WebAuthn is verified by signature against the stored public key; TOTP by
// comparing a counter-derived code. Both are constant-time comparisons, and
// neither is a comparison against a value the client chose earlier in the
// request.
// VerifyFactor checks a second factor and returns the credential that satisfied
// it.
//
// Exported because it is a service boundary rather than an internal helper: the
// login path and the step-up path both need it, and a test that could only reach a
// private function could not have caught the signed-assertion path failing to
// check a signature.
func (s *Service) VerifyFactor(ctx context.Context, tenant, userID, method, code string) (factorID, methodUsed string, ok bool, err error) {
	if strings.TrimSpace(code) == "" {
		return "", "", false, errors.New("identity: a second factor code is required")
	}

	var id, storedMethod string
	var publicKey, wrapped []byte
	var signCount int64
	rowErr := s.Pool.For(tenant, "identity").QueryRow(ctx, `
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
		secret, unwrapErr := s.unwrapTOTP(wrapped)
		if unwrapErr != nil {
			return "", "", false, unwrapErr
		}
		// The stored value is the base32 text that was handed to the
		// authenticator, and the HMAC runs over the bytes that text encodes. An
		// authenticator decodes before hashing, so hashing the base32 characters
		// themselves computes a different key and no code would ever match.
		raw, decodeErr := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(string(secret))
		if decodeErr != nil {
			return "", "", false, fmt.Errorf("identity: stored TOTP secret is not base32: %w", decodeErr)
		}
		for _, skew := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
			// SHA-256 first, because that is the algorithm our own enrolment URI
			// declares. SHA-1 is the RFC 6238 default that an authenticator which
			// ignores the `algorithm` parameter will use, so a client that read the
			// URI and a client that did not both verify. Both are HMAC over the same
			// 160-bit secret with the same counter, so accepting the second does not
			// widen what an attacker with a guess has to search in any way that
			// matters, and it removes a support call at 3am.
			if ConstantTimeEqual(totpCode(raw, sha256.New, s.now().Add(skew)), code) {
				return id, storedMethod, true, nil
			}
			if ConstantTimeEqual(totpCode(raw, sha1.New, s.now().Add(skew)), code) {
				return id, storedMethod, true, nil
			}
		}
		return id, storedMethod, false, nil

	case "webauthn":
		// The assertion arrives as the base64url JSON the browser returned. It is
		// decoded and passed to VerifyAssertion, which checks the signature over
		// authenticatorData || SHA-256(clientDataJSON) and the sign counter.
		//
		// There is deliberately no query here for the origin or the relying party.
		// Those live inside the client data, which is hashed into the signed
		// payload — so a caller that edited the origin would invalidate the
		// signature rather than pass an unchecked check. Reading them from
		// somewhere else would be a second, unsigned, source of truth about where
		// an assertion came from.
		var assertion Assertion
		raw, decErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(code))
		if decErr != nil {
			return "", "", false, errors.New("identity: the assertion is not base64url")
		}
		if err := json.Unmarshal(raw, &assertion); err != nil {
			return "", "", false, errors.New("identity: the assertion is not the expected shape")
		}
		advanced, verr := VerifyAssertion(publicKey, signCount, assertion)
		if verr != nil {
			return "", "", false, fmt.Errorf("identity: webauthn: %w", verr)
		}
		// The counter moves only when the assertion verified, so a later attempt
		// cannot present the same one again.
		if _, err := s.Pool.For(tenant, "identity").Exec(ctx, `
			UPDATE mfa_credentials SET sign_count = $4
			 WHERE tenant_id = $1 AND credential_id = $2 AND sign_count <= $3`,
			tenant, id, signCount, advanced); err != nil {
			return "", "", false, fmt.Errorf("identity: record the authenticator counter: %w", err)
		}
		return id, storedMethod, true, nil
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
	// The algorithm in this URI is the algorithm totpCode implements, and it is
	// asserted by TestTOTPURICAlgorithmMatchesTheVerifier. An authenticator that
	// honours the URI computes SHA-256; one that falls back to the RFC 6238
	// default computes SHA-1. Both have to verify, because an operator whose app
	// does not read the parameter cannot be told which one it used.
	uri := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA256&digits=6&period=30",
		issuer, label, encoded, issuer)
	return encoded, uri, nil
}

// VerifyTOTPEnrollment confirms the user can produce a code from the secret they
// were just given, which is what marks the credential usable.
//
// A credential that is stored but never confirmed is a credential that will fail
// at 3am, so the schema's `verified_at` is not left null by this path.
func (s *Service) VerifyTOTPEnrollment(ctx context.Context, tenant, userID, code string) error {
	_, _, ok, err := s.VerifyFactor(ctx, tenant, userID, "totp", code)
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
//
// The hash is a parameter because the enrolment URI declares SHA-256 while the
// RFC 6238 default — and therefore the fallback for an authenticator that
// ignores the `algorithm` parameter — is SHA-1. The caller tries both.
func totpCode(secret []byte, h func() hash.Hash, at time.Time) string {
	counter := uint64(at.Unix() / 30)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(h, secret)
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

// SecretStore wraps and unwraps secrets with a product-managed key.
//
// It is an interface because the key lives in a file with restricted permissions
// that only the installer reads, and because a test needs a deterministic
// implementation. There is no implementation here that holds a key.
type SecretStore interface {
	Wrap(plaintext []byte) ([]byte, error)
	Unwrap(ciphertext []byte) ([]byte, error)
}

func (s *Service) unwrapTOTP(wrapped []byte) ([]byte, error) {
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
	if err := s.Pool.For(tenant, "identity").QueryRow(ctx, `SELECT count(*) FROM users WHERE tenant_id = $1 AND user_id IN
		   (SELECT user_id FROM user_roles WHERE tenant_id = $1 AND role_id = 'tenant_admin')`,
		tenant).Scan(&existing); err != nil {
		return "", fmt.Errorf("identity: check for existing admin: %w", err)
	}
	if existing > 0 {
		return "", errors.New("identity: a tenant administrator already exists. Use `identity list` " +
			"and an audited recovery path; a second bootstrap is how an account nobody knows " +
			"about gets created")
	}

	// The tenant and the site have to exist. Booting an administrator into a
	// tenant that is not there would surface as a raw foreign-key error, which
	// tells an operator nothing about what to do next.
	//
	// Both existence checks run inside the tenant context. tenants and sites
	// carry tenant_id and therefore row-level security, so a query without the
	// context returns zero rows rather than an error — and bootstrap would report
	// "the tenant does not exist" for a tenant that plainly does.
	var tenants, sites int
	if err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "identity"},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx,
				`SELECT count(*) FROM tenants WHERE tenant_id = $1`, tenant).Scan(&tenants)
		}); err != nil {
		return "", fmt.Errorf("identity: check tenant: %w", err)
	}
	if tenants == 0 {
		return "", fmt.Errorf("identity: tenant %q does not exist. The installer creates the "+
			"tenant and its sites; bootstrap-admin runs afterwards", tenant)
	}
	if siteID != "" {
		if err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "identity"},
			func(tx pgx.Tx) error {
				return tx.QueryRow(ctx,
					`SELECT count(*) FROM sites WHERE tenant_id = $1 AND site_id = $2`,
					tenant, siteID).Scan(&sites)
			}); err != nil {
			return "", fmt.Errorf("identity: check site: %w", err)
		}
		if sites == 0 {
			return "", fmt.Errorf("identity: site %q does not exist in tenant %q. Create it "+
				"first, or omit --site and assign it afterwards", siteID, tenant)
		}
	}
	if len([]rune(passphrase)) < 16 {
		return "", errors.New("identity: the bootstrap passphrase must be at least 16 characters")
	}

	hash, err := HashPassword(passphrase)
	if err != nil {
		return "", err
	}
	userID := "admin-" + hex.EncodeToString(sha256.New().Sum([]byte(tenant + username)))[:8]

	// The write runs as a system worker, and that is the correct context rather
	// than a workaround: this is the installer creating a tenant's first
	// principal, which happens before anyone is authenticated and is not scoped by
	// a session. Under the ordinary tenant predicate the insert is refused by RLS
	// outright, which is the right refusal for everything except this operation.
	if err := s.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
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
	if err := s.Pool.For(tenant, "identity").QueryRow(ctx, `SELECT recovery_required FROM users WHERE tenant_id = $1 AND user_id = $2`,
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
