package main

// The per-class table on a benchmark report.
//
// It is its own file because it is one concern and `benchrun.go` is already the
// longest file in this command, and because the disclaimer it carries is the point of
// the table rather than an aside on it.

import (
	"fmt"
	"strings"

	"github.com/Sergey-Bar/ai-captain-poc/internal/bench"
)

// writePerClass prints the per-class table, and says what it is NOT.
//
// The disclaimer sits on the table and not above it, because the whole failure this
// guards against is a reader who sees a row of abbreviations and supplies the metric
// they expect. "mAP" is the one they supply, and this material cannot produce it: there
// is no confidence sweep and no interpolated curve anywhere in this module, and a mean
// average precision printed from per-class rates is a number with a name it has not
// earned.
//
// The classes appear on SEPARATE rows even when one of them is a poor result, because a
// single figure over cars and birds describes neither. It can be 0.98 because the cars
// are found perfectly and the birds are never found at all, and every reader of that
// number takes it as a claim about the system rather than about one of the two things it
// is made of. A poor row is therefore the most informative row in the table, which is
// the opposite of how a table feels when it is suppressed.
func writePerClass(out *strings.Builder, r bench.Report) {
	if len(r.ClassCounts) == 0 {
		out.WriteString("  PER CLASS     NOT RUN: no frame in this material carried both a detection and a\n")
		out.WriteString("                labelled box, so there is nothing to partition. Per-class precision and\n")
		out.WriteString("                recall need ground truth, and a still set without it has no rows to print.\n")
		return
	}
	out.WriteString("  PER CLASS     recall and precision per class, each over its own denominator\n")
	fmt.Fprintf(out, "                (matched at IoU >= %.2f)\n", r.MatchThreshold)
	out.WriteString("                This is NOT mAP. There is no confidence sweep and no interpolated curve in\n")
	out.WriteString("                this module, so a mean average precision printed from these rates would be a\n")
	out.WriteString("                number with a name it has not earned.\n")
	for _, c := range r.ClassCounts {
		rec := "NOT RUN (no labelled boxes)"
		if v, ok := c.Recall(); ok {
			rec = fmt.Sprintf("%.3f (%d of %d)", v, c.TruePositives, c.GroundTruth)
		}
		prec := "NOT RUN (no detections)"
		if v, ok := c.Precision(); ok {
			prec = fmt.Sprintf("%.3f (%d of %d)", v, c.TruePositives, c.Detected)
		}
		fmt.Fprintf(out, "                %-14s recall %-28s precision %s\n", c.Class, rec, prec)
		if c.Misread > 0 {
			fmt.Fprintf(out, "                %-14s %d of the misses were matched by a DIFFERENT class. That is\n", "", c.Misread)
			fmt.Fprintf(out, "                %-14s worse than a plain miss: something was found and called wrong.\n", "")
		}
	}
}
