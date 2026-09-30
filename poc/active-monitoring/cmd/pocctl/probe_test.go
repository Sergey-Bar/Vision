package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheConfidenceTableIsAFixedStringAndNothingComputesIt is T22's mechanism:
// the tool must be unable to promote a path on its own. A row's confidence is
// a constant, so there is no code path — including a future one — that can turn
// a locally-tested path into a "verified" one.
func TestTheConfidenceTableIsAFixedStringAndNothingComputesIt(t *testing.T) {
	got := probeConfidence()
	if got != "documented (spec) / untested-against-real-vms" {
		t.Fatalf("the confidence string is %q; it must be the one fixed value", got)
	}
	for _, forbidden := range []string{"verified", "VERIFIED", "confirmed", "validated", "working", "tested-ok"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("the confidence string contains %q: a local test is not a VMS", forbidden)
		}
	}
}

// TestTheProbeOutputNeverClaimsAVMSLanding: the table's own text, and the
// rendered output, must both say what the confidence string means.
func TestTheProbeOutputNeverClaimsAVMSLanding(t *testing.T) {
	out := captureProbe(t)
	lower := strings.ToLower(out)
	if !strings.Contains(lower, "untested-against-real-vms") {
		t.Fatal("the probe output does not carry the untested-against-a-real-vms clause")
	}
	if !strings.Contains(lower, "no xprotect server has") && !strings.Contains(lower, "reachable") {
		t.Fatal("the probe output does not say that no real server has answered")
	}
	// A column headed "verified" would be the promotion this test forbids.
	for _, forbidden := range []string{">verified<", "| verified |", ">vms verified<", "reached milestone"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("the probe output contains %q", forbidden)
		}
	}
}

func captureProbe(t *testing.T) string {
	t.Helper()
	old := os.Args
	os.Args = []string{"pocctl", "probe"}
	defer func() { os.Args = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 8192)
		for {
			n, err := r.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()
	if err := cmdProbe(nil); err != nil {
		t.Fatal(err)
	}
	w.Close()
	os.Stdout = orig
	return <-done
}

// TestTheXProtectClientNeverMentionsTheSimulator is the boundary the table's
// honesty rests on: the client cannot know that xpsim exists, so it cannot
// report a simulator landing as if it were a VMS.
func TestTheXProtectClientNeverMentionsTheSimulator(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "xprotect")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "xpsim") {
			t.Fatalf("%s mentions xpsim; the client must not know the simulator exists, or a simulator landing could be reported as a VMS one", e.Name())
		}
	}
}
