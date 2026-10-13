package main

// The run half of `bench`: false positives per HOUR over real footage, and track
// stability.
//
// # Why this needs the store and not the detector
//
// A still set has no duration, so it cannot produce a per-hour rate — the one
// number that goes on a purchase order. That number needs wall-clock time and a
// count of the boxes an operator would have had to dismiss, and the only place
// both of those exist is a recorded run.
//
// So this half reads the store. It opens no model, and that is structural rather
// than a matter of discipline: the function below takes a store and a run id and has
// no path to a runtime library at all. Re-decoding the material with a model here
// would measure the MODEL rather than the run, and the two disagree whenever the
// run used different settings — which is most of the time.
//
// # False positives per hour needs a numerator somebody marked
//
// The count of false positives is the count of REJECTED labels. Nobody has to mark
// anything to compute the other two rates, but a false-positive rate is a claim
// about what a human would have dismissed, and only a human can say. So a run with
// no labels reports the rate as NOT RUN and names that, rather than counting every
// rejected detection as a false positive — which would report the detector's
// recall as its precision, inverted, and look like a catastrophe.
//
// # Track stability, and why the duration here is not the run's
//
// ID switches over a track's life. A long-lived track that is reassigned five times
// is one object followed badly; the same track reassigned five times in five
// seconds is five objects. Both report "5 switches", so stability is reported per
// SECOND as well as per track, and a track's observed duration is printed beside
// both. A stability figure with no time scale beside it is a count of something
// nobody can act on.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/bench"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// RunBench is a finished run's measurement.
type RunBench struct {
	Report  bench.Report
	Options bench.Options
	// Stability is the track-stability arithmetic, and it is nil when the run has no
	// per-frame box log to compute it from.
	Stability *Stability
	// Labels is what the run's marking session found, and it is nil when there was
	// none — which is the case the per-hour rate reports NOT RUN for.
	Labels *store.LabelCounts
}

// Stability is how often the tracker changed its mind about a track's identity.
type Stability struct {
	// Tracks is how many distinct track ids the run saw.
	Tracks int
	// Switches is how many times a track id was seen on two different frames with a
	// gap, which is a track resuming after an interruption.
	Switches int
	// PerSecond normalises it by the run's observed duration, and it is nil over a
	// run with no measured duration.
	PerSecond *float64
	// PerTrack is switches over tracks, and it is nil over a run with no tracks.
	PerTrack *float64
	// MedianTrackSeconds is the observed lifetime of a track, which is what tells a
	// reader whether 20 switches is a lot.
	MedianTrackSeconds *float64
}

// benchRecordedRun measures a run from its store.
func benchRecordedRun(ctx context.Context, st *store.Store, runID string) (RunBench, error) {
	var out RunBench
	if st == nil {
		return out, errors.New("bench: the run measurement needs a store; it measures what a run " +
			"recorded and a nil recorded nothing")
	}

	out.Options = bench.DefaultOptions()

	// --- the duration, from the run's own clock --------------------------------
	//
	// `run.TargetSeconds` is the DECLARED target and is not the material's length;
	// dividing a count by it would report a rate against a plan rather than against
	// what happened. The observed span of the stored detections is the real one, and
	// it is nil when the run recorded nothing.
	observed, spanKnown := observedSpan(ctx, st, runID)
	seconds := 0.0
	if spanKnown {
		seconds = observed.Seconds()
	}

	// --- the labels, which are the false-positive numerator ---------------------
	labels, err := st.LabelTally(ctx, runID)
	if err != nil {
		return out, fmt.Errorf("bench: the labels for %s could not be read: %w", runID, err)
	}
	out.Labels = &labels

	// --- the proposals, the marks and the FP rate -------------------------------
	//
	// Every PROPOSED box is counted in the detection denominator. A rejected LABEL
	// is a false positive. A box nobody marked is neither — it is work not yet done,
	// and counting it as a false positive would report the detector's recall as its
	// precision.
	proposed, err := st.ProposedBoxCount(ctx, runID)
	if err != nil {
		return out, err
	}
	counts := bench.Counts{
		Detected:       proposed,
		FalsePositives: labels.Rejected,
		Seconds:        seconds,
	}
	_ = counts
	out.Report = bench.Accumulate(
		[]bench.FrameCount{{
			Frame: runID, Detected: proposed, FalsePositives: labels.Rejected,
		}},
		seconds, nil, 0,
	)
	out.Report.Unmarked = proposed - labels.Marked
	if out.Report.Unmarked < 0 {
		// The counts disagree, which means the label set and the proposal set came
		// from different runs, or the proposal set was sampled. It is said rather
		// than left negative, because a negative "unmarked" count prints as a negative
		// number on a report and a reader has no idea why.
		out.Report.Unmarked = 0
		fmt.Println("bench: WARNING the marking session holds more labels than the run proposed.")
		fmt.Println("  Either the two were taken from different runs, or the box log was sampled.")
	}
	out.Report.TPFromLabels = labels.Accepted

	// --- track stability --------------------------------------------------------
	if rows, err := st.BoxObservations(ctx, runID); err == nil && len(rows) > 0 {
		out.Stability = trackStability(rows)
	}
	return out, nil
}

// observedSpan is the run's real duration, from the first and last recorded frame.
func observedSpan(ctx context.Context, st *store.Store, runID string) (time.Duration, bool) {
	rows, err := st.BoxObservations(ctx, runID)
	if err != nil || len(rows) < 2 {
		return 0, false
	}
	first, last := rows[0].At, rows[0].At
	for _, r := range rows {
		if r.At.Before(first) {
			first = r.At
		}
		if r.At.After(last) {
			last = r.At
		}
	}
	if last.Before(first) {
		return 0, false
	}
	return last.Sub(first), true
}

// trackStability counts how often a track id reappeared after a gap.
//
// Only MEASURED rows with a real track id are considered. The box log currently
// records -1 for a box no track has claimed, and a -1 that "reappears" every frame
// would otherwise report a stability figure of one switch per frame — the most
// alarming possible number from the most meaningless possible input.
func trackStability(rows []store.BoxObservation) *Stability {
	s := &Stability{}
	byTrack := map[int][]time.Time{}
	for _, r := range rows {
		if !r.Measured || r.TrackID < 0 {
			continue
		}
		byTrack[r.TrackID] = append(byTrack[r.TrackID], r.At)
	}
	s.Tracks = len(byTrack)
	if s.Tracks == 0 {
		return s
	}
	var lifetimes []float64
	for id, times := range byTrack {
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		if len(times) < 2 {
			lifetimes = append(lifetimes, 0)
			continue
		}
		lifetimes = append(lifetimes, times[len(times)-1].Sub(times[0]).Seconds())
		// A switch is a GAP: two sightings of one track id with a missing frame
		// between them. Consecutive frames are the track being seen, not resumed.
		for i := 1; i < len(times); i++ {
			if gap := times[i].Sub(times[i-1]).Seconds(); gap > 1.5 {
				s.Switches++
				_ = id
			}
		}
	}
	sort.Float64s(lifetimes)
	mid := lifetimes[len(lifetimes)/2]
	s.MedianTrackSeconds = &mid
	if t := float64(s.Tracks); t > 0 {
		v := float64(s.Switches) / t
		s.PerTrack = &v
	}
	if total := lifetimes[len(lifetimes)-1] - lifetimes[0]; total > 0 {
		v := float64(s.Switches) / total
		s.PerSecond = &v
	}
	return s
}

// FormatRunBench renders a run measurement, with the two rates that need marking
// reported as NOT RUN and naming what is missing.
func FormatRunBench(runID string, b RunBench) string {
	var out strings.Builder
	fmt.Fprintf(&out, "run measurement: %s\n", runID)
	if b.Report.Seconds > 0 {
		fmt.Fprintf(&out, "  observed      %.1f seconds (the run's own frames, not its declared target)\n",
			b.Report.Seconds)
	} else {
		out.WriteString("  observed      NOT MEASURED — the run recorded fewer than two frames, so\n")
		out.WriteString("                there is no duration and every per-hour rate below is NOT RUN.\n")
	}
	if b.Labels != nil {
		fmt.Fprintf(&out, "  marking       %d marked (%d accepted, %d rejected), %d unmarked\n",
			b.Labels.Marked, b.Labels.Accepted, b.Labels.Rejected, b.Report.Unmarked)
		if len(b.Labels.By) > 0 {
			fmt.Fprintf(&out, "                by %s\n", strings.Join(b.Labels.By, ", "))
		}
		if b.Labels.Marked == 0 {
			out.WriteString("\n                NOTHING WAS MARKED. A false-positive rate is a claim about what a\n")
			out.WriteString("                human would have dismissed, and only a human can say. It is NOT RUN,\n")
			out.WriteString("                and it is emphatically not zero. Run `pocctl label` over this run.\n")
		}
	}
	if b.Report.Unmarked > 0 {
		fmt.Fprintf(&out, "  unmarked      %d of %d proposed boxes carry no judgement. They are counted\n",
			b.Report.Unmarked, b.Report.Detected)
		out.WriteString("                in the denominator and counted as neither true nor false, because a\n")
		out.WriteString("                box nobody looked at is work not yet done rather than a measurement.\n")
	}
	if v, ok := b.Report.FPPerHour(); ok {
		fmt.Fprintf(&out, "  FP PER HOUR   %.3f\n", v)
	} else {
		out.WriteString("  FP PER HOUR   NOT RUN\n")
	}
	if v, ok := b.Report.Precision(); ok && b.Labels != nil && b.Labels.Marked > 0 {
		fmt.Fprintf(&out, "  PRECISION     %.3f over MARKED boxes only (%d accepted of %d marked)\n",
			v, b.Labels.Accepted, b.Labels.Marked)
	} else {
		out.WriteString("  PRECISION     NOT RUN — precision over marked boxes needs a marking session, and\n")
		out.WriteString("                over unmarked ones it would be a claim about nobody's attention.\n")
	}
	writePerClass(&out, b.Report)
	if b.Stability == nil {
		out.WriteString("  STABILITY     NOT RUN — this run has no per-frame box log, so there is nothing\n")
		out.WriteString("                to count track identity over. Run with SP_POC_BOX_LOG_FRAMES above 0.\n")
	} else if b.Stability.Tracks == 0 {
		out.WriteString("  STABILITY     NOT RUN — no measured box carries a track id yet. The tracker\n")
		out.WriteString("                has not claimed a box, so there is no identity to be stable about.\n")
	} else {
		fmt.Fprintf(&out, "  STABILITY     %d tracks, %d id switches\n", b.Stability.Tracks, b.Stability.Switches)
		if b.Stability.PerTrack != nil {
			fmt.Fprintf(&out, "                %.2f switches per track\n", *b.Stability.PerTrack)
		}
		if b.Stability.PerSecond != nil {
			fmt.Fprintf(&out, "                %.3f switches per second\n", *b.Stability.PerSecond)
		}
		if b.Stability.MedianTrackSeconds != nil {
			fmt.Fprintf(&out, "                median track lifetime %.1f seconds\n",
				*b.Stability.MedianTrackSeconds)
		}
		out.WriteString("                A switch count with no time scale beside it is a count of something\n")
		out.WriteString("                nobody can act on, so both rates are printed.\n")
	}
	return out.String()
}
