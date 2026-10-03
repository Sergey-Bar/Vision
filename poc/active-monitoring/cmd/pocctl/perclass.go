package main

// perclass.go - per-class precision and recall, for one setting or for two side
// by side.
//
// # Why the partition happens HERE and not in internal/bench
//
// `bench.Measure(frame, dets, truth, opts)` pairs detections with ground truth by
// GEOMETRY ALONE. It never looks at a class. That is correct for what it was
// written for - `pocctl bench --images` measures a detector's ability to place a
// box on a thing - and it is WRONG for a per-class table, because a `car`
// detection sitting on top of a `person` ground-truth box is one true positive
// under geometric matching, and it is a `car` false positive plus a `person`
// false negative under any definition a reader would use.
//
// So the partition happens BEFORE the call: one `bench.Measure` per class, over
// that class's detections and that class's ground truth only. The arithmetic,
// the counts, the denominators and the `NOT RUN` rules are all `bench`'s; only
// the partition is here, and it is here because it is the part that needs the
// class.
//
// REDCHECK RC-34: replace the partition with a single unpartitioned `Measure` and
// a `car`-over-`person` pair becomes a true positive.
// TestAMisnamedDetectionIsAFalsePositiveNotATruePositive.
//
// # The two rules this file exists to enforce
//
//  1. A rate with no denominator is `NOT RUN`, never 0.000. [bench.Counts]
//     already reports the bool, and this file is what refuses to print the number
//     anyway. The reason is not pedantry: 0.000 is the most reassuring thing a
//     table can print, and over an empty denominator it is the one most likely to
//     be wrong.
//
//  2. This is NOT mAP. One threshold, one IoU, one operating point, no
//     precision-recall curve, no area under it, and no averaging across classes.
//     Every table says so on its face, because a per-class precision table with
//     no such line is read as a benchmark result by everybody who has not been
//     told otherwise, and this module has had three of those.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
)

// notRun is what a rate prints when it has no denominator.
const notRun = "NOT RUN"

// classRow is one class's arithmetic for one setting.
type classRow struct {
	Class  string
	Counts bench.Counts
}

// classFrame is one frame's detections and ground truth, ready to partition.
type classFrame struct {
	Frame string
	// Dets carry their class in the embedded Box, which is the field the decoder
	// filled. Ground truth carries its class in GT.Class, which is the field the
	// annotation filled.
	Dets  []bench.Detection
	Truth []bench.GT
}

// partitionByClass measures each frame and returns one row per class.
//
// It partitions, measures and accumulates with `bench` on every step, and the
// accumulation goes through [bench.Accumulate] rather than being summed here: a
// second summation is a second place for the two to disagree, and the per-frame
// numbers are what a reader checks against the picture.
//
// A class present in only one side still gets a row. A class the model named and
// nobody drew is a row of zero ground truth, and its RECALL column is NOT RUN
// for want of a denominator while its precision column may be perfectly
// measurable - which is the honest split, and hiding it behind a single blank
// would throw away the half that can be computed.
func partitionByClass(frames []classFrame, opts bench.Options) []classRow {
	type acc struct {
		frames   []bench.FrameCount
		dropped  map[string]int
		dropFram int
	}
	byClass := map[string]*acc{}
	get := func(name string) *acc {
		if a, ok := byClass[name]; ok {
			return a
		}
		a := &acc{dropped: map[string]int{}}
		byClass[name] = a
		return a
	}
	for _, fr := range frames {
		dets := map[string][]bench.Detection{}
		for _, d := range fr.Dets {
			n := d.Class
			if n == "" {
				// A detection with no class is not a detection this table can
				// partition, and it is counted under a stated name rather than
				// dropped: a decoder that emitted one is a finding, and dropping it
				// would improve this table.
				n = "(unnamed)"
			}
			dets[n] = append(dets[n], d)
		}
		truth := map[string][]bench.GT{}
		for _, g := range fr.Truth {
			n := g.Class
			if n == "" {
				n = "(unlabelled)"
			}
			truth[n] = append(truth[n], g)
		}
		for n := range dets {
			get(n)
		}
		for n := range truth {
			get(n)
		}
		for n, a := range byClass {
			fc := bench.Measure(fr.Frame, dets[n], truth[n], opts)
			a.frames = append(a.frames, fc)
			for _, g := range truth[n] {
				// A box this build cannot name, or one the annotation set aside, is
				// DROPPED rather than missed. bench.Measure already keeps it out of
				// the recall denominator; counting it here as well is what puts the
				// number beside the precision figure.
				if g.Ignored || !g.Expressible {
					a.dropped[g.Class]++
				}
			}
		}
	}
	out := make([]classRow, 0, len(byClass))
	for name, a := range byClass {
		rep := bench.Accumulate(a.frames, 0, a.dropped, a.dropFram)
		out = append(out, classRow{Class: name, Counts: rep.Counts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}

// rate renders one rate and whether it could be computed.
//
// The bool is carried through from [bench.Counts] rather than tested against a
// count here, so there is exactly one place in this file that decides what "no
// denominator" means and it is the one that already has the rule.
func rate(v float64, ok bool, width int) string {
	if !ok {
		return padRight(notRun, width)
	}
	return padLeft(fmt.Sprintf("%.3f", v), width)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func padLeft(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat(" ", n-len(s)) + s
}

// theDisclaimer is printed on the face of every per-class table.
//
// It is a constant and not an assembled sentence so that a future edit cannot
// drop half of it, and it is on the table rather than in a footer because a
// footer is what a reader reaches after they have already quoted the number.
const theDisclaimer = "  This is NOT mAP. One emit threshold, one match IoU, one operating point, no\n" +
	"  precision-recall curve and no area under it, and no averaging across classes. It is two\n" +
	"  rates at a single point, and a mAP figure quoted beside it would not be comparable.\n"

// CommandPerClass renders one setting's per-class table.
//
// Shape: the class, the raw counts, then the two rates. The counts come before
// the rates because a rate with its counts next to it can be checked by
// arithmetic, and a rate on its own has to be taken on trust.
func CommandPerClass(rows []classRow, opts bench.Options, setting string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "per class - %s (match IoU %.2f)\n", setting, opts.MatchThreshold)
	b.WriteString(theDisclaimer)
	if len(rows) == 0 {
		b.WriteString("\n  No class was named by anything, so there is no row to report. That is the\n")
		b.WriteString("  absence of a measurement and not a precision of zero.\n")
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString("  class                 det    tp    fp    fn  drop     gt   precision     recall\n")
	for _, r := range rows {
		c := r.Counts
		p, pok := c.Precision()
		rec, rok := c.Recall()
		fmt.Fprintf(&b, "  %-20s %5d %5d %5d %5d %5d %6d   %s %s\n",
			r.Class, c.Detected, c.TruePositives, c.FalsePositives, c.FalseNegatives,
			c.Dropped, c.GroundTruth,
			rate(p, pok, 9), rate(rec, rok, 9))
	}
	return b.String()
}

// CommandPerClassSideBySide renders BOTH settings in one table, per class.
//
// Side by side is the whole point. The same object, the same frames, the same
// ground truth, two ways of looking at them, in adjacent columns - because the
// question this command was written to answer is not "what is the precision" but
// "did the crop find anything the whole-frame pass did not", and a question
// answered by two tables on opposite sides of a terminal is a question whose
// answer a reader has to assemble by hand.
//
// A class that appears under only one setting still gets its row in both, with
// NOT RUN on the missing side. Printing the class only where it exists would
// hide exactly the rows that carry the result: a `car` the crop pass found and
// the whole-frame pass never named.
func CommandPerClassSideBySide(whole, crop []classRow, opts bench.Options) string {
	names := map[string]bool{}
	for _, r := range whole {
		names[r.Class] = true
	}
	for _, r := range crop {
		names[r.Class] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	wholeBy := indexRows(whole)
	cropBy := indexRows(crop)

	var b strings.Builder
	fmt.Fprintf(&b, "per class, WHOLE-FRAME beside CROP (match IoU %.2f)\n", opts.MatchThreshold)
	b.WriteString(theDisclaimer)
	if len(ordered) == 0 {
		b.WriteString("\n  No class was named under either setting, so there is nothing to compare. That\n")
		b.WriteString("  is the absence of a measurement and not a pair of zeros.\n")
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString("                        whole-frame                                crop\n")
	b.WriteString("  class               det tp fp fn   precision   recall | det tp fp fn   precision   recall\n")
	for _, n := range ordered {
		w, wok := wholeBy[n]
		c, cok := cropBy[n]
		var wc, cc bench.Counts
		if wok {
			wc = w.Counts
		}
		if cok {
			cc = c.Counts
		}
		pw, pwok := wc.Precision()
		rw, rwok := wc.Recall()
		pc, pcok := cc.Precision()
		rc, rcok := cc.Recall()
		fmt.Fprintf(&b, "  %-18s %3s %2s %2s %2s   %s %s | %3s %2s %2s %2s   %s %s\n",
			n,
			num(wok, wc.Detected), num(wok, wc.TruePositives), num(wok, wc.FalsePositives), num(wok, wc.FalseNegatives),
			rate(pw, pwok, 9), rate(rw, rwok, 9),
			num(cok, cc.Detected), num(cok, cc.TruePositives), num(cok, cc.FalsePositives), num(cok, cc.FalseNegatives),
			rate(pc, pcok, 9), rate(rc, rcok, 9))
	}
	b.WriteString("\n  A cell reads NOT RUN rather than a zero because its denominator was empty: no\n")
	b.WriteString("  detection of that class under that setting, or no drawn box of that class. Both are\n")
	b.WriteString("  absences, and a zero would be a claim that everything drawn was wrong.\n")
	return b.String()
}

// indexRows keys rows by class for the side-by-side join.
func indexRows(rows []classRow) map[string]classRow {
	m := make(map[string]classRow, len(rows))
	for _, r := range rows {
		m[r.Class] = r
	}
	return m
}

// num renders a count, or a dash when the class is absent from that side.
//
// A dash rather than a zero: the class did not occur under that setting, which is
// not the same as occurring zero times, and the two have opposite meanings for a
// precision column.
func num(present bool, v int) string {
	if !present {
		return "-"
	}
	return fmt.Sprintf("%d", v)
}
