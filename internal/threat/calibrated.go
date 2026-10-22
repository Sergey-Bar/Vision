package threat

import (
	"fmt"
	"math"
	"time"

	"github.com/Sergey-Bar/vision/internal/correlation"
)

// The calibrated engine.
//
// # What it changes and what it does not
//
// The CLAIM is unchanged. The same rule, in the same zone, at the same
// severity, is the same claim it was under the legacy engine, and it carries
// the same weights: the base score for the anomaly type, the zone's
// multiplier, the severity's multiplier. What changes is what the engine does
// with the claim, and there are three changes.
//
// 1. The score is a SOFT CAP rather than a truncation. The legacy engine
//    added the contributions and clipped the sum at 100, so a critical
//    presence in a restricted zone (120) and a worse one (300) were the same
//    number, and the queue could not tell them apart. The saturating curve
//    keeps them apart: 120 lands near 70 and 300 near 95, and both stay
//    inside the 0..100 the contract and the console both publish.
//
// 2. The decision is TWO-DIMENSIONAL. The legacy engine derived the severity
//    and the likelihood from ONE number with the same thresholds, so twelve
//    of the sixteen cells were unreachable and `ActionHold` was in no cell
//    at all. Here the severity is the claim (how bad it is if it is real)
//    and the likelihood is the evidence (how likely it is to be real), and
//    the two are computed from different facts.
//
// 3. The score is CALIBRATED against the site's own history. A rule that
//    fires two hundred times a day at a site and is one in fifty real is
//    noise at that site, and the engine is supposed to say so.
//
// # The base rate
//
// The prior is the site's own record of how often a rule was right there. It
// is a LABELLED history: the operator's dispositions, which say whether an
// incident was a true positive, a false positive, or benign. There is no
// other source of that truth in this product.
//
// A site with no dispositions has no prior, and a site with no prior gets
// the legacy behaviour exactly. That is not a fallback for convenience: a
// fresh install has no history, and an engine that scored a fresh install
// differently from the one it replaced would be changing behaviour for
// everybody in order to help the sites that have data.

// PriorLookup is the local base rate for one rule at one site.
//
// It is an interface so that the threat package does not depend on a
// database, and so that the evaluation can supply a corpus-derived prior
// where the product would supply a stored one.
type PriorLookup interface {
	// Prior returns the smoothed precision of the rule at the site, and
	// whether the site has any labelled history for it at all.
	//
	// A rate of zero with known false means "this site has labels and none
	// of them were a true positive", which is a fact. A rate of zero with
	// known false and known true means "there is no history", which is an
	// absence. The two must not be confused: an absence is not a negative
	// observation.
	Prior(site, rule string) (rate float64, known bool)
}

// CalibratedConfig configures the calibrated engine.
//
// The claim weights are the legacy scorer's, unchanged and embedded, because
// the claim is the same claim. The rest is what the calibrated engine adds.
type CalibratedConfig struct {
	// The claim weights. `DefaultScorerConfig` fills them.
	ScorerConfig

	// CorroborationWeight is what one further DEVICE that saw the same
	// thing is worth.
	//
	// It is the evidence the legacy engine threw away: the anomaly's
	// `Details` already carry how many devices saw it, and the scorer read
	// only the zone type and the severity.
	CorroborationWeight float64

	// DurationWeight is what one multiple of the threshold is worth. A
	// presence that lasted twice its threshold is stronger evidence than
	// one that lasted exactly its threshold, and the anomaly says how long
	// it lasted.
	DurationWeight float64

	// NoisePrior is the local precision below which a rule is noise AT THIS
	// SITE.
	//
	// A rule whose labelled history here is less precise than this is not
	// escalated on the strength of its evidence alone: the evidence is
	// about the rule and not about this incident.
	NoisePrior float64

	// PriorFloor is the fraction of its claim a rule keeps at its worst
	// prior.
	//
	// It is not zero, because a score of zero says "nothing anomalous",
	// which is a different claim from "this rule is noise here". The
	// bounded adjustment is deliberate.
	PriorFloor float64

	// HalfLife is the queue's recency decay: the score a two-hour-old
	// incident carries in the queue is half the score it was given.
	//
	// It is a property of the QUEUE and not of the incident. The stored
	// score is the raw one; the decayed one is what the queue sorts by.
	HalfLife time.Duration
}

// DefaultCalibratedConfig returns the calibrated engine's defaults.
//
// The claim weights are the legacy scorer's, so the two engines agree about
// what a claim is worth and disagree only about what to do with it.
func DefaultCalibratedConfig() CalibratedConfig {
	return CalibratedConfig{
		ScorerConfig:        DefaultScorerConfig(),
		CorroborationWeight: 12,
		DurationWeight:      10,
		NoisePrior:          0.2,
		PriorFloor:          0.4,
		HalfLife:            2 * time.Hour,
	}
}

// SoftCap maps a raw sum onto 0..100 without truncating.
//
// The curve is `100 · (1 − e^(−Σ/100))`: it is monotone, it is bounded above
// by 100 without ever reaching it, and it keeps the ORDER of the raw sums.
// The legacy engine's clip kept none of that: everything above 100 was 100.
//
// It is published rather than private because the number is a decision
// (docs/decisions.md D-14) and a reader checking the decision should not
// have to read the implementation to check the arithmetic.
func SoftCap(raw float64) int {
	if raw <= 0 || math.IsNaN(raw) {
		return 0
	}
	v := 100 * (1 - math.Exp(-raw/100))
	return int(math.Round(v))
}

// IsNoisePrior reports whether a local precision means the rule is noise at
// that site.
//
// It is published because the threshold is a decision and a caller that has to
// know whether a site is a noisy one -- the calibration gate does -- must not
// have to read the engine's configuration to find out.
func IsNoisePrior(rate float64) bool {
	return rate < DefaultCalibratedConfig().NoisePrior
}

// CalibratedResult is what the calibrated engine produces.
//
// It carries the raw score, the two axes of the decision, and the action the
// matrix gives for that pair.
type CalibratedResult struct {
	// Score is the raw score, 0..100. This is the number stored with the
	// incident.
	Score int
	// Severity is how bad the claim is if it is real.
	Severity SeverityLevel
	// Likelihood is how likely the incident is to be real.
	Likelihood LikelihoodLevel
	// Action is what the matrix says about that pair.
	Action Action
	// Chain is the reason chain. Every number in the result appears in it.
	Chain ReasonChain
}

// CalibratedScorer is the calibrated engine.
type CalibratedScorer struct {
	cfg    CalibratedConfig
	priors PriorLookup
}

// NewCalibratedScorer creates the calibrated engine.
//
// The prior lookup may be nil, which means "no history anywhere", and the
// engine then behaves as though every rule were new.
func NewCalibratedScorer(cfg CalibratedConfig, priors PriorLookup) *CalibratedScorer {
	return &CalibratedScorer{cfg: cfg, priors: priors}
}

// baseScore is the claim's base weight for an anomaly type.
//
// It is the legacy scorer's, unchanged, and it is read through the legacy
// scorer so there is one table of weights rather than two.
func (s *CalibratedScorer) baseScore(anomalyType string) int {
	return NewScorer(s.cfg.ScorerConfig).baseScore(anomalyType)
}

// zoneMultiplier is the zone type's multiplier.
//
// An unknown zone type is 1.0, exactly as it is in the legacy scorer: a
// bundle's own rule may carry an anomaly with no zone type, and a scorer
// that panicked on it would take the autonomy layer down for one missing
// detail.
func (s *CalibratedScorer) zoneMultiplier(zoneType string) float64 {
	if m := s.cfg.ZoneTypeMultipliers[zoneType]; m != 0 {
		return m
	}
	return 1.0
}

// severityMultiplier is the severity's multiplier.
func (s *CalibratedScorer) severityMultiplier(severity string) float64 {
	if m := s.cfg.SeverityMultipliers[severity]; m != 0 {
		return m
	}
	return 1.0
}

// Score evaluates the incident and returns the calibrated result.
//
// The `ruleID` is the rule bundle's rule that raised the incident, and it is
// the key the base rate is looked up under: the base rate belongs to the
// operator's rule, not to the anomaly engine's kind.
//
// The `at` is the moment the score is computed at, and it is what the
// incident's own timestamps are measured from.
func (s *CalibratedScorer) Score(inc *correlation.CorrelatedIncident, ruleID string, at time.Time) CalibratedResult {
	res := CalibratedResult{Chain: ReasonChain{}}
	if inc == nil || len(inc.Anomalies) == 0 {
		res.Chain.Why = "no factors"
		return res
	}

	raw, severity, likelihood, chain := s.assess(inc, ruleID, at)
	res.Chain = chain
	res.Score = SoftCap(raw)
	res.Severity = severity
	res.Likelihood = likelihood

	action := DefaultMatrix().Decide(severity, likelihood)
	res.Action = action

	// The decomposition is in the chain, because an operator looking at a
	// queue asks "why is this first", and the answer is the chain rather
	// than a number.
	res.Chain.AddStep("severity", severityLabel(severity), severityFactor(severity),
		severityEvidence(inc), at)
	res.Chain.AddStep("likelihood", likelihoodLabel(likelihood), likelihoodFactor(likelihood),
		likelihoodEvidence(inc), at)
	res.Chain.AddStep("decision", string(action), 0,
		fmt.Sprintf("%s × %s → %s", severityLabel(severity), likelihoodLabel(likelihood), action), at)

	return res
}

// assess computes the raw sum, the severity, the likelihood and the chain.
func (s *CalibratedScorer) assess(inc *correlation.CorrelatedIncident, ruleID string, at time.Time) (float64, SeverityLevel, LikelihoodLevel, ReasonChain) {
	var chain ReasonChain
	raw := 0.0
	bestSeverityFactor := 0.0
	devices := 0
	excess := 0.0

	for _, a := range inc.Anomalies {
		base := s.baseScore(a.Type)
		zone := s.zoneMultiplier(detailString(a.Details, "zone_type"))
		sev := s.severityMultiplier(a.Severity)

		claim := float64(base) * zone * sev
		raw += claim

		if f := sev * zone; f > bestSeverityFactor {
			bestSeverityFactor = f
		}
		if d := detailInt(a.Details, "devices"); d > devices {
			devices = d
		}
		if e := detailExcess(a.Details); e > excess {
			excess = e
		}

		chain.AddStep(
			"presence",
			a.RuleID,
			int(math.Round(claim)),
			fmt.Sprintf("%s in a %s zone, severity %s, %.1fs of a %.1fs threshold",
				a.Type, detailString(a.Details, "zone_type"), a.Severity,
				detailFloat(a.Details, "present_time_seconds"), detailFloat(a.Details, "threshold_seconds")),
			at,
		)
	}

	// The corroboration the legacy engine threw away.
	if devices > 1 {
		corroboration := float64(devices-1) * s.cfg.CorroborationWeight
		raw += corroboration
		chain.AddStep("corroboration", "devices", int(math.Round(corroboration)),
			fmt.Sprintf("%d devices saw it", devices), at)
	}

	// The duration excess the legacy engine threw away.
	if excess > 0 {
		duration := excess * s.cfg.DurationWeight
		raw += duration
		chain.AddStep("duration", "duration excess", int(math.Round(duration)),
			fmt.Sprintf("%.1fx the threshold", excess+1), at)
	}

	// The local base rate.
	priorRate, priorKnown := 0.0, false
	if s.priors != nil {
		priorRate, priorKnown = s.priors.Prior(inc.SiteID, ruleID)
	}
	if priorKnown {
		factor := s.cfg.PriorFloor + (1-s.cfg.PriorFloor)*priorRate
		raw *= factor
		chain.AddStep("base_rate", "local base rate", 0,
			fmt.Sprintf("%.0f%% of this rule's incidents at this site were real", priorRate*100), at)
	}

	severity := severityFromFactor(bestSeverityFactor)
	likelihood := s.likelihood(devices, excess, priorRate, priorKnown)

	return raw, severity, likelihood, chain
}

// likelihood is how likely the incident is to be real.
//
// The evidence is the corroborating device count and the duration excess. The
// base rate is the site's own answer to "how often is this rule right here",
// and it CAPS the likelihood: a rule that is noise at this site stays at the
// bottom of the scale no matter how much evidence there is, because the
// evidence is about the rule rather than about this incident.
//
// That cap is what makes `ActionHold` reachable, and it is the plan's own
// sentence: the evidence can be strong and the rule can still be noise at
// this site, so the incident is held rather than escalated.
func (s *CalibratedScorer) likelihood(devices int, excess float64, priorRate float64, priorKnown bool) LikelihoodLevel {
	points := 0
	switch {
	case devices >= 3:
		points += 2
	case devices == 2:
		points++
	}
	switch {
	case excess >= 4:
		points += 2
	case excess >= 1:
		points++
	}

	level := LikelihoodLow
	switch {
	case points >= 4:
		level = LikelihoodVeryHigh
	case points >= 3:
		level = LikelihoodHigh
	case points >= 2:
		level = LikelihoodMedium
	}

	if priorKnown && priorRate < s.cfg.NoisePrior && level > LikelihoodLow {
		return LikelihoodLow
	}
	return level
}

// DecayFactor is the queue's recency factor for an incident of a given age.
//
// The score of an incident that is twice the half-life old is half the
// score, and the score of one that is ten half-lives old is a thousandth of
// it. The incident's STORED score does not change: the decay is a property of
// the queue, and an incident's score is a fact about the incident.
//
// A half-life of zero means no decay, which is what the legacy engine has.
func DecayFactor(age, halfLife time.Duration) float64 {
	if halfLife <= 0 || age <= 0 {
		return 1
	}
	return math.Pow(0.5, age.Hours()/halfLife.Hours())
}

// severityFromFactor maps the claim's factor onto the severity axis.
//
// The thresholds are the ones the design system's own severity scale uses:
// 2.0 and above is critical, 1.25 and above is high, 0.75 and above is
// medium, and below that is low.
func severityFromFactor(f float64) SeverityLevel {
	switch {
	case f >= 2.0:
		return SeverityCritical
	case f >= 1.25:
		return SeverityHigh
	case f >= 0.75:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// severityFactor is the factor a severity level is worth, for the chain.
func severityFactor(s SeverityLevel) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityHigh:
		return 2
	case SeverityMedium:
		return 1
	case SeverityLow:
		return 0
	}
	return 0
}

// likelihoodFactor is the factor a likelihood level is worth, for the chain.
func likelihoodFactor(l LikelihoodLevel) int {
	switch l {
	case LikelihoodVeryHigh:
		return 3
	case LikelihoodHigh:
		return 2
	case LikelihoodMedium:
		return 1
	case LikelihoodLow:
		return 0
	}
	return 0
}

// severityLabel is the severity level's word.
func severityLabel(s SeverityLevel) string { return string(s) }

// likelihoodLabel is the likelihood level's word.
func likelihoodLabel(l LikelihoodLevel) string { return string(l) }

// severityEvidence says what the severity was read from.
func severityEvidence(inc *correlation.CorrelatedIncident) string {
	zone := ""
	sev := ""
	for _, a := range inc.Anomalies {
		if zone == "" {
			zone = detailString(a.Details, "zone_type")
			sev = a.Severity
		}
	}
	return fmt.Sprintf("zone %q, severity %s", zone, sev)
}

// likelihoodEvidence says what the likelihood was read from.
func likelihoodEvidence(inc *correlation.CorrelatedIncident) string {
	devices := 0
	excess := 0.0
	for _, a := range inc.Anomalies {
		if d := detailInt(a.Details, "devices"); d > devices {
			devices = d
		}
		if e := detailExcess(a.Details); e > excess {
			excess = e
		}
	}
	return fmt.Sprintf("%d device(s), %.1fx the threshold", devices, excess+1)
}

// detailString reads a string out of an anomaly's details.
func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	if v, ok := details[key].(string); ok {
		return v
	}
	return ""
}

// detailInt reads an integer out of an anomaly's details.
func detailInt(details map[string]any, key string) int {
	if details == nil {
		return 0
	}
	switch v := details[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// detailFloat reads a number out of an anomaly's details.
func detailFloat(details map[string]any, key string) float64 {
	if details == nil {
		return 0
	}
	switch v := details[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

// detailExcess reads the duration excess out of an anomaly's details.
//
// The excess is how many multiples of the threshold the presence lasted
// BEYOND it: a presence of exactly the threshold has no excess, and one of
// three times the threshold has an excess of two.
func detailExcess(details map[string]any) float64 {
	if details == nil {
		return 0
	}
	present, ok1 := details["present_time_seconds"].(float64)
	threshold, ok2 := details["threshold_seconds"].(float64)
	if !ok1 || !ok2 || threshold <= 0 {
		return 0
	}
	excess := present/threshold - 1
	if excess < 0 {
		return 0
	}
	return excess
}
