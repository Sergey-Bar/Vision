package calibration_test

// The corpus GENERATOR, and the window it reads.
//
// # Why the window exists
//
// A corpus of everything the install has ever seen changes every time an
// operator labels one more incident, and two such reports cannot be compared.
// A corpus over a window is a measurement of a period, and two periods can be
// compared.
//
// # What the generator skips
//
// A case whose reason chain does not carry the features the engine read is
// SKIPPED, and the skip is counted and returned. A corpus that silently dropped
// the cases it could not read would measure the easy half, and the count is
// what says so.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Sergey-Bar/vision/internal/calibration"
	"github.com/Sergey-Bar/vision/internal/dbx"
	"github.com/Sergey-Bar/vision/internal/migrate"
	"github.com/Sergey-Bar/vision/internal/testutil"
)

// theChain is a reason chain the generator can read.
//
// The engine's own record: the presence step carries the zone, the severity and
// the two durations, and the corroboration step carries the device count.
const theChain = `[{"key":"presence","evidence":"presence in a restricted zone, severity medium, 30s of a 10s threshold"},` +
	`{"key":"corroboration","evidence":"2 devices saw it"}]`

// theUnreadableChain is a chain the generator cannot read, and skips.
const theUnreadableChain = `[{"key":"presence","evidence":"something else entirely"}]`

// TestTheGeneratorReadsTheLabelledHistoryInTheWindow is the window.
func TestTheGeneratorReadsTheLabelledHistoryInTheWindow(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t)
	if _, err := (&migrate.Runner{Pool: pool}).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tenant, site, _ := testutil.NewTenant(t, pool)
	// The generator reads through the tenant-bound pool, so it needs the
	// dbx wrapper rather than the raw one.
	dbxPool := &dbx.Pool{Pool: pool}

	now := time.Now().UTC()
	inside := now.Add(-2 * time.Hour)
	outside := now.Add(-40 * 24 * time.Hour)

	seedCase(t, pool, tenant, site, "inc-inside", inside, theChain, "true_positive")
	seedCase(t, pool, tenant, site, "inc-outside", outside, theChain, "true_positive")
	seedCase(t, pool, tenant, site, "inc-unreadable", inside, theUnreadableChain, "true_positive")

	// The whole history: three incidents, two readable.
	corpus, skipped, err := calibration.GenerateCorpus(ctx, dbxPool, tenant)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(corpus.Cases) != 2 {
		t.Errorf("the corpus holds %d case(s), want 2: the unreadable chain is skipped", len(corpus.Cases))
	}
	if skipped != 1 {
		t.Errorf("the generator skipped %d case(s), want 1. A corpus that silently dropped the "+
			"cases it could not read would measure the easy half", skipped)
	}
	if corpus.Provenance == "" {
		t.Error("the corpus states no provenance, and a corpus with no stated provenance reads " +
			"as somebody's real history")
	}

	// The window: only the case inside it.
	windowed, skipped, err := calibration.GenerateCorpusInWindow(ctx, dbxPool, tenant,
		now.Add(-3*time.Hour), now)
	if err != nil {
		t.Fatalf("generate in the window: %v", err)
	}
	if len(windowed.Cases) != 1 {
		t.Errorf("the windowed corpus holds %d case(s), want 1: only the incident inside the "+
			"window belongs in it", len(windowed.Cases))
	}
	if skipped != 1 {
		t.Errorf("the windowed generator skipped %d case(s), want 1", skipped)
	}

	// A window that holds nothing holds nothing, and SAYS SO.
	//
	// The generator refuses rather than returning an empty corpus, and that is
	// the right answer: an empty corpus is a corpus of nothing, and a report
	// over it would be a report of nothing wearing a number.
	if _, _, err := calibration.GenerateCorpusInWindow(ctx, dbxPool, tenant,
		now.Add(-100*24*time.Hour), now.Add(-99*24*time.Hour)); err == nil {
		t.Error("the generator produced a corpus over a window that holds no labelled history")
	}
}

// seedCase writes one incident and the operator's disposition of it.
func seedCase(t *testing.T, pool *pgxpool.Pool, tenant, site, incidentID string, createdAt time.Time, chain, outcome string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents
			(incident_id, tenant_id, primary_site_id, rule_id, rule_bundle_version,
			 bundle_content_hash, policy_version, state, severity, title,
			 first_event_at, last_event_at, created_at, threat_reason_chain)
		VALUES ($1, $2, $3, 'rule-canary', '0.1.0', 'hash', 'p1', 'dispositioned', 'low',
		        'canary', $4, $4, $4, $5::jsonb)`,
		incidentID, tenant, site, createdAt, chain); err != nil {
		t.Fatalf("insert the incident: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO dispositions
			(disposition_id, tenant_id, incident_id, actor_id, outcome, reason_code, notes,
			 agreement, created_at)
		VALUES ($1, $2, $3, 'operator-canary', $4, 'canary', '', 'agreed', $5)`,
		"disp-"+incidentID, tenant, incidentID, outcome, createdAt); err != nil {
		t.Fatalf("insert the disposition: %v", err)
	}
}
