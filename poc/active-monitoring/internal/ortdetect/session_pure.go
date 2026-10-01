package ortdetect

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// The facts a session must report, computed without a runtime.
//
// They are here and not in the cgo file because they are the part of model
// identity that has nothing to do with inference: a hash, a byte count and a
// licence read from the provenance record. Those three are what RC-18 requires a
// decision to name, and they must be computable on a machine with no C toolchain —
// `pocctl` needs to print a model's identity to decide whether it is the one a
// manifest already recorded, and that check must not need the runtime the manifest
// is trying to verify.

// DetectLayout decides NCHW from NHWC by the position of the small axis.
//
// The channel count is small and the spatial dimensions are large, so the position
// of the small one is the answer. The first model this repository measured wanted
// NHWC, and a code path that only knew NCHW would have produced confident nonsense
// from it.
//
// It is REFUSED rather than guessed when neither candidate holds a plausible
// channel count, because a tensor fed in the wrong layout does not error — it
// produces boxes, on whatever the convolution happens to align with, and those
// boxes generate line crossings on objects that are not there. Confident nonsense is
// worse than nothing, because nothing at least tells you the camera is broken.
//
// Axis 1 before axis 3 is the order of preference: an NCHW model has its channels
// in axis 1 and its width in axis 3, and a square NHWC input would satisfy both —
// but a square NHWC input is square NCHW's transpose, so the two are only
// distinguishable by the channel POSITION, and axis 1 is the one Ultralytics and
// the model zoo disagree about least.
func DetectLayout(shape []int64) (string, error) {
	if len(shape) != 4 {
		return "", &Refusal{
			What:   "the model's declared input is not rank 4",
			Detail: sprintf("expected (N,C,H,W) or (N,H,W,C), got %v", shape),
		}
	}
	isChannel := func(d int64) bool { return d == 1 || d == 3 || d == 4 }
	if isChannel(shape[1]) {
		return "nchw", nil
	}
	if isChannel(shape[3]) {
		return "nhwc", nil
	}
	return "", &Refusal{
		What: sprintf("cannot tell whether the model's declared input is NCHW or NHWC from %v", shape),
		Detail: "neither axis 1 nor axis 3 holds a plausible channel count (1, 3 or 4). Guessing here " +
			"would put boxes on the wrong objects and produce crossings that never happened, so the " +
			"model is refused by name instead",
	}
}

// InputSize reads the model's declared spatial size given its layout.
//
// It is a separate function because the two layouts put those axes in different
// places, and reading the wrong pair is a wrong letterbox: a 640x384 frame into a
// declared 384x640 canvas is a different picture from the right way round, and the
// detector produces boxes on both without complaint.
func InputSize(shape []int64, layout string) (w, h int, err error) {
	if len(shape) != 4 {
		return 0, 0, &Refusal{
			What:   "the model's declared input is not rank 4",
			Detail: sprintf("got %v", shape),
		}
	}
	switch layout {
	case "nchw":
		h, w = int(shape[2]), int(shape[3])
	case "nhwc":
		h, w = int(shape[1]), int(shape[2])
	default:
		return 0, 0, &Refusal{
			What:   sprintf("the layout %q is not one this build reads", layout),
			Detail: "expected nchw or nhwc; a layout with a capital letter in it is not a layout",
		}
	}
	if shape[0] != 1 && shape[0] != -1 {
		return 0, 0, &Refusal{
			What: sprintf("the model's declared batch axis is %d, and this detector runs one frame "+
				"at a time", shape[0]),
			Detail: "a batched export would need every record decoded and only one of them used; the " +
				"run must say which, and it does not",
		}
	}
	if w < 1 || h < 1 {
		return 0, 0, &Refusal{
			What:   "the model declares a non-positive spatial size",
			Detail: sprintf("%dx%d from %v in %s", w, h, shape, layout),
		}
	}
	return w, h, nil
}

// HashModel reads a model file and returns its SHA-256 and size.
//
// Streamed rather than read whole: the measured model in this repository is
// 257 MB, and a tool that allocates 257 MB to learn a hash is a tool that will be
// refused by the RSS limit the run enforces on the detector.
//
// The hash is the ONLY model identity that cannot be fabricated. There is no hash
// field in the ONNX graph and no runtime API that reports one, so it is computed
// from the bytes on disk, and it is reported whether or not it matches anything.
func HashModel(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
