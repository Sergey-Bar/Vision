package migrations_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A proposal names a migration file, and a proposal that names a number which has
// already shipped cannot land.
//
// `docs/proposals/hosted-model-attribution.md` proposed
// `0015_observation_hosted_attribution.expand.sql`. By then
// `0015_partitions_and_correlation_indexes.expand.sql` had shipped and the newest
// migration was 0021, so the proposal's number duplicated an applied migration. It
// was invisible to everybody who had not opened `migrations/`, which is everybody
// reading the proposal — and the number is the one thing in a migration plan that
// has to be right before anything else can be.
//
// This is a test over documents, not over code. It is here because the proposal is
// the specification of what gets built, and a specification with a colliding
// identifier is a defect in the specification. It does not check that the
// proposal's *SQL* is right, or that the proposal should be built, or that the
// CHECK it adds is compatible — those are a human reading the diff.

// migrationFile matches the filename form, which is what the runner applies and
// what therefore has to be unique.
var migrationFile = regexp.MustCompile(`\b(\d{4})_[a-z0-9_]+\.expand\.sql\b`)

const proposalsDir = "../docs/proposals"

// TestEveryProposalNamesAMigrationThatCanStillLand walks the proposals directory
// rather than the one file, because the next proposal will have the same failure
// mode and a test about one document is a test that has to be remembered.
func TestEveryProposalNamesAMigrationThatCanStillLand(t *testing.T) {
	entries, err := os.ReadDir(proposalsDir)
	if err != nil {
		t.Fatalf("read %s: %v", proposalsDir, err)
	}

	applied, err := appliedMigrations()
	if err != nil {
		t.Fatalf("read the applied migrations: %v", err)
	}
	free := nextFreeNumber(t, applied)

	proposalsChecked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proposalsDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		proposalsChecked++

		// A proposed migration is one this proposal would *add*: a filename that is
		// not already on disk. Filenames that are on disk are references — the
		// proposal explaining which number it collided with, for instance — and
		// matching those would fail the very sentence that documents the fix.
		var proposed []string
		for _, m := range migrationFile.FindAllStringSubmatch(string(raw), -1) {
			if !applied[m[0]] {
				proposed = append(proposed, m[0])
			}
		}
		for _, name := range proposed {
			number := migrationFile.FindStringSubmatch(name)[1]
			if n, err := strconv.Atoi(number); err == nil && n != free {
				t.Errorf("%s proposes %s. The next migration is %04d — a proposal that reuses a "+
					"shipped number cannot be applied at all, and one that skips ahead leaves a gap "+
					"in the sequence a reader has to account for",
					entry.Name(), name, free)
			}
		}
	}
	if proposalsChecked == 0 {
		t.Fatalf("no proposal was read from %s, so this test is passing without having looked "+
			"at anything", proposalsDir)
	}
}

// The compatibility window and the directory have to agree, or "the next migration
// is 0022" is advice derived from whichever of the two a reader reaches first —
// and a build that trusts the wrong one refuses to start against a correctly
// migrated database.
func TestCompatWindowMatchesTheMigrationsOnDisk(t *testing.T) {
	raw, err := os.ReadFile("compat.json")
	if err != nil {
		t.Fatalf("read compat.json: %v", err)
	}
	var compat struct {
		MinSchemaVersion int `json:"minSchemaVersion"`
		MaxSchemaVersion int `json:"maxSchemaVersion"`
	}
	if err := json.Unmarshal(raw, &compat); err != nil {
		t.Fatalf("decode compat.json: %v", err)
	}
	applied, err := appliedMigrations()
	if err != nil {
		t.Fatalf("read the applied migrations: %v", err)
	}
	if got, want := latest(t, applied), compat.MaxSchemaVersion; got != want {
		t.Errorf("compat.json says maxSchemaVersion %d; the newest migration on disk is %d. One of "+
			"the two is stale", want, got)
	}
	if got, want := latest(t, applied)+1, compat.MaxSchemaVersion+1; got != want {
		t.Errorf("internal inconsistency: latest is %d and next is %d", got, want)
	}
}

// TestStatusReportsTheSchemaVersionThatIsActuallyShipped.
//
// `docs/status.md` is this repository's only source of truth for build state, and
// it carried "Schema version is now 17" for as long as it was true — and then for
// four migrations after it stopped being true. Nothing read the sentence, because
// nothing could check it against `compat.json`, so the number simply rotted in
// place.
//
// The check is deliberately narrow: it looks for a phrase that *reports* the
// current version, and refuses the ones that would make this test impossible to
// satisfy — a document that also narrates the history has to say which number is
// the claim.
func TestStatusReportsTheSchemaVersionThatIsActuallyShipped(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docs", "status.md"))
	if err != nil {
		t.Fatalf("read docs/status.md: %v", err)
	}
	compatRaw, err := os.ReadFile("compat.json")
	if err != nil {
		t.Fatalf("read compat.json: %v", err)
	}
	var compat struct {
		MaxSchemaVersion int `json:"maxSchemaVersion"`
	}
	if err := json.Unmarshal(compatRaw, &compat); err != nil {
		t.Fatalf("decode compat.json: %v", err)
	}
	applied, err := appliedMigrations()
	if err != nil {
		t.Fatalf("read the applied migrations: %v", err)
	}
	want := latest(t, applied)
	if compat.MaxSchemaVersion != want {
		t.Fatalf("compat.json says %d and the tree is at %d; this test cannot say which is "+
			"authoritative", compat.MaxSchemaVersion, want)
	}

	doc := string(raw)
	// Case-insensitive, because a sentence at the start of a line is capitalised and
	// the one this test replaced was capitalised too.
	claim := regexp.MustCompile(`(?i)schema version is now (\d+)`)
	found := claim.FindStringSubmatch(doc)
	if found == nil {
		t.Fatal("docs/status.md no longer states a schema version. It is this repository's " +
			"source of truth for build state, and the number is the one thing in it that has to " +
			"agree with migrations/compat.json")
	}
	if got := found[1]; got != strconv.Itoa(want) {
		t.Errorf("docs/status.md says \"schema version is now %s\"; migrations/ is at %d. One of "+
			"them is describing a repository that does not exist", got, want)
	}
}

// appliedMigrations is the set of shipped migration filenames, read from the
// directory rather than from compat.json, because the filenames are what the
// runner applies.
func appliedMigrations() (map[string]bool, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, entry := range entries {
		if m := migrationFile.FindStringSubmatch(entry.Name()); m != nil {
			out[m[0]] = true
		}
	}
	return out, nil
}

func latest(t *testing.T, applied map[string]bool) int {
	t.Helper()
	highest := 0
	for name := range applied {
		n, err := strconv.Atoi(migrationFile.FindStringSubmatch(name)[1])
		if err != nil {
			t.Fatalf("migration %q does not carry a numeric version", name)
		}
		if n > highest {
			highest = n
		}
	}
	return highest
}

func nextFreeNumber(t *testing.T, applied map[string]bool) int {
	t.Helper()
	return latest(t, applied) + 1
}
