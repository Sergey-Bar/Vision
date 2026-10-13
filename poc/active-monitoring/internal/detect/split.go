package detect

import (
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
	"github.com/Sergey-Bar/vision-poc/internal/classify"
)

// The approved / rejected split, the per-family roll-up, and the four counters
// that keep the two metric sets apart. T5.1, T5.2, T5.3, T5.4.
//
// The split is the point of this file. Before it there was one list and one
// counter: `Stats.Rejected` was an int, and the screen drew whatever the model
// emitted. So a lowering of the score threshold to catch smaller vehicles also
// created tracks for lamp posts, and nothing in the picture or the number said
// so. §4 of the plan calls the screen half D6; the tracker half is that the
// filter's decision was invisible to everything downstream of it.
//
// Two lists, and the naming is not decoration:
//
//   - APPROVED is what the pipeline decided about: class on the approved list,
//     score at or above SP_POC_SCORE_THRESHOLD, area at or above
//     SP_POC_MIN_BOX_AREA, in a permitted polygon. These reach the tracker and
//     can become events.
//   - GHOSTS is what was considered and refused: on the frame, drawn dimmed, and
//     counted NOWHERE. Not in the event rate, not in the tracker's memory, not in
//     the class histogram. A picture showing only approved boxes cannot answer
//     "why is the road empty?", and that is the question an operator asks in
//     minute three of a run that finds nothing.
//
// The types reuse `Box` rather than declaring a parallel one. A second box type
// in the same package is a second thing to convert at every boundary, and a
// conversion is a place for a coordinate to be transposed.

// Thresholds are the numbers the approved/rejected split is drawn with. They come
// from the run's configuration and are held as a value so the split is a pure
// function of (boxes, thresholds) and can be tested without a config or a
// database.
type Thresholds struct {
	// Score is SP_POC_SCORE_THRESHOLD: at or above this, a box of an approved
	// class is approved.
	Score float64
	// GhostScore is SP_POC_GHOST_MIN_SCORE: at or above this, a refused box is
	// still DRAWN, dimmed. Below it, nothing is drawn at all. The band exists
	// because a picture that shows only what passed cannot show what was
	// considered, and an operator watching an empty road cannot otherwise tell
	// "nothing there" from "the threshold is wrong".
	GhostScore float64
	// MinArea is SP_POC_MIN_BOX_AREA, a fraction of the frame. A box smaller
	// than this is a speck, a shadow edge or a letterbox band, and a
	// line-crossing machine that counts specks produces crossings that never
	// happened.
	MinArea float64
	// TrackMinFrames is SP_POC_TRACK_MIN_FRAMES. A track that has not been seen
	// this many times cannot emit a crossing, and its box is drawn as a ghost
	// with its own reason so the picture and the decision agree.
	TrackMinFrames int
	// Approved is the resolved class set, families already expanded. It is a
	// predicate rather than a list so a caller cannot iterate it and disagree
	// with the split about membership.
	Approved func(class string) bool
}

// Split is one frame's answer, in two parts.
type Split struct {
	// Approved are the boxes the pipeline decided about, before the
	// track-minimum-frames check.
	Approved []Box
	// Ghosts are the boxes it refused, each with the reason.
	Ghosts []Ghost
	// AIState is the five-word vocabulary state, carried so the screen prints
	// the REAL state rather than a hardcoded `answered`. D5.
	AIState string
	// Candidates is how many raw candidates the sidecar read out of the model
	// BEFORE any filter ran.
	//
	// It is an `int` and not `ai.Telemetry` on purpose. The counters below use one
	// number, and a typed dependency on the sidecar's package to carry one number
	// is what put `internal/ai` on this package's import list — a boundary
	// `scripts/verify.ps1` fails on, and fails correctly: the line-cross machine
	// has no business knowing what a sidecar is. The full telemetry belongs to the
	// caller and the screen, which are above both.
	//
	// It was also never assigned anywhere in production code, which meant
	// `BoxCandidates` and `CandidateShare()` returned zeros and `false` for every
	// frame of every run. The field is filled from the real answer in `Feed` now.
	Candidates int
}

// Ghost is a refused box and the reason it was refused.
//
// The reason is on the box rather than in a count because the reasons call for
// different actions. A box refused for its CLASS is fixed by editing
// SP_POC_CLASSES. One refused for its SCORE is fixed by lowering the threshold,
// and doing that makes more ghosts. One refused for its AREA is fixed by lowering
// SP_POC_MIN_BOX_AREA, and that is how specks get in. One refused for being too
// NEW is fixed by waiting. A single "rejected: 40" is four different pieces of
// advice and a reader can act on none of them.
type Ghost struct {
	Box
	// Reason is one of the Ghost* constants.
	Reason string
}

// The reasons a box is refused. Closed, because the screen prints them and a
// reason with a typo is a reason with no meaning.
const (
	GhostInvalid    = "not a usable box"
	GhostClass      = "class not on the approved list"
	GhostScore      = "below the score threshold"
	GhostArea       = "too small to be an object"
	GhostZone       = "outside every permitted polygon"
	GhostTooNew     = "track has not been seen long enough"
	GhostEmitting   = "the sidecar could not interpret this frame"
	GhostSuppressed = "approved, but the picture's cap left it undrawn"
)

// Apply is the split, and it is a PURE FUNCTION: given the same boxes and the
// same thresholds and the same zone it produces the same answer, with no clock
// and no state. The tracker is what has memory, and a decision that could change
// with memory would mean the same frame classified differently depending on what
// came before it.
//
// The ORDER of the checks is the order of what can be fixed. Validity, then
// class, then score, then area, then zone. Class comes before score on purpose: a
// box of a class nobody approved is not "a good detection that missed the
// threshold", it is a class this run was not asked about, and reporting it as a
// near miss invites an operator to lower the score threshold in response to a
// class that was never wanted at any score.
func Apply(boxes []Box, th Thresholds, zone Zone) Split {
	out := Split{Approved: []Box{}, Ghosts: []Ghost{}}
	approved := th.Approved
	if approved == nil {
		// A nil predicate approves NOTHING rather than everything. Getting this
		// backwards would put every class a camera can see onto a crossing line,
		// and the failure would be invisible: the run would report a busy road.
		approved = func(string) bool { return false }
	}
	engine := &Engine{zone: zone, cfg: Config{ScoreThreshold: th.Score, Tolerance: 1}}
	for _, b := range boxes {
		var reason string
		switch {
		case !b.Valid():
			reason = GhostInvalid
		case !approved(b.Class):
			reason = GhostClass
		case b.Score < th.Score:
			reason = GhostScore
		case b.Width()*b.Height() < th.MinArea:
			reason = GhostArea
		case !engine.inZone(b):
			reason = GhostZone
		}
		if reason == "" {
			out.Approved = append(out.Approved, b)
			continue
		}
		// Below the ghost floor, nothing is drawn at all. That is the point of
		// the floor: the sidecar has already refused to send noise, and this
		// refuses to draw it either.
		if b.Score >= th.GhostScore {
			out.Ghosts = append(out.Ghosts, Ghost{Box: b, Reason: reason})
		}
	}
	return out
}

// MarkTooNew demotes approved boxes whose track has not been seen long enough.
//
// It takes the minimum as a FUNCTION OF THE BOX rather than as a constant, because the
// minimum is a property of where the box was measured and the two are not the same
// number: a magnified crop produces a structurally better observation per sighting than
// a downscaled whole frame, and [Config.CutMinFrames] exists to say so. Passing the
// whole Thresholds would make it look like a second place the decisions are made, and
// it is not: it is the last of the five steps in Feed, applied to what the first two
// produced.
//
// A function and not a second parameter for the second bar, because the rule is per box
// and not per run: on one frame a car is found whole-frame and a bird is found in a
// crop, and both have to be held to their own bars in the same pass.
//
// It is a SEPARATE function from Apply because the tracker's state is not available to
// a pure function of the boxes. A track with one frame behind it is not yet an object:
// a shadow, a reflection and a compression artefact all look like an object for exactly
// one frame, and only time tells them apart.
//
// It runs AFTER the tracker's update and BEFORE the crossing machine, which is the only
// order in which "has this track been seen long enough" is a fact rather than a guess.
func MarkTooNew(approved []Box, seen func(bx Box) int, minFrames func(bx Box) int) ([]Box, []Ghost) {
	if seen == nil || minFrames == nil {
		return approved, nil
	}
	kept := make([]Box, 0, len(approved))
	var ghosts []Ghost
	for _, b := range approved {
		// A minimum of one means every box qualifies, which is the no-op and is
		// spelled out rather than left to fall out of the comparison.
		if min := minFrames(b); min <= 1 || seen(b) >= min {
			kept = append(kept, b)
			continue
		}
		ghosts = append(ghosts, Ghost{Box: b, Reason: GhostTooNew})
	}
	return kept, ghosts
}

// Tally is the per-family roll-up, and it is SEPARATE for the approved and the
// rejected lists. T5.2.
//
// The reason is arithmetic, not tidiness: "we found 12 cars" and "we rejected 9
// cars" are different measurements, and a reader shown one number for both cannot
// tell which one they are looking at. The grouping is by FAMILY because the
// picture groups by family, and a reader comparing the picture with the table is
// comparing like with like.
type Tally struct {
	// Approved maps a family to how many approved boxes of it were seen.
	Approved map[classify.Family]int
	// Rejected maps a family to how many of its boxes were refused, per reason.
	Rejected map[classify.Family]map[string]int
	// Suppressed maps a family to how many of its approved boxes the picture's
	// cap left undrawn. It is a THIRD number and it is not folded into Rejected:
	// a box that was approved and then not drawn is neither approved-and-hidden
	// nor rejected, and a reader told it was rejected would go looking for a
	// threshold that was never involved.
	Suppressed map[classify.Family]int
}

// NewTally is an empty tally with every family present, so the roll-up has four
// rows from the first frame rather than growing a row per detection. A family
// that has never been seen is a family with a ZERO, and a zero is a measurement.
func NewTally() Tally {
	t := Tally{
		Approved:   map[classify.Family]int{},
		Rejected:   map[classify.Family]map[string]int{},
		Suppressed: map[classify.Family]int{},
	}
	for _, f := range classify.Families {
		t.Approved[f] = 0
		t.Rejected[f] = map[string]int{}
		t.Suppressed[f] = 0
	}
	return t
}

// Add folds one frame's split in.
func (t Tally) Add(s Split) Tally {
	if t.Approved == nil {
		return t
	}
	for _, b := range s.Approved {
		t.Approved[classify.FamilyOf(b.Class)]++
	}
	for _, g := range s.Ghosts {
		f := classify.FamilyOf(g.Class)
		if t.Rejected[f] == nil {
			t.Rejected[f] = map[string]int{}
		}
		t.Rejected[f][g.Reason]++
	}
	return t
}

// Rows renders the tally in the taxonomy's fixed order, so two runs' reports are
// comparable line by line and a map's iteration order cannot leak into a diff.
func (t Tally) Rows() []TallyRow {
	out := make([]TallyRow, 0, len(classify.Families))
	for _, f := range classify.Families {
		row := TallyRow{Family: f, Approved: t.Approved[f], Suppressed: t.Suppressed[f]}
		for reason, n := range t.Rejected[f] {
			row.RejectedTotal += n
			row.Reasons = append(row.Reasons, ReasonCount{Reason: reason, Count: n})
		}
		sort.SliceStable(row.Reasons, func(i, j int) bool { return row.Reasons[i].Reason < row.Reasons[j].Reason })
		out = append(out, row)
	}
	return out
}

// TallyRow is one family's line.
type TallyRow struct {
	Family   classify.Family
	Approved int
	// Reasons is the refusal breakdown, so the reader can see WHICH decision
	// excluded them and not only how many were excluded.
	Reasons []ReasonCount
	// RejectedTotal is the sum, rendered beside the reasons rather than instead
	// of them.
	RejectedTotal int
	// Suppressed is how many were approved and then not drawn, which is a third
	// number and not a refusal.
	Suppressed int
}

// ReasonCount is one refusal reason and its count.
type ReasonCount struct {
	Reason string
	Count  int
}

// Counters are the numbers that keep the two metric sets apart. T5.4.
//
// This is the fix for a measurement that was quietly counting the wrong thing.
// A single `Approved` counter was incremented for every box over the score
// threshold, whatever the class, and the report's headline number was therefore a
// measure of the CLASSIFIER being read as a measure of the PIPELINE. A detector
// with good recall on chairs inflates it; a run with no approved class on the road
// scores zero while looking perfectly healthy.
//
// The load-bearing one is FramesAnswered. A rate computed over FramesAsked
// reports zero when the sidecar was down, and a zero detection rate from a sidecar
// that was never reachable is the most expensive sentence in this module.
type Counters struct {
	// FramesAsked is how many frames the run sent, whatever came back.
	FramesAsked int
	// FramesAnswered is how many the sidecar answered interpretably. Every RATE
	// is over this, never over FramesAsked.
	FramesAnswered int
	// FramesNoObjects is a subset of FramesAnswered: the model answered and saw
	// nothing. It is tracked separately because "answered, saw nothing" and "never
	// answered" are the two readings a reader has to tell apart and
	// FramesAnswered alone does not distinguish them.
	FramesNoObjects int
	// FramesAbsent is a subset of FramesAsked that is NOT a measurement: degraded,
	// not_asked and cannot_tell, counted per state so the fault table can be built
	// without re-reading a log.
	FramesAbsent map[string]int
	// BoxesApproved and BoxesRejected are the two halves of the split.
	BoxesApproved int
	BoxesRejected int
	// BoxesRejectedByReason is the breakdown. A single rejected total is four
	// different pieces of advice and a reader can act on none of them.
	BoxesRejectedByReason map[string]int
	// BoxCandidates is the sidecar's raw candidate count, summed. It is the
	// denominator that makes the others readable: 40 approved out of 10647
	// candidates is a model working, and 40 out of 40 is a filter doing all the
	// work with the classifier contributing nothing.
	BoxCandidates int
}

// NewCounters is a zeroed set with the maps made.
func NewCounters() Counters {
	return Counters{FramesAbsent: map[string]int{}, BoxesRejectedByReason: map[string]int{}}
}

// Observe folds one frame in. It is the ONLY place the counters move, so the
// arithmetic behind the report's headline numbers is in one function and can be
// read.
func (c *Counters) Observe(s Split) {
	if c.FramesAbsent == nil {
		c.FramesAbsent = map[string]int{}
	}
	if c.BoxesRejectedByReason == nil {
		c.BoxesRejectedByReason = map[string]int{}
	}
	c.FramesAsked++
	switch s.AIState {
	case aistate.Asked, aistate.NoObjects:
		c.FramesAnswered++
		if s.AIState == aistate.NoObjects {
			c.FramesNoObjects++
		}
	default:
		// An absent frame is counted as absent and NOT as a frame with zero
		// objects. This is the single line that keeps R4 true: a sidecar that was
		// down must not contribute to a detection RATE.
		c.FramesAbsent[s.AIState]++
	}
	c.BoxCandidates += s.Candidates
	c.BoxesApproved += len(s.Approved)
	c.BoxesRejected += len(s.Ghosts)
	for _, g := range s.Ghosts {
		c.BoxesRejectedByReason[g.Reason]++
	}
}

// DetectionRate is boxes approved per ANSWERED frame.
//
// The denominator is the answered frames and not the asked ones, and the reason
// is in Counters. With no answered frames the rate is ok=false rather than 0.0,
// because a rate over nothing is not a measurement and a zero here would render
// as a confident "0.00 detections per frame".
func (c Counters) DetectionRate() (rate float64, ok bool) {
	if c.FramesAnswered == 0 {
		return 0, false
	}
	return float64(c.BoxesApproved) / float64(c.FramesAnswered), true
}

// CandidateShare is the approved boxes as a fraction of the model's raw
// candidates: the number that separates "the model found a lot and we kept some"
// from "the model found a lot and we threw nearly all of it away because they
// were chairs". With no candidates — an absent frame, or a model that never ran —
// it is ok=false rather than 0.
func (c Counters) CandidateShare() (share float64, ok bool) {
	if c.BoxCandidates == 0 {
		return 0, false
	}
	return float64(c.BoxesApproved) / float64(c.BoxCandidates), true
}

// AbsentFrames is the total of the absent states.
func (c Counters) AbsentFrames() int {
	n := 0
	for _, v := range c.FramesAbsent {
		n += v
	}
	return n
}

// ClassHistogram counts approved boxes by CLASS, which is what an Alarm
// Definition is written against. The family tally cannot answer it: a family is a
// product grouping and a rule names a class.
func ClassHistogram(approved []Box) map[string]int {
	out := map[string]int{}
	for _, b := range approved {
		out[b.Class]++
	}
	return out
}

// FormatClassList renders a histogram in a stable order, for a log line or a
// report. Sorted by count then name, because a reader wants the biggest classes
// first and a stable order is what makes two runs' output diffable.
func FormatClassList(hist map[string]int) string {
	if len(hist) == 0 {
		return "(none)"
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(hist))
	for k, v := range hist {
		pairs = append(pairs, kv{k, v})
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p.k+"="+itoa(p.v))
	}
	return strings.Join(parts, " ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
