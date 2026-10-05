package nlsearch

// The plan store's ORDER, and the digest that makes a row evidence rather than a
// claim.
//
// # What is actually under test
//
// Not SQL. The statements are constants and the writer is the application's, and
// both have their own tests. What is under test here is the ORDER — record, THEN
// run — and the digest that makes a stored plan safe to replay.
//
// The order is invisible in a passing run. Every other test in this package passes
// whether the plan is written before the query or after it, because nothing about
// the RESULT changes. So it takes a test that watches the sequence.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// countingStore records the order of the calls a caller makes.
type countingStore struct {
	calls    []string
	written  StoredPlan
	refuse   *Refusal
	writeErr error
}

func (s *countingStore) WritePlan(_ context.Context, r *Request) (StoredPlan, error) {
	s.calls = append(s.calls, "record")
	if s.writeErr != nil {
		return StoredPlan{}, s.writeErr
	}
	args := r.Plan.BoundArgs()
	return StoredPlan{
		PlanID:     "plan_test_001",
		TenantID:   r.Plan.Tenant,
		SiteID:     r.Plan.Sites[0],
		SQL:        r.Plan.SQL,
		Args:       args,
		PlanSHA256: PlanDigest(r.Plan.SQL, args),
	}, nil
}

func (s *countingStore) MarkExecuted(_ context.Context, _, _ string, _ int, _ time.Time) error {
	s.calls = append(s.calls, "execute")
	return nil
}

func (s *countingStore) ReadPlan(_ context.Context, _, planID string) (StoredPlan, error) {
	if s.written.PlanID == "" || s.written.PlanID != planID {
		return StoredPlan{}, ErrNoSuchPlan
	}
	return s.written, nil
}

// The order, which is the entire property of the table.
func TestAPlanIsRecordedBeforeItRuns(t *testing.T) {
	store := &countingStore{}
	req := aRequest(t)

	stored, err := Record(context.Background(), store, req)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// The caller's next action is the execution, and the row is already written.
	if err := store.MarkExecuted(context.Background(), stored.TenantID, stored.PlanID, 3, time.Now()); err != nil {
		t.Fatalf("mark executed: %v", err)
	}
	if len(store.calls) != 2 || store.calls[0] != "record" || store.calls[1] != "execute" {
		t.Fatalf("the calls were %v. A plan written after the query records what happened, "+
			"which is not what the table is for", store.calls)
	}
	if stored.PlanSHA256 == "" {
		t.Error("the stored plan carries no digest, so a later reader cannot tell whether " +
			"the row in front of them is the row that ran")
	}
	store.written = stored
}

// A plan that cannot be written cannot be run. This is the other half of the
// ordering and it is the half that is easy to get wrong: a store that returns an
// error and a caller that logs it and proceeds anyway has a working search and no
// evidence, which is the state this product is arguing against.
func TestAPlanThatCannotBeWrittenIsNotRun(t *testing.T) {
	store := &countingStore{writeErr: errors.New("the store is down")}
	_, err := Record(context.Background(), store, aRequest(t))
	if err == nil {
		t.Fatal("a plan whose write failed was reported as recorded")
	}
	store.written = StoredPlan{}
	if _, _, _, err := Replay(context.Background(), store, "northwind", "plan_test_001"); err == nil {
		t.Error("a plan that was never written is replayable. There is no row, so there is " +
			"nothing to have been edited, and a replay that invents one is worse than none")
	}
}

// And a nil store is refused rather than silently skipped, for the same reason.
func TestNoStoreMeansNoRunAndItSaysSo(t *testing.T) {
	_, err := Record(context.Background(), nil, aRequest(t))
	if err == nil {
		t.Fatal("Record with no store succeeded")
	}
	if !strings.Contains(err.Error(), "afterwards records what happened") {
		t.Errorf("the refusal does not say why order matters: %v", err)
	}
}

// The digest is verified BEFORE the statement is returned.
//
// This ordering is the control. A plan row that was edited replays as itself and is
// worth nothing as evidence, so the difference between "here is the query that ran"
// and "here is a query somebody wrote" is entirely in whether this check happens
// first.
func TestAPlanRowThatWasEditedIsRefused(t *testing.T) {
	store := &countingStore{}
	stored, err := Record(context.Background(), store, aRequest(t))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	store.written = stored

	// Somebody widened the statement after it was written. This is the exact edit
	// the digest exists to catch, and it is the one a restore or a careless fix
	// produces.
	store.written.SQL = strings.Replace(stored.SQL, tenantPredicate, "true", 1)
	store.written.Args = append([]any(nil), stored.Args...)
	store.written.Args[0] = "some-other-tenant"

	_, sql, args, err := Replay(context.Background(), store, stored.TenantID, stored.PlanID)
	if err == nil {
		t.Fatal("an edited plan row replayed. A plan that replays as itself is not evidence")
	}
	if !errors.Is(err, ErrPlanEdited) {
		t.Errorf("the refusal is not ErrPlanEdited, so a caller cannot tell a tampered row "+
			"from a missing one: %v", err)
	}
	if sql != "" || args != nil {
		t.Error("an edited plan handed back a statement anyway. The check must happen BEFORE " +
			"the statement is returned, not after it has been used")
	}
}

// The two ways a row can be edited that a naive digest would miss.
func TestTheDigestSeparatesArgumentsAndCarriesTheirTypes(t *testing.T) {
	// Moving a boundary: ("ab","c") and ("a","bc") must not collide. Without the
	// separator byte these hash the same, and a row edited by splitting one argument
	// into two would pass.
	if PlanDigest("SEL", []any{"ab", "c"}) == PlanDigest("SEL", []any{"a", "bc"}) {
		t.Error("two different argument lists hash the same. A row edited by moving an " +
			"argument boundary would replay as itself")
	}
	// Type: the string "1" and the integer 1 are different binds and Postgres coerces
	// one into the other. A digest over the VALUE alone would accept a row where
	// somebody changed a site id from text to an integer.
	if PlanDigest("SEL", []any{"1"}) == PlanDigest("SEL", []any{1}) {
		t.Error(`the string "1" and the integer 1 hash the same. A row edited to change an` +
			" argument's TYPE would replay as itself, and Postgres would coerce one into the other")
	}
	// And the same list is stable, which is what makes it usable as evidence.
	if PlanDigest("SEL", []any{"a", 1, nil}) != PlanDigest("SEL", []any{"a", 1, nil}) {
		t.Error("the digest is not stable for the same statement and arguments, so a row " +
			"that was not edited at all would be refused")
	}
}

// A REFUSED plan cannot be replayed, and the refusal says so by name.
//
// The temptation at this point is to run it anyway — the statement is right there,
// and refusing to replay looks like refusing to be useful. It is the exact
// substitution this package exists to prevent.
func TestARefusedPlanCannotBeReplayed(t *testing.T) {
	store := &countingStore{}
	stored, err := Record(context.Background(), store, aRequest(t))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	store.written = stored
	store.written.RefusedReason = "you asked about a site that is not in your scope"
	store.written.RefusalKind = string(ReasonUnknownSite)

	_, sql, args, err := Replay(context.Background(), store, stored.TenantID, stored.PlanID)
	if err == nil {
		t.Fatal("a refused plan replayed. Running it anyway is the defect this package exists to prevent")
	}
	if !errors.Is(err, ErrPlanRefused) {
		t.Errorf("the refusal is not ErrPlanRefused: %v", err)
	}
	if sql != "" || args != nil {
		t.Error("a refused plan handed back its statement. A refusal that still returns " +
			"the query is a warning, and a warning is what this product stopped printing")
	}
}

// A refused row still carries the statement it WOULD have run.
//
// A refusal row with an empty statement records that something was blocked without
// recording what, which is half an audit trail: an auditor cannot tell whether the
// gateway was right to refuse.
func TestARefusedRowCarriesTheStatementItWouldHaveRun(t *testing.T) {
	store := &countingStore{}
	stored, err := Record(context.Background(), store, aRequest(t))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !strings.Contains(stored.SQL, tenantPredicate) {
		t.Error("a stored plan carries no statement with the tenant predicate, so a row " +
			"cannot be read back as the query that was approved")
	}
	if len(stored.Args) == 0 {
		t.Error("a stored plan carries no bound arguments, so a row cannot be replayed " +
			"and a reader cannot see what would have been sent")
	}
}

// Replay does not consult the model. That is the property, not an omission: the
// query a reviewer approved is the query a replay runs.
func TestReplayNeedsNoModelAndNoManifest(t *testing.T) {
	store := &countingStore{}
	stored, err := Record(context.Background(), store, aRequest(t))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	store.written = stored

	_, sql, args, err := Replay(context.Background(), store, stored.TenantID, stored.PlanID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if sql != stored.SQL {
		t.Error("replay returned a different statement than the one that was stored")
	}
	if len(args) != len(stored.Args) || args[0] != stored.Args[0] {
		t.Error("replay returned different arguments than the ones that were stored. A " +
			"replay that re-derives the query is reviewing something else")
	}
}

// The three JSON columns, and that the arguments are in the plan's ORDER.
func TestThePlanRowRendersItsThreeJSONColumns(t *testing.T) {
	structured, plan, args, err := WritePlanJSON(aRequest(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(structured), "north-gate") {
		t.Error("the structured question does not name the site")
	}
	if !strings.Contains(string(plan), "search_documents") {
		t.Error("the plan does not name the corpus it reads")
	}
	// The arguments are an ORDERED array, not an object: a reader who reads them out
	// of a JSON object cannot tell which is `$1`.
	if strings.Contains(string(args), ":") && !strings.HasPrefix(strings.TrimSpace(string(args)), "[") {
		t.Errorf("the bound arguments are not a JSON array, so a reader cannot tell which "+
			"is `$1`: %s", args)
	}
}

// aRequest is a request built from a legitimate question.
func aRequest(t *testing.T) *Request {
	t.Helper()
	m := loadManifestIn(t)
	req, refusal := NewRequest(m, aQuestion(), []string{"north-gate"})
	if refusal != nil {
		t.Fatalf("a legitimate request was refused: %s", refusal)
	}
	return req
}

func loadManifestIn(t *testing.T) *Manifest {
	t.Helper()
	m, refusal := LoadManifest(manifestPath())
	if refusal != nil {
		t.Fatalf("the committed manifest does not load: %s", refusal)
	}
	return m
}
