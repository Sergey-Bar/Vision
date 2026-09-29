package api

import (
	"context"
	"errors"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/clock"
	"github.com/Sergey-Bar/Ai-Captain/internal/clocksub"
	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/jobs"
	"github.com/Sergey-Bar/Ai-Captain/internal/outbox"
)

// The two operational read models the API exposes, wired to the services that
// own them.
//
// They exist as interfaces so the routes could ship before these adapters existed,
// and an endpoint that answers "not configured" to a console that renders its nav
// unconditionally is one an operator learns to distrust. These close that gap.

// DeadLetters reads terminal failures from every queue.
//
// All three: the inbox, the job queue, and the outbox. The outbox was the one
// that got missed, and a message that failed on its way out to the relay was a
// message an operator could not see failed anywhere.
type DeadLetters struct {
	Inbox *inbox.Writer
	Jobs  *jobs.Worker
	Relay *outbox.Relay
}

// ErrNotWired is returned when a read model is absent, so the route can say
// "not configured" rather than inventing an empty result. An empty list and a
// broken list look identical to an operator, and only one of them is a lie.
var ErrNotWired = errors.New("read model is not configured")

// Read returns dead letters of a kind, or both when kind is empty.
func (d DeadLetters) Read(ctx context.Context, tenant, kind string, limit int) ([]DeadLetter, error) {
	switch kind {
	case "inbox":
		if d.Inbox == nil {
			return nil, ErrNotWired
		}
		letters, err := d.Inbox.DeadLetters(ctx, tenant, limit)
		if err != nil {
			return nil, err
		}
		out := make([]DeadLetter, 0, len(letters))
		for _, l := range letters {
			out = append(out, DeadLetter{
				Kind: "inbox", ID: l.SourceEventID, Queue: l.IntegrationID,
				Integration: l.IntegrationID, SourceEvent: l.SourceEventID,
				Attempts: l.Attempts, Reason: l.Reason, UpdatedAt: l.UpdatedAt,
				Replayable: true,
			})
		}
		return out, nil

	case "job":
		if d.Jobs == nil {
			return nil, ErrNotWired
		}
		letters, err := d.Jobs.DeadLetters(ctx, tenant, limit)
		if err != nil {
			return nil, err
		}
		out := make([]DeadLetter, 0, len(letters))
		for _, l := range letters {
			out = append(out, DeadLetter{
				Kind: "job", ID: l.ID, Queue: string(l.Queue),
				Attempts: l.Attempts, Reason: l.Reason, UpdatedAt: l.CreatedAt,
				Replayable: true,
			})
		}
		return out, nil
	case "outbox":
		// The third queue, which the read model claimed to cover and did not.
		//
		// "reads terminal failures from both queues" was true when there were two
		// queues, and then the outbox was added and this switch was not. An
		// operator whose message failed on the way out to the relay saw a
		// dead-letter console that did not mention it.
		if d.Relay == nil {
			return nil, ErrNotWired
		}
		letters, err := d.Relay.DeadLetters(ctx, tenant, limit)
		if err != nil {
			return nil, err
		}
		out := make([]DeadLetter, 0, len(letters))
		for _, l := range letters {
			out = append(out, DeadLetter{
				Kind: "outbox",
				// The outbox key is (tenant, idempotency_key) and its id is a
				// surrogate key nothing outside the relay has a reason to know. The
				// idempotency key is what an operator can act on, and it is what
				// `Relay.Replay` takes.
				ID: l.IdempotencyKey, Queue: l.Topic,
				Integration: "", SourceEvent: l.IdempotencyKey,
				Attempts: l.Attempts, Reason: l.Reason, UpdatedAt: l.DeadLetteredAt,
				Replayable: true,
			})
		}
		return out, nil
	}

	var out []DeadLetter
	if d.Relay != nil {
		items, err := d.Read(ctx, tenant, "outbox", limit)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if d.Inbox != nil {
		items, err := d.Read(ctx, tenant, "inbox", limit)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if d.Jobs != nil {
		items, err := d.Read(ctx, tenant, "job", limit)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if d.Inbox == nil && d.Jobs == nil {
		return nil, ErrNotWired
	}
	return out, nil
}

// ClockDrift reads the device clock view.
type ClockDrift struct {
	Service *clocksub.Service
	// Policy is the site's clock policy, so the view shows the exclusion a rule
	// would apply rather than making the operator derive it from a number.
	Policy clock.Policy
}

// List implements the clock-drift surface.
func (c ClockDrift) List(ctx context.Context, tenant string, limit int) ([]DriftView, error) {
	if c.Service == nil {
		return nil, ErrNotWired
	}
	views, err := c.Service.View(ctx, c.Policy, tenant, limit)
	if err != nil {
		return nil, err
	}
	out := make([]DriftView, 0, len(views))
	for _, v := range views {
		out = append(out, DriftView{
			DeviceID: v.DeviceID, SiteID: v.SiteID, DriftMS: v.DriftMS,
			Band: v.Band, MeasuredAt: v.MeasuredAt,
			Excluded: v.Excluded, Reason: v.Reason,
		})
	}
	return out, nil
}

// BacklogReader reads the ingest backlog the SLO thresholds are evaluated against.
type BacklogReader struct {
	Inbox  *inbox.Writer
	Jobs   *jobs.Worker
	Outbox OutboxPending
}

// OutboxPending is the outbox depth reader.
type OutboxPending func(ctx context.Context, tenant string) (int64, error)

// Count reads the backlog.
func (b BacklogReader) Count(ctx context.Context, tenant string) (BacklogCounts, error) {
	var out BacklogCounts
	if b.Inbox != nil {
		counts, err := b.Inbox.Counts(ctx, tenant)
		if err != nil {
			return out, err
		}
		out.InboxPending = counts.Received + counts.Failed
		out.InboxProcessing = counts.Processing
		out.InboxDeadLetter = counts.DeadLetter
	}
	if b.Jobs != nil {
		counts, err := b.Jobs.Counts(ctx, tenant)
		if err != nil {
			return out, err
		}
		out.JobPending = counts.Pending + counts.Failed
		out.JobDeadLetter = counts.DeadLetter
	}
	if b.Outbox != nil {
		n, err := b.Outbox(ctx, tenant)
		if err != nil {
			return out, err
		}
		out.OutboxPending = n
	}
	return out, nil
}

// BacklogCounts is the backlog in the shape the console and the SLO alerts read.
type BacklogCounts struct {
	InboxPending    int64
	InboxProcessing int64
	InboxDeadLetter int64
	JobPending      int64
	JobDeadLetter   int64
	OutboxPending   int64
	AsOf            time.Time
}
