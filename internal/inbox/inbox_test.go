package inbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Sergey-Bar/Ai-Captain/internal/dbx"
	"github.com/Sergey-Bar/Ai-Captain/internal/inbox"
	"github.com/Sergey-Bar/Ai-Captain/internal/migrate"
	"github.com/Sergey-Bar/Ai-Captain/internal/testutil"
)

func open(t *testing.T) *dbx.Pool {
	t.Helper()
	raw := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: raw}).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := dbx.DefaultConfig()
	if err := cfg.ApplyDSN(os.Getenv(testutil.DatabaseURLEnv)); err != nil {
		t.Fatalf("apply DSN: %v", err)
	}
	pool, err := dbx.Open(context.Background(), cfg, os.Getenv("SP_TEST_DATABASE_PASSWORD"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func optsFor(tenant, site, integration, sourceID string, receivedAt time.Time) inbox.WriteOptions {
	return inbox.WriteOptions{
		TenantID:      tenant,
		SiteID:        site,
		IntegrationID: integration,
		SourceEventID: sourceID,
		Payload:       json.RawMessage(`{"Id":"` + sourceID + `","Type":"Motion"}`),
		ReceivedAt:    receivedAt,
	}
}

// ---------------------------------------------------------------------------
// O4: Accepted.EventID.
// ---------------------------------------------------------------------------

// EventID is documented as the inbox row's identity, "present on both paths so a
// caller can log a correlation id for a duplicate too". It was assigned on
// neither path: the fresh insert built Accepted{Status, ReceivedAt} and both
// duplicate returns built Accepted{Duplicate, Status}, so the field was always
// the empty string and a caller that trusted the doc and logged it logged nothing.
func TestAFreshWriteCarriesTheEventIdentity(t *testing.T) {
	pool := open(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)

	accepted, err := w.Write(context.Background(),
		optsFor(tenant, site, "int-1", "src-1", time.Now().UTC()))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if accepted.Duplicate {
		t.Fatal("the first write was reported as a duplicate")
	}
	if accepted.EventID == "" {
		t.Error("Accepted.EventID is empty on the fresh-insert path. The field is documented as " +
			"the row's identity and was never assigned on any path")
	}
	want := fmt.Sprintf("%s/int-1/src-1", tenant)
	if accepted.EventID != want {
		t.Errorf("EventID is %q, want %q. The identity has to be the scoped natural key, which "+
			"is what identifies the row", accepted.EventID, want)
	}
}

// The duplicate path is the one the field's comment names, and the one that was
// broken. A source re-delivering after a reconnect is the normal case, and it is
// the case where a correlation id is most useful: the caller is holding a second
// copy of an id it has already seen and needs to tie the two together.
func TestADuplicateWriteCarriesTheEventIdentity(t *testing.T) {
	pool := open(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)
	opts := optsFor(tenant, site, "int-1", "src-1", time.Now().UTC())

	first, err := w.Write(context.Background(), opts)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	second, err := w.Write(context.Background(), opts)
	if err != nil {
		t.Fatalf("duplicate write returned an error rather than a no-op: %v", err)
	}
	if !second.Duplicate {
		t.Fatal("the second write was not recognised as a duplicate")
	}
	if second.EventID == "" {
		t.Fatal("Accepted.EventID is empty on the duplicate path. This is the path the field's " +
			"own comment names, and a caller correlating a duplicate has nothing to correlate with")
	}
	if second.EventID != first.EventID {
		t.Errorf("the duplicate reports identity %q and the original %q. They are the same row",
			second.EventID, first.EventID)
	}
	// The identity must name the row that exists, not the request that arrived.
	// A duplicate read one month later still has to name the row that was written
	// a month ago.
	if !second.ReceivedAt.Equal(first.ReceivedAt) {
		t.Errorf("the duplicate reports received_at %s and the original %s. received_at is the "+
			"partition the row landed in, and it is read back rather than echoed from the "+
			"request for exactly this case", second.ReceivedAt, first.ReceivedAt)
	}
}

// The identity has to be the row's, not the request's. Writing a second event
// under a different source id and a different integration must produce a
// different identity; otherwise the field is echoing the request and a log line
// built from it correlates two unrelated events.
func TestDistinctRowsGetDistinctIdentities(t *testing.T) {
	pool := open(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)
	now := time.Now().UTC()

	a, err := w.Write(context.Background(), optsFor(tenant, site, "int-1", "src-1", now))
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	b, err := w.Write(context.Background(), optsFor(tenant, site, "int-2", "src-1", now))
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	c, err := w.Write(context.Background(), optsFor(tenant, site, "int-1", "src-2", now))
	if err != nil {
		t.Fatalf("third write: %v", err)
	}
	if a.EventID == b.EventID || a.EventID == c.EventID || b.EventID == c.EventID {
		t.Errorf("three distinct rows report identities %q, %q and %q. At least two of them "+
			"are the same string, so the identity does not identify the row", a.EventID, b.EventID, c.EventID)
	}
}

// ---------------------------------------------------------------------------
// O5: the dead-letter list.
// ---------------------------------------------------------------------------

// The console's dead-letter list is what the "visible within 60 seconds of
// entering the state" SLO is measured against, and it ordered by received_at —
// ingest order, not dead-letter order.
//
// The two events below are ordered against each other on purpose: the one that
// was ingested LAST is the one that died FIRST, so under ingest ordering it comes
// second and the operator looking for the event that just broke sees the wrong one
// at the top. Under dead-letter ordering it comes first, which is the answer to
// the question the page is opened to ask.
func TestTheDeadLetterListIsOrderedByWhenTheEventDied(t *testing.T) {
	pool := open(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)
	ctx := context.Background()

	// ingestedNewer is the event a source delivered most recently. It is dead
	// lettered with an OLDER completed_at, so it must sort below the other one.
	const ingestedNewer, ingestedOlder = "src-ingested-newer", "src-ingested-older"
	ingestOld := time.Now().UTC().Add(-72 * time.Hour)
	ingestNew := time.Now().UTC().Add(-1 * time.Hour)

	for _, o := range []inbox.WriteOptions{
		optsFor(tenant, site, "int-1", ingestedOlder, ingestOld),
		optsFor(tenant, site, "int-1", ingestedNewer, ingestNew),
	} {
		if _, err := w.Write(ctx, o); err != nil {
			t.Fatalf("write %s: %v", o.SourceEventID, err)
		}
	}

	// ingestedNewer died first; ingestedOlder died most recently.
	testutil.Exec(t, pool.Pool, `
		UPDATE inbox_events
		   SET status = 'dead_letter', completed_at = $3::timestamptz,
		       dead_letter_reason = 'first to die'
		 WHERE tenant_id = $1 AND source_event_id = $2`,
		tenant, ingestedNewer, time.Now().UTC().Add(-2*time.Hour))
	testutil.Exec(t, pool.Pool, `
		UPDATE inbox_events
		   SET status = 'dead_letter', completed_at = $3::timestamptz,
		       dead_letter_reason = 'died most recently'
		 WHERE tenant_id = $1 AND source_event_id = $2`,
		tenant, ingestedOlder, time.Now().UTC().Add(-1*time.Minute))

	letters, err := w.DeadLetters(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 2 {
		t.Fatalf("the operator sees %d dead letters, want 2", len(letters))
	}
	if letters[0].SourceEventID != ingestedOlder {
		t.Errorf("the top of the dead-letter list is %q and the second is %q. %q was ingested "+
			"two days before %q but died a minute ago, while %q died two hours ago, so the "+
			"answer to \"what just died\" is %q and the list does not have it at the top",
			letters[0].SourceEventID, letters[1].SourceEventID, ingestedOlder, ingestedNewer,
			ingestedNewer, ingestedOlder)
	}
}

// UpdatedAt was scanned from received_at, which is not an update time in any
// sense. For an event that spent a long time being retried the two are far
// apart, and a console column labelled "updated" showing the ingest instant tells
// an operator the age of the event rather than the age of the incident.
func TestTheDeadLetterUpdatedAtIsTheCompletionInstantNotTheIngestInstant(t *testing.T) {
	pool := open(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)
	ctx := context.Background()

	ingested := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if _, err := w.Write(ctx, optsFor(tenant, site, "int-1", "src-old", ingested)); err != nil {
		t.Fatalf("write: %v", err)
	}
	died := time.Now().UTC().Add(-2 * time.Hour)
	testutil.Exec(t, pool.Pool, `
		UPDATE inbox_events
		   SET status = 'dead_letter', completed_at = $2::timestamptz,
		       dead_letter_reason = 'a month of retries'
		 WHERE tenant_id = $1 AND source_event_id = 'src-old'`, tenant, died)

	letters, err := w.DeadLetters(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d dead letters, want 1", len(letters))
	}
	got := letters[0]
	if got.UpdatedAt.Equal(ingested) {
		t.Errorf("UpdatedAt is %s, which is the ingest instant (received_at). The field is "+
			"labelled as an update time and is populated from the moment the event arrived, "+
			"which is not the same fact in any sense", got.UpdatedAt)
	}
	if diff := got.UpdatedAt.Sub(died.UTC()); diff > time.Minute || diff < -time.Minute {
		t.Errorf("UpdatedAt is %s and the row's completed_at is %s. UpdatedAt has to be the "+
			"instant the event entered the dead-letter state", got.UpdatedAt, died.UTC())
	}
	if got.FirstSeen.IsZero() {
		t.Error("FirstSeen is zero; the listing lost the field that says when the event was " +
			"first seen, which is a different question from when it died")
	}
	if got.Reason != "a month of retries" {
		t.Errorf("the reason is %q, want the row's dead_letter_reason", got.Reason)
	}
}

// A dead letter that reached the state through the ordinary retry path — Fail,
// after the retry budget is spent — has to carry a completion instant too, or it
// sorts as newest under DESC and heads the operator's list regardless of when it
// died. This drives the real path rather than writing the row directly.
func TestADeadLetterReachedThroughRetryCarriesACompletionInstant(t *testing.T) {
	pool := open(t)
	tenant, site, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)
	ctx := context.Background()

	before := time.Now().UTC().Add(-time.Second)
	if _, err := w.Write(ctx, optsFor(tenant, site, "int-1", "src-retry", time.Now().UTC())); err != nil {
		t.Fatalf("write: %v", err)
	}
	key := inbox.Key{TenantID: tenant, IntegrationID: "int-1", SourceEventID: "src-retry"}

	// One attempt, with the bound at one, is enough to dead-letter.
	claimed, err := w.ClaimFor(ctx, tenant, "worker-1", 10, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d event(s), want 1", len(claimed))
	}
	status, err := w.Fail(ctx, key, "worker-1", fmt.Errorf("the normalizer is down"), 1)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if status != inbox.StatusDeadLetter {
		t.Fatalf("Fail returned %q against a bound of 1, want dead_letter", status)
	}

	letters, err := w.DeadLetters(ctx, tenant, 10)
	if err != nil {
		t.Fatalf("DeadLetters: %v", err)
	}
	if len(letters) != 1 {
		t.Fatalf("the operator sees %d dead letters, want 1", len(letters))
	}
	if letters[0].UpdatedAt.IsZero() {
		t.Error("a dead letter that reached the state through Fail has no completion instant. " +
			"The console list orders by completed_at, and a NULL sorts first under DESC, so " +
			"this row heads the list no matter when it died")
	}
	if letters[0].UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt is %s, which is before the attempt that dead-lettered the event", letters[0].UpdatedAt)
	}
	if letters[0].Reason == "" {
		t.Error("the dead letter has no reason; a dead letter with no reason is an incident " +
			"nobody can act on")
	}
}

// The listing is a console and an alert surface, so an empty list has to be
// genuinely empty rather than a query that matched nothing because the tenant
// context was missing. The database returns no rows for a tenant-scoped table
// read without a context rather than raising, which is how a backlog reads as
// empty on every site at once.
func TestTheDeadLetterListIsScopedToItsTenant(t *testing.T) {
	pool := open(t)
	tenantA, siteA, _ := testutil.NewTenant(t, testutil.Pool(t))
	tenantB, _, _ := testutil.NewTenant(t, testutil.Pool(t))
	w := inbox.NewWriter(pool)
	ctx := context.Background()

	if _, err := w.Write(ctx, optsFor(tenantA, siteA, "int-1", "src-1", time.Now().UTC())); err != nil {
		t.Fatalf("write: %v", err)
	}
	testutil.Exec(t, pool.Pool, `
		UPDATE inbox_events SET status = 'dead_letter', completed_at = now(),
		       dead_letter_reason = 'x' WHERE tenant_id = $1`, tenantA)

	if letters, err := w.DeadLetters(ctx, tenantB, 10); err != nil {
		t.Fatalf("DeadLetters for the other tenant: %v", err)
	} else if len(letters) != 0 {
		t.Errorf("tenant B sees %d of tenant A's dead letters. The listing is not tenant-scoped, "+
			"so one tenant's incident is on another tenant's console", len(letters))
	}
	if letters, err := w.DeadLetters(ctx, tenantA, 10); err != nil {
		t.Fatalf("DeadLetters: %v", err)
	} else if len(letters) != 1 {
		t.Errorf("tenant A sees %d dead letters, want 1", len(letters))
	}
}
