package airuntime

import (
	"context"
	"errors"
)

// Provider is anything that can answer a request for an inference.
//
// It is the whole abstraction the second AI provider needs, and it is here
// rather than in a new package because Request and Result are already the
// product's canonical inference vocabulary: they carry the attribution fields
// and they speak the Degradation enum, and a provider that invented its own
// would be a provider whose failures could not be named the same way as the
// first one's.
//
// It has one method on purpose. Everything a caller needs to do - construct a
// request, name a degradation, read a structured answer - is already expressed
// in the two types, so a second interface would be a second vocabulary for the
// same concepts, and the two would drift.
//
// The rule that matters: **a Provider never fabricates a negative.** If it could
// not answer, it returns a Result with Degraded set and a reason, or an error.
// Returning an empty Structured with Degraded false would be a fabricated "there
// is nothing here", which is the single worst thing this product can do.
type Provider interface {
	// Name identifies the provider on a reading, so a score or a verdict can be
	// traced to the thing that produced it.
	Name() string

	// Do performs one inference. It returns a Result even when the provider has
	// nothing to say, because "I could not answer" is a Result and not an
	// absence.
	Do(ctx context.Context, req Request) (Result, error)
}

// ProviderFor returns p as a Provider, refusing a nil one.
//
// A nil provider in this product's pipeline is not a stub, it is a
// misconfiguration that would otherwise surface as a nil-pointer panic inside a
// job worker at three in the morning.
func ProviderFor(p Provider) (Provider, error) {
	if p == nil {
		return nil, ErrNoEndpoint
	}
	return p, nil
}

// ErrNoPolicy is a site with no provider policy.
//
// It lives HERE rather than in the chain's package because the AI plane has
// to recognise it: a site with no chain is a site whose brain is the local
// runtime, and the plane falls back to it. A site WITH a chain that refuses
// is a refusal, and the two are different facts.
//
// The chain's package aliases it, so a caller of either package names the
// same value.
var ErrNoPolicy = errors.New("airuntime: the site has no provider policy")

// ChainProvider builds the provider for a SITE.
//
// It is the seam between the AI plane and the site's provider CHAIN, and it
// is declared HERE rather than in the chain's own package so that the plane
// does not import the chain and the chain does not import the plane.
//
// The interface is one method because that is all the plane needs: the chain
// is the thing that knows the order, and the plane is the thing that knows
// how to ask.
type ChainProvider interface {
	// ProviderFor returns the provider for a site.
	//
	// An error is a refusal and not an absence: a site with no policy is
	// a site with no brain, and that is a fact the caller records rather
	// than a nil provider it has to guess about.
	ProviderFor(ctx context.Context, tenant, site string) (Provider, error)
}
