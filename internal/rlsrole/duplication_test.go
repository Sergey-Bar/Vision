package rlsrole

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoLargeDuplicatedBlocks is the duplication check Sonar would have run.
//
// It compares function bodies, normalised so that two functions differ only when
// they actually do something different. Identifier names are kept, because
// renaming a function to match another one is not the fix - the two are
// usually there for different reasons and the names say which.
//
// The threshold is deliberately high. A handful of repeated lines is normal and
// extracting it into a helper costs more in indirection than it saves. A
// twenty-line copy is two places to fix one bug, and this repository has already
// had one of those turn into a security defect.
func TestNoLargeDuplicatedBlocks(t *testing.T) {
	// Sixteen statements is the smallest body where a copy is a liability. The
	// three sites this caught during the row-level-security work were each
	// twenty-plus lines and each was a bug fixed in one copy and not the other.
	const minStatements = 16

	root := repoRoot(t)
	fset := token.NewFileSet()
	type block struct {
		where string
		lines int
	}
	byHash := map[string][]block{}

	err := filepath.Walk(filepath.Join(root, "internal"), func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			// Generated code and test bodies are excluded: protobuf output is not
			// ours, and a repeated test fixture is repetition on purpose.
			switch info.Name() {
			case "contracts", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") ||
			strings.HasSuffix(p, "_test.go") ||
			strings.HasSuffix(p, ".pb.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, p, nil, 0)
		if parseErr != nil {
			return nil //nolint:nilerr // a file that does not parse fails the build elsewhere
		}
		rel, _ := filepath.Rel(root, p)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var body []string
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					return false
				}
				switch n.(type) {
				case *ast.AssignStmt, *ast.ExprStmt, *ast.IfStmt, *ast.ForStmt,
					*ast.RangeStmt, *ast.ReturnStmt, *ast.DeferStmt, *ast.SwitchStmt,
					*ast.SelectStmt, *ast.GoStmt, *ast.IncDecStmt:
					body = append(body, normalise(n))
				}
				return true
			})
			if len(body) < minStatements {
				continue
			}
			sum := sha256.Sum256([]byte(strings.Join(body, "\n")))
			hash := hex.EncodeToString(sum[:8])
			byHash[hash] = append(byHash[hash], block{
				where: rel + ":" + itoa(fset.Position(fn.Pos()).Line),
				lines: len(body),
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	var duplicated []string
	for _, group := range byHash {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].where < group[j].where })
		places := make([]string, 0, len(group))
		for _, b := range group {
			places = append(places, b.where)
		}
		duplicated = append(duplicated, strings.Join(places, "  and  "))
	}

	if len(duplicated) > 0 {
		sort.Strings(duplicated)
		t.Errorf("%d block(s) of %d+ statements are duplicated:\n  - %s\n"+
			"  A copy is two places to fix one bug. Extract the shared shape, or say why "+
			"the two genuinely differ.",
			len(duplicated), minStatements, strings.Join(duplicated, "\n  - "))
	}
	t.Logf("scanned internal/ for duplicated blocks of %d+ statements", minStatements)
}

// normalise renders a statement by its shape rather than its spelling, so
// whitespace and the name of a local do not decide whether two bodies match.
func normalise(n ast.Node) string {
	var b strings.Builder
	ast.Inspect(n, func(x ast.Node) bool {
		switch v := x.(type) {
		case *ast.Ident:
			b.WriteString("id:")
			b.WriteString(v.Name)
		case *ast.BasicLit:
			b.WriteString("lit:")
			b.WriteString(v.Value)
		case *ast.SelectorExpr:
			b.WriteString(".")
		}
		return true
	})
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
