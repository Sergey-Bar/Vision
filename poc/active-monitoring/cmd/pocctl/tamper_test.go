package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// seedLedger builds a store with a few appended ledger rows, which is the minimum a
// tamper demonstration can be run against.
func seedLedger(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := context.Background()
	const run = "run-tamper-1"
	if err := st.StartRun(c, store.Run{
		ID: run, Label: "tamper", Mode: "live", SourceKind: "loop", SourceArg: "f.mp4",
		PushBaseURL: "http://127.0.0.1:8098", ClockSource: "host",
		StartedAt: at(10, 0), TargetSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}
	putDetection(t, st, run, "d1", at(10, 5))
	for _, e := range []store.LedgerEntry{
		{At: at(10, 0), Kind: store.LedgerRunStarted, Subject: run, Payload: "label=tamper"},
		{At: at(10, 5), Kind: store.LedgerDetectionCommitted, Subject: "d1", Payload: "class=bird;score=0.700000"},
		{At: at(10, 6), Kind: store.LedgerClipStored, Subject: "d1", Payload: "sha256=" + shaFor("d1")},
	} {
		if _, err := st.AppendLedger(c, e); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// guardsAreBack reports whether the append-only triggers refuse an edit. It is the
// check every test below makes, because leaving the guards out is the one failure
// mode of this command that is not self-announcing.
//
// The probe is a bare UPDATE and deliberately NOT wrapped in `withGuardsRemoved`:
// that helper drops the guards, so using it to ask whether the guards are present
// answers the question by removing the thing being asked about.
func guardsAreBack(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(), `UPDATE ledger SET payload='x' WHERE seq=1`); err == nil {
		t.Fatal(errGuardWasNotBack)
	}
}

// TestTheTamperDemonstrationBreaksTheChainAndPutsItBack is the demo's whole
// contract, in the order the demo makes it.
func TestTheTamperDemonstrationBreaksTheChainAndPutsItBack(t *testing.T) {
	st := seedLedger(t)
	out := captureStdout(t, func() {
		if err := tamperLedger(st, 0); err != nil {
			t.Fatalf("tamperLedger: %v", err)
		}
	})
	for _, want := range []string{"BEFORE   PASS", "DURING   FAIL", "AFTER    PASS"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the demonstration did not print %q:\n%s", want, out)
		}
	}
	rep, err := st.VerifyLedger(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Valid {
		t.Fatalf("the demonstration left the chain broken: %s", rep.Reason)
	}
	// And the guards are back, which is the part nobody asks about and everybody
	// should: a demonstration that removed the append-only protection permanently
	// would trade a permanent missing control for a good story.
	guardsAreBack(t, st)
}

// TestTheTamperDemonstrationChangesExactlyOneByte keeps the demonstration honest
// about its own scope. A rewrite of a whole field would prove the hash covers the
// field and say nothing about the byte, which is where a length-prefixed encoding
// can be wrong without any field-level test noticing.
func TestTheTamperDemonstrationChangesExactlyOneByte(t *testing.T) {
	row := store.LedgerRow{
		Seq: 2, Kind: store.LedgerDetectionCommitted, Subject: "d1",
		Payload: "class=bird;score=0.700000", Hash: strings.Repeat("a", 64),
	}
	original, mutated, field, err := tamperOneByte(row)
	if err != nil {
		t.Fatal(err)
	}
	if field != "payload" {
		t.Fatalf("tampered %q, want the payload: the payload is what a reader looks at", field)
	}
	if diff := byteDiff(original, mutated); diff != 1 || len(original) != len(mutated) {
		t.Fatalf("%d byte(s) changed and %d -> %d bytes long, want 1 at constant length (%q -> %q)",
			diff, len(original), len(mutated), original, mutated)
	}
}

// TestTheTamperDemonstrationFallsBackWhenThereIsNoPayload covers the row shape that
// has no payload at all. Editing the subject is still a one-byte edit and still has
// to be caught.
func TestTheTamperDemonstrationFallsBackWhenThereIsNoPayload(t *testing.T) {
	row := store.LedgerRow{Seq: 1, Kind: store.LedgerRunStarted, Subject: "run-x"}
	original, mutated, field, err := tamperOneByte(row)
	if err != nil {
		t.Fatal(err)
	}
	if field != "subject" || byteDiff(original, mutated) != 1 {
		t.Fatalf("field=%q %q -> %q", field, original, mutated)
	}
}

// TestTheTamperDemonstrationRefusesAnAlreadyBrokenChain is the refusal that keeps
// the demo from hiding a real defect. Breaking a second chain on top of a broken one
// leaves the operator with two findings and the worse one on top.
func TestTheTamperDemonstrationRefusesAnAlreadyBrokenChain(t *testing.T) {
	st := seedLedger(t)
	if err := withGuardsRemoved(st, func() error {
		return rewriteLedgerCell(st, 2, "payload", "class=bird;score=0.900000")
	}); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() { err = tamperLedger(st, 0) })
	if err == nil {
		t.Fatal("the demonstration ran against a chain that was already broken")
	}
	for _, want := range []string{"already broken", "hides the first"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	// And it changed nothing further: row 2 is still the only broken one, so the
	// refusal did not make the damage harder to find.
	rep, verr := st.VerifyLedger(context.Background(), 0, 0)
	if verr != nil {
		t.Fatal(verr)
	}
	if rep.BrokenAt != 2 {
		t.Fatalf("BrokenAt = %d, want 2 — the refusal must not have changed anything", rep.BrokenAt)
	}
}

// TestTheTamperDemonstrationRefusesASequenceThatIsNotThere: naming a row that does
// not exist must not quietly become "the newest row instead". An operator who asked
// to break row 4 and watched row 5 break has watched the wrong thing.
func TestTheTamperDemonstrationRefusesASequenceThatIsNotThere(t *testing.T) {
	st := seedLedger(t)
	var err error
	captureStdout(t, func() { err = tamperLedger(st, 999) })
	if err == nil || !strings.Contains(err.Error(), "is not in this ledger") {
		t.Fatalf("err = %v, want a refusal naming the missing sequence", err)
	}
}

// TestTheTamperDemonstrationEditsTheRowItWasNamedAndNotTheNewest: `--seq` is the
// instructive case, and it has to be the row that changes.
func TestTheTamperDemonstrationEditsTheRowItWasNamedAndNotTheNewest(t *testing.T) {
	st := seedLedger(t)
	out := captureStdout(t, func() {
		if err := tamperLedger(st, 2); err != nil {
			t.Fatalf("tamperLedger: %v", err)
		}
	})
	if !strings.Contains(out, "TARGET   seq=2 ") {
		t.Fatalf("the named row was not the one edited:\n%s", out)
	}
	if !strings.Contains(out, "DURING   FAIL  row 2 ") {
		t.Fatalf("the failure was not attributed to the edited row:\n%s", out)
	}
}

// TestTheTamperDemonstrationRestoresTheGuardsEvenWhenItFails is the failure path.
// A callback that returns an error must still leave the database with its
// append-only triggers.
func TestTheTamperDemonstrationRestoresTheGuardsEvenWhenItFails(t *testing.T) {
	st := seedLedger(t)
	sentinel := errGuardWasNotBack
	if err := withGuardsRemoved(st, func() error { return sentinel }); err != sentinel {
		t.Fatalf("err = %v, want the callback's own error back", err)
	}
	guardsAreBack(t, st)
}

// TestBumpOneByteAlwaysChangesExactlyOnePrintableByte: the mutation is the whole
// demonstration, and a no-op mutation would produce a chain that verifies "during"
// the tamper — which is the failure mode that makes the demo print a reassuring lie.
func TestBumpOneByteAlwaysChangesExactlyOnePrintableByte(t *testing.T) {
	for _, in := range []string{"a", "z", "A", "Z", "0", "9", "9z", "score=0.700000",
		strings.Repeat("f", 64), "…", "-", " ", "=", "7"} {
		out := bumpOneByte(in)
		if len(out) != len(in) {
			t.Fatalf("bumpOneByte(%q) changed the length: %q", in, out)
		}
		if diff := byteDiff(in, out); diff != 1 {
			t.Fatalf("bumpOneByte(%q) = %q, %d byte(s) changed, want 1", in, out, diff)
		}
		if out[len(out)-1] < 0x20 || out[len(out)-1] > 0x7e {
			t.Fatalf("bumpOneByte(%q) produced a non-printable byte %q", in, out[len(out)-1])
		}
	}
}

// TestTruncateMidKeepsTheEndWhereTheChangeIs: the changed byte is the LAST one, and
// a head-only truncation would print a payload that looks untouched.
func TestTruncateMidKeepsTheEndWhereTheChangeIs(t *testing.T) {
	long := strings.Repeat("k", 100) + strings.Repeat("m", 59) + "0"
	got := truncateMid(long, 21)
	if !strings.HasSuffix(got, "0") {
		t.Fatalf("truncateMid dropped the end of %q: %q", long, got)
	}
	if got == long {
		t.Fatal("truncateMid did not truncate")
	}
	if len([]rune(got)) > 21 {
		t.Fatalf("truncateMid returned %d runes for a limit of 21: %q", len([]rune(got)), got)
	}
	// A short string is returned whole rather than mangled, which is the case every
	// ordinary payload hits.
	if got := truncateMid("short", 21); got != "short" {
		t.Fatalf("truncateMid mangled a short string: %q", got)
	}
	// And a limit too small to keep both ends returns the string rather than
	// slicing out of range, because this prints operator-facing text.
	if got := truncateMid(long, 1); got != long {
		t.Fatalf("truncateMid with a limit of 1 = %q", got)
	}
}

// TestTheTamperDemonstrationRefusesAnEmptyLedger: a detector demonstrated against no
// evidence measures nothing, and printing FAIL because there was nothing to break
// would be the same sentence a real failure prints.
func TestTheTamperDemonstrationRefusesAnEmptyLedger(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var runErr error
	captureStdout(t, func() { runErr = tamperLedger(st, 0) })
	if runErr == nil {
		t.Fatal("the demonstration ran against an empty ledger")
	}
	if !strings.Contains(runErr.Error(), "empty") {
		t.Fatalf("the refusal does not say the ledger was empty: %v", runErr)
	}
}

func byteDiff(a, b string) int {
	if len(a) != len(b) {
		return -1
	}
	n := 0
	// An index loop and not `for i := range a`: ranging over a STRING yields each
	// rune's starting index, so a three-byte character contributes one iteration and
	// a difference in its last byte goes unseen. That is precisely the byte this
	// function exists to notice.
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}

var errGuardWasNotBack = &guardError{}

type guardError struct{}

func (*guardError) Error() string {
	return "the append-only guard is NOT back: a tamper demonstration that leaves it out has turned a " +
		"temporary lie into a permanently missing control"
}
