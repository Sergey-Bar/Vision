// Package deadexport is the gate for the "exported symbol with no reader" class.
//
// # Why this exists
//
// Nine of the runnable-core defects found in one round were the same shape. Not a
// bug in the code — the code was correct, and in five cases the function did
// exactly what its name said. The defect was that the symbol was never called:
//
//	RawRecord.Envelope        checkpoint.SealRange        identity.SecretStore
//	api.Server.start          slo.Breaches                 Observability.NewCounters
//	evidence.Options.VerifyHashOnRead                        api.Server.start
//
// In every case the author believed the thing worked, because writing it is what
// you remember. Nothing in `go build`, `go vet` or `go test` can tell you that.
//
// Two of them were worse than a dead symbol. `slo.Breaches` was an SLO evaluator
// that no SLO ran, so the SLOs were a document. `evidence.Options.VerifyHashOnRead`
// was an evidence-integrity switch that nothing ever turned on, so the evidence
// hash was written and never re-read.
//
// # What "a reader" means here
//
// A reader is an identifier *use* in a non-test `.go` file outside the package
// that declares the symbol. Four things that look like readers are not:
//
//   - The declaration itself. `func Foo()` mentions `Foo` in its own file, and a
//     recursive call inside its own body is still its own file.
//   - A test in the same package. A test proves a symbol behaves as documented; it
//     does not prove anything calls it. `slo.Breaches` had a test.
//   - A doc comment. A godoc reference is a promise to a caller who is not there.
//   - A use inside the same package's other files. An exported symbol in the same
//     package is dead to the rest of the tree even if its own package calls it,
//     because nothing outside can.
//
// And two things that look like non-readers are readers, which is why this parses
// rather than greps:
//
//   - A use in a `_test.go` file whose *package name* ends in `_test`. That is a
//     black-box suite, it is a real consumer, and half of `internal/testutil` has
//     no other one. The distinction is the package name, not the directory,
//     because both kinds of test file sit in the same directory.
//   - A use in a file behind a `//go:build` tag. A build-tagged file is a
//     producer, and a text search that ignored tags would either miss it or miss
//     something else.
//
// # Methods are gated differently, and differently for a reason
//
// A method is not reachable by name from another package; it is reached through
// its receiver, and the receiver's type is frequently an interface the caller
// holds. So the "read by another package" rule does not apply to methods at all:
// an interface parameter satisfied structurally is a healthy design, not a defect.
//
// What *is* a defect is a method nobody calls anywhere — in any package, through
// any interface. `observability.Breaches` is that: the SLO evaluator, with a
// test, that no gauge loop and no route ever invoked, so the SLOs were a
// document. So methods are gated on a weaker, tree-wide condition: **the method's
// name never appears as a selector anywhere in the tree**. `x.Foo()` and
// `iface.(interface{ Foo() })` are the only ways to reach a method, so a name
// that appears in neither position is a name nobody can be calling.
//
// The cost is stated rather than hidden: a method whose name collides with a
// common verb somewhere else in the tree is excused. That is the one class this
// gate misses, and missing a dead method is a smaller cost than drowning the
// gate in interface conformance.
//
// # Four things deliberately not gated, each with a reason rather than with a hole
//
//   - **Type declarations.** A healthy exported type is usually never named
//     outside its own package: a consumer satisfies an interface structurally, or
//     writes `x, err := f()` and never mentions the type `f` returns. "No
//     cross-package mention" is the normal state of a working exported type, not a
//     symptom of a dead one. None of the nine defects was a type.
//   - **Struct fields.** A DTO's fields are read by an encoder, and a config's
//     fields are set by a constructor, so "no cross-package mention" is again the
//     normal state. `evidence.Options.VerifyHashOnRead` is the field that made this
//     class worth revisiting, and it is caught today by `TestVerifyHashOnReadIsWired`
//     in `internal/evidence` rather than by a name-collision-prone static rule.
//   - **`package main`.** It cannot be imported, so the question has no possible
//     answer; the count of skipped files is in the summary rather than hidden.
//   - **Unexported symbols.** A file-local function that nothing calls is ordinary
//     code in progress.
//
// # The allow-list
//
// A rule that cannot be excepted is a rule somebody turns off. Every entry carries
// a written reason, an entry with no reason is rejected rather than honoured, and
// `PrintedAllowList` returns the whole list so the gate prints it on every run. A
// gate whose exemptions are visible only on the day they are used is a gate
// nobody audits in advance, and that is where dead entries accumulate.
package deadexport

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding is one exported symbol with no reader.
type Finding struct {
	// File and Line locate the declaration.
	File string
	Line int
	// Package is the declaring package's directory, slash-separated and relative
	// to the module root, e.g. `internal/slo`.
	Package string
	// Symbol is the bare identifier.
	Symbol string
	// Kind is `func`, `type`, `const` or `var`.
	Kind string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s.%s is exported and nothing outside its own package reads it (%s)",
		f.File, f.Line, f.Package, f.Symbol, f.Kind)
}

// Exemption is one allow-list entry, and the reason is mandatory.
//
// Exactly one of Symbol or Prefix is set. A prefix entry covers a named family
// inside one package — `internal/rules.Kind` for the rule-kind vocabulary — and it
// is a prefix rather than a pattern on purpose: a regular expression in an
// exemption is a rule somebody widens by accident, and a family that has grown a
// member nobody checked is exactly the "table seeded with plausible entries nobody
// ran" failure this repository has already paid for once.
type Exemption struct {
	// Symbol is `pkgdir.Symbol`, e.g. `internal/slo.Breaches`.
	Symbol string
	// Prefix is `pkgdir.NamePrefix`, e.g. `internal/rules.Kind`. It matches
	// `pkgdir` plus every exported symbol beginning with `NamePrefix`.
	Prefix string
	// Why must say why the symbols are not dead. An empty reason is a refusal, not
	// an excuse.
	Why string
}

// key is the map key an entry is stored under, which is the prefix for a family
// entry and the exact symbol otherwise.
func (e Exemption) key() string {
	if e.Prefix != "" {
		return e.Prefix
	}
	return e.Symbol
}

// matches reports whether an entry covers a `pkgdir.Symbol` pair.
func (e Exemption) matches(dir, name string) bool {
	if e.Prefix == "" {
		return e.Symbol == dir+"."+name
	}
	dot := strings.LastIndex(e.Prefix, ".")
	if dot < 0 {
		return false
	}
	return e.Prefix[:dot] == dir && strings.HasPrefix(name, e.Prefix[dot+1:])
}

// Repo is a parsed tree: what each package declares, and which packages read what.
type Repo struct {
	// decls maps "pkgkey.Symbol" to its declaration site, where pkgkey is the
	// declaring file's package key.
	decls map[string]declSite
	// reads maps a package key to the identifiers its files use. Keyed by package
	// rather than by directory, because "outside its own package's tests" is a
	// statement about packages: a `_test`-suffixed external test package is a
	// different package and a real consumer, while an in-package test file is the
	// same package and is not.
	reads map[string]map[string]bool
	// qualified maps a reading package to the *qualified* names it mentions, as
	// `qualifier.Selector`.
	//
	// It is separate from `reads` because a bare selector name cannot tell two
	// packages' symbols apart: `covercheck.Derive` in a test file made
	// `statuscheck.Derive` look read, because the name matched. The qualifier is
	// what makes "somebody outside this package named it" true rather than
	// "somebody named a symbol with the same spelling somewhere", and the gate
	// found its own false negative through it.
	//
	// A selector's qualifier is a package name only when it matches a package this
	// module declares — `repo.FilesParsed()` and `s.Breaches()` are the same
	// syntax as `sort.Slice()` and must not count as package reads. See
	// `packageNames`.
	qualified map[string]map[string]bool
	// packageNames is every package name declared under the scanned trees, which is
	// what tells a package qualifier from a variable.
	packageNames map[string]bool
	// methods maps a method's bare name to whether it appears as a selector
	// anywhere in the tree. A method is only ever reached through a receiver, so
	// this is the whole question for a method, and it is asked tree-wide rather
	// than per package for the reason in the package comment.
	methods map[string]bool
	// files is the number of hand-written Go files parsed, reported by Summary so
	// a reader can see the gate looked at the tree.
	files int
	// generated is the number of files skipped for carrying the generated marker.
	generated int
	// mains is the number of `package main` files skipped, which the summary
	// reports rather than passing over in silence.
	mains int
	// errs are parse failures. They are returned rather than swallowed: a gate
	// that cannot read a file must say so, not pass it.
	errs []error
}

// generatedMarker is the convention every Go generator writes, and the reason for
// `https://golang.org/s/generatedcode`: the first line of the file, before the
// package clause, so a reader sees it before anything else.
const generatedMarker = "Code generated by"

// isGenerated reports whether a file's leading comment marks it as generated.
//
// The check is a prefix match on the first lines rather than a substring search
// across the whole file, because a hand-written file that mentions the phrase in
// a comment must not be excluded from the gate. The rules: the marker appears in
// a `//` comment before the `package` clause, and the line ends in
// `DO NOT EDIT.`.
func isGenerated(src []byte) bool {
	head := src
	if len(head) > 4096 {
		head = head[:4096]
	}
	for _, line := range strings.Split(string(head), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "package ") {
			return false
		}
		// Only comments can carry the marker, and a build constraint is a comment
		// that is not it — `//go:build` and `// +build` sit above it in some
		// generators, which is why the loop keeps going rather than stopping at the
		// first comment.
		if !strings.HasPrefix(trimmed, "//") {
			continue
		}
		c := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
		if !strings.HasPrefix(c, generatedMarker) {
			continue
		}
		return strings.Contains(c, "DO NOT EDIT.")
	}
	return false
}

type declSite struct {
	file string
	line int
	kind string
}

// loadDirs are the trees the gate covers. `poc` is a second Go module and is
// pruned by the walk's skip set, not special-cased here.
var loadDirs = []string{"internal", "cmd", "tools"}

// defaultSkip are the directory names pruned anywhere in the walk. `.kilo` holds
// other checkouts of this repository, so a match in it is not a match in the tree
// being gated.
var defaultSkip = map[string]bool{
	".git": true, ".kilo": true, "poc": true, "node_modules": true, "__pycache__": true,
	"dist": true,
}

// Load parses every Go file under root for each directory in LoadDirs.
func Load(root string) *Repo { return LoadDir(root, loadDirs) }

// LoadDir is Load over an explicit list of trees, which is what the gate's own
// tests use to point it at a fixture rather than at the live repository.
//
// A tree that does not exist is an error rather than a silent zero. A gate that
// quietly covers less than it claims is the defect this whole repository keeps
// finding, and it is the defect this gate exists to catch.
func LoadDir(root string, dirs []string) *Repo {
	r := &Repo{
		decls:        map[string]declSite{},
		reads:        map[string]map[string]bool{},
		qualified:    map[string]map[string]bool{},
		packageNames: map[string]bool{},
		methods:      map[string]bool{},
	}
	dirOf := func(path string) string {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return filepath.ToSlash(filepath.Dir(path))
		}
		return filepath.ToSlash(filepath.Dir(rel))
	}

	// Two passes, because deciding whether `x.Y` names a package's symbol needs to
	// know every package name in the tree, and a single pass has only seen the
	// files before the one being read. The first pass parses each file's package
	// clause and nothing else; it is cheap and it makes the second pass's qualifier
	// test sound.
	var files []string
	for _, d := range dirs {
		base := filepath.Join(root, d)
		if _, err := os.Stat(base); err != nil {
			r.errs = append(r.errs, fmt.Errorf("gate tree %s does not exist under %s", d, root))
			continue
		}
		walkErr := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if defaultSkip[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if walkErr != nil {
			r.errs = append(r.errs, walkErr)
		}
	}

	for _, path := range files {
		r.notePackageName(path)
	}
	for _, path := range files {
		r.loadFile(root, path, dirOf(path))
	}
	return r
}

// notePackageName records the package a file declares, reading only as far as the
// package clause.
func (r *Repo) notePackageName(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("read %s: %w", path, err))
		return
	}
	if isGenerated(src) {
		return
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.PackageClauseOnly)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("parse %s: %w", path, err))
		return
	}
	r.packageNames[f.Name.Name] = true
}

func (r *Repo) loadFile(root, path, dir string) {
	head, err := os.ReadFile(path)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("read %s: %w", path, err))
		return
	}
	// Generated code is skipped, and by the standard marker rather than by a
	// path. `internal/contracts/securityv1` alone declares several hundred
	// exported symbols protoc emitted, none of which anybody names by hand, and a
	// gate that reports them is a gate whose output nobody reads. The marker is
	// the convention `go generate` tooling already sets, so a file that starts
	// being hand-edited stops carrying it and comes back under the gate — which
	// is the correct failure direction.
	if isGenerated(head) {
		r.generated++
		return
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, head, 0)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("parse %s: %w", path, err))
		return
	}
	r.files++
	rel, relErr := filepath.Rel(root, path)
	relPath := filepath.ToSlash(rel)
	if relErr != nil {
		relPath = filepath.ToSlash(path)
	}

	pkgKey := dir + "|" + f.Name.Name
	set := r.reads[pkgKey]
	if set == nil {
		set = map[string]bool{}
		r.reads[pkgKey] = set
	}
	qual := r.qualified[pkgKey]
	if qual == nil {
		qual = map[string]bool{}
		r.qualified[pkgKey] = qual
	}

	// `package main` cannot be imported, so "an exported symbol with no reader
	// outside its own package" is a question with no possible answer for it. The
	// rule is a language fact rather than a decision, so no declaration is taken
	// from a main file — and the count is in the summary, because a structural
	// exclusion that is invisible is a structural exclusion nobody re-checks.
	//
	// Its *reads* are still recorded, and that is not a detail. `cmd/security-core`
	// is where the daemon calls `airuntime.NewVLMWorker`, `checkpoint.NewSigner`
	// and everything else the product is assembled from; an earlier version returned
	// before recording them, so the gate reported 40-odd live functions as dead
	// purely because their only caller was in a `main` package.
	if f.Name.Name == "main" {
		r.addSelectors(f, r.methods)
		addIdents(r, f, set, qual)
		r.mains++
		return
	}

	// Test files are excluded from the method scan, which is the same rule the
	// top-level scan uses for a different reason. `observability.Breaches` was
	// called by a test and by nothing else: a unit test proved the SLO evaluator
	// computed the right answer and no route or gauge loop ever invoked it, so the
	// SLOs were a document. A method whose only caller is a test is dead product
	// code wearing a test as a hat.
	isTest := strings.HasSuffix(path, "_test.go")
	if !isTest {
		r.addSelectors(f, r.methods)
	}
	addIdents(r, f, set, qual)

	// A test declares nothing, so only the reads above matter for it.
	if isTest {
		return
	}

	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if !decl.Name.IsExported() {
				continue
			}
			kind := "func"
			if decl.Recv != nil {
				kind = "method"
			}
			r.decls[pkgKey+"."+decl.Name.Name] = declSite{relPath, fset.Position(decl.Pos()).Line, kind}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				// A type declaration is not gated; see the package comment.
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				kind := "var"
				if decl.Tok == token.CONST {
					kind = "const"
				}
				for _, ident := range vs.Names {
					if ident.IsExported() {
						r.decls[pkgKey+"."+ident.Name] = declSite{relPath, fset.Position(ident.Pos()).Line, kind}
					}
				}
			}
		}
	}
}

// addIdents records every name a file *uses*, as distinct from every name it
// declares.
//
// The subtraction of this file's own declaration positions is what makes "a reader
// outside its own file" true rather than "a mention of the name somewhere": the
// declaration's `Name` is a child node of the declaration, and a recursive call
// inside a function body names the same identifier. Without the subtraction every
// function would be a reader of itself and the gate would report nothing for ever.
//
// Keyed by `token.Pos` rather than by `*ast.Object`, which `go/ast` deprecated in
// 1.22 for good reason — object identity cannot be computed correctly without type
// information, and this gate wants no type information. A position is exact, needs
// no resolution, and cannot be confused by two identifiers in one file that happen
// to share an object in a parser's approximation.
func addIdents(r *Repo, f *ast.File, into, qualified map[string]bool) {
	own := map[token.Pos]bool{}
	mark := func(id *ast.Ident) {
		if id != nil {
			own[id.Pos()] = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			mark(d.Name)
			if d.Recv != nil {
				for _, field := range d.Recv.List {
					for _, name := range field.Names {
						mark(name)
					}
				}
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					mark(s.Name)
				case *ast.ValueSpec:
					for _, ident := range s.Names {
						mark(ident)
					}
				}
			}
		}
		return true
	})

	add := func(id *ast.Ident) {
		if id == nil {
			return
		}
		if own[id.Pos()] {
			return
		}
		into[id.Name] = true
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			// A bare identifier, which is a call within this package or a use of a
			// name in scope. Cross-package references arrive as a SelectorExpr.
			add(x)
		case *ast.SelectorExpr:
			// `pkg.Symbol`: the identifier that matters is the selector, not the
			// package qualifier. This is how a reader in another package is seen,
			// and it is recorded twice — once bare, for the method rule, and once
			// qualified, so that `covercheck.Derive` does not excuse
			// `statuscheck.Derive`.
			add(x.Sel)
			if x.Sel != nil {
				if q, ok := r.qualifierName(x.X); ok {
					qualified[q+"."+x.Sel.Name] = true
				}
			}
		}
		return true
	})
}

// qualifierName returns the package qualifier of a selector, when the qualifier
// names a package this module declares.
//
// `repo.FilesParsed()` and `sort.Slice()` are the same syntax, and only the set of
// declared package names tells them apart: `FilesParsed` is a method on a value,
// `Slice` is a symbol in a package. Without the check every `x.Method()` in the
// tree reads every method of that name in every package, and the method rule
// degenerates into a spelling match — which is what it was for the first
// implementation, and it is why the gate reported `FilesParsed` as dead while its
// own test was calling it.
//
// The residual looseness is a variable named the same as a package, which makes
// the gate miss a finding. Missing a dead symbol is the safe direction; reporting a
// live one is the direction that gets the gate switched off.
func (r *Repo) qualifierName(x ast.Expr) (string, bool) {
	id, ok := x.(*ast.Ident)
	if !ok {
		return "", false
	}
	if !r.packageNames[id.Name] {
		return "", false
	}
	return id.Name, true
}

// Check returns every exported symbol with no reader, minus the allow-list.
func (r *Repo) Check(exempt map[string]Exemption) ([]Finding, []error) {
	entries := make([]Exemption, 0, len(exempt))
	bad := append([]error(nil), r.errs...)
	for key, ex := range exempt {
		if strings.TrimSpace(ex.Why) == "" {
			bad = append(bad, fmt.Errorf(
				"the allow-list excuses %s without saying why, which is not an excuse", key))
		}
		if ex.Symbol == "" && ex.Prefix == "" {
			bad = append(bad, fmt.Errorf("allow-list entry %s names neither a symbol nor a prefix", key))
		}
		if ex.Symbol != "" && ex.Prefix != "" {
			bad = append(bad, fmt.Errorf("allow-list entry %s names both a symbol and a prefix; they are alternatives", key))
		}
		entries = append(entries, ex)
	}
	// A family that is allowed to grow silently is a hole with a reason attached,
	// so a stale family entry is named: a prefix that matches nothing is dead
	// weight, and dead weight in an allow-list is an exemption for the next
	// symbol somebody names after it.
	matched := map[string]int{}
	for _, ex := range entries {
		matched[ex.key()] = 0
	}

	keys := make([]string, 0, len(r.decls))
	for k := range r.decls {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var findings []Finding
	for _, key := range keys {
		if r.HasReader(key) {
			continue
		}
		dot := strings.LastIndex(key, ".")
		dir, name := packageKey(key[:dot]), key[dot+1:]
		excused := false
		for _, ex := range entries {
			if ex.matches(dir, name) {
				matched[ex.key()]++
				excused = true
				break
			}
		}
		if excused {
			continue
		}
		d := r.decls[key]
		findings = append(findings, Finding{
			File: d.file, Line: d.line, Package: packageKey(key), Symbol: key[dot+1:], Kind: d.kind,
		})
	}

	stale := make([]string, 0)
	for k, n := range matched {
		if n == 0 {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	for _, k := range stale {
		bad = append(bad, fmt.Errorf(
			"the allow-list excuses %s and nothing matches it any more. Delete the entry rather than "+
				"leaving it to look like a decision that was made", k))
	}
	return findings, bad
}

// addSelectors records every selector name in a file, which is the whole question
// for a method: a method is only ever reached as `x.Name` or through an interface
// assertion that also spells `Name`, so a name that never appears in either
// position is a name nobody can be calling.
//
// Tree-wide and unqualified on purpose — see the package comment. A per-package
// version of this rule would report every method of every interface as dead,
// which is the interface-conformance noise that made method gating unusable.
//
// # Package-qualified selectors are excluded, local packages and imports alike
//
// The rationale is that a package-qualified selector is never a method call, and the
// exclusion used to recognise only the repository's OWN package names. `runtime` is
// not one of those, so `runtime.KeepAlive` in `internal/pipeline/disk_windows.go`
// was recorded as a read of the bare name `KeepAlive` — and that silently gave
// `internal/rtsp.(*Client).KeepAlive` a reader it has never had, so the gate reported
// a correct allow-list entry as stale.
//
// The direction is the bad one. The package comment calls out that a bare-name
// collision is "the one looseness in the gate", and this is that looseness firing:
// it turns a dead export live, which is the direction that makes a gate get switched
// off rather than the direction that makes it cry wolf. The file's import names are
// therefore collected and treated exactly like local package names, because for this
// purpose they are the same thing — an identifier that cannot be a receiver.
func (r *Repo) addSelectors(f *ast.File, into map[string]bool) {
	imports := importedPackageNames(f)
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		// Only an IDENTIFIER that names a package is excluded. A qualifier that is
		// itself a selector -- `c.metrics.Set` -- is a field chain on a value, and
		// that is exactly the shape of a method call the gate has to record.
		//
		// Returning early for a non-identifier qualifier, which an earlier version of
		// this did, silently stopped recording every method reached through a field,
		// and the gate reported fifty methods as dead. That is the same bad direction
		// as the bug this function was fixed for, reached by the fix.
		if id, isIdent := sel.X.(*ast.Ident); isIdent &&
			(r.packageNames[id.Name] || imports[id.Name]) {
			return true
		}
		into[sel.Sel.Name] = true
		return true
	})
}

// importedPackageNames is the set of identifiers in a file that name a package it
// imported, including any alias.
//
// The last path element is the name Go binds by default, and an explicit alias
// replaces it. A dotted import (`a/b/c`) binds the local name `c`, so the last
// element is still right. A blank or dot import (`_`/`.`) binds nothing usable and is
// excluded, because recording `_` as a package name would suppress every selector
// written after it in that file.
func importedPackageNames(f *ast.File) map[string]bool {
	out := make(map[string]bool, len(f.Imports))
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		p := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			switch imp.Name.Name {
			case "_", ".":
				continue
			case "C":
				// A cgo import binds `C`, which is a pseudo-package. Its selectors
				// are not methods of anything in this repository.
				out["C"] = true
				continue
			default:
				out[imp.Name.Name] = true
				continue
			}
		}
		if i := strings.LastIndexByte(p, '/'); i >= 0 && i+1 < len(p) {
			out[p[i+1:]] = true
		} else if p != "" {
			out[p] = true
		}
	}
	return out
}

// HasReader reports whether the symbol is reachable at all: for a top-level
// function, var or const, whether a package other than the declaring one names it
// by its qualified name (`otherpkg.Symbol`); for a method, whether its name is
// used as a selector anywhere in the tree.
//
// Exported so the gate's own tests can ask about a fixture directly.
func (r *Repo) HasReader(key string) bool {
	dot := strings.LastIndex(key, ".")
	pkgKey, name := key[:dot], key[dot+1:]

	// A method is reached through a receiver, so a bare-name collision is
	// indistinguishable from a call. This is the one looseness in the gate and it
	// is documented in the package comment.
	if r.methods[name] {
		return true
	}

	_, pkgName, ok := strings.Cut(pkgKey, "|")
	if !ok {
		pkgName = ""
	}
	want := pkgName + "." + name
	for _, set := range r.qualified {
		if set[want] {
			return true
		}
	}
	return false
}

// packageKey is the display form of a package key: the directory, which is what a
// reader looks up.
func packageKey(key string) string {
	if i := strings.LastIndex(key, "|"); i >= 0 {
		return key[:i]
	}
	return key
}

// Summary is what the gate prints about itself, so a reader can tell the
// difference between "no dead exports" and "the gate covered nothing".
func (r *Repo) Summary() string {
	return fmt.Sprintf("deadexport: %d exported symbols across %d hand-written Go files "+
		"(%d generated files, %d package main files skipped)",
		len(r.decls), r.files, r.generated, r.mains)
}

// FilesParsed is how many hand-written Go files the walk read.
func (r *Repo) FilesParsed() int { return r.files }

// SymbolsDeclared is how many exported top-level symbols the walk found.
func (r *Repo) SymbolsDeclared() int { return len(r.decls) }

// PrintedAllowList renders the allow-list, sorted, one entry per line.
//
// Printed on every run and not only on failure, which is the whole point of
// having a reason field.
func PrintedAllowList(exempt map[string]Exemption) string {
	keys := make([]string, 0, len(exempt))
	for k := range exempt {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "  allow-list (%d entr%s):\n", len(keys), plural(len(keys)))
	for _, k := range keys {
		ex := exempt[k]
		if strings.TrimSpace(ex.Why) == "" {
			b.WriteString("    " + k + " -- NO REASON GIVEN; this entry fails the gate\n")
			continue
		}
		b.WriteString("    " + k + " -- " + ex.Why + "\n")
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
