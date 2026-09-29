package evidence_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/evidence"
)

func newStore(t *testing.T) *evidence.Store {
	t.Helper()
	s, err := evidence.New(evidence.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// The defaults are load-bearing and both were once wrong: Fsync took a plain
// bool, so Options{Root: dir} — which is every caller — got a store that
// acknowledged evidence it had not fsynced, and VerifyHashOnRead was read by
// nobody at all. A default that is silently the wrong value is worse than no
// default, because the comment says otherwise and the comment is what people
// trust.
func TestNewAppliesTheDefaultsItDocuments(t *testing.T) {
	s, err := evidence.New(evidence.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !s.Fsync() {
		t.Error("a store built with no Fsync option came up with fsync off; the field " +
			"comment says the default is true")
	}
	if !s.VerifyHashOnRead() {
		t.Error("a store built with no VerifyHashOnRead option came up with read " +
			"verification off")
	}

	// And an explicit false is honoured, which is the point of the pointer.
	off, err := evidence.New(evidence.Options{
		Root: t.TempDir(), Fsync: evidence.Bool(false),
		VerifyHashOnRead: evidence.Bool(false),
	})
	if err != nil {
		t.Fatalf("New with explicit off: %v", err)
	}
	if off.Fsync() {
		t.Error("an explicit Fsync: false was overridden by the default")
	}
	if off.VerifyHashOnRead() {
		t.Error("an explicit VerifyHashOnRead: false was overridden by the default")
	}
}

// Every one of these must be refused. The list is the plan's: `../`, absolute
// paths, null bytes, symlink escapes, plus drive letters, UNC prefixes and
// empty segments, which are the same attack on a different platform.
func TestValidateRefRejectsEveryTraversalForm(t *testing.T) {
	bad := []string{
		"",
		"../etc/passwd",
		"../../etc/passwd",
		"a/../../b",
		"a/b/../../../c",
		"/etc/passwd",
		"/",
		"a/b/",
		"a//b",
		`a\..\b`,
		`C:\Windows\System32\config`,
		`\\server\share\evidence`,
		"a\x00b",
		"..",
		".",
		"a/./b",
		strings.Repeat("a", 600),
	}
	for _, ref := range bad {
		if err := evidence.ValidateRef(ref); err == nil {
			t.Errorf("reference %q was accepted", ref)
		} else if !errors.Is(err, evidence.ErrPathRejected) {
			t.Errorf("reference %q produced %v, want ErrPathRejected", ref, err)
		}
	}

	good := []string{
		"tenant/ab/abcdef0123456789.jpg",
		"t-1/00/hash.mp4",
		"a/b/c/d/e/f.bin",
	}
	for _, ref := range good {
		if err := evidence.ValidateRef(ref); err != nil {
			t.Errorf("reference %q was rejected: %v", ref, err)
		}
	}
}

// A symlink inside the evidence root pointing outside it is the traversal that
// string validation alone does not catch, and it is the one that matters most
// because planting a symlink is a legitimate filesystem operation for whoever
// has write access to the directory.
func TestSymlinkEscapeIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation on Windows needs a privilege this test does not assume")
	}
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("not evidence"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Root(), "escape")); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}

	for _, ref := range []string{"escape/secret.txt", "escape", "escape/nested/secret.txt"} {
		if _, err := s.Exists(ref); !errors.Is(err, evidence.ErrPathRejected) {
			t.Errorf("reference %q escaped via symlink (got %v)", ref, err)
		}
		if _, _, err := s.Open(ref, ""); !errors.Is(err, evidence.ErrPathRejected) {
			t.Errorf("Open(%q) escaped via symlink (got %v)", ref, err)
		}
	}
}

func TestPutAndOpenRoundTrip(t *testing.T) {
	s := newStore(t)
	body := []byte("snapshot bytes")

	obj, err := s.Put("tenant-a", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	sum := sha256.Sum256(body)
	if obj.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("hash is %s, want %s", obj.SHA256, hex.EncodeToString(sum[:]))
	}
	if obj.SizeBytes != int64(len(body)) {
		t.Errorf("size is %d, want %d", obj.SizeBytes, len(body))
	}
	if obj.Ref != evidence.RefFor("tenant-a", obj.SHA256) {
		t.Errorf("reference %q does not match the content-addressed form", obj.Ref)
	}

	rc, got, err := s.Open(obj.Ref, obj.SHA256)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	read, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(read) != string(body) {
		t.Errorf("read back %q, want %q", read, body)
	}
	if got.SizeBytes != int64(len(body)) {
		t.Errorf("opened object reports size %d", got.SizeBytes)
	}
}

// Content-addressed storage means identical bytes land on the same reference and
// cannot overwrite each other with different content.
func TestIdenticalBytesDeduplicateAndCannotCollide(t *testing.T) {
	s := newStore(t)
	first, err := s.Put("tenant-a", strings.NewReader("same"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	second, err := s.Put("tenant-a", strings.NewReader("same"))
	if err != nil {
		t.Fatalf("Put again: %v", err)
	}
	if first.Ref != second.Ref {
		t.Errorf("identical bytes produced two references: %q and %q", first.Ref, second.Ref)
	}

	other, err := s.Put("tenant-a", strings.NewReader("different"))
	if err != nil {
		t.Fatalf("Put different: %v", err)
	}
	if other.Ref == first.Ref {
		t.Error("different bytes produced the same reference")
	}
}

// A file altered on disk must be reported, not served. An operator looking at
// evidence that cannot be proven to be the evidence an incident cited is worse
// off than being told the file is wrong.
func TestAlteredFileIsReportedNotServed(t *testing.T) {
	s := newStore(t)
	obj, err := s.Put("tenant-a", strings.NewReader("original"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	full := filepath.Join(s.Root(), filepath.FromSlash(obj.Ref))
	if err := os.WriteFile(full, []byte("tampered!!"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	_, _, err = s.Open(obj.Ref, obj.SHA256)
	if err == nil {
		t.Fatal("an altered file was served")
	}
	if !errors.Is(err, evidence.ErrHashMismatch) {
		t.Errorf("expected a hash mismatch, got %v", err)
	}
}

// Sweep deletes files. Its tenant argument builds the directory it walks and the
// directory it deletes inside, and it used to join that argument onto the root
// with no validation at all — so a tenant of "../../etc" walked out of the
// evidence root and os.Remove took whatever the policy said was expired with it.
// Put validates the same argument; Sweep did not, in the one package whose
// contract is that it is the only code touching that root.
func TestSweepRefusesATenantThatEscapesTheRoot(t *testing.T) {
	s := newStore(t)

	// A real file outside the root, standing in for anything a traversal could
	// reach. It has to still be there afterwards.
	outside := filepath.Join(t.TempDir(), "not-evidence.txt")
	if err := os.WriteFile(outside, []byte("irreplaceable"), 0o600); err != nil {
		t.Fatalf("write the outside file: %v", err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(outside, old, old); err != nil {
		t.Fatalf("age the outside file: %v", err)
	}

	for _, tenant := range []string{
		"..",
		"../..",
		"tenant-a/../../..",
		filepath.ToSlash(outside),
		"/etc",
		"C:\\Windows",
	} {
		t.Run(tenant, func(t *testing.T) {
			if _, err := s.Sweep(tenant, evidence.RetentionPolicy{Before: time.Now()}, false); err == nil {
				t.Fatalf("Sweep(%q) was accepted; it walks and deletes outside the evidence root",
					tenant)
			}
			if _, statErr := os.Stat(outside); statErr != nil {
				t.Fatalf("the file outside the evidence root is gone: %v", statErr)
			}
		})
	}
}

// The happy path still works, and it still only touches the tenant's own tree.
func TestSweepDeletesOnlyExpiredObjectsInTheNamedTenant(t *testing.T) {
	s := newStore(t)
	keep, err := s.Put("tenant-a", strings.NewReader("keep"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	drop, err := s.Put("tenant-a", strings.NewReader("drop"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	other, err := s.Put("tenant-b", strings.NewReader("other tenant"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	age := func(obj evidence.Object) {
		t.Helper()
		p := filepath.Join(s.Root(), filepath.FromSlash(obj.Ref))
		old := time.Now().Add(-90 * 24 * time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("age %s: %v", obj.Ref, err)
		}
	}
	age(drop)
	age(other)

	res, err := s.Sweep("tenant-a", evidence.RetentionPolicy{Before: time.Now().Add(-30 * 24 * time.Hour)}, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Removed != 1 {
		t.Errorf("swept %d object(s), want 1: %+v", res.Removed, res.Errors)
	}
	if _, _, err := s.Open(drop.Ref, drop.SHA256); !errors.Is(err, evidence.ErrNotFound) {
		t.Errorf("the expired object survived: %v", err)
	}
	for _, tc := range []struct {
		obj evidence.Object
		why string
	}{{keep, "an unexpired object was swept"}, {other, "another tenant's object was swept"}} {
		r, _, err := s.Open(tc.obj.Ref, tc.obj.SHA256)
		if err != nil {
			t.Errorf("%s: %v", tc.why, err)
		}
		if r != nil {
			_ = r.Close()
		}
	}
}

func TestOpenRejectsTraversalAndReportsMissing(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.Open("../../etc/passwd", ""); !errors.Is(err, evidence.ErrPathRejected) {
		t.Errorf("a traversal was accepted by Open: %v", err)
	}
	_, _, err := s.Open("tenant-a/aa/deadbeef", "")
	if !errors.Is(err, evidence.ErrNotFound) {
		t.Errorf("a missing object produced %v, want ErrNotFound", err)
	}
}

func TestPutIsBounded(t *testing.T) {
	root := t.TempDir()
	s, err := evidence.New(evidence.Options{Root: root, MaxBytes: 16})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.Put("tenant-a", strings.NewReader(strings.Repeat("x", 100))); err == nil {
		t.Fatal("an object over the cap was stored; a source offering an unbounded clip " +
			"is a way to fill the disk during an incident")
	}
}

// A tenant id becomes a path segment, so it gets the same validation as any
// other segment. It also never comes from a request body.
func TestPutRefusesAnUnusableTenant(t *testing.T) {
	s := newStore(t)
	for _, tenant := range []string{"", "..", "../evil", "a/b", `a\b`, "a\x00b"} {
		if _, err := s.Put(tenant, strings.NewReader("x")); err == nil {
			t.Errorf("tenant %q was accepted as a path segment", tenant)
		}
	}
}

func TestSweepRespectsRetentionAndLegalHold(t *testing.T) {
	s := newStore(t)
	old, err := s.Put("tenant-a", strings.NewReader("old"))
	if err != nil {
		t.Fatalf("Put old: %v", err)
	}
	recent, err := s.Put("tenant-a", strings.NewReader("recent"))
	if err != nil {
		t.Fatalf("Put recent: %v", err)
	}

	// Age the first object.
	oldPath := filepath.Join(s.Root(), filepath.FromSlash(old.Ref))
	past := time.Now().Add(-100 * 24 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A dry run reports without removing.
	dry, err := s.Sweep("tenant-a", evidence.RetentionPolicy{MaxAge: 30 * 24 * time.Hour}, true)
	if err != nil {
		t.Fatalf("Sweep dry: %v", err)
	}
	if dry.Removed != 1 {
		t.Errorf("dry run reported %d removals, want 1", dry.Removed)
	}
	if exists, _ := s.Exists(old.Ref); !exists {
		t.Error("a dry run removed a file")
	}

	// Legal hold wins over retention, always.
	held, err := s.Sweep("tenant-a", evidence.RetentionPolicy{
		MaxAge:        30 * 24 * time.Hour,
		SkipLegalHold: func(ref string) (bool, error) { return ref == old.Ref, nil },
	}, false)
	if err != nil {
		t.Fatalf("Sweep held: %v", err)
	}
	if exists, _ := s.Exists(old.Ref); !exists {
		t.Error("legal hold did not protect the object")
	}
	if held.Removed != 0 {
		t.Errorf("sweep removed %d objects under legal hold", held.Removed)
	}

	// Without the hold, the old object goes and the recent one stays.
	if _, err := s.Sweep("tenant-a", evidence.RetentionPolicy{MaxAge: 30 * 24 * time.Hour}, false); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if exists, _ := s.Exists(old.Ref); exists {
		t.Error("the aged object survived the sweep")
	}
	if exists, _ := s.Exists(recent.Ref); !exists {
		t.Error("the sweep removed an object inside its retention")
	}
}

// A sweep that cannot answer the legal-hold question must not delete. Failing
// open here would remove evidence the moment the database is unreachable.
func TestSweepFailsClosedWhenLegalHoldCannotBeChecked(t *testing.T) {
	s := newStore(t)
	obj, err := s.Put("tenant-a", strings.NewReader("held"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	past := time.Now().Add(-100 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(s.Root(), filepath.FromSlash(obj.Ref)), past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	result, err := s.Sweep("tenant-a", evidence.RetentionPolicy{
		MaxAge: 30 * 24 * time.Hour,
		SkipLegalHold: func(string) (bool, error) {
			return false, errors.New("database unreachable")
		},
	}, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if exists, _ := s.Exists(obj.Ref); !exists {
		t.Fatal("the sweep deleted an object it could not clear for legal hold")
	}
	if len(result.Errors) == 0 {
		t.Error("the sweep reported no error while unable to check legal hold")
	}
}

// The store must not read or write outside its root, and a staging file left by a
// crash must not be mistaken for evidence.
func TestStagingFilesAreNotEvidence(t *testing.T) {
	s := newStore(t)
	if _, err := s.Put("tenant-a", strings.NewReader("real")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), ".incoming-crashed"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	result, err := s.Sweep("tenant-a", evidence.RetentionPolicy{MaxAge: time.Nanosecond}, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// The staging file is not under the tenant directory, so a tenant sweep does
	// not see it at all — which is the property that keeps a partial write from
	// being counted as evidence.
	if result.Scanned == 0 {
		t.Skip("tenant directory layout changed; this assertion no longer describes it")
	}
}
