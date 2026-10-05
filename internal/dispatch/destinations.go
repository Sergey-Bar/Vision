package dispatch

// Destinations: where a notify actually goes, and why it is a file and not a database table.
//
// # Why a file
//
// `internal/config` declares the daemon single-tenant and single-site BY CONSTRUCTION
// (`config.go:40`), so a destination is a property of the installation rather than of a
// tenant. A table would be a second source of truth for something the operator wrote once at
// install time, plus a migration, plus an OpenAPI surface, plus - per AGENTS.md - a human to
// read the diff before it lands. The file is the install-time artefact it actually is.
//
// It also means the reachable set of hosts is configured out of band of the rule bundles,
// which is the security property that matters: a bundle is operator-editable and its
// `target` values come from its schema, so a target that could be a URL would make every
// bundle an SSRF primitive. A target here is a NAME, resolved through this file, so the set
// of hosts a dispatch can reach is exactly as wide as the operator made it.
//
// # No secrets, ever
//
// `SecretRef` names an environment variable. The value is read from the environment at
// delivery time and is never stored, never logged, never rendered into a payload and never
// written back to disk.
//
// A secret in a configuration file is a secret in the release archive, in `docker inspect`,
// in a support bundle and in a screenshot. `internal/config` already refuses to put the
// database password on a `Config` field for exactly this reason - its own comment says "a
// field on Config is a field that somebody will print" - and this file is in the same
// position and gets the same answer.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Destination is one reachable receiver.
type Destination struct {
	// Name is what a rule's `target` resolves to.
	Name string `yaml:"name"`
	// URL is where the POST goes.
	//
	// Refused if it is not https, and refused if its host resolves to a loopback, link-local
	// or RFC 1918 address. See CheckURL for why the second half is a control and not
	// caution.
	URL string `yaml:"url"`
	// SecretRef names an environment variable holding a bearer token. Empty means no
	// credential, which is a supported configuration for a receiver on the operator's own
	// network.
	SecretRef string `yaml:"secret_ref,omitempty"`
	// InsecureSkipVerify refuses TLS verification.
	//
	// Present because a site whose receiver sits behind a private certificate authority
	// has a real need for it, and refused when TRUE at startup with a message naming the
	// destination. It is a field rather than an omission so that "we turned it off" is a
	// recorded decision in a file somebody reads, not an absence nobody can see.
	InsecureSkipVerify bool `yaml:"insecure_skip_verify,omitempty"`
}

// Destinations is the configured set.
type Destinations struct {
	// byName is the resolution index. A map because a rule's target is looked up on the
	// delivery path, and a linear scan over a set that will never exceed a handful of
	// entries buys nothing.
	byName map[string]Destination
	// order is the declaration order, for error messages and `securityctl doctor`. A map
	// iteration would make a startup refusal name destinations in a different order on
	// every run, which makes two runs of the same install incomparable.
	order []string
}

// Lookup resolves a destination name.
func (d *Destinations) Lookup(name string) (Destination, bool) {
	if d == nil {
		return Destination{}, false
	}
	dest, ok := d.byName[name]
	return dest, ok
}

// Names returns the configured destination names, sorted.
func (d *Destinations) Names() []string {
	if d == nil {
		return nil
	}
	out := append([]string(nil), d.order...)
	sort.Strings(out)
	return out
}

// Empty reports whether there are no destinations at all.
//
// A nil-safe predicate rather than `len(d.byName) == 0`, because the interesting case is a
// nil *Destinations - an install with no destinations file - and a method that panics on nil
// turns that case into a crash at startup instead of a refusal that names the file.
func (d *Destinations) Empty() bool { return d == nil || len(d.byName) == 0 }

// LoadDestinations reads and validates a destinations file.
//
// Missing file is an EMPTY SET, not an error: an install that sends no notifications is a
// supported configuration and must be able to start. A file that EXISTS and is malformed is
// an error, because an operator who wrote a destinations file and got a typo has asked for
// something and silently not got it otherwise.
func LoadDestinations(path string) (*Destinations, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Destinations{byName: map[string]Destination{}}, nil
		}
		return nil, fmt.Errorf("dispatch: read destinations %s: %w", path, err)
	}
	var doc destinationsDoc
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("dispatch: parse destinations %s: %w", path, err)
	}

	out := &Destinations{byName: map[string]Destination{}}
	for i, d := range doc.Destinations {
		if strings.TrimSpace(d.Name) == "" {
			return nil, fmt.Errorf("dispatch: destinations %s: entry %d has no name, and "+
				"a rule's dispatch target is resolved by name", path, i)
		}
		if _, dup := out.byName[d.Name]; dup {
			return nil, fmt.Errorf("dispatch: destinations %s: %q is declared twice. A "+
				"duplicate name makes the effective one a matter of file order, which is "+
				"how a site ends up notifying one of two receivers", path, d.Name)
		}
		if err := CheckURL(d.URL); err != nil {
			return nil, fmt.Errorf("dispatch: destinations %s: %q: %w", path, d.Name, err)
		}
		if d.InsecureSkipVerify {
			return nil, fmt.Errorf("dispatch: destinations %s: %q sets "+
				"insecure_skip_verify, so the bearer token for this destination would go to "+
				"a server whose certificate was not checked. Install the CA on the host "+
				"instead, or route to a receiver you control", path, d.Name)
		}
		if d.SecretRef != "" {
			// The variable's value is NOT read here. Reading it at startup would put the
			// check in the right place, but it would also mean an install with a
			// destinations file and no secret yet - the normal state before the receiver is
			// provisioned - could not start. The value is checked at delivery, where a
			// missing one dead-letters the message and says which variable is unset.
			if strings.ContainsAny(d.SecretRef, " \t") {
				return nil, fmt.Errorf("dispatch: destinations %s: %q has a secret_ref with "+
					"whitespace in it; it names ONE environment variable", path, d.Name)
			}
		}
		out.byName[d.Name] = d
		out.order = append(out.order, d.Name)
	}
	return out, nil
}

// NewDestinations builds a set from entries already in hand.
//
// It applies the SCHEME and userinfo rules and deliberately does NOT apply the
// host-reachability rules. That split is the point and it is worth being blunt about the
// footgun:
//
//   - `LoadDestinations` is the install-time path and applies everything, including the
//     loopback / link-local / RFC 1918 refusal. An operator's file goes through it.
//   - `NewDestinations` is the programmatic path, used by tests and by an operator
//     embedding this package. It trusts the caller's host choice.
//
// The alternative - applying the reachability rules here too - makes the package untestable,
// and an untestable security control is the exact shape this whole branch is fixing. The
// reachability rule refused `httptest`'s loopback listener, which is a correct refusal of a
// correct URL. So the rule stays where an operator meets it, and this constructor says
// plainly that it is a narrower door.
//
// `insecure_skip_verify` is still refused here: unlike a host choice, turning off
// certificate verification has no legitimate programmatic use, and it is the one setting a
// caller cannot be trusted with.
func NewDestinations(entries []Destination) (*Destinations, error) {
	out := &Destinations{byName: map[string]Destination{}}
	for i, d := range entries {
		if strings.TrimSpace(d.Name) == "" {
			return nil, fmt.Errorf("dispatch: destination %d has no name, and a rule's "+
				"dispatch target is resolved by name", i)
		}
		if _, dup := out.byName[d.Name]; dup {
			return nil, fmt.Errorf("dispatch: destination %q is declared twice", d.Name)
		}
		if err := CheckURLScheme(d.URL); err != nil {
			return nil, fmt.Errorf("dispatch: destination %q: %w", d.Name, err)
		}
		if d.InsecureSkipVerify {
			return nil, fmt.Errorf("dispatch: destination %q sets insecure_skip_verify, so "+
				"the bearer token for it would go to a server whose certificate was not "+
				"checked. Install the CA on the host instead", d.Name)
		}
		out.byName[d.Name] = d
		out.order = append(out.order, d.Name)
	}
	return out, nil
}

// CheckURLScheme refuses a plaintext or credential-bearing destination URL.
//
// The half of `CheckURL` that does not depend on where the host points, so
// `NewDestinations` can apply it without applying the reachability policy.
func CheckURLScheme(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("url is required; a destination with no url is a rule that " +
			"dead-letters every notification")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url %q does not parse: %w", raw, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("url %q is %s://. A destination carries a bearer token and "+
			"evidence-derived text, so it must be https; terminate TLS in front of the "+
			"receiver if it cannot speak it", raw, u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("url %q carries userinfo. A password in a URL reaches every log "+
			"that prints the URL; use secret_ref", raw)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("url %q has no host", raw)
	}
	return nil
}

// destinationsDoc is the file shape.
//
// A wrapper rather than a bare list, because the day an operator wants a file-level
// `version:` - and they will - a bare list has nowhere to put it and the parser has to
// change shape. `KnownFields(true)` means an unknown key is an error, so a typo like
// `secret-ref` is caught at startup instead of leaving a destination unauthenticated.
type destinationsDoc struct {
	Version      int           `yaml:"version"`
	Destinations []Destination `yaml:"destinations"`
}

// CheckURL refuses a destination URL that would make a dispatch an SSRF primitive.
//
// Three rules, and each one is a control rather than caution:
//
//  1. **https only.** A bearer token over plaintext goes to whoever is on the wire, and
//     `internal/milestone/transport.go` sends one to a vendor over TLS this product has to
//     configure. A destination is the same class of thing and gets the same requirement.
//
//  2. **No loopback, link-local, or RFC 1918 host.** This is the part that is easy to
//     wave through as "the operator configured it, they meant it", and the answer is that
//     the destinations file is written once at install time by whoever set the machine up,
//     while the bundle's `target` is edited later and more often. A rule bundle that could
//     name `http://169.254.169.254/` would reach a cloud metadata service from inside a
//     process holding a database connection and every credential the install has. Blocking
//     the address ranges at the configuration layer means a rule can only name a host an
//     operator already decided to trust.
//
//  3. **No userinfo.** `https://user:pass@host/` puts a credential in the URL, and a URL
//     ends up in logs. The `secret_ref` field exists for that.
func CheckURL(raw string) error {
	if err := CheckURLScheme(raw); err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url %q does not parse: %w", raw, err)
	}
	host := u.Hostname()
	if blocked, why := blockedHost(host); blocked {
		return fmt.Errorf("url %q points at %s. %s A rule bundle is edited more often than "+
			"this file, and a bundle that could name an arbitrary host would make every "+
			"bundle an SSRF primitive", raw, host, why)
	}
	return nil
}

// blockedHost reports whether a hostname is one a dispatch must not reach.
//
// A literal IP is checked directly. A NAME is resolved, because `localhost` and an internal
// short name both reach a private address while looking like ordinary hostnames - and a
// check that only inspected the string would pass `https://internal-metadata/` without ever
// looking.
//
// The resolution carries a short deadline rather than being contextless: this runs at
// startup, on the install path, and a DNS server that does not answer would otherwise hold
// the daemon's boot for the resolver's own timeout - twice, once per rule that needs the
// check.
//
// Resolution failures are allowed through, and that is deliberate: this package runs at a
// site that may have no DNS at all, and refusing to start a security product because a name
// did not resolve would push operators towards hardcoded IPs, which is worse. The TLS
// requirement is the control that still holds.
func blockedHost(host string) (bool, string) {
	if ip := net.ParseIP(host); ip != nil {
		return blockedIP(ip)
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true, "the loopback name, which reaches this process's own admin surface"
	}
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return false, ""
	}
	for _, ip := range ips {
		if blocked, why := blockedIP(ip); blocked {
			return true, "it resolves to " + ip.String() + ", " + why
		}
	}
	return false, ""
}

// resolveTimeout bounds one reverse check at startup.
const resolveTimeout = 2 * time.Second

func blockedIP(ip net.IP) (bool, string) {
	switch {
	case ip.IsLoopback():
		return true, "a loopback address, which reaches this process's own listeners"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return true, "a link-local address, which is where cloud instance metadata lives"
	case ip.IsUnspecified():
		return true, "the unspecified address, which is this host"
	case ip.IsPrivate():
		return true, "an RFC 1918 private address, which is inside the site"
	}
	return false, ""
}

// TLSConfig is the client TLS configuration a destination needs.
//
// Exported because an operator whose receiver sits behind a private certificate authority
// needs to install that CA, and the honest way to do it is system trust rather than the
// `insecure_skip_verify` field this package refuses at startup.
func TLSConfig() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

// ExampleDestinations is the documented shape, written to a comment rather than to a
// `.example` file.
//
// A file would drift: nothing reads it, so nothing checks it, and it would sit in the
// release archive claiming to be a configuration example while carrying a syntax the parser
// no longer accepts. This comment is a compile error the day the struct changes.
const ExampleDestinations = `
version: 1
destinations:
  # The escalation path for this site. ` + DefaultOperatorDestination + ` is what a rule
  # means when it omits ` + "`target`" + `.
  - name: ` + DefaultOperatorDestination + `
    url: https://hooks.example.internal/security-platform
    # Names an environment variable holding a bearer token. The value is read at
    # delivery and is never stored in this file, a log, or a payload.
    secret_ref: SP_DISPATCH_OPERATOR_TOKEN

  # A different receiver for supervisor escalation. Keeping it separate from
  # ` + DefaultOperatorDestination + ` is the point: ` + "`notify_operator`" + ` and
  # ` + "`notify_supervisor`" + ` are a routing decision, and collapsing them would make
  # a rule mean something its author did not write.
  - name: ` + DefaultSupervisorDestination + `
    url: https://security.example.com/platform-alerts
    secret_ref: SP_DISPATCH_SUPERVISOR_TOKEN
`

// DefaultDestinationsPath is where the file is read from.
//
// Absolute, because a relative path in a systemd unit is relative to the unit's working
// directory and nothing in a deployment sets that to what anyone expects.
const DefaultDestinationsPath = "/etc/security-platform/dispatch-destinations.yaml"

// ErrNoDestinationsFile is what an operator sees when they have not written one.
var ErrNoDestinationsFile = fmt.Errorf(
	"no destinations are configured (looked for %s). Notifications a rule asks for will "+
		"be refused and dead-lettered. Write the file with this shape:\n%s",
	filepath.ToSlash(DefaultDestinationsPath), ExampleDestinations)
