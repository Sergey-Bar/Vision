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
	// Dynamic mirrors the cgo build's: it says the export declares a variable
	// spatial size, so the input size is the run's choice.
	Dynamic bool
}

// Channels is the channel count implied by the model's own layout. It mirrors
// the cgo build's, so every caller compiles either way.
func (i InputSpec) Channels() int { return 3 }

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

// Open refuses, always, and says what is missing.
func Open(_ SessionConfig) (*Session, error) { return nil, errors.New(NoCgoRefusal) }

// The methods exist so that a caller compiles against either build. Each refuses
// with the same text, because reaching one of them means something constructed a
// Session without a successful Open, and that is worth saying loudly rather than
// allowing a nil dereference to explain it.

// Input is the model's declared input; without cgo there is no model.
func (s *Session) Input() InputSpec { return InputSpec{} }

// Outputs is the model's declared outputs; without cgo there is no model.
func (s *Session) Outputs() []OutputSpec { return nil }

// Identity is the model's identity; without cgo there is no model.
func (s *Session) Identity() Identity { return Identity{} }

// CheckInputSize refuses, naming the missing dependency.
func (s *Session) CheckInputSize(int) error { return errors.New(NoCgoRefusal) }

// CheckDecoderFamily refuses, naming the missing dependency.
func (s *Session) CheckDecoderFamily(string) error { return errors.New(NoCgoRefusal) }

// Close is a no-op: there is no session to close.
func (s *Session) Close() error { return nil }

// Vocabulary is the built-in one, because a build with no cgo has no model and
// therefore no class list of its own.
//
// It is the same answer the cgo build gives when the session declares no
// vocabulary, and a caller must not have to know which build it is talking to
// to ask the question.
func (s *Session) Vocabulary() *Vocabulary { return COCOVocabulary() }

// Infer refuses, always.
func (s *Session) Infer([]float32, string) ([]Tensor, error) {
	return nil, errors.New(NoCgoRefusal)
}

// DetectLayout is PURE — it decides NCHW from NHWC by the position of the small
// axis and needs no runtime — so it is available in both builds. See
// session_pure.go, which is where it lives and why it matters.
