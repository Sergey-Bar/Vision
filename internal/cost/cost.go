// Package cost is what a model call costs, and the arithmetic that gets there.
//
// # Why this is a package and not a constant in a report
//
// Criterion 10 asks for two things that look like one: a number, and the ability to
// stop. The number is the easy half and the easy half is a lie on its own. $0.15 per
// event, times a number of events, is an answer that tells a buyer nothing about
// whether their site is the kind of site that produces a hundred events a day or ten
// thousand. So the model here is built to be READ, and [Model.Explain] returns the
// whole derivation as a list of lines rather than a total.
//
// # The three facts a cost number needs, and this type carries all three
//
//   - what one CALL costs, which is not what one EVENT costs. A rule that corroborates
//     against three signals can spend three calls deciding one event, and a model
//     that conflates the two understates a site by a factor of three.
//   - the event rate, which is the only input nobody knows, and which is therefore an
//     explicit argument at every call site rather than a constant buried in a
//     function. A default event rate turns a cost model into a sales claim.
//   - the ceiling, because a budget that is only reported is a budget that is
//     discovered, and the discovery is an invoice.
//
// # The daily ceiling is DERIVED from a monthly budget
//
// The last line of [Model.Explain] is the one that matters, and it is why there is no
// way to set the daily number separately. A monthly budget checked monthly lets a
// site spend a month's money in a day and find out afterwards. A daily ceiling
// derived from the monthly budget bounds the RATE, which is the thing an event-rate
// spike actually violates. Two numbers that can disagree is a control that will.
package cost

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Model is the price list. Every figure is in USD and every figure is per-unit.
type Model struct {
	// PerCall is what one request to the provider costs. This is what an invoice
	// charges and what a ceiling has to bound.
	PerCall float64
	// CallsPerEvent is how many provider calls one event costs, retries and
	// corroboration included.
	//
	// Not an average over a run: a run's mean hides the case that matters, which is
	// the event a corroborating rule spent three calls to decide. This is the figure
	// the soak's worst case should report and the figure a ceiling should be set
	// from.
	CallsPerEvent float64
}

// Validate refuses a model that cannot be printed.
//
// A zero price is not a discount, it is an unset float, and an unset float in a cost
// model produces a budget of zero and a ceiling nothing ever reaches.
func (m Model) Validate() error {
	switch {
	case m.PerCall <= 0:
		return fmt.Errorf("cost: a per-call price of %v is not a price. Zero or negative here produces a "+
			"budget of zero and a ceiling nothing reaches, which reads as 'costs nothing' rather "+
			"than as 'nobody set this'", m.PerCall)
	case m.CallsPerEvent <= 0:
		return fmt.Errorf("cost: %v calls per event is not a call count. Every other figure here is derived "+
			"from it, so zero makes the ceiling unreachable and the monthly total wrong", m.CallsPerEvent)
	}
	return nil
}

// callCost is one call's price.
func (m Model) callCost() float64 { return m.PerCall }

// EventCost is what one event costs: the calls it takes times the price of one.
//
// Computed, never stored. A stored `PerEvent` is a second answer to the same question
// beside the first, and the two disagree the day somebody changes either.
func (m Model) EventCost() float64 { return m.CallsPerEvent * m.PerCall }

// cents is the tolerance every money comparison uses.
//
// A float64 dollar amount is not exact — `$0.05 * 3` is `0.15000000000000002` — so a
// ceiling compared with `>` refuses the event that lands exactly on it. That is not a
// theoretical problem here: at $0.15 an event against a $6.666666666666667 daily
// ceiling, "exactly at the ceiling" is the case a site hits on a quiet day and the
// one an operator notices. Half a cent of tolerance is far below any real price and
// far above the arithmetic error.
//
// The alternative — rounding every figure to cents — makes `USD` disagree with the
// sum of its own `Calls * PerCall`, which is a worse disagreement to have because it
// is the one that shows up in the derivation.
const cents = 0.005

// withinCents is true when two amounts are the same to half a cent.
func withinCents(a, b float64) bool { return a-b < cents && b-a < cents }

// Day is a site's spend over one day at a stated event rate.
type Day struct {
	EventsPerDay float64
	Calls        float64
	USD          float64
}

// DayAt prices a day at an event rate. The rate is an argument and never a default.
func (m Model) DayAt(eventsPerDay float64) Day {
	calls := eventsPerDay * m.CallsPerEvent
	return Day{EventsPerDay: eventsPerDay, Calls: calls, USD: calls * m.PerCall}
}

// Month is a site's spend over a projection month, against a budget.
type Month struct {
	Days            int
	EventsPerDay    float64
	CallsPerDay     float64
	Calls           float64
	USD             float64
	BudgetUSD       float64
	DailyCeilingUSD float64
}

// monthDays is the projection length.
//
// 30 rather than the calendar length on purpose: a projection that varies with the
// month makes two projections of the same site disagree, and a reader comparing them
// concludes one of the sites changed.
const monthDays = 30

// MonthAt prices a month at an event rate against a budget.
func (m Model) MonthAt(eventsPerDay float64, budgetUSD float64) Month {
	d := m.DayAt(eventsPerDay)
	return Month{
		Days: monthDays, EventsPerDay: eventsPerDay,
		CallsPerDay: d.Calls, Calls: d.Calls * monthDays, USD: d.USD * monthDays,
		BudgetUSD: budgetUSD, DailyCeilingUSD: budgetUSD / monthDays,
	}
}

// Headroom is how many times over the budget this rate is, and 0 at or over it.
//
// Inverted rather than a negative multiple: "−3.4x" is a number a reader has to
// interpret, and "0x, over budget" is the same fact in a form nobody has to think
// about.
//
// The zero is the load-bearing part. It was `Budget / USD` unconditionally, so an
// over-budget site reported `0.11x` — a positive headroom on a site that is nine
// times over — and a reader scanning a table of sites would read it as the one with
// room left.
func (mo Month) Headroom() float64 {
	if mo.BudgetUSD <= 0 || mo.USD <= 0 || mo.OverBudget() {
		return 0
	}
	return mo.BudgetUSD / mo.USD
}

// OverBudget is whether this rate would exceed the budget inside a month.
//
// Within half a cent counts as AT budget rather than over it, for the reason on
// [cents]: `$0.05 * 3` is not exactly `$0.15` and a month of it is not exactly
// `$180`, so an exact comparison reports a site sitting precisely on its budget as
// over it.
func (mo Month) OverBudget() bool { return mo.BudgetUSD > 0 && mo.USD-mo.BudgetUSD > cents }

// camerasInTheCriterion is the camera count criterion 10's headline number is
// stated against. Named so a reader can see it is a constant of the CLAIM and not of
// the arithmetic.
const camerasInTheCriterion = 20

// Explain is the whole derivation as lines, and it is the deliverable.
//
// Not a total. A total is what the criterion asked for and a total is not what a
// buyer needs: two sites can produce the same total and mean completely different
// things, and the buyer has to know WHICH number to scale.
func (m Model) Explain(eventsPerDay, budgetUSD float64) string {
	mo := m.MonthAt(eventsPerDay, budgetUSD)
	day := m.DayAt(eventsPerDay)
	var b strings.Builder
	fmt.Fprintf(&b, "  %.0f events/day x %.1f calls/event = %.0f calls/day\n",
		eventsPerDay, m.CallsPerEvent, mo.CallsPerDay)
	fmt.Fprintf(&b, "  %.0f calls/day x $%.4f/call = $%.4f/day\n", mo.CallsPerDay, m.PerCall, day.USD)
	fmt.Fprintf(&b, "  $%.4f/day x %d days = $%.2f/month at this event rate\n", day.USD, monthDays, mo.USD)
	if mo.OverBudget() {
		fmt.Fprintf(&b, "  budget $%.2f/month: OVER by $%.2f (%.1fx the budget)\n",
			mo.BudgetUSD, mo.USD-mo.BudgetUSD, mo.USD/mo.BudgetUSD)
	} else {
		fmt.Fprintf(&b, "  budget $%.2f/month: %.1fx headroom\n", mo.BudgetUSD, mo.Headroom())
	}
	fmt.Fprintf(&b, "  per-site daily ceiling $%.4f = $%.2f / %d days, so a DAY that reached it is\n",
		mo.DailyCeilingUSD, mo.BudgetUSD, monthDays)
	fmt.Fprintf(&b, "  refused even though the month has not been spent\n")
	fmt.Fprintf(&b, "  $%.2f/month over %d cameras is $%.2f/camera/month\n",
		mo.USD, camerasInTheCriterion, mo.USD/camerasInTheCriterion)
	// The threshold, because "under $200/month" needs a rate to be true at.
	fmt.Fprintf(&b, "  under $%.2f/month holds up to %.0f events/day at %.1f calls/event\n",
		mo.BudgetUSD, mo.maxEventsPerDay(), m.CallsPerEvent)
	return b.String()
}

// maxEventsPerDay is the event rate at which the month exactly meets the budget.
func (mo Month) maxEventsPerDay() float64 {
	if mo.USD <= 0 || mo.EventsPerDay <= 0 {
		return 0
	}
	return mo.EventsPerDay * mo.BudgetUSD / mo.USD
}

// ErrOverBudget is the refusal, and a named error rather than a string so a caller can
// tell "the budget said no" from "the provider was down". Those two look identical
// from a log line and mean opposite things to whoever has to act on it.
var ErrOverBudget = errors.New("cost: this site's daily model budget is spent")

// Guard enforces a per-site, per-day ceiling.
//
// It is in this package because a ceiling that is only reported is a ceiling that is
// discovered, and the discovery is an invoice. [Guard.Spend] is called BEFORE the
// provider call, so the refusal is a decision this product makes rather than a
// surprise the provider sends.
//
// Spend is keyed by site AND by UTC calendar day. UTC rather than the site's zone on
// purpose: a ceiling that resets at local midnight is a ceiling two sites disagree
// about, and a budget that depends on whose clock you read is not a budget.
type Guard struct {
	model      Model
	ceilingUSD float64
	spent      map[dayKey]float64
	now        func() time.Time
}

type dayKey struct {
	site string
	day  string // 2006-01-02 in UTC
}

// NewGuard builds a guard from a monthly budget.
//
// The daily ceiling is DERIVED and there is no way to set it separately: two numbers
// that can disagree is a control that will.
func NewGuard(m Model, monthlyBudgetUSD float64) (*Guard, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if monthlyBudgetUSD <= 0 {
		return nil, errors.New("cost: a guard needs a positive monthly budget. A guard with no budget " +
			"refuses nothing, and a control that cannot refuse is a comment")
	}
	return &Guard{
		model:      m,
		ceilingUSD: monthlyBudgetUSD / monthDays,
		spent:      map[dayKey]float64{},
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// WithClock injects the clock, for the reason every other clock here is injectable:
// a budget that resets on a schedule nobody controls is a budget nobody can test.
func (g *Guard) WithClock(now func() time.Time) *Guard {
	g.now = now
	return g
}

// Model returns the price list this guard enforces.
func (g *Guard) Model() Model { return g.model }

// CeilingUSD is the daily ceiling.
func (g *Guard) CeilingUSD() float64 { return g.ceilingUSD }

func (g *Guard) key(site string) dayKey {
	return dayKey{site: site, day: g.now().UTC().Format("2006-01-02")}
}

// SpentUSD is what a site has spent today, in UTC.
func (g *Guard) SpentUSD(site string) float64 { return g.spent[g.key(site)] }

// RemainingUSD is what a site may still spend today, and never below zero.
func (g *Guard) RemainingUSD(site string) float64 {
	if r := g.ceilingUSD - g.SpentUSD(site); r > 0 {
		return r
	}
	return 0
}

// EventsAffordable is how many more events this site can ask about today at the
// current price.
//
// Derived from the ceiling rather than counted down, so it is right immediately after
// construction instead of needing an incremental counter kept in step with the price.
func (g *Guard) EventsAffordable(site string) int {
	per := g.model.EventCost()
	if per <= 0 {
		return 0
	}
	return int(g.RemainingUSD(site) / per)
}

// Spend records one batch of events and refuses if that would cross the ceiling.
//
// `events` is the whole batch, so a caller asking about twenty at once is priced for
// twenty and refused as twenty. One at a time would let a burst through one at a
// time, which is the shape of the incident this guards against.
func (g *Guard) Spend(site string, events int) error {
	if events < 0 {
		return fmt.Errorf("cost: a negative event count (%d) would CREDIT a site's budget. Refusing, "+
			"because a budget that can be credited is not a budget", events)
	}
	if err := g.check(site, events); err != nil {
		return err
	}
	if events > 0 {
		g.spent[g.key(site)] += g.model.EventCost() * float64(events)
	}
	return nil
}

// Check is Spend without the side effect, for a preflight that wants to know.
//
// An operator asking "would this site fit" and a worker spending the budget are the
// same question with one side, and having only the spending version means the answer
// to the question costs money.
func (g *Guard) Check(site string, events int) error { return g.check(site, events) }

func (g *Guard) check(site string, events int) error {
	if events <= 0 {
		return nil
	}
	k := g.key(site)
	want := g.model.EventCost() * float64(events)
	if g.spent[k]+want-g.ceilingUSD > cents {
		return fmt.Errorf("%w: site %s has $%.4f of a $%.4f daily ceiling (UTC %s) and this batch "+
			"of %d event(s) would spend $%.4f. The month's budget is not spent and the site is "+
			"still refused, because a rate that spikes inside a day is what the ceiling bounds",
			ErrOverBudget, site, g.spent[k], g.ceilingUSD, k.day, events, want)
	}
	return nil
}

// SpendReport is the guard's state for one site.
type SpendReport struct {
	Site       string
	Day        string
	SpentUSD   float64
	CeilingUSD float64
	Events     int
}

// Report is the guard's state, and it exists so the number a report prints and the
// number the guard enforces are the same number read twice.
func (g *Guard) Report(site string, events int) SpendReport {
	k := g.key(site)
	return SpendReport{
		Site: site, Day: k.day,
		SpentUSD: g.spent[k], CeilingUSD: g.ceilingUSD, Events: events,
	}
}
