// The sidecar's self-report, and the tripwire that reads it. T4.1 and T4.2.
//
// The telemetry exists because of what §4 of the plan found. Every one of the four
// bugs produced a run that looked FINE: green tests, a clean report, no error, and
// a picture with boxes on it. Nothing in the old wire format said "these boxes are
// all the same box" or "these coordinates were treated as fractions when they were
// pixels" — so there was nothing for a run to notice and nothing for a reader to
// check. The telemetry is the sidecar saying what it did, in numbers, on every
// frame, and the tripwire is the run refusing to interpret an answer it cannot
// trust.
//
// The vocabulary is the five words already in CONTRACT.md §2.2 and the tripwire
// only ever adds `cannot_tell` where the old code would have added
// `asked_answered`. That is the whole of the change in behaviour: the four bugs
// did not produce a degraded run, they produced a HEALTHY run with nonsense in
// it, and the only thing that could have caught them was a healthy run being
// required to describe its own arithmetic.

package ai

import "fmt"

// Telemetry is the sidecar's report about how it decoded. Every field is present
// on every answer, including an empty one: a missing key and a zero are different
// facts, and the tripwire below can only fire on a key that is there.
//
// It is decoded with `DisallowUnknownFields` like the rest of the response, so a
// sidecar that grows a key this build has never heard of is a protocol error
// rather than a silently dropped field. That is deliberate: a dropped field is a
// number the run cannot check, and a run that cannot check a number is the
// situation this file exists to end.
type Telemetry struct {
	// Layout is the input layout the model declared: nchw or nhwc. A picture
	// decoded with the wrong one is confident nonsense.
	Layout string `json:"layout"`
	// DecoderFamily is `fused` (one output, final coordinates) or `anchored` (one
	// output per stride, coordinates are offsets from per-cell anchor boxes).
	DecoderFamily string `json:"decoder_family"`
	// CoordSpace is `pixel`, `fraction`, or `unknown`. `unknown` is the tripwire's
	// trigger: the sidecar could not tell which unit the coordinates were in, and
	// a box in an unknown unit has no place on the picture.
	CoordSpace string `json:"coord_space"`
	// HasObjectness is a POINTER, because three states matter and a bool has two.
	// false: the classes start at column 4 (a YOLOv8's 84 fields). true: there is
	// an objectness at column 4 (85 fields). null: the field count matches
	// neither, and every class index would shift by one — so the frame is refused
	// rather than answered with the wrong words on the right boxes.
	HasObjectness *bool `json:"has_objectness"`
	// Fields is the measured width of one detection's record, counted from the
	// tensor rather than from a constant. It is printed beside the model name
	// because "the export has 85 fields" resolves most "why are the classes wrong"
	// questions in one sentence.
	Fields int `json:"fields"`
	// Classes is how many class names the model carried in its own metadata. Zero
	// is a real and common value — the measured model in this repository carries
	// none and the sidecar falls back to the COCO-80 vocabulary — and the report
	// prints it, because a fallback is a fact about the answer.
	Classes int `json:"classes"`
	// InputW and InputH are the MODEL's input size, which is NOT the frame's
	// size and never was: the frame is scaled and letterboxed into this. They
	// were added because the sidecar had already started sending them and this
	// struct refuses unknown fields — which refused EVERY frame of a real run and
	// made the POC detect nothing at all, with a report that said `cannot_tell`
	// 169 times and no more.
	//
	// They earn their place on the screen for one specific reason: a square
	// input (416x416) on a 16:9 frame means the top and bottom fifth of the
	// picture are letterbox, and every box near the edge of the frame sits inside
	// that dead band. Seeing the model's input next to the frame's own width is
	// what makes that arithmetic checkable rather than folklore.
	InputW int `json:"input_w"`
	InputH int `json:"input_h"`

	// Candidates is how many raw candidates the model emitted, before any filter.
	// PreNms is how many cleared the emit floor. PostNmsClass and PostNms are the
	// two suppression passes' outputs. The four together are what make "the model
	// found nothing" distinguishable from "the decoder threw everything away": a
	// candidate count of 8112 with a pre-NMS of 0 means the model did not fire,
	// and a pre-NMS of 41 with a post-NMS of 0 means the decoder did.
	Candidates   int `json:"candidates"`
	PreNms       int `json:"pre_nms"`
	PostNmsClass int `json:"post_nms_class"`
	PostNms      int `json:"post_nms"`
	// DroppedDegenerate counts boxes with no area, and DroppedTiny counts boxes
	// below the minimum area or outside the plausible aspect range. Both are
	// counted rather than silently dropped: a scale bug shows up here as a large
	// number, and a picture with no boxes is otherwise indistinguishable from a
	// quiet scene.
	DroppedDegenerate int `json:"dropped_degenerate"`
	DroppedTiny       int `json:"dropped_tiny"`
	// NMSClassIoU and NMSGlobalIoU are the two suppression thresholds, echoed so
	// a report can name them. A threshold nobody can see is a threshold nobody
	// can argue with.
	NMSClassIoU  float64 `json:"nms_class_iou"`
	NMSGlobalIoU float64 `json:"nms_global_iou"`

	// MedianBoxArea is the median box area as a FRACTION of the frame. This is the
	// plan's §4 step 5 number: it was about 0.25 — a quarter of the picture per
	// detection, the signature of the collapsed decode — and it should now be
	// under 0.10. A tenth of a frame is already an enormous object.
	MedianBoxArea float64 `json:"median_box_area"`
	// MedianObjectPx is the median box HEIGHT in the frame's own pixels. It is the
	// number a person can check against the picture without a ruler, and it turns
	// "the boxes are wrong" into "the boxes are three pixels tall".
	MedianObjectPx float64 `json:"median_object_px"`
	// IdenticalGeometry is how many emitted boxes share EXACTLY the same geometry
	// as another box. Zero is the answer for a decoder that is working; eight was
	// the answer for the broken one, and the plan names the fall from 8 to 0 as the
	// proof. A correct decoder CAN emit two identical boxes on a genuinely
	// ambiguous frame — a car under a bridge, a reflection in a wet road — so this
	// is a tripwire at a threshold rather than a gate at zero.
	IdenticalGeometry int `json:"identical_geometry"`
	// Scales is how many stride outputs an anchored export published: one for a
	// fused export, three for the measured zoo export.
	Scales int `json:"scales"`
	// Note is the sidecar's own words when it could not decode, in a sentence an
	// operator can act on. It is on the wire rather than in a log because the log
	// is not attached to the frame, and a frame with no diagnosis in it is a frame
	// that costs an hour.
	Note string `json:"note"`
}

// Seen reports whether the sidecar sent a telemetry block at all.
//
// It is a separate question from "are the fields zero", and it is what separates
// an old sidecar that predates the telemetry from a new sidecar that decoded
// nothing. A reply with NO telemetry is `cannot_tell` with the note saying the
// sidecar did not report how it decoded — never `asked_answered` on the grounds
// that the boxes looked plausible, because a plausible box from a sidecar that
// cannot describe its own arithmetic is the exact failure this file exists for.
func (t Telemetry) Seen() bool {
	return t.CoordSpace != "" || t.Fields != 0 || t.Candidates != 0 || t.Note != ""
}

// identicalGeometryLimit is the tripwire threshold.
//
// Two is chosen over zero for the reason in the field comment: refusing a correct
// decoder's legitimate duplicates would make the run report `cannot_tell` for a
// scene it handled properly. Above two the answer is about the arithmetic and not
// about the scene — the broken decoder produced EIGHT, every one of them
// identical, on every frame of every run.
const identicalGeometryLimit = 2

// medianObjectFloorPx is the lower tripwire on object height.
//
// It is tiny on purpose. The broken decode made objects enormous (230 px) rather
// than tiny, so this check is here to catch the OTHER end of the same class of
// bug — a scale error in the small direction — and to do it without a threshold
// anybody would have to argue about. Three pixels is not an object at 640x384; it
// is a compression artefact, and a line crossing computed from one is a crossing
// that never happened.
const medianObjectFloorPx = 3.0

// Interpret is T4.2: the tripwire, applied to one sidecar answer.
//
// It is a FREE FUNCTION over the decoded reply rather than a method, because the
// decision belongs to the run and not to the wire. The client moves bytes and
// checks the shape; this decides what the answer MEANS.
func Interpret(seq uint64, t Telemetry, boxes []Box) (state string, reason string) {
	if !t.Seen() {
		return StateCannotTell, fmt.Sprintf(
			"frame %d: the sidecar sent no telemetry, so how it decoded is unstated. A plausible box from a "+
				"sidecar that cannot describe its own arithmetic is the failure this check exists for, and a box "+
				"from it must not be counted as an object", seq)
	}
	// The coordinate space. This is the check D2 needed and did not have:
	// `unknown` means the sidecar could not tell whether the model's coordinates
	// were pixels or fractions, and every box below is therefore in a unit nobody
	// can name.
	if t.CoordSpace != "pixel" && t.CoordSpace != "fraction" {
		return StateCannotTell, fmt.Sprintf(
			"frame %d: the sidecar could not tell whether this export's coordinates are input pixels or "+
				"fractions of the input, so a box in an unknown unit has no place on the picture. %s", seq, noteOr(t.Note))
	}
	// The objectness decision. A null here means the field count matched neither
	// 4+classes nor 5+classes, so reading it either way would shift every class
	// index by one: a `truck` reported as a `bench` is a box in the right place
	// with the wrong word on it, which is the one wrong answer nobody can catch by
	// looking.
	if t.HasObjectness == nil {
		return StateCannotTell, fmt.Sprintf(
			"frame %d: this export has %d fields per detection and the model carries %d class names, which "+
				"matches neither 4+classes nor 5+classes, so every class index would shift by one. %s",
			seq, t.Fields, t.Classes, noteOr(t.Note))
	}
	// Identical geometry: the signature of the collapsed decode, and the number
	// the plan's §4 step 5 watches fall from 8 to 0.
	if t.IdenticalGeometry > identicalGeometryLimit {
		return StateCannotTell, fmt.Sprintf(
			"frame %d: the sidecar returned %d boxes at exactly the same geometry, which is the signature of a "+
				"decode whose coordinates have collapsed rather than a scene with %d identical objects. The "+
				"answer is about the arithmetic and not about the road",
			seq, t.IdenticalGeometry, t.IdenticalGeometry)
	}
	// A median object one or two pixels tall is not an object.
	if t.MedianObjectPx > 0 && t.MedianObjectPx < medianObjectFloorPx {
		return StateCannotTell, fmt.Sprintf(
			"frame %d: the median detected object is %.1f pixels tall, which is compression noise rather than "+
				"an object; a line crossing computed from it would be a crossing that never happened",
			seq, t.MedianObjectPx)
	}
	if len(boxes) == 0 {
		return StateNoObjects, ""
	}
	return StateAnswered, ""
}

func noteOr(note string) string {
	if note == "" {
		return "The sidecar gave no further reason."
	}
	return note
}
