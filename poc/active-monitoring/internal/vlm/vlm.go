// Package vlm is the Hafnia VLM client and the strict output parser.
//
// It is the second half of the trigger: the local novelty engine measures a
// region (internal/novelty), this package asks a model what is happening in
// that region, and the answer is written back into XProtect as an event. Four
// properties are load-bearing and each has a test that removes it.
//
// # The answer is never a partial parse
//
// A malformed, prose, or absent answer is aistate.CannotTell and
// [VerdictNone]. It is never "a summary with no verdict", and never a default.
// The parser decodes with DisallowUnknownFields, requires all four fields to be
// present, and requires confidence to be in [0,1].
//
// # A missing answer is never a negative one
//
// [Observation.normalise] is the whole of that rule, and it is one function on
// purpose: a verdict is meaningful only when the ask was answered, so anything
// other than aistate.Asked clears the verdict, the confidence and the summary.
// A false_positive on a degraded ask is a fabricated negative, which is the
// worst failure this product can have — a camera operator is told "nothing
// happened" because we could not ask.
//
// # Region pinning, with no failover
//
// A site declares one region and footage may only leave for that region's host.
// There is deliberately no failover: during an outage a silent failover is a
// sovereignty breach, and it happens at the worst possible moment to whoever was
// unlucky enough to be up.
//
// # Attribution
//
// A hosted model has no weights to hash. This package reports what the API said
// (ModelIdentity, a claim by the provider), what this build pinned around the
// call (ContractVersion, a content hash over the prompt, the prompt's version,
// the output schema version and the model name), and the temperature it asked
// for. It never fabricates a model hash.
package vlm

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
// A closed set on purpose. A free-form region string is a site that resolves to
// wherever a DNS name points, which is the opposite of pinning.
type Region string

// The regions this build can pin to.
const (
	RegionEU Region = "eu"
	RegionUS Region = "us"
)

// ErrUnknownRegion is returned for a region this build does not operate.
var ErrUnknownRegion = errors.New("vlm: unknown region")

// ErrCrossRegion is returned when a request would leave the pinned region.
//
// This error is the point of the whole region mechanism. Failing over to the
// other region when the pinned one is unavailable turns an outage into a
// regulatory incident, and it does it silently.
var ErrCrossRegion = errors.New("vlm: refusing a call that would cross the pinned region")

// ErrNoPrompt is returned when no prompt template was supplied.
//
// The prompt is a required input rather than a constant in this package for two
// reasons. The obvious one is that the provider's documented prompt is the
// provider's to publish. The load-bearing one is that the prompt is the largest
// single determinant of the answer, and an answer whose prompt is a
// compile-time constant is an answer nobody can point at when it is wrong.
var ErrNoPrompt = errors.New("vlm: no prompt template configured")

// regionHosts maps each region to the single host this build will talk to.
//
// This map is the egress allow-list and it is exhaustive: a host not in it is
// not a destination this client can reach, so neither a mistyped configuration
// nor a redirect can turn the upload path into an exfiltration endpoint. An
// operator-configurable base URL would be SSRF with a config file.
//
// Recorded from the provider's published VLM API documentation
// (hafnia.readme.io/docs/vlm-api, retrieved 2026-09-30). The docs carry a
// historical disagreement about the EU host: an older revision published
// `api.hafnia.milestonesys.com` with no `eu.` label, and the 2026-09-08
// revision publishes `api.eu.hafnia.milestonesys.com`. Both spellings are
// recorded here and one live call settles which answers. The losing host is kept
// as a named negative rather than deleted, because "we tried this and it did not
// answer" is evidence and "we removed it" is not.
var regionHosts = map[Region]string{
	RegionEU: "api.eu.hafnia.milestonesys.com",
	RegionUS: "api.us.hafnia.milestonesys.com",
}

// supersededEUHost is the EU spelling an earlier revision of the provider's
// documentation published. It is never dialled and it is asserted absent from
// regionHosts by a test, so it cannot creep back in.
const supersededEUHost = "api.hafnia.milestonesys.com"

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

	// APIModelName is the model identifier sent in the request.
	APIModelName string

	// OutputSchemaVersion is this build's versioned verdict schema. It is part
	// of the contract version, so a schema change changes the contract version
	// and a reviewer can see which schema produced which answer.
	OutputSchemaVersion int

	// Temperature is the sampling temperature asked for. Zero takes
	// [DefaultTemperature], the provider's lowest.
	Temperature float64

	// Timeout bounds one call, upload included.
	Timeout time.Duration

	// DailyQueryCap is the most inference calls this client will make per site
	// per UTC day. Zero means no local cap, which is not the same as "no
	// limit": the provider has one, and the cost argument depends on the local
	// one existing.
	DailyQueryCap int

	// Transport overrides the HTTP client. It exists so a test can point at an
	// httptest server without a production-only allow-list bypass.
	Transport Doer

	// Now is the clock. It is injected so the query cap can be tested against a
	// day boundary without waiting for one.
	Now func() time.Time

	// Logf receives the rare event that has no other home: a delete that
	// failed, so a clip may still be on the provider's disk.
	Logf func(string, ...any)
}

// Doer is the slice of *http.Client this package uses.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DefaultTemperature is the lowest the API accepts.
//
// A small positive number rather than zero because zero is not the same as
// lowest at every provider, and silently asking for a value the API clamps would
// mean the recorded temperature is not the one that ran — the same class of error
// as recording a model hash we do not have.
const DefaultTemperature = 0.01

// Client is one configured connection to the provider.
//
// Safe for concurrent use: the quota, the asset ledger and the call counter are
// mutex-guarded, because two workers each believing there was room for one more
// is a cost bug and a privacy bug at the same time.
type Client struct {
	cfg     Config
	host    string
	region  Region
	http    Doer
	quota   *quota
	assets  *assetLedger
	now     func() time.Time
	calls   *callCounter
	prompts *PromptSet
	// logFn is optional and is used only for a delete that failed, which is the
	// one event in this package that must never be silent.
	logFn func(string, ...any)
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
		return nil, errors.New("vlm: no API key; the call would upload footage unauthenticated")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = DefaultTemperature
	}
	if cfg.OutputSchemaVersion <= 0 {
		cfg.OutputSchemaVersion = OutputSchemaVersion
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	prompts, err := EmbeddedPrompts()
	if err != nil {
		return nil, err
	}
	c := &Client{
		cfg:     cfg,
		host:    host,
		region:  cfg.Region,
		quota:   newQuota(cfg.DailyQueryCap, now),
		assets:  newAssetLedger(),
		now:     now,
		calls:   &callCounter{},
		prompts: prompts,
	}
	c.http = cfg.Transport
	if c.http == nil {
		c.http = newHTTPClient(cfg)
	}
	c.logFn = cfg.Logf
	return c, nil
}

// Prompts returns the embedded prompt set, so a caller can name a class without
// carrying a template around.
func (c *Client) Prompts() *PromptSet { return c.prompts }

// Name is the provider's name, as it appears in the report.
func (c *Client) Name() string { return "hafnia" }

// Region returns the pinned region.
func (c *Client) Region() Region { return c.region }

// Host returns the pinned host, for a banner or a probe report.
func (c *Client) Host() string { return c.host }

// Calls is the number of HTTP requests this client has made.
//
// It exists so a replay can be PROVEN to have made none. A replay that takes no
// client cannot call a provider by construction, and this counter is how that
// property is asserted rather than asserted-about.
func (c *Client) Calls() int64 { return c.calls.get() }

// Attribution is what this build pins around every call.
//
// It is not a method on the client, because two of its four fields are per-call:
// the contract version is a hash OVER THE PROMPT, and the prompt is chosen by
// the incident class. A client-level accessor would have to invent a prompt, and
// an attribution record with an invented prompt in it is worse than none. The
// record that gets written is [Observation].
type Attribution struct {
	// ModelIdentity is what the API reported about the model that answered. It
	// is empty until a call has returned, and it is a claim by the provider, not
	// a verification by us.
	ModelIdentity string
	// ContractVersion is this build's content hash over the prompt, the prompt's
	// version, the output schema version and the API model name. It is
	// reproducible from the repository, which is what makes it worth more than a
	// model hash we could not have computed anyway.
	ContractVersion string
	// Temperature is what was asked for.
	Temperature float64
	// ModelHash is deliberately absent from this struct. See the package
	// comment: a hosted model has no weights to hash, and a synthetic hash
	// defeats the only thing the field is for.
}

// Attribution returns what this build pins around every call, minus the two
// per-call fields. Callers that have a prompt fill those in with
// [ContractVersionFor].
func (c *Client) Attribution() Attribution {
	return Attribution{Temperature: c.cfg.Temperature}
}

// ContractVersionFor is the content hash over everything about the call that
// this build controls.
//
// The provider's model is not in it, and cannot be: it is not ours to pin. What
// is in it is everything that determines the shape of the answer — the prompt,
// the prompt's revision, the schema the answer is validated against, and the
// model name we asked for — so a reviewer asking "why did this answer come out
// like that" gets an answer that does not depend on the provider still serving
// the same model under the same name.
func ContractVersionFor(prompt Prompt, schemaVersion int, apiModel string, region Region) string {
	sum := sha256.New()
	for _, part := range []string{
		"hafnia/vlm/1",
		prompt.ID,
		prompt.Version,
		fmt.Sprintf("%d", schemaVersion),
		apiModel,
		"region=" + string(region),
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

// pinned reports whether host is the one this client may talk to.
//
// It is a method rather than an inline comparison so the red check can remove
// exactly this decision and nothing else around it.
func (c *Client) pinned(host string) bool { return host == c.host }

// requireRegion refuses a call whose destination is not the pinned one.
//
// The check lives here rather than at each call site so that no endpoint can be
// added later without passing through the allow-list.
func (c *Client) requireRegion(host string) error {
	if c.host == "" {
		return fmt.Errorf("%w: no pinned host", ErrCrossRegion)
	}
	if _, err := c.region.Host(); err != nil {
		return err
	}
	// REDCHECK RC-7: the only host this client may talk to is the pinned one.
	// Removing this comparison is what a cross-region egress looks like.
	if !c.pinned(host) {
		return fmt.Errorf("%w: %s is not the pinned host for region %s, which is %s", ErrCrossRegion, host, c.region, c.host)
	}
	return nil
}

// ask is the billable call. The reservation is already made.
//
// It is separate from [Client.Ask] so [Client.Observe] can reserve *before* the
// upload. That ordering is the whole point: reserving after the upload would mean
// a site that has reached its daily cap still ships footage to a third party on
// every event, for a call that is then refused. The cost argument and the privacy
// argument both require the budget to be consulted before anything leaves the
// site.
func (c *Client) ask(ctx context.Context, assetID string, prompt Prompt) (Observation, error) {
	if strings.TrimSpace(assetID) == "" {
		return Observation{}, ErrNoAsset
	}
	started := c.now()
	body, err := c.encodeCompletion(prompt, assetID)
	if err != nil {
		return Observation{}, err
	}
	resp, err := c.call(ctx, http.MethodPost, PathCompletions, "application/json", body)
	if err != nil {
		return Observation{}, err
	}
	defer drain(resp)

	out, err := parseAnswer(resp)
	if err != nil {
		return Observation{}, err
	}
	out.PromptID = prompt.ID
	out.PromptVersion = prompt.Version
	out.ContractVersion = ContractVersionFor(prompt, c.cfg.OutputSchemaVersion, c.cfg.APIModelName, c.cfg.Region)
	out.Temperature = c.cfg.Temperature
	out.AssetID = assetID
	out.LatencyMS = elapsedMS(started, c.now())
	return out, nil
}
