package golden

// The gate: every committed scenario has a decision, every decision names a
// scenario that exists, and the POC's machine-derivable verdicts do not contradict
// the product's.
//
// # Why the first two checks are the ones that matter
//
// A golden gate has one failure mode that looks like success: it covers less than
// the tree. A fixture with no committed decision is behaviour nobody has decided,
// and a decision with no fixture is a row that will never be checked and reads as
// coverage. Both pass silently, and both make the gate SHRINK as the repository
// grows, which is the opposite of what a gate is for.
//
// So the file-integrity checks come first and are unconditional: they need no
// database, they cannot flake, and they are the checks that fail when somebody adds
// a fixture in a hurry.
//
// # What is NOT here, and why
//
// This commit pins the DECISIONS and proves they are complete and self-consistent.
// It does not yet replay each scenario through `pipeline.Loops` and compare what the
// pipeline reaches, and the difference matters: a decision file nobody has compared
// against a pipeline is a list of claims.
//
// The execution half is the next piece and it is not small — it needs a per-package
// database, the real transport, and the real rule engine assembled the way
// `internal/pipeline` assembles them. Writing it as a stub with a "TODO" in the
// middle would make this gate look finished and not be, which is worse than the
// gap being visible.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The committed decision set is the thing a reviewer diffs.
func loadSet(t *testing.T) Set {
	t.Helper()
	s, err := Load(SetPath())
	if err != nil {
		t.Fatalf("%v", err)
	}
	return s
}

func scenarioNamesOnDisk(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(FixturesDir(), "*.json"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no fixtures under %s: %v", FixturesDir(), err)
	}
	out := map[string]bool{}
	for _, p := range entries {
		out[filepath.Base(p)] = true
	}
	// The manifest is not a scenario, and treating it as one would demand a
	// decision for a file that describes the others.
	delete(out, "manifest.json")
	return out
}

// Every fixture has a decision, and every decision has a fixture.
//
// Both directions, because they fail differently: a fixture with no decision is
// behaviour nobody has decided, and a decision with no fixture is a row that will
// never be checked.
func TestEveryFixtureHasADecisionAndEveryDecisionHasAFixture(t *testing.T) {
	set := loadSet(t)
	onDisk := scenarioNamesOnDisk(t)
	decided := map[string]bool{}

	for _, g := range set.Golden {
		if decided[g.Scenario] {
			t.Errorf("%s has two decisions. Two rows for one scenario is a decision the "+
				"gate has to choose between, and choosing is what a reviewer does", g.Scenario)
		}
		decided[g.Scenario] = true
		if !onDisk[g.Scenario] {
			t.Errorf("the decision file names %s, which is not a fixture on disk. A decision "+
				"with no fixture is a row that will never be checked and reads as coverage",
				g.Scenario)
		}
		if g.Why == "" {
			t.Errorf("%s carries no reason. A decision with no stated reason is one somebody "+
				"changes without knowing what was at stake", g.Scenario)
		}
	}
	for name := range onDisk {
		if !decided[name] {
			t.Errorf("%s has no committed decision. A fixture whose behaviour nothing has "+
				"decided is a fixture this gate does not cover, and the gate shrinks as the "+
				"tree grows", name)
		}
	}
}

// The rule id is consistent with the boolean.
//
// A decision that says `raised: false` and still names a rule is a claim about two
// different things, and a gate that reads only the boolean would pass it.
func TestADecisionIsInternallyConsistent(t *testing.T) {
	for _, g := range loadSet(t).Golden {
		switch {
		case !g.Raised && g.RuleID != "":
			t.Errorf("%s says nothing was raised and names rule %q. One of the two is wrong "+
				"and a reader has no way to tell which", g.Scenario, g.RuleID)
		case g.Raised && g.RuleID == "":
			t.Errorf("%s says an incident was raised and names no rule. An incident with no "+
				"rule is one nobody can re-derive, which is the thing this product claims "+
				"not to do", g.Scenario)
		case g.Raised && g.Incidents < 1:
			t.Errorf("%s says it was raised and counts %d incident(s)", g.Scenario, g.Incidents)
		case !g.Raised && g.Incidents != 0:
			t.Errorf("%s says nothing was raised and counts %d incident(s)", g.Scenario, g.Incidents)
		}
	}
}

// The POC's machine-derivable verdicts and the product's decisions must not
// contradict each other.
//
// # The weakest of the checks, and it says so
//
// The two modules cannot share Go code — `poc/active-monitoring/go.mod` declares
// no `replace` and Go's `internal/` rule puts the product's packages out of the
// POC's reach — so this compares two committed FILES rather than two executions,
// and a file-to-file comparison is a weaker claim than a gate that runs both.
//
// What it does catch is the disagreement that matters: the POC adjudicating a clip
// as a FALSE POSITIVE for footage the product raises an incident for is not "two
// views of the same footage". One of them is wrong, and which one is a question
// this repository is arguing should not need a human to answer.
//
// # Why a shared name is the collision worth checking
//
// The POC's keys are clip NAMES (`bird-synthetic.mp4`) and the product's are
// scenario NAMES (`analytics-object-crossed.json`). There is no join between them in
// either module, and that absence is itself the finding — but a name that is both a
// scenario and a clip, asserting opposite things, is a coincidence that would hide a
// real disagreement behind "they are different things".
func TestThePocsAdjudicationsDoNotContradictTheProductsDecisions(t *testing.T) {
	set := loadSet(t)
	clips := machineVerdictClips(t)
	if len(clips) == 0 {
		t.Skip("the POC's synthetic review is not present in this checkout")
	}

	raised := map[string]string{} // scenario -> why, for the ones that raise
	for _, g := range set.Golden {
		if g.Raised {
			raised[g.Scenario] = g.Why
		}
	}
	for scenario, why := range raised {
		for _, clip := range clips {
			if clip == scenario {
				t.Errorf("%s is a scenario the product RAISES and a clip the POC has "+
					"adjudicated. The same name carries opposite claims from two modules "+
					"that cannot see each other.\n  the product says: %s", scenario, why)
			}
		}
	}
}

// machineVerdictClips reads the clip names out of the POC's synthetic review.
//
// It reads the FILE and does not import the POC. Importing the POC from the product
// is the thing the two-module split exists to prevent — and a gate that could
// import both would not be a cross-check, it would be a fourth implementation.
func machineVerdictClips(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "..", "poc", "active-monitoring", "testdata", "groundtruth.synthetic.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the POC's synthetic review is not present at %s: %v", path, err)
	}
	var doc struct {
		Marks []struct {
			Clip    string `json:"clip"`
			Verdict string `json:"verdict"`
		} `json:"marks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	out := make([]string, 0, len(doc.Marks))
	for _, m := range doc.Marks {
		if m.Verdict != "false_positive" && m.Verdict != "true_positive" {
			t.Errorf("%s adjudicates %s as %q, which is neither true_positive nor "+
				"false_positive. A verdict the counting code does not know is a verdict "+
				"that will not be counted", path, m.Clip, m.Verdict)
		}
		out = append(out, m.Clip)
	}
	return out
}

// The set loads, and it is not a file with nothing in it.
//
// The same check `Load` makes, asserted here as a test, because a gate that depends
// on a file being well-formed should say so where somebody reading the gate can see
// it rather than only in the loader's error path.
func TestTheDecisionSetLoadsAndIsNotEmpty(t *testing.T) {
	s := loadSet(t)
	if len(s.Golden) == 0 {
		t.Fatal("the decision set is empty. An empty set is a valid file and it is a gate " +
			"that checks nothing")
	}
	if s.Note == "" {
		t.Error("the decision set carries no note. This file is what a reviewer diffs when " +
			"a decision changes, and the note is what tells them what they are looking at")
	}
	// And the file names itself, so a reader who has it open knows what it is.
	for _, g := range s.Golden {
		if g.Title == "" {
			t.Errorf("%s carries no title. A row with a scenario name and no title is a "+
				"row nobody can read without opening the fixture", g.Scenario)
		}
	}
}
