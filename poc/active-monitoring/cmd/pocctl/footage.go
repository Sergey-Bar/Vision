package main

// footage: measure the detector over real material, and refuse material whose licence
// nobody wrote down.
//
// # Why this command exists
//
// The plan's Phase 0.5 is four measurements, and three of them need a camera. This is
// the tool that takes whatever material you HAVE and produces the same four numbers, so
// the moment you point it at something you are allowed to use it works. It does not
// fetch anything. Fetching is a separate question with a separate answer per source,
// and a tool that both fetches and measures makes the licence gate optional by
// accident: the gate is one argument away from being bypassed by the fetch.
//
// # The gate, and why it is a refusal rather than a warning
//
// `testdata/labels.json` records, per clip, where it came from, what licence covers it
// and whether it is cleared to be shown to anyone. This command reads that record and
// REFUSES a clip whose entry says the licence is not recorded, or whose
// `cleared_for_external_use` is false.
//
// A warning would be useless here, because a warning is printed and then the numbers
// appear, and a reader who has six numbers on a screen does not remember a caveat above
// them. This repository's own argument applies: a measurement whose input nobody can
// account for is a number that may not be quoted, and the tool that produces it is the
// right place to enforce that rather than the reader.
//
// It is deliberately STRICT about the three fields. An entry that omits `licence` is
// refused, not treated as permissive. An omitted field is an unrecorded fact, and the
// whole point of the table is that unrecorded is a distinct state from permissive.
//
// # What it prints, and what it deliberately does not
//
// Both detection settings over the same frames, side by side, with per-class
// precision and recall for each. That comparison IS Phase 0.5.4: the same material,
// the same detector, the only difference being whether a small region was measured at
// full resolution.
//
// It does not print an mAP, and the report says so: a still set of a few hundred
// frames supports a precision and a recall and not a mean average precision.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// clipLabel is one row of `testdata/labels.json`.
//
// The fields this command reads are all declared as POINTERS. That is not pedantry:
// a missing field and a field whose value is the empty string are different facts, and
// "a licence nobody recorded" must not be indistinguishable from "a licence that records
// nothing applies".
type clipLabel struct {
	File       string `json:"file"`
	Provenance string `json:"provenance"`
	SourceURL  string `json:"source_url"`
	Licence    string `json:"licence"`
	Retrieved  string `json:"retrieved"`
	// Cleared is a POINTER because it is genuinely a tri-state in this data: `true`,
	// `false`, and ABSENT are three different answers and the field has to survive all
	// three. The real table has one row with `true`, one with `false`, and rows that
	// omit it entirely.
	Cleared *bool `json:"cleared_for_external_use"`
	// ContainsPeople is PROSE OR A BOOLEAN in this table, and that is not an
	// inconsistency to tidy away -- it is the two answers a careful labeler gives.
	// `car-crossing.mp4` says `false`, because a testsrc pattern has nobody in it. The
	// YouTube row says `unknown - a night road camera; no pedestrian was observed in the
	// frames examined, and no claim is made that none is present`, because that is what
	// the person who looked actually knows.
	//
	// Declaring it as one type broke the other: a `*bool` refused to parse the row that
	// carries the most caution in the table, and a `string` refused to parse the rows
	// that do not. Either way the whole table became unreadable, and an unreadable
	// table is a gate that gates nothing.
	ContainsPe proseOrBool `json:"contains_people"`
	SHA256     string      `json:"sha256"`
	Bytes      int64       `json:"bytes"`
	// Notes is carried and not parsed. It is where the human reasoning lives, and a
	// gate that discards it would be a gate that hides the explanation of why a row
	// says what it says.
	Notes []string `json:"notes"`
	// LocalCopy names a file on this machine that is not committed, which is how the
	// table describes material that exists but must not travel.
	LocalCopy string `json:"local_copy"`
}

// proseOrBool is a JSON value that may be `true`, `"yes"` or a paragraph.
//
// It exists because the field it reads is genuinely two things, and a struct that
// accepted only one of them would make the other rows unreadable rather than merely
// imprecise. A boolean that arrives renders as `true`/`false`; a string arrives as
// itself; and an absent field arrives empty, which the caller renders as NOT RECORDED
// rather than as a default.
type proseOrBool string

// UnmarshalJSON accepts either spelling.
//
// The error is deliberately not swallowed: a number where prose was expected is a
// mistake in the table and the reader should be told which row and which field.
func (p *proseOrBool) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "true", s == "false":
		*p = proseOrBool(s)
		return nil
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"':
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		*p = proseOrBool(str)
		return nil
	case s == "null":
		*p = ""
		return nil
	}
	return fmt.Errorf("`contains_people` must be true, false, or a sentence saying what is known; "+
		"got %s. The field is two things on purpose -- `false` for material with nobody in it, and a "+
		"sentence for material where the answer is `unknown`", s)
}

// Word renders the value for a status line, or NOT RECORDED when it is absent.
func (p proseOrBool) Word() string {
	s := strings.TrimSpace(string(p))
	if s == "" {
		return "NOT RECORDED"
	}
	return s
}

// labelsFile is `testdata/labels.json`.
type labelsFile struct {
	Clips []clipLabel `json:"clips"`
}

// loadLabels reads the provenance table and indexes it by the path it stores.
//
// The index key is the row's own `file` value rather than a base name, because the
// table stores a path (`youtube/x.mp4`) and matching on a base name would let two
// directories hold two clips with the same name and pick whichever came last.
func loadLabels(path string) (map[string]clipLabel, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("footage: cannot read the provenance table %s: %w", path, err)
	}
	var f labelsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("footage: %s is not a provenance table this build understands: %w", path, err)
	}
	out := make(map[string]clipLabel, len(f.Clips))
	for _, c := range f.Clips {
		if c.File == "" {
			continue
		}
		out[normPath(c.File)] = c
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("footage: %s lists no clips. A provenance table with nothing in it describes "+
			"nothing, and this command measures material", path)
	}
	return out, nil
}

// normPath makes a path comparable: forward slashes, no leading "./".
func normPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.TrimPrefix(p, "./")
}

// labelFor finds the row for a clip, by full path and then by base name.
//
// The base-name fallback exists because a caller names a file on a command line and the
// table stores a path relative to `testdata`. It is a FALLBACK and not the primary
// path, and when two rows share a base name the match is refused rather than resolved
// -- an ambiguous licence lookup is exactly the situation where guessing is worst.
func labelFor(labels map[string]clipLabel, path string) (clipLabel, error) {
	key := normPath(path)
	if c, ok := labels[key]; ok {
		return c, nil
	}
	base := key
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	var found []clipLabel
	for _, c := range labels {
		if baseOf(c.File) == base {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return clipLabel{}, fmt.Errorf("footage: %s has NO ROW in the provenance table. Material with no "+
			"recorded provenance produces numbers nobody may quote, so it is refused rather than "+
			"measured. Add a row recording where it came from, what licence covers it, and whether it "+
			"may be shown outside this machine", path)
	case 1:
		return found[0], nil
	default:
		names := make([]string, 0, len(found))
		for _, c := range found {
			names = append(names, c.File)
		}
		sort.Strings(names)
		return clipLabel{}, fmt.Errorf("footage: %s matches %d rows in the provenance table (%s), so "+
			"the licence lookup is ambiguous. Name the full path, or remove the duplicate row: an "+
			"ambiguous licence lookup is the one situation where guessing which licence applies is worst",
			path, len(found), strings.Join(names, ", "))
	}
}

func baseOf(p string) string {
	p = normPath(p)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// clearedForExternalUse resolves the tri-state boolean.
//
// The three states are REAL and the function says which is which, because they are
// three different answers: `true` may be shown, `false` may not, and ABSENT is a
// licence nobody wrote down. Collapsing the third into the first is the failure this
// command exists to prevent.
func (c clipLabel) clearedForExternalUse() (bool, bool) {
	if c.Cleared == nil {
		return false, false // known: NOT cleared, because nobody said otherwise
	}
	return *c.Cleared, true
}

// refuseUncleared is THE gate.
//
// It returns an error naming the file and the three fields that are wrong, because a
// refusal that says only "licence not recorded" is a complaint and a refusal that says
// which three fields to fill is a fix.
func refuseUncleared(path string, c clipLabel, wantExternal bool) error {
	var problems []string

	licence := strings.TrimSpace(c.Licence)
	if licence == "" {
		problems = append(problems, "`licence` is empty or absent, so nothing is recorded about who "+
			"granted permission. An ABSENT field is not a permissive field")
	} else if upper(licence) == "NOT RECORDED" {
		problems = append(problems, "`licence` reads NOT RECORDED. That is a real state and it is the "+
			"one that blocks a measurement: the rights holder has not been asked, so nothing is known "+
			"about what may be done with this file")
	}

	cleared, known := c.clearedForExternalUse()
	switch {
	case !known:
		problems = append(problems, "`cleared_for_external_use` is absent. It must be an explicit true "+
			"or false: \"the licence is unknown\" and \"the licence is known and permissive\" are "+
			"different answers and a reader should not have to infer which one applies")
	case wantExternal && !cleared:
		problems = append(problems, "`cleared_for_external_use` is false, so this material is explicitly "+
			"NOT cleared to leave this machine, and you asked for numbers to quote")
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("footage: %s is not cleared to be measured for quoting, and no number will be "+
		"printed for it.\n  %s\n  Fix the row in the provenance table, or point --clip at material whose "+
		"licence you can state in full: the source, the rights holder, and the permission.",
		path, strings.Join(problems, "\n  "))
}

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// parseClock reads `MM:SS`, `HH:MM:SS` or a plain number of seconds.
//
// It is a function and not a `time.Parse` because the caller is typing a video offset,
// and `15:30` for a video is thirty minutes and fifteen seconds while `time.Parse`
// would want an hour field and an hour-only layout would refuse it. A refusal that
// does not understand the obvious spelling trains people to look the format up.
func parseClock(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	// A bare number is seconds. It is a common spelling and an unambiguous one.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if f < 0 {
			return 0, fmt.Errorf("footage: %q is a negative offset", s)
		}
		return f, nil
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("footage: %q is not an offset. Use MM:SS, HH:MM:SS, or a number of "+
			"seconds", s)
	}
	var total float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0, fmt.Errorf("footage: %q has a part %q that is not a number", s, p)
		}
		// The RIGHTMOST part is seconds, so the multiplier for part i is 60 raised to
		// the number of parts to its right. Walking leftward instead -- the way the
		// multiplier reads most naturally -- turns `1:30` into 1801 seconds, which is
		// a silent thirty-fold error on the one number a caller types by hand.
		mult := 1.0
		for j := len(parts) - 1; j > i; j-- {
			mult *= 60
		}
		total += v * mult
	}
	return total, nil
}

// footageWindow is the declared region of a clip to measure.
type footageWindow struct {
	At    float64 // seconds from the start
	For   float64 // how many seconds
	Every float64 // the sampling interval
}

// validate refuses a window that cannot be measured, and says why.
//
// The dwell arithmetic is here rather than at the call site because it is the reason a
// window of 30 minutes at one frame a second is NOT automatically a good idea: at one
// frame a second a bird crossing in 1.5 s is proposed one or two times, and the whole
// crop pass's argument is that it needs three.
func (w footageWindow) validate() error {
	if w.At < 0 {
		return fmt.Errorf("footage: the offset %v is negative", w.At)
	}
	if w.For <= 0 {
		return fmt.Errorf("footage: the duration %v is not positive, so the window is empty and every "+
			"number over it would be an average over nothing", w.For)
	}
	if w.Every <= 0 {
		return fmt.Errorf("footage: the sampling interval %v is not positive", w.Every)
	}
	if w.Every > w.For {
		return fmt.Errorf("footage: sampling every %v s over a %v s window is one frame in total. The "+
			"report would be a number about a single frame with no variance beside it", w.Every, w.For)
	}
	samples := w.For / w.Every
	if samples < NoveltySamplesNeeded {
		return fmt.Errorf("footage: %v s at every %v s is %.0f sample(s), and the smallest object this "+
			"system exists to report is on screen for about %.1f s and needs %d proposals inside that. "+
			"Widen the window or sample faster, or the measurement cannot speak to the case the crop "+
			"pass was built for",
			w.For, w.Every, samples, NoveltyDwellSeconds, NoveltySamplesNeeded)
	}
	// The SECOND check, and it is the one that catches the case a count alone misses.
	//
	// Thirty minutes sampled every 300 s is SIX samples, which clears the count above and
	// is still useless: an object on screen for 1.5 s cannot be present in six frames
	// spread five minutes apart, so the table would have a row for `bird` and a zero in
	// it, and the zero would look like a detector that cannot see birds rather than a
	// sampling plan that never looked.
	//
	// The rule is therefore about the INTERVAL and not about the total: a sampling
	// interval longer than the shortest dwell cannot see a short-dwell object at all,
	// and no number over such a window may be quoted about one.
	if w.Every > NoveltyDwellSeconds {
		return fmt.Errorf("footage: sampling every %v s cannot see an object on screen for %.1f s, "+
			"which is the smallest thing this system exists to report. The window holds %d samples "+
			"and that count looks fine, which is the trap: the table would carry a row for the small "+
			"object and a zero in it, and the zero would read as a detector limitation rather than a "+
			"sampling plan that never looked. Sample at %.1f s or faster",
			w.Every, NoveltyDwellSeconds, int(samples), NoveltyDwellSeconds)
	}
	return nil
}

// The dwell figures, restated here so the window check and the printed report cannot
// disagree about them.
const (
	// NoveltyDwellSeconds is the plan's own figure for a bird at 30 m.
	NoveltyDwellSeconds = 1.5
	// NoveltySamplesNeeded is how many proposals a region needs to be reliably cropped.
	NoveltySamplesNeeded = 3
)

// describeWindow is the one line the report prints about what it measured.
//
// It carries the window, the sampling and the FRAME COUNT, because a rate over an
// unstated number of samples is a rate nobody can check.
func (w footageWindow) describeWindow() string {
	return fmt.Sprintf("%.1fs .. %.1fs, sampled every %.1fs (%d frames)",
		w.At, w.At+w.For, w.Every, int(w.For/w.Every))
}
