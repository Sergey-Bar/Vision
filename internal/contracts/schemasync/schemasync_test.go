package schemasync_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/vision/internal/contracts/schemasync"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

// The embedded copy and the published contract must be the same bytes. A copy
// that has drifted is a document being validated against a contract nobody
// published, which is the failure this test exists to make loud.
func TestEmbeddedSchemasMatchPublished(t *testing.T) {
	root := repoRoot(t)
	if len(schemasync.Published) == 0 {
		t.Fatal("no schemas are embedded; the sync check would pass vacuously")
	}
	for published, embeddedName := range schemasync.Published {
		t.Run(published, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(published)))
			if err != nil {
				t.Fatalf("read the published contract: %v", err)
			}
			got, err := schemasync.Bytes(embeddedName)
			if err != nil {
				t.Fatalf("read the embedded copy: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the embedded copy of %s is stale.\n"+
					"The schema compiled into the binary is not the schema that was published:\n"+
					"  cp %s internal/contracts/schemasync/%s\n",
					published, filepath.FromSlash(published), filepath.FromSlash(embeddedName))
			}
		})
	}
}

// A schema added under contracts/json-schema without a Go-side copy is a
// contract the binary cannot validate against, and a drift between the two
// trees nobody would ever see. This fails until the copy is made and listed in
// schemasync.Published.
func TestEveryPublishedSchemaHasAnEmbeddedCopy(t *testing.T) {
	root := repoRoot(t)
	publishedRoot := filepath.Join(root, "contracts", "json-schema")
	err := filepath.WalkDir(publishedRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		published := filepath.ToSlash(rel)
		if _, ok := schemasync.Published[published]; !ok {
			t.Errorf("%s has no embedded copy; add it to schemasync.Published and to the "+
				"//go:embed directive, or the binary cannot validate against it", published)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", publishedRoot, err)
	}
}

// The copies are only worth carrying if they are readable as schemas. This does
// not validate documents against them - it proves the bytes are the JSON the
// published contract is, and that a truncated copy fails here rather than in a
// validator at the point an operator uploads something.
func TestEmbeddedCopiesAreParsableJSON(t *testing.T) {
	for published, embeddedName := range schemasync.Published {
		t.Run(published, func(t *testing.T) {
			raw, err := schemasync.Bytes(embeddedName)
			if err != nil {
				t.Fatalf("read the embedded copy: %v", err)
			}
			if !json.Valid(raw) {
				t.Errorf("the embedded copy of %s is not valid JSON (%d bytes)", published, len(raw))
			}
		})
	}
}
