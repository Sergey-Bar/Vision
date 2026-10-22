package fixture

// What each committed scenario is EXPECTED to raise.
//
// # Why the expectations live here and not in the fixture files
//
// A scenario's identity is its content hash, and the hash covers the whole
// file. Putting the expectation in the file would change every scenario's hash
// and the manifest with it, for a fact that is not about the scenario's
// ENVELOPES but about what the scenario is for.
//
// So the expectation is a separate table, and the table is checked against the
// corpus: a scenario with no expectation is a gap, and a scenario in the table
// that is not in the corpus is a stale entry.
//
// # Why the expectations are EXPECTATIONS and not observations
//
// A rate computed by replaying the corpus and counting what it raised would be
// a rate of one, always: everything that was raised was expected by
// definition, and everything that was not raised would be "not expected". That
// is a number that cannot fail.
//
// The expectation has to be authored from what the scenario is FOR, and the
// scenario's own description is the authority. Each entry below says what the
// scenario exists to prove, so a reader who disagrees with one can see exactly
// which one and why.
//
// # The count, and why it is a count
//
// A scenario can expect more than one incident, and "at least one" is a
// weaker claim than "exactly one". The count is what the scenario is there to
// pin down: `duplicate-delivery` exists to prove that one delivered event
// produces one canonical event and NO second side effect, and a count of one
// says that; a boolean would say nothing about the second.
//
// An expectation of zero is meaningful: it is the negative case, and the
// scenarios that expect nothing are the ones that prove the product does NOT
// raise an incident where it should not.

// ExpectedIncidents is how many incidents each committed scenario is expected
// to raise.
//
// The key is the scenario's name, and the value is the count.
//
// The expectations are the PRODUCT's statement, and they were written from the
// scenarios' own descriptions:
//
//   - the detection scenarios expect one incident, because a detection in a
//     restricted zone is what the product is for;
//   - `analytics-object-crossed` expects NONE, because the camera is
//     UNRESTRICTED and the scenario is there to prove the zone rule does not
//     fire on a line-crossing in a zone that is not restricted;
//   - `duplicate-delivery` expects one, because the event IS a detection and
//     the scenario is there to prove the SECOND delivery produces nothing;
//   - the rest expect none, because they are about the inbox, the graph, the
//     device registry and the evidence store, and none of them is a detection.
var ExpectedIncidents = map[string]int{
	// A detection in a restricted zone after hours, corroborated.
	"motion-restricted-zone": 1,
	// A typed detection with an attribute, in the restricted zone.
	"typed-person-with-bag-restricted-zone": 1,
	// The VMS raised an alarm.
	"alarm-lifecycle": 1,
	// One delivered event is one canonical event and no second side effect.
	"duplicate-delivery": 1,
	// A line-crossing on an UNRESTRICTED camera: the zone rule must not fire.
	"analytics-object-crossed": 0,
	// The device registry, the graph's clock reference and the drift subsystem.
	"camera-offline-online": 0,
	// The two evidence-fetch triggers: an operator's bookmark and a metadata
	// event.
	"bookmark-and-metadata": 0,
	// The recording state, which the evidence retention depends on.
	"recording-state": 0,
	// Envelopes that break the field contract. They are dead-lettered, and a
	// dead letter is not an incident.
	"malformed-envelopes": 0,
	// An event whose observed time precedes its arrival.
	"delayed-arrival": 0,
	// Two events delivered newest-first, so arrival order contradicts
	// observation order.
	"out-of-order-arrival": 0,
	// An event from a camera that was never registered on this platform.
	"missing-camera": 0,
	// A reconnect that re-serves the previous window.
	"reconnect-replay": 0,
}

// ExpectedFor is a scenario's expectation, and whether it has one.
//
// A scenario with no expectation is a GAP and not a zero: "the product did
// not raise an incident" and "nobody said whether it should have" are
// different facts, and only the second one is missing information.
func ExpectedFor(name string) (count int, expected bool) {
	c, ok := ExpectedIncidents[name]
	return c, ok
}
