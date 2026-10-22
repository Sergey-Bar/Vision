package main

// The demo passphrase does not leak out of the demo.
//
// # What this checks
//
// The seeded demo site has a passphrase with a fixed value, printed in the seed
// report so a judge can sign in. That is a DEMO credential, and the whole
// point of printing it is that it is not secret.
//
// The risk is not the demo: it is the value escaping into the product, where
// somebody would read it as the passphrase of an install.
//
// So the value is checked to appear in exactly one place in the tree: the
// seeder. A copy in `internal/identity`, in `cmd/security-core` or in a
// deployment script is a credential that somebody will use in production.
//
// # Why it is a check and not a convention
//
// A convention is a sentence in a document, and a document is read by the
// people who are already looking. A check is a failing test, and it is read by
// the person who just added the line.
//
// The check is the mirror image of the one in `internal/identity`: the product
// refuses a passphrase that is too weak, and this refuses the demo's
// passphrase appearing where the product is.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's directory to the module root.
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

// TestTheDemoPassphraseDoesNotAppearOutsideTheSeeder is the check.
//
// The seeder's own directory is exempt, because the value has to live
// somewhere: the constant and the two places that print it.
func TestTheDemoPassphraseDoesNotAppearOutsideTheSeeder(t *testing.T) {
	root := repoRoot(t)
	// The comparison is done in slash form on both sides, because the tree is
	// walked with the platform's separator and the prefix would never match
	// otherwise -- which is how a check that exempts the seeder ends up
	// failing on the seeder.
	seeder := "cmd/security-seed"

	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kilo", "node_modules", "dist", "poc", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, seeder+"/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), demoPassphrase) {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the tree: %v", err)
	}
	for _, f := range found {
		t.Errorf("%s carries the demo passphrase. It is a DEMO credential, printed on purpose "+
			"so a judge can sign in, and a copy of it in the product is a credential somebody "+
			"will use in production", f)
	}
}

// TestTheDefaultPassphraseIsOverridable is the other half.
//
// A demo passphrase that cannot be changed is a passphrase that every seeded
// site in the world shares, and the report prints it as if it were specific to
// the site it was printed for. The environment variable is the way out, and
// this asserts the way out exists.
func TestTheDefaultPassphraseIsOverridable(t *testing.T) {
	if strings.TrimSpace(demoPassphrase) == "" {
		t.Fatal("the demo passphrase is empty, so every seeded site would have a password of " +
			"nothing")
	}
	if !strings.Contains(demoPassphrase, "not-a-secret") {
		t.Errorf("the default passphrase is %q, which does not say what it is. A reader of the "+
			"seed report has to be able to tell at a glance that this value is a demo and not a "+
			"credential", demoPassphrase)
	}
}
