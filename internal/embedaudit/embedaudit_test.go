package embedaudit_test

// The video-embed-audit gate's tests: the repo's own tree is clean, and the
// scanner refuses the definitions it exists for.
//
// Both halves matter. A scanner that passes because it matches nothing is a
// scanner that has never been tried; a scanner that fails on the real tree is a
// scanner that will be turned off. The fixtures here are the first half and the
// committed tree is the second.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/embedaudit"
)

// TestTheSubjectSetIsNotEmpty guards the guard.
//
// A gate whose subject list emptied itself out would pass on everything, and the
// green result would read as a clean tree. This asserts the closed set still holds
// the subjects the plan names.
func TestTheSubjectSetIsNotEmpty(t *testing.T) {
	if embedaudit.SensitiveSubjectCount() < 4 {
		t.Errorf("the sensitive subject set holds %d entries, want at least the four "+
			"the plan names (person, plate, face_crop, credential)", embedaudit.SensitiveSubjectCount())
	}
}

// TestTheCommittedTreeHasNoSensitiveVectorDefinition.
//
// The real tree, and the gate's whole claim: what is committed today cannot
// define a person, plate, face-crop or credential vector.
func TestTheCommittedTreeHasNoSensitiveVectorDefinition(t *testing.T) {
	root := repoRoot(t)
	findings, err := embedaudit.Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestAVectorColumnOnASensitiveTableIsRefused.
//
// The definition the gate exists for: a migration that adds `vector(1536)` to a
// `face_crop` table. On synthetic files, because the tree must not hold one.
func TestAVectorColumnOnASensitiveTableIsRefused(t *testing.T) {
	root := t.TempDir()
	mig := filepath.Join(root, "migrations")
	if err := os.MkdirAll(mig, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(mig, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("0027_face_crop_embedding.expand.sql", `
-- The definition this gate exists to refuse.
CREATE TABLE face_crop_embedding (
    tenant_id    TEXT NOT NULL,
    face_crop_id TEXT NOT NULL,
    embedding    vector(1536) NOT NULL
);`)

	findings, err := embedaudit.Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("the gate reported %d findings, want exactly 1: %v", len(findings), findings)
	}
	if findings[0].Subject != "face_crop" && findings[0].Subject != "facecrop" {
		t.Errorf("the finding names subject %q, want the face-crop one", findings[0].Subject)
	}
}

// TestAProtoVectorFieldOnASensitiveMessageIsRefused.
//
// The same violation in the wire contract: a repeated float on a plate message.
func TestAProtoVectorFieldOnASensitiveMessageIsRefused(t *testing.T) {
	root := t.TempDir()
	pb := filepath.Join(root, "contracts", "protobuf", "security", "v1")
	if err := os.MkdirAll(pb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pb, "plate.proto"), []byte(`
syntax = "proto3";
package security.v1;
message PlateVector {
  string plate_id = 1;
  repeated float embedding = 2;
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := embedaudit.Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("the gate reported %d findings, want exactly 1: %v", len(findings), findings)
	}
}

// TestAProseDenialIsNotAFinding.
//
// The word boundary and the subject-in-name rule: a comment saying no plate
// vector is stored is the truth, not a violation. A scanner that flagged prose
// would be trained to be ignored.
func TestAProseDenialIsNotAFinding(t *testing.T) {
	root := t.TempDir()
	mig := filepath.Join(root, "migrations")
	if err := os.MkdirAll(mig, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mig, "0001_notes.expand.sql"), []byte(`
-- No plate vector is stored: the C7 gate holds this, and a scanner that
-- flagged this comment would be trained to be ignored.
CREATE TABLE plate_reference (
    tenant_id TEXT NOT NULL,
    plate_id  TEXT NOT NULL
);`), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := embedaudit.Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, f := range findings {
		t.Errorf("prose was flagged: %s", f)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}

var _ = strings.TrimSpace
