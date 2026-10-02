package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Evidence is a stored clip. The row is written only after the bytes were read
// back from the store and hashed, so `sha256` is a fact about the file on disk
// and not a claim about a command that exited zero.
type Evidence struct {
	ID          string
	DetectionID string
	SHA256      string
	StorageRef  string
	Bytes       int64
	Trim        string
	TimeBegin   time.Time
	TimeEnd     time.Time
	ReadbackOK  bool
	CreatedAt   time.Time
}

// PutEvidence records a stored clip. The unique index on sha256 means two
// detections whose clips have identical bytes share one stored object, which is
// the whole point of a content-addressed store.
func (s *Store) PutEvidence(ctx context.Context, e Evidence) error {
	if e.SHA256 == "" {
		return errors.New("store: evidence needs a sha256; a clip with no hash cannot be read back and verified, which is the only thing that makes it evidence")
	}
	if !e.ReadbackOK {
		return errors.New("store: refusing to record evidence that was not read back: the bytes must be loaded from the store and hashed before the row exists, otherwise a truncated write looks like a stored clip")
	}
	_, err := s.exec(ctx, `
		INSERT INTO evidence (id,detection_id,sha256,storage_ref,bytes,trim,time_begin,time_end,readback_ok,created_at)
		VALUES (?,?,?,?,?,?,?,?,1,?)
		ON CONFLICT(sha256) DO NOTHING`,
		e.ID, e.DetectionID, e.SHA256, e.StorageRef, e.Bytes, e.Trim, nowUTC(e.TimeBegin), nowUTC(e.TimeEnd), nowUTC(time.Now()))
	if err != nil {
		return fmt.Errorf("store: cannot record evidence %s: %w", e.SHA256, err)
	}
	return nil
}

// EvidenceFor returns the evidence rows of a detection.
func (s *Store) EvidenceFor(ctx context.Context, detectionID string) ([]Evidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,detection_id,sha256,storage_ref,bytes,trim,time_begin,time_end,readback_ok,created_at FROM evidence WHERE detection_id=?`, detectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var tb, te, created string
		if err := rows.Scan(&e.ID, &e.DetectionID, &e.SHA256, &e.StorageRef, &e.Bytes, &e.Trim, &tb, &te, &e.ReadbackOK, &created); err != nil {
			return nil, err
		}
		e.TimeBegin, _ = time.Parse(time.RFC3339Nano, tb)
		e.TimeEnd, _ = time.Parse(time.RFC3339Nano, te)
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EvidenceStats is AC3's input.
type EvidenceStats struct {
	Stored     int
	Verified   int
	Mismatched int
}

// VerifyStats re-reads every stored clip and compares the hash. It returns the
// counts rather than a boolean, because a single mismatch must be locatable.
func (s *Store) VerifyStats(ctx context.Context, runID string, readBack func(Evidence) (string, error)) (EvidenceStats, error) {
	var st EvidenceStats
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id,e.detection_id,e.sha256,e.storage_ref,e.bytes,e.trim,e.time_begin,e.time_end,e.readback_ok,e.created_at
		FROM evidence e JOIN detections d ON d.id=e.detection_id WHERE d.run_id=?`, runID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	var all []Evidence
	for rows.Next() {
		var e Evidence
		var tb, te, created string
		if err := rows.Scan(&e.ID, &e.DetectionID, &e.SHA256, &e.StorageRef, &e.Bytes, &e.Trim, &tb, &te, &e.ReadbackOK, &created); err != nil {
			return st, err
		}
		e.TimeBegin, _ = time.Parse(time.RFC3339Nano, tb)
		e.TimeEnd, _ = time.Parse(time.RFC3339Nano, te)
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, e := range all {
		st.Stored++
		got, err := readBack(e)
		switch {
		case err != nil:
			st.Mismatched++
		case got == e.SHA256:
			st.Verified++
		default:
			st.Mismatched++
		}
	}
	return st, nil
}

// --- faults -----------------------------------------------------------------

// Fault is a recorded event that is not a detection: a backpressure stall, a
// process exit, a rehearsed fault, a disk refusal.
type Fault struct {
	At       time.Time
	Reason   string
	Detail   string
	Expected string
	Observed string
}

// Fault reasons. The closed set is small on purpose, because a reason string
// that is free text is a reason nobody can aggregate.
const (
	FaultAIPipeBackpressure = "ai_pipe_backpressure"
	FaultStreamGap          = "stream_gap"
	FaultProcessExit        = "process_exit"
	FaultDiskBudget         = "disk_budget_exceeded"
	FaultAIUnreadable       = "ai_unreadable"
	FaultClipRefused        = "clip_refused"
	FaultRehearsal          = "rehearsal"
	FaultBackpressureReplay = "fault_rehearsal"
	// FaultDBUnavailable is the database refusing a write. It is its own reason
	// and not a clip refusal, because the two have different owners and
	// different fixes: blaming the clip for a locked database sends an operator
	// to look at the video instead of at the process holding the lock.
	FaultDBUnavailable = "db_unavailable"
	// FaultDecisionNotReplayable is a decision that REFUSED to be written because it
	// did not name the model that produced it (RC-18).
	//
	// It is its own reason and not a `db_unavailable`, because the database is fine
	// and the retry would never succeed: the row was refused for saying nothing about
	// its own provenance. Counting these under a database fault would send an
	// operator to look at a lock file when the thing to fix is the detector's
	// identity, and it would average the count into the storage-slice of the report.
	FaultDecisionNotReplayable = "decision_not_replayable"
	// FaultDetectorRefused is a frame the decoder could not turn into boxes, by
	// name. Not a database fault and not an `ai_unreadable`: nothing arrived that
	// was unreadable — the OUTPUT TENSOR was not a detector's output, which is a
	// fact about the model or the export.
	FaultDetectorRefused = "detector_refused"
	// FaultWorkerExit is the outbox pump ending. The rows it was holding stay
	// pending, and a pump that comes back resumes them rather than re-sending a
	// push whose result was already recorded.
	FaultWorkerExit = "outbox_worker_exit"
	// FaultProviderGone is the push target being unreachable. The outbox keeps
	// the rows and the backoff brings them back.
	FaultProviderGone = "push_target_gone"
	// FaultAbsenceNotTyped is a blindness window that could not become a filterable
	// XProtect alarm because no Analytics Event Type is configured for
	// (class, absence state).
	//
	// It is its own reason and not a `push_target_gone`, because the push target is
	// fine and the retries would never succeed: the body has no type to send. Folding
	// it into a provider fault would send an operator to look at the network while
	// the thing to fix is a missing Analytics Event Type, and it would be averaged
	// into the same rate as a genuinely unreachable VMS.
	FaultAbsenceNotTyped = "absence_not_typed"
)

// RecordFault appends a fault row. Expected and Observed are for rehearsed
// faults: AC7 is scored by comparing them, so both must be present for a
// rehearsal to count as verified.
func (s *Store) RecordFault(ctx context.Context, runID string, f Fault) error {
	if f.Reason == "" {
		return errors.New("store: a fault needs a reason; an anonymous fault cannot be counted by AC7")
	}
	if f.Detail == "" {
		return errors.New("store: a fault needs a detail; a reason without a detail is a shrug")
	}
	_, err := s.exec(ctx, `INSERT INTO faults (run_id,at,reason,detail,expected,observed) VALUES (?,?,?,?,?,?)`,
		runID, nowUTC(f.At), f.Reason, f.Detail, f.Expected, f.Observed)
	if err != nil {
		return fmt.Errorf("store: cannot record fault %s: %w", f.Reason, err)
	}
	return nil
}

// Faults returns a run's faults, oldest first.
func (s *Store) Faults(ctx context.Context, runID string) ([]Fault, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at,reason,detail,expected,observed FROM faults WHERE run_id=? ORDER BY at, id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fault
	for rows.Next() {
		var f Fault
		var at string
		if err := rows.Scan(&at, &f.Reason, &f.Detail, &f.Expected, &f.Observed); err != nil {
			return nil, err
		}
		f.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FaultsByReasonPrefix returns the faults whose reason starts with a prefix, across
// every run in this store.
//
// The prefix is "rehearsal:" and the run is deliberately NOT filtered: a
// rehearsal is evidence about a BUILD, and it is recorded under its own run row
// so that the rehearsal and the measurement are distinguishable. Requiring the
// measured run's id is what left AC7 permanently NOT RUN.
func (s *Store) FaultsByReasonPrefix(ctx context.Context, prefix string) ([]Fault, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at,reason,detail,expected,observed FROM faults WHERE reason LIKE ? || '%' ORDER BY at`, prefix)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read faults with the prefix %q: %w", prefix, err)
	}
	defer rows.Close()
	var out []Fault
	for rows.Next() {
		var f Fault
		var at string
		if err := rows.Scan(&at, &f.Reason, &f.Detail, &f.Expected, &f.Observed); err != nil {
			return nil, err
		}
		f.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, f)
	}
	return out, rows.Err()
}

// FaultCounts is the fault histogram the screen shows.
func (s *Store) FaultCounts(ctx context.Context, runID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT reason, COUNT(*) FROM faults WHERE run_id=? GROUP BY reason`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var r string
		var n int
		if err := rows.Scan(&r, &n); err != nil {
			return nil, err
		}
		out[r] = n
	}
	return out, rows.Err()
}

// --- resource samples -------------------------------------------------------

// Sample is one resource observation: RSS, CPU, the AI p99 and the pipe depth.
// The report's resource peaks come from here, and from nowhere else.
type Sample struct {
	At         time.Time
	RSSBytes   int64
	CPUPercent float64
	AIP99MS    float64
	PipeDepth  int
	DiskBytes  int64
	// CPUMeasured says whether CPUPercent was MEASURED. A zero with no flag is a
	// ceiling that passes on any load at all, so the flag exists and the criteria
	// read it.
	CPUMeasured bool
	// DB is the database's own size, sampled with everything else so the
	// resource table has one row per observation instead of two numbers from
	// two places.
	DB int64
}

// PutSample appends a sample.
func (s *Store) PutSample(ctx context.Context, runID string, sm Sample) error {
	_, err := s.exec(ctx, `
		INSERT OR REPLACE INTO samples (run_id,at,rss_bytes,cpu_percent,ai_p99_ms,pipe_depth,disk_bytes,db_bytes,cpu_measured) VALUES (?,?,?,?,?,?,?,?,?)`,
		runID, nowUTC(sm.At), sm.RSSBytes, sm.CPUPercent, sm.AIP99MS, sm.PipeDepth, sm.DiskBytes, sm.DB, boolInt(sm.CPUMeasured))
	if err != nil {
		return fmt.Errorf("store: cannot record a resource sample: %w", err)
	}
	return nil
}

// Peaks are the maximum observed values, which is what AC5 compares against the
// declared ceilings. "Peak" is the word the plan uses and the report must use.
type Peaks struct {
	RSSBytes   int64
	CPUPercent float64
	AIP99MS    float64
	PipeDepth  int
	// DB is the on-disk size of the database including its WAL, which is the
	// number AC5's database clause scores.
	DB        int64
	DiskBytes int64
	// CPUAnyMeasured is true when at least one sample carried a MEASURED CPU, so
	// AC5's CPU clause can be scored against a measurement rather than a zero.
	// A zero with no flag is a ceiling that passes on any load at all.
	CPUAnyMeasured bool
	Samples        int
}

// SamplePeaks returns the maxima over a run.
func (s *Store) SamplePeaks(ctx context.Context, runID string) (Peaks, error) {
	var p Peaks
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(rss_bytes), MAX(cpu_percent), MAX(ai_p99_ms), MAX(pipe_depth), MAX(disk_bytes), MAX(db_bytes), MAX(cpu_measured)
		FROM samples WHERE run_id=?`, runID).
		Scan(&p.Samples, &nullInt64{&p.RSSBytes}, &nullFloat{&p.CPUPercent}, &nullFloat{&p.AIP99MS}, &nullInt{&p.PipeDepth}, &nullInt64{&p.DiskBytes}, &nullInt64{&p.DB}, &nullBool{&p.CPUAnyMeasured})
	if err != nil {
		return p, err
	}
	// The database size is ALSO measured live, because a WAL that grows between
	// samples is exactly the thing the ceiling is about, and the live value is the
	// one the report prints next to the declared ceiling.
	if p.DB == 0 {
		if size, err := s.Size(); err == nil {
			p.DB = size
		}
	}
	return p, nil
}

type nullInt64 struct{ dst *int64 }

func (n *nullInt64) Scan(v any) error {
	if v == nil {
		*n.dst = 0
		return nil
	}
	switch t := v.(type) {
	case int64:
		*n.dst = t
	case float64:
		*n.dst = int64(t)
	default:
		return fmt.Errorf("store: unexpected numeric type %T", v)
	}
	return nil
}

// nullBool reads SQLite's integer booleans. A sample written before a column
// existed has 0 there, which is exactly the "never measured" case.
type nullBool struct{ dst *bool }

func (n *nullBool) Scan(v any) error {
	if v == nil {
		*n.dst = false
		return nil
	}
	switch t := v.(type) {
	case int64:
		*n.dst = t != 0
	case float64:
		*n.dst = t != 0
	case bool:
		*n.dst = t
	default:
		return fmt.Errorf("store: unexpected boolean type %T", v)
	}
	return nil
}

type nullFloat struct{ dst *float64 }

func (n *nullFloat) Scan(v any) error {
	if v == nil {
		*n.dst = 0
		return nil
	}
	switch t := v.(type) {
	case float64:
		*n.dst = t
	case int64:
		*n.dst = float64(t)
	default:
		return fmt.Errorf("store: unexpected numeric type %T", v)
	}
	return nil
}

// TrimPrefixSource keeps the reason vocabulary honest in one place: reasons are
// lowercase snake case, never a sentence.
func TrimPrefixSource(reason string) string { return strings.ToLower(strings.TrimSpace(reason)) }

var _ = sql.ErrNoRows
