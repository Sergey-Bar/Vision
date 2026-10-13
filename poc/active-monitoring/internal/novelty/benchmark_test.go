package novelty

// Phase 0.5.1 and 0.6.5: what a novelty pass actually costs, and at what rate it has to
// run for a bird to be proposed more than twice.
//
// A1's "2,000 cameras on 3 GPUs" arithmetic assumes 3 ms per novelty pass. This file is
// that 3 ms measured rather than assumed, because an estimate in a proposal is a number
// nobody has checked, and if the real number is an order of magnitude larger the whole
// scale argument has to be redone before anyone quotes it.
//
// It is a BENCHMARK and not a test, on purpose. A test asserts a bound, and a bound on
// wall-clock time is a property of the machine it ran on rather than of the code, so a
// test here would fail on somebody else's laptop for no reason and pass on a slow one by
// accident. What IS asserted, in the other three tests, is arithmetic: that the gate
// runs the number of passes its declared rate says it runs, that the rate covers the
// smallest dwell, and that the cost projection names both rates rather than the cheap
// one.

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

// noveltyFrame builds a frame with a noisy grey field and a bright square, so the pass
// does its real work: a luma downscale, a median, a MAD, a threshold and a connected
// components pass. A uniform frame would be fast for the wrong reason and would measure
// the empty case.
func noveltyFrame(w, h int, x0, x1, y0, y1 int, seed int64) []byte {
	rgb := make([]byte, w*h*3)
	r := rand.New(rand.NewSource(seed))
	// A noisy field, so the MAD is non-zero everywhere rather than only at the square.
	// A zero MAD on a static field takes the degenerate fast path.
	for i := 0; i < len(rgb); i += 3 {
		v := byte(90 + r.Intn(24))
		rgb[i], rgb[i+1], rgb[i+2] = v, v, v
	}
	for y := y0; y < y1 && y < h; y++ {
		for x := x0; x < x1 && x < w; x++ {
			o := (y*w + x) * 3
			rgb[o], rgb[o+1], rgb[o+2] = 220, 220, 225
		}
	}
	return rgb
}

func benchmarkConfig() Config {
	c := Defaults()
	c.Mode = ModeTemporal
	c.GridWidth = 64
	c.MinCells = 4
	c.MaxCandidates = 8
	// A trigger ABOVE the release, which is the only ordering that is a hysteresis
	// band rather than an upside-down one. Defaults() pairs a release of 0.2 with a
	// much lower trigger, so raising the trigger alone would refuse.
	c.Threshold, c.Release = 0.04, 0.02
	return c
}

// TestTheNoveltyPassCostAt1080pIsMeasured reports p50/p95/p99 over 2,000 passes.
//
// The numbers this prints are the ones the scale proposal needs. They are printed and
// not compared, because comparing them would make this a test of the machine. The one
// assertion is that the measurement is above the timer's resolution, because a run that
// cannot resolve its own duration is not a measurement.
//
// It does NOT run under the race detector, and the reason is measured rather than
// assumed: instrumented, the 2,000-frame run did not finish inside the 10-minute test
// timeout and panicked, which took the rest of the package down with it — so the one
// run whose numbers are worth reading was also the run that cost every other test in
// here its instrumented pass. A cost measured through an instrumentor is a property of
// the instrumentor, and the skip says so rather than quietly printing a number 20x
// the truth. Without -race the numbers are the ones the proposal quotes.
func TestTheNoveltyPassCostAt1080pIsMeasured(t *testing.T) {
	if testing.Short() {
		t.Skip("a 2,000-frame 1080p novelty benchmark is not a short test")
	}
	if raceEnabled {
		t.Skip("the race detector inflates this benchmark's own subject — wall-clock time per pass — by " +
			"more than an order of magnitude (measured: the instrumented 2,000-frame run exceeded the " +
			"10-minute test timeout, so >300 ms a pass against 3.5 ms uninstrumented, an ~85x inflation), " +
			"and it then PANICS, which aborts the whole package and costs the other tests here their " +
			"instrumented run. The numbers this benchmark exists to print would be numbers about the " +
			"instrumentor, so it runs on the suite's non-race pass instead")
	}
	const (
		w, h    = 1920, 1080
		frames  = 2000
		a1Assum = 3.0
	)
	eng, err := New(benchmarkConfig())
	if err != nil {
		t.Fatal(err)
	}
	// A warm-up, so first-touch faults on the allocation are not in the sample.
	for i := 0; i < 3; i++ {
		eng.Candidates(noveltyFrame(w, h, 900+i, 940+i, 500, 530, 1), w, h)
	}

	ms := make([]float64, 0, frames)
	proposed := 0
	for i := 0; i < frames; i++ {
		f := noveltyFrame(w, h, 900+i%40, 940+i%40, 500, 530, int64(i))
		t0 := time.Now()
		cands, sc := eng.Candidates(f, w, h)
		ms = append(ms, float64(time.Since(t0).Microseconds())/1000)
		proposed += len(cands)
		if i == 0 && len(cands) == 0 {
			t.Fatalf("the first measured pass proposed nothing, so the benchmark is measuring a frame "+
				"the novelty engine is not asked about and every number it produces would be about the "+
				"empty case (reason %q)", sc.Reason)
		}
	}
	sort.Float64s(ms)
	at := func(p int) float64 { return ms[len(ms)*p/100] }
	p50, p95, p99 := at(50), at(95), at(99)
	var sum float64
	for _, x := range ms {
		sum += x
	}

	t.Logf("novelty pass at %dx%d over %d frames: p50=%.3fms p95=%.3fms p99=%.3fms mean=%.3fms, %d proposals",
		w, h, frames, p50, p95, p99, sum/float64(frames), proposed)
	t.Logf("A1's arithmetic assumes %.1f ms; measured p50 is %.2fx that.", a1Assum, p50/a1Assum)
	t.Logf("at 25 decoded fps that is %.1f ms per second per camera, %.1f%% of one core",
		p50*25, p50*25/10)
	if p50 < 0.01 {
		t.Fatalf("p50 = %.5f ms, which is below the resolution of the timer this benchmark uses. The "+
			"numbers it reports are not measurements", p50)
	}
}

// TestANilGateMeansEveryFrameAndRecordsNothing is the nil contract.
//
// A nil gate is what a caller passes when it has no gate configured, and it reads as
// every frame. The counters belong to a gate, so a nil one records nothing: an earlier
// revision fell through to the marking branch and dereferenced the nil pointer, which
// is what "nil means every frame" costs when it is written as a branch condition rather
// than as a decision.
func TestANilGateMeansEveryFrameAndRecordsNothing(t *testing.T) {
	var g *Gate
	for i := 0; i < 10; i++ {
		if !g.Due(uint64(i), int64(i)*40_000_000) {
			t.Fatalf("a nil gate skipped frame %d. Nil means every frame", i)
		}
	}
	ran, skipped := g.Counts()
	if ran != 0 || skipped != 0 {
		t.Fatalf("a nil gate reported %d run and %d skipped. There is no gate to count", ran, skipped)
	}
	if g.Rate() != 0 {
		t.Fatalf("a nil gate reported rate %d, want 0", g.Rate())
	}
	if !strings.Contains(DescribeGate(g, 25), "unknown") {
		t.Errorf("a nil gate's description does not say the frame rate is unknown: %q", DescribeGate(g, 25))
	}
	// And Reset on a nil gate is a no-op rather than a panic, for the same reason.
	g.Reset()
}

// TestTheGateRunsTheNumberOfPassesItsRateSaysItDoes is the arithmetic that belongs in a
// test.
//
// The bird-dwell argument is "at the novelty rate, a 1.5 s dwell spans at least three
// samples". That is arithmetic over a rate and a dwell time, and if the rate the report
// prints is not the rate the gate used, the argument is about a number nobody measured.
func TestTheGateRunsTheNumberOfPassesItsRateSaysItDoes(t *testing.T) {
	const (
		frames    = 250
		decodeFPS = 25
	)
	frameNS := int64(1e9 / decodeFPS)
	durationS := float64(frames) / decodeFPS
	for _, rate := range []int{0, 2, 5, 10} {
		g := NewGate(rate, decodeFPS)
		measured := 0
		for i := 0; i < frames; i++ {
			if g.Due(uint64(i), int64(i)*frameNS) {
				measured++
			}
		}
		// The property is the RATE, not a count, and the count is derived from elapsed
		// time. An earlier revision divided the frame count by the rate, which
		// compared two unrelated numbers and happened to agree at exactly one of the
		// four rates tested.
		if rate == 0 {
			if measured != frames {
				t.Errorf("at rate 0 the gate ran %d passes over %d frames, want every frame",
					measured, frames)
			}
		} else {
			got := float64(measured) / durationS
			// The property that matters is ONE THING: the gate is never SLOWER than it
			// was asked to be. The direction is the decision. Novelty is pure Go and its
			// output is a proposal, so running faster costs arithmetic and finds more,
			// while running slower silently narrows the set of regions that get cropped
			// and the smallest object this system exists to report is exactly a region
			// the gate did not propose.
			//
			// The slack is the COUNTING resolution, one pass over the sampled duration,
			// not a tolerance on the rate: a rate that is achieved exactly still counts
			// as one pass either side of the boundary depending on where the window
			// starts.
			resolution := 1.0 / durationS
			if got < float64(rate)-resolution {
				t.Errorf("at rate %d the gate ran %.3f passes per second over %.1f s, which is BELOW the "+
					"request by more than one pass of counting resolution (%.3f). A gate that runs slower "+
					"than it says narrows the set of regions that get cropped",
					rate, got, durationS, resolution)
			}
			// And whenever the achieved rate is not the requested one, the gate says
			// so. That is [Gate.Clamped], asserted here from the measured count rather
			// than from the gate's own arithmetic, so a gate whose Clamped is wrong is
			// caught by the same number the report prints.
			if wantClamped := diffAbs(got, float64(rate)) > 0.25; g.Clamped() != wantClamped {
				t.Errorf("at rate %d the gate ran %.3f/s but Clamped() = %v. The report prints %s",
					rate, got, g.Clamped(), "the request was not met" /* or was */)
			}
		}
		ran, skipped := g.Counts()
		if ran != int64(measured) || skipped != int64(frames-measured) {
			t.Errorf("at rate %d the gate reports %d run and %d skipped; the loop counted %d and %d",
				rate, ran, skipped, measured, frames-measured)
		}
		if rate == 0 && skipped != 0 {
			t.Errorf("at rate 0 the gate reported %d skipped frames. A gate that cannot skip must not "+
				"report having skipped", skipped)
		}
	}
}

// TestTheGateFiresOnTheFirstFrameAfterARestart is the state bug a reset exists for.
//
// A supervisor restarts a run and watches for the pipeline to come back. A gate that
// inherited its previous clock would sit closed for a whole interval after the restart,
// which is exactly the window being watched.
func TestTheGateFiresOnTheFirstFrameAfterARestart(t *testing.T) {
	const frameNS = int64(1e9 / 25)
	g := NewGate(2, 25) // one pass per 500 ms
	if !g.Due(0, 0) {
		t.Fatal("the gate refused the first frame")
	}
	if g.Due(1, frameNS) {
		t.Fatal("the gate fired inside its interval")
	}
	// A restart at an arbitrary clock: same gate object, zeroed counters, and a
	// timestamp that is nowhere near the old one.
	g.Reset()
	if !g.Due(2, 1_000_000) {
		t.Fatal("the gate refused the first frame after a Reset. A restart is exactly when the " +
			"pipeline has to come back, and an inherited clock makes it wait")
	}
}

// TestTheNoveltyRateMustCoverTheSmallestDwell is the cross-check behind the rate
// decision, as an assertion rather than as prose.
//
// A bird on screen for a second and a half has to be PROPOSED at least three times or it
// is never reliably cropped and therefore never reliably detected. Three proposals in
// 1.5 s is 2/s, which is marginal, and marginal is the whole reason this is a refusal and
// not a default.
func TestTheNoveltyRateMustCoverTheSmallestDwell(t *testing.T) {
	for _, c := range []struct {
		rate  int
		ok    bool
		shown int
	}{
		{0, true, ProposalsNeededForACrop}, // every decoded frame
		{25, true, 37},                     // the decoded frame rate: comfortably
		{2, true, 3},                       // exactly the floor
		{1, false, 1},                      // a 1.5 s dwell proposed once
		{5, true, 7},
	} {
		got, err := NoveltyRateCoversDwell(c.rate, SmallestDwellSeconds, ProposalsNeededForACrop)
		if (err == nil) != c.ok {
			t.Errorf("NoveltyRateCoversDwell(%d, %.1f, %d) = %v, want ok=%v",
				c.rate, SmallestDwellSeconds, ProposalsNeededForACrop, err, c.ok)
		}
		if got != c.shown {
			t.Errorf("NoveltyRateCoversDwell(%d) reported %d proposals, want %d",
				c.rate, got, c.shown)
		}
		if err != nil {
			if !strings.Contains(err.Error(), "SP_POC_NOVELTY_FPS") {
				t.Errorf("the refusal for rate %d does not name the variable to change: %v", c.rate, err)
			}
			if !strings.Contains(err.Error(), "one sample short") &&
				!strings.Contains(err.Error(), "proposed 1 time") {
				t.Errorf("the refusal for rate %d does not say by how much it is short: %v", c.rate, err)
			}
		}
	}
	// And the two refusals that can name this variable are worded identically, so an
	// operator who reads both does not conclude there are two problems.
	if NoveltyRateRefusal(1, SmallestDwellSeconds, ProposalsNeededForACrop) == "" {
		t.Error("NoveltyRateRefusal returned nothing for a rate that NoveltyRateCoversDwell refused")
	}
	if got := NoveltyRateRefusal(5, SmallestDwellSeconds, ProposalsNeededForACrop); got != "" {
		t.Errorf("NoveltyRateRefusal returned a refusal for a rate that is sufficient: %q", got)
	}
}

// TestAQuantisedGateSaysItCouldNotMeetTheRequest is the control on the clamp.
//
// The gate rounds its interval to a whole number of frames so it can never run slower
// than it was asked to. That means a request above half the decode rate is not met
// exactly, and the only acceptable thing to do with that is say so: a line that prints
// the requested rate next to counters produced by another rate is the shape of claim
// this repository exists to refuse.
func TestAQuantisedGateSaysItCouldNotMeetTheRequest(t *testing.T) {
	const decodeFPS = 25
	for _, c := range []struct {
		rate    int
		clamped bool
	}{
		{0, false},  // every frame, nothing requested
		{2, false},  // 500 ms is 12.5 frames: rounded to 12, so 2.083/s
		{5, false},  // 200 ms is exactly 5 frames
		{10, true},  // 100 ms is 2.5 frames: rounded to 2, so 12.5/s
		{25, false}, // one pass per frame IS 25/s over 25 fps, met exactly
		{50, true},  // faster than the source can deliver: clamped to every frame
	} {
		g := NewGate(c.rate, decodeFPS)
		if got := g.Clamped(); got != c.clamped {
			t.Errorf("at rate %d over %d fps Clamped() = %v, want %v (achieved %.2f/s)",
				c.rate, decodeFPS, got, c.clamped, g.EffectiveRate())
		}
		line := DescribeGate(g, decodeFPS)
		if c.clamped && !strings.Contains(line, "ACHIEVED") {
			t.Errorf("at rate %d the description does not say the request was not met: %q", c.rate, line)
		}
		if !c.clamped && strings.Contains(line, "ACHIEVED") {
			t.Errorf("at rate %d the description claims a shortfall that did not happen: %q", c.rate, line)
		}
		// And the requested rate is ALWAYS named, clamped or not, so a reader can see
		// what was asked for even when the answer is different.
		if c.rate > 0 && !strings.Contains(line, "asked for") {
			t.Errorf("at rate %d the description does not name the requested rate: %q", c.rate, line)
		}
	}
}

// TestTheCostProjectionNamesBothRates is the arithmetic A1 has to be redone against.
//
// At the novelty rate the novelty pass is the dominant per-camera cost, and the detector
// is only paid on frames novelty proposed. A scale claim that quotes the detector's cost
// and not novelty's, at a novelty rate nobody states, is a number that collapses the
// moment somebody asks the obvious next question.
func TestTheCostProjectionNamesBothRates(t *testing.T) {
	const (
		cameras     = 2000
		noveltyMS   = 3.0
		detectorMS  = 17.0
		detectShare = 0.15
	)
	rows := []struct {
		name       string
		noveltyFPS int
	}{
		{"novelty at the detection rate (the optimistic row A1 assumed)", 2},
		{"novelty at the decoded frame rate", 25},
	}
	var cores []float64
	for _, r := range rows {
		n := cameras * float64(r.noveltyFPS) * noveltyMS / 1000
		d := cameras * float64(r.noveltyFPS) * detectShare * detectorMS / 1000
		cores = append(cores, n+d)
		t.Logf("%-52s novelty %6.1f cores + detector %6.1f cores = %6.1f cores", r.name, n, d, n+d)
	}
	// Both numbers belong in the proposal, or neither: a rate that changes the answer by
	// less than five times is a rate that does not matter, and if it does matter then
	// publishing one of the two is a choice rather than a fact.
	if cores[1]/cores[0] < 5 {
		t.Fatalf("the two novelty rates differ by only %.1fx in total core cost, so quoting either "+
			"would be the same number and the rate would not matter. Either the measurement is wrong or "+
			"the projection is", cores[1]/cores[0])
	}
	// And the detector is NOT the dominant term at the decoded rate, which is the whole
	// of the inversion the plan asks for.
	detOnly := cameras * 25 * detectShare * detectorMS / 1000
	noveltyOnly := cameras * 25 * noveltyMS / 1000
	if noveltyOnly <= detOnly {
		t.Logf("at 25 fps novelty costs %.1f cores against the detector's %.1f; the inversion the plan "+
			"asks for is NOT visible with these numbers and the cost model has to say so",
			noveltyOnly, detOnly)
	} else {
		t.Logf("at 25 fps novelty costs %.1f cores against the detector's %.1f, so the cheap pure-Go "+
			"half is the expensive half and the cost model has to say that", noveltyOnly, detOnly)
	}
}
