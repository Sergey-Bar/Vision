package soak

// The per-site, per-class verification slice, as the report prints it.
//
// This is the section the platform calls "where does the system stay quiet" and
// the POC did not have. It belongs beside `cropGateSection` and not anywhere
// else in the report, because the two answer adjacent questions:
//
//   * the crop gate says when WE decided not to LOOK — the rate gate, the
//     novelty gate, a view that changed mid-pan;
//   * this says when we asked and were NOT ANSWERED — a sidecar that was slow,
//     malformed, never reached, or uninterpretable.
//
// Both are "the run stayed quiet for a reason", and one is about our own
// pipeline while the other is about the plane we asked. Printing only the first
// is how a dead sidecar reads as a quiet road, which is the most expensive
// sentence this module can print, and it has printed it three times.
//
// The arithmetic here is deliberately NOT folded into anything. There is no
// single "silence rate" over the whole run: a share over a denominator nobody
// printed is a number about the denominator, and this section's entire value is
// that the denominator is on the page. Counts first, one stated share per row,
// and every share says what its denominator was.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// VerificationCell is one row of the per-site, per-class table. It is the
// store's own shape reached unchanged, so there is one definition of what a cell
// is and a reader can check either side.
type VerificationCell = store.VerificationCell

// VerificationSlice is the whole section: the cells, the run's own totals, and
// the denominator beside them.
type VerificationSlice struct {
	// Measured says whether there was anything to measure. An unmeasured slice
	// prints NOT RUN and a measured zero prints 0, and those are different
	// sentences about different runs.
	Measured bool `json:"measured"`

	// TotalDetections is every detection of the run, including the ones the
	// pipeline abandoned. Retained is the denominator every share below uses.
	TotalDetections int `json:"total_detections"`
	Retained        int `json:"retained"`

	// ByState is the run-level histogram, over the retained detections only.
	ByState map[string]int `json:"by_state"`

	// Answered and Unanswered are the two halves of the same question, and
	// Unanswered is the three CONTRACT.md §2.2 blindness states plus
	// `cannot_tell` — never `no_objects`, which is a real answer.
	Answered   int `json:"answered"`
	Unanswered int `json:"unanswered"`

	// UnansweredShareFrac is Unanswered over Retained, and Measured says
	// whether it is a measurement. Zero out of zero is not a rate.
	UnansweredShareFrac  float64 `json:"unanswered_share"`
	UnansweredMeasured   bool    `json:"unanswered_share_measured"`
	UnansweredShareDenom int     `json:"unanswered_share_denominator"`

	// Cells is the per-site, per-class table. Empty when the slice is not
	// measured, and empty is not a stated zero.
	Cells []VerificationCell `json:"cells,omitempty"`

	// Sites and Classes are the row and column axes of the table, sorted, so
	// the printed table is byte-stable from run to run.
	Sites   []string `json:"sites,omitempty"`
	Classes []string `json:"classes,omitempty"`
}

// VerificationSliceOf builds the slice from the store's cells.
//
// It takes the totals rather than re-reading them, because a slice that queried
// the database twice would be a slice whose two halves could disagree about
// the same run — and this repository's own rule is that a report describes the
// database and not the moment.
func VerificationSliceOf(cells []VerificationCell, byState map[string]int, total, retained int) VerificationSlice {
	v := VerificationSlice{
		Measured:        retained > 0 || len(cells) > 0,
		TotalDetections: total, Retained: retained,
		ByState: byState,
	}
	if v.ByState == nil {
		v.ByState = map[string]int{}
	}
	// The totals are walked over the CLOSED SET rather than over whatever the
	// database happened to hold, so an unrecognised state is refused here and
	// printed as a refusal rather than silently omitted from a share whose
	// numerator then does not add up to the denominator on the same page.
	for _, k := range aistate.All {
		n := byState[k]
		if aistate.IsAnswered(k) {
			v.Answered += n
			continue
		}
		v.Unanswered += n
	}
	for k, n := range byState {
		if !aistate.Valid(k) {
			v.ByState["unrecognised:"+k] = n
		}
	}
	if v.Retained > 0 {
		v.UnansweredShareFrac = float64(v.Unanswered) / float64(v.Retained)
		v.UnansweredMeasured = true
		v.UnansweredShareDenom = v.Retained
	}
	for _, c := range cells {
		v.Sites = append(v.Sites, c.Site)
		v.Classes = append(v.Classes, c.Class)
	}
	sort.Strings(v.Sites)
	sort.Strings(v.Classes)
	// The axes are DEDUPLICATED after sorting rather than while building, so the
	// table's rows and columns are a set. A site that produced detections of four
	// classes must appear once as a row and not four times.
	v.Sites = sortedSet(v.Sites)
	v.Classes = sortedSet(v.Classes)
	v.Cells = cells
	return v
}

// sortedSet sorts and deduplicates a list of axis labels.
//
// Deduplicated AFTER the fact rather than checked before each append: the check
// is O(n) per append and the axis is short, but the reason is not speed. A
// check-before-append quietly drops a duplicate and leaves the reason invisible;
// a sorted set says what it is and can be read.
func sortedSet(in []string) []string {
	sort.Strings(in)
	out := make([]string, 0, len(in))
	for i, s := range in {
		if i > 0 && in[i-1] == s {
			continue
		}
		out = append(out, s)
	}
	return out
}

// cellAt looks one cell up by (site, class, state). A missing cell is a stated
// zero: the query grouped what the database held, and a combination that never
// happened really was zero times.
func cellAt(cells []VerificationCell, site, class, state string) int {
	for _, c := range cells {
		if c.Site == site && c.Class == class && c.State == state {
			return c.Count
		}
	}
	return 0
}

// verificationSliceSection is the paragraph, and it follows the house rules of
// every other section here: every number is a measurement or says so, and a
// section with nothing to measure prints NOT RUN rather than a confident zero.
func verificationSliceSection(v VerificationSlice) string {
	var b strings.Builder
	b.WriteString("## Where this run stayed quiet, per site and class\n\n")
	if !v.Measured {
		b.WriteString("**NOT RUN.** No detection of this run reached the verification slice, so nothing\n")
		b.WriteString("here says anything about whether the model was being served. A run that never\n")
		b.WriteString("produced a detection and a run whose detector saw nothing print the same\n")
		b.WriteString("`detections | 0`, and only one of them is a measurement.\n\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Of the **%d** detection(s) this run retained (**%d** including the ones the\n", v.Retained, v.TotalDetections)
	b.WriteString("pipeline abandoned), the AI plane answered **")
	fmt.Fprintf(&b, "%d** and could not answer **%d**.\n\n", v.Answered, v.Unanswered)
	if v.UnansweredMeasured {
		fmt.Fprintf(&b, "The unanswered share was **%.2f%%** of the %d retained detection(s). ", v.UnansweredShareFrac*100, v.UnansweredShareDenom)
		b.WriteString("This is the\nshare of events that reached the line without the model ever saying anything\n")
		b.WriteString("about them, and it is NOT a detection rate: a high share means the plane was\n")
		b.WriteString("not serving, not that the road was empty.\n\n")
	}
	if len(v.ByState) > 0 {
		b.WriteString("By state of the ask:\n\n| state | count | answered? |\n|---|---|---|\n")
		for _, k := range sortedKeys(v.ByState) {
			label := k
			if strings.HasPrefix(k, "unrecognised:") {
				label = strings.TrimPrefix(k, "unrecognised:") + " (not one of the five)"
			}
			fmt.Fprintf(&b, "| `%s` | %d | %s |\n", label, v.ByState[k], yesNo(aistate.IsAnswered(k)))
		}
		b.WriteString("\n")
	}
	if len(v.Cells) > 0 {
		b.WriteString("Per site and class: the cell is the count of detections, by the state of the\n")
		b.WriteString("ask about them. A site whose cells are mostly the blind states is a site the\n")
		b.WriteString("model was not serving, and the classes it is concentrated in are the Alarm\n")
		b.WriteString("Definitions to look at — which is what this table is for.\n\n")
		header := "| site | class |"
		divider := "|---|---|"
		for _, k := range aistate.All {
			header += fmt.Sprintf(" %s |", k)
			divider += "---|"
		}
		b.WriteString(header + "\n" + divider + "\n")
		for _, site := range v.Sites {
			for _, class := range v.Classes {
				row := "| " + site + " | " + class + " |"
				for _, k := range aistate.All {
					row += fmt.Sprintf(" %d |", cellAt(v.Cells, site, class, k))
				}
				b.WriteString(row + "\n")
			}
		}
		b.WriteString("\n")
	}
	if v.Unanswered == 0 && v.Measured {
		b.WriteString("Every retained detection had an answer, so this run says nothing about\n")
		b.WriteString("behaviour under an absent plane. That is a clean run, and it is also the\n")
		b.WriteString("reason the rehearsed faults below exist.\n\n")
	}
	return b.String()
}

// yesNo is the third column of the state table, and it is spelled out because
// a reader skimming a table of states should not have to remember which of the
// five are answers.
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "NO"
}
