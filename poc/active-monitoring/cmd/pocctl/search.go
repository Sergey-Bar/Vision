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
// "Search what the model said" is a question that sounds like a database query and is
// not. It returns a caption, a clip ref and an attribution, and every one of those three
// can be wrong in a way a row count cannot detect: the caption may be a model's guess
// about footage nobody read, the clip ref may be the clip of a DIFFERENT detection, and
// the attribution may be missing entirely.
//
// So the printing is where this surface is careful, and it is careful about exactly one
// thing: a window whose answers were never obtained is NOT a window with no matches.
// "No matches" is a claim about what the model said. "Nothing was asked" is a claim
// about whether the model was consulted. Printing the first when the second is true is
// the exact failure invariant 3 exists to prevent, in the one surface a judge is most
// likely to read quickly.
//
// What goes on the card is [store.SearchHit.Confidence], the model's OWN confidence in
// the verdict it gave, and it renders as "87% - confirmed" with the VERDICT as the noun.
// It never renders as "87% match", because a number beside a verdict reads as what it
// is and a number alone reads as a similarity, which this surface has no embeddings to
// produce and would be lying if it printed one.
//
// Four rules the card obeys, and they are the feature rather than the formatting:
//
//  1. A card with NO ANSWER shows NO NUMBER. `not_asked`, `degraded` and `cannot_tell`
//     render as their absence word and a reason. A percentage there would be a
//     fabricated measurement.
//  2. The card carries its provenance inline: model identity, contract version and a
//     short output hash. That is the differentiator and it costs three columns.
//  3. Confidence never animates toward a value. These are stored facts; they render at
//     their value.
//  4. Every card is one copy-paste from `pocctl prove`, which recomputes the decision
//     from stored facts with zero provider calls.
func cmdSearch(args []string) error {
	flags, err := flagSet("search", map[string]string{
		"data-root": "", "run": "", "runs": "", "camera": "", "query": "",
		"class": "", "verdict": "", "from": "", "to": "", "limit": "20",
		"order": string(store.OrderAt), "unsearchable": boolSpec,
	}, args)
	if err != nil {
		return err
	}
	// `--unsearchable` is a SPELLING of `pocctl unsearchable`, and both work.
	//
	// Two spellings of one command is usually a defect, and it is here for one reason: a
	// reader who has just run a search and been told the window is not fully searchable
	// will look for a way to SEE that, in the same place, rather than learn a second
	// command name. The flag keeps the discovery path short and the standalone command
	// keeps the thing scriptable on its own.
	if flags["unsearchable"] == "true" {
		return cmdUnsearchable(unsearchableArgs(flags))
	}
	query := strings.TrimSpace(flags["query"])
	if query == "" {
		return fmt.Errorf("--query is required. This command searches what the model SAID and what a REVIEWER " +
			"concluded, and a search with no text would return every row in the run dressed up as a result")
	}
	limit := 20
	if _, err := fmt.Sscanf(flags["limit"], "%d", &limit); err != nil || limit <= 0 {
		return fmt.Errorf("--limit %q is not a positive number", flags["limit"])
	}
	order := strings.TrimSpace(flags["order"])
	switch order {
	case store.OrderAt, store.OrderConfidence, "":
	default:
		return fmt.Errorf("--order %q is not an ordering. It is %q for chronological or %q for the model's "+
			"own verdict confidence. There is no relevance ordering and there will not be one: with no "+
			"embeddings there is nothing to rank by meaning, and a computed relevance score would be a "+
			"number this build cannot account for", order, store.OrderAt, store.OrderConfidence)
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

	// The scope is a SET of runs. `--run` names one, `--runs` names several, and a
	// camera filter with no run at all is refused here rather than being allowed to
	// match everything: a filter that silently widens is worse than no filter.
	scope, err := resolveScope(ctx, st, flags["run"], flags["runs"])
	if err != nil {
		return err
	}
	q := store.SearchQuery{
		RunIDs: scope, Source: strings.TrimSpace(flags["camera"]), Text: query,
		Class: strings.TrimSpace(flags["class"]), Verdict: strings.TrimSpace(flags["verdict"]),
		From: from, To: to, Limit: limit, OrderBy: order,
	}

	hits, err := st.SearchCards(ctx, q)
	if err != nil {
		return err
	}
	total, err := st.SearchCardCounts(ctx, q)
	if err != nil {
		return err
	}
	// The summary is over the SINGLE run, because a window partitioned across runs has
	// no single clock and a panel of counts whose denominators are different runs would
	// be a sum of ratios. With several runs in scope the panel says so instead of
	// pretending.
	summaryRun := ""
	if len(scope) == 1 {
		summaryRun = scope[0]
	}

	fmt.Printf("search: %q", query)
	if camera := strings.TrimSpace(flags["camera"]); camera != "" {
		fmt.Printf(", camera matching %q", camera)
	}
	switch {
	case len(scope) == 1:
		fmt.Printf(", run %s\n", scope[0])
	case len(scope) > 1:
		fmt.Printf(", %d runs\n", len(scope))
	default:
		fmt.Println(", NO RUN IN SCOPE")
	}
	fmt.Println()

	// A camera filter with no run matches NOTHING and says why, rather than reporting a
	// count of zero over an undefined window.
	if len(scope) == 0 {
		fmt.Println("no run is in scope, so this search has NO WINDOW and cannot match anything.")
		fmt.Println("  A camera filter is not a run. It names one level up in `runs.source_arg`,")
		fmt.Println("  and answering a question about no particular run is not a narrower answer")
		fmt.Println("  to this question. Name a run with --run or --runs.")
		fmt.Println()
		printMatchRule()
		return nil
	}

	searched := summarizeForPanel(ctx, st, summaryRun, q)
	if summaryRun == "" {
		fmt.Printf("the window spans %d runs, so the absence breakdown is NOT summed across them: a\n", len(scope))
		fmt.Println("proportion of one run is not a proportion of another, and a total whose parts have")
		fmt.Println("different denominators is a number that means nothing. `pocctl unsearchable --run` gives")
		fmt.Println("the per-run breakdown.")
		fmt.Println()
	} else {
		printAbsence(searched)
		fmt.Println()
	}
	printMatchRule()
	fmt.Println()

	if len(hits) == 0 {
		fmt.Println("no matches.")
		fmt.Println()
		printNotAbsence(searched)
		return nil
	}
	for _, h := range hits {
		printCard(h)
	}
	if total > len(hits) {
		fmt.Printf("%d of %d matching answers shown; --limit raises the number\n", len(hits), total)
		fmt.Println()
	}
	if summaryRun != "" {
		printNotAbsence(searched)
	} else {
		printMultiRunNotAbsence()
	}
	return nil
}

// summarizeForPanel is the absence breakdown for a single run.
//
// It is empty for a multi-run scope and the caller prints the reason rather than a
// total.
func summarizeForPanel(ctx context.Context, st *store.Store, runID string, q store.SearchQuery) store.SearchSummary {
	if runID == "" {
		return store.SearchSummary{}
	}
	_, sum, err := st.SearchObservations(ctx, store.SearchQuery{
		RunID: runID, Text: q.Text, Class: q.Class, Verdict: q.Verdict,
		From: q.From, To: q.To,
	})
	if err != nil {
		return store.SearchSummary{}
	}
	return sum
}

// printMultiRunNotAbsence is the unsearchable notice for a cross-run search.
//
// It is NOT a sum, and saying so is the point. A run that asked nothing and a run that
// asked everything would sum to a fraction that describes neither, and a percentage
// across runs with different denominators is the arithmetic this surface refuses to do
// everywhere else.
func printMultiRunNotAbsence() {
	fmt.Println("THE ABSENCE BREAKDOWN IS NOT PRINTED FOR THIS SEARCH.")
	fmt.Println("  Several runs are in scope and the search does not know which of them could not")
	fmt.Println("  answer. A match absent above is therefore not evidence that no run saw anything.")
	fmt.Println("  Run `pocctl unsearchable --run <id>` per run for the honest per-run figure.")
}

// resolveScope is the run set, and it refuses to answer a camera-only question.
//
// The refusal is a refusal rather than an empty result because a caller who typed a
// camera name and nothing else has made a MISTAKE, and an empty result would tell them
// the camera has no detections when the honest answer is that no run was named. The
// difference matters: one is a fact about the world and the other is a fact about the
// query.
func resolveScope(ctx context.Context, st *store.Store, run, runs string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(runs, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if r := strings.TrimSpace(run); r != "" {
		// An explicit `--run` WINS over `--runs` rather than being unioned in. A union
		// of two overlapping sets is not a set anybody meant, and a caller who named
		// one run and then a list did one of the two on purpose.
		return []string{r}, nil
	}
	if len(out) > 0 {
		return out, nil
	}
	// No run named at all: the latest, which is what every single-run caller wants and
	// what the surface did before it could see more than one.
	latest, err := st.LatestRun(ctx)
	if err != nil {
		return nil, err
	}
	if latest == nil {
		return nil, fmt.Errorf("this store has no runs")
	}
	return []string{latest.ID}, nil
}

// printMatchRule is the one fixed line, on every run, not in a footnote.
//
// A search that ranks by substring while looking like a search that ranks by meaning is
// misleading in the one place where somebody is about to trust it.
func printMatchRule() {
	fmt.Println("  matching is a case-insensitive SUBSTRING over the model's stored sentence and over")
	fmt.Println("  a reviewer's ground-truth note, with structured filters beside it. A similarity-ranked")
	fmt.Println("  answer is not replayable from stored facts, so there are no embeddings here and that")
	fmt.Println("  is a decision rather than an omission.")
}

// printCard is one result, with the number beside the verdict word and nothing else
// that could be mistaken for a similarity.
func printCard(h store.SearchHit) {
	fmt.Printf("%s  %s\n", h.At.UTC().Format(time.RFC3339), confidenceVerdict(h))
	fmt.Printf("    %s\n", h.Summary)
	fmt.Printf("    %s\n", h.Class)
	// The run, whenever there is more than one in scope. It is on EVERY card and not
	// only in the header, because an answer from two runs is not comparable unless both
	// are visible on the card itself.
	fmt.Printf("    run %s (%s), source %s\n", h.RunLabel, h.RunID, h.SourceArg)
	// The bounds come from `store.EvidenceBounds` rather than being formatted here, so
	// a hit whose clip was never stored prints "bounds NOT RECORDED" beside the
	// "no clip retained" above it, and not a window at the year 1. One fact, one
	// rendering, two callers.
	fmt.Printf("    evidence: %s  sha256 %s  %s\n", store.EvidenceWindow(h), short(h.SHA256),
		store.EvidenceBounds(h))
	fmt.Printf("    asked as %s by %s at temperature %.1f, contract %s, output %s\n",
		h.PromptID, h.ModelIdentity, h.Temperature, h.ContractVersion, short(h.OutputHash))
	fmt.Printf("    aistate %s, reason %s\n", h.AIState, h.ReasonCode)
	// A reviewed verdict, with WHO concluded it. "This is wrong" from a named person is
	// evidence; the same sentence from nobody is an opinion, and a card that carried the
	// verdict without the name would have dropped exactly the part that made it worth
	// showing.
	if h.HasGroundTruth {
		marker := "reviewed"
		if h.MarkedFalsePositive {
			marker = "REVIEWED FALSE POSITIVE"
		}
		fmt.Printf("    %s: %s by %q at %s\n", marker, h.GroundTruth, h.GroundTruthBy,
			h.GroundTruthAt.UTC().Format(time.RFC3339))
		if n := strings.TrimSpace(h.GroundTruthNote); n != "" {
			fmt.Printf("      note: %s\n", n)
		}
	}
	fmt.Printf("    reproduce: %s\n", store.ProveCommand(h))
	fmt.Println()
}

// confidenceVerdict is the number on the card, and it is the WHOLE of the rule set.
//
// `87% - confirmed`, never `87% match`. The verdict is the noun and the number is the
// model's confidence in it. A card with no answer prints the absence word and NO number,
// because a percentage against a row nobody was asked about is a fabricated
// measurement, and it is the single easiest way for this surface to become the thing
// this repository exists not to be.
//
// The rendering is a FUNCTION and not a format string at the call site because the rule
// is that the number never appears without the verdict beside it, and the only way to
// keep two facts printed as one unit is to print them from one place.
func confidenceVerdict(h store.SearchHit) string {
	if !h.HasConfidence {
		// No answer. The absence word, and no number at all.
		return fmt.Sprintf("%s (%s) - no confidence to report: the model was %s, reason %s",
			h.Verdict, h.Class, h.AIState, h.ReasonCode)
	}
	return fmt.Sprintf("%.0f%% - %s (%s)", h.Confidence*100, h.Verdict, h.Class)
}

// unsearchableArgs is the forwarding from the `--unsearchable` spelling to the
// standalone command, and it forwards PARSED values rather than the raw argv.
//
// It replaces `argsWithout`, which stripped the `--unsearchable` token out of the
// raw argv and forwarded everything else. That was wrong in a way the flag's own
// existence makes obvious: `pocctl search --unsearchable --query magpie` handed
// `--query magpie` to a subcommand whose flags are `data-root`, `run` and
// `limit`, and the flag the whole feature exists to offer refused the command it
// is a spelling of. A flag that is a spelling of another command must be that
// command, including the flags it accepts and the ones it does not: the search's
// own arguments mean nothing to a list of stretches, and passing them through is
// passing on a question nobody asked.
func unsearchableArgs(searchFlags map[string]string) []string {
	out := []string{}
	if root := strings.TrimSpace(searchFlags["data-root"]); root != "" {
		out = append(out, "--data-root", root)
	}
	if run := strings.TrimSpace(searchFlags["run"]); run != "" {
		out = append(out, "--run", run)
	}
	return out
}

// cmdUnsearchable is the honest answer to scale.
//
// Not "how accurate were you" but "what did you not know, and when". The two questions
// are different and a surface that answers only the first has not answered anything.
// This is also the view that survives contact with a CV reviewer running the POC
// without a cloud, which is exactly when the second question is the one being asked.
func cmdUnsearchable(args []string) error {
	flags, err := flagSet("unsearchable", map[string]string{
		"data-root": "", "run": "", "limit": "10",
	}, args)
	if err != nil {
		return err
	}
	limit := 10
	if _, err := fmt.Sscanf(flags["limit"], "%d", &limit); err != nil || limit <= 0 {
		return fmt.Errorf("--limit %q is not a positive number", flags["limit"])
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
		latest, lerr := st.LatestRun(ctx)
		if lerr != nil {
			return lerr
		}
		if latest == nil {
			return fmt.Errorf("this store has no runs")
		}
		runID = latest.ID
	}

	windows, err := st.UnsearchableWindows(ctx, runID, limit)
	if err != nil {
		return err
	}
	fmt.Printf("windows run %s could not answer, longest first\n", runID)
	fmt.Println()
	if len(windows) == 0 {
		fmt.Println("  none: every proposal in this run was asked and answered.")
		fmt.Println()
		return nil
	}
	for _, w := range windows {
		fmt.Printf("  %s .. %s  (%.1f s)\n",
			w.From.UTC().Format(time.RFC3339), w.To.UTC().Format(time.RFC3339), w.Seconds())
		fmt.Printf("    %d proposal(s): %d not asked, %d asked and degraded, most common reason %q\n",
			w.Proposals, w.NotAsked, w.Degraded, w.Reason)
	}
	fmt.Println()
	fmt.Println("A window listed here is a stretch of the run the system could not speak about.")
	fmt.Println("It is not a run with nothing in it: absence and not-knowing are different claims,")
	fmt.Println("and only one of them is a finding about the scene.")
	return nil
}

// printAbsence is the absence breakdown, printed as a PANEL and not as a footer.
//
// It is four counts and not two because they have different causes with different
// owners: `no_objects` is the model saying the frame was empty, `degraded` is the model
// having been asked and failed, `not_asked` is the model never having been consulted,
// and `cannot_tell` is an answer that parsed and says nothing. Collapsing any two of them
// into "nothing found" produces a sentence that is true and useless, which is the worst
// property a sentence can have.
//
// It is a panel rather than a footer because the panel is the answer to "why is this
// empty?", and if it is small then the empty result is the only thing the reader sees.
func printAbsence(s store.SearchSummary) {
	if s.Proposed == 0 {
		fmt.Println("WINDOW: no detections at all, so there was nothing to ask about (not_asked).")
		fmt.Println("        this is a statement about the detector, not about the model.")
		return
	}
	if s.Answered == 0 {
		fmt.Printf("WINDOW: the model was asked about NOTHING: %d proposal(s), 0 answers "+
			"(not_asked %d, degraded %d).\n", s.Proposed, s.NotAsked, s.Degraded)
		fmt.Println("        the run proposed detections and consulted nobody about them.")
		return
	}
	fmt.Printf("WINDOW: %d proposal(s), asked and answered %d (%d with no objects, %d degraded, "+
		"%d not asked, %d cannot tell)\n",
		s.Proposed, s.Answered, s.NoObjects, s.Degraded, s.NotAsked, s.CannotTell)
	if s.Degraded > 0 {
		fmt.Printf("        %d degraded: asked, and failed, late or malformed. A missing answer here is NOT "+
			"a negative answer.\n", s.Degraded)
	}
	if s.NotAsked > 0 {
		fmt.Printf("        %d not asked: the detector proposed them and the verifier never saw them.\n", s.NotAsked)
	}
	if s.CannotTell > 0 {
		fmt.Printf("        %d cannot tell: answered with insufficient evidence or needs_review, which is a "+
			"positive claim that the frame did not settle it.\n", s.CannotTell)
	}
}

// printNotAbsence is the one refusal this surface must never get wrong.
//
// A search that finds rows AND a search that finds none can both be wrong in the same
// way: if some of the window was never asked, "no matches for X" says nothing about X,
// and printing it without qualification is a negative answer that was never given.
//
// It is a SCREEN and not a footnote: when the window is unsearchable the result area
// says so and states how much of it was never answered, because a count of zero with no
// qualification is the single sentence this whole surface exists to prevent.
func printNotAbsence(s store.SearchSummary) {
	unsearchable := s.NotAsked + s.Degraded
	if unsearchable == 0 {
		return
	}
	fmt.Println("THIS WINDOW IS NOT FULLY SEARCHABLE.")
	fmt.Printf("  %d of the %d proposals in it were never answered (%d not asked, %d degraded).\n",
		unsearchable, s.Proposed, s.NotAsked, s.Degraded)
	fmt.Println("  A match that is absent above is not evidence the model saw nothing. The run did not")
	fmt.Println("  ask, and that is not the same as the model having answered 'no'.")
	fmt.Println("  `pocctl unsearchable` lists the stretches of the run that could not be answered, with")
	fmt.Println("  the reason, which is the honest answer to \"what did you not know, and when\".")
}

// parseWhen reads an optional RFC3339 bound. Empty is "no bound", which is not the same
// as zero.
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
// The search surface reads a store and holds no `soak.Observer` and no `*vlm.Client`, so
// there is no handle by which it could reach a provider -- the same structural claim
// internal/soak/replay.go makes by taking no observer at all. It is a claim about the
// shape of the code, so it is made about the import graph rather than about a comment.
var _ = vlm.VerdictNone
