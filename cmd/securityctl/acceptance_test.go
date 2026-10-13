package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/command"
)

// The acceptance report is a claim about the product, and a claim nobody runs is
// a claim nobody checks. These tests are what stop it decaying: a criterion that
// silently disappears, or one that flips to pass because its check stopped
// working, fails here.

func acceptanceReportFor(t *testing.T) acceptanceReport {
	t.Helper()
	var buf bytes.Buffer
	env := &command.Env{Stdin: strings.NewReader(""), Stdout: &buf, Stderr: &buf}
	code := newRootCommand().Execute(context.Background(), env, []string{"acceptance", "--soak", "40", "--json"})
	if code != command.ExitOK {
		t.Fatalf("acceptance returned %d\n%s", code, buf.String())
	}
	var rep acceptanceReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("the report did not parse: %v\n%s", err, buf.String())
	}
	return rep
}

// TestEveryCriterionIsReportedExactlyOnce: a duplicated number and a missing one
// are the same defect seen from two sides, and both make a report that reads as
// complete while being neither ordered nor exhaustive.
func TestEveryCriterionIsReportedExactlyOnce(t *testing.T) {
	rep := acceptanceReportFor(t)
	seen := map[string]int{}
	for _, c := range rep.Criteria {
		seen[c.ID]++
		if c.Verdict == "" {
			t.Errorf("criterion %s has no verdict; the three values are pass, fail and not-run", c.ID)
		}
		switch c.Verdict {
		case "pass", "fail", "not-run":
		default:
			t.Errorf("criterion %s has the verdict %q, which is not one of the three", c.ID, c.Verdict)
		}
		if c.Why == "" {
			t.Errorf("criterion %s has no text", c.ID)
		}
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("criterion %s is reported %d times", id, n)
		}
	}
	// The plan publishes thirteen. Fewer is an omission; more is an invention.
	if len(rep.Criteria) != 13 {
		t.Errorf("the report has %d criteria, want the plan's 13", len(rep.Criteria))
	}
	if rep.Passed+rep.Failed+rep.NotRun != len(rep.Criteria) {
		t.Errorf("the summary counts do not add up: %d + %d + %d for %d criteria",
			rep.Passed, rep.Failed, rep.NotRun, len(rep.Criteria))
	}
	if rep.Failed > 0 {
		t.Errorf("%d criterion could not be met: %v", rep.Failed, failingIDs(rep))
	}
}

func failingIDs(rep acceptanceReport) []string {
	var out []string
	for _, c := range rep.Criteria {
		if c.Verdict == "fail" {
			out = append(out, c.ID)
		}
	}
	return out
}

// TestANotRunCriterionSaysWhatItNeeds: a "not run" with no reason is a gap a
// reader has to guess at, and a gap somebody has to guess at gets filled in with
// an assumption.
func TestANotRunCriterionSaysWhatItNeeds(t *testing.T) {
	rep := acceptanceReportFor(t)
	for _, c := range rep.Criteria {
		if c.Verdict != "not-run" {
			continue
		}
		if c.Needs == "" {
			t.Errorf("criterion %s is not run and does not say what would be needed", c.ID)
		}
		if c.Evidence == "" {
			t.Errorf("criterion %s is not run and does not say what *is* built", c.ID)
		}
	}
	if rep.NotRun == 0 {
		t.Error("every criterion reports a pass. This build has no key, no live VMS and no camera, " +
			"so a full green here is the report lying rather than the product passing.")
	}
}

// TestTheReportSaysWhatAGreenDoesNotMean: the whole value of a partial report is
// the sentence that stops it being quoted as more than it is.
func TestTheReportSaysWhatAGreenDoesNotMean(t *testing.T) {
	if len(acceptanceReportFor(t).Limitations) == 0 {
		t.Error("the report passes criteria and says nothing about what a green does not mean")
	}
}

// TestTheAcceptanceReportFindsItsOwnRepository is the red-check for a bug this
// command had.
//
// The checks resolved their paths against the working directory, so running
// `securityctl acceptance` from anywhere but the repository root looked for a
// bundle two directories above the repository and reported a path error for a
// check that has nothing to do with paths - which reads as a product failure
// rather than as a misplaced command.
func TestTheAcceptanceReportFindsItsOwnRepository(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot could not find the module from the test's own directory: %v", err)
	}
	if _, err := filepath.Abs(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("repoRoot returned %q, which has no go.mod above it", root)
	}
	// And the check that uses it passes from here.
	if c := checkNeutrality(); c.Verdict != "pass" {
		t.Errorf("the neutrality check failed from the test's own directory: %s", c.Evidence)
	}
}
