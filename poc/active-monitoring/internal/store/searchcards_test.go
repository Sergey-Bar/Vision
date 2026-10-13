package store

// Phase 4b: the search surface's tests.
//
// Each of these protects one of the rules the surface claims. The interesting ones are
// the negative ones -- a search that matches too much, prints a number it did not
// measure, or reports a zero that means "not asked" rather than "nothing there" is the
// failure this whole repository exists to prevent, and none of them produces an error.
// They produce a sentence.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/aistate"
)

const (
	cardRunA = "run-a"
	cardRunB = "run-b"
)

// seedCardRun builds two runs over two cameras, each with answered detections at
// distinct stored confidences, so every cross-run, cross-camera and ordering rule has
// something to be wrong about.
func seedCardRun(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	for _, r := range []struct{ id, label, src string }{
		{cardRunA, "car park north", "rtsp://cam-north/live"},
		{cardRunB, "car park south", "rtsp://cam-south/live"},
	} {
		if err := s.StartRun(ctx, Run{
			ID: r.id, Label: r.label, Mode: "live", SourceKind: "rtsp", SourceArg: r.src,
			PushBaseURL: r.src, PushIsVMS: true, ClockSource: "host",
			StartedAt: base, TargetSeconds: 3600,
		}); err != nil {
			t.Fatalf("cannot start run %s: %v", r.id, err)
		}
	}

	rows := []struct {
		id, run, class string
		conf           float64
		offset         int
	}{
		{"d1", cardRunA, "car", 0.91, 0},
		{"d2", cardRunA, "car", 0.40, 10},
		{"d3", cardRunB, "bird", 0.73, 20},
		{"d4", cardRunB, "bird", 0.12, 30},
	}
	for _, r := range rows {
		putCardDetection(t, s, r.id, r.run, base.Add(time.Duration(r.offset)*time.Second),
			r.class, aistate.Asked)
		// The summary carries the CLASS in its own words, so a text search discriminates
		// between the two runs. A fixture where every row says the same sentence would
		// make every text assertion pass for the wrong reason: the count would be
		// "all the rows" rather than "the rows that matched".
		summary := "a vehicle is stopped at the barrier"
		if r.class == "bird" {
			summary = "a bird is on the path near the gate"
		}
		if err := s.PutObservation(ctx, Observation{
			DetectionID: r.id, RunID: r.run, PromptID: promptAlarm, PromptVersion: "v3",
			AIState: aistate.Asked, Verdict: "confirmed", Summary: summary,
			Confidence: r.conf, ModelIdentity: "hafnia-8b", ContractVersion: "alarm/v3",
			OutputHash: "hash-" + r.id, ReasonCode: "none", CreatedAt: base,
		}); err != nil {
			t.Fatalf("cannot put observation %s: %v", r.id, err)
		}
	}
	return s
}

func putCardDetection(t *testing.T, s *Store, id, run string, at time.Time, class, state string) {
	t.Helper()
	if err := s.Accept(context.Background(), Detection{
		ID: id, RunID: run, At: at, Class: class, TrackID: 1, Score: 0.8,
		BoxTop: 10, BoxLeft: 10, BoxBottom: 100, BoxRight: 100,
		Direction: "left-to-right", Status: "open", AIState: state,
		Provenance: ProvenanceReal,
	}); err != nil {
		t.Fatalf("cannot accept detection %s: %v", id, err)
	}
}

func cardsFor(t *testing.T, s *Store, q SearchQuery) []SearchHit {
	t.Helper()
	hits, err := s.SearchCards(context.Background(), q)
	if err != nil {
		t.Fatalf("SearchCards: %v", err)
	}
	return hits
}

// TestSearchAcrossRunsNamesTheRunOnEveryCard is 4b.5.1.
//
// Cross-run search is legitimate provided every card names its run. A run has its own
// model identity, its own contract version and its own source, so answers from two runs
// are not comparable unless both are visible -- the same reasoning that refuses to
// compare a v1 answer with a v2 answer refuses to compare two runs without their labels.
func TestSearchAcrossRunsNamesTheRunOnEveryCard(t *testing.T) {
	s := seedCardRun(t)
	hits := cardsFor(t, s, SearchQuery{RunIDs: []string{cardRunA, cardRunB}, Text: "", Class: "", Limit: 20})
	if len(hits) != 4 {
		t.Fatalf("a cross-run search returned %d card(s), want 4. Two answers from two runs are not "+
			"comparable unless both are visible", len(hits))
	}
	seen := map[string]bool{}
	for _, h := range hits {
		if h.RunID == "" || h.RunLabel == "" {
			t.Fatalf("a card carries no run: %+v", h)
		}
		if h.SourceArg == "" {
			t.Fatalf("a card carries no source: %+v", h)
		}
		seen[h.RunID] = true
	}
	if !seen[cardRunA] || !seen[cardRunB] {
		t.Fatalf("the cross-run search returned cards from %v, want both runs", seen)
	}
}

// TestACameraFilterMatchesNothingRatherThanEverythingWhenNoRunGiven is the guard the
// old run-scope refusal existed for, restated as a behaviour.
//
// A camera filter that silently matches every run is worse than no filter at all: it
// looks like it worked. So the scope has to be resolved BEFORE the filter and an empty
// scope has to produce an empty result rather than a full-table scan.
func TestACameraFilterMatchesNothingRatherThanEverythingWhenNoRunGiven(t *testing.T) {
	s := seedCardRun(t)
	q := SearchQuery{Source: "cam-north", Text: "vehicle", Limit: 20}
	// No RunID and no RunIDs, so the scope is empty.
	if hits := cardsFor(t, s, q); len(hits) != 0 {
		t.Fatalf("a camera filter with NO RUN in scope matched %d card(s). A filter that silently "+
			"widens is worse than no filter: it looks like it worked", len(hits))
	}
	n, err := s.SearchCardCounts(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the count query with no run in scope returned %d", n)
	}
	// And the same filter WITH a run narrows to that camera, which is the positive half
	// and the reason the negative half is worth a test.
	withRun := SearchQuery{RunID: cardRunA, Source: "cam-north", Text: "", Limit: 20}
	if got := len(cardsFor(t, s, withRun)); got != 2 {
		t.Fatalf("a camera filter within one run matched %d card(s), want the 2 from cam-north", got)
	}
}

// TestTheCameraIsFoundOnTheRunRowAndNotOnADetectionsColumn is the JOIN rather than a
// migration.
//
// `runs.source_arg` already holds the camera, so cross-camera search is a filter and not
// a schema change. This asserts which column it reads, because a version that added
// `detections.camera` would have introduced a second source of truth for one fact.
func TestTheCameraIsFoundOnTheRunRowAndNotOnADetectionsColumn(t *testing.T) {
	s := seedCardRun(t)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE id=? AND source_arg LIKE '%cam-north%'`,
		cardRunA).Scan(&n); err != nil {
		t.Fatalf("runs.source_arg is not readable: %v", err)
	}
	if n != 1 {
		t.Fatalf("runs.source_arg does not hold the camera for run %s", cardRunA)
	}
	// And it must NOT exist on detections, because one fact has one column.
	if _, err := s.db.Exec(`SELECT camera FROM detections LIMIT 1`); err == nil {
		t.Fatal("detections has a camera column. Two columns for one fact is a second source of truth, " +
			"and cross-camera search would read whichever one somebody remembered to write")
	}
}

// TestThePrintedNumberIsTheStoredConfidenceAndNotADerivedOne is 4b.1's control.
//
// The number on the card is `vlm_observations.confidence`, a stored column beside the
// model identity and the contract version. It recomputes from stored facts because it IS
// a stored fact. Anything else on that card -- a normalised score, a blend, a
// similarity -- would be a number this module cannot account for.
func TestThePrintedNumberIsTheStoredConfidenceAndNotADerivedOne(t *testing.T) {
	s := seedCardRun(t)
	hits := cardsFor(t, s, SearchQuery{RunID: cardRunA, Text: "", Limit: 20})
	if len(hits) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(hits))
	}
	byID := map[string]SearchHit{}
	for _, h := range hits {
		byID[h.DetectionID] = h
	}
	// The stored confidences were 0.91 and 0.40. The detection SCORE was 0.8 for both,
	// so a number derived from the wrong column would read 0.8 twice and look plausible.
	if got := byID["d1"].Confidence; got != 0.91 {
		t.Errorf("d1's card carries confidence %v, want the stored 0.91", got)
	}
	if got := byID["d2"].Confidence; got != 0.40 {
		t.Errorf("d2's card carries confidence %v, want the stored 0.40. The detection score was 0.8 "+
			"for both, so this is the number being read from the wrong table", got)
	}
	for _, h := range hits {
		if !h.HasConfidence {
			t.Errorf("%s carries no confidence flag on an answered row", h.DetectionID)
		}
	}
}

// TestACardForAnUnaskedProposalCarriesNoPercentage is 4b.1 rule 3.
//
// `confidence` is NOT NULL DEFAULT 0, so an unasked proposal carries 0 and the flag is
// what distinguishes "the model said zero" from "nobody asked". A card printing "0%
// confirmed" for a row nobody asked about would be a fabricated measurement, and it is
// the single easiest way for this surface to become the thing this repository exists
// not to be.
func TestACardForAnUnaskedProposalCarriesNoPercentage(t *testing.T) {
	s := seedCardRun(t)
	ctx := context.Background()
	putCardDetection(t, s, "d5", cardRunA,
		time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC), "car", aistate.NotAsked)

	for _, h := range cardsFor(t, s, SearchQuery{RunID: cardRunA, Text: "", Limit: 20}) {
		if h.DetectionID == "d5" {
			t.Fatal("an unasked proposal was returned as a card at all. The hits query narrows to " +
				"asked_answered, and an unasked row is not a finding")
		}
	}
	// And the SUMMARY does see it, which is the whole point of the split.
	_, sum, err := s.SearchObservations(ctx, SearchQuery{RunID: cardRunA, Text: "vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NotAsked != 1 {
		t.Fatalf("the summary counted %d not-asked proposals, want 1. A summary built from the answer "+
			"table cannot see a proposal nobody made an answer for", sum.NotAsked)
	}
	if !sum.Unsearchable() {
		t.Fatal("a window with an unasked proposal reports itself searchable")
	}
}

// TestConfidenceOrderingIsStoredAndNeverAComputedRelevance is 4b.5.3.
//
// Ordering is chronological by default, with an explicit switch to verdict confidence.
// There is no relevance score and there never will be: with no similarity there is
// nothing to rank by meaning.
func TestConfidenceOrderingIsStoredAndNeverAComputedRelevance(t *testing.T) {
	s := seedCardRun(t)
	both := []string{cardRunA, cardRunB}

	chronological := cardsFor(t, s, SearchQuery{RunIDs: both, Text: "", Limit: 20})
	if len(chronological) != 4 {
		t.Fatalf("expected 4 cards, got %d", len(chronological))
	}
	for i := 1; i < len(chronological); i++ {
		if chronological[i].At.Before(chronological[i-1].At) {
			t.Fatalf("the default ordering is not chronological: %v then %v",
				chronological[i-1].At, chronological[i].At)
		}
	}

	byConf := cardsFor(t, s, SearchQuery{RunIDs: both, Text: "", Limit: 20, OrderBy: OrderConfidence})
	if len(byConf) != len(chronological) {
		t.Fatalf("ordering changed the number of cards: %d then %d", len(chronological), len(byConf))
	}
	for i := 1; i < len(byConf); i++ {
		if byConf[i].Confidence > byConf[i-1].Confidence {
			t.Fatalf("confidence ordering is ascending: %v then %v", byConf[i-1].Confidence, byConf[i].Confidence)
		}
	}
	if byConf[0].Confidence != 0.91 {
		t.Fatalf("the highest-confidence card is %v, want the stored 0.91", byConf[0].Confidence)
	}
	// An unknown ordering falls back to chronological rather than to something invented.
	other := cardsFor(t, s, SearchQuery{RunIDs: both, Text: "", Limit: 20, OrderBy: "relevance"})
	if len(other) != len(chronological) {
		t.Fatalf("an unknown ordering returned %d cards, want %d", len(other), len(chronological))
	}
	if other[0].DetectionID != chronological[0].DetectionID {
		t.Fatal("an unknown ordering did not fall back to chronological. The CLI refuses it by name, and " +
			"the store must not invent a ranking for a caller that skipped the check")
	}
}

// TestAGroundTruthNoteIsFindable is 4b.5.2, and it is the highest value per line in the
// whole phase.
//
// It makes the reviewer's own judgement retrievable: an operator who wrote "that is the
// bird again, not a person" can find it by typing three words.
func TestAGroundTruthNoteIsFindable(t *testing.T) {
	s := seedCardRun(t)
	ctx := context.Background()
	// A mark needs the tag to be in this run's outbox, because a mark nobody pushed has a
	// subject that cannot be found. Enqueue first, then mark.
	if _, err := s.Enqueue(ctx, OutboxEntry{
		DetectionID: "d1", Kind: "event", Payload: "{}", Tag: "tag-car",
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}, 3); err != nil {
		t.Fatalf("cannot enqueue: %v", err)
	}
	if err := s.MarkGroundTruth(ctx, cardRunA, "tag-car", GroundTruthFalsePositive, "sergey",
		"the barrier shadow, not a stopped car"); err != nil {
		t.Fatalf("cannot mark: %v", err)
	}

	// Search by the REVIEWER's words and not the model's.
	hits := cardsFor(t, s, SearchQuery{RunID: cardRunA, Text: "barrier shadow", Limit: 20})
	if len(hits) != 1 {
		t.Fatalf("searching the reviewer's own note found %d card(s), want 1. The reviewer's judgement "+
			"has to be retrievable, or the column kept for it cannot be used", len(hits))
	}
	h := hits[0]
	if !h.HasGroundTruth {
		t.Fatal("the card does not say it was reviewed")
	}
	if h.GroundTruth != GroundTruthFalsePositive {
		t.Errorf("ground truth = %q, want %q", h.GroundTruth, GroundTruthFalsePositive)
	}
	if h.GroundTruthBy != "sergey" {
		t.Errorf("ground truth by = %q, want the reviewer's name. A verdict from a named person is "+
			"evidence and the same sentence from nobody is an opinion", h.GroundTruthBy)
	}
	if !h.MarkedFalsePositive {
		t.Error("a false_positive mark did not set the marker")
	}
	if h.GroundTruthNote == "" {
		t.Error("the card carries no note, so the thing that was searchable is not shown")
	}
	// And the MODEL's words still work, unchanged.
	if got := len(cardsFor(t, s, SearchQuery{RunID: cardRunA, Text: "vehicle is stopped", Limit: 20})); got != 2 {
		t.Fatalf("searching the model's sentence found %d card(s), want 2", got)
	}
}

// TestTheHighlightLengthComesFromConfigurationAndNotFromTheCard is 4b.3.
//
// The card never states a duration. It states the recorded window, which is what the
// store holds, and the length is whatever the recorder was configured to cut. A card
// saying "30 s highlight" would be claiming a file that does not exist: there is no
// continuous archive.
func TestTheHighlightLengthComesFromConfigurationAndNotFromTheCard(t *testing.T) {
	s := seedCardRun(t)
	if err := s.PutEvidence(context.Background(), Evidence{
		ID: "e1", DetectionID: "d1", SHA256: strings.Repeat("a", 64), StorageRef: "clips/d1.mp4",
		Bytes: 1024, TimeBegin: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		TimeEnd: time.Date(2026, 10, 1, 12, 0, 10, 0, time.UTC), ReadbackOK: true,
		CreatedAt: time.Date(2026, 10, 1, 12, 0, 10, 0, time.UTC),
	}); err != nil {
		t.Fatalf("cannot put evidence: %v", err)
	}
	hits := cardsFor(t, s, SearchQuery{RunID: cardRunA, Text: "", Limit: 20})
	var withClip *SearchHit
	for i := range hits {
		if hits[i].StorageRef != "" {
			withClip = &hits[i]
		}
	}
	if withClip == nil {
		t.Fatal("no card carries its evidence")
	}
	action := CardAction(*withClip)
	if action != ActionRecordedWindow {
		t.Fatalf("action = %q, want %q. Offering a file that will not be there is the exact failure "+
			"this surface must not have; what an investigator needs is the window, so they can request "+
			"the rest from the VMS themselves", action, ActionRecordedWindow)
	}
	window := EvidenceWindow(*withClip)
	if !strings.Contains(window, "clips/d1.mp4") || !strings.Contains(window, "sha256=") {
		t.Fatalf("the evidence window is %q, want the ref and the hash", window)
	}
	if strings.Contains(strings.ToLower(window), "second") ||
		strings.Contains(strings.ToLower(window), " s clip") {
		t.Fatalf("the evidence window states a duration: %q. The length is whatever the recorder was "+
			"configured to cut and the card does not know it", window)
	}
	// A card with no clip says so, and that is a real state: a decision can be stored
	// without its evidence.
	if got := CardAction(SearchHit{}); got != ActionNone {
		t.Errorf("a card with no evidence offers %q, want %q", got, ActionNone)
	}
}

// TestAZeroResultWindowSaysWhetherItWasSearched is 4b.4 rule 2.
//
// "No matches" and "nothing was asked" are different screens. A count of zero without
// the second is a negative answer that was never given.
func TestAZeroResultWindowSaysWhetherItWasSearched(t *testing.T) {
	s := seedCardRun(t)
	ctx := context.Background()
	_, sum, err := s.SearchObservations(ctx, SearchQuery{RunID: cardRunA, Text: "nothing matches this"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.HitsFound != 0 {
		t.Fatalf("HitsFound = %d", sum.HitsFound)
	}
	if sum.Answered != 2 {
		t.Fatalf("Answered = %d, want 2", sum.Answered)
	}
	// A fully-asked window supports "the model found nothing", so it IS searchable.
	if sum.Unsearchable() {
		t.Fatal("a fully-asked window reports itself unsearchable, which would refuse to let the " +
			"surface say the one sentence it CAN support")
	}

	// Add an unasked proposal and the same query becomes unsearchable.
	putCardDetection(t, s, "d6", cardRunA,
		time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC), "car", aistate.NotAsked)
	_, sum2, err := s.SearchObservations(ctx, SearchQuery{RunID: cardRunA, Text: "nothing matches this"})
	if err != nil {
		t.Fatal(err)
	}
	if !sum2.Unsearchable() {
		t.Fatal("a window with an unasked proposal reports itself searchable. The same empty result is " +
			"then printed as a negative answer about a stretch of the run nobody asked about")
	}
}

// TestTheUnsearchableWindowsAreStretchesAndNotCounts is 4b.5.5.
//
// One unasked proposal is not the same fact as twenty. A run that missed one frame in
// seven days and a run that missed a minute at the same time every hour have the same
// count and completely different stories, and the count is what a search surface prints.
func TestTheUnsearchableWindowsAreStretchesAndNotCounts(t *testing.T) {
	s := seedCardRun(t)
	base := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	// Two stretches: three unasked proposals close together, then two far away.
	ids := []struct {
		id     string
		offset int
	}{
		{"u1a", 0}, {"u1b", 1}, {"u1c", 2},
		{"u2a", 600}, {"u2b", 602},
	}
	for i, r := range ids {
		putCardDetection(t, s, r.id, cardRunA, base.Add(time.Duration(r.offset)*time.Second),
			"car", aistate.NotAsked)
		_ = i
	}
	ws, err := s.UnsearchableWindows(context.Background(), cardRunA, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d window(s), want 2. Five unasked proposals happened in two stretches ten "+
			"minutes apart, and one number would describe neither", len(ws))
	}
	// Longest first, and deterministic among equals: two windows of the same length must
	// come back in the same order on two runs of the same query, or the view cannot be
	// quoted in a review.
	if ws[0].Seconds() < ws[1].Seconds() {
		t.Fatalf("windows are not longest-first: %v then %v", ws[0].Seconds(), ws[1].Seconds())
	}
	if ws[0].Seconds() == ws[1].Seconds() && ws[0].From.After(ws[1].From) {
		t.Fatalf("two windows of equal length came back latest-first: %v then %v", ws[0].From, ws[1].From)
	}
	// The LONGER window is the first one, and this is the assertion that says the two
	// are genuinely different stretches rather than one split in half.
	if ws[0].Proposals != 3 || ws[1].Proposals != 2 {
		t.Fatalf("window proposal counts are %d and %d, want 3 and 2", ws[0].Proposals, ws[1].Proposals)
	}
	total := 0
	for _, w := range ws {
		total += w.NotAsked
	}
	if total != 5 {
		t.Fatalf("the windows account for %d unasked proposals, want 5", total)
	}
	// And it refuses with no run, because a window across runs has no single clock to be
	// contiguous on.
	if _, err := s.UnsearchableWindows(context.Background(), "", 10); err == nil {
		t.Fatal("a window list with no run was accepted. Contiguity is a property of ONE run's clock")
	}
}

// TestTheScopeIsASetAndAnExplicitRunWins is the arithmetic behind the cross-run search.
//
// Two overlapping sets are not a set anybody meant, so an explicit `RunID` wins over
// `RunIDs` rather than being unioned in.
func TestTheScopeIsASetAndAnExplicitRunWins(t *testing.T) {
	got := SearchQuery{RunID: "one", RunIDs: []string{"one", "two", "two", " three ", ""}}.scope()
	if len(got) != 1 || got[0] != "one" {
		t.Fatalf("scope = %v, want [one]: an explicit run wins over a list", got)
	}
	if got := (SearchQuery{RunIDs: []string{"a", "b", "b", " a ", ""}}).scope(); len(got) != 2 {
		t.Fatalf("scope = %v, want the deduplicated [a b]", got)
	}
	if got := (SearchQuery{}).scope(); len(got) != 0 {
		t.Fatalf("an empty query scoped to %v, want no runs at all", got)
	}
}

// TestPlaceholdersMatchTheArgumentCount is the binding-order guard.
//
// SQLite binds by position, so a placeholder count and an argument count that disagree
// produce a perfectly plausible wrong answer rather than an error.
func TestPlaceholdersMatchTheArgumentCount(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 8} {
		got := placeholders(n)
		if n == 0 {
			if got != "NULL" {
				t.Errorf("placeholders(0) = %q, want NULL: an empty IN list must match nothing, not "+
					"everything", got)
			}
			continue
		}
		if c := strings.Count(got, "?"); c != n {
			t.Errorf("placeholders(%d) = %q with %d placeholders", n, got, c)
		}
	}
}

// TestCardCountsAndTheLimitAnswerTheSameQuestion is the "showing 20 of 20" guard.
//
// A LIMIT and a COUNT are answered by different plans. A surface that reports the
// limit's length as the total lies about its own size.
func TestCardCountsAndTheLimitAnswerTheSameQuestion(t *testing.T) {
	s := seedCardRun(t)
	ctx := context.Background()
	q := SearchQuery{RunIDs: []string{cardRunA, cardRunB}, Text: "", Limit: 1}
	hits, err := s.SearchCards(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	total, err := s.SearchCardCounts(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("the limit returned %d cards, want 1", len(hits))
	}
	if total != 4 {
		t.Fatalf("the count returned %d, want 4. A surface that reports %d as the total while showing "+
			"%d is a surface that lies about its own size", total, len(hits), total)
	}
}

// TestProveCommandIsOnEveryCard is 4b.4 rule 5.
//
// The capability is worth nothing if the reader has to know the command by heart, so it
// is a string on the card rather than a button that exists somewhere.
func TestProveCommandIsOnEveryCard(t *testing.T) {
	if got := ProveCommand(SearchHit{DetectionID: "d7"}); got != "pocctl prove d7" {
		t.Fatalf("ProveCommand = %q", got)
	}
}
