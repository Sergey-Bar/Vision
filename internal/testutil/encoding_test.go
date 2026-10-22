package testutil

// This file is a gate, not helpers.
//
// `docs/status.md` recorded two control characters in the tree for a long time —
// `internal/inbox/inbox.go` read `pp.site_ids` where it means `app.site_ids`, and
// `internal/milestone/epoch_test.go` had the same byte before `t.In(` — and
// described them as "outside this pass and left alone". They were a `0x07` byte
// where a letter belonged: invisible in a diff, invisible in a review, invisible in
// a grep for the word it corrupted.
//
// A defect that is invisible in every review is invisible in every review. It needs
// a gate, or it needs a person to happen to look, and "a person happens to look" is
// a mechanism with a bad record.
//
// The same encoding repair also left `U+FFFD REPLACEMENT CHARACTER` behind, and
// most of it has since been repaired. The repair was restricted to the ones the
// evidence decided: a character sitting in comment prose between spaces, in a file
// that already used U+2014 EM DASH and used no other dash or curly quote, has one
// reading, and a character in a comment has no behaviour to change. What is left is
// named below rather than guessed at, because the byte it replaced is not recoverable
// from the byte that replaced it, and the two survivors fail that test for different
// reasons: one is prose in the document this repository calls its source of truth,
// and the other is a value the program hashes. They are listed, and the list is
// checked in both directions, so they cannot grow and cannot be forgotten.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoRoot is the module root, from this package's directory.
const repoRoot = "../.."

// replacementChar is what a mangled character becomes when a file is decoded with
// the wrong encoding and written back with the right one.
const replacementChar = "\uFFFD"

// replacementCharSites is every file still carrying a replacement character, with
// the count that was read out of it.
//
// Each entry is a debt with a name and a number, so "this is still there" is a
// checked fact rather than a paragraph in a document nobody re-reads. Removing an
// entry means fixing the file; adding one means the gate noticed something new.
var replacementCharSites = map[string]int{
	// docs/status.md carries one, and the reason it is here twice is worth writing
	// down: a pass that rewrote large sections of that document with PowerShell
	// also re-encoded every em dash into mojibake. Repairing that restored this
	// character, which had been destroyed by the same rewrite. Removing it means
	// fixing the file; it is listed because the ledger is the record of what is
	// there, and a file that loses its character through a repair is exactly the
	// case the ledger exists to catch.
	"docs/status.md": 1,
	// adapter.go's two are not comments: they are the marks either side of a hash
	// marker, `"captured-at"`, in `withoutCapturedTimes`. They are not quote marks.
	// That file holds thirteen U+2014 EM DASH and not one curly quote, so an em dash
	// is the reading the evidence supports, and this entry used to say "quote marks"
	// without having checked. It is still not written, because this is the one site in
	// the tree where the damaged bytes have an effect outside a comment: the marker is
	// hashed into `SourceEventID`, so the id of every unreadable item depends on it.
	// Nothing pins those ids - `TestTwoDistinctUnreadableItemsDoNotCollide` asserts
	// only that two such items differ - so a write here would break no test and pass
	// every gate in this file. It would replace a recorded unknown with an invented
	// value that reads as deliberate, which is the opposite of what a ledger is for.
	"internal/milestone/adapter.go": 2,
}

// TestNoControlCharactersInSourceText is the gate for the class that bit twice.
//
// C0 control bytes other than tab, newline and carriage return, plus DEL. They have
// no business in Go, Markdown or SQL, and every occurrence of one has been a
// mangled letter or a mangled punctuation mark inside a comment.
func TestNoControlCharactersInSourceText(t *testing.T) {
	scanned := 0
	walkSourceText(t, func(rel, path string, raw []byte) {
		scanned++
		for i, b := range raw {
			if !isControl(b) {
				continue
			}
			t.Errorf("%s:%d has the control byte 0x%02X at offset %d. It is a mangled "+
				"character, and a comment that reads wrong is a comment nobody can check",
				rel, lineOf(raw, i), b, i)
		}
	})
	if scanned < 50 {
		t.Errorf("only %d source files were read; this gate is not covering the tree", scanned)
	}
}

// TestTheReplacementCharacterLedgerIsAccurate keeps the debt list honest in both
// directions.
//
// A fixed file still listed keeps the count wrong and nothing notices, because a
// gate that only ever fails on new files is a gate that has quietly stopped
// describing anything.
func TestTheReplacementCharacterLedgerIsAccurate(t *testing.T) {
	found := map[string]int{}
	walkSourceText(t, func(rel, path string, raw []byte) {
		if count := strings.Count(string(raw), replacementChar); count > 0 {
			found[rel] = count
		}
	})

	for path, want := range replacementCharSites {
		got, ok := found[path]
		if !ok {
			t.Errorf("%s no longer contains a replacement character and is still in the ledger "+
				"with %d. Fix the ledger in the same commit: a list that cannot shrink is not a "+
				"list", path, want)
			continue
		}
		if got != want {
			t.Errorf("%s has %d replacement characters; the ledger says %d. Either it was fixed "+
				"partly or something new was introduced — both need the ledger updated",
				path, got, want)
		}
	}
	for path, count := range found {
		if _, listed := replacementCharSites[path]; !listed {
			t.Errorf("%s has %d replacement characters and is not in the ledger. If it is new, add "+
				"it here and fix it; do not leave it for somebody else to find", path, count)
		}
	}
}

// TestTheLedgerIsNotEmpty is the floor on the ledger's own honesty.
//
// Without it, deleting the map turns the two tests above into gates over nothing,
// with no failure anywhere.
func TestTheLedgerIsNotEmpty(t *testing.T) {
	if len(replacementCharSites) == 0 {
		t.Fatal("the replacement-character ledger is empty. If every one of them has been fixed, " +
			"delete the ledger and the two tests that read it in the same commit, so nobody can " +
			"empty it and keep the gate")
	}
	for path, want := range replacementCharSites {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
			t.Errorf("the ledger names %s, which does not exist", path)
		}
		if want <= 0 {
			t.Errorf("the ledger says %s has %d replacement characters", path, want)
		}
	}
}

// walkSourceText visits every text file a reader of this repository would see,
// and hands the callback a repository-relative slash path.
//
// It asks GIT which files those are, rather than walking the filesystem.
//
// The comment on this function used to say "every TRACKED text file" while the
// code walked the filesystem, and the difference is not a wording slip: the
// filesystem holds the build output, the virtualenv, the model weights and the
// footage, and one of them is a JSON file the size of a database dump. The
// scan read it, and the gate that exists to catch a stray byte in the source
// spent ten minutes in `strings.Count` on a build artefact and timed out --
// which is a gate that has stopped running, reported as a failure of the
// thing it was scanning.
//
// `git ls-files --cached --others --exclude-standard` is exactly the right
// set: the files the index knows about, and the new ones that are not ignored
// yet, so a file nobody has staged is still scanned. What it leaves out is
// what no reader of the repository would see.
func walkSourceText(t *testing.T, visit func(rel, path string, raw []byte)) {
	t.Helper()
	for _, rel := range repositoryTextFiles(t) {
		path := filepath.Join(repoRoot, filepath.FromSlash(rel))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		visit(filepath.ToSlash(rel), path, raw)
	}
}

// repositoryTextFiles is the list of text files git knows about, filtered to
// the extensions where a stray byte changes what a reader believes.
func repositoryTextFiles(t *testing.T) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ask git which files the repository has: %v", err)
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		if !trackedText(filepath.FromSlash(name)) {
			continue
		}
		files = append(files, name)
	}
	if len(files) == 0 {
		t.Fatal("git reported no text files at all, so this scan is asserting nothing")
	}
	return files
}

// skipDir is gone.
//
// It kept a filesystem walk off `poc`, `.kilo`, the virtualenv and the build
// output, and the walk it guarded is gone: the scan asks git for the files
// instead, and git already knows what is source.
//
// The `poc` exclusion was the one with a story attached. It was skipped
// because it is the other module's tree, and this comment used to claim "it
// has its own gate". It does not: there is no equivalent ledger or scan under
// `poc/active-monitoring`, and the one U+FFFD the other module carried was
// repaired with nothing checking it either way. Nothing here walks that tree,
// so the honest position is that it is uncovered, not that it is covered
// elsewhere -- and the position is unchanged by the walk being replaced.
//
// A virtualenv was skipped by prefix rather than by exact name, because this
// machine's environment is `.venv-poc`. Third-party bytes that were never
// committed are not this repository's debt, and the alternative -- adding them
// to `replacementCharSites` -- would record somebody else's files as a
// decision this repository made.
//
// The gate that is left is the one that matters: it scans what a reader of the
// repository would see, and it finishes.

// trackedText is the set of files where a stray byte changes what a reader believes.
func trackedText(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".md", ".sql", ".yaml", ".yml", ".json", ".ps1", ".py":
		return true
	}
	return false
}

// isControl is a C0 control byte other than the three that format a text file, plus
// DEL.
func isControl(b byte) bool {
	if b == '\t' || b == '\n' || b == '\r' {
		return false
	}
	return b < 0x20 || b == 0x7F
}

// lineOf is 1-based, because that is what a person and an editor both say.
func lineOf(raw []byte, offset int) int {
	return 1 + strings.Count(string(raw[:offset]), "\n")
}
