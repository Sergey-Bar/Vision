package fixture_test

// The corpus's expectations, and the rule that keeps them honest.
//
// A scenario with no expectation is a GAP: the audit cannot say whether the
// product missed an incident in it. And a scenario in the table that is not in
// the corpus is a STALE entry: the corpus changed and the table did not.
//
// # Every committed scenario declares what it expects
//
// The table and the corpus are checked against each other, both ways, because
// a table that drifts from the corpus in either direction produces a rate
// about a corpus that does not exist.

import (
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/vision/internal/fixture"
)

func TestEveryCommittedScenarioDeclaresWhatItExpects(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "fixtures")
	store, err := fixture.NewStore(dir)
	if err != nil {
		t.Fatalf("open the fixture store: %v", err)
	}
	manifest, _, err := store.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	var unlabelled []string
	for _, entry := range manifest.Scenarios {
		if _, ok := fixture.ExpectedFor(entry.Name); !ok {
			unlabelled = append(unlabelled, entry.Name)
		}
	}
	if len(unlabelled) > 0 {
		t.Errorf("these scenarios declare no expectation, so the replay audit cannot say "+
			"whether the product missed an incident in them: %v", unlabelled)
	}
}

func TestEveryExpectationNamesACommittedScenario(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "fixtures")
	store, err := fixture.NewStore(dir)
	if err != nil {
		t.Fatalf("open the fixture store: %v", err)
	}
	manifest, _, err := store.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	committed := map[string]bool{}
	for _, entry := range manifest.Scenarios {
		committed[entry.Name] = true
	}
	for name := range fixture.ExpectedIncidents {
		if !committed[name] {
			t.Errorf("the expectation table names %q, which the corpus does not contain. A stale "+
				"entry is a rate about a scenario that does not exist", name)
		}
	}
}

// TestTheDetectionScenariosExpectAnIncident is the one judgement worth
// asserting, because it is the one the rate hangs on.
//
// The scenarios that exist to prove a DETECTION is detected expect an incident.
// The scenarios that exist to prove the product does NOT act expect none. If
// that inversion ever happens, the rate is a rate of the wrong thing.
func TestTheDetectionScenariosExpectAnIncident(t *testing.T) {
	for name, want := range map[string]int{
		"motion-restricted-zone":                1,
		"typed-person-with-bag-restricted-zone": 1,
		"alarm-lifecycle":                       1,
		"duplicate-delivery":                    1,
		"analytics-object-crossed":              0,
		"malformed-envelopes":                   0,
		"missing-camera":                        0,
	} {
		got, ok := fixture.ExpectedFor(name)
		if !ok {
			t.Errorf("the scenario %q declares no expectation", name)
			continue
		}
		if got != want {
			t.Errorf("the scenario %q expects %d incident(s), want %d", name, got, want)
		}
	}
}
