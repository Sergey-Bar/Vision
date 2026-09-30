package detector

import (
	"fmt"
)

// FrameDiffOptions configures [FrameDiff].
type FrameDiffOptions struct {
	// Threshold is the changed-pixel ratio above which a picture is worth a
	// query. It is a field rather than a constant because it is the number a
	// reviewer will challenge, and a number that cannot be printed cannot be
	// challenged on its own terms.
	Threshold float64
	// NoiseFloor is the per-pixel luma difference below which a pixel counts as
	// unchanged. Without it a sensor's own noise is a novelty event, which is
	// how a false-positive rate gets to 1.0 in the dark.
	NoiseFloor int
	// Consecutive is how many pictures must exceed the threshold in a row before
	// the detector fires. One frame of noise is common; three is a change.
	Consecutive int
	// Decimate reduces the plane before comparing. Zero leaves it alone.
	Decimate int
	// Warmup is how many pictures are observed before the detector will fire at
	// all. The first frame has no predecessor, so a baseline has to be built
	// before it can be compared against.
	Warmup int
}

func (o FrameDiffOptions) withDefaults() FrameDiffOptions {
	if o.Threshold <= 0 {
		o.Threshold = 0.08
	}
	if o.NoiseFloor <= 0 {
		o.NoiseFloor = 6
	}
	if o.Consecutive <= 0 {
		o.Consecutive = 2
	}
	if o.Warmup <= 0 {
		o.Warmup = 2
	}
	return o
}

// FrameDiff is the reference detector: the fraction of pixels whose luma moved
// by more than the noise floor since the previous picture.
//
// It is per-camera state. Two cameras sharing one reference frame compare
// against each other and score every pixel as changed, which reads as a
// permanent event rather than as a bug.
type FrameDiff struct {
	opts   FrameDiffOptions
	prev   *Luma
	prevOK bool
	seen   int
	streak int
	last   float64
}

var _ Detector = (*FrameDiff)(nil)

// NewFrameDiff returns the reference detector.
func NewFrameDiff(opts FrameDiffOptions) *FrameDiff {
	return &FrameDiff{opts: opts.withDefaults()}
}

// Name identifies the implementation.
func (d *FrameDiff) Name() string { return "frame-difference" }

// Options returns the settings in force, so a score can be reproduced from the
// record that carries it.
func (d *FrameDiff) Options() FrameDiffOptions { return d.opts }

// Reset drops the reference frame and the streak.
func (d *FrameDiff) Reset() {
	d.prev = nil
	d.prevOK = false
	d.seen = 0
	d.streak = 0
	d.last = 0
}

// Observe compares one decoded picture against the previous one.
func (d *FrameDiff) Observe(o Observed) (Reading, error) {
	r := Reading{Detector: d.Name(), Threshold: d.opts.Threshold}
	if o.Luma == nil {
		return r, ErrNoLuma
	}
	cur := *o.Luma
	if d.opts.Decimate > 1 {
		small, err := Decimate(cur, d.opts.Decimate)
		if err != nil {
			return r, err
		}
		cur = small
	}
	if !cur.Valid() {
		return r, fmt.Errorf("detector: luma plane is %dx%d with %d bytes of stride", cur.Width, cur.Height, cur.Stride)
	}
	defer func() {
		// The reference becomes this picture whether or not the comparison
		// happened. A detector that only advances on a clean comparison holds
		// the last good frame and compares against it forever.
		d.prev = &cur
		d.prevOK = true
		d.seen++
	}()

	if !d.prevOK || d.seen < d.opts.Warmup {
		r.Reason = "warming up: no previous picture to compare against"
		return r, nil
	}
	// A resolution change is a new camera as far as the detector is concerned.
	if d.prev.Width != cur.Width || d.prev.Height != cur.Height {
		d.streak = 0
		r.Score = 0
		r.Reason = "frame size changed: the previous picture is not comparable and the baseline restarts"
		return r, nil
	}

	total := cur.Width * cur.Height
	changed := 0
	var sum int
	for y := 0; y < cur.Height; y++ {
		row := y * cur.Stride
		prow := y * d.prev.Stride
		for x := 0; x < cur.Width; x++ {
			delta := int(cur.Plane[row+x]) - int(d.prev.Plane[prow+x])
			if delta < 0 {
				delta = -delta
			}
			sum += delta
			if delta > d.opts.NoiseFloor {
				changed++
			}
		}
	}
	ratio := float64(changed) / float64(total)
	mean := float64(sum) / float64(total)
	d.last = ratio
	r.Score = clamp01(ratio / d.opts.Threshold)

	if ratio >= d.opts.Threshold {
		d.streak++
	} else {
		d.streak = 0
	}
	if d.streak >= d.opts.Consecutive {
		r.Triggered = true
		r.Reason = fmt.Sprintf(
			"%.1f%% of pixels moved more than %d luma levels for %d consecutive pictures",
			ratio*100, d.opts.NoiseFloor, d.streak)
		return r, nil
	}
	r.Reason = fmt.Sprintf(
		"%.1f%% of pixels moved more than %d luma levels, mean shift %.1f, below the %.1f%% threshold",
		ratio*100, d.opts.NoiseFloor, mean, d.opts.Threshold*100)
	return r, nil
}

// LastRatio is the changed-pixel fraction of the most recent comparison.
func (d *FrameDiff) LastRatio() float64 { return d.last }
