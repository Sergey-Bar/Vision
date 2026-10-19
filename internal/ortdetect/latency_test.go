package ortdetect

import (
	"strings"
	"testing"
)

// The throughput gate. `internal/recorder`'s bounded queue BLOCKS rather than
// dropping frames, and the supervisor kills ffmpeg with a recorded fault — so a
// detector slower than the frame interval does not degrade, it produces
// `ai_pipe_backpressure` and fails AC1.
//
// That makes this arithmetic a GATE rather than a metric: it has to be checked
// before a run, from a measurement, and the refusal has to print the numbers.

// TestNoLatencyMeasuredIsNotZeroLatency is the absence rule, in the one place
// where it is easiest to get wrong.
//
// An empty window has no p99. Returning 0 would make `AchievableFPS` return
// +Inf and `HoldsFrameRate` return true — the gate would pass a detector that has
// never run, which is the most expensive possible direction for a gate to be
// wrong in.
func TestNoLatencyMeasuredIsNotZeroLatency(t *testing.T) {
	l := NewLatencies(16)
	if got, ok := l.P99(); ok {
		t.Fatalf("p99 over an empty window = %v and reported ok", got)
	}
	if got, ok := l.AchievableFPS(); ok {
		t.Fatalf("achievable fps over an empty window = %v and reported ok; an unmeasured detector "+
			"is not infinitely fast", got)
	}
	if _, ok := l.Max(); ok {
		t.Fatal("max over an empty window reported ok")
	}
	if l.Count() != 0 {
		t.Fatalf("count = %d, want 0", l.Count())
	}
	ok, arithmetic := l.HoldsFrameRate(5, 30)
	if ok {
		t.Fatal("the gate PASSED with nothing measured")
	}
	if !strings.Contains(arithmetic, "no inference latency") {
		t.Errorf("the refusal does not say what is missing: %q", arithmetic)
	}
	// A nil collector must be safe, because a run that never wired one up is
	// exactly the case this must not panic on.
	var nilLat *Latencies
	if _, ok := nilLat.P99(); ok {
		t.Fatal("a nil Latencies reported a percentile")
	}
	nilLat.Observe(10) // must not panic
	if nilLat.Count() != 0 {
		t.Fatal("a nil Latencies counted an observation")
	}
}

// TestThePercentilesAreNearestRankOverTheWindow pins the arithmetic.
//
// No interpolation: an interpolated percentile is a number no frame produced, and
// this module's rule is that a measurement is something that happened.
func TestThePercentilesAreNearestRankOverTheWindow(t *testing.T) {
	l := NewLatencies(100)
	// 1..100 ms.
	for i := 1; i <= 100; i++ {
		l.Observe(float64(i))
	}
	for _, tc := range []struct {
		p    float64
		want float64
	}{
		{0, 1}, {50, 50}, {95, 95}, {99, 99}, {100, 100},
	} {
		got, ok := l.Percentile(tc.p)
		if !ok {
			t.Fatalf("p%v reported no value", tc.p)
		}
		if got != tc.want {
			t.Errorf("p%v = %v, want %v", tc.p, got, tc.want)
		}
	}
	if got, _ := l.Max(); got != 100 {
		t.Errorf("max = %v, want 100", got)
	}
}

// TestTheWindowIsBoundedAndSlides is what makes two runs comparable.
//
// A growing list would report a p99 that depends on how long the run was, so two
// runs of the same clip at different lengths would not be comparable and a p99
// would describe a period of time nobody named. A bounded ring means the number
// describes a WINDOW, and the window is printed beside it.
func TestTheWindowIsBoundedAndSlides(t *testing.T) {
	l := NewLatencies(10)
	for i := 0; i < 1000; i++ {
		l.Observe(float64(i))
	}
	if l.Count() != 10 {
		t.Fatalf("count = %d, want the window of 10", l.Count())
	}
	if l.Window != 10 {
		t.Fatalf("window = %d, want 10", l.Window)
	}
	// The window holds the last ten observations: 990..999.
	p50, ok := l.P50()
	if !ok {
		t.Fatal("no p50")
	}
	if p50 < 990 || p50 > 999 {
		t.Fatalf("p50 = %v, want a value inside the last ten observations (990..999)", p50)
	}
	if got, _ := l.Min(); got != 990 {
		t.Errorf("min = %v, want 990 — the window did not slide", got)
	}
}

// TestAnEmptyWindowIsAlsoHandledByTheConstructor is the awkward input. A window of
// zero would make `len(recent) < l.Window` false immediately and the modulo would
// divide by zero, so the constructor substitutes the default.
func TestAnEmptyWindowIsAlsoHandledByTheConstructor(t *testing.T) {
	l := NewLatencies(0)
	if l.Window != DefaultLatencyWindow {
		t.Fatalf("window = %d, want the default %d", l.Window, DefaultLatencyWindow)
	}
	l.Observe(5)
	if got, ok := l.P50(); !ok || got != 5 {
		t.Fatalf("p50 = %v ok=%v, want 5", got, ok)
	}
	if NewLatencies(-5).Window != DefaultLatencyWindow {
		t.Fatal("a negative window was accepted")
	}
}

// TestTheGateRefusesAndPrintsTheArithmetic is the refusal, and the arithmetic is
// half of what it must contain.
//
// "cannot hold the frame rate" sends a reader to a profiler. "det_p99=61.2ms x
// ai_fps=30 = 1836ms of a 1000ms second" tells them which of two numbers to change.
func TestTheGateRefusesAndPrintsTheArithmetic(t *testing.T) {
	// 61ms p99, declared at 30 fps.
	l := NewLatencies(100)
	for i := 0; i < 100; i++ {
		l.Observe(61)
	}
	ok, arithmetic := l.HoldsFrameRate(30, 30)
	if ok {
		t.Fatalf("the gate PASSED a 61ms detector at 30fps: %s", arithmetic)
	}
	for _, want := range []string{"det_p99=61.0ms", "ai_fps=30", "1830", "1000ms"} {
		if !strings.Contains(arithmetic, want) {
			t.Errorf("the refusal does not contain %q: %s", want, arithmetic)
		}
	}

	// The same detector at 10 fps fits: 610ms of a second.
	ok, arithmetic = l.HoldsFrameRate(10, 30)
	if !ok {
		t.Fatalf("the gate REFUSED a 61ms detector at 10fps, which fits: %s", arithmetic)
	}
	if !strings.Contains(arithmetic, "610") {
		t.Errorf("the pass does not print the arithmetic: %s", arithmetic)
	}
}

// TestZeroFpsMeansEveryDecodedFrame is the new meaning of SP_POC_AI_FPS=0.
//
// It used to be refused as "not a rate", which made the most demanding mode the
// one mode the configuration could not express. Zero now means every decoded
// frame, and the DECODE rate stands in for it — because that is the demand being
// made — so the gate has to say so in the refusal rather than multiplying by zero
// and passing.
func TestZeroFpsMeansEveryDecodedFrame(t *testing.T) {
	l := NewLatencies(100)
	for i := 0; i < 100; i++ {
		l.Observe(40)
	}
	// 40ms at a decode rate of 20fps: 800ms of a second. Fits.
	ok, arithmetic := l.HoldsFrameRate(0, 20)
	if !ok {
		t.Fatalf("the gate REFUSED 40ms at 20fps: %s", arithmetic)
	}
	if !strings.Contains(arithmetic, "every decoded frame") {
		t.Errorf("the refusal does not explain what zero meant: %s", arithmetic)
	}
	if !strings.Contains(arithmetic, "decode_fps=20.00") {
		t.Errorf("the refusal does not name the rate it used: %s", arithmetic)
	}

	// 40ms at 30fps decode: 1200ms. Refused — and NOT because zero multiplied out.
	l2 := NewLatencies(100)
	for i := 0; i < 100; i++ {
		l2.Observe(40)
	}
	ok, arithmetic = l2.HoldsFrameRate(0, 30)
	if ok {
		t.Fatalf("the gate PASSED a 40ms detector against a 30fps decode rate: %s", arithmetic)
	}
	if !strings.Contains(arithmetic, "1200") {
		t.Errorf("the refusal does not print the arithmetic it actually did: %s", arithmetic)
	}
}

// TestTheAchievableRateIsTheSameArithmetic is the "one declaration" rule. The gate
// and the headline number a banner prints are the same division, so they cannot
// disagree — which is the same discipline the red-check table needs.
func TestTheAchievableRateIsTheSameArithmetic(t *testing.T) {
	l := NewLatencies(100)
	for i := 0; i < 100; i++ {
		l.Observe(50)
	}
	fps, ok := l.AchievableFPS()
	if !ok {
		t.Fatal("no achievable rate")
	}
	if fps != 20 {
		t.Fatalf("achievable fps = %v, want 1000/50 = 20", fps)
	}
	// A 20fps detector at a declared 20fps is exactly at the limit: 50*20 = 1000,
	// which is not below 1000, so it is refused. The boundary is exclusive on
	// purpose — a detector that exactly meets the interval leaves no headroom for
	// the frame that runs long, and this machine has four cores and a demo.
	ok, _ = l.HoldsFrameRate(20, 20)
	if ok {
		t.Fatal("a detector exactly at the limit passed; the comparison must be strict")
	}
	if ok, _ = l.HoldsFrameRate(19, 20); !ok {
		t.Fatal("a detector at 19fps against a 20fps demand was refused")
	}
}

// TestAP99OfZeroIsNotAnInfiniteRate covers the degenerate measurement.
//
// A frame that "took" 0ms is a clock that did not run, not a free inference.
func TestAP99OfZeroIsNotAnInfiniteRate(t *testing.T) {
	l := NewLatencies(4)
	l.Observe(0)
	l.Observe(0)
	if _, ok := l.AchievableFPS(); ok {
		t.Fatal("a p99 of zero reported an achievable rate")
	}
	ok, arithmetic := l.HoldsFrameRate(30, 30)
	if ok {
		t.Fatal("a detector measuring 0ms passed the gate")
	}
	if arithmetic == "" {
		t.Fatal("the refusal printed no arithmetic")
	}
}

// TestTheGateIsTheSameArithmeticWhateverTheLatencyShape checks that a single slow
// tail is enough to refuse, which is the whole reason p99 is used rather than a
// mean.
//
// A mean over a distribution with a long tail fits an interval the tail overruns
// regularly, and the overrun shows up as backpressure on exactly the frames that
// matter — the crowded ones.
func TestTheGateIsTheSameArithmeticWhateverTheLatencyShape(t *testing.T) {
	// 95 fast frames and 5 at 400ms. The mean is ~115ms; the p99 is 400.
	l := NewLatencies(100)
	for i := 0; i < 95; i++ {
		l.Observe(20)
	}
	for i := 0; i < 5; i++ {
		l.Observe(400)
	}
	p50, _ := l.P50()
	p99, _ := l.P99()
	if p50 >= 100 {
		t.Fatalf("p50 = %v; the median should be fast", p50)
	}
	if p99 < 400 {
		t.Fatalf("p99 = %v, want 400 — the tail is what p99 is for", p99)
	}
	// At 2fps both fit (400 x 2 = 800ms of a second); at 30fps the p99 refuses
	// while the median would have passed (20 x 30 = 600ms).
	if ok, arithmetic := l.HoldsFrameRate(2, 30); !ok {
		t.Errorf("a 400ms p99 at 2fps should fit: %s", arithmetic)
	}
	if ok, _ := l.HoldsFrameRate(30, 30); ok {
		t.Error("a 400ms p99 at 30fps must be refused even though the median is 20ms")
	}
	// And the gate agrees with AchievableFPS on the same window, which is the
	// "one declaration" property the banner depends on.
	fps, ok := l.AchievableFPS()
	if !ok {
		t.Fatal("no achievable rate")
	}
	if int(fps) != 2 {
		t.Fatalf("achievable fps = %v, want 1000/400 = 2.5", fps)
	}
	if ok, _ := l.HoldsFrameRate(3, 30); ok {
		t.Error("3fps against a 400ms p99 is 1200ms of a second and must be refused")
	}
}
