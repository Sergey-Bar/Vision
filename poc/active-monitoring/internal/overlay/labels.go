package overlay

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
)

// The box label and the footer strip.
//
// # What a label has to carry, and why each piece is here
//
// With a novelty trigger a box's class is `motion` on every frame and its score is
// a changed-fraction, so the picture could say almost nothing about it. With a real
// detector the picture can say everything, and this is where it says it:
//
//	class   the MODEL'S word, which is the only word anybody can act on. Drawn in
//	        the COCO name rather than the family, because "vehicle" does not tell an
//	        operator whether to open a ticket about a bus.
//	score   the model's confidence, to two decimals. A number the operator can
//	        compare against the approval bar they set, which is the question they
//	        actually have when a box looks wrong.
//	track   the tracker's identity. A track id that changes every frame makes a
//	        viewer distrust the tracker; a visible, stable one is evidence for it.
//
// # Why the footer exists at all
//
// The footer is where the numbers a report would print go, and the reason it is on
// the PICTURE is the same reason the verdict is: a judge, a customer and a ticket
// all get a PNG. A screenshot that shows boxes and omits "det_p99 = 61ms against a
// 30ms frame interval" is a screenshot that makes a system that cannot hold its rate
// look like one that can.
//
// And the three-way distinction in the footer is the absence rule applied to a
// live picture: a measured frame says so, a frame the pipeline never asked about
// says THAT, and a frame the model was asked about and could not answer says the
// third thing. One picture, three states, none of them printed as zero.

// Footer is the strip across the bottom of the picture.
//
// It is a struct and not eleven parameters because the caller assembles it from
// four places — the detector's telemetry, the run's counters, the benchmark's rates
// and the box log's arithmetic — and a signature with eleven arguments is a
// signature somebody gets two of them the wrong way round.
type Footer struct {
	// Detector is the model's own description: what it is, and at what cost. It is
	// EMPTY rather than a placeholder when no model ran, and an empty field prints
	// nothing — a footer reading "model: —" is a claim that a model was configured
	// and none was found.
	Detector string
	// Latency is the measured cost and the rate it supports. "61.2ms p99 / 16.3 fps"
	// is a fact; "fast" is not.
	Latency string
	// FPS is the decode rate, and DeclaredRate is what the configuration asked for.
	// Printing both is what makes backpressure visible: a decode rate well under
	// the declared detection rate is the signature of a detector that cannot keep
	// up, and it appears as a number rather than as a report that says AC1 failed.
	FPS, DeclaredRate string
	// Boxes is the run's own count, and Cut says how many the picture's cap left off.
	Boxes, Cut string
	// BoxLog is the per-frame log's arithmetic, and Sampled is the flag that says a
	// replay would be reading an incomplete log.
	BoxLog  string
	Sampled bool
	// Bench is the benchmark's rates, and it is OPTIONAL in a way the others are
	// not: a run with no marking session has no false-positive rate, and printing a
	// zero for one would be this module's law inverted.
	Bench string
	// Benched says whether a benchmark happened at all, for the same reason.
	Benched bool
}

// drawFooter paints the strip.
//
// It is drawn LAST so it sits over everything, and it is drawn only when the frame
// is tall enough for it — a strip that covers a third of a short picture is worse
// than no strip, and the boxes are what the picture is for.
func drawFooter(img *image.RGBA, s Spec, f Footer) {
	if f.empty() {
		return
	}
	b := img.Bounds()
	lines := f.lines()
	const stripH = 15
	if b.Dy() < stripH*len(lines)+8 {
		return
	}
	y := b.Max.Y - stripH*len(lines) - 2
	draw.Draw(img, image.Rect(b.Min.X, y-1, b.Max.X, b.Max.Y),
		&image.Uniform{headerColour}, image.Point{}, draw.Src)
	for _, line := range lines {
		labelBarAt(img, b.Min.X+4, y+stripH-4, line, footerColour(line))
		y += stripH
	}
}

// empty reports whether there is anything to say, so a run with no detector, no
// benchmark and no note costs no pixels.
func (f Footer) empty() bool {
	return f.Detector == "" && f.Latency == "" && f.Boxes == "" &&
		f.Bench == "" && f.Sampled == false
}

// lines renders the footer, in the order a reader wants them: what the model is,
// what it costs, what it found, and what the run may not claim.
func (f Footer) lines() []string {
	var out []string
	if f.Detector != "" {
		out = append(out, "model "+f.Detector)
	}
	if f.Latency != "" {
		cost := "cost " + f.Latency
		if f.FPS != "" {
			cost += "   decode " + f.FPS
		}
		if f.DeclaredRate != "" {
			cost += " of " + f.DeclaredRate + " requested"
		}
		out = append(out, cost)
	}
	if f.Boxes != "" {
		line := "boxes " + f.Boxes
		if f.Cut != "" {
			line += "   cut " + f.Cut
		}
		out = append(out, line)
	}
	if f.BoxLog != "" {
		line := "box log " + f.BoxLog
		if f.Sampled {
			// The flag is in WORDS on the picture, not as a value. A run whose
			// replay reads a sampled log is making a weaker claim than one whose
			// replay reads everything, and the picture is where somebody finds that
			// out.
			line += "   SAMPLED — a replay over this log is not a replay over every frame"
		}
		out = append(out, line)
	}
	if f.Benched && f.Bench != "" {
		out = append(out, "bench "+f.Bench)
	}
	return out
}

// footerColour is per-line, and the distinctions it makes are the ones a reader
// must not have to infer:
//
//   - a SAMPLED box log is in the warning colour, because a weaker claim printed in
//     the same colour as a stronger one is a weaker claim nobody notices;
//   - everything else is the ordinary foreground.
func footerColour(line string) color.RGBA {
	if strings.Contains(line, "SAMPLED") {
		return warningColour
	}
	if strings.Contains(line, "bench ") {
		return benchColour
	}
	return noteColour
}

// The three colours the footer adds.
var (
	// warningColour is for a claim that is qualified. It is deliberately NOT red:
	// this is not a fault, it is a limitation, and a limitation in red reads as a
	// fault and gets the wrong response.
	warningColour = color.RGBA{210, 170, 90, 255}
	// benchColour separates measured rates from the run's own arithmetic, so a
	// reader can tell at a glance which numbers came from a benchmark and which
	// came from this run.
	benchColour = color.RGBA{120, 200, 160, 255}
)

// DetectorFooter builds the model and cost lines from the detector's own report.
//
// It takes the pieces rather than the detector so that this package stays a leaf
// and a snapshot can be drawn from a stored run with no session present — which is
// the case that matters, because a snapshot of a finished run is the artefact
// somebody looks at afterwards.
func DetectorFooter(model, sha, input string, p50, p95, p99 float64, haveLatency bool,
	fps, declared string) Footer {
	f := Footer{}
	if model != "" {
		desc := model
		if input != "" {
			desc += " " + input
		}
		if sha != "" {
			desc += " sha256:" + sha[:minInt(12, len(sha))]
		}
		f.Detector = desc
	}
	if haveLatency {
		f.Latency = fmt.Sprintf("p50 %.1fms  p95 %.1fms  p99 %.1fms  ->  %.1f fps",
			p50, p95, p99, achievableFPS(p99))
	}
	f.FPS = fps
	f.DeclaredRate = declared
	return f
}

// DetectorOK reports whether a model was named, for a caller asserting that the
// model line is present rather than diffing the rendered text.
//
// It exists because the alternative is a test that searches a string for a
// substring it also uses as a label, and a substring assertion on rendered text is
// a test that passes when the text happens to be empty.
func (f Footer) DetectorOK() bool { return f.Detector != "" }

// achievableFPS is 1000/p99 and it refuses a non-positive p99.
//
// A p99 of zero is a clock that did not run, and 1000/0 is an infinity that would
// be printed on the picture as though it were a measurement. Same rule as the
// preflight gate's, and it is duplicated here on purpose: this package is a leaf
// and cannot import ortdetect, and a footer that printed an infinity would be the
// one place in the module where an absent measurement became an impressive number.
func achievableFPS(p99 float64) float64 {
	if p99 <= 0 {
		return 0
	}
	return 1000 / p99
}
