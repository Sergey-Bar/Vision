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
	// MaxAttempts is the budget, stored on the row so the attempt counter and the bound are in the same place.
	MaxAttempts int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Outbox kinds. These are the only external effects this module performs.
//
// KindAbsenceEvent is the THIRD, and it is new. It is not a new column and not a
// migration: `kind` is unconstrained TEXT, so this is one more value in a column
// that already held two.
//
// It has to be its own kind because of what the unique index is. `(detection_id,
// kind)` gives every detection its own row — which is what makes AC4's "every
// detection has an outbox row" hold — and that is incompatible with collapsing a
// window's 500 blindness proposals onto one remote event, because 500 rows under one
// detection id would be one row and 499 detections would have none.
//
// The alternative was to drop the ordinary detection event for every blindness
// proposal and let the aggregated row stand in for it. That satisfies AC4 (one row
// each) and produces exactly one remote event, and it is worse: the detection DID
// happen — a car crossed the line — and withholding the event with its measured
// bounding box loses the thing the VMS can actually draw. What is missing is the
// VERDICT, not the crossing. So both go out, and only the verdict's absence is
// aggregated.
const (
	KindEvent        = "event"
	KindBookmark     = "bookmark"
	KindAbsenceEvent = "absence_event"
)

// ValidOutboxKind reports whether a kind is one of the three.
//
// It exists so a typo in a kind cannot become a row nothing ever drains: `Due` lists
// by kind through the push worker, and a row whose kind nobody recognises would sit
// at `attempts=0` forever, which AC4 counts as an attempt with no result.
func ValidOutboxKind(k string) bool {
	switch k {
	case KindEvent, KindBookmark, KindAbsenceEvent:
		return true
	}
	return false
}

// Enqueue records the intent to push, in the same transaction the caller is
// already in. The row is created BEFORE the attempt, so a crash between the
// enqueue and the call leaves a pending row the recovery pass retries, and a
// crash after the call leaves a row whose result says "unknown", which the
// read-back call can then resolve.
func (s *Store) Enqueue(ctx context.Context, e OutboxEntry, maxAttempts int) (int64, error) {
	if e.DetectionID == "" || e.Kind == "" {
		return 0, errors.New("store: an outbox entry needs a detection and a kind; an unattributable push cannot be audited")
	}
	if !ValidOutboxKind(e.Kind) {
		return 0, fmt.Errorf("store: %q is not an outbox kind this build performs (%s, %s, %s); a row nothing drains "+
			"would sit at attempts=0 forever and AC4 would count it as an attempt with no result",
			e.Kind, KindEvent, KindBookmark, KindAbsenceEvent)
	}
	if e.Tag == "" {
		return 0, errors.New("store: an outbox entry needs a tag; the tag is the idempotency key the read-back uses, and a counter would collide across a restart")
	}
	// W2-6: the budget is stored on the row. It used to be accepted and
	// discarded (`_ = maxAttempts`), which meant a push the VMS will never accept
	// — a 403, a deleted event type — retried with exponential backoff until the
	// demo ended, and the run reported a pending row rather than a dead letter.
	if maxAttempts <= 0 {
		maxAttempts = DefaultPushAttempts
	}
	now := time.Now().UTC()
	res, err := s.exec(ctx, `
		INSERT INTO outbox (detection_id,kind,payload,tag,attempts,next_attempt,max_attempts,created_at,updated_at)
		VALUES (?,?,?,?,0,?,?,?,?)
		ON CONFLICT(detection_id,kind) DO NOTHING`,
		e.DetectionID, e.Kind, e.Payload, e.Tag, now, maxAttempts, nowUTC(now), nowUTC(now))
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
	return id, nil
}

// PendingAbsence returns the un-sent absence row for one blindness state, or nil.
//
// It is the aggregate's whole bookkeeping, and it asks for a row that has NOT been
// attempted yet: a row with a result is spent, and the next proposal in the same
// state must start a new window rather than join one whose count has already been
// published to the VMS. Two absences merged across a push would produce one remote
// event whose count understates how long the model was unreachable, and the count is
// the number an operator reads to decide how urgently to look.
//
// It matches on the tag prefix rather than on a column, because adding a column for
// this would be a migration to hold a string the tag already holds. `substr` and not
// `LIKE`: the tag is operator-derived text and a `%` or `_` in it would turn a
// prefix match into a pattern.
func (s *Store) PendingAbsence(ctx context.Context, tagPrefix string) (*OutboxEntry, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM outbox
		WHERE kind=? AND dead_letter=0 AND (result IS NULL OR result='')
		  AND substr(tag,1,?)=?
		ORDER BY id DESC LIMIT 1`, KindAbsenceEvent, len(tagPrefix), tagPrefix).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: cannot find the pending absence row for %q: %w", tagPrefix, err)
	}
	return s.Entry(ctx, id)
}

// RefreshAbsence rewrites a pending absence row's payload, its tag and its count.
//
// The TAG moves with the payload, and that is not an optimisation. The push worker
// reads `tag` from this column and passes it to the read-back call, while the tag
// INSIDE the payload is what the VMS stored: if only the payload were rewritten, the
// row would carry one key and the push would send another, the read-back would search
// for the first, find nothing, and report `unknown` — "the push returned 202 but the
// event was not found". The event would exist and the module would say it did not,
// and the count would be republished on the next tick.
//
// It is a CONDITIONAL update — `AND result IS NULL OR result=”` — and that condition
// is the concurrency control rather than a nicety. The push worker runs on its own
// goroutine: if it sends the row between the caller's read and this write, a blind
// UPDATE would overwrite a payload that has already been published, and the count in
// the VMS would then disagree with the count in the row with nothing to say which is
// right.
//
// A false return means the row was spent in between, and the caller starts a new
// window. That is the correct outcome, not an error.
func (s *Store) RefreshAbsence(ctx context.Context, id int64, payload, tag string, count int) (bool, error) {
	res, err := s.exec(ctx, `
		UPDATE outbox SET payload=?, tag=?, detail=?, updated_at=?
		WHERE id=? AND kind=? AND (result IS NULL OR result='')`,
		payload, tag, fmt.Sprintf("aggregated absence event, %d proposal(s) in this window", count),
		nowUTC(time.Now()), id, KindAbsenceEvent)
	if err != nil {
		return false, fmt.Errorf("store: cannot refresh outbox row %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DefaultPushAttempts is the budget a push gets when the caller says nothing.
//
// A number rather than "unlimited": a real VMS answers 403 for an event it will
// never accept, and a row that retries forever is a row whose absence nobody
// notices until they go looking for it.
const DefaultPushAttempts = 8

// RecordAttempt increments the attempt counter, schedules the next try, and
// dead-letters the row when the budget is spent.
//
// The dead-lettering is the fix and the reason this is one method rather than
// three: a budget enforced by the caller is a budget that a caller forgets, and
// the caller here is a two-second ticker with a context that gets cancelled.
func (s *Store) RecordAttempt(ctx context.Context, id int64) error {
	now := time.Now()
	attempts := currentAttempts(ctx, s, id)
	_, err := s.exec(ctx, `
		UPDATE outbox SET attempts=attempts+1, next_attempt=?, updated_at=? WHERE id=?`,
		nowUTC(now.Add(backoffFor(now, attempts))), nowUTC(now), id)
	if err != nil {
		return fmt.Errorf("store: cannot record an attempt on outbox row %d: %w", id, err)
	}
	return s.expireIfSpent(ctx, id, attempts+1)
}

// expireIfSpent dead-letters a row whose budget is gone.
//
// A row with max_attempts=0 predates the column and has no declared budget, so
// it is left alone rather than dead-lettered on its first attempt: the expand step
// is not allowed to change behaviour for rows that already exist.
func (s *Store) expireIfSpent(ctx context.Context, id int64, attempts int) error {
	var max int
	if err := s.db.QueryRowContext(ctx, `SELECT max_attempts FROM outbox WHERE id=?`, id).Scan(&max); err != nil {
		return nil
	}
	if max <= 0 || attempts < max {
		return nil
	}
	return s.DeadLetter(ctx, id, fmt.Sprintf(
		"the push budget of %d attempt(s) is spent and the VMS has not accepted this body; the row is dead-lettered rather "+
			"than retried forever, and it is kept for a human to read", max))
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
		SELECT id,detection_id,kind,payload,tag,attempts,next_attempt,result,detail,remote_id,dead_letter,max_attempts,created_at,updated_at
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
		if err := rows.Scan(&e.ID, &e.DetectionID, &e.Kind, &e.Payload, &e.Tag, &e.Attempts, &next, &result, &detail, &remoteID, &e.DeadLetter, &e.MaxAttempts, &created, &updated); err != nil {
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
		SELECT id,detection_id,kind,payload,tag,attempts,next_attempt,result,detail,remote_id,dead_letter,max_attempts,created_at,updated_at
		FROM outbox WHERE id=?`, id)
	var e OutboxEntry
	var next sql.NullString
	var result, detail, remoteID sql.NullString
	var created, updated string
	if err := row.Scan(&e.ID, &e.DetectionID, &e.Kind, &e.Payload, &e.Tag, &e.Attempts, &next, &result, &detail, &remoteID, &e.DeadLetter, &e.MaxAttempts, &created, &updated); err != nil {
		return nil, err
	}
	e.Result, e.Detail, e.RemoteID = result.String, detail.String, remoteID.String
	return &e, nil
}

// OutboxFor returns the entries belonging to a detection.
func (s *Store) OutboxFor(ctx context.Context, detectionID string) ([]OutboxEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,detection_id,kind,payload,tag,attempts,next_attempt,result,detail,remote_id,dead_letter,max_attempts,created_at,updated_at
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
