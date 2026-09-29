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
	"runtime"
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

// ErrHashRequired is returned when Open is asked for an object without the
// expected hash.
//
// An empty hash is a caller error, not a request for an unverified read. Every
// caller of this package reaches the object through a row in `evidence`, and
// that row's `sha256` is `NOT NULL` with a hex CHECK, so an empty string here
// means the caller lost the row rather than that the object has no hash. Open
// used to treat it as "no verification wanted" and return the bytes with an
// empty SHA256 on the Object, which is indistinguishable from a real one to
// everything downstream: the API handler put it in an X-Content-SHA256 header,
// and the AI worker cited it in an observation.
var ErrHashRequired = errors.New("evidence: an expected sha256 is required to open an object")

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
	//
	// A pointer, because a plain bool cannot say "unset": the zero value of a
	// bool is false, and false here is the wrong answer. As a plain field, every
	// caller that wrote Options{Root: dir} — which is every caller — silently
	// got a store that acknowledges evidence it has not fsynced, on a store
	// whose own comment says the fsync is not decoration. Default-on fields need
	// three states, so they take a pointer.
	Fsync *bool
	// VerifyHashOnRead verifies bytes against the row on every read. Default
	// true. Turning it off is a debugging aid and the only reason it exists.
	VerifyHashOnRead *bool
}

// Bool returns a pointer to b, for the optional-boolean fields above.
func Bool(b bool) *bool { return &b }

// DefaultFsync is the value New applies when Fsync is nil.
const DefaultFsync = true

// DefaultVerifyHashOnRead is the value New applies when VerifyHashOnRead is nil.
const DefaultVerifyHashOnRead = true

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
	fsync := DefaultFsync
	if opts.Fsync != nil {
		fsync = *opts.Fsync
	}
	verify := DefaultVerifyHashOnRead
	if opts.VerifyHashOnRead != nil {
		verify = *opts.VerifyHashOnRead
	}
	return &Store{
		root:       abs,
		fsync:      fsync,
		maxBytes:   opts.MaxBytes,
		hashOnRead: verify,
	}, nil
}

// Root returns the resolved evidence root.
func (s *Store) Root() string { return s.root }

// containedIn reports whether path is inside root, or is root itself.
//
// The root was resolved once in New, so this compares real paths rather than
// names that could be repointed afterwards. The separator on the prefix is what
// makes it a containment test rather than a string test: "/var/evidence-other"
// does not start with "/var/evidence" as a path.
func containedIn(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("not under the evidence root: %w", err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolves to %q, which is outside %q", path, root)
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("resolves to the absolute path %q", rel)
	}
	return nil
}

// Fsync reports whether writes are durable before acknowledgement. Exported so
// a caller — or a test — can assert that the defaults it claims to apply were
// the ones that got applied.
func (s *Store) Fsync() bool { return s.fsync }

// VerifyHashOnRead reports whether a read re-hashes the bytes against the row's
// digest before returning them, and is exported for the same reason as Fsync:
// the default is load-bearing, so it has to be observable from outside the
// package rather than asserted only by the code that sets it.
func (s *Store) VerifyHashOnRead() bool { return s.hashOnRead }

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
			return "", fmt.Errorf("%w: cannot inspect %q: %w", ErrPathRejected, ref, err)
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
	// Verified says whether the bytes this Object describes were hashed and
	// matched against the row's digest on the way out. It is false exactly when
	// the store has VerifyHashOnRead turned off, which is a debugging aid; an
	// empty SHA256 alongside Verified true cannot happen, because verification is
	// what fills it in.
	//
	// This field exists because SHA256 alone could not carry the distinction. A
	// store with verification off returned an empty SHA256, and every consumer
	// read that as "a hash that happens to be empty" — the handler published it
	// as an X-Content-SHA256 response header. A caller that wants to know
	// whether it is looking at proven bytes has to ask.
	Verified bool
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
	// The rename itself has to reach stable storage, not just the bytes it
	// moved. tmp.Sync() above made the content durable, but a rename is a
	// directory-entry update, and on ext4 and XFS that entry lives in the
	// parent directory's journal rather than in the file. A power loss after
	// this function returned can therefore leave an acknowledged object absent —
	// which is the exact failure the Fsync option exists to prevent, and the one
	// an object-addressed store is least able to detect, because a later Put of
	// the same bytes simply rewrites the file and nothing ever notices the gap.
	if err := s.syncDir(filepath.Dir(full)); err != nil {
		return Object{}, err
	}
	return Object{Ref: ref, SizeBytes: written, SHA256: sum}, nil
}

// syncDir fsyncs a directory so a rename into it is durable.
//
// On Windows the runtime cannot sync a directory handle at all — os.Open on a
// directory succeeds and Sync on it fails with ERROR_ACCESS_DENIED, because
// there is no directory fsync in the Win32 model. NTFS metadata journaling and
// its write-ahead log are what cover the rename there, and a rename on NTFS is
// made durable by the log record rather than by a call this package could make.
// So the call is skipped on Windows and the gap is named rather than hidden: the
// durability this buys is real on Linux, and is provided by the filesystem on
// Windows.
//
// Everywhere else the error is returned rather than swallowed. A store that
// cannot promise durability should say so at the point where it acknowledged a
// write, not silently downgrade the guarantee the caller asked for.
func (s *Store) syncDir(dir string) error {
	if !s.fsync || runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("evidence: open directory %q to sync: %w", dir, err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("evidence: sync directory %q: %w", dir, err)
	}
	return nil
}

// Open streams an object, verifying its hash against the row.
//
// The hash check is the reason a file that has been altered on disk is reported
// rather than served. An operator looking at evidence that cannot be proven to be
// the evidence an incident cited is worse off than being told the file is wrong.
//
// expectSHA is required. An empty one is refused with ErrHashRequired rather than
// read as "serve it unverified", because every caller reaches an object through
// a row that carries a NOT NULL sha256, and an empty string there means the
// caller lost the row. Serving the bytes anyway produced an Object whose empty
// SHA256 was indistinguishable from a real one: the API handler put it in an
// X-Content-SHA256 header and the AI worker cited it in an observation that
// claims the platform verified those bytes. That is a false claim about evidence,
// which is the one thing this package exists to prevent.
//
// A store built with VerifyHashOnRead(false) does skip the check — that is what
// the option is for — and marks the returned Object Verified: false so the skip
// is visible to a caller rather than only to the code that configured it.
func (s *Store) Open(ref, expectSHA string) (io.ReadCloser, Object, error) {
	full, err := s.resolve(ref)
	if err != nil {
		return nil, Object{}, err
	}
	// Checked after resolve, not before: a rejected reference should say it was
	// rejected, whatever the caller also got wrong. And a missing object should
	// say it is missing rather than complaining about the hash, because "no such
	// evidence" is the more useful of the two facts to whoever is reading.
	if expectSHA == "" {
		return nil, Object{}, fmt.Errorf("%w: %q cannot be opened without the sha256 its "+
			"row records", ErrHashRequired, ref)
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
	if !s.hashOnRead {
		// The documented debugging escape. It returns the bytes and says, in the
		// one field a caller can read, that they were not proven to be the
		// evidence the row names.
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
	obj.Verified = true
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
	// StoredAt reports when an object was stored, from the `evidence` row that
	// names it. A callback rather than a field, for the same reason
	// SkipLegalHold is one: "when was this stored" is a question only the
	// database can answer, and a policy object carrying its own snapshot of the
	// answer is a snapshot that is wrong by the time the walk reaches the
	// object it is wrong about.
	//
	// Returning the zero time means the age is unknown and the object is kept.
	// Returning an error records it in SweepResult.Errors and keeps the object:
	// a sweep that cannot reach the database fails closed, because the failure
	// mode of the other choice is deleting evidence because the query timed out.
	//
	// This is required whenever the policy can expire anything. Sweep has no
	// filesystem fallback, deliberately — see the mtime note on Sweep.
	StoredAt func(ref string) (time.Time, error)
	// SkipLegalHold is a callback rather than a field, because "is this on legal
	// hold" is a question only the database can answer and a policy object with a
	// stale snapshot of it is worse than a query.
	SkipLegalHold func(ref string) (bool, error)
}

// needsStoredAt reports whether the policy can remove anything, and therefore
// needs to be able to answer "when was this stored".
func (p RetentionPolicy) needsStoredAt() bool {
	return !p.Before.IsZero() || p.MaxAge > 0
}

// Sweep removes objects past the policy and reports what it did.
//
// It is an operator action with a dry-run, not a background timer: deleting
// evidence on a schedule nobody is watching is how a site discovers its retention
// policy was wrong after the fact.
//
// Age comes from the database, through RetentionPolicy.StoredAt, and never from
// the filesystem's mtime. Sweep used to read info.ModTime(), which is not when
// anything was stored: a `touch`, a backup restore, a copy, or a filesystem that
// records mtime from the source archive all produce an mtime unrelated to
// evidence.stored_at. An object restored from last week's backup then looks ten
// years old and a retention sweep deletes it on sight, and an object touched by
// an operator looks brand new and survives a policy it has long outlived. The
// retention law is a fact about the row, and the row is where it is read from.
//
// One query per object, deliberately. A batched lookup would mean a pre-pass over
// the whole tree to collect references, which is a second walk of a directory
// that can hold a site's entire incident history, and it would put a memory
// ceiling on a sweep whose current failure mode is a disk. The cost is a query
// per object on an operator action that a human is watching with a dry-run
// available, which is the right place to spend it. Callers that cannot afford it
// should narrow the walk, not have this guess.
func (s *Store) Sweep(tenant string, policy RetentionPolicy, dryRun bool) (SweepResult, error) {
	var result SweepResult

	// The tenant argument is a path segment, and it is treated exactly as Put
	// treats it. Sweep used to join it straight onto the root and walk whatever
	// that produced, so a tenant of "../../etc" walked outside the evidence root
	// and `os.Remove` at the end of the walk deleted it. Put validates and Sweep
	// did not, in the one package whose entire contract is that it is the only
	// code touching that root.
	//
	// Two checks, not one. ValidateRef rejects the shapes a path can take; the
	// containment check rejects anything that survived them, because a check
	// that names every bad shape is a check that will be extended by the next
	// one somebody finds and not remembered here.
	if strings.TrimSpace(tenant) == "" {
		return result, errors.New("evidence: tenant is required and is never read from a request body")
	}
	if err := ValidateRef(tenant); err != nil {
		return result, fmt.Errorf("evidence: tenant is not a usable path segment: %w", err)
	}
	tenantRoot := filepath.Join(s.root, tenant)
	if err := containedIn(s.root, tenantRoot); err != nil {
		return result, fmt.Errorf("evidence: refusing to sweep %q: %w", tenant, err)
	}

	// An expiry policy with no way to read the stored-at time is refused rather
	// than served from the filesystem. Falling back to mtime here would restore
	// the exact bug this replaced, and it would be silent: the caller asked to
	// delete by age, got a sweep that ran, and never learned the age came from
	// the wrong source.
	if policy.needsStoredAt() && policy.StoredAt == nil {
		return result, errors.New("evidence: a sweep that can expire objects needs " +
			"RetentionPolicy.StoredAt; object age is read from the evidence row, not from " +
			"the filesystem mtime, which says when a file was last written or copied")
	}

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

		// Age, from the row. An object with no evidence row, or a lookup that
		// cannot answer, has an unknown age, and an unknown age is not an old
		// age: it is kept, and the reason is recorded rather than swallowed.
		//
		// A nil lookup is reached when the policy cannot expire anything, and is
		// checked here rather than only at the top of Sweep because the walk
		// runs once per object and this is the line that dereferences it.
		if policy.StoredAt == nil {
			return nil
		}
		storedAt, ageErr := policy.StoredAt(ref)
		if ageErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: stored-at: %v", ref, ageErr))
			return nil
		}
		if storedAt.IsZero() {
			// No row, or a row that does not say. Kept, and not recorded as an
			// error: an object with no evidence row is an orphan, not a fault, and
			// reporting one per sweep would bury the errors that matter.
			return nil
		}

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
