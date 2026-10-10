package correlation

import (
	"fmt"
	"sort"
	"strings"
)

// Second sight: does a second camera corroborate this incident?
//
// The July 2026 controls survey's clearest number is 65.6% of enterprise
// buyers naming "corroborate an alert with a second camera's sighting" as a
// required feature. This is that feature, and it is separation of duty at the
// data plane: one camera seeing something is one witness, and a system that
// treats one witness as proof has no idea how weak its own evidence is.
//
// The rule that governs the implementation: it is DECLARATION-BASED, because
// declarations are the only coverage the system has. The site model's
// Camera.ZoneIDs is the only declared camera→zone link anywhere in this
// repository — there is no FOV geometry, and a second-sight answer derived
// from invented geometry would be a guess dressed as corroboration. So the
// verdicts are:
//
//	corroborated    two or more cameras are IN the incident: two witnesses.
//	single_sighting one camera saw it, and the other cameras declared to cover
//	                that zone are demonstrably watching (they have tracks) and
//	                did not add a sighting. One witness.
//	cannot_tell     the declarations cannot answer: no other camera is
//	                declared for the zone, or the declared cameras have no
//	                tracks, so their silence is an absence of evidence rather
//	                than evidence of absence. Never a negative.
//
// "cannot_tell" is the state this repository's rule about missing observations
// demands: a camera that is not producing tracks has not said "nothing is
// there", and this function must not say it for them.
const (
	SightCorroborated = "corroborated"
	SightSingle       = "single_sighting"
	SightCannotTell   = "cannot_tell"
)

// Sight is the second-sight verdict. Reason is written in the words the
// reason chain uses, because this verdict is what an operator reads when the
// autonomy layer holds an escalation.
type Sight struct {
	Verdict  string   `json:"verdict"`
	Cameras  []string `json:"cameras"`            // the witnesses the verdict rests on
	Declared []string `json:"declared,omitempty"` // other cameras declared for the zone
	Reason   string   `json:"reason"`
}

// Corroborated reports whether a second camera's sighting backs the incident.
func (s Sight) Corroborated() bool { return s.Verdict == SightCorroborated }

// SecondSight judges an incident against the cameras declared to cover the
// zone it happened in, using the tracks currently alive to tell a watching
// camera from a silent one.
//
// zoneCameras is the site model's declaration map, zone id → the camera ids
// declared to cover it. A nil map is "no declaration loaded", which is a
// cannot_tell and never a negative. tracks is every track the fleet currently
// holds; the only thing taken from it is which cameras are alive.
func SecondSight(inc *CorrelatedIncident, tracks []*Track, zoneCameras map[string][]string) Sight {
	if inc == nil {
		return Sight{Verdict: SightCannotTell, Reason: "cannot tell: there is no incident to corroborate"}
	}

	// The witnesses: the distinct cameras the incident's own detections cite.
	witnesses := distinct(inc.CameraIDs)
	if len(witnesses) >= 2 {
		return Sight{
			Verdict:  SightCorroborated,
			Cameras:  witnesses,
			Declared: nil,
			Reason: fmt.Sprintf("corroborated by %d cameras: %s",
				len(witnesses), strings.Join(witnesses, ", ")),
		}
	}
	sighting := "(none)"
	if len(witnesses) == 1 {
		sighting = witnesses[0]
	}

	// Which zone? The anomaly rules attribute every zone membership, so an
	// incident with no zone attribution cannot be checked against a
	// declaration — and saying "corroborated" for it would be the guess.
	zone := ""
	for _, a := range inc.Anomalies {
		if a.ZoneID != "" {
			zone = a.ZoneID
			break
		}
	}
	if zone == "" {
		return Sight{
			Verdict: SightCannotTell,
			Cameras: witnesses,
			Reason:  fmt.Sprintf("cannot tell: incident %s has no zone attribution to check a declaration against", inc.ID),
		}
	}
	if len(zoneCameras) == 0 {
		return Sight{
			Verdict: SightCannotTell,
			Cameras: witnesses,
			Reason:  fmt.Sprintf("cannot tell: no camera->zone declaration is loaded, so %s cannot be checked for a second sighting", zone),
		}
	}

	declared := distinct(zoneCameras[zone])
	others := make([]string, 0, len(declared))
	for _, cam := range declared {
		if cam == sighting {
			continue
		}
		others = append(others, cam)
	}
	if len(others) == 0 {
		return Sight{
			Verdict: SightCannotTell,
			Cameras: witnesses,
			Reason: fmt.Sprintf("cannot tell: %s is covered by one declared camera (%s); a second sighting needs a second declaration",
				zone, strings.Join(declared, ", ")),
		}
	}

	// A declared camera with tracks right now is watching; one with no tracks
	// has not said anything, and this function does not speak for it.
	alive := trackCameras(tracks)
	var watching []string
	for _, cam := range others {
		if alive[cam] {
			watching = append(watching, cam)
		}
	}
	if len(watching) == 0 {
		return Sight{
			Verdict:  SightCannotTell,
			Cameras:  witnesses,
			Declared: others,
			Reason: fmt.Sprintf("cannot tell: the other cameras declared for %s (%s) have no tracks, so their silence proves nothing",
				zone, strings.Join(others, ", ")),
		}
	}

	return Sight{
		Verdict:  SightSingle,
		Cameras:  witnesses,
		Declared: watching,
		Reason: fmt.Sprintf("single sighting by %s: %s watch %s and did not add a sighting",
			sighting, strings.Join(watching, " and "), zone),
	}
}

// trackCameras is the set of camera ids the given tracks came from.
func trackCameras(tracks []*Track) map[string]bool {
	out := make(map[string]bool, len(tracks))
	for _, t := range tracks {
		if t != nil && t.CameraID != "" {
			out[t.CameraID] = true
		}
	}
	return out
}

// distinct returns the non-empty, deduplicated, sorted members of ids.
func distinct(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
