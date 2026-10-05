package golden

// Q4: the POC and the product must reach the SAME decision from ONE golden fixture.
//
// # Why this gate exists at all
//
// Two implementations of "what an event is" is the failure mode nobody plans for.
// The POC parses a vendor's payload and decides what to push; the product
// normalises what arrives and decides what to raise. Nothing in either module
// requires the two to agree, and nothing in either module would notice when they
// stopped — the POC's report would keep saying what it said and the product's
// console would keep showing what it showed, and the two artefacts a customer
// holds would disagree with each other in a way neither is in a position to
// notice.
//
// So this gate pins ONE file of decisions and checks both sides against it.
//
// # What "both sides" can mean, honestly
//
// The two modules cannot share code. `poc/active-monitoring/go.mod` declares no
// `replace` and Go's `internal/` rule puts `internal/rules` and `internal/pipeline`
// out of the POC's reach, so a shared Go package is not available without inventing
// a third module. What IS available is the fixture: one committed scenario that
// both sides read.
//
// So the check is:
//
//   1. the PRODUCT replays the golden scenario through its real pipeline — the
//      real transport, the real normaliser, the real rule engine — and the
//      decision it reaches must be the one in this file;
//   2. the POC's committed expectations for the same clips must agree about which
//      ones raise, read from `groundtruth.synthetic.json`.
//
// # What this gate does NOT prove
//
// It does not prove the two implementations are equivalent, and it must not be
// described as if it did. It pins a set of decisions and fails when either side
// changes its mind about one of them. That is a weaker claim than equivalence and a
// claim somebody can actually check — and a gate that claimed equivalence would be
// claiming something about two modules it cannot both see.
//
// # Why the expectations are in a FILE and not in the test
//
// Because a decision written in a test is a decision the test's author believes. A
// decision in `decisions.json` is a decision a reviewer can diff, and the diff is
// the review. Changing a line here is changing what this product claims, and it
// shows up in `git log` as exactly that.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Golden is one scenario's committed expectation.
type Golden struct {
	// Scenario is the file name under `testdata/fixtures`, and the KEY. It is a file
	// name rather than a path so a scenario cannot be read from outside the fixture
	// directory by editing the file.
	Scenario string `json:"scenario"`
	// Title is the fixture's own title, carried so a reader of this file can tell
	// which scenario it is without opening the fixture.
	Title string `json:"title"`
	// Raised is whether the pipeline raises an incident at all.
	Raised bool `json:"raised"`
	// RuleID is the rule that fires, empty when nothing is raised.
	RuleID string `json:"ruleId"`
	// Incidents is how many. Greater than one is legitimate — two corroborating
	// rules can both fire — and the count is here because "raised" alone would hide
	// a duplicate.
	Incidents int `json:"incidents"`
	// Why is a sentence for whoever changes this next.
	Why string `json:"why"`
}

// Set is the whole file.
type Set struct {
	Schema int      `json:"schema"`
	Note   string   `json:"note"`
	Golden []Golden `json:"golden"`
}

// Load reads the expectations.
//
// An unknown field is refused for the reason it is everywhere in this repository: a
// misspelled key would decode to an empty list, and an empty list makes every
// scenario look unraised, which is a gate that passes.
func Load(path string) (Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Set{}, fmt.Errorf("golden: read %s: %w", path, err)
	}
	var s Set
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Set{}, fmt.Errorf("golden: %s is not a decision set this build understands: %w", path, err)
	}
	if s.Schema != 1 {
		return Set{}, fmt.Errorf("golden: %s declares schema %d and this build reads 1", path, s.Schema)
	}
	if len(s.Golden) == 0 {
		return Set{}, fmt.Errorf("golden: %s holds no decisions. An empty set is a valid file and "+
			"it is a gate that checks nothing", path)
	}
	return s, nil
}

// Sorted returns the expectations in scenario order.
//
// Sorted rather than in file order so a diff of the FILE and a diff of the failure
// list read the same way, and so a reviewer comparing two runs is not scrolling.
func (s Set) Sorted() []Golden {
	out := append([]Golden(nil), s.Golden...)
	sort.Slice(out, func(i, j int) bool { return out[i].Scenario < out[j].Scenario })
	return out
}

// FixturesDir is where the scenarios live.
func FixturesDir() string { return filepath.Join("..", "..", "testdata", "fixtures") }

// SetPath is the expectations file.
func SetPath() string { return "decisions.json" }
