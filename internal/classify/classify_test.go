package classify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryCOCOClassIsMapped pins the table against the vocabulary. A class that
// is in COCO and not in the family table would arrive as `other` with nothing to
// distinguish it from a chair, and the family histogram would quietly under-count
// vehicles for as long as nobody noticed.
//
// The table holds the eighty COCO classes AND the open classes -- the ones a
// model names by a PROMPT rather than carrying in its export. The two lists are
// checked separately, because "a class the model can emit and the table does not
// group" and "a table entry for a class nothing can emit" are different defects
// and a single count would hide both.
func TestEveryCOCOClassIsMapped(t *testing.T) {
	if len(COCO) != 80 {
		t.Fatalf("COCO has %d classes; the COCO-80 vocabulary is 80 and a fixture built against a different length is a fixture built against an assumption", len(COCO))
	}
	for _, c := range COCO {
		if !IsClass(c) {
			t.Errorf("%q is in the COCO vocabulary and is not in the family table", c)
		}
	}
	// And nothing beyond the two lists: a table entry for a class the model
	// cannot emit is a class nobody will ever see counted in a taxonomy total.
	for c := range families {
		found := false
		for _, k := range COCO {
			if k == c {
				found = true
				break
			}
		}
		if !found {
			for _, k := range OpenClasses {
				if k == c {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("the family table has %q, which is neither a COCO class nor an open class", c)
		}
	}
	if len(families) != len(COCO)+len(OpenClasses) {
		t.Fatalf("the family table has %d entries and the vocabulary has %d plus %d open classes",
			len(families), len(COCO), len(OpenClasses))
	}
}

// TestTheOpenClassesAreGrouped pins the open classes' families.
//
// A drone is a vehicle because it is a machine that moves under its own power,
// and the aeroplane in the table above is one too. A balloon is `other`,
// because it carries nothing and it is not alive.
func TestTheOpenClassesAreGrouped(t *testing.T) {
	want := map[string]Family{
		"drone":   FamilyVehicle,
		"balloon": FamilyOther,
	}
	for class, family := range want {
		if got := FamilyOf(class); got != family {
			t.Errorf("%q is in the %s family, want %s", class, got, family)
		}
	}
}

// TestFamiliesForBuildsTheMapForAVocabulary is the product's half of the
// one-way ownership: the DETECTOR's vocabulary holds the names the model
// declared, and the PRODUCT supplies the families, because the family
// judgement is the product's and not the model's.
func TestFamiliesForBuildsTheMapForAVocabulary(t *testing.T) {
	families := FamiliesFor([]string{"person", "truck", "drone", "banana", ""})
	if len(families) != 4 {
		t.Fatalf("the map has %d entries, want four: %v", len(families), families)
	}
	if families["person"] != string(FamilyPerson) {
		t.Errorf("person is in the %s family, want %s", families["person"], FamilyPerson)
	}
	if families["drone"] != string(FamilyVehicle) {
		t.Errorf("drone is in the %s family, want %s", families["drone"], FamilyVehicle)
	}
	if _, empty := families[""]; empty {
		t.Error("the map has an entry for the empty class name")
	}
}

// TestTheFourFamiliesHoldWhatTheyClaim: the counts are pinned because a family that
// grows without a decision about what it means is how a taxonomy becomes a
// synonym list.
//
// The counts include the open classes, because a class the model can emit is a
// class the histogram counts.
func TestTheFourFamiliesHoldWhatTheyClaim(t *testing.T) {
	want := map[Family]int{
		FamilyVehicle: 13,
		FamilyPerson:  1,
		FamilyAnimal:  10,
		FamilyOther:   58,
	}
	for f, n := range want {
		if got := len(Classes(f)); got != n {
			t.Errorf("family %s has %d classes, want %d: %v", f, got, n, Classes(f))
		}
	}
}

// TestFamilyOfIsCaseAndSpaceInsensitive: class names come from the model's own
// metadata, and a taxonomy that is case-sensitive would draw `Car` grey beside a
// red `car` and count it in a different family in the report.
func TestFamilyOfIsCaseAndSpaceInsensitive(t *testing.T) {
	for _, c := range []string{"car", "Car", "CAR", " car ", "\tcar\n"} {
		if got := FamilyOf(c); got != FamilyVehicle {
			t.Errorf("FamilyOf(%q) = %s, want vehicle", c, got)
		}
	}
}

// TestAnUnknownClassIsOtherNotAnError: the model may be a different zoo, and a
// class this build has never heard of is still a real object. Guessing a family
// would be worse than saying `other`, and refusing would stop the run.
func TestAnUnknownClassIsOtherNotAnError(t *testing.T) {
	for _, c := range []string{"", "  ", "a really new class", "42", "traffic-light"} {
		if got := FamilyOf(c); got != FamilyOther {
			t.Errorf("FamilyOf(%q) = %s, want other", c, got)
		}
	}
}

// TestValueCarriesTheClassAndTheFamily is D-11: the class first, so every existing
// Alarm Definition that matches on the words still matches, and the family after
// it, so the value carries information `type` does not.
func TestValueCarriesTheClassAndTheFamily(t *testing.T) {
	cases := map[string]string{
		"car":     "car (vehicle)",
		"person":  "person (person)",
		"bird":    "bird (animal)",
		"chair":   "chair (other)",
		"unknown": "unknown (other)",
	}
	for class, want := range cases {
		if got := Value(class); got != want {
			t.Errorf("Value(%q) = %q, want %q", class, got, want)
		}
	}
	// D-11's other half: the value must NOT be byte-identical to the class, which
	// is what the wire carried before and what made it carry no information.
	if Value("car") == "car" {
		t.Fatal("Value is byte-identical to the class name: it carries no information the type field does not already have")
	}
	if Value("") != "" {
		t.Fatalf("Value(\"\") = %q; an empty class is not an object and must not get a display string", Value(""))
	}
}

// TestResolveAcceptsFamiliesAndClasses is D-9. An existing run's
// SP_POC_CLASSES=bird,car,person must keep working with no change to the
// environment, and a new run must be able to say `vehicle`.
func TestResolveAcceptsFamiliesAndClasses(t *testing.T) {
	got, err := Resolve([]string{"bird", "car", "person"})
	if err != nil {
		t.Fatalf("the pre-existing spelling was refused: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("three classes resolved to %v", got)
	}
	for _, want := range []string{"bird", "car", "person"} {
		if !contains(got, want) {
			t.Errorf("%q is missing from %v", want, got)
		}
	}
}

// TestResolveExpandsAFamilyToItsClasses.
func TestResolveExpandsAFamilyToItsClasses(t *testing.T) {
	got, err := Resolve([]string{"animal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(Classes(FamilyAnimal)) {
		t.Fatalf("animal resolved to %d classes, want %d", len(got), len(Classes(FamilyAnimal)))
	}
}

// TestResolveIsIdempotentAcrossAFamilyAndAMemberOfIt: `vehicle,car` is a plausible
// thing for a person to type, and a duplicate would inflate the approved list.
func TestResolveIsIdempotentAcrossAFamilyAndAMemberOfIt(t *testing.T) {
	got, err := Resolve([]string{"vehicle", "car", "CAR", " truck "})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Fatalf("%q appears twice in %v", c, got)
		}
		seen[c] = true
	}
	if !contains(got, "car") || !contains(got, "truck") {
		t.Fatalf("%v is missing a named class", got)
	}
}

// TestResolveRefusesAnUnknownEntryByName is the whole point: a typo in the class
// list approves nothing, and a run that approves nothing reports zero events as if
// the model had found nothing. The refusal names the variable and the entry.
func TestResolveRefusesAnUnknownEntryByName(t *testing.T) {
	_, err := Resolve([]string{"car", "cars"})
	if err == nil {
		t.Fatal("a misspelled class was accepted; the run would approve nothing and report zero events as a measurement")
	}
	msg := err.Error()
	for _, want := range []string{"SP_POC_CLASSES", "cars", "vehicle", "person", "animal", "other"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
}

// TestFamiliesAreTheFourAndOnlyFour.
func TestFamiliesAreTheFourAndOnlyFour(t *testing.T) {
	if len(Families) != 4 {
		t.Fatalf("there are %d families, want 4", len(Families))
	}
	for _, f := range Families {
		if !ValidFamily(f) {
			t.Errorf("%s is in the list and is not a valid family", f)
		}
		if !IsFamily(string(f)) {
			t.Errorf("IsFamily(%q) is false for a family in the list", f)
		}
	}
	if ValidFamily("vehicle-ish") {
		t.Error("an invented family was accepted; `other` is the catch-all and there is no fifth category")
	}
}

// TestOneWayOwnership is D-8 asserted in code rather than in a review comment:
// the taxonomy lives HERE and the sidecar has no copy of it.
//
// The check is deliberately NOT "the sidecar must not mention families". The
// sidecar's comments discuss the taxonomy in order to explain that it does not
// have one, and it has a `decoder_family` — a different word with a different
// meaning entirely, meaning whether an export is fused or anchored. A check that
// matched the bare word would fail on the very prose that documents the rule, and
// a check that people learn to work around is not a check.
//
// What it bans instead is a TAXONOMY LOOKUP: any function that maps a class to a
// family, and the two family names that are not also COCO class names. `person`
// and `other` are far too common in English to test for textually; `vehicle` and
// `animal` are not, and a table mapping a class to `vehicle` could not exist
// without one of them. The test is crude by design: a table copied into the
// Python would be exactly the drift this watches for, and a crude check that
// fails the moment the copy appears is worth more than a clever one that
// explains itself around it.
func TestOneWayOwnership(t *testing.T) {
	root := moduleRoot(t)
	// The Python sidecar lives in the POC module, which is a separate Go module
	// in this repository: the Go half of the wire moved into the product, and
	// the Python half stayed where it was. The path says so rather than
	// guessing, because a path that silently stopped resolving would turn this
	// test into a no-op that reports green.
	py := filepath.Join(root, "poc", "active-monitoring", "pyworker", "server.py")
	raw, err := os.ReadFile(py)
	if err != nil {
		t.Fatalf("cannot read %s: %v", py, err)
	}
	src := string(raw)
	// The sidecar MUST have the class-name vocabulary: it decodes an index and
	// has to name it at that moment.
	if !strings.Contains(src, "COCO_CLASSES") {
		t.Error("the sidecar no longer carries the COCO class-name table; an export that embeds no names would answer with integers")
	}
	for _, banned := range []string{"family_of", "FamilyOf", "classify.FamilyOf", "animal"} {
		if strings.Contains(src, banned) {
			t.Errorf("the sidecar contains %q; the family taxonomy belongs to internal/classify and the direction is one way (D-8)", banned)
		}
	}
	// `vehicle` is allowed in a comment and nowhere else, so it is checked with a
	// quoted-literal form: a taxonomy entry writes the family as a string value.
	for _, banned := range []string{`"vehicle"`, `'vehicle'`, `"animal"`, `'animal'`} {
		if strings.Contains(src, banned) {
			t.Errorf("the sidecar contains the family value %s; the four families and their members live in internal/classify and the direction is one way (D-8)", banned)
		}
	}
}

// TestTheModuleHasNoInternalDependencies keeps classify a leaf: it may not import
// anything from this module, so the taxonomy cannot start depending on the store or
// the wire and pull a cycle behind it.
//
// It reads the SOURCE rather than asking the go tool, because the go tool is not on
// PATH in a default Windows shell and a check that needs a tool is a check that is
// silently skipped. It reads the non-test files only: this file names the packages
// it is looking for, in the very string it searches for, and a scan that matched
// itself would fail for that reason alone.
func TestTheModuleHasNoInternalDependencies(t *testing.T) {
	root := moduleRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "internal", "classify"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, "internal", "classify", name))
		if err != nil {
			t.Fatal(err)
		}
		checked++
		if strings.Contains(string(raw), "ai-captain-poc/internal/") {
			t.Errorf("%s imports another package of this module; the taxonomy is a leaf and must stay one", name)
		}
	}
	if checked == 0 {
		t.Fatal("no production file was checked; the scan found nothing to read and that is not a pass")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("cannot find the module root")
	return ""
}
