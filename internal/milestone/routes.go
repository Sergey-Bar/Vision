// Package milestone is the protocol adapter for XProtect Milestone.
//
// The adapter has exactly one parsing path. A live server and a fixture file
// differ only in where the bytes come from, so the seam is a Transport and
// everything above it is shared. That is what makes Definition of Done item 1 -
// integration mode and replay mode producing the same canonical event - a
// structural property rather than a thing to keep in sync by hand.
//
// # Confidence in the wire format
//
// Milestone's public API surface is *documented, not verified*. Both documents
// Milestone publishes are committed in this repository, under
// `docs/vendor/milestone/`, and every route below was checked against them in
// `docs/vendor/milestone/route-audit.md` — which is where the citation for each
// one lives. Reading a document fixed five of nine routes and left four needing a
// probe. It cleared none of them, and it cannot: a published document is not a
// running server, so `Unverified` stays true on every route until
// `securityctl adapter-probe` has run against a real installation and a human
// has read the artifact it produced. The plan names this as the top project risk
// and it is still the top project risk; what changed is that the hypothesis is
// now written down and cited rather than remembered.
//
// The specific trap this package was written around is unchanged: a wrong
// endpoint or a wrong field name shows up as an empty event feed on a real site,
// not as a failing test.
package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// APIPrefix is the documented root of the Milestone REST surface.
//
// It is one constant rather than a prefix written into eight paths, because the
// mistake it replaces was made in eight places at once: the route table declared
// `/api/v1/...`, which is not a Milestone prefix and appears in neither published
// document, so every call would have been a 404 whose failure mode is a site that
// looks quiet.
//
// **The two documents disagree on case and this does not resolve it.**
// `events-rest-v1.reference.md` writes `/api/rest/v1/events` five times, and
// `bookmarks-rest-v1.openapi.yaml:36-37` writes `https://localhost/API/rest/v1`.
// This constant takes the lower-case form, because that is the one the events
// reference uses for the resource family this adapter actually calls. If a probe
// answers 404 on every route simultaneously, the case is the first thing to try
// and this line is the only thing to change. See
// `docs/vendor/milestone/route-audit.md` §2.1.
const APIPrefix = "/api/rest/v1"

// Route is one endpoint the adapter calls, and the place every assumption about
// the Milestone API surface is written down.
type Route struct {
	// Method and Path are the wire shape. Path is relative to the server base
	// URL and may contain {placeholders} filled from RouteParams.
	Method string
	Path   string
	// Expect is the shape the adapter requires the response to have. A response
	// that does not match is a probe failure, not a silently empty page.
	Expect string
	// Paginated says the endpoint returns a paged list rather than one object.
	//
	// It is also, in practice, the flag that says "this is a source feed":
	// `Adapter.Poll` reads exactly the routes with it set. Those are one field
	// doing two jobs, so both are written down here rather than discovered. A
	// route that is a list but is not a *feed* of events leaves it clear - see
	// `/eventTypes`, which is a vocabulary the adapter looks up rather than
	// something a customer's cameras produce.
	Paginated bool
	// Unverified marks a route whose real behaviour has not been confirmed
	// against a live server. It is a field rather than a comment so that
	// `securityctl adapter-probe` can print exactly which parts of the adapter
	// are still a hypothesis, and so a test can assert the list shrinks over
	// time rather than rotting silently.
	Unverified bool
	// WriteBack marks a route the adapter posts to rather than reads from. It is
	// a field because the direction of a call changes what has to be true of it:
	// a write is not idempotent unless its key is, and a read that fails is
	// retried while a write that fails may already have happened.
	WriteBack bool
	// ClockProbe marks a route whose answer is a timestamp. It is a field for the
	// same reason: a clock reading has an uncertainty attached and a list item
	// does not.
	ClockProbe bool
	// Task is the vendor's `task` query parameter, and it is the only way this
	// API selects an operation on a POST.
	//
	// It is a field because a `POST` with no `task` is a different operation from
	// a `POST` with one, on the same path. `bookmarks-rest-v1.openapi.yaml:167-171`
	// declares `task` as a *query* parameter and `:142-161` gives it four meanings,
	// so the path alone does not name the call. A non-empty Task also marks the
	// route as one that cannot be read with a GET, which is how the adapter knows
	// to reach for BookmarkSearcher instead.
	Task string
	// Notes says what is being assumed, and for every route it is what the
	// published documents in docs/vendor/milestone say about it.
	Notes string
}

// Routes is the complete assumed Milestone API surface.
//
// Every entry is Unverified, and that stays true after the route audit. A
// published document can refute an assumption and it did — five of these nine
// routes were declared wrong and are corrected here — but refuting is all it can
// do. No Milestone server has ever been reachable from this codebase, so when
// `securityctl adapter-probe` runs against a real installation, the entries it
// confirms lose the flag, and the ones it contradicts get fixed here and there -
// which is the whole point of keeping them in one file.
//
// Every entry carries a verdict in docs/vendor/milestone/route-audit.md, and
// route_audit_test.go fails if one does not.
var Routes = []Route{
	{
		Method: "GET", Path: APIPrefix + "/System/UserGroups", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Prefix documented (/api/rest/v1); the RESOURCE IS NOT. The audit verdict is " +
			"NOT-ANSWERABLE-FROM-DOCS: neither published document mentions user groups, and " +
			"events-rest-v1.reference.md:34 says cameras are enumerated with the Configuration " +
			"REST API instead, so the kind of resource may be wrong as well as the name. Used " +
			"for camera identity only; the platform stores no operator credentials from this.",
	},
	{
		Method: "GET", Path: APIPrefix + "/events", Expect: "object", Paginated: true,
		Unverified: true,
		Notes: "The event feed, and the whole product. Documented: filter time=gt:'<ISO8601>' " +
			"(operators gt/lt only), orderBy=desc:'time', page>=0, size 1..2000, and an answer of " +
			"{\"_links\":{\"prev\",\"next\"},\"array\":[...],\"eventCount\":N}. Unfiltered it " +
			"returns the OLDEST stored events first, which is why the cursor is a filter and not " +
			"an offset. Fields read off one item: id, time, type, source, localId, datatype, tag. " +
			"`type` is a GUID, not a name, so nothing normalises yet - see the audit's H1.",
	},
	{
		Method: "GET", Path: APIPrefix + "/events/{id}", Expect: "object",
		Unverified: true,
		Notes: "One event by id. The placeholder is `id` and is documented as a guid " +
			"(events-rest-v1.reference.md:266-283). 404 means either no such event or one the " +
			"event type's retention policy did not keep, which is a normal race. The motion and " +
			"analytics payload this maps to lives under `data`, which the reference documents for " +
			"events this adapter TRIGGERS rather than for events it reads.",
	},
	{
		Method: "GET", Path: APIPrefix + "/Alarms", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Prefix documented; the RESOURCE IS NOT - the audit verdict is " +
			"NOT-ANSWERABLE-FROM-DOCS. Alarms appear in events-rest-v1.reference.md:49-50 as " +
			"Configuration API objects that events trigger, never as a feed to read. The adapter " +
			"already models an alarm as a state machine (adapter.go deriveEventType), which is " +
			"consistent with that, and CapabilityAlarms is documented as best-effort.",
	},
	{
		Method: "POST", Path: APIPrefix + "/bookmarks", Task: "searchTime",
		Expect: "object", Paginated: true,
		Unverified: true,
		Notes: "The bookmark feed. NOT a GET: bookmarks-rest-v1.openapi.yaml:45-46 says \"Bookmarks " +
			"cannot be retrieved by use a GET .../bookmarks request\". task is a QUERY parameter " +
			"(:167-171) and this is the searchTime task, whose body needs time and countLimit and " +
			"needs either resourceTypes or devicePaths (:305-374). With time and no endTime the " +
			"window is the documented 60-minute default. Reached through BookmarkSearcher, because " +
			"a GET cannot express this call.",
	},
	{
		Method: "GET", Path: APIPrefix + "/ResourceGroups", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Prefix documented; the RESOURCE IS NOT - the audit verdict is " +
			"NOT-ANSWERABLE-FROM-DOCS. Neither document contains the words \"resource group\" or " +
			"\"zone\". Zone-equivalent topology, mapped onto the ZONE graph nodes.",
	},
	{
		Method: "GET", Path: APIPrefix + "/Servlets", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Prefix documented; the resource name is CONTRADICTED - \"servlet\" appears nowhere " +
			"in either document. The documented device vocabulary is cameras, microphones and " +
			"speakers, and a device reference is \"cameras/<guid>\" " +
			"(bookmarks-rest-v1.openapi.yaml:257; events-rest-v1.reference.md:291). Recorded rather " +
			"than renamed, because no published document names a device-list endpoint to rename it " +
			"to. Device state, including online/offline and recording state.",
	},
	{
		Method: "GET", Path: APIPrefix + "/eventTypes",
		Unverified: true,
		// Two fields are deliberately different from every other entry, and both
		// are refusals rather than oversights.
		//
		// `Paginated` is false because this is not a feed. That flag is what
		// `Adapter.Poll` uses to decide what to poll, and an event-type list
		// polled as a source feed would arrive with no `Type` field, dead-letter
		// as unparsed items, and be stored as if it were evidence.
		//
		// `Expect` is empty because the response schema is not in either published
		// document. EVENTs:40 and :42 name the resource and never show a body, so
		// declaring a shape here would be a second guess written down as a fact,
		// and the probe would then report a shape mismatch as a contradiction. The
		// probe reads whatever arrived and reports it, which is the whole reason
		// this route is worth declaring before the probe has ever run.
		Notes: "The event-type vocabulary, and the route that unblocks `type` on an event. " +
			"EVENTS:40 says \"You can list available event types or create new event types " +
			"with the Configuration REST API: Event types\" and :42 names `GET /eventTypes`, " +
			"so the resource is DOCUMENTED; the response schema is NOT - neither document shows " +
			"a body, hence the empty Expect. Read per (tenant, site) and cached, because the " +
			"GUIDs are per-site configuration, not global constants: 481d4d6c-... is an " +
			"instance in one customer's VMS and the same conceptual type carries a different " +
			"GUID at the next site. A compiled-in guid->name table would work at the design " +
			"partner and dead-letter every event at customer two, invisibly, which is why the " +
			"mapping is resolved from this site's own answer and never defaulted. The field " +
			"names this reads (id/name) are an ASSUMPTION; the probe's sample is how it is " +
			"settled. See docs/vendor/milestone/route-audit.md §2.8.",
	},
	{
		Method: "GET", Path: APIPrefix + "/System/Time", Expect: "object", ClockProbe: true,
		Unverified: true,
		Notes: "Prefix documented; the RESOURCE IS NOT - the audit verdict is " +
			"NOT-ANSWERABLE-FROM-DOCS. Neither document declares a clock endpoint, and neither " +
			"declares a field name for a clock answer, so writeback.go's four tolerated spellings " +
			"have nothing to be tolerant of yet. This means the clock-drift gate has no working " +
			"input today: drift past the severe band is supposed to exclude a device from " +
			"time-windowed corroboration, and drift cannot be measured. See the audit's §2.7.",
	},

	// ---------------------------------------------------------------------
	// The Configuration-API family the site configurator drives.
	//
	// These are the routes `securityctl site-config` reads and writes through
	// the Milestone adapter. They share one property with everything else in
	// this table: the prefix is documented and the resources under it are
	// not. EVENTS:40 says cameras, event types and the rest of the
	// configuration live behind the Configuration REST API, and neither
	// committed document contains a single one of those resource names, so
	// every route below is NOT-ANSWERABLE-FROM-DOCS until a human probe runs
	// (`.kilo/skills/milestone-probe`). None of them clears itself: the
	// configurator never promotes a flag, and a probe that cannot answer
	// leaves the route exactly as declared here.
	//
	// None of them is `Paginated`, and that is load-bearing rather than an
	// omission: `Adapter.Poll` builds its feed set from exactly the routes
	// where `Paginated` is true, so marking a configuration list as a page
	// makes the adapter poll it as an EVENT FEED - which on a real Milestone
	// answers with a configuration object, fails `decodeItems`, and produces
	// a `Malformed` dead letter on every poll. A route is a feed when the
	// adapter polls it for records, and nothing here is that.
	// ---------------------------------------------------------------------
	{
		Method: "GET", Path: APIPrefix + "/cameras", Expect: "array",
		Unverified: true,
		Notes: "The camera list the configurator discovers from. EVENTS:40 points " +
			"at the Configuration REST API for cameras and neither document names a " +
			"cameras resource, so this is NOT-ANSWERABLE-FROM-DOCS. Deliberately NOT " +
			"Paginated: Paginated is the flag that makes a route an event FEED, and a " +
			"configuration inventory is not one - see the block comment above.",
	},
	{
		Method: "GET", Path: APIPrefix + "/cameras/{id}", Expect: "object",
		Unverified: true,
		Notes: "One camera's settings, which is what a reconcile compares the " +
			"desired state against. NOT-ANSWERABLE-FROM-DOCS: the placeholder and the " +
			"response schema are both undocumented. 404 here means the camera was " +
			"removed between listing and reading, which the configurator treats as " +
			"an adoption decision for the operator rather than an error to retry.",
	},
	{
		Method: "PUT", Path: APIPrefix + "/cameras/{id}", Expect: "object", WriteBack: true,
		Unverified: true,
		Notes: "The configurator's camera write. NOT-ANSWERABLE-FROM-DOCS: no " +
			"document declares a PUT, a body schema, or which camera settings this " +
			"endpoint would even accept. WriteBack: true so the adapter's task " +
			"tracking applies, and the idempotency-key convention of dispatch.go " +
			"is mirrored by the caller: a re-run of the same plan writes the same " +
			"bytes, so a crash mid-apply is re-run rather than repaired.",
	},
	{
		Method: "GET", Path: APIPrefix + "/recordingServers", Expect: "array",
		Unverified: true,
		Notes: "The recording servers, because retention is configured there and " +
			"not on the camera. NOT-ANSWERABLE-FROM-DOCS: the resource, the shape " +
			"and the retention field are all undocumented. Not Paginated, and " +
			"therefore not a feed, for the reason the block comment above gives.",
	},
	{
		Method: "PUT", Path: APIPrefix + "/recordingServers/{id}", Expect: "object", WriteBack: true,
		Unverified: true,
		Notes: "The retention write, and the most dangerous route in this table: " +
			"the configurator only ever extends retention, because a reduction is a " +
			"data-loss boundary that a human crosses with an explicit command. " +
			"NOT-ANSWERABLE-FROM-DOCS for the resource and the field name alike.",
	},
	{
		Method: "GET", Path: APIPrefix + "/rules", Expect: "array",
		Unverified: true,
		Notes: "The analytics rule list the configurator derives rules into. " +
			"NOT-ANSWERABLE-FROM-DOCS: nothing is documented about a rules resource, " +
			"and the distinction between a site's own rules and the adapter's " +
			"read-only view of them is unknown.",
	},
	{
		Method: "POST", Path: APIPrefix + "/rules", Expect: "object", WriteBack: true,
		Unverified: true,
		Notes: "The analytics rule create. NOT-ANSWERABLE-FROM-DOCS. The bundle " +
			"candidate is rendered by rules.Compiler and activated by a human " +
			"(the plan's sequencing); this route is only reached after that, and " +
			"never by the configurator on its own.",
	},
	{
		Method: "GET", Path: APIPrefix + "/alarmDefinitions", Expect: "array",
		Unverified: true,
		Notes: "The alarm definitions the site's escalations map onto. " +
			"NOT-ANSWERABLE-FROM-DOCS. EVENTS:49-50 places alarms in the " +
			"Configuration API as objects events trigger, which is consistent with " +
			"a definitions list and says nothing about its name.",
	},
}

// RouteByKind maps a fixture event's Kind to the route it stands for.
func RouteByKind(kind string) (Route, bool) {
	for _, r := range Routes {
		if r.Path == kind || r.Method+" "+r.Path == kind {
			return r, true
		}
	}
	return Route{}, false
}

// UnverifiedRoutes lists the routes still awaiting confirmation against a live
// server. Exposed so `securityctl adapter-probe` can report it and a test can
// assert the list is non-empty rather than quietly emptying out.
func UnverifiedRoutes() []Route {
	var out []Route
	for _, r := range Routes {
		if r.Unverified {
			out = append(out, r)
		}
	}
	return out
}

// UnverifiedRoute reports whether the route at path is still awaiting a live
// probe.
//
// It is a lookup rather than an index because the two facts it answers — "which
// clock route" and "is it still unverified" — were previously written down twice
// and could disagree, and the write-back's clock read was one of the two copies.
// See mustFindRoute.
func UnverifiedRoute(path string) bool {
	for _, r := range Routes {
		if r.Path == path {
			return r.Unverified
		}
	}
	return false
}

// Transport supplies raw responses to the adapter.
//
// The interface is deliberately at the byte level rather than the object level.
// If it returned parsed objects, the fixture path would parse the JSON itself and
// the two parsers would drift - which is precisely the bug replay-equality is
// supposed to catch and not create.
type Transport interface {
	// Get performs a read. path is a route path with placeholders already
	// substituted.
	Get(ctx context.Context, route Route, query Query) (*Response, error)
	// Close releases any long-lived connection.
	Close() error
}

// Query is a route query. Milestone paging and time cursors are assumed to be
// query parameters; that assumption is recorded on the route.
type Query struct {
	// Since limits a feed to entries after this instant. Nil means "from the
	// cursor this adapter already has", not "from the beginning" — an adapter
	// that re-reads the whole feed on reconnect is a load problem on a 2,000
	// camera site.
	Since *time.Time
	// Limit is the page size.
	Limit int
	// Cursor continues a paged read.
	Cursor string
	// Params are extra query parameters, passed through as given and spelled the
	// vendor's way: `property=operator:value`, with string values in single
	// quotes (`events-rest-v1.reference.md:219-263`).
	//
	// They exist for questions the adapter has no route for. A probe asking whether
	// a site's analytics are populated asks
	// `analyticData.object.value=contains:'ABC123'`, and there is no reason to
	// learn a new Go type to do it.
	//
	// A name here wins over the same name derived below, because the caller is
	// answering a question the adapter's own paging did not intend to ask. Writes
	// carry none of them, for the same reason `size` is read-only: sending a read
	// filter to a documented body-and-task endpoint asks the vendor about an
	// endpoint that has no filters.
	Params map[string]string
}

// Response is what a transport returns: the status, the allow-listed headers,
// and the body bytes exactly as received.
type Response struct {
	Status  int
	Headers map[string]string
	Body    json.RawMessage
	// Route records which route produced this, so a parse error names the
	// endpoint rather than "the adapter".
	Route Route
}

// UnauthenticatedError is returned by a transport when the source rejected
// credentials. It is a distinct error because it is an operational incident with
// an operator-visible remedy, not a malformed-payload bug to retry.
type UnauthenticatedError struct {
	Route  Route
	Status int
}

func (e *UnauthenticatedError) Error() string {
	return fmt.Sprintf("milestone: %s %s returned %d and rejected the integration credentials; "+
		"the integration's credentials need rotating",
		e.Route.Method, e.Route.Path, e.Status)
}

// MalformedError is returned when a body does not match the shape the route
// declared. It is never swallowed: a feed that silently parses to zero events
// looks exactly like a site with no activity.
type MalformedError struct {
	Route Route
	Why   string
}

func (e *MalformedError) Error() string {
	return fmt.Sprintf("milestone: %s %s returned a body this adapter cannot read: %s",
		e.Route.Method, e.Route.Path, e.Why)
}

// IsNotFound reports whether err is a 404 from the source. Milestone returns 404
// for a resource that was deleted between listing and detail, which is a normal
// race and not an error worth an alert.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return asNotFound(err, &nf)
}

// NotFoundError is a 404 from the source.
type NotFoundError struct {
	Route Route
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("milestone: %s %s returned 404", e.Route.Method, e.Route.Path)
}
