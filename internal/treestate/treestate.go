// Package treestate asks one question: does the working tree say what it claims
// to say?
//
// # Why this exists
//
// Four worktrees sat in this repository frozen mid-merge, one control was disabled
// in product code with the disable left in place, and a branch's entire
// measurement had been deleted by a merge that took the wrong side of a stub. Every
// one of those is invisible to `go build`, `go vet`, `go test`, `gofmt` and
// `golangci-lint` — all five of which were green. They are not code defects. They
// are **state** defects: the tree is not in the state it says it is in, and nothing
// in the toolchain asks.
//
// Three separate gates were proposed for this class and one is here instead,
// because all three are the same defect read from three angles:
//
//   - `redcheck-clean` — no red-check mutation left applied in the tree;
//   - `merge-state`    — no worktree frozen mid-merge;
//   - `branch-landed`  — no branch's work silently lost by a merge.
//
// One gate, four assertions. Splitting them means a fourth assertion starts a
// fourth script, and a gate that has its own file is a gate somebody has to
// remember to run.
//
// # The four assertions
//
//  1. No worktree is frozen. Either a `MERGE_HEAD` exists (a merge in progress that
//     nobody finished) or the index differs from `HEAD` with no merge in progress
//     (staged or unstaged edits sitting in a worktree nobody is working in).
//  2. No red-check mutation is applied. Every `to =` string in either red-check
//     table must be **absent** from the tree. This is the assertion that catches a
//     control removed and left removed — the defect where `soak.CropCounters.GatedOut`
//     stopped being incremented and `store.searchWhere` stopped matching a
//     reviewer's note, both times with a green suite.
//  3. Every local branch has landed. `git log <base>..<branch>` empty, or the
//     branch is in a declined list carrying a reason. This is the assertion that
//     would have caught the 707-line measurement being replaced by a 204-line stub
//     marked "NOT implemented yet".
//  4. Every red-check entry has a transcript in `docs/status.md`. A control whose
//     line is in the table and whose transcript is not in the document is a control
//     nobody watched fail.
//
// # Why assertion 2 is "absent", not "present"
//
// The natural way to write this is to check that each table entry's `from =` string
// IS present. That is assertion 2 inverted, and it fails for a legitimate reason:
// a control whose line has since been refactored away reports as "control missing",
// which is a SKIP, not a pass, and a gate full of SKIPs reads as a broken gate.
//
// So this gate checks the *mutation* is absent instead. A mutation string is a
// deliberately broken line of code carrying the marker `REDCHECK`. If one of those is
// sitting in the tree, something removed a control and did not put it back, and the
// only question is which one. That question is answerable in one grep; the current
// state was not detectable at all.
//
// # What this package does not do
//
// It does not check that the tree is **clean**. A dirty tree is an ordinary thing a
// person is doing right now, and a gate that fails on it is a gate that gets run
// with `--no-verify`. It checks for the three states that mean *work was lost*, which
// is a different thing and is the thing that happened.
package treestate

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Finding is one thing wrong with the tree's claimed state.
//
// It carries enough to act on without opening the file: which assertion fired,
// which file or branch or control is implicated, and what specifically about it is
// wrong. A gate whose failure sends the reader to grep is a gate whose reader stops
// reading it.
type Finding struct {
	// Assertion is 1, 2, 3 or 4. It is a number rather than a name because the
	// number is what the package comment enumerates and what a transcript refers to.
	Assertion int
	// Subject is the worktree path, the mutation string, the branch name, or the
	// control name — whichever the assertion is about.
	Subject string
	// Detail is one sentence saying what is wrong, phrased so it can be printed
	// without the reader needing the assertion's rules in their head.
	Detail string
}

func (f Finding) String() string {
	return fmt.Sprintf("tree-state %d: %s -- %s", f.Assertion, f.Subject, f.Detail)
}

// Report renders findings for a gate's console output, one per line.
//
// Sorted so that two runs over the same tree produce the same transcript. An
// unsorted report is a report whose diff is noise, and a gate whose output
// changes order between runs is a gate people stop reading before comparing them.
func Report(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Assertion != findings[j].Assertion {
			return findings[i].Assertion < findings[j].Assertion
		}
		return findings[i].Subject < findings[j].Subject
	})
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("    ")
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Assertion 1 — no worktree is frozen mid-merge
// ---------------------------------------------------------------------------

// WorktreeState is what assertion 1 needs to know about one worktree.
//
// Both fields are read from the filesystem rather than from `git status`, because
// `git status` on a worktree with a merge in progress prints the conflicts and the
// instruction to resolve them, and a reader who has seen that output several times
// stops reading it. `MERGE_HEAD` and the index-vs-`HEAD` comparison are two facts
// with no prose attached.
type WorktreeState struct {
	// Path is the worktree's own directory, named in the finding so the reader
	// knows which one to go and look at.
	Path string
	// MergeInProgress is whether `MERGE_HEAD` exists in the worktree's git dir.
	MergeInProgress bool
	// IndexDiffersFromHead is whether the index carries staged changes.
	IndexDiffersFromHead bool
	// Detached is reported for a worktree on no branch at all. It is not a finding
	// by itself — a detached worktree is how `git worktree add --detach` is
	// supposed to look, and one is how the retired `pattern-silene` sat. It is
	// carried here so the reader of a finding can see the whole state, and because
	// a detached worktree with staged changes is the combination that most looks
	// like an accident.
	Detached bool
	// Main marks the worktree somebody is working in right now — the one
	// `git worktree list` prints first.
	//
	// It exists because "the tree is dirty" and "a tree was abandoned mid-merge"
	// are different facts, and a gate that cannot tell them apart fires on the
	// first one. This assertion exists because of `crop.go`'s removed
	// `ct.GatedOut++` and `searchcards.go`'s `1=0 -- REDCHECK`, which sat in a
	// worktree nobody was working in for weeks while the person who created them
	// moved on. Reporting an uncommitted change in the main worktree would mean
	// this gate fails on every edit anybody makes, and a gate that fails while you
	// are working is a gate that gets run with `--no-verify`.
	Main bool
}

// CheckWorktrees is assertion 1.
//
// A worktree is a finding when it has a merge in progress, or when its index
// differs from `HEAD` with no merge to explain why AND nobody is working in it.
//
// The second half is the one that matters. A `MERGE_HEAD` is loud: git tells you
// about it every time you touch the worktree. A worktree carrying staged edits with
// no merge is silent, it is indistinguishable from a colleague's in-progress work,
// and the edits in it are the ones a `git worktree remove` deletes. That is how
// `crop.go`'s removed `ct.GatedOut++` and `searchcards.go`'s `1=0 -- REDCHECK` both
// survived: they were not edits anybody was making, they were edits a red-check run
// left behind, sitting in a worktree nobody was working in.
//
// The main worktree is exempt from the second half and **not** from the first. A
// main worktree stuck mid-merge is the worst case in this list, because it is the
// one every other gate runs against: `go build`, `go vet`, `go test` and
// `scripts/verify.ps1` all read it, so a merge stopped here is a verification run
// measuring a tree that does not exist yet.
func CheckWorktrees(states []WorktreeState) []Finding {
	var out []Finding
	for _, w := range states {
		if w.MergeInProgress {
			out = append(out, Finding{
				Assertion: 1,
				Subject:   w.Path,
				Detail: "has a MERGE_HEAD: a merge started here and never finished. " +
					"The resolutions may be on disk and unstaged. Finish it with `git merge` " +
					"or abandon it with `git merge --abort`; do not remove the worktree, " +
					"which deletes whatever was resolved.",
			})
			continue
		}
		if !w.IndexDiffersFromHead || w.Main {
			continue
		}
		extra := ""
		if w.Detached {
			extra = " It is on no branch, so nothing here is reachable from a ref and " +
				"`git worktree remove` deletes it without asking."
		}
		out = append(out, Finding{
			Assertion: 1,
			Subject:   w.Path,
			Detail: "has staged changes with no merge in progress, and it is not the worktree " +
				"anybody is working in. Nothing in this repository reads them: `go build`, " +
				"`go vet`, `go test` and the verification script all run against the main " +
				"worktree. Either they are work in progress -- commit them, or they are a " +
				"red-check run that did not restore, and assertion 2 will say which." + extra,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Assertion 2 — no red-check mutation is applied in the tree
// ---------------------------------------------------------------------------

// Mutation is one red-check entry's `from`/`to` pair, lifted out of a table.
//
// It is a line and the deliberately-broken replacement for it, so finding the broken
// one in a file while the original is gone is what a removed control looks like from
// the outside.
type Mutation struct {
	// Table says which red-check table the entry came from:
	// `scripts/verify.ps1` or `poc/active-monitoring/scripts/verify.ps1`. Carried so
	// the finding names the control rather than just printing a string of broken code.
	Table string
	// Control is the entry's `name`, so the reader is told which control is off
	// rather than being handed a line to grep for.
	Control string
	// From is the correct line, as a substring of the file it lives in.
	From string
	// To is the mutation: the deliberately-broken form. Matched as a substring,
	// because a mutation replaces part of a line and the rest of the line is still
	// there.
	To string
}

// CheckMutationsAbsent is assertion 2.
//
// `files` is the content of every file in the tree, keyed by path, and `present`
// names which of those paths exist. A path that is not present is skipped rather
// than treated as an empty file: an empty file contains no mutation, so the two are
// indistinguishable from the check's side and only one of them is worth reporting.
//
// # Why the test is the PAIR and not the mutation alone
//
// The first version of this assertion searched for `to` on its own, and it reported
// thirty findings on a clean tree. Two of them were instructive:
//
//   - `internal/rules/match.go` has a `switch` that legitimately contains
//     `return DegradedTimeout`, which is the mutation string of a control in
//     `internal\other\place.go`. The mutation was applied nowhere.
//   - the presence-mode entry's mutation is
//     `{Name: "SP_POC_ZONES", kind: kindString,` — a **prefix** of the line it
//     replaces. The correct line contains the broken one, so the broken one can
//     never be absent while the correct one is present.
//
// Both are the same defect: a mutation string is a replacement for ONE OCCURRENCE,
// and it can occur harmlessly anywhere else in the tree. So the assertion asks for
// the conjunction that only a real removal produces:
//
//	`to` is present   AND   `from` is absent
//
// That has no false positives. It also has the property that matters most for a
// repaired tree: once the `from` line is put back, the finding disappears whether or
// not a mutation string happens to remain elsewhere in the file.
//
// A file containing NEITHER is not reported. That is a control whose line has been
// refactored away, which the red-check gate already reports as a SKIP rather than a
// pass; reporting it here too would double-count one fact and make this assertion
// look noisier than it is.
//
// # Why a table is never searched at all
//
// A red-check table contains every mutation string BY DEFINITION: `to = '...'` is
// how the gate spells the broken version. Searching a table for its own mutations
// matches every entry in it. A run of the mutation-only version above reported all
// thirty of the POC table's controls as "APPLIED in
// poc/active-monitoring/scripts/verify.ps1" — the shape of a gate firing on
// everything, which a reader will assume means the repository is broken rather than
// that the gate is matching itself.
func CheckMutationsAbsent(files map[string]string, present map[string]bool, mutations []Mutation) []Finding {
	var out []Finding
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, m := range mutations {
		if strings.TrimSpace(m.To) == "" {
			// An entry with no `to =` cannot be searched for, and a gate that silently
			// skips its own table entries is the quiet-pass defect this whole package
			// exists to prevent. Reported rather than skipped.
			out = append(out, Finding{
				Assertion: 2,
				Subject:   m.Control,
				Detail: fmt.Sprintf("has an EMPTY `to =` in %s, so nothing can be searched "+
					"for and this control cannot be proved by this assertion. An entry with no "+
					"mutation string is an entry the assertion cannot see.", m.Table),
			})
			continue
		}
		if strings.TrimSpace(m.From) == "" {
			out = append(out, Finding{
				Assertion: 2,
				Subject:   m.Control,
				Detail: fmt.Sprintf("has an EMPTY `from =` in %s. Without the correct line this "+
					"assertion cannot tell an applied mutation from a refactor, so the control is "+
					"not covered.", m.Table),
			})
			continue
		}

		for _, p := range paths {
			if !present[p] || sameTable(p, m.Table) {
				continue
			}
			body := files[p]
			if !strings.Contains(body, m.To) || strings.Contains(body, m.From) {
				continue
			}
			out = append(out, Finding{
				Assertion: 2,
				Subject:   m.Control,
				Detail: fmt.Sprintf("its mutation is APPLIED in %s (declared by %s): the broken "+
					"line is there and the correct one is not. The file on disk is the mutation, "+
					"not the source, so everything built from it is the broken version. Put the "+
					"`from =` line back by hand; do not run the gate again until you have, because "+
					"the gate mutates files.", p, m.Table),
			})
		}
	}
	return out
}

// sameTable reports whether a searched path is the table that declared the mutation.
//
// Compared on a slash-normalised, case-insensitive basis because the paths arrive
// from two places that spell them differently: `git ls-files` and a hand-written
// table name. A comparison that misses here would search the table after all, and
// the cost of missing it is thirty false findings on every run.
func sameTable(path, table string) bool {
	return strings.EqualFold(filepath.ToSlash(path), filepath.ToSlash(table))
}

// ---------------------------------------------------------------------------
// Assertion 3 — every local branch has landed, or has said why it has not
// ---------------------------------------------------------------------------

// BranchState is what assertion 3 needs to know about one branch.
type BranchState struct {
	// Name is the branch name.
	Name string
	// CommitsAhead is how many commits are on the branch and not on the base. Zero
	// means landed.
	CommitsAhead int
	// Declined is a written reason this branch is allowed to stay unlanded, or
	// empty. A reason is mandatory: a branch that is unlanded with no reason is the
	// state this assertion exists to catch, and an empty string is how that state
	// would be re-declared as acceptable by accident.
	Declined string
}

// CheckBranchesLanded is assertion 3.
//
// A branch is a finding when it has commits the base does not and no recorded
// reason for having them.
//
// The declined list exists because "land everything" is not a rule a repository can
// enforce forever — a `proposal/` branch is a document, and `main` is a line this
// assertion does not get to redefine. What it can enforce is that a branch with
// unlanded work either says why or does not exist quietly.
//
// `base` is excluded by the caller rather than here, because "is this the branch I
// am comparing against" is a question about the invocation and not about the
// branches.
func CheckBranchesLanded(base string, states []BranchState) []Finding {
	var out []Finding
	for _, b := range states {
		if b.Name == base || b.CommitsAhead <= 0 {
			continue
		}
		if strings.TrimSpace(b.Declined) != "" {
			continue
		}
		out = append(out, Finding{
			Assertion: 3,
			Subject:   b.Name,
			Detail: fmt.Sprintf("has %d commit(s) that %s does not, and no recorded reason "+
				"for having them. Either land it, or record in the declined list next to the "+
				"gate why it stays — an unlanded branch with no reason is indistinguishable "+
				"from work that was lost in a merge.", b.CommitsAhead, base),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Assertion 4 — every red-check entry has a transcript
// ---------------------------------------------------------------------------

// Control is one red-check table entry, reduced to what assertion 4 reads.
type Control struct {
	// Name is the entry's `name`, which is what the transcript has to mention.
	Name string
	// Table says which table it came from.
	Table string
	// Transcript is the document that is supposed to carry its record — normally
	// `docs/status.md`.
	Transcript string
}

// CheckTranscripts is assertion 4.
//
// A control whose name appears in no transcript is a control nobody watched fail.
//
// The check is deliberately a substring search over one document rather than a
// structured record, because the thing being asserted is a **human having watched
// a test go red**, and a structured JSON field is what a gate writes when it wants
// a machine to believe a human did that. The transcript is prose on purpose: it is
// only worth anything if it says what the failure was.
//
// The consequence is that this assertion can be satisfied by a careless mention, so
// it is the weakest of the four and says so. It is still worth having, because the
// alternative is a table that grows by nobody remembering.
func CheckTranscripts(controls []Control) []Finding {
	var out []Finding
	for _, c := range controls {
		if strings.TrimSpace(c.Name) == "" {
			out = append(out, Finding{
				Assertion: 4,
				Subject:   c.Table,
				Detail: "has an entry with no `name`. An unnamed control cannot be " +
					"transcribed, so it is a control that cannot be proved by this assertion.",
			})
			continue
		}
		if !strings.Contains(c.Transcript, c.Name) {
			out = append(out, Finding{
				Assertion: 4,
				Subject:   c.Name,
				Detail: fmt.Sprintf("is in %s and appears nowhere in its transcript document. "+
					"A control with no record of being watched go red is a control that is a "+
					"comment. Run it by hand with the mutation applied and write down what failed.",
					c.Table),
			})
		}
	}
	return out
}
