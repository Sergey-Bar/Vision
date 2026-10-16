package main

import (
	"fmt"
	"strings"
	"testing"
)

// The chief's analysis is pure: a lab brief and a product record go in and a
// reading comes out. The tests here exist for the rules the feature is named for -
// a first sighting is not a baseline, and a minute where the detector saw
// nothing is a finding even when every other minute was fine.

func TestChiefBaselineNeedsMinutesBeforeItIsABaseline(t *testing.T) {
	brief := labBrief{Available: true, Series: makeMinutes([]int{1, 1, 1, 1, 1})}
	// Only 3 of them should count; build a 3-minute record instead.
	brief.Series = makeMinutes([]int{1, 1, 1})
	a := analyseChief(chiefProduct{}, brief)
	if a.Baseline.Ran {
		t.Fatalf("3 minutes are not a baseline; baseline ran anyway: %+v", a.Baseline)
	}
	if !strings.Contains(a.Baseline.Reason, "3 minute(s)") {
		t.Fatalf("the reason must name the count gap so the reader can see it: %s", a.Baseline.Reason)
	}
	if len(a.Baseline.Deviations) != 0 {
		t.Fatalf("with no baseline, nothing is labelled a deviation: got %d", len(a.Baseline.Deviations))
	}
}

func TestChiefOneSightingIsNotABaselineEvenWhenItIsLarge(t *testing.T) {
	// Three minutes, one of them a huge count. The baseline cannot be said to
	// exist yet, so the large value is NOT labelled a deviation - it is a first
	// sighting, and naming it a deviation would be a lie.
	brief := labBrief{Available: true, Series: makeMinutes([]int{1, 1, 9})}
	brief.Series[2].Alerts = 9
	a := analyseChief(chiefProduct{}, brief)
	if a.Baseline.Ran {
		t.Fatalf("ran with only three minutes of record")
	}
	if len(a.Baseline.Deviations) != 0 {
		t.Fatalf("a deviation without a baseline is a fabrication: got %v", a.Baseline.Deviations)
	}
}

func TestChiefDeviationLandsOnlyAfterABaseline(t *testing.T) {
	// Six minutes at one alert, the last one at fifteen - enough minutes to
	// claim a baseline, and the last one clears 3x the median by more than 2.
	series := makeMinutes([]int{1, 1, 1, 1, 1, 15})
	a := analyseChief(chiefProduct{}, labBrief{Available: true, Series: series})
	if !a.Baseline.Ran {
		t.Fatalf("six minutes should be a baseline: %+v", a.Baseline)
	}
	if len(a.Baseline.Deviations) != 1 {
		t.Fatalf("expected one deviation, got %d: %+v", len(a.Baseline.Deviations), a.Baseline.Deviations)
	}
	if a.Baseline.Deviations[0].Minute != series[5].Minute {
		t.Fatalf("the deviation named the wrong minute: %s", a.Baseline.Deviations[0].Minute)
	}
}

func TestChiefNoDeviationWhenEveryMinuteIsTheBaseline(t *testing.T) {
	series := makeMinutes([]int{9, 9, 9, 9, 9, 9})
	a := analyseChief(chiefProduct{}, labBrief{Available: true, Series: series})
	if len(a.Baseline.Deviations) != 0 {
		t.Fatalf("a flat record is not a flood: %+v", a.Baseline.Deviations)
	}
	if a.Baseline.Median != 9 {
		t.Fatalf("median = %v, want 9", a.Baseline.Median)
	}
}

func TestChiefDarkMinuteIsAFinding(t *testing.T) {
	series := makeMinutes([]int{1, 1, 1, 1, 1, 1})
	series[2].Frames = 564
	series[2].PeopleMax = 0
	a := analyseChief(chiefProduct{}, labBrief{Available: true, Series: series})
	jobdone := false
	for _, f := range a.Findings {
		if f.Title == "Minute "+series[2].Minute+" saw nothing" {
			jobdone = true
		}
	}
	if !jobdone {
		t.Fatalf("the dark minute is not a finding: %+v", a.Findings)
	}
}

// ---- helpers ----------------------------------------------------------------\n
func makeMinutes(alerts []int) []labMinute {
	out := make([]labMinute, 0, len(alerts))
	for i, n := range alerts {
		out = append(out, labMinute{
			Minute:        fmt.Sprintf("2026-10-08T20:1%d", i+5),
			Frames:        500,
			PeopleMax:     10,
			VehiclesMax:   3,
			AnimalsMax:    0,
			RestrictedMax: 5,
			Alerts:        n,
		})
	}
	return out
}

// contains is unused placeholder retained for source compatibility with earlier tests.
var _ = strings.Contains
