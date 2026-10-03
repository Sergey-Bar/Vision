package overlay

import (
	"image"
	"math"
	"strings"
	"testing"
)

// The footer strip: what model ran, what it cost, what it found, and what the run
// may not claim.
//
// It exists because a judge, a customer and a ticket all get a PNG. A screenshot
// showing boxes and omitting the throughput is a screenshot that makes a system
// which cannot hold its frame rate look like one that can.

// TestTheFooterIsEmptyWhenThereIsNothingToSay, because a run with no detector
// costs no pixels. An overlay that draws a strip to say "no model" is drawing a
// claim about a component that was not configured.
//
// The measurement is DIFFERENTIAL and that is not a convenience: `drawHeader`
// paints its bar before it checks whether it has any text, so a Spec with nothing
// set is not a blank picture. Asserting "the frame is black" would be testing the
// header, and would pass for the wrong reason the day the header changed.
func TestTheFooterIsEmptyWhenThereIsNothingToSay(t *testing.T) {
	plain, withFooter := newFrame(320, 240), newFrame(320, 240)
	Draw(plain, Spec{})
	Draw(withFooter, Spec{Footer: Footer{Detector: "yolov4n"}})

	if countNonBlack(withFooter) <= countNonBlack(plain) {
		t.Fatalf("a footer with a model line drew %d pixels over %d — nothing was drawn for it",
			countNonBlack(withFooter), countNonBlack(plain))
	}
	if !(Footer{}).empty() {
		t.Fatal("a zero Footer is not empty")
	}
}

// TestTheFooterSaysWhatTheModelWasAndWhatItCost is the reason the strip is here at
// all.
func TestTheFooterSaysWhatTheModelWasAndWhatItCost(t *testing.T) {
	f := DetectorFooter("yolov4n", "abcdef0123456789abcdef", "640x640",
		38.2, 52.0, 61.2, true, "30.0 fps", "5 fps")
	lines := f.lines()
	if len(lines) < 2 {
		t.Fatalf("lines = %d, want at least 2", len(lines))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"yolov4n",             // the model
		"640x640",             // the input it ran at
		"sha256:abcdef012345", // the hash, abbreviated to something a person reads
		"p50 38.2ms", "p95 52.0ms", "p99 61.2ms",
		"16.3 fps", // what the p99 supports
		"decode 30.0 fps of 5 fps requested",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the footer does not contain %q:\n%s", want, joined)
		}
	}
}

// TestTheSampledFlagIsInWordsNotAsAValue, because a run whose replay reads a
// sampled log is making a WEAKER claim than one whose replay reads everything, and
// a weaker claim printed in the same colour as a stronger one is a weaker claim
// nobody notices.
func TestTheSampledFlagIsInWordsNotAsAValue(t *testing.T) {
	f := Footer{BoxLog: "20000 seen 20000 kept", Sampled: true}
	joined := strings.Join(f.lines(), "\n")
	if !strings.Contains(joined, "SAMPLED") {
		t.Fatalf("a sampled log is not flagged:\n%s", joined)
	}
	if !strings.Contains(joined, "not a replay over every frame") {
		t.Errorf("the flag does not say what it means for the replay:\n%s", joined)
	}
	if got := footerColour("box log 20000  SAMPLED — x"); got != warningColour {
		t.Errorf("a sampled line is not in the warning colour")
	}
	// And it is NOT red: this is a limitation, not a fault, and a limitation in red
	// reads as a fault and gets the wrong response.
	if warningColour.R == 248 && warningColour.G == 81 && warningColour.B == 73 {
		t.Error("the warning colour is the fault colour; a sampled log is a limitation")
	}

	f.Sampled = false
	if strings.Contains(strings.Join(f.lines(), "\n"), "SAMPLED") {
		t.Error("a COMPLETE log was flagged as sampled; a reader would be sent to distrust a " +
			"replay that has no reason to be doubted")
	}
}

// TestANoModelFooterPrintsNoModelLine is the absence rule on the picture.
func TestANoModelFooterPrintsNoModelLine(t *testing.T) {
	f := DetectorFooter("", "", "", 0, 0, 0, false, "", "")
	joined := strings.Join(f.lines(), "\n")
	if joined != "" {
		t.Fatalf("a run with no detector produced footer lines:\n%s", joined)
	}
	// And a dash is NOT printed for it: `model: —` is a claim that a model was
	// configured and none was found.
	if strings.Contains(joined, "model:") || strings.Contains(joined, "—") {
		t.Error("the footer printed a placeholder for a component that was not configured")
	}
}

// TestAP99OfZeroDoesNotBecomeAnInfinity is the property most likely to be got wrong,
// because the arithmetic is one division and the consequence is a big impressive
// number on a picture.
func TestAP99OfZeroDoesNotBecomeAnInfinity(t *testing.T) {
	if got := achievableFPS(0); got != 0 {
		t.Fatalf("achievableFPS(0) = %v; a p99 of zero is a clock that did not run, and "+
			"1000/0 is an infinity a reader would take for a measurement", got)
	}
	if got := achievableFPS(-1); got != 0 {
		t.Fatalf("achievableFPS(-1) = %v", got)
	}
	if got := achievableFPS(50); math.Abs(got-20) > 1e-9 {
		t.Fatalf("achievableFPS(50) = %v, want 20", got)
	}

	// And the footer built from an unmeasured latency says nothing about cost,
	// rather than printing a cost of zero milliseconds.
	f := DetectorFooter("m", "abcdef0123456789", "640", 0, 0, 0, false, "5 fps", "")
	if strings.Contains(strings.Join(f.lines(), "\n"), "p99") {
		t.Fatal("an unmeasured latency was printed as a number")
	}
	if !f.DetectorOK() {
		t.Error("the model line is missing even though a model was named")
	}
}

// TestTheBenchLineOnlyAppearsWithABenchmark, for the same reason as the model line.
func TestTheBenchLineOnlyAppearsWithABenchmark(t *testing.T) {
	if (Footer{Bench: "0.800"}).empty() {
		t.Fatal("a footer carrying a bench string reports itself empty")
	}
	// A rate STRING is not enough. The `Benched` flag is what says a benchmark
	// HAPPENED, and a caller who assembles a rate and forgets the flag gets nothing
	// printed rather than a number with no measurement behind it — which is the
	// right way round, because the flag is the only part a reader could not infer
	// from the string itself.
	if got := strings.Join((Footer{Bench: "0.800"}).lines(), "\n"); got != "" {
		t.Errorf("a rate string with no benchmark printed:\n%s", got)
	}
	if got := strings.Join((Footer{Bench: "precision 0.800 recall 0.750", Benched: true}).lines(), "\n"); !strings.Contains(got, "bench") {
		t.Errorf("the bench line is missing: %q", got)
	}
	// A rate with no denominator is NOT printed as zero. The caller's job is not to
	// pass one and this package's job is not to invent one.
	if got := strings.Join((Footer{}).lines(), "\n"); strings.Contains(got, "0.000") {
		t.Errorf("an absent benchmark printed a rate of zero: %q", got)
	}
}

// TestTheFooterIsSkippedOnAFrameTooShortToHoldIt, because a strip that covers a
// third of a short picture is worse than no strip — the boxes are what the picture
// is for.
//
// Differential again, for the same reason as the test above: the header bar is
// painted on every frame whether or not it has text, so the question is whether
// the footer ADDED anything, not whether the picture is blank.
func TestTheFooterIsSkippedOnAFrameTooShortToHoldIt(t *testing.T) {
	full := Footer{Detector: "m", Latency: "p99 1ms", Boxes: "3", BoxLog: "10 seen", Sampled: true}
	need := 15*len(full.lines()) + 8

	for _, dy := range []int{8, 20, need - 1} {
		plain, withFooter := newFrame(320, dy), newFrame(320, dy)
		Draw(plain, Spec{})
		Draw(withFooter, Spec{Footer: full})
		if countNonBlack(withFooter) != countNonBlack(plain) {
			t.Errorf("a %d-pixel-tall frame drew a footer that needs %d pixels of strip", dy, need)
		}
	}

	// And it IS drawn when there is room.
	plain, withFooter := newFrame(320, need+20), newFrame(320, need+20)
	Draw(plain, Spec{})
	Draw(withFooter, Spec{Footer: full})
	if countNonBlack(withFooter) <= countNonBlack(plain) {
		t.Errorf("a frame with room for a %d-pixel strip drew none", need)
	}
}

// TestTheFooterSurvivesADrawThatWouldPanicOnABadSpec, because an overlay that can
// take the pipeline down is worse than one that says nothing.
func TestTheFooterSurvivesADrawThatWouldPanicOnABadSpec(t *testing.T) {
	for _, tc := range []Spec{
		{Footer: Footer{Detector: "m"}},
		{Footer: Footer{Latency: "p99 0ms"}},
		{Footer: Footer{Bench: "x", Benched: true}},
		{Boxes: []TrackedBox{{Box: Box{X1: 5, Y1: 5, X2: 0.1, Y2: 0.1}}}, Footer: Footer{Boxes: "1"}},
		{Boxes: []TrackedBox{{Box: Box{Class: "car", Score: 1, X1: -1, Y1: -1, X2: 2, Y2: 2}}},
			Footer: Footer{Detector: "m", Sampled: true}},
	} {
		img := newFrame(64, 48)
		Draw(img, tc)
		_ = img
	}
}

// newFrame is a black picture of a given size.
func newFrame(w, h int) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// countNonBlack counts pixels that are not pure black, which is how these tests
// assert that something was DRAWN. It is deliberately a count of the whole frame
// rather than of a region: the question is "did the overlay write anything", and a
// region would need the test to know where the overlay draws.
func countNonBlack(img *image.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			if c.R != 0 || c.G != 0 || c.B != 0 || c.A != 0 {
				n++
			}
		}
	}
	return n
}
