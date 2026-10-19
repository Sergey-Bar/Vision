// Package classify owns the family taxonomy: which COCO class belongs to which
// family, and what a family is called in words.
//
// The taxonomy lives here and NOT in the sidecar, and the direction is one-way:
//
//   - CLASS NAMES come from the model. The sidecar reads the `names` metadata the
//     export carries and hands the Go side a string per box. A model with a
//     different vocabulary says what it says.
//   - FAMILIES come from the product. "car" and "truck" are different classes and
//     the same family, and that judgement is the POC's, not Ultralytics'.
//
// The sidecar has its own class-name table because it has to name a class at the
// moment it decodes an index. It has NO family table: a family is a decision about
// what this product cares about, and a Python process that draws boxes has no
// business making it. TestOneWayOwnership asserts the separation so a future edit
// cannot quietly merge them.
//
// English everywhere (D-7). `AGENTS.md` states the operator surface is English and
// the Hebrew RTL surface was dropped, and the 7x13 face the overlay draws with is
// ASCII-only, so a family name that is not ASCII could not be rendered at all.
package classify

import (
	"fmt"
	"sort"
	"strings"
)

// Family is the product's own grouping of the model's classes. Four, and no more:
// every addition is a hue on the overlay and a column in the report, and the point
// of the grouping is that a viewer can read it at a glance.
type Family string

// The four families. These strings are contract vocabulary: they appear in
// `detections.family`, on the screen, in the report, and in the pushed event's
// `value`. CONTRACT.md §2 defines them.
const (
	FamilyVehicle Family = "vehicle"
	FamilyPerson  Family = "person"
	FamilyAnimal  Family = "animal"
	FamilyOther   Family = "other"
)

// Families is the closed set, in the order the report and the screen print them.
// The order is fixed so two runs' reports are comparable line by line.
var Families = []Family{FamilyVehicle, FamilyPerson, FamilyAnimal, FamilyOther}

// ValidFamily reports whether a string is one of the four. A family outside the
// set is refused rather than stored: `other` is a real answer, an invented
// category is not.
func ValidFamily(f Family) bool {
	for _, k := range Families {
		if k == f {
			return true
		}
	}
	return false
}

// families is the table: COCO-80 class -> family. It is a MAP and not a slice of
// pairs because lookups are the only thing this table is for, and a duplicate
// entry in a slice is a silent last-wins.
//
// Every one of the 80 COCO classes appears exactly once. TestEveryCOCOClassIsMapped
// and the family-count assertions in the test file pin that: a class added to the
// model and not to this table is not an error at load time, it is an object that
// arrives as `other` with no way to tell it apart from a chair.
var families = map[string]Family{
	// vehicle
	"bicycle":    FamilyVehicle,
	"car":        FamilyVehicle,
	"motorcycle": FamilyVehicle,
	"bus":        FamilyVehicle,
	"train":      FamilyVehicle,
	"truck":      FamilyVehicle,
	"boat":       FamilyVehicle,
	"airplane":   FamilyVehicle,
	"skateboard": FamilyVehicle,
	"surfboard":  FamilyVehicle,
	"skis":       FamilyVehicle,
	"snowboard":  FamilyVehicle,
	// person
	"person": FamilyPerson,
	// animal
	"bird":     FamilyAnimal,
	"cat":      FamilyAnimal,
	"dog":      FamilyAnimal,
	"horse":    FamilyAnimal,
	"sheep":    FamilyAnimal,
	"cow":      FamilyAnimal,
	"elephant": FamilyAnimal,
	"bear":     FamilyAnimal,
	"zebra":    FamilyAnimal,
	"giraffe":  FamilyAnimal,
	// other: everything that is neither a person, an animal, nor a vehicle.
	"traffic light":  FamilyOther,
	"fire hydrant":   FamilyOther,
	"stop sign":      FamilyOther,
	"parking meter":  FamilyOther,
	"bench":          FamilyOther,
	"backpack":       FamilyOther,
	"umbrella":       FamilyOther,
	"handbag":        FamilyOther,
	"tie":            FamilyOther,
	"suitcase":       FamilyOther,
	"frisbee":        FamilyOther,
	"sports ball":    FamilyOther,
	"kite":           FamilyOther,
	"baseball bat":   FamilyOther,
	"baseball glove": FamilyOther,
	"tennis racket":  FamilyOther,
	"bottle":         FamilyOther,
	"wine glass":     FamilyOther,
	"cup":            FamilyOther,
	"fork":           FamilyOther,
	"knife":          FamilyOther,
	"spoon":          FamilyOther,
	"bowl":           FamilyOther,
	"banana":         FamilyOther,
	"apple":          FamilyOther,
	"sandwich":       FamilyOther,
	"orange":         FamilyOther,
	"broccoli":       FamilyOther,
	"carrot":         FamilyOther,
	"hot dog":        FamilyOther,
	"pizza":          FamilyOther,
	"donut":          FamilyOther,
	"cake":           FamilyOther,
	"chair":          FamilyOther,
	"couch":          FamilyOther,
	"potted plant":   FamilyOther,
	"bed":            FamilyOther,
	"dining table":   FamilyOther,
	"toilet":         FamilyOther,
	"tv":             FamilyOther,
	"laptop":         FamilyOther,
	"mouse":          FamilyOther,
	"remote":         FamilyOther,
	"keyboard":       FamilyOther,
	"cell phone":     FamilyOther,
	"microwave":      FamilyOther,
	"oven":           FamilyOther,
	"toaster":        FamilyOther,
	"sink":           FamilyOther,
	"refrigerator":   FamilyOther,
	"book":           FamilyOther,
	"clock":          FamilyOther,
	"vase":           FamilyOther,
	"scissors":       FamilyOther,
	"teddy bear":     FamilyOther,
	"hair drier":     FamilyOther,
	"toothbrush":     FamilyOther,
}

// COCO is the 80-class vocabulary, in index order. It is here so this package can
// assert that every class is mapped WITHOUT the test importing a second copy, and
// so `pocctl model-probe` can print the same table the model-probe fixture was
// built from. The index matters: it is what an export that embeds no names falls
// back to.
var COCO = []string{
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

// FamilyOf maps a class name to its family.
//
// An unknown class is `other`, never a guess at a family and never an error: the
// model may be a different zoo entirely, and a class this build has never heard of
// is still a real object. The `other` answer is visible in the report's family
// histogram, so an unmapped vocabulary shows up as a wall of `other` rather than
// silently.
func FamilyOf(class string) Family {
	if f, ok := families[normalise(class)]; ok {
		return f
	}
	return FamilyOther
}

// IsFamily reports whether a name is one of the four families, so
// `SP_POC_CLASSES=vehicle` can be told from `SP_POC_CLASSES=car`.
func IsFamily(name string) bool { return ValidFamily(Family(normalise(name))) }

// IsClass reports whether a name is a class in this taxonomy. It is the other
// half of the SP_POC_CLASSES decision: an entry is either a family, a class, or
// neither, and "neither" is a refusal by name.
func IsClass(name string) bool {
	_, ok := families[normalise(name)]
	return ok
}

// Display is the family as the screen shows it: upper case, because it is the
// part read at a glance.
func Display(class string) string { return strings.ToUpper(string(FamilyOf(class))) }

// Value is the `value` field of the pushed event: `"<class> (<family>)"`. D-11.
//
// Both halves are present and the class comes first, because the class is what an
// Alarm Definition matches on and the family is what a human reads. A value that
// carried only the family would break every existing rule that names a class, and
// one that carried only the class would carry no information `type` does not
// already have — which is exactly the defect D11 exists to fix.
func Value(class string) string {
	c := strings.TrimSpace(class)
	if c == "" {
		// An empty class is not an object, and inventing a display string for it
		// would put a sentence in a field an Alarm Definition matches.
		return ""
	}
	return fmt.Sprintf("%s (%s)", c, FamilyOf(c))
}

// Label is the overlay's one-line label: `VEHICLE · car`. The family is upper case
// because it is the part read at a glance and the class is lower case because it
// is the part read deliberately.
func Label(class string) string {
	return Display(class) + " · " + strings.TrimSpace(class)
}

// Classes returns the class names of a family, sorted, so `SP_POC_CLASSES=vehicle`
// can be expanded for a report and so the taxonomy can be printed.
func Classes(f Family) []string {
	var out []string
	for c, fam := range families {
		if fam == f {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// Resolve turns one `SP_POC_CLASSES` entry into the classes it approves.
//
// It accepts a family name and a raw class name (D-9), so an existing run's
// `SP_POC_CLASSES=bird,car,person` keeps working unchanged and a new one can say
// `vehicle`. An entry that is neither is a REFUSAL by name, matching the pattern
// the rest of the configuration already uses: a typo that silently approved nothing
// would produce a run with zero events and no error, which is the most expensive
// kind of silence in this module.
func Resolve(entries []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if IsFamily(e) {
			for _, c := range Classes(Family(normalise(e))) {
				if !seen[c] {
					seen[c] = true
					out = append(out, c)
				}
			}
			continue
		}
		if IsClass(e) {
			key := normalise(e)
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
			continue
		}
		return nil, fmt.Errorf("SP_POC_CLASSES: %q is neither a family (%s) nor a class this taxonomy knows. "+
			"A class name that is misspelled here approves nothing, and a run that approves nothing reports zero events as if the model had found nothing. "+
			"The four families are vehicle, person, animal, other; every class is listed by 'pocctl model-probe' and pinned by internal/classify's tests",
			e, strings.Join(familyNames(), ", "))
	}
	sort.Strings(out)
	return out, nil
}

func familyNames() []string {
	out := make([]string, 0, len(Families))
	for _, f := range Families {
		out = append(out, string(f))
	}
	return out
}

// normalise lowercases and trims. Class names arrive from the model's own
// metadata and may be capitalised or padded; a taxonomy that is case-sensitive
// would put `Car` in `other` and draw it grey beside a red `car`.
func normalise(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
