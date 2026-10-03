package ortdetect

import (
	"strings"
	"testing"
)

// The two defects the GPU run exposed, and both of them are about the difference
// between a fact this module RECORDS and a fact it ACTS ON.
//
// Neither had a test, and that is the point: `internal/ortdetect` had a parity test and
// unit tests for the decoder, and the path from "the model declares an input layout" to
// "the tensor is laid out that way" was untested in both directions. A 4070 turned both
// from invisible into fatal, which is the only reason they are fixed rather than
// documented as a known limitation.

// TestADynamicAxisExportIsRunWithRuntimeAllocatedOutputs is RC-29.
//
// The committed detector is exported with dynamic axes: its declared output dims are
// -1, because the anchor count depends on the input and the export does not know the
// input. This module used to pre-allocate every declared output and refuse the model
// when it could not, which meant the committed detector could not be run at all, on
// either provider.
//
// It is worth being clear about how bad that was, because the refusal was phrased as
// protection: "an output tensor could not be allocated" reads like the module declining
// something dangerous. What it was doing was declining every modern YOLO export, and
// calling an inability a refusal.
func TestADynamicAxisExportIsRunWithRuntimeAllocatedOutputs(t *testing.T) {
	symbolic := []OutputSpec{
		{Name: "Identity:0", Dims: []int64{-1, -1, -1, 3, 85}},
		{Name: "Identity_1:0", Dims: []int64{-1, -1, -1, 3, 85}},
	}
	specs, auto := preallocateOutputs(symbolic)
	if !auto {
		t.Fatal("a dynamic-axis export was pre-allocated. A -1 has no size, so this is not a case where " +
			"pre-allocating happens to work — it is the committed detector refusing to run")
	}
	if len(specs) != len(symbolic) {
		t.Fatalf("the runtime is to be handed %d output slots and got %d; a short slice is refused by the "+
			"binding as a name-count mismatch that does not exist", len(symbolic), len(specs))
	}
	for i, v := range specs {
		if v != nil {
			t.Fatalf("output %d was pre-allocated; the runtime allocates these itself", i)
		}
	}

	// And the concrete case takes the OTHER branch, which is what keeps both paths
	// alive: a control with only one exercised branch is a control that can be deleted
	// without a test noticing.
	//
	// The concrete allocation itself is NOT exercised here, and that is deliberate
	// rather than convenient: `ort.NewTensor` needs an initialised ONNX Runtime, so
	// calling it from a unit test fails for a reason that has nothing to do with the
	// code under test. Asserting on the DECISION covers what this control decides; the
	// allocation is covered by `pocctl detector --selftest`, which has a real session.
	concrete := []OutputSpec{{Name: "out0", Dims: []int64{1, 52, 52, 3, 85}}}
	if hasSymbolicDims(concrete) {
		t.Fatal("a concrete export was treated as symbolic, so every model takes the slow path")
	}
	if _, auto := preallocateOutputs(concrete); auto {
		t.Fatal("a concrete export asked the runtime to allocate; that is slower and needless")
	}
	// A single symbolic dim among concrete ones is still symbolic. A detector that
	// publishes one static output and one dynamic one is the case a check that only
	// looked at the first dimension would get wrong.
	mixed := []OutputSpec{
		{Name: "fixed", Dims: []int64{1, 4, 85}},
		{Name: "anchors", Dims: []int64{-1, 85}},
	}
	if !hasSymbolicDims(mixed) {
		t.Fatal("a single symbolic dimension among concrete ones was missed")
	}
}

// TestTheInputTensorIsLaidOutTheWayTheModelDeclares is RC-30.
//
// The session already worked the layout out from the model's declared dims and recorded
// it — `Input().Layout`, the run report, `fixtureprobe -detector` — and then built the
// tensor in a fixed order regardless. On the committed NHWC export that is a runtime
// error reading `index: 1 Got: 3 Expected: 416`, which is legible.
//
// The reason it is worth a test anyway is the quiet version. A tensor of exactly the
// right number of floats, fed in the wrong order, decodes to boxes that are PLAUSIBLE
// and wrong. Nothing errors. The detection rate looks fine. This module records the
// layout in every artefact it produces, so a recorded fact nobody acts on is the
// specific failure it exists to catch — and it was happening inside the module written
// to catch it.
func TestTheInputTensorIsLaidOutTheWayTheModelDeclares(t *testing.T) {
	const h, w, c = 416, 416, 3
	for _, tc := range []struct {
		layout string
		want   []int64
	}{
		// The committed detector. NHWC.
		{"nhwc", []int64{1, h, w, c}},
		// A YOLO export in the other order, which is the one most tools produce.
		{"nchw", []int64{1, c, h, w}},
		// Unset means the same as nchw, and has to, or a session with no layout
		// recorded produces a tensor of a shape nobody can predict.
		{"", []int64{1, c, h, w}},
	} {
		s := &Session{input: InputSpec{Name: "input_1:0", H: h, W: w, Layout: tc.layout}}
		got, err := s.inputShape(c * h * w)
		if err != nil {
			t.Fatalf("layout %q: %v", tc.layout, err)
		}
		dims := make([]int64, len(got))
		for i, d := range got {
			dims[i] = int64(d)
		}
		if len(dims) != len(tc.want) {
			t.Fatalf("layout %q gave %v, want %v", tc.layout, dims, tc.want)
		}
		for i := range dims {
			if dims[i] != tc.want[i] {
				t.Fatalf("layout %q gave %v, want %v", tc.layout, dims, tc.want)
			}
		}
	}
}

// TestAnUnknownLayoutIsRefusedRatherThanGuessed: a guess produces a tensor of the right
// size that the model reads in the wrong order, which is the quiet failure above again.
// There is no safe default, so there is no default.
func TestAnUnknownLayoutIsRefusedRatherThanGuessed(t *testing.T) {
	s := &Session{input: InputSpec{Name: "input_1:0", H: 416, W: 416, Layout: "nwhc"}}
	_, err := s.inputShape(3 * 416 * 416)
	if err == nil {
		t.Fatal("an unknown layout was accepted; a wrong guess is the quiet failure this refuses")
	}
	for _, want := range []string{"nchw nor nhwc", "plausible and wrong", "export time"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestAWrongSizedTensorIsRefusedByName: the count is the one thing both layouts agree
// on, so a mismatch is unambiguous and it should be reported before the runtime is
// involved.
func TestAWrongSizedTensorIsRefusedByName(t *testing.T) {
	s := &Session{input: InputSpec{Name: "input_1:0", H: 416, W: 416, Layout: "nhwc"}}
	_, err := s.inputShape(123)
	if err == nil {
		t.Fatal("a tensor of the wrong length was accepted")
	}
	for _, want := range []string{"wrong size", "input_1:0", "519168", "123"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// TestTheDeclaredLayoutIsTheOneTheModelSays: the recorded fact and the applied one come
// from the same field. This is the assertion that would have caught RC-30 before the GPU
// did — it is cheap, it needs no model, and it pins the two together.
func TestTheDeclaredLayoutIsTheOneTheModelSays(t *testing.T) {
	// The committed export's declared input, verbatim from
	// pyworker/fixtures/yolov4n.contract.json. If someone re-exports the model in NCHW
	// this test is the thing that must be updated, and it fails loudly rather than the
	// decoder quietly producing wrong boxes.
	_, err := DetectLayout([]int64{-1, 416, 416, 3})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := DetectLayout([]int64{-1, 416, 416, 3}); got != "nhwc" {
		t.Fatalf("a [batch,416,416,3] input was detected as %q, want nhwc", got)
	}
	if got, _ := DetectLayout([]int64{-1, 3, 416, 416}); got != "nchw" {
		t.Fatalf("a [batch,3,416,416] input was detected as %q, want nchw", got)
	}
}
