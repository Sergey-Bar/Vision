// Package clock is the time subsystem.
//
// Clock correctness is load-bearing here: door plus camera plus access
// correlation breaks without it. A site whose cameras are 40 seconds apart will
// happily report a person entering a restricted zone and the door opening as two
// unrelated events, and an operator who cannot trust the timeline stops trusting
// the product.
//
// The three timestamps are kept apart on purpose:
//
//	observed_at   what the source claimed
//	received_at   our ingest
//	normalized_at drift-corrected, and the only field a time-window predicate
//	              may read
//
// Collapsing them into one is the bug this package exists to prevent. A rule
// that reads observed_at is reading the source's clock, which is the thing under
// suspicion.
package clock

import (
	"errors"
	"fmt"
	"time"
)

// Source names where a timestamp's authority came from.
type Source string

const (
	SourceNTP     Source = "ntp"
	SourceVMS     Source = "vms"
	SourceHost    Source = "host"
	SourceUnknown Source = "unknown"
)

// Band is how far a device has drifted from the reference.
type Band string

const (
	// BandOK is inside the site's warn threshold.
	BandOK Band = "ok"
	// BandWarn is past warn but inside severe. Correlation still uses it, and
	// the drift is recorded on the verification result.
	BandWarn Band = "warn"
	// BandSevere excludes the device from time-windowed corroboration entirely.
	// The exclusion is recorded, never applied silently.
	BandSevere Band = "severe"
)

// Policy is a site's clock policy. It is a rule-bundle parameter, not a global
// constant: two sites with different reference clocks have different answers, and
// hard-coding one would silently apply one site's assumptions to another.
type Policy struct {
	// Reference is the clock everything is measured against.
	Reference Source
	// WarnMs and SevereMs bound the drift bands. Severe must be greater than
	// warn; a policy that says otherwise is a configuration error.
	WarnMs   int32
	SevereMs int32
	// SkewToleranceMs bounds how far apart two normalized timestamps may be and
	// still be considered the same moment. It is the number a rule's time
	// window is measured against, and it is deliberately separate from the drift
	// bands: a site can accept 5 seconds of skew in a correlation window while
	// still warning at 2 seconds of device drift.
	SkewToleranceMs int32
}

// DefaultPolicy is the starting point for a site that has not yet measured its
// clocks. It is intentionally strict, because a policy that has never been
// configured should exclude doubtful correlations rather than include them.
func DefaultPolicy() Policy {
	return Policy{
		Reference:       SourceNTP,
		WarnMs:          2000,
		SevereMs:        10000,
		SkewToleranceMs: 5000,
	}
}

// Validate rejects a policy that cannot be evaluated.
func (p Policy) Validate() error {
	switch p.Reference {
	case SourceNTP, SourceVMS, SourceHost:
	default:
		return fmt.Errorf("clock: reference must be ntp, vms or host, got %q", p.Reference)
	}
	if p.WarnMs < 0 || p.SevereMs < 0 {
		return errors.New("clock: drift thresholds cannot be negative")
	}
	if p.SevereMs <= p.WarnMs {
		return fmt.Errorf("clock: severe threshold %dms must exceed warn threshold %dms; "+
			"a device in the severe band would never be distinguishable from one in warn",
			p.SevereMs, p.WarnMs)
	}
	if p.SkewToleranceMs < 0 {
		return errors.New("clock: skew tolerance cannot be negative")
	}
	return nil
}

// Band classifies a drift measurement.
func (p Policy) Band(driftMs int32) Band {
	abs := driftMs
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= p.SevereMs:
		return BandSevere
	case abs >= p.WarnMs:
		return BandWarn
	default:
		return BandOK
	}
}

// DeviceState is what the drift subsystem knows about one device's clock.
type DeviceState struct {
	TenantID string
	DeviceID string
	SiteID   string
	Source   Source
	// DriftMs is measured as source minus reference. A positive value means the
	// device runs ahead.
	DriftMs     int32
	Band        Band
	ReferenceID string
	MeasuredAt  time.Time
}

// Normalized converts an observed timestamp into a normalized one.
//
// The correction is additive: the device's measured drift is subtracted from the
// device's own claim, so an event a 40-second-fast camera stamped at 09:00:20
// normalizes to 09:00:00 on the reference clock.
//
// A zero observed time is a real case, not an error: a source that sent an
// unreadable timestamp produced a record with no trustworthy observation, and
// the fallback to received_at must be visible rather than silent. The caller is
// told which happened.
func Normalized(observed, received time.Time, state *DeviceState, p Policy) (normalized time.Time, usedFallback bool) {
	if observed.IsZero() {
		if received.IsZero() {
			return time.Time{}, true
		}
		return received.UTC(), true
	}
	if state == nil {
		// No measurement for this device means no correction, not an assumed
		// correction of zero with a confident-looking band.
		return observed.UTC(), false
	}
	return observed.UTC().Add(-time.Duration(state.DriftMs) * time.Millisecond), false
}

// Exclusion records a device that was not trusted in a time window.
type Exclusion struct {
	DeviceID string
	DriftMs  int32
	Band     Band
	Reason   string
}

// Window pairs two instants with the tolerance that makes them comparable.
type Window struct {
	From, To  time.Time
	Tolerance time.Duration
}

// Contains reports whether t falls inside the window, allowing the tolerance on
// both edges.
//
// The tolerance is a site parameter rather than a constant because "simultaneous"
// means different things on a site with 2000 cameras on a shared network segment
// and on a site with six cameras on dedicated cabling.
func (w Window) Contains(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	lo := w.From.Add(-w.Tolerance)
	hi := w.To.Add(w.Tolerance)
	return !t.Before(lo) && !t.After(hi)
}

// CorrelationAllowed reports whether a device's clock may be used for
// time-windowed corroboration, and says why when it may not.
//
// Returning the reason rather than a bare bool is what lets the verification
// result record the exclusion. A silently dropped corroborating signal is a
// missing explanation; a recorded one is an answer the operator can read.
func (p Policy) CorrelationAllowed(state *DeviceState) (bool, *Exclusion) {
	if state == nil {
		return true, nil
	}
	band := state.Band
	if band == "" {
		band = p.Band(state.DriftMs)
	}
	if band != BandSevere {
		return true, nil
	}
	return false, &Exclusion{
		DeviceID: state.DeviceID,
		DriftMs:  state.DriftMs,
		Band:     band,
		Reason: fmt.Sprintf("device clock drift %dms is in the severe band (>= %dms); "+
			"excluded from time-windowed corroboration", state.DriftMs, p.SevereMs),
	}
}

// SkewExceeds reports whether two normalized timestamps are further apart than
// the site's tolerance permits, which is the condition a rule's window tests.
func (p Policy) SkewExceeds(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d > time.Duration(p.SkewToleranceMs)*time.Millisecond
}

// Lag is the ingest delay, which the observability contract names as
// ingest_lag_seconds. It is measured between what the source claimed and when we
// accepted it, so a site can see a source that is falling behind before its
// events start correlating wrongly.
func Lag(observed, received time.Time) (time.Duration, bool) {
	if observed.IsZero() || received.IsZero() {
		return 0, false
	}
	d := received.UTC().Sub(observed.UTC())
	if d < 0 {
		// The source claims to be in the future relative to our clock. That is
		// itself the finding, so it is reported as zero rather than clamped
		// and hidden.
		return 0, false
	}
	return d, true
}
