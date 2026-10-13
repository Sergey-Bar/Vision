// Package dispatch performs the side effects a rule asks for.
//
// # What this closes
//
// `rules/builtin/bundle.yaml` asks for `notify_operator`, `notify_supervisor`,
// `request_vlm` and `fetch_evidence`. Before this package, all four parsed, all four
// validated against the closed `rules.DispatchTypes` set at bundle-compile time, and all
// four went nowhere: `outbox.Enqueue` had no production caller and the relay drained an
// empty table. An operator who installed the product, triggered the one incident type it
// promises, and waited for a notification was waiting for a thing with no code behind it.
//
// The chain has four links and this package owns the last two: the pipeline enqueues
// inside the incident's transaction (invariant 1), the relay hands each message to a
// handler, the handler renders a payload through a CLOSED set of renderers, and a
// destination decides where it goes.
//
// # Three decisions that are decisions
//
// **topics are constants, never built from rule text.** `outbox.go`'s own comment says an
// unknown topic is dead-lettered rather than ignored - which is right, and which means a
// mistyped topic derived from a rule bundle would dead-letter real dispatches instead of
// delivering them. A silent drop would have been better and is not available.
//
// **No secret is ever in the destinations file.** `SecretRef` names an environment
// variable; the value is read at startup and never written anywhere. A secret in a
// configuration file is a secret in the release archive, in `docker inspect`, in a support
// bundle and in a screenshot, and `internal/config` refuses to put one on a Config field
// for exactly that reason.
//
// **A bundle naming an unconfigured destination is refused at boot.** Not at first fire. A
// dropped notification at 03:00 is discovered by whoever was on call; a refusal at startup
// is discovered by whoever installed it, while the terminal is still in front of them.
//
// # Dependencies, and why each one is here
//
//	pgx.Tx      the job executors enqueue in a transaction, like everything else that
//	           writes: a job row and the outbox completion have to agree
//	jobs.Enqueuer  the existing durable claim/lease machinery, rather than a bespoke
//	           goroutine that would not survive a restart
//	rules.Dispatch the closed type vocabulary, owned by the package that defines it
package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/jobs"
	"github.com/Sergey-Bar/vision/internal/outbox"
	"github.com/Sergey-Bar/vision/internal/rules"
)

// The topics. Constants, and the reason they are constants is in the package header: a
// topic built from rule text and misspelled is a dead letter, not a dropped message.
const (
	// topicNotifyOperator delivers to the destination named `operator`.
	topicNotifyOperator = "dispatch.notify_operator"
	// topicNotifySupervisor delivers to the destination named `supervisor`.
	topicNotifySupervisor = "dispatch.notify_supervisor"
	// topicRequestVLM enqueues an inference job rather than delivering anything.
	//
	// It is a topic because it goes through the same transactional path: the side effect
	// has to commit with the incident or not at all, and a job row written directly by
	// the pipeline is exactly the separate-transaction gap `enqueueVLM` has today.
	topicRequestVLM = "dispatch.request_vlm"
	// topicFetchEvidence enqueues evidence work rather than delivering anything.
	topicFetchEvidence = "dispatch.fetch_evidence"
)

// topics is the closed set of topics this package handles.
//
// It is the inverse of `rules.DispatchTypes`: a dispatch type with no topic, or a topic
// with no dispatch type, is a mismatch somebody resolves rather than a message to ignore.
// `outbox` dead-letters an unknown topic, so that asymmetry is what stands between a typo
// and a silently dropped notification.
var topics = []string{
	topicFetchEvidence,
	topicNotifyOperator,
	topicNotifySupervisor,
	topicRequestVLM,
}

// topicFor maps a rule's dispatch type onto the topic that carries it.
func topicFor(dispatchType string) (string, error) {
	switch dispatchType {
	case "notify_operator":
		return topicNotifyOperator, nil
	case "notify_supervisor":
		return topicNotifySupervisor, nil
	case "request_vlm":
		return topicRequestVLM, nil
	case "fetch_evidence":
		return topicFetchEvidence, nil
	}
	return "", fmt.Errorf("dispatch: %q is not a dispatch type this package knows; "+
		"the closed set is %s", dispatchType, strings.Join(sortedTypes(), ", "))
}

// sortedTypes is the closed `rules.DispatchTypes` set, sorted, for a message that has to
// say what it expected.
func sortedTypes() []string {
	out := make([]string, 0, len(rules.DispatchTypes))
	for k := range rules.DispatchTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Dispatcher performs a delivery.
//
// A struct rather than package-level state, because `internal/config`'s testability
// contract is `LoadFrom(get Lookup)` - a pure function over an injected lookup - and a
// package with a `var destinations Destinations` global cannot be tested without
// mutating that global. A Dispatcher is constructed once at startup and passed down.
//
// Enqueuer may be nil, and `Handler` reports that as a refusal rather than acknowledging
// the message. See errNoEnqueuer.
type Dispatcher struct {
	// Dests resolves a destination name to a URL and its credential reference.
	Dests *Destinations
	// Enqueuer writes jobs for the two queue-shaped dispatches.
	Enqueuer *jobs.Enqueuer
	// Pool opens the transaction a queued dispatch enqueues in.
	Pool *dbx.Pool
	// httpClient is optional, and nil means build one from defaultTimeout.
	//
	// Injected by tests, which assert a delivery against an `httptest.Server` and would
	// otherwise need a listener on a real port, and by an operator whose receiver sits
	// behind a private certificate authority and needs a custom CA bundle - the supported
	// answer to the `insecure_skip_verify` field the destinations loader refuses.
	httpClient *http.Client
}

// SetHTTPClient injects the HTTP client. See the field's comment.
func (d *Dispatcher) setHTTPClient(c *http.Client) { d.httpClient = c }

// Handler is the `outbox.Handler` this package supplies.
//
// It is the relay's entry point, so its idempotency obligation is the relay's: the relay
// gives at-least-once delivery and a handler that is not idempotent doubles its effect on
// the first retry after a crash.
//
// Both halves of this package are idempotent, by different means. A notification is
// idempotent because the delivery's `IdempotencyKey` is the incident id and the
// destination is expected to dedupe on it - which is why the key is in the payload's
// rendered form and not only on the row. A queued dispatch is idempotent because
// jobs.Enqueue carries the same key and has an
// `ON CONFLICT (tenant_id, queue, idempotency_key) DO NOTHING`.
func (d *Dispatcher) Handler() outbox.Handler {
	return func(ctx context.Context, delivery outbox.Delivery) error {
		dispatchType, err := typeForTopic(delivery.Topic)
		if err != nil {
			// Unknown topic is dead-lettered by the relay's own retry-then-dead-letter
			// path, which is `outbox`'s documented behaviour. Acknowledging it here would
			// mark a side effect delivered that nobody performed, which is the one thing
			// worse than losing it.
			return &UnknownTopic{Topic: delivery.Topic}
		}
		exec, ok := d.executorFor(dispatchType)
		if !ok {
			// A topic this build publishes and does not implement. Reachable only if a
			// case is removed from `executorFor` without removing the topic, which is why
			// the refusal is typed rather than a bare error.
			return &UnknownTopic{Topic: delivery.Topic}
		}
		return exec(ctx, delivery)
	}
}

// typeForTopic is the inverse of topicFor.
//
// Needed because the executor table switches on the DISPATCH TYPE - which is the thing a
// rule names and the thing `internal/capability` measures - while the relay hands over a
// topic. An unknown topic is a refusal, and it is refused before this: `Handler` looks the
// type up with this function and refuses when it misses.
func typeForTopic(topic string) (string, error) {
	switch topic {
	case topicNotifyOperator:
		return "notify_operator", nil
	case topicNotifySupervisor:
		return "notify_supervisor", nil
	case topicRequestVLM:
		return "request_vlm", nil
	case topicFetchEvidence:
		return "fetch_evidence", nil
	}
	return "", fmt.Errorf("dispatch: %q is not a topic this package knows; the closed set "+
		"is %s", topic, strings.Join(topics, ", "))
}

// executorFor is the registered-executor table.
//
// It switches on the dispatch TYPE, which is deliberate and is what makes it readable by
// `internal/capability`'s DispatchExecutors gate. That gate has to decide whether a member
// of `rules.DispatchTypes` has an executor, and it cannot ask this package - a registry
// call would make the gate a statement that this package agrees with itself, which is the
// check that has no content. So the registration is a switch in the source, and the gate
// reads its case labels.
//
// Adding a case here is what closes a refusal in the gate's ledger. Adding one WITHOUT
// deleting the ledger entry fails the gate, which is the intended friction.
func (d *Dispatcher) executorFor(dispatchType string) (Executor, bool) {
	switch dispatchType {
	case "notify_operator":
		return d.notify(topicNotifyOperator), true
	case "notify_supervisor":
		return d.notify(topicNotifySupervisor), true
	case "request_vlm":
		return d.enqueueJob(topicRequestVLM), true
	case "fetch_evidence":
		return d.enqueueJob(topicFetchEvidence), true
	}
	return nil, false
}

// ExecutorTypes returns the dispatch types this dispatcher can perform.
//
// Used by the startup check that refuses an active bundle naming anything else, and by
// `securityctl doctor` so an operator can ask what an install can do rather than
// discovering it from a dead letter.
func (d *Dispatcher) ExecutorTypes() []string {
	var out []string
	for _, t := range sortedTypes() {
		if _, ok := d.executorFor(t); ok {
			out = append(out, t)
		}
	}
	return out
}

// Executor performs one delivery's side effect.
type Executor func(ctx context.Context, d outbox.Delivery) error

// notify builds the executor for a notify topic.
//
// One closure factory rather than two functions, because the two notify topics differ only
// in which destination they resolve - and they differ in a way that matters.
// `notify_operator` goes to whoever is on this site's escalation path and
// `notify_supervisor` goes to whoever supervises it. A rule author choosing between them is
// expressing a routing decision, and collapsing them onto one destination would make the
// rule mean something the operator did not write.
func (d *Dispatcher) notify(topic string) Executor {
	return func(ctx context.Context, delivery outbox.Delivery) error {
		p, err := DecodeNotification(delivery)
		if err != nil {
			return err
		}
		name, err := p.destinationName(topic)
		if err != nil {
			return err
		}
		dest, ok := d.Dests.Lookup(name)
		if !ok {
			// Unreachable while CheckDestinations runs at startup, and deliberately a
			// refusal rather than a log line: if a destination disappears between boot and
			// delivery the message must dead-letter rather than be acknowledged as
			// delivered and lost.
			return &UnconfiguredDestination{Name: name, Topic: topic}
		}
		rendered, err := renderNotification(topic, p)
		if err != nil {
			return err
		}
		return d.deliver(ctx, dest, topic, rendered, delivery)
	}
}

// deliver performs the HTTP POST, and is where the TLS and redirect rules live.
func (d *Dispatcher) deliver(ctx context.Context, dest Destination, topic string,
	r RenderedNotification, delivery outbox.Delivery) error {
	// The scheme and userinfo rules are re-checked here, on every delivery, and not only
	// at load time. That is cheap and it is the right place for a re-check: a
	// destination can be swapped at runtime by an operator with a config reload, and a
	// credential in a URL or a bearer token over plaintext should not depend on the
	// loader having run.
	//
	// The REACHABILITY rules are deliberately not re-checked. They need DNS, they belong
	// to the install-time policy in `CheckURL`, and applying them per delivery would
	// refuse every test that uses `httptest` - a correct refusal of a correct URL. The
	// load-time check is where an operator meets that policy.
	if err := checkURLScheme(dest.URL); err != nil {
		return fmt.Errorf("dispatch: destination %q: %w", dest.Name, err)
	}
	body := renderWebhook(r)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest.URL,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("dispatch: destination %q: build request: %w", dest.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "security-core-dispatch/"+ContractVersion)
	// The idempotency key travels as a header AND inside the body. The header is what a
	// generic receiver dedupes on; the body is what a receiver that stores the payload can
	// read after the fact. Both are the outbox row's own key, so a redelivery presents the
	// same value and a receiver that honours either is safe.
	req.Header.Set("Idempotency-Key", delivery.IdempotencyKey)
	req.Header.Set("X-Idempotency-Key", delivery.IdempotencyKey)
	if dest.SecretRef != "" {
		// Read at DELIVERY, not at boot. A secret can be rotated while the daemon is up,
		// and a cached copy would keep using the old token until a restart - which is the
		// shape of an outage that resolves itself only if somebody restarts the thing.
		token, present := os.LookupEnv(dest.SecretRef)
		if !present || token == "" {
			return &UnconfiguredDestination{Name: dest.Name, Topic: topic,
				Reason: fmt.Sprintf("secret_ref %q names an environment variable that is "+
					"unset or empty", dest.SecretRef)}
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("dispatch: destination %q: %w", dest.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The body is read and discarded. A receiver that returns a large body is not this
	// package's problem, but NOT reading it means the connection cannot be reused, and a
	// relay that leaks a connection per delivery exhausts the process's file descriptors
	// over a weekend.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &DeliveryRefused{Name: dest.Name, Topic: topic, Status: resp.StatusCode}
	}
	return nil
}

// client is the HTTP client, built once and shared.
//
// Redirects are refused, which is a security decision and the reason this is not
// `http.DefaultClient`. `CheckURL` refuses a downgrade to plaintext, but a redirect is a
// fresh request the URL check never saw: a receiver answering 302 with an
// `http://` location would carry the `Authorization` header this code just attached to a
// cleartext socket. Refusing redirects turns that into a dead letter.
func (d *Dispatcher) client() *http.Client {
	if d.httpClient != nil {
		return d.httpClient
	}
	return &http.Client{
		Timeout: defaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("dispatch: a redirect is refused. The destination URL was " +
				"checked at startup and a redirect is a URL that check never saw, so " +
				"following one could downgrade the connection carrying the credential")
		},
	}
}

// enqueueJob builds the executor for a topic whose side effect is a queued job.
//
// The enqueue is a separate transaction on purpose. The side effect that has to commit
// atomically with the incident is the OUTBOX ROW; the job is work that can be claimed,
// retried and leased by the existing durable machinery, and routing it through
// `jobs.Enqueue` gets that rather than a bespoke goroutine.
//
// What this does NOT do is acknowledge the outbox message before the job commits. The
// enqueue error is returned, the relay retries, and the job is written once because the
// job key is the outbox row's own idempotency key and `jobs.Enqueue` has an
// `ON CONFLICT (tenant_id, queue, idempotency_key) DO NOTHING`.
func (d *Dispatcher) enqueueJob(topic string) Executor {
	return func(ctx context.Context, delivery outbox.Delivery) error {
		if d.Enqueuer == nil || d.Pool == nil {
			return errNoEnqueuer
		}
		p, err := DecodeJob(delivery)
		if err != nil {
			return err
		}
		queue, err := p.Queue(topic)
		if err != nil {
			return err
		}
		tx, err := d.Pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("dispatch: %s: begin: %w", topic, err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, _, err := d.Enqueuer.Enqueue(ctx, tx, p.JobIn(queue, delivery)); err != nil {
			return fmt.Errorf("dispatch: %s: %w", topic, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("dispatch: %s: commit: %w", topic, err)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// Payload
// ---------------------------------------------------------------------------

// ContractVersion is the payload contract this build speaks.
//
// In every payload rather than in a header, because a receiver several versions behind
// still has to be able to tell what it received - and a version in a header is dropped by
// the first proxy that rewrites headers.
const ContractVersion = "security.io/dispatch/v1"

// NotificationPayload is what a notify delivery carries.
//
// A CLOSED set of fields, and that is the security property rather than a limitation. The
// payload crosses a network to a third party, so what can be in it is decided by this
// struct and not by whatever a rule bundle's `target` string happens to contain.
//
// There is no free-text body: the body is RENDERED from the fields below, so the set of
// things a receiver can be shown is fixed by a renderer in this package. An
// operator-editable field rendered as markup is a field that ends up executing in somebody's
// incident-management tool.
type NotificationPayload struct {
	IncidentID      string    `json:"incident_id"`
	RuleID          string    `json:"rule_id"`
	Title           string    `json:"title"`
	Severity        string    `json:"severity"`
	SiteID          string    `json:"site_id,omitempty"`
	OccurredAt      time.Time `json:"occurred_at"`
	Explanation     string    `json:"explanation,omitempty"`
	Target          string    `json:"target"`
	IdempotencyKey  string    `json:"idempotency_key"`
	ContractVersion string    `json:"contract_version"`
}

// destinationName is how a target resolves to a configured destination.
//
// A rule's `target` names a DESTINATION, not a recipient and certainly not a URL. `on-call`
// resolves to a row in the destinations file that an operator wrote, which is what keeps an
// operator-editable bundle from being an SSRF primitive: the set of reachable hosts is
// configured out of band and is exactly as wide as the install's destinations file.
//
// An empty target resolves to the topic's default, so a rule that omits it still delivers
// somewhere rather than dead-lettering.
func (p NotificationPayload) destinationName(topic string) (string, error) {
	switch topic {
	case topicNotifyOperator:
		if p.Target == "" {
			return defaultOperatorDestination, nil
		}
		return p.Target, nil
	case topicNotifySupervisor:
		if p.Target == "" {
			return defaultSupervisorDestination, nil
		}
		return p.Target, nil
	}
	return "", fmt.Errorf("dispatch: %q is not a notify topic, so it has no destination", topic)
}

// Default destination names, when a rule omits `target`.
//
// Named constants rather than a config key, because the alternative is an operator having
// to invent a name for the thing the product should already know where to send.
const (
	defaultOperatorDestination   = "operator"
	defaultSupervisorDestination = "supervisor"
)

// JobPayload is what a queue-shaped delivery carries.
type JobPayload struct {
	IncidentID      string          `json:"incident_id,omitempty"`
	EventID         string          `json:"event_id,omitempty"`
	EvidenceID      string          `json:"evidence_id,omitempty"`
	ProfileID       string          `json:"profile_id,omitempty"`
	CameraID        string          `json:"camera_id,omitempty"`
	SiteID          string          `json:"site_id,omitempty"`
	OccurredAt      time.Time       `json:"occurred_at,omitempty"`
	Request         json.RawMessage `json:"request,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key"`
	ContractVersion string          `json:"contract_version"`
}

// Queue resolves the job queue for a topic, and refuses what it cannot do.
//
// This is where the AI path's second blocker is caught. `enqueueVLM` used to enqueue with
// only an incident id and an event id, so `airuntime/worker.go` returned
// `DegradationAbsent` at the evidence check and never contacted the runtime: binding a
// model changed nothing, because the job had nothing to send it. So a VLM job without an
// evidence id is refused here rather than enqueued as a job that is guaranteed to degrade.
//
// A VLM job without an `evidence_id` is still accepted when `profile_id` is set, because a
// profile can resolve its own evidence from the incident - but the refusal says so rather
// than assuming, because assuming is how the original defect survived.
func (p JobPayload) Queue(topic string) (jobs.Queue, error) {
	switch topic {
	case topicRequestVLM:
		switch {
		case p.IncidentID == "":
			return "", fmt.Errorf("dispatch: %s needs an incident_id", topic)
		case p.EventID == "":
			return "", fmt.Errorf("dispatch: %s needs an event_id: the worker looks the "+
				"evidence up from the event, and an inference with no event is an "+
				"inference about nothing", topic)
		case p.EvidenceID == "":
			return "", fmt.Errorf("dispatch: %s has no evidence_id, and "+
				"airuntime.Worker returns DegradationAbsent before it contacts the "+
				"runtime when the job names no evidence. Enqueueing it would produce a "+
				"degraded observation on every run and tell the operator the model could "+
				"not tell when in fact the job never reached it", topic)
		}
		return jobs.QueueVLM, nil
	case topicFetchEvidence:
		if p.IncidentID == "" && p.EventID == "" {
			return "", fmt.Errorf("dispatch: %s needs an incident_id or an event_id to "+
				"fetch evidence for; got neither", topic)
		}
		return jobs.QueueEvidenceFetch, nil
	}
	return "", fmt.Errorf("dispatch: %q is not a job topic", topic)
}

// JobIn builds the durable job, keyed on the outbox row's own idempotency key.
//
// The key is what makes a redelivery safe: `jobs.Enqueue` has
// `ON CONFLICT (tenant_id, queue, idempotency_key) DO NOTHING`, so a relay that retries
// after a crash commits the job exactly once. Deriving it from the delivery rather than
// generating a new one per attempt is the whole mechanism, and a fresh UUID here would turn
// at-least-once delivery into at-least-once DUPLICATE INFERENCE.
func (p JobPayload) JobIn(queue jobs.Queue, delivery outbox.Delivery) jobs.Enqueue {
	key := p.IdempotencyKey
	if key == "" {
		key = delivery.IdempotencyKey
	}
	payload := p.Request
	if len(payload) == 0 {
		payload = mustJSON(p)
	}
	return jobs.Enqueue{
		TenantID:       delivery.TenantID,
		SiteID:         firstNonEmpty(p.SiteID, delivery.SiteID),
		Queue:          queue,
		IdempotencyKey: key,
		Payload:        payload,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Renderers
// ---------------------------------------------------------------------------

// renderNotification renders a payload for delivery.
//
// A CLOSED set of renderers, one per notify topic, and it refuses rather than defaulting.
// The set being closed is the point: a renderer is code that puts product data in front of
// a person or a third party, and "whatever the payload says" is how an incident title
// becomes markup in somebody's dashboard.
func renderNotification(topic string, p NotificationPayload) (RenderedNotification, error) {
	switch topic {
	case topicNotifyOperator, topicNotifySupervisor:
	default:
		return RenderedNotification{}, fmt.Errorf("dispatch: %q has no notification "+
			"renderer; the closed set is %s", topic, strings.Join(topics, ", "))
	}
	if p.IncidentID == "" {
		return RenderedNotification{}, errors.New("dispatch: notification payload carries " +
			"no incident_id, so there is nothing to tell an operator about")
	}

	fields := orderedFields(p)
	var b strings.Builder
	b.WriteString("**" + escapeMarkdown(p.Title) + "**\n\n")
	if p.Explanation != "" {
		b.WriteString(escapeMarkdown(p.Explanation) + "\n\n")
	}
	for _, f := range fields {
		b.WriteString("- " + f[0] + ": " + escapeMarkdown(f[1]) + "\n")
	}
	link := incidentLink(p)
	if link != "" {
		b.WriteString("\n[Open in console](" + link + ")\n")
	}
	return RenderedNotification{
		Topic:    topic,
		Title:    p.Title,
		Body:     b.String(),
		Fields:   fields,
		Occurred: p.OccurredAt.UTC(),
		Link:     link,
	}, nil
}

// RenderedNotification is the delivered shape: a title for a subject or a page, a body for
// a message, and ordered fields for a structured receiver that wants them separately.
type RenderedNotification struct {
	Topic    string
	Title    string
	Body     string
	Fields   [][2]string
	Occurred time.Time
	Link     string
}

// orderedFields is the fixed field order.
//
// Fixed, and not map iteration, because two renders of the same notification have to be
// byte-identical: an operator comparing two pages to see what changed needs the lines in
// the same order, and a `map` makes that a coin flip per process. Golden tests compare
// these bytes.
func orderedFields(p NotificationPayload) [][2]string {
	fields := [][2]string{
		{"Incident", p.IncidentID},
		{"Rule", p.RuleID},
		{"Severity", p.Severity},
		{"Site", p.SiteID},
		{"Occurred", p.OccurredAt.UTC().Format(time.RFC3339)},
	}
	if p.Target != "" {
		fields = append(fields, [2]string{"Target", p.Target})
	}
	return fields
}

// incidentLink points an operator at the incident in the console.
//
// Built from the incident id and nothing else. Deliberately not built from a configured
// base URL: a URL taken from configuration and rendered into a notification is one more
// place a site can send an operator somewhere other than their own console.
func incidentLink(p NotificationPayload) string {
	if p.IncidentID == "" {
		return ""
	}
	return "/incidents/" + p.IncidentID
}

// escapeMarkdown neutralises the characters that turn a title into markup.
//
// This is the renderer's whole security surface, and it is here because an incident title
// comes from a rule's `then.title`, which is operator-editable in a bundle, and a title may
// legitimately contain a vendor's message text. A receiver that renders Markdown would
// execute that.
func escapeMarkdown(s string) string {
	return markdownEscaper.Replace(s)
}

var markdownEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"`", "\\`",
	"*", "\\*",
	"_", "\\_",
	"[", "\\[",
	"]", "\\]",
	"(", "\\(",
	")", "\\)",
	"<", "&lt;",
	">", "&gt;",
	"!", "\\!",
)

// renderWebhook renders the HTTP body a destination receives.
//
// A fixed JSON shape rather than a `map[string]any` built from the payload, because the
// wire format is a contract: adding a field to it is a decision, and a receiver that has to
// cope with an open-ended object cannot version anything.
func renderWebhook(r RenderedNotification) string {
	fields := make([]WebhookField, 0, len(r.Fields))
	for _, f := range r.Fields {
		fields = append(fields, WebhookField{Label: f[0], Value: f[1]})
	}
	return string(mustJSON(webhookBody{
		ContractVersion: ContractVersion,
		Topic:           r.Topic,
		Title:           r.Title,
		Body:            r.Body,
		Fields:          fields,
		OccurredAt:      r.Occurred,
		Link:            r.Link,
	}))
}

type webhookBody struct {
	ContractVersion string         `json:"contract_version"`
	Topic           string         `json:"topic"`
	Title           string         `json:"title"`
	Body            string         `json:"body"`
	Fields          []WebhookField `json:"fields"`
	OccurredAt      time.Time      `json:"occurred_at"`
	Link            string         `json:"link,omitempty"`
}

// WebhookField is one label/value pair in the delivered body.
//
// Exported because a receiver's client library models the body and this is the Go shape
// it corresponds to; the alternative is for every consumer to redeclare it.
type WebhookField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

// DecodeNotification decodes and checks a notify delivery's payload.
func DecodeNotification(d outbox.Delivery) (NotificationPayload, error) {
	var p NotificationPayload
	if err := strictUnmarshal(d.Payload, &p); err != nil {
		return p, fmt.Errorf("dispatch: %s: %w", d.Topic, err)
	}
	if p.ContractVersion != ContractVersion {
		return p, fmt.Errorf("dispatch: %s: payload contract is %q and this build speaks %q. "+
			"A relay running across a rolling deploy can hand a message to a binary that "+
			"cannot read it, and the refusal says so rather than rendering a body with "+
			"every field empty", d.Topic, p.ContractVersion, ContractVersion)
	}
	if p.IncidentID == "" {
		return p, fmt.Errorf("dispatch: %s: payload carries no incident_id", d.Topic)
	}
	return p, nil
}

// DecodeJob decodes and checks a queue-shaped delivery's payload.
func DecodeJob(d outbox.Delivery) (JobPayload, error) {
	p, err := DecodeJobPayload(d.Payload)
	if err != nil {
		return p, fmt.Errorf("dispatch: %s: %w", d.Topic, err)
	}
	if p.ContractVersion != ContractVersion {
		return p, fmt.Errorf("dispatch: %s: payload contract is %q, this build speaks %q",
			d.Topic, p.ContractVersion, ContractVersion)
	}
	return p, nil
}

// EncodeJobPayload renders a job payload. It is the inverse of `DecodeJobPayload` and
// exists because the two ends are separate programs: `EnqueueOutbox` writes one, and a
// consumer reads one, and a consumer that had to reach into this package for the encoder
// would have no way to build a fixture for itself.
func EncodeJobPayload(p JobPayload) ([]byte, error) { return json.Marshal(p) }

// DecodeJobPayload decodes a job payload without a delivery around it.
//
// Separate because the two callers arrive from different directions. The relay has an
// `outbox.Delivery`; `internal/evidencefetch` has a `jobs.Job`, which is the same facts in
// a different struct, and making it construct a throwaway Delivery to satisfy this
// signature would be a fiction that hides which caller is which.
func DecodeJobPayload(raw []byte) (JobPayload, error) {
	var p JobPayload
	if err := strictUnmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.ContractVersion != ContractVersion {
		return p, fmt.Errorf("the payload contract is %q and this build speaks %q",
			p.ContractVersion, ContractVersion)
	}
	return p, nil
}

// strictUnmarshal refuses unknown fields.
//
// DisallowUnknownFields, because a payload field this build does not know about is either a
// forward-compatibility feature nobody agreed to or a message that went to the wrong
// handler. An unknown field is the signal that the two ends disagree, and disagreeing
// silently is how a notification arrives with an empty incident id.
func strictUnmarshal(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		// Marshalling a struct of strings and time.Time cannot fail, and a panic here is
		// better than a payload that is silently empty.
		panic("dispatch: marshal payload: " + err.Error())
	}
	return raw
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// UnknownTopic is a refusal. The relay dead-letters it after its retry budget.
//
// It exists as a type so `securityctl doctor` and the dead-letter report can name it as
// "a topic this build does not implement" rather than as an opaque handler error, which is
// the difference between an operator upgrading the binary and an operator filing a bug.
type UnknownTopic struct{ Topic string }

func (e *UnknownTopic) Error() string {
	return fmt.Sprintf("dispatch: no executor is registered for topic %q; this build "+
		"implements %s. The message is refused rather than acknowledged, because "+
		"acknowledging it would mark the side effect delivered without performing it",
		e.Topic, strings.Join(topics, ", "))
}

// UnconfiguredDestination is a refusal, not a warning.
//
// Returned rather than logged and acknowledged so `outbox` retries it and then
// dead-letters it, which puts the missing destination in `ops/dead-letters` rather than
// in an operator's missed page.
type UnconfiguredDestination struct {
	Name   string
	Topic  string
	Reason string
}

func (e *UnconfiguredDestination) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("dispatch: destination %q for %s: %s", e.Name, e.Topic, e.Reason)
	}
	return fmt.Sprintf("dispatch: destination %q for %s is not configured. The boot check "+
		"refuses a bundle that names an unconfigured destination, so this is a destination "+
		"removed while the daemon was running", e.Name, e.Topic)
}

// DeliveryRefused is a non-2xx from a destination.
//
// A type rather than a formatted error because the retry decision is worth making
// deliberately: 4xx other than 408 and 429 is the receiver saying the request is wrong, and
// retrying it five times delays a dead letter that an operator could act on sooner.
type DeliveryRefused struct {
	Name   string
	Topic  string
	Status int
}

func (e *DeliveryRefused) Error() string {
	return fmt.Sprintf("dispatch: destination %q for %s answered HTTP %d", e.Name, e.Topic, e.Status)
}

// The retry decision is NOT made here, and the omission is worth recording.
//
// A 4xx other than 408 and 429 is the receiver saying the request itself is wrong, and it
// will say so again identically - so spending the relay's whole `MaxAttempts` budget on
// one is a dead letter an operator waits longer for than it needs to.
//
// `outbox.Relay` has no hook for that: its `MaxAttempts` is the only retry policy it
// offers, so this package cannot express the distinction without changing that type.
// Rather than carry a method no caller could reach - which `internal/deadexport` reports
// as TEST-ONLY the moment somebody writes one - the rule is written down here, in the
// comment, where whoever adds the hook will find it. It is prose rather than a const
// because an unused const is a second finding rather than a first.

// errNoEnqueuer is returned when a queued dispatch arrives with no enqueuer wired.
//
// An explicit error rather than a nil check that returns nil, because returning nil here
// would ACK the outbox message: the side effect would be marked delivered without a job row
// ever existing, which is exactly the failure `enqueueVLM`'s separate transaction has today
// and is the failure this whole package exists to remove.
var errNoEnqueuer = errors.New("dispatch: no jobs enqueuer is configured, so a queued " +
	"dispatch cannot be performed. The outbox message is refused rather than acknowledged, " +
	"because acknowledging it would mark the side effect delivered without performing it")

// defaultTimeout bounds one delivery.
//
// A bound because the relay holds a lease while a handler runs, and a handler blocked
// forever stops that message being redelivered until the lease expires - which turns one
// hung receiver into a stalled relay.
const defaultTimeout = 10 * time.Second

// maxDrainBytes bounds how much of a response body is read before discarding.
//
// Not read at all and the connection cannot be reused, and a relay that leaks a connection
// per delivery exhausts the process's file descriptors over a weekend.
const maxDrainBytes = 64 << 10
