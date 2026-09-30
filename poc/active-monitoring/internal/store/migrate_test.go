package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestAnOldDataRootIsMigratedRatherThanRefused: `CREATE TABLE IF NOT EXISTS`
// does not add a column, so the first release that added `samples.db_bytes`
// failed against any data root that already existed with the message "table
// samples has no column named db_bytes". That message reads like a bug and
// costs an hour, and it cost one.
func TestAnOldDataRootIsMigratedRatherThanRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "poc.db")

	// An "old" database: the current tables, EXCEPT `samples`, which is created
	// here in the pre-migration shape. Running the full schema first would
	// already create the new shape and prove nothing.
	oldDB := openRaw(t, path)
	if _, err := oldDB.ExecContext(context.Background(), oldSchemaOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.ExecContext(context.Background(),
		`CREATE TABLE samples (run_id TEXT NOT NULL, at TEXT NOT NULL, rss_bytes INTEGER NOT NULL,
		 cpu_percent REAL NOT NULL, ai_p99_ms REAL NOT NULL, pipe_depth INTEGER NOT NULL,
		 disk_bytes INTEGER NOT NULL, PRIMARY KEY (run_id, at))`); err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.ExecContext(context.Background(),
		`INSERT INTO samples (run_id,at,rss_bytes,cpu_percent,ai_p99_ms,pipe_depth,disk_bytes)
		 VALUES ('r1','2026-09-30T10:00:00Z',1,2,3,4,5)`); err != nil {
		t.Fatal(err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatal(err)
	}

	// The new build opens it.
	s, err := OpenPath(path)
	if err != nil {
		t.Fatalf("the new build refused an old data root: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.RunByID(ctx, "nope"); err == nil {
		t.Fatal("the run table is unreadable, so the migration broke something")
	}
	// The column exists and the old row survived.
	if err := s.PutSample(ctx, "r1", Sample{At: time.Now(), RSSBytes: 99, DB: 1234}); err != nil {
		t.Fatalf("the migrated database still refuses a sample with the new column: %v", err)
	}
	peaks, err := s.SamplePeaks(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if peaks.Samples != 2 {
		t.Fatalf("samples = %d, want 2: the migration lost a row", peaks.Samples)
	}
	if peaks.RSSBytes != 99 {
		t.Fatalf("rss peak = %d, want 99: the migration lost or mangled a row", peaks.RSSBytes)
	}
	// And the version is recorded, so the state of a data root is knowable.
	want, have, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("the schema version was not recorded: %v", err)
	}
	if want != have {
		t.Fatalf("schema version = %d, want %d", have, want)
	}
}

// openRaw opens a database with no schema work at all, so a test can create the
// "old" shape by hand.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

// oldSchemaOnly is the schema as it was before `samples.db_bytes` existed. It is
// written out here rather than derived from the current schema, because deriving
// it would make the test pass by construction and prove nothing about a
// database someone else made.
const oldSchemaOnly = `
CREATE TABLE IF NOT EXISTS runs (
  id TEXT PRIMARY KEY, label TEXT NOT NULL, mode TEXT NOT NULL, source_kind TEXT NOT NULL,
  source_arg TEXT NOT NULL, push_base_url TEXT NOT NULL, push_is_vms INTEGER NOT NULL,
  clock_source TEXT NOT NULL, started_at TEXT NOT NULL, target_seconds REAL NOT NULL,
  ended_at TEXT, note TEXT);
CREATE TABLE IF NOT EXISTS sections (
  id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, started_at TEXT NOT NULL, ended_at TEXT, reason TEXT);
CREATE TABLE IF NOT EXISTS heartbeats (
  run_id TEXT NOT NULL, at TEXT NOT NULL, frames INTEGER NOT NULL DEFAULT 0,
  detections INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (run_id, at));
CREATE TABLE IF NOT EXISTS detections (
  id TEXT PRIMARY KEY, run_id TEXT NOT NULL, at TEXT NOT NULL, class TEXT NOT NULL,
  track_id INTEGER NOT NULL, score REAL NOT NULL, box_top INTEGER NOT NULL, box_left INTEGER NOT NULL,
  box_bottom INTEGER NOT NULL, box_right INTEGER NOT NULL, direction TEXT NOT NULL, status TEXT NOT NULL,
  ai_state TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT 'synthetic', clip_ref TEXT, clip_reason TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS evidence (
  id TEXT PRIMARY KEY, detection_id TEXT NOT NULL, sha256 TEXT NOT NULL, storage_ref TEXT NOT NULL,
  bytes INTEGER NOT NULL, trim TEXT NOT NULL, time_begin TEXT NOT NULL, time_end TEXT NOT NULL,
  readback_ok INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT, detection_id TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
  tag TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, next_attempt TEXT, result TEXT, detail TEXT,
  remote_id TEXT, dead_letter INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS faults (
  id INTEGER PRIMARY KEY AUTOINCREMENT, run_id TEXT NOT NULL, at TEXT NOT NULL, reason TEXT NOT NULL,
  detail TEXT NOT NULL, expected TEXT, observed TEXT);
`

func TestOpeningTheSameDataRootTwiceIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "poc.db")
	for i := 0; i < 3; i++ {
		s, err := OpenPath(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if _, _, err := s.SchemaVersion(context.Background()); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		_ = s.Close()
	}
}
