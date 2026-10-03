package main

// The licence gate against the REAL table.
//
// The unit tests beside the gate use a fixture with every shape of bad row on purpose.
// This file uses `testdata/labels.json` itself, because a gate proved only against a
// fixture is a gate proved against a fiction: the real table has fields this build did
// not anticipate, and the first run against it is how they are found.
//
// It already found one. `contains_people` is PROSE in the real table ("unknown - a
// night road camera; no pedestrian was observed in the frames examined, and no claim is
// made that none is present") and was declared as a `*bool` here, so the row carrying
// the most caution in the table failed to parse and the whole table became unreadable.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realLabels loads the committed provenance table from wherever the test is run.
func realLabels(t *testing.T) map[string]clipLabel {
	t.Helper()
	for _, cand := range []string{
		filepath.Join("..", "testdata", "labels.json"),
		filepath.Join("testdata", "labels.json"),
	} {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			m, err := loadLabels(cand)
			if err != nil {
				t.Fatalf("the committed provenance table does not parse: %v", err)
			}
			return m
		}
	}
	t.Skip("testdata/labels.json is not reachable from here; the unit gate tests still cover the rules")
	return nil
}

// TestTheRealProvenanceTableParsesAndResolvesItsOwnRows.
//
// The table is the gate's input, and a table that fails to parse is a gate that gates
// nothing -- every command that consults it either fails or, worse, is bypassed.
func TestTheRealProvenanceTableParsesAndResolvesItsOwnRows(t *testing.T) {
	labels := realLabels(t)
	if len(labels) == 0 {
		t.Fatal("the committed table parsed to zero rows, which means every lookup would be refused " +
			"and the failure would look like a missing file")
	}
	// Every row must be findable by its OWN path. A row that cannot be found by the
	// path it stores is a row that is silently unreachable, and unreachable rows are
	// how a table grows rows nobody checks.
	for key := range labels {
		if _, err := labelFor(labels, key); err != nil {
			t.Errorf("the table's own row %q does not resolve to itself: %v", key, err)
		}
	}
}

// TestEveryRealRowNamesWhetherItContainsPeople.
//
// The field is prose and the gate prints it. A row with the field ABSENT is a row
// where nobody wrote down whether a person's likeness is inside, and for a product whose
// demo would put that likeness on a projector, an unrecorded "unknown" is the answer
// that has to be visible rather than defaulted.
func TestEveryRealRowNamesWhetherItContainsPeople(t *testing.T) {
	labels := realLabels(t)
	for key, c := range labels {
		if c.ContainsPe.Word() == "NOT RECORDED" {
			t.Errorf("the row for %q does not say whether it contains people. Absent is not the same "+
				"as `false`, and for a product that puts footage of people on a screen the difference "+
				"is the whole point", key)
		}
	}
}

// TestTheUnrecordedYouTubeRowIsRefusedForExternalNumbers is the gate's real case.
//
// This row exists in the committed table with `licence` reading NOT RECORDED and
// `cleared_for_external_use: false`, and it is the row this whole command was built
// for. A gate that passed it would be a gate that does not work.
func TestTheUnrecordedYouTubeRowIsRefusedForExternalNumbers(t *testing.T) {
	labels := realLabels(t)
	var found bool
	for key, c := range labels {
		if !strings.Contains(strings.ToLower(key), "youtube") {
			continue
		}
		found = true
		if err := refuseUncleared(key, c, true); err == nil {
			t.Errorf("the committed table's YouTube row %q was accepted for external numbers. Its own "+
				"`licence` field reads NOT RECORDED and its `cleared_for_external_use` is false", key)
		} else if !strings.Contains(err.Error(), "cleared_for_external_use") {
			t.Errorf("the refusal for %q does not name the field that failed: %v", key, err)
		}
	}
	if !found {
		t.Skip("this checkout has no YouTube row in the table")
	}
}

// TestTheSyntheticRowsAreAcceptedForExternalNumbers.
//
// The gate has to be usable, or people will route around it. The generated clips have
// no rights holder to ask, which is a real answer rather than a missing one, and the
// table says so.
func TestTheSyntheticRowsAreAcceptedForExternalNumbers(t *testing.T) {
	labels := realLabels(t)
	var checked int
	for key, c := range labels {
		if !strings.EqualFold(strings.TrimSpace(c.Provenance), "synthetic") {
			continue
		}
		checked++
		if err := refuseUncleared(key, c, true); err != nil {
			t.Errorf("the generated clip %q was refused for external use: %v", key, err)
		}
	}
	if checked == 0 {
		t.Skip("this checkout has no synthetic rows")
	}
}

// TestALicenceSummarySaysWhenItHasCut: a shortened licence that does not say it was
// shortened is a quotation nobody can check.
func TestALicenceSummarySaysWhenItHasCut(t *testing.T) {
	long := "mine, I shot it on my own driveway in March. There is no third party in it at any point."
	got := summariseLicence(long)
	if !strings.Contains(got, "more character") {
		t.Errorf("a licence paragraph was shortened without saying so: %q", got)
	}
	if strings.Contains(got, "driveway in March. There is no") {
		t.Errorf("the summary did not cut at the first sentence: %q", got)
	}
	// An empty field and a NOT RECORDED field both have to read as NOT RECORDED, and
	// the empty one has to say the field was empty -- because an empty field and a
	// permissive one look identical in the JSON.
	if s := summariseLicence(""); !strings.Contains(s, "empty") {
		t.Errorf("an empty licence summarised as %q", s)
	}
	if s := summariseLicence("NOT RECORDED, and not recoverable"); s != "NOT RECORDED" {
		t.Errorf("a NOT RECORDED licence summarised as %q", s)
	}
	// A one-sentence licence is not cut, so it must not claim to have been.
	short := "CC0."
	if s := summariseLicence(short); strings.Contains(s, "more character") {
		t.Errorf("a one-sentence licence claimed to have been cut: %q", s)
	}
}

// TestClearedWordNamesAllThreeStates: the field is a tri-state and a status line that
// renders two of them is a status line that lies about the third.
func TestClearedWordNamesAllThreeStates(t *testing.T) {
	yes, no := true, false
	for _, c := range []struct {
		name string
		row  clipLabel
		want string
	}{
		{"true", clipLabel{Cleared: &yes}, "yes"},
		{"false", clipLabel{Cleared: &no}, "no"},
		{"absent", clipLabel{}, "NOT RECORDED"},
	} {
		if got := clearedWord(c.row); !strings.HasPrefix(got, c.want) {
			t.Errorf("clearedWord for %s = %q, want %q", c.name, got, c.want)
		}
	}
}
