package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// cmdMark records one person's verdict on one event this run pushed.
//
// It exists because the plumbing for a false-positive rate was already in the store
// and had no way in from a keyboard. `pocctl report` applies a review FILE, which
// is right for a regression and wrong for a first look: the moment a rate is first
// wanted, it is usually wanted about one event, mid-afternoon, and the alternative
// was editing a JSON file by hand with the tag pasted out of a console.
//
// Three properties are load-bearing and all three are about what the printed line
// may claim:
//
//   - The tag is a POSITIONAL argument, not a flag. It is the one value that
//     identifies what is being judged, and a flag makes it the easiest thing to
//     mistype and the easiest thing to omit.
//   - The mark is refused by the store when the run never pushed that tag. The
//     refusal names a tag the run DID push, because a refusal nobody can act on is
//     a refusal nobody will act on.
//   - `Precision` is printed as NOT RUN until somebody has marked something. It is
//     never a zero: nobody looking is not the same claim as nobody being wrong.
func cmdMark(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("mark needs the TAG of an event this run pushed, as its first argument: pocctl mark <tag> --verdict false_positive --by <your name>")
	}
	tag := strings.TrimSpace(args[0])
	flags, err := flagSet("mark", map[string]string{
		"data-root": "", "run": "", "verdict": "", "by": "", "note": "",
	}, args[1:])
	if err != nil {
		return err
	}
	verdict := strings.TrimSpace(flags["verdict"])
	switch verdict {
	case store.GroundTruthTruePositive, store.GroundTruthFalsePositive:
	case "":
		return fmt.Errorf("--verdict is required and must be %s or %s. The default would be a rate that nobody measured",
			store.GroundTruthTruePositive, store.GroundTruthFalsePositive)
	default:
		return fmt.Errorf("--verdict %q is not %s or %s; a review with a third value cannot be counted, and one that cannot be "+
			"counted silently is worse than one that is refused", verdict, store.GroundTruthTruePositive, store.GroundTruthFalsePositive)
	}
	by := strings.TrimSpace(flags["by"])
	if by == "" {
		return fmt.Errorf("--by is required. A rate with no author is a number somebody typed, and nothing in the report can say " +
			"whether that number came from a person who looked or from a person who guessed")
	}

	root := flags["data-root"]
	if root == "" {
		root = defaultDataRoot()
	}
	if root == "" {
		return fmt.Errorf("--data-root is required; a mark written to a guessed location is a mark nobody will find")
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
			return fmt.Errorf("this store has no runs, so there is nothing to mark; pass --data-root for the run you mean")
		}
		runID = run.ID
	}

	// The refusal for a tag this run never pushed comes from the store, not from
	// here. This layer decides nothing: a second implementation of the join is a
	// second thing to keep consistent with the first.
	if err := st.MarkGroundTruth(ctx, runID, tag, verdict, by, strings.TrimSpace(flags["note"])); err != nil {
		return err
	}
	// The count is printed from the STORE's arithmetic rather than from anything
	// derived here. `mark` holds no state of its own, so two marks made in either
	// order produce the same line.
	tp, fp, measured, err := st.Precision(ctx, runID)
	if err != nil {
		return err
	}
	fmt.Printf("marked %s as %s by %s, run %s\n", tag, verdict, by, runID)
	if !measured {
		// Unreachable through this path — one mark is one measured mark — and it is
		// printed rather than assumed, because the alternative is a caller that adds
		// a second entry point later and forgets this branch.
		fmt.Println("precision: NOT RUN: nothing was counted, and NOT RUN is not 0")
		return nil
	}
	fmt.Printf("precision: %d/%d (precision %.2f), marked by %s on %s\n",
		fp, tp+fp, float64(tp)/float64(tp+fp), by, time.Now().UTC().Format("2006-01-02"))
	return nil
}

// defaultDataRoot is the store location when the flag is absent, or "" when there
// is none to default to.
//
// It reads the environment because every other operator command does, and a tool
// that resolves the data root three different ways writes to the wrong place once.
func defaultDataRoot() string {
	return strings.TrimSpace(os.Getenv("SP_POC_DATA_ROOT"))
}

// openReadOnly opens a store for reading and refuses a path that is not there.
//
// `store.Open` creates a data root, which is right for `watch` and wrong for these
// three. A mistyped `--data-root` would otherwise be answered with a fresh empty
// store and "this store has no runs" — which reads like an honest answer about a
// run that does not exist, and is really an answer about a typo. The refusals below
// are the difference between "you asked about the wrong directory" and "there is
// nothing there", and only the first is true.
func openReadOnly(root string) (*store.Store, error) {
	if root == "" {
		return nil, fmt.Errorf("--data-root is required, or set SP_POC_DATA_ROOT")
	}
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("data root %s does not exist: %w. These commands read a run; they do not create one, and a "+
			"mistyped path would otherwise be answered with an empty store", root, err)
	}
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return st, nil
}
