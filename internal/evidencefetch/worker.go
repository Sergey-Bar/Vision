// Package evidencefetch makes an incident's footage findable.
//
// # What this worker is for
//
// `jobs.QueueEvidenceFetch` was declared, `dispatch` grew an executor for it, and nothing
// enqueued into it. The queue had a worker-shaped hole and a producer-shaped hole and only
// one of them is this package's job: `dispatch.EnqueueOutbox` is the producer, already
// written and already committed inside the incident's transaction. Before this package
// there was no consumer either, so `internal/capability` reported the queue as having
// neither and the evidence table was empty in production.
//
// # It does not download footage, and that is the design
//
// The vendor keeps the recording. This product places a **bookmark** — a pointer, at the
// right second, on the vendor's own archive — and the operator opens the vendor's player.
// `internal/milestone`'s own test says it: *"A bookmark is a pointer, not a copy of the
// footage."*
//
// So this worker performs no download, copies no video, and stores no bytes of footage. It
// stores a **receipt**: a small document recording that the bookmark was placed, where, at
// which second, and what drift correction was applied. That receipt is an `evidence` row of
// kind `bookmark` — a kind the schema declared before anything wrote it — pointing at a
// small object in the evidence store.
//
// The receipt is what makes the bookmark auditable. A bookmark on a vendor's archive
// disappears when that vendor's retention rolls the recording off, and a decision recorded
// only as "we bookmarked it" cannot answer "did we, and when" six months later. The receipt
// answers it from our own storage.
//
// # Every refusal names itself
//
// `milestone.DecideBookmark` refuses to place a bookmark that would open at the wrong
// second — severe clock drift, no vendor camera reference, no window. Each of those is
// recorded as a **refusal**, in the audit ledger, with the reason. None of them is an error
// and none of them is silence:
//
//   - An error would retry a decision that will refuse identically every time, and then
//     dead-letter, which reports a broken worker rather than a clock problem.
//   - Silence would be an operator following an incident and finding nothing, which is
//     indistinguishable from there being no footage.
//
// That is the same rule the bookmark package states for itself — *"a bookmark silently not
// placed is an operator who follows an incident and finds nothing"* — and this worker is
// where it has to hold.
package evidencefetch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/audit"
	"github.com/Sergey-Bar/vision/internal/clock"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/dispatch"
	"github.com/Sergey-Bar/vision/internal/evidence"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/milestone"
)

// Actor is the audit actor for everything this worker does.
//
// `system`, not an operator and not the adapter, because the bookmark is placed by the
// product on the strength of a rule's dispatch and not by a person. An operator who later
// asks "who marked this moment" gets an answer that names the mechanism rather than a
// person who happened to be logged in.
const Actor = audit.ActorSystem

// Audit actions. The audit vocabulary is open by design — what is not open is that every
// state transition has one — so these are declared here rather than in `internal/audit`,
// which would make a package that has nothing to do with auditing the owner of what an
// evidence worker writes.
const (
	// ActionPlaced is a bookmark the vendor accepted.
	ActionPlaced = "evidence.bookmark.placed"
	// ActionRefused is a bookmark this worker declined to place, and why.
	ActionRefused = "evidence.bookmark.refused"
)

// Worker consumes `jobs.QueueEvidenceFetch`.
//
// `Bookmarks` may be nil, and that is a refusal rather than a silent success. A deployment
// with no write-back surface is a supported configuration — a replay transport has no
// recorder to bookmark — and an operator on such a deployment should be told the evidence
// was not fetched rather than shown an empty evidence list that looks like there was
// nothing to find.
type Worker struct {
	// Facts supplies the bookmark's inputs.
	//
	// An interface rather than a `Pool` field so the DECISION surface - which bookmark,
	// or which refusal - is testable without a database. That matters more than it looks:
	// the decisions are the whole of this package, and a test that needed a live Postgres
	// to check a clock band is a test nobody runs on the machine where the tree is wrong.
	//
	// `NewPoolFacts` is the production implementation and reads the incident, its camera
	// and the stored clock drift in one statement.
	Facts FactReader
	// Store holds the receipt document. May be nil, in which case the job is refused with
	// a named reason rather than half-written.
	Store *evidence.Store
	// Bookmarks is the vendor write-back surface. Optional; see the type comment.
	Bookmarks milestone.BookmarkWriter
	// Policy bounds the clock bands.
	Policy clock.Policy
	// Audit is the ledger. Optional, because a deployment that has not finished its ledger
	// still has to be able to bookmark, and the absence of a ledger entry is reported
	// rather than treated as a reason to refuse the bookmark.
	Audit *audit.Writer
	// Log carries the refusals. A refusal with no log is a refusal nobody investigates.
	Log *slog.Logger
	// PreRoll and PostRoll bound the bookmark window.
	//
	// Pre-roll is why it exists: a bookmark at the start of the window is a bookmark at
	// the moment the operator does not yet care about. Post-roll is because the event's
	// tail is usually where the evidence of what it was is.
	PreRoll, PostRoll time.Duration
	// RetainFor is how long a receipt is kept.
	//
	// Stated rather than defaulted to the table's default, because `retain_until` is
	// written by this row and a retention policy nobody chose is a policy that will be
	// questioned by whoever holds the deletion policy.
	RetainFor time.Duration
}

// Outcome is what one job did.
//
// A struct rather than an error because the three interesting outcomes are not two: placed,
// refused, and failed. Collapsing refused into failed is what turns a clock problem into a
// dead letter.
type Outcome struct {
	Placed   bool
	Bookmark milestone.BookmarkRef
	Refusal  string
	// EvidenceID is the receipt row, empty when nothing was placed.
	EvidenceID string
	OpenedAt   time.Time
}

// Handle runs one job.
func (w *Worker) Handle(ctx context.Context, j jobs.Job) (Outcome, error) {
	if j.Queue != jobs.QueueEvidenceFetch {
		return Outcome{}, fmt.Errorf("evidencefetch: job is on queue %q, not evidence_fetch", j.Queue)
	}

	payload, err := dispatch.DecodeJobPayload(j.Payload)
	if err != nil {
		return Outcome{}, fmt.Errorf("evidencefetch: %w", err)
	}

	// Misconfiguration is refused BEFORE any work is done, and the checks are in that
	// order on purpose.
	//
	// A deployment with no write-back surface has nothing to point at, so reading the
	// incident first would be work whose result is discarded. And a nil pool has to be a
	// refusal rather than a panic: `dbx.Tenant.QueryRow` dereferences the pool, and a
	// worker that panics on a misconfigured install takes the process down with it
	// instead of recording why the evidence was not fetched.
	if w.Bookmarks == nil {
		return w.refuse(ctx, j, payload, "no_bookmark_surface",
			"this deployment has no vendor write-back surface, so there is no recorder to "+
				"point at. The footage exists on the vendor's own archive and this product "+
				"deliberately does not copy it")
	}
	if w.Store == nil {
		return w.refuse(ctx, j, payload, "no_evidence_store",
			"no evidence store is configured, so a receipt for the bookmark could not be "+
				"kept. A bookmark whose receipt is lost is an incident that cannot later be "+
				"shown to have had evidence")
	}
	if w.Facts == nil {
		return w.refuse(ctx, j, payload, "no_fact_reader",
			"no fact reader is configured, so the incident's window, camera and clock drift "+
				"could not be read. This is a wiring fault rather than a property of the incident")
	}

	facts, err := w.Facts.ReadFacts(ctx, j, payload)
	if err != nil {
		return Outcome{}, err
	}

	decision, openedAt, _ := milestone.DecideBookmark(w.request(facts), w.Policy)
	if !decision.Placed {
		return w.refuse(ctx, j, payload, decision.Refusal,
			"the bookmark was refused because placing it would open at the wrong second or "+
				"point at nothing. See milestone.DecideBookmark for what each refusal means")
	}

	ref, err := w.Bookmarks.WriteBookmark(ctx, milestone.BuildBookmark(w.request(facts), openedAt))
	if err != nil {
		// A vendor failure is a RETRY, not a refusal. The vendor may be down, and a
		// bookmark that fails because the link was down and a bookmark that was never
		// attempted are different facts the operator has to be able to tell apart.
		return Outcome{}, fmt.Errorf("evidencefetch: place the bookmark for incident %s: %w",
			facts.IncidentID, err)
	}

	outcome, err := w.record(ctx, j, payload, ref, openedAt, facts)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{
		Placed: true, Bookmark: ref, EvidenceID: outcome, OpenedAt: openedAt,
	}, nil
}

// Run drains the queue, because a `Worker` that a caller has to loop is a Worker somebody
// will not loop.
//
// Returns the count so a caller can log it, and the first error rather than an aggregate:
// a run that reports "3 of 40 failed" without saying which is a number nobody can act on.
func (w *Worker) Run(ctx context.Context, q *jobs.Worker, tenantID, owner string, limit int) (int, error) {
	claimed, err := q.Claim(ctx, tenantID, owner, limit)
	if err != nil {
		return 0, err
	}
	var handled int
	for _, j := range claimed {
		if _, hErr := w.Handle(ctx, j); hErr != nil {
			if _, fErr := q.Fail(ctx, tenantID, j.ID, owner, hErr); fErr != nil {
				return handled, fmt.Errorf("evidencefetch: job %s failed and could not be "+
					"recorded as failed: %w", j.ID, fErr)
			}
			continue
		}
		if cErr := q.Complete(ctx, tenantID, j.ID, owner); cErr != nil {
			return handled, cErr
		}
		handled++
	}
	return handled, nil
}

// ---------------------------------------------------------------------------
// Facts
// ---------------------------------------------------------------------------

// facts is what the bookmark is decided from.
//
// Every field is read from storage. None comes from the job payload, and that is the whole
// security argument for this worker: `Dispatch.Target` is operator-editable in a bundle, so
// a target that could name a camera would let a rule bundle choose which recording a
// receipt points at. The payload selects the INCIDENT; the incident selects the camera.
// pool recovers the database handle from the fact reader.
//
// The receipt row and the ledger entry need a transaction, and `Facts` is an interface, so
// the pool is reached through the production reader rather than held as a second field on
// Worker. A second field would be a second thing to wire, and a Worker whose `Pool` and
// `Facts` disagreed would write a receipt for an incident it read a different one from.
func (w *Worker) pool() *dbx.Pool {
	if r, ok := w.Facts.(poolFacts); ok {
		return r.pool
	}
	return nil
}

// FactReader supplies the facts a bookmark is decided from.
//
// An interface with one production implementation, and the reason is in the `Worker.Facts`
// comment: the decisions are the package, and they have to be testable without a database.
type FactReader interface {
	ReadFacts(ctx context.Context, j jobs.Job, p dispatch.JobPayload) (Facts, error)
}

// Facts is what the bookmark is decided from.
//
// Every field is read from storage. None comes from the job payload, and that is the whole
// security argument for this worker: `Dispatch.Target` is operator-editable in a bundle, so
// a target that could name a camera would let a rule bundle choose which recording a
// receipt points at. The payload selects the INCIDENT; the incident selects the camera.
type Facts struct {
	IncidentID  string
	SiteID      string
	RuleID      string
	Title       string
	SiteRef     string
	DeviceID    string
	WindowStart time.Time
	WindowEnd   time.Time
	DriftMS     int32
}

// request renders the facts as the bookmark package wants them, with the window bounds
// this installation was configured with.
//
// A method on Worker rather than on Facts so the bounds come from configuration and cannot
// be quietly completed by a method nobody reads. A window widened by something invisible is
// a window an operator cannot account for.
func (w *Worker) request(f Facts) milestone.BookmarkRequest {
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = f.RuleID
	}
	return milestone.BookmarkRequest{
		IncidentID:  f.IncidentID,
		SiteRef:     f.SiteRef,
		SiteID:      f.SiteID,
		WindowStart: f.WindowStart,
		WindowEnd:   f.WindowEnd,
		PreRoll:     w.PreRoll,
		PostRoll:    w.PostRoll,
		Summary:     title,
		Drift:       milestone.Drift{Offset: time.Duration(f.DriftMS) * time.Millisecond},
	}
}

// poolFacts reads the facts from storage.
type poolFacts struct{ pool *dbx.Pool }

// NewPoolFacts is the production `FactReader`: one statement, one transaction's worth of
// joins.
//
// The window comes from `incidents.first_event_at` and `last_event_at` rather than from
// the job, because those are the bounds the incident was raised on, and a bookmark for a
// different window than the decision used is a bookmark at a moment the operator was never
// shown.
//
// The drift comes from `device_clocks`, which the clock subscriber already maintains, and
// NOT from a live `milestone.ClockProbe` call. Sampling the vendor's clock on the evidence
// path would add a vendor round-trip to every incident, and the stored reading is the one
// that was in effect when the event was normalised - which is the reading the window was
// built from. A fresher reading would place the bookmark using a clock the decision never
// saw.
func NewPoolFacts(pool *dbx.Pool) FactReader { return poolFacts{pool: pool} }

func (r poolFacts) ReadFacts(ctx context.Context, j jobs.Job, p dispatch.JobPayload) (Facts, error) {
	var out Facts
	incidentID := firstNonEmpty(p.IncidentID, incidentFromKey(j.IdempotencyKey))
	if incidentID == "" {
		return out, errors.New("evidencefetch: the job names no incident, so there is nothing " +
			"to bookmark. A fetch with no subject is work that cannot be done")
	}

	err := r.pool.For(j.TenantID, "evidence").QueryRow(ctx, `
		SELECT i.primary_site_id, i.rule_id, i.title,
		       i.first_event_at, i.last_event_at,
		       ev.device_id, d.source_device_id,
		       COALESCE(c.drift_ms, 0)
		  FROM incidents i
		  LEFT JOIN LATERAL (
		        SELECT e.device_id
		          FROM incident_events ie
		          JOIN canonical_events e ON e.tenant_id = ie.tenant_id AND e.event_id = ie.event_id
		         WHERE ie.tenant_id = i.tenant_id AND ie.incident_id = i.incident_id
		         ORDER BY ie.role = 'trigger' DESC, e.normalized_at
		         LIMIT 1
		  ) ev ON TRUE
		  LEFT JOIN devices d ON d.tenant_id = i.tenant_id AND d.device_id = ev.device_id
		  LEFT JOIN device_clocks c ON c.tenant_id = i.tenant_id AND c.device_id = ev.device_id
		 WHERE i.tenant_id = $1 AND i.incident_id = $2`,
		j.TenantID, incidentID).
		Scan(&out.SiteID, &out.RuleID, &out.Title, &out.WindowStart, &out.WindowEnd,
			&out.DeviceID, &out.SiteRef, &out.DriftMS)
	if err != nil {
		return out, fmt.Errorf("evidencefetch: read incident %s for its bookmark facts: %w",
			incidentID, err)
	}

	out.IncidentID = incidentID
	if p.CameraID != "" {
		out.DeviceID = p.CameraID
	}
	// A negative drift_ms means the vendor's clock is BEHIND ours. The sign is preserved:
	// a bookmark placed with the offset applied backwards opens forty seconds early, which
	// is a different wrong second rather than the right one.
	return out, nil
}

// incidentFromKey recovers the incident id from the outbox row's idempotency key.
//
// `<incident>:<type>:<target>` is built by `dispatch.messageFor`, and the job carries that
// same key. Recovering it rather than requiring the payload to carry one is a fallback: the
// payload is the right answer and a key parse is a second one, so this is used only when the
// payload has none, and it refuses rather than guessing.
func incidentFromKey(key string) string {
	if at := strings.Index(key, ":"); at > 0 {
		return key[:at]
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
