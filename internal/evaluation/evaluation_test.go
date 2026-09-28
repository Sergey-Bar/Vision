package evaluation_test

import (
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/evaluation"
)

func yes() *bool            { b := true; return &b }
func hours(h float64) *bool { b := h < 2; return &b }

// buildCases produces a set with a known profile: 80% true positives correctly
// detected, 20% false positives.
func buildCases(n int) []evaluation.Case {
	cases := make([]evaluation.Case, 0, n)
	for i := 0; i < n; i++ {
		c := evaluation.Case{
			ID: "case-" + itoa(i), ProfileID: "restricted-person",
			HoursToDisposition: 0.25,
		}
		switch i % 5 {
		case 4:
			c.Outcome = evaluation.OutcomeFalsePositive
			c.PredictedRestricted = yes()
		default:
			c.Outcome = evaluation.OutcomeTruePositive
			c.PredictedRestricted = yes()
		}
		cases = append(cases, c)
	}
	return cases
}

func observationsFor(cases []evaluation.Case) []evaluation.Observation {
	out := make([]evaluation.Observation, 0, len(cases))
	for _, c := range cases {
		predicted := c.PredictedRestricted != nil && *c.PredictedRestricted
		agree := hours(0.25)
		out = append(out, evaluation.Observation{
			ID: c.ID, ProfileID: c.ProfileID, LatencyMS: 1200,
			Structured:     map[string]any{"restrictedZoneBreach": predicted},
			OperatorAgreed: agree,
		})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestAPassingProfileIsEligible(t *testing.T) {
	cases := buildCases(100)
	report := evaluation.Evaluate(cases, observationsFor(cases), evaluation.DefaultGates())

	if len(report.Reports) != 1 {
		t.Fatalf("%d reports, want 1", len(report.Reports))
	}
	pr := report.Reports[0]
	if !pr.Passed {
		t.Errorf("a well-behaved profile failed: %v", pr.Failures)
	}
	if len(report.Promotable) != 1 || report.Promotable[0] != "restricted-person" {
		t.Errorf("promotable is %v", report.Promotable)
	}
	if pr.Labeled != 100 {
		t.Errorf("labelled is %d, want 100", pr.Labeled)
	}
	if pr.FalsePositiveRate < 0.19 || pr.FalsePositiveRate > 0.21 {
		t.Errorf("false-positive rate is %.3f, want about 0.20", pr.FalsePositiveRate)
	}
	if pr.Recall < 0.99 {
		t.Errorf("recall is %.3f, want 1.0", pr.Recall)
	}
}

// The false-positive rate is the gate that matters most, because an operator
// shown noise twice a week learns to dismiss the feed.
func TestNoisyProfileIsNotEligible(t *testing.T) {
	cases := buildCases(100)
	for i := 0; i < 30; i++ {
		cases[i].Outcome = evaluation.OutcomeFalsePositive
	}
	report := evaluation.Evaluate(cases, observationsFor(cases), evaluation.DefaultGates())

	if len(report.Promotable) != 0 {
		t.Error("a profile with a 30% false-positive rate was eligible for promotion")
	}
	pr := report.Reports[0]
	found := false
	for _, f := range pr.Failures {
		if strings.Contains(f, "false-positive") {
			found = true
		}
	}
	if !found {
		t.Errorf("the false-positive gate did not fire: %v", pr.Failures)
	}
}

// Precise by being quiet is not precision.
func TestSilentProfileFailsRecall(t *testing.T) {
	cases := buildCases(100)
	obs := observationsFor(cases)
	for i := range obs {
		obs[i].Structured = map[string]any{"restrictedZoneBreach": false}
	}
	report := evaluation.Evaluate(cases, obs, evaluation.DefaultGates())

	if len(report.Promotable) != 0 {
		t.Error("a profile that never fires was eligible for promotion")
	}
	found := false
	for _, f := range report.Reports[0].Failures {
		if strings.Contains(f, "recall") {
			found = true
		}
	}
	if !found {
		t.Errorf("the recall gate did not fire: %v", report.Reports[0].Failures)
	}
}

// A rate computed from four cases is a number, not an estimate.
func TestTooFewCasesFailsTheGate(t *testing.T) {
	cases := buildCases(4)
	report := evaluation.Evaluate(cases, observationsFor(cases), evaluation.DefaultGates())
	if len(report.Promotable) != 0 {
		t.Error("a profile judged on four cases was eligible for promotion")
	}
	found := false
	for _, f := range report.Reports[0].Failures {
		if strings.Contains(f, "labelled") {
			found = true
		}
	}
	if !found {
		t.Errorf("the minimum-case gate did not fire: %v", report.Reports[0].Failures)
	}
}

// A case nobody judged is not evidence. Counting it either way makes a harness
// that improves when you stop measuring.
func TestUndispositionedCasesAreNeverFoldedIntoARate(t *testing.T) {
	cases := buildCases(100)
	// Ten cases an operator never judged, and one of them a true positive the
	// profile missed. Folding either in would change a published rate.
	for i := 0; i < 10; i++ {
		cases = append(cases, evaluation.Case{
			ID: "unjudged-" + itoa(i), ProfileID: "restricted-person",
			Outcome: evaluation.OutcomeUndispositioned,
		})
	}
	report := evaluation.Evaluate(cases, observationsFor(cases), evaluation.DefaultGates())
	pr := report.Reports[0]

	if pr.Labeled != 100 {
		t.Errorf("labelled is %d, want 100; unjudged cases were counted", pr.Labeled)
	}
	if pr.Undispositioned != 10 {
		t.Errorf("undispositioned is %d, want 10", pr.Undispositioned)
	}
	if pr.TruePositives != 80 {
		t.Errorf("true positives is %d, want 80", pr.TruePositives)
	}
}

// A degraded or insufficient observation yields no prediction at all, and an
// absence is not a negative.
func TestAbsenceIsNotAPrediction(t *testing.T) {
	cases := buildCases(100)
	obs := observationsFor(cases)
	for i := range obs {
		obs[i].Degraded = true
		obs[i].Structured = map[string]any{}
	}
	report := evaluation.Evaluate(cases, obs, evaluation.DefaultGates())
	pr := report.Reports[0]

	if pr.DetectedRestricted != 0 {
		t.Errorf("a degraded observation produced %d detection(s); a degraded observation is "+
			"not evidence and must not detect anything", pr.DetectedRestricted)
	}
	if pr.MissedRestricted != 0 {
		t.Errorf("a degraded observation produced %d miss(es); an absence is not a false negative "+
			"either, it is an absence", pr.MissedRestricted)
	}
	if pr.Degraded != 100 {
		t.Errorf("degraded is %d, want 100", pr.Degraded)
	}
}

func TestInsufficientEvidenceIsNotAPrediction(t *testing.T) {
	cases := buildCases(100)
	obs := observationsFor(cases)
	for i := range obs {
		obs[i].InsufficientEvidence = true
		obs[i].Structured = map[string]any{}
	}
	pr := evaluation.Evaluate(cases, obs, evaluation.DefaultGates()).Reports[0]
	if pr.DetectedRestricted != 0 || pr.MissedRestricted != 0 {
		t.Errorf("an insufficient-evidence observation produced %d detection(s) and %d miss(es); "+
			"\"I cannot tell\" is neither", pr.DetectedRestricted, pr.MissedRestricted)
	}
	if pr.Insufficient != 100 {
		t.Errorf("insufficient is %d, want 100", pr.Insufficient)
	}
}

// A slow profile sits on the p95 incident-visibility path.
func TestSlowProfileFailsLatency(t *testing.T) {
	cases := buildCases(100)
	obs := observationsFor(cases)
	for i := range obs {
		obs[i].LatencyMS = 30000
	}
	report := evaluation.Evaluate(cases, obs, evaluation.DefaultGates())
	if len(report.Promotable) != 0 {
		t.Error("a profile with a 30-second median latency was eligible")
	}
	found := false
	for _, f := range report.Reports[0].Failures {
		if strings.Contains(f, "latency") {
			found = true
		}
	}
	if !found {
		t.Errorf("the latency gate did not fire: %v", report.Reports[0].Failures)
	}
}

// A profile operators routinely contradict is not ready to affect state, whether
// it is wrong or merely illegible.
func TestDisagreementFails(t *testing.T) {
	cases := buildCases(100)
	obs := observationsFor(cases)
	for i := range obs {
		// The operator disagreed with every prediction.
		disagree := false
		obs[i].OperatorAgreed = &disagree
	}
	report := evaluation.Evaluate(cases, obs, evaluation.DefaultGates())
	if len(report.Promotable) != 0 {
		t.Error("a profile the operators contradict on every case was eligible")
	}
}

// Every failing gate is named. A single boolean is not enough to act on.
func TestEveryFailingGateIsNamed(t *testing.T) {
	cases := buildCases(4)
	obs := observationsFor(cases)
	for i := range obs {
		obs[i].Structured = map[string]any{"restrictedZoneBreach": false}
		obs[i].LatencyMS = 60000
	}
	pr := evaluation.Evaluate(cases, obs, evaluation.DefaultGates()).Reports[0]

	if pr.Passed {
		t.Fatal("a failing profile passed")
	}
	joined := strings.Join(pr.Failures, " | ")
	for _, want := range []string{"labelled", "recall", "latency"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the %s gate is missing from %q", want, joined)
		}
	}
}

func TestAvailabilityIsExposedForPilotReporting(t *testing.T) {
	cases := buildCases(100)
	obs := observationsFor(cases)
	for i := 0; i < 25; i++ {
		obs[i].Degraded = true
		obs[i].Structured = map[string]any{}
	}
	pr := evaluation.Evaluate(cases, obs, evaluation.DefaultGates()).Reports[0]

	if pr.Labeled != 100 {
		t.Errorf("labelled is %d, want 100", pr.Labeled)
	}
	if pr.Degraded != 25 {
		t.Errorf("degraded is %d, want 25", pr.Degraded)
	}
	// 75 of 100 were real inferences.
	if pr.Recall < 0 || pr.Recall > 1 {
		t.Errorf("recall is %v", pr.Recall)
	}
}
