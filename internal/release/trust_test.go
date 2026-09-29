package release_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

// The offline verifiers exist to answer "did the party I trust build this". With
// no trusted key the only answer available is "did whoever wrote the archive
// also sign it" — and anyone who can write the archive can sign it. The two
// commands that use this function printed a warning and carried on, which made
// the warning the only thing between an operator and accepting a self-signed
// forgery on the exact commands whose purpose is to detect one.
func TestVerificationRefusesWithoutATrustedKey(t *testing.T) {
	archivePath, manifest, _ := buildArchive(t, map[string][]byte{"core.exe": []byte("binary")})

	if _, err := release.Verify(archivePath, manifest, nil); err == nil {
		t.Fatal("an archive verified with no trusted key. Every self-consistent forgery now " +
			"passes the command an operator runs to check for exactly that")
	} else if !strings.Contains(err.Error(), "trusted") {
		t.Errorf("the refusal should say what is missing, got %q", err)
	}
}

// The seal-shaped half of the same control lives in internal/checkpoint, which
// asserts it against VerifySeal directly. It is not re-asserted here: a second
// copy of the same check in a different package is a second thing to keep in
// step, and the plan's control is one control.

// The signature covered everything in the manifest except the SBOM block.
//
// The reasoning was that the SBOM is a pointer to another file rather than
// manifest content, so the pointer did not need signing. That is the gap: the
// pointer is metadata, and metadata is what somebody with write access to the
// manifest repoints. Restating `sbom: {path, sha256}` under an otherwise valid
// signature is a one-line edit, and Verify never noticed - it only checked that
// the declared path was syntactically safe. A signature that does not cover a
// field in the document it signs is a signature over a smaller document than the
// reader believes.
func TestRepointingTheSBOMBreaksTheSignature(t *testing.T) {
	archivePath, manifest, pub := buildArchive(t, map[string][]byte{"core.exe": []byte("binary")})
	if _, err := release.Verify(archivePath, manifest, pub); err != nil {
		t.Fatalf("the archive does not verify as built: %v", err)
	}

	// The payload itself is the control. A signature is a statement about bytes;
	// if changing the SBOM block does not change the bytes, the signature says
	// nothing about the SBOM block, and no amount of checking downstream repairs
	// that - every check downstream reads a field the signature never covered.
	before := release.SigningPayload(manifest)
	after := release.SigningPayload(func() release.Manifest {
		m := manifest
		m.SBOM.Path = "attacker.spdx.json"
		m.SBOM.SHA256 = sha256Hex([]byte(`{"name":"whatever you like"}`))
		return m
	}())
	if before == after {
		t.Fatal("rewriting the SBOM block left the signed bytes unchanged. The manifest's " +
			"signature has never covered the SBOM, and a pointer with no signature over it " +
			"is metadata somebody with write access can repoint")
	}

	tampered := manifest
	tampered.SBOM.Path = "attacker.spdx.json"
	tampered.SBOM.SHA256 = sha256Hex([]byte(`{"name":"whatever you like"}`))
	if _, err := release.Verify(archivePath, tampered, pub); err == nil {
		t.Fatal("a manifest whose SBOM block was rewritten verified against the original " +
			"signature")
	}
}

// And the other half: even with a signature that does cover the block, the
// declared hash has to be compared to the bytes in the archive. A signed
// pointer to a file whose contents nobody checked tells the operator nothing.
func TestADeclaredSBOMHashMustMatchTheArchive(t *testing.T) {
	dir := t.TempDir()
	priv, pub := keyPair(t)

	// genuine is what goes into the archive; declared is the hash the signed
	// manifest claims for it. They have to differ for this test to mean anything.
	genuine := []byte(`{"spdxVersion":"SPDX-2.3","name":"the real one"}`)
	declared := []byte(`{"spdxVersion":"SPDX-2.3","name":"a different one"}`)

	inputs := []release.SourceFile{
		{ArchivePath: "core.exe", SourcePath: writeTemp(t, dir, "core", []byte("binary"))},
		{ArchivePath: "sbom.spdx.json", SourcePath: writeTemp(t, dir, "sbom", genuine)},
	}
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
		SBOM:    release.SBOM{Path: "sbom.spdx.json", Format: "spdx-json", SHA256: sha256Hex(declared)},
		Builder: "test",
	}, priv, "key-1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Signed with the wrong hash, deliberately: the hash is inside the signature
	// now, so this manifest is authentic and its claim is false.
	if err := release.Sign(&manifest, priv, "key-1", pub); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	_, err = release.Verify(archivePath, manifest, pub)
	if err == nil {
		t.Fatal("a correctly signed manifest declaring the wrong SBOM hash verified. The hash " +
			"was present and never compared to the bytes")
	}
	if !strings.Contains(err.Error(), "SBOM") {
		t.Errorf("the failure should name the SBOM, got %q", err)
	}
}

// A tar entry that is not a regular file is an instruction, not content, and
// whoever writes the archive chooses what it points at. The reader used to skip
// only directories and treat everything else as a file, so a symlink entry was
// indexed by its target path and hashed by its own bytes - and an installer that
// unpacked it would write outside the destination.
func TestNonRegularTarEntriesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, path string)
		want  string
	}{
		{
			name: "symlink",
			build: func(t *testing.T, path string) {
				writeTar(t, path, []tar.Header{
					{Name: "sbom.spdx.json", Typeflag: tar.TypeReg, Size: int64(len(sbomBody)), Mode: 0o600},
					{Name: "core.exe", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
				}, sbomBody)
			},
			want: "symlink",
		},
		{
			name: "hard link",
			build: func(t *testing.T, path string) {
				writeTar(t, path, []tar.Header{
					{Name: "core.exe", Typeflag: tar.TypeLink, Linkname: "C:/Windows/System32/cmd.exe", Mode: 0o600},
				}, nil)
			},
			want: "hard link",
		},
		{
			name: "device",
			build: func(t *testing.T, path string) {
				writeTar(t, path, []tar.Header{
					{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o600},
				}, nil)
			},
			want: "character device",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "release.tar.gz")
			tc.build(t, archivePath)

			priv, pub := keyPair(t)
			manifest := release.Manifest{
				SchemaVersion: release.ManifestVersion,
				Product:       "ai-captain-security",
				Version:       "1.0.0",
				Files:         []release.FileEntry{{Path: "core.exe", SHA256: sha256Hex([]byte("x")), SizeBytes: 1}},
				SBOM:          release.SBOM{Path: "sbom.spdx.json", Format: "spdx-json", SHA256: sha256Hex(sbomBody)},
				Signature:     release.Signature{Algorithm: "ed25519", KeyID: "key-1", PublicKey: hexOf(pub)},
			}
			if err := release.Sign(&manifest, priv, "key-1", pub); err != nil {
				t.Fatalf("Sign: %v", err)
			}

			_, err := release.Verify(archivePath, manifest, pub)
			if err == nil {
				t.Fatalf("an archive containing a %s verified", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the failure should name the %s, got %q", tc.want, err)
			}
		})
	}
}

var sbomBody = []byte(`{"spdxVersion":"SPDX-2.3"}`)

func writeTar(t *testing.T, path string, headers []tar.Header, body []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for i := range headers {
		if err := tw.WriteHeader(&headers[i]); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if headers[i].Typeflag == tar.TypeReg && headers[i].Size > 0 {
			if _, err := tw.Write(body[:headers[i].Size]); err != nil {
				t.Fatalf("write body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
}
