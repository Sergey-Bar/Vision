package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"
)

// seedRun starts one run so the ledger rows have a run to belong to in the
// operator's head. The ledger itself is not keyed by run � it is one chain per
// database, which is the correct shape: a chain per run would let somebody move a
// row from one run to another without breaking anything.
func seedRun(t *testing.T, s *Store) string {
	t.Helper()
	id := "run-test-1"
	if err := s.StartRun(context.Background(), Run{ID: id, Label: "ledger", Mode: "live", StartedAt: time.Now(), TargetSeconds: 3600}); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return id
}

// TestTheChainLinksAndVerificationFindsAnEditedRow is W3-1.
//
// The chain is only worth anything if an edit is DETECTED, so this edits a row
// through the one path that could — raw SQL with the triggers dropped — and then
// checks that verification names the row rather than reporting "valid".
func TestTheChainLinksAndVerificationFindsAnEditedRow(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	runID := seedRun(t, s)

	prev := GenesisHash
	for i, kind := range []string{
		LedgerRunStarted, LedgerDetectionCommitted, LedgerClipStored,
		LedgerVLMAsked, LedgerVLMVerdict, LedgerPushEnqueued, LedgerPushResult, LedgerRunEnded,
	} {
		row, err := s.AppendLedger(ctx, LedgerEntry{
			At: time.Now().Add(time.Duration(i) * time.Second), Kind: kind,
			Subject: "det-abc", Payload: `{"k":"v","n":1}`,
		})
		if err != nil {
			t.Fatalf("AppendLedger(%s): %v", kind, err)
		}
		if row.Seq != int64(i+1) {
			t.Fatalf("the %s row is at sequence %d rather than %d", kind, row.Seq, i+1)
		}
		if row.PrevHash != prev {
			t.Fatalf("the %s row claims predecessor %s and the previous row hashed to %s", kind, short(row.PrevHash), short(prev))
		}
		if len(row.Hash) != 64 {
			t.Fatalf("the %s row's hash is %d characters", kind, len(row.Hash))
		}
		prev = row.Hash
	}
	_ = runID

	rep, err := s.VerifyLedger(ctx, 0, 0)
	if err != nil {
		t.Fatalf("VerifyLedger: %v", err)
	}
	if !rep.Valid || rep.Rows != 8 {
		t.Fatalf("an untouched chain reports %+v", rep)
	}
	if rep.BrokenAt != 0 {
		t.Fatalf("an intact chain reports a break at %d", rep.BrokenAt)
	}

	// Now edit row 4. The triggers refuse an UPDATE, which is the point — so the
	// edit is done with the guard removed, which is exactly what a person with a
	// sqlite3 prompt and an afternoon to lose would do.
	dropLedgerGuards(t, s)
	if _, err := s.DB().ExecContext(ctx,
		`UPDATE ledger SET payload=? WHERE seq=4`, `{"k":"v","n":999999}`); err != nil {
		t.Fatalf("the raw UPDATE was refused even with the triggers dropped: %v", err)
	}

	rep, err = s.VerifyLedger(ctx, 0, 0)
	if err != nil {
		t.Fatalf("VerifyLedger: %v", err)
	}
	if rep.Valid {
		t.Fatal("an edited row still verifies; a chain that cannot detect an edit is a comment")
	}
	if rep.BrokenAt != 4 {
		t.Fatalf("the break is reported at %d rather than at the edited row 4: %s", rep.BrokenAt, rep.Reason)
	}
	if !strings.Contains(rep.Reason, "4") {
		t.Fatalf("the reason does not name the row: %s", rep.Reason)
	}
}

// TestAnOpenEndedLedgerRangeMeansTheWholeChain is the test behind the open-ended
// range, and it exists because `pocctl audit ledger` — the command whose whole job
// is to show a person the rows and whether they chain — passed (0, 0) and got an
// EMPTY slice back from a ledger holding fifty-seven rows. It printed
//
//	the ledger is empty. An empty chain is not an intact one
//	PASS — 57 row(s) from sequence 0 to 57 recomputed and chained
//
// in consecutive sentences. VerifyLedger has always read (0, 0) as "to the end";
// Ledger compared `seq <= to` literally. A tool that contradicts itself about
// whether its evidence exists teaches its reader to read past it, and this one is
// read by people deciding whether to trust a run.
func TestAnOpenEndedLedgerRangeMeansTheWholeChain(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	for i := 0; i < 5; i++ {
		if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerVLMVerdict, Subject: "det-1", Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Ledger(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("Ledger(0,0) returned %d row(s) for a ledger of 5; an open-ended range means to the end", len(got))
	}
	// The two functions must agree about WHICH rows they are talking about, which
	// is the property the caller actually depends on: it verifies (0,0) and prints
	// (0,0), and a disagreement between them is exactly the bug.
	rep, err := s.VerifyLedger(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != len(got) {
		t.Fatalf("VerifyLedger read %d row(s) and Ledger read %d; they are asked about the same range", rep.Rows, len(got))
	}
	// A bounded range is still bounded: the open-ended rule must not swallow it.
	two, err := s.Ledger(ctx, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 2 || two[0].Seq != 2 || two[1].Seq != 3 {
		t.Fatalf("Ledger(2,3) = %+v, want sequences 2 and 3", two)
	}
	// And an empty ledger is empty rather than an error, because `audit ledger` on
	// a fresh data root has to be able to say so.
	empty, err := store2(t)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := empty.Ledger(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("an empty ledger returned %d row(s)", len(rows))
	}
}

// store2 opens a second, empty store in its own temporary directory.
func store2(t *testing.T) (*Store, error) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, nil
}

// TestTheLedgerIsAppendOnly is W3-2, and it is a claim about SQL rather than about
// a Go function: the guard has to stop somebody who is not running this program.
func TestTheLedgerIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	row, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerRunStarted, Subject: "det-1", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE ledger SET payload='{"tampered":true}' WHERE seq=?`,
		`DELETE FROM ledger WHERE seq=?`,
		`UPDATE ledger SET kind='run_ended' WHERE seq=?`,
		`DELETE FROM ledger`,
	} {
		if _, err := s.DB().ExecContext(ctx, stmt, row.Seq); err == nil {
			t.Fatalf("%q was accepted on the ledger", stmt)
		} else if !strings.Contains(strings.ToLower(err.Error()), "append-only") {
			t.Fatalf("%q was refused for the wrong reason: %v", stmt, err)
		}
	}
	// And the row survived, unchanged, because a refusal that half-applied would
	// be worse than no guard.
	got, err := s.Ledger(ctx, row.Seq, row.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hash != row.Hash || got[0].Payload != row.Payload {
		t.Fatalf("the row changed despite the refusals: %+v", got)
	}
	rep, err := s.VerifyLedger(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Valid {
		t.Fatalf("the chain is not valid after four refused writes: %s", rep.Reason)
	}
}

// TestARowWithAMovedFieldBoundaryDoesNotRehash is the reason the encoding is
// length-prefixed. Without it, moving a character from the end of the subject to
// the start of the payload leaves the same byte stream, and an attacker can rewrite
// a row without breaking the chain.
func TestARowWithAMovedFieldBoundaryDoesNotRehash(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerVLMVerdict, Subject: "det-1", Payload: `{"a":"bc"}`}); err != nil {
		t.Fatal(err)
	}
	dropLedgerGuards(t, s)
	if _, err := s.DB().ExecContext(ctx,
		`UPDATE ledger SET subject=?, payload=? WHERE seq=1`, `det-1{"a"`, `:"bc"}`); err != nil {
		t.Fatal(err)
	}
	rep, err := s.VerifyLedger(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid {
		t.Fatal("a row whose field boundary was moved still verifies; the hash is not length-prefixed")
	}
}

// TestAnUnknownLedgerKindIsRefused: an open-ended kind is a value nobody has to
// justify, and a reader cannot know what a row means.
func TestAnUnknownLedgerKindIsRefused(t *testing.T) {
	s := openStore(t)
	if _, err := s.AppendLedger(context.Background(), LedgerEntry{Kind: "something_happened", Subject: "det-1"}); err == nil {
		t.Fatal("an unknown ledger kind was accepted")
	}
	if _, err := s.AppendLedger(context.Background(), LedgerEntry{Kind: LedgerRunStarted, Subject: "  "}); err == nil {
		t.Fatal("a ledger row with no subject was accepted; nothing could attribute it")
	}
}

// TestTheSealCoversTheHeadAndOnlyTheHead is W3-3.
//
// Three distinct attacks, three distinct outcomes. A seal that cannot tell them
// apart is a seal that says nothing.
func TestTheSealCoversTheHeadAndOnlyTheHead(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AppendLedger(ctx, LedgerEntry{
			Kind: LedgerDetectionCommitted, Subject: "det-1", Payload: `{"n":1}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seal, err := s.SealLedger(ctx, "run-test-1", priv, "contract-abc", time.Now())
	if err != nil {
		t.Fatalf("SealLedger: %v", err)
	}
	if seal.Rows != 3 || len(seal.Head) != 64 || seal.KeyID != KeyID(pub) {
		t.Fatalf("the seal is %+v", seal)
	}
	rep, err := s.VerifySeal(ctx, seal, pub)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Accepted {
		t.Fatalf("a fresh seal does not verify: %+v", rep)
	}

	// (a) An APPENDED row. The chain still verifies and the signature is still
	// valid, but the head no longer matches, and that has to be the answer.
	if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerRunEnded, Subject: "det-1", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	rep, err = s.VerifySeal(ctx, seal, pub)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.SignatureOK {
		t.Fatal("the signature did not verify after an append; it is over the head, not over the rows")
	}
	if rep.Accepted {
		t.Fatalf("a chain extended past its seal was ACCEPTED: %+v", rep)
	}
	if !rep.ChainOK {
		t.Fatal("the chain itself should still verify after an append; the defect is the head, not the chain")
	}
	if !strings.Contains(rep.Detail, "appended") {
		t.Fatalf("the reason does not say the chain grew: %s", rep.Detail)
	}
}

// TestASealOverABrokenChainIsRefused: signing a chain that does not verify turns a
// detectable defect into an attested one, which is worse than no seal.
func TestASealOverABrokenChainIsRefused(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	for i := 0; i < 2; i++ {
		if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerRunStarted, Subject: "det-1", Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	dropLedgerGuards(t, s)
	if _, err := s.DB().ExecContext(ctx, `UPDATE ledger SET payload='{"x":1}' WHERE seq=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SealLedger(ctx, "run-test-1", priv, "", time.Now()); err == nil {
		t.Fatal("a broken chain was sealed")
	} else if !strings.Contains(err.Error(), "verify") {
		t.Fatalf("the refusal does not say the chain did not verify: %v", err)
	}
}

// TestAnEmptyLedgerCannotBeSealed: a signature over zero rows attests to nothing
// and looks exactly like a signature over a clean run.
func TestAnEmptyLedgerCannotBeSealed(t *testing.T) {
	s := openStore(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := s.SealLedger(context.Background(), "run-empty", priv, "", time.Now()); err == nil {
		t.Fatal("an empty ledger was sealed")
	}
}

// TestASealFromAnotherKeyIsReportedAsUntrustedRatherThanValid: a valid signature
// from a key nobody vouched for is a different fact from a valid signature from the
// operator's key, and reporting them the same way is how a compromise reads as a
// clean run.
func TestASealFromAnotherKeyIsReportedAsUntrustedRatherThanValid(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pubA, privA, _ := ed25519.GenerateKey(rand.Reader)
	pubB, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerRunStarted, Subject: "det-1", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	seal, err := s.SealLedger(ctx, "run-1", privA, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.VerifySeal(ctx, seal, pubB)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.SignatureOK {
		t.Fatal("the signature did not verify; the signature is over the body and the key in it, not over the trust decision")
	}
	if rep.Trusted {
		t.Fatal("a key nobody named was reported as trusted")
	}
	if s := SealSummary(rep, seal); !strings.Contains(s, "UNTRUSTED") {
		t.Fatalf("the summary does not say UNTRUSTED: %s", s)
	}
	rep, err = s.VerifySeal(ctx, seal, pubA)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Trusted {
		t.Fatal("the key that signed was not reported as trusted when it was the one named")
	}
}

// TestATamperedSealBodyDoesNotVerify: the signature covers the seal's own fields,
// so editing the row count in the seal is caught.
func TestATamperedSealBodyDoesNotVerify(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerRunStarted, Subject: "det-1", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	seal, err := s.SealLedger(ctx, "run-1", priv, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Seal){
		func(x *Seal) { x.Rows = 999 },
		func(x *Seal) { x.Head = strings.Repeat("a", 64) },
		func(x *Seal) { x.RunID = "someone-elses-run" },
		func(x *Seal) { x.SealedAt = "2099-01-01T00:00:00Z" },
		func(x *Seal) { x.ContractVersion = "a-different-build" },
	} {
		bad := seal
		mutate(&bad)
		rep, err := s.VerifySeal(ctx, bad, pub)
		if err != nil {
			t.Fatalf("VerifySeal: %v", err)
		}
		if rep.SignatureOK {
			t.Fatalf("a seal with an edited field verifies: %+v", bad)
		}
	}
}

// TestTheSealSurvivesAReplacedDatabase is the reason the seal is a FILE: a seal
// stored inside the database is rewritten along with the thing it is evidence about.
func TestTheSealSurvivesAReplacedDatabase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := s.AppendLedger(ctx, LedgerEntry{Kind: LedgerRunStarted, Subject: "det-1", Payload: "{}"}); err != nil {
		t.Fatal(err)
	}
	seal, err := s.SealLedger(ctx, "run-1", priv, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := s.SealPath("run-1")
	if err := WriteSeal(path, seal); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Replace the database wholesale. The seal file is beside it and untouched.
	if err := osRemove(s.Path()); err != nil {
		t.Fatalf("could not remove the database: %v", err)
	}
	fresh, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	read, err := ReadSeal(path)
	if err != nil {
		t.Fatalf("the seal file did not survive the database being replaced: %v", err)
	}
	if read.Head != seal.Head || read.Signature != seal.Signature {
		t.Fatal("the seal changed")
	}
	// And it now says something useful: the chain it covered is GONE.
	rep, err := fresh.VerifySeal(ctx, read, pub)
	if err != nil {
		t.Fatal(err)
	}
	if rep.SignatureOK != true {
		t.Fatalf("the signature itself did not verify: %+v", rep)
	}
	if rep.ChainOK {
		t.Fatal("a replaced database reported the old chain as intact")
	}
}

// TestTheTrustedKeyIsRefusedWhenItIsAPublicKeyUsedForSigning is the classic
// mistake: signing with the public half produces a signature that verifies with no
// private key present.
func TestTheTrustedKeyIsRefusedWhenItIsAPublicKeyUsedForSigning(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := ParsePrivateKey(pub); err == nil {
		t.Fatal("a public key was accepted as a signing key")
	}
	if _, err := ParsePrivateKey(nil); err == nil {
		t.Fatal("an empty file was accepted as a signing key")
	}
	if _, err := ParseTrustedKey([]byte("not a key at all")); err == nil {
		t.Fatal("a sentence was accepted as a trusted key")
	}
	if got, err := ParseTrustedKey(pub); err != nil || !got.Equal(pub) {
		t.Fatalf("a raw 32-byte public key was not read: %v", err)
	}
}

// dropLedgerGuards removes the append-only triggers.
//
// It is a TEST-ONLY helper and it is deliberately a separate function: an edited
// row has to be produced somehow to prove that verification catches it, and doing
// it by hand here is honest about the fact that this is exactly what somebody with
// a sqlite3 prompt would do.
func dropLedgerGuards(t *testing.T, s *Store) {
	t.Helper()
	for _, name := range []string{"ledger_no_update", "ledger_no_delete"} {
		if _, err := s.DB().Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			t.Fatalf("the test could not drop the guard: %v", err)
		}
	}
}

// osRemove is a named helper so the one test that deletes the database says so.
func osRemove(path string) error { return os.Remove(path) }
