package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// The checksum hashes the migration body only, on purpose: changing the digest
// format would make every already-deployed site refuse to upgrade, and nothing
// can UPDATE schema_migrations to re-record it, because the migration is the
// thing being blocked. The identity the digest omits — the file's own name and
// phase — is already recorded in the ledger, and is compared instead.
//
// These two tests are the controls for those comparisons. Each removes a
// comparison and the test fails, which is what makes the comparison live rather
// than decorative.

// A renamed applied migration is refused. The bytes are untouched, so the
// checksum alone would happily pass it.
func TestVerifyIdentityCatchesNameMismatch(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	testutil.Exec(t, pool, `UPDATE schema_migrations SET name = $1 WHERE version = 1`, "renamed_by_hand")

	err := runner.VerifyChecksums(ctx)
	if err == nil {
		t.Fatal("a renamed applied migration was not detected")
	}
	if !strings.Contains(err.Error(), "name mismatch") {
		t.Errorf("error should name the name mismatch, got %q", err)
	}

	// The same tampering must stop `up` itself, not just the status command:
	// `up` is the path that applies DDL.
	if _, err := runner.Up(ctx); err == nil {
		t.Error("Up proceeded against a ledger with a renamed migration")
	} else if !strings.Contains(err.Error(), "name mismatch") {
		t.Errorf("Up error should name the name mismatch, got %q", err)
	}
}

// A phase rewritten after the fact is refused. Phase is the expand/contract
// discipline's only record, so a rewritten one is a lie about what a site ran.
func TestVerifyIdentityCatchesPhaseMismatch(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Every migration in this build is an expand, and the column's CHECK
	// constraint accepts backfill and contract, so this is a value the database
	// itself would have accepted at insert time.
	testutil.Exec(t, pool, `UPDATE schema_migrations SET phase = $1 WHERE version = 1`, "contract")

	err := runner.VerifyChecksums(ctx)
	if err == nil {
		t.Fatal("a rewritten migration phase was not detected")
	}
	if !strings.Contains(err.Error(), "phase mismatch") {
		t.Errorf("error should name the phase mismatch, got %q", err)
	}

	if _, err := runner.Up(ctx); err == nil {
		t.Error("Up proceeded against a ledger with a rewritten phase")
	} else if !strings.Contains(err.Error(), "phase mismatch") {
		t.Errorf("Up error should name the phase mismatch, got %q", err)
	}
}

// The three failures must be distinguishable by their message, or a human
// triaging a refused upgrade cannot tell which of the bytes, the name, or the
// phase moved.
func TestVerifyIdentityErrorsAreDistinguishable(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	cases := []struct {
		name    string
		tamper  string
		wantErr string
	}{
		{"bytes", `UPDATE schema_migrations SET checksum = repeat('0', 64) WHERE version = 1`, "checksum mismatch"},
		{"name", `UPDATE schema_migrations SET name = 'renamed_by_hand' WHERE version = 1`, "name mismatch"},
		{"phase", `UPDATE schema_migrations SET phase = 'contract' WHERE version = 1`, "phase mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Reset(t)
			if _, err := runner.Up(ctx); err != nil {
				t.Fatalf("Up: %v", err)
			}
			testutil.Exec(t, pool, tc.tamper)

			err := runner.VerifyChecksums(ctx)
			if err == nil {
				t.Fatalf("tampering with the %s was not detected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error should name the %s mismatch, got %q", tc.name, err)
			}
			for _, other := range cases {
				if other.name == tc.name {
					continue
				}
				if strings.Contains(err.Error(), other.wantErr) {
					t.Errorf("a %s mismatch reported as %q, which names a different field", tc.name, other.wantErr)
				}
			}
		})
	}
}
