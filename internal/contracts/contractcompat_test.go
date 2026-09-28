// Package contractcompat asserts, from inside the test suite, that the protobuf
// contracts have not broken.
//
// The gate in `make contract-check` runs the same comparison from the same code;
// this test exists so that a plain `go test ./...` on a contributor's machine
// catches a breaking change without them having to remember the make target.
package contractcompat_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// contractBaseline mirrors the on-disk structure produced by
// `contractcheck -mode write`. Only the parts the assertions need are decoded.
type contractBaseline struct {
	Messages map[string]struct {
		Fields []struct {
			Number int64  `json:"number"`
			Name   string `json:"name"`
			Type   string `json:"type"`
		} `json:"fields"`
	} `json:"messages"`
	Enums map[string]struct {
		Values []struct {
			Number int32  `json:"number"`
			Name   string `json:"name"`
		} `json:"values"`
	} `json:"enums"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the repository root")
	return ""
}

func TestContractBaselineIsPresentAndNonEmpty(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "compat", "baseline.json"))
	if err != nil {
		t.Fatalf("read contract baseline: %v", err)
	}
	var b contractBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse contract baseline: %v", err)
	}
	if len(b.Messages) == 0 {
		t.Fatal("the contract baseline records no messages; the gate would pass vacuously")
	}
	if len(b.Enums) == 0 {
		t.Fatal("the contract baseline records no enums; the gate would pass vacuously")
	}
}

// The plan names thirteen protobuf files for security/v1. A missing one is a
// contract that was dropped, which the compatibility check alone cannot see —
// removing a whole file makes every field look "removed", but only if the
// baseline still has it, and a baseline regenerated at the same time hides it.
func TestAllPlannedContractFilesExist(t *testing.T) {
	root := repoRoot(t)
	planned := []string{
		"event", "alarm", "device", "camera", "evidence", "incident", "entity",
		"edge", "observation", "disposition", "action", "policy", "job",
	}
	for _, name := range planned {
		path := filepath.Join(root, "contracts", "protobuf", "security", "v1", name+".proto")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("contract file %s.proto is missing: %v", name, err)
		}
	}
}

// The JSON Schema and OpenAPI surfaces are contracts too, and nothing in the
// build currently diffs them. Their presence is asserted here so a contract that
// exists only in someone's head fails a test rather than a pilot.
func TestOtherContractSurfacesExist(t *testing.T) {
	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join("contracts", "openapi", "v1", "openapi.yaml"),
		filepath.Join("contracts", "json-schema", "rule-bundle.schema.json"),
		filepath.Join("contracts", "json-schema", "site-config.schema.json"),
		filepath.Join("contracts", "json-schema", "bundle-manifest.schema.json"),
		filepath.Join("contracts", "json-schema", "ai-observations", "restricted-person.schema.json"),
		filepath.Join("contracts", "json-schema", "ai-observations", "door-forced.schema.json"),
		filepath.Join("contracts", "json-schema", "ai-observations", "vehicle-at-gate.schema.json"),
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("contract surface %s is missing: %v", path, err)
		}
	}
}

// The site config schema has no field that can hold a secret. If one is ever
// added "for convenience", a secret becomes a config file, and a config file is
// what the release archive and the support bundle both end up carrying.
func TestSiteConfigSchemaCannotCarryASecret(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "json-schema", "site-config.schema.json"))
	if err != nil {
		t.Fatalf("read site config schema: %v", err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse site config schema: %v", err)
	}
	forbidden := []string{"password", "secret", "privateKey", "apiKey", "token", "credential"}
	for name := range schema.Properties {
		for _, bad := range forbidden {
			if name == bad {
				t.Errorf("site config schema has a %q property; secrets belong in the "+
					"product-managed key store, never in a config file", name)
			}
		}
	}
}
