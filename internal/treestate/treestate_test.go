package treestate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/treestate"
)

// The four assertions are tested in the direction that matters: each one is fed a
// fixture that is the defect it exists to catch, and each is required to report it.
//
// The fixtures come first and the live tree comes last, on purpose. A gate that has
// only ever run against a clean tree has never been shown to fail, so the first run
// of this gate in anger would be its first run at all — which is how
// `scripts/verify.ps1` came to describe golangci-lint and `-race` as gates while
// neither had ever executed.

// ---------------------------------------------------------------------------
// Assertion 1
// ---------------------------------------------------------------------------

func TestAMergeInProgressIsAFinding(t *testing.T) {
	got := treestate.CheckWorktrees([]treestate.WorktreeState{
		{Path: "/repo/.kilo/worktrees/caller-audit", MergeInProgress: true},
	})
	if len(got) != 1 {
		t.Fatalf("a worktree with a MERGE_HEAD produced %d findings, want 1: %v", len(got), got)
	}
	if got[0].Assertion != 1 {
		t.Errorf("finding is assertion %d, want 1", got[0].Assertion)
	}
	// The finding has to name the worktree, or the reader has six directories to
	// choose between.
	if !strings.Contains(got[0].Subject, "caller-audit") {
		t.Errorf("the finding does not name the worktree: %q", got[0].Subject)
	}
	// And it has to say what to do about it, because the two remedies are
	// opposites and picking the wrong one deletes a resolution.
	if !strings.Contains(got[0].Detail, "merge --abort") {
		t.Errorf("the finding does not offer the abandon remedy: %q", got[0].Detail)
	}
}

func TestStagedChangesWithNoMergeAreAFinding(t *testing.T) {
	got := treestate.CheckWorktrees([]treestate.WorktreeState{
		{Path: "/repo/.kilo/worktrees/footage-measurement", IndexDiffersFromHead: true},
	})
	if len(got) != 1 {
		t.Fatalf("staged changes with no merge produced %d findings, want 1: %v", len(got), got)
	}
	// This is the shape that actually happened and it is the quieter one, so the
	// finding has to point at assertion 2 rather than only at the index.
	if !strings.Contains(got[0].Detail, "assertion 2") {
		t.Errorf("the finding does not connect staged changes to the mutation assertion: %q",
			got[0].Detail)
	}
}

// TestAMainWorktreeWithWorkInProgressIsNotAFinding is the other half of that rule.
//
// This gate is run while editing, so a finding on the main worktree's dirt would
// make it fail on every keystroke, and a gate that fails while you are working is a
// gate that gets run with `--no-verify`. The main worktree's MERGE_HEAD is still a
// finding, and the test below proves that half.
func TestAMainWorktreeWithWorkInProgressIsNotAFinding(t *testing.T) {
	got := treestate.CheckWorktrees([]treestate.WorktreeState{
		{Path: "/repo", IndexDiffersFromHead: true, Main: true},
	})
	if len(got) != 0 {
		t.Errorf("the main worktree's uncommitted work was reported: %v", got)
	}
}

func TestAMainWorktreeStuckMidMergeIsStillAFinding(t *testing.T) {
	got := treestate.CheckWorktrees([]treestate.WorktreeState{
		{Path: "/repo", MergeInProgress: true, Main: true},
	})
	if len(got) != 1 {
		t.Fatalf("a main worktree with a MERGE_HEAD produced %d findings, want 1", len(got))
	}
}

func TestADetachedWorktreeSaysWhatRemovingItCosts(t *testing.T) {
	got := treestate.CheckWorktrees([]treestate.WorktreeState{
		{Path: "/repo/.kilo/worktrees/pattern-silene", IndexDiffersFromHead: true, Detached: true},
	})
	if len(got) != 1 {
		t.Fatalf("a detached dirty worktree produced %d findings, want 1", len(got))
	}
	if !strings.Contains(got[0].Detail, "reachable from a ref") {
		t.Errorf("the finding does not say what a detached worktree costs: %q", got[0].Detail)
	}
}

func TestACleanWorktreeIsNotAFinding(t *testing.T) {
	got := treestate.CheckWorktrees([]treestate.WorktreeState{
		{Path: "/repo"},
		{Path: "/repo/.kilo/worktrees/encoding-repair"},
	})
	if len(got) != 0 {
		t.Errorf("a clean set of worktrees produced findings: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Assertion 2
// ---------------------------------------------------------------------------

// TestAnAppliedMutationIsAFinding is the assertion that closes
// `poc/active-monitoring/internal/soak/crop.go` with `ct.GatedOut++` replaced by a
// comment, and `internal/store/searchcards.go` with `1=0 -- REDCHECK`. Both were in
// the tree with a fully green suite. The fixture is the second one, verbatim.
func TestAnAppliedMutationIsAFinding(t *testing.T) {
	const from = " AND (lower(v.summary) LIKE ? OR lower(g.note) LIKE ?)"
	const to = "OR 1=0 -- REDCHECK: the judgement of the reviewer is unretrievable"
	const src = "poc/active-monitoring/internal/store/searchcards.go"

	files := map[string]string{
		src: "\t\twhere += \" AND (lower(v.summary) LIKE ? OR 1=0 -- REDCHECK: the judgement of the reviewer is unretrievable)\"\n",
	}
	present := map[string]bool{src: true}
	muts := []treestate.Mutation{{
		Table:   "poc/active-monitoring/scripts/verify.ps1",
		Control: "search: the reviewer's note is searchable",
		From:    from,
		To:      to,
	}}

	got := treestate.CheckMutationsAbsent(files, present, muts)
	if len(got) != 1 {
		t.Fatalf("an applied mutation produced %d findings, want 1: %v", len(got), got)
	}
	if got[0].Assertion != 2 {
		t.Errorf("finding is assertion %d, want 2", got[0].Assertion)
	}
	// The finding must name the control, not just print the broken line. A gate
	// that hands the reader a string to grep for is a gate that has not done the
	// work it exists to do.
	if !strings.Contains(got[0].Subject, "reviewer") {
		t.Errorf("the finding does not name the control: %q", got[0].Subject)
	}
	if !strings.Contains(got[0].Detail, "searchcards.go") {
		t.Errorf("the finding does not name the file: %q", got[0].Detail)
	}
	// And it must tell the reader not to re-run the gate, because the gate
	// mutates files and running it on a tree that is already mutated is how a
	// second unrelated file gets mutated too.
	if !strings.Contains(got[0].Detail, "do not run the gate again") {
		t.Errorf("the finding does not warn against re-running: %q", got[0].Detail)
	}

	// Putting the correct line back makes it disappear. This is the direction that
	// decides whether the gate is usable at all: a finding that does not clear on
	// repair is a finding people learn to ignore.
	files[src] += "\t\twhere += \"" + from + "\"\n"
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 0 {
		t.Errorf("a restored control was still reported: %v", got)
	}
}

// TestAMutationTheTableClaimsForAnotherFileIsStillFound: the table's `file` field is
// what a red-check run trusts. A mutation applied somewhere ELSE than the table says
// is the case where trusting it loses the control, so this assertion ignores the
// table's file field entirely and searches every file.
func TestAMutationTheTableClaimsForAnotherFileIsStillFound(t *testing.T) {
	const to = "1=0 -- REDCHECK: the judgement of the reviewer is unretrievable"
	files := map[string]string{"internal/store/searchcards.go": to}
	present := map[string]bool{"internal/store/searchcards.go": true}
	muts := []treestate.Mutation{{
		Table: "poc/active-monitoring/scripts/verify.ps1", Control: "c",
		From: "lower(g.note) LIKE ?", To: to,
	}}
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 1 {
		t.Errorf("a mutation in an unexpected file produced %d findings, want 1", len(got))
	}
}

// TestAGateThatQuotesItsOwnMutationsIsNotItselfAFinding is the fixture for the third
// false positive this gate has found on its own live run.
//
// This file contains four of the tables' `to` strings verbatim, because testing a
// detector requires writing down the thing it detects. Every fixture that quotes a
// `to` without also quoting its `from` is, to a mutation search, an applied control.
//
// It is checked here as an exclusion rather than in the assertion itself: the
// live-tree test deletes `internal/treestate/` from the file set and says why. This
// fixture records the shape -- a quoted `to`, an absent `from`, and the correct
// answer being silence once the package is excluded.
func TestAGateThatQuotesItsOwnMutationsIsNotItselfAFinding(t *testing.T) {
	const to = "return DegradedTimeout"
	files := map[string]string{
		"internal/treestate/treestate_test.go": "		To:   \"" + to + "\",\n",
	}
	present := map[string]bool{"internal/treestate/treestate_test.go": true}
	muts := []treestate.Mutation{{
		Table: "scripts/verify.ps1", Control: "c",
		From: "	return DegradedAbsent", To: to,
	}}

	// Unfiltered, the search does match the fixture -- which is the defect, stated
	// as a fact rather than asserted away.
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 1 {
		t.Fatalf("an unfiltered search found %d findings, want 1; this fixture is what the "+
			"live-tree test has to exclude, so it has to actually match", len(got))
	}

	// And the live-tree test's remedy is a path prefix, which is what makes this
	// close to a real control rather than a special case.
	for p := range files {
		if strings.HasPrefix(p, "internal/treestate/") {
			delete(files, p)
			delete(present, p)
		}
	}
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 0 {
		t.Errorf("the gate's own package was still searched: %v", got)
	}
}

// TestAMutationThatOccursHarmlesslyElsewhereIsNotAFinding is the fixture for a
// false positive this gate found on its own first live run.
//
// internal/rules/match.go contains `return DegradedTimeout` legitimately, inside the
// switch that maps a reason string to its constant -- and that string is the
// mutation of a control declared against a different file. The mutation was applied
// nowhere. A gate that reports it teaches its reader that a clean tree is dirty,
// which is the fastest way to make a gate decorative.
func TestAMutationStringThatOccursHarmlesslyElsewhereIsNotAFinding(t *testing.T) {
	// The real file has BOTH lines: the switch maps every reason string to its
	// constant, so `return DegradedTimeout` sits beside `return DegradedAbsent`.
	files := map[string]string{
		"internal/rules/match.go": "	case DegradedTimeout:\n\t\treturn DegradedTimeout\n" +
			"	case DegradedAbsent:\n\t\treturn DegradedAbsent\n",
	}
	present := map[string]bool{"internal/rules/match.go": true}
	muts := []treestate.Mutation{{
		Table: "scripts/verify.ps1", Control: "c",
		From: "	return DegradedAbsent", To: "	return DegradedTimeout",
	}}
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 0 {
		t.Errorf("a mutation string occurring harmlessly was reported: %v", got)
	}
}

// TestAMutationThatIsAPrefixOfTheCorrectLineIsNotAFinding: the presence-mode entry's
// mutation is `{Name: "SP_POC_ZONES", kind: kindString,` and the correct line is that
// plus ` structural: true, structuralVal: "",`. The broken line is a PREFIX of the
// correct one, so the correct line contains the broken one and the broken one can
// never be absent while the correct one is present. Only the from-absent half of the
// conjunction saves this, which is why both halves are there.
func TestAMutationThatIsAPrefixOfTheCorrectLineIsNotAFinding(t *testing.T) {
	const from = `{Name: "SP_POC_ZONES", kind: kindString, structural: true, structuralVal: "",`
	files := map[string]string{
		"poc/active-monitoring/internal/config/config.go": "\t" + from + "\n",
	}
	present := map[string]bool{"poc/active-monitoring/internal/config/config.go": true}
	muts := []treestate.Mutation{{
		Table: "poc/active-monitoring/scripts/verify.ps1", Control: "c",
		From: from, To: `{Name: "SP_POC_ZONES", kind: kindString,`,
	}}
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 0 {
		t.Errorf("a prefix-mutation over an intact line was reported: %v", got)
	}
}

// TestATableIsNeverSearchedForItsOwnMutations: a red-check table contains every
// mutation string BY DEFINITION -- `to = '...'` is how the gate spells the broken
// version -- so searching a table for its own mutations matches every entry in it.
// A run of the mutation-only version of this assertion reported all thirty of the
// POC table's controls as "APPLIED IN poc/active-monitoring/scripts/verify.ps1",
// which is the shape of a gate firing on everything.
func TestATableIsNeverSearchedForItsOwnMutations(t *testing.T) {
	const tablePath = "scripts/verify.ps1"
	const to = `if false && p.Password != "" { // REDCHECK RC-8`
	files := map[string]string{tablePath: "\tto   = '" + to + "'\n"}
	present := map[string]bool{tablePath: true}
	muts := []treestate.Mutation{{
		Table: tablePath, Control: "a password reaches the connection string",
		From: `	if p.Password != "" {`, To: to,
	}}

	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 0 {
		t.Errorf("a table containing its own mutation was reported: %v", got)
	}

	// And the same mutation in a file that is NOT the table is still a finding,
	// which is the direction that matters. An exclusion that over-reaches is as
	// broken as one that under-reaches.
	files["internal/dbx/dsn.go"] = "\t" + to + "\n"
	present["internal/dbx/dsn.go"] = true
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 1 {
		t.Errorf("the same mutation in a source file produced %d findings, want 1: %v", len(got), got)
	}
}

// TestAnEmptyFieldIsItselfAFinding: an entry with no `to =` cannot be searched for,
// and an entry with no `from =` cannot be told from a refactor. Either way the
// control is not covered, and that is the quiet-pass defect this whole package
// exists to prevent, so both are reported rather than skipped.
func TestAnEmptyFieldIsItselfAFinding(t *testing.T) {
	files := map[string]string{"a.go": "package a"}
	present := map[string]bool{"a.go": true}

	got := treestate.CheckMutationsAbsent(files, present,
		[]treestate.Mutation{{Table: "scripts/verify.ps1", Control: "no mutation", From: "x", To: "  "}})
	if len(got) != 1 {
		t.Fatalf("an empty `to` produced %d findings, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0].Detail, "EMPTY") {
		t.Errorf("the finding does not say the mutation string was empty: %q", got[0].Detail)
	}

	got = treestate.CheckMutationsAbsent(files, present,
		[]treestate.Mutation{{Table: "scripts/verify.ps1", Control: "no from", From: "  ", To: "y"}})
	if len(got) != 1 {
		t.Fatalf("an empty `from` produced %d findings, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0].Detail, "`from =`") {
		t.Errorf("the finding does not say which field was empty: %q", got[0].Detail)
	}
}

func TestACleanTreeHasNoAppliedMutation(t *testing.T) {
	files := map[string]string{
		"crop.go": "\tif !cp.Gate.Due(f.Seq, f.AtNS) {\n\t\tct.GatedOut++\n\t\treturn nil, ct\n\t}\n",
	}
	present := map[string]bool{"crop.go": true}
	muts := []treestate.Mutation{{
		Table:   "poc/active-monitoring/scripts/verify.ps1",
		Control: "crop pass: the rate gate runs BEFORE the novelty engine",
		From:    "if !cp.Gate.Due(f.Seq, f.AtNS) {",
		To:      "if false { // REDCHECK: novelty is paid on every frame and the answer discarded",
	}}
	if got := treestate.CheckMutationsAbsent(files, present, muts); len(got) != 0 {
		t.Errorf("a restored control was reported as mutated: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Assertion 3
// ---------------------------------------------------------------------------

func TestAnUnlandedBranchWithNoReasonIsAFinding(t *testing.T) {
	got := treestate.CheckBranchesLanded("contract", []treestate.BranchState{
		{Name: "footage-measurement", CommitsAhead: 2},
		{Name: "contract", CommitsAhead: 0},
	})
	if len(got) != 1 {
		t.Fatalf("an unlanded branch produced %d findings, want 1: %v", len(got), got)
	}
	if got[0].Subject != "footage-measurement" {
		t.Errorf("finding names %q, want footage-measurement", got[0].Subject)
	}
	if !strings.Contains(got[0].Detail, "2 commit(s)") {
		t.Errorf("the finding does not say how many commits are unlanded: %q", got[0].Detail)
	}
}

func TestADeclinedBranchIsNotAFinding(t *testing.T) {
	got := treestate.CheckBranchesLanded("contract", []treestate.BranchState{
		{Name: "proposal/01-adapter-probe", CommitsAhead: 40,
			Declined: "a proposal is a document, not work in flight; see ROADMAP.md"},
	})
	if len(got) != 0 {
		t.Errorf("a declined branch was reported: %v", got)
	}
}

func TestTheBaseBranchItselfIsNotAFinding(t *testing.T) {
	// `contract` is compared against nothing, so it can never be "ahead of itself".
	// This is a guard against a plumbing mistake rather than a behaviour claim.
	got := treestate.CheckBranchesLanded("contract", []treestate.BranchState{
		{Name: "contract", CommitsAhead: 99},
	})
	if len(got) != 0 {
		t.Errorf("the base branch was reported as unlanded: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Assertion 4
// ---------------------------------------------------------------------------

func TestAControlWithNoTranscriptIsAFinding(t *testing.T) {
	got := treestate.CheckTranscripts([]treestate.Control{{
		Name:       "a password carried in the DSN reaches the connection string",
		Table:      "scripts/verify.ps1",
		Transcript: "a table that mentions other controls but not this one",
	}})
	if len(got) != 1 {
		t.Fatalf("a control with no transcript produced %d findings, want 1: %v", len(got), got)
	}
	if got[0].Assertion != 4 {
		t.Errorf("finding is assertion %d, want 4", got[0].Assertion)
	}
}

func TestAControlWithATranscriptIsNotAFinding(t *testing.T) {
	name := "the configured role reaches the connection string"
	got := treestate.CheckTranscripts([]treestate.Control{{
		Name: name, Table: "scripts/verify.ps1",
		Transcript: "| the configured role reaches the connection string | `if c.User != \"\"` becomes ... |",
	}})
	if len(got) != 0 {
		t.Errorf("a transcribed control was reported: %v", got)
	}
}

func TestAnUnnamedControlIsAFinding(t *testing.T) {
	got := treestate.CheckTranscripts([]treestate.Control{{
		Table: "scripts/verify.ps1", Transcript: "anything",
	}})
	if len(got) != 1 {
		t.Fatalf("an unnamed control produced %d findings, want 1", len(got))
	}
	if !strings.Contains(got[0].Detail, "no `name`") {
		t.Errorf("the finding does not say the control was unnamed: %q", got[0].Detail)
	}
}

// ---------------------------------------------------------------------------
// The report
// ---------------------------------------------------------------------------

func TestTheReportIsStableAndSorted(t *testing.T) {
	got := treestate.CheckBranchesLanded("contract", []treestate.BranchState{
		{Name: "zebra", CommitsAhead: 1},
		{Name: "alpha", CommitsAhead: 1},
	})
	first := treestate.Report(got)
	second := treestate.Report(got)
	if first != second {
		t.Errorf("the report is not stable across two renders of the same findings")
	}
	if strings.Index(first, "alpha") > strings.Index(first, "zebra") {
		t.Errorf("the report is not sorted by subject:\n%s", first)
	}
}

func TestACleanReportIsEmpty(t *testing.T) {
	if r := treestate.Report(nil); r != "" {
		t.Errorf("a clean report rendered %q, want empty", r)
	}
}

// ---------------------------------------------------------------------------
// The red-check table parser
// ---------------------------------------------------------------------------

// TestAMultiLineMutationIsParsed is the fixture for a defect this gate found on its
// own first run.
//
// Two of the POC table's mutations span three lines, because they replace a
// multi-line Go `if` block. A line-oriented regex read 76 of 77 entries and
// reported the TABLE as malformed — which pointed at the table instead of at the
// gate, and would have had somebody editing a correct table to satisfy a broken
// parser. The fixture is that exact shape.
func TestAMultiLineMutationIsParsed(t *testing.T) {
	const table = `  $controls = @(
    @{
      name = 'a single-line entry'
      from = 'if x {'
      to   = 'if false { // REDCHECK'
    },
    @{
      name = 'a multi-line entry'
      from = 'if det != nil {
			det.Detected++
		}'
      to   = 'if det != nil && gt != nil && gt != det {
			det.Detected++
		}
		if gt != nil && det != nil && gt == det {'
    }
  )
`
	dir := t.TempDir()
	p := filepath.Join(dir, "verify.ps1")
	if err := os.WriteFile(p, []byte(table), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	entries := parseRedCheck(t, p)
	if len(entries) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(entries))
	}
	if entries[0].name != "a single-line entry" || entries[0].to != "if false { // REDCHECK" {
		t.Errorf("entry 0 parsed as %+v", entries[0])
	}
	// The whole point: the multi-line value, all three lines, not the first line
	// and not an empty string.
	if !strings.Contains(entries[1].to, "det.Detected++") ||
		!strings.Contains(entries[1].to, "gt == det {") {
		t.Errorf("the multi-line mutation was truncated to %q", entries[1].to)
	}
}

// TestAnEscapedQuoteInAMutationIsReadAsData: PowerShell doubles a quote inside a
// single-quoted string, so `”` is an apostrophe rather than the end of the value.
// Reading it as a terminator truncates the mutation and the assertion then searches
// for a string that cannot occur.
func TestAnEscapedQuoteInAMutationIsReadAsData(t *testing.T) {
	// The name is `it reads '` -- an apostrophe as the last character -- so the
	// literal is open-quote, text, escaped-quote-pair, close-quote: FOUR quotes at
	// the end, not two. The first version of this fixture had two, which is an
	// unterminated literal rather than a value ending in an apostrophe, and it
	// failed for the right reason: the parser read on into the next line and
	// returned `it reads '\n from = `. The reader was correct and the fixture was
	// not.
	const table = `    @{
      name = 'it reads '''
      from = 'if o.Degraded != DegradationNone {'
      to   = 'if false && o.Degraded != DegradationNone { // REDCHECK it doesn''t matter'
    }
`
	dir := t.TempDir()
	p := filepath.Join(dir, "verify.ps1")
	if err := os.WriteFile(p, []byte(table), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	entries := parseRedCheck(t, p)
	if len(entries) != 1 {
		t.Fatalf("parsed %d entries, want 1", len(entries))
	}
	if entries[0].name != "it reads '" {
		t.Errorf("name parsed as %q, want %q", entries[0].name, "it reads '")
	}
	if !strings.Contains(entries[0].to, "doesn't matter") {
		t.Errorf("the doubled quote was not read as an apostrophe: %q", entries[0].to)
	}
}

// TestAnUnterminatedMutationStringIsAnError: a truncated read produces a mutation
// string that matches nothing, and assertion 2 would then report the control as
// guarded. Silence there is the failure mode the whole assertion exists to prevent.
func TestAnUnterminatedMutationStringIsAnError(t *testing.T) {
	const table = "    @{\n      name = 'truncated'\n      to   = 'if false { // REDCHECK\n    }\n"
	dir := t.TempDir()
	p := filepath.Join(dir, "verify.ps1")
	if err := os.WriteFile(p, []byte(table), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// parseRedCheck calls t.Fatalf on this, which is what we want; assert the
	// primitive directly so the failure message is about the reader.
	if _, err := readQuoted(table, strings.Index(table, "'if false")); err == nil {
		t.Error("an unterminated single-quoted string was read as a value")
	}
	// And an index that is not an opening quote at all, which is what a parser
	// bug upstream would hand it. Returning an error rather than indexing at -1 is
	// the difference between a gate that reports and a gate that panics.
	if _, err := readQuoted(table, -1); err == nil {
		t.Error("a negative index was read as a value")
	}
}

// ---------------------------------------------------------------------------
// Against the real tree
// ---------------------------------------------------------------------------

// repoRoot is this module's root, found by walking up to go.mod rather than by
// counting `..` hops. A test that breaks when a directory moves is a test that gets
// deleted rather than fixed.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find go.mod above this package")
	return ""
}

// git runs one git command in dir and returns its stdout.
//
// `gosec` flags G204 here -- a subprocess launched with a variable -- and the flag
// is right about the shape and wrong about the risk: every `args` value in this file
// is a literal, and the variable being passed is `dir`, not a command line. So it is
// suppressed on the line with that reasoning rather than by a file-wide exclusion,
// because a file-wide one would silence the finding everywhere including on a call
// somebody adds later that really does build a command line out of data.
//
// `noctx` is NOT suppressed: a `git` with no deadline is a gate that can hang
// forever, and a verification script that hangs is a gate that gets skipped. The
// context is the test's own, so the bound is the go test timeout.
//
// The `errors.As` is not the same story as G204: `*exec.ExitError` comes back
// wrapped, so the type assertion would not fire on exactly the failures this wants
// to report git's stderr for.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: every argument below is a literal; see above.
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, ee.Stderr)
		}
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

// redCheckEntry is one `@{...}` block lifted out of a PowerShell red-check table.
// redCheckEntry is one `@{...}` block lifted out of a PowerShell red-check table.
type redCheckEntry struct {
	name string
	from string
	to   string
}

// psKeyRE finds the start of a `<key> =` field of a red-check entry.
//
// BOTH quote styles are matched, because the tables use both. The entry at
// `poc/active-monitoring/scripts/verify.ps1:510` is
//
//	to   = "WHERE id=? -- REDCHECK: the terminal-state guard was removed"
//
// written double-quoted because the SQL beside it contains a single quote. A
// parser that read single quotes only dropped that control without saying so, and
// the symptom was two adjacent `name` fields -- which reads as a malformed table
// rather than as a parser that cannot see one entry out of seventy-seven.
var psKeyRE = regexp.MustCompile(`(?m)^[ \t]*(name|from|to)[ \t]*=[ \t]*(?:'|")`)

// parseRedCheck reads every `@{...}` entry's `name`, `from` and `to` out of a
// PowerShell red-check table.
//
// It parses PowerShell string literals rather than running a line-oriented regex,
// and that is not fussiness. Two of the POC table's mutations SPAN LINES:
//
//	to   = 'if det != nil && gt != nil && gt != det {
//			det.Detected++
//		}
//		if gt != nil && det != nil && gt == det {'
//
// A line regex matched 76 of 77 entries and reported the TABLE as malformed. A
// gate that cannot read every control in the table is a gate silently covering 97%
// of them, and the failure pointed at the table rather than at the gate -- which is
// the worse of the two, because the instinct on seeing it is to go and edit a
// correct table to satisfy a broken parser.
//
// The fields are grouped by KEY rather than paired by position, because assertion 2
// needs all three and a table entry is free to order them as it likes.
func parseRedCheck(t *testing.T, path string) []redCheckEntry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)

	matches := psKeyRE.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		t.Fatalf("%s has no red-check entries at all, so this gate is checking nothing", path)
	}

	type kv struct{ key, value string }
	var fields []kv
	for _, m := range matches {
		val, err := readQuoted(body, m[1]-1)
		if err != nil {
			t.Fatalf("%s: reading the %q field: %v", path, body[m[2]:m[3]], err)
		}
		fields = append(fields, kv{key: body[m[2]:m[3]], value: val})
	}

	// Entries are delimited by `name =`: everything between one `name` and the next
	// belongs to that entry. A `from` or `to` seen before the first `name` is
	// reported rather than dropped, because a table with a headless mutation is a
	// table whose first control cannot be read.
	var out []redCheckEntry
	var cur *redCheckEntry
	for _, f := range fields {
		switch f.key {
		case "name":
			out = append(out, redCheckEntry{name: f.value})
			cur = &out[len(out)-1]
		case "from":
			if cur == nil {
				t.Fatalf("%s: a `from` appears before any `name`, so the first entry cannot be "+
					"read. The parser is wrong, not the table.", path)
			}
			cur.from = f.value
		case "to":
			if cur == nil {
				t.Fatalf("%s: a `to` appears before any `name`, so the first entry cannot be "+
					"read. The parser is wrong, not the table.", path)
			}
			cur.to = f.value
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s parsed to zero entries", path)
	}
	for i, e := range out {
		if e.to == "" {
			t.Fatalf("%s entry %d (%q) has no `to`. Assertion 2 cannot read it.", path, i, e.name)
		}
	}
	return out
}

// readQuoted reads the PowerShell string literal beginning at the opening quote at
// index open, and returns its contents with escapes resolved.
//
// Inside single quotes a quote is escaped by doubling it and everything else is
// literal. Inside double quotes a backtick escapes the next character and a doubled
// double-quote is also an escape. A literal containing no quote is the same string
// either way, which is why one reader covers both.
//
// An unterminated literal is an error rather than "everything to end of file": a
// truncated read produces a mutation string that matches nothing, and assertion 2
// would then report the control as guarded. That is the exact failure this
// assertion exists to prevent, reached through the gate itself.
func readQuoted(s string, open int) (string, error) {
	if open < 0 || open >= len(s) || (s[open] != 39 && s[open] != 34) {
		return "", fmt.Errorf("index %d is not an opening quote", open)
	}
	q := s[open]
	var b strings.Builder
	for i := open + 1; i < len(s); i++ {
		if s[i] == q {
			// A doubled quote is an escaped quote; a lone one ends the literal.
			if i+1 < len(s) && s[i+1] == q {
				b.WriteByte(q)
				i++
				continue
			}
			return b.String(), nil
		}
		if q == 34 && s[i] == '`' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return "", fmt.Errorf("unterminated quote at index %d", open)
}

// goFiles reads every tracked Go file in the tree.
//
// `git ls-files` rather than a filesystem walk: the gate is about what is COMMITTED
// plus what is staged, and a walk would also pick up build output and editor
// scratch. Untracked files are a separate question and the red-check tables mutate
// tracked ones.
func goFiles(t *testing.T, root string) (map[string]string, map[string]bool) {
	t.Helper()
	listing := git(t, root, "ls-files", "-z", "--", "*.go", "*.ps1")
	present := map[string]bool{}
	files := map[string]string{}
	for _, rel := range strings.Split(listing, "\x00") {
		if rel == "" {
			continue
		}
		abs := filepath.Join(root, rel)
		raw, err := os.ReadFile(abs)
		if err != nil {
			// A file listed by git but absent from the worktree. Not this gate's
			// business, and treating it as an empty file would be a silent pass.
			continue
		}
		key := filepath.ToSlash(rel)
		present[key] = true
		files[key] = string(raw)
	}
	return files, present
}

func TestTheRealTreeHasNoWorktreeFrozenMidMerge(t *testing.T) {
	root := repoRoot(t)
	var states []treestate.WorktreeState
	for _, line := range strings.Split(strings.TrimSpace(git(t, root, "worktree", "list", "--porcelain")), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			states = append(states, treestate.WorktreeState{Path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "detached"):
			if n := len(states); n > 0 {
				states[n-1].Detached = true
			}
		}
	}
	if len(states) == 0 {
		t.Fatal("git reported no worktrees at all, so this assertion checked nothing")
	}
	// `git worktree list` prints the main worktree first. That ordering is the whole
	// of how the main one is identified, and it is worth saying so in a comment: a
	// later version that sorts this output would silently stop exempting the
	// worktree being edited, and the gate would fail on every keystroke.
	states[0].Main = true
	for i, w := range states {
		// MERGE_HEAD lives in the worktree's own git dir.
		gd := strings.TrimSpace(git(t, root, "rev-parse", "--git-dir", w.Path))
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(root, gd)
		}
		if _, err := os.Stat(filepath.Join(gd, "MERGE_HEAD")); err == nil {
			states[i].MergeInProgress = true
		}
		dirty := strings.TrimSpace(git(t, root, "-C", w.Path, "status", "--porcelain"))
		// Untracked-only dirt is somebody's build output, not work in progress.
		if strings.TrimSpace(stripUntracked(dirty)) != "" {
			states[i].IndexDiffersFromHead = true
		}
	}
	if got := treestate.CheckWorktrees(states); len(got) > 0 {
		t.Errorf("the real tree has frozen worktrees:\n%s", treestate.Report(got))
	}
}

func stripUntracked(status string) string {
	var keep []string
	for _, l := range strings.Split(status, "\n") {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "??") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

func TestTheRealTreeHasNoRedCheckMutationApplied(t *testing.T) {
	root := repoRoot(t)
	tables := []string{
		filepath.Join(root, "scripts", "verify.ps1"),
		filepath.Join(root, "poc", "active-monitoring", "scripts", "verify.ps1"),
	}
	var muts []treestate.Mutation
	for _, p := range tables {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("rel %s: %v", p, err)
		}
		rel = filepath.ToSlash(rel)
		for _, e := range parseRedCheck(t, p) {
			muts = append(muts, treestate.Mutation{Table: rel, Control: e.name, From: e.from, To: e.to})
		}
	}
	// Both tables must have been read. A parser that silently found nothing in the
	// POC table would leave this assertion covering eleven controls and calling it
	// thirty-three.
	if len(muts) < 20 {
		t.Fatalf("only %d mutation(s) were read from the two tables; the parser is wrong, "+
			"not the tables", len(muts))
	}
	t.Logf("assertion 2 is checking %d mutations from two tables", len(muts))

	files, present := goFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no files were read, so this assertion checked nothing")
	}

	// **This gate's own package is excluded, and it is not an exemption.**
	//
	// `treestate_test.go` contains four of the mutations verbatim, as fixtures for
	// the checks that detect an applied mutation. A detector that must be tested
	// against a real instance of the thing it detects has to write that thing down,
	// and the file it writes it in then matches every search for it.
	//
	// The first live run of this assertion reported four findings against
	// `internal/treestate/treestate.go` and `treestate_test.go` -- one per fixture
	// that quotes a mutation's `to` without also quoting its `from`. The controls
	// named were real and the tree was clean, which is the worst combination this
	// gate can produce: a reader who sees "its mutation is APPLIED in
	// internal/treestate/treestate_test.go" concludes the detector is broken, and
	// then cannot find the bug, because there is not one.
	//
	// The exclusion is by path and it is exact: this package, and nothing else. A
	// second gate doing this work would have the same problem and would need the
	// same line, which is a cost worth paying once and complaining about twice.
	const selfPackage = "internal/treestate/"
	for p := range files {
		if strings.HasPrefix(p, selfPackage) {
			delete(files, p)
			delete(present, p)
		}
	}
	if len(files) == 0 {
		t.Fatal("every file was excluded, so this assertion checked nothing")
	}

	got := treestate.CheckMutationsAbsent(files, present, muts)
	if len(got) > 0 {
		t.Errorf("the real tree has an applied red-check mutation:\n%s", treestate.Report(got))
	}
}

func TestEveryLocalBranchHasLandedOrSaidWhy(t *testing.T) {
	root := repoRoot(t)
	// The base is the branch this worktree is on. Hard-coding `contract` would make
	// the gate a claim about a branch name rather than about the tree.
	base := strings.TrimSpace(git(t, root, "rev-parse", "--abbrev-ref", "HEAD"))
	// The declined list: branches allowed to stay unlanded, each with the reason.
	//
	// An entry here is a decision somebody has to be able to read back, so a
	// prefix cannot stand in for one. `proposal/*` would cover ten branches with
	// one sentence, and the day a second branch family needs the same treatment the
	// sentence would be about the wrong one. They are written out, each naming its
	// own document, and `CheckBranchesLanded` refuses an empty reason.
	declined := map[string]string{
		"main": "the published line. contract contains main and is ahead of it by every commit " +
			"in this repository; fast-forwarding main is a human's call, not a gate's",

		// `stabilize/m1-triage` was this branch's first name, before the work was split
		// onto `stabilize/dispatch-chain`. A concurrent session pushed commit 6aa55fc
		// (`internal/nlsearch`) onto that name, so it now carries another session's
		// in-flight work and cannot be fast-forwarded here without sweeping that in.
		//
		// It is declined with a reason rather than deleted, because deleting it would
		// destroy the only ref holding `internal/nlsearch`. Whoever finishes that work
		// lands it, and this entry is then deleted - which is the whole contract of a
		// declined branch: it names itself as temporary and the gate fails the moment it
		// stops being true.
		"stabilize/m1-triage": "another session pushed internal/nlsearch onto this name while " +
			"this branch was being written, and it holds that work in flight. Deleting the " +
			"branch would destroy it; landing it would sweep an unfinished second feature " +
			"into the dispatch chain. It is declined until that session lands or renames it",
	}
	for _, p := range proposalBranches(t, root) {
		declined[p] = "a proposal branch is a DOCUMENT, not work in flight. It holds the " +
			"evidence for one decision and is read on its own; contract is not expected to " +
			"contain it, and merging it would put a report in the product tree. See ROADMAP.md."
	}

	var states []treestate.BranchState
	for _, b := range strings.Split(strings.TrimSpace(git(t, root, "for-each-ref",
		"--format=%(refname:short)", "refs/heads/")), "\n") {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		ahead := strings.TrimSpace(git(t, root, "rev-list", "--count", base+".."+b))
		n := 0
		if ahead != "" {
			var err error
			n, err = strconv.Atoi(ahead)
			if err != nil {
				t.Fatalf("rev-list --count %s..%s returned %q: %v", base, b, ahead, err)
			}
		}
		states = append(states, treestate.BranchState{Name: b, CommitsAhead: n, Declined: declined[b]})
	}
	if len(states) == 0 {
		t.Fatal("no local branches, so this assertion checked nothing")
	}
	t.Logf("assertion 3 is checking %d local branches against %s", len(states), base)
	if got := treestate.CheckBranchesLanded(base, states); len(got) > 0 {
		t.Errorf("the real tree has unlanded branches:\n%s", treestate.Report(got))
	}
}

// proposalBranches lists the local `proposal/*` branches, so the declined list can
// name each one individually rather than matching a prefix.
func proposalBranches(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, b := range strings.Split(strings.TrimSpace(git(t, root, "for-each-ref",
		"--format=%(refname:short)", "refs/heads/proposal/")), "\n") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		t.Log("no proposal/ branches in this tree; the declined list covers main only")
	}
	return out
}

// TestEveryRedCheckEntryHasATranscript is assertion 4, run against the document each
// table's controls are supposed to be recorded in.
//
// **Each table names its own document, and that is not a convenience.** The root
// module's eleven controls are recorded in `docs/status.md`, which `AGENTS.md` names
// as the only source of truth for build state. The POC module's seventy-seven belong
// to a separate module with its own documentation, and demanding they appear in the
// product's status document would mean copying seventy-seven transcripts into a file
// whose subject is the product — where they would be read as product claims about a
// module nobody ships. The first version of this test checked all eighty-eight against
// `docs/status.md` and reported eighty-two findings, seventy-seven of them being the
// POC's own controls.
//
// A control with no transcript in its own module's record is still a finding. That is
// the assertion doing its job: `poc/active-monitoring/redcheck-transcripts.txt` did
// not exist until this gate said the seventy-seven had never been proved, and it
// now carries all seventy-seven with a baseline, a per-control failure and a
// restore check.
func TestEveryRedCheckEntryHasATranscript(t *testing.T) {
	root := repoRoot(t)

	// table path -> the document that records its controls. Adding a third red-check
	// table means adding a line here, and that is the point: a table whose controls
	// are recorded nowhere is a table that cannot be required to prove itself.
	transcripts := map[string]string{
		"scripts/verify.ps1":                       filepath.Join(root, "docs", "status.md"),
		"poc/active-monitoring/scripts/verify.ps1": filepath.Join(root, "poc", "active-monitoring", "redcheck-transcripts.txt"),
	}

	var controls []treestate.Control
	for _, rel := range []string{
		"scripts/verify.ps1",
		"poc/active-monitoring/scripts/verify.ps1",
	} {
		transcriptPath, ok := transcripts[rel]
		if !ok {
			t.Fatalf("no transcript document is registered for %s. Every red-check table must "+
				"say where its controls are recorded, or this assertion cannot cover it.", rel)
		}
		raw, err := os.ReadFile(transcriptPath)
		if err != nil {
			t.Errorf("read the transcript for %s (%s): %v", rel, transcriptPath, err)
			continue
		}
		for _, e := range parseRedCheck(t, filepath.Join(root, filepath.FromSlash(rel))) {
			controls = append(controls, treestate.Control{
				Name: e.name, Table: rel, Transcript: string(raw),
			})
		}
	}
	if len(controls) == 0 {
		t.Fatal("no controls were read, so this assertion checked nothing")
	}
	byTable := map[string]int{}
	for _, c := range controls {
		byTable[c.Table]++
	}
	for _, rel := range []string{"scripts/verify.ps1", "poc/active-monitoring/scripts/verify.ps1"} {
		t.Logf("assertion 4 checks %d controls from %s against %s",
			byTable[rel], rel, transcripts[rel])
	}

	if got := treestate.CheckTranscripts(controls); len(got) > 0 {
		t.Errorf("these red-check entries have no transcript in the document their table names:\n%s",
			treestate.Report(got))
	}
}
