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
	"strconv"
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
	// Build is the compatibility window of the binary running this install. It is
	// kept on the plan so a refusal can name both sides: "this build reads X, the
	// archive needs Y" is actionable and "incompatible" is not.
	Build release.Compatibility
	// SchemaWindowOverlaps says whether this build can read the database the
	// archive expects. False is the case that half-upgrades a site.
	SchemaWindowOverlaps bool
	// CoreWindowOverlaps says whether this build's core version is inside the
	// core window the archive declares it runs on.
	//
	// MinCoreVersion, MaxCoreVersion and ContractsVersion were signed, written
	// into the manifest, copied into the install state - and never read for a
	// decision by anything. A release declaring maxCoreVersion "0.x" installed
	// onto a 1.x core without a word, and because the fields are recorded in
	// Installed the incompatibility then looked compatible after the fact.
	//
	// CoreWindowError carries the reason when the window could not be compared
	// at all, which is refused rather than passed: a bound nobody can parse is a
	// bound nobody is enforcing.
	CoreWindowOverlaps bool
	CoreWindowError    string
	// ContractsVersionMatches says the archive speaks the same contracts this
	// build does. The two names the same wire format, and an archive built
	// against a different one produces incidents this binary cannot read and
	// fields it silently drops.
	ContractsVersionMatches bool
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
	p.Build = buildCompatibility

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

	// The same containment test, on the same build's own window, for the two
	// signed fields that were never compared. The shape is deliberately the
	// schema window's: the archive's window must sit inside the build's, not
	// merely overlap it, because the rollback target after a bad install is the
	// same build and not something that can read more.
	//
	// A version bound that does not parse is a refusal rather than a pass. The
	// field is signed by the release key, so an unparseable one is either a
	// broken release or a bad manifest, and both of those are a reason to stop
	// rather than a reason to install and hope.
	p.CoreWindowOverlaps, p.CoreWindowError = coreWindowContains(
		buildCompatibility, manifest.Compatibility)
	p.ContractsVersionMatches = manifest.Compatibility.ContractsVersion != "" &&
		buildCompatibility.ContractsVersion != "" &&
		manifest.Compatibility.ContractsVersion == buildCompatibility.ContractsVersion
	return p, nil
}

// coreWindowContains reports whether the archive's core window sits inside the
// build's.
//
// "Sits inside" is the same relation the schema window uses and for the same
// reason: a build that cannot be the rollback target is a build that must not be
// the upgrade target either. The comparison is on parsed versions rather than on
// strings because the declared bounds are ranges - "0.x", "2.x" - and "2.x" is
// not between "10.0.0" and "1.0.0" the way a string comparison would say it is.
func coreWindowContains(build, archive release.Compatibility) (bool, string) {
	buildMin, err := parseCoreVersion("minCoreVersion", build.MinCoreVersion)
	if err != nil {
		return false, err.Error()
	}
	buildMax, err := parseCoreVersion("maxCoreVersion", build.MaxCoreVersion)
	if err != nil {
		return false, err.Error()
	}
	archiveMin, err := parseCoreVersion("minCoreVersion", archive.MinCoreVersion)
	if err != nil {
		return false, err.Error()
	}
	archiveMax, err := parseCoreVersion("maxCoreVersion", archive.MaxCoreVersion)
	if err != nil {
		return false, err.Error()
	}
	if buildMin.atLeast(archiveMin) && archiveMax.atMost(buildMax) {
		return true, ""
	}
	return false, ""
}

// coreVersion is one parsed core-version bound.
//
// A component of "x" or "*" - and every component after it - is Unbounded, so
// "1.x" is [1, +inf, +inf] and means "any 1.y.z", which is what a manifest
// author writing "1.x" means and what a string comparison would not.
type coreVersion struct {
	parts     [3]int
	unbounded [3]bool
}

func parseCoreVersion(field, s string) (coreVersion, error) {
	var v coreVersion
	if strings.TrimSpace(s) == "" {
		return v, Refuse("%s is empty. A release that does not declare which core "+
			"versions it runs on cannot be checked against one, and an unchecked "+
			"bound is not a bound", field)
	}
	segments := strings.Split(strings.TrimSpace(s), ".")
	if len(segments) > 3 {
		return v, Refuse("%s %q has more than three components; this build reads "+
			"MAJOR.MINOR.PATCH and a bound it cannot read is a bound it cannot enforce",
			field, s)
	}
	for i, seg := range segments {
		switch seg {
		case "x", "X", "*":
			for j := i; j < 3; j++ {
				v.unbounded[j] = true
			}
		default:
			n, err := strconv.Atoi(seg)
			if err != nil || n < 0 {
				return v, Refuse("%s %q is not a version this build can compare. "+
					"Each component is a number, or x for \"any\"", field, s)
			}
			v.parts[i] = n
		}
	}
	return v, nil
}

// atLeast and atMost order a bound against another. An unbounded component is
// larger than any number, which is the whole meaning of "1.x" as an upper bound
// and the reason it is not comparable as a string.
func (v coreVersion) atLeast(other coreVersion) bool { return compareCoreVersions(v, other) >= 0 }
func (v coreVersion) atMost(other coreVersion) bool  { return compareCoreVersions(v, other) <= 0 }

func compareCoreVersions(a, b coreVersion) int {
	for i := range 3 {
		switch {
		case a.unbounded[i] && b.unbounded[i]:
			continue
		case a.unbounded[i]:
			return 1
		case b.unbounded[i]:
			return -1
		}
		if a.parts[i] != b.parts[i] {
			if a.parts[i] < b.parts[i] {
				return -1
			}
			return 1
		}
	}
	return 0
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
	// The core window and the contracts version are checked before the
	// expand/contract rule, and for the same reason the schema window is first:
	// each is a fact about whether these two binaries belong on one machine at
	// all, and the contract rule is about how the archive is put together.
	if p.CoreWindowError != "" {
		return Refuse("%s. The archive's core window is %s to %s and this build's is "+
			"%s to %s. A bound neither side can read is a bound nobody is checking",
			p.CoreWindowError,
			p.Manifest.Compatibility.MinCoreVersion, p.Manifest.Compatibility.MaxCoreVersion,
			p.Build.MinCoreVersion, p.Build.MaxCoreVersion)
	}
	if !p.CoreWindowOverlaps {
		return Refuse(
			"this build's core window is %s to %s and the archive runs on core %s to %s. "+
				"The archive's window is not inside this build's, so installing puts a core "+
				"and a release that were never in the same window on one site — and the "+
				"rollback target is the same core, so the state cannot be recovered from",
			p.Build.MinCoreVersion, p.Build.MaxCoreVersion,
			p.Manifest.Compatibility.MinCoreVersion, p.Manifest.Compatibility.MaxCoreVersion)
	}
	if !p.ContractsVersionMatches {
		return Refuse(
			"the archive speaks contracts %s and this build speaks %s. A release built "+
				"against a different wire format produces incidents this binary cannot read "+
				"and drops fields it does not recognise, so the two must not be mixed on one "+
				"site",
			quotedOrUndeclared(p.Manifest.Compatibility.ContractsVersion),
			quotedOrUndeclared(p.Build.ContractsVersion))
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

// quotedOrUndeclared renders a version name for a message. A name that is empty
// is said to be undeclared rather than printed as "", because "the archive speaks
// contracts " with nothing after it is not a sentence an operator can act on - and
// an undeclared name is the more likely of the two problems.
func quotedOrUndeclared(v string) string {
	if strings.TrimSpace(v) == "" {
		return "nothing (it declares no contracts version)"
	}
	return fmt.Sprintf("%q", v)
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
	// The core window and the contracts name are printed because they are signed
	// fields an operator is asked to trust. A plan that does not show them is a
	// plan that verified them without ever reading them, which is what used to
	// happen.
	fmt.Fprintf(&b, "  core window  : %s to %s (this build: %s to %s)\n",
		p.Manifest.Compatibility.MinCoreVersion, p.Manifest.Compatibility.MaxCoreVersion,
		p.Build.MinCoreVersion, p.Build.MaxCoreVersion)
	fmt.Fprintf(&b, "  contracts    : %s (this build: %s)\n",
		quotedOrUndeclared(p.Manifest.Compatibility.ContractsVersion),
		quotedOrUndeclared(p.Build.ContractsVersion))
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
	if p.CoreWindowError != "" {
		fmt.Fprintf(&b, "  REFUSED     : %s\n", p.CoreWindowError)
	} else if !p.CoreWindowOverlaps {
		fmt.Fprintf(&b, "  REFUSED     : the core window %s to %s is not inside this build's %s to %s\n",
			p.Manifest.Compatibility.MinCoreVersion, p.Manifest.Compatibility.MaxCoreVersion,
			p.Build.MinCoreVersion, p.Build.MaxCoreVersion)
	}
	if !p.ContractsVersionMatches {
		fmt.Fprintf(&b, "  REFUSED     : contracts %s against this build's %s\n",
			quotedOrUndeclared(p.Manifest.Compatibility.ContractsVersion),
			quotedOrUndeclared(p.Build.ContractsVersion))
	}
	return b.String()
}
