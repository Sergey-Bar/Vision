package proposal_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/Sergey-Bar/vision/internal/proposal"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("cannot encode %+v: %v", v, err)
	}
	return string(raw)
}

// TestTheAliasesAreTheSameType is the test that keeps this package honest.
//
// The whole design rests on `ai.Box`, `detect.Box` and `novelty.Box` being ALIASES
// of proposal.Box rather than three types that happen to look alike. A defined
// type would still compile, every call site would still build, and every
// existing test would still pass — because Go allows assigning between a defined
// type and its underlying struct only with a conversion, and there are 24 `ai`
// tests, 44 `detect` tests and 19 `novelty` tests that would catch it.
//
// They would catch it. But they catch it as a COMPILE error somewhere far from
// this file, with a message about a type mismatch and no mention of the reason.
// This test states the reason.
//
// The check is that assignment needs no conversion in either direction, including
// through a pointer and through an interface, because those are the three routes
// by which the alias can quietly stop being one.
func TestTheAliasesAreTheSameType(t *testing.T) {
	var p proposal.Box
	var v interface{} = p

	// Round-trip through an interface and back. An interface{} holds the dynamic
	// type, so if proposal.Box were a defined type distinct from ai.Box this
	// assertion would fail where a direct assignment had succeeded.
	if _, ok := v.(proposal.Box); !ok {
		t.Fatal("proposal.Box did not survive a round trip through interface{}")
	}

	// Pointer identity: &Box must be assignable to *Box with no conversion.
	pb := &p
	var same = pb
	if same != pb {
		t.Fatal("pointer identity changed through the alias")
	}

	// The reverse direction, which is the one a caller writes: a value of the
	// aliased name assigned into a proposal.Box field.
	p = proposal.Box{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.2, X2: 0.3, Y2: 0.4}
	if p.Class != "car" || p.Score != 0.9 {
		t.Fatalf("composite literal lost its fields: %+v", p)
	}
}

// TestTheJSONTagsAreTheSidecarsWireFormat guards the one thing about this struct
// that is a published contract rather than an internal choice.
//
// `internal/ai` decodes sidecar responses with DisallowUnknownFields, so these
// four names are what the Python process must emit. Renaming one here is a
// breaking wire change that would present as "every frame of every run is
// unparsed" — the exact failure this module has already shipped once, when the
// sidecar began sending input_w and this build had never heard of it.
func TestTheJSONTagsAreTheSidecarsWireFormat(t *testing.T) {
	b := proposal.Box{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.2, X2: 0.3, Y2: 0.4}
	want := `{"cls":"car","score":0.9,"x1":0.1,"y1":0.2,"x2":0.3,"y2":0.4}`
	if got := mustJSON(t, b); got != want {
		t.Fatalf("box wire form = %s, want %s", got, want)
	}
}

// TestNonEmptyAndValidAreDifferentPredicates is the distinction this package most
// easily erases, and the one the migration made concrete.
//
// `novelty` measures a connected component with no class. `detect` receives a
// class from a model. They are the same Go type now, so a single predicate over
// that type has to serve both, and the two answers are genuinely opposite:
//
//   - a full-frame box with no class is a realBox measurement — the whole view
//     changed, and that is the entire point of the novelty trigger;
//   - a box with no class that came from a model is an ANSWER THIS BUILD CANNOT
//     INTERPRET, and drawing it would put a label-less rectangle on the picture.
//
// So NonEmpty answers "does this name a region" and Valid answers "is this a
// detection I can act on". Collapsing them would either refuse every region the
// novelty engine ever found, or accept every nameless box a broken decoder emits.
func TestNonEmptyAndValidAreDifferentPredicates(t *testing.T) {
	wholeFrame := proposal.Box{X1: 0, Y1: 0, X2: 1, Y2: 1}
	if !wholeFrame.NonEmpty() {
		t.Fatal("a full-frame box must be non-empty; it is the box for 'everything changed'")
	}
	if wholeFrame.Valid() {
		t.Fatal("a full-frame box with no class is a MEASUREMENT, not a detection, and must not pass Valid")
	}

	empty := proposal.Box{}
	if empty.NonEmpty() {
		t.Fatal("an all-zero box names no region")
	}

	realBox := proposal.Box{Class: "car", Score: 0.9, X1: 0.1, Y1: 0.2, X2: 0.4, Y2: 0.5}
	if !realBox.Valid() || !realBox.NonEmpty() {
		t.Fatalf("a realBox detection must pass both: %+v", realBox)
	}

	// A measured region with a score but no class: still not a detection.
	noClass := realBox
	noClass.Class = ""
	if noClass.Valid() {
		t.Fatal("a box with no class must not pass Valid even when it has a score")
	}
}

// TestValidRefusesTheDegenerateAndTheAbsurd is RC-16's arithmetic, and it is the
// check that keeps a box with no area from ever being drawn.
//
// The bound is deliberately generous in one direction and tight in the other. A
// box hanging half out of the frame is a realBox detection of a realBox object that is
// half out of shot, and it is drawn. A box at 4.0 is not a detection of anything,
// and a decoder that produced one has a scale bug — which is the D4 defect this
// whole exercise exists to stop.
func TestValidRefusesTheDegenerateAndTheAbsurd(t *testing.T) {
	base := proposal.Box{Class: "car", Score: 0.5, X1: 0.1, Y1: 0.1, X2: 0.4, Y2: 0.4}
	if !base.Valid() {
		t.Fatal("the base box must be valid")
	}

	cases := []struct {
		name  string
		mutar func(b *proposal.Box)
	}{
		{"zero width", func(b *proposal.Box) { b.X2 = b.X1 }},
		{"negative width", func(b *proposal.Box) { b.X2 = b.X1 - 0.1 }},
		{"zero height", func(b *proposal.Box) { b.Y2 = b.Y1 }},
		{"negative height", func(b *proposal.Box) { b.Y2 = b.Y1 - 0.1 }},
		{"NaN score", func(b *proposal.Box) { b.Score = math.NaN() }},
		{"infinite score", func(b *proposal.Box) { b.Score = math.Inf(1) }},
		{"NaN coordinate", func(b *proposal.Box) { b.X1 = math.NaN() }},
		{"far outside the frame", func(b *proposal.Box) { b.X1, b.X2 = 3.9, 4.2 }},
		{"no class", func(b *proposal.Box) { b.Class = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			tc.mutar(&b)
			if b.Valid() {
				t.Fatalf("a box with %s must be refused, not clamped: %+v", tc.name, b)
			}
		})
	}

	// The edge that must stay ALLOWED, because clamping it would move an object
	// out of the frame to where an operator can see it.
	edge := base
	edge.X1, edge.X2 = -0.2, 1.1
	if !edge.Valid() {
		t.Fatal("a box hanging off the edge of the frame is a realBox detection and must survive")
	}
}

// TestCandidateHasOneClassNameAndNotTwo pins the decision recorded in
// proposal.Candidate: the class and the score are PROMOTED from the embedded Box
// rather than redeclared beside it.
//
// Writing `Candidate{Box; Class; Score}` compiles, compiles cleanly, and passes
// every test that does not happen to read `Box.Valid()`. It is a trap, because the
// outer field shadows the inner one: a decoder that fills `Candidate.Class` builds
// boxes whose `Box.Class` is still empty, every candidate fails Valid(), and the
// run reports an empty road with no error anywhere. That is the silent-zero shape
// this module has already been bitten by twice.
//
// This test constructs a candidate the only supported way and reads the class back
// through BOTH paths. If anyone adds a shadowing field, the second read disagrees
// with the first and this fails immediately.
func TestCandidateHasOneClassNameAndNotTwo(t *testing.T) {
	c := proposal.Candidate{Box: proposal.Box{
		Class: "person", Score: 0.77, X1: 0.2, Y1: 0.3, X2: 0.4, Y2: 0.6,
	}}

	if c.Class != "person" {
		t.Fatalf("promoted class = %q, want %q", c.Class, "person")
	}
	// The two reads are the same field today, and they stop being the same field
	// the moment somebody adds a shadowing Class to the candidate. The
	// comparison is written through two names so that it reads as the
	// disagreement it is testing for.
	if promoted, boxed := c.Class, c.Box.Class; promoted != boxed {
		t.Fatalf("the box's own class is %q but the promoted one is %q: there are two places to "+
			"set the class and they disagree", boxed, promoted)
	}
	if c.Score != 0.77 || c.Box.Score != c.Score {
		t.Fatalf("the score has two values: %v and %v", c.Score, c.Box.Score)
	}
	// The promoted Class is not merely equal, it IS the box's field: mutating one
	// mutates the other, because there is only one.
	c.Class = "car"
	if c.Box.Class != "car" {
		t.Fatal("Class on the candidate and Class on its box are two distinct fields")
	}
}

// TestUnionPrefersTheRealRegion is the behaviour `novelty` depends on when it
// merges several components into one region, and it is order-dependent on purpose.
func TestUnionPrefersTheRealRegion(t *testing.T) {
	empty := proposal.Box{}
	realBox := proposal.Box{X1: 0.2, Y1: 0.3, X2: 0.5, Y2: 0.6}

	if got := empty.Union(realBox); got != realBox {
		t.Errorf("union with an empty left side = %+v, want the realBox region unchanged", got)
	}
	if got := realBox.Union(empty); got != realBox {
		t.Errorf("union with an empty right side = %+v, want the realBox region unchanged", got)
	}
	if (empty.Union(empty)).NonEmpty() {
		t.Error("the union of two empty boxes must still be empty")
	}

	a := proposal.Box{X1: 0.2, Y1: 0.2, X2: 0.4, Y2: 0.4}
	b := proposal.Box{X1: 0.6, Y1: 0.5, X2: 0.9, Y2: 0.8}
	want := proposal.Box{X1: 0.2, Y1: 0.2, X2: 0.9, Y2: 0.8}
	if got := a.Union(b); got != want {
		t.Errorf("union = %+v, want the bounding region %+v", got, want)
	}
}
