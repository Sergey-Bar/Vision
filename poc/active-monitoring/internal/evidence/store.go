// Package evidence is the content-addressed clip store.
//
// Three rules, each with tests:
//
//   - The path is derived from the content's SHA-256, never supplied. A caller
//     that could name a path could name someone else's evidence, or escape the
//     store.
//   - A clip is not "stored" because a process exited zero. It is stored when its
//     bytes were read back from the store and hashed to the same value.
//   - Age-based sweeping only. A sweep that built a path by hand is a sweep that
//     can delete something it was not meant to.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Refused describes a path that was rejected, and by which rule. The rule name
// is part of the message so a test can assert the rule and a log can say which
// check fired.
type Refused struct {
	Input  string
	Rule   string
	Reason string
}

func (r *Refused) Error() string {
	return fmt.Sprintf("evidence: refusing %q (%s): %s", r.Input, r.Rule, r.Reason)
}

// The rules, named. A refusal without a rule name is a shrug.
const (
	RuleAbsolute  = "absolute-path"
	RuleTraversal = "parent-traversal"
	RuleNUL       = "nul-byte"
	RuleEmpty     = "empty-name"
	RuleNotHash   = "not-a-sha256"
	RuleEscape    = "escapes-the-store"
	RuleLink      = "symlink-or-junction"
	RuleWindows   = "reserved-or-illegal-character"
	// RuleKind is T7.3: a file whose kind is not one of the three is a file nobody
	// can open with confidence about what it is.
	RuleKind = "not-an-evidence-kind"
)

// Store is the content-addressed directory.
type Store struct {
	Root string
}

// Open prepares the store directory.
func Open(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("evidence: the store root %s is not a usable path: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("evidence: cannot create the store at %s: %w", abs, err)
	}
	return &Store{Root: abs}, nil
}

// isSHA256 is the whole of the naming rule. A 64-character lowercase hex string
// and nothing else, so a path can never contain a separator.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// extFor is declared below, next to the kind vocabulary it belongs to.// The kinds of evidence a detection can point at. T7.3. Closed, and each one
// answers a different question a reader asks:
//
//	CrossingSnapshot  a rendered still of the crossing, for "what did it decide,
//	                  and on what"
//	EventClip         a video of the crossing, for "what actually happened"
//	Frame             one source frame, for "what did the detector see"
//
// They live HERE and not in internal/store, and the direction is one way: this
// package is the lower one, because it owns the bytes and it is what chooses the
// extension a reader's file manager will show them. A vocabulary defined in the
// storage layer and imported downward would be a second source of truth for a
// set of three strings, and the failure mode of that is an extension that says
// `.mp4` beside a row that says `crossing_snapshot`.
const (
	CrossingSnapshot = "crossing_snapshot"
	EventClip        = "event_clip"
	Frame            = "frame"
)

// ValidKind is the closed set. A kind outside it is refused rather than stored: a
// reader who cannot look the kind up will not know what opening the file means.
func ValidKind(k string) bool {
	switch k {
	case CrossingSnapshot, EventClip, Frame:
		return true
	}
	return false
}

// extFor is the extension each evidence kind is stored with.
//
// It is chosen by the KIND and not by the caller, because a caller that chose it
// would eventually choose it wrong, and the consequence is a file a reader opens
// not knowing what it is: a picture where they expected a video, or a video
// where they expected the picture of the decision. The extension is the cheapest
// possible kind marker — it needs no database read to interpret, so a directory
// listing is already readable — and it is why the kind has to agree with the
// bytes rather than being an attribute bolted on afterwards.
func extFor(kind string) string {
	switch kind {
	case EventClip:
		return ".mp4"
	case CrossingSnapshot, Frame:
		return ".jpg"
	default:
		// An unknown kind is stored as `.bin` rather than guessed at. A file
		// whose extension says nothing is honest about saying nothing.
		return ".bin"
	}
}

// RefKind for a hash and a kind. The hash rules are the whole of the naming rule
// and live in `refForHash`; the kind only chooses the extension, which is what
// makes a directory listing readable without a database.
func (s *Store) RefKind(sum, kind string) (string, error) {
	if !ValidKind(kind) {
		return "", &Refused{kind, RuleKind, "the three kinds are " + EventClip + ", " +
			CrossingSnapshot + " and " + Frame +
			"; a file whose kind is not one of them is a file nobody can open with confidence"}
	}
	full, err := s.refForHash(sum)
	if err != nil {
		return "", err
	}
	out := strings.TrimSuffix(full, ".mp4") + extFor(kind)
	if !within(s.Root, out) {
		return "", &Refused{sum, RuleEscape, "the derived path does not lie inside the store root"}
	}
	return out, nil
}

// Ref for a hash, with every check a supplied path would need. It is exported
// so the tests can enumerate the refusals, and so a future caller that tries to
// store under a name it chose is caught by the same rules.
//
// The default kind is `event_clip`, which is the kind every pre-T7.3 write was:
// a clip. Callers that write a picture must say so, because saying so is the
// entire content of T7.3.
func (s *Store) Ref(sum string) (string, error) {
	return s.RefKind(sum, EventClip)
}

// refForHash is the body both Ref and RefKind share. The hash rules are the whole
// of the naming rule: 64 lowercase hex characters and nothing else, so a path can
// never contain a separator. The kind only chooses the extension.
func (s *Store) refForHash(sum string) (string, error) {
	if sum == "" {
		return "", &Refused{sum, RuleEmpty, "an empty content hash names nothing, so the file it would refer to cannot exist"}
	}
	if strings.ContainsRune(sum, 0) {
		return "", &Refused{sum, RuleNUL, "a NUL byte truncates the path in every layer below Go, so the file that was written is not the file that was named"}
	}
	if filepath.IsAbs(sum) || strings.Contains(sum, ":\\") || strings.HasPrefix(sum, "/") || strings.HasPrefix(sum, `\\`) {
		return "", &Refused{sum, RuleAbsolute, "an absolute path would place evidence outside the store, where nothing sweeps it and nothing reads it back"}
	}
	// A drive-relative path such as C:x resolves against that drive's current
	// directory, which is not the store and is not even stable.
	if len(sum) >= 2 && sum[1] == ':' {
		return "", &Refused{sum, RuleAbsolute, "a drive-relative path resolves against that drive's current directory, which is not the store"}
	}
	if strings.ContainsAny(sum, `/\`) {
		return "", &Refused{sum, RuleTraversal, "a separator in the name would place evidence in a directory the store does not own"}
	}
	if sum == "." || sum == ".." || strings.Contains(sum, "..") {
		return "", &Refused{sum, RuleTraversal, "a parent reference would place evidence outside the store"}
	}
	// The control-character rule comes before the hex rule: a name with a
	// control byte is rejected for a more fundamental reason than being the
	// wrong shape, and the operator needs the more fundamental reason.
	for _, r := range sum {
		if r < 0x20 || r == 0x7f {
			return "", &Refused{sum, RuleWindows, "a control character is not a legal file name character"}
		}
	}
	if !isSHA256(sum) {
		return "", &Refused{sum, RuleNotHash, "a store path is derived from a SHA-256, so the name must be 64 lowercase hex characters; a name chosen by a caller is a name a caller can collide"}
	}
	// Two levels, so no directory holds a hundred thousand entries. The levels
	// are the first two bytes of the hash, which is why they cannot be chosen.
	dir := filepath.Join(s.Root, sum[:2], sum[2:4])
	full := filepath.Join(dir, sum+".mp4")
	// Belt and braces: verify the derived path is inside the root even after
	// filepath.Join has cleaned it.
	if !within(s.Root, full) {
		return "", &Refused{sum, RuleEscape, "the derived path does not lie inside the store root"}
	}
	return full, nil
}

// refuseLinks walks every component between the store root and the destination
// and refuses a symlink or a junction at any of them.
//
// W2-7. `RuleLink` was declared in the rule set and nothing ever emitted it,
// which is the worst state a guard can be in: the table says the check exists, so
// a reader concludes the store is protected against a link being planted inside
// it, and no code has ever looked. The evidence store is about to hold the clips a
// proof chain points at, and a proof chain that can be redirected by a directory
// some other process created is not a proof chain.
//
// A junction on Windows is not a symbolic link and `os.Lstat` does not report it
// as one, so the mode is checked for irregularity as well: `os.ModeIrregular` is
// what a reparse point that is not a name-surrogate looks like through this API,
// and a junction is exactly that. Skipping it would leave the rule true on Linux
// and false on the platform this module actually runs on.
//
// A component that does not exist is not a refusal. `PutKind` creates the
// directories immediately above the destination before calling this, so anything
// missing between the root and the leaf is a race we lose to a cleaner rather
// than an attack we should refuse.
func refuseLinks(root, dst string) error {
	rel, err := filepath.Rel(root, dst)
	if err != nil {
		return &Refused{dst, RuleLink, fmt.Sprintf("the destination cannot be related to the store root (%v)", err)}
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return &Refused{cur, RuleLink, fmt.Sprintf("the component cannot be inspected (%v)", err)}
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeIrregular != 0 {
			return &Refused{cur, RuleLink, "the store contains a symbolic link or a junction at this path, so a write " +
				"here would land outside the store; deleting the guard is not a fix and this rule is the guard"}
		}
	}
	return nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

// Put stores the file at src under its own hash and verifies the copy by
// reading it back. It returns the hash and the size.
//
// The kind defaults to `event_clip`, which is what every pre-T7.3 write was. A
// caller writing a picture uses `PutKind`, and the reason the default is a clip
// rather than a refusal is that refusing here would break every existing call
// site for a rule that has no teeth: the bytes are identical either way and only
// the extension differs.
func (s *Store) Put(src string) (Stored, error) { return s.PutKind(src, EventClip) }

// PutKind is `Put` with the kind said out loud. T7.3.
func (s *Store) PutKind(src, kind string) (Stored, error) {
	if !ValidKind(kind) {
		return Stored{}, &Refused{kind, RuleKind, "the three kinds are " + EventClip + ", " +
			CrossingSnapshot + " and " + Frame + "; storing a file as some other kind is storing it as something a reader cannot open"}
	}
	info, err := os.Stat(src)
	if err != nil {
		return Stored{}, fmt.Errorf("evidence: cannot read the %s at %s: %w", kind, src, err)
	}
	if info.Size() == 0 {
		return Stored{}, fmt.Errorf("evidence: the %s at %s is zero bytes; evidence that shows nothing is not evidence, and storing it would make AC3 count a lie", kind, src)
	}
	sum, size, err := hashFile(src)
	if err != nil {
		return Stored{}, err
	}
	dst, err := s.RefKind(sum, kind)
	if err != nil {
		return Stored{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Stored{}, fmt.Errorf("evidence: cannot create %s: %w", filepath.Dir(dst), err)
	}
	if _, err := os.Stat(dst); err == nil {
		// Already stored: the content is the same, so there is nothing to do.
		// Deduplication is the point of a content-addressed store.
		return Stored{Hash: sum, Path: dst, Bytes: size, Kind: kind, Deduplicated: true}, nil
	}
	if err := copyFile(src, dst); err != nil {
		return Stored{}, err
	}
	// W2-7: the destination is walked AFTER the copy and BEFORE the read-back, so
	// a link that appeared under the store while we were copying is caught before
	// anything is recorded as evidence.
	if err := refuseLinks(s.Root, dst); err != nil {
		_ = os.Remove(dst)
		return Stored{}, err
	}
	// R5: read the copy back and hash it. A successful copy() is not evidence
	// that the bytes landed.
	got, gotSize, err := hashFile(dst)
	if err != nil {
		return Stored{}, fmt.Errorf("evidence: the stored %s at %s cannot be read back, so it is not stored: %w", kind, dst, err)
	}
	if got != sum || gotSize != size {
		_ = os.Remove(dst)
		return Stored{}, fmt.Errorf("evidence: the stored %s at %s reads back as %s (%d bytes) but was %s (%d bytes); the copy is discarded rather than recorded as evidence",
			kind, dst, got, gotSize, sum, size)
	}
	return Stored{Hash: sum, Path: dst, Bytes: size, Kind: kind}, nil
}

// Stored is the result of a successful Put.
type Stored struct {
	Hash  string
	Path  string
	Bytes int64
	// Kind is what this file IS, and it decided the extension the file was
	// written with. T7.3. It is on the result rather than inferred from the path
	// because a caller that has to re-derive it from a string is a caller that
	// will eventually get it wrong.
	Kind         string
	Deduplicated bool
}

// StorageRef is the value written into the database and into the pushed event.
// It is relative to the store root, so a data root that moves between machines
// does not invalidate the references in the audit trail.
func (s *Store) StorageRef(sum string) string { return s.StorageRefKind(sum, EventClip) }

// StorageRefKind is `StorageRef` for an evidence kind.
//
// It exists because the extension is chosen by the KIND and `StorageRef` used to
// hardcode `.mp4`. A caller that stored a JPEG and then asked for the reference
// would have been handed the name of a file that does not exist, which is not a
// wrong answer that looks right — it is a reference to nothing, and the row
// pointing at it would look complete.
func (s *Store) StorageRefKind(sum, kind string) string {
	return StorageRefKindOf(sum, kind)
}

// StorageRefKindOf is the relative storage reference for a content hash and a
// kind.
//
// It is a function rather than a method because the runner needs to build the
// reference for a row it is writing and the store that holds the bytes is a
// different package-boundary concern from the shape of the reference. The method
// above is kept so every existing caller keeps working, and the two share this
// one body — a second implementation of the path shape is a second thing to keep
// in sync with the extension table.
func StorageRefKindOf(sum, kind string) string {
	return filepath.ToSlash(filepath.Join(sum[:2], sum[2:4], sum+extFor(kind)))
}

// Get reads a stored object back and returns its hash. It is what AC3 calls.
func (s *Store) Get(sum string) (string, int64, error) {
	p, err := s.Ref(sum)
	if err != nil {
		return "", 0, err
	}
	return hashFile(p)
}

// Open returns a reader over a stored object, for the screen's play button.
func (s *Store) Open(sum string) (*os.File, error) {
	p, err := s.Ref(sum)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("evidence: %s is recorded but not readable: %w", p, err)
	}
	return f, nil
}

// Entry is one object in the store, for the sweep.
type Entry struct {
	Hash    string
	Path    string
	Bytes   int64
	ModTime time.Time
}

// Entries lists what is stored, newest last.
func (s *Store) Entries() ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".mp4") {
			return nil
		}
		sum := strings.TrimSuffix(name, ".mp4")
		if !isSHA256(sum) {
			// A file whose name is not a hash did not come from Put. Sweeping
			// must not touch it, because the store does not own it.
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, Entry{Hash: sum, Path: path, Bytes: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("evidence: cannot list the store at %s: %w", s.Root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.Before(out[j].ModTime) })
	return out, nil
}

// SweepResult says what a sweep removed and, more importantly, what it refused
// to remove.
type SweepResult struct {
	Removed    []string `json:"removed"`
	BytesFreed int64    `json:"bytes_freed"`
	Kept       int      `json:"kept"`
	Reason     string   `json:"reason"`
}

// Sweep removes objects older than the cutoff, oldest first, until either the
// cutoff has been applied or the free-space target is met.
//
// The cutoff is the primary rule and the byte target is a secondary one. An
// operator who declares a retention period gets that period; the byte target
// only ever removes things that are already older than the cutoff, so it can
// never delete a fresh clip to satisfy a disk number.
func (s *Store) Sweep(cutoff time.Time, targetBytes int64) (SweepResult, error) {
	res := SweepResult{Reason: fmt.Sprintf("objects older than %s", cutoff.UTC().Format(time.RFC3339))}
	entries, err := s.Entries()
	if err != nil {
		return res, err
	}
	res.Kept = len(entries)
	var freed int64
	for _, e := range entries {
		if e.ModTime.After(cutoff) {
			continue
		}
		if targetBytes > 0 && freed >= targetBytes {
			break
		}
		if err := os.Remove(e.Path); err != nil {
			// Refuse loudly rather than carrying on: a sweep that cannot delete
			// what it selected is a sweep whose result nobody can trust.
			return res, fmt.Errorf("evidence: cannot sweep %s: %w", e.Path, err)
		}
		res.Removed = append(res.Removed, e.Path)
		freed += e.Bytes
	}
	res.BytesFreed = freed
	return res, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("evidence: cannot read %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("evidence: cannot hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// HashBytes is the same function for a buffer, so a caller that already has the
// bytes does not have to write them out to hash them.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("evidence: cannot read %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("evidence: cannot create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("evidence: cannot copy %s to %s: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("evidence: cannot flush %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("evidence: cannot close %s: %w", dst, err)
	}
	return nil
}

var _ = errors.New
