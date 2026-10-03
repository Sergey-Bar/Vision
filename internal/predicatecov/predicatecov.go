// Package predicatecov is the gate for one rule: `normalized_at` is the only field
// a time-window predicate may read.
//
// # Why this exists
//
// Three times exist on every canonical event, and they are not interchangeable:
//
//	observed_at    what the source claimed
//	received_at    our ingest
//	normalized_at  drift-corrected
//
// A camera whose clock is forty seconds fast puts its events forty seconds from
// where they happened. Read `observed_at` in a window predicate and the window is
// a window on the camera's belief about itself; read `received_at` and it is a
// window on when the ingest happened, which correlates with nothing an operator
// cares about. Only `normalized_at` is a window on the world.
//
// This is stated in `AGENTS.md`, in `internal/clock`'s package comment, in
// `internal/correlator`'s, and in `internal/normalize`'s. It is stated four times
// and enforced nowhere, which is the condition this repository keeps finding:
// a convention nobody can violate is a comment.
//
// # What the rule cannot be
//
// "Which predicates are time-window predicates" is not a question with a mechanical
// answer, and a gate that pretends otherwise produces false positives, and a gate
// with false positives is a gate somebody turns off. So the rule here is stated
// where it *is* mechanical and no further:
//
//  1. **No SQL predicate bounds a source-time column.** `observed_at`,
//     `received_at` and `captured_at` may not appear as the left side of a
//     comparison or inside a `BETWEEN`, in any statement, in any package. There
//     are none in the tree today, so the allow-list starts empty, and an empty
//     allow-list that has been measured is worth more than a broad rule that
//     needs forty exceptions. Bookkeeping predicates — a retention sweep on
//     `created_at`, an ordering on `created_at` — are untouched, because
//     `created_at` is not a claim about when the thing happened and no camera
//     can lie about it.
//  2. **The window predicate must exist and must read `normalized_at`.** This is
//     the positive direction, and without it rule 1 is satisfied by deleting the
//     window query altogether. A gate that only forbids the wrong thing passes
//     when the right thing is removed.
//  3. **The rule language reads `NormalizedAt` and never `ObservedAt`.** The
//     engine's state struct carries both fields today, and `ObservedAt` has no
//     reader in it. That is the shape of the defect this rule exists to prevent:
//     a field that is present, correct, and one line away from being used by the
//     next person who writes a window condition. The gate reports the field as a
//     finding, because the finding is the thing to remove.
package predicatecov

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The three source-time columns, named once. The pattern below builds itself from
// them rather than repeating them, because a regex that lists them separately from
// the sentence that names them is two sources of truth, and the failure of that is
// a gate that permits a column it documents as forbidden.
const (
	observedAt = "observed_at"
	receivedAt = "received_at"
	capturedAt = "captured_at"
)

// SourceTimeColumns are the columns a device's own clock decides, or that our
// ingest decides. A predicate on any of them is a predicate on a claim, not on an
// instant.
//
// Exported because the rule is worth being able to check from another package, and
// because the gate's own test names them rather than re-typing them. It was not
// exported at first, and `internal/deadexport` reported it on the first run for
// having no reader outside its own package — which is the gate doing its job on the
// gate, and the fix was to give the sentence a reader rather than to excuse it.
var SourceTimeColumns = []string{observedAt, receivedAt, capturedAt}

// Finding is one thing that breaks the rule.
type Finding struct {
	// File and Line locate it. Line is the Go line the SQL literal starts on.
	File string
	Line int
	// Rule names what was broken.
	Rule string
	// Detail is the excerpt that identifies it without opening the file.
	Detail string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Rule, f.Detail)
}

// Rules are the rule names, exported so a caller can switch on them.
const (
	// RuleSourceTimePredicate is a SQL predicate bounding a column the source or
	// the ingest decided.
	RuleSourceTimePredicate = "a time-window predicate reads a source-time column"
	// RuleNoWindowPredicate is the positive direction: the canonical-event window
	// query is gone.
	RuleNoWindowPredicate = "the canonical-event window query no longer bounds normalized_at"
	// RuleRuleLanguageReadsObserved is the rule engine reading the source's claim
	// rather than the corrected instant.
	RuleRuleLanguageReadsObserved = "the rule engine reads ObservedAt rather than NormalizedAt"
	// RuleObservedFieldPresent is the unused `ObservedAt` field sitting one line
	// away from the next window condition.
	RuleObservedFieldPresent = "the rule engine's state carries an ObservedAt field with no reader"
)

// A comparison's left side. Deliberately anchored on a word boundary and a
// non-word character after the column, so `e.normalized_at` matches and
// `e.normalized_at_x` does not, and so a column name in a comment is not read as
// a predicate. Comments are stripped before this runs, which is what makes that
// second guarantee hold rather than being a hope.
var (
	compareLeft = regexp.MustCompile(
		`(?i)\b(` + observedAt + `|` + receivedAt + `|` + capturedAt +
			`)\b\s*(?:<=|>=|<|>|=|<>|!=)|\b(?:` + observedAt + `|` + receivedAt + `|` + capturedAt +
			`)\b\s+(?i:not\s+between|between)\b`)
	normalizedBound = regexp.MustCompile(`(?i)\bnormalized_at\b\s*(?:<=|>=|<|>)`)
	// observedAtMention is a bare mention, with no operator required. See
	// RuleSourceTimeColumnSelected.
	observedAtMention = regexp.MustCompile(`(?i)\b(?:` + observedAt + `|` + receivedAt + `|` + capturedAt + `)\b`)
)

// RuleSourceTimeColumnSelected is a source-time column appearing in the window
// query without a comparison — a SELECT list, a function argument, a comment
// stripped earlier. Its presence is not yet a violation, which is exactly why it
// needs watching: the column was in that select list until this work removed it,
// and nothing about rule 1 would have said so.
const RuleSourceTimeColumnSelected = "the window query mentions a source-time column at all"

// firstMatch names what matched, so the finding says *which* column rather than
// only that one of three did.
func firstMatch(re *regexp.Regexp, text string) string {
	if m := re.FindString(text); m != "" {
		return m
	}
	return "a source-time column"
}

// Exemption is one allow-listed source-time predicate, and the reason is
// mandatory. The list starts empty because the tree has no such predicate; a gate
// whose allow-list is seeded with entries nobody ran is a gate that reports
// exemptions and proves nothing.
//
// It is keyed by "file:line" rather than carrying a file and a line, so an
// exemption cannot cover a second statement in the same file by accident and
// there is no way for the two halves of a key to disagree with each other.
type Exemption struct {
	// Why must say why reading a source's claim is correct here. A blank one is a
	// refusal, not an excuse.
	Why string
}

// Config is what Check needs.
type Config struct {
	// Root is the module root.
	Root string
	// CorrelatorDir is where the canonical-event window query must live. Derived
	// from Root by Load if empty.
	CorrelatorDir string
	// RulesDir is the rule language's directory.
	RulesDir string
	// Exempt is the allow-list.
	Exempt map[string]Exemption
}

// Check runs the three rules and returns the findings plus any walk errors.
//
// A walk error is returned rather than swallowed. A gate that cannot read a file
// and reports the rest as clean is the exact defect this repository keeps finding
// in its own other gates.
func Check(cfg Config) ([]Finding, []error) {
	correlator := cfg.CorrelatorDir
	if correlator == "" {
		correlator = filepath.Join(cfg.Root, "internal", "correlator")
	}
	rulesDir := cfg.RulesDir
	if rulesDir == "" {
		rulesDir = filepath.Join(cfg.Root, "internal", "rules")
	}

	var findings []Finding
	var errs []error
	// used records which exemptions actually suppressed a finding, so the stale
	// check asks the exemption rather than the finding it hid.
	used := map[string]bool{}

	// Rule 1 and rule 2, over every SQL literal in the tree.
	correlatorHasWindow := false
	err := filepath.Walk(cfg.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kilo", "poc", "node_modules", "__pycache__", "dist", "migrations", "rules", "docs", "contracts", "deploy", "testdata":
				// `migrations` holds SQL that *defines* the columns rather than
				// reading them, and `rules`/`docs`/`contracts` are not Go.
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(cfg.Root, path)
		if relErr != nil {
			rel = path
		}
		relPath := filepath.ToSlash(rel)

		src, readErr := os.ReadFile(path)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", relPath, readErr))
			return nil
		}
		if isGenerated(src) {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, src, 0)
		if parseErr != nil {
			errs = append(errs, fmt.Errorf("parse %s: %w", relPath, parseErr))
			return nil
		}

		for _, lit := range sqlLiterals(file) {
			text := stripSQLComments(lit.text)
			for _, m := range compareLeft.FindAllString(text, -1) {
				line := fset.Position(lit.pos).Line
				key := relPath + ":" + fmt.Sprint(line)
				if ex, ok := cfg.Exempt[key]; ok {
					if strings.TrimSpace(ex.Why) == "" {
						errs = append(errs, fmt.Errorf(
							"the allow-list excuses %s without saying why, which is not an excuse", key))
					}
					// Recorded rather than dropped: an exemption suppresses a
					// finding, so the stale check downstream has to know it fired
					// from the exemption's side rather than from the finding's.
					used[key] = true
					continue
				}
				findings = append(findings, Finding{
					File: relPath, Line: line, Rule: RuleSourceTimePredicate,
					Detail: excerpt(text, m),
				})
			}
			// Rule 2's positive direction is asked of *the window query*, not of the
			// package. The first version asked whether any literal in
			// internal/correlator bounded `normalized_at`, which one more comparison
			// anywhere in the package would satisfy — so deleting the bound on the
			// actual window query while adding an unrelated one passed the gate whose
			// only job is that the window query exists.
			if strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(correlator)) &&
				strings.Contains(strings.ToLower(text), "canonical_events") &&
				normalizedBound.MatchString(text) {
				correlatorHasWindow = true
			}

			// Rule 3: the window query must not read the source's clock *at all*,
			// even without a comparison.
			//
			// `docs/status.md` says the gate fails if the column comes back. A
			// comparison is what rule 1 catches, so re-adding `e.observed_at` to a
			// SELECT list — the exact shape it had before this work — was invisible.
			// A column in a select list is an invitation, and the select list is where
			// it started.
			if strings.HasPrefix(filepath.ToSlash(path), filepath.ToSlash(correlator)) &&
				observedAtMention.MatchString(text) {
				findings = append(findings, Finding{
					File: relPath, Line: fset.Position(lit.pos).Line, Rule: RuleSourceTimeColumnSelected,
					Detail: "internal/correlator's window query mentions " + firstMatch(observedAtMention, text) +
						". It was removed from this select list on purpose: a source-time column in a " +
						"statement whose window is the whole reason normalized_at exists is one line from " +
						"being used by the next person to write a condition",
				})
			}
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}

	// Rule 2, the positive direction.
	if !correlatorHasWindow {
		findings = append(findings, Finding{
			File: "internal/correlator", Line: 0, Rule: RuleNoWindowPredicate,
			Detail: "no statement in internal/correlator bounds normalized_at. Either the window query " +
				"was deleted or it now reads another column, and a gate that only forbids the wrong " +
				"column passes when the right one is removed",
		})
	}

	// Rule 3.
	findings = append(findings, checkRuleLanguage(rulesDir)...)

	// A declaration for a line that no longer breaks the rule is an exemption for
	// the next line somebody writes after it.
	for key := range cfg.Exempt {
		if !used[key] {
			errs = append(errs, fmt.Errorf(
				"the allow-list excuses %s and nothing breaks the rule there any more. Delete the entry "+
					"rather than leaving it to look like a decision that was made", key))
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, errs
}

// checkRuleLanguage is rule 3: the engine may reach the corrected instant and must
// not reach the source's claim.
//
// The field's *presence* is a finding as well as its reads, and that is the whole
// argument. An `ObservedAt` on the engine's state with no reader is a field that
// exists, is correctly typed, is documented, and is one line from being used by
// the next person to write a window condition. The day it is used, the window is
// on the camera's clock and nothing in the suite fails.
func checkRuleLanguage(rulesDir string) []Finding {
	var out []Finding

	nonTest, reads, err := scanGoDir(rulesDir)
	if err != nil {
		return []Finding{{File: relOrSelf(rulesDir), Rule: RuleRuleLanguageReadsObserved, Detail: err.Error()}}
	}

	if usesObserved(reads) {
		out = append(out, Finding{
			File: relOrSelf(rulesDir), Rule: RuleRuleLanguageReadsObserved,
			Detail: "the engine reads ObservedAt. A time-window predicate reads NormalizedAt; the " +
				"source's own claim is the thing under suspicion, and reading it here is the defect " +
				"the rule exists to prevent",
		})
	}
	if nonTest && !usesObserved(reads) && declaresObservedField(rulesDir) {
		out = append(out, Finding{
			File: relOrSelf(rulesDir), Rule: RuleObservedFieldPresent,
			Detail: "the engine's state carries ObservedAt and nothing reads it. A field that exists, " +
				"is correctly typed and is documented is one line away from being used by the next " +
				"person to write a window condition. Remove the field, or make the gate say why it stays",
		})
	}
	return out
}

// scanGoDir returns whether the directory has non-test Go files, and the selector
// names those files use.
func scanGoDir(dir string) (bool, map[string]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, nil, fmt.Errorf("read %s: %w", dir, err)
	}
	uses := map[string]int{}
	anyNonTest := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			return false, nil, fmt.Errorf("read %s: %w", name, readErr)
		}
		if isGenerated(src) {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		anyNonTest = true
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), src, 0)
		if parseErr != nil {
			return false, nil, fmt.Errorf("parse %s: %w", name, parseErr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel != nil {
				uses[sel.Sel.Name]++
			}
			return true
		})
	}
	return anyNonTest, uses, nil
}

func usesObserved(uses map[string]int) bool { return uses["ObservedAt"] > 0 }

// declaresObservedField reports whether any non-test Go file in dir declares a
// struct field literally named `ObservedAt`.
//
// Parsed rather than grepped, and the distinction matters: the first version of
// this matched the *string* `ObservedAt` anywhere in the file, so the explanatory
// comment recording why the field was removed kept it reporting the removal. A
// check whose own documentation keeps it red is a check that gets deleted.
func declaresObservedField(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		src, readErr := os.ReadFile(path)
		if readErr != nil || isGenerated(src) {
			continue
		}
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, src, 0)
		if parseErr != nil {
			continue
		}
		found := false
		ast.Inspect(f, func(n ast.Node) bool {
			if found {
				return false
			}
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == "ObservedAt" {
						found = true
					}
				}
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// sqlLiterals is every SQL string in a file, with its position. Not a SQL parser
// and not trying to be: the question is which column names appear beside a
// comparison operator, and a string that is not SQL cannot contain
// `observed_at <`.
//
// Concatenations are folded, and that is not a refinement. The correlator's window
// query is written as
//
//	scope.Query(ctx, `
//	   SELECT `+scanColumns+`
//	     FROM canonical_events e`+joins+`
//	   WHERE e.tenant_id = $1
//	     AND e.normalized_at >= $4`)
//
// so the bounds live in the *last* fragment of a `+` chain, and a gate that read
// one fragment at a time would find no window predicate in the repository's own
// correct code and would then report its absence as a deleted query.
func sqlLiterals(f *ast.File) []sqlLit {
	var out []sqlLit
	seen := map[token.Pos]bool{}
	add := func(text string, pos token.Pos) {
		if seen[pos] {
			return
		}
		seen[pos] = true
		out = append(out, sqlLit{text: text, pos: pos})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || be.Op != token.ADD {
			// A bare literal, or a non-concatenation expression. The literal case is
			// handled below; returning true keeps descending.
			return true
		}
		if text, pos, ok := foldConcat(be); ok {
			add(text, pos)
		}
		// The whole chain was folded, so its parts must not be visited again or
		// the same statement is reported once per fragment.
		return false
	})

	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if text, err := unquote(lit.Value); err == nil {
				add(text, lit.Pos())
			}
		}
		return true
	})
	return out
}

// foldConcat flattens a left-leaning `+` chain of string literals. A non-literal
// operand ends the fold rather than failing it, because a query with a `%s` in it
// is still a query and its bounds are still bounds.
func foldConcat(e ast.Expr) (text string, pos token.Pos, ok bool) {
	switch v := e.(type) {
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", token.NoPos, false
		}
		left, lpos, lok := foldConcat(v.X)
		right, _, rok := foldConcat(v.Y)
		if !lok {
			left, lpos = "", token.NoPos
		}
		if !rok {
			right = ""
		}
		if lpos == token.NoPos {
			return "", token.NoPos, false
		}
		return left + right, lpos, true
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", token.NoPos, false
		}
		s, err := unquote(v.Value)
		if err != nil {
			return "", token.NoPos, false
		}
		return s, v.Pos(), true
	default:
		return "", token.NoPos, false
	}
}

type sqlLit struct {
	text string
	pos  token.Pos
}

// stripSQLComments removes `--` line comments and `/* */` blocks.
//
// This is what makes rule 1 about predicates rather than about prose. A file that
// documents the rule by quoting the wrong column in a comment must not fail, and a
// check that cannot tell a comment from a statement is a check that reports the
// documentation as a defect.
func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	out := b.String()
	for {
		i := strings.Index(out, "/*")
		if i < 0 {
			return out
		}
		j := strings.Index(out[i:], "*/")
		if j < 0 {
			return out[:i]
		}
		out = out[:i] + " " + out[i+j+2:]
	}
}

func excerpt(text, match string) string {
	flat := strings.Join(strings.Fields(text), " ")
	i := strings.Index(flat, match)
	if i < 0 {
		return match
	}
	start := i - 40
	if start < 0 {
		start = 0
	}
	end := i + len(match) + 40
	if end > len(flat) {
		end = len(flat)
	}
	return "..." + flat[start:end] + "..."
}

func relOrSelf(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(dir)
	}
	rel, err := filepath.Rel(filepath.Dir(wd), dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}

// isGenerated mirrors the convention `internal/deadexport` uses, kept local so
// this package does not depend on a gate it also feeds.
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
		if strings.HasPrefix(trimmed, "//") {
			c := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
			if !strings.HasPrefix(c, "Code generated by") {
				continue
			}
			return strings.Contains(c, "DO NOT EDIT.")
		}
	}
	return false
}

func unquote(s string) (string, error) {
	// A SQL literal in this repository is either a backquoted raw string or a
	// double-quoted one. Anything else cannot hold a column name and a comparison.
	if strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") && len(s) >= 2 {
		return s[1 : len(s)-1], nil
	}
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(s[1 : len(s)-1]), nil
	}
	return "", fmt.Errorf("not a plain string literal")
}
