package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
)

// HourBeat is one hour of a run: was the stream alive, did the detector propose
// anything, and was the model consulted about what it proposed.
//
// Every field is counted rather than inferred. `Frames` is the heartbeats' own
// frames counter and `Answered` is the number of observations with the ANSWERED
// state, and both are zero when there is nothing, which is why the caller has to
// distinguish zero-that-means-zero from zero-that-means-absent.
type HourBeat struct {
	Hour time.Time
	// Frames is how many frames were READ IN THIS HOUR, computed as the difference
	// between this hour's last reported counter and the previous hour's.
	//
	// It is a difference and not the raw value because the heartbeat counter is
	// CUMULATIVE: printing the raw value would show 3,000,000 in every hour of a
	// six-hour run that read three million frames in the first one. A liveness table
	// whose numbers cannot be added up is a table nobody trusts.
	Frames int64
	// Proposed is how many detections the store holds with an `at` in this hour. It
	// is counted from the detections table and not from the heartbeat counter,
	// because the heartbeat counter is a running total and this is a per-hour figure.
	Proposed  int
	Answered  int
	Confirmed int
	// NeedsReview is the verdict a model returns when it cannot settle it, and it is
	// not a confirmation. Counting it as one would be the single easiest way to
	// make this table flatter and less true.
	NeedsReview int
}

// HourlyLiveness returns one row per hour a run reported in.
//
// It is `heartbeats` grouped by hour, because `heartbeats` is already (run_id, at)
// and exists for exactly this: AC1 is computed from its gaps, and a brief is the
// same evidence read a different way. An hour with NO heartbeat rows does not appear
// at all, and that absence is itself the signal — a gap in this table is a gap in
// liveness, and the caller prints it as one.
func (s *Store) HourlyLiveness(ctx context.Context, runID string) ([]HourBeat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT at, frames FROM heartbeats WHERE run_id=? ORDER BY at`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read heartbeats for run %s: %w", runID, err)
	}
	defer rows.Close()

	byHour := map[string]*HourBeat{}
	var keys []string
	var prev int64
	for rows.Next() {
		var raw string
		var frames int64
		if err := rows.Scan(&raw, &frames); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, fmt.Errorf("store: heartbeat %q is not a timestamp: %w", raw, err)
		}
		key := hourKey(at)
		h, ok := byHour[key]
		if !ok {
			h = &HourBeat{Hour: parseHourKey(key)}
			byHour[key] = h
			keys = append(keys, key)
		}
		// The counter is cumulative and monotonic, so the delta between the previous
		// heartbeat and this one is the frames read in the interval between them. It
		// is clamped at zero because a counter that went backwards is a store that
		// was reopened, and a negative hour is not a thing a reader can interpret.
		if d := frames - prev; d > 0 {
			h.Frames += d
		}
		prev = frames
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(byHour) == 0 {
		return nil, nil
	}

	// The per-hour proposal and answer counts come from the detections and
	// observations tables. A heartbeat counter cannot say how many were ANSWERED,
	// because liveness is recorded by a process that is not the one that asks.
	if err := s.hourlyProposals(ctx, runID, byHour); err != nil {
		return nil, err
	}
	return sortHours(keys, byHour), nil
}

func (s *Store) hourlyProposals(ctx context.Context, runID string, byHour map[string]*HourBeat) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.at,
		       COUNT(*),
		       SUM(CASE WHEN v.aistate=? THEN 1 ELSE 0 END),
		       SUM(CASE WHEN v.verdict=? THEN 1 ELSE 0 END),
		       SUM(CASE WHEN v.verdict=? THEN 1 ELSE 0 END)
		FROM detections d LEFT JOIN vlm_observations v ON v.detection_id=d.id
		WHERE d.run_id=? GROUP BY d.at ORDER BY d.at`,
		// The three states and verdicts are LITERALS here for the same reason
		// `aistate.Asked` is: internal/store sits below internal/vlm and must not
		// import it for vocabulary. The closed sets are pinned by a test in
		// observations_test.go, so a divergence is caught rather than assumed.
		aistate.Asked, verdictConfirmed, verdictNeedsReview, runID)
	if err != nil {
		return fmt.Errorf("store: cannot count detections per hour for run %s: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var n, answered, confirmed, review int64
		if err := rows.Scan(&raw, &n, &answered, &confirmed, &review); err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			continue
		}
		h, ok := byHour[hourKey(at)]
		if !ok {
			// A detection in an hour with no heartbeat. It happens when a run dies
			// between the two writes, and it is not silently dropped: the hour is
			// created with zero frames, which prints as the dead-stream case it is.
			h = &HourBeat{Hour: parseHourKey(hourKey(at))}
			byHour[hourKey(at)] = h
		}
		h.Proposed += int(n)
		h.Answered += int(answered)
		h.Confirmed += int(confirmed)
		h.NeedsReview += int(review)
	}
	return rows.Err()
}

// sortHours orders the map by its keys' times.
//
// It is an insertion sort over a handful of hours rather than sort.Slice over a
// slice built from map iteration, because a brief must be BYTE-STABLE: two runs over
// the same footage have to produce the same document, and Go's map iteration order
// would put the same hours in a different order every time.
func sortHours(keys []string, m map[string]*HourBeat) []HourBeat {
	out := make([]HourBeat, 0, len(m))
	for _, k := range keys {
		out = append(out, *m[k])
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Hour.Before(out[j-1].Hour); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func hourKey(at time.Time) string { return at.UTC().Format("2006-01-02T15") }
func parseHourKey(k string) time.Time {
	t, err := time.Parse("2006-01-02T15", k)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// BriefAnswer is one answered detection, with the clip it is about.
type BriefAnswer struct {
	At         time.Time
	Class      string
	Verdict    string
	Summary    string
	PromptID   string
	StorageRef string
	Score      float64
	// The box in NORMALISED 0..1 coordinates, because `detections` stores 0..1000
	// thousandths and a brief that printed those raw would show `194` where the
	// question was "where in the frame".
	X1, Y1, X2, Y2 float64
}

// BriefAnswers returns the most recent answered detections, newest last.
//
// It filters to `asked_answered` on purpose: a brief that included the DEGRADED rows
// would print their empty summaries, and a table with empty summaries reads as a
// model that declined to answer. It did not; it failed, and the hour table is where
// that is said.
func (s *Store) BriefAnswers(ctx context.Context, runID string, from, to time.Time, limit int) ([]BriefAnswer, error) {
	args := []any{runID, aistate.Asked}
	where := `WHERE d.run_id=? AND v.aistate=?`
	if !from.IsZero() {
		where += " AND d.at>=?"
		args = append(args, nowUTC(from))
	}
	if !to.IsZero() {
		where += " AND d.at<=?"
		args = append(args, nowUTC(to))
	}
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.at, d.class, v.verdict, v.summary, v.prompt_id,
		       COALESCE(e.storage_ref,''), d.score,
		       d.box_left, d.box_top, d.box_right, d.box_bottom
		FROM detections d
		JOIN vlm_observations v ON v.detection_id=d.id
		LEFT JOIN evidence e ON e.detection_id=d.id
		`+where+`
		ORDER BY d.at DESC, d.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read answers for run %s: %w", runID, err)
	}
	defer rows.Close()
	var out []BriefAnswer
	for rows.Next() {
		var a BriefAnswer
		var at string
		if err := rows.Scan(&at, &a.Class, &a.Verdict, &a.Summary, &a.PromptID, &a.StorageRef,
			&a.Score, &a.X1, &a.Y1, &a.X2, &a.Y2); err != nil {
			return nil, fmt.Errorf("store: cannot read a brief answer: %w", err)
		}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		// thousandths to normalised, clamped: a box of 0..1000 that overflows is a
		// stored value this code does not understand, and dividing it would put a
		// number on the map rather than admitting it.
		a.X1, a.Y1 = clamp01(a.X1/1000), clamp01(a.Y1/1000)
		a.X2, a.Y2 = clamp01(a.X2/1000), clamp01(a.Y2/1000)
		out = append(out, a)
	}
	return out, rows.Err()
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

var _ = strings.TrimSpace
