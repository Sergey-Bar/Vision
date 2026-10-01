package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
)

// cmdBrief is one line per hour for a run.
//
// It is the surface a judge asks for after the demo, and it exists because the two
// things that are easy to show and hard to hide — "it ran for six hours" and "the
// model actually looked" — are otherwise scattered across a store with no reader.
//
// The discipline is entirely in the hour classification. Four different things stop
// printing the same way, and three of them are not "nothing happened":
//
//   - no detections, frames > 0  — the stream was up and the detector found nothing
//   - no detections, frames = 0  — the stream was DOWN (frames is 0, not absent)
//   - detections, none asked    — the model was never consulted about what fired
//   - detections, all cleared   — the model WAS asked and said no
//
// The second and third are the ones that get conflated with the first in every
// incident tool ever built, and both are fabrications: a dead stream is not a quiet
// one, and an unasked detection is not a cleared one.
func cmdBrief(args []string) error {
	flags, err := flagSet("brief", map[string]string{
		"data-root": "", "run": "", "from": "", "to": "",
	}, args)
	if err != nil {
		return err
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

	beats, err := st.HourlyLiveness(ctx, runID)
	if err != nil {
		return err
	}
	if len(beats) == 0 {
		return fmt.Errorf("run %s recorded no heartbeats at all, so there is no hour to brief. A run that never reported "+
			"liveness cannot be said to have been alive", runID)
	}

	fmt.Printf("brief: run %s, %d hour(s) from heartbeats\n\n", runID, len(beats))
	fmt.Printf("%-6s  %8s  %5s  %-28s\n", "hour", "frames", "hits", "what happened")
	for _, h := range beats {
		fmt.Printf("%-6s  %8d  %5d  %s\n", h.Hour.UTC().Format("15:04"), h.Frames, h.Proposed, hourSentence(h))
	}

	// The answered alarm summaries, under the table. They are the judge's question
	// ("what did the model actually say") and the table is not the place for prose.
	answers, err := st.BriefAnswers(ctx, runID, from, to, 20)
	if err != nil {
		return err
	}
	if len(answers) > 0 {
		fmt.Println()
		fmt.Printf("what the model said (%d most recent answered alarm%s)\n", len(answers), plural(len(answers)))
		for _, a := range answers {
			fmt.Printf("  %s  %-14s %s\n", a.At.UTC().Format(time.RFC3339), a.Verdict, a.Summary)
			fmt.Printf("      box (%.3f,%.3f)-(%.3f,%.3f)  score %.2f  evidence %s\n",
				a.X1, a.Y1, a.X2, a.Y2, a.Score, a.StorageRef)
		}
	}
	fmt.Println()
	fmt.Println("An hour with no detections and no frames is a dead stream, not a quiet one.")
	fmt.Println("An hour with detections that were never asked is not a cleared hour.")
	return nil
}

// hourSentence is the whole point of this command, in one function.
//
// Every branch names what the STORE says and nothing it does not. In particular a
// zero-frame hour says the stream was down rather than the detector was quiet,
// because a heartbeat that read zero frames is a liveness sample from a process
// watching nothing, and reading it as "no events" is a claim about the camera's
// safety that nobody measured.
func hourSentence(h store.HourBeat) string {
	switch {
	case h.Proposed == 0 && h.Frames == 0:
		return "stream was DOWN: heartbeats with zero frames"
	case h.Proposed == 0:
		return "stream up, detector found nothing (never asked: nothing to ask)"
	case h.Answered == 0:
		return fmt.Sprintf("%d detection(s), model NEVER ASKED: not asked", h.Proposed)
	case h.Answered < h.Proposed:
		return fmt.Sprintf("%d detection(s), %d asked and %d never asked", h.Proposed, h.Answered, h.Proposed-h.Answered)
	case h.Confirmed > 0:
		return fmt.Sprintf("%d detection(s), model asked, %d confirmed", h.Proposed, h.Confirmed)
	case h.NeedsReview > 0:
		return fmt.Sprintf("%d detection(s), model asked and cleared all but %d needs_review", h.Proposed, h.NeedsReview)
	default:
		return fmt.Sprintf("%d detection(s), model asked and CLEARED them all", h.Proposed)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

var _ = context.Background
