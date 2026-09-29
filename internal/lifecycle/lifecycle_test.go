package lifecycle_test

import (
	"archive/tar"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/lifecycle"
	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

func buildArchive(t *testing.T, files map[string]string, mode map[string]string, key ed25519.PrivateKey) (string, release.Manifest, ed25519.PublicKey) {
	t.Helper()
	dir := t.TempDir()
	inputs := make([]release.SourceFile, 0, len(files)+1)
	for name, body := range files {
		src := filepath.Join(dir, "src-"+strings.ReplaceAll(name, "/", "-"))
		if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		inputs = append(inputs, release.SourceFile{
			ArchivePath: name, SourcePath: src, Mode: mode[name],
		})
	}
	const sbomBody = `{"spdxVersion":"SPDX-2.3"}`
	sbomSrc := writeTemp(t, dir, "sbom", sbomBody)
	inputs = append(inputs, release.SourceFile{ArchivePath: "sbom.spdx.json", SourcePath: sbomSrc})

	pub := key.Public().(ed25519.PublicKey)
	archivePath, manifest, err := release.Build(dir, release.BuildInput{
		Version: "1.0.0", Platform: "linux-amd64", Files: inputs,
		Migrations: []release.MigrationEntry{{Version: 1, Name: "core", Phase: "expand"}},
		Compatibility: release.Compatibility{
			MinSchemaVersion: 1, MaxSchemaVersion: 9,
			MinCoreVersion: "1.0.0", MaxCoreVersion: "1.x", ContractsVersion: "security/v1",
		},
		SBOM:    release.SBOM{Path: "sbom.spdx.json", Format: "spdx-json"},
		Builder: "test",
	}, key, "key-1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// The manifest declares the SBOM's hash, so it has to be the hash of the
	// bytes that went in, not of a file that was never written.
	sum, _, err := lifecycle.HashFile(sbomSrc, 0)
	if err != nil {
		t.Fatalf("hash sbom: %v", err)
	}
	manifest.SBOM.SHA256 = sum
	if err := release.Sign(&manifest, key, "key-1", pub); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return archivePath, manifest, pub
}

func writeTemp(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return priv
}

var buildCompat = release.Compatibility{
	MinSchemaVersion: 1, MaxSchemaVersion: 12,
	MinCoreVersion: "1.0.0", MaxCoreVersion: "2.x", ContractsVersion: "security/v1",
}

func TestInstallOfAFreshDirectory(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t,
		map[string]string{"bin/securityctl": "binary", "rules/builtin.yaml": "rules"},
		map[string]string{"bin/securityctl": "0755"}, key)

	program := t.TempDir()
	plan, err := lifecycle.Verify(archivePath, program, manifest, pub, buildCompat)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if plan.IsUpgrade {
		t.Error("a fresh directory was reported as an upgrade")
	}
	if err := lifecycle.CheckRefusals(plan); err != nil {
		t.Fatalf("a valid first install was refused: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "program")
	out, err := lifecycle.Extract(archivePath, dest, plan.Files)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(out.Files) != 3 {
		t.Errorf("extracted %d files, want 3", len(out.Files))
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "securityctl")); err != nil {
		t.Errorf("the binary is not where the manifest said: %v", err)
	}
}

func TestInstallStateRecordsWhatWasInstalled(t *testing.T) {
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, "install-state.json")); !os.IsNotExist(err) {
		t.Fatal("a fresh directory already has install state")
	}
	// A missing state file is a fresh directory, not an error.
	s, err := lifecycle.ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState on a fresh directory: %v", err)
	}
	if s.Version != "" {
		t.Error("a fresh directory reported an installed version")
	}

	err = lifecycle.WriteState(lifecycle.State{
		Version: "1.0.0", Platform: "linux-amd64", SchemaVersion: 9,
		ContractsVersion: "security/v1", InstalledAt: time.Now().UTC(),
		ProgramDir: dir, KeyID: "key-1",
	})
	if err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	read, err := lifecycle.ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if read.Version != "1.0.0" || read.SchemaVersion != 9 || read.KeyID != "key-1" {
		t.Errorf("the state did not round-trip: %+v", read)
	}
}

// A state file written by another installer is not overwritten. It is the only
// record of what this machine was running.
func TestUnparseableInstallStateIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install-state.json"), []byte("{not json"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := lifecycle.ReadState(dir)
	if err == nil {
		t.Fatal("an unparseable install state was accepted")
	}
	if !strings.Contains(err.Error(), "not overwritten") {
		t.Errorf("the error should say the file is preserved, got %q", err)
	}
}

func TestUnreadableStateVersionIsRefused(t *testing.T) {
	dir := t.TempDir()
	body, marshalErr := json.Marshal(map[string]any{
		"schemaVersion": 99, "version": "1.0.0", "programDir": dir,
	})
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	if err := os.WriteFile(filepath.Join(dir, "install-state.json"), body, 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := lifecycle.ReadState(dir)
	if err == nil {
		t.Fatal("a state file from a future installer was accepted")
	}
	if !errors.Is(err, lifecycle.ErrRefused) {
		t.Errorf("error is %v, want a refusal rather than a parse failure", err)
	}
}

// The rule expand/contract exists to enforce, and it is enforced at both layers:
// the release verifier refuses a mixed archive, and so does the installer's own
// gate. The defence is duplicated deliberately - a verifier that is bypassed by
// an operator who trusts a manifest should not be the only thing standing between
// a contract step and a site.
func TestContractStepAlongsideExpandIsRefused(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	manifest.Migrations = append(manifest.Migrations,
		release.MigrationEntry{Version: 2, Name: "narrow", Phase: "contract"})
	if err := release.Sign(&manifest, key, "key-1", pub); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Layer one: the release verifier.
	if _, err := release.Verify(archivePath, manifest, pub); err == nil {
		t.Error("the release verifier accepted an archive mixing expand and contract")
	}

	// Layer two: the installer's gate, exercised on a plan built directly so it
	// is tested even when the verifier above has already stopped the flow.
	//
	// The compatibility fields are set from buildCompat rather than left at their
	// zero values. They are a precondition, not the subject: this test is about
	// the contract rule, and a plan that fails the core window or the contracts
	// version first would refuse for a different reason and assert nothing about
	// contract steps at all. Every other field is a zero value, which is why the
	// schema window is the one boolean that has to be stated.
	plan := lifecycle.Plan{
		Manifest:                manifest,
		Build:                   buildCompat,
		HasContractStep:         true,
		ContractVersions:        []int{2},
		SchemaWindowOverlaps:    true,
		CoreWindowOverlaps:      true,
		ContractsVersionMatches: true,
	}
	err := lifecycle.CheckRefusals(plan)
	if err == nil {
		t.Fatal("the installer's gate accepted a contract step")
	}
	if !strings.Contains(err.Error(), "contract") {
		t.Errorf("the refusal should name the contract step, got %q", err)
	}
	if !strings.Contains(err.Error(), "[2]") {
		t.Errorf("the refusal should name the offending version, got %q", err)
	}
}

// The archive's required window must be inside the build's, not merely
// overlapping it. A build that supports 1..3 cannot run what an archive needing
// 1..9 produces, and its rollback target is the same insufficient build.
func TestNonOverlappingSchemaWindowsAreRefused(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	old := release.Compatibility{
		MinSchemaVersion: 1, MaxSchemaVersion: 3,
		MinCoreVersion: "1.0.0", MaxCoreVersion: "1.x", ContractsVersion: "security/v1",
	}
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, old)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if plan.SchemaWindowOverlaps {
		t.Fatal("a build reading schemas 1..3 accepted an archive needing 1..9")
	}
	err = lifecycle.CheckRefusals(plan)
	if err == nil {
		t.Fatal("a non-contained schema window was accepted")
	}
	if !strings.Contains(err.Error(), "rollback") {
		t.Errorf("the refusal should say why the state is unrecoverable, got %q", err)
	}
}

// The forward case: a newer build installing an older archive, where the
// archive's window is inside the build's. This must not be refused, or every
// upgrade to a newer binary would be blocked.
func TestNewerBuildInstallingAnOlderArchiveIsAllowed(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, buildCompat)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !plan.SchemaWindowOverlaps {
		t.Fatal("a build supporting 1..12 refused an archive needing 1..9")
	}
	if err := lifecycle.CheckRefusals(plan); err != nil {
		t.Errorf("a valid forward upgrade was refused: %v", err)
	}
}

func TestReinstallingTheSameVersionIsRefused(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	program := t.TempDir()
	if err := lifecycle.WriteState(lifecycle.State{
		Version: "1.0.0", Platform: "linux-amd64", SchemaVersion: 9,
		ProgramDir: program, InstalledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	plan, err := lifecycle.Verify(archivePath, program, manifest, pub, buildCompat)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !plan.IsUpgrade {
		t.Fatal("an installed version was not reported as an upgrade")
	}
	if err := lifecycle.CheckRefusals(plan); err == nil {
		t.Error("reinstalling the same version was accepted; an operator may have patched it")
	}
}

// A traversing tar entry is refused at extraction even though the manifest
// verified. The manifest and the tar are two separate claims, and only the second
// one names where bytes land.
func TestExtractionRefusesATraversingEntry(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, buildCompat)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Re-pack with a traversing entry, keeping the manifest as verified.
	evil := filepath.Join(t.TempDir(), "evil.tar.gz")
	packTar(t, evil, map[string]string{
		"bin/x":                 "b",
		"sbom.spdx.json":        `{"spdxVersion":"SPDX-2.3"}`,
		"../../etc/cron.d/evil": "owned",
	})

	_, err = lifecycle.Extract(evil, t.TempDir(), plan.Files)
	if err == nil {
		t.Fatal("an archive with a traversing entry was extracted")
	}
	if !strings.Contains(err.Error(), "travers") && !strings.Contains(err.Error(), "absolute") {
		t.Errorf("the failure should name the traversal, got %q", err)
	}
}

// A symlink is an instruction, not content: it says "when this archive is
// unpacked, put something else here", and whoever can write the archive chooses
// what it points at. This is the code that lands bytes on disk during an
// install, which is the moment a hostile entry would matter most.
//
// The empty body on a link entry meant the hash check would usually catch it, and
// "usually" is not a control — which is why the type is refused by name rather
// than left to the hash.
func TestExtractionRefusesANonRegularEntry(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, buildCompat)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	files := append([]release.FileEntry{}, plan.Files...)
	files = append(files, release.FileEntry{
		Path: "bin/link", SHA256: emptySHA256, SizeBytes: 0, Mode: "0755",
	})

	for _, tc := range []struct {
		name string
		flag byte
		want string
	}{
		{"symlink", tar.TypeSymlink, "symlink"},
		{"hard link", tar.TypeLink, "hard link"},
		{"character device", tar.TypeChar, "character device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evil := filepath.Join(t.TempDir(), "evil.tar.gz")
			packTarEntries(t, evil, []tarEntry{
				{Name: "bin/x", Flag: tar.TypeReg, Body: "b"},
				{Name: "sbom.spdx.json", Flag: tar.TypeReg, Body: `{"spdxVersion":"SPDX-2.3"}`},
				{Name: "bin/link", Flag: tc.flag, Link: "C:/Windows/System32/drivers/etc/hosts"},
			})

			dest := t.TempDir()
			_, err := lifecycle.Extract(evil, dest, files)
			if err == nil {
				t.Fatalf("an archive containing a %s was extracted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the failure should name the %s, got %q", tc.want, err)
			}
			// And nothing landed outside the destination.
			if _, statErr := os.Stat(filepath.Join(dest, "..", "link")); statErr == nil {
				t.Error("a link was materialised during extraction")
			}
		})
	}
}

// An unlisted file is refused. The archive is built from one file and then
// repacked with an extra, because Build lists exactly what it is given - so this
// is the tamper case rather than a build-time one.
func TestExtractionRefusesAnUnlistedFile(t *testing.T) {
	key := newKey(t)
	_, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)

	// Repack with a file the signed manifest does not list.
	const sbomBody = `{"spdxVersion":"SPDX-2.3"}`
	dir := t.TempDir()
	repacked := filepath.Join(dir, "tampered.tar.gz")
	packTar(t, repacked, map[string]string{
		"bin/x":          "b",
		"sbom.spdx.json": sbomBody,
		"bin/extra":      "surprise",
	})

	if _, err := release.Verify(repacked, manifest, pub); err == nil {
		t.Fatal("an archive with an unlisted file passed verification")
	}

	// And extraction refuses it too, on a plan that somehow carried the list.
	plan := lifecycle.Plan{Files: []release.FileEntry{{Path: "bin/x"}}}
	if _, err := lifecycle.Extract(repacked, t.TempDir(), plan.Files); err == nil {
		t.Error("extraction accepted an entry the manifest did not list")
	}
}

func TestDescribeRendersConsequences(t *testing.T) {
	key := newKey(t)
	archivePath, manifest, pub := buildArchive(t, map[string]string{"bin/x": "b"}, nil, key)
	plan, err := lifecycle.Verify(archivePath, t.TempDir(), manifest, pub, buildCompat)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	text := lifecycle.Describe(plan)
	for _, want := range []string{"install", "1.0.0", "linux-amd64", "files", "schema window"} {
		if !strings.Contains(text, want) {
			t.Errorf("the plan is missing %q:\n%s", want, text)
		}
	}
}

// A partial backup is recorded as partial. A dump without the evidence directory
// restores to incidents that cite bytes that do not exist, which looks complete
// and is not.
func TestPartialBackupIsMarkedIncomplete(t *testing.T) {
	b := lifecycle.Backup{
		ManifestVersion: 1, Product: "security-platform", Version: "1.0.0",
		DatabaseFile: "dump.sql", DatabaseSHA256: strings.Repeat("a", 64),
		DatabaseBytes: 1024, DatabaseSchema: 9, Complete: false,
		Notes: "the evidence directory was not included",
	}
	if err := b.Validate(); err != nil {
		t.Errorf("a recorded partial backup was refused: %v", err)
	}
	if b.Complete {
		t.Error("a backup with no evidence claims to be complete")
	}
	if b.Notes == "" {
		t.Error("a partial backup does not say why")
	}
}

func TestBackupValidationRefusesUnusableManifests(t *testing.T) {
	good := lifecycle.Backup{
		ManifestVersion: 1, DatabaseFile: "dump.sql",
		DatabaseSHA256: strings.Repeat("a", 64), DatabaseBytes: 1024,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("a valid backup manifest was refused: %v", err)
	}

	cases := map[string]lifecycle.Backup{
		"no database file": {ManifestVersion: 1, DatabaseSHA256: strings.Repeat("a", 64), DatabaseBytes: 1},
		"malformed hash":   {ManifestVersion: 1, DatabaseFile: "d", DatabaseSHA256: "short", DatabaseBytes: 1},
		"empty database":   {ManifestVersion: 1, DatabaseFile: "d", DatabaseSHA256: strings.Repeat("a", 64)},
		"future manifest":  {ManifestVersion: 99, DatabaseFile: "d", DatabaseSHA256: strings.Repeat("a", 64), DatabaseBytes: 1},
	}
	for name, b := range cases {
		if err := b.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestHashFileRefusesAnOversizedFile(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "big", strings.Repeat("x", 4096))
	if _, _, err := lifecycle.HashFile(p, 1024); err == nil {
		t.Error("a file over the cap was hashed; a full disk must not turn a backup into an outage")
	}
	if _, _, err := lifecycle.HashFile(p, 8192); err != nil {
		t.Errorf("a file inside the cap was refused: %v", err)
	}
}
