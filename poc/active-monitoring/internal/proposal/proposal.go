// Package proposal is the shared vocabulary of the trigger path: one box, one
// candidate, declared once.
//
// It exists because three packages each declared their own Box and the detector
// could not be written without importing one of them. `ai.Box` came from the
// Python sidecar's wire format, `detect.Box` from the line-crossing test and
// `overlay.Box` from the drawing routine, and they agreed on the six field names
// and on nothing else: `detect.Box` and `ai.Box` carry a class and a score,
// `novelty.Box` carries neither because a connected component has not been told
// what it is yet. Three declarations of one idea is how a fifth arrives
// differently again.
//
// So the type lives here, in a package that imports nothing, and the other three
// name it with a type ALIAS rather than a wrapper:
//
//	ai.Box     = proposal.Box
//	detect.Box = proposal.Box
//	novelty.Box = proposal.Box
//
// An alias, not a new defined type, is the whole trick. A wrapper struct would
// need a conversion at every one of the two dozen call sites that read a box,
// and every one of them is a place a coordinate could be rescaled by accident.
// An alias changes no call site at all: the existing 24 `ai` tests, 44 `detect`
// tests and 19 `novelty` tests compile against this file unmodified, and their
// passing is the evidence that nothing moved.
//
// The JSON tags are the sidecar's wire format and they are load-bearing. `ai`
// decodes responses with `DisallowUnknownFields`, so renaming a tag here is a
// breaking change to a published contract, not a refactor.
//
// Coordinates are normalised 0..1 against the FRAME, never against the model's
// input. A detector running at 640x640 and a camera at 1920x1080 have to produce
// the same numbers, or the frame size becomes a hidden parameter of every
// decision downstream. The one place that scales is the decoder, and it undoes
// its own letterbox before it gets here.
package proposal

import "math"

// Box is one detection or one measured region, in normalised 0..1 coordinates
// against the frame.
//
// The class and score are the union of what the three callers needed, not a
// widening of any of them: `novelty` leaves them at their zero values for a
// connected component that no model has looked at yet, and a region with an
// empty class and a zero score is not a detection — it is a place the picture
// changed, which is what it always was.
//
// A box is normalised and NOT clamped to 0..1. A model may legitimately place an
// edge slightly outside the frame, and clamping here would move an object to
// where the operator can see it instead of where the model said it is. What is
// refused is a box with no extent, in [Valid].
type Box struct {
	Class string  `json:"cls"`
	Score float64 `json:"score"`
	X1    float64 `json:"x1"`
	Y1    float64 `json:"y1"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
}

// Candidate is a box carrying the identity of what the model thinks is in it.
//
// The trigger path's `soak.Trigger` interface hands back candidates, and that
// signature could not change without touching every call site, so this is what a
// detector returns: `cand.Class`, `cand.Score` and `cand.Box` all address one
// value, because the class and the score are FIELDS OF THE BOX rather than
// siblings of it.
//
// They are promoted from the embedded Box, not redeclared beside it. Writing
// `Candidate{Box; Class; Score}` compiles, and it is a trap: the outer `Class`
// shadows `Box.Class` at a shallower depth, so `cand.Class` reads the outer one
// while `cand.Box.Valid()` — which is what the detector's own gate calls — reads
// the inner one and finds it empty. A decoder that filled the outer field would
// therefore produce candidates that all fail `Valid()`, with no error anywhere,
// which is the silent-zero shape this module has already been bitten by twice
// (the sidecar's unknown-field wire mismatch and its `input_w` key). One name,
// one place to set it.
type Candidate struct {
	Box
}

// Centre is the point the line-crossing test uses. A box that straddles a line
// with its centre on the line has not crossed it.
func (b Box) Centre() (float64, float64) { return (b.X1 + b.X2) / 2, (b.Y1 + b.Y2) / 2 }

// Width and Height are the box's size in normalised units.
func (b Box) Width() float64  { return b.X2 - b.X1 }
func (b Box) Height() float64 { return b.Y2 - b.Y1 }

// NonEmpty reports whether the box names an actual region.
//
// A full-frame box is non-empty; an all-zero box is not, and an all-zero box on a
// pushed event is the worst thing this pipeline can emit.
func (b Box) NonEmpty() bool { return b.X2 > b.X1 && b.Y2 > b.Y1 }

// Area is the fraction of the frame the box covers.
func (b Box) Area() float64 {
	if !b.NonEmpty() {
		return 0
	}
	return (b.X2 - b.X1) * (b.Y2 - b.Y1)
}

// Valid reports whether a DETECTION is usable: it has a class, a finite score
// and positive extent.
//
// This is stricter than [Box.NonEmpty] on purpose. A measured region with no
// class is a real measurement; a detection with no class is an answer this build
// cannot interpret, and the two must not be checked by the same predicate or a
// decoder bug would be reported as an empty scene.
//
// The bound is generous (-0.5 .. 1.5) for the same reason NonEmpty does not
// clamp: a box hanging off the edge of the frame is a real detection of a real
// object that is half out of shot. A box at 4.0 is not.
func (b Box) Valid() bool {
	if b.Class == "" || math.IsNaN(b.Score) || math.IsInf(b.Score, 0) {
		return false
	}
	if b.X2 <= b.X1 || b.Y2 <= b.Y1 {
		return false
	}
	if math.IsNaN(b.X1) || math.IsNaN(b.Y1) || math.IsNaN(b.X2) || math.IsNaN(b.Y2) {
		return false
	}
	return b.X1 >= -0.5 && b.Y1 >= -0.5 && b.X2 <= 1.5 && b.Y2 <= 1.5
}

// Union merges two boxes, which is how several components in one region become
// one region rather than several overlapping boxes on the operator's screen.
func (b Box) Union(o Box) Box {
	if !b.NonEmpty() {
		return o
	}
	if !o.NonEmpty() {
		return b
	}
	m := b
	if o.X1 < m.X1 {
		m.X1 = o.X1
	}
	if o.Y1 < m.Y1 {
		m.Y1 = o.Y1
	}
	if o.X2 > m.X2 {
		m.X2 = o.X2
	}
	if o.Y2 > m.Y2 {
		m.Y2 = o.Y2
	}
	return m
}
