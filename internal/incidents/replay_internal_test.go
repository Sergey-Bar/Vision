package incidents

import (
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// A drift exclusion is a fact about a device, not a number to be compared
// against. The per-event flag used to be `event.ClockDriftMS >= inSevereBand(d)`,
// where the band was the measured drift of the *first* recorded exclusion
// applied to *every* event in the incident - so one incident with one
// 200ms-excluded camera marked every unrelated camera with 200ms of drift as
// excluded too. The consequence is not cosmetic: DriftedExcluded feeds the rule
// engine, so a fabricated exclusion is a fabricated decision, and invariant 2
// says a decision replays from stored facts rather than re-inferring.
func TestSignalsDoNotFabricateDriftExclusions(t *testing.T) {
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)
	d := Detail{
		Summary: Summary{FirstEvent: at},
		Events: []EventView{
			{EventID: "evt-1", DeviceID: "cam-drifted", NormalizedAt: at, ClockDriftMS: 9000, ClockSource: "host"},
			{EventID: "evt-2", DeviceID: "cam-clean", NormalizedAt: at, ClockDriftMS: 5, ClockSource: "ntp"},
			// Same drift magnitude, different device. The old threshold said both
			// were excluded.
			{EventID: "evt-3", DeviceID: "cam-also-clean", NormalizedAt: at, ClockDriftMS: 9000, ClockSource: "host"},
		},
		Verification: VerificationView{
			DriftExclusions: []rules.Exclusion{{DeviceID: "cam-drifted", DriftMS: 200, Reason: "clock drift"}},
		},
	}

	trigger, others := signalsFrom(d)
	if !trigger.DriftedExcluded {
		t.Error("the event on the excluded device was not marked excluded")
	}
	if len(others) != 2 {
		t.Fatalf("expected two other signals, got %d", len(others))
	}
	for _, sig := range others {
		if sig.DriftedExcluded {
			t.Errorf("event %s on device %q was marked drift-excluded. Only cam-drifted was "+
				"recorded as excluded; the old code compared every event's drift against the "+
				"first exclusion's measured drift, so an unrelated camera with the same drift "+
				"was silently excluded too", sig.EventID, sig.DeviceID)
		}
	}
}

// With no exclusions recorded, the field is false because there is nothing to
// exclude - not because a sentinel threshold happened not to be crossed. The old
// code returned 1<<30, which makes the absence of data indistinguishable from a
// measurement, and it also silently invented a "severe band" out of nothing.
func TestNoRecordedExclusionsMeansNoExclusions(t *testing.T) {
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)
	d := Detail{
		Summary: Summary{FirstEvent: at},
		Events: []EventView{
			{EventID: "evt-1", DeviceID: "cam-1", NormalizedAt: at, ClockDriftMS: 900000, ClockSource: "host"},
		},
	}
	trigger, _ := signalsFrom(d)
	if trigger.DriftedExcluded {
		t.Error("an event was marked drift-excluded with no exclusion recorded. A severe band " +
			"is not a stored fact, and inventing one is the same defect as fabricating the " +
			"exclusion")
	}
}

// One function decides which observation a replay cites, because two did and they
// disagreed: signalsFrom took the oldest, pickObservation took supporting[0] and
// otherwise the newest. So the signals the engine evaluated and the observation
// the answer named could be different records, and which you got depended on
// which field you read.
func TestTheSameObservationIsCitedEverywhere(t *testing.T) {
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)
	obs := []ObservationView{
		{ObservationID: "obs-old", CreatedAt: at, StructuredOutput: map[string]any{"restrictedZoneBreach": true}},
		{ObservationID: "obs-new", CreatedAt: at.Add(time.Hour), StructuredOutput: map[string]any{"restrictedZoneBreach": false}},
	}

	// Ordered by (created_at, observation_id), which is what the query now asks
	// for — without the tiebreak "oldest" was whatever Postgres returned.
	d := Detail{
		Summary:      Summary{FirstEvent: at},
		Events:       []EventView{{EventID: "evt-1", NormalizedAt: at}},
		Observations: obs,
		Verification: VerificationView{SupportingObservations: []string{"obs-old"}},
	}

	if got := pickObservation(obs, d.Verification.SupportingObservations); got == nil ||
		got.ObservationID != "obs-old" {
		t.Fatalf("pickObservation chose %v, want obs-old", got)
	}
	trigger, _ := signalsFrom(d)
	if trigger.AIObservation == nil {
		t.Fatal("the trigger signal carries no observation")
	}
	if trigger.AIObservation.ObservationID != "obs-old" {
		t.Errorf("the trigger signal evaluates observation %q while the explanation cites %q. "+
			"One function has to decide", trigger.AIObservation.ObservationID, "obs-old")
	}
}

// With nothing recorded, the oldest is cited rather than the newest. The newest
// is the most recent re-evaluation, and attributing a decision to a re-evaluation
// that did not make it is a lie an operator would act on.
func TestObservationFallsBackToTheOldest(t *testing.T) {
	at := time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC)
	obs := []ObservationView{
		{ObservationID: "obs-old", CreatedAt: at},
		{ObservationID: "obs-new", CreatedAt: at.Add(time.Hour)},
	}
	got := pickObservation(obs, nil)
	if got == nil || got.ObservationID != "obs-old" {
		t.Errorf("pickObservation chose %v, want the oldest observation", got)
	}
}
