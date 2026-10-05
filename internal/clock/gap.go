package clock

import (
	"fmt"
	"time"
)

// A recording gap is not a quiet scene, and the difference is the whole point of
// this file.
//
// The plan's §9 lists it among the things "easy to forget": a camera that was
// restarting, a network blip, a storage pause. In each case the evidence the
// window needs does not exist, and the only record is the *absence* of events.
// An evaluation that treats that absence as "nothing happened" is not wrong
// about the data - it is wrong about what the data means, and it is wrong
// silently.
//
// So a gap is a first-class outcome with a name, and it is the one outcome that
// can never be resolved by looking harder at the same window.

// GapKind names why a window has no evidence.
type GapKind string

// The gap kinds, all distinct because each sends an operator somewhere
// different.
const (
	// GapNone is the absence of a gap: the window is covered.
	GapNone GapKind = ""
	// GapNoEvents means the camera reported nothing at all in the window. Either
	// the scene was quiet or the camera was not running, and the vendor's own
	// health state is the only thing that distinguishes them.
	GapNoEvents GapKind = "no_events"
	// GapDeviceOffline means the device reported itself offline for part or all
	// of the window. This one is a fact, not an inference.
	GapDeviceOffline GapKind = "device_offline"
	// GapClockUnknown means the window could not be placed on the reference clock
	// because the device's clock was never measured. The events may all be there
	// and still be unplaceable, which is the worst case: the data exists and
	// cannot be used.
	GapClockUnknown GapKind = "clock_unknown"
	// GapExcludedByDrift means the device's clock was measured and the window
	// fell outside the severity the product will compute a correlation on.
	GapExcludedByDrift GapKind = "excluded_by_drift"
	// GapEvidenceUnavailable means the vendor's own recording cannot be fetched
	// for the window - retention, a broken archive, an offline recorder. The
	// event is real and its evidence is gone.
	GapEvidenceUnavailable GapKind = "evidence_unavailable"
)

// Gap is a window with no usable evidence, and why.
//
// It is a value rather than a bool because every consumer of it has to do
// something different per kind, and a bool forces all of them to re-derive the
// distinction from whatever else happened to be nearby.
type Gap struct {
	Kind GapKind
	// Detail is the vendor's own words where there were any. It is prose and
	// nothing parses it.
	Detail string
	// Covered is the fraction of the window for which evidence exists, in
	// [0,1]. A gap over part of a window is a gap, and "partly" is the case
	// that a boolean throws away.
	Covered float64
	// From and To are the window, on the reference clock where it could be
	// placed at all.
	From, To time.Time
}

// Present reports whether the window has usable evidence.
func (g Gap) Present() bool { return g.Kind == GapNone && g.Covered >= 1 }

// Partial reports whether some of the window is covered and some is not.
func (g Gap) Partial() bool { return g.Kind != GapNone && g.Covered > 0 && g.Covered < 1 }

// String renders the gap for an operator.
//
// It says what is missing and what it is not. "No events in window 12:00:08 to
// 12:00:16" is a fact; adding "this is not a finding that nothing happened" is
// what stops it being read as one.
func (g Gap) String() string {
	if g.Kind == GapNone && g.Covered >= 1 {
		return "evidence present for the whole window"
	}
	span := ""
	if !g.From.IsZero() && !g.To.IsZero() {
		span = fmt.Sprintf(" window %s to %s",
			g.From.UTC().Format(time.RFC3339), g.To.UTC().Format(time.RFC3339))
	}
	var covered string
	switch {
	case g.Covered <= 0:
		covered = " no part of the window is covered."
	case g.Covered >= 1:
		covered = " the window is fully covered, so this is a retrieval failure rather than a gap in the recording."
	default:
		covered = fmt.Sprintf(" %.0f%% of the window is covered; the rest is missing.", g.Covered*100)
	}
	detail := ""
	if g.Detail != "" {
		detail = fmt.Sprintf(" The device said: %s", g.Detail)
	}
	return fmt.Sprintf("%s%s.%s%s This is an absence of evidence, not a finding that nothing happened.",
		g.Kind, span, covered, detail)
}

// GapState is the evidence a caller supplies about one window.
//
// It is a struct rather than a list of arguments because the fields are about
// the same window and adding a fourth boolean is how a call site ends up passing
// them in the wrong order.
type GapState struct {
	// EventCount is how many events the device reported in the window.
	EventCount int
	// OfflineFor is how long the device reported itself offline inside the
	// window. A non-zero value is a fact from the vendor, not an inference.
	OfflineFor time.Duration
	// Window is the window being evaluated.
	Window Window
	// State is the device's clock state, or nil when there is none.
	State *DeviceState
	// EvidenceFetched reports whether the vendor's recording for the window was
	// actually retrievable. An event whose evidence cannot be fetched is an event
	// an operator cannot review, which is a different problem from an event that
	// never happened.
	EvidenceFetched bool
	// EvidenceNeeded is whether this evaluation needs the recording at all. A
	// pure clock-correlation does not, and treating its window as gapped would
	// mark most of a healthy system as broken.
	EvidenceNeeded bool
}

// AssessGap decides whether a window has usable evidence, and names the reason
// when it does not.
//
// The order is deliberate and it is the whole design: the *most specific* fact
// wins. A device that reported itself offline is not also "a device that
// reported nothing", and telling an operator the second thing sends them to check
// a camera that was switched off.
//
// A window with no gap returns GapNone with Covered 1, and a caller that only
// checks `Present` is right. A caller that only checks `Kind == GapNone` is
// *also* right, because a partial gap never returns GapNone - the two cannot
// disagree, which is the property that makes this safe to use from a gate.
func AssessGap(s GapState) Gap {
	g := Gap{Covered: 1, From: s.Window.From, To: s.Window.To}

	if s.OfflineFor > 0 {
		g.Kind = GapDeviceOffline
		g.Detail = "the device reported itself offline"
		g.Covered = 1 - fraction(s.OfflineFor, s.Window.To.Sub(s.Window.From))
		if g.Covered < 0 {
			g.Covered = 0
		}
		return g
	}

	// A device whose clock was never measured cannot have its events placed, and
	// unplaceable events are unusable however many of them there are.
	if s.State == nil {
		g.Kind = GapClockUnknown
		g.Covered = 0
		return g
	}
	if !s.State.MeasuredAt.IsZero() {
		// The correction is only usable if the measurement is itself current. A
		// drift figure from last month is not a correction, it is a coincidence.
		if age := s.Window.To.Sub(s.State.MeasuredAt); age > DriftMeasurementMaxAge || age < -time.Minute {
			g.Kind = GapClockUnknown
			g.Covered = 0
			g.Detail = fmt.Sprintf("the clock was measured %s before the window, which is longer than %s",
				age.Round(time.Minute), DriftMeasurementMaxAge)
			return g
		}
	}

	// Placement first, then content: a device in the severe drift band is
	// excluded by the policy whatever it happened to report, and saying "no
	// events" would be a second true sentence about a window the product has
	// already refused to correlate.
	if allowed, exclusion := DefaultPolicy().CorrelationAllowed(s.State); !allowed {
		g.Kind = GapExcludedByDrift
		g.Covered = 0
		if exclusion != nil {
			g.Detail = exclusion.Reason
		}
		return g
	}

	if s.EventCount == 0 {
		g.Kind = GapNoEvents
		g.Covered = 0
		g.Detail = "the device reported no events in the window"
		return g
	}

	if s.EvidenceNeeded && !s.EvidenceFetched {
		g.Kind = GapEvidenceUnavailable
		g.Covered = 1
		g.Detail = "events were reported but the recording for the window could not be retrieved"
		return g
	}
	return g
}

// DriftMeasurementMaxAge is how old a clock measurement may be and still be used
// as a correction.
const DriftMeasurementMaxAge = 6 * time.Hour

func fraction(part, whole time.Duration) float64 {
	if whole <= 0 {
		return 0
	}
	f := float64(part) / float64(whole)
	if f > 1 {
		return 1
	}
	if f < 0 {
		return 0
	}
	return f
}
