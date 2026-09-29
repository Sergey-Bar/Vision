package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/config"
)

// env builds a deterministic lookup. No test here reads the real environment: a
// test that does can only be wrong in the environment it happens to run in, and
// "it passed on my machine" is a sentence nobody should have to say about a
// daemon's refusal messages.
func env(pairs map[string]string) config.Lookup {
	return func(key string) string { return pairs[key] }
}

// valid is the smallest configuration that loads. Every refusal test starts
// from this and breaks exactly one field, so a failure names the field that
// actually broke.
//
// The TLS pair is part of it because "neither TLS nor a trusted proxy" is
// itself a refusal: a listener with nothing terminating TLS in front of it
// serves plaintext, and that is not a configuration this package will load.
func valid() map[string]string {
	return map[string]string{
		"SP_TENANT_ID":      "tenant-local",
		"SP_SITE_ID":        "site-1",
		"SP_INTEGRATION_ID": "int-1",
		"SP_EVIDENCE_ROOT":  "/var/lib/security-platform/evidence",
		"SP_TLS_CERT":       "/etc/ssl/cert.pem",
		"SP_TLS_KEY":        "/etc/ssl/key.pem",
	}
}

func load(t *testing.T, pairs map[string]string) (config.Config, error) {
	t.Helper()
	return config.LoadFrom(env(pairs))
}

func mustLoad(t *testing.T, pairs map[string]string) config.Config {
	t.Helper()
	c, err := load(t, pairs)
	if err != nil {
		t.Fatalf("the configuration was refused: %v", err)
	}
	return c
}

// One case per field, each asserting the exact refusal. A refusal that names the
// variable and the consequence is what somebody reads at three in the morning; a
// refusal that says "invalid configuration" is what makes one of them stop
// reading them.
func TestRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		unset string
		want  string
	}{
		{
			name:  "no tenant",
			unset: "SP_TENANT_ID",
			want:  "SP_TENANT_ID is required",
		},
		{
			name:  "no site",
			unset: "SP_SITE_ID",
			want:  "SP_SITE_ID is required",
		},
		{
			name:  "no integration",
			unset: "SP_INTEGRATION_ID",
			want:  "SP_INTEGRATION_ID is required",
		},
		{
			name:  "no evidence root",
			unset: "SP_EVIDENCE_ROOT",
			want:  "SP_EVIDENCE_ROOT is required",
		},
		{
			name:  "relative evidence root",
			unset: "",
			want:  "SP_EVIDENCE_ROOT must be an absolute path",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pairs := valid()
			if tc.unset != "" {
				delete(pairs, tc.unset)
			} else {
				pairs["SP_EVIDENCE_ROOT"] = "evidence"
			}
			_, err := load(t, pairs)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not contain %q", err, tc.want)
			}
		})
	}
}

// An unset evidence root must never become "" or ".". A core that starts
// without knowing where evidence lives will accept evidence and put it somewhere
// it cannot find again, and that is a silent total loss of the evidence store.
func TestEvidenceRootHasNoSilentDefault(t *testing.T) {
	pairs := valid()
	delete(pairs, "SP_EVIDENCE_ROOT")
	c, err := load(t, pairs)
	if err == nil {
		t.Fatalf("an unset evidence root loaded as %q", c.EvidenceRoot)
	}
	if c.EvidenceRoot != "" && c.EvidenceRoot != "." {
		t.Errorf("a refused configuration still carries a usable evidence root %q", c.EvidenceRoot)
	}
}

func TestMalformedValuesAreRefusedWithTheVariableNamed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"poll interval is not a duration", "SP_POLL_INTERVAL", "soon", "SP_POLL_INTERVAL=\"soon\""},
		{"claim batch is not a number", "SP_CLAIM_BATCH", "lots", "SP_CLAIM_BATCH=\"lots\""},
		{"lease is not a duration", "SP_LEASE_DURATION", "60", "SP_LEASE_DURATION=\"60\""},
		{"log level is not a level", "SP_LOG_LEVEL", "chatty", "SP_LOG_LEVEL must be one of"},
		{"AI endpoint has no scheme", "SP_AI_ENDPOINT", "127.0.0.1:8800", "SP_AI_ENDPOINT must be an http"},
		{"AI deadline is too small", "SP_AI_DEADLINE", "10ms", "SP_AI_DEADLINE must be at least 1s"},
		{"claim batch is zero", "SP_CLAIM_BATCH", "0", "SP_CLAIM_BATCH must be between 1 and"},
		{"lease is longer than the ceiling", "SP_LEASE_DURATION", "48h", "SP_LEASE_DURATION must be between"},
		{"database URL is not a URL", "SP_DATABASE_URL", "://nope", "SP_DATABASE_URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pairs := valid()
			pairs[tc.key] = tc.value
			_, err := load(t, pairs)
			if err == nil {
				t.Fatalf("%s=%q was accepted", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not contain %q", err, tc.want)
			}
		})
	}
}

// A certificate with no key is a misconfiguration, not a TLS configuration the
// core can work around: the alternative is a listener that silently serves
// plaintext to an operator who believes they configured TLS.
func TestTLSRequiresBothHalves(t *testing.T) {
	for _, tc := range []struct{ cert, key string }{
		{"/etc/ssl/cert.pem", ""},
		{"", "/etc/ssl/key.pem"},
	} {
		pairs := valid()
		delete(pairs, "SP_TLS_CERT")
		delete(pairs, "SP_TLS_KEY")
		pairs["SP_TLS_CERT"] = tc.cert
		pairs["SP_TLS_KEY"] = tc.key
		if _, err := load(t, pairs); err == nil {
			t.Errorf("cert=%q key=%q was accepted", tc.cert, tc.key)
		} else if !strings.Contains(err.Error(), "must be set together") {
			t.Errorf("refusal %q does not explain that both halves are needed", err)
		}
	}

	pairs := valid()
	if _, err := load(t, pairs); err != nil {
		t.Errorf("a complete TLS pair was refused: %v", err)
	}
}

// A site behind a proxy has one to name. Without a trusted-proxy list the only
// address left is the leftmost X-Forwarded-For entry, which the caller wrote,
// so the audit row and the rate limiter are both keyed on something the caller
// chose. That is a refusal, not a fallback.
func TestBehindProxyRequiresATrustedProxyList(t *testing.T) {
	pairs := valid()
	pairs["SP_BEHIND_PROXY"] = "true"
	delete(pairs, "SP_TLS_CERT")
	delete(pairs, "SP_TLS_KEY")

	_, err := load(t, pairs)
	if err == nil {
		t.Fatal("SP_BEHIND_PROXY=true with no SP_TRUSTED_PROXY_CIDRS was accepted")
	}
	// The message has to name both settings and say what to do, because the
	// operator reading it has to choose between two variables.
	for _, want := range []string{"SP_BEHIND_PROXY", "SP_TRUSTED_PROXY_CIDRS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}

	pairs["SP_TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8"
	c, err := load(t, pairs)
	if err != nil {
		t.Fatalf("a proxy configuration with a trusted list was refused: %v", err)
	}
	if len(c.TrustedProxyCIDRs) != 1 || c.TrustedProxyCIDRs[0].String() != "10.0.0.0/8" {
		t.Errorf("the trusted list parsed as %v", c.TrustedProxyCIDRs)
	}
}

// A cookie without the Secure attribute on anything reachable from the network
// is a live session handed to whoever is on the path. Loopback is the one
// install where the refusal would be a nuisance rather than a protection.
func TestInsecureSessionCookieRequiresALoopbackListener(t *testing.T) {
	pairs := valid()
	pairs["SP_SESSION_COOKIE_SECURE"] = "false"
	pairs["SP_LISTEN_ADDR"] = "0.0.0.0:8443"

	_, err := load(t, pairs)
	if err == nil {
		t.Fatal("SP_SESSION_COOKIE_SECURE=false on 0.0.0.0 was accepted")
	}
	for _, want := range []string{"SP_SESSION_COOKIE_SECURE", "Secure"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %s", err, want)
		}
	}

	pairs["SP_LISTEN_ADDR"] = "127.0.0.1:8443"
	c, err := load(t, pairs)
	if err != nil {
		t.Fatalf("an insecure cookie on a loopback listener was refused: %v", err)
	}
	if c.SessionCookieSecure {
		t.Error("SP_SESSION_COOKIE_SECURE=false did not read as false on loopback")
	}
}

// Neither TLS nor a proxy means a plaintext listener, and the comment claiming
// otherwise is the kind of claim this repository treats as a defect.
func TestNeitherTLSNorProxyIsRefused(t *testing.T) {
	pairs := valid()
	delete(pairs, "SP_TLS_CERT")
	delete(pairs, "SP_TLS_KEY")

	_, err := load(t, pairs)
	if err == nil {
		t.Fatal("a configuration with neither TLS nor a proxy was accepted")
	}
	if !strings.Contains(err.Error(), "neither TLS nor a trusted proxy") {
		t.Errorf("refusal %q does not say what is missing", err)
	}

	// Behind a proxy, without TLS on this process, is the supported alternative.
	pairs["SP_BEHIND_PROXY"] = "true"
	pairs["SP_TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8"
	if _, err := load(t, pairs); err != nil {
		t.Errorf("a proxied configuration with no local TLS was refused: %v", err)
	}
}

// The default is Secure. A daemon that starts with the cookie flag unset must
// not be reading that as permission to send it in the clear.
func TestSessionCookieSecureDefaultsToTrue(t *testing.T) {
	c := mustLoad(t, valid())
	if !c.SessionCookieSecure {
		t.Error("the session cookie defaults to not-Secure")
	}
}

// A typo in the trusted-proxy list fails open, and nothing downstream reports
// it, so it has to be a refusal at load time.
func TestMalformedTrustedProxyCIDRIsRefused(t *testing.T) {
	pairs := valid()
	pairs["SP_TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8,not-a-cidr"
	_, err := load(t, pairs)
	if err == nil {
		t.Fatal("a malformed CIDR in the trusted list was accepted")
	}
	if !strings.Contains(err.Error(), "SP_TRUSTED_PROXY_CIDRS") {
		t.Errorf("refusal %q does not name the variable", err)
	}
}

// The list is operator-typed, so the common spellings all have to work: blocks,
// a bare address, whitespace, and IPv6.
func TestTrustedProxyCIDRListFormats(t *testing.T) {
	pairs := valid()
	pairs["SP_TRUSTED_PROXY_CIDRS"] = " 10.0.0.0/8 , 192.168.1.7 , 2001:db8::/32 ,"
	c, err := load(t, pairs)
	if err != nil {
		t.Fatalf("a valid trusted list was refused: %v", err)
	}
	got := make([]string, 0, len(c.TrustedProxyCIDRs))
	for _, n := range c.TrustedProxyCIDRs {
		got = append(got, n.String())
	}
	want := []string{"10.0.0.0/8", "192.168.1.7/32", "2001:db8::/32"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d is %s, want %s", i, got[i], want[i])
		}
	}
}

// Unset is an empty list, not an error, and not a wildcard. The wildcard is the
// bug: it would trust every peer, which is the state the refusal exists to
// prevent.
func TestUnsetTrustedProxyListIsEmptyNotWildcard(t *testing.T) {
	c := mustLoad(t, valid())
	if len(c.TrustedProxyCIDRs) != 0 {
		t.Errorf("an unset SP_TRUSTED_PROXY_CIDRS parsed as %v", c.TrustedProxyCIDRs)
	}
}

// No AI runtime is a supported configuration — a site with no model is a real
// deployment — so it must load rather than refuse. What must not happen is the
// daemon inventing an endpoint.
func TestUnsetAIEndpointIsSupportedAndNotInvented(t *testing.T) {
	c := mustLoad(t, valid())
	if c.AIEndpoint != "" {
		t.Errorf("an unset SP_AI_ENDPOINT became %q; the worker would then pretend to "+
			"have a runtime and record a connection failure instead of saying no_runtime", c.AIEndpoint)
	}
}

func TestDefaultsAreApplied(t *testing.T) {
	c := mustLoad(t, valid())

	for _, tc := range []struct {
		name string
		got  any
		want any
	}{
		{"listen", c.Listen, config.DefaultListen},
		{"poll interval", c.PollInterval, config.DefaultPollInterval},
		{"claim batch", c.ClaimBatchSize, config.DefaultClaimBatch},
		{"lease duration", c.LeaseDuration, config.DefaultLeaseDuration},
		{"AI deadline", c.AIDeadline, config.DefaultAIDeadline},
		{"log level", c.LogLevel, config.DefaultLogLevel},
	} {
		if tc.got != tc.want {
			t.Errorf("%s defaulted to %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestValuesAreRead(t *testing.T) {
	pairs := valid()
	pairs["SP_LISTEN_ADDR"] = "127.0.0.1:9443"
	pairs["SP_BEHIND_PROXY"] = "true"
	pairs["SP_TRUSTED_PROXY_CIDRS"] = "10.0.0.0/8"
	pairs["SP_POLL_INTERVAL"] = "30s"
	pairs["SP_CLAIM_BATCH"] = "256"
	pairs["SP_LEASE_DURATION"] = "2m"
	pairs["SP_AI_ENDPOINT"] = "http://127.0.0.1:8800/"
	pairs["SP_AI_DEADLINE"] = "45s"
	pairs["SP_CHECKPOINT_DIR"] = "/var/lib/security-platform/checkpoints"
	pairs["SP_LOG_LEVEL"] = "DEBUG"
	pairs["SP_DATABASE_URL"] = "postgres://security@127.0.0.1:6000/security?sslmode=disable"

	c := mustLoad(t, pairs)

	for _, tc := range []struct {
		name string
		got  any
		want any
	}{
		{"listen", c.Listen, "127.0.0.1:9443"},
		{"behind proxy", c.BehindProxy, true},
		{"poll interval", c.PollInterval, 30 * time.Second},
		{"claim batch", c.ClaimBatchSize, 256},
		{"lease duration", c.LeaseDuration, 2 * time.Minute},
		{"AI deadline", c.AIDeadline, 45 * time.Second},
		{"checkpoints", c.CheckpointDir, "/var/lib/security-platform/checkpoints"},
		{"log level is lowercased", c.LogLevel, "debug"},
		{"database name", c.Database.Database, "security"},
		{"database port", c.Database.Port, 6000},
		{"database user", c.Database.User, "security"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	// The trailing slash is stripped so a caller can join a path without
	// producing a double slash in a URL the operator wrote by hand.
	if c.AIEndpoint != "http://127.0.0.1:8800" {
		t.Errorf("AI endpoint is %q, want the trailing slash removed", c.AIEndpoint)
	}
}

// The database password must not be reachable through this package. If it ever
// becomes a Config field, every "what did the core start with" log line becomes
// a credential disclosure waiting for somebody to paste it into a ticket.
func TestRedactedCarriesNoPassword(t *testing.T) {
	pairs := valid()
	pairs["SP_DATABASE_PASSWORD"] = "hunter2-should-never-be-here"
	c := mustLoad(t, pairs)

	for k, v := range c.Redacted() {
		if s, ok := v.(string); ok && strings.Contains(s, "hunter2") {
			t.Errorf("Redacted()[%q] = %q contains the database password", k, s)
		}
	}
	if strings.Contains(c.Database.DSN(), "hunter2") {
		t.Error("the rendered DSN contains a password; DSN is the field the startup log uses")
	}
	// The non-secret identity is there, or the log line is not worth printing.
	rendered := c.Redacted()
	if rendered["tenant_id"] != "tenant-local" || rendered["site_id"] != "site-1" {
		t.Errorf("Redacted() omits the tenant and site: %v", rendered)
	}
}
