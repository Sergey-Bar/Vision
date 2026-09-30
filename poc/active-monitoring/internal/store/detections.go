package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/evidence"
)

// Detection is one line-cross event. It is written once, in `accepted`, inside
// a transaction, before the clip is cut and before anything is pushed. Every
// later change is a status transition, and the transitions are one-way.
type Detection struct {
	ID    string
	RunID string
	At    time.Time
	Class string
	// Family is the class's family, resolved by internal/classify and stored
	// BESIDE the class rather than inside it. T7.1.
	//
	// The two answer different questions and merging them would break the wrong
	// one: `Class` is the model's own word and is what an Alarm Definition is
	// written against, so a rule that says "car" must keep matching when the
	// taxonomy is extended or the display format changes; `Family` is the
	// product's grouping and is what a report is read by, so that a reader can
	// count vehicles without a lookup table open in the next window. A detection
	// with an empty Family is a detection written before this column existed, and
	// an empty family is NOT the same claim as "the family is `other`".
	Family     string
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
	// SnapshotRef is the rendered crossing snapshot, as a reference into the
	// evidence store rather than as bytes. T6.3. It is set on a later transition,
	// not at accept: the snapshot is drawn from a frame the pipeline has not read
	// yet when the event is committed.
	SnapshotRef string
	// SnapshotKind is what KIND of evidence SnapshotRef points at, from the
	// closed set below. T7.3. A reference with no kind is a reference a reader
	// cannot open with confidence about what they are about to see.
	SnapshotKind string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// The kinds of evidence a detection can point at. T7.3.
//
// They are DEFINED in internal/evidence and reached from here, and the direction
// is one way: evidence is the lower package because it owns the bytes and chooses
// the extension a reader's file manager shows. A second copy of three strings
// here would be a second source of truth for the same closed set, and the failure
// mode of that is a row saying `crossing_snapshot` beside a file called `.mp4` —
// a reader who opens a picture expecting a video and has no way to tell which of
// the two is lying.
const (
	EvidenceCrossingSnapshot = evidence.CrossingSnapshot
	EvidenceEventClip        = evidence.EventClip
	EvidenceFrame            = evidence.Frame
)

// ValidEvidenceKind is the closed set. A kind outside it is refused rather than
// stored: a reader that cannot look the kind up will not know what opening the
// file means.
func ValidEvidenceKind(k string) bool { return evidence.ValidKind(k) }

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
		INSERT INTO detections (id,run_id,at,class,family,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.RunID, nowUTC(d.At), d.Class, nullIfEmpty(d.Family), d.TrackID, d.Score,
		d.BoxTop, d.BoxLeft, d.BoxBottom, d.BoxRight,
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
	row := s.db.QueryRowContext(ctx, `SELECT id,run_id,at,class,family,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,clip_ref,clip_reason,snapshot_ref,snapshot_kind,created_at,updated_at
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
	// The three new columns are NULLABLE, so all three are scanned as nullable and the
	// empty string is what a NULL becomes. A detection with an empty Family is a
	// detection written before the column existed, and an empty family is NOT the
	// same claim as the family being other.
	var family, clipRef, clipReason, snapRef, snapKind sql.NullString
	if err := row.Scan(&d.ID, &d.RunID, &at, &d.Class, &family, &d.TrackID, &d.Score, &d.BoxTop, &d.BoxLeft, &d.BoxBottom, &d.BoxRight,
		&d.Direction, &d.Status, &d.AIState, &d.Provenance, &clipRef, &clipReason, &snapRef, &snapKind, &created, &updated); err != nil {
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
	d.Family, d.SnapshotRef, d.SnapshotKind = family.String, snapRef.String, snapKind.String
	return &d, nil
}

// SetSnapshot records the rendered crossing snapshot against a detection.
//
// It is a SEPARATE method and not a parameter of Accept, and the reason is
// ordering: a detection is committed in `accepted` inside the event transaction,
// and the snapshot is drawn from a frame the pipeline has not read yet at that
// moment. So the picture of the decision is written immediately after the
// decision, and a crash between the two leaves a detection with no snapshot —
// which is visible, rather than a detection that claims a picture exists when it
// does not.
//
// The kind is REQUIRED whenever a reference is given. A reference with no kind is
// a file a reader opens not knowing whether it is a video, a still, or a raw
// frame, and the three demand different things of whoever opens them.
func (s *Store) SetSnapshot(ctx context.Context, id, ref, kind string) error {
	if ref == "" {
		// Clearing it is not a thing anyone needs, and a silent clear would let a
		// retry of the wrong kind wipe a real reference.
		return errors.New("store: a snapshot reference cannot be empty; use the absence of a call to mean 'no snapshot'")
	}
	if !ValidEvidenceKind(kind) {
		return fmt.Errorf("store: %q is not one of the three evidence kinds (%s, %s, %s). A reference with no kind a reader can look up is a file they open not knowing what it is",
			kind, EvidenceCrossingSnapshot, EvidenceEventClip, EvidenceFrame)
	}
	wctx, cancel := s.writeCtx(ctx)
	defer cancel()
	res, err := s.exec(wctx, `
		UPDATE detections SET snapshot_ref=?, snapshot_kind=?, updated_at=? WHERE id=?`,
		ref, kind, nowUTC(time.Now()), id)
	if err != nil {
		return fmt.Errorf("store: cannot record the snapshot for detection %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: detection %s does not exist, so its snapshot was not recorded. A snapshot that says nothing about a detection that is not there would leave a file on disk that nothing points at", id)
	}
	return nil
}

// CountByClassFamily is the query the report's family histogram is built from, and
// it is a query rather than a loop over `Recent` because the class and the family
// are two columns of the same row and reading them apart in Go would be a
// re-implementation of a GROUP BY with a chance of getting the nulls wrong.
func (s *Store) CountByClassFamily(ctx context.Context, runID string) (map[string]map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT class, family, COUNT(*) FROM detections
		WHERE run_id=? AND status NOT IN ('stalled','dropped')
		GROUP BY class, family`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot count detections by class and family: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var class, family string
		var n int
		if err := rows.Scan(&class, &family, &n); err != nil {
			return nil, err
		}
		if out[class] == nil {
			out[class] = map[string]int{}
		}
		out[class][family] = n
	}
	return out, rows.Err()
}

// Stalled lists detections that have been in a non-terminal state longer than
// the limit. This list is AC2's whole content, and it is expected to be empty.
func (s *Store) Stalled(ctx context.Context, runID string, limit time.Duration) ([]Detection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,run_id,at,class,family,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,clip_ref,clip_reason,snapshot_ref,snapshot_kind,created_at,updated_at
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
		SELECT id,run_id,at,class,family,track_id,score,box_top,box_left,box_bottom,box_right,direction,status,ai_state,provenance,clip_ref,clip_reason,snapshot_ref,snapshot_kind,created_at,updated_at
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
