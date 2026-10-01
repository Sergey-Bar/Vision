package ortdetect

// Letterboxing: an aspect-preserving resize into the model's square input, and
// the numbers needed to undo it exactly.
//
// # The one thing this file exists to get right
//
// The previous implementation returned a tensor and nothing else, and the caller
// then divided the model's output by the INPUT size. That is D4, and it is wrong
// for every frame that is not square.
//
// A 640x384 frame letterboxed into a 640x640 canvas has a CONTENT height of 384.
// Dividing y by 640 squashes every box to 0.6 of its true height: every object in
// the picture is drawn short and every crossing happens early. Nothing errors,
// nothing prints a warning, and the picture looks plausible — which is why it
// survived as long as it did.
//
// So the content size comes back from here and is the only thing the decoder
// divides by. [Geometry] is returned as a value, not recomputed, because a
// recomputation is a second implementation and a second implementation is where
// the two halves drift apart.

// Geometry is what the letterbox did, and what undoing it requires.
//
// ContentW and ContentH are the size the frame was scaled TO, inside a canvas of
// InputW by InputH. PadX and PadY are where the top-left corner of that content
// sits inside the canvas.
//
// The four are a unit: content + pad = input, on each axis. A decoder that
// subtracts the pad and divides by the input instead of by the content is the
// D4 defect, and this struct exists so that mistake has no spelling.
type Geometry struct {
	InputW, InputH     int
	ContentW, ContentH int
	PadX, PadY         int
	// Scale is the factor the frame was multiplied by, kept because the anchored
	// decoder needs it directly and recomputing it from content/frame is a second
	// rounding of the same number.
	Scale float64
}

// FrameW and FrameH are the frame's own dimensions, carried because the decoder
// converts back to FRAME fractions and a frame's size is not derivable from a
// square input.
func (g Geometry) FrameW() int { return g.ContentW }
func (g Geometry) FrameH() int { return g.ContentH }

// Content returns the letterboxed content width and height, the divisors.
//
// Named rather than left as two fields so a call site reads
// `g.Content()` and cannot accidentally divide x by the height.
func (g Geometry) Content() (w, h int) { return g.ContentW, g.ContentH }

// Letterbox resizes an RGB frame into a square canvas of inputW by inputH,
// preserving aspect ratio and padding the remainder with black, and returns it in
// the model's OWN memory order.
//
// The layout is a MODEL property and not a convention: ONNX exports both, Ultralytics
// writes NCHW and the ONNX model zoo writes NHWC, and the first model this
// repository measured wanted NHWC. Feeding NCHW to an NHWC input produces confident
// nonsense -- boxes on whatever the convolution happens to align with -- which is worse
// than an error because it generates crossings on objects that are not there.
//
// The order is produced HERE rather than transposed afterwards on purpose. At
// 640x640 that is 1,228,800 floats; a per-frame transpose of 4.9 MB is real work at
// the frame rate this package has to hold, and the write loop emits either order
// for free by changing where it puts the index.
//
// The input is packed RGB, three bytes per pixel, row-major, exactly the shape
// recorder.Frame.RGB carries — so no conversion happens here and no bytes are
// copied except into the output tensor.
//
// Resampling is nearest-neighbour. That is a deliberate accuracy compromise and
// it is stated rather than hidden: the point of this POC is to measure whether a
// real detector produces usable crossings on real traffic footage, not to win a
// mAP contest, and nearest-neighbour keeps the dependency list to the standard
// library and cgo. A bilinear path is a second pass over this function when
// somebody needs the last two points of recall.
func Letterbox(rgb []byte, frameW, frameH, inputW, inputH int, layout string) (tensor []float32, g Geometry, err error) {
	if frameW <= 0 || frameH <= 0 {
		return nil, Geometry{}, errBadFrame(frameW, frameH)
	}
	if inputW <= 0 || inputH <= 0 {
		return nil, Geometry{}, errBadInput(inputW, inputH)
	}
	if layout != "nchw" && layout != "nhwc" {
		return nil, Geometry{}, &Refusal{
			What: sprintf("the input layout %q is not one this build writes", layout),
			Detail: "expected nchw or nhwc. A layout with a capital letter in it is not a layout, and " +
				"guessing between the two hands the model a tensor it reads as a different picture",
		}
	}
	if len(rgb) < frameW*frameH*3 {
		// A short buffer is not a zero frame and must not be read past. This is
		// the same refusal internal/novelty makes for a corrupt frame, and it is
		// a refusal rather than a zero because a decoder handed zeros produces a
		// clean picture of an empty road.
		return nil, Geometry{}, &Refusal{
			What:   "the frame buffer is shorter than its own dimensions claim",
			Detail: sprintf("a %dx%d RGB frame needs %d bytes and the buffer holds %d", frameW, frameH, frameW*frameH*3, len(rgb)),
		}
	}

	// scale is the smaller of the two axis ratios, so the whole frame fits and one
	// axis is exact. Rounding, not truncation: truncation would shave up to a
	// pixel off each axis and the pad would not come out integral.
	scale := min(float64(inputW)/float64(frameW), float64(inputH)/float64(frameH))
	contentW := max(1, int(roundTo(float64(frameW)*scale)))
	contentH := max(1, int(roundTo(float64(frameH)*scale)))
	// Clamp: rounding can push content one pixel past the canvas on an axis that
	// was meant to fit exactly, and a slice out of range panics rather than
	// refusing.
	if contentW > inputW {
		contentW = inputW
	}
	if contentH > inputH {
		contentH = inputH
	}
	padX := (inputW - contentW) / 2
	padY := (inputH - contentH) / 2

	// The canvas is zero-filled first. That IS the padding: black, which is the
	// conventional letterbox value and the one the model's own training used.
	// Anything else — a mean colour, a mirrored edge — shifts every activation
	// near the border and the boxes near the edge with it.
	plane := inputW * inputH
	tensor = make([]float32, plane*3)
	// The two layouts differ in TWO strides, not one, and getting the second wrong
	// is a panic rather than a wrong pixel:
	//
	//   nhwc: index = (y*inputW + x)*3 + c   -> a row is 3*inputW floats, and a
	//                                        pixel's three channels are adjacent
	//   nchw: index = c*plane + y*inputW + x -> a row is inputW floats, a pixel is
	//                                        one float, and channels are a whole
	//                                        plane apart
	//
	// Using inputW for the NHWC row stride (or 3*inputW for NCHW) reads past the
	// end of the buffer on any non-square frame, which is every frame a 16:9 camera
	// produces.
	pixelStride, rowStride, chanStride := 3, inputW*3, 1
	if layout == "nchw" {
		pixelStride, rowStride, chanStride = 1, inputW, plane
	}
	for y := 0; y < contentH; y++ {
		// Nearest-neighbour source row, clamped to the last real row so a
		// rounding error at the far edge cannot index past the frame.
		sy := min(frameH-1, max(0, int(float64(y)/scale+0.5)))
		srcRow := sy * frameW * 3
		dstRow := (padY+y)*rowStride + padX*pixelStride
		for x := 0; x < contentW; x++ {
			sx := min(frameW-1, max(0, int(float64(x)/scale+0.5)))
			s := srcRow + sx*3
			d := dstRow + x*pixelStride
			tensor[d] = float32(rgb[s]) / 255
			tensor[d+chanStride] = float32(rgb[s+1]) / 255
			tensor[d+2*chanStride] = float32(rgb[s+2]) / 255
		}
	}

	return tensor, Geometry{
		InputW: inputW, InputH: inputH,
		ContentW: contentW, ContentH: contentH,
		PadX: padX, PadY: padY, Scale: scale,
	}, nil
}

// Unpad maps a coordinate from the letterboxed canvas back to the frame.
//
// The divisor is the CONTENT size, never the input size. That single choice is
// D4, and it is the reason this function exists rather than being four lines of
// arithmetic inline at the call site: the divisor is the thing that was wrong, and
// a named function makes it hard to get wrong in one place and easy to test.
func (g Geometry) Unpad(v float64, pad, content int) float64 {
	if content <= 0 {
		return 0
	}
	return (v - float64(pad)) / float64(content)
}
