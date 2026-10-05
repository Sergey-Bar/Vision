package cost

// The cost model and the ceiling, tested against the arithmetic rather than against
// a total.
//
// # Why the numbers in these tests are spelled out longhand
//
// Every assertion below computes the expected value from the inputs in the test, so
// a change to the model fails here with the line that disagrees rather than with a
// monthly total that is off by a factor nobody can see. The one test that pins
// literal dollars is [TestTheCriterionTenArithmeticIsTheOneTheDocumentStates], and it
// exists because a model with no external reference is a model that can only be
// checked against itself.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// thePriceList is the figure criterion 10 states: $0.15 per event, a vision model,
// twenty cameras, under $200 a month.
//
// The per-CALL price is derived from it rather than invented, because the criterion
// names an event price and the pipeline spends calls. At three calls per event, one
// event costs $0.15 and one call costs $0.05.
func thePriceList() Model { return Model{PerCall: 0.05, CallsPerEvent: 3} }

// A model that cannot be printed is refused. A zero price is an unset float, and an
// unset float produces a budget of zero and a ceiling nothing reaches.
func TestAModelWithNoPriceIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		model Model
		says  string
	}{
		{"no price", Model{PerCall: 0, CallsPerEvent: 3}, "not a price"},
		{"negative price", Model{PerCall: -1, CallsPerEvent: 3}, "not a price"},
		{"no calls per event", Model{PerCall: 0.05, CallsPerEvent: 0}, "not a call count"},
	}
	for _, c := range cases {
		err := c.model.Validate()
		if err == nil {
			t.Errorf("%s: a model with %v validated. An unset float in a cost model reads as "+
				"'costs nothing' rather than as 'nobody set this'", c.name, c.model)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: the refusal does not say what is wrong: %v", c.name, err)
		}
	}
}

// The internal ledger of a provider call, and the daily ceiling it is bounded by.
func TestAnEventCostsItsCallsAndNotItsOwn(t *testing.T) {
	m := thePriceList()
	if err := m.Validate(); err != nil {
		t.Fatalf("the price list does not validate: %v", err)
	}
	if got, want := m.callCost(), 0.05; got != want {
		t.Errorf("a call is $%.4f, want $%.2f", got, want)
	}
	if got, want := m.EventCost(), 0.15; !withinCents(got, want) {
		t.Errorf("an event is $%.4f, want $%.2f. The two are different numbers and conflating "+
			"them understates a corroborating site by a factor of three", got, want)
	}
}

// The derivation, line by line, because the derivation IS the deliverable.
//
// Criterion 10 says "show the arithmetic, not just the answer", so a total is not
// enough and this test refuses a total.
func TestTheArithmeticIsShownAndNotJustTheTotal(t *testing.T) {
	got := thePriceList().Explain(40, 200)
	for _, want := range []string{
		"40 events/day",
		"3.0 calls/event",
		"120 calls/day",
		"calls/day x $0.0500/call",
		"x 30 days",
		"budget $200.00/month",
		"daily ceiling",
		"20 cameras",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the derivation does not contain %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") < 5 {
		t.Errorf("the derivation is %d line(s). A single line a reader has to re-wrap is a line "+
			"they skip:\n%s", strings.Count(got, "\n"), got)
	}
}

// The literal dollars, checked against the criterion's own numbers rather than
// against this package's arithmetic.
func TestTheCriterionTenArithmeticIsTheOneTheDocumentStates(t *testing.T) {
	const budget = 200.0
	m := thePriceList()
	mo := m.MonthAt(40, budget)

	// 40 events/day x 3 calls x $0.05 = $6/day; x 30 = $180/month, under $200.
	if want := 180.0; !withinCents(mo.USD, want) {
		t.Errorf("40 events/day costs $%.4f/month, want $%.2f. $0.15/event is the criterion's "+
			"number and this is what it works out to at three calls per event", mo.USD, want)
	}
	if mo.OverBudget() {
		t.Errorf("40 events/day is over a $%.0f budget, which is not what the arithmetic says", budget)
	}
	if want := budget / mo.USD; !withinCents(mo.Headroom(), want) {
		t.Errorf("headroom is %.3fx, want %.3fx", mo.Headroom(), want)
	}

	// And the rate at which it stops holding, which is the number a buyer scales.
	// $200/month over 30 days at $4.50 per event-day is 44.44 events/day. The first
	// draft of this test asserted 33, which was the DAILY dollar figure divided by
	// the event price rather than the monthly one divided by it -- the same
	// arithmetic at the wrong scale, which is why the threshold is printed in the
	// derivation and asserted here rather than trusted to a reader.
	if got := m.MonthAt(44, budget).maxEventsPerDay(); !withinCents(got, 44.44) {
		t.Errorf("the derivation's threshold is %.2f events/day, want 44.44", got)
	}
	if m.MonthAt(44, budget).OverBudget() {
		t.Error("44 events/day is over a $200 budget; the derivation prints it as the last " +
			"rate that fits")
	}
	if !m.MonthAt(45, budget).OverBudget() {
		t.Error("45 events/day is under a $200 budget, so the printed threshold is above the real one")
	}
}

// Over budget is reported as over budget, and headroom as zero, because a negative
// multiple is a number a reader has to interpret.
func TestAnOverBudgetSiteIsNotGivenANegativeHeadroom(t *testing.T) {
	mo := thePriceList().MonthAt(400, 200)
	if !mo.OverBudget() {
		t.Fatal("400 events/day is not reported as over a $200 budget")
	}
	if mo.Headroom() != 0 {
		t.Errorf("headroom is %.2fx for an over-budget site. A negative multiple is a number a "+
			"reader has to interpret; zero with the word OVER is the same fact in a form "+
			"nobody has to think about", mo.Headroom())
	}
}

// The ceiling is DERIVED from the monthly budget, so it cannot disagree with it.
func TestTheDailyCeilingIsDerivedFromTheMonthlyBudget(t *testing.T) {
	for _, budget := range []float64{30, 200, 1000} {
		g, err := NewGuard(thePriceList(), budget)
		if err != nil {
			t.Fatalf("a guard for $%.0f: %v", budget, err)
		}
		if want := budget / monthDays; g.CeilingUSD() != want {
			t.Errorf("a $%.0f/month budget gives a $%.4f daily ceiling, want $%.4f. Two numbers "+
				"that can be set independently is a control that will eventually disagree with itself",
				budget, g.CeilingUSD(), want)
		}
	}
}

// A guard with no budget refuses nothing, which is a comment and not a control.
func TestAGuardWithNoBudgetIsRefused(t *testing.T) {
	for _, budget := range []float64{0, -1} {
		g, err := NewGuard(thePriceList(), budget)
		if err == nil {
			t.Errorf("a guard was built with a $%.0f monthly budget. It refuses nothing, and a "+
				"control that cannot refuse is a comment", budget)
			continue
		}
		if g != nil {
			t.Errorf("a guard was returned alongside the error for a $%.0f budget", budget)
		}
	}
	if !strings.Contains(mustFailText(t, thePriceList(), 0), "positive monthly budget") {
		t.Error("the refusal does not name the missing budget")
	}
}

func mustFailText(t *testing.T, m Model, budget float64) string {
	t.Helper()
	_, err := NewGuard(m, budget)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	return err.Error()
}

// The refusal itself, at the boundary.
//
// 40 events/day against a $200/month budget gives a $6.667 daily ceiling. At
// $0.15 an event that is 44 events. So 44 fits and 45 does not, and the error has to
// say which site, how much of the ceiling is gone, and that the MONTH is not spent —
// because an operator reading "budget exceeded" on day one of a month concludes they
// have overspent a month.
func TestTheCeilingRefusesTheCallThatWouldCrossIt(t *testing.T) {
	g := newFixedGuard(t, thePriceList(), 200)
	site := "north-gate"

	affordable := g.EventsAffordable(site)
	if affordable != 44 {
		t.Fatalf("a $200/month budget leaves room for %d events today, want 44. "+
			"$200/30 = $%.4f a day, and $%.2f an event divides that %.1f times",
			affordable, g.CeilingUSD(), thePriceList().EventCost(),
			g.CeilingUSD()/thePriceList().EventCost())
	}

	if err := g.Spend(site, affordable); err != nil {
		t.Fatalf("spending the affordable amount was refused: %v", err)
	}
	if got := g.SpentUSD(site); got <= 0 {
		t.Fatalf("nothing was recorded: spent $%.4f", got)
	}

	err := g.Spend(site, 1)
	if err == nil {
		t.Fatal("one more event was allowed after the ceiling was reached. A ceiling that " +
			"admits the call which crosses it is not a ceiling")
	}
	if !errors.Is(err, ErrOverBudget) {
		t.Errorf("the refusal is not ErrOverBudget, so a caller cannot tell the budget saying "+
			"no from the provider being down: %v", err)
	}
	for _, want := range []string{"north-gate", "daily ceiling", "UTC"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q, and a refusal an operator cannot act on is "+
				"a refusal they will not act on: %v", want, err)
		}
	}
}

// The batch, not the event.
//
// A caller asking about twenty events is priced for twenty and refused as twenty.
// One at a time would let a burst through, which is the shape of the incident this
// guards against.
func TestABatchIsPricedAsABatch(t *testing.T) {
	g := newFixedGuard(t, thePriceList(), 200)
	site := "north-gate"

	if err := g.Spend(site, 50); err == nil {
		t.Fatal("a 50-event batch was allowed against a $6.67 daily ceiling at $0.15 an event")
	}
	if got := g.SpentUSD(site); got != 0 {
		t.Errorf("a refused batch still spent $%.4f. A refusal that charges for the work it "+
			"declined is a refusal that a retry then cannot pass", got)
	}
	// And the two that fit, so the refusal is a boundary and not a blanket.
	if err := g.Spend(site, 44); err != nil {
		t.Errorf("44 events were refused: %v", err)
	}
}

// A negative event count would CREDIT a budget, and a budget that can be credited is
// not a budget.
func TestANegativeBatchIsRefusedRatherThanCredited(t *testing.T) {
	g := newFixedGuard(t, thePriceList(), 200)
	if err := g.Spend("north-gate", -10); err == nil {
		t.Fatal("a negative batch was accepted")
	}
	if got := g.SpentUSD("north-gate"); got != 0 {
		t.Errorf("a negative batch changed the spend to $%.4f", got)
	}
}

// Sites do not share a budget, because a single counter across every site is one
// site's events spending another's allowance.
func TestSitesDoNotShareABudget(t *testing.T) {
	g := newFixedGuard(t, thePriceList(), 200)
	if err := g.Spend("north-gate", 44); err != nil {
		t.Fatalf("north-gate: %v", err)
	}
	if err := g.Check("south-dock", 1); err != nil {
		t.Errorf("south-dock was refused after north-gate spent its whole day: %v", err)
	}
	if got := g.SpentUSD("south-dock"); got != 0 {
		t.Errorf("south-dock shows $%.4f spent, which north-gate's spending cannot do", got)
	}
}

// The calendar key means yesterday's spend is invisible today, and a UTC day means
// two sites in two zones do not disagree about when the budget resets.
func TestTheBudgetResetsOnAUtcDayBoundary(t *testing.T) {
	day := time.Date(2026, 10, 4, 23, 59, 0, 0, time.UTC)
	g := newFixedGuard(t, thePriceList(), 200)
	g.WithClock(func() time.Time { return day })

	if err := g.Spend("north-gate", 44); err != nil {
		t.Fatalf("spend on the last minute of the day: %v", err)
	}
	if err := g.Check("north-gate", 1); err == nil {
		t.Fatal("one more event was allowed a minute before midnight UTC")
	}

	// One minute later the budget is whole again.
	g.WithClock(func() time.Time { return day.Add(2 * time.Minute) })
	if err := g.Check("north-gate", 1); err != nil {
		t.Errorf("the ceiling did not reset after midnight UTC: %v", err)
	}
}

// Check must not charge. An operator asking whether a site would fit, and a worker
// spending the budget, are the same question with one side.
func TestCheckDoesNotSpend(t *testing.T) {
	g := newFixedGuard(t, thePriceList(), 200)
	for i := 0; i < 100; i++ {
		if err := g.Check("north-gate", 10); err != nil {
			t.Fatalf("Check refused on iteration %d: %v", i, err)
		}
	}
	if got := g.SpentUSD("north-gate"); got != 0 {
		t.Errorf("100 calls to Check spent $%.4f. The answer to 'would this site fit' must not "+
			"cost money", got)
	}
	if err := g.Spend("north-gate", 44); err != nil {
		t.Errorf("after 100 Checks the site cannot afford what it could afford before: %v", err)
	}
}

// The report and the guard read the same numbers.
func TestTheReportIsTheGuardsOwnNumbers(t *testing.T) {
	g := newFixedGuard(t, thePriceList(), 200)
	if err := g.Spend("north-gate", 10); err != nil {
		t.Fatalf("spend: %v", err)
	}
	r := g.Report("north-gate", 10)
	if r.SpentUSD != g.SpentUSD("north-gate") {
		t.Errorf("the report says $%.4f and the guard holds $%.4f. Two numbers for one fact, and "+
			"the one an operator reads is the one that will be quoted", r.SpentUSD, g.SpentUSD("north-gate"))
	}
	if r.CeilingUSD != g.CeilingUSD() {
		t.Errorf("the report's ceiling is $%.4f and the guard enforces $%.4f", r.CeilingUSD, g.CeilingUSD())
	}
	if r.Day != "2026-10-04" {
		t.Errorf("the report's day is %q. The day has to be the UTC day or the figure cannot be "+
			"looked up later", r.Day)
	}
}

// newFixedGuard is a guard with a frozen clock, so every assertion above is about
// the arithmetic and none of them is about when the test ran.
func newFixedGuard(t *testing.T, m Model, budget float64) *Guard {
	t.Helper()
	g, err := NewGuard(m, budget)
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	return g.WithClock(func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) })
}
