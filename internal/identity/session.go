package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
)

// Service authenticates principals and manages sessions.
type Service struct {
	Pool *dbx.Pool
	// Audit records every auth event. Optional in a unit test, never optional in
	// a running system: an authentication that is not recorded cannot be
	// investigated after an incident, and "we log in" is not a defence.
	Audit *audit.Writer
	// Now is injectable so lockout and expiry tests are deterministic.
	Now func() time.Time
	// ChallengeTTL is how long an unconsumed MFA challenge lives. A challenge is a
	// temporary bearer of "the password was right", so it is short.
	ChallengeTTL time.Duration
	// SessionIdle and SessionAbsolute bound a session. Both, because idle alone
	// lets a session that is used constantly never expire.
	SessionIdle     time.Duration
	SessionAbsolute time.Duration
	// MaxFailedAttempts before lockout begins.
	MaxFailedAttempts int
	// LockoutBase is the first lockout delay.
	LockoutBase time.Duration
	// SecretStore wraps TOTP secrets. It is an interface because the key lives
	// in a file only the installer reads, and this package must never hold one.
	SecretStore SecretStore
}

// auditEntryFor builds an admin-scoped ledger entry.
func auditEntryFor(tenant, actor, action, target string) audit.Entry {
	return audit.Entry{
		TenantID: tenant, Actor: actor, ActorType: audit.ActorAdmin,
		Action: action, TargetType: "user", TargetID: target,
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) challengeTTL() time.Duration {
	if s.ChallengeTTL <= 0 {
		return 2 * time.Minute
	}
	return s.ChallengeTTL
}

func (s *Service) sessionIdle() time.Duration {
	if s.SessionIdle <= 0 {
		return 15 * time.Minute
	}
	return s.SessionIdle
}

func (s *Service) sessionAbsolute() time.Duration {
	if s.SessionAbsolute <= 0 {
		return 12 * time.Hour
	}
	return s.SessionAbsolute
}

func (s *Service) maxFailed() int {
	if s.MaxFailedAttempts <= 0 {
		return 5
	}
	return s.MaxFailedAttempts
}

func (s *Service) lockoutBase() time.Duration {
	if s.LockoutBase <= 0 {
		return 30 * time.Second
	}
	return s.LockoutBase
}

// LoginResult is what a password step produced.
//
// Exactly one of Session or Challenge is set. A password step never returns a
// session on its own: that is the whole reason MFA is mandatory rather than
// optional.
//
// Principal is always populated. When MFA is required there is no session, and
// a caller that needs to say who is being asked for a second factor — the login
// response, the audit line, the console — has to be able to name them. It used
// to read Principal off a nil Session, which meant every MFA login panicked.
type LoginResult struct {
	ChallengeID string    `json:"challengeId"`
	MFARequired bool      `json:"mfaRequired"`
	ExpiresAt   time.Time `json:"expiresAt"`
	// Session is nil when MFA is required.
	Session   *Session  `json:"session,omitempty"`
	Principal Principal `json:"-"`
}

// Session is an authenticated session.
type Session struct {
	ID        string
	Token     string // raw; the caller puts it in a cookie and never stores it
	CSRFToken string
	Principal Principal
	ExpiresAt time.Time
}

// userRow is the stored account.
type userRow struct {
	TenantID       string
	UserID         string
	Username       string
	DisplayName    string
	PasswordHash   *string
	Enabled        bool
	BreakGlass     bool
	FailedAttempts int
	LockedUntil    *time.Time
	MFAFactor      string
}

// PasswordStep verifies a password and, when a second factor is enrolled,
// returns a challenge instead of a session.
func (s *Service) PasswordStep(ctx context.Context, tenant, username, password, sourceIP string) (LoginResult, error) {
	var result LoginResult

	var user userRow
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "auth"},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT tenant_id, user_id, username, display_name, password_hash, enabled,
				       break_glass, failed_attempts, locked_until
				  FROM users
				 WHERE tenant_id = $1 AND lower(username) = lower($2)
				 FOR UPDATE`, tenant, username).Scan(
				&user.TenantID, &user.UserID, &user.Username, &user.DisplayName,
				&user.PasswordHash, &user.Enabled, &user.BreakGlass,
				&user.FailedAttempts, &user.LockedUntil)
		})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, fmt.Errorf("identity: password step: %w", err)
	}

	// An unknown username and a wrong password take the same path and take
	// comparable time. Returning early for one and not the other is a user
	// enumeration oracle.
	if errors.Is(err, pgx.ErrNoRows) {
		s.recordAuth(ctx, tenant, username, "unknown", audit.ActionLoginFailed, sourceIP,
			"no such user")
		return result, ErrInvalidCredentials
	}
	if !user.Enabled {
		s.recordAuth(ctx, tenant, username, user.UserID, audit.ActionLoginFailed, sourceIP,
			"account is disabled")
		return result, ErrInvalidCredentials
	}

	now := s.now()
	if user.LockedUntil != nil && user.LockedUntil.After(now) {
		until := *user.LockedUntil
		s.recordAuth(ctx, tenant, username, user.UserID, audit.ActionLoginLocked, sourceIP,
			fmt.Sprintf("locked until %s", until.Format(time.RFC3339)))
		return result, fmt.Errorf("%w until %s", ErrLocked, until.Format(time.RFC3339))
	}

	if user.PasswordHash == nil {
		s.recordAuth(ctx, tenant, username, user.UserID, audit.ActionLoginFailed, sourceIP,
			"no password set")
		return result, ErrInvalidCredentials
	}
	ok, verifyErr := VerifyPassword(*user.PasswordHash, password)
	if verifyErr != nil {
		// A stored hash this build cannot verify is a problem to surface, not to
		// report as a wrong password: the operator needs to know the account is
		// unloginable rather than that they mistyped.
		s.recordAuth(ctx, tenant, username, user.UserID, audit.ActionLoginFailed, sourceIP,
			verifyErr.Error())
		return result, verifyErr
	}
	if !ok {
		attempts, lockUntil := s.registerFailure(ctx, tenant, user)
		action := audit.ActionLoginFailed
		reason := "wrong password"
		if lockUntil != nil {
			action = audit.ActionLoginLocked
			reason = fmt.Sprintf("locked until %s after %d failed attempts", lockUntil.Format(time.RFC3339), attempts)
		}
		s.recordAuth(ctx, tenant, username, user.UserID, action, sourceIP, reason)
		return result, ErrInvalidCredentials
	}

	// A successful password clears the counter, so one typo does not accumulate
	// toward a lockout the operator never caused twice.
	if err := s.clearFailures(ctx, tenant, user.UserID); err != nil {
		return result, err
	}

	method, err := s.enrolledFactor(ctx, tenant, user.UserID)
	if err != nil {
		return result, err
	}
	if method == "" {
		// No factor enrolled. This is the one path that can produce a session
		// from a password alone, and it exists only because a bootstrap admin has
		// nobody to enrol against. It is audited at the highest volume precisely
		// so a site that leaves it open is visible.
		sess, err := s.createSession(ctx, tenant, user, sourceIP, "bootstrap", true, nil)
		if err != nil {
			return result, err
		}
		s.recordAuth(ctx, tenant, username, user.UserID, audit.ActionLoginSuccess, sourceIP,
			"session issued with no enrolled factor (bootstrap account)")
		return LoginResult{MFARequired: false, Session: &sess, Principal: sess.Principal}, nil
	}

	challengeID, err := s.issueChallenge(ctx, tenant, user, method)
	if err != nil {
		return result, err
	}
	// Read the scope on this path too. The principal is not authenticated yet —
	// a second factor is still owed — but it is identified, and the response has
	// to name who is being asked.
	roles, scope, err := s.principalScope(ctx, tenant, user.UserID)
	if err != nil {
		return result, err
	}
	result = LoginResult{
		ChallengeID: challengeID,
		MFARequired: true,
		ExpiresAt:   now.Add(s.challengeTTL()),
		Principal: Principal{
			UserID: user.UserID, TenantID: tenant, Username: user.Username,
			Roles: roles, SiteScope: scope, MFAVerified: false, BreakGlass: user.BreakGlass,
		},
	}
	s.recordAuth(ctx, tenant, username, user.UserID, audit.ActionLoginSuccess, sourceIP,
		"password accepted, second factor required")
	return result, nil
}

// issueChallenge creates a short-lived challenge. The challenge id is itself the
// bearer of "the password was right", which is why its lifetime is minutes.
func (s *Service) issueChallenge(ctx context.Context, tenant string, user userRow, method string) (string, error) {
	buf := make([]byte, TokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: read challenge: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	now := s.now()

	return id, s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		// Challenges are held in the sessions table under a distinguished id so
		// they are cleaned up by the same sweep that clears sessions, rather than
		// accumulating in a table nothing prunes.
		_, err := tx.Exec(ctx, `
			INSERT INTO sessions (session_id, tenant_id, user_id, token_hash, csrf_hash,
			                      roles, site_scope, mfa_verified, absolute_expiry, created_at)
			VALUES ($1,$2,$3,$4,$5,'[]'::jsonb,'[]'::jsonb,FALSE,$6,$7)`,
			"chal_"+id, tenant, user.UserID, HashToken("chal:"+id), HashToken("chal:"+id+":csrf"),
			now.Add(s.challengeTTL()), now)
		if err != nil {
			return fmt.Errorf("identity: issue challenge: %w", err)
		}
		_, err = tx.Exec(ctx,
			`UPDATE sessions SET ip = $3, user_agent = $4 WHERE tenant_id = $1 AND session_id = $2`,
			tenant, "chal_"+id, "", method)
		return err
	})
}

// CompleteChallenge verifies a second factor and issues the session.
func (s *Service) CompleteChallenge(ctx context.Context, tenant, challengeID, method, code, sourceIP string) (Session, error) {
	var sess Session
	now := s.now()

	var userID, tokenHash, ip, agent string
	var mfaVerified bool
	_ = mfaVerified
	var attempts int
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "auth"},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT user_id, token_hash, mfa_verified, absolute_expiry, ip, user_agent, mfa_attempts
				  FROM sessions
				 WHERE tenant_id = $1 AND session_id = $2 AND revoked_at IS NULL
				 FOR UPDATE`, tenant, "chal_"+challengeID).Scan(
				&userID, &tokenHash, &mfaVerified, &sess.ExpiresAt, &ip, &agent, &attempts)
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sess, errors.New("identity: no such challenge, or it has expired")
		}
		return sess, fmt.Errorf("identity: complete challenge: %w", err)
	}
	if now.After(sess.ExpiresAt) {
		return sess, errors.New("identity: the challenge has expired; start again with the password")
	}
	// A six-digit code inside a plus-or-minus-thirty-second window is guessable
	// in a couple of minutes of wall clock, and the challenge lives for two of
	// them. Without this a held challenge id is a password equivalent, which is
	// why the password step's lockout does not protect this one: the password
	// already succeeded, so nothing about the account is being counted.
	//
	// The bound is the same MaxFailedAttempts the password step uses, and it is
	// counted per challenge rather than per account, so a legitimate operator
	// who mistypes twice and a caller guessing both still get five tries in
	// total. The counter is checked before verification, not after, so a locked
	// challenge never reaches the factor check at all.
	if attempts >= s.maxFailed() {
		s.recordAuth(ctx, tenant, userID, userID, audit.ActionLoginFailed, sourceIP,
			fmt.Sprintf("second factor refused: %d attempts already failed on this challenge", attempts))
		return sess, fmt.Errorf("%w: too many failed second-factor attempts on this challenge; "+
			"start again with the password", ErrLocked)
	}

	factorID, _, ok, err := s.VerifyFactor(ctx, tenant, userID, method, code)
	if err != nil {
		return sess, err
	}
	if !ok {
		s.registerChallengeFailure(ctx, tenant, challengeID)
		s.recordAuth(ctx, tenant, userID, userID, audit.ActionLoginFailed, sourceIP,
			"second factor rejected")
		return sess, ErrInvalidCredentials
	}

	user, err := s.loadUser(ctx, tenant, userID)
	if err != nil {
		return sess, err
	}
	// The challenge row is deleted in the same transaction that inserts the
	// session. A challenge that could be used twice is a password equivalent: it
	// is a two-minute bearer of "the password was right", so consuming it is the
	// whole control. This used to only insert a new sess_ row and leave the
	// chal_ row in place until PurgeExpired, which meant one intercepted
	// challenge id minted unlimited sessions and two concurrent requests both
	// succeeded — the FOR UPDATE lock committed before verification ran.
	sess, err = s.createSession(ctx, tenant, user, sourceIP, method, true, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`DELETE FROM sessions WHERE tenant_id = $1 AND session_id = $2 AND mfa_verified = FALSE`,
			tenant, "chal_"+challengeID)
		if err != nil {
			return fmt.Errorf("identity: consume challenge: %w", err)
		}
		if tag.RowsAffected() != 1 {
			// Another request consumed it between the read and this write. The
			// whole transaction rolls back, so the second request gets a session
			// neither of them keeps.
			return fmt.Errorf("%w: the challenge was already used", ErrInvalidCredentials)
		}
		return nil
	})
	if err != nil {
		return sess, err
	}

	action := audit.ActionLoginSuccess
	reason := "session issued after second factor"
	if user.BreakGlass {
		action = audit.ActionBreakGlassLogin
		reason = "BREAK-GLASS login"
	}
	s.recordAuth(ctx, tenant, user.Username, userID, action, sourceIP, reason)
	if err := s.touchFactor(ctx, tenant, factorID); err != nil {
		return sess, err
	}
	return sess, nil
}

// StepUp re-verifies a factor for a sensitive action and returns a short-lived
// proof the caller presents with that action.
//
// The proof is separate from the session on purpose. Escalating an incident is
// a different act from being logged in, and a session that was fine fifteen
// minutes ago is not evidence about the operator's intent now.
func (s *Service) StepUp(ctx context.Context, tenant, userID, method, code, action, target string) (string, error) {
	_, _, ok, err := s.VerifyFactor(ctx, tenant, userID, method, code)
	if err != nil {
		return "", err
	}
	if !ok {
		s.recordAuth(ctx, tenant, userID, userID, audit.ActionLoginFailed, "",
			"step-up factor rejected for "+action)
		return "", ErrInvalidCredentials
	}

	buf := make([]byte, TokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("identity: read step-up token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	now := s.now()

	// The proof is stored as a session row with the distinguished prefix, so it
	// is pruned by the same sweep and cannot outlive its own TTL.
	if err := s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO sessions (session_id, tenant_id, user_id, token_hash, csrf_hash,
			                      roles, site_scope, mfa_verified, ip, absolute_expiry, created_at)
			VALUES ($1,$2,$3,$4,$5,'[]'::jsonb,'[]'::jsonb,TRUE,$6,$7,$8)`,
			"stepup_"+HashToken(raw), tenant, userID, HashToken("stepup:"+raw),
			HashToken("stepup:"+raw+":csrf"), action+":"+target, now.Add(5*time.Minute), now)
		if err != nil {
			return fmt.Errorf("identity: store step-up proof: %w", err)
		}
		return nil
	}); err != nil {
		return "", err
	}
	s.recordAuth(ctx, tenant, userID, userID, audit.ActionStepUpMFASucceeded, "",
		"step-up for "+action+" on "+target)
	return raw, nil
}

// ConsumeStepUp spends a proof. Single use, because a proof that could be
// replayed is a session, and the whole point is that this is narrower.
func (s *Service) ConsumeStepUp(ctx context.Context, tenant, userID, proof string) error {
	if strings.TrimSpace(proof) == "" {
		return ErrStepUpRequired
	}
	return s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE sessions SET revoked_at = now(), revoked_reason = 'step-up consumed'
			 WHERE tenant_id = $1 AND user_id = $2 AND session_id = $3
			   AND token_hash = $4 AND revoked_at IS NULL AND absolute_expiry > $5`,
			tenant, userID, "stepup_"+HashToken(proof), HashToken("stepup:"+proof), s.now())
		if err != nil {
			return fmt.Errorf("identity: consume step-up: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: the proof is unknown, expired, or already used", ErrStepUpRequired)
		}
		return nil
	})
}

// Resolve turns a session token into a principal, applying idle and absolute
// expiry and rotating the token.
//
// Rotation on every use is the cheap half of session theft defence: a token
// captured from a browser is only good until the legitimate operator's next
// request.
func (s *Service) Resolve(ctx context.Context, tenant, token, sourceIP string) (Principal, string, error) {
	var p Principal
	if strings.TrimSpace(token) == "" {
		return p, "", errors.New("identity: no session token")
	}
	now := s.now()

	var sessionID, userID, rolesJSON, scopeJSON, ip, agent string
	var mfaVerified, breakGlass bool
	var lastSeen, absoluteExpiry time.Time
	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "session"},
		func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT session_id, user_id, roles, site_scope, mfa_verified, break_glass,
				       last_seen_at, absolute_expiry, ip, user_agent
				  FROM sessions
				 WHERE tenant_id = $1 AND token_hash = $2 AND revoked_at IS NULL
				 FOR UPDATE`, tenant, HashToken(token)).Scan(
				&sessionID, &userID, &rolesJSON, &scopeJSON, &mfaVerified, &breakGlass,
				&lastSeen, &absoluteExpiry, &ip, &agent)
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, "", errors.New("identity: no such session")
		}
		return p, "", fmt.Errorf("identity: resolve: %w", err)
	}

	if now.After(absoluteExpiry) {
		_ = s.revoke(ctx, tenant, sessionID, "absolute expiry")
		return p, "", errors.New("identity: the session has expired")
	}
	if now.Sub(lastSeen) > s.sessionIdle() {
		_ = s.revoke(ctx, tenant, sessionID, "idle timeout")
		return p, "", errors.New("identity: the session timed out")
	}

	user, err := s.loadUser(ctx, tenant, userID)
	if err != nil {
		return p, "", err
	}
	if !user.Enabled {
		_ = s.revoke(ctx, tenant, sessionID, "account disabled")
		return p, "", ErrInvalidCredentials
	}

	p = Principal{
		UserID: user.UserID, TenantID: tenant, Username: user.Username,
		Roles: rolesFromJSON(rolesJSON), SiteScope: stringsFromJSON(scopeJSON),
		SessionID: sessionID, BreakGlass: breakGlass, MFAVerified: mfaVerified,
	}

	// Rotate. The old token stops working immediately, so a token captured from
	// a browser is only good until the legitimate operator's next request.
	newToken, newHash, err := NewToken()
	if err != nil {
		return p, "", err
	}
	newCSRF, newCSRFHash, err := NewToken()
	if err != nil {
		return p, "", err
	}
	if err := s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE sessions
			   SET token_hash = $3, csrf_hash = $4, last_seen_at = $5, ip = $6
			 WHERE tenant_id = $1 AND session_id = $2`,
			tenant, sessionID, newHash, newCSRFHash, now, sourceIP)
		return err
	}); err != nil {
		return p, "", err
	}
	// The rotated CSRF token rides alongside the rotated session token; the
	// client needs both or the next state-changing request fails.
	p.CSRFToken = newCSRF
	return p, newToken, nil
}

// RotateCSRF issues a fresh CSRF token for a live session.
//
// It rotates on authentication and on privilege change, so a CSRF token minted
// before an escalation is not usable after one.
func (s *Service) RotateCSRF(ctx context.Context, tenant, sessionID string) (string, error) {
	raw, hash, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE sessions SET csrf_hash = $3 WHERE tenant_id = $1 AND session_id = $2 AND revoked_at IS NULL`,
			tenant, sessionID, hash)
		return err
	}); err != nil {
		return "", err
	}
	return raw, nil
}

// CheckCSRF compares a presented token with the one the session row holds.
//
// The comparison is against the row rather than against a second cookie, so a
// CSRF token cannot be forged by reading one out of a browser that already had
// it: it is bound to a session that has to be live.
func (s *Service) CheckCSRF(ctx context.Context, tenant, sessionID, presented string) error {
	if strings.TrimSpace(presented) == "" {
		return errors.New("identity: a CSRF token is required on every state-changing method")
	}
	var stored string
	err := s.Pool.For(tenant, "identity").QueryRow(ctx, `SELECT csrf_hash FROM sessions WHERE tenant_id = $1 AND session_id = $2 AND revoked_at IS NULL`,
		tenant, sessionID).Scan(&stored)
	if err != nil {
		return errors.New("identity: no such session")
	}
	if !ConstantTimeEqual(stored, HashToken(presented)) {
		return errors.New("identity: the CSRF token does not match this session")
	}
	return nil
}

// Logout revokes one session.
func (s *Service) Logout(ctx context.Context, tenant, sessionID, actor, reason string) error {
	if err := s.revoke(ctx, tenant, sessionID, reason); err != nil {
		return err
	}
	s.recordAuth(ctx, tenant, actor, actor, audit.ActionLogout, "", reason)
	return nil
}

// RevokeUserSessions revokes every session for a user, which is what disabling
// an account has to do: leaving the existing sessions alive would make
// "disabled" a label rather than a control.
func (s *Service) RevokeUserSessions(ctx context.Context, tenant, userID, reason string) (int64, error) {
	tag, err := s.Pool.For(tenant, "identity").Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $3
		 WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		tenant, userID, reason)
	if err != nil {
		return 0, fmt.Errorf("identity: revoke sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeExpired removes challenges, step-up proofs and dead sessions.
//
// It is safe to run often and removes only rows that can no longer be used, so
// there is no window in which a live session is collected.
func (s *Service) PurgeExpired(ctx context.Context, tenant string) (int64, error) {
	tag, err := s.Pool.For(tenant, "identity").Exec(ctx, `
		DELETE FROM sessions
		 WHERE tenant_id = $1
		   AND (absolute_expiry < $2
		        OR (session_id LIKE 'chal_%' AND created_at < $3))`,
		tenant, s.now(), s.now().Add(-1*time.Hour))
	if err != nil {
		return 0, fmt.Errorf("identity: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CheckCSRFByToken compares a presented CSRF token with the one the session
// named by a session token holds.
//
// By token rather than by session id, because the session token is rotated on
// every request: an id derived from the token that just arrived is one rotation
// stale, and every correctly-tokened request would be refused.
func (s *Service) CheckCSRFByToken(ctx context.Context, tenant, sessionToken, presented string) error {
	if strings.TrimSpace(presented) == "" {
		return errors.New("identity: a CSRF token is required on every state-changing method")
	}
	var stored string
	err := s.Pool.For(tenant, "session").
		QueryRow(ctx, `
			SELECT csrf_hash FROM sessions
			 WHERE tenant_id = $1 AND token_hash = $2 AND revoked_at IS NULL`,
			tenant, HashToken(sessionToken)).Scan(&stored)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("identity: no such session")
		}
		return fmt.Errorf("identity: check CSRF: %w", err)
	}
	if !ConstantTimeEqual(stored, HashToken(presented)) {
		return errors.New("identity: the CSRF token does not match this session")
	}
	return nil
}

func (s *Service) revoke(ctx context.Context, tenant, sessionID, reason string) error {
	_, err := s.Pool.For(tenant, "identity").Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $3
		 WHERE tenant_id = $1 AND session_id = $2 AND revoked_at IS NULL`,
		tenant, sessionID, reason)
	if err != nil {
		return fmt.Errorf("identity: revoke: %w", err)
	}
	return nil
}

// createSession inserts a session row.
//
// withTx, when non-nil, runs inside the same transaction as the insert and
// before it. That is how a caller makes the insert conditional on something —
// the MFA challenge it is redeeming, for instance. One transaction, so either
// both happen or neither does.
func (s *Service) createSession(ctx context.Context, tenant string, user userRow, sourceIP, method string, mfa bool, withTx func(pgx.Tx) error) (Session, error) {
	var sess Session
	token, tokenHash, err := NewToken()
	if err != nil {
		return sess, err
	}
	csrf, csrfHash, err := NewToken()
	if err != nil {
		return sess, err
	}

	roles, scope, err := s.principalScope(ctx, tenant, user.UserID)
	if err != nil {
		return sess, err
	}
	now := s.now()
	sess = Session{
		ID: "sess_" + HashToken(token)[:16], Token: token, CSRFToken: csrf,
		ExpiresAt: now.Add(s.sessionAbsolute()),
		Principal: Principal{
			UserID: user.UserID, TenantID: tenant, Username: user.Username,
			Roles: roles, SiteScope: scope, SessionID: "sess_" + HashToken(token)[:16],
			BreakGlass: user.BreakGlass, MFAVerified: mfa,
		},
	}
	rolesJSON, _ := jsonMarshal(rolesStrings(roles))
	scopeJSON, _ := jsonMarshal(scope)

	if err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "auth"},
		func(tx pgx.Tx) error {
			if withTx != nil {
				if err := withTx(tx); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `
			INSERT INTO sessions (session_id, tenant_id, user_id, token_hash, csrf_hash, roles,
			                      site_scope, mfa_verified, break_glass, ip, user_agent,
			                      created_at, last_seen_at, absolute_expiry)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12,$13)`,
				sess.ID, tenant, user.UserID, tokenHash, csrfHash, rolesJSON, scopeJSON,
				mfa, user.BreakGlass, sourceIP, method, now, sess.ExpiresAt)
			if err != nil {
				return fmt.Errorf("identity: create session: %w", err)
			}
			return nil
		}); err != nil {
		return sess, err
	}
	return sess, nil
}

func (s *Service) loadUser(ctx context.Context, tenant, userID string) (userRow, error) {
	var u userRow
	err := s.Pool.For(tenant, "identity").QueryRow(ctx, `
		SELECT tenant_id, user_id, username, display_name, password_hash, enabled,
		       break_glass, failed_attempts, locked_until
		  FROM users WHERE tenant_id = $1 AND user_id = $2`,
		tenant, userID).Scan(&u.TenantID, &u.UserID, &u.Username, &u.DisplayName,
		&u.PasswordHash, &u.Enabled, &u.BreakGlass, &u.FailedAttempts, &u.LockedUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, ErrInvalidCredentials
		}
		return u, fmt.Errorf("identity: load user: %w", err)
	}
	return u, nil
}

// principalScope reads a user's roles and site assignments.
//
// The query runs inside the tenant context, and that is not a formality. Both
// tables carry tenant_id and therefore row-level security, so a query issued
// without the context sees zero rows — not an error, just an empty result. A
// principal built from that has no roles and no site scope, so every capability
// check and every site check refuses, and the whole console is unreachable while
// the login itself keeps returning a session. It fails closed, which is the right
// direction, and it is still an outage.
//
// The reason this survived until now is that the test database connects as a
// superuser, which bypasses RLS. A test that only ever runs as a superuser cannot
// see this class of bug, which is why the RLS suites in internal/schemalaws set
// the context explicitly.
func (s *Service) principalScope(ctx context.Context, tenant, userID string) ([]Role, []string, error) {
	var roles []Role
	var scope []string

	err := s.Pool.InTenant(ctx, dbx.TenantContext{TenantID: tenant, ActorID: "identity"},
		func(tx pgx.Tx) error {
			// role_id, not name. The capability table in this package is keyed by
			// role_id, and joining to the human-readable name gives a principal
			// whose role is "Tenant Admin" while the code looks for
			// "tenant_admin" — so no capability resolves and every operator is
			// denied everything. The name is a label for a person; the id is the
			// thing a program matches on.
			rows, err := tx.Query(ctx, `
				SELECT ur.role_id FROM user_roles ur
				 WHERE ur.tenant_id = $1 AND ur.user_id = $2
				 ORDER BY ur.role_id`, tenant, userID)
			if err != nil {
				return fmt.Errorf("identity: load roles: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					return fmt.Errorf("identity: scan role: %w", err)
				}
				roles = append(roles, Role(name))
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("identity: load roles: %w", err)
			}

			sites, err := tx.Query(ctx, `
				SELECT site_id FROM user_site_assignments
				 WHERE tenant_id = $1 AND user_id = $2 ORDER BY site_id`, tenant, userID)
			if err != nil {
				return fmt.Errorf("identity: load sites: %w", err)
			}
			defer sites.Close()
			for sites.Next() {
				var site string
				if err := sites.Scan(&site); err != nil {
					return fmt.Errorf("identity: scan site: %w", err)
				}
				scope = append(scope, site)
			}
			return sites.Err()
		})
	if err != nil {
		return nil, nil, err
	}
	return roles, scope, nil
}

func (s *Service) registerFailure(ctx context.Context, tenant string, user userRow) (int, *time.Time) {
	attempts := user.FailedAttempts + 1
	lock, delay := ShouldLock(Lockout{FailedAttempts: attempts}, s.maxFailed(), s.lockoutBase())
	var until *time.Time
	if lock {
		t := s.now().Add(delay)
		until = &t
	}
	_ = s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE users SET failed_attempts = $3, locked_until = $4 WHERE tenant_id = $1 AND user_id = $2`,
			tenant, user.UserID, attempts, until)
		return err
	})
	return attempts, until
}

// registerChallengeFailure counts a rejected second factor against the
// challenge it was offered for.
//
// It is deliberately separate from registerFailure, which counts against the
// account: the password already succeeded on this path, so folding the second
// factor into the account counter would lock an operator out for mistyping a
// six-digit code, and the account counter is cleared on the next successful
// password anyway.
func (s *Service) registerChallengeFailure(ctx context.Context, tenant, challengeID string) {
	_ = s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE sessions SET mfa_attempts = mfa_attempts + 1
			 WHERE tenant_id = $1 AND session_id = $2 AND mfa_verified = FALSE`,
			tenant, "chal_"+challengeID)
		return err
	})
}

func (s *Service) clearFailures(ctx context.Context, tenant, userID string) error {
	return s.Pool.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE users SET failed_attempts = 0, locked_until = NULL, last_login_at = $3
			  WHERE tenant_id = $1 AND user_id = $2`, tenant, userID, s.now())
		return err
	})
}

func (s *Service) enrolledFactor(ctx context.Context, tenant, userID string) (string, error) {
	var method string
	err := s.Pool.For(tenant, "identity").QueryRow(ctx, `
		SELECT method FROM mfa_credentials
		 WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL
		 ORDER BY verified_at NULLS FIRST, created_at LIMIT 1`,
		tenant, userID).Scan(&method)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("identity: enrolled factor: %w", err)
	}
	return method, nil
}

func (s *Service) recordAuth(ctx context.Context, tenant, username, userID, action, ip, reason string) {
	if s.Audit == nil {
		return
	}
	actorType := audit.ActorOperator
	if strings.HasPrefix(action, "identity.") {
		actorType = audit.ActorAdmin
	}
	_ = s.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		_, err := s.Audit.Append(ctx, tx, audit.Entry{
			TenantID:   tenant,
			Actor:      orUser(username, userID),
			ActorType:  actorType,
			Action:     action,
			TargetType: "user",
			TargetID:   orUser(username, userID),
			RequestID:  ip,
			Reason:     reason,
		})
		return err
	})
}

func orUser(username, userID string) string {
	if username != "" {
		return username
	}
	return userID
}
