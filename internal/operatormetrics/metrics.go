package operatormetrics

// The durable metric vocabulary.
//
// # Why the set is closed
//
// A metric name is a promise to whoever reads a dashboard, a release note or a
// customer report. A name that appears in one build and not the next is a
// broken promise, and a name that two packages use for two different numbers is
// worse: the reader has no way to know which one they are looking at.
//
// So the set is closed, in this file, and everything that reads or writes a
// metric name goes through it:
//
//   - the rollup writer, which computes a bucket per metric;
//   - the report, which renders one;
//   - the JSON Schema in `contracts/json-schema/metrics-report.schema.json`,
//     whose enum is checked against this set by a test;
//   - the `CHECK` constraint in migration 0029, whose list is checked against
//     this set by a test.
//
// # The compile-time part, and why it is an array
//
// A new metric that nobody handles is the failure this is for. Go cannot prove
// that a `switch` over a `string` covers every member of a set, so the set is
// an INTEGER enum and the specification table is an ARRAY whose length is
// asserted against the enum's own count at compile time:
//
//	var _ = [1]struct{}{}[len(metricSpecs)-metricCount]
//
// Add a metric to the enum without adding its specification and the index goes
// negative and the package does not compile. Add a specification without
// adding the metric and the index goes past the end and the package does not
// compile. The gap case -- a specification missing in the middle -- is caught
// by `TestEveryMetricHasASpecification`, because a zero specification has no
// name and a nameless metric is not a metric.
//
// The array is keyed by the constant rather than by position, so the order of
// the table is not load-bearing.
//
// # What a metric's specification says
//
// `RolledUp` is the one field that carries a decision rather than a fact.
//
// The rule is: **roll up what is expensive to rescan and stable to aggregate;
// compute on demand what is cheap and volatile.** The ledger and the event
// table are rolled up. Incidents and dispositions are queried directly, because
// a median of a histogram is not a median and a MTTI computed from buckets
// would be a number nobody can defend.
//
// So `mt_ti_seconds` and `mt_tv_seconds` are NOT in this set. They are not
// rolled up, and they are not metrics in this sense: they are computed on
// demand by `Queries.MTTI` and `Queries.MTTV`, and the report says so beside
// the number.

// Metric is one number in the durable metric vocabulary.
//
// It is an integer rather than a string so that the specification table can be
// an array, and an array can carry a compile-time length check. The wire name
// is `String`.
type Metric uint8

// The closed set.
//
// The order is the report's order: the denominator first, the numerator's
// components, then the outcomes, then the North Star and its companions.
const (
	// metricCanonicalEvents is the North Star's DENOMINATOR: the canonical
	// events in the window.
	//
	// It is the denominator and not the incident count on purpose. Dividing
	// attention minutes by incidents improves the number automatically as the
	// rules get more conservative, which would make the product's own
	// improvement look like the product's improvement measured in attention.
	metricCanonicalEvents Metric = iota

	// metricAttentionMinutesCapped is the North Star's NUMERATOR: operator
	// attention minutes, with each incident's share capped.
	metricAttentionMinutesCapped

	// metricAttentionMinutesUncapped is the same numerator without the cap.
	//
	// Both are reported. A capped incident is one where the uncapped number is
	// known to be wrong, which is different from one where it is right, and a
	// report that shows only the capped number hides which incidents were
	// truncated.
	metricAttentionMinutesUncapped

	// metricAttentionIncidents is the N behind the mean: how many incidents
	// contributed attention minutes.
	//
	// An average over one incident is not an average, and a reader who cannot
	// see the N cannot tell that.
	metricAttentionIncidents

	// metricIncidentsRaised is the count of incidents raised, per site and per
	// rule class.
	//
	// It is the numerator of M1 gate 5's ratio: events ingested, canonical
	// events, incidents raised, per camera, with the ratio published.
	metricIncidentsRaised

	// metricVerificationCorroborated is the count of events whose verification
	// state is CORROBORATED.
	metricVerificationCorroborated

	// metricVerificationUnverified is the count whose state is UNVERIFIED.
	//
	// The five states are all counted because they are the two an operator asks
	// about when they suspect the rules are eating incidents -- DUPLICATE and
	// SUPPRESSED -- and a report that names only three of the five answers
	// half the question.
	metricVerificationUnverified

	// metricVerificationContradicted is the count whose state is CONTRADICTED.
	metricVerificationContradicted

	// metricVerificationDuplicate is the count whose state is DUPLICATE.
	metricVerificationDuplicate

	// metricVerificationSuppressed is the count whose state is SUPPRESSED.
	metricVerificationSuppressed

	// metricDispositionsAgreed is the count of dispositions whose agreement is
	// AGREED.
	metricDispositionsAgreed

	// metricDispositionsDisagreed is the count whose agreement is DISAGREED.
	//
	// It is the numerator of `operator_override_rate`.
	metricDispositionsDisagreed

	// metricDispositionsUnsure is the count whose agreement is UNSURE.
	metricDispositionsUnsure

	// metricDispositionsNoAI is the count of dispositions with no AI
	// observation behind them.
	//
	// It is the denominator's other half: an override rate computed over
	// dispositions that HAVE an AI observation is a rate of the AI's accuracy,
	// and a site with no AI at all would otherwise report a rate of zero
	// disagreement, which reads as a perfect score.
	metricDispositionsNoAI

	// metricAIObservations is the count of AI observations recorded.
	metricAIObservations

	// metricAIObservationsDegraded is the count recorded while the AI plane
	// was degraded.
	//
	// It is reported and never folded into a rate: a degraded observation is
	// not evidence about the model, and a denominator that includes it turns
	// an outage into a score.
	metricAIObservationsDegraded

	// metricAIObservationsInsufficient is the count recorded with insufficient
	// evidence.
	//
	// Same rule as the degraded count, and for the same reason.
	metricAIObservationsInsufficient

	// metricEvidenceItemsStored is the count of evidence items stored.
	metricEvidenceItemsStored

	// metricEvidenceFetchFailed is the count of evidence fetches that failed.
	metricEvidenceFetchFailed

	// metricNorthStar is the North Star itself: attention minutes per 1000
	// canonical events.
	//
	// It is never reported without its four companions, and the report type
	// carries them so that a report missing one does not build.
	metricNorthStar

	// metricOperatorOverrideRate is the first companion: the share of
	// dispositions with an AI observation behind them where the operator
	// disagreed with the model.
	metricOperatorOverrideRate

	// metricAIFalseEscalationRate is the second companion: the share of
	// escalations that were false positives.
	metricAIFalseEscalationRate

	// metricEvidenceCompleteness is the third companion: the share of closed
	// incidents whose evidence is retrievable AND passes its hash.
	metricEvidenceCompleteness

	// metricMissedIncidentRate is the fourth companion: the share of expected
	// incidents that were not raised.
	metricMissedIncidentRate

	// metricCount is the size of the closed set. It is not a metric.
	metricCount
)

// MetricKind is what a metric's value is.
type MetricKind string

// The kinds.
//
// A closed set, because "a number" on its own is not a contract: a reader
// cannot tell a count from a rate from a duration without the name carrying
// it, and the name carries it in a way that a dashboard cannot check.
const (
	// kindCount is a number of things.
	kindCount MetricKind = "count"
	// kindMinutes is a number of minutes.
	kindMinutes MetricKind = "minutes"
	// kindRatio is a dimensionless number, and it is never a percentage: a
	// percentage without its denominator is a claim.
	kindRatio MetricKind = "ratio"
	// kindPerThousand is a ratio scaled by a thousand, which is the North
	// Star's own kind.
	kindPerThousand MetricKind = "per_thousand"
)

// MetricSpec is what the product knows about one metric.
type MetricSpec struct {
	// Metric is the metric itself, and is the key the specification table is
	// indexed by.
	Metric Metric
	// Const is the Go constant's name.
	//
	// It is in the specification because a gate has to be able to say which
	// constant a metric is, and a metric whose Go name is only knowable by
	// reading the source is a metric a gate can only check by guessing.
	Const string
	// Name is the wire name: the column value, the JSON member, the database
	// `CHECK` list, and the name a dashboard binds to.
	Name string
	// Kind is what the value is.
	Kind MetricKind
	// OnDemand says whether the number is COMPUTED by the report reader rather
	// than read out of the rollup.
	//
	// A metric is rolled up, on demand, or neither. The third is the honest
	// state of a companion that has no corpus yet, and it is the state the
	// ledger in `producer_test.go` records.
	OnDemand bool
	// RolledUp says whether the number lives in `metric_rollups`.
	//
	// A metric that is not rolled up is computed on demand from the source
	// tables, and the report says so beside the number.
	RolledUp bool
	// IsRate says whether the metric is a rate, and therefore carries an N and
	// a confidence interval.
	IsRate bool
	// Denominator is the metric the rate divides by.
	//
	// It is meaningful only when `IsRate` is true. A rate without a named
	// denominator is a number with an undisclosed denominator, which is the one
	// form of a rate nobody can check.
	Denominator Metric
	// Description is one sentence, in the words an operator would use.
	Description string
}

// metricSpecs is the specification table.
//
// It is an array rather than a map so that `len` of it is a compile-time
// constant, which is what the assertion below needs. The literal is keyed by
// the constant, so the order in the file is not load-bearing.
var metricSpecs = [...]MetricSpec{
	metricCanonicalEvents: {
		Metric: metricCanonicalEvents, Const: "metricCanonicalEvents", Name: "canonical_events", Kind: kindCount, RolledUp: true,
		Description: "canonical events in the window, per site and per rule class",
	},
	metricAttentionMinutesCapped: {
		Metric: metricAttentionMinutesCapped, Const: "metricAttentionMinutesCapped", Name: "attention_minutes_capped", Kind: kindMinutes, RolledUp: true,
		Description: "operator attention minutes, each incident's share capped",
	},
	metricAttentionMinutesUncapped: {
		Metric: metricAttentionMinutesUncapped, Const: "metricAttentionMinutesUncapped", Name: "attention_minutes_uncapped", Kind: kindMinutes, RolledUp: true,
		Description: "operator attention minutes, with no cap",
	},
	metricAttentionIncidents: {
		Metric: metricAttentionIncidents, Const: "metricAttentionIncidents", Name: "attention_incidents", Kind: kindCount, RolledUp: true,
		Description: "how many incidents contributed attention minutes",
	},
	metricIncidentsRaised: {
		Metric: metricIncidentsRaised, Const: "metricIncidentsRaised", Name: "incidents_raised", Kind: kindCount, RolledUp: true,
		Description: "incidents raised, per site and per rule class",
	},
	metricVerificationCorroborated: {
		Metric: metricVerificationCorroborated, Const: "metricVerificationCorroborated", Name: "verification_corroborated", Kind: kindCount, RolledUp: true,
		Description: "events whose verification state is CORROBORATED",
	},
	metricVerificationUnverified: {
		Metric: metricVerificationUnverified, Const: "metricVerificationUnverified", Name: "verification_unverified", Kind: kindCount, RolledUp: true,
		Description: "events whose verification state is UNVERIFIED",
	},
	metricVerificationContradicted: {
		Metric: metricVerificationContradicted, Const: "metricVerificationContradicted", Name: "verification_contradicted", Kind: kindCount, RolledUp: true,
		Description: "events whose verification state is CONTRADICTED",
	},
	metricVerificationDuplicate: {
		Metric: metricVerificationDuplicate, Const: "metricVerificationDuplicate", Name: "verification_duplicate", Kind: kindCount, RolledUp: true,
		Description: "events whose verification state is DUPLICATE",
	},
	metricVerificationSuppressed: {
		Metric: metricVerificationSuppressed, Const: "metricVerificationSuppressed", Name: "verification_suppressed", Kind: kindCount, RolledUp: true,
		Description: "events whose verification state is SUPPRESSED",
	},
	metricDispositionsAgreed: {
		Metric: metricDispositionsAgreed, Const: "metricDispositionsAgreed", Name: "dispositions_agreed", Kind: kindCount, RolledUp: true,
		Description: "dispositions whose agreement is AGREED",
	},
	metricDispositionsDisagreed: {
		Metric: metricDispositionsDisagreed, Const: "metricDispositionsDisagreed", Name: "dispositions_disagreed", Kind: kindCount, RolledUp: true,
		Description: "dispositions whose agreement is DISAGREED",
	},
	metricDispositionsUnsure: {
		Metric: metricDispositionsUnsure, Const: "metricDispositionsUnsure", Name: "dispositions_unsure", Kind: kindCount, RolledUp: true,
		Description: "dispositions whose agreement is UNSURE",
	},
	metricDispositionsNoAI: {
		Metric: metricDispositionsNoAI, Const: "metricDispositionsNoAI", Name: "dispositions_no_ai", Kind: kindCount, RolledUp: true,
		Description: "dispositions with no AI observation behind them",
	},
	metricAIObservations: {
		Metric: metricAIObservations, Const: "metricAIObservations", Name: "ai_observations", Kind: kindCount, RolledUp: true,
		Description: "AI observations recorded",
	},
	metricAIObservationsDegraded: {
		Metric: metricAIObservationsDegraded, Const: "metricAIObservationsDegraded", Name: "ai_observations_degraded", Kind: kindCount, RolledUp: true,
		Description: "AI observations recorded while the AI plane was degraded",
	},
	metricAIObservationsInsufficient: {
		Metric: metricAIObservationsInsufficient, Const: "metricAIObservationsInsufficient", Name: "ai_observations_insufficient", Kind: kindCount, RolledUp: true,
		Description: "AI observations recorded with insufficient evidence",
	},
	metricEvidenceItemsStored: {
		Metric: metricEvidenceItemsStored, Const: "metricEvidenceItemsStored", Name: "evidence_items_stored", Kind: kindCount, RolledUp: true,
		Description: "evidence items stored",
	},
	metricEvidenceFetchFailed: {
		Metric: metricEvidenceFetchFailed, Const: "metricEvidenceFetchFailed", Name: "evidence_fetch_failed", Kind: kindCount, RolledUp: true,
		Description: "evidence fetches that failed",
	},
	metricNorthStar: {
		Metric: metricNorthStar, Const: "metricNorthStar", Name: "north_star_attention_minutes_per_1000_events", Kind: kindPerThousand,
		RolledUp: false, OnDemand: true, IsRate: true, Denominator: metricCanonicalEvents,
		Description: "operator attention minutes per 1000 canonical events",
	},
	metricOperatorOverrideRate: {
		Metric: metricOperatorOverrideRate, Const: "metricOperatorOverrideRate", Name: "operator_override_rate", Kind: kindRatio,
		RolledUp: false, OnDemand: true, IsRate: true, Denominator: metricDispositionsAgreed,
		Description: "the share of AI-backed dispositions where the operator disagreed with the model",
	},
	metricAIFalseEscalationRate: {
		Metric: metricAIFalseEscalationRate, Const: "metricAIFalseEscalationRate", Name: "ai_false_escalation_rate", Kind: kindRatio,
		RolledUp: false, IsRate: true, Denominator: metricVerificationCorroborated,
		Description: "the share of escalations that were false positives",
	},
	metricEvidenceCompleteness: {
		Metric: metricEvidenceCompleteness, Const: "metricEvidenceCompleteness", Name: "evidence_completeness", Kind: kindRatio,
		RolledUp: false, IsRate: true, Denominator: metricIncidentsRaised,
		Description: "the share of closed incidents whose evidence is retrievable and passes its hash",
	},
	metricMissedIncidentRate: {
		Metric: metricMissedIncidentRate, Const: "metricMissedIncidentRate", Name: "missed_incident_rate", Kind: kindRatio,
		RolledUp: false, IsRate: true, Denominator: metricIncidentsRaised,
		Description: "the share of expected incidents that were not raised",
	},
}

// The compile-time assertion.
//
// `len` of an array is a constant expression, so this is checked when the
// package is COMPILED and not when it runs. Add a metric to the enum without
// adding its specification and the index goes negative; add a specification
// without adding the metric and the index goes past the end. Either way the
// package does not build, and a metric nobody handles cannot ship.
//
// The gap case -- a specification missing in the middle -- is a zero
// specification, and `TestEveryMetricHasASpecification` catches it.
var _ = [1]struct{}{}[len(metricSpecs)-int(metricCount)]

// AllMetrics returns the closed set, in the report's order.
//
// It exists so the set can be ASSERTED rather than described: a vocabulary
// that is a list in two places is two vocabularies, and they drift.
func AllMetrics() []Metric {
	out := make([]Metric, 0, metricCount)
	for m := Metric(0); m < metricCount; m++ {
		out = append(out, m)
	}
	return out
}

// Spec returns a metric's specification.
//
// It panics on an unknown metric rather than returning a zero specification,
// because an unknown metric is a bug in the caller and a zero specification
// would be a bug that looks like a number.
func (m Metric) Spec() MetricSpec {
	if int(m) >= len(metricSpecs) {
		panic("operatormetrics: " + m.String() + " is not in the closed metric set")
	}
	return metricSpecs[m]
}

// String is the wire name.
//
// It is the name in the `metric_rollups.metric` column, in the report's JSON,
// and in the JSON Schema's enum, and the three are checked against each other
// by tests.
func (m Metric) String() string {
	if int(m) >= len(metricSpecs) {
		return "unknown_metric"
	}
	return metricSpecs[m].Name
}

// MetricByName finds a metric by its wire name.
func MetricByName(name string) (Metric, bool) {
	for m := Metric(0); m < metricCount; m++ {
		if metricSpecs[m].Name == name {
			return m, true
		}
	}
	return 0, false
}

// MetricNames returns every wire name, in the closed set's order.
//
// It is what the report's schema enum is checked against, and what the
// database's CHECK constraint is checked against.
func MetricNames() []string {
	out := make([]string, 0, metricCount)
	for m := Metric(0); m < metricCount; m++ {
		out = append(out, metricSpecs[m].Name)
	}
	return out
}

// IsRate reports whether a metric is a rate.
//
// A rate is the only kind of number that carries an N and a confidence
// interval, and the report has to know which numbers those are.
func (m Metric) IsRate() bool { return m.Spec().IsRate }

// RolledUp reports whether a metric lives in `metric_rollups`.
func (m Metric) RolledUp() bool { return m.Spec().RolledUp }

// NorthStarMetric is the metric the product leads with.
//
// It is a named member rather than a bare constant because the North Star
// travels with four companions, and a reader who can name the North Star
// without naming the companions is exactly the reader the report must not
// serve.
func NorthStarMetric() Metric { return metricNorthStar }

// CompanionMetrics are the four metrics that travel with the North Star.
//
// The order is the report's order, and it is fixed: a report that showed them
// in a different order each run would be a report nobody could diff.
//
// A North Star without all four is a defective report, and the report type
// carries them for exactly that reason.
func CompanionMetrics() []Metric {
	return []Metric{
		metricOperatorOverrideRate,
		metricAIFalseEscalationRate,
		metricEvidenceCompleteness,
		metricMissedIncidentRate,
	}
}
