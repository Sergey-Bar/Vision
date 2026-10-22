package truthcheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sergey-Bar/vision/internal/truthcheck"
)

// repoRoot walks up from the test's directory to the module root.
//
// A fixed relative path would break the moment the package moved, and a test
// that reads the wrong tree is worse than no test: it reports green about a
// document it never opened.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// TestNoDocumentClaimsAControlThisProductDoesNotHave is the DoD.
//
// It fails when a document says this product has a control it does not have.
func TestNoDocumentClaimsAControlThisProductDoesNotHave(t *testing.T) {
	findings, err := truthcheck.Check(repoRoot(t))
	if err != nil {
		t.Fatalf("truthcheck: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestTheAllowListIsNotStale is the floor on the allow-list's own honesty.
//
// An allowance that permits a claim no file makes is a permission with no
// reason, and a permission with no reason is how the list grows silently. The
// entry has to go when the claim goes, or be given a reason that is still true.
func TestTheAllowListIsNotStale(t *testing.T) {
	root := repoRoot(t)
	for _, a := range truthcheck.Allowances {
		claim, ok := truthcheck.ClaimByID(a.Claim)
		if !ok {
			t.Errorf("the allow-list permits %q, which is not a claim in the closed set. "+
				"Either the claim was removed or the entry names it wrong", a.Claim)
			continue
		}
		if !claim.Re.MatchString(readFile(t, root, a.File)) {
			t.Errorf("the allow-list permits %s to contain %q, and it no longer does. "+
				"Delete the entry: a permission with no reason is how the list grows silently",
				a.File, claim.ID)
		}
	}
}

// TestEveryAllowancePointsAtTheDocumentThatDeclaresTheControlAbsent is the rule
// the allow-list exists to enforce.
//
// An allowance may name a control in one place: the document that says the
// control is absent. If the entry points at nothing, it is a bare permission.
func TestEveryAllowancePointsAtTheDocumentThatDeclaresTheControlAbsent(t *testing.T) {
	root := repoRoot(t)
	for _, a := range truthcheck.Allowances {
		if a.Declares == "" {
			t.Errorf("the allow-list entry for %s in %s does not point at the document that "+
				"declares the control absent", a.Claim, a.File)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(a.Declares))); err != nil {
			t.Errorf("the allow-list entry for %s in %s points at %s, which is not in this "+
				"repository", a.Claim, a.File, a.Declares)
		}
		if a.Why == "" {
			t.Errorf("the allow-list entry for %s in %s has no reason", a.Claim, a.File)
		}
	}
}

// TestTheClosedSetIsNotEmpty is the floor on the claim list.
//
// The list is the gate's whole content. A gate whose content is empty is a
// gate that passes, and a gate that passes because it has nothing to check is
// indistinguishable from a gate that was deleted.
func TestTheClosedSetIsNotEmpty(t *testing.T) {
	if len(truthcheck.Claims) < 5 {
		t.Fatalf("the closed set holds %d claims, so the gate is checking almost nothing. "+
			"Every claim names a control this product does not have, and each one was a false "+
			"claim in a document this repository shipped", len(truthcheck.Claims))
	}
	seen := map[string]bool{}
	for _, c := range truthcheck.Claims {
		if c.Re == nil {
			t.Errorf("the claim %q has no pattern, so it can never be found", c.ID)
		}
		if c.Why == "" {
			t.Errorf("the claim %q has no reason, so a reader of the failure would not know "+
				"what this product does instead", c.ID)
		}
		if seen[c.ID] {
			t.Errorf("the claim id %q appears twice", c.ID)
		}
		seen[c.ID] = true
	}
}

// TestTheGateFailsOnAClaim is the red-check for this gate.
//
// It proves the gate is a gate: a document carrying a false claim is refused,
// rather than the check quietly passing because it compared nothing.
func TestTheGateFailsOnAClaim(t *testing.T) {
	findings := truthcheck.ScanDocument("docs/roadmap/example.md",
		"Every state-changing method requires a CSRF token, and every session cookie is HttpOnly.\n")
	if len(findings) == 0 {
		t.Fatal("a document claiming a CSRF check and a session cookie produced no finding, so " +
			"the gate is not a gate: it would pass on the very document it was written for")
	}
	found := map[string]bool{}
	for _, f := range findings {
		found[f.Claim] = true
	}
	for _, want := range []string{"state-changing-requires-csrf", "session-cookie-httponly"} {
		if !found[want] {
			t.Errorf("the findings do not name the claim %q that was planted: %v", want, findings)
		}
	}
}

// TestTheGateIgnoresAMentionThatIsNotAClaim is the other half of the red-check.
//
// `X-Tenant-Id` on its own is not a claim: `docs/status.md` and `web/scope.go`
// both explain at length why the header used to be read and why it no longer
// is, and that explanation is the document being honest. What is a claim is
// the header being TRUSTED.
func TestTheGateIgnoresAMentionThatIsNotAClaim(t *testing.T) {
	findings := truthcheck.ScanDocument("docs/roadmap/example.md",
		"The console server used to read `X-Tenant-ID` and `X-Site-IDs` out of the request.\n"+
			"The header is not read now, and the reason is in the threat model.\n")
	if len(findings) != 0 {
		t.Errorf("an honest explanation of a removed control produced %v. A gate that fired on "+
			"the mention itself would fire on every document that explains the removal, and would "+
			"be switched off", findings)
	}
}

// TestTheGateCatchesATrustedHeader is the positive half of that red-check.
//
// The same header, described as trusted, is a claim, and the gate has to catch
// it in either word order.
func TestTheGateCatchesATrustedHeader(t *testing.T) {
	for _, text := range []string{
		"A `X-Tenant-Id` header is honoured only when it matches the session tenant.\n",
		"The `X-Tenant-Id` header is honoured and a mismatch is 403.\n",
		"A header is honoured only when it matches the `X-Tenant-Id` session tenant.\n",
	} {
		findings := truthcheck.ScanDocument("docs/roadmap/example.md", text)
		if len(findings) == 0 {
			t.Errorf("a document claiming the header is trusted produced no finding: %q", text)
		}
	}
}

// readFile is the file's text, or a failure.
func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}
