package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeClip(t *testing.T, dir, name string, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRefRejectsEveryBadName is T16's negative table. Each case names the rule
// it expects, so a check that stops firing is a failing test and not a silent
// relaxation.
func TestRefRejectsEveryBadName(t *testing.T) {
	s := testStore(t)
	cases := []struct {
		name  string
		input string
		rule  string
	}{
		{"empty", "", RuleEmpty},
		{"parent traversal", "..", RuleTraversal},
		{"embedded traversal", ".." + string(filepath.Separator) + "etc", RuleTraversal},
		{"windows separator", `..\..\Windows\System32`, RuleTraversal},
		{"absolute posix", "/etc/passwd", RuleAbsolute},
		{"absolute windows", `C:\Windows\System32\config`, RuleAbsolute},
		{"unc", `\\server\share\x`, RuleAbsolute},
		{"drive relative", `C:x`, RuleAbsolute},
		{"nul byte", "abc\x00def", RuleNUL},
		{"nul in a valid-looking hash", goodHash[:10] + "\x00" + goodHash[11:], RuleNUL},
		{"forward slash", "ab/cd", RuleTraversal},
		{"too short", "abc", RuleNotHash},
		{"too long", goodHash + "a", RuleNotHash},
		{"uppercase", strings.ToUpper(goodHash), RuleNotHash},
		{"not hex", strings.Repeat("z", 64), RuleNotHash},
		{"dot", ".", RuleTraversal},
		{"control character", strings.Repeat("a", 63) + "\x01", RuleWindows},
		{"reserved windows name", "CON", RuleNotHash},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Ref(tc.input)
			if err == nil {
				t.Fatalf("%q was accepted as a store name", tc.input)
			}
			ref, ok := err.(*Refused)
			if !ok {
				t.Fatalf("error is %T, want *Refused: a refusal must name its rule", err)
			}
			if ref.Rule != tc.rule {
				t.Fatalf("rule = %q, want %q (message: %s)", ref.Rule, tc.rule, ref.Reason)
			}
			if ref.Reason == "" {
				t.Fatal("the refusal has no reason")
			}
		})
	}
}

func TestRefAcceptsAHashAndDerivesAPathInsideTheStore(t *testing.T) {
	s := testStore(t)
	p, err := s.Ref(goodHash)
	if err != nil {
		t.Fatal(err)
	}
	if !within(s.Root, p) {
		t.Fatalf("the derived path %s is outside the store %s", p, s.Root)
	}
	if !strings.HasPrefix(p, filepath.Join(s.Root, goodHash[:2], goodHash[2:4])) {
		t.Fatalf("the derived path is not sharded by hash: %s", p)
	}
	if filepath.Base(p) != goodHash+".mp4" {
		t.Fatalf("base name = %s", filepath.Base(p))
	}
}

func TestRefIsStableForTheSameHash(t *testing.T) {
	s := testStore(t)
	a, _ := s.Ref(goodHash)
	b, _ := s.Ref(goodHash)
	if a != b {
		t.Fatalf("the same hash produced two paths: %s and %s", a, b)
	}
}

func TestStorageRefIsRelativeAndPortable(t *testing.T) {
	s := testStore(t)
	ref := s.StorageRef(goodHash)
	if filepath.IsAbs(ref) {
		t.Fatalf("the storage ref %q is absolute; a data root that moves would invalidate every recorded reference", ref)
	}
	if strings.Contains(ref, `\`) {
		t.Fatalf("the storage ref %q is not slash-separated, so it is not portable", ref)
	}
}

func TestPutStoresAndReadsBackIdenticalBytes(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	content := strings.Repeat("clip-bytes-", 1000)
	src := writeClip(t, dir, "in.mp4", content)

	got, err := s.Put(src)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != HashBytes([]byte(content)) {
		t.Fatalf("hash = %s, want the content hash", got.Hash)
	}
	if got.Bytes != int64(len(content)) {
		t.Fatalf("bytes = %d, want %d", got.Bytes, len(content))
	}
	back, size, err := s.Get(got.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if back != got.Hash {
		t.Fatalf("the stored bytes read back as %s, want %s", back, got.Hash)
	}
	if size != got.Bytes {
		t.Fatalf("read back %d bytes, want %d", size, got.Bytes)
	}
}

func TestPutDeduplicatesIdenticalContent(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	a := writeClip(t, dir, "a.mp4", "identical")
	b := writeClip(t, dir, "b.mp4", "identical")
	first, err := s.Put(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Put(b)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Deduplicated {
		t.Fatal("identical content was stored twice: the store is not content-addressed in practice")
	}
	if first.Path != second.Path {
		t.Fatalf("the same content produced two paths: %s and %s", first.Path, second.Path)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the store holds %d objects for one piece of content", len(entries))
	}
}

func TestPutRefusesAZeroLengthClip(t *testing.T) {
	s := testStore(t)
	src := writeClip(t, t.TempDir(), "empty.mp4", "")
	_, err := s.Put(src)
	if err == nil || !strings.Contains(err.Error(), "zero bytes") {
		t.Fatalf("a zero-byte clip was stored: %v", err)
	}
}

func TestPutRefusesAMissingSource(t *testing.T) {
	s := testStore(t)
	_, err := s.Put(filepath.Join(t.TempDir(), "nope.mp4"))
	if err == nil {
		t.Fatal("a missing clip was stored")
	}
}

func TestEntriesIgnoresFilesTheStoreDoesNotOwn(t *testing.T) {
	// A file whose name is not a hash did not come from Put. The sweep must not
	// touch it, because the store does not own it.
	s := testStore(t)
	dir := t.TempDir()
	a := writeClip(t, dir, "in.mp4", "content")
	if _, err := s.Put(a); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want only the stored object", len(entries))
	}
}

func TestSweepRemovesOnlyWhatIsOlderThanTheCutoff(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	old := writeClip(t, dir, "old.mp4", "old-content")
	fresh := writeClip(t, dir, "fresh.mp4", "fresh-content")
	oldStored, err := s.Put(old)
	if err != nil {
		t.Fatal(err)
	}
	freshStored, err := s.Put(fresh)
	if err != nil {
		t.Fatal(err)
	}
	// Age one object.
	oldPath, _ := s.Ref(oldStored.Hash)
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatal(err)
	}

	res, err := s.Sweep(time.Now().Add(-24*time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != oldPath {
		t.Fatalf("swept %v, want only %s", res.Removed, oldPath)
	}
	if _, err := os.Stat(freshPath(t, s, freshStored.Hash)); err != nil {
		t.Fatal("the sweep deleted a fresh clip: retention is the primary rule, not a suggestion")
	}
}

func TestSweepNeverDeletesAFreshClipToSatisfyAByteTarget(t *testing.T) {
	// The byte target is secondary. It may only remove things already past the
	// cutoff, so it can never trade a fresh incident for disk space.
	s := testStore(t)
	dir := t.TempDir()
	fresh := writeClip(t, dir, "fresh.mp4", strings.Repeat("x", 10000))
	stored, err := s.Put(fresh)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Sweep(time.Now().Add(-24*time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("a byte target deleted %d fresh objects", len(res.Removed))
	}
	freshPath(t, s, stored.Hash)
	if _, err := os.Stat(freshPath(t, s, stored.Hash)); err != nil {
		t.Fatal("the fresh clip is gone")
	}
}

// TestSweepStopsAtTheByteTargetAmongExpiredObjects: the target is met by
// whole objects, so it can overshoot by at most one object. Here every object is
// 1000 bytes and the target is 1500, so exactly two go.
func TestSweepStopsAtTheByteTargetAmongExpiredObjects(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 4; i++ {
		src := writeClip(t, dir, string(rune('a'+i))+".mp4", strings.Repeat(string(rune('a'+i)), 1000))
		st, err := s.Put(src)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := s.Ref(st.Hash)
		past := time.Now().Add(-time.Duration(10-i) * time.Hour)
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	// Cutoff 5 hours ago: all four qualify. Target 1500 bytes: two of them.
	res, err := s.Sweep(time.Now().Add(-5*time.Hour), 1500)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 2 || res.Removed[0] != paths[0] {
		t.Fatalf("swept %v, want the two oldest of %v", res.Removed, paths)
	}
	if res.BytesFreed != 2000 {
		t.Fatalf("freed = %d, want 2000", res.BytesFreed)
	}
}

func TestSweepOfAnEmptyStoreIsNotAnError(t *testing.T) {
	s := testStore(t)
	res, err := s.Sweep(time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("an empty store swept %v", res.Removed)
	}
	if res.Reason == "" {
		t.Fatal("a sweep with no stated reason: the report prints it")
	}
}

func freshPath(t *testing.T, s *Store, sum string) string {
	t.Helper()
	p, err := s.Ref(sum)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestALinkInTheStoreIsNotFollowed: a junction or symlink inside the store
// pointing outside it must never be read or written through. On Windows the
// junction case is the one that matters, and it is the one an operator creates
// by accident when they put the data root on a share.
func TestALinkInTheStoreIsNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not evidence"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.Root, goodHash[:2])
	if err := os.Symlink(outside, link); err != nil {
		// Windows without developer mode refuses symlinks. Skip rather than
		// claim the case is covered when it is not.
		t.Skipf("this machine refuses to create a symlink: %v", err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !within(s.Root, e.Path) {
			t.Fatalf("the store listed %s, which is outside it", e.Path)
		}
	}
	if _, _, err := s.Get(goodHash); err == nil {
		t.Fatal("a read through a link out of the store succeeded")
	}
}
