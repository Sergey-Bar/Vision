package main

// The calibration report, and the three things it has to get right.
//
// # The breakdowns carry their own N
//
// A number for the whole install is a number for nothing, and a number for one
// site that silently covers every case is worse. Each breakdown is scored over
// its own subset and carries the count of that subset.
//
// # The proportions carry an interval and the rest do not
//
// F1, the calibration error and HAM-per-1000 are not proportions, and a
// Wilson interval on something that is not a proportion is a fabricated number.
// So the four proportions carry one and the other three carry their N and
// nothing else.
//
// # The report says what it cannot tell
//
// The generator SKIPS the cases it cannot read, and the skip count is reported
// rather than folded into a denominator: a corpus that silently dropped the
// cases it could not read would measure the easy half.

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/calibration"
)

// corpus is a small labelled history: two sites, two rule classes, and a
// couple of cases the engine would escalate.
func corpus() calibration.Corpus {
	cases := []calibration.Case{
		{CaseID: "c-1", Site: "north-gate", Rule: "person", Label: calibration.LabelTruePositive,
			Features: calibration.Features{AnomalyType: "presence", ZoneType: "restricted", Severity: "medium", Devices: 2, PresentSeconds: 30, ThresholdSeconds: 10}},
		{CaseID: "c-2", Site: "north-gate", Rule: "person", Label: calibration.LabelFalsePositive,
			Features: calibration.Features{AnomalyType: "presence", ZoneType: "monitor", Severity: "low", Devices: 1, PresentSeconds: 5, ThresholdSeconds: 10}},
		{CaseID: "c-3", Site: "west-warehouse", Rule: "vehicle", Label: calibration.LabelTruePositive,
			Features: calibration.Features{AnomalyType: "presence", ZoneType: "choke", Severity: "high", Devices: 3, PresentSeconds: 60, ThresholdSeconds: 10}},
	}
	return calibration.ComputePriors(calibration.Corpus{
		ContractVersion: calibration.CorpusVersion,
		Provenance:      "test corpus",
		Cases:           cases,
	})
}

// TestTheCalibrationReportBreaksDownBySiteAndByClass is the shape.
func TestTheCalibrationReportBreaksDownBySiteAndByClass(t *testing.T) {
	report := buildCalibrationReport("t-1", time.Time{}, time.Time{}, corpus(), 0, "", "")

	if report.Whole.N != 3 {
		t.Errorf("the whole-install breakdown is over %d cases, want 3", report.Whole.N)
	}
	if len(report.BySite) != 2 {
		t.Fatalf("the site breakdown holds %d entries, want 2: %+v", len(report.BySite), report.BySite)
	}
	if report.BySite[0].Scope.Site != "north-gate" {
		t.Errorf("the first site is %q, want north-gate", report.BySite[0].Scope.Site)
	}
	if report.BySite[0].N != 2 {
		t.Errorf("the north-gate breakdown is over %d cases, want 2. A breakdown that does not "+
			"carry its own N is a number with an undisclosed denominator", report.BySite[0].N)
	}
	if report.BySite[0].Scope.Class != "" {
		t.Errorf("a SITE breakdown carries the class %q", report.BySite[0].Scope.Class)
	}

	if len(report.ByClass) != 2 {
		t.Fatalf("the class breakdown holds %d entries, want 2: %+v", len(report.ByClass), report.ByClass)
	}
	if report.ByClass[0].Scope.Class != "person" {
		t.Errorf("the first class is %q, want person", report.ByClass[0].Scope.Class)
	}
	if report.ByClass[0].Scope.Site != "" {
		t.Errorf("a CLASS breakdown carries the site %q", report.ByClass[0].Scope.Site)
	}
}

// TestTheProportionsCarryAnIntervalAndTheRestDoNot is the honesty of the
// numbers.
//
// A Wilson interval on F1, on the calibration error or on HAM-per-1000 would
// be a fabricated number: none of them is a proportion.
func TestTheProportionsCarryAnIntervalAndTheRestDoNot(t *testing.T) {
	report := buildCalibrationReport("t-1", time.Time{}, time.Time{}, corpus(), 0, "", "")
	whole := report.Whole

	for name, r := range map[string]*rateWithInterval{
		"escalation precision": whole.EscalationPrecision,
		"escalation recall":    whole.EscalationRecall,
		"attention precision":  whole.AttentionPrecision,
		"attention recall":     whole.AttentionRecall,
	} {
		if r == nil {
			t.Errorf("the %s is missing", name)
			continue
		}
		if r.CI == nil {
			t.Errorf("the %s carries no interval. A rate with no interval is a number with an "+
				"undisclosed precision", name)
		}
	}
	for name, r := range map[string]*rateWithInterval{
		"f1":                whole.F1,
		"calibration error": whole.CalibrationError,
		"ham per 1000":      whole.HAMPer1000,
	} {
		if r == nil {
			t.Errorf("the %s is missing", name)
			continue
		}
		if r.CI != nil {
			t.Errorf("the %s carries a Wilson interval, and it is not a proportion. A Wilson "+
				"interval on a quantity that is not a proportion is a fabricated number", name)
		}
	}
}

// TestTheCalibrationReportSaysWhatItCannotTell is the honesty of the corpus.
//
// The generator skips the cases it cannot read, and the count is reported.
func TestTheCalibrationReportSaysWhatItCannotTell(t *testing.T) {
	report := buildCalibrationReport("t-1", time.Time{}, time.Time{}, corpus(), 4, "", "")
	if report.Skipped != 4 {
		t.Errorf("the report says %d case(s) were skipped, want 4. A corpus that silently "+
			"dropped the cases it could not read would measure the easy half", report.Skipped)
	}
	if report.Cases != 3 {
		t.Errorf("the report says %d case(s), want 3", report.Cases)
	}
	if report.Note == "" {
		t.Error("the report carries no note, and a reader cannot tell what the corpus is")
	}
	if report.ReportVersion == "" {
		t.Error("the report carries no version, and a reader cannot tell what it is")
	}
}
