package dispatch

// Enqueuing a rule's dispatches, in the caller's transaction.
//
// This is the file the durability fix lives in, and it exists as a separate file from the
// executors because the two halves run in opposite directions:
//
//   - `Dispatcher.Handler` runs LATER, off the relay, and performs the side effect. It gets
//     at-least-once delivery and must be idempotent.
//   - `EnqueueOutbox` runs NOW, inside the transaction that creates the incident, and only
//     writes rows. It gets exactly-once semantics from that transaction and has no retry
//     logic at all.
//
// Splitting them is what keeps the two obligations from being confused. A handler that
// opened its own transaction would commit the side effect separately from the incident that
// caused it, which is the exact gap `internal/pipeline`'s `enqueueVLM` had: it ran after
// `CreateIncident` had already committed, in its own transaction, so an incident could
// exist with the work about it lost and nothing recording that the work was owed.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Sergey-Bar/Ai-Captain/internal/outbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
)

// Request is what one incident's creation tells the dispatches about.
//
// A struct rather than a list of arguments because the call site has six of them and the
// seventh would be a silent mis-ordering. `Dispatch` is the rule's own list, carried by
// value from the decision that produced them.
type Request struct {
	TenantID   string
	SiteID     string
	IncidentID string
	// EventID is the trigger. A VLM job cannot be built without it: the worker looks the
	// evidence up from the event, so an inference with no event is an inference about
	// nothing.
	EventID string
	// EvidenceID is what `request_vlm` needs and what the old `enqueueVLM` never sent.
	EvidenceID string
	RuleID     string
	Title      string
	Severity   string
	OccurredAt time.Time
	// Dispatch is the winning rule's side-effect list.
	Dispatch []rules.Dispatch
}

// EnqueueOutbox writes one outbox row per dispatch, in the caller's transaction.
//
// The rows are the durable record. Whether the relay ever delivers them is a separate
// question answered by `ops/dead-letters`, and the difference between those two states is
// precisely what the outbox pattern buys: a message that could not be delivered is a
// visible fact rather than an absence.
//
// One message per dispatch, keyed deterministically. The key is
// `<incident>:<type>:<target>`, which makes a re-enqueue of the same decision a no-op via
// `outbox.Enqueue`'s `ON CONFLICT (tenant_id, idempotency_key) DO NOTHING` - and that
// matters for a reason beyond tidiness: the pipeline's retry path re-runs
// `CreateIncident`, and a random key per attempt would make each retry another message.
//
// Nothing here logs, and nothing here talks to a network. This function's whole job is to
// turn a decision into rows that are committed or not committed with the incident.
func EnqueueOutbox(ctx context.Context, tx pgx.Tx, req Request) error {
	if req.IncidentID == "" {
		return fmt.Errorf("dispatch: an incident with no id cannot key its dispatches, and " +
			"a random key would make every retry a second message")
	}
	for _, d := range req.Dispatch {
		topic, err := topicFor(d.Type)
		if err != nil {
			return err
		}
		msg, err := messageFor(topic, req, d)
		if err != nil {
			return err
		}
		if err := outbox.Enqueue(ctx, tx, msg); err != nil {
			return err
		}
	}
	return nil
}

// messageFor builds one outbox message.
//
// The topic decides the payload shape, and there are exactly two: a notification, which is
// rendered and delivered, and a queued job, which is a row in `jobs` written by the relay.
// The split is not cosmetic - a notify topic's `target` is a destination name and a fetch
// topic's `target` is what to capture, so they cannot share a payload type without one of
// the two meanings being wrong.
func messageFor(topic string, req Request, d rules.Dispatch) (outbox.Message, error) {
	key := fmt.Sprintf("%s:%s:%s", req.IncidentID, d.Type, d.Target)

	switch topic {
	case topicNotifyOperator, topicNotifySupervisor:
		return outbox.Message{
			TenantID: req.TenantID,
			SiteID:   req.SiteID,
			Topic:    topic,
			// The key is IN the payload as well as on the row. A receiver that stores the
			// body has to be able to recognise a redelivery from the body alone, and a
			// header is dropped by the first proxy that rewrites headers.
			IdempotencyKey: key,
			Payload: mustJSON(NotificationPayload{
				IncidentID:      req.IncidentID,
				RuleID:          req.RuleID,
				Title:           req.Title,
				Severity:        req.Severity,
				SiteID:          req.SiteID,
				OccurredAt:      req.OccurredAt.UTC(),
				Explanation:     "",
				Target:          d.Target,
				IdempotencyKey:  key,
				ContractVersion: ContractVersion,
			}),
		}, nil

	case topicRequestVLM:
		p := JobPayload{
			IncidentID:      req.IncidentID,
			EventID:         req.EventID,
			EvidenceID:      req.EvidenceID,
			SiteID:          req.SiteID,
			OccurredAt:      req.OccurredAt.UTC(),
			IdempotencyKey:  key,
			ContractVersion: ContractVersion,
		}
		// Validated HERE, at enqueue time, rather than at delivery. `airuntime.Worker`
		// returns `DegradationAbsent` before it contacts the runtime when a job names no
		// evidence, so a job built without one would produce a degraded observation on
		// every run and report to the operator that the model could not tell - when the
		// job never reached the model. Refusing here means the rule's dispatch is dropped
		// loudly at incident creation instead.
		//
		// This is the second of the two blockers on the AI path. The first was that
		// `enqueueVLM` sent no evidence id at all; binding a model changed nothing while
		// the job carried nothing to send it.
		if _, err := p.Queue(topic); err != nil {
			return outbox.Message{}, fmt.Errorf(
				"dispatch: rule asked for request_vlm and the job cannot be built: %w", err)
		}
		return jobMessage(topic, req, key, p), nil

	case topicFetchEvidence:
		p := JobPayload{
			IncidentID:      req.IncidentID,
			EventID:         req.EventID,
			EvidenceID:      req.EvidenceID,
			SiteID:          req.SiteID,
			OccurredAt:      req.OccurredAt.UTC(),
			IdempotencyKey:  key,
			ContractVersion: ContractVersion,
		}
		if _, err := p.Queue(topic); err != nil {
			return outbox.Message{}, fmt.Errorf(
				"dispatch: rule asked for fetch_evidence and the job cannot be built: %w", err)
		}
		return jobMessage(topic, req, key, p), nil
	}

	return outbox.Message{}, fmt.Errorf("dispatch: %q has no message builder; the closed "+
		"set of topics is %v", topic, topics)
}

func jobMessage(topic string, req Request, key string, p JobPayload) outbox.Message {
	return outbox.Message{
		TenantID:       req.TenantID,
		SiteID:         req.SiteID,
		Topic:          topic,
		IdempotencyKey: key,
		Payload:        mustJSON(p),
	}
}
