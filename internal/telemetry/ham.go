// Package telemetry provides live KPIs for the autonomy layer.
package telemetry

import (
	"time"
)

// HAMCalculator computes Human Attention Minutes per 1000 events.
//
// The estimate is deliberately stated rather than hidden: it counts the two
// moments an operator is actually interrupted — an escalation that needs them,
// and an override they chose to make — and it names the minutes each one costs.
// A different site can disagree with the constants; what it may not do is
// present the result as measured when it is an estimate.
type HAMCalculator struct {
	EscalationMinutes float64
	OverrideMinutes   float64
}

// defaultHAMCalculator returns a calculator with conservative defaults.
func defaultHAMCalculator() *HAMCalculator {
	return &HAMCalculator{
		EscalationMinutes: 5.0,
		OverrideMinutes:   10.0,
	}
}

// CalculateHAM computes HAM/1000 from a sample.
//
// (escalations × 5min + overrides × 10min) / (total events / 1000)
func (h *HAMCalculator) CalculateHAM(m Sample) float64 {
	if m.TotalDetections == 0 {
		return 0
	}
	minutes := float64(m.TotalEscalations)*h.EscalationMinutes +
		float64(m.TotalOverrides)*h.OverrideMinutes
	return minutes / (float64(m.TotalDetections) / 1000.0)
}

// TimeToDecisionSeconds is the mean time from an incident's first detection to
// the playbook's decision. Zero when nothing has been decided yet.
func (m Sample) TimeToDecisionSeconds() float64 {
	if m.DecisionCount == 0 {
		return 0
	}
	return m.TotalTimeToDecision.Seconds() / float64(m.DecisionCount)
}

// CorrelationHitRatePct is the share of detections that landed inside a
// correlated incident rather than being dropped as an unpaired sighting.
func (m Sample) CorrelationHitRatePct() float64 {
	if m.TotalDetections == 0 {
		return 100.0
	}
	return float64(m.DetectionsInCorrelated) / float64(m.TotalDetections) * 100.0
}

// ZoneContextPct is the share of detections that were matched to a zone.
func (m Sample) ZoneContextPct() float64 {
	if m.TotalDetections == 0 {
		return 100.0
	}
	return float64(m.DetectionsWithZoneContext) / float64(m.TotalDetections) * 100.0
}

// OperatorOverrideRatePct is the share of escalations an operator chose to
// override. It is the one number that says whether autonomy matched judgement.
func (m Sample) OperatorOverrideRatePct() float64 {
	if m.TotalEscalations == 0 {
		return 0.0
	}
	return float64(m.TotalOverrides) / float64(m.TotalEscalations) * 100.0
}

// AbsenceIntegrityPct is 100 while no negative observation was ever created
// from a missing AI observation, and 0 the moment one was.
func (m Sample) AbsenceIntegrityPct() float64 {
	if m.NegativeCreatedForMissingAI > 0 {
		return 0.0
	}
	return 100.0
}

// AuditIntegrityPct is 100 while no checkpoint verification ever failed, and 0
// the moment one did.
func (m Sample) AuditIntegrityPct() float64 {
	if m.AuditVerificationFailures > 0 {
		return 0.0
	}
	return 100.0
}

// LiveTelemetry is the shape GET /api/telemetry/live returns.
type LiveTelemetry struct {
	HAMPer1000              float64 `json:"ham_per_1000"`
	TimeToDecisionSec       float64 `json:"time_to_decision_sec"`
	CorrelationHitRatePct   float64 `json:"correlation_hit_rate_pct"`
	ZoneContextPct          float64 `json:"zone_context_pct"`
	OperatorOverrideRatePct float64 `json:"operator_override_rate_pct"`
	FalseEscalationRatePct  float64 `json:"false_escalation_rate_pct"`
	AbsenceIntegrityPct     float64 `json:"absence_integrity_pct"`
	AuditIntegrityPct       float64 `json:"audit_integrity_pct"`

	TotalDetections  int64 `json:"total_detections"`
	TotalIncidents   int64 `json:"total_incidents"`
	TotalEscalations int64 `json:"total_escalations"`
	TotalOverrides   int64 `json:"total_overrides"`

	// Camera honesty: how many cameras have gone silent, blind or stale since
	// the window opened. Additive to the wire shape; no existing field changes.
	CamerasSilent int64 `json:"cameras_silent"`
	CamerasBlind  int64 `json:"cameras_blind"`
	CamerasStale  int64 `json:"cameras_stale"`

	WindowStart       string  `json:"window_start"`
	WindowDurationSec float64 `json:"window_duration_sec"`
}

// ToLiveTelemetry converts a sample into the wire shape.
func (m Sample) ToLiveTelemetry(calc *HAMCalculator) LiveTelemetry {
	now := time.Now()
	return LiveTelemetry{
		HAMPer1000:              calc.CalculateHAM(m),
		TimeToDecisionSec:       m.TimeToDecisionSeconds(),
		CorrelationHitRatePct:   m.CorrelationHitRatePct(),
		ZoneContextPct:          m.ZoneContextPct(),
		OperatorOverrideRatePct: m.OperatorOverrideRatePct(),
		FalseEscalationRatePct:  m.FalseEscalationRatePct(),
		AbsenceIntegrityPct:     m.AbsenceIntegrityPct(),
		AuditIntegrityPct:       m.AuditIntegrityPct(),
		TotalDetections:         m.TotalDetections,
		TotalIncidents:          m.TotalIncidents,
		TotalEscalations:        m.TotalEscalations,
		TotalOverrides:          m.TotalOverrides,
		CamerasSilent:           m.CamerasSilent,
		CamerasBlind:            m.CamerasBlind,
		CamerasStale:            m.CamerasStale,
		WindowStart:             m.WindowStart.Format(time.RFC3339),
		WindowDurationSec:       now.Sub(m.WindowStart).Seconds(),
	}
}
