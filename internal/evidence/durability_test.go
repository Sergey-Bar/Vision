package evidence_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/vision/internal/evidence"
)

// The plan's P2.7: "A kill -9 test at three points — mid-receive, mid-mux,
// pre-commit. The assertion is that no partial object is ever visible."
//
// This file covers the pre-commit point, which is the one the evidence store owns:
// a clip that has been muxed and is being stored, killed before the rename that
// publishes it.
//
// The test runs a **real child process and kills it hard**. That is not
// ceremony. Every interesting way this fails fails because a `defer` did not
// run, and a fault-injection hook inside the same process would run its cleanups
// on the way out - so the test would pass against a store that leaks on every
// restart, which is the actual bug.
//
// On Windows `cmd.Kill()` is `TerminateProcess`, which is the platform's
// equivalent of SIGKILL: no deferred functions, no flushing, no unwinding.

// killHelperEnv is the switch that turns this test binary into the child.
const killHelperEnv = "EVIDENCE_KILL_HELPER"

// stagingFiles counts the store's staging files, which is how the parent tells
// "the child really was mid-write" from "the child finished and we killed a
// process that had nothing to do".
func stagingFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), ".incoming-") {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestKilledMidStoreLeavesNoVisibleObject is the pre-commit kill.
func TestKilledMidStoreLeavesNoVisibleObject(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	root := filepath.Join(t.TempDir(), "evidence")
	if _, err := evidence.New(evidence.Options{Root: root}); err != nil {
		t.Fatalf("New: %v", err)
	}

	// A body big enough that the write is still running when the kill lands.
	// The child copies one byte at a time with a pause, so the kill is
	// guaranteed to be mid-write rather than after it.
	payload := bytes.Repeat([]byte("A"), 8<<20)
	want := sha256.Sum256(payload)
	ref := "t1/" + hex.EncodeToString(want[:])[:2] + "/" + hex.EncodeToString(want[:])

	// A context rather than a bare command, so the child is killed if the test
	// itself is torn down before the kill below. A leaked child holding a staging
	// file open would make the next run's sweep assertions meaningless.
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	// Re-executing this test binary as the child. The path is os.Args[0], which
	// is the binary the test runner already started - not anything this test reads
	// from configuration, an environment variable, or a file.
	//nolint:gosec // the argument is the running binary, not input
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestKilledMidStore_Helper")
	cmd.Env = append(os.Environ(), killHelperEnv+"=1", "EVIDENCE_KILL_ROOT="+root)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the child: %v", err)
	}

	// Wait until the child is demonstrably mid-write, then kill it.
	deadline := time.Now().Add(20 * time.Second)
	var sawStaging bool
	for time.Now().Before(deadline) {
		if n := len(stagingFiles(t, root)); n > 0 {
			sawStaging = true
			break
		}
		if cmd.ProcessState != nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !sawStaging {
		_ = cmd.Process.Kill()
		t.Fatalf("the child never began writing; it must have died first:\n%s", out.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()

	// 1. The object is not visible. Its ref is the content hash, and a file that
	//    was never renamed into place has no ref, so nothing can address it.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ref))); err == nil {
		t.Error("a partial object is visible at its final ref")
	}
	store, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, _, err := store.Open(ref, hex.EncodeToString(want[:])); err == nil {
		t.Error("a partial object was readable through the store")
	}

	// 2. The residue is on disk, which is the other half of the problem: the
	//    bytes exist and nothing collected them.
	residue := stagingFiles(t, root)
	if len(residue) == 0 {
		t.Error("the killed child left no residue, so this test is not exercising anything")
	}
	report, err := store.StagingResidue()
	if err != nil {
		t.Fatalf("StagingResidue: %v", err)
	}
	// 3. And the sweep will not take it yet, because it is younger than the
	//    residue age and could belong to a live write.
	if report.Removed != 0 {
		t.Errorf("the sweep removed %d file(s) younger than the residue age; a live Put's staging "+
			"file would be taken from under it", report.Removed)
	}

	// 4. Once the residue is old enough, reopening the store collects it. The
	//    age is backdated rather than slept through, because a test that waits a
	//    minute is a test nobody runs.
	if err := backdateStaging(t, root); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	reopened, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("reopen after backdating: %v", err)
	}
	if got := reopened.LastStagingSweep().Removed; got != 1 {
		t.Errorf("reopening collected %d staging files, want 1", got)
	}
	if n := len(stagingFiles(t, root)); n != 0 {
		t.Errorf("%d staging file(s) survived the sweep", n)
	}

	// 5. And the same bytes can now be stored once, cleanly.
	obj, err := reopened.Put("t1", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Put after the crash: %v", err)
	}
	if obj.Ref != ref {
		t.Errorf("the retried write landed at %q, want %q", obj.Ref, ref)
	}
	if body, _, err := reopened.Open(ref, obj.SHA256); err != nil {
		t.Errorf("the recovered object is not readable: %v", err)
	} else {
		_ = body.Close()
	}
}

// TestTheStagingSweepNeverTouchesAFileThatIsNotResidue guards the sweep's
// blast radius. It is bounded to the root and it is age-bounded, and a sweep
// that reached past either bound would delete a tenant's evidence.
func TestTheStagingSweepNeverTouchesAFileThatIsNotResidue(t *testing.T) {
	root := t.TempDir()
	// An object, at a real content-addressed path.
	store, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	obj, err := store.Put("t1", strings.NewReader("real evidence"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	// A directory whose name starts with the staging prefix. Nothing writes one,
	// and the sweep must leave it alone rather than removing a tree.
	trap := filepath.Join(root, ".incoming-a-directory")
	if err := os.MkdirAll(trap, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(trap, "child"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A fresh staging file, the shape a live Put would have right now.
	live := filepath.Join(root, ".incoming-live")
	if err := os.WriteFile(live, []byte("in progress"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// An old staging file: real residue.
	old := filepath.Join(root, ".incoming-old")
	if err := os.WriteFile(old, []byte("residue"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := backdateOne(t, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	reopened, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	report := reopened.LastStagingSweep()
	if report.Removed != 1 {
		t.Errorf("the sweep removed %d files, want 1 (only the old residue)", report.Removed)
	}
	if _, err := os.Stat(trap); err != nil {
		t.Error("the sweep removed a directory; a tree is never residue")
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("the sweep removed a staging file young enough to belong to a live Put")
	}
	if body, _, err := reopened.Open(obj.Ref, obj.SHA256); err != nil {
		t.Errorf("the sweep damaged a stored object: %v", err)
	} else {
		_ = body.Close()
	}
}

// backdateStaging makes every staging file in root older than d.
func backdateStaging(t *testing.T, root string) error {
	t.Helper()
	for _, name := range stagingFiles(t, root) {
		if err := backdateOne(t, filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}

func backdateOne(t *testing.T, path string) error {
	t.Helper()
	when := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, when, when); err != nil {
		return fmt.Errorf("chtimes %s: %w", filepath.Base(path), err)
	}
	return nil
}

// TestKilledMidStore_Helper is the child process. It is a test so it reuses the
// test binary, and it is gated on an environment variable so an ordinary run
// does nothing.
func TestKilledMidStore_Helper(t *testing.T) {
	if os.Getenv(killHelperEnv) != "1" {
		t.Skip("helper process")
	}
	root := os.Getenv("EVIDENCE_KILL_ROOT")
	store, err := evidence.New(evidence.Options{Root: root})
	if err != nil {
		fmt.Fprintln(os.Stderr, "child New:", err)
		os.Exit(2)
	}
	// A reader that yields a byte at a time and waits, so the write is
	// certainly still in progress when the parent kills us. The pause is what
	// makes this a mid-write kill rather than a race.
	reader := io.MultiReader(
		io.LimitReader(slowSource{bytes.NewReader(bytes.Repeat([]byte("A"), 8<<20))}, 8<<20),
	)
	_, err = store.Put("t1", reader)
	if err != nil {
		fmt.Fprintln(os.Stderr, "child Put:", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// slowSource reads in small pieces with a pause, so the parent can kill the
// process at a point where the bytes are genuinely still arriving.
type slowSource struct{ r io.Reader }

func (s slowSource) Read(p []byte) (int, error) {
	if len(p) > 4096 {
		p = p[:4096]
	}
	n, err := s.r.Read(p)
	time.Sleep(50 * time.Millisecond)
	return n, err
}
