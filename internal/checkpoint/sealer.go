package checkpoint

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Digest is the chain digest a seal commits to.
//
// It is not a hash of the rows; it is the hash of the row hashes, chained. A
// verifier holding only the seal can therefore detect any change to any row in
// the sealed range without the rows themselves, which is what makes the seal
// useful when the database is the thing that was tampered with.
func Digest(rows []RowHash) string {
	h := sha256.New()
	for _, r := range rows {
		_, _ = fmt.Fprintf(h, "%d:", r.Sequence)
		h.Write([]byte(r.Hash))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Sealer verifies a chain, then writes a signed seal for the range it verified.
//
// Verify first, seal second, and never seal what did not verify. A seal over an
// unverified chain asserts that somebody signed a chain they had not read,
// which is worse than no seal at all: it looks like evidence.
type Sealer struct {
	Sink   Sink
	Signer *Signer
	// Trusted is the public key a verifier of these seals must hold. Recorded on
	// the seal so an operator can tell which key was in use at seal time.
	Trusted ed25519.PublicKey
}

// SealRange verifies [from, latest] and, if it holds, persists a seal.
//
// The verification is the same function the on-demand audit check runs, so a seal
// can never be produced by a code path that would not also have reported a
// tamper.
func (s *Sealer) SealRange(ctx context.Context, tx pgx.Tx, tenant string, from, latest int64, at time.Time) (Seal, error) {
	var seal Seal

	var valid bool
	var toSeq int64
	// broken_at is NULL when the chain holds, and PL/pgSQL reports a
	// RETURNS TABLE column as not-null in the row description, so it has to be
	// scanned into a pointer.
	var brokenAt *int64
	var checked int64
	if err := tx.QueryRow(ctx,
		`SELECT valid, to_sequence, broken_at, checked FROM audit_verify_chain($1, $2)`,
		tenant, from).Scan(&valid, &toSeq, &brokenAt, &checked); err != nil {
		return seal, fmt.Errorf("checkpoint: verify chain: %w", err)
	}
	if !valid {
		at := int64(0)
		if brokenAt != nil {
			at = *brokenAt
		}
		return seal, fmt.Errorf("%w: tenant %s chain is broken at sequence %d after %d verified row(s); "+
			"refusing to seal a chain that does not hold",
			ErrVerify, tenant, at, checked)
	}
	if checked == 0 {
		// Sealing nothing is not a seal. Producing one anyway would put a signed
		// artefact in the sink that says nothing about any entry, and an operator
		// handed that file would reasonably believe something had been preserved.
		return seal, fmt.Errorf("%w: there is nothing to seal in sequences %d..%d for tenant %s",
			ErrVerify, from, toSeq, tenant)
	}
	if latest > 0 {
		toSeq = latest
	}

	rows, err := readRowHashes(ctx, tx, tenant, from, toSeq)
	if err != nil {
		return seal, err
	}

	seal = Seal{
		CheckpointID:   fmt.Sprintf("%s-%d-%d", tenant, toSeq, at.UTC().Unix()),
		TenantID:       tenant,
		SealedAt:       at.UTC(),
		FromSequence:   from,
		ToSequence:     toSeq,
		EntryCount:     int64(len(rows)),
		ChainDigest:    Digest(rows),
		SequenceHashes: rows,
	}

	signed, err := s.Signer.Sign(seal)
	if err != nil {
		return seal, err
	}
	// Self-check before writing. A bug in this package must not produce a seal
	// that its own verifier rejects.
	if err := VerifySeal(signed, s.Trusted); err != nil {
		return seal, fmt.Errorf("checkpoint: refusing to persist a seal that fails its own verification: %w", err)
	}
	if err := s.Sink.Write(signed); err != nil {
		return seal, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_checkpoints (
			checkpoint_id, tenant_id, sealed_at, from_sequence, to_sequence,
			entry_count, chain_digest, algorithm, signature, public_key, key_id, sink_ref
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (tenant_id, checkpoint_id) DO NOTHING`,
		signed.CheckpointID, signed.TenantID, signed.SealedAt,
		signed.FromSequence, signed.ToSequence, signed.EntryCount,
		signed.ChainDigest, signed.Algorithm, signed.Signature,
		signed.PublicKey, signed.KeyID, sinkRef(s.Sink),
	); err != nil {
		return seal, fmt.Errorf("checkpoint: record checkpoint row: %w", err)
	}
	return signed, nil
}

func sinkRef(s Sink) string {
	if f, ok := s.(*FileSink); ok {
		return "file:" + f.Root
	}
	return "sink:" + fmt.Sprintf("%T", s)
}

func readRowHashes(ctx context.Context, tx pgx.Tx, tenant string, from, to int64) ([]RowHash, error) {
	rows, err := tx.Query(ctx,
		`SELECT sequence, hash FROM audit_ledger
		  WHERE tenant_id = $1 AND sequence BETWEEN $2 AND $3 ORDER BY sequence`,
		tenant, from, to)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read ledger hashes: %w", err)
	}
	defer rows.Close()

	var out []RowHash
	for rows.Next() {
		var r RowHash
		if err := rows.Scan(&r.Sequence, &r.Hash); err != nil {
			return nil, fmt.Errorf("checkpoint: scan ledger hash: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// VerifyAgainstSeals re-verifies a set of seals against a trusted key and
// reports the first unsealed sequence.
//
// A verifier on an air-gapped site has no database. This is the whole offline
// audit path: hand it the seal files and the trusted public key, and it can say
// whether the sealed history is contiguous and internally consistent.
func VerifyAgainstSeals(seals []Seal, latest int64, trusted ed25519.PublicKey) (int64, error) {
	expected := int64(1)
	for _, s := range seals {
		if err := VerifySeal(s, trusted); err != nil {
			return expected, err
		}
		if s.ToSequence < expected {
			// Already covered by an earlier seal; a re-seal is not a gap.
			continue
		}
		if s.FromSequence > expected {
			return expected, fmt.Errorf("checkpoint: sequences %d..%d were never sealed",
				expected, s.FromSequence-1)
		}
		expected = s.ToSequence + 1
	}
	// `expected` is the first unsealed sequence, so the highest sealed sequence
	// is expected-1. Comparing expected to `latest` would report a false gap on
	// a database whose last row happens to be exactly the last sealed one.
	if latest > 0 && expected-1 > latest {
		return 0, fmt.Errorf("checkpoint: seals claim sequence %d beyond the latest known row %d",
			expected-1, latest)
	}
	return expected, nil
}
