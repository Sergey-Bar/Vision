// Package identity is authentication, sessions and authorization.
//
// Three rules shape everything here.
//
// MFA is mandatory. There is no code path that mints a session from a password
// alone, which is why `sessions.mfa_verified` exists and why a password step
// returns a challenge rather than a cookie.
//
// A break-glass account is a person, not a backdoor. It needs a real password,
// it raises a high-priority audit event on every use, and it cannot be created
// without an audit row - so its existence is a fact somebody knows rather than a
// possibility somebody has.
//
// Passwords are Argon2id with parameters held in the code rather than in the
// hash. That is a deliberate trade: it means an attacker who can raise the cost
// factor cannot, and it means the parameters a site runs with are a property of
// the build that was reviewed rather than of a database row.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Argon2id parameters. These are the build's, not the row's, so a site cannot
// quietly weaken its own password cost factor by editing a column.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// TokenLength is the entropy of a session token, in bytes, before base64. 32
// bytes is not negotiable downward for a token that grants console access.
const TokenLength = 32

// ErrInvalidCredentials is returned for a bad username or a bad password, and is
// deliberately the same error for both. Distinguishing them tells an attacker
// which usernames exist.
var ErrInvalidCredentials = errors.New("identity: invalid username or password")

// ErrLocked is returned when the account is inside its lockout window.
var ErrLocked = errors.New("identity: account is locked")

// ErrMFARequired is returned when a second factor is needed. It is a normal
// outcome, not a failure, and the API surfaces it as 200 with mfaRequired.
var ErrMFARequired = errors.New("identity: a second factor is required")

// ErrStepUpRequired is returned when a sensitive action needs a fresh factor
// rather than a live session.
var ErrStepUpRequired = errors.New("identity: a step-up verification is required")

// HashPassword produces an Argon2id hash in the standard encoded form.
func HashPassword(password string) (string, error) {
	if len(password) < 12 {
		// A length floor, not a composition rule. Composition rules push people
		// toward predictable substitutions; a length floor does not.
		return "", errors.New("identity: a passphrase of at least 12 characters is required")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity: read salt: %w", err)
	}
	hash := argon2id([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2idVersion, argonMemory, argonTime, argonThreads,
		b64(salt), b64(hash)), nil
}

// VerifyPassword checks a password against an encoded hash in constant time.
//
// The parameters are read from the hash, not from the constants above, so a hash
// written by an older build still verifies. What is refused is a hash claiming
// parameters weaker than the build's floor: accepting one would let a row in the
// database decide the site's password cost.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("identity: unrecognised password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("identity: unparseable hash version: %w", err)
	}
	var memory uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false, fmt.Errorf("identity: unparseable hash parameters: %w", err)
	}
	if version != argon2idVersion {
		return false, fmt.Errorf("identity: password hash version %d, this build writes %d",
			version, argon2idVersion)
	}
	if memory < argonMemory || timeCost < argonTime || threads < argonThreads {
		return false, fmt.Errorf(
			"identity: stored password hash uses weaker parameters (m=%d t=%d p=%d) than this "+
				"build's floor (m=%d t=%d p=%d). A row in the database does not get to choose the "+
				"site's password cost", memory, timeCost, threads, argonMemory, argonTime, argonThreads)
	}

	salt, err := unb64(parts[4])
	if err != nil {
		return false, fmt.Errorf("identity: decode salt: %w", err)
	}
	want, err := unb64(parts[5])
	if err != nil {
		return false, fmt.Errorf("identity: decode hash: %w", err)
	}
	// The stored hash length is argon2id's key length. This build always writes
	// argon2.KeyLength, so a row that says otherwise is a row this build did not
	// write, and a hash of several gigabytes would otherwise be truncated to a
	// 32-bit length and verified against a different value than the one stored.
	// Refusing it is the same answer the constant-time compare would give, and it
	// is reached without allocating.
	if len(want) != int(argonKeyLen) {
		return false, fmt.Errorf("identity: the stored hash is %d bytes, this build writes %d. "+
			"A row in the database does not get to choose how much of it is compared",
			len(want), argonKeyLen)
	}
	// argonKeyLen directly: the length was just checked against it, so the
	// conversion is a widening of a constant, not a narrowing of a value.
	got := argon2id([]byte(password), salt, timeCost, memory, threads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NewToken produces a session token and its storage hash.
//
// The raw token goes to the operator's cookie and is never stored. What is
// stored is a SHA-256 of it, so a database dump does not yield sessions. The
// comparison is constant-time for the same reason.
//
// It is `RawURLEncoding`, not the `b64` this file uses everywhere else, because
// this token is rendered into an HTML `value="..."` attribute and html/template
// escapes `value` as a URL context: a `+` from RawStdEncoding becomes `&#43;`,
// the browser decodes the entity before it submits, and the comparison against
// the raw cookie value fails for every token that happened to contain one. The
// URL alphabet — `A-Za-z0-9-_` — needs no escaping at all.
//
// `b64` is deliberately NOT changed: it encodes password salt and hash, which
// are stored, and a token alphabet is not worth a stored-format migration.
func NewToken() (raw string, hash string, err error) {
	buf := make([]byte, TokenLength)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("identity: read token: %w", err)
	}
	raw = tokenB64(buf)
	return raw, HashToken(raw), nil
}

// tokenB64 is the URL-safe alphabet, for tokens that are rendered into a page.
func tokenB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// HashToken is what the sessions table stores.
func HashToken(raw string) string { return sha256Hex(raw) }

// ConstantTimeEqual compares two strings without leaking their contents through
// timing. Used everywhere a secret is compared.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Lockout describes an account's failed-login state.
type Lockout struct {
	FailedAttempts int
	LockedUntil    *time.Time
}

// ShouldLock reports whether a further failure locks the account, and for how
// long.
//
// The backoff is exponential and capped. Linear backoff on a distributed
// guessing attack means the attacker and the legitimate operator are treated the
// same; exponential means the attacker's rate falls off a cliff while the
// operator's single mistake costs a minute.
func ShouldLock(lock Lockout, maxAttempts int, base time.Duration) (bool, time.Duration) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if base <= 0 {
		base = 30 * time.Second
	}
	if lock.FailedAttempts < maxAttempts {
		return false, 0
	}
	over := lock.FailedAttempts - maxAttempts
	// Doubling per extra failure past the threshold, capped at an hour.
	delay := base
	for i := 0; i < over && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	return true, delay
}

// Role is a named capability set. Roles are data, not code: adding a capability
// is a database row, and the code checks capabilities rather than role names, so
// a site's custom role works without a release.
type Role string

// The shipped roles. A site's custom role works without a release, because the
// service layer checks capabilities rather than role names.
const (
	RoleTenantAdmin     Role = "tenant_admin"
	RoleSecurityManager Role = "security_manager"
	RoleOperator        Role = "operator"
	RoleViewer          Role = "viewer"
	RoleService         Role = "service"
)

// Capability is one thing a principal may do. The set is closed and the check is
// exact, because a prefix match would make "incident.read" satisfy
// "incident.read_all" and an operator would escalate by accident.
type Capability string

// Capabilities. The closed set the service layer checks, matched exactly rather
// than by prefix: a prefix match would let incident.read satisfy
// incident.read_all, and an operator would escalate by accident.
//
// `evidence.export` is not in this list, and its absence is deliberate rather
// than an oversight. It was declared here and granted to tenant_admin and
// security_manager, and a tree-wide search for it found no route, no check and
// no audit action — a capability that grants nothing, in the one document an
// auditor reads to answer "who can take this site's evidence out of the
// building". That is a role table which lies, and it was removed rather than
// given a meaning: inventing an export route to justify a constant nobody asked
// for would add a route, an audit action, an export format and a new way for
// evidence bytes to leave a site, all to keep a name. `evidence.fetch` gates the
// route that releases bytes and `evidence.read` gates the metadata; those are
// the two things the product enforces, and the role table now says only those.
//
// The consequence, which is the control: a capability that nothing in the tree
// checks is a capability that grants nothing, and it is asserted in
// capability_coverage_test.go. Adding a constant to this block without adding
// the check that reads it fails that test.
const (
	CapIncidentRead        Capability = "incident.read"
	CapIncidentDisposition Capability = "incident.disposition"
	CapIncidentEscalate    Capability = "incident.escalate"
	CapEvidenceRead        Capability = "evidence.read"
	CapEvidenceFetch       Capability = "evidence.fetch"
	CapAuditRead           Capability = "audit.read"
	CapRulesRead           Capability = "rules.read"
	CapRulesWrite          Capability = "rules.write"
	CapRulesActivate       Capability = "rules.activate"
	CapIdentityManage      Capability = "identity.manage"
	CapSiteConfigure       Capability = "site.configure"
	CapDeadLetterRead      Capability = "dead_letter.read"
	CapDeadLetterReplay    Capability = "dead_letter.replay"
)

// AllCapabilities returns the closed set of capabilities this build declares.
//
// It exists so the set can be asserted rather than described. A capability is a
// promise that some route or check enforces it, and until the set was
// enumerable that promise could only be checked by reading every route by hand —
// which is exactly how evidence.export sat here, granted to two roles and
// enforced by nothing, without anybody noticing. Nothing grants a capability
// from this list to a principal; the list is for the coverage check and for an
// operator tool that wants to render the vocabulary.
func AllCapabilities() []Capability {
	return []Capability{
		CapIncidentRead, CapIncidentDisposition, CapIncidentEscalate,
		CapEvidenceRead, CapEvidenceFetch,
		CapAuditRead, CapRulesRead, CapRulesWrite, CapRulesActivate,
		CapIdentityManage, CapSiteConfigure,
		CapDeadLetterRead, CapDeadLetterReplay,
	}
}

// roleCapabilities is the shipped mapping. The database holds the same data; this
// is the fallback for a bootstrap and the reference the migration seeds from.
//
// The list is the second half of the coverage control: a capability declared
// above and granted to a role here is a capability somebody may rely on, and
// capability_coverage_test.go asserts that every one of them is read by
// something outside this file.
var roleCapabilities = map[Role][]Capability{
	RoleTenantAdmin: {
		CapIncidentRead, CapIncidentDisposition, CapIncidentEscalate,
		CapEvidenceRead, CapEvidenceFetch,
		CapAuditRead, CapRulesRead, CapRulesWrite, CapRulesActivate,
		CapIdentityManage, CapSiteConfigure,
		CapDeadLetterRead, CapDeadLetterReplay,
	},
	RoleSecurityManager: {
		CapIncidentRead, CapIncidentDisposition, CapIncidentEscalate,
		CapEvidenceRead, CapEvidenceFetch,
		CapAuditRead, CapRulesRead, CapDeadLetterRead, CapDeadLetterReplay,
	},
	RoleOperator: {
		CapIncidentRead, CapIncidentDisposition, CapEvidenceRead, CapEvidenceFetch,
	},
	RoleViewer: {
		CapIncidentRead, CapEvidenceRead,
	},
	RoleService: {
		// A service account holds integration capabilities, and nothing else. In
		// particular it never holds audit.read: an adapter that can read the
		// ledger can read the record of its own behaviour.
		"event.ingest", "device.sync", "evidence.write", "ai_observation.write",
	},
}

// CapabilitiesFor returns a role's capabilities.
func CapabilitiesFor(r Role) []Capability { return roleCapabilities[r] }

// Principal is an authenticated actor with its scope.
type Principal struct {
	// UserID is empty for a service account, which is why ActorID and UserID are
	// not interchangeable.
	UserID   string
	TenantID string
	Username string
	Roles    []Role
	// SiteScope is the sites this principal may see. A principal with no sites has
	// no site access at all: a misconfigured assignment denies rather than opens.
	SiteScope []string
	// SessionID and BreakGlass travel with the principal so every audited action
	// names the session it came from.
	SessionID   string
	BreakGlass  bool
	MFAVerified bool
	// CSRFToken is this session's current CSRF token, rotated alongside the
	// session token. It travels with the principal because every state-changing
	// request needs it, and reading it back out of the database on each one
	// would be a round trip for a value the session row has already decided.
	CSRFToken string
	// ServiceAccount marks a non-human principal, which has no password and no
	// MFA because it authenticates with a scoped token.
	ServiceAccount bool
}

// Can reports whether the principal holds a capability.
func (p Principal) Can(c Capability) bool {
	for _, r := range p.Roles {
		for _, have := range roleCapabilities[r] {
			if have == c {
				return true
			}
		}
	}
	return false
}

// CanSeeSite reports whether the principal's scope includes a site.
//
// An empty scope means no access. A principal that somehow has a role but no
// site assignment has been misconfigured, and the safe reading of a
// misconfiguration that grants access is that it does not.
func (p Principal) CanSeeSite(siteID string) bool {
	if siteID == "" {
		return false
	}
	for _, s := range p.SiteScope {
		if s == siteID {
			return true
		}
	}
	return false
}

// InSiteScope returns the principal's sites as a database array literal, for
// setting the tenant context the RLS layer reads.
func (p Principal) InSiteScope() string {
	if len(p.SiteScope) == 0 {
		return ""
	}
	return "{" + strings.Join(p.SiteScope, ",") + "}"
}

// Authorize is the check a handler calls.
//
// It returns the capability error rather than a bool, so a handler can return
// the refusal verbatim and an operator sees which capability they lack. "Access
// denied" with no reason is the worst possible answer at a site with a support
// contract.
func (p Principal) Authorize(c Capability) error {
	if !p.Can(c) {
		return fmt.Errorf("%w: %s does not hold %q", ErrUnauthorized, principalLabel(p), c)
	}
	if !p.MFAVerified && !p.ServiceAccount {
		return fmt.Errorf("%w: %s has no verified second factor", ErrUnauthorized, principalLabel(p))
	}
	return nil
}

// AuthorizeSite additionally checks the site, because holding a capability is
// not the same as being assigned to the place.
func (p Principal) AuthorizeSite(c Capability, siteID string) error {
	if err := p.Authorize(c); err != nil {
		return err
	}
	if !p.CanSeeSite(siteID) {
		return fmt.Errorf("%w: %s is not assigned to site %s", ErrUnauthorized, principalLabel(p), siteID)
	}
	return nil
}

// ErrUnauthorized is returned when a principal may not do something.
var ErrUnauthorized = errors.New("identity: not authorized")

func principalLabel(p Principal) string {
	if p.Username != "" {
		return p.Username
	}
	if p.ServiceAccount {
		return "service:" + p.UserID
	}
	return p.UserID
}
