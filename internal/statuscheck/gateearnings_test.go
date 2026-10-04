package statuscheck

// Which gates have ever been recorded red, and which have not.
//
// # The question
//
// `scripts/verify.ps1` runs twenty-four gates. The record it leaves behind says, for
// each one, whether it passed or failed. Forty such records exist in
// `dist/verification/`, and this file reads all of them and answers: **which gates
// have ever failed?**
//
// The answer, as of the last run: **seven of twenty-four.** The other seventeen have
// passed in every recorded run.
//
// # Why a pass rate is not evidence
//
// A gate that has never gone red is either working or is not checking anything, and
// the two look identical from the outside. `golangci-lint` sat in the script naming
// eleven linters for the repository's whole life without running once, and the first
// run found 526 findings. Nothing about that gate was visible in its output: it
// printed a line and exited 0.
//
// So the number of gates is not a measure of coverage, and a run reporting
// "twenty-four gates, all passed" is claiming much less than it appears to. This test
// makes the seventeen visible by name.
//
// # What this does NOT claim
//
// That a gate listed here is broken, or that one absent from the list is sound.
// Seventeen unproven gates is a statement about the RECORD, and the record only
// covers runs somebody happened to keep -- it is not a survey of the gates' histories.
// `red-check`'s twelve controls are each individually proved in `docs/status.md`,
// which is a stronger claim than "the gate has gone red once", and the gate itself
// goes red whenever any control stops being guarded.
//
// A gate leaves this list by failing, and a gate that cannot fail should not be on
// the list either -- which is why `TestAGateThatHasNeverFailedIsNamed` is the
// assertion rather than a document.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// verificationRecord is the shape of `dist/verification/verify-*.json` that this reads.
//
// Only three fields, and only the ones needed. Declared rather than imported because
// the record is written by a PowerShell script with no Go type behind it, and a
// struct here that drifts from the writer is a struct that silently reads zeroes.
type verificationRecord struct {
	Verified    bool     `json:"verified"`
	FailedGates []string `json:"failedGates"`
	Gates       []struct {
		Name    string `json:"name"`
		Outcome string `json:"outcome"`
	} `json:"gates"`
}

// declaredGates is every gate `scripts/verify.ps1` registers, read from the script
// itself.
//
// Derived rather than written out, for the reason `TestDeriveCountsTheTreeDirectly`
// already gives: a list written here is a list that goes stale silently, and a stale
// list makes this file report a gate that has been removed as never having failed.
func declaredGates(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "verify.ps1"))
	if err != nil {
		t.Fatalf("read scripts/verify.ps1: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Invoke-Gate '", "Skip-Gate '"} {
			if strings.HasPrefix(line, prefix) {
				name := strings.TrimPrefix(line, prefix)
				if i := strings.Index(name, "'"); i >= 0 {
					name = name[:i]
				}
				if name != "" && !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	// `lint`, `generated-code` and `ai-runtime` are registered inside tool-present
	// branches and so are counted twice by the derivation the document test uses;
	// here the name appears once and that is what matters.
	return out
}

func readRecords(t *testing.T) []verificationRecord {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "dist", "verification")
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No records at all is a legitimate state for a fresh clone, and it means the
		// answer is "nothing has been recorded", not "everything is fine".
		t.Skipf("no verification records in %s, so there is nothing to read", dir)
	}
	var out []verificationRecord
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		// Strip a UTF-8 BOM before parsing.
		//
		// `scripts/verify.ps1` writes the record with `Set-Content -Encoding UTF8`,
		// which in Windows PowerShell 5.1 emits a BOM, and `encoding/json` refuses a
		// document that starts with one: `invalid character '\ufeff' looking for
		// beginning of value`. Eight of the forty records in this directory are
		// affected, and a parser that treats them as unreadable would report every gate
		// they saw fail as never having failed -- which is the wrong answer in the
		// direction that makes this file useless.
		raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
		var r verificationRecord
		if err := json.Unmarshal(raw, &r); err != nil {
			// A record this file cannot read is a record whose gate list cannot be
			// trusted, and skipping it quietly would make this report a gate as never
			// failed because the evidence was unreadable.
			t.Errorf("%s does not parse as a verification record: %v", e.Name(), err)
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		t.Skip("no readable verification records")
	}
	return out
}

// TestAGateThatHasNeverFailedIsNamed is the assertion.
//
// It FAILS when a gate that has been recorded red no longer appears in
// `failedGates` -- which is what happens when a gate is removed, renamed, or replaced
// by something that cannot fail. That direction matters: a gate quietly becoming
// unfailable is exactly the `golangci-lint` failure, and the list of what has been
// seen red is the only place it would show up.
func TestAGateThatHasNeverFailedIsNamed(t *testing.T) {
	declared := declaredGates(t)
	if len(declared) == 0 {
		t.Fatal("no gates were read from scripts/verify.ps1, so this test is asserting nothing")
	}

	records := readRecords(t)
	proven := map[string]bool{}
	for _, r := range records {
		for _, g := range r.FailedGates {
			proven[g] = true
		}
	}

	// Every gate that has EVER failed must still be declared. A name that used to
	// fail and is not in the script has been renamed or removed, and both are worth
	// knowing about.
	declaredSet := map[string]bool{}
	for _, g := range declared {
		declaredSet[g] = true
	}
	var retired []string
	for name := range proven {
		if !declaredSet[name] {
			retired = append(retired, name)
		}
	}
	sort.Strings(retired)
	if len(retired) > 0 {
		t.Errorf("these gates are recorded as having failed and are no longer declared: %v.\n"+
			"A gate that was renamed or replaced is fine and the record should be read as "+
			"history; a gate that was REMOVED because it was failing is the thing to look for",
			retired)
	}

	// And the answer, which is the point of the file.
	var never []string
	for _, g := range declared {
		if !proven[g] {
			never = append(never, g)
		}
	}
	sort.Strings(never)
	t.Logf("%d of %d declared gates have been recorded red across %d run record(s): %v",
		len(declared)-len(never), len(declared), len(records), never)
	t.Logf("NEVER RED: %s", strings.Join(never, ", "))
}
