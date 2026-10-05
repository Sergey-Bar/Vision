// Package config is the daemon's configuration, read once at startup.
//
// The systemd unit already shipped an EnvironmentFile that nothing read:
// `EnvironmentFile=-/etc/security-platform/security-platform.env`, pointing at a
// file no binary opened. This package is the reader, and its whole job is to
// make that file mean something without inventing a format for it.
//
// # Secrets
//
// Nothing here is a secret, and nothing here can become one. The database
// password is the deliberate exception and it is *not* a field on Config: it
// goes straight from `SP_DATABASE_PASSWORD` to `dbx.Open` as a separate
// argument. `dbx.EnvOrDefault` says why in its own comment — an environment
// variable survives into child processes and diagnostic dumps, so a value read
// through the ordinary configuration path is a value that will eventually be
// logged. A field on Config is a field that somebody will print.
//
// # No silent defaults
//
// Anything whose absence would change behaviour is an error, not a default. An
// unset evidence root does not become `""` and it does not become the current
// directory; it is a refusal that names the variable, because a core that starts
// with no idea where evidence lives will accept evidence and put it somewhere
// it cannot find again.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"

	// `dispatch` owns the destinations file format and its validation, so it owns the
	// default path too. The dependency runs config -> dispatch rather than the reverse:
	// `config` is the package that decides WHERE things live, and a file format's
	// default location is part of that decision.
	//
	// It is not a cycle. `dispatch` imports `dbx`, `jobs`, `outbox` and `rules`, and
	// none of them import `config`.
	"github.com/Sergey-Bar/Ai-Captain/internal/dispatch"
)

// Config is the resolved configuration for one install.
//
// Single-tenant and single-site by construction. `Server.Tenant` in the API is
// already one field, and pretending otherwise here would only move the
// multi-site problem somewhere it has not been thought about.
type Config struct {
	// Listen is the console and API bind address.
	Listen string
	// TLSCertFile and TLSKeyFile. Both empty means TLS is terminated upstream,
	// which is why BehindProxy exists. One set without the other is a refusal.
	TLSCertFile string
	TLSKeyFile  string
	// BehindProxy says a TLS-terminating proxy fronts this process.
	BehindProxy bool

	// TrustedProxyCIDRs names the proxies whose X-Forwarded-For this process is
	// willing to believe, as `SP_TRUSTED_PROXY_CIDRS`: a comma-separated list of
	// CIDR blocks, whitespace around each entry ignored, e.g.
	// `10.0.0.0/8, 2001:db8::/32`. A bare address is accepted and is read as a
	// single-host block.
	//
	// It is required whenever BehindProxy is set, and there is no default. The
	// leftmost X-Forwarded-For entry is asserted by the caller and therefore
	// chosen by the caller, so a limiter or an audit row keyed on it is keyed on
	// whatever the caller said - which is why an unconfigured proxy list is a
	// refusal rather than a fallback to the leftmost entry.
	TrustedProxyCIDRs []*net.IPNet

	// SessionCookieSecure marks the session cookie Secure. It defaults to true
	// and is only false when an operator explicitly asks, because a console that
	// authenticates over plaintext hands a live session cookie to the network.
	// The only install where false is accepted is a loopback listener.
	SessionCookieSecure bool

	// Tenant, SiteID and IntegrationID scope everything the daemon writes.
	// Tenant is never read from a request body; it comes from here and from the
	// authenticated session, and those two are required to agree.
	Tenant        string
	SiteID        string
	IntegrationID string

	// Database is the connection policy. The password is not here.
	Database dbx.Config

	// EvidenceRoot is where evidence bytes live. Required.
	EvidenceRoot string

	// CheckpointDir and CheckpointKeyPath seal the audit ledger. An empty
	// CheckpointDir is allowed and disables sealing, which is itself visible in
	// the readiness report rather than silently doing nothing.
	// DispatchDestinationsPath is the file `internal/dispatch` reads its notification
	// destinations from. See the assignment in LoadFrom for why it is a path and not a
	// value on this struct.
	CheckpointDir     string
	CheckpointKeyPath string

	DispatchDestinationsPath string

	// PollInterval is how often the ingest loop asks the adapter for events.
	PollInterval time.Duration
	// ClaimBatchSize is how many inbox rows one consume pass takes.
	ClaimBatchSize int
	// LeaseDuration is how long a claimed row stays claimed before another
	// worker may take it. This is the bound on how long a killed process
	// stalls the pipeline.
	LeaseDuration time.Duration

	// AIEndpoint is the runtime.py base URL. Empty is a supported
	// configuration: the VLM worker then records a degraded observation with
	// reason `no_runtime`, which is honest and is what a site with no model
	// should see.
	AIEndpoint string
	// AIDeadline bounds one inference, client-side. runtime.py accepts
	// `deadlineSeconds` and never enforces it, so a hung runtime would hang the
	// worker holding a lease.
	AIDeadline time.Duration

	// LogLevel is one of debug, info, warn, error.
	LogLevel string

	// ShutdownGrace bounds how long in-flight requests get to finish.
	ShutdownGrace time.Duration
}

// Lookup resolves an environment variable. Load takes one so the tests are
// deterministic: a test that reads the real environment can only be wrong in
// the environment it is run in.
type Lookup func(string) string

// DefaultLookup reads the process environment.
func DefaultLookup(key string) string { return os.Getenv(key) }

// Defaults for the values where a default does not change behaviour.
const (
	DefaultListen        = "127.0.0.1:8443"
	DefaultPollInterval  = 5 * time.Second
	DefaultClaimBatch    = 64
	DefaultLeaseDuration = 60 * time.Second
	DefaultAIDeadline    = 10 * time.Second
	DefaultShutdownGrace = 20 * time.Second
	DefaultLogLevel      = "info"
)

// Ceilings for the bounded values. A batch size or lease that can be set
// without limit is a batch that can be set to a number no database will serve,
// which is a denial of service an operator can inflict on themselves with a
// typo in an env file.
const (
	MaxClaimBatch    = 1000
	MaxLeaseDuration = time.Hour
	MaxPollInterval  = time.Hour
)

// Load reads the configuration from the process environment and validates it.
//
// Validation returns a *reason*. "an unset evidence root is an error" is not a
// helpful message at three in the morning; "SP_EVIDENCE_ROOT is required: the
// core stores evidence bytes here and cannot find them again if it guesses" is.
func Load() (Config, error) { return LoadFrom(DefaultLookup) }

// LoadFrom reads the configuration through an injected lookup.
func LoadFrom(get Lookup) (Config, error) {
	if get == nil {
		get = DefaultLookup
	}
	var c Config

	c.Listen = envOr(get, "SP_LISTEN_ADDR", DefaultListen)
	c.TLSCertFile = strings.TrimSpace(get("SP_TLS_CERT"))
	c.TLSKeyFile = strings.TrimSpace(get("SP_TLS_KEY"))
	c.BehindProxy = boolEnv(get, "SP_BEHIND_PROXY", false)
	c.SessionCookieSecure = boolEnv(get, "SP_SESSION_COOKIE_SECURE", true)

	c.Tenant = strings.TrimSpace(get("SP_TENANT_ID"))
	c.SiteID = strings.TrimSpace(get("SP_SITE_ID"))
	c.IntegrationID = strings.TrimSpace(get("SP_INTEGRATION_ID"))

	c.Database = dbx.DefaultConfig()
	if dsn := strings.TrimSpace(get("SP_DATABASE_URL")); dsn != "" {
		if err := c.Database.ApplyDSN(dsn); err != nil {
			return c, fmt.Errorf("config: SP_DATABASE_URL: %w", err)
		}
	}

	c.EvidenceRoot = strings.TrimSpace(get("SP_EVIDENCE_ROOT"))
	c.CheckpointDir = strings.TrimSpace(get("SP_CHECKPOINT_DIR"))
	c.CheckpointKeyPath = strings.TrimSpace(get("SP_CHECKPOINT_KEY"))

	// DispatchDestinationsPath is where `internal/dispatch` reads its destinations from.
	//
	// A PATH and not the destinations themselves, for the reason `internal/config` says
	// about every other field: a field on Config is a field somebody will print, and a
	// destination carries a URL and a `secret_ref` rather than a secret - the secret is
	// named, not held. Putting the destinations on Config would make that rule harder to
	// keep, and `dispatch.LoadDestinations` already owns the file format and its
	// validation.
	//
	// Absent means the default path, and a missing default path means an install with no
	// destinations, which is a supported configuration and not an error. A bundle that
	// notifies is refused at startup by `dispatcher.CheckDestinations`.
	c.DispatchDestinationsPath = strings.TrimSpace(
		envOr(get, "SP_DISPATCH_DESTINATIONS", dispatch.DefaultDestinationsPath))

	c.AIEndpoint = strings.TrimRight(strings.TrimSpace(get("SP_AI_ENDPOINT")), "/")
	c.LogLevel = strings.ToLower(envOr(get, "SP_LOG_LEVEL", DefaultLogLevel))
	c.ShutdownGrace = DefaultShutdownGrace

	var err error
	if c.TrustedProxyCIDRs, err = cidrListEnv(get, "SP_TRUSTED_PROXY_CIDRS"); err != nil {
		return c, err
	}
	if c.PollInterval, err = durationEnv(get, "SP_POLL_INTERVAL", DefaultPollInterval); err != nil {
		return c, err
	}
	if c.AIDeadline, err = durationEnv(get, "SP_AI_DEADLINE", DefaultAIDeadline); err != nil {
		return c, err
	}
	if c.LeaseDuration, err = durationEnv(get, "SP_LEASE_DURATION", DefaultLeaseDuration); err != nil {
		return c, err
	}
	if c.ShutdownGrace, err = durationEnv(get, "SP_SHUTDOWN_GRACE", DefaultShutdownGrace); err != nil {
		return c, err
	}
	if c.ClaimBatchSize, err = intEnv(get, "SP_CLAIM_BATCH", DefaultClaimBatch); err != nil {
		return c, err
	}

	if err := c.validate(); err != nil {
		return c, err
	}
	return c, nil
}

// validate refuses anything whose absence or contradiction would change
// behaviour. Each refusal names the variable and says what breaks.
func (c Config) validate() error {
	switch {
	case c.Tenant == "":
		return fmt.Errorf("config: SP_TENANT_ID is required: tenant_id is never read from a " +
			"request body, so there is nowhere else for the core to learn which tenant it serves")
	case c.SiteID == "":
		return fmt.Errorf("config: SP_SITE_ID is required: the core is single-site and every " +
			"event it normalises is site-bound")
	case c.IntegrationID == "":
		return fmt.Errorf("config: SP_INTEGRATION_ID is required: it is the namespace the " +
			"inbox dedupes within, and without it a re-read of the same feed is re-ingested")
	case c.EvidenceRoot == "":
		return fmt.Errorf("config: SP_EVIDENCE_ROOT is required: the core stores evidence bytes " +
			"there and cannot find them again if it guesses a location")
	case (c.TLSCertFile == "") != (c.TLSKeyFile == ""):
		return fmt.Errorf("config: SP_TLS_CERT and SP_TLS_KEY must be set together; one without " +
			"the other is a certificate with no key or a key with no certificate")
	case c.AIEndpoint != "" && !strings.HasPrefix(c.AIEndpoint, "http://") &&
		!strings.HasPrefix(c.AIEndpoint, "https://"):
		return fmt.Errorf("config: SP_AI_ENDPOINT must be an http:// or https:// URL, got %q", c.AIEndpoint)
	}
	if !filepath.IsAbs(c.EvidenceRoot) && !strings.HasPrefix(c.EvidenceRoot, "/") {
		// Windows and POSIX are both handled by filepath; this only rejects the
		// genuinely relative case, which would otherwise resolve against whatever
		// the working directory happened to be when systemd started the unit.
		if !isRootedPath(c.EvidenceRoot) {
			return fmt.Errorf("config: SP_EVIDENCE_ROOT must be an absolute path, got %q: a "+
				"relative one resolves against the process's working directory, which is not "+
				"something a service unit controls", c.EvidenceRoot)
		}
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("config: SP_LOG_LEVEL must be one of debug, info, warn, error, got %q",
			c.LogLevel)
	}
	if c.ClaimBatchSize < 1 || c.ClaimBatchSize > MaxClaimBatch {
		return fmt.Errorf("config: SP_CLAIM_BATCH must be between 1 and %d, got %d",
			MaxClaimBatch, c.ClaimBatchSize)
	}
	if c.LeaseDuration < time.Second || c.LeaseDuration > MaxLeaseDuration {
		return fmt.Errorf("config: SP_LEASE_DURATION must be between 1s and %s, got %s",
			MaxLeaseDuration, c.LeaseDuration)
	}
	if c.PollInterval < time.Second || c.PollInterval > MaxPollInterval {
		return fmt.Errorf("config: SP_POLL_INTERVAL must be between 1s and %s, got %s",
			MaxPollInterval, c.PollInterval)
	}
	if c.AIDeadline < time.Second {
		return fmt.Errorf("config: SP_AI_DEADLINE must be at least 1s, got %s: a sub-second "+
			"client deadline produces degraded observations that say nothing about the runtime",
			c.AIDeadline)
	}
	// The three refusals below are the "insecure by default" pairings. Same shape
	// as the ceilings above: the failure mode is a configuration that loads and
	// then quietly hands somebody else's traffic the wrong answer, so the answer
	// is to refuse startup and name the setting.
	if c.BehindProxy && len(c.TrustedProxyCIDRs) == 0 {
		return fmt.Errorf("config: SP_BEHIND_PROXY is set but SP_TRUSTED_PROXY_CIDRS is empty: " +
			"without a trusted-proxy list the only client address left is the leftmost " +
			"X-Forwarded-For entry, which the caller wrote, so an audit row or a rate " +
			"limiter keyed on it is keyed on something the caller chose. Set " +
			"SP_TRUSTED_PROXY_CIDRS to the CIDR blocks your proxy connects from " +
			"(comma-separated, e.g. 10.0.0.0/8), or unset SP_BEHIND_PROXY if nothing proxies this process")
	}
	if !c.SessionCookieSecure && !isLoopbackListener(c.Listen) {
		return fmt.Errorf("config: SP_SESSION_COOKIE_SECURE=false is refused on a non-loopback "+
			"listener (%s): a session cookie without the Secure attribute is handed to the "+
			"network on every request. Bind SP_LISTEN_ADDR to 127.0.0.1 or [::1] if this is a "+
			"local development install, or leave the cookie Secure", c.Listen)
	}
	if c.TLSCertFile == "" && !c.BehindProxy {
		return fmt.Errorf("config: neither TLS nor a trusted proxy is configured: with " +
			"SP_TLS_CERT/SP_TLS_KEY unset and SP_BEHIND_PROXY=false the listener serves " +
			"plaintext and nothing upstream is terminating TLS. Set SP_TLS_CERT and " +
			"SP_TLS_KEY, or set SP_BEHIND_PROXY=true together with SP_TRUSTED_PROXY_CIDRS " +
			"if a TLS-terminating proxy fronts this process")
	}
	return nil
}

// isLoopbackListener reports whether the listen address binds only the local
// host.
//
// An empty host (":8443") is every interface and is therefore *not* loopback:
// an address with no host does not say "this machine", it says "every machine
// this machine can reach".
func isLoopbackListener(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port, or a malformed address. Fall back to the whole string rather
		// than guessing: an unparsable listen address is not evidence of loopback.
		host = strings.TrimSpace(addr)
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isRootedPath is filepath.IsAbs plus the POSIX form, so the check behaves the
// same on a Windows build that is validating a unit written for Linux.
func isRootedPath(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) ||
		(len(p) > 1 && p[1] == ':')
}

// Redacted renders the configuration for a startup log.
//
// It exists because a daemon that logs what it started with is worth more than
// one that does not, and because the temptation to log a struct directly is
// what eventually puts a password in a log file. Anything whose name suggests a
// credential is not a field on Config, so nothing here needs redaction - and
// this function is the place that would have to grow if one ever did.
func (c Config) Redacted() map[string]any {
	ai := "unset (the VLM worker will record degraded observations with reason no_runtime)"
	if c.AIEndpoint != "" {
		ai = c.AIEndpoint
	}
	checkpoint := "unset (the audit ledger is not being sealed)"
	if c.CheckpointDir != "" {
		checkpoint = c.CheckpointDir
	}
	return map[string]any{
		"listen":                c.Listen,
		"tls":                   c.TLSCertFile != "",
		"behind_proxy":          c.BehindProxy,
		"trusted_proxy_cidrs":   strings.Join(cidrStrings(c.TrustedProxyCIDRs), ","),
		"session_cookie_secure": c.SessionCookieSecure,
		"tenant_id":             c.Tenant,
		"site_id":               c.SiteID,
		"integration_id":        c.IntegrationID,
		"database":              c.Database.DSN(),
		"evidence_root":         c.EvidenceRoot,
		"checkpoints":           checkpoint,
		"poll_interval":         c.PollInterval.String(),
		"claim_batch":           c.ClaimBatchSize,
		"lease_duration":        c.LeaseDuration.String(),
		"ai_endpoint":           ai,
		"ai_deadline":           c.AIDeadline.String(),
		"log_level":             c.LogLevel,
		"shutdown_grace":        c.ShutdownGrace.String(),
	}
}

// cidrStrings renders the allow-list for the startup log.
func cidrStrings(nets []*net.IPNet) []string {
	out := make([]string, 0, len(nets))
	for _, n := range nets {
		if n != nil {
			out = append(out, n.String())
		}
	}
	return out
}

func envOr(get Lookup, key, def string) string {
	if v := strings.TrimSpace(get(key)); v != "" {
		return v
	}
	return def
}

func boolEnv(get Lookup, key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(get(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		// Not an error: a boolean that reads as false is the safe direction, and
		// refusing to start over a typo in an env file is its own outage.
		return def
	}
}

func durationEnv(get Lookup, key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s=%q is not a duration such as 30s or 5m: %w", key, raw, err)
	}
	return d, nil
}

func intEnv(get Lookup, key string, def int) (int, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return def, nil
	}
	n := 0
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return 0, fmt.Errorf("config: %s=%q is not a whole number", key, raw)
	}
	return n, nil
}

// cidrListEnv parses a comma-separated CIDR allow-list.
//
// An unset value is an empty list, which is a supported state: it means "no
// proxy is trusted", and validate() decides whether that is allowed in the
// configuration as a whole. A value that is set but unparsable is a refusal,
// because a typo in a trusted-proxy list fails open and nothing downstream
// reports it.
func cidrListEnv(get Lookup, key string) ([]*net.IPNet, error) {
	raw := strings.TrimSpace(get(key))
	if raw == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			out = append(out, network)
			continue
		}
		// A bare address is a single-host block. Operators write "10.1.2.3" more
		// often than "10.1.2.3/32" and both mean the same thing here.
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		return nil, fmt.Errorf("config: %s=%q is not a CIDR block or IP address; the list is "+
			"comma-separated, e.g. %q", key, entry, "10.0.0.0/8,192.168.1.1")
	}
	return out, nil
}
