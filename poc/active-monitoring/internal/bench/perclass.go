package bench

// Per-class precision and recall, which is the number a CV reviewer asks for first and
// the number the harness's global figure cannot answer.
//
// # Why per CLASS and not one figure
//
// A single precision over a run containing cars and birds describes neither. It can be
// 0.98 because the cars are found perfectly and the birds are never found at all, and
// every reader of that number takes it as a claim about the system rather than about one
// of the two things it is made of. `bird` and `car` get separate rows, even when one of
// them is a poor result.
//
// # Why this is NOT mAP and says so
//
// One minute of footage with a handful of labelled instances supports a precision and a
// recall per class. It does not support a mean average precision, which needs a
// confidence sweep, an interpolated curve and a dataset whose size is stated. Printing
// "mAP" here would be borrowed authority for a number this material cannot produce, and
// it is the most common way a demo overstates what it measured.
//
// So every row says what it IS, with its denominator on the same line. A rate whose
// denominator is not printed is a rate nobody can check.

// ClassCounts is one class's arithmetic.
//
// It is a struct of raw COUNTS rather than of rates for the same reason [Counts] is: a
// rate with a hidden denominator cannot be checked.
type ClassCounts struct {
	// Class is the name the two sides agree on for the object.
	Class string
	// TruePositives is ground-truth boxes of this class matched by a detection.
	TruePositives int
	// FalsePositives is detections OF THIS CLASS matched to nothing.
	//
	// It is per class and not the global count, and the difference matters: a car
	// detected where there is a bird is a false positive for `car` and a false NEGATIVE
	// for `bird`, and folding both into one bucket hides the confusion a reader most
	// needs to see.
	FalsePositives int
	// FalseNegatives is ground-truth boxes of this class matched by nothing, of either
	// no detection or a detection of a DIFFERENT class.
	FalseNegatives int
	// Misread is how many of those misses were matched by a detection of another class,
	// which is a worse failure than a miss because something was found and called wrong.
	Misread int
	// GroundTruth is the recall denominator and Detected the precision one.
	GroundTruth int
	Detected    int
}

// Recall is true positives over labelled boxes, and whether it could be computed.
//
// `ok=false` over zero labelled boxes: a recall of 0.00 from no ground truth would claim
// nothing was found when nothing was there to find. That is the same absence rule the
// rest of this module follows, and it is the difference between a number and a guess.
func (c ClassCounts) Recall() (float64, bool) {
	if c.GroundTruth == 0 {
		return 0, false
	}
	return float64(c.TruePositives) / float64(c.GroundTruth), true
}

// Precision is true positives over detections, and whether it could be computed.
func (c ClassCounts) Precision() (float64, bool) {
	if c.Detected == 0 {
		return 0, false
	}
	return float64(c.TruePositives) / float64(c.Detected), true
}

// classCounts partitions ONE FRAME.
//
// It takes the pairing and the used-flags rather than recomputing them, because the
// pairing is the decision and this function must not make a second one. A second
// matcher is a second opinion about what the same object is, which is how a benchmark
// reports a recall the picture does not support.
//
// It is a function over a frame and not over a report because the match indices are
// frame-local. Accumulating them across frames would pair a detection from one frame
// with a ground-truth box from another.
func classCounts(dets []Detection, truth []GT, pairs []Match,
	usedDet, usedGT []bool, o Options) []ClassCounts {

	byClass := map[string]*ClassCounts{}
	get := func(c string) *ClassCounts {
		if c == "" {
			return nil
		}
		e, ok := byClass[c]
		if !ok {
			e = &ClassCounts{Class: c}
			byClass[c] = e
		}
		return e
	}

	// A matched pair. When the two sides agree on the class it is a true positive for
	// that class. When they do not, it is a true positive for NEITHER: a bird found as
	// a car is a miss for `bird` and a false positive for `car`, and calling it a hit for
	// both would produce two true positives and two correct-looking rates for one
	// mistake.
	for _, m := range pairs {
		gt, det := get(m.GT.Class), get(m.Detection.Class)
		if gt != nil {
			gt.GroundTruth++
		}
		// The DETECTION's own class counts this detection, matched or not. It is the
		// precision denominator, and leaving a matched detection out of it produces a
		// precision above 1.0 -- which is the arithmetic error this comment exists to
		// record, and which the test for it caught.
		if det != nil {
			det.Detected++
		}
		if gt != nil && det != nil && gt == det {
			gt.TruePositives++
			continue
		}
		if gt != nil {
			gt.FalseNegatives++
			gt.Misread++
		}
		if det != nil {
			det.FalsePositives++
		}
	}

	// A ground-truth box nothing matched. A DROP is not a miss: it is a class this build
	// cannot name, or a region somebody marked ignore, and counting either as a miss
	// would report a recall figure that is really a statement about the vocabulary.
	for gi, g := range truth {
		if g.Ignored || !g.Expressible || usedGT[gi] {
			continue
		}
		if e := get(g.Class); e != nil {
			e.GroundTruth++
			e.FalseNegatives++
		}
	}
	// A detection nothing matched.
	for di, d := range dets {
		if d.Score < o.ClassThreshold || usedDet[di] {
			continue
		}
		if e := get(d.Class); e != nil {
			e.Detected++
			e.FalsePositives++
		}
	}

	out := make([]ClassCounts, 0, len(byClass))
	for _, e := range byClass {
		out = append(out, *e)
	}
	sortClassCounts(out)
	return out
}

// FoldClassCounts sums per-frame partitions into a run-level one.
//
// It is a fold and not a recomputation for the reason above: a run-level table is the
// SUM of its frames' tables, and deriving it from the run's totals would produce the
// same rates with a different set of denominators when a class appears in only some
// frames.
func FoldClassCounts(frames []FrameCount) []ClassCounts {
	byClass := map[string]*ClassCounts{}
	for _, f := range frames {
		for _, c := range f.ByClass {
			e, ok := byClass[c.Class]
			if !ok {
				cp := c
				e = &ClassCounts{Class: c.Class}
				byClass[c.Class] = e
				_ = cp
			}
			e.TruePositives += c.TruePositives
			e.FalsePositives += c.FalsePositives
			e.FalseNegatives += c.FalseNegatives
			e.Misread += c.Misread
			e.GroundTruth += c.GroundTruth
			e.Detected += c.Detected
		}
	}
	out := make([]ClassCounts, 0, len(byClass))
	for _, e := range byClass {
		out = append(out, *e)
	}
	sortClassCounts(out)
	return out
}

// sortClassCounts orders by class name, so a table's row order does not change between
// two runs of the same material. A table that reorders itself cannot be compared by eye
// and has to be diffed, which is the wrong tool for reading a table.
func sortClassCounts(cs []ClassCounts) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j].Class < cs[j-1].Class; j-- {
			cs[j], cs[j-1] = cs[j-1], cs[j]
		}
	}
}
