package main

// Tests for the licence lookup in `fixtureprobe -detector`.
//
// This tool prints the model's licence beside every number it measures, and it
// used to read a structure that does not exist: it looked for `entries[]` with a
// `licence` per entry inside `testdata/labels.json`, and the real table has
// `clips[]` whose licence field belongs to FOOTAGE, not to a model. So the
// function returned "" on every machine for every model while a recorded licence
// sat in `pyworker/fixtures/` — a check that cannot fire reads as a control, and
// this file is the test that says it fires.
//
// These tests run with the module root as the working directory, the way the
// command does, because the lookup reads a path relative to it on purpose: the
// record travels with the repository and not with the working directory.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheRecordedLicenceIsFoundBeneathTheWeightsItDescribes is the whole defect:
// a recorded licence must reach the screen that prints the model's numbers.
func TestTheRecordedLicenceIsFoundBeneathTheWeightsItDescribes(t *testing.T) {
	records, err := modelLicenceRecords()
	if err != nil {
		t.Skipf("%v; the lookup has nothing to read on this layout", err)
	}
	// The record declares the file it describes; the lookup indexes by exactly
	// that field, so a reader can see the two agree rather than trusting a
	// hardcoded name.
	found := false
	for path := range records {
		if strings.HasSuffix(path, ".onnx") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the licence records name no weights file, so `fixtureprobe -detector` has nothing "+
			"to match a --model path against: %v", records)
	}
	for path, licence := range records {
		if strings.TrimSpace(licence) == "" {
			t.Errorf("%s declares an EMPTY licence. An empty field is an unrecorded fact and it is "+
				"printed as NOT RECORDED, which is the honest answer — but the record should not be "+
				"left empty either", path)
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(licence)), "NOT RECORDED") &&
			len(strings.TrimSpace(licence)) < 20 {
			t.Errorf("%s declares a bare NOT RECORDED. An unrecorded licence is allowed to be "+
				"unrecorded; a record that says nothing else is not a record of anything", path)
		}
	}
}

// TestTheLicenceLookupMatchesTheModelsOwnRecord drives the lookup itself, through
// both spellings of the same file: the path the record declares, and the path an
// operator types from the module root.
func TestTheLicenceLookupMatchesTheModelsOwnRecord(t *testing.T) {
	records, err := modelLicenceRecords()
	if err != nil {
		t.Skipf("%v", err)
	}
	for path, licence := range records {
		if !strings.HasSuffix(path, ".onnx") || strings.TrimSpace(licence) == "" {
			continue
		}
		if got := licenceOf(path); got != licence {
			t.Errorf("licenceOf(%q) = %q, want the record's own licence for that file: %q", path, got, licence)
		}
		if got := licenceOf("models/" + filepath.Base(path)); got != licence {
			t.Errorf("licenceOf(%q) = %q, want %q. An operator types `--model models/x.onnx` from the "+
				"module root, and the record says `models/x.onnx`, so the lookup has to cover it",
				"models/"+filepath.Base(path), got, licence)
		}
		return // one record is the case under test
	}
	t.Skip("no record with a non-empty licence, so the lookup has nothing positive to match")
}

// TestAModelWithNoRecordReportsNothingRatherThanADefault is the other half: the
// lookup must not invent MIT, or Apache-2.0, or anything else for a model whose
// provenance nobody wrote down. NOT RECORDED is the answer, and the caller prints
// it.
func TestAModelWithNoRecordReportsNothingRatherThanADefault(t *testing.T) {
	if got := licenceOf("models/weights-nobody-recorded.onnx"); got != "" {
		t.Errorf("licenceOf for an unrecorded model returned %q. A default licence is a claim nobody "+
			"made", got)
	}
}

// TestTheOldLookupCouldNotHaveWorked pins the shape of the defect, so that a
// future edit which reintroduces a lookup against `testdata/labels.json` fails
// here instead of on the podium.
//
// The table's clips carry `bytes`, `provenance` and a licence about FOOTAGE. It has
// no `entries[]`, and no model licence anywhere in it.
func TestTheOldLookupCouldNotHaveWorked(t *testing.T) {
	raw, err := os.ReadFile(labelsFixturePath())
	if err != nil {
		t.Skipf("%v", err)
	}
	s := string(raw)
	if strings.Contains(s, `"entries"`) {
		t.Fatal("testdata/labels.json now holds an `entries` structure. If a model's licence belongs " +
			"in it after all, update licenceOf and the record it reads, and delete this test's claim")
	}
}

func labelsFixturePath() string {
	for _, c := range []string{
		filepath.Join("pyworker", "..", "testdata", "labels.json"),
		filepath.Join("..", "..", "testdata", "labels.json"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return filepath.Join("testdata", "labels.json")
}
