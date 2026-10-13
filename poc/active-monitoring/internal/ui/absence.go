package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// The absence panel is `pocctl search`'s breakdown, on the screen an operator is
// watching during a run.
//
// It exists because that screen has exactly one sentence about the model and it is
// about the LAST frame: `ai state  asked_answered`. Nothing on it says whether the
// run's other proposals were ever sent. So a run whose verifier never got a key
// shows a green `asked_answered` beside a picture nobody asked about, and the
// absence that `pocctl search` refuses to hide is invisible until somebody happens
// to run the command after the run is over.
//
// The words are `cmd/pocctl/search.go`'s, deliberately and not by accident. Two
// surfaces that describe the same window in two vocabularies is how a run acquires
// one answer on the console and a different one in the evidence, and the
// reconciliation is never done because both readings are individually defensible.

// absencePanel renders the run's absence breakdown, or the reason it cannot.
//
// The FIRST branch is the load-bearing one. A read that failed is a missing
// measurement, and four zeros standing in for it is precisely the fabricated
// measurement this panel exists to refuse: a zero beside the word `no_objects`
// says the model looked at nothing and found nothing, which is a claim about a
// provider nobody called.
func absencePanel(d Data) string {
	if !d.AbsenceKnown {
		return noData("the run's answers could not be read")
	}
	a := d.Absence
	var b strings.Builder
	switch {
	case a.Proposed == 0:
		// No proposals is a statement about the DETECTOR. There is no row to
		// measure a cause against, so there is nothing to count and the panel says
		// so rather than rendering a table of four zeros.
		b.WriteString(`<div class="absent-sum">there were <b>NO detections at all</b> in this run so far, so there was ` +
			`nothing to ask about (<b>not_asked</b>). This is a statement about the detector, not about the model</div>`)
	case a.Answered == 0:
		fmt.Fprintf(&b, `<div class="absent-sum">the model was asked about <b>NOTHING</b>: %d proposal(s), 0 answers `+
			`(not_asked %d, degraded %d)</div>`, a.Proposed, a.NotAsked, a.Degraded)
		b.WriteString(`<div class="absent-sum">the run proposed detections and consulted nobody about them</div>`)
		b.WriteString(causeTable(askableCauses(a)))
	default:
		fmt.Fprintf(&b, `<div class="absent-sum">%d proposal(s), asked and answered %d `+
			`(%d with no objects, %d degraded, %d not asked, %d cannot tell)</div>`,
			a.Proposed, a.Answered, a.NoObjects, a.Degraded, a.NotAsked, a.CannotTell)
		b.WriteString(causeTable(absenceCauses(a)))
	}
	b.WriteString(absenceNotice(a))
	b.WriteString(absencePointer(d))
	return b.String()
}

// absenceCause is one of the four states with the sentence that says what it
// MEANS.
//
// The sentence is not decoration. Four counts and no causes is a number a reader
// cannot act on: `0 not_asked, 2 degraded` is either a clean run or a run that
// missed two objects, and only the cause tells them which.
type absenceCause struct {
	name  string
	count int
	why   string
	cls   string
}

// absenceCauses is the four, in CONTRACT.md 2.2's order.
//
// They are four and not two because they have four different causes with four
// different owners. `no_objects` is the model saying the frame was empty, which is
// a negative answer and is counted as one. `degraded` is the model having been
// asked and failed. `not_asked` is the model never having been consulted.
// `cannot_tell` is an answer that parsed and says the frame did not settle it.
func absenceCauses(a store.SearchSummary) []absenceCause {
	return []absenceCause{
		{"no_objects", a.NoObjects, "asked and answered, with no object present in the frame. This IS a negative answer", "pass"},
		{"degraded", a.Degraded, "asked, and failed, late or malformed. A missing answer here is NOT a negative answer", "fail"},
		{"not_asked", a.NotAsked, "the detector proposed them and the verifier never saw them", "notrun"},
		{"cannot_tell", a.CannotTell, "answered with insufficient evidence or needs_review, which is a positive claim that the frame did not settle it", "notrun"},
	}
}

// askableCauses is the subset of the four that are MEASUREMENTS of this window.
//
// With nothing answered there are no asked rows, so `no_objects` and `cannot_tell`
// are not zeros; they are counts over a set that has no members, and a reader
// cannot tell those apart. Collapsing any two of the four produces a sentence that
// is true and useless, which is the worst property a sentence can have; showing all
// four when two of them were never counted produces a number, which is worse,
// because it looks like somebody looked.
func askableCauses(a store.SearchSummary) []absenceCause {
	if a.Answered > 0 {
		return absenceCauses(a)
	}
	out := make([]absenceCause, 0, 2)
	for _, c := range absenceCauses(a) {
		if c.name == "degraded" || c.name == "not_asked" {
			out = append(out, c)
		}
	}
	return out
}

func causeTable(causes []absenceCause) string {
	var b strings.Builder
	b.WriteString(`<table class="causes"><tbody>`)
	for _, c := range causes {
		fmt.Fprintf(&b, `<tr class="%s"><td class="name">%s</td><td class="num">%d</td><td class="why">%s</td></tr>`,
			c.cls, esc(c.name), c.count, esc(c.why))
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// absenceNotice is the one refusal this panel must never get wrong, and it is
// `printNotAbsence` on the screen.
//
// A run where every proposal was answered can carry the sentence "the model found
// nothing about X". A run where two proposals were never sent cannot, and a panel
// that prints the four counts without saying which case this is leaves an operator
// to infer it, from a table, under stress, mid-run. So the run says it in a
// sentence, in the same words the CLI uses, whenever `Unsearchable()` says so.
func absenceNotice(a store.SearchSummary) string {
	if !a.Unsearchable() {
		return ""
	}
	unsearchable := a.NotAsked + a.Degraded
	return fmt.Sprintf(`<div class="nodata"><b>THIS RUN IS NOT FULLY SEARCHABLE.</b> `+
		`%d of the %d proposals in it were never answered (%d not asked, %d degraded). `+
		`An answer that is absent here is not evidence the model saw nothing: the run did not ask, `+
		`and that is not the same as the model having answered 'no'.</div>`,
		unsearchable, a.Proposed, a.NotAsked, a.Degraded)
}

// absencePointer is the one thing this panel asks for, and it is a command.
//
// Not a link, because there is nothing to link to: the screen is read-only and the
// answers are in the store, not in the page. And it names the recorded window rather
// than a video file, because there is no continuous archive to pull and offering
// one would be an offer that fails when it is taken.
func absencePointer(d Data) string {
	cmd := "pocctl search --data-root \"" + filepath.Dir(d.DBPath) + "\""
	if d.Run != nil && d.Run.ID != "" {
		cmd += " --run " + d.Run.ID
	}
	cmd += " --query <text>"
	return fmt.Sprintf(`<div class="absent-more">%s &mdash; the answers themselves, each one carrying the `+
		`recorded window it is about: the clip reference, its exact bounds and its sha256. There is no `+
		`continuous video file to pull instead, because none is written.</div>`, esc(cmd))
}
