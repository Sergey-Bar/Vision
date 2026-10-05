package evidencefetch

// The receipt.
//
// This is what makes the bookmark auditable. The footage lives on the vendor's archive and
// disappears when that vendor's retention rolls it off; a decision recorded only as "we
// bookmarked it" cannot answer "did we, when, and at which second" afterwards. The receipt
// answers all three from our own storage, and it is the reason an `evidence` row of kind
// `bookmark` is worth writing for a pointer rather than a copy.
//
// # Why it is a small document and not a row of columns
//
// The evidence table's own columns cannot hold the answer. `storage_ref` points at bytes, so
// something has to be written; `sha256` is NOT NULL and is checked against a 64-hex
// pattern, so the object has to be a real object with a real digest; and `size_bytes` is
// NOT NULL with a `>= 0` check, so it has to be measured rather than invented. A receipt
// satisfies all three honestly and is small enough that keeping it costs nothing.
//
// The alternative — widening the table with `vendor_bookmark_id`, `opened_at` and
// `drift_applied_ms` — is a migration, a human to read the diff, and three columns that
// describe one vendor's bookmark in a table that also holds a clip from any vendor. The
// document generalises: a second vendor's receipt has fields this one does not, and a JSON
// document grows; a column does not.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/dispatch"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/milestone"
)

// ReceiptVersion is the receipt's own contract.
//
// A version on a document that outlives the code that wrote it, for the same reason the
// dispatch payload carries one: a reader six months from now has to be able to tell whether
// the fields it is looking at mean what they meant on the day.
const ReceiptVersion = "security.io/evidence-bookmark-receipt/v1"

// Receipt is the document.
//
// `Vendor` is a field rather than assumed, because `milestone` is one adapter among a set
// the product is built to extend and a receipt that did not say which system holds the
// footage would be a receipt nobody could act on after the adapter is swapped.
type Receipt struct {
	ContractVersion string `json:"contract_version"`
	IncidentID      string `json:"incident_id"`
	SiteID          string `json:"site_id"`
	Vendor          string `json:"vendor"`

	// BookmarkID is the vendor's own id for the pointer. It is what the operator's player
	// opens, and the only field here that cannot be reconstructed from the rest.
	BookmarkID string `json:"bookmark_id"`
	// Replayed is the vendor's answer to "did you just create this". False against every
	// documented build of this API, and carried rather than dropped so a reader can see
	// that the question was asked. See milestone.BookmarkRef.
	Replayed bool `json:"replayed"`

	// OpenedAt is the second the operator should open at, on OUR clock after the drift
	// correction.
	OpenedAt time.Time `json:"opened_at"`
	// Window is the incident's window the bookmark was placed over.
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	// DriftAppliedMs is the correction that was applied, in milliseconds, signed.
	//
	// Signed because the sign is the whole of it: `-40000` means the vendor's clock was
	// behind ours by forty seconds and the bookmark was moved forward to compensate.
	// Recording the absolute value would turn a corrected bookmark into one that looks
	// uncorrected.
	DriftAppliedMs int64  `json:"drift_applied_ms"`
	DriftBand      string `json:"drift_band"`
	// Label is the human phrase the vendor's bookmark list shows.
	Label string `json:"label"`

	// FootageIsElsewhere is always true, and always written.
	//
	// This product does not duplicate an archive. A reader holding only this receipt must
	// not conclude the video is stored here, so the field says so in the document rather
	// than in a README nobody reads when the vendor refuses to answer.
	FootageIsElsewhere bool `json:"footage_is_elsewhere"`

	// EvidenceCount is zero and is stated, because "we fetched no video" and "we fetched
	// zero bytes" are different facts and only one of them is true.
	EvidenceCount int `json:"evidence_bytes_fetched"`
}

// record writes the receipt: the object, the row, and the ledger entry.
//
// The object is written OUTSIDE the transaction that writes the row, and that ordering is
// deliberate. A blob store is a filesystem and a transaction is a database; holding a
// database transaction open across filesystem writes to get them atomic buys nothing,
// because there is no two-phase commit across the two. The chosen order is object-then-row,
// which leaves at worst an unreferenced object — bytes nobody points at, collected by the
// store's own sweep — rather than a row pointing at an object that was never written. A row
// that points at nothing is an operator who opens an incident and gets a broken link.
func (w *Worker) record(ctx context.Context, j jobs.Job, p dispatch.JobPayload,
	ref milestone.BookmarkRef, openedAt time.Time, f Facts) (string, error) {

	receipt := Receipt{
		ContractVersion:    ReceiptVersion,
		IncidentID:         f.IncidentID,
		SiteID:             f.SiteID,
		Vendor:             VendorName,
		BookmarkID:         ref.ID,
		Replayed:           ref.Replayed,
		OpenedAt:           openedAt.UTC(),
		WindowStart:        f.WindowStart.UTC(),
		WindowEnd:          f.WindowEnd.UTC(),
		DriftAppliedMs:     int64(f.DriftMS),
		DriftBand:          w.request(f).Drift.Band(),
		Label:              w.request(f).Summary,
		FootageIsElsewhere: true,
		EvidenceCount:      0,
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidencefetch: encode the receipt for incident %s: %w",
			f.IncidentID, err)
	}

	obj, err := w.Store.Put(j.TenantID, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("evidencefetch: store the receipt for incident %s: %w",
			f.IncidentID, err)
	}

	// The id is derived from the outbox row's key rather than generated, so a job that is
	// retried after a crash writes the SAME row rather than a second one. `evidence`'s
	// primary key is (tenant_id, evidence_id), so a duplicate would otherwise fail the
	// insert and the retry would look like a storage fault.
	evidenceID := "evd-bookmark-" + f.IncidentID

	pool := w.pool()
	if pool == nil {
		// Reachable only when a caller supplied a FactReader that is not the pool-backed
		// one - a test, or a future reader over a cache. The bookmark is already placed at
		// this point, so the refusal has to be loud: the vendor has a pointer this product
		// cannot account for, and an unrecorded bookmark is an incident nobody can later
		// show had evidence.
		return "", errors.New("evidencefetch: the bookmark was placed but no database is " +
			"reachable to record the receipt, so the vendor holds a pointer this product " +
			"cannot account for. This is a wiring fault, not a property of the incident")
	}

	err = pool.InTenant(ctx, dbx.TenantContext{TenantID: j.TenantID, ActorID: "evidence"},
		func(tx pgx.Tx) error {
			if rowErr := w.insertRow(ctx, tx, j, p, evidenceID,
				obj.Ref, obj.SizeBytes, obj.SHA256, openedAt); rowErr != nil {
				return rowErr
			}
			return w.appendAudit(ctx, tx, j, f, ref, openedAt, ActionPlaced,
				"a bookmark was placed in the vendor's recorder and a receipt kept locally")
		})
	if err != nil {
		return "", err
	}
	return evidenceID, nil
}

// insertRow writes the `evidence` row, and is the only SQL in this file.
//
// `ON CONFLICT DO NOTHING`, because the row is the receipt's index and the receipt object
// is content-addressed by digest: a retry rewrites the same bytes to the same place, and
// making the row refuse rather than replace is what stops a second attempt from
// reassigning an `evidence_id` an operator has already opened.
const insertRowSQL = `
INSERT INTO evidence (evidence_id, tenant_id, site_id, incident_id, kind, tier,
                      observed_at, captured_at, storage_ref, media_type,
                      size_bytes, sha256, retention_policy, retain_until,
                      fetched_by_actor)
VALUES ($1,$2,$3,$4,'bookmark','A',$5,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (tenant_id, evidence_id) DO NOTHING`

// insertRow runs INSIDE the caller's transaction, so the receipt row and the ledger entry
// commit together.
//
// Invariant 1 applied to a new side effect: a receipt whose ledger entry is lost is an
// action this product performed with no record that it performed it, which is the same
// shape as the incident/enqueueVLM gap this branch closed. One transaction, both writes.
func (w *Worker) insertRow(ctx context.Context, tx pgx.Tx, j jobs.Job, p dispatch.JobPayload,
	evidenceID, ref string, size int64, sha string, observedAt time.Time) error {

	retain := observedAt.Add(w.RetainFor)
	_, err := tx.Exec(ctx, insertRowSQL,
		evidenceID, j.TenantID, j.SiteID, p.IncidentID,
		observedAt.UTC(), ref, receiptMediaType, size, sha,
		receiptRetentionPolicy, retain, Actor)
	if err != nil {
		return fmt.Errorf("evidencefetch: record the receipt row for incident %s: %w",
			p.IncidentID, err)
	}
	return nil
}

// Receipt constants.
const (
	// receiptMediaType is the document's own type, distinct from a clip's.
	//
	// Distinct so a receiver can tell "here is your video" from "here is our receipt for a
	// video you already have", which is the distinction the whole design turns on.
	receiptMediaType = "application/vnd.security.bookmark-receipt+json"
	// receiptRetentionPolicy is named rather than reusing the table default, because the
	// default says nothing about what it is for.
	receiptRetentionPolicy = "bookmark-receipt"
)

// VendorName is which vendor's recorder the bookmark is in.
//
// A constant rather than derived from the adapter, because the receipt is written by this
// worker and this worker knows which writer it called. A receipt that guessed from a
// reference format would be a guess.
const VendorName = "milestone"

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// refuse records a decision NOT to place a bookmark, and returns success.
//
// Success, deliberately. A refusal is a completed judgement, not a failure to do work: the
// clock was severe, the camera has no vendor reference, the deployment has no write-back
// surface. Retrying any of those produces the identical refusal, and a job that retries to
// exhaustion dead-letters — which reports a broken worker and sends an operator to look at
// the wrong system. The bug this branch exists to fix is an operator following an incident
// and finding nothing; a dead letter with a named reason does not leave them there, but a
// silent success with no receipt does.
//
// The refusal is written to the audit ledger and logged. Both, for the same reason: a
// refusal with no log is one nobody investigates, and a log with no ledger entry is one an
// auditor cannot find.
func (w *Worker) refuse(ctx context.Context, j jobs.Job, p dispatch.JobPayload,
	reason, why string) (Outcome, error) {

	if reason == "" {
		// A refusal that does not say which one is indistinguishable from a worker that
		// gave up, and it is the specific absence the bookmark package warns about.
		reason = "unspecified"
	}
	if w.Log != nil {
		w.Log.Warn("evidence not fetched",
			"tenant_id", j.TenantID, "site_id", j.SiteID,
			"incident_id", p.IncidentID, "job_id", j.ID, "reason", reason, "why", why)
	}
	if err := w.refusalAudit(ctx, j, p, reason); err != nil {
		return Outcome{Refusal: reason}, err
	}
	return Outcome{Refusal: reason}, nil
}

func (w *Worker) refusalAudit(ctx context.Context, j jobs.Job, p dispatch.JobPayload,
	reason string) error {

	if w.Audit == nil {
		return nil
	}
	return w.pool().InTenant(ctx, dbx.TenantContext{TenantID: j.TenantID, ActorID: "evidence"},
		func(tx pgx.Tx) error {
			_, err := w.Audit.Append(ctx, tx, audit.Entry{
				TenantID:  j.TenantID,
				Actor:     string(Actor),
				ActorType: Actor,
				Action:    ActionRefused,
				// The target is the INCIDENT, not a job. A refusal is a fact about an
				// incident that has no evidence, and an auditor reads incidents.
				TargetType: "incident",
				TargetID:   p.IncidentID,
				// The reason code is what a reader filters on, so the refusal's own
				// vocabulary goes here rather than a sentence.
				Reason: reason,
			})
			if err != nil {
				return fmt.Errorf("evidencefetch: record the refusal for incident %s: %w",
					p.IncidentID, err)
			}
			return nil
		})
}

func (w *Worker) appendAudit(ctx context.Context, tx pgx.Tx, j jobs.Job, f Facts,
	_ milestone.BookmarkRef, _ time.Time, action, reason string) error {

	if w.Audit == nil {
		return nil
	}
	_, err := w.Audit.Append(ctx, tx, audit.Entry{
		TenantID:   j.TenantID,
		Actor:      string(Actor),
		ActorType:  Actor,
		Action:     action,
		TargetType: "incident",
		TargetID:   f.IncidentID,
		Reason:     reason,
		// The receipt is the only artefact this action produced, and naming it is what
		// makes the ledger entry point at something rather than assert something.
		EvidenceRefs: []string{"evd-bookmark-" + f.IncidentID},
	})
	if err != nil {
		return fmt.Errorf("evidencefetch: append the %s entry for incident %s: %w",
			action, f.IncidentID, err)
	}
	return nil
}
