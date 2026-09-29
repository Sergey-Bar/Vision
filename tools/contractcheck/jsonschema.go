package main

// JSON Schema surface. A published JSON Schema is a contract: an uploaded rule
// bundle, a site config file and a release manifest are all validated against
// these documents, so removing a property, relaxing nothing but retyping
// something, or dropping an enum value breaks a writer that is already in the
// field. Nothing diffed these files before; the baseline simply had no section
// for them, which is how `os.Stat` in a test became the whole control.
//
// One property per dotted path. `a.b` is a property of object `a`; `a[].b` is
// a property of the items of array `a`. Local `$ref`s are resolved so that a
// change inside a `$defs` entry is visible at the path that uses it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Prop struct {
	// Type is the declared type, or "enum" / "const:<json>" when the node is
	// constrained by one of those instead, or "any" for an empty schema.
	Type string `json:"type"`
	// Required is the parent object's `required` membership for this property.
	Required bool `json:"required,omitempty"`
	// Enum is the node's enum, or — for an array — the enum of its items, which
	// is where the closed set actually lives in every schema here.
	Enum []string `json:"enum,omitempty"`
}

type Schema struct {
	Properties map[string]Prop `json:"properties"`
}

func parseSchemaFile(path, rel string, out map[string]Schema) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: schema root is not an object", rel)
	}
	s := Schema{Properties: map[string]Prop{}}
	if err := walkSchema(&s, root, root, "", nil); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	out[rel] = s
	return nil
}

func walkSchema(s *Schema, node, root map[string]any, prefix string, stack []string) error {
	if ref, ok := node["$ref"].(string); ok {
		if contains(stack, ref) {
			// A recursive schema (a condition containing a condition). The shape
			// past this point is the shape already recorded above it, so record
			// the cut point and stop rather than looping.
			s.Properties[prefix] = Prop{Type: "$recursive"}
			return nil
		}
		target, err := resolveRef(root, ref)
		if err != nil {
			return err
		}
		return walkSchema(s, target, root, prefix, append(stack, ref))
	}
	if prefix != "" {
		s.Properties[prefix] = propOf(node)
	}
	required := map[string]bool{}
	for _, r := range requiredNames(node["required"]) {
		required[r] = true
	}
	props, _ := node["properties"].(map[string]any)
	for name, raw := range props {
		child, ok := raw.(map[string]any)
		if !ok {
			child = map[string]any{}
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		if err := walkSchema(s, child, root, key, stack); err != nil {
			return err
		}
		if required[name] {
			p := s.Properties[key]
			p.Required = true
			s.Properties[key] = p
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		if err := walkSchema(s, items, root, prefix+"[]", stack); err != nil {
			return err
		}
	}
	return nil
}

func propOf(node map[string]any) Prop {
	p := Prop{}
	switch {
	case hasKey(node, "type"):
		p.Type, _ = node["type"].(string)
	case hasKey(node, "enum"):
		p.Type = "enum"
	case hasKey(node, "const"):
		b, _ := json.Marshal(node["const"])
		p.Type = "const:" + string(b)
	case hasKey(node, "properties"):
		p.Type = "object"
	default:
		p.Type = "any"
	}
	if vals := enumValues(node["enum"]); len(vals) > 0 {
		p.Enum = vals
	} else if items, ok := node["items"].(map[string]any); ok {
		p.Enum = enumValues(items["enum"])
	}
	return p
}

func resolveRef(root map[string]any, ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("external $ref %q: the schemas here are self-contained by design", ref)
	}
	cur := any(root)
	for _, tok := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		tok = strings.ReplaceAll(tok, "~1", "/")
		tok = strings.ReplaceAll(tok, "~0", "~")
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve", ref)
		}
		cur, ok = m[tok]
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve", ref)
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q does not point at a schema object", ref)
	}
	return m, nil
}

func parseSchemaTree(root string) (map[string]Schema, error) {
	out := map[string]Schema{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".schema.json") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		return parseSchemaFile(path, filepath.ToSlash(rel), out)
	})
	return out, err
}

func compareSchemas(base, cur map[string]Schema) []finding {
	var out []finding
	for rel, old := range base {
		now, ok := cur[rel]
		if !ok {
			out = append(out, finding{rel, "schema removed", "documents validated against it stop validating"})
			continue
		}
		for path, of := range old.Properties {
			nf, ok := now.Properties[path]
			if !ok {
				out = append(out, finding{rel + ":" + path, "property removed",
					"a document carrying this field stops validating"})
				continue
			}
			if nf.Type != of.Type {
				out = append(out, finding{rel + ":" + path, "property type changed",
					fmt.Sprintf("%s -> %s", of.Type, nf.Type)})
			}
			if of.Required && !nf.Required {
				out = append(out, finding{rel + ":" + path, "field no longer required",
					"requiredness changed; readers that relied on the guarantee lose it"})
			}
			nowVals := map[string]bool{}
			for _, v := range nf.Enum {
				nowVals[v] = true
			}
			for _, ov := range of.Enum {
				if !nowVals[ov] {
					out = append(out, finding{rel + ":" + path, "enum value removed",
						fmt.Sprintf("value %q is no longer accepted", ov)})
				}
			}
		}
	}
	return out
}

func normalizeSchemas(m map[string]Schema) {
	for k, s := range m {
		for p, v := range s.Properties {
			sort.Strings(v.Enum)
			s.Properties[p] = v
		}
		m[k] = s
	}
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

// enumValues renders each entry of an enum the way it is written in the
// document: a string stays a string, a number stays a number. A non-string is
// marshalled so that `1` and `true` remain distinguishable in the baseline.
func enumValues(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
			continue
		}
		b, err := json.Marshal(item)
		if err != nil {
			continue
		}
		out = append(out, string(b))
	}
	return out
}

// requiredNames is the `required` array, which is property names and nothing
// else.
func requiredNames(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
