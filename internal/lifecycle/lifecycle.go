// Package lifecycle implements install, upgrade, rollback, backup and restore.
//
// These are the five commands that were left as skeletons, and they are the
// difference between a repository and a product: everything else runs from a
// build tree, and these run from a signed archive on a machine with no network.
//
// The properties that shape the whole package:
//
//   - Nothing is trusted before it is verified. Install verifies the archive and
//     its signature with a caller-supplied key before extracting a byte, and
//     extraction itself re-checks every path, because a manifest that was
//     verified and a tar that was extracted are two separate claims.
//   - An upgrade is a backup, then a migration, then a health check, and it
//     leaves the previous install runnable if any of those fail. The plan is
//     explicit that a failed upgrade must not take a site down.
//   - A backup is self-consistent or it does not exist. A database dump without
//     the evidence directory produces an archive whose incidents cite bytes that
//     are not in it, which is worse than no backup because it looks complete.
//   - Rollback never downgrades the schema. If the previous binary cannot read
//     the current schema version, the rollback is refused and says so.
package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

// ErrRefused is a deliberate refusal rather than a failure: the precondition was
// not met and acting anyway would be wrong. It is distinct so a caller can say
// "no" instead of "try again".
var ErrRefused = errors.New("lifecycle: refused")

// Refuse builds a refusal naming the condition that failed.
func Refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// State is an install's on-disk state, read from the install directory.
type State struct {
	// Version is the installed release's version, empty for a fresh directory.
	Version string
	// Platform the release was built for.
	Platform string
	// SchemaVersion the release declared, not the one the database is at. Those
	// two being different is the normal state between an expand and its
	// contract.
	SchemaVersion int
	// ContractsVersion the release was built against.
	ContractsVersion string
	// InstalledAt is when this version was written.
	InstalledAt time.Time
	// Previous is the version this one replaced, for rollback.
	Previous string
	// ProgramDir holds the binaries, distinct from data, config, evidence, logs
	// and secrets. A program directory that also holds a database has no
	// rollback, because overwriting the program overwrites the data.
	ProgramDir string
	// KeyID that signed this install, recorded so an operator can tell which
	// release key a site trusts.
	KeyID string
}

const stateFile = "install-state.json"

// stateSchemaVersion is the state file's own version, bumped when its shape
// changes. An installer that cannot read the state it finds must refuse rather
// than assume.
const stateSchemaVersion = 1

type stateFileFormat struct {
	SchemaVersion int       `json:"schemaVersion"`
	Version       string    `json:"version"`
	Platform      string    `json:"platform"`
	MinSchema     int       `json:"minSchemaVersion"`
	MaxSchema     int       `json:"maxSchemaVersion"`
	Contracts     string    `json:"contractsVersion"`
	InstalledAt   time.Time `json:"installedAt"`
	Previous      string    `json:"previous,omitempty"`
	ProgramDir    string    `json:"programDir"`
	KeyID         string    `json:"keyId"`
}

// ReadState reads an install's state.
//
// A missing file is a fresh directory, not an error: the first install has
// nothing to read. A file that exists and cannot be parsed is an error, because
// overwriting it would destroy the only record of what this machine was running.
func ReadState(programDir string) (State, error) {
	var s State
	raw, err := os.ReadFile(filepath.Join(programDir, stateFile))
	if err != nil {
		if os.IsNotExist(err) {
			s.ProgramDir = programDir
			return s, nil
		}
		return s, fmt.Errorf("lifecycle: read install state: %w", err)
	}
	var f stateFileFormat
	if err := unmarshalJSON(raw, &f); err != nil {
		return s, fmt.Errorf("lifecycle: parse install state: %w. The state file is the only "+
			"record of what this machine was running, so it is not overwritten; move it aside "+
			"deliberately to reinstall", err)
	}
	if f.SchemaVersion != stateSchemaVersion {
		return s, Refuse("the install state is version %d, this build reads %d. Refusing rather "+
			"than guessing at a layout written by another installer", f.SchemaVersion, stateSchemaVersion)
	}
	return State{
		Version: f.Version, Platform: f.Platform,
		SchemaVersion: f.MaxSchema, ContractsVersion: f.Contracts,
		InstalledAt: f.InstalledAt, Previous: f.Previous,
		ProgramDir: f.ProgramDir, KeyID: f.KeyID,
	}, nil
}

// WriteState records an install.
func WriteState(s State) error {
	f := stateFileFormat{
		SchemaVersion: stateSchemaVersion,
		Version:       s.Version, Platform: s.Platform,
		MinSchema: 1, MaxSchema: s.SchemaVersion, Contracts: s.ContractsVersion,
		InstalledAt: s.InstalledAt, Previous: s.Previous,
		ProgramDir: s.ProgramDir, KeyID: s.KeyID,
	}
	body, err := marshalJSON(f)
	if err != nil {
		return fmt.Errorf("lifecycle: encode install state: %w", err)
	}
	// Written via a temporary and renamed, so a crash mid-write leaves the
	// previous state readable rather than a truncated file that refuses to parse
	// on the next boot.
	path := filepath.Join(s.ProgramDir, stateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o640); err != nil {
		return fmt.Errorf("lifecycle: write install state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("lifecycle: publish install state: %w", err)
	}
	return nil
}

// Plan is what a Verify found, and what Install and Upgrade refuse to ignore.
type Plan struct {
	Manifest    release.Manifest
	Files       []release.FileEntry
	ArchivePath string
	// Existing is the install being replaced, empty for a first install.
	Existing State
	// IsUpgrade is true when a version is already installed.
	IsUpgrade bool
	// SchemaWindowOverlaps says whether this build can read the database the
	// archive expects. False is the case that half-upgrades a site.
	SchemaWindowOverlaps bool
	// HasContractStep is set when the archive carries a contract migration. The
	// plan refuses to install one in the same archive as its expand.
	HasContractStep bool
	// ContractVersions names the contract migrations, so the refusal can point
	// at them.
	ContractVersions []int
}

// Verify checks an archive and reports what installing it would do.
//
// It is separate from Install so `preflight` can run it on a machine that is
// about to be upgraded, and so the refusal reasons are readable before anything
// has been touched.
func Verify(archivePath, programDir string, manifest release.Manifest, trustedKey []byte, buildCompatibility release.Compatibility) (Plan, error) {
	var p Plan
	p.ArchivePath = archivePath
	p.Manifest = manifest

	existing, err := ReadState(programDir)
	if err != nil {
		return p, err
	}
	p.Existing = existing
	p.IsUpgrade = existing.Version != ""

	files, err := release.Verify(archivePath, manifest, trustedKey)
	if err != nil {
		return p, err
	}
	p.Files = files

	for _, m := range manifest.Migrations {
		if m.Phase == "contract" {
			p.HasContractStep = true
			p.ContractVersions = append(p.ContractVersions, m.Version)
		}
	}
	sort.Ints(p.ContractVersions)

	// The archive's required window must be inside this build's window, not
	// merely overlapping it. An archive needing schemas 1..9 installed under a
	// build that supports 1..3 produces a database this binary cannot read, and a
	// site in that state is one no rollback can undo: the rollback is to the
	// same insufficient build.
	//
	// The test for the reverse - a build that supports 1..12 installing an
	// archive needing 1..9 - passes, and is the normal forward case.
	archiveMin := manifest.Compatibility.MinSchemaVersion
	archiveMax := manifest.Compatibility.MaxSchemaVersion
	p.SchemaWindowOverlaps = buildCompatibility.MinSchemaVersion <= archiveMin &&
		archiveMax <= buildCompatibility.MaxSchemaVersion
	return p, nil
}

// CheckRefusals applies the rules that stop a bad install, in the order an
// operator would want to hear about them.
//
// Order matters: a manifest that will not verify is reported before a schema
// window that will not overlap, because the first is a security problem and the
// second is a configuration one.
func CheckRefusals(p Plan) error {
	if !p.SchemaWindowOverlaps {
		return Refuse(
			"this build reads schema versions %s and the archive needs %d to %d. The archive's "+
				"window is not inside this build's, so installing would produce a database this "+
				"binary cannot read — and the rollback target is the same insufficient build, "+
				"so the state cannot be recovered from",
			windowText(p.Manifest.Compatibility), p.Manifest.Compatibility.MinSchemaVersion,
			p.Manifest.Compatibility.MaxSchemaVersion)
	}
	if p.HasContractStep {
		return Refuse(
			"the archive contains contract migration(s) %v. A contract step may never ship in the "+
				"same release as the expand that makes it safe: the window in which both the old "+
				"and the new binary must work is the entire point of expand/contract, and "+
				"collapsing it is how an upgrade takes a site down at 3am",
			p.ContractVersions)
	}
	if p.IsUpgrade && p.Manifest.Version == p.Existing.Version {
		return Refuse("version %s is already installed. Reinstalling the same version would "+
			"overwrite a program directory the operator may have patched",
			p.Manifest.Version)
	}
	return nil
}

// windowText names a schema window for a diagnostic.
func windowText(c release.Compatibility) string {
	if c.MinSchemaVersion == c.MaxSchemaVersion {
		return fmt.Sprintf("exactly %d", c.MinSchemaVersion)
	}
	return fmt.Sprintf("%d to %d", c.MinSchemaVersion, c.MaxSchemaVersion)
}

// Describe renders a plan for an operator, so a refusal or an install reads as a
// list of consequences rather than as a log line.
func Describe(p Plan) string {
	var b strings.Builder
	kind := "install"
	if p.IsUpgrade {
		kind = "upgrade"
	}
	fmt.Fprintf(&b, "%s %s (%s)\n", kind, p.Manifest.Version, p.Manifest.Platform)
	fmt.Fprintf(&b, "  files        : %d, all verified against the manifest\n", len(p.Files))
	fmt.Fprintf(&b, "  signed by    : %s\n", p.Manifest.Signature.KeyID)
	fmt.Fprintf(&b, "  schema window: [%d, %d]\n",
		p.Manifest.Compatibility.MinSchemaVersion, p.Manifest.Compatibility.MaxSchemaVersion)
	fmt.Fprintf(&b, "  migrations   : %d\n", len(p.Manifest.Migrations))
	if p.IsUpgrade {
		fmt.Fprintf(&b, "  replacing   : %s (installed %s)\n",
			p.Existing.Version, p.Existing.InstalledAt.Format("2006-01-02"))
		if p.Existing.Previous != "" {
			fmt.Fprintf(&b, "  rollback to : %s\n", p.Existing.Previous)
		}
	}
	if p.HasContractStep {
		fmt.Fprintf(&b, "  REFUSED     : contains contract migration(s) %v\n", p.ContractVersions)
	}
	if !p.SchemaWindowOverlaps {
		b.WriteString("  REFUSED     : the schema windows do not overlap\n")
	}
	return b.String()
}
