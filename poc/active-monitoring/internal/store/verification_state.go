package store

// The per-site, per-class verification-state slice.
//
// The platform has this slice (`operatormetrics.VerificationStatesBySiteAndClass`,
// reached by `securityctl efficiency`) and until this file existed the POC did
// not, which meant the one claim the POC is demonstrated with — that a camera
// can be told what to look for and watched for whether it is looking — was
// measurable on the platform and invisible in the deliverable.
//
// The question it answers is "where does this run stay quiet", and it is asked
// per site and per class because the answer is not a number. A run whose cells
// skew to the blindness states is a run the AI plane was not serving, and the
// classes that skew are the Alarm Definitions to look at. A run whose cells are
// all `no_objects` is a run about the road. Those two print the same
// `detections | 0` in every other section of this module's report, and this
// module has shipped that exact confusion three times.
//
// "Site" here is the run's source, because that is what a site IS in this
// module: one run is one camera or one file in a loop, recorded on the run row
// at insert and never re-read from the environment afterwards. There is no
// sites table to join and inventing one would be a second source of truth for
// the identity the run row already holds.
//
// The states are CONTRACT.md §2.2's four plus `cannot_tell`, and the four are
// the platform's `verification_results.state` values seen from the other end:
// where the platform asks "did this DEFINITION reach a verdict", this asks "did
// this DETECTION ever get an answer". Both collapses of the same question —
// "the system decided not to speak" — into one place, and neither may be
// computed by folding the blind states into the answered one.

import (
	"context"
	"fmt"
)

// VerificationCell is one (site, class, state) cell of the slice.
//
// A cell is a COUNT and nothing else. No rate, no share: a share over a
// denominator nobody printed is a number about the denominator, and this
// slice's whole point is that the denominator is visible.
type VerificationCell struct {
	Site  string `json:"site"`
	Class string `json:"class"`
	State string `json:"state"`
	Count int    `json:"count"`
}

// VerificationStatesBySiteAndClass groups a run's detections by their source,
// their class, and the state of the ask about each one.
//
// The stale and dropped states are excluded for the same reason
// CountByClassFamily excludes them: a detection abandoned mid-pipeline never
// reached the AI plane, so counting it would put an unanswered ask in the
// denominator of a question about answering. That exclusion is why this
// function's total is not the run's detection count, and a reader must be able
// to see the difference — `soak.VerificationSlice` prints both.
//
// No rows are not a stated zero. An empty slice means no detection ever reached
// this query, which is a fact about the run and not a measurement of the plane,
// and the report prints NOT RUN rather than a 0% that reads as a quiet road.
func (s *Store) VerificationStatesBySiteAndClass(ctx context.Context, runID string) ([]VerificationCell, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ru.source_arg, d.class, d.ai_state, COUNT(*)
		FROM detections d JOIN runs ru ON ru.id = d.run_id
		WHERE d.run_id=? AND d.status NOT IN ('stalled','dropped')
		GROUP BY ru.source_arg, d.class, d.ai_state
		ORDER BY ru.source_arg, d.class, d.ai_state`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot group verification states by site and class: %w", err)
	}
	defer rows.Close()
	var out []VerificationCell
	for rows.Next() {
		var c VerificationCell
		if err := rows.Scan(&c.Site, &c.Class, &c.State, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// VerificationTotals is the run-level face of the same slice: one state, one
// count, with nothing grouped by anything.
//
// It exists because a reader who is handed only per-class cells has to add
// eight of them to answer "was the model being served at all", which is the
// first question anybody asks of this slice.
func (s *Store) VerificationTotals(ctx context.Context, runID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.ai_state, COUNT(*)
		FROM detections d
		WHERE d.run_id=? AND d.status NOT IN ('stalled','dropped')
		GROUP BY d.ai_state
		ORDER BY d.ai_state`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot count verification states: %w", err)
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

// RetainedDetections is the slice's denominator, named so the report can print
// it beside the slice without recomputing the exclusion a second time.
//
// `Count` is every detection of the run, including the abandoned ones, so the
// difference between the two is itself information: a run that dropped most of
// its detections has a pipeline problem, and folding it into a picture about
// the AI plane would hide the pipeline behind the plane.
func (s *Store) RetainedDetections(ctx context.Context, runID string) (int, int, error) {
	var total, retained int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM detections WHERE run_id=?`, runID).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("store: cannot count the run's detections: %w", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM detections WHERE run_id=? AND status NOT IN ('stalled','dropped')`, runID).Scan(&retained); err != nil {
		return 0, 0, fmt.Errorf("store: cannot count the run's retained detections: %w", err)
	}
	return total, retained, nil
}
