package pipeline

import (
	"context"
	"time"

	"github.com/Sergey-Bar/vision/internal/anomaly"
	"github.com/Sergey-Bar/vision/internal/correlation"
	"github.com/Sergey-Bar/vision/internal/correlator"
	"github.com/Sergey-Bar/vision/internal/rules"
	"github.com/Sergey-Bar/vision/internal/threat"
)

// The threat score.
//
// It is the number the attention queue ranks by, and the chain beside it is
// what makes the number checkable: an operator looking at a queue asks "why is
// this first", and the answer is the chain rather than a number.
//
// # Why the score is computed here
//
// Here is where the trigger event and the corroborating events are both in
// hand, and the anomaly engine needs both. It is computed ONCE, when the
// incident is raised, and stored with it. A score computed on read would re-run
// the anomaly engine and the scorer over every open incident on every poll of
// the queue, which is the expensive path on the hot path.
//
// # What the anomaly is
//
// A PRESENCE: a class present in a zone, seen by a device, for longer than the
// bundle's window.
//
// It is not the POC's dwell, and it is not the POC's tracker either. The POC
// tracked an ENTITY by its box; the product's record deliberately does not keep
// a person's position in a frame -- a position is a privacy-bearing field and
// the adapter's redaction allow-list does not carry one -- so the product
// cannot track an entity and does not pretend to. What it can see is a class
// that keeps being reported in a zone, which is a weaker claim with a
// different name.
//
// The claim is weaker and the incident says so: the decision's own state says
// whether a second source agreed, and an UNVERIFIED incident is the product's
// word for "a rule matched and nothing corroborated it".
func (l *Loops) threatScore(
	_ context.Context,
	_ string,
	site string,
	corr correlator.Result,
	decision rules.Decision,
	at time.Time,
) (int, []threat.ReasonStep) {
	trigger := corr.Trigger
	if trigger.EventID == "" {
		return 0, nil
	}

	// The correlated incident the anomaly engine and the scorer read.
	//
	// It is built from what the product HAS: the device that saw it, the zone it
	// happened in, the events that corroborated it and the window they covered.
	// It has no entities, because the product's record has none to give.
	firstSeen := trigger.NormalizedAt
	lastSeen := at
	cameraIDs := []string{}
	detectionIDs := []string{}
	if trigger.DeviceID != "" {
		cameraIDs = append(cameraIDs, trigger.DeviceID)
	}
	detectionIDs = append(detectionIDs, trigger.EventID)
	for _, candidate := range corr.Available {
		if candidate.DeviceID != "" && !containsString(cameraIDs, candidate.DeviceID) {
			cameraIDs = append(cameraIDs, candidate.DeviceID)
		}
		detectionIDs = append(detectionIDs, candidate.EventID)
		if candidate.NormalizedAt.Before(firstSeen) {
			firstSeen = candidate.NormalizedAt
		}
		if candidate.NormalizedAt.After(lastSeen) {
			lastSeen = candidate.NormalizedAt
		}
	}
	if firstSeen.IsZero() {
		firstSeen = at
	}
	inc := &correlation.CorrelatedIncident{
		ID:           decision.RuleID + ":" + trigger.EventID,
		SiteID:       site,
		State:        "ACTIVE",
		FirstSeen:    firstSeen,
		LastSeen:     lastSeen,
		CameraIDs:    cameraIDs,
		DetectionIDs: detectionIDs,
		Provenance: correlation.ProvenanceExt{
			SiteID:   site,
			CameraID: trigger.DeviceID,
			ZoneID:   trigger.ZoneID,
		},
	}

	// The anomaly engine, and the site's own zones.
	//
	// The window is the one the correlator actually searched, so the autonomy
	// layer and the rule bundle agree about how long is long.
	window := corr.Window.Used
	if window <= 0 {
		window = 15 * time.Second
	}
	ruleSet := anomaly.PresenceRuleSet(window)
	anomalies := ruleSet.Evaluate(inc, l.Deps.SiteModel, at)
	if len(anomalies) == 0 {
		return 0, nil
	}
	inc.Anomalies = anomalies

	scorer := threat.NewScorer(threat.DefaultScorerConfig())
	score, chain := scorer.Score(inc)
	if score < 0 {
		score = 0
	}
	return score, chain.Steps
}

// containsString reports whether a list holds a value.
func containsString(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
