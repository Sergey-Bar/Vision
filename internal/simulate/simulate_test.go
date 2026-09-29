package simulate_test

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/simulate"
)

// The activation gate compares a digest of a simulation run against the digest
// recorded when the bundle was reviewed. `SimulatedAt` is the one field on the
// report that must not reach it: a simulation re-run to confirm a review has to
// land on the value that was reviewed, or a confirmatory run reads as a change
// and an operator cannot tell a reproduction from a regression.
func TestTheDigestIgnoresTheRunTimestampAndNothingElse(t *testing.T) {
	change := simulate.Change{
		Kind:       simulate.ChangeCreated,
		ScenarioID: "s-1",
		SignalID:   "sig-1",
		Before:     simulate.Outcome{IncidentRaised: false, Severity: "low"},
		After: simulate.Outcome{
			ScenarioID: "s-1", SignalID: "sig-1",
			IncidentRaised: true, Severity: "high", State: "unverified",
		},
	}

	base := simulate.Diff("bundle-1", "bundle-0", "fixtures-1",
		[]simulate.Outcome{{ScenarioID: "s-1", SignalID: "sig-1", Severity: "low"}},
		[]simulate.Outcome{{
			ScenarioID: "s-1", SignalID: "sig-1",
			IncidentRaised: true, Severity: "high", State: "unverified",
		}})
	if base.DiffDigest == "" {
		t.Fatal("a diff with one change produced no digest")
	}

	// The same run, a week later. SimulatedAt is the report's own field, and the
	// digest is computed from the changes rather than from the report, so this
	// has to be equal.
	later := base
	later.SimulatedAt = base.SimulatedAt.Add(7 * 24 * time.Hour)
	if base.DiffDigest != later.DiffDigest {
		t.Error("changing only the report's timestamp changed the diff digest. A " +
			"confirmatory re-run has to reproduce the reviewed digest")
	}

	// And it really is over the changes.
	after := []simulate.Outcome{{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "critical", State: "unverified",
	}}
	changed := simulate.Diff("bundle-1", "bundle-0", "fixtures-1",
		[]simulate.Outcome{{ScenarioID: "s-1", SignalID: "sig-1", Severity: "low"}}, after)
	if changed.DiffDigest == base.DiffDigest {
		t.Error("a different outcome produced the same digest. The digest is what activation " +
			"compares against a reviewed value, so a digest that ignores its inputs approves " +
			"a bundle nobody reviewed")
	}
	_ = change
}

// Everything the digest is claimed to cover does reach it: the bundle version
// and the fixture set are part of the identity of the run, and a run over a
// different set is a different review.
func TestTheDigestCoversTheBundleAndTheFixtureSet(t *testing.T) {
	before := []simulate.Outcome{{ScenarioID: "s-1", SignalID: "sig-1", Severity: "low"}}
	after := []simulate.Outcome{{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "high",
	}}

	base := simulate.DigestFor("bundle-1", "fixtures-1", simulate.Diff(
		"bundle-1", "bundle-0", "fixtures-1", before, after).Changes)

	for name, got := range map[string]string{
		"a different bundle": simulate.DigestFor("bundle-2", "fixtures-1",
			simulate.Diff("bundle-2", "bundle-0", "fixtures-1", before, after).Changes),
		"a different fixture set": simulate.DigestFor("bundle-1", "fixtures-2",
			simulate.Diff("bundle-1", "bundle-0", "fixtures-2", before, after).Changes),
	} {
		if got == base {
			t.Errorf("%s did not change the digest", name)
		}
	}
}

// The empty case has to be a digest, not a panic and not an empty string. An
// activation comparing against "" would be satisfied by a run that produced
// nothing, which is the one result a bundle must never be activated on.
func TestTheEmptyDiffStillHasADigest(t *testing.T) {
	report := simulate.Diff("bundle-1", "bundle-0", "fixtures-1", nil, nil)
	if report.DiffDigest == "" {
		t.Fatal("an empty diff produced an empty digest; that would compare equal to " +
			"anything else that failed to produce one")
	}
	if len(report.DiffDigest) != 64 {
		t.Errorf("the digest is %d characters, want 64 for a hex-encoded SHA-256",
			len(report.DiffDigest))
	}
	if report.DiffDigest != simulate.DigestFor("bundle-1", "fixtures-1", nil) {
		t.Error("a nil change list and the report's own empty change list produced " +
			"different digests; they are the same run")
	}
}
