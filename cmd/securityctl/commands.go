package main

import "github.com/Sergey-Bar/Ai-Captain/internal/command"

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

func verifyReleaseCommand() *command.Command {
	return &command.Command{
		Name:    "verify-release",
		Summary: "Verify a release archive without installing it",
		Usage: `verify-release — check a signed offline release bundle.

Rejects a tampered archive, an unsigned archive, a manifest whose file hashes
do not match the archive contents, a manifest carrying a path that escapes the
archive root, and any manifest that would install a secret.

This is the command to run on a USB stick before trusting it.`,
		Run: notImplemented("verify-release"),
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
duplicate rule id, no unsupported event type, no contradictory configuration),
then compiled. There is no shell, no SQL, no network, no dynamic code and no
unbounded regex anywhere in that path.`,
	}
	rules.Register(
		&command.Command{
			Name:    "lint",
			Summary: "Validate a bundle without activating it",
			Usage:   `lint — validate a bundle file or directory.`,
			Run:     notImplemented("rules lint"),
		},
		&command.Command{
			Name:    "explain",
			Summary: "Explain how a rule evaluates, and why",
			Usage:   `explain — show the compiled rule and the evaluation path for a scenario.`,
			Run:     notImplemented("rules explain"),
		},
		&command.Command{
			Name:    "simulate",
			Summary: "Run a bundle over a fixture set and diff against the active bundle",
			Usage: `simulate — produce the diff digest that activation requires.

The digest is over the diff itself, not over the bundle. Re-running the
simulation and getting a different digest means the fixtures or the active
bundle moved, and the reviewed diff is no longer the one being activated.`,
			Run: notImplemented("rules simulate"),
		},
		&command.Command{
			Name:    "activate",
			Summary: "Activate a stored bundle version",
			Run:     notImplemented("rules activate"),
		},
		&command.Command{
			Name:    "rollback",
			Summary: "Restore the previous immutable bundle version",
			Run:     notImplemented("rules rollback"),
		},
		&command.Command{
			Name:    "show",
			Summary: "List stored bundles and the active version",
			Run:     notImplemented("rules show"),
		},
	)
	return rules
}

func incidentCommand() *command.Command {
	inc := &command.Command{
		Name:    "incident",
		Summary: "Inspect incidents and why they exist",
		Usage: `incident — read-only incident inspection.

Every answer here comes from stored facts and the stored AIObservation. No
command in this tree re-runs a model to explain a past decision, and one that
tried to would be a bug rather than a feature.`,
	}
	inc.Register(
		&command.Command{
			Name:    "list",
			Summary: "List incidents visible to the session",
			Run:     notImplemented("incident list"),
		},
		&command.Command{
			Name:    "show",
			Summary: "Show an incident with its full provenance",
			Usage:   `show <id> — the event IDs, observation ID, model, prompt, hashes, rule id, bundle version and policy version behind the decision.`,
			Run:     notImplemented("incident show"),
		},
		&command.Command{
			Name:    "explain",
			Summary: "Answer why this incident existed",
			Usage:   `explain <id> — the evaluation steps, in order, with the event and observation IDs each step relied on.`,
			Run:     notImplemented("incident explain"),
		},
		&command.Command{
			Name:    "replay",
			Summary: "Reproduce a past decision from stored facts",
			Usage:   `replay <id> — re-evaluate a historical incident from stored facts and the stored observation, and report whether the outcome matches.`,
			Run:     notImplemented("incident replay"),
		},
		&command.Command{
			Name:    "disposition",
			Summary: "Record an operator disposition",
			Run:     notImplemented("incident disposition"),
		},
	)
	return inc
}

func auditCommand() *command.Command {
	aud := &command.Command{
		Name:    "audit",
		Summary: "Verify and seal the audit ledger",
		Usage: `audit — the ledger is separate from the operational logs.

Logs are diagnostic and can be lost. The ledger is authoritative, hash-chained,
and sealed to storage the database owner cannot reach.`,
	}
	aud.Register(
		&command.Command{
			Name:    "verify",
			Summary: "Recompute the hash chain and report the first break",
			Run:     notImplemented("audit verify"),
		},
		&command.Command{
			Name:    "seal",
			Summary: "Seal the verified range through the CheckpointSink",
			Usage:   `seal — verifies first and seals second. It never seals a chain that does not verify.`,
			Run:     notImplemented("audit seal"),
		},
		&command.Command{
			Name:    "checkpoints",
			Summary: "List sealed checkpoints",
			Run:     notImplemented("audit checkpoints"),
		},
		&command.Command{
			Name:    "verify-seals",
			Summary: "Verify the offline seal set with no database present",
			Usage:   `verify-seals — the air-gapped audit path. Takes a directory of seal files and a trusted public key, and reports any gap or forged seal.`,
			Run:     notImplemented("audit verify-seals"),
		},
	)
	return aud
}

func identityCommand() *command.Command {
	id := &command.Command{
		Name:    "identity",
		Summary: "Administrator identity and recovery",
		Usage: `identity — MFA is mandatory and there is no default password.

bootstrap-admin requires a passphrase supplied on the terminal or from the
secret store, never as an argument, and never from a file in the release
archive.`,
	}
	id.Register(
		&command.Command{
			Name:    "bootstrap-admin",
			Summary: "Create the first administrator",
			Run:     notImplemented("identity bootstrap-admin"),
		},
		&command.Command{
			Name:    "enroll-mfa",
			Summary: "Enrol a WebAuthn or TOTP factor for a user",
			Run:     notImplemented("identity enroll-mfa"),
		},
		&command.Command{
			Name:    "list",
			Summary: "List users, roles and site assignments",
			Run:     notImplemented("identity list"),
		},
		&command.Command{
			Name:    "revoke-sessions",
			Summary: "Revoke every session for a user or a tenant",
			Run:     notImplemented("identity revoke-sessions"),
		},
	)
	return id
}
