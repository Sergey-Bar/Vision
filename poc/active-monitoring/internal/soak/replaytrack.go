package soak

// Track replay: recomputing a crossing from stored per-frame boxes.
//
// # Why this file exists
//
// A crossing box used to be recomputable from the `decisions` table alone, because
// it was the box the model reported on the frame the line test fired. It is not any
// more: it is the track's FILTERED box, and a Kalman-filtered position is a
// function of the frames AROUND it. A row holding one filtered box is therefore a
// claim with nothing behind it.
//
// That is the cost the plan names, and this file is the payment.
//
// # What it does
//
// It reads `box_observations` — the per-frame boxes the run recorded — and feeds
// them through a REAL [detect.Engine] built from the run's own zone and thresholds.
//
// That is the important design decision. The obvious implementation re-derives the
// filtered centre itself, and it would be a WEAKER claim: it re-implements the
// arithmetic this file is supposed to be checking, so it agrees with the engine
// whenever the two implementations are consistently wrong and disagrees when only
// one of them is. Re-running the actual engine means the replay is a check on the
// stored facts rather than a second opinion about the arithmetic.
//
// # What it cannot do, and says so
//
// It cannot detect an editing of BOTH the boxes and the decision together. Nothing
// can, without the model. What it CAN detect is a hand-edited `box_observations`
// row, a threshold changed after the run, a decision written by different code from
// the code now in the repository, and a crossing that the stored track does not
// actually support.
//
// `TrackReplayReport.Frames` says how many frames were available, so a reader knows
// how much of the track was replayed rather than assuming all of it.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/detect"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// TrackReplayInput is everything the replay needs, and every one of it comes from
// the run rather than from this package's defaults.
type TrackReplayInput struct {
	Store *store.Store
	// DetectionID names the decision being replayed. It is an ID rather than a frame
	// number because the operator has an ID from the report, and a frame number is
	// ambiguous the moment two objects cross on one frame.
	DetectionID string
	// Zone is the run's own SP_POC_ZONES contents. It is a parameter and not a
	// default because the default would be a line nobody ran against, and a replay
	// that crosses a line the run did not have proves nothing about the run.
	Zone detect.Zone
	// Config is the run's association settings: IoU, Tolerance, TrackMinFrames and
	// MaxAge. ScoreThreshold and Classes come from the stored decision instead,
	// because those two ARE stored beside the decision and using them makes the
	// replay a check on the row rather than on a config the replay supplied.
	Config detect.Config
	// Window is how many frames of history to replay. Bounded, because a replay that
	// silently read a whole run would be a different claim from one that read the
	// frames around the crossing.
	Window int
	// Tolerance is how far a replayed coordinate may sit from the stored one, in
	// thousandths of a frame.
	Tolerance int
}

// TrackReplayReport is the outcome.
type TrackReplayReport struct {
	// Available is whether the per-frame box log held anything to replay from. A
	// detector run whose log was sampled away, or whose database predates the table,
	// has nothing to replay from and this says so rather than passing.
	Available bool
	// Reason names what is missing when Available is false. It is never empty and
	// never a bare "no data": a reader who cannot replay needs to know which of the
	// four ways it failed.
	Reason string

	// Frames is how many per-frame rows the window covered, and Measured how many of
	// those the model actually measured. The two are different facts — a log that
	// holds 20 frames the pipeline never asked about is a replay over nothing.
	Frames   int
	Measured int
	// Skipped is how many frames the window's frames were NOT logged, which is what a
	// sampled log looks like from the inside.
	Skipped int

	// StoredBox and ReplayedBox are the two regions, in the thousandths every box in
	// this store uses.
	StoredBox   [4]int
	ReplayedBox [4]int
	// Delta is how far each coordinate moved, in thousandths. It is reported rather
	// than thresholded away: a reader who wants to know whether the replay was exact
	// or merely close gets the number.
	Delta [4]int
	// Exact, WithinTolerance and Passed are the three claims, in increasing
	// weakness, and all three are printed rather than only the one the verdict used.
	Exact           bool
	WithinTolerance bool
	Passed          bool

	// CrossingsReplayed is how many crossings the replay produced over the window. It
	// is compared against the ONE the run recorded, and the difference is the most
	// informative number here: a replay that finds the crossing at the wrong frame,
	// or twice, or not at all, is a different and much more serious failure than one
	// whose box is a few thousandths out.
	CrossingsReplayed int
	// MatchedFrame is how far the replay's crossing sat from the stored one, in
	// milliseconds, or -1 when there was none.
	MatchedFrame int64
	// StoredFrame is the frame the run recorded the crossing on.
	StoredFrame int64

	// Tolerance is the report's own copy of the tolerance the beat was judged on, so
	// the message beside a PASS names the number rather than the word.
	Tolerance int

	// ReplayClass is the class whose track was replayed, which is the decision's own
	// class unless the fallback was used — and the fallback is the one case a reader
	// needs to know about, so it is named here rather than inferred.
	ReplayClass string
}

// ReplayTrack recomputes a crossing from the stored per-frame boxes.
func ReplayTrack(ctx context.Context, in TrackReplayInput) TrackReplayReport {
	rep := TrackReplayReport{MatchedFrame: -1}
	if in.Store == nil {
		rep.Reason = "no store was supplied; the replay reads stored facts and there is nothing to " +
			"read from a nil"
		return rep
	}
	if in.DetectionID == "" {
		rep.Reason = "no detection was named; a replay of nothing proves nothing"
		return rep
	}
	if in.Window < 2 {
		in.Window = 32
	}
	if in.Tolerance <= 0 {
		in.Tolerance = 20
	}
	rep.Tolerance = in.Tolerance

	dec, err := in.Store.DecisionFor(ctx, in.DetectionID)
	if err != nil {
		rep.Reason = "the decision could not be read: " + err.Error()
		return rep
	}
	rep.StoredFrame = dec.FrameSeq
	rep.StoredBox = [4]int{dec.RegionTop, dec.RegionLeft, dec.RegionBottom, dec.RegionRight}

	rows, err := in.Store.BoxObservations(ctx, dec.RunID)
	if err != nil {
		rep.Reason = "the per-frame box log could not be read: " + err.Error()
		return rep
	}
	if len(rows) == 0 {
		rep.Reason = "this run has no per-frame box observations, so a Kalman-filtered crossing " +
			"cannot be recomputed from anything. Either the run predates the box log, or " +
			"SP_POC_BOX_LOG_FRAMES was 0."
		return rep
	}

	// The window: the crossing's frame and the history the filter needs. It ends AT
	// the crossing frame, because a replay that also fed the frames AFTER would be
	// checking the decision against information that did not exist when the decision
	// was made — which is how a replay gets to agree with an incorrect decision.
	from := dec.FrameSeq - int64(in.Window) + 1
	if from < 0 {
		from = 0
	}
	window := make([]store.BoxObservation, 0, in.Window)
	seenFrames := map[int64]bool{}
	measuredFrames := map[int64]bool{}
	for _, r := range rows {
		if r.FrameSeq < from || r.FrameSeq > dec.FrameSeq {
			continue
		}
		seenFrames[r.FrameSeq] = true
		if r.Measured {
			window = append(window, r)
			measuredFrames[r.FrameSeq] = true
		}
	}
	// Both counts are of FRAMES, not of rows. A frame carrying two boxes is still
	// one frame, and a report saying "9 measured over 8 frames" is a report whose
	// two numbers cannot be subtracted from each other — which is the arithmetic a
	// reader does first to work out how much was sampled.
	rep.Frames = len(seenFrames)
	rep.Measured = len(measuredFrames)
	rep.Skipped = rep.Frames - rep.Measured
	rep.Available = rep.Measured > 0

	if rep.Measured == 0 {
		rep.Reason = "the window before the crossing holds no MEASURED frames. A run whose log was " +
			"sampled at exactly this point cannot be replayed from per-frame boxes, and reporting " +
			"that as a pass would be the silent-zero defect this whole file exists to prevent"
		rep.Available = false
		return rep
	}

	// The replay ENGINE, built from the run's own settings. The score threshold and
	// the approved classes come from the stored decision rather than from `in`,
	// because those two ARE on the row and using them makes the beat a check on the
	// row instead of a check on a config this function invented.
	cfg := in.Config
	cfg.ScoreThreshold = dec.Threshold
	if len(cfg.Classes) == 0 {
		cfg.Classes = []string{dec.TriggerClass}
	}
	if cfg.IoU <= 0 {
		cfg.IoU = 0.3
	}
	if cfg.TrackMinFrames < 1 {
		cfg.TrackMinFrames = 1
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 2 // seconds; the stored boxes carry their own timestamps
	}
	// The filter runs, because the stored crossing was filtered and a replay without
	// it would compare an unfiltered box against a filtered one and always disagree.
	cfg.KalmanDisabled = false

	eng, err := detect.New(in.Zone, cfg)
	if err != nil {
		rep.Reason = "the replay could not build an engine from the run's own zone and thresholds: " +
			err.Error()
		return rep
	}

	// The stored boxes, grouped into tracks by class and fed in frame order.
	//
	// Grouping by class is not a simplification: a decision is about ONE class, and
	// replaying every class through the engine at once would attribute a crossing to
	// whichever object happened to produce one.
	byClass := map[string][]store.BoxObservation{}
	for _, r := range window {
		if r.Class == "" {
			continue
		}
		byClass[r.Class] = append(byClass[r.Class], r)
	}
	classes := make([]string, 0, len(byClass))
	for c := range byClass {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	// Only the decision's own class is replayed, and that is stated rather than
	// assumed: replaying a scene with several objects and reporting the best match
	// would let a wrong crossing be "confirmed" by a different object's.
	target := dec.TriggerClass
	seq, found := byClass[target]
	if !found || len(seq) == 0 {
		// Fall back to the class the region's own family implies, and say so.
		if alt, ok := firstClassIn(byClass, dec.TriggerClass); ok {
			seq = byClass[alt]
			target = alt
		} else {
			rep.Reason = "the window holds no boxes of the class this decision names (" +
				dec.TriggerClass + "), so there is no track to re-filter"
			return rep
		}
	}
	rep.ReplayClass = target

	sort.Slice(seq, func(i, j int) bool { return seq[i].FrameSeq < seq[j].FrameSeq })

	var crossings []detect.Crossing
	for _, r := range seq {
		b := detect.Box{
			Class: r.Class, Score: r.Score,
			X1: float64(r.Left) / 1000, Y1: float64(r.Top) / 1000,
			X2: float64(r.Right) / 1000, Y2: float64(r.Bottom) / 1000,
		}
		cx, _, _ := eng.FeedAnswered(r.At, detect.Answer{
			AIState: "asked_answered", Boxes: []detect.Box{b},
		})
		crossings = append(crossings, cx...)
	}
	rep.CrossingsReplayed = len(crossings)

	if len(crossings) == 0 {
		rep.Reason = fmt.Sprintf("the replayed track of %d %s boxes over %d frames produced NO "+
			"crossing, so the decision recorded on frame %d cannot be recomputed from the boxes this "+
			"run stored. Either the box log was edited, or the crossing came from a track the log "+
			"does not hold.",
			len(seq), target, rep.Frames, dec.FrameSeq)
		return rep
	}

	// The crossing nearest the stored frame. Comparing against the nearest rather
	// than the first means a replay that produces the SAME crossing twice — once on
	// the right frame and once from a re-acquired track — is reported as two, which is
	// a real difference and not one to be smoothed over by picking the lucky one.
	ref := seqAt(seq, dec.FrameSeq)
	var best detect.Crossing
	bestDist := time.Duration(-1)
	for _, c := range crossings {
		d := c.At.Sub(ref)
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	if !ref.IsZero() {
		rep.MatchedFrame = best.At.Sub(ref).Milliseconds()
	}

	// The stored box scale is produced by TRUNCATION at both write sites — the box
	// log's `int(b.Y1 * 1000)` and the decision's — so the replay truncates too.
	//
	// This is a one-thousandth of a frame and it is worth being pedantic about: a
	// replay that ROUNDED where the writer truncated would report a delta of 1 on a
	// perfect run forever, and a beat that is always off by one is a beat nobody
	// reads. Two conversions of the same number must be the same conversion.
	rep.ReplayedBox = [4]int{
		int(best.Box.Y1 * 1000), int(best.Box.X1 * 1000),
		int(best.Box.Y2 * 1000), int(best.Box.X2 * 1000),
	}
	for i := 0; i < 4; i++ {
		d := rep.StoredBox[i] - rep.ReplayedBox[i]
		if d < 0 {
			d = -d
		}
		rep.Delta[i] = d
	}
	rep.Exact = rep.Delta == [4]int{}
	rep.WithinTolerance = rep.Delta[0] <= in.Tolerance && rep.Delta[1] <= in.Tolerance &&
		rep.Delta[2] <= in.Tolerance && rep.Delta[3] <= in.Tolerance
	// PASS requires the box to reproduce AND the crossing to be singular. A replay
	// that finds the crossing twice has found something the run did not claim, and
	// that is a failure whatever the box says.
	rep.Passed = rep.WithinTolerance && rep.CrossingsReplayed == 1
	return rep
}

// seqAt returns the stored timestamp for a frame, or the last one in the window.
//
// It is a linear scan over a window of at most a few dozen rows rather than a map,
// because building a map to look up one key once is more code than the scan and the
// window is bounded by the caller.
func seqAt(seq []store.BoxObservation, frameSeq int64) time.Time {
	for _, r := range seq {
		if r.FrameSeq == frameSeq {
			return r.At
		}
	}
	if len(seq) > 0 {
		return seq[len(seq)-1].At
	}
	return time.Time{}
}

// firstClassIn returns the first class in the map that is not `avoid`, sorted, so
// the fallback is deterministic.
//
// A map iteration is not, and a replay that fell back to a different class on one
// run and another on the next would be a flaky check rather than a failing one.
func firstClassIn(byClass map[string][]store.BoxObservation, avoid string) (string, bool) {
	names := make([]string, 0, len(byClass))
	for c := range byClass {
		if c != avoid {
			names = append(names, c)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	for _, c := range names {
		if len(byClass[c]) > 0 {
			return c, true
		}
	}
	return "", false
}

// FormatTrackReplay renders the report, and every acceptance criterion here is
// PASS, FAIL or NOT RUN with the number that decided it.
//
// "NOT RUN" always names what is missing, because a replay that could not run is
// not a replay that passed and must never be reported as though it had.
func FormatTrackReplay(rep TrackReplayReport, detectionID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "track replay: %s\n", detectionID)
	fmt.Fprintf(&b, "  stored frame   %d\n", rep.StoredFrame)
	if !rep.Available {
		fmt.Fprintf(&b, "  NOT RUN       %s\n", rep.Reason)
		return b.String()
	}
	fmt.Fprintf(&b, "  window        %d frames, %d measured, %d skipped\n",
		rep.Frames, rep.Measured, rep.Skipped)
	fmt.Fprintf(&b, "  class         %s\n", rep.ReplayClass)
	fmt.Fprintf(&b, "  crossings     replayed %d, run recorded 1\n", rep.CrossingsReplayed)
	// The two boxes are printed only when there IS a replayed one. A replay that
	// found nothing would otherwise print `[0 0 0 0]` beside the stored box and a
	// reader would work out a delta of zero from it — a delta that says the boxes
	// agree when in fact there was nothing to compare.
	if rep.CrossingsReplayed > 0 {
		fmt.Fprintf(&b, "  stored box    %v\n", rep.StoredBox)
		fmt.Fprintf(&b, "  replayed box  %v\n", rep.ReplayedBox)
		fmt.Fprintf(&b, "  delta         %v thousandths (tolerance %d)\n", rep.Delta, rep.Tolerance)
	} else {
		fmt.Fprintf(&b, "  stored box    %v\n", rep.StoredBox)
		fmt.Fprintf(&b, "  replayed box  NONE — the replay produced no crossing to compare\n")
	}
	switch {
	case !rep.Available:
		fmt.Fprintf(&b, "  NOT RUN       %s\n", rep.Reason)
	case rep.Passed && rep.Exact:
		fmt.Fprintf(&b, "  PASS          the crossing recomputes from stored per-frame boxes, exactly\n")
	case rep.Passed:
		fmt.Fprintf(&b, "  PASS          the crossing recomputes from stored per-frame boxes, within %d thousandths\n",
			rep.Tolerance)
	case rep.CrossingsReplayed == 0 && rep.Reason != "":
		// The Reason is the sentence that says the decision does not follow from the
		// boxes. The generic failure text would say "produced no crossing", which is
		// the symptom; this says the conclusion, which is the finding.
		fmt.Fprintf(&b, "  FAIL          %s\n", rep.Reason)
	default:
		fmt.Fprintf(&b, "  FAIL          %s\n", replayFailure(rep))
	}
	return b.String()
}

// ToleranceReported is the tolerance the beat passed on, for the message.
func (r TrackReplayReport) ToleranceReported() int { return r.Tolerance }

// replayFailure says WHY in a sentence a reader can act on.
func replayFailure(rep TrackReplayReport) string {
	switch {
	case rep.CrossingsReplayed == 0:
		return "the stored track produced no crossing at all"
	case rep.CrossingsReplayed > 1:
		return fmt.Sprintf("the stored track produced %d crossings where the run recorded one, so the "+
			"box log and the decision disagree about how many events happened", rep.CrossingsReplayed)
	default:
		return fmt.Sprintf("the replayed box is out by %v thousandths against a tolerance of %d",
			rep.Delta, rep.Tolerance)
	}
}
