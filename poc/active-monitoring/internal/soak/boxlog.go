package soak

// The per-frame box log, and the bound on it.
//
// # Why this exists
//
// Once the crossing box is Kalman-filtered it is a FUNCTION of the frames around
// it, and a function of context cannot be recomputed from the `decisions` table
// alone — that table holds one row per incident and nothing about the track it came
// from. So replay needs the per-frame boxes, and this is where they are written.
//
// That makes the log load-bearing for RC-4 and RC-20 rather than diagnostic, which
// is also why the bound below is a SAMPLER and not a truncation.
//
// # The bound is a cap, and a cap that silently drops is a cap that lies
//
// SQLite here runs `synchronous=FULL` on one connection, so an unbounded per-frame
// log at 30 fps is a real ceiling: AC1 fails on storage latency, which has nothing
// to do with detection and which looks exactly like a detector too slow to keep up.
//
// At the cap the log keeps EVERY frame that produced a crossing and every Nth of
// the rest. That is the right split because the two kinds are not equally
// valuable: a frame with no crossing is context, and a frame with one is the claim.
// What was dropped is counted and recorded on the run, so a reader who sees a
// replay over a sampled log is told the log was sampled rather than being left to
// assume it read everything.

import (
	"context"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/detect"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision/shared/classify"
	"github.com/Sergey-Bar/vision/shared/sidecar"
)

// BoxLog is the run's bounded per-frame box log.
//
// It decides WHICH frames get written and the runner hands the result to the store.
// That split is deliberate: the sampling rule is testable without a database, and
// the database write is testable without a sampling rule.
//
// It is NOT safe for concurrent use, and it does not claim to be. The frame loop
// calls it once per frame on the one goroutine that owns the run, and the report
// reads [BoxLogStats] after that loop has finished.
type BoxLog struct {
	// Cap is SP_POC_BOX_LOG_FRAMES. Zero disables the log entirely, which is a real
	// setting and not a default: a run that will not be replayed has no use for it.
	Cap int

	seen       int
	kept       int
	sampledOut int
	// everyNth is the sampling INTERVAL the log is currently on. It is exposed as
	// Stats().EveryNth and reported, because a sampling interval nobody named is not
	// a sampling rule anybody can reason about.
	everyNth int
}

// NewBoxLog builds a logger for a run's declared cap.
//
// A cap of zero DISABLES the log and is preserved rather than clamped to one.
// `SP_POC_BOX_LOG_FRAMES=0` is refused by the configuration layer, so this cannot
// come from a run — but the type is also used by tests and by the report, and a
// constructor that silently turned "off" into "keep one frame in ten thousand"
// would make the disabled state unrepresentable rather than merely refused.
func NewBoxLog(cap int) *BoxLog {
	if cap < 0 {
		cap = 0
	}
	return &BoxLog{Cap: cap}
}

// ShouldKeep reports whether this frame's boxes belong in the log, and why.
//
// The two return values answer two different questions and both are needed. `keep`
// is the decision; `because` is the sentence a report prints beside it, because
// "the box log was sampled" is only meaningful if the reader can see WHICH rule
// sampled it.
func (b *BoxLog) ShouldKeep(crossings int) (keep bool, because string) {
	if b == nil || b.Cap < 1 {
		return false, "the box log is disabled (SP_POC_BOX_LOG_FRAMES=0), so this run cannot be replayed from per-frame boxes"
	}
	b.seen++
	// A crossing frame is always kept, whatever the cap. It is the claim; the rest
	// is context.
	if crossings > 0 {
		b.kept++
		return true, "a crossing frame, which the sampling rule never drops"
	}
	if b.kept < b.Cap {
		b.kept++
		return true, "a frame inside the log cap"
	}
	// At the cap: thin on a LOGARITHMIC schedule.
	//
	// The obvious rule — keep every Nth — is wrong twice over. A FIXED N fills the
	// remaining budget in the first N frames and then goes silent, so a replay
	// cannot see the second half of an incident. A GROWING N of the form
	// `everyNth++` thins so fast that it goes silent too: past a cap of 10 the
	// interval reaches 96 within a hundred frames and stops firing, which is
	// exactly the front-loading it was meant to avoid.
	//
	// This is the schedule that actually keeps spreading: the interval grows with
	// the run's own length, so the retained frames are spread across the WHOLE clip
	// and the count past the cap grows like a logarithm of it rather than linearly.
	// A reader who wants to know the cost of the cap reads
	// Stats().EveryNth, which is the interval the log settled on.
	step := 1 + (b.seen-b.Cap)/b.Cap
	b.everyNth = step
	if b.seen%step == 0 {
		b.kept++
		return true, "a sampled frame past the cap"
	}
	b.sampledOut++
	return false, "a frame past the log cap, sampled out"
}

// BoxLogStats is the log's own arithmetic, for the run record.
//
// SampledOut is the number that matters: a reader who sees it knows the log does
// not describe every frame, and a replay over a sampled log is a weaker claim than
// one over a complete one — which is a fact about the claim, not a footnote on it.
type BoxLogStats struct {
	Cap        int
	Seen       int
	Kept       int
	SampledOut int
	// EveryNth is the sampling interval the log settled on. It is reported because a
	// sampling interval nobody named is not a sampling rule anybody can reason about.
	EveryNth int
}

// Sampled reports whether anything was dropped, and it is what the run record's
// `box_log_sampled` flag is set from.
func (b *BoxLog) Sampled() bool { return b != nil && b.sampledOut > 0 }

// Stats returns the log's arithmetic.
func (b *BoxLog) Stats() BoxLogStats {
	if b == nil {
		return BoxLogStats{}
	}
	return BoxLogStats{
		Cap: b.Cap, Seen: b.seen, Kept: b.kept,
		SampledOut: b.sampledOut, EveryNth: b.everyNth,
	}
}

// SourceFor is what the `source` column on a box observation records.
//
// A measurement made by the Python sidecar and one made by the shipped in-process
// detector must never be reported as the same thing, and the only way to guarantee
// that is for the row to say which.
func SourceFor(mode string) string {
	switch mode {
	case TriggerDetector, TriggerSidecar, TriggerNovelty:
		return mode
	case "":
		return TriggerNovelty
	}
	return mode
}

// boxObservationsFor builds the rows one frame contributes.
//
// Four things are worth stating, because each is a way a replay could silently
// read the wrong thing:
//
//   - a frame with NO boxes still gets a row, and its `measured` flag says whether
//     the model was asked. Without it, "the model saw nothing" and "the pipeline
//     never asked" are the same absence, and a reader counting rows computes a
//     recall of zero out of a run that never ran. This is the defect class this
//     repository has shipped twice.
//   - family is nil for a class the taxonomy does not place, and nil means "not
//     recorded" rather than "no family". An empty string would read as a recorded
//     absence.
//   - track_id is the tracker's own identity, recovered from the live tracks by
//     matching the box. It is -1 when no track claimed the box, which is a
//     SPECIFIC claim rather than a placeholder — 0 would be a track number nobody
//     has.
//   - the track id matters more than it looks. `store.ProposedBoxCount` counts
//     rows with `track_id >= 0`, and that count is the marking session's whole
//     denominator — so a log that wrote -1 on every row would leave the reviewer
//     with nothing to judge and the false-positive rate NOT RUN forever, with no
//     error anywhere. That is the shape of this bug the first time it existed.
func boxObservationsFor(runID string, seq int64, at time.Time, boxes []sidecar.Box,
	tel sidecar.Telemetry, tracks []detect.Track, source string) []store.BoxObservation {

	measured := tel.Candidates > 0 || tel.PreNms > 0 || len(boxes) > 0
	if !measured || len(boxes) == 0 {
		return []store.BoxObservation{{
			RunID: runID, FrameSeq: seq, At: at, TrackID: -1,
			Source: source, Measured: measured,
		}}
	}

	// The live tracks, indexed by class, so a box can be attributed to one.
	//
	// Matching is by class plus overlap, and it is the SAME rule the engine's own
	// permission loop uses — so a box the engine would let cross is the same box
	// this attributes to that track. A second rule here would be a second answer to
	// "which track is this", and the two would disagree exactly where it mattered.
	byClass := map[string][]detect.Track{}
	for _, t := range tracks {
		byClass[strings.ToLower(t.Class)] = append(byClass[strings.ToLower(t.Class)], t)
	}
	attr := func(b sidecar.Box) int {
		best, bestIou := -1, 0.0
		for _, t := range byClass[strings.ToLower(b.Class)] {
			if v := detect.Overlap(t.Box, b); v > bestIou {
				best, bestIou = t.ID, v
			}
		}
		return best
	}

	out := make([]store.BoxObservation, 0, len(boxes))
	for _, b := range boxes {
		var family *string
		if f := classify.FamilyOf(b.Class); f != "" {
			fam := string(f)
			family = &fam
		}
		out = append(out, store.BoxObservation{
			RunID: runID, FrameSeq: seq, At: at, TrackID: attr(b),
			Class: b.Class, Family: family, Score: b.Score,
			Top: int(b.Y1 * 1000), Left: int(b.X1 * 1000),
			Bottom: int(b.Y2 * 1000), Right: int(b.X2 * 1000),
			Source: source, Measured: true,
		})
	}
	return out
}

// writeBoxObservations records one frame's boxes, tolerating a store whose database
// predates the table.
//
// The tolerance is a MIGRATION concern and not a permissions one: a data root
// written by an older build has no `box_observations` table, and a run against one
// should still produce its detections and its report. The error is logged so a run
// that CANNOT log its boxes says so — silently skipping the log on a database that
// does have the table would be the far worse outcome, because the run would produce
// a crossing it can never replay and report nothing about why.
func (r *Runner) writeBoxObservations(ctx context.Context, obs []store.BoxObservation) {
	if r.opt.Store == nil || len(obs) == 0 {
		return
	}
	if err := r.opt.Store.PutBoxObservations(ctx, obs); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return
		}
		r.logf("the per-frame box log could not be written, so this run cannot be replayed from "+
			"stored boxes: %v", err)
	}
}
