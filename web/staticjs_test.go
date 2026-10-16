package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoScriptPassesAnElementAsText: every page's el() helper sets textContent,
// so String(element) is "[object HTMLElement]". A call shaped
// el("td", null, el("code", null, x)) therefore prints that literal into the
// cell - which the gate column on /trust and the proof and artefact columns on
// /census did, visible only when a person looked at the page. The text argument
// is a string; an element goes in through appendChild.
func TestNoScriptPassesAnElementAsText(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("static", "*.js"))
	if err != nil {
		t.Fatalf("glob static/*.js: %v", err)
	}
	if len(files) < 7 {
		t.Fatalf("found %d scripts to scan, and the pages carry more than that: a scan that reads nothing proves nothing", len(files))
	}
	elementAsText := regexp.MustCompile(`el\("[^"]*",\s*[^,)]+,\s*el\(`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if elementAsText.MatchString(line) {
				t.Errorf("%s:%d: %s\n  el() sets textContent, so an element passed as its text argument renders as [object HTMLElement]; append the element to the cell instead",
					filepath.ToSlash(f), i+1, strings.TrimSpace(line))
			}
		}
	}
}
