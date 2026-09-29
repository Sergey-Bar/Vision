package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// copyTree copies a contract surface into a temp directory so a test can break
// it without touching the published contract. The contracts are the source of
// truth and are not this package's to edit.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

func editJSON(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	mutate(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func kinds(findings []finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Kind)
	}
	return out
}

func hasKind(findings []finding, kind string) bool {
	for _, f := range findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

// D5(a): the field type token has to accept a map. Before this, `map<string,
// string>` stopped the type token at the `<`, the field did not match at all,
// and `Device.attributes` was invisible to the gate.
func TestMapFieldsAreInTheBaseline(t *testing.T) {
	dir := t.TempDir()
	src := `syntax = "proto3";
package security.v1;
message Device {
  string device_id = 1;
  map<string, string> attributes = 10;
}`
	if err := os.WriteFile(filepath.Join(dir, "device.proto"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := parseProtoTree(dir)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fields := c.Messages["security.v1.Device"].Fields
	if len(fields) != 2 {
		t.Fatalf("expected both fields, got %d: %+v", len(fields), fields)
	}
	var attr Field
	for _, f := range fields {
		if f.Name == "attributes" {
			attr = f
		}
	}
	if attr.Type != "map<string,string>" {
		t.Errorf("attributes type = %q, want map<string,string>", attr.Type)
	}
	if attr.Number != 10 {
		t.Errorf("attributes number = %d, want 10", attr.Number)
	}
	if !attr.Repeated {
		t.Error("a map field is repeated on the wire; the baseline must say so")
	}
}

// A reformat of the map's whitespace is not a retype of it.
func TestMapTypeWhitespaceIsNotARetype(t *testing.T) {
	if got := normalizeProtoType("map<string,  string>"); got != "map<string,string>" {
		t.Errorf("normalizeProtoType = %q, want map<string,string>", got)
	}
}

// D5(b), and the red-check for it: remove a property from a copy of the real
// site-config schema and assert the gate notices. If the schema comparison is
// removed, narrowed, or pointed at the wrong tree, this goes green without the
// gate existing.
func TestGateSeesARemovedSchemaProperty(t *testing.T) {
	root := repoRoot(t)
	published := filepath.Join(root, "contracts", "json-schema")
	base, err := parseSchemaTree(published)
	if err != nil {
		t.Fatalf("read the published schemas: %v", err)
	}

	tmp := t.TempDir()
	copyTree(t, published, tmp)
	target := filepath.Join(tmp, "site-config.schema.json")
	editJSON(t, target, func(doc map[string]any) {
		props := doc["properties"].(map[string]any)
		delete(props, "evidence")
		// And the required entry, so this is a whole field disappearing rather
		// than a field that merely stopped being mandatory.
		req := doc["required"].([]any)
		kept := make([]any, 0, len(req))
		for _, r := range req {
			if r != "evidence" {
				kept = append(kept, r)
			}
		}
		doc["required"] = kept
	})

	broken, err := parseSchemaTree(tmp)
	if err != nil {
		t.Fatalf("read the broken schemas: %v", err)
	}
	findings := compareSchemas(base, broken)
	if !hasKind(findings, "property removed") {
		t.Fatalf("removing `evidence` from site-config produced no finding; got %v", kinds(findings))
	}
	var found bool
	for _, f := range findings {
		if f.Symbol == "site-config.schema.json:evidence" {
			found = true
		}
	}
	if !found {
		t.Errorf("the finding does not name the property that was removed: %+v", findings)
	}
}

// Retyping a field is breaking even though the field is still there and still
// carries a value.
func TestGateSeesARetypedSchemaProperty(t *testing.T) {
	write := func(body string) map[string]Schema {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.schema.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := parseSchemaTree(dir)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return m
	}
	base := write(`{"type":"object","properties":{"port":{"type":"integer"}}}`)
	broken := write(`{"type":"object","properties":{"port":{"type":"string"}}}`)
	if !hasKind(compareSchemas(base, broken), "property type changed") {
		t.Fatal("integer -> string on the same path was not reported")
	}
}

func TestGateSeesARemovedEnumValue(t *testing.T) {
	base := map[string]Schema{"a.schema.json": {Properties: map[string]Prop{
		"state": {Type: "enum", Enum: []string{"closed", "open"}},
	}}}
	broken := map[string]Schema{"a.schema.json": {Properties: map[string]Prop{
		"state": {Type: "enum", Enum: []string{"closed"}},
	}}}
	if !hasKind(compareSchemas(base, broken), "enum value removed") {
		t.Fatal("dropping `open` from the enum was not reported")
	}
}

func TestGateSeesARelaxedRequiredField(t *testing.T) {
	base := map[string]Schema{"a.schema.json": {Properties: map[string]Prop{
		"siteId": {Type: "string", Required: true},
	}}}
	broken := map[string]Schema{"a.schema.json": {Properties: map[string]Prop{
		"siteId": {Type: "string"},
	}}}
	if !hasKind(compareSchemas(base, broken), "field no longer required") {
		t.Fatal("dropping siteId out of `required` was not reported")
	}
}

// D5(c): an endpoint that stops existing is a contract change no other gate in
// the repository can see.
func TestGateSeesARemovedEndpoint(t *testing.T) {
	base := map[string]APIDoc{"v1/openapi.yaml": {Paths: map[string][]string{
		"/v1/incidents":    {"get", "post"},
		"/v1/audit/ledger": {"get"},
	}}}
	broken := map[string]APIDoc{"v1/openapi.yaml": {Paths: map[string][]string{
		"/v1/incidents": {"get"},
	}}}
	findings := compareOpenAPI(base, broken)
	if !hasKind(findings, "method removed") {
		t.Errorf("POST /v1/incidents disappearing was not reported: %+v", findings)
	}
	deleted := map[string]APIDoc{"v1/openapi.yaml": {Paths: map[string][]string{
		"/v1/incidents": {"get", "post"},
	}}}
	if !hasKind(compareOpenAPI(base, deleted), "path removed") {
		t.Error("the /v1/audit/ledger path disappearing was not reported")
	}
}

// The published document is read as the path/method set it actually declares.
func TestOpenAPIReaderReadsThePublishedDocument(t *testing.T) {
	root := repoRoot(t)
	docs, warnings, err := parseOpenAPITree(filepath.Join(root, "contracts", "openapi"))
	if err != nil {
		t.Fatalf("read the published OpenAPI: %v", err)
	}
	doc, ok := docs["v1/openapi.yaml"]
	if !ok {
		t.Fatalf("the published document was not read: %v", docs)
	}
	if len(doc.Paths) < 15 {
		t.Errorf("read %d paths; the published document declares far more, so the reader "+
			"is not reading the `paths:` block", len(doc.Paths))
	}
	for path, want := range map[string]string{
		"/v1/auth/login":   "post",
		"/v1/incidents":    "get",
		"/v1/auth/session": "delete",
	} {
		var found bool
		for _, m := range doc.Paths[path] {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s has no %s; got %v", path, want, doc.Paths[path])
		}
	}
	// The published document is well-formed. It was not: /v1/health and
	// /v1/health/ready put their operation body beside the method rather than
	// under it, which the specification ignores - so as published, those two
	// endpoints described themselves as having no responses. That was fixed in
	// contracts/openapi/v1/openapi.yaml, and this assertion is what holds it
	// fixed: the warning is about a shape the document must not have, and the
	// only way to keep the count at zero is to keep the document correct.
	//
	// The reader still has to report the shape when it sees it, or the next
	// regression is silent. That half is asserted against a deliberately
	// malformed copy below rather than against the shipped file, because a test
	// that depends on a defect existing in the repository is a test that fails
	// for the right reason at the worst possible time.
	if len(warnings) != 0 {
		t.Errorf("the published OpenAPI document is malformed: %v", warnings)
	}

	// And the reader does report it when the shape is there.
	broken := filepath.Join(t.TempDir(), "malformed.yaml")
	writeMalformedOpenAPI(t, broken)
	if _, got, err := parseOpenAPITree(broken); err != nil || len(got) == 0 {
		t.Errorf("a document with an operation body beside the method produced %d warnings "+
			"(err %v). The warning is how this class of defect is caught at all, and it "+
			"cannot be asserted only against a document that no longer has the defect",
			len(got), err)
	}
}

// writeMalformedOpenAPI writes a document with the operation body at path level
// rather than under the method, which is the exact shape the published document
// used to have.
func writeMalformedOpenAPI(t *testing.T, path string) {
	t.Helper()
	body := `openapi: 3.0.3
info: { title: malformed, version: "1" }
paths:
  /v1/health:
    get:
      tags: [health]
    security: []
    summary: Liveness
    responses:
      "200":
        description: up
  /v1/health/ready:
    get:
      tags: [health]
    security: []
    summary: Readiness
    responses:
      "200":
        description: ready
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the malformed document: %v", err)
	}
}

// R4: the first run over a surface the tool has never diffed will surface
// facts, not regressions. A refresh must therefore be a pure addition, and the
// refusal is the control - this is the test that goes red if the refusal is
// removed.
func TestWriteRefusesToBakeInABreakingChange(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "baseline.json")
	published := map[string]Schema{"a.schema.json": {Properties: map[string]Prop{
		"siteId": {Type: "string", Required: true},
	}}}
	seed, err := json.Marshal(Baseline{Schemas: published})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, seed, 0o644); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.schema.json"),
		[]byte(`{"type":"object","properties":{"name":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	surface, err := detectSurface(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	current, _, err := readSurface(surface, root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var stored Baseline
	if err := json.Unmarshal(seed, &stored); err != nil {
		t.Fatal(err)
	}
	findings := compareSurface(surface, &stored, current)
	if len(findings) == 0 {
		t.Fatal("swapping siteId for name was not detected, so there is nothing to refuse")
	}
	if !hasKind(findings, "property removed") {
		t.Fatalf("expected a property removal, got %v", kinds(findings))
	}

	// The detection alone is not the control. The control is that a refresh
	// which drops something is refused unless the operator says so on the
	// command line.
	err = writeRefusal(findings, false)
	if err == nil {
		t.Fatal("a baseline refresh that removes a published property was allowed")
	}
	if !strings.Contains(err.Error(), "property removed") {
		t.Errorf("the refusal does not say what it is refusing: %v", err)
	}
	if err := writeRefusal(findings, true); err != nil {
		t.Errorf("-allow-breaking did not permit a deliberate version change: %v", err)
	}
	if err := writeRefusal(nil, false); err != nil {
		t.Errorf("a pure addition was refused: %v", err)
	}
}

// A refresh that only adds is allowed, and it must not touch the sections it
// did not read: a protobuf run may not delete the schema section.
func TestWriteOnlyReplacesItsOwnSection(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "baseline.json")
	seed := Baseline{
		Messages: map[string]Message{"security.v1.Device": {Fields: []Field{{Number: 1, Name: "device_id", Type: "string"}}}},
		Schemas:  map[string]Schema{"a.schema.json": {Properties: map[string]Prop{"kept": {Type: "string"}}}},
		OpenAPI:  map[string]APIDoc{"v1/openapi.yaml": {Paths: map[string][]string{"/v1/x": {"get"}}}},
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var stored Baseline
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	fresh := newBaseline()
	fresh.Messages["security.v1.Device"] = Message{Fields: []Field{
		{Number: 1, Name: "device_id", Type: "string"},
		{Number: 2, Name: "added", Type: "string"},
	}}
	merge(&stored, fresh, surfaceProto)

	if _, ok := stored.Schemas["a.schema.json"]; !ok {
		t.Error("a protobuf write deleted the schema section")
	}
	if _, ok := stored.OpenAPI["v1/openapi.yaml"]; !ok {
		t.Error("a protobuf write deleted the openapi section")
	}
	if len(stored.Messages["security.v1.Device"].Fields) != 2 {
		t.Error("the protobuf section was not replaced")
	}
}

// A surface the tool cannot identify must not be read as compatible.
func TestDetectSurfaceRefusesAnUnknownRoot(t *testing.T) {
	if _, err := detectSurface(t.TempDir()); err == nil {
		t.Fatal("an empty directory was accepted as a contract surface")
	}
	if _, err := detectSurface(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a missing directory was accepted")
	}
	for _, tc := range []struct {
		root string
		file string
		want surface
	}{
		{"p", "a.proto", surfaceProto},
		{"s", "a.schema.json", surfaceSchema},
		{"o", "a.yaml", surfaceOpenAPI},
		{"o", "a.yml", surfaceOpenAPI},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, tc.root), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tc.root, tc.file), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := detectSurface(filepath.Join(dir, tc.root))
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if got != tc.want {
			t.Errorf("%s detected as %v, want %v", tc.file, got, tc.want)
		}
	}
}

// The published JSON Schemas are self-contained. An external $ref would mean a
// document that cannot be read on its own, and saying so beats reading a
// partial schema.
func TestExternalRefIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.schema.json"),
		[]byte(`{"type":"object","properties":{"x":{"$ref":"https://example.invalid/x.json"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parseSchemaTree(dir)
	if err == nil || !strings.Contains(err.Error(), "external $ref") {
		t.Fatalf("err = %v, want a refusal to follow an external $ref", err)
	}
}
