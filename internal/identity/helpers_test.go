package identity_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/testutil"
)

// testutilQuery runs a query against the test database and hands back the row
// scanner, so a test that only needs to confirm a stored row does not have to
// spell out the boilerplate.
func testutilQuery(t *testing.T, sql string, args ...any) pgx.Row {
	t.Helper()
	return testutil.Pool(t).QueryRow(context.Background(), sql, args...)
}
