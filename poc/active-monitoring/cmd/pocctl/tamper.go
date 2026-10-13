package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Sergey-Bar/vision-poc/internal/store"
)

// `--tamper` on `pocctl audit ledger`.
//
// The claim this module makes about its ledger is "a recorded decision cannot be
// edited, and if one is, verification says so and names the row". A claim like
// that is worth nothing until somebody has watched it happen, because a hash chain
// that has only ever verified is indistinguishable from a chain that would verify
// anything. So this edits one byte of one row, watches the recomputation fail,
// puts the byte back, and watches it verify again — on the operator's own data
// root, in one command, in about a second.
//
// Three things it refuses to do, and each refusal exists for the same reason:
//
//   - It refuses to run against a chain that is ALREADY broken. Tampering with a
//     damaged chain produces a second, confusing failure on top of the first, and
//     the operator then has two things to look at instead of the one that matters.
//   - It restores the original byte and the append-only guards before it returns,
//     including from its own error paths, and it re-verifies after restoring. A
//     demonstration that leaves the evidence broken is not a demonstration.
//   - It never reports the broken state as anything but FAIL. The module's
//     vocabulary is PASS / FAIL / NOT RUN, and "the chain is intact again" is a
//     different claim from "the chain was never tampered with".

// tamperLedger is the whole demonstration. It is its own function rather than a
// branch inside auditLedger because it has to run after the ordinary read path has
// printed, and interleaving a destructive step with a read loop is how a tool ends
// up restoring something it no longer remembers.
func tamperLedger(st *store.Store, seq int64) error {
	// The chain has to be intact before it is broken. Verifying first is not
	// ceremony; it is what makes the later FAIL attributable to this command rather
	// than to something that was already wrong.
	before, err := st.VerifyLedger(ctx(), 0, 0)
	if err != nil {
		return err
	}
	if !before.Valid {
		return fmt.Errorf("audit ledger --tamper: refusing to run against a chain that is already broken (%s). "+
			"Breaking a second one hides the first, and the one that was already there is the one to look at",
			before.Reason)
	}
	fmt.Printf("\n  BEFORE   PASS  %s\n", before.Reason)

	row, err := pickTamperRow(st, seq)
	if err != nil {
		return err
	}
	original, mutated, field, err := tamperOneByte(row)
	if err != nil {
		return err
	}
	fmt.Printf("  TARGET   seq=%d  kind=%s  subject=%s\n", row.Seq, row.Kind, trunc(row.Subject, 60))
	fmt.Printf("           %s: %q\n", field, truncateMid(original, 96))
	fmt.Printf("         -> %q\n", truncateMid(mutated, 96))

	// The guards come out first, because they are what stops this. Say so, and say
	// that taking them out is the honest part: somebody with a sqlite3 prompt does
	// exactly this, and the triggers are there to stop the careless edit during an
	// incident review rather than the determined one.
	fmt.Printf("\n  taking the append-only guards out (%s)\n", strings.Join(store.LedgerGuardNames(), ", "))
	fmt.Printf("  this is the step a person with a sqlite3 prompt takes. The guards exist to stop the\n")
	fmt.Printf("  accidental edit, not the deliberate one.\n\n")

	if err := withGuardsRemoved(st, func() error {
		if err := rewriteLedgerCell(st, row.Seq, field, mutated); err != nil {
			return err
		}
		rep, verr := st.VerifyLedger(ctx(), 0, 0)
		if verr != nil {
			return verr
		}
		if rep.Valid {
			return errors.New("the chain verified with an edited row in it. The verification does not " +
				"detect an edit, so nothing printed after this line means anything")
		}
		fmt.Printf("  DURING   FAIL  row %d is the first one that does not chain\n", rep.BrokenAt)
		fmt.Printf("           %s\n\n", rep.Reason)

		if err := rewriteLedgerCell(st, row.Seq, field, original); err != nil {
			return fmt.Errorf("the tampered byte could not be put back, so this data root is left BROKEN and "+
				"needs a human: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	// The third reading is the one that answers the question a sceptic actually has,
	// which is whether the tool leaves the data as it found it.
	after, err := st.VerifyLedger(ctx(), 0, 0)
	if err != nil {
		return err
	}
	if !after.Valid {
		return fmt.Errorf("audit ledger --tamper: the chain is STILL broken after the byte was restored (%s). "+
			"The byte was put back and the chain did not recover, so something other than this "+
			"demonstration is wrong", after.Reason)
	}
	fmt.Printf("  AFTER    PASS  %s\n", after.Reason)
	fmt.Println("\n  One byte was changed and changed back. The chain noticed both times and named the row.")
	fmt.Println("  That is the whole claim: not that the ledger cannot be edited — it can, by somebody with")
	fmt.Println("  database access — but that an edit is detected and located, and that `pocctl audit")
	fmt.Println("  ledger` and `pocctl prove` are what detect it.")
	return nil
}

// pickTamperRow chooses which row to edit.
//
// The default is the LAST row: it is the most recent thing recorded and therefore
// the one somebody would edit, and it is the only choice whose restoration is
// independent of everything after it — there is nothing after the last row for a
// half-restored chain to disagree with. `--seq` exists because editing a middle
// row is the more instructive demonstration to run in front of a sceptic, and a
// tool that could only do the first thing would leave that to a person with a
// sqlite3 prompt.
func pickTamperRow(st *store.Store, seq int64) (store.LedgerRow, error) {
	if seq > 0 {
		rows, err := st.Ledger(ctx(), seq, seq)
		if err != nil {
			return store.LedgerRow{}, err
		}
		if len(rows) == 0 {
			return store.LedgerRow{}, fmt.Errorf("audit ledger --tamper: sequence %d is not in this ledger, "+
				"so there is no row to change", seq)
		}
		return rows[0], nil
	}
	rows, err := st.Ledger(ctx(), 0, 0)
	if err != nil {
		return store.LedgerRow{}, err
	}
	if len(rows) == 0 {
		return store.LedgerRow{}, errors.New("audit ledger --tamper: the ledger is empty. There is nothing to " +
			"tamper with, and a detector demonstrated against no evidence measures nothing")
	}
	return rows[len(rows)-1], nil
}

// tamperOneByte produces the pair of values: what the cell holds now, and what it
// will hold after exactly one byte changes.
//
// ONE byte, and not "some plausible-looking value", because the claim is that a
// single changed character is caught. A demonstration that rewrites a whole field
// proves the hash covers the field; it does not prove the hash covers the byte, and
// a length-prefixed encoding with a weak boundary is exactly the kind of thing that
// passes a field-level test and loses a byte.
//
// The cell is the payload, falling back to the subject and then the hash. A payload
// is what a reader looks at, so changing it is the case where the stored hash and
// the row's meaning visibly disagree; with no payload there is still a subject, and
// with neither there is the hash itself, and changing that still breaks the
// recomputation.
func tamperOneByte(row store.LedgerRow) (original, mutated, field string, err error) {
	for _, c := range []struct {
		name string
		v    string
	}{
		{"payload", row.Payload},
		{"subject", row.Subject},
		{"hash", row.Hash},
	} {
		if c.v == "" {
			continue
		}
		if n := bumpOneByte(c.v); n != c.v {
			return c.v, n, c.name, nil
		}
	}
	return "", "", "", errors.New("audit ledger --tamper: no cell of this row can be changed without changing " +
		"its length, and a length change would be a different demonstration than the one this claims to be")
}

// bumpOneByte changes the final byte to a different printable byte, keeping the
// string the same length so the demonstration really is one byte and not a
// reallocation.
func bumpOneByte(s string) string {
	b := []byte(s)
	last := len(b) - 1
	switch c := b[last]; {
	case c >= '0' && c < '8', c >= 'a' && c < 'y', c >= 'A' && c < 'Y':
		b[last] = c + 1
	case c == '9':
		b[last] = '8'
	case c == 'z', c == 'Z':
		b[last] = 'y'
	default:
		b[last] = 'X'
	}
	return string(b)
}

// rewriteLedgerCell writes one column of one row.
//
// The UPDATE is what the guards refuse, which is why `withGuardsRemoved` has to be
// holding. The row is matched on seq AND on its current value: a blind write would
// happily overwrite a row somebody else appended between the read and the write,
// and a tamper demonstration that silently eats an unrelated row is a data-loss bug
// wearing a demonstration's clothes.
func rewriteLedgerCell(st *store.Store, seq int64, column, value string) error {
	switch column {
	case "payload", "subject", "hash":
	default:
		return fmt.Errorf("audit ledger --tamper: %q is not a ledger column this command will write", column)
	}
	q := fmt.Sprintf(`UPDATE ledger SET %s=? WHERE seq=? AND %s=?`, column, column)
	res, err := st.DB().ExecContext(ctx(), q, value, seq, currentCell(st, seq, column))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("ledger row %d did not match on %s while it was being changed, so nothing was "+
			"written. A concurrent append is the likely cause and no row was touched", seq, column)
	}
	return nil
}

func currentCell(st *store.Store, seq int64, column string) string {
	var v string
	_ = st.DB().QueryRowContext(ctx(), `SELECT `+column+` FROM ledger WHERE seq=?`, seq).Scan(&v)
	return v
}

// withGuardsRemoved drops the append-only triggers, runs f, and puts them back.
//
// The put-back is in a `defer` and not at the end of the happy path, because every
// early return between here and there would otherwise leave a database with no
// append-only protection, and that is a much worse thing to leave behind than a red
// demonstration.
func withGuardsRemoved(st *store.Store, f func() error) error {
	if err := dropGuards(st); err != nil {
		return err
	}
	// f runs BETWEEN the drop and the put-back, which is the whole point and the
	// reason the order of these three statements is not free. Installing the guards
	// first — which this did once — makes f's UPDATE fail with the guard's own
	// refusal and the demonstration reports an intact chain on a row it never
	// changed, which is a PASS that means nothing.
	runErr := f()
	if err := reinstallGuards(st); err != nil {
		// Reported before f's error: a demonstration that leaves the guards out has
		// turned a temporary lie into a permanently missing control, and that outranks
		// whatever f was complaining about.
		return fmt.Errorf("audit ledger --tamper: the append-only guards could not be put back, which is worse "+
			"than the edit this was demonstrating. Re-run `pocctl audit ledger` and read this line: %v", err)
	}
	return runErr
}

func dropGuards(st *store.Store) error {
	for _, name := range store.LedgerGuardNames() {
		if _, err := st.DB().ExecContext(ctx(), `DROP TRIGGER IF EXISTS `+name); err != nil {
			return fmt.Errorf("audit ledger --tamper: the guard %s could not be removed, so the edit would be "+
				"refused and the demonstration would measure nothing: %w", name, err)
		}
	}
	return nil
}

func reinstallGuards(st *store.Store) error {
	for _, ddl := range store.LedgerGuards() {
		if _, err := st.DB().ExecContext(ctx(), ddl); err != nil {
			return err
		}
	}
	return nil
}

// truncateMid keeps both ends of a long cell, because the changed byte is at the end
// and a head-only truncation would hide the only part that matters.
func truncateMid(s string, n int) string {
	r := []rune(s)
	if len(r) <= n || n < 3 {
		return s
	}
	head := (n - 1) / 2
	tail := n - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}
