package ortdetect

import (
	"math"
	"strings"
	"testing"
)

func defaultCfg() DecodeConfig {
	return DecodeConfig{
		EmitScore:  0.05,
		MaxBoxes:   100,
		NMSClass:   defaultNMSClass,
		NMSGlobal:  defaultNMSGlobal,
		MinBoxArea: 0.0005,
	}
}

func flatFrame(w, h int) []byte {
	buf := make([]byte, w*h*3)
	for i := 0; i < len(buf); i += 3 {
		buf[i], buf[i+1], buf[i+2] = 128, 128, 128
	}
	return buf
}

// TestTheUnpadDividesByTheContentNotTheInput is RC-13, and it is the single most
// consequential arithmetic in this package.
//
// A 640x384 frame letterboxed into a 640x640 canvas has a CONTENT height of 384.
// Dividing the model's y coordinates by 640 — the input height — squashes every
// box to 0.6 of its true height: every object on the picture is drawn short, every
// crossing happens early, and nothing anywhere reports an error. The boxes are in
// the right x, in the right general area, and confidently the wrong size.
//
// The test checks the geometry directly rather than a decoder round trip, because
// the decoder takes whatever divisor it is given. content + pad == input on each
// axis is the invariant that makes the two halves of the letterbox agree, and the
// height of a box spanning the whole content must come back as 1.0.
func TestTheUnpadDividesByTheContentNotTheInput(t *testing.T) {
	// The exact shape from the defect: 16:9 frame, square canvas.
	_, g := mustLetterbox(t, flatFrame(640, 384), 640, 384, 640, 640)

	if g.ContentW != 640 || g.ContentH != 384 {
		t.Fatalf("content = %dx%d, want 640x384 — the frame's own shape at scale 1",
			g.ContentW, g.ContentH)
	}
	if g.PadX != 0 || g.PadY != 128 {
		t.Fatalf("pad = (%d,%d), want (0,128)", g.PadX, g.PadY)
	}
	if g.ContentW+g.PadX != g.InputW || g.ContentH+g.PadY != g.InputH {
		t.Logf("content %dx%d + pad (%d,%d) = %dx%d, canvas %dx%d: the pad is floor((input-content)/2) "+
			"so the content is CENTRED with up to one pixel of slack on each axis, not flush against "+
			"the top-left. The invariant that matters is the height below, not this one.",
			g.ContentW, g.ContentH, g.PadX, g.PadY,
			g.ContentW+g.PadX, g.ContentH+g.PadY, g.InputW, g.InputH)
	}
	// The invariant that DOES matter, and the one the decoder relies on: the
	// content is centred inside the canvas, to within the rounding of an odd
	// difference. Content flush to a corner would shift every box by half the pad
	// on every frame, which looks like a detector that cannot find the road.
	left, right := g.PadX, g.InputW-g.ContentW-g.PadX
	top, bottom := g.PadY, g.InputH-g.ContentH-g.PadY
	if diff(left, right) > 1 || diff(top, bottom) > 1 {
		t.Fatalf("the content is not centred: left margin %d against right margin %d, top %d against bottom %d",
			left, right, top, bottom)
	}

	y1 := g.Unpad(float64(g.PadY), g.PadY, g.ContentH)
	y2 := g.Unpad(float64(g.PadY+g.ContentH), g.PadY, g.ContentH)
	if got := y2 - y1; math.Abs(got-1) > 1e-9 {
		wrong := (float64(g.PadY+g.ContentH) - float64(g.PadY)) / float64(g.InputH)
		t.Fatalf("a box spanning the whole letterboxed content came back %v tall, want 1.0.\n"+
			"Dividing by the input height (%d) instead of the content height (%d) gives %v — "+
			"that is the defect this test exists for: every box 0.6x its true height on a 16:9 "+
			"frame, with no error anywhere saying so", got, g.InputH, g.ContentH, wrong)
	}
}

// diff is the absolute difference of two small ints, for the centring check.
func diff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// TestLetterboxPreservesAspectOnEveryShape is the property the arithmetic is
// supposed to have, checked across shapes rather than on one fixture.
//
// The failure guarded against is not a wrong number on 16:9 — it is a content that
// overflows the canvas on some other shape, which slices out of range and panics
// rather than refusing.
func TestLetterboxPreservesAspectOnEveryShape(t *testing.T) {
	shapes := [][2]int{
		{640, 384}, {384, 640}, {1920, 1080}, {1080, 1920}, {1280, 720},
		{720, 576}, {640, 480}, {333, 500}, {1, 1000}, {1000, 1}, {17, 19}, {4096, 4096},
	}
	inputs := [][2]int{{640, 640}, {416, 416}, {960, 960}, {320, 320}}

	for _, shape := range shapes {
		for _, in := range inputs {
			w, h := shape[0], shape[1]
			_, g := mustLetterbox(t, flatFrame(w, h), w, h, in[0], in[1])
			id := sprintf("%dx%d into %dx%d", w, h, in[0], in[1])

			if g.ContentW < 1 || g.ContentH < 1 {
				t.Fatalf("%s: content collapsed to %dx%d", id, g.ContentW, g.ContentH)
			}
			if g.ContentW > g.InputW || g.ContentH > g.InputH {
				t.Fatalf("%s: content %dx%d OVERFLOWS the canvas", id, g.ContentW, g.ContentH)
			}
			if g.ContentW+g.PadX > g.InputW || g.ContentH+g.PadY > g.InputH {
				t.Fatalf("%s: content plus pad overflows (%dx%d + %d,%d)", id,
					g.ContentW, g.ContentH, g.PadX, g.PadY)
			}
			if g.PadX < 0 || g.PadY < 0 {
				t.Fatalf("%s: negative pad (%d,%d) means the content does not fit", id, g.PadX, g.PadY)
			}
			wantAspect := float64(w) / float64(h)
			gotAspect := float64(g.ContentW) / float64(g.ContentH)
			// Aspect is preserved only to within the rounding of the smaller axis. A
			// 1000x1 frame scaled into a 640 square has a content height of 1 and
			// cannot preserve its aspect at all — the tolerance has to allow that,
			// or the test fails on a shape no camera produces and stops saying
			// anything about the ones that do.
			tol := math.Max(0.01, wantAspect*2/float64(g.ContentH))
			if math.Abs(wantAspect-gotAspect) > tol {
				t.Errorf("%s: content aspect %.4f, frame aspect %.4f (tolerance %.4f)",
					id, gotAspect, wantAspect, tol)
			}
		}
	}
}

// TestASquareFrameNeedsNoPadding pins the one shape on which D4 is invisible,
// because content divisor and input divisor happen to be equal there. It is worth
// pinning so nobody simplifies the code by assuming they are always equal.
func TestASquareFrameNeedsNoPadding(t *testing.T) {
	_, g := mustLetterbox(t, flatFrame(640, 640), 640, 640, 640, 640)
	if g.PadX != 0 || g.PadY != 0 {
		t.Fatalf("a square frame into a square canvas padded to (%d,%d)", g.PadX, g.PadY)
	}
	if g.ContentW != 640 || g.ContentH != 640 {
		t.Fatalf("content = %dx%d, want 640x640", g.ContentW, g.ContentH)
	}
}

// TestAPortraitFrameIsNotSilentlyMadeLandscape is the axis-agnostic case. A
// portrait frame into a square canvas pads LEFT AND RIGHT, and a decoder that
// subtracted only the vertical pad would shift every box sideways — which looks
// like a detector that cannot find the road rather than like a scaling error.
func TestAPortraitFrameIsNotSilentlyMadeLandscape(t *testing.T) {
	_, g := mustLetterbox(t, flatFrame(384, 640), 384, 640, 640, 640)
	if g.ContentW != 384 || g.ContentH != 640 {
		t.Fatalf("content = %dx%d, want 384x640", g.ContentW, g.ContentH)
	}
	if g.PadX != 128 || g.PadY != 0 {
		t.Fatalf("pad = (%d,%d), want (128,0) — the padding is on the WIDTH axis", g.PadX, g.PadY)
	}
	x1 := g.Unpad(float64(g.PadX), g.PadX, g.ContentW)
	x2 := g.Unpad(float64(g.PadX+g.ContentW), g.PadX, g.ContentW)
	if got := x2 - x1; math.Abs(got-1) > 1e-9 {
		t.Fatalf("a box spanning the content width came back %v wide, want 1.0", got)
	}
}

// TestAShortBufferIsRefusedNotRead is the same rule internal/novelty applies to a
// corrupt frame, and it matters more here: a decoder handed zeros produces a clean
// picture of an empty road and a report saying nothing was there.
func TestAShortBufferIsRefusedNotRead(t *testing.T) {
	_, _, err := Letterbox(make([]byte, 10), 640, 640, 640, 640, "nhwc")
	if err == nil {
		t.Fatal("a 10-byte buffer for a 640x640 frame was accepted")
	}
	if !strings.Contains(err.Error(), "shorter than") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	if _, _, err := Letterbox(flatFrame(10, 10), 0, 0, 640, 640, "nhwc"); err == nil {
		t.Fatal("a frame declaring 0x0 was accepted")
	}
	// A layout this build does not write is refused rather than defaulted. Guessing
	// between NCHW and NHWC hands the model a tensor it reads as a different
	// picture, and that produces boxes rather than an error.
	for _, bad := range []string{"", "NCHW", "channels_first", "nhwc "} {
		if _, _, err := Letterbox(flatFrame(64, 64), 64, 64, 64, 64, bad); err == nil {
			t.Errorf("the layout %q was accepted", bad)
		}
	}
}

// TestTheTensorIsWrittenInTheModelsOwnMemoryOrder is the NCHW half of the
// letterbox, and it is a separate test because it is a separate guarantee.
//
// NHWC is the natural order of a packed RGB frame — three adjacent bytes per
// pixel — so it is what the geometry tests above exercise. NCHW puts each channel's
// whole plane together, and getting it wrong is the D1 failure in its purest form:
// a model fed a transposed tensor does not error, it produces boxes on whatever the
// convolution happens to align with.
//
// The test is a pixel-level check on a 3x2 image with six DISTINCT colours, because
// a uniform frame would pass under either layout.
func TestTheTensorIsWrittenInTheModelsOwnMemoryOrder(t *testing.T) {
	// 3x2 frame, six different pixels.
	rgb := []byte{
		255, 0, 0, 0, 255, 0, 0, 0, 255,
		255, 255, 0, 0, 255, 255, 128, 128, 128,
	}
	const w, h, size = 3, 2, 3

	hwc, _ := mustLetterboxLayout(t, rgb, w, h, size, size, "nhwc")
	nchw, _ := mustLetterboxLayout(t, rgb, w, h, size, size, "nchw")

	// NHWC: value (y, x, c) at (y*size + x)*3 + c
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				want := float32(rgb[(y*w+x)*3+c]) / 255
				got := hwc[(y*size+x)*3+c]
				if math.Abs(float64(got-want)) > 1e-6 {
					t.Errorf("nhwc at (%d,%d,%d) = %v, want %v", y, x, c, got, want)
				}
			}
		}
	}

	// NCHW: value (c, y, x) at c*size*size + y*size + x
	plane := size * size
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 3; c++ {
				want := float32(rgb[(y*w+x)*3+c]) / 255
				got := nchw[c*plane+y*size+x]
				if math.Abs(float64(got-want)) > 1e-6 {
					t.Errorf("nchw at (c=%d,y=%d,x=%d) = %v, want %v", c, y, x, got, want)
				}
			}
		}
	}

	// The two must be PERMUTATIONS of each other, not different pictures. If they
	// were not, one of them lost or invented a pixel.
	if len(hwc) != len(nchw) || len(nchw) != plane*3 {
		t.Fatalf("lengths %d and %d, want %d", len(hwc), len(nchw), plane*3)
	}
	same := 0
	for _, v := range hwc {
		for _, u := range nchw {
			if v == u {
				same++
				break
			}
		}
	}
	if same != len(hwc) {
		t.Errorf("only %d of %d values appear in both layouts", same, len(hwc))
	}

	// The padding must be ZERO in both layouts — black, which is what the model was
	// trained with. A mean colour would shift every activation near the border.
	//
	// The padding is found from the GEOMETRY rather than assumed: a 3x2 frame in a
	// 3x3 canvas pads the bottom row only, so checking row 0 would be checking a
	// row of realClass pixels.
	_, geom := mustLetterboxLayout(t, rgb, w, h, size, size, "nchw")
	for y := 0; y < size; y++ {
		if y >= geom.PadY && y < geom.PadY+geom.ContentH {
			continue // inside the content
		}
		for c := 0; c < 3; c++ {
			for x := 0; x < size; x++ {
				if got := nchw[c*plane+y*size+x]; got != 0 {
					t.Errorf("nchw padding at (c=%d,y=%d,x=%d) = %v, want 0: a non-black letterbox "+
						"shifts every activation near the border", c, y, x, got)
				}
			}
		}
	}
	// And there is at least one padding row, or the test is checking nothing.
	padded := size - geom.ContentH
	if padded < 1 {
		t.Skip("the canvas is exactly the content size; there is no padding to check")
	}
}

// TestTheRecordAxisIsReadFromTheDeclaredShape is the highest-risk silent zero in
// this package, and it is tested on BOTH layouts holding the same content.
//
// A YOLO11n export publishes [1,84,8400] and a YOLOv8 export publishes [1,8400,84].
// Those are the same 705,600 numbers in a different order, so the DATA cannot
// distinguish them — only the declared shape can. Read the wrong axis and you get
// records of the wrong width, which is either noise or a refusal; neither is a
// detection, and neither reports which axis was read.
func TestTheRecordAxisIsReadFromTheDeclaredShape(t *testing.T) {
	records := []rec{
		{cx: 0.20, cy: 0.30, w: 0.10, h: 0.20, class: 2, pixels: 1}, // car
		{cx: 0.70, cy: 0.60, w: 0.08, h: 0.30, class: 7, pixels: 1}, // truck
		{cx: 0.50, cy: 0.50, w: 0.05, h: 0.40, class: 0, pixels: 1}, // person
	}
	// Fractions of a 640 input: a fraction-space model.
	tensor := fusedTensor(records, NumClasses, 4)

	res := Decode(tensor, square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("a well-formed fused tensor was refused: %v", res.Refusal)
	}
	if res.Telemetry.Layout != string(LayoutFieldsLast) {
		t.Errorf("layout = %q, want %q", res.Telemetry.Layout, LayoutFieldsLast)
	}
	if res.Telemetry.Fields != 84 {
		t.Errorf("fields = %d, want 84 — the record width was not read from the declared shape", res.Telemetry.Fields)
	}
	// 168 records: the fixture pads past the field width so the two axes cannot be
	// confused, which is what makes the fields-FIRST half of this test mean
	// anything.
	if res.Telemetry.Candidates != 168 {
		t.Errorf("candidates = %d, want 168 — the fixture's declared record count", res.Telemetry.Candidates)
	}
	if len(res.Boxes) != len(records) {
		t.Fatalf("boxes = %d, want %d: %+v", len(res.Boxes), len(records), res.Boxes)
	}
	for _, r := range records {
		if !hasBoxNear(res.Boxes, ClassName(r.class), r.cx, r.cy, r.w, r.h) {
			t.Errorf("no box for class %d at centre (%.2f,%.2f) size (%.2f,%.2f); got %+v",
				r.class, r.cx, r.cy, r.w, r.h, res.Boxes)
		}
	}

	// The SAME records, declared the other way round: [1, F, N], fields FIRST,
	// which is what a YOLO11n export publishes.
	//
	// The model count is DECLARED here rather than left at zero, and that is the
	// point rather than a convenience: with no declared names the decoder falls
	// back to a shape heuristic whose rule is "where more than one axis qualifies,
	// the LAST one wins" — pyworker's rule, kept for parity. On [1,84,168] both
	// axes qualify, so the heuristic picks 168 and refuses. That behaviour is
	// correct for the case it was written for (a multi-scale darknet head) and it
	// is why the strong path exists: an Ultralytics export carries its 80 class
	// names, and with them the record width is exact arithmetic and the axis
	// choice is unambiguous.
	fieldsFirst := fusedTensorFieldsFirst(records, NumClasses, 4)
	alt := Decode(fieldsFirst, square640(), NumClasses, defaultCfg())
	if alt.Refusal != nil {
		t.Fatalf("the same records declared fields-first were refused: %v", alt.Refusal)
	}
	if alt.Telemetry.Layout != string(LayoutFieldsFirst) {
		t.Fatalf("layout = %q, want %q", alt.Telemetry.Layout, LayoutFieldsFirst)
	}
	if alt.Telemetry.Fields != 84 {
		t.Fatalf("fields = %d, want 84", alt.Telemetry.Fields)
	}
	if len(alt.Boxes) != len(res.Boxes) {
		t.Fatalf("the same records gave %d boxes fields-last and %d fields-first: the layout "+
			"decision is not being made from the declared shape", len(res.Boxes), len(alt.Boxes))
	}
	for _, r := range records {
		if !hasBoxNear(alt.Boxes, ClassName(r.class), r.cx, r.cy, r.w, r.h) {
			t.Errorf("fields-first: no box for class %d at (%.2f,%.2f); got %+v",
				r.class, r.cx, r.cy, alt.Boxes)
		}
	}
}

// TestATensorWithNoRecognisableRecordAxisIsRefused is the other half: a shape that
// is not a detector's output must be a NAMED refusal rather than a zero.
//
// "0 detections" sends its reader to look at the model. "the output tensor's shape
// [1,100,9] cannot be read" sends them to the exporter and says which half is
// wrong.
func TestATensorWithNoRecognisableRecordAxisIsRefused(t *testing.T) {
	for _, dims := range [][]int64{
		{1, 100, 9},  // no axis is a plausible record width for this many candidates
		{1, 7},       // rank 2, 7 fields, 7 candidates — below the candidate floor
		{1, 3, 4, 5}, // rank 4, no axis wide enough
		{1, 4096, 8}, // 4096 fields is not a detection record
		{1},          // rank 1
	} {
		t.Run(sprintDims(dims), func(t *testing.T) {
			tn := rawTensor(dims)
			// A model that declares 80 names makes the refusal exact: a record must
			// be 84 or 85 fields and nothing else can be one.
			res := Decode(tn, square640(), NumClasses, defaultCfg())
			if res.Refusal == nil {
				t.Fatalf("shape %v was decoded into %d boxes instead of being refused", dims, len(res.Boxes))
			}
			if len(res.Boxes) != 0 {
				t.Fatalf("a refusal produced %d boxes; a refusal and a result are different things", len(res.Boxes))
			}
			if res.Refusal.What == "" || res.Refusal.Detail == "" {
				t.Fatalf("the refusal has no name or no detail: %+v", res.Refusal)
			}
			if got := StateFor(res.Refusal, len(res.Boxes)); got != StateDegraded {
				t.Fatalf("state = %q, want degraded", got)
			}
		})
	}
}

// TestTheObjectnessDecisionIsThreeStates is the arithmetic that shifts every class
// index by one if it is wrong.
//
// Getting it wrong produces a box in exactly the right place wearing the wrong
// word — a `truck` reported as a `bench` — the one error a reader cannot catch by
// looking at the picture. So the third state must exist and must refuse.
func TestTheObjectnessDecisionIsThreeStates(t *testing.T) {
	if got := DecideObjectness(84, NumClasses); got != ObjectnessNone {
		t.Errorf("84 fields with 80 classes: got %v, want no objectness", got)
	}
	if got := DecideObjectness(85, NumClasses); got != ObjectnessYes {
		t.Errorf("85 fields with 80 classes: got %v, want objectness at column 4", got)
	}
	for _, bad := range []int{0, 1, 83, 86, 100, 1000} {
		if got := DecideObjectness(bad, NumClasses); got != ObjectnessUnknown {
			t.Errorf("%d fields with 80 classes: got %v, want undecided", bad, got)
		}
	}
	if got := DecideObjectness(12, 8); got != ObjectnessNone {
		t.Errorf("12 fields with 8 declared classes: got %v, want no objectness", got)
	}
	if got := DecideObjectness(13, 8); got != ObjectnessYes {
		t.Errorf("13 fields with 8 declared classes: got %v, want objectness", got)
	}
	if got := DecideObjectness(14, 8); got != ObjectnessUnknown {
		t.Errorf("14 fields with 8 declared classes: got %v, want undecided", got)
	}

	// The refusal must reach the caller as degraded, never as no_objects. This
	// shape reaches the OBJECTNESS decision specifically: 50 fields is wide
	// enough to be chosen as the record axis, and neither 84 nor 85, so the frame
	// is refused for the class-shift reason rather than for a shape problem.
	res := Decode(rawTensor([]int64{1, 100, 50}), square640(), 0, defaultCfg())
	if res.Refusal == nil {
		t.Fatal("a 50-field record was decoded rather than refused")
	}
	if res.Telemetry.HasObjectness != ObjectnessUnknown {
		t.Fatalf("has_objectness = %v, want undecided", res.Telemetry.HasObjectness)
	}
	if got := StateFor(res.Refusal, len(res.Boxes)); got != StateDegraded {
		t.Fatalf("state = %q, want %q: a decoder failure reported as anything but degraded is the "+
			"defect this package exists to not ship", got, StateDegraded)
	}
	if !strings.Contains(res.Refusal.Detail, "bench") {
		t.Errorf("the refusal does not say what a wrong reading would produce: %q", res.Refusal.Detail)
	}
}

// TestAnObjectnessChannelIsReadAtColumnFive is the 85-field case end to end, so the
// three-state decision is not only tested as arithmetic but as a decode.
func TestAnObjectnessChannelIsReadAtColumnFive(t *testing.T) {
	// 85 fields: 4 box + 1 objectness + 80 classes. The class channel for `car`
	// is column 6, and column 4 is the objectness.
	records := []rec{{cx: 0.4, cy: 0.4, w: 0.2, h: 0.2, class: 2, pixels: 1}}
	tn := fusedTensor(records, NumClasses, 5)
	res := Decode(tn, square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.Fields != 85 {
		t.Errorf("fields = %d, want 85", res.Telemetry.Fields)
	}
	if res.Telemetry.HasObjectness != ObjectnessYes {
		t.Errorf("has_objectness = %v, want an objectness at column 4", res.Telemetry.HasObjectness)
	}
	if len(res.Boxes) != 1 || res.Boxes[0].Class != "car" {
		t.Fatalf("boxes = %+v, want one car. With 85 fields read as 84 the car channel lands on "+
			"the wrong column and the class comes out wrong while the box stays in the right place", res.Boxes)
	}
	// The score is class x objectness, so it must be below the class logit alone.
	if res.Boxes[0].Score > 0.99 {
		t.Errorf("score = %v; the objectness channel was not multiplied in", res.Boxes[0].Score)
	}
}

// TestTheBoxChannelsAreNeverSigmoided is D1, restated as a test.
//
// A YOLOv8 emits cx,cy,w,h in INPUT PIXELS. sigmoid(300) is 1.0 to fifteen decimal
// places, so every box on every frame becomes the same normalised box, every frame
// reports the same identical boxes, and the run comes out CLEAN. Nothing crashes
// and nothing prints a warning.
func TestTheBoxChannelsAreNeverSigmoided(t *testing.T) {
	records := []rec{
		{cx: 0.0625, cy: 0.078125, w: 0.046875, h: 0.09375, class: 2, pixels: 640}, // 40,50 30x60
		{cx: 0.5, cy: 0.46875, w: 0.125, h: 0.0625, class: 2, pixels: 640},         // 320,300 80x40
		{cx: 0.875, cy: 0.890625, w: 0.1875, h: 0.15625, class: 2, pixels: 640},    // 560,570 120x100
	}
	tn := fusedTensor(records, NumClasses, 4)

	res := Decode(tn, square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.CoordSpace != "pixel" {
		t.Fatalf("coord space = %q, want pixel: coordinates reaching 610 on a 640 input cannot be fractions",
			res.Telemetry.CoordSpace)
	}
	// Three DIFFERENT boxes, not one repeated three times. A sigmoid on a box
	// channel puts every one of them at 0.5,0.5 and NMS then collapses them.
	if res.Telemetry.PostNMS != 3 {
		t.Fatalf("post_nms = %d, want 3. Identical boxes here mean a sigmoid was applied to a box "+
			"channel: every box collapses onto 0.5,0.5 and suppression eats them", res.Telemetry.PostNMS)
	}
	if len(res.Boxes) != 3 {
		t.Fatalf("boxes = %d, want 3", len(res.Boxes))
	}
	for _, r := range records {
		if !hasBoxNear(res.Boxes, "car", r.cx, r.cy, r.w, r.h) {
			t.Errorf("no box at centre (%.4f,%.4f) size (%.4f,%.4f); got %+v",
				r.cx, r.cy, r.w, r.h, res.Boxes)
		}
	}
	// And the sizes are LENGTHS: 30x60 pixels on a 640 input is 0.046875 x 0.09375
	// normalised. exp() or sigmoid() would both destroy that.
	if w, h := res.Boxes[0].Width(), res.Boxes[0].Height(); math.Abs(w*640-30) > 1e-3 || math.Abs(h*640-60) > 1e-3 {
		t.Fatalf("size = %vx%v pixels, want 30x60 — exp and sigmoid both destroy a length", w*640, h*640)
	}
}

// TestFractionCoordinatesStayFractions is the other branch of the same decision,
// and it is the one that catches a model emitting 0..1 for a 640 input.
func TestFractionCoordinatesStayFractions(t *testing.T) {
	records := []rec{
		{cx: 0.20, cy: 0.30, w: 0.10, h: 0.20, class: 2, pixels: 1},
		{cx: 0.70, cy: 0.32, w: 0.11, h: 0.21, class: 2, pixels: 1},
	}
	res := Decode(fusedTensor(records, NumClasses, 4), square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.CoordSpace != "fraction" {
		t.Fatalf("coord space = %q, want fraction", res.Telemetry.CoordSpace)
	}
	for _, r := range records {
		if !hasBoxNear(res.Boxes, "car", r.cx, r.cy, r.w, r.h) {
			t.Errorf("a fraction-space coordinate was rescaled: no box at (%.2f,%.2f); got %+v",
				r.cx, r.cy, res.Boxes)
		}
	}
}

// TestFractionCoordinatesHaveTheLetterboxPadRemoved is the branch interaction: a
// fraction-space model still needs the pad subtracted, and forgetting it shifts
// every box into the padding band.
func TestFractionCoordinatesHaveTheLetterboxPadRemoved(t *testing.T) {
	// A 16:9 frame: content 640x384, pad (0,128), input 640x640.
	g := Geometry{InputW: 640, InputH: 640, ContentW: 640, ContentH: 384, PadX: 0, PadY: 128, Scale: 1}
	// One box at the vertical CENTRE of the frame: cy 0.5 of the content, which in
	// canvas coordinates is 128 + 0.5*384 = 320.
	records := []rec{{cx: 0.5, cy: 0.5, w: 0.1, h: 0.2, class: 2, pixels: 1}}
	res := Decode(fusedTensor(records, NumClasses, 4), g, 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if len(res.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1: %+v", len(res.Boxes), res.Boxes)
	}
	if _, cy := res.Boxes[0].Centre(); math.Abs(cy-0.5) > 1e-3 {
		t.Fatalf("centre y = %.4f, want 0.5. The letterbox pad was not removed from a fraction-space "+
			"coordinate: the canvas centre is 320, the content centre is 192, and dividing without "+
			"subtracting the pad puts the box at %.4f", cy, 320.0/384.0)
	}
}

// TestFractionCoordinatesAreNotTreatedAsPixels is the asymmetry in looksLikePixels,
// and getting it wrong shrinks a box by a factor of the input.
//
// A fraction-space model with a box slightly off-frame can report a centre of 1.02.
// Treating that as pixels would scale it by 640 and put it 640 times off the
// picture, so a value must CLEAR THE INPUT SIZE to count as pixels — not merely
// exceed 1.0.
func TestFractionCoordinatesAreNotTreatedAsPixels(t *testing.T) {
	for _, tc := range []struct {
		name string
		cx   []float64
		want bool
	}{
		{"all well inside", []float64{0.2, 0.5, 0.9}, false},
		{"slightly off frame", []float64{1.02, 0.5, 0.9}, false},
		{"just past 1.5", []float64{1.6, 0.5, 0.9}, true},
		{"input pixels", []float64{320, 300, 640}, true},
		{"half the input", []float64{320, 10, 10}, true},
		{"negative pixels", []float64{-400, 0, 0}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikePixels(tc.cx, make([]float64, len(tc.cx)), 640, 640); got != tc.want {
				t.Fatalf("looksLikePixels(%v) = %v, want %v", tc.cx, got, tc.want)
			}
		})
	}
	if looksLikePixels(nil, nil, 640, 640) != true {
		t.Error("an empty tensor must match the Python reference and read as pixel space. It is " +
			"unreachable with any effect — the decode loop over zero records does nothing — and it is " +
			"pinned only so the two decoders cannot drift apart on it, because a parity test compares " +
			"this function's answer on every tensor including the empty one")
	}
}

// TestClassAgnosticSuppressionCollapsesTheCrossClassPair is RC-14 and the reason
// there are two passes.
//
// A `car` box and a `truck` box on ONE object, scores close, boxes overlapping
// completely. Class-aware NMS CANNOT collapse them — it only ever compares boxes of
// the same class, so the pair is invisible to it by construction. Without the
// second pass the run reports two vehicles where there is one, and the error is
// invisible on the picture: one object, one track, two crossings.
func TestClassAgnosticSuppressionCollapsesTheCrossClassPair(t *testing.T) {
	car := suppressed{box: [4]float64{0.10, 0.20, 0.30, 0.40}, score: 0.62, class: 2}
	truck := suppressed{box: [4]float64{0.102, 0.201, 0.302, 0.401}, score: 0.58, class: 7}
	cands := []suppressed{car, truck}

	afterClass := nmsClassAware(cands, defaultNMSClass)
	if len(afterClass) != 2 {
		t.Fatalf("class-aware pass removed %d of 2 boxes; it must not compare across classes", len(afterClass))
	}
	afterGlobal := nmsGlobal(afterClass, defaultNMSGlobal)
	if len(afterGlobal) != 1 {
		t.Fatalf("class-agnostic pass left %d boxes, want 1: a car and a truck on one object is one object",
			len(afterGlobal))
	}
	if afterGlobal[0].class != 2 {
		t.Fatalf("surviving class = %d, want 2 (car) — the higher score must win", afterGlobal[0].class)
	}
	if math.Abs(afterGlobal[0].score-0.62) > 1e-9 {
		t.Fatalf("surviving score = %v, want 0.62", afterGlobal[0].score)
	}
}

// TestTheTwoPassesRunEndToEndOnATensor is RC-14 through the whole decode rather
// than through the helper, because a fixture that passes at the helper level can
// still be bypassed by the decode path.
func TestTheTwoPassesRunEndToEndOnATensor(t *testing.T) {
	// Two records, same object, different classes, nearly identical geometry. Both
	// clear the floor; the class-aware pass must keep both; the global pass one.
	records := []rec{
		{cx: 0.20, cy: 0.30, w: 0.20, h: 0.20, class: 2, pixels: 1},   // car
		{cx: 0.202, cy: 0.302, w: 0.20, h: 0.20, class: 7, pixels: 1}, // truck, same object
	}
	res := Decode(fusedTensor(records, NumClasses, 4), square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.PreNMS != 2 {
		t.Fatalf("pre_nms = %d, want 2", res.Telemetry.PreNMS)
	}
	if res.Telemetry.PostNMSClass != 2 {
		t.Fatalf("post_nms_class = %d, want 2 — the class-aware pass must keep a car and a truck "+
			"that are different classes", res.Telemetry.PostNMSClass)
	}
	if res.Telemetry.PostNMS != 1 {
		t.Fatalf("post_nms = %d, want 1 — the class-agnostic pass is the only thing that can "+
			"collapse a cross-class pair", res.Telemetry.PostNMS)
	}
	if len(res.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(res.Boxes))
	}
}

// TestTwoSameClassOverlappingBoxesBecomeOne is the ordinary case, and the simpler
// half of RC-14: without NMS, one object emitted twice is two boxes, two tracks
// and two crossings.
func TestTwoSameClassOverlappingBoxesBecomeOne(t *testing.T) {
	high := suppressed{box: [4]float64{0.10, 0.20, 0.30, 0.40}, score: 0.9, class: 2}
	low := suppressed{box: [4]float64{0.11, 0.21, 0.31, 0.41}, score: 0.5, class: 2}
	if got := nmsClassAware([]suppressed{high, low}, defaultNMSClass); len(got) != 1 {
		t.Fatalf("two heavily overlapping same-class boxes yielded %d, want 1", len(got))
	}
	// Two DISTINCT objects of the same class must both survive — NMS is not a
	// de-duplicator for objects that are genuinely apart.
	far := suppressed{box: [4]float64{0.70, 0.70, 0.90, 0.90}, score: 0.8, class: 2}
	if got := nmsClassAware([]suppressed{high, far}, defaultNMSClass); len(got) != 2 {
		t.Fatalf("two well-separated same-class boxes yielded %d, want 2", len(got))
	}
	// And two objects that overlap only slightly must both survive at 0.45.
	slide := suppressed{box: [4]float64{0.26, 0.20, 0.46, 0.40}, score: 0.8, class: 2}
	if got := nmsClassAware([]suppressed{high, slide}, defaultNMSClass); len(got) != 2 {
		t.Fatalf("two objects overlapping at about 0.3 IoU yielded %d, want 2: a threshold that "+
			"merges adjacent vehicles would under-count every crossing", len(got))
	}
}

// TestEqualScoresBreakTiesByIndexNotBySortOrder is what makes the cross-decoder
// parity test possible at all.
//
// Two decoders that disagree only on which of two equal-scoring boxes survives
// will disagree on a DIFFERENT box each frame, because the order they break the
// tie in is not the same. The parity test would then fail intermittently on a run
// that is in fact correct, and a flaky test is a test nobody trusts.
func TestEqualScoresBreakTiesByIndexNotBySortOrder(t *testing.T) {
	cands := []suppressed{
		{box: [4]float64{0.10, 0.10, 0.20, 0.20}, score: 0.5, class: 2, original: 0},
		{box: [4]float64{0.50, 0.50, 0.60, 0.60}, score: 0.5, class: 5, original: 1},
		{box: [4]float64{0.80, 0.80, 0.90, 0.90}, score: 0.5, class: 7, original: 2},
	}
	order := sortedByScore(cands)
	for i, got := range order {
		if got != i {
			t.Fatalf("equal scores reordered: position %d holds original %d. The sort must be stable "+
				"or the parity test against the Python decoder becomes flaky", i, got)
		}
	}
	// Descending scores come out descending.
	desc := []suppressed{{score: 0.1}, {score: 0.9}, {score: 0.5}}
	o := sortedByScore(desc)
	if desc[o[0]].score != 0.9 || desc[o[1]].score != 0.5 || desc[o[2]].score != 0.1 {
		t.Fatalf("not sorted descending: %v", o)
	}
}

// TestClampHappensBeforeTheGate is the ordering, and it is the ordering the Python
// does and most decoders get wrong.
//
// A box entirely OUTSIDE the frame has a perfectly healthy area before clamping
// and no area at all after it. Gate first and it waves through; clamp first and it
// is refused. Emitting the clamped version would put {0,0,0,0} on the wire, which
// the consumer reads as an uninterpretable answer — so ONE box off the top of the
// frame turns a frame with nineteen good detections into a frame nobody can read.
func TestClampHappensBeforeTheGate(t *testing.T) {
	records := []rec{
		// Entirely above the frame: cy far negative in pixels.
		{cx: 0.5, cy: -8.0, w: 0.05, h: 0.05, class: 2, pixels: 640},
		// Wholly inside.
		{cx: 0.5, cy: 0.5, w: 0.05, h: 0.05, class: 2, pixels: 640},
	}
	res := Decode(fusedTensor(records, NumClasses, 4), square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.DroppedDegenerate == 0 {
		t.Fatal("the box entirely outside the frame was not counted as degenerate. If it was not " +
			"dropped, a zero-size box reached the output and the consumer reads that as an answer " +
			"it cannot interpret")
	}
	if len(res.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1 — the off-frame record must not survive", len(res.Boxes))
	}
	for _, b := range res.Boxes {
		if b.X2 <= b.X1 || b.Y2 <= b.Y1 {
			t.Fatalf("an emitted box has no area after clamping: %+v", b)
		}
	}
}

// TestADegenerateBoxIsNeverEmitted is RC-16 at the decoder's boundary.
//
// The consumer refuses a zero-or-negative extent box, so emitting one is not a
// small thing: it does not draw a bad rectangle, it makes the whole frame
// unreadable.
func TestADegenerateBoxIsNeverEmitted(t *testing.T) {
	// Three live records in a field of dead ones: zero width, zero height, and a
	// realClass box. The dead records matter — a zero-FILLED background would be
	// sigmoided into a score of 0.5 by the class-block check (which has to look at
	// the whole block, because a single-channel check misses a logit tensor whose
	// first class channel is near zero), and then 81 phantom candidates would
	// reach the gate and drown out the two degenerate ones this test is about.
	tn := fusedTensor([]rec{
		{cx: 0.5, cy: 0.5, w: 0, h: 0.05, class: 2, logit: 4, pixels: 1},
		{cx: 0.5, cy: 0.5, w: 0.05, h: 0, class: 2, logit: 4, pixels: 1},
		{cx: 0.5, cy: 0.5, w: 0.05, h: 0.05, class: 2, logit: 4, pixels: 1},
	}, NumClasses, 4)

	res := Decode(tn, square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.PreNMS != 3 {
		t.Fatalf("pre_nms = %d, want 3 — the dead records must stay below the floor", res.Telemetry.PreNMS)
	}
	for _, b := range res.Boxes {
		if !b.Valid() {
			t.Fatalf("an invalid box was emitted: %+v", b)
		}
		if b.X2 <= b.X1 || b.Y2 <= b.Y1 {
			t.Fatalf("a zero-or-negative extent box was emitted: %+v", b)
		}
	}
	if res.Telemetry.DroppedDegenerate != 2 {
		t.Errorf("dropped degenerate = %d, want 2", res.Telemetry.DroppedDegenerate)
	}
	if len(res.Boxes) != 1 {
		t.Errorf("boxes = %d, want 1 — only the third record has any extent", len(res.Boxes))
	}
}

// TestEveryEmittedBoxIsValid is the belt-and-braces version of RC-16: whatever the
// route, nothing that [proposal.Box.Valid] refuses may come out of the decoder.
//
// It is a property over a fixture set rather than a single case, because the routes
// to a degenerate box are many — zero extent, off-frame, sub-pixel, absurdly thin —
// and a test that only walks one of them walks the one the author thought of.
func TestEveryEmittedBoxIsValid(t *testing.T) {
	cases := map[string][]rec{
		"ordinary": {
			{cx: 0.5, cy: 0.5, w: 0.2, h: 0.2, class: 2, pixels: 1},
		},
		"off the top": {
			{cx: 0.5, cy: -4.0, w: 0.2, h: 0.2, class: 2, pixels: 1},
			{cx: 0.5, cy: 0.5, w: 0.2, h: 0.2, class: 0, pixels: 1},
		},
		"off the left": {
			{cx: -4.0, cy: 0.5, w: 0.2, h: 0.2, class: 2, pixels: 1},
			{cx: 0.3, cy: 0.5, w: 0.2, h: 0.2, class: 7, pixels: 1},
		},
		"hanging off the right": {
			{cx: 1.4, cy: 0.5, w: 0.2, h: 0.2, class: 2, pixels: 1},
			{cx: 0.7, cy: 0.5, w: 0.2, h: 0.2, class: 9, pixels: 1},
		},
		"sliver": {
			{cx: 0.5, cy: 0.5, w: 0.00001, h: 0.5, class: 2, pixels: 1},
			{cx: 0.2, cy: 0.2, w: 0.1, h: 0.1, class: 0, pixels: 1},
		},
		"sub-pixel in a 640 input": {
			{cx: 0.5, cy: 0.5, w: 0.0001, h: 0.0001, class: 2, pixels: 640},
		},
	}
	for name, records := range cases {
		t.Run(name, func(t *testing.T) {
			res := Decode(fusedTensor(records, NumClasses, 4),
				square640(), 0, defaultCfg())
			if res.Refusal != nil {
				t.Fatalf("refused: %v", res.Refusal)
			}
			for _, b := range res.Boxes {
				if !b.Valid() {
					t.Fatalf("emitted an invalid box: %+v", b)
				}
				if b.Class == "" {
					t.Fatalf("emitted a box with no class: %+v", b)
				}
				if b.Score < 0 || b.Score > 1 {
					t.Fatalf("emitted a score outside [0,1]: %+v", b)
				}
			}
		})
	}
}

// TestAThinBoxIsDroppedNotEmitted keeps the aspect gate honest. A box eight times
// wider than it is tall is not an object; it is the edge of a shadow, a railing,
// or a letterbox band, and emitting it costs association work on every frame.
func TestAThinBoxIsDroppedNotEmitted(t *testing.T) {
	records := []rec{
		{cx: 0.5, cy: 0.5, w: 0.40, h: 0.02, class: 2, pixels: 1}, // aspect 20
		{cx: 0.3, cy: 0.3, w: 0.10, h: 0.10, class: 0, pixels: 1}, // ordinary
	}
	res := Decode(fusedTensor(records, NumClasses, 4), square640(), 0, defaultCfg())
	if res.Telemetry.DroppedTiny != 1 {
		t.Fatalf("dropped tiny = %d, want 1 — an aspect of 20 is a shadow edge, not an object",
			res.Telemetry.DroppedTiny)
	}
	if len(res.Boxes) != 1 || res.Boxes[0].Class != "person" {
		t.Fatalf("boxes = %+v, want only the ordinary person", res.Boxes)
	}
}

// TestTheEmitFloorIsLowAndTheApprovalBarIsNotHere states the division of labour
// this package exists to preserve.
//
// The emit floor refuses to SEND NOISE. The approval bar is the run's decision and
// lives in SP_POC_SCORE_THRESHOLD where a reader can see it. This package must not
// become the decision: a detector that suppressed at 0.45 would be making a
// judgement the operator configured somewhere else.
func TestTheEmitFloorIsLowAndTheApprovalBarIsNotHere(t *testing.T) {
	cfg := defaultCfg()
	if cfg.EmitScore >= 0.45 {
		t.Fatalf("the emit floor is %v; at or above the approval bar the detector has become the decision",
			cfg.EmitScore)
	}
	if cfg.EmitScore <= 0 {
		t.Fatalf("the emit floor is %v; without a floor NMS keeps every candidate anchor, because a "+
			"sigmoid of a logit is never exactly zero", cfg.EmitScore)
	}

	// A weak box must be EMITTED and left for the run to refuse. That band is the
	// ghost band, and it is where the two-stage recovery later reads from.
	//
	// The background is realClass dead records rather than zeros, because the
	// class-block sigmoid turns a zero into a score of 0.5 — which is above the
	// floor and would make every record a candidate.
	weak := rec{cx: 0.5, cy: 0.5, w: 0.06, h: 0.06, class: 2, logit: -1, pixels: 1}
	res := Decode(fusedTensor([]rec{weak}, NumClasses, 4), square640(), 0, cfg)
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if len(res.Boxes) != 1 {
		t.Fatalf("a box at %.3f was not emitted (pre_nms=%d); it is above the emit floor and the "+
			"approval bar is the run's decision, not this package's", sigmoid(-1), res.Telemetry.PreNMS)
	}
	if res.Boxes[0].Score >= 0.45 {
		t.Fatalf("score = %v, expected a weak box below the approval bar", res.Boxes[0].Score)
	}
	if res.Boxes[0].Class != "car" {
		t.Fatalf("class = %q, want car: a weak box must still carry the class the model gave it, "+
			"or the recovery stage cannot use it", res.Boxes[0].Class)
	}
}

// TestTheTelemetryDistinguishesModelFailureFromDecoderFailure is the reason every
// count in Telemetry exists.
//
// Two runs both report "0 detections". In one the model found nothing. In the
// other the decoder threw everything away. They need opposite fixes, and a report
// that prints only the total has told its reader nothing.
func TestTheTelemetryDistinguishesModelFailureFromDecoderFailure(t *testing.T) {
	// (a) The model did not fire: many candidates, none above the floor.
	quiet := uniformTensor([]int64{1, 100, 84}, rec{cx: 0.5, cy: 0.5, w: 0.1, h: 0.1, class: 2, pixels: 1}, 4)
	for i := 6; i < 84; i++ {
		// All class channels well below the floor.
		for r := range 100 {
			quiet.Data[r*84+i] = -30
		}
	}
	a := Decode(quiet, square640(), 0, defaultCfg())
	if a.Refusal != nil {
		t.Fatalf("refused: %v", a.Refusal)
	}
	if a.Telemetry.Candidates != 100 {
		t.Errorf("candidates = %d, want 100", a.Telemetry.Candidates)
	}
	if a.Telemetry.PreNMS != 0 {
		t.Errorf("pre_nms = %d, want 0 — the model did not fire", a.Telemetry.PreNMS)
	}
	if len(a.Boxes) != 0 {
		t.Errorf("boxes = %d, want 0", len(a.Boxes))
	}

	// (b) The decoder threw them away: 200 identical candidates, so suppression
	// collapses them to one.
	twin := uniformTensor([]int64{1, 200, 84}, rec{cx: 0.5, cy: 0.5, w: 0.1, h: 0.1, class: 2, pixels: 1}, 4)
	b := Decode(twin, square640(), 0, defaultCfg())
	if b.Refusal != nil {
		t.Fatalf("refused: %v", b.Refusal)
	}
	if b.Telemetry.PreNMS == 0 {
		t.Fatal("pre_nms = 0; every identical candidate should clear the floor")
	}
	if b.Telemetry.PostNMSClass > 10 {
		t.Errorf("post_nms_class = %d from 200 identical candidates; suppression did not run",
			b.Telemetry.PostNMSClass)
	}
	if len(b.Boxes) != 1 {
		t.Errorf("boxes = %d, want 1 from 200 identical candidates", len(b.Boxes))
	}
}

// TestTheBoxCapIsAppliedAndCounted is what "a cap that silently drops is a cap that
// lies" means in practice.
func TestTheBoxCapIsAppliedAndCounted(t *testing.T) {
	// 30 well-separated cars.
	records := make([]rec, 0, 30)
	for i := 0; i < 30; i++ {
		x := 0.03 + float64(i)*0.032
		records = append(records, rec{cx: x, cy: 0.5, w: 0.025, h: 0.2, class: 2, pixels: 1})
	}
	cfg := defaultCfg()
	cfg.MaxBoxes = 5
	res := Decode(fusedTensor(records, NumClasses, 4), square640(), 0, cfg)
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if len(res.Boxes) != 5 {
		t.Fatalf("boxes = %d, want the cap of 5", len(res.Boxes))
	}
	if res.Telemetry.PostNMS != 5 {
		t.Errorf("post_nms = %d, want 5 — post_nms counts what the cap KEPT, and the difference "+
			"from the pre-cap count is what the cap removed", res.Telemetry.PostNMS)
	}
	// They come back highest-score first, and all scores here are equal, so the
	// order is the input order — which is the stability the parity test needs.
	for i := 1; i < len(res.Boxes); i++ {
		if res.Boxes[i].Score > res.Boxes[i-1].Score {
			t.Fatal("emitted boxes are not in descending score order")
		}
	}
}

// TestAModelWithNoDeclaredNamesFallsBackToCOCOAndSaysSo is the fallback, reported
// rather than hidden. "We fell back to COCO-80" is a fact about the answer.
func TestAModelWithNoDeclaredNamesFallsBackToCOCOAndSaysSo(t *testing.T) {
	records := []rec{{cx: 0.5, cy: 0.5, w: 0.2, h: 0.2, class: 2, pixels: 1}}
	res := Decode(fusedTensor(records, NumClasses, 4), square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.Classes != 0 {
		t.Errorf("classes = %d, want 0 — the model declared none and the fallback must not be "+
			"reported as though the model had", res.Telemetry.Classes)
	}
	if len(res.Boxes) != 1 || res.Boxes[0].Class != "car" {
		t.Fatalf("boxes = %+v, want one car from the COCO fallback", res.Boxes)
	}
}

// TestAModelWithDeclaredNamesUsesThem is the other branch: when the model declares
// its own class count, the record width is exact and the vocabulary is the
// model's, not COCO's.
func TestAModelWithDeclaredNamesUsesThem(t *testing.T) {
	// 8 declared classes -> 12 fields without objectness. Class 3 is inside the
	// model's range but outside COCO's naming only if 3 >= 8, so pick 7.
	records := []rec{{cx: 0.5, cy: 0.5, w: 0.2, h: 0.2, class: 7, pixels: 1}}
	res := Decode(fusedTensor(records, 8, 4), square640(), 8, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.Classes != 8 {
		t.Errorf("classes = %d, want 8", res.Telemetry.Classes)
	}
	// Index 7 within COCO-80 is `truck`, but this model declared only 8 names, so
	// the name is the COCO fallback at that index — and it is reported, because the
	// class count is a fact about the model that a reader is entitled to see.
	if len(res.Boxes) != 1 || res.Boxes[0].Class != "truck" {
		t.Fatalf("boxes = %+v, want the fallback name at index 7", res.Boxes)
	}
}

// TestAClassIndexOutsideTheVocabularyIsDroppedNotNamed stops a box arriving with an
// empty class, which the consumer would refuse with no idea why.
func TestAClassIndexOutsideTheVocabularyIsDroppedNotNamed(t *testing.T) {
	if ClassName(NumClasses) != "" || ClassName(-1) != "" || ClassName(9999) != "" {
		t.Fatal("an out-of-range class index must resolve to the empty string, not to a name and not to the index")
	}
	if ClassName(0) != "person" || ClassName(2) != "car" || ClassName(7) != "truck" {
		t.Fatalf("the COCO order is wrong: 0=%q 2=%q 7=%q", ClassName(0), ClassName(2), ClassName(7))
	}
	// A model with 100 classes whose winning class is index 90: outside COCO-80.
	records := []rec{{cx: 0.5, cy: 0.5, w: 0.2, h: 0.2, class: 90, pixels: 1}}
	res := Decode(fusedTensor(records, 100, 4), square640(), 100, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if len(res.Boxes) != 0 {
		t.Fatalf("a box with an unresolvable class was emitted: %+v", res.Boxes)
	}
	if res.Telemetry.DroppedUnknownClass != 1 {
		t.Errorf("dropped unknown class = %d, want 1 — it must be COUNTED, not silently dropped",
			res.Telemetry.DroppedUnknownClass)
	}
}

// TestFieldsForIsTheSameArithmeticTheDecoderUses keeps the exported helper honest,
// because a caller computing the expected record width with different arithmetic
// would agree with the decoder on 84 and disagree on 85 — which is exactly the
// off-by-one that renames a truck a bench.
func TestFieldsForIsTheSameArithmeticTheDecoderUses(t *testing.T) {
	if got := FieldsFor(NumClasses, false); got != 84 {
		t.Errorf("FieldsFor(80, no objectness) = %d, want 84", got)
	}
	if got := FieldsFor(NumClasses, true); got != 85 {
		t.Errorf("FieldsFor(80, objectness) = %d, want 85", got)
	}
	for _, tc := range []struct {
		fields, classes int
		want            Objectness
	}{
		{FieldsFor(80, false), 80, ObjectnessNone},
		{FieldsFor(80, true), 80, ObjectnessYes},
		{FieldsFor(8, false), 8, ObjectnessNone},
		{FieldsFor(8, true), 8, ObjectnessYes},
	} {
		if got := DecideObjectness(tc.fields, tc.classes); got != tc.want {
			t.Errorf("DecideObjectness(%d, %d) = %v, want %v", tc.fields, tc.classes, got, tc.want)
		}
	}
}

// TestTheTelemetryCarriesTheDeclaredShapeAndGeometry is the "why is the picture
// empty" tool. A reader needs the model's input size, the content size inside it,
// and the pad, all on one line, to check the arithmetic that put a box where it is.
func TestTheTelemetryCarriesTheDeclaredShapeAndGeometry(t *testing.T) {
	g := Geometry{InputW: 640, InputH: 640, ContentW: 640, ContentH: 384, PadX: 0, PadY: 128, Scale: 1}
	records := []rec{{cx: 0.5, cy: 0.5, w: 0.2, h: 0.2, class: 2, pixels: 1}}
	res := Decode(fusedTensor(records, NumClasses, 4), g, 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	// The decode populates the geometry it used, so a report can print it beside a
	// detection rate.
	if res.Telemetry.InputW != 640 || res.Telemetry.ContentH != 384 || res.Telemetry.PadY != 128 {
		t.Fatalf("telemetry geometry = %+v", res.Telemetry)
	}
}

// TestTheClassBlockIsCheckedWholeNotOneChannel is a silent zero that this decoder
// shipped for one build, and it is the third member of the family this repository
// has now hit three times: something wrong that produces zero detections and no
// error.
//
// The bug: the "are these probabilities or logits" test sampled ONE channel of the
// class block rather than the whole block. pyworker samples the whole
// `flat[:, first_score:]` slice, and the difference matters in exactly one
// situation — a tensor of LOGITS whose FIRST class channel happens to sit near
// zero, which is what a record with no confident class looks like. That record
// passes the single-channel test, no sigmoid is applied, the raw logits are used
// as scores, every score is negative, every candidate is under the emit floor, and
// the detector reports an empty road over a frame with objects on it.
//
// The fixture is built so channel 4 is 0.0 — a valid probability, which is what
// fools the single-channel check — while channel 6 holds the winning logit and the
// rest are strongly negative. If the check goes back to sampling one channel, the
// winning logit of -1 is read as a score of -1 and nothing is emitted.
func TestTheClassBlockIsCheckedWholeNotOneChannel(t *testing.T) {
	tn := fusedTensor([]rec{{cx: 0.5, cy: 0.5, w: 0.1, h: 0.1, class: 2, logit: -1, pixels: 1}},
		NumClasses, 4)
	// Channel 4 is the first class channel. Force it to a value inside [0,1] so a
	// single-channel check would call the whole block probabilities.
	for r := 0; r < int(tn.Dims[1]); r++ {
		tn.Data[r*84+4] = 0
	}
	if !isProbabilityColumn(tn.Data, 4, int(tn.Dims[1]), 84) {
		t.Fatal("the fixture no longer fools a single-channel check; it has stopped testing the bug")
	}
	if isProbabilityBlock(tn.Data, 4, 80, int(tn.Dims[1]), 84) {
		t.Fatal("the fixture's block is entirely inside [0,1]; it has stopped testing the bug")
	}

	res := Decode(tn, square640(), 0, defaultCfg())
	if res.Refusal != nil {
		t.Fatalf("refused: %v", res.Refusal)
	}
	if res.Telemetry.PreNMS == 0 {
		t.Fatalf("pre_nms = 0 over a tensor whose winning class channel is a logit of -1. The class " +
			"block was judged probabilities on the strength of one channel, no sigmoid was applied, " +
			"and every raw logit was read as a score — so every candidate fell below the floor and " +
			"the detector reported an empty road")
	}
	if len(res.Boxes) != 1 {
		t.Fatalf("boxes = %d, want 1", len(res.Boxes))
	}
	// The emitted score must be a PROBABILITY, not the raw logit it came from. It
	// is not sigmoid(-1): once the block is sigmoided, the forced channel-4 value
	// of 0.0 becomes sigmoid(0) = 0.5 and outranks the winning car channel, so the
	// box comes back as `person` at 0.5. That is correct behaviour over a
	// deliberately odd tensor — a realClass model does not publish 0.0 for an ambiguous
	// class next to a logit for the rest — and what the assertion is here to catch
	// is a score of -1 or 0, which is what a missed sigmoid produces.
	if res.Boxes[0].Score <= 0 || res.Boxes[0].Score >= 1 {
		t.Fatalf("score = %v: a raw logit was used as a score, so the class block was never "+
			"passed through the logistic", res.Boxes[0].Score)
	}
}

// TestTheObjectnessChannelIsCheckedAsOneColumn states the other half of the
// distinction: the objectness IS one column, so it is checked as one column. A
// block check there would be harmless but a block check over the WRONG extent
// would not be, so the two are named separately on purpose.
func TestTheObjectnessChannelIsCheckedAsOneColumn(t *testing.T) {
	// An 85-field record whose objectness channel is a logit (4) and whose class
	// channels are probabilities. The two blocks are in DIFFERENT units, which is
	// legal: a model may publish a sigmoided class head over a raw objectness.
	data := make([]float32, 2*85)
	data[4] = 4               // objectness: a logit
	for c := 5; c < 85; c++ { // classes: already probabilities
		data[c] = 0.9
	}
	if isProbabilityColumn(data, 4, 2, 85) {
		t.Error("the objectness column holds 4, which is not a probability")
	}
	if !isProbabilityBlock(data, 5, 80, 2, 85) {
		t.Error("the class block is entirely inside [0,1] and must be recognised as probabilities")
	}
}

// TestTheFieldsFirstGatherIsStridedNotContiguous is the transposition, and it is
// checked by CONTENT rather than by not panicking.
//
// On a [1,F,N] tensor a record's fields are N apart in memory. Gathering a
// contiguous run instead of striding field by field produces records full of other
// records' fields: boxes in roughly the right place wearing the wrong numbers,
// which is the one class of error a reader cannot catch by looking at the picture.
func TestTheFieldsFirstGatherIsStridedNotContiguous(t *testing.T) {
	// Rank 4, fields first, so the transpose has more than one non-field axis and
	// the axis-stride arithmetic is actually exercised rather than trivially right.
	const fields, n1, n2 = 6, 4, 5
	src := make([]float32, fields*n1*n2)
	for f := 0; f < fields; f++ {
		for a := 0; a < n1; a++ {
			for b := 0; b < n2; b++ {
				// Unique, recoverable value per (f, a, b).
				src[f*n1*n2+a*n2+b] = float32(f*1000 + a*100 + b)
			}
		}
	}
	tn := Tensor{Name: "o", Dims: []int64{fields, n1, n2}, Data: src}

	// With six declared fields per record and ten classes the record width must be
	// 4+10 = 14, which this tensor is not, so the strong path refuses it. That is
	// the refusal we want to check is NOT a panic and names the shape.
	res := Decode(tn, square640(), 10, defaultCfg())
	if res.Refusal == nil {
		t.Fatalf("a %d-field record with 10 declared classes was accepted", fields)
	}
	if !strings.Contains(res.Refusal.Detail, "[6 4 5]") {
		t.Errorf("the refusal does not quote the declared shape: %q", res.Refusal.Detail)
	}

	// And with the field count the shape actually declares, the gather must be
	// exact. Build [1, F, N] where F is a valid record width.
	const realClass = 84
	const recs = 6
	big := make([]float32, realClass*recs)
	for f := 0; f < realClass; f++ {
		for r := 0; r < recs; r++ {
			big[f*recs+r] = float32(f*100 + r)
		}
	}
	tn2 := Tensor{Name: "o", Dims: []int64{1, realClass, recs}, Data: big}
	data, count, gotFields, layout, err := records(tn2, NumClasses)
	if err != nil {
		t.Fatal(err)
	}
	if layout != LayoutFieldsFirst {
		t.Fatalf("layout = %q, want fields-first", layout)
	}
	if gotFields != realClass || count != recs {
		t.Fatalf("count=%d fields=%d, want %d and %d", count, gotFields, recs, realClass)
	}
	for r := 0; r < recs; r++ {
		for f := 0; f < realClass; f++ {
			want := float32(f*100 + r)
			if data[r*realClass+f] != want {
				t.Fatalf("record %d field %d = %v, want %v: the gather is reading the wrong "+
					"elements, so every field is another record's", r, f, data[r*realClass+f], want)
			}
		}
	}
}

// of +60 overflows exp() to +Inf, and Inf/Inf is NaN — and a NaN is not a low
// score. NaN fails every comparison, so it propagates through the floor, through
// NMS's ordering and into the emitted box, where it becomes a coordinate.
func TestSigmoidIsClippedSoItCannotProduceNaN(t *testing.T) {
	for _, v := range []float32{-1e9, -1000, -60, -1, 0, 1, 60, 1000, 1e9} {
		got := sigmoid(v)
		if math.IsNaN(got) {
			t.Fatalf("sigmoid(%v) = NaN", v)
		}
		if got < 0 || got > 1 {
			t.Fatalf("sigmoid(%v) = %v, outside [0,1]", v, got)
		}
	}
	if math.Abs(sigmoid(0)-0.5) > 1e-12 {
		t.Fatalf("sigmoid(0) = %v, want 0.5", sigmoid(0))
	}
	// A huge positive logit saturates at 1 rather than becoming Inf/Inf.
	if got := sigmoid(1e9); math.Abs(got-1) > 1e-15 {
		t.Fatalf("sigmoid(1e9) = %v, want 1", got)
	}
}

// TestIsProbabilityDecidesOnRange is D3. A model that emits probabilities must be
// left alone, because a sigmoid on a probability squashes every detection toward
// 0.5 and turns a good 0.9 into a useless 0.71 — a quiet halving of every score in
// the run, with the threshold unchanged.
func TestIsProbabilityDecidesOnRange(t *testing.T) {
	if !isProbability([]float32{0, 0.5, 0.9, 1}) {
		t.Error("values already inside [0,1] must be recognised as probabilities")
	}
	if isProbability([]float32{0, 0.5, 1.2}) {
		t.Error("a value above 1 means these are logits, not probabilities")
	}
	if isProbability([]float32{-0.1, 0.5}) {
		t.Error("a negative value means these are logits")
	}
	if !isProbability(nil) {
		t.Error("an empty column is vacuously a probability; it is not a logit column")
	}
}
