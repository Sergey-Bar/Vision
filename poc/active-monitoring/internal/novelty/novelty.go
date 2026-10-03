// Package novelty is the local novelty engine: pure Go, licence-free, and the
// half of the trigger that owns the region.
//
// The split of labour with the model is the honest one. The Hafnia VLM API
// documents no response schema and no region or bounding-box output — it returns
// text. So this package MEASURES the region from pixels the process already has
// (recorder.Frame.RGB), and the model is asked what is happening in it and is
// never asked for coordinates. The box on the pushed XProtect event is therefore
// ours and measured; the label on it is the model's and attributed.
//
// # A score is not a verdict
//
// Every result here is a score and a reason. There is no field that says whether
// something is an incident, because the only thing that can say that is the model
// and it is asked once, per event, on the clip. A novelty engine that emitted a
// verdict would be a second opinion nobody could trace.
//
// # Two modes, one interface
//
//   - [ModeTemporal] diffs against the PREVIOUS frame and returns a measured box
//     from connected components. This is the `alarm` class: a localised change.
//   - [ModeReference] diffs against a PINNED reference view and returns no box,
//     because the whole frame changed and a full-frame box is the honest one.
//     This is the `camera_integrity` class: a camera that has been moved or
//     covered reports nothing at all.
//
// # Both ways of getting this wrong are silent, so both are tested
//
// A [ModeReference] score must not rise on a moving object: a car crossing an
// intact view is a large temporal change and a small global one. A
// [ModeTemporal] score must not fire on a lighting change: every cell moves and
// nothing happened. Both are addressed the same way — a robust illumination
// offset is removed before the mask is thresholded, so the score measures a
// CHANGE IN THE SCENE rather than a change in its brightness.
package novelty

import (
	"fmt"
	"math"
	"sort"

	"github.com/Sergey-Bar/ai-captain-poc/internal/proposal"
)

// Mode is which comparison the engine makes.
type Mode string

// The two modes. A closed set, because "novelty" without a stated reference is a
// word and not a measurement.
const (
	// ModeTemporal diffs against the previous frame and yields a measured box.
	ModeTemporal Mode = "temporal"
	// ModeReference diffs against a pinned reference view and yields no box.
	ModeReference Mode = "reference"
)

// ValidMode reports whether m is one of the two modes this build operates.
func ValidMode(m Mode) bool { return m == ModeTemporal || m == ModeReference }

// Box is a measured region, normalised 0..1 against the FRAME.
//
// An ALIAS of [proposal.Box], the same type `detect.Box` and `ai.Box` name. It
// carries a class and a score that this package never fills in, because a
// connected component has not been told what it is yet; it reads one engine's
// vocabulary and another's when a detector fills them.
//
// [Box.NonEmpty] — the predicate this package actually uses — moved to `proposal`
// with the type. Valid is deliberately NOT the predicate this package uses: Valid
// requires a class, and a mask component has none, so checking a mask with Valid
// would refuse every region the novelty engine ever found.
type Box = proposal.Box

// Reason names why a score is what it is, in a fixed vocabulary rather than a
// sentence. Nothing in this module matches on prose.
type Reason string

// The reasons a mask can report.
const (
	// ReasonFirstFrame is the frame that establishes a baseline. There is
	// nothing to compare it against and it is not a zero.
	ReasonFirstFrame Reason = "first_frame"
	// ReasonUnpinned is a reference-mode score with no pinned view yet.
	ReasonUnpinned Reason = "unpinned"
	// ReasonIlluminationOnly means the whole difference was accounted for by a
	// brightness change, so no cell survived the threshold.
	ReasonIlluminationOnly Reason = "illumination_only"
	// ReasonBelowRelease means the score is under the release level, so the
	// sustain streak was reset and nothing fired.
	ReasonBelowRelease Reason = "below_release"
	// ReasonNotSustained means the score is above the threshold but not for
	// long enough. One frame is a car; a hundred is a moved camera.
	ReasonNotSustained Reason = "not_sustained"
	// ReasonCooldown means the score is above the threshold but the cooldown
	// after the last event has not elapsed.
	ReasonCooldown Reason = "cooldown"
	// ReasonChanged is a score above the threshold, sustained, and ready.
	ReasonChanged Reason = "changed"
	// ReasonBelowThreshold is a score under the threshold.
	ReasonBelowThreshold Reason = "below_threshold"
	// ReasonNoComponent is a score above the threshold whose changed cells are
	// too scattered or too small to be a region.
	ReasonNoComponent Reason = "no_component"
	// ReasonNoPrevious is a temporal score with no previous frame yet.
	ReasonNoPrevious Reason = "no_previous"
	// ReasonCorrupt is a frame that could not be read at all: wrong dimensions,
	// or a buffer shorter than the dimensions claim. It is not a zero.
	ReasonCorrupt Reason = "corrupt_frame"
)

// Candidate is one proposed region for this frame.
//
// It is a PROPOSAL. Cheap perception proposes; the expensive model verifies. A
// candidate that reaches the operator has been through a clip and a model.
type Candidate struct {
	Box   Box
	Score float64
	// Cells is how many grid cells the component covers. It is a size, in the
	// unit the mask is actually measured in, which is what makes "too small to
	// be an object" a measurement rather than a vibe.
	Cells int
	// Reason is the same vocabulary the engine reported.
	Reason Reason
}

// Finding is a trigger event: the whole view has stayed different.
type Finding struct {
	Box   Box
	Score float64
	// Streak is how many consecutive frames the score has been at or above the
	// threshold. It is the number that separates one frame from a camera that
	// has not moved back.
	Streak int
	// RegionSource says whether the box was MEASURED or is the WHOLE FRAME. It
	// is reported rather than inferred, because "the box covers everything" and
	// "the box was measured and it happens to cover everything" are different
	// claims and an operator looking at an event deserves to know which.
	RegionSource RegionSource
	Reason       Reason
}

// RegionSource is where a reported region came from.
type RegionSource string

// The three sources.
const (
	// RegionMeasured is a box that came out of connected components.
	RegionMeasured RegionSource = "measured"
	// RegionWholeFrame is the honest box for a global change: everything.
	RegionWholeFrame RegionSource = "whole_frame"
	// RegionCut is a measured region that was MAGNIFIED before the detector saw it.
	//
	// It is a third value rather than a flag on the other two because the question the
	// column answers is "what did the model actually look at", and a crop changes that
	// answer in a way neither `measured` nor `whole_frame` describes. A bird 40 px
	// across in a 1920x1080 frame is 8 px after the whole-frame pass shrinks it to
	// 416x416 and is therefore below the detection floor; the same bird in a 200x150
	// crop arrives as roughly 100 px and is found easily. Both measurements are real,
	// both are stored, and a reader comparing two runs has to be able to tell which
	// one produced the box. Otherwise "the detector improved" is unfalsifiable,
	// because the only thing that changed was the size of the window.
	RegionCut RegionSource = "cut"
	// RegionNone means no region was reported at all.
	RegionNone RegionSource = "none"
)

// WholeFrame is the box for a global change.
func WholeFrame() Box { return Box{X1: 0, Y1: 0, X2: 1, Y2: 1} }

// Score is one frame's novelty measurement.
type Score struct {
	Mode Mode
	// Value is the fraction of the grid that survived the threshold, in [0,1].
	// It is a FRACTION and not a mean absolute difference because a fraction has
	// a scale that does not move when the exposure changes.
	Value float64
	// Illumination is the robust brightness offset that was removed before
	// thresholding. It is reported so a reader can see how much of the raw
	// difference was lighting, and it is the reason a lighting change does not
	// fire the detector.
	Illumination float64
	// Spread is the robust scale of the residual. Zero means the two frames
	// differ by a constant and nothing else, which is exactly a lighting change.
	Spread float64
	// Cells is how many cells survived the threshold.
	Cells int
	// Reason names the outcome.
	Reason Reason
}

// Fired reports whether a score should raise an event.
func (s Score) Fired() bool { return s.Value > 0 && s.Cells > 0 }

// Fired is the ONE comparison the trigger path makes on a novelty score, and it
// is a function so a replay can re-run the exact same predicate.
//
// It is a function rather than an inline `score >= threshold` at each site because
// that expression exists in three places now — the engine, the report and the
// replay — and three copies of a threshold comparison is three chances for one of
// them to use the RELEASE value by mistake, which would make a proof of a
// threshold the proof of a different threshold.
func Fired(score, threshold float64) bool { return score >= threshold }

// Sustained reports whether a streak has reached the required length.
//
// The reference trigger's whole argument is that one frame is a car and a hundred
// is a moved camera, and this is that hundred.
func Sustained(streak, sustain int) bool { return sustain > 0 && streak >= sustain }

// Unchanged reports whether a score means "the view did not change", and it is a
// function because the alternative is a status page deciding which reasons count as
// a negative and getting one of them wrong.
//
// It is FALSE for every reason that means the measurement never happened, and that
// distinction is the whole of RC-11:
//
//   - `unpinned` — a reference-mode run with no reference view has compared this
//     frame with NOTHING. "The camera has not moved" is a fabricated negative about
//     a physical object.
//   - `first_frame` and `no_previous` — the baseline, which is not a comparison.
//   - `corrupt_frame` — a frame that could not be read is not a camera that is fine.
//
// "Nothing changed" and "nothing was compared" print the same two words, and only
// one of them is a fact about a camera.
func (s Score) Unchanged() bool {
	switch s.Reason {
	case ReasonUnpinned, ReasonFirstFrame, ReasonNoPrevious, ReasonCorrupt:
		return false
	}
	return true
}

// IntegrityState is what a reference-mode run may be SAID to be.
//
// It is a closed set of three and not a boolean, because the interesting case is
// the one a boolean cannot hold: `Unpinned` is not `Unchanged` and not `Changed`.
type IntegrityState string

const (
	IntegrityUnpinned  IntegrityState = "unpinned"
	IntegrityUnchanged IntegrityState = "unchanged"
	IntegrityChanged   IntegrityState = "changed"
)

// IntegrityStateFor is the reference-mode status of an engine.
//
// The refusal it enforces is RC-11: an engine with no pinned reference reports
// `unpinned` and can never report `unchanged`, however quiet the frames are. A
// camera that was never pinned is not a camera that is fine, and the difference is
// the difference between a measurement and an assumption about a physical object.
func (e *Engine) IntegrityState() IntegrityState {
	if e.cfg.Mode != ModeReference {
		// A temporal engine has no reference and no integrity claim to make; saying
		// anything here would be a status about the wrong detector.
		return IntegrityUnpinned
	}
	// REDCHECK RC-11, durable half: no pinned reference, no `unchanged` verdict.
	// Deleting this guard makes a quiet camera read as an intact one, which is the
	// one answer this trigger must never produce by accident.
	if !e.Pinned() {
		return IntegrityUnpinned
	}
	if e.last.Reason == ReasonChanged || Fired(e.last.Value, e.cfg.Threshold) {
		return IntegrityChanged
	}
	return IntegrityUnchanged
}

// luma converts one RGB triple to luma.
//
// The weights are the usual ones. They matter here only for consistency: the
// engine compares luma to luma, so a slightly different weighting would change
// the score and not its meaning.
func luma(r, g, b byte) float64 {
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}

// grid is a downscaled luma plane.
type grid struct {
	W, H  int
	cells []float64
}

// lumaPlane downsamples an RGB frame to a WxH luma grid by box-averaging.
//
// Downsampling is not an optimisation. It is what makes the connected-component
// mask a statement about REGIONS rather than about JPEG noise: a 640x360 frame is
// 230k cells and every one of them is its own speckle, while 64x36 is 2304 cells
// and a car is 40 of them.
func lumaPlane(rgb []byte, w, h, gw, gh int) (grid, error) {
	if w <= 0 || h <= 0 || gw <= 0 || gh <= 0 {
		return grid{}, fmt.Errorf("novelty: a frame must have positive dimensions; got %dx%d into %dx%d", w, h, gw, gh)
	}
	if len(rgb) != w*h*3 {
		// A short buffer is a corrupt frame, not an empty one. Reading past it
		// would produce a score from memory nobody recorded.
		return grid{}, fmt.Errorf("novelty: the frame claims %dx%d (%d bytes of RGB) but carries %d bytes, so it is corrupt "+
			"and was not scored", w, h, w*h*3, len(rgb))
	}
	out := grid{W: gw, H: gh, cells: make([]float64, gw*gh)}
	// Map each output cell to a source rectangle, clamped so an odd aspect
	// ratio cannot produce a rectangle that runs off the end of the frame.
	for gy := 0; gy < gh; gy++ {
		y0, y1 := cellRange(gy, h, gh)
		for gx := 0; gx < gw; gx++ {
			x0, x1 := cellRange(gx, w, gw)
			sum, n := 0.0, 0
			for y := y0; y < y1; y++ {
				row := y * w * 3
				for x := x0; x < x1; x++ {
					i := row + x*3
					sum += luma(rgb[i], rgb[i+1], rgb[i+2])
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			out.cells[gy*gw+gx] = sum / float64(n)
		}
	}
	return out, nil
}

// cellRange is the half-open source range one output cell covers.
//
// The ranges are clamped and the last cell absorbs whatever is left, so gw=w+1
// does not silently drop a column.
func cellRange(i, extent, cells int) (int, int) {
	lo := i * extent / cells
	hi := (i + 1) * extent / cells
	if hi <= lo {
		hi = lo + 1
	}
	if hi > extent {
		hi = extent
	}
	if lo >= extent {
		lo = extent - 1
	}
	return lo, hi
}

// diff is the difference between two grids of the same shape, cell by cell.
func diff(a, b grid) []float64 {
	out := make([]float64, len(a.cells))
	for i := range a.cells {
		out[i] = a.cells[i] - b.cells[i]
	}
	return out
}

// lumaEpsilon is the largest residual spread that still counts as "every cell
// moved by the same amount".
//
// It exists because a float64 median subtraction leaves 1e-14 of dust behind, and
// `spread == 0` is a test on a floating-point number that will eventually fail on
// some machine. A stated epsilon is the difference between a test that says what
// it means and a test that says "the exact bit pattern happened to be zero".
const lumaEpsilon = 1e-6

// maxScaleLuma bounds how far the adaptive noise scale may run ahead of the
// absolute floor.
//
// It is the difference between the two silent failures. A pure MAD scale reads a
// large deviation as "a noisy sensor" and divides the real signal by it, so a
// camera that has been physically moved — where the residual is large everywhere
// and the MAD is large precisely because the change is real — comes out as
// nothing. Capping the scale means a change that covers the frame is still a
// change that covers the frame.
const maxScaleLuma = 64

// robust subtracts the median (the illumination offset) and scales by a noise
// floor, then reports how much is left.
//
// This is the whole of "a lighting change is not an event". A uniform exposure
// change moves every cell by the same signed amount, the median absorbs it, and
// nothing survives the threshold no matter how bright it got. A car moves some
// cells and leaves most of them alone, so the median is ~zero and the car
// survives. A camera that has been knocked moves every cell by a DIFFERENT
// amount, so the median is a compromise and the residual is everywhere.
//
// Both statistics are medians rather than means because a mean is dragged by the
// very thing being measured: the car's own pixels, or the whole new view.
func robust(d []float64, k, floor float64) (residual []float64, illumination, spread float64) {
	residual = make([]float64, len(d))
	if len(d) == 0 {
		return residual, 0, 0
	}
	illumination = median(d)
	absDev := make([]float64, len(d))
	for i, v := range d {
		residual[i] = v - illumination
		absDev[i] = math.Abs(residual[i])
	}
	spread = median(absDev)
	// The scale is the larger of the measured noise floor and an absolute one,
	// capped. Without the floor, two frames that are IDENTICAL apart from
	// rounding have a spread of zero, any residual is infinitely significant,
	// and a single differing cell becomes a component. Without the cap, a real
	// change large enough to raise the spread is divided out of existence.
	scale := math.Min(math.Max(spread*k, floor), maxScaleLuma)
	if scale <= 0 {
		return residual, illumination, 0
	}
	for i := range residual {
		residual[i] /= scale
	}
	return residual, illumination, spread
}

// median is the median of a slice, without mutating it.
//
// The input is copied rather than sorted in place because several callers hold
// the same difference slice and a sort would be a write to shared state from a
// read-only function.
func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	mid := len(c) / 2
	if len(c)%2 == 1 {
		return c[mid]
	}
	return (c[mid-1] + c[mid]) / 2
}
