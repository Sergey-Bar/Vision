package novelty

import "fmt"

// The view-change detector: the rule that stops a moved camera from reading as a flood
// of presence events.
//
// # The problem
//
// `camera_integrity` and presence mode both run. A camera being moved produces an
// enormous novelty spike, which produces tracks, which produce events, which read as
// "everything passed at once". The camera-integrity event is CORRECT and the system is
// simultaneously reporting a storm of false presence.
//
// That is worse than reacting to nothing, because the operator has been told the
// camera moved AND that a hundred things happened, and the second of those is false.
// A system that reacts to a moved camera with fifty detections is a system an operator
// learns to turn off.
//
// # The signal, and why it is one this module already computes
//
// A view change is not "something changed". It is "THE WHOLE VIEW changed", and the
// novelty engine already measures the two numbers that tell them apart:
//
//   - [Score.Illumination] is the frame-wide brightness offset that `robust()` REMOVED
//     before thresholding. A camera panning away from a wall changes every pixel's
//     brightness at once, so this is large. A car crossing the frame does not change it.
//   - [Score.Spread] is how widely the residual is distributed after the offset is
//     removed. A moved camera leaves structure everywhere; a vehicle leaves structure in
//     one place.
//
// Brightness alone is not enough: a cloud crossing a window moves the whole frame's
// luma without moving the camera. Spread alone is not enough: a global compression
// change does the same. Both together, and sustained over a frame or two, is a view
// change and nothing else.
//
// So this is a measurement, not a new signal. It reads the score the engine already
// published and adds no per-frame cost.

// ViewVerdict is what the detector concluded about a frame.
type ViewVerdict int

const (
	// ViewStable is the ordinary case: the view is what it was.
	ViewStable ViewVerdict = iota
	// ViewChanged is the frame the view moved on. It is a single verdict and not a
	// mode, because a camera moves once and the consequence is declared once.
	ViewChanged
	// ViewSettling is the stretch after a change, while the baseline re-learns.
	//
	// It is a distinct verdict and not a boolean because the difference is what an
	// operator is told. "Stable" means presence events are being emitted normally.
	// "Settling" means they are being SUPPRESSED, and suppression that is not named is
	// silence, which this repository treats as the most expensive defect it has.
	ViewSettling
)

func (v ViewVerdict) String() string {
	switch v {
	case ViewChanged:
		return "view changed"
	case ViewSettling:
		return "view settling"
	default:
		return "view stable"
	}
}

// ViewChangeConfig is the detector's thresholds.
type ViewChangeConfig struct {
	// MinIllumination is the smallest frame-wide brightness offset that counts as a
	// view change, in luma units out of 255.
	//
	// It is compared against a SIGNED magnitude. The offset is negative as often as it
	// is positive, because a camera tilting toward the sky and one tilting toward the
	// ground are the same event, and a detector that only fires on one of them is a
	// detector that works in half the rooms.
	MinIllumination float64
	// MinSpread is the smallest distribution width, in luma units, that counts.
	MinSpread float64
	// SustainFrames is how many consecutive frames must agree before it is a view
	// change rather than a door opening.
	//
	// It is 2 and not 1 because the single-frame version of this rule is exactly the
	// false positive the anti-jitter rule elsewhere in this repository exists to
	// prevent, and applying two different standards to the same phenomenon is how a
	// pipeline ends up with a jitter rule and a no-jitter rule.
	SustainFrames int
	// SettleFrames is how many frames stay suppressed after a change.
	SettleFrames int
}

// Defaults are the measured ones, and the reasoning is in the fields.
func ViewChangeDefaults() ViewChangeConfig {
	return ViewChangeConfig{
		MinIllumination: 12,
		MinSpread:       8,
		SustainFrames:   2,
		SettleFrames:    25,
	}
}

// Validate refuses a configuration that cannot decide anything.
func (c ViewChangeConfig) Validate() error {
	if c.MinIllumination <= 0 {
		return fmt.Errorf("novelty: a view-change illumination threshold of %v cannot fire; a camera move changes the frame's brightness by far more than zero", c.MinIllumination)
	}
	if c.MinSpread < 0 {
		return fmt.Errorf("novelty: a view-change spread threshold of %v is negative", c.MinSpread)
	}
	if c.SustainFrames < 1 {
		return fmt.Errorf("novelty: %d frames of sustain is not a number of frames. One frame is exactly the single-frame-noise case SP_POC_TRACK_MIN_FRAMES exists to reject", c.SustainFrames)
	}
	if c.SettleFrames < 1 {
		return fmt.Errorf("novelty: %d settling frames is not a settle time. Zero would suppress nothing, which is a detector that exists and does nothing", c.SettleFrames)
	}
	return nil
}

// ViewChange reads the score stream and says whether the camera moved.
//
// It is a SEPARATE type from the Engine rather than a mode of it, because it consumes
// the engine's output rather than producing any: adding a mode would have meant a second
// set of thresholds in the same configuration block, and two sets of thresholds for two
// questions in one struct is how a reader ends up not knowing which applies.
type ViewChange struct {
	cfg ViewChangeConfig
	// agree counts consecutive frames that met BOTH thresholds.
	agree int
	// settle counts down the frames still suppressed.
	settle int
	// changes is how many view changes this detector has declared.
	changes int
	// suppressed is how many frames presence was gated because of them.
	//
	// It is the counter that makes the rule auditable. A suppression with no count is
	// silence, and silence in a security pipeline reads as "nothing happened", which is
	// the one sentence it must never be allowed to say.
	suppressed int64
}

// NewViewChange returns a detector, refusing a configuration that cannot decide.
func NewViewChange(cfg ViewChangeConfig) (*ViewChange, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &ViewChange{cfg: cfg}, nil
}

// Observe feeds one frame's score and returns what it concluded.
func (v *ViewChange) Observe(sc Score) ViewVerdict {
	if v == nil {
		return ViewStable
	}
	// A frame the engine could not score carries no evidence, and is treated as neither
	// a change nor a confirmation. Deciding on a frame with no measurement is how a
	// detector starts declaring camera faults on a run whose camera is fine.
	if sc.Reason == ReasonCorrupt || sc.Reason == ReasonFirstFrame {
		return v.verdict()
	}
	if v.settle > 0 {
		v.settle--
		v.suppressed++
		v.agree = 0
		return ViewSettling
	}
	moved := absF(sc.Illumination) >= v.cfg.MinIllumination && absF(sc.Spread) >= v.cfg.MinSpread
	if !moved {
		v.agree = 0
		return ViewStable
	}
	v.agree++
	if v.agree < v.cfg.SustainFrames {
		return ViewStable
	}
	// Declared. The agreement counter is cleared so a second move is a second event
	// rather than a continuation of the first, and the settle window opens.
	v.agree = 0
	v.changes++
	v.settle = v.cfg.SettleFrames
	return ViewChanged
}

// Suppress opens the settling window without declaring a change.
//
// It is separate from [ViewChange.Observe] because an operator who knows the camera
// moved, or a supervisor restarting after one, needs to gate presence WITHOUT claiming
// the system detected anything. A detector that can only be opened by a detection is a
// detector whose state cannot be set from outside, and an un-settable detector is a
// detector that suppresses forever.
func (v *ViewChange) Suppress() {
	if v == nil {
		return
	}
	v.settle = v.cfg.SettleFrames
	v.agree = 0
}

// Reset clears the state, for a restart.
func (v *ViewChange) Reset() {
	if v == nil {
		return
	}
	v.agree, v.settle, v.changes, v.suppressed = 0, 0, 0, 0
}

// verdict is what to report for a frame that carried no measurement, or for the frames
// after one: settling while the window is open.
func (v *ViewChange) verdict() ViewVerdict {
	if v.settle > 0 {
		return ViewSettling
	}
	return ViewStable
}

// Changes is how many view changes have been declared.
func (v *ViewChange) Changes() int {
	if v == nil {
		return 0
	}
	return v.changes
}

// SuppressedFrames is how many frames presence was gated.
//
// It is reported on the same screen as the event count, because a run that declares one
// camera move and suppresses nothing, and a run that declares one and suppresses four
// hundred frames, are different runs and the difference is what an operator needs.
func (v *ViewChange) SuppressedFrames() int64 {
	if v == nil {
		return 0
	}
	return v.suppressed
}

// Settling reports whether the window is open right now.
func (v *ViewChange) Settling() bool { return v != nil && v.settle > 0 }

// Describe is the one line for the report and the screen.
func (v *ViewChange) Describe() string {
	if v == nil {
		return "view integrity: not watched"
	}
	state := "stable"
	if v.Settling() {
		state = "SETTLING, presence suppressed"
	}
	return fmt.Sprintf("view integrity: %d change(s) declared, %d frame(s) suppressed, %s",
		v.changes, v.suppressed, state)
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
