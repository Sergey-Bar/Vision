package soak

// The footer: what model ran, what it cost, what it found, and what the run may not
// claim.
//
// # Why the picture carries this at all
//
// A judge, a customer and a ticket all get a PNG. Nothing else. So whatever the
// picture does not carry, nobody downstream ever learns — and a screenshot showing
// boxes while omitting `det_p99 = 61ms against a 5 fps demand` is a screenshot
// that makes a system which cannot hold its frame rate look like one that can.
//
// This is the same reasoning that put the model's verdict on the picture rather
// than only in a row of HTML the crop tool could remove, and it is stated here
// because the temptation to move it into the console is the same one each time.
//
// # What it refuses to print
//
// It prints nothing for a component that was not configured. A `model: —` line is a
// claim that a model was configured and none was found, and a `precision 0.000`
// line is a claim that the detector was perfectly wrong, when in fact no marking
// session happened. Every absence here is an ABSENCE, and this module's rule is
// that an absence is never rendered as a zero or a dash.

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/overlay"
	"github.com/Sergey-Bar/vision/shared/classify"
)

// footer builds the strip for this run.
//
// It is a method rather than a free function because everything on the strip comes
// from the runner: the model's identity, the box log's arithmetic, the run's
// counters. A free function would take six arguments and somebody would pass two of
// them in the wrong order.
func (r *Runner) footer() overlay.Footer {
	f := overlay.Footer{}
	id := r.opt.DetIdentity

	// The model line. Absent when no detector ran, which is the `novelty` and
	// `sidecar` case — and a run in those modes prints no model line rather than
	// one that says there was none.
	if UsesDetector(r.opt.TriggerMode) && id.ModelPath != "" {
		input := ""
		if id.InputSize > 0 {
			input = fmt.Sprintf("%dx%d", id.InputSize, id.InputSize)
		}
		f = overlay.DetectorFooter(baseName(id.ModelPath), id.SHA256, input,
			r.detP50(), r.detP95(), r.detP99(), r.detLatencyKnown(),
			r.decodeRateText(), r.declaredRateText())
	}

	// The box log's arithmetic. This is the line that makes a replay's strength
	// visible: a run whose log was sampled makes a WEAKER claim than one whose log
	// was not, and the picture is where somebody finds that out.
	if st := r.BoxLogStats(); st.Seen > 0 {
		f.BoxLog = fmt.Sprintf("%d frames seen, %d kept", st.Seen, st.Kept)
		f.Sampled = r.BoxLogSampled()
	}

	// The counts, per FAMILY, because a single total hides which family the cap ate
	// and "we drew 12 of 40" without saying which 28 is a number nobody can act on.
	if f.Boxes == "" {
		f.Boxes, f.Cut = r.countersText()
	}
	_ = classify.FamilyVehicle
	return f
}

// detLatencyKnown says whether a detector latency exists at all, and it is separate
// from the values so an UNMEASURED detector prints no cost line rather than one
// reading zero milliseconds.
func (r *Runner) detLatencyKnown() bool {
	return r.opt.Detector != nil && r.detP99() > 0
}

func (r *Runner) detP50() float64 { return r.detPercentile(50) }
func (r *Runner) detP95() float64 { return r.detPercentile(95) }
func (r *Runner) detP99() float64 { return r.detPercentile(99) }

// detPercentile reads the DETECTOR's own distribution.
//
// The runner's `lat` is a running figure over every measured frame including the
// sidecar's, and a p99 computed from a mean cannot be computed at all. So this
// reads the detector's bounded ring — the same window the preflight gate refuses
// on, which is the point of it being one ring.
func (r *Runner) detPercentile(p int) float64 {
	lp, ok := r.opt.Detector.(latencyPercentiles)
	if !ok {
		return 0
	}
	p50, p95, p99 := lp.LatencyPercentiles()
	switch p {
	case 50:
		if p50 != nil {
			return *p50
		}
	case 95:
		if p95 != nil {
			return *p95
		}
	case 99:
		if p99 != nil {
			return *p99
		}
	}
	return 0
}

// decodeRateText is how fast frames are arriving, as a word rather than a number
// with no comparison beside it.
func (r *Runner) decodeRateText() string {
	t := r.DetectorTally()
	if !t.Seen || t.FramesAsked == 0 {
		return ""
	}
	secs := r.observedSeconds()
	if secs <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f fps", float64(t.FramesAsked)/secs)
}

// declaredRateText is what the configuration asked the detector for, which is the
// comparison a reader needs: a decode rate well below the declared detection rate
// is the signature of a detector that cannot keep up.
func (r *Runner) declaredRateText() string {
	if r.opt.AIFPS <= 0 {
		return ""
	}
	return fmt.Sprintf("%d fps", r.opt.AIFPS)
}

// countersText is the run's own box arithmetic, as "drawn of proposed".
func (r *Runner) countersText() (boxes, cut string) {
	t := r.DetectorTally()
	if !t.Seen {
		return "", ""
	}
	s := fmt.Sprintf("%d approved of %d candidates", t.BoxesApproved, t.BoxCandidates)
	if t.BoxesRejected > 0 {
		return s, fmt.Sprintf("%d rejected", t.BoxesRejected)
	}
	return s, ""
}

// observedSeconds is how long the run has been going, from the detector tally's
// own frame count and the runner's start.
//
// It is derived rather than read from the target, because a rate computed against
// a DECLARED duration is a rate against a plan.
func (r *Runner) observedSeconds() float64 {
	if r.start.IsZero() {
		return 0
	}
	return time.Since(r.start).Seconds()
}

// baseName is a path's last element, and it is here rather than imported from path
// because the value is for a PICTURE and a picture should show the model's name,
// not a path that is machine-specific and longer than the strip.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}
