package rules

// The priority function, and the negative specification it carries.
//
// Most of a priority function's tests are arithmetic: with these weights and
// those factors, this number. Those are here, and they are the boring half.
//
// The half that matters is the set of things the function must NOT do. A ranking
// function with a wrong weight produces a badly ordered queue; a ranking function
// with a forbidden input produces a queue nobody can defend, and it produces it
// while passing every arithmetic test written for it. So the tests below are
// mostly about ABSENCE.

import (
	"testing"

	"github.com/Sergey-Bar/vision/internal/camerahealth"
)

// baseFactors is a reasonable middle-of-the-road incident, and every test breaks
// exactly one thing from it.
func baseFactors() PriorityFactors {
	return PriorityFactors{
		ClassWeight:   0.5,
		Severity:      "medium",
		ZoneWeight:    0.5,
		Cameras:       []string{camerahealth.Fresh},
		Suppressed:    false,
		Corroboration: Unverified,
	}
}

// TestTheArithmeticIsTheDocumentedSketch is the positive half: the function
// computes the §5.2 expression it documents, normalised by the weight available,
// and a term missing from the sum is a finding rather than a preference.
func TestTheArithmeticIsTheDocumentedSketch(t *testing.T) {
	w := DefaultPriorityWeights
	f := baseFactors()

	// Hand-computed from the documented expression: class 0.5 at weight 1,
	// medium's severity rank 3 at weight 2, zone 0.5 at weight 0.5, state 1 at
	// weight 1, unverified's rank 0 at weight 0.75, and a healthy camera's
	// penalty 0 at weight 1.
	raw := w.Class*0.5 + w.Sev*3 + w.Zone*0.5 + w.State*1 + w.Corr*0
	want := raw / TotalWeight(w) * MaxPriority
	got := Priority(f, w)
	if got < want-0.0001 || got > want+0.0001 {
		t.Fatalf("Priority = %.4f, want %.4f (raw %.4f over a total weight of %.4f). The expression, the normalisation and the sketch have drifted apart",
			got, want, raw, TotalWeight(w))
	}
	if got >= MaxPriority {
		t.Fatalf("a middle-of-the-road incident ranks %.4f, at the top of the scale. A function whose ordinary case saturates has one row in its queue", got)
	}
}

// TestEveryFactorMovesTheAnswer checks that none of the six is decorative.
//
// A weight on an input nothing reads is a weight a reader believes exists, and
// this is the cheapest place to catch that.
func TestEveryFactorMovesTheAnswer(t *testing.T) {
	base := Priority(baseFactors(), DefaultPriorityWeights)
	cases := map[string]func(*PriorityFactors){
		"class weight":   func(f *PriorityFactors) { f.ClassWeight = 1 },
		"severity":       func(f *PriorityFactors) { f.Severity = "critical" },
		"zone weight":    func(f *PriorityFactors) { f.ZoneWeight = 1 },
		"corroboration":  func(f *PriorityFactors) { f.Corroboration = Corroborated },
		"camera can see": func(f *PriorityFactors) { f.Cameras = []string{camerahealth.Blind} },
		"suppressed":     func(f *PriorityFactors) { f.Suppressed = true },
	}
	for name, break1 := range cases {
		f := baseFactors()
		break1(&f)
		if Priority(f, DefaultPriorityWeights) == base {
			t.Errorf("changing the %s did not change the priority, so the weight on it is decorative", name)
		}
	}
}

// TestAnAnomalousInputDoesNotRaisePriority is THE CONTROL, and it is the one the
// autonomous plan names against its own overflow: "abnormal = dangerous" (M6).
//
// It is written as a property rather than as a single case because the property
// is what makes the control general: for the SAME stored facts, an input being
// more or less unusual for its site must not move the ranking. A single case
// would be satisfied by a scaling coincidence.
//
// The way to break this test is to add the anomaly term back into the sum — the
// term the file documents as deliberately absent, with the REDCHECK line above
// it. There is no field for it in PriorityFactors, so adding it costs two edits
// (a field and a term), and that is deliberate: the second edit is the one a
// reviewer sees.
func TestAnAnomalousInputDoesNotRaisePriority(t *testing.T) {
	// The SAME incident, described three ways, differing ONLY in how unusual it
	// is for this site: entirely routine, unusual for the hour, and the most
	// unusual thing the site has recorded. All three must rank identically,
	// because how unusual something is is MEASURED and PUBLISHED — in
	// `internal/operatormetrics`, beside the verification slice, where it is
	// honest about what it is — and is not a prior on how much an operator
	// should care.
	ordinary := baseFactors()
	ordinary.AnomalyScore = 0
	unusual := baseFactors()
	unusual.AnomalyScore = 0.5
	neverBefore := baseFactors()
	neverBefore.AnomalyScore = 1

	got := []float64{
		Priority(ordinary, DefaultPriorityWeights),
		Priority(unusual, DefaultPriorityWeights),
		Priority(neverBefore, DefaultPriorityWeights),
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Fatalf("the same incident ranks %.4f and %.4f depending on how unusual it is for its site. A priority that moves with anomaly is M6: the leaf that crosses the driveway ranks above the crowd crossing the road", got[0], got[i])
		}
	}

	// And the property stated from the other side: an incident that is entirely
	// ORDINARY and corroborated must outrank one that is UNUSUAL and
	// contradicted. If anomaly ever enters the sum, this becomes false and the
	// assertion above is no longer the only thing standing between the two.
	crowd := baseFactors()
	crowd.AnomalyScore = 0
	crowd.Corroboration = Corroborated
	crowd.Severity = "high"
	leaf := baseFactors()
	leaf.AnomalyScore = 1
	leaf.Corroboration = Contradicted
	leaf.Severity = "low"
	if Priority(crowd, DefaultPriorityWeights) <= Priority(leaf, DefaultPriorityWeights) {
		t.Fatal("a corroborated high-severity incident does not outrank an unusual, contradicted low-severity one")
	}
}

// TestABlindCameraSuppressesAndNeverCreates is the other forbidden direction.
//
// A camera that cannot see is a fact about the evidence, not about the event. It
// must reduce the ranking — and it can never raise one, because "we could not
// see" is not a reason to escalate anything.
func TestABlindCameraSuppressesAndNeverCreates(t *testing.T) {
	seen := baseFactors()
	seen.Cameras = []string{camerahealth.Fresh}
	fresh := Priority(seen, DefaultPriorityWeights)

	blind := baseFactors()
	blind.Cameras = []string{camerahealth.Blind}
	if got := Priority(blind, DefaultPriorityWeights); got >= fresh {
		t.Fatalf("a blind camera left the priority at %.4f against %.4f for one that could see. The penalty must reduce the ranking, and it did not", got, fresh)
	}

	// The direction: no camera handles capacity to turn the penalty into a
	// bonus. A camera list of every broken state must still rank BELOW the same
	// incident on a healthy camera, not above it.
	worst := baseFactors()
	worst.Cameras = []string{camerahealth.Silent, camerahealth.Blind, "not-a-state", "unknown"}
	if got := Priority(worst, DefaultPriorityWeights); got >= fresh {
		t.Fatalf("an incident from a silent, blind and unknown camera ranks %.4f, at or above %.4f for a healthy one. The penalty has become a bonus, which is the whole failure this function exists to avoid", got, fresh)
	}
}

// TestNoCameraInformationIsTheWorstCaseAndNotTheBest is the failure mode of a
// missing measurement, and it is the same rule the report keeps re-learning: an
// empty set is not a zero set.
func TestNoCameraInformationIsTheWorstCaseAndNotTheBest(t *testing.T) {
	measured := baseFactors()
	measured.Cameras = []string{camerahealth.Fresh}
	unmeasured := baseFactors()
	unmeasured.Cameras = nil

	if got := Priority(unmeasured, DefaultPriorityWeights); got >= Priority(measured, DefaultPriorityWeights) {
		t.Fatalf("an incident whose cameras were never measured ranks %.4f, at or above %.4f for one measured fresh. An unmeasured camera must never read as a healthy one", got, Priority(measured, DefaultPriorityWeights))
	}
}

// TestASuppressedIncidentRanksAtTheBottom is the state term.
//
// A suppressed incident is an ingest decision working, and it still has to
// produce a number because the metrics report suppressed totals. Zero is the
// number: anything above it is an incident that can be sorted back into view.
func TestASuppressedIncidentRanksAtTheBottom(t *testing.T) {
	f := baseFactors()
	f.Suppressed = true
	f.Severity = "critical"
	f.Corroboration = Corroborated
	f.ClassWeight = 1
	f.ZoneWeight = 1
	if got := Priority(f, DefaultPriorityWeights); got != 0 {
		t.Fatalf("a suppressed critical incident ranks %.4f, want 0: a suppressed incident that ranks anywhere above the bottom is one that can be sorted back into view", got)
	}
}

// TestTheCorroborationOrderIsTheDocumentedOne is the rank, and it is checked
// against the CLAIMED order rather than against whatever the arithmetic happens
// to produce.
func TestTheCorroborationOrderIsTheDocumentedOne(t *testing.T) {
	order := []EvidenceState{Corroborated, Unverified, Contradicted}
	seen := map[EvidenceState]float64{}
	for _, state := range order {
		f := baseFactors()
		f.Corroboration = state
		seen[state] = Priority(f, DefaultPriorityWeights)
	}
	if !(seen[Corroborated] > seen[Unverified] && seen[Unverified] > seen[Contradicted]) {
		t.Fatalf("the corroboration order is %v, want corroborated > unverified > contradicted", seen)
	}
}

// TestAnUnrankableCorroborationStateIsRefused covers the closed set.
//
// A state outside the three is a protocol error, and it is refused rather than
// ranked as unverified — because ranking it silently puts an unrankable incident
// in the middle of a queue, and its position is then a fact about a typo.
func TestAnUnrankableCorroborationStateIsRefused(t *testing.T) {
	for _, s := range []EvidenceState{Corroborated, Unverified, Contradicted} {
		if !s.Valid() {
			t.Errorf("%q is one of the three and reports itself invalid", s)
		}
	}
	for _, s := range []EvidenceState{"", "duplicate", "suppressed", "corroborated-ish"} {
		if s.Valid() {
			t.Errorf("%q is not one of the three and reports itself valid; an unrankable state would be ranked as unverified", s)
		}
	}
}

// TestAWeightTableFromAnotherVersionIsRefused is the version guard, and it
// exists because the arithmetic is about to move into the site config.
//
// Applying another version's weights would produce a number that looks like this
// version's, and a queue whose ranking cannot be re-derived cannot be defended in
// an audit. The plan is explicit that changing the weights is a change with a
// diff; this is the half of that which is checkable in code.
func TestAWeightTableFromAnotherVersionIsRefused(t *testing.T) {
	w := DefaultPriorityWeights
	w.Version = PriorityVersion + 1
	if got := Priority(baseFactors(), w); got != 0 {
		t.Fatalf("a weight table from version %d produced %.4f, want 0: the answer would be indistinguishable from this version's", w.Version, got)
	}
}

// TestTheAnswerIsClampedToTheScale is the ranking number's bound.
//
// It exists so nobody invents a normalisation at the call site, which is how a
// ranking number becomes a probability in one place and a percentage in another.
func TestTheAnswerIsClampedToTheScale(t *testing.T) {
	f := baseFactors()
	f.ClassWeight = 10
	f.ZoneWeight = 10
	f.Severity = "critical"
	f.Corroboration = Corroborated
	if got := Priority(f, DefaultPriorityWeights); got > MaxPriority {
		t.Fatalf("Priority = %.4f, above MaxPriority %d. A ranking number outside its scale is a percentage waiting to happen", got, MaxPriority)
	}
	f.ClassWeight = -10
	f.ZoneWeight = -10
	f.Severity = "info"
	if got := Priority(f, DefaultPriorityWeights); got < 0 {
		t.Fatalf("Priority = %.4f, below zero", got)
	}
}
