package clock_test

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/clock"
)

var base = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func TestPolicyValidation(t *testing.T) {
	valid := []clock.Policy{
		{Reference: clock.SourceNTP, WarnMs: 2000, SevereMs: 10000, SkewToleranceMs: 5000},
		{Reference: clock.SourceVMS, WarnMs: 0, SevereMs: 1, SkewToleranceMs: 1},
	}
	for i, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("case %d: a valid policy was refused: %v", i, err)
		}
	}

	invalid := map[string]clock.Policy{
		// The reference is a closed set of exact values. A site configuration
		// that says "NTP" or "ntp-server" is a configuration error worth naming
		// rather than a case to guess at: a reference clock that silently became
		// "host" would make every drift measurement measure the wrong thing.
		"unknown reference":  {Reference: "ntp-server", WarnMs: 1, SevereMs: 2, SkewToleranceMs: 1},
		"empty reference":    {WarnMs: 1, SevereMs: 2, SkewToleranceMs: 1},
		"wrong case":         {Reference: "NTP", WarnMs: 1, SevereMs: 2, SkewToleranceMs: 1},
		"severe below warn":  {Reference: clock.SourceNTP, WarnMs: 10000, SevereMs: 2000, SkewToleranceMs: 1},
		"severe equals warn": {Reference: clock.SourceNTP, WarnMs: 2000, SevereMs: 2000, SkewToleranceMs: 1},
		"negative warn":      {Reference: clock.SourceNTP, WarnMs: -1, SevereMs: 2000, SkewToleranceMs: 1},
		"negative skew":      {Reference: clock.SourceNTP, WarnMs: 1, SevereMs: 2000, SkewToleranceMs: -1},
	}
	for name, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A severe band that never triggers makes the drift measurement decorative, and
// a severe band that triggers on a well-tuned device excludes half the site from
// corroboration for no reason.
func TestBandBoundaries(t *testing.T) {
	p := clock.Policy{Reference: clock.SourceNTP, WarnMs: 2000, SevereMs: 10000, SkewToleranceMs: 5000}
	cases := map[int32]clock.Band{
		0:      clock.BandOK,
		1999:   clock.BandOK,
		2000:   clock.BandWarn,
		9999:   clock.BandWarn,
		10000:  clock.BandSevere,
		-2000:  clock.BandWarn,
		-9999:  clock.BandWarn,
		-10000: clock.BandSevere,
		40000:  clock.BandSevere,
	}
	for drift, want := range cases {
		if got := p.Band(drift); got != want {
			t.Errorf("drift %dms is band %q, want %q", drift, got, want)
		}
	}
}

// Drift is symmetric: a device 40 seconds slow is exactly as unusable for a
// five-second window as one 40 seconds fast.
func TestBandIsSymmetric(t *testing.T) {
	p := clock.Policy{Reference: clock.SourceNTP, WarnMs: 2000, SevereMs: 10000, SkewToleranceMs: 5000}
	if p.Band(40000) != p.Band(-40000) {
		t.Error("the same drift in opposite directions produced different bands")
	}
}

// The correction is additive and it subtracts: a device that runs ahead has its
// timestamps pulled back.
func TestNormalizedAppliesDriftInTheRightDirection(t *testing.T) {
	var got time.Time
	// A camera 40 seconds FAST claims 09:00:40 when the true time is 09:00:00.
	got, fallback := clock.Normalized(base.Add(40*time.Second), base, &clock.DeviceState{DriftMs: 40000})
	if fallback {
		t.Error("a device with an observed time was reported as a fallback")
	}
	if !got.Equal(base) {
		t.Errorf("normalized is %s, want %s", got, base)
	}

	// And a device 40 seconds SLOW is pushed forward.
	got, _ = clock.Normalized(base.Add(-40*time.Second), base, &clock.DeviceState{DriftMs: -40000})
	if !got.Equal(base) {
		t.Errorf("normalized is %s, want %s", got, base)
	}
}

// A missing source timestamp falls back to the receipt, and says so. Silently
// substituting a plausible time is how "we guessed" and "the source said so" end
// up looking identical in a stored record.
func TestNormalizedFallsBackVisibly(t *testing.T) {
	got, fallback := clock.Normalized(time.Time{}, base, nil)
	if !fallback {
		t.Error("a missing observed time was not reported as a fallback")
	}
	if !got.Equal(base) {
		t.Errorf("normalized is %s, want the receipt time %s", got, base)
	}
}

// No measurement means no correction, and the two states must stay distinct: a
// device that has never been checked is not a device measured at zero drift.
func TestNoMeasurementMeansNoCorrection(t *testing.T) {
	observed := base.Add(3 * time.Second)
	got, fallback := clock.Normalized(observed, base, nil)
	if fallback {
		t.Error("an observed time with no device state was reported as a fallback")
	}
	if !got.Equal(observed) {
		t.Errorf("normalized is %s, want the uncorrected %s", got, observed)
	}
}

func TestNormalizedWithNeitherTime(t *testing.T) {
	got, fallback := clock.Normalized(time.Time{}, time.Time{}, nil)
	if !fallback {
		t.Error("no times at all was not reported as a fallback")
	}
	if !got.IsZero() {
		t.Errorf("normalized is %s, want the zero time", got)
	}
}

// The severe band excludes a device from corroboration, and the reason says so -
// because a corroborating signal that vanished without explanation is what makes
// an operator stop trusting the product.
func TestCorrelationExclusionCarriesItsReason(t *testing.T) {
	p := clock.Policy{Reference: clock.SourceNTP, WarnMs: 2000, SevereMs: 10000, SkewToleranceMs: 5000}

	ok, exclusion := p.CorrelationAllowed(&clock.DeviceState{
		DeviceID: "cam-1", DriftMs: 5000, Band: clock.BandWarn,
	})
	if !ok || exclusion != nil {
		t.Error("a device in the warn band was excluded from corroboration")
	}

	ok, exclusion = p.CorrelationAllowed(&clock.DeviceState{
		DeviceID: "cam-2", DriftMs: 40000, Band: clock.BandSevere,
	})
	if ok {
		t.Error("a device in the severe band was allowed to corroborate")
	}
	if exclusion == nil {
		t.Fatal("the exclusion carried no reason")
	}
	if exclusion.DeviceID != "cam-2" {
		t.Errorf("the exclusion names %q, want cam-2", exclusion.DeviceID)
	}
	if exclusion.Reason == "" {
		t.Error("the exclusion has no reason text")
	}

	// A device with no band recorded is classified from its drift rather than
	// being trusted by default.
	ok, _ = p.CorrelationAllowed(&clock.DeviceState{DeviceID: "cam-3", DriftMs: 40000})
	if ok {
		t.Error("a device with an unclassified band of 40s was allowed to corroborate")
	}

	// A nil state is a device with no measurement, which is not a severe band.
	ok, exclusion = p.CorrelationAllowed(nil)
	if !ok || exclusion != nil {
		t.Error("a device with no measurement was excluded; unmeasured is not untrustworthy")
	}
}

func TestSkewExceeds(t *testing.T) {
	p := clock.Policy{Reference: clock.SourceNTP, WarnMs: 2000, SevereMs: 10000, SkewToleranceMs: 5000}
	if p.SkewExceeds(base, base.Add(4*time.Second)) {
		t.Error("a 4s gap exceeded a 5s tolerance")
	}
	if !p.SkewExceeds(base, base.Add(6*time.Second)) {
		t.Error("a 6s gap did not exceed a 5s tolerance")
	}
	// Symmetric, because arrival order is not a thing the comparison knows about.
	if !p.SkewExceeds(base, base.Add(-6*time.Second)) {
		t.Error("the skew comparison is asymmetric")
	}
	if p.SkewExceeds(base, base) {
		t.Error("a zero gap exceeded the tolerance")
	}
}

func TestWindowContains(t *testing.T) {
	w := clock.Window{From: base, To: base.Add(10 * time.Second), Tolerance: 2 * time.Second}

	for _, at := range []time.Time{base, base.Add(5 * time.Second), base.Add(10 * time.Second)} {
		if !w.Contains(at) {
			t.Errorf("window does not contain %s", at)
		}
	}
	// The tolerance extends both edges, so "simultaneous" is a band and not an
	// instant.
	for _, at := range []time.Time{base.Add(-2 * time.Second), base.Add(12 * time.Second)} {
		if !w.Contains(at) {
			t.Errorf("window does not contain %s despite the tolerance", at)
		}
	}
	for _, at := range []time.Time{base.Add(-3 * time.Second), base.Add(13 * time.Second), {}} {
		if w.Contains(at) {
			t.Errorf("window wrongly contains %v", at)
		}
	}
}

// A source claiming a time in the future relative to our clock is itself the
// finding. Reporting zero hides it; reporting a negative lag would be a metric
// nobody can chart.
func TestLagRejectsAClaimFromTheFuture(t *testing.T) {
	lag, ok := clock.Lag(base, base.Add(5*time.Second))
	if !ok {
		t.Fatal("a normal lag was not reported")
	}
	if lag != 5*time.Second {
		t.Errorf("lag is %s, want 5s", lag)
	}
	if _, ok := clock.Lag(base.Add(time.Minute), base); ok {
		t.Error("a source claiming a future time produced a usable lag")
	}
	if _, ok := clock.Lag(time.Time{}, base); ok {
		t.Error("a missing observed time produced a usable lag")
	}
}
