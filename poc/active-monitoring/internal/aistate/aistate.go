// Package aistate is the sidecar's answer vocabulary, and nothing else.
//
// It exists because two packages need these five words and neither may depend on
// the other. `internal/ai` speaks them to the wire; `internal/detect` counts them
// when it folds a frame into its run-level counters. A shared vocabulary that
// only one of them can see has to be declared twice, and a vocabulary declared
// twice is a vocabulary that drifts — and a drift here is not a compile error, it
// is a run that counts `degraded` frames as `asked_answered` and reports an
// invented detection rate.
//
// So the words live here, below both, in a package that imports nothing. That is
// the same shape as `internal/classify` and for the same reason: it is the
// vocabulary the other packages agree on, so a vocabulary package that reached
// for the store or the wire would make the agreement depend on the thing it
// describes.
//
// These are CONTRACT.md §2.2's four states plus the one this module adds. The
// wire adds no sixth: a sidecar that cannot describe its own decode says
// `cannot_tell`, it does not say `answered` because its boxes looked plausible.
package aistate

// The five states. `Asked` is what `internal/ai` has always called
// `asked_answered`; the wire spelling is unchanged.
const (
	// Asked: the frame was sent, the sidecar ran the model, and the answer was
	// interpretable. The boxes below it may be zero.
	Asked = "asked_answered"
	// NoObjects: asked, and the answer was an empty array. This is a real answer
	// and NOT a failure — it is the only state in which "the model found
	// nothing" is a statement about the scene.
	NoObjects = "no_objects"
	// Degraded: asked, and the answer was late, malformed, or came from a failing
	// read. NEVER a negative answer.
	Degraded = "degraded"
	// NotAsked: the frame was never sent — backpressure, a busy worker, a gap.
	// NEVER a negative answer.
	NotAsked = "not_asked"
	// CannotTell: the response was structurally valid and not interpretable.
	// NEVER a negative answer.
	CannotTell = "cannot_tell"
)

// All is the closed set, in the order CONTRACT.md §2.2 lists them.
var All = []string{Asked, NoObjects, Degraded, NotAsked, CannotTell}

// Valid reports whether a state is one of the five.
//
// It exists so a state arriving from the wire is CHECKED at the layer that reads
// it, rather than being compared against a literal at each of the dozen places
// that switch on it. An unknown state is not a sixth answer; it is a protocol
// error, and this is the one function that says so.
func Valid(s string) bool {
	for _, k := range All {
		if k == s {
			return true
		}
	}
	return false
}

// IsAnswered reports whether a state means "the model ran and this frame can be
// counted as an observation".
//
// It is `Is`-prefixed and not `Answered` because the wire word is
// `asked_answered`, and a predicate named `Answered` next to a constant named
// `Asked` is a package where `aistate.Answered` is ambiguous at every call site.
//
// The three that are NOT: a frame that was never sent, one whose answer was
// late or malformed, and one that arrived but could not be interpreted. Folding
// any of those into a detection RATE is how a run with a dead sidecar reports
// zero detections per frame as though the road were empty — which is the most
// expensive sentence this module can print.
func IsAnswered(s string) bool { return s == Asked || s == NoObjects }
