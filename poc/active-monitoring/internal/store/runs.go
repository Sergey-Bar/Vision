package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Run is one execution of the pipeline.
type Run struct {
	ID            string
	Label         string
	Mode          string
	SourceKind    string
	SourceArg     string
	PushBaseURL   string
	PushIsVMS     bool
	ClockSource   string
	StartedAt     time.Time
	TargetSeconds float64
	EndedAt       *time.Time
	Note          string
}

// StartRun records a run before anything else happens, so a crash in the first
// second still leaves a row the report can talk about.
func (s *Store) StartRun(ctx context.Context, r Run) error {
	if r.ID == "" {
		return errors.New("store: a run needs an id; without one the report cannot name what it is measuring")
	}
	if r.TargetSeconds <= 0 {
		return errors.New("store: a run needs a positive target duration; without one PASS cannot be distinguished from PROVISIONAL")
	}
	if r.PushIsVMS && strings.Contains(r.PushBaseURL, "127.0.0.1") {
		return errors.New("store: a run is marked as reaching a real VMS while its base URL is loopback; that is the one combination the report must never show")
	}
	_, err := s.exec(ctx, `
		INSERT INTO runs (id,label,mode,source_kind,source_arg,push_base_url,push_is_vms,clock_source,started_at,target_seconds,note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Label, r.Mode, r.SourceKind, r.SourceArg, r.PushBaseURL, boolInt(r.PushIsVMS), r.ClockSource, nowUTC(r.StartedAt), r.TargetSeconds, r.Note)
	if err != nil {
		return fmt.Errorf("store: cannot record the start of run %s: %w", r.ID, err)
	}
	return nil
}

// EndRun closes a run. A run with no end time is how a crash shows up, so the
// report can distinguish "finished" from "died".
func (s *Store) EndRun(ctx context.Context, id string, at time.Time, note string) error {
	res, err := s.exec(ctx, `UPDATE runs SET ended_at=?, note=COALESCE(?,note) WHERE id=?`, nowUTC(at), note, id)
	if err != nil {
		return fmt.Errorf("store: cannot close run %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: run %s does not exist, so it cannot be closed", id)
	}
	return nil
}

// LatestRun returns the most recent run, or nil when there is none. The screen
// shows the latest run when it is opened without naming one.
func (s *Store) LatestRun(ctx context.Context) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,label,mode,source_kind,source_arg,push_base_url,push_is_vms,clock_source,started_at,target_seconds,ended_at,note
		FROM runs ORDER BY started_at DESC LIMIT 1`)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// RunByID returns one run.
func (s *Store) RunByID(ctx context.Context, id string) (*Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,label,mode,source_kind,source_arg,push_base_url,push_is_vms,clock_source,started_at,target_seconds,ended_at,note
		FROM runs WHERE id=?`, id)
	return scanRun(row)
}

type rowScanner interface{ Scan(...any) error }

func scanRun(row rowScanner) (*Run, error) {
	var r Run
	var started string
	var ended sql.NullString
	var note sql.NullString
	var isVMS int
	if err := row.Scan(&r.ID, &r.Label, &r.Mode, &r.SourceKind, &r.SourceArg, &r.PushBaseURL, &isVMS, &r.ClockSource, &started, &r.TargetSeconds, &ended, &note); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("store: cannot read a run: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return nil, fmt.Errorf("store: run %s has an unreadable start time %q: %w", r.ID, started, err)
	}
	r.StartedAt = t
	r.PushIsVMS = isVMS != 0
	if ended.Valid {
		if v, err := time.Parse(time.RFC3339Nano, ended.String); err == nil {
			r.EndedAt = &v
		}
	}
	r.Note = note.String
	return &r, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- heartbeats -------------------------------------------------------------

// Heartbeat is one liveness sample. AC1 is computed from the gaps between them,
// so this table is the AC1 evidence and nothing else may be.
type Heartbeat struct {
	RunID      string
	At         time.Time
	Frames     int64
	Detections int64
}

// Beat appends a heartbeat.
func (s *Store) Beat(ctx context.Context, h Heartbeat) error {
	_, err := s.exec(ctx, `INSERT OR REPLACE INTO heartbeats (run_id,at,frames,detections) VALUES (?,?,?,?)`,
		h.RunID, nowUTC(h.At), h.Frames, h.Detections)
	if err != nil {
		return fmt.Errorf("store: cannot record a heartbeat: %w", err)
	}
	return nil
}

// Gap is a silence longer than the threshold. AC1 requires zero of these.
type Gap struct {
	From    time.Time
	To      time.Time
	Seconds float64
}

// HeartbeatGaps returns every interval between consecutive heartbeats longer
// than the threshold, plus the tail gap when a run ended without a final beat.
func (s *Store) HeartbeatGaps(ctx context.Context, runID string, threshold time.Duration) ([]Gap, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at FROM heartbeats WHERE run_id=? ORDER BY at`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read heartbeats: %w", err)
	}
	defer rows.Close()
	var times []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, fmt.Errorf("store: heartbeat %q is not a timestamp: %w", raw, err)
		}
		times = append(times, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var gaps []Gap
	for i := 1; i < len(times); i++ {
		if d := times[i].Sub(times[i-1]); d > threshold {
			gaps = append(gaps, Gap{From: times[i-1], To: times[i], Seconds: d.Seconds()})
		}
	}
	run, err := s.RunByID(ctx, runID)
	if err == nil && run != nil && len(times) > 0 {
		end := time.Now().UTC()
		if run.EndedAt != nil {
			end = *run.EndedAt
		}
		if d := end.Sub(times[len(times)-1]); d > threshold {
			gaps = append(gaps, Gap{From: times[len(times)-1], To: end, Seconds: d.Seconds()})
		}
	}
	return gaps, nil
}

// HeartbeatCount is how many beats a run recorded. Zero beats is itself an AC1
// failure: a run that never reported liveness cannot be said to have been alive.
func (s *Store) HeartbeatCount(ctx context.Context, runID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM heartbeats WHERE run_id=?`, runID).Scan(&n)
	return n, err
}

// FirstHeartbeat is the run's first beat, which is the run's true start for the
// purposes of the availability axis.
func (s *Store) FirstHeartbeat(ctx context.Context, runID string) (time.Time, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT at FROM heartbeats WHERE run_id=? ORDER BY at LIMIT 1`, runID).Scan(&raw)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}
