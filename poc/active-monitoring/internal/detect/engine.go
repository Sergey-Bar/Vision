package detect

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
	"github.com/Sergey-Bar/ai-captain-poc/internal/classify"
)

// Crossing is one decision: a tracked object went from one side of a line to
// the other, once, and that is the event. Exactly one Crossing is produced per
// pass, per track, per line, no matter how many frames the pass takes.
type Crossing struct {
	TrackID   int
	Class     string
	Line      string
	Direction string
	At        time.Time
	Box       Box
	// Frames is how many frames the track existed before the crossing. It is in
	// the event because "a box that appeared on the far side of the line" and
	// "a tracked object crossed" are different facts, and only the second is an
	// event anyone wants.
	Frames int
	// Score is the model's confidence in the box the crossing was decided on.
	// See Confidence.
	Score float64
	// Cut records that this decision's box was found by looking at a MAGNIFIED CROP of
	// the region rather than at the whole frame.
	//
	// It is a bool and not the region-source string because this package owns no
	// vocabulary of sources. `novelty` does. The only two answers the engine can give
	// are "the crop pass produced this" and "the whole-frame pass did", `soak` carries
	// them as `novelty.RegionCut` and `novelty.RegionMeasured`, and nobody else needs
	// the distinction or has a way to spell it.
	//
	// It travels on the Crossing rather than being looked up afterwards because a later
	// reader has to be able to tell two boxes apart from the stored row alone. Per
	// invariant 2, a fact that is displayed but not stored is a fact that gets
	// re-derived, and a re-derived region source is a re-inference.
	Cut bool
}

// Family is the crossing's class family, resolved from the taxonomy rather than
// carried on the Crossing.
//
// It is an accessor and not a field because the class is the only thing the
// engine actually knows: storing a second, derived string beside the one it was
// derived from is a second thing to keep in sync, and the failure mode of that
// is a row saying `vehicle` beside a class of `truck` with no way to tell which
// of the two the report should have read.
func (c Crossing) Family() classify.Family { return classify.FamilyOf(c.Class) }

// Score is the model's confidence in the box this crossing was decided on.
//
// It is copied from the box at the instant of the crossing and not looked up
// afterwards, because the box's score is what the MODEL said and a crossing's
// confidence that is recomputed from a later box would be a different number
// wearing the same name.
//
// It exists because a pushed event with `confidence: 0` is an event that tells
// an operator the detector had no opinion, which is not what happened: the
// detector had an opinion strong enough to put a box on the picture, and the
// `scoreOf` helper returned a hardcoded zero because the crossing did not carry
// the number. A field that is always zero is not a conservative default, it is a
// missing measurement printed as a real one.
func (c Crossing) Confidence() float64 { return c.Score }

// Track is a live tracked object, for the screen's overlay and pulse rows.
type Track struct {
	ID    int
	Class string
	Box   Box
	Seen  int
	First time.Time
	Last  time.Time
	// Side is the current side of each line, and Latched records that a crossing
	// has already been emitted for the current pass so the same crossing is not
	// emitted on every frame it stays on the far side.
	Side    map[string]int
	Latched map[string]bool
	// allowed is whether this track has been seen at least
	// SP_POC_TRACK_MIN_FRAMES times. A track that has not is drawn as a ghost and
	// cannot emit a crossing, and the flag lives on the TRACK rather than being
	// recomputed from a box that has already moved on: a track is the thing that
	// remembers, so it is the thing that remembers this.
	allowed bool
	// aged is whether this track has EVER reached SP_POC_TRACK_MIN_FRAMES
	// sightings. Unlike `allowed` it is NEVER cleared.
	//
	// The two flags answer different questions and conflating them is why the
	// two-stage ghost association could not work at all:
	//
	//   - `allowed` is per FRAME Ã¢â‚¬â€ "can this track emit a crossing right now". It is
	//     cleared every frame and set only by an APPROVED box, because "an object we
	//     used to see" is not "an object we can see now".
	//   - `aged` is per TRACK Ã¢â‚¬â€ "has this object existed long enough to be real". It
	//     persists, because an object half-occluded by a pole is still the object we
	//     have been watching, and the frame it dipped in is not the frame it was
	//     invented.
	//
	// The ghost pass gates on `aged`. Gating it on `allowed` made it dead code: a
	// frame containing nothing but ghosts has no approved box, so nothing sets
	// `allowed`, so nothing can be resumed Ã¢â‚¬â€ which is precisely the frame the pass
	// exists for.
	aged bool
	// kalman is this track's position filter. It is EMBEDDED rather than a pointer so
	// copying a Track Ã¢â‚¬â€ which the overlay does once per frame per track Ã¢â‚¬â€ cannot
	// leave two objects sharing one filter, and a shared filter is a box that jumps
	// between two objects.
	kalman Kalman
	// lastKF is when the filter was last updated, for the dt the prediction needs.
	// Zero means never, which is how the first frame is told from the second.
	lastKF time.Time
	// resumed counts how many frames this track was held alive by a ghost rather
	// than by an approved box.
	//
	// It is on the track rather than in the frame's Stats because it is a property of
	// the TRACK's history: a report that says "12 tracks resumed on the last frame"
	// is about one frame, and a report that says "track 7 survived 31 dips" is the
	// sentence that tells an operator the score threshold is right for this material.
	resumed int
	// Cut records that the box this track was last matched to came from a MAGNIFIED
	// CROP rather than from the whole-frame pass.
	//
	// It is set on every match rather than at creation, because it describes the most
	// recent observation: an object small enough to need the crop pass at one moment
	// and large enough for the whole-frame pass at the next is a real sequence, and
	// freezing the answer at creation would report the first.
	//
	// Three things read it and they all mean different things by it. The track-minimum
	// check holds a crop-sourced track to a LOWER bar, because a magnified sighting is
	// a structurally better observation and two of them are worth more than two
	// eight-pixel smudges. The presence latch counts one event per track regardless of
	// it. And the Crossing carries it, so the stored row can say how the box was found
	// rather than leaving a reader to guess.
	Cut bool
	// announced is whether this track has already emitted a presence event.
	//
	// It is the presence primitive's latch and it is per TRACK, not per class and not
	// per line. A line-crossing track can emit twice, once each way, because a line has
	// two sides and re-arms when the object returns. Presence has no second event to
	// wait for: "something was here and it is being tracked" is a single fact about a
	// single object, and a track that re-announced itself every frame while it stayed
	// on screen would turn one bird into a hundred events.
	//
	// It is never cleared, which is the same rule as `aged`: the track is the thing
	// that remembers.
	announced bool
}

// FilteredCentre returns this track's filtered centre and whether the filter has a
// state at all.
//
// A track with no filter state yet returns its raw centre, so a one-frame track is
// indistinguishable from an unfiltered one Ã¢â‚¬â€ which is the honest answer and what
// keeps the first frame of every track identical to what it was before the filter
// existed.
func (t *Track) FilteredCentre() (cx, cy float64, ok bool) {
	if c, d, has := t.kalman.Centre(); has {
		return c, d, true
	}
	cx, cy = t.Box.Centre()
	return cx, cy, false
}

// Resumed is how many frames this track was held alive by a refused box.
func (t *Track) Resumed() int { return t.resumed }

// family is the track's class family, resolved once so the screen's table does
// not recompute the taxonomy per row and cannot disagree with the report about
// what an object is.
func (t Track) family() string { return string(classify.FamilyOf(t.Class)) }

// Stats are the per-frame counters. They are counters of what the ENGINE saw, not
// of what happened in the world, and the four that matter across a whole run live
// in Counters Ã¢â‚¬â€ see split.go, which is where the run-level arithmetic is.
//
// `Rejected` is kept for compatibility with the per-frame print and is NOT the
// run's rejection count: the run's is split by reason, because a single rejected
// total is four different pieces of advice.
type Stats struct {
	Offered     int
	Approved    int
	Rejected    int
	Tracks      int
	TracksAlive int
	// Ghosts is how many refused boxes were still DRAWN, and GhostReasons breaks
	// them down. A refused box below SP_POC_GHOST_MIN_SCORE is counted in
	// Rejected and not here, because it was not shown.
	Ghosts       int
	GhostReasons map[string]int
	// DroppedTooNew is how many approved boxes were demoted because their track
	// had not been seen SP_POC_TRACK_MIN_FRAMES times. It is separate from
	// Rejected because these boxes were correct and merely young, and an
	// operator told "19 rejected" would look at a threshold when the answer is
	// "wait".
	DroppedTooNew int
	// Resumed is how many tracks were held alive this frame by a REFUSED box rather
	// than by an approved one Ã¢â‚¬â€ a score dip that did not split an object in two.
	//
	// It is here because it is the number that tells an operator whether their score
	// threshold suits the material. A scene full of occlusion with Resumed near zero
	// is a threshold set above what this detector actually produces for this footage,
	// and the fix is the threshold rather than the detector.
	Resumed int
	// GhostResumed is the same count by refusal reason, so a reader can see whether
	// the dips are score dips or something the two-stage pass cannot help with Ã¢â‚¬â€
	// an area refusal, say, which is a different problem with a different fix.
	GhostResumed map[string]int
}

// Config are the decision parameters. Every one of them is set from the
// environment by internal/config; none has a default that changes a verdict.
type Config struct {
	ScoreThreshold float64
	Classes        []string
	// Kalman is the position filter's tuning, and its zero value means
	// "[DefaultKalman]".
	//
	// It is in Config rather than a constructor argument because it is a property of
	// the ENGINE and there is one place a caller already declares every other engine
	// property. A separate `WithKalman` would be a second place, and a second place
	// is where a default ends up disagreeing with itself.
	Kalman KalmanParams
	// KalmanOn turns the position filter off, and its zero value is ON.
	//
	// It exists because the filter CHANGES the crossing box, so anybody reproducing
	// an older measurement needs the older behaviour available by name rather than by
	// reverting a commit and discovering which of several changes mattered.
	//
	// The zero value being ON is a Go wart rather than a choice Ã¢â‚¬â€ a bool that means
	// "enabled" cannot default to true Ã¢â‚¬â€ so the field is named for DISABLING and
	// read as "not disabled". A caller cannot switch the filter off by omitting the
	// field, and cannot switch it on by setting it, which is exactly the pair of
	// mistakes this arrangement prevents.
	KalmanDisabled bool
	// Approve is the decision "may this class produce a crossing event", handed in
	// rather than reconstructed from Classes.
	//
	// The engine used to build its own map from Classes, which put the answer in two
	// places that did not agree: `config.IsApproved` accepts a class whose FAMILY is
	// approved, and the engine's private map did not. The shipped trigger's class is
	// `motion` Ã¢â‚¬â€ not a COCO class, family `other` Ã¢â‚¬â€ so `watch` configured with
	// `SP_POC_CLASSES=other` approved it while the engine refused every candidate as
	// `class not on the approved list`. A predicate and not a second copy of the
	// list, because a list resolved in two places is a list that will be resolved in
	// two different ways.
	//
	// Nil keeps the old exact-name behaviour, so every existing caller and every
	// test in this package behaves exactly as it did.
	Approve  func(class string) bool
	IoU      float64
	MaxAge   time.Duration
	Cooldown time.Duration
	// Tolerance is the band around a line, in normalised perpendicular
	// distance, inside which a track's side does not change. Without it a track
	// sitting on the line would flip on floating-point noise and the crossing
	// count would not be reproducible.
	Tolerance float64
	// GhostScore is SP_POC_GHOST_MIN_SCORE: at or above it a refused box is still
	// DRAWN, dimmed. Below it nothing is drawn at all. Zero means "draw no
	// ghosts", which is a valid choice and not the same as an unset one.
	GhostScore float64
	// MinArea is SP_POC_MIN_BOX_AREA, a fraction of the frame. A box smaller than
	// this is a speck, a shadow edge or a letterbox band. A fraction rather than
	// a pixel count because the unit has to survive a change of resolution: a
	// threshold in pixels means something different on a 640-wide pipe and a
	// 1920-wide one, and the operator who set it would not know they had changed
	// it.
	MinArea float64
	// TrackMinFrames is SP_POC_TRACK_MIN_FRAMES. A track that has not been seen
	// this many times cannot emit a crossing, and its box is drawn as a ghost
	// with its own reason so the picture and the decision agree.
	//
	// The check is HERE and not only in the tracker because "how much evidence is
	// enough" is the same decision as the score threshold, and a decision made in
	// two places is a decision that will eventually disagree with itself.
	TrackMinFrames int
	// CutMinFrames is SP_POC_CROP_MIN_FRAMES: how many consecutive sightings a track
	// whose box came from a MAGNIFIED CROP needs before it may emit.
	//
	// It exists because TrackMinFrames is calibrated for the whole-frame pass and the
	// two measurements are not the same kind of evidence. A vehicle crossing a frame
	// at 30 km/h from 30 m is on screen for one to two seconds; at the detection
	// sampling rate that is a handful of samples, and demanding three is what
	// separates a crossing from a compression artefact. A bird at 30 m is on screen
	// for about the same second and a half, but after the whole-frame pass shrinks
	// 1920x1080 to 416x416 it is EIGHT pixels across, which is below the floor at which
	// any detector is reliable, so the only way it is ever seen at all is through a
	// crop. Through a crop it arrives as roughly a hundred pixels.
	//
	// Two observations of a hundred-pixel bird are worth more than two of an
	// eight-pixel smudge, and holding them to the same bar erases the smallest object
	// the system exists to report. So the minimum is SOURCE-AWARE: one constant for a
	// whole-frame track, a lower one for a crop-sourced track, and the noise the full
	// bar exists to stop is still stopped, because a shadow the whole-frame pass finds
	// is a shadow the whole-frame pass still has to see three times.
	//
	// It is validated as being at or below TrackMinFrames. A "lower" bar that is
	// higher is not a lower bar, and a configuration that accepted one would report a
	// source-aware minimum while applying a single stricter one.
	CutMinFrames int
	// MaxBoxesDrawn is SP_POC_MAX_BOXES_DRAWN, the cap on boxes on the picture.
	// It does not affect the tracker or the events: it is what the SCREEN draws,
	// and what it left off is counted per family and printed.
	MaxBoxesDrawn int
}

// Engine holds the tracker, the line-crossing state machines and the cooldowns.
// It is not safe for concurrent use: it is called from the single pipeline
// goroutine, next to the clock, and giving it a lock would only hide a second
// reader of time.
type Engine struct {
	zone Zone
	cfg  Config
	ok   map[string]bool
	// approve is the caller's answer to "may this class cross", when it supplied one.
	// See Config.Approve.
	approve func(class string) bool
	mu      sync.Mutex // guards nextID only, so tests can run in parallel
	next    int
	live    map[int]*Track
	// cooldown is per (line, class), not per track: a new track for the same
	// object after a brief occlusion should not get a second event inside the
	// window, and a per-track key would let it.
	cooldown map[string]time.Time
	// counters and tally accumulate over the run. They are here rather than in
	// the runner because they have to move on the SAME frame the split was made
	// on: a counter updated a frame later is a counter describing a different
	// picture than the one the report prints.
	counters Counters
	tally    Tally
	// suppressed gates presence while a view change settles, and suppressedEvents
	// counts what was withheld. Both are here rather than in the caller because the gate
	// has to be inside the latch's reach: a caller that checked it before calling Feed
	// would be right until somebody added a second call site.
	suppressed       bool
	suppressedEvents int
	// invalidated counts the tracks dropped by view changes, over the run.
	invalidated int
	// lastSplit is the previous frame's split, after the track-minimum-frames
	// check, for the screen to draw.
	lastSplit Split
	// kalmanP is the position filter's tuning, and it is a FIELD with a default
	// rather than a constant so a test and a caller can supply their own without
	// the engine having to know why.
	kalmanP KalmanParams
	// kalmanOn is whether the filter runs at all. It defaults ON and there is a
	// switch because the filter changes the crossing box, so a caller reproducing an
	// older measurement needs the older behaviour available by name rather than by
	// reverting a commit.
	kalmanOn bool
}

// kalmanOrDefault resolves a zero Config into the declared tuning.
//
// A struct's zero value is not the same as "the operator chose nothing", and
// resolving it here rather than at each use is what makes the resolution auditable:
// there is one function that decides, and a caller can see it.
func (c Config) kalmanOrDefault() KalmanParams {
	if c.Kalman.ProcessNoise > 0 && c.Kalman.MeasurementNoise > 0 {
		return c.Kalman
	}
	return DefaultKalman()
}

// kalmanParams returns the tuning in use, so the default is decided in one place.
func (e *Engine) kalmanParams() KalmanParams {
	if !e.kalmanOn {
		return KalmanParams{ProcessNoise: 1e12, MeasurementNoise: 1e12,
			InitialVelocityNoise: 1e12}
	}
	return e.kalmanP
}

// New builds an engine. It refuses an empty approved class list, because an
// engine that approves nothing produces a clean run that measured nothing.
func New(zone Zone, cfg Config) (*Engine, error) {
	if err := zone.Validate(); err != nil {
		return nil, fmt.Errorf("detect: %w", err)
	}
	if len(cfg.Classes) == 0 {
		return nil, fmt.Errorf("detect: the approved class list is empty, so no box can reach the line and a run would report zero events as if the model had found nothing")
	}
	if cfg.IoU <= 0 || cfg.IoU > 1 {
		return nil, fmt.Errorf("detect: the association threshold %v is not an IoU", cfg.IoU)
	}
	if cfg.Tolerance < 0 {
		return nil, fmt.Errorf("detect: the line tolerance %v is negative", cfg.Tolerance)
	}
	// The crop-sourced minimum is only meaningful when it is LOWER, so the pair is
	// resolved as a PAIR rather than two independent numbers.
	//
	// A whole-frame minimum of zero or one is the no-op, every sighting qualifies, and
	// a caller that means "no minimum" must get none from the crop path either.
	// Defaulting the crop bar to 1 in that case would make the better measurement
	// STRICTER than the whole-frame one, which is backwards, and would refuse a
	// configuration that asked for nothing in particular. So below one there is no
	// minimum to be source-aware about, neither validation below applies, and both bars
	// stay off.
	if cfg.TrackMinFrames <= 1 {
		cfg.CutMinFrames = cfg.TrackMinFrames
	} else {
		// A zero here means "a crop-sourced track needs no sightings at all", which is
		// the exact single-frame-noise failure TrackMinFrames exists to prevent,
		// reached through a setting nobody chose.
		if cfg.CutMinFrames == 0 {
			cfg.CutMinFrames = 1
		}
		if cfg.CutMinFrames < 1 {
			return nil, fmt.Errorf("detect: the crop-sourced minimum is %v sightings; at least one is required, because a track that has been seen zero times is not an object", cfg.CutMinFrames)
		}
		if cfg.CutMinFrames > cfg.TrackMinFrames {
			return nil, fmt.Errorf("detect: the crop-sourced minimum (%d) is above the whole-frame minimum (%d), so the source-aware minimum applies the STRICTER bar to the better measurement and there is no lower bar at all. A crop-sourced sighting is a magnified observation and needs no more of them than a whole-frame one, not more", cfg.CutMinFrames, cfg.TrackMinFrames)
		}
	}
	ok := make(map[string]bool, len(cfg.Classes))
	for _, c := range cfg.Classes {
		ok[strings.ToLower(strings.TrimSpace(c))] = true
	}
	approve := cfg.Approve
	if approve == nil {
		// A Config built by hand keeps the old behaviour, so the package's own tests
		// and any caller that never had a Config still approve by exact name.
		approve = func(class string) bool { return ok[strings.ToLower(strings.TrimSpace(class))] }
	}
	return &Engine{
		zone:     zone,
		cfg:      cfg,
		ok:       ok,
		approve:  approve,
		live:     map[int]*Track{},
		cooldown: map[string]time.Time{},
		counters: NewCounters(),
		// The filter is ON by default and tuned by [DefaultKalman]. It is on by
		// default because a jittery crossing box produces duplicate events for one
		// stationary object, which is a defect in the shipped path rather than a
		// setting somebody chose. The zero Config means "default tuning", and the
		// KalmanOn flag is read as "not explicitly disabled".
		kalmanP:  cfg.kalmanOrDefault(),
		kalmanOn: !cfg.KalmanDisabled,
		tally:    NewTally(),
	}, nil
}

// Answer is what the caller learned about ONE frame before the engine sees it.
//
// It exists because a box and a frame's ANSWER are different facts and the
// engine needs both. It used to be handed only the boxes, and set the state to
// `asked_answered` itself Ã¢â‚¬â€ which is a hardcoded `answered` one layer below the
// screen's hardcoded `answered`, and for the same reason: the engine had no
// state to print, so it printed a word nobody had claimed.
type Answer struct {
	// Boxes is what the model returned. Empty is a legitimate answer and the
	// state says whether it was a model that looked and found nothing or a
	// sidecar that was never reached.
	Boxes []Box
	// AIState is the five-word vocabulary state for this frame, from
	// `internal/aistate`. An empty or unknown state is recorded as ABSENT by the
	// counters, never as an answered frame with zero objects.
	AIState string
	// Candidates is the sidecar's pre-filter count for this frame. It is what
	// makes every other number on the screen arguable: a detection rate with no
	// candidate count beside it cannot be told apart from a decoder that threw
	// everything away.
	Candidates int
	// Cut is the SUBSET of Boxes that the detector found by looking at a magnified crop
	// of a measured region rather than at the whole frame.
	//
	// It is a parallel list and not a field on the box because `proposal.Box`'s JSON
	// tags are the sidecar's published wire format, pinned by
	// TestTheJSONTagsAreTheSidecarsWireFormat, and a seventh field would break every
	// sidecar response this build has ever parsed. The alternative, a second box type
	// carrying a source, is the wrapper struct that `proposal`'s own header says puts
	// a conversion at every call site, and a conversion on a coordinate is a place to
	// rescale one by accident.
	//
	// Membership is by value, which is exact rather than approximate: a crop-sourced
	// box and a whole-frame box on the same object differ in the fields the decoder
	// produced, so an equal value means the same measurement and nothing else. Boxes
	// absent from Cut were found by the whole-frame pass.
	Cut []Box
}

// CutSet is the membership index for Answer.Cut.
//
// It is returned rather than built inside the engine because two entry points need
// it, [Engine.FeedAnswered] and [Engine.SplitAt], and a per-frame set that lived on
// the Engine would be one more piece of mutable state that has to be set before the
// right call and cleared after it. Two orderings and one forgotten clear is a frame's
// boxes read against the previous frame's source, which produces a run of confident
// wrong answers with no error anywhere.
func (a Answer) CutSet() map[Box]bool {
	if len(a.Cut) == 0 {
		return nil
	}
	m := make(map[Box]bool, len(a.Cut))
	for _, b := range a.Cut {
		m[b] = true
	}
	return m
}

// FeedSplitAt runs the split and the track bookkeeping WITHOUT the line-cross
// machine, the cooldowns, or the counters, and returns the split.
//
// It exists because "would this box be approved, and how old is the track behind
// it" is a question the crossing machine does not answer and the tests need to
// ask it directly. Everything it touches is the same code path `Feed` uses for
// steps 1 to 3, so a test that passes here is a statement about the real
// pipeline and not about a parallel implementation.
func (e *Engine) FeedSplitAt(at time.Time, boxes []Box) Split {
	return e.SplitAt(at, Answer{Boxes: boxes, AIState: aistate.Asked})
}

// SplitAt is FeedSplitAt with the frame's real answer.
func (e *Engine) SplitAt(at time.Time, in Answer) Split {
	split := Apply(in.Boxes, e.thresholds(), e.zone)
	split.AIState = in.AIState
	split.Candidates = in.Candidates
	cut := in.CutSet()
	tracked, _ := e.associate(at, split.Approved, cut)
	kept, tooNew := MarkTooNew(tracked, e.seenAt, e.minFramesFor(cut))
	split.Approved = kept
	split.Ghosts = append(append([]Ghost{}, split.Ghosts...), tooNew...)
	e.lastSplit = split
	return split
}

// minFramesFor is the per-box minimum the track check uses.
//
// It reads the FRAME's cut set rather than the track's own flag, deliberately. The
// question is "how much evidence is behind THIS sighting", and a sighting is either a
// magnified crop or it is not. Reading the track's flag instead would make the minimum
// depend on how the track happened to be created: an object first seen small and later
// seen large would keep the low bar forever, which is the opposite of what "two smudges
// are not a bird" is for.
//
// A nil set means the whole-frame pass produced everything on this frame, and every
// box is then held to the full TrackMinFrames.
func (e *Engine) minFramesFor(cut map[Box]bool) func(Box) int {
	if len(cut) == 0 {
		return func(Box) int { return e.cfg.TrackMinFrames }
	}
	return func(b Box) int {
		if cut[b] {
			return e.cfg.CutMinFrames
		}
		return e.cfg.TrackMinFrames
	}
}

// trackMinFor is how many sightings a TRACK needs before it may emit.
//
// It is the track's own rather than the frame's, because `aged` and `allowed` are
// properties of the track's history: they are set from a match and read on a later
// frame, possibly one the crop pass did not contribute to. The one place this could
// drift is that decision, and it does not, because the flag it sets and the permission
// FeedAnswered grants are both read from the track here.
func (e *Engine) trackMinFor(t *Track) int {
	if t.Cut {
		return e.cfg.CutMinFrames
	}
	return e.cfg.TrackMinFrames
}

// associate is step 2: match approved boxes to live tracks, greedily by best IoU,
// and return the boxes that are attached to a track plus how many tracks exist.
//
// It is a method rather than inline code in Feed so the ORDER Ã¢â‚¬â€ split, associate,
// demote, cross Ã¢â‚¬â€ is visible as a list of five calls rather than as one block,
// and so a test can exercise association without the crossing machine.
//
// A greedy pass is the right size for this: the alternative is an assignment
// problem whose optimum nobody here can verify, and a slightly worse association
// costs nothing that the report claims to measure.
func (e *Engine) associate(at time.Time, approved []Box, cut map[Box]bool) ([]Box, int) {
	// Retire tracks that have not been seen for MaxAge, BEFORE associating.
	// Associating first would let a track that vanished for a minute be matched
	// to whatever appears now, and a re-appearing object after a long occlusion
	// is a new track, not a revival of the old one.
	for id, t := range e.live {
		if at.Sub(t.Last) > e.cfg.MaxAge {
			delete(e.live, id)
		}
	}
	// Every track starts the frame not allowed. A track that is matched to a box
	// becomes allowed once it has been seen often enough; a track that is NOT
	// matched must not keep the permission it had last frame, or an object that
	// left the frame a second ago would still be able to cross.
	for _, t := range e.live {
		t.allowed = false
	}

	type pair struct {
		track int
		box   int
		score float64
	}
	// A track matches a box when the boxes OVERLAP or TOUCH.
	//
	// Touching is the second half and it is not a loosened threshold. The novelty
	// engine's component is the changed AREA, which for an object moving D px per
	// frame is a D-px strip: frame N occupies [a, a+D] and frame N+1 occupies
	// [a+D, a+2D]. Those two boxes share an edge and no interior, so their IoU is
	// exactly zero, no threshold below 1.0 matches them, and every frame started a
	// new track. `seen` never reached `TrackMinFrames`, nothing was ever old enough
	// to cross, and `pocctl demo` reported 189 approved boxes and zero crossings
	// with no error.
	//
	// Touching is also the tightest rule that fixes it. It admits a box whose centre
	// is within half the two boxes' combined width and height Ã¢â‚¬â€ exactly the case
	// where two regions abut Ã¢â‚¬â€ and refuses everything with a gap between them. Two
	// objects that are near each other therefore do not merge: association below is
	// greedy and one-to-one, so each box joins its single nearest track and a
	// second box gets a second track. The cost of a looser gate here is identity
	// SWITCHING when two objects cross closely, which is the ordinary trade in every
	// tracker and is bounded by the gap this rule refuses.
	var pairs []pair
	for _, t := range e.live {
		for bi, b := range approved {
			if !strings.EqualFold(t.Class, b.Class) {
				continue
			}
			v := iou(t.Box, b)
			switch {
			case v >= e.cfg.IoU:
				// Scored above 1 so any overlap outranks any touch: where both
				// rules would match, the overlapping track is the better answer
				// and the ordering below has to say so.
				pairs = append(pairs, pair{track: t.ID, box: bi, score: 1 + v})
			case touching(t.Box, b):
				pairs = append(pairs, pair{track: t.ID, box: bi, score: v})
			}
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	matchedBoxes := map[int]int{}
	assigned := map[int]int{}
	for _, p := range pairs {
		if _, ok := assigned[p.track]; ok {
			continue
		}
		if _, ok := matchedBoxes[p.box]; ok {
			continue
		}
		matchedBoxes[p.box] = p.track
		assigned[p.track] = p.box
	}

	for id, bi := range assigned {
		t := e.live[id]
		t.Box = approved[bi]
		t.Cut = cut[approved[bi]]
		t.Seen++
		t.Last = at
		// The filter is updated HERE, on the measurement, and NOT from the ghost pass
		// and not from the crossing. That is the whole of the filter's contract: it
		// sees what the MODEL measured and nothing else.
		cx, cy := t.Box.Centre()
		dt := 0.0
		if !t.lastKF.IsZero() {
			dt = at.Sub(t.lastKF).Seconds()
		}
		t.kalman.Update(cx, cy, dt, e.kalmanParams())
		t.lastKF = at
		// `aged` is set here and NEVER cleared Ã¢â‚¬â€ see the field's comment. It is the
		// track's own answer to "have I existed long enough", and it is the flag the
		// ghost pass gates on.
		if t.Seen >= e.trackMinFor(t) {
			t.aged = true
		}
	}

	attached := make([]Box, 0, len(approved))
	for bi, b := range approved {
		if _, ok := matchedBoxes[bi]; ok {
			attached = append(attached, b)
			continue
		}
		e.next++
		e.live[e.next] = &Track{
			ID: e.next, Class: b.Class, Box: b, Seen: 1, First: at, Last: at,
			Side: map[string]int{}, Latched: map[string]bool{}, Cut: cut[b],
		}
		attached = append(attached, b)
	}
	return attached, len(e.live)
}

// touching reports whether two boxes abut: their centres are within half their
// combined width and height.
//
// The epsilon is one grid cell's worth at a 64-wide grid, because the novelty
// engine measures on exactly that grid and two abutting cells can be a rounding
// step apart. Without it a pair that is exactly touching fails on floating-point
// dust, and the failure is silent and total: no track, no crossing, no error.
func touching(a, b Box) bool {
	acx, acy := a.Centre()
	bcx, bcy := b.Centre()
	const eps = 1.0 / 64
	dx := absf(acx - bcx)
	dy := absf(acy - bcy)
	return dx <= (a.X2-a.X1+b.X2-b.X1)/2+eps && dy <= (a.Y2-a.Y1+b.Y2-b.Y1)/2+eps
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// Feed is one frame's worth of boxes at one instant. It returns the crossings
// decided by this frame, the live tracks after the update, and the counters.
//
// The instant is supplied by the caller because the caller owns the clock. This
// package never calls time.Now: a second clock is a second opinion about when
// something happened, and two opinions is how a clip ends up pointing at the
// wrong second.
//
// The ORDER below is load-bearing and it is the same order every time:
//
//  1. split by validity, class, score, area, zone  Ã¢â‚¬â€ what is a candidate at all
//  2. track the APPROVED boxes only                Ã¢â‚¬â€ the tracker never sees a ghost
//  3. demote tracks below SP_POC_TRACK_MIN_FRAMES Ã¢â‚¬â€ time is the only thing that
//     tells an object from a compression artefact
//  4. run the line-cross machine on what survived  Ã¢â‚¬â€ only those can be events
//  5. fold the run-level counters                  Ã¢â‚¬â€ on the SAME frame
//
// The previous order fed the tracker everything over the score threshold, so
// lowering the threshold to catch smaller vehicles also created tracks for lamp
// posts, and the screen's list and the event stream disagreed about what the
// system was looking at. The tracker seeing only approved boxes is the fix, and
// `Ghosts` existing at all is what makes it visible.
//
// `Feed` is the shorthand for "the model answered this frame and here are its
// boxes", and it is the reason twenty-odd call sites did not have to learn about
// `Answer`. `FeedAnswered` is the path the pipeline takes, because the pipeline
// knows the state and `Feed` would have to guess it.
func (e *Engine) Feed(at time.Time, boxes []Box) ([]Crossing, []Track, Stats) {
	return e.FeedAnswered(at, Answer{Boxes: boxes, AIState: aistate.Asked})
}

// FeedAnswered is Feed with the frame's real answer, including a state that is
// not `asked_answered`.
//
// This is the only way a frame with no answer can reach the tracker at all, and
// it has to exist: a `degraded` or `not_asked` frame arrives with an empty box
// list, and if the pipeline skipped the engine for it the counters would see only
// the frames that worked Ã¢â‚¬â€ which is exactly how a run with a dead sidecar ends up
// reporting a clean detection rate.
func (e *Engine) FeedAnswered(at time.Time, in Answer) ([]Crossing, []Track, Stats) {
	var st Stats
	st.Offered = len(in.Boxes)
	st.GhostReasons = map[string]int{}

	// 1. The split.
	split := Apply(in.Boxes, e.thresholds(), e.zone)
	split.AIState = in.AIState
	split.Candidates = in.Candidates
	// The ghosts the split drew, and the ones that were refused below the ghost
	// floor and are not shown at all.
	for _, g := range split.Ghosts {
		st.Ghosts++
		st.GhostReasons[g.Reason]++
	}
	st.Rejected = st.Offered - len(split.Approved)
	// A box refused below the ghost floor was neither drawn nor tracked, and it
	// is a real rejection: it is counted in Rejected and not in Ghosts.
	for _, b := range in.Boxes {
		if _, drawn := e.splitIndex(split, b); !drawn {
			st.Rejected++
		}
	}
	approved := split.Approved
	st.Approved = len(approved)

	// 2. The tracker sees APPROVED boxes only.
	cut := in.CutSet()
	minFrames := e.minFramesFor(cut)
	tracked, tracks := e.associate(at, approved, cut)
	st.Tracks = tracks
	st.TracksAlive = len(e.live)

	// 3. A track that has not been seen long enough cannot be an object yet.
	tracked, tooNew := MarkTooNew(tracked, e.seenAt, minFrames)
	st.DroppedTooNew = len(tooNew)
	for _, g := range tooNew {
		st.Ghosts++
		st.GhostReasons[g.Reason]++
	}
	// The permission to CROSS is set here, after the check, and it is set on the
	// TRACK. A track that reached the threshold keeps the permission for as long
	// as it is being seen; a track that is not seen this frame loses it, because
	// "an object we used to see" is not "an object we can see now".
	for _, b := range tracked {
		if e.seenAt(b) >= minFrames(b) {
			for _, t := range e.live {
				if strings.EqualFold(t.Class, b.Class) && iou(t.Box, b) > 0 {
					t.allowed = true
					break
				}
			}
		}
	}
	e.lastSplit = split
	e.lastSplit.Approved = tracked
	e.lastSplit.Ghosts = append(append([]Ghost{}, split.Ghosts...), tooNew...)

	// 3b. The SECOND association pass, over the GHOSTS.
	//
	// A detector's confidence is not constant across the frames of one object, and
	// a vehicle half-occluded by a pole scores below the approval bar while the same
	// vehicle unoccluded scores above it. Under a one-stage tracker that frame is
	// simply refused, the approved boxes on either side have no track to join, and
	// one object becomes two tracks each too young to cross Ã¢â‚¬â€ which reads on a
	// report as a detector that found nothing, and is the single most likely reason
	// a real detector with a real model produces zero crossings on real footage.
	//
	// It runs HERE, after step 3 and not immediately after the approved association,
	// and the position is load-bearing: it checks `t.allowed`, and that flag is set
	// by step 3. Placed before it, the pass read a flag the same frame had not yet
	// written and resumed nothing at all Ã¢â‚¬â€ which is the failure this ordering
	// comment exists to prevent, and it looked exactly like the feature not working.
	//
	// It is still after the approved association, so an approved box has already
	// claimed its track and a ghost cannot steal it. See [Engine.associateGhosts] for
	// the three bounds that stop a ghost doing any harm.
	resumed := e.associateGhosts(at, split.Ghosts)
	st.Resumed = len(resumed)
	st.GhostResumed = map[string]int{}
	for _, m := range resumed {
		st.GhostResumed[m.Reason]++
		if t, ok := e.live[m.TrackID]; ok {
			t.resumed++
		}
	}

	// 4. The line-cross machine, on what survived all three checks.
	//
	// `step` runs for EVERY live track, not only the ones old enough to emit.
	// That distinction is the whole of how the minimum-frames rule is correct:
	// `step` both OBSERVES and EMITS, and a track that was approaching the line
	// for three frames must be OBSERVED during those three frames. Gating the
	// whole call on `allowed` would mean the first side reading a track ever got
	// was taken after it aged in Ã¢â‚¬â€ so a vehicle that drove up to the line and
	// across it was recorded as having been on the far side all along, and its
	// crossing was never detected. That is a real detector, crossing a real
	// line, producing no event.
	//
	// So: observe always, and let `step` refuse to EMIT until the track is old
	// enough. The gate belongs at the emission point, not around the observer.
	var crossings []Crossing
	for _, t := range sortedTracks(e.live) {
		for _, c := range e.step(at, t) {
			if t.allowed {
				crossings = append(crossings, c)
			}
		}
	}
	for i, t := range e.live {
		if t.Seen == 0 {
			delete(e.live, i)
		}
	}

	// 5. The run-level counters, on this frame.
	e.counters.Observe(e.lastSplit)
	e.tally = e.tally.Add(e.lastSplit)

	alive := e.live
	out := make([]Track, 0, len(alive))
	for _, t := range sortedTracks(alive) {
		out = append(out, *t)
	}
	st.TracksAlive = len(alive)
	return crossings, out, st
}

// splitIndex is not used for identity Ã¢â‚¬â€ it exists only so `Feed` can count a box
// that was refused below the ghost floor. It is kept as a method rather than an
// inline loop so the "was this box drawn" question has one answer in the package.
func (e *Engine) splitIndex(s Split, b Box) (int, bool) {
	for i, g := range s.Ghosts {
		if g.Box == b {
			return i, true
		}
	}
	return 0, false
}

// seenAt is how many frames the engine's tracker has seen a box like this one.
//
// It matches on class and on a position close to the box's centre, rather than on
// identity, because the caller is asking about a BOX and not about a track. The
// radius is a third of the frame, which is generous on purpose: the question is
// "is there a track near here at all", and a tight radius would answer "no" for a
// box that has been on screen for ten frames and has drifted.
func (e *Engine) seenAt(b Box) int {
	const radius = 0.33
	cx, cy := b.Centre()
	best := 0
	for _, t := range e.live {
		if !strings.EqualFold(t.Class, b.Class) {
			continue
		}
		tcx, tcy := t.Box.Centre()
		dx, dy := tcx-cx, tcy-cy
		if dx*dx+dy*dy <= radius*radius && t.Seen > best {
			best = t.Seen
		}
	}
	return best
}

// InvalidateAll drops every live track and returns how many there were.
//
// It exists for the view-change case and the reason is arithmetic rather than
// architectural. After a camera moves, every track describes a position the camera is
// no longer looking at, and the association threshold would happily match the new view's
// boxes to the old view's tracks: a wall that was at x=0.1 is still at x=0.1 in the new
// view's noise, and IoU does not know the camera moved. So the tracks have to go, and
// the count comes back because "a view change invalidated 14 tracks" is the number an
// operator needs to understand what their event stream just did, and "a view change
// happened" alone does not say it.
//
// It does NOT clear the counters or the cooldown. Those describe the run, not the view.
func (e *Engine) InvalidateAll() int {
	n := len(e.live)
	e.live = make(map[int]*Track, len(e.live))
	e.next = 0
	e.invalidated += n
	return n
}

// LiveTrackCount is how many tracks the engine currently holds.
//
// It exists so a caller can report the invalidated count against something it measured
// rather than against a constant it expected, which is the difference between an
// assertion and a hope. It is a COUNT and not the `LiveTracks` list, which is for
// drawing and which allocates.
func (e *Engine) LiveTrackCount() int { return len(e.live) }

// SetSuppressed gates presence events until it is cleared.
//
// It gates PRESENCE and not crossings, because a line crossing is a claim about two
// positions and a view change destroys the frame's geometry rather than its content: a
// line-crossing deployment has named lines drawn on a fixed installation, and a camera
// that moved from its installation is a camera-integrity event, not fifty crossings.
func (e *Engine) SetSuppressed(v bool) { e.suppressed = v }

// Suppressed reports whether presence is gated.
func (e *Engine) Suppressed() bool { return e.suppressed }

// SuppressedEvents is how many presence events were withheld while gated.
//
// It is a COUNT and not a boolean because "nothing was emitted because nothing happened"
// and "nothing was emitted because the camera moved" are the same absence and only one
// of them is a finding. A gate with no counter is silence.
func (e *Engine) SuppressedEvents() int { return e.suppressedEvents }

// InvalidatedTracks is how many tracks a view change has dropped, over the run.
func (e *Engine) InvalidatedTracks() int { return e.invalidated }

// thresholds adapts the engine's Config to the split's Thresholds, so the split
// has one description of the decisions and the engine has one description of the
// association, and neither can drift from the other.
func (e *Engine) thresholds() Thresholds {
	return Thresholds{
		Score:          e.cfg.ScoreThreshold,
		GhostScore:     e.cfg.GhostScore,
		MinArea:        e.cfg.MinArea,
		TrackMinFrames: e.cfg.TrackMinFrames,
		Approved:       e.isApproved,
	}
}

func (e *Engine) isApproved(class string) bool {
	if e.approve != nil {
		return e.approve(class)
	}
	return e.ok[strings.ToLower(strings.TrimSpace(class))]
}

// Counters returns the run-level metric counters.
func (e *Engine) Counters() Counters { return e.counters }

// Tally returns the per-family roll-up.
func (e *Engine) Tally() Tally { return e.tally }

// LastSplit is the previous frame's approved/rejected split, for the screen. It
// is the split as it was AFTER the track-minimum-frames check, so the picture
// shows exactly what the engine decided and not what it was considering.
func (e *Engine) LastSplit() Split { return e.lastSplit }

// step runs the line-crossing state machine for one track against every line.
// CrossingBox is the box the line test is DECIDED on, and it is the track's
// FILTERED box.
//
// This is the decision the Kalman filter exists for, and it is deliberately made
// once, here, rather than inside [step].
//
// A single frame's box jitters by a few pixels, and a jittery box flips sides of a
// line it is sitting on: a car parked across a line emits a crossing, then a second
// one a frame later as the box breathes, and the report shows two events for one
// stationary vehicle. Deciding on the filtered centre removes that, because the
// filtered centre of an object that is not moving does not move.
//
// The size is NOT filtered Ã¢â‚¬â€ see [FilteredBox] Ã¢â‚¬â€ so the box that reaches the report
// and the evidence store has the width the MODEL reported. A reader comparing a
// crossing against the picture should find the same extent they can see.
//
// And this is why replay cannot read the decisions table: the box on a Crossing is
// now a function of the frames AROUND it. Replay re-runs the whole track from
// `box_observations`.
func (e *Engine) crossingBox(t *Track) Box {
	if fx, fy, ok := t.FilteredCentre(); ok {
		return FilteredBox(t.Box, fx, fy)
	}
	return t.Box
}

func (e *Engine) step(at time.Time, t *Track) []Crossing {
	// The presence machine, and the ONLY difference between it and the crossing one is
	// that it has no line to test. A zone with no lines is the primary configuration,
	// not a degenerate one: the question becomes "was something tracked here at all",
	// and the answer is one event per object rather than one event per side-change of a
	// drawn segment.
	if len(e.zone.Lines) == 0 {
		return e.presence(at, t)
	}
	// The centre the test is decided on is the track's FILTERED centre, while the
	// box on the event carries the same filtered centre with the model's own width.
	// See [Engine.crossingBox] for why the split is in that order.
	cx, cy := t.Box.Centre()
	if fx, fy, ok := t.FilteredCentre(); ok {
		cx, cy = fx, fy
	}
	var out []Crossing
	for _, l := range e.zone.Lines {
		if !e.lineApplies(l, t.Class) {
			continue
		}
		sign, dist := l.side(cx, cy)

		prev, had := t.Side[l.Name]
		// Inside the band: the side is unknown, and an unknown side is not a
		// crossing. Holding the previous side is what stops a track parked on
		// the line from generating a stream of events.
		if dist < e.cfg.Tolerance {
			if had {
				t.Side[l.Name] = prev
			}
			continue
		}
		if !had {
			t.Side[l.Name] = sign
			continue
		}
		if sign == prev {
			t.Side[l.Name] = sign
			continue
		}
		// The side changed. That is a crossing, unless this pass already emitted
		// one for this line, or the cooldown for this line and class has not
		// elapsed.
		if t.Latched[l.Name] {
			// Re-arm only once the object is back on the original side, so a
			// second real crossing in the same direction is still counted.
			t.Side[l.Name] = sign
			continue
		}
		if last, ok := e.cooldown[cooldownKey(l.Name, t.Class)]; ok && at.Sub(last) < e.cfg.Cooldown {
			t.Side[l.Name] = sign
			continue
		}
		t.Latched[l.Name] = true
		e.cooldown[cooldownKey(l.Name, t.Class)] = at
		out = append(out, Crossing{
			TrackID: t.ID, Class: t.Class, Line: l.Name, Direction: l.Direction,
			// The FILTERED box, not the measured one. This is the single line where
			// that decision is made, and it is the reason a Crossing's box can no
			// longer be recomputed from this row alone. See [Engine.crossingBox].
			At: at, Box: e.crossingBox(t), Frames: t.Seen, Score: t.Box.Score, Cut: t.Cut,
		})
		t.Side[l.Name] = sign
	}
	// Re-arm a latched line when the object returns to the far side of it.
	for _, l := range e.zone.Lines {
		if !t.Latched[l.Name] {
			continue
		}
		other := 0
		if v, ok := t.Side[l.Name]; ok {
			other = -v
		}
		sign, dist := l.side(cx, cy)
		if other != 0 && sign == other && dist >= e.cfg.Tolerance {
			t.Latched[l.Name] = false
		}
	}
	return out
}

func cooldownKey(line, class string) string { return line + "|" + class }

// presenceLine is the cooldown namespace presence events share.
//
// It is a NAME and not an empty string on purpose. The cooldown is keyed per
// (namespace, class) so a bird and a car do not suppress each other, and reusing ""
// would collide with any real line an operator happened to leave unnamed. Zone.Validate
// forbids that, so the collision cannot happen, but only by accident of a rule written
// for a different reason. A reserved namespace is not an accident.
const presenceLine = "\x00presence"

// presence emits one event for a track the first time it is old enough to be real.
//
// Four rules, and each of them is a rule the crossing machine also has:
//
//   - `allowed` gates it, so the track-minimum check decides what counts as an object.
//     That is the whole anti-jitter mechanism and it is reused rather than
//     re-implemented; a second copy would be a second opinion about how much evidence
//     is enough.
//   - `announced` latches it, so ONE TRACK is ONE EVENT. A crossing re-arms because a
//     line has two sides; presence has no second side, and a track that stayed on
//     screen for four seconds at two samples a second would otherwise be eight events.
//   - The per-class cooldown is reused unchanged, so a second bird inside the window is
//     suppressed the same way a second car inside it is.
//   - `Line` and `Direction` are EMPTY, and empty is the honest value. There is no line
//     and there is no direction, and inventing either would put a name on an event that
//     never crossed anything.
//
// The box is the track's FILTERED box, for the same reason the crossing's is: a single
// frame's box jitters, and an event whose box breathes is an event an operator has to
// check twice.
func (e *Engine) presence(at time.Time, t *Track) []Crossing {
	if !t.allowed || t.announced {
		return nil
	}
	// Suppression is checked here, at the ONE place a presence event is produced,
	// rather than at each caller.
	//
	// It is here because the gate has to be inside the latch's reach and outside the
	// caller's ability to forget. A caller that checked `Suppressed()` before calling
	// `Feed` would be correct until somebody added a second call site, and a view change
	// that arrived on a frame handled by that second call site would be gated for one
	// path and not the other.
	//
	// `announced` is NOT set while suppressed. A track that was suppressed and then
	// cleared emits when the view settles, which is correct: the object was really
	// there. Latching it would lose the event permanently.
	if e.suppressed {
		e.suppressedEvents++
		return nil
	}
	key := cooldownKey(presenceLine, t.Class)
	if last, ok := e.cooldown[key]; ok && at.Sub(last) < e.cfg.Cooldown {
		// Inside the window. The track is NOT latched, so the event is emitted as soon
		// as the window has elapsed and the track is still there, which is the same
		// shape the crossing machine uses when a cooldown suppresses a side change.
		return nil
	}
	t.announced = true
	e.cooldown[key] = at
	return []Crossing{{
		TrackID: t.ID, Class: t.Class,
		// No line, no direction, and both say so rather than being left empty for a
		// reader to guess at.
		At: at, Box: e.crossingBox(t), Frames: t.Seen, Score: t.Box.Score, Cut: t.Cut,
	}}
}

func (e *Engine) lineApplies(l Line, class string) bool {
	if len(l.Classes) == 0 {
		return true
	}
	class = strings.ToLower(class)
	for _, c := range l.Classes {
		if strings.ToLower(c) == class {
			return true
		}
	}
	return false
}

// inZone applies the polygon restrictions, if the zone has any.
func (e *Engine) inZone(b Box) bool {
	if len(e.zone.Polygons) == 0 {
		return true
	}
	cx, cy := b.Centre()
	for _, p := range e.zone.Polygons {
		if !p.contains(cx, cy) {
			continue
		}
		if len(p.Classes) == 0 {
			return true
		}
		for _, c := range p.Classes {
			if strings.EqualFold(c, b.Class) {
				return true
			}
		}
	}
	return false
}

func sortedTracks(m map[int]*Track) []*Track {
	out := make([]*Track, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Overlap is [Engine]'s association IoU, exported for the runner's box log.
//
// It is EXPORTED rather than duplicated because the runner attributes each frame's
// boxes to a track by matching them against the live tracks, and a second
// implementation of IoU is a second answer to 'which track is this'. The two
// disagreeing would put a box on one track in the table and another in the
// tracker's head, and the box log is what replay reads.
//
// Its name is Overlap rather than IoU because the package's own iou stays private
// and a renamed export would have been a second name for one thing.
func Overlap(a, b Box) float64 { return iou(a, b) }

func iou(a, b Box) float64 {
	ix1, iy1 := maxF(a.X1, b.X1), maxF(a.Y1, b.Y1)
	ix2, iy2 := minF(a.X2, b.X2), minF(a.Y2, b.Y2)
	iw, ih := ix2-ix1, iy2-iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	ua := a.Width()*a.Height() + b.Width()*b.Height() - inter
	if ua <= 0 {
		return 0
	}
	return inter / ua
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// LiveTracks returns the current tracks, for the screen.
func (e *Engine) LiveTracks() []Track {
	return e.snapshot()
}

func (e *Engine) snapshot() []Track {
	out := make([]Track, 0, len(e.live))
	for _, t := range sortedTracks(e.live) {
		out = append(out, *t)
	}
	return out
}
