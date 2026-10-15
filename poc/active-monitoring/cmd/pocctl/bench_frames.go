package main

import (
	"fmt"

	"github.com/Sergey-Bar/vision-poc/internal/ortdetect"
)

// A frame's outcome in the benchmark, and the line that prints it.
//
// This is a function of the ANSWER rather than of the loop for one reason: the
// loop cannot see the difference between the two kinds of zero. `Detect` returns
// its refusal inside the Answer — `Boxes` is empty either way — so a caller that
// looks only at `len(ans.Boxes)` reads a frame the decoder could not decode as a
// frame where it found nothing. That is what happened here with the committed
// yolov4n.onnx: every frame of the still set came back "multi-scale ANCHORED
// head, the in-process decoder reads only fused outputs", and the benchmark
// printed `negative frame, nothing drawn, nothing there` for all of them.
//
// Zero from a search is a measurement. Zero from a refusal is the absence of
// one, and the two printings are the difference between a benchmark that can be
// believed and one that cannot.

// Outcome is one frame's classification.
type Outcome int

const (
	// OutcomeMeasured means the decoder answered and the metrics stand.
	OutcomeMeasured Outcome = iota
	// OutcomeRefused means the decoder refused the frame, by name.
	OutcomeRefused
	// OutcomeNegative means the decoder answered, found nothing, and the ground
	// truth also held nothing: the one combination that is unambiguously right.
	OutcomeNegative
)

// classifyAnswer names one frame's outcome.
//
// The refusal check is FIRST and it is the control. An Answer whose State is
// `degraded` or `not_asked` is also not a measurement, and it is classified as
// refused too — a frame nobody answered for is not a frame with nothing in it,
// which is invariant 3 seen from the one place the benchmark turns an answer
// into a number. `CannotTell` is deliberately absent: this decoder produces that
// condition as a [Refusal], and the absence states it shares with the sidecar
// are documented on the package.
func classifyAnswer(ans ortdetect.Answer) (Outcome, string) {
	if ans.Refusal != nil {
		return OutcomeRefused, fmt.Sprintf("%s (state %s)", ans.Refusal.What, orEmpty(ans.State))
	}
	if ans.State == ortdetect.StateDegraded || ans.State == ortdetect.StateNotAsked {
		return OutcomeRefused, fmt.Sprintf("the detector reported the state %q and no refusal; a frame nobody answered for is not a frame with nothing in it", orEmpty(ans.State))
	}
	if len(ans.Boxes) == 0 {
		return OutcomeNegative, "negative frame, nothing drawn, nothing there"
	}
	return OutcomeMeasured, ""
}

func orEmpty(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
