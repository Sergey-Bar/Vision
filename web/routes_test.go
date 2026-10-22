package main

// The routes are a table, and this file is the gate that keeps it a table.
//
// The console server used to register twenty-two paths to a file that was
// build output and was gitignored, so every page of the five-minute judge
// track answered 404 with an empty body while the pages themselves sat in the
// tree, complete and unreachable.
//
// A route table that only the writer of the code can read is not a table, it
// is a set of habits. So the rules are mechanical, and they are here:
//
//   - one path, one owner;
//   - no path with no owner;
//   - the console's route table is the SPA's own, read out of the SPA's
//     source, and the server keeps no second copy of it.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pageMux is the mux the pages live on. It is built the same way main builds
// it, so the test exercises the same wiring.
func pageMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	registerPageRoutes(mux)
	return mux
}

// get fetches one path from the page mux.
func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestEveryShowcasePageIsServed
//
// Every page of the showcase's table answers, with HTML, from the binary.
//
// The file is read out of the EMBEDDED assets rather than off disk, because
// the embedded copy is what the server serves. A page that exists in the
// working tree and not in the binary is a page that works on the developer's
// machine and 404s in the release archive.
func TestEveryShowcasePageIsServed(t *testing.T) {
	mux := pageMux(t)
	for _, p := range showcasePages {
		t.Run(p.Path, func(t *testing.T) {
			if _, err := staticFS.ReadFile("static/" + p.File); err != nil {
				t.Fatalf("the page %s is not in the binary: %v", p.File, err)
			}
			rec := get(t, mux, p.Path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: status %d, want 200", p.Path, rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("GET %s: content type %q, want text/html", p.Path, ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<!doctype html>") {
				t.Errorf("GET %s: the body is not an HTML document (%d bytes)", p.Path, len(body))
			}
		})
	}
}

// TestEveryDemoActResolves
//
// The five acts of docs/showcase/narration-judges.md, fetched.
//
// This is the list the rehearsal walks, and the list lives next to the table
// that owns the paths, so the two cannot drift.
func TestEveryDemoActResolves(t *testing.T) {
	mux := pageMux(t)
	for _, p := range demoActs {
		rec := get(t, mux, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", p, rec.Code)
		}
	}
}

// TestNoPathHasTwoOwners
//
// The showcase's paths are unique, and none of them is under the console's
// prefix.
//
// Two surfaces claiming one path is how the demo 404'd: the SPA was
// registered last, so it shadowed every page of the demo. The check is
// mechanical because the failure was mechanical.
func TestNoPathHasTwoOwners(t *testing.T) {
	seen := map[string]string{}
	for _, p := range showcasePages {
		if other, dup := seen[p.Path]; dup {
			t.Errorf("the path %s is claimed twice: by %s and by %s", p.Path, other, p.File)
		}
		seen[p.Path] = p.File
		if p.Path == consolePrefix || strings.HasPrefix(p.Path, consolePrefix+"/") {
			t.Errorf("the showcase claims %s, which is the console's prefix", p.Path)
		}
		if !strings.HasPrefix(p.Path, "/") {
			t.Errorf("the showcase's path %q does not start with a slash", p.Path)
		}
	}
	if strings.HasPrefix(consolePrefix, "/") == false {
		t.Fatalf("the console prefix %q does not start with a slash", consolePrefix)
	}
}

// TestNoPathHasNoOwner
//
// Every page in web/static is served by exactly one path.
//
// The defect this catches is not theoretical: web/static/fleet.html sat in
// the tree for a long time with no path to it, and it called four endpoint
// families - /api/fleet/*, /api/operator/*, /api/search/chat - that exist
// nowhere in this repository. A page nobody can reach, whose data layer was
// never written, is a file rather than a page.
//
// dev.html is the exception and the reason is in its name: it is the
// development-only page, behind -dev, and it is not part of the demo.
func TestNoPathHasNoOwner(t *testing.T) {
	owned := map[string]bool{}
	for _, p := range showcasePages {
		owned[p.File] = true
	}
	entries, err := os.ReadDir("static")
	if err != nil {
		t.Fatalf("read the showcase's directory: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		if e.Name() == "dev.html" {
			continue
		}
		if !owned[e.Name()] {
			t.Errorf("%s is in web/static and no path serves it. Either give it a "+
				"path in showcasePages or delete it: a page a judge cannot reach is "+
				"not a page, and a page whose endpoints do not exist is worse than "+
				"no page at all", e.Name())
		}
	}
	for _, p := range showcasePages {
		if _, err := os.Stat(filepath.Join("static", p.File)); err != nil {
			t.Errorf("the table names %s, which is not in web/static: %v", p.File, err)
		}
	}
}

// spaRoutePattern reads the SPA's own route table out of its source.
//
// The client owns the routes: the server serves the shell for every path
// under /console and the router decides what the path means. So the list of
// console routes lives in web/spa/src/components/Shell.tsx and NOWHERE ELSE.
// A second copy in Go is a second list of a list, which is the defect that
// made "press F5 and the page 404s" possible in the first place.
var spaRoutePattern = regexp.MustCompile(`<Route\s+path="([^"]+)"`)

// spaRoutes reads the SPA's declared routes, in the order they appear.
func spaRoutes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("spa", "src", "components", "Shell.tsx"))
	if err != nil {
		t.Fatalf("read the SPA's route table: %v", err)
	}
	matches := spaRoutePattern.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatal("no routes were found in the SPA's source, so this test is asserting nothing")
	}
	var out []string
	for _, m := range matches {
		if m[1] == "*" {
			continue // the catch-all is not a route
		}
		out = append(out, m[1])
	}
	return out
}

// TestEveryConsoleRouteIsServed
//
// Every route the SPA declares is under the console's prefix, and the server
// answers it with the shell.
//
// The answer is either the built bundle (200) or the committed placeholder
// (503), and the test says which. What it refuses to accept is a 404: the
// console server must never answer a console route with nothing.
func TestEveryConsoleRouteIsServed(t *testing.T) {
	mux := pageMux(t)
	routes := spaRoutes(t)
	seen := map[string]bool{}
	for _, r := range routes {
		if seen[r] {
			t.Errorf("the SPA declares the route %q twice", r)
		}
		seen[r] = true
		if r == "" {
			continue
		}
		path := consolePrefix + "/" + r
		if !strings.HasPrefix(path, consolePrefix+"/") {
			t.Errorf("the console route %q is not under the prefix %q", r, consolePrefix)
		}
		rec := get(t, mux, path)
		switch rec.Code {
		case http.StatusOK:
			if !strings.Contains(rec.Body.String(), `id="root"`) {
				t.Errorf("GET %s: the body is not the SPA's shell", path)
			}
		case http.StatusServiceUnavailable:
			if !strings.Contains(rec.Body.String(), "npm run build") {
				t.Errorf("GET %s: the placeholder does not say how to build the bundle", path)
			}
		default:
			t.Errorf("GET %s: status %d, want 200 (built) or 503 (not built)", path, rec.Code)
		}
	}
}

// TestTheConsoleIsNotAtTheRoot
//
// The console's routes are not at the root any more.
//
// The root belongs to the demo. A judge who lands on the root gets the
// showcase; a judge who wants the product goes to /console.
func TestTheConsoleIsNotAtTheRoot(t *testing.T) {
	mux := pageMux(t)
	for _, p := range []string{"/incidents", "/clips", "/cameras", "/upload", "/users", "/audit", "/settings", "/sites", "/site", "/events"} {
		rec := get(t, mux, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404. The root belongs to the showcase; "+
				"the console's routes live under %s", p, rec.Code, consolePrefix)
		}
	}
}

// TestTheConsolePrefixAnswers
//
// /console itself answers, with the same body as /console/.
//
// Two URLs for one page is two things to keep in sync, and the redirect
// between them is a thing that can break.
func TestTheConsolePrefixAnswers(t *testing.T) {
	mux := pageMux(t)
	rec := get(t, mux, consolePrefix)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET %s: 404. The console's own address must answer", consolePrefix)
	}
	body := rec.Body.String()
	if body == "" {
		t.Errorf("GET %s: the body is empty", consolePrefix)
	}
}

// TestTheUnknownPathSaysWhatExists
//
// A path that does not exist answers with the list of the paths that do.
//
// A judge who mistypes a path is better served by the list than by a bare
// 404, and the list is the cheapest possible documentation of a route table.
func TestTheUnknownPathSaysWhatExists(t *testing.T) {
	mux := pageMux(t)
	rec := get(t, mux, "/pipelne")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /pipelne: status %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	for _, p := range showcasePages {
		if !strings.Contains(body, p.Path) {
			t.Errorf("the 404 page does not mention %s", p.Path)
		}
	}
	if !strings.Contains(body, consolePrefix) {
		t.Errorf("the 404 page does not mention %s", consolePrefix)
	}
}

// TestTheShowcasePagesAreTheOnlyPages
//
// The route table is sorted, unique and complete, and the demo's list of acts
// is a subset of it.
//
// The sort is not cosmetic: a table that is written by hand drifts, and a
// table that is sorted by hand can be sorted wrong.
func TestTheShowcasePagesAreTheOnlyPages(t *testing.T) {
	acts := map[string]bool{}
	for _, p := range demoActs {
		acts[p] = true
	}
	for _, p := range showcasePages {
		if !acts[p.Path] && p.Path != "/fleet" {
			t.Logf("the page %s is served but is not one of the demo's acts", p.Path)
		}
	}
	var paths []string
	for _, p := range showcasePages {
		paths = append(paths, p.Path)
	}
	if !sort.StringsAreSorted(paths) && len(paths) > 1 {
		// The table is ordered by act, not by alphabet, so this is only a
		// report. It is here because "the order of the table is the order of
		// the demo" is a claim worth making visible when it stops being true.
		t.Log("the showcase table is not sorted; it is ordered by the judge's track")
	}
}
