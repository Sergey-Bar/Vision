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
// write to the database and does not build canonical events — that is the
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
	}
}

// WithClock pins the clock. Used by the capture harness so a capture's
// received_at is real while the scenario's own epoch stays synthetic.
func (a *Adapter) WithClock(clock func() time.Time) *Adapter {
	a.clock = clock
	return a
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
}

// deviceRefPattern matches the bracketed identifier Milestone conventionally
// appends to a source name, as in "Entrance Camera 01 [CAM-001]".
var deviceRefPattern = regexp.MustCompile(`\[([A-Za-z0-9._:-]{1,64})\]`)

// sourceDeviceRef extracts the device identifier from an envelope.
//
// Three shapes are accepted, in the order they are tried: a bracketed id, a
// dedicated device field, and finally the bare source name. The bracketed form
// is a convention rather than a documented guarantee, which is why the dedicated
// field is tried first where one exists.
func sourceDeviceRef(item map[string]any) string {
	if d := stringField(item, "DeviceId", "deviceId", "DeviceID", "CameraId", "cameraId"); d != "" {
		return d
	}
	src := stringField(item, "Source", "source", "SourceName", "sourceName")
	if src == "" {
		return ""
	}
	if m := deviceRefPattern.FindStringSubmatch(src); len(m) == 2 {
		return m[1]
	}
	// A bare source name with no bracketed id is not a device reference. Treating
	// a human-readable label as an id would create a device row per camera
	// label and attach events to the wrong thing when a label is reworded.
	return ""
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
//   - Five types the normalizer does emit — recording.started, recording.stopped,
//     access.granted, access.denied, gate — were not keys at all.
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
				SourceEventID:   id,
				EventType:       adapterFeedErrorType,
				SiteID:          a.siteID,
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
// is a loop. The bound is generous for a 200-per-page feed — 40,000 events in
// one poll — and what happens at the bound is the important part: the feed is
// reported as truncated rather than silently short, because the alternative is
// the cursor jumping past events that were never read, which is the one failure
// this whole function exists to prevent.
const maxFeedPages = 200

func (a *Adapter) readFeed(ctx context.Context, route Route, since *time.Time) ([]RawRecord, error) {
	// Follow the cursor to the end of the feed.
	//
	// This read exactly one page and then stopped. `transport.Get` captures the
	// `Next-Cursor` header into `Response.Headers` — deliberately, and with a
	// comment saying so — and nothing ever followed it. The poll then advanced
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
			Since: since, Limit: 200, Cursor: cursor,
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

		next := ""
		if resp != nil {
			next = resp.Headers["Next-Cursor"]
		}
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
func (a *Adapter) recordFeedError(ctx context.Context, route Route, id string, cause error) {
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
			// unreadable item at position 3 of page 2 were given the same id — and
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
func (a *Adapter) unparsedItemID(route Route, item map[string]any) string {
	sum := sha256.Sum256([]byte(a.integrationID + "\x00" + route.Path + "\x00" + canonicalItemBytes(item)))
	return a.integrationID + ":" + route.Path + ":unparsed:" + hex.EncodeToString(sum[:])[:16]
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
// and device reference — none of which are columns on the inbox row. Re-deriving
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
	// The type field alone is not always enough. An alarm arrives as "Alarm"
	// with a changing AlarmState, and a device arrives as "Servlets.State"
	// with a changing State; the transition is the event, so it is derived here
	// rather than left for the normalizer to guess at from a payload.
	eventType := deriveEventType(rawType, item)

	observed, tz := parseMilestoneTime(item)

	// The envelope is the sanitized body. It was parsed into a map to redact it,
	// so it is re-encoded rather than copied: the re-encoding is canonical —
	// encoding/json sorts map keys — and therefore round-trips, which is what
	// lets the consume loop re-derive every derived field from the stored bytes
	// weeks later and get the same answers.
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
		// and the whole ingest path is dead while every unit test passes — which
		// is what happened: the field was declared, documented and never assigned.
		Envelope: envelope,
		// The source names its device in the Source field, conventionally as
		// "Entrance Camera 01 [CAM-001]". Resolving it here rather than leaving
		// the raw string in the payload is what lets the graph attach the event
		// to a device, and therefore lets any rule that filters on device match.
		// An unresolvable reference is left empty: the event still arrives, and
		// is unattributable rather than attributed to something guessed.
		SourceDeviceRef: sourceDeviceRef(item),
	}

	if !KnownMilestoneEventTypes[eventType] {
		// Carried, not dropped. An unknown type is a source change we want to
		// see, and the backpressure metric will show it.
		rec.Malformed = true
		rec.MalformedReason = fmt.Sprintf("unrecognised source event type %q", rawType)
	}
	return rec, nil
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
	return normalizeEventType(rawType)
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

	// A paged envelope is assumed to wrap the array. Both shapes are handled
	// because which one Milestone actually uses is one of the unverified
	// assumptions, and `securityctl adapter probe` exists to settle it.
	var page struct {
		Items  []map[string]any `json:"Items"`
		Data   []map[string]any `json:"Data"`
		Values []map[string]any `json:"values"`
		Total  *int             `json:"TotalCount"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, &MalformedError{Route: route, Why: "body is neither an array nor a known page envelope"}
	}
	switch {
	case page.Items != nil:
		return page.Items, nil
	case page.Data != nil:
		return page.Data, nil
	case page.Values != nil:
		return page.Values, nil
	}
	return nil, &MalformedError{Route: route, Why: "page envelope contained no recognisable item array"}
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
// A naive timestamp is treated as UTC for storage — the database stores UTC
// everywhere — but SourceTimezone comes back empty, so a downstream reader can
// tell the difference between a source that declared a zone and one that did
// not. That difference decides how a DST-spanning window is reasoned about.
//
// The reported zone is the one the source wrote, not whatever Go infers. A
// source that says "+02:00" has said nothing about which country it is in, and
// a host in Israel would otherwise have every foreign timestamp recorded as
// IST — which looks like a fact and is an assumption.
func parseMilestoneTime(item map[string]any) (time.Time, string) {
	raw := stringField(item, "Timestamp", "timestamp", "Time", "time",
		"CreationTime", "EventTime", "ObservedAt", "observedAt")
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
