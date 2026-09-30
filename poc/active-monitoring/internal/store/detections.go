package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Detection is one line-cross event. It is written once, in `accepted`, inside
// a transaction, before the clip is cut and before anything is pushed. Every
// later change is a status transition, and the transitions are one-way.
type Detection struct {
	ID         string
	RunID      string
	At         time.Time
	Class      string
	TrackID    int
	Score      float64
	BoxTop     int
	BoxLeft    int
	BoxBottom  int
	BoxRight   int
	Direction  string
	Status     string
	AIState    string
	Provenance string
	ClipRef    string
	ClipReason string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Accept commits a detection in `accepted`. This is the R2 boundary: when it
// returns nil, the event is on the medium, and no external effect has happened
// yet. A crash immediately after leaves a row that the recovery pass can finish.
func (s *Store) Accept(ctx context.Context, d Detection) error {
	if d.ID == "" {
		return errors.New("store: a detection needs an id derived from its content; a counter could produce the same id twice after a crash and the two events would be indistinguishable")
	}
	if !ValidStatus(StatusAccepted) {
		return fmt.Errorf("store: %q is not a valid initial status", StatusAccepted)
	}
	if d.Class == "" {
		return errors.New("store: a detection needs a class; an empty class is not an event anyone can act on")
	}
	if !ValidProvenance(d.Provenance) {
		return fmt.Errorf("store: provenance %q is not real or synthetic; the report prints this next to every number and an unlabelled number is a false number", d.Provenance)
	}
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	// A write gets its own short deadline. A frozen database must be a named
	// fault on the event path, not a ten second stall in the frame loop that a
	// reader on the screen would see as a run that stopped.
	wctx, cancel := s.writeCtx(ctx)
	defer cancel()
	_, err := s.exec(wctx, `
		INSERT INTO detections (id,run_id,at,class,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.RunID, nowUTC(d.At), d.Class, d.TrackID, d.Score, d.BoxTop, d.BoxLeft, d.BoxBottom, d.BoxRight,
		d.Direction, StatusAccepted, d.AIState, d.Provenance, nowUTC(d.CreatedAt), nowUTC(d.UpdatedAt))
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("store: detection %s already exists: this is the idempotency boundary working, and re-accepting the same content-derived id is refused rather than duplicated", d.ID)
		}
		if wctx.Err() != nil {
			return fmt.Errorf("store: detection %s could not be committed: the write could not be completed within %s (%v). The event is REFUSED, not queued and not lost: nothing external happened for it, and the fault names the database so an operator looks at the process holding the lock rather than at the video",
				d.ID, s.writeTimeout, wctx.Err())
		}
		return fmt.Errorf("store: cannot accept detection %s: %w", d.ID, err)
	}
	return nil
}

// Provenance values. D7: every piece of material carries one, and the report
// prints it next to every number derived from it.
const (
	ProvenanceReal      = "real"
	ProvenanceSynthetic = "synthetic"
)

// ValidProvenance is the closed set.
func ValidProvenance(p string) bool { return p == ProvenanceReal || p == ProvenanceSynthetic }

// Transition moves a detection to a new status. It refuses to move out of a
// terminal state and refuses an unknown status, so a bug cannot quietly create
// a detection that AC2 will later call lost.
func (s *Store) Transition(ctx context.Context, id, to string, clipRef, clipReason string) error {
	if !ValidStatus(to) {
		return fmt.Errorf("store: %q is not one of the seven statuses in CONTRACT.md; writing it would create a non-terminal detection that fails AC2 for a reason nobody can see", to)
	}
	res, err := s.exec(ctx, `
		UPDATE detections SET status=?, updated_at=?, clip_ref=COALESCE(?,clip_ref), clip_reason=COALESCE(?,clip_reason)
		WHERE id=? AND status NOT IN ('done')`,
		to, nowUTC(time.Now()), nullIfEmpty(clipRef), nullIfEmpty(clipReason), id)
	if err != nil {
		return fmt.Errorf("store: cannot move detection %s to %s: %w", id, to, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		err := s.db.QueryRowContext(ctx, `SELECT status FROM detections WHERE id=?`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: detection %s does not exist, so it cannot be moved to %s; this is a lost event and AC2 must fail", id, to)
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: %s is already %s and cannot become %s", ErrTerminal, id, status, to)
	}
	return nil
}

// Complete is the transition to `done`, the single terminal state. It is only
// reachable from a clip outcome and a push outcome, both of which are recorded,
// so "done" always has evidence behind it.
func (s *Store) Complete(ctx context.Context, id string) error {
	return s.Transition(ctx, id, StatusDone, "", "")
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SetClip records the evidence reference on a detection.
func (s *Store) SetClip(ctx context.Context, id, ref, reason string) error {
	_, err := s.exec(ctx, `UPDATE detections SET clip_ref=?, clip_reason=?, updated_at=? WHERE id=?`,
		nullIfEmpty(ref), nullIfEmpty(reason), nowUTC(time.Now()), id)
	if err != nil {
		return fmt.Errorf("store: cannot record the clip for detection %s: %w", id, err)
	}
	return nil
}

// Get returns one detection.
func (s *Store) Get(ctx context.Context, id string) (*Detection, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,run_id,at,class,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,clip_ref,clip_reason,created_at,updated_at
		FROM detections WHERE id=?`, id)
	d, err := scanDetection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return d, err
}

func scanDetection(row rowScanner) (*Detection, error) {
	var d Detection
	var at, created, updated string
	var clipRef, clipReason sql.NullString
	if err := row.Scan(&d.ID, &d.RunID, &at, &d.Class, &d.TrackID, &d.Score, &d.BoxTop, &d.BoxLeft, &d.BoxBottom, &d.BoxRight,
		&d.Direction, &d.Status, &d.AIState, &d.Provenance, &clipRef, &clipReason, &created, &updated); err != nil {
		return nil, err
	}
	var err error
	if d.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return nil, fmt.Errorf("store: detection %s has an unreadable timestamp %q: %w", d.ID, at, err)
	}
	if d.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, err
	}
	if d.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, err
	}
	d.ClipRef, d.ClipReason = clipRef.String, clipReason.String
	return &d, nil
}

// Stalled lists detections that have been in a non-terminal state longer than
// the limit. This list is AC2's whole content, and it is expected to be empty.
func (s *Store) Stalled(ctx context.Context, runID string, limit time.Duration) ([]Detection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,run_id,at,class,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,clip_ref,clip_reason,created_at,updated_at
		FROM detections WHERE run_id=? AND status NOT IN ('done') AND updated_at < ? ORDER BY updated_at`,
		runID, nowUTC(time.Now().Add(-limit)))
	if err != nil {
		return nil, fmt.Errorf("store: cannot look for stalled detections: %w", err)
	}
	defer rows.Close()
	var out []Detection
	for rows.Next() {
		d, err := scanDetection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// CountByStatus is the status histogram, which is the number the screen shows
// next to the pipeline.
func (s *Store) CountByStatus(ctx context.Context, runID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM detections WHERE run_id=? GROUP BY status`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Recent returns the newest detections, for the screen's table.
func (s *Store) Recent(ctx context.Context, runID string, limit int) ([]Detection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,run_id,at,class,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,clip_ref,clip_reason,created_at,updated_at
		FROM detections WHERE run_id=? ORDER BY at DESC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read recent detections: %w", err)
	}
	defer rows.Close()
	var out []Detection
	for rows.Next() {
		d, err := scanDetection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// Count is the number of detections in a run.
func (s *Store) Count(ctx context.Context, runID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM detections WHERE run_id=?`, runID).Scan(&n)
	return n, err
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
