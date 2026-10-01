// Package ortdetect is the in-process ONNX detector: an object detector that
// runs in this process, at the frame rate, with real classes and real scores.
//
// # Why this exists
//
// The two most recent real runs of this system approved 6,578 and 297 boxes and
// recorded ZERO vehicles, ZERO people and ZERO animals. Not because the detector
// was wrong, but because there was no detector: the shipped trigger is a
// whole-frame change-fraction threshold, one-sided on brightness, and a fraction
// has no vocabulary. It can say "the picture changed" and nothing else.
//
// The only real object detector in this repository until now is a Python sidecar,
// used for measurement, at a measured ~0.9 s per frame against a 0.8 s deadline.
// A detector slower than the frame interval does not degrade gracefully here:
// `internal/recorder`'s bounded queue BLOCKS rather than dropping frames, and the
// supervisor kills ffmpeg with a recorded fault. So the sidecar was never a
// candidate for the trigger path, only a measuring instrument.
//
// This package is in-process, so the cost per frame is one ORT session call.
//
// # What it is not
//
// It is not a verifier. It proposes; the run decides. `SP_POC_SCORE_THRESHOLD` and
// the approved class list are the decision and they live in the run's
// configuration where a reader can see them. This package's job is to refuse to
// emit noise and to report what it did in numbers.
//
// # The boundary, and why it is strict
//
// This package must not import `internal/store`, `internal/soak`, `internal/detect`,
// `internal/evidence` or `internal/xprotect` — and `scripts/verify.ps1` checks
// that, because the rules it holds are the ones that make a measurement
// checkable. It may import `internal/proposal` (the box vocabulary), `internal/ai`
// (the absence states, which must not drift) and `internal/classify` (families).
//
// The reason is the same one `internal/vlm` has: a decoder that can read the
// database can be handed a recorder's answer instead of computing one, and the
// claim "this number was measured" becomes unfalsifiable.
//
// # Absences
//
// A missing AI observation is never a negative observation, and the words are the
// four the contract already fixes: asked, no_objects, degraded, not_asked. A
// session error is `degraded`. It is NEVER `no_objects`, and that distinction is
// the difference between "we looked and there was nothing" and "we did not look",
// which is the defect class this repository has shipped twice.
package ortdetect

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/ai"
	"github.com/Sergey-Bar/ai-captain-poc/internal/proposal"
)

// Box is one detection, normalised 0..1 against the FRAME.
//
// An alias of [proposal.Box], the same type the tracker and the sidecar path use,
// so a box produced here needs no conversion on its way to `detect.Engine`. The
// JSON tags are the sidecar's wire format and are inherited with it.
type Box = proposal.Box

// The four absence states, aliased from internal/ai so they cannot drift.
//
// `CannotTell` is NOT among them as a separate word here: a decoder either
// decoded the frame or it did not. `CannotTell` is the sidecar's word for a reply
// that arrived and could not be interpreted, and this package produces the same
// condition — a tensor that is not a detector's output — as a [Refusal] rather
// than as a state. The refusal is strictly better information, and the
// translation into `degraded` happens at the one place that owns it.
const (
	StateAsked     = ai.StateAnswered
	StateNoObjects = ai.StateNoObjects
	StateDegraded  = ai.StateDegraded
	StateNotAsked  = ai.StateNotAsked
)

// StateFor maps a decode outcome to one of the four states.
//
// The mapping is a table rather than a chain of ifs because the ONE case that
// matters is the second: a decoder failure is `degraded`, never `no_objects`. A
// session error reported as `no_objects` is a healthy answer derived from a
// failed read, which is exactly what AC6 forbids and what this repository shipped
// once — 169 frames of `cannot_tell` in a report that said the road was empty.
func StateFor(refusal *Refusal, emitted int) string {
	switch {
	case refusal != nil:
		return StateDegraded
	case emitted == 0:
		return StateNoObjects
	}
	return StateAsked
}

// Telemetry is the decoder's own report about what it did with one frame.
//
// Every count exists to make one failure distinguishable from another, and this
// package has been bitten by the confusion twice. A run that reports "0
// detections" has told its reader nothing: the model may have found nothing
// (Candidates large, PreNMS zero), or the decoder may have thrown everything away
// (PreNMS large, PostNMS zero). Those are opposite diagnoses and they need
// opposite fixes.
//
// It is a distinct type from [ai.Telemetry] rather than an alias because this one
// carries fields the sidecar's does not — the DECLARED shape, the record layout
// and the input geometry — and it is converted at one place rather than by
// structural coincidence.
type Telemetry struct {
	// Candidates is how many records the output tensor held, before any filter.
	Candidates int
	// PreNMS is how many cleared the emit floor.
	PreNMS int
	// PostNMSClass is the class-aware pass's output; PostNMS is the class-agnostic
	// one's. Both are reported because the second pass is the one that catches a
	// `car` and a `truck` on one object, and a reader who sees PostNMSClass=9 and
	// PostNMS=5 learns something the total alone does not say.
	PostNMSClass int
	PostNMS      int
	// CapDropped is how many boxes SP_POC_DET_MAX_BOXES removed. It is counted
	// rather than silently dropped, because a cap that silently drops is a cap that
	// lies — and 100 boxes per frame is a cap that a crowded junction reaches.
	CapDropped int
	Emitted    int
	// DroppedDegenerate counts boxes with no area AFTER clamping, and
	// DroppedTiny counts those below the area floor or outside the aspect range.
	// Both are counted rather than silently dropped.
	DroppedDegenerate   int
	DroppedTiny         int
	DroppedUnknownClass int
	// Fields is the measured record width, counted from the tensor rather than
	// assumed. It is printed beside the model name because "the export has 85
	// fields" resolves most "why are the classes wrong" questions in one sentence.
	Fields int
	// Classes is how many class names the model carried in its own metadata. Zero
	// is a real and common value, and it is reported rather than hidden: falling
	// back to COCO-80 is a fact about the answer.
	Classes int
	// HasObjectness is the three-state decision, as its own string so the value is
	// readable in a report without a lookup table.
	HasObjectness Objectness
	// Layout is `fields-last` ([1,8400,84]) or `fields-first` ([1,84,8400]), and it
	// is read from the DECLARED shape. It is in the telemetry because "which axis
	// did you read" is the first question anyone asks about a zero.
	Layout string
	// DeclaredShape is the session's own shape for this output, verbatim.
	DeclaredShape []int64
	// CoordSpace is `pixel`, `fraction`, or `unknown`. `unknown` means the decode
	// was refused, and a box in an unknown unit has no place on the picture.
	CoordSpace string
	// InputW and InputH are the model's declared input, ContentW and ContentH the
	// letterboxed content inside it, and PadX and PadY where that content sits.
	//
	// All six are here because they are what make an operator's arithmetic
	// checkable: a square 640x640 input on a 16:9 frame means the top and bottom
	// fifth of the picture is letterbox, every box near the edge sits inside that
	// dead band, and the only way to see that is to see the model's input beside
	// the frame's own dimensions.
	InputW, InputH     int
	ContentW, ContentH int
	PadX, PadY         int
	// Note is a human sentence for a specific refusal. It is not prose anyone
	// matches on — the machine-readable refusals are [Refusal.What] and
	// [Refusal.Detail], and this is the line beside them in a report.
	Note string
}

// ToAITelemetry converts to the shape the pipeline and the report already read.
//
// The three frame counts the plan requires — frames decoded, frames measured,
// frames not measured — are not this type's job: they belong to the run, which is
// the only thing that knows how many frames the source produced. What this
// conversion does carry is everything about HOW a frame was measured, so a
// detection rate printed beside it can be argued with.
func (t Telemetry) ToAITelemetry() ai.Telemetry {
	hasObj := t.HasObjectness == ObjectnessYes
	out := ai.Telemetry{
		Layout: t.Layout,
		// This decoder reads fused outputs and publishes no family name of its
		// own; "fused" is the fact, and claiming anything else would be a claim
		// about a model this code has not inspected.
		DecoderFamily:     "fused",
		CoordSpace:        t.CoordSpace,
		HasObjectness:     &hasObj,
		Fields:            t.Fields,
		Classes:           t.Classes,
		InputW:            t.InputW,
		InputH:            t.InputH,
		Candidates:        t.Candidates,
		PreNms:            t.PreNMS,
		PostNmsClass:      t.PostNMSClass,
		PostNms:           t.PostNMS,
		DroppedDegenerate: t.DroppedDegenerate,
		DroppedTiny:       t.DroppedTiny,
	}
	if t.Note != "" {
		out.Note = t.Note
	}
	return out
}

// Latencies is the distribution of inference cost, and it is what the throughput
// gate is computed from.
//
// p99 rather than a mean, because the question the preflight check answers is
// whether the SLOWEST frames fit the frame interval. A mean over a distribution
// with a long tail will fit an interval that the tail overruns regularly, and the
// overrun shows up as `ai_pipe_backpressure` on the frames that matter.
//
// The p99 is not computed by sorting on every frame. A reservoir would be cheaper
// but its p99 moves as the sample fills, which makes a run's reported p99 depend
// on how long the run was — and two runs of the same clip would not be
// comparable. A bounded ring of the most recent N is used instead: it is exact
// over the window it covers and the window is declared.
type Latencies struct {
	// Window is how many recent samples the percentiles are computed over. It is
	// printed beside the percentiles because a p99 without its window describes a
	// period of time that nobody named.
	Window int
	recent []float64
	next   int
	filled bool
}

// DefaultLatencyWindow is how many recent frames the percentiles cover.
//
// 1024 frames is roughly 34 seconds at 30 fps and 3.4 minutes at 5 fps, so the
// window is long enough to cover a traffic-light cycle on either and short enough
// that a detector whose latency changes mid-run is not reported as one number for
// the whole run.
const DefaultLatencyWindow = 1024

// NewLatencies builds a collector with an explicit window.
func NewLatencies(window int) *Latencies {
	if window < 1 {
		window = DefaultLatencyWindow
	}
	return &Latencies{Window: window, recent: make([]float64, 0, window)}
}

// Observe records one frame's inference cost in milliseconds.
func (l *Latencies) Observe(ms float64) {
	if l == nil {
		return
	}
	if len(l.recent) < l.Window {
		l.recent = append(l.recent, ms)
		return
	}
	// Full: overwrite the oldest, which `next` points at. A ring rather than a
	// growing slice, because a run is measured for a week and the bound is the
	// point of the declared window.
	l.recent[l.next] = ms
	l.next = (l.next + 1) % l.Window
	l.filled = true
}

// Count is how many samples the current percentiles cover, which is less than the
// window until the window fills.
func (l *Latencies) Count() int {
	if l == nil {
		return 0
	}
	return len(l.recent)
}

// sorted returns a copy in ascending order, leaving the ring intact.
func (l *Latencies) sorted() []float64 {
	out := make([]float64, len(l.recent))
	copy(out, l.recent)
	sort.Float64s(out)
	return out
}

// Percentile returns the p-th percentile in [0,100] over the current window.
//
// Nearest-rank on the sorted sample: the value at index ceil(p/100 * n) - 1. No
// interpolation, because an interpolated percentile is a number no frame produced
// and this module's rule is that a measurement is something that happened.
func (l *Latencies) Percentile(p float64) (float64, bool) {
	if l == nil || len(l.recent) == 0 {
		return 0, false
	}
	s := l.sorted()
	if p <= 0 {
		return s[0], true
	}
	if p >= 100 {
		return s[len(s)-1], true
	}
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(s) {
		rank = len(s)
	}
	return s[rank-1], true
}

// P50, P95 and P99, each reporting whether it could be computed.
//
// The bool is `ok=false` over an empty window and it is load-bearing: a percentile
// with no samples is not zero, and a report that printed 0 ms because nothing had
// been measured would claim the detector was infinitely fast.
func (l *Latencies) P50() (float64, bool) { return l.Percentile(50) }
func (l *Latencies) P95() (float64, bool) { return l.Percentile(95) }
func (l *Latencies) P99() (float64, bool) { return l.Percentile(99) }

// Max is the slowest frame in the window.
func (l *Latencies) Max() (float64, bool) {
	if l == nil || len(l.recent) == 0 {
		return 0, false
	}
	s := l.sorted()
	return s[len(s)-1], true
}

// Min is the fastest frame in the window. It is here because a p99 with no
// accompanying minimum cannot distinguish "consistently slow" from "fast most of
// the time and pathological once", and those two need different responses.
func (l *Latencies) Min() (float64, bool) {
	if l == nil || len(l.recent) == 0 {
		return 0, false
	}
	s := l.sorted()
	return s[0], true
}

// AchievableFPS is the detection rate the measured p99 supports: 1000 / p99.
//
// It is the arithmetic the preflight refusal is built on, and it is named here
// rather than at the call site so the gate and the refusal cannot compute
// different things from the same measurement.
//
// It reports `ok=false` over an empty window rather than an infinite rate.
func (l *Latencies) AchievableFPS() (float64, bool) {
	p99, ok := l.P99()
	if !ok || p99 <= 0 {
		return 0, false
	}
	return 1000 / p99, true
}

// HoldsFrameRate reports whether a declared rate fits in a second of wall clock.
//
// This is the whole of the throughput gate, and it is one comparison:
// det_p99 x ai_fps < 1000. A rate of 0 means EVERY DECODED FRAME, and the
// measured decode rate stands in for it, because that is the demand being made.
//
// The arithmetic is returned as text so the refusal can PRINT it. A refusal that
// says "cannot hold the frame rate" sends its reader to a profiler; one that says
// "det_p99=61.2ms x ai_fps=30 = 1836ms > 1000ms per second" tells them exactly
// which of the two numbers to change.
func (l *Latencies) HoldsFrameRate(aiFPS int, decodeFPS float64) (ok bool, arithmetic string) {
	p99, measured := l.P99()
	if !measured {
		return false, "no inference latency has been measured yet, so the declared rate cannot be checked"
	}
	// A p99 of zero is not a free inference; it is a clock that did not run.
	// [Latencies.AchievableFPS] already refuses it, and the gate must agree —
	// otherwise the headline rate says "cannot compute" while the gate says "fits",
	// and a reader has two numbers that contradict each other.
	if p99 <= 0 {
		return false, sprintf("the measured p99 is %.3fms, which is not a measurement: a clock that "+
			"did not run is not a free inference. Re-measure before trusting this gate", p99)
	}
	demand := aiFPS
	what := sprintf("ai_fps=%d", aiFPS)
	if aiFPS == 0 {
		// Every decoded frame. The decode rate is the demand, and the detector
		// must fit inside a second of it.
		demand = int(math.Ceil(decodeFPS))
		what = sprintf("ai_fps=0 (every decoded frame, decode_fps=%.2f)", decodeFPS)
	}
	perSecond := p99 * float64(demand)
	arithmetic = sprintf("%s: det_p99=%.1fms x %s = %.0fms of a 1000ms second", what, p99, what, perSecond)
	return perSecond < 1000, arithmetic
}

// sprintf is fmt.Sprintf with one argument shape, so the arithmetic strings above
// read as arithmetic rather than as format directives at every call site.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func roundTo(v float64) float64 { return math.Round(v) }

// Tensor is one raw model output, with the shape the SESSION DECLARED rather
// than the shape anybody assumed.
//
// The declared shape is the only trustworthy thing about an output tensor's
// layout, and it is what decode.go reads its record axis from. A []float32 with no
// shape attached cannot distinguish a YOLO11n export from a YOLOv8 one: they are
// the same numbers in a different order.
//
// Carrying the name as well is what lets a report say WHICH output of a
// multi-output model failed, rather than "the first one".
type Tensor struct {
	Name string
	Dims []int64
	Data []float32
}

// nowUTC is here rather than imported so every timestamp this package writes has
// one spelling. Storage is UTC everywhere; a local-time timestamp in a table the
// report reads is a bug that only appears in November.
func nowUTC() time.Time { return time.Now().UTC() }

// unusedGuards keep the compiler honest about helpers that exist for symmetry
// with the Python reference and are not yet called by the fused path. They are
// referenced here rather than deleted because each names a refusal a caller will
// need the moment an anchored model is pointed at this decoder, and a refusal
// discovered during a demo is worth less than one discovered by reading.
var (
	_ = errAnchored
	_ = checkFamily
	_ = joinNames
)
