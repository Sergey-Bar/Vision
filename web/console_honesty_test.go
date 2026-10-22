package main

// The console must not invent data.
//
// This file is a gate, not a test of behaviour: it reads the console's own
// source and fails when it finds a record the product did not produce.
//
// The console used to carry three kinds of invented data, and all three were
// invisible on the screen because they looked exactly like the real thing:
//
//   - a table of eight cameras at three sites, served by the console when the
//     endpoint was unreachable. An operator could not tell it from the
//     inventory, and an operator is the person who is supposed to be able to.
//   - a camera, a clip and an incident per video the BROWSER remembered, kept
//     in localStorage. The clip's observation time was the moment the page was
//     opened and its size was zero. The incident's severity came out of a
//     round-robin over a list.
//   - a list the console served under the product's name, so the product's own
//     API answered with three sites nobody has ever been to.
//
// A console that keeps its own records beside the product's is a console with
// two truths, and the one in the browser is the one a reader cannot check.
//
// So the rule is: the console's front end holds NO state of its own, and
// carries NO inventory of its own. Everything on the screen is something the
// product or the device said.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// spaSourceFiles are the files of the console's front end.
func spaSourceFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	root := filepath.Join("spa", "src")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx":
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the SPA's source: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no SPA source files were found, so this test is asserting nothing")
	}
	return out
}

// consoleSourceFiles are the console server's own Go files.
//
// This file is excluded, and the exclusion is not a convenience: it NAMES the
// invented sites, and a gate that fails on its own vocabulary is a gate that
// cannot be read.
func consoleSourceFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the console's directory: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if e.Name() == "console_honesty_test.go" {
			continue
		}
		out = append(out, e.Name())
	}
	if len(out) == 0 {
		t.Fatal("no console source files were found, so this test is asserting nothing")
	}
	return out
}

// TestTheConsoleFrontEndHoldsNoStateOfItsOwn
//
// The console's front end must not persist anything in the browser.
//
// The rule is not "the front end may not use localStorage" because that would
// be a rule about a mechanism. It is "the front end keeps no records of its
// own": a camera list, a clip list and an incident list in the browser are
// three inventories the product does not have, and a reader cannot check any
// of them.
//
// The check is on the CALL, not on the word, because a comment explaining
// that the browser used to keep records is exactly the kind of sentence this
// file's own rule is about.
func TestTheConsoleFrontEndHoldsNoStateOfItsOwn(t *testing.T) {
	forbidden := []string{
		"localStorage.",
		"sessionStorage.",
		"indexedDB.",
	}
	for _, path := range spaSourceFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Errorf("%s uses %s. The console's front end keeps no records of its "+
					"own: a camera, a clip or an incident that lives in the browser is a "+
					"record the product did not produce, and a reader cannot check it",
					path, needle)
			}
		}
	}
}

// TestTheConsoleInventsNoCamerasOrSites
//
// The console must carry no inventory of its own.
//
// The invented sites are named here so that the names are not free to come
// back: a table of cameras at "North Campus" was read as a real inventory
// because it had the SHAPE of one, and the shape is the only thing a reader
// sees.
func TestTheConsoleInventsNoCamerasOrSites(t *testing.T) {
	forbidden := []string{
		"North Campus",
		"West Warehouse",
		"Downtown Hub",
		"DEMO_CAMERAS",
		"demoCameras",
	}
	for _, path := range append(spaSourceFiles(t), consoleSourceFiles(t)...) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Errorf("%s carries %q. The console's camera list is the product's and "+
					"the device's; a list the console made up is a list that looks exactly "+
					"like the real one", path, needle)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The showcase, which the two gates above never reached.
//
// The gates above scan web/spa/src. The demo lives in web/static, and it was
// where the invented numbers were: a top bar reading CPU 34% and Memory 67%,
// a sidebar listing six cameras that do not exist, and a count of twelve.
// Nothing in the console's front end was affected, and the gate that was
// supposed to catch it never looked here.
// ---------------------------------------------------------------------------

// showcaseSourceFiles lists the showcase's own pages and scripts.
func showcaseSourceFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pattern := range []string{"static/*.html", "static/*.js"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		out = append(out, matches...)
	}
	if len(out) == 0 {
		t.Fatal("no showcase files were found, so this test is asserting nothing")
	}
	return out
}

// TestTheShowcaseInventsNoNumbers
//
// The showcase's pages must not carry a number of their own.
//
// The number must come from a file the machine wrote, and the page must say
// which file. A literal percentage in a showcase page is a measurement that
// was typed in, and the demo's whole claim is that it does not do that.
//
// The rule is on the LITERAL, not on the meaning: "the recorded gate ran 33
// gates" is a number too, and it is fine when it is written by the script
// from the record. What is not fine is a number baked into the markup.
func TestTheShowcaseInventsNoNumbers(t *testing.T) {
	// A literal percentage in the markup: "34%", "67%".
	percentage := regexp.MustCompile(`>\s*\d{1,3}\s*%\s*<`)
	// A progress bar filled to a fixed width: style="width: 34%".
	barWidth := regexp.MustCompile(`style="[^"]*width:\s*\d+(\.\d+)?%`)
	// A count badge with a number baked into it.
	countBadge := regexp.MustCompile(`class="count">\s*\d+`)

	for _, path := range showcaseSourceFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		if m := percentage.FindString(body); m != "" {
			t.Errorf("%s carries the literal %q. A number in the markup is a "+
				"measurement nobody took: read it from the file that holds it, and "+
				"say which file", path, strings.TrimSpace(m))
		}
		if m := barWidth.FindString(body); m != "" {
			t.Errorf("%s carries the literal %q. A bar filled to a fixed width is "+
				"the same claim in a different shape", path, m)
		}
		if m := countBadge.FindString(body); m != "" {
			t.Errorf("%s carries the literal %q. A count in the markup is an "+
				"inventory nobody produced", path, m)
		}
	}
}

// TestTheShowcaseReachesNothingOutsideThisMachine
//
// No page of the showcase may reference a host other than this one.
//
// The demo ran at a venue with no network, and the pages fetched their
// typefaces from Google Fonts, which is a page about a signed record making
// an outbound request before it had said anything. The fonts are now served
// from web/static/fonts.
//
// The check is on the REFERENCE, not on the request: a page that mentions a
// URL in prose is fine, and a page whose markup points at another origin is
// not.
func TestTheShowcaseReachesNothingOutsideThisMachine(t *testing.T) {
	external := regexp.MustCompile(`(?i)(src|href)\s*=\s*["']https?://[^"']+["']`)
	for _, path := range showcaseSourceFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range external.FindAllString(string(raw), -1) {
			t.Errorf("%s points at %s. The demo must not need the network: a page "+
				"that fetches anything is a page that renders differently on the "+
				"judge's machine than on this one", path, m)
		}
	}
}

// TestEveryClassOnAShowcasePageIsDefined
//
// Every class a showcase page uses must be defined by a stylesheet, and every
// class a stylesheet defines must be used by a page.
//
// The first half is the defect this file found: nine of the eleven pages used
// .nav, .wrap, .card, .tbl, .page-head, .foot and .bottom-tabs, and NO
// stylesheet in the repository defined any of them. Every one of those pages
// rendered as an unstyled document - the census, the page whose grades are
// the whole argument, as a wall of Times New Roman.
//
// The second half is the other direction, and it is what keeps the file from
// growing: a class nobody uses is a rule nobody reads.
//
// The classes a script writes at run time are read out of the scripts
// themselves, so a class invented in JavaScript counts as used and a class
// invented in JavaScript with no rule is caught like any other.
func TestEveryClassOnAShowcasePageIsDefined(t *testing.T) {
	defined := map[string]string{}
	cssFiles, err := filepath.Glob("static/*.css")
	if err != nil {
		t.Fatalf("glob the stylesheets: %v", err)
	}
	if len(cssFiles) == 0 {
		t.Fatal("no stylesheets were found, so this test is asserting nothing")
	}
	selector := regexp.MustCompile(`\.([a-zA-Z][a-zA-Z0-9_-]*)`)
	for _, path := range cssFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range selector.FindAllStringSubmatch(string(raw), -1) {
			if _, seen := defined[m[1]]; !seen {
				defined[m[1]] = path
			}
		}
	}

	used := map[string]string{}
	classAttr := regexp.MustCompile(`class="([^"$<]*)"`)
	elCall := regexp.MustCompile(`(?:el|createElement)\(\s*"[a-z]+"\s*,\s*"([^"]+)"`)
	for _, path := range showcaseSourceFiles(t) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(raw)
		for _, re := range []*regexp.Regexp{classAttr, elCall} {
			for _, m := range re.FindAllStringSubmatch(body, -1) {
				for _, c := range strings.Fields(m[1]) {
					if _, seen := used[c]; !seen {
						used[c] = path
					}
				}
			}
		}
	}

	for c, path := range used {
		if _, ok := defined[c]; !ok {
			t.Errorf("%s uses the class %q and no stylesheet defines it. The page "+
				"renders unstyled, which is how the census came to be a wall of "+
				"Times New Roman with the grades still in it", path, c)
		}
	}

	// The other direction, reported as one line per stylesheet rather than one
	// per class: a rule nobody uses is debt, and the debt is worth seeing, but
	// forty lines of it drown the failures.
	unused := map[string][]string{}
	for c, path := range defined {
		if _, ok := used[c]; !ok {
			unused[path] = append(unused[path], c)
		}
	}
	for path, classes := range unused {
		sort.Strings(classes)
		t.Logf("%s defines %d class(es) no page uses: %s", path, len(classes), strings.Join(classes, " "))
	}
}
