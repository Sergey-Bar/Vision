package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dutylog"
)

// The console hand-over page's reader, against a real artifact file: one read
// returns both the brief and the artifact state, the brief comes from the same
// mapping the POC uses, and a site with nothing configured says so by name
// rather than rendering an empty hand-over.

func writeShiftLog(t *testing.T, dir string) string {
	t.Helper()
	w, err := dutylog.Open(dir, "site-1", "2026-10-10-auto")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	fixed := time.Date(2026, 10, 10, 23, 5, 0, 0, time.UTC)
	for _, ev := range []dutylog.Event{
		{TS: fixed, Kind: dutylog.KindTransition, Actor: "autonomy", Subject: "inc-a", Action: "VERIFY/VERIFYING", Reason: "dwell in a restricted zone"},
		{TS: fixed.Add(time.Minute), Kind: dutylog.KindNote, Actor: "operator:sam", Subject: "route-01", Reason: "walked the route, camera smeared"},
		{TS: fixed.Add(2 * time.Minute), Kind: dutylog.KindCoverage, Actor: "autonomy",
			CameraIDs: []string{"cam-02"}, Subject: "restricted", Action: "under-observed",
			Before: "covered", After: "under-observed",
			Reason: "every declared camera of restricted (cam-02) is degraded"},
	} {
		if _, err := w.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return w.Path()
}

func TestDutyLogReaderReadsTheArtifactOnce(t *testing.T) {
	dir := t.TempDir()
	path := writeShiftLog(t, dir)
	fixed := time.Date(2026, 10, 10, 23, 5, 0, 0, time.UTC)

	r := NewDutyLogReader(dir, "site-1", func() time.Time { return fixed })
	view, err := r.Read(t.Context(), "tenant-1", "site-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	b := view.Brief
	if b.Shift != "2026-10-10-auto" {
		t.Errorf("brief shift = %q, want the shift the file names", b.Shift)
	}
	// The shared mapping: a transition is an action, a coverage finding is not.
	if len(b.Actions) != 1 {
		t.Errorf("brief carries %d actions, want the one transition", len(b.Actions))
	}
	if len(b.Notes) != 1 || !strings.Contains(b.Notes[0].Text, "walked the route") {
		t.Errorf("brief notes = %+v, want the patrol note", b.Notes)
	}
	if len(b.Coverage) != 1 || !b.Coverage[0].UnderObserved {
		t.Errorf("brief coverage = %+v, want the under-observed zone the log records", b.Coverage)
	}
	if b.Headline == "" {
		t.Error("the brief has no headline")
	}

	state := view.State
	if state.Path != path || state.Lines != 3 {
		t.Errorf("state = %+v, want the file's own path and 3 lines", state)
	}
	if state.HeadHash == "" {
		t.Error("the state carries no chain head, so the page cannot show it")
	}
	if state.Verification != "ok" {
		t.Errorf("verification = %q, want ok", state.Verification)
	}
	if !strings.Contains(state.Dump, "under-observed") {
		t.Error("the dump does not carry the coverage line")
	}
	if state.DumpNote != "" {
		t.Errorf("a 3-line shift reports truncation %q", state.DumpNote)
	}
}

func TestDutyLogReaderNamesTheAbsence(t *testing.T) {
	// No directory configured at all: the named absence the page renders.
	r := NewDutyLogReader("", "site-1", nil)
	if _, err := r.Read(t.Context(), "t", "s"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("an unconfigured site's error = %v, want it named", err)
	}

	// Configured but nothing written this shift: also named, and not an empty
	// page.
	r = NewDutyLogReader(t.TempDir(), "site-1", nil)
	if _, err := r.Read(t.Context(), "t", "s"); err == nil || !strings.Contains(err.Error(), "no hand-over artifact") {
		t.Errorf("a shift with no artifact's error = %v, want it named", err)
	}
}

// TestDutyLogReaderDumpIsBounded is the page's bound made explicit: a shift's
// dump carries the most recent events, and the bound comes from the one place
// that declares it (dutylog.MaxDumpEvents) rather than a number here.
func TestDutyLogReaderDumpIsBounded(t *testing.T) {
	dir := t.TempDir()
	w, err := dutylog.Open(dir, "site-1", "2026-10-10-auto")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	for i := 0; i < dutylog.MaxDumpEvents+25; i++ {
		if _, err := w.Append(dutylog.Event{
			Kind: dutylog.KindNote, Actor: "operator:sam", Subject: "route-01",
			Reason: "note", TS: time.Date(2026, 10, 10, 23, 0, i, 0, time.UTC),
		}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	view, err := NewDutyLogReader(dir, "site-1", nil).Read(t.Context(), "t", "s")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	state := view.State
	if state.Lines != dutylog.MaxDumpEvents+25 {
		t.Fatalf("the log holds %d lines, want the full count", state.Lines)
	}
	// The dump is capped and the page says so, which is the disclosure the
	// console's own tail was missing.
	if !strings.Contains(state.DumpNote, "showing the most recent") {
		t.Errorf("a truncated dump does not disclose its bound: %q", state.DumpNote)
	}
	if got := strings.Count(state.Dump, "note"); got < dutylog.MaxDumpEvents {
		t.Errorf("the dump renders %d note lines, want at least the %d-line bound", got, dutylog.MaxDumpEvents)
	}
}
