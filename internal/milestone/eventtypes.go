package milestone

// Resolving `type` from the site's own answer.
//
// # The problem this file exists to solve
//
// On a documented event, `type` is a **GUID**
// (`events-rest-v1.reference.md:289-297`, `:380-388`). The adapter's vocabulary
// is names. So every event from a real server arrives as `unknown.<guid>`, is
// flagged `Malformed`, and is dead-lettered — which is correct under an undecided
// vocabulary and useless as a product.
//
// # Why there is no table
//
// **The GUIDs are per-site.** `481d4d6c-9156-4698-af7e-74b32f9e4359` is an
// instance in one customer's XProtect configuration; the next site has a
// different one for the same conceptual type, because the GUID is generated when
// the event type is created there. A compiled-in `guid -> motion` map would work
// perfectly at the design partner and silently dead-letter every event at customer
// two, which is invisible until there is a customer two and unreachable by any
// test that uses the design partner's bytes.
//
// So the mapping is **name-based and resolved per `(tenant, site)` at read time**
// from that site's own `GET /eventTypes` (`:40`, `:42`). Two sites answering with
// two different GUIDs for the same concept both resolve, each to its own site's
// name. `TestTwoSitesWithDifferentGUIDsResolveToTheirOwnNames` is that assertion,
// and it is the one that catches the trap.
//
// # What this deliberately does NOT do
//
// It does not map a name onto a canonical type. Deciding that "Motion Started"
// means `motion` is a published-vocabulary decision, `AGENTS.md` reserves it, and
// the mapping is not writable until the probe has shown a real site's real names.
// What lands here is the missing half: the site's own name reaches the reason
// string, the record and the stored envelope, and the existing normalizer — whose
// tables are untouched — is offered the name instead of a GUID. A site whose names
// happen to be spellings the normalizer already knows starts working on its own;
// one whose are not dead-letters exactly as before, with the vendor's own
// string in the reason instead of only an opaque GUID.
//
// # Unresolved is a state, not a default
//
// A GUID this site did not answer for is **unresolved**. It is never guessed, and
// it is never "probably motion". Invariant 3 is the reason: a missing AI
// observation is never a negative observation, and a missing type name is the
// same kind of absence. A defaulted type would be a fabricated fact wearing the
// vendor's authority.

import (
	"context"
	"strings"
	"sync"
)

// eventTypesRoute is this site's event-type vocabulary.
//
// Declared once, in `Routes`, and looked up here by path with the same
// `mustFindRoute` the clock route uses — so a typo is a startup panic rather
// than a request to a path nobody audited.
var eventTypesRoute = mustFindRoute(APIPrefix + "/eventTypes")

// resolvedTypeNameKey is the envelope field the site-resolved type name is stored
// under.
//
// It exists, and it is the least obvious line in this file. The event's type is
// derived from this site's answer, and `RecordFromPayload` re-derives the record
// from the *stored bytes* weeks later — which is what makes a retried claim
// produce exactly the same canonical event. A name fetched at ingest time is not
// a property of the stored bytes: re-deriving without it would produce
// `unknown.<guid>` for an event that was ingested as `motion`, and the same
// event would have two different types depending on when it was read.
//
// So the resolved name is written into the envelope as it is re-encoded, and the
// replay path reads it from there and never calls the site. The envelope is
// therefore the vendor's body plus exactly one annotation, and a reader of the
// stored event can see which name this site gave the type without a second query.
//
// The key is prefixed because it is not the vendor's field and must not be
// mistaken for one, and because a vendor that ever ships a field of this name
// would otherwise have it silently overwritten.
const resolvedTypeNameKey = "_resolvedEventTypeName"

// siteEventTypes is one site's GUID -> name map, plus why it is not there.
type siteEventTypes struct {
	mu     sync.RWMutex
	byGUID map[string]string
	// why is what stopped the fetch, empty when it worked. It is a sentence
	// rather than a bool so that a dead letter can quote it: "the name is
	// unresolved" is not actionable, and "the site does not deploy /eventTypes"
	// is.
	why string
}

// guidKeyFields and nameKeyFields are the spellings this reads off one entry of
// the vendor's answer.
//
// **These are an assumption and not a citation.** EVENTS:40 and :42 name the
// resource and never show a body (audit §2.8), so the field names here are
// guessed from the vendor's own vocabulary in the surrounding prose — `:42` names
// `generatorType` and `generatorSubtype`, `:335` filters on `typeGroup` — and
// from the shape every other resource in this API has. An entry with neither a
// readable id nor a readable name is skipped rather than half-recorded: a map
// with a GUID pointing at "" would resolve to nothing while looking resolved.
var (
	guidKeyFields = []string{"id", "Id", "ID", "guid", "eventTypeId"}
	nameKeyFields = []string{"name", "Name", "displayName", "typeName"}
)

// lookup answers with the site's own name for a GUID, and whether it resolved.
//
// It never guesses. An empty name and `false` mean the same thing the operator
// needs to hear: this site did not tell us what this type is.
func (s *siteEventTypes) lookup(guid string) (string, bool) {
	if s == nil || guid == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	name, ok := s.byGUID[normaliseEventTypeKey(guid)]
	return name, ok
}

// normaliseEventTypeKey folds a GUID to one spelling.
//
// A GUID is case-insensitive (RFC 4122 §3), and the two documents that show one
// spell it lowercase. Folding is what stops the same type resolving on the
// second page of a poll and not on the first.
func normaliseEventTypeKey(guid string) string {
	return strings.ToLower(strings.TrimSpace(guid))
}

// siteEventTypeName resolves the site's own name for an event's raw `type`.
func (a *Adapter) siteEventTypeName(rawType string) (string, bool) {
	// Only a GUID is worth asking about. `normalizeEventType` already reads the
	// vendor's names, and a poll of a 20-events-per-second feed asking a question
	// about every "Motion" is work for an answer that is already in hand.
	if !isEventTypeGUID(rawType) {
		return "", false
	}
	return a.eventTypes.lookup(rawType)
}

// isEventTypeGUID is the 8-4-4-4-12 hex shape, reusing the device test for the
// same reason it exists there: the only question being decided is whether this
// value is an identifier rather than a name, and shape is the only evidence
// available at this point in the parse.
func isEventTypeGUID(s string) bool { return isDeviceUUID(s) }

// refreshEventTypes re-reads this site's vocabulary.
//
// Called once per poll, before any feed is read, so that resolution is a map
// lookup per event rather than a request per event. Once per poll rather than
// never, because an operator who adds an event type in the VMS this afternoon
// should not have to wait for a restart to see it; the read is a few hundred
// bytes against a poll that is already reading pages.
//
// Every outcome is recorded, including "this site does not have it". A site
// without the route is a normal deployment, not an outage: `Status == 404` is
// recorded as the reason the names are unresolved and nothing else happens.
func (a *Adapter) refreshEventTypes(ctx context.Context) {
	byGUID, why := a.fetchEventTypes(ctx)
	a.eventTypes.mu.Lock()
	defer a.eventTypes.mu.Unlock()
	a.eventTypes.byGUID = byGUID
	a.eventTypes.why = why
}

func (a *Adapter) fetchEventTypes(ctx context.Context) (map[string]string, string) {
	route := eventTypesRoute
	resp, err := a.transport.Get(ctx, route, Query{})
	if err != nil {
		return nil, "no type name could be resolved: " + describe(route) + ": " + err.Error()
	}
	if resp == nil {
		return nil, "no type name could be resolved: " + describe(route) +
			" answered with no response at all"
	}
	if resp.Status == 404 {
		// Not deployed. Recorded, not treated as a fault: an installation that
		// predates the Configuration API is an installation whose event types
		// stay GUIDs, and the feed behaves exactly as it did before this route
		// existed.
		return nil, "this site does not deploy " + describe(route) +
			", so its event types stay GUIDs"
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, "no type name could be resolved: " + describe(route) + " answered " +
			statusText(resp.Status)
	}
	// The same decoder the feeds use. Two readers of a vendor body would be two
	// answers to "what shape is this", which is the drift class this repository
	// has already paid for once.
	items, err := decodeItems(route, resp.Body)
	if err != nil {
		return nil, "no type name could be resolved: " + err.Error()
	}
	byGUID := make(map[string]string, len(items))
	for _, item := range items {
		guid := stringField(item, guidKeyFields...)
		name := stringField(item, nameKeyFields...)
		if guid == "" || name == "" {
			continue
		}
		byGUID[normaliseEventTypeKey(guid)] = name
	}
	return byGUID, ""
}

// unresolvedReason is why the site-resolved name is missing, for a dead letter.
func (a *Adapter) unresolvedReason() string {
	a.eventTypes.mu.RLock()
	defer a.eventTypes.mu.RUnlock()
	return a.eventTypes.why
}
