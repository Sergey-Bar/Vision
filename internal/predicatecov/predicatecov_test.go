package predicatecov_test

// The gate, over the live tree and over fixtures.
//
// The fixture tests are not decoration. The rule this gate enforces is a
// convention stated in four places and enforced nowhere, so the only thing that
// makes it real is that the gate can be shown to catch the thing it names. Each
// fixture writes the exact violation, runs the gate, and requires the finding.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/predicatecov"
)

const repoRoot = "../.."

// exempt is the source-time predicate allow-list. Empty, and that is measured: a
// full walk of the tree finds no predicate on observed_at, received_at or
// captured_at. A gate that starts with forty exceptions is a gate whose
// exceptions nobody chose.
var exempt = map[string]predicatecov.Exemption{}

// TestNoTimePredicateReadsASourceTimeColumn is the rule over the real tree.
func TestNoTimePredicateReadsASourceTimeColumn(t *testing.T) {
	findings, errs := predicatecov.Check(predicatecov.Config{Root: repoRoot, Exempt: exempt})
	for _, e := range errs {
		t.Errorf("%v", e)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestTheGateCoversTheTree keeps the gate from passing because it walked nothing.
func TestTheGateCoversTheTree(t *testing.T) {
	n := 0
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kilo", "poc", "node_modules", "__pycache__", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if n < 120 {
		t.Fatalf("only %d non-test Go files are in the tree; the gate's walk is not reaching it", n)
	}
}

// TestAPredicateOnObservedAtIsCaught is the red-check, in the direction that
// matters: a window that reads the camera's own clock.
func TestAPredicateOnObservedAtIsCaught(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const windowQuery = ` + "`" + `
SELECT event_id FROM canonical_events
WHERE tenant_id = $1
  AND e.observed_at >= $2
  AND e.observed_at <= $3
  ORDER BY e.normalized_at ASC` + "`" + `

func Window() string { return windowQuery }
`,
	})
	findings, errs := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	for _, e := range errs {
		t.Fatalf("unexpected error: %v", e)
	}
	if !has(findings, predicatecov.RuleSourceTimePredicate) {
		t.Errorf("a window reading observed_at was not reported: %v", findings)
	}
	if n := count(findings, predicatecov.RuleSourceTimePredicate); n != 2 {
		t.Errorf("expected both bounds to be reported, got %d: %v", n, findings)
	}
}

// TestABETWEENOnASourceTimeColumnIsCaught: the same violation spelled the other way.
func TestABETWEENOnASourceTimeColumnIsCaught(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE tenant_id = $1 AND observed_at BETWEEN $2 AND $3` + "`" + `

func Q() string { return q }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleSourceTimePredicate) {
		t.Errorf("a BETWEEN on observed_at was not reported: %v", findings)
	}
}

// TestTheColumnNamedInACommentIsNotAPredicate. The rule is documented in four
// places, three of which are comments that quote the wrong column on purpose. A
// check that cannot tell a comment from a statement is a check that reports the
// documentation as a defect, and the fix everybody reaches for is deleting the
// documentation.
func TestTheColumnNamedInACommentIsNotAPredicate(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `-- A predicate on observed_at is reading the source's clock, not the world.
SELECT event_id FROM canonical_events
WHERE tenant_id = $1
  AND normalized_at >= $2
  AND normalized_at <= $3
  /* received_at is our ingest and belongs to no window either. */
  ORDER BY normalized_at` + "`" + `

func Q() string { return q }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if has(findings, predicatecov.RuleSourceTimePredicate) {
		t.Errorf("a comment quoting the wrong column was reported as a predicate: %v", findings)
	}
}

// TestBookkeepingPredicatesAreLeftAlone: a retention sweep on created_at is not a
// window predicate, and no camera can lie about when its own row was written.
func TestBookkeepingPredicatesAreLeftAlone(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE tenant_id = $1 AND normalized_at >= $2` + "`" + `

const sweep = ` + "`" + `DELETE FROM inbox_events
WHERE tenant_id = $1 AND created_at < $2` + "`" + `

func Q() string      { return q }
func Sweep() string  { return sweep }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if has(findings, predicatecov.RuleSourceTimePredicate) {
		t.Errorf("a retention sweep on created_at was reported: %v", findings)
	}
}

// TestTheWindowPredicateCannotBeDeleted is the positive direction, and it is the
// half that makes the rule mean something. Rule 1 alone is satisfied by removing
// the query: no predicate reads observed_at, because there is no predicate.
func TestTheWindowPredicateCannotBeDeleted(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const sweep = ` + "`" + `DELETE FROM inbox_events
WHERE tenant_id = $1 AND created_at < $2` + "`" + `

func Sweep() string { return sweep }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleNoWindowPredicate) {
		t.Errorf("deleting the window query was not reported: %v", findings)
	}
}

// TestTheRuleEngineMayNotReadObservedAt: the in-memory half of the rule, and the
// one with a field sitting one line away from being wrong.
func TestTheRuleEngineMayNotReadObservedAt(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE normalized_at >= $2` + "`" + `

func Q() string { return q }
`,
		"rules/engine.go": `package rules

type State struct {
	NormalizedAt time.Time
	ObservedAt   time.Time
}

func (s State) InWindow() bool { return s.ObservedAt.Unix() > 0 }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleRuleLanguageReadsObserved) {
		t.Errorf("the rule engine reading ObservedAt was not reported: %v", findings)
	}
}

// TestTheUnusedObservedFieldIsReported: a field that exists, is correctly typed and
// is documented is one line away from being used by the next person to write a
// window condition, and the day it is used nothing in the suite fails.
func TestTheUnusedObservedFieldIsReported(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE normalized_at >= $2` + "`" + `

func Q() string { return q }
`,
		"rules/engine.go": `package rules

type State struct {
	NormalizedAt time.Time
	// ObservedAt is what the source claimed. Nothing reads it.
	ObservedAt time.Time
}

func (s State) InWindow(t time.Time) bool { return !s.NormalizedAt.Before(t) }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleObservedFieldPresent) {
		t.Errorf("an ObservedAt field with no reader was not reported: %v", findings)
	}
}

func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	// The literal starts on line 3 of the fixture file, which is what the finding
	// carries, so the exemption matches the finding it is meant to excuse. An
	// exemption that matches nothing is refused too, but for a different reason,
	// and a test that cannot tell the two apart is testing the wrong one.
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE observed_at >= $2 AND normalized_at >= $3` + "`" + `

func Q() string { return q }
`,
	})
	_, errs := predicatecov.Check(predicatecov.Config{
		Root:   dir,
		Exempt: map[string]predicatecov.Exemption{"internal/correlator/c.go:3": {}},
	})
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "without saying why") {
			found = true
		}
	}
	if !found {
		t.Errorf("a blank reason on a matching exemption was not refused: %v", errs)
	}
}

func TestAnExemptionWithAReasonSuppressesTheFinding(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE observed_at >= $2 AND normalized_at >= $3` + "`" + `

func Q() string { return q }
`,
	})
	findings, errs := predicatecov.Check(predicatecov.Config{
		Root: dir,
		Exempt: map[string]predicatecov.Exemption{
			"internal/correlator/c.go:3": {
				Why: "the site is a single fixed camera whose clock is corrected once at install",
			},
		},
	})
	for _, e := range errs {
		t.Errorf("unexpected error: %v", e)
	}
	if has(findings, predicatecov.RuleSourceTimePredicate) {
		t.Errorf("a declared exemption did not suppress the finding: %v", findings)
	}
}

// TestTheGateNamesItsOwnForbiddenColumns: the exported list is a reader of the
// pattern, and a pattern that lists the columns separately from the list is two
// sources of truth.
func TestTheGateNamesItsOwnForbiddenColumns(t *testing.T) {
	want := map[string]bool{"observed_at": true, "received_at": true, "captured_at": true}
	if len(predicatecov.SourceTimeColumns) != len(want) {
		t.Fatalf("SourceTimeColumns has %d members, want %d", len(predicatecov.SourceTimeColumns), len(want))
	}
	for _, c := range predicatecov.SourceTimeColumns {
		if !want[c] {
			t.Errorf("SourceTimeColumns names %q, which is not a source-time column", c)
		}
		// Each must actually be caught, or the list documents a column the gate
		// permits. One predicate per column, written out rather than generated, so
		// the three are visibly the same shape.
		dir := fixture(t, map[string]string{
			"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT event_id FROM canonical_events
WHERE ` + c + ` >= $2 AND normalized_at >= $3` + "`" + `

func Q() string { return q }
`,
		})
		findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
		if !has(findings, predicatecov.RuleSourceTimePredicate) {
			t.Errorf("SourceTimeColumns names %q and the gate does not catch a predicate on it", c)
		}
	}
}

// TestAPredicateInAKeywordFreeFragmentIsCaught is the red-check for a bypass the
// gate shipped with.
//
// The first version required a SQL keyword before examining a literal, because a
// string with no `where` or `select` in it is not a statement. That is true of a
// whole query and false of a fragment, and the fragment is where the bypass lives:
//
//	const bounds = ` AND e.observed_at >= $2`
//	q := `SELECT ... WHERE e.tenant_id = $1` + bounds
//
// The concatenation folds to something keyword-bearing and predicate-free, the
// fragment was skipped, and a time window on the source's own clock passed the
// gate that exists to forbid one.
func TestAPredicateInAKeywordFreeFragmentIsCaught(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const bounds = ` + "`" + ` AND e.observed_at >= $2` + "`" + `

const q = ` + "`" + `SELECT event_id FROM canonical_events e
WHERE e.tenant_id = $1 AND e.normalized_at >= $3` + "`" + ` + bounds

func Q() string { return q }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleSourceTimePredicate) {
		t.Errorf("a source-time predicate in a keyword-free fragment was not reported: %v", findings)
	}
}

// TestTheWindowQueryMustNotMentionASourceTimeColumnAtAll covers the select list,
// which rule 1 cannot see because it has no comparison operator.
//
// `e.observed_at` was in that select list until this work removed it, and
// `docs/status.md` says the gate fails if the column comes back. Re-adding it
// without a comparison — the exact shape it had — was invisible.
func TestTheWindowQueryMustNotMentionASourceTimeColumnAtAll(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const q = ` + "`" + `SELECT e.event_id, e.observed_at, e.normalized_at
FROM canonical_events e
WHERE e.tenant_id = $1
  AND e.normalized_at >= $2` + "`" + `

func Q() string { return q }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleSourceTimeColumnSelected) {
		t.Errorf("a source-time column in the window query's select list was not reported: %v", findings)
	}
}

// TestAnUnrelatedNormalizedAtDoesNotSatisfyTheWindowRule: rule 2 is about the
// window query, and asking about the package instead meant one extra comparison
// anywhere in it would satisfy a gate whose only job is that the window query
// exists.
func TestAnUnrelatedNormalizedAtDoesNotSatisfyTheWindowRule(t *testing.T) {
	dir := fixture(t, map[string]string{
		"correlator/c.go": `package correlator

const unrelated = ` + "`" + `SELECT id FROM something_else WHERE normalized_at >= $1` + "`" + `

const q = ` + "`" + `SELECT event_id FROM canonical_events e WHERE e.tenant_id = $1` + "`" + `

func Q() string      { return q }
func Unrelated() string { return unrelated }
`,
	})
	findings, _ := predicatecov.Check(predicatecov.Config{Root: dir, Exempt: nil})
	if !has(findings, predicatecov.RuleNoWindowPredicate) {
		t.Errorf("an unrelated normalized_at comparison satisfied the window rule: %v", findings)
	}
}

func has(findings []predicatecov.Finding, rule string) bool { return count(findings, rule) > 0 }

func count(findings []predicatecov.Finding, rule string) int {
	n := 0
	for _, f := range findings {
		if f.Rule == rule {
			n++
		}
	}
	return n
}

// fixture writes a throwaway module tree. The two directories the rules look at
// are `internal/correlator` and `internal/rules`, so a fixture key of
// `correlator/c.go` lands at `internal/correlator/c.go` and nowhere else — a
// helper that also wrote it at the root would report every finding twice and the
// fixture would pass for the wrong reason.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"internal/correlator", "internal/rules"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for rel, body := range files {
		top, name, ok := strings.Cut(rel, "/")
		if !ok {
			t.Fatalf("fixture key %q has no package directory", rel)
		}
		dst := filepath.Join(dir, "internal", filepath.FromSlash(top), name)
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return dir
}
