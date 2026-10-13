package evaluation_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/evaluation"
)

// promoteGates is DefaultGates with a case floor these small fixtures can meet.
//
// The real floor is 200 and deliberately so: at thirty cases a profile sitting
// exactly on the 0.20 false-positive gate is still consistent with a true rate
// near 37%, and a true recall of 52% clears the "at least 0.70" gate. The
// thresholds under test here are the rate thresholds; the sample-size threshold
// has its own test and is asserted against DefaultGates directly.
func promoteGates() evaluation.Gates {
	g := evaluation.DefaultGates()
	g.MinLabelledCases = 10
	return g
}

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
	report := evaluation.Evaluate(cases, observationsFor(cases), promoteGates())

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
	report := evaluation.Evaluate(cases, observationsFor(cases), promoteGates())

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
	report := evaluation.Evaluate(cases, obs, promoteGates())

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
	report := evaluation.Evaluate(cases, observationsFor(cases), promoteGates())
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
	report := evaluation.Evaluate(cases, observationsFor(cases), promoteGates())
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
	report := evaluation.Evaluate(cases, obs, promoteGates())
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
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]
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
	report := evaluation.Evaluate(cases, obs, promoteGates())
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
	report := evaluation.Evaluate(cases, obs, promoteGates())
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
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

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
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

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

// A rate whose numerator includes cases the denominator does not is not a rate.
//
// One true positive detected and twenty benign cases all flagged as restricted
// used to produce a recall of 21.0, because DetectedRestricted counted every
// case the profile flagged and the denominator was only the true positives. A
// gate written as "at least 0.70" cannot fail against 21.0, so the gate that
// decides M1 exit gate 4 was not a gate.
func TestRecallCountsOnlyTruePositives(t *testing.T) {
	cases := []evaluation.Case{{
		ID: "tp-1", ProfileID: "p", Outcome: evaluation.OutcomeTruePositive,
		PredictedRestricted: yes(),
	}}
	for i := 0; i < 20; i++ {
		// Twenty benign cases the profile flagged anyway: noise, and every one of
		// them a wrong answer that must not count towards recall.
		cases = append(cases, evaluation.Case{
			ID: "benign-" + itoa(i), ProfileID: "p",
			Outcome: evaluation.OutcomeBenign, PredictedRestricted: yes(),
		})
	}
	pr := evaluation.Evaluate(cases, observationsFor(cases), promoteGates()).Reports[0]

	if pr.DetectedRestricted != 1 {
		t.Errorf("detected-restricted is %d, want 1; benign cases flagged as restricted are "+
			"noise and cannot be recall", pr.DetectedRestricted)
	}
	if pr.Recall != 1.0 {
		t.Errorf("recall is %.2f, want 1.0 (one of one true positives detected)", pr.Recall)
	}
	if pr.FalsePositiveRate != 0 {
		t.Errorf("false-positive rate is %.2f, want 0; benign cases are not false positives, "+
			"the noise floor is the cases an operator called worth reporting and then "+
			"dispositioned as nothing", pr.FalsePositiveRate)
	}
}

// The mirror image: absence must not lower the measured noise floor. Labeled
// counts cases an operator judged; Answered counts cases the profile actually
// returned a judgement for, and the rate divides by the second. Folding
// degraded observations into the denominator is "improve by measuring less", the
// failure this package's own doc exists to prevent.
func TestAbsenceDoesNotLowerTheMeasuredRates(t *testing.T) {
	cases := buildCases(20) // 16 true positives, 4 false positives: FPR 0.20
	obs := observationsFor(cases)
	// Add 80 degraded cases. Labeled goes to 100; Answered must stay at 20.
	for i := 0; i < 80; i++ {
		cases = append(cases, evaluation.Case{
			ID: "absent-" + itoa(i), ProfileID: "restricted-person",
			Outcome: evaluation.OutcomeFalsePositive,
		})
		obs = append(obs, evaluation.Observation{
			ID: "absent-" + itoa(i), ProfileID: "restricted-person",
			Degraded: true, Structured: map[string]any{},
		})
	}
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

	if pr.Labeled != 100 {
		t.Errorf("labelled is %d, want 100; an operator judged all of them", pr.Labeled)
	}
	if pr.Answered != 20 {
		t.Errorf("answered is %d, want 20; a degraded observation produced no judgement", pr.Answered)
	}
	want := 4.0 / 20.0
	if pr.FalsePositiveRate < want-0.001 || pr.FalsePositiveRate > want+0.001 {
		t.Errorf("false-positive rate is %.3f, want %.3f; eighty absent cases were folded "+
			"into the denominator and the noise floor was diluted fourfold",
			pr.FalsePositiveRate, want)
	}
}

// An un-normalised counter left in a rate field renders as 500% and fails a gate
// for a reason that has nothing to do with the profile.
// The sample-size floor is a number, and it is a number about arithmetic rather
// than about this profile. At thirty cases the gates are not decidable, so the
// floor is 200 and shadow evaluation - which collects cases rather than deciding
// on them - keeps a lower one and says so.
func TestPromotionNeedsTwoHundredLabelledCases(t *testing.T) {
	if got := evaluation.DefaultGates().MinLabelledCases; got != 200 {
		t.Errorf("the promotion case floor is %d, want 200; at thirty cases a profile on the "+
			"0.20 false-positive gate is still consistent with a true rate near 37%%, and a "+
			"true recall of 52%% passes a 0.70 gate", got)
	}
	if got := evaluation.ShadowGates().MinLabelledCases; got >= evaluation.DefaultGates().MinLabelledCases {
		t.Errorf("the shadow case floor is %d, which is not lower than the promotion floor of %d; "+
			"a shadow run must be able to collect cases without passing a decision",
			got, evaluation.DefaultGates().MinLabelledCases)
	}
}

// A rate whose numerator only moves on a subset must divide by that subset.
//
// The disagreement numerator increments only when a case carries an operator's
// verdict; the denominator was every answered case. One disagreeing verdict out
// of 200 answered cases produced 0.005 and passed the 0.25 gate - a one-case
// measurement reported as a two-hundred-case one, and the direction is always the
// flattering one, because a small denominator inflates the rate but a large one
// deflates it.
func TestTheDisagreementRateDividesByTheCasesThatHaveAVerdict(t *testing.T) {
	cases := buildCases(200)
	obs := observationsFor(cases)
	// Every case starts with a verdict, because that is what a fully-reviewed
	// fixture set looks like. Strip all but one, and make that one disagree.
	for i := range obs {
		obs[i].OperatorAgreed = nil
	}
	disagree := false
	obs[0].OperatorAgreed = &disagree

	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

	if pr.OperatorVerdicts != 1 {
		t.Fatalf("the report says %d operator verdicts, want 1", pr.OperatorVerdicts)
	}
	if pr.Answered <= 1 {
		t.Fatalf("the fixture produced only %d answered cases, so it cannot demonstrate the "+
			"mixed-population case", pr.Answered)
	}
	// One disagreement out of one verdict is 1.00, which the 0.25 gate must
	// catch. Divided by 200 it is 0.005, which it does not - and that is the
	// entire defect: a profile that disagrees with the only case anybody reviewed
	// is reported as disagreeing with half a percent of them.
	if pr.DisagreementRate != 1.0 {
		t.Errorf("the disagreement rate is %.3f, want 1.00 over one verdict. The rate is being "+
			"computed over the %d answered cases rather than the %d that carry a verdict, so a "+
			"one-case measurement is reported as a two-hundred-case one",
			pr.DisagreementRate, pr.Answered, pr.OperatorVerdicts)
	}
	var complained bool
	for _, f := range pr.Failures {
		if strings.Contains(f, "disagreement rate") {
			complained = true
		}
	}
	if !complained {
		t.Errorf("a profile that disagrees with the only case anybody reviewed passed the gate "+
			"with %v", pr.Failures)
	}
}

// A profile that never detected a true positive is the worst recall there is.
// The gate used to be skipped when `TruePositives == 0`, which is exactly the
// profile that has detected none.
func TestAProfileWithNoTruePositivesFailsRatherThanPassing(t *testing.T) {
	// 200 answered cases, every one of them a false positive or benign, so the
	// recall gate has nothing to measure.
	cases := make([]evaluation.Case, 0, 200)
	obs := make([]evaluation.Observation, 0, 200)
	for i := 0; i < 200; i++ {
		id := "case-" + strconv.Itoa(i)
		cases = append(cases, evaluation.Case{ID: id, ProfileID: "p", Outcome: evaluation.OutcomeBenign})
		obs = append(obs, evaluation.Observation{
			ID: id, ProfileID: "p",
			Structured: map[string]any{"restrictedZoneBreach": false},
		})
	}
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

	var complained bool
	for _, f := range pr.Failures {
		if strings.Contains(f, "recall is not measurable") {
			complained = true
		}
	}
	if !complained {
		t.Errorf("a profile with zero true positives passed the gates with %v. Zero true "+
			"positives is the worst possible recall, not an absent measurement", pr.Failures)
	}
	if pr.Passed {
		t.Error("the report is marked as passed")
	}
}

// The sample-size floor is about the population the rates are computed over.
//
// It was checked against `Labeled`, which counts cases that produced no answer.
// Two hundred degraded observations therefore satisfied a floor of 200 while the
// false-positive rate it exists to support was decided by five cases.
func TestTheSampleSizeFloorCountsAnsweredCases(t *testing.T) {
	cases := buildCases(200)
	obs := observationsFor(cases)
	// 195 of the 200 are degraded: labelled, but no answer.
	for i := 0; i < 195; i++ {
		obs[i].Degraded = true
		obs[i].Structured = map[string]any{}
	}
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

	if pr.Labeled != 200 {
		t.Fatalf("the fixture produced %d labelled cases, want 200", pr.Labeled)
	}
	if pr.Answered != 5 {
		t.Fatalf("the fixture produced %d answered cases, want 5", pr.Answered)
	}
	var complained bool
	for _, f := range pr.Failures {
		if strings.Contains(f, "answered case(s)") {
			complained = true
		}
	}
	if !complained {
		t.Errorf("200 labelled cases with 195 of them degraded satisfied a floor of 200 while "+
			"every rate was computed over 5. The floor exists so a rate is not decided by a "+
			"handful of cases; against Labeled it does not do that. Failures: %v", pr.Failures)
	}
}

func TestDisagreementIsNotMeasurableRatherThanACount(t *testing.T) {
	cases := buildCases(20)
	obs := observationsFor(cases)
	for i := range obs {
		obs[i].OperatorAgreed = nil // no operator verdict accompanies any answer
	}
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

	if pr.DisagreementMeasurable {
		t.Error("disagreement is reported as measurable with no operator verdicts at all")
	}
	if pr.DisagreementRate != 0 {
		t.Errorf("disagreement rate is %.2f, want 0; an un-normalised count of %d was left "+
			"in a rate field", pr.DisagreementRate, pr.Disagreements)
	}
	for _, f := range pr.Failures {
		if strings.Contains(f, "disagreement") {
			t.Errorf("the disagreement gate fired on an unmeasurable rate: %s", f)
		}
	}
}

// The denominator of a disagreement rate is the cases that were both answered and
// judged by an operator, not the total case count.
func TestDisagreementRateIsNormalised(t *testing.T) {
	cases := buildCases(20)
	obs := observationsFor(cases)
	for i := 0; i < 5; i++ {
		disagree := false
		obs[i].OperatorAgreed = &disagree
	}
	pr := evaluation.Evaluate(cases, obs, promoteGates()).Reports[0]

	if !pr.DisagreementMeasurable {
		t.Fatal("disagreement is not measurable, but five operator verdicts were supplied")
	}
	if pr.Disagreements != 5 {
		t.Errorf("disagreements is %d, want 5", pr.Disagreements)
	}
	want := 5.0 / 20.0
	if pr.DisagreementRate < want-0.001 || pr.DisagreementRate > want+0.001 {
		t.Errorf("disagreement rate is %.3f, want %.3f", pr.DisagreementRate, want)
	}
}
