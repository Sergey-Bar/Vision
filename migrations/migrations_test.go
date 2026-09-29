package migrations

import (
	"errors"
	"strings"
	"testing"
)

func TestAllLoadsAndValidates(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("All returned no migrations; the release would install an empty schema")
	}

	seen := map[int]bool{}
	prev := 0
	carried := CarriedContractViolations()
	for _, m := range all {
		// A migration is either valid, or carries a violation that was recorded
		// in grandfatheredContractViolations before this check existed. It cannot
		// be anything else: All() above returns an error for a file that is not
		// in that list and does not validate, so reaching here at all means the
		// set is well-formed. The exception is keyed by file name and is
		// asserted to be exactly the known set in
		// TestGrandfatheredViolationsAreExactlyTheKnownOnes.
		if err := m.IsValid(); err != nil {
			var contractErr *ContractPhaseError
			if _, ok := carried[m.FileName()]; !ok || !errors.As(err, &contractErr) {
				t.Errorf("%s: %v", m.FileName(), err)
			}
		}
		if m.Version <= prev {
			t.Errorf("migrations are not in ascending order: %d after %d", m.Version, prev)
		}
		if seen[m.Version] {
			t.Errorf("duplicate version %d", m.Version)
		}
		seen[m.Version] = true
		prev = m.Version
		if len(m.Checksum) != 64 {
			t.Errorf("%s: checksum is %d chars, want 64", m.FileName(), len(m.Checksum))
		}
	}
}

// The expand/contract discipline is a property of the file layout, so it is
// testable without a database. A contract step bundled into its expand is the
// single mistake that makes an in-place upgrade unsafe, and it is easy to make
// by accident when renaming files.
func TestM1ContainsNoContractStep(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, m := range all {
		if m.Phase == PhaseContract {
			t.Errorf("%s is a contract step. M1 ships expand steps only; a contract "+
				"step must not ship until the expand it completes has been observed in the field",
				m.FileName())
		}
	}
}

func TestParseFileName(t *testing.T) {
	cases := []struct {
		in      string
		version int
		name    string
		phase   string
		wantErr bool
	}{
		{"0001_tenancy.expand.sql", 1, "tenancy", "expand", false},
		{"0007_audit.contract.sql", 7, "audit", "contract", false},
		{"0042_a.b.backfill.sql", 42, "a.b", "backfill", false},
		{"1_tenancy.expand.sql", 1, "tenancy", "expand", false},
		{"tenancy.expand.sql", 0, "", "", true},
		{"0001_tenancy.sql", 0, "", "", true},
		{"0001_tenancy.migrated.sql", 1, "tenancy", "migrated", false}, // phase validated later
		{"abc_name.expand.sql", 0, "", "", true},
	}
	for _, tc := range cases {
		v, n, p, err := parseFileName(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected an error, got version=%d name=%q phase=%q", tc.in, v, n, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if v != tc.version || n != tc.name || p != tc.phase {
			t.Errorf("%s: got (%d, %q, %q), want (%d, %q, %q)", tc.in, v, n, p, tc.version, tc.name, tc.phase)
		}
	}
}

func TestIsValidRejectsUnknownPhase(t *testing.T) {
	m := Migration{Version: 1, Name: "x", Phase: "migrated", SQL: "SELECT 1"}
	if err := m.IsValid(); err == nil {
		t.Fatal("expected an unknown phase to be rejected")
	} else if !strings.Contains(err.Error(), "migrated") {
		t.Errorf("error should name the offending phase, got %q", err)
	}
}

// The phase used to be a filename label and nothing more. IsValid checked that
// the string was one of three known words and never opened the SQL, so a
// `DROP COLUMN` in a file called `.expand.sql` validated and was applied. Each
// case below is contract DDL in a non-contract file, and each must be rejected.
func TestIsValidRejectsContractDDLInANonContractFile(t *testing.T) {
	cases := []struct {
		name  string
		phase Phase
		sql   string
	}{
		{"drop table in an expand", PhaseExpand, `DROP TABLE old_events;`},
		{"drop table if exists in an expand", PhaseExpand, `DROP TABLE IF EXISTS old_events CASCADE;`},
		{"drop column in an expand", PhaseExpand, `ALTER TABLE sites DROP COLUMN default_locale;`},
		{"drop column in a backfill", PhaseBackfill, `ALTER TABLE sites DROP COLUMN IF EXISTS x;`},
		{"truncate in an expand", PhaseExpand, `TRUNCATE events;`},
		{"alter table type in an expand", PhaseExpand, `ALTER TABLE events ALTER COLUMN bigint TYPE numeric;`},
		{"alter table set data type in an expand", PhaseExpand, `ALTER TABLE events ALTER c SET DATA TYPE text;`},
		{"alter table rename in an expand", PhaseExpand, `ALTER TABLE events RENAME TO events_v2;`},
		{"alter table rename column in an expand", PhaseExpand, `ALTER TABLE events RENAME COLUMN a TO b;`},
		{"drop table lower case", PhaseExpand, `drop table old_events;`},
		{"drop table after other statements", PhaseExpand, "CREATE INDEX i ON events (a);\nDROP TABLE old_events;\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Migration{Version: 1, Name: "x", Phase: tc.phase, SQL: tc.sql}
			err := m.IsValid()
			if err == nil {
				t.Fatalf("contract DDL in a %s file was accepted: %s", tc.phase, tc.sql)
			}
			var contractErr *ContractPhaseError
			if !errors.As(err, &contractErr) {
				t.Fatalf("error is %T, want *ContractPhaseError, so All() cannot tell this "+
					"from a rejection: %v", err, err)
			}
		})
	}
}

// The other half, and the half that decides whether the check is usable. A
// keyword search over SQL that was not stripped of its comments and its literals
// refuses valid migrations, and a validator that refuses valid migrations gets
// worked around. Every case here is a migration somebody could reasonably
// write, and the word appears in it somewhere other than as a keyword.
func TestIsValidAcceptsKeywordsInsideIdentifiersAndLiterals(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"a column named dropped_at", `CREATE TABLE evidence (dropped_at TIMESTAMPTZ);`},
		{"a column named truncate_date", `CREATE TABLE jobs (truncate_date DATE);`},
		{"a column named type", `CREATE TABLE t (type TEXT, drop_state TEXT);`},
		{"a table named drop_logs", `CREATE TABLE drop_logs (a int);`},
		{"the word in a line comment", "-- we used to DROP TABLE events here\nCREATE TABLE t (a int);"},
		{"the word in a block comment", "/* DROP TABLE events;\n   TRUNCATE everything; */\nCREATE TABLE t (a int);"},
		{"the word in a string literal", `INSERT INTO rules (title) VALUES ('do not drop table events');`},
		{"the word in a literal with an escaped quote", `INSERT INTO rules (title) VALUES ('it''s fine: drop table x');`},
		{"the word in a quoted identifier", `CREATE TABLE "drop table x" (a int);`},
		{"a hyphenated table name", `CREATE TABLE drop-table-old (a int);`},
		{"drop constraint is not contract ddl", `ALTER TABLE outbox_events DROP CONSTRAINT uq;`},
		{"drop index is not contract ddl", `DROP INDEX CONCURRENTLY old_idx;`},
		{"add column with a type is not a type change", `ALTER TABLE events ADD COLUMN kind TEXT;`},
		{"add constraint is not contract ddl", `ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0);`},
		{"create or replace function", `CREATE OR REPLACE FUNCTION f() RETURNS int AS $$ SELECT 1 $$ LANGUAGE sql;`},
		// The documented false negative, asserted rather than left to a comment.
		// The string inside EXECUTE is blanked as a literal, so a DO block that
		// drops a table is not seen. Accepting it is the conservative failure:
		// the alternative is parsing PL/pgSQL, and a validator that refuses
		// valid migrations gets worked around. See checkPhaseDiscipline.
		{"contract ddl inside a do block is a documented false negative", `DO $$ BEGIN EXECUTE 'DROP TABLE x'; END $$;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Migration{Version: 1, Name: "x", Phase: PhaseExpand, SQL: tc.sql}
			if err := m.IsValid(); err != nil {
				t.Fatalf("a valid expand was refused: %v\nSQL: %s", err, tc.sql)
			}
		})
	}
}

// The same rule, in the one file it must not apply to: a contract step is
// allowed to do contract DDL. That is the entire point of the phase.
func TestIsValidAllowsContractDDLInAContractFile(t *testing.T) {
	for _, sql := range []string{
		`ALTER TABLE sites DROP COLUMN default_locale;`,
		`DROP TABLE old_events;`,
		`TRUNCATE events;`,
		`ALTER TABLE events ALTER COLUMN bigint TYPE numeric;`,
		`ALTER TABLE events RENAME TO events_v2;`,
	} {
		m := Migration{Version: 99, Name: "x", Phase: PhaseContract, SQL: sql}
		if err := m.IsValid(); err != nil {
			t.Errorf("a contract step was refused for doing contract DDL (%s): %v", sql, err)
		}
	}
}

// A grandfathered entry is a recorded debt, not a policy knob. This asserts the
// one that exists is the one the history says exists, so the list cannot grow by
// accident and so that removing the offending file makes this test point at the
// stale entry.
func TestGrandfatheredViolationsAreExactlyTheKnownOnes(t *testing.T) {
	carried := CarriedContractViolations()
	if len(carried) != len(grandfatheredContractViolations) {
		t.Fatalf("CarriedContractViolations returned %d entries, want %d",
			len(carried), len(grandfatheredContractViolations))
	}

	all, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	live := map[string]bool{}
	for _, m := range all {
		live[m.FileName()] = true
	}
	for name := range carried {
		if !live[name] {
			t.Errorf("%s is grandfathered but is not in the migration set; remove the entry", name)
		}
	}

	// And the entry must actually be needed: without it, 0010 does not load.
	// If that ever stops being true, the exemption is dead weight.
	var ten *Migration
	for i := range all {
		if all[i].FileName() == "0010_drop_default_locale.expand.sql" {
			ten = &all[i]
		}
	}
	if ten == nil {
		t.Fatal("0010 is missing from the migration set; this test no longer describes anything")
	}
	var contractErr *ContractPhaseError
	if err := ten.IsValid(); !errors.As(err, &contractErr) {
		t.Errorf("0010 no longer carries contract DDL (%v), so its grandfather entry should be removed", err)
	}
}

func TestReleaseCompatibilityWindowCoversMigrations(t *testing.T) {
	compat, err := ReleaseCompatibility()
	if err != nil {
		t.Fatalf("ReleaseCompatibility: %v", err)
	}
	latest, err := LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest > compat.MaxSchemaVersion {
		t.Errorf("migration %d is outside the declared window [%d, %d]; "+
			"a binary built from this tree would refuse to install its own schema",
			latest, compat.MinSchemaVersion, compat.MaxSchemaVersion)
	}
	if compat.MinSchemaVersion > 1 {
		t.Errorf("minSchemaVersion is %d, but version 1 exists. A window that excludes the "+
			"first migration makes the initial install impossible", compat.MinSchemaVersion)
	}
}

// The embedded manifest and the file on disk are two copies of one fact. The
// release verifier reads the file; the runtime reads the embed. If they drift,
// a site verifies a window the binary does not honour.
func TestEmbeddedAndOnDiskManifestsAgree(t *testing.T) {
	embedded, err := ReleaseCompatibility()
	if err != nil {
		t.Fatalf("ReleaseCompatibility: %v", err)
	}
	onDisk, err := LoadCompatibilityFromFile(compatFile)
	if err != nil {
		t.Fatalf("LoadCompatibilityFromFile(%s): %v", compatFile, err)
	}
	if embedded != onDisk {
		t.Errorf("compat.json on disk is %+v but the embedded copy is %+v", onDisk, embedded)
	}
}

// Editing an applied migration is the mistake the checksum exists to catch.
// Here we assert the mechanism: any byte change produces a different checksum.
func TestChecksumDetectsAnyEdit(t *testing.T) {
	original := checksum([]byte("CREATE TABLE t (a int);"))
	edited := checksum([]byte("CREATE TABLE t (a int); "))
	if original == edited {
		t.Fatal("checksum did not change after an edit")
	}
	if len(original) != 64 {
		t.Fatalf("checksum is %d chars, want 64", len(original))
	}
}
