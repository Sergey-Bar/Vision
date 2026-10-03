package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/aistate"
)

// SearchHit is one answer, with the clip it is about and the attribution that
// makes it a claim rather than a caption.
//
// Every field here is something a reader could quote and be wrong about, which is
// why they are all carried together: a summary without its evidence ref and its
// output hash is a caption, and a caption is exactly the thing invariant 3 exists to
// stop looking like a measurement.
type SearchHit struct {
	DetectionID string
	At          time.Time
	Class       string
	// Summary is the model's own sentence. It is never paraphrased, because a
	// paraphrase of a model's sentence is a sentence this repository invented.
	Summary string
	// Verdict is the closed-set verdict, and AIState is the state the answer was
	// obtained in. They travel together because a verdict printed without its state
	// claims the answer was obtained cleanly.
	Verdict    string
	AIState    string
	ReasonCode string
	PromptID   string
	// ModelIdentity is the provider's CLAIM. There is no weight hash for a hosted
	// model and this repository does not invent one.
	ModelIdentity   string
	ContractVersion string
	Temperature     float64
	OutputHash      string
	// StorageRef, SHA256 and the time bounds are the evidence. A finding a person
	// cannot pull the clip for is not a finding they can adjudicate, and AC6 is
	// adjudicated by a person.
	StorageRef string
	SHA256     string
	TimeBegin  time.Time
	TimeEnd    time.Time

	// Confidence is the model's OWN confidence in the verdict it gave about this
	// clip, and it is a STORED COLUMN: `vlm_observations.confidence`, beside the
	// model identity, the contract version and the output hash. It recomputes from
	// stored facts because it IS a stored fact.
	//
	// It is not a similarity and must never render as one. There are no embeddings
	// here and that is a decision rather than an omission; what this number is is the
	// model's confidence in its own answer, which is a different claim with a different
	// failure mode.
	//
	// HasConfidence is separate because `confidence` is `NOT NULL DEFAULT 0` and a
	// model may legitimately answer 0. Treating 0 as "no number" would print a
	// fabricated measurement for a model that said zero, and treating a missing answer
	// as 0 would print a percentage for a row nobody asked. So the two are different
	// facts and one flag carries the difference.
	Confidence    float64
	HasConfidence bool

	// RunID, RunLabel and SourceArg name WHICH RUN answered. They are carried on
	// every card because a cross-run search is legitimate and an answer from two runs
	// is not comparable unless both are visible: a run has its own model identity, its
	// own contract version and its own source, and the same reasoning that refuses to
	// compare a v1 answer with a v2 answer refuses to compare two runs without their
	// labels.
	RunID     string
	RunLabel  string
	SourceArg string

	// GroundTruth is what a human concluded about this tag, once one has reviewed it.
	//
	// It is carried on the card rather than in a separate screen because the search
	// result is where a reviewer looks, and a verdict the reviewer already overturned
	// that the card does not show is a verdict the next reader will re-litigate. `By`
	// carries who concluded it, because "this is wrong" from a named person is evidence
	// and the same sentence from nobody is an opinion.
	GroundTruth         string
	GroundTruthTag      string
	GroundTruthBy       string
	GroundTruthNote     string
	GroundTruthAt       time.Time
	HasGroundTruth      bool
	MarkedFalsePositive bool
}

// SearchQuery is what a person asked for. Every field is optional except Text and
// RunID, and the optional ones narrow rather than filter out a window.
//
// There is no Camera field and there is deliberately no way to add one: this
// module records ONE camera per run, in `runs.push_base_url` and the run's
// configuration, and `detections` has no camera column to filter on. A search
// filter for a camera would be a flag that silently matches everything, which is
// worse than no flag.
type SearchQuery struct {
	// RunID is the one run, which is the common case and what every existing caller
	// uses.
	RunID string
	// RunIDs is a SET of runs, and a search across runs is legitimate because every
	// card names its run. It used to be refused outright, with the reasoning that
	// "searching across runs would answer a question about no particular run" — which
	// was true and stopped being true the moment every card carried its run's label,
	// model identity and contract version. The refusal was protecting a gap that has
	// been closed, and leaving it in place would have been a guard outliving its
	// reason.
	//
	// `RunIDs` wins when it is non-empty, so a caller that sets both gets the set and
	// not the union, and a union of two overlapping sets is not a set anybody meant.
	RunIDs []string
	// Source filters on the camera, which is `runs.source_arg` one level up. There is
	// no camera column on `detections` and none is added: the JOIN finds it, so this is
	// a filter and not a migration.
	//
	// A source with no run in scope matches NOTHING. That is the whole reason the
	// original run-scope refusal existed, restated as a behaviour rather than an error:
	// a camera filter that silently matched everything is worse than no filter at all,
	// and a filter that returns an empty result says "nothing from this camera in this
	// window" while one that returns everything says nothing at all.
	Source string
	// Text is matched against the model's summary AND the reviewer's ground-truth note,
	// case-insensitively. Both, because an operator searching "the person who looked at
	// this said it was a shadow" is looking for the reviewer's words and not the
	// model's, and that is the query with the highest value per line in this whole
	// surface.
	Text    string
	Class   string
	Verdict string
	From    time.Time
	To      time.Time
	Limit   int
	// OrderBy is "at" for chronological, which is the DEFAULT, or "confidence" for
	// verdict confidence, highest first.
	//
	// There is no relevance ordering and there never will be. With no similarity there
	// is nothing to rank by meaning, and a "relevance" column computed from the text
	// match would be a number this module cannot account for, which is the one thing a
	// search surface must not invent.
	OrderBy string
}

// Search orders.
const (
	// OrderAt is chronological, the default.
	OrderAt = "at"
	// OrderConfidence is verdict confidence, highest first.
	OrderConfidence = "confidence"
)

// scope is the resolved set of runs a query is over.
//
// An explicit `RunID` WINS over `RunIDs` rather than being unioned in. A caller who
// named one run and then a list did one of the two on purpose, and a union of two
// overlapping sets is not a set anybody meant.
func (q SearchQuery) scope() []string {
	if r := strings.TrimSpace(q.RunID); r != "" {
		return []string{r}
	}
	if len(q.RunIDs) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range q.RunIDs {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// placeholders is `?,?,?` for an IN list of n runs.
//
// It is built rather than written out because the argument count and the placeholder
// count have to agree and a hand-written pair is where they stop agreeing.
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// SearchSummary is what the window looked like, INCLUDING everything the search did
// not return.
//
// It is a separate return value and not something derived from the hits on purpose.
// A list of hits cannot describe a window: eleven hits and zero hits are the same
// list when eleven proposals were never asked, and the caller that only receives the
// list will print "nothing found" for both.
type SearchSummary struct {
	// Proposed is how many detections the window contains. Every other field except
	// Answered partitions it, and Answered counts a subset of the observations.
	Proposed int
	// Answered is how many of them the model was actually asked about and got an
	// answer for. It is the number a reader wants and the one most often missing,
	// because a run with no key at all still produces detections.
	Answered int
	// NoObjects is asked and answered, with no object present in the frame. That IS
	// a negative answer and it is counted as one.
	NoObjects int
	// Degraded is asked and failed: late, malformed, out of budget, refused. The
	// question reached the model and did not come back as an answer.
	Degraded int
	// NotAsked is never sent. The detector proposed it and the verifier never saw
	// it.
	NotAsked int
	// CannotTell is asked and answered with insufficient evidence, or refused the
	// closed-set verdict. This is a POSITIVE answer saying the frame did not settle
	// it, and it is different from all three above.
	CannotTell int
	// HitsFound is how many answers matched Text, before Limit was applied.
	HitsFound int
}

// Unsearchable is whether this window can support the sentence "the model found
// nothing about X".
//
// It is not `NoObjects + NotAsked`. A `no_objects` answer IS the model reporting
// nothing, so a window full of those supports the sentence. A window with an
// unasked proposal does not, and neither does one whose answer was lost.
func (s SearchSummary) Unsearchable() bool { return s.NotAsked > 0 || s.Degraded > 0 }

// SearchObservations searches what the model SAID, and reports what the window was.
//
// It is one function returning two values rather than a list and a count because the
// two are used together or the surface lies. Everything it reads is a STORED field:
// the summary, the verdict, the model identity, the contract version, the output hash
// and the evidence ref are all columns, and nothing here re-asks a model or
// re-derives an answer.
func (s *Store) SearchObservations(ctx context.Context, q SearchQuery) ([]SearchHit, SearchSummary, error) {
	var sum SearchSummary
	if strings.TrimSpace(q.RunID) == "" {
		return nil, sum, fmt.Errorf("store: a search needs a run; every stored answer is a column beside a run id and searching across runs would answer a question about no particular run")
	}
	if err := s.searchSummary(ctx, q, &sum); err != nil {
		return nil, sum, err
	}
	if sum.HitsFound == 0 {
		return nil, sum, nil
	}

	// The two predicates are in the HITS query and not in the summary's: a
	// `camera_integrity` answer is a sentence about the VIEW, and a `degraded` row is
	// a failure wearing a sentence's clothes. Neither is what an operator is
	// searching for when they type "what did the model say about this car", so
	// neither is returned as a match.
	//
	// The summary deliberately does NOT apply them, because the summary's whole job is
	// to count the window including what was never asked and what failed. A summary that
	// filtered to `asked_answered` would report a window of nine proposals as a window
	// of one, and "the model found nothing" would be true and useless. Same rows, two
	// different questions, and only one of them filters.
	args := []any{q.RunID, aistate.Asked, promptAlarm, "%" + strings.ToLower(q.Text) + "%"}
	where := `WHERE d.run_id=? AND v.aistate=? AND v.prompt_id=? AND v.summary IS NOT NULL AND lower(v.summary) LIKE ?`
	for _, f := range []struct {
		clause string
		value  string
	}{
		{"AND d.class=?", strings.TrimSpace(q.Class)},
		{"AND v.verdict=?", strings.TrimSpace(q.Verdict)},
	} {
		if f.value != "" {
			where += " " + f.clause
			args = append(args, f.value)
		}
	}
	if !q.From.IsZero() {
		where += " AND d.at>=?"
		args = append(args, nowUTC(q.From))
	}
	if !q.To.IsZero() {
		where += " AND d.at<=?"
		args = append(args, nowUTC(q.To))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.at, d.class, v.summary, v.verdict, v.aistate, v.reason_code, v.prompt_id,
		       v.model_identity, v.contract_version, v.temperature, v.output_hash,
		       e.storage_ref, e.sha256, e.time_begin, e.time_end
		FROM detections d
		JOIN vlm_observations v ON v.detection_id=d.id
		LEFT JOIN evidence e ON e.detection_id=d.id
		`+where+`
		ORDER BY d.at, d.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, sum, fmt.Errorf("store: cannot search observations for run %s: %w", q.RunID, err)
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var at string
		var ref, sha sql.NullString
		// The time bounds are NULLABLE, because `evidence` is a LEFT JOIN and a
		// decision can be stored without its clip. Scanning them into plain strings
		// aborts the entire search with "converting NULL to string is unsupported",
		// which loses every card in the window because ONE of them had no evidence --
		// the opposite of what a search surface should do about a missing clip.
		var tb, te sql.NullString
		if err := rows.Scan(&h.DetectionID, &at, &h.Class, &h.Summary, &h.Verdict, &h.AIState,
			&h.ReasonCode, &h.PromptID, &h.ModelIdentity, &h.ContractVersion, &h.Temperature,
			&h.OutputHash, &ref, &sha, &tb, &te); err != nil {
			return nil, sum, fmt.Errorf("store: cannot read a search row: %w", err)
		}
		h.At, _ = time.Parse(time.RFC3339Nano, at)
		h.StorageRef, h.SHA256 = ref.String, sha.String
		h.TimeBegin, _ = time.Parse(time.RFC3339Nano, tb.String)
		h.TimeEnd, _ = time.Parse(time.RFC3339Nano, te.String)
		// The confidence flag is the same derivation as the cards query's: derived
		// from the ANSWER, never from the number, because the column is NOT NULL
		// DEFAULT 0 and a row nobody asked about carries 0.
		h.HasConfidence = h.AIState == aistate.Asked
		out = append(out, h)
	}
	return out, sum, rows.Err()
}

// searchSummary partitions the window.
//
// The partition is over DETECTIONS and not over observations, because a detection
// with no observation row is exactly the `not_asked` case and a query that started
// from vlm_observations could not count it. A summary built from the answer table
// alone is structurally incapable of noticing that the model was never asked, and
// that is the whole bug this surface has to not have.
func (s *Store) searchSummary(ctx context.Context, q SearchQuery, sum *SearchSummary) error {
	args := []any{q.RunID}
	where := `WHERE d.run_id=?`
	if !q.From.IsZero() {
		where += " AND d.at>=?"
		args = append(args, nowUTC(q.From))
	}
	if !q.To.IsZero() {
		where += " AND d.at<=?"
		args = append(args, nowUTC(q.To))
	}

	// COALESCE on every SUM: over a LEFT JOIN with no rows at all, SUM is NULL and a
	// NULL int is a crash. A crash on "the window is empty" would be an absurd way
	// to say nothing, and it is exactly what happened the first time.
	//
	// The three placeholders in the SELECT list come BEFORE the ones in `where`, so
	// the arguments are in that order: SQLite binds by position in the statement's
	// text, and a list ordered by where-clause-then-select binds the state names to
	// the run id and returns a perfectly plausible zero.
	err := s.db.QueryRowContext(ctx, `
		SELECT
		  COUNT(*),
		  COALESCE(SUM(CASE WHEN v.detection_id IS NOT NULL THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN v.aistate=? AND v.verdict=? THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN v.detection_id IS NOT NULL AND v.aistate<>? THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN v.detection_id IS NULL THEN 1 ELSE 0 END),0)
		FROM detections d LEFT JOIN vlm_observations v ON v.detection_id=d.id
		`+where,
		append([]any{aistate.Asked, verdictNoObjects, aistate.Asked}, args...)...).
		Scan(&sum.Proposed, &sum.Answered, &sum.NoObjects, &sum.Degraded, &sum.NotAsked)
	if err != nil {
		return fmt.Errorf("store: cannot summarise the window for run %s: %w", q.RunID, err)
	}
	for _, dst := range []*int{&sum.Answered, &sum.NoObjects, &sum.Degraded, &sum.NotAsked} {
		if *dst < 0 {
			*dst = 0
		}
	}

	// `cannot_tell` is the residue: asked, and not `no_objects`. It is derived rather
	// than matched so a new AIState meaning "the model could not settle it" lands in
	// the right bucket without a second edit — and so `needs_review`, which IS a
	// positive claim that the frame did not settle it, is never counted as a
	// confirmation.
	sum.CannotTell = sum.Answered - sum.NoObjects
	if sum.CannotTell < 0 {
		sum.CannotTell = 0
	}

	if strings.TrimSpace(q.Text) == "" {
		sum.HitsFound = sum.Answered
		return nil
	}
	sum.HitsFound = s.countMatching(ctx, q)
	return nil
}

// countMatching must apply the SAME predicates as the hits query, or `HitsFound`
// and `len(hits)` are answers to two different questions and `--limit` prints a
// number that is wrong in a way nobody can see.
func (s *Store) countMatching(ctx context.Context, q SearchQuery) int {
	args := []any{q.RunID, aistate.Asked, promptAlarm, "%" + strings.ToLower(q.Text) + "%"}
	where := `WHERE d.run_id=? AND v.aistate=? AND v.prompt_id=? AND v.summary IS NOT NULL AND lower(v.summary) LIKE ?`
	if c := strings.TrimSpace(q.Class); c != "" {
		where += " AND d.class=?"
		args = append(args, c)
	}
	if v := strings.TrimSpace(q.Verdict); v != "" {
		where += " AND v.verdict=?"
		args = append(args, v)
	}
	if !q.From.IsZero() {
		where += " AND d.at>=?"
		args = append(args, nowUTC(q.From))
	}
	if !q.To.IsZero() {
		where += " AND d.at<=?"
		args = append(args, nowUTC(q.To))
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM detections d JOIN vlm_observations v ON v.detection_id=d.id `+where, args...).Scan(&n); err != nil {
		return 0
	}
	return n
}
