package emulator

import (
	"fmt"
	"strings"
	"time"
)

// Detection is one object the detector found in one frame.
//
// The coordinates are normalised to the frame: 0 is the left edge, 1 is the
// right. The platform stores what the VMS asserted, and a vendor that reports
// pixels has to be trusted about the frame's size; normalised coordinates do
// not carry that assumption.
type Detection struct {
	// Class is the model's own class name.
	Class string
	// Confidence is the model's score for this object.
	Confidence float64
	// X1, Y1, X2, Y2 are the box, normalised to the frame.
	X1, Y1, X2, Y2 float64
}

// Width is the box's width in frame fractions.
func (d Detection) Width() float64 { return d.X2 - d.X1 }

// Height is the box's height in frame fractions.
func (d Detection) Height() float64 { return d.Y2 - d.Y1 }

// CentreX is the box's horizontal centre.
func (d Detection) CentreX() float64 { return (d.X1 + d.X2) / 2 }

// CentreY is the box's vertical centre.
func (d Detection) CentreY() float64 { return (d.Y1 + d.Y2) / 2 }

// bagClasses are the classes that travel with a person.
//
// A bag is not a person and a person is not a bag. What the operator asks is
// "who was carrying something", and the answer is an association between two
// boxes in one frame, computed by the VMS's own analytics engine — which is
// where it belongs, because the association is a claim about the picture and
// the picture is the VMS's.
var bagClasses = map[string]bool{
	"backpack":     true,
	"handbag":      true,
	"suitcase":     true,
	"briefcase":    true,
	"umbrella":     true,
	"shopping bag": true,
}

// classPersonWithBag is the class a person becomes when the frame also holds a
// bag that belongs to them.
//
// It is a class the platform refines into a typed event, so it is a decision
// with a rule behind it and not a default — the same rule that governs
// `restricted-person` in the adapter's own class table.
const classPersonWithBag = "person-with-bag"

// classPersonCount is the class of the event that carries how many people were
// in the frame.
//
// It is its own class rather than a field on another event because the count is
// a property of the FRAME, not of any one object: two people in a frame are
// two events, and the number two belongs to neither of them. The count travels
// in the event's class id, because the adapter's redaction allow-list permits
// exactly that field and nothing else — a count smuggled into a field the
// platform does not allow-list would be redacted on ingest and the platform
// would silently lose it.
const classPersonCount = "person-count"

// classVehicle is the class of a road vehicle.
const classVehicle = "vehicle"

// classAnimal is the class of an animal.
const classAnimal = "animal"

// classBird is the class of a bird.
//
// It is its own class rather than an animal because a bird in the sky and a
// dog on the ground are different facts with different consequences: a bird is
// what a fence sensor reports, a dog is what a neighbour complains about.
const classBird = "bird"

// classDrone is the class of an unmanned aircraft.
const classDrone = "drone"

// classBalloon is the class of a balloon.
const classBalloon = "balloon"

// classOther is the class of anything the taxonomy does not name.
const classOther = "other"

// emulatorRuleNames are the classes this VMS asserts, and so the rules its own
// analytics engine runs.
//
// They are listed here rather than derived from the taxonomy because the list
// is a FACT about this VMS: it asserts these classes and no others, and the
// vendor's own rules route says so.
var emulatorRuleNames = []string{
	classPerson,
	classPersonWithBag,
	classVehicle,
	classAnimal,
	classBird,
	classDrone,
	classBalloon,
	classPersonCount,
}

// alarmClasses are the classes the VMS raises an alarm for.
//
// A person is not an alarm. A person with a bag is something an operator
// looks at, and a vehicle somewhere it should not be is something an operator
// acts on.
var alarmClasses = map[string]bool{
	classPersonWithBag: true,
	classVehicle:       true,
	classDrone:         true,
}

// alarmClassNames are the alarm types the VMS can raise, in the order they are
// published.
var alarmClassNames = []string{
	classPersonWithBag,
	classVehicle,
	classDrone,
}

// vehicleClasses are the classes that are vehicles.
//
// COCO-80 names seven kinds of vehicle and the taxonomy does not care which:
// the platform's rule is about a vehicle being somewhere it should not be, and
// "a car" and "a bus" are the same fact for that purpose.
var vehicleClasses = map[string]bool{
	"bicycle":    true,
	"car":        true,
	"motorcycle": true,
	"bus":        true,
	"train":      true,
	"truck":      true,
	"boat":       true,
	"airplane":   true,
}

// animalClasses are the classes that are animals.
var animalClasses = map[string]bool{
	"bird":     true,
	"cat":      true,
	"dog":      true,
	"horse":    true,
	"sheep":    true,
	"cow":      true,
	"elephant": true,
	"bear":     true,
	"zebra":    true,
	"giraffe":  true,
}

// taxonomy maps the detector's class names onto the platform's canonical
// classes.
//
// It is a table and not a rule because the platform's class list is CLOSED —
// eighteen names, fixed by the adapter — and a detector class that is not in
// the table is not refined. The event is not lost: it keeps the generic
// analytics type it always had, and the class name the VMS asserted is carried
// as its own field.
var taxonomy = map[string]string{
	"person":  classPerson,
	"vehicle": classVehicle,
	"animal":  classAnimal,
	"bird":    classBird,
	"drone":   classDrone,
	"balloon": classBalloon,
}

// classPerson is the canonical class of a person.
const classPerson = "person"

// canonicalClass maps a detector class onto a canonical one.
func canonicalClass(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := taxonomy[key]; ok {
		return canonical
	}
	switch {
	case vehicleClasses[key]:
		return classVehicle
	case animalClasses[key]:
		return classAnimal
	}
	return classOther
}

// associate turns raw detections into the classes the VMS asserts.
//
// Two things happen here and both are the VMS's own analytics rather than the
// platform's:
//
//   - A person with a bag inside their box becomes `person-with-bag`.
//   - The classes are renamed to the platform's canonical vocabulary.
//
// The association is a spatial claim about one frame: the bag's box lies
// inside the person's box. It is deliberately not a tracking claim and not an
// identity claim: the platform finds moments, not people, and a bag that
// happens to be near a person is an observation about one frame.
func associate(dets []Detection) []Detection {
	out := make([]Detection, 0, len(dets))
	for _, d := range dets {
		out = append(out, Detection{
			Class:      canonicalClass(d.Class),
			Confidence: d.Confidence,
			X1:         d.X1, Y1: d.Y1, X2: d.X2, Y2: d.Y2,
		})
	}
	// The bag that belongs to a person is found by containment, and the person's
	// class is only raised when the bag is genuinely inside them rather than
	// merely beside them.
	for i := range out {
		if out[i].Class != classPerson {
			continue
		}
		for _, d := range dets {
			if !bagClasses[strings.ToLower(strings.TrimSpace(d.Class))] {
				continue
			}
			if contains(out[i], d) {
				out[i].Class = classPersonWithBag
				break
			}
		}
	}
	return out
}

// contains reports whether a box lies inside another.
//
// It is containment by CENTRE, with a tolerance, and not by overlap: a bag
// whose centre is at the edge of a person's box is beside them, not carried by
// them, and a person standing next to a suitcase is not carrying it.
func contains(outer, inner Detection) bool {
	const tolerance = 0.02
	cx, cy := inner.CentreX(), inner.CentreY()
	return cx >= outer.X1-tolerance && cx <= outer.X2+tolerance &&
		cy >= outer.Y1-tolerance && cy <= outer.Y2+tolerance
}

// countPeople counts the people in a frame.
//
// A person with a bag is still a person, so the count does not double it.
func countPeople(dets []Detection) int {
	n := 0
	for _, d := range dets {
		if d.Class == classPerson || d.Class == classPersonWithBag {
			n++
		}
	}
	return n
}

// event is one analytics event in the vendor's envelope.
//
// The field names are the vendor's, because this is the wire: the adapter reads
// `AnalyticData.Object.Value.ClassName` and the platform's redaction allow-list
// names `object.value.classname`. Spelling them differently here would be a
// disagreement the adapter would resolve by degrading, and a degraded event is
// an event the operator never sees.
type event struct {
	ID       string
	Type     string
	DeviceID string
	At       time.Time
	// ClassName is the class the VMS asserted.
	ClassName string
	// ClassID is the stable identifier for it. For a count it carries the count.
	ClassID string
	// Confidence is the engine's own score.
	Confidence float64
	// Vendor is which engine asserted this.
	Vendor string
	// RuleName is the VMS-side rule that fired.
	RuleName string
}

// envelope renders the event in the vendor's wire shape.
func (e event) envelope() map[string]any {
	return map[string]any{
		"Id":        e.ID,
		"Type":      e.Type,
		"Timestamp": e.At.UTC().Format("2006-01-02T15:04:05.000Z"),
		"AnalyticData": map[string]any{
			"Vendor":   e.Vendor,
			"RuleName": e.RuleName,
			"Object": map[string]any{
				"Type": "ObjectDetected",
				"Value": map[string]any{
					"DeviceObject": map[string]any{"Id": e.DeviceID},
					"Confidence":   e.Confidence,
					"ClassId":      e.ClassID,
					"ClassName":    e.ClassName,
				},
			},
		},
	}
}

// describeDetection renders one detection for the control surface.
func describeDetection(d Detection) map[string]any {
	return map[string]any{
		"class":      d.Class,
		"confidence": d.Confidence,
		"x1":         d.X1,
		"y1":         d.Y1,
		"x2":         d.X2,
		"y2":         d.Y2,
	}
}

// classIDFor renders the stable identifier for a class.
//
// For a count it is the count itself, and the reason is the redaction
// allow-list: `object.value.classid` survives ingest and nothing else in that
// subtree does, so a number that has to survive has to travel in it.
func classIDFor(class string, count int) string {
	if class == classPersonCount {
		return fmt.Sprintf("%d", count)
	}
	return strings.ToLower(strings.ReplaceAll(class, " ", "_"))
}
