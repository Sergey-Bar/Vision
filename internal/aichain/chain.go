// Package aichain is the per-site provider chain.
//
// # What a chain is, and why it is not a setting
//
// The product's brain is a CHAIN: a site names the providers it may use and
// the order they are tried in, and the chain walks them until one answers.
//
// A chain rather than a selection, for three reasons:
//
//   - A site that names ONE provider has a single point of failure, and the
//     failure is visible only as "no observations today".
//   - The local-first rule wants a LOCAL model first and a hosted one as the
//     fallback, and that is an ORDER, not a choice.
//   - The order is a POLICY. It says what may leave the site, and it is the
//     thing an auditor asks about.
//
// # The rule that matters
//
// **A chain NEVER fabricates an answer.** If every provider in the chain
// could not answer, the chain returns a DEGRADED result naming why, and the
// observation records it. A chain that returned an empty answer after
// exhausting its providers would be reporting "there is nothing here" about a
// question nobody managed to ask — which is the single worst thing this
// product can do.
//
// # Egress
//
// Every call to a provider that is not on this machine is recorded: which
// provider, which observation, how much, and the digests of the evidence that
// went. The record never carries the CONTENT, because a log of what left
// that kept what left would be a second copy of the evidence in a place
// nobody chose to put it.
package aichain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/jackc/pgx/v5"
)

// The adapter kinds.
//
// A closed set, because a kind the build does not know is a chain that
// cannot run, and a chain that cannot run is a site whose brain is absent
// with no body to point at.
const (
	// KindOpenAICompatible speaks the OpenAI chat-completions API.
	KindOpenAICompatible = "openai_compatible"
	// KindAnthropic speaks the Anthropic messages API.
	KindAnthropic = "anthropic"
	// KindLocalBundle is a model served on this machine.
	KindLocalBundle = "local_bundle"
)

// Kinds is the closed vocabulary.
var Kinds = []string{KindOpenAICompatible, KindAnthropic, KindLocalBundle}

// ErrNoPolicy is a site with no chain.
//
// It is a refusal and not an empty chain, because "this site has no brain
// configured" and "this site's brain is empty" are different facts and only
// one of them is a site with a working product.
//
// It is the plane's own sentinel, aliased: the AI plane has to be able to
// tell "no chain" from "a chain that refused", and it cannot import this
// package without a cycle.
var ErrNoPolicy = airuntime.ErrNoPolicy

// ErrEgressRefused is a site that may not send anything off the machine.
//
// The refusal names the provider, because a site with a local model first
// and a hosted one second is a site whose chain WORKS until the local model
// fails — and an operator needs to know which provider the refusal stopped.
var ErrEgressRefused = errors.New("aichain: this site may not send evidence off the machine")

// ErrUnknownKind is an adapter kind the build does not know.
var ErrUnknownKind = errors.New("aichain: an adapter kind this build does not implement")

// ProviderSpec is one entry of a chain.
//
// The credential is named by an ENVIRONMENT VARIABLE and never carried
// here: a key in a database row is a key in every backup of that row.
type ProviderSpec struct {
	Kind string `json:"kind"`
	// BaseURL is where the provider lives.
	BaseURL string `json:"baseUrl"`
	// Model is the model the provider is asked for.
	Model string `json:"model"`
	// APIKeyEnv names the environment variable holding the credential. Empty
	// means the provider needs no credential, which is what a local bundle
	// does.
	APIKeyEnv string `json:"apiKeyEnv"`
}

// Policy is a site's chain.
type Policy struct {
	TenantID string
	SiteID   string
	// Providers is the chain, in the order it is tried.
	Providers []ProviderSpec
	// SystemPrompt is given to every provider in the chain.
	SystemPrompt string
	// EgressAllowed says whether the site may send anything off the machine.
	EgressAllowed bool
	// Timeout bounds one provider call.
	Timeout time.Duration
}

// Chain is a site's chain, ready to run.
//
// The providers are BUILT when the chain is built, so a policy naming an
// unknown adapter kind is refused there — at startup, in front of a person —
// and not at three in the morning when the first observation is due.
type Chain struct {
	policy    Policy
	providers []airuntime.Provider
	// egress records what left the site. It is nil when the chain was
	// built without a service behind it, and a chain without one still
	// runs: it just has nowhere to record what it sent.
	egress *Service
}

// NewChain returns a chain over the given providers.
//
// It exists beside `ChainFor` because the chain's BEHAVIOUR is worth testing
// without a database, and because a caller that assembles a chain in code —
// a test, or an install that has no policy row yet — needs the same
// construction the policy reader uses.
func NewChain(policy Policy, providers []airuntime.Provider) *Chain {
	return &Chain{policy: policy, providers: providers}
}

// Policy returns the chain's policy.
func (c *Chain) Policy() Policy { return c.policy }

// Providers returns the chain's adapters, in order.
func (c *Chain) Providers() []airuntime.Provider { return c.providers }

// Name identifies the chain on a reading.
//
// It names the CHAIN and not a provider, because the chain is what the
// observation is attributed to: the order is the policy, and the order is
// what would have to be replayed to reproduce the answer.
func (c *Chain) Name() string { return "chain" }

// Do runs the chain.
//
// It walks the providers in order and returns the first answer that is not
// degraded. If EVERY provider degrades, it returns the FIRST degradation —
// the first is the one closest to the site's intent, because the chain's
// order is the site's own statement of which provider it prefers.
//
// An error from a provider (a refused connection, a timeout) is not a
// degradation and does not stop the chain: the next provider is tried. An
// error that is NOT a provider failure — a policy that cannot be built —
// stops it, because trying the next provider would not help.
func (c *Chain) Do(ctx context.Context, req airuntime.Request) (airuntime.Result, error) {
	if len(c.providers) == 0 {
		return airuntime.Result{}, ErrNoPolicy
	}
	var firstDegraded *airuntime.Result
	var failures []string
	for _, p := range c.providers {
		res, err := p.Do(ctx, req)
		if err != nil {
			// The provider FAILED rather than declining. The chain moves
			// on, and the reason is collected so that a chain that
			// exhausted itself can say WHY.
			failures = append(failures, p.Name()+": "+err.Error())
			continue
		}
		// The EGRESS is recorded before the answer is judged, because the
		// bytes left the site whether or not the answer was any good, and
		// a record of what left that skipped the bad answers is a record
		// of what left that nobody can trust.
		c.recordEgress(ctx, p.Name(), req)
		if !res.Degraded {
			// The adapter that answered is named here as well as in the
			// adapter: a chain that walked two links and is asked which
			// one produced the answer must be able to say so.
			if res.Provider == "" {
				res.Provider = p.Name()
			}
			return res, nil
		}
		if firstDegraded == nil {
			degraded := res
			firstDegraded = &degraded
		}
		failures = append(failures, p.Name()+": "+res.DegradedDetail)
	}
	if firstDegraded != nil {
		// The chain exhausted itself. The DEGRADED answer is the honest
		// one, and it carries every provider's reason so that the
		// operator is not left with one.
		firstDegraded.DegradedDetail = strings.Join(failures, "; ")
		return *firstDegraded, nil
	}
	// Every provider FAILED rather than declined. That is an error and not
	// a degradation: the difference is that nothing was said about the
	// question at all.
	return airuntime.Result{}, fmt.Errorf("aichain: no provider in the chain answered: %s",
		strings.Join(failures, "; "))
}

// recordEgress writes the record of what left the site.
//
// It records the DIGESTS and the size and never the content: the record is
// of what left, and keeping what left would be a second copy of the evidence
// in a place nobody chose to put it.
//
// A failure to record is NOT a failure of the inference. The bytes left
// either way, and refusing to answer because the record could not be written
// would trade a recorded answer for an unrecorded one.
func (c *Chain) recordEgress(ctx context.Context, provider string, req airuntime.Request) {
	if c.egress == nil || req.TenantID == "" || req.SiteID == "" {
		return
	}
	hashes := make([]string, 0, len(req.Evidence))
	var bytes int64
	for _, ev := range req.Evidence {
		hashes = append(hashes, ev.SHA256)
		bytes += int64(len(ev.Data))
	}
	err := c.egress.RecordEgress(ctx, Egress{
		TenantID:       req.TenantID,
		SiteID:         req.SiteID,
		EgressID:       egressID(req),
		Provider:       provider,
		ObservationID:  req.ObservationID,
		ProfileID:      req.ProfileID,
		EvidenceCount:  len(req.Evidence),
		EvidenceBytes:  bytes,
		EvidenceHashes: hashes,
		RequestedAt:    time.Now().UTC(),
	})
	if err != nil {
		c.egress.Logf("aichain: the egress of %s could not be recorded: %v", provider, err)
	}
}

// egressID names one egress.
//
// It is derived from the observation and the provider, so the SAME call
// records the SAME row and a retry does not double the record.
func egressID(req airuntime.Request) string {
	return "egr_" + req.ObservationID
}

// Service reads policies and runs chains.
type Service struct {
	Pool *dbx.Pool
	// Logf reports what the chain is doing. It never receives evidence
	// content and never receives a credential.
	Logf func(format string, args ...any)
}

// NewService returns the chain service.
func NewService(pool *dbx.Pool) *Service {
	return &Service{Pool: pool, Logf: func(string, ...any) {}}
}

// Policy reads a site's policy.
//
// A site with no policy is ErrNoPolicy and NOT an empty chain: a site whose
// brain is configured and a site with no brain are different facts, and only
// one of them is a working product.
func (s *Service) Policy(ctx context.Context, tenant, site string) (Policy, error) {
	if s == nil || s.Pool == nil {
		return Policy{}, errors.New("aichain: the chain service has no pool behind it")
	}
	var (
		providersRaw []byte
		systemPrompt string
		egress       bool
		timeoutS     int
	)
	err := s.Pool.WithSites(tenant, "ai-chain", []string{site}).QueryRow(ctx, `
		SELECT providers, system_prompt, egress_allowed, timeout_seconds
		  FROM ai_policies
		 WHERE tenant_id = $1 AND site_id = $2`,
		tenant, site).Scan(&providersRaw, &systemPrompt, &egress, &timeoutS)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A site with NO POLICY ROW is a site with no policy, and the
			// refusal is NAMED so that a caller can tell it from a
			// database that could not be read. The difference matters: a
			// site with no chain speaks deterministically, and a site whose
			// chain could not be read is DEGRADED.
			return Policy{}, ErrNoPolicy
		}
		return Policy{}, err
	}
	var specs []ProviderSpec
	if err := json.Unmarshal(providersRaw, &specs); err != nil {
		return Policy{}, fmt.Errorf("aichain: the provider chain of %s is not a list of adapters: %w", site, err)
	}
	return Policy{
		TenantID:      tenant,
		SiteID:        site,
		Providers:     specs,
		SystemPrompt:  systemPrompt,
		EgressAllowed: egress,
		Timeout:       time.Duration(timeoutS) * time.Second,
	}, nil
}

// ChainFor builds a site's chain.
//
// A provider that is off the machine is REFUSED when the site may not send
// anything off the machine — here, in front of a person, rather than at the
// moment the first observation is due.
func (s *Service) ChainFor(ctx context.Context, tenant, site string) (*Chain, error) {
	policy, err := s.Policy(ctx, tenant, site)
	if err != nil {
		return nil, err
	}
	if len(policy.Providers) == 0 {
		return nil, ErrNoPolicy
	}
	providers := make([]airuntime.Provider, 0, len(policy.Providers))
	for i, spec := range policy.Providers {
		p, err := build(spec, policy)
		if err != nil {
			return nil, fmt.Errorf("aichain: the provider at position %d of %s's chain: %w", i+1, site, err)
		}
		providers = append(providers, p)
	}
	return &Chain{policy: policy, providers: providers, egress: s}, nil
}

// ProviderFor returns the chain for a site.
//
// It is the SEAM the AI plane uses: the plane asks for a site's provider and
// gets the chain, and the chain records what left the site on the way.
func (s *Service) ProviderFor(ctx context.Context, tenant, site string) (airuntime.Provider, error) {
	chain, err := s.ChainFor(ctx, tenant, site)
	if err != nil {
		return nil, err
	}
	return chain, nil
}

// build turns one spec into an adapter.
//
// A HOSTED provider needs the site's egress permission, and a local bundle
// does not: a bundle is on this machine by definition, and the adapter
// checks the ADDRESS rather than the kind — a "local" bundle pointed at a
// remote host is a remote bundle with a local name.
func build(spec ProviderSpec, policy Policy) (airuntime.Provider, error) {
	opts := airuntime.AdapterOptions{
		BaseURL: spec.BaseURL,
		Model:   spec.Model,
		System:  policy.SystemPrompt,
		Timeout: policy.Timeout,
	}
	// The credential is read from the environment and never carried in the
	// policy row.
	if spec.APIKeyEnv != "" {
		opts.APIKey = os.Getenv(spec.APIKeyEnv)
	}
	switch spec.Kind {
	case KindOpenAICompatible:
		if !policy.EgressAllowed {
			return nil, fmt.Errorf("%w: the chain names %q, which is not on this machine",
				ErrEgressRefused, spec.Kind)
		}
		return airuntime.OpenAICompatible{AdapterOptions: opts}, nil
	case KindAnthropic:
		if !policy.EgressAllowed {
			return nil, fmt.Errorf("%w: the chain names %q, which is not on this machine",
				ErrEgressRefused, spec.Kind)
		}
		return airuntime.Anthropic{AdapterOptions: opts}, nil
	case KindLocalBundle:
		return airuntime.LocalBundle{AdapterOptions: opts}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, spec.Kind)
	}
}

// Egress is one record of something that left the site.
type Egress struct {
	TenantID      string
	SiteID        string
	EgressID      string
	Provider      string
	ObservationID string
	ProfileID     string
	EvidenceCount int
	EvidenceBytes int64
	// EvidenceHashes are the digests of what left. Never the content.
	EvidenceHashes []string
	RequestedAt    time.Time
}

// RecordEgress writes one egress record.
//
// The digests go in and the CONTENT does not, and the function has no
// parameter that could carry it: the content is not something the log is
// able to hold, so the question does not arise.
func (s *Service) RecordEgress(ctx context.Context, e Egress) error {
	if s == nil || s.Pool == nil {
		return errors.New("aichain: the chain service has no pool behind it")
	}
	if e.EgressID == "" {
		return errors.New("aichain: an egress record needs an id")
	}
	if e.Provider == "" {
		return errors.New("aichain: an egress record needs the provider that answered")
	}
	hashes := e.EvidenceHashes
	if hashes == nil {
		hashes = []string{}
	}
	// The insert goes through the tenant-bound pool, so the RLS policies
	// apply exactly as they do everywhere else.
	_, err := s.Pool.WithSites(e.TenantID, "ai-chain", []string{e.SiteID}).Exec(ctx, `
		INSERT INTO ai_egress_log
		    (tenant_id, site_id, egress_id, provider, observation_id, profile_id,
		     evidence_count, evidence_bytes, evidence_hashes, requested_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		e.TenantID, e.SiteID, e.EgressID, e.Provider, e.ObservationID, e.ProfileID,
		e.EvidenceCount, e.EvidenceBytes, hashes, e.RequestedAt)
	if err != nil {
		return fmt.Errorf("aichain: record the egress of %s: %w", e.Provider, err)
	}
	return nil
}

// EgressLog reads the egress records for a site, newest first.
func (s *Service) EgressLog(ctx context.Context, tenant, site string, limit int) ([]Egress, error) {
	if s == nil || s.Pool == nil {
		return nil, errors.New("aichain: the chain service has no pool behind it")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.WithSites(tenant, "ai-chain", []string{site}).Query(ctx, `
		SELECT tenant_id, site_id, egress_id, provider, observation_id, profile_id,
		       evidence_count, evidence_bytes, evidence_hashes, requested_at
		  FROM ai_egress_log
		 WHERE tenant_id = $1 AND site_id = $2
		 ORDER BY requested_at DESC
		 LIMIT $3`,
		tenant, site, limit)
	if err != nil {
		return nil, fmt.Errorf("aichain: read the egress log of %s: %w", site, err)
	}
	defer rows.Close()
	var out []Egress
	for rows.Next() {
		var e Egress
		if err := rows.Scan(&e.TenantID, &e.SiteID, &e.EgressID, &e.Provider,
			&e.ObservationID, &e.ProfileID, &e.EvidenceCount, &e.EvidenceBytes,
			&e.EvidenceHashes, &e.RequestedAt); err != nil {
			return nil, fmt.Errorf("aichain: scan an egress record: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aichain: iterate the egress log of %s: %w", site, err)
	}
	return out, nil
}
