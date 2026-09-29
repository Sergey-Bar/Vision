package main

// OpenAPI surface. The HTTP contract is a contract like any other: an adapter
// or a console build written against `GET /v1/incidents/{id}` stops working the
// day the method is dropped or the path is renamed, and no other gate in this
// repository can see that happen.
//
// This is a deliberately small reader, not a YAML parser. The document is
// machine-generated-looking and strictly two-space indented, and the only two
// things worth a compatibility gate are the path set and the method set under
// it. A dependency-free reader that fails loudly on a layout it does not
// recognise is better than a general parser that silently reads a different
// document from the one that ships.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// httpMethods is the OpenAPI fixed field set. Anything else at the four-space
// indent under a path is not an operation, and treating it as one would put
// nonsense in the baseline.
var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// pathItemFields are the fields the OpenAPI specification allows on a path item
// itself rather than on one of its operations. `contracts/openapi/v1/openapi.yaml`
// indents `security: []` at the path level for `/v1/health`, which the
// specification then reads as "no authentication on this path" — the author's
// intent, and a real fact about the published surface, so it is a path field
// here too and not a parse error.
var pathItemFields = map[string]bool{
	"$ref": true, "summary": true, "description": true,
	"servers": true, "parameters": true, "security": true,
}

type APIDoc struct {
	Paths map[string][]string `json:"paths"`
}

func parseOpenAPIFile(path, rel string, out map[string]APIDoc, warnings *[]string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	doc := APIDoc{Paths: map[string][]string{}}
	var (
		inPaths   bool
		curPath   string
		seenPath  bool
		foundPath bool
	)
	for n, raw2 := range lines {
		line := strings.TrimRight(raw2, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.Contains(line[:indent], "\t") {
			return fmt.Errorf("%s:%d: tab indentation; the path reader assumes spaces", rel, n+1)
		}
		key := yamlKey(line)

		if indent == 0 {
			inPaths = key == "paths"
			curPath, seenPath = "", false
			continue
		}
		if !inPaths {
			continue
		}
		switch {
		case indent == 2:
			if !strings.HasPrefix(key, "/") {
				return fmt.Errorf("%s:%d: %q is not a path under `paths:`", rel, n+1, key)
			}
			curPath, seenPath = key, true
			foundPath = true
			doc.Paths[curPath] = nil
		case indent == 4 && seenPath:
			if pathItemFields[key] {
				continue
			}
			if !httpMethods[key] {
				// Not a method, so it cannot change the path/method set this
				// gate compares. It does mean the document is malformed - an
				// operation body that is not nested under its method - and that
				// is worth saying out loud rather than either swallowing or
				// refusing to check the rest of the file.
				*warnings = append(*warnings, fmt.Sprintf(
					"%s:%d: %q sits beside the method under path %q instead of inside it; "+
						"the specification ignores it there, so the published document and the "+
						"server it documents disagree", rel, n+1, key, curPath))
				continue
			}
			doc.Paths[curPath] = append(doc.Paths[curPath], key)
		}
	}
	if !foundPath {
		return fmt.Errorf("%s: no `paths:` block found", rel)
	}
	out[rel] = doc
	return nil
}

// yamlKey is the part of a mapping line before its colon, so that `get:` and
// `security: []` both yield a bare key. It is not a YAML parser and does not
// try to be; every line it is pointed at is a two-space-indented mapping entry.
func yamlKey(line string) string {
	head := strings.TrimSpace(line)
	if i := strings.Index(head, ":"); i >= 0 {
		return strings.TrimSpace(head[:i])
	}
	return head
}

// parseOpenAPITree returns the path/method set of every OpenAPI document under
// root, plus any warnings about documents it had to make a judgement call on.
func parseOpenAPITree(root string) (map[string]APIDoc, []string, error) {
	out := map[string]APIDoc{}
	var warnings []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isYAML(path) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		return parseOpenAPIFile(path, filepath.ToSlash(rel), out, &warnings)
	})
	return out, warnings, err
}

func compareOpenAPI(base, cur map[string]APIDoc) []finding {
	var out []finding
	for rel, old := range base {
		now, ok := cur[rel]
		if !ok {
			out = append(out, finding{rel, "openapi document removed", "the published HTTP surface is gone"})
			continue
		}
		for path, methods := range old.Paths {
			nowMethods, ok := now.Paths[path]
			if !ok {
				out = append(out, finding{rel + " " + path, "path removed",
					"every client calling it gets a 404"})
				continue
			}
			have := map[string]bool{}
			for _, m := range nowMethods {
				have[m] = true
			}
			for _, m := range methods {
				if !have[m] {
					out = append(out, finding{fmt.Sprintf("%s %s %s", rel, m, path), "method removed",
						"clients calling it get a 405"})
				}
			}
		}
	}
	return out
}

func normalizeOpenAPI(m map[string]APIDoc) {
	for k, d := range m {
		for p, methods := range d.Paths {
			sort.Strings(methods)
			d.Paths[p] = methods
		}
		m[k] = d
	}
}
