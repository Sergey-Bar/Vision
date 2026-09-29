package incidents_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/audit"
	"github.com/Sergey-Bar/Ai-Captain/internal/incidents"
	"github.com/Sergey-Bar/Ai-Captain/internal/rules"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func newService(t *testing.T) (*incidents.Service, string, string) {
	t.Helper()
	s, tenant, site := service(t)
	s.Audit = &audit.Writer{Pool: s.Pool}
	return s, tenant, site
}

func seedIncident(t *testing.T, svc *incidents.Service, tenant, site string, supporting []string) string {
	t.Helper()
	id, err := svc.CreateIncident(context.Background(), incidents.Create{
		TenantID: tenant, SiteID: site,
		Decision: rules.Decision{
			State: rules.StateCorroborated, RuleID: "core.camera-offline",
			Severity: "low", Reason: "no_corroboration_required",
			Action:           rules.Action{CreateIncident: true, Severity: "low"},
			SupportingEvents: supporting,
			DegradedReason:   rules.DegradedNone,
		},
		RuleBundleVersion: "0.1.0", BundleContentHash: strings.Repeat("d", 64),
		PolicyVersion: "p1", Title: "Camera offline",
		SupportingEventIDs: []string{"evt-1"},
	})
	if err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	return id
}

// An incident with no `corroborate` clause has nothing in `SupportingEvents`.
//
// The engine resolves such a rule to StateCorroborated with the reason
// "no_corroboration_required", and the trigger is carried in a *different* field
// â€” `Create.SupportingEventIDs` â€” which the link loop read only to decide which
// of the supporting events was the trigger. So the loop iterated an empty list
// and the incident was inserted with no `incident_events` rows at all.
//
// Replay then reads no events, `signalsFrom` returns a zero-valued trigger, and
// `reevaluate` runs the rule against an empty event type. The replay mismatch
// was structural: the incident had never recorded what it was about.
func TestAnIncidentAlwaysLinksAtLeastItsTrigger(t *testing.T) {
	svc, tenant, site := newService(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name        string
		supporting  []string
		expectRoles []string
	}{
		{"no corroboration clause at all", nil, []string{"trigger"}},
		{"a corroborated decision", []string{"evt-2"}, []string{"trigger", "supporting"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := seedIncident(t, svc, tenant, site, tc.supporting)

			// The role lives on the join, not on the event view: `Detail.Events` is
			// the event, and whether the incident claims it as the trigger or as
			// support is the incident's claim about it.
			rows, err := testutil.Pool(t).Query(ctx, `
				SELECT event_id, role FROM incident_events
				 WHERE tenant_id = $1 AND incident_id = $2`, tenant, id)
			if err != nil {
				t.Fatalf("read the linked events: %v", err)
			}
			roles := map[string]bool{}
			for rows.Next() {
				var evID, role string
				if err := rows.Scan(&evID, &role); err != nil {
					t.Fatalf("scan: %v", err)
				}
				roles[role] = true
			}
			rows.Close()
			if len(roles) == 0 {
				t.Fatal("the incident has no linked events at all. The trigger is carried in " +
					"a different field from the supporting list, and only the supporting list " +
					"was walked, so a decision with no corroboration produced an incident " +
					"with no evidence in it")
			}
			for _, want := range tc.expectRoles {
				if !roles[want] {
					t.Errorf("no event has the role %q; roles are %v", want, roles)
				}
			}
		})
	}
}

// A state change is not a creation.
//
// `SetState` recorded `audit.ActionIncidentCreated` for every lifecycle
// transition, with the from and to states in the state-hash fields. A reader
// filtering the ledger on "incident created" saw N+1 creations for one incident,
// and the rows that were not creations were indistinguishable from the one that
// was. An append-only ledger is worth something precisely because two different
// events are two different rows.
func TestALifecycleTransitionIsNotAuditedAsACreation(t *testing.T) {
	svc, tenant, site := newService(t)
	ctx := context.Background()

	id := seedIncident(t, svc, tenant, site, []string{"evt-1"})
	if err := svc.SetState(ctx, tenant, id, incidents.StateInReview, "op-a", "taking a look"); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	var creations, rows int
	if err := testutil.Pool(t).QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE action = $2), count(*)
		  FROM audit_ledger
		 WHERE tenant_id = $1 AND target_type = 'incident' AND target_id = $3`,
		tenant, string(audit.ActionIncidentCreated), id).
		Scan(&creations, &rows); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if creations != 1 {
		t.Errorf("the ledger records %d creation(s) of incident %s, want exactly 1. Every "+
			"lifecycle transition was written as a creation, so a reader filtering on "+
			"'incident created' sees N+1 creations for one incident", creations, id)
	}
	if rows < 2 {
		t.Errorf("the ledger has %d row(s) for this incident, want the creation and at least "+
			"one transition. The transition is being lost rather than mislabelled", rows)
	}
}

// The read, the check and the write are one statement.
//
// The state was read on a separate connection outside the transaction, checked
// against a lifecycle table that is advisory, and written with no guard on the
// state it was read from.
//
// Sequentially the old code was already right: `lifecycleAllows` refuses a move
// out of `closed` on its own. What the missing guard left open is two writers
// interleaving. A reads `open` and asks for `in_review`; B closes; A's update
// lands after B's. `COALESCE($closed, closed_at)` preserves the close timestamp,
// so the incident ends up dispositioned-as-open and closed at once, and nothing
// in the ledger records that the two updates crossed.
//
// So the test races them, and asserts the one shape a reader cannot act on.
func TestAConcurrentCloseAndReviewCannotProduceAHalfClosedIncident(t *testing.T) {
	svc, tenant, site := newService(t)
	ctx := context.Background()

	for attempt := 0; attempt < 20; attempt++ {
		id := seedIncident(t, svc, tenant, site, []string{"evt-1"})

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = svc.SetState(ctx, tenant, id, incidents.StateClosed, "op-b", "resolved")
		}()
		go func() {
			defer wg.Done()
			_ = svc.SetState(ctx, tenant, id, incidents.StateInReview, "op-a", "taking a look")
		}()
		wg.Wait()

		detail, err := svc.Get(ctx, tenant, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		var closedAt *time.Time
		if err := testutil.Pool(t).QueryRow(ctx,
			`SELECT closed_at FROM incidents WHERE tenant_id = $1 AND incident_id = $2`,
			tenant, id).Scan(&closedAt); err != nil {
			t.Fatalf("read closed_at: %v", err)
		}

		// The two writes are in different transactions, so the final state is
		// whichever landed last, and that is legitimate. What must not happen is a
		// state that contradicts the close timestamp: `in_review` alongside a
		// `closed_at` is an incident that says it is being worked and is finished
		// at the same time.
		if string(detail.State) == string(incidents.StateInReview) && (closedAt != nil) {
			t.Fatalf("attempt %d: the incident is %q and closed at %s. A stale writer "+
				"overwrote the state after the close and the close timestamp was "+
				"preserved. The update is guarded on the state it was read from, so the "+
				"loser of a race cannot land", attempt, detail.State, *closedAt)
		}
	}
}
