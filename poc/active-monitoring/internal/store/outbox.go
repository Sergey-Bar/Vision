package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

// OutboxEntry is one external effect that must be attempted. It exists so that
// a detection is never pushed without a row saying it was going to be, and
// never attempted without a row saying what happened.
type OutboxEntry struct {
	ID          int64
	DetectionID string
	Kind        string
	Payload     string
	Tag         string
	Attempts    int
	NextAttempt *time.Time
	Result      string
	Detail      string
	RemoteID    string
	DeadLetter  bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Outbox kinds. These are the only external effects this module performs.
const (
	KindEvent    = "event"
	KindBookmark = "bookmark"
)

// Enqueue records the intent to push, in the same transaction the caller is
// already in. The row is created BEFORE the attempt, so a crash between the
// enqueue and the call leaves a pending row the recovery pass retries, and a
// crash after the call leaves a row whose result says "unknown", which the
// read-back call can then resolve.
func (s *Store) Enqueue(ctx context.Context, e OutboxEntry, maxAttempts int) (int64, error) {
	if e.DetectionID == "" || e.Kind == "" {
		return 0, errors.New("store: an outbox entry needs a detection and a kind; an unattributable push cannot be audited")
	}
	if e.Tag == "" {
		return 0, errors.New("store: an outbox entry needs a tag; the tag is the idempotency key the read-back uses, and a counter would collide across a restart")
	}
	now := time.Now().UTC()
	res, err := s.exec(ctx, `
		INSERT INTO outbox (detection_id,kind,payload,tag,attempts,next_attempt,created_at,updated_at)
		VALUES (?,?,?,?,0,?,?,?)
		ON CONFLICT(detection_id,kind) DO NOTHING`,
		e.DetectionID, e.Kind, e.Payload, e.Tag, nowUTC(now), nowUTC(now), nowUTC(now))
	if err != nil {
		return 0, fmt.Errorf("store: cannot enqueue a %s for detection %s: %w", e.Kind, e.DetectionID, err)
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// The unique index on (detection_id, kind) already holds a row: this
		// effect was already enqueued. Return the existing row's id.
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM outbox WHERE detection_id=? AND kind=?`, e.DetectionID, e.Kind).Scan(&id); err != nil {
			return 0, fmt.Errorf("store: cannot find the existing outbox row for detection %s: %w", e.DetectionID, err)
		}
		return id, nil
	}
	_ = maxAttempts
	return id, nil
}

// RecordAttempt increments the attempt counter and schedules the next try. The
// backoff is exponential with a cap, and it is the budget AC4 measures: a
// provider that never answers must exhaust it and be dead-lettered rather than
// retried forever.
func (s *Store) RecordAttempt(ctx context.Context, id int64) error {
	now := time.Now()
	_, err := s.exec(ctx, `
		UPDATE outbox SET attempts=attempts+1, next_attempt=?, updated_at=? WHERE id=?`,
		nowUTC(now.Add(backoffFor(now, currentAttempts(ctx, s, id)))), nowUTC(now), id)
	if err != nil {
		return fmt.Errorf("store: cannot record an attempt on outbox row %d: %w", id, err)
	}
	return nil
}

func currentAttempts(ctx context.Context, s *Store, id int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT attempts FROM outbox WHERE id=?`, id).Scan(&n)
	return n
}

// backoffFor is exponential, capped, and jittered by attempt parity so two
// entries do not retry in lockstep. The cap matters: an unbounded backoff would
// make a seven-day run look healthy while nothing is being delivered.
//
// The exponent is clamped before the multiplication, because 2^99 overflows a
// float64-to-Duration conversion and produces a negative duration, which would
// make a dead provider look instantly retryable.
func backoffFor(now time.Time, attempts int) time.Duration {
	const maxExponent = 20
	if attempts < 0 {
		attempts = 0
	}
	if attempts > maxExponent {
		attempts = maxExponent
	}
	d := time.Duration(float64(2*time.Second) * math.Pow(2, float64(attempts)))
	if d <= 0 || d > 5*time.Minute {
		d = 5 * time.Minute
	}
	if attempts%2 == 1 {
		d += d / 4
		if d > 6*time.Minute {
			d = 6 * time.Minute
		}
	}
	return d
}

// RecordResult writes the outcome of an attempt. An empty result means "we do
// not know", which is a real state: the request may have been accepted and the
// response lost. The read-back call resolves it, and until then AC4 counts the
// attempt as having no result.
func (s *Store) RecordResult(ctx context.Context, id int64, result, detail, remoteID string) error {
	now := time.Now()
	_, err := s.exec(ctx, `
		UPDATE outbox SET result=?, detail=?, remote_id=COALESCE(NULLIF(?,''),remote_id), next_attempt=NULL, updated_at=? WHERE id=?`,
		result, detail, remoteID, nowUTC(now), id)
	if err != nil {
		return fmt.Errorf("store: cannot record the result of outbox row %d: %w", id, err)
	}
	return nil
}

// DeadLetter marks a row as given up on. The row stays for human review; it is
// never deleted, because "we stopped trying" is a fact someone has to be able
// to read.
func (s *Store) DeadLetter(ctx context.Context, id int64, reason string) error {
	_, err := s.exec(ctx, `UPDATE outbox SET dead_letter=1, result='push_dead', detail=?, updated_at=? WHERE id=?`,
		reason, nowUTC(time.Now()), id)
	if err != nil {
		return fmt.Errorf("store: cannot dead-letter outbox row %d: %w", id, err)
	}
	return nil
}

// Due lists the entries that should be attempted now: never tried, or due for
// another try, and not dead-lettered.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]OutboxEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,detection_id,kind,payload,tag,attempts,next_attempt,result,detail,remote_id,dead_letter,created_at,updated_at
		FROM outbox
		WHERE dead_letter=0 AND (result IS NULL OR result='') AND (next_attempt IS NULL OR next_attempt <= ?)
		ORDER BY next_attempt, id LIMIT ?`, nowUTC(now), limit)
	if err != nil {
		return nil, fmt.Errorf("store: cannot list due outbox entries: %w", err)
	}
	return scanOutbox(rows)
}

func scanOutbox(rows *sql.Rows) ([]OutboxEntry, error) {
	defer rows.Close()
	var out []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		var next sql.NullString
		var result, detail, remoteID sql.NullString
		var created, updated string
		if err := rows.Scan(&e.ID, &e.DetectionID, &e.Kind, &e.Payload, &e.Tag, &e.Attempts, &next, &result, &detail, &remoteID, &e.DeadLetter, &created, &updated); err != nil {
			return nil, err
		}
		e.Result, e.Detail, e.RemoteID = result.String, detail.String, remoteID.String
		if next.Valid {
			if t, err := time.Parse(time.RFC3339Nano, next.String); err == nil {
				e.NextAttempt = &t
			}
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		e.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Entry returns one outbox row.
func (s *Store) Entry(ctx context.Context, id int64) (*OutboxEntry, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,detection_id,kind,payload,tag,attempts,next_attempt,result,detail,remote_id,dead_letter,created_at,updated_at
		FROM outbox WHERE id=?`, id)
	var e OutboxEntry
	var next sql.NullString
	var result, detail, remoteID sql.NullString
	var created, updated string
	if err := row.Scan(&e.ID, &e.DetectionID, &e.Kind, &e.Payload, &e.Tag, &e.Attempts, &next, &result, &detail, &remoteID, &e.DeadLetter, &created, &updated); err != nil {
		return nil, err
	}
	e.Result, e.Detail, e.RemoteID = result.String, detail.String, remoteID.String
	return &e, nil
}

// OutboxFor returns the entries belonging to a detection.
func (s *Store) OutboxFor(ctx context.Context, detectionID string) ([]OutboxEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,detection_id,kind,payload,tag,attempts,next_attempt,result,detail,remote_id,dead_letter,created_at,updated_at
		FROM outbox WHERE detection_id=? ORDER BY id`, detectionID)
	if err != nil {
		return nil, err
	}
	return scanOutbox(rows)
}

// AttemptStats is AC4's input: how many attempts exist and how many of them have
// a recorded result.
type AttemptStats struct {
	Total        int
	WithResult   int
	DeadLetter   int
	Unattributed int
}

// Stats counts the outbox. `Unattributed` is the number of detections with no
// outbox row at all, which AC4 counts as a failure: a push that nobody recorded
// is a push nobody can account for.
func (s *Store) Stats(ctx context.Context, runID string) (AttemptStats, error) {
	var st AttemptStats
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN result IS NOT NULL AND result <> '' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN dead_letter=1 THEN 1 ELSE 0 END)
		FROM outbox o JOIN detections d ON d.id=o.detection_id WHERE d.run_id=?`, runID).
		Scan(&nullInt{&st.Total}, &nullInt{&st.WithResult}, &nullInt{&st.DeadLetter})
	if err != nil {
		return st, err
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM detections d
		WHERE d.run_id=? AND NOT EXISTS (SELECT 1 FROM outbox o WHERE o.detection_id=d.id)`, runID).Scan(&st.Unattributed)
	return st, err
}

// nullInt lets SUM() return NULL for an empty set without a scan error.
type nullInt struct{ dst *int }

func (n *nullInt) Scan(v any) error {
	if v == nil {
		*n.dst = 0
		return nil
	}
	switch t := v.(type) {
	case int64:
		*n.dst = int(t)
	case float64:
		*n.dst = int(t)
	default:
		return fmt.Errorf("store: unexpected numeric type %T", v)
	}
	return nil
}
