package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheCommittedHumanReviewIsReadable covers a defect the live demo found on
// 2026-10-15, and it was smaller than it looks.
//
// `pocctl report` scores the false-positive criterion by reading
// testdata/groundtruth.json into a STRICT-decoded struct. The committed file
// carries a `$comment` header — the convention every other table in this module
// uses — and the struct had no field for it, so every report printed
//
//	false positives: NOT RUN: testdata/groundtruth.json is not a review this
//	build understands: json: unknown field "$comment"
//
// The file was fine. The loader was the thing that could not read it, and the
// output blamed the file, which is the one reading that sends a person looking
// in the wrong place.
//
// So this test loads the COMMITTED file rather than a synthetic one: the only
// way to know the command that scores a number can read the file the number
// comes from is to read it in CI.
func TestTheCommittedHumanReviewIsReadable(t *testing.T) {
	// A `go test` runs in the PACKAGE directory, where `repoRoot()` warns that
	// this is not the module root — and it is right to warn. So the test finds
	// the module root itself, the way the other tables' tests do, and reads the
	// file by an absolute path.
	root := moduleRoot(t)
	path := filepath.Join(root, "testdata", "groundtruth.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the committed human review is not where the report looks (%s): %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the committed human review could not be read: %v", err)
	}
	var rv review
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rv); err != nil {
		t.Fatalf("the report would refuse the committed review: %v. The decoder is strict on purpose, so every key the file uses must be DECLARED", err)
	}
	if rv.Schema != 1 {
		t.Fatalf("schema = %d, want 1; the file and the loader would disagree about a shape nobody checked", rv.Schema)
	}
	// The marks are the reason the file exists, and the assertion is not a count
	// — the honest statement is that they DECODE into the review type the report
	// uses, which is what makes the criterion scoreable at all.
	for i, m := range rv.Marks {
		if strings.TrimSpace(m.Tag) == "" {
			t.Errorf("mark %d has no tag: a mark that attaches to nothing is a mark nobody can match back to a pushed event", i)
		}
		if m.Verdict != "tp" && m.Verdict != "fp" {
			t.Errorf("mark %d says %q, which is neither tp nor fp; the store refuses an unreadable verdict and the file must agree", i, m.Verdict)
		}
	}
}

// moduleRoot walks up from the test's working directory to the module's go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("no go.mod found walking up from the test's directory")
		}
		wd = parent
	}
}
