package ortdetect

// NoCgoRefusal is the exact text, so the gate and a demo cannot word it
// differently.
//
// It lives here rather than beside the no-cgo build because it is part of the
// detector's CONTRACT in both builds: a build with cgo can still be asked what
// a build without cgo would say, and a caller that prints this constant needs
// it to exist. A constant that exists only in the build that refuses is a
// constant that cannot be compared against.
const NoCgoRefusal = "ortdetect: this build has no cgo, so the in-process ONNX detector cannot be " +
	"compiled. The detector needs a C toolchain because ONNX Runtime is loaded through cgo. " +
	"On Windows: install MSYS2 and put C:\\msys64\\mingw64\\bin on PATH, then set CGO_ENABLED=1. " +
	"In Docker: build FROM a golang image that has gcc, or install build-essential. " +
	"SP_POC_TRIGGER_MODE=novelty needs no toolchain and still works — the whole-frame change " +
	"trigger is pure Go. What is lost is the DETECTOR, not the pipeline."
