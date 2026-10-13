package config

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
)

// TestAnUnmappedAbsencePairIsRefusedByName is the plan's requirement and the whole
// reason the map exists. The failure it prevents is specific: an absence falling
// back to the detection's own event type arrives at the Smart Client looking like a
// crossing, an Alarm Definition on "vehicle crossing the line" fires on it, and an
// operator is paged about a car that may not exist because the module could not
// reach its own model.
func TestAnUnmappedAbsencePairIsRefusedByName(t *testing.T) {
	m := complete(t)
	m["SP_POC_EVENT_TYPE_GUIDS"] = "*@not_asked=11111111-1111-4111-8111-111111111111"
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.EventTypeGUIDFor("car", aistate.Degraded)
	if err == nil {
		t.Fatal("an unmapped (class, absence state) pair resolved to something")
	}
	for _, want := range []string{"(car, degraded)", "SP_POC_EVENT_TYPE_GUIDS", "*@degraded=<guid>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// And it says what the operator should do about it, in the house style: what
	// breaks and what to set.
	if !strings.Contains(err.Error(), "would make an absence arrive as a crossing alarm") {
		t.Fatalf("the refusal does not name the consequence: %v", err)
	}
}

// TestAnAnsweredDetectionStillGoesToTheDetectionType: the map must not change the
// existing behaviour for a run where the model answered. That is the one case where
// there is nothing new to push.
func TestAnAnsweredDetectionStillGoesToTheDetectionType(t *testing.T) {
	m := complete(t)
	m["SP_POC_EVENT_TYPE_GUIDS"] = "*@not_asked=11111111-1111-4111-8111-111111111111"
	cfg, err := Load(lookupFrom(m))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{aistate.Asked, aistate.NoObjects} {
		got, gerr := cfg.EventTypeGUIDFor("car", state)
		if gerr != nil {
			t.Fatalf("(%s) is an answered state and must not need the blindness map: %v", state, gerr)
		}
		if got != m["SP_POC_EVENT_TYPE_GUID"] {
			t.Fatalf("(%s) resolved to %q, want the ordinary detection type %q", state, got, m["SP_POC_EVENT_TYPE_GUID"])
		}
	}
}

// TestNoObjectsIsRefusedItsOwnEventType is the anti-alarm-fatigue control. Three
// types, not four: `no_objects` is a negative ANSWER, not a blindness, and a type
// that fires whenever a camera sees nothing is the fatigue this product exists to
// remove.
func TestNoObjectsIsRefusedItsOwnEventType(t *testing.T) {
	_, err := ParseEventTypeMap("car@no_objects=11111111-1111-4111-8111-111111111111")
	if err == nil {
		t.Fatal("no_objects was accepted as an event-type state")
	}
	for _, want := range []string{"no_objects", "negative ANSWER", "alarm fatigue"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// It lists the three that DO qualify, from the same sentence the plan uses, so a
	// reader who has the refusal does not have to go looking for the reasoning. The
	// loop is per-state rather than one substring because the list's order is a
	// presentation choice and a test must not break when it changes.
	for _, state := range BlindnessStates {
		if !strings.Contains(err.Error(), state) {
			t.Fatalf("the refusal does not list %q among the states that qualify: %v", state, err)
		}
	}
}

// TestTheExactKeyWinsOverTheWildcard: a per-class entry is the exception, so it must
// not be shadowed by the catch-all. The other way round — the wildcard quietly
// overriding a deliberate per-class mapping — would send one site's blindness alarm
// to another's event type.
func TestTheExactKeyWinsOverTheWildcard(t *testing.T) {
	m, err := ParseEventTypeMap("*@not_asked=aaaa;car@not_asked=bbbb")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.Lookup("car", aistate.NotAsked); !ok || got != "bbbb" {
		t.Fatalf("Lookup(car, not_asked) = %q, %t; want the exact entry bbbb", got, ok)
	}
	if got, ok := m.Lookup("truck", aistate.NotAsked); !ok || got != "aaaa" {
		t.Fatalf("Lookup(truck, not_asked) = %q, %t; want the wildcard aaaa", got, ok)
	}
	if _, ok := m.Lookup("car", aistate.CannotTell); ok {
		t.Fatal("a pair with no entry resolved anyway")
	}
}

// TestTheKeyIsCaseAndSpaceInsensitive: `CAR@NOT_ASKED` and `car@not_asked` name the
// same thing, and a map where one of them loses silently is a map that works on one
// site and not another.
//
// The upper case is only applied to the STATE. The state's own spelling is fixed by
// the wire — `not_asked`, not `notasked` — and a parser that "helpfully" stripped the
// underscore would accept a spelling no other tool in the module accepts, so
// `NotAsked` is refused here exactly as `nonsense` is.
func TestTheKeyIsCaseAndSpaceInsensitive(t *testing.T) {
	m, err := ParseEventTypeMap("  CAR @ NOT_ASKED  =  abcd  ;  *@DEGRADED=efgh ")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Lookup("car", aistate.NotAsked); got != "abcd" {
		t.Fatalf("the padded, upper-cased entry did not resolve: %q", got)
	}
	if got, _ := m.Lookup("car", aistate.Degraded); got != "efgh" {
		t.Fatalf("the upper-cased state did not resolve: %q", got)
	}
	if _, err := ParseEventTypeMap("car@NotAsked=abcd"); err == nil {
		t.Fatal("the Go constant's spelling was accepted as a state; only the wire spelling is")
	}
}

// TestTwoGuidsForOnePairIsRefusedByName: last-write-wins would make the GUID that
// gets pushed depend on parse order, and an operator reading the variable would see
// one value and get the other.
func TestTwoGuidsForOnePairIsRefusedByName(t *testing.T) {
	_, err := ParseEventTypeMap("car@not_asked=aaaa;car@not_asked=bbbb")
	if err == nil {
		t.Fatal("a duplicate pair was accepted")
	}
	for _, want := range []string{"configured twice", "aaaa", "bbbb", "map iteration order"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// The wildcard bucket is a separate bucket, so `*@not_asked` and `car@not_asked`
	// together are NOT a duplicate. They are the exact-key-wins-wildcard case.
	if _, err := ParseEventTypeMap("*@not_asked=aaaa;car@not_asked=bbbb"); err != nil {
		t.Fatalf("a wildcard and an exact entry for the same state were called a duplicate: %v", err)
	}
}

// TestAMalformedEntryIsRefusedNamingTheEntry: a refusal that says only
// "SP_POC_EVENT_TYPE_GUIDS is malformed" sends an operator to re-read the whole
// line. One that quotes the offending token is a one-glance fix.
func TestAMalformedEntryIsRefusedNamingTheEntry(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"car-not_asked=aaaa", "@"},
		{"car@not_asked", "<class>@<absence-state>=<guid>"},
		{"car@not_asked=", "no guid"},
		{"@not_asked=aaaa", "names no class"},
		{"car@nonsense@not_asked=aaaa", "@"},
	} {
		_, err := ParseEventTypeMap(tc.raw)
		if err == nil {
			t.Fatalf("%q was accepted", tc.raw)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: the refusal does not say %q: %v", tc.raw, tc.want, err)
		}
		if !strings.Contains(err.Error(), "car") && strings.Contains(tc.raw, "car") {
			t.Fatalf("%q: the refusal does not quote the offending entry: %v", tc.raw, err)
		}
	}
}

// TestAnEmptyMapIsUsableAndSaysWhatIsMissing: an account without the rights to
// register Analytics Event Types is a real world, and the refusal has to describe
// the limitation rather than pointing at a variable that was never set.
func TestAnEmptyMapIsUsableAndSaysWhatIsMissing(t *testing.T) {
	cfg, err := Load(lookupFrom(complete(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EventTypeGUIDs.Empty() {
		t.Fatal("a world with no SP_POC_EVENT_TYPE_GUIDS produced a non-empty map")
	}
	_, err = cfg.EventTypeGUIDFor("car", aistate.NotAsked)
	if err == nil {
		t.Fatal("an empty map resolved an absence state")
	}
	for _, want := range []string{"admin rights", "unsearchable", "SP_POC_EVENT_TYPE_GUIDS is empty"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestABadEntryIsRefusedAtLoadNotAtTheFirstFrame: a run that starts and then pushes
// nothing produces zero alarms and no error, and "why did nothing arrive" is a much
// longer investigation than a startup refusal.
func TestABadEntryIsRefusedAtLoadNotAtTheFirstFrame(t *testing.T) {
	m := complete(t)
	m["SP_POC_EVENT_TYPE_GUIDS"] = "car@no_objects=aaaa"
	_, err := Load(lookupFrom(m))
	if err == nil {
		t.Fatal("a bad map was accepted at startup")
	}
	if !strings.Contains(err.Error(), "SP_POC_EVENT_TYPE_GUIDS") {
		t.Fatalf("the refusal does not name the variable: %v", err)
	}
}

// TestTheMapIsPrintedInAStableOrder: a list of configured pairs that reorders itself
// between runs is a list an operator cannot read a diff of.
func TestTheMapIsPrintedInAStableOrder(t *testing.T) {
	m, err := ParseEventTypeMap("truck@not_asked=d;car@degraded=b;*@cannot_tell=a;car@not_asked=c")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(m.Pairs(), " ")
	want := "*@cannot_tell=a car@degraded=b car@not_asked=c truck@not_asked=d"
	if got != want {
		t.Fatalf("Pairs() = %q, want %q", got, want)
	}
}

// TestAMissingEventTypeGUIDIsRefusedForAnAnsweredDetection: the ordinary
// SP_POC_EVENT_TYPE_GUID is still required, and this names the pair rather than
// silently producing an event with no type.
//
// The Config is built by hand rather than through Load, because Load already refuses
// an unset SP_POC_EVENT_TYPE_GUID at startup — which is the correct behaviour and
// makes the guard unreachable that way. The guard is for the other caller of the
// resolver: code that assembles a Config itself.
func TestAMissingEventTypeGUIDIsRefusedForAnAnsweredDetection(t *testing.T) {
	_, err := (Config{}).EventTypeGUIDFor("car", aistate.Asked)
	if err == nil || !strings.Contains(err.Error(), "SP_POC_EVENT_TYPE_GUID") {
		t.Fatalf("err = %v, want a refusal naming SP_POC_EVENT_TYPE_GUID", err)
	}
}
