// Package checkpoint seals the audit ledger to a sink outside the database.
//
// A seal answers one question: if the database is destroyed or an operator with
// write access rewrites history, can anyone still prove what the ledger said at
// a point in time? The answer has to be yes even when the attacker controls the
// Postgres instance, which is why the seal is signed and written somewhere the
// attacker does not control.
//
// M1 ships one implementation: a signed offline file. Production points the same
// interface at a WORM-capable target. The interface is deliberately narrow, so
// adding a target is an implementation detail rather than a redesign.
package checkpoint

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Seal is what gets written. Everything needed to verify it later is inside it
// except the trusted root: the public key is carried so a verifier can check the
// signature, and compared against the key the verifier was configured with so a
// substituted key cannot launder a forged chain.
type Seal struct {
	SchemaVersion  int       `json:"schemaVersion"`
	CheckpointID   string    `json:"checkpointId"`
	TenantID       string    `json:"tenantId"`
	SealedAt       time.Time `json:"sealedAt"`
	FromSequence   int64     `json:"fromSequence"`
	ToSequence     int64     `json:"toSequence"`
	EntryCount     int64     `json:"entryCount"`
	ChainDigest    string    `json:"chainDigest"`
	Algorithm      string    `json:"algorithm"`
	Signature      string    `json:"signature"`
	PublicKey      string    `json:"publicKey"`
	KeyID          string    `json:"keyId"`
	SequenceHashes []RowHash `json:"sequenceHashes,omitempty"`
}

// RowHash lets a verifier localize a break instead of only learning that
// something, somewhere, changed.
type RowHash struct {
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
}

// ErrVerify is returned when a seal does not hold up. The message names the
// specific failure, because "verification failed" on an air-gapped site is the
// worst possible audit answer.
var ErrVerify = errors.New("checkpoint: verification failed")

// Sink is a destination for seals.
//
// Refuse is called before every write and must return a non-nil error to stop
// it. It exists because a sink that silently accepts a write it could not
// persist would turn a seal into a claim rather than a fact, which is the
// opposite of its purpose.
type Sink interface {
	Write(seal Seal) error
	Refuse(seal Seal) error
}

// Signer produces and verifies seal signatures. Ed25519 because an air-gapped
// site verifies with a small, dependency-free implementation and a key that is
// cheap to rotate.
type Signer struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	keyID   string
}

// NewSigner generates a signing key. The private half is written to the
// product-managed key file by the installer and never to the seal, a config
// file, a log, or the ledger.
func NewSigner(keyID string) (*Signer, error) {
	if strings.TrimSpace(keyID) == "" {
		return nil, errors.New("checkpoint: key id is required")
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: generate key: %w", err)
	}
	return &Signer{private: priv, public: pub, keyID: keyID}, nil
}

// NewSignerFromKey restores a signer from a stored private key.
func NewSignerFromKey(keyID string, private ed25519.PrivateKey) (*Signer, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("checkpoint: private key is %d bytes, want %d",
			len(private), ed25519.PrivateKeySize)
	}
	return &Signer{private: private, public: private.Public().(ed25519.PublicKey), keyID: keyID}, nil
}

// KeyID identifies the signing key so a verifier can tell which of a rotated set
// signed a seal.
func (s *Signer) KeyID() string { return s.keyID }

// PublicKey returns the verification key.
func (s *Signer) PublicKey() ed25519.PublicKey { return s.public }

// PrivateKey returns the signing key. The installer persists it encrypted; this
// accessor exists so that exactly one caller in the codebase can hold it.
func (s *Signer) PrivateKey() ed25519.PrivateKey { return s.private }

// Sign fills in the signature over the seal's unsigned content.
//
// The signature covers every field except Signature itself, encoded as a
// length-prefixed string for the same reason the ledger hash is: a delimited
// join is ambiguous, and an ambiguous signature is a signature that can be
// moved from one seal to another.
func (s *Signer) Sign(seal Seal) (Seal, error) {
	seal.SchemaVersion = 1
	seal.Algorithm = "ed25519"
	seal.KeyID = s.keyID
	seal.PublicKey = hex.EncodeToString(s.public)
	seal.Signature = ""
	sig := ed25519.Sign(s.private, []byte(signingPayload(seal)))
	seal.Signature = hex.EncodeToString(sig)
	return seal, nil
}

// VerifySeal checks the signature and the internal consistency of a seal.
//
// trustedPublic, when non-nil, must match the seal's embedded key. Without
// that check an attacker with write access to the sink directory can sign a
// forged chain with a key they generated, and the verification passes - which
// is the entire threat this sink exists to defeat.
func VerifySeal(seal Seal, trustedPublic ed25519.PublicKey) error {
	if seal.Algorithm != "ed25519" {
		return fmt.Errorf("%w: unknown signature algorithm %q", ErrVerify, seal.Algorithm)
	}
	if seal.ChainDigest == "" || len(seal.ChainDigest) != 64 {
		return fmt.Errorf("%w: chain digest is not a 64-character hex SHA-256", ErrVerify)
	}
	if _, err := hex.DecodeString(seal.ChainDigest); err != nil {
		return fmt.Errorf("%w: chain digest is not valid hex: %w", ErrVerify, err)
	}
	if seal.FromSequence > seal.ToSequence {
		return fmt.Errorf("%w: sealed range %d..%d is inverted", ErrVerify, seal.FromSequence, seal.ToSequence)
	}
	if seal.ToSequence-seal.FromSequence+1 != seal.EntryCount {
		return fmt.Errorf("%w: sealed range spans %d rows but entry count is %d",
			ErrVerify, seal.ToSequence-seal.FromSequence+1, seal.EntryCount)
	}

	embedded, err := hex.DecodeString(seal.PublicKey)
	if err != nil || len(embedded) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: embedded public key is malformed", ErrVerify)
	}
	// A nil trusted key is a refusal.
	//
	// The comparison below is the whole control: a seal verified against the key
	// it carries is a seal that proves whoever wrote it signed their own forgery.
	// Skipping the comparison when no key was supplied made the offline verifier
	// - the command whose entire job is to detect a forged or re-signed seal -
	// pass on exactly the input it exists to reject. The caller now refuses
	// before it gets here; this is the backstop for any other caller.
	if trustedPublic == nil {
		return fmt.Errorf("%w: no trusted public key was supplied, so this can only confirm "+
			"that whoever wrote the seal signed it. Supply the sealing key you trust; refusing "+
			"here is the point of the check", ErrVerify)
	}
	if !equalBytes(trustedPublic, embedded) {
		return fmt.Errorf("%w: seal was signed by key %q, not the trusted key", ErrVerify, seal.KeyID)
	}
	sig, err := hex.DecodeString(seal.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is malformed", ErrVerify)
	}
	if !ed25519.Verify(embedded, []byte(signingPayload(seal)), sig) {
		return fmt.Errorf("%w: signature does not verify against key %q", ErrVerify, seal.KeyID)
	}
	return nil
}

func signingPayload(s Seal) string {
	rows := make([]string, 0, len(s.SequenceHashes))
	for _, r := range s.SequenceHashes {
		rows = append(rows, fmt.Sprintf("%d:%s", r.Sequence, r.Hash))
	}
	sort.Strings(rows)
	fields := []string{
		"v1",
		s.CheckpointID,
		s.TenantID,
		s.SealedAt.UTC().Format(time.RFC3339Nano),
		fmt.Sprintf("%d", s.FromSequence),
		fmt.Sprintf("%d", s.ToSequence),
		fmt.Sprintf("%d", s.EntryCount),
		s.ChainDigest,
		s.KeyID,
		// The public key is bound into the payload. It was not, which meant a seal
		// could be re-pointed at a different key and re-signed by whoever holds
		// that key without invalidating anything: the signature covered the
		// KeyID, which is a label, and not the key it names. The only thing that
		// caught a swapped key was the trusted-key comparison, which the
		// verifier skipped when no key was supplied.
		s.PublicKey,
		fmt.Sprintf("%d", len(rows)),
		strings.Join(rows, ","),
	}
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%d:%s|", len(f), f)
	}
	return b.String()
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FileSink writes one seal per file into a directory.
//
// The directory is the trust boundary: it must not be writable by anyone who
// can reach the database, or the seal proves nothing. The sink refuses paths
// that escape its root, because a tenant id or checkpoint id that reached an
// operator as free text is an obvious way to try.
type FileSink struct {
	Root  string
	perm  os.FileMode
	fsync bool
}

// NewFileSink creates the seal directory if it does not exist.
func NewFileSink(root string) (*FileSink, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("checkpoint: seal root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: resolve seal root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("checkpoint: create seal root: %w", err)
	}
	return &FileSink{Root: abs, perm: 0o640, fsync: true}, nil
}

func (f *FileSink) Write(seal Seal) error {
	if err := f.Refuse(seal); err != nil {
		return err
	}
	name := fmt.Sprintf("seal-%s-%s.json", sanitize(seal.TenantID), seal.CheckpointID)
	path, err := f.resolve(name)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(seal, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint: encode seal: %w", err)
	}
	body = append(body, '\n')

	if err := writeFileSync(path, body, f.perm, f.fsync); err != nil {
		return fmt.Errorf("checkpoint: write seal: %w", err)
	}
	return nil
}

// Refuse reports whether this seal may be written. It is the gate a caller checks
// before constructing a writer, kept separate so the decision is testable without a
// directory on disk.
func (f *FileSink) Refuse(seal Seal) error {
	switch {
	case seal.CheckpointID == "":
		return errors.New("checkpoint: checkpoint id is required")
	case seal.TenantID == "":
		return errors.New("checkpoint: tenant id is required")
	case seal.Signature == "":
		return errors.New("checkpoint: refusing to persist an unsigned seal")
	}
	// A tenant id reaches this code from configuration and from an install-time
	// argument. If it is not already a safe file-name fragment, the seal file
	// name would be built from sanitized input while the recorded tenant id is
	// not - two seals that collide on disk. Refuse instead of guessing.
	if !isSafeFragment(seal.TenantID) {
		return fmt.Errorf("checkpoint: tenant id %q is not a safe file-name fragment", seal.TenantID)
	}
	if !isSafeFragment(seal.CheckpointID) {
		return fmt.Errorf("checkpoint: checkpoint id %q is not a safe file-name fragment", seal.CheckpointID)
	}
	return nil
}

func isSafeFragment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func (f *FileSink) resolve(name string) (string, error) {
	if name != filepath.Base(name) {
		return "", fmt.Errorf("checkpoint: %q is not a file name", name)
	}
	full := filepath.Join(f.Root, name)
	// filepath.Join already cleaned the path, but the check is explicit
	// because a seal path is attacker-reachable input and this is the class of
	// bug the negative test suite exists to catch.
	if !strings.HasPrefix(full, f.Root+string(os.PathSeparator)) {
		return "", fmt.Errorf("checkpoint: %q escapes the seal root", name)
	}
	return full, nil
}

// List returns every seal in the directory, newest last.
func (f *FileSink) List() ([]Seal, error) {
	entries, err := os.ReadDir(f.Root)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read seal root: %w", err)
	}
	var out []Seal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := f.Load(filepath.Join(f.Root, e.Name()))
		if err != nil {
			// A corrupt file is reported, not skipped: a missing seal in a
			// sequence is exactly the signal an auditor needs.
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SealedAt.Before(out[j].SealedAt) })
	return out, nil
}

// Load reads one seal.
func (f *FileSink) Load(path string) (Seal, error) {
	var s Seal
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("checkpoint: read seal: %w", err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("checkpoint: parse seal %s: %w", filepath.Base(path), err)
	}
	return s, nil
}

// Gap returns the first unsealed sequence in [1, latest].
//
// A gap means a seal was lost or removed. Because the previous seal is signed,
// a gap is detectable offline by anyone holding the seals - which is the reason
// the seal set is worth keeping off the database host.
func (f *FileSink) Gap(latest int64) (int64, error) {
	seals, err := f.List()
	if err != nil {
		return 0, err
	}
	seals = append(seals, Seal{ToSequence: latest})
	sort.Slice(seals, func(i, j int) bool { return seals[i].ToSequence < seals[j].ToSequence })

	expected := int64(1)
	for _, s := range seals {
		if s.ToSequence < expected {
			continue
		}
		if s.FromSequence > expected {
			return expected, nil
		}
		expected = s.ToSequence + 1
	}
	return expected, nil
}

func sanitize(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if strings.Trim(out, ".") == "" {
		return "_"
	}
	return out
}
