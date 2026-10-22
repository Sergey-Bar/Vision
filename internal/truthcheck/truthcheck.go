// Package truthcheck refuses a document that claims a control this product does
// not have.
//
// # What this is for
//
// This repository's own documentation used to assert, in the package doc of its
// API and in its threat model, four things that were not true:
//
//   - that every state-changing method required a CSRF token;
//   - that every session cookie was HttpOnly, Secure and SameSite;
//   - that a `X-Tenant-Id` header was honoured only when it matched the session
//     tenant;
//   - that the console had a login, a session and a logout.
//
// None of them existed. The console has no login, no session, no cookie and no
// token, so the first two are about a surface that is not there, and the third
// was about a header that was read with nothing to match it against -- which
// made it an instruction rather than an authorisation.
//
// A security product whose documentation claims controls it does not have is
// worse than one that claims nothing, because the documentation is what a
// security director reads. A false claim there is a false claim about what this
// product does to a stranger's data.
//
// # What it checks, and what it deliberately does not
//
// A closed list of claim patterns, and the files allowed to match them.
//
// The list is closed and short, and every entry says why the control is
// absent. It is not a keyword ban: the word "CSRF" appears in `internal/identity`,
// which IMPLEMENTS a CSRF check for a sign-in that does not exist yet, and a
// gate that fired on that word would be a gate that gets switched off.
//
// The patterns match ASSERTIONS, not names. `X-Tenant-Id` on its own is not a
// claim: `docs/status.md` and `web/scope.go` both explain at length why the
// header used to be read and why it no longer is, and that explanation is the
// document being honest. What is a claim is the header being TRUSTED, and
// that is what the pattern looks for.
//
// The allow-list is per FILE and per CLAIM, not per file. A file that may name
// one absent control may not name another, so the list cannot grow silently in
// either direction: adding a claim to a file needs a new entry with a reason,
// and an entry whose claim is no longer there is stale and fails the gate.
//
// # The scope, and why it is not the whole tree
//
// Markdown documents, and the COMMENTS of Go files.
//
// Not Go string literals: `internal/identity` returns an error whose text says
// a token is required, and that is a true statement about the function it is
// in -- which is why that one file is on the allow-list. Not JavaScript and
// TypeScript: the SPA is a separate surface with its own gates.
//
// This package's own source is excluded. It holds the claim patterns, because
// a gate that reads itself finds itself on the first run.
//
// # What this gate does NOT claim
//
// That the documentation is complete, or that every control it describes
// exists. It claims one thing: a document may not assert that this product has
// a control it does not have. A control that exists and is not described is a
// different defect, and `internal/capability` is where that one lives.
//
// And it does not catch a reworded false claim whose assertion no pattern
// matches. That is the honest limit of a narrow claim-list, and it is why the
// list is the one a human reads when they are about to write about a control.
package truthcheck

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Claim is one assertion a document may not make.
//
// The pattern matches an ASSERTION, not a name. See the package doc for why.
type Claim struct {
	// ID names the claim, and is what an allowance refers to.
	ID string
	// Re is the pattern.
	Re *regexp.Regexp
	// Why says what this product does not have, and what it has instead.
	Why string
}

// The closed set.
//
// Every entry names a control that is absent from this tree, and every entry
// was a false claim in a document this repository shipped.
var (
	// claimRequireCSRF names a middleware that is not in the tree.
	claimRequireCSRF = Claim{
		ID: "require-csrf",
		Re: regexp.MustCompile(`\brequireCSRF\b`),
		Why: "there is no CSRF middleware: the console has no session, no cookie and no token, " +
			"so there is nothing for a token to protect",
	}
	// claimRequireTenant names a function that is not in the tree.
	claimRequireTenant = Claim{
		ID: "require-tenant",
		Re: regexp.MustCompile(`\brequireTenant\b`),
		Why: "there is no such function: the tenant is configuration, and it was never read from " +
			"a request, so there is no mismatch to refuse",
	}
	// claimHeaderIsHonouredOnlyWhenItMatches is the "matched against the session
	// tenant" claim, in either spelling of the verb.
	claimHeaderIsHonouredOnlyWhenItMatches = Claim{
		ID: "header-honoured-only-when-it-matches",
		Re: regexp.MustCompile(`(?i)\b(honou?red|trusted|respected)\s+only\s+when\s+it\s+matches\b`),
		Why: "the header is not read at all. It used to be honoured on an install with no " +
			"configured tenant, which made it an instruction rather than an authorisation",
	}
	// claimStateChangingRequiresCSRF asserts a CSRF check on every
	// state-changing method.
	claimStateChangingRequiresCSRF = Claim{
		ID:  "state-changing-requires-csrf",
		Re:  regexp.MustCompile(`(?i)every\s+state-changing\s+method\s+requires\s+a\s+CSRF\s+token`),
		Why: "no method requires a token, because there is no session to bind one to",
	}
	// claimCSRFTokenOnEvery is the same claim, phrased the other way.
	claimCSRFTokenOnEvery = Claim{
		ID: "csrf-token-on-every",
		Re: regexp.MustCompile(`(?i)a\s+CSRF\s+token\s+on\s+every`),
		Why: "there is no token on any method. The console's forms render no token field, " +
			"because an empty one is worse than none",
	}
	// claimCSRFTokenRequiredOnEvery is the identity package's own error message,
	// and a document that repeats it about the product is making the same false
	// claim in prose.
	claimCSRFTokenRequiredOnEvery = Claim{
		ID: "csrf-token-required-on-every",
		Re: regexp.MustCompile(`(?i)CSRF\s+token\s+is\s+required\s+on\s+every`),
		Why: "no method requires a token. The string appears in internal/identity, which " +
			"implements the check for a sign-in that does not exist yet and is called by nothing",
	}
	// claimSessionCookieIsHTTPOnly asserts session cookies with attributes.
	claimSessionCookieIsHTTPOnly = Claim{
		ID:  "session-cookie-httponly",
		Re:  regexp.MustCompile(`(?i)session\s+cookies?\s+(is|are)\s+HttpOnly`),
		Why: "there is no session cookie: the console sets no cookie at all",
	}
	// claimSessionCookieAttributes is the exact attribute list the false claim
	// carried.
	claimSessionCookieAttributes = Claim{
		ID:  "session-cookie-attributes",
		Re:  regexp.MustCompile(`(?i)HttpOnly\s*\+\s*Secure\s*\+\s*SameSite`),
		Why: "that is a list of attributes for a cookie the console does not set",
	}
	// claimTenantHeaderIsHonoured says the tenant header is trusted.
	claimTenantHeaderIsHonoured = Claim{
		ID: "tenant-header-honoured",
		Re: regexp.MustCompile(`(?i)\bX-(Tenant|Site)-Id\b[^\n]{0,60}\b(is|are|was|were)\s+(honou?red|honou?red|trusted|respected)\b`),
		Why: "the header is not read. The tenant comes from configuration and the site from the " +
			"principal's scope",
	}
	// claimHeaderIsHonouredBeforeItsName is the same claim with the words the
	// other way round.
	claimHeaderIsHonouredBeforeItsName = Claim{
		ID: "header-honoured-before-name",
		Re: regexp.MustCompile(`(?i)\b(honou?red|honou?red|trusted|respected)\b[^\n]{0,60}\bX-(Tenant|Site)-Id\b`),
		Why: "the header is not read. The tenant comes from configuration and the site from the " +
			"principal's scope",
	}
)

// Claims is the closed set, in the order the table above reads.
var Claims = []Claim{
	claimRequireCSRF,
	claimRequireTenant,
	claimHeaderIsHonouredOnlyWhenItMatches,
	claimStateChangingRequiresCSRF,
	claimCSRFTokenOnEvery,
	claimCSRFTokenRequiredOnEvery,
	claimSessionCookieIsHTTPOnly,
	claimSessionCookieAttributes,
	claimTenantHeaderIsHonoured,
	claimHeaderIsHonouredBeforeItsName,
}

// Allowance is one file that may match one claim.
//
// Per file AND per claim, deliberately. A file that may name one absent
// control may not name another, so the list cannot grow silently.
type Allowance struct {
	// File is the repository-relative path.
	File string
	// Claim is the ID of the claim the file may contain.
	Claim string
	// Declares is the document that says the control is absent. It is where a
	// reader goes, and the gate checks that it exists.
	Declares string
	// Why is the sentence.
	Why string
}

// Allowances is the list.
//
// Every entry points at the document that says the control is ABSENT. That is
// the rule: a control that is absent may only be NAMED in the document that
// says so. A second document naming it is a second document asserting it.
var Allowances = []Allowance{
	{
		File:     "docs/status.md",
		Claim:    claimTenantHeaderIsHonoured.ID,
		Declares: "docs/threat-model.md",
		Why: "docs/status.md records the history of the header being read and then removed, " +
			"which is the only place that history belongs",
	},
	{
		File:     "docs/status.md",
		Claim:    claimRequireCSRF.ID,
		Declares: "docs/threat-model.md",
		Why: "docs/status.md records the middleware that was removed with the login, which is " +
			"the only place that history belongs",
	},
	{
		File:     "docs/status.md",
		Claim:    claimSessionCookieAttributes.ID,
		Declares: "docs/threat-model.md",
		Why: "docs/status.md records the cookie attributes the API's package doc used to claim, " +
			"which is the only place that history belongs",
	},
	{
		File:     "internal/identity/session.go",
		Claim:    claimCSRFTokenRequiredOnEvery.ID,
		Declares: "docs/threat-model.md",
		Why: "internal/identity/session.go is the function's own error message, which is a true " +
			"statement about the function it is in. The function is unwired and the console " +
			"never calls it",
	},
}

// Finding is one claim found where it may not be.
type Finding struct {
	// File is the repository-relative path.
	File string
	// Line is the 1-based line.
	Line int
	// Claim is the claim's ID.
	Claim string
	// Why is the claim's reason, carried so the message says what is wrong and
	// not only that something is.
	Why string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: this document claims a control this product does not have (%s). %s",
		f.File, f.Line, f.Claim, f.Why)
}

// ClaimByID finds a claim by its ID.
func ClaimByID(id string) (Claim, bool) {
	for _, c := range Claims {
		if c.ID == id {
			return c, true
		}
	}
	return Claim{}, false
}

// allowed reports whether a file may contain a claim.
func allowed(file, claimID string) bool {
	for _, a := range Allowances {
		if a.File == file && a.Claim == claimID {
			return true
		}
	}
	return false
}

// skipDirs are the directories the scan does not walk.
//
// `.kilo` holds other checkouts of this repository and the agent's own
// workspace, and `poc` is a separate Go module that does not import this one.
// `dist` holds build output. `node_modules` is dependency source.
var skipDirs = map[string]bool{
	".git":         true,
	".kilo":        true,
	"poc":          true,
	"dist":         true,
	"node_modules": true,
	"__pycache__":  true,
	"coverage":     true,
}

// selfPackage is this package's own directory.
//
// It holds the claim patterns, because a gate that reads itself finds itself
// on the first run.
const selfPackage = "internal/truthcheck"

// Check scans the tree and returns every claim found where it may not be.
func Check(root string) ([]Finding, error) {
	var out []Finding
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == selfPackage || strings.HasPrefix(rel, selfPackage+"/") {
			return nil
		}

		switch {
		case strings.HasSuffix(rel, ".md"):
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			out = append(out, ScanDocument(rel, string(raw))...)
		case strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go"):
			findings, parseErr := scanGoComments(rel, path)
			if parseErr != nil {
				return parseErr
			}
			out = append(out, findings...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("truthcheck: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

// ScanDocument checks one document's whole text and returns every claim found
// where it may not be.
//
// It is exported so that the gate's own tests can plant a claim in a string
// and watch the gate refuse it, without a second copy of the scanner.
func ScanDocument(file, text string) []Finding {
	var out []Finding
	for _, c := range Claims {
		if allowed(file, c.ID) {
			continue
		}
		for _, loc := range c.Re.FindAllStringIndex(text, -1) {
			out = append(out, Finding{
				File:  file,
				Line:  lineOf(text, loc[0]),
				Claim: c.ID,
				Why:   c.Why,
			})
		}
	}
	return out
}

// scanGoComments checks the COMMENTS of a Go file, and only the comments.
//
// A Go string literal is not a claim about the product: `internal/identity`
// returns an error whose text says a token is required, and that is a true
// statement about the function it is in.
//
// The comments are read with the parser rather than with a regexp, because a
// regexp that stops matching when somebody reformats a file is a gate that
// stops working without failing.
func scanGoComments(rel, path string) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		// A file that does not parse fails the build anyway, and a gate that
		// fails on it as well would report two problems for one mistake.
		//
		// The error is therefore REPORTED rather than swallowed: a file that
		// stopped parsing is a file whose comments are no longer being read,
		// and a gate that quietly stopped reading them is a gate that stopped
		// working without failing.
		return nil, fmt.Errorf("truthcheck: parse %s: %w", rel, err)
	}
	var out []Finding
	for _, group := range file.Comments {
		for _, c := range group.List {
			for _, claim := range Claims {
				if allowed(rel, claim.ID) {
					continue
				}
				if claim.Re.MatchString(c.Text) {
					out = append(out, Finding{
						File:  rel,
						Line:  fset.Position(c.Pos()).Line,
						Claim: claim.ID,
						Why:   claim.Why,
					})
				}
			}
		}
	}
	return out, nil
}

// lineOf is the 1-based line holding a byte offset.
func lineOf(s string, offset int) int {
	if offset > len(s) {
		offset = len(s)
	}
	return strings.Count(s[:offset], "\n") + 1
}
