package milestone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Adapter turns source envelopes into normalized source records. It does not
// write to the database and does not build canonical events - that is the
// inbox normalizer's job, and keeping the two apart is what makes
// kill -9 mid-pipeline testable: the adapter's whole responsibility is "read,
// sanitize, hand over", and its output is durable the moment it is written.
type Adapter struct {
	transport Transport
	// clock is injectable so a test can pin "now" and a capture can be
	// timestamped without the scenario's own epoch leaking into it.
	clock func() time.Time
	// siteID and integrationID scope every record this adapter produces. The
	// tenant is never set here: it comes from the runtime session, and a
	// transport-supplied tenant would be a tenant read from a file.
	siteID        string
	integrationID string
	// eventTypes is this site's own GUID -> type-name map, read from
	// `/eventTypes` once per poll. It is per-adapter and that is per-site by
	// construction: the adapter is built once for one site in `security-core`,
	// so this is the per-(tenant, site) cache the route needs, and no global
	// map can leak one customer's vocabulary into another's.
	eventTypes *siteEventTypes
	// Logf receives feed-level faults. It never receives event bodies, credentials
	// or anything the source sent beyond the route that failed.
	//
	// It exists because the alternative was a feed outage that produced no inbox
	// row, no dead letter and no counter: `pipeline.Poll` drops a record with no
	// source id, and the outage record used to have none.
	Logf func(format string, args ...any)
}

// New builds an adapter over a transport.
func New(t Transport, siteID, integrationID string) *Adapter {
	return &Adapter{
		transport:     t,
		clock:         func() time.Time { return time.Now().UTC() },
		siteID:        siteID,
		integrationID: integrationID,
		eventTypes:    &siteEventTypes{byGUID: map[string]string{}},
	}
}

// WithClock pins the clock. Used by the capture harness so a capture's
// received_at is real while the scenario's own epoch stays synthetic.
func (a *Adapter) WithClock(clock func() time.Time) *Adapter {
	a.clock = clock
	return a
}

// WriteBookmark delegates to the underlying transport if it implements BookmarkWriter.
// This allows the Adapter to serve as a BookmarkWriter for the evidence fetch worker.
func (a *Adapter) WriteBookmark(ctx context.Context, b Bookmark) (BookmarkRef, error) {
	if w, ok := a.transport.(BookmarkWriter); ok {
		return w.WriteBookmark(ctx, b)
	}
	return BookmarkRef{}, fmt.Errorf("milestone: the underlying transport does not support bookmark write-back")
}

// RawRecord is one sanitized source envelope, ready for the inbox. It is the
// adapter's only output type: one shape whether it came from a socket or a file.
type RawRecord struct {
	TenantID       string
	SiteID         string
	IntegrationID  string
	SourceSystemID string
	// SourceEventID is the source's own id. Uniqueness is scoped to
	// (tenant, integration, source_event_id), so a re-read of the same event
	// after a reconnect is a duplicate insert the inbox turns into a no-op.
	SourceEventID string
	EventType     string
	// ObservedAt is what the source claimed, converted to UTC. The source's own
	// timezone is kept separately because a site with a DST transition inside an
	// incident window cannot be reasoned about otherwise.
	ObservedAt     time.Time
	SourceTimezone string
	ClockSource    string
	ReceivedAt     time.Time
	// Envelope is the sanitized body, byte-identical to what the source sent
	// after redaction. It is stored verbatim in source_events; the canonical
	// event is a normalization of it, never a replacement.
	Envelope json.RawMessage
	// SourceSystemTimezone records the VMS's declared timezone when it declared
	// one, and is empty when it did not. A missing timezone is recorded as
	// missing rather than assumed UTC.
	Malformed bool
	// MalformedReason explains why, when Malformed is set. A malformed envelope
	// still has to be stored: the platform must be able to show an operator that
	// a source sent something unreadable, which is a source fault, not a reason
	// to lose the event.
	MalformedReason string
	// SourceDeviceRef is the device identifier the source named, resolved from
	// the envelope's Source field. Empty when the source named no device or
	// named one this platform has not registered.
	SourceDeviceRef string
	// SourceTypeName is the name *this site* gives the event's type, read from
	// that site's own `/eventTypes` when the `type` field is a GUID.
	//
	// Empty when it did not resolve, which is a state and not a default: a site
	// that does not deploy the route, or a GUID the site did not answer for, both
	// leave this empty and both leave the event flagged Malformed with the reason
	// quoted. See eventtypes.go.
	//
	// It is carried rather than consumed here because mapping a name onto a
	// canonical type is a human decision (`AGENTS.md`), and this is the input to
	// it.
	SourceTypeName string
}

// deviceRefPattern matches the bracketed identifier Milestone conventionally
// appends to a source name, as in "Entrance Camera 01 [CAM-001]".
var deviceRefPattern = regexp.MustCompile(`\[([A-Za-z0-9._:-]{1,64})\]`)

// restResourcePattern matches the REST resource string this API uses to name a
// device, as in `cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c`.
//
// This is the **documented** form. `events-rest-v1.reference.md:291` shows
// `"source": "cameras/e028f597-…"` on a real event and :409 states that a camera's
// may also be written as a bare `{id}`, so both occur and both have to parse.
//
// The bracketed convention above is not documented anywhere. It was assumed, and
// because it was assumed and nothing tested it against a documented body, every
// event from a real server arrived with no device attached — which reads as a
// quiet site rather than as a parser that was wrong. See
// docs/vendor/milestone/route-audit.md §3.
var restResourcePattern = regexp.MustCompile(`^([a-z][a-z0-9]{0,31})/([A-Za-z0-9._:-]{1,128})$`)

// sourceDeviceRef extracts the device identifier from an envelope.
//
// Four shapes are accepted, in the order they are tried: a dedicated device field,
// the documented REST resource string, a bracketed id, and — nothing else. A bare
// human-readable label is refused, because treating a label as an id would create a
// device row per camera label and attach events to the wrong thing the day someone
// rewords it.
//
// For the REST form the **id alone** is returned, not the whole `cameras/<uuid>`.
// The collection is the vendor's routing information, not an identifier: the
// device row's `source_device_id` holds the uuid, and
// `pipeline.Loops.resolveDevice` matches on exactly that. Keeping the prefix would
// mean matching against a value the rest of the platform never stores.
//
// What the collection does carry — this is a camera and not a microphone — is left
// to the device row's own `kind`. One place owns that mapping, and a second one
// built from a string prefix would be a second place to be wrong.
func sourceDeviceRef(item map[string]any) string {
	if d := stringField(item, "DeviceId", "deviceId", "DeviceID", "CameraId", "cameraId"); d != "" {
		return d
	}
	src := stringField(item, "Source", "source", "SourceName", "sourceName")
	if src == "" {
		return ""
	}
	if m := restResourcePattern.FindStringSubmatch(src); len(m) == 3 {
		return m[2]
	}
	if m := deviceRefPattern.FindStringSubmatch(src); len(m) == 2 {
		return m[1]
	}
	// A bare source name with no bracketed id is not a device reference. Treating
	// a human-readable label as an id would create a device row per camera
	// label and attach events to the wrong thing when a label is reworded.
	//
	// A bare *uuid* is a device reference, and the documented event carries one
	// whenever the collection prefix is omitted (:409). It is accepted on its
	// shape, not on the assumption that a label would never look like a uuid.
	if isDeviceUUID(src) {
		return src
	}
	return ""
}

// isDeviceUUID is the 8-4-4-4-12 hex shape. It is a shape test and not a version
// or variant test, because the only question being decided is whether this value
// is an identifier rather than a name.
func isDeviceUUID(s string) bool {
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

// KnownMilestoneEventTypes are the event types the adapter produces that the
// platform understands. Anything else is carried through as Malformed rather
// than dropped: an unrecognised type from a vendor we have not seen yet is
// information, and the ingest backpressure metric is the right place for it to
// show up.
//
// The table is derived from normalizeEventType and deriveEventType, not written
// alongside them. It used to be a hand-written list keyed on the vendor's own
// PascalCase spellings, which produced two failures at once:
//
//   - Twelve of its eighteen keys ("Motion", "RecordingStarted", "GateEvent",
//     "DeviceState", …) could never be reached, because the check runs against
//     the *normalized* type and normalization lowercases everything.
//   - Five types the normalizer does emit - recording.started, recording.stopped,
//     access.granted, access.denied, gate - were not keys at all.
//
// Every real motion, analytics and access event therefore arrived flagged
// `Malformed` with "unrecognised source event type", which is a source change
// the operator wants to see and is the one thing they can never stop seeing.
//
// The test in adapter_test.go asserts both directions: every type the
// normalisation functions can return is a key, and every key is reachable. A
// table that drifts from its producer again is then a failing test rather than a
// stream of false alarms.
var KnownMilestoneEventTypes = buildKnownEventTypes()

// The two synthetic types the adapter itself produces. They are not vendor
// vocabulary, and they are not malformed in the "unrecognised" sense — the
// Malformed flag on them describes the feed or the item, not the type.
const (
	adapterFeedErrorType    = "adapter_feed_error"
	adapterUnparsedItemType = "adapter_unparsed_item"
)

// buildKnownEventTypes assembles the closed set from the same source of truth
// the check compares against.
func buildKnownEventTypes() map[string]bool {
	set := map[string]bool{
		adapterFeedErrorType:    true,
		adapterUnparsedItemType: true,
	}
	for _, t := range normalizedEventTypes {
		set[t] = true
	}
	return set
}

// normalizedEventTypes is every value normalizeEventType and deriveEventType can
// return. Keeping it next to the switch that produces them is what makes the
// table above honest: a new arm in either switch is a new name here, and the
// consistency test fails if it is missing.
var normalizedEventTypes = []string{
	"motion",
	"analytics",
	// The one class the analyticData refinement produces. The M1 incident
	// class, deterministic rather than modelled: see analyticdata.go.
	"analytics.restricted_person",
	"alarm.created",
	"alarm.acknowledged",
	"alarm.closed",
	"device.camera_offline",
	"device.camera_online",
	"device_state",
	"recording.started",
	"recording.stopped",
	"bookmark",
	"metadata",
	"access.granted",
	"access.denied",
	"gate",
}

// NormalizedEventTypes returns the closed set of types the adapter produces.
// Exported so the rule DSL and its documentation name the same set.
func NormalizedEventTypes() []string {
	out := make([]string, len(normalizedEventTypes))
	copy(out, normalizedEventTypes)
	sort.Strings(out)
	return out
}

// Poll reads every feed once and returns the records in a deterministic order.
//
// Order is by (source event id, observed time), not by arrival. An adapter that
// returns whatever order the socket delivered is one whose replay mode and
// integration mode can disagree, and Definition of Done item 1 depends on them
// not.
func (a *Adapter) Poll(ctx context.Context, since *time.Time) ([]RawRecord, error) {
	// The site's own event-type vocabulary, read before any feed so that every
	// record this poll produces is typed against the same answer.
	//
	// It is first because the alternative is asking the site once per event, and
	// it is unconditional because a site whose types do not resolve is exactly the
	// site an operator most needs to be able to read the reason for. Nothing here
	// can fail a poll: the refresh records why and moves on.
	a.refreshEventTypes(ctx)

	feeds := []Route{}
	for _, r := range Routes {
		if r.Paginated {
			feeds = append(feeds, r)
		}
	}
	sort.Slice(feeds, func(i, j int) bool { return feeds[i].Path < feeds[j].Path })

	var all []RawRecord
	for _, route := range feeds {
		recs, err := a.readFeed(ctx, route, since)
		if err != nil {
			// A feed that cannot be read is recorded and the poll continues.
			// Aborting the whole poll because one endpoint is down would turn a
			// partial outage into a total ingest outage, and the SLO for ingest
			// loss counts accepted events, not whole feeds.
			//
			// The record carries a SourceEventID and a SiteID. It used to carry
			// neither, and `pipeline.Poll` drops exactly those — "record with no
			// source id was not written" — so a feed outage produced no inbox row,
			// no dead letter and no counter, while the ingest-loss SLO saw a clean
			// feed. An outage that is invisible to the thing that watches for
			// outages is not an outage that gets noticed.
			//
			// The id is derived from the route and the poll window, so a feed that
			// stays down produces one dead letter per poll rather than a new row
			// per attempt, and the operator sees it as a repeating fact rather than
			// a flood.
			id := a.feedErrorID(route, since)
			a.recordFeedError(ctx, route, id, err)
			recs = []RawRecord{{
				TenantID:        "",
				IntegrationID:   a.integrationID,
				SourceSystemID:  a.integrationID,
				SiteID:          a.siteID,
				SourceEventID:   id,
				EventType:       adapterFeedErrorType,
				ObservedAt:      time.Now().UTC(),
				SourceTimezone:  "",
				ClockSource:     "",
				ReceivedAt:      time.Now().UTC(),
				Envelope:        []byte("{}"),
				SourceDeviceRef: "",
				SourceTypeName:  "",
				Malformed:       true,
				MalformedReason: err.Error(),
			}}
		}
		all = append(all, recs...)
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].SourceEventID != all[j].SourceEventID {
			return all[i].SourceEventID < all[j].SourceEventID
		}
		return all[i].ObservedAt.Before(all[j].ObservedAt)
	})
	return all, nil
}

// maxFeedPages bounds one feed's pagination.
//
// A cursor that does not advance is a server bug, and a cursor that never ends
// is a loop. The bound is generous for a 200-per-page feed - 40,000 events in
// one poll - and what happens at the bound is the important part: the feed is
// reported as truncated rather than silently short, because the alternative is
// the cursor jumping past events that were never read, which is the one failure
// this whole function exists to prevent.
const maxFeedPages = 200

func (a *Adapter) readFeed(ctx context.Context, route Route, since *time.Time) ([]RawRecord, error) {
	// A route the vendor selects with a `task` query parameter cannot be read with
	// a GET, so it goes through the optional task surface.
	//
	// A transport without one is a replay, and a replay serves recorded bytes by
	// path - which is what it has always done, and is the whole reason this surface
	// is optional rather than a fourth method on Transport. The live transport
	// implements it, so this fallback is only ever taken by a fixture.
	if route.Task != "" {
		if searcher, ok := a.transport.(BookmarkSearcher); ok {
			return a.readTaskedFeed(ctx, searcher, route, since)
		}
	}
	// Follow the cursor to the end of the feed.
	//
	// This read exactly one page and then stopped. `transport.Get` captures the
	// `Next-Cursor` header into `Response.Headers` — deliberately, and with a
	// comment saying so - and nothing ever followed it. The poll then advanced
	// `integrations.last_success_at` to the newest received_at of the records it
	// did read, so the next poll asked for `since = now` and items 201..N were
	// never requested again.
	//
	// That is invariant 1: accepted events, silently and permanently, with no
	// dead letter, because the loop never learned they existed. The cursor is
	// carried in the query so the server, not the client, decides what "the next
	// page" means.
	var (
		out    []RawRecord
		cursor string
		seen   = map[string]bool{}
	)
	for page := 0; page < maxFeedPages; page++ {
		resp, err := a.transport.Get(ctx, route, Query{
			// The documented page size, not a number typed here: the vendor's range
			// is 1..2000 and a value outside it is a request it may refuse.
			Since: since, Limit: DefaultPageSize, Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		// The status is checked here rather than only inside Parse, because "this
		// feed is not deployed on this site" and "this feed is failing" are very
		// different states and only the first one is expected. A transport that
		// returns a 404 response rather than a 404 error must not turn a partial
		// Milestone installation into a stream of alerts.
		if resp != nil && resp.Status == 404 {
			return nil, nil
		}
		recs, err := a.Parse(route, resp)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)

		next := nextLink(resp)
		if next == "" {
			return out, nil
		}
		if seen[next] {
			// The server is handing back a cursor it has already given us, so
			// following it would loop for ever. Stopping is the only safe answer,
			// and saying so is what makes it safe.
			return out, fmt.Errorf("milestone: %s returned the pagination cursor %q twice; "+
				"the feed was read up to that point and the rest is not readable. Refusing to "+
				"loop, and refusing to silently skip the remainder",
				describe(route), next)
		}
		seen[next] = true
		cursor = next
	}
	return out, fmt.Errorf("milestone: %s still had more pages after %d; the feed was truncated "+
		"and the unread events are NOT lost from Milestone's side — they will be read on the "+
		"next poll — but this poll's results are incomplete",
		describe(route), maxFeedPages)
}

// readTaskedFeed reads a feed the vendor selects with a `task` query parameter.
//
// One call, because the documented answer to a search carries no continuation: a
// bookmark search returns `{"array":[…]}` with no `_links`
// (`bookmarks-rest-v1.openapi.yaml:380-386`), bounded by the `countLimit` its
// search body carries. There is no second page to be truncated by, which is worth
// stating because the loop above exists precisely because a truncated feed is
// otherwise invisible.
func (a *Adapter) readTaskedFeed(ctx context.Context, s BookmarkSearcher, route Route, since *time.Time) ([]RawRecord, error) {
	resp, err := s.SearchBookmarks(ctx, Query{Since: since, Limit: DefaultPageSize})
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Status == 404 {
		return nil, nil
	}
	return a.Parse(route, resp)
}

// feedErrorID is the source event id for a feed that could not be read.
//
// Derived from the integration, the route and the poll window, so the same
// failing feed in the same window produces the same id and the inbox dedupes it.
// An id that changed on every attempt would turn a feed that has been down for
// an hour into sixty dead letters, and an operator learns to ignore that.
func (a *Adapter) feedErrorID(route Route, since *time.Time) string {
	window := "all"
	if since != nil {
		window = since.UTC().Format(time.RFC3339)
	}
	sum := sha256.Sum256([]byte(a.integrationID + "\x00" + route.Path + "\x00" + window))
	return "feed-error-" + hex.EncodeToString(sum[:])[:16]
}

// recordFeedError makes an unreadable feed visible to the audit ledger.
//
// The inbox row that carries the failure record is the thing the dead-letter
// console lists, and this is the line that makes the outage an *event* rather
// than an absence. A feed that is down and a feed that is quiet are
// indistinguishable to anything watching accepted events; they are not
// indistinguishable to an operator reading a log.
func (a *Adapter) recordFeedError(_ context.Context, route Route, id string, cause error) {
	if a.Logf != nil {
		a.Logf("milestone: feed %s could not be read and is recorded as %s: %v",
			describe(route), id, cause)
	}
}

// Parse converts one response into records. It is the single parsing path, shared
// by every transport, and it is exported so the replay harness and the tests
// exercise the same code a live server does.
func (a *Adapter) Parse(route Route, resp *Response) ([]RawRecord, error) {
	if resp == nil {
		return nil, fmt.Errorf("milestone: %s returned no response", describe(route))
	}
	switch resp.Status {
	case 401, 403:
		return nil, &UnauthenticatedError{Route: route, Status: resp.Status}
	case 404:
		return nil, &NotFoundError{Route: route}
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return nil, fmt.Errorf("milestone: %s returned status %d", describe(route), resp.Status)
	}
	if !json.Valid(resp.Body) {
		return nil, &MalformedError{Route: route, Why: "body is not JSON"}
	}

	items, err := decodeItems(route, resp.Body)
	if err != nil {
		return nil, err
	}

	now := a.clock()
	out := make([]RawRecord, 0, len(items))
	for i, item := range items {
		rec, err := a.toRecord(item, i, now)
		if err != nil {
			// One unreadable item in a page must not lose the rest of the page.
			// It becomes a malformed record with its own identity, so the
			// platform can show the operator that item arrived and was not read.
			//
			// The identity is derived from the item's own bytes, not from its
			// position in the page. The index is the index *within one page*, so
			// with pagination the unreadable item at position 3 of page 1 and the
			// unreadable item at position 3 of page 2 were given the same id - and
			// the inbox deduplicated the second away, which is one silently lost
			// event per page. Hashing the item means two genuinely distinct
			// unreadable items get distinct ids, and the same unreadable item
			// re-read on the next poll gets the same one, which is what makes it a
			// repeatable fact rather than a flood.
			out = append(out, RawRecord{
				TenantID:        a.tenant(),
				SiteID:          a.siteID,
				IntegrationID:   a.integrationID,
				SourceSystemID:  a.integrationID,
				SourceEventID:   a.unparsedItemID(route, item),
				EventType:       adapterUnparsedItemType,
				ReceivedAt:      now,
				Envelope:        []byte(`{"unparsed":true}`),
				Malformed:       true,
				MalformedReason: err.Error(),
			})
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// unparsedItemID identifies an item that arrived and could not be read, by what
// it contains rather than by where it sat.
//
// Two different unreadable items must not collide, and the same unreadable item
// read twice must produce the same id so the platform can show it as one
// outstanding fact instead of a new dead letter per poll.
// unparsedItemID identifies a page item the adapter could not parse.
//
// The hash is over the item's content because an unparseable item has no usable
// id, and a position in the page is not an identity. Captured timestamps are
// excluded from that hash: they say when the page was fetched, not what the item
// was, so including them would give the same malformed item a different
// identity on every replay - and a fixture that is not deterministic is exactly
// what the format exists to prevent.
func (a *Adapter) unparsedItemID(route Route, item map[string]any) string {
	sum := sha256.Sum256([]byte(a.integrationID + "\x00" + route.Path + "\x00" + canonicalItemBytes(withoutCapturedTimes(item))))
	return a.integrationID + ":" + route.Path + ":unparsed:" + hex.EncodeToString(sum[:])[:16]
}

// withoutCapturedTimes returns a copy of item with the timestamp fields
// replaced by a fixed marker, so that the hash is over what the item says
// rather than about when it was seen.
func withoutCapturedTimes(item map[string]any) map[string]any {
	stripped := make(map[string]any, len(item))
	for k, v := range item {
		stripped[k] = v
	}
	for _, key := range milestoneTimestampKeys {
		if _, ok := stripped[key]; ok {
			stripped[key] = "�captured-at�"
		}
	}
	return stripped
}

// canonicalItemBytes renders an item so that two structurally equal items hash
// the same and two different ones do not. encoding/json already orders map keys
// when marshalling, so this is a straight marshal of the decoded value.
func canonicalItemBytes(item map[string]any) string {
	body, err := json.Marshal(item)
	if err != nil {
		// A value decoded from JSON always marshals back. If one somehow does
		// not, the item's Go rendering is still a stable identity within this
		// process, which is better than the position-based id this replaced.
		return fmt.Sprintf("%v", item)
	}
	return string(body)
}

func (a *Adapter) tenant() string { return "" }

// RecordFromPayload re-derives a record from one stored envelope.
//
// This exists because the inbox stores the sanitized envelope and nothing else.
// The consume loop claims a row weeks after the adapter handed it over, and
// Normalize needs the derived event type, observed time, timezone, clock source
// and device reference - none of which are columns on the inbox row. Re-deriving
// them here rather than persisting them has one property worth having: the
// derivation is a pure function of the stored bytes, so a claim that is retried
// after a crash produces exactly the same canonical event, and a normalization
// bug can be fixed and re-run against the original envelope.
//
// It runs the same path `Parse` does. A second implementation would be a second
// set of answers to "what type is this", which is precisely the drift C3 was.
func (a *Adapter) RecordFromPayload(payload json.RawMessage) (RawRecord, error) {
	var item map[string]any
	if err := json.Unmarshal(payload, &item); err != nil {
		return RawRecord{}, &MalformedError{Why: "stored envelope is not a JSON object"}
	}
	now := a.clock()
	rec, err := a.toRecord(item, 0, now)
	if err != nil {
		return RawRecord{}, &MalformedError{Why: err.Error()}
	}
	// The identity fields are the row's, not the envelope's re-read: the key is
	// what the inbox claimed and what Normalize will dedupe on.
	return rec, nil
}

func (a *Adapter) toRecord(item map[string]any, index int, now time.Time) (RawRecord, error) {
	id := stringField(item, "Id", "id", "ID", "EventId", "eventId")
	if id == "" {
		// A page item with no id cannot be deduplicated, and a record that
		// cannot be deduplicated will be re-ingested on every poll.
		return RawRecord{}, fmt.Errorf("item %d has no id field", index)
	}

	rawType := stringField(item, "Type", "type", "EventType", "eventType")
	if rawType == "" {
		return RawRecord{}, fmt.Errorf("item %s has no type field", id)
	}

	// The site's own name for this type, and the one fact that makes this parse
	// re-derivable from the stored envelope alone.
	//
	// The stored annotation is read first and the site is asked second, which is
	// the whole of the replay contract: `RecordFromPayload` re-derives from bytes
	// weeks later, so it must find the name in them and must never make a call to
	// find out. An envelope that already carries the name is answered from
	// itself even after the site renames the type, because a decision replays
	// from stored facts and not from what the vendor says today.
	typeName := storedTypeName(item)
	if typeName == "" {
		if resolved, ok := a.siteEventTypeName(rawType); ok {
			typeName = resolved
			item[resolvedTypeNameKey] = typeName
		}
	}

	// The type field alone is not always enough. An alarm arrives as "Alarm"
	// with a changing AlarmState, and a device arrives as "Servlets.State"
	// with a changing State; the transition is the event, so it is derived here
	// rather than left for the normalizer to guess at from a payload.
	//
	// The name replaces the GUID here and nowhere else. `normalizeEventType` and
	// its table are untouched — this is an input, not a new mapping — so a site
	// whose names are spellings the normalizer already knows starts working on
	// its own, and one whose are not behaves exactly as before.
	eventType := deriveEventType(typeInput(rawType, typeName), item)

	observed, tz := parseMilestoneTime(item)

	// The envelope is the sanitized body. It was parsed into a map to redact it,
	// so it is re-encoded rather than copied: the re-encoding is canonical -
	// encoding/json sorts map keys - and therefore round-trips, which is what
	// lets the consume loop re-derive every derived field from the stored bytes
	// weeks later and get the same answers.
	//
	// The redaction runs here, before the marshal, and on the replay path too.
	// `analyticData.object.value` is where licence-plate text lives, and the
	// envelope is stored verbatim in source_events and is the bytes a replay
	// re-derives from — so a projection that left the plate in the stored bytes
	// would have redacted nothing. The class fields the refinement read are
	// untouched, which is what keeps the derivation identical on both paths.
	redactAnalyticPII(item)
	envelope, err := json.Marshal(item)
	if err != nil {
		return RawRecord{}, fmt.Errorf("event %s could not be re-encoded after sanitizing: %w", id, err)
	}

	rec := RawRecord{
		SiteID:         a.siteID,
		IntegrationID:  a.integrationID,
		SourceSystemID: a.integrationID,
		SourceEventID:  id,
		EventType:      eventType,
		ObservedAt:     observed,
		SourceTimezone: tz,
		ClockSource:    "vms",
		ReceivedAt:     now,
		// Without this the inbox refuses every event with "payload is required",
		// and the whole ingest path is dead while every unit test passes - which
		// is what happened: the field was declared, documented and never assigned.
		Envelope: envelope,
		// The source names its device in the Source field, conventionally as
		// "Entrance Camera 01 [CAM-001]". Resolving it here rather than leaving
		// the raw string in the payload is what lets the graph attach the event
		// to a device, and therefore lets any rule that filters on device match.
		// An unresolvable reference is left empty: the event still arrives, and
		// is unattributable rather than attributed to something guessed.
		SourceDeviceRef: sourceDeviceRef(item),
		// Carried, not consumed. See the field's own comment, and eventtypes.go:
		// which names a site uses is the human's mapping decision, and this is
		// the input to it.
		SourceTypeName: typeName,
	}

	if !KnownMilestoneEventTypes[eventType] {
		// Carried, not dropped. An unknown type is a source change we want to
		// see, and the backpressure metric will show it.
		rec.Malformed = true
		rec.MalformedReason = a.unrecognisedTypeReason(rawType, typeName)
	}
	return rec, nil
}

// typeInput is the string the type derivation reads: the site's own name when
// there is one, and the raw `type` field otherwise.
//
// The raw field is kept in the fallback rather than discarded so that an
// unresolved GUID still produces exactly the `unknown.<guid>` it always did, and
// a site that deploys nothing new is byte-for-byte unaffected.
func typeInput(rawType, typeName string) string {
	if typeName != "" {
		return typeName
	}
	return rawType
}

// storedTypeName is the site-resolved name carried in a stored envelope.
func storedTypeName(item map[string]any) string {
	return stringField(item, resolvedTypeNameKey)
}

// unrecognisedTypeReason says what the vendor called it, not only what we were
// handed.
//
// An operator opening a dead letter cannot act on `unknown.481d4d6c-…`: they
// have no way to look a GUID up, and the envelope is the only place the raw type
// survives. A name is something they can search the customer's VMS for, and it
// is what the alias table this work unblocks has to be written against.
func (a *Adapter) unrecognisedTypeReason(rawType, typeName string) string {
	if typeName != "" {
		return fmt.Sprintf("this site calls event type %q a %q, and the platform's "+
			"vocabulary does not yet name that; the type id the vendor sent is %q",
			typeName, rawType, rawType)
	}
	why := a.unresolvedReason()
	if why == "" {
		return fmt.Sprintf("unrecognised source event type %q", rawType)
	}
	return fmt.Sprintf("unrecognised source event type %q, and no name for it could be "+
		"resolved from the site: %s", rawType, why)
}

// deriveEventType refines the coarse source type using the state fields the
// feed carries alongside it.
func deriveEventType(rawType string, item map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(rawType)) {
	case "alarm":
		// An alarm is a state machine, not a type. The same source id appears
		// three times with three states, and the transition is what a rule
		// correlates on.
		switch strings.ToLower(strings.TrimSpace(stringField(item, "AlarmState", "alarmState", "State"))) {
		case "acknowledged", "acked":
			return "alarm.acknowledged"
		case "closed", "resolved":
			return "alarm.closed"
		default:
			return "alarm.created"
		}
	case "servlets.state", "servicestate", "devicestate":
		switch strings.ToLower(strings.TrimSpace(stringField(item, "State", "state", "ServiceState"))) {
		case "offline", "unavailable", "disconnected":
			return "device.camera_offline"
		case "online", "available", "connected":
			return "device.camera_online"
		default:
			return "device_state"
		}
	}
	base := normalizeEventType(rawType)
	// An analytics event whose analyticData names a class the taxonomy knows
	// becomes a typed event: `restricted-person` is the M1 incident class, and
	// the vendor's own assertion makes it a geometric test rather than a
	// modelled one — no VLM, no GPU, no 8-second inference, and a geometric
	// test cannot hallucinate a person who is not there.
	//
	// A class the taxonomy does not name keeps the generic analytics event it
	// always had. The event is not lost and nothing is guessed; the refinement
	// is a table with one entry, and a second entry is a decision with a rule
	// behind it.
	if base == "analytics" {
		if obj, ok := parseAnalyticObject(item); ok {
			if refined, ok := analyticCanonicalClass(obj); ok {
				return refined
			}
		}
	}
	return base
}

// normalizeEventType maps the vendor's PascalCase vocabulary onto the stable
// lowercase set the rule DSL names.
//
// The mapping lives here and nowhere else. A rule bundle must be portable
// between sites and between replay and integration mode, so it refers to the
// normalized names, and the vendor's spelling is an adapter detail.
func normalizeEventType(t string) string {
	lower := strings.ToLower(strings.TrimSpace(t))
	switch lower {
	case "motion", "motiondetected":
		return "motion"
	case "analytics", "objectdetected", "linecrossed", "loitering":
		return "analytics"
	case "alarm", "alarmcreated":
		return "alarm.created"
	case "alarmacknowledged", "alarmack":
		return "alarm.acknowledged"
	case "alarmclosed":
		return "alarm.closed"
	case "cameraoffline", "servicestateoffline":
		return "device.camera_offline"
	case "cameraonline", "servicestateonline":
		return "device.camera_online"
	case "recordingstarted":
		return "recording.started"
	case "recordingstopped":
		return "recording.stopped"
	case "bookmark":
		return "bookmark"
	case "metadata":
		return "metadata"
	case "accessgranted":
		return "access.granted"
	case "accessdenied":
		return "access.denied"
	case "gateevent", "gateopen", "gateclose":
		return "gate"
	case "devicestate":
		return "device_state"
	}
	return "unknown." + lower
}

func decodeItems(route Route, body json.RawMessage) ([]map[string]any, error) {
	var arr []map[string]any
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr, nil
	}

	// A paged envelope wraps the array under a name. Four names are read here
	// because which one a given build uses is one of the unverified assumptions
	// and `securityctl adapter-probe` exists to settle it.
	//
	// `array` is not one of those guesses: it is the documented one. Both
	// published answers put their entries there —
	// `{"_links":{"prev","next"},"array":[…],"eventCount":N}` for events
	// (`events-rest-v1.reference.md:377-391`) and `{"array":[…]}` for a bookmark
	// search (`bookmarks-rest-v1.openapi.yaml:380-386`). Before it was read, a
	// corrected path would have answered with a body this function could not parse
	// at all, which is the failure `routes.go` warns about: an empty feed that
	// looks like a quiet site.
	var page struct {
		Array  []map[string]any `json:"array"`
		Items  []map[string]any `json:"Items"`
		Data   []map[string]any `json:"Data"`
		Values []map[string]any `json:"values"`
		Total  *int             `json:"TotalCount"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, &MalformedError{Route: route, Why: "body is neither an array nor a known page envelope"}
	}
	switch {
	case page.Array != nil:
		return page.Array, nil
	case page.Items != nil:
		return page.Items, nil
	case page.Data != nil:
		return page.Data, nil
	case page.Values != nil:
		return page.Values, nil
	}
	return nil, &MalformedError{Route: route, Why: "page envelope contained no recognisable item array"}
}

// nextLink is the next page, from the documented place or the header.
//
// Both are read, and neither replaces the other. The documented continuation is
// `_links.next` in the body (`events-rest-v1.reference.md:377-379`); the
// `Next-Cursor` **header** appears in neither document, and is what this adapter
// used to follow and what a fixture transport still hands out. Which one a real
// build sends is a probe question, so this reads whichever arrives and treats a
// page with neither as the last page.
func nextLink(resp *Response) string {
	if resp == nil {
		return ""
	}
	if cursor := resp.Headers["Next-Cursor"]; cursor != "" {
		return cursor
	}
	var page struct {
		Links struct {
			Next string `json:"next"`
		} `json:"_links"`
	}
	if len(resp.Body) == 0 || json.Unmarshal(resp.Body, &page) != nil {
		return ""
	}
	return strings.TrimSpace(page.Links.Next)
}

func stringField(m map[string]any, names ...string) string {
	for _, n := range names {
		if v, ok := m[n]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
			if f, ok := v.(float64); ok {
				return fmt.Sprintf("%.0f", f)
			}
		}
	}
	return ""
}

// milestoneTimeLayouts are the timestamp formats assumed to appear in Milestone
// payloads, most specific first. All are parsed as UTC unless a timezone is
// present in the value; a naive timestamp is recorded with an empty timezone
// rather than being silently assumed to be UTC, because "we guessed" and "the
// source said so" must not look identical in the stored record.
var milestoneTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
}

// parseMilestoneTime extracts the observed time and the source timezone.
//
// A naive timestamp is treated as UTC for storage - the database stores UTC
// everywhere - but SourceTimezone comes back empty, so a downstream reader can
// tell the difference between a source that declared a zone and one that did
// not. That difference decides how a DST-spanning window is reasoned about.
//
// The reported zone is the one the source wrote, not whatever Go infers. A
// source that says "+02:00" has said nothing about which country it is in, and
// a host in Israel would otherwise have every foreign timestamp recorded as
// IST - which looks like a fact and is an assumption.
// milestoneTimestampKeys are the envelope fields a timestamp is read from.
//
// It is one list, used both to read a timestamp and to rebase one during replay.
// Two lists would be two chances for a replay to move a field the normalizer does
// not read, and the symptom of that is a window test that passes against a
// fixture and disagrees with a live capture.
var milestoneTimestampKeys = []string{
	"Timestamp", "timestamp", "Time", "time",
	"CreationTime", "EventTime", "ObservedAt", "observedAt",
}

func parseMilestoneTime(item map[string]any) (time.Time, string) {
	raw := stringField(item, milestoneTimestampKeys...)
	if raw == "" {
		return time.Time{}, ""
	}
	for _, layout := range milestoneTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			if zone, ok := declaredZone(raw); ok {
				return t.UTC(), zone
			}
			return t.UTC(), ""
		}
	}
	// An unparseable timestamp is not a reason to lose the event. The zero time
	// forces the normalizer to fall back to received_at and to mark the record
	// as lacking a trustworthy observation, which is visible.
	return time.Time{}, ""
}

// declaredZone extracts the zone exactly as the source wrote it.
func declaredZone(raw string) (string, bool) {
	if strings.HasSuffix(raw, "Z") {
		return "Z", true
	}
	// An offset is a sign, two digits, a colon, two digits, at the end. A bare
	// dash is a date separator and must not be mistaken for one.
	if len(raw) >= 6 {
		tail := raw[len(raw)-6:]
		if tail[0] == '+' || tail[0] == '-' {
			if isDigits(tail[1:3]) && tail[3] == ':' && isDigits(tail[4:6]) {
				return tail, true
			}
		}
	}
	if len(raw) >= 5 {
		tail := raw[len(raw)-5:]
		if tail[0] == '+' || tail[0] == '-' {
			if isDigits(tail[1:3]) && isDigits(tail[3:5]) {
				return tail[:3] + ":" + tail[3:], true
			}
		}
	}
	return "", false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
