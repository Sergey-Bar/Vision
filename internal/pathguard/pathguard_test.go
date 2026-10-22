package pathguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/pathguard"
)

// The guard.
//
// The product writes to TWO roots — the evidence store and the recording
// index — and both take a reference that names a file inside one. The guard is
// the ONE rule they share, and this is the control that proves it refuses
// every form of traversal.

// TestTheGuardRefusesEveryTraversalForm is the table of every shape the
// guard refuses.
//
// The forms are the ones a reference can take and a path resolver can still
// act on: a parent segment, an absolute path, a Windows volume, a UNC
// prefix, a backslash, a NUL byte, an empty segment, and a segment that is
// only dots.
func TestTheGuardRefusesEveryTraversalForm(t *testing.T) {
	refused := []string{
		"",
		"..",
		"../etc/passwd",
		"../../etc/passwd",
		"a/../../b",
		"a/b/../../../c",
		"/etc/passwd",
		"/",
		"a//b",
		"a/",
		`a\b`,
		`..\..\windows`,
		"a\x00b",
		".",
		"...",
		"a/./b",
		`\\host\share\file`,
		`//host/share/file`,
		"C:/windows/system32",
		"C:\\windows\\system32",
	}
	for _, ref := range refused {
		if err := pathguard.ValidateRef(ref); err == nil {
			t.Errorf("the guard accepted %q", ref)
		}
	}
}

// TestTheGuardAcceptsAPlainReference is the other direction, and a guard that
// only refuses is a guard that refuses everything.
func TestTheGuardAcceptsAPlainReference(t *testing.T) {
	accepted := []string{
		"a",
		"a/b",
		"tenant/site/device/20261022T120000.mp4",
		"file.with.dots.mp4",
		"a/b.c/d",
	}
	for _, ref := range accepted {
		if err := pathguard.ValidateRef(ref); err != nil {
			t.Errorf("the guard refused the plain reference %q: %v", ref, err)
		}
	}
}

// TestTheGuardRefusesAReferencePastTheLengthLimit is the bound.
//
// A reference reaches a DATABASE and an export tool as well as the
// filesystem, so it is bounded where the reason is explicit.
func TestTheGuardRefusesAReferencePastTheLengthLimit(t *testing.T) {
	long := strings.Repeat("a", 513)
	if err := pathguard.ValidateRef(long); err == nil {
		t.Errorf("the guard accepted a reference of %d characters", len(long))
	}
	// And one at the limit is fine.
	if err := pathguard.ValidateRef(strings.Repeat("a", 512)); err != nil {
		t.Errorf("the guard refused a reference of exactly 512 characters: %v", err)
	}
}

// TestTheGuardRefusesAPathOutsideTheRoot is the containment half.
//
// A reference that passes the SHAPE check can still resolve outside the root
// — through a symlink, or through a junction on Windows — so the resolved
// path is compared against the resolved root, and the separator on the prefix
// is what makes it a containment test rather than a string test.
func TestTheGuardRefusesAPathOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not evidence"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A sibling directory whose name STARTS with the root's name is not
	// inside it, and a string prefix would say it was.
	sibling := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-other")
	if err := os.MkdirAll(sibling, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer func() { _ = os.RemoveAll(sibling) }()

	outsideFile := filepath.Join(sibling, "secret.txt")
	if err := pathguard.ContainedIn(root, outsideFile); err == nil {
		t.Errorf("the guard accepted %q, which is a sibling of the root and not inside it", outsideFile)
	}
	// And the root itself is inside the root.
	if err := pathguard.ContainedIn(root, root); err != nil {
		t.Errorf("the guard refused the root itself: %v", err)
	}
	// And a file in it is.
	inside := filepath.Join(root, "a", "b.mp4")
	if err := pathguard.ContainedIn(root, inside); err != nil {
		t.Errorf("the guard refused a path inside the root: %v", err)
	}
}
