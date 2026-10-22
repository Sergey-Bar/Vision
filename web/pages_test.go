package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// consolePageFiles lists the console's own screens.
//
// The glob is `spa/src/pages/*.tsx`, and the `.tsx` suffix matches the TEST
// files too: a `Audit.test.tsx` beside an `Audit.tsx` is a test for a page,
// not a page. Reading one as a page is how a screen's own heading count gets
// attributed to a test file, and the gate then fails on a page that is fine.
//
// So the test files are filtered out here, once, and every gate that walks the
// console's screens uses this list. A second glob in another file would be a
// second place to forget, and the point of the list is that there is one.
func consolePageFiles(t *testing.T) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join("spa", "src", "pages", "*.tsx"))
	if err != nil {
		t.Fatalf("glob the console's pages: %v", err)
	}
	var out []string
	for _, path := range entries {
		base := filepath.Base(path)
		if strings.HasSuffix(base, ".test.tsx") || strings.HasSuffix(base, ".spec.tsx") {
			continue
		}
		out = append(out, path)
	}
	if len(out) == 0 {
		t.Fatalf("found no console pages under spa/src/pages. A gate that reads nothing " +
			"proves nothing, and this one would pass on an empty tree")
	}
	return out
}

// consolePageSource reads one page's source.
func consolePageSource(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
