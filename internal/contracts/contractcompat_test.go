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
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	Schemas map[string]struct {
		Properties map[string]struct {
			Type     string   `json:"type"`
			Required bool     `json:"required"`
			Enum     []string `json:"enum"`
		} `json:"properties"`
	} `json:"schemas"`
	OpenAPI map[string]struct {
		Paths map[string][]string `json:"paths"`
	} `json:"openapi"`
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
	if len(b.Schemas) == 0 {
		t.Fatal("the contract baseline records no JSON Schemas; the gate would pass vacuously")
	}
	if len(b.OpenAPI) == 0 {
		t.Fatal("the contract baseline records no OpenAPI paths; the gate would pass vacuously")
	}
}

// A path added to the OpenAPI document but not recorded in the baseline is a
// contract no future run of contractcheck can protect.
//
// The comparison reads the baseline and the document, so a path in NEITHER is
// invisible: removing a path the baseline does not hold looks like nothing
// happened. Eight live paths were in exactly that state when this was written
// -- published, served, and outside the baseline -- and nothing noticed.
//
// The document is parsed with a YAML parser rather than read line by line,
// because a line reader cannot tell a path from a description that mentions
// one, and a gate that counts the wrong thing is worse than no gate.
func TestBaselineRecordsEveryPublishedOpenAPIPath(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "compat", "baseline.json"))
	if err != nil {
		t.Fatalf("read contract baseline: %v", err)
	}
	var b contractBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse contract baseline: %v", err)
	}

	docPath := filepath.Join(root, "contracts", "openapi", "v1", "openapi.yaml")
	rawDoc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read the OpenAPI document: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(rawDoc, &doc); err != nil {
		t.Fatalf("parse the OpenAPI document: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("the OpenAPI document declares no paths; the comparison would pass vacuously")
	}

	entry, ok := b.OpenAPI["v1/openapi.yaml"]
	if !ok {
		t.Fatal("the baseline records no OpenAPI document; run " +
			"contractcheck -mode write -root contracts/openapi")
	}
	for path, item := range doc.Paths {
		methods, recorded := entry.Paths[path]
		if !recorded {
			t.Errorf("the OpenAPI document publishes %s and the baseline does not record "+
				"it. Removing it later would look like nothing happened.", path)
			continue
		}
		for method := range item {
			if !isPublishedMethod(method) {
				continue
			}
			if !containsString(methods, method) {
				t.Errorf("the OpenAPI document publishes %s %s and the baseline records "+
					"only %v for that path", method, path, methods)
			}
		}
	}
}

// isPublishedMethod reports whether a path-item key is an operation.
func isPublishedMethod(key string) bool {
	switch strings.ToLower(key) {
	case "get", "put", "post", "delete", "options", "head", "patch", "trace":
		return true
	}
	return false
}

// containsString reports whether a list holds a value.
func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// A schema added to contracts/json-schema but not recorded in the baseline is a
// contract no future run of contractcheck can protect. The tool reads the tree
// and the baseline from the same place, so nothing notices a file that exists
// in one and not the other unless something compares the two.
func TestBaselineRecordsEveryPublishedSchema(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "compat", "baseline.json"))
	if err != nil {
		t.Fatalf("read contract baseline: %v", err)
	}
	var b contractBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("parse contract baseline: %v", err)
	}
	schemaRoot := filepath.Join(root, "contracts", "json-schema")
	found := 0
	err = filepath.WalkDir(schemaRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(schemaRoot, path)
		if err != nil {
			return err
		}
		found++
		key := filepath.ToSlash(rel)
		entry, ok := b.Schemas[key]
		if !ok {
			t.Errorf("%s is not in the baseline; run contractcheck -mode write -root contracts/json-schema", key)
			return nil
		}
		if len(entry.Properties) == 0 {
			t.Errorf("the baseline records %s with no properties, which cannot detect a removed field", key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", schemaRoot, err)
	}
	if found == 0 {
		t.Fatal("no schemas were found on disk; the walk found nothing to compare")
	}
}

// The plan names thirteen protobuf files for security/v1. A missing one is a
// contract that was dropped, which the compatibility check alone cannot see -
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
