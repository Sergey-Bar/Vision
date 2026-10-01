package soak

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/ai-captain-poc/internal/recorder"
	"github.com/Sergey-Bar/ai-captain-poc/internal/store"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xprotect"
	"github.com/Sergey-Bar/ai-captain-poc/internal/xpsim"
)

// TestTheLocalFaultTableIsClosed: the plan names six. A seventh appears only by
// being written down here first, with an expectation, which is the only way a
// new fault class gets the attention it needs.
func TestTheLocalFaultTableIsClosed(t *testing.T) {
	want := []LocalFault{LocalKillWorker, LocalKillFFmpeg, LocalKillPython, LocalFreezeDB, LocalFillDisk, LocalDisconnectSim}
	if len(LocalFaults) != len(want) {
		t.Fatalf("LocalFaults has %d entries, want %d", len(LocalFaults), len(want))
	}
	for i, f := range want {
		if LocalFaults[i] != f {
			t.Fatalf("LocalFaults[%d] = %q, want %q", i, LocalFaults[i], f)
		}
		if !ValidLocalFault(f) {
			t.Fatalf("%q is in the list but ValidLocalFault says no", f)
		}
	}
	if ValidLocalFault("power_cut") {
		t.Fatal("a fault that is not in the table was accepted")
	}
	exps := LocalExpectations()
	if len(exps) != len(LocalFaults) {
		t.Fatalf("%d expectations for %d faults; every fault needs an outcome", len(exps), len(LocalFaults))
	}
	seen := map[LocalFault]bool{}
	for _, e := range exps {
		if seen[e.Fault] {
			t.Fatalf("%q has two expectations", e.Fault)
		}
		seen[e.Fault] = true
		// Each row must say what the outcome is, what must be recorded, and what
		// must NOT happen. A row that only says what happens is half a spec.
		if e.Outcome == "" || e.MustRecord == "" || e.MustNotHappen == "" {
			t.Fatalf("%q has an incomplete expectation: %+v", e.Fault, e)
		}
	}
}

func TestTheLocalTableRendersAndNeverSaysAllClearWhenSomethingIsNotRun(t *testing.T) {
	md := LocalMarkdown()
	for _, f := range LocalFaults {
		if !strings.Contains(md, string(f)) {
			t.Fatalf("the local table does not mention %q", f)
		}
	}
	if !strings.Contains(md, "must not:") {
		t.Fatal("the table does not print what must not happen, which is the part a reviewer needs")
	}
	rows, proved, notRun := LocalReport()
	if proved+notRun != len(rows) {
		t.Fatal("the summary does not add up to the table")
	}
	// If every row is proved, the summary may not claim anything about a
	// provisional state. If any is NOT RUN, it must say so.
	if notRun > 0 && !strings.Contains(md, "NOT RUN") {
		t.Fatal("a row is NOT RUN and the table does not say the word")
	}
}

// --- db_freeze --------------------------------------------------------------

// TestFrozenDatabaseRefusesTheWriteAndNamesItAsADatabaseFault is T25's
// db_freeze. A locked database must fail by name, and the run must record a
// DATABASE fault rather than blaming the clip: the two have different owners and
// different fixes.
func TestFrozenDatabaseRefusesTheWriteAndNamesItAsADatabaseFault(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.StartRun(ctx, store.Run{ID: "r1", Label: "freeze", Mode: "live", SourceKind: "loop",
		SourceArg: "f", PushBaseURL: "http://127.0.0.1:1", ClockSource: "host",
		StartedAt: time.Now().Add(-time.Minute), TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}

	// A second connection in the same process with an EXCLUSIVE lock, held for
	// the length of the write. This is what a frozen database looks like from
	// the writer's side.
	locker, err := store.OpenExclusive(ctx, s.Path())
	if err != nil {
		t.Skipf("cannot open a second connection: %v", err)
	}
	defer locker.Close()
	release, err := locker.Hold(ctx, 2*time.Second)
	if err != nil {
		t.Skipf("cannot take the lock on this platform: %v", err)
	}
	defer release()

	// The store's own write deadline is what fires, not an external context: a
	// frozen database must be a named fault, not a stall in the frame loop.
	s.SetWriteTimeout(400 * time.Millisecond)
	start := time.Now()
	err = s.Accept(ctx, store.Detection{ID: "d-freeze", RunID: "r1", At: time.Now(), Class: "bird",
		TrackID: 1, Score: 0.9, Direction: "ltr", AIState: "asked_answered", Provenance: store.ProvenanceSynthetic})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a write succeeded while another connection held an exclusive lock: the write was buffered as if it had happened")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the refused write took %s; a frozen database must be reported quickly, not waited out", elapsed)
	}
	if !DBBusyError(err) {
		t.Fatalf("the frozen write was not recognised as a database fault: %v", err)
	}
	// The event is refused, and the refusal is by name.
	if !strings.Contains(err.Error(), "d-freeze") {
		t.Fatalf("the refusal does not name the event it refused: %v", err)
	}
	if !strings.Contains(err.Error(), "REFUSED, not queued and not lost") {
		t.Fatalf("the refusal does not say what happened to the event: %v", err)
	}
	// And after the lock is released, the same write succeeds on a normal
	// deadline: the freeze refused the event and corrupted nothing. The write
	// timeout is restored first, because a 400ms deadline is the condition under
	// test and not the condition the run lives in.
	release()
	release = func() {}
	s.SetWriteTimeout(0)
	if err := s.Accept(ctx, store.Detection{ID: "d-freeze", RunID: "r1", At: time.Now(), Class: "bird",
		TrackID: 1, Score: 0.9, Direction: "ltr", AIState: "asked_answered", Provenance: store.ProvenanceSynthetic}); err != nil {
		t.Fatalf("the write after the freeze was released failed, so the freeze corrupted something: %v", err)
	}
}

// --- disk_fill --------------------------------------------------------------

// TestAFullDiskStopsTheRunAndKeepsTheEvidence is T25's disk_fill, end to end
// through the sweep: the rolling segments go, stored evidence stays, and the run
// is told the space is gone.
func TestAFullDiskStopsTheRunAndKeepsTheEvidence(t *testing.T) {
	root := t.TempDir()
	rolling := filepath.Join(root, "rolling")
	ev := filepath.Join(root, "evidence", "ab")
	if err := os.MkdirAll(rolling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ev, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 30, 10, 0, 0, 0, time.Local)
	for i := 0; i < 6; i++ {
		writeSeg(rolling, i, 2000)
	}
	clip := filepath.Join(ev, "c0ffee.mp4")
	if err := os.WriteFile(clip, make([]byte, 9000), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = base
	d := recorder.Disk{Root: root, RollingDir: rolling, Ceiling: 4000}
	_, err := d.Sweep()
	budget, refused := SweepRefused(err)
	if !refused {
		t.Fatalf("a data root that cannot fit was accepted: %v", err)
	}
	if !strings.Contains(budget.Error(), "stored evidence is never deleted") {
		t.Fatalf("the refusal does not say why it stopped: %s", budget.Error())
	}
	if _, err := os.Stat(clip); err != nil {
		t.Fatal("the stored clip was deleted to satisfy the disk ceiling: that is the one thing the sweep must never do")
	}
}

func writeSeg(dir string, n, size int) {
	name := recorder.SegmentFileName(n)
	_ = os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644)
}

// --- simulator_disconnect ---------------------------------------------------

// TestAProviderThatDisappearsLeavesTheDetectionNonTerminal is T25's
// simulator_disconnect. AC2 is computed from non-terminal detections, so a push
// that is still owed must keep its detection non-terminal: completing it would
// make AC2 pass while a push is outstanding.
func TestAProviderThatDisappearsLeavesTheDetectionNonTerminal(t *testing.T) {
	sim := xpsim.New()
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()

	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.StartRun(ctx, store.Run{ID: "r1", Label: "disconnect", Mode: "live", SourceKind: "loop",
		SourceArg: "f", PushBaseURL: srv.URL, ClockSource: "host", StartedAt: time.Now().Add(-time.Minute),
		TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(ctx, store.Detection{ID: "d1", RunID: "r1", At: time.Now(), Class: "bird",
		TrackID: 1, Score: 0.9, Direction: "ltr", AIState: "asked_answered",
		Provenance: store.ProvenanceSynthetic}); err != nil {
		t.Fatal(err)
	}
	row, err := s.Enqueue(ctx, store.OutboxEntry{DetectionID: "d1", Kind: store.KindEvent,
		Payload: `{"type":"t","source":"cameras/g","time":"2026-09-30T10:15:04Z","datatype":"analytics","tag":"tag-1"}`,
		Tag:     "tag-1"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	// The provider goes away mid-attempt.
	srv.Close()
	if err := s.RecordResult(ctx, row, "", "connection refused after the request was attempted", ""); err != nil {
		t.Fatal(err)
	}

	// The detection is still owed a push, so it is not terminal.
	stalled, err := s.Stalled(ctx, "r1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stalled) != 1 {
		t.Fatalf("stalled = %d, want 1: completing a detection whose push is unresolved would make AC2 pass while a push is outstanding", len(stalled))
	}
	// The attempt has no result, so AC4 counts it.
	stats, err := s.Stats(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if stats.WithResult == stats.Total {
		t.Fatalf("an attempt whose response never arrived was counted as having a result: %+v", stats)
	}
	// And it is still due, so a provider that comes back gets the push.
	due, err := s.Due(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %d, want 1: the row was lost while the provider was gone", len(due))
	}
}

// TestAPushThatCameBackIsNotSentTwice: the restart-after-worker-kill case. A
// pump that comes back must resume the pending rows and must not re-send a row
// whose result was already recorded.
func TestAPushThatCameBackIsNotSentTwice(t *testing.T) {
	sim := xpsim.New()
	srv := httptest.NewServer(sim.Handler())
	defer srv.Close()

	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.StartRun(ctx, store.Run{ID: "r1", Label: "worker", Mode: "live", SourceKind: "loop",
		SourceArg: "f", PushBaseURL: srv.URL, ClockSource: "host", StartedAt: time.Now().Add(-time.Minute),
		TargetSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	for i, tag := range []string{"tag-a", "tag-b"} {
		id := "d" + string(rune('a'+i))
		if err := s.Accept(ctx, store.Detection{ID: id, RunID: "r1", At: time.Now(), Class: "bird",
			TrackID: i, Score: 0.9, Direction: "ltr", AIState: "asked_answered",
			Provenance: store.ProvenanceSynthetic}); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"t","source":"cameras/g","time":"2026-09-30T10:15:04Z","datatype":"analytics","tag":"` + tag + `"}`
		row, err := s.Enqueue(ctx, store.OutboxEntry{DetectionID: id, Kind: store.KindEvent, Payload: body, Tag: tag}, 4)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// The first one is sent before the worker dies.
			if err := s.RecordResult(ctx, row, "push_sent", "202 and the read-back found it", "evt-1"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The pump comes back. Only the row without a result is due.
	due, err := s.Due(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Tag != "tag-b" {
		t.Fatalf("after the worker came back, due = %+v; a row that was already sent must not be sent again", due)
	}
	// And the sent row's detection is not re-pushed by the client's read-back.
	c, err := xprotect.New(xprotect.Config{BaseURL: srv.URL, Timeout: 2 * time.Second,
		Credentials: xprotect.Credentials{ClientID: "c", ClientSecret: "s", Username: "u", Password: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	// A real box, because RC-6 refuses an all-zero one. That refusal is the point:
	// the rehearsal is about the pump and the read-back, and a fixture that could
	// not be pushed would have failed for a reason that has nothing to do with
	// either.
	out, err := c.TriggerEvent(ctx, xprotect.EventInput{
		EventTypeGUID: "t", SourceGUID: "g", At: time.Now(), Tag: "tag-b",
		Class: "motion", Value: "motion", Confidence: 0.5,
		Box:      xprotect.Box{Top: 400, Left: 120, Bottom: 560, Right: 340},
		RuleName: "alarm-reference-pinned",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.State != "push_sent" {
		t.Fatalf("the resumed push did not land: %+v", out)
	}
	held, _ := httpGetTags(srv.URL)
	if strings.Count(held, "tag-b") != 1 {
		t.Fatalf("the tag appears %d times on the server, want once: %s", strings.Count(held, "tag-b"), held)
	}
}

func httpGetTags(base string) (string, error) {
	resp, err := http.Get(base + "/__control/events")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n]), nil
}
