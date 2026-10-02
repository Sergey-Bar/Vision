//go:build cgo

// The ONNX Runtime session.
//
// cgo-gated, and the gate is a build tag rather than an apology. Without cgo this
// package must still COMPILE, because the whole module has to compile for
// `novelty` mode, for the race detector, and for anyone checking out the
// repository on a machine with no C toolchain. A detector that cannot be built
// takes the entire module down with it; a detector that builds and refuses by name
// takes nothing down and tells the operator exactly what to install. The refusal
// lives in session_nocgo.go and names the command.
//
// # Why the library is named by path
//
// `SP_POC_ORT_LIB`, not resolved from PATH. Two ORT versions can be installed on
// one Windows machine, they are not ABI-compatible with each other's Go bindings,
// and the SHA-256 recorded against the weights is the SHA-256 of one specific
// build. A library discovered from PATH is a library nobody chose, and a
// measurement taken against it names a runtime that may not be the one anybody can
// reproduce.

package ortdetect

import (
	"errors"
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// SessionConfig is everything needed to open a session, all of it declared by the
// caller rather than discovered.
type SessionConfig struct {
	// LibraryPath is SP_POC_ORT_LIB: the ONNX Runtime shared library, named by path.
	LibraryPath string
	// ModelPath is SP_POC_DET_MODEL.
	ModelPath string
	// Threads is SP_POC_ORT_THREADS.
	//
	// It is recorded with every latency measurement, because a p99 without its
	// thread count describes no build: ORT otherwise picks from the machine's core
	// count, so the same run on a 4-core laptop and a 16-core server has different
	// latency and the recorded p99 describes neither.
	Threads int
	// ExecutionProvider is SP_POC_ORT_EP: "cpu" or "cuda". It is recorded on every
	// measurement for the same reason the thread count is — a latency without an
	// execution provider describes a device nobody can name.
	//
	// "cpu" is the default rather than a fallback. A run that silently fell back to
	// the CPU because the GPU was unavailable would produce the same p99 numbers as a
	// CPU run while the operator believed a GPU was in the loop, which is the exact
	// shape of the defect this module exists to end.
	ExecutionProvider string
	// CUDADeviceID is SP_POC_ORT_CUDA_DEVICE: which GPU, zero-based, as CUDA numbers
	// them. Only read when ExecutionProvider is "cuda".
	CUDADeviceID int
}

// Session is an open ONNX Runtime session and the facts about the model it was
// opened from.
//
// The facts are kept on the session rather than re-derived on demand because they
// are what every downstream number must be able to name: which file, which
// hash, which licence, what input size, and which output shape. [Identity] is the
// bundle RC-18 requires.
type Session struct {
	mu       sync.Mutex
	sess     *ort.DynamicAdvancedSession
	opts     *ort.SessionOptions
	input    InputSpec
	outputs  []OutputSpec
	identity Identity
	closed   bool
}

// InputSpec is the model's declared input.
type InputSpec struct {
	Name string
	// W and H are the model's declared INPUT size. Read from the session and
	// cross-checked against SP_POC_DET_INPUT, because a wrong value letterboxes a
	// frame into a square the model was not exported for and that is the most
	// productive way to produce confident nonsense.
	W, H int
	// Layout is `nchw` or `nhwc`, decided from the DECLARED shape by
	// [DetectLayout] and never by convention.
	//
	// ONNX exports both. Ultralytics writes NCHW and the ONNX model zoo writes
	// NHWC, and the measured model in this repository wanted NHWC. Feeding NCHW to
	// an NHWC input does not error — it produces boxes.
	Layout string
	// Rank is the declared rank, which is 4 for both layouts and is recorded
	// because a rank that is not 4 is a refusal and the number is what a reader
	// needs to see.
	Rank int
}

// OutputSpec is one declared output, with the shape read from the session.
type OutputSpec struct {
	Name string
	Dims []int64
}

// Identity is what a decision must name to be checkable.
//
// This is the RC-18 bundle: a model answer is a CLAIM about a specific file, and a
// claim that cannot name its file describes a build nobody can reconstruct. Every
// field here comes from the file on disk or from the session that opened it — none
// of it is a constant in this source.
type Identity struct {
	ModelPath string
	// SHA256 of the model file, computed by reading it. NOT a field the ONNX graph
	// carries — there is no such field — and never fabricated.
	SHA256 string
	Bytes  int64
	// Licence is the licence RECORDED for the model, read from
	// `testdata/labels.json`. It is a string in this struct because it comes from
	// that file, and an empty one is reported rather than defaulted: a model whose
	// licence nobody recorded is a fact about the answer.
	Licence string
	// RuntimeVersion is whatever the loaded ORT reports, so a measurement can name
	// the runtime as well as the weights.
	RuntimeVersion string
	// ExecutionProvider is the device the session runs on, normalised from
	// SP_POC_ORT_EP.
	//
	// It is the CONFIGURED provider, and it says so, because this binding exposes no
	// read-back of a session's active providers. What stands behind it is not a hope:
	// a CUDA run is refused before the session is opened unless `nvidia-smi` reports
	// the configured device, and the append itself is refused on error. So the value
	// is backed by a check that ran, not by a fallback that did not.
	ExecutionProvider string
	// CUDADeviceID is which GPU the run was confirmed against, zero-based. Zero on
	// the CPU, and there it means nothing rather than "the first GPU".
	CUDADeviceID int
}

// DetectLayout decides NCHW from NHWC by the position of the small axis. It is
// pure and lives in session_pure.go, available in both builds.

// Open loads the runtime and the model, and returns a session that has already
// read the model's own declarations.
//
// Everything that can be refused is refused HERE, at open, by name — rather than
// discovered at the first frame. A model whose shape cannot be read is a mistake
// in the export or in the configuration, and both are found in seconds at startup
// and in the middle of a demo otherwise.
func Open(cfg SessionConfig) (*Session, error) {
	if cfg.Threads < 1 {
		return nil, errors.New("ortdetect: the thread count must be at least one; a session with no " +
			"threads has no defined latency, and the recorded p99 would describe no build")
	}
	if cfg.LibraryPath == "" {
		return nil, &Refusal{
			What: "no ONNX Runtime library was named",
			Detail: "set SP_POC_ORT_LIB to the onnxruntime shared library, or run `pocctl fetch-runtime` " +
				"which writes the path it verified into the manifest",
		}
	}
	if _, err := os.Stat(cfg.LibraryPath); err != nil {
		// Named as a missing FILE and not as a failed load: "cannot load onnxruntime"
		// sends a reader to reinstall ORT, when the actual problem is usually a path
		// that does not exist.
		return nil, &Refusal{
			What: "the ONNX Runtime library is not where SP_POC_ORT_LIB says",
			Detail: sprintf("%s: %v. Run `pocctl fetch-runtime` to write a path that exists, or point the "+
				"variable at the library your machine has", cfg.LibraryPath, err),
		}
	}
	if _, err := os.Stat(cfg.ModelPath); err != nil {
		return nil, &Refusal{
			What:   "the detector's model file is not where SP_POC_DET_MODEL says",
			Detail: sprintf("%s: %v", cfg.ModelPath, err),
		}
	}

	ort.SetSharedLibraryPath(cfg.LibraryPath)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, &Refusal{
			What: "the ONNX Runtime could not be initialised",
			Detail: sprintf("%v. The library at %s loaded but its environment did not come up; "+
				"on Windows this is usually a Visual C++ redistributable mismatch between the runtime "+
				"build and the one on the machine", err, cfg.LibraryPath),
		}
	}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		_ = ort.DestroyEnvironment()
		return nil, &Refusal{
			What:   "the ONNX Runtime session options could not be created",
			Detail: err.Error(),
		}
	}
	if err := opts.SetIntraOpNumThreads(cfg.Threads); err != nil {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, &Refusal{
			What:   "the ONNX Runtime refused the configured intra-op thread count",
			Detail: sprintf("SP_POC_ORT_THREADS=%d: %v", cfg.Threads, err),
		}
	}
	if err := applyExecutionProvider(opts, cfg.ExecutionProvider, cfg.CUDADeviceID); err != nil {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, err
	}

	// Read the declarations from a temporary session BEFORE opening the real one, so
	// a refusal costs nothing and names the model rather than the session.
	inInfo, outInfo, err := ort.GetInputOutputInfoWithOptions(cfg.ModelPath, opts)
	if err != nil {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, &Refusal{
			What:   "the model could not be opened to read its own declarations",
			Detail: sprintf("%s: %v", cfg.ModelPath, err),
		}
	}
	if len(inInfo) != 1 {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, &Refusal{
			What:   "the model does not take exactly one input",
			Detail: sprintf("it takes %d: %v", len(inInfo), namesOf(inInfo)),
		}
	}
	layout, err := DetectLayout(inInfo[0].Dimensions)
	if err != nil {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, err
	}
	w, h, err := InputSize(inInfo[0].Dimensions, layout)
	if err != nil {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, err
	}

	inNames := namesOf(inInfo)
	outNames := namesOf(outInfo)
	sess, err := ort.NewDynamicAdvancedSession(cfg.ModelPath, inNames, outNames, opts)
	if err != nil {
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, &Refusal{
			What:   "the ONNX Runtime could not create a session for the model",
			Detail: sprintf("%s: %v", cfg.ModelPath, err),
		}
	}

	outputs := make([]OutputSpec, 0, len(outInfo))
	for _, o := range outInfo {
		outputs = append(outputs, OutputSpec{Name: o.Name, Dims: o.Dimensions})
	}

	id := Identity{ModelPath: cfg.ModelPath, ExecutionProvider: NormalizeEP(cfg.ExecutionProvider),
		CUDADeviceID: cfg.CUDADeviceID}
	sum, size, err := HashModel(cfg.ModelPath)
	if err != nil {
		_ = sess.Destroy()
		_ = opts.Destroy()
		_ = ort.DestroyEnvironment()
		return nil, &Refusal{
			What: "the model's SHA-256 could not be computed",
			Detail: sprintf("%s: %v. A model whose hash cannot be read is refused rather than run "+
				"unattributed, because a detection rate that cannot name the file it came from "+
				"describes a build nobody can rebuild", cfg.ModelPath, err),
		}
	}
	id.SHA256, id.Bytes = sum, size
	if md, err := sess.GetModelMetadata(); err == nil {
		id.RuntimeVersion = sprintf("%v", md)
		_ = md.Destroy()
	}

	return &Session{
		sess: sess, opts: opts,
		input:    InputSpec{Name: inInfo[0].Name, W: int(w), H: int(h), Layout: layout, Rank: len(inInfo[0].Dimensions)},
		outputs:  outputs,
		identity: id,
	}, nil
}

// Input returns the model's declared input, for the cross-check against
// SP_POC_DET_INPUT.
func (s *Session) Input() InputSpec { return s.input }

// Outputs returns the model's declared outputs, with their shapes.
//
// The SHAPES are the whole point of asking: a YOLO11n export publishes
// [1,84,8400] and a YOLOv8 export publishes [1,8400,84], and those are the same
// numbers in a different order. See decode.go.
func (s *Session) Outputs() []OutputSpec {
	out := make([]OutputSpec, len(s.outputs))
	copy(out, s.outputs)
	return out
}

// Identity returns what a decision made through this session must name.
func (s *Session) Identity() Identity { return s.identity }

// CheckInputSize refuses a configured input size the model does not declare.
//
// This is a refusal rather than an adjustment. The model has one input size and it
// is whatever the export baked in; letterboxing into a different square is the most
// productive way to produce confident nonsense, and adjusting silently would mean
// the operator's declared SP_POC_DET_INPUT was not what ran.
func (s *Session) CheckInputSize(configured int) error {
	if configured <= 0 {
		return &Refusal{
			What: "SP_POC_DET_INPUT was not set",
			Detail: sprintf("the model declares %dx%d; set the variable to that number so the run "+
				"says out loud what it letterboxed into", s.input.W, s.input.H),
		}
	}
	if configured != s.input.W || configured != s.input.H {
		return &Refusal{
			What: sprintf("SP_POC_DET_INPUT=%d does not match the model's declared input %dx%d",
				configured, s.input.W, s.input.H),
			Detail: "the input size is a property of the model, not a preference. Letterboxing a frame " +
				"into a square the model was not exported for produces boxes, not an error, and those " +
				"boxes are in the wrong place by a scale factor",
		}
	}
	return nil
}

// CheckDecoderFamily refuses an export this build cannot decode.
func (s *Session) CheckDecoderFamily(family string) error { return checkFamily(family) }

// Infer runs the model over one already-letterboxed tensor and returns the raw
// outputs with their DECLARED shapes.
//
// The tensor is the whole input buffer: [1,C,H,W] or [1,H,W,C] as the model's own
// layout says, with the batch axis present. Callers that want the geometry back
// should use [Detector.Detect], which letterboxes and decodes in one step; this is
// the seam the parity test drives, because it takes a tensor and hands back tensors
// with nothing in between.
//
// Every call takes the session lock. ORT sessions are not safe for concurrent Run
// calls, and the frame loop is the only caller, so the lock costs nothing and its
// absence would be a data race rather than a slowdown.
func (s *Session) Infer(tensor []float32, layout string) ([]Tensor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("ortdetect: the session is closed")
	}
	// The caller's layout is CROSS-CHECKED against the model's rather than used.
	// Laying the tensor out in the caller's order when the model declares another is
	// the quiet version of the bug RC-30 is about: a tensor of exactly the right size,
	// fed in the wrong order, decoding to boxes that are plausible and wrong. Refusing
	// is the only answer that does not involve this module guessing which of two
	// confident statements is the true one.
	if layout != "" && layout != s.input.Layout {
		return nil, &Refusal{
			What: "the caller's input layout disagrees with the model's",
			Detail: sprintf("the caller says %s and %s declares %s. Laying the tensor out in the caller's "+
				"order would feed the model a tensor of the right SIZE in the wrong order, which is a detector "+
				"that decodes boxes that are plausible and wrong. Fix the caller or re-export the model",
				layout, s.input.Name, s.input.Layout),
		}
	}

	shape, serr := s.inputShape(len(tensor))
	if serr != nil {
		return nil, serr
	}
	in, err := ort.NewTensor(shape, tensor)
	if err != nil || in == nil {
		return nil, &Refusal{
			What: "the input tensor could not be created",
			Detail: sprintf("a %dx%d %s input of %d floats was refused by the runtime: %v",
				s.input.W, s.input.H, s.input.Layout, len(tensor), err),
		}
	}
	defer func() { _ = in.Destroy() }()

	outSpecs, autoAllocate := preallocateOutputs(s.outputs)
	if !autoAllocate {
		defer func() {
			for _, v := range outSpecs {
				_ = v.Destroy()
			}
		}()
	}

	if err := s.sess.Run([]ort.Value{in}, outSpecs); err != nil {
		for _, v := range outSpecs {
			if v != nil {
				_ = v.Destroy()
			}
		}
		return nil, &Refusal{
			What:   "the ONNX Runtime could not run the model",
			Detail: err.Error(),
		}
	}
	defer func() {
		for _, v := range outSpecs {
			if v != nil {
				_ = v.Destroy()
			}
		}
	}()

	// The tensor's OWN shape is read back from the runtime rather than trusted from
	// the declaration. A model whose static shape disagrees with what it returns is
	// not hypothetical — a dynamic-axis export declares -1 for the record count —
	// and reading back is the only way the decoder ever sees the real layout.
	out := make([]Tensor, 0, len(outSpecs))
	for i, v := range outSpecs {
		t, ok := v.(*ort.Tensor[float32])
		if !ok {
			return nil, &Refusal{
				What:   "a model output was not float32",
				Detail: sprintf("%s: this decoder reads float32 outputs only", s.outputs[i].Name),
			}
		}
		shape := t.GetShape()
		out = append(out, Tensor{Name: s.outputs[i].Name, Dims: shape, Data: t.GetData()})
	}
	return out, nil
}

// inputShape is the tensor shape in the layout the MODEL declares.
//
// REDCHECK RC-30. It is not always (1, C, H, W), and the difference is not cosmetic.
//
// This function's caller used to build (1, C, H, W) unconditionally while accepting a
// `layout` argument it immediately discarded with `_ = layout`. The session had already
// worked out the layout from the model's own declared dims and recorded it — it was in
// `Input().Layout`, in the run report and in `fixtureprobe -detector`'s output — and then
// the tensor was laid out in a different order from the one the model declared.
//
// The symptom on an NHWC export is a runtime error reading `index: 1 Got: 3 Expected: 416`,
// which is a real and legible failure. The reason it matters anyway is the class of bug
// next door: the layout was RECORDED correctly and APPLIED wrongly, so every artefact
// describing the model said `nhwc` while the bytes fed to it were NCHW. A recorded fact
// nobody acts on is the failure this module exists to end, and here it was hiding inside
// the module that exists to catch it.
func (s *Session) inputShape(n int) (ort.Shape, error) {
	c, h, w := int64(s.input.Channels()), int64(s.input.H), int64(s.input.W)
	var shape ort.Shape
	switch s.input.Layout {
	case "nhwc":
		shape = ort.NewShape(1, h, w, c)
	case "nchw", "":
		shape = ort.NewShape(1, c, h, w)
	default:
		return nil, &Refusal{
			What: "the model's input layout is neither nchw nor nhwc",
			Detail: sprintf("%q was detected for %s, so this module cannot lay the tensor out for it. "+
				"Transpose the model at export time rather than guessing here: a wrong guess produces a "+
				"tensor of the right SIZE that the model reads in the wrong order, which decodes to boxes "+
				"that are plausible and wrong", s.input.Layout, s.input.Name),
		}
	}
	if want := int(c * h * w); want != n {
		return nil, &Refusal{
			What: "the input tensor is the wrong size for this model",
			Detail: sprintf("%s declares %d x %d in %s, which is %d floats, and %d were supplied. "+
				"The caller's layout disagrees with the model's", s.input.Name, h, w, s.input.Layout, want, n),
		}
	}
	return shape, nil
}

// Channels is the channel count implied by the model's own layout.
func (i InputSpec) Channels() int {
	switch i.Layout {
	case "nchw":
		return 3
	case "nhwc":
		return 3
	}
	return 3
}

// preallocateOutputs allocates the output tensors, or declines to.
//
// REDCHECK RC-29. It DECLINES — handing the runtime a slice of nils so the RUNTIME
// allocates — whenever any declared output dimension is not a positive number.
//
// Declining is not a fallback and it is not an optimisation. A model exported with
// dynamic axes publishes declared output dims of -1, and a -1 cannot be sized: this
// module used to refuse such a model outright with "an output tensor could not be
// allocated", which means the committed detector could not be run at all, on either
// provider. That is the common shape for a modern YOLO export, not an exotic one, so
// the old behaviour was not a safety check — it was an inability wearing a refusal's
// clothes, and it failed while claiming to be protecting something.
//
// The returned bool is TRUE when the runtime is allocating, and the caller must then
// not pre-destroy: the binding writes the tensors it creates back into the slice it
// was given, so the same slice holds the results.
//
// The two branches are kept as two functions rather than unified because there is no
// way to unify them: a concrete shape is cheaper to pre-allocate and a symbolic one is
// not allocatable at all.
func preallocateOutputs(outputs []OutputSpec) ([]ort.Value, bool) {
	if hasSymbolicDims(outputs) {
		// REDCHECK RC-29: this branch IS the control. Removing it puts the refusal
		// back and the committed detector cannot be run on either provider.
		return make([]ort.Value, len(outputs)), true
	}
	return preallocateConcrete(outputs), false
}

// hasSymbolicDims reports whether any declared output dimension is not a positive
// number.
//
// A dimension of -1 is what an export with dynamic axes publishes, and it carries no
// size: the count of anchors depends on the input, and the export does not know the
// input. There is no number to pre-allocate and no conservative guess, because a
// conservative guess is a small buffer and a small buffer is a truncated frame.
func hasSymbolicDims(outputs []OutputSpec) bool {
	for _, o := range outputs {
		for _, d := range o.Dims {
			if d <= 0 {
				return true
			}
		}
	}
	return false
}

// preallocateConcrete allocates every declared output. A nil entry means the
// allocation was refused, which [Session.Infer] reports by name rather than running
// with a missing output.
func preallocateConcrete(outputs []OutputSpec) []ort.Value {
	specs := make([]ort.Value, 0, len(outputs))
	for _, o := range outputs {
		t, err := ort.NewTensor(ort.NewShape(o.Dims...), make([]float32, shapeSize(o.Dims)))
		if err != nil || t == nil {
			for _, v := range specs {
				_ = v.Destroy()
			}
			return nil
		}
		specs = append(specs, t)
	}
	return specs
}

// Close releases the session, the options and the ORT environment.
//
// The environment is a process-wide singleton and it is destroyed LAST, because
// tearing it down while another session is still open is a use-after-free in C.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	if s.sess != nil {
		if err := s.sess.Destroy(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.opts != nil {
		if err := s.opts.Destroy(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := ort.DestroyEnvironment(); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return fmt.Errorf("ortdetect: closing the session: %w", firstErr)
	}
	return nil
}

func namesOf(infos []ort.InputOutputInfo) []string {
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.Name)
	}
	return out
}

func shapeSize(dims []int64) int64 {
	n := int64(1)
	for _, d := range dims {
		if d <= 0 {
			return 0
		}
		n *= d
	}
	return n
}
