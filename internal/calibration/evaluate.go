package calibration

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/telemetry"
	"github.com/Sergey-Bar/vision/internal/threat"
)

// The measurement.
//
// # What is measured
//
// Two engines, the same cases, the same labels:
//
//   - PRECISION: of the incidents an engine escalated, how many were real.
//     A low precision is a control room that learns to ignore the screen.
//   - RECALL: of the incidents that were real, how many the engine
//     escalated. A low recall is an engine that misses the thing it was
//     built to find.
//   - CALIBRATION ERROR: how far the engine's SCORE is from the observed
//     rate of realness. A score of 80 that is right 80% of the time is
//     calibrated; a score of 80 that is right 30% of the time is a number
//     that means nothing, whatever it is called.
//   - THE ATTENTION COST: the estimated operator minutes per thousand cases.
//     It is the product's own North Star, and it is computed with the
//     telemetry package's own calculator so the number in this report and the
//     number on the dashboard are the same number.
//
// # The comparison is the point
//
// The legacy engine's numbers are reported BESIDE the calibrated engine's,
// always. A measurement of the new engine on its own says only that the new
// engine is the new engine.

const (
	// evaluationEpoch is the moment the corpus's cases are scored at.
	//
	// It is a fixed instant rather than "now" because the corpus is a fixed
	// file: a case's age is part of its features, and "now" would make the
	// golden file change every day for no reason.
	evaluationEpoch = "2026-01-01T00:00:00Z"
)

// EngineReport is one engine's measurement over the corpus.
type EngineReport struct {
	// Cases is how many cases the engine decided.
	Cases int
	// Escalated is how many the engine escalated.
	Escalated int
	// Attention is how many the engine asked a human about: an escalation or
	// a verification. Both cost somebody a minute of their life.
	Attention int
	// EscalatedTruePositives and EscalatedFalsePositives split the
	// escalations by the operator's answer.
	EscalatedTruePositives  int
	EscalatedFalsePositives int
	// AttendedTruePositives and AttendedFalsePositives split the
	// attentions the same way.
	AttendedTruePositives  int
	AttendedFalsePositives int
	// Missed is how many of the real incidents the engine did not escalate.
	Missed int
	// EscalationPrecision and EscalationRecall are over the ESCALATIONS:
	// of the incidents the engine escalated, how many were real, and of the
	// incidents that were real, how many it escalated.
	//
	// An engine that escalates nothing has a precision of zero and a recall
	// of zero, and the escalated count says why.
	EscalationPrecision float64
	EscalationRecall    float64
	// AttentionPrecision and AttentionRecall are over the ATTENTION: the
	// escalations and the verifications together.
	//
	// They are the numbers that decide whether a control room keeps looking
	// at the screen, because a verification costs an operator a minute of
	// their life exactly as an escalation does.
	AttentionPrecision float64
	AttentionRecall    float64
	// F1 is the harmonic mean of the attention precision and recall.
	F1 float64
	// CalibrationError is the expected calibration error over the score
	// bins: the mean distance between the mean score in a bin and the share
	// of real incidents in it.
	CalibrationError float64
	// AttentionMinutes is the estimated operator time the engine's decisions
	// cost, and HAMPer1000 is that per thousand cases.
	AttentionMinutes float64
	HAMPer1000       float64
	// Mismatches names the cases whose decision differs from the golden
	// file. An empty list is the pass condition.
	Mismatches []string
}

// Report is the measurement of both engines over one corpus.
type Report struct {
	// Cases is how many cases the corpus holds.
	Cases int
	// Legacy is the legacy engine's measurement.
	Legacy EngineReport
	// Calibrated is the calibrated engine's measurement.
	Calibrated EngineReport
	// CalibrationBins is how many bins the calibration error was measured
	// over.
	CalibrationBins int
}

// DecisionFor is what one engine decided for one case.
type DecisionFor struct {
	Score      int
	Action     threat.Action
	Severity   threat.SeverityLevel
	Likelihood threat.LikelihoodLevel
}

// ScoreCase scores one case with the legacy engine.
func ScoreCase(c Case) DecisionFor {
	inc := incidentFor(c)
	score, _ := threat.NewScorer(threat.DefaultScorerConfig()).Score(inc)
	action := threat.DefaultMatrix().DecideFromScore(score)
	return DecisionFor{Score: score, Action: action}
}

// ScoreCaseCalibrated scores one case with the calibrated engine.
//
// The prior is the case's own, which the corpus stores leave-one-out: it is
// the site's base rate WITHOUT this case's label.
func ScoreCaseCalibrated(c Case) DecisionFor {
	inc := incidentFor(c)
	priors := MapPrior{
		priorKey(c.Site, c.Rule): {Rate: c.Prior.Rate, Known: c.Prior.Known},
	}
	res := threat.NewCalibratedScorer(threat.DefaultCalibratedConfig(), priors).
		Score(inc, c.Rule, evaluationTime())
	return DecisionFor{
		Score:      res.Score,
		Action:     res.Action,
		Severity:   res.Severity,
		Likelihood: res.Likelihood,
	}
}

// evaluationTime is the fixed instant the corpus is scored at.
func evaluationTime() time.Time {
	t, err := time.Parse(time.RFC3339, evaluationEpoch)
	if err != nil {
		// The constant is a literal in this file, so this cannot happen
		// unless somebody edits it into something unparsable, in which case
		// the zero time is a worse answer than a panic.
		panic("calibration: the evaluation epoch is not a valid instant: " + err.Error())
	}
	return t
}

// incidentFor builds the incident a case describes.
//
// It is the adapter between the corpus's stored features and the engine's
// input, and it is the only place the two vocabularies meet.
func incidentFor(c Case) *correlation.CorrelatedIncident {
	at := evaluationTime()
	details := map[string]any{
		"present_time_seconds": c.Features.PresentSeconds,
		"threshold_seconds":    c.Features.ThresholdSeconds,
		"zone_type":            c.Features.ZoneType,
		"devices":              c.Features.Devices,
	}
	cameras := make([]string, 0, c.Features.Devices)
	for i := 0; i < c.Features.Devices; i++ {
		cameras = append(cameras, fmt.Sprintf("camera-%d", i+1))
	}
	severity := c.Features.Severity
	if severity == "" {
		severity = "medium"
	}
	anomalyType := c.Features.AnomalyType
	if anomalyType == "" {
		anomalyType = "presence"
	}
	return &correlation.CorrelatedIncident{
		ID:        c.CaseID,
		SiteID:    c.Site,
		State:     "ACTIVE",
		FirstSeen: at.Add(-time.Duration(c.Features.PresentSeconds) * time.Second),
		LastSeen:  at,
		CameraIDs: cameras,
		Anomalies: []correlation.Anomaly{{
			Type:      anomalyType,
			RuleID:    c.Rule,
			ZoneID:    "zone-" + c.Features.ZoneType,
			FirstSeen: at.Add(-time.Duration(c.Features.PresentSeconds) * time.Second),
			LastSeen:  at,
			Severity:  severity,
			Details:   details,
		}},
	}
}

// Evaluate measures both engines over a corpus.
func Evaluate(c Corpus) Report {
	rep := Report{Cases: len(c.Cases), CalibrationBins: 10}
	rep.Legacy = measure(c, false)
	rep.Calibrated = measure(c, true)
	return rep
}

// measure is one engine's measurement.
func measure(c Corpus, calibrated bool) EngineReport {
	out := EngineReport{Cases: len(c.Cases)}
	ham := &telemetry.HAMCalculator{EscalationMinutes: 5, OverrideMinutes: 10}
	var sample telemetry.Sample
	sample.TotalDetections = int64(len(c.Cases))

	for _, cs := range c.Cases {
		var d DecisionFor
		if calibrated {
			d = ScoreCaseCalibrated(cs)
		} else {
			d = ScoreCase(cs)
		}
		isReal := cs.Label == LabelTruePositive

		escalated := d.Action == threat.ActionEscalate
		attention := escalated || d.Action == threat.ActionVerify

		if escalated {
			out.Escalated++
			if isReal {
				out.EscalatedTruePositives++
			} else {
				out.EscalatedFalsePositives++
			}
		}
		if attention {
			out.Attention++
			if isReal {
				out.AttendedTruePositives++
			} else {
				out.AttendedFalsePositives++
			}
		}
		if isReal && !escalated {
			out.Missed++
		}

		// The golden file's expectation, and whether the engine met it.
		want, ok := c.Expectations[cs.CaseID]
		if ok {
			got := Decision{
				Score:      d.Score,
				Action:     string(d.Action),
				Severity:   string(d.Severity),
				Likelihood: string(d.Likelihood),
			}
			if calibrated {
				if got != want.Calibrated {
					out.Mismatches = append(out.Mismatches,
						fmt.Sprintf("%s: calibrated decided %+v, want %+v", cs.CaseID, got, want.Calibrated))
				}
			} else {
				if got != want.Legacy {
					out.Mismatches = append(out.Mismatches,
						fmt.Sprintf("%s: legacy decided %+v, want %+v", cs.CaseID, got, want.Legacy))
				}
			}
		}

		// The calibration error is computed per site, below: a score means
		// something relative to the site's own history and not on its own.
	}

	// The two precisions, the two recalls and the F1.
	//
	// A precision with no positives behind it is zero, and the count beside
	// it says why: an engine that escalated nothing has a precision of zero
	// and a recall of zero, and that is a true statement about it.
	if out.Escalated > 0 {
		out.EscalationPrecision = float64(out.EscalatedTruePositives) / float64(out.Escalated)
	}
	totalReal := out.EscalatedTruePositives + out.Missed
	if totalReal > 0 {
		out.EscalationRecall = float64(out.EscalatedTruePositives) / float64(totalReal)
	}
	if out.Attention > 0 {
		out.AttentionPrecision = float64(out.AttendedTruePositives) / float64(out.Attention)
	}
	if totalReal > 0 {
		out.AttentionRecall = float64(out.AttendedTruePositives) / float64(totalReal)
	}
	if out.AttentionPrecision+out.AttentionRecall > 0 {
		out.F1 = 2 * out.AttentionPrecision * out.AttentionRecall /
			(out.AttentionPrecision + out.AttentionRecall)
	}

	// The calibration error, WITHIN each site.
	//
	// It is measured per site because a score means something relative to
	// the site's own history and not on its own: the same 40 is a routine
	// observation at a site whose rule is right half the time and a serious
	// one at a site whose rule is right nine times in ten. A single global
	// binning would measure the corpus's mix of sites rather than either
	// engine's calibration, and the engine that is AWARE of the site's
	// history would be measured as though it were not.
	//
	// The sites' errors are averaged, weighted by how many cases each site
	// holds, so a site with one case does not outvote a site with twelve.
	out.CalibrationError = siteCalibrationError(c, calibrated)

	// The attention cost, with the telemetry package's own calculator.
	sample.TotalEscalations = int64(out.Escalated)
	sample.TotalOverrides = int64(out.EscalatedFalsePositives)
	out.HAMPer1000 = ham.CalculateHAM(sample)
	out.AttentionMinutes = out.HAMPer1000 * float64(len(c.Cases)) / 1000

	sort.Strings(out.Mismatches)
	return out
}

// siteCalibrationError is the expected calibration error, measured within
// each site and averaged over the sites.
func siteCalibrationError(c Corpus, calibrated bool) float64 {
	type bin struct {
		sum   float64
		count int
		real  int
	}
	bySite := map[string][]Case{}
	for _, cs := range c.Cases {
		bySite[cs.Site] = append(bySite[cs.Site], cs)
	}
	weighted := 0.0
	total := 0
	for _, cases := range bySite {
		bins := map[int]*bin{}
		for _, cs := range cases {
			var d DecisionFor
			if calibrated {
				d = ScoreCaseCalibrated(cs)
			} else {
				d = ScoreCase(cs)
			}
			idx := binIndex(d.Score, 10)
			b := bins[idx]
			if b == nil {
				b = &bin{}
				bins[idx] = b
			}
			b.sum += float64(d.Score)
			b.count++
			if cs.Label == LabelTruePositive {
				b.real++
			}
		}
		for _, b := range bins {
			if b.count == 0 {
				continue
			}
			mean := b.sum / float64(b.count)
			observed := float64(b.real) / float64(b.count)
			weighted += math.Abs(mean/100-observed) * float64(b.count)
			total += b.count
		}
	}
	if total == 0 {
		return 0
	}
	return weighted / float64(total)
}

// binIndex is the bin a score falls into, out of n bins over 0..100.
func binIndex(score, n int) int {
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	idx := score * n / 101
	if idx >= n {
		idx = n - 1
	}
	return idx
}

// BuildExpectations computes the golden decisions for a corpus.
//
// It is what `-update` writes, and it is a separate function so that the
// generator and the gate cannot disagree about what the engines decide.
func BuildExpectations(c Corpus) Corpus {
	expect := map[string]Expected{}
	for _, cs := range c.Cases {
		legacy := ScoreCase(cs)
		cal := ScoreCaseCalibrated(cs)
		expect[cs.CaseID] = Expected{
			Legacy: Decision{
				Score:  legacy.Score,
				Action: string(legacy.Action),
			},
			Calibrated: Decision{
				Score:      cal.Score,
				Action:     string(cal.Action),
				Severity:   string(cal.Severity),
				Likelihood: string(cal.Likelihood),
			},
		}
	}
	c.Expectations = expect
	return c
}
