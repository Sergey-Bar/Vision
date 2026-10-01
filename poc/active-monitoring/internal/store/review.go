package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Reviewable is one pushed event, with everything a person needs to judge it.
type Reviewable struct {
	DetectionID string
	At          time.Time
	Class       string
	Family      string
	Score       float64
	// X1..Y2 are the measured box in NORMALISED 0..1 coordinates. `detections` stores
	// thousandths and a judge asked "where in the frame" should not be handed 286.
	X1, Y1, X2, Y2 float64
	// Tag is the content-derived tag this event was pushed under, read from the
	// OUTBOX rather than derived. `MarkGroundTruth` joins on it, so a tag this
	// function invented would produce a mark command the store refuses, and a person
	// would read that refusal as the tool being broken.
	Tag string
	// ClipRef is the evidence row's content-addressed storage reference.
	ClipRef string
	// Verdict is the human's answer when one exists, and "" when not. It is a
	// pointer-shaped emptiness rather than a default, because "nobody looked" and
	// "somebody said false_positive" must not print the same.
	Verdict  string
	MarkedBy string
	MarkedAt time.Time
}

// Reviewable returns the pushed events of a run, newest first, joined to their tag,
// their clip and their human verdict.
//
// It is one query because the three of them are one fact: the thing that was pushed,
// the thing that proves it, and the person's answer. Assembled separately they could
// disagree — a sheet showing a verdict for an event with no clip is the exact thing
// that makes a false-positive rate a measurement of a guess.
func (s *Store) Reviewable(ctx context.Context, runID string, limit int) ([]Reviewable, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.at, d.class, d.family, d.score,
		       d.box_left, d.box_top, d.box_right, d.box_bottom,
		       COALESCE((SELECT o.tag FROM outbox o WHERE o.detection_id=d.id ORDER BY o.id LIMIT 1),''),
		       COALESCE(NULLIF(d.clip_ref,''), COALESCE((SELECT e.storage_ref FROM evidence e WHERE e.detection_id=d.id ORDER BY e.created_at DESC LIMIT 1),'')),
		       COALESCE((SELECT g.verdict FROM ground_truth g WHERE g.run_id=d.run_id AND g.tag=
		                 (SELECT o.tag FROM outbox o WHERE o.detection_id=d.id ORDER BY o.id LIMIT 1)),''),
		       COALESCE((SELECT g.by FROM ground_truth g WHERE g.run_id=d.run_id AND g.tag=
		                 (SELECT o.tag FROM outbox o WHERE o.detection_id=d.id ORDER BY o.id LIMIT 1)),''),
		       COALESCE((SELECT g.at FROM ground_truth g WHERE g.run_id=d.run_id AND g.tag=
		                 (SELECT o.tag FROM outbox o WHERE o.detection_id=d.id ORDER BY o.id LIMIT 1)),'')
		FROM detections d
		WHERE d.run_id=? AND EXISTS (SELECT 1 FROM outbox o WHERE o.detection_id=d.id)
		ORDER BY d.at DESC
		LIMIT ?`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the reviewable events for run %s: %w", runID, err)
	}
	defer rows.Close()

	var out []Reviewable
	for rows.Next() {
		var r Reviewable
		var at, marked string
		var bl, bt, br, bb int
		if err := rows.Scan(&r.DetectionID, &at, &r.Class, &r.Family, &r.Score,
			&bl, &bt, &br, &bb, &r.Tag, &r.ClipRef, &r.Verdict, &r.MarkedBy, &marked); err != nil {
			return nil, fmt.Errorf("store: cannot read a reviewable event: %w", err)
		}
		r.At, _ = time.Parse(time.RFC3339Nano, at)
		if marked != "" {
			r.MarkedAt, _ = time.Parse(time.RFC3339Nano, marked)
		}
		r.X1, r.Y1, r.X2, r.Y2 = norm(bl), norm(bt), norm(br), norm(bb)
		if strings.TrimSpace(r.Tag) == "" {
			// No tag means nothing was pushed under one, and `mark` would refuse it.
			// Rendering the row anyway with an empty tag would hand out a command
			// that cannot work, so it is named as unmarked instead.
			r.Tag = "(not pushed)"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// norm turns a stored thousandth into a fraction, clamped.
func norm(v int) float64 {
	f := float64(v) / 1000
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}
