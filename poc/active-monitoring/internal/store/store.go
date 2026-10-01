// Package store is the POC's durable state: a SQLite database in WAL mode
// holding runs, heartbeats, detections, evidence, the outbox and faults.
//
// The rule it exists to enforce is R2 from CONTRACT.md: a detection is recorded
// in a committed transaction BEFORE any external effect, and every push attempt
// is an outbox row. A kill -9 at any point must leave a resumable state, not a
// lost one.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // cgo-free SQLite; the POC must build without a C toolchain
)

// Detection statuses. These are the only values ever written to
// detections.status, and they are the closed set defined in CONTRACT.md §2.1.
// Worker labels such as "pending" or "clipping" are never written here.
const (
	StatusAccepted    = "accepted"
	StatusClipOK      = "clip_ok"
	StatusClipRefused = "clip_refused"
	StatusPushSent    = "push_sent"
	StatusPushRefused = "push_refused"
	StatusPushDead    = "push_dead"
	StatusDone        = "done"
)

// Terminal reports whether a status ends the detection's life. AC2 is computed
// entirely from this: a row that is not terminal after five minutes is a lost
// event.
func Terminal(status string) bool {
	switch status {
	case StatusDone:
		return true
	}
	return false
}

// ValidStatus is the closed set. A status outside it is a programming error and
// is refused rather than stored, because a stray status would silently become a
// non-terminal detection and would fail AC2 for a reason nobody can see.
func ValidStatus(status string) bool {
	switch status {
	case StatusAccepted, StatusClipOK, StatusClipRefused, StatusPushSent, StatusPushRefused, StatusPushDead, StatusDone:
		return true
	}
	return false
}

// Store wraps the database.
type Store struct {
	db   *sql.DB
	path string
	// writeTimeout bounds a single write. It exists so a frozen database is
	// REPORTED as a fault rather than stalling the frame loop for the driver's
	// full busy timeout, which on a week-long run is the difference between a
	// named fault and a run that silently stops recording events.
	writeTimeout time.Duration
}

// DefaultWriteTimeout bounds one write when nothing else says otherwise. It is
// shorter than the driver's busy timeout on purpose.
const DefaultWriteTimeout = 5 * time.Second

// SetWriteTimeout bounds a single write. Zero restores the default.
func (s *Store) SetWriteTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultWriteTimeout
	}
	s.writeTimeout = d
}

func (s *Store) writeCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	d := s.writeTimeout
	if d <= 0 {
		d = DefaultWriteTimeout
	}
	return context.WithTimeout(ctx, d)
}

// busyTimeoutMS is the driver's own wait. It is short on purpose: the driver
// blocks INSIDE the call and does not observe a Go context, so a long driver
// timeout would make a frozen database a silent ten-second stall in the frame
// loop. The store's own writeTimeout, applied by exec, is what bounds the wait.
const busyTimeoutMS = 250

// Open creates or opens the database. WAL is set because a reader (the screen)
// must be able to read while the writer (the pipeline) writes, and because a
// commit that returned means the bytes are on the medium.
func Open(dataRoot string) (*Store, error) {
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("store: cannot create the data root %s: %w", dataRoot, err)
	}
	path := filepath.Join(dataRoot, "poc.db")
	s, err := OpenPath(path)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// exec performs a write with the store's own deadline and a retry loop.
//
// The retry is what makes a frozen database a NAMED FAULT rather than a stall.
// The driver returns BUSY quickly, the store retries on a short interval, and
// when the store's writeTimeout is reached it gives up and says so by name. The
// event is refused at that point and nothing external has happened for it, which
// is what R1 requires: a refusal is visible, and a stall is not.
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	deadline := s.writeTimeout
	if deadline <= 0 {
		deadline = DefaultWriteTimeout
	}
	until := time.Now().Add(deadline)
	var last error
	for {
		res, err := s.db.ExecContext(ctx, query, args...)
		if err == nil {
			return res, nil
		}
		last = err
		if !isBusy(err) {
			return nil, err
		}
		if time.Now().After(until) {
			return nil, fmt.Errorf("store: the write could not be completed within %s because the database is locked (%v). "+
				"The event is REFUSED, not queued and not lost: nothing external happened for it, and the fault names the database "+
				"so an operator looks at the process holding the lock rather than at the video", deadline, last)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("store: the write was abandoned because the run ended while the database was locked (%v). "+
				"The event is REFUSED, not queued and not lost", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "database is locked") ||
		strings.Contains(s, "database table is locked") ||
		strings.Contains(s, "sqlite_busy") ||
		strings.Contains(s, "is busy")
}

// OpenPath opens a database at an explicit path. Used by the tests and by the
// screen, which may open the same file read-only.
func OpenPath(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=busy_timeout(" +
		itoa(busyTimeoutMS) + ")&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: cannot open %s: %w", path, err)
	}
	// One writer. The pipeline is single-threaded at the database boundary and
	// a second writer would only buy SQLITE_BUSY under WAL.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path, writeTimeout: DefaultWriteTimeout}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for the packages that need to read rows directly (the
// screen). Writes always go through the methods here.
func (s *Store) DB() *sql.DB { return s.db }

// Path is the database file.
func (s *Store) Path() string { return s.path }

// SealPath is where a run's seal lives.
//
// It is beside the database rather than inside it, because a seal has to survive
// the database being replaced: if the ledger is rewritten from scratch the old
// seal file is still the old attestation, and putting the seal IN the database
// would mean rewriting the evidence along with the thing it is evidence about.
//
// The run id is sanitised because it reaches the filesystem here, and a run id is
// operator-supplied: `SP_POC_LABEL` becomes part of it, and a label of
// `..\..\somewhere` must not decide where a file is written.
func (s *Store) SealPath(runID string) string {
	return filepath.Join(filepath.Dir(s.path), "seals", sanitiseRunID(runID)+".seal.json")
}

// sanitiseRunID reduces an operator-supplied run id to something that can be a
// filename and nothing else.
func sanitiseRunID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "run"
	}
	return out
}

// Size is the on-disk size including the WAL, which is the number AC5 scores.
func (s *Store) Size() (int64, error) {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(s.path + suffix)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

// journalMode reports the mode actually in effect, so preflight can prove the
// database is in WAL rather than assume it.
func (s *Store) journalMode() (string, error) {
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", err
	}
	return mode, nil
}

// ErrTerminal is returned when a detection has already reached a terminal state
// and is asked to move again. There is no "un-accept after commit" and no
// "amend a finished event": a detection row is a fact, and the transition
// history lives in the outbox.
var ErrTerminal = errors.New("store: detection is already terminal")

const schema = `
CREATE TABLE IF NOT EXISTS runs (
  id            TEXT PRIMARY KEY,
  label         TEXT NOT NULL,
  mode          TEXT NOT NULL,
  source_kind   TEXT NOT NULL,
  source_arg    TEXT NOT NULL,
  push_base_url TEXT NOT NULL,
  push_is_vms   INTEGER NOT NULL,
  clock_source  TEXT NOT NULL,
  started_at    TEXT NOT NULL,
  target_seconds REAL NOT NULL,
  ended_at      TEXT,
  note          TEXT
);

CREATE TABLE IF NOT EXISTS sections (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id     TEXT NOT NULL REFERENCES runs(id),
  started_at TEXT NOT NULL,
  ended_at   TEXT,
  reason     TEXT
);

CREATE TABLE IF NOT EXISTS heartbeats (
  run_id     TEXT NOT NULL REFERENCES runs(id),
  at         TEXT NOT NULL,
  frames     INTEGER NOT NULL DEFAULT 0,
  detections INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (run_id, at)
);

CREATE TABLE IF NOT EXISTS detections (
  id           TEXT PRIMARY KEY,
  run_id       TEXT NOT NULL REFERENCES runs(id),
  at           TEXT NOT NULL,
  class        TEXT NOT NULL,
  track_id     INTEGER NOT NULL,
  score        REAL NOT NULL,
  box_top      INTEGER NOT NULL,
  box_left     INTEGER NOT NULL,
  box_bottom   INTEGER NOT NULL,
  box_right    INTEGER NOT NULL,
  direction    TEXT NOT NULL,
  status       TEXT NOT NULL,
  ai_state     TEXT NOT NULL,
  provenance   TEXT NOT NULL DEFAULT 'synthetic',
  clip_ref     TEXT,
  clip_reason  TEXT,
  -- T7.1: the family name, beside the class and not inside it. The class column
  -- is what an Alarm Definition is written against; the family column is the
  -- product's grouping and is what a report is read by. Nullable here and NOT
  -- NULL only after the backfill: a row from a build that predates the column has
  -- no family, and NULL says "not recorded" where an empty string would say
  -- "recorded as none".
  family       TEXT,
  -- T6.3: the rendered snapshot of the crossing, as a reference into the
  -- evidence store rather than as bytes. A reader asking "what did it see?"
  -- three days after a run should not have to have had the screen open, and the
  -- clip may have been rotated away by the disk budget.
  snapshot_ref TEXT,
  -- T7.3: what KIND of evidence a file is — event_clip, crossing_snapshot or
  -- frame. A constant column is a column that cannot be extended.
  snapshot_kind TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS detections_run_at ON detections(run_id, at);
CREATE INDEX IF NOT EXISTS detections_status ON detections(status);

CREATE TABLE IF NOT EXISTS evidence (
  id           TEXT PRIMARY KEY,
  detection_id TEXT NOT NULL REFERENCES detections(id),
  sha256       TEXT NOT NULL,
  storage_ref  TEXT NOT NULL,
  bytes        INTEGER NOT NULL,
  trim         TEXT NOT NULL,
  time_begin   TEXT NOT NULL,
  time_end     TEXT NOT NULL,
  readback_ok  INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS evidence_sha ON evidence(sha256);

CREATE TABLE IF NOT EXISTS outbox (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  detection_id TEXT NOT NULL REFERENCES detections(id),
  kind         TEXT NOT NULL,
  payload      TEXT NOT NULL,
  tag          TEXT NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  next_attempt TEXT,
  result       TEXT,
  detail       TEXT,
  remote_id    TEXT,
  dead_letter  INTEGER NOT NULL DEFAULT 0,
  -- max_attempts is the budget, stored ON THE ROW rather than only in the
  -- runner's configuration. W2-6: it was accepted by Enqueue and discarded, so a
  -- push the VMS will never accept retried until the demo ended. A budget that
  -- is not next to the counter it bounds is a number in a slide.
  max_attempts INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS outbox_detection_kind ON outbox(detection_id, kind);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(dead_letter, next_attempt);

CREATE TABLE IF NOT EXISTS faults (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id     TEXT NOT NULL REFERENCES runs(id),
  at         TEXT NOT NULL,
  reason     TEXT NOT NULL,
  detail     TEXT NOT NULL,
  expected   TEXT,
  observed   TEXT
);
CREATE INDEX IF NOT EXISTS faults_run ON faults(run_id, at);

-- The detector's own arithmetic for a run, as one JSON blob.
--
-- It is a blob and not eight columns because it is a TALLY: it is written once at
-- the end of a run, read by 'pocctl report', and has no query written against any
-- single field of it. The point of putting it here at all is that a regenerated
-- report must say the same thing as the live one -- and a run reporting zero
-- detections with no way to say WHY is the sentence this table exists to make
-- impossible.
CREATE TABLE IF NOT EXISTS detector_tally (
  run_id TEXT PRIMARY KEY REFERENCES runs(id),
  at     TEXT NOT NULL,
  json   TEXT NOT NULL
);

-- D-8: a person's verdict about a PUSHED event, keyed by the event's tag.
--
-- The key is the tag and not a detection id on purpose. The tag is the string
-- the Events API read-back matches on, so a mark is attached to the event that
-- actually left the process rather than to a detection row that happens to
-- resemble it -- and a mark whose tag is not in this run's outbox is refused at
-- write time rather than counted, because a verdict about an event nobody pushed
-- is a verdict about nothing.
--
-- by and note are TEXT NOT NULL DEFAULT '' because a mark with no explanation is
-- a legitimate mark, but a NULL would have to be read as one.
CREATE TABLE IF NOT EXISTS ground_truth (
  run_id  TEXT NOT NULL REFERENCES runs(id),
  tag     TEXT NOT NULL,
  verdict TEXT NOT NULL,
  by      TEXT NOT NULL DEFAULT '',
  note    TEXT NOT NULL DEFAULT '',
  at      TEXT NOT NULL,
  PRIMARY KEY (run_id, tag)
);

CREATE TABLE IF NOT EXISTS samples (
  run_id     TEXT NOT NULL REFERENCES runs(id),
  at         TEXT NOT NULL,
  rss_bytes  INTEGER NOT NULL,
  cpu_percent REAL NOT NULL,
  ai_p99_ms  REAL NOT NULL,
  pipe_depth INTEGER NOT NULL,
  disk_bytes INTEGER NOT NULL,
  PRIMARY KEY (run_id, at)
);

-- W1-8: the attribution record for one VLM answer about one clip.
--
-- It is a TABLE and not a column on detections because a detection may be asked
-- about more than once across a resumed run, and because the fields here are the
-- ones a proof chain reads rather than the ones a report scans.
--
-- model_identity is what the API SAID, which is a claim by the provider. There is
-- deliberately NO model_hash column and there never will be one in this table: a
-- hosted model has no weights to hash, and a synthetic hash would satisfy a
-- column check while defeating the only thing the column is for.
CREATE TABLE IF NOT EXISTS vlm_observations (
  detection_id        TEXT PRIMARY KEY REFERENCES detections(id),
  run_id              TEXT NOT NULL REFERENCES runs(id),
  prompt_id           TEXT NOT NULL DEFAULT '',
  prompt_version      TEXT NOT NULL DEFAULT '',
  aistate             TEXT NOT NULL,
  verdict             TEXT NOT NULL,
  summary             TEXT NOT NULL DEFAULT '',
  confidence          REAL NOT NULL DEFAULT 0,
  insufficient_evidence INTEGER NOT NULL DEFAULT 0,
  model_identity      TEXT NOT NULL DEFAULT '',
  contract_version    TEXT NOT NULL DEFAULT '',
  temperature         REAL NOT NULL DEFAULT 0,
  reason_code         TEXT NOT NULL DEFAULT 'none',
  detail              TEXT NOT NULL DEFAULT '',
  output_hash         TEXT NOT NULL DEFAULT '',
  input_hashes        TEXT NOT NULL DEFAULT '',
  latency_ms          INTEGER NOT NULL DEFAULT 0,
  prompt_tokens       INTEGER NOT NULL DEFAULT 0,
  completion_tokens   INTEGER NOT NULL DEFAULT 0,
  asset_id            TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL
);

-- W3-4: the stored decision inputs, so a decision can be replayed from facts
-- with no provider call at all.
--
-- Everything needed to recompute "did this fire" lives here and nowhere else: the
-- measurement, the threshold it was compared against, the region the measurement
-- produced and where that region came from. The model was NOT an input to the
-- decision to raise the alert — it is the verdict on an alert that was already
-- raised — and this table is what makes that structural rather than a claim.
CREATE TABLE IF NOT EXISTS decisions (
  detection_id  TEXT PRIMARY KEY REFERENCES detections(id),
  run_id        TEXT NOT NULL REFERENCES runs(id),
  trigger_mode  TEXT NOT NULL,
  trigger_class TEXT NOT NULL,
  score         REAL NOT NULL,
  threshold     REAL NOT NULL,
  release       REAL NOT NULL,
  sustain       INTEGER NOT NULL DEFAULT 0,
  streak        INTEGER NOT NULL DEFAULT 0,
  score_reason  TEXT NOT NULL DEFAULT '',
  region_source TEXT NOT NULL DEFAULT '',
  region_top    INTEGER NOT NULL DEFAULT 0,
  region_left   INTEGER NOT NULL DEFAULT 0,
  region_bottom INTEGER NOT NULL DEFAULT 0,
  region_right  INTEGER NOT NULL DEFAULT 0,
  frame_seq     INTEGER NOT NULL DEFAULT 0,
  reference_sha TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);

-- W2-1: the pinned reference view for a camera, stored so the measurement it
-- anchors is reproducible on the next run and so a reviewer can be handed the
-- exact picture.
CREATE TABLE IF NOT EXISTS reference_views (
  run_id       TEXT NOT NULL REFERENCES runs(id),
  camera_guid  TEXT NOT NULL,
  storage_ref  TEXT NOT NULL,
  sha256       TEXT NOT NULL,
  pinned_at    TEXT NOT NULL,
  source       TEXT NOT NULL,
  PRIMARY KEY (run_id, camera_guid)
);

-- W3-1: the hash-chained decision ledger. One row per decision step.
--
-- hash is computed over a LENGTH-PREFIXED encoding of this row and the previous
-- row's hash, so field boundaries cannot be shifted to produce a collision, and
-- every row after an edited one fails verification.
CREATE TABLE IF NOT EXISTS ledger (
  seq       INTEGER PRIMARY KEY,
  at        TEXT NOT NULL,
  kind      TEXT NOT NULL,
  subject   TEXT NOT NULL,
  payload   TEXT NOT NULL,
  prev_hash TEXT NOT NULL,
  hash      TEXT NOT NULL
);

-- Per-frame box observations: what the detector measured on every frame it was
-- asked about, whether or not that frame produced an event.
--
-- This table exists because the tracker now has MEMORY. A crossing box is
-- Kalman-filtered, so it is a function of the frames around it and cannot be
-- recomputed from the decisions table alone. Replay therefore reads this table
-- and re-runs the whole track from stored per-frame boxes, which is a STRONGER
-- claim than the one it replaces: "a decision replays from stored facts" at
-- track granularity rather than at row granularity.
--
-- The measured column is what tells "the model saw nothing" apart from "the
-- pipeline never asked". A frame the detector was not asked about is stored with
-- measured=0 and carries no boxes, and a reader counting rows instead of reading
-- that column will compute a recall of zero out of a run that never ran.
--
-- The source column says which decoder produced the row -- detector or sidecar --
-- so a measurement made by the Python process is never presented as one made by
-- the shipped path.
--
-- NOTE: this blob is a Go raw string literal. A backtick in one of these comments
-- terminates the string at the backtick and the rest of the schema is parsed as
-- Go. That is not a style rule here, it is the reason every comment in this blob
-- spells a column name in plain words.
CREATE TABLE IF NOT EXISTS box_observations (
  run_id     TEXT NOT NULL REFERENCES runs(id),
  frame_seq  INTEGER NOT NULL,
  at         TEXT NOT NULL,
  track_id   INTEGER NOT NULL DEFAULT -1,
  class      TEXT NOT NULL DEFAULT '',
  family     TEXT,
  score      REAL NOT NULL DEFAULT 0,
  box_top    INTEGER NOT NULL DEFAULT 0,
  box_left   INTEGER NOT NULL DEFAULT 0,
  box_bottom INTEGER NOT NULL DEFAULT 0,
  box_right  INTEGER NOT NULL DEFAULT 0,
  source     TEXT NOT NULL DEFAULT '',
  measured   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (run_id, frame_seq, track_id, class, box_top, box_left, box_bottom, box_right)
);
CREATE INDEX IF NOT EXISTS box_observations_run_frame ON box_observations(run_id, frame_seq);

-- A human's judgement about one proposed box: accepted or rejected.
--
-- This is the only source of a false-positive rate, and it is a row rather than
-- a number in a report because the number is only recomputable while the
-- judgements behind it are kept. A run whose labels table is empty has a
-- false-positive rate of NOT RUN, and says so.
--
-- The primary key is the run, the frame and the box itself, so marking the same
-- box twice is one judgement updated rather than two rows that disagree.
CREATE TABLE IF NOT EXISTS labels (
  run_id    TEXT NOT NULL REFERENCES runs(id),
  frame_seq INTEGER NOT NULL,
  box_key   TEXT NOT NULL,
  verdict   TEXT NOT NULL,
  class     TEXT NOT NULL DEFAULT '',
  by        TEXT NOT NULL,
  note      TEXT NOT NULL DEFAULT '',
  at        TEXT NOT NULL,
  PRIMARY KEY (run_id, frame_seq, box_key)
);

-- Objects a human DREW for what the detector missed.
--
-- Without this table recall is not measurable at all: a frame with no detection
-- carries no information, because "the detector found nothing" and "there was
-- nothing" are the same row. A drawn box is the only thing that tells them
-- apart, which is why the labelling tool has a drag gesture and not only a
-- verdict key.
--
-- The idx column is the human's own index within the frame, so two objects at
-- the same place on two different frames are two rows and not a collision.
CREATE TABLE IF NOT EXISTS label_objects (
  run_id     TEXT NOT NULL REFERENCES runs(id),
  frame_seq  INTEGER NOT NULL,
  idx        INTEGER NOT NULL,
  class      TEXT NOT NULL,
  box_top    INTEGER NOT NULL,
  box_left   INTEGER NOT NULL,
  box_bottom INTEGER NOT NULL,
  box_right  INTEGER NOT NULL,
  by         TEXT NOT NULL,
  at         TEXT NOT NULL,
  PRIMARY KEY (run_id, frame_seq, idx)
);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("store: cannot create the schema in %s: %w", s.path, err)
	}
	// W3-2: the ledger is append-only, and a chain a single SQL statement can
	// rewrite is a comment. The triggers are created after the table rather than
	// inside the schema blob so that the red check can remove them from ONE place
	// and the rest of the migration is untouched by the mutation.
	for _, stmt := range ledgerGuards {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: cannot install the ledger's append-only guard: %w", err)
		}
	}
	// A data root that predates a column must keep working. `CREATE TABLE IF NOT
	// EXISTS` does not add a column, so a new release against an old database
	// failed with "table samples has no column named db_bytes" — a message that
	// reads like a bug and costs an hour. Columns are added here, once, and the
	// version is recorded so the state of a data root is knowable.
	if err := s.ensureColumns(ctx); err != nil {
		return err
	}
	mode, err := s.journalMode()
	if err != nil {
		return fmt.Errorf("store: cannot read the journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("store: %s is in %q journal mode, not WAL; without WAL the screen cannot read while the pipeline writes, and a commit that returned might not be on the medium", s.path, mode)
	}
	return s.recordVersion(ctx)
}

// addedColumns is every column this module has added to an existing table
// after v1, in order. Each is applied only when the table does not already have
// it, so running it twice is free.
var addedColumns = []struct {
	table, column, decl string
}{
	{"samples", "db_bytes", "INTEGER NOT NULL DEFAULT 0"},
	{"samples", "cpu_measured", "INTEGER NOT NULL DEFAULT 0"},
	// T7.1. The family's name, BESIDE the class name and not inside it: `class`
	// is the model's own word and is what an Alarm Definition matches on, and
	// `family` is the product's grouping and is what a report is read by. Storing
	// them as one string would make `WHERE class = 'car'` stop matching the moment
	// anyone touched the format, and every existing rule would break with no
	// error.
	//
	// NULLABLE, and the null is meaningful: a row written by a build that predates
	// this column has no family, and NULL says "not recorded" where an empty
	// string would say "recorded as none". The NOT NULL contract step is a
	// separate migration and may never be bundled with the expand that makes it
	// safe.
	{"detections", "family", "TEXT"},
	// T6.3. The rendered snapshot of the crossing. The bytes live in the evidence
	// store and this holds the reference, for the same reason `clip_ref` is a
	// reference: one file, one copy, and the evidence store is the only place
	// bytes are written.
	{"detections", "snapshot_ref", "TEXT"},
	// T7.3. What KIND of evidence a file is. Without it the kind was a constant
	// column that always said the same thing, and a column that is always the
	// same value cannot be extended and cannot be told from an observed one.
	{"detections", "snapshot_kind", "TEXT"},
	// W2-6: the attempt budget, beside the attempt counter it bounds.
	{"outbox", "max_attempts", "INTEGER NOT NULL DEFAULT 0"},
	// Model attribution on `decisions`. EVERY one of these is NULLABLE, and the
	// null is the honest value rather than a missing one: a `motion`-mode decision
	// was made from a pixel diff and a threshold, and no model produced it, so
	// NULL says "no model" where 0 or '' would say "a model was involved and
	// measured zero", which is a fabricated measurement.
	//
	// That distinction is the whole reason RC-18 can be a red check. The check is
	// one comparison — a decision written in `detector` mode must name its model
	// and its SHA-256 — and it is meaningful precisely because a motion-mode
	// decision legitimately names nothing. Were these columns NOT NULL DEFAULT
	// '', every row would name a model that never ran.
	{"decisions", "model_identity", "TEXT"},
	{"decisions", "model_sha256", "TEXT"},
	{"decisions", "model_licence", "TEXT"},
	{"decisions", "input_size", "INTEGER"},
	{"decisions", "nms_class_iou", "REAL"},
	{"decisions", "nms_global_iou", "REAL"},
	{"decisions", "infer_ms", "REAL"},
	{"decisions", "det_latency_p50_ms", "REAL"},
	{"decisions", "det_latency_p95_ms", "REAL"},
	{"decisions", "det_latency_p99_ms", "REAL"},
	// The track this crossing belongs to. -1 would be a fabricated track number;
	// NULL is "this decision came from a trigger that does not track".
	{"decisions", "track_id", "INTEGER"},
}

// schemaVersion is bumped whenever a migration is added above.
const schemaVersion = 8

func (s *Store) ensureColumns(ctx context.Context) error {
	for _, c := range addedColumns {
		has, err := s.hasColumn(ctx, c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.decl)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			// Two processes migrating at once is a real possibility on a
			// restart, and the second one losing the race is not an error.
			if has2, err2 := s.hasColumn(ctx, c.table, c.column); err2 == nil && has2 {
				continue
			}
			return fmt.Errorf("store: cannot add %s.%s to %s: %w", c.table, c.column, s.path, err)
		}
	}
	return nil
}

func (s *Store) hasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return false, fmt.Errorf("store: cannot read the columns of %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) recordVersion(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("store: cannot record the schema version in %s: %w", s.path, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO schema_meta (id, version, applied_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET version = excluded.version, applied_at = excluded.applied_at`,
		schemaVersion, nowUTC(time.Now())); err != nil {
		return fmt.Errorf("store: cannot record the schema version in %s: %w", s.path, err)
	}
	return nil
}

// SchemaVersion is the version this build expects, and the version the data
// root records. They differ when someone opens an old data root with a new
// build, and the difference is printed rather than repaired silently.
func (s *Store) SchemaVersion(ctx context.Context) (want, have int, err error) {
	var v int
	e := s.db.QueryRowContext(ctx, `SELECT version FROM schema_meta WHERE id = 1`).Scan(&v)
	if e != nil {
		return schemaVersion, 0, e
	}
	return schemaVersion, v, nil
}

// Exclusive is a second connection used only to hold a write lock on the same
// file. It exists so the database-freeze fault can be rehearsed for real rather
// than mocked: a mocked lock proves nothing about whether the writer's error is
// recognisable, and that recognisability is the whole point.
type Exclusive struct {
	db *sql.DB
}

// OpenExclusive opens a second connection to the same file. It does not run the
// schema: it exists to block.
func OpenExclusive(ctx context.Context, path string) (*Exclusive, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: cannot open %s for locking: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `SELECT 1`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Exclusive{db: db}, nil
}

// Hold takes an EXCLUSIVE write lock and returns the function that releases it.
// It blocks until the lock is taken or the context ends, so a caller can hold it
// for a measured interval and no longer.
func (e *Exclusive) Hold(ctx context.Context, forDur time.Duration) (func(), error) {
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `BEGIN EXCLUSIVE`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("store: cannot take the exclusive lock: %w", err)
	}
	release := func() {
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		_ = conn.Close()
	}
	return release, nil
}

// Close releases the connection.
func (e *Exclusive) Close() error { return e.db.Close() }

func nowUTC(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
