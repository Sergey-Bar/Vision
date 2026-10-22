package shared_test

// The aliases are the SAME TYPES, not copies.
//
// # Why this file exists
//
// `shared/` is a public face over `internal/` packages, so the POC module can
// reach them across a Go module boundary. The seam only works if the two sides
// agree about what a type IS.
//
// A wrapper would be a second implementation of one thing, and the repository's
// own route audit warns against exactly that: two implementations of one thing,
// drifting apart silently. A copy of a decoder is the worst possible thing to
// let drift, because the drift shows up as "the product detected an object the
// POC did not" and nobody knows which one is wrong.
//
// A type ALIAS makes that impossible to get wrong, and this file is the proof.
//
// # The proof
//
// Each declaration below is a COMPILE-TIME assertion. A value of the internal
// type is ASSIGNED to a variable of the shared type, with no conversion: Go
// does not allow a value of one NAMED type to be assigned to a variable of
// another, so the assignment compiles only when the two names are the same
// type. If the shared package ever became a wrapper instead of an alias, this
// file would stop compiling, and a test file that does not compile is a
// failing test.
//
// That is the whole test: the property is a property of the type system and
// not of the program's behaviour, so there is nothing to run.

import (
	"testing"

	internalaistate "github.com/Sergey-Bar/vision/internal/aistate"
	sharedaistate "github.com/Sergey-Bar/vision/shared/aistate"

	internalclassify "github.com/Sergey-Bar/vision/internal/classify"
	sharedclassify "github.com/Sergey-Bar/vision/shared/classify"

	internalortdetect "github.com/Sergey-Bar/vision/internal/ortdetect"
	sharedortdetect "github.com/Sergey-Bar/vision/shared/ortdetect"

	internalproposal "github.com/Sergey-Bar/vision/internal/proposal"
	sharedproposal "github.com/Sergey-Bar/vision/shared/proposal"

	internalsidecar "github.com/Sergey-Bar/vision/internal/sidecar"
	sharedsidecar "github.com/Sergey-Bar/vision/shared/sidecar"
)

// The compile-time assertions.
//
// A value of the internal type IS a value of the shared type, with no
// conversion and no chance of the two sides disagreeing about what it means.
var (
	_ sharedclassify.Family = internalclassify.FamilyVehicle

	_ sharedortdetect.Box    = internalortdetect.Box{}
	_ sharedortdetect.Answer = internalortdetect.Answer{}

	_ sharedproposal.Box       = internalproposal.Box{}
	_ sharedproposal.Candidate = internalproposal.Candidate{}

	_ sharedsidecar.Box      = internalsidecar.Box{}
	_ sharedsidecar.Response = internalsidecar.Response{}
)

// takesBox takes a shared box.
//
// The parameter's type is written out, so a call with a value of the INTERNAL
// type compiles only because the two names are the same type.
func takesBox(sharedortdetect.Box) {}

// TestTheAliasesAreTheSameTypes is the runtime half.
//
// The compile-time assertions above are the real proof; this one exists so the
// file is a TEST and not a compile error, and so a reader sees what the file is
// about without reading the declarations.
//
// The CONSTANTS are the vocabulary the two modules share, and a vocabulary
// declared twice is a vocabulary that drifts -- which here is not a compile
// error but an invented detection rate.
func TestTheAliasesAreTheSameTypes(t *testing.T) {
	box := internalortdetect.Box{}
	// The call below is the assertion: the parameter's type is written out, so
	// the call compiles only because the two names are the same type. An
	// inferred type would compile whether or not the names matched.
	takesBox(box)
	if box != (sharedortdetect.Box{}) {
		t.Errorf("the alias is not the type it fronts: %+v", box)
	}

	if sharedaistate.Asked != internalaistate.Asked {
		t.Errorf("the shared answer state is %q and the internal one is %q. The two modules "+
			"must not disagree about what an answer is",
			sharedaistate.Asked, internalaistate.Asked)
	}
	if sharedclassify.FamilyVehicle != internalclassify.FamilyVehicle {
		t.Errorf("the shared family is %q and the internal one is %q",
			sharedclassify.FamilyVehicle, internalclassify.FamilyVehicle)
	}
}
