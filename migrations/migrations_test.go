package migrations

import (
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
	for _, m := range all {
		if err := m.IsValid(); err != nil {
			t.Errorf("%s: %v", m.FileName(), err)
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
