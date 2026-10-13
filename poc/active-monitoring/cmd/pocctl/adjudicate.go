package main

// `pocctl adjudicate` — the machine-derivable half of the ground-truth review.
//
// # Why this exists beside `pocctl mark`
//
// Criterion 07 asks for a false-positive rate, and the rate is `FP / (TP + FP)` over
// events **a person adjudicated**. `testdata/groundtruth.json` exists for that, and
// its `$comment` forbids filling it with a number nobody looked at. This command does
// not weaken that rule and does not route around it.
//
// Two of the three committed clips have an answer that is decidable from the pixels:
// `bird-synthetic.mp4` is a drawn bird on a flat background and is offered to a
// vehicle detector, and `injection-hostile.mp4` is the adversarial fixture with the
// prompt-injection string rendered as pixels over a frame with no vehicle. Nobody has
// to have an opinion about either, so their verdicts are committed in
// `testdata/groundtruth.synthetic.json` and this applies them.
//
// # The three refusals, and each one is the point
//
//   - **A mark for footage a person has to look at.** `car-crossing.mp4` is not in the
//     synthetic file, and adding it would be asserting an answer about a real car on a
//     real road. The file's own `$comment` says which clips qualify and why.
//   - **A mark for an event this run never pushed.** Resolution goes through
//     `TagsForClip`, which joins outbox → detection → run, and an empty resolution is
//     reported as unresolved and NOT counted. So the adjudicator can only mark an
//     event that actually left the process, which is the same join `pocctl mark` uses.
//   - **A verdict outside the closed set.** `MarkGroundTruth` refuses it by name, and
//     this layer checks the FILE first so a typo in a committed file is named at the
//     file rather than at the twentieth tag it reached.
//
// # What it prints
//
// The resolution table, then the precision with its provenance NAMED. A rate printed
// without its source is a number nobody can check, and this repository's evidence
// rule is that a number names the file it came from. It also says out loud that this
// is the machine half and that a person's review of the same run is a different
// number — the two are printed separately and never summed, because summing them
// produces a rate that belongs to neither.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// syntheticMark is one committed answer.
type syntheticMark struct {
	Clip         string `json:"clip"`
	Verdict      string `json:"verdict"`
	Why          string `json:"why"`
	ReviewableBy string `json:"reviewable_by"`
}

// syntheticReview is the committed file's shape.
type syntheticReview struct {
	Comment []string        `json:"$comment"`
	Schema  int             `json:"schema"`
	Source  string          `json:"source"`
	Marks   []syntheticMark `json:"marks"`
}

func syntheticGroundTruthFile() string {
	return filepath.Join(repoRoot(), "testdata", "groundtruth.synthetic.json")
}

// loadSyntheticReview reads the file and refuses a shape it has not seen.
//
// `DisallowUnknownFields` for the reason `applyGroundTruth` uses it: a misspelled
// key would otherwise decode to an empty mark list and the command would report
// "no clip resolved" — a sentence nobody could act on, because the file was there
// and full.
func loadSyntheticReview(path string) (*syntheticReview, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var rv syntheticReview
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rv); err != nil {
		return nil, fmt.Errorf("%s is not a review this build understands: %w", path, err)
	}
	if rv.Schema != 1 {
		return nil, fmt.Errorf("%s declares schema %d and this build reads 1. Refusing rather than guessing at a shape it has never seen",
			path, rv.Schema)
	}
	for i, m := range rv.Marks {
		if !store.ValidGroundTruth(m.Verdict) {
			return nil, fmt.Errorf("%s mark %d (%s) says %q, which is neither %s nor %s. A committed answer that cannot be counted would be a number nobody could read back",
				path, i, m.Clip, m.Verdict, store.GroundTruthTruePositive, store.GroundTruthFalsePositive)
		}
		if strings.TrimSpace(m.Why) == "" {
			// A verdict with no reason is a verdict nobody can check, and this file's
			// entire claim to being different from the human one is that each answer
			// carries its derivation.
			return nil, fmt.Errorf("%s mark %d (%s) carries no `why`. This file's claim to being "+
				"machine-definable is that the reason is in the frame; an answer with no reason "+
				"is just an assertion", path, i, m.Clip)
		}
	}
	return &rv, nil
}

func cmdAdjudicate(args []string) error {
	flags, err := flagSet("adjudicate", map[string]string{
		"data-root": "", "run": "", "by": "", "dry-run": "",
	}, args)
	if err != nil {
		return err
	}
	root := flags["data-root"]
	if root == "" {
		root = defaultDataRoot()
	}
	if root == "" {
		return fmt.Errorf("--data-root is required; a review written to a guessed location is a review nobody will find")
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
			return fmt.Errorf("this store has no runs, so there is nothing to adjudicate; pass --data-root for the run you mean")
		}
		runID = run.ID
	}

	path := syntheticGroundTruthFile()
	rv, err := loadSyntheticReview(path)
	if err != nil {
		return err
	}
	by := strings.TrimSpace(flags["by"])
	if by == "" {
		// The author is never optional here either. `mark` requires it for the same
		// reason: a rate with no author is a number somebody typed, and nothing in the
		// report can then say whether it came from somebody who looked or somebody
		// who guessed. Here the author is this command, and the note says WHY it is
		// allowed to sign — the answer was in the frame.
		by = "synthetic"
	}
	dryRun := strings.EqualFold(strings.TrimSpace(flags["dry-run"]), "true")

	fmt.Printf("adjudicate: run %s\n", runID)
	fmt.Printf("  source     %s\n", path)
	fmt.Printf("  provenance %s\n", rv.Source)
	fmt.Printf("\n")
	fmt.Printf("  %-26s %-14s %-9s %s\n", "CLIP", "VERDICT", "RESOLVED", "WHY")
	fmt.Printf("  %-26s %-14s %-9s %s\n", strings.Repeat("-", 26), strings.Repeat("-", 14), strings.Repeat("-", 9), strings.Repeat("-", 40))

	// Applied in a stable order, for the reason `applyGroundTruth` sorts: two runs of
	// the same review against the same run must print the same thing, and a refused
	// mark must always be the same one.
	ordered := append([]syntheticMark(nil), rv.Marks...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Clip < ordered[j].Clip })

	applied, unresolved := 0, 0
	for _, m := range ordered {
		tags, err := st.TagsForClip(ctx, runID, m.Clip)
		if err != nil {
			return fmt.Errorf("adjudicate: resolve %s: %w", m.Clip, err)
		}
		if len(tags) == 0 {
			unresolved++
			fmt.Printf("  %-26s %-14s %-9s %s\n", m.Clip, m.Verdict, "NOT FOUND",
				"no event from this clip was pushed on this run; NOT counted")
			continue
		}
		if !dryRun {
			for _, tag := range tags {
				if err := st.MarkGroundTruth(ctx, runID, tag, m.Verdict, by,
					fmt.Sprintf("synthetic: %s", m.Why)); err != nil {
					return fmt.Errorf("adjudicate: mark %s: %w", tag, err)
				}
			}
		}
		applied += len(tags)
		fmt.Printf("  %-26s %-14s %-9s %s\n", m.Clip, m.Verdict, fmt.Sprintf("%d tag(s)", len(tags)),
			truncateNote(m.Why, 52))
	}

	fmt.Printf("\n")
	if dryRun {
		fmt.Printf("dry run: nothing was written. %d mark(s) would have been applied, %d clip(s) would not have resolved.\n",
			applied, unresolved)
		return nil
	}

	tp, fp, measured, err := st.Precision(ctx, runID)
	if err != nil {
		return fmt.Errorf("adjudicate: count the review: %w", err)
	}
	if !measured {
		fmt.Println("false positives: NOT RUN: no mark resolved to an event this run pushed")
		return nil
	}
	fmt.Printf("false positives: %d/%d (precision %.2f)\n", fp, tp+fp, float64(tp)/float64(tp+fp))
	fmt.Printf("  %d mark(s) applied across %d clip(s); %d clip(s) unresolved and NOT counted\n",
		applied, len(ordered)-unresolved, unresolved)
	fmt.Printf("  provenance: %s -- %s\n", path, rv.Source)
	fmt.Printf("  THIS IS THE MACHINE-DEFINABLE HALF. A person's review of this run is a different\n")
	fmt.Printf("  number, recorded with `pocctl mark` and reported by `pocctl report`. The two are\n")
	fmt.Printf("  printed separately and never summed, because summing them produces a rate that\n")
	fmt.Printf("  belongs to neither.\n")
	return nil
}

func truncateNote(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
