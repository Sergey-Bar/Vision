// Package detectclient re-exports the AI sidecar client to modules outside
// this one. The YouTube POC server (module github.com/Sergey-Bar/vision)
// lives outside this module's path, so it cannot import internal/ai directly —
// and it must not grow a second copy of the wire, because the wire is pinned
// from both sides and two clients are how one side drifts.
//
// Everything here is an alias or a one-line forwarder. The checks, the strict
// decoder and the Interpret tripwire stay in internal/ai where their tests are.
package detectclient

import (
	"context"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/ai"
)

// Box is one detection in normalised frame coordinates — the wire format.
type Box = ai.Box

// Result is one sidecar answer, including the absence state.
type Result = ai.Result

// Telemetry is the sidecar's own report about how it decoded a frame.
type Telemetry = ai.Telemetry

// ProbeResult is what /healthz answers.
type ProbeResult = ai.ProbeResult

// Client talks to the Python sidecar over loopback HTTP.
type Client = ai.Client

// Latencies records the round-trip distribution.
type Latencies = ai.Latencies

// The absence vocabulary, aliased so both modules name one set of states.
const (
	StateAnswered   = ai.StateAnswered
	StateNoObjects  = ai.StateNoObjects
	StateDegraded   = ai.StateDegraded
	StateNotAsked   = ai.StateNotAsked
	StateCannotTell = ai.StateCannotTell
)

// AbsenceOf reports whether a state means "we do not know whether anything
// was there".
func AbsenceOf(state string) bool { return ai.AbsenceOf(state) }

// New builds a client for a sidecar base URL with a per-frame deadline.
func New(baseURL string, deadline time.Duration) (*ai.Client, error) {
	return ai.New(baseURL, deadline)
}

// Probe asks the sidecar whether it is ready.
func Probe(ctx context.Context, baseURL string, deadline time.Duration) (ai.ProbeResult, error) {
	c, err := ai.New(baseURL, deadline)
	if err != nil {
		return ai.ProbeResult{}, err
	}
	return c.Probe(ctx)
}

// NewLatencies builds a rolling latency window.
func NewLatencies(limit int) *ai.Latencies { return ai.NewLatencies(limit) }
