// Package telemetry provides live KPIs for the autonomy layer.
package telemetry

import "time"

// MetricsCollector collects and aggregates telemetry metrics.
//
// It holds no goroutine and no timer of its own: the caller records events as
// they happen and reads the derived KPIs whenever a client asks. A collector
// that ran its own ticker would be a second clock in a product whose whole
// claim is that its numbers come from one pass over one set of facts.
type MetricsCollector struct {
	measures *Measures
	calc     *HAMCalculator
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		measures: newMeasures(),
		calc:     defaultHAMCalculator(),
	}
}

// GetLiveTelemetry returns current live telemetry.
func (mc *MetricsCollector) GetLiveTelemetry() LiveTelemetry {
	return mc.measures.Snapshot().ToLiveTelemetry(mc.calc)
}

// RecordDetection records a detection.
func (mc *MetricsCollector) RecordDetection() { mc.measures.RecordDetection() }

// RecordCorrelated records detections grouped into a correlated incident.
func (mc *MetricsCollector) RecordCorrelated(n int) { mc.measures.RecordCorrelated(n) }

// RecordIncident records a correlated incident.
func (mc *MetricsCollector) RecordIncident() { mc.measures.RecordIncident() }

// RecordEscalation records an escalation.
func (mc *MetricsCollector) RecordEscalation() { mc.measures.RecordEscalation() }

// RecordOverride records an operator override.
func (mc *MetricsCollector) RecordOverride() { mc.measures.RecordOverride() }

// RecordFalseEscalation records an escalation an operator overrode: the
// escalation happened, and the operator took it back.
func (mc *MetricsCollector) RecordFalseEscalation() { mc.measures.RecordFalseEscalation() }

// RecordTimeToDecision records the time from first detection to decision.
func (mc *MetricsCollector) RecordTimeToDecision(d time.Duration) {
	mc.measures.RecordTimeToDecision(d)
}

// RecordZoneContext records a detection matched to a zone.
func (mc *MetricsCollector) RecordZoneContext() { mc.measures.RecordZoneContext() }

// RecordNegativeForMissingAI records a violation of invariant 3.
func (mc *MetricsCollector) RecordNegativeForMissingAI() { mc.measures.RecordNegativeForMissingAI() }

// RecordAuditFailure records an audit ledger verification failure.
func (mc *MetricsCollector) RecordAuditFailure() { mc.measures.RecordAuditFailure() }

// RecordCameraDegraded records a camera entering a named degraded health
// state (silent or blind).
func (mc *MetricsCollector) RecordCameraDegraded(state string) {
	mc.measures.RecordCameraDegraded(state)
}
