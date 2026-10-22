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
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"

	// `dispatch` owns the destinations file format and its validation, so it owns the
	// default path too. The dependency runs config -> dispatch rather than the reverse:
	// `config` is the package that decides WHERE things live, and a file format's
	// default location is part of that decision.
	//
	// It is not a cycle. `dispatch` imports `dbx`, `jobs`, `outbox` and `rules`, and
	// none of them import `config`.
	"github.com/Sergey-Bar/vision/internal/dispatch"
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
	// TrustProxyAuthHeader names the header a trusted reverse proxy sets with
	// the OPERATOR'S identity, as `SP_TRUST_PROXY_AUTH_HEADER`.
	//
	// It is empty on a single-operator install, and the console then runs as
	// the configured principal. It is set on an install that fronts the console
	// with a proxy that terminates TLS and authenticates the operator, and it
	// changes the console's refusal behaviour: a request that does not carry
	// the header is refused, and a request that carries it from an untrusted
	// peer is refused.
	//
	// The header carries the IDENTITY and not the AUTHORITY. The role and the
	// site scope stay configuration, so a proxy that asserts an operator's
	// name cannot grant that operator more than the product's capability set
	// allows.
	//
	// It requires `SP_BEHIND_PROXY` and `SP_TRUSTED_PROXY_CIDRS`, and there is
	// no default: a header nobody can check the source of is a header anybody
	// can set.
	TrustProxyAuthHeader string

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

	// PrincipalRole is the role the API's CONFIGURED principal holds.
	//
	// The console has no login: there is no session, no user and no second
	// factor. What there is a configured identity, and this is its role. The
	// API's capability checks are real against it, so the role decides what the
	// console may do.
	//
	// The default is the tenant administrator. An install that has not
	// configured an identity has one identity, and it is the one that
	// administers.
	PrincipalRole string
	// ReportRoot is where the scheduled and exported analytics reports are
	// written.
	//
	// It is a ROOT and not a path: a report names a file INSIDE it, and the
	// name is validated by `internal/evidence`'s path guard before anything is
	// written. An operator who wants a report somewhere else configures the
	// root; they do not get to name an absolute path, because a path that
	// crosses out of the root is a path that reaches wherever the process can
	// write.
	ReportRoot string
	// ReportInterval is how often the loop checks for a report that is due.
	//
	// An hour, because the shortest cadence is a day, and a check every hour
	// misses at most one publication by an hour.
	ReportInterval time.Duration
	// EventRetention is how long the event tables are kept, and it is the
	// horizon the partition sweep drops at.
	//
	// It is the install's number and not a constant, because a sweep that
	// guessed a window would be a sweep that deleted data nobody agreed to
	// delete. Zero disables the sweep, and the ledger and the AI observations
	// are never swept at all: they are append-only, and the chain is what makes
	// the ledger worth keeping.
	EventRetention time.Duration
	// PartitionSweepInterval is how often the partition sweep runs.
	PartitionSweepInterval time.Duration
	// PrincipalSites are the sites the configured principal may see. Empty means
	// every site, which is the single-site install's case.
	PrincipalSites []string

	// SecretKeyPath is where the MFA wrapping key lives, as `SP_SECRET_KEY`.
	//
	// The key seals the second factor: the database holds the factor wrapped
	// under it, so the process that verifies a factor has to be the one that
	// can open it. The default is the installer's, so an install that has not
	// overridden it still finds the key the installer wrote.
	SecretKeyPath string

	// LoginRateLimitPerMinute is how many authentication attempts one source
	// may make per minute, as `SP_LOGIN_RATE_LIMIT_PER_MINUTE`.
	//
	// It is the site configuration's `console.loginRateLimitPerMinute`
	// (contracts/json-schema/site-config.schema.json), under the name the
	// configuration publishes it under: a control that is tuned under one name
	// and documented under another is a control nobody can tune.
	LoginRateLimitPerMinute int

	// ThreatEngine is which threat engine scores the attention queue, as
	// `SP_THREAT_ENGINE`.
	//
	// It is `legacy` or `calibrated`, and it is `legacy` by default.
	//
	// The default is `legacy` because the calibrated engine changes what the
	// operator sees, and a change to what an operator sees is a decision a
	// human signs (docs/decisions.md D-15). A flag that defaulted to the new
	// behaviour would make the decision by default, which is the same thing
	// as making it without one.
	//
	// A site with no labelled history sees NO difference between the two:
	// the calibrated engine leaves a rule's score alone when the site has no
	// history for it, because an absence is not a negative observation.
	ThreatEngine string

	// ThreatHalfLife is the attention queue's recency decay, as
	// `SP_THREAT_HALF_LIFE`.
	//
	// It is a duration, and it is the half-life: an incident twice this old
	// carries half its score in the queue. Zero disables the decay, which is
	// what the legacy engine has.
	//
	// The decay is a property of the QUEUE. The incident's stored score does
	// not change, because an incident's score is a fact about the incident
	// and a queue's order is a judgement.
	ThreatHalfLife time.Duration

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

	// HandoverDir is where the shift hand-over artifact lives, read by the
	// console's hand-over page. Empty means the site keeps none, which the
	// page reports by name.
	HandoverDir string

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

	// EvidenceFetchPreRoll is how far before the incident window the bookmark
	// should open. Default 5s.
	EvidenceFetchPreRoll time.Duration
	// EvidenceFetchPostRoll is how far after the incident window the bookmark
	// should extend. Default 10s.
	EvidenceFetchPostRoll time.Duration
	// EvidenceFetchRetainFor is how long a bookmark receipt is kept before the
	// retention sweep may delete it. Default 90 days.
	EvidenceFetchRetainFor time.Duration

	// RetentionSweepInterval is how often the retention sweep runs. Default 1 hour.
	// Set to 0 to disable automatic sweeps.
	RetentionSweepInterval time.Duration

	// RollupInterval is how often the durable metric rollup is derived.
	// Default 5 minutes. Set to 0 to disable the rollup.
	//
	// The rollup is what the analytics report reads, so an install with it
	// disabled has a report that says "not measured" and an install with it
	// enabled has one that says a number. Both are honest; the second is
	// useful.
	RollupInterval time.Duration

	// SiteModelPath is the operator's site map: the YAML that declares the
	// site's zones, cameras and routes.
	//
	// It is the SOURCE of the site view, and it is the operator's own drawing
	// rather than something the product inferred. Empty means this install has
	// no site model, and the site view says so rather than drawing an empty
	// map, because a map with no zones says the site has no zones.
	SiteModelPath string
	// VLMConcurrency is how many VLM jobs run in parallel. Default 1.
	// Set to 0 to disable the VLM worker entirely.
	VLMConcurrency int
}

// Lookup resolves an environment variable. Load takes one so the tests are
// deterministic: a test that reads the real environment can only be wrong in
// the environment it is run in.
type Lookup func(string) string

// DefaultLookup reads the process environment.
func DefaultLookup(key string) string { return os.Getenv(key) }

// Defaults for the values where a default does not change behaviour.
const (
	DefaultListen                 = "127.0.0.1:8443"
	DefaultPollInterval           = 5 * time.Second
	DefaultClaimBatch             = 64
	DefaultLeaseDuration          = 60 * time.Second
	DefaultAIDeadline             = 10 * time.Second
	DefaultShutdownGrace          = 20 * time.Second
	DefaultLogLevel               = "info"
	DefaultEvidenceFetchPreRoll   = 5 * time.Second
	DefaultEvidenceFetchPostRoll  = 10 * time.Second
	DefaultEvidenceFetchRetainFor = 90 * 24 * time.Hour
	DefaultRetentionSweepInterval = time.Hour
	// DefaultRollupInterval is how often the durable metric rollup is
	// derived.
	//
	// Five minutes rather than hourly, because the rollup derives the hour
	// that just ENDED: a pass every five minutes means the freshest bucket in
	// the table is never more than five minutes stale, and a report an
	// operator asks for at nine in the morning covers the night rather than
	// stopping at seven.
	DefaultRollupInterval = 5 * time.Minute
	// DefaultReportInterval is how often the loop checks for a report that is
	// due.
	//
	// An hour, because the shortest cadence is a day, and a check every hour
	// misses at most one publication by an hour.
	DefaultReportInterval = time.Hour
	DefaultVLMConcurrency = 1
	// DefaultEventRetention is how long the event tables are kept.
	//
	// Ninety days, which is a quarter. It is long enough that a site's
	// behaviour can be compared across a season and short enough that the
	// tables do not grow without a conversation.
	DefaultEventRetention = 90 * 24 * time.Hour
	// DefaultPartitionSweepInterval is how often the partition sweep runs.
	//
	// A day, because a partition only becomes droppable once a month has
	// passed, and a check that often misses at most one day of a month's
	// worth of partitions.
	DefaultPartitionSweepInterval = 24 * time.Hour
	// DefaultSecretKeyPath is where the MFA wrapping key lives when the
	// environment does not say. It matches the installer's, so an install that
	// has not overridden it still finds the key the installer wrote.
	DefaultSecretKeyPath = "/etc/security-platform/secret.key"
	// DefaultLoginRateLimitPerMinute is the published default for the
	// per-source login rate limit, the same number the site configuration
	// publishes as `console.loginRateLimitPerMinute`.
	DefaultLoginRateLimitPerMinute = 5
	// DefaultThreatEngine is the threat engine an install uses when the
	// configuration does not name one.
	//
	// It is `legacy`, and the reason is in the field's own comment: the
	// calibrated engine changes what the operator sees, and a human signs
	// that decision.
	DefaultThreatEngine = ThreatEngineLegacy
	// DefaultThreatHalfLife is the attention queue's default recency decay.
	// Two hours is long enough that an incident an operator is working
	// through does not fall down the queue, and short enough that
	// yesterday's fire is not today's fire.
	DefaultThreatHalfLife = 2 * time.Hour
)

// The threat engines.
//
// They are a closed vocabulary because a typo in an env file must not
// silently select a third behaviour.
const (
	// ThreatEngineLegacy is the engine the product shipped with: one score,
	// one axis, and a decision the matrix derived from the score.
	ThreatEngineLegacy = "legacy"
	// ThreatEngineCalibrated is the engine that scores the claim and the
	// evidence separately, calibrates against the site's own labelled
	// history, and decides on the two axes rather than on the score.
	ThreatEngineCalibrated = "calibrated"
)

// Ceilings for the bounded values. A batch size or lease that can be set
// without limit is a batch that can be set to a number no database will serve,
// which is a denial of service an operator can inflict on themselves with a
// typo in an env file.
const (
	MaxClaimBatch    = 1000
	MaxLeaseDuration = time.Hour
	MaxPollInterval  = time.Hour
	// MaxLoginRateLimitPerMinute is the ceiling on the per-source login rate
	// limit, and it is the same bound the site configuration publishes. A
	// limit above it is not a tighter limit; it is a limit nobody can trip.
	MaxLoginRateLimitPerMinute = 600
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
	c.TrustProxyAuthHeader = strings.TrimSpace(get("SP_TRUST_PROXY_AUTH_HEADER"))
	c.SessionCookieSecure = boolEnv(get, "SP_SESSION_COOKIE_SECURE", true)

	c.Tenant = strings.TrimSpace(get("SP_TENANT_ID"))
	c.SiteID = strings.TrimSpace(get("SP_SITE_ID"))
	c.IntegrationID = strings.TrimSpace(get("SP_INTEGRATION_ID"))
	c.SiteModelPath = strings.TrimSpace(get("SP_SITE_MODEL"))
	c.PrincipalRole = strings.TrimSpace(get("SP_PRINCIPAL_ROLE"))
	if c.PrincipalRole == "" {
		c.PrincipalRole = "tenant_admin"
	}
	// The sites the configured principal may see. Empty means every site, and
	// the single-site install is the case where that is right.
	for _, site := range strings.Split(get("SP_PRINCIPAL_SITES"), ",") {
		if site = strings.TrimSpace(site); site != "" {
			c.PrincipalSites = append(c.PrincipalSites, site)
		}
	}

	c.Database = dbx.DefaultConfig()
	if dsn := strings.TrimSpace(get("SP_DATABASE_URL")); dsn != "" {
		if err := c.Database.ApplyDSN(dsn); err != nil {
			return c, fmt.Errorf("config: SP_DATABASE_URL: %w", err)
		}
	}

	c.EvidenceRoot = strings.TrimSpace(get("SP_EVIDENCE_ROOT"))
	// The report root, and the reason it is derived from the evidence root
	// rather than invented: the reports are the product's own artefacts and
	// they belong next to the other artefacts, on the same volume, under the
	// same owner. An install that moves the evidence root moves the reports
	// with it.
	c.ReportRoot = strings.TrimSpace(get("SP_REPORT_ROOT"))
	if c.ReportRoot == "" && c.EvidenceRoot != "" {
		c.ReportRoot = filepath.Join(c.EvidenceRoot, "reports")
	}
	c.CheckpointDir = strings.TrimSpace(get("SP_CHECKPOINT_DIR"))
	c.CheckpointKeyPath = strings.TrimSpace(get("SP_CHECKPOINT_KEY"))

	// The MFA wrapping key and the per-source login rate limit.
	//
	// Both are configuration a site can change, and both are read here rather
	// than inside the packages that use them, because a value a site cannot
	// change without a rebuild is not configuration.
	c.SecretKeyPath = envOr(get, "SP_SECRET_KEY", DefaultSecretKeyPath)

	// HandoverDir is where the shift hand-over artifact is written and read
	// from: one append-only, hash-chained JSONL per shift, the console's
	// hand-over page reading the same files.
	//
	// Absent means the site keeps no duty log, and the console page says so by
	// name rather than rendering an empty hand-over, which would read as
	// "nothing happened". It is a path and not the artifact, for the same
	// reason DispatchDestinationsPath is one.
	c.HandoverDir = strings.TrimSpace(get("SP_HANDOVER_DIR"))

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

	c.EvidenceFetchPreRoll = DefaultEvidenceFetchPreRoll
	if v := strings.TrimSpace(get("SP_EVIDENCE_FETCH_PRE_ROLL")); v != "" {
		var err error
		c.EvidenceFetchPreRoll, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_EVIDENCE_FETCH_PRE_ROLL: %w", err)
		}
	}
	c.EvidenceFetchPostRoll = DefaultEvidenceFetchPostRoll
	if v := strings.TrimSpace(get("SP_EVIDENCE_FETCH_POST_ROLL")); v != "" {
		var err error
		c.EvidenceFetchPostRoll, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_EVIDENCE_FETCH_POST_ROLL: %w", err)
		}
	}
	c.EvidenceFetchRetainFor = DefaultEvidenceFetchRetainFor
	if v := strings.TrimSpace(get("SP_EVIDENCE_FETCH_RETAIN_FOR")); v != "" {
		var err error
		c.EvidenceFetchRetainFor, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_EVIDENCE_FETCH_RETAIN_FOR: %w", err)
		}
	}
	c.RetentionSweepInterval = DefaultRetentionSweepInterval
	if v := strings.TrimSpace(get("SP_RETENTION_SWEEP_INTERVAL")); v != "" {
		var err error
		c.RetentionSweepInterval, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_RETENTION_SWEEP_INTERVAL: %w", err)
		}
	}
	c.RollupInterval = DefaultRollupInterval
	c.ReportInterval = DefaultReportInterval
	c.EventRetention = DefaultEventRetention
	c.PartitionSweepInterval = DefaultPartitionSweepInterval
	if v := strings.TrimSpace(get("SP_EVENT_RETENTION")); v != "" {
		var err error
		c.EventRetention, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_EVENT_RETENTION: %w", err)
		}
	}
	if v := strings.TrimSpace(get("SP_PARTITION_SWEEP_INTERVAL")); v != "" {
		var err error
		c.PartitionSweepInterval, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_PARTITION_SWEEP_INTERVAL: %w", err)
		}
	}
	if v := strings.TrimSpace(get("SP_REPORT_INTERVAL")); v != "" {
		var err error
		c.ReportInterval, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_REPORT_INTERVAL: %w", err)
		}
	}
	if v := strings.TrimSpace(get("SP_ROLLUP_INTERVAL")); v != "" {
		var err error
		c.RollupInterval, err = time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_ROLLUP_INTERVAL: %w", err)
		}
	}
	c.VLMConcurrency = DefaultVLMConcurrency
	if v := strings.TrimSpace(get("SP_VLM_CONCURRENCY")); v != "" {
		var err error
		c.VLMConcurrency, err = strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("config: SP_VLM_CONCURRENCY: %w", err)
		}
	}

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
	if c.LoginRateLimitPerMinute, err = intEnv(get, "SP_LOGIN_RATE_LIMIT_PER_MINUTE", DefaultLoginRateLimitPerMinute); err != nil {
		return c, err
	}

	// The threat engine, and the queue's recency decay.
	c.ThreatEngine = strings.ToLower(strings.TrimSpace(get("SP_THREAT_ENGINE")))
	if c.ThreatEngine == "" {
		c.ThreatEngine = DefaultThreatEngine
	}
	if c.ThreatHalfLife, err = durationEnv(get, "SP_THREAT_HALF_LIFE", DefaultThreatHalfLife); err != nil {
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
	case c.ThreatEngine != ThreatEngineLegacy && c.ThreatEngine != ThreatEngineCalibrated:
		return fmt.Errorf("config: SP_THREAT_ENGINE must be %q or %q, got %q",
			ThreatEngineLegacy, ThreatEngineCalibrated, c.ThreatEngine)
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
	if c.VLMConcurrency < 0 || c.VLMConcurrency > 16 {
		return fmt.Errorf("config: SP_VLM_CONCURRENCY must be between 0 and 16, got %d",
			c.VLMConcurrency)
	}
	// The per-source login rate limit is bounded at both ends, and the low end
	// matters as much as the high one: a limit of zero would refuse every
	// attempt from every source, which reads as a broken install rather than
	// as a strict one.
	if c.LoginRateLimitPerMinute < 1 || c.LoginRateLimitPerMinute > MaxLoginRateLimitPerMinute {
		return fmt.Errorf("config: SP_LOGIN_RATE_LIMIT_PER_MINUTE must be between 1 and %d, got %d",
			MaxLoginRateLimitPerMinute, c.LoginRateLimitPerMinute)
	}
	if c.AIDeadline < time.Second {
		return fmt.Errorf("config: SP_AI_DEADLINE must be at least 1s, got %s: a sub-second "+
			"client deadline produces degraded observations that say nothing about the runtime",
			c.AIDeadline)
	}
	if c.EvidenceFetchPreRoll < 0 {
		return fmt.Errorf("config: SP_EVIDENCE_FETCH_PRE_ROLL cannot be negative, got %s",
			c.EvidenceFetchPreRoll)
	}
	if c.EvidenceFetchPostRoll < 0 {
		return fmt.Errorf("config: SP_EVIDENCE_FETCH_POST_ROLL cannot be negative, got %s",
			c.EvidenceFetchPostRoll)
	}
	if c.EvidenceFetchRetainFor < time.Hour {
		return fmt.Errorf("config: SP_EVIDENCE_FETCH_RETAIN_FOR must be at least 1h, got %s",
			c.EvidenceFetchRetainFor)
	}
	if c.RetentionSweepInterval < 0 {
		return fmt.Errorf("config: SP_RETENTION_SWEEP_INTERVAL cannot be negative, got %s",
			c.RetentionSweepInterval)
	}
	if c.RetentionSweepInterval > 0 && c.RetentionSweepInterval < time.Minute {
		return fmt.Errorf("config: SP_RETENTION_SWEEP_INTERVAL must be 0 (disabled) or at least 1m, got %s",
			c.RetentionSweepInterval)
	}
	if c.RollupInterval < 0 {
		return fmt.Errorf("config: SP_ROLLUP_INTERVAL cannot be negative, got %s",
			c.RollupInterval)
	}
	if c.RollupInterval > 0 && c.RollupInterval < time.Minute {
		return fmt.Errorf("config: SP_ROLLUP_INTERVAL must be 0 (disabled) or at least 1m, got %s",
			c.RollupInterval)
	}
	// A negative retention would be a horizon in the FUTURE, and the sweep
	// drops everything before it -- which is everything.
	if c.EventRetention < 0 {
		return fmt.Errorf("config: SP_EVENT_RETENTION cannot be negative, got %s: a horizon in "+
			"the future drops every partition", c.EventRetention)
	}
	if c.PartitionSweepInterval < 0 {
		return fmt.Errorf("config: SP_PARTITION_SWEEP_INTERVAL cannot be negative, got %s",
			c.PartitionSweepInterval)
	}
	if c.ReportInterval < 0 {
		return fmt.Errorf("config: SP_REPORT_INTERVAL cannot be negative, got %s",
			c.ReportInterval)
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
			"SP_TRUSTED_PROXY_CIDRs to the CIDR blocks your proxy connects from " +
			"(comma-separated, e.g. 10.0.0.0/8), or unset SP_BEHIND_PROXY if nothing proxies this process")
	}
	// The proxy-auth header is only believed when the request came from a
	// TRUSTED proxy, and the trusted-proxy list is what says which peers are
	// trusted. Without it the header is a header: forgeable by anybody who can
	// reach the port, which is the opposite of what it is for.
	if c.TrustProxyAuthHeader != "" {
		if !c.BehindProxy {
			return fmt.Errorf("config: SP_TRUST_PROXY_AUTH_HEADER is set but SP_BEHIND_PROXY is " +
				"not: the identity header is only believed when the request arrives from a " +
				"trusted proxy, and without SP_BEHIND_PROXY there is no trusted-proxy list to " +
				"check it against. Set SP_BEHIND_PROXY and SP_TRUSTED_PROXY_CIDRS, or unset " +
				"SP_TRUST_PROXY_AUTH_HEADER")
		}
		if len(c.TrustedProxyCIDRs) == 0 {
			return fmt.Errorf("config: SP_TRUST_PROXY_AUTH_HEADER is set but " +
				"SP_TRUSTED_PROXY_CIDRS is empty: the identity header would be believed from " +
				"any peer, which makes it a way in rather than a way in. Set " +
				"SP_TRUSTED_PROXY_CIDRS to the CIDR blocks your proxy connects from")
		}
	}
	if !c.SessionCookieSecure && !isLoopbackListener(c.Listen) {
		return fmt.Errorf("config: SP_SESSION_COOKIE_SECURE=false is refused on a non-loopback "+
			"listener (%s): a session cookie without the Secure attribute is handed to the "+
			"network on every request. Bind SP_LISTEN_ADDR to 127.0.0.1 or [::1] if this is a "+
			"local development install, or leave the cookie Secure", c.Listen)
	}
	// The console's identity is the network's, and a console on a network an
	// operator's browser shares with a stranger has no identity at all.
	//
	// With no authenticating proxy, every request runs as the CONFIGURED
	// principal, so every operator who can reach the port is the same
	// operator, and the audit ledger cannot tell them apart. On a loopback
	// address that is fine -- the only thing that can reach it is the operator
	// on the machine. On a routable address it is not, and the refusal is at
	// STARTUP rather than a warning, because a warning is read by the person
	// who is already looking and this is read by nobody.
	//
	// This is D-16's consequence in code: the appliance era's answer is
	// "loopback or a proxy", and an install that wants neither has to say so
	// out loud.
	//
	// It is checked AFTER the session-cookie refusal, so a configuration with
	// both problems is told about the more specific one first.
	if c.TrustProxyAuthHeader == "" && !isLoopbackListener(c.Listen) {
		return fmt.Errorf("config: SP_LISTEN_ADDR is %s, which is not a loopback address, and no "+
			"authenticating proxy is configured: the console would serve every operator who can "+
			"reach the port as the SAME configured principal, with no way to tell them apart. "+
			"Bind SP_LISTEN_ADDR to 127.0.0.1 or [::1], or set SP_TRUST_PROXY_AUTH_HEADER with "+
			"SP_BEHIND_PROXY and SP_TRUSTED_PROXY_CIDRS and put an authenticating proxy in "+
			"front (deploy/ ships a reference configuration)", c.Listen)
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
		"listen":                    c.Listen,
		"tls":                       c.TLSCertFile != "",
		"behind_proxy":              c.BehindProxy,
		"trust_proxy_auth_header":   c.TrustProxyAuthHeader,
		"trusted_proxy_cidrs":       strings.Join(cidrStrings(c.TrustedProxyCIDRs), ","),
		"session_cookie_secure":     c.SessionCookieSecure,
		"tenant_id":                 c.Tenant,
		"site_id":                   c.SiteID,
		"integration_id":            c.IntegrationID,
		"database":                  c.Database.DSN(),
		"evidence_root":             c.EvidenceRoot,
		"checkpoints":               checkpoint,
		"poll_interval":             c.PollInterval.String(),
		"claim_batch":               c.ClaimBatchSize,
		"lease_duration":            c.LeaseDuration.String(),
		"ai_endpoint":               ai,
		"ai_deadline":               c.AIDeadline.String(),
		"log_level":                 c.LogLevel,
		"shutdown_grace":            c.ShutdownGrace.String(),
		"evidence_fetch_pre_roll":   c.EvidenceFetchPreRoll.String(),
		"evidence_fetch_post_roll":  c.EvidenceFetchPostRoll.String(),
		"evidence_fetch_retain_for": c.EvidenceFetchRetainFor.String(),
		"retention_sweep_interval":  c.RetentionSweepInterval.String(),
		"rollup_interval":           c.RollupInterval.String(),
		"vlm_concurrency":           c.VLMConcurrency,
		"threat_engine":             c.ThreatEngine,
		"threat_half_life":          c.ThreatHalfLife.String(),
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
