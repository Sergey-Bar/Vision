package store

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func marshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("store: the seal could not be encoded: %w", err)
	}
	return b, nil
}

func unmarshalStrict(raw []byte, into any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}

func pemDecode(raw []byte) ([]byte, string) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, ""
	}
	return block.Bytes, block.Type
}

func x509ParsePKIXPublicKey(der []byte) (ed25519.PublicKey, error) {
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("the key is a %T, not an ed25519 public key", key)
	}
	return pub, nil
}

// The seal.
//
// A hash chain proves the ledger has not been edited SINCE THE LAST ROW. It does
// not prove anything about the rows before that: a person with write access can
// delete the tail, or rewrite the head and recompute every hash forward, and the
// chain will verify the whole way. What closes that is a signature over the head
// taken by a key the person does not have.
//
// So the seal is over the FINAL hash and the row count. One line, one key, one
// verification, and it is checkable by somebody who has never run this module.

// Seal is a signature over a ledger's head.
//
// It is deliberately small. A seal that carried every row would be a second copy
// of the ledger with a second thing to keep in sync, and a second thing to keep in
// sync is a second thing to get wrong.
type Seal struct {
	// Version is this seal format's version, so a future format can be read
	// rather than misparsed.
	Version int `json:"version"`
	// RunID is the run whose ledger head this seals.
	RunID string `json:"run_id"`
	// SealedAt is when the seal was taken. It is a FACT about the seal and not
	// about the run: a chain sealed a year ago is a chain that was intact a year
	// ago.
	SealedAt string `json:"sealed_at"`
	// Head is the hash of the last row, which is a commitment to every row
	// before it because each row's hash covers its predecessor's.
	Head string `json:"head"`
	// Rows is how many rows the seal covers. A count beside a head means a
	// truncated chain cannot be passed off as a short one.
	Rows int64 `json:"rows"`
	// PublicKey is the key that signed, base64 std encoding. It travels in the
	// seal so a verifier can say WHICH key signed, and verification against an
	// operator-supplied `--trusted-key` is what turns "signed" into "signed by
	// somebody we know".
	PublicKey string `json:"public_key"`
	// KeyID is the first sixteen hex characters of the public key's fingerprint,
	// so a seal can be matched to a key on a screen without printing the key.
	KeyID string `json:"key_id"`
	// Signature is ed25519 over the canonical seal body. Base64 std encoding,
	// because a seal is a thing a person pastes into a ticket.
	Signature string `json:"signature"`
	// ContractVersion is this build's content hash over the prompt, its revision,
	// the schema version and the API model name — copied here so a sealed
	// artifact says what produced it even when the tool that produced it is gone.
	ContractVersion string `json:"contract_version,omitempty"`
}

// SealVersion is this build's seal format version.
const SealVersion = 1

// SigningBody is the exact bytes a seal signs.
//
// It is a method rather than an inline string so a verifier and a signer cannot
// disagree about it: the whole value of a signature is that two programs sign and
// verify the same bytes, and a hand-written literal in each place is the one way
// to be sure they do not.
func (s Seal) SigningBody() string {
	return fmt.Sprintf("aicaptain-seal/v%d\nrun=%s\nrows=%d\nhead=%s\nsealed_at=%s\nkey_id=%s\ncontract=%s\n",
		s.Version, s.RunID, s.Rows, s.Head, s.SealedAt, s.KeyID, s.ContractVersion)
}

// KeyID is the short fingerprint of a public key.
//
// Sixteen hex characters: enough that two keys in a demo are never confused, short
// enough to read out loud.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// SealLedger signs a run's ledger head with a private key.
//
// It refuses to seal an EMPTY ledger. A signature over zero rows attests to
// nothing, and a seal that attests to nothing looks exactly like a seal that
// attests to a clean run.
func (s *Store) SealLedger(ctx context.Context, runID string, priv ed25519.PrivateKey, contractVersion string, at time.Time) (Seal, error) {
	var seal Seal
	if len(priv) != ed25519.PrivateKeySize {
		return seal, fmt.Errorf("store: the sealing key is %d bytes; an ed25519 private key is %d. A key of another "+
			"size is not a key this build can use and sealing with it would produce a signature nobody can verify",
			len(priv), ed25519.PrivateKeySize)
	}
	from, to, err := s.LedgerRange(ctx)
	if err != nil {
		return seal, err
	}
	if to == 0 {
		return seal, fmt.Errorf("store: the ledger for %s is empty, so there is nothing to seal; a signature over zero rows "+
			"attests to nothing and looks exactly like a signature over a clean run", runID)
	}
	headRow, err := s.Ledger(ctx, to, to)
	if err != nil {
		return seal, err
	}
	if len(headRow) != 1 {
		return seal, fmt.Errorf("store: the head row %d could not be read back", to)
	}
	// The chain is verified BEFORE it is signed. A seal over a broken chain is a
	// signature saying "this broken chain is what I saw", which is worse than no
	// seal: it converts a detectable defect into an attested one.
	rep, err := s.VerifyLedger(ctx, from, to)
	if err != nil {
		return seal, err
	}
	if !rep.Valid {
		return seal, fmt.Errorf("store: refusing to seal a ledger that does not verify (%s); a signature over a broken "+
			"chain converts a detectable defect into an attested one", rep.Reason)
	}
	if at.IsZero() {
		at = time.Now()
	}
	pub := priv.Public().(ed25519.PublicKey)
	seal = Seal{
		Version: SealVersion, RunID: runID, SealedAt: at.UTC().Format(time.RFC3339Nano),
		Head: headRow[0].Hash, Rows: to, PublicKey: base64.StdEncoding.EncodeToString(pub),
		KeyID: KeyID(pub), ContractVersion: contractVersion,
	}
	sig := ed25519.Sign(priv, []byte(seal.SigningBody()))
	seal.Signature = base64.StdEncoding.EncodeToString(sig)
	return seal, nil
}

// SealReport is what a verification found.
type SealReport struct {
	// SignatureOK is whether the signature matches the seal body AND the key
	// inside the seal. SignatureOK with Trusted false is a DIFFERENT outcome from
	// SignatureOK with Trusted true, and they are reported separately because
	// "signed by somebody we do not trust" and "not signed at all" need different
	// responses.
	SignatureOK bool
	// Trusted is whether the key that signed is the key the verifier named.
	Trusted bool
	// ChainOK is whether the chain recomputes from the stored rows.
	//
	// It is deliberately NOT the same field as Accepted. A chain can be intact
	// AND have grown past the head its seal covers, and that is the single most
	// common way a database gets quietly extended — append a row, keep the old
	// seal file, and every "the chain verifies" check still passes. Accepted is the
	// verdict; ChainOK is one of its three inputs.
	ChainOK bool
	// Accepted is the whole verdict: the signature is valid, the chain verifies,
	// the chain's head is the head the seal covers, and the row count matches.
	//
	// It exists so a caller cannot accidentally treat ChainOK as the answer.
	Accepted bool
	// Rows is how many rows were checked, and Head is the recomputed head, so a
	// report says WHICH chain was verified rather than only that one was.
	Rows int64
	Head string
	// Detail is a sentence naming what failed, or what was confirmed.
	Detail string
}

// VerifySeal checks a seal against the stored ledger and a trusted key.
//
// The order is chain first, then signature, and it matters: verifying the
// signature first would let a reader conclude "the chain is attested" for a chain
// that no longer verifies, because the signature is over the head hash and not
// over the rows.
func (s *Store) VerifySeal(ctx context.Context, seal Seal, trusted ed25519.PublicKey) (SealReport, error) {
	var rep SealReport
	if seal.Version != SealVersion {
		return rep, fmt.Errorf("store: the seal declares version %d and this build reads version %d; a seal is not "+
			"guessed at", seal.Version, SealVersion)
	}
	sig, err := base64.StdEncoding.DecodeString(seal.Signature)
	if err != nil {
		return rep, fmt.Errorf("store: the seal's signature is not base64: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(seal.PublicKey)
	if err != nil {
		return rep, fmt.Errorf("store: the seal's public key is not base64: %w", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		return rep, fmt.Errorf("store: the seal carries a %d-byte public key; ed25519 uses %d", len(pub), ed25519.PublicKeySize)
	}
	// The key that signed must be the one inside the seal, or a signature over one
	// key could be presented alongside another key's identity.
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(seal.SigningBody()), sig) {
		rep.Detail = "the signature does not match the seal body and the key inside it, so this seal was not produced " +
			"from the rows it claims to cover"
		return rep, nil
	}
	rep.SignatureOK = true

	if len(trusted) == ed25519.PublicKeySize {
		rep.Trusted = ed25519.PublicKey(pub).Equal(trusted)
	} else {
		rep.Trusted = KeyID(ed25519.PublicKey(pub)) == seal.KeyID
	}

	chain, err := s.VerifyLedger(ctx, 0, 0)
	if err != nil {
		return rep, err
	}
	rep.ChainOK = chain.Valid
	// Accepted stays false until every input has passed; the early returns below set it.
	rep.Rows = int64(chain.Rows)
	rep.Head = seal.Head
	if !chain.Valid {
		rep.Detail = "the signature is valid but the chain in the database no longer verifies (" + chain.Reason +
			"); rows were added, removed or edited after the seal was taken"
		return rep, nil
	}
	head, err := s.Ledger(ctx, chain.To, chain.To)
	if err != nil {
		return rep, err
	}
	if len(head) != 1 || head[0].Hash != seal.Head {
		got := "<none>"
		if len(head) == 1 {
			got = short(head[0].Hash)
		}
		rep.Detail = "the chain verifies but its head is " + got + ", not the head the seal covers; rows were appended " +
			"after the seal was taken"
		return rep, nil
	}
	if int64(chain.To) != seal.Rows {
		rep.Detail = fmt.Sprintf("the chain verifies and its head matches, but it holds %d rows and the seal covers %d; "+
			"rows were removed before the tail and the chain was re-cut", chain.To, seal.Rows)
		return rep, nil
	}
	rep.Detail = fmt.Sprintf("the seal verifies against key %s and the chain recomputes over %d rows to head %s",
		seal.KeyID, chain.Rows, short(seal.Head))
	rep.Accepted = true
	return rep, nil
}

// WriteSeal writes a seal as JSON.
//
// It creates the directory, because the seal directory does not exist until the
// first seal is taken and a tool that refuses to create its own output directory
// is a tool whose first use fails.
func WriteSeal(path string, seal Seal) error {
	body, err := marshalIndent(seal)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("store: the seal directory could not be created: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("store: the seal could not be written to %s: %w", path, err)
	}
	return nil
}

// ReadSeal reads a seal from JSON.
//
// It refuses a file with anything else in it rather than parsing what it can, so a
// truncated or appended-to seal is a refusal with a legible message.
func ReadSeal(path string) (Seal, error) {
	var seal Seal
	raw, err := os.ReadFile(path)
	if err != nil {
		return seal, fmt.Errorf("store: the seal could not be read from %s: %w", path, err)
	}
	if err := unmarshalStrict(raw, &seal); err != nil {
		return seal, fmt.Errorf("store: %s is not a seal this build reads: %w", path, err)
	}
	if seal.Version == 0 || seal.Head == "" || seal.Signature == "" {
		return seal, fmt.Errorf("store: %s is missing one of version, head or signature, so it is not a seal", path)
	}
	return seal, nil
}

// ParseTrustedKey reads a trusted public key from a file.
//
// PEM and a bare 32-byte binary are both accepted, because those are the two forms
// an operator will actually have: `openssl genpkey` produces one and
// `go run` writes the other.
func ParseTrustedKey(raw []byte) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.Contains(trimmed, "BEGIN") {
		block, _ := pemDecode([]byte(trimmed))
		if block == nil {
			return nil, errors.New("store: the trusted key file has a PEM header and no block")
		}
		key, err := x509ParsePKIXPublicKey(block)
		if err != nil {
			return nil, fmt.Errorf("store: the trusted key file is PEM but not an ed25519 public key: %w", err)
		}
		return key, nil
	}
	if b, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	if len(trimmed) == ed25519.PublicKeySize {
		return ed25519.PublicKey([]byte(trimmed)), nil
	}
	return nil, fmt.Errorf("store: the trusted key is neither PEM, base64 nor a raw %d-byte key. Print the fingerprint with "+
		"`pocctl audit verify-seals --print-key` and compare it by eye if the file is in doubt",
		ed25519.PublicKeySize)
}

// ParsePrivateKey reads a signing key from a file.
//
// PEM PKCS#8 and a bare 32-byte seed are both accepted, because those are the two
// forms `openssl genpkey` and a Go key printer produce. A public key is REFUSED:
// signing with the public half produces a signature that verifies with no private
// key present, which is the textbook mistake and one an audit tool should not
// help with.
func ParsePrivateKey(raw []byte) (ed25519.PrivateKey, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.Contains(trimmed, "BEGIN") {
		block, typ := pemDecode([]byte(trimmed))
		if block == nil {
			return nil, errors.New("store: the key file has a PEM header and no block")
		}
		key, err := x509.ParsePKCS8PrivateKey(block)
		if err != nil {
			return nil, fmt.Errorf("store: the key file is PEM but not a usable private key (%s): %w", typ, err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("store: the key is a %T, not an ed25519 private key", key)
		}
		return priv, nil
	}
	if b, err := hex.DecodeString(trimmed); err == nil {
		switch len(b) {
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(b), nil
		case ed25519.PrivateKeySize:
			return ed25519.PrivateKey(b), nil
		}
	}
	return nil, fmt.Errorf("store: the key is neither PEM PKCS#8, nor a hex %d-byte seed, nor a hex %d-byte key. A PUBLIC "+
		"key is refused here on purpose: signing with the public half produces a signature that verifies with no private "+
		"key present, which proves nothing", ed25519.SeedSize, ed25519.PrivateKeySize)
}

// KeyFingerprint is what an operator reads out loud to check a seal by hand.
func KeyFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// SealSummary renders a one-line summary for a console.
func SealSummary(rep SealReport, seal Seal) string {
	trusted := "untrusted key"
	if rep.Trusted {
		trusted = "trusted key " + seal.KeyID
	}
	switch {
	case !rep.SignatureOK:
		return "NOT SEALED  " + rep.Detail
	case !rep.ChainOK:
		return "SEAL BROKEN  " + rep.Detail
	case !rep.Trusted:
		return "SEALED BY AN UNTRUSTED KEY  " + seal.KeyID + " — the signature is valid and nobody vouched for the signer"
	default:
		return "SEALED  " + trusted + "  " + rep.Detail
	}
}
