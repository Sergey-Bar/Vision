package store

// The search surface's additional capabilities: across runs and cameras, over a
// reviewer's own words, ordered by the model's own confidence, and the honest list of
// windows the run could not answer.
//
// Nothing here re-asks a model or re-derives an answer. Every number it returns is a
// column that was written when the decision was made, which is what makes a search
// result a claim with provenance rather than a caption.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
)

// searchColumns is the SELECT list shared by every search path.
//
// It is a constant and not a format string because the columns and the Scan targets
// have to stay in step, and a `fmt.Sprintf` that grows a column without growing the
// Scan is the kind of change that compiles and then reads the wrong field into the
// wrong variable. [scanHit] is the only reader.
const searchColumns = `
		d.id, d.at, d.class, v.summary, v.verdict, v.aistate, v.reason_code, v.prompt_id,
		v.model_identity, v.contract_version, v.temperature, v.output_hash,
		v.confidence,
		e.storage_ref, e.sha256, e.time_begin, e.time_end,
		r.id, r.label, r.source_arg,
		g.tag, g.verdict, g.by, g.note, g.at`

// The trailing newline is load-bearing. The constant is concatenated directly onto the
// WHERE clause by both callers, and a version without one produced
// `g.tag=d.tagWHERE d.run_id=...`, which SQLite reports as a syntax error near "d" --
// which names neither the missing separator nor the file that omitted it.
// The join to a reviewer's verdict goes through the OUTBOX, and that is not an
// indirection to be tidied away.
//
// `ground_truth` is keyed on (run_id, tag), and a tag is an XProtect event tag
// allocated when the detection was pushed -- it is not a detection id and there is no
// `detections.tag` column to join on. The outbox row is what carries the mapping from
// one to the other, and `MarkGroundTruth` already refuses a tag that is not in this
// run's outbox for the same reason: a mark nobody ever pushed is a mark whose subject
// cannot be found.
//
// So the LEFT JOIN is on the outbox, not on a column that does not exist. An earlier
// revision wrote `g.tag=d.tag` and SQLite reported "no such column: d.tag", which names
// the missing column and not the table that should have supplied it.
const searchFrom = `
	FROM detections d
	JOIN vlm_observations v ON v.detection_id=d.id
	JOIN runs r ON r.id=d.run_id
	LEFT JOIN evidence e ON e.detection_id=d.id
	LEFT JOIN outbox o ON o.detection_id=d.id
	LEFT JOIN ground_truth g ON g.run_id=d.run_id AND g.tag=o.tag
`

// scanHit reads one search row.
//
// Every nullable column goes through a `sql.Null*`, because a LEFT JOIN with no
// matching row is a normal outcome rather than an error and a card with no evidence
// still has to render.
func scanHit(rows *sql.Rows) (SearchHit, error) {
	var h SearchHit
	var at string
	var ref, sha sql.NullString
	// The time bounds are NULLABLE and not optional. `evidence` is a LEFT JOIN and a
	// decision can be stored without its clip -- the clip may have been rotated away by
	// the disk budget -- so "no clip" is a normal row rather than an error. Scanned
	// into a plain string they abort the whole search with "converting NULL to string
	// is unsupported", which loses every card in the window because one of them had
	// no evidence.
	var tb, te sql.NullString
	var runID, runLabel, sourceArg sql.NullString
	var gtTag, gtVerdict, gtBy, gtNote, gtAt sql.NullString
	if err := rows.Scan(&h.DetectionID, &at, &h.Class, &h.Summary, &h.Verdict, &h.AIState,
		&h.ReasonCode, &h.PromptID, &h.ModelIdentity, &h.ContractVersion, &h.Temperature,
		&h.OutputHash, &h.Confidence,
		&ref, &sha, &tb, &te,
		&runID, &runLabel, &sourceArg,
		&gtTag, &gtVerdict, &gtBy, &gtNote, &gtAt); err != nil {
		return h, err
	}
	h.At, _ = time.Parse(time.RFC3339Nano, at)
	h.StorageRef, h.SHA256 = ref.String, sha.String
	h.TimeBegin, _ = time.Parse(time.RFC3339Nano, tb.String)
	h.TimeEnd, _ = time.Parse(time.RFC3339Nano, te.String)
	h.RunID, h.RunLabel, h.SourceArg = runID.String, runLabel.String, sourceArg.String
	// The confidence flag is derived from the ANSWER, not from the number.
	// `vlm_observations.confidence` is NOT NULL DEFAULT 0, so a row nobody asked about
	// carries 0 and would render as "0% confirmed" if the number alone decided.
	h.HasConfidence = h.AIState == aistate.Asked
	if gtTag.Valid {
		h.HasGroundTruth = true
		h.GroundTruthTag = gtTag.String
		h.GroundTruth = gtVerdict.String
		h.GroundTruthBy = gtBy.String
		h.GroundTruthNote = gtNote.String
		h.GroundTruthAt, _ = time.Parse(time.RFC3339Nano, gtAt.String)
		h.MarkedFalsePositive = strings.EqualFold(strings.TrimSpace(gtVerdict.String),
			GroundTruthFalsePositive)
	}
	return h, nil
}

// searchWhere builds the shared predicates and their arguments.
//
// It returns the clause and the arguments ALREADY IN ORDER, because SQLite binds by
// position and an argument list ordered differently from the statement produces a
// perfectly plausible wrong answer rather than an error. That is the single most
// expensive mistake available in this file.
func searchWhere(q SearchQuery, scope []string) (string, []any) {
	args := make([]any, 0, len(scope)+8)
	where := "WHERE d.run_id IN (" + placeholders(len(scope)) + ")"
	for _, r := range scope {
		args = append(args, r)
	}
	if c := strings.TrimSpace(q.Class); c != "" {
		where += " AND d.class=?"
		args = append(args, c)
	}
	if v := strings.TrimSpace(q.Verdict); v != "" {
		where += " AND v.verdict=?"
		args = append(args, v)
	}
	if s := strings.TrimSpace(q.Source); s != "" {
		where += " AND r.source_arg LIKE ?"
		// The camera filter is a SUBSTRING of the source argument, which is a URL or a
		// file path, and it is matched with `%term%` so an operator can type a camera
		// name without knowing whether it is a host, a stream key or a filename.
		args = append(args, "%"+s+"%")
	}
	if !q.From.IsZero() {
		where += " AND d.at>=?"
		args = append(args, nowUTC(q.From))
	}
	if !q.To.IsZero() {
		where += " AND d.at<=?"
		args = append(args, nowUTC(q.To))
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		// The model's own words OR the reviewer's. Both, because an operator searching
		// for what a person concluded about a clip is searching the ground-truth note,
		// and a surface that only searched the model's sentence would make the
		// reviewer's judgement unretrievable.
		where += " AND (lower(v.summary) LIKE ? OR lower(g.note) LIKE ?)"
		needle := "%" + strings.ToLower(t) + "%"
		args = append(args, needle, needle)
	}
	return where, args
}

// searchOrder is the ORDER BY, and it is one of two things.
//
// Never a relevance score. With no similarity there is nothing to rank by meaning, and
// a relevance column computed from the text match would be a number this module cannot
// account for, which is the one thing a search surface must not invent.
func searchOrder(q SearchQuery) string {
	if strings.EqualFold(strings.TrimSpace(q.OrderBy), OrderConfidence) {
		// Confidence DESC, then time, then id. The tie-breakers are not decoration:
		// without them two cards with the same confidence can come back in either order
		// on two runs of the same query, and a search that reorders itself between two
		// identical requests cannot be cited.
		return " ORDER BY v.confidence DESC, d.at, d.id"
	}
	return " ORDER BY d.at, d.id"
}

// SearchCards is the hits query.
//
// The two predicates that narrow to real ANSWERS are HERE and not in the summary's, for
// the reason already stated on [Store.SearchObservations]: a `camera_integrity` answer
// is a sentence about the VIEW and a `degraded` row is a failure wearing a sentence's
// clothes, and the summary's whole job is to count the window INCLUDING what was never
// asked and what failed.
func (s *Store) SearchCards(ctx context.Context, q SearchQuery) ([]SearchHit, error) {
	scope := q.scope()
	if len(scope) == 0 {
		// No run in scope matches NOTHING, and says so by returning an empty result.
		//
		// This is the guard the old run-scope refusal used to be, restated as a
		// behaviour. An error here would tell an operator their camera filter is broken
		// when the honest answer is "nothing from that camera in that window", and
		// silently dropping the filter would return EVERY run's answers, which is the
		// specific failure the refusal existed to prevent.
		return nil, nil
	}
	where, args := searchWhere(q, scope)
	args = append(args, aistate.Asked, promptAlarm)
	where += " AND v.aistate=? AND v.prompt_id=? AND v.summary IS NOT NULL"
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+searchColumns+searchFrom+where+searchOrder(q)+`
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: cannot search cards: %w", err)
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		h, serr := scanHit(rows)
		if serr != nil {
			return nil, fmt.Errorf("store: cannot read a search row: %w", serr)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SearchCardCounts is how many cards a query matches, before Limit.
//
// It is a separate call rather than a field on the result because a LIMIT and a COUNT
// are answered by SQLite by different plans, and a surface that reports "showing 20 of
// 20" when there are four hundred is a surface that lies about its own size.
func (s *Store) SearchCardCounts(ctx context.Context, q SearchQuery) (int, error) {
	scope := q.scope()
	if len(scope) == 0 {
		return 0, nil
	}
	where, args := searchWhere(q, scope)
	args = append(args, aistate.Asked, promptAlarm)
	where += " AND v.aistate=? AND v.prompt_id=? AND v.summary IS NOT NULL"
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) `+searchFrom+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: cannot count search cards: %w", err)
	}
	return n, nil
}

// UnsearchableWindow is one contiguous stretch of the run that could not be answered.
//
// It is a WINDOW rather than a row because one unasked proposal is not the same fact as
// twenty. A run that missed one frame in seven days and a run that missed a minute at
// the same time every hour have the same count of unasked proposals and completely
// different stories, and the count is what a search surface prints.
type UnsearchableWindow struct {
	From      time.Time
	To        time.Time
	Proposals int
	NotAsked  int
	Degraded  int
	// Reason is the most common reason code in the window, or "none".
	//
	// It is the MOST COMMON and not the first, because a window whose first cause was a
	// one-off would be named after the one-off and the other nine minutes of quota
	// exhaustion would be invisible.
	Reason string
}

// Seconds is the window's length, which is the number an operator compares against a
// run's total.
func (w UnsearchableWindow) Seconds() float64 {
	if w.To.Before(w.From) {
		return 0
	}
	return w.To.Sub(w.From).Seconds()
}

// UnsearchableWindows lists the stretches of a run that could not be answered.
//
// This is the honest answer to scale and it is what a reviewer will ask about a system
// running without a cloud: not "how accurate were you" but "what did you not know, and
// when". The two questions are different and a surface that answers only the first has
// not answered anything.
//
// The windows are computed from DETECTIONS with no `asked_answered` observation, over a
// LEFT JOIN, because a query that started from the answer table could not count a
// proposal nobody made an answer for. That is not an optimisation; it is the only
// formulation that can see the gap.
func (s *Store) UnsearchableWindows(ctx context.Context, runID string, limit int) ([]UnsearchableWindow, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("store: a list of unsearchable windows needs a run; a window across runs " +
			"has no single run's clock to be contiguous on")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.at, v.aistate, COALESCE(v.reason_code,'')
		FROM detections d
		LEFT JOIN vlm_observations v ON v.detection_id=d.id
		WHERE d.run_id=? AND (v.detection_id IS NULL OR v.aistate<>?)
		ORDER BY d.at`, runID, aistate.Asked)
	if err != nil {
		return nil, fmt.Errorf("store: cannot list unsearchable windows for run %s: %w", runID, err)
	}
	defer rows.Close()

	var out []UnsearchableWindow
	reasonCount := map[string]int{}
	var cur *UnsearchableWindow
	for rows.Next() {
		var at string
		var state, reason sql.NullString
		if err := rows.Scan(&at, &state, &reason); err != nil {
			return nil, fmt.Errorf("store: cannot read an unsearchable row: %w", err)
		}
		ts, perr := time.Parse(time.RFC3339Nano, at)
		if perr != nil {
			return nil, fmt.Errorf("store: a detection instant %q is not a time: %w", at, perr)
		}
		// A window is broken by a gap of more than GAPSECONDS between proposals. The
		// value is the detection sampling interval times two, which is the largest gap
		// that is still "the next sample" rather than "a hole".
		const gapSeconds = 4
		if cur == nil || ts.Sub(cur.To).Seconds() > gapSeconds {
			if cur != nil {
				cur.Reason = mostCommon(reasonCount)
				out = append(out, *cur)
				reasonCount = map[string]int{}
			}
			cur = &UnsearchableWindow{From: ts, To: ts}
		}
		cur.To = ts
		cur.Proposals++
		if !state.Valid || state.String != aistate.Asked {
			cur.NotAsked++
		} else {
			cur.Degraded++
		}
		reasonCount[reason.String]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cur != nil {
		cur.Reason = mostCommon(reasonCount)
		out = append(out, *cur)
	}
	// The LONGEST first, because the question being asked is "what did you not know", and
	// the answer to that is where the hole was biggest.
	sortWindowsBySecondsDesc(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func mostCommon(counts map[string]int) string {
	best, bestN := "none", -1
	for k, n := range counts {
		// Ties break on the name, so the reason for a window does not change between two
		// runs of the same query.
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	if best == "" {
		return "none"
	}
	return best
}

// sortWindowsBySecondsDesc orders longest first, and EARLIEST first among equals.
//
// The tie-break is not decoration. Two windows of the same length can come back in
// either order on two runs of the same query, and a view whose own ordering changes
// between two identical requests cannot be quoted in a review. The sort is a
// hand-written insertion sort rather than `sort.Slice` because this repository does not
// import `sort` here for a five-element slice and a comparator closure that reads a
// method would be the part a reader has to evaluate.
func sortWindowsBySecondsDesc(ws []UnsearchableWindow) {
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0; j-- {
			a, b := ws[j-1], ws[j]
			if a.Seconds() > b.Seconds() || (a.Seconds() == b.Seconds() && a.From.Before(b.From)) {
				break
			}
			ws[j-1], ws[j] = ws[j], ws[j-1]
		}
	}
}

// The evidence actions a card may offer.
//
// They are a CLOSED SET and not a string on the card, because an action a reader can be
// shown and cannot be performed is worse than an action that is absent: it tells them
// the capability exists and then does not deliver it.
const (
	// ActionRecordedWindow offers the clip plus its exact bounds and hash.
	ActionRecordedWindow = "the recorded window"
	// ActionNone is what a card with no evidence row gets, which is a real state: a
	// decision can be stored without its clip.
	ActionNone = "no clip retained"
)

// CardAction is what this hit can offer an operator, by name.
func CardAction(h SearchHit) string {
	if h.StorageRef == "" || h.TimeBegin.IsZero() || h.TimeEnd.IsZero() {
		return ActionNone
	}
	return ActionRecordedWindow
}

// EvidenceWindow is the one-line evidence description for a hit: the recorded window,
// never "the full video file".
//
// The distinction is not pedantry. There is no continuous archive; the store holds cut
// clips with a sha256 and time bounds, so offering a file that will not be there is the
// exact failure this surface must not have. What an investigator actually needs is the
// window, so that they can request the rest from the VMS themselves.
func EvidenceWindow(h SearchHit) string {
	if CardAction(h) == ActionNone {
		return ActionNone
	}
	return fmt.Sprintf("%s [%s .. %s] sha256=%s",
		h.StorageRef,
		h.TimeBegin.UTC().Format(time.RFC3339),
		h.TimeEnd.UTC().Format(time.RFC3339),
		shortHash(h.SHA256))
}

// ProveCommand is the one-click reproduction: re-derive the decision from stored facts
// with zero provider calls.
//
// It is a string on the card and not a button that exists somewhere, because the whole
// point is that an operator can COPY it. The capability is worth nothing if the reader
// has to know the command by heart.
func ProveCommand(h SearchHit) string {
	return "pocctl prove " + h.DetectionID
}

// shortHash is eight characters, which is enough to compare two cards by eye and not
// enough to be mistaken for the whole hash. The whole hash is in the store and in
// `pocctl prove`.
func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
