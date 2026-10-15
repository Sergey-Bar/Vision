package main

import (
	"strings"
	"testing"
)

// TestARefusedFrameIsNotANegativeOne is the control the loop above used to lack,
// and it was not a test anybody could have written: the classification lived
// inline in the metrics loop, so the only way to exercise it was to run a model
// nobody on a clean checkout has.
//
// The defect it catches, measured on this machine with the committed
// yolov4n.onnx: every frame of the still set came back from Detect with a
// refusal inside the Answer and Boxes empty - the pinned Model Zoo export is a
// multi-scale ANCHORED head and the in-process decoder reads only fused
// outputs - and the benchmark printed
// "negative frame, nothing drawn, nothing there" for all of them. A reader would
// have taken that as a clean measurement of a detector that found nothing, when
// the truth was a detector that could not look.
func TestARefusedFrameIsNotANegativeOne(t *testing.T) {
	// The two zeros. The refusal carries the named reason the decoder produced,
	// which is what makes a zero diagnosable instead of merely observable.
	refused := detAnswerDegraded()
	clean := detAnswerNoObjects()

	outcome, line := classifyAnswer(refused)
	if outcome != OutcomeRefused {
		t.Fatalf("a frame the decoder refused classified as %d, not OutcomeRefused", outcome)
	}
	if !strings.Contains(line, "ANCHORED") {
		t.Fatalf("the refused frame's line does not carry the decoder's own words: %q", line)
	}

	outcome, line = classifyAnswer(clean)
	if outcome != OutcomeNegative {
		t.Fatalf("an answered frame with nothing in it classified as %d, not OutcomeNegative", outcome)
	}
	if !strings.Contains(line, "nothing drawn, nothing there") {
		t.Fatalf("the negative frame's line lost its sentence: %q", line)
	}
}

// TestAFrameTheDetectorDidNotAnswerForIsRefused: the state word and the refusal
// are set together by the decoder, so an Answer carrying only the degraded or
// not_asked state - which is what a future decoder path that answered
// no_objects wrongly would look like from here - must not be read as a
// measurement either. Invariant 3's honesty, in the one place the benchmark
// turns an answer into a number.
func TestAFrameTheDetectorDidNotAnswerForIsRefused(t *testing.T) {
	for _, state := range []string{detStateDegraded(), detStateNotAsked()} {
		outcome, line := classifyAnswer(detAnswerWithState(state))
		if outcome != OutcomeRefused {
			t.Errorf("state %q classified as %d, not OutcomeRefused: %q", state, outcome, line)
		}
	}
}

// TestAMeasuredFrameKeepsItsBoxes is the third branch, asserted because the
// first two are the ones with the defect: a classification that swallowed a
// frame with real boxes would be the same bug pointed at the opposite direction.
func TestAMeasuredFrameKeepsItsBoxes(t *testing.T) {
	outcome, line := classifyAnswer(detAnswerWithBoxes())
	if outcome != OutcomeMeasured {
		t.Fatalf("an answered frame classified as %d, not OutcomeMeasured: %q", outcome, line)
	}
}
