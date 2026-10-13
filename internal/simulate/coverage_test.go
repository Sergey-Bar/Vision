package simulate_test

// This file holds the tests for what a diff does *not* say. `simulate_test.go`
// covers the digest's inputs; these cover the two differences the diff was
// blind to, so they land in a file of their own.

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/simulate"
)

// The field is declared with the comment "so a digest over a truncated run
// cannot be mistaken for a digest over the whole fixture set", and nothing ever
// assigned it, so every report ever printed carried maxEvents 0 and the guarantee
// did not exist. A truncated run's change list is a prefix of a whole run's, so
// the two produce the same digest - and the digest is exactly what activation
// compares against an acknowledged value.
func TestATruncatedRunAndAWholeRunDoNotProduceTheSameReport(t *testing.T) {
	// The two signals the truncated run covers, and the one it does not. The
	// prefix is byte-identical to the whole run's prefix, which is the point:
	// nothing in the change list distinguishes them.
	whole := []simulate.Outcome{
		{ScenarioID: "s-1", SignalID: "sig-1", IncidentRaised: true, Severity: "high", State: "unverified"},
		{ScenarioID: "s-1", SignalID: "sig-2"},
		{ScenarioID: "s-1", SignalID: "sig-3", IncidentRaised: true, Severity: "low", State: "unverified"},
	}
	truncated := whole[:1]

	// No baseline, so the two runs differ only in coverage and the digest is a
	// straight function of the change list.
	full := simulate.DiffCapped("bundle-1", "", "fixtures-1", nil, whole, 0)
	cut := simulate.DiffCapped("bundle-1", "", "fixtures-1", nil, truncated, 1)

	if full.MaxEvents != 0 {
		t.Errorf("an uncapped run reports MaxEvents %d; 0 is the only value that means "+
			"'not capped', and it is not the same claim as 'capped at zero'", full.MaxEvents)
	}
	if cut.MaxEvents != 1 {
		t.Errorf("the capped run reports MaxEvents %d, want the cap it ran under", cut.MaxEvents)
	}
	if !cut.Truncated {
		t.Error("a run that hit its cap does not report Truncated. The report is the only " +
			"thing the operator reads, and it must say the run stopped early")
	}
	if full.Truncated {
		t.Error("a whole run reports Truncated")
	}
	if full.DiffDigest == cut.DiffDigest {
		t.Errorf("a run over one signal and a run over three produce the same digest %s.\n"+
			"The cap is the one thing they differ by, and the digest is the field "+
			"activation compares against an acknowledged value, so this is an "+
			"acknowledgement that covers a run nobody performed", full.DiffDigest)
	}

	// And the truncation is visible as a line, not only as a number. The row is
	// the same shape the misaligned-run case uses, so there is one way to read a
	// synthetic row in this report rather than two.
	var sawRow bool
	for _, c := range cut.Changes {
		if c.Kind == simulate.ChangeStateChanged && strings.Contains(c.After.State, "truncated") {
			sawRow = true
		}
	}
	if !sawRow {
		t.Errorf("the truncated report carries no row saying it was truncated:\n%+v", cut.Changes)
	}
	// It reaches the digest, which is the whole promise the field makes.
	if cut.DiffDigest != simulate.DigestFor(cut.BundleVersion, cut.FixtureSet, cut.Changes) {
		t.Error("the truncation is not covered by the digest, so the two runs would " +
			"compare equal in the gate that reads it")
	}
}

// A run that finished under its cap is not a truncated run. A cap that was not
// reached is the normal case and saying "truncated" about it would train an
// operator to ignore the field.
func TestARunThatFinishedUnderItsCapIsNotTruncated(t *testing.T) {
	after := []simulate.Outcome{
		{ScenarioID: "s-1", SignalID: "sig-1"},
		{ScenarioID: "s-1", SignalID: "sig-2"},
	}
	report := simulate.DiffCapped("bundle-1", "", "fixtures-1", nil, after, 100)
	if report.Truncated {
		t.Errorf("a run of %d outcomes under a cap of %d reports Truncated", len(after), 100)
	}
	for _, c := range report.Changes {
		if strings.Contains(c.After.State, "truncated") {
			t.Errorf("a complete run carries a truncation row: %+v", c)
		}
	}
}

// `classify` read IncidentRaised, Severity, Degraded and State, and ignored
// RuleID and Escalate. A candidate bundle that swaps which rule raises an
// incident therefore produced no change row and no contribution to the digest, so
// an operator's acknowledged digest was unchanged by a change in who decides.
func TestAChangeInWhichRuleFiredIsAChange(t *testing.T) {
	base := simulate.Outcome{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "high", State: "unverified",
		RuleID: "rule-door-held-open",
	}
	swapped := base
	swapped.RuleID = "rule-door-left-open"

	before := []simulate.Outcome{base}
	after := []simulate.Outcome{swapped}

	report := simulate.Diff("bundle-1", "bundle-0", "fixtures-1", before, after)
	if len(report.Changes) != 1 {
		t.Fatalf("a bundle that swapped the deciding rule produced %d change row(s), want 1.\n"+
			"classify ignored RuleID, so the difference classified as no_change and never "+
			"reached the digest: %+v", len(report.Changes), report.Changes)
	}
	if report.Changes[0].Kind != simulate.ChangeRuleChanged {
		t.Errorf("the change is a %q, want %q", report.Changes[0].Kind, simulate.ChangeRuleChanged)
	}
	if report.Totals["ruleChanged"] != 1 {
		t.Errorf("totals do not count the rule change: %v", report.Totals)
	}
	if report.DiffDigest == simulate.Diff("bundle-1", "bundle-0", "fixtures-1", before, before).DiffDigest {
		t.Error("swapping the deciding rule left the digest unchanged. The digest is what " +
			"activation gates on, so an operator acknowledged a report that could not " +
			"have told them the rule behind every incident had changed")
	}
}

// Escalation is the other field classify dropped, and it is the one that pages
// somebody. Turning it on is a change in what the deployment does at 3am.
func TestTurningEscalationOnIsAChange(t *testing.T) {
	before := []simulate.Outcome{{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "high", State: "unverified", RuleID: "rule-1",
	}}
	after := []simulate.Outcome{{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "high", State: "unverified", RuleID: "rule-1",
		Escalate: true,
	}}

	report := simulate.Diff("bundle-1", "bundle-0", "fixtures-1", before, after)
	if len(report.Changes) != 1 || report.Changes[0].Kind != simulate.ChangeEscalationChanged {
		t.Fatalf("turning escalation on produced %d change row(s) of kind %q, want one of "+
			"kind %q", len(report.Changes), kindOf(report), simulate.ChangeEscalationChanged)
	}
	if report.Totals["escalationChanged"] != 1 {
		t.Errorf("totals do not count the escalation change: %v", report.Totals)
	}
}

// The two new kinds are classified last, so a difference an operator can see in
// a console row keeps the kind it always had. This is asserted rather than
// assumed: reordering classify is exactly the change that would silently relabel
// every existing report.
func TestAVisibleDifferenceKeepsItsKindOverARuleOrEscalationChange(t *testing.T) {
	before := []simulate.Outcome{{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "high", State: "unverified", RuleID: "rule-1",
	}}
	after := []simulate.Outcome{{
		ScenarioID: "s-1", SignalID: "sig-1",
		IncidentRaised: true, Severity: "critical", State: "unverified",
		RuleID: "rule-2", Escalate: true,
	}}

	report := simulate.Diff("bundle-1", "bundle-0", "fixtures-1", before, after)
	if len(report.Changes) != 1 {
		t.Fatalf("want one change row, got %d", len(report.Changes))
	}
	if got := report.Changes[0].Kind; got != simulate.ChangeSeverityChanged {
		t.Errorf("a severity change that also changed the rule was classified %q, want %q. "+
			"Rule identity is the subtlest of the differences, so it must not displace a "+
			"severity change an operator can already see", got, simulate.ChangeSeverityChanged)
	}
	// Nothing is lost by that ordering: the row carries both outcomes whole.
	if report.Changes[0].After.RuleID != "rule-2" || !report.Changes[0].After.Escalate {
		t.Error("the row does not carry the new rule and escalation, so classifying by the " +
			"most visible difference hides nothing")
	}
}

func kindOf(r simulate.Report) simulate.ChangeKind {
	if len(r.Changes) == 0 {
		return ""
	}
	return r.Changes[0].Kind
}
