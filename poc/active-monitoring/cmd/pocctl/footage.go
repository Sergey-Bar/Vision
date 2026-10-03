package main

// footage.go - the licence gate and the sampling window for `pocctl footage`.
//
// # Why the gate is a separate file with no model in it
//
// The gate has one job and it is the job the rest of the module cannot do for
// itself: decide whether this project is ALLOWED to hold a number derived from
// this material. A measurement taken over footage whose licence nobody recorded
// is not a weaker measurement; it is a number that may not be quoted, printed in
// a report, or shown to a judge. So the gate is pure - no session, no frame, no
// ffmpeg - and it runs to completion before a single pixel is decoded.
//
// It is pure so a test can drive every refusal in a table without a model and
// without a clip. A gate that needed the thing it gates would be a gate that
// could not be exercised on the case that matters most, which is "refuse this".
//
// # The four refusals, and what each one stops
//
//  1. No row in testdata/labels.json for this material. Provenance that nobody
//     wrote down is not provenance.
//  2. An absent `licence`. Empty is refused rather than defaulted, for the same
//     reason: a licence field filled from what would be convenient to claim is
//     worse than an absent one, because an absent one is visible.
//  3. A licence reading `NOT RECORDED`. This is checked as a PREFIX and the reason
//     is worth stating: the real row in this repository reads "NOT RECORDED, and
//     it is not recoverable from this file", so an equality test would let the
//     one row that matters through. A sentence that BEGINS by saying nothing was
//     recorded is a declaration that nothing was recorded.
//  4. An absent `cleared_for_external_use`, or one that is false.
//
// Refusal 4 is a POINTER, not a bool, and that is the entire control. A `bool`
// field cannot tell "cleared: false" from "this row never answered", and those
// two are opposite answers. On this tree the difference decides the whole
// measurement: `car-crossing.mp4` and `bird-synthetic.mp4` carry a recorded
// licence and no clearance field at all, so a bool would let them through and
// the pointer refuses them.
//
// # The window
//
// A sampling interval longer than 1.5 s is refused, and the reason is a property
// of the product rather than a taste: the smallest object the system exists to
// report is on screen for about that long. Sample a 4-second road at a 3-second
// interval and a pedestrian is in two of your four samples - and if they are in
// neither, you report a clean road over material where somebody was standing.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxSamplingInterval is the longest gap between sampled frames this command
// accepts.
//
// 1.5 s, from the duration of the smallest object this system reports. It is a
// refusal and not a warning because the measurement it protects against is the
// most convincing kind of wrong: four frames of an empty road look exactly like a
// detector that works.
const maxSamplingInterval = 1500 * time.Millisecond

// footageLabel is one row of the `clips` array in testdata/labels.json.
//
// Only the fields the gate reads are declared. A gate that parsed the whole row
// would be able to be confused by a note, and the notes in this file are long
// prose written for a person.
type footageLabel struct {
	File       string `json:"file"`
	Provenance string `json:"provenance"`
	Licence    string `json:"licence"`
	// Cleared is a POINTER. See the file comment: this is the control that
	// distinguishes "the row says no" from "the row never said".
	Cleared *bool `json:"cleared_for_external_use"`
	// ClearedNote is carried so the refusal can quote the row's own reason rather
	// than a sentence this file invented.
	ClearedNote string `json:"cleared_for_external_use_note"`
	Attribution string `json:"attribution"`
	SHA256      string `json:"sha256"`
}

// footageLabels is the shape of testdata/labels.json.
type footageLabels struct {
	Clips []footageLabel `json:"clips"`
}

// readFootageLabels reads the provenance table.
//
// A file that is not readable is a refusal rather than an empty table, because an
// empty table would be read downstream as "no material has a licence", which is
// a claim about the world rather than about this command's failure to find a
// file.
func readFootageLabels(path string) ([]footageLabel, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the provenance table is not readable: %w. Without it no material in this "+
			"project may be measured, because a measurement over footage whose terms nobody wrote down "+
			"is a number nobody may quote", err)
	}
	var file footageLabels
	if err := jsonUnmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("%s is not readable as a provenance table: %w", path, err)
	}
	return file.Clips, nil
}

// resolveLabel finds the one row that describes MATERIAL.
//
// Exact match on the recorded path first, and base-name match only if no exact
// match exists. The order matters: `testdata/youtube/sTF-6_xinUU.mp4` and
// `testdata/youtube2/eO19UTm93GQ.mp4` have different base names, so neither is
// ambiguous, while a material named only by its base name could match a row in
// either directory.
//
// Zero matches and two matches are both refusals, and they are different
// sentences because they need different work from the operator: one needs the
// row written, the other needs the operator to say which file they meant.
func resolveLabel(clips []footageLabel, material string) (footageLabel, error) {
	wantSlash := filepath.ToSlash(material)
	for _, c := range clips {
		if filepath.ToSlash(c.File) == wantSlash {
			return c, nil
		}
	}
	base := filepath.Base(material)
	var byBase []footageLabel
	for _, c := range clips {
		if filepath.Base(filepath.ToSlash(c.File)) == base {
			byBase = append(byBase, c)
		}
	}
	switch len(byBase) {
	case 0:
		return footageLabel{}, fmt.Errorf("no row in the provenance table describes %q. The table holds %d row(s) "+
			"(%s). A material with no provenance here is a material whose numbers may not be quoted, so this is "+
			"refused rather than measured and labelled later", material, len(clips), clipNames(clips))
	case 1:
		return byBase[0], nil
	default:
		names := make([]string, 0, len(byBase))
		for _, c := range byBase {
			names = append(names, c.File)
		}
		sort.Strings(names)
		return footageLabel{}, fmt.Errorf("%q is ambiguous: %d rows in the provenance table have that base name (%s). "+
			"This command will not pick one for you, because the two rows carry different licences and picking the "+
			"wrong one measures material under terms that were never granted. Pass the full relative path",
			base, len(names), strings.Join(names, ", "))
	}
}

func clipNames(clips []footageLabel) string {
	names := make([]string, 0, len(clips))
	for _, c := range clips {
		names = append(names, c.File)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "it holds none"
	}
	return strings.Join(names, ", ")
}

// licenceUnrecorded is the prefix a licence field carries when nothing was
// recorded.
//
// A prefix and not an equality test, because the row in this repository that
// matters reads "NOT RECORDED, and it is not recoverable from this file", and an
// equality test would wave the single most important row in the table through a
// gate whose whole purpose is to stop it.
const licenceUnrecorded = "NOT RECORDED"

// checkLicenceGate returns every reason MATERIAL may not be measured, or nil.
//
// All the reasons are collected rather than returned one at a time. An operator
// fixing a provenance row wants to know everything wrong with it in one go, and a
// gate that reports one problem per run turns one edit into four runs.
//
// The order is fixed and it is the order of severity, so the printed list reads
// as the reason it is refusing rather than as the order the fields happen to sit
// in the JSON.
func checkLicenceGate(c footageLabel) error {
	var why []string
	if strings.TrimSpace(c.Licence) == "" {
		why = append(why, "the row carries no licence. An absent licence is refused rather than defaulted: "+
			"a licence field filled from what would be convenient to claim is worse than an absent one, "+
			"because an absent one is visible")
	} else if strings.HasPrefix(normaliseSentence(c.Licence), licenceUnrecorded) {
		why = append(why, fmt.Sprintf("the licence reads %q. Nothing was recorded, so there are no terms to "+
			"measure under. Pass the path as it is recorded and the row's own note says what is missing",
			trimTo(c.Licence, 60)))
	}
	switch {
	case c.Cleared == nil:
		why = append(why, "the row carries no `cleared_for_external_use` field. Absent is not the same answer as "+
			"false: the first says nobody decided, and this command will not decide for them. "+
			"`car-crossing.mp4` and `bird-synthetic.mp4` are both in this state today")
	case !*c.Cleared:
		note := c.ClearedNote
		if note == "" {
			note = "the row records no reason"
		}
		why = append(why, fmt.Sprintf("the row records `cleared_for_external_use: false` (%s). This material may "+
			"be watched on this machine and may not be measured into a number anybody else can be shown", trimTo(note, 70)))
	}
	if len(why) == 0 {
		return nil
	}
	return &licenceRefusal{File: c.File, Reasons: why}
}

// licenceRefusal is the gate's error, carrying the reasons so the command can
// print them one per line instead of folding four sentences into one string that
// a terminal wraps into soup.
type licenceRefusal struct {
	File    string
	Reasons []string
}

func (r *licenceRefusal) Error() string {
	return fmt.Sprintf("%s may not be measured: %s", r.File, strings.Join(r.Reasons, "; "))
}

// ReasonLines returns the refusal as printable lines.
func (r *licenceRefusal) ReasonLines() []string { return r.Reasons }

// normaliseSentence upper-cases and collapses whitespace, so a licence field
// that has been wrapped across lines is compared the way it was written.
func normaliseSentence(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), " "))
}

// trimTo shortens a quoted field for a one-line refusal, marking that it did.
func trimTo(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// footageWindow is a validated sampling window.
type footageWindow struct {
	From  time.Duration
	To    time.Duration
	Every time.Duration
	// Declared is how many frames this window promises at this interval. It is
	// DECLARED, not measured: the reader reports what it actually got, and the
	// two differing is a finding rather than a rounding.
	Declared int
}

// Seconds is the window's length.
func (w footageWindow) Seconds() float64 { return w.To.Seconds() - w.From.Seconds() }

// validateFootageWindow refuses a window that cannot answer the question.
//
// Three refusals, and the second is the load-bearing one:
//
//   - an interval of zero or less, which is a division by zero;
//   - an interval LONGER THAN 1.5 s, because the smallest object this system
//     exists to report is on screen for about that long, and a longer interval
//     does not sample the object at all;
//   - an empty or inverted window, which samples nothing.
//
// The interval check comes before the window check so the message an operator
// gets for `--from 2 --to 8 --every 3` is about the interval, which is the thing
// they have to change.
func validateFootageWindow(from, to, every time.Duration) (footageWindow, error) {
	if every <= 0 {
		return footageWindow{}, fmt.Errorf("the sampling interval is %v. It must be positive: an interval of zero "+
			"or less does not divide", every)
	}
	if every > maxSamplingInterval {
		return footageWindow{}, fmt.Errorf("the sampling interval is %v and this command refuses anything over %v.\n"+
			"  The smallest object this system exists to report is on screen for about %v, so a longer interval "+
			"does not sample it at all. Sample the same window more often, or measure a window in which the "+
			"object is present for longer - a wider interval makes an empty road look like a clean result, which "+
			"is the most convincing kind of wrong number this module can print", every, maxSamplingInterval, maxSamplingInterval)
	}
	if to <= from {
		return footageWindow{}, fmt.Errorf("the window [%v, %v] is empty. There is nothing to sample, and a "+
			"measurement over no frames reports no numbers rather than zero of them", from, to)
	}
	return footageWindow{From: from, To: to, Every: every, Declared: declaredSamples(from, to, every)}, nil
}

// declaredSamples is how many frames a window promises at an interval.
//
// CEIL and not floor+1, because the window is HALF-OPEN: it covers [from, to). A
// six-second window sampled every half second covers 1.0, 1.5, ... 5.5 and stops
// at 6.0, which is twelve frames rather than thirteen. The reader is driven by
// ffmpeg's own `-t`, which is half-open, so declaring floor+1 would print a
// mismatch on every exact-multiple window - and a reader who sees "11 declared, 10
// read" on every run learns to ignore the row that exists to catch a real shortfall.
//
// The consequence is stated because it is the reader's business: a frame sitting
// exactly on `to` is NOT sampled. Raise `to` by one interval if the material
// matters right up to it.
func declaredSamples(from, to, every time.Duration) int {
	if every <= 0 || to <= from {
		return 0
	}
	n := math.Ceil(float64(to-from) / float64(every))
	if n < 1 {
		return 1
	}
	return int(n)
}
