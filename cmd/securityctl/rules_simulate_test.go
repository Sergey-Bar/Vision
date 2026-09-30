package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// The plan's P4.2: "The rules simulate path runs with the AI plane killed and
// shows the degraded path."
//
// The machinery was already there - the simulator records `Degraded` and
// `DegradedReason` per outcome, and the per-change lines already print them. What
// was missing is that a *first* run has no baseline, so it prints no change
// lines, so the claim was invisible exactly when somebody first runs the command
// to check it. That is what these tests pin.
//
// The third one is the substance: degrading an observation is not the same as
// suppressing the incident, and a bundle whose incident count silently tracks
// model availability is a bundle a customer would misread as their site getting
// quieter.

func runSimulate(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	env := &command.Env{Stdin: strings.NewReader(""), Stdout: &buf, Stderr: &buf}

	// Absolute paths for both inputs, so the test does not depend on the runner's
	// working directory. The bundle is named rather than defaulted: a simulation
	// of whichever bundle happens to be lying around is not a reproducible claim,
	// and the activation gate binds to the candidate.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	code := newRootCommand().Execute(context.Background(), env, []string{
		"rules", "simulate",
		"--bundle", filepath.Join(root, "rules", "builtin", "bundle.yaml"),
		"--fixtures", filepath.Join(root, "testdata", "fixtures"),
	})
	if code != command.ExitOK {
		t.Fatalf("rules simulate returned %d\n%s", code, buf.String())
	}
	return buf.String()
}

// flatten collapses runs of whitespace, so a phrase can be matched whether or
// not the terminal wrapped it. A test that matches a sentence across a line
// break fails every time somebody reflows a message, and then gets deleted.
func flatten(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestTheSimulationStatesTheAIPlaneIsAbsent(t *testing.T) {
	out := runSimulate(t)
	flat := flatten(out)
	if !strings.Contains(flat, "AI PLANE: absent") {
		t.Fatalf("the simulation does not say the AI plane is absent:\n%s", out)
	}
	// It has to say what that *means*, not only that it is the case, or a reader
	// has to already know the product to learn anything from the line.
	for _, want := range []string{
		"degraded observation",
		"UNVERIFIED rather than blocked",
		"no rule treats the absence as a negative answer",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the simulation's AI-plane statement does not say %q:\n%s", want, out)
		}
	}
}

func TestTheSimulationCountsItsDegradationsByReason(t *testing.T) {
	out := runSimulate(t)
	flat := flatten(out)
	// The count line is what makes the claim checkable rather than decorative.
	if !strings.Contains(flat, "outcome(s) degraded") {
		t.Errorf("the simulation does not count its degraded outcomes:\n%s", out)
	}
	// A degradation with no reason is the failure this repository treats as its
	// worst, so the report says so rather than printing a count.
	if strings.Contains(flat, "WARNING") {
		t.Errorf("a simulation outcome was degraded with no reason:\n%s", out)
	}
	// The reasons themselves must be named. `no_runtime` and `absent` are
	// deliberately different facts and the report must not collapse them.
	if !strings.Contains(flat, "degraded (") {
		t.Errorf("the simulation reports no degraded outcomes with their reasons, which cannot "+
			"be right with no model configured:\n%s", out)
	}
}

func TestTheSimulationStillRaisesIncidentsWithThePlaneKilled(t *testing.T) {
	out := runSimulate(t)
	// The count is on a line of its own, so it is read without flattening.
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "incident(s) raised,")
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[:i])
		if len(fields) < 2 {
			t.Errorf("the incident count line is not a count: %q", line)
			return
		}
		raised, err := strconv.Atoi(fields[0])
		if err != nil {
			t.Errorf("the incident count is not a number: %q", line)
			return
		}
		if raised == 0 {
			t.Errorf("no incident was raised with the AI plane absent.\n"+
				"A rule that stops raising when the model is down is a rule whose incident count "+
				"silently tracks model availability, and a customer watching that count would read "+
				"it as a change in their site's activity:\n%s", out)
		}
		return
	}
	t.Errorf("the simulation does not report how many incidents it raised:\n%s", out)
}

// TestTheSimulateFlagsActuallyApply is the red-check for a bug this command had.
//
// `rules simulate` declared its flags twice: once in the command tree, which the
// dispatcher parses, and again in its own Run, which then received an empty
// argument list. Every flag was consumed by the tree and arrived nowhere, so
// `--bundle` was silently ignored and the simulation always ran against the
// default bundle.
//
// The consequence was not cosmetic. The activation digest is computed over the
// candidate bundle and `rules activate` refuses anything the report does not
// match, so an operator who simulated a candidate was handed a digest for the
// *default* bundle, reviewed that, and activated a bundle nobody had simulated -
// and the gate agreed, because the report was internally consistent about the
// wrong file.
func TestTheSimulateFlagsActuallyApply(t *testing.T) {
	var buf bytes.Buffer
	env := &command.Env{Stdin: strings.NewReader(""), Stdout: &buf, Stderr: &buf}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	// A fixtures directory that does not exist. If the flag applies, the command
	// says so; if it is swallowed, the command falls back to the default
	// directory, finds the real fixtures, and succeeds - which is how this bug
	// survived every existing test.
	code := newRootCommand().Execute(context.Background(), env, []string{
		"rules", "simulate",
		"--bundle", filepath.Join(root, "rules", "builtin", "bundle.yaml"),
		"--fixtures", filepath.Join(root, "testdata", "no-such-fixture-set"),
	})
	if code == command.ExitOK {
		t.Fatalf("the simulation ignored --fixtures and succeeded against the default set.\n"+
			"A flag that is accepted and not applied is worse than a flag that is refused.\n%s",
			buf.String())
	}
	if !strings.Contains(flatten(buf.String()), "no-such-fixture-set") {
		t.Errorf("the failure does not name the directory it was given, so an operator cannot tell "+
			"a bad path from a missing one:\n%s", buf.String())
	}
}
