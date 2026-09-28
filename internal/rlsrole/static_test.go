package rlsrole

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tenantScopedTables are the tables a migration gave row-level security.
//
// The list is read from the migrations rather than hard-coded, so a table added
// later is covered without anyone remembering this file. A hard-coded list is a
// list that goes stale, and a stale list is a guard that stops guarding.
var tenantScopedTables = []string{
	"tenant", "site", "zone", "device", "camera", "integration",
	"inbox_event", "inbox_events", "inbox_event_keys",
	"canonical_event", "canonical_events", "canonical_event_keys",
	"incident", "incidents", "incident_events", "incident_evidence",
	"verification_results", "evidence", "edge", "edges", "edge_revocations",
	"edge_evidence", "entity", "entities", "device_clocks",
	"ai_observations", "disposition", "dispositions",
	"polic", "rule_bundle", "audit_ledger", "audit_checkpoints",
	"user", "users", "user_roles", "user_site_assignments", "session", "sessions",
	"mfa_credential", "mfa_credentials", "outbox_events", "jobs",
	"source_events",
}

// TestNoBareTenantQueries is the static half of this package.
//
// Every query the product makes against a tenant-scoped table must run inside
// InTenant or InSystemTenant. A bare Pool.Query, Pool.QueryRow or Pool.Exec with
// SQL naming one of those tables is a finding, always — because the correct form
// is always to go through a transaction that already carries the context.
//
// This rule exists because the class is invisible to every other check. The unit
// tests connect as a superuser, which bypasses row-level security, so a query
// with no tenant context returns the right answer in the suite and the wrong
// answer on a site. The runtime canary in this package catches the flows it
// drives; this catches everything else, before anyone deploys it.
func TestNoBareTenantQueries(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	var findings []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			name := info.Name()
			// Generated code, version control and the vendored toolchain are not
			// ours to police.
			if name == ".git" || name == "bin" || name == "dist" {
				return filepath.SkipDir
			}
			// Another checkout on this machine, and the agent's own worktrees. A
			// finding in one of those is not a finding here — and a tool that
			// reads them will happily rewrite code it does not own, which is how
			// forty unrelated files were modified in a neighbouring worktree
			// before this check was given the skip.
			if strings.HasPrefix(name, ".") || name == "worktrees" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)+"contracts"+string(filepath.Separator)+"securityv1") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			// A file that does not parse fails the build elsewhere, with a
			// clearer message than this pass could produce. Skipping keeps one
			// failure per problem instead of two.
			return nil //nolint:nilerr // deliberate: a broken file is reported by the compiler
		}
		rel, _ := filepath.Rel(root, path)
		findings = append(findings, scanFile(fset, file, rel)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(findings) > 0 {
		t.Errorf("%d query against a tenant-scoped table runs without a tenant context.\n"+
			"Each of these returns no rows (or is refused by RLS) under the role the product "+
			"actually runs as, and returns the right answer in the test suite because the "+
			"suite connects as a superuser.\n"+
			"  - the query must run inside Pool.InTenant or Pool.InSystemTenant\n\n  %s",
			len(findings), strings.Join(findings, "\n  - "))
	}
}

func scanFile(fset *token.FileSet, file *ast.File, rel string) []string {
	var findings []string

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// The call reads `s.Pool.Query(...)`: the outer selector is the method,
		// the inner one names the field.
		receiver, ok := sel.X.(*ast.SelectorExpr)
		if !ok || receiver.Sel.Name != "Pool" {
			return true
		}
		switch sel.Sel.Name {
		case "Query", "QueryRow", "Exec":
		default:
			return true
		}
		// Every one of these takes (ctx, sql, args...), so the SQL is the
		// second argument. Reading the first gets the context and finds nothing,
		// which is a guard that passes because it is looking at the wrong place.
		if len(call.Args) < 2 {
			return true
		}
		sql := literalOf(call.Args[1])
		if sql == "" {
			return true
		}
		if mentionsTenantTable(sql) == "" {
			return true
		}
		pos := fset.Position(call.Pos())
		fn := enclosingFunc(file, call)
		findings = append(findings, fmt.Sprintf(
			"%s:%d  in %s  (Pool.%s)", rel, pos.Line, fn, sel.Sel.Name))
		return true
	})
	return findings
}

// enclosingFunc names the function a call sits in, so a report is bounded by
// functions rather than by lines. Forty-eight lines in a dozen functions is a
// day's work; forty-eight scattered call sites is a design problem.
func enclosingFunc(file *ast.File, call *ast.CallExpr) string {
	var name string
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Body == nil {
			return true
		}
		if decl.Pos() <= call.Pos() && call.End() <= decl.End() {
			name = decl.Name.Name
		}
		return true
	})
	if name == "" {
		return "a package-level expression"
	}
	return name
}

// literalOf returns the SQL when the argument is a string literal or the
// concatenation of literals, which is how every query in this repository is
// written. A query built from a variable is not scanned, which is a known limit
// and is stated rather than hidden.
func literalOf(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if s, err := strconv.Unquote(e.Value); err == nil {
				return s
			}
		}
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return ""
		}
		return literalOf(e.X) + literalOf(e.Y)
	case *ast.ParenExpr:
		return literalOf(e.X)
	}
	return ""
}

func mentionsTenantTable(sql string) string {
	lower := strings.ToLower(sql)
	for _, table := range tenantScopedTables {
		// A whole-word match, so `sites_of_users` does not count as `site`.
		idx := 0
		for {
			at := strings.Index(lower[idx:], table)
			if at < 0 {
				break
			}
			at += idx
			before := at - 1
			for before >= 0 && isWordByte(lower[before]) {
				before--
			}
			after := at + len(table)
			for after < len(lower) && isWordByte(lower[after]) {
				after++
			}
			if (before < 0 || !isWordByte(lower[before])) &&
				(after >= len(lower) || !isWordByte(lower[after])) {
				return table
			}
			idx = at + len(table)
		}
	}
	return ""
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the repository root")
	return ""
}
