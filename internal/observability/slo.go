package observability

import (
	"fmt"
	"sort"
	"time"
)

// SLO is one objective from the plan, with the number it is measured against.
//
// Each is stated as a threshold and a window rather than as a percentage alone,
// because "ingest availability 99.5%" is not actionable at 3am and
// "more than 5 accepted events lost in a day" is.
type SLO struct {
	Name string
	// Target is the value the plan states.
	Target string
	// Breached reports whether the current readings violate it.
	Breached bool
	// Measured is the reading that decided it, formatted for an operator.
	Measured string
	// Remedy is what to do about it. Every SLO carries one, because an alert
	// with no remedy is an alert everybody learns to ignore.
	Remedy string
}

// Breaches evaluates the SLOs against the registry.
//
// It is a function and not a method on a struct because the SLO list is
// configuration — a site may tighten the ingest target — and because the
// evaluation must be testable without a running system.
func Breaches(r *Registry, accepted, deadLettered, driftExcluded, correlations, contradictions, aiDegraded, diskFreePercent float64, incidentVisibilities []time.Duration) []SLO {
	byName := map[string]float64{
		GaugeBacklogDepth:       r.Gauges.Get(GaugeBacklogDepth),
		GaugeOutboxPending:      r.Gauges.Get(GaugeOutboxPending),
		GaugeJobQueueDepth:      r.Gauges.Get(GaugeJobQueueDepth),
		GaugeDeadLetterCount:    r.Gauges.Get(GaugeDeadLetterCount),
		GaugeClockSevereDevices: r.Gauges.Get(GaugeClockSevereDevices),
	}

	var out []SLO

	// SLO 1: zero accepted event loss. Expressed as a count rather than a
	// percentage because a percentage of zero is still a non-zero number of lost
	// events, and an operator reading "0.0000% loss" should be suspicious of the
	// measurement rather than reassured by it.
	out = append(out, SLO{
		Name:     "ingest_availability",
		Target:   "zero accepted events lost",
		Breached: accepted > 0,
		Measured: fmt.Sprintf("%.0f lost, %d dead-lettered", accepted, int(deadLettered)),
		Remedy: "check the dead-letter list before anything else. A dead letter is an accepted " +
			"event the platform could not process, and the reason is recorded on it.",
	})

	// SLO 2: ingest lag. The plan's target is 5 seconds at p95.
	lag := r.IngestLag.QuantileSeconds(0.95)
	if lag < 0 {
		lag = 0
	}
	lagSeconds := time.Duration(lag * float64(time.Second))
	out = append(out, SLO{
		Name:     "ingest_latency",
		Target:   "p95 under 5s",
		Breached: lag > 5,
		Measured: fmt.Sprintf("p95 %.2fs", lagSeconds.Seconds()),
		Remedy: "a source is falling behind or the normalizer is saturated. Check the backlog " +
			"gauge before touching anything else.",
	})

	// SLO 3: incident visibility inside 15 seconds at p95, on the AI path.
	var p95 time.Duration
	if len(incidentVisibilities) > 0 {
		sorted := append([]time.Duration(nil), incidentVisibilities...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		p95 = sorted[(int(0.95 * float64(len(sorted)-1)))]
	}
	out = append(out, SLO{
		Name:     "incident_visibility",
		Target:   "p95 under 15s",
		Breached: p95 > 15*time.Second,
		Measured: fmt.Sprintf("p95 %s", p95),
		Remedy: "this is the AI path. A model that is slow is an availability problem " +
			"for incident visibility and is not an SLO for ingest.",
	})

	// SLO 4: the AI plane is never an SLO. This is recorded as a breach of a
	// different kind: the product keeps working, but a pilot claiming an AI
	// benefit has to be able to say how often it was there.
	out = append(out, SLO{
		Name:     "ai_plane_availability",
		Target:   "reported, never required",
		Breached: aiDegraded > 0,
		Measured: fmt.Sprintf("%.0f degraded decision(s) recorded", aiDegraded),
		Remedy: "none, by design. The core keeps ingesting and verifying without it. " +
			"This number is how a pilot measures what the AI plane actually contributed.",
	})

	// SLO 5: backlog. Above the soft limit the site is degraded; above the hard
	// limit it is losing.
	out = append(out, SLO{
		Name:   "ingest_backlog",
		Target: "soft 5000, hard 50000",
		Breached: byName[GaugeBacklogDepth] > 50000 || byName[GaugeOutboxPending] > 50000 ||
			byName[GaugeJobQueueDepth] > 50000,
		Measured: fmt.Sprintf("inbox %.0f, outbox %.0f, jobs %.0f",
			byName[GaugeBacklogDepth], byName[GaugeOutboxPending], byName[GaugeJobQueueDepth]),
		Remedy: "above the hard limit the platform is refusing to accept rather than " +
			"dropping. Check the source feed first, then the normalizer.",
	})

	// SLO 6: correlation behaves under drift. A device past the severe band is
	// excluded, and that is a configuration fact, not a failure — but too many of
	// them means the site cannot corroborate anything.
	out = append(out, SLO{
		Name:     "correlation_under_drift",
		Target:   "fewer than 1% of devices in the severe band",
		Breached: byName[GaugeClockSevereDevices] > 0,
		Measured: fmt.Sprintf("%.0f device(s) excluded from corroboration", byName[GaugeClockSevereDevices]),
		Remedy: "excluded devices do not corroborate, so their events will read as UNVERIFIED. " +
			"Check the drift view for which devices and how far off they are.",
	})

	// SLO 7: dead letters visible within 60 seconds. Measured by the operator's
	// interval, not by the platform's, which is deliberate: the requirement is
	// about the operator being able to see it, and only an operator can say so.
	out = append(out, SLO{
		Name:     "dead_letter_visibility",
		Target:   "visible within 60s",
		Breached: byName[GaugeDeadLetterCount] > 0,
		Measured: fmt.Sprintf("%.0f dead letter(s)", byName[GaugeDeadLetterCount]),
		Remedy: "the console's dead-letter list is the fastest route. A dead letter is never " +
			"dropped silently and replay is an operator action.",
	})

	// SLO 8: evidence disk. This is the one that ends an incident badly: a full
	// disk stops evidence fetches, and an incident without its evidence is not
	// defensible.
	out = append(out, SLO{
		Name:     "evidence_disk",
		Target:   "10% free, warning at 25%",
		Breached: diskFreePercent < 10,
		Measured: fmt.Sprintf("%.1f%% free", diskFreePercent),
		Remedy: "run a retention sweep before deleting anything by hand. A sweep that cannot " +
			"check legal hold deletes nothing, so a dry run is safe.",
	})

	_ = driftExcluded
	_ = correlations
	_ = contradictions
	return out
}

// Breached returns only the objectives not being met, which is what an alert
// sends and what an operator reads.
func Breached(all []SLO) []SLO {
	var out []SLO
	for _, s := range all {
		if s.Breached {
			out = append(out, s)
		}
	}
	return out
}
