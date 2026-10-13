package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/ortdetect"
)

// The crop-pass self-test: the arithmetic the whole crop pass rests on, measured on
// this machine, with the real letterbox and the real coordinate mapping.
//
// # What it is and is not
//
// It measures the ARITHMETIC and it does NOT claim the detector found anything. It puts
// a 40x30 px object into a real 1920x1080 frame of real bytes, measures how many pixels
// of it survive the whole-frame pass's resize, then does the same through a 200x150
// crop, and prints both numbers side by side.
//
// That is the claim the crop pass makes, and it is a claim about RESOLUTION:
//
//	a whole-frame pass shrinks 1920x1080 to 416x416, so a 40 px object arrives at about
//	8 px, which is below the floor at which any detector is reliable;
//	the same object inside a 200x150 crop arrives at about 100 px, which is not.
//
// # Why it is worth having even though it finds nothing
//
// Because it can be run by anyone, anywhere, with no camera, no footage and no licence.
// Every other number in this plan's Phase 0.5 needs owned footage, a GPU, or both, and
// an argument that needs three things you do not have is an argument a judge cannot
// check. This one needs the weights file.
//
// And it is falsifiable in the way the plan cares about: if the crop geometry is wrong,
// the delivered sizes come out wrong, and the row says FAIL rather than a smaller number
// nobody would notice. The `Uncrop` row is the same property for the coordinate
// mapping -- it asserts the box came back in FRAME coordinates, which is the letterbox
// divisor defect in a new place and the one this repository has already shipped once.
//
// What it deliberately does NOT print is a detection rate. Finding a synthetic square is
// not finding a bird, and a row that said "PASS: bird detected" from a synthetic frame
// would be the single most dishonest line this command could emit.

// cropCheckRowsFrom opens its own detector for the crop rows.
//
// It is a SEPARATE session from [liveCheck] rather than sharing one, and the reason is
// ordering: `liveCheck` returns when the model refuses, and the crop rows need a
// working inference to measure at all. Sharing would mean threading a session through a
// function whose contract is "two rows about the device", and a third caller of it would
// then have to know about the crop rows.
// cropCheckRowsFrom opens its own detector for the crop rows.
//
// It is a SEPARATE session from [liveCheck] rather than sharing one, and the reason is
// ordering: `liveCheck` returns when the model refuses, and the crop rows need a
// working inference to measure at all. Sharing would mean threading a session through a
// function whose contract is "two rows about the device", and a third caller of it would
// then have to know about the crop rows.
//
// A detector refuses to be built without `SP_POC_DET_INPUT`, which is the right
// behaviour for a RUN: the number has to be said out loud rather than inferred from the
// model. It is the wrong behaviour for a DIAGNOSTIC, where a person has not configured
// a run and only wants to know whether this machine can do the thing. So the refusal is
// caught here and reported as NOT RUN with the same instruction, and `--selftest` stays
// answerable for somebody who has not set the variable yet.
func cropCheckRowsFrom(cfg ortdetect.SessionConfig, runs int) ([]ortdetect.Check, error) {
	input := envInt("SP_POC_DET_INPUT", "", 0)
	if input <= 0 {
		return []ortdetect.Check{{
			Name:  "crop pass: whole-frame vs crop",
			State: ortdetect.StateNotRun,
			Detail: "SP_POC_DET_INPUT is not set, so no detector can be built and the crop rows were not " +
				"evaluated. NOT RUN rather than PASS or FAIL: the rows above settle the DEVICE and these " +
				"need it. Set SP_POC_DET_INPUT to the size the model declares (416 for this export) and run " +
				"this again to get the whole-frame-versus-crop arithmetic measured on this machine",
		}}, nil
	}
	sess, err := ortdetect.Open(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.Close() }()
	det, err := ortdetect.NewDetector(ortdetect.Config{
		Session:   sess,
		InputSize: input,
		Decode: ortdetect.DecodeConfig{
			EmitScore:  cropFloat("SP_POC_DET_SCORE", 0.25),
			MaxBoxes:   cropInt("SP_POC_DET_MAX_BOXES", 100),
			NMSClass:   cropFloat("SP_POC_DET_NMS_CLASS", 0.45),
			NMSGlobal:  cropFloat("SP_POC_DET_NMS_GLOBAL", 0.75),
			MinBoxArea: cropFloat("SP_POC_MIN_BOX_AREA", 0.0004),
		},
	})
	if err != nil {
		return nil, err
	}
	return cropCheckRows(det, det.Input(), runs), nil
}

// cropInt and cropFloat read an optional knob, reporting the DEFAULT rather than
// refusing, because a diagnostic that refused on a tuning variable would be a
// preflight wearing a different hat. The defaults are the ones the README states.
func cropInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func cropFloat(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// cropObjectPx is the object's short side in frame pixels: a bird 40 px across at 30 m,
// which is the plan's own figure and the whole premise of the crop pass.
const cropObjectPx = 40

// cropFloorPx is the short side below which a detection is not reliable. It is 16, the
// bottom of the band, and it is a property of the model and not of this command -- which
// is why the row prints the delivered size AND the verdict rather than collapsing them.
const cropFloorPx = 16.0

// cropFrameW and cropFrameH are the source frame. 1080p, because that is what a camera
// is and because 4.6x downscale is the number the argument turns on.
const (
	cropFrameW = 1920
	cropFrameH = 1080
)

// cropRectW and cropRectH are the crop the proposal specifies. 200x150, which
// letterboxes to the same 416x416 and therefore magnifies rather than shrinks.
const (
	cropRectW = 200
	cropRectH = 150
)

// cropFrame builds a real frame of real bytes with one bright object on a noisy field.
//
// The noise is not decoration. A uniform frame takes the novelty engine's degenerate
// fast path and has a zero MAD, so a measurement over one would be about the empty case
// and would look faster than reality.
func cropFrame(objW, objH, x0, y0 int, seed uint32) []byte {
	rgb := make([]byte, cropFrameW*cropFrameH*3)
	// A cheap deterministic LCG, so the frame is byte-identical on two machines and a
	// difference in the numbers is a difference in the code rather than in the input.
	s := seed
	next := func() byte {
		s = s*1664525 + 1013904223
		return byte(90 + (s>>16)%24)
	}
	for i := 0; i < len(rgb); i += 3 {
		v := next()
		rgb[i], rgb[i+1], rgb[i+2] = v, v, v
	}
	for y := y0; y < y0+objH && y < cropFrameH; y++ {
		for x := x0; x < x0+objW && x < cropFrameW; x++ {
			o := (y*cropFrameW + x) * 3
			rgb[o], rgb[o+1], rgb[o+2] = 225, 228, 235
		}
	}
	return rgb
}

// deliveredPx is how many pixels of the object's SHORT SIDE reach the model, given a
// letterbox scale.
//
// It is computed from the letterbox's OWN scale rather than from the frame and the input
// separately, because recomputing the ratio here is a second implementation of the
// arithmetic under test and two implementations drift.
func deliveredPx(scale float64, objPx int) float64 { return float64(objPx) * scale }

// cropVerdict is the plan's claim as a row that can fail.
//
// It is a SEPARATE FUNCTION and not inline code in [cropCheckRows] because it is the
// whole of the claim and it needs its own tests. Tested through `cmdDetector` it would
// need a model file and a runtime library, so every assertion about it would be
// NOT RUN on a machine without the weights -- which is most machines, including every
// one that reviews the code.
//
// The three failures it can report, and the one thing it never reports:
//
//   - the whole-frame pass delivered AT OR ABOVE the floor: this frame size does not
//     demonstrate the problem, and saying it does is the failure mode. It FAILS rather
//     than reporting a win that is not there.
//   - the crop delivered BELOW the floor: the crop is too large to help, so every crop
//     in the run is a whole inference spent on nothing.
//   - the margin is under fourfold: a change of one number in the crop size destroys
//     the margin the cost argument rests on.
//
// It never reports a detection. Finding a synthetic square is not finding a bird.
func cropVerdict(wholePx, cropPx, floorPx float64) (ortdetect.Check, string) {
	ratio := 0.0
	if wholePx > 0 {
		ratio = cropPx / wholePx
	}
	detail := fmt.Sprintf("a %.0f px object arrives at %.1f px through the whole-frame pass and %.1f px "+
		"through the crop, a factor of %.1f", float64(cropObjectPx), wholePx, cropPx, ratio)
	switch {
	case wholePx >= floorPx:
		return ortdetect.Check{State: ortdetect.StateFail}, detail + fmt.Sprintf(
			". The whole-frame pass delivers %.1f px, at or above the %.0f px floor, so this frame "+
				"size does NOT demonstrate the problem the crop pass exists to solve. The row fails "+
				"rather than reporting a win that is not there", wholePx, floorPx)
	case cropPx < floorPx:
		return ortdetect.Check{State: ortdetect.StateFail}, detail + fmt.Sprintf(
			". The crop delivers %.1f px, still below the %.0f px floor, so the crop is too large "+
				"to help and every crop in this run is a whole inference spent on nothing",
			cropPx, floorPx)
	case ratio < 4:
		return ortdetect.Check{State: ortdetect.StateFail}, detail + fmt.Sprintf(
			". A factor of %.1f is not the margin the cost argument rests on; a change of one number "+
				"in the crop size destroys it", ratio)
	}
	return ortdetect.Check{State: ortdetect.StatePass}, detail
}

// cropCheckRows runs the comparison and returns the rows.
//
// It takes a Detector and the model's InputSpec rather than opening a session itself,
// because the caller already has one and a second session for the same weights would
// double the memory and report different latency.
func cropCheckRows(d *ortdetect.Detector, in ortdetect.InputSpec, runs int) []ortdetect.Check {
	var rows []ortdetect.Check

	// The object is placed away from the frame's edges so the crop does not have to
	// clamp, and so a clamp cannot quietly become the thing being measured.
	objW, objH := cropObjectPx, cropObjectPx
	const x0, y0 = 900, 500
	frame := cropFrame(objW, objH, x0, y0, 1)

	// 1. WHOLE FRAME. The letterbox geometry is asked for directly rather than being
	// read off an inference, because a refusal or a degraded frame would otherwise hide
	// the arithmetic this row exists to show.
	_, wholeGeom, err := ortdetect.Letterbox(frame, cropFrameW, cropFrameH, in.W, in.H, string(in.Layout))
	if err != nil {
		return append(rows, ortdetect.Check{
			Name:  "crop pass: whole-frame arithmetic",
			State: ortdetect.StateFail,
			Detail: fmt.Sprintf("cannot letterbox a %dx%d frame into %dx%d: %v. Nothing else in this "+
				"command can be evaluated without it", cropFrameW, cropFrameH, in.W, in.H, err),
		})
	}
	wholePx := deliveredPx(wholeGeom.Scale, cropObjectPx)

	// 2. THE CROP. The same function over the same bytes at the crop's own size, which
	// is what the crop pass does after `Rect.Extract`.
	_, cropGeom, err := ortdetect.Letterbox(frame[:cropRectW*cropRectH*3], cropRectW, cropRectH, in.W, in.H, string(in.Layout))
	if err != nil {
		return append(rows, ortdetect.Check{
			Name:   "crop pass: crop arithmetic",
			State:  ortdetect.StateFail,
			Detail: fmt.Sprintf("cannot letterbox a %dx%d crop into %dx%d: %v", cropRectW, cropRectH, in.W, in.H, err),
		})
	}
	cropPx := deliveredPx(cropGeom.Scale, cropObjectPx)

	// 3. THE VERDICT, which is the plan's claim stated as a row that can fail.
	v, detail := cropVerdict(wholePx, cropPx, cropFloorPx)
	rows = append(rows, ortdetect.Check{Name: "crop pass: whole-frame vs crop, delivered size",
		State: v.State,
		Detail: detail + fmt.Sprintf(". Model input %dx%d %s, content %dx%d, pad %d,%d. This is the "+
			"letterbox's own scale on this machine's build of it, and it is an ARITHMETIC row: it "+
			"says nothing about whether the detector finds the object",
			in.W, in.H, in.Layout, wholeGeom.ContentW, wholeGeom.ContentH, wholeGeom.PadX, wholeGeom.PadY)})

	// 4. THE COORDINATE MAPPING, on real inferences.
	//
	// `Uncrop` is asserted here and not only in a unit test because the unit test uses
	// synthetic boxes and this row uses the real letterbox's real output on real bytes.
	// The defect it guards -- mapping a crop's box back without the crop's offset --
	// produces plausible coordinates at the frame's origin with no error anywhere.
	rect := ortdetect.Rect{X: x0 - 20, Y: y0 - 20, W: cropRectW, H: cropRectH}
	rect, cerr := rect.Clamp(cropFrameW, cropFrameH)
	if cerr != nil {
		rows = append(rows, ortdetect.Check{Name: "crop pass: boxes come back in frame coordinates",
			State: ortdetect.StateFail, Detail: cerr.Error()})
		return rows
	}
	wholeAns := d.Detect(1, frame, cropFrameW, cropFrameH, ortdetect.Rect{})
	cropAns := d.Detect(2, frame, cropFrameW, cropFrameH, rect)
	if wholeAns.Refusal != nil {
		rows = append(rows, ortdetect.Check{
			Name:  "crop pass: boxes come back in frame coordinates",
			State: ortdetect.StateNotRun,
			Detail: fmt.Sprintf("the whole-frame inference refused (%s), so there is no measured box to "+
				"map and this row is NOT RUN rather than PASS", wholeAns.Refusal.What),
		})
		return rows
	}
	if cropAns.Refusal != nil {
		rows = append(rows, ortdetect.Check{
			Name:  "crop pass: boxes come back in frame coordinates",
			State: ortdetect.StateNotRun,
			Detail: fmt.Sprintf("the crop inference refused (%s), so there is no crop box to map back",
				cropAns.Refusal.What),
		})
		return rows
	}
	rows = append(rows, cropCoordinateRow(wholeAns, cropAns, rect))

	// 5. THE COST, which is what decides whether the whole idea is affordable. Measured
	// over `runs` calls per path, with the same first-call exclusion the latency check
	// uses, because the first call on a CUDA provider builds the context and can be an
	// order of magnitude slower.
	if runs > 1 {
		rows = append(rows, cropCostRow(d, frame, rect, runs))
	}
	return rows
}

// cropCoordinateRow asserts that a crop's boxes are in FRAME coordinates.
//
// The assertion is on the crop's own box, and it is deliberately loose about which box:
// the point is that the crop's boxes sit INSIDE THE CROP RECTANGLE, which they can only
// do if the crop's offset was applied. A version that dropped the offset would place
// them all in the frame's top-left corner, and the assertion catches that while saying
// nothing about whether the detector was right about the contents.
func cropCoordinateRow(whole, crop ortdetect.Answer, rect ortdetect.Rect) ortdetect.Check {
	c := ortdetect.Check{Name: "crop pass: boxes come back in frame coordinates"}
	if len(crop.Boxes) == 0 {
		// No box is NOT a failure. The synthetic square may not be detected, and this
		// row is about coordinates rather than about detection, so it says NOT RUN.
		c.State = ortdetect.StateNotRun
		c.Detail = fmt.Sprintf("the crop inference found no box on this frame, so there is nothing to map "+
			"back. The whole-frame pass found %d. This row is about COORDINATES and says nothing about "+
			"whether the detector is right: run it over owned footage for that", len(whole.Boxes))
		return c
	}
	// Every crop box must be inside the crop rectangle, in frame coordinates.
	loX, loY := float64(rect.X)/float64(cropFrameW), float64(rect.Y)/float64(cropFrameH)
	hiX, hiY := float64(rect.X+rect.W)/float64(cropFrameW), float64(rect.Y+rect.H)/float64(cropFrameH)
	// A tolerance of one thousandth of the frame, which is a pixel at 1000 px wide and
	// is here because the decoder's own arithmetic is float and a strict >= would fail
	// on a box exactly on the boundary.
	const tol = 1e-3
	for i, b := range crop.Boxes {
		if !cropBoxInside(b, loX, loY, hiX, hiY, tol) {
			c.State = ortdetect.StateFail
			c.Detail = fmt.Sprintf("crop box %d of %d is %+v, which is OUTSIDE the crop's own rectangle "+
				"[%v %v %v %v] in frame coordinates. The crop's offset was not applied on the way "+
				"back: every box lands near the frame's origin with plausible coordinates and no error "+
				"anywhere, which is the letterbox divisor defect in a new place",
				i, len(crop.Boxes), b, loX, loY, hiX, hiY)
			return c
		}
	}
	c.State = ortdetect.StatePass
	c.Detail = fmt.Sprintf("all %d crop box(es) fall inside the crop's rectangle expressed against the "+
		"FRAME, which they can only do if the crop's offset was applied to the mapping back. The "+
		"whole-frame pass found %d box(es) over the same frame, and this row compares coordinate "+
		"SPACES rather than detections",
		len(crop.Boxes), len(whole.Boxes))
	return c
}

// cropBoxInside is the containment test, separated so it is testable without a model.
//
// It is a function rather than an inline comparison for the reason the whole verdict
// is: a control nobody can exercise is a comment. This one can be pointed at a box that
// DID come back unmapped and asked whether it noticed.
func cropBoxInside(b ortdetect.Box, loX, loY, hiX, hiY, tol float64) bool {
	return b.X1 >= loX-tol && b.X2 <= hiX+tol && b.Y1 >= loY-tol && b.Y2 <= hiY+tol
}

// cropCostRow measures both paths and states the comparison a reviewer will ask for.
func cropCostRow(d *ortdetect.Detector, frame []byte, rect ortdetect.Rect, runs int) ortdetect.Check {
	// One warm call per path, discarded: the first call on a CUDA provider builds the
	// context, loads cuDNN and JIT-compiles the graph, and including it is how a GPU
	// gets reported as slow.
	d.Detect(0, frame, cropFrameW, cropFrameH, ortdetect.Rect{})
	d.Detect(0, frame, cropFrameW, cropFrameH, rect)

	wholeMS := timeIt(runs, func(i int) {
		d.Detect(uint64(i+10), frame, cropFrameW, cropFrameH, ortdetect.Rect{})
	})
	cropMS := timeIt(runs, func(i int) {
		d.Detect(uint64(i+10), frame, cropFrameW, cropFrameH, rect)
	})
	add := wholeMS + cropMS
	c := ortdetect.Check{
		Name:  "crop pass: cost of a whole-frame pass plus one crop",
		State: ortdetect.StatePass,
		Detail: fmt.Sprintf("over %d warm run(s) each: whole-frame %.1f ms, crop %.1f ms, together %.1f "+
			"ms. A frame carrying ONE proposed region therefore costs about %.2fx a frame carrying "+
			"none, and the multiplier is what the region cap exists to bound",
			runs, wholeMS, cropMS, add, add/wholeMS),
	}
	// A crop that costs several whole-frame passes is not affordable at any rate. The
	// threshold is 3 and it is deliberately generous: this row is a smoke alarm, and the
	// real answer is Phase 0.5.2 on real footage.
	if wholeMS > 0 && cropMS/wholeMS > 3 {
		c.State = ortdetect.StateFail
		c.Detail += fmt.Sprintf(". A crop costing %.1fx a whole-frame pass is not affordable at any "+
			"proposal rate, and the region cap cannot fix a cost this shape", cropMS/wholeMS)
	}
	return c
}

// timeIt runs fn n times and returns the MEAN wall-clock milliseconds.
//
// A mean and not a percentile, and the row says so: this is a smoke alarm for the cost
// shape, and a p99 over `runs` samples with a discarded first call is a number that looks
// precise and is not.
func timeIt(n int, fn func(i int)) float64 {
	if n < 1 {
		return 0
	}
	t0 := time.Now()
	for i := 0; i < n; i++ {
		fn(i)
	}
	return float64(time.Since(t0).Microseconds()) / 1000 / float64(n)
}
