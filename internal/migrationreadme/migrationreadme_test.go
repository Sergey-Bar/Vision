package migrationreadme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's directory to the module root.
//
// A fixed relative path would break the moment the package moved, and a test
// that reads the wrong tree is worse than no test: it reports green about a
// document it never opened.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// TestTheReadmeDescribesTheDirectory is the DoD for migrations/README.md.
//
// It fails when a migration exists and the document does not name it, when the
// document names a migration that does not exist, when the stated version range
// is not the directory's own range, and when the quoted compatibility manifest
// is not the manifest on disk.
func TestTheReadmeDescribesTheDirectory(t *testing.T) {
	findings, err := Check(repoRoot(t))
	if err != nil {
		t.Fatalf("migrationreadme: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestTheDirectoryAndTheTableAreTheSameSet pins the derivation itself.
//
// TestTheReadmeDescribesTheDirectory would pass vacuously if Check read nothing
// and the document happened to describe nothing, so the directory and the table
// are counted here independently.
func TestTheDirectoryAndTheTableAreTheSameSet(t *testing.T) {
	root := repoRoot(t)

	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatalf("read the migrations directory: %v", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) < 2 {
		t.Fatalf("the migrations directory holds %d .sql files, so the check is asserting almost "+
			"nothing", len(files))
	}

	raw, err := os.ReadFile(filepath.Join(root, "migrations", "README.md"))
	if err != nil {
		t.Fatalf("read the README: %v", err)
	}
	rows := 0
	for _, line := range splitLines(string(raw)) {
		if tableRowRe.MatchString(line) {
			rows++
		}
	}
	if rows != len(files) {
		t.Errorf("the README's file table has %d rows and the directory holds %d .sql files. "+
			"Every migration needs a row, and every row needs a migration", rows, len(files))
	}
}

// TestTheGateFailsWhenTheTableIsShort is the red-check for this gate.
//
// It proves the gate is a gate: a README that describes fourteen migrations in
// a directory of twenty-eight is refused, rather than the check quietly
// passing because it compared a directory against itself.
func TestTheGateFailsWhenTheTableIsShort(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "migrations", "README.md"))
	if err != nil {
		t.Fatalf("read the README: %v", err)
	}
	lines := splitLines(string(raw))

	// Drop the LAST table row, which is the highest-numbered migration.
	last := -1
	for i, line := range lines {
		if tableRowRe.MatchString(line) {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("the README has no file table rows at all, so there is nothing to drop")
	}
	shortened := ""
	for i, line := range lines {
		if i == last {
			continue
		}
		shortened += line + "\n"
	}

	findings := checkText(shortened, filesInDirectory(t, root))
	if len(findings) == 0 {
		t.Fatal("dropping the highest-numbered migration from the README produced no finding, so " +
			"the gate is not a gate: it would pass on a document that describes a directory which " +
			"no longer exists")
	}
}

// filesInDirectory is the directory's own .sql files.
func filesInDirectory(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatalf("read the migrations directory: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// splitLines splits on both line endings, because a document edited on Windows
// and a document edited on Linux are the same document.
func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
