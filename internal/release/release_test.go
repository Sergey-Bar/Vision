package release_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

func keyPair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv, pub
}

func buildArchive(t *testing.T, files map[string][]byte) (string, release.Manifest, ed25519.PublicKey) {
	t.Helper()
	priv, pub := keyPair(t)
	dir := t.TempDir()

	inputs := make([]release.SourceFile, 0, len(files))
	for name, body := range files {
		src := filepath.Join(dir, "src-"+sanitize(name))
		if err := os.WriteFile(src, body, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		inputs = append(inputs, release.SourceFile{ArchivePath: name, SourcePath: src})
	}
	inputs = append(inputs, release.SourceFile{
		ArchivePath: "sbom.spdx.json",
		SourcePath:  writeTemp(t, dir, "sbom", []byte(`{"spdxVersion":"SPDX-2.3"}`)),
	})

	archivePath, manifest, err := release.Build(dir, release.BuildInput{
		Version:  "1.0.0",
		Platform: "windows-amd64",
		Files:    inputs,
		Migrations: []release.MigrationEntry{
			{Version: 1, Name: "core", Phase: "expand"},
		},
		Compatibility: release.Compatibility{
			MinSchemaVersion: 1, MaxSchemaVersion: 9,
			MinCoreVersion: "1.0.0", MaxCoreVersion: "1.x",
			ContractsVersion: "security/v1",
		},
		SBOM:    release.SBOM{Path: "sbom.spdx.json", Format: "spdx-json"},
		Builder: "test",
	}, priv, "key-1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Fill the SBOM hash in and re-sign, as a real builder would.
	manifest.SBOM.SHA256 = sha256Hex([]byte(`{"spdxVersion":"SPDX-2.3"}`))
	if err := release.Sign(&manifest, priv, "key-1", pub); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return archivePath, manifest, pub
}

func writeTemp(t *testing.T, dir, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func sanitize(name string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(name)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// A freshly built archive verifies against its own manifest and the key it was
// signed with. If this fails, nothing else in this file is worth reading.
func TestABuiltArchiveVerifies(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl":    []byte("binary"),
		"rules/builtin.yaml": []byte("apiVersion: security.io/v1\n"),
	})
	if _, err := release.Verify(archivePath, manifest, pub); err != nil {
		t.Fatalf("a freshly built archive did not verify: %v", err)
	}
}

// A tampered archive must fail, and the failure must name the file.
func TestTamperedArchiveIsRefused(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	rewriteTarball(t, archivePath, map[string][]byte{
		"bin/securityctl": []byte("TAMPERED"),
		"sbom.spdx.json":  []byte(`{"spdxVersion":"SPDX-2.3"}`),
	})

	_, err := release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("a tampered archive verified")
	}
	if !strings.Contains(err.Error(), "securityctl") {
		t.Errorf("the failure should name the altered file, got %q", err)
	}
}

// An unsigned archive is a claim, not evidence.
func TestUnsignedManifestIsRefused(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	manifest.Signature.Value = ""

	_, err := release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("an unsigned manifest verified")
	}
	if !strings.Contains(err.Error(), "unsigned") {
		t.Errorf("the failure should say it is unsigned, got %q", err)
	}
}

// An attacker who can write the archive can also sign it. Without a trusted key
// the signature proves only that they signed their own forgery.
func TestForeignSigningKeyIsRefused(t *testing.T) {
	archivePath, manifest, _ := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	_, trusted := keyPair(t)

	_, err := release.Verify(archivePath, manifest, trusted)
	if err == nil {
		t.Fatal("an archive signed by an untrusted key verified")
	}
	if !strings.Contains(err.Error(), "trusted key") {
		t.Errorf("the failure should name the trust failure, got %q", err)
	}
}

// A changed manifest does not verify against the old signature.
func TestManifestEditBreaksTheSignature(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	manifest.Version = "9.9.9"

	if _, err := release.Verify(archivePath, manifest, pub); err == nil {
		t.Fatal("an edited manifest verified against the original signature")
	}
}

// A file in the archive that the manifest does not list is a file nobody
// reviewed.
func TestUnlistedFileIsRefused(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	rewriteTarball(t, archivePath, map[string][]byte{
		"bin/securityctl":    []byte("binary"),
		"sbom.spdx.json":     []byte(`{"spdxVersion":"SPDX-2.3"}`),
		"bin/extra-backdoor": []byte("surprise"),
	})

	_, err := release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("an archive with an unlisted file verified")
	}
	if !strings.Contains(err.Error(), "extra-backdoor") {
		t.Errorf("the failure should name the unlisted file, got %q", err)
	}
}

// A listed file that is absent fails at 3am otherwise.
func TestMissingFileIsRefused(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	rewriteTarball(t, archivePath, map[string][]byte{
		"sbom.spdx.json": []byte(`{"spdxVersion":"SPDX-2.3"}`),
	})

	_, err := release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("an archive missing a listed file verified")
	}
	if !strings.Contains(err.Error(), "does not contain") {
		t.Errorf("the failure should say the file is absent, got %q", err)
	}
}

// A tar entry of "../../../etc/cron.d/evil" is code execution on any machine that
// extracts it. The name is checked before the entry is read.
func TestPathTraversalEntryIsRefused(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "evil.tar.gz")

	f, _ := os.Create(archivePath)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("* * * * * root sh -c 'beacon'\n")
	_ = tw.WriteHeader(&tar.Header{
		Name: "../../../etc/cron.d/evil", Mode: 0o644,
		Size: int64(len(body)), Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	manifest := release.Manifest{
		SchemaVersion: 1,
		Product:       "security-platform",
		Version:       "1.0.0", Platform: "linux-amd64",
		Files: []release.FileEntry{{Path: "bin/x", SHA256: strings.Repeat("a", 64)}},
		SBOM:  release.SBOM{Path: "sbom.spdx.json", SHA256: strings.Repeat("b", 64)},
	}
	_, pub := keyPair(t)
	_ = pub
	manifest.Signature = release.Signature{
		Algorithm: "ed25519", KeyID: "k", PublicKey: "", Value: "",
	}

	_, err := release.Verify(archivePath, manifest, nil)
	if err == nil {
		t.Fatal("an archive with a traversal entry verified")
	}
	if !strings.Contains(err.Error(), "traverses") && !strings.Contains(err.Error(), "absolute") {
		t.Errorf("the failure should name the traversal, got %q", err)
	}
}

func TestValidateFilePathRejectsEveryEscape(t *testing.T) {
	bad := []string{
		"", "/etc/passwd", "../etc/passwd", "a/../../b",
		`C:\Windows\x`, `a\..\b`, "~/x", "a/./b", "../../",
		strings.Repeat("a", 600),
		"has space/x",
	}
	for _, p := range bad {
		if err := release.ValidateFilePath(p); err == nil {
			t.Errorf("path %q was accepted", p)
		}
	}
	for _, p := range []string{"bin/securityctl", "rules/builtin/bundle.yaml", "a/b/c-1.0+build"} {
		if err := release.ValidateFilePath(p); err != nil {
			t.Errorf("path %q was refused: %v", p, err)
		}
	}
}

// A release archive is installed at every site. A secret in one is a secret
// everywhere.
func TestSecretMaterialIsRefused(t *testing.T) {
	dir := t.TempDir()
	priv, _ := keyPair(t)

	cases := map[string]string{
		"private key":  "-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n-----END RSA PRIVATE KEY-----\n",
		"aws key":      "AKIAIOSFODNN7EXAMPLE\n",
		"github token": "ghp_0123456789abcdefghijklmnopqrstuvwxyz\n",
		"dsn":          "postgres://admin:hunter2@db.internal:5432/security\n",
		"password":     "password: \"hunter2hunter2\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			// The archive path is derived from the case name without spaces,
			// because a path with a space is refused for a different and earlier
			// reason and this test is about the secret scan.
			archivePath := "config/" + strings.ReplaceAll(sanitize(name), " ", "_")
			src := writeTemp(t, dir, sanitize(name), []byte(body))
			_, _, err := release.Build(dir, release.BuildInput{
				Version: "1.0.0", Platform: "linux-amd64",
				Files:   []release.SourceFile{{ArchivePath: archivePath, SourcePath: src}},
				SBOM:    release.SBOM{Path: "sbom.spdx.json"},
				Builder: "test",
			}, priv, "key-1")
			if err == nil {
				t.Fatalf("an archive containing %s was built", name)
			}
			if !strings.Contains(err.Error(), "credential-shaped") {
				t.Errorf("the failure should name the problem, got %q", err)
			}
		})
	}
}

// A binary cannot contain a passphrase in a form the scan would catch, and a
// regex over forty megabytes is a denial of service in the verifier.
func TestBinaryFilesAreNotScanned(t *testing.T) {
	binary := []byte{0x7f, 0x45, 0x4c, 0x46, 0x00, 0x00, 0x00, 0x00}
	if err := release.ScanForSecrets("bin/tool", binary); err != nil {
		t.Errorf("a binary was scanned for secrets: %v", err)
	}
}

// A contract step shipped alongside its expand is the mistake the
// expand/contract discipline exists to prevent, and it is easy to make by
// accident when packaging.
func TestContractStepAlongsideExpandIsRefused(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{
		"bin/securityctl": []byte("binary"),
	})
	manifest.Migrations = append(manifest.Migrations,
		release.MigrationEntry{Version: 2, Name: "narrow", Phase: "contract"})
	// Both problems are reported together: the mixed phases and the broken
	// signature the edit caused. A test asserting on the phase rule alone would
	// pass for the wrong reason, so the message is checked for the phase.
	_, err := release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("a mixed expand/contract archive verified")
	}
	if !strings.Contains(err.Error(), "contract") {
		t.Errorf("the failure should name the mixed phases, got %q", err)
	}
}

// A manifest with no files is not a release.
func TestEmptyManifestIsRefused(t *testing.T) {
	archivePath, _, pub := buildArchive(t, nil)
	manifest := release.Manifest{
		SchemaVersion: 1, Product: "security-platform", Version: "1.0.0",
		Platform: "linux-amd64",
		SBOM:     release.SBOM{Path: "sbom.spdx.json", SHA256: strings.Repeat("b", 64)},
		Signature: release.Signature{Algorithm: "ed25519", KeyID: "k",
			PublicKey: hexOf(pub), Value: strings.Repeat("a", 128)},
	}
	if err := release.Sign(&manifest, mustKey(t), "k", pub); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	_, err := release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("a manifest listing no files verified")
	}
	if !strings.Contains(err.Error(), "no files") {
		t.Errorf("the failure should say the manifest is empty, got %q", err)
	}
}

func mustKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	priv, _ := keyPair(t)
	return priv
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}

// rewriteTarball replaces an archive's contents, for the tamper tests.
func rewriteTarball(t *testing.T, archivePath string, files map[string][]byte) {
	t.Helper()
	tmp := archivePath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)),
			Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(),
		}); err != nil {
			t.Fatalf("header: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	if err := os.Rename(tmp, archivePath); err != nil {
		t.Fatalf("rename: %v", err)
	}
}
