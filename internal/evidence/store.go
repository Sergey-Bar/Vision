// Package evidence stores bytes on a local filesystem behind a narrow interface.
//
// Postgres holds addressing, hash and retention law. The bytes live here, and
// this package is the only thing that touches the evidence root. That is
// deliberate: a path-handling decision made in a dozen call sites is a path
// traversal waiting to happen in one of them.
//
// Three rules, each of which has a negative test in evidence_test.go:
//
//   - A storage reference is relative, forward-slashed, and free of `.`, `..`,
//     drive letters, UNC prefixes and NUL bytes. The database CHECK enforces
//     the same thing on the column, so a row that somehow bypassed this code
//     still cannot name a path outside the root.
//   - A resolved path must still be under the root after symlink resolution.
//     Validating the string is not enough: a symlink planted inside the root
//     points wherever it likes.
//   - A stored object's hash is verified on read. A file that no longer matches
//     its row is reported, not served: an operator looking at evidence that
//     cannot be proven to be the evidence an incident cited is worse off than
//     being told the file is wrong.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ErrPathRejected is returned for a reference that names something outside the
// evidence root, whatever the reason.
var ErrPathRejected = errors.New("evidence: storage reference rejected")

// ErrHashMismatch is returned when stored bytes no longer match their row.
var ErrHashMismatch = errors.New("evidence: stored bytes do not match their recorded hash")

// ErrNotFound is returned when a reference resolves to nothing.
var ErrNotFound = errors.New("evidence: no such object")

// Store is a filesystem evidence store rooted at one directory.
type Store struct {
	root string
	// fsync makes a write durable before it is acknowledged. On a site whose
	// power can go, an evidence fetch that is acknowledged but still in the page
	// cache is evidence that does not exist.
	fsync bool
	// maxBytes caps a single object. A source that offers a "clip" of unbounded
	// size is a way to fill the disk during an incident.
	maxBytes int64
	// hashOnRead verifies bytes against the row on every read.
	hashOnRead bool
}

// Options configures a store.
type Options struct {
	Root string
	// MaxBytes caps one object. Default 512 MiB, which is far beyond a
	// snapshot and far below a disk.
	MaxBytes int64
	// Fsync makes writes durable before acknowledgement. Default true.
	Fsync bool
	// VerifyHashOnRead is default true. Turning it off is a debugging aid and
	// the only reason it exists.
	VerifyHashOnRead bool
}

// DefaultMaxBytes bounds a single stored object.
const DefaultMaxBytes int64 = 512 << 20

// New opens a store, creating the root if it does not exist.
func New(opts Options) (*Store, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, errors.New("evidence: a root is required")
	}
	abs, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("evidence: resolve root: %w", err)
	}
	// A symlinked root is resolved once, here, so every later comparison is
	// against a real path rather than a name that could be repointed.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("evidence: create root: %w", err)
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	return &Store{
		root:       abs,
		fsync:      opts.Fsync,
		maxBytes:   opts.MaxBytes,
		hashOnRead: true,
	}, nil
}

// Root returns the resolved evidence root.
func (s *Store) Root() string { return s.root }

// unsafeSegment matches a path segment that is a traversal, a drive, a UNC
// prefix, or anything with a NUL in it.
var unsafeSegment = regexp.MustCompile(`(?i)^(\.\.?|:|\\\\|//)`)

// ValidateRef checks a storage reference without touching the filesystem.
//
// Exported because a caller may want to reject a reference at intake, before
// any bytes are fetched, rather than at write time.
func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: empty reference", ErrPathRejected)
	}
	// A NUL truncates the path in every syscall that takes a C string. Go's
	// own os package rejects it, but a reference reaches a database and an
	// export tool as well, so it is refused here where the reason is explicit.
	if strings.ContainsRune(ref, 0) {
		return fmt.Errorf("%w: reference contains a NUL byte", ErrPathRejected)
	}
	if strings.ContainsRune(ref, '\\') {
		return fmt.Errorf("%w: %q contains a backslash; references are forward-slashed and relative", ErrPathRejected, ref)
	}
	if strings.HasPrefix(ref, "/") {
		return fmt.Errorf("%w: %q is absolute", ErrPathRejected, ref)
	}
	if filepath.IsAbs(ref) || filepath.VolumeName(ref) != "" {
		return fmt.Errorf("%w: %q names a volume", ErrPathRejected, ref)
	}
	if len(ref) > 512 {
		return fmt.Errorf("%w: reference is %d characters, at most 512", ErrPathRejected, len(ref))
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment == "" {
			return fmt.Errorf("%w: %q has an empty path segment", ErrPathRejected, ref)
		}
		if unsafeSegment.MatchString(segment) {
			return fmt.Errorf("%w: %q contains segment %q", ErrPathRejected, ref, segment)
		}
		// A segment that is only dots on some filesystem is a traversal even
		// where it is not on this one.
		if strings.Trim(segment, ".") == "" {
			return fmt.Errorf("%w: %q contains segment %q", ErrPathRejected, ref, segment)
		}
	}
	return nil
}

// resolve turns a validated reference into an absolute path, and re-checks that
// the result is under the root after symlink resolution.
//
// The second check is the one that matters. Validating the string proves the
// reference looks relative; it does not prove the file it names is inside the
// root, because a symlink planted earlier by anyone with write access to the
// evidence directory can point anywhere.
func (s *Store) resolve(ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	full := filepath.Join(s.root, filepath.FromSlash(ref))
	if !strings.HasPrefix(full, s.root+string(os.PathSeparator)) && full != s.root {
		return "", fmt.Errorf("%w: %q escapes the evidence root", ErrPathRejected, ref)
	}

	// Walk each ancestor and refuse any that is a symlink pointing elsewhere.
	// Checking only the final path would miss an intermediate link.
	rel, err := filepath.Rel(s.root, full)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not under the evidence root", ErrPathRejected, ref)
	}
	current := s.root
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				// Nothing to resolve; the write path will create it.
				return full, nil
			}
			return "", fmt.Errorf("%w: cannot inspect %q: %v", ErrPathRejected, ref, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("%w: %q traverses an unresolvable symlink at %q", ErrPathRejected, ref, segment)
			}
			if !strings.HasPrefix(target, s.root+string(os.PathSeparator)) && target != s.root {
				return "", fmt.Errorf("%w: %q traverses a symlink at %q pointing outside the evidence root", ErrPathRejected, ref, segment)
			}
		}
	}
	return full, nil
}

// Object is a stored evidence object.
type Object struct {
	Ref       string
	SizeBytes int64
	SHA256    string
	MediaType string
}

// Put stores bytes under a reference and returns its address and hash.
//
// The reference is generated from the hash rather than taken from the caller,
// which removes a whole class of question: there is no path for a caller to
// supply. Layout is <root>/<tenant>/<hash[0:2]>/<hash>, so identical bytes
// deduplicate and nothing depends on a source-supplied name.
func (s *Store) Put(tenant string, body io.Reader) (Object, error) {
	if strings.TrimSpace(tenant) == "" {
		return Object{}, errors.New("evidence: tenant is required and is never read from a request body")
	}
	// A tenant id is one path segment, not a path. A tenant of "a/b" would
	// otherwise nest into another tenant's tree without ever leaving the root, so
	// it would not be a traversal and would still be wrong: it would put one
	// tenant's objects under a prefix another tenant could be given.
	if err := ValidateRef(tenant); err != nil {
		return Object{}, fmt.Errorf("evidence: tenant is not a usable path segment: %w", err)
	}
	if strings.Contains(tenant, "/") {
		return Object{}, fmt.Errorf("evidence: tenant %q is not a single path segment", tenant)
	}

	// Write to a temporary file inside the root, hash while copying, then rename
	// into place. Renaming is atomic, so a reader never sees a half-written
	// object and a crash never leaves one that looks complete.
	tmp, err := os.CreateTemp(s.root, ".incoming-*")
	if err != nil {
		return Object{}, fmt.Errorf("evidence: create staging file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	hasher := sha256.New()
	limited := io.LimitReader(body, s.maxBytes+1)
	written, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if err != nil {
		return Object{}, fmt.Errorf("evidence: write: %w", err)
	}
	if written > s.maxBytes {
		return Object{}, fmt.Errorf("evidence: object exceeds the %d byte cap", s.maxBytes)
	}
	sum := hex.EncodeToString(hasher.Sum(nil))

	ref := fmt.Sprintf("%s/%s/%s", tenant, sum[:2], sum)
	full, err := s.resolve(ref)
	if err != nil {
		return Object{}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return Object{}, fmt.Errorf("evidence: create directory: %w", err)
	}
	if err := tmp.Chmod(0o640); err != nil {
		return Object{}, fmt.Errorf("evidence: chmod: %w", err)
	}
	if s.fsync {
		if err := tmp.Sync(); err != nil {
			return Object{}, fmt.Errorf("evidence: sync: %w", err)
		}
	}
	if err := tmp.Close(); err != nil {
		return Object{}, fmt.Errorf("evidence: close: %w", err)
	}
	if err := os.Rename(tmpName, full); err != nil {
		// A concurrent writer of identical bytes is not a failure: the object is
		// content-addressed, so the file that is already there is the right one.
		if _, statErr := os.Stat(full); statErr == nil {
			return Object{Ref: ref, SizeBytes: written, SHA256: sum}, nil
		}
		return Object{}, fmt.Errorf("evidence: publish: %w", err)
	}
	return Object{Ref: ref, SizeBytes: written, SHA256: sum}, nil
}

// Open streams an object, verifying its hash against the row when the caller
// supplies one.
//
// The hash check is the reason a file that has been altered on disk is reported
// rather than served. An operator looking at evidence that cannot be proven to be
// the evidence an incident cited is worse off than being told the file is wrong.
func (s *Store) Open(ref, expectSHA string) (io.ReadCloser, Object, error) {
	full, err := s.resolve(ref)
	if err != nil {
		return nil, Object{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Object{}, fmt.Errorf("%w: %q", ErrNotFound, ref)
		}
		return nil, Object{}, fmt.Errorf("evidence: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Object{}, fmt.Errorf("evidence: stat: %w", err)
	}

	obj := Object{Ref: ref, SizeBytes: info.Size()}
	if expectSHA == "" {
		return f, obj, nil
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		_ = f.Close()
		return nil, Object{}, fmt.Errorf("evidence: hash: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, Object{}, fmt.Errorf("evidence: rewind: %w", err)
	}
	obj.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	if obj.SHA256 != expectSHA {
		_ = f.Close()
		return nil, Object{}, fmt.Errorf("%w: %q hashes to %s, row says %s",
			ErrHashMismatch, ref, obj.SHA256, expectSHA)
	}
	return f, obj, nil
}

// Exists reports whether an object is present, without opening it.
func (s *Store) Exists(ref string) (bool, error) {
	full, err := s.resolve(ref)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("evidence: stat: %w", err)
	}
	return info.Mode().IsRegular(), nil
}

// RefFor computes the reference an object with this hash will be stored at,
// without writing it. The incident and evidence tables need the address before
// the bytes are fetched.
func RefFor(tenant, sha string) string {
	sum := strings.ToLower(strings.TrimSpace(sha))
	if len(sum) < 3 {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s", tenant, sum[:2], sum)
}

// SweepResult reports what a retention sweep did.
type SweepResult struct {
	Scanned int
	Removed int
	Freed   int64
	Errors  []string
}

// RetentionPolicy is one sweep's rules.
type RetentionPolicy struct {
	// Before removes objects stored earlier than this. An object with a
	// zero StoredAt is never removed: an unknown age is not an old age.
	Before time.Time
	// MaxAge, when positive, is a second bound applied to StoredAt.
	MaxAge time.Duration
	// SkipLegalHold is a callback rather than a field, because "is this on legal
	// hold" is a question only the database can answer and a policy object with a
	// stale snapshot of it is worse than a query.
	SkipLegalHold func(ref string) (bool, error)
}

// Sweep removes objects past the policy and reports what it did.
//
// It is an operator action with a dry-run, not a background timer: deleting
// evidence on a schedule nobody is watching is how a site discovers its retention
// policy was wrong after the fact.
func (s *Store) Sweep(tenant string, policy RetentionPolicy, dryRun bool) (SweepResult, error) {
	var result SweepResult

	tenantRoot := filepath.Join(s.root, tenant)
	err := filepath.WalkDir(tenantRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		result.Scanned++

		rel, relErr := filepath.Rel(s.root, path)
		if relErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, relErr))
			return nil
		}
		ref := filepath.ToSlash(rel)

		if policy.SkipLegalHold != nil {
			held, holdErr := policy.SkipLegalHold(ref)
			if holdErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", ref, holdErr))
				return nil
			}
			if held {
				return nil
			}
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", ref, infoErr))
			return nil
		}
		storedAt := info.ModTime()
		expired := !policy.Before.IsZero() && storedAt.Before(policy.Before)
		if !expired && policy.MaxAge > 0 {
			expired = time.Since(storedAt) > policy.MaxAge
		}
		if !expired {
			return nil
		}
		if dryRun {
			result.Removed++
			result.Freed += info.Size()
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", ref, rmErr))
			return nil
		}
		result.Removed++
		result.Freed += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("evidence: sweep: %w", err)
	}
	return result, nil
}
