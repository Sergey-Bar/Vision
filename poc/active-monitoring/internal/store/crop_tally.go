package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The crop gate's tally, persisted beside the detector's.
//
// It exists for the same reason `detector_tally` does â€” `pocctl report` rebuilds the
// report from the store alone â€” and it closes a gap that one did not: `Runner.crops`
// was accumulated under a lock on every frame and read by nothing at all. The
// counter that says whether the novelty RATE gate is looking often enough was
// in memory and nowhere else for the whole life of this code, which is precisely the
// defect class this repository treats as worse than a missing feature: a number
// everybody assumes exists because its field name is written down.
//
// Opaque bytes, for the reason `detector_tally` gives: `internal/store` is below
// `internal/soak` and must not know the shape of a tally it has no query against.

// SaveCropTally writes (or replaces) a run's crop tally.
func (s *Store) SaveCropTally(ctx context.Context, runID string, tally any) error {
	body, err := json.Marshal(tally)
	if err != nil {
		return fmt.Errorf("store: the crop gate's tally for run %s cannot be encoded: %w", runID, err)
	}
	_, err = s.exec(ctx, `
		INSERT INTO crop_tally (run_id, at, json) VALUES (?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET at=excluded.at, json=excluded.json`,
		runID, nowUTC(time.Now()), string(body))
	if err != nil {
		return fmt.Errorf("store: cannot record the crop gate's tally for run %s: %w", runID, err)
	}
	return nil
}

// CropTally returns a run's stored crop tally.
//
// `found` is false when the run recorded none, which is NOT an error and NOT a zero
// tally: a run from before this existed recorded nothing, and printing zeros for it
// would say "the gate looked at every frame" about a run nobody measured.
func (s *Store) CropTally(ctx context.Context, runID string) (raw []byte, found bool, err error) {
	var body string
	err = s.db.QueryRowContext(ctx, `SELECT json FROM crop_tally WHERE run_id=?`, runID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		// No row for this run, which is the ordinary case for a run from before
		// this table existed or one whose pipeline died before it could write.
		return nil, false, nil
	}
	if err != nil {
		// Anything else is a real failure and is returned as one. The sibling
		// `DetectorTally` swallows every error here, and the reason this one does
		// not is that "the database is broken" and "there is no row" are different
		// facts: the first is one a caller should surface, and swallowing it turns
		// a broken store into a NOT RUN sentence about a run nobody measured.
		return nil, false, fmt.Errorf("store: cannot read the crop gate's tally for run %s: %w", runID, err)
	}
	return []byte(body), true, nil
}
