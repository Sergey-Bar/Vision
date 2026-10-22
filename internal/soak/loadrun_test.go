package soak_test

// The load-run artefact, and the one question it exists to answer.
//
// # The question
//
// "Did this run cover EVERY objective?"
//
// A load run that measured four of the eight objectives and published four
// numbers is indistinguishable, to a reader, from one that measured all eight.
// The objectives it did not cover are the ones nobody is being told about, and a
// gate that only counted the covered ones would pass a run that proved almost
// nothing.
//
// The roadmap names this as the one thing that is not cuttable: publishing an
// SLO the run did not cover is worse than publishing none.

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/observability"
	"github.com/Sergey-Bar/vision/internal/soak"
)

// fullRun is an artefact that measured every objective.
func fullRun(t *testing.T) soak.LoadRun {
	t.Helper()
	run := soak.LoadRun{
		Version:         soak.LoadRunVersion,
		StartedAt:       time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC),
		FinishedAt:      time.Date(2026, 10, 21, 9, 5, 0, 0, time.UTC),
		Rate:            120,
		Events:          36000,
		DurationSeconds: 300,
	}
	for _, name := range observability.SLONames() {
		run.Objectives = append(run.Objectives, soak.ObjectiveReading{
			Name:     name,
			Target:   observability.SLOTargets()[name],
			Measured: "measured",
		})
	}
	return run
}

// TestARunThatMeasuredEveryObjectiveIsComplete is the pass case.
func TestARunThatMeasuredEveryObjectiveIsComplete(t *testing.T) {
	run := fullRun(t)
	if !run.Complete() {
		t.Errorf("a run that measured every objective is not complete: %v", run.Uncovered)
	}
	if len(soak.UncoveredObjectives(run)) != 0 {
		t.Errorf("the run reports %v uncovered, want none", soak.UncoveredObjectives(run))
	}
}

// TestARunThatMissedAnObjectiveIsNotComplete is the fail case.
func TestARunThatMissedAnObjectiveIsNotComplete(t *testing.T) {
	run := fullRun(t)
	run.Objectives = run.Objectives[:len(run.Objectives)-1]
	missed := observability.SLONames()[len(observability.SLONames())-1]

	if run.Complete() {
		t.Error("a run that missed an objective reports as complete")
	}
	uncovered := soak.UncoveredObjectives(run)
	if len(uncovered) != 1 || uncovered[0] != missed {
		t.Errorf("the uncovered set is %v, want [%s]", uncovered, missed)
	}
}

// TestAnArtefactThatClaimsAnObjectiveItDidNotMeasureIsCaught is the honesty
// check.
//
// The artefact says it measured eight objectives. The BUILD says there are
// eight, and the artefact's own readings name three of them. The comparison is
// against the BUILD and the ARTEFACT, so an artefact that overstates itself
// is caught even though it says it is complete.
func TestAnArtefactThatClaimsAnObjectiveItDidNotMeasureIsCaught(t *testing.T) {
	run := fullRun(t)
	// The artefact keeps three readings and drops its own account of what it
	// covered -- which is what a stale or hand-edited artefact looks like.
	run.Objectives = run.Objectives[:3]
	run.Uncovered = []string{}

	all := observability.SLONames()
	measured := map[string]bool{}
	for _, o := range run.Objectives {
		measured[o.Name] = true
	}
	var want []string
	for _, name := range all {
		if !measured[name] {
			want = append(want, name)
		}
	}
	got := soak.UncoveredObjectives(run)
	if len(got) != len(want) {
		t.Errorf("the uncovered set holds %d objective(s), want %d", len(got), len(want))
	}
}

// TestTheObjectiveListIsNotEmpty is the floor.
//
// A build with no objectives would make every run complete, and the gate would
// pass everything.
func TestTheObjectiveListIsNotEmpty(t *testing.T) {
	names := observability.SLONames()
	if len(names) < 4 {
		t.Fatalf("the build evaluates %d objective(s), so the load-run gate proves almost nothing",
			len(names))
	}
	targets := observability.SLOTargets()
	for _, name := range names {
		if targets[name] == "" {
			t.Errorf("the objective %q has no target", name)
		}
	}
}
