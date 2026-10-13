package main

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// `pocctl search --unsearchable` is a SPELLING of `pocctl unsearchable`, and the two
// must be the same command.
//
// The spelling exists so a reader who has just been told the window is not fully
// searchable can SEE that in the same place. So it used to forward its own argv with
// only `--unsearchable` removed, and `pocctl search --unsearchable --query magpie`
// handed `--query magpie` to a subcommand whose flags are `data-root`, `run` and
// `limit`: the flag refused the command it is a spelling of. Two names for one
// command that only works if you use the one that takes no arguments is not two
// spellings.
//
// The mutation is going back to forwarding the argv.
//
// The store side of this surface is 4b and is not what this file tests; what it tests
// is that the discovery path a reader is told to take WORKS.
func TestTheUnsearchableFlagIsTheSameCommandAsTheUnsearchableCommand(t *testing.T) {
	root, runID := seedUnsearchableRun(t)

	standalone := capture(t, []string{"--data-root", root, "--run", runID}, cmdUnsearchable)
	viaFlag := capture(t, []string{"--data-root", root, "--run", runID, "--unsearchable",
		"--query", "magpie"}, cmdSearch)

	if !strings.Contains(standalone, "could not answer") {
		t.Fatalf("the standalone command printed no window list:\n%s", standalone)
	}
	if viaFlag != standalone {
		t.Fatalf("the two spellings printed different things.\nstandalone:\n%s\nvia the flag:\n%s",
			standalone, viaFlag)
	}
	if !strings.Contains(viaFlag, "not asked") {
		t.Fatalf("the window list does not name the reason:\n%s", viaFlag)
	}
	// And a search that is asked to also search is still a search, so the flag does
	// not swallow the command it lives in.
	withQuery := capture(t, []string{"--data-root", root, "--run", runID, "--query", "magpie"}, cmdSearch)
	if !strings.Contains(withQuery, "search:") {
		t.Fatalf("a plain search printed no header:\n%s", withQuery)
	}
}

// seedUnsearchableRun is a run with one asked answer and one proposal that was never
// sent, which is the state that makes the stretch list non-empty.
func seedUnsearchableRun(t *testing.T) (root, runID string) {
	t.Helper()
	root = t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	const run = "run-unsearchable-1"
	if err := st.StartRun(ctx, store.Run{
		ID: run, Label: "unsearchable", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(10, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	putDetection(t, st, run, "d-asked", at(10, 5))
	putObservation(t, st, run, "d-asked", "asked_answered", "alarm",
		"confirmed", "a magpie on the fence")
	putDetection(t, st, run, "d-never", at(10, 6))
	return root, run
}
