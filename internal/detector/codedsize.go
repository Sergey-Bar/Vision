package detector

import "fmt"

// CodedSizeOptions configures [CodedSize].
type CodedSizeOptions struct {
	// Threshold is the score above which a picture is worth a query.
	Threshold float64
	// Sensitivity is how much bigger than its own recent baseline a picture has
	// to be before the score reaches 1. A value of 0.6 means "half again as
	// big". It is the single most consequential number in this package and the
	// one a reviewer will ask about.
	Sensitivity float64
	// Alpha is the weight of each new picture in the running baseline. Low
	// values react slowly, which is what keeps a single busy picture from
	// redefining "normal" for the next minute.
	Alpha float64
	// Window is how many recent pictures the score is averaged over before it is
	// compared with the threshold.
	//
	// It exists because the raw measurement is noisy at the frame level. One
	// picture's coded size swings with a compression hiccup, a scene detail, or
	// simply a two-frame periodicity in what the camera is looking at, and
	// comparing a single such number to a threshold is a trigger that flickers.
	// Three pictures is 120ms at 25fps, which is far below any interval that
	// matters for deciding to spend money, and it removes the flicker without
	// hiding an onset - which is itself several frames long by definition.
	Window int
	// Consecutive is how many pictures must exceed the threshold in a row before
	// the detector fires. One frame of noise is common; a run of them is a
	// change.
	Consecutive int
	// Warmup is how many pictures are folded into the baseline before the
	// detector will fire. Firing before the baseline exists would fire on the
	// camera's own start-up frames.
	Warmup int
}

func (o CodedSizeOptions) withDefaults() CodedSizeOptions {
	if o.Threshold <= 0 {
		o.Threshold = 0.5
	}
	if o.Sensitivity <= 0 {
		o.Sensitivity = 0.6
	}
	if o.Alpha <= 0 || o.Alpha > 1 {
		o.Alpha = 0.05
	}
	if o.Window <= 0 {
		o.Window = 3
	}
	if o.Consecutive <= 0 {
		o.Consecutive = 2
	}
	if o.Warmup <= 0 {
		o.Warmup = 25
	}
	return o
}

// CodedSize is the detector that needs no decoder.
//
// H.264 spends bits on change. A camera looking at nothing encodes tiny
// pictures; the moment a person walks through the frame the encoder spends
// several times as many bytes, and it spends them again on every frame they
// are still there. That is a motion signal, available from the bytes on the
// wire, with no decode, no model and no weights.
//
// # What it is not
//
// It is not frame differencing, and it does not know what moved. It cannot tell
// a person from a shadow from a branch from rain, and the threshold that suits
// one will not suit another: a camera with a fixed analogue gain in a dark car
// park has a noise floor that never stops, and the honest response is a false
// positive rate the operator can see rather than a claim of equivalence with
// the reference detector.
//
// The interface exists so a deployment that can decode can swap this for
// [FrameDiff] without touching anything upstream.
type CodedSize struct {
	opts     CodedSizeOptions
	baseline float64
	seen     int
	streak   int
	last     float64
	// window is the ring of recent raw scores the reported score is the mean
	// of. It is bounded by Window, so the memory is O(Window) and not O(stream).
	window []float64
	at     int
}

var _ Detector = (*CodedSize)(nil)

// NewCodedSize returns the decoder-free detector.
func NewCodedSize(opts CodedSizeOptions) *CodedSize {
	return &CodedSize{opts: opts.withDefaults()}
}

// Name identifies the implementation.
func (d *CodedSize) Name() string { return "coded-size" }

// Options returns the settings in force, so a score can be reproduced from the
// record that carries it.
func (d *CodedSize) Options() CodedSizeOptions { return d.opts }

// Reset drops the baseline. A baseline carried across a camera change measures
// the new camera against the old one's bitrate, which is meaningless.
func (d *CodedSize) Reset() {
	d.baseline = 0
	d.seen = 0
	d.streak = 0
	d.last = 0
	d.window = nil
	d.at = 0
}

// Baseline is the running mean picture size the detector compares against.
func (d *CodedSize) Baseline() float64 { return d.baseline }

// mean returns the mean of the window, which is the score the threshold is
// compared against.
func (d *CodedSize) push(v float64) float64 {
	if len(d.window) < d.opts.Window {
		d.window = append(d.window, v)
	} else {
		d.window[d.at] = v
		d.at = (d.at + 1) % len(d.window)
	}
	sum := 0.0
	for _, x := range d.window {
		sum += x
	}
	return sum / float64(len(d.window))
}

// Observe scores one coded picture by its size relative to the running
// baseline.
//
// A keyframe is scored like any other picture and never updates the baseline;
// see the comment at the update for why that distinction is the whole ballgame.
func (d *CodedSize) Observe(o Observed) (Reading, error) {
	r := Reading{Detector: d.Name(), Threshold: d.opts.Threshold}
	if o.CodedBytes <= 0 {
		return r, fmt.Errorf("detector: picture reports %d coded bytes", o.CodedBytes)
	}
	size := float64(o.CodedBytes)

	if d.seen < d.opts.Warmup {
		if d.baseline == 0 {
			d.baseline = size
		} else {
			d.baseline += d.opts.Alpha * (size - d.baseline)
		}
		d.seen++
		r.Reason = fmt.Sprintf("warming up: baseline is %.0f bytes over %d pictures", d.baseline, d.seen)
		return r, nil
	}
	if d.baseline <= 0 {
		// A baseline of zero is only reachable if every picture so far was empty,
		// and a division by it would be an infinity that reads as a certain
		// detection. The picture itself is the baseline.
		d.baseline = size
		d.seen++
		r.Reason = "no usable baseline yet; this picture becomes it"
		return r, nil
	}

	ratio := size / d.baseline
	score := d.push(clamp01((ratio - 1) / d.opts.Sensitivity))
	d.last = score
	r.Score = score

	// A window of one picture is not an average. Comparing the threshold
	// against a partial window makes the detector *noisier* at the start of a
	// scored run than it would be without the window at all, which is the
	// opposite of what the window is for - and the first pictures after warmup
	// are exactly the ones a keyframe inflates. So the window fills before it is
	// allowed to decide anything.
	if len(d.window) < d.opts.Window {
		d.baseline += d.opts.Alpha * (size - d.baseline)
		r.Reason = fmt.Sprintf("filling the scoring window: %d of %d pictures since the baseline was built",
			len(d.window), d.opts.Window)
		return r, nil
	}

	if score >= d.opts.Threshold {
		d.streak++
	} else {
		d.streak = 0
	}
	// The baseline absorbs the change after the fact, so a busy scene stops
	// looking busy within a few seconds. That is the intended behaviour: what
	// this detector is looking for is a *change*, not a permanently high
	// bitrate, and a site that genuinely has constant motion gets a permanently
	// quiet trigger rather than a permanently firing one.
	//
	// Keyframes are excluded from that update, and this is the one place the
	// design would otherwise be quietly wrong. A keyframe encodes every block
	// from scratch, so it is large for a reason that has nothing to do with what
	// is in the frame. Folding one into the baseline raises the bar by the time
	// the picture after it arrives, which is precisely when the motion is
	// still there - so the change that started the sequence is normalised away
	// and the trigger stays quiet through the exact event it exists to catch.
	if !o.KeyFrame {
		d.baseline += d.opts.Alpha * (size - d.baseline)
	}

	switch {
	case d.streak >= d.opts.Consecutive:
		r.Triggered = true
		r.Reason = fmt.Sprintf(
			"picture is %.2fx its %.0f-byte baseline for %d consecutive pictures",
			ratio, d.baseline, d.streak)
	case d.streak > 0:
		r.Reason = fmt.Sprintf(
			"picture is %.2fx its %.0f-byte baseline, over the threshold for %d of the %d consecutive pictures needed",
			ratio, d.baseline, d.streak, d.opts.Consecutive)
	default:
		r.Reason = fmt.Sprintf(
			"picture is %.2fx its %.0f-byte baseline, averaged over %d pictures, below the %.2fx the threshold needs",
			ratio, d.baseline, len(d.window), 1+d.opts.Sensitivity*d.opts.Threshold)
	}
	return r, nil
}

// LastScore is the most recent score, for a live status line.
func (d *CodedSize) LastScore() float64 { return d.last }
