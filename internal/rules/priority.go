package rules

import (
	"github.com/Sergey-Bar/vision/internal/camerahealth"
)

// The exception-queue priority function.
//
// This is P2.2 of the autonomous plan, and it is written here rather than in its
// eventual home because the weights it needs belong in
// `contracts/json-schema/site-config.schema.json` and a contract change needs a
// human to read the diff. `docs/status.md` says so and is right. What does NOT
// need the contract is the arithmetic and — more importantly — the control that
// decides what the arithmetic may never read, because a priority function whose
// INPUTS are wrong is worse than one whose weights are wrong: weights can be
// tuned, and an input nobody audited is a ranking nobody can explain.
//
// The sketch this implements is the autonomous plan's §5.2:
//
//	priority = w_class × class_weight(site, behaviour)   # from the site config
//	         + w_sev   × incident.severity                # the activated bundle, NOT a model
//	         + w_zone  × zone_weight(site, zone)          # restricted areas, asset proximity
//	         + w_health× health_penalty(cameras involved) # a blind camera suppresses, never creates
//	         + w_state × 0 if suppressed | 1 otherwise
//	         + w_corr  × corroboration_state              # corroborated > unverified > contradicted
//
// # What it may never read
//
// The plan names three, and they are the same refusal three times over:
//
//   - the operator's past agreement with the model used as a RANKING PRIOR.
//     `ai-and-data.md` §7.3: "the operator agreed last time" is a MEASUREMENT,
//     not a prior. A queue that learns what an operator clicks ranks what an
//     operator clicks, and the operator stops being shown the thing they have
//     not clicked yet — which is the only thing the queue is for.
//   - any baseline or drift score. See §5.4 and the control below.
//   - anything about a person. §4.1.
//
// The second one is the expensive one, and it is the reason the control below is
// a line of code rather than a review note.

// PriorityVersion is the version of the arithmetic, carried with the answer.
//
// A priority that cannot say which version of itself produced it cannot be
// re-derived, and an exception queue nobody can replay is an exception queue
// nobody can defend in an audit. The weights move to site config when the schema
// is signed; this number changes when they do, and changing it is a diff.
const PriorityVersion = 1

// EvidenceState is where an incident's evidence stands, and it is a CLOSED SET
// with a rank rather than a score.
//
// The vocabulary is `verification_results.state` seen from the ranking side,
// narrowed to the three that are about EVIDENCE: a corroborated incident outranks
// an unverified one, and both outrank a contradicted one. `duplicate` and
// `suppressed` are not here — they are the states that decide whether an incident
// reaches the queue at all, and folding them in would rank an ingest success
// above its own cause.
type EvidenceState string

const (
	// Corroborated is the evidence agreeing with itself, and it is the top of
	// the three.
	Corroborated EvidenceState = "corroborated"
	// Unverified is the evidence not disagreeing and nothing supporting it, and
	// it is the middle. This is the state most incidents will be in most of the
	// time, which is why it ranks where it does rather than at zero.
	Unverified EvidenceState = "unverified"
	// Contradicted is the evidence disagreeing with itself, and it is the bottom.
	// It is not the floor by convention: a contradicted incident outranks a
	// suppressed one because a suppressed incident is not in the queue at all.
	Contradicted EvidenceState = "contradicted"
)

// Valid reports whether a corroboration state is one of the three.
//
// An unknown state is refused rather than ranked: an unrankable incident in the
// middle of a queue is worse than one at the bottom, because its position is then
// a fact about a typo.
func (c EvidenceState) Valid() bool {
	switch c {
	case Corroborated, Unverified, Contradicted:
		return true
	}
	return false
}

// rank orders the corroboration states, and it is the only place that ordering
// exists so two call sites cannot order them differently.
func (c EvidenceState) rank() float64 {
	switch c {
	case Corroborated:
		return 1
	case Unverified:
		return 0
	case Contradicted:
		return -1
	}
	return 0
}

// PriorityFactors is the whole input to the priority function, and the fact that
// it has SIX fields and no seventh is the design.
//
// Every field is a stored, versioned fact. There is no field for how unusual the
// event is for this site, because that is the one input the plan forbids and the
// one input every novelty-triggered system reaches for.
type PriorityFactors struct {
	// ClassWeight is what the site's behaviour rules say this class is worth,
	// as a number in [0,1]. It is a site decision and not a model's opinion.
	ClassWeight float64
	// Severity is the ACTIVATED RULE BUNDLE's severity, not a model's. It is
	// one of the closed Severities set, validated by the compiler before it
	// reaches here.
	Severity string
	// ZoneWeight is what the site says about the zone: restricted areas and
	// asset proximity, in [0,1].
	ZoneWeight float64
	// Cameras is the health of every camera the incident was derived from, in
	// the camerahealth vocabulary. A blind camera suppresses priority; it never
	// creates it.
	Cameras []string
	// Suppressed is whether this incident was suppressed by an earlier decision.
	// A suppressed incident is not in the queue, and the factor exists so the
	// arithmetic accounts for it rather than being asked to explain it.
	Suppressed bool
	// Corroboration is where the evidence stands, and it is ranked rather than
	// scored.
	Corroboration EvidenceState

	// AnomalyScore is how unusual this detection is for this site, on [0,1],
	// and it is CARRIED AND DELIBERATELY NOT RANKED.
	//
	// The field exists so the refusal can fail. See `anomalyCoefficient` below:
	// a forbidden input that is not represented at all has no control, and a
	// refusal without a control is a comment. This one has a coefficient, the
	// coefficient is zero, and the red-check that turns it to 1 is what proves
	// the refusal is load-bearing rather than hopeful.
	AnomalyScore float64
}

// PriorityWeights is the weight table, versioned.
//
// They live HERE, in code, and not in the site config, because the site config's
// schema is a contract and this is not the commit that changes it. The plan is
// explicit that the weights belong in a versioned config so a change is a diff;
// this struct is that config's in-memory form, and when the schema is signed the
// defaults move and nothing else changes. Making them a table rather than six
// literals at the call site is what keeps that move possible.
type PriorityWeights struct {
	Class   float64
	Sev     float64
	Zone    float64
	Health  float64
	State   float64
	Corr    float64
	Version int
}

// DefaultPriorityWeights is the version-1 table.
//
// The numbers are starting points, and they are stated rather than left
// implicit because a weight nobody wrote down is a weight nobody reviews. Class
// outweighs zone: what an object IS is worth more than where it is, which is the
// ordering a site operator expects and the one that survives a camera move.
// Severity outweighs class because the bundle is the site's own decision.
var DefaultPriorityWeights = PriorityWeights{
	Class:   1.0,
	Sev:     2.0,
	Zone:    0.5,
	Health:  1.0,
	State:   1.0,
	Corr:    0.75,
	Version: PriorityVersion,
}

// Priority computes the ranking number for one incident, with the weights given.
//
// The answer is the weighted sum, divided by the total weight available, times
// MaxPriority — so it is in [0, MaxPriority] BY CONSTRUCTION and needs no clamp.
//
// That normalisation is not cosmetic, and the first version of this function did
// not have it: it clamped the raw sum to [0,1] and returned 100 for a medium
// incident, because the raw sum of six terms against reasonable weights is
// already above 1. Every incident ranked 100, every severity ranked 100, and the
// function passed its own arithmetic test while being a queue with one row in it.
// A scale where everything saturates is a scale where nothing ranks, and it is
// the same defect as the one this file exists to prevent, arrived at from the
// other end.
//
// The health term is the only one that SUBTRACTS, and it can only reduce the
// answer, which is the whole of "a blind camera suppresses priority, never
// creates it": that a camera cannot see is not a reason anything it saw is
// important.
func Priority(f PriorityFactors, w PriorityWeights) float64 {
	if w.Version != PriorityVersion {
		// A weight table from another version of the arithmetic is refused
		// rather than applied, because applying it would produce a number that
		// looks like this version's.
		return 0
	}
	if f.Suppressed {
		// A suppressed incident is not in the queue. The arithmetic still has
		// to produce a number for one — the metrics report suppressed totals —
		// and the number is zero rather than a small positive, because a
		// suppressed incident that ranks anywhere above the bottom is an
		// incident that can be sorted back into view.
		return 0
	}
	raw := w.Class*clamp01(f.ClassWeight) +
		w.Sev*float64(severityRank(f.Severity)) +
		w.Zone*clamp01(f.ZoneWeight) +
		w.State*1 +
		w.Corr*clamp11(f.Corroboration.rank()) +
		anomalyCoefficient*clamp01(f.AnomalyScore) -
		w.Health*clamp01(healthPenalty(f.Cameras))
	if raw < 0 {
		raw = 0
	}
	return raw / TotalWeight(w) * MaxPriority
}

// TotalWeight is the largest answer the arithmetic can produce for these
// weights, before the health penalty.
//
// It is exported because a reader checking the function's claim that the answer
// is bounded needs the denominator, and because a weight whose denominator is not
// written down is a weight a caller has to recompute — which is how two call sites
// end up with two scales.
func TotalWeight(w PriorityWeights) float64 {
	return w.Class + w.Sev*maxSeverityRank + w.Zone + w.State + w.Corr
}

// MaxPriority is the top of the scale the arithmetic produces.
//
// It exists so a caller does not invent its own normalisation, which is how a
// ranking number becomes a probability in one place and a percentage in another.
const MaxPriority = 100

// maxSeverityRank is severityRank's largest value, and it is written down rather
// than derived because it is the denominator of the normalisation: deriving it
// from the closed set means adding a severity changes what every existing
// incident's number MEANS, which is exactly what a version bump is for.
const maxSeverityRank = float64(5)

// clamp11 bounds a rank to [-1,1].
func clamp11(v float64) float64 {
	switch {
	case v < -1:
		return -1
	case v > 1:
		return 1
	}
	return v
}

// anomalyCoefficient is the coefficient on the anomaly term, and it is ZERO
// because the term is forbidden.
//
// It is stated as a number in the sum rather than left out of it, and the weight
// table has no `Anomaly` field, so turning this term back on is exactly one edit
// to exactly one line — which is the shape a red-check control has to have. The
// alternative (omitting the term entirely) would make the refusal a comment, and
// a refusal that is a comment is the failure mode this whole repository is built
// to prevent.
//
// RED-CHECK CONTROL: `anomalyCoefficient` 0 -> 1. It compiles, it changes the
// arithmetic for an anomalous input and nothing else, and
// TestAnAnomalousInputDoesNotRaisePriority goes red.
const anomalyCoefficient = 0

// healthPenalty is the derived health penalty over the cameras involved, in
// [0,1], and it is the worst camera's state rather than the average.
//
// An average is the wrong statistic here: nine healthy cameras and one blind one
// is a site where a tenth of the incident's evidence cannot be seen, not a site
// that is 90% healthy. The worst state is what suppresses.
func healthPenalty(cameras []string) float64 {
	if len(cameras) == 0 {
		// No camera information is the WORST case and not the best: an incident
		// derived from a camera nobody measured is an incident whose evidence
		// cannot be checked, and `unknown` is the camerahealth state that must
		// never be read as `fresh`.
		return 1
	}
	worst := 0.0
	for _, c := range cameras {
		if r := cameraPenalty(c); r > worst {
			worst = r
		}
	}
	return worst
}

// cameraPenalty maps one camerahealth state to its penalty, and it is the only
// place that mapping exists.
//
// `fresh` is zero and everything else is not, and `silent` and `blind` are the
// two that matter: a camera that cannot see contributed evidence a reader cannot
// verify, and that must reduce how hard the incident is pushed at an operator.
func cameraPenalty(state string) float64 {
	switch state {
	case camerahealth.Fresh:
		return 0
	case camerahealth.Stale:
		return 0.25
	case camerahealth.Blind:
		return 1
	case camerahealth.Silent:
		return 1
	}
	return 1
}

// clamp01 bounds a factor to the unit interval.
func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// ---------------------------------------------------------------------------
// What this function may never read.
//
// The rest of this file is a NEGATIVE SPECIFICATION, and it is written as code
// because a specification written as prose is a specification that drifts. Each
// entry below is a term that is NOT in the sum above, with the reason, and the
// red-check control that proves the term stays out.
// ---------------------------------------------------------------------------

// AnomalyScore is deliberately NOT A FACTOR.
//
// This is the load-bearing absence in the file. The metrics doc proposes deriving
// an anomaly score per site — how unusual a detection is for that camera, that
// hour, that class — and it would be the single most valuable number the platform
// could put on an exception queue. It is also, ranked as a priority prior, the
// "abnormal = dangerous" failure the autonomous plan names as M6: one person
// stepping off a pavement at 3am is maximally abnormal and trivial; twelve people
// crossing a road together at 8am is entirely ordinary and urgent; a leaf crossing
// a lit doorway for the first time is a novelty event and not an incident. The
// two orderings disagree on exactly the cases that matter, and a queue that ranks
// abnormality puts the leaf at the top.
//
// So the anomaly score is a MEASUREMENT the platform publishes, in
// `internal/operatormetrics`, beside the verification slice — where it is honest
// about what it is — and it is not an input here.
//
// RED-CHECK CONTROL: uncommenting the term below raises priority for an anomalous
// input at every other factor equal, and TestAnAnomalousInputDoesNotRaisePriority
// goes red. That test is the control that keeps this file from being a comment:
//
//	NOT p += w.Anomaly × clamp01(f.AnomalyScore)
//
// The weight table has no Anomaly field, so adding the term above needs the table
// changed too, and that is deliberate: a forbidden input has to cost two edits,
// and the second edit is the one a reviewer sees.
//
// OperatorAgreement is likewise not a factor. "The operator agreed last time" is a
// measurement, not a prior; a queue that ranks by it ranks what an operator
// already agrees with and never surfaces the exception the queue exists for.
//
// Anything about a person is not a factor either — not height, not gait, not
// clothing, not whether they were here before. §4.1. The class weight is the
// site's own decision about a class of object, which is a different claim from a
// judgement about a human being, and the difference is the whole of §4.1.
