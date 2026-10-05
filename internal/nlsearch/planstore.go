package nlsearch

// The plan store: every question and every query, written BEFORE the query runs,
// and replayable afterwards without the model.
//
// # Why the write comes first
//
// The table exists so a reviewer can answer "what did this product actually ask,
// and what did it actually run". A row written AFTER execution answers that only
// for the runs somebody kept, and it cannot distinguish a query an operator
// APPROVED from one the gateway produced on its own — because both would be rows
// written by the same code, in the same shape, after the fact.
//
// So the order is: compile, write the row, show the plan, execute, stamp the row.
//
// # Why a REFUSED plan is stored too
//
// Because a gateway that records only what it allowed has invisible refusals, and a
// search refused sixty times in a day is a finding about the principal's SCOPE that
// nobody would ever see. The refusal row carries the statement it WOULD have run: a
// refusal row with an empty statement records that something was blocked without
// recording what, which is half an audit trail.
//
// # Why replay does not need the model
//
// A [Request] carries the compiled plan, and the plan carries the statement and the
// bound arguments. Replaying means executing those again. The model is not in that
// path, and that is the property rather than the omission: **the query a reviewer
// approved is the query a replay runs**, and a replay that re-derived the query
// would be reviewing something else.
//
// # What it will not do
//
// Replay will not return a plan whose stored digest no longer matches what it
// stores. A plan row that has been EDITED — by hand, by a restore, by a bug —
// replays as itself and is therefore worthless as evidence, so [Replay] verifies the
// digest BEFORE it hands back a statement.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNoSuchPlan is a plan id that is not in this tenant.
//
// A named error so a caller can tell "there is no such plan" from "there was one
// and the store is unreachable": opposite problems for whoever is on call, and the
// same status code otherwise.
var ErrNoSuchPlan = errors.New("nlsearch: no such plan in this tenant")

// ErrPlanEdited is a plan whose stored digest does not match its stored statement.
//
// This is the control that makes a plan row evidence rather than a claim.
var ErrPlanEdited = errors.New("nlsearch: this plan row has been edited since it was written")

// ErrPlanRefused is a replay of a plan the gateway declined.
//
// Named, because the temptation at this point is to run it anyway and the name is
// what a reviewer reading the log sees.
var ErrPlanRefused = errors.New("nlsearch: this plan was refused and no query ever ran against it")

// StoredPlan is one row of `search_plans`.
type StoredPlan struct {
	PlanID   string
	TenantID string
	SiteID   string
	ActorID  string
	SQL      string
	Args     []any
	// PlanSHA256 is what the row CLAIMS. It is verified against [PlanDigest] of SQL
	// and Args before any replay.
	PlanSHA256 string
	// RefusedReason is empty for a plan that was approved.
	RefusedReason string
	RefusalKind   string
	PlannedAt     time.Time
	ExecutedAt    time.Time
	RowCount      int
	HasRowCount   bool
}

// Approved reports whether this plan was allowed to run.
func (p StoredPlan) Approved() bool { return p.RefusedReason == "" }

// PlanStore is the surface the application supplies, exactly as `api.Deps` does for
// every other table this product owns.
//
// An interface rather than a `*dbx.Pool` so that the store-BEFORE-execute ORDER can
// be tested without a database — which is the property that actually matters and
// the one that is invisible in a passing run.
type PlanStore interface {
	// WritePlan stores the plan BEFORE it runs and returns the row.
	WritePlan(ctx context.Context, r *Request) (StoredPlan, error)
	// MarkExecuted stamps a plan that ran, with the row count it returned.
	MarkExecuted(ctx context.Context, tenantID, planID string, rows int, at time.Time) error
	// ReadPlan returns a stored plan for replay.
	ReadPlan(ctx context.Context, tenantID, planID string) (StoredPlan, error)
}

// PlanDigest is the digest a plan row is verified against on replay.
//
// The statement and the arguments are separated by a byte that cannot appear in
// either, and each argument is rendered with its TYPE. Both matter: without the
// separator `("ab","c")` and `("a","bc")` hash the same and a row edited by moving
// one argument boundary would replay as itself; without the type, the string "1" and
// the integer 1 hash the same and Postgres would coerce one into the other.
func PlanDigest(sql string, args []any) string {
	h := sha256.New()
	h.Write([]byte(sql))
	h.Write([]byte{0})
	for i, a := range args {
		if i > 0 {
			h.Write([]byte{0})
		}
		_, _ = fmt.Fprintf(h, "%T=%v", a, a)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Record writes the plan BEFORE it runs.
//
// Named [Record] and not [Save] on purpose: a record is something that happened,
// and the ordering — record, then run — is the entire property. A method called
// `Save` invites a caller to call it whenever it is convenient, and convenient is
// after.
func Record(ctx context.Context, store PlanStore, r *Request) (StoredPlan, error) {
	if store == nil {
		return StoredPlan{}, errors.New("nlsearch: no plan store, so a plan could not be " +
			"written before it ran. A plan recorded only afterwards records what happened, " +
			"which is not what the table is for")
	}
	return store.WritePlan(ctx, r)
}

// Replay returns the statement and arguments to execute for a stored plan.
//
// The digest is verified FIRST. That ordering is the whole control: a plan row that
// was edited replays as itself and is worth nothing as evidence, so this is the
// difference between "here is the query that ran" and "here is a query somebody
// wrote".
func Replay(ctx context.Context, store PlanStore, tenantID, planID string) (StoredPlan, string, []any, error) {
	if store == nil {
		return StoredPlan{}, "", nil, ErrNoSuchPlan
	}
	p, err := store.ReadPlan(ctx, tenantID, planID)
	if err != nil {
		return StoredPlan{}, "", nil, err
	}
	if got := PlanDigest(p.SQL, p.Args); got != p.PlanSHA256 {
		return p, "", nil, fmt.Errorf("%w: plan %s stores a statement digesting to %s and the "+
			"row claims %s. It was edited after it was written, and a plan that replays as "+
			"itself is not evidence of anything", ErrPlanEdited, planID, got, p.PlanSHA256)
	}
	if !p.Approved() {
		return p, "", nil, fmt.Errorf("%w: plan %s was refused (%s: %s). Running it anyway is "+
			"the defect this whole package exists to prevent",
			ErrPlanRefused, planID, p.RefusalKind, p.RefusedReason)
	}
	return p, p.SQL, p.Args, nil
}

// WritePlanJSON renders a [Request] as the three JSON columns a plan row carries:
// the structured question, the plan a reader approved, and the bound arguments.
//
// Exported because the JSON shape is part of what a reviewer reads, and a shape
// that exists only inside a writer cannot be inspected by anything else — including
// by the test that says what the writer will do with it.
func WritePlanJSON(r *Request) (structured, plan, args []byte, err error) {
	structured, err = json.Marshal(map[string]any{
		"text":  r.Question.Text,
		"sites": r.Question.Sites,
		"kinds": kindStrings(r.Plan.Kinds),
		"since": windowString(r.Question.Since),
		"until": windowString(r.Question.Until),
		"limit": r.Question.Limit,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("nlsearch: render the question as JSON: %w", err)
	}
	plan, err = json.Marshal(map[string]any{
		"tenant":         r.Plan.Tenant,
		"sites":          r.Plan.Sites,
		"kinds":          kindStrings(r.Plan.Kinds),
		"since":          windowString(r.Plan.Since),
		"until":          windowString(r.Plan.Until),
		"limit":          r.Plan.Limit,
		"matchedIndexed": r.Plan.MatchedIndexed,
		"tables":         AllowedTables,
		"rendered":       r.Plan.String(),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("nlsearch: render the plan as JSON: %w", err)
	}
	args, err = json.Marshal(r.Plan.boundArgs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("nlsearch: render the bound arguments as JSON: %w", err)
	}
	return structured, plan, args, nil
}

// kindStrings renders the kinds for JSON.
func kindStrings(kinds []SourceKind) []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// windowString renders one side of a window for JSON, and an unbounded side as an
// EMPTY STRING rather than a sentinel date.
func windowString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
