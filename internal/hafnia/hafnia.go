// Package hafnia is the second AI provider: a remote video-language service
// reached over HTTPS, with clips uploaded as assets, a prompt run against them,
// and the asset deleted afterwards.
//
// It implements airuntime.Provider, which is the whole of the interface between
// "something that answers a question about evidence" and the product. It is
// deliberately not a Milestone thing, and a neutrality test asserts that nothing
// outside the vendor package imports a vendor package.
//
// # Three things make a hosted VLM different from the local runtime
//
//  1. It is a **processor**, and the clip leaving the site is the moment that
//     happens. Uploading is explicit, counted and deletable - not an
//     implementation detail of a call.
//  2. It has **regions**, and a site declares one. Cross-region failover is
//     refused by name: during an outage a silent failover is a sovereignty
//     breach, not a degraded mode.
//  3. It gives **no weights to hash** and no determinism, so an observation
//     cannot honestly carry a model hash. See [Client.Attribution].
//
// # Attribution, and what is deliberately not invented
//
// The local runtime is a Python process whose model bundle has a content
// signature, so an observation about it can name a model hash. A hosted VLM
// returns a model identifier and a response and nothing else: its weights are
// not ours, are not addressable, and change on the provider's schedule.
//
// So this client does not invent a hash. It reports what the API actually said
// ([Attribution.ModelIdentity]), what this build pinned around the call
// ([Attribution.ModelContractVersion]: a content hash over the prompt template,
// the prompt version, the output schema version and the API model name), and the
// temperature it asked for. `ModelHash` is left empty, and
// observations.Validate accepts an empty ModelHash only when those two are
// present. A fabricated hash would satisfy a column check and defeat the one
// thing the column is for.
package hafnia

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Region is where a site is allowed to send footage.
//
// A closed set on purpose. A free-form region string is a site that silently
// resolves to wherever a DNS name points, which is the opposite of pinning.
type Region string

// The regions this build can pin to.
const (
	RegionEU Region = "eu"
	RegionUS Region = "us"
)

// ErrUnknownRegion is returned for a region this build does not operate.
var ErrUnknownRegion = errors.New("hafnia: unknown region")

// ErrCrossRegion is returned when a request would leave the pinned region.
//
// This error is the point of the whole region mechanism. The alternative -
// failing over to the other region when the pinned one is unavailable - turns an
// outage into a regulatory incident, and it does it silently, at the worst
// possible moment, to whoever was unlucky enough to be up. A site whose pinned
// region is unreachable gets a named degradation and keeps its evidence on site.
var ErrCrossRegion = errors.New("hafnia: refusing a call that would cross the pinned region")

// ErrNoPrompt is returned when no prompt template was supplied.
//
// The prompt is a required input rather than a constant in this package, for two
// reasons. The obvious one is that the provider's own documented prompt is the
// provider's to publish and this package must not paraphrase it into something
// that merely resembles it. The load-bearing one is that the prompt is the
// largest single determinant of the answer, and an answer whose prompt is a
// compile-time constant is an answer nobody can point at when it is wrong.
var ErrNoPrompt = errors.New("hafnia: no prompt template configured")

// regionHosts maps each region to the single host this build will talk to.
//
// This map is the egress allow-list and it is exhaustive: a host not in it is
// not a destination this client can reach, so neither a mistyped configuration
// nor a redirect can turn the upload path into an exfiltration endpoint. An
// operator-configurable base URL would be SSRF with a config file.
var regionHosts = map[Region]string{
	RegionEU: "api.eu.hafnia.example",
	RegionUS: "api.us.hafnia.example",
}

// Host returns the pinned host for a region.
func (r Region) Host() (string, error) {
	h, ok := regionHosts[r]
	if !ok {
		return "", fmt.Errorf("%w: %q; this build carries %v", ErrUnknownRegion, r, KnownRegions())
	}
	return h, nil
}

// KnownRegions lists the regions this build can pin to, sorted so a diagnostic
// reads the same on every machine.
func KnownRegions() []Region {
	out := make([]Region, 0, len(regionHosts))
	for r := range regionHosts {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ParseRegion reads a region from configuration, refusing anything outside the
// closed set.
func ParseRegion(s string) (Region, error) {
	r := Region(strings.ToLower(strings.TrimSpace(s)))
	if r == "" {
		return "", fmt.Errorf("%w: no region declared; a site declares where its footage may go", ErrUnknownRegion)
	}
	if _, err := r.Host(); err != nil {
		return "", err
	}
	return r, nil
}

// Config configures a Client.
type Config struct {
	// Region is the pinned region. Required, with no default: a default here is
	// a site whose footage went somewhere nobody chose.
	Region Region

	// APIKey authenticates to the provider. It comes from the secret store and
	// never from a URL, a flag or a file in the repository.
	APIKey string

	// PromptTemplate is the provider's documented prompt, supplied verbatim by
	// the operator. Required - see [ErrNoPrompt].
	PromptTemplate string

	// PromptVersion is the operator's label for this prompt revision. It is part
	// of the contract version, so changing the prompt changes the version and a
	// reviewer can see which prompt produced which answer.
	PromptVersion string

	// OutputSchemaVersion is this build's versioned profile schema. It is part
	// of the contract version for the same reason.
	OutputSchemaVersion int

	// APIModelName is the model identifier sent in the request.
	APIModelName string

	// Temperature is the sampling temperature asked for. Zero takes
	// [DefaultTemperature], the provider's lowest.
	Temperature float64

	// Timeout bounds one call, upload included.
	Timeout time.Duration

	// AllowInsecureSkipVerify disables TLS verification. It exists so a site
	// behind a private certificate authority can be reached, it defaults to off,
	// and it is named for what it does.
	AllowInsecureSkipVerify bool

	// DailyQueryCap is the most inference calls this client will make per site
	// per UTC day. Zero means no local cap, which is not the same as "no limit":
	// the provider has one, and the plan's cost argument depends on the local
	// one existing.
	DailyQueryCap int

	// Transport overrides the HTTP client. It exists so a test can point at an
	// httptest server without a production-only allow-list bypass.
	Transport Doer

	// Now is the clock. It is injected so the query cap can be tested against a
	// day boundary without waiting for one.
	Now func() time.Time
}

// Doer is the slice of *http.Client this package uses.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DefaultTemperature is the lowest the API accepts.
//
// It is a small positive number rather than zero because zero is not the same as
// lowest at every provider, and silently asking for a value the API clamps would
// mean the recorded temperature is not the one that ran - the same class of error
// as recording a model hash we do not have.
const DefaultTemperature = 0.01

// Attribution is what this build pins around every call.
type Attribution struct {
	// ModelIdentity is what the API reported about the model that answered. It
	// is empty until a call has returned, and it is a claim by the provider, not a
	// verification by us.
	ModelIdentity string
	// ModelContractVersion is this build's content hash over the prompt
	// template, the prompt version, the output schema version and the API model
	// name. It is reproducible from the repository, which is what makes it worth
	// more than a model hash we could not have computed anyway.
	ModelContractVersion string
	// Temperature is what was asked for.
	Temperature float64
}

// Client is one configured connection to the provider.
//
// Safe for concurrent use: the quota and the asset ledger are mutex-guarded,
// because both are shared budget and a data race on either means two workers each
// believing there was room for one more.
type Client struct {
	cfg    Config
	host   string
	region Region
	http   Doer
	quota  *quota
	assets *assetLedger
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

// New builds a client for a region.
//
// The configuration is validated here rather than at the first call, so a
// misconfiguration is a startup failure with a legible message rather than a
// degraded observation at three in the morning.
func New(cfg Config) (*Client, error) {
	host, err := cfg.Region.Host()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		// An unauthenticated call uploads the site's footage to a stranger's
		// account, so this is refused rather than sent and answered 401.
		return nil, errors.New("hafnia: no API key; the call would upload footage unauthenticated")
	}
	if strings.TrimSpace(cfg.PromptTemplate) == "" {
		return nil, ErrNoPrompt
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = DefaultTemperature
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	c := &Client{
		cfg:    cfg,
		host:   host,
		region: cfg.Region,
		quota:  newQuota(cfg.DailyQueryCap, now),
		assets: newAssetLedger(),
		now:    now,
		sleep:  sleepContext,
	}
	c.http = cfg.Transport
	if c.http == nil {
		c.http = newHTTPClient(cfg)
	}
	return c, nil
}

// Name satisfies airuntime.Provider. It is on the pointer, so *Client is the
// provider; see provider.go for why the assertion lives there.
func (c *Client) Name() string { return "hafnia" }

// Region returns the pinned region.
func (c *Client) Region() Region { return c.region }

// Host returns the pinned host, for a banner or a probe report.
func (c *Client) Host() string { return c.host }

// Attribution returns what this build pins around every call.
func (c *Client) Attribution() Attribution {
	return Attribution{
		ModelContractVersion: c.contractVersion(),
		Temperature:          c.cfg.Temperature,
	}
}

// contractVersion is the content hash over everything about the call that this
// build controls.
//
// The provider's model is not in it, and cannot be: it is not ours to pin. What
// is in it is everything that determines the shape of the answer - the prompt,
// the prompt's revision, the schema the answer is validated against, and the
// model name we asked for - so a reviewer asking "why did this answer come out
// like that" gets an answer that does not depend on the provider still serving
// the same model under the same name.
func (c *Client) contractVersion() string {
	sum := sha256.New()
	for _, part := range []string{
		"hafnia/1",
		c.cfg.PromptTemplate,
		c.cfg.PromptVersion,
		fmt.Sprintf("%d", c.cfg.OutputSchemaVersion),
		c.cfg.APIModelName,
		"region=" + string(c.cfg.Region),
	} {
		sum.Write([]byte(part))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Quota reports the site's current spend against the daily cap.
func (c *Client) Quota() QuotaStatus { return c.quota.status(c.now()) }

// OutstandingAssets lists the asset ids this client uploaded and has not been
// told to delete. A non-empty list after a clean run means footage is still on
// somebody else's disk.
func (c *Client) OutstandingAssets() []string { return c.assets.ids() }

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
