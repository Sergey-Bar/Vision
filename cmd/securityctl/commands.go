package main

import (
	"flag"

	"github.com/Sergey-Bar/vision/internal/command"
)

// The lifecycle, rule and operator commands below are the command-tree skeleton
// for Milestone 1: every name an operator or an installer will reach for exists,
// with the usage text that says what it will do and what it will refuse.
//
// They refuse rather than pretend. A command that reports success without having
// done the thing is worse than a missing command: an installer that says
// "upgrade complete" when it did not is a site that believes it is patched.
//
// Each becomes real in its own track; the marker names that track so the refusal
// is information rather than a dead end.

func rulesCommand() *command.Command {
	rules := &command.Command{
		Name:    "rules",
		Summary: "Rule bundle lint, explain, simulate and activation",
		Usage: `rules — treat a rule bundle as untrusted input.

A bundle is YAML. It is validated against its JSON Schema, then semantically
validated (referenced zone and schedule exist, no impossible window, no
duplicate rule id, no unsupported event type, no contradictory configuration,
no rule with an action that does nothing), then compiled. There is no shell, no
SQL, no network, no dynamic code and no unbounded regular expression anywhere in
that path — the strongest operator in the language is a fixed prefix match.`,
	}
	rules.Register(
		&command.Command{
			Name:    "lint",
			Summary: "Validate a bundle without activating it",
			Flags: func(fs *flag.FlagSet) {
				fs.String("bundle", "", "Bundle file, or a directory of them. Defaults to rules/builtin.")
				fs.Bool("json", false, "Emit machine-readable output.")
			},
			Run: runRulesLint,
		},
		&command.Command{
			Name:    "explain",
			Summary: "Explain a rule, or list them all with their actions",
			Usage: `explain [--rule <id>] [--bundle <path>]

With no --rule it lists every rule, its priority, whether it is enabled, and what
it does. With --rule it prints the condition tree, the dispatches it requires, and
what happens when the AI plane is unavailable — because "what does this rule do
without a model" is the question an operator asks before trusting a bundle.`,
			Run: runRulesExplain,
		},
		&command.Command{
			Name:    "simulate",
			Summary: "Run a bundle over the fixture set and diff against the active bundle",
			Usage: `simulate — produce the diff digest that activation requires.

The digest is over the diff, not over the bundle and not over the run time. So
re-running the simulation to confirm a review produces the same digest, while a
change to the fixtures or to the active bundle produces a different one — and a
review that no longer describes what would happen no longer authorises anything.

Runs from committed fixtures with no Milestone server reachable.`,
			// No Flags closure here, deliberately.
			//
			// The command dispatcher documents the trap: a command that declares
			// a Flags closure has its flags parsed by the tree, and its Run then
			// receives only the positionals. `rules simulate` owned its own
			// FlagSet as well, so `--bundle`, `--fixtures`, `--baseline`, `--json`
			// and `--max-events` were all consumed by the tree and arrived
			// nowhere. Every one of them was silently ignored.
			//
			// That is not a cosmetic bug. The activation digest is computed over
			// the candidate bundle, and `rules activate` refuses anything the
			// simulate report does not match. So an operator who simulated
			// `--bundle candidate.yaml` was handed a digest for the *default*
			// bundle, reviewed it, and activated a bundle nobody simulated - and
			// the gate agreed, because the report was internally consistent about
			// the wrong file.
			//
			// Owning the FlagSet in Run is the other supported shape, and it is
			// the one that actually applies the flags.
			Run: runRulesSimulate,
		},
		&command.Command{
			Name:    "activate",
			Summary: "Activate a stored bundle version",
			Usage: `activate --version <v> --report <report.json>

Takes the report ules simulate --json produced, not a digest. The gate
re-hashes the acknowledged change list and compares it with the digest the report
carries, so a report edited after it was read, a report for a different
bundle, and a digest typed by hand are all refused. A bundle that has not been
simulated cannot be activated, so there is no path that turns a rule on without
someone having read what it changes.`,
			Flags: func(fs *flag.FlagSet) {
				fs.String("version", "", "Bundle version to activate.")
				fs.String("diff-digest", "", "Diff digest from `rules simulate`.")
			},
			Run: runRulesActivate,
		},
		&command.Command{
			Name:    "rollback",
			Summary: "Restore the previous immutable bundle version",
			Usage:   `rollback — returns to whichever version was active before this one, audited.`,
			Run:     runRulesRollback,
		},
		&command.Command{
			Name:    "show",
			Summary: "List stored bundles and the active version",
			Flags: func(fs *flag.FlagSet) {
				fs.Bool("json", false, "Emit machine-readable output.")
			},
			Run: runRulesShow,
		},
		&command.Command{
			Name:    "store",
			Summary: "Store a bundle without activating it",
			Usage: `store — compiles and stores a bundle immutably.

Storing is not activating. They are separate commands because a bundle that
could be activated in one step would be activated without anyone reading what it
changes.`,
			Flags: func(fs *flag.FlagSet) {
				fs.String("bundle", "", "Bundle file to store.")
				fs.String("source", "", "Source reference recorded with the bundle.")
			},
			Run: runRulesStore,
		},
	)
	return rules
}
