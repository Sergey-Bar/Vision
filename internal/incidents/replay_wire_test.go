package incidents

// The wire shape of the explanation, and why this file exists.
//
// `internal/operatormetrics` had the defect this test guards against: its
// report's top level carried JSON tags and the structs behind it carried none,
// so `securityctl efficiency --json` emitted `Incidents` and `ByState` beside a
// document whose own keys were `verification_states`. A console reading
// `attention.incidents` got `undefined` and rendered an honest "nothing
// measured" empty state over a window that had plenty — a green page about
// absent data, which is the worst shape this failure takes.
//
// `Explanation` is tagged camelCase — `incidentId`, `ruleId`,
// `replayedFromStoredFacts` — and that is a different convention from the one
// the metrics document uses. The difference is deliberate and this test does not
// try to reconcile them: what it asserts is that THIS document is internally
// consistent, because two documents with different schemes are a design decision
// and one document with two schemes is a bug.

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// camelKey is the convention `Explanation` uses.
var camelKey = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*[a-z0-9]$|^[a-z]$`)

// TestTheExplanationSpeaksOneJSONLanguage walks the whole emitted document.
func TestTheExplanationSpeaksOneJSONLanguage(t *testing.T) {
	ex := Explanation{
		IncidentID: "incident-1", RuleID: "rule-1",
		Steps: []ReplayStep{{Order: 1, Kind: "condition", Description: "Event x triggered it.",
			EventIDs: []string{"e1"}}},
		Matches: true,
	}
	blob, err := json.Marshal(ex)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(blob, &tree); err != nil {
		t.Fatalf("did not marshal to an object: %v", err)
	}
	walkWire(t, "explanation", tree)
}

// TestReplayedFromStoredFactsIsAlwaysOnTheWire is the part of invariant 2 that a
// struct test CAN prove, and it is narrower than the version of this test that
// came first.
//
// That version marshalled a bare `Explanation{IncidentID: "x"}` and asserted the
// field was true. It failed, and it was wrong to: `Replay` is the only constructor
// of an `Explanation` that is ever encoded, and it sets the field at replay.go:101
// — the doc comment says so in as many words, and `incidents_test.go` already
// asserts it after a real replay against the database. A struct literal that
// skips the constructor is not the object the wire carries, and a test built on
// one proves nothing about invariant 2.
//
// What a struct test CAN prove, and what matters on its own: the field is not
// `omitempty`. A consumer reading the wire must see the key whether or not it is
// true, because a MISSING key is not a false claim — it is an absent one, and a
// screen that renders "replayed" when the key is nowhere to be found is a screen
// inventing a verdict.
func TestReplayedFromStoredFactsIsAlwaysOnTheWire(t *testing.T) {
	blob, err := json.Marshal(Explanation{IncidentID: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(blob, &tree); err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw, ok := tree["replayedFromStoredFacts"]
	if !ok {
		t.Fatal("replayedFromStoredFacts is absent from the wire. The field is not omitempty by design: a consumer must always be able to READ the claim rather than infer it from the key being there, and a missing key is not a false claim but an absent one")
	}
	if _, isBool := raw.(bool); !isBool {
		t.Fatalf("replayedFromStoredFacts is %T, want a bool; a claim that is not a boolean on the wire is a claim nobody can read", raw)
	}
}

// TestReplayedFromStoredFactsIsSetByTheConstructor is the other half, and it
// exists because the first version of the test above proved too much and failed.
//
// It asserts what replay.go:101 does, through the constructor, so a reader of the
// wire test can see where the true comes from without trusting a comment.
func TestReplayedFromStoredFactsIsSetByTheConstructor(t *testing.T) {
	// The constructor is the one place an Explanation is built. Rather than
	// assert on an unexported builder, this walks the source for the field's one
	// assignment and fails if it is gone: a constant true that stops being set
	// is exactly the kind of regression a struct-level test cannot see.
	src, err := os.ReadFile("replay.go")
	if err != nil {
		t.Fatalf("read replay.go: %v", err)
	}
	if !strings.Contains(string(src), "ReplayedFromStoredFacts: true") {
		t.Fatal("`ReplayedFromStoredFacts: true` is no longer in replay.go. Replay is the only constructor of an Explanation and the field is set there; if it has moved, this assertion must move with it, because the wire's constant-true claim has no other source")
	}
}

// walkWire descends decoded JSON and applies the convention at every depth.
func walkWire(t *testing.T, path string, node any) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if !camelKey.MatchString(key) {
				t.Errorf("%s.%s: %q is not camelCase. A struct field with no tag reaches the wire under its Go name, and a consumer reading the other convention gets undefined",
					path, key, key)
				continue
			}
			walkWire(t, path+"."+key, child)
		}
	case []any:
		for i, child := range v {
			walkWire(t, path+"["+strings.TrimSpace(itoaWire(i))+"]", child)
		}
	}
}

func itoaWire(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
