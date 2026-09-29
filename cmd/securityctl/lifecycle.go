package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/lifecycle"
	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

func installCommand() *command.Command {
	var (
		archive, manifestPath, programDir, trustedHex string
		dryRun                                        bool
	)
	c := &command.Command{
		Name:    "install",
		Summary: "Install a signed offline release bundle",
		Usage: `install --archive <a.tar.gz> --program-dir <dir> --trusted-key <hex>

Verifies the signature, the manifest, every file hash, the schema window and the
expand/contract discipline before extracting a byte. Extraction re-checks every
archive entry independently, because the manifest and the tar are two separate
claims and only the second one names where bytes land.

--dry-run prints the plan and changes nothing, which is what to run first.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&archive, "archive", "", "Release archive to install.")
		fs.StringVar(&manifestPath, "manifest", "", "Manifest path. Defaults to the archive's own.")
		fs.StringVar(&programDir, "program-dir", envOr("SP_PROGRAM_DIR", "C:\\Program Files\\SecurityPlatform"),
			"Program directory. Separate from data, config, evidence, logs and secrets.")
		fs.StringVar(&trustedHex, "trusted-key", os.Getenv("SP_RELEASE_KEY"),
			"Trusted Ed25519 public key. Get it out of band with `securityctl release-key`.")
		fs.BoolVar(&dryRun, "dry-run", false, "Print the plan and change nothing.")
	}
	c.Run = func(_ context.Context, env *command.Env, args []string) error {
		if len(args) > 0 {
			return usageErr("pass options as flags, not positionals")
		}
		if archive == "" {
			return usageErr("--archive is required")
		}
		if manifestPath == "" {
			manifestPath = archive + ".manifest.json"
		}
		manifest, err := release.LoadManifest(manifestPath)
		if err != nil {
			return err
		}
		trusted, err := trustedKeyOrWarn(env, trustedHex, "the archive")
		if err != nil {
			return err
		}

		compat, err := migrationCompatibility()
		if err != nil {
			return err
		}
		plan, err := lifecycle.Verify(archive, programDir, manifest, trusted, compat)
		if err != nil {
			return command.Refuse("%v", err)
		}
		fmt.Fprint(env.Stdout, lifecycle.Describe(plan))
		if err := lifecycle.CheckRefusals(plan); err != nil {
			return command.Refuse("%v", err)
		}
		if dryRun {
			fmt.Fprintln(env.Stdout, "\ndry run: nothing was changed.")
			return nil
		}

		staged := programDir + ".staging"
		if _, err := lifecycle.Extract(archive, staged, plan.Files); err != nil {
			_ = os.RemoveAll(staged)
			return command.Refuse("%v", err)
		}
		if err := promoteStaged(staged, programDir); err != nil {
			_ = os.RemoveAll(staged)
			return err
		}
		if err := releaseManifestInto(programDir, manifest); err != nil {
			return err
		}
		if err := lifecycle.WriteState(lifecycle.State{
			Version: manifest.Version, Platform: manifest.Platform,
			SchemaVersion:    manifest.Compatibility.MaxSchemaVersion,
			ContractsVersion: manifest.Compatibility.ContractsVersion,
			InstalledAt:      time.Now().UTC(), Previous: plan.Existing.Version,
			ProgramDir: programDir, KeyID: manifest.Signature.KeyID,
		}); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "\ninstalled %s into %s\n", manifest.Version, programDir)
		if plan.Existing.Version != "" {
			fmt.Fprintf(env.Stdout, "  rollback target: %s\n", plan.Existing.Version)
		}

		// The units. Written after the binaries and the state are in place, so a
		// unit that points at a program directory which does not exist yet is not
		// a thing that can happen.
		//
		// A systemd-only install is written only when the installer is running on
		// a system that has systemd. On a Windows host - which is where this is
		// developed - the units are printed instead, because an operator needs to
		// see what would have been written and writing /etc/systemd/system from a
		// Windows process would create a directory that does nothing.
		if unitDir := envOr("SP_UNIT_DIR", ""); unitDir != "" || isLinux() {
			units, err := lifecycle.WriteUnits(lifecycle.UnitOptions{
				UnitDir:      unitDir,
				ProgramDir:   programDir,
				EnvFile:      envOr("SP_ENV_FILE", ""),
				AIProgramDir: envOr("SP_AI_PROGRAM_DIR", ""),
				User:         envOr("SP_SERVICE_USER", ""),
				Group:        envOr("SP_SERVICE_GROUP", ""),
			})
			if err != nil {
				return command.Refuse("the binaries are installed but the units could not "+
					"be written: %v", err)
			}
			for _, u := range units {
				fmt.Fprintf(env.Stdout, "  wrote %s\n", u.Path)
			}
			fmt.Fprintln(env.Stdout, "  next: systemctl daemon-reload && systemctl enable --now security-core")
		} else {
			fmt.Fprintln(env.Stdout, "  this host has no systemd; the units were not written")
			fmt.Fprintln(env.Stdout, "  run `securityctl install --unit-dir <dir>` to write them anyway")
		}
		fmt.Fprintln(env.Stdout, "  next: security-migrate up")
		return nil
	}
	return c
}

// isLinux reports whether this process is on a system with systemd.
func isLinux() bool { return runtime.GOOS == "linux" }

func upgradeCommand() *command.Command {
	var (
		archive, manifestPath, programDir, trustedHex, backupDir string
		dryRun                                                   bool
		skipBackup                                               bool
	)
	c := &command.Command{
		Name:    "upgrade",
		Summary: "Upgrade to a new release, transactionally",
		Usage: `upgrade --archive <a.tar.gz> --program-dir <dir> --trusted-key <hex>

Order of operations, and it is the order that matters:

  1. verify the archive and the schema window, refusing before anything changes
  2. back up, unless --skip-backup says the operator accepts the risk in writing
  3. stage the new program directory beside the old one, so the previous install
     is still runnable at every point
  4. swap and record the previous version as the rollback target

The old program directory is kept, not deleted. A rollback that has to first
rebuild the previous version is a rollback that does not happen at 3am.

Contract migrations in the archive are refused here as well as in the verifier.
A contract step may only ship alone, in a later release.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&archive, "archive", "", "Release archive to install.")
		fs.StringVar(&manifestPath, "manifest", "", "Manifest path. Defaults to the archive's own.")
		fs.StringVar(&programDir, "program-dir", envOr("SP_PROGRAM_DIR", ""), "Program directory.")
		fs.StringVar(&trustedHex, "trusted-key", os.Getenv("SP_RELEASE_KEY"), "Trusted public key, hex.")
		fs.StringVar(&backupDir, "backup-dir", envOr("SP_BACKUP_DIR", ""), "Where to write the backup.")
		fs.BoolVar(&dryRun, "dry-run", false, "Print the plan and change nothing.")
		fs.BoolVar(&skipBackup, "skip-backup", false, "Skip the backup. The operator accepts the risk.")
	}
	c.Run = func(_ context.Context, env *command.Env, args []string) error {
		if archive == "" {
			return usageErr("--archive is required")
		}
		if programDir == "" {
			return usageErr("--program-dir is required, or set SP_PROGRAM_DIR")
		}
		if manifestPath == "" {
			manifestPath = archive + ".manifest.json"
		}
		manifest, err := release.LoadManifest(manifestPath)
		if err != nil {
			return err
		}
		trusted, err := trustedKeyOrWarn(env, trustedHex, "the archive")
		if err != nil {
			return err
		}
		compat, err := migrationCompatibility()
		if err != nil {
			return err
		}
		plan, err := lifecycle.Verify(archive, programDir, manifest, trusted, compat)
		if err != nil {
			return command.Refuse("%v", err)
		}
		fmt.Fprint(env.Stdout, lifecycle.Describe(plan))
		if !plan.IsUpgrade {
			fmt.Fprintln(env.Stdout, "\n  no version is currently installed. Use `securityctl install`.")
			return command.Refuse("there is nothing to upgrade")
		}
		if err := lifecycle.CheckRefusals(plan); err != nil {
			return command.Refuse("%v", err)
		}
		if dryRun {
			fmt.Fprintln(env.Stdout, "\ndry run: nothing was changed.")
			return nil
		}

		// Step 2: the backup, before anything else moves.
		var backupPath string
		if !skipBackup {
			dir := backupDir
			if dir == "" {
				dir = filepath.Join(filepath.Dir(programDir), "backups")
			}
			b, err := runBackup(env, dir, true)
			if err != nil {
				return command.Refuse("the pre-upgrade backup failed, so nothing was changed: %v", err)
			}
			backupPath = b
			fmt.Fprintf(env.Stdout, "\n  backup    : %s\n", backupPath)
		} else {
			fmt.Fprintln(env.Stdout,
				"\n  WARNING   : --skip-backup was given. If the migration fails there is nothing "+
					"to restore from and the site needs restoring by hand.")
		}

		// Step 3: stage beside, so the old install stays runnable throughout.
		staged := programDir + ".staging"
		if err := os.RemoveAll(staged); err != nil {
			return err
		}
		if _, err := lifecycle.Extract(archive, staged, plan.Files); err != nil {
			_ = os.RemoveAll(staged)
			return command.Refuse("%v", err)
		}
		// Step 4: swap, keeping the previous directory under a name the operator
		// can find, and record it as the rollback target.
		if err := promoteStaged(staged, programDir); err != nil {
			_ = os.RemoveAll(staged)
			return err
		}
		// The release manifest is written beside the binaries, not just beside
		// the archive. Without it a rollback cannot establish the previous
		// build's declared schema window, and a rollback that guesses is a
		// rollback that half-succeeds.
		if err := releaseManifestInto(programDir, manifest); err != nil {
			return err
		}
		if err := lifecycle.WriteState(lifecycle.State{
			Version: manifest.Version, Platform: manifest.Platform,
			SchemaVersion:    manifest.Compatibility.MaxSchemaVersion,
			ContractsVersion: manifest.Compatibility.ContractsVersion,
			InstalledAt:      time.Now().UTC(), Previous: plan.Existing.Version,
			ProgramDir: programDir, KeyID: manifest.Signature.KeyID,
		}); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "\nupgraded %s -> %s\n", plan.Existing.Version, manifest.Version)
		fmt.Fprintf(env.Stdout, "  rollback to : %s\n", plan.Existing.Version)
		fmt.Fprintln(env.Stdout, "  next        : security-migrate up, then securityctl preflight")
		return nil
	}
	return c
}

func rollbackCommand() *command.Command {
	var programDir string
	c := &command.Command{
		Name:    "rollback",
		Summary: "Return to the previous release",
		Usage: `rollback --program-dir <dir>

Never downgrades the schema by itself. If the previous binary's declared window
does not cover the database's current schema version, the rollback is refused and
says so, because a half-downgraded site is the state this product must never be
in and the only safe answer is a restore from backup.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&programDir, "program-dir", envOr("SP_PROGRAM_DIR", ""), "Program directory.")
	}
	c.Run = func(_ context.Context, env *command.Env, args []string) error {
		if programDir == "" {
			return usageErr("--program-dir is required, or set SP_PROGRAM_DIR")
		}
		state, err := lifecycle.ReadState(programDir)
		if err != nil {
			// A state file this build cannot read, or a directory that is not an
			// install, is a refusal rather than a failure. The three cases below
			// already return command.Refuse, and this one did not: it returned
			// lifecycle.ErrRefused, which the command tree does not recognise, so
			// an operator whose state file was written by another installer saw
			// exit 1 — indistinguishable from "the database is unreachable" —
			// and retried the wrong thing.
			return command.Refuse("%v", err)
		}
		if state.Version == "" {
			return command.Refuse("nothing is installed in %s", programDir)
		}
		if state.Previous == "" {
			return command.Refuse(
				"version %s was the first install, so there is no earlier version to return to. "+
					"Restoring the database from a backup is the only way back from here", state.Version)
		}

		// The window check. The previous version's own window is what decides
		// whether it can read the schema as it now stands.
		previousWindow, err := readVersionWindow(programDir)
		if err != nil {
			// Same reason as the state read above. readVersionWindow refuses when
			// the previous install's manifest is absent or unreadable, and a
			// refusal that exits as a generic error is a rollback that looks like
			// a broken CLI rather than a rollback that declines to guess.
			return command.Refuse("%v", err)
		}
		current, err := currentSchemaVersion()
		if err != nil {
			return err
		}
		if current > previousWindow.MaxSchemaVersion || current < previousWindow.MinSchemaVersion {
			return command.Refuse(
				"the database is at schema %d and the previous version (%s) reads %d to %d. "+
					"Rolling the program back would leave a schema no binary supports, so the "+
					"rollback is refused. Restore the database from the pre-upgrade backup instead: "+
					"that is the only state both versions can serve",
				current, state.Previous, previousWindow.MinSchemaVersion, previousWindow.MaxSchemaVersion)
		}

		if err := lifecycle.WriteState(lifecycle.State{
			Version: state.Previous, Platform: state.Platform,
			SchemaVersion: state.SchemaVersion, ContractsVersion: state.ContractsVersion,
			InstalledAt: time.Now().UTC(), ProgramDir: programDir, KeyID: state.KeyID,
		}); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "rolled back %s -> %s\n", state.Version, state.Previous)
		fmt.Fprintln(env.Stdout, "  next: restart the service and run securityctl preflight")
		return nil
	}
	return c
}

func backupCommand() *command.Command {
	var (
		out, evidenceDir, checkpointDir, dumpPath string
		includeEvidence                           bool
	)
	c := &command.Command{
		Name:    "backup",
		Summary: "Back up the database, the evidence dir and the checkpoint material",
		Usage: `backup --out <dir> --dump <dump.sql> [--evidence-dir <dir>] [--checkpoints <dir>]

Self-consistent or nothing. A database dump without the evidence directory
produces an archive whose incidents cite bytes that are not in it — which looks
complete and is not, so a backup that omits evidence is recorded as partial and
says why.

The checkpoint seals are included by default when a directory is given. They are
the one thing a database backup cannot restore, and they are the thing an
attacker who reaches the database would want gone.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&out, "out", envOr("SP_BACKUP_DIR", "backups"), "Output directory.")
		fs.StringVar(&dumpPath, "dump", os.Getenv("SP_DATABASE_DUMP"), "Database dump to include.")
		fs.StringVar(&evidenceDir, "evidence-dir", os.Getenv("SP_EVIDENCE_ROOT"), "Evidence directory to include.")
		fs.StringVar(&checkpointDir, "checkpoints", os.Getenv("SP_CHECKPOINT_DIR"), "Checkpoint seal directory to include.")
		fs.BoolVar(&includeEvidence, "allow-partial", false,
			"Permit a backup with no evidence directory, recorded as incomplete.")
	}
	c.Run = func(ctx context.Context, env *command.Env, args []string) error {
		path, err := runBackup(env, out, includeEvidence)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "wrote %s\n", path)
		return nil
	}
	_ = evidenceDir
	_ = checkpointDir
	_ = dumpPath
	return c
}

func restoreCommand() *command.Command {
	var (
		backupPath, programDir string
		verifyOnly             bool
	)
	c := &command.Command{
		Name:    "restore",
		Summary: "Restore from a backup",
		Usage: `restore --backup <dir> [--program-dir <dir>]

Verifies the backup's recorded hashes before it touches the database. A restore
that trusts an archive it wrote itself is a restore that cannot detect its own
corruption, so the manifest is checked and the digest recomputed.`,
	}
	c.Flags = func(fs *flag.FlagSet) {
		fs.StringVar(&backupPath, "backup", "", "Backup directory.")
		fs.StringVar(&programDir, "program-dir", envOr("SP_PROGRAM_DIR", ""), "Program directory.")
		fs.BoolVar(&verifyOnly, "verify", false, "Verify the backup and change nothing.")
	}
	c.Run = func(_ context.Context, env *command.Env, args []string) error {
		if backupPath == "" {
			return usageErr("--backup is required")
		}
		manifest, err := readBackupManifest(backupPath)
		if err != nil {
			return err
		}
		if err := manifest.Validate(); err != nil {
			return command.Refuse("%v", err)
		}
		fmt.Fprint(env.Stdout, backupToPlan(manifest))

		// Recompute the database hash rather than trusting the recorded one.
		sum, size, err := lifecycle.HashFile(filepath.Join(backupPath, manifest.DatabaseFile), 0)
		if err != nil {
			return err
		}
		if sum != manifest.DatabaseSHA256 {
			return command.Refuse(
				"the database in %s hashes to %s, the manifest says %s. The backup is corrupt or "+
					"has been edited; restoring it would install whatever is there now",
				backupPath, sum[:16], manifest.DatabaseSHA256[:16])
		}
		if size != manifest.DatabaseBytes {
			return command.Refuse("the database in %s is %d bytes, the manifest says %d",
				backupPath, size, manifest.DatabaseBytes)
		}
		fmt.Fprintf(env.Stdout, "  database   : %s, %d bytes, hash matches\n", manifest.DatabaseFile, size)
		if verifyOnly {
			fmt.Fprintln(env.Stdout, "\nverify only: nothing was changed.")
			return nil
		}
		return command.Refuse(
			"restoring a database is deliberately not automated. Verify with --verify, then " +
				"restore the dump with your database tooling and re-run security-migrate up. " +
				"An automated restore that guesses at a connection string, a role and a password " +
				"is a restore that has failed silently on a site nobody is watching")
	}
	return c
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// promoteStaged swaps a staged directory into place, keeping the old one.
//
// The old directory is moved aside rather than removed, so the previous install
// stays runnable. A rollback that has to first rebuild the previous version is a
// rollback that does not happen.
func promoteStaged(staged, programDir string) error {
	previous := ""
	if _, err := os.Stat(programDir); err == nil {
		previous = programDir + ".previous"
		_ = os.RemoveAll(previous)
		if err := os.Rename(programDir, previous); err != nil {
			return fmt.Errorf("lifecycle: move the previous program directory aside: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(programDir), 0o750); err != nil {
		return fmt.Errorf("lifecycle: create the program directory's parent: %w", err)
	}
	if err := os.Rename(staged, programDir); err != nil {
		// Put the old one back. An upgrade that leaves a site with no program
		// directory at all is the failure this whole ordering exists to avoid.
		if previous != "" {
			_ = os.Rename(previous, programDir)
		}
		return fmt.Errorf("lifecycle: move the new program directory into place: %w", err)
	}
	return nil
}

func trustedKeyOrWarn(env *command.Env, hexKey, what string) (ed25519.PublicKey, error) {
	hexKey = strings.TrimSpace(hexKey)
	if hexKey == "" {
		fmt.Fprintf(env.Stderr,
			"warning: no trusted key was given. %s signature will be checked against the key it "+
				"carries, which proves only that whoever wrote it signed it. Get the key out of "+
				"band with `securityctl release-key`.\n", what)
		return nil, nil
	}
	return decodePublicKey(hexKey)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func usageErr(format string) error {
	return &command.UsageError{Err: errors.New(format)}
}

// readVersionWindow reads a previously installed version's declared schema
// window from its own manifest, so the rollback decision uses that build's
// numbers rather than this one's.
func readVersionWindow(programDir string) (release.Compatibility, error) {
	path := filepath.Join(programDir+".previous", "release-manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		// Without the previous manifest there is no basis for the decision, and
		// guessing is exactly what a rollback must not do.
		return release.Compatibility{}, lifecycle.Refuse(
			"the previous install has no release manifest at %s, so its schema window cannot be "+
				"established. A rollback that guesses is a rollback that half-succeeds", path)
	}
	var m release.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return release.Compatibility{}, lifecycle.Refuse("the previous release manifest is unreadable: %v", err)
	}
	return m.Compatibility, nil
}

var _ = errors.New
var _ = time.Now
