package main

// This file holds the install/upgrade/rollback gate.
//
// internal/dr's TestRecoveryDrill and the `recovery-drill` step in
// scripts/verify.ps1 cover restore-from-backup. They prove a site can be brought
// back from data loss. They say nothing about the operation an operator
// actually performs on a running site at 02:00, which is: put the next version
// on top of this one. That is a different failure surface â€” a staging mistake
// clobbers the operator's env file, a swap that deletes the old directory leaves
// nothing to roll back to, a refusal that is not a refusal installs a core that
// cannot read the site it was just put on â€” and none of it was covered by
// anything a release is gated on.
//
// These tests drive the real commands through newRootCommand().Execute, not a
// reimplementation of what they do: the staging directory, the swap, the state
// file, the units, the backup and the rollback window check are all the shipped
// code paths.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/command"
	"github.com/Sergey-Bar/Ai-Captain/internal/lifecycle"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/release"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

// releaseA and releaseB are two consecutive releases. The bodies differ, and so
// does the file set, so an assertion about what the upgrade replaced and what it
// preserved is an assertion about bytes rather than about a file existing.
var (
	releaseA = map[string]string{
		"security-core":      "core-binary-A",
		"securityctl":        "cli-binary-A",
		"rules/builtin.yaml": "rule: only-in-A\n",
	}
	releaseB = map[string]string{
		"security-core":         "core-binary-B",
		"securityctl":           "cli-binary-B",
		"rules/builtin.yaml":    "rule: only-in-B\n",
		"migrations/README.txt": "the B release ships a new migration\n",
	}
)

// ---------------------------------------------------------------------------
// A: the install. A directory, the units it wrote, their modes, the state file.
// ---------------------------------------------------------------------------

func TestInstallPutsTheBinariesTheUnitsAndTheStateOnDisk(t *testing.T) {
	site := newSite(t)
	rel := buildRelease(t, "0.1.0", testCompat(t), releaseA)

	out := mustRun(t, site, "install",
		"--archive", rel.archive, "--program-dir", site.program,
		"--trusted-key", rel.pubHex)

	// The binaries the manifest named, byte for byte.
	for name, want := range releaseA {
		got := readFile(t, filepath.Join(site.program, filepath.FromSlash(name)))
		if got != want {
			t.Errorf("%s is %q after the install, want %q", name, got, want)
		}
	}

	// The state file is the only record of what this machine was running, and a
	// first install has to record the version, the window and the signing key.
	state, err := lifecycle.ReadState(site.program)
	if err != nil {
		t.Fatalf("ReadState after the install: %v", err)
	}
	if state.Version != "0.1.0" {
		t.Errorf("the state records version %q, want 0.1.0", state.Version)
	}
	if state.Previous != "" {
		t.Errorf("a first install records previous = %q. There is no previous version, and "+
			"recording one gives rollback something to claim exists", state.Previous)
	}
	if state.SchemaVersion != testCompat(t).MaxSchemaVersion {
		t.Errorf("the state records schema window %d, want %d", state.SchemaVersion,
			testCompat(t).MaxSchemaVersion)
	}
	if state.ProgramDir != site.program {
		t.Errorf("the state records program dir %q, want %q", state.ProgramDir, site.program)
	}
	if state.KeyID != "test-key" {
		t.Errorf("the state records signing key %q, want test-key. An operator cannot tell "+
			"which release key a site trusts without it", state.KeyID)
	}
	if state.InstalledAt.IsZero() {
		t.Error("the state records no install time")
	}

	// The units, and the one that matters most.
	unit := filepath.Join(site.unitDir, "security-core.service")
	unitBody := readFile(t, unit)
	if !strings.Contains(unitBody, "ExecStart=") {
		t.Errorf("the unit does not name an ExecStart:\n%s", unitBody)
	}
	// systemd on Linux refuses backslashes, and the installer also runs on a
	// developer machine, so a path rendered with a Windows separator starts here
	// and fails everywhere it is actually installed. The separator is asserted on
	// the paths rather than by banning the character: the unit legitimately
	// contains a backslash in every `\n` line terminator, which systemd reads as
	// an escape and this is not.
	if !strings.Contains(unitBody, "ExecStart="+strings.ReplaceAll(site.program, `\`, "/")) {
		t.Errorf("the unit's ExecStart is not a forward-slash path:\n%s", unitBody)
	}
	if !strings.Contains(unitBody, strings.ReplaceAll(site.program, `\`, "/")) {
		t.Errorf("the unit does not point at the program directory it was installed into:\n%s", unitBody)
	}
	if !strings.Contains(unitBody, "EnvironmentFile=-"+strings.ReplaceAll(site.envFile, `\`, "/")) {
		t.Errorf("the unit does not name the environment file:\n%s", unitBody)
	}
	// Every hardening line in the shipped unit has to survive the installer, or
	// the installer is a way to strip hardening from a running site.
	for _, want := range []string{
		"NoNewPrivileges=true", "ProtectSystem=strict", "ProtectHome=true",
		"PrivateTmp=true", "MemoryDenyWriteExecute=true", "CapabilityBoundingSet=",
	} {
		if !strings.Contains(unitBody, want) {
			t.Errorf("the installed unit lost %q", want)
		}
	}

	env := readFile(t, site.envFile)
	if !strings.Contains(env, "SP_TENANT_ID=") || !strings.Contains(env, "SP_SECRET_KEY=") {
		t.Errorf("the environment file was not written from the template:\n%s", env)
	}
	// A required variable ships empty rather than as a placeholder. A core that
	// starts with tenant id "CHANGE-ME" is worse than a core that refuses.
	if strings.Contains(env, "CHANGE-ME") {
		t.Error("the environment template ships a placeholder value for a required variable")
	}

	requirePOSIXModesOnThisHost(t)
	assertMode(t, unit, 0o644, "systemd reads the unit as a different account")
	assertMode(t, site.envFile, 0o640, "it carries SP_DATABASE_URL and the path to the secret key")

	if !strings.Contains(out, "installed 0.1.0") {
		t.Errorf("the install did not report what it installed:\n%s", out)
	}
	// And it says a rollback target exists only when one does.
	if strings.Contains(out, "rollback target") {
		t.Errorf("a first install announced a rollback target:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// B: the upgrade. What is preserved, what is replaced, and what is left behind.
// ---------------------------------------------------------------------------

func TestAnUpgradeReplacesTheProgramAndPreservesTheOperatorSEnvFile(t *testing.T) {
	site := newSite(t)
	relA := buildRelease(t, "0.1.0", testCompat(t), releaseA)
	mustRun(t, site, "install",
		"--archive", relA.archive, "--program-dir", site.program, "--trusted-key", relA.pubHex)

	// The operator does what operators do: they edit the file. This is the site's
	// tenant id and its database password, and the upgrade is the command they run
	// when something else has gone wrong.
	const operatorEdit = "SP_TENANT_ID=the-real-tenant\nSP_DATABASE_PASSWORD=the-real-password\n"
	if err := os.WriteFile(site.envFile, []byte(operatorEdit), 0o640); err != nil {
		t.Fatalf("write the operator's env file: %v", err)
	}
	unitBefore := readFile(t, filepath.Join(site.unitDir, "security-core.service"))

	relB := buildRelease(t, "0.2.0", testCompat(t), releaseB)
	// The backup is not skipped. --skip-backup exists and an operator may use it,
	// but a gate that always passes it is not testing the ordering the upgrade
	// documents, which is backup first and only then move anything.
	mustRun(t, site, "upgrade",
		"--archive", relB.archive, "--program-dir", site.program,
		"--trusted-key", relB.pubHex, "--backup-dir", site.backups)

	// Preserved: the environment file and the operator's edits to it. Replacing it
	// with a fresh template on the upgrade meant to fix the site is the single
	// most damaging thing an installer can do here â€” the site comes back up with
	// an empty tenant id and no database password.
	if got := readFile(t, site.envFile); got != operatorEdit {
		t.Errorf("the environment file after the upgrade is:\n%s\nwant the operator's own:\n%s",
			got, operatorEdit)
	}
	requirePOSIXModesOnThisHost(t)
	assertMode(t, site.envFile, 0o640, "a preserved env file is still a secrets file")

	// Replaced: the program, wholesale. B's bytes, B's new file, and none of A's
	// that B dropped. A partial merge is what leaves a site running two versions'
	// worth of rules with no record of which.
	if got := readFile(t, filepath.Join(site.program, "security-core")); got != releaseB["security-core"] {
		t.Errorf("the core binary is %q after the upgrade, want %q", got, releaseB["security-core"])
	}
	if got := readFile(t, filepath.Join(site.program, "rules", "builtin.yaml")); got != releaseB["rules/builtin.yaml"] {
		t.Errorf("the rules file is %q after the upgrade, want B's", got)
	}
	if _, err := os.Stat(filepath.Join(site.program, "migrations", "README.txt")); err != nil {
		t.Errorf("a file only B ships is not in the program directory: %v", err)
	}
	// The staging directory must not survive a successful swap. A leftover
	// programDir.staging is a full second copy of a release nobody is running.
	if _, err := os.Stat(site.program + ".staging"); !os.IsNotExist(err) {
		t.Errorf("the staging directory still exists after the upgrade: %v", err)
	}

	// The units are not the upgrade's business. install writes them; upgrade
	// replaces the program directory and leaves them alone, so a unit the operator
	// has customised is not silently rewritten by the command they run to patch
	// the site. Stated as an assertion so a future change to that ordering is a
	// test failure rather than a surprise at a site.
	if got := readFile(t, filepath.Join(site.unitDir, "security-core.service")); got != unitBefore {
		t.Errorf("the upgrade rewrote the unit file.\ninstall owns the units and upgrade does " +
			"not; a change here means a unit an operator customised is being replaced by the " +
			"command they ran to fix something else")
	}

	// The recorded version moved, and the version it replaced is recorded as the
	// rollback target.
	state, err := lifecycle.ReadState(site.program)
	if err != nil {
		t.Fatalf("ReadState after the upgrade: %v", err)
	}
	if state.Version != "0.2.0" {
		t.Errorf("the state records version %q after upgrading to 0.2.0", state.Version)
	}
	if state.Previous != "0.1.0" {
		t.Errorf("the state records previous = %q, want 0.1.0. The rollback path reads exactly "+
			"this field, and an empty one is a site whose rollback target does not exist",
			state.Previous)
	}
	// The manifest is written beside the binaries, not just beside the archive,
	// because readVersionWindow reads the *previous* install's own manifest to
	// decide whether it can read the current schema.
	if _, err := os.Stat(filepath.Join(site.program, "release-manifest.json")); err != nil {
		t.Errorf("no release manifest beside the binaries: %v. The rollback window check "+
			"reads the previous install's manifest from there", err)
	}
}

// The one thing an upgrade must not break. The old program directory is moved
// aside rather than deleted, so a rollback has something to roll back to; a
// rollback that has to first rebuild the previous version is a rollback that does
// not happen at 3am.
func TestThePreviousVersionIsStillOnDiskAfterAnUpgrade(t *testing.T) {
	site := newSite(t)
	relA := buildRelease(t, "0.1.0", testCompat(t), releaseA)
	mustRun(t, site, "install",
		"--archive", relA.archive, "--program-dir", site.program, "--trusted-key", relA.pubHex)

	relB := buildRelease(t, "0.2.0", testCompat(t), releaseB)
	mustRun(t, site, "upgrade",
		"--archive", relB.archive, "--program-dir", site.program,
		"--trusted-key", relB.pubHex, "--backup-dir", site.backups)

	prev := site.program + ".previous"
	// A's actual bytes, so "still on disk" means runnable rather than merely
	// present as an empty directory.
	if got := readFile(t, filepath.Join(prev, "security-core")); got != releaseA["security-core"] {
		t.Errorf("the previous install's core is %q, want A's %q. The upgrade deleted the "+
			"version the rollback path exists to return to", got, releaseA["security-core"])
	}
	if got := readFile(t, filepath.Join(prev, "rules", "builtin.yaml")); got != releaseA["rules/builtin.yaml"] {
		t.Errorf("the previous install's rules file is %q, want A's", got)
	}
	// And its own manifest, which is the only record of the window the rollback
	// decides against. readVersionWindow refuses without it, so its absence turns
	// a working rollback into a refusal at exactly the wrong moment.
	//
	// readVersionWindow takes the live program directory and appends the suffix
	// itself â€” that is how the rollback command calls it, and calling it any
	// other way would test a path the shipped code never reads.
	prevManifest, err := readVersionWindow(site.program)
	if err != nil {
		t.Fatalf("the previous install has no readable release manifest: %v", err)
	}
	if prevManifest.MaxSchemaVersion != testCompat(t).MaxSchemaVersion {
		t.Errorf("the previous manifest declares a schema window ending at %d, want %d",
			prevManifest.MaxSchemaVersion, testCompat(t).MaxSchemaVersion)
	}
}

// The refusals. Each one is a state an operator can actually reach, and each one
// must change nothing.

// An archive whose core window excludes the core doing the installing. The
// rollback target is that same core, so installing it leaves a state nothing can
// recover from â€” which is why this is a refusal and not a warning.
func TestAnInstallWhoseCompatibilityWindowExcludesThisCoreIsRefused(t *testing.T) {
	site := newSite(t)
	rel := buildRelease(t, "0.1.0", release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 20,
		MinCoreVersion: "2.0.0", MaxCoreVersion: "2.x",
		ContractsVersion: "security/v1",
	}, releaseA)

	code, out := runCtl(t, site, "install",
		"--archive", rel.archive, "--program-dir", site.program, "--trusted-key", rel.pubHex)
	if code != command.ExitRefused {
		t.Fatalf("installing an archive built for core 2.x returned %d, want a refusal (%d)\n%s",
			code, command.ExitRefused, out)
	}
	if !strings.Contains(out, "core window") {
		t.Errorf("the refusal does not name the core window, which is the thing that failed:\n%s", out)
	}
	if _, err := os.Stat(site.program); !os.IsNotExist(err) {
		t.Errorf("a refused install created %s anyway: %v", site.program, err)
	}
}

// The other direction of the same rule, on the same build: an archive declaring a
// schema window this build cannot read. Installing it would produce a database
// this binary misreads, and the rollback would be to the same binary.
func TestAnInstallWhoseSchemaWindowIsOutsideThisBuildIsRefused(t *testing.T) {
	site := newSite(t)
	rel := buildRelease(t, "0.1.0", release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 4000,
		MinCoreVersion: "0.1.0", MaxCoreVersion: "0.x",
		ContractsVersion: "security/v1",
	}, releaseA)

	code, out := runCtl(t, site, "install",
		"--archive", rel.archive, "--program-dir", site.program, "--trusted-key", rel.pubHex)
	if code != command.ExitRefused {
		t.Fatalf("installing an archive needing schema 4000 returned %d, want a refusal (%d)\n%s",
			code, command.ExitRefused, out)
	}
	if !strings.Contains(out, "schema") {
		t.Errorf("the refusal does not name the schema window:\n%s", out)
	}
}

// A rollback to a version that does not exist. Two shapes of it: a first install
// with no earlier version, and an upgrade whose previous directory is gone.
func TestARollbackToAVersionThatDoesNotExistIsRefused(t *testing.T) {
	t.Run("there is no earlier version", func(t *testing.T) {
		site := newSite(t)
		rel := buildRelease(t, "0.1.0", testCompat(t), releaseA)
		mustRun(t, site, "install",
			"--archive", rel.archive, "--program-dir", site.program, "--trusted-key", rel.pubHex)

		code, out := runCtl(t, site, "rollback", "--program-dir", site.program)
		if code != command.ExitRefused {
			t.Fatalf("a rollback from a first install returned %d, want a refusal (%d)\n%s",
				code, command.ExitRefused, out)
		}
		if !strings.Contains(out, "first install") {
			t.Errorf("the refusal does not say the install had no predecessor:\n%s", out)
		}
	})

	t.Run("the previous version is not on disk", func(t *testing.T) {
		site := newSite(t)
		relA := buildRelease(t, "0.1.0", testCompat(t), releaseA)
		mustRun(t, site, "install",
			"--archive", relA.archive, "--program-dir", site.program, "--trusted-key", relA.pubHex)
		relB := buildRelease(t, "0.2.0", testCompat(t), releaseB)
		mustRun(t, site, "upgrade",
			"--archive", relB.archive, "--program-dir", site.program,
			"--trusted-key", relB.pubHex, "--backup-dir", site.backups)

		// The state still names 0.1.0 as the rollback target, and the target is
		// gone. Guessing at the window is what this refusal exists to prevent, and
		// a rollback that guesses half-succeeds.
		if err := os.RemoveAll(site.program + ".previous"); err != nil {
			t.Fatalf("remove the previous install: %v", err)
		}
		code, out := runCtl(t, site, "rollback", "--program-dir", site.program)
		if code != command.ExitRefused {
			t.Fatalf("a rollback with no previous install on disk returned %d, want a refusal (%d)\n%s",
				code, command.ExitRefused, out)
		}
		if !strings.Contains(out, "release manifest") {
			t.Errorf("the refusal does not say the basis for the decision is missing:\n%s", out)
		}
	})

	t.Run("nothing is installed at all", func(t *testing.T) {
		site := newSite(t)
		code, out := runCtl(t, site, "rollback", "--program-dir", site.program)
		if code != command.ExitRefused {
			t.Fatalf("a rollback against an empty directory returned %d, want a refusal (%d)\n%s",
				code, command.ExitRefused, out)
		}
	})
}

// Upgrading the version that is already installed. The program directory is the
// one thing an operator may have patched, and overwriting it with the same
// bytes-and-all is how a local fix is lost with no record.
func TestUpgradingToTheVersionAlreadyInstalledIsRefused(t *testing.T) {
	site := newSite(t)
	rel := buildRelease(t, "0.1.0", testCompat(t), releaseA)
	mustRun(t, site, "install",
		"--archive", rel.archive, "--program-dir", site.program, "--trusted-key", rel.pubHex)

	code, out := runCtl(t, site, "upgrade",
		"--archive", rel.archive, "--program-dir", site.program,
		"--trusted-key", rel.pubHex, "--backup-dir", site.backups)
	if code != command.ExitRefused {
		t.Fatalf("upgrading to the installed version returned %d, want a refusal (%d)\n%s",
			code, command.ExitRefused, out)
	}
	if !strings.Contains(out, "already installed") {
		t.Errorf("the refusal does not say the version is the installed one:\n%s", out)
	}
	// And the install is untouched.
	state, err := lifecycle.ReadState(site.program)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Previous != "" {
		t.Errorf("a refused upgrade recorded previous = %q; it changed the install it refused", state.Previous)
	}
}

// And the upgrade refuses on a directory with nothing in it, rather than
// performing a first install under a command whose whole contract is that the
// previous install stays runnable.
func TestUpgradingAnEmptyDirectoryIsRefused(t *testing.T) {
	site := newSite(t)
	rel := buildRelease(t, "0.1.0", testCompat(t), releaseA)
	code, out := runCtl(t, site, "upgrade",
		"--archive", rel.archive, "--program-dir", site.program,
		"--trusted-key", rel.pubHex, "--backup-dir", site.backups)
	if code != command.ExitRefused {
		t.Fatalf("upgrading an empty directory returned %d, want a refusal (%d)\n%s",
			code, command.ExitRefused, out)
	}
	if !strings.Contains(out, "install") {
		t.Errorf("the refusal does not point at the command that would work:\n%s", out)
	}
}

// The rollback, run for real. Database-backed, because the window check reads
// the schema version the site is actually at â€” which is the whole point of the
// check, and the reason a rollback cannot be reasoned about offline.
func TestTheRollbackReturnsToThePreviousVersionWhenThatVersionCanReadTheSchema(t *testing.T) {
	site := newSite(t)
	requireMigratedSchema(t)

	relA := buildRelease(t, "0.1.0", testCompat(t), releaseA)
	mustRun(t, site, "install",
		"--archive", relA.archive, "--program-dir", site.program, "--trusted-key", relA.pubHex)
	relB := buildRelease(t, "0.2.0", testCompat(t), releaseB)
	mustRun(t, site, "upgrade",
		"--archive", relB.archive, "--program-dir", site.program,
		"--trusted-key", relB.pubHex, "--backup-dir", site.backups)

	if got := readFile(t, filepath.Join(site.program, "security-core")); got != releaseB["security-core"] {
		t.Fatalf("setup did not upgrade: the core is %q", got)
	}

	code, out := runCtl(t, site, "rollback", "--program-dir", site.program)
	if code != command.ExitOK {
		t.Fatalf("the rollback returned %d, want ok\n%s", code, out)
	}
	state, err := lifecycle.ReadState(site.program)
	if err != nil {
		t.Fatalf("ReadState after the rollback: %v", err)
	}
	if state.Version != "0.1.0" {
		t.Errorf("the state records %q after rolling back from 0.2.0, want 0.1.0", state.Version)
	}
	// The previous directory is the copy to roll back to, so it is still there
	// after the rollback. Nothing in the rollback path deletes it.
	if _, err := os.Stat(site.program + ".previous"); err != nil {
		t.Errorf("the rollback removed the previous install: %v. A second rollback would "+
			"have nothing to return to", err)
	}
}

// The other half of the window check: a previous version that cannot read the
// schema the site is at. Rolling the program back would leave a site no binary
// supports, and the only state both versions can serve is a restore from the
// pre-upgrade backup â€” so the rollback is refused and says so.
func TestTheRollbackIsRefusedWhenThePreviousVersionCannotReadTheSchema(t *testing.T) {
	site := newSite(t)
	requireMigratedSchema(t)

	// A is installed and then replaced by B. A's own declared window is
	// deliberately narrow, so the database â€” which is at the current schema
	// version â€” is outside it.
	narrow := release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 1,
		MinCoreVersion: "0.1.0", MaxCoreVersion: "0.x", ContractsVersion: "security/v1",
	}
	relA := buildRelease(t, "0.1.0", narrow, releaseA)
	mustRun(t, site, "install",
		"--archive", relA.archive, "--program-dir", site.program, "--trusted-key", relA.pubHex)
	relB := buildRelease(t, "0.2.0", testCompat(t), releaseB)
	mustRun(t, site, "upgrade",
		"--archive", relB.archive, "--program-dir", site.program,
		"--trusted-key", relB.pubHex, "--backup-dir", site.backups)

	code, out := runCtl(t, site, "rollback", "--program-dir", site.program)
	if code != command.ExitRefused {
		t.Fatalf("a rollback onto a version that cannot read the schema returned %d, want a "+
			"refusal (%d)\n%s", code, command.ExitRefused, out)
	}
	if !strings.Contains(out, "Restore the database") {
		t.Errorf("the refusal does not name the only state that is safe â€” a restore:\n%s", out)
	}
	// And it did not move: a refused rollback that half-applies is worse than
	// either outcome.
	state, err := lifecycle.ReadState(site.program)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Version != "0.2.0" {
		t.Errorf("a refused rollback left the state at %q; it changed the install it refused", state.Version)
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// site is one machine: a program directory, a unit directory, an env file and a
// backup directory, all under one temporary root.
type site struct {
	root     string
	program  string
	unitDir  string
	envFile  string
	backups  string
	dumpFile string
}

// newSite lays out a machine and points the installer's environment at it.
//
// The environment variables rather than only the flags matter: WriteUnits
// defaults its env file to /etc/security-platform/security-platform.env, and a
// test that did not override it would write outside the temporary directory.
func newSite(t *testing.T) *site {
	t.Helper()
	root := t.TempDir()
	s := &site{
		root:     root,
		program:  filepath.Join(root, "opt", "security-platform"),
		unitDir:  filepath.Join(root, "etc", "systemd", "system"),
		envFile:  filepath.Join(root, "etc", "security-platform", "security-platform.env"),
		backups:  filepath.Join(root, "backups"),
		dumpFile: filepath.Join(root, "dump.sql"),
	}
	// The pre-upgrade backup. runBackup deliberately does not shell out to
	// pg_dump â€” a backup that depends on a tool being present fails silently on
	// the site that needed it â€” so it takes a dump the operator produced and
	// copies it. This is that dump.
	if err := os.WriteFile(s.dumpFile, []byte("-- a database dump\n"), 0o600); err != nil {
		t.Fatalf("write the dump: %v", err)
	}
	t.Setenv("SP_UNIT_DIR", s.unitDir)
	t.Setenv("SP_ENV_FILE", s.envFile)
	t.Setenv("SP_DATABASE_DUMP", s.dumpFile)
	// Explicitly empty: a test must not pick up the developer's evidence
	// directory, and an unset one is what runBackup records as an incomplete
	// backup.
	t.Setenv("SP_EVIDENCE_ROOT", "")
	return s
}

// testCompat is the compatibility window a test release declares: the window
// this build itself publishes, so the install is the normal forward case.
func testCompat(t *testing.T) release.Compatibility {
	return release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: buildMaxSchemaVersion(t),
		MinCoreVersion: "0.1.0", MaxCoreVersion: "0.x",
		ContractsVersion: "security/v1",
	}
}

type builtRelease struct {
	archive string
	pubHex  string
}

// buildRelease builds and signs a real archive through release.Build, and hands
// back the archive path. The manifest lands beside it under the name install and
// upgrade default to, so the commands read it the way they read an operator's.
func buildRelease(t *testing.T, version string, compat release.Compatibility, files map[string]string) builtRelease {
	t.Helper()
	outDir := filepath.Join(t.TempDir(), "dist")

	inputs := make([]release.SourceFile, 0, len(files)+1)
	for name, body := range files {
		src := filepath.Join(outDir, "src-"+strings.ReplaceAll(name, "/", "-"))
		if err := os.MkdirAll(filepath.Dir(src), 0o750); err != nil {
			t.Fatalf("create the source directory: %v", err)
		}
		if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		inputs = append(inputs, release.SourceFile{
			ArchivePath: name, SourcePath: src, Mode: "0755",
		})
	}
	const sbomBody = `{"spdxVersion":"SPDX-2.3"}`
	sbomSrc := filepath.Join(outDir, "sbom.spdx.json")
	if err := os.WriteFile(sbomSrc, []byte(sbomBody), 0o600); err != nil {
		t.Fatalf("write the sbom: %v", err)
	}
	inputs = append(inputs, release.SourceFile{ArchivePath: "sbom.spdx.json", SourcePath: sbomSrc})

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate a signing key: %v", err)
	}
	// The migrations the manifest declares are the ones this build actually
	// carries, not a hand-written list. A release whose manifest disagrees with
	// the binary is the drift the whole ledger is about.
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatalf("embeddedMigrations: %v", err)
	}
	archive, manifest, err := release.Build(outDir, release.BuildInput{
		Version: version, Platform: "linux-amd64", Files: inputs,
		Migrations: migrations, Compatibility: compat,
		SBOM:    release.SBOM{Path: "sbom.spdx.json", Format: "spdx-json"},
		Builder: "test",
	}, priv, "test-key")
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	// The manifest declares the SBOM's hash, so it has to be the hash of the
	// bytes that went in. Build writes a manifest with the field empty; signing
	// that would sign a manifest that claims nothing about a file it lists.
	raw, err := os.ReadFile(sbomSrc)
	if err != nil {
		t.Fatalf("read the sbom back: %v", err)
	}
	manifest.SBOM.SHA256 = sha256Hex(raw)
	if err := release.Sign(&manifest, priv, "test-key", pub); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	writeManifest(t, archive+".manifest.json", manifest)

	return builtRelease{archive: archive, pubHex: hex.EncodeToString(pub)}
}

func mustRun(t *testing.T, s *site, name string, args ...string) string {
	t.Helper()
	code, out := runCtl(t, s, name, args...)
	if code != command.ExitOK {
		t.Fatalf("securityctl %s returned %d, want ok\n%s", name, code, out)
	}
	return out
}

func runCtl(t *testing.T, _ *site, name string, args ...string) (command.ExitCode, string) {
	t.Helper()
	var buf bytes.Buffer
	env := &command.Env{Stdin: strings.NewReader(""), Stdout: &buf, Stderr: &buf}
	code := newRootCommand().Execute(context.Background(), env, append([]string{name}, args...))
	return code, buf.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// buildMaxSchemaVersion is the top of the schema window this build publishes,
// read from the embedded manifest rather than written down here. A test that
// hardcoded it would keep passing after the window moved, and would then be
// building releases against a window this binary does not support.
func buildMaxSchemaVersion(t *testing.T) int {
	t.Helper()
	c, err := migrationCompatibility()
	if err != nil {
		t.Fatalf("migrationCompatibility: %v", err)
	}
	return c.MaxSchemaVersion
}

// writeManifest puts the re-signed manifest where install and upgrade look for
// it by default: beside the archive, under the same name release.Build uses.
func writeManifest(t *testing.T, path string, m release.Manifest) {
	t.Helper()
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("encode the manifest: %v", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o640); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// requirePOSIXModesOnThisHost skips the mode assertions where the filesystem has
// no modes to assert.
//
// Windows has a read-only flag and nothing else: os.Chmod there cannot express
// 0640 and os.Stat reports a synthesised mode. The control is about a Linux
// install, which is the only place this product is installed; a Windows run of
// the mode assertions would be testing NTFS. It is skipped, loudly, and the rest
// of the install is still asserted on every platform.
func requirePOSIXModesOnThisHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits; the mode assertions are about a " +
			"Linux install. The rest of this test still runs")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode, why string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s is mode %o, want %o: %s", path, got, want, why)
	}
}

// requireMigratedSchema points the CLI at the test database and brings it to the
// current version, so the rollback's window check has a real schema version to
// read. It skips rather than fails when no database is configured, like every
// other database-backed test in this repository.
func requireMigratedSchema(t *testing.T) {
	t.Helper()
	raw := testutil.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := (&migrate.Runner{Pool: raw}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Setenv("SP_DATABASE_URL", os.Getenv(testutil.DatabaseURLEnv))
	t.Setenv("SP_DATABASE_PASSWORD", os.Getenv("SP_TEST_DATABASE_PASSWORD"))
}
