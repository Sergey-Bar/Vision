package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The ground-truth review. D-8.
//
// It is a REVIEW and not a measurement the machine made, and the whole design is
// arranged to keep it that way. Nothing here infers a verdict, scores a frame,
// compares a snapshot to anything, or produces a number when nobody looked.
// There are exactly two things in this file: a way to record what a person said
// about an event that was pushed, and a way to count what they said.
//
// The refusal that carries the weight is in MarkGroundTruth: a mark whose tag is
// not in this run's OUTBOX is refused by name. That is the join the plan asks
// for, and it is here rather than in the report because the report is the thing
// the number comes out of, and a number that can be produced from a tag nobody
// ever pushed is a number nobody can check.

// The two verdicts. A closed set, because a third word would be a mark no
// reader of the report could interpret, and it would be counted.
const (
	GroundTruthTruePositive  = "true_positive"
	GroundTruthFalsePositive = "false_positive"
)

// ValidGroundTruth is the closed set.
func ValidGroundTruth(v string) bool {
	return v == GroundTruthTruePositive || v == GroundTruthFalsePositive
}

// MarkGroundTruth records one person's verdict about one pushed event.
//
// `by` and `note` are arguments and not fields of a struct because
// `soak.HumanMarking.Verdict` returns NOT RUN when nobody is recorded as having
// marked the events — a rate with no author is a number somebody typed — so
// storing the tag without the author would store a mark that can never be
// printed as a measurement. Six parameters is the price of that, and it is
// cheaper than a rate nobody can trace back to the person who looked.
func (s *Store) MarkGroundTruth(ctx context.Context, runID, tag, verdict, by, note string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return fmt.Errorf("store: a ground-truth mark needs the event tag it is about; an empty tag is a verdict about nothing")
	}
	if !ValidGroundTruth(verdict) {
		return fmt.Errorf("store: %q is neither %s nor %s. Those are the only two things a person can say about an event, and a third word would be counted by a report nobody could read it with", verdict, GroundTruthTruePositive, GroundTruthFalsePositive)
	}
	// The join. A mark has to be about an event THIS run pushed.
	matches, err := s.pushedTags(ctx, runID, tag)
	if err != nil {
		return fmt.Errorf("store: cannot check whether %q was pushed on run %s: %w", tag, runID, err)
	}
	if matches == 0 {
		return fmt.Errorf("store: %q is not a tag this run pushed%s. A mark is attached to an event that actually left the process, and matching one to a tag nobody sent would put a person's verdict on nothing",
			tag, s.pushedSample(ctx, runID))
	}
	_, err = s.exec(ctx, `
		INSERT INTO ground_truth (run_id, tag, verdict, by, note, at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(run_id, tag) DO UPDATE SET verdict=excluded.verdict, by=excluded.by,
		                                       note=excluded.note, at=excluded.at`,
		runID, tag, verdict, strings.TrimSpace(by), note, nowUTC(time.Now()))
	if err != nil {
		return fmt.Errorf("store: cannot record the ground-truth mark for %s: %w", tag, err)
	}
	return nil
}

// pushedTags is the join itself: does an outbox row in this run carry this tag?
func (s *Store) pushedTags(ctx context.Context, runID, tag string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox o JOIN detections d ON d.id=o.detection_id
		WHERE d.run_id=? AND o.tag=?`, runID, tag).Scan(&n)
	return n, err
}

// pushedSample names a few tags the run DID push, because a refusal nobody can
// act on is a refusal nobody will act on. It is the difference between "unknown
// tag" and "unknown tag; this run pushed aicap-car-91f2…, aicap-car-0c41…".
func (s *Store) pushedSample(ctx context.Context, runID string) string {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.tag FROM outbox o JOIN detections d ON d.id=o.detection_id
		WHERE d.run_id=? ORDER BY o.id LIMIT 3`, runID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return ""
		}
		tags = append(tags, t)
	}
	if len(tags) == 0 {
		return ""
	}
	return "; this run pushed " + strings.Join(tags, ", ")
}

// GroundTruthMark is one recorded verdict, for the report to print.
type GroundTruthMark struct {
	Tag     string
	Verdict string
	By      string
	Note    string
	At      time.Time
}

// GroundTruth is a run's review.
func (s *Store) GroundTruth(ctx context.Context, runID string) ([]GroundTruthMark, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tag,verdict,by,note,at FROM ground_truth WHERE run_id=? ORDER BY tag`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the ground-truth review: %w", err)
	}
	defer rows.Close()
	var out []GroundTruthMark
	for rows.Next() {
		var m GroundTruthMark
		var at string
		if err := rows.Scan(&m.Tag, &m.Verdict, &m.By, &m.Note, &at); err != nil {
			return nil, err
		}
		m.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Precision is the number: true positives and false positives, and whether it
// was measured at all.
//
// `measured` is false when nobody marked anything, and the caller MUST print
// that as NOT RUN. There is deliberately no fourth return value and no default,
// because the only two states this can be in are "a person looked" and "nobody
// looked", and a zero returned for the second one is how a module ends up
// claiming a false-positive rate of 0.00 that no human produced.
//
// Precision = TP / (TP + FP), which is the number the plan asks for and NOT
// TP/total examined: an unmarked event is not a true positive, it is an event
// nobody looked at, and folding it in would make the rate better the less
// anyone reviewed.
func (s *Store) Precision(ctx context.Context, runID string) (tp, fp int, measured bool, err error) {
	var ntp, nfp, total int
	err = s.db.QueryRowContext(ctx, `
		SELECT
		  SUM(CASE WHEN verdict='true_positive' THEN 1 ELSE 0 END),
		  SUM(CASE WHEN verdict='false_positive' THEN 1 ELSE 0 END),
		  COUNT(*)
		FROM ground_truth WHERE run_id=?`, runID).
		Scan(&nullInt{&ntp}, &nullInt{&nfp}, &total)
	if err != nil {
		return 0, 0, false, fmt.Errorf("store: cannot count the ground-truth review: %w", err)
	}
	return ntp, nfp, total > 0, nil
}
