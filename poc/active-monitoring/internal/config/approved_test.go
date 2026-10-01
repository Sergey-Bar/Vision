package config

import (
	"testing"
)

// TestTheShippedTriggerClassCanBeApproved is the test for the defect that made the
// shipped path unrunnable.
//
// `SP_POC_TRIGGER_MODE=novelty` measures a region with pure-Go arithmetic and emits
// the class `motion`. `motion` is not a COCO class, so `classify.Resolve` refuses it
// by name — correctly, because a misspelled class in an approved list is exactly the
// thing that approves nothing while looking configured.
//
// The only remaining spelling is the FAMILY, and that did not work either: naming
// `other` expanded to the COCO classes whose family is `other`, and `IsApproved` did
// an exact lookup against that set. `motion` is not one of them, because `other` IS
// the family rather than a member of one.
//
// The result was that every candidate the shipped trigger produced was dropped as
// `class not on the approved list`. Zero detections, zero errors, exit status 0 —
// and a `pocctl demo` that printed "here is what landed" over a pipeline that could
// not land anything.
func TestTheShippedTriggerClassCanBeApproved(t *testing.T) {
	world := complete(t)
	world["SP_POC_CLASSES"] = "other,car,bird,person,truck,bus"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatalf("the list the demo ships with was refused: %v", err)
	}
	if !cfg.IsApproved("motion") {
		t.Fatalf("%q was refused. The novelty engine emits it, and no spelling of SP_POC_CLASSES can approve it, so "+
			"the shipped trigger cannot produce one crossing event in a run configured by its own demo", "motion")
	}
	// And the family itself, for a caller that has only the family.
	if !cfg.IsApprovedFamily("motion") {
		t.Fatal("the family of the shipped trigger's class is not in the approved set either")
	}
}

// TestAFamilyStillDoesNotApproveEverything: the fix must not become "any class is
// approved once any family is named", which is the other way to produce a busy road.
func TestAFamilyStillDoesNotApproveEverything(t *testing.T) {
	world := complete(t)
	world["SP_POC_CLASSES"] = "vehicle"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsApproved("car") {
		t.Fatal("car is in the vehicle family and was refused")
	}
	// `person` is named nowhere and belongs to no named family.
	if cfg.IsApproved("person") {
		t.Fatal("person was approved by a list naming only the vehicle family")
	}
	// And the novelty class, whose family is `other`, is not approved by `vehicle`.
	if cfg.IsApproved("motion") {
		t.Fatal("motion was approved by a list naming only the vehicle family; its family is other")
	}
}

// TestAnExactClassNameStillWins: naming a class must keep working, because
// `SP_POC_CLASSES=bird,car,person` is the pre-existing spelling and an operator
// using it means those three and nothing else.
func TestAnExactClassNameStillWins(t *testing.T) {
	world := complete(t)
	world["SP_POC_CLASSES"] = "bird,car,person"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bird", "car", "person"} {
		if !cfg.IsApproved(want) {
			t.Errorf("%q is named and was refused", want)
		}
	}
	for _, notWant := range []string{"truck", "bus", "chair"} {
		if cfg.IsApproved(notWant) {
			t.Errorf("%q was approved by a list naming three classes", notWant)
		}
	}
	if cfg.IsApproved("motion") {
		t.Error("motion was approved by a list naming bird, car and person")
	}
}

// TestTheResolutionIsStableAcrossCopies is the cache, from the angle that broke it.
// A Config is passed BY VALUE to every reader, so a cache that fills itself on one
// copy is empty on the next one, and a class filter that approves nothing depending
// on which copy reached it is the worst kind.
func TestTheResolutionIsStableAcrossCopies(t *testing.T) {
	world := complete(t)
	// A family is NAMED here on purpose. With no family in the list the approved
	// family set is legitimately empty, and asserting otherwise would be asserting
	// that `car` implies `vehicle` — which is not what naming a class means.
	world["SP_POC_CLASSES"] = "vehicle,person"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatal(err)
	}
	first := cfg.IsApproved("car")
	if !first {
		t.Fatal("car is in the vehicle family and was refused by the original")
	}
	for i := 0; i < 5; i++ {
		c := cfg // the copy every reader in this module is handed
		if got := c.IsApproved("car"); got != first {
			t.Fatalf("copy %d disagrees with the original: %v vs %v", i, got, first)
		}
		if !c.IsApprovedFamily("car") {
			t.Fatalf("copy %d lost the approved families; the family was named in the list", i)
		}
	}
	// And a list with no family named still approves exactly the classes named.
	cfg2, err := Load(lookupFrom(complete(t)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.IsApprovedFamily("car") {
		t.Fatal("a list naming car but not vehicle approved the vehicle family")
	}
	if !cfg2.IsApproved("car") {
		t.Fatal("a list naming car refused car")
	}
}

// TestTheFamilySetIsNotTheClassSet: they are different sets and a reader must not be
// able to confuse them. `other` is a family and is in no class list.
func TestTheFamilySetIsNotTheClassSet(t *testing.T) {
	world := complete(t)
	world["SP_POC_CLASSES"] = "other"
	cfg, err := Load(lookupFrom(world))
	if err != nil {
		t.Fatal(err)
	}
	classes, err := cfg.ApprovedClasses()
	if err != nil {
		t.Fatal(err)
	}
	if classes["other"] {
		t.Error("`other` is in the class set; it is a family, and a class set containing it would approve a class named other")
	}
	if !cfg.IsApprovedFamily("kite") {
		t.Error("kite maps to the other family and is not approved by a list naming other")
	}
	if len(cfg.Classes) != 1 || cfg.Classes[0] != "other" {
		t.Errorf("the raw list no longer contains the family it was given: %v", cfg.Classes)
	}
}
