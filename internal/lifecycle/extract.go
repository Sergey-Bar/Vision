package lifecycle

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/release"
)

func marshalJSON(v any) ([]byte, error)   { return json.Marshal(v) }
func unmarshalJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

// Extracted is what an extraction produced.
type Extracted struct {
	Files []release.FileEntry
	Bytes int64
	Dir   string
}

// Extract unpacks a verified archive into a directory.
//
// Every entry's path is re-validated here rather than trusted from the manifest,
// because the manifest and the tar are two separate claims and the tar is the
// one that names where bytes land. A verified manifest with a traversing tar is
// exactly the case where only the second check saves the machine.
func Extract(archivePath, dest string, manifest release.Manifest, files []release.FileEntry) (Extracted, error) {
	var out Extracted
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return out, fmt.Errorf("lifecycle: create destination: %w", err)
	}
	expected := map[string]release.FileEntry{}
	for _, f := range files {
		expected[f.Path] = f
	}
	if len(expected) == 0 {
		return out, Refuse("the manifest lists no files; extracting an empty archive would " +
			"leave an install that appears to have succeeded")
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return out, fmt.Errorf("lifecycle: open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return out, fmt.Errorf("lifecycle: read archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, fmt.Errorf("lifecycle: read archive: %w", err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		name := cleanSlash(header.Name)
		if err := release.ValidateFilePath(name); err != nil {
			return out, err
		}
		if seen[name] {
			return out, Refuse("the archive contains %q twice", name)
		}
		seen[name] = true

		want, listed := expected[name]
		if !listed {
			return out, Refuse("the archive contains %q, which the manifest does not list. "+
				"Verify already refused this; extracting anyway would defeat it", name)
		}
		if len(want.SHA256) != 64 {
			// A manifest entry with no usable hash is not something to extract
			// against. Slicing it for a diagnostic would crash the installer,
			// which is the worst possible response to a malformed manifest: it
			// leaves the operator with a half-extracted install and no message.
			return out, Refuse("the manifest entry for %q has a malformed SHA-256 (%q)", name, want.SHA256)
		}

		target, err := safeJoin(dest, name)
		if err != nil {
			return out, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return out, fmt.Errorf("lifecycle: create directory for %q: %w", name, err)
		}

		hasher := sha256.New()
		body, err := io.ReadAll(io.LimitReader(io.TeeReader(tr, hasher), 256<<20))
		if err != nil {
			return out, fmt.Errorf("lifecycle: read %q: %w", name, err)
		}
		sum := hex.EncodeToString(hasher.Sum(nil))
		if sum != want.SHA256 {
			return out, Refuse("file %q hashes to %s, the manifest says %s", name, sum[:16], want.SHA256[:16])
		}
		mode := os.FileMode(0o640)
		if want.Mode == "0755" || want.Mode == "0750" {
			mode = 0o750
		}
		if err := os.WriteFile(target, body, mode); err != nil {
			return out, fmt.Errorf("lifecycle: write %q: %w", name, err)
		}
		out.Files = append(out.Files, want)
		out.Bytes += int64(len(body))
	}
	out.Dir = dest

	if len(seen) != len(expected) {
		var missing []string
		for name := range expected {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		return out, Refuse("the archive is missing %d listed file(s): %s",
			len(missing), strings.Join(missing, ", "))
	}
	return out, nil
}

// safeJoin refuses a path that leaves the destination, including by way of a
// symlink already present in the tree.
func safeJoin(dest, name string) (string, error) {
	full := filepath.Join(dest, filepath.FromSlash(name))
	root := dest
	if resolved, err := filepath.EvalSymlinks(dest); err == nil {
		root = resolved
	}
	if !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", Refuse("%q would be written outside the destination", name)
	}
	// Check each ancestor, so a link planted earlier in the tree is caught.
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", Refuse("%q is not under the destination", name)
	}
	current := root
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return full, nil
			}
			return "", fmt.Errorf("lifecycle: inspect %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", Refuse("%q traverses an unresolvable symlink at %q", name, segment)
			}
			if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
				return "", Refuse("%q traverses a symlink at %q pointing outside the destination",
					name, segment)
			}
		}
	}
	return full, nil
}

func cleanSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// Backup is a backup manifest.
//
// It records what a restore needs and what it must check, because a restore that
// trusts an archive it wrote itself is a restore that cannot detect its own
// corruption.
type Backup struct {
	ManifestVersion int       `json:"manifestVersion"`
	TakenAt         time.Time `json:"takenAt"`
	Product         string    `json:"product"`
	Version         string    `json:"version"`
	Platform        string    `json:"platform"`
	Tenant          string    `json:"tenant"`
	DatabaseFile    string    `json:"databaseFile"`
	DatabaseSHA256  string    `json:"databaseSha256"`
	DatabaseBytes   int64     `json:"databaseBytes"`
	EvidenceDir     string    `json:"evidenceDir,omitempty"`
	EvidenceFile    string    `json:"evidenceFile,omitempty"`
	EvidenceSHA256  string    `json:"evidenceSha256,omitempty"`
	EvidenceBytes   int64     `json:"evidenceBytes,omitempty"`
	CheckpointFile  string    `json:"checkpointFile,omitempty"`
	CheckpointSHA   string    `json:"checkpointSha256,omitempty"`
	// DatabaseSchema is the schema the dump was taken at, so a restore knows whether
	// the binary it is restoring under can read it.
	DatabaseSchema int `json:"databaseSchemaVersion"`
	// Complete is true when the evidence directory was included. A backup
	// without it is a partial backup, and it is recorded as one rather than
	// being indistinguishable from a whole one.
	Complete bool `json:"complete"`
	// Notes says why a backup is partial.
	Notes string `json:"notes,omitempty"`
}

// Validate refuses a manifest a restore should not act on.
func (b Backup) Validate() error {
	switch {
	case b.ManifestVersion != stateSchemaVersion:
		return Refuse("the backup manifest is version %d, this build reads %d", b.ManifestVersion, stateSchemaVersion)
	case b.DatabaseFile == "":
		return Refuse("the backup manifest names no database file")
	case len(b.DatabaseSHA256) != 64:
		return Refuse("the backup manifest has a malformed database hash")
	case b.DatabaseBytes <= 0:
		return Refuse("the backup manifest records an empty database; a zero-byte dump restores " +
			"to an empty schema and looks like a successful restore")
	}
	return nil
}

// HashFile hashes a file, and refuses a file larger than the cap.
//
// A backup reads whole files, and an unbounded read is a way for a full disk to
// turn a backup into an outage.
func HashFile(path string, maxBytes int64) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("lifecycle: open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("lifecycle: stat %s: %w", path, err)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return "", 0, Refuse("%s is %d bytes, over the %d byte backup cap",
			path, info.Size(), maxBytes)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("lifecycle: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
