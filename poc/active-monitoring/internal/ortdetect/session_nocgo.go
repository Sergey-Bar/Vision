//go:build !cgo

// The no-cgo half of the session.
//
// It exists so that `go build ./...` works on a machine with no C toolchain, and it
// exists as a REFUSAL rather than as a stub that returns nothing.
//
// A stub returning zero boxes would be the worst possible outcome: `detector` mode
// would start, decode every frame into an empty result, report a detection rate of
// zero and pass every acceptance criterion. That is the defect this whole package
// exists to end — the previous build reported 6,578 approved boxes and zero
// vehicles because a change-fraction has no vocabulary. Adding a second way to
// report a confident zero would be the same mistake in new code.
//
// So without cgo, opening a session fails with a message naming the missing
// dependency and the command that installs it. `motion`/`novelty` mode still runs,
// the suite still passes, and `verify.ps1` prints SKIPPED with the same command
// rather than a quiet pass.

package ortdetect

import "errors"

// SessionConfig is the same shape as the cgo build's, so every caller compiles
// either way. See session_cgo.go for what each field means.
type SessionConfig struct {
	LibraryPath       string
	ModelPath         string
	Threads           int
	ExecutionProvider string
	CUDADeviceID      int
}

// InputSpec mirrors the cgo build's. It is a struct rather than a set of
// constants so that a caller reading the field names learns what the values mean.
type InputSpec struct {
	Name   string
	W, H   int
	Layout string
	Rank   int
}

// OutputSpec mirrors the cgo build's.
type OutputSpec struct {
	Name string
	Dims []int64
}

// Identity mirrors the cgo build's. Every field is empty, and that is reported
// rather than defaulted: there is no model, so there is no hash.
type Identity struct {
	ModelPath         string
	SHA256            string
	Bytes             int64
	Licence           string
	RuntimeVersion    string
	ExecutionProvider string
	CUDADeviceID      int
}

// Session is the no-cgo placeholder. It has no fields because it cannot exist: the
// zero value is not a session and must not be mistaken for one.
type Session struct{}

// NoCgoRefusal is the exact text, so the gate and a demo cannot word it
// differently.
const NoCgoRefusal = "ortdetect: this build has no cgo, so the in-process ONNX detector cannot be " +
	"compiled. The detector needs a C toolchain because ONNX Runtime is loaded through cgo. " +
	"On Windows: install MSYS2 and put C:\\msys64\\mingw64\\bin on PATH, then set CGO_ENABLED=1. " +
	"In Docker: build FROM a golang image that has gcc, or install build-essential. " +
	"SP_POC_TRIGGER_MODE=novelty needs no toolchain and still works — the whole-frame change " +
	"trigger is pure Go. What is lost is the DETECTOR, not the pipeline."

// Open refuses, always, and says what is missing.
func Open(cfg SessionConfig) (*Session, error) { return nil, errors.New(NoCgoRefusal) }

// The methods exist so that a caller compiles against either build. Each refuses
// with the same text, because reaching one of them means something constructed a
// Session without a successful Open, and that is worth saying loudly rather than
// allowing a nil dereference to explain it.

func (s *Session) Input() InputSpec                { return InputSpec{} }
func (s *Session) Outputs() []OutputSpec           { return nil }
func (s *Session) Identity() Identity              { return Identity{} }
func (s *Session) CheckInputSize(int) error        { return errors.New(NoCgoRefusal) }
func (s *Session) CheckDecoderFamily(string) error { return errors.New(NoCgoRefusal) }
func (s *Session) Close() error                    { return nil }

// Infer refuses, always.
func (s *Session) Infer([]float32, string) ([]Tensor, error) {
	return nil, errors.New(NoCgoRefusal)
}

// DetectLayout is PURE — it decides NCHW from NHWC by the position of the small
// axis and needs no runtime — so it is available in both builds. See
// session_pure.go, which is where it lives and why it matters.
