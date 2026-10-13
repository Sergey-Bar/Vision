//go:build !race

package novelty

// raceEnabled is false when this test binary was built normally.
//
// The benchmark that reads it then runs, which is where its numbers are valid:
// a per-pass cost measured without an instrumentor is the cost the scale
// proposal quotes, and the same number measured with one is a fact about the
// instrumentor. See racebuild.go for the other half and the measurement that
// prompted both.
const raceEnabled = false
