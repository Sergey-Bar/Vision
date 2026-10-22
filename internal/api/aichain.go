package api

import (
	"errors"
	"net/http"

	"github.com/Sergey-Bar/vision/internal/aichain"
	"github.com/Sergey-Bar/vision/internal/airuntime"
	"github.com/Sergey-Bar/vision/internal/identity"
)

// The provider chain.
//
// # What this surface is for
//
// The site names the providers its brain may use and the order they are tried
// in, and that order is a POLICY: it says what may leave the site. So the
// chain is published, because "which model answered, and what did we send it"
// is the question an auditor asks and the one an operator needs answered when
// the site says "no observations today".
//
// # The two halves
//
//   - the CHAIN: which providers, in what order, and whether the site may
//     send anything off the machine at all;
//   - the EGRESS LOG: what left the site, by digest and by size, and never
//     the content.
//
// The log's redaction is the point of it. A log that carried the evidence
// would be a second copy of the evidence in a place nobody chose to put it,
// and a customer reading "what left my site" would be reading their own
// footage rather than a record of it.

// handleAIChain answers: what is the site's brain, and what has it sent away?
func (s *Server) handleAIChain(w http.ResponseWriter, r *http.Request, p identity.Principal, d Deps) {
	if d.AIChain == nil {
		// An install with no chain service has no chain, and the answer
		// says so by name rather than rendering an empty list that reads
		// as "nothing left".
		WriteJSON(w, http.StatusOK, map[string]any{
			"available":   false,
			"unavailable": "This install has no AI provider chain configured.",
			"providers":   []any{},
			"egress":      []any{},
		})
		return
	}
	site, ok := s.recordingSite(w, r, p)
	if !ok {
		return
	}

	policy, policyErr := d.AIChain.Policy(r.Context(), p.TenantID, site)
	out := map[string]any{
		"available": true,
		"siteId":    site,
		"providers": []any{},
		"egress":    []any{},
	}
	if policyErr == nil {
		providers := make([]map[string]any, 0, len(policy.Providers))
		for i, spec := range policy.Providers {
			providers = append(providers, map[string]any{
				// The position IS the policy: the order is what says
				// which provider the site prefers and what it falls
				// back to.
				"position": i + 1,
				"kind":     spec.Kind,
				"model":    spec.Model,
				// The base URL, with no credential in it. The chain
				// names the CREDENTIAL by an environment variable
				// and never carries it.
				"baseUrl": spec.BaseURL,
				// Whether a credential is configured, and never the
				// credential.
				"credentialConfigured": spec.APIKeyEnv != "",
			})
		}
		out["providers"] = providers
		out["egressAllowed"] = policy.EgressAllowed
		out["systemPromptConfigured"] = policy.SystemPrompt != ""
		out["timeoutSeconds"] = int(policy.Timeout.Seconds())

		// And whether the chain BUILDS. A policy can name a provider
		// this build cannot use, or one the site may not reach, and the
		// operator needs to know that before the first observation is
		// due rather than after.
		if _, err := d.AIChain.ChainFor(r.Context(), p.TenantID, site); err != nil {
			out["chainAvailable"] = false
			out["chainUnavailable"] = chainRefusal(err)
		} else {
			out["chainAvailable"] = true
		}
	} else {
		out["policyAvailable"] = false
		out["policyUnavailable"] = "This site has no provider policy: it has no brain configured, " +
			"which is a fact about the site and not a failure of the request."
	}

	egress, err := d.AIChain.EgressLog(r.Context(), p.TenantID, site, 50)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "read_failed", err.Error(), requestID(r))
		return
	}
	records := make([]map[string]any, 0, len(egress))
	for _, e := range egress {
		records = append(records, map[string]any{
			"egressId":      e.EgressID,
			"provider":      e.Provider,
			"observationId": e.ObservationID,
			"profileId":     e.ProfileID,
			"evidenceCount": e.EvidenceCount,
			"evidenceBytes": e.EvidenceBytes,
			// The digests and NOT the content: the log is a record of
			// what left, and keeping what left would make it a second
			// copy of the evidence.
			"evidenceHashes": e.EvidenceHashes,
			"requestedAt":    e.RequestedAt,
		})
	}
	out["egress"] = records

	// The chain's own refusals are named, because each one means a
	// different thing to the person who has to fix it.
	if policyErr != nil && !errors.Is(policyErr, aichain.ErrNoPolicy) {
		out["policyUnavailable"] = policyErr.Error()
	}

	WriteJSON(w, http.StatusOK, out)
}

// chainRefusal names WHY a chain cannot be built.
//
// Each refusal means a different thing and each needs a different fix, so
// they are named rather than flattened into "the chain is not configured".
func chainRefusal(err error) string {
	switch {
	case errors.Is(err, aichain.ErrEgressRefused):
		return "This site may not send evidence off the machine, and its chain names a " +
			"provider that is not on this machine. Either the provider is local or the " +
			"site's egress permission is off."
	case errors.Is(err, aichain.ErrUnknownKind):
		return "This site's chain names an adapter kind this build does not implement."
	case errors.Is(err, airuntime.ErrRemoteBundle):
		return "This site's chain names a local model bundle that is not on this machine."
	case errors.Is(err, airuntime.ErrNoBaseURL):
		return "This site's chain names a provider with no address."
	default:
		return err.Error()
	}
}
