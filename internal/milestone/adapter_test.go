package milestone_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/milestone"
)

var testEpoch = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// The documented path, so a test that feeds a body to a route is feeding it to
// the route the adapter actually calls. It used to be "/api/v1/Events", which is
// not a Milestone prefix — see docs/vendor/milestone/route-audit.md §2.1 — and
// every test in this file passed against a path no server has ever answered.
const eventsRoute = "/api/rest/v1/events"

const bookmarksRoute = "/api/rest/v1/bookmarks"

// stubTransport serves a fixed body per route, so the adapter's parsing is
// testable without a server and without a fixture set.
type stubTransport struct {
	bodies map[string]json.RawMessage
	status map[string]int
}

func (s *stubTransport) Get(_ context.Context, route milestone.Route, _ milestone.Query) (*milestone.Response, error) {
	// A status override is honoured whether or not there is a body, so a test
	// can express "this feed is deployed and failing" as distinct from "this
	// feed is not deployed here".
	if code, ok := s.status[route.Path]; ok {
		return &milestone.Response{Status: code, Route: route}, nil
	}
	body, ok := s.bodies[route.Path]
	if !ok {
		// A feed that is not deployed on this site must not look like a fault.
		return &milestone.Response{Status: 404, Route: route}, nil
	}
	return &milestone.Response{Status: 200, Body: body, Route: route}, nil
}

func (s *stubTransport) Close() error { return nil }

// SearchBookmarks makes the stub a faithful stand-in for the live transport,
// which does implement the optional task surface.
//
// Without it, every poll test in this file would exercise the replay fallback —
// `Transport.Get` on a route the vendor documents as POST-only — instead of the
// bookmark read a live adapter actually performs. The stub answers from the same
// two maps, so a test sets a body or a status once and both paths see it.
func (s *stubTransport) SearchBookmarks(ctx context.Context, _ milestone.Query) (*milestone.Response, error) {
	return s.Get(ctx, milestone.Route{Path: bookmarksRoute, Task: "searchTime"}, milestone.Query{})
}

// newAdapter builds an adapter over a stub transport, pinned to a fixed clock so
// every timestamp in the tests is deterministic.
//
// Both are returned because every test needs the adapter to poll and the stub to
// set up bodies and status codes. Splitting them would leave one of the two
// unused at every call site, which is worse than a two-value return.
func newAdapter() (*milestone.Adapter, *stubTransport) {
	stub := &stubTransport{bodies: map[string]json.RawMessage{}, status: map[string]int{}}
	return milestone.New(stub, "site-1", "int-1").
		WithClock(func() time.Time { return testEpoch }), stub
}

func poll(t *testing.T, a *milestone.Adapter) []milestone.RawRecord {
	t.Helper()
	recs, err := a.Poll(context.Background(), nil)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return recs
}

// pollOver polls through a transport directly, for the cases that exercise the
// transport rather than the adapter's own construction.
func pollOver(t *testing.T, tr milestone.Transport) []milestone.RawRecord {
	t.Helper()
	a := milestone.New(tr, "site-1", "int-1").
		WithClock(func() time.Time { return testEpoch })
	return poll(t, a)
}

func feedEvents(t *testing.T, recs []milestone.RawRecord) map[string]milestone.RawRecord {
	t.Helper()
	byID := map[string]milestone.RawRecord{}
	for _, r := range recs {
		if r.SourceEventID != "" {
			byID[r.SourceEventID] = r
		}
	}
	return byID
}

func TestPollParsesAFeed(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`[
		{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"},
		{"Id":"e2","Type":"Analytics","Timestamp":"2026-03-01T09:00:01.500Z"}
	]`)

	recs := poll(t, a)
	events := feedEvents(t, recs)
	if len(events) != 2 {
		t.Fatalf("parsed %d events, want 2 (all records: %+v)", len(events), recs)
	}

	// The vendor's PascalCase vocabulary is normalized exactly once, in the
	// adapter. A rule bundle refers to the normalized names so it is portable
	// between sites and between replay and integration mode.
	if events["e2"].EventType != "analytics" {
		t.Errorf("event type is %q, want \"analytics\"", events["e2"].EventType)
	}
	if !events["e1"].ObservedAt.Equal(testEpoch) {
		t.Errorf("observed_at is %s, want %s", events["e1"].ObservedAt, testEpoch)
	}
	if events["e1"].ReceivedAt != testEpoch {
		t.Errorf("received_at is %s, want the pinned clock", events["e1"].ReceivedAt)
	}
	if events["e1"].ClockSource != "vms" {
		t.Errorf("clock_source is %q, want \"vms\"", events["e1"].ClockSource)
	}
}

// A naive timestamp is stored as UTC but must not claim the source declared a
// timezone. The difference decides how a DST-spanning window is reasoned about,
// so conflating them is a correctness bug, not a tidiness one.
func TestTimezoneProvenanceIsPreserved(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`[
		{"Id":"z1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"},
		{"Id":"z2","Type":"Motion","Timestamp":"2026-03-01T11:00:00+02:00"},
		{"Id":"z3","Type":"Motion","Timestamp":"2026-03-01 09:00:00"},
		{"Id":"z4","Type":"Motion","Timestamp":"nonsense"}
	]`)

	byID := feedEvents(t, poll(t, a))

	if got := byID["z1"].SourceTimezone; got == "" {
		t.Error("an explicit Z timestamp recorded no timezone")
	}
	if got := byID["z2"].SourceTimezone; got != "+02:00" {
		t.Errorf("offset timestamp recorded timezone %q, want +02:00", got)
	}
	if got := byID["z3"].SourceTimezone; got != "" {
		t.Errorf("a naive timestamp claimed timezone %q; a missing zone must stay missing", got)
	}
	if !byID["z3"].ObservedAt.Equal(testEpoch) {
		t.Errorf("naive timestamp parsed to %s, want %s treated as UTC", byID["z3"].ObservedAt, testEpoch)
	}
	// An unparseable time must not be silently replaced by a plausible one. The
	// zero time forces the normalizer to fall back to received_at and to mark
	// the record as lacking a trustworthy observation, which is visible.
	if !byID["z4"].ObservedAt.IsZero() {
		t.Errorf("an unparseable timestamp became %s instead of the zero time", byID["z4"].ObservedAt)
	}
}

// An unreadable item must not lose the rest of the page. It becomes its own
// record with a reason, so an operator can be shown the source sent something the
// platform could not read.
func TestOneUnreadableItemDoesNotLoseThePage(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`[
		{"Id":"good-1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"},
		{"Type":"Motion","Timestamp":"2026-03-01T09:00:01Z"},
		{"Id":"good-2","Type":"Analytics","Timestamp":"2026-03-01T09:00:02Z"}
	]`)

	var good, unparsed int
	for _, r := range poll(t, a) {
		switch r.EventType {
		case "adapter_unparsed_item":
			unparsed++
			if !r.Malformed || r.MalformedReason == "" {
				t.Errorf("unparsed record has no reason: %+v", r)
			}
			// It must still have an identity, or it is re-read on every poll and
			// the malformed count grows without bound.
			if r.SourceEventID == "" {
				t.Error("unparsed record has no source id and would be re-ingested on every poll")
			}
		case "adapter_feed_error":
			// Routes this stub does not serve. Not part of this assertion.
		default:
			good++
		}
	}
	if good != 2 {
		t.Errorf("recovered %d good records, want 2", good)
	}
	if unparsed != 1 {
		t.Errorf("produced %d unparsed records, want 1", unparsed)
	}
}

// vendorSpellings is one example of every vendor type the normalisation
// functions recognise, with the normalized name each must produce.
//
// This is the fixture that pins the contract between the normalizer and the
// KnownMilestoneEventTypes table. It is written out rather than generated from
// the implementation, because a table generated from the implementation cannot
// detect the implementation changing.
var vendorSpellings = []struct {
	rawType string
	item    map[string]any
	want    string
}{
	{rawType: "Motion", want: "motion"},
	{rawType: "MotionDetected", want: "motion"},
	{rawType: "motion", want: "motion"},
	{rawType: "Analytics", want: "analytics"},
	{rawType: "ObjectDetected", want: "analytics"},
	{rawType: "LineCrossed", want: "analytics"},
	{rawType: "Loitering", want: "analytics"},
	{rawType: "Alarm", item: map[string]any{"State": "Created"}, want: "alarm.created"},
	{rawType: "Alarm", item: map[string]any{"State": "Acknowledged"}, want: "alarm.acknowledged"},
	{rawType: "Alarm", item: map[string]any{"State": "Closed"}, want: "alarm.closed"},
	{rawType: "AlarmCreated", want: "alarm.created"},
	{rawType: "AlarmAcknowledged", want: "alarm.acknowledged"},
	{rawType: "AlarmClosed", want: "alarm.closed"},
	{rawType: "Servlets.State", item: map[string]any{"State": "Offline"}, want: "device.camera_offline"},
	{rawType: "Servlets.State", item: map[string]any{"State": "Online"}, want: "device.camera_online"},
	{rawType: "Servlets.State", item: map[string]any{"State": "Rebooting"}, want: "device_state"},
	{rawType: "DeviceState", want: "device_state"},
	{rawType: "CameraOffline", want: "device.camera_offline"},
	{rawType: "CameraOnline", want: "device.camera_online"},
	{rawType: "RecordingStarted", want: "recording.started"},
	{rawType: "RecordingStopped", want: "recording.stopped"},
	{rawType: "Bookmark", want: "bookmark"},
	{rawType: "Metadata", want: "metadata"},
	{rawType: "AccessGranted", want: "access.granted"},
	{rawType: "AccessDenied", want: "access.denied"},
	{
		// The one class the analyticData refinement produces, reached through
		// the path production takes: an analytics event whose analyticData
		// names the class the taxonomy knows. The spelling table is not the
		// only producer — `restricted-person` is not a vendor spelling, it is
		// a class — so the vocabulary's reachability is asserted here too or
		// this guard would read the new name as drift.
		rawType: "ObjectDetected",
		want:    "analytics.restricted_person",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "RestrictedPerson", "ClassName": "restricted-person",
					},
				},
			},
		},
	},
	{rawType: "GateEvent", want: "gate"},
	{rawType: "GateOpen", want: "gate"},
	{rawType: "GateClose", want: "gate"},

	// The classes the analyticData refinement produces beyond the M1 one. Each
	// is reachable because a VMS's analytics engine asserts it, and the
	// assertion reaches the platform as an `AnalyticData` subtree naming the
	// class. They are in this fixture because a class nobody's feed produces is
	// a class the platform would refine into a typed event no incident ever
	// turns on.
	{rawType: "ObjectDetected", want: "analytics.person_with_bag",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "PersonWithBag", "ClassName": "person-with-bag",
					},
				},
			},
		}},
	{rawType: "ObjectDetected", want: "analytics.vehicle",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "Vehicle", "ClassName": "vehicle",
					},
				},
			},
		}},
	{rawType: "ObjectDetected", want: "analytics.animal",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "Animal", "ClassName": "animal",
					},
				},
			},
		}},
	{rawType: "ObjectDetected", want: "analytics.bird",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "Bird", "ClassName": "bird",
					},
				},
			},
		}},
	{rawType: "ObjectDetected", want: "analytics.drone",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "Drone", "ClassName": "drone",
					},
				},
			},
		}},
	{rawType: "ObjectDetected", want: "analytics.balloon",
		item: map[string]any{
			"AnalyticData": map[string]any{
				"Object": map[string]any{
					"Type": "ObjectDetected",
					"Value": map[string]any{
						"ClassId": "Balloon", "ClassName": "balloon",
					},
				},
			},
		}},
}

// TestEveryTypeTheAdapterProducesIsKnown is the C3 test.
//
// KnownMilestoneEventTypes used to be keyed on the vendor's PascalCase
// spellings while the check ran against the *normalized* type. Twelve of its
// eighteen keys were unreachable and five types the adapter really does produce
// were missing, so every motion, analytics and access event on a live site
// arrived flagged "unrecognised source event type".
//
// Both directions are asserted. A missing key means real events are marked
// malformed; an unreachable key means the table is claiming to recognise
// something the adapter can never emit, which is the same drift wearing the
// opposite sign.
func TestEveryTypeTheAdapterProducesIsKnown(t *testing.T) {
	reachable := map[string]bool{}

	for _, tc := range vendorSpellings {
		t.Run(tc.rawType+"/"+tc.want, func(t *testing.T) {
			a, stub := newAdapter()
			item := map[string]any{
				"Id": "e1", "Type": tc.rawType,
				"Timestamp": "2026-03-01T09:00:00Z",
			}
			for k, v := range tc.item {
				item[k] = v
			}
			body, err := json.Marshal([]map[string]any{item})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			stub.bodies[eventsRoute] = body

			recs := poll(t, a)
			var found bool
			for _, r := range recs {
				if r.SourceEventID != "e1" {
					continue
				}
				found = true
				if r.EventType != tc.want {
					t.Errorf("%q normalized to %q, want %q", tc.rawType, r.EventType, tc.want)
				}
				if r.Malformed {
					t.Errorf("%q produced %q and was flagged malformed as %q; a type the "+
						"platform understands must never be reported as unrecognised",
						tc.rawType, r.EventType, r.MalformedReason)
				}
				if milestone.KnownMilestoneEventTypes[r.EventType] {
					reachable[r.EventType] = true
				}
			}
			if !found {
				t.Fatalf("%q produced no record", tc.rawType)
			}
		})
	}

	for key := range milestone.KnownMilestoneEventTypes {
		if reachable[key] {
			continue
		}
		// The two synthetic adapter types have no vendor spelling - they are
		// produced by the feed-error and unparsed-item paths, not by
		// normalisation - so they are covered by their own tests.
		if key == "adapter_feed_error" || key == "adapter_unparsed_item" {
			continue
		}
		t.Errorf("KnownMilestoneEventTypes has key %q, which no vendor spelling in this "+
			"fixture produces; the table and the normalizer have drifted apart", key)
	}
}

// A type nobody recognises is still carried, with its name intact, and still
// flagged. This is the one case where Malformed is the correct answer: a vendor
// adding an event type is information the operator should see, not a reason to
// lose the event.
func TestUnknownEventTypeIsCarriedNotDropped(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`[
		{"Id":"u1","Type":"QuantumFlux","Timestamp":"2026-03-01T09:00:00Z"}
	]`)

	byID := feedEvents(t, poll(t, a))
	rec, ok := byID["u1"]
	if !ok {
		t.Fatal("the unrecognised event was dropped")
	}
	if !rec.Malformed {
		t.Error("an unrecognised type was not flagged")
	}
	if milestone.KnownMilestoneEventTypes[rec.EventType] {
		t.Errorf("%q is a key in KnownMilestoneEventTypes, so it was treated as known", rec.EventType)
	}
	if !strings.Contains(rec.EventType, "quantumflux") {
		t.Errorf("unrecognised type normalized to %q and lost its name", rec.EventType)
	}
}

// A documented event body, verbatim.
//
// From `events-rest-v1.reference.md:380-388`, the sample response of
// `GET /api/rest/v1/events`. Every other test in this file feeds a body in the
// shape the adapter *assumed* — `{"Id","Type":"Motion","Timestamp","Source":"Cam
// [cam-1]"}` — and a test whose fixture agrees with the code under test about a
// value the vendor contradicts is a test of nothing. This one is the vendor's own
// sample, so it is the only test in the package that can fail if the parser is
// wrong about the real thing.
const documentedEvent = `{
	"specversion": "1.0",
	"type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
	"source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
	"time": "2023-02-28T18:10:12.4786334Z",
	"id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
	"localId": 4,
	"datatype": "none",
	"tag": "My custom tag"
}`

// TestADocumentedEventIsAttributedToTheDeviceThatProducedIt.
//
// The device reference is the one field in this that needed no human decision, and
// getting it wrong was the quietest defect in the package: `sourceDeviceRef` looked
// for a bracketed id in `Source`, the documented value is `cameras/<uuid>`, so every
// event from a real server arrived with no device attached. Nothing crashed. Zone
// rules and device-filtered rules simply never matched, and that is indistinguishable
// from a site where nothing is happening.
func TestADocumentedEventIsAttributedToTheDeviceThatProducedIt(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(
		`{"_links":{"next":""},"array":[` + documentedEvent + `],"eventCount":1}`)

	byID := feedEvents(t, poll(t, a))
	rec, ok := byID["5e6a4c06-b290-473d-80b5-8e0dffa1d3a6"]
	if !ok {
		t.Fatalf("a documented event was not ingested at all. The item id is read from the "+
			"documented `id` field, so a miss here means the decoder does not read the vendor's "+
			"own body. Records seen: %d", len(byID))
	}

	// The bare uuid, not `cameras/<uuid>`: `resolveDevice` matches against
	// `devices.source_device_id`, which holds the identifier.
	const wantDevice = "e028f597-c64b-4f27-bb8d-08f5cd768e1c"
	if rec.SourceDeviceRef != wantDevice {
		t.Errorf("the event's device is %q, want %q. An empty value here is not a failure "+
			"anywhere: zone rules and device-filtered rules simply stop matching, and that "+
			"looks exactly like a site where nothing is happening",
			rec.SourceDeviceRef, wantDevice)
	}
}

// TestADocumentedEventCarriesEveryFieldItHasToCarry is the same body read for the
// rest of what it must preserve, so a parser regression names itself.
func TestADocumentedEventCarriesEveryFieldItHasToCarry(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	recs := poll(t, a)
	if len(recs) != 1 {
		t.Fatalf("got %d records from a one-item page", len(recs))
	}
	rec := recs[0]

	if rec.SourceEventID != "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6" {
		t.Errorf("SourceEventID is %q", rec.SourceEventID)
	}
	// Seven decimal places, UTC, as the reference documents (:294). RFC3339Nano is
	// the only layout that keeps them; a second-resolution format would silently
	// round an observation to the wrong second.
	if got := rec.ObservedAt.UTC().Format("2006-01-02T15:04:05.9999999Z07:00"); got != "2023-02-28T18:10:12.4786334Z" {
		t.Errorf("ObservedAt rendered as %q; the source declared 2023-02-28T18:10:12.4786334Z", got)
	}
	// The raw body is retained, so a field this build does not understand is still
	// readable by whoever has to work out what it meant. That is what makes the
	// remaining gap below a decision rather than a loss.
	if len(rec.Envelope) == 0 {
		t.Error("the raw body was not retained")
	}
	if !strings.Contains(string(rec.Envelope), `"tag"`) {
		t.Error("the retained body is not the vendor's own; a field this build does not read " +
			"has to survive for the next person to look at")
	}
}

// TestTheOnlyThingADocumentedEventCannotDoIsNameItsType pins the blast radius.
//
// `type` on a documented event is a GUID, and mapping it to a normalized type is a
// contract decision a human has to make — see
// docs/vendor/milestone/route-audit.md §3 and §5. So a documented event is flagged
// `Malformed` and dead-lettered, and that is correct behaviour: it is stored, the
// reason is on it, and nothing is silently mapped to a guess.
//
// This test exists so the *only* thing standing between the adapter and a working
// feed is named. When H1 is decided, this is the test that should be the one to
// change, and it should fail loudly if a second gap appears beside it.
func TestTheOnlyThingADocumentedEventCannotDoIsNameItsType(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	rec := poll(t, a)[0]

	if !rec.Malformed {
		t.Fatal("a GUID event type was accepted as a name. If H1 has been decided, this test " +
			"is now wrong rather than the code — update it deliberately")
	}
	if !strings.Contains(rec.MalformedReason, "481d4d6c-9156-4698-af7e-74b32f9e4359") {
		t.Errorf("MalformedReason is %q; it must quote the GUID the vendor sent, because "+
			"\"unrecognised type\" with the value in the envelope is a thing somebody has to "+
			"open the dead letter to act on", rec.MalformedReason)
	}
	// And the device still resolved, because a dead letter that names the device is
	// triageable and one that does not is not.
	if rec.SourceDeviceRef == "" {
		t.Error("a dead-lettered event lost its device reference")
	}
}

// A bare uuid with no collection prefix is a documented form of `source`
// (:409), so it has to parse. It is distinguished from a human-readable label by
// shape, which is the only thing available at this point in the pipeline.
func TestABareDeviceUUIDIsADeviceReferenceButALabelIsNot(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"cameras prefix", `{"id":"a1","type":"Motion","time":"2026-03-01T09:00:00Z",
			"source":"cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c"}`,
			"e028f597-c64b-4f27-bb8d-08f5cd768e1c"},
		{"bare uuid", `{"id":"a2","type":"Motion","time":"2026-03-01T09:00:00Z",
			"source":"e028f597-c64b-4f27-bb8d-08f5cd768e1c"}`,
			"e028f597-c64b-4f27-bb8d-08f5cd768e1c"},
		{"microphones prefix", `{"id":"a3","type":"Motion","time":"2026-03-01T09:00:00Z",
			"source":"microphones/e028f597-c64b-4f27-bb8d-08f5cd768e1c"}`,
			"e028f597-c64b-4f27-bb8d-08f5cd768e1c"},
		{"bracketed convention", `{"Id":"a4","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z",
			"Source":"Entrance Camera 01 [CAM-001]"}`, "CAM-001"},
		{"a label is not an id", `{"Id":"a5","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z",
			"Source":"Entrance Camera 01"}`, ""},
		{"a rest path is not a device", `{"Id":"a6","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z",
			"Source":"site-hq/entrance/cameras/01"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, stub := newAdapter()
			stub.bodies[eventsRoute] = json.RawMessage("[" + tc.body + "]")
			rec := poll(t, a)[0]
			if rec.SourceDeviceRef != tc.want {
				t.Errorf("source %q resolved to %q, want %q",
					tc.body, rec.SourceDeviceRef, tc.want)
			}
		})
	}
}

// A feed that is not deployed on this site is not a fault, and a feed that is
// deployed but failing must not take the whole poll down with it. Aborting on
// the first bad endpoint would turn a partial outage into a total ingest outage.
func TestFeedFailureIsIsolated(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`[{"Id":"e1","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"}]`)
	stub.status[bookmarksRoute] = 500

	var good, feedErr int
	for _, r := range poll(t, a) {
		switch r.EventType {
		case "adapter_feed_error":
			feedErr++
		case "motion":
			good++
		}
	}
	if good != 1 {
		t.Errorf("a failing feed cost %d good record(s)", 1-good)
	}
	if feedErr != 1 {
		t.Errorf("the failing feed produced %d error records, want 1", feedErr)
	}
}

// A credential failure is an operator incident with a remedy; a malformed body
// is a payload bug. They must not be reported the same way.
func TestCredentialFailureIsNamedAsSuch(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`[{"Id":"e1","Type":"Motion"}]`)
	stub.status[eventsRoute] = 401

	var found bool
	for _, r := range poll(t, a) {
		if r.EventType == "adapter_feed_error" && strings.Contains(r.MalformedReason, "401") {
			found = true
		}
	}
	if !found {
		t.Fatal("a 401 was not surfaced as a feed error naming the status")
	}
}

// Output order must be deterministic. An adapter that returns whatever order the
// socket delivered is one whose replay mode and integration mode can disagree,
// and Definition of Done item 1 depends on them not.
func TestPollOrderIsDeterministic(t *testing.T) {
	body := json.RawMessage(`[
		{"Id":"c","Type":"Motion","Timestamp":"2026-03-01T09:00:02Z"},
		{"Id":"a","Type":"Motion","Timestamp":"2026-03-01T09:00:00Z"},
		{"Id":"b","Type":"Motion","Timestamp":"2026-03-01T09:00:01Z"}
	]`)

	var first string
	for run := 0; run < 5; run++ {
		a, stub := newAdapter()
		stub.bodies[eventsRoute] = body
		var ids []string
		for _, r := range poll(t, a) {
			if r.SourceEventID != "" {
				ids = append(ids, r.SourceEventID)
			}
		}
		joined := strings.Join(ids, ",")
		if run == 0 {
			first = joined
			if joined != "a,b,c" {
				t.Fatalf("order is %v, want [a b c]", ids)
			}
			continue
		}
		if joined != first {
			t.Fatalf("run %d produced %v, run 0 produced %v", run, ids, first)
		}
	}
}

func TestReplayTransportIsDeterministic(t *testing.T) {
	scenarios := loadFixtures(t)
	var first string
	for run := 0; run < 3; run++ {
		tr := milestone.NewReplayTransport(scenarios).WithEpoch(testEpoch)
		var ids []string
		for _, r := range pollOver(t, tr) {
			ids = append(ids, r.SourceEventID+"|"+r.EventType)
		}
		if len(ids) == 0 {
			t.Fatal("replay produced no records")
		}
		joined := strings.Join(ids, ",")
		if run == 0 {
			first = joined
			continue
		}
		if joined != first {
			t.Fatalf("run %d diverged from run 0", run)
		}
	}
}

// A replay at a different wall-clock instant must produce the same relative
// timing. An absolute timestamp in a fixture is a fixture that quietly stops
// being deterministic.
func TestReplayEpochDoesNotChangeRelativeTiming(t *testing.T) {
	scenarios := loadFixtures(t)
	fiveYears := 5 * 365 * 24 * time.Hour

	early := pollOver(t, milestone.NewReplayTransport(scenarios).WithEpoch(testEpoch))
	late := pollOver(t, milestone.NewReplayTransport(scenarios).WithEpoch(testEpoch.Add(fiveYears)))

	if len(early) != len(late) {
		t.Fatalf("record counts differ: %d vs %d", len(early), len(late))
	}
	for i := range early {
		if early[i].SourceEventID != late[i].SourceEventID {
			t.Fatalf("record %d differs: %q vs %q", i, early[i].SourceEventID, late[i].SourceEventID)
		}
		if early[i].ReceivedAt != testEpoch || late[i].ReceivedAt != testEpoch {
			t.Fatalf("record %s received_at moved with the epoch", early[i].SourceEventID)
		}
	}
}

// The required behaviours must actually appear in replay output. A fixture set
// that loads cleanly but replays to nothing proves nothing.
func TestReplayProducesEveryRequiredBehaviour(t *testing.T) {
	recs := pollOver(t, milestone.NewReplayTransport(loadFixtures(t)).WithEpoch(testEpoch))

	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.EventType] = true
	}
	for _, want := range []string{
		"motion", "analytics", "alarm.created", "alarm.acknowledged", "alarm.closed",
		"device.camera_offline", "device.camera_online", "bookmark", "metadata",
		"recording.started", "recording.stopped", "adapter_unparsed_item",
	} {
		if !seen[want] {
			t.Errorf("replay produced no %q event", want)
		}
	}
}

// A duplicate delivery must arrive as two records with the same source id. The
// inbox is what turns that into one canonical event; the adapter's job is only
// to hand both over honestly rather than deciding on its own.
func TestReplayDeliversDuplicatesVerbatim(t *testing.T) {
	scenarios := loadFixtures(t)
	var dupes int
	byID := map[string]int{}
	for _, r := range pollOver(t, milestone.NewReplayTransport(scenarios).WithEpoch(testEpoch)) {
		byID[r.SourceEventID]++
		if r.SourceEventID == "dup-0001" || r.SourceEventID == "rr-0001" {
			dupes++
		}
	}
	if dupes == 0 {
		t.Fatal("no duplicate delivery appeared in replay output")
	}
	for id, n := range byID {
		if id == "dup-0001" || id == "rr-0001" || id == "rr-0002" {
			if n < 2 {
				t.Errorf("source id %s was delivered %d time(s), want the duplicate to survive", id, n)
			}
		}
	}
}

// The API surface is assumed, not verified. That has to be visible rather than
// an impression: the probe command and this test both depend on the list.
func TestMilestoneRoutesAreDeclaredUnverified(t *testing.T) {
	if len(milestone.Routes) == 0 {
		t.Fatal("no routes are declared")
	}
	unverified := milestone.UnverifiedRoutes()
	if len(unverified) == 0 {
		t.Skip("every route has been confirmed against a live server; update the package doc")
	}
	for _, r := range unverified {
		if r.Notes == "" {
			t.Errorf("%s %s is unverified but records no assumption", r.Method, r.Path)
		}
	}

	// Every route a fixture claims to come from must be a declared route,
	// otherwise a fixture could exercise a path no adapter knows about and a
	// green replay would prove nothing.
	declared := map[string]bool{}
	for _, r := range milestone.Routes {
		declared[r.Path] = true
	}
	for _, sc := range loadFixtures(t) {
		for _, ev := range sc.Events {
			if !declared[ev.Kind] {
				t.Errorf("fixture %q references route %q, which is not in Routes", sc.Name, ev.Kind)
			}
		}
	}
}
