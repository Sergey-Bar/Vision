package evidencefetch

// The fetch path: the bytes, not only the pointer.
//
// # Why this exists beside the bookmark
//
// The bookmark is the design: the vendor keeps the recording and the operator opens the
// vendor's player. But an incident's evidence is not always the vendor's to give — a
// camera whose archive has rolled, a site whose retention is shorter than the
// investigation, a partner who wants the packet for their own insurer — and "we pointed
// at it" is not an answer to "what did you keep".
//
// So where a deployment configures a media surface, this worker ALSO takes the bytes for
// the window it just bookmarked, stores them in the evidence store like any other
// evidence, and records an `evidence.fetched` row. The bookmark is unaffected: the
// pointer is still there, the receipt still says the footage is elsewhere, and a site
// without a media surface behaves exactly as it did before.
//
// # The probe runs before the fetch
//
// Every documented build of the vendor's media APIs refuses a request for time the
// recording does not cover. A worker that fetched without probing would dead-letter on
// every gap — which is a broken worker, not a gap in a recording. The probe answers
// first, the fetch asks for what the probe found, and a probe that found nothing is a
// gap this product records rather than a failure it retries.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// actionFetched is bytes taken from the vendor's recording into our own store.
//
// A separate action from `evidence.bookmark.placed` because the two are different facts
// about the world: one says the vendor holds a pointer, the other says we hold bytes.
// An auditor asking "what does this product store" is asking the second question, and a
// ledger that answered it with the first would be wrong in the direction that matters.
const actionFetched = "evidence.fetched"

// Fetched row constants. The media type is the vendor's, defaulted honestly: a clip
// from this API is an MP4, and a receiver that wants to know more asks the row's own
// `media_type`, which is copied from the fetch rather than from this constant.
const (
	fetchedClipMediaType = "video/mp4"
	fetchedRetention     = "fetched-media"
)

// fetch attempts the bytes for the window the bookmark was placed over.
//
// It is called only when `Media` is configured. It returns an empty id when there was
// nothing to fetch, which is not an error: the probe found nothing, and a gap in a
// recording is a fact this product records rather than a failure it retries.
//
// The ordering is object-then-row, the same one the receipt uses and for the same
// reason: a blob store is a filesystem and a transaction is a database, there is no
// two-phase commit across the two, and the failure mode worth having is bytes nobody
// points at rather than a row pointing at bytes that were never written.
func (w *Worker) fetch(ctx context.Context, j jobs.Job, f Facts, openedAt time.Time) (string, error) {
	if w.Media == nil || w.Store == nil {
		return "", nil
	}
	from, to := w.fetchWindow(f)
	probe, err := w.Media.ProbeRange(ctx, f.SiteRef, from, to)
	if err != nil {
		// A probe failure is a RETRY, not a refusal: the vendor may be down. A dead
		// letter here would report a broken worker for a link that was down.
		return "", fmt.Errorf("evidencefetch: probe the vendor's recording for incident %s: %w",
			f.IncidentID, err)
	}
	if !probe.Contains(openedAt) {
		// The instant the bookmark opens at is not in a recording the vendor holds.
		// The bookmark still stands — it points at a second that may come back when an
		// archive is restored — so the fetch simply does not happen.
		return "", nil
	}

	clip, err := w.Media.FetchClip(ctx, f.SiteRef, from, to)
	if err != nil {
		return "", fmt.Errorf("evidencefetch: fetch the clip for incident %s: %w", f.IncidentID, err)
	}
	if closer, ok := clip.Reader.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	obj, err := w.Store.Put(j.TenantID, clip.Reader)
	if err != nil {
		return "", fmt.Errorf("evidencefetch: store the clip for incident %s: %w", f.IncidentID, err)
	}

	// The id is derived like the receipt's is, so a retry after a crash writes the SAME
	// row: `evidence`'s primary key is (tenant_id, evidence_id) and a second insert would
	// fail as a storage fault, which is the wrong report for a retry.
	evidenceID := "evd-clip-" + f.IncidentID
	pool := w.pool()
	if pool == nil {
		return "", fmt.Errorf("evidencefetch: the clip for incident %s was stored but no "+
			"database is reachable to record it. This is a wiring fault", f.IncidentID)
	}

	mediaType := clip.ContentType
	if mediaType == "" {
		mediaType = fetchedClipMediaType
	}
	rec, err := w.Store.Put(j.TenantID, bytes.NewReader(mustJSON(fetchRecord{
		IncidentID:  f.IncidentID,
		Vendor:      VendorName,
		CameraRef:   f.SiteRef,
		Kind:        clip.Kind,
		MediaType:   mediaType,
		SizeBytes:   obj.SizeBytes,
		CoveredFrom: clip.Range.From,
		CoveredTo:   clip.Range.To,
		ProbeSource: probe.Source,
		OpenedAt:    openedAt.UTC(),
	})))
	if err != nil {
		return "", fmt.Errorf("evidencefetch: store the fetch record for incident %s: %w",
			f.IncidentID, err)
	}

	err = pool.InTenant(ctx, dbx.TenantContext{TenantID: j.TenantID, ActorID: "evidence"},
		func(tx pgx.Tx) error {
			if err := w.insertFetchedRow(ctx, tx, j, f, evidenceID, obj, mediaType, clip, openedAt); err != nil {
				return err
			}
			return w.appendFetchedAudit(ctx, tx, j, f, evidenceID, rec, clip)
		})
	if err != nil {
		return "", err
	}
	return evidenceID, nil
}

// fetchWindow is the window the fetch asks for: the incident's own, opened by the
// configured pre/post roll and corrected by the drift the bookmark was corrected by.
//
// It is derived rather than passed in so the fetch and the bookmark cannot disagree
// about which seconds they are talking about, and it is a method on Worker so the
// rolls come from configuration rather than from a caller.
func (w *Worker) fetchWindow(f Facts) (time.Time, time.Time) {
	drift := time.Duration(f.DriftMS) * time.Millisecond
	return f.WindowStart.Add(-w.PreRoll).Add(drift).UTC(),
		f.WindowEnd.Add(w.PostRoll).Add(drift).UTC()
}

// fetchRecord is the small document kept beside the clip: what was fetched, from where,
// and what the probe said was available.
//
// It exists because the evidence row's own columns cannot hold the answer: a row says
// bytes exist at a ref, and the question an auditor asks six months later is which
// window those bytes actually covered — which is the fetch's answer and not the ask's.
type fetchRecord struct {
	IncidentID  string    `json:"incident_id"`
	Vendor      string    `json:"vendor"`
	CameraRef   string    `json:"camera_ref"`
	Kind        string    `json:"kind"`
	MediaType   string    `json:"media_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CoveredFrom time.Time `json:"covered_from"`
	CoveredTo   time.Time `json:"covered_to"`
	ProbeSource string    `json:"probe_source"`
	OpenedAt    time.Time `json:"opened_at"`
}

func mustJSON(v any) []byte {
	body, err := json.Marshal(v)
	if err != nil {
		// The record is all strings, ints and times. A marshal failure here is a
		// programming error, and panicking in a worker would take the process down — so
		// the bytes degrade to the empty object rather than to a crash.
		return []byte("{}")
	}
	return body
}

const insertFetchedRowSQL = `
INSERT INTO evidence (evidence_id, tenant_id, site_id, incident_id, kind, tier,
                      observed_at, captured_at, storage_ref, media_type,
                      size_bytes, sha256, retention_policy, retain_until,
                      fetched_by_actor)
VALUES ($1,$2,$3,$4,$5,'A',$6,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (tenant_id, evidence_id) DO NOTHING`

func (w *Worker) insertFetchedRow(ctx context.Context, tx pgx.Tx, j jobs.Job, f Facts,
	evidenceID string, obj evidence.Object, mediaType string, clip milestone.MediaClip,
	observedAt time.Time) error {

	kind := clip.Kind
	if kind == "" {
		kind = milestone.MediaClipKind
	}
	retain := observedAt.Add(w.RetainFor)
	_, err := tx.Exec(ctx, insertFetchedRowSQL,
		evidenceID, j.TenantID, j.SiteID, f.IncidentID, kind,
		observedAt.UTC(), obj.Ref, mediaType, obj.SizeBytes, obj.SHA256,
		fetchedRetention, retain, Actor)
	if err != nil {
		return fmt.Errorf("evidencefetch: record the fetched clip for incident %s: %w",
			f.IncidentID, err)
	}
	return nil
}

// appendFetchedAudit records the fetch in the ledger, inside the caller's transaction
// with the row.
//
// Invariant 1 applied to a second side effect: bytes stored with no record that they
// were stored is an action this product performed and cannot account for.
func (w *Worker) appendFetchedAudit(ctx context.Context, tx pgx.Tx, j jobs.Job, f Facts,
	evidenceID string, rec evidence.Object, clip milestone.MediaClip) error {

	if w.Audit == nil {
		return nil
	}
	_, err := w.Audit.Append(ctx, tx, audit.Entry{
		TenantID:   j.TenantID,
		Actor:      string(Actor),
		ActorType:  Actor,
		Action:     actionFetched,
		TargetType: "incident",
		TargetID:   f.IncidentID,
		Reason: fmt.Sprintf("fetched %d %s bytes covering %s..%s", clip.Size, clip.Kind,
			clip.Range.From.UTC().Format(time.RFC3339), clip.Range.To.UTC().Format(time.RFC3339)),
		// Both refs: the clip and the record beside it. An entry that named only one
		// would leave a reader unable to find the other.
		EvidenceRefs: []string{evidenceID, rec.Ref},
	})
	if err != nil {
		return fmt.Errorf("evidencefetch: append the %s entry for incident %s: %w",
			actionFetched, f.IncidentID, err)
	}
	return nil
}
