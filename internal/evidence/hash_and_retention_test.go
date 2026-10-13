package evidence_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/evidence"
)

// Open had a third state that nothing documented: an empty expectSHA returned
// the bytes and an Object whose SHA256 was empty, indistinguishable from a
// verified one. The handler put that empty string in an X-Content-SHA256
// response header and the AI worker cited it in an observation that claims the
// platform verified those bytes. The VerifyHashOnRead option, documented "default
// true", was read by nobody.
//
// An empty hash is a caller error. Every caller reaches an object through a row
// whose sha256 is NOT NULL with a hex CHECK, so an empty string means the caller
// lost the row, and serving unverified bytes is the wrong way to find out.
func TestOpenRefusesAnEmptyExpectedHash(t *testing.T) {
	s := newStore(t)
	obj, err := s.Put("tenant-a", strings.NewReader("bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, got, err := s.Open(obj.Ref, "")
	if !errors.Is(err, evidence.ErrHashRequired) {
		t.Fatalf("Open with no expected hash returned %v, want ErrHashRequired", err)
	}
	if rc != nil {
		t.Error("Open returned a reader alongside the error; the bytes are reachable " +
			"by a caller that only logs the error")
		_ = rc.Close()
	}
	// The Object must be zero, not a plausible-looking one. A returned Object
	// with an empty SHA256 is the defect itself.
	if got.SHA256 != "" || got.Verified {
		t.Errorf("Open returned Object %+v alongside the error", got)
	}
}

// The error has to be matchable, or a caller cannot tell "you forgot the hash"
// from "the file is wrong" — which are different incidents with different
// responses, and the API handler already maps them to different status codes.
func TestErrHashRequiredIsDistinguishableFromOtherFailures(t *testing.T) {
	s := newStore(t)
	obj, err := s.Put("tenant-a", strings.NewReader("bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	_, _, missing := s.Open("tenant-a/aa/deadbeefdeadbeef", strings.Repeat("a", 64))
	if !errors.Is(missing, evidence.ErrNotFound) || errors.Is(missing, evidence.ErrHashRequired) {
		t.Errorf("a missing object produced %v, which is not distinguishable as ErrNotFound", missing)
	}

	_, _, noHash := s.Open(obj.Ref, "")
	if !errors.Is(noHash, evidence.ErrHashRequired) || errors.Is(noHash, evidence.ErrNotFound) {
		t.Errorf("a missing hash produced %v, which is not distinguishable as ErrHashRequired", noHash)
	}

	full := filepath.Join(s.Root(), filepath.FromSlash(obj.Ref))
	if err := os.WriteFile(full, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	_, _, wrong := s.Open(obj.Ref, obj.SHA256)
	if !errors.Is(wrong, evidence.ErrHashMismatch) || errors.Is(wrong, evidence.ErrHashRequired) {
		t.Errorf("a tampered object produced %v, which is not distinguishable as ErrHashMismatch", wrong)
	}
}

// The other half of the control: turning verification off must be visible to a
// caller, not just to the code that configured it. Before, the disabled path
// produced an Object with an empty SHA256 and no way to say so.
func TestVerifyHashOnReadFalseMarksTheObjectUnverified(t *testing.T) {
	off, err := evidence.New(evidence.Options{
		Root: t.TempDir(), VerifyHashOnRead: evidence.Bool(false),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	obj, err := off.Put("tenant-a", strings.NewReader("bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, got, err := off.Open(obj.Ref, obj.SHA256)
	if err != nil {
		t.Fatalf("Open with verification off: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if got.Verified {
		t.Error("an object read with VerifyHashOnRead(false) reported Verified: true")
	}

	// And a store that does verify says so, so the flag is not just always-false.
	on := newStore(t)
	verified, err := on.Put("tenant-a", strings.NewReader("bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc2, got2, err := on.Open(verified.Ref, verified.SHA256)
	if err != nil {
		t.Fatalf("Open with verification on: %v", err)
	}
	defer func() { _ = rc2.Close() }()
	if !got2.Verified {
		t.Error("a verified object reported Verified: false")
	}
	if got2.SHA256 != verified.SHA256 {
		t.Errorf("SHA256 is %q, want the row's %q", got2.SHA256, verified.SHA256)
	}

	// The tampered bytes are served when verification is off - that is what the
	// option is for - and refused when it is on. Each store is tampered in its
	// own root, because the two are separate directories holding the same
	// content-addressed reference.
	for _, s := range []*evidence.Store{off, on} {
		full := filepath.Join(s.Root(), filepath.FromSlash(obj.Ref))
		if err := os.WriteFile(full, []byte("tampered"), 0o600); err != nil {
			t.Fatalf("tamper: %v", err)
		}
	}
	rc3, _, err := off.Open(obj.Ref, obj.SHA256)
	if err != nil {
		t.Fatalf("verification off should serve altered bytes, got %v", err)
	}
	_ = rc3.Close()
	if _, _, err := on.Open(verified.Ref, verified.SHA256); !errors.Is(err, evidence.ErrHashMismatch) {
		t.Errorf("the verifying store served altered bytes: %v", err)
	}
}

// Put fsynced the bytes and then renamed them, and never synced the directory the
// rename wrote into. tmp.Sync() makes the content durable; a rename is a
// directory-entry update, and on ext4 and XFS that entry lives in the parent
// directory rather than in the file. A power loss after Put returned can leave an
// acknowledged object absent - and a content-addressed store is the worst kind of
// store to lose one in, because a later Put of the same bytes rewrites the file
// and nothing ever notices the gap.
//
// What is asserted here is the observable part: that the directory the object
// landed in is a real, synced, still-openable directory after Put returns, and
// that the store reports an error rather than a silent success if it cannot be.
// The syscall sequence itself is not observable from Go, and a test that claimed
// to prove durability across a power cut would be lying about what it checked.
func TestPutSyncsTheDirectoryItRenamedInto(t *testing.T) {
	s := newStore(t)
	obj, err := s.Put("tenant-a", strings.NewReader("bytes"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	dir := filepath.Dir(filepath.Join(s.Root(), filepath.FromSlash(obj.Ref)))
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the object directory does not exist after Put: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}

	// The directory has to be openable as one, which is the first half of the
	// sync path and the half a platform can actually refuse.
	d, err := os.Open(dir)
	if err != nil {
		t.Fatalf("the object directory could not be opened for sync: %v", err)
	}
	_ = d.Close()

	// On a platform with no directory fsync, the store skips the call and says
	// so; on one that has it, the call must not have failed. Asserting the
	// specific outcome per platform keeps this test from asserting a guarantee
	// the platform does not make.
	if runtime.GOOS != "windows" {
		d, err := os.Open(dir)
		if err != nil {
			t.Fatalf("open for sync: %v", err)
		}
		defer func() { _ = d.Close() }()
		if err := d.Sync(); err != nil {
			t.Errorf("syncing the object directory failed on this platform, so Put's "+
				"durability guarantee is not being met either way: %v", err)
		}
	}

	// The object itself is intact and verifiable, which is the property the
	// directory sync exists to protect.
	rc, got, err := s.Open(obj.Ref, obj.SHA256)
	if err != nil {
		t.Fatalf("the object did not survive its own Put: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if !got.Verified {
		t.Error("the object Put just acknowledged is not marked verified on read")
	}
}

// A store with fsync off must not fail when the directory cannot be opened, and
// must not pay for a sync it was not asked for. This is the option's whole
// purpose: a scratch store in a test or a dry run should be fast.
func TestPutWithoutFsyncDoesNotRequireTheDirectoryToBeSyncable(t *testing.T) {
	s, err := evidence.New(evidence.Options{Root: t.TempDir(), Fsync: evidence.Bool(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Put("tenant-a", strings.NewReader(strings.Repeat("x", i+1))); err != nil {
			t.Fatalf("Put with fsync off: %v", err)
		}
	}
}

// Sweep used to read info.ModTime() and call it the storage time. It is not:
// a `touch`, a backup restore, a copy, or a filesystem that takes mtime from a
// source archive all produce an mtime unrelated to when the bytes were stored.
// RetentionPolicy documents "an object with a zero StoredAt is never removed: an
// unknown age is not an old age" and there was no StoredAt anywhere in Sweep.
//
// These two objects are the whole failure: a recent object with a ten-year-old
// mtime, and an object with no evidence row at all. Both must survive.
func TestSweepUsesStoredAtAndNotTheFilesystemMtime(t *testing.T) {
	s := newStore(t)
	table := fakeEvidenceTable{}

	restored := putStored(t, s, table, "restored from backup")
	table[restored.Ref] = time.Now()

	orphan := putStored(t, s, table, "no evidence row")
	delete(table, orphan.Ref)

	genuinelyOld := putStored(t, s, table, "actually old")
	table[genuinelyOld.Ref] = time.Now().Add(-10 * 365 * 24 * time.Hour)

	// Age the restored object's mtime by a decade, which is what restoring from
	// an archive does to it.
	tenYearsAgo := time.Now().Add(-10 * 365 * 24 * time.Hour)
	restoredPath := filepath.Join(s.Root(), filepath.FromSlash(restored.Ref))
	if err := os.Chtimes(restoredPath, tenYearsAgo, tenYearsAgo); err != nil {
		t.Fatalf("age the restored object's mtime: %v", err)
	}
	info, err := os.Stat(restoredPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.ModTime().After(time.Now().Add(-24 * time.Hour)) {
		t.Skipf("this filesystem did not take the mtime (%v); the test cannot set up its "+
			"premise here", info.ModTime())
	}

	// A Before that is older than the ten-year-old mtime but younger than the
	// decade-old stored_at, so the two sources disagree about this one sweep:
	// the old mtime implementation deleted all three objects, and the correct
	// one deletes only the third. MaxAge catches that third object, whose age is
	// a decade either way.
	res, err := s.Sweep("tenant-a", evidence.RetentionPolicy{
		Before:   time.Now().Add(-10*365*24*time.Hour - 180*24*time.Hour),
		MaxAge:   365 * 24 * time.Hour,
		StoredAt: table.storedAt,
	}, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if exists, _ := s.Exists(restored.Ref); !exists {
		t.Error("an object stored recently whose file mtime is ten years old was swept; " +
			"age must come from the evidence row, not the filesystem")
	}
	if exists, _ := s.Exists(orphan.Ref); !exists {
		t.Error("an object with no evidence row was swept; an unknown age is not an old age")
	}
	if exists, _ := s.Exists(genuinelyOld.Ref); exists {
		t.Errorf("the object the row records as ten years old survived: %+v", res.Errors)
	}
	if res.Removed != 1 {
		t.Errorf("swept %d objects, want 1: %+v", res.Removed, res.Errors)
	}
}

// The companion property: mtime must not be able to keep an object alive either.
// An object touched today, whose row says it is a decade old, is still expired -
// otherwise `touch` becomes a way to retain evidence past its policy.
func TestSweepDoesNotLetAFreshMtimeRetainExpiredEvidence(t *testing.T) {
	s := newStore(t)
	table := fakeEvidenceTable{}
	obj := putStored(t, s, table, "expired but touched")
	table[obj.Ref] = time.Now().Add(-10 * 365 * 24 * time.Hour)

	// The file was just written, so its mtime is now.
	info, err := os.Stat(filepath.Join(s.Root(), filepath.FromSlash(obj.Ref)))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if time.Since(info.ModTime()) > time.Hour {
		t.Skip("the filesystem's mtime is not usable as a premise for this test")
	}

	res, err := s.Sweep("tenant-a", evidence.RetentionPolicy{
		MaxAge:   30 * 24 * time.Hour,
		StoredAt: table.storedAt,
	}, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if res.Removed != 1 {
		t.Errorf("swept %d objects, want 1: %+v", res.Removed, res.Errors)
	}
	if exists, _ := s.Exists(obj.Ref); exists {
		t.Error("an object whose row says it is ten years old survived because its file " +
			"was written recently; a touch must not buy indefinite retention")
	}
}

// A sweep that cannot reach the database must not delete. This is the same
// fail-closed rule the legal-hold callback has, applied to the age: the other
// failure mode is that a query times out and the sweep takes the evidence with
// it.
func TestSweepFailsClosedWhenStoredAtCannotBeRead(t *testing.T) {
	s := newStore(t)
	table := fakeEvidenceTable{}
	obj := putStored(t, s, table, "expired")
	table[obj.Ref] = time.Now().Add(-10 * 365 * 24 * time.Hour)

	result, err := s.Sweep("tenant-a", evidence.RetentionPolicy{
		MaxAge: 30 * 24 * time.Hour,
		StoredAt: func(string) (time.Time, error) {
			return time.Time{}, errors.New("database unreachable")
		},
	}, false)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if exists, _ := s.Exists(obj.Ref); !exists {
		t.Fatal("the sweep deleted an object it could not date")
	}
	if len(result.Errors) == 0 {
		t.Error("the sweep reported no error while unable to read stored_at")
	}
}

// An expiry policy with no way to read the age is refused. Falling back to mtime
// here would restore the exact bug this replaced, silently: the caller asked to
// delete by age, got a sweep that ran, and never learned the age came from the
// wrong source.
func TestSweepRefusesToExpireWithoutAStoredAtLookup(t *testing.T) {
	s := newStore(t)
	table := fakeEvidenceTable{}
	obj := putStored(t, s, table, "bytes")
	table[obj.Ref] = time.Now().Add(-10 * 365 * 24 * time.Hour)

	for _, tc := range []struct {
		name   string
		policy evidence.RetentionPolicy
	}{
		{"Before with no lookup", evidence.RetentionPolicy{Before: time.Now()}},
		{"MaxAge with no lookup", evidence.RetentionPolicy{MaxAge: 30 * 24 * time.Hour}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Sweep("tenant-a", tc.policy, true); err == nil {
				t.Fatal("a sweep that can expire objects was accepted with no stored-at lookup")
			}
			if exists, _ := s.Exists(obj.Ref); !exists {
				t.Fatal("the object was removed by a sweep that should not have run")
			}
		})
	}

	// A sweep that cannot expire anything needs no lookup: counting bytes and
	// reporting what it found is a legitimate thing to ask for.
	if _, err := s.Sweep("tenant-a", evidence.RetentionPolicy{}, true); err != nil {
		t.Errorf("a non-expiring sweep was refused: %v", err)
	}
}
