package checkpoint

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSealed(t *testing.T, s *Signer, tenant string, from, to int64) Seal {
	t.Helper()
	rows := make([]RowHash, 0, to-from+1)
	for i := from; i <= to; i++ {
		rows = append(rows, RowHash{Sequence: i, Hash: strings.Repeat("a", 63) + string(rune('0'+i))})
	}
	signed, err := s.Sign(Seal{
		CheckpointID: "cp-1",
		TenantID:     tenant,
		SealedAt:     time.Unix(1772000000, 0).UTC(),
		FromSequence: from,
		ToSequence:   to,
		EntryCount:   int64(len(rows)),
		ChainDigest:  Digest(rows),
		// SequenceHashes is omitted on purpose in some cases below; here the
		// payload that gets signed must include it, so build it explicitly.
		SequenceHashes: rows,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return signed
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	s, err := NewSigner("key-2026-01")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	seal := newSealed(t, s, "tenant-a", 1, 10)
	if err := VerifySeal(seal, s.PublicKey()); err != nil {
		t.Fatalf("VerifySeal: %v", err)
	}
}

// The whole point of the trusted-key argument is that an attacker who can write
// the seal directory cannot also mint the key. A seal signed by a different key
// must fail even though it is internally consistent and self-signed.
func TestVerifyRejectsForeignKey(t *testing.T) {
	trusted, _ := NewSigner("trusted")
	attacker, _ := NewSigner("attacker")

	seal := newSealed(t, attacker, "tenant-a", 1, 10)
	err := VerifySeal(seal, trusted.PublicKey())
	if err == nil {
		t.Fatal("a seal signed by an untrusted key was accepted")
	}
	if !strings.Contains(err.Error(), "trusted key") {
		t.Errorf("error should name the trust failure, got %q", err)
	}
}

// No trust anchor is a refusal, not a weaker check.
//
// This used to assert the opposite: that a self-consistent seal verifies with a
// nil key. That is the whole vulnerability. A seal checked against the key it
// carries is a seal that proves only that whoever wrote it signed their own
// forgery, and the command whose whole job is to detect a re-signed seal was
// the one accepting them. There is no reading under which "I have nobody to
// trust" should mean "therefore I will trust the thing I am verifying".
func TestVerifyRefusesWithoutATrustAnchor(t *testing.T) {
	attacker, _ := NewSigner("attacker")
	seal := newSealed(t, attacker, "tenant-a", 1, 10)

	err := VerifySeal(seal, nil)
	if err == nil {
		t.Fatal("a seal verified with no trusted key. The offline verifier now accepts any " +
			"self-consistent forgery, which is the input it exists to reject")
	}
	if !errors.Is(err, ErrVerify) {
		t.Errorf("the refusal should be a verification error, got %v", err)
	}
	if !strings.Contains(err.Error(), "trusted") {
		t.Errorf("the error should say what is missing, got %q", err)
	}
}

func TestVerifyDetectsFieldTampering(t *testing.T) {
	s, _ := NewSigner("key-1")
	seal := newSealed(t, s, "tenant-a", 1, 10)

	cases := map[string]func(Seal) Seal{
		"tenant":          func(x Seal) Seal { x.TenantID = "tenant-b"; return x },
		"from sequence":   func(x Seal) Seal { x.FromSequence = 2; return x },
		"to sequence":     func(x Seal) Seal { x.ToSequence = 11; return x },
		"entry count":     func(x Seal) Seal { x.EntryCount = 99; return x },
		"chain digest":    func(x Seal) Seal { x.ChainDigest = strings.Repeat("b", 64); return x },
		"sealed at":       func(x Seal) Seal { x.SealedAt = x.SealedAt.Add(time.Hour); return x },
		"sequence hashes": func(x Seal) Seal { x.SequenceHashes = nil; return x },
		"checkpoint id":   func(x Seal) Seal { x.CheckpointID = "cp-2"; return x },
		"key id":          func(x Seal) Seal { x.KeyID = "key-2"; return x },
		"public key":      func(x Seal) Seal { x.PublicKey = strings.Repeat("c", 64); return x },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifySeal(mutate(seal), s.PublicKey()); err == nil {
				t.Fatalf("tampering with %s was not detected", name)
			}
		})
	}
}

func TestVerifyRejectsMalformedSeals(t *testing.T) {
	s, _ := NewSigner("key-1")
	base := newSealed(t, s, "tenant-a", 1, 10)

	cases := map[string]func(Seal) Seal{
		"empty digest":      func(x Seal) Seal { x.ChainDigest = ""; return x },
		"short digest":      func(x Seal) Seal { x.ChainDigest = "abc"; return x },
		"non-hex digest":    func(x Seal) Seal { x.ChainDigest = strings.Repeat("z", 64); return x },
		"inverted range":    func(x Seal) Seal { x.FromSequence = 10; x.ToSequence = 1; x.EntryCount = 10; return x },
		"count mismatch":    func(x Seal) Seal { x.EntryCount = 3; return x },
		"unknown algorithm": func(x Seal) Seal { x.Algorithm = "md5"; return x },
		"empty signature":   func(x Seal) Seal { x.Signature = ""; return x },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := VerifySeal(mutate(base), nil); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// Length-prefixed signing means the encoding is unambiguous. If it were a
// delimiter join, moving a character across a field boundary would produce the
// same payload and the same signature over a different seal.
func TestSigningPayloadIsUnambiguous(t *testing.T) {
	a := Seal{
		CheckpointID: "cp", TenantID: "t|1", SealedAt: time.Unix(0, 0).UTC(),
		FromSequence: 1, ToSequence: 2, EntryCount: 2, ChainDigest: strings.Repeat("a", 64),
	}
	b := Seal{
		CheckpointID: "cp", TenantID: "t", SealedAt: time.Unix(0, 0).UTC(),
		FromSequence: 1, ToSequence: 2, EntryCount: 2, ChainDigest: "1" + strings.Repeat("a", 63),
	}
	if signingPayload(a) == signingPayload(b) {
		t.Fatal("two different seals produced the same signing payload")
	}
}

func TestFileSinkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewFileSink(filepath.Join(dir, "seals"))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	s, _ := NewSigner("key-1")
	seal := newSealed(t, s, "tenant-a", 1, 5)
	if err := sink.Write(seal); err != nil {
		t.Fatalf("Write: %v", err)
	}

	loaded, err := sink.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("got %d seals, want 1", len(loaded))
	}
	if err := VerifySeal(loaded[0], s.PublicKey()); err != nil {
		t.Fatalf("reloaded seal does not verify: %v", err)
	}
}

func TestFileSinkRefusesUnsignedAndUnsafe(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewFileSink(dir)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	cases := map[string]Seal{
		"unsigned":         {},
		"no tenant":        {CheckpointID: "cp", Signature: "00"},
		"no checkpoint":    {TenantID: "t", Signature: "00"},
		"tenant traversal": {TenantID: "../escape", CheckpointID: "cp", Signature: "00"},
		"tenant slash":     {TenantID: "a/b", CheckpointID: "cp", Signature: "00"},
		"tenant nfd":       {TenantID: "t\x00x", CheckpointID: "cp", Signature: "00"},
		"id traversal":     {TenantID: "t", CheckpointID: "../../etc/passwd", Signature: "00"},
	}
	for name, seal := range cases {
		t.Run(name, func(t *testing.T) {
			if err := sink.Write(seal); err == nil {
				t.Fatal("sink accepted a seal it should have refused")
			}
		})
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("refused seals still left %d file(s) on disk", len(entries))
	}
}

// A removed seal is the offline attack that matters most: the database is
// rewritten, and the attacker also removes the seal that would have exposed it.
// Anyone holding the remaining seals must still be able to see the gap.
func TestGapDetectsRemovedSeal(t *testing.T) {
	dir := t.TempDir()
	sink, _ := NewFileSink(dir)
	s, _ := NewSigner("key-1")

	first := newSealed(t, s, "t", 1, 10)
	first.CheckpointID = "cp-1"
	first, _ = s.Sign(first)
	middle := newSealed(t, s, "t", 11, 20)
	middle.CheckpointID = "cp-2"
	middle, _ = s.Sign(middle)
	last := newSealed(t, s, "t", 21, 30)
	last.CheckpointID = "cp-3"
	last, _ = s.Sign(last)

	for _, seal := range []Seal{first, middle, last} {
		if err := sink.Write(seal); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "cp-2") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}
	}

	gap, err := sink.Gap(30)
	if err != nil {
		t.Fatalf("Gap: %v", err)
	}
	if gap != 11 {
		t.Fatalf("gap reported at %d, want 11", gap)
	}
}

func TestVerifyAgainstSeals(t *testing.T) {
	s, _ := NewSigner("key-1")
	seals := []Seal{}
	for i, span := range [][2]int64{{1, 10}, {11, 20}} {
		seal := newSealed(t, s, "t", span[0], span[1])
		seal.CheckpointID = "cp-" + string(rune('a'+i))
		seal, _ = s.Sign(seal)
		seals = append(seals, seal)
	}
	next, err := VerifyAgainstSeals(seals, 20, s.PublicKey())
	if err != nil {
		t.Fatalf("VerifyAgainstSeals: %v", err)
	}
	if next != 21 {
		t.Fatalf("next unsealed sequence is %d, want 21", next)
	}

	if _, err := VerifyAgainstSeals(seals[1:], 20, s.PublicKey()); err == nil {
		t.Fatal("a gap at the start of the set was not reported")
	}
}

func TestDigestChangesWithAnyRow(t *testing.T) {
	rows := []RowHash{{1, strings.Repeat("a", 64)}, {2, strings.Repeat("b", 64)}}
	base := Digest(rows)
	if base == Digest([]RowHash{rows[1], rows[0]}) {
		t.Fatal("digest is order-insensitive; the chain order is part of what is committed to")
	}
	if base == Digest(rows[:1]) {
		t.Fatal("digest did not change when a row was removed")
	}
	if len(base) != 64 {
		t.Fatalf("digest is %d chars, want 64", len(base))
	}
}

func TestNewSignerFromKey(t *testing.T) {
	s, _ := NewSigner("k")
	restored, err := NewSignerFromKey("k", s.PrivateKey())
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	seal := newSealed(t, restored, "t", 1, 3)
	if err := VerifySeal(seal, s.PublicKey()); err != nil {
		t.Fatalf("a seal signed by a restored key does not verify: %v", err)
	}
	if _, err := NewSignerFromKey("k", ed25519.PrivateKey("too short")); err == nil {
		t.Fatal("a short key was accepted")
	}
}
