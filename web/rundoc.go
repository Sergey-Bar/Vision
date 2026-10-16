package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// rundocDoc is one document this repository's own commands wrote, served by name.
type rundocDoc struct {
	rel         string
	contentType string
	// writtenBy names the command that produces the file, so a missing document
	// answers with what would create it rather than with a bare 404.
	writtenBy string
}

// rundocDocs is the WHOLE input surface of /rundoc/.
//
// The request selects a KEY from this map and nothing else. There is no path
// parameter, no cleaning and no join with the request: a name that is not one
// of these keys is a 404 before any file is touched, so `..`, an absolute path
// or a NUL byte cannot reach the filesystem by construction rather than by a
// check whose removal would be the next defect. What is served is a run report
// this repository's own tools wrote into dist/, which the pages link to so a
// reader can see the artefact whose numbers they are reading.
var rundocDocs = map[string]rundocDoc{
	"poc-report": {
		rel:         "dist/demo/report.html",
		contentType: "text/html; charset=utf-8",
		writtenBy:   "`pocctl demo`",
	},
	"poc-dashboard": {
		rel:         "dist/demo/dashboard.html",
		contentType: "text/html; charset=utf-8",
		writtenBy:   "`pocctl demo`",
	},
	"live-report": {
		rel:         "dist/live-demo/report.html",
		contentType: "text/html; charset=utf-8",
		writtenBy:   "`scripts/live-demo.ps1`",
	},
	"live-transcript": {
		rel:         "dist/live-demo/transcript.txt",
		contentType: "text/plain; charset=utf-8",
		writtenBy:   "`scripts/live-demo.ps1`",
	},
}

// rundocPath resolves a served name to its file. The name indexes the allowlist
// and is never used as a path segment: this function is the one line a red-check
// removes, and with it gone the request name reaches the filesystem directly.
func rundocPath(repoRoot, name string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(rundocDocs[name].rel))
}

// rundocNames lists the served names, sorted, for a refusal that says what to
// ask for instead.
func rundocNames() string {
	names := make([]string, 0, len(rundocDocs))
	for name := range rundocDocs {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func registerRundocRoutes(repoRoot string) {
	http.HandleFunc("/rundoc/", handleRundoc(repoRoot))
}

func handleRundoc(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "rundoc: these documents are read-only", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/rundoc/")
		if _, ok := rundocDocs[name]; !ok {
			http.Error(w, fmt.Sprintf(
				"rundoc: %q is not one of the documents this page serves (%s). There is no path to guess: the name is the whole request.",
				name, rundocNames()), http.StatusNotFound)
			return
		}
		path := rundocPath(repoRoot, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, fmt.Sprintf(
				"rundoc: %s is not on this machine yet, so there is nothing to show. It is written by %s.",
				rundocDocs[name].rel, rundocDocs[name].writtenBy), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", rundocDocs[name].contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	}
}
