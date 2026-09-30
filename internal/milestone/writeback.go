package milestone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Replayed bool
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

// bookmarkRoute is the write-back route. It is Unverified until a live probe
// confirms it, and the plan's P0.5 is exactly that probe - so this stays marked
// rather than being quietly promoted to verified by a passing test.
var bookmarkRoute = Route{
	Method: "POST", Path: "/api/v1/Bookmarks",
	Expect: "object", WriteBack: true, Unverified: true,
}

// systemTimeRoute is the clock route, for the same reason.
var systemTimeRoute = Route{
	Method: "GET", Path: "/api/v1/System/Time",
	Expect: "object", ClockProbe: true, Unverified: true,
}

// bookmarkPayload is the exact request body for a bookmark.
//
// A struct rather than a map, for the reason the hafnia client has the same
// rule: a request that quietly grows a field is a request whose wire shape
// nobody has reviewed, and the reviewer is a customer with a security review.
type bookmarkPayload struct {
	Bookmarks []bookmarkItem `json:"bookmarks"`
}

type bookmarkItem struct {
	// IdempotencyKey goes in a header rather than the body where the vendor
	// accepts it, and is repeated here because the body shape is what a live
	// probe has to confirm.
	IdempotencyKey string `json:"idempotencyKey"`
	DeviceRef      string `json:"deviceRef"`
	Time           string `json:"time"`
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
}

func (t *httpTransport) WriteBookmark(ctx context.Context, b Bookmark) (BookmarkRef, error) {
	if err := b.validate(); err != nil {
		return BookmarkRef{}, err
	}
	if err := t.requireToken(); err != nil {
		return BookmarkRef{}, err
	}
	body, err := json.Marshal(bookmarkPayload{Bookmarks: []bookmarkItem{{
		IdempotencyKey: b.IdempotencyKey,
		DeviceRef:      b.SiteRef,
		Time:           b.Time.UTC().Format(time.RFC3339),
		Label:          b.Label,
		Description:    b.Description,
	}}})
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
func parseBookmarkRef(resp wireResponse) (BookmarkRef, error) {
	var wire struct {
		ID       string `json:"id"`
		Bookmark struct {
			ID string `json:"id"`
		} `json:"bookmark"`
	}
	if err := json.Unmarshal(resp.Body, &wire); err != nil {
		return BookmarkRef{}, fmt.Errorf("milestone: the bookmark answer is not readable: %w", err)
	}
	id := wire.ID
	if id == "" {
		id = wire.Bookmark.ID
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
	endpoint, err := t.urlForWrite(route, method)
	if err != nil {
		return wireResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
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

// urlForWrite builds a write-side URL, keeping the base-URL trailing-slash
// handling in one place.
func (t *httpTransport) urlForWrite(route Route, method string) (string, error) {
	u := *t.base
	u.Path = strings.TrimRight(t.base.Path, "/") + route.Path
	if method == http.MethodGet {
		q := url.Values{}
		q.Set("pageSize", "1")
		u.RawQuery = q.Encode()
	}
	if u.Host == "" {
		return "", errors.New("milestone: the base URL has no host")
	}
	return u.String(), nil
}

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
