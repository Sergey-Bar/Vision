package fixture

import "sort"

// The required behaviours, and the corpus's coverage of them.
//
// # Why the vocabulary lives here and not in a test
//
// The list of behaviours a corpus has to cover was a list inside a TEST, which
// made it a fact about one test rather than a fact about the corpus. The
// replay audit needs the same list -- "a corpus that does not cover the
// required behaviours reports its gaps rather than a rate" -- and a package
// cannot read a test's local variable.
//
// So the vocabulary moved here, where the scenarios it describes live, and the
// test now asks the package.
//
// # What a "required behaviour" is
//
// A behaviour the platform has to get right, and therefore a scenario that
// has to exist. They are named in the scenarios' `covers` lists, and the
// corpus is complete when every name here appears in at least one of them.
//
// The list is a closed set, and it is closed on purpose: a behaviour nobody
// thought of is a behaviour nobody tested, and the only way to notice that is
// to have a list that says what the set is.

// requiredCases is every behaviour the committed corpus has to cover.
//
// The names are the ones the scenarios' `covers` lists use, and a scenario
// whose purpose is unstated is a scenario nobody will notice has stopped
// testing anything.
var requiredCases = []string{
	// The shapes of source traffic.
	"motion",
	"analytics",
	"alarm.created",
	"alarm.acknowledged",
	"device.camera_offline",
	"device.camera_online",
	"bookmark",
	"recording.started",
	"metadata",
	// The shapes of BAD input, which are the ones a product gets wrong.
	"malformed",
	"duplicate",
	"delayed",
	"out-of-order",
	"missing-camera",
	"reconnect",
}

// Coverage is what the corpus covers and what it is missing.
type Coverage struct {
	// Covered are the required behaviours at least one scenario covers.
	Covered []string `json:"covered"`
	// Missing are the required behaviours no scenario covers.
	//
	// A non-empty Missing is the whole point: a corpus with a gap reports the
	// gap and NOT a rate, because a rate computed over a corpus that does not
	// exercise a behaviour is a number about the corpus rather than about the
	// product.
	Missing []string `json:"missing"`
	// Complete says whether the corpus covers every required behaviour.
	Complete bool `json:"complete"`
}

// CoverageOf is the corpus's coverage of the required behaviours.
//
// The counts are of the REQUIRED behaviours, not of the scenarios: a corpus with
// forty scenarios and one missing behaviour is still incomplete, and the
// number that says so is the missing one.
func CoverageOf(manifest Manifest) Coverage {
	covered := map[string]bool{}
	for _, entry := range manifest.Scenarios {
		for _, c := range entry.Covers {
			covered[c] = true
		}
	}
	out := Coverage{Covered: []string{}, Missing: []string{}}
	for _, required := range requiredCases {
		if covered[required] {
			out.Covered = append(out.Covered, required)
		} else {
			out.Missing = append(out.Missing, required)
		}
	}
	sort.Strings(out.Covered)
	sort.Strings(out.Missing)
	out.Complete = len(out.Missing) == 0
	return out
}
