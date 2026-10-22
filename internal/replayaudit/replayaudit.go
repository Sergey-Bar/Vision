// Package replayaudit measures the product against the corpus it ships.
//
// # What it answers
//
// "When the committed scenarios are replayed through the product, does it raise
// the incidents the scenarios are there to prove?" The answer is the share of
// expected incidents that were NOT raised, with its N and its interval.
//
// # Why the expectation has to be authored
//
// A rate computed by replaying the corpus and counting what it raised would be
// a rate of one, always: everything raised was expected by definition, and
// everything not raised would be "not expected". That is a number that cannot
// fail.
//
// So the expectation is authored from what each scenario is FOR, and
// `internal/fixture` holds the table. A scenario with no expectation is a GAP,
// and a corpus with gaps reports the gaps rather than a rate.
//
// # What this package does NOT do
//
// It does not replay the corpus. The replay is the product's own path -- the
// milestone adapter over the replay transport, driven through the pipeline's
// `PollOnce` and `ConsumeOnce` -- and the seeder already drives it.
//
// So the seam is deliberate: `Run` takes what was RAISED and compares it with
// what was EXPECTED, and the caller that replays supplies the first. A package
// that both replayed and compared would be a second path through the product,
// and the seeder's own comment says the point is that there is not one.
package replayaudit

import (
	"fmt"
	"sort"
	"time"
)

// Expected is one scenario's expectation.
type Expected struct {
	// Scenario is the scenario's name.
	Scenario string `json:"scenario"`
	// Incidents is how many incidents the scenario is expected to raise.
	//
	// Zero is meaningful: it is the negative case, and the scenarios that
	// expect nothing are the ones that prove the product does not raise an
	// incident where it should not.
	Incidents int `json:"incidents"`
	// Covers are the behaviours the scenario is there to prove.
	Covers []string `json:"covers,omitempty"`
}

// Raised is what a replay raised, per scenario.
type Raised struct {
	// Scenario is the scenario's name.
	Scenario string `json:"scenario"`
	// Incidents is how many incidents the replay raised for it.
	Incidents int `json:"incidents"`
	// Detail says what happened, for a reader who wants to know WHY.
	Detail string `json:"detail,omitempty"`
}

// Missed is a scenario that was expected to raise an incident and raised none.
type Missed struct {
	Scenario string `json:"scenario"`
	Expected int    `json:"expected"`
	Detail   string `json:"detail,omitempty"`
}

// Extra is a scenario that raised more incidents than it was expected to.
//
// It is reported and NOT folded into the rate: a scenario that raises two
// incidents where one was expected is a defect of a DIFFERENT KIND from one
// that raises none, and averaging them would hide both.
type Extra struct {
	Scenario string `json:"scenario"`
	Expected int    `json:"expected"`
	Raised   int    `json:"raised"`
	Detail   string `json:"detail,omitempty"`
}

// Report is the audit.
//
// The RATE is over the scenarios that EXPECT at least one incident, and the
// scenarios that expect none are excluded from the denominator: a scenario that
// expects nothing has nothing to miss.
type Report struct {
	// Expected is what each scenario was expected to raise.
	Expected []Expected `json:"expected"`
	// Raised is what the replay raised.
	Raised []Raised `json:"raised"`
	// Missed are the scenarios that expected an incident and raised none.
	Missed []Missed `json:"missed"`
	// Extra are the scenarios that raised more than they were expected to.
	Extra []Extra `json:"extra"`
	// Unlabelled are the scenarios with no expectation at all.
	//
	// They are reported and never folded into a denominator: "the product did
	// not raise an incident" and "nobody said whether it should have" are
	// different facts, and only the second one is missing information.
	Unlabelled []string `json:"unlabelled"`
	// N is the number of scenarios that expected at least one incident.
	N int `json:"n"`
	// Rate is the share of those that were missed, and it is nil when there
	// were none.
	Rate *Rate `json:"rate"`
	// Complete says whether every scenario declared an expectation.
	//
	// It is false when there are unlabelled scenarios, and a rate over a
	// corpus that is not complete is a rate over part of the corpus.
	Complete bool `json:"complete"`
	// Note carries what a reader needs to know and would otherwise have to
	// guess.
	Note string `json:"note"`
}

// Rate is a proportion, with the N behind it and its interval.
type Rate struct {
	// Value is the proportion, and it is nil when the N is zero.
	Value *float64 `json:"value"`
	// N is the denominator.
	N int `json:"n"`
	// Missed is the numerator.
	Missed int `json:"missed"`
	// CI is the Wilson interval on the rate, and it is nil when the N is zero.
	CI *Interval `json:"confidence_interval"`
}

// Interval is a confidence interval on a proportion.
type Interval struct {
	Method string  `json:"method"`
	Lower  float64 `json:"lower"`
	Upper  float64 `json:"upper"`
}

// Run compares what was raised with what was expected.
//
// The expectations come from the caller, which is the corpus: the audit's input
// is the corpus's own table, and a scenario with no expectation in it is a
// GAP and not a zero.
//
// The rate is over the scenarios that EXPECTED at least one incident. A
// scenario that expects nothing has nothing to miss, and including it in the
// denominator would make the rate better the more negative cases the corpus
// carries.
func Run(expected []Expected, raised []Raised) Report {
	byScenario := make(map[string]Raised, len(raised))
	for _, r := range raised {
		byScenario[r.Scenario] = r
	}

	report := Report{
		Expected:   expected,
		Raised:     raised,
		Missed:     []Missed{},
		Extra:      []Extra{},
		Unlabelled: []string{},
		Complete:   true,
		Note: "The expectations are the corpus's own table, authored from what each scenario " +
			"exists to prove. A scenario with no expectation is a GAP and is reported as one; the " +
			"rate is over the scenarios that expected at least one incident, because a scenario " +
			"that expects nothing has nothing to miss.",
	}

	missed := 0
	for _, e := range expected {
		got, ok := byScenario[e.Scenario]
		if !ok {
			// The scenario was not replayed at all, which is a gap in the
			// REPLAY and not a missed incident: nothing was raised because
			// nothing was run.
			report.Unlabelled = append(report.Unlabelled, e.Scenario)
			report.Complete = false
			continue
		}
		switch {
		case e.Incidents > 0 && got.Incidents == 0:
			report.Missed = append(report.Missed, Missed{
				Scenario: e.Scenario, Expected: e.Incidents, Detail: got.Detail,
			})
			missed++
		case got.Incidents > e.Incidents:
			report.Extra = append(report.Extra, Extra{
				Scenario: e.Scenario, Expected: e.Incidents, Raised: got.Incidents, Detail: got.Detail,
			})
		}
	}

	// The denominator: the scenarios that expected at least one incident.
	denominator := 0
	for _, e := range expected {
		if e.Incidents > 0 {
			denominator++
		}
	}
	report.N = denominator
	if denominator > 0 {
		v := float64(missed) / float64(denominator)
		report.Rate = &Rate{
			Value:  &v,
			N:      denominator,
			Missed: missed,
			CI:     wilson(int64(missed), int64(denominator)),
		}
	}

	sort.Strings(report.Unlabelled)
	return report
}

// wilson is the Wilson score interval on a proportion.
//
// It is the interval the report carries because the normal approximation is
// exactly wrong at the ends -- a proportion near zero or one has a skewed
// sampling distribution -- and the ends are where a miss rate lives.
func wilson(successes, n int64) *Interval {
	if n <= 0 {
		return nil
	}
	const z = 1.959963984540054
	p := float64(successes) / float64(n)
	denom := 1 + z*z/float64(n)
	centre := p + z*z/(2*float64(n))
	spread := z * sqrt(p*(1-p)/float64(n)+z*z/(4*float64(n)*float64(n)))
	lower := (centre - spread) / denom
	upper := (centre + spread) / denom
	if lower < 0 {
		lower = 0
	}
	if upper > 1 {
		upper = 1
	}
	return &Interval{Method: "wilson", Lower: lower, Upper: upper}
}

// sqrt is math.Sqrt, kept local so the package has one import.
func sqrt(v float64) float64 {
	if v <= 0 {
		return 0
	}
	// Newton's method, which is exact enough for an interval's endpoints and
	// keeps the package free of a math import for one call.
	x := v
	if x < 1 {
		x = 1
	}
	for i := 0; i < 40; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}

// String renders the report's shape in one line, for a log.
func (r Report) String() string {
	return fmt.Sprintf("%d scenario(s), %d expected an incident, %d missed, %d extra, %d unlabelled",
		len(r.Expected), r.N, len(r.Missed), len(r.Extra), len(r.Unlabelled))
}

// GeneratedReport is an audit with the instant it was built.
//
// The instant is carried so a report is never undated, and an undated report
// cannot be compared with anything.
type GeneratedReport struct {
	Report      Report    `json:"report"`
	GeneratedAt time.Time `json:"generated_at"`
}
