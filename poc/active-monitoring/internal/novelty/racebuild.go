//go:build race

package novelty

// raceEnabled reports that this test binary was built with the race detector.
//
// It exists for one caller: the 1080p novelty benchmark, whose subject is
// WALL-CLOCK time per pass. The race detector instruments every memory access,
// which inflates that number by more than an order of magnitude, so a run with
// it prints a cost that is true of the instrumentor and false of the code. The
// benchmark's own doc comment says the numbers are printed and never asserted
// precisely because they are a property of the machine; adding an instrumentor
// to the machine does not make them a property of the code either.
//
// The failure this prevents was measured, not hypothesised: under -race the
// 2,000-frame run did not finish inside Go's 10-minute test timeout and panicked
// (">300 ms per pass against 3.5 ms uninstrumented, measured side by side on the
// same machine — an ~85x inflation"), and that panic aborted the whole package —
// so the race detector never got to report on the tests in this package that ARE
// concurrently interesting. One benchmark measuring the wrong thing was costing
// every other test its instrumented run.
//
// Two files rather than a runtime probe, because there is no supported way to
// ask the race runtime whether it is on from ordinary code, and a build
// constraint is the question asked at the only moment the answer is stable.
const raceEnabled = true
