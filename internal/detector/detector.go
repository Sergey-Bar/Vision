// Package detector is the always-on trigger: cheap, local, and rule-free.
//
// # Why it exists
//
// A vision-language model costs about $0.15 a query. At one query every ten
// seconds on one camera that is roughly $1,300 a day, so continuous model
// sampling does not exist economically and anything that depends on it is a
// demo rather than a product. Whatever decides *when to look* has to run on
// hardware already in the rack, at a rate nobody has to budget for.
//
// That is this package. It watches a stream and reports a novelty score.
//
// # What it deliberately does not do
//
// It does not decide anything. A Detector returns a score and a reason and
// nothing else - there is no field for a verdict, no "this is a person", no
// class. Three reasons, in order of how much damage the alternative does:
//
//   - A detector that emits a verdict has to be trained, and a trained model on
//     a 6-week clock is a threshold someone will challenge and a model nobody
//     can explain to a customer.
//   - The one thing this product must never do is fabricate a negative. A
//     detector that reports "no problem" is a model that ran and said nothing
//     happened, which is exactly the confusion the three-absences discipline
//     exists to prevent. A novelty score has no such failure mode available to
//     it, because the vocabulary does not contain the word.
//   - The VLM owns verdicts. The model card says it is not designed to be the
//     sole basis for a decision, and the reconciliation is that the trigger is
//     rule-free while what becomes an incident and what is audited stay
//     deterministic and governed.
//
// # Two implementations, one interface
//
// The interface exists so the second one is a swap rather than a rewrite.
//
//   - [FrameDiff] is the reference: it compares decoded luma and is the CPU path
//     a GPU decode feeds. It is the better detector and it needs a decoder.
//   - [CodedSize] works on the coded picture's own size and needs no decoder at
//     all. It is what a stream-only capture path can run today.
//
// They are not equally good and the product says which one produced a number
// rather than presenting both as the same thing.
package detector

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrNoLuma is returned by a detector that needs decoded pixels when it is
// given a coded picture it cannot read. It is a distinct error because the two
// detector families need different things from the capture path, and silently
// substituting one for the other is how a stream-only deployment ends up
// reporting frame-differencing scores it never computed.
var ErrNoLuma = errors.New("detector: this detector needs a decoded luma plane")

// Luma is a decoded greyscale plane.
type Luma struct {
	Width  int
	Height int
	Stride int
	Plane  []byte
}

// Valid reports whether the plane can be read without panicking.
func (l Luma) Valid() bool {
	return l.Width > 0 && l.Height > 0 && l.Stride >= l.Width && len(l.Plane) >= l.Stride*(l.Height-1)+l.Width
}

// Sample reads one pixel, clamping rather than trusting the caller's arithmetic.
func (l Luma) Sample(x, y int) byte {
	if x < 0 || y < 0 || x >= l.Width || y >= l.Height {
		return 0
	}
	return l.Plane[y*l.Stride+x]
}

// Observed is one reading handed to a Detector.
//
// Luma is optional. A capture path that has not decoded its frames still has
// every field a compressed-domain detector needs, and refusing to run without a
// decoder would leave the trigger switched off on exactly the deployments that
// cannot afford a GPU.
type Observed struct {
	// CodedBytes is the picture's size in the elementary stream, start codes
	// included or not as the caller prefers. It is the only field a detector
	// needs when the stream has not been decoded.
	CodedBytes int
	KeyFrame   bool
	Time       time.Time
	Luma       *Luma
}

// Reading is one detector output: a score, the threshold it was compared
// against, and a reason in words.
//
// There is no field here for a verdict, and that is the design rather than an
// omission. See the package comment.
type Reading struct {
	Detector  string
	Score     float64
	Threshold float64
	// Reason is a sentence an operator can read, naming the measurement rather
	// than the conclusion. It is a fixed vocabulary, not generated prose: a
	// reason that varies per event cannot be counted, and a count is how a
	// false-positive rate is computed.
	Reason string
	// Triggered is "this frame is worth spending a query on". It is not an
	// incident and it is not a detection.
	Triggered bool
}

// Detector is the trigger interface.
type Detector interface {
	// Name identifies the implementation, and travels onto every reading, so a
	// score can be traced to the thing that produced it.
	Name() string
	// Observe returns a reading for one picture.
	Observe(o Observed) (Reading, error)
	// Reset clears per-stream state. It exists because a Detector holds a
	// baseline derived from the stream it is watching, and a baseline carried
	// across a camera change measures the new camera against the old one's.
	Reset()
}

func clamp01(v float64) float64 {
	switch {
	case math.IsNaN(v) || v < 0:
		return 0
	case v > 1:
		return 1
	}
	return v
}

// Decimate block-averages a luma plane down by an integer factor.
//
// A 4K stream is 8.3 million pixels a frame, and a novelty score does not
// improve past a few hundred thousand. Block averaging rather than subsampling
// is the point: subsampling throws away exactly the low-contrast changes - a
// shadow edge crossing a car - that the detector is looking for, while averaging
// keeps them as a difference in mean luminance.
func Decimate(l Luma, factor int) (Luma, error) {
	if factor < 1 {
		return Luma{}, fmt.Errorf("detector: decimation factor %d is not at least 1", factor)
	}
	if !l.Valid() {
		return Luma{}, errors.New("detector: luma plane is not readable")
	}
	if factor == 1 {
		return l, nil
	}
	w := l.Width / factor
	h := l.Height / factor
	if w < 1 || h < 1 {
		return Luma{}, fmt.Errorf("detector: %dx%d cannot be decimated by %d", l.Width, l.Height, factor)
	}
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum := 0
			for dy := 0; dy < factor; dy++ {
				for dx := 0; dx < factor; dx++ {
					sum += int(l.Plane[(y*factor+dy)*l.Stride+x*factor+dx])
				}
			}
			// Saturate rather than truncate. A white block averaged into a
			// black frame is 255, and the arithmetic mean of 16 white pixels is
			// 255 — but a wider factor puts the numerator past 255 * factor^2
			// for a mean that is still in range, and byte() would then wrap a
			// bright average into a dark one. A dark frame that reads as light
			// produces novelty every frame, which is the failure this detector
			// exists to avoid.
			mean := sum / (factor * factor)
			if mean > 255 {
				mean = 255
			}
			out[y*w+x] = byte(mean) //nolint:gosec // clamped to a byte above
		}
	}
	return Luma{Width: w, Height: h, Stride: w, Plane: out}, nil
}
