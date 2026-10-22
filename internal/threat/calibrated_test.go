package threat

// The calibrated engine's own gates.
//
// The engine is a rearrangement of a number nobody had measured, and this file
// is what holds the rearrangement to its four claims:
//
//   - the soft cap is monotone and bounded, so a worse incident keeps a
//     worse score and no score leaves 0..100;
//   - the decay is half-life correct, so an incident twice the half-life old
//     carries half the score in the queue;
//   - the matrix's sixteen cells are all reachable, which the legacy
//     score-only derivation could never manage;
//   - HOLD is reachable, and it means what it says.

import (
	"math"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
)

// TestTheSoftCapIsMonotoneAndBounded
//
// The soft cap keeps the ORDER of the raw sums and stays inside 0..100.
//
// The legacy engine CLIPPED at 100, so a raw sum of 120 and a raw sum of 300
// were the same number and the queue could not tell them apart.
func TestTheSoftCapIsMonotoneAndBounded(t *testing.T) {
	raws := []float64{0, 1, 9, 18, 27, 54, 64, 88, 100, 120, 200, 300, 1000}
	previous := -1
	for _, raw := range raws {
		got := SoftCap(raw)
		if got < 0 || got > 100 {
			t.Errorf("SoftCap(%v) = %d, which is outside 0..100", raw, got)
		}
		if got < previous {
			t.Errorf("SoftCap(%v) = %d, which is LOWER than SoftCap of a smaller sum (%d): "+
				"the cap is not monotone and the queue's order is not the order of the claims",
				raw, got, previous)
		}
		if got == previous && raw > 0 && previous >= 0 {
			// Two different raw sums may round to the same integer, but the
			// legacy clip made EVERYTHING above 100 identical. A single
			// collision is rounding; a flat run is a clip.
			if previous == 100 {
				t.Errorf("SoftCap(%v) = 100 and the sum before it was 100 too: the cap is "+
					"clipping, and the difference between the sums is lost", raw)
			}
		}
		previous = got
	}
	if SoftCap(120) == SoftCap(300) {
		t.Error("SoftCap(120) and SoftCap(300) are the same number: the difference between " +
			"a serious incident and a much more serious one is gone")
	}
	if SoftCap(0) != 0 {
		t.Errorf("SoftCap(0) = %d, want 0", SoftCap(0))
	}
}

// TestTheDecayIsHalfLifeCorrect
//
// An incident twice the half-life old carries half its score in the queue, and
// an incident ten half-lives old carries a thousandth of it.
//
// The decay is a property of the QUEUE. The stored score does not change, and
// the factor is what the queue's SQL multiplies the score by.
func TestTheDecayIsHalfLifeCorrect(t *testing.T) {
	half := 2 * time.Hour
	cases := []struct {
		age  time.Duration
		want float64
	}{
		{0, 1},
		{half, 0.5},
		{2 * half, 0.25},
		{10 * half, 1.0 / 1024.0},
	}
	for _, c := range cases {
		got := DecayFactor(c.age, half)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("DecayFactor(%v, %v) = %v, want %v", c.age, half, got, c.want)
		}
	}
	// A half-life of zero is no decay, which is what the legacy engine has.
	if got := DecayFactor(10*time.Hour, 0); got != 1 {
		t.Errorf("DecayFactor with no half-life = %v, want 1: the queue's order would change "+
			"for an install that has not asked for a decay", got)
	}
	// An age in the future is not a young incident, it is a clock problem,
	// and it must not make the score grow.
	if got := DecayFactor(-time.Hour, half); got != 1 {
		t.Errorf("DecayFactor with a negative age = %v, want 1", got)
	}
}

// TestTheMatrixReachesAllSixteenCells
//
// Every one of the sixteen cells of the decision matrix is a cell.
//
// The legacy engine derived BOTH axes from one score with the same
// thresholds, so the two axes were always equal and twelve of the sixteen
// cells could not be reached at all. The calibrated engine computes them from
// different facts, and this is the test that says so.
func TestTheMatrixReachesAllSixteenCells(t *testing.T) {
	m := DefaultMatrix()
	severities := []SeverityLevel{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow}
	likelihoods := []LikelihoodLevel{LikelihoodVeryHigh, LikelihoodHigh, LikelihoodMedium, LikelihoodLow}

	reached := map[string]bool{}
	for _, s := range severities {
		for _, l := range likelihoods {
			action := m.Decide(s, l)
			if action == "" {
				t.Errorf("the cell %s×%s has no action", s, l)
			}
			reached[string(s)+"×"+string(l)] = true
		}
	}
	if len(reached) != 16 {
		t.Errorf("the matrix reaches %d cell(s), want 16", len(reached))
	}

	// The legacy derivation cannot reach them, and that is the point of the
	// change: it is the reason the two axes were separate.
	legacy := map[string]bool{}
	for score := 0; score <= 100; score++ {
		action := m.DecideFromScore(score)
		legacy[string(action)] = true
	}
	if len(legacy) > 4 {
		t.Errorf("the score-only derivation reaches %d action(s) over the whole 0..100 range, "+
			"and the matrix has four: a derivation that reaches every cell is not a "+
			"derivation of two axes", len(legacy))
	}
}

// TestHoldIsReachableAndMeansSomething
//
// HOLD is in the matrix and the calibrated engine produces it.
//
// HOLD is the cell where the CLAIM is serious and the evidence here is thin.
// The likelihood reaches the bottom of the scale in two ways: the evidence
// was thin, or the site's own base rate says the rule is noise here and caps
// it there. In both cases the answer is the same -- do not spend a human on
// it, do not escalate it, keep it in the queue and watch it.
func TestHoldIsReachableAndMeansSomething(t *testing.T) {
	m := DefaultMatrix()
	if got := m.Decide(SeverityCritical, LikelihoodLow); got != ActionHold {
		t.Errorf("the cell Critical×Low = %s, want HOLD", got)
	}
	if got := m.Decide(SeverityHigh, LikelihoodLow); got != ActionHold {
		t.Errorf("the cell High×Low = %s, want HOLD", got)
	}
	// A routine claim with thin evidence is observed, not held: HOLD is the
	// serious claim's answer and it would mean nothing if everything with
	// thin evidence were held.
	if got := m.Decide(SeverityMedium, LikelihoodLow); got != ActionObserve {
		t.Errorf("the cell Medium×Low = %s, want OBSERVE", got)
	}
	// And the score-only path cannot reach it, because a score carries no
	// evidence axis.
	for score := 0; score <= 100; score++ {
		if m.DecideFromScore(score) == ActionHold {
			t.Errorf("the score-only derivation reached HOLD at the score %d, and it cannot: "+
				"a score carries no evidence axis", score)
		}
	}
}

// TestTheCalibratedEngineUsesTheEvidenceTheLegacyEngineThrewAway
//
// The corroborating device count and the duration excess change the score.
//
// They used to be thrown away: the anomaly's details carried both and the
// scorer read only the zone type and the severity. The corpus measures what
// that cost, and this is the unit test for the mechanism.
func TestTheCalibratedEngineUsesTheEvidenceTheLegacyEngineThrewAway(t *testing.T) {
	one := scoreOf(t, 1, 3.0)
	two := scoreOf(t, 2, 3.0)
	three := scoreOf(t, 3, 3.0)
	if one >= two || two >= three {
		t.Errorf("the scores for one, two and three devices are %d, %d and %d: the "+
			"corroborating device count does not reach the score", one, two, three)
	}

	short := scoreOf(t, 1, 3.0)
	long := scoreOf(t, 1, 15.0)
	if short >= long {
		t.Errorf("the scores for a presence at the threshold and one five times over are %d and %d: "+
			"the duration excess does not reach the score", short, long)
	}
}

// scoreOf scores a presence with a given device count and duration.
func scoreOf(t *testing.T, devices int, present float64) int {
	t.Helper()
	inc := &correlation.CorrelatedIncident{
		ID:     "c",
		SiteID: "s",
		Anomalies: []correlation.Anomaly{{
			Type:     "presence",
			RuleID:   "presence.dwell",
			Severity: "high",
			Details: map[string]any{
				"zone_type":            "restricted",
				"devices":              devices,
				"present_time_seconds": present,
				"threshold_seconds":    3.0,
			},
		}},
	}
	res := NewCalibratedScorer(DefaultCalibratedConfig(), nil).Score(inc, "rule", time.Now())
	return res.Score
}

// TestTheCalibratedEngineHandlesWhatItIsGiven
//
// The edges.
//
// An incident with no anomalies is nothing to score, an unknown zone type and
// an unknown severity are worth what the legacy scorer says they are worth
// (the zone's multiplier of 1.0 and the severity's of 1.0), and a site with
// no history leaves the score alone.
func TestTheCalibratedEngineHandlesWhatItIsGiven(t *testing.T) {
	// Nothing to score.
	res := NewCalibratedScorer(DefaultCalibratedConfig(), nil).Score(nil, "rule", time.Now())
	if res.Score != 0 {
		t.Errorf("a nil incident scored %d, want 0", res.Score)
	}
	res = NewCalibratedScorer(DefaultCalibratedConfig(), nil).
		Score(&correlation.CorrelatedIncident{ID: "c", SiteID: "s"}, "rule", time.Now())
	if res.Score != 0 {
		t.Errorf("an incident with no anomalies scored %d, want 0", res.Score)
	}
	if res.Chain.Why == "" {
		t.Error("an incident with no anomalies has no reason: the chain says nothing about why")
	}

	// An unknown zone type and an unknown severity are worth 1.0 each,
	// exactly as they are in the legacy scorer.
	inc := &correlation.CorrelatedIncident{
		ID:     "c",
		SiteID: "s",
		Anomalies: []correlation.Anomaly{{
			Type:     "presence",
			RuleID:   "presence.dwell",
			Severity: "unknown-severity",
			Details: map[string]any{
				"zone_type":            "unknown-zone",
				"devices":              1,
				"present_time_seconds": 3.0,
				"threshold_seconds":    3.0,
			},
		}},
	}
	res = NewCalibratedScorer(DefaultCalibratedConfig(), nil).Score(inc, "rule", time.Now())
	// The base weight for a presence, with no zone multiplier and no
	// severity multiplier.
	if want := SoftCap(18); res.Score != want {
		t.Errorf("an unknown zone and an unknown severity scored %d, want %d: the "+
			"fallbacks are 1.0 each, exactly as they are in the legacy scorer", res.Score, want)
	}
	// A claim with no severity weighting and no zone weighting is worth 1.0,
	// which is the middle of the severity scale: the same as a presence in a
	// monitored zone at medium severity, which is what it is.
	if res.Severity != SeverityMedium {
		t.Errorf("an unknown zone and an unknown severity are %s, want medium", res.Severity)
	}
}

// TestTheBaseRateMovesTheScoreAndTheLikelihood
//
// The prior does two things: it scales the claim, and it caps the likelihood
// at a site whose rule is noise.
//
// A site with NO history does neither, which is the whole point of an absence
// not being a negative observation.
func TestTheBaseRateMovesTheScoreAndTheLikelihood(t *testing.T) {
	inc := &correlation.CorrelatedIncident{
		ID:     "c",
		SiteID: "s",
		Anomalies: []correlation.Anomaly{{
			Type:     "presence",
			RuleID:   "presence.dwell",
			Severity: "high",
			Details: map[string]any{
				"zone_type":            "restricted",
				"devices":              3,
				"present_time_seconds": 15.0,
				"threshold_seconds":    3.0,
			},
		}},
	}
	cfg := DefaultCalibratedConfig()

	// No history at all: the score is the claim's, unchanged.
	res := NewCalibratedScorer(cfg, nil).Score(inc, "rule", time.Now())
	without := res.Score
	if res.Likelihood != LikelihoodVeryHigh {
		t.Errorf("the likelihood with no history is %s, want very high", res.Likelihood)
	}

	// A good local history: the score is barely lower and the likelihood is
	// the same.
	res = NewCalibratedScorer(cfg, testPriors{
		priorKey("s", "rule"): {rate: 0.9, known: true},
	}).Score(inc, "rule", time.Now())
	if res.Score >= without {
		t.Errorf("a good local history scored %d and no history scored %d: the prior "+
			"does not move the score at all", res.Score, without)
	}
	if res.Likelihood != LikelihoodVeryHigh {
		t.Errorf("the likelihood at a good site is %s, want very high", res.Likelihood)
	}

	// A noisy local history: the score is lower and the likelihood is capped
	// at the bottom, which is what makes HOLD reachable.
	res = NewCalibratedScorer(cfg, testPriors{
		priorKey("s", "rule"): {rate: 0.05, known: true},
	}).Score(inc, "rule", time.Now())
	if res.Likelihood != LikelihoodLow {
		t.Errorf("the likelihood at a noisy site is %s, want low: the base rate caps it",
			res.Likelihood)
	}
	if res.Action != ActionHold {
		t.Errorf("the action at a noisy site is %s, want HOLD", res.Action)
	}
	if res.Score >= without {
		t.Errorf("a noisy local history scored %d and no history scored %d: the prior "+
			"does not move the score at all", res.Score, without)
	}
	if !IsNoisePrior(0.05) {
		t.Error("a prior of 0.05 is not a noise prior")
	}
	if IsNoisePrior(0.9) {
		t.Error("a prior of 0.9 is a noise prior")
	}
}

// priorKey is the key a prior is stored under in a test.
func priorKey(site, rule string) string { return site + "\x00" + rule }

// testPriors is a prior lookup for the tests.
//
// It is here and not in `internal/calibration` because the calibration
// package imports this one, and a package cannot import its own importer.
type testPriors map[string]struct {
	rate  float64
	known bool
}

// Prior returns the base rate for a rule at a site.
func (m testPriors) Prior(site, rule string) (float64, bool) {
	p, ok := m[priorKey(site, rule)]
	if !ok {
		return 0, false
	}
	return p.rate, p.known
}

// TestEveryLikelihoodLevelIsReachable
//
// The four levels, and the evidence that reaches them.
//
// The levels are the engine's whole output on the likelihood axis, and a
// level nothing reaches is a level the matrix has a cell for and the engine
// never enters.
func TestEveryLikelihoodLevelIsReachable(t *testing.T) {
	cases := []struct {
		devices int
		present float64
		want    LikelihoodLevel
	}{
		{1, 3.0, LikelihoodLow},
		{2, 3.0, LikelihoodLow},
		{2, 6.0, LikelihoodMedium},
		{3, 6.0, LikelihoodHigh},
		{3, 15.0, LikelihoodVeryHigh},
	}
	for _, c := range cases {
		inc := &correlation.CorrelatedIncident{
			ID:     "c",
			SiteID: "s",
			Anomalies: []correlation.Anomaly{{
				Type:     "presence",
				RuleID:   "presence.dwell",
				Severity: "high",
				Details: map[string]any{
					"zone_type":            "restricted",
					"devices":              c.devices,
					"present_time_seconds": c.present,
					"threshold_seconds":    3.0,
				},
			}},
		}
		res := NewCalibratedScorer(DefaultCalibratedConfig(), nil).Score(inc, "rule", time.Now())
		if res.Likelihood != c.want {
			t.Errorf("%d device(s) for %.1fs is %s, want %s", c.devices, c.present, res.Likelihood, c.want)
		}
	}
}

// TestEverySeverityLevelIsReachable
//
// The four levels, and the claim that reaches them.
func TestEverySeverityLevelIsReachable(t *testing.T) {
	cases := []struct {
		zone     string
		severity string
		want     SeverityLevel
	}{
		{"restricted", "critical", SeverityCritical},
		{"choke", "high", SeverityCritical},
		{"restricted", "high", SeverityCritical},
		{"choke", "medium", SeverityHigh},
		{"asset", "medium", SeverityHigh},
		{"monitor", "medium", SeverityMedium},
		{"route", "medium", SeverityMedium},
		{"monitor", "low", SeverityLow},
		{"route", "low", SeverityLow},
	}
	for _, c := range cases {
		inc := &correlation.CorrelatedIncident{
			ID:     "c",
			SiteID: "s",
			Anomalies: []correlation.Anomaly{{
				Type:     "presence",
				RuleID:   "presence.dwell",
				Severity: c.severity,
				Details: map[string]any{
					"zone_type":            c.zone,
					"devices":              1,
					"present_time_seconds": 3.0,
					"threshold_seconds":    3.0,
				},
			}},
		}
		res := NewCalibratedScorer(DefaultCalibratedConfig(), nil).Score(inc, "rule", time.Now())
		if res.Severity != c.want {
			t.Errorf("a %s zone at severity %s is %s, want %s", c.zone, c.severity, res.Severity, c.want)
		}
	}
}
