package main

// `fetch-runtime` — obtain the ONNX Runtime shared library and RECORD what it is.
//
// Why this is a subcommand and not a line in a README: the library is named by
// SP_POC_ORT_LIB rather than discovered from PATH, because two ONNX Runtime versions
// can be installed on one Windows machine, they are not ABI-compatible with each
// other's Go bindings, and the SHA-256 recorded beside a model's weights is the
// SHA-256 of one specific build. A library found by searching PATH is a library
// nobody chose.
//
// So this command does three things and refuses to do a fourth:
//
//  1. downloads the release asset named on the command line;
//  2. computes its SHA-256 and prints it;
//  3. records the path, the digest and the licence in the manifest.
//
// It does NOT ship a hardcoded digest. A digest baked into this source is a digest
// somebody verified once, on a build that may not be the one being downloaded, and
// this repository's rule is that an unverified artefact is an unaccounted one. The
// digest is PRINTED and, when the caller supplies one with --sha256, CHECKED. A
// first run with no expected digest therefore records what arrived and says plainly
// that nobody has verified it yet, and a second run with --sha256 fails loudly if
// the bytes differ. That is the honest two-step: record, then verify.
//
// The licence is MIT and that is not a claim this file gets to make on its own — it
// is `--licence`, defaulting to the string the caller passes, and the command PRINTS
// what it is about to record so a wrong default is visible in the same breath.

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/soak"
)

// runtimeManifest is what fetch-runtime writes and what a later run verifies
// against.
//
// It is a separate file from `testdata/labels.json` on purpose. That file records
// the provenance of MATERIAL — clips, images, ground truth — and it is the file a
// reader goes to when they ask what this repository is allowed to redistribute. The
// runtime library is a build dependency, not material, and folding the two together
// would mean either the material record carries a DLL path or the material
// licence check has to know about Go modules.
const runtimeManifest = "models/runtime.json"

// runtimeRecord is one verified artefact.
type runtimeRecord struct {
	// Kind is `onnxruntime`. It is here so a manifest can also hold a CUDA or DirectML
	// provider later without a schema change, and so a reader can tell what it is
	// looking at without inferring it from the file extension.
	Kind string `json:"kind"`
	// Path is where it landed, relative to the repository root where possible.
	Path string `json:"path"`
	// SHA256 is computed from the bytes on disk. There is no field in a shared
	// library that reports its own digest and no runtime API that returns one, so
	// this is the only honest value and it is computed rather than declared.
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	// Licence is what the caller passed. Empty is RECORDED as empty and reported as
	// such, rather than defaulted to something plausible: a library whose terms
	// nobody wrote down is a library nobody has checked.
	Licence string `json:"licence"`
	// Source is the URL it came from, verbatim, including its version, because a
	// digest beside a URL that has drifted says nothing.
	Source string `json:"source"`
	// Verified says whether the digest was CHECKED against one the caller expected.
	// False means recorded and never confirmed, which is a different fact from
	// confirmed, and a report that cannot tell them apart is a report nobody can
	// rely on.
	Verified bool `json:"verified"`
	// VerifiedBy says WHICH digest was confirmed, and it is a field of its own
	// rather than a phrase folded into Verified for a specific reason: the two checks
	// are not interchangeable, and a boolean cannot tell them apart.
	//
	// `library` means the caller supplied this file's own digest. `archive` means the
	// whole release asset was confirmed against a digest a release page publishes and
	// the file was extracted from it — which is the STRONGER claim, because a publisher
	// prints the archive's digest and prints this file's nowhere, so pinning the member
	// to a number this module computed and a person pasted back is a check against
	// ourselves. Empty means nothing was confirmed.
	//
	// A record that says `verified: true` and cannot say which of the two it means is
	// a record whose `true` has two very different values behind it.
	VerifiedBy string `json:"verified_by,omitempty"`
	// FetchedAt is when it was obtained. Not a claim about the library's build date,
	// which is inside the artefact and not on its face.
	FetchedAt string `json:"fetched_at"`
	// Platform is GOOS/GOARCH, because the same release name resolves to a different
	// file on each platform and a manifest that does not say which one it holds is a
	// manifest that will be read on the wrong machine.
	Platform string `json:"platform"`
}

func cmdFetchRuntime(args []string) error {
	flags, err := flagSet("fetch-runtime", map[string]string{
		"url":            "",
		"out":            defaultRuntimePath(),
		"sha256":         "",
		"archive-sha256": "",
		"licence":        "MIT",
		"manifest":       runtimeManifest,
		"verify":         "0",
		"provider":       "keep",
	}, args)
	if err != nil {
		return err
	}
	url := firstNonEmpty(flags["url"], os.Getenv("SP_POC_ORT_URL"))

	// `--verify` reads the manifest and re-checks what it already recorded. It needs
	// no network and no flags, and it is the command somebody runs when they want
	// to know whether the library on disk is the one they recorded.
	if flags["verify"] != "0" {
		return verifyRuntime(flags["manifest"], flags["out"])
	}

	if url == "" {
		return fmt.Errorf("fetch-runtime: no URL. Pass --url or set SP_POC_ORT_URL.\n"+
			"  The library is not vendored and this file does not carry a URL: a hardcoded download "+
			"source in a source file is a source nobody checked, and it silently rots when the "+
			"release naming changes.\n"+
			"  Take the URL from https://github.com/microsoft/onnxruntime/releases and pick the asset "+
			"matching your platform (%s). The library is inside the release ZIP under lib/ — it is not "+
			"the ZIP itself.", runtime.GOOS+"/"+runtime.GOARCH)
	}

	out := flags["out"]
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}

	// The release asset is a ZIP and the library is inside it. Extracting one member
	// of an archive is done with archive/zip rather than by shelling out to a
	// third-party unarchiver, because the alternative is running whatever `unzip` or
	// `tar` happens to be on PATH over a file whose contents this run is about to
	// load into its own address space.
	work, err := os.MkdirTemp("", "sp-ort-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()

	zipPath := filepath.Join(work, "download.zip")
	fmt.Printf("fetch-runtime: downloading\n  %s\n", url)
	if err := download(url, zipPath); err != nil {
		return err
	}

	// The ARCHIVE's digest, checked against the one the publisher published, before a
	// single byte is extracted.
	//
	// This is a different check from `--sha256` and it is not redundant with it. A
	// release page publishes one digest, and it is the digest of the ASSET — the whole
	// ZIP. `--sha256` compares the digest of the extracted LIBRARY, which the publisher
	// prints nowhere, so a reader who pastes the published digest into `--sha256` gets a
	// mismatch and has no way to tell whether the download is corrupt or they used the
	// wrong flag. Asking this module to know its own library's digest is also asking it
	// to trust a number that only exists because this module computed it, which is not
	// a provenance check at all.
	archiveSum, archiveSize, err := hashFile(zipPath)
	if err != nil {
		return err
	}
	wantArchive := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		flags["archive-sha256"], os.Getenv("SP_POC_ORT_ARCHIVE_SHA256"))))
	if wantArchive == "" {
		fmt.Printf("fetch-runtime: NO EXPECTED ARCHIVE DIGEST WAS SUPPLIED, so the download is NOT VERIFIED.\n"+
			"  archive sha256 = %s (%s)\n"+
			"  Release pages publish ONE digest and it is this one, the asset's. Read it against the release\n"+
			"  page and re-run with --archive-sha256=%s.\n"+
			"  (--sha256 is a different check: it pins the extracted library, which the publisher prints nowhere.)\n",
			archiveSum, soak.Bytes(archiveSize), archiveSum)
	} else if wantArchive != archiveSum {
		return fmt.Errorf("fetch-runtime: the DOWNLOADED ARCHIVE does not match the digest you expected.\n"+
			"  expected %s\n  actual   %s\n  size     %s\n  from     %s\n"+
			"  Nothing was extracted and nothing was written. The archive digest is the one a release page "+
			"publishes, so a mismatch here means the download is corrupt, the URL now points somewhere else, "+
			"or the expected digest belongs to a different asset — and which of those it is decides whether you "+
			"retry or stop.",
			wantArchive, archiveSum, soak.Bytes(archiveSize), url)
	} else {
		fmt.Printf("fetch-runtime: archive digest MATCHES the expected %s (%s)\n", archiveSum[:16], soak.Bytes(archiveSize))
	}

	member, err := extractLibrary(zipPath, work)
	if err != nil {
		return err
	}
	fmt.Printf("fetch-runtime: archive member %s\n", filepath.Base(member))

	sum, size, err := hashFile(member)
	if err != nil {
		return err
	}

	// The expected digest is compared BEFORE the file is put anywhere near `out`. A
	// library that does not match must never reach the path a run will load, even if
	// the mismatch is "just" a different build.
	expected := strings.ToLower(strings.TrimSpace(firstNonEmpty(flags["sha256"], os.Getenv("SP_POC_ORT_SHA256"))))
	verified := false
	verifiedBy := ""
	if expected != "" {
		if expected != sum {
			return fmt.Errorf("fetch-runtime: the download does not match the digest you expected.\n"+
				"  expected %s\n  actual   %s\n  from     %s\n"+
				"  Nothing was written to %s. Either the URL points at something else, the release was "+
				"re-cut, or the expected digest is from a different build — and which of those it is "+
				"changes what you should do next, so this refuses rather than warning.",
				expected, sum, url, out)
		}
		verified, verifiedBy = true, "library"
		fmt.Printf("fetch-runtime: digest MATCHES the expected %s\n", sum[:16])
	} else if wantArchive != "" {
		// The archive was confirmed above, so the file extracted from it is pinned to
		// something a publisher published. Saying `false` here would understate what
		// happened, and the field exists to say what actually did.
		verified, verifiedBy = true, "archive"
		fmt.Printf("fetch-runtime: library digest %s was not compared against anything of its own, but the\n"+
			"  ARCHIVE it came out of was confirmed, so these bytes are pinned to a published digest.\n", sum[:16])
	} else {
		fmt.Printf("fetch-runtime: NO EXPECTED DIGEST WAS SUPPLIED, so this is RECORDED AND NOT VERIFIED.\n"+
			"  sha256 = %s\n"+
			"  A release page publishes the ARCHIVE's digest, not this file's, so use --archive-sha256\n"+
			"  with that value. --sha256 pins this file and needs a digest nobody publishes.\n",
			sum)
	}

	if err := copyFile(member, out); err != nil {
		return err
	}

	rec := runtimeRecord{
		Kind: "onnxruntime", Path: filepath.ToSlash(out), SHA256: sum, Bytes: size,
		Licence: flags["licence"], Source: url, Verified: verified, VerifiedBy: verifiedBy,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if rec.Licence == "" {
		fmt.Println("fetch-runtime: WARNING --licence was empty, so the licence is RECORDED AS EMPTY.")
		fmt.Println("  That is not a licence. A shared library whose terms nobody wrote down is one")
		fmt.Println("  nobody has checked, and this repository's rule is that such a thing is")
		fmt.Println("  unaccounted. Pass --licence with the term you actually read.")
	}
	if err := writeRuntimeRecord(flags["manifest"], rec); err != nil {
		return err
	}

	fmt.Printf("\nfetch-runtime: wrote %s (%s)\n", out, soak.Bytes(size))
	fmt.Printf("  sha256   %s (%s)\n", sum, verifiedWord(rec.Verified))
	fmt.Printf("  licence  %s\n", licenceOrEmpty(rec.Licence))
	fmt.Printf("  recorded %s\n\n", flags["manifest"])

	// The companions. The provider DLLs are what make a GPU package a GPU package,
	// and they have to sit BESIDE the library because that is where ONNX Runtime looks
	// for them at session-creation time.
	companions, cerr := fetchProviderDLLs(zipPath, filepath.Dir(out), flags, url, verified, verifiedBy)
	if cerr != nil {
		return cerr
	}
	for _, c := range companions {
		fmt.Printf("  also wrote %s (%s) and recorded it\n", c.Path, soak.Bytes(c.Bytes))
	}
	if len(companions) == 0 {
		fmt.Println("\n  No execution-provider DLL was found in this archive, so this is a CPU-only build.")
		fmt.Println("  SP_POC_ORT_EP=cuda will be refused by name, before a session is opened, rather than")
		fmt.Println("  falling back to the CPU and recording numbers from the wrong machine. To get a CUDA")
		fmt.Println("  build, fetch the `-gpu` asset for this platform; the CPU one is named without it.")
	}
	fmt.Println("\nSet these to run the detector:")
	fmt.Printf("  SP_POC_ORT_LIB=%s\n", out)
	fmt.Printf("  SP_POC_ORT_SHA256=%s\n", sum)
	return nil
}

// providerKind is the manifest Kind for a companion library, and it NAMES the
// provider.
//
// The record's own doc comment said this field existed "so a manifest can also hold a
// CUDA or DirectML provider later without a schema change", and this is that later. A
// manifest with no schema change three weeks after the comment was written is worth
// more than a manifest with a new field and a migration nobody had to think about.
//
// One kind PER PROVIDER rather than one kind for all of them, because the manifest's
// replacement key is (kind, platform) and a GPU package ships more than one companion:
// `onnxruntime_providers_cuda.dll` and `onnxruntime_providers_tensorrt.dll` under a
// shared kind would overwrite each other and leave a manifest describing half of what
// is on disk.
func providerKind(base string) string { return "onnxruntime_provider_" + base }

// fetchProviderDLLs extracts the provider libraries that sit beside the main library in
// the archive, and records each one.
//
// It is a separate pass from extractLibrary rather than a mode of it, because
// `libraryCandidates` deliberately EXCLUDES anything containing "provider" — that
// exclusion is right for choosing which library to LOAD and would be wrong for choosing
// which files to KEEP. Folding them together is how a GPU package ends up installed as
// a CPU library that cannot find its own provider, and the symptom is a session that
// opens and quietly runs on the CPU.
//
// The archive is consulted for members in the SAME DIRECTORY as the library and no
// deeper: the provider lives beside the library, and a copy from a `training/` tree or
// a nested example directory is a different build with the same file name.
func fetchProviderDLLs(zipPath, destDir string, flags map[string]string, url string, verified bool, verifiedBy string) ([]runtimeRecord, error) {
	if flags["provider"] == "drop" {
		return nil, nil
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("fetch-runtime: cannot reopen the archive to look for provider libraries: %w", err)
	}
	defer func() { _ = zr.Close() }()

	suffix := sharedLibrarySuffix()
	index := map[string]*zip.File{}
	for _, f := range zr.File {
		index[f.Name] = f
	}
	var out []runtimeRecord
	for _, name := range providerCandidates(index, suffix) {
		target, xerr := extractMember(index[name], destDir)
		if xerr != nil {
			return nil, fmt.Errorf("fetch-runtime: cannot extract the provider library %s: %w",
				filepath.ToSlash(name), xerr)
		}
		sum, size, herr := hashFile(target)
		if herr != nil {
			return nil, herr
		}
		rec := runtimeRecord{
			Kind: providerKind(providerName(filepath.Base(target))), Path: filepath.ToSlash(target), SHA256: sum, Bytes: size,
			Licence: flags["licence"], Source: url,
			// The COMPANION is verified exactly when the download was, and the reason is
			// worth stating: a digest confirmed for onnxruntime.dll says nothing about a
			// file that came from the same download. It is the same archive, so it is the
			// same download, and it is recorded as such rather than as independently
			// checked.
			Verified:   verified,
			VerifiedBy: verifiedBy,
			FetchedAt:  time.Now().UTC().Format(time.RFC3339),
			Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		}
		if err := writeRuntimeRecord(flags["manifest"], rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// providerName is the provider a companion DLL implements, derived from its file name.
//
// `onnxruntime_providers_cuda.dll` -> `cuda`, so the manifest's Kind reads
// `onnxruntime_provider_cuda` and a reader can see which provider a row is for without
// matching on a path. Anything it cannot name keeps the whole base name, because an
// unrecognised companion is still a companion and dropping it silently is the failure
// this extraction exists to avoid.
func providerName(base string) string {
	stem := strings.TrimSuffix(base, sharedLibrarySuffix())
	stem = strings.TrimPrefix(stem, "onnxruntime_providers_")
	if stem == "" || stem == base {
		return strings.TrimSuffix(base, sharedLibrarySuffix())
	}
	return strings.ToLower(stem)
}

// providerCandidates names the provider libraries in the archive's library directory.
//
// It takes the archive index rather than a name slice so that it can be tested without
// building a ZIP, and it matches on the directory DEPTH as well as the name, because a
// `training/onnxruntime.dll` from a provider-shaped package is the wrong library and
// there is no way to tell from the name.
func providerCandidates(index map[string]*zip.File, suffix string) []string {
	var out []string
	for name := range index {
		if info := index[name]; info != nil && info.FileInfo().IsDir() {
			continue
		}
		slashed := filepath.ToSlash(name)
		base := strings.ToLower(filepath.Base(slashed))
		if !strings.HasSuffix(base, suffix) {
			continue
		}
		if !strings.Contains(base, "provider") {
			continue
		}
		// Beside the library, not deeper. The path in a Microsoft release is
		// `onnxruntime/lib/onnxruntime_providers_cuda.dll`, which is depth 2; a
		// training build or an example copy sits deeper still.
		if strings.Count(slashed, "/") > 2 {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// verifyRuntime re-checks what the manifest already recorded, with no network.
//
// This is the command somebody runs a month later, on a different machine, to find
// out whether the library sitting on disk is the one this project measured against.
// It is cheap, offline, and it prints the digest either way — a mismatch is the
// whole point of running it.
func verifyRuntime(manifest, lib string) error {
	recs, err := readRuntimeRecords(manifest)
	if err != nil {
		return fmt.Errorf("fetch-runtime --verify: %w", err)
	}
	path := lib
	if path == "" {
		path = defaultRuntimePath()
	}
	found := false
	for _, rec := range recs {
		if rec.Kind != "onnxruntime" {
			continue
		}
		found = true
		target := path
		if _, err := os.Stat(target); err != nil {
			target = rec.Path
		}
		sum, size, err := hashFile(target)
		if err != nil {
			fmt.Printf("fetch-runtime: %s is not readable: %v\n", target, err)
			continue
		}
		if sum == rec.SHA256 {
			fmt.Printf("fetch-runtime: %s MATCHES the recorded digest %s (%s, %s)\n",
				target, rec.SHA256[:16], soak.Bytes(size), rec.Platform)
			continue
		}
		// A mismatch is printed in full and returns an error. Two onnxruntime builds
		// on one machine are not ABI-compatible with each other's Go bindings, so a
		// silent mismatch here becomes a crash inside the detector a week later.
		fmt.Fprintf(os.Stderr, "fetch-runtime: %s DOES NOT MATCH the recorded digest.\n"+
			"  recorded %s  (%s)\n  on disk   %s  (%s)\n  from      %s\n"+
			"  Two ONNX Runtime versions on one machine are not ABI-compatible with each "+
			"other's Go bindings, so this is not a warning: it becomes a crash inside the "+
			"detector later. Point SP_POC_ORT_LIB at the recorded file, or re-record with "+
			"`fetch-runtime --url ... --sha256 ...` once you have decided which one you mean.\n",
			target, rec.SHA256, soak.Bytes(sizeOf(rec.Bytes)), sum, soak.Bytes(size), rec.Source)
		return fmt.Errorf("fetch-runtime: %s does not match the recorded digest", target)
	}
	if !found {
		return fmt.Errorf("fetch-runtime: %s records no onnxruntime entry, so there is nothing to "+
			"verify against. Run `pocctl fetch-runtime --url ...` first", manifest)
	}
	return nil
}

func writeRuntimeRecord(manifest string, rec runtimeRecord) error {
	recs, err := readRuntimeRecords(manifest)
	if err != nil {
		return err
	}
	// Replace an entry of the same kind and platform rather than appending: a
	// manifest that accumulates one row per download is a manifest whose "what is
	// installed" answer depends on how many times somebody re-fetched.
	//
	// The key is (kind, platform) and NOT (kind, platform, path). Adding the path
	// looks like the fix for several providers sharing a kind — and it is, but it is
	// the wrong fix, because it lets two fetch-runtime runs that installed DIFFERENT
	// libraries at different paths both survive, and `runtime --verify` then reports
	// two onnxruntime entries and no longer answers "what is installed". The providers
	// get distinct KINDS instead, which is what a kind is for.
	out := recs[:0:0]
	for _, r := range recs {
		if r.Kind == rec.Kind && r.Platform == rec.Platform {
			continue
		}
		out = append(out, r)
	}
	out = append(out, rec)
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifest, append(raw, '\n'), 0o644)
}

func readRuntimeRecords(manifest string) ([]runtimeRecord, error) {
	raw, err := os.ReadFile(manifest)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", manifest, err)
	}
	var out []runtimeRecord
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s is not readable as a runtime manifest: %w", manifest, err)
	}
	return out, nil
}

// libraryCandidates picks the ONNX Runtime library out of an archive's member
// names, by SUFFIX.
//
// It is a pure function taking the suffix as an argument rather than reading the
// platform inside, and that is deliberate: the selection rule is identical on all
// three platforms and the only thing that differs is the extension, so testing it
// on one machine must be able to exercise all three. A function that called
// `sharedLibrarySuffix()` itself could only ever be tested against the host.
//
// The rule is "ends with the shared-library suffix, is not a provider, is not the
// training build, and is not a versioned SONAME". Every exclusion is there because
// the alternative is loading the wrong file:
//
//   - a versioned SONAME (`libonnxruntime.so.1.23.0`) is the same library with a
//     version suffix, and loading it under the unversioned name defeats the
//     dynamic linker's whole purpose;
//   - a provider plugin loads with an import error deep inside cgo, on a frame,
//     after a run has started;
//   - the training build is a different ABI entirely.
func libraryCandidates(names []string, suffix string) []string {
	suffix = strings.ToLower(suffix)
	var out []string
	for _, name := range names {
		lower := strings.ToLower(filepath.ToSlash(name))
		if !strings.HasSuffix(lower, suffix) {
			continue
		}
		if strings.Contains(lower, "provider") || strings.Contains(lower, "training") {
			continue
		}
		// A SONAME with a version suffix: the extension is no longer last.
		base := strings.ToLower(filepath.Base(filepath.FromSlash(name)))
		if base != "libonnxruntime"+suffix && base != "onnxruntime"+suffix {
			continue
		}
		out = append(out, name)
	}
	return out
}

// extractLibrary finds the ONNX Runtime library inside a release ZIP.
//
// The member is chosen by NAME SUFFIX rather than by a hardcoded path, because the
// path moved between releases (`onnxruntime/bin/...` became `onnxruntime/lib/...`)
// and a hardcoded path is a command that worked once and then did not. If the
// archive holds more than one candidate the refusal says so and lists them, because
// guessing which of two DLLs a run should load is the decision this command exists
// to avoid.
func extractLibrary(zipPath, dest string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("the download is not a ZIP archive: %w\n"+
			"  If the URL pointed straight at the library rather than at the release ZIP, "+
			"pass that URL instead — this command expects the archive and picks the "+
			"library out of it", err)
	}
	defer func() { _ = zr.Close() }()

	names := make([]string, 0, len(zr.File))
	index := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
		index[f.Name] = f
	}
	candidates := libraryCandidates(names, sharedLibrarySuffix())

	if len(candidates) == 0 {
		return "", fmt.Errorf("the archive holds no onnxruntime %s. It may be the wrong asset for "+
			"this platform, or a release that ships its libraries somewhere else; list the archive "+
			"with any unzip tool and pass the URL of the asset that contains lib/libonnxruntime%s",
			sharedLibrarySuffix(), sharedLibrarySuffix())
	}
	if len(candidates) > 1 {
		var b strings.Builder
		for _, c := range candidates {
			b.WriteString("\n    ")
			b.WriteString(filepath.ToSlash(c))
		}
		return "", fmt.Errorf("the archive holds %d candidate libraries and this command will not "+
			"guess which one to load:%s\n  Download the one your platform needs by hand, or point "+
			"--url at an asset that holds exactly one", len(candidates), b.String())
	}

	return extractMember(index[candidates[0]], dest)
}

// extractMember writes one archive member into `dest` under its base name and returns
// where it landed.
func extractMember(f *zip.File, dest string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("cannot open %s in the archive: %w", filepath.ToSlash(f.Name), err)
	}
	defer func() { _ = rc.Close() }()
	target := filepath.Join(dest, filepath.Base(filepath.FromSlash(f.Name)))
	out, err := os.Create(target)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return target, nil
}

func sharedLibrarySuffix() string {
	if runtime.GOOS == "windows" {
		return ".dll"
	}
	if runtime.GOOS == "darwin" {
		return ".dylib"
	}
	return ".so"
}

func defaultRuntimePath() string {
	name := "libonnxruntime.so"
	if runtime.GOOS == "windows" {
		name = "onnxruntime.dll"
	} else if runtime.GOOS == "darwin" {
		name = "libonnxruntime.dylib"
	}
	return filepath.Join("models", name)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func verifiedWord(v bool) string {
	if v {
		return "VERIFIED against the digest you supplied"
	}
	return "RECORDED, NOT VERIFIED — supply --sha256 to confirm it"
}

func licenceOrEmpty(l string) string {
	if strings.TrimSpace(l) == "" {
		return "NOT RECORDED"
	}
	return l
}

func sizeOf(n int64) int64 { return n }
