package ortdetect

import (
	"fmt"
	"math"
	"testing"
)

// Helpers for building tensors whose declared shape and whose content are chosen
// INDEPENDENTLY.
//
// That independence is the whole point. A helper that laid records out in the same
// order the decoder reads them would pass even when the decoder reads the wrong
// axis, because both would be wrong together — and reading the wrong axis is the
// failure this package exists to prevent. So the constructors here are driven by
// (record, field) indices in RECORD order while the DECLARED axis order is a
// separate choice, which is what lets a test put identical content behind
// [1,N,F] and [1,F,N] and assert one decode from both.
//
// # Why every fixture is padded to a realistic candidate count
//
// A YOLO output is thousands of candidate anchors and almost all of them are
// silent — below the emit floor. A fixture of three live records is not a shape
// any model produces, and the decoder is right to be suspicious of one: it
// requires at least minCandidates records before it will guess a record width from
// a model that declares no class names, because "one axis happens to be a
// plausible width" is exactly the guess that reads the wrong axis of a real tensor.
//
// So [padRecords] pads every fixture out to that floor with DEAD records — real
// records that score below the floor. That is not a workaround to make a test
// pass; it is what makes the fixture resemble the thing it stands for.

// rec is one fused detection record, in the shape a test wants to express.
//
// Coordinates are FRACTIONS of the model's input, and [rec.pixels] multiplies them
// by an input size so a pixel-space test can produce values above 1.0 — which is
// the only thing that distinguishes a pixel-space model from a fraction-space one.
type rec struct {
	cx, cy float64
	w, h   float64
	class  int
	// logit is what the winning class channel carries. The shipped exports emit
	// LOGITS, and a fixture that used probabilities would exercise the other
	// branch of isProbabilityColumn and prove nothing about the shipped models.
	// 4 -> 0.982, 0 -> 0.5, -3 -> 0.047, -8 -> -0.0003.
	logit  float32
	pixels float64
}

// live is a record that clears any sane emit floor.
func live(cx, cy, w, h float64, class int, pixels float64) rec {
	return rec{cx: cx, cy: cy, w: w, h: h, class: class, logit: 4, pixels: pixels}
}

// dead is a record below the emit floor: a candidate anchor the model scored out.
func dead() rec {
	return rec{cx: 0.5, cy: 0.5, w: 0.001, h: 0.001, class: 2, logit: -30, pixels: 1}
}

func (r rec) row(fields, firstScore, classes int) []float32 {
	out := make([]float32, fields)
	out[0] = float32(r.cx * r.pixels)
	out[1] = float32(r.cy * r.pixels)
	out[2] = float32(r.w * r.pixels)
	out[3] = float32(r.h * r.pixels)
	if firstScore > 4 {
		// Objectness: a logit of 4 is 0.982, above any emit floor.
		out[4] = 4
	}
	for c := 0; c < classes && firstScore+c < fields; c++ {
		if c == r.class {
			out[firstScore+c] = r.logit
		} else {
			out[firstScore+c] = -8
		}
	}
	return out
}

// padRecords returns the given live records followed by dead ones, up to at least
// minCandidates and rounded up to a whole number of strides — and ALWAYS to a
// record count that DIFFERS from the field width.
//
// That last part is not cosmetic. pyworker's rule for choosing the record axis is
// "where more than one axis qualifies, the LAST one wins", which is right for a
// real multi-scale head and means a shape like [1,84,84] is genuinely ambiguous:
// both axes qualify, the decoder picks fields-last, and a fixture that meant to
// test the fields-FIRST path would silently test fields-last and pass. Padding past
// the field width is what makes [1,84,168] unambiguously fields-first.
func padRecords(records []rec, fields int) []rec {
	want := max(minCandidates, fields+1)
	if len(records) > want {
		want = len(records)
	}
	// Round up to the next multiple of 84, which is one stride's worth of a COCO
	// head, so a fixture lands on a count a real export publishes.
	want = ((want + 83) / 84) * 84
	out := make([]rec, 0, want)
	out = append(out, records...)
	for len(out) < want {
		out = append(out, dead())
	}
	return out
}

// fusedTensor builds a fields-LAST tensor: [1, N, F], which is what a YOLOv8
// export publishes.
func fusedTensor(records []rec, classes, firstScore int) Tensor {
	fields := classes + firstScore
	padded := padRecords(records, fields)
	n := int64(len(padded))
	return scatter([]int64{1, n, int64(fields)}, padded, fields, firstScore, classes, true)
}

// fusedTensorFieldsFirst builds a fields-FIRST tensor: [1, F, N], which is what a
// YOLO11n export publishes.
//
// Same records, same values, different axis order, and a record count that
// differs from the field width so the two axes cannot be confused.
func fusedTensorFieldsFirst(records []rec, classes, firstScore int) Tensor {
	fields := classes + firstScore
	padded := padRecords(records, fields)
	n := int64(len(padded))
	return scatter([]int64{1, int64(fields), n}, padded, fields, firstScore, classes, false)
}

// scatter writes records into a tensor of the declared dims, in the axis order the
// dims ask for.
func scatter(dims []int64, records []rec, fields, firstScore, classes int, fieldsLast bool) Tensor {
	total := int64(1)
	for _, d := range dims {
		total *= d
	}
	t := Tensor{Name: "output0", Dims: dims, Data: make([]float32, total)}
	n := int64(len(records))
	for i, r := range records {
		row := r.row(fields, firstScore, classes)
		if fieldsLast {
			copy(t.Data[int64(i)*int64(fields):(int64(i)+1)*int64(fields)], row)
		} else {
			for f := 0; f < fields; f++ {
				t.Data[int64(i)+int64(f)*n] = row[f]
			}
		}
	}
	return t
}

// uniformTensor fills every record identically, which is how the "the model emitted
// the same box 200 times" case is built.
func uniformTensor(dims []int64, r rec, firstScore int) Tensor {
	total := int64(1)
	for _, d := range dims {
		total *= d
	}
	t := Tensor{Name: "output0", Dims: dims, Data: make([]float32, total)}
	fields := int(dims[len(dims)-1])
	if isFieldCount(dims[1]) && !isFieldCount(dims[len(dims)-1]) {
		fields = int(dims[1])
	}
	classes := fields - firstScore
	n := int(total) / fields
	row := r.row(fields, firstScore, classes)
	for i := 0; i < n; i++ {
		copy(t.Data[i*fields:(i+1)*fields], row)
	}
	return t
}

// rawTensor is a zero-filled tensor whose bytes the test sets by hand, for the
// cases that need one field changed.
//
// It is padded to a realistic record count for the same reason [fusedTensor] is:
// the decoder will not guess a record width from a tensor with four candidates,
// and that refusal is correct behaviour rather than something a fixture should
// work around by making the tensor implausible.
func rawTensor(dims []int64) Tensor {
	total := int64(1)
	for _, d := range dims {
		total *= d
	}
	return Tensor{Name: "output0", Dims: dims, Data: make([]float32, total)}
}

// paddedRaw is [1, N, fields] zero-filled with N at or above minCandidates, for a
// test that writes only the first few records by hand and leaves the rest silent.
func paddedRaw(fields int, n int) Tensor {
	if n < minCandidates {
		n = minCandidates
	}
	return rawTensor([]int64{1, int64(n), int64(fields)})
}

// square640 is the geometry of a square frame in a square 640 input: no pad, and
// the content divisor equals the input divisor.
func square640() Geometry {
	return Geometry{InputW: 640, InputH: 640, ContentW: 640, ContentH: 640, Scale: 1}
}

func prod(dims []int64) int {
	n := 1
	for _, d := range dims {
		n *= int(d)
	}
	return n
}

// isFieldCount is the fixture's own copy of the record-width rule, deliberately
// NOT shared with decode.go.
//
// If it were the same function, a change to the decoder's notion of a plausible
// field width would change the fixture's notion at the same moment and the two
// would agree while both were wrong. The fixture is pinned to the RULE — 5 to
// maxFields, from the plan's own arithmetic — and not to whatever the code
// currently believes.
func isFieldCount(d int64) bool { return d >= 5 && d <= maxFields }

func sprintDims(d []int64) string { return fmt.Sprint(d) }

// mustLetterbox fails the test rather than returning a zero geometry.
func mustLetterbox(t *testing.T, rgb []byte, w, h, iw, ih int) ([]float32, Geometry) {
	t.Helper()
	tensor, g, err := Letterbox(rgb, w, h, iw, ih)
	if err != nil {
		t.Fatalf("Letterbox(%dx%d -> %dx%d) refused: %v", w, h, iw, ih, err)
	}
	if len(tensor) != iw*ih*3 {
		t.Fatalf("Letterbox returned %d values, want %d", len(tensor), iw*ih*3)
	}
	return tensor, g
}

// hasBoxNear reports whether a box of the given class sits within tol of the given
// centre and size, all in normalised frame units.
func hasBoxNear(boxes []Box, class string, cx, cy, w, h float64) bool {
	for _, b := range boxes {
		if b.Class != class {
			continue
		}
		bcx, bcy := b.Centre()
		if math.Abs(bcx-cx) < 2e-3 && math.Abs(bcy-cy) < 2e-3 &&
			math.Abs(b.Width()-w) < 2e-3 && math.Abs(b.Height()-h) < 2e-3 {
			return true
		}
	}
	return false
}
