package main

import (
	"flag"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
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

func installCommand() *command.Command {
	return &command.Command{
		Name:    "install",
		Summary: "Install a signed offline release bundle",
		Usage: `install — install a release bundle with no network access.

Verifies the bundle signature, hash manifest, SBOM and migration compatibility
before touching the machine. Refuses if the database is outside the schema
window, if the evidence directory is not exclusively writable, or if a secret
would have to live in a config file.`,
		Run: notImplemented("install"),
	}
}

func upgradeCommand() *command.Command {
	return &command.Command{
		Name:    "upgrade",
		Summary: "Upgrade to a new release, transactionally",
		Usage: `upgrade — advance the install to a new release.

Takes a backup first, applies expand steps only, and leaves the previous
install runnable on any failure. A contract step shipped in the same archive as
its expand is refused: the window in which both binaries must work is the whole
point of expand/contract, and collapsing it takes the site down.`,
		Run: notImplemented("upgrade"),
	}
}

func rollbackCommand() *command.Command {
	return &command.Command{
		Name:    "rollback",
		Summary: "Return to the previous release",
		Usage: `rollback — restore the previous install.

Never downgrades the schema by itself. If the previous binary cannot read the
current schema version, the rollback is refused and says so, because a
half-downgraded site is the state this product must never be in.`,
		Run: notImplemented("rollback"),
	}
}

func backupCommand() *command.Command {
	return &command.Command{
		Name:    "backup",
		Summary: "Back up the database, the evidence dir and the checkpoint material",
		Usage: `backup — produce a restorable, self-consistent backup.

Backing up the database without the evidence directory produces an archive
whose incidents reference bytes that do not exist, so this command refuses a
partial backup unless it is told explicitly.`,
		Run: notImplemented("backup"),
	}
}
func restoreCommand() *command.Command {
	return &command.Command{
		Name:    "restore",
		Summary: "Restore from a backup",
		Usage:   `restore — restore a backup produced by securityctl backup.`,
		Run:     notImplemented("restore"),
	}
}

// rulesCommand is the rule bundle tool surface. lint, explain, simulate and
// activation are implemented together, in the same track, because a bundle that
// can be activated without a simulate diff is a bundle nobody reviewed.
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
			Flags: func(fs *flag.FlagSet) {
				fs.String("bundle", "", "Candidate bundle file. Defaults to the newest in rules/builtin.")
				fs.String("fixtures", "testdata/fixtures", "Fixture set directory.")
				fs.String("baseline", "", "Bundle to diff against. Defaults to the stored active bundle.")
				fs.Bool("json", false, "Emit machine-readable output.")
				fs.Int("max-events", 100000, "Cap on evaluated signals.")
			},
			Run: runRulesSimulate,
		},
		&command.Command{
			Name:    "activate",
			Summary: "Activate a stored bundle version",
			Usage: `activate --version <v> --diff-digest <sha256>

Refuses without a diff digest, and refuses a digest that is not a SHA-256. A
bundle that has not been simulated cannot be activated, so there is no path that
turns a rule on without someone having read what it changes.`,
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
