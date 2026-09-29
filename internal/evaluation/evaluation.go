// Package evaluation measures whether an AI profile is any good.
//
// The harness exists because the alternative is shipping a model because it felt
// right. It scores a profile against labelled cases and reports, per profile,
// the numbers a decision to promote or demote it should be made on — and it
// treats a missing case as a failure rather than as an absence, because a metric
// that improves when you stop measuring something is not a metric.
//
// The gates a profile must pass before it may affect authoritative state are here
// as code, not as prose in a document nobody runs. That is the difference between
// a promotion gate and a suggestion.
package evaluation

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Outcome is the label a case carries, from the operator's disposition.
type Outcome string

const (
	// OutcomeTruePositive is the observation being right and an incident warranted.
	OutcomeTruePositive Outcome = "true_positive"
	// OutcomeFalsePositive is the observation being right that something happened and
	// wrong that it mattered. The rule's noise floor.
	OutcomeFalsePositive Outcome = "false_positive"
	// OutcomeBenign is nothing to report.
	OutcomeBenign Outcome = "benign"
	// OutcomeUndispositioned is no operator having said. A case nobody judged is not
	// evidence, and counting it as anything is how a harness makes a profile look
	// better than it is.
	OutcomeUndispositioned Outcome = "undispositioned"
)

// Case is one labelled scenario.
type Case struct {
	ID string
	// ProfileID is the profile the case exercises.
	ProfileID string
	Outcome   Outcome
	// PredictedRestricted is what the observation asserted. A nil pointer means the
	// observation carried no answer — absent, degraded, or insufficient — and
	// that is recorded as such rather than as a false.
	PredictedRestricted *bool
	// InSufficientEvidence records that the model said it could not tell.
	InSufficientEvidence bool
	// Degraded records that the AI plane could not answer at all.
	Degraded bool
	// HoursToDisposition is the wall time between the observation and an operator
	// judging it. The operational review is part of the evaluation: a profile
	// that is accurate and costs twenty minutes of operator attention per incident
	// has not earned its place.
	HoursToDisposition float64
}

// Observation is what a case was scored against.
type Observation struct {
	ID                   string
	ProfileID            string
	LatencyMS            int64
	Degraded             bool
	DegradedReason       string
	InsufficientEvidence bool
	// Structured is the validated output, keyed by field.
	Structured map[string]any
	// OperatorAgreed is the operator's assessment, when one exists.
	OperatorAgreed *bool
}

// Gates are the thresholds a profile must pass to be promoted to
// authoritative state.
//
// They are a struct rather than constants so a profile can be held to a stricter
// bar than another, and so the numbers are visible in a diff when they change.
type Gates struct {
	// MaxFalsePositiveRate is the noise floor. Above it an operator stops reading
	// the feed, and a feed nobody reads is worth nothing whatever its recall.
	MaxFalsePositiveRate float64
	// MinRecallOnTruePositives catches a profile that is precise by being quiet.
	MinRecallOnTruePositives float64
	// MaxMedianLatencyMS is the cost the pipeline pays. AI is not on the
	// availability path, but it is on the p95 incident-visibility path, and a
	// profile that adds thirty seconds to every incident is a profile operators
	// learn to work around.
	MaxMedianLatencyMS int64
	// MaxDisagreementRate is the share of observations an operator contradicted.
	// A high rate means either the profile is wrong or its output is not legible,
	// and both are reasons not to promote it.
	MaxDisagreementRate float64
	// MinLabelledCases rejects a profile judged on a handful of cases. A rate
	// computed from four cases is a number, not an estimate.
	MinLabelledCases int
}

// DefaultGates is the bar for a production profile in M1.
//
// The false-positive rate is the tightest of these, and deliberately so: a
// security operator who is shown noise twice a week learns to dismiss the feed,
// and everything the product does afterwards depends on them not doing that.
//
// MinLabelledCases is 200, and the arithmetic is the reason. At n=30 a profile
// landing exactly on the 0.20 false-positive gate is still consistent with a
// true rate anywhere near 37%, and a true recall of 52% passes the "at least 0.70
// recall" gate. Thirty cases is not a small sample, it is a sample too small for
// the numbers the gates are written in.
func DefaultGates() Gates {
	return Gates{
		MaxFalsePositiveRate:     0.20,
		MinRecallOnTruePositives: 0.70,
		MaxMedianLatencyMS:       8000,
		MaxDisagreementRate:      0.25,
		MinLabelledCases:         200,
	}
}

// ShadowGates is the bar for a profile being measured against live traffic
// without affecting anything. It is looser, because the point of shadow evaluation
// is to collect cases, not to promote.
//
// Its case floor is deliberately and explicitly lower than the promotion floor,
// and it is called out in the field's own documentation: a shadow run is a data
// collection exercise and must not fail for lack of cases, a promotion run is a
// decision and must not pass on a handful. The two numbers used to be 30 and 10
// against a promotion gate that thirty cases could not support.
func ShadowGates() Gates {
	g := DefaultGates()
	g.MinLabelledCases = 10
	return g
}

// ProfileReport is one profile's measured performance.
type ProfileReport struct {
	ProfileID string
	// Labeled is the count of cases an operator actually judged. Undispositioned
	// cases are counted separately and never folded into a rate.
	Labeled            int
	Undispositioned    int
	TruePositives      int
	FalsePositives     int
	Benign             int
	Degraded           int
	Insufficient       int
	DetectedRestricted int
	MissedRestricted   int
	// Answered is the count of cases the profile actually returned a judgement
	// for. It is the denominator every rate below uses, and it is smaller than
	// Labeled whenever a case was absent, degraded or insufficient. A missing AI
	// observation is never a negative observation (invariant 3), so folding those
	// cases into a denominator lowers the measured noise floor by measuring less —
	// which is the exact failure this package's own doc says it exists to prevent.
	Answered int
	// Disagreements is how many answered cases the operator contradicted.
	Disagreements int
	// DisagreementMeasurable is false when no case was both answered and
	// accompanied by an operator's verdict. An un-normalised count left in a rate
	// field renders as 500% and fails a gate for a reason that has nothing to do
	// with the profile; this flag says "not measurable" instead.
	DisagreementMeasurable bool

	FalsePositiveRate  float64
	Recall             float64
	DisagreementRate   float64
	MedianLatencyMS    int64
	HoursToDisposition float64

	// Passed is whether every gate is met. Every gate, not the average of them: a
	// profile that is precise and slow is not half-passing.
	Passed bool
	// Failures names the gates that were not met, so a promotion decision reads
	// as a list rather than as a boolean.
	Failures []string
}

// Report is the whole evaluation.
type Report struct {
	ProducedAt time.Time
	Reports    []ProfileReport
	// Promotable lists the profile ids that passed. Production promotion is a
	// separate, human decision; this says which ones are eligible.
	Promotable []string
}

// Evaluate scores every case and reports per profile.
func Evaluate(cases []Case, observations []Observation, gates Gates) Report {
	report := Report{ProducedAt: time.Now().UTC()}

	byID := make(map[string]Observation, len(observations))
	for _, obs := range observations {
		byID[obs.ID] = obs
	}

	grouped := map[string][]Case{}
	for _, c := range cases {
		grouped[c.ProfileID] = append(grouped[c.ProfileID], c)
	}

	profileIDs := make([]string, 0, len(grouped))
	for id := range grouped {
		profileIDs = append(profileIDs, id)
	}
	sort.Strings(profileIDs)

	for _, profileID := range profileIDs {
		pr := ProfileReport{ProfileID: profileID}
		var latencies []int64
		var dispositions []float64

		for _, c := range grouped[profileID] {
			obs, hasObservation := byID[c.ID]
			// The observation is the authoritative record of what the AI plane
			// did. The case's flag is what the labelling tool believed, and a
			// disagreement between the two is itself worth counting — so both are
			// read, and the observation wins.
			degraded := obs.Degraded || c.Degraded
			insufficient := obs.InsufficientEvidence || c.InSufficientEvidence
			if degraded {
				pr.Degraded++
			}
			if insufficient {
				pr.Insufficient++
			}
			if !hasObservation {
				// A case with no observation at all. It is not a miss and it is
				// not a false positive; it is an absence, and it is counted as one.
				pr.Undispositioned++
				continue
			}
			latencies = append(latencies, obs.LatencyMS)

			restricted := predictedRestricted(obs)
			if c.Outcome == OutcomeUndispositioned {
				pr.Undispositioned++
				continue
			}
			pr.Labeled++
			dispositions = append(dispositions, c.HoursToDisposition)

			if restricted == nil {
				// No judgement. Labeled counts it, because an operator did judge the
				// case; nothing else does, because there was no answer to score. Every
				// rate below divides by Answered, so a profile cannot improve its
				// numbers by declining to answer — which is the failure the package doc
				// names, and which the previous denominator (Labeled) invited.
				continue
			}
			pr.Answered++

			// The outcome sub-counts are answered counts, not label counts, for the
			// same reason: an absent case is not a false positive, and counting it as
			// one is how a profile that fails to answer is scored as noisier than a
			// profile that answers and gets it wrong.
			switch c.Outcome {
			case OutcomeTruePositive:
				pr.TruePositives++
			case OutcomeFalsePositive:
				pr.FalsePositives++
			case OutcomeBenign:
				pr.Benign++
			}

			// Recall counts only true positives. Counting every case the profile
			// flagged as restricted — including benign ones, where "restricted" is
			// simply wrong — put a numerator of 21 on a denominator of 1 and produced
			// a recall of 21.0, which passes any gate written as "at least 0.70". The
			// gate that decides M1 exit gate 4 was not measuring what it said.
			if c.Outcome == OutcomeTruePositive {
				if *restricted {
					pr.DetectedRestricted++
				} else {
					pr.MissedRestricted++
				}
			}
			if obs.OperatorAgreed != nil {
				pr.DisagreementMeasurable = true
				if *obs.OperatorAgreed != *restricted {
					pr.Disagreements++
				}
			}
		}

		pr.MedianLatencyMS = median(latencies)
		pr.HoursToDisposition = mean(dispositions)
		// Every denominator is the number of cases that produced an answer, never the
		// number of cases an operator looked at.
		pr.FalsePositiveRate = rate(pr.FalsePositives, pr.Answered)
		pr.Recall = rate(pr.DetectedRestricted, pr.TruePositives)
		if pr.DisagreementMeasurable {
			pr.DisagreementRate = rate(pr.Disagreements, pr.Answered)
		}

		pr.Failures = evaluateGates(pr, gates)
		pr.Passed = len(pr.Failures) == 0
		if pr.Passed {
			report.Promotable = append(report.Promotable, profileID)
		}
		report.Reports = append(report.Reports, pr)
	}
	return report
}

// evaluateGates returns the names of the gates a profile did not meet.
//
// Every gate is checked independently. A profile that is precise, slow, and
// disagreed with constantly has three failures, and averaging them into a score
// is how a profile nobody should ship gets promoted.
func evaluateGates(pr ProfileReport, g Gates) []string {
	var failures []string

	if pr.Labeled < g.MinLabelledCases {
		failures = append(failures, fmt.Sprintf(
			"only %d labelled case(s), need %d: a rate computed from a handful of cases is a "+
				"number, not an estimate", pr.Labeled, g.MinLabelledCases))
	}
	if pr.FalsePositiveRate > g.MaxFalsePositiveRate {
		failures = append(failures, fmt.Sprintf(
			"false-positive rate %.2f exceeds %.2f: an operator shown noise twice a week "+
				"learns to dismiss the feed", pr.FalsePositiveRate, g.MaxFalsePositiveRate))
	}
	if pr.TruePositives > 0 && pr.Recall < g.MinRecallOnTruePositives {
		failures = append(failures, fmt.Sprintf(
			"recall %.2f below %.2f: the profile is precise by being quiet",
			pr.Recall, g.MinRecallOnTruePositives))
	}
	if pr.MedianLatencyMS > g.MaxMedianLatencyMS {
		failures = append(failures, fmt.Sprintf(
			"median latency %dms exceeds %dms: the profile sits on the p95 incident-visibility path",
			pr.MedianLatencyMS, g.MaxMedianLatencyMS))
	}
	if pr.DisagreementMeasurable && pr.DisagreementRate > g.MaxDisagreementRate {
		failures = append(failures, fmt.Sprintf(
			"disagreement rate %.2f exceeds %.2f: either the profile is wrong or its output "+
				"is not legible, and both are reasons not to promote it",
			pr.DisagreementRate, g.MaxDisagreementRate))
	}
	return failures
}

// predictedRestricted reads the structured field the production profile emits.
//
// A missing field, a degraded observation and an insufficient-evidence
// observation all yield nil, which the caller counts as an absence rather than as
// a negative. That is the invariant, expressed once.
func predictedRestricted(obs Observation) *bool {
	if obs.Degraded || obs.InsufficientEvidence {
		return nil
	}
	raw, ok := obs.Structured["restrictedZoneBreach"]
	if !ok {
		return nil
	}
	b, ok := raw.(bool)
	if !ok {
		return nil
	}
	return &b
}

// rate is the promotion contract in one place. A zero denominator is 0, never a
// divide by zero and never a leftover count: a rate field holding an
// un-normalised integer is a number that means nothing and reads as a real
// measurement.
func rate(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

// Runner executes a profile against a case set through the AI plane.
//
// It is an interface so the harness can be tested with a deterministic stand-in
// and run for real without either pretending to be the other.
type Runner interface {
	Run(ctx context.Context, cases []Case) ([]Observation, error)
}
