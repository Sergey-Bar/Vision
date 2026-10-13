package main

// Tests for `fetch-runtime`'s part that has real logic: choosing one library out of
// a release archive, and refusing rather than guessing.
//
// The download itself is not tested — it is `net/http` against a URL the operator
// supplies — but everything between "the bytes arrived" and "the bytes are on disk
// at a path a run will load" is, and that is where a wrong decision becomes a crash
// inside the detector a week later.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeArchive builds a ZIP with the given members, so the extraction path is
// exercised against archive layouts that have actually shipped.
func writeArchive(t *testing.T, path string, members map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for name, body := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeTarGz builds the OTHER format a release ships in, with a directory entry
// first the way a real release tar has one.
//
// Linux and macOS assets are `.tgz` and Windows assets are ZIPs, so a fetch that
// only understood ZIP worked on exactly one of the three platforms its own docs
// claimed — and it failed with a message about a format, which is the same shape
// of defect as refusing by platform name. These tests exist so that shape cannot
// come back quietly.
func writeTarGz(t *testing.T, path string, members map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "onnxruntime/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for name, body := range members {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestTheSelectionRuleIsTheSameOnAllThreePlatforms exercises the SUFFIX parameter
// directly rather than going through a real archive.
//
// The rule is identical on Linux, macOS and Windows and only the extension differs,
// so testing it on one machine must still be able to exercise all three. That is
// what [libraryCandidates] taking the suffix as an argument buys, and it is why
// this test is not the same test three times with three archives.
func TestTheSelectionRuleIsTheSameOnAllThreePlatforms(t *testing.T) {
	for _, tc := range []struct {
		platform string
		suffix   string
		libs     []string
	}{
		{"linux", ".so", []string{
			"onnxruntime/lib/libonnxruntime.so",
			"libonnxruntime.so",
			"third_party/onnxruntime/libonnxruntime.so",
		}},
		{"darwin", ".dylib", []string{
			"onnxruntime/lib/libonnxruntime.dylib",
			"libonnxruntime.dylib",
		}},
		{"windows", ".dll", []string{
			"onnxruntime/lib/onnxruntime.dll",
			"onnxruntime.dll",
		}},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			members := append([]string{}, tc.libs...)
			// The things that must never be chosen, whatever the platform.
			members = append(members,
				"README.md",
				"onnxruntime/include/onnxruntime_cxx_api.h",
				"onnxruntime/lib/libonnxruntime_providers_shared"+tc.suffix,
				"onnxruntime/lib/libonnxruntime_training"+tc.suffix,
				"onnxruntime/lib/libonnxruntime"+tc.suffix+".1.23.0", // SONAME
				"onnxruntime/lib/libonnxruntime"+tc.suffix+".symbols",
				"onnxruntime/lib/libonnxruntime_c"+tc.suffix, // a colliding prefix, not the library
				"onnxruntime/capi/onnxruntime_c_api.h",
			)
			got := libraryCandidates(members, tc.suffix)
			if len(got) != len(tc.libs) {
				t.Fatalf("selected %v, want exactly %v", got, tc.libs)
			}
			for i := range tc.libs {
				if got[i] != tc.libs[i] {
					t.Errorf("selected %v, want %v", got, tc.libs)
					break
				}
			}
		})
	}
}

// TestTheWrongPlatformExtensionIsNotAMatch is the cross-platform mistake worth
// naming: a Windows archive offered to a Linux host contains `.dll` files, and a
// suffix-only check that accepted any shared-library extension would hand the
// detector a Windows binary on Linux and fail at load with a message about a
// missing symbol.
func TestTheWrongPlatformExtensionIsNotAMatch(t *testing.T) {
	members := []string{
		"onnxruntime/lib/onnxruntime.dll",
		"onnxruntime/lib/onnxruntime.so",
		"onnxruntime/lib/onnxruntime.dylib",
	}
	for _, tc := range []struct{ platform, suffix, want string }{
		{"windows", ".dll", "onnxruntime.dll"},
		{"linux", ".so", "onnxruntime.so"},
		{"darwin", ".dylib", "onnxruntime.dylib"},
	} {
		got := libraryCandidates(members, tc.suffix)
		if len(got) != 1 {
			t.Fatalf("%s: selected %v, want exactly one", tc.platform, got)
		}
		if filepath.Base(got[0]) != tc.want {
			t.Errorf("%s: selected %s, want %s", tc.platform, got[0], tc.want)
		}
	}
}

// TestTwoCandidatesIsARefusalNotACoin is the decision this command exists to
// delegate to a human.
//
// It would be easy to take the first candidate. Two ONNX Runtime builds on one
// machine are not ABI-compatible with each other's Go bindings, so a coin flip here
// becomes a crash inside the detector later — and a crash naming neither the right
// file nor the wrong one.
//
// The realistic two-candidate case is an archive holding the same library at two
// paths, which is what a re-cut release archive looks like: the new `lib/` layout
// beside the old `bin/` one that was not cleaned up.
func TestTwoCandidatesIsARefusalNotACoin(t *testing.T) {
	for _, tc := range []struct{ platform, suffix string }{
		{"linux", ".so"}, {"windows", ".dll"}, {"darwin", ".dylib"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			// The suffix is a parameter, so the test does not depend on the host.
			members := []string{
				"onnxruntime/lib/libonnxruntime" + tc.suffix,
				"onnxruntime/bin/libonnxruntime" + tc.suffix,
				"onnxruntime/include/onnxruntime_c_api.h",
			}
			got := libraryCandidates(members, tc.suffix)
			if len(got) != 2 {
				t.Fatalf("selected %v, want both candidates so the caller has to refuse", got)
			}
		})
	}
}

// TestExtractRefusesAnArchiveHoldingTwoCandidates drives the real function on the
// host's platform, so the refusal path is exercised end to end rather than only its
// selector.
func TestExtractRefusesAnArchiveHoldingTwoCandidates(t *testing.T) {
	dir := t.TempDir()
	suffix := sharedLibrarySuffix()
	archive := filepath.Join(dir, "two.zip")
	writeArchive(t, archive, map[string]string{
		"onnxruntime/lib/libonnxruntime" + suffix: "the library, in the current layout",
		"onnxruntime/bin/libonnxruntime" + suffix: "the same library, in the old layout",
	})
	_, err := extractLibrary(archive, t.TempDir())
	if err == nil {
		t.Fatal("an archive holding two candidate libraries was resolved by guessing")
	}
	if !strings.Contains(err.Error(), "2 candidate libraries") {
		t.Errorf("the refusal does not say how many: %v", err)
	}
	for _, want := range []string{"/lib/libonnxruntime" + suffix, "/bin/libonnxruntime" + suffix} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not list %q: %v", want, err)
		}
	}
}

// TestExtractRefusesAnArchiveWithNoLibrary drives the other refusal, because
// "the download arrived and holds nothing loadable" is the commonest real failure:
// the wrong asset for the platform.
func TestExtractRefusesAnArchiveWithNoLibrary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "wrong.zip")
	writeArchive(t, archive, map[string]string{
		"onnxruntime/include/onnxruntime_cxx_api.h":                           "headers only",
		"onnxruntime/lib/libonnxruntime" + sharedLibrarySuffix() + ".symbols": "a symbol file",
		"README.md": "documentation",
	})
	_, err := extractLibrary(archive, t.TempDir())
	if err == nil {
		t.Fatal("an archive holding no library was accepted")
	}
	if !strings.Contains(err.Error(), sharedLibrarySuffix()) {
		t.Errorf("the refusal does not name the suffix it looked for: %v", err)
	}
	if !strings.Contains(err.Error(), "wrong asset") {
		t.Errorf("the refusal does not suggest the likeliest cause: %v", err)
	}
}

// TestANonArchiveIsRefusedWithTheOtherWayOut named, because the fix for "the URL
// pointed at the DLL rather than the ZIP" is completely different from the fix for
// "the download failed".
func TestANonArchiveIsRefusedWithTheOtherWayOut(t *testing.T) {
	dir := t.TempDir()
	notZip := filepath.Join(dir, "download.zip")
	if err := os.WriteFile(notZip, []byte("MZ\x90\x00 this is a DLL, not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := extractLibrary(notZip, t.TempDir())
	if err == nil {
		t.Fatal("a DLL was accepted as a release archive")
	}
	if !strings.Contains(err.Error(), "pointed straight at the library") {
		t.Errorf("the refusal does not name the other way out: %v", err)
	}
}

// TestExtractFindsTheOneLibrary drives the success path on the host's platform.
func TestExtractFindsTheOneLibrary(t *testing.T) {
	dir := t.TempDir()
	suffix := sharedLibrarySuffix()
	archive := filepath.Join(dir, "good.zip")
	writeArchive(t, archive, map[string]string{
		"onnxruntime/lib/libonnxruntime" + suffix:                  "not really a library",
		"onnxruntime/lib/libonnxruntime_providers_shared" + suffix: "a provider",
		"onnxruntime/lib/libonnxruntime_training" + suffix:         "the training build",
		"onnxruntime/lib/libonnxruntime" + suffix + ".1.23.0":      "a versioned SONAME",
		"onnxruntime/include/onnxruntime_cxx_api.h":                "a header",
		"README.md": "documentation",
	})
	got, err := extractLibrary(archive, t.TempDir())
	if err != nil {
		t.Fatalf("the library was not found: %v", err)
	}
	if filepath.Base(got) != "libonnxruntime"+suffix {
		t.Fatalf("extracted %q; providers, the training build and the SONAME must all be excluded", got)
	}
	// And the bytes came out intact — a selection that picks the right NAME and the
	// wrong member content is still a wrong library.
	raw, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "not really a library" {
		t.Fatalf("extracted %q, want the library member's own bytes", raw)
	}
}

// TestTheManifestReplacesItsOwnRowRatherThanAccumulating is the difference between
// a manifest that answers "what is installed" and one that answers "how many times
// has somebody re-fetched".
func TestTheManifestReplacesItsOwnRowRatherThanAccumulating(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "runtime.json")

	first := runtimeRecord{Kind: "onnxruntime", Path: "models/a.dll", SHA256: "aaa",
		Bytes: 1, Licence: "MIT", Verified: true, Platform: "windows/amd64"}
	if err := writeRuntimeRecord(manifest, first); err != nil {
		t.Fatal(err)
	}
	second := runtimeRecord{Kind: "onnxruntime", Path: "models/b.dll", SHA256: "bbb",
		Bytes: 2, Licence: "Apache-2.0", Verified: false, Platform: "windows/amd64"}
	if err := writeRuntimeRecord(manifest, second); err != nil {
		t.Fatal(err)
	}
	recs, err := readRuntimeRecords(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("manifest holds %d rows after two fetches of the same kind and platform, want 1: %+v",
			len(recs), recs)
	}
	if recs[0].SHA256 != "bbb" || recs[0].Licence != "Apache-2.0" {
		t.Fatalf("the row was not replaced: %+v", recs[0])
	}
	// The verified flag survives as a FACT about the newest fetch rather than being
	// carried over from the previous one — a re-fetch with no --sha256 is an
	// unverified fetch even if the previous fetch was verified.
	if recs[0].Verified {
		t.Fatal("a re-fetch with no expected digest was recorded as VERIFIED; the flag describes " +
			"this fetch, not the last one")
	}

	// A different platform is a DIFFERENT row, not an overwrite.
	other := runtimeRecord{Kind: "onnxruntime", Path: "models/libonnxruntime.so", SHA256: "ccc",
		Bytes: 3, Licence: "MIT", Verified: true, Platform: "linux/amd64"}
	if err := writeRuntimeRecord(manifest, other); err != nil {
		t.Fatal(err)
	}
	recs, err = readRuntimeRecords(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("manifest holds %d rows, want 2 — one per platform", len(recs))
	}
}

// TestAMissingManifestIsEmptyRatherThanAnError, because `--verify` on a machine
// that has not fetched anything should say "nothing recorded", not crash.
func TestAMissingManifestIsEmptyRatherThanAnError(t *testing.T) {
	recs, err := readRuntimeRecords(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing manifest returned an error rather than an empty list: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("a missing manifest returned %d rows", len(recs))
	}
	if err := verifyRuntime(filepath.Join(t.TempDir(), "absent.json"), ""); err == nil {
		t.Fatal("verifying against a manifest that records nothing returned no error")
	} else if !strings.Contains(err.Error(), "nothing to") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

// TestAVerifiedMismatchIsAnErrorAndNotAWarning is the whole point of `--verify`.
//
// Two ONNX Runtime versions on one machine are not ABI-compatible with each other's
// Go bindings, so a silent mismatch here becomes a crash inside the detector a week
// later — with an error that names neither the right file nor the wrong one.
func TestAVerifiedMismatchIsAnErrorAndNotAWarning(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "runtime.json")

	actual := filepath.Join(dir, "onnxruntime.dll")
	if err := os.WriteFile(actual, []byte("the version that arrived"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, _, err := hashFile(actual)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeRuntimeRecord(manifest, runtimeRecord{
		Kind: "onnxruntime", Path: actual, SHA256: strings.Repeat("0", 64),
		Bytes: 21, Licence: "MIT", Verified: true, Platform: "windows/amd64",
	}); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntime(manifest, actual); err == nil {
		t.Fatal("a digest mismatch was reported as a success")
	}

	if err := writeRuntimeRecord(manifest, runtimeRecord{
		Kind: "onnxruntime", Path: actual, SHA256: sum, Bytes: 21,
		Licence: "MIT", Verified: true, Platform: "windows/amd64",
	}); err != nil {
		t.Fatal(err)
	}
	if err := verifyRuntime(manifest, actual); err != nil {
		t.Fatalf("the recorded digest did not verify against the file it was recorded from: %v", err)
	}
}

// TestTheLinuxAndMacOSAssetFormatIsRead covers the other archive format, on
// every host platform, with the host's own suffix.
//
// The release asset for Linux and macOS is a `.tgz`; the Windows one is a ZIP.
// `fetch-runtime` was written against `archive/zip` only, so it refused every
// Linux and macOS asset with a message about an archive format — a fetch that
// worked on one of the three platforms its own `--url` error message names. The
// selection rule is suffix-parameterised (the same rule on all three, only the
// extension differs), so the format under test here is the tar path around it.
func TestTheLinuxAndMacOSAssetFormatIsRead(t *testing.T) {
	dir := t.TempDir()
	suffix := sharedLibrarySuffix()
	archive := filepath.Join(dir, "onnxruntime-linux-x64-1.29.0.tgz")
	writeTarGz(t, archive, map[string]string{
		"onnxruntime/lib/libonnxruntime" + suffix:                  "the library, in the tarball layout",
		"onnxruntime/lib/libonnxruntime_providers_shared" + suffix: "a provider",
		"onnxruntime/lib/libonnxruntime_training" + suffix:         "the training build",
		"onnxruntime/lib/libonnxruntime" + suffix + ".1.23.0":      "a versioned SONAME",
		"onnxruntime/include/onnxruntime_cxx_api.h":                "a header",
		"README.md": "documentation",
	})
	got, err := extractLibrary(archive, dir)
	if err != nil {
		t.Fatalf("the tarball's library was not found: %v", err)
	}
	if filepath.Base(got) != "libonnxruntime"+suffix && filepath.Base(got) != "onnxruntime"+suffix {
		t.Fatalf("extracted %q; the provider, the training build and the SONAME must all be excluded", got)
	}
	raw, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("the extracted file is not readable: %v", err)
	}
	if string(raw) != "the library, in the tarball layout" {
		t.Fatalf("extracted %q, want the library member's own bytes. A selection that picks the right "+
			"NAME and the wrong member content is still a wrong library", raw)
	}
}

// TestTheProvidersComeOutOfATarGzToo covers the companion pass over the same
// archive.
//
// The provider DLLs are what make a GPU package a GPU package and they sit
// beside the library, so the tar path has to find them there — a fetch that
// extracted the main library and silently left the providers behind installs a
// CUDA build that runs on the CPU and says otherwise.
func TestTheProvidersComeOutOfATarGzToo(t *testing.T) {
	dir := t.TempDir()
	suffix := sharedLibrarySuffix()
	archive := filepath.Join(dir, "onnxruntime-linux-x64-gpu_cuda12-1.29.0.tgz")
	writeTarGz(t, archive, map[string]string{
		"onnxruntime/lib/libonnxruntime" + suffix:                             "the library",
		"onnxruntime/lib/libonnxruntime_providers_shared" + suffix:            "the shared provider",
		"onnxruntime/lib/libonnxruntime_providers_cuda" + suffix:              "the cuda provider",
		"training/onnxruntime/lib/libonnxruntime_providers_training" + suffix: "a copy from the training tree, which is deeper than beside",
	})
	manifest := filepath.Join(dir, "runtime.json")
	destDir := filepath.Join(dir, "providers")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recs, err := fetchProviderDLLs(archive, destDir,
		map[string]string{"provider": "keep", "licence": "MIT", "manifest": manifest},
		"https://example.invalid/release.tgz", true, "archive")
	if err != nil {
		t.Fatalf("the provider pass refused a tarball: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("extracted %d provider libraries, want exactly the two that sit BESIDE the library:\n%+v",
			len(recs), recs)
	}
	stored, err := readRuntimeRecords(manifest)
	if err != nil {
		t.Fatalf("the manifest could not be read back: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("the manifest holds %d rows, want one per provider", len(stored))
	}
	// The verified flag travels WITH the download: a digest confirmed for the
	// library says nothing about a file from the same archive independently, and
	// it is recorded as the same download rather than as an independent check.
	for _, r := range stored {
		if !r.Verified || r.VerifiedBy != "archive" {
			t.Errorf("provider %s recorded as verified=%v by %q; the companion's verification is the "+
				"download's own, and saying anything stronger would be a claim nobody checked",
				r.Path, r.Verified, r.VerifiedBy)
		}
	}
}

// TestADirectoryEntryIsNotAMember is the tar-only hazard.
//
// A release tar holds directory entries as well as files, and a directory indexed
// as if it were a library is a "member" whose bytes are nothing. Skipping
// non-regular entries is the whole of it.
func TestADirectoryEntryIsNotAMember(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "with-dirs.tgz")
	writeTarGz(t, archive, map[string]string{
		"onnxruntime/lib/libonnxruntime" + sharedLibrarySuffix(): "the library",
	})
	index, closeArchive, err := openRelease(archive)
	if err != nil {
		t.Fatalf("the tarball could not be indexed: %v", err)
	}
	defer closeArchive()
	for name := range index {
		if strings.HasSuffix(name, "/") {
			t.Errorf("%s is a directory and was indexed as a member", name)
		}
	}
	if _, ok := index["onnxruntime/lib/libonnxruntime"+sharedLibrarySuffix()]; !ok {
		t.Errorf("the library member is missing from the index: %v", index)
	}
}

// TestAFormatTheBuildCannotReadNamesTheOtherWayOut keeps the original refusal's
// job: the fix for "the URL pointed at the library" is different from the fix for
// "the download failed", and both were named before there were two formats.
func TestAFormatTheBuildCannotReadNamesTheOtherWayOut(t *testing.T) {
	dir := t.TempDir()
	// A ZIP named as a tarball: the extension says tar, the bytes say zip.
	zipAsTar := filepath.Join(dir, "release.tgz")
	writeArchive(t, zipAsTar, map[string]string{"onnxruntime/lib/onnxruntime.dll": "a library"})
	_, err := extractLibrary(zipAsTar, t.TempDir())
	if err == nil {
		t.Fatal("a ZIP named as a tarball was read as a tarball")
	}
	if !strings.Contains(err.Error(), "pointed straight at") {
		t.Errorf("the refusal does not name the other way out: %v", err)
	}
}
