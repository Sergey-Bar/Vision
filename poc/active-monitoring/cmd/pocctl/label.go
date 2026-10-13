package main

// `pocctl label` — mark a run's boxes, and serve the page on loopback.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sergey-Bar/vision-poc/internal/labelui"
	"github.com/Sergey-Bar/vision-poc/internal/store"
)

func cmdLabel(args []string) error {
	flags, err := flagSet("label", map[string]string{
		"run": "", "data-root": "", "addr": "127.0.0.1:8096", "by": "",
	}, args)
	if err != nil {
		return err
	}
	runID := flags["run"]
	if runID == "" {
		return fmt.Errorf("label: no run was named. Marking the wrong run's boxes produces a rate " +
			"about material nobody measured")
	}
	dataRoot := flags["data-root"]
	if dataRoot == "" {
		dataRoot = os.Getenv("SP_POC_DATA_ROOT")
	}
	if dataRoot == "" {
		dataRoot = filepath.Join(".", ".demo-data")
	}
	st, err := store.Open(dataRoot)
	if err != nil {
		return fmt.Errorf("label: the data root could not be opened: %w", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	// The missing-box-log check is HERE rather than in the page, because a reviewer
	// who opens a page over a run with nothing to mark will conclude the tool is
	// broken. It is not: the run recorded no per-frame boxes, and that is a fact
	// about the run.
	proposed, err := st.ProposedBoxCount(ctx, runID)
	if err != nil {
		return fmt.Errorf("label: the proposed-box count could not be read: %w", err)
	}
	if proposed == 0 {
		fmt.Printf("label: %s has no per-frame box observations to mark.\n", runID)
		fmt.Println("  A marking session needs SP_POC_BOX_LOG_FRAMES above 0 and a run that recorded")
		fmt.Println("  them. `pocctl bench --run " + runID + "` will say the same thing and also print")
		fmt.Println("  the per-hour rate this session was supposed to produce.")
		return nil
	}

	fmt.Printf("label: %d proposed boxes in %s\n", proposed, runID)
	fmt.Println("  Accept or reject each one, and DRAW the objects the detector missed — without")
	fmt.Println("  those a false-positive rate cannot be computed and recall cannot be measured.")
	fmt.Println()
	return labelui.Serve(ctx, labelui.Options{
		RunID: runID, Store: st, Addr: flags["addr"], By: flags["by"],
		Logf: func(f string, a ...any) { fmt.Printf(f+"\n", a...) },
	})
}
