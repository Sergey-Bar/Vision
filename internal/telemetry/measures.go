// Package telemetry provides live KPIs for the autonomy layer.
package telemetry

import (
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/camerahealth"
)

// Sample is a consistent, lock-free copy of the counters at one instant.
//
// The split from Measures exists for a reason `go vet` insists on: a method
// with a value receiver on a struct holding a `sync.RWMutex` copies the lock.
// Beyond the vet error, a derived KPI computed from counters that are still
// being written can read as a number that never existed — 4 detections and 3
// of them correlated is 75%, and a half-written read can produce both halves
// from different moments. Snapshot once, derive from the snapshot.
//
// The camera-honesty counters: how many cameras have entered a named degraded
// state since the window opened. The states are camerahealth's and the set is
// its Degraded predicate, so a stale camera is degraded for the brief, for
// coverage and for the capture plan — and here too, which is the point: one
// input, one answer, everywhere.
type Sample struct {
	TotalDetections  int64
	TotalIncidents   int64
	TotalEscalations int64
	TotalOverrides   int64
	// FalseEscalations is the escalated-then-overridden count: the number of
	// times the autonomy layer escalated and an operator took it back. It is
	// the numerator of the only companion measure that matters to a buyer,
	// because an escalation budget spent on the wrong event is a control room
	// that learns to ignore the screen.
	FalseEscalations            int64
	DetectionsInCorrelated      int64
	DetectionsWithZoneContext   int64
	TotalTimeToDecision         time.Duration
	DecisionCount               int64
	NegativeCreatedForMissingAI int64
	AuditVerificationFailures   int64
	// Camera-honesty counters: how many cameras have entered a named
	// degraded health state since the window opened. A camera that is
	// silent (no frames) or blind (no usable image) is a camera nobody is
	// watching, and the ~30% of organisations that still check device
	// health by hand find that out after the incident.
	// CameraStales is the third degraded state, counted for the same reason as
	// the other two: `camerahealth.Degraded` says a stale camera is not
	// watching, and a counter that disagrees with the brief and the coverage
	// section is a counter nobody trusts.
	CamerasSilent int64
	CamerasBlind  int64
	CamerasStale  int64
	WindowStart   time.Time
}

// Measures holds the live telemetry counters behind a lock, because the
// autonomy pass writes them while a dashboard reads them.
type Measures struct {
	mu sync.RWMutex
	s  Sample
}

// newMeasures creates a new measures instance.
func newMeasures() *Measures {
	return &Measures{s: Sample{WindowStart: time.Now()}}
}

// RecordDetection records a detection.
func (m *Measures) RecordDetection() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.TotalDetections++
}

// RecordIncident records a correlated incident.
func (m *Measures) RecordIncident() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.TotalIncidents++
}

// RecordEscalation records an escalation.
func (m *Measures) RecordEscalation() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.TotalEscalations++
}

// RecordOverride records an operator override of an autonomy decision.
func (m *Measures) RecordOverride() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.TotalOverrides++
}

// RecordFalseEscalation records an escalation an operator took back: the
// escalation happened AND the operator overrode it, which is the pair of
// facts the rate is made of. It is deliberately not recorded for every
// override — taking back an OBSERVE is a routine correction, not a false
// escalation.
func (m *Measures) RecordFalseEscalation() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.FalseEscalations++
	m.s.TotalOverrides++
}

// FalseEscalationRatePct is overrides-of-escalations per escalation. Zero
// when there is no escalation to divide by: a rate with an empty denominator
// is not 0% confidence, it is no measurement, and this reads 0 rather than
// telling a story.
func (s Sample) FalseEscalationRatePct() float64 {
	if s.TotalEscalations == 0 {
		return 0
	}
	return float64(s.FalseEscalations) / float64(s.TotalEscalations) * 100
}

// RecordCorrelated records the detections that were grouped into a correlated
// incident, which is what the correlation hit rate reads.
func (m *Measures) RecordCorrelated(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.DetectionsInCorrelated += int64(n)
}

// RecordTimeToDecision records the time from an incident's first detection to
// the playbook's decision.
func (m *Measures) RecordTimeToDecision(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.TotalTimeToDecision += d
	m.s.DecisionCount++
}

// RecordZoneContext records a detection that was matched to a zone.
func (m *Measures) RecordZoneContext() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.DetectionsWithZoneContext++
}

// RecordNegativeForMissingAI records a negative observation created from a
// missing AI observation. This violates invariant 3; it is counted so it can
// only ever read zero, and so a future change that breaks it is visible.
func (m *Measures) RecordNegativeForMissingAI() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.NegativeCreatedForMissingAI++
}

// RecordAuditFailure records an audit ledger checkpoint verification failure.
func (m *Measures) RecordAuditFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.AuditVerificationFailures++
}

// RecordCameraDegraded records a camera entering a named degraded health
// state. The set of degraded states is `camerahealth.Degraded`'s, not a second
// opinion spelled here: a stale camera is degraded for the brief, coverage and
// the capture plan, so it is degraded for this counter too.
func (m *Measures) RecordCameraDegraded(state string) {
	if !camerahealth.Degraded(state) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch state {
	case camerahealth.Silent:
		m.s.CamerasSilent++
	case camerahealth.Blind:
		m.s.CamerasBlind++
	case camerahealth.Stale:
		m.s.CamerasStale++
	}
}

// Snapshot returns a consistent copy of the counters. A copy rather than a
// read lock held open for the length of an HTTP response, so a slow client
// cannot stall the autonomy pass that is writing these numbers.
func (m *Measures) Snapshot() Sample {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.s
}
