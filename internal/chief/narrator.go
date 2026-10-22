package chief

import (
	"context"
	"time"
)

// Narrator is the model-shaped boundary.
//
// This is the same shape `internal/investigate` uses, and for the same
// reason: the retrieval and the narration are different jobs with different
// failure modes, and the one that must not fail the other is an interface.
//
// The interface is HERE and not in the package that talks to a model, because
// the deterministic core must not be able to reach a model by accident. A
// package that can import the AI plane can acquire a dependency on it, and
// the dependency is what turns an optional second opinion into a required
// one. The Chief's render is pure and reaches no model; the narration is an
// INPUT to it.
//
// # What the narrator may say
//
// The request carries the facts, and a narration that cites anything else is
// refused by `Render`. That is not a politeness in the prompt and not a
// request to the model: it is a check the product makes, on the answer, after
// the fact. A model that is told the rules is usually right. A model that is
// CHECKED is right.
type Narrator interface {
	// Narrate answers over the facts it was given.
	//
	// The error is for a narrator that could not answer AT ALL. A narrator
	// that answered with a degraded answer returns a Narration whose
	// attribution names the degradation, and the caller decides what to do
	// with it; the two are different failures and the product must not
	// collapse them.
	Narrate(ctx context.Context, req NarrationRequest) (*Narration, error)
}

// NarrationRequest is what the Chief hands the narrator.
//
// The Facts are the CLOSED SET of things the answer may be about. The Chief
// builds it from the same facts it renders, so the check in `Render` cannot
// be satisfied by a fact the Chief does not itself hold.
type NarrationRequest struct {
	// TenantID and SiteID are the scope.
	TenantID string
	SiteID   string
	// Since is the cursor: what the answer is about.
	Since time.Time
	// Headline is the deterministic one-liner. It is given to the narrator
	// as the thing to expand on, not as a fact to repeat — the narrator is
	// told what the product already knows so it does not have to re-derive
	// it, and it is told it may only add voice.
	Headline string
	// Facts is the closed set the answer may cite.
	Facts []Fact
}

// FactsFrom builds the request's fact list from the queue and the brief.
//
// It is exported because the API assembles the facts and the narrator needs
// them, and because there must be ONE way to build them: two ways is two
// opinions about what the Chief knows.
func FactsFrom(items []Item, cameras []string, zones []string, openItems []string) []Fact {
	facts := make([]Fact, 0, len(items)+len(cameras)+len(zones)+len(openItems))
	for _, it := range items {
		facts = append(facts, Fact{
			ID:   it.IncidentID,
			Kind: factIncident,
			Text: describe(it),
		})
	}
	for _, c := range cameras {
		facts = append(facts, Fact{ID: c, Kind: factCamera, Text: "camera " + c})
	}
	for _, z := range zones {
		facts = append(facts, Fact{ID: z, Kind: factZone, Text: "zone " + z})
	}
	for _, o := range openItems {
		facts = append(facts, Fact{ID: o, Kind: factOpenItem, Text: "open item " + o})
	}
	return facts
}
