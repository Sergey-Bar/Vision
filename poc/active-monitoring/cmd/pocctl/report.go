package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/soak"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// storedDetectorTally is the run's detector arithmetic, read from the store.
//
// pocctl report has no runner in memory, so this is the ONLY way it can say
// why a run produced nothing. A run with no stored tally comes back Seen:
// false and is printed as NOT RUN with the reason; it is never zeros.
func storedDetectorTally(st *store.Store, runID string) soak.DetectorTally {
	raw, found, err := st.DetectorTally(context.Background(), runID)
	if err != nil || !found {
		return soak.DetectorTally{}
	}
	var t soak.DetectorTally
	if err := json.Unmarshal(raw, &t); err != nil {
		return soak.DetectorTally{}
	}
	return t
}

// storedCropTally is the run's crop-gate arithmetic, read from the store.
//
// Its sibling above explains the shape. This one closes the gap the sibling did
// not: `Runner.crops` was accumulated on every frame and read by nothing, so
// "when did we decide not to look, and how often" — and "how many proposed crops
// were refused as too small" — existed nowhere a report could reach.
func storedCropTally(st *store.Store, runID string) soak.CropTally {
	raw, found, err := st.CropTally(context.Background(), runID)
	if err != nil || !found {
		return soak.CropTally{}
	}
	var t soak.CropTally
	if err := json.Unmarshal(raw, &t); err != nil {
		return soak.CropTally{}
	}
	return t
}

// storedVerificationSlice is the per-site, per-class slice, read from the store.
//
// Unlike its two siblings this one is not a stored blob: the cells are the
// detections themselves, grouped at read time, because a slice that was written
// once at the end of a run would be a slice about the moment it was written.
// The report's own rule is that it describes the database and not the moment,
// and this is the section where that rule matters most — the question it answers
// is "was the model being served", and the answer changes every time a
// detection's ai_state moves.
//
// An error returns an UNMEASURED slice and never a zero-filled one. The section
// prints NOT RUN, which is a visible statement that this run says nothing; a
// zero-filled slice would print "every retained detection had an answer" for a
// run where nothing was read, which is the exact sentence this section exists to
// refuse.
func storedVerificationSlice(st *store.Store, runID string) soak.VerificationSlice {
	cells, cellsErr := st.VerificationStatesBySiteAndClass(context.Background(), runID)
	byState, statesErr := st.VerificationTotals(context.Background(), runID)
	total, retained, totalsErr := st.RetainedDetections(context.Background(), runID)
	if cellsErr != nil || statesErr != nil || totalsErr != nil {
		return soak.VerificationSlice{}
	}
	return soak.VerificationSliceOf(cells, byState, total, retained)
}

func exec2(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// groundTruthFile is the review, and where it is expected to be.
//
// It lives beside the MATERIAL rather than beside the run, because the marks are
// judgements about events and the event tags are content-derived from what the
// detector saw: a run in a scratch data root and a run in the demo data root
// push the same tags for the same crossings, and one review of the footage
// should serve both.
func groundTruthFile() string {
	return filepath.Join(repoRoot(), "testdata", "groundtruth.json")
}

// mark is one person's verdict about one pushed event.
//
// `schema` is CHECKED and the file's shape is DECLARED, not inferred. A review
// this build has never heard the shape of is refused rather than read with a
// best guess, because a review read the wrong way silently marks nothing and
// then reports NOT RUN for a reason that looks like nobody looked.
type mark struct {
	Tag     string `json:"tag"`
	Verdict string `json:"verdict"`
	Note    string `json:"note"`
	By      string `json:"by"`
}

type review struct {
	Schema int    `json:"schema"`
	Marks  []mark `json:"marks"`
}

// modelName is who verified this run's events, read from the environment the run
// ran under rather than from the database.
//
// `none` is printed as `none`. An unverified proposal is a real product — the local
// novelty trigger measures a region and proposes it — and it is a different thing
// from a verified incident, and the truth line is where that difference has to be
// visible.
func modelName() string {
	p := strings.TrimSpace(os.Getenv("SP_POC_VLM_PROVIDER"))
	if p == "" || strings.EqualFold(p, "none") {
		return "none"
	}
	if m := strings.TrimSpace(os.Getenv("SP_POC_VLM_MODEL")); m != "" {
		return p + " (" + m + ")"
	}
	return p
}

// applyGroundTruth loads the review, records it against the run, prints the line,
// and returns the marking for the report.
//
// It returns a `HumanMarking` rather than a number so `Verdict` can put the
// AUTHOR in the sentence: a rate with no author is a number somebody typed, and
// `soak` already refuses to print one as a measurement.
func applyGroundTruth(st *store.Store, runID string, c context.Context) *soak.HumanMarking {
	// The STORE first, always.
	//
	// It used to read only `testdata/groundtruth.json`, so the two ways a human can
	// mark an event did not talk to each other: `pocctl mark` wrote to the store and
	// the report ignored it, and the file was the only source that counted. A person
	// who marked ten events with the command beside them on the review sheet, then ran
	// the report and saw `false positives: NOT RUN`, was being told the truth about
	// the file and nothing at all about their own work.
	//
	// The file remains the fallback because it is what the regression fixtures use,
	// and because a review somebody committed is a real thing. It is second because a
	// mark made against THIS run's own pushed tags is fresher than any file on disk.
	if marks, err := st.GroundTruth(c, runID); err != nil {
		fmt.Printf("false positives: NOT RUN: the marks recorded against this run could not be read (%v)\n", err)
		return nil
	} else if len(marks) > 0 {
		return markingFromMarks(st, runID, c, marks, "this run's own recorded marks")
	}

	path := groundTruthFile()
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("false positives: NOT RUN: %s could not be read (%v)\n", path, err)
		return nil
	}
	var rv review
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rv); err != nil {
		fmt.Printf("false positives: NOT RUN: %s is not a review this build understands: %v\n", path, err)
		return nil
	}
	if rv.Schema != 1 {
		fmt.Printf("false positives: NOT RUN: %s declares schema %d and this build reads 1. Refusing rather than guessing at a shape it has never seen.\n", path, rv.Schema)
		return nil
	}
	if len(rv.Marks) == 0 {
		// An empty list is valid, and it means NOT RUN. It does NOT mean zero
		// false positives, and the printed sentence says so in full rather than
		// abbreviated, because this is the one line that keeps the report honest.
		fmt.Println("false positives: NOT RUN: no human marked any event in testdata/groundtruth.json")
		return nil
	}

	// Applied in a stable order, so two reports from the same review are
	// byte-identical and a refused mark is always the same one.
	ordered := append([]mark(nil), rv.Marks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Tag < ordered[j].Tag })

	by, note := "", ""
	applied := 0
	for _, k := range ordered {
		if err := st.MarkGroundTruth(c, runID, k.Tag, k.Verdict, k.By, k.Note); err != nil {
			fmt.Printf("  NOT APPLIED — %v\n", err)
			continue
		}
		applied++
		if by == "" {
			by, note = k.By, k.Note
		}
	}

	tp, fp, measured, err := st.Precision(c, runID)
	if err != nil {
		fmt.Printf("false positives: NOT RUN: the review could not be counted: %v\n", err)
		return nil
	}
	if !measured {
		fmt.Println("false positives: NOT RUN: no mark in testdata/groundtruth.json matched an event this run pushed")
		return nil
	}
	if applied != tp+fp {
		// The count has to be the count of what was WRITTEN, not the count of
		// what was asked for. Silently dropping a mark that matched nothing would
		// let a precision figure be printed for a smaller review than the file
		// holds, which is the failure this whole mechanism exists to prevent.
		fmt.Printf("  %d of %d mark(s) did not match an event this run pushed and were NOT counted\n", len(ordered)-applied, len(ordered))
	}
	precision := float64(tp) / float64(tp+fp)
	fmt.Printf("false positives: %d/%d (precision %.2f), marked by %s\n", fp, tp+fp, precision, orUnstated(by))
	return &soak.HumanMarking{Examined: tp + fp, False: fp, By: by, When: time.Now().UTC(), Note: note}
}

// markingFromMarks turns marks already IN THE STORE into a HumanMarking.
//
// It does not re-apply them: `MarkGroundTruth` refuses a tag the run never pushed, so
// the rows that exist are the rows that matched. It counts from the store's own
// `Precision`, which is the same arithmetic the marking command printed, so the
// number in the report and the number the person saw cannot differ.
func markingFromMarks(st *store.Store, runID string, c context.Context, marks []store.GroundTruthMark, source string) *soak.HumanMarking {
	tp, fp, measured, err := st.Precision(c, runID)
	if err != nil || !measured {
		fmt.Println("false positives: NOT RUN: the recorded marks could not be counted, so no rate is printed")
		return nil
	}
	by, note, when := "", "", time.Time{}
	for _, m := range marks {
		if by == "" {
			by, note, when = m.By, m.Note, m.At
		}
	}
	if !when.IsZero() {
		when = when.UTC()
	} else {
		when = time.Now().UTC()
	}
	precision := float64(tp) / float64(tp+fp)
	fmt.Printf("false positives: %d/%d (precision %.2f), marked by %s on %s — %d mark(s) from %s\n",
		fp, tp+fp, precision, orUnstated(by), when.Format("2006-01-02"), len(marks), source)
	return &soak.HumanMarking{Examined: tp + fp, False: fp, By: by, When: when, Note: note}
}

func orUnstated(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unstated"
	}
	return s
}

// reportFor rebuilds the report from the store. It shares the scoring with the
// live report, so a regenerated report and the one written at the end of a run
// cannot disagree.
func reportFor(st *store.Store, run *store.Run, c context.Context) (soak.Report, error) {
	// The human review, applied and counted BEFORE the criteria are scored. It is
	// not a criterion and never becomes one — `soak.Score` does not read it — but
	// it is part of the report, and a report whose false-positive row says NOT
	// RUN because nobody loaded the review file is a report that has not been
	// run, whatever the rest of it says.
	human := applyGroundTruth(st, run.ID, c)
	counts, _ := st.CountByStatus(c, run.ID)
	faults, _ := st.Faults(c, run.ID)
	beats, _ := st.HeartbeatCount(c, run.ID)
	gaps, _ := st.HeartbeatGaps(c, run.ID, 30*time.Second)
	stalled, _ := st.Stalled(c, run.ID, 5*time.Minute)
	peaks, _ := st.SamplePeaks(c, run.ID)
	push, _ := st.Stats(c, run.ID)

	var rehearsals []soak.Rehearsal
	for _, f := range faults {
		if f.Expected == "" {
			continue
		}
		rehearsals = append(rehearsals, soak.Rehearsal{Fault: f.Reason, Expected: f.Expected, Observed: f.Observed, At: f.At, Detail: f.Detail})
	}

	end := time.Now().UTC()
	if run.EndedAt != nil {
		end = *run.EndedAt
	}
	elapsed := end.Sub(run.StartedAt)
	in := soak.Inputs{
		Elapsed: elapsed, Target: time.Duration(run.TargetSeconds) * time.Second,
		RunEnded:       run.EndedAt != nil,
		HeartbeatCount: beats, ResourceSamples: peaks.Samples,
		EvidenceStored: 0, PushAttempts: push.Total, PushesWithResult: push.WithResult,
		UnattributedPushes: push.Unattributed, DeadLettered: push.DeadLetter,
		PeakRSSBytes: peaks.RSSBytes, PeakDBBytes: peaks.DB, PeakDiskBytes: peaks.DiskBytes,
		Provenance:            "see the run row; this report does not guess it",
		PushTargetIsSimulator: !run.PushIsVMS,
		SourceKind:            run.SourceKind, SourceLabel: run.SourceKind,
		FalsePositives: human,
	}
	for _, g := range gaps {
		in.HeartbeatGaps = append(in.HeartbeatGaps, soak.GapMeasurement{From: g.From, To: g.To, Seconds: g.Seconds})
	}
	for _, s := range stalled {
		in.StalledDetections = append(in.StalledDetections, soak.StalledDetection{ID: s.ID, Status: s.Status, Age: time.Since(s.UpdatedAt)})
	}
	for _, v := range counts {
		in.TotalDetections += v
	}
	criteria := soak.Score(in)
	summary := soak.Overall(in, criteria)

	rows := make([]soak.FaultRow, 0, len(faults))
	for _, f := range faults {
		v := "recorded"
		if f.Expected != "" {
			if f.Observed == f.Expected {
				v = "PASS"
			} else {
				v = "FAIL"
			}
		}
		rows = append(rows, soak.FaultRow{At: f.At, Reason: f.Reason, Detail: f.Detail,
			Expected: f.Expected, Observed: f.Observed, Verdict: v})
	}
	truth := soak.BuildTruth(run.SourceKind, run.PushBaseURL, "xpsim", run.PushIsVMS, in.Provenance).
		WithProof(st, c, run.ID, modelName())
	return soak.Report{
		GeneratedAt: time.Now().UTC(),
		Run: soak.RunInfo{ID: run.ID, Label: run.Label, Mode: run.Mode, StartedAt: run.StartedAt,
			EndedAt: run.EndedAt, TargetSecs: run.TargetSeconds, ElapsedSecs: elapsed.Seconds(),
			ClockSource: run.ClockSource, Database: st.Path()},
		Verdict: string(summary.Verdict), Provisional: summary.Provisional,
		Criteria: criteria, Summary: summary, Faults: rows, Rehearsals: rehearsals,
		Peaks: soak.Peaks{RSSBytes: peaks.RSSBytes, DBBytes: peaks.DB, DiskBytes: peaks.DiskBytes,
			CPUPercent: peaks.CPUPercent, AIP99MS: peaks.AIP99MS, Samples: peaks.Samples},
		Counters: soak.Counters{ByStatus: counts, PushAttempts: push.Total, DeadLettered: push.DeadLetter,
			Heartbeats: beats, Faults: len(faults), Detector: storedDetectorTally(st, run.ID),
			CropGate:     storedCropTally(st, run.ID),
			Verification: storedVerificationSlice(st, run.ID)},
		FalsePositives: soak.NewFalsePositiveRow(in.FalsePositives),
		Truth:          truth,
		NotProven:      soak.NotProven(in, summary),
		Warnings: []string{
			"This report was regenerated from the store. The declared ceilings and the rehearsal " +
				"outcomes are not stored as numbers, so the criteria that depend on them are scored " +
				"NOT RUN here rather than guessed.",
		},
	}, nil
}
