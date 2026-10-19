package ortdetect

import (
	"sync"
	"time"
)

// The detector: one frame in, boxes and telemetry out.
//
// This is the seam the pipeline uses. It exists as a type rather than as a
// function for three reasons, each of which is a property this module needs to be
// able to state:
//
//   - it HOLDS the latency distribution, so the throughput gate and the report read
//     one measurement rather than two;
//   - it holds the model's IDENTITY, so a decision made from a box it produced can
//     name the file, hash and licence that file came from;
//   - it is SAFE for one caller, which is the frame loop, and says so rather than
//     leaving it to be discovered.
//
// # Why this is not `soak.Trigger`
//
// The plan expected the detector to implement `soak.Trigger`, and that would have
// been free of pipeline changes. It cannot work, and the reason is worth writing
// down because it is the kind of thing that looks fine in a diff.
//
// `soak.Trigger.Candidates` returns `[]novelty.Candidate`, and
// `Runner.measureTrigger` builds each box from `c.Box` while assigning
// `Class: r.triggerClass()` — ONE class, from configuration, for every box on the
// frame. That is correct for a novelty trigger, which measures a change-fraction
// and genuinely has no class to report. Routing a detector through it would
// discard every class the model produced and label a bus and a pedestrian with the
// same word, which is the one failure a reader cannot catch by looking at the
// picture.
//
// So `soak` gains a [soak.Detector] interface with the shape a detector actually
// has — per-frame, with telemetry — and the novelty trigger is left exactly as it
// was. One interface, one branch, and the class survives.

// Config is what a detector needs, all of it declared by the run.
type Config struct {
	// Session is an opened session. Ownership stays with the caller: [Detector]
	// does not close it, because a run may want to print the model's identity after
	// the detector has stopped.
	Session *Session
	// EmitScore, MaxBoxes, NMSClass, NMSGlobal and MinBoxArea are SP_POC_DET_SCORE,
	// SP_POC_DET_MAX_BOXES, the two NMS thresholds and SP_POC_MIN_BOX_AREA.
	Decode DecodeConfig
	// LatencyWindow is how many recent frames the percentiles cover. Zero takes
	// [DefaultLatencyWindow].
	LatencyWindow int
	// InputSize is SP_POC_DET_INPUT, cross-checked against the model's declared
	// input at construction rather than at the first frame.
	InputSize int
	// Vocabulary is the class list the boxes are named from.
	//
	// Nil means the SESSION's vocabulary, which in turn defaults to the
	// built-in eighty-name list. It is a separate field so a caller can name
	// the classes the session found without opening the session again --
	// and so a mismatch between the two is a caller's mistake rather than a
	// silent fallback to a table of eighty.
	Vocabulary *Vocabulary
}

// Detector runs one model over frames, and remembers what it cost.
type Detector struct {
	sess   *Session
	decode DecodeConfig
	lat    *Latencies

	// mu guards the reusable buffers. The frame loop is the only caller, but the
	// latency collector and the session are both shared state and a run that later
	// adds a second consumer must not find a data race in the detector.
	mu     sync.Mutex
	tensor []float32
	// cropBuf is the reusable extraction buffer for a crop pass. It is guarded by mu
	// for the same reason `tensor` is: two detections must not share it, and the
	// session and the latency collector are shared state.
	cropBuf []byte
	geom    Geometry
	input   InputSpec
	classes int
}

// NewDetector validates the configuration against the session and returns a
// detector.
//
// Everything checkable without a frame is checked HERE. A model whose declared
// input does not match SP_POC_DET_INPUT is a mistake in the configuration, and it
// costs nothing to find at construction; it costs a demo to find at frame 3000.
func NewDetector(cfg Config) (*Detector, error) {
	if cfg.Session == nil {
		return nil, &Refusal{
			What: "the detector was given no session",
			Detail: "open one with ortdetect.Open and pass it here; a detector with no model would " +
				"report zero detections on every frame",
		}
	}
	if err := cfg.Session.CheckInputSize(cfg.InputSize); err != nil {
		return nil, err
	}
	if cfg.Decode.EmitScore < 0 || cfg.Decode.EmitScore > 1 {
		return nil, &Refusal{
			What: sprintf("SP_POC_DET_SCORE=%v is not a probability", cfg.Decode.EmitScore),
			Detail: "the model's emit floor must be a probability in [0,1]; it is not the approval bar, " +
				"which is SP_POC_SCORE_THRESHOLD and belongs to the run",
		}
	}
	if cfg.Decode.MaxBoxes < 1 {
		return nil, &Refusal{
			What: sprintf("SP_POC_DET_MAX_BOXES=%d", cfg.Decode.MaxBoxes),
			Detail: "a cap of zero emits nothing and reports a detection rate of zero over a road with " +
				"traffic on it, which is a confident zero rather than an honest one",
		}
	}
	in := cfg.Session.Input()
	// The class list the boxes are named from: the caller's, the session's, or
	// the built-in one. The order matters -- a caller's vocabulary wins, because
	// a caller that names its classes is the one that knows them.
	vocabulary := cfg.Vocabulary
	if vocabulary == nil {
		vocabulary = cfg.Session.Vocabulary()
	}
	decode := cfg.Decode
	decode.Vocabulary = vocabulary
	d := &Detector{
		sess:   cfg.Session,
		decode: decode,
		lat:    NewLatencies(cfg.LatencyWindow),
		input:  in,
	}
	// The class count is read from the model's own metadata where it has any, and
	// zero otherwise. Zero is a real and common value — the export measured in this
	// repository carries none — and it selects the COCO-80 field arithmetic. It is
	// not defaulted to 80 and reported as though the model had said so.
	d.classes = 0
	return d, nil
}

// Answer is one frame's result.
//
// The three fields a caller must not confuse: Boxes is what the model found,
// State is whether the pipeline actually looked (and [StateFor] decides that, not
// the caller), and Telemetry is what the decoder did on the way there. A run that
// reports only the first has reported nothing actionable.
type Answer struct {
	Seq uint64
	// Boxes is normalised against the FRAME, never against the model's input.
	Boxes []Box
	// State is one of the four words: asked_answered, no_objects, degraded,
	// not_asked. It is derived from Refusal and len(Boxes) by [StateFor] and never
	// set by hand, because a caller that sets it can set it wrong.
	State string
	// Telemetry is the decoder's arithmetic, including the geometry it used.
	Telemetry Telemetry
	// LatencyMS is THIS frame's inference cost, wall clock, and it is recorded even
	// on a refusal — a session that errors in 40ms is a fast failure and still a
	// cost the run paid.
	LatencyMS float64
	// Refusal is non-nil when the frame could not be measured. A refusal is not a
	// zero and not a degraded run: it is a named reason, and it is what makes a
	// detection rate of zero diagnosable instead of merely observable.
	Refusal *Refusal
	// Crop is the region that was measured, and it is the empty Rect for a
	// whole-frame pass.
	//
	// It is on the answer rather than supplied by the caller because the caller's
	// rectangle is a REQUEST and this one is what was actually measured, after
	// clamping. A crop that ran off the edge of the frame was measured on less than
	// was asked for, and a caller reading back its own request would print the wrong
	// one. Per invariant 2 it is stored: a report that says "found in a 200x150 crop"
	// has to be able to name which crop, and re-deriving it from a rounded region is a
	// re-inference.
	Crop Rect
}

// Detect measures one frame, or one crop of one frame.
//
// crop is the region to look at and is the zero Rect meaning the whole frame. It is a
// parameter and not a second method because there is exactly ONE implementation of the
// crop path, and a second entry point would be a second place for the letterbox, the
// decode and the mapping back to disagree. The zero Rect means the whole frame, so a
// caller with no crop pass passes `Rect{}` and this is behaviourally identical to what
// Detect did before the parameter existed.
//
// The order is fixed and each step can refuse by name:
//
//	extract     -> the crop's pixels, contiguous, or a refusal naming the frame
//	letterbox   -> the model's own layout, with the content geometry
//	infer       -> the model, with the output shapes READ BACK from the runtime
//	decode      -> boxes in CROP coordinates, or a refusal naming the axis
//	uncrop      -> boxes in FRAME coordinates
//
// The output shapes are read back rather than trusted from the declaration, because
// a dynamic-axis export declares -1 for its record count and the declaration is then a
// lie the decoder would act on.
//
// The caller owns the ordering of a whole-frame pass against a crop pass, because
// only the caller knows whether a crop is worth an inference, and the answer depends
// on what the whole-frame pass already found.
func (d *Detector) Detect(seq uint64, rgb []byte, frameW, frameH int, crop Rect) Answer {
	started := time.Now()
	ans := Answer{Seq: seq}

	if len(rgb) == 0 || frameW <= 0 || frameH <= 0 {
		// `not_asked`, and NOT derived through [StateFor]. StateFor maps a refusal to
		// `degraded`, which is right for a session that was asked and could not be
		// read — and wrong here, because nothing was asked. The frame never arrived,
		// so the model was never called, and the pipeline's own convention says so
		// too: runner.go answers `not_asked` for a frame with no pixels.
		//
		// The difference is the whole of the four-word vocabulary. `degraded` says we
		// looked and could not read; `not_asked` says we did not look. A run that
		// conflates them reports a camera fault as a model fault, and its reader
		// goes looking in the wrong place.
		ans.Refusal = &Refusal{
			What: sprintf("frame %d carries no pixels (%d bytes, %dx%d)", seq, len(rgb), frameW, frameH),
			Detail: "the detector cannot measure a frame it was not given, and no model was called. " +
				"This is not_asked, not no_objects and not degraded: nothing was looked at, which is a " +
				"different fact from looking and finding nothing, and a different fact again from looking " +
				"and failing to understand",
		}
		ans.State = StateNotAsked
		ans.LatencyMS = msSince(started)
		d.lat.Observe(ans.LatencyMS)
		return ans
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	in := d.input
	need := in.W * in.H * 3
	if cap(d.tensor) < need {
		d.tensor = make([]float32, need)
	}
	buf := d.tensor[:need]

	// The crop is extracted first, and everything downstream is then UNCHANGED: the
	// letterbox sees a plain packed RGB buffer whose width is the crop's, the decoder
	// sees the crop's own geometry, and the result is normalised against the CROP. That
	// is what makes the crop path a whole-frame pass over a smaller picture rather than
	// a second kind of measurement.
	src, viewW, viewH := rgb, frameW, frameH
	if !crop.Empty() {
		rect, cerr := crop.Clamp(frameW, frameH)
		if cerr != nil {
			ans.Refusal = toRefusal(cerr)
			ans.State = StateFor(ans.Refusal, 0)
			ans.LatencyMS = msSince(started)
			d.lat.Observe(ans.LatencyMS)
			return ans
		}
		bufRGB, eerr := rect.Extract(rgb, frameW, frameH, d.cropBuf)
		if eerr != nil {
			ans.Refusal = toRefusal(eerr)
			ans.State = StateFor(ans.Refusal, 0)
			ans.LatencyMS = msSince(started)
			d.lat.Observe(ans.LatencyMS)
			return ans
		}
		d.cropBuf = bufRGB
		src, viewW, viewH, crop = bufRGB, rect.W, rect.H, rect
	}

	tensor, geom, err := Letterbox(src, viewW, viewH, in.W, in.H, in.Layout)
	if err != nil {
		ans.Refusal = toRefusal(err)
		ans.State = StateFor(ans.Refusal, 0)
		ans.LatencyMS = msSince(started)
		d.lat.Observe(ans.LatencyMS)
		return ans
	}
	copy(buf, tensor)
	d.geom = geom

	outputs, err := d.sess.Infer(buf, in.Layout)
	if err != nil {
		ans.Refusal = toRefusal(err)
		ans.State = StateFor(ans.Refusal, 0)
		ans.LatencyMS = msSince(started)
		d.lat.Observe(ans.LatencyMS)
		return ans
	}
	if len(outputs) == 0 {
		ans.Refusal = &Refusal{
			What: "the model returned no outputs at all",
			Detail: "a session with zero outputs cannot be measured against; the run's declared input and " +
				"the model's graph disagree",
		}
		ans.State = StateFor(ans.Refusal, 0)
		ans.LatencyMS = msSince(started)
		d.lat.Observe(ans.LatencyMS)
		return ans
	}

	// The FIRST output is the detection head. A fused detector publishes one; an
	// anchored darknet head publishes one per stride and is refused by name rather
	// than decoded as if it were fused, because its coordinates are offsets from
	// anchor boxes that live in the model's .cfg and not in the ONNX graph.
	if len(outputs) != 1 {
		ans.Refusal = errAnchored(shapeOf(outputs))
		ans.State = StateFor(ans.Refusal, 0)
		ans.LatencyMS = msSince(started)
		d.lat.Observe(ans.LatencyMS)
		return ans
	}

	res := Decode(outputs[0], geom, d.classes, d.decode)
	ans.Boxes = res.Boxes
	ans.Telemetry = res.Telemetry
	ans.Refusal = res.Refusal
	// The boxes come back normalised against the CROP, and everything downstream in
	// this module, the tracker, the report, the pushed event and the stored decision, is
	// normalised against the FRAME. Uncropping happens HERE and nowhere else, on the one
	// line, so there is no second mapping to get wrong. See [Rect.Uncrop] for why it is
	// a named method rather than three lines of arithmetic.
	if !crop.Empty() {
		for i := range ans.Boxes {
			ans.Boxes[i] = crop.Uncrop(ans.Boxes[i], frameW, frameH)
		}
	}
	// The geometry is attached to the answer rather than left to the caller to
	// correlate, so a report printing a detection rate can print beside it the
	// model's input, the content size and the pad that produced those boxes.
	ans.Telemetry.InputW, ans.Telemetry.InputH = geom.InputW, geom.InputH
	ans.Telemetry.ContentW, ans.Telemetry.ContentH = geom.ContentW, geom.ContentH
	ans.Telemetry.PadX, ans.Telemetry.PadY = geom.PadX, geom.PadY
	ans.Crop = crop
	ans.State = StateFor(ans.Refusal, len(ans.Boxes))
	ans.LatencyMS = msSince(started)
	d.lat.Observe(ans.LatencyMS)
	return ans
}

// Latencies returns the live distribution, for the preflight gate and the report.
//
// The same object, not a copy: a caller that copied it would be able to report a
// percentile that the detector never measured.
func (d *Detector) Latencies() *Latencies { return d.lat }

// Identity returns what a decision made from this detector's boxes must name.
func (d *Detector) Identity() Identity { return d.sess.Identity() }

// Session returns the session this detector runs on, so the owner that opened it
// can close it.
//
// The detector does not close it on Close-of-its-own because the run may want to
// print the model's identity after the detector has stopped, and because ownership
// of a process-wide ORT environment belongs to whoever initialised it.
func (d *Detector) Session() *Session { return d.sess }

// Input returns the model's declared input, for the banner and the report.
func (d *Detector) Input() InputSpec { return d.input }

// DeclareClassCount records how many class names the model itself carries.
//
// It is a method rather than a Config field because it comes from the MODEL, not
// from the run: a caller reads it off the model's metadata and hands it over, and
// the value is reported on every decode so a reader can tell "the model named 80
// classes" from "this build assumed COCO-80 because the model named none".
func (d *Detector) DeclareClassCount(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.classes = n
}

// ClassCount returns what the detector believes the model's class count to be, and
// whether it was declared or assumed.
func (d *Detector) ClassCount() (n int, declared bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.classes, d.classes > 0
}

// Seen returns how many frames this detector has been asked to measure.
//
// It is the denominator for a detection rate. A rate computed without it — or
// computed over frames the detector never saw — is the confusion this package
// exists to end, so the count is on the detector rather than in the report.
func (d *Detector) Seen() uint64 {
	count := d.lat.Count()
	// The count is a length, so it cannot be negative; the check is here
	// because a reader of this file cannot know that and a narrowing
	// conversion that looks unchecked is one somebody has to re-derive.
	if count < 0 {
		return 0
	}
	return uint64(count)
}

func shapeOf(ts []Tensor) [][]int64 {
	out := make([][]int64, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Dims)
	}
	return out
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000
}
