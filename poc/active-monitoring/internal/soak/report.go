package soak

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
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

	// Detector is the SPLIT's own arithmetic, carried here so a run with zero
	// detections can say why.
	//
	// It is not decoration. "0 detections" has two very different causes — the
	// model found nothing, or the model found plenty and the pipeline threw it
	// away — and from the outside they print the same two characters. A report
	// that cannot distinguish them is the most expensive silence there is: it
	// reads as a quiet road when it may be a threshold that is wrong.
	Detector DetectorTally `json:"detector"`

	// CropGate is the crop gate's own arithmetic, carried here for the same
	// reason as Detector and closing the gap it did not.
	//
	// Detector answers "what did the model see". CropGate answers "when did we
	// decide not to look, and how often" — and the second question was
	// unanswerable, because `Runner.crops` was accumulated on every frame and
	// read by nothing. The rate gate's whole comment says its GatedOut counter
	// "is the counter that says whether the rate is too low", so a counter
	// nobody printed made the claim a comment about a measurement.
	CropGate CropTally `json:"crop_gate"`

	// Verification is the per-site, per-class slice of where the run stayed
	// quiet because the AI plane did not answer. It is the other half of the
	// question CropGate asks: CropGate is when WE chose not to look, this is
	// when we asked and were not answered. Printed side by side, because either
	// one alone reads as a quiet road when what happened was a gap.
	Verification VerificationSlice `json:"verification"`
}

// DetectorTally is `internal/detect.Counters`, as the report needs it.
//
// The same shape and not the type itself: `internal/soak` imports
// `internal/detect` already, but a report struct that holds a live map from a
// live engine is a report that can change under the writer.
type DetectorTally struct {
	// Seen says the split actually ran. `Frames == 0` is NOT "the road was
	// empty" — it is "nothing was ever offered to it", and those are printed
	// differently for exactly that reason.
	Seen               bool           `json:"seen"`
	FramesAsked        int            `json:"frames_asked"`
	FramesAnswered     int            `json:"frames_answered"`
	FramesNoObjects    int            `json:"frames_no_objects"`
	FramesAbsent       map[string]int `json:"frames_absent,omitempty"`
	BoxCandidates      int            `json:"box_candidates"`
	BoxesApproved      int            `json:"boxes_approved"`
	BoxesRejected      int            `json:"boxes_rejected"`
	BoxesRejectedBy    map[string]int `json:"boxes_rejected_by_reason,omitempty"`
	ApprovedByFamily   map[string]int `json:"boxes_approved_by_family,omitempty"`
	ApprovedByClass    map[string]int `json:"boxes_approved_by_class,omitempty"`
	CandidateShareFrac float64        `json:"candidate_share,omitempty"`
	// ShareMeasured says whether CandidateShareFrac is a measurement. A zero
	// with no candidates is not a rate of nothing-to-nothing.
	ShareMeasured bool `json:"candidate_share_measured"`
}

// detectorSection is the paragraph that makes `0 detections` readable.
//
// It exists because that number has two causes that look identical from outside
// this module: the model saw nothing, or the model saw plenty and the pipeline
// refused it. The second one is a configuration mistake and the first one is a
// fact about the road, and a report that cannot tell them apart sends somebody
// to re-tune a threshold that was never wrong.
//
// Every number here is a MEASUREMENT or it says so. `NOT RUN` is printed for a
// frame count of zero, and a candidate share is printed only when there were
// candidates, because `0 out of 0` is not a rate.
func detectorSection(d DetectorTally, totalDetections int) string {
	var b strings.Builder
	b.WriteString("## What the detector did\n\n")
	if !d.Seen || d.FramesAsked == 0 {
		b.WriteString("**NOT RUN.** No frame was ever offered to the detector, so nothing here says\n")
		b.WriteString("anything about the scene. A run that asked nothing and a run that found\n")
		b.WriteString("nothing print the same `detections | 0`, and only one of them is a\n")
		b.WriteString("measurement.\n\n")
		return b.String()
	}
	fmt.Fprintf(&b, "The model was asked about **%d** frame(s) and answered **%d** of them", d.FramesAsked, d.FramesAnswered)
	if d.FramesNoObjects > 0 {
		fmt.Fprintf(&b, " (%d answered with an empty array, which is a real answer and not a failure)", d.FramesNoObjects)
	}
	b.WriteString(".\n\n")
	if len(d.FramesAbsent) > 0 {
		b.WriteString("Frames with no usable answer, by state: ")
		b.WriteString(joinCounts(d.FramesAbsent))
		b.WriteString(". **These are not frames with zero objects** — a frame the model never\n")
		b.WriteString("answered cannot contribute to a detection rate, and folding it in would\n")
		b.WriteString("make a dead sidecar look like a quiet road.\n\n")
	}
	fmt.Fprintf(&b, "| | count |\n|---|---|\n")
	fmt.Fprintf(&b, "| raw candidates read out of the model | %d |\n", d.BoxCandidates)
	fmt.Fprintf(&b, "| boxes APPROVED for tracking | %d |\n", d.BoxesApproved)
	fmt.Fprintf(&b, "| boxes rejected | %d |\n\n", d.BoxesRejected)
	if d.ShareMeasured {
		fmt.Fprintf(&b, "The approved share of raw candidates was **%.4f**.\n\n", d.CandidateShareFrac)
	}
	if len(d.BoxesRejectedBy) > 0 {
		b.WriteString("Why boxes were rejected:\n\n| reason | count |\n|---|---|\n")
		for _, k := range sortedKeys(d.BoxesRejectedBy) {
			fmt.Fprintf(&b, "| `%s` | %d |\n", k, d.BoxesRejectedBy[k])
		}
		b.WriteString("\n")
	} else if d.BoxCandidates > 0 {
		b.WriteString("No box was rejected for any reason.\n\n")
	}
	if len(d.ApprovedByFamily) > 0 {
		b.WriteString("Approved boxes by family:\n\n| family | count |\n|---|---|\n")
		for _, k := range sortedKeys(d.ApprovedByFamily) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, d.ApprovedByFamily[k])
		}
		b.WriteString("\n")
	}
	// The one sentence that closes the section, and it is a question rather than
	// a verdict because this module does not know which of the two causes it is.
	if d.BoxesApproved == 0 && d.BoxCandidates > 0 {
		b.WriteString("**The model found objects and the pipeline approved none of them.** That is a\n")
		b.WriteString("configuration problem — the threshold, the approved classes, or the minimum\n")
		b.WriteString("box area — and NOT an empty road. Fix the configuration; lowering a threshold\n")
		b.WriteString("until synthetic material fires is the same mistake with a different number.\n\n")
	} else if d.BoxCandidates == 0 {
		b.WriteString("The model produced no candidates at all on any frame. That is either a scene\n")
		b.WriteString("with nothing in it, or a decode that discarded everything — and the decode\n")
		b.WriteString("telemetry on the screen is what tells the two apart.\n\n")
	} else if d.BoxesApproved > 0 && totalDetections == 0 {
		b.WriteString("Boxes were approved and tracked, and nothing crossed the line. A line in the\n")
		b.WriteString("wrong place produces zero events with no error at all, so this is the result a\n")
		b.WriteString("wrong zone file produces — check the line against a still of THIS source.\n\n")
	}
	return b.String()
}

// CropGateSectionForTest renders the crop gate section for callers that cannot
// reach the unexported one. It exists because the section's honesty is the part
// worth testing — a share, a NOT RUN for an unmeasured run, and the number to
// change when the share is high — and none of that is reachable through a struct
// field. It is the same function the report calls.
func CropGateSectionForTest(c CropTally) string { return cropGateSection(c) }

// MarkdownForTest renders the whole report, for the same reason: a caller that
// cannot reach the renderer has no way to assert where a section lands in it, and
// "the section exists in the file" is a weaker claim than "the demo shows it".
func MarkdownForTest(r Report) string { return r.Markdown() }

// cropGateSection is the second half of the question the detector section asks.
//
// `detectorSection` says what the model was ASKED and what it answered. This one
// says how often the run decided not to ask — and it exists because
// `Runner.crops` was accumulated under a lock on every frame and read by nothing
// at all, for the whole life of this code. The rate gate's own comment says its
// gated-out counter "is the counter that says whether the rate is too low", so the
// number that answers whether the camera is looking often enough was a field
// nobody printed.
//
// A share and not a count, for the same reason every other section here uses one:
// 40 gated-out frames means nothing on its own, and 40 out of 42 is a camera that
// is not looking most of the time. Both facts reach this function; only the second
// one is actionable.
func cropGateSection(c CropTally) string {
	var b strings.Builder
	b.WriteString("## When the run decided not to look\n\n")
	if !c.Seen || c.Frames == 0 {
		b.WriteString("**NOT RUN.** The crop pass was never offered a frame, so nothing here says\n")
		b.WriteString("anything about the rate. A run that gated every frame and a run that\n")
		b.WriteString("never ran the pass print the same silence, and only one of them is a\n")
		b.WriteString("configuration problem.\n\n")
		return b.String()
	}
	if c.ShareMeasured {
		fmt.Fprintf(&b, "The novelty RATE gate declined to run the crop pass on **%d** of **%d**\n",
			c.GatedOut, c.Proposed+c.GatedOut)
		fmt.Fprintf(&b, "offered frame(s) — **%.1f%%** of the run.\n\n", 100*c.GatedOutFrac)
		if c.GatedOutFrac > 0.5 {
			b.WriteString("**More than half the frames were gated out.** That is the number that says\n")
			b.WriteString("whether the rate is too low: a gate skipping most of the frames cannot\n")
			b.WriteString("propose regions often enough for the 1.5 s dwell the crop policy needs, and\n")
			b.WriteString("the only other symptom is the smallest objects quietly going missing —\n")
			b.WriteString("which is invisible in every other number in this report. `--ai-fps 1` is the\n")
			b.WriteString("known-good value on this machine; anything under it hits the dwell floor.\n\n")
		}
	} else {
		fmt.Fprintf(&b, "The crop pass ran on **%d** frame(s) and nothing was gated out, so there is\n", c.Proposed)
		b.WriteString("no share to report — every frame offered was taken.\n\n")
	}
	fmt.Fprintf(&b, "| | count |\n|---|---|\n")
	fmt.Fprintf(&b, "| crops proposed and taken | %d |\n", c.Proposed)
	fmt.Fprintf(&b, "| proposed and refused: overlap | %d |\n", c.SkippedOverlap)
	fmt.Fprintf(&b, "| proposed and refused: below the minimum area | %d |\n", c.SkippedSmall)
	fmt.Fprintf(&b, "| proposed and refused: the cap was reached | %d |\n", c.SkippedCap)
	fmt.Fprintf(&b, "| regions inferred from boxes | %d |\n", c.Inferred)
	fmt.Fprintf(&b, "| frames declaring a view change | %d |\n", c.ViewChanges)
	fmt.Fprintf(&b, "| frames inside the settling window | %d |\n", c.ViewSettling)
	fmt.Fprintf(&b, "| the detector refused a crop | %d |\n", c.Refusals)
	b.WriteString("\n")
	if c.SmallMeasured && c.SmallShareFrac > 0.25 {
		fmt.Fprintf(&b, "**%.1f%% of proposed crops were refused as below the minimum area.** A large\n", 100*c.SmallShareFrac)
		b.WriteString("number here is a fact about the material and not about the camera: the\n")
		b.WriteString("policy will not take a region that small, and the regions that size are\n")
		b.WriteString("the whole point of a crop pass. `detector.crop_min_area` is the number to\n")
		b.WriteString("change, and the calibration script that derives it is in `scripts/`.\n\n")
	}
	if c.KeptMeasured && c.KeptShareFrac < 0.5 && c.BoxesFound > 4 {
		fmt.Fprintf(&b, "Only **%.1f%%** of crop boxes survived deduplication against the whole-frame\n", 100*c.KeptShareFrac)
		b.WriteString("boxes, which means the two passes disagree about the same object. That is\n")
		b.WriteString("not a failure — it is the fact a crop pass is supposed to find — and the\n")
		b.WriteString("share is what says how much of it there is.\n\n")
	}
	if c.Seen && c.Proposed == 0 && c.GatedOut == 0 {
		b.WriteString("**The crop pass ran and proposed nothing.** Either the novelty engine\n")
		b.WriteString("never fired, or it fired and the zone has no boxes to build regions from.\n")
		b.WriteString("The novelty engine's own numbers are on the screen and in its section.\n\n")
	}
	// The pairing that makes this section worth having.
	//
	// Both of these are true at once on a run with a degraded detector, and before
	// this section existed the crop pass looked idle while the reason sat in a
	// different section of the same report. A crop refused by the detector is not a
	// crop the gate declined: the pass proposed, and the model could not answer.
	if c.Refusals > 0 && c.Refusals >= c.Proposed {
		fmt.Fprintf(&b, "**The detector refused all %d crop(s) the pass proposed.** That is not the\n", c.Refusals)
		b.WriteString("rate gate declining to look — it is the pass proposing and the model being\n")
		b.WriteString("unable to answer, which on this pipeline is what a degraded detector looks\n")
		b.WriteString("like from here. The detector section above says how many frames it could\n")
		b.WriteString("not answer at all; the two numbers are the same fact seen from two places.\n\n")
	}
	return b.String()
}

func joinCounts(m map[string]int) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, fmt.Sprintf("`%s` %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}

// TruthLine is the sentences that make the report honest, generated from the run's
// own configuration and its own records rather than written by the caller.
//
// W3-6: it now carries the PROOF beats as well as the provenance beats. A report
// that says where the footage came from and where the events went is honest about
// its INPUTS; one that also says whether its own ledger verifies, whether anybody
// attested to it, and whether a decision replays is honest about its OWN EVIDENCE.
// Those are different claims and a reader who wants the second one should not have
// to run a command to find it.
type TruthLine struct {
	Source     string `json:"source"`
	PushTarget string `json:"push_target"`
	Provenance string `json:"provenance"`

	// Ledger is the chain over this run's rows: `valid`, `BROKEN at <seq>`, or
	// `NOT RUN — the ledger is empty`.
	Ledger string `json:"ledger"`
	// Seal is the attestation over the chain's head, and it is deliberately a
	// separate word from Ledger: a chain that verifies is not the same claim as a
	// chain somebody signed.
	Seal string `json:"seal"`
	// Replay is the newest detection recomputed from stored facts, with the number
	// of provider calls that took.
	Replay string `json:"replay"`
	// Model is who verified the events, and `none` is printed as `none`: an
	// unverified proposal is a real product and it must never read as a verified
	// incident.
	Model string `json:"model"`
}

// BuildTruth produces the provenance half of the truth line. Against the simulator
// it says so, word for word, every time. There is no code path that produces a
// "reached Milestone" claim without an explicit non-loopback base URL.
func BuildTruth(sourceLabel, pushBaseURL, pushName string, isVMS bool, provenance string) TruthLine {
	t := TruthLine{Source: sourceLabel, Provenance: provenance}
	if isVMS && pushBaseURL != "" {
		t.PushTarget = fmt.Sprintf("%s (%s)", pushName, pushBaseURL)
		return t
	}
	t.PushTarget = "push target: local simulator (xpsim). No XProtect server was reachable during this run."
	return t
}

// WithProof fills the four proof beats from the run's own records.
//
// It takes a store and a replay report rather than booleans, because a caller who
// passes booleans will pass `true` for a field it did not check, and a proof line
// on a report is worth exactly as much as the code that produced it.
//
// Every field is a FACT with a fallback that says it was not measured. A missing
// seal is `NOT SEALED` and never an empty string; an unrun replay is `NOT RUN` and
// never a pass.
func (t TruthLine) WithProof(st *store.Store, ctx context.Context, runID, model string) TruthLine {
	if model == "" {
		model = "none"
	}
	t.Model = model
	if st == nil {
		t.Ledger = "NOT RUN — the report has no store to verify against"
		t.Seal = "NOT RUN"
		t.Replay = "NOT RUN"
		return t
	}
	rep, err := st.VerifyLedger(ctx, 0, 0)
	switch {
	case err != nil:
		t.Ledger = "NOT RUN — " + err.Error()
	case rep.Rows == 0:
		t.Ledger = "NOT RUN — the ledger is empty, and an empty chain is not an intact one"
	case rep.Valid:
		t.Ledger = fmt.Sprintf("valid — %d row(s) recomputed", rep.Rows)
	default:
		t.Ledger = fmt.Sprintf("BROKEN at sequence %d — %s", rep.BrokenAt, rep.Reason)
	}

	// The seal is looked up by RUN, and the run is the report's own. A seal for
	// another run is not this run's evidence and saying so would be the same class
	// of error as reading another camera's verdict.
	seal, sealErr := store.ReadSeal(st.SealPath(runID))
	switch {
	case sealErr != nil:
		t.Seal = "NOT SEALED — nobody has attested that this chain is what it says"
	default:
		sr, err := st.VerifySeal(ctx, seal, nil)
		switch {
		case err != nil:
			t.Seal = "UNREADABLE — " + err.Error()
		case sr.Accepted:
			t.Seal = fmt.Sprintf("signed by key %s over %d row(s), head matches", seal.KeyID, seal.Rows)
		case !sr.SignatureOK:
			t.Seal = "BROKEN — " + sr.Detail
		case !sr.ChainOK:
			t.Seal = "STALE — the chain no longer verifies: " + sr.Detail
		default:
			t.Seal = "STALE — " + sr.Detail
		}
	}

	run, err := st.LatestRun(ctx)
	if err != nil || run == nil {
		t.Replay = "NOT RUN — this report is not about a run in this database"
		return t
	}
	recent, err := st.Recent(ctx, run.ID, 1)
	if err != nil || len(recent) == 0 {
		t.Replay = "NOT RUN — the run has no detections, so there is nothing to replay"
		return t
	}
	t.Replay = fmt.Sprintf("%s — replay `pocctl prove %s`", run.ID, recent[0].ID)
	return t
}

// ProofLine is the proof beats as one sentence for a console.
func (t TruthLine) ProofLine() string {
	return fmt.Sprintf("ledger: %s · seal: %s · model: %s · replay: %s", t.Ledger, t.Seal, t.Model, t.Replay)
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
	// W3-6: the proof beats, in the same block as the provenance beats, because
	// they are the same kind of sentence — a claim about this report that a reader
	// is entitled to check.
	w("```\n%s\n```\n\n", r.Truth.ProofLine())
	w("`pocctl audit ledger` recomputes the chain, `pocctl audit seals --trusted-key <f>` verifies\n")
	w("the signature, and `pocctl prove <detection>` replays one decision from stored facts with\n")
	w("zero provider calls. A beat above that says NOT RUN means that command was not run, not\n")
	w("that it would have passed.\n\n")

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
	w("%s", detectorSection(r.Counters.Detector, r.Counters.Detections))
	// The verification slice comes BEFORE the crop gate, and the order is the
	// reading order: the detector section says what the model saw, this says
	// which of those sightings the plane was asked about and answered, and the
	// crop gate says how often we chose not to look at all. A reader who stops
	// after the crop gate has missed nothing, because the two sections it sits
	// between are the ones that make it mean something.
	w("%s", verificationSliceSection(r.Counters.Verification))
	w("%s", cropGateSection(r.Counters.CropGate))

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
