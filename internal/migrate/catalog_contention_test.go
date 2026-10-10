package migrate

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// isCatalogContention was added in the same commit as the retry behaviour it
// guards and grew no test. It is pure, which makes the whole retry decision
// testable without a database — and it decides whether a migration failure is
// retried silently or reported, so a predicate that cannot distinguish the two
// is a predicate that would hide a real failure for four seconds and then log
// something misleading about it.

// contentionMessage is the one message phrase the retry means. It is spelled out
// here rather than read back off the PgError below, so that what this test
// asserts is visible in the test and not only in the constant.
const contentionMessage = "tuple concurrently updated in relation \"pg_authid\""

func pgErr(code, message string) error {
	return &pgconn.PgError{Code: code, Message: message}
}

func TestIsCatalogContention(t *testing.T) {
	// The exact pair the migrator sees when CREATE ROLE races another session.
	// Postgres reports `tuple concurrently updated` under SQLSTATE XX000, the
	// generic internal-error state, which is precisely why the message check is
	// there at all: the state alone covers hundreds of failures this retry has
	// nothing to do with.
	contention := pgErr("XX000", contentionMessage)

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"the contention itself", contention, true},
		{"nil error", nil, false},

		// 40001 is a serialization failure — a retry-able conflict, and the code
		// this predicate would use if it only read the state. It must NOT match,
		// or a serialization failure in the migration SQL would be retried as if
		// it were catalog contention and reported with the wrong reason.
		{"serialization failure", pgErr("40001", contentionMessage), false},
		{"right message wrong state", pgErr("42601", contentionMessage), false},

		// A different internal error under the same SQLSTATE. This is the case
		// the message check exists for: `XX000` is the entire internal-error
		// class, and without the message half of the predicate a genuine failure
		// would be retried four times over four seconds and then reported as
		// contention — a delayed failure with the wrong reason on it.
		{"internal error, not contention", pgErr("XX000", "cache lookup failed for relation 12345"), false},
		{"empty message under the state", pgErr("XX000", ""), false},

		// A non-Postgres error must not match, or every ordinary failure would be
		// retried and then reported as contention.
		{"plain error", errors.New("connection refused"), false},
		{"wrapped plain error", fmt.Errorf("migrate: %w", errors.New("connection refused")), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCatalogContention(tt.err); got != tt.want {
				t.Errorf("isCatalogContention(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestIsCatalogContentionUnwraps is the case that decides whether a wrapped
// failure is retried. The migrator composes errors as it walks the chain, so a
// PgError one level down is the normal shape rather than the exception.
func TestIsCatalogContentionUnwraps(t *testing.T) {
	contention := pgErr("XX000", contentionMessage)

	wrapped := fmt.Errorf("migrate: apply 0008: %w", contention)
	if !isCatalogContention(wrapped) {
		t.Error("a PgError wrapped once was not recognised as catalog contention, so the " +
			"migrator's own error composition would defeat its retry decision")
	}

	doubleWrapped := fmt.Errorf("migrate: apply 0008: %w", wrapped)
	if !isCatalogContention(doubleWrapped) {
		t.Error("a PgError wrapped twice was not recognised as catalog contention")
	}
}
