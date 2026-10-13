package brief

import (
	"github.com/Sergey-Bar/vision/internal/dutylog"
)

// FactsFromEvents maps a duty log's events into brief facts.
//
// It is here, in the renderer's package, because the POC and the console both
// need it and each carried its own copy: the console filed coverage lines
// under actions while the POC excluded them, so one artifact rendered two
// different briefs and the console one under-reported the site's blind spots.
// One mapping, one answer, and the place the mapping lives is the package whose
// job is deciding what a brief contains.
//
// The kind set is the artifact's own vocabulary, and coverage lines are routed
// to `Facts.Coverage` (the last line per zone wins, because that is the state
// the artifact says the zone is in now) rather than to actions, which are
// decisions and overrides by definition. The degraded cameras are the zone's
// own event field, not text parsed back out of the reason.
func FactsFromEvents(events []dutylog.Event) Facts {
	var facts Facts
	var coverage map[string]ZoneCoverage
	for _, ev := range events {
		switch ev.Kind {
		case dutylog.KindDecision, dutylog.KindOverride, dutylog.KindTransition:
			facts.Actions = append(facts.Actions, Action{
				TS: ev.TS, Actor: ev.Actor, Subject: ev.Subject,
				Action: ev.Action, Reason: ev.Reason,
			})
		case dutylog.KindCoverage:
			if coverage == nil {
				coverage = map[string]ZoneCoverage{}
			}
			// The event carries the zone's DECLARED cameras and names the
			// degraded ones in its evidence refs, so the two lists stay
			// distinct. Assigning ev.CameraIDs to both, as the first version
			// did, made the console's "Declared cameras" column print the
			// degraded subset and rendered a recovery line with a blank
			// column — a brief that says a zone is watched by the cameras that
			// are dark.
			cov := ZoneCoverage{
				ZoneID:          ev.Subject,
				DeclaredCameras: ev.CameraIDs,
				DegradedCameras: ev.EvidenceRefs,
				UnderObserved:   ev.After == "under-observed",
			}
			coverage[ev.Subject] = cov
		case dutylog.KindNote:
			facts.Notes = append(facts.Notes, Note{
				TS: ev.TS, Actor: ev.Actor, Text: ev.Reason,
				// The camera field is filled only when the subject IS a
				// camera. A patrol note's subject is a route and an operator
				// note's subject is whatever they typed, so filling this from
				// the subject produced a camera named "route-01" and a camera
				// named after an incident id — a misattributed record served
				// at /api/briefs/latest and to the console.
				Camera: firstEventCamera(ev),
			})
		}
	}
	for _, cov := range coverage {
		facts.Coverage = append(facts.Coverage, cov)
	}
	return facts
}

// firstEventCamera names the camera a note took about, or returns the empty
// string when the note's subject is not a camera at all.
//
// The subject is NOT the camera: a patrol note's subject is a route and an
// operator note's subject is whatever they typed, which produced a camera
// named "route-01" and a camera named after an incident id. The honest
// derivation is from the event's own camera field, which the writers set when
// they have one; anything else is a subject, and the field stays empty rather
// than carrying a guess.
func firstEventCamera(ev dutylog.Event) string {
	if len(ev.CameraIDs) > 0 {
		return ev.CameraIDs[0]
	}
	return ""
}
