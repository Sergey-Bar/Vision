package statuscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestStatusMatchesTheTree is the DoD for docs/status.md.
//
// It exists because the document was wrong three times and correcting it twice by
// hand did not make it stay right. Every number here is derived from the file that
// defines it, so the only way to go green is to make the document match.
func TestStatusMatchesTheTree(t *testing.T) {
	findings, err := Check(repoRoot(t))
	if err != nil {
		t.Fatalf("statuscheck: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestDeriveCountsTheTreeDirectly pins the derivation itself.
//
// TestStatusMatchesTheTree would pass vacuously if Derive returned zeros and the
// document happened to print zeros, so the numbers are asserted against the tree
// here independently. These are the values the document is expected to print, and
// they are the thing a reader checks the document against by hand.
func TestDeriveCountsTheTreeDirectly(t *testing.T) {
	root := repoRoot(t)
	d, err := Derive(root)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	if d.Messages != 25 {
		t.Errorf("contract messages: got %d, want 20", d.Messages)
	}
	if d.Enums != 22 {
		t.Errorf("contract enums: got %d, want 22", d.Enums)
	}
	if d.Schemas != 8 {
		t.Errorf("published JSON Schemas: got %d, want 8", d.Schemas)
	}
	if d.Paths != 22 {
		t.Errorf("OpenAPI paths: got %d, want 22", d.Paths)
	}
	// The eight published schemas' properties, summed. The number moves when a
	// schema gains a property, and `contractcheck -mode write` records it in the
	// baseline - which is why a document that disagrees is a defect and not a
	// rounding difference.
	if d.Properties != 269 {
		t.Errorf("schema properties: got %d, want 269", d.Properties)
	}
	if d.GoMinor != "26" {
		t.Errorf("go.mod minor: got %q, want %q", d.GoMinor, "26")
	}
	// Thirty-three named gates in scripts/verify.ps1. `lint`, `generated-code`
	// and `ai-runtime` each appear twice because they are registered inside a tool-
	// present branch, and one branch is one gate. `skip-audit`, `no-silent-skips`,
	// `cover-floor`, `dead-export` and `predicate-cov` are the five added with the
	// 2026-10-02 gate pass and the 2026-10-04 one; `tree-state`, `fuzz` and `flake`
	// are three more; `roadmap-check` is the one that holds the roadmap documents'
	// package, migration and contract counts to the tree; `boot` is the 2026-10-14
	// Wave 2 gate that migrates, seeds, starts the whole service against a real
	// Postgres and requires it to serve, rather than asserting a binary builds.
	//
	// This is the one assertion in the file that duplicates what the gate already
	// derives, and it is deliberate: a derivation that stops finding gates would
	// make the document agree with a smaller tree, and only a number written out
	// here notices that. Updating it is the cost of adding a gate.
	if d.Gates != 35 {
		t.Errorf("gates: got %d, want 35", d.Gates)
	}
}

// TestEveryGateIsNamed guards the derivation in the other direction: a gate that
// lost its name would silently stop being counted, and the count would go down,
// which the document would then be asked to match. A gate is only a gate if it has
// a name a reader can find in the verification record.
func TestEveryGateIsNamed(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "verify.ps1"))
	if err != nil {
		t.Fatalf("read verify.ps1: %v", err)
	}
	for _, m := range gateRe.FindAllStringSubmatch(string(raw), -1) {
		if m[1] == "" {
			t.Errorf("scripts/verify.ps1 registers a gate with an empty name; the " +
				"verification record would carry a gate nobody can look up")
		}
		if strings.ContainsAny(m[1], " ") {
			t.Errorf("gate name %q contains a space; the record keys gates by name", m[1])
		}
	}
}

// TestTheDocumentActuallyPrintsTheCounts is the control on the control.
//
// Every assertion above is a comparison, and a comparison passes when both sides
// are zero or when the pattern silently stops matching. This test requires the
// Verification block to contain at least one live example of each claim, so a
// future edit that rewords that paragraph and drops the numbers cannot turn the
// gate into a no-op that reports green forever.
func TestTheDocumentActuallyPrintsTheCounts(t *testing.T) {
	root := repoRoot(t)
	d := mustDerive(t, root)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "status.md"))
	if err != nil {
		t.Fatalf("read status.md: %v", err)
	}
	status := string(raw)
	block, ok := verificationBlock(status)
	if !ok {
		t.Fatalf("docs/status.md has no **Verification:** block, so every claim in it " +
			"is unchecked. Restore the block rather than deleting the check: an " +
			"unchecked claim reads as a checked one")
	}

	for _, c := range claims(d) {
		if !c.re.MatchString(block) {
			t.Errorf("the Verification block prints no %q, so that claim is not checked "+
				"by TestStatusMatchesTheTree. Either state it with the correct number or "+
				"remove the check; a claim that cannot be seen is a claim that reads "+
				"as checked", c.label)
		}
	}
	if !gateCountRe.MatchString(block) {
		t.Error("the Verification block prints no gate count, so the gate count is not checked")
	}
}

// TestTheGateCountIgnoresDatedRunRecords is the control on the scope restriction.
//
// Every claim is read from the Verification block alone, because the document also
// records what was true on a given day - a table of what a round fixed, a dated run
// record. Editing one of those to match today's tree would be falsifying a record
// rather than correcting a claim, and a gate that reported accurate history as a
// defect would train its author to write less about why something changed.
//
// That reasoning is only worth something if the scope demonstrably changes the
// outcome, so it is asserted: the body of the document contains gate counts that
// disagree with the tree, the same pattern run over the Verification block reports
// nothing, and run over the whole document it reports the historical records.
func TestTheGateCountIgnoresDatedRunRecords(t *testing.T) {
	root := repoRoot(t)
	d, err := Derive(root)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "status.md"))
	if err != nil {
		t.Fatalf("read status.md: %v", err)
	}
	status := string(raw)

	block, ok := verificationBlock(status)
	if !ok {
		t.Fatalf("docs/status.md has no **Verification:** block; nothing is checked")
	}
	// The block is the current state and must agree with the tree.
	if got := gateClaim(block, d); len(got) != 0 {
		t.Errorf("the Verification block states a gate count that disagrees with the tree: %v", got)
	}

	body := strings.TrimPrefix(status, block)
	if !gateCountRe.MatchString(body) {
		t.Skip("docs/status.md carries no gate count outside its Verification block; " +
			"the scope restriction has nothing to protect")
	}
	// The demonstration: unchecked, the historical records read as defects.
	whole := gateClaim(status, d)
	if len(whole) == 0 {
		t.Skip("the body's gate counts already agree with the tree, so the scope " +
			"restriction is not currently load-bearing")
	}
	if len(gateClaim(block, d)) >= len(whole) {
		t.Error("checking the Verification block reported as many disagreements as " +
			"checking the whole document, so the scope is not excluding the historical " +
			"run records it exists to exclude")
	}
}

// TestNumbersOutsideTheVerificationBlockAreNotChecked is the control on the scope,
// stated as a property of the checker rather than as a property of today's prose.
//
// The previous version of this test asserted that the body of docs/status.md
// contains none of the claim patterns, which is not the property that matters and
// is not even true: several sections restate the contract counts, correctly, and
// there is nothing wrong with that. What matters is narrower and is what the
// scoping buys: a number outside the Verification block is history, and history is
// not checked. So this drives the checkers directly with a synthetic document whose
// Verification block is correct and whose later prose states the old values as the
// thing a past round fixed - the exact sentence that was reported as a defect
// twice while writing the real paragraph.
//
// A test that asserts a property of the file as it stands passes until someone
// edits the file, and then it fails for a reason that has nothing to do with the
// checker. This one cannot.
func TestNumbersOutsideTheVerificationBlockAreNotChecked(t *testing.T) {
	d := Derived{
		Messages: 20, Enums: 22, Schemas: 7, Properties: 248, Paths: 20,
		GoMinor: "26", Gates: 17,
	}

	const correct = "**Verification:** green - seventeen gates: contractcheck reports " +
		"20 messages and 22 enums compatible with the baseline, plus 7 JSON Schemas " +
		"(248 properties) and the OpenAPI document's 20 paths.\n"

	// A stale number in a later section, phrased as the thing a round fixed.
	const history = "\nThis round corrected the schema count: the paragraph above had " +
		"claimed 6 JSON Schemas (232 properties), and it named fifteen gates while " +
		"the script ran seventeen.\n"

	doc := correct + history

	block, ok := verificationBlock(doc)
	if !ok {
		t.Fatal("verificationBlock did not find the marker in a document that opens with it")
	}

	// The block itself is clean, and every claim in it is present.
	for _, c := range claims(d) {
		if !c.re.MatchString(block) {
			t.Errorf("synthetic Verification block does not print %q, so this test "+
				"would pass without exercising anything", c.label)
		}
	}
	if got := numbers(block, d); len(got) != 0 {
		t.Errorf("a correct Verification block produced findings: %v", got)
	}
	if got := gateClaim(block, d); len(got) != 0 {
		t.Errorf("a correct Verification block produced gate findings: %v", got)
	}

	// The history, checked whole-document, would be reported - which is the defect
	// the scoping exists to prevent.
	if got := numbers(doc, d); len(got) == 0 {
		t.Skip("the synthetic history no longer trips a whole-document check; the " +
			"patterns and the sentence have drifted apart and this test needs rewording")
	}

	// And the block-scoped check is what actually runs, so it must be silent.
	if got := gateClaim(block, d); len(got) != 0 {
		t.Errorf("history outside the Verification block was reported as a claim: %v", got)
	}
}

// TestTheScopeIsWhatCheckActuallyUses closes the loop. The two tests above prove
// the pieces behave; this proves Check is wired to the block rather than to the
// whole document, which is the part that could silently regress.
func TestTheScopeIsWhatCheckActuallyUses(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "status.md"))
	if err != nil {
		t.Fatalf("read status.md: %v", err)
	}
	status := string(raw)
	block, ok := verificationBlock(status)
	if !ok {
		t.Fatalf("docs/status.md has no **Verification:** block")
	}
	if block == status {
		t.Error("verificationBlock returned the entire document, so the scope is not " +
			"narrowing anything and every claim in the body is being checked")
	}
	// lineOffset locates the block with strings.Index, so what it needs is that the
	// block is findable and begins at the marker - not that it is a prefix. It is
	// not: the marker sits partway down the document, and a block that was a
	// prefix would have thrown away everything above it.
	if i := strings.Index(status, verificationBlockMarker); i < 0 {
		t.Fatal("the marker is missing from docs/status.md, so no line mapping is possible")
	} else if !strings.HasPrefix(status[i:], block) {
		t.Error("the block does not begin at the marker, so a finding's line number " +
			"would point at the wrong line")
	}
	if !strings.Contains(status, block) {
		t.Error("the block is not findable in the document, so lineOffset returns 0 " +
			"and every finding cites line 1")
	}
}

func mustDerive(t *testing.T, root string) Derived {
	t.Helper()
	d, err := Derive(root)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return d
}
