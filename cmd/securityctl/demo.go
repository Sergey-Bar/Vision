package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
)

// demoCommand is registered but not populated.
//
// P0 of the execution plan puts it here and P6 fills it in. Registering it now
// rather than at P6 is deliberate: a command added in P6 would arrive as a
// behavioural change, so the diff that introduces it would also be the diff that
// changes what an operator gets when they type `securityctl demo`. Registering it
// first separates "this exists" from "this does something", so the P6 diff is
// reviewable on its own.
//
// # Why it fails
//
// A skeleton that exits 0 looks like a working demo to every machine that reads
// the exit code, and this repository's own history is a list of things that looked
// like they worked because they exited 0. `slo.Breaches` was an SLO evaluator with
// a test and no caller. A verification run before this change reported exit 0
// while 273 tests skipped.
//
// So this command says what it is, lists what it will do, and fails. The failure
// is the honest answer to "did the demo run?": no, and not yet. It exits 0 with
// `--json` and `--plan`, because those two report rather than demonstrate, and a
// report that fails is a report nobody runs.
func demoCommand() *command.Command {
	return &command.Command{
		Name:    "demo",
		Summary: "RESERVED: run the product against a synthetic day. Not implemented yet",
		Usage: `demo -- RESERVED. Registered by the 2026-10-02 foundation pass, populated by P6.

What P6 makes this do, from the execution plan:

  - ingest a synthetic day of camera events through the real pipeline, not a
    fixture replay, so the clock corrector and the correlator both run
  - trigger at least one incident, dispose it through the console form, and
    show the incident closed with its hash-chained audit rows
  - prove the North Star numerator is not hand-computed: the number printed is
    the number the query returns
  - run with a stub model provider and say so on every AI-derived line, because
    a demo whose AI output is indistinguishable from a real inference is a demo
    that will be quoted as evidence
  - finish in under three minutes on one machine with no network

Until then this command reports that state and exits non-zero. Run
"securityctl demo --plan" for the acceptance criteria P6 has to meet, and read
docs/status.md for what is actually built.

  --json     machine-readable only. Exits 0: this reports rather than demonstrates.
  --plan     print the P6 acceptance criteria. Exits 0.

Examples:
  securityctl demo            # says it is reserved, exits 1
  securityctl demo --plan     # what P6 has to satisfy, exits 0
  securityctl demo --json     # machine-readable, exits 0`,
		Run: runDemo,
	}
}

// demoState is the machine-readable form.
//
// `populated` is a field rather than the absence of one, because a consumer
// reading JSON has to be able to tell "not implemented" from "implemented and
// reported nothing". A report that omits the distinction is the same defect as a
// green run that tested nothing.
type demoState struct {
	Command   string   `json:"command"`
	Populated bool     `json:"populated"`
	Phase     string   `json:"phase"`
	Summary   string   `json:"summary"`
	WillDo    []string `json:"will_do,omitempty"`
	Criteria  []string `json:"criteria,omitempty"`
	Evidence  string   `json:"evidence"`
}

// demoCriteria is what P6 has to satisfy, quoted here so a reader does not have to
// open another document to know what "done" means.
var demoCriteria = []string{
	"ingest a synthetic day through the real pipeline, so the clock corrector and the correlator both run",
	"trigger at least one incident and dispose it through the console form",
	"the incident closes with its hash-chained audit rows readable",
	"the North Star numerator printed is the number the query returns, not a hand-computed one",
	"every AI-derived line says it came from a stub provider",
	"finishes in under three minutes on one machine with no network",
}

var demoWillDo = []string{
	"synthesise a day of camera events across a site, an actor and a schedule",
	"run them through ingest, drift correction and correlation",
	"drive one incident to disposition through the console form",
	"print the four North Star companions beside the numerator",
	"print the evidence hashes, so a claim in the output can be re-derived",
}

func runDemo(ctx context.Context, env *command.Env, args []string) error {
	c := demoCommand()

	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "Machine-readable only.")
	plan := fs.Bool("plan", false, "Print the P6 acceptance criteria.")
	if err := fs.Parse(args); err != nil {
		return &command.UsageError{Cmd: c, Err: err}
	}

	st := demoState{
		Command:   "demo",
		Populated: false,
		Phase:     "P0",
		Summary:   "reserved: registered by the foundation pass, populated by P6. Not implemented.",
		WillDo:    demoWillDo,
		Evidence:  "docs/status.md is the source of truth for what is built",
	}
	if *plan {
		st.Criteria = demoCriteria
	}

	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}

	if *plan {
		_, _ = fmt.Fprintln(env.Stdout, "securityctl demo -- what P6 has to satisfy:")
		_, _ = fmt.Fprintln(env.Stdout)
		for i, c := range demoCriteria {
			_, _ = fmt.Fprintf(env.Stdout, "  %d. %s\n", i+1, c)
		}
		_, _ = fmt.Fprintln(env.Stdout)
		_, _ = fmt.Fprintln(env.Stdout, "None of these is met yet. "+st.Evidence+".")
		return nil
	}

	_, _ = fmt.Fprintln(env.Stderr, "securityctl demo is RESERVED and not implemented.")
	_, _ = fmt.Fprintln(env.Stderr)
	_, _ = fmt.Fprintln(env.Stderr, "It is registered because the execution plan puts it in P0 and fills it")
	_, _ = fmt.Fprintln(env.Stderr, "in at P6, and a command that arrives already working hides the behaviour")
	_, _ = fmt.Fprintln(env.Stderr, "change inside the commit that adds it.")
	_, _ = fmt.Fprintln(env.Stderr)
	_, _ = fmt.Fprintln(env.Stdout, "When it is populated it will:")
	for _, w := range demoWillDo {
		_, _ = fmt.Fprintln(env.Stdout, "  - "+lowerFirst(w))
	}
	_, _ = fmt.Fprintln(env.Stdout)
	_, _ = fmt.Fprintln(env.Stdout, "Acceptance criteria: securityctl demo --plan")
	_, _ = fmt.Fprintln(env.Stdout, "Build state:        "+st.Evidence)
	_, _ = fmt.Fprintln(env.Stderr)
	_, _ = fmt.Fprintln(env.Stderr, "Exiting non-zero on purpose: this command did not demonstrate anything,")
	_, _ = fmt.Fprintln(env.Stderr, "and the exit status is the only part of it a script will read.")

	return fmt.Errorf("demo is not implemented")
}

// lowerFirst avoids `strings.ToLower` on a whole sentence, which is what the
// obvious one-liner does and which loses every capital in the rest of the line.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
