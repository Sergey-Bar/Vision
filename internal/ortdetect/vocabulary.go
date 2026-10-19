package ortdetect

import "strings"

// Vocabulary is a class list.
//
// # Why it is a value and not a constant
//
// The class names used to be a package-level table of eighty names, which
// meant the class list was a property of the BUILD rather than of the session.
// That works for exactly one kind of model -- a fixed export with eighty
// classes -- and it is the wrong shape for the other kind, where the classes
// are named by a PROMPT and so differ from one session to the next.
//
// A vocabulary is therefore a value: the names, the prompt that produced them,
// where the names came from, and the family each name belongs to.
//
// # The one-way ownership
//
// The NAMES come from the model and the FAMILIES come from the product, and
// the two are not merged here. `internal/classify` owns the family judgement --
// "car" and "truck" are different classes and the same family, and that is the
// product's judgement -- and this package carries whatever map the product
// supplies without knowing what a family means.
//
// A detector that grouped classes itself would make the taxonomy a property of
// the model, and swapping models would silently change what the report counts.
type Vocabulary struct {
	// Names are the class names, in the model's own index order.
	Names []string
	// Prompt is the text that produced these names, for a model whose classes
	// are named by a prompt. Empty means the classes are fixed: a closed
	// export's names are not a prompt's answer, they are the export's.
	Prompt string
	// Source says where the names came from: "coco-80" for the built-in list,
	// or the name of the model that declared them.
	Source string
	// Families maps a class name to the product's family for it. It may be
	// empty, and then no class has a family -- which is a fact about the
	// vocabulary and not an error.
	Families map[string]string
}

// Name resolves a class index to the model's own name for it.
//
// An index outside the vocabulary resolves to the empty string rather than to
// a made-up name or to the index itself. The empty string is what
// [proposal.Box.Valid] refuses, so an export with more classes than this
// vocabulary knows about produces boxes that are counted and dropped -- loudly,
// with a name -- instead of boxes wearing a plausible label that means nothing.
func (v *Vocabulary) Name(i int) string {
	if v == nil || i < 0 || i >= len(v.Names) {
		return ""
	}
	return v.Names[i]
}

// Size is how many names the vocabulary holds.
func (v *Vocabulary) Size() int {
	if v == nil {
		return 0
	}
	return len(v.Names)
}

// FamilyOf is the product's family for a class name.
//
// It is empty for a name the product has no family for, and that is the
// honest answer: a class nobody has grouped has not been grouped.
func (v *Vocabulary) FamilyOf(name string) string {
	if v == nil {
		return ""
	}
	return v.Families[strings.ToLower(strings.TrimSpace(name))]
}

// COCOVocabulary is the built-in eighty-name vocabulary.
//
// The prompt is empty because these names are not a prompt's answer: they are
// the export's, and a vocabulary that claimed a prompt for them would be
// claiming a model that does not exist.
func COCOVocabulary() *Vocabulary {
	names := make([]string, len(cocoClasses))
	copy(names, cocoClasses[:])
	return &Vocabulary{
		Names:  names,
		Source: "coco-80",
	}
}

// OpenVocabulary is a vocabulary whose classes were named by a prompt.
//
// It is the shape an open-vocabulary export takes: the names are the ones the
// prompt asked for, in the model's own order, and the prompt is recorded so
// that a reader of a measurement can see WHAT was asked for. A measurement
// whose class list is not reproducible is not a measurement.
//
// The families are supplied by the product, because the family judgement is
// the product's.
func OpenVocabulary(prompt string, names []string, families map[string]string) *Vocabulary {
	return &Vocabulary{
		Names:    names,
		Prompt:   prompt,
		Source:   "open-vocabulary",
		Families: families,
	}
}
