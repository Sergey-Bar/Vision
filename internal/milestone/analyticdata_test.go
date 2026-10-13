package milestone_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/milestone"
)

// analyticEvent is the shape the vendor publishes for an analytics event,
// including the plate-bearing field that must not survive ingest.
//
// `value.data` carries the licence plate the roadmap names:
// `master-roadmap.md` M2 — *"`analyticData.object.value` carries licence-plate
// text, which is personal data — M2 targets object class and counts, and LPR is
// an M3 item routed to the compliance track first."*
const analyticEvent = `{
	"specversion": "1.0",
	"type": "ObjectDetected",
	"source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
	"time": "2023-02-28T18:10:12.4786334Z",
	"id": "7f1c2a90-1111-4222-8333-abcdefabcdef",
	"AnalyticData": {
		"Object": {
			"Type": "ObjectDetected",
			"Value": {
				"DeviceObject": {"Id": "e028f597-c64b-4f27-bb8d-08f5cd768e1c"},
				"Confidence": 0.94,
				"ClassId": "RestrictedPerson",
				"ClassName": "restricted-person",
				"Data": {"Plate": "12-345-67", "CameraSerial": "SN-99213"}
			}
		},
		"Vendor": "Axis Camera Application Platform",
		"RuleName": "Perimeter breach"
	}
}`

// TestARestrictedPersonAnalyticEventBecomesATypedEvent.
//
// The M1 incident class, deterministic rather than modelled. The vendor's own
// analytics engine asserted the class, so a rule matching
// `analytics.restricted_person` is a geometric test over a typed event — no
// VLM, no GPU, no 8-second inference, and a geometric test cannot hallucinate a
// person who is not there.
func TestARestrictedPersonAnalyticEventBecomesATypedEvent(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + analyticEvent + `],"eventCount":1}`)

	recs := poll(t, a)
	if len(recs) != 1 {
		t.Fatalf("poll returned %d records, want 1", len(recs))
	}
	if got := recs[0].EventType; got != "analytics.restricted_person" {
		t.Fatalf("the event was typed %q, want \"analytics.restricted_person\". "+
			"Without the refinement the M1 class needs a VLM to detect at all", got)
	}
	if recs[0].Malformed {
		t.Errorf("a typed analytics event was marked malformed (%s); the vocabulary "+
			"and the derivation must name the same set", recs[0].MalformedReason)
	}
}

// TestAnUnnamedAnalyticClassKeepsTheGenericAnalyticsType.
//
// The table has one entry because a second entry is a decision with a rule
// behind it. A class the taxonomy does not name keeps the generic analytics
// event it always had: the event is not lost, and nothing is guessed.
func TestAnUnnamedAnalyticClassKeepsTheGenericAnalyticsType(t *testing.T) {
	item := map[string]any{
		"Id": "7f1c2a90-1111-4222-8333-abcdefabcdef", "Type": "ObjectDetected",
		"AnalyticData": map[string]any{
			"Object": map[string]any{
				"Type":  "ObjectDetected",
				"Value": map[string]any{"ClassId": "Vehicle", "ClassName": "delivery van"},
			},
		},
	}
	if got := reflectDerive(item); got != "analytics" {
		t.Errorf("an analytic class the taxonomy does not name was typed %q, want "+
			"\"analytics\". A defaulted class is a fabricated fact wearing the "+
			"vendor's authority", got)
	}
}

// TestAnAnalyticObjectWithNoClassIsNotRefined.
//
// An analyticData object with no class is provenance about an assertion nobody
// can act on. It must not produce a typed event, and it must not lose the
// event either.
func TestAnAnalyticObjectWithNoClassIsNotRefined(t *testing.T) {
	item := map[string]any{
		"Id": "7f1c2a90-1111-4222-8333-abcdefabcdef", "Type": "ObjectDetected",
		"AnalyticData": map[string]any{
			"Object": map[string]any{"Type": "ObjectDetected", "Value": map[string]any{"Confidence": 0.5}},
		},
	}
	if got := reflectDerive(item); got != "analytics" {
		t.Errorf("an analytic object with no class was typed %q, want \"analytics\"", got)
	}
}

// TestThePlateBearingFieldsAreRedactedFromTheStoredEnvelope.
//
// The privacy bound, at the boundary that enforces it. The envelope is stored
// verbatim in source_events and is the bytes a replay re-derives from, so a
// projection that left the plate in the stored bytes would have redacted
// nothing. The redaction runs before the marshal, on the ingest path and on the
// replay path alike.
func TestThePlateBearingFieldsAreRedactedFromTheStoredEnvelope(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + analyticEvent + `],"eventCount":1}`)

	recs := poll(t, a)
	envelope := string(recs[0].Envelope)
	for _, mustNotSurvive := range []string{"12-345-67", "SN-99213", "Plate"} {
		if strings.Contains(envelope, mustNotSurvive) {
			t.Errorf("the stored envelope still carries %q. The ingest boundary is "+
				"where the LPR bound is enforced, and an envelope is stored verbatim", mustNotSurvive)
		}
	}
	if !strings.Contains(envelope, "[redacted]") {
		t.Error("the redaction left no marker. A silently shortened envelope looks " +
			"like a source that simply sent less")
	}
	// What the refinement needs survives, which is what makes the redaction safe
	// to run on the replay path.
	for _, mustSurvive := range []string{"restricted-person", "RestrictedPerson", "Axis Camera Application Platform", "Perimeter breach"} {
		if !strings.Contains(envelope, mustSurvive) {
			t.Errorf("the stored envelope no longer carries %q, so a replay can no "+
				"longer re-derive the typed event", mustSurvive)
		}
	}
}

// TestTheTypedEventReDerivesFromTheRedactedEnvelope.
//
// Invariant 2, on the new derivation. `RecordFromPayload` re-derives the record
// from the stored bytes weeks later and the consume loop calls it on every
// claim, so the typed event must come back out of the redacted envelope
// unchanged — otherwise the refinement is a property of the poll rather than of
// the event, and a claim retried after a crash types the event differently.
func TestTheTypedEventReDerivesFromTheRedactedEnvelope(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + analyticEvent + `],"eventCount":1}`)

	ingested := poll(t, a)[0]
	replayed, err := a.RecordFromPayload(ingested.Envelope)
	if err != nil {
		t.Fatalf("RecordFromPayload: %v", err)
	}
	if replayed.EventType != ingested.EventType {
		t.Errorf("the same stored bytes replayed as %q, having been ingested as %q. "+
			"A claim retried after a crash must produce the same canonical event",
			replayed.EventType, ingested.EventType)
	}
	if string(replayed.Envelope) != string(ingested.Envelope) {
		t.Error("the redaction is not idempotent: re-deriving from a redacted " +
			"envelope produced different bytes, so the replay contract is broken")
	}
}

// TestAnItemWithoutAnalyticDataIsUntouched.
//
// Analytics is per-site configuration and is frequently not enabled. The
// common case is an event with no analyticData at all, and the redaction must
// leave it byte-for-byte alone — invariant 3 is the platform degrading to
// "VMS alarms only", which is already the design.
func TestAnItemWithoutAnalyticDataIsUntouched(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventTypesRoute] = eventTypeList(site("481d4d6c-9156-4698-af7e-74b32f9e4359", "Analytics"))
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	recs := poll(t, a)
	if got := recs[0].EventType; got != "analytics" {
		t.Fatalf("a plain analytics event was typed %q, want \"analytics\"", got)
	}
	if strings.Contains(string(recs[0].Envelope), "[redacted]") {
		t.Error("an event with no analyticData was redacted anyway")
	}
}

// reflectDerive runs the type derivation over one item, through the parse path
// that ingests it, so the test asserts what a poll would produce rather than
// calling the derivation directly.
func reflectDerive(item map[string]any) string {
	rec := milestone.RawRecord{}
	_ = rec
	body, err := json.Marshal(item)
	if err != nil {
		return "unmarshallable"
	}
	a, stub := newAdapter()
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + string(body) + `],"eventCount":1}`)
	recs, err := a.Poll(context.Background(), nil)
	if err != nil || len(recs) != 1 {
		return "poll failed"
	}
	return recs[0].EventType
}
