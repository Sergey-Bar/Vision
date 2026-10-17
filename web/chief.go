package main

// The chief of security: /chief.
//
// One page that reads both records the showcase hub already knows how to
// read - the product's own record and the lab's own 400 MB knowledge store -
// and says what they, taken together, amount to. The numbers are the records'
// numbers. The reading is deterministic: the same two records always produce
// the same sentence, and every sentence decomposes to the line where the number
// comes from, which is printed beside it. A judgement with no number behind it
// is not printed; a sentence that carries no judgement is not written.
//
// Honesty, which is the whole feature of this product, applies here too: when
// a baseline needs five minutes of a scene and the record holds three, the
// chief says "there is no baseline" - it does not publish a number and call it
// one. That refusal is itself a finding, and the one an animal crossing daily
// feeds directly into: one crossing is a first sighting, not a pattern.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------- the brief --------------------------------------------------------\n
type chiefBrief struct {
	GeneratedAt     string                `json:"generatedAt"`
	Headline        string                `json:"headline"`
	Read            string                `json:"read"`
	Product         *chiefProduct         `json:"product"`
	Lab             *labBrief             `json:"lab"`
	Baseline        *chiefBaseline        `json:"baseline"`
	Dark            []chiefDarkMinute     `json:"dark"`
	Findings        []chiefFinding        `json:"findings"`
	Recommendations []chiefRecommendation `json:"recommendations"`
	Perimeter       *askPerimeter         `json:"perimeter"`
	Sources         []askSource           `json:"sources"`
}

type chiefProduct struct {
	Ran            bool     `json:"ran"`
	Reason         string   `json:"reason,omitempty"`
	DataRoot       string   `json:"dataRoot,omitempty"`
	Runs           int      `json:"runs"`
	SpanFrom       string   `json:"spanFrom,omitempty"`
	SpanTo         string   `json:"spanTo,omitempty"`
	WithDetections int      `json:"withDetections"`
	Detections     int      `json:"detections"`
	Run            string   `json:"run,omitempty"`
	Source         string   `json:"source,omitempty"`
	Synthetic      bool     `json:"synthetic"`
	Events         int      `json:"events"`
	WithClip       int      `json:"withClip"`
	Judged         int      `json:"judged"`
	Unjudged       int      `json:"unjudged"`
	Classes        []string `json:"classes"`
}

type labMinute struct {
	Minute        string  `json:"minute"`
	Frames        int     `json:"frames"`
	PeopleAvg     float64 `json:"peopleAvg"`
	PeopleMax     int     `json:"peopleMax"`
	VehiclesAvg   float64 `json:"vehiclesAvg"`
	VehiclesMax   int     `json:"vehiclesMax"`
	AnimalsMax    int     `json:"animalsMax"`
	RestrictedAvg float64 `json:"restrictedAvg"`
	RestrictedMax int     `json:"restrictedMax"`
	OccupancyAvg  float64 `json:"occupancyAvg"`
	Alerts        int     `json:"alerts"`
}

type labLearner struct {
	Path    string         `json:"path"`
	Present bool           `json:"present"`
	Rows    map[string]int `json:"rows"`
	Reason  string         `json:"reason,omitempty"`
}

type labBrief struct {
	Available     bool           `json:"available"`
	Reason        string         `json:"reason,omitempty"`
	StorePath     string         `json:"storePath,omitempty"`
	GeneratedAt   string         `json:"generatedAt,omitempty"`
	VideoID       string         `json:"videoId,omitempty"`
	VideoCount    int            `json:"videoCount,omitempty"`
	WindowFrom    string         `json:"windowFrom,omitempty"`
	WindowTo      string         `json:"windowTo,omitempty"`
	Frames        int            `json:"frames"`
	Series        []labMinute    `json:"series"`
	AlertRules    map[string]int `json:"alertRules"`
	AlertMessages map[string]int `json:"alertMessages"`
	Clips         int            `json:"clips"`
	Entities      int            `json:"entities"`
	Relations     int            `json:"relations"`
	Learner       labLearner     `json:"learner"`
}

type chiefBaseline struct {
	Ran        bool             `json:"ran"`
	Reason     string           `json:"reason,omitempty"`
	Metric     string           `json:"metric,omitempty"`
	Minutes    int              `json:"minutes"`
	Median     float64          `json:"median"`
	Deviations []chiefDeviation `json:"deviations"`
}

type chiefDeviation struct {
	Minute string  `json:"minute"`
	Value  float64 `json:"value"`
	Median float64 `json:"median"`
	Times  float64 `json:"times"`
}

type chiefDarkMinute struct {
	Minute string `json:"minute"`
	Frames int    `json:"frames"`
}

type chiefFinding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Source   string `json:"source"`
}

type chiefRecommendation struct {
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Command string `json:"command,omitempty"`
}

// chiefBaselineMinMinutes is the floor under the word "baseline": a deviation
// needs minutes of the same scene to deviate FROM, and one sighting is not a
// baseline - it is the first sighting. (This is the animal-crossing rule.)
const chiefBaselineMinMinutes = 5

type chiefAnalysis struct {
	Headline        string
	Read            string
	Baseline        *chiefBaseline
	Dark            []chiefDarkMinute
	Findings        []chiefFinding
	Recommendations []chiefRecommendation
}

func registerChiefRoutes(repoRoot string) {
	http.HandleFunc("/api/chief", handleChief(repoRoot))
}

func handleChief(repoRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in handleChief: %v", rec)
				http.Error(w, fmt.Sprintf("Internal error: %v", rec), http.StatusInternalServerError)
			}
		}()
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		writeShowcaseJSON(w, buildChiefBrief(ctx, repoRoot))
	}
}

// ---------- gathering both records -------------------------------------------
func buildChiefBrief(ctx context.Context, repoRoot string) chiefBrief {
	brief := chiefBrief{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Findings:        []chiefFinding{},
		Recommendations: []chiefRecommendation{},
		Dark:            []chiefDarkMinute{},
		Sources:         []askSource{},
	}

	per := loadCameras(repoRoot)
	brief.Perimeter = &per
	if per.Ran {
		brief.Sources = append(brief.Sources, askSource{Label: per.File, Detail: fmt.Sprintf("%d cameras, %d cleared for external use", len(per.Cameras), per.ClearedCount)})
	}

	product := chiefProduct{Classes: []string{}}
	root, reason := pocRootAndBinary(repoRoot)
	if reason != "" {
		product.Reason = reason
	} else {
		runs, err := loadRuns(ctx, repoRoot, root)
		if err != nil {
			product.Reason = err.Error()
		} else {
			product.Ran = true
			product.DataRoot = root.Path
			brief.Sources = append(brief.Sources, askSource{Label: "pocctl runs --json --data-root " + root.Path})
			product.Runs = len(runs.Runs)
			for _, r := range runs.Runs {
				product.Detections += r.Detections
				if r.Detections > 0 {
					product.WithDetections++
				}
			}
			if n := len(runs.Runs); n > 0 {
				product.SpanFrom = runs.Runs[n-1].StartedAt
				product.SpanTo = runs.Runs[0].StartedAt
			}
			if pick := pickRunWithEvents(runs.Runs); pick != nil {
				product.Run = pick.ID
				product.Source = pick.SourceArg
				product.Synthetic = strings.Contains(strings.ToLower(pick.SourceArg), "testdata")
				if pick.Detections > 0 {
					review, err := loadReview(ctx, repoRoot, root, pick.ID)
					if err != nil {
						brief.Sources = append(brief.Sources, askSource{Label: "pocctl review --json", Detail: err.Error()})
					} else {
						brief.Sources = append(brief.Sources, askSource{Label: "pocctl review --json --run " + pick.ID + " --data-root " + root.Path})
						classes := map[string]bool{}
						for _, row := range review.Rows {
							product.Events++
							if row.ClipRef != "" {
								product.WithClip++
							}
							if row.Verdict != "" {
								product.Judged++
							}
							classes[row.Class] = true
						}
						product.Unjudged = product.Events - product.Judged
						for c := range classes {
							product.Classes = append(product.Classes, c)
						}
						sort.Strings(product.Classes)
					}
				}
			}
		}
	}
	brief.Product = &product

	lab := loadLabBrief(ctx, repoRoot)
	brief.Lab = &lab
	if lab.Available {
		brief.Sources = append(brief.Sources, askSource{Label: "py -3 tools/record_brief.py", Detail: "read-only over " + lab.StorePath + " and the learner's database"})
	}

	analysis := analyseChief(product, lab)
	brief.Headline = analysis.Headline
	brief.Read = analysis.Read
	brief.Baseline = analysis.Baseline
	brief.Dark = analysis.Dark
	brief.Findings = analysis.Findings
	brief.Recommendations = analysis.Recommendations
	return brief
}

// ---------- the analysis, pure and testable -------------------------------
func analyseChief(product chiefProduct, lab labBrief) chiefAnalysis {
	an := chiefAnalysis{
		Dark:            []chiefDarkMinute{},
		Findings:        []chiefFinding{},
		Recommendations: []chiefRecommendation{},
	}

	// --- the baseline and its deviations ---
	base := chiefBaseline{Metric: "restricted-zone alerts per minute", Deviations: []chiefDeviation{}}
	switch {
	case !lab.Available:
		base.Reason = "the lab record is not readable on this machine (" + lab.Reason + ")"
	case len(lab.Series) < chiefBaselineMinMinutes:
		base.Reason = fmt.Sprintf("the lab record holds %d minute(s); a baseline needs at least %d minutes of the same scene before a deviation means anything", len(lab.Series), chiefBaselineMinMinutes)
	default:
		base.Ran = true
		base.Minutes = len(lab.Series)
		vals := make([]float64, 0, len(lab.Series))
		for _, m := range lab.Series {
			vals = append(vals, float64(m.Alerts))
		}
		base.Median = median(vals)
		for _, m := range lab.Series {
			v := float64(m.Alerts)
			if v > base.Median*3 && v > base.Median+2 {
				base.Deviations = append(base.Deviations, chiefDeviation{
					Minute: m.Minute, Value: v, Median: base.Median, Times: v / base.Median,
				})
			}
		}
	}
	an.Baseline = &base

	// --- minutes where the detector saw nothing ---
	for _, m := range lab.Series {
		if m.Frames > 100 && m.PeopleMax == 0 {
			an.Dark = append(an.Dark, chiefDarkMinute{Minute: m.Minute, Frames: m.Frames})
		}
	}

	// --- findings ---
	if lab.Available {
		totalAlerts := 0
		for _, n := range lab.AlertRules {
			totalAlerts += n
		}
		if totalAlerts > 100 && len(lab.Series) >= chiefBaselineMinMinutes {
			an.Findings = append(an.Findings, chiefFinding{
				Severity: "watch",
				Title:    "The restricted-zone rule is a stream, not an alert",
				Detail: fmt.Sprintf("no-zone-entry fired %d times across %d minutes (%0.1f/min) on the lab's record. A rule at its own baseline is a queue no operator reads; the zone, or the threshold, is a human's decision.",
					totalAlerts, len(lab.Series), float64(totalAlerts)/float64(len(lab.Series))),
				Source: "tools/record_brief.py -> " + lab.StorePath,
			})
		}
		for _, d := range an.Dark {
			an.Findings = append(an.Findings, chiefFinding{
				Severity: "watch",
				Title:    fmt.Sprintf("Minute %s saw nothing", d.Minute),
				Detail:   fmt.Sprintf("%d frames were processed and the detector found zero people. Either the scene changed or the detector stopped seeing; the record does not say which.", d.Frames),
				Source:   "tools/record_brief.py -> " + lab.StorePath,
			})
		}
		if lab.Learner.Present {
			total := 0
			for _, n := range lab.Learner.Rows {
				total += n
			}
			if total == 0 {
				an.Findings = append(an.Findings, chiefFinding{
					Severity: "notice",
					Title:    "The behavioral learner holds zero rows",
					Detail:   "its baselines compare a live frame against the same camera's own history; on this machine it has never had any.",
					Source:   lab.Learner.Path,
				})
			}
		} else {
			an.Findings = append(an.Findings, chiefFinding{
				Severity: "notice",
				Title:    "The behavioral learner has no database yet",
				Detail:   lab.Learner.Reason,
				Source:   lab.Learner.Path,
			})
		}
	}

	if product.Ran {
		if product.WithDetections < product.Runs {
			an.Findings = append(an.Findings, chiefFinding{
				Severity: "notice",
				Title:    fmt.Sprintf("%d of %d runs recorded zero detections", product.Runs-product.WithDetections, product.Runs),
				Detail:   fmt.Sprintf("the record's %d detection(s) sit in %d run(s); every stored event is class %s.", product.Detections, product.WithDetections, strings.Join(product.Classes, ", ")),
				Source:   "pocctl runs --json",
			})
		}
		if product.Synthetic {
			an.Findings = append(an.Findings, chiefFinding{
				Severity: "notice",
				Title:    "Every stored detection is a fixture",
				Detail:   fmt.Sprintf("the run with events reads `%s` - a clip recorded for this demo, not a camera.", product.Source),
				Source:   "pocctl runs --json",
			})
		}
		if product.Events > 0 && product.Unjudged > 0 {
			an.Findings = append(an.Findings, chiefFinding{
				Severity: "watch",
				Title:    fmt.Sprintf("%d of %d events have no human verdict", product.Unjudged, product.Events),
				Detail:   "the false-positive rate is the one number that needs a person; the review sheet prints the exact mark command beside each event.",
				Source:   "pocctl review --json",
			})
		}
	}

	// --- recommendations ---
	if product.Events > 0 && product.Unjudged > 0 {
		an.Recommendations = append(an.Recommendations, chiefRecommendation{
			Title:   fmt.Sprintf("Judge the %d unjudged event(s)", product.Unjudged),
			Detail:  "`pocctl review` prints the exact mark command beside each event; verdicts are the difference between a measured false-positive rate and a guess.",
			Command: "pocctl review --data-root " + product.DataRoot,
		})
	}
	if lab.Available && lab.Learner.Present {
		total := 0
		for _, n := range lab.Learner.Rows {
			total += n
		}
		if total == 0 {
			an.Recommendations = append(an.Recommendations, chiefRecommendation{
				Title:   "Give the learner a scene to learn",
				Detail:  "the lab server can run a live source through the learner so its baselines have rows; the lab page names the command and the limits.",
				Command: "py tools/live_poc_server.py (lab demo)",
			})
		}
	}
	if len(an.Dark) > 0 {
		an.Recommendations = append(an.Recommendations, chiefRecommendation{
			Title:  "Re-watch the minute that saw nothing",
			Detail: "the frames exist in the lab's record; a source that goes quiet is either a scene change or a blind camera, and only a person can tell them apart.",
		})
	}
	if base.Ran && len(base.Deviations) == 0 {
		an.Recommendations = append(an.Recommendations, chiefRecommendation{
			Title:  "Keep the baseline rule, and expect it to say nothing",
			Detail: fmt.Sprintf("no minute exceeded 3x the median (%0.1f) by more than 2; on this record the surprise is the silent minute, not a spike - which is what a baseline is for.", base.Median),
		})
	}

	// --- the read, assembled from the facts above ---
	var b strings.Builder
	if product.Ran {
		fmt.Fprintf(&b, "The product's record holds %d run(s), %d detection(s), and %d run(s) hold all of them. ", product.Runs, product.Detections, product.WithDetections)
	} else {
		b.WriteString("The product's record could not be read on this machine. ")
	}
	if lab.Available {
		alerts := 0
		for _, n := range lab.AlertRules {
			alerts += n
		}
		fmt.Fprintf(&b, "The lab's record holds %d minutes of measurements from one public relay camera: %d restricted-zone alerts, and %d minute(s) where the detector saw nothing. ", len(lab.Series), alerts, len(an.Dark))
	} else {
		b.WriteString("The lab's record is not readable on this machine. ")
	}
	b.WriteString("Nothing in either record comes from a camera of ours.")
	an.Read = b.String()

	if lab.Available {
		an.Headline = fmt.Sprintf("The chief read %d run(s) and %d minute(s) of measurements; %d finding(s) want a human.", product.Runs, len(lab.Series), len(an.Findings))
	} else {
		an.Headline = fmt.Sprintf("The chief read %d run(s); the lab's record is not readable here; %d finding(s) want a human.", product.Runs, len(an.Findings))
	}
	return an
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64{}, vals...)
	sort.Float64s(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// ---------- the lab's own read-only report -----------------------------------
func findPython(repoRoot string) (string, []string) {
	for _, c := range []string{
		filepath.Join(repoRoot, ".venv-poc", "Scripts", "python.exe"),
		filepath.Join(repoRoot, ".venv-poc", "bin", "python"),
	} {
		if fileExists(c) {
			return c, nil
		}
	}
	if p, err := exec.LookPath("py"); err == nil {
		return p, []string{"-3"}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", nil
}

func loadLabBrief(ctx context.Context, repoRoot string) labBrief {
	out := labBrief{
		StorePath:     "dist/live-poc.sqlite3",
		Series:        []labMinute{},
		AlertRules:    map[string]int{},
		AlertMessages: map[string]int{},
		Learner:       labLearner{Rows: map[string]int{}},
	}
	script := filepath.Join(repoRoot, "tools", "record_brief.py")
	if !fileExists(script) {
		out.Reason = "tools/record_brief.py is not on this machine"
		return out
	}
	py, prefix := findPython(repoRoot)
	if py == "" {
		out.Reason = "no Python 3 interpreter was found (looked for .venv-poc, py, python3, python)"
		return out
	}
	store := filepath.Join(repoRoot, "dist", "live-poc.sqlite3")
	behavioral := filepath.Join(repoRoot, "dist", "behavioral.sqlite3")
	args := append(append([]string{}, prefix...), script, "--store", store, "--behavioral", behavioral)
	cmd := exec.CommandContext(ctx, py, args...)
	cmd.Dir = repoRoot
	raw, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		detail := ""
		if errors.As(err, &ee) {
			detail = lastMeaningfulLine(string(ee.Stderr))
		}
		out.Reason = "tools/record_brief.py failed: " + err.Error()
		if detail != "" {
			out.Reason = "tools/record_brief.py failed: " + detail
		}
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		out.Reason = "tools/record_brief.py did not print JSON: " + err.Error()
		return out
	}
	if out.Available && out.Series == nil {
		out.Series = []labMinute{}
	}
	return out
}
