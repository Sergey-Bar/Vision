// Package detectclient re-exports the detector sidecar client to the callers
// inside this module that want the shorter name.
//
// # Why the client lives in the product module now
//
// The implementation is `github.com/Sergey-Bar/vision/internal/sidecar`, and
// its public face is `github.com/Sergey-Bar/vision/shared/sidecar`. The sidecar
// wire moved into the product module along with the detector, because the wire
// and the decoder are two halves of one contract and a contract with halves in
// two modules is a contract that drifts.
//
// This package is a second name for the same types, for the callers inside this
// module that already say `detectclient`. Everything here is an alias or a
// one-line forwarder; the checks, the strict decoder and the Interpret
// tripwire stay in the product module where their tests are.
package detectclient

import (
	"context"
	"time"

	"github.com/Sergey-Bar/vision/shared/sidecar"
)

// Box is one detection in normalised frame coordinates — the wire format.
type Box = sidecar.Box

// Result is one sidecar answer, including the absence state.
type Result = sidecar.Result

// Telemetry is the sidecar's own report about how it decoded a frame.
type Telemetry = sidecar.Telemetry

// ProbeResult is what /healthz answers.
type ProbeResult = sidecar.ProbeResult

// Client talks to the Python sidecar over loopback HTTP.
type Client = sidecar.Client

// Latencies records the round-trip distribution.
type Latencies = sidecar.Latencies

// The absence vocabulary, aliased so both modules name one set of states.
const (
	StateAnswered   = sidecar.StateAnswered
	StateNoObjects  = sidecar.StateNoObjects
	StateDegraded   = sidecar.StateDegraded
	StateNotAsked   = sidecar.StateNotAsked
	StateCannotTell = sidecar.StateCannotTell
)

// AbsenceOf reports whether a state means "we do not know whether anything
// was there".
func AbsenceOf(state string) bool { return sidecar.AbsenceOf(state) }

// New builds a client for a sidecar base URL with a per-frame deadline.
func New(baseURL string, deadline time.Duration) (*sidecar.Client, error) {
	return sidecar.New(baseURL, deadline)
}

// Probe asks the sidecar whether it is ready.
func Probe(ctx context.Context, baseURL string, deadline time.Duration) (sidecar.ProbeResult, error) {
	c, err := sidecar.New(baseURL, deadline)
	if err != nil {
		return sidecar.ProbeResult{}, err
	}
	return c.Probe(ctx)
}

// NewLatencies builds a rolling latency window.
func NewLatencies(limit int) *sidecar.Latencies { return sidecar.NewLatencies(limit) }
