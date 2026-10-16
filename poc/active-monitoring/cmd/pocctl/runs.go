package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// cmdRuns lists every run in the store, newest first, with the number of
// detections it recorded.
//
// It exists because the store has held a run history since the first soak and the
// only ways to read it were `soak status` (the newest run) and `report` (one run at
// a time). A window that shows runs has to be able to ask for the list, and it has
// to be able to ask without opening the database itself: the numbers it prints come
// from the store's own rows, so a list and a report cannot disagree about what
// happened.
//
//	pocctl runs --data-root <root> [--json]
func cmdRuns(args []string) error {
	flags, err := flagSet("runs", map[string]string{"data-root": "", "json": boolSpec}, args)
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
	runs, err := st.ListRuns(ctx)
	if err != nil {
		return err
	}
	if flags["json"] == "true" {
		b, err := jsonMarshalIndent(runsJSON(root, runs))
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	if len(runs) == 0 {
		fmt.Printf("no runs in %s\n", root)
		return nil
	}
	for _, r := range runs {
		fmt.Printf("%s  %s  %s  %s  detections=%d  %s\n",
			r.Run.ID, r.Run.StartedAt.UTC().Format(time.RFC3339), r.Run.Mode, r.Run.SourceKind,
			r.Detections, r.Run.Label)
	}
	fmt.Printf("%d run(s) in %s\n", len(runs), root)
	return nil
}

// runJSON is one run as the JSON surface reads it. The field order is the
// document's order, and `endedAt` is omitted rather than zeroed for a run that has
// not ended: "still running" and "ended at 0001-01-01" must not print the same.
type runJSON struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Mode          string  `json:"mode"`
	SourceKind    string  `json:"sourceKind"`
	SourceArg     string  `json:"sourceArg"`
	StartedAt     string  `json:"startedAt"`
	EndedAt       string  `json:"endedAt,omitempty"`
	TargetSeconds float64 `json:"targetSeconds"`
	Note          string  `json:"note"`
	Detections    int     `json:"detections"`
}

type runsJSONDoc struct {
	DataRoot    string    `json:"dataRoot"`
	GeneratedAt string    `json:"generatedAt"`
	Count       int       `json:"count"`
	Runs        []runJSON `json:"runs"`
}

// runsJSON assembles the document. `Runs` is built with make so an empty store
// prints `"runs": []` and never `null`: a consumer that has to handle both is a
// consumer that will handle one.
func runsJSON(root string, runs []store.RunSummary) runsJSONDoc {
	doc := runsJSONDoc{
		DataRoot:    root,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Count:       len(runs),
		Runs:        make([]runJSON, 0, len(runs)),
	}
	for _, r := range runs {
		row := runJSON{
			ID:            r.Run.ID,
			Label:         r.Run.Label,
			Mode:          r.Run.Mode,
			SourceKind:    r.Run.SourceKind,
			SourceArg:     r.Run.SourceArg,
			StartedAt:     r.Run.StartedAt.UTC().Format(time.RFC3339),
			TargetSeconds: r.Run.TargetSeconds,
			Note:          r.Run.Note,
			Detections:    r.Detections,
		}
		if r.Run.EndedAt != nil {
			row.EndedAt = r.Run.EndedAt.UTC().Format(time.RFC3339)
		}
		doc.Runs = append(doc.Runs, row)
	}
	return doc
}
