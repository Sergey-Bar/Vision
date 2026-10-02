package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GenesisHash is the prev_hash of the first row.
//
// Sixty-four zeros, not an empty string: every row's hash is computed over a
// length-prefixed encoding that includes the previous hash, and an empty genesis
// would make row 1 the only row whose encoding is a different shape from every
// row after it.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// Ledger kinds. One row per decision step, and a closed set because an
// open-ended kind is a value nobody has to justify.
const (
	// LedgerRunStarted is the first row of a run: what was declared before
	// anything was measured.
	LedgerRunStarted = "run_started"
	// LedgerDetectionCommitted is the decision to raise an alert.
	LedgerDetectionCommitted = "detection_committed"
	// LedgerClipStored is the evidence write.
	LedgerClipStored = "clip_stored"
	// LedgerVLMAsked is the model call, with the attribution beside it.
	LedgerVLMAsked = "vlm_asked"
	// LedgerPushEnqueued is the intent to write back to the VMS.
	LedgerPushEnqueued = "push_enqueued"
	// LedgerPushResult is what the VMS said.
	LedgerPushResult = "push_result"
	// LedgerVLMVerdict is the validated structured output, as a hash.
	LedgerVLMVerdict = "vlm_verdict"
	// LedgerRunEnded closes the run.
	LedgerRunEnded = "run_ended"
)

// ValidLedgerKind refuses a kind outside the closed set.
func ValidLedgerKind(k string) bool {
	switch k {
	case LedgerRunStarted, LedgerDetectionCommitted, LedgerClipStored, LedgerVLMAsked,
		LedgerPushEnqueued, LedgerPushResult, LedgerVLMVerdict, LedgerRunEnded:
		return true
	}
	return false
}

// LedgerRow is one recorded decision step.
type LedgerRow struct {
	Seq      int64
	At       time.Time
	Kind     string
	Subject  string
	Payload  string
	PrevHash string
	Hash     string
}

// LedgerEntry is what a caller appends. Seq and the hashes are the store's to
// compute: a caller that supplied its own prev_hash could point the chain
// somewhere else, and a caller that supplied its own hash could point it at
// anything.
type LedgerEntry struct {
	At      time.Time
	Kind    string
	Subject string
	Payload string
}

// ErrAppendOnly is returned when something tries to rewrite a recorded row.
var ErrAppendOnly = errors.New("store: the ledger is append-only; a recorded decision row cannot be edited or deleted")

// ledgerGuardUpdate refuses an UPDATE on the ledger.
//
// A SQLite trigger rather than a rule in Go because the point is that NOBODY can
// edit a row — including a human with a sqlite3 prompt, which is exactly who you
// cannot rely on not to "just fix a typo" in an incident review.
const ledgerGuardUpdate = `CREATE TRIGGER IF NOT EXISTS ledger_no_update BEFORE UPDATE ON ledger
BEGIN SELECT RAISE(ABORT, 'the ledger is append-only: a recorded decision row cannot be edited'); END;`

// ledgerGuardDelete refuses a DELETE on the ledger, for the same reason.
const ledgerGuardDelete = `CREATE TRIGGER IF NOT EXISTS ledger_no_delete BEFORE DELETE ON ledger
BEGIN SELECT RAISE(ABORT, 'the ledger is append-only: a recorded decision row cannot be deleted'); END;`

// ledgerGuards is the whole of the append-only mechanism.
//
// REDCHECK RC-3. It is a slice of exactly two statements so that emptying it
// removes the guard and NOTHING ELSE, which is what makes the red check a
// measurement rather than a diff.
var ledgerGuards = []string{ledgerGuardUpdate, ledgerGuardDelete}

// LedgerGuardNames are the two trigger names `ledgerGuards` installs.
//
// It exists for exactly one caller — the tamper demonstration in `pocctl audit
// ledger --tamper`, which has to drop them to prove that verification catches an
// edited row, and then put them back. Spelling the names in that command instead
// would be a second copy of the guard's identity: rename a trigger in this file
// and the demonstration would silently drop nothing, report an intact chain after
// "tampering" with it, and print a PASS that means nothing.
//
// The names are returned and the DDL below is kept unexported on purpose. A tool
// that can remove the guard can always remove a trigger by name from SQL, so
// hiding the string would be security theatre; what the guard is FOR is to stop
// somebody editing a row by accident, in the middle of an incident review, while
// they still believe it is being recorded.
func LedgerGuardNames() []string {
	return []string{"ledger_no_update", "ledger_no_delete"}
}

// LedgerGuards returns the CREATE TRIGGER statements, for re-installation after a
// demonstration has taken them out.
func LedgerGuards() []string {
	return append([]string(nil), ledgerGuards...)
}

// Append adds one row to the chain.
//
// The read of the tail and the insert are in ONE transaction. Two appends racing
// would otherwise both read the same prev_hash and produce two rows claiming the
// same predecessor, which is not a chain.
func (s *Store) AppendLedger(ctx context.Context, e LedgerEntry) (LedgerRow, error) {
	if !ValidLedgerKind(e.Kind) {
		return LedgerRow{}, fmt.Errorf("store: ledger kind %q is not one this build records", e.Kind)
	}
	if strings.TrimSpace(e.Subject) == "" {
		return LedgerRow{}, errors.New("store: a ledger row with no subject cannot be attributed to anything")
	}
	at := e.At
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LedgerRow{}, fmt.Errorf("store: cannot begin the ledger append: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var seq int64
	var prev string
	row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0), COALESCE((SELECT hash FROM ledger ORDER BY seq DESC LIMIT 1), ?)
		FROM ledger`, GenesisHash)
	if err := row.Scan(&seq, &prev); err != nil {
		return LedgerRow{}, fmt.Errorf("store: cannot read the ledger tail: %w", err)
	}
	next := seq + 1
	h := ledgerHash(next, at, e.Kind, e.Subject, e.Payload, prev)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ledger (seq,at,kind,subject,payload,prev_hash,hash) VALUES (?,?,?,?,?,?,?)`,
		next, nowUTC(at), e.Kind, e.Subject, e.Payload, prev, h); err != nil {
		return LedgerRow{}, fmt.Errorf("store: cannot append to the ledger: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LedgerRow{}, fmt.Errorf("store: cannot commit the ledger append: %w", err)
	}
	return LedgerRow{
		Seq: next, At: at, Kind: e.Kind, Subject: e.Subject, Payload: e.Payload,
		PrevHash: prev, Hash: h,
	}, nil
}

// ledgerHash is the chain hash over a length-prefixed encoding.
//
// Every part is written as an 8-byte big-endian length followed by its bytes.
// The length prefix is what stops two different rows producing the same
// concatenation: without it, moving a character from the end of the subject to
// the start of the payload leaves the byte stream identical, and the hash of a
// chain that a person can re-cut at will is a hash of nothing.
func ledgerHash(seq int64, at time.Time, kind, subject, payload, prev string) string {
	h := sha256.New()
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(seq))
	h.Write(n[:])
	part := func(s string) {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	part(nowUTC(at))
	part(kind)
	part(subject)
	part(payload)
	part(prev)
	return hex.EncodeToString(h.Sum(nil))
}

// hashMatches compares two hex digests.
//
// It is a function so the red check can remove exactly this comparison and
// nothing around it. RC-2 turns it into a length test, which compiles, which
// still lets the edited-row test find a chain that looks intact.
func hashMatches(a, b string) bool { return len(a) == 64 && a == b }

// ChainReport is what verification found.
type ChainReport struct {
	// Rows is how many rows were read.
	Rows int
	// From and To are the inclusive sequence range that was checked.
	From, To int64
	// Valid is true when every row in the range chains to its predecessor.
	Valid bool
	// BrokenAt is the sequence number of the FIRST row that does not chain, or 0
	// when the chain is intact.
	//
	// It is the first rather than the only, because a chain that is broken at row
	// 3 and intact at row 4 is a chain something rewrote, and reporting only the
	// last bad row would tell a reviewer where the damage ENDED.
	BrokenAt int64
	// Reason is a sentence naming what failed, for a human.
	Reason string
}

// VerifyLedger recomputes every hash in a sequence range and reports the first
// row that does not chain.
//
// It reads rows and recomputes; it never trusts a stored hash for anything other
// than comparison, which is the entire point of it.
func (s *Store) VerifyLedger(ctx context.Context, from, to int64) (ChainReport, error) {
	rep := ChainReport{Valid: true, From: from, To: to}
	if to <= 0 {
		// No upper bound: verify to the end of the chain.
		var maxSeq sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM ledger`).Scan(&maxSeq); err != nil {
			return rep, fmt.Errorf("store: cannot read the end of the ledger: %w", err)
		}
		if !maxSeq.Valid {
			rep.Valid = false
			rep.Reason = "the ledger is empty; there is nothing to verify, and an empty chain is not an intact one"
			return rep, nil
		}
		to = maxSeq.Int64
		rep.To = to
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq,at,kind,subject,payload,prev_hash,hash FROM ledger
		WHERE seq >= ? AND seq <= ? ORDER BY seq`, from, to)
	if err != nil {
		return rep, fmt.Errorf("store: cannot read the ledger: %w", err)
	}
	defer rows.Close()

	prev := GenesisHash
	// `from` is 1-based, and 0 means "from the beginning". Starting the expected
	// sequence at the caller's `from` rather than at 1 would report the FIRST row as
	// a gap and declare every chain broken, which is the kind of bug that makes a
	// verifier ignored rather than fixed.
	seq := from
	if seq < 1 {
		seq = 1
	}
	if from > 1 {
		var prevHash string
		if err := s.db.QueryRowContext(ctx, `SELECT hash FROM ledger WHERE seq = ?`, from-1).Scan(&prevHash); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return rep, fmt.Errorf("store: verification starts at %d but the row before it does not exist, so the chain "+
					"cannot be anchored: a gap in the middle of the chain is itself the finding", from)
			}
			return rep, fmt.Errorf("store: cannot anchor the chain before sequence %d: %w", from, err)
		}
		prev = prevHash
	}

	for rows.Next() {
		var r LedgerRow
		var at string
		if err := rows.Scan(&r.Seq, &at, &r.Kind, &r.Subject, &r.Payload, &r.PrevHash, &r.Hash); err != nil {
			return rep, fmt.Errorf("store: cannot read ledger row %d: %w", seq, err)
		}
		r.At, _ = time.Parse(time.RFC3339Nano, at)
		rep.Rows++
		if r.Seq != seq {
			rep.Valid = false
			rep.BrokenAt = seq
			rep.Reason = fmt.Sprintf("the ledger skips from sequence %d to %d; a chain with a hole in it is a chain "+
				"with a row removed, and it cannot be verified past the hole", seq, r.Seq)
			return rep, nil
		}
		if !hashMatches(ledgerHash(r.Seq, r.At, r.Kind, r.Subject, r.Payload, r.PrevHash), r.Hash) {
			rep.Valid = false
			rep.BrokenAt = r.Seq
			rep.Reason = fmt.Sprintf("row %d does not hash to its stored value; the row's own contents were changed after "+
				"it was written (recorded hash %s, recomputed %s)", r.Seq, short(r.Hash),
				short(ledgerHash(r.Seq, r.At, r.Kind, r.Subject, r.Payload, r.PrevHash)))
			return rep, nil
		}
		if !hashMatches(prev, r.PrevHash) {
			rep.Valid = false
			rep.BrokenAt = r.Seq
			rep.Reason = fmt.Sprintf("row %d claims predecessor %s but row %d is %s; the chain was re-cut between them",
				r.Seq, short(r.PrevHash), r.Seq-1, short(prev))
			return rep, nil
		}
		prev = r.Hash
		seq++
	}
	if err := rows.Err(); err != nil {
		return rep, fmt.Errorf("store: cannot read the ledger: %w", err)
	}
	if rep.Rows == 0 {
		// An empty range is NOT an intact chain. It is the absence of rows, and a
		// verifier that reports an empty ledger as VALID is the exact failure that
		// makes every other check on this page decorative.
		rep.Valid = false
		rep.Reason = "no rows in the requested range; a range with no rows is not an intact chain"
		return rep, nil
	}
	rep.Reason = fmt.Sprintf("%d row(s) from sequence %d to %d recomputed and chained", rep.Rows, from, seq-1)
	return rep, nil
}

// lastSeq is the highest recorded sequence, or 0 for an empty ledger.
//
// 0 rather than an error for the empty case, because `pocctl audit ledger` on a
// fresh data root has to be able to say "nothing has been recorded" and a reader
// needs that to be a sentence and not a stack trace.
func (s *Store) lastSeq(ctx context.Context) int64 {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM ledger`).Scan(&maxSeq); err != nil {
		return 0
	}
	return maxSeq.Int64
}

// LedgerRange returns the recorded sequence bounds.
func (s *Store) LedgerRange(ctx context.Context) (from, to int64, err error) {
	var lo, hi sql.NullInt64
	if err = s.db.QueryRowContext(ctx, `SELECT MIN(seq), MAX(seq) FROM ledger`).Scan(&lo, &hi); err != nil {
		return 0, 0, fmt.Errorf("store: cannot read the ledger bounds: %w", err)
	}
	if !lo.Valid {
		return 0, 0, nil
	}
	return lo.Int64, hi.Int64, nil
}

// Ledger returns the rows in a range, for the seal and the report.
//
// `to <= 0` means "to the end of the chain", which is what [Store.VerifyLedger]
// does and what every caller passing (0, 0) meant. It did NOT used to, because this
// function compared `seq <= to` literally: `pocctl audit ledger` — the command whose
// entire job is to show a person the rows and whether they chain — asked for
// `Ledger(0, 0)`, got an empty slice, printed "the ledger is empty. An empty chain
// is not an intact one", and printed "PASS — 57 row(s) recomputed and chained" on
// the line below it. A tool that says a ledger is empty and intact in consecutive
// sentences teaches its reader to read past it.
//
// The two functions now agree on what a range means. A caller that genuinely wants
// the empty range — nothing, for a check of "is there anything here" — should ask
// for (1, 0) or use [Store.LedgerRange], and there is no such caller today.
func (s *Store) Ledger(ctx context.Context, from, to int64) ([]LedgerRow, error) {
	if to <= 0 {
		// REDCHECK RC-21: this is the whole open-ended range. Removing it puts
		// `pocctl audit ledger`'s self-contradiction back — "the ledger is empty"
		// printed above a PASS that says how many rows chained.
		to = s.lastSeq(ctx)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq,at,kind,subject,payload,prev_hash,hash FROM ledger
		WHERE seq >= ? AND seq <= ? ORDER BY seq`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the ledger: %w", err)
	}
	defer rows.Close()
	var out []LedgerRow
	for rows.Next() {
		var r LedgerRow
		var at string
		if err := rows.Scan(&r.Seq, &at, &r.Kind, &r.Subject, &r.Payload, &r.PrevHash, &r.Hash); err != nil {
			return nil, err
		}
		r.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LedgerFor returns the rows whose subject is a given detection.
func (s *Store) LedgerFor(ctx context.Context, subject string) ([]LedgerRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT seq,at,kind,subject,payload,prev_hash,hash FROM ledger
		WHERE subject=? ORDER BY seq`, subject)
	if err != nil {
		return nil, fmt.Errorf("store: cannot read the ledger for %s: %w", subject, err)
	}
	defer rows.Close()
	var out []LedgerRow
	for rows.Next() {
		var r LedgerRow
		var at string
		if err := rows.Scan(&r.Seq, &at, &r.Kind, &r.Subject, &r.Payload, &r.PrevHash, &r.Hash); err != nil {
			return nil, err
		}
		r.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

func short(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "…"
}
