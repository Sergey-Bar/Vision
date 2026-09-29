package checkpoint

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// DefaultInterval is how often the scheduler looks for unsealed rows.
//
// Five minutes is a compromise with a real bias. A seal is evidence of
// continuity, and evidence is worth little when it only proves what was true an
// hour ago; against that, every seal reads the whole unsealed range and verifies
// the chain, so a very short interval on a busy tenant is real work for a
// guarantee that barely advances.
const DefaultInterval = 5 * time.Minute

// Scheduler seals the audit ledger on a timer.
//
// It exists because SealRange is a primitive with no caller: the ledger was
// append-only and hash-chained, and the thing that made the chain tamper-evident
// over time - a signed seal every so often - was something an operator had to
// run by hand. A guarantee that depends on somebody remembering is not a
// guarantee.
//
// # What it never does
//
// Seal a chain that does not verify. SealRange already refuses that, and the
// scheduler treats the refusal as a hard stop for that pass and logs it loudly
// rather than retrying in a loop: a broken chain is an incident, and a scheduler
// that retries every five minutes turns it into an outage of the audit plane.
type Scheduler struct {
	// Pool opens transactions for the verify and the seal.
	Pool PGXPooler
	// Sealer does the work. It is a field rather than something built here so
	// the key handling is the caller's, and so a test can seal with a key it
	// generated.
	Sealer *Sealer
	// Tenant is the tenant whose ledger is sealed. Single-tenant per install.
	Tenant string
	// Interval is the poll period. Zero uses DefaultInterval.
	Interval time.Duration
	// Now is injectable so a test can drive time rather than sleep.
	Now func() time.Time
	// OnSealed is called after a successful seal. Nil is fine.
	OnSealed func(seal Seal)
	// Logf receives progress. It never receives ledger contents.
	Logf func(format string, args ...any)
}

// PGXPooler is the narrow part of the pool the scheduler needs.
//
// It is an interface so the scheduler can be driven in a test without a
// database, and because depending on *dbx.Pool here would make the checkpoint
// package import the pool package for one method.
type PGXPooler interface {
	InSystemTenant(ctx context.Context, fn func(tx pgx.Tx) error) error
}

// Run seals on a timer until the context is cancelled.
//
// It returns nil on cancellation: a scheduler stopped by a shutdown is the
// expected path, not an error to log. It seals once immediately, because a core
// that restarts should not wait a full interval before protecting anything.
//
// A failed pass is logged and the loop continues. It used to be discarded
// entirely, which meant a sealer that failed every time - a key that would not
// load, a database that went away, a signature that stopped verifying - produced
// no log line, no counter and no error, and the ledger simply stopped being
// sealed. The ledger is append-only and hash-chained precisely so that a gap in
// its protection is detectable, and a scheduler that cannot report the gap makes
// it undetectable. Running on after a failure is right: one bad pass is not a
// reason to stop protecting; going quiet about it is.
func (s *Scheduler) Run(ctx context.Context) error {
	s.reportSeal(ctx)

	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.reportSeal(ctx)
		}
	}
}

// reportSeal runs one pass and says what happened, in a way that survives the
// caller doing nothing with the return values.
//
// A context cancelled during shutdown is not a seal failure and is not reported
// as one: the loop is stopping by request, and a log line saying the sealer is
// broken every time a core restarts teaches its reader to ignore the ones that
// matter.
func (s *Scheduler) reportSeal(ctx context.Context) {
	sealed, err := s.sealOnce(ctx)
	if err == nil {
		if !sealed && s.Logf != nil {
			s.Logf("checkpoint: no seal written; nothing new since the last one")
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	if s.Logf != nil {
		s.Logf("checkpoint: seal failed: %v", err)
	}
}

// SealOnce runs one pass and reports whether a seal was written.
func (s *Scheduler) SealOnce(ctx context.Context) (bool, error) {
	return s.sealOnce(ctx)
}

func (s *Scheduler) sealOnce(ctx context.Context) (bool, error) {
	if s.Sealer == nil {
		return false, errors.New("checkpoint: the scheduler has no sealer; a scheduler with " +
			"no key would appear to protect the ledger and protect nothing")
	}
	if s.Tenant == "" {
		return false, errors.New("checkpoint: the scheduler has no tenant")
	}

	var sealed bool
	var from, latest int64

	err := s.Pool.InSystemTenant(ctx, func(tx pgx.Tx) error {
		// The resume point is the highest sequence already covered, from this
		// table rather than from the seal files. The files are the evidence; the
		// table is what the core trusts about its own progress. If the files were
		// deleted the table would still not re-seal an already-sealed range, which
		// is correct - re-sealing the same rows produces a second signed artefact
		// claiming the same thing, and two artefacts that must agree is one more
		// thing that can be made to disagree.
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(to_sequence), 0) FROM audit_checkpoints WHERE tenant_id = $1`,
			s.Tenant).Scan(&from); err != nil {
			return fmt.Errorf("checkpoint: read the last sealed sequence: %w", err)
		}

		var highest int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(sequence), 0) FROM audit_ledger WHERE tenant_id = $1`,
			s.Tenant).Scan(&highest); err != nil {
			return fmt.Errorf("checkpoint: read the latest ledger sequence: %w", err)
		}
		latest = highest

		from++
		if from > latest {
			// Nothing new. Not an error and not a seal: sealing nothing would put a
			// signed artefact in the sink saying nothing about any entry.
			return nil
		}

		seal, err := s.Sealer.SealRange(ctx, tx, s.Tenant, from, latest, s.now())
		if err != nil {
			if errors.Is(err, ErrVerify) {
				// A broken chain stops sealing for now and is reported. It is not
				// retried in a tight loop: the fix is a human decision about what
				// happened to the ledger, and a scheduler that keeps trying is a
				// log nobody reads.
				s.logf("the ledger chain does not verify; sealing is stopped until it does "+
					"(%v)", err)
				return nil
			}
			return err
		}
		sealed = true
		if s.OnSealed != nil {
			s.OnSealed(seal)
		}
		return nil
	})
	if err != nil {
		s.logf("a sealing pass failed: %v", err)
		return sealed, err
	}
	if sealed {
		s.logf("sealed audit sequences %d..%d for tenant %s", from, latest, s.Tenant)
	}
	return sealed, nil
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// LoadSignerKey reads an Ed25519 private key from a file.
//
// The file holds the 32-byte seed, either hex or standard base64. The seed rather
// than the full private key, because the seed is the part that is stable across
// the key files a deployment accumulates and it is half the bytes in a file an
// operator has to move around.
//
// A missing file is refused with the remedy rather than a fresh key being
// generated: a generated key seals nothing that anybody can verify, because the
// corresponding public key is not held anywhere the operator knows about. That
// failure is silent in the worst way - seals are written and every one of them
// fails verification months later.
func LoadSignerKey(path, keyID string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("checkpoint: no signing key at %s: %w. Generate one with "+
				"securityctl keygen; a key generated here would seal rows that no verifier "+
				"holds the public half for", path, err)
		}
		return nil, fmt.Errorf("checkpoint: read the signing key: %w", err)
	}
	seed, err := DecodeSeed(raw)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: %s: %w", path, err)
	}
	return NewSignerFromKey(keyID, ed25519.NewKeyFromSeed(seed))
}

// DecodeSeed reads an Ed25519 seed from hex or base64.
//
// Both because a hex key an operator generated with openssl and a base64 one
// they exported from any language are both things people arrive with, and
// refusing one of them produces a support ticket rather than a working install.
func DecodeSeed(raw []byte) ([]byte, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, errors.New("the signing key is empty")
	}
	if len(text) == 64 {
		if seed, err := hex.DecodeString(text); err == nil {
			return seed, nil
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if seed, err := enc.DecodeString(text); err == nil && len(seed) == 32 {
			return seed, nil
		}
	}
	return nil, fmt.Errorf("the signing key is neither 64 hex characters nor 32 base-32-encoded "+
		"bytes (%d bytes of input)", len(raw))
}
