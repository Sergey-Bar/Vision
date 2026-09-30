package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// The plan's §14 extras, on the demonstration path.
//
// "Replay determinism with zero provider calls" and "A stubbed-provider fault
// matrix" are both claims a judge makes about any system that costs money per
// query, and both are cheap to state and easy to get wrong. They are here as
// failing tests.

// TestReplayIsDeterministicAndSpendsNothing is the first.
//
// `poc` has no provider wired in, so the "zero provider calls" half is structural
// and what is actually being tested is determinism: the same bytes must produce
// the same clip, the same hash and the same incident, run after run and on a
// different day. A demo that produces a different evidence hash every time is a
// demo whose artefacts nobody can compare, and an incident id that changes
// between runs is a retry that looks like a second event.
func TestReplayIsDeterministicAndSpendsNothing(t *testing.T) {
	dir := t.TempDir()
	args := []string{
		"--stream", "../../testdata/poc/testsrc.h264",
		"--out", dir, "--json",
		"--pre-roll", "4s", "--post-roll", "3s", "--max-time", "20s",
	}
	first := runPocReportForTest(t, args...)
	second := runPocReportForTest(t, args...)

	if first.Clip == nil || second.Clip == nil {
		t.Fatalf("a replay produced no clip")
	}
	// The bytes and their hash.
	if first.Clip.Bytes != second.Clip.Bytes {
		t.Errorf("the clip length changed between runs: %d then %d", first.Clip.Bytes, second.Clip.Bytes)
	}
	if first.Evidence == nil || second.Evidence == nil {
		t.Fatal("a replay stored no evidence")
	}
	if first.Evidence.SHA256 != second.Evidence.SHA256 {
		t.Errorf("the evidence hash changed between runs:\n %s\n %s",
			first.Evidence.SHA256, second.Evidence.SHA256)
	}
	if first.Evidence.StorageRef != second.Evidence.StorageRef {
		t.Errorf("the storage reference changed between runs:\n %s\n %s",
			first.Evidence.StorageRef, second.Evidence.StorageRef)
	}
	// The event identity, which is derived from the content and must therefore be
	// the same fact twice.
	if first.Incident != nil && second.Incident != nil {
		if first.Incident.EventID != second.Incident.EventID {
			t.Errorf("the event id changed between runs:\n %s\n %s",
				first.Incident.EventID, second.Incident.EventID)
		}
	}
	// The trigger, the reason and the pre-roll. A demo whose trigger moves is a
	// demo whose false-positive rate cannot be quoted.
	if first.TriggerWhy != second.TriggerWhy {
		t.Errorf("the trigger reason changed between runs:\n %s\n %s", first.TriggerWhy, second.TriggerWhy)
	}
	if first.Clip.PreRollPics != second.Clip.PreRollPics {
		t.Errorf("the pre-roll changed between runs: %d then %d pictures",
			first.Clip.PreRollPics, second.Clip.PreRollPics)
	}
	if first.Pictures != second.Pictures {
		t.Errorf("the picture count changed between runs: %d then %d", first.Pictures, second.Pictures)
	}

	// And the thing that makes a replay safe: it asked nobody anything. There is
	// no provider in this command at all, so the strongest available assertion is
	// that the report says so in the place a reader looks.
	if !reportSaysNoAIPlane(t, args) {
		t.Error("a replay's report does not say the AI plane was never asked; a reader would have to " +
			"know that to know the run was free")
	}
}

func reportSaysNoAIPlane(t *testing.T, args []string) bool {
	t.Helper()
	code, out := runPocCmd(t, args...)
	if code != command.ExitOK {
		t.Fatalf("poc returned %d\n%s", code, out)
	}
	return strings.Contains(out, "no AI plane is configured")
}

// runPocReportForTest parses a poc report for a test.
func runPocReportForTest(t *testing.T, args ...string) pocReport {
	t.Helper()
	code, out := runPocCmd(t, args...)
	if code != command.ExitOK {
		t.Fatalf("poc %v returned %d\n%s", args, code, out)
	}
	var rep pocReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse the report: %v\n%s", err, out)
	}
	return rep
}
