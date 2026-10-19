// Package classify is the public face of the class taxonomy.
//
// The implementation is `github.com/Sergey-Bar/vision/internal/classify`. It
// is internal because the taxonomy is the POC's own vocabulary -- which COCO
// class belongs to which family -- and the product's canonical classes are a
// different, closed list.
//
// The POC module resolves the same taxonomy, so it comes here, over the module
// boundary, as aliases of the same values.
package classify

import "github.com/Sergey-Bar/vision/internal/classify"

// The taxonomy.
type (
	// Family is one of the four families a class belongs to.
	Family = classify.Family
)

// The families.
const (
	FamilyVehicle = classify.FamilyVehicle
	FamilyPerson  = classify.FamilyPerson
	FamilyAnimal  = classify.FamilyAnimal
	FamilyOther   = classify.FamilyOther
)

// The families.
var (
	// COCO is the built-in class list.
	COCO = classify.COCO
	// Families is the four families.
	Families = classify.Families
)

// The functions.
var (
	// Classes expands a family into its classes.
	Classes = classify.Classes
	// Display renders a family for a human.
	Display = classify.Display
	// FamilyOf is the family a class belongs to.
	FamilyOf = classify.FamilyOf
	// IsClass reports whether a name is a known class.
	IsClass = classify.IsClass
	// IsFamily reports whether a name is a known family.
	IsFamily = classify.IsFamily
	// Label renders a class for a human.
	Label = classify.Label
	// Resolve expands a list of families and classes into a class list.
	Resolve = classify.Resolve
	// ValidFamily reports whether a family is one of the four.
	ValidFamily = classify.ValidFamily
	// Value is the canonical value of a class.
	Value = classify.Value
)
