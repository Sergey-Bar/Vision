package replayaudit_test

// The replay audit.
//
// # The rate is over the scenarios that EXPECTED an incident
//
// A scenario that expects nothing has nothing to miss, and including it in the
// denominator would make the rate better the more negative cases the corpus
// carries. The corpus is mostly negative cases, so that would be a rate that
// improves by adding proofs that the product does nothing.
//
// # A scenario with no expectation is a GAP and not a zero
//
// "The product did not raise an incident" and "nobody said whether it should
// have" are different facts, and only the second one is missing information.
//
// # A scenario that raised MORE than it was expected to is reported separately
//
// It is a defect of a different KIND, and averaging it into the miss rate
// would hide both.

import (
	"testing"

	"github.com/Sergey-Bar/vision/internal/replayaudit"
)

func TestTheRateIsOverTheScenariosThatExpectedAnIncident(t *testing.T) {
	report := replayaudit.Run(
		[]replayaudit.Expected{
			{Scenario: "motion", Incidents: 1},
			{Scenario: "malformed", Incidents: 0},
		},
		[]replayaudit.Raised{
			{Scenario: "motion", Incidents: 0, Detail: "nothing raised"},
			{Scenario: "malformed", Incidents: 0},
		},
	)

	if len(report.Missed) != 1 || report.Missed[0].Scenario != "motion" {
		t.Fatalf("the missed set is %+v, want the motion scenario", report.Missed)
	}
	if report.N != 1 {
		t.Errorf("the rate is over %d scenario(s), want 1. A scenario that expects nothing has "+
			"nothing to miss", report.N)
	}
	if report.Rate == nil {
		t.Fatal("the report carries no rate")
	}
	if report.Rate.Value == nil || *report.Rate.Value != 1 {
		t.Errorf("the rate is %v, want 1.00", report.Rate.Value)
	}
	if report.Rate.CI == nil {
		t.Error("the rate carries no interval. A rate with no interval is a number with an " +
			"undisclosed precision")
	}
}

func TestAGapIsNotAMiss(t *testing.T) {
	report := replayaudit.Run(
		[]replayaudit.Expected{
			{Scenario: "never-replayed", Incidents: 1},
		},
		[]replayaudit.Raised{},
	)

	if len(report.Missed) != 0 {
		t.Errorf("the missed set is %+v. A scenario that was not replayed is a gap in the "+
			"REPLAY, not a missed incident", report.Missed)
	}
	if len(report.Unlabelled) != 1 || report.Unlabelled[0] != "never-replayed" {
		t.Errorf("the unlabelled set is %v, want the scenario that was never replayed",
			report.Unlabelled)
	}
	if report.Complete {
		t.Error("the report says the corpus is complete, and a scenario was never replayed")
	}
}

func TestAScenarioThatRaisedTooMuchIsReportedSeparately(t *testing.T) {
	report := replayaudit.Run(
		[]replayaudit.Expected{
			{Scenario: "duplicate", Incidents: 1},
		},
		[]replayaudit.Raised{
			{Scenario: "duplicate", Incidents: 2, Detail: "two incidents from one event"},
		},
	)

	if len(report.Extra) != 1 || report.Extra[0].Scenario != "duplicate" {
		t.Errorf("the extra set is %+v, want the duplicate scenario", report.Extra)
	}
	if len(report.Missed) != 0 {
		t.Errorf("the missed set is %+v. A scenario that raised too much did not MISS one",
			report.Missed)
	}
	if report.Rate != nil && report.Rate.Value != nil && *report.Rate.Value != 0 {
		t.Errorf("the rate is %v, want 0.00", *report.Rate.Value)
	}
}

func TestAnEmptyCorpusHasNoRate(t *testing.T) {
	report := replayaudit.Run(nil, nil)
	if report.Rate != nil {
		t.Errorf("the report carries a rate over nothing: %+v", report.Rate)
	}
	if report.N != 0 {
		t.Errorf("the report's N is %d, want 0", report.N)
	}
}
