// The recording of what the detector measured, and of what a human said about it.
//
// Three tables and one rule connecting them: a number about a run may only be
// recomputed from these rows. Anything a report shows about precision, recall,
// false positives per hour or track stability is arithmetic over this file, so
// the file is the claim and the report is a reading of it.
//
// `box_observations` is the per-frame record, `labels` is a human's verdict on a
// proposed box, and `label_objects` is an object a human drew because the
// detector missed it. The third table is the one that makes recall measurable at
// all: without a drawn box, "the detector found nothing" and "there was nothing"
// are the same row, and no arithmetic over it can tell them apart.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BoxObservation is one box the detector reported on one frame.
//
// Measured is the field that carries the weight. A frame the pipeline never asked
// about is recorded with Measured false and carries no boxes, and it is recorded
// at all — because a report that counts rows and ignores this column will report
// a recall of zero from a run in which the detector was never invoked, and that
// is the exact defect this repository shipped once.
type BoxObservation struct {
	RunID    string
	FrameSeq int64
	At       time.Time
	// TrackID is the tracker's identity, or -1 for a box no track claimed.
	TrackID int
	Class   string
	// Family is nullable and a nil means "not recorded", where an empty string
	// would mean "recorded as no family". Same rule as detections.family.
	Family *string
	Score  float64
	// The box, in 0..1000 thousandths against the frame — the same scale
	// detections.box_* and the decisions region already use. Three tables in
	// this store and one scale; a second scale is a reader that disagrees.
	Top    int
	Left   int
	Bottom int
	Right  int
	// Source names the decoder: `detector` or `sidecar`. A measurement made by
	// the Python process must never be reported as one made by the shipped path,
	// and the only way to guarantee that is for the row to say which.
	Source   string
	Measured bool
}

// PutBoxObservations writes one frame's boxes in a single transaction.
//
// One transaction, not one statement per box: a frame whose boxes are half
// written is a frame that looks like an occlusion, and replay would read a
// track that skipped. The whole frame is atomic or it is not there.
func (s *Store) PutBoxObservations(ctx context.Context, obs []BoxObservation) error {
	if len(obs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: cannot begin the box-observation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO box_observations
			(run_id,frame_seq,at,track_id,class,family,score,box_top,box_left,box_bottom,box_right,source,measured)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("store: cannot prepare the box-observation insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, o := range obs {
		if o.RunID == "" {
			return errors.New("store: a box observation needs a run; an unattributable measurement is not a measurement")
		}
		if _, err := stmt.ExecContext(ctx, o.RunID, o.FrameSeq, nowUTC(o.At), o.TrackID, o.Class,
			o.Family, o.Score, o.Top, o.Left, o.Bottom, o.Right, o.Source, boolInt(o.Measured)); err != nil {
			return fmt.Errorf("store: cannot record a box observation on frame %d of %s: %w", o.FrameSeq, o.RunID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: cannot commit the box observations for frame %d: %w", obs[0].FrameSeq, err)
	}
	return nil
}

// BoxObservations returns a run's per-frame boxes in frame order.
//
// Ordered by frame_seq and not by at, because a replay must re-run the frames in
// the order the tracker saw them. Two frames can carry the same timestamp from a
// coarse clock, and an ordering that cannot tell them apart is an ordering that
// makes a track non-deterministic.
func (s *Store) BoxObservations(ctx context.Context, runID string) ([]BoxObservation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id,frame_seq,at,track_id,class,family,score,box_top,box_left,box_bottom,box_right,source,measured
		FROM box_observations WHERE run_id=? ORDER BY frame_seq, track_id, class`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the box observations for %s: %w", runID, err)
	}
	defer rows.Close()
	var out []BoxObservation
	for rows.Next() {
		var o BoxObservation
		var at string
		var family sql.NullString
		var measured int
		if err := rows.Scan(&o.RunID, &o.FrameSeq, &at, &o.TrackID, &o.Class, &family, &o.Score,
			&o.Top, &o.Left, &o.Bottom, &o.Right, &o.Source, &measured); err != nil {
			return nil, fmt.Errorf("store: cannot read a box observation: %w", err)
		}
		o.At, _ = time.Parse(time.RFC3339Nano, at)
		if family.Valid {
			f := family.String
			o.Family = &f
		}
		o.Measured = measured != 0
		out = append(out, o)
	}
	return out, rows.Err()
}

// FramesMeasured counts the frames the detector actually reported boxes for.
//
// This is the third of the three numbers the report carries, beside frames
// decoded and frames logged. All three are needed because "the model saw nothing"
// and "the pipeline never asked" print identically on a report that prints only
// one.
func (s *Store) FramesMeasured(ctx context.Context, runID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT frame_seq) FROM box_observations WHERE run_id=? AND measured=1`, runID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: cannot count the measured frames for %s: %w", runID, err)
	}
	return n, nil
}

// The two verdicts a human may record. A closed set: "mostly yes" is not a label
// anybody can count, and a false-positive rate needs a denominator.
const (
	LabelAccept = "accept"
	LabelReject = "reject"
)

// Label is one human's judgement about one proposed box.
type Label struct {
	RunID    string
	FrameSeq int64
	// BoxKey identifies WHICH box in that frame. It is a string rather than the
	// four coordinates because a human marks the box they were shown, and the
	// coordinates are how it is identified afterwards.
	BoxKey  string
	Verdict string
	Class   string
	// By is who marked it. A false-positive rate with no name against it is a
	// number from nowhere.
	By   string
	Note string
	At   time.Time
}

// PutLabel records or updates one judgement.
func (s *Store) PutLabel(ctx context.Context, l Label) error {
	if l.RunID == "" || l.BoxKey == "" || l.By == "" {
		return errors.New("store: a label needs a run, a box and a name; a judgement nobody made is not a label")
	}
	if l.Verdict != LabelAccept && l.Verdict != LabelReject {
		return fmt.Errorf("store: label verdict must be %q or %q, not %q", LabelAccept, LabelReject, l.Verdict)
	}
	if l.At.IsZero() {
		l.At = time.Now().UTC()
	}
	_, err := s.exec(ctx, `
		INSERT INTO labels (run_id,frame_seq,box_key,verdict,class,by,note,at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(run_id,frame_seq,box_key) DO UPDATE SET
			verdict=excluded.verdict, class=excluded.class, by=excluded.by,
			note=excluded.note, at=excluded.at`,
		l.RunID, l.FrameSeq, l.BoxKey, l.Verdict, l.Class, l.By, l.Note, nowUTC(l.At))
	if err != nil {
		return fmt.Errorf("store: cannot record the label on frame %d of %s: %w", l.FrameSeq, l.RunID, err)
	}
	return nil
}

// Labels returns a run's judgements in frame order.
func (s *Store) Labels(ctx context.Context, runID string) ([]Label, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id,frame_seq,box_key,verdict,class,by,note,at
		FROM labels WHERE run_id=? ORDER BY frame_seq, box_key`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the labels for %s: %w", runID, err)
	}
	defer rows.Close()
	var out []Label
	for rows.Next() {
		var l Label
		var at string
		if err := rows.Scan(&l.RunID, &l.FrameSeq, &l.BoxKey, &l.Verdict, &l.Class, &l.By, &l.Note, &at); err != nil {
			return nil, fmt.Errorf("store: cannot read a label: %w", err)
		}
		l.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, l)
	}
	return out, rows.Err()
}

// LabelCounts is how many boxes were accepted and how many were rejected.
//
// Both are returned even when one is zero, because a run with 400 rejects and no
// accepts is a completely different claim from a run with nothing marked at all,
// and a struct that omitted the empty half would make them print alike.
type LabelCounts struct {
	Accepted int
	Rejected int
	// Marked is how many PROPOSED boxes carry a judgement. The gap between
	// Marked and the number of proposed boxes is the remaining work, and the
	// report prints that number so a marking session has a target.
	Marked int
	// By names who marked them, so a row nobody can attribute is visible.
	By []string
}

// LabelTally summarises a run's judgements.
func (s *Store) LabelTally(ctx context.Context, runID string) (LabelCounts, error) {
	var c LabelCounts
	rows, err := s.db.QueryContext(ctx,
		`SELECT verdict, COUNT(*), GROUP_CONCAT(DISTINCT by) FROM labels WHERE run_id=? GROUP BY verdict`, runID)
	if err != nil {
		return c, fmt.Errorf("store: cannot tally the labels for %s: %w", runID, err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var verdict string
		var n int
		var by sql.NullString
		if err := rows.Scan(&verdict, &n, &by); err != nil {
			return c, fmt.Errorf("store: cannot read a label tally: %w", err)
		}
		c.Marked += n
		switch verdict {
		case LabelAccept:
			c.Accepted = n
		case LabelReject:
			c.Rejected = n
		}
		if by.Valid {
			for _, name := range strings.Split(by.String, ",") {
				if name != "" && !seen[name] {
					seen[name] = true
					c.By = append(c.By, name)
				}
			}
		}
	}
	return c, rows.Err()
}

// LabelObject is an object a human drew on a frame: a thing the detector missed,
// or one it reported and the human wants to correct the box of.
type LabelObject struct {
	RunID    string
	FrameSeq int64
	// Idx is the human's own index within the frame.
	Idx    int
	Class  string
	Top    int
	Left   int
	Bottom int
	Right  int
	By     string
	At     time.Time
}

// PutLabelObject records one drawn box.
func (s *Store) PutLabelObject(ctx context.Context, o LabelObject) error {
	if o.RunID == "" || o.By == "" || o.Class == "" {
		return errors.New("store: a drawn object needs a run, a class and a name")
	}
	if o.Right <= o.Left || o.Bottom <= o.Top {
		return fmt.Errorf("store: refusing to record the degenerate drawn box (%d,%d,%d,%d) on frame %d of %s; "+
			"evidence that names no place is not a measurement",
			o.Top, o.Left, o.Bottom, o.Right, o.FrameSeq, o.RunID)
	}
	if o.At.IsZero() {
		o.At = time.Now().UTC()
	}
	_, err := s.exec(ctx, `
		INSERT INTO label_objects (run_id,frame_seq,idx,class,box_top,box_left,box_bottom,box_right,by,at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(run_id,frame_seq,idx) DO UPDATE SET
			class=excluded.class, box_top=excluded.box_top, box_left=excluded.box_left,
			box_bottom=excluded.box_bottom, box_right=excluded.box_right, by=excluded.by, at=excluded.at`,
		o.RunID, o.FrameSeq, o.Idx, o.Class, o.Top, o.Left, o.Bottom, o.Right, o.By, nowUTC(o.At))
	if err != nil {
		return fmt.Errorf("store: cannot record the drawn object %d on frame %d of %s: %w", o.Idx, o.FrameSeq, o.RunID, err)
	}
	return nil
}

// LabelObjects returns a run's drawn boxes in frame order.
func (s *Store) LabelObjects(ctx context.Context, runID string) ([]LabelObject, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT run_id,frame_seq,idx,class,box_top,box_left,box_bottom,box_right,by,at
		FROM label_objects WHERE run_id=? ORDER BY frame_seq, idx`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the drawn objects for %s: %w", runID, err)
	}
	defer rows.Close()
	var out []LabelObject
	for rows.Next() {
		var o LabelObject
		var at string
		if err := rows.Scan(&o.RunID, &o.FrameSeq, &o.Idx, &o.Class, &o.Top, &o.Left,
			&o.Bottom, &o.Right, &o.By, &at); err != nil {
			return nil, fmt.Errorf("store: cannot read a drawn object: %w", err)
		}
		o.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ProposedBoxCount counts the boxes offered for judgement on a run: the measured
// box observations. It is the denominator of a marking session's progress, and
// it is a count of MEASURED rows only — offering a human the frames the
// pipeline skipped would be asking them to confirm a silence.
func (s *Store) ProposedBoxCount(ctx context.Context, runID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM box_observations WHERE run_id=? AND measured=1 AND track_id >= 0`, runID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: cannot count the proposed boxes for %s: %w", runID, err)
	}
	return n, nil
}
