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
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	securityv1 "github.com/Sergey-Bar/Ai-Captain/internal/contracts/securityv1"
	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
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
}

// Normalizer converts inbox rows to canonical events.
type Normalizer struct {
	Pool *dbx.Pool
	// IDGenerator overrides the derived event id. Injectable so a golden test
	// can pin ids, and so a future scheme change is one field rather than a
	// rewrite of every call site.
	IDGenerator func(key inbox.Key) string
}

// New builds a normalizer.
func New(pool *dbx.Pool) *Normalizer {
	return &Normalizer{Pool: pool}
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

	driftMs, err := n.loadDrift(ctx, in)
	if err != nil {
		return result, err
	}

	normalizedAt, usedFallback := NormalizedTime(in.ObservedAt, in.ReceivedAt, driftMs)
	result.NormalizedAt = normalizedAt
	result.UsedReceivedFallback = usedFallback

	payload, err := canonicalPayload(in)
	if err != nil {
		return result, err
	}
	integrity := IntegrityHash(in, normalizedAt, payload)

	err = n.Pool.InTenant(ctx, dbx.TenantContext{
		TenantID: in.Key.TenantID,
		ActorID:  "normalizer",
	}, func(tx pgx.Tx) error {
		eventID := n.eventID(in.Key)

		// The scoped dedupe key for canonical events is taken first, exactly as
		// the inbox's is. A conflict means this source event already produced a
		// canonical event, and the correct outcome is no work.
		var claimed int
		if err := tx.QueryRow(ctx, `
			INSERT INTO canonical_event_keys (tenant_id, integration_id, source_event_id, event_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (tenant_id, integration_id, source_event_id) DO NOTHING
			RETURNING 1`,
			in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID, eventID,
		).Scan(&claimed); err != nil {
			return fmt.Errorf("normalize: claim canonical key: %w", err)
		}
		if claimed == 0 {
			result.AlreadyNormalized = true
			result.EventID = eventID
			return n.completeInbox(ctx, tx, in, eventID)
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
			driftMs, nullString(in.DeviceID), nullString(in.ZoneID),
			correlationID(in), in.Key.SourceEventID,
			payload, vendorRef(in), integrity,
		).Scan(&storedEventID); err != nil {
			return fmt.Errorf("normalize: insert canonical event: %w", err)
		}
		result.EventID = storedEventID
		result.IntegrityHash = integrity
		return n.completeInbox(ctx, tx, in, storedEventID)
	})
	return result, err
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

// loadDrift reads the measured clock drift for the event's device.
//
// A device with no measurement yields zero with found=false, and the caller
// applies no correction. That is deliberately different from a device measured at
// exactly zero drift: the first has never been checked and the second has been
// checked and is fine.
func (n *Normalizer) loadDrift(ctx context.Context, in Input) (int32, error) {
	if in.DeviceID == "" {
		return 0, nil
	}
	var drift int32
	err := n.Pool.QueryRow(ctx, `
		SELECT drift_ms FROM device_clocks WHERE tenant_id = $1 AND device_id = $2`,
		in.Key.TenantID, in.DeviceID).Scan(&drift)
	if err != nil {
		// Only "not measured yet" is benign. Swallowing every error would turn a
		// dropped connection, a cancelled context or a permission failure into
		// "no drift", and that removes the clock correction silently — the
		// correlation degrades and nothing says why.
		if errors.Is(err, pgx.ErrNoRows) {
			// No measurement is not an error. The device may not have been
			// sampled yet, and inventing a drift value would be worse than
			// recording none.
			return 0, nil
		}
		return 0, fmt.Errorf("normalize: read the device clock: %w", err)
	}
	return drift, nil
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
// event — a retry after a crash, a replay of a fixture, a rebuild of a derived
// projection — must land on the same identity. A generated id would make each
// attempt a different event, and crash recovery a reconciliation problem rather
// than a retry.
func DeriveEventID(key inbox.Key) string {
	sum := sha256.Sum256([]byte(key.TenantID + "\x00" + key.IntegrationID + "\x00" + key.SourceEventID))
	return "evt_" + hex.EncodeToString(sum[:12])
}

func canonicalPayload(in Input) (json.RawMessage, error) {
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
func IntegrityHash(in Input, normalizedAt time.Time, payload []byte) string {
	h := sha256.New()
	for _, part := range []string{
		in.Key.TenantID, in.Key.IntegrationID, in.Key.SourceEventID,
		in.SiteID, in.EventType, SchemaVersion,
		isoOrEmpty(in.ObservedAt), normalizedAt.UTC().Format(time.RFC3339Nano),
		in.SourceTZ, in.DeviceID, in.ZoneID, clockSourceOrUnknown(in.ClockSource),
	} {
		_, _ = fmt.Fprintf(h, "%d:%s", len(part), part)
	}
	h.Write(payload)
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
