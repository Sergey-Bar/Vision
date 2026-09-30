package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func acceptOne(t *testing.T, s *Store, d Detection) {
	t.Helper()
	if err := s.Accept(context.Background(), d); err != nil {
		t.Fatalf("accept %s: %v", d.ID, err)
	}
}

// TestTheFamilyIsItsOwnColumnAndNotPartOfTheClass is T7.1, and the reason is
// stated as a query: an Alarm Definition is written against the class, so the
// class column has to keep matching the class the model emitted no matter what
// happens to the display format or the taxonomy.
func TestTheFamilyIsItsOwnColumnAndNotPartOfTheClass(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.StartRun(ctx, Run{ID: "r1", Label: "family", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	acceptOne(t, s, Detection{
		ID: "d1", RunID: "r1", At: time.Now(), Class: "car", Family: "vehicle",
		TrackID: 1, Score: 0.9, BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2,
		Direction: "north", AIState: "asked_answered", Provenance: ProvenanceSynthetic,
	})
	got, err := s.Get(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Class != "car" {
		t.Errorf("the class round-tripped as %q; the class column must be the model's own word and nothing else", got.Class)
	}
	if got.Family != "vehicle" {
		t.Errorf("family = %q, want vehicle", got.Family)
	}
	// The decisive assertion: the class is still queryable as itself. If the two
	// had been stored as one string, this is the query that breaks.
	var n int
	if err := s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM detections WHERE run_id=? AND class=?`, "r1", "car").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("`WHERE class = 'car'` found %d rows, want 1. Storing the class and the family as one string breaks every existing rule, silently", n)
	}
	// And the family is queryable on its own, which is the other half.
	byFamily, err := s.CountByClassFamily(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if byFamily["car"]["vehicle"] != 1 {
		t.Errorf("by class and family = %v, want car/vehicle = 1", byFamily)
	}
	if byFamily["car (vehicle)"] != nil {
		t.Error("the class column contains the family: the two are one string, and the rule above would have stopped matching")
	}
}

// TestAnAbsentFamilyIsNotTheFamilyOther: the nullable column's null is a
// different claim from a value. A detection written before the column existed has
// no family recorded; that is not the same as saying its family is `other`, and
// collapsing the two would make an old database look like a run whose classes were
// all unmapped.
func TestAnAbsentFamilyIsNotTheFamilyOther(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.StartRun(ctx, Run{ID: "r2", Label: "nofamily", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	acceptOne(t, s, Detection{
		ID: "d1", RunID: "r2", At: time.Now(), Class: "car",
		TrackID: 1, Score: 0.9, BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2,
		Direction: "north", AIState: "asked_answered", Provenance: ProvenanceSynthetic,
	})
	got, err := s.Get(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Family != "" {
		t.Errorf("family = %q for a detection written without one; an absent family and the family `other` are different claims", got.Family)
	}
	// The row stores NULL, not an empty string, so a query can tell them apart.
	var isNull bool
	if err := s.DB().QueryRowContext(ctx,
		`SELECT family IS NULL FROM detections WHERE id=?`, "d1").Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if !isNull {
		t.Error("the family column holds an empty string rather than NULL: a query cannot tell 'not recorded' from 'recorded as none'")
	}
}

// TestTheSnapshotNeedsAKindAndTheDetectionNeedsToExist: T6.3 and T7.3 together.
// A reference with no kind is a file a reader opens not knowing what it is, and a
// snapshot recorded against a detection that does not exist leaves a file on disk
// that nothing points at.
func TestTheSnapshotNeedsAKindAndTheDetectionNeedsToExist(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.StartRun(ctx, Run{ID: "r3", Label: "snapshot", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:1", ClockSource: "host", StartedAt: time.Now(), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	acceptOne(t, s, Detection{
		ID: "d1", RunID: "r3", At: time.Now(), Class: "car", Family: "vehicle",
		TrackID: 1, Score: 0.9, BoxTop: 1, BoxLeft: 1, BoxBottom: 2, BoxRight: 2,
		Direction: "north", AIState: "asked_answered", Provenance: ProvenanceSynthetic,
	})

	if err := s.SetSnapshot(ctx, "d1", "", EvidenceCrossingSnapshot); err == nil {
		t.Error("an empty snapshot reference was accepted; use the absence of a call to mean 'no snapshot'")
	}
	for _, bad := range []string{"", "picture", "jpeg", "snapshot"} {
		if err := s.SetSnapshot(ctx, "d1", "ab/cd.jpg", bad); err == nil {
			t.Errorf("the evidence kind %q was accepted; a reader cannot look it up", bad)
		} else if !strings.Contains(err.Error(), "crossing_snapshot") {
			t.Errorf("the refusal for kind %q does not list the three kinds: %v", bad, err)
		}
	}
	if err := s.SetSnapshot(ctx, "d1", "ab/cd.jpg", EvidenceCrossingSnapshot); err != nil {
		t.Fatalf("a valid snapshot was refused: %v", err)
	}
	if err := s.SetSnapshot(ctx, "nope", "ab/cd.jpg", EvidenceCrossingSnapshot); err == nil {
		t.Error("a snapshot was recorded against a detection that does not exist; the file would be on the medium with nothing pointing at it")
	}

	got, err := s.Get(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotRef != "ab/cd.jpg" || got.SnapshotKind != EvidenceCrossingSnapshot {
		t.Errorf("snapshot = %q/%q, want ab/cd.jpg/%s", got.SnapshotRef, got.SnapshotKind, EvidenceCrossingSnapshot)
	}
	// A detection with no snapshot is a visible state, not an error: the snapshot
	// is written immediately after the event and a crash between the two leaves
	// the event with nothing. That has to be readable as "no snapshot" rather
	// than as a claim that a picture exists.
	var nullRef bool
	if err := s.DB().QueryRowContext(ctx,
		`SELECT snapshot_ref IS NULL FROM detections WHERE id=?`, "d1").Scan(&nullRef); err != nil {
		t.Fatal(err)
	}
	if nullRef {
		t.Error("snapshot_ref is NULL after a snapshot was recorded")
	}
}

// TestAnOlderDataRootGainsTheColumns: the expand step. A seven-day run's database
// already exists, and a build that only edited the CREATE TABLE would find the
// column missing on precisely the database people are trying to read evidence out
// of.
func TestAnOlderDataRootGainsTheColumns(t *testing.T) {
	dir := t.TempDir()
	// Build a database with the PRE-T7.1 detections table and nothing else this
	// release adds, then open it with the current code.
	legacy := filepath.Join(dir, "legacy.db")
	s, err := Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	// Drop the three columns to simulate the older shape. SQLite 3.35+ supports
	// DROP COLUMN; the runner's SQLite is newer, and the alternative — building
	// the legacy schema by hand — would be a second copy of the schema to keep in
	// step with the first.
	for _, c := range []string{"family", "snapshot_ref", "snapshot_kind"} {
		if _, err := s.DB().Exec(`ALTER TABLE detections DROP COLUMN ` + c); err != nil {
			t.Skipf("this SQLite cannot DROP COLUMN (%v), so the expand path cannot be exercised here", err)
		}
	}
	_ = s.Close()

	reopened, err := Open(legacy)
	if err != nil {
		t.Fatalf("reopening a data root that predates the columns failed: %v", err)
	}
	defer reopened.Close()
	for _, c := range []string{"family", "snapshot_ref", "snapshot_kind"} {
		has, err := reopened.hasColumn(context.Background(), "detections", c)
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Errorf("detections.%s was not added to an existing data root: a seven-day run's database would lose the family and the snapshot", c)
		}
	}
}

// TestTheSchemaVersionMoves: a data root's version is how its state is knowable,
// and a migration that does not bump it is a migration nobody can reason about
// after the fact.
func TestTheSchemaVersionMoves(t *testing.T) {
	if schemaVersion < 4 {
		t.Fatalf("schemaVersion is %d; the family, snapshot_ref and snapshot_kind columns are schema version 4", schemaVersion)
	}
}
