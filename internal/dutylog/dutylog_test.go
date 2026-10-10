package dutylog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestLog(t *testing.T) (*Writer, string) {
	t.Helper()
	dir := t.TempDir()
	w, err := Open(dir, "site-1", "2026-10-10-night")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, dir
}

func decisionEvent(subject string) Event {
	return Event{
		Kind:      KindDecision,
		Actor:     "autonomy",
		TenantID:  "tenant-1",
		SiteID:    "site-1",
		CameraIDs: []string{"cam-01", "cam-02"},
		Subject:   subject,
		Action:    "ESCALATE",
		Reason:    "dwell 42s in restricted zone, corroborated by two cameras",
		Before:    "VERIFYING",
		After:     "ESCALATING",
		TS:        time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC),
	}
}

// TestAppendChainsEveryLine is the core contract: lines link to their
// predecessor, the sequence is unbroken, and the file verifies.
func TestAppendChainsEveryLine(t *testing.T) {
	w, _ := openTestLog(t)

	first, err := w.Append(decisionEvent("inc-aaa"))
	if err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if first.Seq != 1 {
		t.Errorf("first event seq = %d, want 1", first.Seq)
	}
	if first.ParentHash != "" {
		t.Errorf("first event has a parent %q, want none", first.ParentHash)
	}

	second, err := w.Append(Event{
		Kind: KindOverride, Actor: "operator:sam", Subject: "inc-aaa",
		Action: "HOLD", Reason: "known contractor, verified on the floor",
		Before: "ESCALATING", After: "OVERRIDDEN",
	})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if second.ParentHash != first.Hash {
		t.Errorf("second event parent %q, want %q", second.ParentHash, first.Hash)
	}
	if second.Seq != 2 {
		t.Errorf("second event seq = %d, want 2", second.Seq)
	}
	if err := w.Verify(); err != nil {
		t.Errorf("chain does not verify: %v", err)
	}
	if w.LastHash() != second.Hash {
		t.Errorf("writer head %q, want %q", w.LastHash(), second.Hash)
	}
}

// TestEditingALineBreaksVerification is the tamper-evidence contract: the
// ledger the audit claim rests on must fail when a row is changed, the same
// way the product's audit ledger does.
func TestEditingALineBreaksVerification(t *testing.T) {
	w, dir := openTestLog(t)
	if _, err := w.Append(decisionEvent("inc-aaa")); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := w.Append(Event{Kind: KindNote, Actor: "operator:sam", Subject: "cam-01", Reason: "lens smeared"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if err := w.Verify(); err != nil {
		t.Fatalf("chain should verify before tampering: %v", err)
	}

	path := filepath.Join(dir, FileName("site-1", "2026-10-10-night"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2", len(lines))
	}

	// An "innocent" edit: the operator's note gets reworded.
	edited := strings.Replace(lines[1], "lens smeared", "lens fine", 1)
	if edited == lines[1] {
		t.Fatal("test edit did not change the line")
	}
	if err := os.WriteFile(path, []byte(lines[0]+"\n"+edited+"\n"), 0o600); err != nil {
		t.Fatalf("write tampered log: %v", err)
	}
	err = w.Verify()
	if err == nil {
		t.Fatal("verification passed after a line was edited; the chain is decorative")
	}
	if !strings.Contains(err.Error(), "hash") && !strings.Contains(err.Error(), "parent") {
		t.Errorf("error %q does not name the chain failure", err)
	}
}

// TestWriterSurvivesFileLossAndClose is the robustness contract: a hand-over
// process must not die because the file went away or the writer was closed.
// Appending after Close fails by name — never a panic, never a write to a
// closed handle — and a shift whose file is gone starts a fresh chain rather
// than refusing to run.
func TestWriterSurvivesFileLossAndClose(t *testing.T) {
	w, dir := openTestLog(t)
	if _, err := w.Append(decisionEvent("inc-aaa")); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := w.Append(Event{Kind: KindNote, Actor: "operator:sam", Reason: "file vanished mid-shift"}); err == nil {
		t.Error("append after close succeeded; a closed writer must fail by name")
	}
	// The file that was written still verifies: closing never truncates.
	if err := w.Verify(); err != nil {
		t.Errorf("closed shift's file does not verify: %v", err)
	}

	// The file going away between shifts: the next writer starts a fresh
	// chain rather than the process refusing to run.
	path := filepath.Join(dir, FileName("site-1", "2026-10-10-night"))
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove log: %v", err)
	}
	w2, err := Open(dir, "site-1", "2026-10-10-night")
	if err != nil {
		t.Fatalf("reopen after removal: %v", err)
	}
	defer func() { _ = w2.Close() }()
	ev, err := w2.Append(Event{Kind: KindNote, Actor: "operator:sam", Reason: "fresh shift"})
	if err != nil {
		t.Fatalf("append on fresh shift: %v", err)
	}
	if ev.Seq != 1 || ev.ParentHash != "" {
		t.Errorf("fresh shift starts at seq %d with parent %q, want 1 and none", ev.Seq, ev.ParentHash)
	}
	if err := w2.Verify(); err != nil {
		t.Errorf("fresh shift does not verify: %v", err)
	}
}

// TestAppendResumesAnExistingFile checks the second-Writer case: a restart
// continues the chain rather than forking it.
func TestAppendResumesAnExistingFile(t *testing.T) {
	w, dir := openTestLog(t)
	first, err := w.Append(decisionEvent("inc-aaa"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w2, err := Open(dir, "site-1", "2026-10-10-night")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = w2.Close() }()
	second, err := w2.Append(Event{Kind: KindNote, Actor: "operator:sam", Subject: "inc-aaa", Reason: "continued after restart"})
	if err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	if second.Seq != 2 {
		t.Errorf("resumed seq = %d, want 2", second.Seq)
	}
	if second.ParentHash != first.Hash {
		t.Errorf("resumed parent %q, want %q (the chain forked)", second.ParentHash, first.Hash)
	}
	if err := w2.Verify(); err != nil {
		t.Errorf("resumed chain does not verify: %v", err)
	}
}

// TestFsyncHappensBeforeAppendReturns is the durability contract: the duty
// log's whole claim is that a decision that was broadcast survived the
// crash. The sync seam counts the calls, in order.
func TestFsyncHappensBeforeAppendReturns(t *testing.T) {
	w, _ := openTestLog(t)
	var mu sync.Mutex
	syncs := 0
	w.syncFn = func(*os.File) error {
		mu.Lock()
		syncs++
		mu.Unlock()
		return nil
	}
	if _, err := w.Append(decisionEvent("inc-aaa")); err != nil {
		t.Fatalf("append: %v", err)
	}
	mu.Lock()
	got := syncs
	mu.Unlock()
	if got != 1 {
		t.Errorf("fsync calls = %d, want 1", got)
	}
}

// TestActionWithoutReasonIsRefused is the reason-chain contract at the log's
// boundary: a decision with no explanation is the exact defect the artifact
// exists to make impossible, so the log refuses the line rather than
// accepting a decision nobody can defend.
func TestActionWithoutReasonIsRefused(t *testing.T) {
	w, _ := openTestLog(t)
	bad := decisionEvent("inc-aaa")
	bad.Reason = ""
	if _, err := w.Append(bad); err == nil {
		t.Fatal("a decision with no reason was accepted")
	}
	if err := w.Verify(); err != nil {
		t.Errorf("refusing a line left a broken chain: %v", err)
	}
}

// TestReadBackIsCanonical checks the wire format: every line is one canonical
// JSON object whose fields round-trip, so the file is readable without this
// package.
func TestReadBackIsCanonical(t *testing.T) {
	w, dir := openTestLog(t)
	stored, err := w.Append(decisionEvent("inc-aaa"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	path := filepath.Join(dir, FileName("site-1", "2026-10-10-night"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("file has %d lines for one event", len(lines))
	}
	var back Event
	if err := json.Unmarshal([]byte(lines[0]), &back); err != nil {
		t.Fatalf("line is not JSON: %v", err)
	}
	if back.Subject != stored.Subject || back.Action != stored.Action || back.Reason != stored.Reason {
		t.Errorf("round-trip lost data: %+v vs %+v", back, stored)
	}
	if back.Hash != stored.Hash || back.ParentHash != stored.ParentHash {
		t.Error("round-trip lost the chain fields")
	}
	if !strings.HasPrefix(lines[0], `{"seq":1,"ts":"`) {
		t.Errorf("line does not start with the canonical field order: %s", lines[0][:40])
	}
	events, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("ReadAll returned %d events, want 1", len(events))
	}
}

// TestExtractPerSubject is the per-incident view: the log is one file, and
// the audit's "everything about inc-aaa" is a filter over it.
func TestExtractPerSubject(t *testing.T) {
	w, dir := openTestLog(t)
	for _, s := range []string{"inc-aaa", "inc-bbb", "inc-aaa"} {
		ev := decisionEvent(s)
		ev.Reason = "reason for " + s
		if _, err := w.Append(ev); err != nil {
			t.Fatalf("append %s: %v", s, err)
		}
	}
	got, err := Extract(w.Path(), "inc-aaa")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("extracted %d events for inc-aaa, want 2", len(got))
	}
	missing, err := Extract(filepath.Join(dir, "no-such-shift.jsonl"), "nothing")
	if err != nil {
		t.Fatalf("extract on a missing file: %v", err)
	}
	if missing != nil {
		t.Errorf("extract from a nonexistent log returned %v, want nil", missing)
	}
}

// TestOpenRefusesACorruptChain: starting a shift over a damaged file fails by
// name rather than silently appending after the damage.
func TestOpenRefusesACorruptChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName("site-1", "shift"))
	if err := os.WriteFile(path, []byte("this is not json\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Open(dir, "site-1", "shift"); err == nil {
		t.Fatal("Open accepted a file that is not a duty log")
	}
}

// TestFileNameIsSafe is the path-traversal guard on the naming: a site id or
// shift label must not be able to escape the log directory.
func TestFileNameIsSafe(t *testing.T) {
	got := FileName("../../etc/passwd", "..\\..\\win")
	if strings.ContainsAny(got, "/\\") {
		t.Errorf("file name %q contains a path separator", got)
	}
	if !strings.HasPrefix(got, "dutylog-") || !strings.HasSuffix(got, ".jsonl") {
		t.Errorf("file name %q does not have the duty-log shape", got)
	}
}

func TestDumpIsHumanReadable(t *testing.T) {
	out := Dump([]Event{{
		TS: time.Date(2026, 10, 10, 22, 0, 0, 0, time.UTC), Kind: KindDecision,
		Actor: "autonomy", Subject: "inc-aaa", Action: "ESCALATE", Reason: "two cameras, 42s dwell",
		Before: "VERIFYING", After: "ESCALATING", CameraIDs: []string{"cam-01", "cam-02"},
	}})
	for _, want := range []string{"decision", "autonomy", "inc-aaa", "ESCALATE", "VERIFYING → ESCALATING", "cams=cam-01+cam-02", "reason: two cameras, 42s dwell"} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q:\n%s", want, out)
		}
	}
}

// TestShiftFromNameInvertsFileNameForEverySiteID is the round-trip the
// console's reader depends on. Trimming the RAW site id instead of the mapped
// one worked only while the site id contained nothing safename changes, and
// left the site id inside the label — so the reader opened (and, with O_CREATE,
// created) a file that is not the artifact on screen.
func TestShiftFromNameInvertsFileNameForEverySiteID(t *testing.T) {
	for _, site := range []string{"site-1", "Site 1", "a/b", "tenant:core"} {
		for _, shift := range []string{"2026-10-10-night", "2026-10-10"} {
			name := FileName(site, shift)
			got := ShiftFromName(name, site)
			if got != shift {
				t.Errorf("ShiftFromName(%q, %q) = %q, want %q", name, site, got, shift)
			}
		}
	}
	// And the ugly case that motivated the change.
	if got := ShiftFromName(FileName("Site 1", "2026-10-10-night"), "Site 1"); got != "2026-10-10-night" {
		t.Errorf("a site id the mapping changes: got %q, want 2026-10-10-night", got)
	}
}

// TestVerifyChainOverParsedEvents is the same chain rule Writer.Verify
// enforces, over bytes a reader already holds: a reader must not have to open
// the file (with O_CREATE) to check the artifact it just read.
func TestVerifyChainOverParsedEvents(t *testing.T) {
	good := []Event{
		{Seq: 1, Hash: "a"},
		{Seq: 2, ParentHash: "a", Hash: "b"},
		{Seq: 3, ParentHash: "b", Hash: "c"},
	}
	if err := VerifyChain(good); err != nil {
		t.Errorf("a well-formed chain failed: %v", err)
	}
	broken := []Event{{Seq: 1, Hash: "a"}, {Seq: 2, ParentHash: "x", Hash: "b"}}
	if err := VerifyChain(broken); err == nil {
		t.Error("a chain with a broken parent link verified")
	}
	gap := []Event{{Seq: 1, Hash: "a"}, {Seq: 3, ParentHash: "a", Hash: "c"}}
	if err := VerifyChain(gap); err == nil {
		t.Error("a chain with a gap in the sequence verified")
	}
	empty := []Event{{Seq: 1, Hash: "a"}, {Seq: 2, ParentHash: "a"}}
	if err := VerifyChain(empty); err == nil {
		t.Error("a chain with a missing hash verified")
	}
}

// TestAFailedFsyncDoesNotDuplicateTheSequence is the durability contract this
// log's whole claim rests on: a line that reached the file but not the disk is
// still a line in the chain. The first version rewound `seq` on this path, so
// the next append reused the same sequence and parent - two lines with one
// sequence number, a Verify that fails at that line, and an Open that refuses
// the file for ever after, because an append-only log cannot be repaired.
// One fsync failure used to cost the shift its artifact.
func TestAFailedFsyncDoesNotDuplicateTheSequence(t *testing.T) {
	w, _ := openTestLog(t)
	failures := 0
	w.syncFn = func(*os.File) error {
		failures++
		if failures == 1 {
			return context.DeadlineExceeded
		}
		return nil
	}

	if _, err := w.Append(decisionEvent("inc-aaa")); err == nil {
		t.Fatal("the failing fsync was not reported")
	}
	// The next append must continue the chain rather than reusing the
	// sequence and parent the failed line already consumed.
	next, err := w.Append(Event{Kind: KindNote, Actor: "operator:sam", Subject: "inc-aaa", Reason: "after the failure"})
	if err != nil {
		t.Fatalf("append after a failed fsync: %v", err)
	}
	if next.Seq != 2 {
		t.Errorf("the sequence continued as %d, want 2 - the failed line reused its sequence", next.Seq)
	}
	if next.ParentHash == "" {
		t.Error("the next line has no parent; the chain forked")
	}
	if err := w.Verify(); err != nil {
		t.Errorf("the chain does not verify after a failed fsync: %v", err)
	}
	// And the in-memory ring mirrors the file, including the line whose fsync
	// failed: the brief renders from the ring while the artifact view reads the
	// file, so a ring missing a written line makes the two disagree — the first
	// version of this fix advanced the chain but forgot the ring.
	ring := w.Events()
	if len(ring) != 2 {
		t.Fatalf("the ring holds %d events, want both written lines", len(ring))
	}
	if ring[0].Hash == "" || ring[1].ParentHash != ring[0].Hash {
		t.Error("the ring's own chain is broken after a failed fsync")
	}
}
