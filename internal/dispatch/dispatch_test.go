package dispatch

// The dispatch executor: four dispatch types, a closed set of renderers, and a boot
// refusal for a bundle that names a destination nobody configured.
//
// INTERNAL test package, not `dispatch_test`. That is a change of convention for this
// directory and it is deliberate: `internal/deadexport` found twenty exported symbols in
// this package that nothing outside read, because a test is the only thing that read
// them - and a test-only reader is exactly the shape that gate exists to report. Writing
// the tests inside the package lets the API shrink to what product code actually needs,
// and the tests still exercise the exported path (`Dispatcher.Handler`) because nothing
// stops an internal test from calling it.
//
// The alternative - exporting twenty symbols so a test can see them - is how a package
// ends up with a public surface nobody chose.
//
// # What is asserted here, and why each one is a control
//
// The package's whole value is that a rule's `then.dispatch` now does something. That
// value is only real if the paths which would silently do nothing instead REFUSE, so most
// of what follows is about refusals:
//
//   - A topic with no executor is refused, not acknowledged. `outbox`'s handler contract
//     says a `nil` return means delivered, so a `nil` here would mark a side effect
//     delivered that nobody performed - strictly worse than losing it, because the row says
//     it succeeded.
//   - A destination that is not configured is refused, not logged. Same reasoning, plus
//     `outbox` dead-letters it, which puts the gap in `ops/dead-letters`.
//   - A VLM job with no `evidence_id` is refused at enqueue time. `airuntime.Worker`
//     returns `DegradationAbsent` before it contacts the runtime when a job names no
//     evidence, so enqueueing it produces a degraded observation on every run and tells
//     the operator the model could not tell when in fact the job never reached it.
//   - A payload from a different contract version is refused rather than rendered with
//     every field empty.
//   - An unknown field in a payload is refused. The two ends disagreeing is the signal, and
//     disagreeing silently is how a notification arrives with no incident id.
//   - A destination URL that is plaintext, carries a password, or points at a private or
//     link-local address is refused.
//
// # The idempotency obligation
//
// `outbox` gives at-least-once delivery and says a handler "must be idempotent on
// IdempotencyKey". Both halves are checked by construction here: a queued dispatch keys
// its job on the delivery's own key, and the notification carries that key in the header
// and in the body so a receiver can dedupe on either.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sergey-Bar/vision/internal/outbox"
	"github.com/Sergey-Bar/vision/internal/rules"
)

// delivery builds one outbox delivery of the given topic and payload.
func delivery(topic, idemKey string, payload any) outbox.Delivery {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return outbox.Delivery{
		Message: outbox.Message{
			TenantID:       "tenant-1",
			SiteID:         "site-1",
			Topic:          topic,
			IdempotencyKey: idemKey,
			Payload:        raw,
		},
	}
}

func notification(key string) NotificationPayload {
	return NotificationPayload{
		IncidentID:      "inc-1",
		RuleID:          "core.person-in-restricted-zone-after-hours",
		Title:           "Person in a restricted zone after hours",
		Severity:        "high",
		SiteID:          "site-1",
		Explanation:     "Two independent signals agreed within 5s.",
		Target:          "operator",
		IdempotencyKey:  key,
		ContractVersion: ContractVersion,
	}
}

// ---------------------------------------------------------------------------
// topics
// ---------------------------------------------------------------------------

// TestEveryDispatchTypeHasATopicAndEveryTopicADispatchType is the bijection the two
// closed sets depend on.
//
// `outbox` dead-letters an unknown topic rather than ignoring it, which is right and which
// means an asymmetry between these two sets is a dead letter rather than a dropped message.
// The check is a bijection in both directions rather than "every type has a topic",
// because a topic with no dispatch type is the direction nothing would otherwise catch: a
// handler that is registered, reachable and never requested.
func TestEveryDispatchTypeHasATopicAndEveryTopicADispatchType(t *testing.T) {
	covered := map[string]string{}
	for typ := range rules.DispatchTypes {
		topic, err := topicFor(typ)
		if err != nil {
			t.Errorf("dispatch type %q has no topic: %v. A type that validates against the "+
				"closed set and has no topic is a rule an operator believes is running",
				typ, err)
			continue
		}
		covered[topic] = typ
	}
	for _, topic := range topics {
		if _, ok := covered[topic]; !ok {
			t.Errorf("topic %q is registered by this package and no dispatch type maps to "+
				"it. Either a dispatch type was removed from rules.DispatchTypes and this "+
				"topic is dead, or one was added and topicFor was not", topic)
		}
	}
	if len(covered) != len(topics) {
		t.Errorf("%d dispatch types map onto %d topics; the two closed sets must be the "+
			"same size", len(covered), len(topics))
	}
}

// TestAnUnknownDispatchTypeIsRefusedNotDefaulted pins that topicFor errors.
//
// A `map[string]string` built at init cannot fail: it returns "" for a miss, and "" is an
// empty topic that `outbox` dead-letters with a message naming nothing. The error is what
// lets `CheckDestinations` report the type by name at startup.
func TestAnUnknownDispatchTypeIsRefusedNotDefaulted(t *testing.T) {
	topic, err := topicFor("send_an_email")
	if err == nil {
		t.Fatalf("an unknown dispatch type produced topic %q instead of an error", topic)
	}
	if topic != "" {
		t.Errorf("topic is %q on the error path; it has to be empty so a caller that ignores "+
			"the error cannot enqueue under a blank topic", topic)
	}
	// The message has to name the closed set, because "unknown type" without a list is not
	// something an operator can act on.
	for _, want := range []string{"notify_operator", "fetch_evidence"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so a reader cannot tell what the closed "+
				"set is: %v", want, err)
		}
	}
}

// ---------------------------------------------------------------------------
// The handler
// ---------------------------------------------------------------------------

// TestAnUnknownTopicIsRefusedRatherThanAcknowledged is the most important assertion in
// this file.
//
// `outbox.Handler`'s contract is that returning nil means delivered. A handler that
// returned nil for an unknown topic would write a row saying a notification was sent when
// no notification was sent - and unlike a lost message, that one is not visible anywhere.
func TestAnUnknownTopicIsRefusedRatherThanAcknowledged(t *testing.T) {
	d := &Dispatcher{}
	err := d.Handler()(context.Background(), delivery("send_a_carrier_pigeon", "k1", map[string]any{}))
	if err == nil {
		t.Fatal("the handler accepted a topic it has no executor for and returned nil, which " +
			"outbox reads as DELIVERED. A side effect nobody performed, recorded as " +
			"performed, is worse than a lost one")
	}
	var unknown *UnknownTopic
	if !errors.As(err, &unknown) {
		t.Fatalf("error is %T (%v), want *UnknownTopic so ops can tell a version "+
			"mismatch from a broken receiver", err, err)
	}
	if !strings.Contains(err.Error(), topics[0]) {
		t.Errorf("the refusal does not list what this build implements: %v", err)
	}
}

// TestEveryRegisteredTopicHasAnExecutor closes the other half of the same hole: a topic
// this package publishes as handled but that has no executor would dead-letter every
// message on it, permanently, with no way for an operator to tell why.
func TestEveryRegisteredTopicHasAnExecutor(t *testing.T) {
	d := &Dispatcher{}
	h := d.Handler()
	for _, topic := range topics {
		// A payload that fails to decode is fine here: the point is that the TOPIC is
		// routed to an executor rather than refused as unknown. An executor that refuses
		// for a different reason proves the routing exists.
		err := h(context.Background(), delivery(topic, "k1", map[string]any{}))
		var unknown *UnknownTopic
		if errors.As(err, &unknown) {
			t.Errorf("topic %q is in topics but the handler refused it as unknown, "+
				"so every message on it would dead-letter", topic)
		}
	}
}

// TestNotificationDeliveryPostsTheRenderedBody is the positive case: the thing the whole
// package exists to make happen.
func TestNotificationDeliveryPostsTheRenderedBody(t *testing.T) {
	var gotAuth, gotIdem string
	var got RenderedNotification
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdem = r.Header.Get("Idempotency-Key")
		var body struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = RenderedNotification{Title: body.Title, Body: body.Body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// A TLS server, not a plain one, and that is not incidental: the destinations loader
	// refuses an http:// URL, so the first version of this test used `httptest.NewServer`
	// and failed on the very control it was exercising. A test that has to use the insecure
	// transport to test the secure path is a test that cannot be written, which is the
	// correct outcome for a control this strict.
	t.Setenv("TEST_DISPATCH_TOKEN", "s3cret-token")
	d := &Dispatcher{
		Dests: testDests(t, "operator", srv.URL, "TEST_DISPATCH_TOKEN"),
	}
	d.setHTTPClient(srv.Client())

	key := "inc-1:operator"
	if err := d.Handler()(context.Background(),
		delivery(topicNotifyOperator, key, notification(key))); err != nil {
		t.Fatalf("a configured, reachable destination refused the delivery: %v", err)
	}

	if got.Title != "Person in a restricted zone after hours" {
		t.Errorf("title is %q", got.Title)
	}
	if !strings.Contains(got.Body, "inc-1") {
		t.Errorf("body does not carry the incident id an operator needs: %q", got.Body)
	}
	if gotAuth != "Bearer s3cret-token" {
		t.Errorf("Authorization is %q; the secret_ref has to reach the wire as a bearer token", gotAuth)
	}
	if gotIdem != key {
		t.Errorf("Idempotency-Key is %q, want the delivery's own key %q. A relay that "+
			"retries presents the same key, and a receiver that cannot dedupe on it gets "+
			"the same page twice", gotIdem, key)
	}
}

// TestAnUnsetSecretRefIsRefusedNotSentUnauthenticated is the reason a secret is read at
// delivery rather than cached at boot, stated as a control.
//
// A destination configured with a `secret_ref` whose variable is unset must NOT be sent an
// unauthenticated request: the receiver either rejects it (and the message dead-letters)
// or accepts it (and the operator's incident data has been posted somewhere the operator
// believed was authenticated). The refusal is the correct outcome in both cases.
func TestAnUnsetSecretRefIsRefusedNotSentUnauthenticated(t *testing.T) {
	var posted bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// An explicitly EMPTY variable rather than an absent one, because "unset" and "set to
	// nothing" are the two states an operator hits and both have to refuse. `t.Setenv`
	// with an empty value sets it to empty and restores the previous value afterwards,
	// which is the shape this case needs.
	t.Setenv("TEST_ABSENT_TOKEN", "")
	d := &Dispatcher{Dests: testDests(t, "operator", srv.URL, "TEST_ABSENT_TOKEN")}
	d.setHTTPClient(srv.Client())

	key := "inc-1:operator"
	err := d.Handler()(context.Background(),
		delivery(topicNotifyOperator, key, notification(key)))
	if err == nil {
		t.Fatal("a destination with an unset secret_ref delivered anyway. That posts the " +
			"incident to a receiver that was supposed to require a token")
	}
	var missing *UnconfiguredDestination
	if !errors.As(err, &missing) {
		t.Fatalf("error is %T (%v), want *UnconfiguredDestination", err, err)
	}
	if !strings.Contains(err.Error(), "TEST_ABSENT_TOKEN") {
		t.Errorf("the refusal does not name the environment variable that is unset, so an "+
			"operator cannot fix it: %v", err)
	}
	if posted {
		t.Error("a request reached the receiver despite the refusal")
	}
}

// TestARedeliveryPresentsTheSameIdempotencyKey is the at-least-once obligation, tested by
// running the same delivery twice and comparing what the receiver saw.
//
// `outbox` is explicit that the relay is at-least-once and that a handler must be
// idempotent. For a notification that means the receiver can tell the two apart; for a
// queued dispatch it means the job is written once.
func TestARedeliveryPresentsTheSameIdempotencyKey(t *testing.T) {
	var keys []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := &Dispatcher{Dests: testDests(t, "operator", srv.URL, "")}
	d.setHTTPClient(srv.Client())

	key := "inc-7:operator"
	dl := delivery(topicNotifyOperator, key, notification(key))
	h := d.Handler()
	for range 2 {
		if err := h(context.Background(), dl); err != nil {
			t.Fatalf("delivery: %v", err)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("the receiver saw %d requests, want 2", len(keys))
	}
	if keys[0] != keys[1] || keys[0] != key {
		t.Errorf("the two deliveries presented %q and %q, want the same key %q. A receiver "+
			"cannot dedupe what it cannot recognise", keys[0], keys[1], key)
	}
}

// TestAQueuedDispatchWithNoEnqueuerIsRefused is invariant 1 applied to a new side effect.
//
// Returning nil here would ACK the outbox message, and the outbox row would say DELIVERED
// while no job row exists. That is the exact failure `enqueueVLM`'s separate transaction
// has today: the incident commits, the work is lost, and nothing records that the work was
// supposed to happen.
func TestAQueuedDispatchWithNoEnqueuerIsRefused(t *testing.T) {
	d := &Dispatcher{}
	p := JobPayload{
		IncidentID: "inc-1", EventID: "evt-1", EvidenceID: "ev-1",
		ContractVersion: ContractVersion,
	}
	err := d.Handler()(context.Background(),
		delivery(topicRequestVLM, "inc-1:request_vlm", p))
	if !errors.Is(err, errNoEnqueuer) {
		t.Fatalf("error is %v, want errNoEnqueuer. Without an enqueuer the side effect "+
			"cannot be performed and the message must NOT be acknowledged", err)
	}
}

// TestAVLMJobWithNoEvidenceIsRefused catches the AI path's second blocker at enqueue time.
//
// `airuntime/worker.go` returns `DegradationAbsent` at its evidence check and returns
// before it ever contacts the runtime. So a job with an incident and an event but no
// evidence produces a degraded observation on every single run, and an operator reading it
// concludes the model could not tell - when in fact the job never reached the model. That
// is invariant 3's exact failure mode: an absence recorded as a negative.
func TestAVLMJobWithNoEvidenceIsRefused(t *testing.T) {
	cases := []struct {
		name string
		p    JobPayload
		want string
	}{
		{"no incident", JobPayload{EventID: "evt-1", EvidenceID: "ev-1",
			ContractVersion: ContractVersion}, "incident_id"},
		{"no event", JobPayload{IncidentID: "inc-1", EvidenceID: "ev-1",
			ContractVersion: ContractVersion}, "event_id"},
		{"no evidence", JobPayload{IncidentID: "inc-1", EventID: "evt-1",
			ContractVersion: ContractVersion}, "evidence_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.Queue(topicRequestVLM)
			if err == nil {
				t.Fatalf("a VLM job with %s was accepted. The worker returns "+
					"DegradationAbsent before contacting the runtime, so this job would "+
					"degrade on every run", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the missing field %q: %v", tc.want, err)
			}
		})
	}
}

// TestAVLMJobWithEvidenceIsAccepted is the other direction, and it matters for the same
// reason: a gate that refuses everything is a gate nobody trusts and nobody keeps.
func TestAVLMJobWithEvidenceIsAccepted(t *testing.T) {
	p := JobPayload{
		IncidentID: "inc-1", EventID: "evt-1", EvidenceID: "ev-1",
		ContractVersion: ContractVersion,
	}
	q, err := p.Queue(topicRequestVLM)
	if err != nil {
		t.Fatalf("a complete VLM job was refused: %v", err)
	}
	if q != "vlm" {
		t.Errorf("queue is %q, want the VLM queue", q)
	}
}

// TestAQueuedJobIsKeyedOnTheDeliverysOwnIdempotencyKey is what makes a relay retry safe.
//
// `jobs.Enqueue` has `ON CONFLICT (tenant_id, queue, idempotency_key) DO NOTHING`. That
// only prevents a duplicate inference if the key is the SAME on the retry, so deriving it
// per attempt instead of from the delivery would turn at-least-once delivery into
// at-least-once duplicate inference - at roughly $0.15 a pop.
func TestAQueuedJobIsKeyedOnTheDeliverysOwnIdempotencyKey(t *testing.T) {
	dl := delivery(topicRequestVLM, "inc-9:request_vlm", JobPayload{
		IncidentID: "inc-9", EventID: "evt-9", EvidenceID: "ev-9",
		ContractVersion: ContractVersion,
	})
	in := JobPayload{IncidentID: "inc-9", EventID: "evt-9", EvidenceID: "ev-9",
		ContractVersion: ContractVersion}.JobIn("vlm", dl)

	if in.IdempotencyKey != dl.IdempotencyKey {
		t.Errorf("job key is %q, want the delivery's %q. A fresh key per attempt makes every "+
			"relay retry a second inference", in.IdempotencyKey, dl.IdempotencyKey)
	}
	if in.TenantID != dl.TenantID {
		t.Errorf("job tenant is %q, want the delivery's %q", in.TenantID, dl.TenantID)
	}
}

// TestAnEvidenceFetchWithNeitherIdIsRefused stops a fetch that has nothing to fetch for.
func TestAnEvidenceFetchWithNeitherIdIsRefused(t *testing.T) {
	p := JobPayload{ContractVersion: ContractVersion}
	if _, err := p.Queue(topicFetchEvidence); err == nil {
		t.Fatal("an evidence fetch naming neither an incident nor an event was accepted")
	}
}

// ---------------------------------------------------------------------------
// Payload decoding
// ---------------------------------------------------------------------------

// TestAPayloadFromAnotherContractVersionIsRefused catches rolling-deploy skew.
//
// A relay running across a rolling deploy can hand a message written by one build to a
// binary that cannot read it. Decoding leniently produces a notification with an empty
// incident id and an empty title, which is worse than a dead letter: it arrives, it is
// real, and it says nothing.
func TestAPayloadFromAnotherContractVersionIsRefused(t *testing.T) {
	d := &Dispatcher{}
	stale := notification("inc-1")
	stale.ContractVersion = "security.io/dispatch/v0"

	err := d.Handler()(context.Background(),
		delivery(topicNotifyOperator, "inc-1", stale))
	if err == nil {
		t.Fatal("a payload from contract v0 was accepted by a build that speaks v1")
	}
	if !strings.Contains(err.Error(), ContractVersion) {
		t.Errorf("the refusal does not name the contract this build speaks: %v", err)
	}
}

// TestAnUnknownPayloadFieldIsRefused is the two-ends-disagree signal.
//
// `DisallowUnknownFields` is deliberate. Forward compatibility was available - ignore the
// field - and it would have produced exactly the defect this work removes: a message that
// decodes into a struct whose field is not the field that was sent, arriving successfully
// with the wrong contents.
func TestAnUnknownPayloadFieldIsRefused(t *testing.T) {
	d := &Dispatcher{}
	err := d.Handler()(context.Background(),
		delivery(topicNotifyOperator, "inc-1", map[string]any{
			"incident_id":      "inc-1",
			"contract_version": ContractVersion,
			"webhook_url":      "https://attacker.example/collect",
		}))
	if err == nil {
		t.Fatal("a payload carrying an unknown field decoded successfully. The field this " +
			"build does not know about is the signal that the two ends disagree, and " +
			"disagreeing silently is how a message arrives with the wrong contents")
	}
	if !strings.Contains(err.Error(), "webhook_url") {
		t.Errorf("the refusal does not name the offending field: %v", err)
	}
}

// TestANotificationWithNoIncidentIsRefused stops an empty page.
//
// Every other field of a notification is a description of the incident. Without the id
// there is nothing to describe, and a receiver would store a title with nothing to open.
func TestANotificationWithNoIncidentIsRefused(t *testing.T) {
	d := &Dispatcher{}
	p := notification("inc-1")
	p.IncidentID = ""
	err := d.Handler()(context.Background(),
		delivery(topicNotifyOperator, "inc-1", p))
	if err == nil {
		t.Fatal("a notification with no incident_id was accepted")
	}
}

// ---------------------------------------------------------------------------
// Renderers
// ---------------------------------------------------------------------------

// TestTheRendererIsClosed pins that a non-notify topic has no notification renderer.
//
// "Render whatever the payload says" is how an incident title becomes markup in somebody's
// dashboard, so the set of renderers is fixed and the refusal names it.
func TestTheRendererIsClosed(t *testing.T) {
	_, err := renderNotification(topicRequestVLM, notification("k"))
	if err == nil {
		t.Fatal("a VLM topic produced a notification rendering")
	}
	if !strings.Contains(err.Error(), topicNotifyOperator) {
		t.Errorf("the refusal does not list the topics that DO have a renderer: %v", err)
	}
}

// TestTheRenderedBodyIsByteStable is why the field order is a slice and not a map.
//
// Two renders of the same notification have to be identical, because an operator comparing
// two pages to see what changed needs the lines in the same order, and because a golden
// test compares bytes. Map iteration makes that a coin flip per process.
func TestTheRenderedBodyIsByteStable(t *testing.T) {
	p := notification("inc-1")
	first, err := renderNotification(topicNotifyOperator, p)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for range 20 {
		again, err := renderNotification(topicNotifyOperator, p)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if again.Body != first.Body {
			t.Fatalf("two renders of one payload differ:\n%q\n%q", first.Body, again.Body)
		}
	}
}

// TestTheRendererEscapesMarkup is the renderer's whole security surface.
//
// An incident title comes from a rule's `then.title`, which is operator-editable in a
// bundle, and a title may legitimately contain a vendor's message text. A receiver that
// renders Markdown would execute what is in it.
//
// The assertion is scoped to the TITLE LINE rather than the whole body, and the scoping is
// not tidiness. The first version checked the body for `](` and failed - correctly - on the
// console link the renderer appends, `[Open in console](/incidents/inc-1)`, which contains
// `](` because this renderer emits Markdown on purpose. A control that cannot distinguish
// "the hostile input survived" from "the renderer produced its own markup" trains its
// reader to ignore it.
func TestTheRendererEscapesMarkup(t *testing.T) {
	hostile := []struct{ in, stillPresent string }{
		{"[click me](https://attacker.example)", "]("},
		{"**bold**", "**bold**"},
		{"<script>alert(1)</script>", "<script>"},
		{"`code`", "`code`"},
		{"![img](https://attacker.example/pixel)", "![img]"},
	}
	for _, tc := range hostile {
		p := notification("inc-1")
		p.Title = tc.in
		r, err := renderNotification(topicNotifyOperator, p)
		if err != nil {
			t.Fatalf("render %q: %v", tc.in, err)
		}
		titleLine, _, found := strings.Cut(r.Body, "\n")
		if !found {
			t.Fatalf("the rendered body has no title line:\n%q", r.Body)
		}
		if strings.Contains(titleLine, tc.stillPresent) {
			t.Errorf("a title of %q rendered with %q still present in the title line:\n%s",
				tc.in, tc.stillPresent, titleLine)
		}
	}
}

// TestTheConsoleLinkCarriesOnlyTheIncidentID pins that no configured base URL leaks into a
// notification.
//
// A URL taken from configuration and rendered into a notification is one more place a site
// can send an operator somewhere other than their own console. The route already carries
// the tenant from the session.
func TestTheConsoleLinkCarriesOnlyTheIncidentID(t *testing.T) {
	r, err := renderNotification(topicNotifyOperator, notification("inc-1"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if r.Link != "/incidents/inc-1" {
		t.Errorf("link is %q, want the relative console route", r.Link)
	}
	if strings.Contains(r.Link, "://") {
		t.Errorf("the link is absolute: %q", r.Link)
	}
}

// ---------------------------------------------------------------------------
// The boot check
// ---------------------------------------------------------------------------

// TestAConfiguredBundleStarts is the positive case for the boot check, and it is here so
// that the refusals below are refusals rather than a check that refuses everything.
func TestAConfiguredBundleStarts(t *testing.T) {
	d := &Dispatcher{Dests: twoDests(t)}
	b := probeBundle(t, map[string][]string{
		"probe.notify":    {"notify_operator/operator"},
		"probe.supervise": {"notify_supervisor/supervisor"},
		"probe.fetch":     {"fetch_evidence/pre-event"},
		"probe.vlm":       {"request_vlm/restricted-person"},
	})
	if err := d.CheckDestinations(b); err != nil {
		t.Fatalf("a bundle whose every dispatch target resolves was refused: %v", err)
	}
}

// TestAnUnconfiguredDestinationRefusesTheBoot is the point of the check.
//
// The alternative is a dead letter discovered at 03:00 by whoever is on call, from a row
// that says only that the message was not delivered. A refusal at startup names the rule,
// the target and the destinations that DO exist.
func TestAnUnconfiguredDestinationRefusesTheBoot(t *testing.T) {
	d := &Dispatcher{Dests: twoDests(t)}
	b := probeBundle(t, map[string][]string{
		"probe.notify": {"notify_operator/on-call-when-nobody-answers"},
	})
	err := d.CheckDestinations(b)
	if err == nil {
		t.Fatal("a bundle naming a destination this install does not have started anyway. " +
			"The first notification for that rule would dead-letter, discovered at 03:00")
	}
	var refusal *StartupRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error is %T (%v), want *StartupRefusal so main can print the "+
			"problems as a list", err, err)
	}
	// The message has to carry all three facts: which rule, which target, and what does
	// exist. A refusal an operator cannot act on is a refusal they work around by deleting
	// the rule.
	for _, want := range []string{"probe.notify", "on-call-when-nobody-answers", "operator", "supervisor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so an operator cannot act on it:\n%v",
				want, err)
		}
	}
}

// TestNoDestinationsAtAllRefusesABundleThatNotifies is the install that has the bundle
// shipped and never wrote the file.
//
// This is the shape an operator actually hits: the built-in bundle notifies, no
// destinations file was written, and without the check the product starts and looks fine.
func TestNoDestinationsAtAllRefusesABundleThatNotifies(t *testing.T) {
	d := &Dispatcher{}
	err := d.CheckDestinations(probeBundle(t, map[string][]string{
		"probe.notify": {"notify_operator/nothing-configured"},
	}))
	if err == nil {
		t.Fatal("an install with no destinations and a bundle that notifies started. That " +
			"product opens incidents and performs no side effect at all")
	}
	if !strings.Contains(err.Error(), DefaultDestinationsPath) {
		t.Errorf("the refusal does not say where to write the file: %v", err)
	}
}

// TestABundleThatOnlyQueuesNeedsNoDestinations keeps the check from refusing something it
// should not.
//
// A queued dispatch's `target` names WHAT to fetch, not where to send it, so checking it
// against the destinations file would refuse every bundle that asks for evidence - and
// refusing that would push operators to delete their evidence requests to start the daemon.
func TestABundleThatOnlyQueuesNeedsNoDestinations(t *testing.T) {
	d := &Dispatcher{}
	if err := d.CheckDestinations(probeBundle(t, map[string][]string{
		"probe.fetch": {"fetch_evidence/pre-event"},
		"probe.vlm":   {"request_vlm/restricted-person"},
	})); err != nil {
		t.Fatalf("a bundle whose dispatches are all queue-shaped was refused for missing "+
			"destinations: %v", err)
	}
}

// TestADisabledRuleIsNotHeldAgainstTheOperator makes "disable a rule" possible.
//
// A rule with `enabled: false` is not going to notify anyone. Refusing the daemon over it
// would mean an operator has to edit the destinations file before they can turn a rule
// off, which is a strange coupling and one that would be discovered at the worst moment.
func TestADisabledRuleIsNotHeldAgainstTheOperator(t *testing.T) {
	d := &Dispatcher{}
	b := probeBundle(t, map[string][]string{
		"probe.notify": {"notify_operator/on-call"},
	})
	for i := range b.Compiled {
		b.Compiled[i].Enabled = false
	}
	if err := d.CheckDestinations(b); err != nil {
		t.Fatalf("a disabled rule's unconfigured destination refused the boot: %v", err)
	}
}

// TestABundleWithNoDispatchIsFine is the last positive case, and it matters: an install
// whose bundle does not notify must not need a destinations file.
func TestABundleWithNoDispatchIsFine(t *testing.T) {
	d := &Dispatcher{}
	if err := d.CheckDestinations(probeBundle(t, nil)); err != nil {
		t.Fatalf("a bundle that dispatches nothing was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The destinations loader, which is where the reachability policy actually lives
// ---------------------------------------------------------------------------

// TestTheDestinationLoaderAppliesTheReachabilityPolicy covers the three rules
// `NewDestinations` deliberately does not apply.
//
// The split between the two constructors exists so the delivery tests can use an
// `httptest` loopback listener, and a split like that is only safe if the half that was
// skipped is itself covered. These are the assertions that do that.
//
// The third case is the one that matters most: a destinations file - the thing an operator
// writes, and the thing a rule's `target` is resolved through - pointing at the cloud
// metadata address would let a dispatch carry evidence-derived text and a bearer token to a
// service that hands credentials to whoever asks.
func TestTheDestinationLoaderAppliesTheReachabilityPolicy(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{"loopback", "https://127.0.0.1:8443/hook", "loopback"},
		{"loopback name", "https://localhost:8443/hook", "loopback"},
		{"cloud metadata", "https://169.254.169.254/latest/meta-data/", "link-local"},
		{"rfc1918", "https://10.1.2.3/hook", "RFC 1918"},
		{"unspecified", "https://0.0.0.0/hook", "unspecified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "d.yaml")
			body := "version: 1\ndestinations:\n  - name: op\n    url: " + tc.url + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, err := LoadDestinations(path)
			if err == nil {
				t.Fatalf("the loader accepted %s. That address is reachable from this "+
					"process, and a destinations file is the only thing standing between a "+
					"rule bundle's `target` and it", tc.url)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say what is wrong (%q): %v", tc.want, err)
			}
		})
	}
}

// TestTheDestinationLoaderRefusesPlaintextAndURLCredentials covers the other two rules,
// which apply on both constructors because a credential in a URL reaches every log that
// prints it and a bearer token over plaintext goes to whoever is on the wire.
func TestTheDestinationLoaderRefusesPlaintextAndURLCredentials(t *testing.T) {
	for _, tc := range []struct{ url, want string }{
		{"http://hooks.example.com/x", "https"},
		{"https://user:pass@hooks.example.com/x", "userinfo"},
	} {
		if _, err := newDestinations([]Destination{
			{Name: "op", URL: tc.url},
		}); err == nil {
			t.Errorf("the constructor accepted %s", tc.url)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("the refusal for %s does not mention %q: %v", tc.url, tc.want, err)
		}
	}
}

// TestInsecureSkipVerifyIsRefusedEverywhere is the one setting a caller cannot be trusted
// with.
//
// Unlike a host choice - where a loopback receiver is a legitimate thing for a test - turning
// off certificate verification has no programmatic use and no legitimate install. It is
// refused by both constructors so there is no path that accepts it.
func TestInsecureSkipVerifyIsRefusedEverywhere(t *testing.T) {
	entry := []Destination{
		{Name: "op", URL: "https://hooks.example.com/x", InsecureSkipVerify: true},
	}
	if _, err := newDestinations(entry); err == nil {
		t.Error("NewDestinations accepted insecure_skip_verify")
	}
	path := filepath.Join(t.TempDir(), "d.yaml")
	body := "version: 1\ndestinations:\n  - name: op\n    url: https://hooks.example.com/x\n" +
		"    insecure_skip_verify: true\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadDestinations(path); err == nil {
		t.Error("LoadDestinations accepted insecure_skip_verify")
	}
}

// TestTheLoaderRefusesATypoInAKeyRatherThanLeavingADestinationOpen is why KnownFields is on.
//
// `secret-ref` instead of `secret_ref` parses as an unknown key, and with lenient decoding
// the destination loads with no credential - and the operator believes they configured one.
// A silent misconfiguration of a security setting is worse than a refusal.
func TestTheLoaderRefusesATypoInAKeyRatherThanLeavingADestinationOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.yaml")
	body := "version: 1\ndestinations:\n  - name: op\n    url: https://hooks.example.com/x\n" +
		"    secret-ref: SP_TOKEN\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadDestinations(path); err == nil {
		t.Fatal("a destinations file with `secret-ref` loaded successfully, leaving the " +
			"destination with no credential while the operator believes it has one")
	}
}

// TestAMissingDestinationsFileIsAnEmptySetNotAnError keeps a notification-free install
// startable.
//
// An install that sends no notifications is a supported configuration. A loader that
// required the file would refuse the daemon for a capability the operator is not using,
// and the fix would be to write a file nobody wants rather than to turn the feature off.
func TestAMissingDestinationsFileIsAnEmptySetNotAnError(t *testing.T) {
	d, err := LoadDestinations(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("a missing destinations file is an error: %v", err)
	}
	if !d.Empty() {
		t.Error("a missing file produced a non-empty set")
	}
}

// TestADuplicateDestinationNameIsRefused because a duplicate makes the effective one a
// matter of file order, and that is how a site ends up notifying one of two receivers.
func TestADuplicateDestinationNameIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.yaml")
	body := "version: 1\ndestinations:\n  - name: op\n    url: https://a.example.com/x\n" +
		"  - name: op\n    url: https://b.example.com/x\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadDestinations(path); err == nil {
		t.Fatal("two destinations share a name and the loader accepted it")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeDests writes a destinations file and loads it, so the tests exercise the real
// parser rather than a hand-built Destinations.
func writeDests(t *testing.T, body string) *Destinations {
	t.Helper()
	path := filepath.Join(t.TempDir(), "destinations.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write destinations: %v", err)
	}
	d, err := LoadDestinations(path)
	if err != nil {
		t.Fatalf("load destinations: %v", err)
	}
	return d
}

// testDests builds destinations programmatically for a test receiver.
//
// `NewDestinations` rather than `LoadDestinations`, and that is the documented reason the
// constructor exists: the reachability rule refuses `httptest`'s loopback listener, which is
// a correct refusal of a correct URL. The scheme and userinfo rules still apply here, and
// `TestTheDestinationLoaderAppliesTheReachabilityPolicy` covers the rules this constructor
// deliberately does not.
func testDests(t *testing.T, name, url, secretRef string) *Destinations {
	t.Helper()
	d, err := newDestinations([]Destination{
		{Name: name, URL: url, SecretRef: secretRef},
	})
	if err != nil {
		t.Fatalf("build destinations: %v", err)
	}
	return d
}

func twoDests(t *testing.T) *Destinations {
	t.Helper()
	return writeDests(t, `version: 1
destinations:
  - name: operator
    url: https://hooks.example.internal/security-platform
    secret_ref: SP_DISPATCH_OPERATOR_TOKEN
  - name: supervisor
    url: https://security.example.com/alerts
    secret_ref: SP_DISPATCH_SUPERVISOR_TOKEN
`)
}

// probeBundle compiles a bundle of enabled rules, each with the given `type/target`
// dispatches.
func probeBundle(t *testing.T, dispatches map[string][]string) *rules.CompiledBundle {
	t.Helper()
	if dispatches == nil {
		dispatches = map[string][]string{}
	}
	var body strings.Builder
	body.WriteString("apiVersion: security.io/v1\nkind: RuleBundle\nversion: 0.1.0\n" +
		"description: boot-check fixture.\nsite:\n  siteId: site-default\n" +
		"  timezones:\n    - Asia/Jerusalem\nsettings:\n  clock:\n" +
		"    referenceSource: ntp\n    driftWarnMs: 2000\n    driftSevereMs: 10000\n" +
		"    skewTolerance: 5000\nrules:\n")
	if len(dispatches) == 0 {
		// An empty LIST, not an omitted key. The schema says `rules` is an array, and
		// leaving it out fails validation - which is worth honouring here rather than
		// working around, because this fixture is meant to compile through the same
		// path a real bundle does.
		body.WriteString("  []\n")
	}
	for id, ds := range dispatches {
		body.WriteString("  - id: " + id + "\n    description: probe.\n    enabled: true\n" +
			"    priority: 500\n    when:\n      eventType: [motion]\n    then:\n" +
			"      createIncident: true\n      dispatch:\n")
		for _, d := range ds {
			typ, target, _ := strings.Cut(d, "/")
			body.WriteString("        - type: " + typ + "\n          target: " + target + "\n")
		}
	}
	b, problems := rules.NewCompiler().Compile([]byte(body.String()))
	for _, p := range problems {
		if p.Severity == rules.SeverityError {
			t.Fatalf("probe bundle did not compile: %s", p)
		}
	}
	if b == nil {
		t.Fatal("probe bundle is nil")
	}
	return b
}
