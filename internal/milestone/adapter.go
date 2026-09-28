package milestone

import (
	"context"
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

// KnownMilestoneEventTypes are the source event types the adapter recognises.
// Anything else is carried through as Malformed rather than dropped: an
// unrecognised type from a vendor we have not seen yet is information, and the
// ingest backpressure metric is the right place for it to show up.
var KnownMilestoneEventTypes = map[string]bool{
	"Motion": true, "Analytics": true, "MotionDetected": true,
	"alarm": true, "alarm.created": true, "alarm.acknowledged": true, "alarm.closed": true,
	"device.camera_offline": true, "device.camera_online": true, "device_state": true,
	"RecordingStarted": true, "RecordingStopped": true,
	"Bookmark": true, "Metadata": true, "AccessGranted": true, "AccessDenied": true,
	"GateEvent": true, "DeviceState": true,
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
			recs = []RawRecord{{
				EventType:       "adapter_feed_error",
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

func (a *Adapter) readFeed(ctx context.Context, route Route, since *time.Time) ([]RawRecord, error) {
	resp, err := a.transport.Get(ctx, route, Query{Since: since, Limit: 200})
	if err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
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
	return a.Parse(route, resp)
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
		rec, err := a.toRecord(route, item, i, now)
		if err != nil {
			// One unreadable item in a page must not lose the rest of the page.
			// It becomes a malformed record with its own identity, so the
			// platform can show the operator that item arrived and was not read.
			out = append(out, RawRecord{
				TenantID:        a.tenant(),
				SiteID:          a.siteID,
				IntegrationID:   a.integrationID,
				SourceSystemID:  a.integrationID,
				SourceEventID:   fmt.Sprintf("%s:%s:unparsed:%d", a.integrationID, route.Path, i),
				EventType:       "adapter_unparsed_item",
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

func (a *Adapter) tenant() string { return "" }

func (a *Adapter) toRecord(route Route, item map[string]any, index int, now time.Time) (RawRecord, error) {
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
