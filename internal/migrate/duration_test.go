package migrate_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// schema_migrations.duration_ms is an INTEGER count of milliseconds, and
// time.Duration is a count of nanoseconds. Scanning the column straight into a
// time.Duration read a 1.5 second migration back as 1500 nanoseconds, which
// DurationS then rendered as "1.5µs" on the operator-facing migration status —
// a report that says every migration on a site took less time than a disk seek.
//
// The value is seeded rather than produced by the run, because a migration that
// applies in under a millisecond records 0 and 0 is indistinguishable from the
// bug. A seeded 1500ms is not: it is either 1.5s or it is 1.5-s, and only one of
// those is a duration.
func TestAppliedReportsDurationInMillisecondsNotNanoseconds(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	testutil.Exec(t, pool, `UPDATE schema_migrations SET duration_ms = 1500 WHERE version = 1`)

	applied, err := runner.Applied(ctx)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	var first migrate.Applied
	for _, a := range applied {
		if a.Version == 1 {
			first = a
		}
	}
	if first.Version != 1 {
		t.Fatal("version 1 is not in the ledger")
	}

	want := 1500 * time.Millisecond
	if first.Duration != want {
		t.Errorf("a row recording 1500ms reported a duration of %v (%s), want %v (%s). "+
			"duration_ms is milliseconds and time.Duration is nanoseconds; reading the "+
			"column without converting divides every reported time by a million",
			first.Duration, first.DurationS, want, want)
	}
	if first.DurationS != "1.5s" {
		t.Errorf("DurationS is %q, want %q; this is the string an operator reads", first.DurationS, "1.5s")
	}
	// The two fields are rendered from the same value, and they must agree.
	if parsed, err := time.ParseDuration(first.DurationS); err != nil {
		t.Errorf("DurationS %q does not parse as a duration: %v", first.DurationS, err)
	} else if parsed != first.Duration {
		t.Errorf("Duration is %v but DurationS parses to %v", first.Duration, parsed)
	}
}

// The unit bug was invisible because nothing asserted the reported durations of a
// real run against the wall clock that produced them. Every value the ledger
// reports has to be a whole number of milliseconds, and none of them can exceed
// the time the run actually took - a sum that overflows the measured wall clock
// is a conversion error even when each individual value looks plausible.
func TestAppliedDurationsAreWholeMillisecondsWithinTheRunItMeasured(t *testing.T) {
	pool := testutil.Pool(t)
	testutil.Reset(t)
	ctx := context.Background()
	runner := &migrate.Runner{Pool: pool}

	start := time.Now()
	if _, err := runner.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	elapsed := time.Since(start)

	applied, err := runner.Applied(ctx)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("the ledger is empty after a full run")
	}

	// Slack for the time between the run returning and the clock being read, and
	// for the ledger query itself.
	slack := 5 * time.Second
	var total time.Duration
	for _, a := range applied {
		if a.Duration%time.Millisecond != 0 {
			t.Errorf("version %d: reported duration %v is not a whole number of milliseconds",
				a.Version, a.Duration)
		}
		if a.Duration < 0 {
			t.Errorf("version %d: reported a negative duration %v", a.Version, a.Duration)
		}
		total += a.Duration
	}

	// A conversion that reads milliseconds as nanoseconds makes the total
	// smaller, so the ceiling is where the other-direction bug shows. A
	// conversion that multiplies twice makes it larger, and a run of 18
	// migrations would then report more time than the wall clock spent. A total
	// of zero is not asserted against: on a fast machine every step can land
	// under a millisecond and record 0, which is the ledger rounding rather than
	// the ledger lying. The seeded test above is what pins the unit.
	if total > elapsed+slack {
		t.Errorf("the ledger reports %v of migration time for a run that took %v", total, elapsed)
	}
}
