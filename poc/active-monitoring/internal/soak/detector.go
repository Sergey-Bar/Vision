package soak

import (
	"github.com/Sergey-Bar/vision/shared/ortdetect"
	"github.com/Sergey-Bar/vision/shared/sidecar"
)

// ORTDetector adapts *ortdetect.Detector to the [Detector] interface.
//
// The adapter exists for two reasons, and neither of them is "to avoid a little
// boilerplate".
//
// First, the two packages must not know each other's ANSWER types.
// `ortdetect.Answer` is the decoder's own shape and `DetectAnswer` is the shape the
// frame loop wants. Sharing one type would make `ortdetect` depend on the frame
// loop's vocabulary, and this package is a leaf that a boundary check holds to
// importing nothing but the box type, the novelty candidate and the absence states.
//
// Second, the throughput gate needs the detector's OWN latency distribution and
// `soak` cannot reach it through the interface: a method on the interface named
// `LatencyPercentiles` would be a detector concern leaking into a three-method
// interface that a test double would then have to implement. The adapter holds the
// pointer instead, so the interface stays small and the gate still gets the real
// numbers rather than the runner's average.
type ORTDetector struct {
	d   *ortdetect.Detector
	lat *ortdetect.Latencies
}

// NewORTDetector wraps a detector for the frame loop.
//
// A nil detector is refused rather than wrapped: an adapter holding nothing would
// satisfy the interface and report `not_asked` on every frame, which is a run that
// looks like a camera fault and is not one.
func NewORTDetector(d *ortdetect.Detector) (*ORTDetector, error) {
	if d == nil {
		return nil, &ortdetect.Refusal{
			What: "the detector adapter was given no detector",
			Detail: "open a session and build a detector before wiring one into a run; an adapter " +
				"holding nothing reports not_asked on every frame, which looks like a camera fault " +
				"and is not one",
		}
	}
	return &ORTDetector{d: d, lat: d.Latencies()}, nil
}

// Detect measures one frame, or one crop of it, and reports what it found and what it
// cost.
//
// The zero Rect is the whole frame, so every call site that had no crop pass before the
// parameter existed is unchanged by it.
func (a *ORTDetector) Detect(seq uint64, rgb []byte, w, h int, crop ortdetect.Rect) DetectAnswer {
	ans := a.d.Detect(seq, rgb, w, h, crop)
	out := DetectAnswer{
		State:     ans.State,
		Telemetry: ans.Telemetry,
		LatencyMS: ans.LatencyMS,
		Refusal:   ans.Refusal,
		Crop:      ans.Crop,
	}
	// The boxes are COPIED, not aliased. The detector reuses its buffers between
	// frames, so an aliased slice would be rewritten by the next frame while the
	// tracker was still holding it — a box that changes under a track, with no error
	// and a report describing a frame nobody has.
	//
	// The copy is of the slice header's contents, so the cost is one allocation per
	// measured frame, which at 100 boxes is a few kilobytes against a detector
	// costing milliseconds.
	if len(ans.Boxes) > 0 {
		out.Boxes = make([]sidecar.Box, len(ans.Boxes))
		copy(out.Boxes, ans.Boxes)
	}
	return out
}

// LatencyPercentiles returns the detector's own p50, p95 and p99 over its window.
//
// Each is nil when it could not be computed, and nil is the honest answer over an
// empty window: a p99 with no samples is not zero, and a report printing 0 ms
// because nothing had been measured would claim the detector was infinitely fast.
//
// The gate's refusal reads these and nothing else, so the number it refuses on and
// the number the report prints are the same measurement.
func (a *ORTDetector) LatencyPercentiles() (p50, p95, p99 *float64) {
	if a == nil || a.lat == nil {
		return nil, nil, nil
	}
	if v, ok := a.lat.P50(); ok {
		p50 = &v
	}
	if v, ok := a.lat.P95(); ok {
		p95 = &v
	}
	if v, ok := a.lat.P99(); ok {
		p99 = &v
	}
	return p50, p95, p99
}

// Latencies returns the live distribution, for the preflight throughput gate.
func (a *ORTDetector) Latencies() *ortdetect.Latencies { return a.lat }

// Identity returns what a decision made from this detector's boxes must name.
func (a *ORTDetector) Identity() ortdetect.Identity { return a.d.Identity() }

// Inner returns the wrapped detector, for `cmd/watch` to reach the session it owns.
func (a *ORTDetector) Inner() *ortdetect.Detector { return a.d }

// Session returns the session, for whoever opened it to close it.
func (a *ORTDetector) Session() *ortdetect.Session { return a.d.Session() }

// latencyPercentiles is the shape a detector exposes for the gate, as an interface
// so a test double can supply one without a session.
type latencyPercentiles interface {
	LatencyPercentiles() (p50, p95, p99 *float64)
}

// percentilesFor returns the percentiles from whichever detector is in use, or three
// nils.
//
// The interface check rather than a type assertion on the concrete type, so a test
// double in the soak package can participate in the attribution path — which is
// what makes the attribution testable at all, since a real detector needs a session
// and a session needs a DLL.
func percentilesFor(d Detector) (p50, p95, p99 *float64) {
	if lp, ok := d.(latencyPercentiles); ok {
		return lp.LatencyPercentiles()
	}
	return nil, nil, nil
}
