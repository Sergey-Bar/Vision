package rlsrole

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryRouteDeclaresACapability is the authorization matrix, enforced.
//
// Every authenticated route in the API is registered through requireCapability.
// That is the only mechanism in the server that checks a capability, and a route
// registered through requirePrincipal alone is one where somebody remembered to
// write the check by hand - or forgot. A forgotten check is a read of somebody
// else's incidents.
//
// The check is deliberately narrow. It finds the two registration helpers and
// requires a capability in the first. The second is allowed only for the routes
// that genuinely cannot have one, each listed with the reason, so adding a route
// without a gate is a decision somebody wrote down rather than an oversight.
func TestEveryRouteDeclaresACapability(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal", "api", "handlers.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse handlers.go: %v", err)
	}

	namesCapability := func(call *ast.CallExpr) bool {
		found := false
		ast.Inspect(call, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok &&
				strings.HasPrefix(sel.Sel.Name, "Cap") {
				found = true
			}
			return true
		})
		return found
	}

	// isBareNil reports whether an argument is the literal `nil`.
	//
	// A bare `nil` is an `*ast.Ident` whose name happens to be "nil"; a real
	// resolver is a call (`siteOfIncident(d)`) or a function literal. Anything that
	// is not the bare identifier is a resolver, including forms this check does not
	// know about -- which is the right default here, because the failure being
	// guarded against is a gate that was wired with no resolver at all, not one
	// wired with an unusual one.
	isBareNil := func(e ast.Expr) bool {
		ident, ok := e.(*ast.Ident)
		return ok && ident.Name == "nil"
	}

	exempt := map[string]string{
		"POST /v1/auth/login":      "a caller has no session yet, so there is nothing to authorise",
		"POST /v1/auth/mfa/verify": "the challenge is itself the credential: single-use and short-lived",
		// The console's stylesheet. It is the same file for every operator, it
		// carries no tenant data, and requiring a session to fetch a stylesheet
		// would leave an unauthenticated operator looking at an unstyled page.
		"GET /static/{file}": "the console stylesheet: identical for every operator and " +
			"carrying no tenant data",
		// The console's sign-out. There is no capability that fits it: signing out is
		// not something an operator may lack the right to do, and a CapSessionRead
		// held by nobody would be a capability table entry that grants nothing while
		// looking like a control. It requires a principal -- so it authorises rather
		// than merely existing -- and the CSRF check does apply to it.
		//
		// Listed here for the reason this list exists: so that "this route needs no
		// capability" is a sentence somebody wrote rather than a gap in a switch.
		"POST /logout": "signing out is not gated on a capability because there is no " +
			"capability to gate it on; it requires a principal and a CSRF token",
	}

	var problems []string
	registered := 0

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// The registration is mux.Handle("GET /v1/x", s.requireCapability(...)).
		// The route is this call's first argument; the gate is inside its second.
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "mux" || sel.Sel.Name != "Handle" {
			return true
		}
		if len(call.Args) < 2 {
			return true
		}
		route := literalOf(call.Args[0])
		if route == "" {
			return true
		}

		// A route registered with no gate helper at all, rather than an ungated one.
		gate, isCall := call.Args[1].(*ast.CallExpr)
		if !isCall {
			if _, allowed := exempt[route]; !allowed {
				problems = append(problems, route+": registered without a gate helper, so no "+
					"capability check runs at all")
			}
			registered++
			return true
		}
		gateSel, ok := gate.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		registered++

		// The stylesheet is registered with a static handler rather than a gate
		// helper, and it is on the exempt list. Checked by route rather than by
		// helper name, because "no gate helper" is the shape the exempt routes
		// legitimately have.
		if _, allowed := exempt[route]; allowed {
			return true
		}

		switch gateSel.Sel.Name {
		case "requireCapability":
			if !namesCapability(gate) {
				problems = append(problems, route+": gated by requireCapability with no capability")
			}
		case "requireCapabilityAtSite":
			// Added when the per-site check moved into the router. It is a gate, and
			// it names a capability, so it is here rather than on the exempt list --
			// which matters, because the alternative reading of a new wrapper's name
			// being unrecognised is "not an authorisation gate", and a wrapper that
			// resolves an incident's site and calls `AuthorizeSite` is precisely one.
			//
			// It is listed separately from `requireCapability` rather than folded into
			// it because the two are not interchangeable: this one refuses an id that
			// resolves to no site, and `requireCapability` cannot. Naming both in one
			// case would lose the distinction and let the weaker wrapper be used on a
			// site-bound route without anything noticing.
			if !namesCapability(gate) {
				problems = append(problems, route+": gated by requireCapabilityAtSite with no capability")
			}
			// And it must actually resolve a site. `requireCapabilityAtSite(cap, nil, h)`
			// would panic on the first request rather than refuse it, and a panic on
			// the authorisation path is a refusal only by accident.
			//
			// Checked by type rather than by source text: a bare `nil` argument is an
			// `*ast.Ident` named "nil", and anything else -- a call, a selector, a
			// function literal, which is what `siteOfIncident(d)` is -- is a resolver.
			// Comparing the rendered source instead would need a printer and would
			// then be a check on formatting.
			if len(gate.Args) < 3 || isBareNil(gate.Args[1]) {
				problems = append(problems, route+": gated by requireCapabilityAtSite with no site "+
					"resolver, so it cannot check the site it is named for")
			}
		case "requirePrincipal":
			problems = append(problems, route+": registered with requirePrincipal and no "+
				"capability gate. If it genuinely needs none, add it to the exempt list "+
				"with the reason — do not leave it ungated")
		default:
			problems = append(problems, route+": registered with "+gateSel.Sel.Name+
				", which is not an authorisation gate")
		}
		return true
	})

	if registered == 0 {
		t.Fatal("no routes were found, so the check is looking at the wrong place and passing " +
			"vacuously — a matrix that reads nothing asserts nothing")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("%d of %d route(s) have no capability gate:\n  - %s",
			len(problems), registered, strings.Join(problems, "\n  - "))
	}
	t.Logf("%d routes checked, %d gated by capability", registered, registered-len(problems))
}

// TestHandlersDoNotReturnRawErrors is a small honesty check on the error surface.
//
// A database error names tables, columns, and occasionally a value. Handing one
// to a browser tells whoever is reading - including an attacker holding a valid
// session - the shape of the schema behind an endpoint that was supposed to say
// "not found". The detail belongs in the log; the client gets a message it can
// act on.
func TestHandlersDoNotReturnRawErrors(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var problems []string

	err := filepath.Walk(filepath.Join(root, "internal", "api"), func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, p, nil, 0)
		if parseErr != nil {
			return nil //nolint:nilerr // a file that does not parse fails the build elsewhere
		}
		rel, _ := filepath.Rel(root, p)

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteError" || len(call.Args) < 4 {
				return true
			}
			pos := fset.Position(call.Lparen)
			ast.Inspect(call.Args[3], func(m ast.Node) bool {
				inner, ok := m.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if id, ok := inner.X.(*ast.Ident); ok && id.Name == "err" && inner.Sel.Name == "Error" {
					problems = append(problems,
						rel+":"+strconv.Itoa(pos.Line)+"  a raw database or service error is "+
							"returned to the client. Log the detail; return a message the "+
							"reader can act on")
				}
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Errorf("%d handler(s) return a raw error to the client:\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
	t.Logf("the error surface across internal/api is free of raw database errors")
}
