package fixture_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func testEpoch() time.Time { return time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC) }

func zeroTime() time.Time { return time.Time{} }

func newReadCloser(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

// repoFixtureDir finds the committed fixture set by walking up to the module
// root, so the test does not depend on the package's working directory.
func repoFixtureDir(t interface{ Fatalf(string, ...any) }) string {
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
