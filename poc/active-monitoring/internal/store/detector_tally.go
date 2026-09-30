package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// The detector's tally, persisted so a REGENERATED report can say what the live
// one said.
//
// It exists because `pocctl report` rebuilds the report from the store alone, and
// before this the detector's arithmetic was in memory and nowhere else. A run
// that produced no detections therefore reported `0` whether the model saw
// nothing or the pipeline refused everything it saw, and `pocctl report` could
// not tell you which because the information was gone.
//
// The blob is stored opaquely on purpose. `internal/store` is below
// `internal/soak` and must not know the shape of a tally it has no query against,
// so it moves bytes and nothing else.

// SaveDetectorTally writes (or replaces) a run's tally.
//
// It is an UPSERT and not an INSERT-only, because a run that is re-reported
// writes again, and a second row for the same run would make "which is the
// answer" a question.
func (s *Store) SaveDetectorTally(ctx context.Context, runID string, tally any) error {
	body, err := json.Marshal(tally)
	if err != nil {
		return fmt.Errorf("store: the detector's tally for run %s cannot be encoded: %w", runID, err)
	}
	_, err = s.exec(ctx, `
		INSERT INTO detector_tally (run_id, at, json) VALUES (?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET at=excluded.at, json=excluded.json`,
		runID, nowUTC(time.Now()), string(body))
	if err != nil {
		return fmt.Errorf("store: cannot record the detector's tally for run %s: %w", runID, err)
	}
	return nil
}

// DetectorTally returns a run's stored tally.
//
// `found` is false when the run recorded none, which is NOT an error and NOT an
// empty tally: a run from before this existed, or a run whose pipeline died
// before it could write one. The caller prints "NOT RUN" for that and says why —
// it must not print zeros, because zeros read as a measurement.
func (s *Store) DetectorTally(ctx context.Context, runID string) (raw []byte, found bool, err error) {
	var body string
	err = s.db.QueryRowContext(ctx, `SELECT json FROM detector_tally WHERE run_id=?`, runID).Scan(&body)
	if err != nil {
		// sql.ErrNoRows and anything else here means the same thing to a caller
		// that wants to print a sentence: this run has no tally.
		return nil, false, nil
	}
	return []byte(body), true, nil
}
