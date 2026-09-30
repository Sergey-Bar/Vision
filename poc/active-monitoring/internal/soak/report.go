package soak

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report is the whole thing: the numbers, the criteria, the faults, the peaks,
// and the list of things it does not prove.
type Report struct {
	GeneratedAt time.Time   `json:"generated_at"`
	Run         RunInfo     `json:"run"`
	Verdict     string      `json:"verdict"`
	Provisional bool        `json:"provisional"`
	Criteria    []Criterion `json:"criteria"`
	Summary     Summary     `json:"summary"`
	Faults      []FaultRow  `json:"faults"`
	Rehearsals  []Rehearsal `json:"rehearsals"`
	Peaks       Peaks       `json:"peaks"`
	Counters    Counters    `json:"counters"`
	Truth       TruthLine   `json:"truth"`
	NotProven   []string    `json:"not_proven"`
	// FalsePositives is its own row and not a criterion. It is NOT RUN until a
	// human has marked events, and it is never folded into the overall verdict:
	// a number that exists only when a person looked cannot be one a machine
	// scores itself on.
	FalsePositives FalsePositiveRow `json:"false_positives"`
	Warnings       []string         `json:"warnings,omitempty"`
}

// FalsePositiveRow is the false-positive line as printed.
type FalsePositiveRow struct {
	Verdict  Verdict `json:"verdict"`
	Examined int     `json:"examined"`
	False    int     `json:"false"`
	By       string  `json:"by,omitempty"`
	When     string  `json:"when,omitempty"`
	Note     string  `json:"note,omitempty"`
	// Printed is the sentence, so the JSON and the Markdown cannot disagree
	// about what the row says.
	Printed string `json:"printed"`
}

// NewFalsePositiveRow builds the row from the human's marking.
func NewFalsePositiveRow(h *HumanMarking) FalsePositiveRow {
	v, printed := h.Verdict()
	row := FalsePositiveRow{Verdict: v, Printed: printed}
	if h != nil {
		row.Examined, row.False, row.By, row.Note = h.Examined, h.False, h.By, h.Note
		if !h.When.IsZero() {
			row.When = h.When.Format(time.RFC3339)
		}
	}
	return row
}

// RunInfo is the run's identity. It is the header the report prints, and it is
// read from the database rather than from whatever the process currently holds,
// so the report describes the run and not the moment.
type RunInfo struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Mode        string     `json:"mode"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	TargetSecs  float64    `json:"target_seconds"`
	ElapsedSecs float64    `json:"elapsed_seconds"`
	ClockSource string     `json:"clock_source"`
	Database    string     `json:"database"`
}

// FaultRow is one recorded fault, with the rehearsal's expectation beside it.
type FaultRow struct {
	At       time.Time `json:"at"`
	Reason   string    `json:"reason"`
	Detail   string    `json:"detail"`
	Expected string    `json:"expected,omitempty"`
	Observed string    `json:"observed,omitempty"`
	Verdict  string    `json:"verdict"`
}

// Peaks are the measured maxima, with the declared ceilings beside them.
type Peaks struct {
	RSSBytes     int64   `json:"rss_bytes"`
	DeclaredRSS  int64   `json:"declared_rss_bytes"`
	DBBytes      int64   `json:"db_bytes"`
	DeclaredDB   int64   `json:"declared_db_bytes"`
	DiskBytes    int64   `json:"disk_bytes"`
	DeclaredDisk int64   `json:"declared_disk_bytes"`
	CPUPercent   float64 `json:"cpu_percent"`
	// CPUMeasured says whether CPUPercent is a measurement. The resource table
	// prints NOT RUN against it rather than a zero that passes on any load.
	CPUMeasured  bool    `json:"cpu_measured"`
	DeclaredCPU  float64 `json:"declared_cpu_percent"`
	AIP99MS      float64 `json:"ai_p99_ms"`
	DeclaredAIMs float64 `json:"declared_ai_budget_ms"`
	Samples      int     `json:"samples"`
}

// Counters is the detection and push tally.
type Counters struct {
	Detections     int            `json:"detections"`
	ByStatus       map[string]int `json:"by_status"`
	Evidence       int            `json:"evidence"`
	PushAttempts   int            `json:"push_attempts"`
	DeadLettered   int            `json:"dead_lettered"`
	Heartbeats     int            `json:"heartbeats"`
	Faults         int            `json:"faults"`
	SegmentsOnDisk int            `json:"segments_on_disk"`
}

// TruthLine is the two sentences that make the report honest, generated from
// the run's own configuration rather than written by the caller.
type TruthLine struct {
	Source     string `json:"source"`
	PushTarget string `json:"push_target"`
	Provenance string `json:"provenance"`
}

// BuildTruth produces the truth line. Against the simulator it says so, word for
// word, every time. There is no code path that produces a "reached Milestone"
// claim without an explicit non-loopback base URL.
func BuildTruth(sourceLabel, pushBaseURL, pushName string, isVMS bool, provenance string) TruthLine {
	t := TruthLine{Source: sourceLabel, Provenance: provenance}
	if isVMS && pushBaseURL != "" {
		t.PushTarget = fmt.Sprintf("%s (%s)", pushName, pushBaseURL)
		return t
	}
	t.PushTarget = "push target: local simulator (xpsim). No XProtect server was reachable during this run."
	return t
}

// NotProven is the machine's own list of what this report does not establish.
// It is derived from the inputs, so a run that never reached its target or never
// rehearsed a fault carries the corresponding line.
func NotProven(in Inputs, s Summary) []string {
	out := []string{
		"This report does not prove 100% detection: there is no ground truth, only a detection rate.",
	}
	if in.FalsePositives == nil || in.FalsePositives.Examined == 0 {
		out = append(out, "This report does not prove a false-positive rate: no human marked any event as false, so there is no number to print. The row in the table says NOT RUN and a zero would be a measurement nobody made.")
	}
	out = append(out,
		"This report does not prove an event reached an operator's Smart Client. What it proves is that the POST is contract-shaped and that a read-back found it.",
	)
	if in.PushTargetIsSimulator {
		out = append(out, "The push target was a local simulator, so nothing reached Milestone. No XProtect server was reachable during this run.")
	}
	if in.SourceKind == "loop" {
		out = append(out, "The source was a recording played in a loop. No live camera was used, so the rtsp path is documented and unproven.")
	}
	if in.Provenance == "synthetic" {
		out = append(out, "The video material this run's numbers come from is labelled synthetic. It is not evidence that the system detects birds or anything else in the world.")
	}
	if len(in.RehearsedFaults) == 0 {
		out = append(out, "No fault was rehearsed on this run, so nothing here is evidence of behaviour under failure.")
	}
	if s.Provisional {
		out = append(out, fmt.Sprintf("The run reached %s of a %s target, so the verdict is PROVISIONAL: a shorter run cannot conclude a longer claim.", in.Elapsed.Round(time.Minute), in.Target))
	}
	if in.AIP99Samples == 0 {
		out = append(out, "No AI round trip was measured, so no latency claim is made.")
	}
	return out
}

// Markdown renders the report. Every number in it came from a row, and the
// provenance is printed next to the numbers derived from material.
func (r Report) Markdown() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# POC run report\n\n")
	w("- **run**: `%s` (%s)\n", r.Run.ID, r.Run.Label)
	w("- **mode**: `%s`\n", r.Run.Mode)
	w("- **clock source**: `%s`\n", r.Run.ClockSource)
	w("- **database**: `%s`\n", r.Run.Database)
	w("- **elapsed**: %s of a %s target\n", dur(r.Run.ElapsedSecs), dur(r.Run.TargetSecs))
	w("- **verdict**: **%s**", r.Verdict)
	if r.Provisional {
		w(" (PROVISIONAL: the run has not reached its target, so nothing here concludes)")
	}
	w("\n\n")
	w("```\n%s\n%s\n%s\n```\n\n", r.Truth.Source, r.Truth.PushTarget, r.Truth.ProvenanceLine())

	w("## Acceptance criteria\n\n")
	w("Three verdicts only: `PASS`, `FAIL`, `NOT RUN`. A `NOT RUN` is never a `PASS`.\n\n")
	w("| # | criterion | verdict | measured | how | arithmetic |\n|---|---|---|---|---|---|\n")
	for _, c := range r.Criteria {
		w("| %s | %s | **%s** | %s | %s | `%s` |\n",
			c.ID, c.Name, c.Verdict, oneLine(c.Observed), oneLine(c.How), c.Arithmetic)
		if c.Missing != "" {
			w("| | (why NOT RUN) | | %s | | |\n", oneLine(c.Missing))
		}
		for _, cl := range c.Clauses {
			if cl.Verdict == VerdictPass {
				continue
			}
			w("| | - %s | %s | %s | | want %s |\n", oneLine(cl.Name), cl.Verdict, oneLine(cl.Value), oneLine(cl.Want))
		}
	}
	w("\n")

	w("## False positives\n\n")
	fp := r.FalsePositives
	w("| verdict | examined | marked false | by | when |\n|---|---|---|---|---|\n")
	w("| **%s** | %s | %s | %s | %s |\n\n", fp.Verdict, orDash(fp.Examined), orDash(fp.False),
		orUnstated(fp.By), orUnstated(fp.When))
	w("  %s\n", fp.Printed)
	if fp.Note != "" {
		w("\n  in the reviewer's words: %s\n", fp.Note)
	}
	w("\n  This row is not a criterion and is not folded into the verdict. A false-positive")
	w("  rate exists only when a person looked at the events and said which were false;")
	w("  until then it is `NOT RUN`, and a zero here would be a measurement nobody made.\n\n")

	w("## Counters\n\n")
	w("| what | value |\n|---|---|\n")
	w("| detections | %d |\n", r.Counters.Detections)
	for _, k := range sortedKeys(r.Counters.ByStatus) {
		w("| detections in `%s` | %d |\n", k, r.Counters.ByStatus[k])
	}
	w("| stored evidence | %d |\n", r.Counters.Evidence)
	w("| push attempts | %d |\n", r.Counters.PushAttempts)
	w("| dead-lettered pushes | %d |\n", r.Counters.DeadLettered)
	w("| heartbeats | %d |\n", r.Counters.Heartbeats)
	w("| faults | %d |\n", r.Counters.Faults)
	w("| rolling segments on disk | %d |\n", r.Counters.SegmentsOnDisk)
	w("\n")

	w("## Faults\n\n")
	if len(r.Faults) == 0 {
		w("No fault was recorded on this run.\n\n")
	} else {
		w("| at | reason | verdict | detail |\n|---|---|---|---|\n")
		for _, f := range r.Faults {
			w("| %s | `%s` | %s | %s |\n", f.At.Format(time.RFC3339), f.Reason, f.Verdict, oneLine(f.Detail))
		}
		w("\n")
	}

	w("## Rehearsed faults\n\n")
	if len(r.Rehearsals) == 0 {
		w("**NOT RUN.** No fault was injected on this run, so this report contains no evidence at all about behaviour under failure. Gate D comes before the run clock, not after it.\n\n")
	} else {
		w("| fault | expected | observed | verdict |\n|---|---|---|---|\n")
		for _, r2 := range r.Rehearsals {
			v := VerdictPass
			if r2.Observed != r2.Expected {
				v = VerdictFail
			}
			w("| `%s` | `%s` | `%s` | %s |\n", r2.Fault, r2.Expected, r2.Observed, v)
		}
		w("\n")
	}

	w("## Resource peaks\n\n")
	w("| resource | peak | declared | within |\n|---|---|---|---|\n")
	w("| RSS | %s | %s | %s |\n", Bytes(r.Peaks.RSSBytes), Bytes(r.Peaks.DeclaredRSS), within(r.Peaks.RSSBytes, r.Peaks.DeclaredRSS))
	w("| database | %s | %s | %s |\n", Bytes(r.Peaks.DBBytes), Bytes(r.Peaks.DeclaredDB), within(r.Peaks.DBBytes, r.Peaks.DeclaredDB))
	w("| data root | %s | %s | %s |\n", Bytes(r.Peaks.DiskBytes), Bytes(r.Peaks.DeclaredDisk), within(r.Peaks.DiskBytes, r.Peaks.DeclaredDisk))
	w("| ffmpeg+python CPU | %s | %.1f%% | %s |\n",
		cpuCell(r.Peaks.CPUPercent, r.Peaks.CPUMeasured), r.Peaks.DeclaredCPU,
		cpuWithin(r.Peaks.CPUPercent, r.Peaks.CPUMeasured, r.Peaks.DeclaredCPU))
	w("| AI p99 | %.0f ms | %.0f ms | %s |\n", r.Peaks.AIP99MS, r.Peaks.DeclaredAIMs,
		within(int64(r.Peaks.AIP99MS), int64(r.Peaks.DeclaredAIMs)))
	w("\n")

	if len(r.Warnings) > 0 {
		w("## Warnings\n\n")
		for _, warn := range r.Warnings {
			w("- %s\n", warn)
		}
		w("\n")
	}

	w("## What this report does not prove\n\n")
	for _, n := range r.NotProven {
		w("- %s\n", n)
	}
	w("\n")
	w("---\n\nGenerated %s by the POC's own store. Every number above was read from a row.\n",
		r.GeneratedAt.Format(time.RFC3339))
	return b.String()
}

// ProvenanceLine is the provenance sentence, printed verbatim in the header.
func (t TruthLine) ProvenanceLine() string {
	if t.Provenance == "" {
		return "provenance: UNSTATED - the numbers above came from material nobody labelled"
	}
	return "provenance: " + t.Provenance
}

func within(value, declared int64) string {
	if declared <= 0 {
		return "NOT RUN (no ceiling declared)"
	}
	if value <= declared {
		return "yes"
	}
	return "**NO**"
}

func orDash(n int) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprint(n)
}

func orUnstated(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unstated"
	}
	return s
}

// cpuCell renders a CPU measurement, or the fact that there was none. A "0.0%"
// for a measurement nobody took is a ceiling that passes on any load at all, and
// a seven-day run scored against one is a run whose CPU clause was never checked.
func cpuCell(pct float64, ok bool) string {
	if !ok {
		return "NOT RUN (no child-process CPU was measured)"
	}
	return fmt.Sprintf("%.1f%%", pct)
}

func cpuWithin(pct float64, ok bool, declared float64) string {
	if !ok {
		return "NOT RUN"
	}
	return within(int64(pct), int64(declared))
}

func dur(seconds float64) string {
	return time.Duration(seconds * float64(time.Second)).Round(time.Second).String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "|", "\\|")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// JSON renders the machine-readable form. The UI and any later analysis read
// this, not the Markdown.
func (r Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }
