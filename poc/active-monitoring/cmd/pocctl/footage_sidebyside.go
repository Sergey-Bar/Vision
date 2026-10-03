package main

// footage_sidebyside.go - the two settings' per-class tables in ONE table.
//
// # Why this is a second renderer and not a change to writePerClass
//
// `perclass.go` renders ONE setting's per-class table and `benchrun.go` calls it. This file
// renders BOTH settings for the same frames, class by class, in adjacent columns, and it is
// separate because the two answer different questions: `writePerClass` answers "how did this
// detector do on this material", and this answers "did the crop pass find anything the
// whole-frame pass did not". Editing the first to do the second would change what every
// existing benchmark report prints.
//
// # Side by side is the whole point
//
// The question this command was written to answer is not "what is the precision" but "did
// looking again, closer, find something the first look missed". A question answered by two
// tables on opposite sides of a terminal is a question whose answer a reader has to assemble
// by hand, and an assembled comparison is the one that gets quoted wrong.
//
// A class that appears under only one setting still gets its row in BOTH, with the missing
// side marked absent. Printing the class only where it exists would hide exactly the rows
// that carry the result: a `bird` the crop pass found and the whole-frame pass never named.
//
// # The counts are `bench`'s, not this file's
//
// [bench.ClassCounts] and [bench.FoldClassCounts] do the partition and the summation. This
// file only prints. That is deliberate in the same way the crop geometry is: the partition is
// the decision, and a renderer that made its own would be a second opinion about what the same
// object is.
//
// # One set of column widths
//
// The header is rendered through the SAME cell formatter as the rows. A table whose header
// says `fp` above a column of true positives is the kind of misalignment a reader silently
// forgives, and this is a measurement.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
)

// theSideBySideDisclaimer is printed on the face of the table.
//
// It is a constant and not an assembled sentence so a future edit cannot drop half of it, and
// it is ON the table rather than in a footer because a footer is what a reader reaches after
// they have already quoted the number.
const theSideBySideDisclaimer = "This is NOT mAP. One emit threshold, one match IoU, one operating point,\n" +
	"no confidence sweep, no interpolated curve, no area under it and no averaging\n" +
	"across classes. It is two rates at a single point for each of two settings, and a\n" +
	"mAP figure quoted beside it would not be comparable to anything here.\n"

// The column widths, declared once because the header and the rows must agree.
const (
	sideBySideIndent = "                   "
	colClass         = 18
	colCount         = 4 // tp, fp, fn
	colMis           = 5 // mis
	colDenom         = 4 // gt, det
	colRate          = 7 // precision, recall
)

// writePerClassSideBySide prints both settings per class, and says what it is NOT.
func writePerClassSideBySide(out *strings.Builder, whole, crop []bench.ClassCounts, o bench.Options) {
	out.WriteString("\n")
	names := unionClassNames(whole, crop)
	if len(names) == 0 {
		out.WriteString("  BOTH SETTINGS    NOT RUN: neither pass named a class, so there is no row to compare.\n")
		out.WriteString("                 That is the absence of a measurement and not a pair of zeros.\n")
		return
	}
	fmt.Fprintf(out, "  BOTH SETTINGS    per class, WHOLE-FRAME beside CROP (matched at IoU >= %.2f)\n", o.MatchThreshold)
	fmt.Fprintf(out, "                 %s", theSideBySideDisclaimer)
	out.WriteString("\n")
	fmt.Fprintf(out, "%s%s|%s\n", sideBySideIndent, cells("class", "tp", "fp", "fn", "mis", "gt", "det", "prec", "rec"),
		cells("", "tp", "fp", "fn", "mis", "gt", "det", "prec", "rec"))
	wholeBy := classIndex(whole)
	cropBy := classIndex(crop)
	for _, n := range names {
		w, wok := wholeBy[n]
		c, cok := cropBy[n]
		fmt.Fprintf(out, "%s%s|%s\n", sideBySideIndent, dataRow(n, w, wok), dataRow("", c, cok))
	}
	out.WriteString("\n")
	out.WriteString("                 A cell reads NOT RUN rather than a zero when its denominator was empty: no\n")
	out.WriteString("                 detection of that class under that setting, or no drawn box of that class.\n")
	out.WriteString("                 Both are absences, and a zero would be a claim that everything drawn was\n")
	out.WriteString("                 wrong. A zero that IS printed is measured: one drawn box and nothing found is\n")
	out.WriteString("                 a real recall of zero.000 and means what it says. `mis` counts misses matched\n")
	out.WriteString("                 by a DIFFERENT class, which is worse than a miss: something was found and\n")
	out.WriteString("                 called the wrong thing.\n")
}

// cells renders one row of fixed-width columns, used for BOTH the header and the data.
func cells(class string, tp, fp, fn, mis, gt, det, prec, rec string) string {
	return fmt.Sprintf("%-*s %*s %*s %*s %*s %*s %*s  %-*s %-*s",
		colClass, orSpace(class),
		colCount, tp, colCount, fp, colCount, fn,
		colMis, mis, colDenom, gt, colDenom, det,
		colRate, prec, colRate, rec)
}

// dataRow renders one side of one class's row.
func dataRow(name string, c bench.ClassCounts, present bool) string {
	if !present {
		return cells(name, "-", "-", "-", "-", "-", "-", "-", "-")
	}
	prec := notRun
	if v, ok := c.Precision(); ok {
		prec = fmt.Sprintf("%.3f", v)
	}
	rec := notRun
	if v, ok := c.Recall(); ok {
		rec = fmt.Sprintf("%.3f", v)
	}
	return cells(name, itoa(c.TruePositives), itoa(c.FalsePositives), itoa(c.FalseNegatives),
		itoa(c.Misread), itoa(c.GroundTruth), itoa(c.Detected), prec, rec)
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }

// orSpace keeps a blank name from collapsing the column.
func orSpace(s string) string {
	if s == "" {
		return " "
	}
	return s
}

// unionClassNames is every class either side named, sorted.
func unionClassNames(whole, crop []bench.ClassCounts) []string {
	seen := map[string]bool{}
	for _, c := range whole {
		seen[c.Class] = true
	}
	for _, c := range crop {
		seen[c.Class] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// classIndex keys rows by class.
func classIndex(cs []bench.ClassCounts) map[string]bench.ClassCounts {
	m := make(map[string]bench.ClassCounts, len(cs))
	for _, c := range cs {
		m[c.Class] = c
	}
	return m
}
