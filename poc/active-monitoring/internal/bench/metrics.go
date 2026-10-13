// Package bench measures a detector.
//
// # The boundary, and it is the important part
//
// This file is PURE. It imports `internal/proposal` for the box type and nothing
// else from this module — no store, no pipeline, no detector, no database.
//
// That is not tidiness. The metrics here are the numbers a report prints, and a
// metric that reads a database is a metric that can be wrong in a way no arithmetic
// can show: a wrong denominator looks exactly like a right one. `metrics.go` — this
// file — is therefore the only half of the benchmark, and it is testable against a
// hand-computed fixture rather than against the pipeline. `evaluate.go` is the half
// that holds a session, and it is the only half allowed to.
//
// # The three numbers, and what each one is for
//
//   - PRECISION is what an operator experiences: of the boxes you drew, how many
//     were a thing that was really there.
//   - RECALL is what a security reviewer asks: of the things that were there, how
//     many did you find.
//   - FALSE POSITIVES PER HOUR is the only one that goes on a purchase order, and
//     it is the one nobody computes — because it needs a duration, and a duration
//     needs a clock the benchmark does not own.
//
// # The taxonomy penalty, stated up front
//
// A recall figure computed against ground truth whose classes this build cannot
// express is a figure about the TAXONOMY, not about the detector. Open Images has
// 600 classes; COCO has 80; a still of a `hair drier` is a real object that a COCO
// detector can never name, and counting it as a miss would report a recall
// penalty for a vocabulary limitation.
//
// So [Report.Dropped] records every dropped ground-truth box and every dropped
// image, and the caller is expected to print it beside the recall. A recall quoted
// without it is a claim about the ground truth, not about the model.
package bench

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/proposal"
)

// Box is one detection or one ground-truth object, in normalised 0..1 coordinates
// against the image.
//
// An alias of [proposal.Box], so a box produced by the detector needs no conversion
// on its way into a comparison — and a conversion on a coordinate is a place to
// rescale one by accident.
type Box = proposal.Box

// GT is one ground-truth object: a box, a class, and whether this build can
// express that class at all.
type GT struct {
	Box
	// Class is the ground truth's own class name, from its own vocabulary. It is
	// NOT a COCO name and it is not expected to be one.
	Class string
	// Expressible reports whether this build has a word for the class.
	//
	// It is the flag that separates "the detector missed it" from "the detector
	// could not have named it", and the two must never be added together without
	// this distinction being visible.
	Expressible bool
	// Ignored marks a box that is deliberately not counted at all — a crowd in the
	// far background, a reflection in a window. It exists because "we chose not to
	// count this" and "the detector missed this" must not be the same row.
	Ignored bool
}

// Detection is one box the detector produced.
type Detection struct {
	Box
	// Frame names which image this came from, because a still set is many images and
	// a box matched against ground truth from a different one is a false positive of
	// the harness rather than of the detector.
	Frame string
}

// IoU is intersection over union, in the one implementation every number here uses.
//
// One implementation and not two, because a precision of 4/5 computed with one
// matching rule and a recall of 6/8 computed with another produces a pair of numbers
// that look like a measurement and are not.
func IoU(a, b Box) float64 {
	ix1, iy1 := math.Max(a.X1, b.X1), math.Max(a.Y1, b.Y1)
	ix2, iy2 := math.Min(a.X2, b.X2), math.Min(a.Y2, b.Y2)
	iw, ih := math.Max(0, ix2-ix1), math.Max(0, iy2-iy1)
	inter := iw * ih
	if inter <= 0 {
		return 0
	}
	aa := math.Max(0, a.X2-a.X1) * math.Max(0, a.Y2-a.Y1)
	ab := math.Max(0, b.X2-b.X1) * math.Max(0, b.Y2-b.Y1)
	union := aa + ab - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

// Match is one detection paired with one ground-truth box.
type Match struct {
	Detection
	GT     GT
	IoU    float64
	GTIdx  int
	DetIdx int
}

// Counts is the arithmetic, before it is turned into rates.
//
// It is a struct of raw COUNTS rather than of rates because a rate with a
// zero denominator has to be reported as NOT RUN, and a caller holding only rates
// cannot tell "0.00" from "no samples". The counts are the record; the rates are
// derived from them and never the other way round.
type Counts struct {
	// TruePositives is the number of ground-truth boxes matched by exactly one
	// detection, at or above the match threshold.
	TruePositives int
	// FalsePositives is detections matched to nothing.
	FalsePositives int
	// FalseNegatives is ground-truth boxes matched to nothing, EXCLUDING the dropped
	// ones. A drop is not a miss.
	FalseNegatives int
	// Dropped is ground-truth boxes this build cannot express a class for, plus the
	// Ignored ones. They are counted apart and never added to FalseNegatives.
	Dropped int
	// GroundTruth is the denominator for recall: every counted box, matched or not.
	GroundTruth int
	// Detected is the denominator for precision.
	Detected int
	// Seconds is the wall time the material covers, for the per-hour rate. Zero is a
	// real value and means "not measured", which is why FPPerHour reports ok=false.
	Seconds float64
}

// Precision is true positives over detections, and whether it could be computed.
//
// `ok=false` over zero detections: a precision of 0.00 from no detections would be
// a claim that every detection is wrong, and there were none. It is the same
// absence rule the rest of this module follows.
func (c Counts) Precision() (float64, bool) {
	if c.Detected == 0 {
		return 0, false
	}
	return float64(c.TruePositives) / float64(c.Detected), true
}

// Recall is true positives over ground truth, and whether it could be computed.
//
// The denominator EXCLUDES dropped boxes. That is the taxonomy-penalty rule stated
// as arithmetic: a `hair drier` this build cannot name is not a recall miss, and
// [Report.Dropped] exists so the reader can see how many were set aside.
func (c Counts) Recall() (float64, bool) {
	if c.GroundTruth == 0 {
		return 0, false
	}
	return float64(c.TruePositives) / float64(c.GroundTruth), true
}

// FPPerHour is false positives per hour of material, and whether it could be
// computed.
//
// This is the number that goes on a purchase order and the one nobody computes,
// because it needs a DURATION and a duration needs a clock the benchmark does not
// own. Zero seconds is NOT zero false positives per hour — it is an absent
// denominator, and it is reported as `ok=false` rather than as an encouraging
// infinity.
func (c Counts) FPPerHour() (float64, bool) {
	if c.Seconds <= 0 {
		return 0, false
	}
	return float64(c.FalsePositives) / (c.Seconds / 3600), true
}

// Report is the finished measurement, with everything a reader needs to argue with
// it.
type Report struct {
	Counts
	// Matched is the pairing, sorted by IoU descending so the best match is first
	// and a reader can see how tight the matching was. A report that gives recall
	// without saying the match was at IoU 0.25 is asking the reader to trust a
	// threshold they cannot see.
	Matched []Match
	// ClassCounts is the run's per-class partition, folded from the frames' own.
	//
	// It is beside the totals and not inside them because a total cannot be split: the
	// counts say how many were missed and say nothing about WHAT was missed, which is
	// the entire content of a per-class row.
	ClassCounts []ClassCounts
	// MatchThreshold is the IoU a pair had to reach, printed on every report.
	MatchThreshold float64
	// DroppedBoxes and DroppedFrames name WHAT was set aside. Both are counted and
	// both are non-zero in any real Open Images run, because COCO-80 cannot express
	// 600 classes.
	DroppedBoxes  int
	DroppedFrames int
	// DroppedByClass breaks the drop down by class, so a reader can see whether it is
	// one systematic gap or a long tail.
	DroppedByClass map[string]int
	// Unmarked is how many PROPOSED boxes carry no human judgement, over a run
	// measurement.
	//
	// It is a first-class field rather than something derived at the call site
	// because it is the difference between two numbers that look identical: a
	// detector's precision over the boxes somebody looked at, and its precision over
	// the boxes nobody did. The second is not a measurement and printing it as one is
	// how a benchmark reports a figure that no human ever confirmed.
	Unmarked int
	// TPFromLabels is the accepted count from a marking session, which is the
	// run-measurement analogue of a true positive. It is separate from
	// TruePositives because that field is computed by BOX MATCHING against ground
	// truth, and a run has no ground truth — it has a human's opinion, which is a
	// different and equally useful kind of truth.
	TPFromLabels int
	// PerFrame is each frame's own counts, because a per-frame number is what a
	// reader checks against the picture they are looking at.
	PerFrame []FrameCount
}

// FrameCount is one frame's arithmetic.
type FrameCount struct {
	Frame          string
	TruePositives  int
	FalsePositives int
	FalseNegatives int
	Dropped        int
	GroundTruth    int
	Detected       int
	// ByClass is this frame's own per-class partition.
	//
	// It is computed HERE and not from the counts above, because a per-class table
	// cannot be reconstructed from a total. The counts say how many were missed; they
	// say nothing about WHAT was missed, and the class of a missed box is the entire
	// content of a per-class row.
	//
	// It is carried on the FRAME rather than accumulated into the report directly
	// because the match indices are frame-local. Summing indices across frames would
	// pair a detection from one frame with a ground-truth box from another, which is a
	// silent wrong answer rather than a compile error.
	ByClass []ClassCounts
}

// Options are the settings a measurement is taken under, and every one of them
// appears on the report because every one of them moves the numbers.
type Options struct {
	// MatchThreshold is the IoU a detection and a ground-truth box must reach to be
	// the same object.
	//
	// 0.5 is the COCO convention and it is the default. It is a MODEL property and
	// not a preference: a ground-truth box drawn by a human is tight, a detector's
	// box on a partly-occluded object is not, and lowering the threshold is how a
	// benchmark reports a recall improvement that is really a threshold change.
	MatchThreshold float64
	// PerClass reports per-class recall as well as the overall figure.
	PerClass bool
	// ClassThreshold is the minimum score a detection must carry to be counted.
	//
	// It is here rather than assumed to be 0 because the detector's emit floor and
	// the run's approval bar are different things, and a benchmark that counted
	// boxes the run would have refused is measuring a different system.
	ClassThreshold float64
}

// DefaultOptions is the declared default, resolved in one place.
func DefaultOptions() Options { return Options{MatchThreshold: 0.5, PerClass: true} }

func (o Options) withDefaults() Options {
	if o.MatchThreshold <= 0 {
		o.MatchThreshold = 0.5
	}
	return o
}

// Measure pairs a frame's detections against its ground truth and returns the
// arithmetic.
//
// The pairing is GREEDY BY IoU and one-to-one. Greedy rather than optimal because
// the optimal assignment is a Hungarian algorithm for a difference that on a still
// set is well under a percent, and a reader who wants the optimal pairing should
// read the matcher. One-to-one because two detections on one ground-truth box is
// the FALSE POSITIVE this metric exists to count: it is how a detector emits a
// `car` and a `truck` on one object, and collapsing them into one true positive
// would hide the only interesting thing about them.
func Measure(frame string, dets []Detection, truth []GT, opts Options) FrameCount {
	o := opts.withDefaults()
	fc := FrameCount{Frame: frame}
	// The per-class partition is computed from the SAME pairing this function makes,
	// here, while both sides' indices mean something. It is not derived from the totals
	// afterwards because the totals carry no classes.
	var pairs []Match
	for _, d := range dets {
		if d.Score < o.ClassThreshold {
			continue
		}
		fc.Detected++
	}
	// A hand-rolled greedy: sort every candidate pair by IoU descending and take
	// them while both sides are free.
	type cand struct {
		di, gi int
		iou    float64
	}
	var cands []cand
	for di, d := range dets {
		if d.Score < o.ClassThreshold {
			continue
		}
		for gi, g := range truth {
			if g.Ignored {
				continue
			}
			if v := IoU(d.Box, g.Box); v >= o.MatchThreshold {
				cands = append(cands, cand{di, gi, v})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].iou > cands[j].iou })
	usedDet := make([]bool, len(dets))
	usedGT := make([]bool, len(truth))
	for _, c := range cands {
		if usedDet[c.di] || usedGT[c.gi] {
			continue
		}
		usedDet[c.di], usedGT[c.gi] = true, true
		fc.TruePositives++
		pairs = append(pairs, Match{
			Detection: dets[c.di], GT: truth[c.gi], IoU: c.iou, DetIdx: c.di, GTIdx: c.gi,
		})
	}
	for di := range dets {
		if dets[di].Score < o.ClassThreshold {
			continue
		}
		if !usedDet[di] {
			fc.FalsePositives++
		}
	}
	for gi, g := range truth {
		if g.Ignored {
			fc.Dropped++
			continue
		}
		if !usedGT[gi] {
			if !g.Expressible {
				// A miss on a class this build cannot name is a TAXONOMY penalty, not
				// a detector failure. Counting it as a false negative would report a
				// recall figure that is really a statement about the vocabulary.
				fc.Dropped++
				continue
			}
			fc.FalseNegatives++
		}
		fc.GroundTruth++
	}
	// The per-class partition, from the pairing and the same slices, so its indices
	// mean what they say.
	fc.ByClass = classCounts(dets, truth, pairs, usedDet, usedGT, o)
	return fc
}

// Accumulate folds frame counts into a run report.
//
// It is separate from [Measure] because the per-frame numbers are what a reader
// checks against a picture and the run numbers are what a report prints, and
// computing one from the other is a place for them to disagree.
func Accumulate(frames []FrameCount, seconds float64, droppedByClass map[string]int, droppedFrames int) Report {
	rep := Report{MatchThreshold: 0.5, DroppedByClass: droppedByClass, DroppedFrames: droppedFrames}
	rep.PerFrame = frames
	// The run's per-class table is the FOLD of its frames' tables, computed here where
	// the frames are in hand. It is not derived from the run's totals afterwards: the
	// totals carry no classes, so there is nothing to derive it from.
	rep.ClassCounts = FoldClassCounts(frames)
	for _, f := range frames {
		rep.TruePositives += f.TruePositives
		rep.FalsePositives += f.FalsePositives
		rep.FalseNegatives += f.FalseNegatives
		rep.Dropped += f.Dropped
		rep.GroundTruth += f.GroundTruth
		rep.Detected += f.Detected
	}
	rep.Seconds = seconds
	for _, v := range droppedByClass {
		rep.DroppedBoxes += v
	}
	return rep
}

// Format renders a measurement, and every rate is PASS-like or NOT RUN with the
// count that decided it.
//
// The rule is the module's: absence is never rendered as zero. A precision with no
// detections, a recall with no ground truth and a per-hour rate with no duration
// are all `NOT RUN` naming what is missing — because all three print as `0.000` if
// they are allowed to, and a number of 0.000 on a rate is the most reassuring thing
// a report can say and the one most likely to be wrong.
func Format(rep Report, opts Options) string {
	o := opts.withDefaults()
	rep.MatchThreshold = o.MatchThreshold
	var b strings.Builder
	b.WriteString("detector measurement\n")
	fmt.Fprintf(&b, "  match IoU     %.2f (printed because a recall without its matching rule is a\n", o.MatchThreshold)
	fmt.Fprintf(&b, "                claim about a threshold the reader cannot see)\n")
	if rep.Seconds > 0 {
		fmt.Fprintf(&b, "  material      %.1f seconds\n", rep.Seconds)
	} else {
		b.WriteString("  material      NOT MEASURED\n")
	}
	fmt.Fprintf(&b, "  counts        tp %d  fp %d  fn %d  dropped %d\n",
		rep.TruePositives, rep.FalsePositives, rep.FalseNegatives, rep.Dropped)
	fmt.Fprintf(&b, "  denominators  detections %d  ground truth %d\n\n", rep.Detected, rep.GroundTruth)

	if v, ok := rep.Precision(); ok {
		fmt.Fprintf(&b, "  PRECISION     %.3f  (%d of %d detections were a real object)\n",
			v, rep.TruePositives, rep.Detected)
	} else {
		b.WriteString("  PRECISION     NOT RUN — the detector produced no detections, so there is\n")
		b.WriteString("                nothing to be right or wrong about. This is not 0.000.\n")
	}
	if v, ok := rep.Recall(); ok {
		fmt.Fprintf(&b, "  RECALL        %.3f  (%d of %d ground-truth objects were found)\n",
			v, rep.TruePositives, rep.GroundTruth)
	} else {
		b.WriteString("  RECALL        NOT RUN — no ground-truth box was countable, so there is no\n")
		b.WriteString("                denominator. This is not 0.000.\n")
	}
	if v, ok := rep.FPPerHour(); ok {
		fmt.Fprintf(&b, "  FP PER HOUR   %.2f  (%d false positives over %.1f seconds)\n",
			v, rep.FalsePositives, rep.Seconds)
	} else {
		b.WriteString("  FP PER HOUR   NOT RUN — the material's duration was not measured, and a\n")
		b.WriteString("                per-hour rate with no duration is an encouraging infinity.\n")
	}

	if rep.Dropped > 0 {
		fmt.Fprintf(&b, "\n  TAXONOMY      %d ground-truth boxes were NOT counted against recall,\n", rep.Dropped)
		b.WriteString("                because this build has no class name for them. A recall quoted\n")
		b.WriteString("                without this number is a claim about the vocabulary, not about\n")
		b.WriteString("                the model. Breakdown:\n")
		keys := make([]string, 0, len(rep.DroppedByClass))
		for k := range rep.DroppedByClass {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "                  %-24s %d\n", k, rep.DroppedByClass[k])
		}
		if rep.DroppedFrames > 0 {
			fmt.Fprintf(&b, "                and %d images were dropped whole, for the same reason\n",
				rep.DroppedFrames)
		}
	}
	if len(rep.PerFrame) > 0 {
		b.WriteString("\n  per frame\n")
		for _, f := range rep.PerFrame {
			fmt.Fprintf(&b, "    %-28s tp %-4d fp %-4d fn %-4d dropped %-4d gt %-4d det %d\n",
				f.Frame, f.TruePositives, f.FalsePositives, f.FalseNegatives, f.Dropped,
				f.GroundTruth, f.Detected)
		}
	}
	return b.String()
}
