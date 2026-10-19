// Package ortdetect is the public face of the in-process ONNX detector.
//
// The implementation is `github.com/Sergey-Bar/vision/internal/ortdetect`, and
// it is internal because a detector that opens a native session and runs a
// model over camera frames is a thing the product wires deliberately, not a
// thing any caller may reach for.
//
// The POC module needs the same detector -- it is the thing that proved the
// decoder, the refusal states and the CUDA preflight -- so it comes here, over
// the module boundary, as aliases of the same types.
package ortdetect

import "github.com/Sergey-Bar/vision/internal/ortdetect"

// The detector's surface.
type (
	// Answer is what one frame produced.
	Answer = ortdetect.Answer
	// Box is a detected region. It is the same type the sidecar wire and the
	// tracker use, so a box crosses every seam without a conversion.
	Box = ortdetect.Box
	// Check is one preflight result.
	Check = ortdetect.Check
	// Config opens a detector.
	Config = ortdetect.Config
	// DecodeConfig is the decoder's tunable surface.
	DecodeConfig = ortdetect.DecodeConfig
	// DecodeResult is what a decoded tensor produced.
	DecodeResult = ortdetect.DecodeResult
	// Detector is an open session plus the state that runs frames through it.
	Detector = ortdetect.Detector
	// DeviceCheck is the CUDA device confirmation.
	DeviceCheck = ortdetect.DeviceCheck
	// Geometry is the letterbox geometry a decode is mapped back through.
	Geometry = ortdetect.Geometry
	// Identity is the model's identity: path, digest, provider, device.
	Identity = ortdetect.Identity
	// InputSpec is the model's declared input.
	InputSpec = ortdetect.InputSpec
	// Latencies is the bounded window of measured latencies.
	Latencies = ortdetect.Latencies
	// Layout is the axis order the model declared.
	Layout = ortdetect.Layout
	// Objectness is the three-state answer about an objectness channel.
	Objectness = ortdetect.Objectness
	// OutputSpec is the model's declared output.
	OutputSpec = ortdetect.OutputSpec
	// Rect is a crop rectangle.
	Rect = ortdetect.Rect
	// Refusal is a named refusal, with a reason and an offer.
	Refusal = ortdetect.Refusal
	// Session is an open ONNX Runtime session.
	Session = ortdetect.Session
	// SessionConfig opens a session.
	SessionConfig = ortdetect.SessionConfig
	// Telemetry is what the detector reports about a frame.
	Telemetry = ortdetect.Telemetry
	// Tensor is a decoded output tensor.
	Tensor = ortdetect.Tensor
)

// The absence vocabulary the detector shares with the sidecar.
const (
	StateAsked     = ortdetect.StateAsked
	StateNoObjects = ortdetect.StateNoObjects
	StateDegraded  = ortdetect.StateDegraded
	StateNotAsked  = ortdetect.StateNotAsked
)

// The preflight check states.
const (
	StatePass   = ortdetect.StatePass
	StateFail   = ortdetect.StateFail
	StateNotRun = ortdetect.StateNotRun
)

// The axis orders a model can declare.
const (
	LayoutFieldsLast  = ortdetect.LayoutFieldsLast
	LayoutFieldsFirst = ortdetect.LayoutFieldsFirst
)

// The constants.
const (
	// DefaultLatencyWindow is the default bounded latency window.
	DefaultLatencyWindow = ortdetect.DefaultLatencyWindow
	// NoCgoRefusal is what a build without cgo says, by name, instead of
	// pretending the detector is available.
	NoCgoRefusal = ortdetect.NoCgoRefusal
	// NumClasses is the size of the built-in vocabulary.
	NumClasses = ortdetect.NumClasses
)

// The functions.
var (
	// ClassName is the name of a class index.
	ClassName = ortdetect.ClassName
	// Decode turns a tensor into boxes.
	Decode = ortdetect.Decode
	// DecideObjectness is the three-state objectness decision.
	DecideObjectness = ortdetect.DecideObjectness
	// DetectLayout reads the axis order out of a declared shape.
	DetectLayout = ortdetect.DetectLayout
	// FieldsFor is the field arithmetic the decoder uses.
	FieldsFor = ortdetect.FieldsFor
	// HashModel is the model's digest and size.
	HashModel = ortdetect.HashModel
	// InputSize reads the declared input size out of a shape.
	InputSize = ortdetect.InputSize
	// Letterbox maps a frame onto the model's input.
	Letterbox = ortdetect.Letterbox
	// NewDetector opens a detector.
	NewDetector = ortdetect.NewDetector
	// NewLatencies opens a bounded latency window.
	NewLatencies = ortdetect.NewLatencies
	// NormalizeEP normalizes an execution provider name.
	NormalizeEP = ortdetect.NormalizeEP
	// Open opens a session.
	Open = ortdetect.Open
	// Overlaps reports whether two boxes overlap.
	Overlaps = ortdetect.Overlaps
	// Preflight checks a configuration without opening a session.
	Preflight = ortdetect.Preflight
	// PreflightProviders checks the providers a configuration would use.
	PreflightProviders = ortdetect.PreflightProviders
	// PrependLibraryDirToPATH makes a runtime library findable.
	PrependLibraryDirToPATH = ortdetect.PrependLibraryDirToPATH
	// StateFor names the absence state a refusal or an empty answer implies.
	StateFor = ortdetect.StateFor
	// ValidateEP refuses an execution provider this build cannot honour.
	ValidateEP = ortdetect.ValidateEP
)
