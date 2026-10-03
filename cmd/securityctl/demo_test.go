package main

// `securityctl demo` is registered and not populated, so the tests here are about
// one property: **it must not look like it works.**
//
// That is the whole argument for the command existing in this shape. A skeleton
// that exits 0 satisfies every script that reads the exit code, and this
// repository has a list of things that looked fine for exactly that reason —
// `slo.Breaches`, an SLO evaluator with a test and no caller, and a verification
// run that reported exit 0 with 273 tests skipped.

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// runDemoCLI runs the real command tree, not the function, because the exit code
// is produced by the command layer's error mapping rather than by demo's return.
func runDemoCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := newRootCommand().Execute(context.Background(), &command.Env{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
	}, args)
	return int(code), stdout.String(), stderr.String()
}

func TestDemoExitsNonZeroBecauseItDidNotDemonstrate(t *testing.T) {
	code, _, _ := runDemoCLI(t, "demo")
	if code == 0 {
		t.Error("demo exited 0 without demonstrating anything. Every script that reads the exit " +
			"code would record a working demo that never ran")
	}
}

// TestDemoSaysSoOnEveryStream: an operator who only reads stdout, or only stderr,
// must still be told. A message on the other stream is a message nobody sees.
func TestDemoSaysSoOnEveryStream(t *testing.T) {
	_, stdout, stderr := runDemoCLI(t, "demo")
	if !strings.Contains(stdout, "When it is populated") {
		t.Error("stdout does not say the command is a placeholder")
	}
	if !strings.Contains(stderr, "RESERVED") {
		t.Error("stderr does not say the command is a placeholder")
	}
	if !strings.Contains(stderr, "non-zero") {
		t.Error("stderr does not say why the exit code is what it is")
	}
}

func TestDemoJSONReportsPopulatedFalseAndExitsZero(t *testing.T) {
	code, stdout, _ := runDemoCLI(t, "demo", "--json")
	if code != 0 {
		t.Errorf("demo --json exited %d; it reports rather than demonstrates, so it exits 0", code)
	}
	var st demoState
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatalf("the JSON is not parseable, so a consumer cannot rely on it: %v\n%s", err, stdout)
	}
	// `populated` is a field rather than an omission, because a consumer reading
	// JSON has to distinguish "not implemented" from "implemented and reported
	// nothing". An absent field is the same defect as a green run that tested
	// nothing.
	if st.Populated {
		t.Error(`"populated": true, from a command that did nothing`)
	}
	if st.Command != "demo" || st.Phase != "P0" {
		t.Errorf("the state does not identify itself: %+v", st)
	}
	if len(st.WillDo) == 0 {
		t.Error("the JSON lists nothing the command will do")
	}
}

// TestDemoPlanListsTheAcceptanceCriteria. `--plan` is the one flag that makes P6's
// bar legible without the plan document open, so it has to actually carry it.
func TestDemoPlanListsTheAcceptanceCriteria(t *testing.T) {
	code, stdout, _ := runDemoCLI(t, "demo", "--plan")
	if code != 0 {
		t.Errorf("demo --plan exited %d; it reports rather than demonstrates", code)
	}
	for i := 1; i <= len(demoCriteria); i++ {
		if !strings.Contains(stdout, "  "+strconv.Itoa(i)+". ") {
			t.Errorf("criterion %d is not numbered in the output:\n%s", i, stdout)
		}
	}
	if !strings.Contains(stdout, "None of these is met yet") {
		t.Error("the plan output does not say that none of the criteria is met. A list of six " +
			"requirements above a command that satisfies none of them reads as a spec in progress " +
			"rather than as an unmet gate")
	}
	if !strings.Contains(stdout, "docs/status.md") {
		t.Error("the plan output does not point at where the build state is stated")
	}
}

// TestDemoCriteriaCoverTheThreeThingsThatMatter: the pipeline runs, the incident
// closes with its audit rows, and the North Star numerator is the query's number
// rather than a computed one. Those are the three claims a demo makes that a
// transcript alone cannot support.
func TestDemoCriteriaCoverTheThreeThingsThatMatter(t *testing.T) {
	joined := strings.ToLower(strings.Join(demoCriteria, " "))
	for _, want := range []string{"clock corrector", "hash-chained audit rows", "query returns"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no acceptance criterion mentions %q, which is one of the three claims a demo "+
				"makes that a transcript cannot support", want)
		}
	}
}
