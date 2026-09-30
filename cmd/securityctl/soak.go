package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
	"github.com/Sergey-Bar/Ai-Captain/internal/soak"
)

// soakCommand is the plan's Phase 5 instrument.
//
// It exists because the plan says so, and the reason it says so is worth
// keeping in the usage text: the eight pass criteria are published *before* the
// seven-day run, so the run cannot be re-tuned until it passes. A gate whose
// criteria are written after the fact measures the author's patience.
func soakCommand() *command.Command {
	return &command.Command{
		Name:    "soak",
		Summary: "Run the long-run harness and report the eight published pass criteria",
		Usage: `soak — the long-run harness, and the chaos rehearsal.

Pushes synthetic events through ingest, detection, evaluation, the evidence store
and the AI plane, using a stub provider so it needs no key, no network and no
vendor. Then it reads eight criteria off the record it produced.

The criteria are not the harness. The harness records; Criteria reads. Nothing in
this command decides whether it passed, and every criterion is proven capable of
failing by a test that constructs a run violating it.

  --events      how many synthetic events
  --cameras     how many cameras they come from
  --retries     attempts per event; only the first may pay
  --chaos       run the four fault-injection rehearsals as well
  --json        machine-readable only

Examples:
  securityctl soak --events 5000 --retries 3
  securityctl soak --chaos --json`,
		Run: runSoak,
	}
}

func runSoak(ctx context.Context, env *command.Env, args []string) error {
	fs := flag.NewFlagSet("soak", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var (
		events  = fs.Int("events", 200, "How many synthetic events to push through.")
		cameras = fs.Int("cameras", 4, "How many cameras they come from.")
		retries = fs.Int("retries", 1, "Attempts per event. Only the first may reach the provider.")
		driftMS = fs.Int("drift-ms", 0, "Clock drift the run reports, in milliseconds.")
		driftAt = fs.Int("drift-from", 0, "Event index the drift begins at.")
		out     = fs.String("out", "", "Directory for the evidence store. Defaults to a temp dir.")
		chaos   = fs.Bool("chaos", false, "Also run the fault-injection rehearsals.")
		asJSON  = fs.Bool("json", false, "Machine-readable only.")
		fixture = fs.String("fixture", "testdata/poc/testsrc.h264",
			"An elementary stream to put through the evidence store. Empty to skip that path.")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return &command.UsageError{Err: fmt.Errorf("soak takes no positional arguments; got %q", fs.Arg(0))}
	}
	if *events < 1 {
		return &command.UsageError{Err: fmt.Errorf("--events must be at least 1")}
	}
	if *retries < 1 {
		// A retry count of zero is a soak that never tries, which is not a
		// smaller soak; it is a different command.
		return &command.UsageError{Err: fmt.Errorf("--retries must be at least 1; a run that never retries cannot test that a retry does not pay")}
	}

	work := *out
	if work == "" {
		dir, err := os.MkdirTemp("", "securityctl-soak-")
		if err != nil {
			return fmt.Errorf("soak: create a working directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		work = dir
	}
	store, err := evidence.New(evidence.Options{Root: filepath.Join(work, "evidence")})
	if err != nil {
		return fmt.Errorf("soak: open the evidence store: %w", err)
	}

	opts := soak.Options{
		Events: *events, Cameras: *cameras, Retries: *retries,
		DriftMS:        int32(*driftMS), //nolint:gosec // a drift figure is milliseconds
		DriftFromEvent: *driftAt,
		Answer:         soak.AnswerConfirm,
		// The kills the run *reports* are the ones the evidence store's own
		// durability test exercises, because those are the kills this run
		// actually performed. Claiming a kill here that nothing performed would
		// make the durability criterion's "not exercised" line disappear and the
		// green mean nothing.
		Kills: []string{"mid-receive", "mid-mux", "pre-commit"},
	}
	if *fixture != "" {
		raw, err := os.ReadFile(*fixture)
		if err != nil {
			return fmt.Errorf("soak: read --fixture: %w", err)
		}
		opts.Fixture = raw
	}

	// A scripted provider: the run must contain a failure period as well as a
	// healthy one, because a soak that only ever sees success has not tested the
	// behaviour that costs money.
	provider := soak.NewStub(soak.AnswerSequence(soak.AnswerConfirm, *events/2, soak.AnswerConfirm)...)
	run := soak.NewRunner(store, provider, opts).Run(ctx)
	summary := soak.Summarise(run)

	var rehearsals []soak.RehearsalResult
	if *chaos {
		rehearsals = soak.Rehearse(ctx, 20)
	}

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		payload := map[string]any{"soak": summary, "chaos": rehearsals}
		if err := enc.Encode(payload); err != nil {
			return err
		}
	} else {
		fmt.Fprint(env.Stdout, soak.Report(run))
		printRehearsals(env, rehearsals)
		printSoakCaveats(env, *fixture != "", store)
	}

	if !soak.AllPassed(summary) {
		return command.Refuse("soak: at least one published criterion did not pass")
	}
	if *chaos {
		for _, r := range rehearsals {
			if !r.AsExpected {
				return command.Refuse("soak: the %s rehearsal did not behave as published", r.ID)
			}
		}
	}
	return nil
}

func printRehearsals(env *command.Env, results []soak.RehearsalResult) {
	if len(results) == 0 {
		return
	}
	fmt.Fprintln(env.Stdout, "\nCHAOS REHEARSAL")
	for _, r := range results {
		mark := "as expected"
		if !r.AsExpected {
			mark = "DID NOT BEHAVE AS PUBLISHED"
		}
		fmt.Fprintf(env.Stdout, "  %s — %s\n", r.ID, r.Component)
		fmt.Fprintf(env.Stdout, "    expected: %s\n", r.Expect)
		fmt.Fprintf(env.Stdout, "    observed: %s (%s)\n", r.Observed, mark)
		if len(r.Degradations) > 0 {
			var parts []string
			for reason, n := range r.Degradations {
				parts = append(parts, fmt.Sprintf("%s=%d", reason, n))
			}
			fmt.Fprintf(env.Stdout, "    degradations: %s\n", strings.Join(parts, " "))
		}
	}
}

// printSoakCaveats says plainly what the run did not cover.
//
// A soak that prints only its score reads as a clean bill of health for a
// system, and the things it skipped are the things a reader needs to know it
// skipped.
func printSoakCaveats(env *command.Env, usedFixture bool, store *evidence.Store) {
	fmt.Fprintln(env.Stdout, "\nWHAT THIS RUN DID NOT COVER")
	caveats := []string{
		"the detector, the muxer and the pre-roll are not exercised: this run starts at ingest. " +
			"Those have their own tests, and the muxer is read back by an independent decoder there.",
		"the kill points are reported as exercised because the evidence store's durability test " +
			"performs them. This run did not kill a process; it recorded that the test does.",
		"the provider is a stub with scripted answers. It is not a model, and a green run says " +
			"nothing about a model's accuracy - only about the plumbing around it.",
	}
	if !usedFixture {
		caveats = append(caveats, "no elementary stream was supplied, so the evidence store was not written to at all.")
	}
	if s := store.LastStagingSweep(); s.Removed > 0 {
		caveats = append(caveats, fmt.Sprintf(
			"the store collected %d staging file(s) left by a previous run, %.0f KiB",
			s.Removed, float64(s.Freed)/1024))
	}
	for _, c := range caveats {
		fmt.Fprintf(env.Stdout, "  - %s\n", c)
	}

}
