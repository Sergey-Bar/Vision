package milestone_test

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The neutrality invariant, locked with a test.
//
// The plan's competitive claim number 6 is "it is not a Milestone product - the
// new code names no vendor, and a test enforces it." A claim in a slide is worth
// nothing; a claim in a test fails the build. This is that test, and it is the
// cheapest of the three provider-abstraction ideas in the plan: thirty lines
// against a whole capability interface nobody has to maintain.
//
// Two halves, because they are two different promises:
//
//   - **No package outside internal/milestone imports it.** The vendor surface is
//     three small methods; everything else in the product reaches the vendor
//     through them, so a package that imports it directly has skipped the seam and
//     the seam is not holding.
//   - **The new capture and AI packages name no vendor.** internal/rtsp,
//     internal/mp4, internal/detector and internal/hafnia are the code this work
//     adds, and none of them may mention the system of record. A muxer that
//     knows about a VMS is a muxer that cannot be pointed at the next one.

// modulePath is read from go.mod rather than hardcoded, so a module rename does
// not turn this test into a test that passes because everything is external.
func modulePath(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// packages lists every package in the module, by import path.
func packages(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var out []string
	// One entry per directory, not per file: the walk visits every .go in a
	// package and a package that has four of them is still one package.
	seen := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kilo", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		d := filepath.ToSlash(rel)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the tree: %v", err)
	}
	return out
}

// importsOf is the set of import paths a package's non-test files name.
//
// It uses go/build's importer rather than a text search so that a vendor name
// inside a comment or a string does not trip it, and so a dot-import or an alias
// is still seen.
func importsOf(t *testing.T, dir string) map[string]bool {
	t.Helper()
	pkg, err := build.ImportDir(dir, build.ImportComment)
	if err != nil {
		// A directory with only test files, or a package that does not compile on
		// its own. Neither is a neutrality violation, and the build gate covers
		// the second.
		return map[string]bool{}
	}
	out := map[string]bool{}
	for _, i := range pkg.Imports {
		out[i] = true
	}
	for _, s := range pkg.TestImports {
		out[s] = true
	}
	for _, s := range pkg.XTestImports {
		out[s] = true
	}
	return out
}

// compositionRoot are the places allowed to name the vendor.
//
// The plan's phrasing is "no package outside internal/milestone imports it", and
// taken literally that is wrong in a way that would either be ignored or would
// force the wiring to be impossible: the binaries have to construct the adapter
// and the pipeline has to be handed one, and *that* is where the vendor is
// supposed to be named. The invariant is about the code that has no business
// knowing - the capture path, the muxer, the detector, the AI clients, the rules,
// the evidence store. So the exemption is named rather than implied, and adding a
// fourth caller is a deliberate edit to a list in a test.
//
// `cmd/security-seed` is the fourth caller and it is a caller for the same reason
// `cmd/security-core` is: it is a composition root. The seed builds an adapter
// over `milestone.ReplayTransport` and hands it to `pipeline.New`, which is the
// identical act the core performs against a live VMS. What differs is where the
// transport's bytes come from — the committed fixtures rather than a network — and
// that is a property of the transport, not a reason for the seed to reach past the
// adapter. If the seed stopped constructing the adapter it could not drive the
// product's own pipeline, and the seeded site would stop being the product's.
var compositionRoot = map[string]bool{
	"cmd/security-core":      true,
	"cmd/securityctl":        true,
	"cmd/security-seed":      true,
	"internal/pipeline":      true,
	"internal/milestone":     true,
	"internal/evidencefetch": true,
}

func TestOnlyTheCompositionRootImportsTheVendorPackage(t *testing.T) {
	mod := modulePath(t)
	vendorPath := mod + "/internal/milestone"

	var offenders []string
	for _, dir := range packages(t) {
		if compositionRoot[dir] {
			continue
		}
		for imp := range importsOf(t, filepath.Join("..", "..", filepath.FromSlash(dir))) {
			if imp == vendorPath {
				offenders = append(offenders, dir)
				break
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("packages outside the composition root import the vendor: %v\n"+
			"Reach the vendor through its three-method surface. A direct import is a package "+
			"that skipped the seam, and the seam is what makes the next vendor cheap.\n"+
			"If one of these genuinely has to wire the vendor in, add it to compositionRoot "+
			"above with the reason - do not delete this test.",
			offenders)
	}
}

// newPackages are the packages this work adds. The list is spelled out rather
// than derived, because a derived list would silently stop checking a package
// that was renamed, and the point of the test is that it cannot be forgotten.
var newPackages = []string{
	"rtsp",
	"mp4",
	"detector",
	"hafnia",
}

// vendorWords are the substrings a vendor-neutral package must not contain.
//
// They are matched against the *source text*, not identifiers, so a comment
// explaining why a design is vendor-neutral is allowed to say the name - which is
// not the same as the code depending on it. A false positive here costs a
// sentence; a false negative costs the claim.
var vendorWords = []string{
	"milestone", "xprotect", "smart client", "imageserver", "somfy", "axis",
	"hikvision", "dahua", "genetec", "avigilon", "uniview", "bosch",
}

func TestTheNewPackagesNameNoVendor(t *testing.T) {
	for _, dir := range newPackages {
		files, err := filepath.Glob(filepath.Join("..", dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("%s has no source; the list of new packages is stale", dir)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			lower := strings.ToLower(string(raw))
			for _, w := range vendorWords {
				if !strings.Contains(lower, w) {
					continue
				}
				// Allowed in a comment: the whole point of several of these
				// files is to say why they are vendor-neutral, and a test that
				// forbade the explanation would have them say it in less useful
				// words.
				if inCommentOnly(lower, w) {
					continue
				}
				t.Errorf("%s mentions %q outside a comment.\n"+
					"This package is one of the four the plan says name no vendor. A muxer "+
					"that knows about a video system is a muxer that cannot be pointed at the next one.",
					f, w)
			}
		}
	}
}

// inCommentOnly reports whether every occurrence of a word in a source file is
// inside a comment or a doc comment.
func inCommentOnly(lower string, word string) bool {
	stripped := stripComments(lower)
	return !strings.Contains(stripped, word)
}

// stripComments removes line and block comments, leaving code and string
// literals. String literals are kept on purpose: a vendor name in a string is
// usually a URL, a header or a route, and those are exactly what must not be
// here.
func stripComments(s string) string {
	var b strings.Builder
	lines := strings.Split(s, "\n")
	inBlock := false
	for _, line := range lines {
		i := 0
		for i < len(line) {
			if inBlock {
				if j := strings.Index(line[i:], "*/"); j >= 0 {
					inBlock = false
					i += j + 2
					continue
				}
				break
			}
			if strings.HasPrefix(line[i:], "//") {
				break
			}
			if strings.HasPrefix(line[i:], "/*") {
				inBlock = true
				i += 2
				continue
			}
			b.WriteByte(line[i])
			i++
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestTheVendorSurfaceIsSmall is the other half of "three small methods".
//
// A seam that has grown to forty methods is not a seam, and the growth is the
// predictable failure: one more field per vendor quirk, and the next vendor's
// quirks do not fit. Asserting the number is cheap, and it is the only thing that
// makes "small" a property rather than an adjective.
//
// The plan allows three additions to this interface - a bookmark write-back and a
// clock probe - so the ceiling is what exists plus those, with a stated reason
// for the room.
func TestTheVendorSurfaceIsSmall(t *testing.T) {
	methods := transportMethodCount(t)
	const ceiling = 5
	if methods > ceiling {
		t.Errorf("the vendor surface has %d methods; the ceiling is %d.\n"+
			"Every extra method is one more thing the next vendor must implement, and the point "+
			"of this seam is that a second vendor costs three methods rather than a rewrite.",
			methods, ceiling)
	}
}

// transportMethodCount counts the methods declared on the Transport interface by
// parsing the declaration, so a comment above a method is not counted and a
// signature inside a comment is not counted either.
func transportMethodCount(t *testing.T) int {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		_, body, ok := strings.Cut(string(raw), "type Transport interface {")
		if !ok {
			continue
		}
		decl, _, ok := strings.Cut(body, "\n}")
		if !ok {
			t.Fatalf("the Transport interface in %s does not close on its own line", f)
		}
		n := 0
		for _, line := range strings.Split(stripComments(strings.ToLower(decl)), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "(") && strings.HasSuffix(line, ")") {
				n++
			}
		}
		if n == 0 {
			t.Fatalf("the Transport interface in %s counted as having no methods; the parser is wrong, "+
				"and a test that silently counts zero is worse than no test", f)
		}
		return n
	}
	t.Fatal("no declaration of Transport interface found")
	return 0
}
