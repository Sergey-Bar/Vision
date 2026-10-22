package evidence_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/evidence"
)

// The plan's fourth validation extra names four traversal forms and then adds
// the one that matters most: "path traversal ×4 on every evidence path,
// **including a Windows symlink**".
//
// The existing symlink test skips on Windows, because `os.Symlink` there needs a
// privilege a test does not assume. That leaves the escape untested on the
// platform a site's evidence volume usually runs on, which is exactly where it
// would be found.
//
// A **directory junction** is the Windows thing that is not privileged: `mklink
// /J` works for any user and behaves like a symlink for every path-resolution
// purpose that matters here. So the Windows half of the control is testable
// after all, and skipping it was a statement about `os.Symlink` rather than about
// the code.

// junction creates a directory junction at link pointing at target.
//
// It returns false when junctions are unavailable, so the caller can skip with
// the reason rather than fail.
//
// The shell is named by COMSPEC and not by PATH, and that is not a detail.
//
// `exec.Command("cmd", ...)` finds `cmd` through PATH, and the verification
// script runs the suite with a PATH it has built for the toolchain. That
// PATH does not always carry `C:\Windows\System32`, and a `cmd` that cannot
// be found is a junction that cannot be created — so the test SKIPPED on the
// platform the junction exists for, and the skip-audit gate reported it as
// an undeclared skip.
//
// The control was therefore not running where it matters, and it was failing
// to run for a reason that had nothing to do with the code under test.
//
// COMSPEC is where Windows records its own command processor, and it is an
// ABSOLUTE path. Naming it directly is the same reasoning as naming an
// absolute path for a tool in a unit test: the tool is part of the platform,
// not part of the environment.
func junction(t *testing.T, link, target string) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		return false
	}
	shell := os.Getenv("COMSPEC")
	if strings.TrimSpace(shell) == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	//nolint:gosec // a fixed Windows tool with arguments this test builds
	cmd := exec.CommandContext(t.Context(), shell, "/c", "mklink", "/J", link, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("mklink /J unavailable: %v\n%s", err, out)
		return false
	}
	return true
}

// TestAWindowsJunctionEscapeIsRefused closes the platform gap the plan names.
func TestAWindowsJunctionEscapeIsRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this is the Windows half; the POSIX half is TestSymlinkEscapeIsRefused")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not evidence"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	link := filepath.Join(s.Root(), "escape")
	if !junction(t, link, outside) {
		t.Skip("directory junctions are unavailable on this host")
	}

	// A junction resolves during path walking, so `filepath.EvalSymlinks` and a
	// plain `os.Stat` both see straight through it. The store's containment
	// check is what has to stop it, and this is the only place that is tested
	// on this platform.
	if _, statErr := os.Stat(filepath.Join(link, "secret.txt")); statErr != nil {
		t.Fatalf("the junction does not resolve, so this test would pass for the wrong reason: %v", statErr)
	}

	for _, ref := range []string{
		"escape/secret.txt",
		"escape",
		filepath.Join("escape", "nested", "secret.txt"),
	} {
		if _, err := s.Exists(ref); !errors.Is(err, evidence.ErrPathRejected) {
			t.Errorf("reference %q escaped through a junction (got %v)", ref, err)
		}
		if _, _, err := s.Open(ref, strings.Repeat("a", 64)); !errors.Is(err, evidence.ErrPathRejected) {
			t.Errorf("Open(%q) escaped through a junction (got %v)", ref, err)
		}
	}

	// And the file outside the root was not read, which is the property the
	// error is standing in for.
	body, err := os.ReadFile(filepath.Join(outside, "secret.txt"))
	if err != nil || string(body) != "not evidence" {
		t.Errorf("the file outside the root was changed: %q %v", body, err)
	}
}

// TestAJunctionCannotBeUsedAsATenantName: a junction inside the root means the
// sweep, which walks by tenant, must not be talked into walking out of it.
//
// The write is refused too, and that is the point: the first version of this
// test expected Put to succeed and the sweep to catch it, which is one step later
// than it needs to be. A junction where a tenant belongs is refused at the first
// thing that touches it.
func TestAJunctionCannotBeUsedAsATenantName(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("this is the Windows half")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "victim"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The junction lives at the path a tenant would occupy.
	if !junction(t, filepath.Join(s.Root(), "t1"), outside) {
		t.Skip("directory junctions are unavailable on this host")
	}
	if _, err := s.Put("t1", strings.NewReader("evidence")); !errors.Is(err, evidence.ErrPathRejected) {
		t.Errorf("Put through a tenant junction was not refused as a path rejection (got %v)", err)
	}
	// And the sweep, which walks by tenant, refuses too.
	if _, err := s.Sweep("t1", evidence.RetentionPolicy{Before: time.Now().Add(time.Hour)}, true); err == nil {
		t.Error("a sweep accepted a tenant whose directory is a junction")
	}
	// And nothing outside the root was touched.
	if _, err := os.Stat(filepath.Join(outside, "victim")); err != nil {
		t.Errorf("the sweep reached outside the root: %v", err)
	}
}
