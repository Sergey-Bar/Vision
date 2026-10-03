package main

// The licence gate, and the window arithmetic.
//
// The gate is the reason this command exists, so its tests are the ones that assert
// the REFUSALS rather than the acceptances. A test that a good row passes is worth
// much less than a test that an empty field is refused, because the acceptances are
// the path nobody takes by accident and the refusals are the ones that protect
// somebody else's footage.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLabels = `{
  "$comment": "a table with one good row and every shape of bad row this gate exists to catch",
  "clips": [
    {
      "file": "mine/car-park.mp4",
      "provenance": "real",
      "source_url": "my own phone, on my own driveway",
      "licence": "mine. I shot it and I hold the rights.",
      "retrieved": "2026-10-03",
      "cleared_for_external_use": true,
      "contains_people": false,
      "sha256": "aa11"
    },
    {
      "file": "mine/no-licence.mp4",
      "provenance": "real",
      "source_url": "a friend's camera",
      "retrieved": "2026-10-03",
      "cleared_for_external_use": true
    },
    {
      "file": "mine/not-recorded.mp4",
      "provenance": "real",
      "licence": "NOT RECORDED, and it is not recoverable from this file",
      "retrieved": "2026-10-03",
      "cleared_for_external_use": false
    },
    {
      "file": "mine/no-cleared-field.mp4",
      "provenance": "real",
      "licence": "CC-BY 4.0, the photographer is named in the file's description",
      "retrieved": "2026-10-03"
    },
    {
      "file": "dup/a.mp4",
      "provenance": "real",
      "licence": "CC0",
      "retrieved": "2026-10-03",
      "cleared_for_external_use": true
    },
    {
      "file": "dup2/a.mp4",
      "provenance": "real",
      "licence": "all rights reserved",
      "retrieved": "2026-10-03",
      "cleared_for_external_use": true
    }
  ]
}`

func loadSampleLabels(t *testing.T) map[string]clipLabel {
	t.Helper()
	path := writeTemp(t, "labels.json", sampleLabels)
	m, err := loadLabels(path)
	if err != nil {
		t.Fatalf("the sample table does not load: %v", err)
	}
	return m
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestARowWithEverythingRecordedIsAccepted: the one path that produces numbers.
func TestARowWithEverythingRecordedIsAccepted(t *testing.T) {
	labels := loadSampleLabels(t)
	c, err := labelFor(labels, "mine/car-park.mp4")
	if err != nil {
		t.Fatalf("a fully recorded row was not found: %v", err)
	}
	if err := refuseUncleared("mine/car-park.mp4", c, true); err != nil {
		t.Fatalf("a fully recorded, explicitly cleared row was refused: %v", err)
	}
}

// TestAnAbsentLicenceFieldIsRefusedAndNotTreatedAsPermissive is the control.
//
// An absent field is an unrecorded fact. Treating it as permissive because the JSON
// did not object is the exact shape of the defect this repository exists to end: a
// missing record read as a clean bill of health.
func TestAnAbsentLicenceFieldIsRefusedAndNotTreatedAsPermissive(t *testing.T) {
	labels := loadSampleLabels(t)
	c, err := labelFor(labels, "mine/no-licence.mp4")
	if err != nil {
		t.Fatal(err)
	}
	err = refuseUncleared("mine/no-licence.mp4", c, true)
	if err == nil {
		t.Fatal("a row with no `licence` field was accepted. An absent field is an unrecorded fact and " +
			"must not read as a permissive one")
	}
	if !strings.Contains(err.Error(), "licence") {
		t.Fatalf("the refusal does not name the field: %v", err)
	}
	// And the message says which fields to fill, because a refusal that only complains
	// is a complaint.
	if !strings.Contains(err.Error(), "rights holder") {
		t.Errorf("the refusal does not say what to record: %v", err)
	}
}

// TestALicenceReadingNotRecordedIsRefused: the real state, stated in words.
func TestALicenceReadingNotRecordedIsRefused(t *testing.T) {
	labels := loadSampleLabels(t)
	c, _ := labelFor(labels, "mine/not-recorded.mp4")
	err := refuseUncleared("mine/not-recorded.mp4", c, true)
	if err == nil {
		t.Fatal("a row whose licence reads NOT RECORDED was accepted")
	}
	if !strings.Contains(err.Error(), "cleared_for_external_use") {
		t.Errorf("the refusal does not mention the second failing field: %v", err)
	}
}

// TestAnAbsentClearedFlagIsRefused: three states, and the third is the dangerous one.
func TestAnAbsentClearedFlagIsRefused(t *testing.T) {
	labels := loadSampleLabels(t)
	c, _ := labelFor(labels, "mine/no-cleared-field.mp4")
	err := refuseUncleared("mine/no-cleared-field.mp4", c, true)
	if err == nil {
		t.Fatal("a row with a permissive LICENCE but no `cleared_for_external_use` was accepted. " +
			"\"the licence is permissive\" and \"nobody decided whether it may be shown\" are different " +
			"answers")
	}
	// For a LOCAL measurement the absent flag is still refused: the flag is about
	// external use, and this command's output is quotable, which is an external act.
	if _, known := c.clearedForExternalUse(); known {
		t.Error("an absent flag resolved as known")
	}
}

// TestMaterialWithNoRowAtAllIsRefused: nothing recorded, nothing measured.
func TestMaterialWithNoRowAtAllIsRefused(t *testing.T) {
	labels := loadSampleLabels(t)
	_, err := labelFor(labels, "mine/never-seen-before.mp4")
	if err == nil {
		t.Fatal("material with no provenance row was resolved. A file whose provenance nobody recorded " +
			"produces numbers nobody may quote")
	}
	if !strings.Contains(err.Error(), "provenance") {
		t.Errorf("the refusal does not name the table: %v", err)
	}
}

// TestAnAmbiguousLookupIsRefusedRatherThanGuessed.
//
// Two rows with the same base name and DIFFERENT licences is the situation where
// picking one is worst, because the tool would silently apply whichever it found.
func TestAnAmbiguousLookupIsRefusedRatherThanGuessed(t *testing.T) {
	labels := loadSampleLabels(t)
	if _, err := labelFor(labels, "somewhere-else/a.mp4"); err == nil {
		t.Fatal("a base name matching two rows was resolved to one of them. That is a licence lookup " +
			"resolved by coin toss")
	} else if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("the refusal does not say the lookup was ambiguous: %v", err)
	}
	// And naming the full path resolves it, which is the remedy.
	if _, err := labelFor(labels, "dup/a.mp4"); err != nil {
		t.Fatalf("naming the full path did not resolve the lookup: %v", err)
	}
}

// TestParseClockReadsTheSpellingsPeopleType: an offset in a video is MM:SS, and a
// parser that wants HH:MM:SS trains people to look the format up.
func TestParseClockReadsTheSpellingsPeopleType(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
	}{
		{"", 0},
		{"0", 0},
		{"90", 90},
		{"90.5", 90.5},
		{"1:30", 90},
		{"00:01:30", 90},
		{"01:00:00", 3600},
		{"2:05:03", 2*3600 + 5*60 + 3},
		{"  1:30  ", 90},
	} {
		got, err := parseClock(c.in)
		if err != nil {
			t.Errorf("parseClock(%q) failed: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseClock(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"-5", "abc", "1:2:3:4", "1:xx", "1:"} {
		if _, err := parseClock(bad); err == nil {
			t.Errorf("parseClock(%q) was accepted", bad)
		}
	}
}

// TestAWindowTooSparseToSpeakAboutTheCaseIsRefused.
//
// Thirty minutes sampled once a second is a lot of frames; thirty minutes sampled once
// a minute is not, and the specific failure is that a bird on screen for 1.5 s gets one
// or two samples, which is the case the crop pass exists for. The check refuses it by
// name rather than producing a table with a row for `bird` and a zero in it.
func TestAWindowTooSparseToSpeakAboutTheCaseIsRefused(t *testing.T) {
	// A healthy window.
	good := footageWindow{At: 0, For: 1800, Every: 1}
	if err := good.validate(); err != nil {
		t.Fatalf("a 30-minute window at 1 fps was refused: %v", err)
	}
	// One frame in total.
	if err := (footageWindow{At: 0, For: 30, Every: 60}).validate(); err == nil {
		t.Error("a window yielding a single frame was accepted. A rate over one frame has no variance " +
			"beside it and is a claim about one picture")
	}
	// The specific failure: too few samples of a short dwell.
	thin := footageWindow{At: 0, For: 1800, Every: 300}
	err := thin.validate()
	if err == nil {
		t.Fatal("30 minutes sampled every 300 s was accepted")
	}
	if !strings.Contains(err.Error(), "1.5 s") && !strings.Contains(err.Error(), "proposals") {
		t.Errorf("the refusal does not say which case it is protecting: %v", err)
	}
	// And the degenerate windows.
	for _, w := range []footageWindow{
		{At: -1, For: 10, Every: 1},
		{At: 0, For: 0, Every: 1},
		{At: 0, For: 10, Every: 0},
		{At: 0, For: 10, Every: -1},
	} {
		if err := w.validate(); err == nil {
			t.Errorf("the degenerate window %+v was accepted", w)
		}
	}
}

// TestTheWindowDescriptionCarriesTheFrameCount: a rate over an unstated sample count is
// a rate nobody can check.
func TestTheWindowDescriptionCarriesTheFrameCount(t *testing.T) {
	d := footageWindow{At: 600, For: 1800, Every: 2}.describeWindow()
	for _, want := range []string{"600.0", "2400.0", "2.0", "900"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description %q does not carry %q", d, want)
		}
	}
}
