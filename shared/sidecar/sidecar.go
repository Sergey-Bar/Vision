// Package sidecar is the public face of the Python detector sidecar client.
//
// The implementation is `github.com/Sergey-Bar/vision/internal/sidecar`. It is
// internal because the sidecar is an untrusted peer on loopback, and its wire
// format is pinned by tests that must not be reachable from outside the
// module.
//
// The POC module speaks the same wire, so it comes here, over the module
// boundary, as aliases of the same types.
package sidecar

import "github.com/Sergey-Bar/vision/internal/sidecar"

// The wire.
type (
	// Box is a detected region.
	Box = sidecar.Box
	// Client is the sidecar client.
	Client = sidecar.Client
	// Latencies is the bounded window of measured round trips.
	Latencies = sidecar.Latencies
	// ProbeResult is what a health probe reported.
	ProbeResult = sidecar.ProbeResult
	// Response is the strict wire response.
	Response = sidecar.Response
	// Result is one frame's answer.
	Result = sidecar.Result
	// Telemetry is what the sidecar said about its own decode.
	Telemetry = sidecar.Telemetry
)

// The absence vocabulary.
const (
	StateAnswered   = sidecar.StateAnswered
	StateNoObjects  = sidecar.StateNoObjects
	StateDegraded   = sidecar.StateDegraded
	StateNotAsked   = sidecar.StateNotAsked
	StateCannotTell = sidecar.StateCannotTell
)

// The functions.
var (
	// AbsenceOf reports whether a state is an absence rather than an answer.
	AbsenceOf = sidecar.AbsenceOf
	// Interpret reads a sidecar answer into a state and a reason.
	Interpret = sidecar.Interpret
	// New opens a sidecar client.
	New = sidecar.New
	// NewLatencies opens a bounded latency window.
	NewLatencies = sidecar.NewLatencies
)
