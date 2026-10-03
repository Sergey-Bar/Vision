package main

// cmdFootage: measure the detector over real material, behind a licence gate.
//
// # The shape of the command
//
//	pocctl footage --clip FILE [--labels testdata/labels.json]
//	                [--at HH:MM:SS] [--for 30m] [--every 1s]
//	                [--external]
//
// `--external` is the one that matters. Without it the numbers are for this machine;
// WITH it, the command additionally refuses any material whose provenance row does not
// say it may be shown to somebody else. A number printed on a laptop is a local claim.
// A number in a submission is an external act, and the tool should know the difference
// rather than the operator remembering it.
//
// # What it refuses, and why the refusals are the feature
//
//   - no provenance row: nothing recorded, so nothing quotable
//   - `licence` empty or absent: an absent field is an unrecorded fact
//   - `licence` reading NOT RECORDED: a real state, and the one that blocks
//   - `cleared_for_external_use` absent: "permissive" and "nobody decided" are different
//   - a base name matching two rows with different licences: refused, not guessed
//   - a sampling interval longer than the smallest dwell: the table would carry a zero
//     for the small object and the zero would read as a detector limitation
//
// # What it does NOT do
//
// It does not fetch. Fetching is a separate question with a separate answer per source,
// and a tool that both fetches and measures makes this gate optional by accident: it is
// one argument away from being bypassed by the fetch.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func footageUsage() {
	fmt.Println(`pocctl footage - measure the detector over material you have the right to use

USAGE
  pocctl footage --clip FILE [flags]

FLAGS
  --clip PATH      the video to measure. Required.
  --labels PATH    the provenance table. Default testdata/labels.json
  --at OFFSET      where to start. MM:SS, HH:MM:SS, or seconds. Default 0
  --for DURATION   how long to measure. The same spellings. Default 30m
  --every INTERVAL how often to sample. Default 1s
  --external       the numbers are for somebody else, so refuse anything not cleared
                   to be shown outside this machine

WHY IT REFUSES
  Every number this command prints is derived from somebody's camera. A number whose
  input nobody can account for is a number nobody may quote, so the gate is in the
  TOOL rather than in a README: material with no provenance row, an absent or
  NOT RECORDED licence, or an absent cleared_for_external_use flag is refused by name.

  It also refuses a sampling interval longer than ` + fmt.Sprintf("%.1f", NoveltyDwellSeconds) + `s. An
  object on screen for that long cannot appear in frames further apart, and the
  resulting table would carry a zero for the smallest thing this system exists to
  report, which reads as a detector limitation rather than a sampling plan that
  never looked.`)
}

func cmdFootage(args []string) error {
	flags, err := flagSet("footage", map[string]string{
		"clip": "", "labels": "", "at": "0", "for": "30m", "every": "1s",
		"external": boolSpec,
	}, args)
	if err != nil {
		footageUsage()
		return err
	}
	clip := strings.TrimSpace(flags["clip"])
	if clip == "" {
		footageUsage()
		return fmt.Errorf("footage: --clip is required. This command measures material you already have; " +
			"it does not fetch, because fetching is a separate question with a separate answer per source")
	}

	// 1. THE GATE, before anything is read from the video. Refusing first means a
	// rejected file is never opened, so nothing about it is even decoded.
	labelsPath := strings.TrimSpace(flags["labels"])
	if labelsPath == "" {
		found, ferr := findLabelsTable()
		if ferr != nil {
			return ferr
		}
		labelsPath = found
	}
	labels, err := loadLabels(labelsPath)
	if err != nil {
		return err
	}
	label, err := labelFor(labels, clip)
	if err != nil {
		return err
	}
	if err := refuseUncleared(clip, label, flags["external"] == "true"); err != nil {
		return err
	}

	// 2. The window, before the file is opened, so a bad interval is reported without
	// paying for a decode.
	win, err := parseFootageWindow(flags["at"], flags["for"], flags["every"])
	if err != nil {
		return err
	}

	// 3. The file itself.
	st, err := os.Stat(clip)
	if err != nil {
		return fmt.Errorf("footage: cannot open %s: %w", clip, err)
	}
	if st.IsDir() {
		return fmt.Errorf("footage: %s is a directory, not a clip", clip)
	}
	if st.Size() == 0 {
		return fmt.Errorf("footage: %s is empty. A zero-byte file has no frames, and every number over "+
			"it would be an average over nothing", clip)
	}

	// The licence is cleared and the window is well-formed. Everything up to here can be
	// reported without having touched the material.
	fmt.Printf("footage: the gate passed\n")
	fmt.Printf("  %s\n", clip)
	fmt.Printf("  provenance table %s\n", labelsPath)
	fmt.Printf("  provenance %s, retrieved %s\n", orNone(label.Provenance, "(not recorded)"),
		orNone(label.Retrieved, "(not recorded)"))
	fmt.Printf("  licence %s\n", summariseLicence(label.Licence))
	fmt.Printf("  cleared for external use: %s\n", clearedWord(label))
	fmt.Printf("  contains people: %s\n", label.ContainsPe.Word())
	fmt.Printf("  window %s\n", win.describeWindow())
	fmt.Println()
	return fmt.Errorf("footage: the licence gate and the window both pass, and this is where the " +
		"measurement runs. It is NOT implemented yet: the remaining piece is the frame sampler and " +
		"the two-pass comparison over it, and the reason it is absent rather than half-written is " +
		"that a partial comparison prints two columns of numbers and invites a reader to compare them. " +
		"The gate, the window arithmetic and the provenance model are done and proved; see " +
		"docs/measurements.md for what still has to be measured over this material")
}

// findLabelsTable looks for the provenance table and says WHERE it found it.
//
// It searches rather than guessing one relative path, because every other subcommand in
// this binary is invoked from the module root while the binary itself lives one level
// down, and a hard-coded guess is wrong for half the invocations. It PRINTS the path it
// used: a gate that silently read a different table than the one the operator had in
// mind is a gate that gates nothing.
func findLabelsTable() (string, error) {
	var tried []string
	for _, cand := range []string{
		filepath.Join("testdata", "labels.json"),
		filepath.Join("..", "testdata", "labels.json"),
		filepath.Join("..", "..", "testdata", "labels.json"),
	} {
		tried = append(tried, cand)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("footage: no provenance table found. Looked in %s. Pass --labels with the "+
		"path, and note that this command REFUSES material with no row: a table it cannot find is a "+
		"gate that cannot run", strings.Join(tried, ", "))
}

func parseFootageWindow(at, for_, every string) (footageWindow, error) {
	a, err := parseClock(at)
	if err != nil {
		return footageWindow{}, err
	}
	f, err := parseClock(for_)
	if err != nil {
		return footageWindow{}, err
	}
	e, err := parseClock(every)
	if err != nil {
		return footageWindow{}, err
	}
	w := footageWindow{At: a, For: f, Every: e}
	return w, w.validate()
}

// summariseLicence shortens a licence field for the one line above.
//
// It prints the FIRST SENTENCE and says how much it cut, because a licence paragraph
// on a status line is unreadable and a shortened licence that does not say it was
// shortened is a quotation nobody can check.
func summariseLicence(l string) string {
	l = strings.TrimSpace(l)
	if l == "" {
		return "NOT RECORDED (the field is empty, which is not the same as permissive)"
	}
	if u := upper(l); strings.HasPrefix(u, "NOT RECORDED") {
		return "NOT RECORDED"
	}
	sentence := l
	if i := strings.IndexAny(sentence, ".!?"); i > 0 {
		sentence = sentence[:i+1]
	}
	if rest := len(l) - len(sentence); rest > 0 {
		return fmt.Sprintf("%s (%d more character(s) in the table)", sentence, rest)
	}
	return sentence
}

func clearedWord(c clipLabel) string {
	cleared, known := c.clearedForExternalUse()
	switch {
	case !known:
		return "NOT RECORDED (the field is absent)"
	case cleared:
		return "yes"
	default:
		return "no"
	}
}
