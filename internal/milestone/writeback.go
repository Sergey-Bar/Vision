package milestone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The optional half of the vendor surface.
//
// The plan calls for three additions - a bookmark write-back, a clock probe, and
// a capability report - and says they are **optional so ReplayTransport does not
// break**. That word does real work here. A fixture transport serves recorded
// bytes; it has no clock to sample and no bookmark to place, and if these were
// methods on Transport every fixture would have to grow a fake implementation of
// a system it is standing in for. So they are separate interfaces, discovered by
// type assertion, and an implementation that does not have one is not broken -
// it is a replay.
//
// That is also what keeps the surface small. The neutrality test counts the
// methods on Transport; three more on three more interfaces is a different
// number, and a caller that adds a fourth optional interface is adding to a
// list a test reads.

// Bookmark is a pointer into the vendor's own recording.
//
// It is a pointer, deliberately, and not a copy of the footage. The operator
// opens the vendor's player, at the right second, on the vendor's own archive -
// which is the whole of the "we do not duplicate an archive" decision, and the
// reason this is a bookmark and not an upload of our own clip.
type Bookmark struct {
	// IdempotencyKey is derived from the incident, not from a clock.
	//
	// A retry after a timeout must not create a second bookmark, and a second
	// bookmark at the same second is a duplicate an operator has to recognise and
	// dismiss. The key is derived so a retry computes the same value, which is the
	// only way idempotency survives a process restart.
	IdempotencyKey string
	// SiteRef names the camera, in the vendor's own vocabulary. It comes from the
	// adapter's device reference, never from a request body.
	SiteRef string
	// Time is the second in the recording the operator should open at.
	Time time.Time
	// Label is a short human phrase for the bookmark list.
	Label string
	// Description is the longer text. It is prose and nothing parses it, which is
	// why a change to its wording cannot change behaviour.
	Description string
}

// BookmarkWriter is the optional write-back surface.
//
// It is named for what it writes, not for the vendor: a second vendor's
// write-back is also a bookmark in its own player's own recording, and the
// concept generalises even though the route does not.
type BookmarkWriter interface {
	WriteBookmark(ctx context.Context, b Bookmark) (BookmarkRef, error)
}

// BookmarkRef is what a placed bookmark is known by afterwards.
type BookmarkRef struct {
	ID string
	// Replayed is true when the vendor recognised the idempotency key and
	// returned the existing bookmark instead of creating a second one.
	//
	// It is reported rather than inferred, because "I created one" and "one
	// already existed" are the same answer to "does the operator see a bookmark"
	// and different answers to "did we just double-write".
	//
	// It is **false against every documented build of this API**, and that is a
	// fact rather than a default. Neither published document declares an
	// idempotency mechanism — no `Idempotency-Key` header, and no statement that a
	// client-supplied `reference` is honoured as a caller-chosen key — so a vendor
	// has no documented reason to echo one back. The field exists because a caller
	// has to be able to ask the question, and the answer it will get is "we do not
	// know". See docs/vendor/milestone/route-audit.md §2.5.
	Replayed bool
}

// BookmarkSearcher is the optional bookmark-read surface.
//
// It is optional, and separate, for the same reason BookmarkWriter is: this
// adapter reads bookmarks with a `POST`, because the vendor says a GET cannot
// retrieve them
// (`bookmarks-rest-v1.openapi.yaml:45-46`: *"Bookmarks cannot be retrieved by use
// a GET .../bookmarks request. Please use one of the search tasks under the POST
// requests."*), and `Transport.Get` is a GET. Widening `Transport` to take a
// method and a body would make its own name false and would give every fixture
// transport a fake implementation of a search task it does not have.
//
// It returns the vendor's own bytes rather than records, for the same reason
// `Get` does: the adapter is the single parsing path, and a search that parsed
// here would be a second parser.
type BookmarkSearcher interface {
	SearchBookmarks(ctx context.Context, query Query) (*Response, error)
}

// searchTimeBody is the documented body of `task=searchTime`
// (`bookmarks-rest-v1.openapi.yaml:305-374`).
type searchTimeBody struct {
	// Time is the start of the window and is required (:373).
	Time string `json:"time"`
	// CountLimit is required (:374) and is the vendor's name for a page size.
	CountLimit int `json:"countLimit"`
	// ResourceTypes or DevicePaths must be specified (:257-258). There is no "all"
	// value, so the documented collections are named explicitly rather than
	// guessed at: a bookmark on a microphone or a speaker would otherwise be
	// invisible to a poll that asked for cameras only.
	ResourceTypes []string `json:"resourceTypes"`
}

// bookmarkResourceTypes is every device collection the documented search accepts.
var bookmarkResourceTypes = []string{"cameras", "microphones", "speakers"}

// SearchBookmarks reads the bookmark feed over a time window.
//
// One call, one page. The documented search answer carries no continuation link —
// it is a `bookmarkArrayBody`, `{"array":[…]}` (`:380-386`) — so `countLimit` is
// the whole extent of the read and there is nothing to follow.
func (t *httpTransport) SearchBookmarks(ctx context.Context, query Query) (*Response, error) {
	route := mustFindTask(APIPrefix+"/bookmarks", "searchTime")
	if t.token == "" {
		return nil, &UnauthenticatedError{Route: route, Status: 0}
	}
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	// The window's start. The vendor's documented default span is 60 minutes
	// (:342) when neither endTime nor timeLimit is given, so a poll that wants a
	// different span has to say so, and a poll that wants the default says nothing.
	body, err := json.Marshal(searchTimeBody{
		Time:          searchStart(query.Since),
		CountLimit:    limit,
		ResourceTypes: bookmarkResourceTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("milestone: encode the bookmark search: %w", err)
	}
	resp, err := t.do(ctx, http.MethodPost, route, body, "")
	if err != nil {
		return nil, err
	}
	return &Response{
		Status:  resp.Status,
		Body:    resp.Body,
		Route:   route,
		Headers: map[string]string{"Content-Type": "application/json"},
	}, nil
}

// searchStart is the window start for a search, and the reason a poll with no
// cursor still asks a bounded question.
//
// `time` is required by the vendor, so "no cursor" cannot be expressed as its
// absence. It is expressed as the epoch instead: with the documented 60-minute
// default span, a cursorless poll reads the first hour of recorded history rather
// than everything, which is bounded and honest. A poll that has a cursor uses it.
func searchStart(since *time.Time) string {
	if since == nil {
		return time.Unix(0, 0).UTC().Format(time.RFC3339)
	}
	return since.UTC().Format(time.RFC3339)
}

// mustFindTask resolves a path-and-task pair declared in Routes, so a task the
// code sends but the table does not declare is a startup panic rather than a call
// the audit cannot account for.
func mustFindTask(path, task string) Route {
	for _, r := range Routes {
		if r.Path == path && r.Task == task {
			return r
		}
	}
	panic("milestone: no route is declared at " + path + " with task " + task)
}

// ClockProbe is the optional clock surface.
//
// Why it is worth an optional interface at all: the product stores UTC and reads
// the vendor's own clock to measure drift, and a bookmark placed at the wrong
// offset is a bookmark at the wrong second. A site whose clock is measured is a
// site whose window selection is defensible; one whose clock is assumed is one
// whose window is a guess that happens to be right most of the time.
type ClockProbe interface {
	ServerTime(ctx context.Context) (ServerTime, error)
}

// ServerTime is a clock reading with its uncertainty, because a clock reading
// without one is a number and a number cannot be compared with a threshold.
type ServerTime struct {
	// At is the vendor's current time.
	At time.Time
	// RoundTrip is how long the call took, so a reader can discount a reading
	// taken while the link was slow.
	RoundTrip time.Duration
}

// Capability is one thing a deployment can do.
//
// A closed vocabulary, because a console that renders a string nobody chose is a
// console that renders whatever a misconfiguration said.
type Capability string

const (
	// CapabilityRTSPDirect means cameras are reachable as RTSP streams.
	CapabilityRTSPDirect Capability = "rtsp-direct"
	// CapabilityBookmarks means a verdict can be pointed at in the vendor player.
	CapabilityBookmarks Capability = "bookmarks"
	// CapabilityEvents means inbound events arrive.
	CapabilityEvents Capability = "events"
	// CapabilityClockProbe means the vendor's clock can be sampled.
	CapabilityClockProbe Capability = "clock-probe"
	// CapabilityAlarms means alarms can be raised.
	//
	// Best-effort by nature: per the vendor's own forum, events are not retained
	// by default and an alarm needs a customer-defined alarm definition. A
	// deployment that has not defined one reports this false, and a consumer
	// that needs an alarm degrades to a bookmark rather than failing.
	CapabilityAlarms Capability = "alarms"
)

// Capabilities is what a deployment can do, as a report.
//
// It is a plain map rather than a type with a query method, because a capability
// report is read exactly once by a console and once by a gate, and a method that
// answers one boolean would be a second thing to keep in step with the map.
type Capabilities struct {
	// Has is the set of capabilities this deployment has.
	Has map[Capability]bool
	// Unknown lists capability names a caller asked about that this build does
	// not know. It is reported rather than treated as absent, because "we have
	// never heard of that" and "we checked and it is not there" are different
	// facts and a capability gate that conflates them hides a broken probe.
	Unknown []Capability
}

// CapabilityReporter is the optional capability surface.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// ProbeCapabilities reports what a transport can do.
//
// It is a function rather than a method so that a caller can probe a transport
// that implements none of the optional interfaces, and get an honest empty
// answer instead of a nil dereference.
func ProbeCapabilities(t Transport) Capabilities {
	var out Capabilities
	out.Has = map[Capability]bool{}
	if t == nil {
		return out
	}
	if _, ok := t.(BookmarkWriter); ok {
		out.Has[CapabilityBookmarks] = true
	}
	if _, ok := t.(ClockProbe); ok {
		out.Has[CapabilityClockProbe] = true
	}
	if _, ok := t.(CapabilityReporter); ok {
		for name, present := range t.(CapabilityReporter).Capabilities().Has {
			out.Has[name] = present
		}
	}
	// Reads are what the adapter is built on, so anything that can Get can
	// receive events. It is reported from the base interface rather than
	// asserted, so a replay transport does not claim to receive anything live.
	if _, ok := t.(interface {
		Get(context.Context, Route, Query) (*Response, error)
	}); ok {
		out.Has[CapabilityEvents] = true
	}
	return out
}

// AllCapabilities is the closed vocabulary, sorted so a probe report reads the
// same on every machine.
func AllCapabilities() []Capability {
	all := []Capability{
		CapabilityAlarms, CapabilityBookmarks, CapabilityClockProbe,
		CapabilityEvents, CapabilityRTSPDirect,
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	return all
}

// Clock drift bands.
//
// A site whose vendor clock is more than a few seconds from ours cannot have a
// window selection that means anything, and the plan is explicit that a camera
// in the severe band widens its window or is refused with a named reason. The
// bands live here because this is the only place that knows what the readings
// are comparable to.
const (
	// ClockDriftWarn is the point past which a window is widened.
	ClockDriftWarn = 2 * time.Second
	// ClockDriftSevere is the point past which a window cannot be trusted at
	// all and the caller must decide rather than the product.
	ClockDriftSevere = 30 * time.Second
)

// Drift is the measured difference between the vendor's clock and ours.
type Drift struct {
	Offset  time.Duration
	Sampled time.Time
}

// Band names the severity of a drift reading, which is a closed vocabulary
// because it drives a refusal and a refusal has to say which one.
func (d Drift) Band() string {
	switch {
	case d.Offset > ClockDriftSevere:
		return "severe"
	case d.Offset > ClockDriftWarn:
		return "warn"
	default:
		return "ok"
	}
}

// bookmarkRoute is the write-back route.
//
// The path is documented (`bookmarks-rest-v1.openapi.yaml:140`) and the method is
// documented. The interesting part is the absent `task`.
//
// # Why no task, deliberately
//
// `task` is a query parameter that selects one of four operations on this one path
// (`:142-161`, `:167-171`), and the two that matter here are opposites:
//
//   - **no task** — "a new bookmark is created from the body" … "the bookmark is
//     created, but the rule system is not triggered" (`:145`, `:160-161`).
//   - **`newReference`** — "create a new reference and trigger rules", "will be
//     used to execute configured rules, to ensure actions like Start-Recording is
//     performed in real time", and then: "**The newReference task does not create
//     the bookmark in the database**" (`:147`, `:156-158`).
//
// This seam exists to place a pointer the operator can follow: the vendor's
// player, at the right second, on the vendor's own archive. `newReference` does
// not create that pointer. It fires the customer's own configured rules, which
// can start recordings on their archive — an operational side effect on a
// customer's system, and therefore a capability activation, which AGENTS.md
// reserves to a human.
//
// So Task is empty, and the reason is written here rather than left to be
// re-derived by the next reader. The read side of the same path takes
// `task=searchTime` and that is a different route entry, not a variation of this
// one.
var bookmarkRoute = Route{
	Method: "POST", Path: APIPrefix + "/bookmarks",
	Expect: "object", WriteBack: true, Unverified: true,
	Notes: "Documented, including the decision to send no `task`: a task-less POST creates " +
		"the bookmark and does not trigger the vendor's rules, while `newReference` triggers " +
		"rules and creates no bookmark. Body is newBookmarkBody — header and devicePath are " +
		"required, timeTriggered is the instant the operator should open at, and the vendor " +
		"derives timeBegin/timeEnd from it when they are absent. The answer is " +
		"{\"array\":[{...,\"reference\":<bookmark id>}]}.",
}

// systemTimeRoute is the clock route.
//
// It used to be a second, independent declaration of the same path that the Routes
// table also declares, and nothing tied the two together: they could disagree and
// every test would still pass, because each only ever compared itself with its own
// copy. Two copies of one fact is the same defect class as a route with no verdict
// in the audit — a thing the code states that nothing checks — so it is one
// declaration now, looked up by path, and route_audit_test.go says so.
//
// It is Unverified for the same reason every other route is, and it has one more:
// neither published document declares a clock endpoint at all. See
// docs/vendor/milestone/route-audit.md §2.7.
var systemTimeRoute = mustFindRoute(APIPrefix + "/System/Time")

// mustFindRoute resolves a path that is declared in Routes, so a typo is a
// startup panic rather than a route that silently addresses nothing.
func mustFindRoute(path string) Route {
	for _, r := range Routes {
		if r.Path == path {
			return r
		}
	}
	panic("milestone: no route is declared at " + path)
}

// newBookmarkBody is the documented request body for a bookmark
// (`bookmarks-rest-v1.openapi.yaml:424-457`).
//
// A struct rather than a map, for the reason the hafnia client has the same
// rule: a request that quietly grows a field is a request whose wire shape
// nobody has reviewed, and the reviewer is a customer with a security review.
//
// The shape it replaces sent `bookmarks`, `idempotencyKey`, `deviceRef`, `time`
// and `label`. **None of those five field names exists in the published body**,
// and the vendor requires `header` and `devicePath`, so the request would have
// been refused before a bookmark could have failed to land. The field names below
// are the documented ones, in the documented spelling.
type newBookmarkBody struct {
	// Header is the bookmark's title in the operator's list. Required by the
	// vendor (:456).
	Header string `json:"header"`
	// Description is the longer text. Optional, and prose.
	Description string `json:"description,omitempty"`
	// TimeTriggered is the instant the operator should open at.
	//
	// It is the only time this body carries. The vendor documents that when
	// `timeBegin` or `timeEnd` is absent the `timeTriggered` is used for them
	// (:428-430), so sending one timestamp gets a well-formed bookmark without
	// this code inventing a clip duration it has no basis for.
	TimeTriggered string `json:"timeTriggered,omitempty"`
	// DevicePath names the camera. Required by the vendor (:457).
	DevicePath devicePathRef `json:"devicePath"`
}

// devicePathRef is the documented `{type, id}` device reference
// (`bookmarks-rest-v1.openapi.yaml:224-235`): `type` is the resource collection,
// `cameras` for a camera, and `id` is a uuid.
type devicePathRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// defaultDeviceType is assumed when the reference is a bare uuid.
//
// The events reference says a camera source "can be cameras/{id} or simply {id}"
// (`events-rest-v1.reference.md:409`), so a bare id is a documented form and the
// only question is which collection it names. A camera is the only device type
// this adapter reads, so that is the assumption, and it is an assumption rather
// than a fact — a bare microphone uuid would be sent as a camera.
const defaultDeviceType = "cameras"

// parseDevicePath reads a Milestone device reference into the documented pair.
//
// It refuses a reference that is not one rather than passing it through. The
// adapter's own SiteRef used to be a display string such as
// "Perimeter North [CAM-014]", and a required field carrying a value the vendor
// does not recognise is a 400 the caller would have to reverse-engineer from an
// error body — while a refusal here names the cause before any bytes leave, which
// is what requireToken does for credentials and for the same reason.
//
// Accepted: `cameras/<uuid>`, `microphones/<uuid>`, `speakers/<uuid>`, and a bare
// uuid.
//
// The uuid check is an **assumption**, not a certainty: the documented schema
// says `id` is `format: uuid` (`:233-235`) and this build enforces that, so a site
// whose device ids are not uuids has bookmarks refused rather than sent and
// refused. The refusal names the cause, which is the point — a 400 with no
// explanation is the outcome being avoided — and the probe is where the assumption
// is settled.
func parseDevicePath(ref string) (devicePathRef, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return devicePathRef{}, fmt.Errorf("%w: no device reference", ErrWriteBack)
	}
	kind, id := "", trimmed
	if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
		kind, id = trimmed[:slash], trimmed[slash+1:]
	}
	if !isUUID(id) {
		return devicePathRef{}, fmt.Errorf("%w: %q is not a device reference. This API names a "+
			"device as \"cameras/<uuid>\"; a camera's display name is not one, and sending it "+
			"would be a refusal from the vendor with no explanation",
			ErrWriteBack, clip(ref, 80))
	}
	if kind == "" {
		return devicePathRef{Type: defaultDeviceType, ID: id}, nil
	}
	switch kind {
	case "cameras", "microphones", "speakers":
		return devicePathRef{Type: kind, ID: id}, nil
	default:
		return devicePathRef{}, fmt.Errorf("%w: %q names the device collection %q, and the "+
			"documented collections are cameras, microphones and speakers", ErrWriteBack, clip(ref, 80), kind)
	}
}

// isUUID is the shape check on a bare device id: 32 hex digits in the 8-4-4-4-12
// arrangement. It is a shape test and not a version or variant test, because the
// only thing being decided is whether this value is a uuid at all.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}

func (t *httpTransport) WriteBookmark(ctx context.Context, b Bookmark) (BookmarkRef, error) {
	if err := b.validate(); err != nil {
		return BookmarkRef{}, err
	}
	if err := t.requireToken(); err != nil {
		return BookmarkRef{}, err
	}
	device, err := parseDevicePath(b.SiteRef)
	if err != nil {
		return BookmarkRef{}, err
	}
	body, err := json.Marshal(newBookmarkBody{
		Header:        b.Label,
		Description:   b.Description,
		TimeTriggered: b.Time.UTC().Format(time.RFC3339),
		DevicePath:    device,
	})
	if err != nil {
		return BookmarkRef{}, fmt.Errorf("milestone: encode the bookmark: %w", err)
	}

	//nolint:nilerr // a non-2xx is a refusal, not a transport failure, and the
	// branch below turns it into a named one
	resp, err := t.post(ctx, bookmarkRoute, body, b.IdempotencyKey)
	if err != nil {
		return BookmarkRef{}, err
	}

	ref, err := parseBookmarkRef(resp)
	if err != nil {
		return BookmarkRef{}, err
	}
	ref.Replayed = resp.Status == http.StatusOK && idInHeader(resp.Header, b.IdempotencyKey)
	return ref, nil
}

// parseBookmarkRef reads the vendor's answer.
//
// A 200 that carries no bookmark id is not a success. "The call returned 200" and
// "a bookmark exists" are different claims, and a bookmark that silently did not
// land is an operator who finds nothing when they follow the incident.
//
// The identity field is `reference`, described in the vendor's own schema as
// "Bookmark ID" (`:408-410`), reachable two documented ways: `array[0].reference`
// for a task-less POST, whose answer is a `bookmarkArrayBody` (`:380-386`), and
// `result.reference` for `task=newReference` (`:211-223`). It used to read `id`
// and `bookmark.id`, neither of which the published answer contains, so a
// successful write was parsed as a failure.
func parseBookmarkRef(resp wireResponse) (BookmarkRef, error) {
	var wire struct {
		Array []struct {
			Reference string `json:"reference"`
		} `json:"array"`
		Result struct {
			Reference string `json:"reference"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Body, &wire); err != nil {
		return BookmarkRef{}, fmt.Errorf("milestone: the bookmark answer is not readable: %w", err)
	}
	id := wire.Result.Reference
	for _, item := range wire.Array {
		if strings.TrimSpace(item.Reference) != "" {
			id = item.Reference
			break
		}
	}
	if id == "" {
		return BookmarkRef{}, fmt.Errorf("%w: the vendor answered %d with no bookmark id, so the operator "+
			"will find nothing", ErrWriteBack, resp.Status)
	}
	return BookmarkRef{ID: id}, nil
}

func idInHeader(h http.Header, key string) bool {
	if h == nil {
		return false
	}
	return h.Get("Idempotency-Key") == key
}

func (b Bookmark) validate() error {
	switch {
	case strings.TrimSpace(b.IdempotencyKey) == "":
		// A bookmark with no key cannot be made idempotent, and a duplicate
		// bookmark is an operator-visible defect rather than a silent one.
		return errors.New("milestone: a bookmark idempotency key is required")
	case strings.TrimSpace(b.SiteRef) == "":
		return errors.New("milestone: a bookmark device reference is required")
	case b.Time.IsZero():
		return errors.New("milestone: a bookmark needs a time; one without is a bookmark at an unknown second")
	}
	return nil
}

// requireToken refuses a write before any bytes leave, rather than letting the
// vendor answer 401.
//
// The reason is not politeness. A write that is refused by the vendor is a fact
// this product then has to represent, and a fact it represents as "the bookmark
// could not be placed" when the real cause was "this process was never
// configured with credentials" sends an operator to look at the wrong system.
func (t *httpTransport) requireToken() error {
	if strings.TrimSpace(t.token) == "" {
		return fmt.Errorf("%w: no Milestone token is configured, so the write was not attempted",
			ErrWriteBack)
	}
	return nil
}

func (t *httpTransport) ServerTime(ctx context.Context) (ServerTime, error) {
	if err := t.requireToken(); err != nil {
		return ServerTime{}, err
	}
	started := time.Now()

	resp, err := t.get(ctx, systemTimeRoute)
	if err != nil {
		return ServerTime{}, err
	}
	elapsed := time.Since(started)

	var wire struct {
		// Several spellings, because this endpoint's shape is exactly what the
		// live probe has to confirm. Reading all of them is what makes a shape
		// mismatch a nil time and a named probe failure rather than a zero.
		DateTime    string `json:"dateTime"`
		Time        string `json:"time"`
		CurrentTime string `json:"currentTime"`
		ServerTimeS string `json:"serverTime"`
	}
	if err := json.Unmarshal(resp.Body, &wire); err != nil {
		// Wrapped, not replaced: a caller branching on ErrClockProbe has to be
		// able to tell "the vendor's clock is unreadable" from "the vendor is
		// down", and only one of those is a clock problem.
		return ServerTime{}, fmt.Errorf("%w: the clock answer is not readable: %w", ErrClockProbe, err)
	}
	raw := firstNonEmpty(wire.DateTime, wire.Time, wire.CurrentTime, wire.ServerTimeS)
	if raw == "" {
		return ServerTime{}, fmt.Errorf("%w: the clock answer carried no time in any shape this build knows; "+
			"the route is Unverified and needs a live probe", ErrClockProbe)
	}
	at, err := parseServerTime(raw)
	if err != nil {
		return ServerTime{}, err
	}
	return ServerTime{At: at.UTC(), RoundTrip: elapsed}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// serverTimeLayouts are the shapes a clock endpoint is written in, most specific
// first. It is the same list the event parser uses, kept here so a clock reading
// and an event timestamp are read the same way - a window computed from one and
// a bookmark placed by the other are only comparable if they were parsed alike.
var serverTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02 15:04:05",
}

func parseServerTime(raw string) (time.Time, error) {
	for _, layout := range serverTimeLayouts {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q is not a timestamp in any shape this build knows", ErrClockProbe, clip(raw, 60))
}

// SampleDrift measures the vendor's clock against ours.
//
// One reading, not a trend. A single sample is a fact and a trend is a
// conclusion; a caller that wants the conclusion should take several and say so,
// because a one-sample "drift" that happens to land on a bad network is not a
// clock problem and treating it as one sends an operator to look at the wrong
// thing.
func SampleDrift(ctx context.Context, probe ClockProbe, ours func() time.Time) (Drift, error) {
	if probe == nil {
		return Drift{}, fmt.Errorf("%w: no clock probe on this deployment", ErrClockProbe)
	}
	if ours == nil {
		ours = func() time.Time { return time.Now().UTC() }
	}
	reading, err := probe.ServerTime(ctx)
	if err != nil {
		return Drift{}, err
	}
	// Half the round trip is the correction: the vendor's instant was true
	// somewhere between the send and the receive, and a reading that does not
	// discount that is off by up to half the link's latency in the wrong
	// direction.
	before := reading.RoundTrip / 2
	return Drift{
		Offset:  reading.At.Add(before).Sub(ours()),
		Sampled: ours(),
	}, nil
}

// post and get share the request path, so the write-back surface cannot develop
// its own URL construction, its own auth or its own redirect policy.
//
// They return bytes and headers rather than an *http.Response, and that is a
// deliberate shape: a caller holding a response body has to remember to close it,
// and a write-back call has no reason to stream. Returning bytes makes a leaked
// connection impossible to express rather than merely discouraged.
//
// The idempotency key is a parameter rather than a field on the transport. A
// field would be a data race the moment two incidents were written back
// concurrently, and it would also be wrong on the second call even
// single-threaded - the transport would still be carrying the previous incident's
// key.
func (t *httpTransport) post(ctx context.Context, route Route, body []byte, idempotencyKey string) (wireResponse, error) {
	return t.do(ctx, http.MethodPost, route, body, idempotencyKey)
}

func (t *httpTransport) get(ctx context.Context, route Route) (wireResponse, error) {
	return t.do(ctx, http.MethodGet, route, nil, "")
}

// wireResponse is a read-and-closed response.
type wireResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

func (t *httpTransport) do(ctx context.Context, method string, route Route, body []byte, idempotencyKey string) (wireResponse, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	endpoint, err := t.urlFor(route, method, Query{})
	if err != nil {
		return wireResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return wireResponse{}, fmt.Errorf("milestone: build the %s: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The key travels in a header as well as the body, because a vendor that
	// honours headers and ignores the body is exactly the case where a retry
	// creates a second bookmark. The header costs nothing when it is ignored.
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return wireResponse{}, fmt.Errorf("milestone: %s %s: %w", method, route.Path, err)
	}
	// Closed on every path out of here, including the early return below, by one
	// defer rather than by remembering at each branch. Drained first so the
	// connection is reusable; the read is bounded because the body is
	// untrusted and the read path already bounds it.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxBodyBytes))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return wireResponse{}, describeStatus(route, resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return wireResponse{}, fmt.Errorf("milestone: read the answer for %s: %w", describe(route), err)
	}
	return wireResponse{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// urlForWrite no longer exists: URL construction is one function, in
// transport.go, so the write surface cannot develop its own. It used to live here
// and it is recorded here because of what it got wrong — it appended `pageSize=1`
// to every GET, which is not a parameter either published document declares, and
// it knew nothing about the vendor's `task` query parameter, which is how a POST
// selects its operation.

func describeStatus(route Route, resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s answered %d", ErrWriteBack, route.Path, resp.StatusCode)
	case http.StatusConflict:
		// A conflict on a write is most often "one with that key already exists",
		// which for a bookmark is the success case.
		return fmt.Errorf("%w: %s answered 409; a bookmark with this idempotency key likely exists",
			ErrWriteBack, route.Path)
	default:
		return fmt.Errorf("%w: %s answered %d", ErrWriteBack, route.Path, resp.StatusCode)
	}
}
