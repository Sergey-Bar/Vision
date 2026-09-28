// Package milestone is the protocol adapter for XProtect Milestone.
//
// The adapter has exactly one parsing path. A live server and a fixture file
// differ only in where the bytes come from, so the seam is a Transport and
// everything above it is shared. That is what makes Definition of Done item 1 —
// integration mode and replay mode producing the same canonical event — a
// structural property rather than a thing to keep in sync by hand.
//
// # Confidence in the wire format
//
// Milestone's public API surface is assumed, not verified. The plan names this
// as the top project risk, and it is: a wrong endpoint or a wrong field name
// shows up as an empty event feed on a real site, not as a failing test. Every
// endpoint and field name therefore lives in one file, Route, with a comment
// saying it is unverified, and the first thing to do on a real installation is
// to run `securityctl adapter probe`, which lists what the server actually
// returns next to what this adapter expects. Until that has been run against a
// real server, treat the mapping as a hypothesis with tests.
package milestone

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

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
	Paginated bool
	// Unverified marks a route whose real behaviour has not been confirmed
	// against a live server. It is a field rather than a comment so that
	// `securityctl adapter probe` can print exactly which parts of the adapter
	// are still a hypothesis, and so a test can assert the list shrinks over
	// time rather than rotting silently.
	Unverified bool
	// Notes says what is being assumed.
	Notes string
}

// Routes is the complete assumed Milestone API surface.
//
// Every entry is Unverified. That is accurate today: no Milestone server has
// been reachable from this codebase. When `securityctl adapter probe` runs
// against a real installation, the entries it confirms lose the flag, and the
// ones it contradicts get fixed here and there — which is the whole point of
// keeping them in one file.
var Routes = []Route{
	{
		Method: "GET", Path: "/api/v1/System/UserGroups", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Used to enumerate cameras and resource groups. The platform does not " +
			"store operator credentials from this; camera identity only.",
	},
	{
		Method: "GET", Path: "/api/v1/Events", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "The event feed. Polled with a since-time cursor. Field names assumed: " +
			"Id, Timestamp, Type, Source, Priority, TimeSpan, Message, ImagePath, " +
			"VideoRecordingStartTime.",
	},
	{
		Method: "GET", Path: "/api/v1/Events/{eventId}", Expect: "object",
		Unverified: true,
		Notes: "Full event detail, including the motion/analytics payload this adapter " +
			"maps to the canonical event payload.",
	},
	{
		Method: "GET", Path: "/api/v1/Alarms", Expect: "array", Paginated: true,
		Unverified: true,
		Notes: "Alarm feed. Alarm created/updated/acknowledged all arrive as a changing " +
			"alarm state rather than distinct event types, so the adapter derives the " +
			"state transition.",
	},
	{
		Method: "GET", Path: "/api/v1/Bookmarks", Expect: "array", Paginated: true,
		Unverified: true,
		Notes:      "Bookmarks, including those created by a rule action.",
	},
	{
		Method: "GET", Path: "/api/v1/ResourceGroups", Expect: "array", Paginated: true,
		Unverified: true,
		Notes:      "Zone-equivalent topology. Mapped onto the ZONE graph nodes.",
	},
	{
		Method: "GET", Path: "/api/v1/Servlets", Expect: "array", Paginated: true,
		Unverified: true,
		Notes:      "Device state, including online/offline and recording state.",
	},
	{
		Method: "GET", Path: "/api/v1/System/Time", Expect: "object",
		Unverified: true,
		Notes: "Server time. Read to measure clock drift between the VMS and this host; " +
			"drift past the severe band excludes the source from time-windowed " +
			"corroboration.",
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
// server. Exposed so `securityctl adapter probe` can report it and a test can
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

// Transport supplies raw responses to the adapter.
//
// The interface is deliberately at the byte level rather than the object level.
// If it returned parsed objects, the fixture path would parse the JSON itself and
// the two parsers would drift — which is precisely the bug replay-equality is
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
