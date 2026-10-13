package milestone_test

// The `type`-is-a-GUID unblock.
//
// Milestone returns an event's `type` as a GUID
// (`events-rest-v1.reference.md:289-297`, `:380-388`) and the platform's
// vocabulary is names. These tests are the ones that make the difference
// observable, and each is written so that removing the change fails it.
//
// What is *not* here is an alias table. Deciding that a vendor's own name means
// a canonical type is a published-vocabulary decision that `AGENTS.md` reserves
// to a human, and it is not writable until a probe has shown a real site's real
// names. What lands is the missing half: the site's answer reaches the record,
// the envelope and the dead-letter reason.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/milestone"
)

const eventTypesRoute = "/api/rest/v1/eventTypes"

// eventTypeList is one site's answer, written the way the vendor's own prose
// implies it: an id and a name, with the generator fields EVENTS:42 names
// (`generatorType`, `generatorSubtype`) alongside.
//
// **The shape is an assumption.** EVENTS:40 and :42 name the resource and never
// show a body (route-audit.md §2.8), so this is a stand-in for the probe, and
// the probe's `sample` field is what settles it.
func eventTypeList(entries ...string) json.RawMessage {
	return json.RawMessage(`{"array":[` + strings.Join(entries, ",") + `]}`)
}

// site answers with a type list for one GUID. Two sites that call the same thing
// the same way share a name; two sites whose configurations were built
// separately do not, which is the whole point.
func site(guid, name string) string {
	return `{"id":"` + guid + `","name":"` + name + `","generatorType":"MIPDevice"}`
}

// Two sites, two GUIDs, one concept.
//
// This is the red-check that catches the trap in the plan: the GUIDs are
// **per-site configuration**, not global constants. `481d4d6c-…` is an instance
// in one customer's XProtect, and the next site has a different one for the same
// conceptual type. A compiled-in `guid -> name` table works at the design
// partner and dead-letters every event at customer two — invisible until there
// is a customer two, and unreachable by any test that only uses the design
// partner's bytes.
//
// So each site below is given its own GUID *in its own event body*, which is what
// a real second site sends. Resolve from a package-level map instead of from the
// site, and the second adapter stops resolving.
func TestTwoSitesWithDifferentGUIDsResolveToTheirOwnNames(t *testing.T) {
	const (
		guidA = "481d4d6c-9156-4698-af7e-74b32f9e4359"
		guidB = "9c1b7f30-2a44-4f6d-9f01-77c1b2aa55e1"
	)

	readAt := func(guid, name string) milestone.RawRecord {
		t.Helper()
		a, stub := newAdapter()
		stub.bodies[eventTypesRoute] = eventTypeList(site(guid, name))
		stub.bodies[eventsRoute] = json.RawMessage(
			`{"array":[` + documentedEventOfType(guid) + `],"eventCount":1}`)
		recs := poll(t, a)
		if len(recs) != 1 {
			t.Fatalf("got %d records from a one-item page: %+v", len(recs), recs)
		}
		return recs[0]
	}

	partner := readAt(guidA, "Motion Started")
	customerTwo := readAt(guidB, "Motion Started")

	for _, c := range []struct {
		who  string
		guid string
		rec  milestone.RawRecord
	}{
		{"the design partner", guidA, partner},
		{"customer two", guidB, customerTwo},
	} {
		if c.rec.SourceTypeName != "Motion Started" {
			t.Errorf("%s's own type name did not resolve: SourceTypeName is %q. The GUIDs are "+
				"per-site configuration, so anything keyed on the other site's GUID fails here "+
				"and nowhere else until there is a second customer",
				c.who, c.rec.SourceTypeName)
		}
		if !strings.Contains(c.rec.MalformedReason, c.guid) {
			t.Errorf("%s's dead letter does not quote the GUID that site sent (%s): %q",
				c.who, c.guid, c.rec.MalformedReason)
		}
		// The vendor's own name is in the sentence the operator reads, which is
		// the whole of what the alias table decision needs as input.
		if !strings.Contains(c.rec.MalformedReason, "Motion Started") {
			t.Errorf("%s's dead letter does not name what the site calls the type: %q",
				c.who, c.rec.MalformedReason)
		}
	}
}

// documentedEventOfType is the vendor's own sample event
// (`events-rest-v1.reference.md:380-388`) with `type` set to this site's GUID.
//
// The sample is used verbatim apart from that one field, because the point of
// every test in this file is the documented shape and nothing else.
func documentedEventOfType(guid string) string {
	return strings.Replace(documentedEvent,
		"481d4d6c-9156-4698-af7e-74b32f9e4359", guid, 1)
}

// A site whose names are spellings the normalizer already knows starts working
// on its own, with no table added here.
//
// This is the whole of what `typeInput` buys: the existing normalizer is offered
// the site's own name instead of a GUID. Nothing in `normalizeEventType` or
// `KnownMilestoneEventTypes` changed, and this test is what would fail if someone
// later made the change by adding a mapping instead.
func TestASiteWhoseNamesMatchTheVocabularyNeedsNoTable(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventTypesRoute] = eventTypeList(
		site("481d4d6c-9156-4698-af7e-74b32f9e4359", "Analytics"))
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	rec := poll(t, a)[0]
	if rec.EventType != "analytics" {
		t.Errorf("event type is %q, want \"analytics\": this site's own name normalises and "+
			"the event should need no dead letter", rec.EventType)
	}
	if rec.Malformed {
		t.Errorf("the event was dead-lettered as %q", rec.MalformedReason)
	}
}

// The red-check from the plan: when the vocabulary cannot be read, the dead
// letter says so, and it does not invent a name to fill the gap.
//
// An unresolved type is a state, not a default (invariant 3: a missing
// observation is never a negative one). A resolver that answered "Motion" on a
// miss would produce confident, wrong, vendor-attributed dead letters — the worst
// failure mode this repository has, because it looks like a working feed.
func TestAFeedThatCannotReadTheVocabularySaysWhyAndGuessesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		// because is the half of the cause that is actionable: what an operator
		// has to go and look at.
		because string
	}{
		{"not deployed here", 404, "does not deploy"},
		{"refused", 401, "answered 401"},
		{"failing", 500, "answered 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, stub := newAdapter()
			stub.status[eventTypesRoute] = tc.status
			stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

			rec := poll(t, a)[0]
			if !rec.Malformed {
				t.Fatal("an event with no resolvable type was accepted")
			}
			if rec.SourceTypeName != "" {
				t.Errorf("a type name was invented: %q", rec.SourceTypeName)
			}
			if !strings.Contains(rec.MalformedReason, "481d4d6c-9156-4698-af7e-74b32f9e4359") {
				t.Errorf("the reason lost the GUID: %q", rec.MalformedReason)
			}
			// "Unresolved" with no cause is not actionable. The cause is the half
			// of this that matters, and it is the only new thing in the sentence.
			if !strings.Contains(rec.MalformedReason, "no name for it could be resolved") {
				t.Errorf("the reason does not say the name was unresolved: %q", rec.MalformedReason)
			}
			if !strings.Contains(rec.MalformedReason, tc.because) {
				t.Errorf("the reason does not name the cause (%q): %q", tc.because, rec.MalformedReason)
			}
		})
	}
}

// A body this adapter cannot read is a body it must say so about, rather than
// one that yields an empty vocabulary and a dead letter blaming the GUID.
//
// The empty answer is the dangerous one: `/eventTypes` returning `[]` on a site
// that *does* have event types is indistinguishable from a site with none, and
// the second reading is a guess.
func TestAVocabularyTheAdapterCannotReadIsNotAnEmptyVocabulary(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventTypesRoute] = json.RawMessage(`{"unexpected":true}`)
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	rec := poll(t, a)[0]
	if rec.SourceTypeName != "" {
		t.Errorf("a name was resolved out of a body this adapter cannot read: %q", rec.SourceTypeName)
	}
	if !strings.Contains(rec.MalformedReason, "eventTypes") {
		t.Errorf("an unreadable vocabulary produced the plain unrecognised-type reason: %q",
			rec.MalformedReason)
	}
}

// An entry with a GUID and no name, or a name and no GUID, is skipped rather
// than half-recorded.
//
// A map entry keyed on the GUID with an empty value resolves to "" while looking
// resolved, and the code that reads it would have to know which empty string
// means "the site said nothing" and which means "the site said nothing useful".
func TestAHalfReadableVocabularyEntryIsNotAResolution(t *testing.T) {
	a, stub := newAdapter()
	stub.bodies[eventTypesRoute] = eventTypeList(
		`{"id":"481d4d6c-9156-4698-af7e-74b32f9e4359"}`,
		`{"name":"Motion Started"}`,
		`{"id":"481d4d6c-9156-4698-af7e-74b32f9e4359","name":"Motion Started"}`,
	)
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	rec := poll(t, a)[0]
	// The complete entry is what makes this resolve; the two half entries must
	// not have introduced a second, conflicting answer.
	if rec.SourceTypeName != "Motion Started" {
		t.Errorf("SourceTypeName is %q, want \"Motion Started\"", rec.SourceTypeName)
	}
}

// The vocabulary is a lookup, not a feed.
//
// `Adapter.Poll` reads every route marked `Paginated`, and an event-type list
// polled as a source feed arrives with no `Type` field, dead-letters as an
// unparsed item and is stored as though a customer's cameras had produced it.
// This is the gate for that, and it is red-checked by setting `Paginated` on the
// route.
func TestTheEventTypeVocabularyIsNeverPolledAsAFeed(t *testing.T) {
	for _, r := range milestone.Routes {
		if r.Path == eventTypesRoute {
			if r.Paginated {
				t.Error("/eventTypes is marked Paginated, so Adapter.Poll reads it as a source " +
					"feed and stores an event-type list as if it were evidence from a camera")
			}
			if r.WriteBack {
				t.Error("/eventTypes is marked WriteBack; it is a read")
			}
			if r.Task != "" {
				t.Errorf("/eventTypes carries task=%q; the documents name it a GET", r.Task)
			}
			return
		}
	}
	t.Fatal("no route is declared at /eventTypes, so nothing resolves a GUID and every event " +
		"from a real server dead-letters as unknown.<guid>")
}

// Invariant 2: a decision replays from stored facts.
//
// `RecordFromPayload` re-derives the record from the stored envelope weeks later,
// and the consume loop calls it on every claim — including a claim retried after
// a crash. If the site-resolved name lived only in memory, the same event would
// be typed `motion` on the poll that read it and `unknown.<guid>` on the claim
// that consumed it, and no replay would notice because both answers are
// internally consistent.
//
// So the name is stored with the envelope. This test changes the site's answer
// after the event was read, because that is the case that catches it: the stored
// bytes win over what the vendor says today.
func TestTheResolvedNameIsStoredSoTheTypeReplays(t *testing.T) {
	const guid = "481d4d6c-9156-4698-af7e-74b32f9e4359"
	a, stub := newAdapter()
	stub.bodies[eventTypesRoute] = eventTypeList(site(guid, "Analytics"))
	stub.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `],"eventCount":1}`)

	ingested := poll(t, a)[0]
	if ingested.EventType != "analytics" {
		t.Fatalf("the event was ingested as %q, want \"analytics\"", ingested.EventType)
	}
	if !strings.Contains(string(ingested.Envelope), resolvedNameKey) {
		t.Fatalf("the stored envelope does not carry the resolved name (%q); a replay of "+
			"these bytes would re-derive the type from nothing", string(ingested.Envelope))
	}

	// The operator renames the event type in the VMS this afternoon.
	stub.bodies[eventTypesRoute] = eventTypeList(site(guid, "Object Detected"))

	replayed, err := a.RecordFromPayload(ingested.Envelope)
	if err != nil {
		t.Fatalf("RecordFromPayload: %v", err)
	}
	if replayed.EventType != ingested.EventType {
		t.Errorf("the same stored bytes replayed as %q, having been ingested as %q. A claim "+
			"retried after a crash must produce the same canonical event",
			replayed.EventType, ingested.EventType)
	}
}

// resolvedNameKey is the envelope field the site-resolved type name is stored
// under. Spelled out here rather than read from the package because it is a
// property of the *stored bytes*, and a test that imported the constant would
// pass if the constant changed.
const resolvedNameKey = "_resolvedEventTypeName"

// The vocabulary is read once per poll, not once per event.
//
// Twenty events a second is the designed load (`internal/dbx/dbx.go`
// `DefaultConfig`). Asking the site for its type list per event would be twenty
// extra requests a second against a customer's VMS to answer a question whose
// answer did not change between two of them.
func TestTheVocabularyIsReadOncePerPollNotOncePerEvent(t *testing.T) {
	reads := &countingTransport{bodies: map[string]json.RawMessage{}}
	a := milestone.New(reads, "site-1", "int-1").
		WithClock(func() time.Time { return testEpoch })
	reads.bodies[eventTypesRoute] = eventTypeList(site("481d4d6c-9156-4698-af7e-74b32f9e4359", "Analytics"))
	reads.bodies[eventsRoute] = json.RawMessage(`{"array":[` + documentedEvent + `,` + documentedEvent + `],"eventCount":2}`)

	if _, err := a.Poll(context.Background(), nil); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got := reads.count(eventTypesRoute); got != 1 {
		t.Errorf("/eventTypes was read %d times in one poll; it is a lookup, not a per-event call", got)
	}
}

type countingTransport struct {
	mu     sync.Mutex
	bodies map[string]json.RawMessage
	counts map[string]int
}

func (c *countingTransport) Get(_ context.Context, route milestone.Route, _ milestone.Query) (*milestone.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[route.Path]++
	body, ok := c.bodies[route.Path]
	if !ok {
		return &milestone.Response{Status: 404, Route: route}, nil
	}
	return &milestone.Response{Status: 200, Body: body, Route: route}, nil
}

func (c *countingTransport) Close() error { return nil }

func (c *countingTransport) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[path]
}
