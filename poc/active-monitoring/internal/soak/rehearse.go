package soak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sergey-Bar/vision-poc/internal/store"
	"github.com/Sergey-Bar/vision-poc/internal/xprotect"
)

// The rehearsal plan. It is the SAME table `pocctl rehearse` runs, and it is
// written here rather than imported so that a run and a pre-flight gate cannot
// disagree about what "green" means.
var rehearsalPlan = []struct {
	Fault string
	Want  string
	Why   string
}{
	{"401", "auth_failed", "a 401 refreshes the token once; a second 401 is a credential problem, not a stale token"},
	{"403", "push_refused", "a 403 is a definite no and is terminal for that detection"},
	{"400", "push_refused", "a 400 is a definite no and is terminal for that detection"},
	{"500", "retry", "a 5xx is worth another attempt"},
	{"connection-reset", "retry", "a reset connection is not a data problem; the backoff budget is what dead-letters it"},
	{"timeout", "retry", "a timeout must not wedge the worker"},
	{"truncated-json", "not-sent", "a 202 whose body arrived in half is not an accepted event"},
}

// Rehearser injects each planned fault into the run's OWN push target and
// records what the client did about it.
//
// Why this runs inside the run rather than as a separate command:
//
// AC7 asks "did the modelled faults behave". A rehearsal performed hours before
// the run answers a different question — whether the build was that way when the
// clock started. The faults have to be injected against the code path the
// measurement is exercising, and the outcome has to land in the same store the
// report reads, or the criterion is `NOT RUN` and nobody can say why.
//
// It only runs against a SIMULATOR. A real VMS has no control surface to arm a
// fault on, and pretending otherwise would mean inventing failures on somebody's
// production system. Against a live VMS this is `NOT RUN` and says so.
type Rehearser struct {
	BaseURL   string
	Store     *store.Store
	RunID     string
	HTTP      *http.Client
	NewClient func() (*xprotect.Client, error)
	Logf      func(string, ...any)
}

// IsSimulator is the only condition under which an injected fault means anything.
func (r *Rehearser) IsSimulator() bool {
	return strings.HasPrefix(r.BaseURL, "http://127.0.0.1") ||
		strings.HasPrefix(r.BaseURL, "http://localhost") ||
		strings.HasPrefix(r.BaseURL, "http://[::1]")
}

// Run performs the whole plan and records a row per fault. It returns the rows so
// the caller can log them, and it NEVER returns an error for a fault that
// behaved unexpectedly — an unexpected outcome is a result, not a failure of the
// rehearsal.
func (r *Rehearser) Run(ctx context.Context) ([]Rehearsal, error) {
	if r.Store == nil || r.NewClient == nil {
		return nil, nil
	}
	if !r.IsSimulator() {
		return nil, nil
	}
	rows := make([]Rehearsal, 0, len(rehearsalPlan))
	for _, step := range rehearsalPlan {
		row := Rehearsal{Fault: step.Fault, Expected: step.Want, At: time.Now().UTC()}
		row.Observed, row.Detail = r.one(ctx, step.Fault)
		rows = append(rows, row)
		_ = r.Store.RecordFault(ctx, r.RunID, store.Fault{
			At: row.At, Reason: "rehearsal:" + step.Fault, Detail: step.Why,
			Expected: step.Want, Observed: row.Observed,
		})
		if r.Logf != nil {
			mark := "as rehearsed"
			if row.Observed != step.Want {
				mark = "UNEXPECTED"
			}
			r.Logf("rehearsal %s -> observed %s, expected %s (%s): %s",
				step.Fault, row.Observed, step.Want, mark, step.Why)
		}
		r.disarm(ctx)
	}
	return rows, nil
}

// one arms a fault, makes a single push, and classifies the outcome.
func (r *Rehearser) one(ctx context.Context, fault string) (string, string) {
	if err := r.arm(ctx, fault); err != nil {
		return "arm-failed", err.Error()
	}
	client, err := r.NewClient()
	if err != nil {
		return "no-client", err.Error()
	}
	// The push is a scratch event with its OWN tag, so a successful attempt here
	// can never be confused with a real detection's event, and the read-back
	// that follows is on that tag alone.
	tag := "aicap-rehearsal-" + fault
	out, err := client.SendPayload(ctx, xprotect.PushPayload{Body: rehearsalBody(tag), Tag: tag})
	if err == nil {
		switch out.State {
		case "push_sent":
			return "push_sent", out.Detail
		case "unknown":
			return "not-sent", out.Detail
		case "push_refused":
			return "push_refused", out.Detail
		default:
			return out.State, out.Detail
		}
	}
	return classifyPushError(err), err.Error()
}

func classifyPushError(err error) string {
	var auth *xprotect.ErrAuth
	if errors.As(err, &auth) {
		return "auth_failed"
	}
	var refused *xprotect.RefusedError
	if errors.As(err, &refused) {
		return "push_refused"
	}
	var retryable *xprotect.RetryableError
	if errors.As(err, &retryable) {
		return "retry"
	}
	return "other"
}

// rehearsalBody is a minimal, valid analytics event. It is not a detection and
// does not pretend to be one: it exists only to make the client do a full
// authenticate -> push -> read-back cycle against an injected fault.
func rehearsalBody(tag string) []byte {
	body := map[string]any{
		"type":     "b4d1e1c2-0000-4000-8000-000000000001",
		"source":   "cameras/0d9a1b2c-1111-2222-3333-444455556666",
		"time":     time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"datatype": "analytics",
		"tag":      tag,
		"data": map[string]any{
			"object": map[string]any{"type": "rehearsal", "value": "rehearsal", "confidence": 1.0,
				"boundingBox": map[string]int{"top": 0, "left": 0, "bottom": 0, "right": 0}},
			"rule":   map[string]any{"name": "fault-rehearsal"},
			"vendor": map[string]any{"name": "aicaptain"},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// arm asks the simulator's control surface to inject a fault on the events
// POST, and only there: a fault on every path would hit the token call and the
// rehearsal would end up measuring the wrong thing.
func (r *Rehearser) arm(ctx context.Context, fault string) error {
	payload, _ := json.Marshal(map[string]any{
		// UNLIMITED, not one call. A 401 makes the client refresh its token and
		// try again, and with a count of one that retry would go through
		// untouched — so the rehearsal would report a successful push for a
		// fault that is really a rejected credential. A real 401 storm does not
		// stop after one call, and neither does the rehearsal's.
		"fault": fault, "count": 0,
		"path": "/api/rest/v1/events", "method": "POST",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(r.BaseURL, "/")+"/__control/fault", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return fmt.Errorf("the fault could not be armed on the simulator's control surface: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the simulator refused to arm %s: %s", fault, resp.Status)
	}
	return nil
}

func (r *Rehearser) disarm(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(r.BaseURL, "/")+"/__control/reset", nil)
	if err != nil {
		return
	}
	resp, err := r.client().Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func (r *Rehearser) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// StartRehearsal runs the plan once, `after` the run has started, and returns
// the rows. It is a goroutine entry point so the run's timing is not held up by
// seven deliberate failures.
func (r *Rehearser) StartRehearsal(ctx context.Context, after time.Duration, done func([]Rehearsal)) {
	go func() {
		t := time.NewTimer(after)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rows, err := r.Run(ctx)
		if err != nil && r.Logf != nil {
			r.Logf("the rehearsal could not run: %v", err)
		}
		if done != nil {
			done(rows)
		}
	}()
}
