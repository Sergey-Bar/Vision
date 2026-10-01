package detect

import (
	"sort"
	"strings"
	"time"
)

// Two-stage ghost association.
//
// # What it is for
//
// The score threshold is the only knob that decides what a run counts, and a
// detector's confidence is NOT CONSTANT across the frames of one object. A vehicle
// half-occluded by a pole scores 0.31 against an approval bar of 0.45; the same
// vehicle unoccluded scores 0.88. Under a one-stage tracker the occluded frame is
// simply REFUSED, the approved box on either side of it has no track to join, and
// the object becomes two tracks each too young to cross.
//
// So a refused box goes through a SECOND association pass, against the ghosts, and
// a track that a ghost matched RESUMES rather than dying. The object keeps one
// track across the dip, the track keeps its age, and the crossing is detected on
// the frame the object actually reached the line.
//
// # What it is NOT
//
// It does not APPROVE anything. A ghost is a ghost: it is not counted as a
// detection, it cannot emit a crossing, and it is not drawn as approved. It only
// prevents an approved object from being split in two by a threshold it happened
// to dip below.
//
// That distinction is the whole safety property of this file, and it is why the
// second pass cannot reuse the first one's machinery wholesale: the first pass
// grants a track to a box and the second must NOT.

// GhostMatch is one ghost box matched to one live track.
//
// It is returned separately from the association rather than folded into it,
// because the caller needs to DRAW the resumed tracks and needs to count the
// matches as a number a report can print — "31 tracks resumed through a score dip"
// is the sentence that tells an operator the threshold is right for this material,
// and "0 resumed" against a scene full of occlusion tells them it is not.
type GhostMatch struct {
	TrackID int
	// Ghost is the refused box that resumed the track, carried so the caller can
	// mark it on the picture as the reason the track did not die.
	Ghost Box
	// Reason is why the box was refused, which is the SAME reason the report already
	// prints. Carrying it here is what lets a resumed track be distinguished from one
	// that was never interrupted.
	Reason string
}

// associateGhosts is the second pass.
//
// It runs AFTER the approved association, so an approved box has already claimed
// its track and a ghost cannot steal it. The matching rules are deliberately the
// LOOSER of the two — a ghost may match on TOUCHING alone — because a ghost is
// by definition not a good enough box to pass the overlap threshold, and requiring
// it to pass the same test as an approved box would mean it never matches.
//
// The looseness is bounded by three things, and all three matter:
//
//   - the class must match, case-insensitively. This is the strongest single
//     constraint available: a `person` ghost cannot resume a `car` track.
//   - the track must already be AGED, i.e. it has been seen enough times at some
//     point in its life. A ghost cannot bring a track to age; it can only keep an
//     aged one alive. The flag is `aged` and not `allowed` — see Track.aged, which
//     records that using `allowed` here made this pass dead code.
//   - each track takes at most one ghost and each ghost takes at most one track, so
//     two objects passing each other cannot merge.
//
// A track that a ghost matched keeps its Box, its Seen count and its Kalman state
// UNCHANGED. It does not move to the ghost's position, because the ghost is not a
// measurement this run trusts, and a track that moved to an untrusted box would be
// positioned by the thing it was refused for.
func (e *Engine) associateGhosts(at time.Time, ghosts []Ghost) []GhostMatch {
	if len(ghosts) == 0 || len(e.live) == 0 {
		return nil
	}
	type pair struct {
		track int
		ghost int
		score float64
	}
	var pairs []pair
	for _, t := range e.live {
		if !t.aged {
			continue
		}
		for gi, g := range ghosts {
			if !strings.EqualFold(t.Class, g.Box.Class) {
				continue
			}
			v := iou(t.Box, g.Box)
			if v > e.cfg.IoU {
				// Any overlap at all outranks a bare touch, matching the ordering the
				// approved pass uses, so the two passes rank consistently.
				pairs = append(pairs, pair{track: t.ID, ghost: gi, score: 1 + v})
			} else if touching(t.Box, g.Box) {
				pairs = append(pairs, pair{track: t.ID, ghost: gi, score: v})
			}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })

	assigned := map[int]int{}
	usedGhost := map[int]bool{}
	for _, p := range pairs {
		if _, ok := assigned[p.track]; ok {
			continue
		}
		if usedGhost[p.ghost] {
			continue
		}
		assigned[p.track] = p.ghost
		usedGhost[p.ghost] = true
	}

	out := make([]GhostMatch, 0, len(assigned))
	for id, gi := range assigned {
		g := ghosts[gi]
		if t, ok := e.live[id]; ok {
			t.resumed++
		}
		out = append(out, GhostMatch{TrackID: id, Ghost: g.Box, Reason: g.Reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TrackID < out[j].TrackID })
	return out
}

// Resumed reports how many tracks were held alive by a ghost this frame.
//
// It is a STATS field rather than something recomputed from the matches list,
// because the report prints it every frame and a list length is not a number a
// caller can average without holding the list.
func Resumed(matches []GhostMatch) int { return len(matches) }
