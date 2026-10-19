package ortdetect

// The COCO-80 class vocabulary, in the model's own index order.
//
// The direction of this table matters and is one-way: names come from the MODEL,
// families come from the PRODUCT. `internal/classify` owns the family grouping
// and a test asserts that this file never mentions a family. A detector that
// grouped classes itself would make the taxonomy a property of the model, and
// swapping models would silently change what the report counts.
//
// It lives here rather than in the model because the measured export carries no
// names of its own — its own metadata says nothing — and a class index with no
// name reaches the operator as the string "9". `Models()` and the session's own
// declared metadata are reconciled in probe.go, and the count that results is
// printed on the contract record: an export that DOES carry names is told apart
// from one that does not, because "we fell back to COCO-80" is a fact about the
// answer that a reader is entitled to know.
//
// The order is the canonical COCO order and it is load-bearing: index 2 is `car`
// and index 7 is `truck`, and swapping two entries would put a `bench` label on a
// bus while every box stayed exactly where it was. That is the shape of bug a
// reader cannot catch by looking at the picture.
var cocoClasses = [80]string{
	"person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat",
	"traffic light", "fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat",
	"dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe", "backpack",
	"umbrella", "handbag", "tie", "suitcase", "frisbee", "skis", "snowboard", "sports ball",
	"kite", "baseball bat", "baseball glove", "skateboard", "surfboard", "tennis racket",
	"bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple",
	"sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair",
	"couch", "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse",
	"remote", "keyboard", "cell phone", "microwave", "oven", "toaster", "sink",
	"refrigerator", "book", "clock", "vase", "scissors", "teddy bear", "hair drier",
	"toothbrush",
}

// ClassName resolves a class index to the model's own name for it.
//
// An index outside the vocabulary resolves to the empty string rather than to a
// made-up name or to the index itself. The empty string is what
// [proposal.Box.Valid] refuses, so an export with more classes than this
// vocabulary knows about produces boxes that are counted and dropped — loudly,
// with a name — instead of boxes wearing a plausible label that means nothing.
func ClassName(i int) string {
	if i < 0 || i >= len(cocoClasses) {
		return ""
	}
	return cocoClasses[i]
}

// NumClasses is how many names this vocabulary holds, and therefore the field
// count a detector's records must have: 4 box channels plus 80 classes is 84
// fields without an objectness channel, and 85 with one.
const NumClasses = len(cocoClasses)

// FieldsFor is the record width for a given class count and objectness decision.
//
// It is the arithmetic that decides whether column 4 is an objectness or the
// first class, and getting it wrong shifts every class by one — which produces a
// `truck` reported as a `bench`: a box in exactly the right place wearing the
// wrong word. That is the most expensive guess available in this file, so it is
// computed from the model's OWN declared names where it has them and refused
// where it does not.
func FieldsFor(classes int, hasObjectness bool) int {
	n := 4 + classes
	if hasObjectness {
		n++
	}
	return n
}
