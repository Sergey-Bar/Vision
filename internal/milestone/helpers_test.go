package milestone_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/fixture"
)

// fixtureDir finds the committed fixture set by walking up to the module root, so
// the test does not depend on the package's working directory.
func fixtureDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "testdata", "fixtures")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not find testdata/fixtures above the working directory")
	return ""
}

func loadFixtures(t *testing.T) []fixture.Scenario {
	t.Helper()
	store, err := fixture.NewStore(fixtureDir(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	scenarios, err := store.All()
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no fixtures found; CI must not require a Milestone server, and these are the ones it uses")
	}
	return scenarios
}
