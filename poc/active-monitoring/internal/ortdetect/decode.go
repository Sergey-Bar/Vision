package ortdetect

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Decoding one raw output tensor into normalised boxes against the frame.
//
// # Why this file is mostly refusals
//
// A detector that reads the wrong axis does not crash and does not print an
// error. It produces ZERO detections, or worse, confident nonsense: boxes that
// are on real objects and carry the wrong class. This repository has hit a silent
// zero twice — the sidecar's unknown-field wire mismatch, and its `input_w` key —
// and both times the run reported itself healthy.
//
// So the two decisions that can silently mis-decode are made from the tensor's
// OWN DECLARED SHAPE and nothing else:
//
//  1. Which axis is the record width. A YOLO11n export publishes [1,84,8400]
//     and a YOLOv8 export publishes [1,8400,84]. Those are the same 705,600
//     numbers in a different order, so the data itself cannot distinguish them.
//     Read the first and you get 84 candidates of 8400 fields each; read the
//     second, 8400 candidates of 84 fields. One is a detector and the other is
//     noise, and both are a []float32.
//
//  2. Whether the coordinates are input pixels or fractions of the input.
//     Decided on the RAW values, before anything transforms them, because the
//     previous build measured the ALREADY-sigmoided values — which are all <= 1
//     by construction, so the answer was always "fractions", so the pixel
//     conversion never ran, so a pixel-space model kept its raw 0..640 numbers
//     and every box was clamped to the frame's far corner.

// maxFields and minCandidates bound what may be read as a record width.
//
// A record is a handful of fields: 5 or 6 for the box plus objectness, 84 or 85
// for COCO with 80 classes. Nothing above a few hundred fields is a detection,
// and a tensor with only a handful of candidates is not a detector's output
// either — it is a feature map. Both bounds exist so a shape that happens to
// contain a plausible number is not mistaken for one.
const (
	maxFields     = 512
	minCandidates = 8
)

// Layout is how the record axis sits in the declared shape.
type Layout string

const (
	// LayoutFieldsLast is [1, N, F]: N records of F fields, records contiguous.
	// A YOLOv8n export publishes [1, 8400, 84].
	LayoutFieldsLast Layout = "fields-last"
	// LayoutFieldsFirst is [1, F, N]: F fields of N records, fields contiguous.
	// A YOLO11n export publishes [1, 84, 8400].
	LayoutFieldsFirst Layout = "fields-first"
)

// records reads a declared output tensor as a record-major table and returns the
// data, the record count, the field count and which axis the fields were on.
//
// `data` is record-major: record r's field f is at data[r*fields+f]. When the
// tensor is already fields-last this returns the underlying slice with no copy;
// when it is fields-first this transposes, which is the one allocation in the
// decode path and is paid once per frame rather than per candidate.
func records(t Tensor, classCount int) (data []float32, count, fields int, layout Layout, err error) {
	dims := t.Dims
	// Drop leading batch axes while the first is 1 and something remains. A batch
	// of 2 is not a detector this can read and is refused below by the field
	// search, which is the honest outcome.
	for len(dims) > 2 && dims[0] == 1 {
		dims = dims[1:]
	}
	if len(dims) < 2 {
		return nil, 0, 0, "", &Refusal{
			What:   "the output tensor's declared shape is not a detector's output",
			Detail: sprintf("expected at least rank 2 after dropping a batch axis, got %v", t.Dims),
		}
	}
	total := 1
	for _, d := range dims {
		if d <= 0 {
			return nil, 0, 0, "", &Refusal{
				What:   "the output tensor declares a non-positive dimension",
				Detail: sprintf("shape %v", t.Dims),
			}
		}
		total *= int(d)
	}
	if total == 0 || total > len(t.Data) {
		return nil, 0, 0, "", &Refusal{
			What:   "the output tensor's declared shape does not match the bytes returned",
			Detail: sprintf("shape %v implies %d values and the runtime returned %d", t.Dims, total, len(t.Data)),
		}
	}

	chosen := -1
	if classCount > 0 {
		// A model that carries its OWN names gives exact arithmetic: a record is
		// 4+classes or 5+classes fields, and nothing else can be a record. This is
		// the strong signal and it is preferred.
		wanted := map[int]bool{4 + classCount: true, 5 + classCount: true}
		for axis, d := range dims {
			if wanted[int(d)] {
				// Where more than one axis qualifies the LAST wins, because that is
				// the axis a multi-scale head puts its records on. A tie is a shape
				// the model does not really have.
				chosen = axis
			}
		}
		if chosen < 0 {
			return nil, 0, 0, "", &Refusal{
				What: "the model's declared class names and its output shape disagree",
				Detail: sprintf("the model declares %d class names, so a detection record must have %v fields, "+
					"and no axis of %v is that wide", classCount, []int{4 + classCount, 5 + classCount}, t.Dims),
			}
		}
	} else {
		// No declared names: only the weak shape heuristic is available, and it is
		// a refusal-prone guess by nature. It exists because the export measured in
		// this repository carries none.
		for axis, d := range dims {
			a := int(d)
			if a >= 5 && a <= maxFields && total/a >= minCandidates {
				chosen = axis
			}
		}
		if chosen < 0 {
			return nil, 0, 0, "", &Refusal{
				What:   "no axis of the output shape could be a record width",
				Detail: sprintf("shape %v has no axis between 5 and %d with at least %d candidates, and the model declares no class names to check against", t.Dims, maxFields, minCandidates),
			}
		}
	}

	fields = int(dims[chosen])
	rest := 1
	for axis, d := range dims {
		if axis != chosen {
			rest *= int(d)
		}
	}

	if chosen == len(dims)-1 {
		// Fields already last and contiguous. No copy.
		return t.Data[:rest*fields], rest, fields, LayoutFieldsLast, nil
	}
	// Fields-first: transpose into record-major order.
	out := make([]float32, rest*fields)
	// Row-major strides: stride[a] is the product of every dimension AFTER axis a.
	// The element at (c0, c1, ..., cn) is at sum(ci * stride[i]).
	//
	// Every axis needs its OWN stride. Using the field axis's stride for the record
	// axes as well reads past the end of the buffer on a [1, F, N] tensor — on
	// [1,84,168] the last record's first field lands at 167*84 = 14028 and a whole
	// record of 84 fields would run to 21084 in a buffer of 14112 — which is a panic
	// rather than a wrong answer, so it cannot ship.
	stride := make([]int, len(dims))
	acc := 1
	for a := len(dims) - 1; a >= 0; a-- {
		stride[a] = acc
		acc *= int(dims[a])
	}
	fieldStride := stride[chosen]

	// The record index runs over every axis EXCEPT `chosen`, in row-major order, so
	// record r's fields are contiguous once gathered.
	for r := 0; r < rest; r++ {
		srcBase := 0
		rem := r
		for a := len(dims) - 1; a >= 0; a-- {
			if a == chosen {
				continue
			}
			srcBase += (rem % int(dims[a])) * stride[a]
			rem /= int(dims[a])
		}
		if srcBase+(fields-1)*fieldStride >= len(t.Data) {
			// Unreachable for a shape whose product matches the buffer, which the
			// check above already established. It is here because the alternative
			// is a slice panic inside a frame loop, and a decoder that panics on one
			// frame takes the process down rather than degrading to `degraded`.
			return nil, 0, 0, "", &Refusal{
				What: "the declared output shape does not index inside the returned bytes",
				Detail: sprintf("shape %v puts record %d's last field at element %d of a %d-element buffer",
					t.Dims, r, srcBase+(fields-1)*fieldStride, len(t.Data)),
			}
		}
		// A STRIDED gather, field by field. A record's fields are not adjacent in
		// the source: for [1,F,N] with the field axis first, field f of record n is
		// at f*N+n, so consecutive fields are N apart. Copying a contiguous run
		// would gather N records' worth of interleaved data and produce a record
		// full of another record's fields — boxes in roughly the right place
		// wearing the wrong numbers, which is the one failure a reader cannot see.
		for f := 0; f < fields; f++ {
			out[r*fields+f] = t.Data[srcBase+f*fieldStride]
		}
	}
	return out, rest, fields, LayoutFieldsFirst, nil
}

// Objectness is the three-state answer to "is column 4 an objectness channel".
//
// It is three states and not two because a bool cannot say "neither": when the
// field count matches 4+classes and 5+classes equally — which happens when the
// model declares one more class than the record actually has — every class index
// would shift by one. Guessing produces a `truck` reported as a `bench`: a box in
// exactly the right place wearing the wrong word, which is the one error a reader
// cannot catch by looking at the picture.
//
// So the third state is a refusal, and it is a refusal by name saying which of
// the two halves is wrong.
type Objectness int

const (
	// ObjectnessNone means the class channels start at column 4. A YOLOv8's 84
	// fields.
	ObjectnessNone Objectness = iota
	// ObjectnessYes means column 4 is an objectness and the classes start at
	// column 5. A YOLOv4's 85 fields.
	ObjectnessYes
	// ObjectnessUnknown means the field count matches neither, and the frame is
	// refused rather than answered with the wrong words on the right boxes.
	ObjectnessUnknown
)

func (o Objectness) String() string {
	switch o {
	case ObjectnessNone:
		return "no objectness at column 4"
	case ObjectnessYes:
		return "objectness at column 4"
	}
	return "undecided"
}

// DecideObjectness settles column 4 from the FIELD COUNT and the model's declared
// names.
//
// Exact when the model carries names, because the arithmetic is then
// 4+classes/5+classes with no room for doubt. With no declared names only the
// COCO-80 shapes are known, and anything else is [ObjectnessUnknown] rather than
// a guess.
func DecideObjectness(fields, classCount int) Objectness {
	if classCount > 0 {
		switch fields {
		case 4 + classCount:
			return ObjectnessNone
		case 5 + classCount:
			return ObjectnessYes
		}
		return ObjectnessUnknown
	}
	switch fields {
	case 4 + NumClasses:
		return ObjectnessNone
	case 5 + NumClasses:
		return ObjectnessYes
	}
	return ObjectnessUnknown
}

// sigmoid is a numerically stable logistic.
//
// The clip is not decoration. A logit of -60 or +60 overflows exp() to 0 or +Inf,
// and +Inf/Inf is NaN — so without the clip an entire class channel becomes NaN,
// and NaN is not a low score. NaN fails every comparison, so it propagates
// through the floor, through NMS's ordering and into the emitted box, where it
// becomes a coordinate. A NaN coordinate is refused at the last moment, one
// layer away from the arithmetic that produced it.
func sigmoid(x float32) float64 {
	v := float64(x)
	if v > 60 {
		v = 60
	}
	if v < -60 {
		v = -60
	}
	return 1.0 / (1.0 + math.Exp(-v))
}

// isProbability decides whether a tensor already holds probabilities rather than
// logits, on the RANGE.
//
// A model that emits probabilities is left alone, because a sigmoid on a
// probability squashes every detection toward 0.5 and turns a good 0.9 into a
// useless 0.71 — which is not a crash, it is a quiet halving of every score in
// the run, and it moves the effective threshold without moving the threshold.
//
// THIS IS ONLY EVER CALLED ON THE OBJECTNESS AND CLASS CHANNELS.
//
// That is the whole of D1 and it is worth stating loudly, because a previous
// version also ran it over the four box channels. A YOLOv8 emits `cx,cy,w,h` in
// INPUT PIXELS, so `sigmoid(300)` is 1.0 to fifteen decimal places, every box on
// every frame became the same normalised box, every frame reported the same
// identical boxes, and the run came out clean. A box coordinate is a LENGTH. It
// is never a probability and nothing here may treat it as one.
func isProbability(vals []float32) bool {
	if len(vals) == 0 {
		return true
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo >= 0 && hi <= 1
}

// looksLikePixels decides whether the coordinates are input pixels or fractions
// of the input.
//
// Decided on the RAW values, which is the entire point. A pixel-space model puts
// centres anywhere in 0..inputW; a fraction-space model puts them in 0..1. The
// two cases are asymmetric on purpose: a fraction-space model with a box slightly
// off-frame can report a centre of 1.02, and treating that as pixels would shrink
// the box by a factor of 640 — so a value must CLEAR THE INPUT SIZE to count as
// pixels, not merely exceed 1.0.
func looksLikePixels(cx, cy []float64, inputW, inputH int) bool {
	if len(cx) == 0 && len(cy) == 0 {
		// Mirrors pyworker's `_looks_like_pixels`, which returns True for an empty
		// tensor. It is unreachable with any effect — the decode loop over zero
		// records touches nothing — and it is matched rather than "corrected"
		// because the cross-decoder parity test compares this function's answer on
		// every tensor, and a divergence on the empty one is a divergence.
		return true
	}
	peak := 0.0
	for _, v := range cx {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	for _, v := range cy {
		if a := math.Abs(v); a > peak {
			peak = a
		}
	}
	if peak <= 1.0+1e-6 {
		return false
	}
	small := inputW
	if inputH < small {
		small = inputH
	}
	return peak > 1.5 || peak >= float64(small)/2.0
}

// DecodeConfig is the decoder's settings, all of them run configuration rather
// than constants.
type DecodeConfig struct {
	// EmitScore is SP_POC_DET_SCORE: the MODEL'S EMIT FLOOR. It is not the
	// approval bar — SP_POC_SCORE_THRESHOLD is that, and it belongs to the run.
	// This floor exists because the model emits every candidate anchor with some
	// non-zero score (a sigmoid of a logit is never exactly zero), and without a
	// floor NMS keeps thousands of them and the detector answers with a megabyte
	// of noise that reads as a detection rate.
	EmitScore float64
	// MaxBoxes is SP_POC_DET_MAX_BOXES, applied after suppression. What it removed
	// is counted and reported, because a cap that silently drops is a cap that lies.
	MaxBoxes int
	// NMSClass and NMSGlobal are the two passes. See nms.go.
	NMSClass  float64
	NMSGlobal float64
	// MinBoxArea is the smallest box treated as an object, as a fraction of the
	// frame.
	MinBoxArea float64
}

// DecodeResult is one frame's decode: the boxes and the arithmetic that produced
// them.
//
// Every count here exists to make one thing distinguishable from another. "The
// model found nothing" (Candidates large, PreNMS zero) and "the decoder threw
// everything away" (PreNMS large, PostNMS zero) print identically on a report
// that prints only a detection rate — and they mean opposite things about whether
// the model or this code is at fault.
type DecodeResult struct {
	Boxes []Box
	// Telemetry is the decoder's own report about what it did.
	Telemetry Telemetry
	// Refusal is non-nil when the frame could not be decoded. A refusal is not a
	// zero: it is a specific named reason, and it is the reason a run with no
	// detections can be diagnosed instead of merely observed.
	Refusal *Refusal
}

// Refusal is a named "this model or this decoder cannot be trusted", carrying the
// reason and enough detail to act on.
//
// It is a type rather than an error string because the reason has to SURVIVE into
// telemetry. A run that prints "0 detections" is unactionable; a run that prints
// "the output tensor's shape [1,84,8400] cannot be read: no axis is a record
// width" sends its reader to the exporter.
type Refusal struct {
	What   string
	Detail string
}

func (r *Refusal) Error() string {
	if r.Detail == "" {
		return r.What
	}
	return r.What + ": " + r.Detail
}

// Is lets a caller test a refusal without importing errors and comparing strings,
// which is how a string comparison ends up in a switch statement that misses one.
func (r *Refusal) Is(target error) bool {
	var other *Refusal
	if errors.As(target, &other) {
		return r.What == other.What
	}
	return false
}

// Decode turns one raw output tensor into normalised boxes against the frame.
//
// `classCount` is how many class names the model itself declared: zero for an
// export that carries none, in which case the COCO-80 shapes are assumed and the
// field count must match one of them exactly.
func Decode(t Tensor, g Geometry, classCount int, cfg DecodeConfig) DecodeResult {
	data, count, fields, layout, err := records(t, classCount)
	if err != nil {
		return DecodeResult{Refusal: toRefusal(err), Telemetry: Telemetry{
			Candidates: 0, CoordSpace: "unknown", DeclaredShape: t.Dims,
		}}
	}

	obj := DecideObjectness(fields, classCount)
	if obj == ObjectnessUnknown {
		want := ""
		if classCount > 0 {
			want = sprintf("this model declares %d class names, so a record must have %v fields", classCount,
				[]int{4 + classCount, 5 + classCount})
		} else {
			want = sprintf("this model declares no class names, so only the COCO-80 shapes (%v) are readable",
				[]int{4 + NumClasses, 5 + NumClasses})
		}
		return DecodeResult{
			Refusal: &Refusal{
				What: "the field count matches neither 4+classes nor 5+classes",
				Detail: sprintf("%s, and the record is %d fields. Reading it either way would shift every "+
					"class index by one and report a truck as a bench, so this frame is refused", want, fields),
			},
			Telemetry: Telemetry{Candidates: count, Fields: fields, Classes: classCount,
				// The UNDECIDED state, recorded. Leaving it at the zero value would
				// print "no objectness at column 4" on a frame whose whole point is
				// that the decoder does not know — which is this module's law
				// inverted: an absence reported as a settled fact.
				HasObjectness: obj,
				Layout:        string(layout), DeclaredShape: t.Dims, CoordSpace: "unknown"},
		}
	}

	firstScore := 4
	var objVals []float64
	if obj == ObjectnessYes {
		firstScore = 5
		objVals = make([]float64, count)
		for r := 0; r < count; r++ {
			objVals[r] = float64(data[r*fields+4])
		}
		if !isProbabilityColumn(data, 4, count, fields) {
			for i := range objVals {
				objVals[i] = sigmoid(data[i*fields+4])
			}
		}
	}
	classes := fields - firstScore
	if classes <= 0 {
		return DecodeResult{
			Refusal: &Refusal{
				What:   "the tensor has a box and a score but no class channels",
				Detail: sprintf("the record is %d fields and the first class channel would be column %d", fields, firstScore),
			},
			Telemetry: Telemetry{Candidates: count, Fields: fields, Classes: classCount,
				HasObjectness: obj, Layout: string(layout), DeclaredShape: t.Dims,
				CoordSpace: "unknown", InputW: g.InputW, InputH: g.InputH,
				ContentW: g.ContentW, ContentH: g.ContentH, PadX: g.PadX, PadY: g.PadY},
		}
	}
	if !isProbabilityBlock(data, firstScore, classes, count, fields) {
		for r := 0; r < count; r++ {
			for c := 0; c < classes; c++ {
				data[r*fields+firstScore+c] = float32(sigmoid(data[r*fields+firstScore+c]))
			}
		}
	}

	// --- the box channels, NEVER sigmoided (D1) -------------------------------
	cx := make([]float64, count)
	cy := make([]float64, count)
	for r := 0; r < count; r++ {
		cx[r] = float64(data[r*fields])
		cy[r] = float64(data[r*fields+1])
	}
	bw := make([]float64, count)
	bh := make([]float64, count)
	for r := 0; r < count; r++ {
		bw[r] = float64(data[r*fields+2])
		bh[r] = float64(data[r*fields+3])
	}

	// The coordinate space is decided on the RAW values, before anything touches
	// them. See the file comment: measuring the sigmoided values made this always
	// answer "fractions", so the conversion below never ran.
	//
	// Both branches then do the SAME arithmetic — bring the coordinate to input
	// pixels, remove the pad, divide by the content — and differ only in the unit
	// the model used to publish it. That is deliberate: they used to be written as
	// two separate pieces of arithmetic, and the fraction branch grew a division
	// the pixel branch did not have, so a fraction-space box was scaled by the
	// input size a second time and came out 1/640th of its true size.
	coordSpace := "fraction"
	// toPixels converts one model-published coordinate to input pixels.
	toPixels := func(v float64, inputSize int) float64 {
		if coordSpace == "pixel" {
			return v
		}
		return v * float64(inputSize)
	}
	if looksLikePixels(cx, cy, g.InputW, g.InputH) {
		coordSpace = "pixel"
	}
	cw, ch := g.Content()
	fcw, fch := float64(max(1, cw)), float64(max(1, ch))
	for r := 0; r < count; r++ {
		cx[r] = (toPixels(cx[r], g.InputW) - float64(g.PadX)) / fcw
		cy[r] = (toPixels(cy[r], g.InputH) - float64(g.PadY)) / fch
		bw[r] = toPixels(bw[r], g.InputW) / fcw
		bh[r] = toPixels(bh[r], g.InputH) / fch
	}

	// --- the score channels ---------------------------------------------------
	best := make([]float64, count)
	which := make([]int, count)
	for r := 0; r < count; r++ {
		bv, bi := -1.0, 0
		for c := 0; c < classes; c++ {
			v := float64(data[r*fields+firstScore+c])
			if v > bv {
				bv, bi = v, c
			}
		}
		best[r], which[r] = bv, bi
		if obj == ObjectnessYes {
			best[r] *= objVals[r]
		}
	}

	cands := make([]suppressed, 0, count)
	preNMS := 0
	for r := 0; r < count; r++ {
		if best[r] < cfg.EmitScore {
			continue
		}
		preNMS++
		x1 := cx[r] - bw[r]/2
		y1 := cy[r] - bh[r]/2
		x2 := cx[r] + bw[r]/2
		y2 := cy[r] + bh[r]/2
		cands = append(cands, suppressed{box: [4]float64{x1, y1, x2, y2}, score: best[r], class: which[r], original: r})
	}

	tel := Telemetry{
		Candidates: count, PreNMS: preNMS, Fields: fields, Classes: classCount,
		HasObjectness: obj, Layout: string(layout), DeclaredShape: t.Dims, CoordSpace: coordSpace,
		// The geometry this decode actually used, on the record rather than left for
		// the caller to correlate. A report that prints a detection rate beside a
		// model name but not beside the model's input size, the content size and
		// the pad is asking its reader to trust an arithmetic they cannot check.
		InputW: g.InputW, InputH: g.InputH, ContentW: g.ContentW, ContentH: g.ContentH,
		PadX: g.PadX, PadY: g.PadY,
	}
	if len(cands) == 0 {
		tel.Note = sprintf("nothing scored at or above the %g emit floor", cfg.EmitScore)
		return DecodeResult{Telemetry: tel}
	}

	afterClass := nmsClassAware(cands, cfg.NMSClass)
	tel.PostNMSClass = len(afterClass)
	if len(afterClass) == 0 {
		tel.Note = "class-aware suppression removed every candidate"
		return DecodeResult{Telemetry: tel}
	}
	afterBoth := nmsGlobal(afterClass, cfg.NMSGlobal)

	chosen := afterBoth
	if len(chosen) > cfg.MaxBoxes {
		tel.CapDropped = len(chosen) - cfg.MaxBoxes
		chosen = chosen[:cfg.MaxBoxes]
	}
	// PostNMS counts what the decoder KEPT, which is after the cap — matching
	// pyworker, where `post_nms` is `len(chosen)` after the same slice. A count
	// taken before the cap would make the two implementations disagree on every
	// crowded frame, which is exactly the frame the cross-decoder parity test runs
	// on. What the cap removed is counted separately in CapDropped, because a cap
	// that silently drops is a cap that lies.
	tel.PostNMS = len(chosen)

	// CLAMP FIRST, THEN GATE. It is the other way round in most decoders and it is
	// wrong here for a specific reason: a box that sits entirely outside the frame
	// has a perfectly healthy area BEFORE clamping and no area AT ALL after it, so
	// a gate applied to the raw values waves it through and the emitted box is then
	// `{0,0,0,0}`. An inverted box is refused by the consumer as an uninterpretable
	// answer, so ONE box off the top of the frame turns a frame with nineteen good
	// detections into a frame the pipeline cannot read at all.
	out := make([]Box, 0, len(chosen))
	for _, s := range chosen {
		cx1 := clamp01(s.box[0])
		cy1 := clamp01(s.box[1])
		cx2 := clamp01(s.box[2])
		cy2 := clamp01(s.box[3])
		if cx2-cx1 <= 1e-6 || cy2-cy1 <= 1e-6 {
			tel.DroppedDegenerate++
			continue
		}
		area := (cx2 - cx1) * (cy2 - cy1)
		aspect := (cx2 - cx1) / math.Max(1e-9, cy2-cy1)
		if area < cfg.MinBoxArea || aspect < minAspect || aspect > maxAspect {
			tel.DroppedTiny++
			continue
		}
		name := ClassName(s.class)
		if name == "" {
			// The model has more classes than this vocabulary knows. The box is
			// counted and dropped rather than sent nameless: an unnamed box would be
			// refused downstream by Valid() with no idea why, and a nameless box on
			// the picture is a rectangle a reader cannot act on.
			tel.DroppedUnknownClass++
			continue
		}
		out = append(out, Box{Class: name, Score: s.score, X1: cx1, Y1: cy1, X2: cx2, Y2: cy2})
	}
	sortByScoreDesc(out)
	tel.Emitted = len(out)
	return DecodeResult{Boxes: out, Telemetry: tel}
}

// minAspect and maxAspect bound how thin a box may be. A box eight times wider
// than it is tall is not an object; it is the edge of a shadow, a railing, or a
// letterbox band. Mirrors pyworker's MIN_ASPECT/MAX_ASPECT.
const (
	minAspect = 1.0 / 8.0
	maxAspect = 8.0
)

// isProbabilityColumn reports whether ONE column of a record-major table is
// already probabilities. It is the right test for the objectness channel, which is
// a single column, and it is what pyworker's `_is_probability(obj)` does.
func isProbabilityColumn(data []float32, col, count, fields int) bool {
	for r := 0; r < count; r++ {
		v := data[r*fields+col]
		if v < 0 || v > 1 {
			return false
		}
	}
	return true
}

// isProbabilityBlock reports whether the whole class BLOCK is already
// probabilities.
//
// It must be the whole block and not one channel of it, and getting that wrong is
// a silent zero rather than a crash. A tensor of logits whose first class channel
// happens to sit near zero — which is what an object with no confident class
// looks like — passes a single-channel test, no sigmoid is applied, and the raw
// LOGITS are then used as scores. Every score is negative, every candidate is
// below the emit floor, and the detector reports an empty road over a frame with
// objects on it. Nothing errors.
//
// pyworker makes the same distinction (`_is_probability(class_scores)` over the
// whole `flat[:, first_score:]` slice, versus `_is_probability(obj)` over one
// column) and this is the Go side of keeping that.
func isProbabilityBlock(data []float32, firstCol, ncols, count, fields int) bool {
	if ncols <= 0 {
		return true
	}
	for r := 0; r < count; r++ {
		base := r*fields + firstCol
		for c := 0; c < ncols; c++ {
			v := data[base+c]
			if v < 0 || v > 1 {
				return false
			}
		}
	}
	return true
}

func clamp01(v float64) float64 {
	return math.Min(1, math.Max(0, v))
}

func sortByScoreDesc(boxes []Box) {
	// Insertion sort: the list is at most MaxBoxes long (100 by default) and is
	// nearly sorted by the time it arrives, because suppression already visited it
	// in descending score order. It is stable, which matters for parity.
	for i := 1; i < len(boxes); i++ {
		b := boxes[i]
		j := i - 1
		for j >= 0 && boxes[j].Score < b.Score {
			boxes[j+1] = boxes[j]
			j--
		}
		boxes[j+1] = b
	}
}

func toRefusal(err error) *Refusal {
	var r *Refusal
	if errors.As(err, &r) {
		return r
	}
	return &Refusal{What: "the output tensor could not be read", Detail: err.Error()}
}

func errBadFrame(w, h int) *Refusal {
	return &Refusal{
		What:   "the frame declares non-positive dimensions",
		Detail: sprintf("%dx%d", w, h),
	}
}

func errBadInput(w, h int) *Refusal {
	return &Refusal{
		What:   "the model's declared input size is not positive",
		Detail: sprintf("%dx%d", w, h),
	}
}

// errAnchored names the one decoder family this build does not implement.
func errAnchored(dims [][]int64) *Refusal {
	return &Refusal{
		What: "this export is a multi-scale ANCHORED head and the in-process decoder reads only fused outputs",
		Detail: sprintf("the session declares %d outputs, and an anchored darknet head publishes one tensor "+
			"per stride with coordinates that are offsets from anchor boxes living in the model's .cfg rather "+
			"than in the ONNX graph — so there is nothing in the file to recover them from. Run `pocctl demo` "+
			"with SP_POC_TRIGGER_MODE=sidecar for an anchored export: the Python sidecar implements that family, "+
			"and pointing this decoder at it instead would mean guessing an anchor set, which produces boxes in "+
			"the right place with the wrong sizes — the worst of both answers. Output shapes: %v", len(dims), dims),
	}
}

// checkFamily refuses a decoder family other than the fused one, by name.
func checkFamily(family string) *Refusal {
	if family == "" || family == "fused" {
		return nil
	}
	return &Refusal{
		What:   fmt.Sprintf("this build's decoder reads only fused outputs, and the session reports the family %q", family),
		Detail: "an anchored export needs anchor boxes that are not in the ONNX file; use the sidecar for that family",
	}
}

// joinNames renders a refusal for a report line without losing its structure.
func joinNames(parts ...string) string { return strings.Join(parts, " ") }
