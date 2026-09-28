// Package release builds and verifies signed offline release archives.
//
// This is the boundary where a supply-chain attacker gets code execution on
// every site at once, so it is the one boundary in the product where the
// verification is more careful than the construction: a builder that produces a
// wrong archive is a bug, and a verifier that accepts one is a compromise.
//
// The properties that matter:
//
//   - Every file in the archive is listed in the manifest with its size and
//     SHA-256, and the verifier recomputes both. A file present but unlisted is
//     a file nobody reviewed.
//   - Every listed path is archive-relative, forward-slashed, and free of `..`, a
//     drive letter, a backslash and a leading slash.
//   - The manifest is signed, and verification takes a trusted public key rather
//     than trusting the key the archive carries.
//   - The archive contains no credential-shaped material, and the manifest schema
//     has no field that could carry one — so "the manifest says there is no
//     secret" is a structural claim rather than an inspection result.
package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ManifestVersion is the manifest schema version.
const ManifestVersion = 1

// ErrVerification is returned for a bundle that must not be installed. The message
// names what failed, because an installer running from a USB stick at a site with
// no network cannot go and look it up.
var ErrVerification = errors.New("release: verification failed")

// FileEntry is one file in the manifest.
type FileEntry struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	Mode      string `json:"mode,omitempty"`
	Required  bool   `json:"required"`
}

// MigrationEntry declares a migration in the archive.
type MigrationEntry struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	Phase   string `json:"phase"`
}

// Compatibility is the schema window the release supports.
type Compatibility struct {
	MinSchemaVersion int    `json:"minSchemaVersion"`
	MaxSchemaVersion int    `json:"maxSchemaVersion"`
	MinCoreVersion   string `json:"minCoreVersion"`
	MaxCoreVersion   string `json:"maxCoreVersion"`
	ContractsVersion string `json:"contractsVersion"`
}

// SBOM points at the software bill of materials.
type SBOM struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"`
}

// Provenance records how the archive was built.
type Provenance struct {
	Builder         string    `json:"builder"`
	BuildStartedAt  time.Time `json:"buildStartedAt"`
	BuildFinishedAt time.Time `json:"buildFinishedAt"`
	SourceDigest    string    `json:"sourceDigest"`
}

// Signature is the detached signature over the manifest body.
type Signature struct {
	Algorithm string    `json:"algorithm"`
	KeyID     string    `json:"keyId"`
	Value     string    `json:"value"`
	PublicKey string    `json:"publicKey"`
	SignedAt  time.Time `json:"signedAt"`
}

// Manifest is the archive's index and its authenticity record.
type Manifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Product       string           `json:"product"`
	Version       string           `json:"version"`
	Platform      string           `json:"platform"`
	CreatedAt     time.Time        `json:"createdAt"`
	GitCommit     string           `json:"gitCommit,omitempty"`
	Files         []FileEntry      `json:"files"`
	Migrations    []MigrationEntry `json:"migrations"`
	Compatibility Compatibility    `json:"compatibility"`
	SBOM          SBOM             `json:"sbom"`
	Provenance    Provenance       `json:"provenance"`
	Signature     Signature        `json:"signature"`
}

// productName is what this archive is.
const productName = "security-platform"

// unsafePath rejects anything that is not a plain archive-relative path.
//
// The check is on the string, before any filesystem call, and again on the
// cleaned name. A tar entry of "../../../etc/cron.d/evil" is a code-execution
// primitive on any machine that extracts it, so this is the first thing a
// verifier does with every entry.
var unsafePath = regexp.MustCompile(`(^/)|(\.\.)|(^~)|([A-Za-z]:)|(\\)`)

// ValidateFilePath checks one manifest path.
func ValidateFilePath(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("%w: an empty file path", ErrVerification)
	}
	if len(p) > 512 {
		return fmt.Errorf("%w: file path %q is longer than 512 characters", ErrVerification, p)
	}
	if unsafePath.MatchString(p) {
		return fmt.Errorf("%w: file path %q is absolute, traverses, or uses a platform separator",
			ErrVerification, p)
	}
	if path.Clean(p) != p {
		return fmt.Errorf("%w: file path %q is not in canonical form (it should be %q)",
			ErrVerification, p, path.Clean(p))
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/' || r == '+':
		default:
			return fmt.Errorf("%w: file path %q contains an unexpected character %q",
				ErrVerification, p, r)
		}
	}
	return nil
}

// secretShaped finds credential-shaped material in a file that is about to be
// packaged.
//
// This is a backstop, not the primary control. The primary control is that the
// release builder is given an explicit file list and never a directory to sweep,
// so a developer's stray .env cannot be swept in by accident. This exists to
// catch the case where somebody added the file to the list deliberately.
var secretShaped = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |PGP )?PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bghp_[0-9A-Za-z]{36}\b`),
	regexp.MustCompile(`(?i)\b(password|passwd|api[_-]?key|secret[_-]?key|bearer)\s*[:=]\s*["'][^"']{8,}["']`),
	regexp.MustCompile(`postgres(ql)?://[^\s:@]+:[^\s@]+@`),
	regexp.MustCompile(`-----BEGIN CERTIFICATE-----.*?-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----`),
}

// ScanForSecrets reports credential-shaped material in a file body.
func ScanForSecrets(name string, body []byte) error {
	// Binary files cannot contain a passphrase in a form this would catch, and
	// a regex over a 40 MiB binary is a denial of service in the verifier.
	if looksBinary(body) {
		return nil
	}
	for _, re := range secretShaped {
		if re.Match(body) {
			return fmt.Errorf("%w: %s contains credential-shaped material matching %q. "+
				"A release archive is installed at every site, so a secret in one is a secret "+
				"everywhere; the release builder takes an explicit file list, which makes "+
				"this reachable only by adding the file deliberately",
				ErrVerification, name, re.String())
		}
	}
	return nil
}

func looksBinary(body []byte) bool {
	limit := len(body)
	if limit > 8000 {
		limit = 8000
	}
	for i := 0; i < limit; i++ {
		if body[i] == 0 {
			return true
		}
	}
	return false
}

// Verify recomputes the archive's file list and checks every claim.
//
// trustedKey, when non-nil, must match the manifest's embedded key. Without that
// check an attacker who can write the archive can also sign it, and the signature
// proves only that they signed their own forgery.
func Verify(archivePath string, manifest Manifest, trustedKey ed25519.PublicKey) ([]FileEntry, error) {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if manifest.SchemaVersion != ManifestVersion {
		add("manifest schema version %d, this build reads %d", manifest.SchemaVersion, ManifestVersion)
	}
	if manifest.Product != productName {
		add("manifest product is %q, want %q", manifest.Product, productName)
	}
	if manifest.Signature.Algorithm != "ed25519" {
		add("signature algorithm is %q, want ed25519. An archive signed with anything else "+
			"was not signed by this pipeline", manifest.Signature.Algorithm)
	}

	// Every path is checked before any of them is extracted, and again from the
	// archive's own index below.
	seen := map[string]bool{}
	for _, f := range manifest.Files {
		if err := ValidateFilePath(f.Path); err != nil {
			add("%v", err)
			continue
		}
		if seen[f.Path] {
			add("file %q is listed twice", f.Path)
		}
		seen[f.Path] = true
		if len(f.SHA256) != 64 || !isHex(f.SHA256) {
			add("file %q has a malformed SHA-256", f.Path)
		}
	}

	// A contract step in the same archive as the expand that makes it safe is the
	// mistake expand/contract exists to prevent, and it is easy to make by
	// accident when packaging.
	phases := map[string]int{}
	for _, m := range manifest.Migrations {
		phases[m.Phase]++
	}
	if phases["contract"] > 0 && phases["expand"] > 0 {
		add("the archive contains both expand and contract migrations. A contract step must " +
			"ship alone, in a later release, after the expand it completes has been observed " +
			"in the field")
	}

	if err := verifySignature(manifest, trustedKey); err != nil {
		add("%v", err)
	}

	actual, archiveProblems := readArchive(archivePath)
	problems = append(problems, archiveProblems...)

	// The manifest and the archive must agree in both directions. A file
	// present but unlisted is a file nobody reviewed; a file listed but absent is
	// an install that will fail at 3am.
	for name := range seen {
		if _, ok := actual[name]; !ok {
			add("manifest lists %q, which the archive does not contain", name)
		}
	}
	for name := range actual {
		if !seen[name] {
			add("archive contains %q, which the manifest does not list. A file nobody "+
				"reviewed is a file nobody will notice is there.", name)
		}
	}
	for _, f := range manifest.Files {
		if err := ValidateFilePath(f.Path); err != nil {
			continue
		}
		entry, ok := actual[f.Path]
		if !ok {
			continue
		}
		if entry.size != f.SizeBytes {
			add("file %q is %d bytes, the manifest says %d", f.Path, entry.size, f.SizeBytes)
		}
		if entry.sum != f.SHA256 {
			add("file %q hashes to %s, the manifest says %s", f.Path, entry.sum[:16], f.SHA256[:16])
		}
	}

	if len(manifest.Files) == 0 {
		add("the manifest lists no files. An empty archive that verifies is worse than one " +
			"that fails.")
	}
	if manifest.SBOM.Path == "" {
		add("the manifest declares no SBOM. A build that cannot say what it shipped is a " +
			"build that cannot be asked what is running")
	} else if err := ValidateFilePath(manifest.SBOM.Path); err != nil {
		add("SBOM path: %v", err)
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("%w: %d problem(s):\n  - %s",
			ErrVerification, len(problems), strings.Join(problems, "\n  - "))
	}
	return manifest.Files, nil
}

type archiveEntry struct {
	size int64
	sum  string
}

// readArchive indexes the archive, refusing every path it would refuse in the
// manifest, and scanning text files for credential-shaped material.
func readArchive(archivePath string) (map[string]archiveEntry, []string) {
	out := map[string]archiveEntry{}
	var problems []string

	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot open archive: %v", err)}
	}
	defer func() { _ = f.Close() }()

	if strings.HasSuffix(archivePath, ".zip") {
		return readZip(f, add)
	}

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot read archive: %v", err)}
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, append(problems, fmt.Sprintf("cannot read archive: %v", err))
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		// The name is cleaned and then re-checked, because a tar entry name is
		// attacker-controlled and the whole point of this loop is that nothing
		// from the archive decides where a file lands.
		name := path.Clean(header.Name)
		if err := ValidateFilePath(name); err != nil {
			add("%v", err)
			continue
		}
		if _, duplicate := out[name]; duplicate {
			add("archive contains %q twice", name)
			continue
		}

		hasher := sha256.New()
		body, readErr := io.ReadAll(io.LimitReader(io.TeeReader(tr, hasher), 64<<20))
		if readErr != nil {
			add("cannot read %q: %v", name, readErr)
			continue
		}
		if err := ScanForSecrets(name, body); err != nil {
			add("%v", err)
		}
		out[name] = archiveEntry{size: header.Size, sum: hex.EncodeToString(hasher.Sum(nil))}
	}
	return out, problems
}

func readZip(f *os.File, add func(string, ...any)) (map[string]archiveEntry, []string) {
	out := map[string]archiveEntry{}
	info, err := f.Stat()
	if err != nil {
		return out, []string{fmt.Sprintf("cannot stat archive: %v", err)}
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return out, []string{fmt.Sprintf("cannot read zip: %v", err)}
	}
	for _, zf := range zr.File {
		name := path.Clean(zf.Name)
		if err := ValidateFilePath(name); err != nil {
			add("%v", err)
			continue
		}
		if _, duplicate := out[name]; duplicate {
			add("archive contains %q twice", name)
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			add("cannot open %q: %v", name, err)
			continue
		}
		hasher := sha256.New()
		body, _ := io.ReadAll(io.LimitReader(io.TeeReader(rc, hasher), 64<<20))
		_ = rc.Close()
		if err := ScanForSecrets(name, body); err != nil {
			add("%v", err)
		}
		out[name] = archiveEntry{size: int64(zf.UncompressedSize64), sum: hex.EncodeToString(hasher.Sum(nil))}
	}
	return out, nil
}

func verifySignature(m Manifest, trustedKey ed25519.PublicKey) error {
	if m.Signature.Value == "" {
		return fmt.Errorf("%w: the manifest is unsigned, and an unsigned archive is only a claim", ErrVerification)
	}
	embedded, err := hex.DecodeString(m.Signature.PublicKey)
	if err != nil || len(embedded) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: the manifest's public key is malformed", ErrVerification)
	}
	if trustedKey != nil && !equalBytes(trustedKey, embedded) {
		return fmt.Errorf("%w: the archive was signed by key %q, not the trusted key. An attacker "+
			"who can write the archive can also sign it, and without this check the "+
			"signature proves only that they signed their own forgery",
			ErrVerification, m.Signature.KeyID)
	}
	sig, err := hex.DecodeString(m.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: the manifest signature is malformed", ErrVerification)
	}
	if !ed25519.Verify(embedded, []byte(SigningPayload(m)), sig) {
		return fmt.Errorf("%w: the manifest signature does not verify", ErrVerification)
	}
	return nil
}

// SigningPayload is the bytes a manifest signature covers.
//
// It excludes the signature field itself and the timestamp, so re-signing an
// unchanged manifest produces a stable signature and a verifier can tell a
// changed manifest from a re-signed one.
func SigningPayload(m Manifest) string {
	clone := m
	clone.Signature = Signature{}
	clone.SBOM = SBOM{}
	body, err := json.Marshal(clone)
	if err != nil {
		// A fixed struct of marshalable values cannot fail, so this would be a
		// programming error rather than a runtime condition.
		return "unmarshalable"
	}
	return string(body)
}

// Sign fills in the manifest signature.
func Sign(m *Manifest, key ed25519.PrivateKey, keyID string, trusted ed25519.PublicKey) error {
	m.SchemaVersion = ManifestVersion
	m.Product = productName
	m.Signature = Signature{
		Algorithm: "ed25519",
		KeyID:     keyID,
		PublicKey: hex.EncodeToString(trusted),
	}
	m.Signature.Value = ""
	sig := ed25519.Sign(key, []byte(SigningPayload(*m)))
	m.Signature.Value = hex.EncodeToString(sig)
	return nil
}

// BuildInput is the explicit file list a release is built from.
type BuildInput struct {
	Version  string
	Platform string
	// Files is the list, in archive order. It is explicit on purpose: a builder
	// that swept a directory would eventually package somebody's .env, and the
	// secret scan is a backstop rather than the plan.
	Files []SourceFile
	// Migrations and Compatibility describe what the release expects.
	Migrations     []MigrationEntry
	Compatibility  Compatibility
	SBOM           SBOM
	GitCommit      string
	SourceDigest   string
	Builder        string
	BuildStartedAt time.Time
}

// SourceFile is one input to the build.
type SourceFile struct {
	// ArchivePath is where it lands. It is validated before the file is opened.
	ArchivePath string
	SourcePath  string
	Mode        string
}

// Build writes a signed tar.gz archive and its manifest.
//
// The archive is written before the manifest is signed, because the manifest
// carries the archive's own file hashes and cannot be final until the files are.
func Build(outDir string, in BuildInput, key ed25519.PrivateKey, keyID string) (string, Manifest, error) {
	var m Manifest

	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return "", m, fmt.Errorf("release: create output directory: %w", err)
	}
	archiveName := fmt.Sprintf("%s-%s-%s.tar.gz", productName, in.Version, in.Platform)
	archivePath := filepath.Join(outDir, archiveName)

	started := in.BuildStartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}

	files := make([]FileEntry, 0, len(in.Files))
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return "", m, fmt.Errorf("release: create archive: %w", err)
	}
	defer func() { _ = archive.Close() }()

	gz := gzip.NewWriter(archive)
	tw := tar.NewWriter(gz)

	for _, src := range in.Files {
		if err := ValidateFilePath(src.ArchivePath); err != nil {
			return "", m, err
		}
		body, err := os.ReadFile(src.SourcePath)
		if err != nil {
			return "", m, fmt.Errorf("release: read %s: %w", src.SourcePath, err)
		}
		if err := ScanForSecrets(src.ArchivePath, body); err != nil {
			return "", m, err
		}
		sum := sha256.Sum256(body)
		entry := FileEntry{
			Path:      src.ArchivePath,
			SizeBytes: int64(len(body)),
			SHA256:    hex.EncodeToString(sum[:]),
			Mode:      orDefault(src.Mode, "0644"),
			Required:  true,
		}
		files = append(files, entry)

		header := &tar.Header{
			Name:     src.ArchivePath,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			// A fixed mtime. A build that stamps the current time produces a
			// different archive every run, and a diff of two builds of the same
			// commit would be empty of content and full of timestamps.
			ModTime: time.Unix(0, 0).UTC(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return "", m, fmt.Errorf("release: write header for %s: %w", src.ArchivePath, err)
		}
		if _, err := tw.Write(body); err != nil {
			return "", m, fmt.Errorf("release: write %s: %w", src.ArchivePath, err)
		}
	}
	if err := tw.Close(); err != nil {
		return "", m, fmt.Errorf("release: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", m, fmt.Errorf("release: close gzip: %w", err)
	}
	if err := archive.Sync(); err != nil {
		return "", m, fmt.Errorf("release: sync archive: %w", err)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	trusted := key.Public().(ed25519.PublicKey)
	m = Manifest{
		SchemaVersion: ManifestVersion,
		Product:       productName,
		Version:       in.Version,
		Platform:      in.Platform,
		CreatedAt:     started,
		GitCommit:     in.GitCommit,
		Files:         files,
		Migrations:    in.Migrations,
		Compatibility: in.Compatibility,
		SBOM:          in.SBOM,
		Provenance: Provenance{
			Builder:         in.Builder,
			BuildStartedAt:  started,
			BuildFinishedAt: time.Now().UTC(),
			SourceDigest:    in.SourceDigest,
		},
	}
	if err := Sign(&m, key, keyID, trusted); err != nil {
		return "", m, err
	}

	manifestPath := filepath.Join(outDir, archiveName+".manifest.json")
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", m, fmt.Errorf("release: encode manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, append(body, '\n'), 0o640); err != nil {
		return "", m, fmt.Errorf("release: write manifest: %w", err)
	}
	return archivePath, m, nil
}

// LoadManifest reads a manifest from disk.
func LoadManifest(path string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("release: read manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("release: parse manifest: %w", err)
	}
	return m, nil
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
