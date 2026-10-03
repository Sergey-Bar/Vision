package novelty

import (
	"fmt"
)

// At what rate the novelty pass runs, and why that is a decision rather than a default.
//
// # The problem
//
// The crop pass makes novelty LOAD-BEARING. Novelty is what PROPOSES the region; a
// region that is never proposed is never cropped; and a region that is never cropped is
// never measured at full resolution, which is the only way the smallest objects are ever
// seen. So the novelty rate is not a cost knob. It decides whether the headline case
// works.
//
// # The arithmetic
//
// A bird at 30 m is on screen for about 1.5 seconds. It needs to be proposed at least
// three times in that window, or it is proposed once or twice and its detection depends
// entirely on where in its dwell the gate happened to sample. Three proposals in 1.5 s
// is 2 per second.
//
// Two per second is not a margin. It is the floor, it is reached only if the bird's
// dwell is the optimistic end of the estimate, and "the smallest object the product
// exists to report is found about twice" is a sentence that does not survive a room with
// a CV reviewer in it.
//
// So the honest shape is a novelty rate ABOVE the detection rate:
//
//	novelty   at the rate it needs to catch motion, because it is cheap and pure Go
//	detector  only on frames novelty proposed, because it is neither
//
// That inverts the cost model the scale proposal assumes, which prices one detector
// inference per sampled frame and charges novelty nothing. See [Gate] for the counting,
// because a rate that is not counted is a rate nobody can project a cost from.

// Gate decides whether a given decoded frame is one the novelty pass runs on.
//
// It is a SEPARATE type from the Engine rather than a field on it, because the engine
// holds per-frame measurement state and the gate holds a rate. Merging them would make
// "what does this engine cost" a question whose answer depends on how often you asked.
//
// # Why it takes the decode rate
//
// A gate can only fire on a FRAME BOUNDARY, so the rate it can actually achieve at a
// given source frame rate is quantised: at 25 fps a 10/s cadence can land on 8.4/s,
// because the nearest frame boundary is every third frame and every second frame is
// 12.5/s. That is a 16% shortfall, and it is the kind that hides.
//
// So the gate is told the decode rate, clamps its interval to one frame, and REPORTS
// both numbers: [Gate.Rate] is what was asked for and [Gate.EffectiveRate] is what the
// gate can do. When they differ, [Gate.Describe] prints both and says the request could
// not be met. A gate that quietly runs slower than its printed rate makes the cost
// projection optimistic and the dwell arithmetic optimistic at the same time, and both
// of those are already at their margins.
type Gate struct {
	// rate is the REQUESTED novelty rate, in passes per second. Zero means every
	// frame.
	rate int
	// decodeFPS is the source's decoded frame rate, used only to clamp. Zero means the
	// rate is unknown and nothing is clamped.
	decodeFPS int
	// intervalNS is the clamped minimum interval between passes. A gate that compared
	// integers on every decoded frame would be doing the same subtraction 25 times a
	// second to answer a question whose answer changes once per interval.
	intervalNS int64
	// lastNS is when the pass last ran, in nanoseconds on a monotonic clock.
	lastNS int64
	// started separates "has never run" from "ran at time zero". Without it the first
	// frame after a restart inherits the previous run's clock and the gate can sit
	// closed for an interval after a restart, which is exactly when a supervisor is
	// watching for the pipeline to come back.
	started bool
	// lastSeq is the frame number of the last pass, kept because a caller has one and
	// a frame count reads better in a report than a timestamp.
	lastSeq uint64
	// ran counts the passes this gate has let through and skipped the frames it did
	// not. Both are counted because a gate that silently drops is a gate that lies, and
	// this one sits on the path that decides whether the smallest objects are ever
	// measured at all.
	ran, skipped int64
}

// NewGate returns a gate that runs the novelty pass at `rate` passes per second over a
// source decoding at `decodeFPS` frames per second.
//
// A zero rate means every decoded frame. A zero `decodeFPS` means the frame rate is not
// known, and nothing is quantised: the gate then runs at its interval exactly, and
// [Gate.Clamped] is false because there is nothing to compare against. That is the
// honest behaviour for a caller that cannot state the source rate, and the report line
// says the frame rate is unknown rather than printing a quantised figure.
//
// A negative rate is refused by [NoveltyRateCoversDwell] before it reaches here; this
// clamps it rather than dividing by it, because a gate cannot print a message.
func NewGate(rate, decodeFPS int) *Gate {
	if rate < 0 {
		rate = 0
	}
	g := &Gate{rate: rate, decodeFPS: decodeFPS}
	if rate > 0 {
		g.intervalNS = int64(1e9) / int64(rate)
		if decodeFPS > 0 && int64(decodeFPS) > 0 {
			frameNS := int64(1e9) / int64(decodeFPS)
			if frameNS <= 0 {
				frameNS = 1
			}
			// Quantise DOWN to a whole number of frames, so the gate NEVER runs
			// slower than it was asked to.
			//
			// The direction of the rounding is the decision, and it is not
			// symmetric. Rounding the interval UP gives the nearest rate from below: at
			// 25 fps a 10/s request would land on 8.4/s, a 16% shortfall that nobody
			// would see. Rounding it DOWN lands on 12.5/s instead, which costs CPU and
			// proposes MORE regions than asked for.
			//
			// Over-shooting is the safe direction here for a specific reason: novelty
			// is pure Go and its output is a proposal, so a faster rate costs
			// arithmetic and finds more. Under-shooting silently narrows the set of
			// regions that get cropped, and the smallest object the system exists to
			// report is exactly a region the gate did not propose. One of those two
			// errors shows up on a cost projection and the other never shows up at all.
			if frames := g.intervalNS / frameNS; frames >= 1 {
				g.intervalNS = frames * frameNS
			} else {
				g.intervalNS = frameNS
			}
		}
	}
	return g
}

// Due reports whether the novelty pass should run on decoded frame `seq`, at `atNS`
// nanoseconds on a monotonic clock.
//
// It is a TIME gate and only a time gate. An earlier revision also disjoined on the
// frame NUMBER, to rescue a source whose timestamps were monotonic but sparse, and that
// term made the gate fire roughly every other frame at any rate below the frame rate: at
// 25 fps with a 5/s gate the frame number had advanced by five between passes, so the
// frame term was always satisfied and the declared rate was never applied. A second term
// that can be independently satisfied does not narrow the gate, it opens it, and the way
// it failed was silently: every number the gate reported was wrong and nothing said so.
func (g *Gate) Due(seq uint64, atNS int64) bool {
	// A NIL gate means no gate is configured, which reads as every frame. It returns
	// true and records nothing, because there is no gate whose counters could record
	// it: an earlier revision fell through to `mark` here and dereferenced the nil
	// pointer, which is what a "nil means every frame" reading costs when it is
	// written as a branch condition rather than as a decision.
	if g == nil {
		return true
	}
	if g.intervalNS == 0 {
		g.mark(seq, atNS)
		return true
	}
	if g.started && atNS-g.lastNS < g.intervalNS {
		g.skipped++
		return false
	}
	g.mark(seq, atNS)
	return true
}

func (g *Gate) mark(seq uint64, atNS int64) {
	g.ran++
	g.started = true
	g.lastNS, g.lastSeq = atNS, seq
}

// Rate is the REQUESTED novelty rate, in passes per second, or zero for every frame.
func (g *Gate) Rate() int {
	if g == nil {
		return 0
	}
	return g.rate
}

// EffectiveRate is the fastest rate the gate can actually sustain over its source.
//
// It is a separate accessor because it is a DIFFERENT number from [Gate.Rate] whenever
// the request is above half the decode rate, and printing one where the reader expects
// the other is how a 16% shortfall becomes an invisible one.
func (g *Gate) EffectiveRate() float64 {
	if g == nil || g.rate == 0 {
		if g == nil {
			return 0
		}
		return float64(g.decodeFPS)
	}
	return 1e9 / float64(g.intervalNS)
}

// Clamped reports whether the gate's achieved rate differs from the requested one.
//
// It is a BOOL and not a comparison the caller makes, because "effective is not
// requested" is false for a rate of 0 and for a source whose rate is unknown, and both
// are states the caller would otherwise have to get right.
//
// It is deliberately true in BOTH directions. The gate rounds its interval down to a
// whole number of frames so it never runs slower than asked, so the commonest clamped
// case is a rate that came out HIGHER, and a check written as "is the effective rate
// lower" would report every one of those as met.
func (g *Gate) Clamped() bool {
	if g == nil || g.rate == 0 {
		return false
	}
	// A quarter of a pass per second of slack, which is below any quantisation this
	// produces at a decode rate anyone runs.
	return diffAbs(g.EffectiveRate(), float64(g.rate)) > 0.25
}

func diffAbs(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// Counts is what the gate has done, in passes run and frames skipped.
func (g *Gate) Counts() (ran, skipped int64) {
	if g == nil {
		return 0, 0
	}
	return g.ran, g.skipped
}

// Reset clears the gate's clock and its counters, so a calibration run does not leave
// its own history in the counters a soak then reports.
func (g *Gate) Reset() {
	if g == nil {
		return
	}
	g.lastNS, g.lastSeq, g.started, g.ran, g.skipped = 0, 0, false, 0, 0
}

// NoveltyRateCoversDwell is the cross-check between the rate and the thing the rate is
// for.
//
// `rate` is passes per second, `dwellSeconds` the shortest on-screen time the system
// claims to report, and `needSamples` how many proposals a region must get before it is
// reliably cropped. A rate that cannot deliver `needSamples` in `dwellSeconds` is
// refused BY NAME, with the consequence and the remedy, in the same shape as every other
// refusal in this module.
//
// Zero is accepted and means EVERY DECODED FRAME. It is not a special case to be
// worried about: it is the fastest the gate can go, and it is the honest answer when the
// caller has not decided yet.
//
// The error names SP_POC_NOVELTY_FPS because that is the variable an operator changes,
// and a refusal that does not name the thing to change is a complaint rather than a
// fix. It also names the sample shortfall, because "raise the rate" is advice and "you
// are one sample short of a bird" is a number.
func NoveltyRateCoversDwell(rate int, dwellSeconds float64, needSamples int) (int, error) {
	if dwellSeconds <= 0 {
		return 0, fmt.Errorf("novelty: a dwell time of %v seconds is not a dwell time", dwellSeconds)
	}
	if needSamples < 1 {
		return 0, fmt.Errorf("novelty: %d required samples is not a number of samples", needSamples)
	}
	if rate < 0 {
		return 0, fmt.Errorf("novelty: a novelty rate of %d passes per second is not a rate", rate)
	}
	if rate == 0 {
		// Every decoded frame. The sample count depends on the source's frame rate and
		// is bounded below by one, so this cannot fail and is reported as such.
		return needSamples, nil
	}
	got := int(float64(rate) * dwellSeconds)
	if got < needSamples {
		needed := float64(needSamples) / dwellSeconds
		return got, fmt.Errorf(
			"novelty: at %d pass(es) per second a region on screen for %.1f s is proposed %d time(s), "+
				"and %d are needed before it is reliably cropped. A region that is proposed once or twice "+
				"is a region whose detection depends on where in its dwell the gate happened to sample, "+
				"and the smallest object this system exists to report is exactly that region. Raise "+
				"SP_POC_NOVELTY_FPS to at least %d, or set it to 0 to run the pass on every decoded frame "+
				"and pay the cost in core-seconds",
			rate, dwellSeconds, got, needSamples, int(needed)+1)
	}
	return got, nil
}

// SmallestDwellSeconds is the on-screen time the rate cross-check is written against.
//
// It is the plan's own figure for a bird at 30 m, and it is stated as a constant rather
// than passed in at each call site so that changing the estimate is one edit and one
// red-check, and so that two call sites cannot quietly assume different animals.
const SmallestDwellSeconds = 1.5

// ProposalsNeededForACrop is how many proposals a region needs before it is reliably
// cropped.
//
// Three, for the same reason SP_POC_TRACK_MIN_FRAMES is three for a whole-frame track:
// one is what a shadow looks like and two is what a reflection looks like.
const ProposalsNeededForACrop = 3

// ReasonShortDwell is the reason string a refusal at rate-check time carries into the
// report, so a run that refused to start says WHY in the same vocabulary as everything
// else it reports.
const ReasonShortDwell = "novelty rate too low for the smallest dwell"

// NoveltyRateRefusal is the message the configuration layer prints, so the two refusals
// that can name this variable are worded identically. Two wordings for one fact is the
// way an operator concludes there are two problems.
func NoveltyRateRefusal(rate int, dwellSeconds float64, needSamples int) string {
	_, err := NoveltyRateCoversDwell(rate, dwellSeconds, needSamples)
	if err == nil {
		return ""
	}
	return err.Error()
}

// DescribeGate is the one line the report and the footer both print, so the rate on
// screen is the rate the run used.
//
// When the gate could not meet the request it says so AND prints the rate it achieved.
// A line that prints the requested rate next to counters produced by another rate is
// the shape of claim this repository exists to refuse.
func DescribeGate(g *Gate, decodeFPS int) string {
	if g == nil {
		return fmt.Sprintf("novelty: every decoded frame (source rate unknown; %d fps declared)", decodeFPS)
	}
	ran, skipped := g.Counts()
	total := ran + skipped
	skippedPct := 0.0
	if total > 0 {
		skippedPct = 100 * float64(skipped) / float64(total)
	}
	base := fmt.Sprintf("novelty: %d run, %d skipped (%.1f%%) over a %d fps source",
		ran, skipped, skippedPct, decodeFPS)
	if g.Rate() == 0 {
		return base + ", every frame"
	}
	base += fmt.Sprintf(", asked for %d pass(es)/s", g.Rate())
	if g.Clamped() {
		// The honest form names the gap, because a reader who only sees the request
		// has been told a number the run did not use.
		return base + fmt.Sprintf(", ACHIEVED %.1f/s: a gate can only fire on a frame boundary, so a "+
			"rate above %.0f/s over %d fps cannot be met", g.EffectiveRate(),
			float64(decodeFPS)/2, decodeFPS)
	}
	return base + fmt.Sprintf(", met exactly")
}
