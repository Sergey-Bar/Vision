package main

import ort "github.com/Sergey-Bar/vision/shared/ortdetect"

// The answers under test, built by helpers so the body of a test reads as the
// claim it is making. The package is imported under a short alias for one
// reason: the long name is easy to mistype and a mistyped identifier inside a
// helper is one compile error instead of four inside assertions, where it reads
// as a variable named after a package.

// detAnswerDegraded is a frame the decoder refused, with the refusal's own
// words: the committed yolov4n.onnx is a multi-scale ANCHORED head and the
// in-process decoder reads only fused outputs.
func detAnswerDegraded() ort.Answer {
	return ort.Answer{
		State: ort.StateDegraded,
		Refusal: &ort.Refusal{
			What: "this export is a multi-scale ANCHORED head and the in-process decoder reads only fused outputs",
		},
	}
}

// detAnswerNoObjects is a frame the decoder answered and found nothing in.
func detAnswerNoObjects() ort.Answer {
	return ort.Answer{State: ort.StateNoObjects}
}

// detStateDegraded and detStateNotAsked are the two states that mean the frame
// was not measured.
func detStateDegraded() string { return ort.StateDegraded }
func detStateNotAsked() string { return ort.StateNotAsked }

// detAnswerWithState is an Answer carrying only the state word.
func detAnswerWithState(state string) ort.Answer {
	return ort.Answer{State: state}
}

// detAnswerWithBoxes is a frame with real detections in it.
func detAnswerWithBoxes() ort.Answer {
	return ort.Answer{
		State: ort.StateNoObjects,
		Boxes: []ort.Box{{Class: "person", Score: 0.9}},
	}
}
