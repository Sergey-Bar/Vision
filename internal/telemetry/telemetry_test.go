package telemetry

import (
	"testing"
	"time"
)

func TestHAMCalculator(t *testing.T) {
	calc := defaultHAMCalculator()

	// 10 escalations and 5 overrides per 1000 detections is
	// (10×5 + 5×10) / 1 = 100 minutes of attention per thousand events.
	if got := calc.CalculateHAM(Sample{TotalDetections: 1000, TotalEscalations: 10, TotalOverrides: 5}); got != 100.0 {
		t.Errorf("HAM = %v, want 100.0", got)
	}

	// The same interruptions spread over twice the events is half the cost.
	if got := calc.CalculateHAM(Sample{TotalDetections: 2000, TotalEscalations: 10, TotalOverrides: 5}); got != 50.0 {
		t.Errorf("HAM = %v, want 50.0", got)
	}

	// Nothing measured, nothing claimed.
	if got := calc.CalculateHAM(Sample{}); got != 0.0 {
		t.Errorf("HAM with no detections = %v, want 0.0", got)
	}

	// A site that escalates little and is overridden less has a small number,
	// which is the point of measuring it.
	if got := calc.CalculateHAM(Sample{TotalDetections: 10000, TotalEscalations: 2}); got != 1.0 {
		t.Errorf("HAM = %v, want 1.0", got)
	}
}

func TestMeasuresDerivedKPIs(t *testing.T) {
	m := Sample{
		TotalDetections:           200,
		DetectionsInCorrelated:    160,
		DetectionsWithZoneContext: 190,
		TotalEscalations:          10,
		TotalOverrides:            5,
		FalseEscalations:          2,
		TotalTimeToDecision:       300 * time.Second,
		DecisionCount:             20,
	}

	if got := m.TimeToDecisionSeconds(); got != 15.0 {
		t.Errorf("TimeToDecision = %v, want 15.0", got)
	}
	if got := m.CorrelationHitRatePct(); got != 80.0 {
		t.Errorf("CorrelationHitRate = %v, want 80.0", got)
	}
	if got := m.ZoneContextPct(); got != 95.0 {
		t.Errorf("ZoneContext = %v, want 95.0", got)
	}
	if got := m.OperatorOverrideRatePct(); got != 50.0 {
		t.Errorf("OperatorOverrideRate = %v, want 50.0", got)
	}
	if got := m.FalseEscalationRatePct(); got != 20.0 {
		t.Errorf("FalseEscalationRate = %v, want 20.0 (2 of 10 escalations)", got)
	}
	if got := m.AbsenceIntegrityPct(); got != 100.0 {
		t.Errorf("AbsenceIntegrity = %v, want 100.0", got)
	}
	if got := m.AuditIntegrityPct(); got != 100.0 {
		t.Errorf("AuditIntegrity = %v, want 100.0", got)
	}
}

// TestFalseEscalationRateIsItsOwnMeasure: both rates divide by escalations,
// and the difference is the numerator — every override versus overrides of
// escalations. The 91% who priced false-positives asked for the second
// number, and the first cannot stand in for it.
func TestFalseEscalationRateIsItsOwnMeasure(t *testing.T) {
	m := Sample{}
	if got := m.FalseEscalationRatePct(); got != 0 {
		t.Errorf("FalseEscalationRate with no escalations = %v, want 0", got)
	}
	m = Sample{TotalEscalations: 4, FalseEscalations: 2, TotalIncidents: 40, TotalOverrides: 3}
	if got := m.FalseEscalationRatePct(); got != 50.0 {
		t.Errorf("FalseEscalationRate = %v, want 50.0 (2 of 4 escalations)", got)
	}
	if got := m.OperatorOverrideRatePct(); got != 75.0 {
		t.Errorf("OperatorOverrideRate = %v, want 75.0 (3 of 4 escalations overridden)", got)
	}
}

// TestZeroDivisionsAreSafeAndHonest is about what a fresh deployment reads: a
// system that has seen nothing reports no violations and no measured rate,
// rather than a 100 that looks like a result.
func TestZeroDivisionsAreSafeAndHonest(t *testing.T) {
	m := Sample{}
	if got := m.TimeToDecisionSeconds(); got != 0 {
		t.Errorf("TimeToDecision = %v, want 0", got)
	}
	if got := m.OperatorOverrideRatePct(); got != 0 {
		t.Errorf("OperatorOverrideRate = %v, want 0 when nothing escalated", got)
	}
	// Absence and audit integrity are not rates: they are 100 until the
	// invariant breaks, and that is the claim being made.
	if got := m.AbsenceIntegrityPct(); got != 100 {
		t.Errorf("AbsenceIntegrity = %v, want 100", got)
	}
	if got := m.AuditIntegrityPct(); got != 100 {
		t.Errorf("AuditIntegrity = %v, want 100", got)
	}

	live := m.ToLiveTelemetry(defaultHAMCalculator())
	if live.WindowStart == "" {
		t.Error("live telemetry has no window start; a reader cannot tell how fresh it is")
	}
	if live.HAMPer1000 != 0 {
		t.Errorf("HAM = %v, want 0", live.HAMPer1000)
	}
}

// TestIntegrityKPIToZeroOnInvariantBreak is the other half of the invariant
// instrumentation: once a negative observation is created from a missing AI
// observation, the number moves away from 100 instead of the violation being
// invisible.
func TestIntegrityKPIToZeroOnInvariantBreak(t *testing.T) {
	m := Sample{TotalDetections: 10}
	m.NegativeCreatedForMissingAI = 1
	if got := m.AbsenceIntegrityPct(); got != 0 {
		t.Errorf("AbsenceIntegrity = %v, want 0 once a negative was created for a missing AI", got)
	}

	m2 := Sample{TotalDetections: 10}
	m2.AuditVerificationFailures = 1
	if got := m2.AuditIntegrityPct(); got != 0 {
		t.Errorf("AuditIntegrity = %v, want 0 once a checkpoint failed", got)
	}
}

func TestMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector()

	mc.RecordDetection()
	mc.RecordDetection()
	mc.RecordCorrelated(5)
	mc.RecordIncident()
	mc.RecordEscalation()
	mc.RecordOverride()
	mc.RecordTimeToDecision(10 * time.Second)
	mc.RecordZoneContext()

	live := mc.GetLiveTelemetry()
	if live.TotalDetections != 2 {
		t.Errorf("TotalDetections = %d, want 2", live.TotalDetections)
	}
	if live.TotalIncidents != 1 {
		t.Errorf("TotalIncidents = %d, want 1", live.TotalIncidents)
	}
	if live.TotalEscalations != 1 {
		t.Errorf("TotalEscalations = %d, want 1", live.TotalEscalations)
	}
	if live.TotalOverrides != 1 {
		t.Errorf("TotalOverrides = %d, want 1", live.TotalOverrides)
	}
	if live.TimeToDecisionSec != 10.0 {
		t.Errorf("TimeToDecision = %v, want 10.0", live.TimeToDecisionSec)
	}
	// Two detections, five of them correlated, one matched to a zone: these
	// read 250% and 50%, and that is the point — the rates are computed over
	// the real denominator, not clamped to look tidy.
	if live.CorrelationHitRatePct != 250.0 {
		t.Errorf("CorrelationHitRate = %v, want 250.0", live.CorrelationHitRatePct)
	}
	if live.AbsenceIntegrityPct != 100 {
		t.Errorf("AbsenceIntegrity = %v, want 100", live.AbsenceIntegrityPct)
	}
}

func TestMeasuresSnapshotIsIndependent(t *testing.T) {
	m := newMeasures()
	m.RecordDetection()

	snap := m.Snapshot()
	if snap.TotalDetections != 1 {
		t.Fatalf("snapshot TotalDetections = %d, want 1", snap.TotalDetections)
	}

	// Writing after the snapshot must not change the snapshot: a reader that
	// held a live reference would see numbers move under it.
	m.RecordDetection()
	if snap.TotalDetections != 1 {
		t.Errorf("snapshot changed after a later write: %d", snap.TotalDetections)
	}
	if m.Snapshot().TotalDetections != 2 {
		t.Error("the snapshot taken after the write should see it")
	}
}

func TestMeasuresSnapshotIsRaceFree(t *testing.T) {
	// The autonomy pass writes while a dashboard reads. run with -race.
	m := newMeasures()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				m.RecordDetection()
				m.Snapshot()
			}
		}
	}()
	for i := 0; i < 100; i++ {
		m.Snapshot().ToLiveTelemetry(defaultHAMCalculator())
	}
	close(stop)
	<-done
}
