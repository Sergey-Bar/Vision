package main

import (
	"context"
	"os/exec"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

func exec2(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// reportFor rebuilds the report from the store. It shares the scoring with the
// live report, so a regenerated report and the one written at the end of a run
// cannot disagree.
func reportFor(st *store.Store, run *store.Run, c context.Context) (soak.Report, error) {
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
	truth := soak.BuildTruth(run.SourceKind, run.PushBaseURL, "xpsim", run.PushIsVMS, in.Provenance)
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
			Heartbeats: beats, Faults: len(faults)},
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
