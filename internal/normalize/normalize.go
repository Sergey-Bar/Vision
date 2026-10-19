// Package normalize turns accepted inbox events into canonical events.
//
// It is the stage the plan's fault-injection tests care about most, because it
// runs after intake has committed and before anything downstream has happened.
// A crash here is the classic window for either losing an event or producing it
// twice, so the design is:
//
//   - The canonical event and the inbox transition commit in one transaction.
//     There is no state in which a canonical event exists and the inbox still
//     says received.
//   - The scoped dedupe key is taken inside that same transaction. Re-running
//     normalization for a key that already produced an event is a no-op, not a
//     second event, so crash recovery is a retry rather than a reconciliation.
//   - The event id is derived from the scoped key, not generated. Two runs over
//     the same source event must land on the same identity, or a retry after a
//     crash creates a second event under a new name.
//   - Facts are never rewritten. A new interpretation of an event is a new row,
//     which is what lets a months-old decision replay from the same stored fact.
package normalize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Sergey-Bar/vision/internal/clocksub"
	securityv1 "github.com/Sergey-Bar/vision/internal/contracts/securityv1"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/embeddings"
	"github.com/Sergey-Bar/vision/internal/inbox"
)

// SchemaVersion is the canonical event schema version this build writes. It is
// stored on every row so a months-old event can still be read by a binary that
// knows what the field meant when it was written.
const SchemaVersion = "1"

// Input is one inbox event to normalize.
type Input struct {
	Key        inbox.Key
	SiteID     string
	EventType  string
	ObservedAt time.Time
	SourceTZ   string
	ReceivedAt time.Time
	// ClockSource is what the source claimed its clock was. It is recorded
	// rather than inferred, because "the source said VMS" and "we assumed VMS"
	// are different facts about the trustworthiness of a timestamp.
	ClockSource string
	Payload     json.RawMessage
	// DeviceID and ZoneID are resolved by the caller from the source's device
	// reference. A source naming a device this platform has never registered
	// produces a record with no device, which the graph and the rules treat as
	// unattributable rather than as belonging to some default.
	DeviceID string
	ZoneID   string
	// LeaseOwner is the worker that holds the inbox lease. The inbox transition
	// matches on it, so a worker whose lease was taken away cannot complete an
	// event another worker is now processing. Status alone is not enough: a
	// reclaimed row is still status=processing.
	LeaseOwner string
}

// Result is what one normalization produced.
type Result struct {
	// AlreadyNormalized is true when this key had already produced an event.
	// Re-running is safe and produces no second row; the caller reports it so a
	// crash-recovery pass can be told apart from a first pass.
	AlreadyNormalized bool
	EventID           string
	IntegrityHash     string
	NormalizedAt      time.Time
	// UsedReceivedFallback records that the source's timestamp was unreadable
	// and received_at was used instead. It is a stored fact, not a log line,
	// because "we guessed" and "the source said so" must not look identical
	// months later.
	UsedReceivedFallback bool
	// ClockDriftMeasured says a clock measurement existed for this event's
	// source. It is the difference the schema has no column for and the doc
	// comment has been claiming since the first draft: a device measured at
	// exactly zero drift and a device never sampled used to produce
	// byte-identical normalized_at and byte-identical clock_drift_ms, so
	// nothing downstream could tell "checked, clock is fine" from "never
	// checked" and both read as a trustworthy timeline.
	ClockDriftMeasured bool
	// ClockDriftFrom names which measurement was applied: DriftFromDevice,
	// DriftFromSourceSystem, or "" when nothing was measured. A VMS-wide skew
	// applies to every event the system produced, including events that name no
	// device, so the source of the correction is itself a stored fact.
	ClockDriftFrom string
	// ClockMeasuredAt is when the applied measurement was taken. It is
	// deliberately NOT written into the stored payload: a re-measurement
	// between two normalization runs of the same source event would then change
	// the event's integrity hash, which is a worse version of the defect the
	// hash fix above is about.
	ClockMeasuredAt time.Time
}

// The two things a drift measurement can be. A device's own clock is the more
// specific measurement and wins when there is one; the source system's is the
// fallback for everything it produced.
const (
	DriftFromDevice       = "device"
	DriftFromSourceSystem = "source_system"
)

// Normalizer converts inbox rows to canonical events.
type Normalizer struct {
	Pool *dbx.Pool
	// IDGenerator overrides the derived event id. Injectable so a golden test
	// can pin ids, and so a future scheme change is one field rather than a
	// rewrite of every call site.
	IDGenerator func(key inbox.Key) string
	// embeddings writes the search row for each canonical event, inside the
	// same transaction as the event itself.
	//
	// A nil writer means the search rows are not written, and the free-text
	// search then answers "unavailable" -- which is what it did for as long as
	// the table existed and nothing wrote it. A writer with Enabled=false means
	// the database has no pgvector, and the ingest continues: the search is
	// degraded, the record is not.
	embeddings *embeddings.Writer
}

// New builds a normalizer.
func New(pool *dbx.Pool) *Normalizer {
	return &Normalizer{Pool: pool}
}

// WithEmbeddings attaches the search writer.
//
// It is an option rather than a constructor argument because the writer needs
// to know whether the database has pgvector, and that is a question about the
// database rather than about the normalizer.
func (n *Normalizer) WithEmbeddings(w *embeddings.Writer) *Normalizer {
	n.embeddings = w
	return n
}

// Normalize converts one inbox event and completes its lease in the same
// transaction.
//
// The caller must already hold the lease. Normalize refuses to run without one,
// because a normalization that is not leased can be run twice concurrently and
// the only thing standing between that and a duplicate side effect is the lease.
func (n *Normalizer) Normalize(ctx context.Context, in Input) (Result, error) {
	var result Result
	if err := validateInput(in); err != nil {
		return result, err
	}

	drift, err := n.loadDrift(ctx, in)
	if err != nil {
		return result, err
	}
	result.ClockDriftMeasured = drift.Found
	result.ClockDriftFrom = drift.From
	result.ClockMeasuredAt = drift.MeasuredAt

	normalizedAt, usedFallback := NormalizedTime(in.ObservedAt, in.ReceivedAt, drift.DriftMs)
	result.NormalizedAt = normalizedAt
	result.UsedReceivedFallback = usedFallback

	payload, err := canonicalPayload(in, drift)
	if err != nil {
		return result, err
	}
	integrity := IntegrityHash(in, normalizedAt, in.Payload)

	err = n.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: in.Key.TenantID,
		// site_isolation has been RESTRICTIVE since migration 21, so a transaction
		// that named only its tenant is refused by the WITH CHECK on the canonical
		// event it is here to insert. The RLS canary is what found this: every other
		// test connects as the superuser, which bypasses RLS entirely, so the whole
		// ingest path ran green for as long as the policy did not consult the site.
		SiteIDs: []string{in.Key.SiteID},
		ActorID: "normalizer",
	}, func(tx pgx.Tx) error {
		eventID := n.eventID(in.Key)

		// The scoped dedupe key for canonical events is taken first, exactly as
		// the inbox's is. A conflict means this source event already produced a
		// canonical event, and the correct outcome is no work.
		//
		// ON CONFLICT DO NOTHING returns *no row* rather than a zero, and this
		// used to treat that as a failure - so the branch below was unreachable:
		// every replay of an already-normalized key died here with "no rows in
		// result set", and the no-op the package doc promises never ran. The
		// inbox writer has always made the same distinction; this now does too.
		var claimed int
		err := tx.QueryRow(ctx, `
			INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, integration_id, source_event_id) DO NOTHING
			RETURNING 1`,
			in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID, eventID,
		).Scan(&claimed)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			claimed = 0
		case err != nil:
			return fmt.Errorf("normalize: claim canonical key: %w", err)
		}
		if claimed == 0 {
			// The key already produced an event. This is a retry, and a retry is
			// a no-op with a report - not an error.
			result.AlreadyNormalized = true
			result.EventID = eventID
			// The derived facts are deterministic, so they are filled in on this
			// path too. A caller comparing this run's integrity hash with the
			// stored one is comparing the same two things; leaving it empty made
			// "already normalized" indistinguishable from "produced nothing",
			// and the replay's own hash was the one value the N1 defect made
			// wrong.
			result.IntegrityHash = integrity
			return n.settleReplay(ctx, tx, in, eventID)
		}

		// observed_at is NOT NULL. A source that sent an unreadable timestamp
		// still has a receipt time, and storing the receipt keeps the column
		// honest: its own documentation says observed_at is the source's claim,
		// and UsedReceivedFallback is recorded so a reader can see it was not
		// available.
		observed := in.ObservedAt.UTC()
		if in.ObservedAt.IsZero() {
			observed = in.ReceivedAt.UTC()
		}

		var storedEventID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO canonical_events (
				tenant_id, event_id, site_id, source_system_id, source_event_id, integration_id,
				event_type, schema_version, observed_at, received_at, normalized_at, source_timezone,
				clock_source, clock_drift_ms, device_id, zone_id, correlation_id, causation_id,
				payload, vendor_payload_ref, integrity_hash
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
			RETURNING event_id`,
			in.Key.TenantID, eventID, in.SiteID, in.Key.IntegrationID,
			in.Key.SourceEventID, in.Key.IntegrationID,
			in.EventType, SchemaVersion, observed, in.ReceivedAt.UTC(), normalizedAt,
			nullString(in.SourceTZ), clockSourceOrUnknown(in.ClockSource),
			drift.DriftMs, nullString(in.DeviceID), nullString(in.ZoneID),
			correlationID(in), in.Key.SourceEventID,
			payload, vendorRef(in), integrity,
		).Scan(&storedEventID); err != nil {
			return fmt.Errorf("normalize: insert canonical event: %w", err)
		}
		result.EventID = storedEventID
		result.IntegrityHash = integrity
		// The search row, in the SAME transaction as the event.
		//
		// Inside the transaction is the point: there is no window in which the
		// event exists and its search row does not, and none in which a search
		// row exists without its event. A write after the commit would have
		// both: a crash between the two would leave an event the search could
		// never find, and the search would report a quiet record.
		if err := n.embeddings.Write(ctx, tx, in.Key.TenantID, storedEventID, embeddings.Document(
			in.EventType, in.DeviceID, in.ZoneID, labelOf(in))); err != nil {
			return err
		}
		return n.completeInbox(ctx, tx, in, storedEventID)
	})
	return result, err
}

// labelOf reads a human-readable label out of an event's payload.
//
// It is a best effort on purpose, and it never fails the event: the label is
// what makes an event findable by a word, and an event whose payload has no
// label is still an event. The fields it looks for are the ones a source
// actually uses for a name, and the first one that is a non-empty string wins.
func labelOf(in Input) string {
	if len(in.Payload) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"label", "className", "class_name", "class", "title", "name", "objectType"} {
		if v, ok := payload[key].(string); ok {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
	}
	// A nested analyticData object carries the class name, which is the label
	// that matters for a VMS's own analytics.
	if analytic, ok := payload["analyticData"].(map[string]any); ok {
		if object, ok := analytic["object"].(map[string]any); ok {
			if value, ok := object["value"].(map[string]any); ok {
				if v, ok := value["className"].(string); ok {
					if v = strings.TrimSpace(v); v != "" {
						return v
					}
				}
			}
		}
	}
	return ""
}

// completeInbox is the only place an inbox event reaches its terminal state on
// the happy path. It is inside the same transaction as the canonical event, so
// the two facts cannot disagree.
func (n *Normalizer) completeInbox(ctx context.Context, tx pgx.Tx, in Input, eventID string) error {
	// The lease owner is part of the match, not just the status. A reclaimed row
	// is still status=processing, so matching on status alone would let a worker
	// whose lease expired mark an event complete while the worker that reclaimed
	// it is midway through the same work.
	tag, err := tx.Exec(ctx, `
		UPDATE inbox_events
		   SET status = 'processed', completed_at = now(),
		       canonical_event_id = $5,
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
		   AND status = 'processing'
		   AND lease_owner IS NOT DISTINCT FROM $4`,
		in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID, in.LeaseOwner, eventID)
	if err != nil {
		return fmt.Errorf("normalize: complete inbox event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// The lease moved on while this transaction ran, so another worker may
		// be doing the same work right now. Rolling back here is what stops two
		// workers from both writing. The loser retries and finds the key already
		// claimed, which is a no-op.
		return fmt.Errorf("normalize: inbox lease on %s/%s is no longer held by %q; "+
			"rolling back so the worker that now holds it can finish",
			in.Key.IntegrationID, in.Key.SourceEventID, in.LeaseOwner)
	}
	return nil
}

// settleReplay is what a repeat normalization does to the inbox row.
//
// The defect this replaces: the already-normalized branch called completeInbox,
// which requires status='processing' AND lease_owner = ours. On a row a prior
// attempt already drove to 'processed' - where lease_owner is NULL, because
// completing a row releases the lease - that match found no row, the function
// returned an error, and the whole transaction rolled back. So the package
// documented at the top of this file ("re-running normalization for a key that
// already produced an event is a no-op, not a second event, so crash recovery is
// a retry rather than a reconciliation") was false: it was an error. The
// consequence was at the call site, in pipeline.process, which checks err before
// result.AlreadyNormalized - a replay of a fully processed event was routed to
// inbox.Fail and eventually dead-lettered, with its canonical event, its edges
// and its incident all present. Invariant 1 says a row reaches a terminal state
// unless explicitly dead-lettered; a row that was successfully processed must
// not end up dead-lettered as collateral of a retry.
//
// So the three states are separated here:
//
//   - still claimable by us (status='processing', lease ours): complete it. The
//     key existed without this worker having finished, which is what a crash
//     between the key claim and the inbox update leaves.
//   - already terminal ('processed', or 'dead_letter' from some earlier attempt):
//     no work. The lease is not ours and is not required, because there is
//     nothing left to transition.
//   - 'processing' under somebody else's lease, or any non-terminal status: an
//     error, so the caller retries and the row is never left claimable-and-skipped
//     for ever, which is how an accepted event would be lost.
func (n *Normalizer) settleReplay(ctx context.Context, tx pgx.Tx, in Input, eventID string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE inbox_events
		   SET status = 'processed', completed_at = now(),
		       canonical_event_id = $5,
		       lease_owner = NULL, lease_expires_at = NULL, last_error = NULL
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3
		   AND status = 'processing'
		   AND lease_owner IS NOT DISTINCT FROM $4`,
		in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID, in.LeaseOwner, eventID)
	if err != nil {
		return fmt.Errorf("normalize: complete inbox event: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var status string
	err = tx.QueryRow(ctx, `
		SELECT status FROM inbox_events
		 WHERE tenant_id = $1 AND integration_id = $2 AND source_event_id = $3`,
		in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		// The key exists but the inbox row does not. The two are written in one
		// transaction, so this state is not reachable from this code; if it is
		// ever observed the row is gone and silence would hide it.
		return fmt.Errorf("normalize: %s/%s has a canonical event but no inbox row; "+
			"refusing to report a successful replay", in.Key.IntegrationID, in.Key.SourceEventID)
	}
	if err != nil {
		return fmt.Errorf("normalize: read the inbox state of a replayed event: %w", err)
	}

	switch inbox.Status(status) {
	case inbox.StatusProcessed, inbox.StatusDeadLetter:
		// Terminal, and the canonical event is behind it. A dead letter here is
		// left as it is: it is the stored record of an attempt that failed for
		// some other reason, and un-dead-lettering it from a replay would
		// rewrite a fact rather than finish a job.
		return nil
	case inbox.StatusProcessing:
		// Claimable, by somebody else. Rolling back is what stops two workers
		// from both writing; the loser retries and finds the key claimed.
		return fmt.Errorf("normalize: inbox lease on %s/%s is no longer held by %q; "+
			"rolling back so the worker that now holds it can finish",
			in.Key.IntegrationID, in.Key.SourceEventID, in.LeaseOwner)
	default:
		// 'received' or 'failed': not claimable by us, not terminal. Returning
		// nil here would leave the row to be re-claimed, hit this branch again
		// and return nil again, for ever - an accepted event that never reaches
		// a terminal state. An error puts it back on the retry path instead.
		return fmt.Errorf("normalize: %s/%s is %q while its canonical event already exists; "+
			"it is neither claimable by %q nor terminal, so no-op here would strand it",
			in.Key.IntegrationID, in.Key.SourceEventID, status, in.LeaseOwner)
	}
}

func validateInput(in Input) error {
	switch {
	case in.Key.TenantID == "":
		return errors.New("normalize: tenant id is required and is never read from a request body")
	case in.Key.IntegrationID == "":
		return errors.New("normalize: integration id is required")
	case in.Key.SourceEventID == "":
		return errors.New("normalize: source event id is required; without it the event cannot be deduplicated")
	case in.SiteID == "":
		return errors.New("normalize: site id is required; a canonical event is physically site-bound")
	case in.EventType == "":
		return errors.New("normalize: event type is required")
	case len(in.Payload) == 0:
		return errors.New("normalize: payload is required")
	case in.ReceivedAt.IsZero():
		return errors.New("normalize: received at is required; it is the partition key and cannot be invented")
	}
	return nil
}

// driftReading is one clock measurement, and whether it existed at all.
type driftReading struct {
	DriftMs int32
	// Found separates "measured, and the measurement was zero" from "never
	// measured". loadDrift used to return only the value, so both were the int32
	// zero and the difference existed only in a doc comment.
	Found bool
	// From is DriftFromDevice, DriftFromSourceSystem, or "" when nothing was
	// measured.
	From string
	// MeasuredAt is when the measurement was taken, from device_clocks.
	MeasuredAt time.Time
}

// loadDrift reads the clock drift that applies to this event, and says whether a
// measurement existed at all.
//
// A device with no measurement yields zero with Found=false, and the caller
// applies no correction. That is deliberately different from a device measured at
// exactly zero drift: the first has never been checked and the second has been
// checked and is fine, and the stored row now says which one it was.
//
// When the device itself has never been measured, the source system's
// measurement is used instead. That is the VMS case: the whole system is the
// clock, so its drift is the drift of every event it produced, including events
// naming no device. It was measured and stored by clocksub under a reserved id
// and read by nobody - a VMS 40 seconds fast produced 40 seconds of uncorrected
// normalized_at with no degradation recorded anywhere, which is the exact
// failure the clocksub package exists to prevent. The device's own measurement
// wins where there is one: it is the more specific measurement, and applying
// both would count the same skew twice.
func (n *Normalizer) loadDrift(ctx context.Context, in Input) (driftReading, error) {
	if in.DeviceID != "" {
		device, err := n.readDrift(ctx, in.Key.TenantID, in.Key.SiteID, in.DeviceID)
		if err != nil {
			return driftReading{}, err
		}
		if device.Found {
			device.From = DriftFromDevice
			return device, nil
		}
	}
	source, err := n.readDrift(ctx, in.Key.TenantID, in.Key.SiteID,
		clocksub.SourceClockDeviceID(in.Key.IntegrationID))
	if err != nil {
		return driftReading{}, err
	}
	if source.Found {
		source.From = DriftFromSourceSystem
	}
	return source, nil
}

// readDrift reads one device_clocks row, or Found=false when there is none.
//
// The read is scoped to the site as well as the tenant. `device_clocks` is a
// site-scoped table and the RLS policy meant to enforce that is PERMISSIVE — it
// is ORed away by `tenant_isolation` — so the site constraint does not exist at
// the database layer. Binding it here means the statement is correct on its own
// terms and needs no change when the policy becomes RESTRICTIVE.
func (n *Normalizer) readDrift(ctx context.Context, tenant, site, deviceID string) (driftReading, error) {
	var d driftReading
	scope := n.Pool.WithSites(tenant, "normalizer", []string{site})
	err := scope.QueryRow(ctx, `
		SELECT drift_ms, measured_at FROM device_clocks WHERE tenant_id = $1 AND device_id = $2`,
		tenant, deviceID).Scan(&d.DriftMs, &d.MeasuredAt)
	if err != nil {
		// Only "not measured yet" is benign. Swallowing every error would turn a
		// dropped connection, a cancelled context or a permission failure into
		// "no drift", and that removes the clock correction silently — the
		// correlation degrades and nothing says why.
		if errors.Is(err, pgx.ErrNoRows) {
			// No measurement is not an error. The device may not have been
			// sampled yet, and inventing a drift value would be worse than
			// recording none.
			return driftReading{}, nil
		}
		return driftReading{}, fmt.Errorf("normalize: read the device clock: %w", err)
	}
	d.Found = true
	d.MeasuredAt = d.MeasuredAt.UTC()
	return d, nil
}

// NormalizedTime applies a device's measured drift to the source's claim.
//
// A zero observed time is a real case, not an error: a source that sent an
// unreadable timestamp produced a record with no trustworthy observation, and the
// fallback to received_at must be visible rather than silent.
func NormalizedTime(observed, received time.Time, driftMs int32) (normalized time.Time, usedFallback bool) {
	if observed.IsZero() {
		if received.IsZero() {
			return time.Time{}, true
		}
		return received.UTC(), true
	}
	if driftMs == 0 {
		return observed.UTC(), false
	}
	return observed.UTC().Add(-time.Duration(driftMs) * time.Millisecond), false
}

func (n *Normalizer) eventID(key inbox.Key) string {
	if n.IDGenerator != nil {
		return n.IDGenerator(key)
	}
	return DeriveEventID(key)
}

// DeriveEventID computes an event id from the scoped source key.
//
// Derived, not random, and this is load-bearing: two runs over the same source
// event - a retry after a crash, a replay of a fixture, a rebuild of a derived
// projection - must land on the same identity. A generated id would make each
// attempt a different event, and crash recovery a reconciliation problem rather
// than a retry.
func DeriveEventID(key inbox.Key) string {
	sum := sha256.Sum256([]byte(key.TenantID + "\x00" + key.IntegrationID + "\x00" + key.SourceEventID))
	return "evt_" + hex.EncodeToString(sum[:12])
}

func canonicalPayload(in Input, drift driftReading) (json.RawMessage, error) {
	// The vendor envelope is carried inside the canonical payload rather than
	// beside it, so a reader has the source's own words without a second join.
	var raw any
	if err := json.Unmarshal(in.Payload, &raw); err != nil {
		return nil, fmt.Errorf("normalize: payload is not JSON: %w", err)
	}
	canonical := map[string]any{
		"eventType":      in.EventType,
		"observedAt":     isoOrEmpty(in.ObservedAt),
		"receivedAt":     isoOrEmpty(in.ReceivedAt),
		"sourceTimezone": in.SourceTZ,
		"clockSource":    clockSourceOrUnknown(in.ClockSource),
		// The clock correction is a stored fact and so is the provenance of the
		// correction, and with it the absence of one. Without these two keys a
		// device measured at exactly zero drift and a device never sampled
		// produced byte-identical rows, and every downstream reader - the
		// correlator, the console, a months-late audit - read both as a clock
		// that had been checked and found fine.
		"clockDriftMeasured": drift.Found,
		"clockDriftFrom":     drift.From,
	}
	if in.DeviceID == "" {
		// Marked, not defaulted. A rule that requires a device must refuse this
		// event rather than silently correlate it against nothing.
		canonical["unregisteredDevice"] = true
	}
	doc := map[string]any{"vendor": raw, "canonical": canonical}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("normalize: encode canonical payload: %w", err)
	}
	return out, nil
}

// IntegrityHash is the SHA-256 over the canonical serialization of the event.
//
// It covers the fields that define the event and deliberately not the receipt
// time: two runs over the same source event at different wall-clock instants
// must produce the same hash, or the determinism the golden tests depend on is
// only apparent.
//
// It used to be the other way round. This function enumerated the covered fields
// and then finished with h.Write(payload), where payload was the whole
// canonical document - whose canonical map contains "receivedAt". So the receipt
// time was in the hashed bytes by the back door, and the doc comment above was
// false: the same source event normalized at two instants produced two
// integrity hashes, which defeats hash comparison as a replay or dedupe check
// and makes golden-fixture byte-stability unachievable.
//
// The envelope was the mistake, and there are two ways to remove it. The first
// is to drop receivedAt from the stored canonical map, which would fix the hash
// by deleting a real stored fact - the receipt time is a column on the row and
// belongs in the document a reader consults. The second, taken here, is to stop
// hashing our own serialization and hash the vendor's bytes instead: the
// sanitized envelope is stored verbatim in source_events, so hashing it ties the
// row to exactly the bytes it was derived from, keeps the receipt time out, and
// stops the hash from moving every time a field is added to the canonical map -
// which is how this defect happened.
func IntegrityHash(in Input, normalizedAt time.Time, vendorEnvelope []byte) string {
	h := sha256.New()
	for _, part := range []string{
		in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID,
		in.SiteID, in.EventType, SchemaVersion,
		isoOrEmpty(in.ObservedAt), normalizedAt.UTC().Format(time.RFC3339Nano),
		in.SourceTZ, in.DeviceID, in.ZoneID, clockSourceOrUnknown(in.ClockSource),
	} {
		_, _ = fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	// Length-prefixed for the same reason the fields are: a byte stream that is
	// the concatenation of two different documents must not collide with the
	// stream that is one.
	_, _ = fmt.Fprintf(h, "envelope:%d:", len(vendorEnvelope))
	h.Write(vendorEnvelope)
	return hex.EncodeToString(h.Sum(nil))
}

func clockSourceOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	switch s {
	case "ntp", "vms", "host":
		return s
	}
	return "unknown"
}

func correlationID(in Input) string {
	// Correlation groups the events one physical occurrence produced, across
	// cameras and devices. The source's own event id is the best available
	// grouping key in M1; a site needing richer grouping supplies a device
	// reference alongside it.
	if in.DeviceID == "" {
		return in.Key.SourceEventID
	}
	return in.DeviceID + ":" + in.Key.SourceEventID
}

func vendorRef(in Input) string {
	if in.Key.IntegrationID == "" || in.Key.SourceEventID == "" {
		return ""
	}
	// A pointer to the immutable sanitized envelope, not a copy. The bytes live
	// once in source_events; duplicating them into every canonical event is how a
	// large deployment fills a disk.
	return fmt.Sprintf("source_events/%s/%s/%s",
		in.Key.IntegrationID, in.Key.TenantID, in.Key.SourceEventID)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func isoOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// ToProto converts a normalized event into its contract type.
//
// This exists so the contract is not decorative: the console's JSON is the
// protobuf's JSON, and a test asserts the two agree. A second parallel Go struct
// for the same event is how a wire contract quietly stops matching the thing it
// describes.
func ToProto(r Result, in Input) (*securityv1.Event, error) {
	var doc map[string]any
	if err := json.Unmarshal(in.Payload, &doc); err != nil {
		return nil, fmt.Errorf("normalize: payload is not JSON: %w", err)
	}
	payload, err := structpb.NewStruct(doc)
	if err != nil {
		return nil, fmt.Errorf("normalize: build payload struct: %w", err)
	}

	return &securityv1.Event{
		EventId:          r.EventID,
		TenantId:         in.Key.TenantID,
		SiteId:           in.SiteID,
		SourceSystemId:   in.Key.IntegrationID,
		SourceEventId:    in.Key.SourceEventID,
		IntegrationId:    in.Key.IntegrationID,
		EventType:        in.EventType,
		SchemaVersion:    SchemaVersion,
		ObservedAt:       timestamppbOrNil(in.ObservedAt),
		ReceivedAt:       timestamppbOrNil(in.ReceivedAt),
		NormalizedAt:     timestamppbOrNil(r.NormalizedAt),
		SourceTimezone:   in.SourceTZ,
		ClockSource:      clockSourceEnum(in.ClockSource),
		DeviceId:         in.DeviceID,
		ZoneId:           in.ZoneID,
		CorrelationId:    correlationID(in),
		CausationId:      in.Key.SourceEventID,
		Payload:          payload,
		VendorPayloadRef: vendorRef(in),
		IntegrityHash:    r.IntegrityHash,
	}, nil
}

func timestamppbOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t.UTC())
}

func clockSourceEnum(s string) securityv1.ClockSource {
	switch s {
	case "ntp":
		return securityv1.ClockSource_CLOCK_SOURCE_NTP
	case "vms":
		return securityv1.ClockSource_CLOCK_SOURCE_VMS
	case "host":
		return securityv1.ClockSource_CLOCK_SOURCE_HOST
	default:
		return securityv1.ClockSource_CLOCK_SOURCE_UNKNOWN
	}
}
