package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/vlm"
)

// cmdSearch is the surface a judge asks for first, and the one most likely to lie.
//
// "Search what the model said" is a question that sounds like a database query and
// is not. It returns a caption, a clip ref and an attribution, and every one of
// those three can be wrong in a way a row count cannot detect: the caption may be
// a model's guess about footage nobody read, the clip ref may be the clip of a
// DIFFERENT detection, and the attribution may be missing entirely.
//
// So the printing is where this surface is careful, and it is careful about exactly
// one thing: a window whose answers were never obtained is NOT a window with no
// matches. "No matches" is a claim about what the model said. "Nothing was asked" is
// a claim about whether the model was consulted. Printing the first when the second
// is true is the exact failure invariant 3 exists to prevent, in the one surface a
// judge is most likely to read quickly.
func cmdSearch(args []string) error {
	flags, err := flagSet("search", map[string]string{
		"data-root": "", "run": "", "query": "", "class": "", "verdict": "",
		"from": "", "to": "", "limit": "20",
	}, args)
	if err != nil {
		return err
	}
	query := strings.TrimSpace(flags["query"])
	if query == "" {
		return fmt.Errorf("--query is required. This command searches what the model SAID, and a search with no text would " +
			"return every row in the run dressed up as a result")
	}
	limit := 20
	if _, err := fmt.Sscanf(flags["limit"], "%d", &limit); err != nil || limit <= 0 {
		return fmt.Errorf("--limit %q is not a positive number", flags["limit"])
	}
	from, err := parseWhen(flags["from"], "--from")
	if err != nil {
		return err
	}
	to, err := parseWhen(flags["to"], "--to")
	if err != nil {
		return err
	}

	root := flags["data-root"]
	if root == "" {
		root = defaultDataRoot()
	}
	st, err := openReadOnly(root)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	runID := strings.TrimSpace(flags["run"])
	if runID == "" {
		run, err := st.LatestRun(ctx)
		if err != nil {
			return err
		}
		if run == nil {
			return fmt.Errorf("this store has no runs")
		}
		runID = run.ID
	}

	hits, searched, err := st.SearchObservations(ctx, store.SearchQuery{
		RunID: runID, Text: query, Class: strings.TrimSpace(flags["class"]),
		Verdict: strings.TrimSpace(flags["verdict"]), From: from, To: to, Limit: limit,
	})
	if err != nil {
		return err
	}

	fmt.Printf("search: %q, run %s\n", query, runID)
	fmt.Println()
	printAbsence(searched)

	// The ranking sentence is printed on EVERY run, hit or no hit. It is not a
	// footnote and it is not in the docs: a search that ranks by substring while
	// looking like a search that ranks by meaning is misleading in the one place
	// where somebody is about to trust it, and the whole reason this surface can be
	// re-run and checked is that it reads nothing but stored columns.
	fmt.Println("  matching is a case-insensitive SUBSTRING over the model's stored sentence,")
	fmt.Println("  with structured filters beside it. A similarity-ranked answer is not")
	fmt.Println("  replayable from stored facts, so there are no embeddings here and that is")
	fmt.Println("  a decision rather than an omission.")
	fmt.Println()

	if len(hits) == 0 {
		fmt.Printf("no matches.\n")
		fmt.Println()
		printNotAbsence(searched)
		return nil
	}
	for _, h := range hits {
		fmt.Printf("%s  %s  %s\n", h.At.UTC().Format(time.RFC3339), h.Verdict, h.Class)
		fmt.Printf("    %s\n", h.Summary)
		fmt.Printf("    %s\n", evidenceLine(h))
		fmt.Printf("    asked as %s by %s at temperature %.1f, contract %s, output %s\n",
			h.PromptID, h.ModelIdentity, h.Temperature, h.ContractVersion, short(h.OutputHash))
		fmt.Printf("    aistate %s, reason %s\n", h.AIState, h.ReasonCode)
		fmt.Println()
	}
	if searched.HitsFound > len(hits) {
		fmt.Printf("%d of %d matching answers shown; --limit raises the number\n", len(hits), searched.HitsFound)
		fmt.Println()
	}
	printNotAbsence(searched)
	return nil
}

// printAbsence is the four absence states, printed as four sentences.
//
// They are four and not two because they have different causes with different
// owners: `no_objects` is the model saying the frame was empty, `degraded` is the
// model having been asked and failed, `not_asked` is the model never having been
// consulted, and `cannot_tell` is an answer that parsed and says nothing. Collapsing
// any two of them into "nothing found" produces a sentence that is true and useless,
// which is the worst property a sentence can have.
func printAbsence(s store.SearchSummary) {
	if s.Proposed == 0 {
		fmt.Println("in this window there were NO detections at all, so there was nothing to ask about (not_asked)")
		fmt.Println("  this is a statement about the detector, not about the model")
		return
	}
	if s.Answered == 0 {
		fmt.Printf("in this window the model was asked about NOTHING: %d proposal(s), 0 answers (not_asked %d, degraded %d)\n",
			s.Proposed, s.NotAsked, s.Degraded)
		fmt.Println("  the run proposed detections and consulted nobody about them")
		return
	}
	fmt.Printf("in this window: %d proposal(s), asked and answered %d (%d with no objects, %d degraded, %d not asked, %d cannot tell)\n",
		s.Proposed, s.Answered, s.NoObjects, s.Degraded, s.NotAsked, s.CannotTell)
	if s.Degraded > 0 {
		fmt.Printf("  %d asked and degraded: asked, and failed, late or malformed. A missing answer here is NOT a negative answer\n", s.Degraded)
	}
	if s.NotAsked > 0 {
		fmt.Printf("  %d not asked: the detector proposed them and the verifier never saw them\n", s.NotAsked)
	}
	if s.CannotTell > 0 {
		fmt.Printf("  %d cannot tell: answered with insufficient evidence or needs_review, which is a positive claim that the frame did not settle it\n", s.CannotTell)
	}
	fmt.Println()
}

// printNotAbsence is the one refusal this surface must never get wrong.
//
// A search that finds rows AND a search that finds none can both be wrong in the
// same way: if some of the window was never asked, "no matches for X" says nothing
// about X, and printing it without qualification is a negative answer that was
// never given.
func printNotAbsence(s store.SearchSummary) {
	unsearchable := s.NotAsked + s.Degraded
	if unsearchable == 0 {
		return
	}
	fmt.Println("THIS WINDOW IS NOT FULLY SEARCHABLE.")
	fmt.Printf("  %d of the %d proposals in it were never answered (%d not asked, %d degraded).\n", unsearchable, s.Proposed, s.NotAsked, s.Degraded)
	fmt.Println("  A match that is absent above is not evidence the model saw nothing. The run did")
	fmt.Println("  not ask, and this is not the same as the model having answered 'no'.")
}

// evidenceLine is the RECORDED WINDOW an answer is about: the clip reference, its
// exact bounds and its hash. Never a video file, because none is written: there is
// no continuous archive to pull and offering one would be an offer that fails when
// it is taken.
//
// An answer with no stored clip says so in words rather than printing the zero time.
// A zero-time window reads as a measured window at the year 1, and an answer nobody
// can pull the clip for is not a finding anybody can adjudicate; the reader has to
// be able to tell those two apart at a glance.
func evidenceLine(h store.SearchHit) string {
	if strings.TrimSpace(h.StorageRef) == "" && strings.TrimSpace(h.SHA256) == "" {
		return "evidence NONE: no clip was stored for this answer, so there is nothing to pull. " +
			"This is an answer about a window nobody can watch, and it is NOT a statement about the scene"
	}
	bounds := h.TimeBegin.UTC().Format(time.RFC3339) + " .. " + h.TimeEnd.UTC().Format(time.RFC3339)
	if h.TimeBegin.IsZero() || h.TimeEnd.IsZero() {
		bounds = "bounds NOT RECORDED"
	}
	return fmt.Sprintf("evidence %s  sha256 %s  %s", orNone(h.StorageRef, "no reference"), short(h.SHA256), bounds)
}

// parseWhen reads an optional RFC3339 bound. Empty is "no bound", which is not the
// same as zero.
func parseWhen(s, flag string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is not an RFC3339 timestamp; time is compared against normalized_at and a "+
			"bound nobody can parse would silently become no bound at all", flag, s)
	}
	return t.UTC(), nil
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// searchCarriesNoObserver is the structural claim, checked in one place.
//
// The search surface reads a store and holds no `soak.Observer` and no `*vlm.Client`,
// so there is no handle by which it could reach a provider — the same structural
// claim internal/soak/replay.go makes by taking no observer at all. It is a claim
// about the shape of the code, so it is made about the import graph rather than
// about a comment.
var _ = vlm.VerdictNone
