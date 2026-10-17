package main

// The free-text console: /ask.
//
// One input box, a closed vocabulary, and every answer traced to the file or
// the command it came from. The design rule is the repository's own: a hit
// resolves without a model, and a model - if one is ever attached - narrates
// what the record already holds. Nothing here sends the request anywhere: the
// words select an intent and fill a draft; they never reach a shell, a path, a
// database or a model.
//
// What each intent does, in one line:
//
//	replay     reads the record through `pocctl runs/review --json` and serves
//	           the stored clip of a stored event; the request cannot name a file
//	rule       drafts a bundle in the product's own rule language and compiles
//	           it with the product's own compiler before showing it; the draft
//	           is born disabled and a human activates it
//	record     the run list, read by the POC's own tool
//	ledger     the chain state, decided by `pocctl audit ledger` and quoted
//	perimeter  the curated camera table, as written
//	brief      the chief of security's reading of both records
//	help       the vocabulary, listed
//	refuse     said out loud, with examples - this page does not guess
//
// A word the rule language cannot express is NAMED as dropped rather than
// silently ignored: "someone is running" drafts a motion rule and says, in the
// same breath, that the language has no speed condition. That sentence is the
// feature. A draft that quietly means less than it appears to is the failure
// mode this page exists to refuse.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sergey-Bar/vision/internal/rules"
)

// ---------- the request and the answer envelope -----------------------------

type askRequest struct {
	Q string `json:"q"`
}

type askSource struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

type askResponse struct {
	Query     string        `json:"query"`
	Intent    string        `json:"intent"`
	Matched   []string      `json:"matched,omitempty"`
	Headline  string        `json:"headline"`
	Detail    string        `json:"detail,omitempty"`
	Sources   []askSource   `json:"sources"`
	Replay    *askReplay    `json:"replay,omitempty"`
	Rule      *askRuleDraft `json:"rule,omitempty"`
	Record    *askRecord    `json:"record,omitempty"`
	Ledger    *askLedger    `json:"ledger,omitempty"`
	Perimeter *askPerimeter `json:"perimeter,omitempty"`
	Brief     *chiefBrief   `json:"brief,omitempty"`
}

func registerAskRoutes(repoRoot string) {
	http.HandleFunc("/api/ask", handleAsk(repoRoot))
}

func handleAsk(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleAsk: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var req askRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeShowcaseJSON(w, map[string]string{"reason": "the body must be {\"q\": \"...\"}: " + err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		writeShowcaseJSON(w, buildAsk(ctx, repoRoot, strings.TrimSpace(req.Q)))
	}
}

// ---------- the intent router -----------------------------------------------

type askIntentMatch struct {
	Name    string
	Matched []string
}

// askPhrases is the vocabulary, in priority order. The first intent with a
// matched word wins, so the order IS the policy: a sentence that names the
// ledger and also says "show me" is about the ledger.
var askPhrases = []struct {
	name  string
	words []string
}{
	{"help", []string{"help", "what can you do", "what can i ask", "how do i use"}},
	{"chief", []string{"chief", "brief", "briefing", "advise", "advice", "what should i", "what should we", "anything unusual", "anything strange", "anything suspicious", "suspicious", "insight"}},
	{"ledger", []string{"ledger", "chain", "tamper", "intact", "audit"}},
	{"perimeter", []string{"perimeter", "camera", "zone", "coverage", "watched"}},
	{"record", []string{"runs", "history", "record", "summary", "report", "how many", "what happened", "what has run"}}, {"rule", []string{"rule", "alert when", "alert me when", "alert if", "notify when", "notify me when", "wake me when", "wake me if", "trigger when", "trigger if", "create a rule", "make a rule", "new rule", "author a rule"}},
	{"replay", []string{"replay", "play", "clip", "footage", "detection", "event", "crossing", "watch", "see", "show", "video"}},
}

// resolveAskIntent turns a sentence into one intent. Deterministic: the same
// sentence always resolves the same way, and the words that decided it are
// returned so the page can print them.
func resolveAskIntent(q string) askIntentMatch {
	tokens := wordsOf(q)
	if len(tokens) == 0 {
		return askIntentMatch{Name: "help"}
	}
	for _, p := range askPhrases {
		var matched []string
		for _, w := range p.words {
			if matchesPhrase(tokens, w) {
				matched = append(matched, w)
			}
		}
		if len(matched) > 0 {
			return askIntentMatch{Name: p.name, Matched: matched}
		}
	}
	// No verb matched, but a subject did: "someone is running" is a rule
	// request, because a subject with no verb is a sentence about what to
	// watch for.
	if _, word, ok := findDraftSubject(tokens); ok {
		return askIntentMatch{Name: "rule", Matched: []string{word}}
	}
	return askIntentMatch{Name: "refuse"}
}

// wordsOf lowercases text and splits it into alphanumeric tokens.
func wordsOf(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return false
		}
		return true
	})
}

// matchesPhrase reports whether the tokens contain the phrase. A one-word
// phrase also matches its simple plural, so "camera" matches "cameras" and
// "detection" matches "detections".
func matchesPhrase(tokens []string, phrase string) bool {
	pw := strings.Fields(phrase)
	for i := 0; i+len(pw) <= len(tokens); i++ {
		ok := true
		for j, w := range pw {
			tok := tokens[i+j]
			match := tok == w
			if !match && len(pw) == 1 {
				match = tok == w+"s"
			}
			if !match {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// ---------- the response builders -------------------------------------------

func buildAsk(ctx context.Context, repoRoot, q string) askResponse {
	intent := resolveAskIntent(q)
	resp := askResponse{Query: q, Intent: intent.Name, Matched: intent.Matched, Sources: []askSource{}}
	tokens := wordsOf(q)

	switch intent.Name {
	case "help":
		resp.Headline = "Ask in plain words; a closed vocabulary answers."
		resp.Detail = "This page resolves a fixed vocabulary and nothing else: replay, rule, record, ledger, perimeter, brief. " +
			"Every answer is read from a file this machine already wrote or from the POC's own tool - no model is required for any of it. " +
			"Try: `show me the last detection`, `make a rule for someone on the fence at night`, `how many runs`, `is the ledger intact`, `cameras`, `brief me`."

	case "refuse":
		resp.Headline = "Nothing in that sentence matches a surface this page has."
		resp.Detail = "This page does not guess and does not send your words anywhere. The vocabulary: " +
			"replay a stored event; draft a rule; the run record; the ledger state; the perimeter; the chief's brief. " +
			"Try: `replay the crossing`, `alert me when someone is on the fence after hours`, `brief me`."

	case "chief":
		brief := buildChiefBrief(ctx, repoRoot)
		resp.Brief = &brief
		resp.Headline = brief.Headline
		resp.Detail = brief.Read
		resp.Sources = brief.Sources

	case "ledger":
		led := buildAskLedger(ctx, repoRoot)
		resp.Ledger = &led
		resp.Headline = ledgerHeadline(led)
		resp.Detail = "The chain state is decided by the POC's own `audit ledger`, never by this page; the output below is the command's, kept as it was written."
		if !led.Ran {
			resp.Detail = led.Reason
		} else {
			resp.Sources = []askSource{{Label: led.Command, Detail: led.DataRoot}}
		}

	case "perimeter":
		per := loadCameras(repoRoot)
		resp.Perimeter = &per
		if per.Ran {
			resp.Headline = fmt.Sprintf("%d cameras on the perimeter; %d cleared for external use.", len(per.Cameras), per.ClearedCount)
			resp.Detail = "Every row is a real camera run by somebody else, published publicly; watching is public and storing or republishing clips is theirs to permit. That is why every row says `cleared_for_external_use: false`."
			resp.Sources = []askSource{{Label: per.File}}
		} else {
			resp.Headline = "The perimeter table is not on this machine."
			resp.Detail = per.Reason
		}

	case "record":
		rec := buildAskRecord(ctx, repoRoot)
		resp.Record = &rec
		resp.Headline = recordHeadline(rec)
		if rec.Reason != "" {
			resp.Detail = rec.Reason
		} else {
			resp.Detail = fmt.Sprintf("The record spans %s → %s; %d of %d runs hold events, %d detections in total.",
				rec.SpanFrom, rec.SpanTo, rec.WithDetections, rec.Runs, rec.Detections)
		}
		resp.Sources = []askSource{{Label: rec.Command, Detail: rec.DataRoot}}

	case "rule":
		draft := draftRule(q)
		resp.Rule = &draft
		resp.Headline = fmt.Sprintf("Drafted `%s`; the product's own compiler read it: %d error(s), %d warning(s).", draft.Slug, draft.Errors, draft.Warnings)
		resp.Detail = "The draft is born disabled - nothing on this page activates a rule. A human reviews it and applies it."
		if len(draft.Dropped) > 0 {
			resp.Detail += " Words the rule language cannot express are named below rather than silently dropped."
		}
		resp.Sources = []askSource{
			{Label: "github.com/Sergey-Bar/vision/internal/rules", Detail: "the compiler that validated the draft, in-process"},
			{Label: "rules/builtin/bundle.yaml", Detail: "the bundle shape the draft mirrors"},
		}
		replay := buildAskReplay(ctx, repoRoot, subjectClassOf(tokens))
		resp.Replay = &replay

	case "replay":
		replay := buildAskReplay(ctx, repoRoot, subjectClassOf(tokens))
		resp.Replay = &replay
		resp.Headline = replayHeadline(replay)
		resp.Detail = replay.Note
		if replay.Reason != "" {
			resp.Detail = replay.Reason
		}
		if replay.Command != "" {
			resp.Sources = append(resp.Sources, askSource{Label: replay.Command, Detail: replay.DataRoot})
		}
		// A sentence that names what to watch for also gets a draft: the words
		// asked to see something, and seeing it once is how a rule starts.
		if _, _, ok := findDraftSubject(tokens); ok {
			draft := draftRule(q)
			resp.Rule = &draft
		}
	}
	return resp
}

func ledgerHeadline(led askLedger) string {
	switch {
	case !led.Ran:
		return "The ledger command could not run on this machine."
	case led.State == "intact":
		return fmt.Sprintf("The ledger verifies: %s.", led.RunID)
	case led.State == "empty":
		return "The ledger is empty: there is no chain to verify yet."
	default:
		return "The ledger did not verify."
	}
}

func recordHeadline(rec askRecord) string {
	if !rec.Ran {
		return "The run record could not be read on this machine."
	}
	return fmt.Sprintf("%d runs in the record.", rec.Runs)
}

func replayHeadline(rep askReplay) string {
	switch {
	case !rep.Ran:
		return "There is no record to replay on this machine."
	case rep.Run == "":
		return "The record holds no runs."
	case len(rep.Rows) == 0:
		return fmt.Sprintf("Run %s holds no events to replay.", rep.Run)
	default:
		return fmt.Sprintf("Replaying run %s: %d stored event(s).", rep.Run, rep.Total)
	}
}

// ---------- the run list and the review sheet, via the POC's own tool -------

type pocRun struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Mode          string  `json:"mode"`
	SourceKind    string  `json:"sourceKind"`
	SourceArg     string  `json:"sourceArg"`
	StartedAt     string  `json:"startedAt"`
	EndedAt       string  `json:"endedAt"`
	TargetSeconds float64 `json:"targetSeconds"`
	Note          string  `json:"note"`
	Detections    int     `json:"detections"`
}

type pocRunsFile struct {
	DataRoot    string   `json:"dataRoot"`
	GeneratedAt string   `json:"generatedAt"`
	Count       int      `json:"count"`
	Runs        []pocRun `json:"runs"`
}

type pocReviewRow struct {
	DetectionID string    `json:"detectionId"`
	At          string    `json:"at"`
	Class       string    `json:"class"`
	Family      string    `json:"family"`
	Score       float64   `json:"score"`
	Box         []float64 `json:"box"`
	Tag         string    `json:"tag"`
	ClipRef     string    `json:"clipRef"`
	Verdict     string    `json:"verdict"`
	MarkedBy    string    `json:"markedBy"`
	MarkedAt    string    `json:"markedAt"`
}

type pocReviewFile struct {
	DataRoot    string         `json:"dataRoot"`
	Run         string         `json:"run"`
	GeneratedAt string         `json:"generatedAt"`
	Count       int            `json:"count"`
	Judged      int            `json:"judged"`
	Rows        []pocReviewRow `json:"rows"`
}

var (
	showcaseRunsMu    sync.Mutex
	showcaseRunsCache = map[string]showcaseRunsEntry{}

	showcaseReviewMu    sync.Mutex
	showcaseReviewCache = map[string]showcaseReviewEntry{}

	// Zero in tests, where caching would hide the thing being tested.
	showcaseRunsTTL   = 20 * time.Second
	showcaseReviewTTL = 20 * time.Second
)

type showcaseRunsEntry struct {
	at   time.Time
	file pocRunsFile
}

type showcaseReviewEntry struct {
	at   time.Time
	file pocReviewFile
}

// pocRootAndBinary resolves the one data root every read path acts on and
// refuses, by name, when either half of the pair is absent.
func pocRootAndBinary(repoRoot string) (showcaseRootCheck, string) {
	bin := pocctlPath(repoRoot)
	if !fileExists(bin) {
		return showcaseRootCheck{}, fmt.Sprintf(
			"pocctl is not built on this machine (expected %s); scripts/live-demo.ps1 builds it",
			filepath.ToSlash(filepath.Join("poc", "active-monitoring", "bin", filepath.Base(bin))))
	}
	chosen, _ := chooseRoot(demoRoots(repoRoot))
	if chosen.Path == "" {
		return showcaseRootCheck{}, "no demo data root exists under poc/active-monitoring (.demo-data or .live-demo); `pocctl demo` writes one in about three minutes, and scripts/live-demo.ps1 writes one for the live path"
	}
	return chosen, ""
}

// loadRuns reads the run list through `pocctl runs --json`, cached briefly.
func loadRuns(ctx context.Context, repoRoot string, root showcaseRootCheck) (pocRunsFile, error) {
	showcaseRunsMu.Lock()
	if e, ok := showcaseRunsCache[root.Path]; ok && showcaseRunsTTL > 0 && time.Since(e.at) < showcaseRunsTTL {
		showcaseRunsMu.Unlock()
		return e.file, nil
	}
	showcaseRunsMu.Unlock()

	abs := filepath.Join(repoRoot, filepath.FromSlash(root.Path))
	exit, out := showcaseRun(ctx, pocctlPath(repoRoot), "runs", "--json", "--data-root", abs)
	if exit != 0 {
		return pocRunsFile{}, fmt.Errorf("pocctl runs --json exited %d: %s", exit, lastMeaningfulLine(out))
	}
	var file pocRunsFile
	if err := json.Unmarshal([]byte(out), &file); err != nil {
		return pocRunsFile{}, fmt.Errorf("pocctl runs --json did not print JSON: %w", err)
	}
	showcaseRunsMu.Lock()
	showcaseRunsCache[root.Path] = showcaseRunsEntry{at: time.Now(), file: file}
	showcaseRunsMu.Unlock()
	return file, nil
}

// loadReview reads one run's review sheet through `pocctl review --json`,
// cached briefly: the sheet is one query in the store and ten spawns per page
// load is a performance bug, not honesty.
func loadReview(ctx context.Context, repoRoot string, root showcaseRootCheck, runID string) (pocReviewFile, error) {
	key := root.Path + "|" + runID
	showcaseReviewMu.Lock()
	if e, ok := showcaseReviewCache[key]; ok && showcaseReviewTTL > 0 && time.Since(e.at) < showcaseReviewTTL {
		showcaseReviewMu.Unlock()
		return e.file, nil
	}
	showcaseReviewMu.Unlock()

	abs := filepath.Join(repoRoot, filepath.FromSlash(root.Path))
	exit, out := showcaseRun(ctx, pocctlPath(repoRoot), "review", "--json", "--run", runID, "--data-root", abs)
	if exit != 0 {
		return pocReviewFile{}, fmt.Errorf("pocctl review --json exited %d: %s", exit, lastMeaningfulLine(out))
	}
	var file pocReviewFile
	if err := json.Unmarshal([]byte(out), &file); err != nil {
		return pocReviewFile{}, fmt.Errorf("pocctl review --json did not print JSON: %w", err)
	}
	showcaseReviewMu.Lock()
	showcaseReviewCache[key] = showcaseReviewEntry{at: time.Now(), file: file}
	showcaseReviewMu.Unlock()
	return file, nil
}

// pickRunWithEvents is the run a replay acts on: the one holding the most
// events, ties going to the newest, and the newest run at all when nothing
// holds any. The choice is said out loud in the response, never silent.
func pickRunWithEvents(runs []pocRun) *pocRun {
	var best *pocRun
	for i := range runs {
		r := &runs[i]
		if best == nil || r.Detections > best.Detections {
			best = r
		}
	}
	return best
}

// ---------- replay ----------------------------------------------------------

type askReplayRow struct {
	DetectionID string  `json:"detectionId"`
	At          string  `json:"at"`
	Class       string  `json:"class"`
	Score       float64 `json:"score"`
	ClipKind    string  `json:"clipKind,omitempty"`
	ClipURL     string  `json:"clipUrl,omitempty"`
	Verdict     string  `json:"verdict,omitempty"`
	MarkedBy    string  `json:"markedBy,omitempty"`
}

type askReplay struct {
	Ran       bool           `json:"ran"`
	Reason    string         `json:"reason,omitempty"`
	Command   string         `json:"command,omitempty"`
	DataRoot  string         `json:"dataRoot,omitempty"`
	Run       string         `json:"run,omitempty"`
	Source    string         `json:"source,omitempty"`
	Synthetic bool           `json:"synthetic"`
	Total     int            `json:"total"`
	Rows      []askReplayRow `json:"rows"`
	Note      string         `json:"note,omitempty"`
}

const askReplayRowCap = 40

func buildAskReplay(ctx context.Context, repoRoot, wantClass string) askReplay {
	rep := askReplay{Rows: []askReplayRow{}}
	root, reason := pocRootAndBinary(repoRoot)
	if reason != "" {
		rep.Reason = reason
		return rep
	}
	runs, err := loadRuns(ctx, repoRoot, root)
	if err != nil {
		rep.Reason = err.Error()
		return rep
	}
	rep.Ran = true
	rep.DataRoot = root.Path
	rep.Command = "pocctl runs --json --data-root " + root.Path
	pick := pickRunWithEvents(runs.Runs)
	if pick == nil {
		rep.Note = "the record holds no runs"
		return rep
	}
	rep.Run = pick.ID
	rep.Source = pick.SourceArg
	rep.Synthetic = strings.Contains(strings.ToLower(pick.SourceArg), "testdata")

	if pick.Detections == 0 {
		rep.Note = fmt.Sprintf("run %s recorded zero detections; there is nothing in it to replay. The record's newest runs are listed on the record card.", pick.ID)
		return rep
	}

	review, err := loadReview(ctx, repoRoot, root, pick.ID)
	if err != nil {
		rep.Note = "the run list was read but its events could not be: " + err.Error()
		return rep
	}
	rep.Command += "\npocctl review --json --run " + pick.ID + " --data-root " + root.Path
	rep.Total = len(review.Rows)
	for i, row := range review.Rows {
		if i >= askReplayRowCap {
			break
		}
		out := askReplayRow{
			DetectionID: row.DetectionID,
			At:          row.At,
			Class:       row.Class,
			Score:       row.Score,
			Verdict:     row.Verdict,
			MarkedBy:    row.MarkedBy,
		}
		if row.ClipRef != "" {
			out.ClipURL = "/api/replay/clip?run=" + pick.ID + "&det=" + row.DetectionID
			out.ClipKind = clipKindOf(row.ClipRef)
		}
		rep.Rows = append(rep.Rows, out)
	}
	rep.Note = replayNote(wantClass, rep, pick)
	return rep
}

// replayNote says what the player is actually showing: a fixture and not a
// camera, a class the words did not ask for, or events without a clip.
func replayNote(wantClass string, rep askReplay, run *pocRun) string {
	var notes []string
	if rep.Synthetic {
		notes = append(notes, fmt.Sprintf("the run's source is `%s` - a fixture recorded for this demo, not a camera; the clips are synthetic and labelled so", run.SourceArg))
	} else if run.SourceArg != "" {
		notes = append(notes, fmt.Sprintf("the run's source is `%s`; the footage is somebody else's and stays on this machine", run.SourceArg))
	}
	if wantClass != "" && wantClass != "motion" {
		classes := map[string]bool{}
		for _, r := range rep.Rows {
			classes[r.Class] = true
		}
		names := make([]string, 0, len(classes))
		for c := range classes {
			names = append(names, c)
		}
		sort.Strings(names)
		if !classes[wantClass] {
			notes = append(notes, fmt.Sprintf("nothing in this run is class %s; the record holds class %s - the player shows what the record holds, not what the words asked for", wantClass, strings.Join(names, ", ")))
		}
	}
	withClip := 0
	for _, r := range rep.Rows {
		if r.ClipURL != "" {
			withClip++
		}
	}
	if withClip < len(rep.Rows) {
		notes = append(notes, fmt.Sprintf("%d of %d events carry a clip; the rest have none and cannot be judged", withClip, len(rep.Rows)))
	}
	return strings.Join(notes, ". ") + "."
}

// clipKindOf maps an evidence reference to the element a player should use.
func clipKindOf(ref string) string {
	switch strings.ToLower(filepath.Ext(ref)) {
	case ".mp4", ".webm":
		return "video"
	case ".jpg", ".jpeg", ".png":
		return "image"
	default:
		return ""
	}
}

// ---------- record, ledger, perimeter ---------------------------------------

type askRecordRow struct {
	ID         string `json:"id"`
	StartedAt  string `json:"startedAt"`
	Mode       string `json:"mode"`
	Source     string `json:"source"`
	Detections int    `json:"detections"`
}

type askRecord struct {
	Ran            bool           `json:"ran"`
	Reason         string         `json:"reason,omitempty"`
	Command        string         `json:"command,omitempty"`
	DataRoot       string         `json:"dataRoot,omitempty"`
	Runs           int            `json:"runs"`
	SpanFrom       string         `json:"spanFrom,omitempty"`
	SpanTo         string         `json:"spanTo,omitempty"`
	WithDetections int            `json:"withDetections"`
	Detections     int            `json:"detections"`
	Table          []askRecordRow `json:"table"`
}

func buildAskRecord(ctx context.Context, repoRoot string) askRecord {
	rec := askRecord{Table: []askRecordRow{}}
	root, reason := pocRootAndBinary(repoRoot)
	if reason != "" {
		rec.Reason = reason
		return rec
	}
	runs, err := loadRuns(ctx, repoRoot, root)
	if err != nil {
		rec.Reason = err.Error()
		return rec
	}
	rec.Ran = true
	rec.DataRoot = root.Path
	rec.Command = "pocctl runs --json --data-root " + root.Path
	rec.Runs = len(runs.Runs)
	for _, r := range runs.Runs {
		rec.Detections += r.Detections
		if r.Detections > 0 {
			rec.WithDetections++
		}
		rec.Table = append(rec.Table, askRecordRow{
			ID: r.ID, StartedAt: r.StartedAt, Mode: r.Mode, Source: r.SourceArg, Detections: r.Detections,
		})
	}
	if n := len(runs.Runs); n > 0 {
		rec.SpanFrom = runs.Runs[n-1].StartedAt
		rec.SpanTo = runs.Runs[0].StartedAt
	}
	return rec
}

type askLedger struct {
	Ran      bool   `json:"ran"`
	Reason   string `json:"reason,omitempty"`
	Command  string `json:"command,omitempty"`
	DataRoot string `json:"dataRoot,omitempty"`
	Exit     int    `json:"exit"`
	State    string `json:"state,omitempty"`
	RunID    string `json:"runId,omitempty"`
	Output   string `json:"output,omitempty"`
}

func buildAskLedger(ctx context.Context, repoRoot string) askLedger {
	led := askLedger{}
	root, reason := pocRootAndBinary(repoRoot)
	if reason != "" {
		led.Reason = reason
		return led
	}
	abs := filepath.Join(repoRoot, filepath.FromSlash(root.Path))
	exit, out := showcaseRun(ctx, pocctlPath(repoRoot), "audit", "ledger", "--data-root", abs)
	led.Ran = true
	led.Command = "pocctl audit ledger --data-root " + root.Path
	led.DataRoot = root.Path
	led.Exit = exit
	led.State, led.RunID, _ = classifyLedger(exit, out)
	led.Output = trimTail(out, 24)
	return led
}

// trimTail keeps the last n non-empty lines, so a page shows the verdict and
// not a wall.
func trimTail(out string, n int) string {
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

type askCamera struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Place                 string `json:"place"`
	Operator              string `json:"operator"`
	URL                   string `json:"url"`
	Verified              string `json:"verified"`
	ContainsPeople        string `json:"containsPeople"`
	ClearedForExternalUse bool   `json:"clearedForExternalUse"`
}

type askPerimeter struct {
	Ran          bool        `json:"ran"`
	Reason       string      `json:"reason,omitempty"`
	File         string      `json:"file,omitempty"`
	Cameras      []askCamera `json:"cameras"`
	ClearedCount int         `json:"clearedCount"`
}

func loadCameras(repoRoot string) askPerimeter {
	out := askPerimeter{Cameras: []askCamera{}}
	rel := filepath.ToSlash(filepath.Join("poc", "active-monitoring", "cameras.json"))
	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		out.Reason = rel + " is not on this machine"
		return out
	}
	var file struct {
		Cameras []askCamera `json:"cameras"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		out.Reason = rel + " exists but could not be read: " + err.Error()
		return out
	}
	out.Ran = true
	out.File = rel
	out.Cameras = file.Cameras
	for _, c := range file.Cameras {
		if c.ClearedForExternalUse {
			out.ClearedCount++
		}
	}
	return out
}

// ---------- the rule draft --------------------------------------------------

type askRuleTerm struct {
	Term   string `json:"term"`
	Became string `json:"became"`
}

type askRuleDraft struct {
	Slug     string          `json:"slug"`
	Title    string          `json:"title"`
	YAML     string          `json:"yaml"`
	Mapped   []askRuleTerm   `json:"mapped"`
	Dropped  []askRuleTerm   `json:"dropped"`
	Problems []rules.Problem `json:"problems"`
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Apply    string          `json:"apply"`
}

type draftSubject struct {
	id     string
	title  string
	events []string
	sev    string
	words  []string
}

var draftSubjects = []draftSubject{
	{"person", "Person", []string{"motion", "analytics"}, "high",
		[]string{"person", "someone", "man", "woman", "human", "people", "intruder", "burglar", "thief"}},
	{"vehicle", "Vehicle", []string{"motion", "analytics"}, "medium",
		[]string{"car", "vehicle", "truck", "van"}},
	{"animal", "Animal", []string{"motion", "analytics"}, "medium",
		[]string{"animal", "dog", "fox", "deer", "horse", "cow", "sheep"}},
	{"door", "Door", []string{"access.denied", "device_state"}, "critical",
		[]string{"door", "gate"}},
	{"camera", "Camera", []string{"device.camera_offline"}, "low",
		[]string{"camera"}},
}

var defaultDraftSubject = draftSubject{"motion", "Motion", []string{"motion", "analytics"}, "medium", nil}

func findDraftSubject(tokens []string) (draftSubject, string, bool) {
	for _, s := range draftSubjects {
		for _, w := range s.words {
			if matchesPhrase(tokens, w) {
				return s, w, true
			}
		}
	}
	return defaultDraftSubject, "", false
}

// subjectClassOf is the class a sentence's subject names, for the honest
// "nothing here is class X" note. Empty when the sentence names no subject.
func subjectClassOf(tokens []string) string {
	if s, _, ok := findDraftSubject(tokens); ok {
		return s.id
	}
	return ""
}

var draftZoneWords = []string{"fence", "perimeter", "restricted", "boundary", "yard", "backyard", "compound"}
var draftTimeWords = []string{"night", "overnight", "after hours", "after-hours", "outside hours", "outside business hours"}

var draftDropped = []struct {
	words  []string
	reason string
}{
	{[]string{"running", "runs", "ran", "sprint", "sprinting", "fast", "speeding", "speed", "racing"},
		"the rule language has no speed or kinematic condition; the draft reads the motion signal instead"},
	{[]string{"loitering", "loiter", "lingering", "dwelling", "standing", "waiting"},
		"the rule language has no duration condition; a person who stops is not distinguished from one who passes"},
	{[]string{"red", "blue", "green", "yellow", "white", "black", "silver", "orange", "brown", "grey", "gray"},
		"the rule language has no colour condition; the draft does not read the object's colour"},
}

// draftSiteSettings mirrors the built-in bundle's site and settings blocks, so
// a draft differs from the shipped bundle only where the sentence says so.
const draftSiteSettings = `site:
  siteId: site-default
  timezones:
    - Asia/Jerusalem

settings:
  clock:
    referenceSource: ntp
    driftWarnMs: 2000
    driftSevereMs: 10000
    skewTolerance: 5000
  dedupe:
    windowSeconds: 30
    keys:
      - zone.id
      - device.id
  burstMerge:
    windowSeconds: 60
    maxEvents: 200

`

// draftRule turns a sentence into a disabled bundle in the product's rule
// language, compiles it with the product's own compiler, and names every word
// it could not express. Pure: no files, no shell, no state.
func draftRule(q string) askRuleDraft {
	tokens := wordsOf(q)
	subj, subjWord, hasSubj := findDraftSubject(tokens)

	zone, zoneWord := false, ""
	for _, w := range draftZoneWords {
		if matchesPhrase(tokens, w) {
			zone, zoneWord = true, w
			break
		}
	}
	overnight, timeWord := false, ""
	for _, w := range draftTimeWords {
		if matchesPhrase(tokens, w) {
			overnight, timeWord = true, w
			break
		}
	}

	var mapped, dropped []askRuleTerm
	if hasSubj {
		mapped = append(mapped, askRuleTerm{subjWord, "subject " + subj.title + " -> eventType [" + strings.Join(subj.events, ", ") + "]"})
	} else {
		mapped = append(mapped, askRuleTerm{"(no subject word)", "eventType [motion, analytics]"})
	}
	if zone {
		mapped = append(mapped, askRuleTerm{zoneWord, "zone.pathPrefix /restricted"})
	}
	if overnight {
		mapped = append(mapped, askRuleTerm{timeWord, "schedule business-hours, inverted"})
	}
	for _, d := range draftDropped {
		for _, w := range d.words {
			if matchesPhrase(tokens, w) {
				dropped = append(dropped, askRuleTerm{w, d.reason})
			}
		}
	}

	sev := subj.sev
	if subj.id == "person" && (zone || overnight) {
		sev = "high"
	}
	title := draftTitle(subj, zone, overnight)
	slug := "draft." + subj.id
	if zone {
		slug += ".restricted"
	}
	if overnight {
		slug += ".after-hours"
	}

	var b strings.Builder
	b.WriteString("apiVersion: security.io/v1\nkind: RuleBundle\nversion: 0.1.0\n")
	b.WriteString("description: " + yamlQuote("A draft authored from free text on the showcase hub: "+clipText(q, 160)) + "\n\n")
	b.WriteString(draftSiteSettings)
	b.WriteString("rules:\n")
	b.WriteString("  - id: " + slug + "\n")
	b.WriteString("    description: " + yamlQuote("Drafted from free text. Nothing here is active until a human reviews and applies it.") + "\n")
	b.WriteString("    enabled: false\n")
	b.WriteString("    priority: 500\n")
	b.WriteString("    tags: [draft]\n")
	b.WriteString("    when:\n")
	b.WriteString("      all:\n")
	b.WriteString("        - eventType: [" + strings.Join(subj.events, ", ") + "]\n")
	if zone {
		b.WriteString("        - zone:\n            pathPrefix: /restricted\n")
	}
	if overnight {
		b.WriteString("        - schedule:\n            id: business-hours\n            invert: true\n")
	}
	if subj.id != "camera" {
		b.WriteString("        - corroborate:\n            eventTypes: [analytics, access.granted, device_state, motion]\n            minSignals: 2\n            minEvidenceTier: B\n")
	}
	b.WriteString("    then:\n")
	b.WriteString("      createIncident: true\n")
	b.WriteString("      severity: " + sev + "\n")
	b.WriteString("      title: " + yamlQuote(title) + "\n")
	if subj.id == "door" {
		b.WriteString("      escalate: true\n")
	}
	yaml := b.String()

	// The product's own compiler is the only thing that decides whether this
	// draft is well-formed. The page shows its answer, not a reassurance.
	_, problems := rules.NewCompiler().Compile([]byte(yaml))
	if problems == nil {
		problems = []rules.Problem{}
	}
	errs, warns := 0, 0
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			errs++
		} else {
			warns++
		}
	}

	apply := "Write the draft to a file and run: securityctl rules lint --bundle <file>. Activation is a separate, reviewed step and this page does not do it."
	if errs > 0 {
		apply = fmt.Sprintf("The compiler refused this draft (%d error(s)); nothing was activated and nothing should be until they are fixed.", errs)
	}

	return askRuleDraft{
		Slug: slug, Title: title, YAML: yaml,
		Mapped: mapped, Dropped: dropped,
		Problems: problems, Errors: errs, Warnings: warns,
		Apply: apply,
	}
}

func draftTitle(subj draftSubject, zone, overnight bool) string {
	switch subj.id {
	case "door":
		if overnight {
			return "Door forced after hours"
		}
		return "Door event"
	case "camera":
		return "Camera offline"
	}
	title := subj.title
	if zone {
		title += " in the restricted zone"
	}
	if overnight {
		title += " after hours"
	}
	if !zone && !overnight {
		title += " detected"
	}
	return title
}

// clipText collapses whitespace and truncates, so a draft's provenance line is
// one short line and never a paragraph.
func clipText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return s
}

// yamlQuote renders a string as a YAML double-quoted scalar. JSON string
// escaping is a subset of YAML's, so json.Marshal produces a scalar every
// YAML reader here accepts - and a sentence containing newlines or quotes
// cannot become a second YAML document.
func yamlQuote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return string(b)
}
